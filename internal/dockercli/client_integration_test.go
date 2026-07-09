//go:build integration

package dockercli

import (
	"context"
	"testing"
	"time"
)

// TestPing is the M0 preflight probe: confirm the Go SDK can reach the Podman
// runtime. Requires `podman system service` running and DOCKER_HOST pointed at
// its socket (see the `podman-env` Makefile target).
func TestPing(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("Ping: could not reach the container runtime: %v", err)
	}
}
