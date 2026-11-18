#!/usr/bin/env python3
"""Run the P7 Office/WPS case through the real office_tools MCP server."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any

from p7_complex_cases import cases


ROOT = Path(__file__).resolve().parent.parent


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


class MCPProcess:
    def __init__(self, workspace: Path, binary: Path) -> None:
        env = os.environ.copy()
        env["GOFLOW_WORKSPACE_ROOT"] = str(workspace)
        self.process = subprocess.Popen(
            [str(binary)], cwd=workspace, env=env, stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            encoding="utf-8", errors="replace", bufsize=1,
        )
        self._next_id = 1

    def call(self, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
        if self.process.stdin is None or self.process.stdout is None:
            raise RuntimeError("office_tools MCP pipes are unavailable")
        request = {
            "jsonrpc": "2.0",
            "id": self._next_id,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments or {}},
        }
        self._next_id += 1
        self.process.stdin.write(json.dumps(request, ensure_ascii=False) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            stderr = self.process.stderr.read() if self.process.stderr else ""
            raise RuntimeError(f"office_tools exited before response: {stderr[-2000:]}")
        response = json.loads(line)
        result = response.get("result") if isinstance(response, dict) else None
        if not isinstance(result, dict):
            raise RuntimeError(f"invalid MCP response for {name}: {response}")
        content = result.get("content")
        if isinstance(content, str):
            try:
                decoded = json.loads(content)
            except json.JSONDecodeError:
                decoded = {"message": content}
        else:
            decoded = content
        return {"name": name, "is_error": bool(result.get("is_error")), "payload": decoded}

    def close(self) -> None:
        pid = self.process.pid
        if self.process.stdin:
            try:
                self.process.stdin.close()
            except OSError:
                pass
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            if os.name == "nt":
                # office_tools may start a converter/helper child. Terminate
                # only this MCP process tree so its executable is released.
                subprocess.run(
                    ["taskkill", "/PID", str(pid), "/T", "/F"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    check=False,
                )
            else:
                self.process.kill()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        for stream in (self.process.stdout, self.process.stderr):
            if stream:
                try:
                    stream.close()
                except OSError:
                    pass


def call_checked(client: MCPProcess, operations: list[dict[str, Any]], name: str, arguments: dict[str, Any]) -> dict[str, Any]:
    result = client.call(name, arguments)
    operations.append(result)
    return result


def payload_error(result: dict[str, Any]) -> str:
    payload = result.get("payload")
    if isinstance(payload, dict):
        return str(payload.get("error") or payload.get("message") or payload)
    return str(payload)


def build_binary(output: Path) -> None:
    env = os.environ.copy()
    env.setdefault("GOCACHE", str(ROOT / ".artifacts" / "p7-office-gocache"))
    Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    completed = subprocess.run(
        ["go", "build", "-buildvcs=false", "-o", str(output), "./mcp_servers/office_tools"],
        cwd=ROOT, env=env, text=True, encoding="utf-8", errors="replace",
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=240, check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"office_tools build failed:\n{completed.stdout[-8000:]}")


def remove_file_with_retry(path: Path, attempts: int = 50, delay_seconds: float = 0.2) -> bool:
    """Remove a just-exited Windows binary despite short-lived handle lag."""
    for attempt in range(max(1, attempts)):
        try:
            path.unlink(missing_ok=True)
        except OSError:
            pass
        if not path.exists():
            return True
        if attempt + 1 < attempts:
            time.sleep(delay_seconds)
    return not path.exists()


def run_case(workspace: Path, keep_binary: bool = False) -> dict[str, Any]:
    case = next(item for item in cases() if item.domain == "documents")
    generated = ("templates", "inputs", "deliverables", "acceptance", "fixture-manifest.json", "README.md", ".office-tools-bin", ".office-tools-bin.exe")
    if (workspace / "fixture-manifest.json").is_file():
        for relative in generated:
            target = workspace / relative
            if target.is_dir():
                shutil.rmtree(target)
            elif target.exists():
                target.unlink()
    elif any((workspace / relative).exists() for relative in generated):
        raise RuntimeError("指定工作区不是空目录且不是本驱动创建的夹具；为避免删除用户文件而停止")
    case.builder(workspace)
    (workspace / "deliverables").mkdir(parents=True, exist_ok=True)
    binary = workspace / ".office-tools-bin.exe" if os.name == "nt" else workspace / ".office-tools-bin"
    build_binary(binary)
    operations: list[dict[str, Any]] = []
    failures: list[str] = []
    source_docx = workspace / "templates" / "security-report-template.docx"
    source_xlsx = workspace / "templates" / "remediation-data-template.xlsx"
    source_pptx = workspace / "templates" / "review-template.pptx"
    hashes_before = {path.name: sha256_file(path) for path in (source_docx, source_xlsx, source_pptx)}
    client = MCPProcess(workspace, binary)
    try:
        capability = call_checked(client, operations, "office_capabilities", {})
        atomic = call_checked(
            client,
            operations,
            "fill_office_template",
            {
                "source_path": "templates/security-report-template.docx",
                "output_path": "deliverables/atomic-failure.docx",
                "expected_source_sha256": hashes_before[source_docx.name],
                "replacements": [
                    {"old_text": "{{CLIENT_NAME}}", "new_text": "星河科技"},
                    {"old_text": "{{INTENTIONALLY_MISSING_TOKEN}}", "new_text": "不应写入"},
                ],
                "overwrite": False,
            },
        )
        if not atomic["is_error"] or (workspace / "deliverables" / "atomic-failure.docx").exists():
            failures.append("atomic batch failure did not reject the whole write")
        hashes_after_atomic = {path.name: sha256_file(path) for path in (source_docx, source_xlsx, source_pptx)}
        if hashes_after_atomic != hashes_before:
            failures.append("template hash changed after rejected atomic batch")

        docx = call_checked(
            client,
            operations,
            "fill_office_template",
            {
                "source_path": "templates/security-report-template.docx",
                "output_path": "deliverables/report.docx",
                "expected_source_sha256": hashes_before[source_docx.name],
                "replacements": [
                    {"old_text": "{{REPORT_TITLE}}", "new_text": "Alpha 平台安全整改交付报告"},
                    {"old_text": "{{CLIENT_NAME}}", "new_text": "星河科技"},
                    {"old_text": "{{REPORT_DATE}}", "new_text": "2026-08-18"},
                    {"old_text": "{{EXECUTIVE_SUMMARY}}", "new_text": "已完成两项有证据支持的整改，并通过公开与隐藏复测。"},
                    {"old_text": "{{FINDING_1_ID}}", "new_text": "SEC-001"},
                    {"old_text": "{{FINDING_1_SEVERITY}}", "new_text": "高"},
                    {"old_text": "{{FINDING_1_EVIDENCE}}", "new_text": "授权检查默认允许未知角色"},
                    {"old_text": "{{FINDING_1_RECOMMENDATION}}", "new_text": "未知角色改为默认拒绝并补充回归测试"},
                    {"old_text": "{{FINDING_2_ID}}", "new_text": "SEC-002"},
                    {"old_text": "{{FINDING_2_SEVERITY}}", "new_text": "中"},
                    {"old_text": "{{FINDING_2_EVIDENCE}}", "new_text": "审计日志记录完整令牌"},
                    {"old_text": "{{FINDING_2_RECOMMENDATION}}", "new_text": "仅保留令牌哈希前八位"},
                    {"old_text": "{{REMEDIATION_SUMMARY}}", "new_text": "2 项整改已完成，独立复测通过。"},
                    {"old_text": "{{NEXT_ACTIONS}}", "new_text": "保持变更记录，按周期复核授权边界。"},
                ],
            },
        )
        if docx["is_error"]:
            failures.append(f"DOCX template fill failed: {payload_error(docx)}")
        xlsx = call_checked(
            client,
            operations,
            "fill_office_template",
            {
                "source_path": "templates/remediation-data-template.xlsx",
                "output_path": "deliverables/data.xlsx",
                "expected_source_sha256": hashes_before[source_xlsx.name],
                "replacements": [
                    {"old_text": "{{CLIENT_NAME}}", "new_text": "星河科技"},
                    {"old_text": "{{FINDING_1_ID}}", "new_text": "SEC-001"},
                    {"old_text": "{{FINDING_1_SEVERITY}}", "new_text": "高"},
                    {"old_text": "{{FINDING_1_EVIDENCE}}", "new_text": "授权检查默认允许未知角色"},
                    {"old_text": "{{FINDING_1_RECOMMENDATION}}", "new_text": "未知角色改为默认拒绝并补充回归测试"},
                    {"old_text": "{{FINDING_1_STATUS}}", "new_text": "已完成"},
                    {"old_text": "{{FINDING_2_ID}}", "new_text": "SEC-002"},
                    {"old_text": "{{FINDING_2_SEVERITY}}", "new_text": "中"},
                    {"old_text": "{{FINDING_2_EVIDENCE}}", "new_text": "审计日志记录完整令牌"},
                    {"old_text": "{{FINDING_2_RECOMMENDATION}}", "new_text": "仅保留令牌哈希前八位"},
                    {"old_text": "{{FINDING_2_STATUS}}", "new_text": "已完成"},
                ],
            },
        )
        if xlsx["is_error"]:
            failures.append(f"XLSX template fill failed: {payload_error(xlsx)}")
        pptx = call_checked(
            client,
            operations,
            "fill_office_template",
            {
                "source_path": "templates/review-template.pptx",
                "output_path": "deliverables/review.pptx",
                "expected_source_sha256": hashes_before[source_pptx.name],
                "replacements": [
                    {"old_text": "{{PRESENTATION_TITLE}}", "new_text": "星河科技 · Alpha 平台安全整改交付"},
                    {"old_text": "{{PRESENTATION_SUBTITLE}}", "new_text": "授权范围内的整改、复测与后续行动"},
                    {"old_text": "{{REMEDIATION_SUMMARY}}", "new_text": "两项整改已完成，独立复测通过。"},
                    {"old_text": "{{NEXT_ACTIONS}}", "new_text": "保持变更记录，按周期复核授权边界。"},
                ],
            },
        )
        if pptx["is_error"]:
            failures.append(f"PPTX template fill failed: {payload_error(pptx)}")
        for path in ("deliverables/report.docx", "deliverables/data.xlsx", "deliverables/review.pptx"):
            result = call_checked(client, operations, "inspect_office_document", {"path": path, "max_text_chars": 20000})
            if result["is_error"]:
                failures.append(f"inspect failed for {path}: {payload_error(result)}")

        validations = [
            ("deliverables/report.docx", {"required_text": ["星河科技", "SEC-001", "SEC-002"], "source_path": "templates/security-report-template.docx", "require_source_fidelity": True, "allowed_source_text": ["Alpha 平台安全整改交付报告", "星河科技", "2026-08-18", "已完成两项有证据支持的整改，并通过公开与隐藏复测。", "SEC-001 / SEC-002", "高 / 中", "授权检查默认拒绝未知角色；审计日志只保留令牌哈希前八位。", "补充授权回归并完成独立复测。"]}),
            ("deliverables/data.xlsx", {"required_text": ["星河科技"], "require_formulas": True, "require_auto_filter": True, "require_frozen_rows": True, "require_print_setup": True}),
            ("deliverables/review.pptx", {"required_text": ["星河科技", "整改结果"], "min_slides": 3}),
        ]
        for path, options in validations:
            result = call_checked(client, operations, "validate_office_document", {"path": path, **options})
            validation_payload = result.get("payload") or {}
            if result["is_error"] or not bool(validation_payload.get("deliverable", validation_payload.get("valid", False))):
                failures.append(f"validation failed for {path}: {payload_error(result)}")

        pdf = call_checked(
            client,
            operations,
            "export_office_pdf",
            {"path": "deliverables/report.docx", "output_path": "deliverables/report.pdf", "converter": "auto", "overwrite": True},
        )
        if pdf["is_error"]:
            failures.append(f"PDF export blocked: {payload_error(pdf)}")
        preview = call_checked(
            client,
            operations,
            "render_office_preview",
            {"path": "deliverables/report.docx", "output_dir": "deliverables/preview", "max_pages": 20, "width": 1280, "height": 960},
        )
        if preview["is_error"]:
            failures.append(f"preview blocked: {payload_error(preview)}")
    finally:
        client.close()
        if not keep_binary:
            if not remove_file_with_retry(binary):
                failures.append(f"office_tools binary could not be removed after bounded retries: {binary.name}")

    return {
        "passed": not failures,
        "case_id": case.id,
        "capabilities": capability.get("payload"),
        "operations": operations,
        "failures": failures,
        "source_hashes_unchanged": hashes_before == {path.name: sha256_file(path) for path in (source_docx, source_xlsx, source_pptx)},
        "deliverables": [str(path.relative_to(workspace)) for path in sorted((workspace / "deliverables").rglob("*")) if path.is_file()],
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path)
    parser.add_argument("--output-report", type=Path)
    parser.add_argument("--keep-binary", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    temporary: tempfile.TemporaryDirectory[str] | None = None
    if args.workspace:
        workspace = args.workspace.resolve()
        workspace.mkdir(parents=True, exist_ok=True)
    else:
        temporary = tempfile.TemporaryDirectory(prefix="p7-office-")
        workspace = Path(temporary.name)
    try:
        report = run_case(workspace, keep_binary=args.keep_binary)
        output = args.output_report or (workspace / "office-mcp-acceptance.json")
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({"passed": report["passed"], "report": str(output), "failures": report["failures"]}, ensure_ascii=False, indent=2))
        return 0 if report["passed"] else 1
    finally:
        if temporary:
            temporary.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
