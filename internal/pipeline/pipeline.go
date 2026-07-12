// Package pipeline runs a submission through two independently-attributed
// stages: compile, then (only if compilation succeeds) execute its tests. The
// separation is the point — a compile-time failure and a runtime failure carry
// different meaning, and the pipeline records which stage decided the outcome.
package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/aicv/internal/dockercli"
	"github.com/aicv/internal/pool"
	"github.com/aicv/internal/sandbox"
)

// Job is one submission to compile and test. Files is a complete project (for
// Rust: Cargo.toml + src/*.rs).
type Job struct {
	Lang  Lang
	Files map[string][]byte
	TTL   time.Duration
}

// Run compiles the submission and, if it compiles, runs its tests, returning a
// Result with independent compile/execute attribution. It borrows a warm
// container from the pool for the duration and returns it afterwards.
func Run(ctx context.Context, p *pool.Pool, cli *dockercli.Client, job Job) (Result, error) {
	c, err := p.Acquire(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire container: %w", err)
	}
	defer p.Release(c)

	// Isolated per-job dir so a reused container carries no state between jobs.
	workDir := "/tmp/job-" + randID()
	defer func() {
		_, _ = cli.Exec(context.Background(), c.ID(), dockercli.ExecConfig{Cmd: []string{"rm", "-rf", workDir}})
	}()

	// Stage 1: compile. This also compiles the test harness, so a test that
	// fails to *compile* is attributed here, not to execution.
	compileRes, err := sandbox.Run(ctx, cli, c, sandbox.ExecSpec{
		Files:   job.Files,
		WorkDir: workDir,
		Cmd:     compileCommand(job.Lang),
		TTL:     job.TTL,
	})
	if err != nil {
		return Result{}, fmt.Errorf("compile stage: %w", err)
	}

	res := Result{
		Stage:        StageCompile,
		CompilerJSON: compileRes.Stdout,
		CompilerRaw:  compileRes.Stderr,
		TimedOut:     compileRes.TimedOut,
		Duration:     compileRes.Duration,
	}
	if compileRes.TimedOut || compileRes.ExitCode != 0 {
		return res, nil // did not compile; execution never runs
	}
	res.Compiled = true

	// Stage 2: execute the (already-built) tests.
	execRes, err := sandbox.Run(ctx, cli, c, sandbox.ExecSpec{
		WorkDir: workDir,
		Cmd:     executeCommand(job.Lang),
		TTL:     job.TTL,
	})
	if err != nil {
		return Result{}, fmt.Errorf("execute stage: %w", err)
	}
	res.Stage = StageExecute
	res.RuntimeStdout = execRes.Stdout
	res.RuntimeStderr = execRes.Stderr
	res.ExitCode = execRes.ExitCode
	res.TimedOut = execRes.TimedOut
	res.Crashed = isCrash(execRes.ExitCode)
	res.Passed = execRes.ExitCode == 0 && !execRes.TimedOut
	res.Duration = compileRes.Duration + execRes.Duration
	return res, nil
}

// randID returns a short random hex string for a per-job working directory.
func randID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
