// Package sandbox runs a single job inside an already-running container: it
// stages the job's files, executes the command under an optional wall-clock TTL,
// and captures stdout/stderr/exit. The TTL is enforced inside the container so
// an overrun kills only the job process, leaving the container reusable by the
// pool.
package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aicv/internal/dockercli"
)

// Container is the minimal handle sandbox needs from the pool. Keeping it an
// interface avoids importing the pool package (and the resulting import cycle).
type Container interface {
	ID() string
}

// ExecSpec describes one job to run.
type ExecSpec struct {
	Files   map[string][]byte // relative paths -> contents, written under WorkDir
	WorkDir string
	Cmd     []string
	Env     []string
	TTL     time.Duration // 0 = no limit
}

// ExecResult is the captured outcome of a run.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	Duration time.Duration
}

// Run stages the job's files into the container, runs the command under the
// spec's TTL, and returns the captured result.
func Run(ctx context.Context, cli *dockercli.Client, c Container, spec ExecSpec) (ExecResult, error) {
	id := c.ID()

	if spec.WorkDir != "" {
		out, err := cli.Exec(ctx, id, dockercli.ExecConfig{Cmd: []string{"mkdir", "-p", spec.WorkDir}})
		if err != nil {
			return ExecResult{}, fmt.Errorf("prepare workdir: %w", err)
		}
		if out.ExitCode != 0 {
			return ExecResult{}, fmt.Errorf("prepare workdir %q: exit %d: %s",
				spec.WorkDir, out.ExitCode, strings.TrimSpace(out.Stderr))
		}
	}
	if len(spec.Files) > 0 {
		dest := spec.WorkDir
		if dest == "" {
			dest = "/"
		}
		if err := cli.CopyIn(ctx, id, dest, spec.Files); err != nil {
			return ExecResult{}, fmt.Errorf("stage files: %w", err)
		}
	}

	cmd, ttlSecs := wrapWithTimeout(spec.Cmd, spec.TTL)

	execCtx := ctx
	if ttlSecs > 0 {
		// A grace window beyond the in-container limit so the API call can't hang
		// if the runtime is slow to report the kill.
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(ttlSecs)*time.Second+10*time.Second)
		defer cancel()
	}

	start := time.Now()
	out, err := cli.Exec(execCtx, id, dockercli.ExecConfig{Cmd: cmd, WorkDir: spec.WorkDir, Env: spec.Env})
	dur := time.Since(start)
	if err != nil {
		return ExecResult{Duration: dur}, fmt.Errorf("exec: %w", err)
	}

	return ExecResult{
		Stdout:   out.Stdout,
		Stderr:   out.Stderr,
		ExitCode: out.ExitCode,
		TimedOut: timedOut(out.ExitCode, ttlSecs, dur),
		Duration: dur,
	}, nil
}
