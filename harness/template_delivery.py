#!/usr/bin/env python3
"""Cold-start or upgrade one target from the canonical Scaffold Source."""

import json, os, re, subprocess, sys
from pathlib import Path, PurePosixPath

root = Path(os.environ.get("HARNESS_PROJECT_ROOT", Path(__file__).resolve().parent.parent)).resolve()
engine = root / "scripts/harnessctl.sh"
patterns = {
    "module": re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~/-]*$"),
    "service": re.compile(r"^[a-z][a-z0-9-]*$"),
    "owner": re.compile(r"^@[A-Za-z0-9][A-Za-z0-9-]{0,38}(?:/[A-Za-z0-9][A-Za-z0-9-]{0,99})?$"),
}


def fail(message): raise SystemExit(f"template_delivery: {message}")


def run(command, cwd=root, env=None):
    return subprocess.run(command, cwd=cwd, env=env, text=True, capture_output=True, check=False)


def git(repo, *arguments):
    result = run(["git", "-C", str(repo), *arguments], repo)
    if result.returncode: fail(result.stderr.strip() or "Git command failed")
    return result.stdout.strip()


def load(path, label):
    try: payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc: fail(f"invalid {label}: {exc}")
    if not isinstance(payload, dict): fail(f"invalid {label}")
    return payload


def normalized(value):
    value = value.strip().removesuffix(".git").removesuffix("/")
    for prefix in ("git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/"):
        if value.startswith(prefix): return "github.com/" + value[len(prefix):]
    return value


def source_contract():
    contract = load(root / "harness/suite_contract.json", "suite contract")
    source = contract.get("scaffold_source", {})
    if contract.get("schema_version") != 1 or contract.get("contract_version") != 1: fail("unsupported suite contract")
    if source.get("delivery_intents") != ["bootstrap", "upgrade"]: fail("invalid delivery intents")
    if Path.cwd().resolve() != root or Path(git(root, "rev-parse", "--show-toplevel")).resolve() != root: fail("run from the exact scaffold root")
    remote = run(["git", "-C", str(root), "remote", "get-url", "origin"], root)
    if remote.returncode or normalized(remote.stdout) != source.get("repository"): fail("current repository is not the canonical Scaffold Source")
    if git(root, "status", "--porcelain=v1", "--untracked-files=all"): fail("Scaffold Source must be clean")
    if not engine.is_file() or engine.is_symlink() or not os.access(engine, os.X_OK): fail("scripts/harnessctl.sh must be an executable regular file")
    return contract


def target_root(raw, clean):
    candidate = Path(raw)
    if not candidate.is_absolute(): fail("target must be an absolute path")
    if candidate.is_symlink() or not candidate.is_dir(): fail("target must be a non-symlink directory")
    target = candidate.resolve()
    if target == root or Path(git(target, "rev-parse", "--show-toplevel")).resolve() != target: fail("target must be a different Git repository root")
    if clean and git(target, "status", "--porcelain=v1", "--untracked-files=all"): fail("Target Repository must be clean")
    return target


def delivery(arguments):
    if len(arguments) < 4 or arguments[:3:2] != ["--intent", "--target"]: fail("requires --intent bootstrap|upgrade --target ABSOLUTE_PATH")
    if arguments[1] not in {"bootstrap", "upgrade"}: fail("intent must be bootstrap or upgrade")
    return arguments[1], arguments[3], arguments[4:]


def audit(target, drift):
    result = run([str(engine), "scaffold", "audit", "--template", str(root), "--repo", str(target)])
    if result.returncode not in ({0, 1} if drift else {0}): fail(result.stderr.strip() or "scaffold audit failed")
    try: payload = json.loads(result.stdout)
    except json.JSONDecodeError as exc: fail(f"scaffold audit returned invalid JSON: {exc}")
    if not drift and (not payload.get("converged") or payload.get("target_dirty")): fail("Target Repository is not clean and converged")
    return payload


def preflight(arguments):
    intent, raw, extra = delivery(arguments)
    if extra: fail("preflight accepts no extra arguments")
    source_contract(); target = target_root(raw, True); has_lock = (target / "harness/scaffold.lock").is_file()
    if (intent == "bootstrap" and has_lock) or (intent == "upgrade" and not has_lock): fail(f"{intent} intent does not match the target delivery record")
    report = audit(target, True)
    print(json.dumps({"intent": intent, "source_commit": git(root, "rev-parse", "HEAD"), "status": "ready", "target": str(target), "target_converged": bool(report.get("converged"))}, sort_keys=True))


def record(arguments):
    intent, raw, extra = delivery(arguments); source_contract(); target = target_root(raw, False)
    command = [str(engine), "scaffold", "record", "--template", str(root), "--repo", str(target)]
    while extra:
        if len(extra) < 2 or extra[0] != "--resolution": fail("record accepts repeated --resolution path=decision")
        command += extra[:2]; extra = extra[2:]
    result = run(command)
    if result.returncode: fail(result.stderr.strip() or "scaffold record failed")
    try: lock = json.loads(result.stdout)
    except json.JSONDecodeError as exc: fail(f"scaffold record returned invalid JSON: {exc}")
    path, temporary = target / "harness/scaffold.lock", target / "harness/.scaffold.lock.tmp"
    path.parent.mkdir(parents=True, exist_ok=True); temporary.write_text(json.dumps(lock, separators=(",", ":")) + "\n", encoding="utf-8"); os.replace(temporary, path)
    print(json.dumps({"intent": intent, "status": "recorded", "target": str(target), "template_commit": lock.get("template_commit")}, sort_keys=True))


def complete(arguments):
    intent, raw, extra = delivery(arguments)
    if extra: fail("complete accepts no extra arguments")
    contract = source_contract(); target = target_root(raw, True)
    if not (target / "harness/scaffold.lock").is_file(): fail("Target Repository lacks a delivery record")
    report = audit(target, False); runtime = os.environ.copy(); runtime["HARNESS_PROJECT_ROOT"] = str(target)
    ready = run([sys.executable, "-I", "-B", "-S", str(target / "harness/repository_verification.py"), "ready"], target, runtime)
    if ready.returncode: fail(ready.stderr.strip() or "Target Repository readiness failed")
    print(json.dumps({"contract_version": contract["contract_version"], "intent": intent, "status": "delivered", "target": str(target), "template_commit": report["template_commit"]}, sort_keys=True))


def initialize(arguments):
    if arguments == ["--help"]: print("usage: scripts/init_project.sh --module MODULE --service SERVICE --owner OWNER"); return
    if len(arguments) != 6 or arguments[::2] != ["--module", "--service", "--owner"]: fail("init requires --module, --service, and --owner")
    values = dict(zip(patterns, arguments[1::2]))
    if any(not patterns[name].fullmatch(value) for name, value in values.items()) or "//" in values["module"] or values["module"].endswith("/"): fail("invalid initialization value")
    contract = load(root / "harness/project_template.json", "project template contract"); rules = contract.get("replacements", [])
    if contract.get("schema_version") != 1 or [rule.get("argument") for rule in rules] != list(patterns): fail("invalid project template contract")
    originals, planned = {}, {}
    for rule in rules:
        if not rule.get("edits"): fail("invalid project template contract")
        for edit in rule["edits"]:
            relative, old, new = edit.get("path"), edit.get("old"), edit.get("new"); parsed = PurePosixPath(relative) if isinstance(relative, str) else PurePosixPath(".."); path = root / parsed
            if not isinstance(old, str) or not old or not isinstance(new, str) or new.count("{value}") != 1 or parsed.is_absolute() or ".." in parsed.parts or path.is_symlink() or not path.is_file() or root not in path.resolve().parents: fail("project template contains an unsafe edit")
            originals.setdefault(path, path.read_text(encoding="utf-8")); content = planned.get(path, originals[path])
            if old not in content: fail(f"{rule['argument']} placeholder is absent from {relative}")
            planned[path] = content.replace(old, new.replace("{value}", values[rule["argument"]]))
    for path, content in planned.items():
        if content != originals[path]: path.write_text(content, encoding="utf-8")
    print("initialized project: " + " ".join(f"{name}={values[name]}" for name in patterns))


arguments = sys.argv[1:]
if not arguments: fail("expected init, preflight, record, or complete")
command = arguments.pop(0)
if command == "init": initialize(arguments)
elif command == "preflight": preflight(arguments)
elif command == "record": record(arguments)
elif command == "complete": complete(arguments)
else: fail("unknown command")
