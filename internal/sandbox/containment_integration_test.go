//go:build integration

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aicv/internal/dockercli"
)

// advPidsLimit caps processes per container so a fork bomb cannot exhaust the
// host's process table.
const advPidsLimit = 64

// newHardenedContainer creates an offline container with the full containment
// stack applied (no network, non-root image user, pids cap, runtime default
// seccomp).
func newHardenedContainer(t *testing.T) (*dockercli.Client, Container) {
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
		Image:     testImage,
		Cmd:       []string{"sleep", "2147483647"},
		Network:   "none",
		PidsLimit: advPidsLimit,
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

// newSeccompContainer creates a container with the full containment stack AND
// the project's custom seccomp whitelist applied, so a test can prove the
// profile — not some other control — is what blocks a given syscall.
func newSeccompContainer(t *testing.T) (*dockercli.Client, Container) {
	t.Helper()
	profile, err := filepath.Abs(filepath.Join("..", "..", "images", "rust", "seccomp.json"))
	if err != nil {
		t.Fatalf("resolve profile path: %v", err)
	}
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
		Image:              testImage,
		Cmd:                []string{"sleep", "2147483647"},
		Network:            "none",
		PidsLimit:          advPidsLimit,
		SeccompProfilePath: profile,
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

func loadScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "adversarial", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func runAdversarial(t *testing.T, script string, ttl time.Duration) ExecResult {
	t.Helper()
	cli, ct := newHardenedContainer(t)
	res, err := Run(context.Background(), cli, ct, ExecSpec{
		Cmd:     []string{"sh", "-c", script},
		WorkDir: "/tmp/adv",
		TTL:     ttl,
	})
	if err != nil {
		t.Fatalf("Run adversarial: %v", err)
	}
	return res
}

func TestContainment_NetworkEgressBlocked(t *testing.T) {
	res := runAdversarial(t, loadScript(t, "net_escape.sh"), 10*time.Second)
	if strings.Contains(res.Stdout, "REACHED_NETWORK") {
		t.Errorf("network egress NOT contained: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "BLOCKED") {
		t.Errorf("expected BLOCKED; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestContainment_FilesystemTamperBlocked(t *testing.T) {
	res := runAdversarial(t, loadScript(t, "fs_escape.sh"), 10*time.Second)
	if strings.Contains(res.Stdout, "WROTE_SYSTEM_FILE") {
		t.Errorf("filesystem tamper NOT contained: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "BLOCKED") {
		t.Errorf("expected BLOCKED; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestContainment_SeccompBlocksUnshare(t *testing.T) {
	cli, ct := newSeccompContainer(t)
	res, err := Run(context.Background(), cli, ct, ExecSpec{
		Cmd:     []string{"sh", "-c", loadScript(t, "syscall_blocked.sh")},
		WorkDir: "/tmp/adv",
		TTL:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run adversarial: %v", err)
	}
	if strings.Contains(res.Stdout, "REACHED_UNSHARE") {
		t.Errorf("unshare NOT contained by seccomp: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "BLOCKED") {
		t.Errorf("expected BLOCKED; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
}

func TestContainment_ForkBombBounded(t *testing.T) {
	res := runAdversarial(t, loadScript(t, "fork_bomb.sh"), 3*time.Second)
	// Contained by the per-container process limit: fork starts failing
	// ("can't fork") long before the host's process table is exhausted, so the
	// attempt ends bounded rather than running unbounded. If the limit somehow
	// didn't bite, the TTL is the backstop.
	if res.Duration > 5*time.Second {
		t.Errorf("fork bomb ran too long (%s) — not contained", res.Duration)
	}
	if !strings.Contains(res.Stderr, "can't fork") && !res.TimedOut {
		t.Errorf("no evidence of containment; exit=%d dur=%s stderr=%q",
			res.ExitCode, res.Duration, res.Stderr)
	}
	t.Logf("fork bomb contained: exit=%d timedOut=%v dur=%s", res.ExitCode, res.TimedOut, res.Duration)
	// The test process (host) is still running — the bomb did not take the
	// machine or the pool down.
}
