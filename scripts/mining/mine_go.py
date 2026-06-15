#!/usr/bin/env python3
"""
mine_go.py — Mine direct dependencies from top-starred Go repositories on GitHub.

Methodology:
  1. Query GitHub Search API for top N Go repos by stars (excluding forks + archived).
  2. For each repo, fetch root go.mod from default branch via raw.githubusercontent.com.
  3. Parse go.mod, extract direct requires (excluding // indirect).
  4. Aggregate: one vote per package per repo.
  5. Write raw + aggregated JSON to <output-dir>/<date>/go-raw.json.

Usage:
  export GITHUB_TOKEN=ghp_...
  python mine_go.py                       # default: 1000 repos
  python mine_go.py --corpus-size 50      # smoke test
"""

from __future__ import annotations
import argparse
import json
import os
import re
import sys
import time
from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path

import requests
import warnings
warnings.filterwarnings("ignore", message=".*LibreSSL.*")

GITHUB_SEARCH_URL = "https://api.github.com/search/repositories"
RAW_BASE = "https://raw.githubusercontent.com"
PER_PAGE = 100
MAX_PAGES = 10                # GitHub Search hard cap: 1000 results total
SEARCH_SLEEP = 2.5            # search API: 30 req/min authenticated
RAW_SLEEP = 0.1               # raw.githubusercontent is more permissive
SEARCH_QUERY = "language:Go fork:false archived:false"


# ──────────────────────────────────────────────────────────────
# HTTP
# ──────────────────────────────────────────────────────────────

def github_session(token: str) -> requests.Session:
    s = requests.Session()
    s.headers.update({
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {token}",
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "verifier-mining-script",
    })
    return s


def search_top_repos(session: requests.Session, corpus_size: int) -> list[dict]:
    pages_needed = min(MAX_PAGES, (corpus_size + PER_PAGE - 1) // PER_PAGE)
    repos = []
    for page in range(1, pages_needed + 1):
        params = {
            "q": SEARCH_QUERY,
            "sort": "stars",
            "order": "desc",
            "per_page": PER_PAGE,
            "page": page,
        }
        print(f"[search] page {page}/{pages_needed}...", file=sys.stderr)
        r = session.get(GITHUB_SEARCH_URL, params=params, timeout=30)
        r.raise_for_status()
        repos.extend(r.json().get("items", []))
        if len(repos) >= corpus_size:
            break
        time.sleep(SEARCH_SLEEP)
    return repos[:corpus_size]


def fetch_gomod(session: requests.Session, full_name: str, default_branch: str) -> str | None:
    url = f"{RAW_BASE}/{full_name}/{default_branch}/go.mod"
    try:
        r = session.get(url, timeout=20)
    except requests.RequestException as e:
        print(f"[gomod] {full_name}: {e}", file=sys.stderr)
        return None
    if r.status_code == 200:
        return r.text
    if r.status_code == 404:
        return None
    print(f"[gomod] {full_name}: unexpected {r.status_code}", file=sys.stderr)
    return None


# ──────────────────────────────────────────────────────────────
# go.mod parsing — minimal state machine
# ──────────────────────────────────────────────────────────────

_REQUIRE_BLOCK_OPEN = re.compile(r"^\s*require\s*\(\s*$")
_REQUIRE_SINGLE = re.compile(r"^\s*require\s+(\S+)\s+(\S+)")
_BLOCK_LINE = re.compile(r"^\s*(\S+)\s+(\S+)")


def parse_direct_deps(gomod_text: str) -> list[tuple[str, str]]:
    """Return list of (module_path, version) for direct deps. Excludes // indirect."""
    deps = []
    in_block = False
    for raw_line in gomod_text.splitlines():
        parts = raw_line.split("//", 1)
        code = parts[0].rstrip()
        comment = parts[1] if len(parts) > 1 else ""
        is_indirect = "indirect" in comment

        if not in_block:
            if _REQUIRE_BLOCK_OPEN.match(raw_line):
                in_block = True
                continue
            m = _REQUIRE_SINGLE.match(code)
            if m and not is_indirect:
                deps.append((m.group(1), m.group(2)))
            continue

        # inside require ( ... )
        if code.strip() == ")":
            in_block = False
            continue
        if not code.strip():
            continue
        m = _BLOCK_LINE.match(code)
        if m and not is_indirect:
            deps.append((m.group(1), m.group(2)))

    return deps


# ──────────────────────────────────────────────────────────────
# main
# ──────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--corpus-size", type=int, default=1000)
    ap.add_argument("--output-dir", type=Path,
                    default=Path(__file__).resolve().parents[2] / "testdata" / "mining")
    args = ap.parse_args()

    token = os.environ.get("GITHUB_TOKEN")
    if not token:
        sys.exit("error: set GITHUB_TOKEN environment variable")

    today = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    out_dir = args.output_dir / today
    out_dir.mkdir(parents=True, exist_ok=True)
    out_path = out_dir / "go-raw.json"

    session = github_session(token)

    print(f"[mine] corpus size: {args.corpus_size}", file=sys.stderr)
    repos = search_top_repos(session, args.corpus_size)
    print(f"[mine] retrieved {len(repos)} repos", file=sys.stderr)

    repo_records = []
    package_info: dict[str, dict] = defaultdict(
        lambda: {"repos": [], "versions": Counter()}
    )

    for i, repo in enumerate(repos, 1):
        full_name = repo["full_name"]
        default_branch = repo.get("default_branch", "main")
        stars = repo.get("stargazers_count", 0)

        gomod = fetch_gomod(session, full_name, default_branch)

        if gomod is None:
            repo_records.append({
                "full_name": full_name, "stars": stars,
                "default_branch": default_branch,
                "has_gomod": False, "direct_deps": [],
            })
        else:
            raw_deps = parse_direct_deps(gomod)
            # one vote per package per repo
            unique_pkgs: dict[str, str] = {}
            for name, ver in raw_deps:
                unique_pkgs.setdefault(name, ver)

            repo_records.append({
                "full_name": full_name, "stars": stars,
                "default_branch": default_branch, "has_gomod": True,
                "direct_deps": [{"name": n, "version": v}
                                for n, v in sorted(unique_pkgs.items())],
            })

            for name, ver in unique_pkgs.items():
                package_info[name]["repos"].append(full_name)
                package_info[name]["versions"][ver] += 1

        if i % 50 == 0:
            print(f"[mine] processed {i}/{len(repos)}", file=sys.stderr)
        time.sleep(RAW_SLEEP)

    # aggregate
    package_counts = sorted(
        (
            {
                "name": pkg,
                "repos_count": len(info["repos"]),
                "example_repos": info["repos"][:5],
                "most_common_version": info["versions"].most_common(1)[0][0],
                "version_distribution": dict(info["versions"].most_common(5)),
            }
            for pkg, info in package_info.items()
        ),
        key=lambda x: x["repos_count"],
        reverse=True,
    )

    repos_with_gomod = sum(1 for r in repo_records if r["has_gomod"])
    total_occurrences = sum(len(r["direct_deps"]) for r in repo_records)

    output = {
        "language": "go",
        "mined_at": datetime.now(timezone.utc).isoformat(),
        "corpus_source": "github.com top-starred Go repos via Search API",
        "query": SEARCH_QUERY,
        "stats": {
            "repos_attempted": len(repo_records),
            "repos_with_gomod": repos_with_gomod,
            "repos_without_gomod": len(repo_records) - repos_with_gomod,
            "total_direct_dep_occurrences": total_occurrences,
            "unique_packages": len(package_counts),
        },
        "package_counts": package_counts,
        "repos": repo_records,
    }

    with open(out_path, "w") as f:
        json.dump(output, f, indent=2)

    print(f"[mine] wrote {out_path}", file=sys.stderr)
    print(f"[mine] {repos_with_gomod}/{len(repo_records)} repos had go.mod",
          file=sys.stderr)
    print(f"[mine] {len(package_counts)} unique packages, "
          f"{total_occurrences} direct occurrences", file=sys.stderr)


if __name__ == "__main__":
    main()