#!/usr/bin/env python3
"""Initialize a frozen, blind report-only smoke workspace."""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import pathlib
import secrets
import shlex
import shutil
import subprocess
import sys
import tempfile

from qualification_inventory import validate_fixture
from qualification_matrix import build_plan, load_cases
from qualification_run import QUALIFICATION_MODEL, QUALIFICATION_REASONING_EFFORT


def run(*args: str, cwd: pathlib.Path | None = None) -> str:
    completed = subprocess.run(
        list(args),
        cwd=cwd,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return completed.stdout.strip()


def write_json(path: pathlib.Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def file_sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def tree_sha256(root: pathlib.Path) -> str:
    digest = hashlib.sha256()
    entries = sorted(root.rglob("*"))
    symlinks = [path.relative_to(root).as_posix() for path in entries if path.is_symlink()]
    if symlinks:
        raise ValueError(f"tree contains symlinks: {', '.join(symlinks)}")
    files = [path for path in entries if path.is_file()]
    for path in files:
        relative = path.relative_to(root).as_posix().encode()
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        content = path.read_bytes()
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return digest.hexdigest()


def validate_all_fixtures(cases: list[dict[str, object]], fixtures: pathlib.Path) -> None:
    errors: list[str] = []
    for case in cases:
        case_id = str(case["id"])
        fixture = case.get("fixture")
        if not isinstance(fixture, dict):
            errors.append(f"{case_id}: fixture metadata is invalid")
            continue
        root = fixtures / case_id.lower()
        if not root.is_dir():
            errors.append(f"{case_id}: fixture directory is missing")
            continue
        for message in validate_fixture(root, fixture.get("language")):
            errors.append(f"{case_id}: {message}")
    if errors:
        raise ValueError("invalid qualification fixtures: " + "; ".join(errors))


def materialize_case(script: pathlib.Path, fixture: pathlib.Path, output: pathlib.Path) -> dict[str, str]:
    raw = run(sys.executable, str(script), "--fixture", str(fixture), "--output", str(output))
    value = json.loads(raw)
    if not all(isinstance(value.get(field), str) and value[field] for field in ("repository", "base", "target")):
        raise ValueError(f"materializer returned invalid metadata for {fixture.name}")
    return value


def task_markdown(task: dict[str, object], skill_path: pathlib.Path, binary: pathlib.Path) -> str:
    command = shlex.join(
        [
            str(binary),
            "prepare",
            "--host",
            str(task["host"]),
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
    return f"""# Blind Code Quality Report-Only Smoke Run

Run ID: `{task['run_id']}`
Required host: `{task['host']}`

This is a blind report-only smoke run. Do not search for or read the code-quality source repository, eval cases, operator manifest, sibling tasks, or expected results. Do not infer an expected verdict from the run ID. Work only from the prepared session inputs and the frozen Skill.

1. Read the frozen Skill at `{skill_path}`.
2. Run:

```bash
{command}
```

3. Follow only the CLI-returned `workflow_path` until `finalize` returns `COMPLETE` or `INCOMPLETE`.
4. Do not inspect files outside the returned session directory after `prepare`.
5. Do not add a human confirmation. Report the final JSON and Markdown paths to the operator.
"""


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=pathlib.Path, default=pathlib.Path("."))
    parser.add_argument("--cases", type=pathlib.Path, default=pathlib.Path("evals/cases.json"))
    parser.add_argument("--fixtures", type=pathlib.Path, default=pathlib.Path("pilot/fixtures"))
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--allow-dirty-development", action="store_true")
    args = parser.parse_args()

    source = args.source.resolve(strict=True)
    cases_path = (source / args.cases).resolve(strict=True) if not args.cases.is_absolute() else args.cases.resolve(strict=True)
    fixtures = (source / args.fixtures).resolve(strict=True) if not args.fixtures.is_absolute() else args.fixtures.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        raise ValueError("--output must not already exist")
    output.parent.mkdir(parents=True, exist_ok=True)

    head = run("git", "rev-parse", "HEAD^{commit}", cwd=source)
    dirty_lines = [line for line in run("git", "status", "--porcelain", cwd=source).splitlines() if line]
    if dirty_lines and not args.allow_dirty_development:
        raise ValueError("qualification initialization requires a clean source checkout")

    cases = load_cases(cases_path)
    validate_all_fixtures(cases, fixtures)
    schedule = build_plan(cases, {})
    planned_runs = len(cases)
    expected_host_totals = {"codex": planned_runs}
    if schedule["planned_runs"] != planned_runs or schedule["host_totals"] != expected_host_totals:
        raise ValueError("smoke schedule is not one Codex run per case")

    temporary = pathlib.Path(tempfile.mkdtemp(prefix=f".{output.name}-", dir=output.parent))
    try:
        version = f"0.1.1-report-only-smoke.{head[:12]}"
        if dirty_lines:
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
        final_skill_path = output / "plugin" / "code-quality" / "skills" / "code-quality" / "SKILL.md"
        final_binary = output / "quality-review"

        materializer = source / "pilot" / "materialize.py"
        repositories: dict[str, dict[str, str]] = {}
        for case in cases:
            case_id = str(case["id"])
            repository_id = "repo-" + secrets.token_hex(12)
            metadata = materialize_case(
                materializer,
                fixtures / case_id.lower(),
                temporary / "repositories" / repository_id,
            )
            metadata["repository"] = str(output / "repositories" / repository_id)
            repositories[case_id] = {"repository_id": repository_id, **metadata}

        private_runs: list[dict[str, object]] = []
        for slot in schedule["slots"]:
            case_id = str(slot["case_id"])
            repository = repositories[case_id]
            run_id = "run-" + secrets.token_hex(12)
            output_root = output / "sessions" / run_id
            task = {
                "schema_version": 1,
                "run_id": run_id,
                "host": slot["host"],
                "repository": repository["repository"],
                "base": repository["base"],
                "target": repository["target"],
                "diff_reason": f"blind report-only smoke committed increment {run_id}",
                "output_root": str(output_root),
            }
            task_path = temporary / "tasks" / str(slot["host"]) / f"{run_id}.json"
            write_json(task_path, task)
            task_path.with_suffix(".md").write_text(
                task_markdown(task, final_skill_path, final_binary),
                encoding="utf-8",
            )
            private_runs.append(
                {
                    "run_id": run_id,
                    "case_id": case_id,
                    "rule_id": slot["rule_id"],
                    "kind": slot["kind"],
                    "run_number": slot["run_number"],
                    "host": slot["host"],
                    "repository_id": repository["repository_id"],
                    "task": task_path.relative_to(temporary).as_posix(),
                }
            )

        operator_manifest = {
            "schema_version": 1,
            "visibility": "operator_and_human_reviewer_only",
            "runs": private_runs,
        }
        write_json(temporary / "operator-manifest.json", operator_manifest)
        os.chmod(temporary / "operator-manifest.json", 0o600)
        private_cases = temporary / "private" / "cases.json"
        private_cases.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(cases_path, private_cases)
        os.chmod(private_cases, 0o600)
        for directory in ("sessions", "replay-records", "run-evidence", "human-reviews", "batch-logs"):
            (temporary / directory).mkdir()
        write_json(
            temporary / "smoke-summary.json",
            json.loads(
                run(
                    str(binary),
                    "replay",
                    "summarize",
                    "--cases",
                    str(private_cases),
                    "--records",
                    str(temporary / "replay-records"),
                )
            ),
        )
        baseline = {
            "schema_version": 1,
            "profile": "report_only_smoke",
            "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "source_commit": head,
            "source_dirty": bool(dirty_lines),
            "development_only": bool(dirty_lines),
            "rc_version": version,
            "go_version": run("go", "version"),
            "codex_version": run("codex", "--version"),
            "qualification_model": QUALIFICATION_MODEL,
            "qualification_reasoning_effort": QUALIFICATION_REASONING_EFFORT,
            "policy_version": json.loads((source / "policy" / "manifest.json").read_text(encoding="utf-8"))["policy_version"],
            "cases_sha256": file_sha256(cases_path),
            "fixtures_sha256": tree_sha256(fixtures),
            "plugin_sha256": tree_sha256(plugin_target),
            "binary_sha256": file_sha256(binary),
            "planned_runs": schedule["planned_runs"],
            "host_totals": schedule["host_totals"],
        }
        write_json(temporary / "baseline.json", baseline)
        write_json(
            temporary / "progress.json",
            {
                "schema_version": 1,
                "planned_runs": planned_runs,
                "completed_runs": 0,
                "metrics_available": 0,
                "report_only_smoke_complete": False,
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
            "source_dirty": bool(dirty_lines),
            "planned_runs": planned_runs,
            "status": "initialized",
        },
        sys.stdout,
        sort_keys=True,
    )
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
