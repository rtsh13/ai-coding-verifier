# ADR-002: Rust as Primary Evaluation Language

| Field      | Value                                                        |
|------------|--------------------------------------------------------------|
| Status     | Accepted                                                     |
| Date       | 2026-06-22                                                   |
| Author     | Ruchit Singh                                                 |
| Supervisor | Prof. Luo Mai                                                |
| Scope      | Phase 2 — Rust sandbox image, compiler diagnostic comparison |
| Related    | ADR-001, `scripts/mining/mine_rust.py`, `images/rust/`       |

---

## 1. Context

ADR-001 established the empirical dependency selection methodology and proved
the offline sandbox infrastructure using Go as Phase 1. Prof. Mai subsequently
suggested prioritising Rust over Go, noting that Go is uncommon in tool-calling
workflows. His recommendation was directional rather than directive.

Two questions needed empirical answers before committing to Rust as the primary
evaluation language:

> **Q1. Is Rust's compiler diagnostic output structurally richer than Go's for
> the AI verifier's reward signal?**

> **Q2. Does the same dependency mining and offline isolation methodology
> transfer from Go to Rust?**

---

## 2. Decision

Rust is the **primary evaluation language** for the dissertation. Go remains as
the pipeline validation language (Phase 1), proving the container infrastructure
works. All evaluation results, reward signal analysis, and RL training
discussion in the dissertation will use Rust.

---

## 3. Empirical Justification: Compiler Diagnostic Comparison

### 3.1 Error Code Taxonomy

Verified by enumerating `rustc --explain E0001` through `E0810` on rustc 1.75.0
and inspecting Go's compiler output on go1.22.2:

| Metric                                | Go (gc)  | Rust (rustc) | Ratio   |
|---------------------------------------|----------|--------------|---------|
| Documented error codes                | ~150*    | **507**      | 1:3.4   |
| Error codes exposed in compiler output| **0**    | **507**      | —       |
| Semantic categories                   | ~14      | **14+**      | —       |
| Categories unique to language         | 0        | **8**        | —       |
| Error codes in unique categories      | 0        | **~102**     | —       |

\* Go's type checker has ~150 internal codes in `internal/types/errors/codes.go`,
but these are **never exposed** in compiler output. Users see only plain text
messages with no error code identifier.

### 3.2 Unique-to-Rust Error Categories

Eight entire error categories exist in Rust that have no equivalent in Go's type
system. These were verified by compiling intentionally broken programs in both
languages:

| Category              | Example Code | Description                              |
|-----------------------|-------------|------------------------------------------|
| Ownership / move      | E0382       | Use of moved value                       |
| Borrow conflicts      | E0502       | Cannot borrow as mutable while immutable |
| Lifetimes             | E0106       | Missing lifetime specifier               |
| Trait bounds           | E0277       | Trait bound not satisfied                |
| Pattern exhaustiveness| E0004       | Non-exhaustive patterns in match         |
| Unsafe code           | E0133       | Use of unsafe block/function             |
| Macro system          | E0659       | Ambiguous name in macro expansion        |
| Closure captures      | E0373       | Closure may outlive current function     |

These categories produce qualitatively different feedback signals that Go
physically cannot generate, regardless of parsing effort.

### 3.3 Structured Output Format

The critical architectural difference for the AI verifier:

**Go** output format:
```
./main.go:5:17: cannot use "hello" (untyped string constant) as int value
```
- No error code in output
- No `--error-format=json` flag (verified: `go build --help` has no format flag)
- No labeled source spans, no secondary diagnostics, no machine-applicable suggestions
- The verifier must regex-parse free-text stderr to extract any structure

**Rust** output format (via `--error-format=json` / `--message-format=json`):
```json
{
  "code": {"code": "E0308", "explanation": "..."},
  "message": "mismatched types",
  "level": "error",
  "spans": [
    {"label": "expected `i32`, found `&str`", "is_primary": true, ...},
    {"label": "expected due to this", "is_primary": false, ...}
  ],
  "children": [...]
}
```
- Error code, severity, labeled primary and secondary spans, child diagnostics
- Machine-applicable suggestions with applicability ratings
- Native JSON — no parsing layer needed; the verifier schema maps directly

### 3.4 Implication for the Reward Signal

The reward signal pipeline for each language would be architecturally different:

- **Go**: `go build` → regex-parse stderr → classify error → reward
- **Rust**: `cargo build --message-format=json` → `json.parse()` → reward

Rust eliminates the parsing layer entirely. The verifier can also distinguish
fixable errors (machine-applicable suggestion present → higher partial reward)
from fundamental errors (no suggestion → lower reward), creating a
finer-grained reward signal for the GRPO training loop.

---

## 4. Rust Dependency Mining

### 4.1 Methodology

The same repo-prevalence methodology from ADR-001 was applied to Rust:

1. Mined top 1,000 starred Rust repos via GitHub Search API (`mine_rust.py`)
2. Fetched root `Cargo.toml` for each repo
3. Handled three Cargo.toml patterns:
   - **Non-workspace** (e.g. ripgrep): `[dependencies]` at root
   - **Workspace with `[workspace.dependencies]`** (e.g. alacritty): used as dependency set
   - **Workspace without** (e.g. tokio, actix-web): fetched primary subcrate by matching repo name against workspace members
4. Computed repo-prevalence: fraction of repos directly requiring each crate
5. `tomli` used instead of `toml` for Python 3.9.6 TOML 1.0 compatibility (bevy's `Cargo.toml` failed to parse under the `toml` library)

### 4.2 Results

| Metric                    | Go (ADR-001) | Rust         |
|---------------------------|-------------|--------------|
| Repos mined               | 1,000       | 1,000        |
| Repos with manifest       | 891         | 899          |
| Unique packages           | ~8,400      | ~2,179       |
| K at ≥20% prevalence      | 13          | 21           |
| K at ≥15% prevalence      | 19          | 30           |
| K at ≥10% prevalence      | **42**      | **52**       |
| K at ≥5% prevalence       | 101         | 117          |

**Selected threshold: ≥10% prevalence → K=52 crates**, consistent with Go.

Top 10 crates by prevalence:

| Rank | Crate              | Prevalence |
|------|--------------------|-----------|
| 1    | serde              | 67.4%     |
| 2    | serde_json         | 67.4%     |
| 3    | clap               | 51.2%     |
| 4    | anyhow             | 51.2%     |
| 5    | regex              | 51.2%     |
| 6    | chrono             | 48.8%     |
| 7    | tokio              | 48.8%     |
| 8    | tracing            | 46.5%     |
| 9    | thiserror          | 44.2%     |
| 10   | tempfile           | 44.2%     |

---

## 5. Offline Isolation Mechanism

### 5.1 Decision: `cargo vendor` over Registry Mirror

`cargo vendor` was chosen as the offline isolation mechanism. It is the direct
analogue of Go's `GOPROXY=off` + baked module cache approach:

- `cargo vendor` dumps all crate sources (including transitive dependencies)
  into a `vendor/` directory
- `.cargo/config.toml` configures source replacement to point at `vendor/`
- `CARGO_NET_OFFLINE=true` prevents any network access at runtime

A local registry mirror (e.g. Panamax, Crates.io mirror) was considered and
rejected: it requires running a server process inside the container, adds
complexity, and solves a problem (dynamic resolution) that the sandbox
explicitly does not need.

### 5.2 Container Image

| Property            | Go (ADR-001)         | Rust                              |
|---------------------|---------------------|-----------------------------------|
| Base image          | `golang:1.26-alpine`| `rust:1.96-alpine`                |
| Offline mechanism   | `GOPROXY=off` + `GOSUMDB=off` | `cargo vendor` + `CARGO_NET_OFFLINE=true` |
| System deps         | none (CGO disabled) | `musl-dev`, `openssl-dev`, `pkgconfig` |
| User                | sandbox (UID 1000)  | sandbox (UID 1000)                |
| Multi-stage build   | Yes                 | Yes                               |
| Pre-compiled deps   | No                  | Yes (both debug + release)        |

The `openssl-dev` and `pkgconfig` packages are required because `reqwest`
(41.9% prevalence) pulls in `openssl-sys`, which has a build script that
links against system OpenSSL. This is a Rust-specific concern with no Go
equivalent (Go's `CGO_ENABLED=0` avoids all C library dependencies).

### 5.3 Smoke Test

```
$ podman run --rm --network none rust-sandbox rustc --version
rustc 1.96.0 (ac68faa20 2026-05-25)
```

End-to-end test with vendored dependency and intentional compilation error:

```
$ podman run --rm --network none rust-sandbox sh -c '
    mkdir -p /tmp/exec/test/src && cd /tmp/exec/test
    echo "[package]\nname=\"t\"\nversion=\"0.1.0\"\nedition=\"2021\"\n[dependencies]\nserde=\"1\"" > Cargo.toml
    echo "fn main() { let x: i32 = \"hello\"; }" > src/main.rs
    cargo build --offline --message-format=json 2>&1 | grep E0308'
```

Result: structured JSON with `"code":{"code":"E0308"}`, labeled spans, and
secondary diagnostics — confirming the verifier's reward signal pipeline
requires only JSON parsing, no regex.

---

## 6. Artifacts

| Artifact                                     | Path                                      |
|----------------------------------------------|-------------------------------------------|
| Rust mining script                           | `scripts/mining/mine_rust.py`             |
| Prevalence analysis (language-agnostic)      | `scripts/mining/analyze_prevalence.py`    |
| Cargo.toml generator                         | `scripts/mining/generate_cargo_toml.py`   |
| Mining output                                | `testdata/mining/2026-06-21/rust-raw.json`|
| Prevalence analysis output                   | `testdata/mining/2026-06-21/rust-prevalence-analysis.json` |
| Generated Cargo.toml                         | `images/rust/Cargo.toml`                  |
| Vendored sources                             | `images/rust/vendor/`                     |
| Cargo vendor config                          | `images/rust/.cargo/config.toml`          |
| Dockerfile                                   | `images/rust/Dockerfile`                  |
| Compiler diagnostic comparison               | `testdata/analysis/compiler_diagnostic_comparison.py` |

---

## 7. What This Changes

The original framing was "Go validates the pipeline, Rust is richer for
evaluation." The empirical comparison revealed the gap is larger than
anticipated: the verifier architecture is fundamentally different for each
language. Go requires a custom parsing layer to extract any structure from
stderr; Rust eliminates that layer entirely via native JSON diagnostics.

This means Go Phase 1 proved the **container infrastructure** (sandbox, pool,
offline isolation, Dockerfile pattern) but did **not** validate the reward
signal pipeline, which is architecturally simpler for Rust. The dissertation
evaluation will focus on Rust, with Go referenced only as infrastructure
validation.