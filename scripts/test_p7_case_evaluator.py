from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))

from p7_case_evaluator import _delivery_integrity_issues, evaluate_p7_case, evaluate_p7_quality, find_linux_race_gate, is_unavailable_race_result, run_command
from p7_complex_cases import P7ComplexCase


class DeliveryIntegrityTests(unittest.TestCase):
    def _case(self) -> P7ComplexCase:
        def builder(root: Path) -> None:
            (root / "pkg").mkdir(parents=True, exist_ok=True)
            (root / "pkg/impl.go").write_text("package pkg\n\nvar Value = 1\n", encoding="utf-8")
            (root / "pkg/public_test.go").write_text("package pkg\n\nimport \"testing\"\n\nfunc TestPublic(t *testing.T) {}\n", encoding="utf-8")

        return P7ComplexCase(
            id="integrity", domain="development", title="integrity", workflow="", prompt="",
            builder=builder, required_paths=("pkg/impl.go",),
        )

    def test_allows_extending_or_formatting_public_tests(self) -> None:
        case = self._case()
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            case.builder(workspace)
            (workspace / "pkg/impl.go").write_text("package pkg\n\nvar Value = 2\n", encoding="utf-8")
            (workspace / "pkg/public_test.go").write_text(
                "package pkg\n\nimport \"testing\"\n\nfunc TestPublic(t *testing.T) {\n\tt.Log(\"formatted\")\n}\n\nfunc TestAdded(t *testing.T) {}\n",
                encoding="utf-8",
            )
            issues = _delivery_integrity_issues(case, workspace)
        self.assertEqual(issues, [])

    def test_detects_removed_public_test_case(self) -> None:
        case = self._case()
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            case.builder(workspace)
            (workspace / "pkg/impl.go").write_text("package pkg\n\nvar Value = 2\n", encoding="utf-8")
            (workspace / "pkg/public_test.go").write_text("package pkg\n", encoding="utf-8")
            issues = _delivery_integrity_issues(case, workspace)
        self.assertTrue(any("public test cases removed" in issue for issue in issues), issues)

    def test_detects_delivery_that_changed_nothing(self) -> None:
        case = self._case()
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            case.builder(workspace)
            issues = _delivery_integrity_issues(case, workspace)
        self.assertTrue(any("no required source path was modified" in issue for issue in issues), issues)

    def test_accepts_a_clean_real_change(self) -> None:
        case = self._case()
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            case.builder(workspace)
            (workspace / "pkg/impl.go").write_text("package pkg\n\nvar Value = 3\n", encoding="utf-8")
            issues = _delivery_integrity_issues(case, workspace)
        self.assertEqual(issues, [])


class FormatGateRegressionTests(unittest.TestCase):
    def test_gofmt_zero_exit_with_listed_files_is_failure(self) -> None:
        with tempfile.TemporaryDirectory() as raw, patch("p7_case_evaluator.subprocess.run", return_value=subprocess.CompletedProcess([], 0, "main.go\n")):
            self.assertFalse(run_command(("gofmt", "-l", "."), Path(raw))["passed"])

    def test_format_gate_excludes_evaluator_owned_hidden_tests(self) -> None:
        case = P7ComplexCase(
            id="format-gate", domain="development", title="format gate", workflow="", prompt="",
            builder=lambda root: None, hidden_files={"hidden_test.go": "package main"},
            verify_commands=(("gofmt", "-l", "."), ("go", "test", "./...")),
        )
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)

            def check(command, workspace):
                self.assertEqual((workspace / "hidden_test.go").exists(), command[0] != "gofmt")
                return {"command": list(command), "passed": True, "output": ""}

            with patch("p7_case_evaluator.run_command", side_effect=check):
                self.assertTrue(evaluate_p7_case(case, root, include_race=False)["passed"])


class P7CaseEvaluatorTests(unittest.TestCase):
    def test_race_infrastructure_failure_requires_real_linux_gate(self) -> None:
        case = P7ComplexCase(
            id="race-case",
            domain="development",
            title="race",
            workflow="",
            prompt="",
            builder=lambda root: None,
            verify_commands=(("go", "test", "./..."),),
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            gate = workspace / ".github" / "workflows" / "race.yml"
            gate.parent.mkdir(parents=True)
            gate.write_text("jobs:\n  race:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -race ./...\n", encoding="utf-8")
            with patch("p7_case_evaluator.run_command", side_effect=[
                {"passed": True, "command": ["go", "test", "./..."], "output": "ok"},
                {"passed": False, "command": ["go", "test", "-race", "./..."], "output": "go: -race requires cgo"},
            ]):
                result = evaluate_p7_case(case, workspace)
        self.assertTrue(result["passed"])
        race = result["verification"][-1]
        self.assertTrue(race["deferred"])
        self.assertFalse(race["passed"], "a race run that never started must not be reported as passed")
        self.assertFalse(race["blocking"])
        self.assertEqual(race["status"], "deferred")
        self.assertEqual(race["gate"], ".github/workflows/race.yml")
        self.assertTrue(result["quality_score"]["hard_gate"])

    def test_unavailable_race_without_linux_gate_blocks_delivery(self) -> None:
        case = P7ComplexCase(
            id="race-nogate",
            domain="development",
            title="race",
            workflow="",
            prompt="",
            builder=lambda root: None,
            verify_commands=(("go", "test", "./..."),),
        )
        with tempfile.TemporaryDirectory() as raw, patch("p7_case_evaluator.run_command", side_effect=[
            {"passed": True, "command": ["go", "test", "./..."], "output": "ok"},
            {"passed": False, "command": ["go", "test", "-race", "./..."], "output": "go: -race requires cgo"},
        ]):
            result = evaluate_p7_case(case, Path(raw))
        self.assertFalse(result["passed"])
        self.assertFalse(result["verification"][-1].get("deferred"))

    def test_race_product_failure_is_not_deferred(self) -> None:
        self.assertFalse(is_unavailable_race_result({"output": "WARNING: DATA RACE"}))
        with tempfile.TemporaryDirectory() as raw:
            self.assertEqual(find_linux_race_gate(Path(raw)), "")

    def test_evaluator_is_independent_and_reports_missing_deliverables(self) -> None:
        case = P7ComplexCase(
            id="unit-case",
            domain="development",
            title="unit",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "hidden"},
            verify_commands=(("go", "test", "./..."),),
            required_deliverables=("deliverables/report.md",),
        )
        with tempfile.TemporaryDirectory() as raw, patch(
            "p7_case_evaluator.run_command",
            return_value={"passed": True, "command": ["go", "test"]},
        ):
            workspace = Path(raw)
            result = evaluate_p7_case(case, workspace)
            self.assertTrue((workspace / "hidden.txt").is_file())
        self.assertFalse(result["passed"])
        self.assertEqual(result["missing_deliverables"], ["deliverables/report.md"])
        self.assertTrue(result["independent"])

    def test_evaluator_cache_does_not_land_in_the_workspace(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw) / "workspace"
            workspace.mkdir()
            (workspace / "go.mod").write_text("module example\n\ngo 1.22\n", encoding="utf-8")
            (workspace / "main.go").write_text("package main\nfunc main() {}\n", encoding="utf-8")
            poisoned = str((workspace / ".cache").resolve())
            env = {key: value for key, value in os.environ.items() if key not in {"GOTMPDIR"}}
            env["GOCACHE"] = poisoned
            with patch.dict(os.environ, env, clear=True):
                result = run_command(("go", "env", "GOCACHE"), workspace)
            self.assertTrue(result["passed"], result.get("output"))
            cache = result["output"].strip().splitlines()[-1]
            self.assertTrue(cache)
            self.assertNotIn(str(workspace.resolve()), cache)
            self.assertNotEqual(Path(cache).resolve(), Path(poisoned))
            self.assertFalse((workspace / ".cache").exists())
            self.assertFalse(any("go-build" in path.as_posix() for path in workspace.rglob("*")))

    def test_quality_score_does_not_call_passing_without_runtime_evidence(self) -> None:
        case = P7ComplexCase(
            id="quality-case",
            domain="research",
            title="quality",
            workflow="",
            prompt="",
            builder=lambda root: None,
            required_deliverables=("deliverables/report.md",),
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            output = workspace / "deliverables/report.md"
            output.parent.mkdir()
            output.write_text("# Verified report\n", encoding="utf-8")
            result = evaluate_p7_quality(
                case,
                workspace,
                {
                    "passed": True,
                    "verification": [{"passed": True}],
                    "missing_deliverables": [],
                },
                evidence={},
                run={},
            )
        self.assertEqual(result["score"], 90)
        self.assertEqual(result["dimensions"]["efficiency"], 0)
        self.assertTrue(any("tool result" in note for note in result["notes"]))

    def test_quality_uses_the_declared_case_budget(self) -> None:
        case = P7ComplexCase(
            id="budget-case",
            domain="research",
            title="budget",
            workflow="",
            prompt="",
            builder=lambda root: None,
            required_deliverables=("deliverables/report.md",),
            llm_call_budget=60,
            artifact_budget=60,
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            output = workspace / "deliverables/report.md"
            output.parent.mkdir()
            output.write_text("# Verified report\n", encoding="utf-8")
            result = evaluate_p7_quality(
                case,
                workspace,
                {"passed": True, "verification": [{"passed": True}], "missing_deliverables": []},
                evidence={"tool_results": 4, "artifacts": 52},
                run={"budget_llm_calls": 56},
            )
        self.assertEqual(result["dimensions"]["efficiency"], 10)
        self.assertFalse(any("efficiency budget exceeded" in note for note in result["notes"]))

    def test_quality_default_budget_still_flags_a_larger_run(self) -> None:
        case = P7ComplexCase(
            id="default-budget",
            domain="research",
            title="budget",
            workflow="",
            prompt="",
            builder=lambda root: None,
            required_deliverables=("deliverables/report.md",),
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            output = workspace / "deliverables/report.md"
            output.parent.mkdir()
            output.write_text("# Verified report\n", encoding="utf-8")
            result = evaluate_p7_quality(
                case,
                workspace,
                {"passed": True, "verification": [{"passed": True}], "missing_deliverables": []},
                evidence={"tool_results": 4, "artifacts": 31},
                run={"budget_llm_calls": 45},
            )
        self.assertEqual(result["dimensions"]["efficiency"], 5)
        self.assertTrue(any("llm_calls=45/40" in note for note in result["notes"]), result["notes"])

    def test_quality_hard_gate_requires_engineering_completeness(self) -> None:
        case = P7ComplexCase(
            id="quality-completeness",
            domain="documents",
            title="quality",
            workflow="",
            prompt="",
            builder=lambda root: None,
            required_deliverables=("deliverables/report.pdf",),
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            output = workspace / "deliverables/report.pdf"
            output.parent.mkdir()
            output.write_bytes(b"%PDF-1.4\n/Type /Page\n%%EOF\n")
            result = evaluate_p7_quality(
                case,
                workspace,
                {"passed": True, "verification": [], "missing_deliverables": []},
                evidence={"tool_results": 1},
                run={},
            )
        self.assertFalse(result["hard_gate"])
        self.assertEqual(result["dimensions"]["engineering_completeness"], 0)

    def test_cross_domain_report_must_match_successful_static_check(self) -> None:
        case = P7ComplexCase(
            id="cross-report",
            domain="cross-domain",
            title="cross",
            workflow="",
            prompt="",
            builder=lambda root: None,
            verify_commands=(("go", "test", "./..."), ("go", "vet", "./...")),
            required_deliverables=("docs/unified-delivery.md",),
        )
        with tempfile.TemporaryDirectory() as raw, patch(
            "p7_case_evaluator.run_command",
            return_value={"passed": True, "command": ["go", "vet", "./..."], "output": ""},
        ):
            workspace = Path(raw)
            report = workspace / "docs/unified-delivery.md"
            report.parent.mkdir()
            report.write_text("# 报告\n\n静态检查：未获得成功证据。\n", encoding="utf-8")
            result = evaluate_p7_case(case, workspace, include_race=False)
        self.assertFalse(result["passed"])
        self.assertTrue(any(item.get("command") == ["delivery-report-consistency"] for item in result["verification"]))


if __name__ == "__main__":
    unittest.main()
