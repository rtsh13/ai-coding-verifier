// Package dockercli is a thin wrapper over the Docker Go SDK, pointed at
// Podman's Docker-compatible API socket. It is the lowest layer of the verifier
// stack: every container operation (create, exec, copy, kill, remove) goes
// through here.
package dockercli

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Client wraps a Docker/Podman API client.
type Client struct {
	api *client.Client
}

// New connects to the container runtime. It honours the standard DOCKER_HOST
// environment variable (set it to the Podman socket, e.g.
// unix:///.../podman-machine-default-api.sock — see the `podman-host` Makefile
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

// CreateConfig describes a container to create. Pooled sandbox containers are
// created with a long-lived Cmd (e.g. sleep) so the pool can Exec many jobs into
// them without paying container start-up cost each time.
type CreateConfig struct {
	Image     string
	Cmd       []string // container process; use a sleep loop for pooled containers
	Env       []string
	Network   string // "none" for offline isolation
	MemBytes  int64  // 0 = unlimited
	NanoCPUs  int64  // 0 = unlimited; 1 CPU = 1_000_000_000
	PidsLimit int64  // max processes/threads; 0 = unlimited. Bounds fork bombs.
	WorkDir   string
	// SeccompProfileJSON, when non-empty, is applied as the container's seccomp
	// profile (the JSON itself, not a path). When empty the runtime's default
	// profile still applies.
	SeccompProfileJSON string
}

// Create creates a container (does not start it) and returns its id.
func (c *Client) Create(ctx context.Context, cfg CreateConfig) (string, error) {
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(cfg.Network),
		Resources: container.Resources{
			Memory:   cfg.MemBytes,
			NanoCPUs: cfg.NanoCPUs,
		},
	}
	if cfg.PidsLimit > 0 {
		limit := cfg.PidsLimit
		hostCfg.Resources.PidsLimit = &limit
	}
	if cfg.SeccompProfileJSON != "" {
		hostCfg.SecurityOpt = []string{"seccomp=" + cfg.SeccompProfileJSON}
	}
	// Clear any image ENTRYPOINT so Cmd runs directly (pooled containers just
	// need to stay alive, e.g. `sleep infinity`).
	resp, err := c.api.ContainerCreate(ctx,
		&container.Config{
			Image:      cfg.Image,
			Cmd:        cfg.Cmd,
			Entrypoint: []string{},
			Env:        cfg.Env,
			WorkingDir: cfg.WorkDir,
		},
		hostCfg, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	return resp.ID, nil
}

// Start starts a created container.
func (c *Client) Start(ctx context.Context, id string) error {
	if err := c.api.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container %s: %w", id, err)
	}
	return nil
}

// ExecConfig describes a command to run inside a running container.
type ExecConfig struct {
	Cmd     []string
	WorkDir string
	Env     []string
}

// ExecOutput is the captured result of an Exec.
type ExecOutput struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command inside a running container and captures stdout, stderr,
// and the exit code. stdout and stderr are demultiplexed independently.
func (c *Client) Exec(ctx context.Context, id string, cfg ExecConfig) (ExecOutput, error) {
	created, err := c.api.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          cfg.Cmd,
		WorkingDir:   cfg.WorkDir,
		Env:          cfg.Env,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return ExecOutput{}, fmt.Errorf("exec create: %w", err)
	}

	attach, err := c.api.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return ExecOutput{}, fmt.Errorf("exec attach: %w", err)
	}
	defer attach.Close()

	var outBuf, errBuf bytes.Buffer
	// Podman/Docker multiplex stdout+stderr on one stream; StdCopy splits them.
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, attach.Reader); err != nil {
		return ExecOutput{}, fmt.Errorf("exec read: %w", err)
	}

	inspect, err := c.api.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return ExecOutput{}, fmt.Errorf("exec inspect: %w", err)
	}

	return ExecOutput{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		ExitCode: inspect.ExitCode,
	}, nil
}

// CopyIn writes files into a running container under destDir. Keys are paths
// relative to destDir (e.g. "src/main.rs"); destDir must already exist.
func (c *Client) CopyIn(ctx context.Context, id, destDir string, files map[string][]byte) error {
	tarball, err := buildTar(files)
	if err != nil {
		return fmt.Errorf("build tar: %w", err)
	}
	if err := c.api.CopyToContainer(ctx, id, destDir, tarball, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy to container %s:%s: %w", id, destDir, err)
	}
	return nil
}

// Kill sends SIGKILL to a container.
func (c *Client) Kill(ctx context.Context, id string) error {
	if err := c.api.ContainerKill(ctx, id, "KILL"); err != nil {
		return fmt.Errorf("kill container %s: %w", id, err)
	}
	return nil
}

// Remove deletes a container. A plain force-remove first sends SIGTERM and waits
// the runtime's default 10s stop grace before SIGKILL — far too slow for GC and
// pool teardown. So we SIGKILL first (ignoring "not running" for already-stopped
// containers), making removal near-instant.
func (c *Client) Remove(ctx context.Context, id string) error {
	_ = c.Kill(ctx, id) // best-effort: harmless if already stopped
	if err := c.api.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}

// buildTar packs files into a tar archive for CopyToContainer.
func buildTar(files map[string][]byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}
