#!/usr/bin/env python3
"""
analyze_prevalence.py — Repo-prevalence based threshold analysis.

Reads {language}-raw.json from a mining run, computes what fraction of repos
directly require each package, and emits reports at prevalence thresholds
[0.05, 0.10, 0.15, 0.20].

This methodology was adopted after the cumulative-coverage approach
(see analyze.py) revealed that the Go dependency distribution is too
flat for a Pareto-style threshold to yield a feasible K.

Supports both Go and Rust mining output schemas:
  - Go:   stats.repos_with_gomod, repos[].has_gomod, repos[].direct_deps[].name
  - Rust: metadata.repos_with_cargo_toml, raw[].cargo_toml_found, raw[].direct_deps[].crate

Usage:
  python analyze_prevalence.py --language go
  python analyze_prevalence.py --language rust
  python analyze_prevalence.py --language rust --mining-dir output/2026-06-21
"""

import argparse
import json
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

THRESHOLDS = [0.05, 0.10, 0.15, 0.20]

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_MINING_ROOT = REPO_ROOT / "testdata" / "mining"


# ──────────────────────────────────────────────────────────────
# IO
# ──────────────────────────────────────────────────────────────

def find_latest_mining_dir(root: Path) -> Path:
    candidates = sorted(
        (d for d in root.iterdir() if d.is_dir() and len(d.name) == 10),
        reverse=True,
    )
    if not candidates:
        sys.exit(f"error: no mining directories found under {root}")
    return candidates[0]


def load_raw(path: Path) -> dict:
    with open(path) as f:
        return json.load(f)


# ──────────────────────────────────────────────────────────────
# Schema normalisation
# ──────────────────────────────────────────────────────────────

def normalise(raw: dict, language: str) -> dict:
    """
    Return a uniform dict regardless of Go vs Rust mining output schema.

    Uniform schema:
      {
        "repos_attempted": int,
        "repos_with_manifest": int,
        "mined_at": str,
        "repos": [
          {
            "repo": str,
            "has_manifest": bool,
            "direct_deps": [{"name": str, "version": str}, ...],
          }, ...
        ]
      }
    """
    if language == "go":
        return {
            "repos_attempted": raw["stats"]["repos_attempted"],
            "repos_with_manifest": raw["stats"]["repos_with_gomod"],
            "mined_at": raw["mined_at"],
            "repos": [
                {
                    "repo": r["repo"],
                    "has_manifest": r["has_gomod"],
                    "direct_deps": [
                        {"name": d["name"], "version": d["version"]}
                        for d in r["direct_deps"]
                    ],
                }
                for r in raw["repos"]
            ],
            # Go mining already has aggregated package_counts with version info
            "_package_counts": raw.get("package_counts", []),
        }
    elif language == "rust":
        return {
            "repos_attempted": raw["metadata"]["corpus_size"],
            "repos_with_manifest": raw["metadata"]["repos_with_cargo_toml"],
            "mined_at": raw["metadata"]["mined_at"],
            "repos": [
                {
                    "repo": r["repo"],
                    "has_manifest": r["cargo_toml_found"],
                    "direct_deps": [
                        {"name": d["crate"], "version": d["version"]}
                        for d in r["direct_deps"]
                    ],
                }
                for r in raw["raw"]
            ],
            "_package_counts": [],  # Rust mining doesn't pre-aggregate
        }
    else:
        sys.exit(f"error: unsupported language '{language}'")


# ──────────────────────────────────────────────────────────────
# Prevalence analysis
# ──────────────────────────────────────────────────────────────

def compute_prevalence_curve(normalised: dict) -> list[dict]:
    """
    For each package, compute:
      - repo prevalence (fraction of repos that directly require it)
      - most common version (modal version across repos)
      - version distribution
    """
    repos_with_manifest = normalised["repos_with_manifest"]

    pkg_repo_count: Counter = Counter()
    pkg_versions: dict[str, list[str]] = {}

    for repo in normalised["repos"]:
        if not repo["has_manifest"]:
            continue
        seen = set()
        for dep in repo["direct_deps"]:
            name = dep["name"]
            version = dep["version"]
            if name in seen:
                continue
            seen.add(name)
            pkg_repo_count[name] += 1
            pkg_versions.setdefault(name, []).append(version)

    # If Go mining already has pre-aggregated version info, use it
    pre_agg = {}
    for entry in normalised.get("_package_counts", []):
        pre_agg[entry["name"]] = {
            "most_common_version": entry["most_common_version"],
            "version_distribution": entry["version_distribution"],
        }

    curve = []
    for rank, (pkg_name, repo_count) in enumerate(
        sorted(pkg_repo_count.items(), key=lambda x: x[1], reverse=True),
        start=1,
    ):
        # Compute version distribution from raw data
        ver_counter = Counter(pkg_versions.get(pkg_name, []))
        version_dist = {v: c for v, c in ver_counter.most_common()}
        most_common_ver = ver_counter.most_common(1)[0][0] if ver_counter else "unknown"

        # Prefer pre-aggregated data if available (Go)
        if pkg_name in pre_agg:
            most_common_ver = pre_agg[pkg_name]["most_common_version"]
            version_dist = pre_agg[pkg_name]["version_distribution"]

        curve.append({
            "rank": rank,
            "name": pkg_name,
            "repos_count": repo_count,
            "prevalence": repo_count / repos_with_manifest,
            "most_common_version": most_common_ver,
            "version_distribution": version_dist,
        })

    return curve


def find_k_at_threshold(curve: list[dict], threshold: float) -> int:
    """Smallest K such that all packages ranked <= K have prevalence >= threshold."""
    k = 0
    for entry in curve:
        if entry["prevalence"] >= threshold:
            k = entry["rank"]
        else:
            break
    return k


def build_threshold_report(curve: list[dict]) -> dict:
    report = {}
    for t in THRESHOLDS:
        k = find_k_at_threshold(curve, t)
        selected = curve[:k]
        report[f"{int(t * 100)}"] = {
            "threshold": t,
            "k": k,
            "min_prevalence_in_selection": (
                selected[-1]["prevalence"] if selected else None
            ),
            "packages": [
                {
                    "rank": e["rank"],
                    "name": e["name"],
                    "version": e["most_common_version"],
                    "repos_count": e["repos_count"],
                    "prevalence": round(e["prevalence"], 6),
                }
                for e in selected
            ],
        }
    return report


# ──────────────────────────────────────────────────────────────
# Plotting
# ──────────────────────────────────────────────────────────────

def render_prevalence_chart(curve: list[dict], thresholds_report: dict,
                            language: str, output_path: Path,
                            xlim: int = 200) -> None:
    ranks = [e["rank"] for e in curve[:xlim]]
    prevalences = [e["prevalence"] for e in curve[:xlim]]

    manifest_name = "go.mod" if language == "go" else "Cargo.toml"

    fig, ax = plt.subplots(figsize=(9, 5.5))
    ax.plot(ranks, prevalences, linewidth=1.8, color="#1f77b4")
    ax.fill_between(ranks, prevalences, alpha=0.10, color="#1f77b4")

    colors = ["#d62728", "#ff7f0e", "#2ca02c", "#9467bd"]
    for (t_key, t_data), color in zip(thresholds_report.items(), colors):
        t = t_data["threshold"]
        k = t_data["k"]
        ax.axhline(y=t, linestyle="--", color=color, alpha=0.55, linewidth=1)
        if k > 0 and k <= xlim:
            ax.axvline(x=k, linestyle=":", color=color, alpha=0.55, linewidth=1)
            ax.annotate(
                f"K={k} @ \u2265{int(t*100)}% prevalence",
                xy=(k, t),
                xytext=(k + xlim * 0.02, t + 0.015),
                fontsize=9,
                color=color,
            )

    ax.set_xlabel("Package rank (by repo prevalence)")
    ax.set_ylabel("Repo prevalence (fraction of repos directly requiring package)")
    ax.set_title(
        f"Package prevalence distribution \u2014 {language.title()} "
        f"(top {xlim} of {len(curve)} packages)"
    )
    ax.set_xlim(0, xlim)
    ax.set_ylim(0, max(prevalences) * 1.1)
    ax.grid(True, alpha=0.3)

    fig.tight_layout()
    fig.savefig(output_path, dpi=150)
    plt.close(fig)
    print(f"[chart] wrote {output_path}", file=sys.stderr)


# ──────────────────────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────────────────────

def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mining-dir", type=Path, default=None,
                    help="Path to a dated mining directory. Defaults to latest.")
    ap.add_argument("--language", default="go", choices=["go", "rust"])
    ap.add_argument("--chart-xlim", type=int, default=200,
                    help="X-axis upper bound for the prevalence chart.")
    args = ap.parse_args()

    mining_dir = args.mining_dir or find_latest_mining_dir(DEFAULT_MINING_ROOT)
    raw_path = mining_dir / f"{args.language}-raw.json"
    if not raw_path.exists():
        sys.exit(f"error: {raw_path} not found")

    print(f"[load] {raw_path}", file=sys.stderr)
    raw = load_raw(raw_path)
    normalised = normalise(raw, args.language)

    manifest_name = "go.mod" if args.language == "go" else "Cargo.toml"

    curve = compute_prevalence_curve(normalised)
    thresholds = build_threshold_report(curve)

    output = {
        "language": args.language,
        "methodology": "repo-prevalence threshold (adopted after coverage-based analysis showed flat distribution)",
        "analyzed_at": datetime.now(timezone.utc).isoformat(),
        "source_file": str(raw_path),
        "source_mined_at": normalised["mined_at"],
        "corpus_size": normalised["repos_attempted"],
        "repos_with_manifest": normalised["repos_with_manifest"],
        "unique_packages": len(curve),
        "thresholds": thresholds,
        "prevalence_curve": [
            {
                "rank": e["rank"],
                "name": e["name"],
                "repos_count": e["repos_count"],
                "prevalence": round(e["prevalence"], 6),
                "most_common_version": e["most_common_version"],
            }
            for e in curve
        ],
    }

    out_json = mining_dir / f"{args.language}-prevalence-analysis.json"
    with open(out_json, "w") as f:
        json.dump(output, f, indent=2)
    print(f"[analysis] wrote {out_json}", file=sys.stderr)

    chart_path = mining_dir / f"{args.language}-prevalence-curve.png"
    render_prevalence_chart(curve, thresholds, args.language, chart_path,
                            xlim=args.chart_xlim)

    print("", file=sys.stderr)
    print(f"=== {args.language.upper()} repo-prevalence thresholds ===",
          file=sys.stderr)
    print(f"(corpus: {normalised['repos_with_manifest']} repos with {manifest_name})",
          file=sys.stderr)
    for t_key, t_data in thresholds.items():
        k = t_data["k"]
        min_prev = t_data["min_prevalence_in_selection"]
        if k > 0:
            print(
                f"  \u2265{t_key}%  \u2192  K = {k:>4}  "
                f"(weakest selected package at {min_prev*100:.2f}% prevalence)",
                file=sys.stderr,
            )
        else:
            print(f"  \u2265{t_key}%  \u2192  K = 0  (no packages meet threshold)",
                  file=sys.stderr)


if __name__ == "__main__":
    main()