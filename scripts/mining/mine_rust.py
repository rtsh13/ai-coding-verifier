#!/usr/bin/env python3
"""
mine_rust.py — Mine direct dependencies from top-starred Rust repositories on GitHub.

Methodology:
  1. Query GitHub Search API for top N Rust repos by stars (excluding forks + archived).
  2. For each repo, fetch root Cargo.toml from default branch via raw.githubusercontent.com.
  3. Parse Cargo.toml, extract [dependencies] (excluding [dev-dependencies] and [build-dependencies]).
  4. Aggregate: one vote per crate per repo (prevalence = fraction of repos that use a crate).
  5. Write raw + aggregated JSON to <output-dir>/<date>/rust-raw.json.

Differences from mine_go.py:
  - Fetches Cargo.toml instead of go.mod
  - Parses TOML [dependencies] instead of require blocks
  - Handles workspace Cargo.toml (may have [workspace.dependencies] but no [dependencies])
  - Crate names are simple identifiers (e.g. "serde") not module paths

Usage:
  export GITHUB_TOKEN=ghp_...
  python mine_rust.py                       # default: 1000 repos
  python mine_rust.py --corpus-size 50      # smoke test
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

try:
    import tomllib                          # Python 3.11+
except ModuleNotFoundError:
    try:
        import tomli as tomllib             # pip install tomli (backport, 3.9+)
    except ModuleNotFoundError:
        sys.exit("Missing dependency: pip install tomli")

import requests

GITHUB_SEARCH_URL = "https://api.github.com/search/repositories"
RAW_BASE = "https://raw.githubusercontent.com"
PER_PAGE = 100
MAX_PAGES = 10                # GitHub Search hard cap: 1000 results total
SEARCH_SLEEP = 2.5            # search API: 30 req/min authenticated
RAW_SLEEP = 0.1               # raw.githubusercontent is more permissive
SEARCH_QUERY = "language:Rust fork:false archived:false"


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


def fetch_cargo_toml(session: requests.Session, full_name: str, default_branch: str) -> str | None:
    """Fetch root Cargo.toml from a repo's default branch."""
    url = f"{RAW_BASE}/{full_name}/{default_branch}/Cargo.toml"
    try:
        r = session.get(url, timeout=20)
    except requests.RequestException as e:
        print(f"[cargo] {full_name}: {e}", file=sys.stderr)
        return None
    if r.status_code == 200:
        return r.text
    if r.status_code == 404:
        return None
    print(f"[cargo] {full_name}: unexpected {r.status_code}", file=sys.stderr)
    return None


# ──────────────────────────────────────────────────────────────
# Cargo.toml parsing
# ──────────────────────────────────────────────────────────────

def _extract_deps_from_table(raw_deps: dict) -> list[tuple[str, str]]:
    """Extract (crate_name, version_spec) pairs from a TOML dependencies table.

    Handles:
      serde = "1.0"                                    # simple string
      tokio = { version = "1", features = ["full"] }   # inline table
      my-crate = { git = "https://..." }               # git dep (no version)
      local-crate = { path = "../local" }              # path dep
    """
    deps = []
    for crate_name, spec in raw_deps.items():
        if isinstance(spec, str):
            deps.append((crate_name, spec))
        elif isinstance(spec, dict):
            version = spec.get("version", "")
            if version:
                deps.append((crate_name, version))
            elif spec.get("git") or spec.get("path"):
                source = "git" if spec.get("git") else "path"
                deps.append((crate_name, f"<{source}>"))
    return deps


def parse_direct_deps(cargo_text: str) -> tuple[list[tuple[str, str]], bool]:
    """Return (deps, needs_subcrate_fetch).

    Three cases:
      1. Non-workspace repo (e.g. ripgrep): [dependencies] at root → use directly
      2. Workspace with [workspace.dependencies] (e.g. serde): use those
      3. Workspace WITHOUT [workspace.dependencies] (e.g. tokio): return empty + True

    Returns:
      deps: list of (crate_name, version_spec)
      needs_subcrate_fetch: True if caller should try fetching subcrate Cargo.toml
    """
    try:
        parsed = tomllib.loads(cargo_text)
    except tomllib.TOMLDecodeError as e:
        print(f"[parse] TOML parse error: {e}", file=sys.stderr)
        return [], False

    is_workspace = "workspace" in parsed

    # Case 1: Non-workspace with [dependencies]
    raw_deps = parsed.get("dependencies", {})
    if raw_deps:
        return _extract_deps_from_table(raw_deps), False

    if not is_workspace:
        # No [dependencies] and not a workspace — likely a lib with no deps
        return [], False

    # Case 2: Workspace with [workspace.dependencies]
    ws_deps = parsed.get("workspace", {}).get("dependencies", {})
    if ws_deps:
        return _extract_deps_from_table(ws_deps), False

    # Case 3: Workspace without [workspace.dependencies]
    # Caller should try fetching the primary subcrate
    return [], True


def get_workspace_members(cargo_text: str) -> list[str]:
    """Extract workspace member names (non-glob only)."""
    try:
        parsed = tomllib.loads(cargo_text)
    except tomllib.TOMLDecodeError:
        return []
    members = parsed.get("workspace", {}).get("members", [])
    # Filter out glob patterns like "crates/*"
    return [m for m in members if "*" not in m and "?" not in m]


def guess_primary_subcrate(repo_name: str, members: list[str]) -> str | None:
    """Heuristic: the primary subcrate usually matches the repo name.

    Examples:
      tokio-rs/tokio → member "tokio"
      actix/actix-web → member "actix-web"
      hyperium/hyper → member "hyper"
    """
    # Exact match first
    if repo_name in members:
        return repo_name

    # Try without common org-name prefixes (e.g., "tokio-rs/tokio")
    for member in members:
        if member == repo_name or repo_name.endswith(member):
            return member

    # First member as last resort (often the primary crate)
    return members[0] if members else None


# ──────────────────────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser(description="Mine Rust crate dependencies from top GitHub repos")
    ap.add_argument("--corpus-size", type=int, default=1000,
                    help="Number of top-starred repos to mine (max 1000)")
    ap.add_argument("--output-dir", type=str, default="output",
                    help="Base output directory")
    ap.add_argument("--token", type=str, default=None,
                    help="GitHub token (or set GITHUB_TOKEN env var)")
    args = ap.parse_args()

    token = args.token or os.environ.get("GITHUB_TOKEN")
    if not token:
        sys.exit("Set GITHUB_TOKEN env var or pass --token")

    session = github_session(token)

    # ── Step 1: Search ──
    print(f"[main] Searching top {args.corpus_size} Rust repos by stars...", file=sys.stderr)
    repos = search_top_repos(session, args.corpus_size)
    print(f"[main] Found {len(repos)} repos", file=sys.stderr)

    # Filter: GitHub's language:Rust query matches repos with *any* Rust files.
    # Keep only repos where the primary language is Rust.
    pre_filter = len(repos)
    repos = [r for r in repos if (r.get("language") or "").lower() == "rust"]
    if pre_filter != len(repos):
        print(f"[main] Filtered to {len(repos)} repos (dropped {pre_filter - len(repos)} non-Rust-primary)", file=sys.stderr)

    # ── Step 2: Fetch & parse Cargo.toml for each ──
    raw_records = []
    all_deps: Counter = Counter()            # crate -> total mentions
    repo_has_crate: defaultdict = defaultdict(set)  # crate -> set of repos
    repos_with_cargo = 0
    repos_without_cargo = 0

    for i, repo in enumerate(repos):
        full_name = repo["full_name"]
        stars = repo["stargazers_count"]
        branch = repo.get("default_branch", "main")

        print(f"[fetch] ({i+1}/{len(repos)}) {full_name} ★{stars}...", file=sys.stderr)
        cargo_text = fetch_cargo_toml(session, full_name, branch)
        time.sleep(RAW_SLEEP)

        if cargo_text is None:
            repos_without_cargo += 1
            raw_records.append({
                "repo": full_name,
                "stars": stars,
                "cargo_toml_found": False,
                "source": "none",
                "direct_deps": [],
            })
            continue

        repos_with_cargo += 1
        direct, needs_subcrate = parse_direct_deps(cargo_text)
        source = "root"

        # Case 3: workspace without workspace.dependencies — try primary subcrate
        if needs_subcrate:
            members = get_workspace_members(cargo_text)
            repo_short = full_name.split("/")[-1]  # e.g. "tokio" from "tokio-rs/tokio"
            primary = guess_primary_subcrate(repo_short, members)
            if primary:
                print(f"  [workspace] trying subcrate {primary}/Cargo.toml...", file=sys.stderr)
                sub_text = fetch_cargo_toml(session, full_name, f"{branch}/{primary}")
                # raw.githubusercontent.com path: /{owner}/{repo}/{branch}/{subcrate}/Cargo.toml
                # but fetch_cargo_toml already appends /Cargo.toml, so we need a different approach
                sub_url = f"{RAW_BASE}/{full_name}/{branch}/{primary}/Cargo.toml"
                try:
                    r = session.get(sub_url, timeout=20)
                    if r.status_code == 200:
                        sub_direct, _ = parse_direct_deps(r.text)
                        if sub_direct:
                            direct = sub_direct
                            source = f"subcrate:{primary}"
                            print(f"  [workspace] found {len(direct)} deps in {primary}/", file=sys.stderr)
                except requests.RequestException:
                    pass
                time.sleep(RAW_SLEEP)

        # One vote per crate per repo (dedup within a single repo)
        seen_in_repo = set()
        for crate_name, version in direct:
            all_deps[crate_name] += 1
            if crate_name not in seen_in_repo:
                repo_has_crate[crate_name].add(full_name)
                seen_in_repo.add(crate_name)

        raw_records.append({
            "repo": full_name,
            "stars": stars,
            "cargo_toml_found": True,
            "source": source,
            "direct_deps": [{"crate": c, "version": v} for c, v in direct],
        })

    # ── Step 3: Aggregate ──
    prevalence = {
        crate: len(repo_set) / repos_with_cargo
        for crate, repo_set in repo_has_crate.items()
    }

    ranked = sorted(prevalence.items(), key=lambda x: -x[1])

    # ── Step 4: Write output ──
    datestamp = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    out_dir = Path(args.output_dir) / datestamp
    out_dir.mkdir(parents=True, exist_ok=True)

    output = {
        "metadata": {
            "language": "rust",
            "corpus_size": len(repos),
            "repos_with_cargo_toml": repos_with_cargo,
            "repos_without_cargo_toml": repos_without_cargo,
            "unique_crates": len(all_deps),
            "mined_at": datetime.now(timezone.utc).isoformat(),
            "search_query": SEARCH_QUERY,
        },
        "prevalence": [
            {"crate": crate, "prevalence": round(prev, 4), "repo_count": len(repo_has_crate[crate])}
            for crate, prev in ranked
        ],
        "raw": raw_records,
    }

    out_path = out_dir / "rust-raw.json"
    with open(out_path, "w") as f:
        json.dump(output, f, indent=2)

    print(f"\n[main] Done. Output: {out_path}", file=sys.stderr)
    print(f"[main] Repos with Cargo.toml: {repos_with_cargo}/{len(repos)}", file=sys.stderr)
    print(f"[main] Unique crates found: {len(all_deps)}", file=sys.stderr)
    print(f"\n[main] Top 30 crates by prevalence:", file=sys.stderr)
    for crate, prev in ranked[:30]:
        count = len(repo_has_crate[crate])
        print(f"  {prev*100:5.1f}%  ({count:3d} repos)  {crate}", file=sys.stderr)


if __name__ == "__main__":
    main()