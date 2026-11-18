from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from scripts.acceptance_evidence import DOMAIN_NAMES
from scripts.p7_complex_cases import cases
from scripts.validate_p7_matrix import validate_p7_matrix


class P7MatrixValidationTests(unittest.TestCase):
    def test_rejects_formal_round_with_skipped_provider_probe(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            catalog = cases()
            for case in catalog:
                round_dir = root / "交付证据" / DOMAIN_NAMES[case.domain] / case.id / "第1轮"
                round_dir.mkdir(parents=True)
                report = {
                    "schema_version": 1,
                    "passed": True,
                    "case_id": case.id,
                    "domain": case.domain,
                    "repetition": 1,
                    "provider": {"valid": True, "configured": True, "complete": True, "skipped": True},
                }
                (round_dir / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with patch("scripts.validate_p7_matrix.validate_acceptance_round", return_value={"quality_score": 100}):
                with self.assertRaisesRegex(RuntimeError, "lacks complete Provider evidence"):
                    validate_p7_matrix(root, repetitions=1)

    def test_rejects_duplicate_case_ids_or_domains_before_reading_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            catalog = list(cases())
            catalog[-1] = catalog[0]
            with patch("scripts.validate_p7_matrix.cases", return_value=catalog):
                with self.assertRaisesRegex(RuntimeError, "six unique cases"):
                    validate_p7_matrix(root)

    def test_requires_user_acceptance_evidence_root(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            with self.assertRaisesRegex(RuntimeError, "root is missing"):
                validate_p7_matrix(Path(raw))

    def test_rejects_incomplete_matrix_before_counting_rounds(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            (root / "交付证据").mkdir()
            with self.assertRaisesRegex(RuntimeError, "missing evidence case directory"):
                validate_p7_matrix(root)

    def test_reports_failed_existing_round_before_later_missing_round(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            case = next(item for item in cases() if item.domain == "cross-domain")
            for repetition in (1, 2):
                round_dir = root / "交付证据" / DOMAIN_NAMES[case.domain] / case.id / f"第{repetition}轮"
                round_dir.mkdir(parents=True)
                report = {
                    "schema_version": 1,
                    "passed": repetition == 1,
                    "case_id": case.id,
                    "domain": case.domain,
                    "repetition": repetition,
                    "run_id": f"run-{repetition}",
                    "provider": {"valid": True, "configured": True, "complete": True, "skipped": False},
                }
                (round_dir / "report.json").write_text(json.dumps(report), encoding="utf-8")

            def validate(target: Path, *, require_success: bool = False):
                self.assertTrue(require_success)
                if target.name == "第2轮":
                    raise RuntimeError("acceptance round is not successful")
                return {"quality_score": 100}

            with patch("scripts.validate_p7_matrix.validate_acceptance_round", side_effect=validate):
                with self.assertRaisesRegex(RuntimeError, "acceptance round is not successful"):
                    validate_p7_matrix(root, repetitions=3)


if __name__ == "__main__":
    unittest.main()
