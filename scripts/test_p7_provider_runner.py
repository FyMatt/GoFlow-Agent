from __future__ import annotations

import json
import sys
import tempfile
import unittest
import urllib.error
import urllib.request
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from scripts.acceptance_evidence import acceptance_round_path
from scripts.p7_complex_cases import P7ComplexCase, cases
from scripts.p7_provider_runner import (
    adopt_live_workspace_delivery,
    configured_primary_probe,
    cleanup_runtime_path,
    copy_workspace_for_hidden_eval,
    evaluate_on_isolated_copy,
    hidden_paths_present,
    main,
    parse_args,
    prepare_case_resume,
    provider_probe_retryable,
    provider_probe_with_retries,
    provider_temporarily_unavailable,
    ResearchServer,
    restore_conflicted_workspace_files,
    stage_provider_secret,
    stage_provider_secret_value,
    terminal_delivery_recovered,
    verify_staging_then_apply,
)


class P7ProviderProbeRetryTests(unittest.TestCase):
    def test_retries_empty_response_and_requires_one_complete_attempt(self) -> None:
        empty = {
            "status": "error",
            "message": "provider returned an empty chat response",
            "capabilities": {"chat": False},
            "complete": False,
        }
        complete = {
            "status": "ok",
            "message": "provider capabilities verified",
            "capabilities": {
                "chat": True,
                "streaming": True,
                "tool_calling": True,
                "structured_output": True,
            },
            "complete": True,
        }
        with (
            patch("scripts.p7_provider_runner.provider_probe", side_effect=[empty, complete]) as provider_probe,
            patch("scripts.p7_provider_runner.time.sleep") as sleep,
        ):
            result = provider_probe_with_retries("http://127.0.0.1:1")
        self.assertTrue(result["complete"])
        self.assertEqual(result["attempts"], 2)
        self.assertEqual(provider_probe.call_count, 2)
        sleep.assert_called_once_with(1)

    def test_does_not_retry_non_transient_capability_failure(self) -> None:
        unsupported = {
            "status": "error",
            "message": 'provider returned 404 Not Found: model_not_found',
            "capabilities": {"chat": False},
            "complete": False,
        }
        self.assertFalse(provider_probe_retryable(unsupported))
        with patch("scripts.p7_provider_runner.provider_probe", return_value=unsupported) as provider_probe:
            result = provider_probe_with_retries("http://127.0.0.1:1")
        self.assertFalse(result["complete"])
        self.assertEqual(result["attempts"], 1)
        provider_probe.assert_called_once()

    def test_retries_probe_timeout_exception_and_keeps_machine_readable_result(self) -> None:
        complete = {
            "status": "ok",
            "message": "provider capabilities verified",
            "model": "test-model",
            "capabilities": {
                "chat": True,
                "streaming": True,
                "tool_calling": True,
                "structured_output": True,
            },
            "complete": True,
        }
        with (
            patch("scripts.p7_provider_runner.provider_probe", side_effect=[TimeoutError("timed out"), complete]) as provider_probe,
            patch("scripts.p7_provider_runner.time.sleep") as sleep,
        ):
            result = provider_probe_with_retries("http://127.0.0.1:1")
        self.assertTrue(result["complete"])
        self.assertEqual(result["attempts"], 2)
        self.assertEqual(provider_probe.call_count, 2)
        sleep.assert_called_once_with(1)

    def test_cloudflare_524_is_retryable_for_probe_and_task(self) -> None:
        probe = {
            "status": "limited",
            "message": "provider connection succeeded with limited capabilities",
            "capability_details": [
                {"name": "tool_calling", "supported": False, "message": "provider returned 524 : error code: 524"}
            ],
            "complete": False,
        }
        self.assertTrue(provider_probe_retryable(probe))
        self.assertTrue(provider_temporarily_unavailable({"execution_error": "provider returned 524 : error code: 524"}))

    def test_probe_retries_mixed_route_bad_request_without_marking_task_transient(self) -> None:
        probe = {
            "status": "limited",
            "message": "provider connection succeeded with limited capabilities",
            "capability_details": [
                {"name": "structured_output", "supported": False, "message": "provider returned 400 Bad Request"}
            ],
            "complete": False,
        }
        self.assertTrue(provider_probe_retryable(probe))
        self.assertFalse(provider_temporarily_unavailable({"execution_error": "provider returned 400 Bad Request"}))

    def test_http2_protocol_error_is_retryable_for_probe_and_task(self) -> None:
        message = "send Responses stream request: stream error: stream ID 11; PROTOCOL_ERROR; received from peer"
        self.assertTrue(provider_probe_retryable({"complete": False, "message": message}))
        self.assertTrue(
            provider_temporarily_unavailable(
                {"status": "action_failed", "provider_run": {"summary": message}}
            )
        )

    def test_terminal_delivery_recovery_requires_every_quality_gate(self) -> None:
        error = "[WinError 10054] connection reset"
        run = {"status": "completed"}
        staging = {"passed": True}
        evaluation = {"passed": True}
        self.assertTrue(terminal_delivery_recovered(error, run, staging, evaluation, True))
        self.assertFalse(terminal_delivery_recovered("model_not_found", run, staging, evaluation, True))
        self.assertFalse(terminal_delivery_recovered(error, {"status": "running"}, staging, evaluation, True))
        self.assertFalse(terminal_delivery_recovered(error, run, {"passed": False}, evaluation, True))
        self.assertFalse(terminal_delivery_recovered(error, run, staging, {"passed": False}, True))
        self.assertFalse(terminal_delivery_recovered(error, run, staging, evaluation, False))


class P7ProviderRunnerResumeTests(unittest.TestCase):
    def _report(self, case, repetition: int) -> dict:
        return {
            "case_id": case.id,
            "domain": case.domain,
            "repetition": repetition,
            "provider": {"valid": True, "configured": True, "complete": True, "model": "test-model"},
        }

    def _round(self, root: Path, case, repetition: int) -> Path:
        target = acceptance_round_path(root, domain=case.domain, case_id=case.id, repetition=repetition)
        target.mkdir(parents=True)
        (target / "report.json").write_text(json.dumps(self._report(case, repetition)), encoding="utf-8")
        return target

    def test_resumes_only_valid_consecutive_rounds(self) -> None:
        case = cases()[0]
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            first = self._round(root, case, 1)
            with patch("scripts.p7_provider_runner.validate_acceptance_round", return_value={"valid": True}):
                completed, archived = prepare_case_resume(root, case, 3, "resume-test")
            self.assertEqual(completed, {1})
            self.assertIsNone(archived)
            self.assertTrue(first.is_dir())

    def test_archives_a_failed_or_broken_series_before_restart(self) -> None:
        case = cases()[0]
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            first = self._round(root, case, 1)
            second = self._round(root, case, 2)

            def validate(target: Path, *, require_success: bool = False):
                del require_success
                if target == second:
                    raise RuntimeError("independent verification failed")
                return {"valid": True}

            with patch("scripts.p7_provider_runner.validate_acceptance_round", side_effect=validate):
                completed, archived = prepare_case_resume(root, case, 3, "archive-test")
            self.assertEqual(completed, {1})
            self.assertIsNotNone(archived)
            assert archived is not None
            self.assertTrue(first.exists())
            self.assertFalse(second.exists())
            self.assertFalse((archived / "第1轮").exists())
            self.assertTrue((archived / "第2轮" / "report.json").is_file())
            manifest = json.loads((archived / "archive.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest["rounds"], [2])

    def test_archives_rounds_whose_live_provider_probe_was_skipped(self) -> None:
        case = cases()[0]
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            first = self._round(root, case, 1)
            report_path = first / "report.json"
            report = json.loads(report_path.read_text(encoding="utf-8"))
            report["provider"]["skipped"] = True
            report_path.write_text(json.dumps(report), encoding="utf-8")
            with patch("scripts.p7_provider_runner.validate_acceptance_round", return_value={"valid": True}):
                completed, archived = prepare_case_resume(root, case, 3, "skipped-probe-test")
            self.assertEqual(completed, set())
            self.assertIsNotNone(archived)
            assert archived is not None
            self.assertFalse(first.exists())
            self.assertTrue((archived / "第1轮" / "report.json").is_file())

    def test_targeting_an_earlier_round_does_not_archive_later_history(self) -> None:
        case = cases()[0]
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            first = self._round(root, case, 1)
            later = self._round(root, case, 3)
            with patch("scripts.p7_provider_runner.validate_acceptance_round", return_value={"valid": True}):
                completed, archived = prepare_case_resume(root, case, 1, "history-test")
            self.assertEqual(completed, {1})
            self.assertIsNone(archived)
            self.assertTrue(first.is_dir())
            self.assertTrue(later.is_dir())

    def test_cleanup_runtime_path_removes_readonly_tree(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            runtime = Path(raw) / "runtime"
            nested = runtime / "gocache"
            nested.mkdir(parents=True)
            readonly = nested / "entry"
            readonly.write_text("cache", encoding="utf-8")
            readonly.chmod(0o400)
            passed, detail = cleanup_runtime_path(runtime)
            self.assertTrue(passed, detail)
            self.assertFalse(runtime.exists())

    def test_research_server_exposes_catalog_and_resets_failure_injection(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            (workspace / "sources").mkdir()
            (workspace / "source-catalog.json").write_text('{"sources":[]}', encoding="utf-8")
            (workspace / "sources" / "removed-source.html").write_text("recovered", encoding="utf-8")
            for _ in range(2):
                server = ResearchServer(workspace)
                base_url = server.start()
                try:
                    with urllib.request.urlopen(f"{base_url}/source-catalog.json", timeout=2) as response:
                        self.assertEqual(response.status, 200)
                    with self.assertRaises(urllib.error.HTTPError) as raised:
                        urllib.request.urlopen(f"{base_url}/sources/removed-source.html", timeout=2)
                    self.assertEqual(raised.exception.code, 503)
                    with urllib.request.urlopen(f"{base_url}/sources/removed-source.html", timeout=2) as response:
                        self.assertEqual(response.status, 200)
                finally:
                    server.close()


class P7ProviderRunnerArgumentTests(unittest.TestCase):
    def test_probe_only_does_not_require_acceptance_workspace(self) -> None:
        with patch.object(sys, "argv", ["p7_provider_runner.py", "--probe-only"]):
            args = parse_args()
        self.assertTrue(args.probe_only)
        self.assertIsNone(args.acceptance_workspace)

    def test_provider_secret_is_staged_only_from_primary_secret_directory(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            source = root / "secrets" / "providers" / "primary" / "api_key.reserve-2"
            source.parent.mkdir(parents=True)
            source.write_text("provider-secret\n", encoding="utf-8")
            data_root = root / "isolated"
            destination = stage_provider_secret(root, data_root, source)
            self.assertEqual(destination.read_text(encoding="utf-8"), "provider-secret\n")
            self.assertEqual(destination.relative_to(data_root).as_posix(), "secrets/providers/primary/api_key")

    def test_provider_secret_rejects_escape_and_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            primary = root / "secrets" / "providers" / "primary"
            primary.mkdir(parents=True)
            outside = root / "outside-secret"
            outside.write_text("secret\n", encoding="utf-8")
            with self.assertRaises(ValueError):
                stage_provider_secret(root, root / "isolated", outside)
            link = primary / "api_key.link"
            try:
                link.symlink_to(outside)
            except (OSError, NotImplementedError):
                self.skipTest("symlink creation is unavailable on this Windows host")
            with self.assertRaises(ValueError):
                stage_provider_secret(root, root / "isolated", link)

    def test_invalid_provider_secret_cleans_temporary_workspace(self) -> None:
        args = self._probe_args()
        args.provider_secret = Path("outside-secret")
        with tempfile.TemporaryDirectory() as raw:
            with (
                patch("scripts.p7_provider_runner.ROOT", Path(raw)),
                patch("scripts.p7_provider_runner.parse_args", return_value=args),
                patch("scripts.p7_provider_runner.build_binary") as build_binary,
            ):
                with self.assertRaises(ValueError):
                    main()
            self.assertEqual(list(Path(raw).glob(".artifacts/p7-provider/*/p7-workspaces-*")), [])
            build_binary.assert_not_called()

    def test_provider_secret_content_and_source_are_absent_from_probe_report(self) -> None:
        args = self._probe_args()
        args.provider_secret = Path("secrets/providers/primary/api_key.reserve-test")
        probe = {
            "status": "error",
            "message": "provider unavailable",
            "model": "test-model",
            "capabilities": {"chat": False},
            "complete": False,
        }
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            source = root / args.provider_secret
            source.parent.mkdir(parents=True)
            source.write_text("super-secret-provider-value\n", encoding="utf-8")
            with (
                patch("scripts.p7_provider_runner.ROOT", root),
                patch("scripts.p7_provider_runner.parse_args", return_value=args),
                patch("scripts.p7_provider_runner.build_binary"),
                patch("scripts.p7_provider_runner.start_server", return_value=(None, None)),
                patch("scripts.p7_provider_runner.stop_server"),
                patch("scripts.p7_provider_runner.provider_probe", return_value=probe),
            ):
                self.assertEqual(main(), 1)
            report = next(root.glob(".artifacts/p7-provider/*/report.json"))
            report_text = report.read_text(encoding="utf-8")
            self.assertNotIn("super-secret-provider-value", report_text)
            self.assertNotIn("api_key.reserve-test", report_text)
            self.assertFalse(list(root.glob(".artifacts/p7-provider/*/p7-workspaces-*")))

    def test_skipped_probe_still_records_configured_model(self) -> None:
        probe = configured_primary_probe(skipped=True)
        self.assertTrue(probe.get("skipped"))
        self.assertTrue(str(probe.get("model") or "").strip())
        self.assertTrue(str(probe.get("provider") or "").strip())
        self.assertTrue(probe.get("valid") is True)
        self.assertTrue(probe.get("configured") is True)
        self.assertTrue(probe.get("complete") is True)

    def test_dns_lookup_failure_is_temporarily_unavailable(self) -> None:
        self.assertTrue(
            provider_temporarily_unavailable(
                {
                    "execution_error": "",
                    "status": "failed",
                    "provider_run": {
                        "summary": 'workflow stage plan: llm chat: send Responses stream request: Post "https://lucen.plus/responses": dial tcp: lookup lucen.plus: no such host'
                    },
                }
            )
        )

    def test_windows_connection_reset_is_temporarily_unavailable(self) -> None:
        self.assertTrue(
            provider_temporarily_unavailable(
                {
                    "execution_error": "[WinError 10054] 远程主机强迫关闭了一个现有的连接。",
                    "status": "completed",
                    "provider_run": {},
                }
            )
        )

    def test_provider_read_timeout_is_temporarily_unavailable(self) -> None:
        self.assertTrue(
            provider_temporarily_unavailable(
                {"status": "action_failed", "provider_run": {"summary": "read Responses stream: context deadline exceeded"}}
            )
        )
        self.assertTrue(
            provider_temporarily_unavailable(
                {
                    "status": "action_failed",
                    "provider_run": {"summary": "wsarecv: An existing connection was forcibly closed by the remote host"},
                }
            )
        )

    def test_formal_run_requires_acceptance_workspace(self) -> None:
        with patch.object(sys, "argv", ["p7_provider_runner.py"]):
            with self.assertRaises(SystemExit) as raised:
                parse_args()
        self.assertEqual(raised.exception.code, 2)

    def _probe_args(self) -> SimpleNamespace:
        return SimpleNamespace(
            case=None,
            repeat=1,
            timeout=30.0,
            acceptance_workspace=None,
            keep_workspaces=False,
            skip_provider_probe=False,
            probe_only=True,
            provider_secret=None,
            provider_secret_env=None,
        )

    def test_provider_secret_value_is_staged_without_source_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            destination = stage_provider_secret_value(Path(raw), "provider-secret")
            self.assertEqual(destination.read_text(encoding="utf-8"), "provider-secret\n")
            self.assertEqual(destination.relative_to(Path(raw)).as_posix(), "secrets/providers/primary/api_key")

    def test_provider_secret_options_are_mutually_exclusive(self) -> None:
        with patch.object(sys, "argv", ["p7_provider_runner.py", "--probe-only", "--provider-secret", "a", "--provider-secret-env", "B"]):
            with self.assertRaises(SystemExit) as raised:
                parse_args()
        self.assertEqual(raised.exception.code, 2)

    def test_probe_only_stops_before_fixture_creation(self) -> None:
        probe = {
            "status": "ok",
            "message": "connected",
            "model": "test-model",
            "capabilities": {"chat": True},
            "complete": True,
        }
        with tempfile.TemporaryDirectory() as raw:
            with (
                patch("scripts.p7_provider_runner.ROOT", Path(raw)),
                patch("scripts.p7_provider_runner.parse_args", return_value=self._probe_args()),
                patch("scripts.p7_provider_runner.build_binary"),
                patch("scripts.p7_provider_runner.start_server", return_value=(None, None)),
                patch("scripts.p7_provider_runner.stop_server"),
                patch("scripts.p7_provider_runner.provider_probe", return_value=probe),
                patch("scripts.p7_provider_runner.prepare_workspace") as prepare_workspace,
            ):
                self.assertEqual(main(), 0)
            prepare_workspace.assert_not_called()
            reports = list(Path(raw).glob(".artifacts/p7-provider/*/report.json"))
            self.assertEqual(len(reports), 1)
            report = json.loads(reports[0].read_text(encoding="utf-8"))
            self.assertTrue(report["passed"])
            self.assertEqual(report["formal_p7_rounds"], 0)
            artifact_dir = reports[0].parent
            self.assertFalse((artifact_dir / "gocache").exists())
            self.assertFalse((artifact_dir / "gotmp").exists())
            self.assertFalse((artifact_dir / "goflow-p7.exe").exists())

    def test_probe_failure_writes_zero_round_report(self) -> None:
        probe = {
            "status": "error",
            "message": "provider unavailable",
            "model": "test-model",
            "capabilities": {"chat": False},
            "complete": False,
        }
        with tempfile.TemporaryDirectory() as raw:
            with (
                patch("scripts.p7_provider_runner.ROOT", Path(raw)),
                patch("scripts.p7_provider_runner.parse_args", return_value=self._probe_args()),
                patch("scripts.p7_provider_runner.build_binary"),
                patch("scripts.p7_provider_runner.start_server", return_value=(None, None)),
                patch("scripts.p7_provider_runner.stop_server"),
                patch("scripts.p7_provider_runner.provider_probe", return_value=probe),
                patch("scripts.p7_provider_runner.prepare_workspace") as prepare_workspace,
            ):
                self.assertEqual(main(), 1)
            prepare_workspace.assert_not_called()
            reports = list(Path(raw).glob(".artifacts/p7-provider/*/report.json"))
            self.assertEqual(len(reports), 1)
            report = json.loads(reports[0].read_text(encoding="utf-8"))
            self.assertFalse(report["passed"])
            self.assertEqual(report["formal_p7_rounds"], 0)
            self.assertEqual(report["results"], [])
            artifact_dir = reports[0].parent
            self.assertFalse((artifact_dir / "gocache").exists())
            self.assertFalse((artifact_dir / "gotmp").exists())
            self.assertFalse((artifact_dir / "goflow-p7.exe").exists())

    def test_hidden_eval_copy_reads_current_file_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            src = Path(raw) / "src"
            dst = Path(raw) / "dst"
            (src / ".git").mkdir(parents=True)
            (src / ".git" / "HEAD").write_text("stale", encoding="utf-8")
            (src / "internal").mkdir()
            (src / "internal" / "cache.go").write_text("package cache\n", encoding="utf-8")
            copy_workspace_for_hidden_eval(src, dst)
            self.assertEqual((dst / "internal" / "cache.go").read_text(encoding="utf-8"), "package cache\n")
            self.assertFalse((dst / ".git").exists())

    def test_hidden_eval_uses_a_copy_and_does_not_mutate_source(self) -> None:
        case = P7ComplexCase(
            id="copy-case",
            domain="development",
            title="copy",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "secret-hidden"},
            verify_commands=(("go", "test", "./..."),),
            required_deliverables=("docs/upgrade.md",),
        )
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw) / "workspace"
            workspace.mkdir()
            (workspace / "docs").mkdir()
            (workspace / "docs" / "upgrade.md").write_text("# upgrade\n", encoding="utf-8")
            with patch("scripts.p7_case_evaluator.run_command", return_value={"passed": True, "command": ["go", "test", "./..."]}):
                result = evaluate_on_isolated_copy(
                    case,
                    workspace,
                    evidence={"tool_results": 1, "token_events": 1, "artifacts": 1},
                    run={},
                )
            self.assertTrue(result["passed"], result)
            self.assertFalse((workspace / "hidden.txt").exists())
            self.assertEqual(hidden_paths_present(case, workspace), [])

    def test_leaked_hidden_files_in_staging_block_writeback(self) -> None:
        case = P7ComplexCase(
            id="leak-case",
            domain="development",
            title="leak",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "secret-hidden"},
        )
        with tempfile.TemporaryDirectory() as raw:
            staging = Path(raw) / "staging"
            staging.mkdir()
            (staging / "hidden.txt").write_text("secret-hidden", encoding="utf-8")
            with (
                patch("scripts.p7_provider_runner.evaluate_on_isolated_copy", return_value={"passed": True}),
                patch("scripts.p7_provider_runner.apply_staged_changes_if_available", return_value=True) as apply_changes,
            ):
                evaluation, applied = verify_staging_then_apply(
                    case, staging, "http://127.0.0.1:9", "run-leak", [], {"tool_results": 1}, {"status": "completed"}
                )
            self.assertFalse(applied)
            self.assertFalse(evaluation["passed"])
            apply_changes.assert_not_called()
            self.assertEqual(hidden_paths_present(case, staging), [])

    def test_writeback_is_skipped_when_staging_hidden_eval_fails(self) -> None:
        case = P7ComplexCase(
            id="fail-case",
            domain="development",
            title="fail",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "hidden"},
        )
        with tempfile.TemporaryDirectory() as raw:
            staging = Path(raw) / "staging"
            staging.mkdir()
            (staging / "main.go").write_text("package main\n", encoding="utf-8")
            with (
                patch("scripts.p7_provider_runner.evaluate_on_isolated_copy", return_value={"passed": False, "failures": ["hidden test failed"]}),
                patch("scripts.p7_provider_runner.apply_staged_changes_if_available", return_value=True) as apply_changes,
            ):
                evaluation, applied = verify_staging_then_apply(
                    case, staging, "http://127.0.0.1:9", "run-1", [], {"tool_results": 1}, {"status": "completed"}
                )
            self.assertFalse(evaluation["passed"])
            self.assertFalse(applied)
            apply_changes.assert_not_called()
            self.assertEqual(hidden_paths_present(case, staging), [])

    def test_writeback_uses_public_apply_only_after_staging_pass(self) -> None:
        case = P7ComplexCase(
            id="pass-case",
            domain="development",
            title="pass",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "hidden"},
        )
        with tempfile.TemporaryDirectory() as raw:
            staging = Path(raw) / "staging"
            staging.mkdir()
            with (
                patch("scripts.p7_provider_runner.evaluate_on_isolated_copy", return_value={"passed": True}),
                patch("scripts.p7_provider_runner.apply_staged_changes_if_available", return_value=True) as apply_changes,
            ):
                evaluation, applied = verify_staging_then_apply(
                    case, staging, "http://127.0.0.1:9", "run-2", [], {"tool_results": 2}, {"status": "completed"}
                )
            self.assertTrue(evaluation["passed"])
            self.assertTrue(applied)
            apply_changes.assert_called_once_with("http://127.0.0.1:9", "workflow", "run-2", [])

    def test_writeback_copies_staging_when_public_apply_unavailable(self) -> None:
        case = P7ComplexCase(
            id="fallback-case",
            domain="development",
            title="fallback",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "hidden"},
        )
        with tempfile.TemporaryDirectory() as raw:
            staging = Path(raw) / "staging"
            workspace = Path(raw) / "workspace"
            staging.mkdir()
            workspace.mkdir()
            (staging / "docs").mkdir()
            (staging / "docs" / "upgrade.md").write_text("# 升级\n\n回滚步骤与验证命令。\n", encoding="utf-8")
            with (
                patch("scripts.p7_provider_runner.evaluate_on_isolated_copy", return_value={"passed": True}),
                patch("scripts.p7_provider_runner.apply_staged_changes_if_available", return_value=False),
            ):
                evaluation, applied = verify_staging_then_apply(
                    case,
                    staging,
                    "http://127.0.0.1:9",
                    "run-fallback",
                    [],
                    {"tool_results": 2},
                    {"status": "completed"},
                    workspace=workspace,
                )
            self.assertTrue(evaluation["passed"])
            self.assertTrue(applied)
            self.assertTrue((workspace / "docs" / "upgrade.md").is_file())

    def test_apply_workspace_conflict_restores_head_and_retries(self) -> None:
        case = P7ComplexCase(
            id="conflict-case",
            domain="security",
            title="conflict",
            workflow="",
            prompt="",
            builder=lambda root: None,
            hidden_files={"hidden.txt": "hidden"},
        )
        approvals: list[dict] = []
        with tempfile.TemporaryDirectory() as raw:
            staging = Path(raw) / "staging"
            staging.mkdir()
            workspace = Path(raw) / "workspace"
            workspace.mkdir()
            calls = {"count": 0}

            def apply(_base, _kind, _run_id, _approvals):
                calls["count"] += 1
                if calls["count"] == 1:
                    raise RuntimeError("apply_staged_changes returned 409: {'error': 'workspace conflict for internal/request/header.go: source SHA-256 changed'}")
                return True

            with (
                patch("scripts.p7_provider_runner.evaluate_on_isolated_copy", return_value={"passed": True}),
                patch("scripts.p7_provider_runner.apply_staged_changes_if_available", side_effect=apply),
                patch("scripts.p7_provider_runner.restore_conflicted_workspace_files", return_value=["internal/request/header.go"]) as restore,
            ):
                evaluation, applied = verify_staging_then_apply(
                    case, staging, "http://127.0.0.1:9", "run-conflict", approvals, {"tool_results": 2}, {"status": "completed"}, workspace=workspace
                )
            self.assertTrue(evaluation["passed"])
            self.assertTrue(applied)
            self.assertEqual(calls["count"], 2)
            restore.assert_called_once()

    def test_adopt_live_workspace_delivery_when_staging_is_missing(self) -> None:
        staging = {"passed": False, "failures": ["durable task-staging workspace is missing"]}
        evaluation = {"passed": True, "case_id": "p7-research-source-verification"}
        adopted, applied = adopt_live_workspace_delivery(staging, evaluation, False)
        self.assertTrue(applied)
        self.assertTrue(adopted["passed"])
        self.assertTrue(adopted["live_workspace_delivery"])
        unchanged, still_false = adopt_live_workspace_delivery(staging, {"passed": False}, False)
        self.assertFalse(still_false)
        self.assertEqual(unchanged, staging)

    def test_restore_conflicted_workspace_files_checks_out_head(self) -> None:
        error = "workspace conflict for internal/request/header.go: source SHA-256 changed"
        with tempfile.TemporaryDirectory() as raw:
            workspace = Path(raw)
            with patch("scripts.p7_provider_runner.git_output") as git:
                restored = restore_conflicted_workspace_files(workspace, error)
            self.assertEqual(restored, ["internal/request/header.go"])
            git.assert_called_once_with(workspace, "checkout", "HEAD", "--", "internal/request/header.go")

    def test_missing_staging_workspace_does_not_write_back(self) -> None:
        case = cases()[0]
        with patch("scripts.p7_provider_runner.apply_staged_changes_if_available") as apply_changes:
            evaluation, applied = verify_staging_then_apply(
                case, None, "http://127.0.0.1:9", "run-3", [], {}, {}
            )
        self.assertFalse(evaluation["passed"])
        self.assertFalse(applied)
        apply_changes.assert_not_called()

    def test_probe_only_rejects_skipping_the_probe(self) -> None:
        with patch.object(
            sys,
            "argv",
            ["p7_provider_runner.py", "--probe-only", "--skip-provider-probe"],
        ):
            with self.assertRaises(SystemExit) as raised:
                parse_args()
        self.assertEqual(raised.exception.code, 2)


if __name__ == "__main__":
    unittest.main()
