#!/usr/bin/env python3
"""Optional browser smoke test for the embedded Web Studio.

This script starts GoFlow HTTP mode with a temporary runtime and opens the
Studio pages in a locally installed headless Chrome/Edge/Chromium binary. It
uses no third-party Python packages. When no browser binary is found, the script
skips by default; pass --required to make that a failure in CI.
"""

from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import json
import os
import re
import secrets
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import textwrap
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
import zlib
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SMOKE_LAZY_TIMELINE_MARKER = "SMOKE_LAZY_TIMELINE_DETAIL_LOADED"
WINDOWS_EDGE_SANDBOX_FLAGS = [
    "--disable-features=RendererCodeIntegrity,NetworkServiceSandbox,CalculateNativeWinOcclusion,EdgeStartupBoost",
    "--disable-component-update",
    "--disable-sync",
    "--disable-domain-reliability",
    "--disable-client-side-phishing-detection",
    "--disable-default-apps",
    "--metrics-recording-only",
]


class BrowserSmokeUnavailable(RuntimeError):
    """Raised when a browser exists but cannot run in this host session."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=0, help="HTTP port to use. Defaults to a free local port.")
    parser.add_argument("--timeout", type=float, default=45.0, help="Seconds to wait for startup and page rendering.")
    parser.add_argument("--browser", type=Path, default=None, help="Path to Chrome, Edge, or Chromium.")
    parser.add_argument("--required", action="store_true", help="Fail when no usable browser is found.")
    parser.add_argument("--skip-build", action="store_true", help="Use an existing --binary instead of building.")
    parser.add_argument("--binary", type=Path, default=None, help="Path to an existing goflow binary.")
    parser.add_argument("--target-only", action="store_true", help="Run only the execution Target browser flow and its screenshots.")
    parser.add_argument("--staged-delivery-only", action="store_true", help="Run only the staged delivery task-page flow and screenshots.")
    parser.add_argument("--baseline-only", action="store_true", help="Capture the task-page baseline and exit after startup.")
    parser.add_argument("--baseline-output", type=Path, default=None, help="Write reproducible startup and task-page metrics as JSON.")
    parser.add_argument(
        "--enforce-performance",
        action="store_true",
        help="Prewarm the binary and fail when hot startup, task readiness, or common interaction response exceeds V3 gates.",
    )
    parser.add_argument(
        "--performance-only",
        action="store_true",
        help="Use a fresh workspace with no persisted run and execute only the V3 performance baseline.",
    )
    parser.add_argument(
        "--screenshot-dir",
        type=Path,
        default=None,
        help="Also capture desktop and 390px mobile screenshots of the four primary Studio pages.",
    )
    return parser.parse_args()


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def run_checked(args: list[str], env: dict[str, str]) -> None:
    completed = subprocess.run(args, cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if completed.returncode != 0:
        raise AssertionError(f"{' '.join(args)} failed with {completed.returncode}\n{completed.stdout}")


def run_go_build_checked(args: list[str], env: dict[str, str]) -> None:
    last_output = ""
    for attempt in range(1, 4):
        completed = subprocess.run(args, cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        last_output = completed.stdout
        if completed.returncode == 0:
            return
        if "Access is denied" not in last_output or attempt == 3:
            break
        time.sleep(0.8 * attempt)
    raise AssertionError(f"{' '.join(args)} failed with build retries\n{last_output}")


def request_text(url: str, timeout: float = 5.0) -> tuple[int, str]:
    request = urllib.request.Request(url, headers={"User-Agent": "goflow-browser-smoke/1"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", errors="replace")


def request_json(
    url: str,
    *,
    method: str = "GET",
    body: dict[str, object] | None = None,
    timeout: float = 5.0,
) -> tuple[int, dict[str, object]]:
    data = json.dumps(body).encode("utf-8") if body is not None else None
    request = urllib.request.Request(
        url,
        data=data,
        method=method,
        headers={"User-Agent": "goflow-browser-smoke/1", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = json.loads(response.read().decode("utf-8", errors="replace") or "{}")
            return response.status, payload if isinstance(payload, dict) else {"items": payload}
    except urllib.error.HTTPError as exc:
        payload = json.loads(exc.read().decode("utf-8", errors="replace") or "{}")
        return exc.code, payload if isinstance(payload, dict) else {"items": payload}


def create_smoke_execution_target(base_url: str, target_id: str, *, enabled: bool) -> None:
    status, payload = request_json(
        f"{base_url}/api/targets",
        method="POST",
        body={
            "id": target_id,
            "kind": "ssh",
            "display_name": "测试 SSH 主机" if enabled else "已禁用 SSH 主机",
            "host": "192.0.2.40" if enabled else "192.0.2.41",
            "port": 22,
            "user": "operator",
            "credential_ref": f"secret://targets/{target_id}/private_key",
            "host_key_sha256": "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
            "allowed_commands": ["uptime", "df -h"],
            "enabled": enabled,
        },
    )
    if status != 201 or payload.get("id") != target_id:
        raise AssertionError(f"create smoke execution target failed: status={status} payload={payload}")


def assert_asset_contains(base_url: str, path: str, needles: list[str]) -> None:
    status, body = request_text(f"{base_url}{path}")
    if status != 200:
        raise AssertionError(f"{path} returned {status}: {body[:500]}")
    for needle in needles:
        if needle not in body:
            raise AssertionError(f"{path} missing asset marker {needle!r}")


def build_binary(tmpdir: Path, env: dict[str, str]) -> Path:
    suffix = ".exe" if os.name == "nt" else ""
    binary = tmpdir / f"goflow-browser-smoke{suffix}"
    run_go_build_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], env)
    return binary


def write_smoke_runtime_config(runtime_home: Path) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir_path = runtime_home / "skills"
    smoke_skill_dir = skill_dir_path / "smoke"
    smoke_skill_dir.mkdir(parents=True, exist_ok=True)
    (smoke_skill_dir / "SKILL.md").write_text(
        textwrap.dedent(
            """
            ---
            name: smoke
            description: 用于浏览器冒烟测试的最小技能。
            ---

            仅在本地浏览器冒烟测试中使用此占位技能。
            """
        ).lstrip(),
        encoding="utf-8",
    )
    config_text = textwrap.dedent(
        f"""
        agent:
          name: 浏览器冒烟测试
          max_iterations: 4
          timeout: 30s

        default_agent: chat

        providers:
          primary:
            provider: openai-compatible
            base_url: ${{GOFLOW_BASE_URL}}
            api_key: ${{GOFLOW_API_KEY}}
            model: ${{GOFLOW_MODEL}}
            timeout: 10s
            temperature: 0
            max_tokens: 128
            retry_count: 0
            retry_backoff: 1s
          backup:
            provider: openai-compatible
            base_url: ${{GOFLOW_BACKUP_BASE_URL}}
            api_key: ${{GOFLOW_BACKUP_API_KEY}}
            model: ${{GOFLOW_BACKUP_MODEL}}
            timeout: 10s
            temperature: 0
            max_tokens: 128
            retry_count: 0
            retry_backoff: 1s

        agents:
          chat:
            name: 浏览器测试智能体
            description: 用于浏览器冒烟测试的对话智能体。
            provider: primary
            mode: chat
            tool_policy: confirm
            allowed_tool_kinds: [read, network]
            max_iterations: 4
          auditor:
            name: 浏览器质量验证智能体
            description: 用于浏览器冒烟测试的质量验证智能体。
            provider: primary
            mode: audit
            tool_policy: confirm
            allowed_tool_kinds: [read]
            max_iterations: 2

        mcp_servers:
          - name: smoke_tools
            command: {json.dumps(sys.executable)}
            args: [{json.dumps(str(ROOT / "mcp_servers" / "python_notes.py"))}]
            enabled: true
            timeout: 30s
            workdir: {json.dumps(str(ROOT))}
            env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
            isolation: none
            allowed_commands: [{json.dumps(sys.executable)}]

        skill:
          directory: {json.dumps(str(skill_dir_path))}
          match_threshold: 1
          hot_reload: false

        audit:
          enabled: true
          redact_content: false
          show_trace_in_cli: false

        verifier:
          enabled: false
          agent: auditor
          modes: [fix, audit]
          max_tokens: 128

        tool_risk_policy:
          require_approval_for_unsandboxed_risky_tools: true
          disable_remember_for_unsandboxed_risky_tools: true
          reject_unsandboxed_risky_tools: false

        session:
          max_history: 4

        log:
          level: info
          format: text
        """
    ).lstrip()
    config_path = config_dir / "goflow.yaml"
    config_path.write_text(config_text, encoding="utf-8")
    return config_path


def write_smoke_workflow_graph(runtime_home: Path) -> None:
    workflow_dir = runtime_home / "workflows" / "plan-implement-audit"
    workflow_dir.mkdir(parents=True, exist_ok=True)
    (workflow_dir / "workflow.yaml").write_text(
        textwrap.dedent(
            """
            name: plan-implement-audit
            description: 用于验证节点预算恢复操作的浏览器冒烟测试工作流。
            budget:
              hard_prompt_tokens: 240
            stages:
              - name: plan
                node_type: skill
                agent: planner
                skill: execution-plan
                outputs:
                  plan: result.output
                  summary: result.summary
                next:
                  - quality
              - name: quality
                node_type: quality_gate
                next:
                  - audit
              - name: audit
                node_type: skill
                agent: auditor
                skill: code-audit
            """
        ).lstrip(),
        encoding="utf-8",
    )
    contract_dir = runtime_home / "workflows" / "smoke-contract-workflow"
    contract_dir.mkdir(parents=True, exist_ok=True)
    (contract_dir / "workflow.yaml").write_text(
        textwrap.dedent(
            """
            name: smoke-contract-workflow
            description: 用于验证契约失败和模型升级诊断的浏览器冒烟测试工作流。
            stages:
              - name: plan
                node_type: skill
                agent: planner
                skill: execution-plan
                params:
                  contract_required: "true"
                  on_contract_fail: block
                  require_json_output: "true"
                  required_json_keys: summary
                  escalate_model: strong-smoke-model
                outputs:
                  summary: result.summary
                next:
                  - audit
              - name: audit
                node_type: skill
                agent: auditor
                skill: code-audit
            """
        ).lstrip(),
        encoding="utf-8",
    )
    complex_dir = runtime_home / "workflows" / "complex-project-delivery"
    complex_dir.mkdir(parents=True, exist_ok=True)
    (complex_dir / "workflow.yaml").write_text(
        textwrap.dedent(
            """
            name: complex-project-delivery
            description: 用于验证复杂交付阶段运行状态展示的浏览器冒烟测试工作流。
            stages:
              - name: start
                node_type: start
                next: [plan]
                position: {x: 60, y: 240}
              - name: plan
                node_type: agent
                agent: planner
                skill: execution-plan
                outputs:
                  plan: result.output
                next: [approve-plan]
                position: {x: 340, y: 240}
              - name: approve-plan
                node_type: checkpoint
                next: [implement]
                position: {x: 640, y: 240}
              - name: implement
                node_type: agent
                agent: fixer
                skill: code-writing
                outputs:
                  implementation: result.output
                  changed_files: result.changed_files
                next: [verify-split]
                position: {x: 940, y: 240}
              - name: verify-split
                node_type: parallel
                params:
                  concurrent: true
                next: [test, lint]
                position: {x: 1240, y: 240}
              - name: test
                node_type: skill
                agent: auditor
                skill: code-audit
                outputs:
                  test_report: result.output
                next: [verify-join]
                position: {x: 1540, y: 120}
              - name: lint
                node_type: skill
                agent: planner
                skill: code-audit
                outputs:
                  lint_report: result.output
                next: [verify-join]
                position: {x: 1540, y: 360}
              - name: verify-join
                node_type: join
                params:
                  wait_for: test,lint
                next: [audit]
                position: {x: 1840, y: 240}
              - name: audit
                node_type: skill
                agent: auditor
                skill: code-audit
                outputs:
                  audit: result.output
                next: [quality]
                position: {x: 2140, y: 240}
              - name: quality
                node_type: quality_gate
                params:
                  stages: implement,test,lint,audit
                routes:
                  pass: final-report
                  warning: recovery
                  fail: recovery
                position: {x: 2440, y: 240}
              - name: recovery
                node_type: skill
                agent: fixer
                skill: execution-plan
                outputs:
                  recovery: result.output
                next: [final-report]
                position: {x: 2740, y: 380}
              - name: final-report
                node_type: skill
                agent: planner
                skill: execution-plan
                outputs:
                  final_report: result.output
                next: [end]
                position: {x: 3040, y: 240}
              - name: end
                node_type: end
                position: {x: 3340, y: 240}
            """
        ).lstrip(),
        encoding="utf-8",
    )
    conflict_dir = runtime_home / "workflows" / "smoke-parallel-conflict"
    conflict_dir.mkdir(parents=True, exist_ok=True)
    (conflict_dir / "workflow.yaml").write_text(
        textwrap.dedent(
            """
            name: smoke-parallel-conflict
            description: 用于验证并行文件冲突诊断的浏览器冒烟测试工作流。
            stages:
              - name: split
                node_type: parallel
                params:
                  concurrent: true
                next:
                  - implement
                  - audit
              - name: implement
                node_type: skill
                agent: fixer
                skill: code-writing
                next:
                  - join
              - name: audit
                node_type: skill
                agent: auditor
                skill: code-audit
                next:
                  - join
              - name: join
                node_type: join
                params:
                  wait_for: implement,audit
                next:
                  - report
              - name: report
                node_type: skill
                agent: planner
                skill: execution-plan
            """
        ).lstrip(),
        encoding="utf-8",
    )
    live_readiness_dir = runtime_home / "workflows" / "smoke-live-readiness-workflow"
    live_readiness_dir.mkdir(parents=True, exist_ok=True)
    (live_readiness_dir / "workflow.yaml").write_text(
        textwrap.dedent(
            """
            name: smoke-live-readiness-workflow
            description: 用于验证实时执行就绪诊断的浏览器冒烟测试工作流。
            stages:
              - name: apply-live
                node_type: tool
                agent: operations-specialist
                skill: execution-plan
                tool: network_tools/device_restconf_live_apply
                approval: true
                execution:
                  mode: live
                  risk_level: high
                  boundary: production-restconf-change
                  requires_approval: true
                  requires_authorized_scope: true
                  requires_rollback: true
                  requires_credential_ref: true
                  requires_allowlist: true
                  allow_live_tools:
                    - network_tools/device_restconf_live_apply
                  required_params:
                    - approval_ref
                    - change_ticket
                    - allowed_hosts
                    - allowed_paths
                params:
                  host: router-edge-01.example.com
                  endpoint: https://router-edge-01.example.com/restconf/data/native/interface
                  method: PATCH
                position: {x: 280, y: 240}
            """
        ).lstrip(),
        encoding="utf-8",
    )


def write_smoke_artifact(workspace: Path) -> dict:
    content = "# Smoke artifact\n\nFull artifact body for memory viewer."
    encoded = content.encode("utf-8")
    digest = hashlib.sha256(encoded).hexdigest()
    ref = f"sha256:{digest}"
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    artifact_dir = workspace / ".goflow" / "artifacts"
    objects_dir = artifact_dir / "objects"
    objects_dir.mkdir(parents=True, exist_ok=True)
    object_path = objects_dir / f"{digest}.json.gz"
    obj = {
        "ref": ref,
        "hash": digest,
        "created_at": now,
        "updated_at": now,
        "mime": "text/markdown",
        "summary": "Smoke artifact summary",
        "content": content,
        "size": len(encoded),
        "stored_bytes": 0,
        "kind": "smoke_report",
        "title": "Smoke Artifact Report",
        "metadata": {"source": "smoke"},
    }
    with gzip.open(object_path, "wt", encoding="utf-8") as file:
        json.dump(obj, file, indent=2)
        file.write("\n")
    stored_bytes = object_path.stat().st_size
    metadata = {key: value for key, value in obj.items() if key != "content"}
    metadata["stored_bytes"] = stored_bytes
    (artifact_dir / "index.json").write_text(
        json.dumps({"updated_at": now, "objects": [metadata]}, indent=2) + "\n",
        encoding="utf-8",
    )
    return {"hash": digest, "ref": ref, "content": content, "title": obj["title"]}


def write_smoke_memory(workspace: Path) -> None:
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    memory_dir = workspace / ".goflow" / "memory"
    memory_dir.mkdir(parents=True, exist_ok=True)
    (memory_dir / "solutions.json").write_text(
        json.dumps(
            {
                "updated_at": now,
                "solutions": [
                    {
                        "id": "sol-smoke-active",
                        "created_at": now,
                        "updated_at": now,
                        "last_used_at": now,
                        "problem_signature": "Smoke provider retry decision",
                        "problem": "Smoke run needs a reusable retry decision",
                        "decision": "Reuse the verified smoke retry path",
                        "solution": "Prefer the active retry decision before asking again",
                        "applicability": ["same smoke retry signature"],
                        "invalid_when": ["provider retry contract changes"],
                        "verification_command": "go test ./internal/memory",
                        "confidence": "high",
                        "resolved": True,
                        "use_count": 2,
                    },
                    {
                        "id": "sol-smoke-old",
                        "created_at": now,
                        "updated_at": now,
                        "retired_at": now,
                        "problem_signature": "Old smoke retry decision",
                        "decision": "Use the old retry path",
                        "solution": "Old retry path kept for audit",
                        "verification_command": "go test ./internal/memory",
                        "confidence": "medium",
                        "resolved": True,
                        "retired": True,
                        "retired_reason": "Superseded in smoke fixture",
                        "superseded_by": "sol-smoke-active",
                    },
                ],
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )


def write_smoke_task_staging(runtime_home: Path, workspace: Path, run_id: str) -> None:
    source = workspace / "staged-delivery.txt"
    source.write_text("before\n", encoding="utf-8")
    task_root = runtime_home / "task-staging" / run_id
    input_root = task_root / "input"
    task_workspace = task_root / "workspace"
    metadata = task_root / "metadata"
    input_root.mkdir(parents=True, exist_ok=True)
    task_workspace.mkdir(parents=True, exist_ok=True)
    metadata.mkdir(parents=True, exist_ok=True)
    input_file = input_root / source.name
    staged_file = task_workspace / source.name
    input_file.write_text("before\n", encoding="utf-8")
    staged_file.write_text("after\n", encoding="utf-8")
    baseline_data = input_file.read_bytes()
    baseline = {
        "schema_version": "goflow.task-staging.v1",
        "run_id": run_id,
        "workspace_root": str(workspace.resolve()),
        "staging_root": str(task_workspace.resolve()),
        "created_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "files": [
            {
                "path": source.name,
                "sha256": hashlib.sha256(baseline_data).hexdigest(),
                "size": len(baseline_data),
                "mode": input_file.stat().st_mode & 0o777,
            }
        ],
        "strategy": "copy",
    }
    (metadata / "baseline.json").write_text(json.dumps(baseline, indent=2) + "\n", encoding="utf-8")


def write_smoke_office_fixture(workspace: Path) -> dict:
    delivery_dir = workspace / "deliverables"
    preview_dir = workspace / "previews"
    delivery_dir.mkdir(parents=True, exist_ok=True)
    preview_dir.mkdir(parents=True, exist_ok=True)
    artifact_path = delivery_dir / "smoke-security-report.docx"
    preview_path = preview_dir / "smoke-security-report-page-1.png"

    with zipfile.ZipFile(artifact_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr(
            "[Content_Types].xml",
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
            '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
            '<Default Extension="xml" ContentType="application/xml"/>'
            '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>'
            '</Types>',
        )
        archive.writestr(
            "_rels/.rels",
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>'
            '</Relationships>',
        )
        archive.writestr(
            "word/document.xml",
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
            '<w:body><w:p><w:r><w:t>Smoke security report</w:t></w:r></w:p><w:sectPr/></w:body>'
            '</w:document>',
        )

    width, height = 96, 64
    rows = bytearray()
    for y in range(height):
        rows.append(0)
        for x in range(width):
            if 10 <= x < 86 and 10 <= y < 18:
                rows.extend((33, 37, 41))
            elif 10 <= x < 72 and y in (28, 36, 44):
                rows.extend((13, 148, 136))
            else:
                rows.extend((248, 249, 250))

    def png_chunk(kind: bytes, payload: bytes) -> bytes:
        checksum = zlib.crc32(kind + payload) & 0xFFFFFFFF
        return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", checksum)

    preview_path.write_bytes(
        b"\x89PNG\r\n\x1a\n"
        + png_chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
        + png_chunk(b"IDAT", zlib.compress(bytes(rows), 9))
        + png_chunk(b"IEND", b"")
    )
    return {
        "artifact_path": artifact_path.relative_to(workspace).as_posix(),
        "preview_paths": [preview_path.relative_to(workspace).as_posix()],
        "mime": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        "sha256": hashlib.sha256(artifact_path.read_bytes()).hexdigest(),
    }


def write_smoke_session(workspace: Path) -> dict:
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    run_id = "run-agent-smoke-lazy"
    workflow_run_id = "wf-smoke-budget-diagnostics"
    blocked_workflow_run_id = "wf-smoke-contract-blocked"
    model_escalated_workflow_run_id = "wf-smoke-model-escalated"
    complex_delivery_run_id = "wf-smoke-complex-delivery"
    quality_workflow_run_id = "wf-smoke-quality-gate-failed"
    parallel_conflict_workflow_run_id = "wf-smoke-parallel-file-conflict"
    budget_approval_run_id = "wf-smoke-budget-approval"
    execution_readiness_run_id = "wf-smoke-execution-readiness-blocked"
    interrupted_workflow_run_id = "wf-smoke-restart-interrupted"
    artifact_ref = "goflow://session-artifacts/artifact-agent-smoke"
    artifact_content = "# Smoke artifact\n\nFull artifact body for run history viewer."
    workflow_artifact_ref = f"goflow://workflow-runs/{workflow_run_id}/artifacts/plan-report"
    workflow_artifact_content = "Budget warning artifact body for selected-node artifact inspection."
    office_artifact_ref = "goflow://session-artifacts/artifact-office-smoke"
    office_artifact = write_smoke_office_fixture(workspace)
    office_artifact_content = json.dumps(office_artifact, ensure_ascii=False)
    session_dir = workspace / ".goflow"
    session_dir.mkdir(parents=True, exist_ok=True)
    snapshot = {
        "active_agent": "chat",
        "mode": "chat",
        "recent_prompts": [],
        "recent_tools": [],
        "agent_runs": [
            {
                "id": run_id,
                "status": "completed",
                "request": "Smoke run for summary-first history rendering.",
                "output": "Smoke run summary only. Timeline details load on demand.",
                "agent_id": "chat",
                "mode": "chat",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "result": {
                    "output": "Smoke staged delivery is implemented and verified.",
                    "agent_id": "chat",
                    "mode": "fix",
                    "tool_results": [
                        {
                            "call_id": "write-staged-smoke",
                            "tool_name": "file_tools/write_file",
                            "content": json.dumps(
                                {
                                    "relative_path": "staged-delivery.txt",
                                    "status": "modified",
                                    "added_lines": 1,
                                    "deleted_lines": 1,
                                    "bytes_written": 6,
                                    "diff_preview": "@@ -1,1 +1,1 @@\n-before\n+after",
                                }
                            ),
                        },
                        {
                            "call_id": "test-staged-smoke",
                            "tool_name": "command_tools/run_command",
                            "content": json.dumps(
                                {
                                    "program": "go",
                                    "args": ["test", "./..."],
                                    "exit_code": 0,
                                    "stdout": "ok",
                                    "duration_ms": 10,
                                }
                            ),
                        },
                    ],
                    "changes": [{"summary": "updated staged delivery fixture", "files": ["staged-delivery.txt"]}],
                    "verification": [{"kind": "test", "status": "passed", "detail": "go test ./... passed"}],
                },
                "events_count": 2,
                "artifacts_count": 4,
                "diffs_count": 1,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "agent_run_started",
                        "content": "Smoke run started.",
                        "agent_id": "chat",
                        "mode": "chat",
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "tool_result",
                        "content": f"{SMOKE_LAZY_TIMELINE_MARKER}: full timeline payload should appear only after lazy loading.",
                        "tool_name": "smoke_tool",
                        "tool_call_id": "call-smoke-lazy",
                        "agent_id": "chat",
                        "mode": "chat",
                    },
                ],
                "artifacts": [
                    {
                        "id": "artifact-agent-smoke",
                        "ref": artifact_ref,
                        "artifact_ref": artifact_ref,
                        "kind": "tool_result",
                        "title": "Smoke Artifact Result",
                        "summary": "Smoke artifact summary",
                        "content": "",
                        "mime": "text/markdown",
                        "size": 54,
                        "stored_bytes": 0,
                        "tool_name": "smoke_tool",
                        "tool_call_id": "call-smoke-artifact",
                        "agent_id": "chat",
                        "mode": "chat",
                        "metadata": {"source": "smoke"}
                    },
                    {
                        "id": "artifact-office-smoke",
                        "ref": office_artifact_ref,
                        "artifact_ref": office_artifact_ref,
                        "kind": "document",
                        "title": "网安评估报告",
                        "summary": "真实 Office 产物操作验收夹具",
                        "content": office_artifact_content,
                        "mime": office_artifact["mime"],
                        "size": (workspace / office_artifact["artifact_path"]).stat().st_size,
                        "stored_bytes": 0,
                        "tool_name": "office_tools/create_docx",
                        "agent_id": "documentation-specialist",
                        "mode": "workflow",
                        "metadata": {"source": "smoke-office", "sha256": office_artifact["sha256"]}
                    },
                    {
                        "id": "artifact-staged-write",
                        "kind": "tool_result",
                        "title": "staged-delivery.txt 变更",
                        "summary": "1 行修改",
                        "content": json.dumps(
                            {
                                "relative_path": "staged-delivery.txt",
                                "status": "modified",
                                "added_lines": 1,
                                "deleted_lines": 1,
                                "bytes_written": 6,
                                "diff_preview": "@@ -1,1 +1,1 @@\n-before\n+after",
                            }
                        ),
                        "mime": "application/json",
                        "tool_name": "file_tools/write_file",
                        "tool_call_id": "write-staged-smoke",
                        "agent_id": "chat",
                        "mode": "fix",
                        "metadata": {"source": "smoke-staged-delivery"}
                    },
                    {
                        "id": "artifact-staged-test",
                        "ref": "goflow://session-artifacts/artifact-staged-test",
                        "artifact_ref": "goflow://session-artifacts/artifact-staged-test",
                        "kind": "test",
                        "title": "go test ./...",
                        "summary": "真实命令验证通过",
                        "content": json.dumps(
                            {
                                "program": "go",
                                "args": ["test", "./..."],
                                "exit_code": 0,
                                "stdout": "ok",
                                "duration_ms": 10,
                            }
                        ),
                        "mime": "application/json",
                        "tool_name": "command_tools/run_command",
                        "tool_call_id": "test-staged-smoke",
                        "agent_id": "chat",
                        "mode": "fix",
                        "metadata": {
                            "source": "smoke-staged-delivery",
                            "category": "verification",
                            "hash": "smoke-staged-test-passed",
                        }
                    }
                ],
            }
        ],
        "workflow": {},
        "workflow_runs": [
            {
                "id": interrupted_workflow_run_id,
                "name": "plan-implement-audit",
                "status": "running",
                "request": "模拟进程重启后安全恢复工作流。",
                "summary": "进程停止时正在执行实现阶段。",
                "started_at": now,
                "updated_at": now,
                "attempt": 1,
                "next_stage": "implement",
                "events_count": 1,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "task_stage",
                        "stage": "implement",
                        "content": "进程重启前正在执行实现阶段",
                        "workflow_name": "plan-implement-audit",
                        "workflow_status": "running",
                        "next_stage": "implement"
                    }
                ]
            },
            {
                "id": workflow_run_id,
                "name": "smoke-budget-workflow",
                "status": "completed",
                "request": "Smoke workflow for budget diagnostics.",
                "summary": "Smoke workflow completed with visible budget attribution.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "budget_scope": "prompt",
                "budget_reason": "budget_soft_limit_hit",
                "budget_metric": "prompt_tokens",
                "budget_used": 180,
                "budget_soft_limit": 160,
                "budget_hard_limit": 240,
                "budget_remaining": 60,
                "budget_prompt_tokens": 100,
                "budget_estimated_prompt_tokens": 100,
                "budget_net_prompt_tokens": 100,
                "budget_gross_prompt_tokens": 175,
                "budget_saved_tokens": 75,
                "budget_memory_saved_tokens": 12,
                "budget_history_saved_tokens": 8,
                "budget_artifact_saved_tokens": 5,
                "budget_skill_saved_tokens": 20,
                "budget_tool_schema_saved_tokens": 30,
                "budget_output_tokens": 10,
                "budget_total_tokens": 110,
                "budget_llm_calls": 1,
                "artifacts_count": 2,
                "artifacts": [
                    {
                        "id": "artifact-workflow-budget-plan",
                        "stage": "plan",
                        "kind": "report",
                        "title": "Smoke plan budget artifact",
                        "summary": "Artifact evidence for the budget diagnostic smoke run.",
                        "content": workflow_artifact_content,
                        "artifact_ref": workflow_artifact_ref,
                        "content_bytes": len(workflow_artifact_content.encode("utf-8")),
                        "stored_bytes": len(workflow_artifact_content.encode("utf-8")),
                        "mime": "text/plain",
                        "metadata": {
                            "source_ref": "workflow.budget.prompt_tokens",
                            "hash": "smoke-plan-budget-artifact"
                        }
                    }
                ],
                "completed_stages": [
                    {
                        "stage": "plan",
                        "status": "completed",
                        "summary": "Planner produced a compact result.",
                        "budget_scope": "prompt",
                        "budget_reason": "budget_soft_limit_hit",
                        "budget_metric": "prompt_tokens",
                        "budget_used": 180,
                        "budget_soft_limit": 160,
                        "budget_hard_limit": 240,
                        "budget_remaining": 60,
                        "budget_prompt_tokens": 100,
                        "budget_estimated_prompt_tokens": 100,
                        "budget_net_prompt_tokens": 100,
                        "budget_gross_prompt_tokens": 175,
                        "budget_saved_tokens": 75,
                        "budget_memory_saved_tokens": 12,
                        "budget_history_saved_tokens": 8,
                        "budget_artifact_saved_tokens": 5,
                        "budget_skill_saved_tokens": 20,
                        "budget_tool_schema_saved_tokens": 30,
                        "budget_output_tokens": 10,
                        "budget_total_tokens": 110,
                        "budget_llm_calls": 1,
                        "source_ref": "workflow.budget.prompt_tokens",
                        "metadata": {
                            "budget_saved_tokens": "75",
                            "budget_gross_prompt_tokens": "175",
                            "budget_net_prompt_tokens": "100",
                            "budget.reason": "budget_soft_limit_hit",
                            "model.soft_budget_route": "true",
                            "model.soft_budget_route_ref": "stage.params.soft_budget_*",
                            "model.provider": "backup",
                            "model.model": "small-worker",
                            "model.max_tokens": "900",
                            "model.temperature": "0.1",
                            "source_ref": "workflow.budget.prompt_tokens"
                        },
                        "artifacts": [
                            {
                                "id": "artifact-workflow-budget-plan",
                                "stage": "plan",
                                "kind": "report",
                                "title": "Smoke plan budget artifact",
                                "summary": "Artifact evidence for the budget diagnostic smoke run.",
                                "content": workflow_artifact_content,
                                "artifact_ref": workflow_artifact_ref,
                                "content_bytes": len(workflow_artifact_content.encode("utf-8")),
                                "stored_bytes": len(workflow_artifact_content.encode("utf-8")),
                                "mime": "text/plain",
                                "metadata": {
                                    "source_ref": "workflow.budget.prompt_tokens",
                                    "hash": "smoke-plan-budget-artifact"
                                }
                            }
                        ],
                        "result": {"output": "Planner result", "summary": "Planner summary"}
                    }
                ],
                "events_count": 5,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "prompt_budget",
                        "stage": "plan",
                        "content": "Prompt budget attribution recorded.",
                        "budget_estimated_prompt_tokens": 100,
                        "budget_net_prompt_tokens": 100,
                        "budget_gross_prompt_tokens": 175,
                        "budget_saved_tokens": 75,
                        "budget_memory_saved_tokens": 12,
                        "budget_history_saved_tokens": 8,
                        "budget_artifact_saved_tokens": 5,
                        "budget_skill_saved_tokens": 20,
                        "budget_tool_schema_saved_tokens": 30,
                        "prompt_budget": {
                            "estimated_prompt_tokens": 100,
                            "memory_estimated_saved_tokens": 12,
                            "history_estimated_saved_tokens": 8,
                            "artifact_omitted_tokens": 5,
                            "skill_omitted_tokens": 20,
                            "tool_schema_estimated_saved_tokens": 30
                        }
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "status",
                        "stage": "plan",
                        "content": "soft budget model route applied at stage plan: provider=backup model=small-worker max_tokens=900 temperature=0.1",
                        "reason": "budget_model_route_applied",
                        "severity": "info",
                        "source_ref": "stage.params.soft_budget_*"
                    },
                    {
                        "seq": 3,
                        "at": now,
                        "type": "status",
                        "stage": "plan",
                        "content": "Smoke workflow budget warning marker.",
                        "reason": "budget_soft_limit_hit",
                        "budget_reason": "budget_soft_limit_hit",
                        "budget_scope": "prompt",
                        "budget_metric": "prompt_tokens",
                        "budget_used": 180,
                        "budget_soft_limit": 160,
                        "budget_hard_limit": 240,
                        "budget_remaining": 60,
                        "severity": "warning",
                        "source_ref": "workflow.budget.prompt_tokens"
                    },
                    {
                        "seq": 4,
                        "at": now,
                        "type": "token_usage",
                        "stage": "plan",
                        "prompt_tokens": 90,
                        "output_tokens": 10
                    }
                ],
            },
            {
                "id": blocked_workflow_run_id,
                "name": "smoke-contract-workflow",
                "status": "blocked",
                "next_stage": "plan",
                "request": "Smoke workflow for blocked-stage diagnostics.",
                "summary": "Smoke workflow blocked by contract validation.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "reason": "contract_validation_failed",
                "contract_check": "smoke_summary_contract_required",
                "source_ref": "stages.plan.acceptance[0]",
                "completed_stages": [
                    {
                        "stage": "plan",
                        "status": "blocked",
                        "summary": "Contract validation failed: missing required acceptance evidence.",
                        "reason": "contract_validation_failed",
                        "severity": "error",
                        "contract_check": "smoke_summary_contract_required",
                        "source_ref": "stages.plan.acceptance[0]",
                        "metadata": {
                            "contract_failed": "true",
                            "reason": "contract_validation_failed",
                            "contract_check": "smoke_summary_contract_required",
                            "source_ref": "stages.plan.acceptance[0]"
                        },
                        "acceptance": [
                            {
                                "name": "summary-present",
                                "status": "failed",
                                "reason": "Missing required summary evidence"
                            }
                        ],
                        "result": {"output": "Partial planner result without required acceptance evidence."}
                    }
                ],
                "events_count": 2,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "contract_validation_failed",
                        "stage": "plan",
                        "content": "contract_validation_failed: missing required acceptance evidence.",
                        "reason": "contract_validation_failed",
                        "severity": "error",
                        "contract_check": "smoke_summary_contract_required",
                        "source_ref": "stages.plan.acceptance[0]"
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "workflow_result",
                        "workflow_name": "smoke-contract-workflow",
                        "workflow_status": "blocked",
                        "content": "Workflow blocked by contract validation.",
                        "reason": "contract_validation_failed",
                        "contract_check": "smoke_summary_contract_required",
                        "source_ref": "stages.plan.acceptance[0]"
                    }
                ],
            },
            {
                "id": execution_readiness_run_id,
                "name": "smoke-live-readiness-workflow",
                "status": "blocked",
                "request": "Smoke workflow for live execution readiness diagnostics.",
                "summary": "Smoke live RESTCONF stage blocked before tool execution because required readiness evidence is missing.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "reason": "execution_readiness_blocked",
                "contract_check": "execution_readiness",
                "source_ref": "stage.execution.allow_live_tools",
                "completed_stages": [
                    {
                        "stage": "apply-live",
                        "status": "blocked",
                        "node_type": "tool",
                        "agent_id": "operations-specialist",
                        "skill": "execution-plan",
                        "tool": "network_tools/device_restconf_live_apply",
                        "summary": "Live RESTCONF apply blocked by missing execution readiness evidence.",
                        "reason": "execution_readiness_blocked",
                        "severity": "error",
                        "contract_check": "execution_readiness",
                        "source_ref": "stage.execution.allow_live_tools",
                        "outputs": {
                            "execution_mode": "live",
                            "execution_ready": "false",
                            "execution_risk_level": "high",
                            "execution_boundary": "production-restconf-change",
                            "execution_missing": "approval,authorized_scope,rollback_plan,credential_ref,allowlist,allow_live_tools:network_tools/device_restconf_live_apply"
                        },
                        "metadata": {
                            "contract_failed": "true",
                            "reason": "execution_readiness_blocked",
                            "contract_check": "execution_readiness",
                            "source_ref": "stage.execution.allow_live_tools",
                            "execution.mode": "live",
                            "execution.ready": "false",
                            "execution.risk_level": "high",
                            "execution.boundary": "production-restconf-change",
                            "execution.missing": "approval,authorized_scope,rollback_plan,credential_ref,allowlist,allow_live_tools:network_tools/device_restconf_live_apply",
                            "execution.reason": "workflow stage apply-live is live but missing execution readiness",
                            "execution.source_ref": "stage.execution.allow_live_tools"
                        },
                        "result": {
                            "output": "Live RESTCONF apply blocked before tool execution. Missing approval, authorized_scope, rollback_plan, credential_ref, allowlist, and allow_live_tools."
                        }
                    }
                ],
                "events_count": 2,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "execution_readiness_blocked",
                        "stage": "apply-live",
                        "content": "execution_readiness_blocked: missing approval, authorized_scope, rollback_plan, credential_ref, allowlist, allow_live_tools:network_tools/device_restconf_live_apply",
                        "reason": "execution_readiness_blocked",
                        "severity": "error",
                        "contract_check": "execution_readiness",
                        "source_ref": "stage.execution.allow_live_tools",
                        "metadata": {
                            "execution.mode": "live",
                            "execution.ready": "false",
                            "execution.risk_level": "high",
                            "execution.boundary": "production-restconf-change",
                            "execution.missing": "approval,authorized_scope,rollback_plan,credential_ref,allowlist,allow_live_tools:network_tools/device_restconf_live_apply"
                        }
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "workflow_result",
                        "workflow_name": "smoke-live-readiness-workflow",
                        "workflow_status": "blocked",
                        "content": "Workflow blocked by live execution readiness.",
                        "reason": "execution_readiness_blocked",
                        "contract_check": "execution_readiness",
                        "source_ref": "stage.execution.allow_live_tools"
                    }
                ],
            },
            {
                "id": model_escalated_workflow_run_id,
                "name": "smoke-contract-workflow",
                "status": "completed",
                "request": "Smoke workflow recovered by model escalation.",
                "summary": "Smoke workflow recovered the contract failure with a stronger model route.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "reason": "model_escalated",
                "source_ref": "stage.params.escalate_model",
                "completed_stages": [
                    {
                        "stage": "plan",
                        "status": "completed",
                        "attempts": 2,
                        "node_type": "skill",
                        "skill": "execution-plan",
                        "agent_id": "planner",
                        "summary": "Escalated model produced contract-satisfying JSON.",
                        "reason": "model_escalated",
                        "source_ref": "stage.params.escalate_model",
                        "metadata": {
                            "model.escalated": "true",
                            "model.route": "model_escalated",
                            "model.model": "strong-smoke-model",
                            "model.escalation_ref": "stage.params.escalate_model",
                            "source_ref": "stage.params.escalate_model",
                            "reason": "model_escalated"
                        },
                        "outputs": {
                            "summary": "model escalation recovered"
                        },
                        "output_values": {
                            "summary": "model escalation recovered"
                        },
                        "result": {
                            "output": "{\"summary\":\"model escalation recovered\"}",
                            "summary": "model escalation recovered",
                            "model": "strong-smoke-model"
                        }
                    },
                    {
                        "stage": "audit",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "auditor",
                        "summary": "Audit ran after model escalation.",
                        "result": {"output": "Audit after escalation passed."}
                    }
                ],
                "events_count": 4,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "contract_validation_failed",
                        "stage": "plan",
                        "content": "contract_validation_failed: JSON summary was missing.",
                        "reason": "contract_validation_failed",
                        "severity": "error",
                        "contract_check": "json_output:summary",
                        "source_ref": "result.output"
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "status",
                        "stage": "plan",
                        "content": "model escalated for stage plan: model=strong-smoke-model",
                        "reason": "model_escalated",
                        "severity": "info",
                        "source_ref": "stage.params.escalate_model"
                    },
                    {
                        "seq": 3,
                        "at": now,
                        "type": "status",
                        "stage": "plan",
                        "content": "workflow stage plan retry attempt 2: contract_validation_failed",
                        "reason": "stage_retry",
                        "severity": "warning",
                        "source_ref": "stage.retry.attempt[2]"
                    },
                    {
                        "seq": 4,
                        "at": now,
                        "type": "workflow_result",
                        "workflow_name": "smoke-contract-workflow",
                        "workflow_status": "completed",
                        "content": "Workflow recovered by model escalation.",
                        "reason": "model_escalated",
                        "source_ref": "stage.params.escalate_model"
                    }
                ],
            },
            {
                "id": complex_delivery_run_id,
                "name": "complex-project-delivery",
                "status": "completed",
                "request": "Smoke workflow for complex delivery visualization.",
                "summary": "Complex delivery completed with one approved plan, implementation, parallel verification, audit, and final report.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "change_slices": [
                    {
                        "id": "slice-a",
                        "title": "交付 Slice A",
                        "execution_mode": "serial",
                        "input_files": ["README.md", "main.go"],
                        "input_hashes": {"README.md": "smoke-readme-input", "main.go": "smoke-main-input"},
                        "target_files": ["README.md", "main.go"],
                        "verification_commands": ["go test ./...", "go vet ./..."],
                        "contract_hash": "smoke-contract-slice-a",
                        "status": "verified",
                        "attempt": 2,
                        "evidence_stages": ["implement", "test", "lint", "audit"]
                    },
                    {
                        "id": "slice-b",
                        "title": "等待发布说明复核",
                        "execution_mode": "serial",
                        "depends_on": ["slice-a"],
                        "target_files": ["UPGRADE.md"],
                        "verification_commands": ["git diff --check"],
                        "contract_hash": "smoke-contract-slice-b",
                        "status": "blocked",
                        "blocked_reason": "等待用户确认发布说明中的回滚范围",
                        "attempt": 1,
                        "evidence_stages": ["plan"]
                    }
                ],
                "verification": [
                    {"kind": "test", "name": "Go 回归测试", "status": "passed", "stage": "test", "detail": "go test ./... 通过"},
                    {"kind": "static", "name": "静态检查", "status": "passed", "stage": "lint", "detail": "go vet ./... 通过"}
                ],
                "artifacts": [
                    {"id": "artifact-complex-plan", "title": "交付计划", "kind": "report", "mime": "text/markdown", "summary": "包含分片、验证和回滚范围"},
                    {"id": "artifact-complex-diff", "title": "Source Diff", "kind": "diff", "mime": "text/x-diff", "summary": "真实变更：README.md、main.go"}
                ],
                "completed_stages": [
                    {
                        "stage": "plan",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "execution-plan",
                        "agent_id": "planner",
                        "summary": "Plan created for Slice A.",
                        "outputs": {
                            "plan": "Delivery Plan: deliver Slice A\nTarget Files: README.md, main.go\nChange Slices: Slice A updates README.md and main.go\nDependencies: none\nAcceptance Checks: behavior documented and tested\nVerification Commands: go test ./... and go vet ./...\nFailure Recovery: preserve evidence and retry the failed stage\nDefinition of Done: both files updated and checks pass"
                        },
                        "result": {
                            "output": "Delivery Plan: deliver Slice A\nTarget Files: README.md, main.go\nChange Slices: Slice A updates README.md and main.go\nDependencies: none\nAcceptance Checks: behavior documented and tested\nVerification Commands: go test ./... and go vet ./...\nFailure Recovery: preserve evidence and retry the failed stage\nDefinition of Done: both files updated and checks pass"
                        }
                    },
                    {
                        "stage": "implement",
                        "status": "completed",
                        "node_type": "agent",
                        "skill": "code-writing",
                        "agent_id": "fixer",
                        "summary": "Slice A implemented in two files.",
                        "outputs": {
                            "implementation": "Change Summary: delivered Slice A\nFiles Changed: README.md, main.go\nRequirements Covered: behavior and documentation\nVerification Attempted: go test ./... passed\nFailure Recovery: no recovery needed\nRemaining Risks: none",
                            "changed_files": "README.md, main.go"
                        },
                        "result": {
                            "output": "Change Summary: delivered Slice A\nFiles Changed: README.md, main.go\nRequirements Covered: behavior and documentation\nVerification Attempted: go test ./... passed\nFailure Recovery: no recovery needed\nRemaining Risks: none"
                        }
                    },
                    {
                        "stage": "test",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "auditor",
                        "summary": "Tests passed after one recovered attempt.",
                        "outputs": {
                            "test_report": "Test Result: pass\nCommands Run: go test ./...\nCoverage: Slice A behavior\nFailure Evidence: first provider attempt returned HTTP 500\nRecovery Attempt: second attempt passed\nResidual Risk: none"
                        },
                        "result": {
                            "output": "Test Result: pass\nCommands Run: go test ./...\nCoverage: Slice A behavior\nFailure Evidence: first provider attempt returned HTTP 500\nRecovery Attempt: second attempt passed\nResidual Risk: none",
                            "verification": [
                                {"kind": "test", "status": "passed", "detail": "go test ./... passed after retry"}
                            ]
                        }
                    },
                    {
                        "stage": "lint",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "planner",
                        "summary": "Static checks passed.",
                        "outputs": {
                            "lint_report": "Lint Result: pass\nCommands Run: go vet ./...\nFindings: none\nConfiguration Notes: default Go configuration\nRecovery Attempt: not needed\nResidual Risk: none"
                        },
                        "result": {
                            "output": "Lint Result: pass\nCommands Run: go vet ./...\nFindings: none\nConfiguration Notes: default Go configuration\nRecovery Attempt: not needed\nResidual Risk: none",
                            "verification": [
                                {"kind": "static", "status": "passed", "detail": "go vet ./... passed"}
                            ]
                        }
                    },
                    {
                        "stage": "audit",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "auditor",
                        "summary": "Delivery evidence accepted.",
                        "outputs": {
                            "audit": "Audit Decision: pass\nRequirements Coverage: Slice A covered\nTest Evidence: go test ./... passed\nLint Evidence: go vet ./... passed\nBlocking Findings: none\nRecovery Status: injected test failure recovered\nRecommendation: deliver"
                        },
                        "result": {
                            "output": "Audit Decision: pass\nRequirements Coverage: Slice A covered\nTest Evidence: go test ./... passed\nLint Evidence: go vet ./... passed\nBlocking Findings: none\nRecovery Status: injected test failure recovered\nRecommendation: deliver"
                        }
                    },
                    {
                        "stage": "quality",
                        "status": "completed",
                        "node_type": "quality_gate",
                        "summary": "Quality gate passed.",
                        "outputs": {
                            "quality_status": "pass",
                            "failures": "none"
                        },
                        "result": {
                            "output": "Quality Status: pass\nFailed Checks: none"
                        }
                    },
                    {
                        "stage": "final-report",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "execution-plan",
                        "agent_id": "planner",
                        "summary": "Completion report produced.",
                        "outputs": {
                            "final_report": "Delivery Status: completed\nChanges Delivered: Slice A behavior and documentation\nFiles Changed: README.md, main.go\nTest Evidence: go test ./... passed after retry\nLint Evidence: go vet ./... passed\nAudit Decision: pass\nRecovery: injected failure recovered\nResidual Risks: none\nArtifact References: delivery-plan, implementation-report, test-report, lint-report, delivery-audit"
                        },
                        "result": {
                            "output": "Delivery Status: completed\nChanges Delivered: Slice A behavior and documentation\nFiles Changed: README.md, main.go\nTest Evidence: go test ./... passed after retry\nLint Evidence: go vet ./... passed\nAudit Decision: pass\nRecovery: injected failure recovered\nResidual Risks: none\nArtifact References: delivery-plan, implementation-report, test-report, lint-report, delivery-audit"
                        }
                    }
                ],
                "events_count": 7,
                "events": [
                    {"seq": 1, "at": now, "type": "task_stage", "stage": "plan", "content": "Delivery plan created for Slice A."},
                    {"seq": 2, "at": now, "type": "task_stage", "stage": "implement", "content": "Implementation changed README.md and main.go."},
                    {"seq": 3, "at": now, "type": "stage_retry", "stage": "test", "attempt": 2, "content": "Test stage recovered on attempt 2."},
                    {"seq": 4, "at": now, "type": "task_stage", "stage": "test", "content": "Tests passed."},
                    {"seq": 5, "at": now, "type": "task_stage", "stage": "lint", "content": "Static checks passed."},
                    {"seq": 6, "at": now, "type": "task_stage", "stage": "audit", "content": "Delivery audit passed."},
                    {"seq": 7, "at": now, "type": "workflow_result", "workflow_name": "complex-project-delivery", "workflow_status": "completed", "content": "Complex delivery completed."}
                ],
            },
            {
                "id": quality_workflow_run_id,
                "name": "plan-implement-audit",
                "status": "blocked",
                "request": "Smoke workflow for quality gate diagnostics.",
                "summary": "Smoke workflow blocked by a failed quality gate.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "reason": "quality_gate_failed",
                "contract_check": "quality_gate",
                "source_ref": "acceptance_failed=1; verification_failed=1",
                "completed_stages": [
                    {
                        "stage": "quality",
                        "status": "blocked",
                        "node_type": "quality_gate",
                        "summary": "Quality gate failed because required acceptance and verification evidence is missing.",
                        "reason": "quality_gate_failed",
                        "severity": "error",
                        "contract_check": "quality_gate",
                        "source_ref": "acceptance_failed=1; verification_failed=1",
                        "quality_status": "failed",
                        "quality_score": 61,
                        "quality_failures": "acceptance_failed=1; verification_failed=1; missing evidence artifact",
                        "metadata": {
                            "quality_failed": "true",
                            "quality_status": "failed",
                            "score": "61",
                            "failures": "acceptance_failed=1; verification_failed=1; missing evidence artifact",
                            "warnings": "verification status unknown for audit evidence",
                            "reason": "quality_gate_failed",
                            "contract_check": "quality_gate",
                            "source_ref": "acceptance_failed=1; verification_failed=1",
                            "severity": "error"
                        },
                        "outputs": {
                            "quality_status": "failed",
                            "score": "61",
                            "failures": "acceptance_failed=1; verification_failed=1; missing evidence artifact",
                            "warnings": "verification status unknown for audit evidence",
                            "route": "fail",
                            "acceptance_failed": "1",
                            "verification_failed": "1"
                        },
                        "result": {
                            "output": "Quality gate failed: acceptance_failed=1; verification_failed=1; missing evidence artifact.",
                            "outputs": {
                                "quality_status": "failed",
                                "score": 61,
                                "failures": "acceptance_failed=1; verification_failed=1; missing evidence artifact",
                                "route": "fail"
                            },
                            "quality_status": "failed"
                        }
                    }
                ],
                "events_count": 2,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "quality_gate_failed",
                        "stage": "quality",
                        "content": "Quality gate failed: acceptance_failed=1; verification_failed=1; missing evidence artifact.",
                        "reason": "quality_gate_failed",
                        "severity": "error",
                        "contract_check": "quality_gate",
                        "source_ref": "acceptance_failed=1; verification_failed=1",
                        "quality_status": "failed",
                        "quality_score": 61,
                        "failures": "acceptance_failed=1; verification_failed=1; missing evidence artifact"
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "workflow_result",
                        "workflow_name": "plan-implement-audit",
                        "workflow_status": "blocked",
                        "content": "Workflow blocked by quality gate failure.",
                        "reason": "quality_gate_failed",
                        "contract_check": "quality_gate",
                        "source_ref": "acceptance_failed=1; verification_failed=1"
                    }
                ],
            },
            {
                "id": parallel_conflict_workflow_run_id,
                "name": "smoke-parallel-conflict",
                "status": "completed",
                "request": "Smoke workflow for parallel file conflict diagnostics.",
                "summary": "Smoke workflow completed with a warning for parallel branches touching the same file.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "reason": "parallel_file_conflict",
                "contract_check": "parallel_file_ownership",
                "source_ref": "internal/shared.go:audit,implement",
                "completed_stages": [
                    {
                        "stage": "implement",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-writing",
                        "agent_id": "fixer",
                        "summary": "Implement branch changed internal/shared.go.",
                        "severity": "warning",
                        "contract_check": "parallel_file_ownership",
                        "source_ref": "internal/shared.go:audit,implement",
                        "metadata": {
                            "parallel_branch_owner": "implement",
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement",
                            "contract_check": "parallel_file_ownership",
                            "severity": "warning"
                        },
                        "outputs": {
                            "changed_files": "internal/shared.go",
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                        },
                        "result": {
                            "output": "Implementation branch touched internal/shared.go.",
                            "outputs": {
                                "changed_files": ["internal/shared.go"],
                                "parallel_file_conflict": True,
                                "parallel_file_conflict_paths": ["internal/shared.go"],
                                "parallel_file_conflict_owners": ["audit", "implement"],
                                "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                            }
                        }
                    },
                    {
                        "stage": "audit",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "auditor",
                        "summary": "Audit branch also changed internal/shared.go.",
                        "severity": "warning",
                        "contract_check": "parallel_file_ownership",
                        "source_ref": "internal/shared.go:audit,implement",
                        "metadata": {
                            "parallel_branch_owner": "audit",
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement",
                            "contract_check": "parallel_file_ownership",
                            "severity": "warning"
                        },
                        "outputs": {
                            "changed_files": "internal/shared.go",
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                        },
                        "result": {
                            "output": "Audit branch touched internal/shared.go.",
                            "outputs": {
                                "changed_files": ["internal/shared.go"],
                                "parallel_file_conflict": True,
                                "parallel_file_conflict_paths": ["internal/shared.go"],
                                "parallel_file_conflict_owners": ["audit", "implement"],
                                "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                            }
                        }
                    },
                    {
                        "stage": "join",
                        "status": "completed",
                        "node_type": "join",
                        "summary": "Join completed after recording the parallel file conflict warning.",
                        "reason": "parallel_file_conflict",
                        "severity": "warning",
                        "contract_check": "parallel_file_ownership",
                        "source_ref": "internal/shared.go:audit,implement",
                        "metadata": {
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement",
                            "contract_check": "parallel_file_ownership",
                            "severity": "warning"
                        },
                        "outputs": {
                            "parallel_file_conflict": "true",
                            "parallel_file_conflict_paths": "internal/shared.go",
                            "parallel_file_conflict_owners": "audit, implement",
                            "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                        },
                        "result": {
                            "output": "Parallel file conflict warning before join: internal/shared.go:audit,implement.",
                            "outputs": {
                                "parallel_file_conflict": True,
                                "parallel_file_conflict_paths": ["internal/shared.go"],
                                "parallel_file_conflict_owners": ["audit", "implement"],
                                "parallel_file_conflict_source_ref": "internal/shared.go:audit,implement"
                            }
                        }
                    },
                    {
                        "stage": "report",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "execution-plan",
                        "agent_id": "planner",
                        "summary": "Report captured the warning without blocking delivery.",
                        "result": {"output": "Workflow completed; inspect join for parallel file conflict warning."}
                    }
                ],
                "events_count": 4,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "task_stage",
                        "stage": "implement",
                        "content": "parallel branch implement completed with changed_files=internal/shared.go",
                        "reason": "parallel_branch",
                        "source_ref": "split"
                    },
                    {
                        "seq": 2,
                        "at": now,
                        "type": "task_stage",
                        "stage": "audit",
                        "content": "parallel branch audit completed with changed_files=internal/shared.go",
                        "reason": "parallel_branch",
                        "source_ref": "split"
                    },
                    {
                        "seq": 3,
                        "at": now,
                        "type": "status",
                        "stage": "join",
                        "content": "parallel branch direct-write handoff warning before join: implement:params.tools=file_tools/write_file",
                        "reason": "parallel_patch_artifact_required",
                        "severity": "warning",
                        "contract_check": "parallel_patch_artifact_handoff",
                        "source_ref": "implement:params.tools=file_tools/write_file"
                    },
                    {
                        "seq": 4,
                        "at": now,
                        "type": "status",
                        "stage": "join",
                        "content": "parallel_file_conflict before join: internal/shared.go:audit,implement",
                        "reason": "parallel_file_conflict",
                        "severity": "warning",
                        "contract_check": "parallel_file_ownership",
                        "source_ref": "internal/shared.go:audit,implement"
                    },
                    {
                        "seq": 5,
                        "at": now,
                        "type": "workflow_result",
                        "workflow_name": "smoke-parallel-conflict",
                        "workflow_status": "completed",
                        "content": "Workflow completed with a parallel file conflict warning.",
                        "reason": "parallel_file_conflict",
                        "contract_check": "parallel_file_ownership",
                        "source_ref": "internal/shared.go:audit,implement"
                    }
                ],
            },
            {
                "id": budget_approval_run_id,
                "name": "plan-implement-audit",
                "status": "awaiting_budget_approval",
                "request": "Smoke workflow paused for hard budget approval.",
                "summary": "Smoke workflow paused at a hard prompt budget boundary.",
                "started_at": now,
                "updated_at": now,
                "attempt": 1,
                "next_stage": "plan",
                "approval_prompt": "Hard prompt budget limit hit before the plan stage could continue.",
                "reason": "budget_hard_limit_hit",
                "budget_scope": "prompt",
                "budget_reason": "budget_hard_limit_hit",
                "budget_metric": "prompt_tokens",
                "budget_used": 250,
                "budget_soft_limit": 200,
                "budget_hard_limit": 240,
                "budget_remaining": 0,
                "budget_prompt_tokens": 250,
                "budget_estimated_prompt_tokens": 250,
                "budget_net_prompt_tokens": 250,
                "budget_gross_prompt_tokens": 300,
                "budget_saved_tokens": 50,
                "budget_total_tokens": 250,
                "budget_llm_calls": 1,
                "source_ref": "workflow.budget.prompt_tokens",
                "events_count": 1,
                "events": [
                    {
                        "seq": 1,
                        "at": now,
                        "type": "budget_hard_limit_hit",
                        "stage": "plan",
                        "content": "Hard prompt budget limit hit before the plan stage could continue.",
                        "reason": "budget_hard_limit_hit",
                        "budget_reason": "budget_hard_limit_hit",
                        "budget_scope": "prompt",
                        "budget_metric": "prompt_tokens",
                        "budget_used": 250,
                        "budget_soft_limit": 200,
                        "budget_hard_limit": 240,
                        "budget_remaining": 0,
                        "severity": "warning",
                        "source_ref": "workflow.budget.prompt_tokens",
                        "needs_action": True
                    }
                ],
            }
        ],
        "messages": [],
        "blackboard": [],
        "artifacts": [
            {
                "id": "artifact-agent-smoke",
                "ref": artifact_ref,
                "artifact_ref": artifact_ref,
                "kind": "tool_result",
                "title": "Smoke Artifact Result",
                "summary": "Smoke artifact summary",
                "content": artifact_content,
                "content_bytes": len(artifact_content.encode("utf-8")),
                "stored_bytes": len(artifact_content.encode("utf-8")),
                "mime": "text/markdown",
                "tool_name": "smoke_tool",
                "tool_call_id": "call-smoke-artifact",
                "agent_id": "chat",
                "mode": "chat",
                "metadata": {"source": "smoke"}
            },
            {
                "id": "artifact-office-smoke",
                "ref": office_artifact_ref,
                "artifact_ref": office_artifact_ref,
                "kind": "document",
                "title": "网安评估报告",
                "summary": "真实 Office 产物操作验收夹具",
                "content": office_artifact_content,
                "content_bytes": len(office_artifact_content.encode("utf-8")),
                "stored_bytes": len(office_artifact_content.encode("utf-8")),
                "mime": office_artifact["mime"],
                "tool_name": "office_tools/create_docx",
                "agent_id": "documentation-specialist",
                "mode": "workflow",
                "metadata": {"source": "smoke-office", "sha256": office_artifact["sha256"]}
            }
        ],
        "pending_approvals": [
            {
                "call_id": "call-smoke-approval",
                "tool_name": "write_file",
                "agent_id": "chat",
                "arguments_summary": "path=demo.py content=1200 chars",
                "agent_run_id": run_id,
                "risk": {
                    "risk_level": "high",
                    "kind": "write",
                    "requires_approval": True,
                    "workspace_scoped_inputs": True,
                    "workspace_scope_enforced": True,
                },
            }
        ],
    }
    (session_dir / "session.json").write_text(json.dumps(snapshot, indent=2) + "\n", encoding="utf-8")
    return {
        "run_id": run_id,
        "workflow_run_id": workflow_run_id,
        "blocked_workflow_run_id": blocked_workflow_run_id,
        "model_escalated_workflow_run_id": model_escalated_workflow_run_id,
        "execution_readiness_run_id": execution_readiness_run_id,
        "complex_delivery_run_id": complex_delivery_run_id,
        "quality_workflow_run_id": quality_workflow_run_id,
        "parallel_conflict_workflow_run_id": parallel_conflict_workflow_run_id,
        "budget_approval_run_id": budget_approval_run_id,
        "interrupted_workflow_run_id": interrupted_workflow_run_id,
        "marker": SMOKE_LAZY_TIMELINE_MARKER,
        "artifact_ref": artifact_ref,
        "office_artifact_path": office_artifact["artifact_path"],
        "office_preview_path": office_artifact["preview_paths"][0],
    }


def wait_for_server(base_url: str, proc: subprocess.Popen[str], timeout: float) -> None:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        if proc.poll() is not None:
            output = proc.stdout.read() if proc.stdout is not None else ""
            raise AssertionError(f"goflow exited before HTTP became ready with {proc.returncode}\n{output}")
        try:
            status, _ = request_text(f"{base_url}/api/session", timeout=2.0)
            if status == 200:
                return
        except Exception as exc:  # noqa: BLE001 - startup context matters here.
            last_error = str(exc)
        time.sleep(0.05)
    raise AssertionError(f"HTTP server did not become ready at {base_url}: {last_error}")


def browser_candidates() -> list[Path]:
    names = [
        "chrome",
        "google-chrome",
        "google-chrome-stable",
        "chromium",
        "chromium-browser",
        "msedge",
        "microsoft-edge",
    ]
    paths: list[Path] = []
    for name in names:
        resolved = shutil.which(name)
        if resolved:
            paths.append(Path(resolved))
    if os.name == "nt":
        program_files = [os.environ.get("ProgramFiles"), os.environ.get("ProgramFiles(x86)"), os.environ.get("LocalAppData")]
        relative = [
            Path("Google/Chrome/Application/chrome.exe"),
            Path("Microsoft/Edge/Application/msedge.exe"),
            Path("Chromium/Application/chrome.exe"),
        ]
        for base in program_files:
            if not base:
                continue
            for suffix in relative:
                paths.append(Path(base) / suffix)
    return paths


def find_browser(explicit: Path | None, required: bool) -> Path | None:
    candidates = [explicit] if explicit else browser_candidates()
    for candidate in candidates:
        if candidate and candidate.is_file():
            return candidate
    if required:
        searched = ", ".join(str(path) for path in candidates if path)
        raise AssertionError(f"no Chrome/Edge/Chromium browser found for browser smoke test; searched: {searched}")
    return None


def render_dom(browser: Path, url: str, timeout: float, profile_dir: Path) -> str:
    failures = []
    for launch_attempt in range(1, 4):
        page_profile = Path(tempfile.mkdtemp(prefix="goflow-browser-page-", dir=profile_dir))
        crash_dir = page_profile / "crash-dumps"
        crash_dir.mkdir(parents=True, exist_ok=True)
        base_command = [
            str(browser),
            "--disable-gpu",
            "--disable-dev-shm-usage",
            "--disable-extensions",
            "--disable-background-networking",
            "--disable-breakpad",
            "--disable-crash-reporter",
            "--disable-crashpad",
            *WINDOWS_EDGE_SANDBOX_FLAGS,
            "--no-first-run",
            "--no-default-browser-check",
            "--no-sandbox",
            f"--crash-dumps-dir={crash_dir}",
            f"--user-data-dir={page_profile}",
            "--virtual-time-budget=6000",
            "--dump-dom",
            url,
        ]
        attempt_failures = []
        try:
            for headless_flag in ("--headless=new", "--headless"):
                command = [base_command[0], headless_flag, *base_command[1:]]
                try:
                    completed = subprocess.run(
                        command,
                        text=True,
                        encoding="utf-8",
                        errors="replace",
                        stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT,
                        timeout=timeout,
                    )
                except subprocess.TimeoutExpired as exc:
                    output = (exc.stdout or "") if isinstance(exc.stdout, str) else ""
                    attempt_failures.append(f"{headless_flag}: timed out after {timeout}s\n{output[-2000:]}")
                    continue
                if completed.returncode == 0:
                    return completed.stdout
                attempt_failures.append(f"{headless_flag}: exit {completed.returncode}\n{completed.stdout[-3000:]}")
        finally:
            shutil.rmtree(page_profile, ignore_errors=True)
        failures.extend(f"attempt {launch_attempt} {failure}" for failure in attempt_failures)
        combined = "\n".join(attempt_failures).lower()
        if "crashpad" not in combined and "mojo" not in combined and "access is denied" not in combined and "拒绝访问" not in combined:
            break
        time.sleep(0.6 * launch_attempt)
    raise BrowserSmokeUnavailable(f"browser failed for {url}\n" + "\n".join(failures))


def evaluate_browser_json(
    browser: Path,
    url: str,
    script: str,
    timeout: float,
    profile_dir: Path,
    viewport: tuple[int, int] | None = None,
    screenshot_path: Path | None = None,
) -> dict[str, object]:
    parsed_url = urllib.parse.urlsplit(url)
    query = urllib.parse.parse_qs(parsed_url.query, keep_blank_values=True)
    if "lang" not in query and "locale" not in query:
        separator = "&" if parsed_url.query else "?"
        base = urllib.parse.urlunsplit((parsed_url.scheme, parsed_url.netloc, parsed_url.path, parsed_url.query, ""))
        url = f"{base}{separator}lang=en"
        if parsed_url.fragment:
            url += f"#{parsed_url.fragment}"
    debug_port = free_port()
    debug_profile = profile_dir / f"debug-{secrets.token_hex(4)}"
    debug_profile.mkdir()
    crash_dir = debug_profile / "crash-dumps"
    crash_dir.mkdir(parents=True, exist_ok=True)
    viewport_width, viewport_height = viewport or (1440, 1100)
    command = [
        str(browser),
        "--headless=new",
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-extensions",
        "--disable-background-networking",
        "--disable-breakpad",
        "--disable-crash-reporter",
        "--disable-crashpad",
        *WINDOWS_EDGE_SANDBOX_FLAGS,
        "--no-first-run",
        "--no-default-browser-check",
        "--no-sandbox",
        f"--crash-dumps-dir={crash_dir}",
        f"--user-data-dir={debug_profile}",
        f"--remote-debugging-port={debug_port}",
        f"--window-size={viewport_width},{viewport_height}",
        url,
    ]
    proc = subprocess.Popen(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    try:
        page = wait_for_debug_page(debug_port, url, proc, timeout)
        parsed_debug_url = urllib.parse.urlparse(str(page["webSocketDebuggerUrl"]))
        if viewport is not None:
            devtools_call(
                parsed_debug_url,
                {
                    "id": 100,
                    "method": "Emulation.setDeviceMetricsOverride",
                    "params": {
                        "width": viewport_width,
                        "height": viewport_height,
                        "deviceScaleFactor": 1,
                        "mobile": viewport_width <= 700,
                    },
                },
                timeout,
            )
            time.sleep(0.25)
        expression = textwrap.dedent(
            f"""
            (async () => {{
              await new Promise(resolve => setTimeout(resolve, 900));
              const result = {script};
              return result;
            }})()
            """
        )
        response: dict[str, object] = {}
        for attempt in range(1, 5):
            response = devtools_call(
                parsed_debug_url,
                {
                    "id": attempt,
                    "method": "Runtime.evaluate",
                    "params": {"expression": expression, "awaitPromise": True, "returnByValue": True},
                },
                timeout,
            )
            error = response.get("error")
            if isinstance(error, dict):
                message = str(error.get("message", ""))
                if "Execution context was destroyed" in message or "Inspected target navigated or closed" in message:
                    time.sleep(0.35 * attempt)
                    continue
            break
        if response.get("exceptionDetails"):
            raise AssertionError(f"browser eval exception for {url}: {response['exceptionDetails']}")
        result = response.get("result", {}).get("result", {}).get("value")
        if not isinstance(result, dict):
            raise AssertionError(f"browser eval returned non-object for {url}: {response}")
        if screenshot_path is not None:
            screenshot_path.parent.mkdir(parents=True, exist_ok=True)
            capture = devtools_call(
                parsed_debug_url,
                {
                    "id": 101,
                    "method": "Page.captureScreenshot",
                    "params": {"format": "png", "fromSurface": True, "captureBeyondViewport": False},
                },
                timeout,
            )
            encoded = capture.get("result", {}).get("data")
            if not isinstance(encoded, str) or not encoded:
                raise AssertionError(f"browser screenshot returned no image for {url}: {capture}")
            screenshot_path.write_bytes(base64.b64decode(encoded))
        return result
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)


def capture_v3_baseline(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    startup_ms: int,
    output_path: Path | None,
    enforce_performance: bool = False,
) -> dict[str, object]:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const deadline = Date.now() + 8000;
          while (Date.now() < deadline && !document.querySelector(".chat-grid")) {
            await new Promise(resolve => setTimeout(resolve, 100));
          }
          const entries = performance.getEntriesByType("resource");
          const paths = entries.map(entry => new URL(entry.name).pathname);
          const apiPaths = paths.filter(path => path.startsWith("/api/"));
          const scriptEntries = entries.filter(entry => new URL(entry.name).pathname.endsWith(".js"));
          const styleEntries = entries.filter(entry => new URL(entry.name).pathname.endsWith(".css"));
          const body = document.body;
          const main = document.querySelector("main") || document.querySelector(".main") || body;
          const domElements = document.querySelectorAll("*").length;
          const documentScrollHeight = document.documentElement.scrollHeight;
          const mainScrollHeight = main.scrollHeight;
          const navigation = performance.getEntriesByType("navigation")[0];
          const drawer = document.querySelector("#taskHistoryDrawer");
          const drawerToggle = document.querySelector("#taskDrawerToggle");
          const drawerClose = document.querySelector("#taskDrawerClose");
          let taskDrawerOpenResponseMS = null;
          if (drawer && drawerToggle && drawerClose) {
            if (drawer.classList.contains("open")) drawerClose.click();
            const started = performance.now();
            drawerToggle.click();
            await new Promise(resolve => requestAnimationFrame(resolve));
            taskDrawerOpenResponseMS = Math.round((performance.now() - started) * 100) / 100;
            drawerClose.click();
            await new Promise(resolve => requestAnimationFrame(resolve));
          }
          const prompt = document.querySelector("#prompt");
          let composerFocusResponseMS = null;
          if (prompt) {
            const started = performance.now();
            prompt.focus();
            await new Promise(resolve => requestAnimationFrame(resolve));
            composerFocusResponseMS = Math.round((performance.now() - started) * 100) / 100;
          }
          return {
            page_ready: Boolean(document.querySelector(".chat-grid")),
            navigation_entries: performance.getEntriesByType("navigation").length,
            navigation_dom_interactive_ms: Math.round(Number(navigation?.domInteractive || 0)),
            navigation_load_event_ms: Math.round(Number(navigation?.loadEventEnd || 0)),
            task_page_interactive_ms: Number(globalThis.__GOFLOW_PERFORMANCE__?.taskPageInteractiveMS || 0),
            task_drawer_open_response_ms: taskDrawerOpenResponseMS,
            composer_focus_response_ms: composerFocusResponseMS,
            resource_requests: entries.length,
            api_requests: apiPaths.length,
            api_paths: [...new Set(apiPaths)].sort(),
            javascript_requests: scriptEntries.length,
            javascript_decoded_bytes: Math.round(scriptEntries.reduce((sum, entry) => sum + Number(entry.decodedBodySize || entry.transferSize || 0), 0)),
            stylesheet_requests: styleEntries.length,
            stylesheet_decoded_bytes: Math.round(styleEntries.reduce((sum, entry) => sum + Number(entry.decodedBodySize || entry.transferSize || 0), 0)),
            dom_elements: domElements,
            horizontal_overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 2,
            document_scroll_height: documentScrollHeight,
            main_scroll_height: mainScrollHeight,
            viewport_width: window.innerWidth,
            viewport_height: window.innerHeight,
            ready_state: document.readyState
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
    )
    metrics["schema_version"] = "goflow.v3-baseline.v1"
    metrics["captured_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    metrics["server_startup_ms"] = startup_ms
    metrics["browser"] = str(browser)
    if not metrics.get("page_ready") or metrics.get("horizontal_overflow"):
        raise AssertionError(f"task-page baseline failed readiness or overflow gate: {metrics}")
    if enforce_performance:
        # Shared CI runners are slower than a developer desktop. The scale keeps
        # the same relative gate while allowing an explicit, documented margin.
        try:
            performance_scale = float(os.environ.get("GOFLOW_V3_PERF_SCALE", "1") or "1")
        except ValueError:
            performance_scale = 1.0
        performance_scale = performance_scale if performance_scale >= 1 else 1.0
        gates = {
            "server_startup_ms": 700 * performance_scale,
            "task_page_interactive_ms": 700 * performance_scale,
            "task_drawer_open_response_ms": 100 * performance_scale,
            "composer_focus_response_ms": 100 * performance_scale,
        }
        failures = {
            name: {"actual": metrics.get(name), "limit": limit}
            for name, limit in gates.items()
            if not isinstance(metrics.get(name), (int, float))
            or float(metrics[name]) <= 0
            or float(metrics[name]) >= limit
        }
        if failures:
            raise AssertionError(f"V3 performance gate failed: {failures}; metrics={metrics}")
    if output_path is not None:
        output_path = output_path.resolve()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(metrics, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return metrics


def wait_for_debug_page(port: int, expected_url: str, proc: subprocess.Popen[str], timeout: float) -> dict[str, object]:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        if proc.poll() is not None:
            output = proc.stdout.read() if proc.stdout is not None else ""
            raise BrowserSmokeUnavailable(f"debug browser exited before evaluation\n{output[-2000:]}")
        try:
            status, text = request_text(f"http://127.0.0.1:{port}/json", timeout=2.0)
            if status == 200:
                pages = json.loads(text)
                for page in pages:
                    if str(page.get("url", "")).startswith(expected_url.split("#", 1)[0]):
                        return page
        except Exception as exc:  # noqa: BLE001 - startup diagnostics matter.
            last_error = str(exc)
        time.sleep(0.2)
    raise BrowserSmokeUnavailable(f"debug browser did not expose page for {expected_url}: {last_error}")


def devtools_call(parsed_url: urllib.parse.ParseResult, payload: dict[str, object], timeout: float) -> dict[str, object]:
    host = parsed_url.hostname or "127.0.0.1"
    port = parsed_url.port or 80
    path = parsed_url.path or "/"
    key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
    request = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        "\r\n"
    ).encode("ascii")
    with socket.create_connection((host, port), timeout=timeout) as sock:
        sock.settimeout(timeout)
        sock.sendall(request)
        response = b""
        while b"\r\n\r\n" not in response:
            response += sock.recv(4096)
        if b" 101 " not in response.split(b"\r\n", 1)[0]:
            raise AssertionError(f"DevTools websocket handshake failed: {response[:200]!r}")
        send_websocket_text(sock, json.dumps(payload))
        while True:
            message = recv_websocket_text(sock)
            parsed = json.loads(message)
            if parsed.get("id") == payload.get("id"):
                return parsed


def capture_primary_page_screenshots(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    output_dir: Path,
) -> None:
    output_dir = output_dir.resolve()
    routes = {
        "tasks": "playground",
        "workflows": "workflows",
        "resources": "catalog",
        "settings": "settings",
    }
    viewports = {
        "desktop": (1440, 1000),
        "mobile": (390, 844),
    }
    for page_name, route in routes.items():
        for theme_name in ("light", "dark"):
            for viewport_name, viewport in viewports.items():
                screenshot_name = f"{page_name}-{viewport_name}.png" if theme_name == "light" else f"{page_name}-dark-{viewport_name}.png"
                metrics = evaluate_browser_json(
                    browser,
                    f"{base_url}/console?lang=zh#{route}",
                    f"""
                (async () => {{
                  if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                    window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                    await new Promise(resolve => setTimeout(resolve, 250));
                  }}
                  if ({json.dumps(page_name)} === "tasks" && document.querySelector("#taskHistoryDrawer")?.classList.contains("open")) {{
                    document.querySelector("#taskDrawerClose")?.click();
                    await new Promise(resolve => setTimeout(resolve, 180));
                  }}
                  if ({json.dumps(page_name)} === "workflows") {{
                    const studio = document.querySelector(".studio");
                    if (studio && (!studio.classList.contains("left-rail-collapsed") || studio.classList.contains("inspector-open"))) {{
                      document.querySelector("#workflowDrawerBackdrop")?.click();
                      await new Promise(resolve => setTimeout(resolve, 180));
                    }}
                  }}
                  return {{
                  route: location.hash,
                  theme: document.documentElement.dataset.theme,
                  viewport: [innerWidth, innerHeight],
                  scrollY: Math.round(window.scrollY),
                  scrollWidth: document.documentElement.scrollWidth,
                  overflowX: document.documentElement.scrollWidth > innerWidth,
                  studioClass: document.querySelector(".studio")?.className || "",
                  workflowZoom: document.querySelector("#zoomLabel")?.textContent || "",
                  workflowNodeWidth: Math.round(document.querySelector(".flow-node")?.getBoundingClientRect().width || 0),
                  appText: (document.querySelector("#app")?.textContent || "").trim().length
                  }};
                }})()
                """,
                    timeout,
                    profile_dir,
                    viewport=viewport,
                    screenshot_path=output_dir / screenshot_name,
                )
                if metrics.get("theme") != theme_name:
                    raise AssertionError(f"{page_name} {theme_name} {viewport_name} screenshot has the wrong theme: {metrics}")
                if metrics.get("overflowX"):
                    raise AssertionError(f"{page_name} {theme_name} {viewport_name} screenshot has horizontal overflow: {metrics}")
                if int(metrics.get("scrollY") or 0) > 2:
                    raise AssertionError(f"{page_name} {theme_name} {viewport_name} should open at the top of the page: {metrics}")
                if page_name == "workflows" and viewport_name == "mobile" and "left-rail-collapsed" not in str(metrics.get("studioClass") or ""):
                    raise AssertionError(f"workflow {theme_name} mobile screenshot should start with the compact build palette: {metrics}")
                if page_name == "workflows":
                    minimum_zoom = 75 if viewport_name == "mobile" else 65
                    zoom_text = str(metrics.get("workflowZoom") or "0").strip().rstrip("%")
                    try:
                        zoom_value = int(zoom_text)
                    except ValueError:
                        zoom_value = 0
                    if zoom_value < minimum_zoom:
                        raise AssertionError(f"workflow {theme_name} {viewport_name} nodes are scaled below the readable limit: {metrics}")
                    minimum_node_width = 210 if viewport_name == "mobile" else 185
                    if int(metrics.get("workflowNodeWidth") or 0) < minimum_node_width:
                        raise AssertionError(f"workflow {theme_name} {viewport_name} nodes are too narrow to read: {metrics}")
                if int(metrics.get("appText") or 0) < 20:
                    raise AssertionError(f"{page_name} {theme_name} {viewport_name} screenshot rendered an empty app: {metrics}")

    drawer_states = {
        "workflow-library": "document.querySelector('#workflowLibraryToggle')?.click()",
        "workflow-inspector": "document.querySelector('.flow-node')?.click()",
    }
    for state_name, open_script in drawer_states.items():
        for theme_name in ("light", "dark"):
            metrics = evaluate_browser_json(
                browser,
                f"{base_url}/console?lang=zh#workflows",
                f"""
                (async () => {{
                  if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                    window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                    await new Promise(resolve => setTimeout(resolve, 250));
                  }}
                  const studio = document.querySelector('.studio');
                  if (studio && (!studio.classList.contains('left-rail-collapsed') || studio.classList.contains('inspector-open'))) {{
                    document.querySelector('#workflowDrawerBackdrop')?.click();
                    await new Promise(resolve => setTimeout(resolve, 180));
                  }}
                  {open_script};
                  await new Promise(resolve => setTimeout(resolve, 260));
                  return {{
                    theme: document.documentElement.dataset.theme,
                    libraryOpen: !document.querySelector('.studio')?.classList.contains('left-rail-collapsed'),
                    inspectorOpen: document.querySelector('.studio')?.classList.contains('inspector-open') || false,
                    overflowX: document.documentElement.scrollWidth > innerWidth
                  }};
                }})()
                """,
                timeout,
                profile_dir,
                viewport=viewports["desktop"],
                screenshot_path=output_dir / f"{state_name}-{'dark-' if theme_name == 'dark' else ''}desktop.png",
            )
            expected_open = metrics.get("libraryOpen") if state_name == "workflow-library" else metrics.get("inspectorOpen")
            if not expected_open or metrics.get("overflowX") or metrics.get("theme") != theme_name:
                raise AssertionError(f"{state_name} {theme_name} screenshot failed drawer checks: {metrics}")

    for theme_name in ("light", "dark"):
        metrics = evaluate_browser_json(
            browser,
            f"{base_url}/console?lang=zh#catalog",
            f"""
            (async () => {{
              if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                await new Promise(resolve => setTimeout(resolve, 250));
              }}
              document.querySelector('[data-resource-kind="mcp"]')?.click();
              await new Promise(resolve => setTimeout(resolve, 100));
              document.querySelector('[data-new-resource="mcp"]')?.click();
              await new Promise(resolve => setTimeout(resolve, 220));
              const advanced = document.querySelector('.resource-connection-form .resource-advanced-section');
              return {{
                theme: document.documentElement.dataset.theme,
                title: document.querySelector('#designerTitle')?.textContent || '',
                commandVisible: Boolean(document.querySelector('#mcpCommand')),
                advancedClosed: Boolean(advanced && !advanced.open),
                hasWizard: Boolean(document.querySelector('[data-mcp-step], [data-mcp-next]')),
                overflowX: document.documentElement.scrollWidth > innerWidth
              }};
            }})()
            """,
            timeout,
            profile_dir,
            viewport=viewports["desktop"],
            screenshot_path=output_dir / f"resource-connection-editor-{'dark-' if theme_name == 'dark' else ''}desktop.png",
        )
        if (
            metrics.get("theme") != theme_name
            or "MCP 连接" not in str(metrics.get("title") or "")
            or not metrics.get("commandVisible")
            or not metrics.get("advancedClosed")
            or metrics.get("hasWizard")
            or metrics.get("overflowX")
        ):
            raise AssertionError(f"resource connection editor {theme_name} screenshot failed checks: {metrics}")


def assert_usage_dashboard(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    output_dir: Path | None = None,
) -> None:
    screenshot_path = output_dir / "usage-desktop.png" if output_dir is not None else None
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#usage",
        """
        (async () => {
          const deadline = Date.now() + 10000;
          while (Date.now() < deadline && !document.querySelector('.usage-dashboard')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const targets = Array.from(document.querySelectorAll('[data-usage-chart="composition"] .usage-hit-target'));
          const hoverTarget = targets.at(-1);
          if (hoverTarget) {
            document.querySelector('[data-usage-chart="composition"]')?.scrollIntoView({ block: 'center', inline: 'nearest' });
            await new Promise(resolve => setTimeout(resolve, 100));
            const rect = hoverTarget.getBoundingClientRect();
            hoverTarget.dispatchEvent(new PointerEvent('pointerenter', { bubbles: true, clientX: rect.left + rect.width / 2, clientY: rect.top + 32 }));
            await new Promise(resolve => setTimeout(resolve, 80));
          }
          const tooltip = document.querySelector('[data-usage-chart="composition"] .usage-chart-tooltip');
          const colorOf = selector => {
            const node = document.querySelector(selector);
            return node ? getComputedStyle(node).backgroundColor : '';
          };
          const tokenValues = Array.from(document.querySelectorAll('.usage-metric strong')).slice(0, 5).map(node => node.textContent.trim());
          const rangeButtons = Array.from(document.querySelectorAll('[data-usage-range]'));
          const initialSelected = rangeButtons.filter(button => button.getAttribute('aria-pressed') === 'true').map(button => button.dataset.usageRange);
          const range24 = rangeButtons.find(button => button.dataset.usageRange === '24h');
          range24?.click();
          const rangeDeadline = Date.now() + 5000;
          while (Date.now() < rangeDeadline && (document.querySelector('[data-usage-range="24h"]')?.getAttribute('aria-pressed') !== 'true' || !document.querySelector('.usage-dashboard'))) {
            await new Promise(resolve => setTimeout(resolve, 100));
          }
          const currentRangeButtons = Array.from(document.querySelectorAll('[data-usage-range]'));
          const selectedAfter = currentRangeButtons.filter(button => button.getAttribute('aria-pressed') === 'true').map(button => button.dataset.usageRange);
          const selectedRange = currentRangeButtons.find(button => button.dataset.usageRange === '24h');
          const inactiveRange = currentRangeButtons.find(button => button.dataset.usageRange === '30d');
          const selectedStyle = selectedRange ? getComputedStyle(selectedRange) : null;
          const inactiveStyle = inactiveRange ? getComputedStyle(inactiveRange) : null;
          const refreshedTarget = Array.from(document.querySelectorAll('[data-usage-chart="composition"] .usage-hit-target')).at(-1);
          if (refreshedTarget) {
            const rect = refreshedTarget.getBoundingClientRect();
            refreshedTarget.dispatchEvent(new PointerEvent('pointerenter', { bubbles: true, clientX: rect.left + rect.width / 2, clientY: rect.top + 32 }));
            await new Promise(resolve => setTimeout(resolve, 100));
          }
          const refreshedTooltip = document.querySelector('[data-usage-chart="composition"] .usage-chart-tooltip');
          const inputToggle = document.querySelector('[data-usage-legend="trend"] [data-usage-series="input"]');
          const inputPlot = document.querySelector('[data-usage-chart="trend"] [data-usage-plot-series="input"]');
          inputToggle?.click();
          await new Promise(resolve => setTimeout(resolve, 220));
          const toggleHidden = Boolean(inputToggle?.getAttribute('aria-pressed') === 'false' && inputToggle.classList.contains('inactive') && inputPlot?.classList.contains('series-hidden'));
          const inactiveOpacity = inputToggle ? Number.parseFloat(getComputedStyle(inputToggle).opacity || '1') : 1;
          inputToggle?.click();
          await new Promise(resolve => setTimeout(resolve, 220));
          const toggleRestored = Boolean(inputToggle?.getAttribute('aria-pressed') === 'true' && !inputPlot?.classList.contains('series-hidden'));
          return {
            activeView: document.querySelector('#app')?.dataset.view || '',
            navActive: document.querySelector('[data-view="usage"]')?.classList.contains('active') || false,
            metrics: document.querySelectorAll('.usage-metric').length,
            rangeButtons: rangeButtons.map(button => button.dataset.usageRange),
            initialSelected,
            selectedAfter,
            rangeSelectionDistinct: Boolean(selectedStyle && inactiveStyle && (selectedStyle.backgroundColor !== inactiveStyle.backgroundColor || selectedStyle.borderColor !== inactiveStyle.borderColor || selectedStyle.boxShadow !== inactiveStyle.boxShadow)),
            charts: document.querySelectorAll('.usage-chart-svg').length,
            agentRows: document.querySelectorAll('.usage-agent-row').length,
            tokenValues,
            unitSafe: tokenValues.filter(value => value !== '-').every(value => /(?:K|M|B)(?:\\s|$)/.test(value)),
            tooltipVisible: Boolean(refreshedTooltip && !refreshedTooltip.classList.contains('hidden')),
            tooltipText: refreshedTooltip?.textContent || '',
            colorLinks: {
              total: [colorOf('.usage-legend .total i'), colorOf('.usage-tooltip-dot.total')],
              input: [colorOf('.usage-legend .input i'), colorOf('.usage-tooltip-dot.input')],
              cached: [colorOf('.usage-legend .cached i'), colorOf('.usage-tooltip-dot.cached')],
              output: [colorOf('.usage-legend .output i'), colorOf('.usage-tooltip-dot.output')]
            },
            toggleHidden,
            toggleRestored,
            inactiveOpacity,
            englishLeak: (document.querySelector('.usage-page')?.textContent || '').includes('Usage is aggregated from the retained Agent and Workflow token_usage timeline events'),
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
        screenshot_path=screenshot_path,
    )
    if metrics.get("activeView") != "usage" or not metrics.get("navActive"):
        raise AssertionError(f"usage route did not activate, got {metrics}")
    if metrics.get("metrics") != 6 or metrics.get("rangeButtons") != ["24h", "7d", "30d"]:
        raise AssertionError(f"usage controls or metrics are incomplete, got {metrics}")
    if metrics.get("initialSelected") != ["7d"] or metrics.get("selectedAfter") != ["24h"] or not metrics.get("rangeSelectionDistinct"):
        raise AssertionError(f"usage range selection must be visibly and accessibly distinct, got {metrics}")
    if int(metrics.get("charts") or 0) < 2 or int(metrics.get("agentRows") or 0) < 1:
        raise AssertionError(f"usage charts did not render recorded timeline data, got {metrics}")
    if not metrics.get("unitSafe") or metrics.get("overflowX"):
        raise AssertionError(f"usage token units or desktop layout are invalid, got {metrics}")
    tooltip_text = str(metrics.get("tooltipText") or "")
    if not metrics.get("tooltipVisible") or any(label not in tooltip_text for label in ("输入", "输出", "缓存命中", "缓存写入", "缓存命中率")):
        raise AssertionError(f"usage chart tooltip is incomplete, got {metrics}")
    color_links = metrics.get("colorLinks") or {}
    if any(not values or len(values) != 2 or not values[0] or values[0] != values[1] for values in color_links.values()):
        raise AssertionError(f"usage tooltip colors must match chart legends, got {metrics}")
    if not metrics.get("toggleHidden") or not metrics.get("toggleRestored") or float(metrics.get("inactiveOpacity") or 1) >= 0.6:
        raise AssertionError(f"usage legends must hide, dim, and restore chart series, got {metrics}")
    if metrics.get("englishLeak"):
        raise AssertionError(f"usage page still exposes the English retention note, got {metrics}")

    dark_screenshot_path = output_dir / "usage-dark-desktop.png" if output_dir is not None else None
    dark_metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#usage",
        """
        (async () => {
          const deadline = Date.now() + 10000;
          while (Date.now() < deadline && !document.querySelector('.usage-dashboard')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          if (document.documentElement.dataset.theme !== 'dark') {
            window.dispatchEvent(new CustomEvent('goflow:set-theme', { detail: { theme: 'dark' } }));
            await new Promise(resolve => setTimeout(resolve, 240));
          }
          const shell = document.querySelector('[data-usage-chart="composition"]');
          const target = shell?.querySelector('.usage-hit-target:last-of-type');
          shell?.scrollIntoView({ block: 'center', inline: 'nearest' });
          await new Promise(resolve => setTimeout(resolve, 100));
          if (target) {
            const rect = target.getBoundingClientRect();
            target.dispatchEvent(new PointerEvent('pointerenter', { bubbles: true, clientX: rect.left + rect.width / 2, clientY: rect.top + 32 }));
            await new Promise(resolve => setTimeout(resolve, 100));
          }
          const styleValue = (selector, property) => {
            const node = document.querySelector(selector);
            return node ? getComputedStyle(node)[property] : '';
          };
          const series = {
            total: styleValue('.usage-line.total', 'stroke'),
            input: styleValue('.usage-line.input', 'stroke'),
            cached: styleValue('.usage-line.cached', 'stroke'),
            output: styleValue('.usage-line.output', 'stroke')
          };
          return {
            theme: document.documentElement.dataset.theme,
            series,
            uniqueSeries: new Set(Object.values(series)).size,
            chartBackground: styleValue('.usage-svg-shell', 'backgroundColor'),
            axisColor: styleValue('.usage-chart-svg text', 'fill'),
            gridStroke: styleValue('.usage-chart-svg .grid', 'stroke'),
            tooltipBackground: styleValue('.usage-chart-tooltip:not(.hidden)', 'backgroundColor'),
            tooltipVisible: Boolean(document.querySelector('.usage-chart-tooltip:not(.hidden)')),
            tooltipText: document.querySelector('.usage-chart-tooltip:not(.hidden)')?.textContent || '',
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
        screenshot_path=dark_screenshot_path,
    )

    def color_is_visible(value: object) -> bool:
        channels = [int(item) for item in re.findall(r"\d+", str(value or ""))[:3]]
        return len(channels) == 3 and max(channels) >= 150 and sum(channels) >= 260

    series_colors = (dark_metrics.get("series") or {}).values()
    if (
        dark_metrics.get("theme") != "dark"
        or int(dark_metrics.get("uniqueSeries") or 0) != 4
        or not all(color_is_visible(value) for value in series_colors)
        or not color_is_visible(dark_metrics.get("axisColor"))
        or not dark_metrics.get("tooltipVisible")
        or dark_metrics.get("overflowX")
    ):
        raise AssertionError(f"usage dark theme contrast or interaction is invalid, got {dark_metrics}")
    if any(label not in str(dark_metrics.get("tooltipText") or "") for label in ("输入", "输出", "缓存命中", "缓存写入")):
        raise AssertionError(f"usage dark theme tooltip is incomplete, got {dark_metrics}")


def assert_task_controls_and_result_styles(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    output_dir: Path | None = None,
) -> None:
    for theme_name in ("light", "dark"):
        screenshot_path = None
        if output_dir is not None:
            output_dir.mkdir(parents=True, exist_ok=True)
            screenshot_path = output_dir / f"tasks-result-style-{'dark-' if theme_name == 'dark' else ''}desktop.png"
        metrics = evaluate_browser_json(
            browser,
            f"{base_url}/console?lang=zh#playground",
            f"""
            (async () => {{
              if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                await new Promise(resolve => setTimeout(resolve, 220));
              }}
              const totals = document.querySelector("#runTimelineTotals");
              if (totals) {{
                totals.classList.remove("hidden");
                totals.innerHTML = '<div><strong>运行总计</strong><span>3 次模型调用</span></div><dl><div><dt>输入</dt><dd>12.4K</dd></div><div><dt>输出</dt><dd>1.8K</dd></div><div><dt>缓存</dt><dd>8.2K</dd></div><div><dt>命中率</dt><dd>66%</dd></div><div><dt>总计</dt><dd>14.2K</dd></div><div><dt>耗时</dt><dd>38 秒</dd></div></dl>';
              }}
              const panel = document.querySelector(".run-results");
              const empty = document.querySelector("#resultEmpty");
              const output = document.querySelector("#resultOutput");
              panel?.classList.remove("hidden");
              empty?.classList.add("hidden");
              output?.classList.remove("hidden");
              if (output) output.innerHTML = '<div class="result-section-label">最终答案</div><div class="result-card markdown final"><h2>验证结果</h2><p>结构化产出应当易读，并清楚区分正文、代码和数据。</p><ol><li>第一项</li><li>第二项</li><li>第三项</li><li>第四项</li><li>第五项</li><li>第六项完整可见</li></ol><pre><span>json</span><code>{{\\n  &quot;status&quot;: &quot;passed&quot;,\\n  &quot;tests&quot;: 18,\\n  &quot;cached_tokens&quot;: 8200\\n}}</code></pre><table><thead><tr><th>检查项</th><th>结果</th></tr></thead><tbody><tr><td>审批实时刷新</td><td>通过</td></tr><tr><td>对话缓存</td><td>稳定前缀</td></tr></tbody></table></div>';
              output?.scrollIntoView({{ block: "center", inline: "nearest" }});
              await new Promise(resolve => setTimeout(resolve, 160));
              const code = document.querySelector(".result-card pre");
              const timeline = document.querySelector(".run-timeline");
              const permission = document.querySelector("#permissionMode");
              const reasoning = document.querySelector("#reasoningEffort");
              const runMode = document.querySelector("#runMode");
              const agent = document.querySelector("#agentSelect");
              runMode.value = "agent";
              runMode.dispatchEvent(new Event("change", {{ bubbles: true }}));
              const preferredAgent = Array.from(agent?.options || []).find(option => option.value === "software-engineer")
                || Array.from(agent?.options || []).find(option => option.value);
              agent.value = preferredAgent?.value || "";
              agent.dispatchEvent(new Event("change", {{ bubbles: true }}));
              await new Promise(resolve => setTimeout(resolve, 80));
              const agentField = document.querySelector("#agentField");
              return {{
                theme: document.documentElement.dataset.theme,
                overflowX: document.documentElement.scrollWidth > innerWidth,
                permissionOptions: Array.from(permission?.options || []).map(option => option.value),
                reasoningOptions: Array.from(reasoning?.options || []).map(option => option.value),
                runModeOptions: Array.from(runMode?.options || []).map(option => option.value),
                agentOptionCount: agent?.options?.length || 0,
                expectedAgent: preferredAgent?.value || "",
                selectedMode: runMode?.value || "",
                selectedAgent: agent?.value || "",
                agentSelectorVisible: Boolean(agentField && getComputedStyle(agentField).display !== "none" && agentField.getBoundingClientRect().height > 0),
                totalsInGrid: totals?.parentElement?.classList.contains("run-console-grid") || false,
                timelineHeight: Math.round(timeline?.getBoundingClientRect().height || 0),
                codeBackground: code ? getComputedStyle(code).backgroundColor : "",
                codeColor: code ? getComputedStyle(code).color : "",
                resultPanelMaxHeight: panel ? getComputedStyle(panel).maxHeight : "",
                resultOutputOverflowY: output ? getComputedStyle(output).overflowY : "",
                fullResultVisible: Boolean(output?.textContent.includes("第六项完整可见")),
                resultVisible: Boolean(panel && !panel.classList.contains("hidden") && code)
              }};
            }})()
            """,
            timeout,
            profile_dir,
            viewport=(1440, 1100),
            screenshot_path=screenshot_path,
        )
        if metrics.get("theme") != theme_name or metrics.get("overflowX"):
            raise AssertionError(f"task result {theme_name} layout failed: {metrics}")
        if metrics.get("permissionOptions") != ["confirm", "workspace", "full"]:
            raise AssertionError(f"task permission controls are incomplete: {metrics}")
        if metrics.get("reasoningOptions") != ["low", "mid", "high"]:
            raise AssertionError(f"task reasoning controls are incomplete: {metrics}")
        if metrics.get("runModeOptions") != ["auto", "agent", "workflow"] or int(metrics.get("agentOptionCount") or 0) < 1:
            raise AssertionError(f"task Agent selector is incomplete: {metrics}")
        if metrics.get("selectedMode") != "agent" or not metrics.get("expectedAgent") or metrics.get("selectedAgent") != metrics.get("expectedAgent") or not metrics.get("agentSelectorVisible"):
            raise AssertionError(f"task Agent selector cannot select a concrete Agent: {metrics}")
        if not metrics.get("totalsInGrid") or int(metrics.get("timelineHeight") or 0) < 500:
            raise AssertionError(f"task totals/timeline layout is not usable: {metrics}")
        if not metrics.get("resultVisible"):
            raise AssertionError(f"task result preview is not visible: {metrics}")
        if metrics.get("resultPanelMaxHeight") != "none" or metrics.get("resultOutputOverflowY") not in {"visible", "clip"} or not metrics.get("fullResultVisible"):
            raise AssertionError(f"task final output should render completely without an inner clipped viewport: {metrics}")
        background = str(metrics.get("codeBackground") or "")
        channels = [int(value) for value in re.findall(r"\d+", background)[:3]]
        if len(channels) != 3:
            raise AssertionError(f"task code surface has no computed color: {metrics}")
        average = sum(channels) / 3
        if theme_name == "light" and average < 180:
            raise AssertionError(f"light task code surface is still too dark: {metrics}")
        if theme_name == "dark" and average < 24:
            raise AssertionError(f"dark task code surface is too close to black: {metrics}")


def assert_staged_delivery_action_layout(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    smoke_run: dict,
    output_dir: Path | None = None,
) -> None:
    run_id = str(smoke_run["run_id"])
    viewports = {"desktop": (1440, 1000), "mobile": (390, 844)}
    for theme_name in ("light", "dark"):
        for viewport_name, viewport in viewports.items():
            screenshot_path = None
            if output_dir is not None:
                output_dir.mkdir(parents=True, exist_ok=True)
                suffix = f"{'dark-' if theme_name == 'dark' else ''}{viewport_name}"
                screenshot_path = output_dir / f"tasks-staged-delivery-{suffix}.png"
            metrics = evaluate_browser_json(
                browser,
                f"{base_url}/console?lang=zh#playground",
                f"""
                (async () => {{
                  const fixtureKey = "goflow.smoke.stagedDelivery";
                  if (localStorage.getItem(fixtureKey) !== {json.dumps(run_id)}) {{
                    localStorage.setItem("goflow.playground.runState", JSON.stringify({{
                      mode: "agent",
                      runID: {json.dumps(run_id)},
                      status: "completed",
                      updatedAt: Date.now()
                    }}));
                    localStorage.setItem(fixtureKey, {json.dumps(run_id)});
                    location.reload();
                    await new Promise(resolve => setTimeout(resolve, 2000));
                  }}
                  const deadline = Date.now() + 8000;
                  while (Date.now() < deadline && !document.querySelector('[data-agent-action="apply_staged_changes"]')) {{
                    await new Promise(resolve => setTimeout(resolve, 120));
                  }}
                  if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                    window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                    await new Promise(resolve => setTimeout(resolve, 250));
                  }}
                  if (document.body.classList.contains("task-drawer-open")) {{
                    document.querySelector("#taskDrawerBackdrop")?.click();
                    await new Promise(resolve => setTimeout(resolve, 250));
                  }}
                  const attention = document.querySelector("#runAttention");
                  attention?.scrollIntoView({{ block: "center", inline: "nearest" }});
                  await new Promise(resolve => setTimeout(resolve, 120));
                  const apply = attention?.querySelector('[data-agent-action="apply_staged_changes"]');
                  const discard = attention?.querySelector('[data-agent-action="discard_staged_changes"]');
                  const retry = attention?.querySelector('[data-agent-action="retry"]');
                  const summary = attention?.querySelector(".run-action-summary");
                  const insideViewport = element => {{
                    if (!element) return false;
                    const rect = element.getBoundingClientRect();
                    return rect.width > 0 && rect.height > 0 && rect.left >= 0 && rect.right <= innerWidth;
                  }};
                  return {{
                    theme: document.documentElement.dataset.theme,
                    viewport: [innerWidth, innerHeight],
                    overflowX: document.documentElement.scrollWidth > innerWidth,
                    attentionText: attention?.textContent || "",
                    summaryText: summary?.textContent || "",
                    timelineHint: document.querySelector("#runTimelineHint")?.textContent || "",
                    resultStatus: document.querySelector("#resultStatus")?.textContent || "",
                    applyVisible: insideViewport(apply),
                    applyDisabled: apply ? apply.disabled : null,
                    discardVisible: insideViewport(discard),
                    retryVisible: Boolean(retry),
                    attentionVisible: Boolean(attention && !attention.classList.contains("hidden"))
                  }};
                }})()
                """,
                timeout,
                profile_dir,
                viewport=viewport,
                screenshot_path=screenshot_path,
            )
            actual_viewport = metrics.get("viewport") or [0, 0]
            viewport_ok = (
                1200 <= int(actual_viewport[0]) <= 1500 and int(actual_viewport[1]) >= 700
                if viewport_name == "desktop"
                else 320 <= int(actual_viewport[0]) <= 540 and int(actual_viewport[1]) >= 640
            )
            if metrics.get("theme") != theme_name or not viewport_ok or metrics.get("overflowX"):
                raise AssertionError(f"staged delivery {theme_name} {viewport_name} layout failed: {metrics}")
            if not metrics.get("attentionVisible") or not metrics.get("applyVisible") or metrics.get("applyDisabled") or not metrics.get("discardVisible"):
                raise AssertionError(f"staged delivery actions are not directly usable: {metrics}")
            if not metrics.get("retryVisible"):
                raise AssertionError(f"current task actions should expose Retry without opening history: {metrics}")
            summary = str(metrics.get("summaryText", ""))
            if "1 个文件变更" not in summary or "质量门" not in summary:
                raise AssertionError(f"staged delivery summary should expose count and quality status: {metrics}")
            if "等待应用已验证变更" not in str(metrics.get("timelineHint", "")) or "待应用" not in str(metrics.get("resultStatus", "")):
                raise AssertionError(f"staged delivery should not look fully delivered before apply: {metrics}")


def send_websocket_text(sock: socket.socket, text: str) -> None:
    data = text.encode("utf-8")
    header = bytearray([0x81])
    length = len(data)
    if length < 126:
        header.append(0x80 | length)
    elif length < 65536:
        header.extend([0x80 | 126, *struct.pack("!H", length)])
    else:
        header.extend([0x80 | 127, *struct.pack("!Q", length)])
    mask = secrets.token_bytes(4)
    masked = bytes(byte ^ mask[index % 4] for index, byte in enumerate(data))
    sock.sendall(bytes(header) + mask + masked)


def recv_websocket_text(sock: socket.socket) -> str:
    first = recv_exact(sock, 2)
    opcode = first[0] & 0x0F
    length = first[1] & 0x7F
    if length == 126:
        length = struct.unpack("!H", recv_exact(sock, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", recv_exact(sock, 8))[0]
    mask = b""
    if first[1] & 0x80:
        mask = recv_exact(sock, 4)
    data = recv_exact(sock, length)
    if mask:
        data = bytes(byte ^ mask[index % 4] for index, byte in enumerate(data))
    if opcode == 0x8:
        raise AssertionError("DevTools websocket closed before response")
    return data.decode("utf-8", errors="replace")


def recv_exact(sock: socket.socket, size: int) -> bytes:
    chunks = bytearray()
    while len(chunks) < size:
        chunk = sock.recv(size - len(chunks))
        if not chunk:
            raise AssertionError("DevTools websocket closed unexpectedly")
        chunks.extend(chunk)
    return bytes(chunks)


def assert_rendered(label: str, dom: str, needles: list[str]) -> None:
    lowered = dom.lower()
    if "common.errortitle" in lowered or "too much recursion" in lowered or "maximum call stack" in lowered:
        raise AssertionError(f"{label} rendered an error page or recursion failure\n{dom[-2000:]}")
    for needle in needles:
        if needle not in dom:
            raise AssertionError(f"{label} missing rendered marker {needle!r}\n{dom[-2000:]}")


def assert_playground_timeline_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (() => {
          const messages = document.querySelector("#messages");
          const timeline = document.querySelector(".run-timeline");
          const setup = document.querySelector(".run-brief");
          const output = document.querySelector(".run-output-stack");
          if (!messages || !timeline || !setup || !output) return { missing: true };
          const timelineHeightBefore = Math.round(timeline.getBoundingClientRect().height);
          if (!messages.dataset.smokeFilled) {
            messages.dataset.smokeFilled = "1";
            for (let i = 0; i < 36; i += 1) {
              const item = document.createElement("div");
              item.className = "timeline-event stage";
              item.innerHTML = '<div class="timeline-marker"></div><div class="timeline-card"><strong>Smoke timeline event</strong><p>Repeated event ' + i + '</p></div>';
              messages.appendChild(item);
            }
          }
          const messagesStyle = getComputedStyle(messages);
          const timelineStyle = getComputedStyle(timeline);
          const setupStyle = getComputedStyle(setup);
          const controls = Array.from(document.querySelectorAll(".chat-grid button"))
            .filter(button => !button.disabled && button.getClientRects().length > 0 && getComputedStyle(button).visibility !== "hidden")
            .map(button => ({
              id: button.id || button.dataset.runAction || button.dataset.agentAction || button.dataset.historyAction || "button",
              text: String(button.textContent || "").replace(/\\s+/g, " ").trim(),
              title: button.getAttribute("title") || "",
              aria: button.getAttribute("aria-label") || ""
            }));
          return {
            missing: false,
            overflowY: messagesStyle.overflowY,
            setupOverflowY: setupStyle.overflowY,
            messagesHeight: Math.round(messages.getBoundingClientRect().height),
            setupHeight: Math.round(setup.getBoundingClientRect().height),
            timelineHeight: Math.round(timeline.getBoundingClientRect().height),
            timelineHeightBefore,
            messagesScrollHeight: Math.round(messages.scrollHeight),
            messagesClientHeight: Math.round(messages.clientHeight),
            messagesMaxHeight: messagesStyle.maxHeight,
            timelineMaxHeight: timelineStyle.maxHeight,
            outputTop: Math.round(output.getBoundingClientRect().top),
            timelineBottom: Math.round(timeline.getBoundingClientRect().bottom),
            controls
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("playground timeline layout missing expected nodes")
    if metrics.get("overflowY") not in {"auto", "scroll"}:
        raise AssertionError(f"playground timeline should use an internal scroll viewport, got {metrics}")
    if metrics.get("setupOverflowY") in {"auto", "scroll"}:
        raise AssertionError(f"playground setup panel should show fully without internal scroll, got {metrics}")
    if int(metrics.get("messagesScrollHeight") or 0) <= int(metrics.get("messagesClientHeight") or 0) + 4:
        raise AssertionError(f"playground timeline should scroll when events exceed its fixed viewport, got {metrics}")
    if abs(int(metrics.get("timelineHeight") or 0) - int(metrics.get("timelineHeightBefore") or 0)) > 4:
        raise AssertionError(f"playground timeline height changed after events were added, got {metrics}")
    if not 400 <= int(metrics.get("timelineHeight") or 0) <= 660:
        raise AssertionError(f"playground timeline viewport height is outside the intended range, got {metrics}")
    if int(metrics.get("outputTop") or 0) + 2 < int(metrics.get("timelineBottom") or 0):
        raise AssertionError(f"playground output should remain below the expanded timeline, got {metrics}")
    controls = metrics.get("controls") or []
    if not controls:
        raise AssertionError(f"playground did not expose any visible controls, got {metrics}")
    for control in controls:
        hint = f"{control.get('text', '')} {control.get('title', '')} {control.get('aria', '')}"
        if not re.search(r"[\u3400-\u9fff]", hint):
            raise AssertionError(f"playground control is missing Chinese copy: {control}")
        if not control.get("title") or not control.get("aria"):
            raise AssertionError(f"playground control is missing Chinese hover/accessibility copy: {control}")


def assert_attachment_input_flow(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const waitFor = async (predicate, ms = 7000) => {
            const deadline = Date.now() + ms;
            while (Date.now() < deadline) {
              const value = predicate();
              if (value) return value;
              await new Promise(resolve => setTimeout(resolve, 50));
            }
            return null;
          };
          const input = await waitFor(() => document.querySelector('#attachmentInput'));
          const tray = document.querySelector('#attachmentTray');
          const prompt = document.querySelector('#prompt');
          if (!input || !tray || !prompt) return { missing: true };
          const encoded = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZlkwAAAAASUVORK5CYII=';
          const bytes = Uint8Array.from(atob(encoded), value => value.charCodeAt(0));
          const exercise = async (mode, name) => {
            const file = new File([bytes], name, { type: 'image/png' });
            const transfer = new DataTransfer();
            transfer.items.add(file);
            const started = performance.now();
            let event = null;
            if (mode === 'input') {
              input.files = transfer.files;
              event = new Event('change', { bubbles: true, cancelable: true });
              input.dispatchEvent(event);
            } else {
              event = new Event(mode, { bubbles: true, cancelable: true });
              Object.defineProperty(event, mode === 'paste' ? 'clipboardData' : 'dataTransfer', { value: transfer });
              prompt.dispatchEvent(event);
            }
            const immediate = Boolean(tray.querySelector('.attachment-chip img'));
            const previewResponseMS = Math.round((performance.now() - started) * 100) / 100;
            const ready = await waitFor(() => tray.querySelector('.attachment-chip.ready'));
            const image = tray.querySelector('.attachment-chip.ready img');
            const remove = tray.querySelector('.attachment-chip.ready [data-remove-attachment]');
            const result = {
              immediate,
              previewResponseMS,
              ready: Boolean(ready),
              imageVisible: Boolean(image && image.getBoundingClientRect().width > 0),
              nameVisible: tray.textContent.includes(name),
              removeEnabled: Boolean(remove && !remove.disabled),
              defaultPrevented: mode === 'input' ? true : event.defaultPrevented
            };
            remove?.click();
            result.removed = Boolean(await waitFor(() => !tray.querySelector('.attachment-chip')));
            return result;
          };
          return {
            missing: false,
            input: await exercise('input', '选择文件截图.png'),
            paste: await exercise('paste', '粘贴截图.png'),
            drop: await exercise('drop', '拖入截图.png')
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    required = ["immediate", "ready", "imageVisible", "nameVisible", "removeEnabled", "removed", "defaultPrevented"]
    failures: dict[str, object] = {}
    for mode in ("input", "paste", "drop"):
        result = metrics.get(mode)
        if not isinstance(result, dict):
            failures[mode] = "missing result"
            continue
        missing = [name for name in required if not result.get(name)]
        preview_ms = result.get("previewResponseMS")
        if missing or not isinstance(preview_ms, (int, float)) or preview_ms >= 150:
            failures[mode] = {"missing": missing, "previewResponseMS": preview_ms}
    if metrics.get("missing") or failures:
        raise AssertionError(f"attachment input flow failed: {failures}; metrics={metrics}")


def assert_approval_queue_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#approvals",
        """
        (() => {
          const list = document.querySelector("#approvalList");
          if (!list) return { missing: true };
            const card = document.createElement("button");
            card.type = "button";
            card.className = "item approval-card";
            card.innerHTML = '<div class="approval-card-head"><strong>write_file</strong><span class="badge warn">Tool</span></div><div class="approval-card-answers"><span><small>Will run</small><b>write_file</b></span><span><small>May touch</small><b>Workspace files</b></span><span><small>Why approval</small><b>Protected tool call</b></span></div><p>path=demo.py content=1200 chars</p><small>call-1</small>';
            const group = document.createElement("section");
            group.className = "approval-group";
            group.innerHTML = '<div class="approval-group-head"><div><strong>Tool queue</strong><span>1 pending / Tool approval</span></div><span class="badge warn">Needs decision</span></div><div class="approval-group-list"></div>';
            group.querySelector(".approval-group-list").appendChild(card);
            list.appendChild(group);
            const listStyle = getComputedStyle(list);
            return {
              missing: false,
              listHeight: Math.round(list.getBoundingClientRect().height),
              groupHeight: Math.round(group.getBoundingClientRect().height),
              cardHeight: Math.round(card.getBoundingClientRect().height),
              alignContent: listStyle.alignContent,
              gridAutoRows: listStyle.gridAutoRows
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("approval queue layout missing expected nodes")
    card_height = int(metrics.get("cardHeight") or 0)
    list_height = int(metrics.get("listHeight") or 0)
    if card_height <= 0 or list_height <= 0:
        raise AssertionError(f"approval queue layout did not measure correctly, got {metrics}")
    if card_height > 280 or card_height > max(280, list_height // 2):
        raise AssertionError(f"single approval card should not stretch to fill the queue, got {metrics}")


def assert_ordinary_user_decision_surfaces(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#approvals",
        """
        (() => {
          const group = document.querySelector(".approval-group");
          const card = document.querySelector(".approval-card");
          const answers = Array.from(document.querySelectorAll(".approval-card-answers b")).map(node => node.textContent.trim());
          const labels = Array.from(document.querySelectorAll(".approval-card-answers small")).map(node => node.textContent.trim());
          const inspect = document.querySelector("[data-approval-inspect]");
          return {
            hasGroup: Boolean(group),
            hasCard: Boolean(card),
            labels,
            answers,
            inspectLabel: inspect ? inspect.textContent.trim() : "",
            activeCard: Boolean(document.querySelector(".approval-card.active"))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if not metrics.get("hasGroup") or not metrics.get("hasCard") or not metrics.get("activeCard"):
        raise AssertionError(f"approval decision surface should group and select the pending request, got {metrics}")
    joined_labels = " ".join(metrics.get("labels") or [])
    joined_answers = " ".join(metrics.get("answers") or [])
    if "Will run" not in joined_labels or "May touch" not in joined_labels or "Why approval" not in joined_labels:
        raise AssertionError(f"approval card should answer the three user questions, got {metrics}")
    if "write_file" not in joined_answers or ("Workspace files" not in joined_answers and "Files or state may be changed" not in joined_answers):
        raise AssertionError(f"approval card should explain the tool and workspace impact, got {metrics}")
    if "Inspect" not in str(metrics.get("inspectLabel", "")):
        raise AssertionError(f"approval detail should expose an inspect action, got {metrics}")

    zh = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#approvals",
        """
        (() => ({
          group: Boolean(document.querySelector(".approval-group")),
          labels: Array.from(document.querySelectorAll(".approval-card-answers small")).map(node => node.textContent.trim()),
          inspect: document.querySelector("[data-approval-inspect]")?.textContent.trim() || "",
          body: document.body.textContent
        }))()
        """,
        timeout,
        profile_dir,
    )
    zh_labels = " ".join(zh.get("labels") or [])
    if not zh.get("group") or "将执行" not in zh_labels or "可能影响" not in zh_labels or "审批原因" not in zh_labels:
        raise AssertionError(f"Chinese approval card should use localized ordinary-user labels, got {zh}")
    if "查看" not in str(zh.get("inspect", "")):
        raise AssertionError(f"Chinese approval detail should localize inspect action, got {zh}")


def assert_memory_artifact_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path, artifact: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#memory",
        f"""
        (async () => {{
          const deadline = Date.now() + 5000;
          while (Date.now() < deadline && !document.querySelector(".memory-artifact-index-item")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const view = document.querySelector(".memory-view");
          const form = document.querySelector(".memory-artifact-form");
          const input = document.querySelector("#artifactRef");
          const button = document.querySelector(".memory-artifact-load-button");
          const indexItem = document.querySelector(".memory-artifact-index-item");
          if (!view || !form || !input || !button || !indexItem) return {{ missing: true }};
          const initialButtonStyle = getComputedStyle(button);
          const initialButtonMetrics = {{
            whiteSpace: initialButtonStyle.whiteSpace,
            scrollWidth: Math.round(button.scrollWidth),
            clientWidth: Math.round(button.clientWidth)
          }};
          input.value = {json.dumps(artifact["ref"])};
          form.dispatchEvent(new Event("submit", {{ bubbles: true, cancelable: true }}));
          const detailDeadline = Date.now() + 5000;
          while (Date.now() < detailDeadline && !document.querySelector(".memory-artifact .markdown-body")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const artifactBody = document.querySelector(".memory-artifact .markdown-body");
          const currentView = document.querySelector(".memory-view");
          const currentButton = document.querySelector(".memory-artifact-load-button");
          const buttonStyle = currentButton ? getComputedStyle(currentButton) : initialButtonStyle;
          const viewRect = currentView ? currentView.getBoundingClientRect() : view.getBoundingClientRect();
          const bodyRect = document.body.getBoundingClientRect();
          return {{
            missing: false,
            hasRecentItem: Boolean(indexItem),
            hasArtifactBody: Boolean(artifactBody),
            bodyText: artifactBody ? artifactBody.textContent : "",
            buttonWhiteSpace: buttonStyle.whiteSpace || initialButtonMetrics.whiteSpace,
            buttonScrollWidth: Math.round((currentButton ? currentButton.scrollWidth : 0) || initialButtonMetrics.scrollWidth),
            buttonClientWidth: Math.round((currentButton ? currentButton.clientWidth : 0) || initialButtonMetrics.clientWidth),
            viewScrollWidth: Math.round((currentView ? currentView.scrollWidth : 0) || view.scrollWidth),
            viewClientWidth: Math.round((currentView ? currentView.clientWidth : 0) || view.clientWidth),
            bodyWidth: Math.round(bodyRect.width),
            viewWidth: Math.round(viewRect.width)
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("memory artifact layout missing expected nodes")
    if not metrics.get("hasRecentItem"):
        raise AssertionError(f"memory artifact index did not render a recent artifact, got {metrics}")
    if not metrics.get("hasArtifactBody") or "Full artifact body for memory viewer" not in str(metrics.get("bodyText", "")):
        raise AssertionError(f"memory artifact viewer did not lazy-load full content, got {metrics}")
    if int(metrics.get("buttonScrollWidth") or 0) > int(metrics.get("buttonClientWidth") or 0) + 2:
        raise AssertionError(f"artifact load button text should not overflow, got {metrics}")
    if int(metrics.get("viewScrollWidth") or 0) > int(metrics.get("viewClientWidth") or 0) + 2:
        raise AssertionError(f"memory view should not create horizontal overflow, got {metrics}")


def assert_catalog_brief_creation_path(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#catalog",
        """
        (async () => {
          const openDeadline = Date.now() + 5000;
          while (Date.now() < openDeadline && !document.querySelector('[data-resource-kind="skill"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const skillTab = document.querySelector('[data-resource-kind="skill"]');
          if (!skillTab) return { missingOpen: true };
          skillTab.click();
          const open = document.querySelector('[data-new-resource="skill"]');
          if (!open) return { missingOpen: true };
          open.click();
          const deadline = Date.now() + 5000;
          while (Date.now() < deadline && document.querySelector("#resourceDesigner")?.classList.contains("hidden")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const designer = document.querySelector("#resourceDesigner");
          const type = document.querySelector("#resourceType");
          const name = document.querySelector("#resourceName");
          const description = document.querySelector("#resourceDescription");
          const instructions = document.querySelector("#skillInstructions");
          const kinds = [...document.querySelectorAll("[data-resource-kind]")].map(node => node.dataset.resourceKind);
          if (!designer || !type || !name || !description || !instructions) return { missingForm: true };
          const designerOpen = !designer.classList.contains("hidden");
          const hasInternalSkillFields = Boolean(document.querySelector("#skillPreferredAgent, #skillKeywords, #agentToolPolicy, #agentAllowedKinds, #agentAllowedTools"));
          document.querySelector("[data-close-designer]")?.click();
          document.querySelector('[data-resource-kind="workflow"]')?.click();
          const workflowCreate = document.querySelector("[data-new-resource]");
          return {
            missingOpen: false,
            missingForm: false,
            designerOpen,
            typeValue: type.value,
            kinds,
            commonFieldCount: [name, description, instructions].filter(Boolean).length,
            hasInternalSkillFields,
            workflowCreateHidden: Boolean(workflowCreate?.classList.contains("hidden") || workflowCreate?.classList.contains("is-invisible")),
            hasLegacyCreateEntry: Boolean(document.querySelector('[data-new-resource="tool"], [data-new-resource="kit"], [data-new-resource="team-template"]'))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingOpen") or metrics.get("missingForm"):
        raise AssertionError(f"simple resource creation path missing expected controls, got {metrics}")
    if not metrics.get("designerOpen") or metrics.get("typeValue") != "skill":
        raise AssertionError(f"simple resource creation should open the Skill editor, got {metrics}")
    if metrics.get("kinds") != ["all", "agent", "subagent", "workflow", "skill", "tool", "mcp"]:
        raise AssertionError(f"resource navigation should expose one unified view and exactly six resource kinds, got {metrics}")
    if int(metrics.get("commonFieldCount") or 0) != 3 or metrics.get("hasInternalSkillFields"):
        raise AssertionError(f"Skill editor should expose only name, purpose, and instructions, got {metrics}")
    if not metrics.get("workflowCreateHidden"):
        raise AssertionError(f"Resources should route workflow creation to the workflow designer instead of a duplicate form, got {metrics}")
    if metrics.get("hasLegacyCreateEntry"):
        raise AssertionError(f"legacy Tool, Kit, and Team create entries should be retired from Resources, got {metrics}")

    mcp_metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#catalog",
        """
        (async () => {
          const deadline = Date.now() + 5000;
          while (Date.now() < deadline && !document.querySelector('[data-resource-kind="mcp"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          document.querySelector('[data-resource-kind="mcp"]')?.click();
          const openDeadline = Date.now() + 5000;
          while (Date.now() < openDeadline && !document.querySelector('[data-new-resource="mcp"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          document.querySelector('[data-new-resource="mcp"]')?.click();
          const formDeadline = Date.now() + 5000;
          while (Date.now() < formDeadline && !document.querySelector("[data-mcp-submit]")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const name = document.querySelector("#resourceName");
          const command = document.querySelector("#mcpCommand");
          if (name) name.value = "browser-smoke-mcp";
          if (command) command.value = "go";
            const optional = document.querySelector("#mcpOptional");
            const advanced = optional?.closest("details");
          return {
            hasCommand: Boolean(command),
            hasEnabled: Boolean(document.querySelector("#mcpEnabled")),
            hasOptional: Boolean(optional),
            optionalChecked: Boolean(optional?.checked),
            advancedCollapsed: Boolean(advanced && !advanced.open),
            hasCheckAndSave: Boolean(document.querySelector("[data-mcp-submit]")),
            hasWizardSteps: Boolean(document.querySelector("[data-mcp-step], [data-mcp-next], [data-mcp-back]"))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if not all(mcp_metrics.get(key) for key in ["hasCommand", "hasEnabled", "hasCheckAndSave"]):
        raise AssertionError(f"Connection creation should expose the command, enabled state, and save action, got {mcp_metrics}")
    if mcp_metrics.get("hasOptional") or mcp_metrics.get("optionalChecked") or mcp_metrics.get("hasWizardSteps"):
        raise AssertionError(f"Connection creation should hide low-value fallback fields and stay on one concise page, got {mcp_metrics}")

    handoff_metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#catalog",
        """
        (async () => {
          const deadline = Date.now() + 7000;
          while (Date.now() < deadline && !document.querySelector('[data-resource-card="agent:auditor"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const card = document.querySelector('[data-resource-card="agent:auditor"]');
          const capability = document.querySelector("#resourceCapabilityFilter");
          const health = document.querySelector("#resourceHealthFilter");
          const validation = document.querySelector("#resourceValidationFilter");
          if (!card || !capability || !health || !validation) return { missingCatalog: true };
          capability.value = "audit";
          capability.dispatchEvent(new Event("change", { bubbles: true }));
          await new Promise(resolve => setTimeout(resolve, 120));
          const filteredCards = [...document.querySelectorAll("[data-resource-card]")].map(node => node.dataset.resourceCard);
          capability.value = "";
          capability.dispatchEvent(new Event("change", { bubbles: true }));
          const versionTitle = card.querySelector(".resource-row-facts code")?.title || "";
          const hasTechnicalVersion = /sha256:|version|版本|哈希/i.test(card.textContent || "") || Boolean(versionTitle);
          document.querySelector('[data-resource-card="agent:auditor"] [data-use-resource]')?.click();
          const taskDeadline = Date.now() + 7000;
          while (Date.now() < taskDeadline && (location.hash !== "#playground" || !document.querySelector("#agentSelect"))) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          return {
            missingCatalog: false,
            filteredCards,
            versionTitle,
            hasTechnicalVersion,
            hash: location.hash,
            selectedAgent: document.querySelector("#agentSelect")?.value || "",
            selectedMode: document.querySelector("#runMode")?.value || "",
            resourceDraftCleared: !localStorage.getItem("goflow.playground.resourceDraft"),
            horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 2
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if handoff_metrics.get("missingCatalog"):
        raise AssertionError(f"resource catalog filters or rows are missing, got {handoff_metrics}")
    if handoff_metrics.get("filteredCards") != ["agent:auditor"]:
        raise AssertionError(f"resource capability filter should isolate the audit agent, got {handoff_metrics}")
    if handoff_metrics.get("hasTechnicalVersion"):
        raise AssertionError(f"resource row should hide internal content versions from ordinary users, got {handoff_metrics}")
    if handoff_metrics.get("hash") != "#playground" or handoff_metrics.get("selectedAgent") != "auditor" or handoff_metrics.get("selectedMode") != "agent":
        raise AssertionError(f"Use in task should select the exact resource in Tasks, got {handoff_metrics}")
    if not handoff_metrics.get("resourceDraftCleared") or handoff_metrics.get("horizontalOverflow"):
        raise AssertionError(f"resource handoff should be consumed without page overflow, got {handoff_metrics}")


def assert_catalog_tab_layout_stability(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    """Ensure tab content cannot move the centered resource layout."""
    for theme_name in ("light", "dark"):
        metrics = evaluate_browser_json(
            browser,
            f"{base_url}/console?lang=zh#catalog",
            f"""
            (async () => {{
              if (document.documentElement.dataset.theme !== {json.dumps(theme_name)}) {{
                window.dispatchEvent(new CustomEvent("goflow:set-theme", {{ detail: {{ theme: {json.dumps(theme_name)} }} }}));
                await new Promise(resolve => setTimeout(resolve, 180));
              }}
              const measure = () => {{
                const selectors = [".shell", ".resource-library", ".resource-library-layout", ".resource-kind-nav", ".resource-browser", ".resource-list"];
                const values = {{}};
                for (const selector of selectors) {{
                  const element = document.querySelector(selector);
                  const rect = element?.getBoundingClientRect();
                  values[selector] = rect ? {{ x: rect.x, width: rect.width }} : null;
                }}
                values.overflowX = document.documentElement.scrollWidth > innerWidth + 2;
                return values;
              }};
              const measurements = {{}};
              const preservedCard = document.querySelector('[data-resource-card^="agent:"]');
              const preservedKey = preservedCard?.dataset.resourceCard || "";
              let preservedResourceNode = Boolean(preservedCard);
              for (const kind of ["all", "agent", "subagent", "workflow", "skill", "tool", "mcp", "all"]) {{
                document.querySelector(`[data-resource-kind="${{kind}}"]`)?.click();
                await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
                measurements[kind] = measure();
                if ((kind === "agent" || kind === "all") && preservedKey) {{
                  preservedResourceNode = preservedResourceNode && document.querySelector(`[data-resource-card="${{CSS.escape(preservedKey)}}"]`) === preservedCard;
                }}
              }}
              const values = Object.values(measurements).filter(Boolean);
              const positions = values.map(value => value[".resource-library"]?.x).filter(value => typeof value === "number");
              const shellWidths = values.map(value => value[".shell"]?.width).filter(value => typeof value === "number");
              const spread = values => values.length ? Math.max(...values) - Math.min(...values) : 999;
              return {{
                theme: document.documentElement.dataset.theme,
                libraryXDelta: spread(positions),
                shellWidthDelta: spread(shellWidths),
                overflowX: values.some(value => value.overflowX),
                preservedResourceNode
              }};
            }})()
            """,
            timeout,
            profile_dir,
            viewport=(1440, 1000),
        )
        if (
            metrics.get("theme") != theme_name
            or float(metrics.get("libraryXDelta", 999)) > 1
            or float(metrics.get("shellWidthDelta", 999)) > 1
            or metrics.get("overflowX")
            or not metrics.get("preservedResourceNode")
        ):
            raise AssertionError(f"resource catalog tabs should preserve layout position in {theme_name}, got {metrics}")


def assert_catalog_tool_workflow_handoff(browser: Path, base_url: str, timeout: float, profile_dir: Path, output_dir: Path | None = None) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#catalog",
        """
        (async () => {
          const waitFor = async predicate => {
            const deadline = Date.now() + 10000;
            while (Date.now() < deadline) {
              if (predicate()) return true;
              await new Promise(resolve => setTimeout(resolve, 80));
            }
            return false;
          };
          await waitFor(() => document.querySelector('[data-resource-kind="tool"]'));
          const apiCatalog = await fetch('/api/resources?kind=tool').then(response => response.json()).catch(error => ({ error: String(error) }));
          const workflowOptions = await fetch('/api/workflow-options').then(response => response.json()).catch(error => ({ error: String(error) }));
          document.querySelector('[data-resource-kind="tool"]')?.click();
          await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
          const card = document.querySelector('[data-resource-card^="tool:"]');
          const toolName = card?.dataset.resourceCard?.slice('tool:'.length) || '';
          card?.querySelector('[data-use-resource][data-kind="tool"]')?.click();
          const opened = await waitFor(() => location.hash === '#workflows' && document.querySelector('.flow-node.tool.selected'));
          const schema = document.querySelector('#stageToolSchema');
          const agentField = document.querySelector('#stageAgent')?.closest('[data-stage-field="agent"]');
          return {
            opened,
            toolName,
            selectedTool: document.querySelector('#stageTool')?.value || '',
            toolMode: document.querySelector('#stageToolMode')?.value || '',
            schemaVisible: Boolean(schema && !schema.classList.contains('hidden')),
            schemaFields: schema?.querySelectorAll('.workflow-tool-schema-fields code').length || 0,
            connectedHidesLegacyAgent: Boolean(agentField?.classList.contains('hidden')),
            draftCleared: !localStorage.getItem('goflow.workflow.toolDraft'),
            apiToolCount: Array.isArray(apiCatalog?.items) ? apiCatalog.items.length : 0,
            optionToolCount: Array.isArray(workflowOptions?.tool_catalog) ? workflowOptions.tool_catalog.length : 0,
            apiError: apiCatalog?.error || workflowOptions?.error || '',
            overflowX: document.documentElement.scrollWidth > innerWidth + 2
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
        screenshot_path=output_dir / "workflow-connected-tool-desktop.png" if output_dir else None,
    )
    if (
        not metrics.get("opened")
        or not metrics.get("toolName")
        or metrics.get("selectedTool") != metrics.get("toolName")
        or metrics.get("toolMode") != "connected"
        or not metrics.get("schemaVisible")
        or int(metrics.get("schemaFields", 0)) < 1
        or not metrics.get("connectedHidesLegacyAgent")
        or not metrics.get("draftCleared")
        or metrics.get("overflowX")
    ):
        raise AssertionError(f"MCP tool resource should insert a configured connected workflow node, got {metrics}")


def assert_route_level_view_loading(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#catalog",
        """
        (async () => {
          const deadline = Date.now() + 7000;
          while (Date.now() < deadline && !document.querySelector(".resource-library")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const beforeEntries = performance.getEntriesByType("resource");
          const before = beforeEntries.map(entry => new URL(entry.name).pathname);
          const initialJSBytes = beforeEntries
            .filter(entry => new URL(entry.name).pathname.endsWith(".js"))
            .reduce((total, entry) => total + Number(entry.decodedBodySize || entry.transferSize || 0), 0);
          const workflowStartedAt = performance.now();
          document.querySelector('[data-view="workflows"]')?.click();
          const workflowDeadline = Date.now() + 9000;
          while (Date.now() < workflowDeadline && !document.querySelector("#canvas")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const after = performance.getEntriesByType("resource").map(entry => new URL(entry.name).pathname);
          return {
            catalogReady: Boolean(document.querySelector(".resource-library")) || before.includes("/assets/views/catalog.js"),
            catalogLoaded: before.includes("/assets/views/catalog.js"),
            chatLoadedBefore: before.includes("/assets/views/chat.js"),
            workflowsLoadedBefore: before.includes("/assets/views/workflows.js"),
            settingsLoadedBefore: before.includes("/assets/views/settings.js"),
            workflowsLoadedAfter: after.includes("/assets/views/workflows.js"),
            canvasReady: Boolean(document.querySelector("#canvas")),
            initialJSBytes,
            workflowLoadMs: Math.round(performance.now() - workflowStartedAt)
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if not metrics.get("catalogReady") or not metrics.get("catalogLoaded"):
        raise AssertionError(f"catalog route did not load its own view module, got {metrics}")
    if metrics.get("chatLoadedBefore") or metrics.get("workflowsLoadedBefore") or metrics.get("settingsLoadedBefore"):
        raise AssertionError(f"catalog route should not eagerly load unrelated large view modules, got {metrics}")
    if not metrics.get("workflowsLoadedAfter") or not metrics.get("canvasReady"):
        raise AssertionError(f"workflow module should load on navigation and render the canvas, got {metrics}")
    if int(metrics.get("initialJSBytes") or 0) > 1_100_000:
        raise AssertionError(f"catalog initial JavaScript exceeded the 1.1 MB decoded budget, got {metrics}")
    if int(metrics.get("workflowLoadMs") or 0) > 5_000:
        raise AssertionError(f"warm local workflow navigation exceeded the 5 second budget, got {metrics}")


def assert_primary_page_accessibility(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const views = ["playground", "workflows", "catalog", "usage", "settings"];
          const results = [];
          const visible = node => {
            const style = getComputedStyle(node);
            return style.display !== "none" && style.visibility !== "hidden" && !node.closest(".hidden") && node.getClientRects().length > 0;
          };
          const accessibleName = node => {
            const direct = node.getAttribute("aria-label") || node.getAttribute("title") || "";
            if (direct.trim()) return direct.trim();
            if (["INPUT", "SELECT", "TEXTAREA"].includes(node.tagName)) {
              const labels = [...(node.labels || [])].map(label => label.textContent || "").join(" ").trim();
              if (labels) return labels;
            }
            return (node.textContent || "").replace(/\\s+/g, " ").trim();
          };
          for (const view of views) {
            location.hash = view;
            const deadline = Date.now() + 9000;
            while (Date.now() < deadline && (document.querySelector("#app")?.dataset.view !== view || document.querySelector("#app .loading-panel"))) {
              await new Promise(resolve => setTimeout(resolve, 120));
            }
            await new Promise(resolve => setTimeout(resolve, 120));
            const interactive = [...document.querySelectorAll("#mainContent button, #mainContent a[href], #mainContent input, #mainContent select, #mainContent textarea")].filter(visible);
            const unnamed = interactive.filter(node => !accessibleName(node)).map(node => `${node.tagName.toLowerCase()}#${node.id || ""}.${node.className || ""}`);
            const missingAlt = [...document.querySelectorAll("#mainContent img")].filter(node => visible(node) && !node.hasAttribute("alt")).map(node => node.getAttribute("src") || "img");
            const ids = [...document.querySelectorAll("#mainContent [id]")].map(node => node.id).filter(Boolean);
            const duplicateIds = [...new Set(ids.filter((id, index) => ids.indexOf(id) !== index))];
            results.push({
              view,
              unnamed: unnamed.slice(0, 12),
              missingAlt: missingAlt.slice(0, 12),
              duplicateIds: duplicateIds.slice(0, 12),
              hasHeading: Boolean(document.querySelector("#mainContent h1")),
              horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 2
            });
          }
          return { results };
        })()
        """,
        timeout,
        profile_dir,
    )
    failures = [
        item
        for item in metrics.get("results", [])
        if item.get("unnamed") or item.get("missingAlt") or item.get("duplicateIds") or not item.get("hasHeading") or item.get("horizontalOverflow")
    ]
    if len(metrics.get("results", [])) != 5 or failures:
        raise AssertionError(f"primary page accessibility/layout audit failed: {failures or metrics}")


def assert_workflow_metadata_zh(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        zh_url(base_url, "/workflows"),
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector('.flow-node[data-stage-name="plan"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const node = document.querySelector('.flow-node[data-stage-name="plan"]');
          if (!node) return { missingNode: true };
          node.click();
          const metaDeadline = Date.now() + 4000;
          let panel = document.querySelector("#stageNodeTypeMeta");
          while (
            Date.now() < metaDeadline &&
            (!panel || panel.classList.contains("hidden") || !panel.textContent.trim())
          ) {
            await new Promise(resolve => setTimeout(resolve, 120));
            panel = document.querySelector("#stageNodeTypeMeta");
          }
          const text = panel ? panel.textContent.replace(/\\s+/g, " ").trim() : "";
          const nodeTitle = node.querySelector(".node-title")?.textContent.trim() || "";
          const nameField = document.querySelector('[data-stage-field="name"]');
          const nameHelp = nameField ? nameField.textContent.replace(/\\s+/g, " ").trim() : "";
          const taskEditor = document.querySelector("#stageTaskEditor");
          const taskText = taskEditor ? taskEditor.textContent.replace(/\\s+/g, " ").trim() : "";
          return {
            missingNode: false,
            hasMeta: Boolean(panel && !panel.classList.contains("hidden") && text),
            text,
            hasDescription: text.includes("负责此阶段的智能体配置。"),
            hasExample: text.includes("先生成一份可供后续阶段消费的计划。") && text.includes("下游节点引用 stages.plan.outputs.plan"),
            hasPlanExample: text.includes("计划阶段"),
            hasLocalizedStageTitle: nodeTitle === "制定计划",
            hasCompatibilityIdHelp: nameHelp.includes("阶段兼容 ID") && nameHelp.includes("供工作流引用使用"),
            hasSimpleLocalizedName: taskText.includes("显示名称") && taskText.includes("制定计划") && !taskText.includes("阶段名称 start"),
            englishLeaks: [
              "Extra node parameters passed to the runtime and shown in Studio.",
              "purpose: Explain what this stage must produce",
              "Create a plan that later stages can consume.",
            ].filter(item => text.includes(item)),
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingNode"):
        raise AssertionError("workflow zh smoke could not find the preset plan node")
    if not metrics.get("hasMeta"):
        raise AssertionError(f"workflow zh smoke did not render node metadata, got {metrics}")
    if not metrics.get("hasDescription") or not metrics.get("hasExample") or not metrics.get("hasPlanExample"):
        raise AssertionError(f"workflow zh smoke missing localized node metadata, got {metrics}")
    if not metrics.get("hasLocalizedStageTitle") or not metrics.get("hasCompatibilityIdHelp") or not metrics.get("hasSimpleLocalizedName"):
        raise AssertionError(f"workflow zh smoke missing localized stage title or compatibility ID guidance, got {metrics}")
    if metrics.get("englishLeaks"):
        raise AssertionError(f"workflow zh smoke found untranslated node metadata, got {metrics}")


def assert_simple_workflow_build_path(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/workflows",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector('[data-template="condition"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const search = document.querySelector("#nodePaletteSearch");
          if (!search) return { missingPalette: true };
          search.value = "condition";
          search.dispatchEvent(new Event("input", { bubbles: true }));
          await new Promise(resolve => setTimeout(resolve, 250));
          const conditionButton = document.querySelector('[data-template="condition"]');
          if (!conditionButton) return { missingPalette: true };
          const beforeConditionCount = document.querySelectorAll('.flow-node.condition').length;
          conditionButton.click();
          const nodeDeadline = Date.now() + 5000;
          while (Date.now() < nodeDeadline && document.querySelectorAll('.flow-node.condition').length <= beforeConditionCount) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const conditionNodes = Array.from(document.querySelectorAll('.flow-node.condition'));
          const node = conditionNodes[conditionNodes.length - 1];
          const inspector = document.querySelector("#stageForm");
          const taskEditor = document.querySelector("#stageTaskEditor");
          const routePreview = document.querySelector("#stageRoutePreview");
          const rawFields = document.querySelector(".workflow-raw-fields");
          const validate = document.querySelector("#validateGraph");
          const statusBeforeDismiss = document.querySelector("#workflowBoardStatus");
          if (!node || !inspector || !taskEditor || !validate) return { missingNode: true, beforeConditionCount, conditionCount: conditionNodes.length, canvasText: document.querySelector("#canvas")?.textContent || "" };
          validate.click();
          const validationDeadline = Date.now() + 5000;
          let panel = document.querySelector("#workflowValidationPanel");
          while (Date.now() < validationDeadline && (!panel || panel.classList.contains("hidden") || panel.classList.contains("loading"))) {
            await new Promise(resolve => setTimeout(resolve, 120));
            panel = document.querySelector("#workflowValidationPanel");
          }
          const validationText = panel ? panel.textContent : "";
          const dismiss = panel?.querySelector("[data-workflow-validation-dismiss]");
          const beforeDismissHidden = panel?.classList.contains("hidden") || false;
          if (dismiss) {
            dismiss.click();
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          return {
            missingPalette: false,
            missingNode: false,
            nodeText: node.textContent,
            selected: node.classList.contains("selected"),
            taskEditorText: taskEditor.textContent,
            hasRoutePreview: Boolean(routePreview && !routePreview.classList.contains("hidden")),
            rawFieldsClosed: rawFields ? !rawFields.open : false,
            validationText,
            validationDismissed: Boolean(panel && panel.classList.contains("hidden")),
            boardStatusHiddenAfterDismiss: Boolean(statusBeforeDismiss && statusBeforeDismiss.classList.contains("hidden"))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingPalette") or metrics.get("missingNode"):
        raise AssertionError(f"simple workflow build path missing expected controls, got {metrics}")
    if "condition" not in str(metrics.get("nodeText", "")).lower() or not metrics.get("selected"):
        raise AssertionError(f"clicking a palette node should add and select a condition node, got {metrics}")
    if "condition" not in str(metrics.get("taskEditorText", "")).lower() and "route" not in str(metrics.get("taskEditorText", "")).lower():
        raise AssertionError(f"workflow inspector should show the simple task editor for the condition node, got {metrics}")
    if not metrics.get("rawFieldsClosed"):
        raise AssertionError(f"raw workflow fields should remain folded in the default view, got {metrics}")
    if not str(metrics.get("validationText", "")).strip():
        raise AssertionError(f"workflow validation should produce a visible, dismissible result, got {metrics}")
    if not metrics.get("validationDismissed"):
        raise AssertionError(f"workflow validation panel should be dismissible, got {metrics}")


def assert_workflow_focus_mode_keeps_canvas_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/workflows",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && (!document.querySelector("#focusCanvas") || !document.querySelector("#canvas"))) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const button = document.querySelector("#focusCanvas");
          const canvas = document.querySelector("#canvas");
          const studio = document.querySelector(".studio");
          if (!button || !canvas || !studio) return { missing: true };
          button.click();
          await new Promise(resolve => setTimeout(resolve, 300));
          const rect = canvas.getBoundingClientRect();
          const studioRect = studio.getBoundingClientRect();
          const boardRect = document.querySelector(".workflow-board")?.getBoundingClientRect();
          const topbar = document.querySelector(".topbar");
          const toolbar = document.querySelector(".board-toolbar");
          const toolbarRect = toolbar?.getBoundingClientRect();
          const toolbarContentBottom = toolbar
            ? Math.max(...Array.from(toolbar.children).map(node => node.getBoundingClientRect().bottom))
            : -1;
          const left = document.querySelector(".studio-left");
          const right = document.querySelector(".studio-right");
          const resizer = document.querySelector(".workflow-inspector-resizer");
          const result = {
            focused: studio.classList.contains("focus-mode"),
            canvasWidth: Math.round(rect.width),
            canvasHeight: Math.round(rect.height),
            studioWidth: Math.round(studioRect.width),
            boardWidth: Math.round(boardRect?.width || 0),
            viewportWidth: window.innerWidth,
            topbarVisible: topbar ? getComputedStyle(topbar).display !== "none" : false,
            toolbarVisible: toolbar ? getComputedStyle(toolbar).display !== "none" : false,
            toolbarTop: Math.round(toolbarRect?.top || -1),
            toolbarContentFits: toolbarRect ? toolbarContentBottom <= toolbarRect.bottom + 1 : false,
            leftVisible: left ? getComputedStyle(left).display !== "none" : false,
            rightVisible: right ? getComputedStyle(right).display !== "none" : false,
            resizerVisible: resizer ? getComputedStyle(resizer).display !== "none" : false
          };
          button.click();
          return result;
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"workflow focus mode is missing its canvas controls, got {metrics}")
    if not metrics.get("focused"):
        raise AssertionError(f"workflow focus mode did not activate, got {metrics}")
    if metrics.get("leftVisible") or metrics.get("rightVisible") or metrics.get("resizerVisible"):
        raise AssertionError(f"workflow focus mode should reserve the viewport for the canvas, got {metrics}")
    if (not metrics.get("topbarVisible") or not metrics.get("toolbarVisible") or
            metrics.get("toolbarTop", -1) < 0 or not metrics.get("toolbarContentFits")):
        raise AssertionError(f"workflow focus mode must keep navigation and canvas tools visible, got {metrics}")
    if metrics.get("canvasWidth", 0) < metrics.get("viewportWidth", 0) * 0.7 or metrics.get("canvasHeight", 0) < 320:
        raise AssertionError(f"workflow focus mode must keep a large visible canvas, got {metrics}")


def assert_workflow_drawers_open_on_demand(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/workflows",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && (!document.querySelector('#workflowLibraryToggle') || !document.querySelector('.flow-node'))) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const studio = document.querySelector('.studio');
          const library = document.querySelector('.studio-left');
          const inspector = document.querySelector('.studio-right');
          const backdrop = document.querySelector('#workflowDrawerBackdrop');
          const libraryToggle = document.querySelector('#workflowLibraryToggle');
          const inspectorClose = document.querySelector('#workflowInspectorClose');
          const node = document.querySelector('.flow-node');
          if (!studio || !library || !inspector || !backdrop || !libraryToggle || !inspectorClose || !node) return { missing: true };
          backdrop.click();
          await new Promise(resolve => setTimeout(resolve, 180));
          const initial = {
            libraryClosed: studio.classList.contains('left-rail-collapsed'),
            libraryHidden: library.getAttribute('aria-hidden') === 'true',
            libraryInert: library.hasAttribute('inert'),
            inspectorClosed: !studio.classList.contains('inspector-open'),
            inspectorHidden: inspector.getAttribute('aria-hidden') === 'true',
            inspectorInert: inspector.hasAttribute('inert')
          };
          libraryToggle.click();
          await new Promise(resolve => setTimeout(resolve, 220));
          const libraryOpen = {
            open: !studio.classList.contains('left-rail-collapsed'),
            hidden: library.getAttribute('aria-hidden') === 'true',
            inert: library.hasAttribute('inert'),
            backdropVisible: getComputedStyle(backdrop).display !== 'none',
            backdropPointerEvents: getComputedStyle(backdrop).pointerEvents
          };
          const paletteNode = library.querySelector('[data-template="agent"]');
          const canvas = document.querySelector('#canvas');
          const nodeCountBeforeDrop = document.querySelectorAll('.flow-node').length;
          let dragDrop = { supported: false };
          if (paletteNode && canvas && typeof DataTransfer === 'function' && typeof DragEvent === 'function') {
            const transfer = new DataTransfer();
            const canvasRect = canvas.getBoundingClientRect();
            paletteNode.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: transfer }));
            canvas.dispatchEvent(new DragEvent('dragenter', { bubbles: true, cancelable: true, dataTransfer: transfer, clientX: canvasRect.right - 120, clientY: canvasRect.top + 220 }));
            canvas.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer: transfer, clientX: canvasRect.right - 120, clientY: canvasRect.top + 220 }));
            const dropReady = canvas.classList.contains('drop-ready');
            canvas.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: transfer, clientX: canvasRect.right - 120, clientY: canvasRect.top + 220 }));
            await new Promise(resolve => setTimeout(resolve, 220));
            const nodeCountAfterDrop = document.querySelectorAll('.flow-node').length;
            dragDrop = {
              supported: true,
              transferType: transfer.getData('application/x-goflow-node'),
              dropReady,
              added: nodeCountAfterDrop === nodeCountBeforeDrop + 1,
              cleared: !document.querySelector('#canvas')?.classList.contains('drop-ready')
            };
            document.querySelector('.flow-node.selected .node-delete')?.click();
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          backdrop.click();
          await new Promise(resolve => setTimeout(resolve, 180));
          const inspectorNode = document.querySelector('.flow-node');
          inspectorNode?.click();
          await new Promise(resolve => setTimeout(resolve, 220));
          const inspectorOpen = {
            open: studio.classList.contains('inspector-open'),
            hidden: inspector.getAttribute('aria-hidden') === 'true',
            inert: inspector.hasAttribute('inert'),
            selected: Boolean(document.querySelector('.flow-node.selected')),
            backdropVisible: getComputedStyle(backdrop).display !== 'none',
            backdropPointerEvents: getComputedStyle(backdrop).pointerEvents
          };
          inspectorClose.click();
          await new Promise(resolve => setTimeout(resolve, 180));
          return {
            initial,
            libraryOpen,
            dragDrop,
            inspectorOpen,
            closed: !studio.classList.contains('inspector-open') && inspector.hasAttribute('inert'),
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"workflow drawer controls are missing, got {metrics}")
    if metrics.get("initial") != {
        "libraryClosed": True,
        "libraryHidden": True,
        "libraryInert": True,
        "inspectorClosed": True,
        "inspectorHidden": True,
        "inspectorInert": True,
    }:
        raise AssertionError(f"workflow canvas should start without persistent side panels, got {metrics}")
    if metrics.get("libraryOpen") != {"open": True, "hidden": False, "inert": False, "backdropVisible": False, "backdropPointerEvents": "none"}:
        raise AssertionError(f"workflow library drawer did not open cleanly, got {metrics}")
    drag_drop = metrics.get("dragDrop") or {}
    if drag_drop.get("supported") and (
        drag_drop.get("transferType") != "agent"
        or not drag_drop.get("dropReady")
        or not drag_drop.get("added")
        or not drag_drop.get("cleared")
    ):
        raise AssertionError(f"workflow palette node should drag and drop onto the open canvas, got {metrics}")
    if metrics.get("inspectorOpen") != {
        "open": True,
        "hidden": False,
        "inert": False,
        "selected": True,
        "backdropVisible": False,
        "backdropPointerEvents": "none",
    }:
        raise AssertionError(f"selecting a workflow node should open its inspector drawer, got {metrics}")
    if not metrics.get("closed") or metrics.get("overflowX"):
        raise AssertionError(f"workflow drawers should close without horizontal overflow, got {metrics}")


def assert_settings_warning_resolution_path(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#settings",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector("[data-settings-simple]")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const simple = document.querySelector("[data-settings-simple]");
          const providerForm = document.querySelector("[data-provider-setup-form]");
          const workspaceCard = document.querySelector(".settings-workspace-readiness");
          const workspaceVersions = document.querySelector(".settings-workspace-versions");
          const confirm = document.querySelector("[data-settings-workspace-confirm]");
          const themeButtons = Array.from(document.querySelectorAll("[data-settings-theme]"));
          if (!simple || !providerForm || !workspaceCard || !workspaceVersions || themeButtons.length !== 2) return { missing: true };
          const confirmTextBefore = confirm ? confirm.textContent.trim() : "";
          if (confirm) {
            confirm.click();
            const confirmDeadline = Date.now() + 5000;
            while (Date.now() < confirmDeadline && confirm.getAttribute("aria-busy") === "true") {
              await new Promise(resolve => setTimeout(resolve, 120));
            }
          }
          const output = document.querySelector("[data-settings-workspace-output]");
          const initialTheme = document.documentElement.dataset.theme;
          const nextTheme = initialTheme === "dark" ? "light" : "dark";
          themeButtons.find(button => button.dataset.settingsTheme === nextTheme)?.click();
          await new Promise(resolve => setTimeout(resolve, 100));
          return {
            missing: false,
            sectionCount: simple.querySelectorAll(":scope > .settings-simple-section").length,
            providerFields: Boolean(
              providerForm.querySelector("[data-provider-setup-base-url]") &&
              providerForm.querySelector("[data-provider-setup-api-key]") &&
              providerForm.querySelector("[data-provider-setup-model]")
            ),
            hasDuplicateProviderFields: [
              "[data-provider-setup-base-url]",
              "[data-provider-setup-api-key]",
              "[data-provider-setup-model]"
            ].some(selector => providerForm.querySelectorAll(selector).length !== 1),
            confirmTextBefore,
            confirmBusy: confirm ? confirm.getAttribute("aria-busy") : "",
            workspaceOutput: output ? output.textContent.trim() : "",
            themeChanged: document.documentElement.dataset.theme === nextTheme,
            activeThemeCount: themeButtons.filter(button => button.getAttribute("aria-pressed") === "true").length,
            hasObsoleteControls: Boolean(
              document.querySelector("[data-settings-data-backup]") ||
              document.querySelector('[data-settings-workspace-entry="manage"]') ||
              document.querySelector('[data-settings-workspace-entry="open"]')
            ),
            hasWorkspaceVersions: workspaceVersions.textContent.includes("工作区版本") || workspaceVersions.textContent.includes("Workspace versions"),
            hasLegacyModules: Boolean(
              document.querySelector(".settings-hero") ||
              document.querySelector(".settings-health-panel") ||
              document.querySelector(".settings-update-panel") ||
              document.querySelector("[data-target-manager]") ||
              document.querySelector(".settings-advanced-runtime")
            )
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"settings ordinary setup path missing expected controls, got {metrics}")
    if metrics.get("sectionCount") != 3:
        raise AssertionError(f"settings should expose exactly three useful ordinary sections, got {metrics}")
    if not metrics.get("providerFields"):
        raise AssertionError(f"settings should provide the Provider API Key field, got {metrics}")
    if metrics.get("hasDuplicateProviderFields"):
        raise AssertionError(f"settings should render one Provider URL, API Key, and model field, got {metrics}")
    if metrics.get("confirmTextBefore") and metrics.get("confirmBusy") == "true":
        raise AssertionError(f"settings workspace confirmation should clear busy state, got {metrics}")
    if not metrics.get("themeChanged") or metrics.get("activeThemeCount") != 1:
        raise AssertionError(f"settings appearance control should apply one active theme, got {metrics}")
    if metrics.get("hasObsoleteControls"):
        raise AssertionError(f"settings should not expose obsolete backup or fake workspace controls, got {metrics}")
    if not metrics.get("hasWorkspaceVersions"):
        raise AssertionError(f"settings should expose workspace version history, got {metrics}")
    if metrics.get("hasLegacyModules"):
        raise AssertionError(f"settings should not expose legacy diagnostics, updates, or target management, got {metrics}")


def assert_settings_workspace_entry_flow(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        zh_url(base_url, "/console#settings"),
        """
        (async () => {
          await new Promise(resolve => setTimeout(resolve, 700));
          const form = document.querySelector('[data-settings-workspace-switch]');
          const input = form?.querySelector('[data-settings-workspace-path]');
          const button = form?.querySelector('button[type="submit"]');
          const picker = form?.querySelector('[data-settings-workspace-pick]');
          const original = input?.value || '';
          if (input && form) {
            input.value = ` ${original} `;
            form.requestSubmit();
            const deadline = Date.now() + 6000;
            while (Date.now() < deadline && !document.querySelector('.settings-workspace-readiness.good')) {
              await new Promise(resolve => setTimeout(resolve, 120));
            }
          }
          const workspace = await fetch('/api/workspace').then(response => response.json()).catch(() => ({}));
          const nextInput = document.querySelector('[data-settings-workspace-path]');
          return {
            hash: location.hash,
            page: Boolean(document.querySelector('[data-settings-anchor="workspace"]')),
            boundary: Boolean(document.querySelector('.settings-workspace-readiness')),
            switchForm: Boolean(form),
            editable: Boolean(input && !input.readOnly && !input.disabled),
            submitVisible: Boolean(button && button.getBoundingClientRect().width > 0),
            pickerVisible: Boolean(picker && picker.getBoundingClientRect().width > 0),
            pickerExpected: Boolean(workspace.capabilities?.can_open_host_folder_picker),
            original,
            current: nextInput?.value || '',
            confirmed: Boolean(workspace.confirmed),
            overflowX: document.documentElement.scrollWidth > innerWidth + 2
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
    )
    if (
        metrics.get("hash") != "#settings"
        or not metrics.get("page")
        or not metrics.get("boundary")
        or not metrics.get("switchForm")
        or not metrics.get("editable")
        or not metrics.get("submitVisible")
        or metrics.get("pickerVisible") != metrics.get("pickerExpected")
        or not metrics.get("confirmed")
        or metrics.get("overflowX")
    ):
        raise AssertionError(f"settings should expose a real editable workspace switch flow, got {metrics}")


def assert_execution_target_page_flow(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#settings",
        """
        (async () => {
          const waitFor = async (predicate, ms = 7000) => {
            const deadline = Date.now() + ms;
            while (Date.now() < deadline) {
              const value = predicate();
              if (value) return value;
              await new Promise(resolve => setTimeout(resolve, 100));
            }
            return null;
          };
          const manager = await waitFor(() => document.querySelector("[data-target-manager]"));
          if (!manager) return { missing: true };
          const initialRows = Array.from(manager.querySelectorAll("[data-target-row]"));
          const hasLocal = initialRows.some(row => row.dataset.targetRow === "local");
          const hasRemote = initialRows.some(row => row.dataset.targetRow === "smoke-ssh");
          const add = manager.querySelector("[data-target-add]");
          add?.click();
          const form = await waitFor(() => manager.querySelector("[data-target-form]:not(.hidden)"));
          if (!form) return { missing: true, hasLocal, hasRemote };
          form.querySelector("[data-target-name]").value = "页面验收主机";
          form.querySelector("[data-target-host]").value = "192.0.2.88";
          form.querySelector("[data-target-port]").value = "2222";
          form.querySelector("[data-target-user]").value = "operator";
          form.querySelector("[data-target-credential]").value = "secret://targets/page-smoke/private_key";
          form.querySelector("[data-target-commands]").value = "uptime\\ndf -h";
          const originalFetch = window.fetch;
          let failScan = true;
          let capturedPreflight = null;
          const preflightRequests = [];
          window.fetch = async (input, init = {}) => {
            const url = String(input);
            if (url.includes("/api/targets/scan-host-key")) {
              if (failScan) {
                return new Response(JSON.stringify({ error: "SSH host-key scan returned no usable keys" }), { status: 502, headers: { "Content-Type": "application/json" } });
              }
              return new Response(JSON.stringify({
                host: "192.0.2.88",
                port: 2222,
                count: 1,
                keys: [{ host: "[192.0.2.88]:2222", key_type: "ssh-ed25519", fingerprint: "SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" }]
              }), { status: 200, headers: { "Content-Type": "application/json" } });
            }
            if (url.includes("/api/runs/preflight")) {
              const request = JSON.parse(String(init.body || "{}"));
              preflightRequests.push(request);
              if (String(request.input || "").includes("检查远程主机状态")) capturedPreflight = request;
              return new Response(JSON.stringify({ valid: false, blocking_issues: [{ message: "browser smoke stop" }] }), { status: 200, headers: { "Content-Type": "application/json" } });
            }
            return originalFetch(input, init);
          };
          form.querySelector("[data-target-scan]").click();
          await waitFor(() => form.querySelector("[data-target-scan-status].error"));
          const failureVisible = form.querySelector("[data-target-scan-status]")?.textContent.includes("SSH host-key scan") || false;
          failScan = false;
          form.querySelector("[data-target-scan]").click();
          await waitFor(() => form.querySelector("input[name='target-host-key']"));
          const keyVisible = Boolean(form.querySelector("input[name='target-host-key']:checked"));
          form.requestSubmit();
          const createdRow = await waitFor(() => document.querySelector('[data-target-row="ssh-192-0-2-88-2222-operator"]'), 9000);
          const created = Boolean(createdRow);
          await originalFetch("/api/workspace/confirm", { method: "POST" }).catch(() => null);
          localStorage.removeItem("goflow.playground.runState");
          localStorage.removeItem("goflow.workflowStudio.activeRun");
          location.hash = "playground";
          const selector = await waitFor(() => document.querySelector("#executionTargetSelect"), 9000);
          if (!selector) {
            window.fetch = originalFetch;
            return { missing: true, hasLocal, hasRemote, failureVisible, keyVisible, created };
          }
          const disabledOption = Array.from(selector.options).find(option => option.value === "disabled-ssh");
          selector.value = "ssh-192-0-2-88-2222-operator";
          selector.dispatchEvent(new Event("change", { bubbles: true }));
          const prompt = document.querySelector("#prompt");
          prompt.value = "检查远程主机状态";
          prompt.dispatchEvent(new Event("input", { bubbles: true }));
          document.querySelector("#send")?.click();
          await waitFor(() => capturedPreflight, 6000);
          const fieldVisible = !document.querySelector("#executionTargetField")?.classList.contains("hidden");
          window.fetch = originalFetch;
          const result = {
            missing: false,
            hasLocal,
            hasRemote,
            failureVisible,
            keyVisible,
            created,
            fieldVisible,
            selected: selector.value,
            disabledOption: Boolean(disabledOption?.disabled),
            sendDisabled: Boolean(document.querySelector("#send")?.disabled),
            sendHandler: typeof document.querySelector("#send")?.onclick,
            runStatus: document.querySelector("#runStatus")?.textContent?.trim() || "",
            referenceFeedback: document.querySelector("#referenceFeedback")?.textContent?.trim() || "",
            preflightTarget: capturedPreflight?.target_id || "",
            preflightRequestCount: preflightRequests.length
          };
          localStorage.setItem("goflow.playground.executionTarget", "local");
          return result;
        })()
        """,
        timeout,
        profile_dir,
    )
    required = ["hasLocal", "hasRemote", "failureVisible", "keyVisible", "created", "fieldVisible", "disabledOption"]
    missing = [name for name in required if not metrics.get(name)]
    if metrics.get("missing") or missing:
        raise AssertionError(f"execution Target page flow missing {missing}, got {metrics}")
    expected = "ssh-192-0-2-88-2222-operator"
    if metrics.get("selected") != expected or metrics.get("preflightTarget") != expected:
        raise AssertionError(f"task execution Target selection was not passed to preflight, got {metrics}")


def assert_execution_target_selection_flow(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const waitFor = async (predicate, ms = 8000) => {
            const deadline = Date.now() + ms;
            while (Date.now() < deadline) {
              const value = predicate();
              if (value) return value;
              await new Promise(resolve => setTimeout(resolve, 100));
            }
            return null;
          };
          const selector = await waitFor(() => document.querySelector("#executionTargetSelect"));
          if (!selector) return { missing: true };
          const remote = Array.from(selector.options).find(option => option.value === "smoke-ssh");
          const disabled = Array.from(selector.options).find(option => option.value === "disabled-ssh");
          if (!remote) return { missing: true, hasDisabled: Boolean(disabled?.disabled) };
          const originalFetch = window.fetch;
          let capturedPreflight = null;
          window.fetch = async (input, init = {}) => {
            if (String(input).includes("/api/runs/preflight")) {
              capturedPreflight = JSON.parse(String(init.body || "{}"));
              return new Response(JSON.stringify({ valid: false, blocking_issues: [{ message: "browser smoke stop" }] }), {
                status: 200,
                headers: { "Content-Type": "application/json" }
              });
            }
            return originalFetch(input, init);
          };
          try {
            selector.value = "smoke-ssh";
            selector.dispatchEvent(new Event("change", { bubbles: true }));
            const prompt = document.querySelector("#prompt");
            prompt.value = "检查远程主机状态";
            prompt.dispatchEvent(new Event("input", { bubbles: true }));
            document.querySelector("#send")?.click();
            await waitFor(() => capturedPreflight, 6000);
            return {
              missing: false,
              selected: selector.value,
              preflightTarget: capturedPreflight?.target_id || "",
              hasDisabled: Boolean(disabled?.disabled),
              fieldVisible: !document.querySelector("#executionTargetField")?.classList.contains("hidden")
            };
          } finally {
            window.fetch = originalFetch;
            localStorage.setItem("goflow.playground.executionTarget", "local");
          }
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing") or not metrics.get("fieldVisible") or not metrics.get("hasDisabled"):
        raise AssertionError(f"task execution Target selection is incomplete, got {metrics}")
    if metrics.get("selected") != "smoke-ssh" or metrics.get("preflightTarget") != "smoke-ssh":
        raise AssertionError(f"task execution Target selection was not passed to preflight, got {metrics}")


def capture_execution_target_screenshots(
    browser: Path,
    base_url: str,
    timeout: float,
    profile_dir: Path,
    output_dir: Path,
) -> None:
    cases = [
        ("tasks-target-desktop", "playground", (1440, 1000), False, False),
        ("tasks-target-dark-desktop", "playground", (1440, 1000), True, False),
        ("tasks-target-mobile", "playground", (390, 844), False, False),
    ]
    for name, view, viewport, dark, open_form in cases:
        selector = "#executionTargetField"
        script = f"""
        (async () => {{
          const deadline = Date.now() + 8000;
          while (Date.now() < deadline && !document.querySelector({json.dumps(selector)})) {{
            await new Promise(resolve => setTimeout(resolve, 100));
          }}
          document.documentElement.dataset.theme = {json.dumps("dark" if dark else "light")};
          const node = document.querySelector({json.dumps(selector)});
          await new Promise(resolve => setTimeout(resolve, 250));
          const focus = node;
          focus?.scrollIntoView({{ block: "start" }});
          await new Promise(resolve => setTimeout(resolve, 250));
          return {{
            found: Boolean(focus),
            horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
            width: document.documentElement.clientWidth,
            focusWidth: focus?.getBoundingClientRect().width || 0
          }};
        }})()
        """
        metrics = evaluate_browser_json(
            browser,
            f"{base_url}/console?lang=zh#{view}",
            script,
            timeout,
            profile_dir,
            viewport=viewport,
            screenshot_path=output_dir / f"{name}.png",
        )
        if not metrics.get("found") or metrics.get("horizontalOverflow"):
            raise AssertionError(f"execution Target screenshot {name} failed layout checks: {metrics}")
        if float(metrics.get("focusWidth") or 0) > float(metrics.get("width") or 0) + 1:
            raise AssertionError(f"execution Target screenshot {name} exceeds viewport: {metrics}")


def assert_provider_capability_probe_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#settings",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector("[data-provider-setup-form]")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const form = document.querySelector("[data-provider-setup-form]");
          const button = form?.querySelector("[data-provider-setup-test]");
          const output = form?.querySelector("[data-provider-setup-output]");
          if (!form || !button || !output) return { missing: true };
          const apiKey = form.querySelector("[data-provider-setup-api-key]");
          if (apiKey) apiKey.value = "browser-smoke-key";
          const originalFetch = window.fetch;
          window.fetch = async (input, init) => {
            if (String(input).includes("/api/resources/providers/") && String(input).endsWith("/test")) {
              return new Response(JSON.stringify({
                valid: true,
                status: "limited",
                latency_ms: 128,
                capabilities: [
                  { name: "chat", status: "supported", supported: true, latency_ms: 20 },
                  { name: "streaming", status: "supported", supported: true, latency_ms: 32 },
                  { name: "tool_calling", status: "supported", supported: true, latency_ms: 41 },
                  { name: "structured_output", status: "unsupported", supported: false, message: "not available" }
                ]
              }), { status: 200, headers: { "Content-Type": "application/json" } });
            }
            return originalFetch(input, init);
          };
          try {
            button.click();
            const resultDeadline = Date.now() + 5000;
            while (Date.now() < resultDeadline && !output.querySelector(".settings-provider-probe-list")) {
              await new Promise(resolve => setTimeout(resolve, 100));
            }
            const items = Array.from(output.querySelectorAll(".settings-provider-probe-list span"));
            const insideViewport = element => {
              const rect = element.getBoundingClientRect();
              return rect.width > 0 && rect.left >= 0 && rect.right <= innerWidth;
            };
            return {
              missing: false,
              viewport: [innerWidth, innerHeight],
              documentWidth: document.documentElement.scrollWidth,
              overflowX: document.documentElement.scrollWidth > innerWidth,
              itemCount: items.length,
              allItemsVisible: items.every(insideViewport),
              unavailableCount: items.filter(item => item.classList.contains("unavailable")).length,
              outputText: output.textContent.trim()
            };
          } finally {
            window.fetch = originalFetch;
          }
        })()
        """,
        timeout,
        profile_dir,
        viewport=(390, 844),
    )
    if metrics.get("missing"):
        raise AssertionError(f"Provider capability test controls are missing, got {metrics}")
    viewport = metrics.get("viewport") or [0, 0]
    if not (320 <= int(viewport[0]) <= 540 and int(viewport[1]) >= 640) or metrics.get("overflowX"):
        raise AssertionError(f"Provider capability results should fit the mobile viewport, got {metrics}")
    if metrics.get("itemCount") != 4 or not metrics.get("allItemsVisible") or metrics.get("unavailableCount") != 1:
        raise AssertionError(f"Provider capability results should expose four readable capability states, got {metrics}")
    if not str(metrics.get("outputText", "")).strip():
        raise AssertionError(f"Provider capability test should render a visible summary, got {metrics}")
    output_text = str(metrics.get("outputText") or "")
    if "settings.provider" in output_text or any(value in output_text for value in ("streaming", "tool_calling", "structured_output")):
        raise AssertionError(f"Provider capability result still exposes untranslated data labels, got {metrics}")


def assert_chinese_runtime_step_copy(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const i18n = await import('/assets/i18n.js');
          const inputs = [
            'plan',
            'implement',
            'recovery-verify',
            'final-quality',
            'waiting for model response...',
            'waiting for model response after tool results...',
            'running verifier pass with auditor (provider=primary model=gpt-6.1-sol)...',
            'verifier pass failed: context canceled',
            'read Responses stream: unexpected EOF before terminal response event',
            'reasoning over tool observations',
            'agent=quality-engineer mode=audit',
            'gathering context with tools: file_tools/read_file',
            'workflow tool render_office_preview approved',
            'change-slice evidence recorded for stage recovery-verify',
            'workflow stage read limit reached (8); use the supplied stage evidence and produce the stage report',
            'workflow stage implementation-path: llm chat: read Responses stream error: context canceled',
            'context canceled',
            'workflow run cancelled while paused',
            'workflow interrupted by process restart; retry from the saved request',
            'workflow stage revision-summary: llm chat: temporary model failure: responses status failed: Our servers are currently overloaded. Please try again later.',
            'send Responses stream request: Post "https://gateway.example/v1/responses": net/http: TLS handshake timeout',
            'provider response was interrupted before tool execution: llm chat: temporary model failure: responses status failed: Our servers are currently overloaded. Please try again later.',
            'send Responses stream request: Post "https://gateway.example/v1/responses": read tcp 127.0.0.1:3456->127.0.0.1:443: wsarecv: An existing connection was forcibly closed by the remote host.'
            ,'tool call read_file_range was not executed: change slice slice-docs is not ready: dependencies for slice "slice-docs" are not complete'
          ];
          return {
            language: i18n.currentLanguage(),
            outputs: inputs.map(input => ({ input, output: i18n.localizedText(input) }))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("language") != "zh":
        raise AssertionError(f"runtime step translation test did not load Chinese mode: {metrics}")
    outputs = metrics.get("outputs") or []
    if len(outputs) != 24:
        raise AssertionError(f"runtime step translation test returned incomplete output: {metrics}")
    untranslated = []
    forbidden = re.compile(
        r"\b(plan|implement|recovery|quality|waiting for model|running verifier pass|verifier pass failed|reasoning over|gathering context|workflow tool|change-slice evidence|change slice|dependencies for slice|read limit reached|workflow stage|responses stream|context canceled|workflow run cancelled|workflow interrupted|process restart|temporary model failure|currently overloaded|TLS handshake timeout|interrupted before tool execution|wsarecv|forcibly closed)\b",
        re.IGNORECASE,
    )
    for item in outputs:
        output = str(item.get("output") or "")
        if not re.search(r"[\u3400-\u9fff]", output) or forbidden.search(output):
            untranslated.append(item)
    if untranslated:
        raise AssertionError(f"Chinese runtime steps still expose English user-facing copy: {untranslated}")


def assert_task_timeline_filters_stream_fragments(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const chat = await import('/assets/views/chat.js');
          const rows = [
            { kind: 'event', type: 'reasoning', agent_id: 'software-engineer', summary: 'should' },
            { kind: 'event', type: 'text', agent_id: 'software-engineer', summary: 'adding' },
            { kind: 'event', type: 'status', agent_id: 'software-engineer', summary: 'waiting for model response...' },
            { kind: 'event', type: 'status', agent_id: 'software-engineer', reason: 'provider_request_started', summary: 'provider request attempt 1/3 started; waiting up to 5m0s for a response' },
            { kind: 'event', type: 'status', agent_id: 'software-engineer', reason: 'provider_request_retry', severity: 'warning', summary: 'provider request attempt 1/3 failed before assistant output; retrying attempt 2/3 in 2s: timeout' },
            { kind: 'event', type: 'status', agent_id: 'software-engineer', reason: 'provider_response_connected', at: new Date(Date.now() - 65000).toISOString(), summary: 'provider request attempt 2/3 connected; waiting for streamed model output' },
            { kind: 'event', type: 'tool_call', agent_id: 'software-engineer', tool_name: 'read_file', tool_call_id: 'call-read', arguments_summary: 'web/app.py' },
            { kind: 'event', type: 'tool_result', agent_id: 'software-engineer', tool_name: 'read_file', tool_call_id: 'call-read', summary: JSON.stringify({ relative_path: 'web/app.py', size: 11013, content: 'raw source must not be shown' }) },
            { kind: 'event', type: 'task_stage', agent_id: 'software-engineer', stage: 'inspect', summary: 'gathering context with tools: file_tools/read_file' },
            { kind: 'artifact', title: 'completion_integrity', summary: '阶段暂停，尚无完成性验收结果，需要等待模型恢复后继续执行并验证。', status: 'blocked' }
          ];
          const items = chat.timelineItemsForTaskRun(rows, { run_type: 'agent', agent_id: 'software-engineer', status: 'running', timeline: rows });
          return {
            count: items.length,
            titles: items.map(item => item.title || ''),
            details: items.map(item => item.detail || ''),
            artifactTone: items.find(item => item.title === '完成状态检查')?.tone,
            raw: JSON.stringify(items)
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("count") != 6:
        raise AssertionError(f"task timeline should keep meaningful steps and filter stream fragments, got {metrics}")
    if metrics.get("artifactTone") == "success" or not metrics.get("artifactTone"):
        raise AssertionError(f"unfinished evidence must not appear as a successful execution, got {metrics}")
    raw = str(metrics.get("raw") or "").lower()
    if any(fragment in raw for fragment in ('"should"', '"adding"', "waiting for model response")):
        raise AssertionError(f"task timeline still exposes model stream fragments, got {metrics}")
    if "模型请求" not in str(metrics.get("raw") or "") or "provider request attempt" in raw:
        raise AssertionError(f"task timeline should expose localized provider request and retry states, got {metrics}")
    if "已等待 1 分" not in str(metrics.get("raw") or ""):
        raise AssertionError(f"active Provider wait should expose a live localized elapsed duration, got {metrics}")
    rendered = str(metrics.get("raw") or "")
    if "已完成：读取文件" not in rendered or "web/app.py" not in rendered or "raw source must not be shown" in rendered:
        raise AssertionError(f"task timeline should collapse tool calls into clear result summaries, got {metrics}")


def assert_human_readable_run_outputs(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    result = evaluate_browser_json(browser, f"{base_url}/console?lang=zh#playground", """
      (async () => {
        const chat = await import('/assets/views/chat.js');
        const template = 'Implementation summary, files changed, verification attempted, deviations, remaining work, and risk notes.';
        const actual = '## 最终总结\\n已修改测试文件，并完成真实验证。\\n\\n## 剩余工作\\n无。';
        const run = {status:'completed',final_output:actual,artifacts:[{title:'Implementation report',kind:'report',summary:template}]};
        const fallback = {status:'completed',summary:actual,artifacts:run.artifacts};
        const rows = [
          {kind:'event',type:'tool_result',tool_name:'write_file',summary:'truncated JSON...',tool_result:{relative_path:'tests/test_ui_smoke.py',status:'created',added_lines:56,deleted_lines:0}},
          {kind:'event',type:'tool_result',tool_name:'delete_file',tool_result:{relative_path:'obsolete.py',status:'deleted',added_lines:0,deleted_lines:8}},
          {kind:'event',type:'tool_result',tool_name:'start_process',tool_result:{process_id:'p1',pid:123,exit_code:-1,running:true,preview_url:'http://127.0.0.1:5001/'}},
          {kind:'event',type:'tool_result',tool_name:'stop_process',tool_result:{process_id:'p1',pid:123,exit_code:1,running:false,stop_requested:true,preview_url:'http://127.0.0.1:5001/'}}
        ];
        const items = chat.timelineItemsForTaskRun(rows,{run_type:'agent',status:'completed'});
        const statusItem=chat.timelineItemsForTaskRun([{kind:'event',type:'agent_run_completed',agent_id:'software-engineer',summary:'预览验证完成',status:'completed'}],{run_type:'agent',status:'completed'})[0];
        return {output:chat.workflowRunResultText(run),fallback:chat.workflowRunResultText(fallback),items,statusItem};
      })()
    """, timeout, profile_dir)
    for key in ("output", "fallback"):
        if "最终总结" not in result.get(key, "") or "Implementation summary" in result.get(key, ""):
            raise AssertionError(f"artifact description replaced actual final output: {result}")
    items = result.get("items") or []
    if len(items) != 4 or "tests/test_ui_smoke.py" not in items[0].get("detail", "") or "新增 56 行" not in items[0].get("detail", "") or "删除 8 行" not in items[1].get("detail", ""):
        raise AssertionError(f"file paths and line deltas are missing: {result}")
    if items[2].get("tone") == "error" or items[3].get("tone") == "error" or not items[2].get("preview", {}).get("running") or items[3].get("preview", {}).get("running"):
        raise AssertionError(f"managed lifecycle or preview state is misleading: {result}")
    if "completed" in result.get("statusItem", {}).get("detail", "") or "已完成" not in result.get("statusItem", {}).get("detail", ""):
        raise AssertionError(f"raw terminal status leaked into the Chinese timeline: {result}")


def assert_history_usage_replay(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    result = evaluate_browser_json(browser, f"{base_url}/console?lang=zh#playground", """
      (async () => {
        const wait = async fn => { for(let i=0;i<200;i++){if(fn())return;await new Promise(r=>setTimeout(r,50));}throw Error('view not ready'); };
        await wait(()=>document.querySelector('.chat-grid')?.parentElement.classList.contains('view-ready'));
        const root = document.querySelector('.chat-grid').parentElement;
        const runID = 'run-agent-smoke-lazy';
        const originalFetch = window.fetch.bind(window);
        const payload = await (await originalFetch(`/api/runs/${runID}?view=workbench`)).json();
        payload.token_usage = {prompt_tokens:2000,output_tokens:40,cached_tokens:800,total_tokens:2040,model_calls:4};
        payload.timeline = Array.from({length:4},(_,index)=>({kind:'event',type:'token_usage',seq:101+index,agent_id:'chat',prompt_tokens:500,output_tokens:10,cached_tokens:200,output_kind:'text'}));
        payload.final_output = '服务验证通过。\\n\\nVerification pass:\\nStatus: pass\\nChecked: 服务正常\\nRisks: none\\nNext: none';
        window.fetch = async (input,options) => {
          const url = new URL(typeof input === 'string' ? input : input.url,location.origin);
          if(url.pathname === `/api/runs/${runID}` && url.searchParams.get('view') === 'workbench')return new Response(JSON.stringify(payload),{headers:{'Content-Type':'application/json'}});
          return originalFetch(input,options);
        };
        try {
          window.location.hash='#catalog';
          await wait(()=>root.dataset.view==='catalog'&&root.classList.contains('view-ready'));
          localStorage.setItem('goflow.playground.runState',JSON.stringify({mode:'agent',runID,updatedAt:Date.now()}));
          window.location.hash='#playground';
          await wait(()=>root.dataset.view==='playground'&&root.classList.contains('view-ready'));
          await new Promise(r=>setTimeout(r,500));
          const shown=Array.from(root.querySelectorAll('#runTimelineTotals dd')).map(node=>node.innerText);
          const text=root.querySelector('#resultOutput')?.innerText||'';
          return {shown,body:root.querySelector('#runTimelineTotals')?.innerText||'',text};
        } finally { window.fetch = originalFetch; }
      })()
    """, timeout, profile_dir)
    values = result.get("shown") or []
    if not values or not values[0].startswith("2K") or not values[1].startswith("0.04K"):
        raise AssertionError(f"snapshot replay counted reported usage twice: {result}")
    text = str(result.get("text") or "")
    if "风险：none" in text or "下一步：none" in text or "风险：无" not in text:
        raise AssertionError(f"verifier no-risk details are not localized: {result}")


def assert_run_token_accounting(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const chat = await import('/assets/views/chat.js');
          const events = [
            { type: 'token_usage', prompt_tokens: 600, output_tokens: 80, cached_tokens: 240 },
            { type: 'token_usage', prompt_tokens: 400, output_tokens: 120, cached_tokens: 160 }
          ];
          const retained = chat.runTokenUsageSummary({
            budget_prompt_tokens: 5000,
            budget_output_tokens: 900,
            budget_cached_tokens: 2100,
            budget_total_tokens: 5900,
            events
          });
          const persisted = chat.runTokenUsageSummary({
            budget_prompt_tokens: 2000,
            budget_output_tokens: 300,
            budget_cached_tokens: 500,
            budget_total_tokens: 2300,
            events: []
          });
          const bounded = chat.runTokenUsageSummary({
            token_usage: { prompt_tokens: 1500, output_tokens: 250, cached_tokens: 600, total_tokens: 1750, model_calls: 3 },
            budget_prompt_tokens: 9000,
            budget_cached_tokens: 1000,
            events: [{ type: 'token_usage', prompt_tokens: 100, output_tokens: 10, cached_tokens: 0 }]
          });
          const timeline = chat.extractTimelineTokenUsage({
            prompt_tokens: 800,
            output_tokens: 100,
            cached_tokens: 200,
            cache_hit_rate: 0
          });
          const fullFinal = JSON.stringify({
            summary: '完整最终结果',
            decision: 'blocked',
            evidence: ['证据一', '证据二'],
            verification: [{ command: 'python -m unittest', result: 'passed' }],
            blockers: ['缺少测试文件'],
            next_actions: ['执行启动级验证']
          }, null, 2);
          const resultText = chat.workflowRunResultText({
            summary: '[route/planner] truncated...',
            completed_stages: [
              { stage: 'route-request', summary: 'route' },
              { stage: 'consolidate', output_values: { summary: '过度简化的几句话' }, result: { output: fullFinal } }
            ]
          });
          const effectiveStatus = chat.workflowRunEffectiveStatus({
            status: 'completed',
            completed_stages: [{ stage: 'consolidate', output_values: { decision: 'blocked' } }]
          });
          return { retained, persisted, bounded, timeline, fullFinal, resultText, effectiveStatus };
        })()
        """,
        timeout,
        profile_dir,
    )
    retained = metrics.get("retained") or {}
    persisted = metrics.get("persisted") or {}
    bounded = metrics.get("bounded") or {}
    timeline = metrics.get("timeline") or {}
    if (
        retained.get("prompt") != 1000
        or retained.get("output") != 200
        or retained.get("cached") != 400
        or retained.get("total") != 1200
        or retained.get("samples") != 2
        or abs(float(retained.get("cacheHitRate") or 0) - 0.4) > 0.0001
    ):
        raise AssertionError(f"run totals must use retained token timeline events without double counting: {metrics}")
    if (
        persisted.get("prompt") != 2000
        or persisted.get("output") != 300
        or persisted.get("cached") != 500
        or persisted.get("total") != 2300
        or abs(float(persisted.get("cacheHitRate") or 0) - 0.25) > 0.0001
    ):
        raise AssertionError(f"persisted token totals must remain a fallback when events are unavailable: {metrics}")
    if (
        bounded.get("prompt") != 1500
        or bounded.get("output") != 250
        or bounded.get("cached") != 600
        or bounded.get("total") != 1750
        or bounded.get("samples") != 3
        or abs(float(bounded.get("cacheHitRate") or 0) - 0.4) > 0.0001
    ):
        raise AssertionError(f"bounded workbench token totals must use the retained current-run aggregate: {metrics}")
    if abs(float(timeline.get("cacheHitRate") or 0) - 0.25) > 0.0001:
        raise AssertionError(f"timeline cache hit rate must be cached/input, got {metrics}")
    if metrics.get("resultText") != metrics.get("fullFinal") or "完整最终结果" not in str(metrics.get("resultText") or ""):
        raise AssertionError(f"workflow result must preserve the complete final stage output, got {metrics}")
    if metrics.get("effectiveStatus") != "blocked":
        raise AssertionError(f"historical completed runs with blocked final decisions must display as blocked: {metrics}")


def assert_playground_context_prompts(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=en#playground",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector("#runContextGuide")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const prompt = document.querySelector("#prompt");
          const guide = document.querySelector("#runContextGuide");
          if (!prompt || !guide) return { missing: true };
          prompt.value = "Please edit README.md and run tests";
          prompt.dispatchEvent(new Event("input", { bubbles: true }));
          await new Promise(resolve => setTimeout(resolve, 250));
          const items = Array.from(guide.querySelectorAll(".run-context-guide-item"));
          const actionButtons = Array.from(guide.querySelectorAll("[data-run-context-action]"));
          const runtime = await fetch("/api/runtime").then(response => response.json()).catch(() => ({}));
          const workspace = runtime.workspace || {};
          const workspaceConfirmed = Boolean(workspace.confirmed);
          return {
            missing: false,
            hidden: guide.classList.contains("hidden"),
            text: guide.textContent,
            actionNames: actionButtons.map(button => button.dataset.runContextAction || ""),
            itemCount: items.length,
            workspaceRoot: workspace.root || workspace.display || "",
            workspaceConfirmed
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing") or metrics.get("hidden"):
        raise AssertionError(f"playground context prompts should appear for workspace/file tasks, got {metrics}")
    actions = set(metrics.get("actionNames") or [])
    required_actions = {"files"}
    if metrics.get("workspaceRoot") and not metrics.get("workspaceConfirmed"):
        required_actions.add("workspace")
    if not required_actions.issubset(actions):
        raise AssertionError(f"playground context prompts should include relevant file and workspace actions, got {metrics}")
    text = str(metrics.get("text", ""))
    if "Attach exact files" not in text or (metrics.get("workspaceRoot") and not metrics.get("workspaceConfirmed") and "Confirm workspace first" not in text):
        raise AssertionError(f"playground context prompts should use ordinary-user copy, got {metrics}")

    zh = evaluate_browser_json(
        browser,
        zh_url(base_url, "/console#playground"),
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector("#runContextGuide")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const prompt = document.querySelector("#prompt");
          const guide = document.querySelector("#runContextGuide");
          if (!prompt || !guide) return { missing: true };
          prompt.value = "请修改 README.md 并运行测试";
          prompt.dispatchEvent(new Event("input", { bubbles: true }));
          await new Promise(resolve => setTimeout(resolve, 250));
          const runtime = await fetch("/api/runtime").then(response => response.json()).catch(() => ({}));
          const workspace = runtime.workspace || {};
          return {
            missing: false,
            text: guide.textContent,
            actionNames: Array.from(guide.querySelectorAll("[data-run-context-action]")).map(button => button.dataset.runContextAction || ""),
            workspaceRoot: workspace.root || workspace.display || "",
            workspaceConfirmed: Boolean(workspace.confirmed)
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if zh.get("missing"):
        raise AssertionError(f"Chinese playground context prompts missing expected nodes, got {zh}")
    zh_text = str(zh.get("text", ""))
    if "引用精确文件" not in zh_text or (zh.get("workspaceRoot") and not zh.get("workspaceConfirmed") and "先确认工作区" not in zh_text):
        raise AssertionError(f"Chinese playground context prompts should be localized, got {zh}")


def assert_workflow_developer_fields_folded(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/workflows",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector('[data-template="condition"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const conditionButton = document.querySelector('[data-template="condition"]');
          if (!conditionButton) return { missingPalette: true };
          const initialPaletteTypes = Array.from(document.querySelectorAll('.node-palette [data-node-type]')).map(node => node.dataset.nodeType).sort();
          conditionButton.click();
          const nodeDeadline = Date.now() + 5000;
          while (Date.now() < nodeDeadline && !document.querySelector('.flow-node[data-stage-name^="condition"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const advancedToggle = document.querySelector("#workflowAdvancedToggle");
          if (!advancedToggle) return { missingAdvancedToggle: true };
          if (advancedToggle.getAttribute("aria-expanded") !== "true") {
            advancedToggle.click();
            await new Promise(resolve => setTimeout(resolve, 350));
          }
          const advancedPaletteTypes = Array.from(document.querySelectorAll('.node-palette [data-node-type]')).map(node => node.dataset.nodeType).sort();
          const advancedTab = document.querySelector('[data-workflow-inspector-tab="advanced"]');
          if (!advancedTab) return { missingTabs: true };
          advancedTab.click();
          await new Promise(resolve => setTimeout(resolve, 250));
          const raw = document.querySelector("#stageAdvancedRawPanel");
          const routes = document.querySelector("#stageRoutes");
          const params = document.querySelector("#stageParams");
          const openDeveloper = document.querySelector('[data-stage-task-open-developer]');
          return {
            missingPalette: false,
            missingAdvancedToggle: false,
            missingTabs: false,
            advancedOpen: Boolean(document.querySelector(".studio")?.classList.contains("advanced-open")),
            initialPaletteTypes,
            advancedPaletteTypes,
            rawExists: Boolean(raw),
            rawClosed: raw ? raw.open === false : false,
            rawHidden: raw ? raw.classList.contains("hidden") : true,
            routesInRaw: Boolean(routes && raw && raw.contains(routes.closest("label") || routes)),
            paramsHiddenOrRaw: Boolean(params && (params.closest("#stageAdvancedRawPanel") || params.closest("label")?.classList.contains("hidden"))),
            developerButton: Boolean(openDeveloper),
            advancedText: document.querySelector("#stageAdvancedPanel")?.textContent || ""
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingPalette") or metrics.get("missingAdvancedToggle") or metrics.get("missingTabs") or not metrics.get("advancedOpen"):
        raise AssertionError(f"workflow developer field smoke missing controls, got {metrics}")
    expected_types = ["agent", "checkpoint", "condition", "end", "parallel", "sub_workflow", "tool"]
    if metrics.get("initialPaletteTypes") != expected_types or metrics.get("advancedPaletteTypes") != expected_types:
        raise AssertionError(f"workflow palette should remain fixed to seven composite nodes, got {metrics}")
    if not metrics.get("rawExists") or not metrics.get("rawClosed") or not metrics.get("routesInRaw"):
        raise AssertionError(f"workflow raw route maps should stay in a closed developer subpanel, got {metrics}")
    if not metrics.get("paramsHiddenOrRaw") or not metrics.get("developerButton"):
        raise AssertionError(f"workflow params/raw fields should be optional and reachable from a developer action, got {metrics}")


def assert_run_history_lazy_loading(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const marker = {json.dumps(smoke_run["marker"])};
          const runID = {json.dumps(smoke_run["run_id"])};
          const runKey = `agent:${{runID}}`;
          const listDeadline = Date.now() + 6000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const panel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const button = panel?.querySelector('[data-history-load="timeline"]');
          const content = panel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !panel || !button || !content) return {{ missing: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          const beforeBodyHasMarker = document.body.textContent.includes(marker);
          const beforeContentHasMarker = content.textContent.includes(marker);
          const beforeDetailScrollWidth = Math.round(detail.scrollWidth);
          const beforeDetailClientWidth = Math.round(detail.clientWidth);
          button.click();
          const loadedDeadline = Date.now() + 6000;
          while (Date.now() < loadedDeadline && !content.textContent.includes(marker)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timeline = panel.querySelector(".run-history-lazy-timeline");
          const buttonStyle = getComputedStyle(button);
          return {{
            missing: false,
            beforeBodyHasMarker,
            beforeContentHasMarker,
            afterBodyHasMarker: document.body.textContent.includes(marker),
            afterContentHasMarker: content.textContent.includes(marker),
            hasTimeline: Boolean(timeline),
            panelLoaded: panel.classList.contains("loaded"),
            buttonDisabled: button.disabled,
            buttonBusy: button.getAttribute("aria-busy"),
            buttonText: button.textContent.trim(),
            buttonWhiteSpace: buttonStyle.whiteSpace,
            beforeDetailScrollWidth,
            beforeDetailClientWidth,
            detailScrollWidth: Math.round(detail.scrollWidth),
            detailClientWidth: Math.round(detail.clientWidth),
            timelineScrollWidth: timeline ? Math.round(timeline.scrollWidth) : 0,
            timelineClientWidth: timeline ? Math.round(timeline.clientWidth) : 0
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("run history lazy loading missing expected nodes")
    if metrics.get("beforeBodyHasMarker") or metrics.get("beforeContentHasMarker"):
        raise AssertionError(f"run history loaded timeline detail before click, got {metrics}")
    if not metrics.get("afterBodyHasMarker") or not metrics.get("afterContentHasMarker") or not metrics.get("hasTimeline"):
        raise AssertionError(f"run history timeline did not lazy-load marker after click, got {metrics}")
    if not metrics.get("panelLoaded") or not metrics.get("buttonDisabled"):
        raise AssertionError(f"run history timeline panel should be marked loaded and disabled after click, got {metrics}")
    if metrics.get("buttonBusy") != "false":
        raise AssertionError(f"run history timeline button should clear busy state after load, got {metrics}")
    if int(metrics.get("detailScrollWidth") or 0) > int(metrics.get("detailClientWidth") or 0) + 2:
        raise AssertionError(f"run history detail should not create horizontal overflow, got {metrics}")
    if int(metrics.get("timelineScrollWidth") or 0) > int(metrics.get("timelineClientWidth") or 0) + 2:
        raise AssertionError(f"run history lazy timeline should not create horizontal overflow, got {metrics}")


def assert_workflow_restart_reconciliation_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["interrupted_workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) runButton.click();
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelineButton = stack?.querySelector('[data-history-load="timeline"]');
          const timelineContent = stack?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !timelineButton || !timelineContent) return {{ missing: true, hasRunButton: Boolean(runButton) }};
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("进程重启") && !timelineContent.textContent.includes("process_restart")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          return {{
            missing: false,
            detailText: detail.textContent,
            timelineText: timelineContent.textContent,
            hasRetry: Boolean(detail.querySelector('[data-history-action="retry"]:not([disabled])')),
            hasCancel: Boolean(detail.querySelector('[data-history-action="cancel"]'))
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"restart reconciliation history nodes are missing, got {metrics}")
    detail_text = str(metrics.get("detailText", ""))
    if not any(label in detail_text for label in ("执行中断", "Interrupted")) or not metrics.get("hasRetry") or metrics.get("hasCancel"):
        raise AssertionError(f"interrupted workflow should expose Retry without Cancel, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    chinese_explanation = "进程重启" in timeline_text and "工作流因进程重启中断" in timeline_text
    english_explanation = "process_restart" in timeline_text and "workflow interrupted by process restart" in timeline_text
    if not chinese_explanation and not english_explanation:
        raise AssertionError(f"restart reconciliation timeline should explain the interruption, got {metrics}")


def assert_run_history_artifacts_lazy_loading(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["run_id"])};
          const runKey = `agent:${{runID}}`;
          const listDeadline = Date.now() + 6000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const panel = stack?.querySelector('[data-history-lazy-panel="artifacts"]');
          const button = panel?.querySelector('[data-history-load="artifacts"]');
          const content = panel?.querySelector('[data-history-lazy-content="artifacts"]');
          if (!stack || !panel || !button || !content) return {{ missing: true, hasRunButton: Boolean(runButton), detailText: document.querySelector("#runHistoryDetail")?.textContent || "" }};
          const beforeBodyHasArtifact = document.body.textContent.includes("Smoke Artifact Result");
          const beforeContentHasArtifact = content.textContent.includes("Smoke Artifact Result");
          button.click();
          const loadedDeadline = Date.now() + 6000;
          while (Date.now() < loadedDeadline && !content.textContent.includes("Smoke Artifact Result")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const viewer = panel.querySelector(".run-artifact-viewer");
          const preview = panel.querySelector(".run-artifact-preview");
          const listItem = panel.querySelector(".run-artifact-list-item");
          const previewBody = panel.querySelector(".run-artifact-content");
          const loadButton = panel.querySelector(".run-history-load-copy strong");
          return {{
            missing: false,
            beforeBodyHasArtifact,
            beforeContentHasArtifact,
            afterBodyHasArtifact: document.body.textContent.includes("Smoke Artifact Result"),
            afterContentHasArtifact: content.textContent.includes("Smoke Artifact Result"),
            hasViewer: Boolean(viewer),
            hasPreview: Boolean(preview),
            hasListItem: Boolean(listItem),
            hasPreviewBody: Boolean(previewBody),
            loadButtonText: loadButton ? loadButton.textContent.trim() : "",
            panelLoaded: panel.classList.contains("loaded"),
            buttonDisabled: button.disabled,
            buttonBusy: button.getAttribute("aria-busy"),
            displayedArtifactRef: panel.querySelector(".run-artifact-preview code")?.textContent.trim() || ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("run history artifacts lazy loading missing expected nodes")
    if metrics.get("beforeBodyHasArtifact") or metrics.get("beforeContentHasArtifact"):
        raise AssertionError(f"run history artifacts loaded content before click, got {metrics}")
    if not metrics.get("afterBodyHasArtifact") or not metrics.get("afterContentHasArtifact"):
        raise AssertionError(f"run history artifacts did not lazy-load content after click, got {metrics}")
    if not metrics.get("hasViewer") or not metrics.get("hasPreview") or not metrics.get("hasListItem") or not metrics.get("hasPreviewBody"):
        raise AssertionError(f"run history artifacts viewer did not render expected subpanels, got {metrics}")
    if "Loaded" not in str(metrics.get("loadButtonText", "")):
        raise AssertionError(f"run history artifacts button label did not switch to loaded state, got {metrics}")
    if not metrics.get("panelLoaded") or not metrics.get("buttonDisabled"):
        raise AssertionError(f"run history artifacts panel should be marked loaded and disabled after click, got {metrics}")
    if metrics.get("buttonBusy") != "false":
        raise AssertionError(f"run history artifacts button should clear busy state after load, got {metrics}")
    if not str(metrics.get("displayedArtifactRef", "")).startswith("goflow://session-artifacts/"):
        raise AssertionError(f"run history artifacts should surface the selected artifact ref in the DOM, got {metrics}")


def assert_run_history_office_artifact_actions(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        zh_url(base_url, "/console#playground"),
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["run_id"])};
          const expectedPath = {json.dumps(smoke_run["office_artifact_path"])};
          const expectedPreviewPath = {json.dumps(smoke_run["office_preview_path"])};
          const runKey = `agent:${{runID}}`;
          const listDeadline = Date.now() + 7000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) runButton.click();
          const detailDeadline = Date.now() + 7000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const panel = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"] [data-history-lazy-panel="artifacts"]`);
          const loadButton = panel?.querySelector('[data-history-load="artifacts"]');
          if (loadButton && !loadButton.disabled) loadButton.click();
          const artifactDeadline = Date.now() + 7000;
          while (Date.now() < artifactDeadline && !panel?.querySelector("[data-office-edit-path]")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const office = panel?.querySelector(".office-artifact");
          const image = office?.querySelector("img");
          const download = office?.querySelector('a[href*="download=1"]');
          const preview = office?.querySelector('a[target="_blank"]');
          const edit = office?.querySelector("[data-office-edit-path]");
          const prompt = document.querySelector("#prompt");
          if (!office || !image || !download || !preview || !edit || !prompt) {{
            return {{ missing: true, panelText: panel?.textContent || "" }};
          }}
          image.scrollIntoView({{ block: "center", inline: "nearest" }});
          const imageDeadline = Date.now() + 5000;
          while (Date.now() < imageDeadline && (!image.complete || image.naturalWidth < 1)) {{
            await new Promise(resolve => setTimeout(resolve, 100));
          }}
          let decodedWidth = image.naturalWidth;
          let decodedHeight = image.naturalHeight;
          if (decodedWidth < 1 || decodedHeight < 1) {{
            const eagerImage = new Image();
            await new Promise(resolve => {{
              const done = () => {{
                decodedWidth = eagerImage.naturalWidth;
                decodedHeight = eagerImage.naturalHeight;
                resolve();
              }};
              eagerImage.onload = done;
              eagerImage.onerror = done;
              eagerImage.src = image.src;
              setTimeout(done, 5000);
            }});
          }}
          const [downloadResponse, previewResponse] = await Promise.all([fetch(download.href), fetch(preview.href)]);
          const [downloadBody, previewBody] = await Promise.all([downloadResponse.arrayBuffer(), previewResponse.arrayBuffer()]);
          edit.click();
          return {{
            missing: false,
            labels: Array.from(office.querySelectorAll("a,button")).map(node => node.textContent.trim()),
            editPath: edit.dataset.officeEditPath || "",
            promptValue: prompt.value,
            imageLoaded: decodedWidth > 0 && decodedHeight > 0,
            imageLoading: image.getAttribute("loading") || "",
            imagePath: new URL(image.src).searchParams.get("path"),
            downloadOK: downloadResponse.ok,
            downloadBytes: downloadBody.byteLength,
            downloadDisposition: downloadResponse.headers.get("content-disposition") || "",
            downloadPath: downloadResponse.headers.get("x-goflow-artifact-path") || "",
            previewOK: previewResponse.ok,
            previewBytes: previewBody.byteLength,
            previewType: previewResponse.headers.get("content-type") || "",
            previewPath: previewResponse.headers.get("x-goflow-artifact-path") || "",
            expectedPath,
            expectedPreviewPath
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"Office artifact actions are missing from run history, got {metrics}")
    if metrics.get("labels") != ["下载文件", "打开预览", "继续编辑"]:
        raise AssertionError(f"Office artifact actions should use concise Chinese labels, got {metrics}")
    if metrics.get("editPath") != metrics.get("expectedPath") or metrics.get("expectedPath") not in str(metrics.get("promptValue", "")):
        raise AssertionError(f"Continue editing should place the exact artifact reference into the task prompt, got {metrics}")
    if not metrics.get("imageLoaded") or metrics.get("imageLoading") != "lazy" or metrics.get("imagePath") != metrics.get("expectedPreviewPath"):
        raise AssertionError(f"Office preview image did not decode from the expected workspace file, got {metrics}")
    if not metrics.get("downloadOK") or int(metrics.get("downloadBytes") or 0) < 200:
        raise AssertionError(f"Office download did not return the real DOCX payload, got {metrics}")
    if "attachment" not in str(metrics.get("downloadDisposition", "")) or metrics.get("downloadPath") != metrics.get("expectedPath"):
        raise AssertionError(f"Office download response is missing attachment/path evidence, got {metrics}")
    if not metrics.get("previewOK") or int(metrics.get("previewBytes") or 0) < 100 or "image/png" not in str(metrics.get("previewType", "")):
        raise AssertionError(f"Office preview endpoint did not return a non-empty PNG, got {metrics}")
    if metrics.get("previewPath") != metrics.get("expectedPreviewPath"):
        raise AssertionError(f"Office preview response should expose its workspace-relative path, got {metrics}")


def assert_workflow_budget_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 6000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const context = detail?.querySelector(".run-history-contextual-summary");
          const tokenDetails = context?.querySelector(".run-history-token-details");
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !context || !tokenDetails || !timelinePanel || !timelineButton || !timelineContent) return {{ missing: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          const beforeTimelineText = timelineContent.textContent;
          timelineButton.click();
          const loadedDeadline = Date.now() + 6000;
          while (Date.now() < loadedDeadline && !timelineContent.textContent.includes("Budget warning")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          return {{
            missing: false,
            detailText: detail.textContent,
            contextText: context.textContent,
            tokenText: tokenDetails.textContent,
            beforeTimelineText,
            timelineText: timelineContent.textContent,
            timelineLoaded: timelinePanel.classList.contains("loaded"),
            timelineBusy: timelineButton.getAttribute("aria-busy"),
            timelineDisabled: timelineButton.disabled,
            detailScrollWidth: Math.round(detail.scrollWidth),
            detailClientWidth: Math.round(detail.clientWidth)
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"workflow budget diagnostics missing expected run history nodes, got {metrics}")
    detail_text = str(metrics.get("detailText", ""))
    token_text = str(metrics.get("tokenText", ""))
    timeline_text = str(metrics.get("timelineText", ""))
    if "Saved tokens" not in token_text or "0.075K tokens" not in token_text:
        raise AssertionError(f"run history should show saved-token attribution, got {metrics}")
    if "Gross prompt baseline" not in token_text or "0.175K tokens" not in token_text:
        raise AssertionError(f"run history should show gross prompt baseline, got {metrics}")
    if "Net prompt sent" not in token_text or "0.1K tokens" not in token_text:
        raise AssertionError(f"run history should show net prompt sent, got {metrics}")
    if "Budget limit" not in token_text or "Prompt tokens: 180 / 240" not in token_text:
        raise AssertionError(f"run history should show budget limit diagnostics, got {metrics}")
    if "Budget warning" not in timeline_text or "workflow.budget.prompt_tokens" not in timeline_text:
        raise AssertionError(f"lazy workflow timeline should expose the budget warning source, got {metrics}")
    if "Budget warning" in str(metrics.get("beforeTimelineText", "")):
        raise AssertionError(f"budget timeline details should stay lazy before click, got {metrics}")
    if not metrics.get("timelineLoaded") or not metrics.get("timelineDisabled") or metrics.get("timelineBusy") != "false":
        raise AssertionError(f"budget timeline lazy panel should settle after load, got {metrics}")
    if int(metrics.get("detailScrollWidth") or 0) > int(metrics.get("detailClientWidth") or 0) + 2:
        raise AssertionError(f"workflow budget diagnostics should not overflow horizontally, got {metrics}")
    if "Smoke workflow completed with visible budget attribution" not in detail_text:
        raise AssertionError(f"workflow budget smoke should select the seeded workflow run, got {metrics}")


def assert_workflow_studio_budget_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const deadline = Date.now() + 8000;
          while (Date.now() < deadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const openButton = Array.from(detail?.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio" || button.textContent.includes("Open in Studio"));
          if (!detail || !openButton) return {{ missingChatAction: true, detailText: detail ? detail.textContent : "" }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="plan"]');
          const autoStageRuntime = document.querySelector("#stageRuntime");
          const autoSelected = Boolean(node?.classList.contains("selected"));
          const autoCurrent = Boolean(node?.classList.contains("runtime-current"));
          const autoPreviewVisible = Boolean(autoStageRuntime && !autoStageRuntime.classList.contains("hidden"));
          const autoPreviewText = autoStageRuntime?.textContent || "";
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const detailDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < detailDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("Budget limit"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const artifactPanelBefore = document.querySelector('#stageRuntime [data-runtime-diagnostic-panel="artifact"]');
          const artifactLoadButton = artifactPanelBefore?.querySelector('[data-stage-runtime-load]');
          if (artifactLoadButton) artifactLoadButton.click();
          const artifactDeadline = Date.now() + 8000;
          let artifactPanel = document.querySelector('#stageRuntime [data-runtime-diagnostic-panel="artifact"]');
          while (
            Date.now() < artifactDeadline &&
            (!artifactPanel || !artifactPanel.textContent.includes("Smoke plan budget artifact"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
            artifactPanel = document.querySelector('#stageRuntime [data-runtime-diagnostic-panel="artifact"]');
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            autoSelected,
            autoCurrent,
            autoPreviewVisible,
            autoPreviewText,
            hasRuntimeButton: Boolean(runtimeButton),
            runtimePaneHidden: Boolean(document.querySelector("#workflowRuntimePane")?.classList.contains("hidden")),
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            artifactPanelVisible: Boolean(artifactPanel),
            artifactLoadVisible: Boolean(artifactLoadButton),
            artifactText: artifactPanel ? artifactPanel.textContent : "",
            runOutputText: output ? output.textContent : "",
            canvasScrollWidth: Math.round(document.querySelector("#canvas")?.scrollWidth || 0),
            canvasClientWidth: Math.round(document.querySelector("#canvas")?.clientWidth || 0)
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatAction"):
        raise AssertionError(f"workflow history should expose Open in Studio action, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["workflow_run_id"]:
        raise AssertionError(f"Open in Studio should focus the saved workflow run, got {metrics}")
    if not metrics.get("nodeExists") or "has-runtime" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the plan node with runtime diagnostics, got {metrics}")
    node_text = str(metrics.get("nodeText", "")).lower()
    if "budget:" not in node_text or "prompt" not in node_text:
        raise AssertionError(f"Workflow Studio node should show a compact budget warning, got {metrics}")
    if not metrics.get("hasRuntimeButton") or metrics.get("runtimePaneHidden"):
        raise AssertionError(f"Workflow Studio should provide an obvious runtime detail path, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = [
        "budget_soft_limit_hit",
        "Budget limit",
        "Prompt tokens: 180 / 240",
        "Saved tokens",
        "75",
        "Gross prompt baseline",
        "175",
        "Net prompt sent",
        "100",
        "workflow.budget.prompt_tokens",
        "View prompt budget",
        "View model route",
        "backup",
        "small-worker",
        "stage.params.soft_budget_*",
        "Open artifacts",
    ]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing {missing_stage}, got {metrics}")
    artifact_text = str(metrics.get("artifactText", ""))
    required_artifact = ["Smoke plan budget artifact", "Budget warning artifact body", "goflow://workflow-runs"]
    missing_artifact = [needle for needle in required_artifact if needle not in artifact_text]
    if not metrics.get("artifactPanelVisible") or missing_artifact:
        raise AssertionError(f"Workflow Studio selected-node runtime should open stage artifacts, missing {missing_artifact}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if "Budget warning" not in output_text or "workflow.budget.prompt_tokens" not in output_text:
        raise AssertionError(f"Workflow Studio run log should expose budget warning source, got {metrics}")


def assert_workflow_budget_approval_action_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["budget_approval_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          const openButton = Array.from(detail?.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!detail || !timelineButton || !timelineContent || !openButton) return {{ missingChatAction: true, detailText: detail ? detail.textContent : "" }};
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("Current step: plan")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="plan"]');
          const autoStageRuntime = document.querySelector("#stageRuntime");
          const autoSelected = Boolean(node?.classList.contains("selected"));
          const autoCurrent = Boolean(node?.classList.contains("runtime-current"));
          const autoPreviewVisible = Boolean(autoStageRuntime && !autoStageRuntime.classList.contains("hidden"));
          const autoPreviewText = autoStageRuntime?.textContent || "";
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const actionDeadline = Date.now() + 10000;
          let stageRuntime = document.querySelector("#stageRuntime");
          let approveButton = document.querySelector('#stageRuntime [data-workflow-run-action="approve_budget"]');
          while (
            Date.now() < actionDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !approveButton)
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
            approveButton = document.querySelector('#stageRuntime [data-workflow-run-action="approve_budget"]');
          }}
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            autoSelected,
            autoCurrent,
            autoPreviewVisible,
            autoPreviewText,
            hasRuntimeButton: Boolean(runtimeButton),
            approveActionVisible: Boolean(approveButton),
            approveActionDisabled: approveButton ? approveButton.disabled : null,
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatAction"):
        raise AssertionError(f"budget approval run should expose Open in Studio action, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["budget_approval_run_id"]:
        raise AssertionError(f"Open in Studio should focus the budget approval run, got {metrics}")
    if "Current step: plan" not in str(metrics.get("timelineText", "")):
        raise AssertionError(f"budget approval timeline should identify the current workflow step, got {metrics}")
    if not metrics.get("nodeExists") or "runtime-waiting" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the budget approval node as waiting, got {metrics}")
    if not metrics.get("autoSelected") or not metrics.get("autoCurrent") or not metrics.get("autoPreviewVisible") or "Node run preview" not in str(metrics.get("autoPreviewText", "")):
        raise AssertionError(f"Workflow Studio should auto-focus the current node and show its live run preview, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the budget approval node, got {metrics}")
    if not metrics.get("approveActionVisible") or metrics.get("approveActionDisabled"):
        raise AssertionError(f"Workflow Studio selected-node runtime should expose an enabled approve budget action, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required = ["Approve budget", "View prompt budget", "budget_hard_limit_hit", "Prompt tokens: 250 / 240", "workflow.budget.prompt_tokens"]
    missing = [needle for needle in required if needle not in stage_text]
    if missing:
        raise AssertionError(f"Workflow Studio budget approval runtime is missing {missing}, got {metrics}")


def assert_workflow_blocked_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["blocked_workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !timelinePanel || !timelineButton || !timelineContent) {{
            return {{ missingChatNodes: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          }}
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("contract_validation_failed")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          const openButton = Array.from(detail.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!openButton) return {{ missingStudioAction: true, detailText: detail.textContent, timelineText }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="plan"]');
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const runtimeDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < runtimeDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("contract_validation_failed"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const actionDeadline = Date.now() + 6000;
          let retryButton = document.querySelector('#stageRuntime [data-workflow-run-action="retry"]');
          let escalateButton = document.querySelector('#stageRuntime [data-workflow-run-action="escalate_model"]');
          while (Date.now() < actionDeadline && (!retryButton || !escalateButton)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            retryButton = document.querySelector('#stageRuntime [data-workflow-run-action="retry"]');
            escalateButton = document.querySelector('#stageRuntime [data-workflow-run-action="escalate_model"]');
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatNodes: false,
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            hasRuntimeButton: Boolean(runtimeButton),
            retryActionVisible: Boolean(retryButton),
            retryActionDisabled: retryButton ? retryButton.disabled : null,
            escalateActionVisible: Boolean(escalateButton),
            escalateActionDisabled: escalateButton ? escalateButton.disabled : null,
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            runOutputText: output ? output.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatNodes") or metrics.get("missingStudioAction"):
        raise AssertionError(f"blocked workflow diagnostics missing Chat history path, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["blocked_workflow_run_id"]:
        raise AssertionError(f"Open in Studio should focus the blocked workflow run, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    required_timeline = ["contract_validation_failed", "smoke_summary_contract_required", "stages.plan.acceptance[0]"]
    missing_timeline = [needle for needle in required_timeline if needle not in timeline_text]
    if missing_timeline:
        raise AssertionError(f"Chat lazy timeline is missing blocked-stage diagnostics {missing_timeline}, got {metrics}")
    if not metrics.get("nodeExists") or "runtime-error" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the blocked plan node as runtime error, got {metrics}")
    node_text = str(metrics.get("nodeText", ""))
    if "contract:" not in node_text or "smoke_summary_contract_required" not in node_text:
        raise AssertionError(f"Workflow Studio node should show the blocked contract check, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the blocked node, got {metrics}")
    if not metrics.get("retryActionVisible") or metrics.get("retryActionDisabled"):
        raise AssertionError(f"Workflow Studio selected-node runtime should expose an enabled retry action for blocked runs, got {metrics}")
    if not metrics.get("escalateActionVisible") or metrics.get("escalateActionDisabled"):
        raise AssertionError(f"Workflow Studio selected-node runtime should expose an enabled model escalation action for blocked contract runs, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = ["contract_validation_failed", "smoke_summary_contract_required", "stages.plan.acceptance[0]", "View missing contract", "Escalate model"]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing blocked diagnostics {missing_stage}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if "Contract failure" not in output_text or "contract_validation_failed" not in output_text or "stages.plan.acceptance[0]" not in output_text:
        raise AssertionError(f"Workflow Studio run log should expose contract failure diagnostics, got {metrics}")


def assert_workflow_execution_readiness_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["execution_readiness_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !timelinePanel || !timelineButton || !timelineContent) {{
            return {{ missingChatNodes: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          }}
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("execution_readiness_blocked")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          const openButton = Array.from(detail.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!openButton) return {{ missingStudioAction: true, detailText: detail.textContent, timelineText }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="apply-live"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="apply-live"]');
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const runtimeDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < runtimeDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("execution_readiness"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatNodes: false,
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            hasRuntimeButton: Boolean(runtimeButton),
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            runOutputText: output ? output.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatNodes") or metrics.get("missingStudioAction"):
        raise AssertionError(f"execution readiness diagnostics missing Chat history path, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["execution_readiness_run_id"]:
        raise AssertionError(f"Open in Studio should focus the execution-readiness workflow run, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    required_timeline = ["execution_readiness_blocked", "execution_readiness", "stage.execution.allow_live_tools"]
    missing_timeline = [needle for needle in required_timeline if needle not in timeline_text]
    if missing_timeline:
        raise AssertionError(f"Chat lazy timeline is missing execution-readiness diagnostics {missing_timeline}, got {metrics}")
    if not metrics.get("nodeExists") or "runtime-error" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the live-readiness node as runtime error, got {metrics}")
    node_text = str(metrics.get("nodeText", ""))
    required_node = ["Execution blocked", "live"]
    missing_node = [needle for needle in required_node if needle not in node_text]
    if missing_node:
        raise AssertionError(f"Workflow Studio node should show live-readiness evidence {missing_node}, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the execution-readiness node, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = [
        "Execution readiness blocked",
        "execution_readiness",
        "Execution ready",
        "No",
        "Execution boundary",
        "production-restconf-change",
        "stage.execution.allow_live_tools",
        "network_tools/device_restconf_live_apply",
    ]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing execution-readiness diagnostics {missing_stage}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if "execution_readiness_blocked" not in output_text or "stage.execution.allow_live_tools" not in output_text:
        raise AssertionError(f"Workflow Studio run log should expose execution-readiness diagnostics, got {metrics}")


def assert_workflow_model_escalation_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["model_escalated_workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !timelinePanel || !timelineButton || !timelineContent) {{
            return {{ missingChatNodes: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          }}
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("model_escalated")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          const openButton = Array.from(detail.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!openButton) return {{ missingStudioAction: true, detailText: detail.textContent, timelineText }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="plan"]');
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const runtimeDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < runtimeDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("model_escalated"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatNodes: false,
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            hasRuntimeButton: Boolean(runtimeButton),
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            runOutputText: output ? output.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatNodes") or metrics.get("missingStudioAction"):
        raise AssertionError(f"model escalation diagnostics missing Chat history path, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["model_escalated_workflow_run_id"]:
        raise AssertionError(f"Open in Studio should focus the model-escalated workflow run, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    required_timeline = ["model_escalated", "stage_retry", "stage.params.escalate_model", "Attempt 2"]
    missing_timeline = [needle for needle in required_timeline if needle not in timeline_text]
    if missing_timeline:
        raise AssertionError(f"Chat lazy timeline is missing model escalation diagnostics {missing_timeline}, got {metrics}")
    if not metrics.get("nodeExists") or "has-runtime" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the model-escalated plan node with runtime diagnostics, got {metrics}")
    node_text = str(metrics.get("nodeText", ""))
    if "model escalated" not in node_text:
        raise AssertionError(f"Workflow Studio node should show model escalation chip, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the model-escalated node, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = ["Model escalation", "model_escalated", "strong-smoke-model", "stage.params.escalate_model"]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing model escalation diagnostics {missing_stage}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if "model_escalated" not in output_text or "stage_retry" not in output_text or "stage.params.escalate_model" not in output_text:
        raise AssertionError(f"Workflow Studio run log should expose model escalation diagnostics, got {metrics}")


def assert_complex_delivery_runtime_visualization_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["complex_delivery_run_id"])};
          const savedTask = JSON.parse(localStorage.getItem("goflow.playground.runState") || "null");
          if (!savedTask || savedTask.mode !== "workflow" || savedTask.runID !== runID) {{
            localStorage.setItem("goflow.playground.runState", JSON.stringify({{
              mode: "workflow",
              runID,
              status: "completed",
              updatedAt: Date.now()
            }}));
            location.reload();
            await new Promise(resolve => setTimeout(resolve, 2200));
          }}
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) runButton.click();
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const openButton = Array.from(detail?.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          const executionDeadline = Date.now() + 8000;
          while (Date.now() < executionDeadline && !document.querySelector("[data-result-execution] .run-execution-summary")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const execution = document.querySelector("[data-result-execution] .run-execution-summary");
          const workbenchResponse = await fetch(`/api/runs/${{encodeURIComponent(runID)}}?view=workbench`);
          const workbenchPayload = workbenchResponse.ok ? await workbenchResponse.json() : {{}};
          const workbenchExecution = workbenchPayload?.execution || {{}};
          const sliceItems = Array.from(execution?.querySelectorAll(".run-execution-slice") || []);
          const blockerItems = Array.from(execution?.querySelectorAll(".run-execution-blockers li") || []);
          const verificationItems = Array.from(execution?.querySelectorAll(".run-execution-checks li") || []);
          const artifactItems = Array.from(execution?.querySelectorAll(".run-execution-artifacts li") || []);
          if (!detail || !openButton) return {{ missingStudioAction: true, detailText: detail ? detail.textContent : "", executionText: execution?.textContent || "" }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const stages = ["plan", "implement", "test", "lint", "audit", "quality", "final-report"];
          const texts = {{}};
          for (const stageName of stages) {{
            const node = document.querySelector(`.flow-node[data-stage-name="${{stageName}}"]`);
            const button = node?.querySelector('[data-node-runtime-open]');
            if (button) button.click();
            const deadline = Date.now() + 8000;
            let panel = document.querySelector("#stageRuntime");
            const expectedTitle = stageName === "plan" ? "Project plan"
            : stageName === "implement" ? "Implementation summary"
              : ["test", "lint", "audit"].includes(stageName) ? "Final validation"
              : stageName === "quality" ? "Remaining work"
              : "Final report";
            while (Date.now() < deadline && (!panel || panel.classList.contains("hidden") || !panel.textContent.includes(expectedTitle))) {{
              await new Promise(resolve => setTimeout(resolve, 120));
              panel = document.querySelector("#stageRuntime");
            }}
            texts[stageName] = panel ? panel.textContent : "";
          }}
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingStudioAction: false,
            executionVisible: Boolean(execution),
            executionText: execution?.textContent || "",
            sliceCount: sliceItems.length,
            blockerCount: blockerItems.length,
            verificationCount: verificationItems.length,
            artifactCount: artifactItems.length,
            workbenchStatus: workbenchResponse.status,
            workbenchSliceCount: Array.isArray(workbenchExecution.change_slices) ? workbenchExecution.change_slices.length : 0,
            workbenchVerificationCount: Array.isArray(workbenchExecution.verification) ? workbenchExecution.verification.length : 0,
            workbenchArtifactCount: Array.isArray(workbenchExecution.artifacts) ? workbenchExecution.artifacts.length : 0,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            texts,
            hasLegacyIteration: !!document.querySelector('.flow-node[data-stage-name="iteration"]'),
            hasLegacyFinalValidation: !!document.querySelector('.flow-node[data-stage-name="final-validation"]')
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingStudioAction"):
        raise AssertionError(f"complex delivery run should open in Workflow Studio, got {metrics}")
    if not metrics.get("executionVisible"):
        raise AssertionError(f"task page should render the bounded execution summary before opening Studio, got {metrics}")
    if int(metrics.get("sliceCount") or 0) != 2 or int(metrics.get("blockerCount") or 0) != 1:
        raise AssertionError(f"task execution summary should expose both change slices and the blocked reason, got {metrics}")
    if int(metrics.get("verificationCount") or 0) != 2 or int(metrics.get("artifactCount") or 0) != 2:
        raise AssertionError(f"task execution summary should expose verification and artifact evidence, got {metrics}")
    execution_text = str(metrics.get("executionText") or "")
    for needle in ("Slice A", "UPGRADE.md", "go test ./... passed after retry", "Source Diff"):
        if needle not in execution_text:
            raise AssertionError(f"task execution summary is missing {needle!r}: {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["complex_delivery_run_id"]:
        raise AssertionError(f"Open in Studio should focus the complex delivery run, got {metrics}")
    texts = metrics.get("texts") or {}
    required = {
        "plan": ["Project plan", "Work slices", "Slice A", "Verification strategy", "go test ./...", "Definition of done"],
        "implement": ["Implementation summary", "Slice A", "Files changed", "README.md", "main.go", "Requirements coverage"],
        "test": ["Final validation", "Test Result", "Commands run", "go test ./...", "Failure Evidence", "HTTP 500"],
        "lint": ["Final validation", "Lint Result", "Commands run", "go vet ./...", "Findings", "none"],
        "audit": ["Final validation", "Audit Decision", "Requirements coverage", "Slice A covered", "Blocking Findings", "none"],
        "quality": ["Remaining work", "Quality status", "pass", "none"],
        "final-report": ["Final report", "Requirements delivered", "README", "main.go", "Verification evidence", "Artifact references"],
    }
    for stage, needles in required.items():
        text = str(texts.get(stage, ""))
        missing = [needle for needle in needles if needle not in text]
        if missing:
            raise AssertionError(f"complex delivery runtime visualization for {stage} is missing {missing}, got {metrics}")
    if metrics.get("hasLegacyIteration") or metrics.get("hasLegacyFinalValidation"):
        raise AssertionError(f"complex delivery workflow should not render legacy iteration stages, got {metrics}")


def assert_workflow_quality_gate_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["quality_workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !timelinePanel || !timelineButton || !timelineContent) {{
            return {{ missingChatNodes: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          }}
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("quality_gate_failed")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          const openButton = Array.from(detail.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!openButton) return {{ missingStudioAction: true, detailText: detail.textContent, timelineText }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="quality"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="quality"]');
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const runtimeDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < runtimeDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("View quality gate"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatNodes: false,
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            hasRuntimeButton: Boolean(runtimeButton),
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            runOutputText: output ? output.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatNodes") or metrics.get("missingStudioAction"):
        raise AssertionError(f"quality gate diagnostics missing Chat history path, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["quality_workflow_run_id"]:
        raise AssertionError(f"Open in Studio should focus the quality gate workflow run, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    required_timeline = ["quality_gate_failed", "acceptance_failed=1", "verification_failed=1", "quality_gate"]
    missing_timeline = [needle for needle in required_timeline if needle not in timeline_text]
    if missing_timeline:
        raise AssertionError(f"Chat lazy timeline is missing quality gate diagnostics {missing_timeline}, got {metrics}")
    if not metrics.get("nodeExists") or "runtime-error" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the quality gate node as runtime error, got {metrics}")
    node_text = str(metrics.get("nodeText", ""))
    if "quality:" not in node_text or "failed" not in node_text:
        raise AssertionError(f"Workflow Studio node should show quality failure evidence, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the quality gate node, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = [
        "Quality gate failure",
        "View quality gate",
        "Quality status",
        "failed",
        "Quality score",
        "61",
        "Quality failures",
        "acceptance_failed=1",
        "verification_failed=1",
        "quality_gate",
    ]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing quality gate diagnostics {missing_stage}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if "Quality gate failure" not in output_text or "quality_gate_failed" not in output_text or "acceptance_failed=1" not in output_text:
        raise AssertionError(f"Workflow Studio run log should expose quality gate failure diagnostics, got {metrics}")


def assert_workflow_parallel_file_conflict_diagnostics_visible(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runID = {json.dumps(smoke_run["parallel_conflict_workflow_run_id"])};
          const runKey = `workflow:${{runID}}`;
          const listDeadline = Date.now() + 8000;
          while (Date.now() < listDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains("active")) {{
            runButton.click();
          }}
          const detailDeadline = Date.now() + 8000;
          while (Date.now() < detailDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const detail = document.querySelector("#runHistoryDetail");
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="${{runID}}"]`);
          const timelinePanel = stack?.querySelector('[data-history-lazy-panel="timeline"]');
          const timelineButton = timelinePanel?.querySelector('[data-history-load="timeline"]');
          const timelineContent = timelinePanel?.querySelector('[data-history-lazy-content="timeline"]');
          if (!detail || !stack || !timelinePanel || !timelineButton || !timelineContent) {{
            return {{ missingChatNodes: true, hasRunButton: Boolean(runButton), detailText: detail ? detail.textContent : "" }};
          }}
          timelineButton.click();
          const timelineDeadline = Date.now() + 8000;
          while (Date.now() < timelineDeadline && !timelineContent.textContent.includes("parallel_file_conflict")) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const timelineText = timelineContent.textContent;
          const openButton = Array.from(detail.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!openButton) return {{ missingStudioAction: true, detailText: detail.textContent, timelineText }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="join"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const node = document.querySelector('.flow-node[data-stage-name="join"]');
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const runtimeButton = node?.querySelector('[data-node-runtime-open]');
          if (runtimeButton) runtimeButton.click();
          const runtimeDeadline = Date.now() + 8000;
          let stageRuntime = document.querySelector("#stageRuntime");
          while (
            Date.now() < runtimeDeadline &&
            (!stageRuntime || stageRuntime.classList.contains("hidden") || !stageRuntime.textContent.includes("View file conflicts"))
          ) {{
            await new Promise(resolve => setTimeout(resolve, 120));
            stageRuntime = document.querySelector("#stageRuntime");
          }}
          const output = document.querySelector("#runOutput");
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingChatNodes: false,
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            timelineText,
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
            hasRuntimeButton: Boolean(runtimeButton),
            stageRuntimeText: stageRuntime ? stageRuntime.textContent : "",
            runOutputText: output ? output.textContent : ""
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingChatNodes") or metrics.get("missingStudioAction"):
        raise AssertionError(f"parallel file conflict diagnostics missing Chat history path, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["parallel_conflict_workflow_run_id"]:
        raise AssertionError(f"Open in Studio should focus the parallel conflict workflow run, got {metrics}")
    timeline_text = str(metrics.get("timelineText", ""))
    required_timeline = [
        "Parallel branches",
        "parallel_file_conflict",
        "internal/shared.go",
        "implement",
        "audit",
        "parallel_file_ownership",
        "parallel_patch_artifact_required",
        "parallel_patch_artifact_handoff",
        "write_file",
    ]
    missing_timeline = [needle for needle in required_timeline if needle not in timeline_text]
    if missing_timeline:
        raise AssertionError(f"Chat lazy timeline is missing parallel file conflict diagnostics {missing_timeline}, got {metrics}")
    if not metrics.get("nodeExists") or "runtime-waiting" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the join node as warning/waiting for the file conflict, got {metrics}")
    node_text = str(metrics.get("nodeText", ""))
    if "file conflict" not in node_text.lower() or "internal/shared.go" not in node_text:
        raise AssertionError(f"Workflow Studio join node should show file conflict evidence, got {metrics}")
    if not metrics.get("hasRuntimeButton"):
        raise AssertionError(f"Workflow Studio should expose runtime details for the join conflict node, got {metrics}")
    stage_text = str(metrics.get("stageRuntimeText", ""))
    required_stage = [
        "Parallel file conflict",
        "View file conflicts",
        "Conflict files",
        "internal/shared.go",
        "Conflict owners",
        "implement",
        "audit",
        "parallel_file_ownership",
        "View handoff warning",
        "parallel_patch_artifact_handoff",
        "write_file",
    ]
    missing_stage = [needle for needle in required_stage if needle not in stage_text]
    if missing_stage:
        raise AssertionError(f"Workflow Studio selected-node runtime is missing file conflict diagnostics {missing_stage}, got {metrics}")
    output_text = str(metrics.get("runOutputText", ""))
    if (
        "File conflict warning" not in output_text
        or "parallel_file_conflict" not in output_text
        or "internal/shared.go" not in output_text
        or "Parallel handoff warning" not in output_text
        or "parallel_patch_artifact_required" not in output_text
        or "write_file" not in output_text
    ):
        raise AssertionError(f"Workflow Studio run log should expose file conflict warning diagnostics, got {metrics}")


def zh_url(base_url: str, path: str) -> str:
    if "#" in path:
        before_hash, fragment = path.split("#", 1)
        separator = "&" if "?" in before_hash else "?"
        return f"{base_url}{before_hash}{separator}lang=zh#{fragment}"
    separator = "&" if "?" in path else "?"
    return f"{base_url}{path}{separator}lang=zh"


def assert_auto_orchestration_preview(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          const waitFor = async (predicate, ms = 7000) => {
            const deadline = Date.now() + ms;
            while (Date.now() < deadline) {
              const value = predicate();
              if (value) return value;
              await new Promise(resolve => setTimeout(resolve, 100));
            }
            return null;
          };
          const send = await waitFor(() => document.querySelector("#send"));
          const prompt = document.querySelector("#prompt");
          const originalFetch = window.fetch;
          window.fetch = async (input, init = {}) => {
            if (String(input).includes("/api/runs/preflight")) {
              return new Response(JSON.stringify({
                valid: false,
                kind: "workflow",
                target: "plan-implement-audit",
                domain: "software",
                strategy: "workflow",
                reason: "根据原始需求和现有资源安排规划、实现、验证与审计。",
                orchestration: [
                  { stage: "clarify", node_type: "agent", agent: "planner", skill: "execution-plan", purpose: "澄清需求。", expected_output: "任务契约" },
                  { stage: "implement", node_type: "agent", agent: "fixer", skill: "code-writing", purpose: "修改工作区。", expected_output: "文件 Diff" },
                  { stage: "verify", node_type: "skill", agent: "quality-engineer", skill: "code-audit", purpose: "浏览器验证。", expected_output: "验证证据" },
                  { stage: "audit", node_type: "skill", agent: "auditor", skill: "code-audit", purpose: "独立审计。", expected_output: "审计结论" }
                ],
                issues: [{ severity: "error", message: "browser smoke stop" }]
              }), { status: 200, headers: { "Content-Type": "application/json" } });
            }
            return originalFetch(input, init);
          };
          try {
            prompt.value = "再帮我美化美化这个项目";
            prompt.dispatchEvent(new Event("input", { bubbles: true }));
            send.click();
            const plan = await waitFor(() => document.querySelector(".task-orchestration-plan"));
            return {
              exists: Boolean(plan),
              open: Boolean(plan?.open),
              rows: plan?.querySelectorAll("li").length || 0,
              workflow: plan?.dataset.orchestrationWorkflow || "",
              agentCount: Number(plan?.dataset.orchestrationAgentCount || 0),
              agents: Array.from(plan?.querySelectorAll("li") || []).map(row => row.dataset.orchestrationAgent || ""),
              stages: Array.from(plan?.querySelectorAll("li") || []).map(row => row.dataset.orchestrationStage || ""),
              text: plan?.textContent || "",
              context: document.querySelector("#taskExecutionContext")?.textContent || ""
            };
          } finally {
            window.fetch = originalFetch;
          }
        })()
        """,
        timeout,
        profile_dir,
    )
    text = str(metrics.get("text", ""))
    context = str(metrics.get("context", ""))
    required = ["Diff"]
    missing = [needle for needle in required if needle not in text]
    if not metrics.get("exists") or not metrics.get("open") or metrics.get("rows") != 4 or len(text.strip()) < 40 or missing:
        raise AssertionError(f"automatic orchestration preview is incomplete; missing={missing}, got {metrics}")
    if metrics.get("workflow") != "plan-implement-audit" or metrics.get("agentCount") != 4:
        raise AssertionError(f"automatic orchestration should retain stable Workflow and Agent identities, got {metrics}")
    if metrics.get("agents") != ["planner", "fixer", "quality-engineer", "auditor"] or metrics.get("stages") != ["clarify", "implement", "verify", "audit"]:
        raise AssertionError(f"automatic orchestration stage assignment is incorrect, got {metrics}")


def assert_navigation_shell(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        """
        (async () => {
          const primary = document.querySelector("[data-primary-nav]");
          const utility = document.querySelector("[data-utility-nav]");
          const primaryButtons = Array.from(primary?.querySelectorAll("button[data-view]") || []);
          const utilityButtons = Array.from(utility?.querySelectorAll("button[data-view]") || []);
          const executionContext = document.querySelector("#taskExecutionContext");
          const iconConsistency = {};
          const controls = document.querySelector(".run-control-bar");
          const composer = document.querySelector(".composer-shell");
          const controlsBeforeComposer = Boolean(controls && composer && (controls.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING));
          for (const view of ["playground", "workflows", "catalog", "usage", "settings"]) {
            if (location.hash !== `#${view}`) {
              location.hash = view;
              await new Promise(resolve => setTimeout(resolve, 180));
            }
            const currentNav = document.querySelector(`[data-primary-nav] button[data-view="${view}"]`);
            const currentTitleIcon = document.querySelector("#sectionTitle .section-title-icon svg");
            iconConsistency[view] = {
              matches: Boolean(currentNav && currentTitleIcon && currentNav.querySelector(".nav-icon svg")?.innerHTML === currentTitleIcon.innerHTML),
              navSize: currentNav?.querySelector(".nav-icon svg")?.getAttribute("width") || "",
              titleSize: currentTitleIcon?.getAttribute("width") || ""
            };
          }
          return {
            primaryCount: primaryButtons.length,
            primaryViews: primaryButtons.map(button => button.dataset.view),
            utilityExists: Boolean(utility),
            utilityViews: utilityButtons.map(button => button.dataset.view),
            brandAsset: document.querySelector("img.brand-mark")?.getAttribute("src") || "",
            executionContextChips: executionContext ? executionContext.querySelectorAll(".task-context-chip").length : 0,
            executionContextLabels: Array.from(executionContext?.querySelectorAll(".task-context-chip small") || []).map(item => item.textContent.trim()),
            controlsBeforeComposer,
            iconConsistency
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    expected_primary = ["playground", "workflows", "catalog", "usage", "settings"]
    if metrics.get("primaryCount") != 5 or metrics.get("primaryViews") != expected_primary:
        raise AssertionError(f"primary navigation should expose exactly five destinations, got {metrics}")
    if metrics.get("utilityExists") or metrics.get("utilityViews"):
        raise AssertionError(f"retired workspace, memory, and status navigation must stay removed, got {metrics}")
    if metrics.get("brandAsset") != "/assets/brand-mark.svg":
        raise AssertionError(f"Studio shell should load the shared brand mark, got {metrics}")
    if metrics.get("executionContextChips", 0) < 5:
        raise AssertionError(f"Tasks should expose actual execution context before a run, got {metrics}")
    if "Target" in metrics.get("executionContextLabels", []):
        raise AssertionError(f"ordinary workspace tasks should not expose a Target control, got {metrics}")
    if not metrics.get("controlsBeforeComposer"):
        raise AssertionError(f"reasoning and permission controls should appear before the task composer, got {metrics}")
    icon_consistency = metrics.get("iconConsistency") or {}
    if any(not (icon_consistency.get(view) or {}).get("matches") for view in expected_primary):
        raise AssertionError(f"top-level view title icons should match their navigation icons, got {metrics}")
    if any((icon_consistency.get(view) or {}).get("navSize") != "16" or (icon_consistency.get(view) or {}).get("titleSize") != "16" for view in expected_primary):
        raise AssertionError(f"top-level view icons should use the shared 16px size, got {metrics}")

    schedules = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        """
        (async () => {
          const panel = document.querySelector('#schedulePanel');
          const scheduleButton = document.querySelector('#scheduleTask');
          const form = document.querySelector('#scheduleForm');
          return {
            removed: !panel && !scheduleButton && !form && !document.querySelector('#scheduleTrigger'),
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if (
        not schedules.get("removed")
        or schedules.get("overflowX")
    ):
        raise AssertionError(f"retired scheduled-task controls must not remain in the ordinary task DOM, got {schedules}")

    task_drawer = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        """
        (async () => {
          const drawer = document.querySelector('#taskHistoryDrawer');
          const toggle = document.querySelector('#taskDrawerToggle');
          const close = document.querySelector('#taskDrawerClose');
          const backdrop = document.querySelector('#taskDrawerBackdrop');
          if (drawer?.classList.contains('open')) {
            close?.click();
            await new Promise(resolve => setTimeout(resolve, 200));
          }
          const initial = {
            open: drawer?.classList.contains('open') || false,
            hidden: drawer?.getAttribute('aria-hidden') === 'true',
            inert: drawer?.hasAttribute('inert') || false
          };
          toggle?.click();
          await new Promise(resolve => setTimeout(resolve, 240));
          const opened = {
            open: drawer?.classList.contains('open') || false,
            hidden: drawer?.getAttribute('aria-hidden') === 'true',
            inert: drawer?.hasAttribute('inert') || false,
            expanded: toggle?.getAttribute('aria-expanded') === 'true',
            backdropVisible: Boolean(backdrop && getComputedStyle(backdrop).display !== 'none')
          };
          close?.click();
          await new Promise(resolve => setTimeout(resolve, 200));
          return {
            initial,
            opened,
            closed: {
              open: drawer?.classList.contains('open') || false,
              hidden: drawer?.getAttribute('aria-hidden') === 'true',
              inert: drawer?.hasAttribute('inert') || false,
              expanded: toggle?.getAttribute('aria-expanded') === 'true',
              backdropHidden: Boolean(backdrop && getComputedStyle(backdrop).display === 'none')
            },
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if (
        task_drawer.get("initial") != {"open": False, "hidden": True, "inert": True}
        or task_drawer.get("opened") != {
            "open": True,
            "hidden": False,
            "inert": False,
            "expanded": True,
            "backdropVisible": True,
        }
        or task_drawer.get("closed") != {
            "open": False,
            "hidden": True,
            "inert": True,
            "expanded": False,
            "backdropHidden": True,
        }
        or task_drawer.get("overflowX")
    ):
        raise AssertionError(f"task history drawer should open on demand and close cleanly, got {task_drawer}")

    retired_overview = evaluate_browser_json(
        browser,
        f"{base_url}/console#overview",
        """
        (async () => {
          const deadline = Date.now() + 5000;
          while (Date.now() < deadline && !document.querySelector("#app")?.dataset.view) {
            await new Promise((resolve) => setTimeout(resolve, 50));
          }
          return {
            renderedView: document.querySelector("#app")?.dataset.view || "",
            activeView: document.querySelector(".nav button.active[data-view]")?.dataset.view || "",
            title: document.querySelector("#sectionTitle")?.textContent?.trim() || "",
            hasTaskConsole: Boolean(document.querySelector(".chat-grid"))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if (
        retired_overview.get("renderedView") != "playground"
        or retired_overview.get("activeView") != "playground"
        or not retired_overview.get("hasTaskConsole")
    ):
        raise AssertionError(f"retired Overview links should fall back to Tasks, got {retired_overview}")

    mobile = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        """
        (async () => {
          document.querySelector("#sidebarToggle")?.click();
          await new Promise(resolve => setTimeout(resolve, 100));
          const primaryButtons = Array.from(document.querySelectorAll("[data-primary-nav] button[data-view]"));
          const schedules = document.querySelector("#schedulePanel");
          const scheduleForm = document.querySelector("#scheduleForm");
          const scheduleRect = scheduleForm?.getBoundingClientRect();
          const insideViewport = element => {
            if (!element) return false;
            const rect = element.getBoundingClientRect();
            return rect.width > 0 && rect.height > 0 && rect.left >= 0 && rect.right <= innerWidth;
          };
          return {
            viewport: [innerWidth, innerHeight],
            overflowX: document.documentElement.scrollWidth > innerWidth,
            primaryVisible: primaryButtons.length === 5 && primaryButtons.every(insideViewport),
            utilityRemoved: !document.querySelector("[data-utility-nav]"),
            menuOpen: document.querySelector("[data-app-shell]")?.classList.contains("mobile-nav-open"),
            scheduleFormVisible: Boolean(scheduleForm && getComputedStyle(scheduleForm).display !== "none"),
            scheduleFormInsideViewport: Boolean(scheduleRect && scheduleRect.left >= 0 && scheduleRect.right <= innerWidth),
            labels: primaryButtons.map(button => button.querySelector(".nav-label")?.textContent?.trim() || "")
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(390, 844),
    )
    viewport = mobile.get("viewport") or [0, 0]
    if not (320 <= int(viewport[0]) <= 540 and int(viewport[1]) >= 640) or mobile.get("overflowX"):
        raise AssertionError(f"mobile navigation should fit the viewport without horizontal overflow, got {mobile}")
    if not mobile.get("menuOpen") or not mobile.get("primaryVisible") or not mobile.get("utilityRemoved"):
        raise AssertionError(f"mobile navigation should expose the five primary destinations, got {mobile}")
    if mobile.get("labels") != ["运行", "工作流", "资源", "用量", "设置"]:
        raise AssertionError(f"mobile navigation should default to complete Chinese labels, got {mobile}")
    if mobile.get("scheduleFormVisible"):
        raise AssertionError(f"mobile ordinary task flow should not expose scheduled-task configuration, got {mobile}")

    desktop_sidebar = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        """
        (async () => {
          const shell = document.querySelector('[data-app-shell]');
          const toggle = document.querySelector('#sidebarToggle');
          const buttons = Array.from(document.querySelectorAll('[data-primary-nav] button[data-view]'));
          const width = () => Math.round(document.querySelector('.sidebar')?.getBoundingClientRect().width || 0);
          const initial = { className: shell?.className || '', width: width(), expanded: toggle?.getAttribute('aria-expanded') || '' };
          toggle?.click();
          await new Promise(resolve => setTimeout(resolve, 120));
          const collapsed = {
            className: shell?.className || '',
            width: width(),
            expanded: toggle?.getAttribute('aria-expanded') || '',
            titles: buttons.map(button => button.title || ''),
            visibleIcons: buttons.every(button => getComputedStyle(button.querySelector('.nav-icon')).display !== 'none')
          };
          toggle?.click();
          await new Promise(resolve => setTimeout(resolve, 120));
          const restored = { className: shell?.className || '', width: width(), expanded: toggle?.getAttribute('aria-expanded') || '' };
          return { initial, collapsed, restored, overflowX: document.documentElement.scrollWidth > innerWidth };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
    )
    collapsed = desktop_sidebar.get("collapsed") or {}
    if (
        (desktop_sidebar.get("initial") or {}).get("width") != 248
        or collapsed.get("width") != 68
        or "sidebar-collapsed" not in str(collapsed.get("className") or "")
        or collapsed.get("expanded") != "false"
        or not all(str(title).strip() for title in collapsed.get("titles") or [])
        or not collapsed.get("visibleIcons")
        or (desktop_sidebar.get("restored") or {}).get("width") != 248
        or desktop_sidebar.get("overflowX")
    ):
        raise AssertionError(f"desktop sidebar collapse should preserve icon access and layout, got {desktop_sidebar}")

    mcp_filter = evaluate_browser_json(
        browser,
        f"{base_url}/console#catalog",
        """
        (async () => {
          const button = document.querySelector('[data-resource-kind="mcp"]');
          button?.click();
          await new Promise(resolve => setTimeout(resolve, 100));
          return {
            active: button?.classList.contains('active') || false,
            label: button?.textContent?.trim() || '',
            rows: document.querySelectorAll('.resource-row').length,
            heading: document.querySelector('#resourceKindTitle')?.textContent?.trim() || '',
            listPresent: Boolean(document.querySelector('#resourceList')),
            overflowX: document.documentElement.scrollWidth > innerWidth
          };
        })()
        """,
        timeout,
        profile_dir,
        viewport=(1440, 1000),
    )
    if not mcp_filter.get("active") or not mcp_filter.get("label") or not mcp_filter.get("heading") or not mcp_filter.get("listPresent") or mcp_filter.get("overflowX"):
        raise AssertionError(f"MCP tool connection sidebar filter should expose usable rows, got {mcp_filter}")


def main() -> int:
    args = parse_args()
    if args.performance_only:
        args.enforce_performance = True
        args.baseline_only = True
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("browser smoke skipped: no Chrome/Edge/Chromium binary found")
        return 0

    port = args.port or free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = os.environ.copy()
    env.update(
        {
            "GOFLOW_BASE_URL": env.get("GOFLOW_BASE_URL", "http://127.0.0.1:9/v1"),
            "GOFLOW_API_KEY": env.get("GOFLOW_API_KEY", "smoke-key"),
            "GOFLOW_MODEL": env.get("GOFLOW_MODEL", "smoke-model"),
            "GOFLOW_BACKUP_BASE_URL": env.get("GOFLOW_BACKUP_BASE_URL", "http://127.0.0.1:9/v1"),
            "GOFLOW_BACKUP_API_KEY": env.get("GOFLOW_BACKUP_API_KEY", "smoke-key"),
            "GOFLOW_BACKUP_MODEL": env.get("GOFLOW_BACKUP_MODEL", "smoke-backup-model"),
        }
    )
    smoke_tmp_root = ROOT / ".smoke-tmp"
    smoke_tmp_root.mkdir(exist_ok=True)
    tmpdir = Path(tempfile.mkdtemp(prefix="goflow-browser-smoke-", dir=smoke_tmp_root))
    proc: subprocess.Popen[str] | None = None
    try:
        runtime_home = tmpdir / "runtime"
        cache_dir = tmpdir / "gocache"
        workspace = tmpdir / "workspace"
        profile_dir = Path(tempfile.mkdtemp(prefix="goflow-browser-profile-"))
        runtime_home.mkdir()
        cache_dir.mkdir()
        workspace.mkdir()
        smoke_artifact = write_smoke_artifact(workspace)
        write_smoke_memory(workspace)
        smoke_run = None
        if not args.performance_only:
            smoke_run = write_smoke_session(workspace)
            write_smoke_task_staging(runtime_home, workspace, smoke_run["run_id"])
        write_smoke_workflow_graph(runtime_home)
        config_path = write_smoke_runtime_config(runtime_home)
        env.setdefault("GOCACHE", str(cache_dir))
        binary = args.binary
        if not args.skip_build:
            binary = build_binary(tmpdir, env)
        if binary is None:
            raise AssertionError("--skip-build requires --binary")
        if args.enforce_performance:
            run_checked([str(binary), "--version"], env)
        startup_started = time.perf_counter()
        proc = subprocess.Popen(
            [
                str(binary),
                "--config",
                str(config_path),
                "--workspace",
                str(workspace),
                "--http",
                f":{port}",
            ],
            cwd=ROOT,
            env=env,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        wait_for_server(base_url, proc, args.timeout)
        startup_ms = round((time.perf_counter() - startup_started) * 1000)
        if args.baseline_only or args.baseline_output is not None or args.enforce_performance:
            baseline = capture_v3_baseline(
                browser,
                base_url,
                args.timeout,
                profile_dir,
                startup_ms,
                args.baseline_output,
                args.enforce_performance,
            )
            print(f"V3 browser baseline: {json.dumps(baseline, ensure_ascii=False, sort_keys=True)}")
            if args.baseline_only:
                return 0
        if args.staged_delivery_only:
            assert_staged_delivery_action_layout(
                browser,
                base_url,
                args.timeout,
                profile_dir,
                smoke_run,
                args.screenshot_dir,
            )
            print(f"staged delivery browser smoke passed at {base_url} with {browser}")
            return 0
        create_smoke_execution_target(base_url, "smoke-ssh", enabled=True)
        create_smoke_execution_target(base_url, "disabled-ssh", enabled=False)
        if args.target_only:
            assert_execution_target_selection_flow(browser, base_url, args.timeout, profile_dir)
            if args.screenshot_dir is not None:
                capture_execution_target_screenshots(browser, base_url, args.timeout, profile_dir, args.screenshot_dir)
            print(f"execution Target browser smoke passed at {base_url} with {browser}")
            return 0
        assert_execution_target_selection_flow(browser, base_url, args.timeout, profile_dir)
        assert_asset_contains(
            base_url,
            "/assets/views/chat.js",
            [
                "runHistoryLazyPanelsHTML",
                "data-history-lazy-stack",
                "fetchRun(run, options.summary ? { summary: true } : {})",
                "fetchRunTimeline(run, { limit: 120 })",
                "include_content: true, limit: 12",
                "data-run-context-action",
                "taskExecutionContext",
                "requires_execution_target",
                "target_id: targetID",
                "executionTargetSelect",
                "target_id: requestedTargetID",
                "savedRunIsAvailable",
                "Number(error?.status) === 404",
                "trimTimelineWindow",
                "taskOrchestrationPlanHTML",
            ],
        )
        assert_asset_contains(
            base_url,
            "/assets/app.js",
            ["loadViewRenderer", 'module: "./views/workflows.js"', "Promise.all([refreshRuntime(), loadViewRenderer(view)])"],
        )
        assert_asset_contains(
            base_url,
            "/assets/i18n.js",
            ["chat.runHistoryLazyTitle", "chat.runHistoryLazySummaryFirst", "chat.runHistoryLazyLoad", "chat.runContextFilesTitle", "chat.executionContextTarget", "chat.orchestrationTitle", "settings.simpleAppearanceTitle"],
        )
        assert_asset_contains(
            base_url,
            "/assets/styles.css",
            ["run-history-lazy-stack", "run-history-lazy-panel", "run-history-lazy-timeline"],
        )
        assert_asset_contains(
            base_url,
            "/assets/styles-workbench.css",
            [
                "Final workbench theme layer",
                "--sidebar-highlight: #ececee",
                ".shell .run-console-grid",
                ".task-execution-context",
                ".task-orchestration-plan",
                ".task-execution-target",
                ".settings-simple",
                "padding-right: 16px",
                "margin-right: 8px",
            ],
        )
        assert_asset_contains(base_url, "/assets/brand-mark.svg", ['aria-label="GoFlow"', 'viewBox="0 0 32 32"'])
        assert_asset_contains(base_url, "/favicon.ico", ['aria-label="GoFlow"', 'viewBox="0 0 32 32"'])
        try:
            console_dom = render_dom(browser, zh_url(base_url, "/console"), args.timeout, profile_dir)
            workflows_dom = render_dom(browser, zh_url(base_url, "/workflows"), args.timeout, profile_dir)
            playground_dom = render_dom(browser, zh_url(base_url, "/console#playground"), args.timeout, profile_dir)
            catalog_dom = render_dom(browser, zh_url(base_url, "/console#catalog"), args.timeout, profile_dir)
            settings_dom = render_dom(browser, zh_url(base_url, "/console#settings"), args.timeout, profile_dir)
            playground_zh_dom = render_dom(browser, zh_url(base_url, "/console#playground"), args.timeout, profile_dir)
            settings_zh_dom = render_dom(browser, zh_url(base_url, "/console#settings"), args.timeout, profile_dir)
            settings_en_dom = render_dom(browser, f"{base_url}/console?lang=en#settings", args.timeout, profile_dir)
        except BrowserSmokeUnavailable as exc:
            if args.required:
                raise AssertionError(str(exc)) from exc
            print(f"browser smoke skipped: browser could not run in this host session: {exc}")
            return 0
        assert_rendered(
            "/console",
            console_dom,
            ["chat-grid", "你想让 GoFlow 做什么？", "任务输入"],
        )
        assert_rendered(
            "/workflows",
            workflows_dom,
            ["workflow-palette-panel", "workflow-inspector-panel", "workflow-context-contract", "工作流"],
        )
        assert_rendered(
            "/console#playground",
            playground_dom,
            [
                "chat-grid",
                "run-brief",
                "run-timeline",
                "你想让 GoFlow 做什么？",
                "任务输入",
                "task-execution-context",
            ],
        )
        assert_navigation_shell(browser, base_url, args.timeout, profile_dir)
        assert_auto_orchestration_preview(browser, base_url, args.timeout, profile_dir)
        assert_playground_timeline_layout(browser, base_url, args.timeout, profile_dir)
        assert_chinese_runtime_step_copy(browser, base_url, args.timeout, profile_dir)
        assert_run_token_accounting(browser, base_url, args.timeout, profile_dir)
        assert_attachment_input_flow(browser, base_url, args.timeout, profile_dir)
        assert_playground_context_prompts(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console#catalog",
            catalog_dom,
            ["resource-library", "resource-library-layout", "resource-kind-nav", "resource-browser", "子智能体"],
        )
        assert_catalog_brief_creation_path(browser, base_url, args.timeout, profile_dir)
        assert_catalog_tab_layout_stability(browser, base_url, args.timeout, profile_dir)
        assert_catalog_tool_workflow_handoff(browser, base_url, args.timeout, profile_dir, args.screenshot_dir)
        assert_route_level_view_loading(browser, base_url, args.timeout, profile_dir)
        assert_primary_page_accessibility(browser, base_url, args.timeout, profile_dir)
        assert_workflow_metadata_zh(browser, base_url, args.timeout, profile_dir)
        assert_simple_workflow_build_path(browser, base_url, args.timeout, profile_dir)
        assert_workflow_drawers_open_on_demand(browser, base_url, args.timeout, profile_dir)
        assert_workflow_focus_mode_keeps_canvas_visible(browser, base_url, args.timeout, profile_dir)
        assert_workflow_developer_fields_folded(browser, base_url, args.timeout, profile_dir)
        assert_run_history_lazy_loading(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_restart_reconciliation_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_run_history_artifacts_lazy_loading(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_run_history_office_artifact_actions(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_budget_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_studio_budget_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_budget_approval_action_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_blocked_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_execution_readiness_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_model_escalation_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_complex_delivery_runtime_visualization_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_quality_gate_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_workflow_parallel_file_conflict_diagnostics_visible(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_rendered(
            "/console#settings",
            settings_dom,
            [
                "settings-simple",
                "settings-model-section",
                "settings-workspace-section",
                "settings-appearance-section",
                "模型",
                "工作区",
                "工作区版本",
                "外观",
            ],
        )
        assert_settings_warning_resolution_path(browser, base_url, args.timeout, profile_dir)
        assert_settings_workspace_entry_flow(browser, base_url, args.timeout, profile_dir)
        assert_provider_capability_probe_layout(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console?lang=zh#playground",
            playground_zh_dom,
            [
                "chat-grid",
                "run-brief",
                "run-timeline",
                "你想让 GoFlow 做什么？",
                "任务输入",
            ],
        )
        assert_rendered(
            "/console?lang=zh#settings",
            settings_zh_dom,
            [
                "settings-simple",
                "settings-model-section",
                "settings-workspace-section",
                "settings-appearance-section",
                "模型",
                "工作区",
                "工作区版本",
                "外观",
            ],
        )
        assert_rendered(
            "/console?lang=en#settings",
            settings_en_dom,
            [
                "settings-simple",
                "Model",
                "Workspace",
                "Workspace versions",
                "Appearance",
            ],
        )
        assert_staged_delivery_action_layout(
            browser,
            base_url,
            args.timeout,
            profile_dir,
            smoke_run,
            args.screenshot_dir,
        )
        assert_usage_dashboard(
            browser,
            base_url,
            args.timeout,
            profile_dir,
            args.screenshot_dir,
        )
        assert_task_controls_and_result_styles(
            browser,
            base_url,
            args.timeout,
            profile_dir,
            args.screenshot_dir,
        )
        assert_task_timeline_filters_stream_fragments(browser, base_url, args.timeout, profile_dir)
        assert_human_readable_run_outputs(browser, base_url, args.timeout, profile_dir)
        assert_history_usage_replay(browser, base_url, args.timeout, profile_dir)
        if args.screenshot_dir is not None:
            capture_primary_page_screenshots(
                browser,
                base_url,
                args.timeout,
                profile_dir,
                args.screenshot_dir,
            )
        print(f"browser smoke passed at {base_url} with {browser}")
        return 0
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
        shutil.rmtree(profile_dir, ignore_errors=True)
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
