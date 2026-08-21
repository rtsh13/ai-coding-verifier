# Chapter 5 Evaluation — Raw Data & Results

**Status:** measurements in progress (2026-08-01). This file collects every number
before any Chapter 5 prose is written. Do not derive the chapter until this is complete.

Environment: macOS host, podman 5.7.1 Linux VM (Fedora CoreOS, `applehv`), 2 GiB RAM,
Rust sandbox toolchain 1.96. All latency/correctness runs are **serial** (`--concurrency 1`)
per the memory-bound policy (4 concurrent heavy compiles OOM the 2 GiB VM).

---

## Workload: 328 known-answer jobs (the pooled HumanEval-X set)

- **164 HumanEval-X Rust problems**, each with a canonical (expect-pass) and a buggy
  (expect-fail) variant → **328 jobs** (164 pass, 164 fail). Verified: 328 cases, 328
  unique IDs, split {pass:164, fail:164}.
- Built by pooling both task-suite splits: `heldout_combined.jsonl` (33 HE-X → 66) +
  `train_combined.jsonl` (131 HE-X → 262). The train/held-out partition existed only to
  isolate a training set for the descoped RL phase; with **no model trained, it does not
  bind an infrastructure evaluation**, so all 164 problems are used. The two splits are
  disjoint by underlying problem ID (verified — no overlap).
- The 481 MultiPL-E records are excluded: they ship no solution body (empty stubs awaiting
  a model completion), so there is nothing to compile. Not a split issue — absence of code.
- Regenerate: `aicv gen-bench --out X.jsonl <combined.jsonl>` on each file, concatenate.

---

## Evaluation methodology — reasoning & decisions (discussion notes)

These notes capture *why* the evaluation is structured the way it is, so the Chapter 5
prose can be derived from settled reasoning rather than re-litigated. Recorded 2026-08-01.

### 1. The problem: no external baseline exists
No prior system publishes infrastructure latency for compiled-language execution
(MultiPL-E reports pass@k; online judges report per-program time limits, not
assignment/pipeline latency). So there is nothing external to benchmark against. Two
consequences follow and drive everything below.

### 2. Targets are self-imposed floors, not evidence
The 2 s (assignment) and 30 s (pipeline) targets are engineering requirements justified by
the RL consumer's throughput needs, NOT derived from literature. Both the full system AND
the ablated baselines clear them by enormous margins, so **the targets do not discriminate
between designs**. Therefore: keep a small "pre-registered bar, met" table for honesty, but
**do not build any prose argument on target-vs-measured**. In particular, do NOT compare S1
to 2 s — compare warm 333 ns vs container-per-job 28.8 ms.

### 3. Proof method: self-ablation, system as its own baseline
Each mechanism is proven by removing it and re-measuring on the *same* workload; the delta
is its contribution. This is the only defensible method when no comparable prior system
exists.

### 4. Ablation is directional, not about magnitude
The question an ablation answers is: did the mechanism *improve or degrade* performance? Not
"by how much". Even +32 ms is a win if nothing regressed. Do NOT inflate small wins, and do
NOT present the system as flawless — report drawbacks (e.g. the ~1% pool contribution on
dep-heavy jobs, the S7 miss) plainly. Modest-but-honest beats impressive-but-inflated.

### 5. Single-variable (one-at-a-time) ablation is valid — and its results are CONDITIONAL
Removing one mechanism while holding the rest of the system fixed is the standard, accepted
ablation method. It is not weakened by the other mechanisms remaining on — that is the
definition. What it measures is the mechanism's **marginal contribution, conditional on the
others being present**. This conditionality is inherent, not a defect; it must be *stated*,
not hidden. A full factorial (2^n runs) would isolate interactions but is out of scope on a
serial 2 GiB VM; OAT-from-full is the correct pragmatic choice.

### 6. Why the single-variable ablation is the STRONGER (fairer) comparison
Both arms carry the full advanced setup (offline image, fast teardown, exec dispatch); only
the ablated mechanism differs. So the delta is honestly attributable to that mechanism ALONE
— no stacking every advantage on one side against a strawman. This is *more* credible than
"full system vs naive-everything-off", which would inflate one mechanism by crediting it with
the value of all the others. The trade: because the rest of the system is already good, each
mechanism looks **modest** (a conservative, lower-bound marginal gain), not heroic. Let the
*set* of results — deterministic sub-µs assignment, 0 leaks, full containment, 0 false
positives — carry the overall case, not any single dramatic number.

### 7. IMPORTANT — container-per-job is NOT a faithful InterCode/StepCoder reproduction
The email framed the headline ablation as "also a fair comparison to prior art, since
container-per-job is InterCode's per-episode model". That claim is **overstated** and should
be scoped down. As run (`--max-jobs 1`), container-per-job **inherits** this work's other
optimisations:

| Precondition the ablation assumes | What prior art actually does |
|---|---|
| Same runtime + host (podman, this VM) | Different runtime/host — absolute numbers not comparable |
| Slim OFFLINE vendored image (deps baked) | Likely ONLINE dependency resolution per episode → far slower, on the compile side |
| SIGKILL fast teardown (confirmed `dockercli.Remove`, ~3 ms) | No such fix; teardown cost model differs |
| Two-stage COMPILED pipeline | Interpreted-language (Python) execution — different cost structure |

So container-per-job is a **clean ablation of pooling** (accurate for "what does the pool add
to *this* system"), but a **lower bound** on the gap to a naive per-episode design — which
would additionally pay online dependency resolution and image cost. **Recommendation:** scope
the claim precisely — "this isolates the pooling contribution; it inherits the offline image
and fast teardown, so it under-states the gap to a naive per-episode environment". Drop or
heavily qualify "fair comparison to prior art".

### 8. Known interaction to state with results
The pool's contribution is **bounded by the current dependency-compilation floor**. Because
compilation dominates (~4 s/job), the pool's ~28 ms assignment saving is ~1% of total. If S7
were fixed (deps amortised → ~1 s jobs), the same ablation would report a *larger* relative
pool contribution. State this interaction where the pool result is reported.

### 9. No cross-model comparison, deliberately
Infrastructure performance is model-independent (assignment unaffected by code origin;
pipeline latency depends on the submission's dependencies, not the model). Cross-model would
measure model pass@k — the descoped RL question. Comparison axis is **workload weight**
(dep-light ~0.85 s vs dep-heavy ~4 s) instead.

### Methodology sentence to put in the thesis
> "Each ablation removes a single mechanism from the complete system and re-measures on the
> same workload, so the reported figure is that mechanism's marginal contribution conditional
> on the others being present; interaction effects are not isolated, as a full factorial was
> out of scope. The container-per-job configuration isolates the warm pool but inherits the
> offline image and fast teardown, so it is a conservative lower bound on the cost of a naive
> per-episode design rather than a reimplementation of prior systems."

---

## Measurement plan & status

| # | Measurement | Command | Status |
|---|---|---|---|
| Setup | 4-job smoke | serial bench | ✅ 4/4 correct, warm assign 375 ns |
| S6 + S2 + S1 | **Warm arm** (correctness, pipeline latency, warm assignment) | `bench --concurrency 1 --ttl 60` over 328 | ✅ 326/328, 0 FP, 2 FN |
| Headline | **Container-per-job arm** (ablation baseline) | `bench --concurrency 1 --max-jobs 1` over 328 | ✅ assign 333 ns vs 28.8 ms |
| S3 | Image footprint | `podman images` | ✅ Rust 1.99 GB, Go 984 MB |
| S4 | Reclamation / soak / leak | no-recycle soak + leak check | ✅ 0 leaks; zombies 12→105 |
| S5 | Adversarial containment (4 classes) + seccomp on/off | containment integration tests + ablation | ✅ 4/4; on/off clean |
| S7 | Cache effectiveness | same dep-heavy job ×5 in one warm container | ✅ flat, unmet (as expected) |
| S5-completeness | 328 under seccomp profile (allow-list admits legit work) | `bench --seccomp` over 328 | ✅ 0 differences vs unrestricted |

---

## Results

### S3 — Image footprint (collected)
- Rust sandbox image: **1.99 GB** (down from 5.18 GB original, −62%).
- Go sandbox image: **984 MB**.

### Setup smoke (collected)
- 4 jobs serial: 4/4 correct, 0 FP, 0 FN. Warm assignment p50 375 ns. Pipeline running clean.

### S6 / S2 / S1 — Warm arm (328 jobs, serial, warm pool, TTL 60s) — COLLECTED

Outcome distribution: passed 162, test_failed 162, compile_error 4.

**S6 Correctness: 326/328 correct, 0 false positives, 2 false negatives.**
- 0 FP over 164 buggy jobs — no broken solution accepted (rule-of-three upper bound ~1.8%).
- 2 FN, both compile_error on canonical solutions: `Rust/50#canonical`, `Rust/32#canonical`.

**Root cause of BOTH false negatives (IMPORTANT — differs from current thesis):**
- Hard errors are `[E0061] method takes 1 argument but 2 supplied` and
  `[E0277] {integer}: SampleRange<_> not satisfied`, on `rand`'s `gen_range(low, high)`.
- The canonical solutions use the **old `rand` 0.7 API** `gen_range(a, b)` (2 args); the
  vendored `rand` is 0.8+, where it is `gen_range(a..b)` (1 range arg). So the cause is
  **`rand` crate-version drift**, i.e. a dependency-version mismatch.
- `std::ascii::AsciiExt` appears only as a `[deprecated]` **warning**, not the hard error.
- ⚠️ Current §5 (line 903) attributes `Rust/50` to AsciiExt becoming a hard error. That is
  **misattributed** — the actual blocker is the `rand::gen_range` signature change. Reconcile.
- Classification: these are dependency-version-drift FNs, the safe error direction (reject a
  correct-against-old-API solution), never a false positive. 2 of 164 problems use `rand`.

**S1 Assignment latency (warm):** p50 333 ns, p95 959 ns, max 1958 ns. Target < 2 s — pass, huge margin.

**S2 Pipeline latency:** mean 4192 ms, p95 4910 ms, p99 5392 ms. Distribution:
p50 4086, p90 4572, p95 4910, p99 5392 ms. **One outlier: `Rust/156#buggy` = 41 028 ms.**
- Reproducible: re-ran at 45.9 s and 47.3 s. Not a transient.
- It is a *buggy* solution finishing `test_failed`; time is in **execution** (deps compile in ~5 s).
  The bug causes a pathological slow run; caught correctly, and it stays under the 60 s TTL.
- Honest S2 statement: 327/328 jobs ≤ ~5.4 s (p99); target 30 s met for every canonical
  solution and all but one buggy solution. The single outlier is degenerate buggy input, and
  the TTL is the backstop (a 30 s TTL would kill it as a timeout instead).

Raw: `results_warm.jsonl` (328 lines, 0 harness errors).

### Headline ablation — warm pool vs container-per-job (328 jobs each, serial) — COLLECTED

| Metric | Warm pool | Container-per-job (`--max-jobs 1`) |
|---|---|---|
| Assignment p50 | 333 ns | 28,811,667 ns (28.8 ms) |
| Assignment p95 | 959 ns | 39,323,917 ns (39.3 ms) |
| Assignment max | 1,958 ns | 129,380,500 ns (129 ms) |
| Pipeline mean (all) | 4192 ms | 4235 ms |
| Pipeline mean (excl. Rust/156 outlier) | 4079 ms | 4112 ms |
| Correctness | 326/328, 0 FP, 2 FN | 326/328, 0 FP, 2 FN (identical) |

**Assignment delta: ~86,000× (333 ns → 28.8 ms).** The pool removes container
create+start from the critical path and makes assignment deterministic sub-µs.

**Total-pipeline delta: only +32 ms/job**, because compilation (~4 s) dominates a
dep-heavy job's cost. On this workload the pool saves <1% of wall-clock per job.

Notes:
- Measured steady-state container create+start ≈ 28.8 ms (p50), NOT the ~0.3 s the
  current thesis S1 states. 0.3 s may have been a first-cold-create; steady-state is ~29 ms.
- +32 ms/job = ~29 ms create + ~3 ms teardown. The tiny teardown is evidence the SIGKILL
  fast-teardown works: without it, each recycle would stall ~10 s on the SIGTERM grace
  (per-job arm would be +10 s/job, not +32 ms). The ablation validates the reclamation design.
- FRAMING: the original "container start-up/teardown would dominate the short compilations"
  is contradicted for dep-heavy jobs — compilation dominates, lifecycle is ~1%. Reframe S1 as
  "assignment reduced ~86,000× and made deterministic," not "creation would otherwise dominate."

Raw: `results_perjob.jsonl`.

### S4 — Reclamation / soak — COLLECTED

**No leaks:** confirmed 0 sandbox containers after every run — the 328-job warm arm, the
328-job container-per-job arm (656 jobs, 328 recycles), AND the 80-job no-recycle soak
(which itself reported `leaks after Close: 0`). Clean teardown every time.

**Zombie-process accumulation (no-recycle soak, 80 jobs in ONE container, `--max-jobs 0`):**
defunct process count grew **12 → 105** across the soak (108 total procs, ~3 live). Each
finished job's `timeout` wrapper lingers as a defunct process because the keep-alive `sleep`
runs as PID 1 and does not reap children. This reproduces the thesis's ~101-procs finding.

**Recycling bounds it:** the container-per-job arm (`--max-jobs 1`, 328 recycles) served one
job per container and showed no accumulation and 0 leaks — recycling after
`MaxJobsPerContainer` resets the process table before it approaches the per-container cap.

Note on peak container count: all runs here are serial (`--concurrency 1`), so peak live
containers = 1, not the "peak 4 = cap" figure (that needed concurrency 4, which OOMs the
2 GiB VM). The leak/recycling/accumulation evidence stands at concurrency 1.

### S5-completeness — full 328 under the seccomp profile — COLLECTED
Running all 328 jobs with the deny-by-default profile applied gives results **identical** to
the unrestricted run: 0 outcome differences across all 328 jobs, same 326/328, same 0 FP,
same 2 FN. The allow-list admits every legitimate compile-and-test job while denying the
escape surface. Raw: `results_seccomp.jsonl`.

### S5 — Adversarial containment — COLLECTED
4/4 attack classes contained (integration tests, all PASS):
- NetworkEgressBlocked (0.32s) — disabled network
- FilesystemTamperBlocked (0.18s) — non-root user
- SeccompBlocksUnshare (0.19s) — seccomp whitelist
- ForkBombBounded (0.15s) — process-count cap

**Seccomp on/off controlled ablation (self-ablation, isolates the layer):**
- Profile OFF: `unshare -Ur` → `REACHED_UNSHARE` (escape succeeds).
- Profile ON:  `unshare -Ur` → `BLOCKED`.
- Only the profile changed → seccomp, not another layer, is the control. Script:
  `testdata/adversarial/syscall_blocked.sh`.

### S7 — Cache effectiveness (UNMET, confirmed) — COLLECTED
Same dep-heavy job (regex+md5+rand) ×5 consecutively in ONE warm container (no recycling):
3807, 4194, 3859, 3646, 3954 ms. **Flat — no downward trend, no cross-job cache.**
Each run recompiles the identical crate code from scratch (fresh per-job target dir).
(Absolute times ~3.8 s here vs ~6.8 s in the earlier record; the finding — flatness, no
amortisation — is what matters and is unchanged.)

---

## Consolidated criteria table (328-job workload)

| # | Criterion | Target | Measured (2026-08, 328 jobs) | Verdict |
|---|---|---|---|---|
| S1 | Assignment latency | < 2 s | warm p50 333 ns (per-job baseline 28.8 ms) | pass |
| S2 | Pipeline latency | < 30 s | mean 4.2 s, p95 4.9 s, p99 5.4 s; 1 buggy-runtime outlier ~45 s | pass (327/328) |
| S3 | Image footprint | minimal | Rust 1.99 GB (−62%), Go 984 MB | pass |
| S4 | Reclamation / no leak | bounded, 0 leak | 0 leaked over 656+80 jobs; zombies 12→105 no-recycle, recycling bounds | pass |
| S5 | Adversarial containment | ≥ 95% | 4/4 classes; seccomp on/off ablation clean; profile-complete over 328 | pass |
| S6 | Verdict correctness | low FP/FN | **326/328, 0 FP, 2 FN** | pass |
| S7 | Cache effectiveness | speedup | flat (no amortisation), ~3 s/job repeated dep compile | unmet |
| Ablation | warm vs container-per-job | (own baseline) | assignment 333 ns vs 28.8 ms (~86,000×); total +32 ms/job | — |

## Per-criterion evidence source (applies the methodology notes above)

Lead every criterion with its ablation; keep the target only as a "pre-registered bar, met"
line (see methodology §2, §6). Which evidence carries which criterion:
- S1 → ablation (warm vs per-job): 333 ns vs 28.8 ms. STRONG.
- S2 → workload-weight breakdown (dep-light ~0.85 s vs dep-heavy ~4 s) + S7 gap. The pool
  ablation is WEAK for S2 (only +32 ms; compilation dominates) — do NOT sell S2 on it.
- S3 → ablation vs un-slimmed image (1.99 vs 5.18 GB).
- S4 → ablation recycle vs no-recycle (0 leaks/bounded vs zombies→105).
- S5 → ablation seccomp on/off (BLOCKED vs REACHED_UNSHARE).
- S7 → ablation cached vs uncached — shows NO benefit (flat), the honest miss.
- Keep a small criteria table (targets met) but make all prose arguments ablations.

## Thesis reconciliation (changes vs current Chapter 5 draft)

1. **Workload is now 328 jobs (164 problems), not 66.** Pooled all HumanEval-X across the
   old train/held-out split (justified: no model trained → split doesn't bind an
   infrastructure eval). Correctness base is 5× larger; 0-FP bound tightens to ~1.8%.
2. **False-negative cause is misattributed.** Both FNs (`Rust/50`, `Rust/32`) are
   `rand::gen_range` crate-version drift (old 2-arg API vs vendored 1-arg), NOT
   `std::ascii::AsciiExt` (which is only a warning). Fix §5 line 903. This is a
   *dependency-version-drift* story, which fits the prevalence/vendoring narrative better.
3. **Two FNs now, not one.** `Rust/32` surfaced only at 164 problems — scaling exposed it,
   strengthening the "benchmarks drift" finding.
4. **S2 has a buggy-runtime outlier.** 327/328 ≤ 5.4 s (p99); `Rust/156#buggy` reproducibly
   ~45 s (slow execution of buggy code, under TTL). Report "p99 5.4 s, one buggy outlier",
   not "max 23 s".
5. **S1 cold-create is ~28.8 ms, not ~0.3 s.** Reframe S1 as "assignment reduced ~86,000×
   and made deterministic". The ablation's total win is ~32 ms/job (compilation dominates),
   so do NOT claim "container creation would otherwise dominate" — it does not, for dep-heavy jobs.
6. **Concurrency/peak-container evidence is serial-only.** All runs `--concurrency 1` (OOM
   ceiling). "Peak 4 containers = cap" cannot be reproduced here; drop or re-measure on a
   larger host.

## Provenance
Raw per-job JSONL in session scratchpad: `results_warm.jsonl`, `results_perjob.jsonl`,
`results_seccomp.jsonl`, `cache_results.jsonl`, `soak_noreycle.jsonl`. Workload:
`specs_328.jsonl` (regenerable via `gen-bench` on both combined files). Env: `DOCKER_HOST`
= podman machine socket; image `rust-sandbox` (Rust 1.96); serial `--concurrency 1`.
