# AI Coding Verifier — Engineering Record

**Purpose.** A complete, defensible account of the system: what was built, every
significant design decision and *why* it was made, the findings that emerged, the
evaluation results, and the honest limitations / future work. Written as a
reference for the dissertation and its defence — nothing important should be
missing from this document.

**Status.** System built (M0–M10), tested (`-race` + integration, leak-checked),
and evaluated. 6 of 7 success criteria pass; the 7th is a quantified, documented
limitation with a clear fix path.

---

## 1. What the system is (and the pivot)

**One sentence.** A fast, lightweight, secure, self-cleaning **sandboxed
compile-run-verify service for compiled languages** (Rust primary, Go secondary):
given a code submission, it compiles it, runs its tests inside an isolated
offline container, and returns a **structured verdict** (passed / compile-error /
runtime-error / test-failed, plus compiler diagnostics and timings).

**The pivot (2026-07-09).** The project began as an RL/GRPO *training* verifier
(with a reward-signal ablation as the thesis). The supervisor cut reinforcement
learning from scope. The deliverable became a **pure systems verifier** — no
model, no training, no pass@k. Consequences that persist in the code:

- The RL-era `internal/reward` scalar (the `Coarse`/`Structured`, `−0.65`/`K`
  reward formula) is now **dead code**. It was deliberately **kept, not deleted**
  (decision: additive changes only; clean up later), and the live path uses a
  separate `internal/verdict` classifier instead.
- `pkg/api` is just `Verify(Job) → Verdict` — **not** an InterCode-style Gym
  environment (`reset`/`step`/`get_reward`), which is what the RL era required.
- Success is measured by **system metrics** (S1–S7 below), not model behaviour.

**Language framing.** Rust is primary because its compiler exposes structured,
machine-readable diagnostics (`cargo --message-format=json`: error codes,
labelled spans, machine-applicable fix suggestions, child-diagnostic chains). Go
exposes none of this — flat text only. The `verifier` package embodies this
contrast (`ParseRust` is rich; `ParseGo` is a best-effort regex with no error
codes), which is itself evidence for the Rust-primary decision.

---

## 2. Architecture

A layered Go stack (module `github.com/aicv`). A job flows **down**; a verdict
flows back **up**. Each layer knows only the one beneath it.

```
cmd/aicv (CLI: verify / bench / gen-bench)      ← human entry point
        │
pkg/api  Env.Verify(Job) → Verdict              ← the funnel (owns pool + reaper)
        │
internal/pipeline  Run: compile → execute       ← two-stage, independent attribution
        │
internal/pool  Acquire / Release (warm LRU)     ← borrow a warm container
internal/sandbox  Run: stage files, exec, TTL   ← run one job, capture output
internal/ttl  background reaper (backstop TTL)
        │
internal/dockercli  create/start/exec/copy/kill/remove   ← podman via Docker SDK
        │
🐧 podman container (rust-sandbox / go-sandbox)  ← cargo build, then cargo test

returns:  Result → internal/verifier (parse diagnostics) + internal/verdict
          (classify outcome) → Verdict
```

Supporting packages: `internal/verifier` (diagnostics), `internal/verdict`
(outcome classification), `internal/dataset` (task_suite → eval cases),
`internal/reward` (RL-era, dead but retained).

**Cross-cutting decisions.**
- **Go + podman via the Docker Go SDK.** Podman is daemonless and rootless;
  the standard `github.com/docker/docker/client` talks to its API socket
  unmodified. (Pinned `docker/go-connections v0.5.0`: v0.7.0 dropped
  `sockets.DialPipe`, which docker v27 requires.)
- **Dependency injection everywhere.** Each layer *receives* its dependencies
  (the `*dockercli.Client`, the container, the pool) rather than constructing
  them. One shared client/connection; every layer is independently testable with
  fakes.
- **Integration tests are build-tagged** (`//go:build integration`) so the fast
  unit suite needs no podman; container tests run via `make int-test` (which
  wires `DOCKER_HOST` to the podman socket).
- **Offline is non-negotiable.** Every container runs `--network none`; Rust uses
  vendored crates + `CARGO_NET_OFFLINE=true`; Go uses `GOPROXY=off GOSUMDB=off`.

---

## 3. Milestone-by-milestone record

Each milestone was built on its own branch with TDD (unit + integration),
committed and pushed separately (see §7). For each: **what**, **key decisions &
why**, **limitations**.

### M0 — Podman SDK wiring
- **What.** `internal/dockercli.Client` with `New`/`Ping`/`Close`; added the
  Docker Go SDK to `go.mod`; `make podman-host` / `make int-test` targets.
- **Decisions.** Connect via `DOCKER_HOST` with API-version negotiation (podman's
  supported version set differs). Pin `go-connections v0.5.0` (see §2).

### M1 — Container lifecycle (`dockercli`)
- **What.** `Create` (offline, mem/CPU/pids caps, seccomp hook) → `Start` →
  `Exec` (stdout/stderr demux via `stdcopy`, real exit codes) → `CopyIn` (tar) →
  `Kill` → `Remove`.
- **Decisions & why.**
  - **`Create` and `Start` are separate** (the podman model; `docker run` = both).
  - **Pooled containers run `sleep 2147483647`** as PID 1 — a do-nothing
    keep-alive so the container stays up and jobs run via `Exec` (like SSH-ing in),
    instead of `podman run` per command (which would pay container start-up on
    every compile).
  - **`Remove` sends SIGKILL *before* force-remove.** A plain force-remove sends
    SIGTERM and waits the runtime's **default 10s stop grace** before SIGKILL —
    and PID 1 (`sleep`) *ignores* SIGTERM (the kernel gives PID 1 no default
    signal action), so every teardown took ~10s. SIGKILL is uncatchable even by
    PID 1 → teardown dropped from **~10s to ~0.17s (≈60×)**. This is essential for
    auto-GC and pool teardown.
- **Limitations.** Error branches (create/exec failure) are lightly tested by
  design (happy-path integration coverage ~73%).

### M2 — Job runner (`sandbox`)
- **What.** `Run(job)` stages files → execs the command under a wall-clock TTL →
  captures stdout/stderr/exit/`TimedOut`/duration.
- **Decisions & why.**
  - **TTL enforced *inside* the container** via busybox `timeout -s KILL <secs>` —
    SIGKILL (not the default SIGTERM) so untrusted code cannot catch and ignore
    it. Kills only the job process; the container survives for reuse.
  - **A Go-side context deadline (`TTL + 10s`)** backstops the in-container
    timeout in case the exec/API call itself hangs.
  - **Non-root image (uid 1000).** Discovered during M2: `mkdir /work` fails
    (permission denied); jobs must use writable `/tmp` work dirs. `Run` now
    **surfaces** a failed workdir prep loudly instead of failing cryptically.
- **Limitations.** `TimedOut` detection uses exit code (137/143) + duration
  corroboration; an OOM kill (also 137) is disambiguated only by elapsed time.

### M3 — Warm container pool (`pool`) — the hardest module
- **What.** `New` pre-warms `MinWarm`; `Acquire` reuses idle → grows under
  `MaxSize` → **blocks** at capacity until a `Release`; `Release` returns or
  **recycles** (after `MaxJobsPerContainer`); `Close` removes everything.
- **Decisions & why.**
  - **One job per container at a time.** Simpler and far better isolation than the
    original proposal's multi-job-per-container model (no cross-job `/tmp`
    interference). Container states collapse to IDLE/BUSY; OVERFLOW becomes a
    *pool-level* condition.
  - **Two sources of truth:** a **buffered channel** (`idle`, sized to `MaxSize`)
    for the *hand-off* — Go guarantees each value goes to exactly one receiver, so
    two callers can never get the same container *for free*; and a
    **mutex-guarded `count` + `all` registry** for the *capacity ledger*.
  - **Reserve-before-slow-create.** `grow` increments `count` under the lock
    *before* the slow `backend.Create`, so concurrent callers see the reservation
    and correctly get `errAtCapacity` — this is what enforces "never exceed
    `MaxSize`" under concurrency without holding the lock during I/O.
  - **`Backend` interface** abstracts `dockercli` so unit tests use an in-memory
    fake (state machine + concurrency provable with `-race`, no podman).
  - **`RemoveByID` + `Release`-skips-reaped** (added in M8) let the TTL reaper
    force-remove a container mid-job without the pipeline's deferred `Release`
    re-parking a dead container.
- **Limitations.** `Release`-skips-reaped relies on the invariant that the reaper
  only ever targets still-checked-out containers (so it can't race a normal
  re-park); documented in code.

### M4 — TTL reaper (`ttl`)
- **What.** `Track(id, ttl)` / `Untrack(id)` + a background `Start(ctx)` sweep
  that force-kills anything past its deadline via an injected `KillFunc`. The
  container-level **backstop** to M2's process-level timeout (for when the whole
  exec/container hangs).
- **Decisions & why.**
  - **Injectable clock** → the sweep logic is unit-tested deterministically with a
    fake clock (no real waiting); only one test uses the real clock.
  - **Kills invoked outside the lock** so a slow kill doesn't stall Track/Untrack.
  - **`Wait()`** blocks until the sweep goroutine (and any in-flight reap) exits —
    added after a real bug (see §5).

### M5 — Two-stage pipeline (`pipeline`)
- **What.** `Run(Job)` acquires a container → **compile** (`cargo test --no-run
  --message-format=json`) → if it compiles, **execute** (`cargo test`) → fills a
  `Result` with independent attribution. Per-job `/tmp` dir, cleaned up.
- **Decisions & why.**
  - **`cargo test --no-run` for the compile stage** (not `cargo build`) so a test
    that fails to *compile* is attributed to the compile stage, not execution.
  - **Independent attribution is the whole point:** a compile failure returns
    immediately with `Stage=compile`, `Compiled=false`, and execution *never
    runs* (`RuntimeStdout=""`). A failing assertion is `Stage=execute`. A signal
    death (`exit ≥ 128`) sets `Crashed` (runtime error vs test failure).
  - **Reaper wiring is optional** via a functional option (`WithReaper`), so M5's
    tests are unchanged and only the API passes a reaper.
- **Limitations.** Go execute path is minimal (`go test`); only Rust is exercised
  end-to-end.

### M6 — Structured diagnostics (`verifier`) — built in the foundation
- **What.** `ParseRust` projects `cargo --message-format=json` into a
  language-agnostic `Diagnostic` (code, labelled spans, machine-applicable
  suggestions **flattened from the child-diagnostic chain** — e.g. E0382's
  `.clone()` fix lives on a child node). `ParseGo` is a best-effort flat-text
  fallback with no error code.
- **Decisions & why.** Fixtures are **real captured `cargo` output** (E0308,
  E0382, E0502, clean build), not hand-written. Keeping `ParseGo` is deliberate:
  it is the runnable *evidence* for "Rust structured vs Go flat" (the Rust-primary
  argument), at ~15 lines of cost.
- **Coverage.** 95% (pure, table-driven).

### M7 — Verdict classifier (`verdict`)
- **What.** Public `Classify(Result) → Outcome` (Passed / CompileError /
  RuntimeError / TestFailed).
- **Decisions & why.** **Additive** (user's explicit choice): a *new*
  `internal/verdict` package rather than refactoring `internal/reward` and
  deleting the RL scalar — clean up later. Ordering: didn't compile →
  CompileError first; passed → Passed; timed-out/crashed → RuntimeError; else
  TestFailed. 100% coverage.

### M8 — Public API + TTL wiring (`pkg/api`)
- **What.** `Env` = client + pool + reaper. `NewEnv` builds them and starts the
  sweep; `Verify(Job) → Verdict` runs the pipeline, parses diagnostics,
  classifies, and packs a `Verdict`; `Close` tears down.
- **Decisions & why.**
  - The **`Env` owns the reaper's lifecycle** and wires `reaper.KillFunc =
    pool.RemoveByID`. Jobs are tracked by **container id** (one job per container).
  - **`Close` calls `reaper.Wait()`** before closing the pool/client — see §5.
- **Limitations.** `Verify` returns a total duration; the finer timing breakdown
  was added later (S1).

### M9 — CLI (`cmd/aicv`)
- **What.** `verify <path>` (a `.rs` file wrapped in a minimal cargo project, or a
  project dir; exit code reflects the verdict). `bench <jsonl>` runs a workload of
  self-contained job-specs concurrently and reports outcomes + latency +
  correctness. `gen-bench` (added in the eval phase) converts a dataset.
- **Decisions & why.**
  - **`bench` consumes a general job-spec format** (`{id, lang, files, expected}`),
    decoupling the CLI from any dataset schema; a separate converter produces
    specs. This is the evaluation harness's data path.
  - **Explicit `env.Close()` before `os.Exit`** — `os.Exit` skips deferred
    functions, which was leaking containers on a failing `verify` (see §5).
- **Limitations.** `bench` writes all results at the end (no incremental progress
  output); flags must precede the positional arg (Go `flag` convention).

### M10 — Security (`dockercli`/`pool`/`api` + corpus)
- **What.** Per-container **`PidsLimit`** (fork-bomb bound); an adversarial corpus
  (`testdata/adversarial/*.sh`: network egress, filesystem tamper, fork bomb); a
  containment integration test.
- **Decisions & why.** The containment stack is **`--network none` + non-root uid
  1000 + pids limit + TTL + a custom deny-by-default seccomp whitelist** (see the
  seccomp follow-up below; originally the runtime's *default* profile, with the
  `images/*/seccomp.json` files empty). Result: **4/4 attacks contained** (network
  blocked, `/etc` write denied, fork bomb hits `can't fork` and is bounded, and a
  `unshare(2)` user-namespace escape is denied by the whitelist).
- **Limitations.** **gVisor** (a stronger user-space kernel isolation option,
  mentioned in the proposal) is not implemented.

### Custom seccomp whitelist (M10 follow-up)
- **What.** A deny-by-default seccomp profile (`images/{rust,go}/seccomp.json`,
  `defaultAction: SCMP_ACT_ERRNO`) with a curated ~200-syscall allow-list covering
  exactly what the compiler + test harness need; every dangerous syscall a
  container escape would use (`mount`, `pivot_root`, `chroot`, `unshare`, `setns`,
  `ptrace`, `process_vm_*`, `kexec_load`, `init_module`, `bpf`, `perf_event_open`,
  `keyctl`/`add_key`, `reboot`, `swapon`, `settimeofday`, `iopl`/`ioperm`, …) is
  absent and therefore blocked. Wired end-to-end via a new `SeccompProfilePath`
  field: `cmd/aicv` (`--seccomp <path>`) → `api.Config` → `pool.Config` →
  `dockercli.CreateConfig` → `SecurityOpt`.
- **Decisions & why.**
  - **Path, not inline JSON.** Podman's Docker-compat API treats the `seccomp=`
    value as a *file path*, not profile content (passing JSON gave `file name too
    long` — it `ReadFile`s the value). So the profile is passed by path (also more
    portable to native podman). The CLI resolves it to an absolute path and
    fails-fast if the file is missing.
  - **Curated hardened baseline, not grown-from-zero.** The elegant per-syscall
    discovery loop (`SCMP_ACT_LOG` default → read the audit log → add the missing
    syscall) proved unreliable on the Fedora/applehv podman VM — `SCMP_ACT_LOG`
    produced no `ausearch`-visible records despite `actions_logged` including
    `log`. So the allow-list was authored from the known compiler/runtime syscall
    set and **verified empirically** instead (below).
- **Verification (the key result).** The profile *changes nothing* about
  correctness: the full 66-job held-out benchmark under the deny-by-default
  profile scored an **identical 65/66, 0 false-positives, same single `Rust/50`
  false-negative** — proving the allow-list is complete for the workload (no
  legitimate compile broke). Enforcement is proven by a **with/without control**:
  the identical `unshare(2)` (and `keyctl(2)`) call *succeeds* for the unprivileged
  job **without** the profile and is *blocked* (EPERM) **with** it — isolating
  seccomp as the specific wall, which the network/non-root/pids controls could not
  demonstrate. Covered by `TestContainment_SeccompBlocksUnshare` +
  `testdata/adversarial/syscall_blocked.sh`.
- **Limitations.** Rust profile verified end-to-end; the (identical) Go profile is
  verified only on a trivial compile (the Go path is minimal end-to-end anyway).

### Evaluation-phase work (post-M10)
- **`internal/dataset` converter.** Turns a **humaneval-x** task_suite record into
  known-answer cases: `canonical_solution` → expect *pass*, `buggy_solution` →
  expect *fail*; assembles `prompt + solution + tests` into a cargo project whose
  `Cargo.toml` auto-declares external crates (`DetectCrates` scans `use`
  statements). **mbpp/multipl-e records are not convertible** — they ship *no*
  reference solution (prompt + tests = an empty-bodied stub for a model to fill)
  and use a `main()`-based harness, not the `#[cfg(test)]` harness the pipeline
  runs.
- **md5 vendoring.** The humaneval-x Rust preamble imports `md5`, a long-tail
  crate below the ≥10% prevalence threshold and thus not in the baked set. Added
  `md5 = 0.7` to the image manifest, re-vendored, rebuilt.
- **Image slim-down.** Removed the pre-compiled `target/` (both debug + release)
  from the image: jobs compile in their own per-job target dir and *never read*
  the baked one, so it was ~1.6 GB of dead weight. **5.18 GB → 1.99 GB (62%)**,
  proven not to break execution.
- **Timing instrumentation (S1).** `Result`/`Verdict` gained
  `Assignment`/`Compile`/`Execute` durations; `bench` reports assignment latency.
- **Soak tooling (S4).** `bench --max-jobs N` (container recycling).

---

## 4. Evaluation (S1–S7)

Ground truth: the contamination-controlled task_suite (540 train / 105 held-out).
The correctness set is the **33 held-out humaneval-x problems** (the only records
with canonical *and* buggy solutions) → **66 known-answer jobs** (33 expect pass,
33 expect fail). Method: `gen-bench` → `bench` (which scores each verdict against
its expected label).

| # | Criterion | Target | Result | Verdict |
|---|-----------|--------|--------|---------|
| S1 | assignment latency | < 2s | **~375 ns** warm (channel handoff); ~0.3s cold (container create) | ✅ pass (huge margin) |
| S2 | full pipeline latency | < 30s | mean **~7–8s** (dep-heavy Rust), p95 ~11s, max ~23s | ✅ pass |
| S3 | lightweight image | "minimal" | **1.99 GB** (from 5.18 GB); Go image 984 MB | ✅ pass (honest floor for an offline Rust compiler) |
| S4 | auto-GC / no leak | bounded, no leak | 200-job soak: peak **4** live containers (= cap), **0 leak** after, per-container procs bounded at **~101** with recycling | ✅ pass |
| S5 | adversarial containment | ≥ 95% | **4/4 contained** (incl. seccomp-specific `unshare`) | ✅ pass |
| S6 | verdict correctness | low FP/FN | **65/66 correct**, **0 false-positives**, 1 false-negative | ✅ pass |
| S7 | dependency-cache reuse | speedup | **none** — repeated identical jobs stay flat at ~7s; ~6s/job of dep recompilation is repeated | ⚠️ documented limitation |

**S6 detail (the headline).** 0 false positives — the verifier *never* accepted a
broken solution (all 33 buggy solutions correctly rejected). 32/33 canonical
solutions pass. The single false negative is `humaneval-x/Rust/50#canonical`,
which genuinely fails to compile against Rust 1.96 (dataset/toolchain drift, not a
verifier flaw). Measured **serially** — an earlier concurrent run showed spurious
compile-errors from OOM (see §5), which vanished serially.

**S7 detail.** The same `regex`+`md5` job run 5× in one warm container: 8.8s, 7.2s,
6.8s, 6.8s, 6.9s — **flat**, not decreasing. A trivial no-dep job is ~0.85s. So
~6s/job is repeated dependency compilation that a cache would eliminate.

---

## 5. Findings surfaced during evaluation (and how each was handled)

These are real, defensible results — the evaluation doing its job.

1. **Long-tail crate coverage gap.** The ≥10%-prevalence mined crate set (52
   crates, ADR-001) covers `rand`/`regex` but *not* `md5`, which every
   humaneval-x problem imports in its harness preamble → all 33 failed to compile
   offline. **Fix:** vendor `md5` (targeted addition). *Result to report:* mined
   coverage handles the common case; long-tail crates need targeted vendoring.

2. **OOM under concurrency on a small VM.** The podman VM has **2 GiB RAM**;
   4 concurrent `regex`-linking compiles exhaust it → `rustc` OOM-killed →
   *spurious* compile-errors (false negatives). **Serial runs are clean.** *Report
   as:* concurrency must be sized to available memory for heavy compiles; not a
   verifier or dataset defect.

3. **Zombie process accumulation.** The pooled container's PID 1 (`sleep`) does
   not reap children, so finished `[timeout]` wrappers linger as defunct
   processes. Over a long soak they'd exhaust PIDs. **Mitigation:**
   `MaxJobsPerContainer` recycling bounds it (soak peaked at ~101 procs, not
   ~1000). **Cleaner future fix:** a reaping init (`tini`) as PID 1.

4. **The baked `target/` was unused** (S3 ↔ S7). The image pre-compiled deps "for
   speed," but jobs compile in fresh per-job target dirs and never read it → 1.6
   GB paid for no benefit. Removed it (S3 win) and documented the real cache gap
   (S7).

5. **The `Env.Close` reap-vs-close leak.** `Close` closed the docker client while
   a reap was still in flight → a container left `Exited(137)`, not removed.
   **Fix:** `ttl.Manager.Wait()`, called in `Close` before teardown. Caught only
   because every test run ends with a `podman ps` leak check.

6. **`os.Exit` skips deferred `Close`.** A failing `aicv verify` called `os.Exit(1)`,
   skipping `defer env.Close()` → leaked containers. **Fix:** explicit `Close`
   before `os.Exit`. Same leak-check caught it.

7. **Dataset/toolchain drift.** Some canonical solutions carry deprecated std
   items (e.g. `std::ascii::AsciiExt`) — warnings on Rust 1.96, and one
   (`Rust/50`) is a hard compile error. The benchmark is a few years old.

---

## 6. Limitations & future work (consolidated)

Ordered roughly by impact.

1. **No cross-job dependency-cache reuse (S7).** ~6s/job of repeated compilation.
   *Fix paths:* `sccache` (content-addressed, safe, ~half-day) or a shared
   per-container `CARGO_TARGET_DIR` (fits one-job-per-container, but has a
   correctness gotcha — same package name `submission` could reuse a prior job's
   binary; needs a forced rebuild). This is the single biggest latency win left.
2. **Zombie reaping.** Recycling mitigates; a `tini`-style init as PID 1 would fix
   it structurally.
3. **Crate coverage is prevalence-mined + manual.** Long-tail crates (like `md5`)
   need targeted vendoring; a submission using an un-vendored crate fails offline.
4. **mbpp/multipl-e problems are not usable for correctness** (no reference
   solution; `main()`-based harness). The correctness set is humaneval-x (66
   cases). A `main()`-harness adapter would unlock them for *latency* workloads.
5. **Rust-primary; Go path is minimal** (compiles/tests but unexercised
   end-to-end; kept as the "structured-vs-flat" contrast).
6. **Dead RL `reward` scalar** retained for now; a cleanup pass would remove it.
7. **One false negative (`Rust/50`)** from toolchain drift; a dataset-cleanup or
   toolchain-pin pass would address it.
8. **VM memory limits concurrency** for heavy compiles (2 GiB). More RAM (or the
   S7 cache, which cuts per-job memory + time) would allow parallelism.
9. **No gVisor.** The containment stack is now a *custom* deny-by-default seccomp
   whitelist + network isolation + non-root + pids limit + TTL. **gVisor** (a
   user-space kernel for even stronger isolation) remains future work.

---

## 7. Repository & branch structure

Per-milestone branches, each committed + pushed with its own PR (commit messages
are plain, no co-author trailer, per project convention):

```
feat/infra-build     — foundation (verifier, verdict, result types) + M0 + M1
feat/m2-sandbox       feat/m3-pool       feat/m4-ttl       feat/m5-pipeline
feat/m7-verdict       feat/m8-api        feat/m9-cli       feat/m10-security
feat/eval-converter   — internal/dataset + gen-bench + bench correctness scoring
feat/vendor-md5       — md5 crate added to the rust image
feat/slim-rust-image  — dropped pre-compiled target/ (5.18GB → 1.99GB)
feat/s1-latency       — assignment/compile/execute timing instrumentation
feat/s4-soak          — bench --max-jobs (container recycling)
feat/seccomp-profile  — custom deny-by-default seccomp whitelist + --seccomp flag
```

Key specs/records in `docs/`: `PRD-infrastructure.md` (the build spec),
`decisions/ADR-001` (dependency mining), `decisions/ADR-002` (Rust primary), and
this record.

**Run the system:** `make int-test` (integration suite); `aicv verify <file.rs>`;
`aicv gen-bench <dataset.jsonl>` then `aicv bench --concurrency 1 <jobs.jsonl>`.

---

## 8. One-paragraph summary for the abstract

We designed and implemented an offline, sandboxed compile-run-verify service for
compiled languages, filling the gap left by interpreted-language frameworks
(InterCode, StepCoder) that lack compiled-language execution infrastructure. The
system provides a dependency-cached Rust/Go sandbox (empirically-selected baked
crates), an LRU warm-container pool with TTL/GC lifecycle management, a two-stage
compile-execute pipeline with independent error attribution, and a verifier that
turns `rustc`/`cargo` JSON into structured diagnostics and a clean verdict — all
behind a single `Verify(job) → Verdict` API and a CLI. On the held-out HumanEval-X
Rust ground truth the verifier achieved 65/66 correct with **zero false
positives**; warm-container assignment is ~375 ns, the full pipeline runs in
~7–8s (well under the 30s target), attacks are contained (4/4, including a
seccomp-specific user-namespace escape), containers leak nothing across a 200-job
soak, and the image was reduced 62% to 1.99 GB. The one
unmet target — cross-job dependency-cache reuse — is quantified (~6s/job
recoverable) with a clear implementation path, and is the principal avenue for
future work.
