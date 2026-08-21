# ai-coding-verifier

**A research prototype of AI Coding Verification Infrastructure for compiled languages.**

This repository is the working prototype behind the dissertation *AI Coding
Verifier for Compiled Languages*. Given an untrusted code submission for a
problem, the service compiles it, runs its tests inside an isolated, offline,
resource-capped container, and returns a **structured verdict** that names both
the outcome (passed, compile error, runtime error, or test failure) and the
stage that produced it, alongside the compiler's diagnostics and per-stage
timings.

Primary language of evaluation is **Rust**, chosen for the richness of its
compiler diagnostics; **Go** is retained as the plainer contrast those
diagnostics are measured against.

## Background and motivation

A growing share of new code is written by language models, yet they are still
evaluated and trained as if coding were a single pass from a prompt to an answer.
Recent work (InterCode, StepCoder) has started to let a model run its code, read
the errors, and try again, but only for **interpreted** languages (Python, SQL,
Bash) where a program runs directly with no separate build to clear first.

**Compiled** languages have been left out, even though their compilers produce a
far richer signal than the pass-or-fail outcome that prior work depends on. In a
compiled language nothing runs until the compiler and linker accept the whole
program, so a submission passes through distinct compilation and linking stages
before any runtime behaviour is observable, and each stage emits its own
diagnostics. Rust's compiler in particular emits machine-readable JSON
diagnostics with documented error codes, exact source spans, and
machine-applicable fixes — a much stronger signal than a coarse test outcome.

Admitting compiled languages is an **infrastructure problem before it is a
machine-learning one**. The intended consumer is an automated loop that runs
whatever a model emits, so every submission is untrusted and must build and
execute in strict isolation with no network; it issues work faster than per-job
environment creation could keep up with, so environments must be prepared ahead
and reused; and it acts on verdicts without human review, so the output must be
trustworthy enough that wrongly accepting a broken submission is treated as worse
than rejecting a correct one.

This prototype builds and evaluates that missing execution layer. It does **not**
train a model or run the learning loop above it — there is no model, no reward,
and no training loop in scope.

## Requirements the system is built to meet

The design is organised around four requirements, and the evaluation judges the
system against them:

| | Requirement | What it means |
|---|-------------|---------------|
| **R1** | **Containment** | An untrusted submission runs where it cannot reach the network, gain privileges, or disturb the host — even when it actively tries to. |
| **R2** | **Structured verdict** | A result names the stage at which a submission failed and preserves the compiler's diagnostics, rather than collapsing to a single value. |
| **R3** | **Bounded cost** | The time and resources spent per submission stay low enough for continuous, high-volume use. |
| **R4** | **Trustworthy verdict** | The verdict is reliable enough to act on without a human checking it first. |

## Architecture

The system is a layered pipeline: a trusted host surrounds a single *untrusted*
region — the sandboxed container in which the submission runs. The boundary is
crossed only by a resource-bounded job descending in and captured evidence
returning out.

```
   Submission (Job)                                         Verdict
        │                                                      ▲
        ▼                                                      │
   ┌──────────────────────────  pkg/api (Verify)  ───────────────────┐
   │   pool.Acquire ─► pipeline (compile ─► execute) ─► verifier ─► verdict
   │      │                        │                 (diagnostics) (classify)
   │   ttl (deadline + reclaim)  Result
   │   dockercli (container runtime: create/exec/copy/kill/remove)    │
   └─────────────────────────────────────────────────────────────────┘
        untrusted container: --network none · seccomp · non-root · pid/mem caps
```

- **Entry point** — one call, `Env.Verify(job)`, decouples callers from the stack beneath.
- **Orchestration** (bounded cost, R3) — draws a warm container from the **pool**, drives the two-stage **pipeline**, and enforces a per-job deadline with background reclamation (**ttl**).
- **Isolation** (containment, R1) — the submission runs only inside the sandboxed container, confined by disabled networking, a non-root user, a process cap, a seccomp system-call whitelist, and a deadline.
- **Verifier** (structured verdict + trust, R2/R4) — decides the outcome *outside* the sandbox from captured evidence alone, so the untrusted process never reports its own result; parses the compiler output into structured diagnostics and classifies the outcome.

The two-stage pipeline compiles the tests without running them
(`cargo test --no-run`) so that a test whose own code fails to compile is charged
to the compile stage, then runs the compiled tests. A compile failure returns
immediately with empty runtime output; a process killed by a signal is marked
crashed rather than merely failed. This **stage attribution** is what separates
the design from the interpreted-language frameworks it builds on.

### Repository layout

| Path | Role |
|------|------|
| `pkg/api/` | Public API — `NewEnv(Config)`, `Verify(ctx, Job) → Verdict`, `Close`. |
| `cmd/aicv/` | CLI driver: `verify`, `bench`, `gen-bench`. |
| `internal/dockercli/` | Thin wrapper over the container runtime (create/exec/copy/kill/remove). |
| `internal/pool/` | Warm, capacity-bounded container pool with reuse and recycling. |
| `internal/ttl/` | Per-job deadline enforcement and background reclamation of overruns. |
| `internal/pipeline/` | Two-stage compile → execute orchestration and the `Result` model. |
| `internal/sandbox/` | Staging files in and capturing stdout/stderr/exit inside a container. |
| `internal/verifier/` | Rust/Go compiler output → structured `Diagnostic`. |
| `internal/verdict/` | Classifies a `Result` into a four-way `Outcome`. |
| `internal/dataset/` | Task-suite records → known-answer verification cases. |
| `images/go/`, `images/rust/` | Offline sandbox image definitions (Dockerfile, seccomp profile, vendored deps). |
| `task_suite/` | Rust workload (HumanEval-X, MultiPL-E) with prompts, solutions, and tests. |
| `testdata/adversarial/` | Containment corpus: network egress, filesystem tampering, fork bomb, namespace escape. |
| `docs/` | PRD, ADRs, engineering record, and the evaluation data. |
| `scripts/` | Dependency mining and task-suite build scripts. |

## Prerequisites

- **Go 1.25+**
- A container runtime exposing a Docker-compatible API socket. Development uses
  **Podman** on macOS (`podman machine start`). The Go SDK talks to it via
  `DOCKER_HOST`.
- Both sandbox images present locally: `rust-sandbox` (Rust, primary) and
  `aicv/go-sandbox` (Go). These are offline images with the selected dependencies
  vendored in.

Point the SDK at the Podman socket:

```sh
export DOCKER_HOST=$(make -s podman-host)
```

Build the sandbox images (from their definitions under `images/`):

```sh
scripts/build-images.sh          # both images
scripts/build-images.sh rust     # just the Rust image
scripts/build-images.sh go       # just the Go image
```

The script prefers `podman` and falls back to `docker` (override with
`ENGINE=docker`), tags each image the way the CLI expects (`rust-sandbox`,
`aicv/go-sandbox`), and prints the resulting image sizes. Equivalent to a direct
`podman build -t rust-sandbox images/rust` if you prefer to run it by hand.

### Startup cost and language selection

The image you pick **is** the language: the pool builds all its containers from a
single sandbox image, so `--image rust-sandbox` runs Rust submissions and
`--image aicv/go-sandbox` runs Go submissions. The image under `images/` carries
the toolchain and the vendored, offline dependencies for that language.

The **first setup is deliberately the expensive part**. When an `Env` starts, the
pool synchronously pre-warms `MinWarm` containers — creating and starting each one
up front — so that container start-up is paid *once*, at boot, rather than on
every job. The larger the pool (higher `--concurrency` / `MinWarm`), the longer
this initial warm-up takes; after it, job assignment is near-instant because each
job is handed an already-running container. Paying container creation and teardown
in advance, off the per-job critical path, is the whole point of the pool.

## How to test it

### 1. Unit tests (no container runtime needed)

```sh
go test ./...
```

### 2. Integration tests (need Podman + the images)

These are gated behind a build tag and exercise the real container runtime — the
pool, sandbox exec, pipeline, and containment tests:

```sh
make int-test
# equivalent to:
#   DOCKER_HOST=unix://<podman-socket> go test -tags=integration -race ./...
```

### 3. Smoke-test the images offline

Prove each image can compile and run with the network disabled:

```sh
make smoke        # Go image
make smoke_rust   # Rust image
```

### 4. Verify a single submission (end-to-end)

Build the CLI and run one submission through a warm pool:

```sh
go build -o aicv ./cmd/aicv

# a directory or file containing the submission (e.g. src/main.rs, Cargo.toml)
./aicv verify --lang rust ./path/to/submission
```

The command prints the outcome, per-stage timings, and any compiler
diagnostics, and exits non-zero if the submission did not pass.

### 5. Run a full known-answer workload (correctness + latency)

`gen-bench` turns a task-suite dataset into a **known-answer** job file — each
case tagged with its expected verdict (canonical solution → pass, buggy variant →
fail). `bench` then runs the whole workload through the pool and reports the
outcomes, assignment and pipeline latency, and the false-positive /
false-negative counts against that ground truth.

```sh
# 1. build a known-answer workload from a task-suite file
./aicv gen-bench task_suite/heldout_combined.jsonl --out /tmp/heldout.jobs.jsonl

# 2. run it (serial; concurrency > 1 may exhaust a small VM's memory)
./aicv bench --concurrency 1 --out /tmp/results.jsonl /tmp/heldout.jobs.jsonl
```

Per-job results are written as JSONL to `--out`; a summary is printed to stderr.

> **Note on concurrency:** on a memory-constrained VM (the evaluation used a 2 GiB
> Podman machine), several concurrent Rust compiles can exhaust memory and get
> OOM-killed, which looks like a compile error. The reported measurements are
> therefore run serially (`--concurrency 1`); raise it only on a memory-adequate
> host.

## Evaluation summary

With no comparable system to benchmark against, each mechanism is judged by
switching it off and re-running the same workload (self-ablation). The reference
workload is a known-answer set of **328 jobs** drawn from the 164 HumanEval-X Rust
problems — each supplying a canonical solution expected to pass and a buggy
variant expected to fail — run serially on a 2 GiB VM with the Rust 1.96
toolchain. Headline results from the dissertation's evaluation:

- **Trustworthy verdicts (R4):** 326/328 correct, **zero false positives** over the 164 buggy solutions; the 2 false negatives are the safe direction (a correct solution rejected), both traced to `rand` dependency-version drift rather than a verifier defect.
- **Containment (R1):** all 4 adversarial escape classes contained; the deny-by-default seccomp profile shown by controlled ablation to be the operative control for the namespace escape, and the full workload returns identical outcomes with it on.
- **Bounded cost (R3):** warm assignment ~333 ns vs ~28.8 ms per-job container creation (~86,000×); end-to-end pipeline mean 4.2 s, p99 5.4 s (well under the 30 s target); slim offline image 1.99 GB (down from 5.18 GB); zero leaked containers across 736 jobs.
- **Structured verdict (R2):** every verdict carries the failing stage plus the compiler's diagnostics — the instrument by which both false negatives and the one latency outlier were diagnosed from the verdict alone.

The two quantitative targets — 2 s for assignment and 30 s for the pipeline — are
self-imposed engineering floors for the intended consumer, cleared by wide
margins; no claim rests on the distance between a measured value and a target.
Full data is in `docs/eval-data-2026-08.md` (raw JSONL under
`docs/eval-raw-2026-08/`).

## Further reading

- `docs/PRD-infrastructure.md` — the build spec and architecture.
- `docs/ENGINEERING-RECORD.md` — build log of how the modules came together.
- `docs/decisions/` — ADRs (baked-dependency selection, Rust as primary language).
- `docs/eval-data-2026-08.md` — measured results, with raw data under `docs/eval-raw-2026-08/`.
- `thesis/` — the dissertation this prototype accompanies.
