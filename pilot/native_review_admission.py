#!/usr/bin/env python3
"""Validate the human-audited native-review evaluation admission manifest."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import sys

from qualification_inventory import validate_fixture


EXPORTED_FUNCTION = re.compile(
    r"^func\s+(?:(?P<receiver>\([^)]*\))\s+)?(?P<name>[A-Z][A-Za-z0-9_]*)\s*(?P<signature>\([^)]*\)(?:\s*[^\{]+)?)\s*\{\s*$"
)

EXPECTED_SCORING = {
    "positive": "at_least_one_actionable_introduced_defect",
    "counterexample": "no_actionable_introduced_defect",
    "insufficient": "human_only_not_automatically_scored",
    "extra_valid_findings": "allowed",
    "legacy_rule_severity_trigger_evidence_and_count": "diagnostic_only",
}


def file_sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree_sha256(root: pathlib.Path) -> str:
    digest = hashlib.sha256()
    entries = sorted(root.rglob("*"))
    for path in entries:
        relative = path.relative_to(root).as_posix()
        if path.is_symlink():
            raise ValueError(f"fixture tree contains symlink: {relative}")
        if not path.is_file():
            continue
        relative_bytes = relative.encode()
        digest.update(len(relative_bytes).to_bytes(8, "big"))
        digest.update(relative_bytes)
        content = path.read_bytes()
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return digest.hexdigest()


def exported_go_surface(root: pathlib.Path) -> dict[str, str]:
    surface: dict[str, str] = {}
    for path in sorted(root.rglob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for raw_line in path.read_text(encoding="utf-8").splitlines():
            match = EXPORTED_FUNCTION.match(raw_line.strip())
            if not match:
                continue
            receiver = match.group("receiver") or ""
            receiver_types = re.findall(r"\*?[A-Z][A-Za-z0-9_]*", receiver)
            owner = receiver_types[-1].lstrip("*") + "." if receiver_types else ""
            key = owner + match.group("name")
            signature = re.sub(r"\s+", " ", match.group("signature").strip())
            if key in surface:
                raise ValueError(f"duplicate exported Go function in {root}: {key}")
            surface[key] = signature
    return surface


def counterexample_api_issues(cases: list[dict[str, object]], fixtures_root: pathlib.Path) -> dict[str, list[str]]:
    issues: dict[str, list[str]] = {}
    for case in cases:
        if case.get("kind") != "counterexample":
            continue
        fixture = case.get("fixture")
        if not isinstance(fixture, dict) or fixture.get("language") != "go":
            continue
        case_id = str(case["id"])
        root = fixtures_root / case_id.lower()
        base = exported_go_surface(root / "base")
        target = exported_go_surface(root / "target")
        messages = []
        for name, signature in sorted(base.items()):
            if name not in target:
                messages.append(f"removed exported function {name}")
            elif target[name] != signature:
                messages.append(f"changed exported function {name}: {signature} -> {target[name]}")
        if messages:
            issues[case_id] = messages
    return issues


def string_list(value: object, field: str) -> list[str]:
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise ValueError(f"{field} must be a list of nonempty strings")
    if len(value) != len(set(value)):
        raise ValueError(f"{field} contains duplicates")
    return value


def audit_inventory(
    cases_path: pathlib.Path,
    fixtures_root: pathlib.Path,
    admission_path: pathlib.Path,
) -> dict[str, object]:
    payload = json.loads(cases_path.read_text(encoding="utf-8"))
    cases = payload.get("cases")
    if not isinstance(cases, list):
        raise ValueError("cases.json must contain a cases array")
    case_by_id = {str(case["id"]): case for case in cases if isinstance(case, dict) and isinstance(case.get("id"), str)}
    if len(case_by_id) != len(cases):
        raise ValueError("cases.json contains missing or duplicate ids")

    admission = json.loads(admission_path.read_text(encoding="utf-8"))
    if admission.get("schema_version") != 1 or admission.get("profile") != "native_review_vnext":
        raise ValueError("native-review admission header is invalid")
    qualified = string_list(admission.get("qualified_case_ids"), "qualified_case_ids")
    human_only = string_list(admission.get("human_only_case_ids"), "human_only_case_ids")
    blocked = admission.get("blocked_cases")
    if not isinstance(blocked, dict) or any(not isinstance(key, str) or not isinstance(value, str) for key, value in blocked.items()):
        raise ValueError("blocked_cases must map case ids to reasons")

    classified = qualified + human_only + list(blocked)
    unknown = sorted(set(classified) - set(case_by_id))
    missing = sorted(set(case_by_id) - set(classified))
    duplicates = sorted({case_id for case_id in classified if classified.count(case_id) > 1})
    errors: list[str] = []
    scoring_contract_match = admission.get("scoring") == EXPECTED_SCORING
    if not scoring_contract_match:
        errors.append("scoring contract does not match native-review vNext")
    if unknown:
        errors.append("unknown cases: " + ", ".join(unknown))
    if missing:
        errors.append("unclassified cases: " + ", ".join(missing))
    if duplicates:
        errors.append("multiply classified cases: " + ", ".join(duplicates))

    qualified_kind_totals: dict[str, int] = {}
    for case_id in qualified:
        kind = str(case_by_id[case_id]["kind"])
        qualified_kind_totals[kind] = qualified_kind_totals.get(kind, 0) + 1
        if kind not in {"positive", "counterexample"}:
            errors.append(f"{case_id}: {kind} cannot be automatically qualified")
    for case_id in human_only:
        if case_by_id[case_id].get("kind") != "insufficient":
            errors.append(f"{case_id}: only insufficient cases may be human-only")

    invalid_fixtures: dict[str, list[str]] = {}
    for case_id, case in case_by_id.items():
        fixture = case.get("fixture")
        language = fixture.get("language") if isinstance(fixture, dict) else None
        fixture_errors = validate_fixture(fixtures_root / case_id.lower(), language)
        if fixture_errors:
            invalid_fixtures[case_id] = fixture_errors

    actual_cases_sha256 = file_sha256(cases_path)
    actual_fixtures_sha256 = tree_sha256(fixtures_root)
    hashes_match = (
        admission.get("cases_sha256") == actual_cases_sha256
        and admission.get("fixtures_sha256") == actual_fixtures_sha256
    )
    if not hashes_match:
        errors.append("frozen cases or fixture hash does not match the admission manifest")
    if invalid_fixtures:
        errors.append("one or more fixtures are not reviewable")

    api_issues = counterexample_api_issues(cases, fixtures_root)
    if api_issues:
        errors.append("counterexamples remove or change exported Go functions")

    report = {
        "schema_version": 1,
        "profile": "native_review_vnext",
        "total_cases": len(cases),
        "qualified_cases": len(qualified),
        "human_only_cases": len(human_only),
        "blocked_cases": blocked,
        "qualified_kind_totals": dict(sorted(qualified_kind_totals.items())),
        "scoring_contract_match": scoring_contract_match,
        "cases_sha256": actual_cases_sha256,
        "fixtures_sha256": actual_fixtures_sha256,
        "hashes_match": hashes_match,
        "invalid_fixtures": invalid_fixtures,
        "counterexample_api_issues": api_issues,
        "errors": errors,
        "admission_ready": not errors and not blocked,
    }
    return report


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cases", type=pathlib.Path, default=pathlib.Path("evals/cases.json"))
    parser.add_argument("--fixtures", type=pathlib.Path, default=pathlib.Path("pilot/fixtures"))
    parser.add_argument("--admission", type=pathlib.Path, default=pathlib.Path("evals/native-review-admission.json"))
    args = parser.parse_args()
    report = audit_inventory(args.cases.resolve(strict=True), args.fixtures.resolve(strict=True), args.admission.resolve(strict=True))
    json.dump(report, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0 if report["admission_ready"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
