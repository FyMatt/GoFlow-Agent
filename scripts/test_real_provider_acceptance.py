from __future__ import annotations

import unittest
import os
import json
import shutil
import tempfile
from pathlib import Path
from unittest.mock import patch
from types import SimpleNamespace

from scripts import real_provider_acceptance as acceptance

from scripts.real_provider_acceptance import (
    collect_run_evidence,
    completed_quality_gate,
    create_agent_fixture,
    create_workflow_fixture,
    durable_change_paths,
    event_pair_duration_ms,
    run_exceeded_call_ceiling,
    format_check,
    parse_event_at_ms,
    provider_probe_complete,
    sanitize_provider_message,
    transient_tool_approval_conflict,
)


class RealProviderAcceptanceTests(unittest.TestCase):
    def test_timeout_keeps_full_snapshot_out_of_the_error_message(self) -> None:
        snapshot = {"status": "running", "next_stage": "final-report", "events": [{"content": "x" * 20000}]}
        ticks = iter([0.0, 0.0, 2.0])
        with patch.object(acceptance.time, "monotonic", side_effect=lambda: next(ticks, 2.0)), \
             patch.object(acceptance.time, "sleep"), \
             patch.object(acceptance, "request_json", return_value=(200, snapshot)):
            with self.assertRaises(acceptance.RunDriveTimeout) as raised:
                acceptance.drive_run("http://127.0.0.1", "workflow", "run-1", 1)
        self.assertLess(len(str(raised.exception)), 300)
        self.assertIn("final-report", str(raised.exception))
        self.assertEqual(raised.exception.last_snapshot, snapshot)

    def test_quality_score_cannot_hide_an_incomplete_run(self) -> None:
        for status in ["blocked", "failed", "cancelled", "paused_need_more_budget", "running"]:
            with self.subTest(status=status):
                self.assertFalse(completed_quality_gate({"status": status}, 100))
        self.assertTrue(completed_quality_gate({"status": "completed"}, 85))
        self.assertTrue(completed_quality_gate({"status": "succeeded"}, 100))
        self.assertFalse(completed_quality_gate({"status": "completed"}, 84))

    def test_failed_postcheck_preserves_the_completed_model_run(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            secret = root / "secrets/providers/primary/api_key"
            secret.parent.mkdir(parents=True)
            secret.write_text("local-fixture", encoding="utf-8")
            options = SimpleNamespace(timeout=1, keep_workspaces=True, skip_provider_probe=True, max_auto_retries=0)
            with patch.object(acceptance, "ROOT", root), patch.object(acceptance, "parse_args", return_value=options), \
                 patch.object(acceptance, "run_checked", return_value={"returncode": 0}), \
                 patch.object(acceptance, "create_agent_fixture"), patch.object(acceptance, "create_workflow_fixture"), \
                 patch.object(acceptance, "start_server", return_value=(None, None)), patch.object(acceptance, "stop_server"), \
                 patch.object(acceptance, "request_json", return_value=(202, {"run_id": "real-run"})), \
                 patch.object(acceptance, "drive_run", return_value=({"status": "completed"}, [], "real-run")), \
                 patch.object(acceptance, "collect_run_evidence", return_value={"diffs": 1, "tool_results": 3}), \
                 patch.object(acceptance, "add_agent_hidden_tests"), \
                 patch.object(acceptance, "validate_workspace", side_effect=RuntimeError("postcheck failed")):
                with self.assertRaisesRegex(RuntimeError, "postcheck failed"):
                    acceptance.main()
            report = json.loads(next(root.glob(".artifacts/real-provider-acceptance/*/report.json")).read_text(encoding="utf-8"))
            self.assertFalse(report["passed"])
            self.assertEqual(report["agent"]["run_id"], "real-run")
            self.assertEqual(report["agent"]["status"], "completed")
            self.assertEqual(report["agent"]["evidence"]["tool_results"], 3)
            self.assertIn("postcheck failed", report["notes"])

    @unittest.skipUnless(shutil.which("gofmt") and shutil.which("git"), "Go formatter and git are required")
    def test_fixture_baselines_are_formatted_before_the_model_runs(self) -> None:
        for create in [create_agent_fixture, create_workflow_fixture]:
            with self.subTest(fixture=create.__name__), tempfile.TemporaryDirectory() as temporary:
                workspace = Path(temporary)
                create(workspace, os.environ.copy())
                self.assertEqual(format_check(workspace, os.environ.copy())["output"].strip(), "")

    def test_code_probe_does_not_require_vision(self) -> None:
        self.assertTrue(provider_probe_complete({"valid": True, "model": "configured-model"}, {"api_key_set": True}, {
            "chat": True, "streaming": True, "tool_calling": True, "structured_output": True, "vision": False,
        }))

    def test_provider_diagnostics_remove_full_and_masked_key_material(self) -> None:
        messages = (
            'provider returned 401 Unauthorized: {"error":{"message":"Your api key: ****a613 is invalid"}}',
            "Authorization: Bearer abcdefghijklmnop",
            "sk-" + "1234567890abcdefghijklmnop",
        )
        for message in messages:
            with self.subTest(message=message):
                safe = sanitize_provider_message(message)
                self.assertNotIn("a613", safe)
                self.assertNotIn("abcdefghijklmnop", safe)
                self.assertNotIn("sk-1234567890", safe)
    def test_complete_probe_is_provider_and_model_agnostic(self) -> None:
        capabilities = {
            "chat": True,
            "streaming": True,
            "tool_calling": True,
            "structured_output": True,
            "vision": True,
        }
        self.assertTrue(
            provider_probe_complete(
                {"valid": True, "model": "gpt-5.6-sol"},
                {"api_key_set": True},
                capabilities,
            )
        )

    def test_incomplete_probe_rejects_missing_runtime_evidence(self) -> None:
        complete = {
            "chat": True,
            "streaming": True,
            "tool_calling": True,
            "structured_output": True,
        }
        cases = (
            ({"valid": False, "model": "model"}, {"api_key_set": True}, complete),
            ({"valid": True, "model": "model"}, {"api_key_set": False}, complete),
            ({"valid": True, "model": ""}, {"api_key_set": True}, complete),
            ({"valid": True, "model": "model"}, {"api_key_set": True}, {**complete, "tool_calling": False}),
        )
        for result, provider, capabilities in cases:
            with self.subTest(result=result, provider=provider, capabilities=capabilities):
                self.assertFalse(provider_probe_complete(result, provider, capabilities))

    def test_durable_change_paths_uses_structured_workspace_tool_evidence(self) -> None:
        change = {
            "path": "apply_patch",
            "summary": '{"path":"C:\\\\task-staging\\\\run-1\\\\workspace\\\\portcfg\\\\port.go"}',
        }
        self.assertEqual(durable_change_paths(change), {"portcfg/port.go"})

    def test_durable_change_paths_rejects_non_workspace_and_escape_paths(self) -> None:
        self.assertEqual(durable_change_paths({"path": "apply_patch"}), set())
        self.assertEqual(durable_change_paths({"path": "../outside.go"}), set())

    def test_transient_tool_approval_conflict_is_strictly_scoped(self) -> None:
        error = "ordinary agent tool approval context is not available yet or is missing from the persisted agent run snapshot"
        self.assertTrue(transient_tool_approval_conflict("approve-tools", error))
        self.assertFalse(transient_tool_approval_conflict("apply_staged_changes", error))
        self.assertFalse(transient_tool_approval_conflict("approve-tools", "permission denied"))

    def test_collect_run_evidence_includes_delivery_and_observed_tool_duration(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:a"},
                    {"type": "tool_result", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:a", "is_error": False},
                    {"type": "token_usage", "prompt_tokens": 12, "output_tokens": 4},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, [{"stage": "verify", "result": {"tool_results": [{"tool_name": "run_command"}]}}]
            if path.endswith("/diffs"):
                return 200, [{"path": "workspace/a.go"}]
            if path.startswith("/api/workflow-runs/run-1/artifacts?"):
                return 200, [{
                    "kind": "tool_result",
                    "tool_name": "run_command",
                    "title": "go test",
                    "metadata": {"duration_ms": "27"},
                }]
            if path.endswith("/delivery"):
                return 200, {
                    "schema_version": "goflow.delivery.v1",
                    "conclusion": "passed",
                    "metrics": {"duration_ms": 100, "llm_calls": 2},
                    "quality": {"score": 100},
                    "hard_gates": [{"name": "terminal", "passed": True}],
                }
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-1")
        self.assertEqual(evidence["prompt_tokens"], 12)
        self.assertEqual(evidence["output_tokens"], 4)
        self.assertEqual(evidence["tool_duration_ms"], 27)
        self.assertEqual(evidence["tool_duration_samples"], 1)
        self.assertEqual(evidence["duplicate_tool_calls"], 0)
        self.assertEqual(evidence["duplicate_full_reads"], 0)
        self.assertEqual(evidence["tool_signature_samples"], 1)
        self.assertEqual(evidence["delivery_schema_version"], "goflow.delivery.v1")
        self.assertEqual(evidence["delivery_quality"]["score"], 100)

    def test_event_pair_duration_uses_call_and_result_timestamps(self) -> None:
        self.assertEqual(parse_event_at_ms("2026-08-22T23:08:06.7786186Z"), parse_event_at_ms("2026-08-22T23:08:06.778618+00:00"))
        durations = event_pair_duration_ms(
            [
                {"type": "tool_call", "tool_call_id": "c1", "at": "2026-08-22T23:08:06.7786186Z"},
                {"type": "tool_result", "tool_call_id": "c1", "at": "2026-08-22T23:08:06.7931194Z"},
            ]
        )
        self.assertEqual(list(durations), ["c1"])
        self.assertGreaterEqual(durations["c1"], 14)
        self.assertLess(durations["c1"], 20)

    def test_collect_run_evidence_uses_event_timestamps_when_duration_field_missing(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "research_snapshot", "tool_call_id": "s1", "arguments_hash": "sha256:snap", "at": "2026-08-22T23:07:43.0000000Z"},
                    {"type": "tool_result", "tool_name": "research_snapshot", "tool_call_id": "s1", "arguments_hash": "sha256:snap", "is_error": False, "at": "2026-08-22T23:07:43.0400000Z"},
                    {"type": "token_usage", "prompt_tokens": 8, "output_tokens": 2},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, [{"stage": "research", "result": {"tool_results": [{"tool_name": "research_snapshot"}]}}]
            if path.endswith("/diffs"):
                return 200, [{"path": "deliverables/evidence.json"}]
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "success", "metrics": {"duration_ms": 69781}}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-research")
        self.assertEqual(evidence["tool_duration_samples"], 1)
        self.assertEqual(evidence["tool_duration_ms"], 40)
        self.assertNotEqual(evidence["tool_duration_ms"], 69781)

    def test_collect_run_evidence_counts_duplicate_commands_not_ranged_reads(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file_range", "tool_call_id": "r1", "arguments_hash": "sha256:range"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file_range", "tool_call_id": "r1", "arguments_hash": "sha256:range", "is_error": False},
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file_range", "tool_call_id": "r2", "arguments_hash": "sha256:range"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file_range", "tool_call_id": "r2", "arguments_hash": "sha256:range", "is_error": False},
                    {"type": "tool_call", "stage": "implement", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:test"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:test", "is_error": False},
                    {"type": "tool_call", "stage": "implement", "tool_name": "run_command", "tool_call_id": "c2", "arguments_hash": "sha256:test"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "run_command", "tool_call_id": "c2", "arguments_hash": "sha256:test", "is_error": False},
                    {"type": "tool_call", "stage": "research", "tool_name": "research_snapshot", "tool_call_id": "s1"},
                    {"type": "tool_result", "stage": "research", "tool_name": "research_snapshot", "tool_call_id": "s1", "is_error": False},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-range-cmd")
        self.assertEqual(evidence["duplicate_tool_calls"], 1)
        self.assertEqual(evidence["duplicate_full_reads"], 0)
        self.assertGreaterEqual(evidence["tool_signature_samples"], 5)

    def test_collect_run_evidence_counts_only_successful_duplicate_signatures(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "tool_call", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "tool_call", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:retry"},
                    {"type": "tool_result", "tool_name": "run_command", "tool_call_id": "c1", "arguments_hash": "sha256:retry", "is_error": True},
                    {"type": "tool_call", "tool_name": "run_command", "tool_call_id": "c2", "arguments_hash": "sha256:retry"},
                    {"type": "tool_result", "tool_name": "run_command", "tool_call_id": "c2", "arguments_hash": "sha256:retry", "is_error": False},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if path.startswith("/api/workflow-runs/run-duplicate/artifacts?"):
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-duplicate")
        self.assertEqual(evidence["duplicate_tool_calls"], 0)
        self.assertEqual(evidence["duplicate_full_reads"], 1)
        self.assertEqual(evidence["tool_signature_samples"], 4)

    def test_collect_run_evidence_allows_same_read_across_workflow_stages(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "stage": "plan", "tool_name": "read_file", "tool_call_id": "p1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "plan", "tool_name": "read_file", "tool_call_id": "p1", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "workflow_approval_submitted", "stage": "approve-plan"},
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file", "tool_call_id": "i1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file", "tool_call_id": "i1", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file", "tool_call_id": "i2", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file", "tool_call_id": "i2", "arguments_hash": "sha256:file", "is_error": False},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-cross-stage")
        self.assertEqual(evidence["duplicate_tool_calls"], 0)
        self.assertEqual(evidence["duplicate_full_reads"], 1)
        self.assertEqual(evidence["tool_signature_samples"], 3)

    def test_collect_run_evidence_clears_duplicates_on_stage_retry(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "stage": "produce", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "produce", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "status", "stage": "produce", "reason": "stage_retry", "content": "workflow stage produce retry attempt 2"},
                    {"type": "tool_call", "stage": "produce", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "produce", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file", "is_error": False},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-stage-retry")
        self.assertEqual(evidence["duplicate_tool_calls"], 0)
        self.assertEqual(evidence["duplicate_full_reads"], 0)

    def test_collect_run_evidence_clears_duplicates_on_status_event(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file", "is_error": False},
                    {"type": "status", "stage": "implement", "content": "workflow stage implement retry attempt 2: still fail public tests"},
                    {"type": "tool_call", "stage": "implement", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "stage": "implement", "tool_name": "read_file", "tool_call_id": "r2", "arguments_hash": "sha256:file", "is_error": False},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-status-retry")
        self.assertEqual(evidence["duplicate_full_reads"], 0)

    def test_collect_run_evidence_ignores_persisted_duplicate_call_ids(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file"},
                    {"type": "tool_call", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file"},
                    {"type": "tool_result", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file", "is_error": False, "duration_ms": 12, "content": '{"duration_ms":12}'},
                    {"type": "tool_result", "tool_name": "read_file", "tool_call_id": "r1", "arguments_hash": "sha256:file", "is_error": False, "content": '{"duration_ms":12}'},
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-persisted")
        self.assertEqual(evidence["duplicate_tool_calls"], 0)
        self.assertEqual(evidence["duplicate_full_reads"], 0)
        self.assertEqual(evidence["tool_signature_samples"], 1)
        self.assertEqual(evidence["tool_duration_samples"], 1)
        self.assertEqual(evidence["tool_duration_ms"], 12)

    def test_collect_run_evidence_reads_index_duration_from_search_json(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "file_tools/search_files", "tool_call_id": "s1", "arguments_hash": "sha256:search"},
                    {
                        "type": "tool_result",
                        "tool_name": "file_tools/search_files",
                        "tool_call_id": "s1",
                        "arguments_hash": "sha256:search",
                        "is_error": False,
                        "content": '{"query":"Cache","index_duration_ms":18,"matches":[]}',
                    },
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-index-duration")
        self.assertEqual(evidence["tool_duration_ms"], 18)
        self.assertEqual(evidence["tool_duration_samples"], 1)

    def test_collect_run_evidence_reads_duration_from_event_json_content(self) -> None:
        def fake_request(_base_url: str, path: str):
            if "events?" in path and "limit=0" in path:
                return 200, [
                    {"type": "tool_call", "tool_name": "command_tools/run_command", "tool_call_id": "c1", "arguments_hash": "sha256:cmd"},
                    {
                        "type": "tool_result",
                        "tool_name": "command_tools/run_command",
                        "tool_call_id": "c1",
                        "arguments_hash": "sha256:cmd",
                        "is_error": False,
                        "content": '{"program":"go","args":["test","./..."],"exit_code":0,"duration_ms":41}',
                    },
                ]
            if path.endswith("/stages?limit=0&include_content=1"):
                return 200, []
            if path.endswith("/diffs"):
                return 200, []
            if "artifacts?" in path:
                return 200, []
            if path.endswith("/delivery"):
                return 200, {"schema_version": "goflow.delivery.v1", "conclusion": "passed"}
            raise AssertionError(path)

        with patch("scripts.real_provider_acceptance.request_json", side_effect=fake_request):
            evidence = collect_run_evidence("http://127.0.0.1", "workflow", "run-duration")
        self.assertEqual(evidence["tool_duration_ms"], 41)
        self.assertEqual(evidence["tool_duration_samples"], 1)
        self.assertEqual(evidence["duplicate_tool_calls"], 0)


if __name__ == "__main__":
    unittest.main()

class RunCallCeilingTests(unittest.TestCase):
    def test_ceiling_triggers_above_limit(self) -> None:
        self.assertTrue(run_exceeded_call_ceiling({"budget_llm_calls": 121}, 120))

    def test_ceiling_allows_at_the_limit(self) -> None:
        self.assertFalse(run_exceeded_call_ceiling({"budget_llm_calls": 120}, 120))

    def test_ceiling_is_disabled_for_zero_or_missing_data(self) -> None:
        self.assertFalse(run_exceeded_call_ceiling({"budget_llm_calls": 500}, 0))
        self.assertFalse(run_exceeded_call_ceiling({}, 120))
        self.assertFalse(run_exceeded_call_ceiling({"budget_llm_calls": "not-a-number"}, 120))

