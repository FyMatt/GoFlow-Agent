#!/usr/bin/env python3

import contextlib
import io
import unittest
import sys
import tempfile
import json
import gzip
import os
import subprocess
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))

from real_provider_matrix import (
    MatrixCase,
    build_cases,
    evaluate_approval_sequence,
    evaluate_terminal_quality,
    inspect_persisted_run,
    install_hidden_files,
    is_negated_forbidden_occurrence,
    prepare_workspace,
    requires_runtime_restart_injection,
    reevaluate_existing_report,
)
from real_provider_acceptance import drive_run
import validate_real_provider_matrix


class TerminalQualityTest(unittest.TestCase):
    def test_definition_validator_cannot_be_mistaken_for_p7_execution(self) -> None:
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(validate_real_provider_matrix.main(), 0)
        message = output.getvalue()
        self.assertIn("no model call", message)
        self.assertIn("not P7 evidence", message)
        self.assertNotIn("real Provider matrix validation passed", message)

    def test_every_complex_or_recovery_case_has_workflow_restart_injection(self) -> None:
        cases = [case for case in build_cases() if case.level in {"complex", "recovery"}]
        self.assertTrue(cases)
        self.assertTrue(all(case.workflow for case in cases))
        self.assertTrue(all(requires_runtime_restart_injection(case) for case in cases))

    def test_complex_matrix_workspace_tracks_large_fixture_without_hidden_metadata(self) -> None:
        case = next(item for item in build_cases() if item.id == "dev-04-ttl-cache")
        with tempfile.TemporaryDirectory() as temp_dir:
            workspace = prepare_workspace(case, Path(temp_dir), os.environ.copy(), 1)
            tracked = subprocess.check_output(
                ["git", "ls-files"], cwd=workspace, text=True, encoding="utf-8"
            ).splitlines()
            fixture_files = [path for path in tracked if path.startswith("fixture-modules/")]
            self.assertGreaterEqual(len(fixture_files), 120)
            self.assertIn("fixture-manifest.json", tracked)
            self.assertFalse((workspace / "acceptance" / "hidden" / f"{case.id}.json").exists())

    def test_forbidden_term_in_explicit_disclaimer_is_not_a_claim(self) -> None:
        text = "固定声明：不得补充判定依据、测试结果、修复措施或其他结论。"
        start = text.casefold().index("修复措施")
        self.assertTrue(is_negated_forbidden_occurrence(text.casefold(), "修复措施", start))

    def test_forbidden_term_after_disclaimer_is_still_a_claim(self) -> None:
        text = "固定声明：不得补充修复措施，但修复措施已完成。"
        start = text.casefold().rindex("修复措施")
        self.assertFalse(is_negated_forbidden_occurrence(text.casefold(), "修复措施", start))

    def test_explicit_server_tool_reference_becomes_a_required_tool_gate(self) -> None:
        case = next(item for item in build_cases() if item.id == "ops-01-host-diagnosis")
        self.assertEqual(case.required_tool_names, ("host_diagnostic_snapshot",))

    def test_ops_03_requires_literal_protocol_fields_and_excludes_extra_metadata(self) -> None:
        case = next(item for item in build_cases() if item.id == "ops-03-device-dry-run")
        path = "deliverables/device-dry-run.md"
        self.assertIn("requires_approval: true", case.prompt)
        self.assertIn("report_markdown", case.prompt)
        self.assertIn("operator_constraints=[禁止 reload。]", case.files["input/source.txt"])
        self.assertIn("requires_approval: true", case.required_terms[path])
        for term in ("需要审批", "凭据引用", "cred:edge-01", "干跑模式"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_sec_01_enforces_static_evidence_boundary(self) -> None:
        case = next(item for item in build_cases() if item.id == "sec-01-code-audit")
        path = "deliverables/code-audit.md"
        self.assertIn("实际利用结果未知", case.required_terms[path])
        self.assertIn("db.Query(query, input)", case.required_terms[path])
        self.assertNotIn("参数化", case.required_terms[path])
        for term in ("未见上游校验、转义", "净化"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_sec_03_requires_copyable_context_and_real_path_substitution(self) -> None:
        case = next(item for item in build_cases() if item.id == "sec-03-baseline")
        path = "deliverables/ssh-baseline.md"
        command = "sshd -T -C user=<用户>,host=<服务端名>,addr=<客户端IP>,laddr=<服务端IP>,lport=<服务端端口>"
        self.assertIn(command, case.prompt)
        self.assertIn(command, case.required_terms[path])
        self.assertIn("实际执行前必须把全部占位符替换为确认后的真实单文件路径", case.prompt)
        self.assertIn("绝不能带占位符直接执行", case.prompt)
        self.assertIn("必须逐字写明‘不得出现通配符’", case.prompt)
        for term in (
            "不得出现通配符",
            "<源文件1> -> <源文件1>.bak.<TIMESTAMP>",
            "<源文件2> -> <源文件2>.bak.<TIMESTAMP>",
            "<源文件3> -> <源文件3>.bak.<TIMESTAMP>",
            "生成一次时间戳并用于所有备份",
            "全部待修改文件",
        ):
            self.assertIn(term, case.required_terms[path])
        for term in ("不得替换为实际路径", "若三项配置分布在不同文件", "（若存在）"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_sec_04_does_not_invent_input_controllability(self) -> None:
        case = next(item for item in build_cases() if item.id == "sec-04-evidence-confirmation")
        path = "deliverables/vulnerability-evidence.md"
        for term in ("攻击者提交", "用户可控", "外部输入"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_research_01_preserves_each_date_semantic(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-01-official-sources")
        path = "deliverables/official-research.md"
        self.assertIn("不能把后三者统一放入发布日期列", case.prompt)
        for term in ("日期类型", "发布日期", "稳定阶段日期", "更新时间"):
            self.assertIn(term, case.required_terms[path])

    def test_research_02_does_not_expand_a_component_limit_into_cost(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-02-product-comparison")
        path = "deliverables/comparison.md"
        self.assertIn("不得扩写为成本、复杂度、工作量、性能或其他影响", case.prompt)
        for term in ("引入成本", "转换成本", "集成成本", "工作量增加"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_research_03_preserves_complete_structured_events(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-03-structured-extraction")
        path = "deliverables/data.csv"
        self.assertIn("研究范围明确同时包含 alpha 和 beta", case.prompt)
        self.assertIn("不能缩小本请求明确列出的范围", case.prompt)
        self.assertIn("read_file", case.required_tool_names)
        for term in (
            "进入稳定阶段",
            "2026-01-02",
            "更新时间 2025-06-01",
            "结构化导出",
            "批量处理",
            "额外转换组件",
        ):
            self.assertIn(term, case.required_terms[path])

    def test_research_04_does_not_invent_freshness_or_truth_status(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-04-conflict-check")
        path = "deliverables/conflict-review.md"
        for term in ("社区内容不虚假", "已过期", "过时", "滞后", "陈旧", "时效落后", "共同指向 alpha 2.0 为当前版本", "同步延迟"):
            self.assertIn(term, case.forbidden_terms[path])
        for term in ("同域", "独立", "合并", "当前版本"):
            self.assertIn(term, case.required_terms[path])
        self.assertIn("read_file", case.required_tool_names)
        for term in ("证明 alpha 2.0 为当前版本", "证明 alpha 2.0 是当前版本"):
            self.assertNotIn(term, case.forbidden_terms[path])
        for term in ("结论：alpha 2.0 为当前版本", "结论：alpha 2.0 是当前版本"):
            self.assertIn(term, case.forbidden_terms[path])
        for term in ("可信度高于", "可信度低于", "同步延迟", "同步机制"):
            self.assertIn(term, case.forbidden_terms[path])

    def test_research_05_allows_a_bounded_unresolved_conflict(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-05-cited-report")
        self.assertIn("不得仅因冲突存在而把任务判为失败", case.prompt)
        self.assertIn("不写同步、发布流程、镜像状态或其他成因假设", case.prompt)
        self.assertNotIn("双来源", case.forbidden_terms["deliverables/research-report.md"])
        for term in ("单一来源", "无法裁决"):
            self.assertIn(term, case.required_terms["deliverables/research-report.md"])
        for term in ("可能未同步更新", "尚未在该镜像生效", "同步延迟"):
            self.assertIn(term, case.forbidden_terms["deliverables/research-report.md"])

    def test_research_06_limits_scope_and_requires_failure_snapshot(self) -> None:
        case = next(item for item in build_cases() if item.id == "research-06-source-failure")
        self.assertIn("研究范围只限 alpha 2.0", case.prompt)
        self.assertIn("来源快照", case.prompt)
        self.assertIn("分别作为原子事实", case.prompt)
        for term in ("beta 3.1 于", "beta 3.1 已", "beta 3.1 支持"):
            self.assertIn(term, case.forbidden_terms["deliverables/source-failure.md"])
        self.assertIn("read_file", case.required_tool_names)

    def test_docs_01_requires_real_word_pdf_tool_chain(self) -> None:
        case = next(item for item in build_cases() if item.id == "docs-01-word-pdf")
        for tool in ("read_file", "create_docx", "export_office_pdf", "validate_office_document"):
            self.assertIn(tool, case.required_tool_names)
        self.assertEqual(
            case.office_expectations["deliverables/项目简报.docx"]["required_text"],
            ["项目 A", "结论", "风险", "下一步"],
        )

    def test_docs_06_preserves_only_the_four_atomic_source_facts(self) -> None:
        case = next(item for item in build_cases() if item.id == "docs-06-cross-domain")
        self.assertIn("只允许原样使用或拆分重组以下四条原子事实", case.prompt)
        self.assertIn("三个可编辑文件都必须完整覆盖这四条原子事实", case.prompt)
        self.assertIn("不得添加解释性段落", case.prompt)
        for path in (
            "deliverables/综合报告.docx",
            "deliverables/发现跟踪.xlsx",
            "deliverables/评审汇报.pptx",
        ):
            for term in ("按设计要求", "验收测试", "安全隐患", "建议措施", "全面复核", "已修复", "风险已得到缓解", "已通过", "待复核"):
                self.assertIn(term, case.forbidden_terms[path])
            for term in ("SEC-001", "DEV-001", "参数化查询", "公开与隐藏测试通过", "部署配置复核"):
                self.assertIn(term, case.required_terms[path])

    def test_dev_06_requires_exactly_one_stage_approval(self) -> None:
        case = next(item for item in build_cases() if item.id == "dev-06-restart-approval")
        self.assertEqual(case.expected_approval_names, ("approve_stage",))
        self.assertTrue(evaluate_approval_sequence(case, [{"name": "approve_stage"}])["passed"])
        repeated = evaluate_approval_sequence(
            case,
            [{"name": "approve_stage"}, {"name": "approve_all_tools"}],
        )
        self.assertFalse(repeated["passed"])
        self.assertIn("approval sequence mismatch", repeated["failure"])

    def test_drive_run_restores_stage_approval_before_submitting_it(self) -> None:
        state = {"detail_reads": 0, "restarts": 0}

        def fake_request_json(base_url: str, path: str, payload=None, timeout=30.0):
            del base_url, payload, timeout
            if path.endswith("/actions"):
                return 200, {"actions": [{"name": "approve_stage", "path": "/approve", "available": True}]}
            state["detail_reads"] += 1
            if state["detail_reads"] <= 2:
                return 200, {"id": "run-1", "status": "awaiting_approval"}
            return 200, {"id": "run-1", "status": "completed", "completed_stages": []}

        def restart() -> str:
            state["restarts"] += 1
            return "http://restarted"

        with patch("real_provider_acceptance.request_json", side_effect=fake_request_json), patch(
            "real_provider_acceptance.submit_action",
            return_value={"name": "approve_stage", "path": "/approve", "status": 202},
        ), patch("real_provider_acceptance.time.sleep", return_value=None):
            run, approvals, run_id = drive_run(
                "http://initial",
                "workflow",
                "run-1",
                5.0,
                on_first_workflow_approval=restart,
            )

        self.assertEqual(run_id, "run-1")
        self.assertEqual(run["status"], "completed")
        self.assertEqual(state["restarts"], 1)
        self.assertEqual([item["name"] for item in approvals], ["approve_stage"])

    def test_persisted_run_requires_matching_settled_compact_and_full_sessions(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            workspace = Path(temp_dir)
            session_dir = workspace / ".goflow"
            session_dir.mkdir()
            settled = {
                "agent_runs": [{"id": "run-1", "status": "completed"}],
                "pending_approvals": [],
            }
            (session_dir / "session.json").write_text(json.dumps(settled), encoding="utf-8")
            with gzip.open(session_dir / "session.full.json.gz", "wt", encoding="utf-8") as handle:
                json.dump(settled, handle)

            result = inspect_persisted_run(workspace, "agent", "run-1")
            self.assertTrue(result["passed"], result)

            stale = {
                "agent_runs": [{
                    "id": "run-1",
                    "status": "awaiting_tool_approval",
                    "pending_call_id": "call-1",
                    "pending_tool_name": "write_file",
                }],
                "pending_approvals": [{"call_id": "call-1", "agent_run_id": "run-1"}],
            }
            (session_dir / "session.json").write_text(json.dumps(stale), encoding="utf-8")
            result = inspect_persisted_run(workspace, "agent", "run-1")
            self.assertFalse(result["passed"])
            self.assertTrue(any("not terminal" in failure for failure in result["failures"]))

    def test_existing_report_can_be_reevaluated_without_provider_run(self) -> None:
        case = MatrixCase(id="synthetic", domain="development", level="simple", title="synthetic", agent="", skill="", prompt="")
        with tempfile.TemporaryDirectory() as temp_dir:
            artifact_dir = Path(temp_dir)
            workspace = artifact_dir / "workspaces-test" / case.id
            workspace.mkdir(parents=True)
            report_path = artifact_dir / "report.json"
            report_path.write_text(json.dumps({
                "passed": False,
                "results": [{
                    "case": case.id,
                    "passed": False,
                    "status": "completed",
                    "restart_restored": False,
                    "evidence": {"tool_results": 1, "tool_names": []},
                    "terminal_quality": {"passed": True},
                    "run_summary": {"errors": []},
                    "error": "",
                }],
            }), encoding="utf-8")

            output_path, report = reevaluate_existing_report(report_path, [case])
            self.assertTrue(report["passed"])
            self.assertTrue(report["results"][0]["evaluation"]["passed"])
            self.assertEqual(report["results"][0]["reevaluated_workspace"], str(workspace))
            self.assertTrue(output_path.is_file())
            self.assertEqual(report_path.name, "report.json")

    def test_existing_report_finds_repeated_preserved_workspace(self) -> None:
        case = MatrixCase(id="synthetic", domain="development", level="simple", title="synthetic", agent="", skill="", prompt="")
        with tempfile.TemporaryDirectory() as temp_dir:
            artifact_dir = Path(temp_dir)
            workspace = artifact_dir / "workspaces-test" / "repeat-2" / case.id
            workspace.mkdir(parents=True)
            report_path = artifact_dir / "report.json"
            report_path.write_text(json.dumps({
                "passed": False,
                "results": [{
                    "case": case.id,
                    "repetition": 2,
                    "passed": False,
                    "status": "completed",
                    "restart_restored": False,
                    "evidence": {"tool_results": 1, "tool_names": []},
                    "terminal_quality": {"passed": True},
                    "run_summary": {"errors": []},
                    "error": "",
                }],
            }), encoding="utf-8")

            _, report = reevaluate_existing_report(report_path, [case])
            self.assertTrue(report["passed"])
            self.assertEqual(report["results"][0]["reevaluated_workspace"], str(workspace))

    def test_dev_04_hidden_refresh_test_is_installed_only_for_evaluation(self) -> None:
        case = next(case for case in build_cases() if case.id == "dev-04-ttl-cache")
        hidden_path = "cache/matrix_hidden_test.go"
        self.assertIn(hidden_path, case.hidden_files)
        self.assertNotIn(hidden_path, case.files)
        self.assertNotIn("TestMatrixConcurrentRefreshDoesNotReturnStaleMiss", case.prompt)

        with tempfile.TemporaryDirectory() as temp_dir:
            workspace = Path(temp_dir)
            target = workspace / hidden_path
            self.assertFalse(target.exists())
            install_hidden_files(case, workspace)
            self.assertEqual(target.read_text(encoding="utf-8"), case.hidden_files[hidden_path])
            formatted = subprocess.run(
                ["gofmt", "-l", str(target)],
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
            )
            self.assertEqual(formatted.stdout, "")

    def test_agent_run_does_not_require_workflow_quality_gate(self) -> None:
        result = evaluate_terminal_quality({"status": "completed"}, "agent")
        self.assertTrue(result["passed"])
        self.assertFalse(result["required"])

    def test_terminal_summary_preserves_agent_output_for_quality_review(self) -> None:
        from real_provider_matrix import summarize_terminal_run

        summary = summarize_terminal_run({"id": "run-1", "status": "completed", "output": "verified delivery"})
        self.assertEqual(summary["output"], "verified delivery")

    def test_terminal_workflow_with_pending_next_stage_is_rejected(self) -> None:
        result = evaluate_terminal_quality({"status": "completed", "next_stage": "quality"}, "workflow")
        self.assertFalse(result["passed"])
        self.assertIn("next_stage", result["reason"])

    def test_initial_quality_pass_is_accepted_without_repair_closure(self) -> None:
        run = {
            "completed_stages": [
                {"stage": "quality", "status": "completed", "outputs": {"quality_status": "passed", "route": "pass"}},
                {"stage": "final-report", "status": "completed", "result": {}},
            ]
        }
        result = evaluate_terminal_quality(run, "workflow")
        self.assertTrue(result["passed"])
        self.assertEqual(result["stage"], "quality")

    def test_report_stage_is_checked_when_final_report_name_is_not_used(self) -> None:
        run = {
            "completed_stages": [
                {"stage": "quality", "status": "completed", "outputs": {"quality_status": "passed", "route": "pass"}},
                {"stage": "report", "status": "warning", "result": {}},
            ]
        }
        result = evaluate_terminal_quality(run, "workflow")
        self.assertFalse(result["passed"])
        self.assertEqual(result["final_report_stage"], "report")

    def test_final_quality_supersedes_failed_initial_gate(self) -> None:
        run = {
            "completed_stages": [
                {"stage": "quality", "status": "warning", "outputs": {"quality_status": "failed", "route": "fail"}},
                {
                    "stage": "final-quality",
                    "status": "completed",
                    "output": {"variables": {"quality_status": "passed", "route": "pass"}},
                },
                {"stage": "final-report", "status": "completed", "result": {"quality_status": "passed"}},
            ]
        }
        result = evaluate_terminal_quality(run, "workflow")
        self.assertTrue(result["passed"])
        self.assertEqual(result["stage"], "final-quality")

    def test_failed_final_quality_is_rejected(self) -> None:
        run = {
            "completed_stages": [
                {"stage": "quality", "status": "warning", "outputs": {"quality_status": "failed", "route": "fail"}},
                {"stage": "final-quality", "status": "warning", "outputs": {"quality_status": "failed", "route": "fail"}},
                {"stage": "final-report", "status": "warning", "result": {}},
            ]
        }
        result = evaluate_terminal_quality(run, "workflow")
        self.assertFalse(result["passed"])
        self.assertIn("final-quality", result["reason"])

    def test_warning_final_report_is_rejected_after_passing_gate(self) -> None:
        run = {
            "completed_stages": [
                {"stage": "quality", "status": "completed", "outputs": {"quality_status": "passed", "route": "pass"}},
                {"stage": "final-report", "status": "warning", "result": {}},
            ]
        }
        result = evaluate_terminal_quality(run, "workflow")
        self.assertFalse(result["passed"])
        self.assertIn("final-report", result["reason"])


if __name__ == "__main__":
    unittest.main()
