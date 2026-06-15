# ADR-001: Baked Dependency Selection for Offline Sandbox Images

| Field      | Value                                        |
|------------|----------------------------------------------|
| Status     | Accepted                                     |
| Date       | 2026-06-15                                   |
| Author     | Ruchit Singh                                 |
| Supervisor | Dr. Luo Mai                                  |
| Scope      | Phase 1 — Docker images & dependency caching |
| Related    | `scripts/mining/`, `testdata/mining/2026-06-15/`, `images/go/go.mod` |

---

## 1. Context

The architecture defined in the project proposal requires each Go and Rust
submission to be compiled and executed inside a sandboxed container with
**`GOPROXY=off`** (for Go) and equivalent offline isolation for Rust. This is
a hard constraint, motivated by:

1. **Security** — disallowing network I/O during job execution prevents
   exfiltration and arbitrary remote code retrieval.
2. **Reproducibility** — frozen dependency versions guarantee that the same
   submission compiles identically every time it is graded.
3. **Latency** — no network round-trips during the compile-execute pipeline.

Offline isolation implies that any third-party dependency a submission imports
must be present in the image's module cache at build time. The base image
therefore bakes a curated set of dependencies via `go mod download` (Go) and
`cargo vendor` (Rust). The question this ADR resolves is:

> **How is the curated dependency list selected, and how is the selection
> justified empirically rather than by intuition?**

The proposal text described the list only as "a curated list of most commonly
used dependencies", which is not defensible in a dissertation viva. A
methodology grounded in observable data is required.

---

## 2. Initial approach — Pareto / cumulative-coverage thresholds

### 2.1 Methodology (as planned)

The first methodology hypothesised that direct-dependency usage in the Go
ecosystem followed a Pareto-style distribution: a small head of universally-used
packages accounting for the majority of import occurrences, followed by a long
tail. Under this assumption, a **coverage-based threshold** was natural:

> Rank packages by total occurrence count across the corpus.
> Compute cumulative coverage. Select the smallest K such that the top K
> packages account for ≥ T% of all direct-dependency occurrences, with
> T ∈ {0.70, 0.75, 0.80, 0.85, 0.90} reported as sensitivity analysis.

This is the standard Pareto cutoff used widely in empirical software-engineering
studies, and it has the methodological appeal that K is derived from the data
rather than imposed.

### 2.2 Implementation

- `scripts/mining/mine_go.py` — fetches the top 1000 starred Go repositories
  via the GitHub Search API, retrieves each repo's root `go.mod`, parses
  direct `require` entries (excluding `// indirect`), and writes per-repo
  records plus aggregated package counts to
  `testdata/mining/2026-06-15/go-raw.json`.
- `scripts/mining/analyze.py` — applies the cumulative-coverage thresholds
  and emits `go-analysis.json` plus a coverage curve PNG.

The mining run completed successfully:

| Statistic                          | Value     |
|------------------------------------|-----------|
| Repos attempted                    | 1000      |
| Repos with parseable root `go.mod` | 891 (89%) |
| Total direct-dependency occurrences| 29,431    |
| Unique packages observed           | 7,103     |

### 2.3 Result

The coverage thresholds yielded the following K values:

| Coverage threshold | K (selected packages) |
|--------------------|-----------------------|
| 70%                | 940                   |
| 75%                | 1,322                 |
| 80%                | 1,885                 |
| 85%                | 2,689                 |
| 90%                | 4,160                 |

None of these values are operationally feasible. Baking 1,885 packages
into a Docker image would produce a multi-gigabyte image with a
multi-hour build time, defeating the latency and footprint targets stated
in the proposal (cold start under 2 s; full pipeline under 30 s).

### 2.4 Why the methodology failed

The cumulative-coverage curve (see `go-coverage-curve.png`) reveals that the
Go dependency distribution is **substantially flatter than Pareto**. The top
package, `github.com/stretchr/testify`, accounts for only ~1.6 % of total
direct-dependency occurrences — not 30 %, 40 %, or 50 % as a Pareto-shaped
distribution would predict. To reach 80 % cumulative coverage, the threshold
must descend deep into the long tail.

This is not a flaw in the mining; it is a real structural property of the
Go ecosystem. The arithmetic forces it: with 891 repositories averaging
~33 direct dependencies each but drawing from a vocabulary of 7,103 unique
packages, the average package appears in only ~4 repos. There is no small
"head" that dominates.

A speculative explanation, worth pursuing in future work: the Go standard
library is unusually comprehensive (`net/http`, `encoding/json`, `database/sql`,
goroutines, etc. are in `std`), so third-party packages tend to address
narrower, more specialised needs rather than core functionality. This
contrasts with, for instance, the Python scientific ecosystem, where
`numpy` / `pandas` / `requests` are near-mandatory across large fractions
of code.

The implication is that **cumulative coverage is the wrong question for
this domain**. It would yield a tractable K only if a small set of
universally-mandatory packages existed; in the Go ecosystem, none does.

---

## 3. Revised approach — repo-prevalence thresholds

### 3.1 Methodology

Instead of asking "what fraction of dependency-import *events* does this
package cover", reframe the question to:

> **"What fraction of repositories directly require this package?"**

This is the **repo-prevalence** of the package: the count of distinct
repositories listing it under `require` (de-duplicated within a repo to one
vote per package), divided by the number of repositories with a parseable
`go.mod`.

A package is selected for baking if its repo-prevalence meets or exceeds a
threshold P. Sensitivity is reported at four levels:
P ∈ {0.05, 0.10, 0.15, 0.20}.

### 3.2 Why this is the right framing

The shift from coverage to prevalence changes the semantic claim under
which a baked package is justified:

| Methodology         | Implicit claim per baked package                       |
|---------------------|--------------------------------------------------------|
| Cumulative coverage | "Accounts for a measurable share of all imports"       |
| Repo prevalence     | "Used by at least P% of typical Go projects"           |

The prevalence claim is what the architecture actually requires. If a
submission is generated against a "typical Go project" distribution, the
baked image needs to satisfy the imports of that typical project — which
is a per-repo question, not a per-import-event question.

The methodology is also robust to repo size. Cumulative coverage is
implicitly weighted by repo size (larger repos contribute more import
events, dominating the count). Repo-prevalence weights every repo equally,
which better reflects the question "is this package broadly chosen by
developers."

### 3.3 Result

Implementation: `scripts/mining/analyze_prevalence.py`. Output:
`go-prevalence-analysis.json`, `go-prevalence-curve.png`.

| Prevalence threshold | K  | Weakest selected package's prevalence |
|----------------------|----|---------------------------------------|
| ≥ 20%                | 13 | 21.44%                                |
| ≥ 15%                | 19 | 16.16%                                |
| **≥ 10%** (primary)  | **42** | **10.10%**                        |
| ≥ 5%                 | 101| 5.05%                                 |

The prevalence curve (`go-prevalence-curve.png`) exhibits a clear
inflection around rank ~40, where the slope transitions from steep
decline to gradual decline. The ≥ 10 % threshold (K=42) lands at this
inflection, providing a principled justification beyond the round-number
appeal of the threshold itself.

### 3.4 Decision

**Adopt repo-prevalence ≥ 10 % as the primary baked-dependency selection
methodology, yielding K = 42 packages for Go.**

Sensitivity at ≥ 20 %, ≥ 15 %, and ≥ 5 % is reported alongside the primary
configuration in the dissertation.

The list of 42 packages, with each package's `most_common_version`, is the
canonical input to `images/go/go.mod`. The mapping from
`go-prevalence-analysis.json` → `go.mod` is mechanical and reproducible.

---

## 4. Limitations and threats to validity

### 4.1 Corpus bias

The corpus is the top 1,000 Go repositories on GitHub by star count. This
biases the sample toward:

- **Infrastructure and cloud-native projects** (Kubernetes, Prometheus,
  Docker / Moby, Traefik, etc.). The K=42 list reflects this — three
  `k8s.io/*` packages appear in the top 30.
- **Mature, popular projects.** Newer projects, internal enterprise code,
  and niche-domain libraries are under-represented.
- **Library authors over application authors.** Highly-starred Go projects
  skew toward developer tooling.

A submission targeting a domain (e.g., embedded systems, financial
modelling, scientific computing in Go) that diverges from the
infrastructure-heavy corpus may import packages absent from the baked set
and fail to compile under `GOPROXY=off`.

### 4.2 89% manifest yield

109 of the 1,000 repos lacked a parseable root `go.mod`. The dominant
causes are multi-module monorepos and pre-modules repositories. Their
exclusion is documented and accepted as a scope constraint, but it may
bias the prevalence numbers slightly toward "well-structured" projects.

### 4.3 Direct-only counting

Indirect (transitive) requires are excluded. This is intentional — they
represent dependencies developers did not *choose*, only inherited — but
it means some packages that are de facto common at the binary level
(`golang.org/x/sys`, certain protobuf runtime libraries) may be
under-counted if most projects pull them transitively rather than directly.

### 4.4 Temporal snapshot

The mining was conducted on a single date (2026-06-15). Package
popularity drifts: new packages rise, old ones decay, version churn is
constant. The selection is a point-in-time snapshot, not a stable
ranking. A re-mining run several months out would provide a temporal
stability check; this is left as future work for the dissertation
defence.

### 4.5 Version selection

For each baked package, the version pinned in `images/go/go.mod` is the
`most_common_version` from the corpus. This is the modal version, not
necessarily the latest or the most stable. For most packages this is the
current release; for `golang.org/x/exp` (and similar pseudo-versioned
packages) the modal version is essentially a snapshot, and API stability
is not guaranteed across modal updates.

### 4.6 Cross-language generalisation

This ADR covers Go. The Rust prevalence analysis (via crates.io download
statistics) is methodologically distinct: crates.io publishes download
counts directly, sidestepping the need for repository-level scraping.
The selection criterion for Rust will be addressed in a future ADR. The
underlying principle (data-driven prevalence over Pareto coverage) is
expected to carry over, but the empirical shape of the Rust distribution
may differ.

---

## 5. Reproducibility

To reproduce the analysis from scratch:

```bash
# 1. Set up
python3.12 -m venv .venv
source .venv/bin/activate
pip install -r scripts/mining/requirements.txt

# 2. Authenticate
export GITHUB_TOKEN=<personal access token, no scopes needed>

# 3. Mine
python scripts/mining/mine_go.py --corpus-size 1000

# 4. Analyse (rejected coverage methodology, kept as evidence)
python scripts/mining/analyze.py

# 5. Analyse (adopted prevalence methodology)
python scripts/mining/analyze_prevalence.py
```

All outputs are written to `testdata/mining/<YYYY-MM-DD>/`. The
2026-06-15 snapshot is committed to the repository and treated as the
canonical reference for the dissertation.

---

## 6. For the dissertation

The methodology section should:

1. Open with the offline-isolation requirement (Section 1 above).
2. Present the Pareto / cumulative-coverage approach as the initial
   methodology, with its theoretical motivation.
3. Show the coverage curve and the resulting K values, demonstrating
   empirically that the methodology yields infeasible Ks.
4. Discuss why the Go ecosystem's distribution is flatter than Pareto
   (Section 2.4 hypothesis), framing this as a substantive empirical
   finding rather than a methodology failure.
5. Introduce repo-prevalence as the revised methodology.
6. Show the prevalence curve and report K at four thresholds, selecting
   ≥ 10 % (K=42) as primary.
7. Document the limitations from Section 4 as threats to validity.

Both the coverage curve and the prevalence curve should appear as figures.
The coverage curve is the *evidence* that justifies the methodology
pivot; the prevalence curve is the *anchor* for the adopted methodology.
Showing only the second figure would hide the reasoning trail.

---

## 7. Status of follow-up work

- [ ] Generate `images/go/go.mod` from the 42 selected packages.
- [ ] Write the Go Dockerfile that consumes the generated `go.mod`.
- [ ] Run the offline smoke test (`docker run --network none -e GOPROXY=off …`)
      to confirm a hello-world program importing one or more of the 42
      baked packages compiles successfully.
- [ ] Repeat the methodology for Rust (separate ADR).
- [ ] Optional: re-mine in ~1 month for temporal stability comparison.