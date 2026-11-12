from __future__ import annotations

import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from p7_case_evaluator import evaluate_p7_case
from p7_complex_cases import cases
from p7_office_mcp_acceptance import run_case as run_office_reference
from p7_office_mcp_acceptance import remove_file_with_retry
from p7_reference_solutions import apply_reference_solution


class P7ReferenceSolutionTests(unittest.TestCase):
    def test_remove_file_with_retry_is_idempotent(self) -> None:
        with tempfile.TemporaryDirectory(prefix="p7-office-cleanup-") as raw:
            target = Path(raw) / "missing-binary.exe"
            self.assertTrue(remove_file_with_retry(target, attempts=2, delay_seconds=0))

    def test_reference_solutions_pass_all_code_and_research_variants(self) -> None:
        for case in cases():
            if case.domain == "documents":
                continue
            for index, mutator in enumerate(case.variant_mutators):
                with self.subTest(case=case.id, variant=index):
                    with tempfile.TemporaryDirectory(prefix="p7-reference-") as raw:
                        root = Path(raw)
                        case.builder(root)
                        mutator(root)
                        apply_reference_solution(case, root)
                        result = evaluate_p7_case(
                            case,
                            root,
                            evidence={"tool_results": 4, "artifacts": 6},
                            run={"budget_llm_calls": 8},
                            include_race=False,
                        )
                        self.assertTrue(result["passed"], result)
                        self.assertGreaterEqual(result["quality_score"]["score"], 80, result)

    def test_office_reference_uses_real_mcp_and_independent_evaluator(self) -> None:
        if sys.platform != "win32" and not (shutil.which("libreoffice") or shutil.which("soffice")):
            self.skipTest("actual Office/LibreOffice openability validation is unavailable")
        with tempfile.TemporaryDirectory(prefix="p7-office-reference-") as raw:
            result = run_office_reference(Path(raw))
        self.assertTrue(result["passed"], result)
        self.assertTrue(result["source_hashes_unchanged"], result)
        self.assertGreaterEqual(len(result["operations"]), 10, result)


if __name__ == "__main__":
    unittest.main()
