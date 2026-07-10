//go:build integration

package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
	"github.com/aicv/internal/sandbox"
)

const testImage = "rust-sandbox"

func TestPool_RealContainers_PrewarmReuseClose(t *testing.T) {
	cli, err := dockercli.New()
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cli.Ping(pctx); err != nil {
		t.Skipf("podman not reachable (set DOCKER_HOST): %v", err)
	}
	ctx := context.Background()

	p, err := New(cli, Config{Image: testImage, MinWarm: 2, MaxSize: 3})
	if err != nil {
		t.Fatalf("New pool: %v", err)
	}
	defer p.Close(ctx)

	if s := p.Stats(); s.Idle != 2 || s.Total != 2 {
		t.Errorf("after prewarm: stats=%+v, want Idle 2 Total 2", s)
	}

	// A warm acquire is just a channel read — no container creation — so it must
	// be far under the 2s assignment target.
	t0 := time.Now()
	c1, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Errorf("warm acquire took %s, want < 100ms", d)
	}

	// The acquired container really runs jobs.
	res, err := sandbox.Run(ctx, cli, c1, sandbox.ExecSpec{
		WorkDir: "/tmp/w",
		Cmd:     []string{"echo", "hi"},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("sandbox.Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hi" {
		t.Errorf("job stdout = %q, want hi", res.Stdout)
	}

	// Release then re-acquire reuses a warm container rather than creating one:
	// the pool still holds only its two pre-warmed containers (idle is FIFO, so
	// the id may differ — reuse means "no growth", not "same one back").
	p.Release(c1)
	c2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire (reuse): %v", err)
	}
	if s := p.Stats(); s.Total != 2 {
		t.Errorf("after reuse: Total=%d, want 2 (reused, not recreated)", s.Total)
	}
	p.Release(c2)

	// Close tears everything down; further acquires are refused.
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := p.Acquire(ctx); err != ErrClosed {
		t.Errorf("Acquire after Close = %v, want ErrClosed", err)
	}
}
