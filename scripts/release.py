#!/usr/bin/env python3
"""Verify one approved source commit once, then package exactly that commit."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile


class ReleaseError(RuntimeError):
    pass


PLATFORMS = ("darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64")
VERSION = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?")


def run(arguments, root, environment, capture=False):
    result = subprocess.run(arguments, cwd=root, env=environment, text=True,
                            stdout=subprocess.PIPE if capture else None, check=False)
    if result.returncode:
        raise ReleaseError(f"command failed ({result.returncode}): {arguments[0]}")
    return result.stdout.strip() if capture else None


def git(root, environment, *arguments):
    return run(["git", *arguments], root, environment, capture=True)


def identity(root, environment, compare_ref):
    if not compare_ref:
        raise ReleaseError("an explicit VERIFY_COMPARE_REF is required")
    if Path(git(root, environment, "rev-parse", "--show-toplevel")).resolve() != root:
        raise ReleaseError("release must run from the selected repository root")
    for entry in git(root, environment, "ls-files", "-v", "-z").split("\0"):
        if entry and (entry[0].islower() or entry[0] == "S"):
            raise ReleaseError("release rejects assume-unchanged and skip-worktree index flags")
    if git(root, environment, "status", "--porcelain=v1", "--untracked-files=all"):
        raise ReleaseError("release requires a clean working tree, including untracked files")
    source = {
        "head_sha": git(root, environment, "rev-parse", "--verify", "HEAD^{commit}"),
        "head_tree_sha": git(root, environment, "rev-parse", "--verify", "HEAD^{tree}"),
        "compare_sha": git(root, environment, "rev-parse", "--verify", "--end-of-options", f"{compare_ref}^{{commit}}"),
    }
    verify_source_bytes(root, environment, source["head_sha"])
    return source


def verify_source_bytes(root, environment, head, source_root=None):
    # status/diff honor index flags and clean filters; verification must execute
    # the literal committed bytes that git archive will subsequently package.
    source_root = root if source_root is None else source_root
    for entry in git(root, environment, "ls-tree", "-r", "-z", head).split("\0"):
        if not entry:
            continue
        metadata, relative = entry.split("\t", 1)
        mode, kind, expected = metadata.split()
        path = source_root / relative
        if kind != "blob" or mode not in ("100644", "100755", "120000"):
            raise ReleaseError(f"unsupported committed source type: {relative}")
        if any(parent.is_symlink() for parent in path.parents if parent != source_root and source_root in parent.parents):
            raise ReleaseError(f"source parent is a symlink: {relative}")
        try:
            info = path.lstat()
        except OSError as error:
            raise ReleaseError(f"committed source is unavailable: {relative}") from error
        if mode == "120000":
            if not stat.S_ISLNK(info.st_mode):
                raise ReleaseError(f"source type differs from committed blob: {relative}")
            data = os.fsencode(os.readlink(path))
        else:
            if not stat.S_ISREG(info.st_mode) or bool(info.st_mode & stat.S_IXUSR) != (mode == "100755"):
                raise ReleaseError(f"source type or executable mode differs from committed blob: {relative}")
            descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
            with os.fdopen(descriptor, "rb") as stream:
                data = stream.read()
        actual = hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()
        if actual != expected:
            raise ReleaseError(f"source bytes differ from committed blob: {relative}")


def require_approval(environment, source):
    evidence = environment.get("AI_BOUNDARY_APPROVAL_EVIDENCE", "").strip()
    if (environment.get("AI_BOUNDARY_APPROVED") != "1" or
            not evidence.startswith(("owner-request:", "github:")) or
            len(evidence) > 512 or any(c in evidence for c in "\x00\r\n")):
        raise ReleaseError("release requires explicit owner approval and canonical Harness approval evidence")
    for name, key in (("RELEASE_APPROVED_HEAD_SHA", "head_sha"),
                      ("RELEASE_APPROVED_COMPARE_SHA", "compare_sha")):
        if environment.get(name) != source[key]:
            raise ReleaseError(f"{name} must match the commit covered by the owner approval")
    return evidence


def regular_json(path):
    if path.is_symlink() or not path.is_file():
        raise ReleaseError(f"expected a regular file: {path.name}")
    try:
        value = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise ReleaseError(f"invalid JSON: {path.name}") from error
    if not isinstance(value, dict):
        raise ReleaseError(f"expected a JSON object: {path.name}")
    return value


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def qualification_inputs(environment):
    scope = environment.get("RELEASE_EVAL_SCOPE", "strict")
    limitation = environment.get("RELEASE_EVAL_LIMITATION_EVIDENCE", "").strip()
    if scope not in ("strict", "limited"):
        raise ReleaseError("HOLD: RELEASE_EVAL_SCOPE must be strict or limited")
    if scope == "limited" and (not limitation or len(limitation) > 512 or any(c in limitation for c in "\x00\r\n")):
        raise ReleaseError("HOLD: limited qualification requires explicit RELEASE_EVAL_LIMITATION_EVIDENCE")
    names = ("RELEASE_EVAL_SUITE", "RELEASE_EVAL_EVIDENCE", "RELEASE_EVAL_CONTRACT")
    if any(not environment.get(name) for name in names):
        raise ReleaseError("HOLD: release requires RELEASE_EVAL_SUITE, RELEASE_EVAL_EVIDENCE and RELEASE_EVAL_CONTRACT")
    if not re.fullmatch(r"contract-v1:sha256:[0-9a-f]{64}", environment[names[2]]):
        raise ReleaseError("HOLD: RELEASE_EVAL_CONTRACT must be a current contract-v1:sha256 digest")
    suite, evidence = (Path(environment[name]) for name in names[:2])
    if not suite.is_absolute() or not evidence.is_absolute():
        raise ReleaseError("HOLD: evaluation suite and evidence must use absolute paths")
    config = regular_json(suite).get("review_config")
    regular_json(evidence)
    if (not isinstance(config, dict) or set(config) != {"host", "model", "reasoning_effort"} or
            config.get("host") not in ("codex", "claude-code") or
            any(not isinstance(value, str) or not value.strip() for value in config.values())):
        raise ReleaseError("HOLD: trusted suite requires explicit review_config host, model and reasoning_effort")
    return {"suite": suite, "evidence": evidence, "config": config,
            "scope": scope, "limitation_evidence": limitation if scope == "limited" else "",
            "contract": environment[names[2]], "suite_sha256": digest(suite), "evidence_sha256": digest(evidence)}


def qualify(snapshot, root, source, binary, inputs, environment):
    config = inputs["config"]
    plan = json.loads(run([str(binary), "plan", "--host", config["host"], "--model", config["model"],
                          "--reasoning-effort", config["reasoning_effort"], "--execution-profile", "production-ci",
                          "--review-scope", "full", "--repo", str(root), "--base", source["compare_sha"],
                          "--target", source["head_sha"]], root, environment, capture=True))
    if (plan.get("status") != "READY" or plan.get("provider_invocations") != 0 or
            plan.get("current_head") != source["head_sha"] or
            plan.get("request", {}).get("base_commit") != source["compare_sha"] or
            plan.get("contract_digest") != inputs["contract"]):
        raise ReleaseError("HOLD: evaluation contract must match the current built CLI and approved commit range")
    command = [sys.executable, "-B", str(snapshot / "pilot/review_evidence.py"), "verify",
               "--suite", str(inputs["suite"]), "--evidence", str(inputs["evidence"]),
               "--expected-contract", inputs["contract"], "--quality-bin", str(binary)]
    if inputs["scope"] == "limited":
        command.append("--allow-incomplete")
    result = subprocess.run(command,
                            cwd=snapshot, env=environment, text=True, capture_output=True, check=False)
    try:
        output = json.loads(result.stdout)
    except ValueError as error:
        raise ReleaseError("HOLD: evaluation verification did not return a JSON result") from error
    if not isinstance(output, dict):
        raise ReleaseError("HOLD: invalid evaluation result")
    write_json(root / ".artifacts/release-qualification.json", {
        **output, "scope": inputs["scope"], "limitation_evidence": inputs["limitation_evidence"],
    })
    expected_status = "LIMITED" if inputs["scope"] == "limited" else "PASS"
    if result.returncode or output.get("status") != expected_status:
        raise ReleaseError(f"{output.get('status', 'HOLD')}: frozen evaluation did not meet {inputs['scope']} qualification; see .artifacts/release-qualification.json")
    if (digest(inputs["suite"]) != inputs["suite_sha256"] or digest(inputs["evidence"]) != inputs["evidence_sha256"] or
            output.get("contract_digest") != inputs["contract"] or output.get("suite_sha256") != "sha256:" + inputs["suite_sha256"]):
        raise ReleaseError("HOLD: evaluation inputs or qualification identity changed")
    return {"verification": output, "suite_sha256": inputs["suite_sha256"],
            "scope": inputs["scope"], "limitation_evidence": inputs["limitation_evidence"],
            "evidence_sha256": inputs["evidence_sha256"], "review_config": config}


def check_dist(path):
    if path.is_symlink() or (path.exists() and not path.is_dir()):
        raise ReleaseError("dist must be a regular directory, never a symlink")
    if not path.exists():
        return
    manifest = regular_json(path / "release-manifest.json")
    assets = manifest.get("assets")
    if manifest.get("schema_version") != 1 or manifest.get("product") != "quality-review" or not isinstance(assets, dict):
        raise ReleaseError("refusing to replace an unrecognized dist directory")
    expected = set(assets) | {"release-manifest.json", "checksums.txt"}
    if set(p.name for p in path.iterdir()) != expected:
        raise ReleaseError("dist contains files outside the previous release manifest")
    for name, sha256 in assets.items():
        asset = path / name
        if Path(name).name != name or asset.is_symlink() or not asset.is_file() or digest(asset) != sha256:
            raise ReleaseError("previous dist is modified; preserve it before packaging again")


def archive_source(root, destination, source, environment):
    archive = destination.parent / "source.tar"
    with archive.open("wb") as stream:
        result = subprocess.run(["git", "archive", "--format=tar", source["head_sha"]],
                                cwd=root, env=environment, stdout=stream, check=False)
    if result.returncode:
        raise ReleaseError("cannot archive the frozen source commit")
    destination.mkdir()
    with tarfile.open(archive) as bundle:
        # Python's data filter rejects absolute paths, traversal and escaping links.
        bundle.extractall(destination, filter="data")


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def create_release(root, version, compare_ref, environment=None):
    root = Path(root).resolve()
    environment = dict(os.environ if environment is None else environment)
    if not VERSION.fullmatch(version):
        raise ReleaseError("VERSION must be an explicit vX.Y.Z release version")
    source = identity(root, environment, compare_ref)
    approval = require_approval(environment, source)
    evaluation = qualification_inputs(environment)
    dist = root / "dist"
    check_dist(dist)
    artifacts = root / ".artifacts"
    if artifacts.is_symlink() or (artifacts.exists() and not artifacts.is_dir()):
        raise ReleaseError(".artifacts must be a regular directory")
    artifacts.mkdir(exist_ok=True)
    evidence_dir = artifacts / "release"
    if evidence_dir.is_symlink():
        raise ReleaseError("release evidence must not be a symlink")
    environment.update({
        "VERIFY_COMPARE_REF": source["compare_sha"],
        "VERIFY_PROFILE": "release", "VERIFY_EVIDENCE_MODE": "release",
        "VERIFY_ARTIFACT_DIR": str(evidence_dir),
        "AI_BOUNDARY_APPROVAL_MODE": "required",
        "AI_BOUNDARY_APPROVAL_EVIDENCE": approval,
        "CODE_QUALITY_RELEASE_VERSION": version,
        "HARNESS_PROJECT_ROOT": str(root),
        "PYTHONDONTWRITEBYTECODE": "1",
    })
    # Harness invokes the native release-check gate. Never invoke it again here.
    run([str(root / "harness/repository_verification.py"), "verify", "release"], root, environment)
    if identity(root, environment, compare_ref) != source:
        raise ReleaseError("source or baseline changed during release verification")
    verify_command = [str(root / "scripts/harnessctl.sh"), "evidence", "verify",
                      "--evidence-dir", str(evidence_dir), "--compare-ref", source["compare_sha"],
                      "--mode", "release", "--profile", "release"]
    run(verify_command, root, environment)
    summary = regular_json(evidence_dir / "summary.json")
    manifest = regular_json(evidence_dir / "artifact_manifest.json")
    if (summary.get("overall") != "passed" or summary.get("release_ready") is not True or
            summary.get("mode") != "release" or summary.get("profile") != "release" or
            any(summary.get("git", {}).get(key) != value for key, value in source.items()) or
            summary.get("approval", {}).get("approval_evidence") != approval or
            summary.get("approval", {}).get("approved") is not True):
        raise ReleaseError("verified release evidence does not match source and owner approval")
    evidence_hashes = {name: digest(evidence_dir / name)
                       for name in ("summary.json", "artifact_manifest.json")}
    with tempfile.TemporaryDirectory(prefix="distribution-", dir=artifacts) as temporary:
        temporary = Path(temporary)
        snapshot, package = temporary / "source", temporary / "package"
        archive_source(root, snapshot, source, environment)
        # archive attributes (export-ignore/export-subst) must not change the
        # source that was tested, even when the Git identity is unchanged.
        verify_source_bytes(root, environment, source["head_sha"], snapshot)
        package.mkdir()
        build_environment = dict(environment, GOWORK="off", GOFLAGS="", CGO_ENABLED="0")
        toolchain = run(["go", "version"], snapshot, build_environment, capture=True)
        host = platform.system().lower() + "/" + {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64"}.get(platform.machine(), "unsupported")
        if host not in PLATFORMS:
            raise ReleaseError("HOLD: release qualification requires a supported native build host")
        qualification = None
        for target in (host, *(target for target in PLATFORMS if target != host)):
            target_os, architecture = target.split("/")
            build_environment.update(GOOS=target_os, GOARCH=architecture)
            binary = package / f"quality-review_{target_os}_{architecture}"
            run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags",
                 f"-s -w -X main.version={version}", "-o",
                 str(binary), "./cmd/quality-review"],
                snapshot, build_environment)
            if target == host:
                qualification = qualify(snapshot, root, source, binary, evaluation, environment)
        for source_name, target_name in (("install.sh", "install.sh"),
                                         ("plugins/code-quality/scripts/bootstrap.sh", "bootstrap.sh")):
            source_path = snapshot / source_name
            if source_path.is_symlink() or not source_path.is_file():
                raise ReleaseError("release installer must be a regular committed file")
            shutil.copy2(source_path, package / target_name)
        write_json(package / "release-evidence.json", {
            "schema_version": 1, "summary": summary, "artifact_manifest": manifest,
            "sha256": evidence_hashes,
        })
        write_json(package / "release-qualification.json", qualification)
        write_json(package / "release-manifest.json", {
            "schema_version": 1, "product": "quality-review", "version": version,
            "source": source, "toolchain": toolchain,
            "qualification": qualification,
            "assets": {path.name: digest(path) for path in sorted(package.iterdir())},
        })
        (package / "checksums.txt").write_text("".join(
            f"{digest(path)}  {path.name}\n" for path in sorted(package.iterdir())))
        if identity(root, environment, compare_ref) != source:
            raise ReleaseError("source or baseline changed while packaging")
        if any(digest(evidence_dir / name) != sha256 for name, sha256 in evidence_hashes.items()):
            raise ReleaseError("release evidence changed while packaging")
        check_dist(dist)
        backup = temporary / "previous-dist"
        if dist.exists():
            dist.rename(backup)
        try:
            package.rename(dist)
        except OSError:
            if backup.exists():
                backup.rename(dist)
            raise
    return {"version": version, "source": source, "directory": str(dist)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--compare-ref", required=True)
    args = parser.parse_args()
    try:
        result = create_release(Path(__file__).resolve().parent.parent, args.version, args.compare_ref)
    except (OSError, ValueError, tarfile.TarError, ReleaseError) as error:
        parser.exit(1, f"release: {error}\n")
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
