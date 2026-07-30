from __future__ import annotations

import json
import pathlib
import sys
import unittest

PILOT_DIR = pathlib.Path(__file__).resolve().parent
SOURCE_ROOT = PILOT_DIR.parent
sys.path.insert(0, str(PILOT_DIR))

from native_review_admission import audit_inventory  # noqa: E402


class NativeReviewAdmissionTest(unittest.TestCase):
    def test_manifest_covers_the_frozen_inventory(self) -> None:
        report = audit_inventory(
            SOURCE_ROOT / "evals" / "cases.json",
            SOURCE_ROOT / "pilot" / "fixtures",
            SOURCE_ROOT / "evals" / "native-review-admission.json",
        )
        self.assertEqual(report["total_cases"], 60)
        self.assertEqual(report["qualified_cases"], 40)
        self.assertEqual(report["human_only_cases"], 20)
        self.assertEqual(report["blocked_cases"], {})
        self.assertEqual(report["qualified_kind_totals"], {"counterexample": 20, "positive": 20})
        self.assertTrue(report["scoring_contract_match"])
        self.assertTrue(report["hashes_match"])
        self.assertTrue(report["admission_ready"])

    def test_counterexamples_do_not_remove_or_change_exported_go_functions(self) -> None:
        report = audit_inventory(
            SOURCE_ROOT / "evals" / "cases.json",
            SOURCE_ROOT / "pilot" / "fixtures",
            SOURCE_ROOT / "evals" / "native-review-admission.json",
        )
        self.assertEqual(report["counterexample_api_issues"], {})

    def test_known_label_and_visibility_repairs_remain_aligned(self) -> None:
        cases = json.loads((SOURCE_ROOT / "evals" / "cases.json").read_text(encoding="utf-8"))["cases"]
        by_id = {case["id"]: case for case in cases}

        des_002_facts = " ".join(by_id["DES-002-positive"]["fixture"]["facts"])
        self.assertIn("20-million-row", des_002_facts)
        self.assertIn("five minutes", des_002_facts)
        self.assertNotIn("500-million-row", des_002_facts)

        des_005_facts = " ".join(by_id["DES-005-positive"]["fixture"]["facts"])
        self.assertIn("500000", des_005_facts)
        self.assertIn("two seconds", des_005_facts)
        request_contract = SOURCE_ROOT / "pilot" / "fixtures" / "des-005-positive"
        self.assertEqual(
            (request_contract / "base" / "request-contract.json").read_bytes(),
            (request_contract / "target" / "request-contract.json").read_bytes(),
        )

        des_003 = SOURCE_ROOT / "pilot" / "fixtures" / "des-003-counterexample"
        for side in ("base", "target"):
            source = (des_003 / side / "regions.go").read_text(encoding="utf-8")
            self.assertIn("len(regions) > maxSupportedRegions", source)

        sec_001_target = (
            SOURCE_ROOT / "pilot" / "fixtures" / "sec-001-counterexample" / "target" / "handler.go"
        ).read_text(encoding="utf-8")
        self.assertIn('identity.Role != "editor"', sec_001_target)
        self.assertIn("LookupForTenant(identity.TenantID, resourceID)", sec_001_target)

        cor_002_target = (
            SOURCE_ROOT / "pilot" / "fixtures" / "cor-002-counterexample" / "target" / "insert.go"
        ).read_text(encoding="utf-8")
        self.assertIn("errors.Is(err, ErrDuplicate)", cor_002_target)

        for case_id, contract in (
            ("cor-002-counterexample", "store-contract.json"),
            ("cor-004-counterexample", "outbox-contract.json"),
        ):
            fixture = SOURCE_ROOT / "pilot" / "fixtures" / case_id
            self.assertEqual(
                (fixture / "base" / contract).read_bytes(),
                (fixture / "target" / contract).read_bytes(),
            )

        cor_004_target = (
            SOURCE_ROOT / "pilot" / "fixtures" / "cor-004-counterexample" / "target" / "place.go"
        ).read_text(encoding="utf-8")
        self.assertIn("func ReconcileOutbox", cor_004_target)
        self.assertIn("delete(database.outbox, id)", cor_004_target)


if __name__ == "__main__":
    unittest.main()
