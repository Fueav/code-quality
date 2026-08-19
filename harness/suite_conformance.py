#!/usr/bin/env python3
"""Prove exact Template, Template Sync, and HDD revisions compose."""

import argparse, hashlib, json, os, shutil, subprocess, sys, tempfile
from pathlib import Path

root = Path(__file__).resolve().parent.parent
urls = {"harness-template-sync": "https://github.com/Fueav/harness-template-sync.git", "harness-driven-development": "https://github.com/Fueav/harness-driven-development.git"}
def run(command, cwd, env=None): return subprocess.run(command, cwd=cwd, env={key: value for key, value in (env or os.environ).items() if env is not None or key != "HARNESS_PROJECT_ROOT"}, text=True, capture_output=True, check=False)
def git(repo, *arguments):
    result = run(["git", "-C", str(repo), *arguments], repo)
    if result.returncode: raise RuntimeError(result.stderr.strip() or "Git command failed")
    return result.stdout.strip()

def normalized(value):
    value = value.strip().removesuffix(".git").removesuffix("/")
    for prefix in ("git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/"):
        if value.startswith(prefix): return "github.com/" + value[len(prefix):]
    return value

def is_source():
    try:
        expected = json.loads((root / "harness/suite_contract.json").read_text())["scaffold_source"]["repository"]
        return Path(git(root, "rev-parse", "--show-toplevel")).resolve() == root.resolve() and normalized(git(root, "remote", "get-url", "origin")) == expected
    except (OSError, KeyError, ValueError, json.JSONDecodeError, RuntimeError): return False


def component(path, name):
    path = path.resolve()
    if Path(git(path, "rev-parse", "--show-toplevel")).resolve() != path: raise RuntimeError(f"{name} must be a Git repository root")
    if git(path, "status", "--porcelain=v1", "--untracked-files=all"): raise RuntimeError(f"{name} must be clean")
    digest = hashlib.sha256(); plugin = path / "plugins" / name
    for item in sorted(candidate for candidate in plugin.rglob("*") if candidate.is_file()): digest.update(item.relative_to(plugin).as_posix().encode() + b"\0" + item.read_bytes() + b"\0")
    return {"commit": git(path, "rev-parse", "HEAD"), "package_sha256": digest.hexdigest(), "version": (path / "VERSION").read_text().strip()}


def step(label, command, cwd, steps, env=None):
    result = run(command, cwd, env); steps.append({"name": label, "status": "passed" if result.returncode == 0 else "failed"})
    if result.returncode: raise RuntimeError(f"{label} failed: {(result.stderr or result.stdout).strip()}")


def resolve(raw, name, temporary):
    if raw: return Path(raw).resolve()
    destination = temporary / name; result = run(["git", "clone", "--depth", "1", urls[name], str(destination)], root)
    if result.returncode: raise RuntimeError(result.stderr.strip() or f"cannot clone {name}")
    return destination


def verify(sync_raw, hdd_raw, output):
    evidence = {"schema_version": 1, "status": "failed", "components": {}, "steps": []}; steps = evidence["steps"]; os.environ.setdefault("HARNESS_PROJECT_ROOT", str(root))
    try:
        if not is_source() or git(root, "status", "--porcelain=v1", "--untracked-files=all"): raise RuntimeError("suite conformance requires a clean canonical Scaffold Source root")
        contract = json.loads((root / "harness/suite_contract.json").read_text()); version = contract["contract_version"]
        with tempfile.TemporaryDirectory() as directory:
            temporary = Path(directory); sync = resolve(sync_raw, "harness-template-sync", temporary); hdd = resolve(hdd_raw, "harness-driven-development", temporary)
            compatibility = [json.loads((repo / "contracts/suite-compatibility.json").read_text()) for repo in (sync, hdd)]
            if any(version not in item.get("suite_contract_versions", []) for item in compatibility): raise RuntimeError("a component does not support the Template suite contract")
            actual = {"scaffold_manifest": json.loads((root / "harness/scaffold_manifest.json").read_text())["schema_version"], "harness_profiles": json.loads((root / "harness/harness_profiles.json").read_text())["schema_version"], "workflow_registry": json.loads((root / "docs/harness-workflows.json").read_text())["version"]}
            if any(value not in compatibility[0].get("template_contracts", {}).get(name, []) for name, value in actual.items()): raise RuntimeError("Template Sync does not support the current Template schemas")
            evidence["contract_version"] = version; evidence["contract_sha256"] = hashlib.sha256((root / "harness/suite_contract.json").read_bytes()).hexdigest()
            evidence["components"] = {
                "ai-first-go-template": {"commit": git(root, "rev-parse", "HEAD")},
                "harness-template-sync": component(sync, "harness-template-sync"),
                "harness-driven-development": component(hdd, "harness-driven-development"),
            }
            for label, repo in (("template_sync", sync), ("hdd", hdd)):
                step(label + "_release", [sys.executable, "scripts/verify_release.py"], repo, steps)
                step(label + "_evals", [sys.executable, "scripts/verify_evals.py", "--results", "evals/results.json"], repo, steps)
            target = temporary / "target"; shutil.copytree(root, target, symlinks=True, ignore=shutil.ignore_patterns(".git", ".artifacts", ".tools", "__pycache__"))
            step("target_init", [str(target / "scripts/init_project.sh"), "--module", "example.com/suite/service", "--service", "suite-service", "--owner", "@suite-team"], target, steps)
            if git(root, "status", "--porcelain=v1", "--untracked-files=all"): raise RuntimeError("target initialization escaped into the Scaffold Source")
            steps.append({"name": "delivery_environment_isolation", "status": "passed"})
            target_git = [("init", ["git", "init", "-q"]), ("email", ["git", "config", "user.email", "suite@example.com"]), ("name", ["git", "config", "user.name", "Suite Conformance"]), ("add", ["git", "add", "-A"]), ("commit", ["git", "commit", "-qm", "target baseline"])]
            for label, command in target_git: step("target_git_" + label, command, target, steps)
            delivery = root / "harness/template_delivery.py"
            step("delivery_preflight", [str(delivery), "preflight", "--intent", "bootstrap", "--target", str(target)], root, steps)
            step("delivery_record", [str(delivery), "record", "--intent", "bootstrap", "--target", str(target), "--resolution", "CODEOWNERS=adapted"], root, steps)
            for label, command in (("delivery_add", ["git", "add", "-A"]), ("delivery_commit", ["git", "commit", "-qm", "record delivery"])): step(label, command, target, steps)
            step("delivery_complete", [str(delivery), "complete", "--intent", "bootstrap", "--target", str(target)], root, steps)
            step("target_build", ["go", "build", "./..."], target, steps); step("target_test", ["go", "test", "./..."], target, steps)
            step("hdd_target_contract", [sys.executable, "scripts/verify_evals.py", "--results", "evals/results.json", "--repository", str(target)], hdd, steps)
            runtime = os.environ.copy(); runtime["HARNESS_PROJECT_ROOT"] = str(target)
            wrong = run([sys.executable, "-I", "-B", "-S", str(target / "harness/template_delivery.py"), "preflight", "--intent", "upgrade", "--target", str(target)], target, runtime)
            if wrong.returncode == 0 or "canonical Scaffold Source" not in wrong.stderr: raise RuntimeError("Template Sync source-root guard did not fail closed")
            steps.append({"name": "wrong_source_stop", "status": "passed"}); (target / "harness/scaffold.lock").unlink()
            incomplete = run([sys.executable, "-I", "-B", "-S", str(target / "harness/repository_verification.py"), "ready"], target, runtime)
            if incomplete.returncode == 0 or "template delivery" not in incomplete.stderr: raise RuntimeError("HDD readiness accepted incomplete delivery")
            steps.append({"name": "incomplete_delivery_stop", "status": "passed"})
        evidence["status"] = "passed"
    except (OSError, KeyError, ValueError, json.JSONDecodeError, RuntimeError) as exc: evidence["error"] = str(exc)
    output.parent.mkdir(parents=True, exist_ok=True); output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n")
    if evidence["status"] != "passed": print(f"suite_conformance: {evidence.get('error', 'failed')}", file=sys.stderr); return 1
    print(f"suite conformance passed: {output}"); return 0


parser = argparse.ArgumentParser(description=__doc__); parser.add_argument("mode", choices=("verify", "gate"), nargs="?", default="verify")
parser.add_argument("--sync-repo", default=os.environ.get("SYNC_REPO")); parser.add_argument("--hdd-repo", default=os.environ.get("HDD_REPO")); parser.add_argument("--output", type=Path, default=root / ".artifacts/suite/conformance.json")
args = parser.parse_args()
if args.mode == "gate" and not is_source(): print("suite conformance not applicable outside the canonical Scaffold Source"); raise SystemExit(0)
raise SystemExit(verify(args.sync_repo, args.hdd_repo, args.output))
