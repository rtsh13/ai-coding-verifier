#!/usr/bin/env python3
"""
generate_cargo_toml.py — Generate images/rust/Cargo.toml from prevalence analysis.

Reads rust-prevalence-analysis.json from a mining directory, extracts the
crates at the specified threshold, and writes a Cargo.toml suitable for
baking into the Rust sandbox image via `cargo vendor`.

This is a synthetic project that exists purely to drive `cargo vendor` —
the same pattern as generate_gomod.py for Go.

Usage:
  python generate_cargo_toml.py                          # ≥10% threshold (primary)
  python generate_cargo_toml.py --threshold 5            # ≥5% (broader)
  python generate_cargo_toml.py --mining-dir testdata/mining/2026-06-21
"""

import argparse
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MINING_ROOT = REPO_ROOT / "testdata" / "mining"
DEFAULT_OUTPUT = REPO_ROOT / "images" / "rust" / "Cargo.toml"
RUST_EDITION = "2021"
PACKAGE_NAME = "sandbox-baked"


def find_latest_mining_dir(root: Path) -> Path:
    candidates = sorted(
        (d for d in root.iterdir() if d.is_dir() and len(d.name) == 10),
        reverse=True,
    )
    if not candidates:
        sys.exit(f"error: no mining directories found under {root}")
    return candidates[0]


def normalise_version(version: str) -> str:
    """
    Normalise a version string for Cargo.toml.

    mine_rust.py records whatever version string appears in each repo's
    Cargo.toml. The most_common_version from the prevalence analysis may be:
      "1.0"        → keep as-is (Cargo interprets as ^1.0)
      "1.0.75"     → keep as-is
      "~2.7.0"     → keep as-is (tilde requirement)
      "0.3.17"     → keep as-is
      "<git>"      → replace with "*" (any version)
      "<path>"     → replace with "*"
      ""           → replace with "*"

    For the baked image, we want the most common version to be the floor,
    allowing compatible updates (Cargo's default ^ behaviour).
    """
    if not version or version.startswith("<"):
        return "*"

    # Strip leading ~ or ^ or = if present — we'll let Cargo's default
    # ^ semantics handle compatibility
    cleaned = version.lstrip("~^=")

    # If it looks like a valid semver (possibly partial), return it
    if re.match(r"^\d+(\.\d+)*", cleaned):
        return cleaned

    return "*"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mining-dir", type=Path, default=None,
                    help="Mining dir. Defaults to latest.")
    ap.add_argument("--threshold", type=int, default=10,
                    choices=[5, 10, 15, 20],
                    help="Prevalence threshold percentage. Default 10.")
    ap.add_argument("--output", type=Path, default=DEFAULT_OUTPUT,
                    help="Output path for Cargo.toml")
    args = ap.parse_args()

    mining_dir = args.mining_dir or find_latest_mining_dir(DEFAULT_MINING_ROOT)
    analysis_path = mining_dir / "rust-prevalence-analysis.json"
    if not analysis_path.exists():
        sys.exit(f"error: {analysis_path} not found. Run analyze_prevalence.py --language rust first.")

    with open(analysis_path) as f:
        analysis = json.load(f)

    threshold_key = str(args.threshold)
    if threshold_key not in analysis["thresholds"]:
        sys.exit(f"error: threshold {threshold_key} not in analysis.")

    selection = analysis["thresholds"][threshold_key]
    k = selection["k"]
    packages = selection["packages"]

    if k == 0:
        sys.exit(f"error: no packages meet the ≥{args.threshold}% threshold.")

    # Filter out path/git-only deps that resolved to "*"
    valid_packages = []
    skipped = []
    for pkg in packages:
        ver = normalise_version(pkg["version"])
        if ver == "*":
            skipped.append(pkg["name"])
        else:
            valid_packages.append((pkg["name"], ver, pkg["prevalence"], pkg["repos_count"]))

    if skipped:
        print(f"[generate] skipped {len(skipped)} path/git-only crates: {', '.join(skipped)}",
              file=sys.stderr)

    # Sort alphabetically for stable diffs
    valid_packages.sort(key=lambda p: p[0])
    name_width = max(len(p[0]) for p in valid_packages)

    # Build Cargo.toml content
    lines = [
        f"# Baked dependency manifest for the Rust sandbox image.",
        f"#",
        f"# This file is GENERATED — do not edit by hand.",
        f"# Source: {analysis.get('source_file', 'unknown')}",
        f"# Mined:  {analysis.get('source_mined_at', 'unknown')}",
        f"# Methodology: repo-prevalence ≥ {args.threshold}% (see ADR-001)",
        f"# K = {len(valid_packages)} crates, generated {datetime.now(timezone.utc).isoformat()}",
        f"",
        f"[package]",
        f'name = "{PACKAGE_NAME}"',
        f'version = "0.0.1"',
        f'edition = "{RUST_EDITION}"',
        f"",
        f"[dependencies]",
    ]

    for crate_name, version, prevalence, repo_count in valid_packages:
        # Comment with prevalence for traceability
        lines.append(
            f'{crate_name:<{name_width}} = "{version}"'
            f"  # {prevalence*100:.1f}% ({repo_count} repos)"
        )

    lines.append("")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("\n".join(lines))

    print(f"[generate] wrote {args.output}", file=sys.stderr)
    print(f"[generate] {len(valid_packages)} crates at ≥{args.threshold}% prevalence",
          file=sys.stderr)


if __name__ == "__main__":
    main()