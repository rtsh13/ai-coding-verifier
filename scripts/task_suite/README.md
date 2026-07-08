# Task-Suite Builder (PRD Part A)

Reproducible build of the Rust coding task suite described in `docs/prd.md` §2.
Produces a contamination-safe train / held-out split from three upstream sources.

## Sources

| Source | Family | Origin | Rust problems |
|---|---|---|---|
| MultiPL-E HumanEval-Rust | humaneval | machine-translated from HumanEval | 155 |
| HumanEval-X-Rust (HumanEvalPack) | humaneval | hand-written | 164 |
| MultiPL-E MBPP-Rust | mbpp | machine-translated from MBPP | 356 |

The two HumanEval sources trace back to the **same 164 underlying problems**, so the
split is done **by underlying problem ID**, never by dataset (see PRD §2.4–2.5).
MBPP is an independent source and is split by its own stable id.

## Prerequisites

- Python 3.12 (`python3.12`)
- Git, network access to github.com
- `datasets`, `tqdm` (installed into a local venv by the build script)

## Usage

```bash
# 1. Clone upstream repos + build MultiPL-E Rust prompts (idempotent)
scripts/task_suite/01_build_prompts.sh

# 2. Build the split, manifest, and combined jsonl files
data/.venv/bin/python scripts/task_suite/02_build_split.py

# 3. Verify overlaps + duplicate-docstring data-quality checks
data/.venv/bin/python scripts/task_suite/03_verify.py
```

Outputs land in `task_suite/` (committed). Cloned repos and the venv live under
`data/` and are git-ignored (regenerable).

## Reproducibility

The split is deterministic: fixed seed `42`, documented RNG call order, and stable
sort keys independent of file line-order. Re-running from scratch reproduces an
identical `task_suite/split_manifest.json`. See `docs/task-suite-progress.md` for the
exact decisions and result counts.
