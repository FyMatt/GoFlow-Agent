#!/usr/bin/env python3
"""Run deterministic Office creation, template, modification, export, and preview acceptance."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ARTIFACT_ROOT = ROOT / ".artifacts" / "golden-scenario-9"


class MCPProcess:
    def __init__(self) -> None:
        env = os.environ.copy()
        env["GOFLOW_WORKSPACE_ROOT"] = str(ROOT)
        self.process = subprocess.Popen(
            ["go", "run", "./mcp_servers/office_tools"],
            cwd=ROOT,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        self.next_id = 1

    def _request(self, name: str, arguments: dict) -> dict:
        if self.process.stdin is None or self.process.stdout is None:
            raise RuntimeError("MCP process streams are unavailable")
        request_id = self.next_id
        self.next_id += 1
        payload = {"jsonrpc": "2.0", "id": request_id, "method": "tools/call", "params": {"name": name, "arguments": arguments}}
        self.process.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            stderr = self.process.stderr.read() if self.process.stderr is not None else ""
            raise RuntimeError(f"MCP process stopped before replying: {stderr}")
        response = json.loads(line)
        return response.get("result") or {}

    def call(self, name: str, arguments: dict) -> dict:
        result = self._request(name, arguments)
        if result.get("is_error"):
            raise RuntimeError(str(result.get("content")))
        return json.loads(str(result.get("content") or "{}"))

    def call_error(self, name: str, arguments: dict) -> str:
        result = self._request(name, arguments)
        if not result.get("is_error"):
            raise AssertionError(f"{name} should fail, got {result}")
        return str(result.get("content") or "")

    def close(self) -> None:
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)


def relative(path: Path) -> str:
    return path.relative_to(ROOT).as_posix()


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require_text(inspection: dict, expected: list[str]) -> None:
    preview = str(inspection.get("text_preview") or "")
    for text in expected:
        if text not in preview:
            raise AssertionError(f"document inspection is missing {text!r}: {preview}")


def export_preview_validate(mcp: MCPProcess, source: dict, stem: str) -> dict:
    source_path = str(source["artifact_path"])
    pdf = mcp.call(
        "export_office_pdf",
        {"path": source_path, "output_path": relative(ARTIFACT_ROOT / "pdf" / f"{stem}.pdf"), "converter": "auto", "overwrite": True},
    )
    preview = mcp.call(
        "render_office_preview",
        {"path": source_path, "output_dir": relative(ARTIFACT_ROOT / "preview" / stem), "max_pages": 4, "width": 1280, "height": 900},
    )
    validation = mcp.call(
        "validate_office_document",
        {
            "path": source_path,
            "require_pdf": True,
            "require_preview": True,
            "pdf_path": pdf["artifact_path"],
            "preview_paths": preview["preview_paths"],
        },
    )
    if not validation.get("deliverable"):
        raise AssertionError(f"{source_path} did not pass delivery validation: {validation}")
    return {"source": source, "pdf": pdf, "preview": preview, "validation": validation}


def run_scenario(required: bool) -> dict:
    if ARTIFACT_ROOT.exists():
        shutil.rmtree(ARTIFACT_ROOT)
    ARTIFACT_ROOT.mkdir(parents=True, exist_ok=True)
    mcp = MCPProcess()
    try:
        capabilities = mcp.call("office_capabilities", {})
        required_capabilities = ["can_create_pptx", "can_export_pdf", "can_render_preview"]
        missing = [name for name in required_capabilities if not capabilities.get(name)]
        if required and missing:
            raise AssertionError(f"required native Office capabilities are unavailable: {missing}; {capabilities.get('message')}")

        template = mcp.call(
            "create_docx",
            {
                "path": relative(ARTIFACT_ROOT / "templates" / "网络安全评估模板.docx"),
                "title": "{{报告标题}}",
                "subtitle": "{{评估范围}}",
                "sections": [
                    {"title": "评估结论", "paragraphs": ["{{评估结论}}"]},
                    {
                        "title": "关键风险",
                        "tables": [
                            {
                                "headers": ["编号", "风险", "等级", "整改建议"],
                                "rows": [["{{风险编号}}", "{{风险描述}}", "{{风险等级}}", "{{整改建议}}"]],
                            }
                        ],
                    },
                    {"title": "固定声明", "paragraphs": ["本报告只覆盖已授权范围，模板样式和本段内容必须保持不变。"]},
                ],
            },
        )
        template_path = ROOT / str(template["artifact_path"])
        template_hash = file_sha256(template_path)
        security_report = mcp.call(
            "fill_office_template",
            {
                "source_path": template["artifact_path"],
                "output_path": relative(ARTIFACT_ROOT / "deliverables" / "网络安全评估与整改报告.docx"),
                "expected_source_sha256": template["sha256"],
                "replacements": [
                    {"old_text": "{{报告标题}}", "new_text": "网络安全评估与整改报告", "meaning": "生成正式报告标题"},
                    {"old_text": "{{评估范围}}", "new_text": "授权范围：本地测试服务与配置基线", "meaning": "写入授权边界"},
                    {"old_text": "{{评估结论}}", "new_text": "发现一项高风险输入校验缺陷；已给出最小修复、回归验证和剩余风险。", "meaning": "写入证据化结论"},
                    {"old_text": "{{风险编号}}", "new_text": "SEC-001", "meaning": "关联结构化发现"},
                    {"old_text": "{{风险描述}}", "new_text": "参数未绑定可能导致注入风险", "meaning": "写入风险说明"},
                    {"old_text": "{{风险等级}}", "new_text": "高", "meaning": "写入风险等级"},
                    {"old_text": "{{整改建议}}", "new_text": "使用参数化查询并执行公开与隐藏回归测试", "meaning": "写入工程化整改要求"},
                ],
            },
        )
        if file_sha256(template_path) != template_hash:
            raise AssertionError("template source changed during template generation")
        security_inspection = mcp.call("inspect_office_document", {"path": security_report["artifact_path"], "max_text_chars": 10000})
        require_text(security_inspection, ["网络安全评估与整改报告", "SEC-001", "参数化查询", "模板样式和本段内容必须保持不变"])

        requirements = mcp.call(
            "create_docx",
            {
                "path": relative(ARTIFACT_ROOT / "deliverables" / "开发需求文档.docx"),
                "title": "本地智能体工作流平台需求文档",
                "sections": [
                    {"title": "文档能力", "paragraphs": ["旧需求：系统只需要新建 Word 文档。"]},
                    {"title": "验收标准", "paragraphs": ["旧验收：能够生成文件即可。"]},
                    {"title": "既有约束", "paragraphs": ["资源配置保持简洁，未指定章节和格式不得变化。"]},
                ],
            },
        )
        failure = mcp.call_error(
            "replace_office_text",
            {
                "path": requirements["artifact_path"],
                "expected_sha256": requirements["sha256"],
                "replacements": [
                    {"old_text": "旧需求：系统只需要新建 Word 文档。", "new_text": "不得提交的中间内容", "meaning": "模拟第一项可定位"},
                    {"old_text": "不存在的第二项", "new_text": "不得提交", "meaning": "模拟第二项定位失败"},
                ],
            },
        )
        if "未修改文件" not in failure or file_sha256(ROOT / str(requirements["artifact_path"])) != requirements["sha256"]:
            raise AssertionError(f"failed atomic modification did not preserve the source: {failure}")
        modified_requirements = mcp.call(
            "replace_office_text",
            {
                "path": requirements["artifact_path"],
                "expected_sha256": requirements["sha256"],
                "replacements": [
                    {"old_text": "旧需求：系统只需要新建 Word 文档。", "new_text": "新需求：系统支持从零制作、基于模板生成，并按自然语言意图准确修改 Word、Excel 和 PPT。", "meaning": "扩展文档处理范围"},
                    {"old_text": "旧验收：能够生成文件即可。", "new_text": "新验收：源文件可打开、可编辑，未指定内容保持不变，并可导出 PDF、生成预览和通过格式校验。", "meaning": "提升工程化验收标准"},
                ],
            },
        )
        if modified_requirements.get("replacement_count") != 2 or not modified_requirements.get("backup_path"):
            raise AssertionError(f"batch modification evidence is incomplete: {modified_requirements}")
        requirements_inspection = mcp.call("inspect_office_document", {"path": requirements["artifact_path"], "max_text_chars": 10000})
        require_text(requirements_inspection, ["基于模板生成", "自然语言意图准确修改", "未指定内容保持不变", "资源配置保持简洁"])

        findings = mcp.call(
            "create_xlsx",
            {
                "path": relative(ARTIFACT_ROOT / "deliverables" / "安全发现与整改跟踪.xlsx"),
                "sheets": [
                    {"name": "发现清单", "header_rows": 1, "freeze_rows": 1, "auto_filter": True, "rows": [["编号", "领域", "等级", "状态", "证据"], ["SEC-001", "网络安全", "高", "已修复", "公开与隐藏回归通过"], ["OPS-001", "网络运维", "中", "待窗口", "干跑计划与回滚已准备"]]},
                    {"name": "统计", "header_rows": 1, "rows": [["指标", "数量"], ["发现总数", "=COUNTA('发现清单'!A2:A100)"], ["已修复", "=COUNTIF('发现清单'!D2:D100,\"已修复\")"]]},
                ],
            },
        )
        presentation = mcp.call(
            "create_pptx",
            {
                "path": relative(ARTIFACT_ROOT / "deliverables" / "复杂任务交付汇报.pptx"),
                "title": "本地智能体复杂任务交付",
                "subtitle": "开发、网络安全、网络运维与办公文档闭环",
                "slides": [
                    {"title": "任务目标", "bullets": ["完成真实任务而不是只输出建议", "领域结果通过质量门后生成正式文档"]},
                    {"title": "工程化证据", "bullets": ["代码、测试、差异和结构化发现可追溯", "Word、Excel、PPT 和 PDF 均有真实产物"]},
                    {"title": "文档处理", "bullets": ["支持从零制作、模板生成和精确修改", "哈希保护、原子提交、备份、预览和格式复核"]},
                ],
            },
        )

        deliveries = {
            "security_report": export_preview_validate(mcp, security_report, "security-report"),
            "requirements": export_preview_validate(mcp, modified_requirements, "requirements"),
            "findings": export_preview_validate(mcp, findings, "findings"),
            "presentation": export_preview_validate(mcp, presentation, "presentation"),
        }
        result = {
            "status": "passed",
            "capabilities": capabilities,
            "template_generation": {
                "template": template,
                "template_source_preserved": True,
                "report": security_report,
                "inspection": security_inspection,
            },
            "semantic_modification": {
                "failed_batch_preserved_source": True,
                "failure": failure,
                "modified": modified_requirements,
                "inspection": requirements_inspection,
            },
            "cross_domain_deliveries": deliveries,
        }
        (ARTIFACT_ROOT / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        return result
    finally:
        mcp.close()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--required", action="store_true", help="缺少真实 Office 转换或预览能力时直接失败。")
    args = parser.parse_args()
    result = run_scenario(args.required)
    print(json.dumps({"status": result["status"], "artifact_root": relative(ARTIFACT_ROOT), "deliveries": list(result["cross_domain_deliveries"])}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
