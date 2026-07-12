//go:build integration

package dockercli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// testImage is the Rust sandbox image (primary language). Alpine-based, so it
// has sh/echo/cat/ls/sleep from busybox.
const testImage = "rust-sandbox"

// testClient returns a connected client, skipping the test if Podman is
// unreachable (so `go test ./...` without podman doesn't hard-fail).
func testClient(t *testing.T) *Client {
	t.Helper()
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("podman not reachable (set DOCKER_HOST): %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// startSleeper creates and starts an offline, long-lived container to exec into,
// registering its removal for cleanup.
func startSleeper(t *testing.T, c *Client) string {
	t.Helper()
	ctx := context.Background()
	id, err := c.Create(ctx, CreateConfig{
		Image:   testImage,
		Cmd:     []string{"sleep", "2147483647"}, // busybox sleep wants a number
		Network: "none",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = c.Remove(context.Background(), id) })
	if err := c.Start(ctx, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id
}

func TestExec_EchoAndExitCode(t *testing.T) {
	c := testClient(t)
	id := startSleeper(t, c)
	ctx := context.Background()

	out, err := c.Exec(ctx, id, ExecConfig{Cmd: []string{"echo", "hello"}})
	if err != nil {
		t.Fatalf("Exec echo: %v", err)
	}
	if strings.TrimSpace(out.Stdout) != "hello" {
		t.Errorf("stdout = %q, want \"hello\"", out.Stdout)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", out.ExitCode)
	}

	out3, err := c.Exec(ctx, id, ExecConfig{Cmd: []string{"sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatalf("Exec exit3: %v", err)
	}
	if out3.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", out3.ExitCode)
	}
}

func TestExec_StdoutStderrSeparate(t *testing.T) {
	c := testClient(t)
	id := startSleeper(t, c)

	out, err := c.Exec(context.Background(), id, ExecConfig{
		Cmd: []string{"sh", "-c", "echo to-out; echo to-err 1>&2"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(out.Stdout, "to-out") || strings.Contains(out.Stdout, "to-err") {
		t.Errorf("stdout = %q, want only to-out", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "to-err") {
		t.Errorf("stderr = %q, want to-err", out.Stderr)
	}
}

func TestCopyIn_AndRead(t *testing.T) {
	c := testClient(t)
	id := startSleeper(t, c)
	ctx := context.Background()

	if err := c.CopyIn(ctx, id, "/tmp", map[string][]byte{
		"greeting.txt": []byte("hi there"),
	}); err != nil {
		t.Fatalf("CopyIn: %v", err)
	}

	out, err := c.Exec(ctx, id, ExecConfig{Cmd: []string{"cat", "/tmp/greeting.txt"}})
	if err != nil {
		t.Fatalf("Exec cat: %v", err)
	}
	if out.Stdout != "hi there" {
		t.Errorf("file contents = %q, want \"hi there\"", out.Stdout)
	}
}

func TestNetwork_None_OnlyLoopback(t *testing.T) {
	c := testClient(t)
	id := startSleeper(t, c)

	// With --network none, the container has only the loopback interface; no
	// eth0 exists. This is a tool-independent proof of network isolation.
	out, err := c.Exec(context.Background(), id, ExecConfig{Cmd: []string{"ls", "/sys/class/net"}})
	if err != nil {
		t.Fatalf("Exec ls: %v", err)
	}
	ifaces := strings.Fields(out.Stdout)
	if !contains(ifaces, "lo") {
		t.Errorf("interfaces = %v, want loopback present", ifaces)
	}
	if contains(ifaces, "eth0") {
		t.Errorf("interfaces = %v, want NO eth0 (network should be isolated)", ifaces)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
