//go:build integration

package api

import (
	"context"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
	"github.com/aicv/internal/verdict"
)

func newTestEnv(t *testing.T, cfg Config) *Env {
	t.Helper()
	// Probe podman first so a missing runtime skips rather than hard-fails.
	probe, err := dockercli.New()
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := probe.Ping(pctx); err != nil {
		_ = probe.Close()
		t.Skipf("podman not reachable (set DOCKER_HOST): %v", err)
	}
	_ = probe.Close()

	if cfg.Image == "" {
		cfg.Image = "rust-sandbox"
	}
	e, err := NewEnv(cfg)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

func rustJob(body string) Job {
	return Job{
		Lang: Rust,
		Files: map[string][]byte{
			"Cargo.toml":  []byte("[package]\nname = \"sol\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"),
			"src/main.rs": []byte(body),
		},
		TTL: 90 * time.Second,
	}
}

const passing = `pub fn add(a: i32, b: i32) -> i32 { a + b }
fn main() {}
#[cfg(test)]
mod tests { use super::*; #[test] fn t() { assert_eq!(add(2, 2), 4); } }
`

const compileErr = `fn main() { let _x: i32 = "hello"; }`

const testFail = `pub fn add(a: i32, b: i32) -> i32 { a + b }
fn main() {}
#[cfg(test)]
mod tests { use super::*; #[test] fn t() { assert_eq!(add(2, 2), 5); } }
`

func TestVerify_Passing(t *testing.T) {
	e := newTestEnv(t, Config{MinWarm: 1, MaxSize: 2})
	v, err := e.Verify(context.Background(), rustJob(passing))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if v.Outcome != verdict.Passed {
		t.Errorf("Outcome = %s, want passed (compiler: %s)", v.Outcome, v.CompilerOutput)
	}
	if v.Duration <= 0 {
		t.Errorf("Duration not measured")
	}
}

func TestVerify_CompileError_HasDiagnostics(t *testing.T) {
	e := newTestEnv(t, Config{MinWarm: 1, MaxSize: 2})
	v, err := e.Verify(context.Background(), rustJob(compileErr))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if v.Outcome != verdict.CompileError {
		t.Errorf("Outcome = %s, want compile_error", v.Outcome)
	}
	var found bool
	for _, d := range v.Diagnostics {
		if d.Code == "E0308" {
			found = true
		}
	}
	if !found {
		t.Errorf("want an E0308 diagnostic; got %d", len(v.Diagnostics))
	}
}

func TestVerify_TestFailure(t *testing.T) {
	e := newTestEnv(t, Config{MinWarm: 1, MaxSize: 2})
	v, err := e.Verify(context.Background(), rustJob(testFail))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if v.Outcome != verdict.TestFailed {
		t.Errorf("Outcome = %s, want test_failed", v.Outcome)
	}
}

// TestReaper_RemovesUntrackedContainer exercises the ttl wiring end to end without
// needing a genuinely wedged job: it acquires a real container, registers it with
// the reaper on a tiny deadline and never untracks it (simulating a hang), and
// asserts the reaper force-removes it from the pool.
func TestReaper_RemovesUntrackedContainer(t *testing.T) {
	e := newTestEnv(t, Config{MinWarm: 1, MaxSize: 2, SweepInterval: 200 * time.Millisecond})
	ctx := context.Background()

	c, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if s := e.pool.Stats(); s.Total != 1 {
		t.Fatalf("Total = %d, want 1", s.Total)
	}

	// Simulate a hung job: tracked, but never untracked, with a short deadline.
	e.reaper.Track(c.ID(), 100*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if e.pool.Stats().Total == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s := e.pool.Stats(); s.Total != 0 {
		t.Fatalf("reaper did not remove the container; Total = %d", s.Total)
	}

	// The job's deferred Release now runs on the reaped container — must be a no-op.
	e.pool.Release(c)
	if s := e.pool.Stats(); s.Idle != 0 || s.Total != 0 {
		t.Errorf("after reaped Release: stats=%+v, want all zero", s)
	}
}
