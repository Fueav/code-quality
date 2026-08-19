#!/usr/bin/env python3
"""Verify repository readiness or delegate pinned Harness operations."""

import hashlib, json, os, re, subprocess, sys
from pathlib import Path, PurePosixPath

root = Path(os.environ.get("HARNESS_PROJECT_ROOT", Path(__file__).resolve().parent.parent)).resolve()
engine = root / "scripts/harnessctl.sh"
sha40, sha64 = re.compile(r"^[0-9a-f]{40}$"), re.compile(r"^[0-9a-f]{64}$")


def fail(message): raise SystemExit(f"repository_verification: {message}")


def run(command, check=True):
    result = subprocess.run(command, cwd=root, text=True, capture_output=True, check=False)
    if check and result.returncode: fail(result.stderr.strip() or "command failed")
    return result


def git(*arguments, check=True): return run(["git", "-C", str(root), *arguments], check)


def delegate(*arguments):
    if not engine.is_file() or engine.is_symlink() or not os.access(engine, os.X_OK): fail("scripts/harnessctl.sh must be an executable regular file")
    os.execv(engine, [str(engine), *arguments])


def load(relative, delivery=False):
    try: payload = json.loads((root / relative).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc: fail(("template delivery is incomplete: " if delivery else "") + f"invalid {relative}: {exc}")
    if not isinstance(payload, dict): fail(f"invalid {relative}")
    return payload


def safe(relative):
    if not isinstance(relative, str): fail("suite contract contains a non-string path")
    parsed = PurePosixPath(relative); path = root / parsed; resolved = path.resolve(strict=False)
    if parsed.is_absolute() or ".." in parsed.parts or resolved == root or root not in resolved.parents: fail("suite contract contains an unsafe path")
    return path


def digest(path):
    if not path.is_file() or path.is_symlink(): fail(f"required regular file is missing: {path.relative_to(root)}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


def readiness():
    contract = load("harness/suite_contract.json", True)
    if set(contract) != {"schema_version", "contract_version", "scaffold_source", "repository_contract"} or (contract.get("schema_version"), contract.get("contract_version")) != (1, 1): fail("template delivery is incomplete: unsupported suite contract")
    if contract.get("scaffold_source") != {"repository": "github.com/moss-site/ai-first-go-template", "delivery_intents": ["bootstrap", "upgrade"]}: fail("template delivery is incomplete: invalid Scaffold Source contract")
    repository = contract.get("repository_contract")
    if not isinstance(repository, dict) or set(repository) != {"required_paths", "executable_paths", "schema_versions"}: fail("template delivery is incomplete: invalid repository contract")
    required, executables, schemas = repository["required_paths"], repository["executable_paths"], repository["schema_versions"]
    if not isinstance(required, list) or len(required) != len(set(required)) or not isinstance(executables, list) or not isinstance(schemas, dict): fail("template delivery is incomplete: invalid repository contract")
    paths = {relative: safe(relative) for relative in required}
    for relative, path in paths.items():
        if not path.is_file() or path.is_symlink(): fail(f"template delivery is incomplete: missing required path {relative}")
    for relative in executables:
        if relative not in paths or not os.access(safe(relative), os.X_OK): fail(f"template delivery is incomplete: non-executable interface {relative}")
    for relative, rule in schemas.items():
        if relative not in paths or not isinstance(rule, dict) or set(rule) != {"field", "allowed"}: fail("template delivery is incomplete: invalid schema rule")
        payload = load(relative, True)
        if not isinstance(rule["field"], str) or not isinstance(rule["allowed"], list) or payload.get(rule["field"]) not in rule["allowed"]: fail(f"template delivery is incomplete: unsupported schema at {relative}")
    manifest, lock = load("harness/scaffold_manifest.json", True), load("harness/scaffold.lock", True)
    if not sha40.fullmatch(str(lock.get("template_commit", ""))) or lock.get("manifest_sha256") != digest(root / "harness/scaffold_manifest.json"): fail("template delivery is incomplete: stale scaffold lock")
    managed = manifest.get("managed_paths")
    if manifest.get("schema_version") != 1 or not isinstance(managed, list) or not isinstance(manifest.get("retired_paths"), list): fail("template delivery is incomplete: invalid scaffold manifest")
    semantic = {}
    for item in managed:
        if not isinstance(item, dict) or item.get("strategy") not in {"copy", "symlink", "manual_merge", "project_overlay"}: fail("template delivery is incomplete: invalid managed path")
        relative, strategy, required_here = item.get("path"), item["strategy"], item.get("required", True); path = safe(relative)
        if item.get("when_target_exists"):
            target = item.get("target")
            if not isinstance(target, str): fail("template delivery is incomplete: invalid managed symlink")
            resolved = (path.parent / target).resolve(strict=False)
            if root not in resolved.parents: fail("template delivery is incomplete: unsafe managed symlink")
            required_here = resolved.exists()
        if strategy == "symlink":
            if required_here and (not path.is_symlink() or os.readlink(path) != item.get("target")): fail(f"template delivery is incomplete: invalid managed symlink {relative}")
        elif required_here and (not path.is_file() or path.is_symlink()): fail(f"template delivery is incomplete: missing managed path {relative}")
        if strategy in {"manual_merge", "project_overlay"} and path.is_file() and not path.is_symlink(): semantic[relative] = (strategy, digest(path))
    resolutions = lock.get("resolved_paths")
    if not isinstance(resolutions, list): fail("template delivery is incomplete: invalid scaffold resolutions")
    by_path = {item.get("path"): item for item in resolutions if isinstance(item, dict)}
    if len(by_path) != len(resolutions) or set(by_path) != set(semantic): fail("template delivery is incomplete: semantic resolution set is stale")
    for relative, (strategy, current) in semantic.items():
        item = by_path[relative]
        fields = {"path", "strategy", "resolution", "template_sha256", "target_sha256"}
        if set(item) != fields or item["strategy"] != strategy or item["resolution"] not in {"merged", "preserved", "adapted", "relocated"} or not sha64.fullmatch(str(item["template_sha256"])) or item["target_sha256"] != current: fail(f"template delivery is incomplete: semantic resolution is stale for {relative}")
    for relative in manifest["retired_paths"]:
        path = safe(relative)
        if path.exists() or path.is_symlink(): fail(f"template delivery is incomplete: retired path remains {relative}")
    print(json.dumps({"contract_version": 1, "status": "ready", "template_commit": lock["template_commit"]}, sort_keys=True))


arguments = sys.argv[1:]
if not arguments: fail("expected ready, compare, verify, review, or approval")
command = arguments.pop(0)
if command == "ready":
    if arguments: fail("ready accepts no arguments")
    readiness()
elif command == "verify":
    if not arguments or arguments[0] not in {"change", "candidate", "release"}: fail("verify requires change, candidate, or release")
    delegate("verify", *arguments)
elif command == "compare":
    if len(arguments) != 3: fail("compare requires EVENT PR_BASE_SHA PUSH_BEFORE_SHA")
    event, pr_base, before = arguments
    if event == "pull_request": selected = pr_base
    elif event == "schedule":
        commits = git("rev-list", "--first-parent", "--max-count=2", "HEAD").stdout.splitlines(); selected = commits[-1] if commits else ""
    elif event == "push" and before == "0" * 40:
        roots = sorted(git("rev-list", "--max-parents=0", "HEAD").stdout.splitlines()); selected = roots[0] if roots else ""
    elif event == "push": selected = before
    else: fail("unsupported GitHub event")
    if not sha40.fullmatch(selected): fail("compare commit must be a full SHA-1")
    if git("cat-file", "-e", f"{selected}^{{commit}}", check=False).returncode: git("fetch", "--no-tags", "--depth=1", "origin", selected)
    git("cat-file", "-e", f"{selected}^{{commit}}"); print(selected)
elif command == "review":
    if len(arguments) != 3: fail("review requires REPOSITORY PR_NUMBER HEAD_SHA")
    repository, number, head = arguments
    if repository.count("/") != 1 or repository.startswith("/") or repository.endswith("/") or not number.isdigit() or not sha40.fullmatch(head): fail("invalid review context")
    owner, name = repository.split("/"); reference = f"refs/remotes/origin/pr/{number}"; git("fetch", "--no-tags", "origin", f"+pull/{number}/head:{reference}")
    if git("rev-parse", "--verify", f"{reference}^{{commit}}").stdout.strip() != head: fail("fetched pull-request head does not match event head")
    gh = os.environ.get("HARNESS_GH_BIN", "gh"); query = "query($owner:String!,$repository:String!,$number:Int!){repository(owner:$owner,name:$repository){pullRequest(number:$number){reviewDecision}}}"
    decision = run([gh, "api", "graphql", "-f", f"query={query}", "-F", f"owner={owner}", "-F", f"repository={name}", "-F", f"number={number}", "--jq", '.data.repository.pullRequest.reviewDecision // "REVIEW_REQUIRED"']).stdout.strip()
    runs = run([gh, "api", f"repos/{repository}/actions/workflows/ci.yml/runs?event=pull_request&head_sha={head}&status=success&per_page=100", "--jq", '[.workflow_runs[] | select(.conclusion == "success")] | sort_by(.run_attempt, .id) | last | .id // empty']).stdout.strip()
    if decision not in {"APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED"} or not runs.isdigit(): fail("invalid review or candidate verification result")
    print(f"decision={decision}\nrun_id={runs}")
elif command == "approval":
    if len(arguments) != 3 or not all(sha40.fullmatch(value) for value in arguments[:2]): fail("approval requires HEAD_SHA COMPARE_SHA REVIEW_DECISION")
    delegate("approval", "finalize", "--candidate-dir", ".artifacts/candidate", "--expected-head-sha", arguments[0], "--expected-compare-sha", arguments[1], "--review-decision", arguments[2], "--output", ".artifacts/approval/finalization.json")
else: fail("unknown command")
