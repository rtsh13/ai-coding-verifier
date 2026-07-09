// Package dockercli is a thin wrapper over the Docker Go SDK, pointed at
// Podman's Docker-compatible API socket. It is the lowest layer of the verifier
// stack: every container operation (create, exec, copy, kill, remove) goes
// through here. M0 establishes connectivity only; the full container API lands
// in M1.
package dockercli

import (
	"context"

	"github.com/docker/docker/client"
)

// Client wraps a Docker/Podman API client.
type Client struct {
	api *client.Client
}

// New connects to the container runtime. It honours the standard DOCKER_HOST
// environment variable (set it to the Podman socket, e.g.
// unix:///.../podman-machine-default-api.sock — see the `podman-env` Makefile
// target), and negotiates the API version so it works against Podman's slightly
// different supported version set.
func New() (*Client, error) {
	api, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{api: api}, nil
}

// Ping verifies the runtime is reachable and responding.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.api.Ping(ctx)
	return err
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	return c.api.Close()
}
