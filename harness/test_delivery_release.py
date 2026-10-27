"""Release failure injection uses disposable repositories and offline fake tools."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "scripts/release.py"
SPEC = importlib.util.spec_from_file_location("release", SCRIPT)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


VERIFIER = r'''#!/usr/bin/env python3
import json, os, pathlib, subprocess, sys
root = pathlib.Path.cwd()
out = root / '.artifacts/release'
out.mkdir(parents=True, exist_ok=True)
with (root / '.artifacts/trace').open('a') as stream: stream.write('verify release\n')
assert sys.argv[1:] == ['verify', 'release']
assert os.environ['VERIFY_PROFILE'] == 'release'
assert os.environ['VERIFY_EVIDENCE_MODE'] == 'release'
assert os.environ['AI_BOUNDARY_APPROVAL_MODE'] == 'required'
if os.environ.get('FIXTURE_FAILURE') == 'verification': sys.exit(19)
def git(ref): return subprocess.check_output(['git', 'rev-parse', ref], text=True).strip()
summary = {'schema_version': 1, 'overall': 'passed', 'release_ready': True,
           'mode': 'release', 'profile': 'release',
           'git': {'head_sha': git('HEAD'), 'head_tree_sha': git('HEAD^{tree}'),
                   'compare_sha': os.environ['VERIFY_COMPARE_REF']},
           'approval': {'approved': True, 'approval_satisfied': True,
                        'approval_evidence': os.environ['AI_BOUNDARY_APPROVAL_EVIDENCE']}}
(out / 'summary.json').write_text(json.dumps(summary))
(out / 'artifact_manifest.json').write_text(json.dumps({'schema_version': 1, 'artifacts': {}}))
if os.environ.get('FIXTURE_FAILURE') == 'head':
    subprocess.run(['git', 'commit', '--allow-empty', '-qm', 'concurrent commit'], check=True)
if os.environ.get('FIXTURE_FAILURE') == 'baseline':
    subprocess.run(['git', 'tag', '-f', 'baseline', 'HEAD'], check=True)
'''

EVIDENCE = r'''#!/usr/bin/env python3
import os, pathlib, sys
with pathlib.Path('.artifacts/trace').open('a') as stream: stream.write('evidence verify\n')
assert sys.argv[1:3] == ['evidence', 'verify']
assert sys.argv[-4:] == ['--mode', 'release', '--profile', 'release']
if os.environ.get('FIXTURE_FAILURE') == 'evidence': sys.exit(23)
'''

QUALIFIER = r'''#!/usr/bin/env python3
import hashlib, json, os, pathlib, sys
assert sys.argv[1] == 'verify'
with pathlib.Path(os.environ['FIXTURE_TRACE']).open('a') as stream: stream.write('qualify structural fixture\n')
suite = pathlib.Path(sys.argv[sys.argv.index('--suite') + 1])
assert pathlib.Path(sys.argv[sys.argv.index('--quality-bin') + 1]).is_file()
status = os.environ.get('FIXTURE_QUALIFICATION', 'PASS')
assert ('--allow-incomplete' in sys.argv) == (os.environ.get('RELEASE_EVAL_SCOPE') == 'limited')
print(json.dumps({'status': status, 'claim': 'STRUCTURAL_FIXTURE_ONLY',
                  'contract_digest': sys.argv[sys.argv.index('--expected-contract') + 1],
                  'incomplete_cases': ['fixture-failed-case'] if status == 'LIMITED' else [],
                  'suite_sha256': os.environ.get('FIXTURE_SUITE_DIGEST', 'sha256:' + hashlib.sha256(suite.read_bytes()).hexdigest())}))
sys.exit(0 if status in ('PASS', 'LIMITED') else 1)
'''

BUILDER = r'''#!/usr/bin/env python3
import os, pathlib, sys
if sys.argv[1:] == ['version']:
    print('go version fixture-offline')
    sys.exit(0)
with pathlib.Path(os.environ['FIXTURE_TRACE']).open('a') as stream:
    stream.write('build ' + os.environ['GOOS'] + '/' + os.environ['GOARCH'] + '\n')
if os.environ.get('FIXTURE_FAILURE') == 'build': sys.exit(29)
assert sys.argv[1] == 'build'
assert not pathlib.Path('.git').exists()
assert os.environ['CGO_ENABLED'] == '0' and os.environ['GOWORK'] == 'off'
assert os.environ['GOFLAGS'] == ''
binary = pathlib.Path(sys.argv[sys.argv.index('-o') + 1])
binary.write_text('#!/usr/bin/env python3\n# ' + repr(pathlib.Path('source.txt').read_text()) + '\n' + """
import json, os, pathlib, sys
assert sys.argv[1] == 'plan'
assert sys.argv[sys.argv.index('--execution-profile') + 1] == 'production-ci'
assert sys.argv[sys.argv.index('--review-scope') + 1] == 'full'
with pathlib.Path(os.environ['FIXTURE_TRACE']).open('a') as stream: stream.write('plan current binary\\n')
print(json.dumps({'status': 'READY', 'provider_invocations': 0,
                  'current_head': sys.argv[sys.argv.index('--target') + 1],
                  'request': {'base_commit': sys.argv[sys.argv.index('--base') + 1]},
                  'contract_digest': os.environ.get('FIXTURE_PLAN_CONTRACT', 'contract-v1:sha256:' + 'a' * 64)}))
""")
binary.chmod(0o700)
if os.environ.get('FIXTURE_FAILURE') == 'evidence_changed':
    with pathlib.Path(os.environ['VERIFY_ARTIFACT_DIR'], 'summary.json').open('a') as stream: stream.write(' ')
'''


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        temporary = Path(self.temporary.name)
        self.root, tools = temporary / "repo", temporary / "tools"
        self.root.mkdir()
        tools.mkdir()
        self.environment = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"],
                                PYTHONDONTWRITEBYTECODE="1", GIT_CONFIG_NOSYSTEM="1")
        for name in list(self.environment):
            if name.startswith(("VERIFY_", "HARNESS_", "AI_BOUNDARY_", "RELEASE_", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE")):
                self.environment.pop(name)
        self.command("init", "-q")
        self.command("config", "user.email", "fixture@example.invalid")
        self.command("config", "user.name", "Offline release fixture")
        (self.root / ".gitignore").write_text(".artifacts/\n.tools/\ndist/\n")
        (self.root / "source.txt").write_text("committed source\n")
        self.write("install.sh", "#!/bin/sh\nexit 0\n")
        self.write("plugins/code-quality/scripts/bootstrap.sh", "#!/bin/sh\nexit 0\n")
        self.write("harness/repository_verification.py", VERIFIER)
        self.write("scripts/harnessctl.sh", EVIDENCE)
        self.write("pilot/review_evidence.py", QUALIFIER)
        self.write(".ai-boundaries.yml", "allowed:\n  - README.md\napproval_required:\n  - source.txt\n  - harness/\n  - scripts/\n  - .ai-boundaries.yml\nforbidden:\n  - secrets/\n")
        self.command("add", "-A")
        self.command("commit", "-qm", "base")
        self.base = self.command("rev-parse", "HEAD")
        self.command("tag", "baseline")
        (self.root / "source.txt").write_text("release source\n")
        self.command("add", "source.txt")
        self.command("commit", "-qm", "release")
        self.head = self.command("rev-parse", "HEAD")
        builder = tools / "go"
        builder.write_text(BUILDER)
        builder.chmod(0o700)
        suite, evidence = temporary / "suite.json", temporary / "evidence.json"
        suite.write_text(json.dumps({"review_config": {"host": "codex", "model": "fixture-model", "reasoning_effort": "high"}}))
        evidence.write_text(json.dumps({"kind": "structural-fixture"}))
        self.environment.update({
            "AI_BOUNDARY_APPROVED": "1",
            "AI_BOUNDARY_APPROVAL_EVIDENCE": "owner-request:offline-release-fixture",
            "RELEASE_APPROVED_HEAD_SHA": self.head,
            "RELEASE_APPROVED_COMPARE_SHA": self.base,
            "FIXTURE_TRACE": str(self.root / ".artifacts/trace"),
            "RELEASE_EVAL_SUITE": str(suite), "RELEASE_EVAL_EVIDENCE": str(evidence),
            "RELEASE_EVAL_CONTRACT": "contract-v1:sha256:" + "a" * 64,
        })

    def command(self, *arguments):
        return subprocess.check_output(["git", *arguments], cwd=self.root,
                                       env=self.environment, text=True).strip()

    def write(self, relative, contents):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
        path.chmod(0o700)

    def execute(self, compare="baseline"):
        return release.create_release(self.root, "v0.6.0", compare, self.environment)

    def trace(self):
        path = self.root / ".artifacts/trace"
        return path.read_text().splitlines() if path.exists() else []

    def test_success_binds_commit_assets_and_real_gate_result(self):
        result = self.execute()
        self.assertEqual(result["source"]["head_sha"], self.head)
        self.assertEqual(self.trace().count("verify release"), 1)
        self.assertEqual(self.trace().count("evidence verify"), 1)
        self.assertEqual(self.trace().count("qualify structural fixture"), 1)
        self.assertEqual(self.trace().count("plan current binary"), 1)
        self.assertEqual(len([s for s in self.trace() if s.startswith("build ")]), 4)
        dist = self.root / "dist"
        manifest = json.loads((dist / "release-manifest.json").read_text())
        self.assertEqual(manifest["source"]["compare_sha"], self.base)
        for name, digest in manifest["assets"].items():
            self.assertEqual(hashlib.sha256((dist / name).read_bytes()).hexdigest(), digest)
        self.assertIn("# 'release source\\n'", (dist / "quality-review_linux_amd64").read_text())
        self.assertEqual(manifest["qualification"]["verification"]["claim"], "STRUCTURAL_FIXTURE_ONLY")
        self.assertEqual(self.command("status", "--porcelain"), "")

    def test_dirty_tree_stops_before_verification(self):
        (self.root / "source.txt").write_text("uncommitted\n")
        with self.assertRaisesRegex(release.ReleaseError, "clean working tree"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_clean_filter_cannot_hide_different_verification_bytes(self):
        path = "harness/repository_verification.py"
        self.command("config", "filter.fixture.clean", "sed '/^# hidden filtered change$/d'")
        (self.root / ".gitattributes").write_text(path + " filter=fixture\n")
        self.command("add", ".gitattributes")
        self.command("commit", "-qm", "add fixture clean filter")
        self.environment["RELEASE_APPROVED_HEAD_SHA"] = self.command("rev-parse", "HEAD")
        with (self.root / path).open("a") as stream:
            stream.write("# hidden filtered change\n")
        # Refresh the index through its clean filter: no committed bytes change.
        self.command("add", path)
        self.assertEqual(self.command("status", "--porcelain"), "")
        with self.assertRaisesRegex(release.ReleaseError, "source bytes differ from committed blob"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_executable_mode_is_checked_even_when_git_ignores_it(self):
        self.command("config", "core.filemode", "false")
        (self.root / "harness/repository_verification.py").chmod(0o600)
        self.assertEqual(self.command("status", "--porcelain"), "")
        with self.assertRaisesRegex(release.ReleaseError, "executable mode"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_archive_attributes_cannot_change_verified_source(self):
        for attribute in ("export-ignore", "export-subst"):
            with self.subTest(attribute=attribute):
                (self.root / ".gitattributes").write_text("source.txt " + attribute + "\n")
                (self.root / "source.txt").write_text("$Format:%H$\n")
                self.command("add", ".gitattributes", "source.txt")
                self.command("commit", "-qm", "archive attribute fixture")
                self.environment["RELEASE_APPROVED_HEAD_SHA"] = self.command("rev-parse", "HEAD")
                with self.assertRaisesRegex(release.ReleaseError, "committed source is unavailable|source bytes differ"):
                    self.execute()
                self.assertFalse((self.root / "dist").exists())
        self.assertFalse(any(line.startswith("build ") for line in self.trace()))

    def test_missing_evaluation_inputs_hold_before_verification(self):
        for name in ("RELEASE_EVAL_SUITE", "RELEASE_EVAL_EVIDENCE", "RELEASE_EVAL_CONTRACT"):
            value = self.environment.pop(name)
            with self.subTest(name=name), self.assertRaisesRegex(release.ReleaseError, "HOLD"):
                self.execute()
            self.environment[name] = value
        self.assertEqual(self.trace(), [])

    def test_evaluation_contract_must_come_from_current_binary(self):
        self.environment["FIXTURE_PLAN_CONTRACT"] = "contract-v1:sha256:" + "b" * 64
        with self.assertRaisesRegex(release.ReleaseError, "current built CLI"):
            self.execute()
        self.assertNotIn("qualify structural fixture", self.trace())
        self.assertFalse((self.root / "dist").exists())

    def test_evaluation_rejects_wrong_or_bare_suite_digest(self):
        bare = hashlib.sha256(Path(self.environment["RELEASE_EVAL_SUITE"]).read_bytes()).hexdigest()
        wrong = ("0" if bare[0] != "0" else "1") + bare[1:]
        for value in (bare, "sha256:" + wrong):
            with self.subTest(digest=value):
                self.environment["FIXTURE_SUITE_DIGEST"] = value
                with self.assertRaisesRegex(release.ReleaseError, "qualification identity changed"):
                    self.execute()
                self.assertFalse((self.root / "dist").exists())

    def test_evaluation_hold_and_block_preserve_previous_distribution(self):
        self.execute()
        before = (self.root / "dist/release-manifest.json").read_bytes()
        for status in ("HOLD", "BLOCK"):
            self.environment["FIXTURE_QUALIFICATION"] = status
            with self.subTest(status=status), self.assertRaisesRegex(release.ReleaseError, status):
                self.execute()
            self.assertEqual((self.root / "dist/release-manifest.json").read_bytes(), before)

    def test_limited_qualification_requires_explicit_limitation_evidence(self):
        self.environment["RELEASE_EVAL_SCOPE"] = "limited"
        for reference in ("", "  ", "user-request:one\nuser-request:two"):
            self.environment["RELEASE_EVAL_LIMITATION_EVIDENCE"] = reference
            with self.subTest(reference=reference), self.assertRaisesRegex(release.ReleaseError, "LIMITATION_EVIDENCE"):
                self.execute()
        self.environment["RELEASE_EVAL_SCOPE"] = "unknown"
        with self.assertRaisesRegex(release.ReleaseError, "RELEASE_EVAL_SCOPE"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_strict_default_rejects_limited_even_with_successful_verifier_exit(self):
        self.environment["FIXTURE_QUALIFICATION"] = "LIMITED"
        with self.assertRaisesRegex(release.ReleaseError, "strict qualification"):
            self.execute()
        self.assertFalse((self.root / "dist").exists())

    def test_limited_retains_scope_failures_and_reference_without_claiming_pass(self):
        self.environment.update(RELEASE_EVAL_SCOPE="limited", RELEASE_EVAL_LIMITATION_EVIDENCE="user-request:offline-fixture-only",
                                FIXTURE_QUALIFICATION="LIMITED")
        self.execute()
        manifest = json.loads((self.root / "dist/release-manifest.json").read_text())
        qualification = json.loads((self.root / "dist/release-qualification.json").read_text())
        self.assertEqual(manifest["qualification"], qualification)
        self.assertEqual(qualification["scope"], "limited")
        self.assertEqual(qualification["limitation_evidence"], "user-request:offline-fixture-only")
        self.assertEqual(qualification["verification"]["status"], "LIMITED")
        self.assertEqual(qualification["verification"]["incomplete_cases"], ["fixture-failed-case"])
        previous = (self.root / "dist/release-manifest.json").read_bytes()
        for status in ("HOLD", "BLOCK", "PASS"):
            self.environment["FIXTURE_QUALIFICATION"] = status
            with self.subTest(status=status), self.assertRaisesRegex(release.ReleaseError, "limited qualification"):
                self.execute()
            self.assertEqual((self.root / "dist/release-manifest.json").read_bytes(), previous)

    def test_untracked_file_stops_before_verification(self):
        (self.root / "untracked").write_text("new source\n")
        with self.assertRaisesRegex(release.ReleaseError, "clean working tree"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_hidden_index_flags_cannot_hide_uncommitted_verification_code(self):
        path = "harness/repository_verification.py"
        original = (self.root / path).read_text()
        for flag in ("assume-unchanged", "skip-worktree"):
            with self.subTest(flag=flag):
                self.command("update-index", "--" + flag, path)
                (self.root / path).write_text(original + "\n# hidden uncommitted change\n")
                self.assertEqual(self.command("status", "--porcelain"), "")
                with self.assertRaisesRegex(release.ReleaseError, "index flags"):
                    self.execute()
                (self.root / path).write_text(original)
                self.command("update-index", "--no-" + flag, path)
        self.assertEqual(self.trace(), [])

    def test_missing_or_stale_approval_stops_before_verification(self):
        for name in ("AI_BOUNDARY_APPROVED", "AI_BOUNDARY_APPROVAL_EVIDENCE",
                     "RELEASE_APPROVED_HEAD_SHA", "RELEASE_APPROVED_COMPARE_SHA"):
            with self.subTest(name=name):
                value = self.environment.pop(name)
                with self.assertRaises(release.ReleaseError):
                    self.execute()
                self.environment[name] = value
        self.environment["RELEASE_APPROVED_HEAD_SHA"] = self.base
        with self.assertRaisesRegex(release.ReleaseError, "covered by the owner approval"):
            self.execute()
        self.assertEqual(self.trace(), [])

    def test_explicit_baseline_and_version_are_required(self):
        with self.assertRaisesRegex(release.ReleaseError, "VERIFY_COMPARE_REF"):
            self.execute("")
        with self.assertRaisesRegex(release.ReleaseError, "explicit vX.Y.Z"):
            release.create_release(self.root, "dev", self.base, self.environment)
        self.assertEqual(self.trace(), [])

    def test_gate_failure_never_builds_or_publishes(self):
        for failure in ("verification", "evidence"):
            with self.subTest(failure=failure):
                self.environment["FIXTURE_FAILURE"] = failure
                with self.assertRaises(release.ReleaseError):
                    self.execute()
                self.assertFalse((self.root / "dist").exists())
        self.assertFalse(any(s.startswith("build ") for s in self.trace()))

    def test_head_change_stops_before_build(self):
        self.environment["FIXTURE_FAILURE"] = "head"
        with self.assertRaisesRegex(release.ReleaseError, "source or baseline changed"):
            self.execute()
        self.assertEqual(self.trace(), ["verify release"])

    def test_baseline_ref_change_stops_before_build(self):
        self.environment["FIXTURE_FAILURE"] = "baseline"
        with self.assertRaisesRegex(release.ReleaseError, "source or baseline changed"):
            self.execute()
        self.assertEqual(self.trace(), ["verify release"])

    def test_failed_build_preserves_previous_distribution(self):
        self.execute()
        previous = (self.root / "dist/release-manifest.json").read_bytes()
        self.environment["FIXTURE_FAILURE"] = "build"
        with self.assertRaises(release.ReleaseError):
            self.execute()
        self.assertEqual((self.root / "dist/release-manifest.json").read_bytes(), previous)

    def test_evidence_change_during_packaging_never_publishes(self):
        self.environment["FIXTURE_FAILURE"] = "evidence_changed"
        with self.assertRaisesRegex(release.ReleaseError, "evidence changed"):
            self.execute()
        self.assertFalse((self.root / "dist").exists())

    def test_rejects_symlink_and_unknown_existing_dist(self):
        target = Path(self.temporary.name) / "preserved"
        target.mkdir()
        dist = self.root / "dist"
        dist.symlink_to(target, target_is_directory=True)
        with self.assertRaisesRegex(release.ReleaseError, "symlink"):
            release.check_dist(dist)
        with self.assertRaises(release.ReleaseError):
            self.execute()
        dist.unlink()
        dist.mkdir()
        (dist / "user-data").write_text("preserve")
        with self.assertRaises(release.ReleaseError):
            self.execute()
        self.assertEqual((dist / "user-data").read_text(), "preserve")
        self.assertEqual(self.trace(), [])

    def test_release_profile_cannot_be_downgraded_by_environment(self):
        self.environment.update(VERIFY_PROFILE="change", VERIFY_EVIDENCE_MODE="candidate",
                                AI_BOUNDARY_APPROVAL_MODE="deferred")
        self.execute()
        self.assertEqual(self.trace().count("verify release"), 1)

    @unittest.skipUnless(os.environ.get("HARNESSCTL_BIN"), "set HARNESSCTL_BIN for the real pinned-engine integration")
    def test_real_harness_records_bound_release_evidence(self):
        project = SCRIPT.parent.parent
        self.write("scripts/harnessctl.sh", (project / "scripts/harnessctl.sh").read_text())
        self.write("harness/repository_verification.py", (project / "harness/repository_verification.py").read_text())
        self.write("harness/harness.lock", (project / "harness/harness.lock").read_text())
        profiles = json.loads((project / "harness/harness_profiles.json").read_text())
        profiles["gate_sets"]["release"] = ["change_scope", "release_context_before", "ai_boundaries",
                                           "project_release_check", "release_context_after"]
        profiles["custom_gates"].pop("suite_conformance")
        profiles["conditional_gates"].pop("suite_conformance")
        profiles["conditional_gates"]["repository_modules"]["always_profiles"] = []
        profiles["custom_gates"]["project_release_check"]["run"] = "harness/fixture_release.sh"
        self.write("harness/harness_profiles.json", json.dumps(profiles))
        self.write("harness/fixture_release.sh", "#!/bin/sh\nset -eu\nprintf 'fixture native gate\\n' >> .artifacts/trace\n")
        self.write(".ai-boundaries.yml", "allowed:\n  - README.md\napproval_required:\n  - source.txt\n  - harness/\n  - scripts/\n  - .ai-boundaries.yml\nforbidden:\n  - secrets/\n")
        self.command("add", "-A")
        self.command("commit", "-qm", "real engine fixture")
        self.head = self.command("rev-parse", "HEAD")
        self.environment["RELEASE_APPROVED_HEAD_SHA"] = self.head
        self.execute()
        self.assertEqual(self.trace().count("fixture native gate"), 1)
        evidence = json.loads((self.root / "dist/release-evidence.json").read_text())
        self.assertEqual(evidence["summary"]["engine"]["version"], "v0.8.0")
        self.assertTrue(evidence["summary"]["approval"]["approval_satisfied"])
        self.assertEqual(evidence["summary"]["git"]["head_sha"], self.head)


if __name__ == "__main__":
    unittest.main()
