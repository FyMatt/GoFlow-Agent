#!/usr/bin/env python3
"""受控漏洞正例 / 误报验收。

构造两个独立仓库：一个包含真实注入、路径穿越和硬编码密钥，另一个是等价但已修复的
版本。为每个仓库单独启动一个 GoFlow 服务（避免影响当前工作区），用同一个安全审计
工作流跑一次，检查：

1. 正例仓库：审计报告必须指出真实问题（注入 / 路径穿越 / 硬编码密钥中至少两类），
   并引用实际文件；
2. 负例仓库：审计结论必须为通过或"未发现"，且不得把正常代码报成漏洞。

用法：
    python scripts/security_detection_acceptance.py
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "scripts"))

from real_provider_acceptance import (  # noqa: E402
    drive_run,
    free_port,
    start_server,
    stop_server,
    wait_for_server,
)

VULNERABLE_APP = '''"""小型示例服务（含刻意保留的受控漏洞，仅用于审计验收）。"""
import os
import sqlite3

SECRET_KEY = "hardcoded-demo-secret-0123456789"

BASE_DIR = os.path.dirname(os.path.abspath(__file__))


def get_user(connection, username):
    cursor = connection.cursor()
    cursor.execute("SELECT * FROM users WHERE name = '%s'" % username)
    return cursor.fetchone()


def read_file(name):
    return open(os.path.join(BASE_DIR, name)).read()


def lookup_user(connection, username):
    cursor = connection.cursor()
    cursor.execute("SELECT * FROM users WHERE name = ?", (username,))
    return cursor.fetchone()
'''

FIXED_APP = '''"""小型示例服务（修复版本，仅用于误报验收）。"""
import os
import sqlite3

SECRET_KEY = os.environ.get("APP_SECRET_KEY", "")

BASE_DIR = os.path.dirname(os.path.abspath(__file__))


def get_user(connection, username):
    cursor = connection.cursor()
    cursor.execute("SELECT * FROM users WHERE name = ?", (username,))
    return cursor.fetchone()


def read_file(name):
    path = os.path.join(BASE_DIR, "notes", os.path.basename(name))
    return open(path).read()


def lookup_user(connection, username):
    return get_user(connection, username)
'''

README = "# demo-service\n\n受控安全审计验收样例。\n"

VULN_PROMPT = (
    "在明确授权的本地示例仓库中做一次只读安全审计：审计 app.py，指出真实存在的注入、路径穿越和硬编码密钥问题，"
    "给出文件、函数与修复建议，并把完整报告写入 deliverables/security-report.md。不要修改业务代码。"
)
BENIGN_PROMPT = (
    "在明确授权的本地示例仓库中做一次只读安全审计：审计 app.py，确认是否存在注入、路径穿越和硬编码密钥问题，"
    "并把结论写入 deliverables/security-report.md。不要修改业务代码。"
)

VULN_KEYWORDS = {
    "injection": ["注入", "sql inject"],
    "traversal": ["路径穿越", "目录穿越", "path traversal"],
    "secret": ["硬编码", "hardcoded", "密钥"],
}


def write_fixture(root: Path, app_source: str) -> Path:
    root.mkdir(parents=True, exist_ok=True)
    (root / "app.py").write_text(app_source, encoding="utf-8")
    (root / "README.md").write_text(README, encoding="utf-8")
    (root / "deliverables").mkdir(exist_ok=True)
    (root / "deliverables" / "security-report.md").write_text(
        "# 安全审计报告\n\n待补充。\n", encoding="utf-8"
    )
    return root


def build_binary(destination: Path) -> None:
    subprocess.run(
        ["go", "build", "-o", str(destination), "./cmd/goflow"],
        cwd=str(ROOT),
        check=True,
    )


def usage_summary(body: dict[str, Any]) -> dict[str, Any]:
    records = body.get("usage_records") or []
    prompt = sum(int(record.get("prompt_tokens") or 0) for record in records)
    cached = sum(int(record.get("cached_tokens") or 0) for record in records)
    output = sum(int(record.get("output_tokens") or 0) for record in records)
    return {
        "calls": len(records),
        "prompt_tokens": prompt,
        "cached_tokens": cached,
        "output_tokens": output,
        "miss_tokens": prompt - cached,
    }


def evaluate_vulnerable(report: str) -> dict[str, Any]:
    lowered = report.lower()
    matched = {name: any(term.lower() in lowered for term in terms) for name, terms in VULN_KEYWORDS.items()}
    return {
        "mentions_app": "app.py" in lowered,
        "matched": matched,
        "matched_count": sum(matched.values()),
        "passed": "app.py" in lowered and sum(matched.values()) >= 2,
    }


NEGATIONS = ("未发现", "不存在", "未复现", "未确认", "未检测到", "无漏洞", "均无", "无需", "不属于", "加固", "建议", "假设")


def evaluate_benign(report: str) -> dict[str, Any]:
    """负例通过条件：结论为未发现，且没有把正常代码定为中高危漏洞。

    只把带严重度（高/中）的发现行视为误报；加固建议、假设级残余模式和信息级
    备注不算误报，否则会把合规报告误判为幻觉。
    """
    false_positives = []
    for line in report.splitlines():
        stripped = line.strip()
        if not stripped or any(negative in stripped for negative in NEGATIONS):
            continue
        if not re.search(r"F-\d+|发现|漏洞", stripped):
            continue
        if not re.search(r"(注入|穿越|硬编码|密钥)", stripped):
            continue
        if re.search(r"(高|中)(危|风险|严重度)?", stripped):
            false_positives.append(stripped[:160])
    says_not_vulnerable = any(
        term in report for term in ("未发现", "不存在", "未检测到", "均未确认", "总体结论：通过")
    )
    return {
        "says_not_vulnerable": says_not_vulnerable,
        "false_positive_claim": bool(false_positives),
        "false_positive_lines": false_positives,
        "passed": says_not_vulnerable and not false_positives,
    }


def run_case(
    binary: Path,
    workspace: Path,
    prompt: str,
    timeout: float,
    log_dir: Path,
) -> dict[str, Any]:
    port = free_port()
    environment = os.environ.copy()
    environment["GOFLOW_DATA_ROOT"] = str(ROOT)
    log_path = log_dir / f"{workspace.name}-server.log"
    process, log_handle = start_server(binary, workspace, port, environment, log_path)
    base_url = f"http://127.0.0.1:{port}"
    try:
        wait_for_server(base_url, process)
        import urllib.request

        request = urllib.request.Request(
            base_url + "/api/runs",
            data=json.dumps(
                {
                    "type": "workflow",
                    "workflow": "security-audit-evidence-gate",
                    "input": prompt,
                    "background": True,
                }
            ).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(request, timeout=60) as response:
            body = json.loads(response.read().decode("utf-8"))
        run_id = body.get("run_id") or body.get("id")
        final, approvals, _ = drive_run(base_url, "workflow", str(run_id), timeout, max_auto_retries=1)
        final["approvals"] = approvals
        return {"run_id": run_id, "final": final}
    finally:
        stop_server(process, log_handle)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace-root", default=str(ROOT / ".artifacts" / "security-detection"))
    parser.add_argument("--timeout", type=float, default=1500.0)
    parser.add_argument("--case", choices=["vulnerable", "benign"], action="append", default=None,
                        help="only run the selected case(s); default runs both")
    args = parser.parse_args()

    stamp = time.strftime("%Y%m%d-%H%M%S")
    root = Path(args.workspace_root).resolve() / stamp
    log_dir = root / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    binary = root / ("goflow-accept.exe" if os.name == "nt" else "goflow-accept")
    build_binary(binary)

    vulnerable = write_fixture(root / "vulnerable", VULNERABLE_APP)
    benign = write_fixture(root / "benign", FIXED_APP)

    results: dict[str, Any] = {"stamp": stamp, "cases": {}}
    selected = set(args.case or ["vulnerable", "benign"])
    for name, workspace, prompt, evaluator in (
        ("vulnerable", vulnerable, VULN_PROMPT, evaluate_vulnerable),
        ("benign", benign, BENIGN_PROMPT, evaluate_benign),
    ):
        if name not in selected:
            continue
        outcome = run_case(binary, workspace, prompt, args.timeout, log_dir)
        final = outcome["final"]
        report_path = workspace / "deliverables" / "security-report.md"
        report = report_path.read_text(encoding="utf-8") if report_path.is_file() else ""
        evaluation = evaluator(report)
        results["cases"][name] = {
            "run_id": outcome["run_id"],
            "status": final.get("status"),
            "usage": usage_summary(final),
            "approvals": len(final.get("approvals") or []),
            "report_path": str(report_path),
            "report_bytes": len(report.encode("utf-8")),
            "evaluation": evaluation,
        }

    results["passed"] = all(item["evaluation"]["passed"] for item in results["cases"].values())
    print(json.dumps(results, ensure_ascii=False, indent=2))
    return 0 if results["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
