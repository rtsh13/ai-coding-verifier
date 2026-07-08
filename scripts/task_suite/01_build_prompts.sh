#!/usr/bin/env bash
# PRD Part A, step 1: clone upstream sources and build MultiPL-E Rust prompts.
# Idempotent: safe to re-run. All heavy artifacts land under data/ (git-ignored).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="$ROOT/data/sources"
VENV="$ROOT/data/.venv"
PY="$VENV/bin/python"

mkdir -p "$SRC"

# --- Python venv with datasets + tqdm (PRD §1) -----------------------------
# We use an isolated venv rather than `pip install --break-system-packages`
# so we never mutate the system/homebrew Python.
if [ ! -x "$PY" ]; then
  echo ">> creating venv at $VENV"
  python3.12 -m venv "$VENV"
  "$PY" -m pip install --quiet --upgrade pip
  "$PY" -m pip install --quiet datasets tqdm
fi

# --- Source 1: MultiPL-E (PRD §2.2) ----------------------------------------
if [ ! -d "$SRC/MultiPL-E/.git" ]; then
  echo ">> cloning MultiPL-E"
  git clone --depth 1 https://github.com/nuprl/MultiPL-E.git "$SRC/MultiPL-E"
fi

# --- Source 2: octopack / HumanEvalPack (PRD §2.3) -------------------------
# Raw Rust data ships in the repo; no build step needed.
if [ ! -d "$SRC/octopack/.git" ]; then
  echo ">> cloning octopack"
  git clone --depth 1 https://github.com/bigcode-project/octopack.git "$SRC/octopack"
fi

# --- Build MultiPL-E Rust prompts (PRD §2.2) -------------------------------
cd "$SRC/MultiPL-E/dataset_builder"
mkdir -p ../prompts

echo ">> building HumanEval -> Rust prompts"
"$PY" prepare_prompts_for_hfhub.py \
  --lang humaneval_to_rs.py \
  --original-dataset humaneval \
  --originals ../datasets/originals-with-cleaned-doctests \
  --output "jsonl:../prompts/humaneval-rs.jsonl"

# NOTE: use datasets/mbpp-typed, NOT datasets/mbpp — the plain mbpp/ folder
# uses tabs and untyped signatures that break the translator's Python parser.
echo ">> building MBPP -> Rust prompts"
"$PY" prepare_prompts_for_hfhub.py \
  --lang humaneval_to_rs.py \
  --original-dataset mbpp \
  --originals ../datasets/mbpp-typed \
  --output "jsonl:../prompts/mbpp-rs.jsonl"

echo ">> done:"
wc -l ../prompts/humaneval-rs.jsonl ../prompts/mbpp-rs.jsonl
