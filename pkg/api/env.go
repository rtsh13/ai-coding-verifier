package api

import (
	"context"
	"fmt"
	"time"

	"github.com/aicv/internal/dockercli"
	"github.com/aicv/internal/pipeline"
	"github.com/aicv/internal/pool"
	"github.com/aicv/internal/ttl"
	"github.com/aicv/internal/verdict"
	"github.com/aicv/internal/verifier"
)

const (
	defaultSweep = 5 * time.Second
	defaultGrace = 30 * time.Second
)

// Env is the running verifier: a warm container pool plus the TTL reaper that
// reclaims hung jobs. Build one with NewEnv, call Verify, and Close it when done.
type Env struct {
	cli    *dockercli.Client
	pool   *pool.Pool
	reaper *ttl.Manager
	cancel context.CancelFunc
	grace  time.Duration
}

// NewEnv connects to the container runtime, pre-warms the pool, and starts the
// TTL reaper (wired to force-remove any container whose job hangs past deadline).
func NewEnv(cfg Config) (*Env, error) {
	cli, err := dockercli.New()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	p, err := pool.New(cli, pool.Config{
		Image:               cfg.Image,
		MinWarm:             cfg.MinWarm,
		MaxSize:             cfg.MaxSize,
		MaxJobsPerContainer: cfg.MaxJobsPerContainer,
		MemBytes:            cfg.MemBytes,
		NanoCPUs:            cfg.NanoCPUs,
		PidsLimit:           cfg.PidsLimit,
	})
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("pool: %w", err)
	}

	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = defaultSweep
	}
	grace := cfg.ReaperGrace
	if grace <= 0 {
		grace = defaultGrace
	}

	// The reaper's kill = force-remove the hung container from the pool, which
	// frees its slot so a fresh replacement can be created.
	reaper := ttl.New(func(id string) error {
		p.RemoveByID(id)
		return nil
	}, sweep)

	ctx, cancel := context.WithCancel(context.Background())
	reaper.Start(ctx)

	return &Env{cli: cli, pool: p, reaper: reaper, cancel: cancel, grace: grace}, nil
}

// Verify compiles and tests one submission and returns its Verdict.
func (e *Env) Verify(ctx context.Context, job Job) (Verdict, error) {
	res, err := pipeline.Run(ctx, e.pool, e.cli, job, pipeline.WithReaper(e.reaper, e.grace))
	if err != nil {
		return Verdict{}, err
	}
	return Verdict{
		Outcome:        verdict.Classify(res),
		Diagnostics:    diagnosticsFor(job.Lang, res),
		CompilerOutput: res.CompilerRaw,
		Stdout:         res.RuntimeStdout,
		Stderr:         res.RuntimeStderr,
		ExitCode:       res.ExitCode,
		TimedOut:       res.TimedOut,
		Duration:       res.Duration,
		Assignment:     res.Assignment,
		Compile:        res.Compile,
		Execute:        res.Execute,
	}, nil
}

// Close stops the reaper, removes every container, and closes the client. It
// waits for the reaper goroutine to finish first so an in-flight reap can't run
// against an already-closed pool or client.
func (e *Env) Close(ctx context.Context) error {
	e.cancel()      // signal the reaper to stop
	e.reaper.Wait() // and wait for any in-flight reap to complete
	err := e.pool.Close(ctx)
	_ = e.cli.Close()
	return err
}

// diagnosticsFor parses whichever compiler output the language produced.
func diagnosticsFor(lang Lang, res pipeline.Result) []verifier.Diagnostic {
	var diags []verifier.Diagnostic
	switch lang {
	case pipeline.Go:
		diags, _ = verifier.ParseGo(res.CompilerRaw)
	default:
		diags, _ = verifier.ParseRust(res.CompilerJSON)
	}
	return diags
}
