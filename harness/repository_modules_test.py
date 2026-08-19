#!/usr/bin/env python3
"""Contract tests for repository readiness and template delivery."""

import hashlib, json, os, subprocess, sys, tempfile
from pathlib import Path

source = Path(__file__).resolve().parent.parent
verify, deliver = source / "harness/repository_verification.py", source / "harness/template_delivery.py"


def call(script, root, *args, cwd=None, env=None):
    runtime = os.environ.copy(); runtime.update(env or {}); runtime["HARNESS_PROJECT_ROOT"] = str(root)
    return subprocess.run([sys.executable, "-I", "-B", "-S", str(script), *args], cwd=cwd or root, env=runtime, text=True, capture_output=True, check=False)


def git(root, *args):
    result = subprocess.run(["git", "-C", str(root), *args], text=True, capture_output=True, check=False)
    if result.returncode: raise AssertionError(result.stderr)
    return result.stdout.strip()


def initialize(root, remote=None):
    git(root, "init", "-q"); git(root, "config", "user.email", "test@example.com"); git(root, "config", "user.name", "Test")
    (root / "fixture").write_text("root\n"); git(root, "add", "-A"); git(root, "commit", "-qm", "root")
    if remote: git(root, "remote", "add", "origin", remote)
    return git(root, "rev-parse", "HEAD")


def write(path, payload):
    path.parent.mkdir(parents=True, exist_ok=True); path.write_text(json.dumps(payload, sort_keys=True) + "\n")


with tempfile.TemporaryDirectory() as directory:
    repo = Path(directory); first = initialize(repo)
    assert call(verify, repo, "compare", "schedule", "", "").stdout.strip() == first
    (repo / "fixture").write_text("head\n"); git(repo, "commit", "-qam", "head"); head = git(repo, "rev-parse", "HEAD"); git(repo, "commit", "--allow-empty", "-qm", "tip")
    for args, expected in [(("compare", "pull_request", first, ""), first), (("compare", "schedule", "", ""), head), (("compare", "push", "", "0" * 40), first)]:
        result = call(verify, repo, *args); assert result.returncode == 0 and result.stdout.strip() == expected, result.stderr
    assert call(verify, repo, "compare", "unknown", "", "0" * 40).returncode != 0

with tempfile.TemporaryDirectory() as directory:
    repo = Path(directory)
    manifest = {"schema_version": 1, "managed_paths": [{"path": "AGENTS.md", "strategy": "manual_merge"}], "retired_paths": []}
    contract = {
        "schema_version": 1, "contract_version": 1,
        "scaffold_source": {"repository": "github.com/moss-site/ai-first-go-template", "delivery_intents": ["bootstrap", "upgrade"]},
        "repository_contract": {
            "required_paths": ["AGENTS.md", "harness/scaffold_manifest.json", "harness/scaffold.lock"], "executable_paths": [],
            "schema_versions": {
                "harness/scaffold_manifest.json": {"field": "schema_version", "allowed": [1]},
                "harness/scaffold.lock": {"field": "schema_version", "allowed": [1]},
            },
        },
    }
    write(repo / "harness/suite_contract.json", contract); write(repo / "harness/scaffold_manifest.json", manifest); (repo / "AGENTS.md").write_text("target contract\n")
    digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
    lock = {
        "schema_version": 1, "template_commit": "a" * 40, "manifest_sha256": digest(repo / "harness/scaffold_manifest.json"),
        "resolved_paths": [{"path": "AGENTS.md", "strategy": "manual_merge", "resolution": "merged", "template_sha256": digest(repo / "AGENTS.md"), "target_sha256": digest(repo / "AGENTS.md")}],
    }
    write(repo / "harness/scaffold.lock", lock)
    ready = call(verify, repo, "ready"); assert ready.returncode == 0 and json.loads(ready.stdout)["status"] == "ready", ready.stderr
    (repo / "AGENTS.md").write_text("drifted\n"); drifted = call(verify, repo, "ready"); assert drifted.returncode != 0 and "semantic resolution" in drifted.stderr
    (repo / "harness/scaffold.lock").unlink(); missing = call(verify, repo, "ready"); assert missing.returncode != 0 and "template delivery" in missing.stderr

with tempfile.TemporaryDirectory() as directory:
    base = Path(directory); scaffold, target = base / "scaffold", base / "target"; scaffold.mkdir(); target.mkdir()
    contract = {
        "schema_version": 1, "contract_version": 1,
        "scaffold_source": {"repository": "github.com/moss-site/ai-first-go-template", "delivery_intents": ["bootstrap", "upgrade"]},
        "repository_contract": {"required_paths": [], "executable_paths": [], "schema_versions": {}},
    }
    write(scaffold / "harness/suite_contract.json", contract)
    engine = scaffold / "scripts/harnessctl.sh"; engine.parent.mkdir(parents=True); engine.write_text("#!/bin/sh\nprintf '{\"status\":\"drift\",\"converged\":false}\\n'\nexit 1\n"); engine.chmod(0o755)
    initialize(scaffold, "git@github.com:moss-site/ai-first-go-template.git"); initialize(target)
    result = call(deliver, scaffold, "preflight", "--intent", "bootstrap", "--target", str(target))
    assert result.returncode == 0 and json.loads(result.stdout)["status"] == "ready", result.stderr
    relative = call(deliver, scaffold, "preflight", "--intent", "bootstrap", "--target", "../target"); assert relative.returncode != 0 and "absolute" in relative.stderr
    child = scaffold / "child"; child.mkdir(); wrong_cwd = call(deliver, scaffold, "preflight", "--intent", "bootstrap", "--target", str(target), cwd=child)
    assert wrong_cwd.returncode != 0 and "exact scaffold root" in wrong_cwd.stderr
    write(target / "harness/suite_contract.json", contract); outside = call(deliver, target, "preflight", "--intent", "bootstrap", "--target", str(scaffold))
    assert outside.returncode != 0 and "canonical Scaffold Source" in outside.stderr

print("repository_modules: passed")
