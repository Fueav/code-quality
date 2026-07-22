#!/usr/bin/env python3
"""Materialize a committed pilot fixture without invoking a model."""

from __future__ import annotations

import argparse
import json
import pathlib
import shutil
import subprocess
import sys


def run(repo: pathlib.Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env={
            "PATH": __import__("os").environ.get("PATH", ""),
            "GIT_AUTHOR_NAME": "Code Quality Pilot",
            "GIT_AUTHOR_EMAIL": "pilot@example.invalid",
            "GIT_COMMITTER_NAME": "Code Quality Pilot",
            "GIT_COMMITTER_EMAIL": "pilot@example.invalid",
        },
    )
    return completed.stdout.strip()


def copy_tree(source: pathlib.Path, target: pathlib.Path) -> None:
    for path in sorted(source.rglob("*")):
        relative = path.relative_to(source)
        destination = target / relative
        if path.is_symlink():
            raise ValueError(f"fixture contains symlink: {relative}")
        if path.is_dir():
            destination.mkdir(parents=True, exist_ok=True)
        elif path.is_file():
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, destination)
        else:
            raise ValueError(f"fixture contains unsupported path: {relative}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()

    fixture = args.fixture.resolve()
    output = args.output.resolve()
    if not (fixture / "base").is_dir() or not (fixture / "target").is_dir():
        raise ValueError("fixture must contain base/ and target/")
    if output.exists():
        raise ValueError("output must not already exist")
    output.mkdir(parents=True)
    run(output, "init", "-b", "main")
    copy_tree(fixture / "base", output)
    run(output, "add", ".")
    run(output, "commit", "-m", "pilot base")
    base = run(output, "rev-parse", "HEAD")
    tracked = [value for value in run(output, "ls-files", "-z").split("\0") if value]
    for relative in tracked:
        path = output / pathlib.PurePosixPath(relative)
        if path.is_symlink() or path.is_file():
            path.unlink()
        else:
            raise ValueError(f"tracked fixture path is not a regular file: {relative}")
    copy_tree(fixture / "target", output)
    run(output, "add", ".")
    run(output, "commit", "-m", "pilot target")
    target = run(output, "rev-parse", "HEAD")
    json.dump({"repository": str(output), "base": base, "target": target}, sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
