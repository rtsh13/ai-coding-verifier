#!/usr/bin/env python3
"""PRD Part A, step 2: build the contamination-safe split + combined task suite.

Reads the three source datasets, assigns every problem to `train` or `heldout`,
and writes a self-contained, committable task suite under `task_suite/`.

Key design decisions (see docs/task-suite-progress.md for the rationale):

1.  HumanEval family (MultiPL-E-HE + HumanEval-X) is split BY UNDERLYING PROBLEM
    ID, not by dataset — both are renderings of the same 164 HumanEval problems,
    so a by-dataset split would leak reworded train problems into held-out
    (PRD §2.4-2.5).
2.  Held-out HumanEval uses ONLY the hand-written HumanEval-X rendering (used
    once). Both renderings of *train*-ID problems are kept for training diversity.
3.  MBPP is an independent source with no contamination risk, so it is split by
    its own stable problem id (PRD §2.5).
4.  Determinism: fixed seed 42; RNG order is HumanEval-ids-first then MBPP-ids;
    MBPP ids are sorted before shuffling so the split does not depend on jsonl
    line order. Re-running reproduces an identical manifest.
5.  The two families use different Rust test harnesses, so every combined record
    carries a `harness` tag ("multipl-e" -> run via `fn main`; "humanevalpack"
    -> run via `cargo test`) plus the full original record under `raw`.
"""
from __future__ import annotations

import hashlib
import json
import random
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC = ROOT / "data" / "sources"
OUT = ROOT / "task_suite"
OUT_SRC = OUT / "sources"

SEED = 42
HELD_RATIO = 0.20  # 20% held-out, 80% train

MULTIPLE_HE = SRC / "MultiPL-E" / "prompts" / "humaneval-rs.jsonl"
MULTIPLE_MBPP = SRC / "MultiPL-E" / "prompts" / "mbpp-rs.jsonl"
HUMANEVAL_X = (
    SRC / "octopack" / "evaluation" / "create" / "humaneval-x"
    / "data" / "rust" / "data" / "humanevalpack.jsonl"
)

# Default stop tokens for the HumanEval-X (humanevalpack) harness. MultiPL-E
# ships its own per-problem stop_tokens; HumanEval-X does not, so we supply a
# conservative default that cuts generation at the end of the function body or
# the start of the appended test / main scaffolding.
HUMANEVALPACK_STOP = ["\n}\n", "\nfn main", "\n#[cfg(test)]", "\nmod tests"]

FN_RE = re.compile(r"\bfn\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:<[^>]*>)?\s*\(")


def read_jsonl(path: Path) -> list[dict]:
    with path.open() as f:
        return [json.loads(line) for line in f if line.strip()]


def write_jsonl(path: Path, records: list[dict]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w") as f:
        for r in records:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def he_id_from_multiple(name: str) -> int:
    # "HumanEval_0_has_close_elements" -> 0
    return int(name.split("_")[1])


def he_id_from_x(task_id: str) -> int:
    # "Rust/0" -> 0
    return int(task_id.split("/")[1])


def mbpp_id_from_multiple(name: str) -> int:
    # "mbpp_3_is_not_prime" -> 3  (stable MBPP problem number)
    return int(name.split("_")[1])


def entry_point_from_prompt(prompt: str) -> str | None:
    m = FN_RE.search(prompt)
    return m.group(1) if m else None


def main() -> None:
    multiple_he = read_jsonl(MULTIPLE_HE)
    multiple_mbpp = read_jsonl(MULTIPLE_MBPP)
    humaneval_x = read_jsonl(HUMANEVAL_X)

    # Build the split ID sets (deterministic)
    rng = random.Random(SEED)

    all_he_ids = list(range(164))
    rng.shuffle(all_he_ids)
    he_cut = int(len(all_he_ids) * (1 - HELD_RATIO))  # 131
    he_train_ids = set(all_he_ids[:he_cut])
    he_heldout_ids = set(all_he_ids[he_cut:])

    mbpp_ids = sorted(mbpp_id_from_multiple(r["name"]) for r in multiple_mbpp)
    rng.shuffle(mbpp_ids)
    mbpp_cut = int(len(mbpp_ids) * (1 - HELD_RATIO))  # 284
    mbpp_train_ids = set(mbpp_ids[:mbpp_cut])
    mbpp_heldout_ids = set(mbpp_ids[mbpp_cut:])

    # Emit combined records
    train: list[dict] = []
    heldout: list[dict] = []

    # MBPP (both splits) -- MultiPL-E rendering
    for r in multiple_mbpp:
        uid = mbpp_id_from_multiple(r["name"])
        split = "train" if uid in mbpp_train_ids else "heldout"
        rec = {
            "id": f"multipl-e-mbpp/{r['name']}",
            "source": "multipl-e-mbpp",
            "family": "mbpp",
            "underlying_id": uid,
            "split": split,
            "harness": "multipl-e",
            "prompt": r["prompt"],
            "tests": r["tests"],
            "stop_tokens": json.loads(r["stop_tokens"]) if isinstance(r["stop_tokens"], str) else r["stop_tokens"],
            "entry_point": entry_point_from_prompt(r["prompt"]),
            "raw": r,
        }
        (train if split == "train" else heldout).append(rec)

    # HumanEval family -- MultiPL-E rendering: TRAIN-ID problems only.
    # (Held-out IDs from MultiPL-E are deliberately dropped; held-out uses the
    #  hand-written HumanEval-X rendering exactly once.)
    for r in multiple_he:
        uid = he_id_from_multiple(r["name"])
        if uid not in he_train_ids:
            continue
        train.append({
            "id": f"multipl-e-humaneval/{r['name']}",
            "source": "multipl-e-humaneval",
            "family": "humaneval",
            "underlying_id": uid,
            "split": "train",
            "harness": "multipl-e",
            "prompt": r["prompt"],
            "tests": r["tests"],
            "stop_tokens": json.loads(r["stop_tokens"]) if isinstance(r["stop_tokens"], str) else r["stop_tokens"],
            "entry_point": entry_point_from_prompt(r["prompt"]),
            "raw": r,
        })

    # HumanEval family -- HumanEval-X (hand-written) rendering: BOTH splits.
    for r in humaneval_x:
        uid = he_id_from_x(r["task_id"])
        split = "train" if uid in he_train_ids else "heldout"
        rec = {
            "id": f"humaneval-x/{r['task_id']}",
            "source": "humaneval-x",
            "family": "humaneval",
            "underlying_id": uid,
            "split": split,
            "harness": "humanevalpack",
            "prompt": r["prompt"],
            "tests": r["test"],
            "stop_tokens": HUMANEVALPACK_STOP,
            "entry_point": r.get("entry_point"),
            "raw": r,
        }
        (train if split == "train" else heldout).append(rec)

    # Stable ordering for reproducible file output.
    train.sort(key=lambda x: x["id"])
    heldout.sort(key=lambda x: x["id"])

    # Write outputs
    # Self-contained copies of the source renderings (data/ is git-ignored).
    write_jsonl(OUT_SRC / "multipl-e-humaneval-rs.jsonl", multiple_he)
    write_jsonl(OUT_SRC / "multipl-e-mbpp-rs.jsonl", multiple_mbpp)
    write_jsonl(OUT_SRC / "humaneval-x-rust.jsonl", humaneval_x)

    write_jsonl(OUT / "train_combined.jsonl", train)
    write_jsonl(OUT / "heldout_combined.jsonl", heldout)

    def count(records, src):
        return sum(1 for r in records if r["source"] == src)

    manifest = {
        "seed": SEED,
        "held_out_ratio": HELD_RATIO,
        "generated_by": "scripts/task_suite/02_build_split.py",
        "sources": {
            "multipl-e-humaneval": {"path": str(MULTIPLE_HE.relative_to(ROOT)), "sha256": sha256(MULTIPLE_HE), "count": len(multiple_he)},
            "multipl-e-mbpp": {"path": str(MULTIPLE_MBPP.relative_to(ROOT)), "sha256": sha256(MULTIPLE_MBPP), "count": len(multiple_mbpp)},
            "humaneval-x": {"path": str(HUMANEVAL_X.relative_to(ROOT)), "sha256": sha256(HUMANEVAL_X), "count": len(humaneval_x)},
        },
        "humaneval_split": {
            "train_ids": sorted(he_train_ids),
            "heldout_ids": sorted(he_heldout_ids),
            "n_train_ids": len(he_train_ids),
            "n_heldout_ids": len(he_heldout_ids),
        },
        "mbpp_split": {
            "train_ids": sorted(mbpp_train_ids),
            "heldout_ids": sorted(mbpp_heldout_ids),
            "n_train_ids": len(mbpp_train_ids),
            "n_heldout_ids": len(mbpp_heldout_ids),
        },
        "counts": {
            "train": {
                "multipl-e-mbpp": count(train, "multipl-e-mbpp"),
                "multipl-e-humaneval": count(train, "multipl-e-humaneval"),
                "humaneval-x": count(train, "humaneval-x"),
                "total": len(train),
            },
            "heldout": {
                "multipl-e-mbpp": count(heldout, "multipl-e-mbpp"),
                "humaneval-x": count(heldout, "humaneval-x"),
                "total": len(heldout),
            },
        },
    }
    (OUT / "split_manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")

    # Console summary + invariant assertions
    print("== Source counts ==")
    print(f"  MultiPL-E HumanEval-Rust : {len(multiple_he)}")
    print(f"  MultiPL-E MBPP-Rust      : {len(multiple_mbpp)}")
    print(f"  HumanEval-X-Rust         : {len(humaneval_x)}")
    print("== Train ==")
    for k, v in manifest["counts"]["train"].items():
        print(f"  {k:24}: {v}")
    print("== Held-out ==")
    for k, v in manifest["counts"]["heldout"].items():
        print(f"  {k:24}: {v}")

    # Contamination invariant: no underlying HE id appears in both splits.
    train_he = {r["underlying_id"] for r in train if r["family"] == "humaneval"}
    held_he = {r["underlying_id"] for r in heldout if r["family"] == "humaneval"}
    assert not (train_he & held_he), f"HE id leak: {train_he & held_he}"
    train_mbpp = {r["underlying_id"] for r in train if r["family"] == "mbpp"}
    held_mbpp = {r["underlying_id"] for r in heldout if r["family"] == "mbpp"}
    assert not (train_mbpp & held_mbpp), f"MBPP id leak: {train_mbpp & held_mbpp}"
    print("\nOK: no underlying-ID overlap between train and held-out (HE + MBPP).")


if __name__ == "__main__":
    main()
