#!/usr/bin/env python3
"""
analyze.py — Coverage-based threshold analysis of mined Go dependencies.

Reads go-raw.json from a mining run, computes cumulative coverage,
emits a JSON report with K selected at thresholds [0.70, 0.75, 0.80, 0.85, 0.90],
and renders a matplotlib chart of the coverage curve.

Usage:
  python analyze.py                                  # latest mining run
  python analyze.py --mining-dir testdata/mining/2026-06-15
  python analyze.py --language go
"""

import argparse
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

import matplotlib.pyplot as plt

THRESHOLDS = [0.70, 0.75, 0.80, 0.85, 0.90]

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
# Coverage analysis
# ──────────────────────────────────────────────────────────────

def compute_coverage_curve(package_counts: list[dict], total_occurrences: int) -> list[dict]:
    """
    Build the cumulative coverage curve.

    Each entry: { rank, name, repos_count, occurrence_share, cumulative_coverage,
                  most_common_version, version_distribution }
    """
    curve = []
    cumulative = 0
    for rank, pkg in enumerate(package_counts, start=1):
        count = pkg["repos_count"]
        cumulative += count
        curve.append({
            "rank": rank,
            "name": pkg["name"],
            "repos_count": count,
            "occurrence_share": count / total_occurrences,
            "cumulative_coverage": cumulative / total_occurrences,
            "most_common_version": pkg["most_common_version"],
            "version_distribution": pkg["version_distribution"],
            "example_repos": pkg["example_repos"],
        })
    return curve


def find_k_at_threshold(curve: list[dict], threshold: float) -> int:
    """Smallest K such that cumulative_coverage[K-1] >= threshold."""
    for entry in curve:
        if entry["cumulative_coverage"] >= threshold:
            return entry["rank"]
    return len(curve)  # threshold unreachable — return everything


def build_threshold_report(curve: list[dict]) -> dict:
    report = {}
    for t in THRESHOLDS:
        k = find_k_at_threshold(curve, t)
        selected = curve[:k]
        report[f"{int(t * 100)}"] = {
            "threshold": t,
            "k": k,
            "actual_coverage": selected[-1]["cumulative_coverage"],
            "packages": [
                {
                    "rank": e["rank"],
                    "name": e["name"],
                    "version": e["most_common_version"],
                    "repos_count": e["repos_count"],
                    "occurrence_share": round(e["occurrence_share"], 6),
                    "cumulative_coverage": round(e["cumulative_coverage"], 6),
                }
                for e in selected
            ],
        }
    return report


# ──────────────────────────────────────────────────────────────
# Plotting
# ──────────────────────────────────────────────────────────────

def render_coverage_chart(curve: list[dict], thresholds_report: dict,
                          language: str, output_path: Path) -> None:
    ranks = [e["rank"] for e in curve]
    coverages = [e["cumulative_coverage"] for e in curve]

    fig, ax = plt.subplots(figsize=(9, 5.5))
    ax.plot(ranks, coverages, linewidth=1.8, color="#1f77b4")
    ax.fill_between(ranks, coverages, alpha=0.10, color="#1f77b4")

    # Threshold reference lines + K annotations
    colors = ["#d62728", "#ff7f0e", "#2ca02c", "#9467bd", "#8c564b"]
    for (t_key, t_data), color in zip(thresholds_report.items(), colors):
        t = t_data["threshold"]
        k = t_data["k"]
        ax.axhline(y=t, linestyle="--", color=color, alpha=0.55, linewidth=1)
        ax.axvline(x=k, linestyle=":", color=color, alpha=0.55, linewidth=1)
        ax.annotate(
            f"K={k} @ {int(t*100)}%",
            xy=(k, t),
            xytext=(k + max(ranks) * 0.02, t - 0.04),
            fontsize=9,
            color=color,
        )

    ax.set_xlabel("Package rank (by occurrence count)")
    ax.set_ylabel("Cumulative coverage of direct-dependency occurrences")
    ax.set_title(
        f"Cumulative dependency coverage — {language.title()} "
        f"(top {len(curve)} packages)"
    )
    ax.set_xlim(0, min(len(curve), 500))   # zoom to where the action happens
    ax.set_ylim(0, 1.0)
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
    args = ap.parse_args()

    mining_dir = args.mining_dir or find_latest_mining_dir(DEFAULT_MINING_ROOT)
    raw_path = mining_dir / f"{args.language}-raw.json"
    if not raw_path.exists():
        sys.exit(f"error: {raw_path} not found")

    print(f"[load] {raw_path}", file=sys.stderr)
    raw = load_raw(raw_path)

    total = raw["stats"]["total_direct_dep_occurrences"]
    package_counts = raw["package_counts"]

    curve = compute_coverage_curve(package_counts, total)
    thresholds = build_threshold_report(curve)

    output = {
        "language": args.language,
        "analyzed_at": datetime.now(timezone.utc).isoformat(),
        "source_file": str(raw_path.relative_to(REPO_ROOT)),
        "source_mined_at": raw["mined_at"],
        "corpus_size": raw["stats"]["repos_attempted"],
        "repos_with_manifest": raw["stats"]["repos_with_gomod"],
        "total_occurrences": total,
        "unique_packages": raw["stats"]["unique_packages"],
        "thresholds": thresholds,
        "coverage_curve": [
            {
                "rank": e["rank"],
                "name": e["name"],
                "repos_count": e["repos_count"],
                "occurrence_share": round(e["occurrence_share"], 6),
                "cumulative_coverage": round(e["cumulative_coverage"], 6),
                "most_common_version": e["most_common_version"],
            }
            for e in curve
        ],
    }

    out_json = mining_dir / f"{args.language}-analysis.json"
    with open(out_json, "w") as f:
        json.dump(output, f, indent=2)
    print(f"[analysis] wrote {out_json}", file=sys.stderr)

    chart_path = mining_dir / f"{args.language}-coverage-curve.png"
    render_coverage_chart(curve, thresholds, args.language, chart_path)

    # Console summary
    print("", file=sys.stderr)
    print(f"=== {args.language.upper()} coverage thresholds ===", file=sys.stderr)
    for t_key, t_data in thresholds.items():
        print(
            f"  {t_key}%  →  K = {t_data['k']:>4}  "
            f"(actual coverage: {t_data['actual_coverage']*100:.2f}%)",
            file=sys.stderr,
        )


if __name__ == "__main__":
    main()