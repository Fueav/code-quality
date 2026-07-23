#!/usr/bin/env python3
"""Initialize a frozen, blind 3-project historical report-only pilot."""

from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import secrets
import shlex
import shutil
import subprocess
import sys
import tempfile

from historical_pilot import load_json, validate_manifest
from qualification_initialize import file_sha256, tree_sha256, write_json
from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT


def run(*args: str, cwd: pathlib.Path | None = None) -> str:
    completed = subprocess.run(
        list(args),
        cwd=cwd,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if completed.returncode != 0:
        raise ValueError(f"command failed ({' '.join(args[:2])}): {completed.stderr.strip()}")
    return completed.stdout.strip()


def task_markdown(task: dict[str, object], skill_path: pathlib.Path, binary: pathlib.Path) -> str:
    command = shlex.join(
        [
            str(binary),
            "prepare",
            "--host",
            "codex",
            "--repo",
            str(task["repository"]),
            "--base",
            str(task["base"]),
            "--target",
            str(task["target"]),
            "--diff-reason",
            str(task["diff_reason"]),
            "--output-root",
            str(task["output_root"]),
        ]
    )
    return f"""# Blind Historical Code Quality Report-Only Run

Run ID: `{task['run_id']}`
Required host: `codex`

Review this committed change without trying to infer its historical outcome. Do not inspect the pilot manifest, operator mapping, sibling tasks, workspace parent, source repositories, or any commit after the supplied target. Work only from this prompt, the frozen Skill, and the prepared session.

1. Read the frozen Skill at `{skill_path}`.
2. Run:

```bash
{command}
```

3. Follow only the CLI-returned `workflow_path` until `finalize` returns `COMPLETE` or `INCOMPLETE`.
4. After `prepare`, inspect only files inside the returned session directory.
5. Do not add a human judgment. Report the final JSON and Markdown paths to the operator.
"""


def materialize_blind_repository(
    source: pathlib.Path,
    base: str,
    target: str,
    output: pathlib.Path,
) -> dict[str, str]:
    source = source.resolve(strict=True)
    resolved_root = pathlib.Path(run("git", "-C", str(source), "rev-parse", "--show-toplevel")).resolve(strict=True)
    if resolved_root != source:
        raise ValueError(f"project repository must identify its Git root: {source}")
    resolved_base = run("git", "-C", str(source), "rev-parse", "--verify", f"{base}^{{commit}}")
    resolved_target = run("git", "-C", str(source), "rev-parse", "--verify", f"{target}^{{commit}}")
    first_parent = run("git", "-C", str(source), "rev-parse", f"{resolved_target}^1")
    if resolved_base != base or resolved_target != target or first_parent != resolved_base:
        raise ValueError("historical changes must use full SHAs for one first-parent commit")
    changed = run("git", "-C", str(source), "diff", "--name-only", resolved_base, resolved_target, "--")
    if not changed:
        raise ValueError("historical change has no changed files")

    output.mkdir(parents=True)
    run("git", "init", "--quiet", str(output))
    run("git", "-C", str(output), "config", "core.sparseCheckout", "true")
    sparse = output / ".git" / "info" / "sparse-checkout"
    sparse.write_text("/.code-quality-empty-checkout\n", encoding="utf-8")
    run(
        "git",
        "-C",
        str(output),
        "fetch",
        "--quiet",
        "--depth=2",
        "--no-tags",
        source.as_uri(),
        resolved_target,
    )
    run("git", "-C", str(output), "checkout", "--quiet", "--detach", resolved_target)
    if run("git", "-C", str(output), "status", "--porcelain=v1", "--untracked-files=all"):
        raise ValueError("materialized blind repository is dirty")
    if run("git", "-C", str(output), "rev-parse", "HEAD") != resolved_target:
        raise ValueError("materialized blind repository target mismatch")
    run("git", "-C", str(output), "cat-file", "-e", f"{resolved_base}^{{commit}}")
    return {
        "base": resolved_base,
        "target": resolved_target,
        "base_tree": run("git", "-C", str(output), "rev-parse", f"{resolved_base}^{{tree}}"),
        "target_tree": run("git", "-C", str(output), "rev-parse", f"{resolved_target}^{{tree}}"),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    parser.add_argument("--manifest", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--allow-dirty-development", action="store_true")
    args = parser.parse_args()

    source = args.source.resolve(strict=True)
    manifest_path = args.manifest.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        raise ValueError("--output must not already exist")
    output.parent.mkdir(parents=True, exist_ok=True)

    head = run("git", "rev-parse", "HEAD^{commit}", cwd=source)
    dirty = bool(run("git", "status", "--porcelain=v1", "--untracked-files=all", cwd=source))
    if dirty and not args.allow_dirty_development:
        raise ValueError("historical pilot initialization requires a clean source checkout")
    manifest = load_json(manifest_path)
    changes = validate_manifest(manifest)
    projects = {str(project["id"]): project for project in manifest["projects"]}

    temporary = pathlib.Path(tempfile.mkdtemp(prefix=f".{output.name}-", dir=output.parent))
    try:
        version = f"0.1.0-report-only-historical.{head[:12]}"
        if dirty:
            version += ".dirty-development"
        binary = temporary / "quality-review"
        run(
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            f"-s -w -X main.version={version}",
            "-o",
            str(binary),
            "./cmd/quality-review",
            cwd=source,
        )
        plugin_source = source / "plugins" / "code-quality"
        tree_sha256(plugin_source)
        plugin_target = temporary / "plugin" / "code-quality"
        shutil.copytree(plugin_source, plugin_target, symlinks=False)
        final_skill = output / "plugin" / "code-quality" / "skills" / "code-quality" / "SKILL.md"
        final_binary = output / "quality-review"

        operator_runs: list[dict[str, object]] = []
        for change_id, change in changes.items():
            project = projects[str(change["project_id"])]
            repository_id = "repo-" + secrets.token_hex(12)
            run_id = "run-" + secrets.token_hex(12)
            materialized = materialize_blind_repository(
                pathlib.Path(str(project["repository"])),
                str(change["base_commit"]),
                str(change["target_commit"]),
                temporary / "repositories" / repository_id,
            )
            final_repository = output / "repositories" / repository_id
            output_root = output / "sessions" / run_id
            task = {
                "schema_version": 1,
                "run_id": run_id,
                "host": "codex",
                "repository": str(final_repository),
                "base": materialized["base"],
                "target": materialized["target"],
                "diff_reason": f"blind historical committed change {run_id}",
                "output_root": str(output_root),
            }
            task_path = temporary / "tasks" / "codex" / f"{run_id}.json"
            write_json(task_path, task)
            task_path.with_suffix(".md").write_text(task_markdown(task, final_skill, final_binary), encoding="utf-8")
            operator_runs.append(
                {
                    "run_id": run_id,
                    "change_id": change_id,
                    "project_id": change["project_id"],
                    "ground_truth": change["ground_truth"],
                    "labeler": change["labeler"],
                    "label_note": change["label_note"],
                    "host": "codex",
                    "repository_id": repository_id,
                    "base_tree": materialized["base_tree"],
                    "target_tree": materialized["target_tree"],
                    "task": task_path.relative_to(temporary).as_posix(),
                }
            )

        write_json(
            temporary / "operator-manifest.json",
            {
                "schema_version": 1,
                "visibility": "operator_and_human_reviewer_only",
                "runs": operator_runs,
            },
        )
        os.chmod(temporary / "operator-manifest.json", 0o600)
        private_manifest = temporary / "private" / "manifest.json"
        private_manifest.parent.mkdir(parents=True)
        shutil.copyfile(manifest_path, private_manifest)
        os.chmod(private_manifest, 0o600)
        for directory in ("sessions", "observations", "run-evidence", "records", "human-reviews", "batch-logs"):
            (temporary / directory).mkdir()

        baseline = {
            "schema_version": 1,
            "profile": "report_only_historical_pilot",
            "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "source_commit": head,
            "source_dirty": dirty,
            "development_only": dirty,
            "rc_version": version,
            "go_version": run("go", "version"),
            "codex_version": run("codex", "--version"),
            "qualification_model": QUALIFICATION_MODEL,
            "qualification_reasoning_effort": QUALIFICATION_REASONING_EFFORT,
            "policy_version": json.loads((source / "policy" / "manifest.json").read_text(encoding="utf-8"))["policy_version"],
            "manifest_sha256": file_sha256(manifest_path),
            "plugin_sha256": tree_sha256(plugin_target),
            "binary_sha256": file_sha256(binary),
            "planned_runs": len(changes),
            "host_totals": {"codex": len(changes)},
        }
        write_json(temporary / "baseline.json", baseline)
        write_json(
            temporary / "progress.json",
            {
                "schema_version": 1,
                "planned_runs": len(changes),
                "executed_runs": 0,
                "reviewed_runs": 0,
                "historical_evidence_complete": False,
            },
        )
        temporary.rename(output)
    except BaseException:
        shutil.rmtree(temporary, ignore_errors=True)
        raise

    json.dump(
        {
            "output": str(output),
            "source_commit": head,
            "source_dirty": dirty,
            "planned_runs": len(changes),
            "status": "initialized",
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
