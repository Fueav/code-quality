import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


MINING_DIR = Path(__file__).resolve().parents[1]


def run(*args: str, cwd: Path, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=cwd,
        check=check,
        text=True,
        capture_output=True,
        env={**os.environ, "LC_ALL": "C"},
    )


def commit(repo: Path, subject: str) -> str:
    run("git", "add", ".", cwd=repo)
    run("git", "commit", "-m", subject, cwd=repo)
    return run("git", "rev-parse", "HEAD", cwd=repo).stdout.strip()


class MiningToolsTest(unittest.TestCase):
    def make_repo(self) -> Path:
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        run("git", "init", "-q", cwd=root)
        run("git", "config", "user.email", "mining-test@example.com", cwd=root)
        run("git", "config", "user.name", "Mining Test", cwd=root)
        return root

    def test_prefilter_keeps_only_fix_commits_touching_real_source(self) -> None:
        repo = self.make_repo()
        (repo / "src").mkdir()
        (repo / "src/app.py").write_text("value = 1\n")
        commit(repo, "initial implementation")

        (repo / "docs").mkdir()
        (repo / "docs/README.md").write_text("fixed wording\n")
        commit(repo, "fix docs typo")

        (repo / "tests").mkdir()
        (repo / "tests/test_app.py").write_text("def test_app(): pass\n")
        commit(repo, "bug regression test")

        (repo / "config").mkdir()
        (repo / "config/app.example.yaml").write_text("enabled: true\n")
        commit(repo, "fix config template")

        (repo / "src/app.py").write_text("value = 2\n")
        kept_first = commit(repo, "hotfix runtime behavior")

        (repo / "src/app.py").write_text("value = 3\n")
        kept_second = commit(repo, "fix another runtime bug")

        (repo / "src/app.py").write_text("value = 4\n")
        commit(repo, "refactor runtime behavior")

        result = run(str(MINING_DIR / "prefilter.sh"), str(repo), cwd=repo)
        self.assertEqual(
            result.stdout.strip().splitlines(),
            [
                f"KEEP {kept_second} :: fix another runtime bug",
                f"KEEP {kept_first} :: hotfix runtime behavior",
            ],
        )

    def test_aggregate_filters_and_deduplicates_by_introducing_commit(self) -> None:
        repo = self.make_repo()
        (repo / "src").mkdir()
        (repo / "src/app.py").write_text("value = 0\n")
        commit(repo, "initial implementation")

        (repo / "src/app.py").write_text("value = 1\n")
        kept_intro = commit(repo, "introduce subtle behavior")

        (repo / "src/large.py").write_text("".join(f"line_{n} = {n}\n" for n in range(3001)))
        large_intro = commit(repo, "introduce oversized change")

        results = repo / "results"
        results.mkdir()

        def write(name: str, **overrides: object) -> None:
            payload: dict[str, object] = {
                "schema_version": "1.0.0",
                "repo": repo.name,
                "fix_commit": "a" * 40,
                "fix_subject": "fix behavior",
                "scope": "IN_SCOPE",
                "rule_id": "COR-001",
                "defect_class": "wrong_code",
                "material": True,
                "static_detectable": True,
                "difficulty": 3,
                "introducing_commit": kept_intro,
                "evidence_chain": ["git blame src/app.py", "git show --stat"],
                "defect": "The introduced branch returns the wrong value for a valid request.",
                "defect_class_basis": "Existing behavior was implemented incorrectly.",
            }
            payload.update(overrides)
            (results / name).write_text(json.dumps(payload))

        write("keep-low.json", difficulty=2, fix_commit="1" * 40)
        write(
            "keep-high.json",
            difficulty=4,
            fix_commit="2" * 40,
            defect_class="missing_safeguard",
            defect="The change omitted the required authorization check.",
            defect_class_basis="A required security guard was absent.",
        )
        write("not-material.json", material=False, introducing_commit="b" * 40)
        write("not-static.json", static_detectable=False, introducing_commit="c" * 40)
        write("not-locatable.json", introducing_commit="")
        write("too-large.json", introducing_commit=large_intro)

        output = repo / "targets.json"
        stats = repo / "stats.json"
        run(
            "python3",
            str(MINING_DIR / "aggregate.py"),
            "--repo",
            str(repo),
            "--results-dir",
            str(results),
            "--output",
            str(output),
            "--stats-output",
            str(stats),
            cwd=repo,
        )

        targets = json.loads(output.read_text())
        self.assertEqual(len(targets), 1)
        self.assertEqual(targets[0]["introducing_commit"], kept_intro)
        self.assertEqual(targets[0]["defect_class"], "missing_safeguard")
        self.assertEqual(targets[0]["max_difficulty"], 4)
        self.assertEqual(len(targets[0]["defects"]), 2)

        summary = json.loads(stats.read_text())
        self.assertEqual(summary["result_files"], 6)
        self.assertEqual(summary["output_targets"], 1)
        self.assertEqual(summary["rejections"]["material_false"], 1)
        self.assertEqual(summary["rejections"]["static_detectable_false"], 1)
        self.assertEqual(summary["rejections"]["introducing_commit_unlocatable"], 1)
        self.assertEqual(summary["rejections"]["introducing_commit_too_large"], 1)
        self.assertEqual(summary["deduplicated_results"], 1)


if __name__ == "__main__":
    unittest.main()
