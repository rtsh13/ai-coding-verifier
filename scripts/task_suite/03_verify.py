#!/usr/bin/env python3
"""PRD Part A, step 3: data-quality + contamination verification (PRD §2.6).

Two checks that would silently corrupt a held-out evaluation if missed:

  (a) Does MBPP-Rust secretly overlap with HumanEval-X-Rust?  Compared by
      entry-point/function name; any name collision is inspected to confirm it
      is a coincidental name reuse, not a duplicate task.

  (b) Is HumanEval-X-Rust internally clean?  Scan for problems that share an
      identical docstring (a signature of copy-paste translation errors), then
      report which split each affected task_id landed in — the held-out set must
      not contain a known-buggy instance.

Exits non-zero if a *new* problem appears that the PRD did not already diagnose
(e.g. a real duplicate task across sources, or a buggy id that leaked into
held-out), so this doubles as a regression guard when the split is regenerated.
"""
from __future__ import annotations

import json
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = ROOT / "task_suite"

# Name collisions the PRD already inspected and cleared as coincidental
# (different tasks reusing a common function name) — PRD §2.6(a).
KNOWN_NAME_COLLISIONS = {"maximum", "search", "triangle_area", "decimal_to_binary"}

# Duplicate-docstring pairs the PRD already diagnosed — PRD §2.6(b).
KNOWN_DOCSTRING_PAIRS = {
    frozenset({"Rust/45", "Rust/71"}),
    frozenset({"Rust/88", "Rust/116"}),
    frozenset({"Rust/133", "Rust/142"}),
}


def read_jsonl(path: Path) -> list[dict]:
    with path.open() as f:
        return [json.loads(line) for line in f if line.strip()]


def main() -> int:
    problems = 0
    he_x = read_jsonl(OUT / "sources" / "humaneval-x-rust.jsonl")
    mbpp = read_jsonl(OUT / "sources" / "multipl-e-mbpp-rs.jsonl")
    train = read_jsonl(OUT / "train_combined.jsonl")
    heldout = read_jsonl(OUT / "heldout_combined.jsonl")

    split_of = {}
    for r in train:
        split_of[r["id"]] = "train"
    for r in heldout:
        split_of[r["id"]] = "heldout"

    # ---- (a) MBPP <-> HumanEval-X name overlap ----------------------------
    print("== (a) MBPP <-> HumanEval-X entry-point name overlap ==")
    he_names = {r["entry_point"]: r["task_id"] for r in he_x}

    def mbpp_fn(rec):
        # "mbpp_3_is_not_prime" -> "is_not_prime"
        return rec["name"].split("_", 2)[2]

    mbpp_names = defaultdict(list)
    for r in mbpp:
        mbpp_names[mbpp_fn(r)].append(r["name"])

    collisions = sorted(set(he_names) & set(mbpp_names))
    print(f"  shared function names: {collisions or 'none'}")
    unexpected = set(collisions) - KNOWN_NAME_COLLISIONS
    if unexpected:
        problems += 1
        print(f"  !! NEW name collision(s) not pre-cleared by PRD: {sorted(unexpected)}")
        print("     -> inspect docstrings/solutions to confirm not a real duplicate task.")
    else:
        print("  all collisions are PRD-pre-cleared coincidental name reuse. OK.")

    # ---- (b) HumanEval-X duplicate-docstring scan -------------------------
    print("\n== (b) HumanEval-X duplicate-docstring scan ==")
    docstring_to_ids = defaultdict(list)
    for r in he_x:
        docstring_to_ids[r["docstring"].strip()].append(r["task_id"])
    suspects = {d: ids for d, ids in docstring_to_ids.items() if len(ids) > 1}

    found_pairs = {frozenset(ids) for ids in suspects.values()}
    for ids in sorted(suspects.values()):
        splits = {tid: split_of.get(f"humaneval-x/{tid}", "??") for tid in ids}
        print(f"  shared docstring: {ids} -> splits {splits}")
        # A known-buggy instance must never sit in held-out. The PRD verified
        # only the clean half of the Rust/45<->71 pair (Rust/71) is in held-out.
        held = [t for t, s in splits.items() if s == "heldout"]
        if len(ids) == len(held):
            problems += 1
            print(f"    !! BOTH members of a duplicate pair are in held-out: {ids}")

    new_pairs = found_pairs - KNOWN_DOCSTRING_PAIRS
    if new_pairs:
        problems += 1
        print(f"  !! NEW duplicate-docstring pair(s) not diagnosed by PRD: "
              f"{[sorted(p) for p in new_pairs]}")
    else:
        print("  all duplicate-docstring pairs are PRD-diagnosed. OK.")

    # Held-out cleanliness: none of the PRD's known-buggy ids may be in held-out.
    # PRD §2.6(b): Rust/45, 88, 116, 133, 142 are the buggy/ambiguous instances;
    # only Rust/71 (clean) is expected in held-out.
    KNOWN_BUGGY = ["Rust/45", "Rust/88", "Rust/116", "Rust/133", "Rust/142"]
    leaked = [t for t in KNOWN_BUGGY if split_of.get(f"humaneval-x/{t}") == "heldout"]
    print("\n== held-out cleanliness ==")
    if leaked:
        problems += 1
        print(f"  !! known-buggy id(s) leaked into held-out: {leaked}")
    else:
        print(f"  none of {KNOWN_BUGGY} are in held-out. Held-out set is clean. OK.")

    print()
    if problems:
        print(f"FAIL: {problems} issue(s) need attention (see !! lines above).")
        return 1
    print("PASS: all §2.6 checks clear; split matches PRD diagnoses.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
