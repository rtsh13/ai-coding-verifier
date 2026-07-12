//go:build integration

package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
)

const testImage = "rust-sandbox"

type fakeContainer struct{ id string }

func (f fakeContainer) ID() string { return f.id }

// newContainer creates and starts an offline sleeper container to run jobs in,
// registering cleanup. Skips if podman is unreachable.
func newContainer(t *testing.T) (*dockercli.Client, Container) {
	t.Helper()
	c, err := dockercli.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(pctx); err != nil {
		t.Skipf("podman not reachable (set DOCKER_HOST): %v", err)
	}
	id, err := c.Create(context.Background(), dockercli.CreateConfig{
		Image:   testImage,
		Cmd:     []string{"sleep", "2147483647"},
		Network: "none",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := c.Start(context.Background(), id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Remove(context.Background(), id)
		_ = c.Close()
	})
	return c, fakeContainer{id: id}
}

func TestRun_StagesFilesAndCaptures(t *testing.T) {
	cli, ct := newContainer(t)
	res, err := Run(context.Background(), cli, ct, ExecSpec{
		Files:   map[string][]byte{"hello.txt": []byte("payload")},
		WorkDir: "/tmp/work",
		Cmd:     []string{"cat", "/tmp/work/hello.txt"},
		TTL:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "payload" {
		t.Errorf("stdout = %q, want \"payload\"", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
	if res.TimedOut {
		t.Errorf("unexpected TimedOut")
	}
	if res.Duration <= 0 {
		t.Errorf("duration not measured")
	}
}

func TestRun_ExitCodeAndStderr(t *testing.T) {
	cli, ct := newContainer(t)
	res, err := Run(context.Background(), cli, ct, ExecSpec{
		WorkDir: "/tmp/work",
		Cmd:     []string{"sh", "-c", "echo out; echo err 1>&2; exit 2"},
		TTL:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 2 {
		t.Errorf("exit = %d, want 2", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "out") || strings.Contains(res.Stdout, "err") {
		t.Errorf("stdout = %q, want only out", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "err") {
		t.Errorf("stderr = %q, want err", res.Stderr)
	}
}

func TestRun_TTLKills_ContainerStaysUsable(t *testing.T) {
	cli, ct := newContainer(t)
	res, err := Run(context.Background(), cli, ct, ExecSpec{
		WorkDir: "/tmp/work",
		Cmd:     []string{"sleep", "30"},
		TTL:     1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("want TimedOut; got exit=%d dur=%s", res.ExitCode, res.Duration)
	}
	if res.Duration > 5*time.Second {
		t.Errorf("timeout took too long: %s", res.Duration)
	}

	// The job TTL must kill only the job, not the container — it stays reusable.
	res2, err := Run(context.Background(), cli, ct, ExecSpec{
		WorkDir: "/tmp/work",
		Cmd:     []string{"echo", "alive"},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run after ttl: %v", err)
	}
	if strings.TrimSpace(res2.Stdout) != "alive" {
		t.Errorf("container not reusable after TTL kill: %q", res2.Stdout)
	}
}
