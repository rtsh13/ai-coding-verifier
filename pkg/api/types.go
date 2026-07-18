// Package api is the public entry point to the verifier: construct an Env, then
// call Verify(job) to compile-run-verify a submission and get back a Verdict.
// It owns the container pool and the TTL reaper; there is no model and no
// training loop involved.
package api

import (
	"time"

	"github.com/aicv/internal/pipeline"
	"github.com/aicv/internal/verdict"
	"github.com/aicv/internal/verifier"
)

// Job and Lang are re-exported from the pipeline so callers depend only on this
// package.
type Job = pipeline.Job
type Lang = pipeline.Lang

const (
	Rust = pipeline.Rust
	Go   = pipeline.Go
)

// Verdict is the verifier's answer for one submission.
type Verdict struct {
	Outcome        verdict.Outcome
	Diagnostics    []verifier.Diagnostic
	CompilerOutput string // raw, human-readable compiler stderr
	Stdout         string // runtime stdout (empty if it never ran)
	Stderr         string // runtime stderr
	ExitCode       int
	TimedOut       bool
	Duration       time.Duration // total compile + execute time
	Assignment     time.Duration // time to acquire a warm container (S1)
	Compile        time.Duration
	Execute        time.Duration
}

// Config configures an Env.
type Config struct {
	Image               string        // sandbox image, e.g. "rust-sandbox"
	MinWarm             int           // containers to pre-warm
	MaxSize             int           // hard cap on live containers
	MaxJobsPerContainer int           // recycle after N jobs (0 = unlimited)
	MemBytes            int64         // per-container memory cap
	NanoCPUs            int64         // per-container CPU cap
	PidsLimit           int64         // per-container process cap (bounds fork bombs)
	SeccompProfilePath  string        // path to a custom seccomp profile; empty = runtime default
	SweepInterval       time.Duration // reaper sweep cadence (default 5s)
	ReaperGrace         time.Duration // extra time beyond a job's TTL before reaping (default 30s)
}
