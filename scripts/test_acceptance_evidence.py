from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from acceptance_evidence import validate_acceptance_output_root, validate_acceptance_round, write_acceptance_round


class AcceptanceOutputRootTests(unittest.TestCase):
    def test_rejects_disposable_evidence_roots(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            for name in (".artifacts", ".tmp"):
                with self.subTest(name=name), self.assertRaises(RuntimeError) as caught:
                    validate_acceptance_output_root(root / name / "p7")
                self.assertIn("disposable artifacts directory", str(caught.exception))

    def test_accepts_a_persistent_evidence_root(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            accepted = validate_acceptance_output_root(root / "p7-acceptance" / "round-1")
            self.assertEqual(accepted, (root / "p7-acceptance" / "round-1").resolve())



class AcceptanceEvidenceTests(unittest.TestCase):
    def _git_workspace(self, root: Path) -> Path:
        workspace = root / "workspace"
        workspace.mkdir()
        subprocess.run(["git", "init"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
        subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=workspace, check=True)
        subprocess.run(["git", "config", "user.name", "Acceptance Test"], cwd=workspace, check=True)
        (workspace / "tracked.txt").write_text("before\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=workspace, check=True)
        subprocess.run(["git", "commit", "-m", "baseline"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
        (workspace / "tracked.txt").write_text("after\n", encoding="utf-8")
        return workspace

    def test_writes_complete_redacted_round_with_tracked_and_untracked_diff(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            workspace = root / "workspace"
            output = root / "output"
            workspace.mkdir()
            subprocess.run(["git", "init"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=workspace, check=True)
            subprocess.run(["git", "config", "user.name", "Acceptance Test"], cwd=workspace, check=True)
            (workspace / "tracked.txt").write_text("before\n", encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=workspace, check=True)
            subprocess.run(["git", "commit", "-m", "baseline"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
            (workspace / "tracked.txt").write_text("after\n", encoding="utf-8")
            (workspace / "deliverables").mkdir()
            (workspace / "deliverables" / "report.md").write_text("usable\n", encoding="utf-8")
            (workspace / "docs").mkdir()
            (workspace / "docs" / "upgrade.md").write_text("# Upgrade\n", encoding="utf-8")
            fake_key = "sk-" + "1234567890" + "abcdefghijkl"
            target = write_acceptance_round(
                output,
                domain="development",
                case_id="dev-complex",
                repetition=1,
                request_text=f"use {fake_key} safely",
                task_contract={"api_key": "must-not-leak", "goal": "deliver"},
                timeline=[{"authorization": "Bearer abcdefghijklmnopqrstuvwxyz"}],
                approvals=[],
                tool_summary={"tool_calls": 2, "tool_results": 2},
                verification={"passed": True, "independent": True},
                quality_score={"score": 100, "hard_gate": True, "source": "deterministic_external_evaluator"},
                report={
                    "schema_version": 1,
                    "quality_contract_version": 2,
                    "passed": True,
                    "provider": {"model": "test-model"},
                    "run_id": "run-1",
                    "started_at": "2026-08-18T00:00:00+00:00",
                    "finished_at": "2026-08-18T00:00:01+00:00",
                    "duration_ms": 1000,
                    "token_usage": {"prompt_tokens": 10, "output_tokens": 5, "total_tokens": 15},
                    "tool_metrics": {"calls": 2, "results": 2, "duration_ms": 12, "duration_samples": 1, "duplicate_tool_calls": 0, "duplicate_full_reads": 0, "signature_samples": 2},
                    "failure_recovery": {"done": True},
                },
                workspace=workspace,
                deliverable_paths=("deliverables/report.md", "docs/upgrade.md"),
            )
            expected = {
                "request.txt", "task-contract.json", "timeline.json", "approvals.json",
                "tool-summary.json", "diff.patch", "verification.json", "quality-score.json",
                "report.json", "deliverables/report.md", "deliverables/docs/upgrade.md",
            }
            actual = {path.relative_to(target).as_posix() for path in target.rglob("*") if path.is_file()}
            self.assertEqual(expected, actual)
            combined = "\n".join(path.read_text(encoding="utf-8", errors="ignore") for path in target.rglob("*") if path.is_file())
            self.assertNotIn("must-not-leak", combined)
            self.assertNotIn(fake_key[:9], combined)
            self.assertIn("tracked.txt", (target / "diff.patch").read_text(encoding="utf-8"))
            self.assertIn("deliverables/report.md", (target / "diff.patch").read_text(encoding="utf-8"))
            self.assertNotIn(b"\r\n", (target / "diff.patch").read_bytes())
            report = json.loads((target / "report.json").read_text(encoding="utf-8"))
            self.assertEqual({item["path"] for item in report["deliverables"]}, {"report.md", "docs/upgrade.md"})
            validation = validate_acceptance_round(target, require_success=True)
            self.assertTrue(validation["valid"])
            self.assertEqual(validation["deliverables"], 2)

            report["quality_contract_version"] = 1
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "obsolete quality contract"):
                validate_acceptance_round(target, require_success=True)
            report["quality_contract_version"] = 2
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")

            report["token_usage"] = {"prompt_tokens": 0, "output_tokens": 0, "total_tokens": 0}
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "no real Provider token evidence"):
                validate_acceptance_round(target, require_success=True)
            report["token_usage"] = {"prompt_tokens": 10, "output_tokens": 5, "total_tokens": 15}
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")

            quality = json.loads((target / "quality-score.json").read_text(encoding="utf-8"))
            quality["source"] = "model_self_evaluation"
            (target / "quality-score.json").write_text(json.dumps(quality), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "independent quality score"):
                validate_acceptance_round(target, require_success=True)
            quality["source"] = "deterministic_external_evaluator"
            (target / "quality-score.json").write_text(json.dumps(quality), encoding="utf-8")

            report["tool_metrics"]["duration_samples"] = 0
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "tool duration evidence"):
                validate_acceptance_round(target, require_success=True)
            report["tool_metrics"]["duration_samples"] = 1
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")

            report["tool_metrics"]["duplicate_tool_calls"] = 1
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "duplicate tool calls"):
                validate_acceptance_round(target, require_success=True)
            report["tool_metrics"]["duplicate_tool_calls"] = 0
            report["tool_metrics"]["duplicate_full_reads"] = 1
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "duplicate full-file reads"):
                validate_acceptance_round(target, require_success=True)
            report["tool_metrics"]["duplicate_full_reads"] = 0
            (target / "report.json").write_text(json.dumps(report), encoding="utf-8")

            tool_summary = json.loads((target / "tool-summary.json").read_text(encoding="utf-8"))
            tool_summary["tool_results"] = 99
            (target / "tool-summary.json").write_text(json.dumps(tool_summary), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "metrics disagree for results"):
                validate_acceptance_round(target, require_success=True)
            tool_summary["tool_results"] = 2
            (target / "tool-summary.json").write_text(json.dumps(tool_summary), encoding="utf-8")

            (target / "deliverables" / "undeclared.txt").write_text("not declared\n", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "manifest does not match persisted files"):
                validate_acceptance_round(target, require_success=True)
            (target / "deliverables" / "undeclared.txt").unlink()

            (target / "deliverables" / "docs" / "upgrade.md").write_text("tampered\n", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "hash mismatch"):
                validate_acceptance_round(target, require_success=True)

    def test_rejects_disposable_or_workspace_local_evidence_root(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            workspace = root / "workspace"
            workspace.mkdir()
            subprocess.run(["git", "init"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
            subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=workspace, check=True)
            subprocess.run(["git", "config", "user.name", "Acceptance Test"], cwd=workspace, check=True)
            (workspace / "input.txt").write_text("baseline\n", encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=workspace, check=True)
            subprocess.run(["git", "commit", "-m", "baseline"], cwd=workspace, check=True, stdout=subprocess.DEVNULL)
            common = {
                "domain": "development",
                "case_id": "dev-complex",
                "repetition": 1,
                "request_text": "test",
                "task_contract": {},
                "timeline": [],
                "approvals": [],
                "tool_summary": {},
                "verification": {},
                "quality_score": {},
                "report": {},
                "workspace": workspace,
            }
            with self.assertRaisesRegex(RuntimeError, "outside the disposable"):
                write_acceptance_round(workspace / "evidence", **common)
            with self.assertRaisesRegex(RuntimeError, "disposable artifacts"):
                write_acceptance_round(root / ".artifacts" / "evidence", **common)

    def test_round_publish_is_atomic_and_never_overwrites_existing_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            workspace = self._git_workspace(root)
            output = root / "output"
            common = {
                "domain": "development",
                "case_id": "dev-complex",
                "repetition": 1,
                "request_text": "test",
                "task_contract": {},
                "timeline": [],
                "approvals": [],
                "tool_summary": {},
                "verification": {},
                "quality_score": {},
                "report": {"schema_version": 1, "passed": False},
                "workspace": workspace,
            }
            target = write_acceptance_round(output, **common)
            original = (target / "report.json").read_bytes()
            with self.assertRaisesRegex(RuntimeError, "cannot be overwritten"):
                write_acceptance_round(output, **common)
            self.assertEqual((target / "report.json").read_bytes(), original)

            failed_output = root / "failed-output"
            with self.assertRaisesRegex(RuntimeError, "escapes the workspace"):
                write_acceptance_round(
                    failed_output,
                    **common,
                    deliverable_paths=("../outside.txt",),
                )
            case_root = failed_output / "交付证据" / "开发编码" / "dev-complex"
            self.assertFalse((case_root / "第1轮").exists())
            self.assertEqual(list(case_root.glob(".*-writing-*")), [])


if __name__ == "__main__":
    unittest.main()
