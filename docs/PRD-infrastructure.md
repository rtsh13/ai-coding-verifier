# PRD: AI Coding Verifier — Infrastructure Build & Test

**Status:** active build spec · **Scope:** pure infrastructure (no ML/RL/training)
**Supersedes:** `docs/prd.md` (the RL/GRPO build guide — shelved).

> This document is the single source of truth for **finishing and testing the
> verifier infrastructure**. It ignores the dissertation entirely. Goal: a
> working, measured system.

---

## 1. What we are building (one paragraph)

A **sandboxed compile-execute-verify service for compiled languages** (Rust
primary, Go second). Given a code submission for a problem, it compiles it, runs
its tests inside an isolated offline container, and returns a **structured
verdict** (passed / compile-error / runtime-error / test-failed, plus compiler
diagnostics and timings). It is **fast** (warm-container assignment < 2s, full
pipeline < 30s), **lightweight** (minimal offline images), **secure**
(seccomp/gVisor, no network), and **self-cleaning** (TTL + GC, no leaks). The
system is driven by a CLI/library API — there is no model and no training loop.

## 2. Success criteria (these ARE the evaluation)

Straight from the project brief (slide "P1: AI Coding Verifier"):

| # | Property | Measurable target |
|---|----------|-------------------|
| S1 | **Fast — assignment** | warm container assigned in **< 2s** (p95) |
| S2 | **Fast — pipeline** | full compile-execute-verify in **< 30s** (p95) |
| S3 | **Lightweight** | minimal images; report size + dep count; Rust image is currently 5.18 GB — reducing it is a target |
| S4 | **Auto-GC** | after N=1000 jobs, container count + memory return to baseline; TTL terminates overruns |
| S5 | **Secure** | ≥ 95% of adversarial corpus contained; no host impact; runs `--network none` |
| S6 | **Correct** | on the dataset workload: canonical solutions → pass, buggy solutions → fail; report false-positive / false-negative rate |
| S7 | **Cache effective** | warm/cached runs materially faster than cold/uncached (report speedup) |

## 3. Current state (verified 2026-07-09)

**Done and real:**
- Offline images built: `aicv/go-sandbox` (984 MB), `rust-sandbox` (5.18 GB).
- Dependency mining + prevalence selection (Python, `scripts/mining/`).
- Dataset: `task_suite/train_combined.jsonl` (540), `heldout_combined.jsonl` (105) — Rust problems with prompt/solution/tests fields.
- `internal/verifier/` — cargo/rustc JSON → structured `Diagnostic` (built, tested; **uncommitted** in working tree).
- `internal/reward/` — 4-way `classify()` outcome logic (built, tested; the RL *scalar* part will be dropped, the classification kept).
- `internal/pipeline/result.go` — `Result`/`Stage`/`Lang` data types (built; uncommitted).
- `Makefile` — `smoke` (Go) and `smoke_rust` targets prove the images compile/run offline via podman.

**Partial / not real yet:**
- `images/go/seccomp.json` and `images/go/Dockerfile`-referenced profiles are **empty 0-byte files** — security is unimplemented.
- Adversarial corpus (`testdata/adversarial/*.go`) — **empty stubs**.
- Go image `go.sum` not committed (reproducible-rebuild gap).

**Not started (empty stubs):**
- `internal/dockercli`, `internal/sandbox`, `internal/pool`, `internal/ttl`,
  `internal/pipeline` (compile/execute/orchestration), `pkg/api`.
- No CLI to drive the system. No evaluation harness. `go.mod` has no deps.

## 4. Architecture

```
   Submission (Job)                                         Verdict
        │                                                      ▲
        ▼                                                      │
   ┌─────────────────────────  pkg/api (Verify) ─────────────────────┐
   │                                                                 │
   │   pool.Acquire ──► sandbox.Run ──► pipeline (compile ─► execute)│
   │      ▲  │                              │                        │
   │      │  │                              ▼                        │
   │   ttl (TTL + GC sweep)            Result ─► verifier ─► verdict  │
   │      │                                (diagnostics)  (classify) │
   │   dockercli (podman SDK: create/exec/copy/kill/remove)          │
   └─────────────────────────────────────────────────────────────────┘
                         all containers: --network none, seccomp, mem/cpu caps
```

**Data flow:** a `Job` enters `pkg/api.Verify` → `pool` hands back a warm
container → `pipeline` copies files in via `sandbox`, runs `cargo build`
(compile stage), and if that succeeds runs `cargo test` (execute stage),
producing a `Result` → `verifier` parses the compiler JSON into diagnostics →
`verdict` classifies the outcome → a `Verdict` is returned. `ttl` enforces
wall-clock limits and GC reclaims containers.

## 5. Core data model (the contracts every module shares)

```go
// input
type Lang int; const ( Rust Lang = iota; Go )
type Job struct {
    ID    string
    Lang  Lang
    Files map[string][]byte // e.g. "src/main.rs", "Cargo.toml", test files
    TTL   time.Duration
}

// internal outcome of a run (already exists: internal/pipeline/result.go)
type Stage int; const ( StageCompile Stage = iota; StageExecute )
type Result struct {
    Stage Stage; Compiled, Passed, TimedOut, Crashed bool
    CompilerRaw, CompilerJSON, RuntimeStdout, RuntimeStderr string
    ExitCode int; Duration time.Duration
}

// public verdict (the system's answer)
type Outcome int
const ( Passed Outcome = iota; CompileError; RuntimeError; TestFailed )
type Timings struct { Assignment, Compile, Execute, Total time.Duration }
type Verdict struct {
    Outcome     Outcome
    Diagnostics []verifier.Diagnostic
    Stdout, Stderr string
    ExitCode    int
    Timings     Timings
    TimedOut    bool
}
```

## 6. Build plan (dependency-ordered; each ends with a testable "done")

Integration tests are gated `//go:build integration` (need podman + images);
unit tests run bare. TDD throughout. `-race` on anything concurrent.

### M0 · Preflight (½ day)
- Add podman Docker SDK to `go.mod`: `github.com/docker/docker`.
- Confirm `podman system service` reachable; document socket in a `Makefile` target.
- Commit the already-built verifier/reward/result working-tree code (once you okay a commit).
- **Done:** `go build ./internal/verifier ./internal/reward ./internal/pipeline` clean; `podman info` OK from a tiny Go probe.

### M1 · `dockercli` — podman wrapper (2–3 days)
- **Files:** `internal/dockercli/client.go` (+ integration test).
- **API:** `New()`, `Create(ctx, CreateConfig)`, `Start`, `Exec(ctx,id,ExecConfig)→ExecOutput`, `CopyIn(ctx,id,dir,files)`, `Kill`, `Remove`. `CreateConfig` includes `Network:"none"`, `MemBytes`, `NanoCPUs`, `SeccompProfilePath`.
- **Done:** create `aicv/go-sandbox` with `--network none`, copy a file in, `Exec cat` returns it, `Exec` of exit-3 returns `ExitCode 3`, `wget` to any host fails, `Remove` deletes it. `go test -tags=integration -race ./internal/dockercli`.

### M2 · `sandbox` — run a job, capture output (3–4 days)
- **Files:** `internal/sandbox/sandbox.go`, `capture.go` (+ integration test).
- **API:** `Run(ctx, cli, container, ExecSpec)→ExecResult`. `ExecSpec{Files,WorkDir,Cmd,Env,TTL}`; `ExecResult{Stdout,Stderr,ExitCode,TimedOut,Duration}`.
- **Done:** files land under WorkDir; a >TTL command returns `TimedOut` and is killed (container still usable); stdout/stderr captured separately.

### M3 · `pool` — warm LRU container pool (4–6 days, hardest)
- **Files:** `internal/pool/pool.go`, `container.go`, `state.go` (+ unit test with fake backend, + integration test).
- **API:** `New(Backend,Config)`, `Acquire(ctx)→*Container`, `Release(*Container)`, `Close(ctx)`. States IDLE/BUSY/OVERFLOW. `Container.ID()` satisfies `sandbox.Container`.
- **Done:** pre-warms `MinWarm`; under concurrent Acquire/Release, live containers never exceed `MaxSize` and are **reused** (create count flat once warm); at capacity Acquire blocks/errs by policy; `Close` removes all. `-race` clean.

### M4 · `ttl` — TTL + GC sweep (2–3 days)
- **Files:** `internal/ttl/manager.go` (+ unit test, fake clock).
- **API:** `New(kill,sweep)`, `Track(id,ttl)`, `Untrack(id)`, `Start(ctx)`.
- **Done:** overrun job killed on next sweep; `Untrack` cancels; sweep goroutine stops on ctx cancel. `-race` clean.

### M5 · `pipeline` — compile → execute with attribution (3–4 days)
- **Files:** `internal/pipeline/compile.go`, `execute.go`, `pipeline.go` (+ integration test). `result.go` exists.
- **API:** `Run(ctx, pool, cli, Job)→Result`. Rust: `cargo build --message-format=json --offline` then `cargo test --offline`; Go analog.
- **Done:** non-compiling Rust → `Stage=Compile, Compiled=false, CompilerJSON≠"", RuntimeStdout=""`; compiles-but-fails-tests → `Stage=Execute, Compiled=true, Passed=false`; passing → `Passed=true`. Sets `Crashed` on panic/signal. Smoke fixtures pass through.

### M6 · `verifier` — DONE ✅
- Keep as-is. Consumed by the verdict + surfaced in `Verdict.Diagnostics`.

### M7 · `verdict` — classify Result → Outcome (½ day)
- **Files:** refactor `internal/reward/` → `internal/verdict/` (or keep package, rename type). Keep `classify()` (4-way); **delete the RL scalar + Mode/RewardResult**.
- **API:** `Classify(Result)→Outcome`.
- **Done:** the four outcomes map correctly; existing classify tests pass (minus scalar tests).

### M8 · `pkg/api` — the public verifier API (2–3 days)
- **Files:** `pkg/api/env.go`, `types.go`.
- **API:** `NewEnv(Config)`, `Verify(ctx, Job)→Verdict`, `Close(ctx)`. Assembles pool+pipeline+verifier+verdict; fills `Timings`.
- **Done:** a canonical Rust solution from `heldout_combined.jsonl` → `Passed`; a broken one → correct error outcome + populated diagnostics; timings populated.

### M9 · `cmd/aicv` — CLI to drive the system (1–2 days)
- **Files:** `cmd/aicv/main.go`. Subcommands: `verify <file...>` (single job), `bench <jsonl>` (run a dataset file, emit per-job verdict + timing as JSON/CSV).
- **Done:** `aicv verify` on a known-good and known-bad file prints the right verdict; `aicv bench task_suite/heldout_combined.jsonl` runs the whole set and writes a results file.

### M10 · Security — real seccomp + adversarial corpus (2–3 days)
- **Files:** real `images/*/seccomp.json`; fill `testdata/adversarial/` (fork bomb, fs-escape outside `/work`, network egress) for Rust + Go; wire `SeccompProfilePath` through M1; containment integration test.
- **Done:** each adversarial submission is contained (blocked/terminated, host unaffected, bounded failure Verdict); seccomp denies a blocked syscall; containment ratio computed.

## 7. Test & evaluation plan (first-class — this is the deliverable's proof)

Build a harness (`cmd/aicv bench` + scripts) that produces each number:

| ID | Measures (crit.) | Method |
|----|------------------|--------|
| **E1 Correctness** (S6) | false pos/neg | Run canonical solutions (expect Pass) + `buggy_solution` variants (expect fail) from the dataset; tabulate. **Prereq:** confirm each record carries `{prompt, canonical_solution, tests}` and build a "known-answer" workload file. |
| **E2 Latency** (S1,S2) | assignment p50/p95, pipeline p50/p95 | Instrument `Timings`; run the 105-problem workload N times; histogram vs <2s / <30s. |
| **E3 Throughput / pool** (S1) | jobs/sec, queue depth, reuse rate | Fire C concurrent clients at `Verify`; record pool container count, reuse count, wait times. |
| **E4 Cache** (S7) | cold vs warm speedup | Same job: fresh container vs warm (populated `cargo`/`GOCACHE`) — report ratio; deps baked vs would-be-fetched. |
| **E5 Auto-GC / leak** (S4) | steady-state containers + RSS | Run 1000 jobs; assert container count + memory return to baseline; TTL-overrun job is reclaimed. |
| **E6 Security** (S5) | % contained | Run adversarial corpus with seccomp on; count contained; compare seccomp off; note gVisor option. |
| **E7 Lightweight** (S3) | image size, dep count | Report `podman images` sizes, baked dep counts; identify Rust-image reduction opportunities (5.18 GB is large — vendored crates). |

Each E-test emits a machine-readable results file so numbers are reproducible.

## 8. Sequence & rough timeline (solo)

```
Wk1: M0, M1, M2            (podman wrapper + sandbox exec)
Wk2: M3, M4               (pool + ttl — the concurrency core)
Wk3: M5, M7, M8, M9       (pipeline + verdict + API + CLI → end-to-end works)
Wk4: M10 + E1,E2          (security + correctness/latency numbers)
Wk5: E3–E7               (throughput, cache, leak, security, lightweight) + writeup of results
```

**End-to-end "it works" milestone (end Wk3):** `aicv verify` compiles and
verdicts a real Rust submission through a warm pool. **Fully-measured milestone
(end Wk5):** all of S1–S7 have numbers.

## 9. Risks & open decisions

- **Podman-on-macOS quirks** (socket, `--network none`, seccomp support in the podman VM) — validate early in M1; gVisor may be Linux-only (note as host-runtime option, test on Linux if available).
- **Rust image size (5.18 GB)** — vendored crates dominate; consider trimming the crate set or a registry-mirror approach; report honestly under S3.
- **Dataset fields for E1** — confirm `canonical_solution` + `tests` are present per record before relying on it as a known-answer workload.
- **`go.sum` reproducibility** — commit the Go image's `go.sum` (M0/M10).
- **Cache semantics in the pool** — decide whether jobs share a container's compile cache (fast, but cross-job state) or use per-job dirs on a warm toolchain (isolated); default: isolated `/work` dir per job, shared read-only toolchain + dep cache.

## 10. Prerequisites

Podman running (`podman system service`), both images present (they are), Go
1.25, host `cargo`/`rustc` for capturing fixtures. No GPU. No Python beyond the
existing dataset scripts.
