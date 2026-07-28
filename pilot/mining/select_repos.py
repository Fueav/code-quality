#!/usr/bin/env python3
"""Mechanically rank sibling repositories for historical mining."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
import subprocess


EXCLUDED = {"agent_marketplace", "general-agent-ai", "code-quality"}


def git(repo: Path, *args: str) -> str:
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        text=True,
        capture_output=True,
    ).stdout.strip()


def common_git_dir(repo: Path) -> Path:
    value = Path(git(repo, "rev-parse", "--git-common-dir"))
    return (repo / value).resolve() if not value.is_absolute() else value.resolve()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("root", nargs="?", default="/Users/chris/AiProject", type=Path)
    parser.add_argument("--limit", type=int, default=5)
    args = parser.parse_args()

    excluded_common_dirs = set()
    for name in EXCLUDED:
        excluded_repo = args.root / name
        if excluded_repo.is_dir():
            try:
                excluded_common_dirs.add(common_git_dir(excluded_repo))
            except subprocess.CalledProcessError:
                pass

    rows = []
    for repo in sorted(args.root.iterdir()):
        if not repo.is_dir() or repo.name in EXCLUDED or repo.name.endswith("-worktrees"):
            continue
        try:
            if git(repo, "rev-parse", "--is-inside-work-tree") != "true":
                continue
            if common_git_dir(repo) in excluded_common_dirs:
                continue
            total = int(git(repo, "rev-list", "--count", "HEAD"))
            fixes = git(
                repo,
                "log",
                "--regexp-ignore-case",
                "--extended-regexp",
                "--grep=fix|hotfix|revert|bug",
                "--format=%H",
            ).splitlines()
        except (subprocess.CalledProcessError, ValueError):
            continue
        if total >= 30 and len(fixes) >= 5:
            rows.append({"repo": repo.name, "path": str(repo.resolve()), "total_commits": total, "fix_commits": len(fixes)})

    rows.sort(key=lambda row: (-row["fix_commits"], -row["total_commits"], row["path"]))
    print(json.dumps(rows[: args.limit], ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
