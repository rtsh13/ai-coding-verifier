#!/usr/bin/env python3
"""
generate_gomod.py — Generate images/go/go.mod from prevalence analysis.

Reads go-prevalence-analysis.json from a mining directory, extracts the
packages at the specified threshold, and writes a go.mod file suitable
for baking into the Go sandbox image.

Usage:
  python generate_gomod.py                          # ≥10% threshold (primary)
  python generate_gomod.py --threshold 5            # ≥5% (broader)
  python generate_gomod.py --mining-dir testdata/mining/2026-06-15
"""

import argparse
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MINING_ROOT = REPO_ROOT / "testdata" / "mining"
DEFAULT_OUTPUT = REPO_ROOT / "images" / "go" / "go.mod"
GO_VERSION = "1.26"
MODULE_NAME = "sandbox/baked"


def find_latest_mining_dir(root: Path) -> Path:
    candidates = sorted(
        (d for d in root.iterdir() if d.is_dir() and len(d.name) == 10),
        reverse=True,
    )
    if not candidates:
        sys.exit(f"error: no mining directories found under {root}")
    return candidates[0]


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mining-dir", type=Path, default=None,
                    help="Mining dir. Defaults to latest.")
    ap.add_argument("--threshold", type=int, default=10,
                    choices=[5, 10, 15, 20],
                    help="Prevalence threshold percentage. Default 10.")
    ap.add_argument("--output", type=Path, default=DEFAULT_OUTPUT,
                    help="Output path for go.mod")
    args = ap.parse_args()

    mining_dir = args.mining_dir or find_latest_mining_dir(DEFAULT_MINING_ROOT)
    analysis_path = mining_dir / "go-prevalence-analysis.json"
    if not analysis_path.exists():
        sys.exit(f"error: {analysis_path} not found. Run analyze_prevalence.py first.")

    with open(analysis_path) as f:
        analysis = json.load(f)

    threshold_key = str(args.threshold)
    if threshold_key not in analysis["thresholds"]:
        sys.exit(f"error: threshold {threshold_key} not in analysis.")

    selection = analysis["thresholds"][threshold_key]
    k = selection["k"]
    packages = selection["packages"]

    # Build go.mod content
    lines = [
        f"// Baked dependency manifest for the Go sandbox image.",
        f"//",
        f"// This file is GENERATED — do not edit by hand.",
        f"// Source: {analysis['source_file']}",
        f"// Mined:  {analysis['source_mined_at']}",
        f"// Methodology: repo-prevalence ≥ {args.threshold}% (see ADR-001)",
        f"// K = {k} packages, generated {datetime.now(timezone.utc).isoformat()}",
        f"",
        f"module {MODULE_NAME}",
        f"",
        f"go {GO_VERSION}",
        f"",
        f"require (",
    ]

    # Sort packages alphabetically for stable diffs
    sorted_pkgs = sorted(packages, key=lambda p: p["name"])
    name_width = max(len(p["name"]) for p in sorted_pkgs)

    for pkg in sorted_pkgs:
        # Format: \t<name padded> <version>
        lines.append(f"\t{pkg['name']:<{name_width}}  {pkg['version']}")

    lines.append(")")
    lines.append("")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("\n".join(lines))

    print(f"[generate] wrote {args.output}", file=sys.stderr)
    print(f"[generate] {k} packages at ≥{args.threshold}% prevalence",
          file=sys.stderr)


if __name__ == "__main__":
    main()