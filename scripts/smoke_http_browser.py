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
import tempfile
import textwrap
import time
import urllib.error
import urllib.parse
import urllib.request
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
            description: Minimal skill used by the browser smoke test runtime.
            ---

            Use this placeholder skill only for local smoke tests.
            """
        ).lstrip(),
        encoding="utf-8",
    )
    config_text = textwrap.dedent(
        f"""
        agent:
          name: GoFlow Browser Smoke
          max_iterations: 4
          timeout: 30s

        default_agent: chat

        providers:
          primary:
            provider: openai-compatible
            base_url: ${{GOFLOW_BASE_URL}}
            api_key: ${{GOFLOW_API_KEY}}
            model: ${{GOFLOW_MODEL}}
            fallback_provider: backup
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
            name: Chat
            description: Browser-smoke chat agent.
            provider: primary
            mode: chat
            tool_policy: confirm
            allowed_tool_kinds: [read, network]
            max_iterations: 4
          auditor:
            name: Auditor
            description: Browser-smoke verifier agent.
            provider: primary
            mode: audit
            tool_policy: confirm
            allowed_tool_kinds: [read]
            max_iterations: 2

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
            description: Browser smoke graph used to validate node-level budget recovery actions.
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
            description: Browser smoke graph used to validate contract failure and model escalation diagnostics.
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
            description: Browser smoke graph used to validate complex delivery stage runtime visualization.
            stages:
              - name: plan
                node_type: skill
                agent: planner
                skill: execution-plan
                outputs:
                  project_plan: result.output
                next:
                  - iteration
                position: {x: 100, y: 240}
              - name: iteration
                node_type: agent
                agent: fixer
                skill: code-writing
                outputs:
                  plan_update: result.plan_update
                  remaining_work: result.blockers
                next:
                  - final-validation
                position: {x: 560, y: 240}
              - name: final-validation
                node_type: skill
                agent: auditor
                skill: code-audit
                outputs:
                  final_validation: result.output
                next:
                  - final-report
                position: {x: 1020, y: 240}
              - name: final-report
                node_type: skill
                agent: planner
                skill: execution-plan
                outputs:
                  final_report: result.output
                position: {x: 1480, y: 240}
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
            description: Browser smoke graph used to validate parallel file conflict diagnostics.
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
            description: Browser smoke graph used to validate live execution readiness diagnostics.
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
    artifact_ref = "goflow://session-artifacts/artifact-agent-smoke"
    artifact_content = "# Smoke artifact\n\nFull artifact body for run history viewer."
    workflow_artifact_ref = f"goflow://workflow-runs/{workflow_run_id}/artifacts/plan-report"
    workflow_artifact_content = "Budget warning artifact body for selected-node artifact inspection."
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
                "events_count": 2,
                "artifacts_count": 1,
                "diffs_count": 0,
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
                    }
                ],
            }
        ],
        "workflow": {},
        "workflow_runs": [
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
                "artifacts_count": 1,
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
                "summary": "Complex delivery completed with a project plan, one iteration, final validation, and final report.",
                "started_at": now,
                "updated_at": now,
                "completed_at": now,
                "attempt": 1,
                "completed_stages": [
                    {
                        "stage": "plan",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "execution-plan",
                        "agent_id": "planner",
                        "summary": "Plan created for Slice A.",
                        "outputs": {
                            "project_plan": "Project Plan: deliver Slice A\nWork Slices: Slice A updates README.md\nVerification Strategy: go test ./...\nDefinition of Done: README updated and tests pass"
                        },
                        "result": {
                            "output": "Project Plan: deliver Slice A\nWork Slices: Slice A updates README.md\nVerification Strategy: go test ./...\nDefinition of Done: README updated and tests pass"
                        }
                    },
                    {
                        "stage": "iteration",
                        "status": "completed",
                        "node_type": "agent",
                        "skill": "code-writing",
                        "agent_id": "fixer",
                        "summary": "Slice A implemented and marked complete.",
                        "outputs": {
                            "plan_update": "Slice A complete",
                            "remaining_work": "none",
                            "summary": "PROJECT_COMPLETE"
                        },
                        "result": {
                            "output": "Iteration Number: 1\nSelected Work Slice: Slice A\nImplementation Summary: README.md updated\nVerification Performed: go test ./... passed\nPlan Update: Slice A complete\nRemaining Work: none\nCompletion Decision: PROJECT_COMPLETE\nNext Action: final validation"
                        }
                    },
                    {
                        "stage": "final-validation",
                        "status": "completed",
                        "node_type": "skill",
                        "skill": "code-audit",
                        "agent_id": "auditor",
                        "summary": "Final validation passed.",
                        "outputs": {
                            "final_validation": "Final Validation Result: pass\nCommands Run: go test ./...\nRequirements Coverage: Slice A covered\nFailures: none\nResidual Risks: none"
                        },
                        "result": {
                            "output": "Final Validation Result: pass\nCommands Run: go test ./...\nRequirements Coverage: Slice A covered\nFailures: none\nResidual Risks: none"
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
                            "final_report": "Completion Summary: Slice A delivered\nRequirements Delivered: README update\nFiles Changed: README.md\nVerification Evidence: go test ./... passed\nResidual Risks: none\nArtifact References: project-plan, final-validation-report"
                        },
                        "result": {
                            "output": "Completion Summary: Slice A delivered\nRequirements Delivered: README update\nFiles Changed: README.md\nVerification Evidence: go test ./... passed\nResidual Risks: none\nArtifact References: project-plan, final-validation-report"
                        }
                    }
                ],
                "events_count": 4,
                "events": [
                    {"seq": 1, "at": now, "type": "task_stage", "stage": "plan", "content": "Project plan created for Slice A."},
                    {"seq": 2, "at": now, "type": "task_stage", "stage": "iteration", "content": "Iteration completed with PROJECT_COMPLETE."},
                    {"seq": 3, "at": now, "type": "task_stage", "stage": "final-validation", "content": "Final validation passed."},
                    {"seq": 4, "at": now, "type": "workflow_result", "workflow_name": "complex-project-delivery", "workflow_status": "completed", "content": "Complex delivery completed."}
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
        "marker": SMOKE_LAZY_TIMELINE_MARKER,
        "artifact_ref": artifact_ref,
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
        time.sleep(0.25)
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


def evaluate_browser_json(browser: Path, url: str, script: str, timeout: float, profile_dir: Path) -> dict[str, object]:
    debug_port = free_port()
    debug_profile = profile_dir / f"debug-{secrets.token_hex(4)}"
    debug_profile.mkdir()
    crash_dir = debug_profile / "crash-dumps"
    crash_dir.mkdir(parents=True, exist_ok=True)
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
        "--window-size=1440,1100",
        url,
    ]
    proc = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    try:
        page = wait_for_debug_page(debug_port, url, proc, timeout)
        expression = textwrap.dedent(
            f"""
            (async () => {{
              await new Promise(resolve => setTimeout(resolve, 900));
              const result = {script};
              return result;
            }})()
            """
        )
        parsed_debug_url = urllib.parse.urlparse(str(page["webSocketDebuggerUrl"]))
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
            if isinstance(error, dict) and "Execution context was destroyed" in str(error.get("message", "")):
                time.sleep(0.35 * attempt)
                continue
            break
        if response.get("exceptionDetails"):
            raise AssertionError(f"browser eval exception for {url}: {response['exceptionDetails']}")
        result = response.get("result", {}).get("result", {}).get("value")
        if not isinstance(result, dict):
            raise AssertionError(f"browser eval returned non-object for {url}: {response}")
        return result
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)


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
        f"{base_url}/console#playground",
        """
        (() => {
          const messages = document.querySelector("#messages");
          const timeline = document.querySelector(".run-timeline");
          const setup = document.querySelector(".run-brief");
          const output = document.querySelector(".run-output-stack");
          if (!messages || !timeline || !setup || !output) return { missing: true };
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
          return {
            missing: false,
            overflowY: messagesStyle.overflowY,
            setupOverflowY: setupStyle.overflowY,
            messagesHeight: Math.round(messages.getBoundingClientRect().height),
            setupHeight: Math.round(setup.getBoundingClientRect().height),
            timelineHeight: Math.round(timeline.getBoundingClientRect().height),
            messagesScrollHeight: Math.round(messages.scrollHeight),
            messagesClientHeight: Math.round(messages.clientHeight),
            messagesMaxHeight: messagesStyle.maxHeight,
            timelineMaxHeight: timelineStyle.maxHeight,
            outputTop: Math.round(output.getBoundingClientRect().top),
            timelineBottom: Math.round(timeline.getBoundingClientRect().bottom)
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("playground timeline layout missing expected nodes")
    if metrics.get("overflowY") not in {"auto", "scroll"}:
        raise AssertionError(f"playground timeline should scroll internally, got {metrics}")
    if metrics.get("setupOverflowY") in {"auto", "scroll"}:
        raise AssertionError(f"playground setup panel should show fully without internal scroll, got {metrics}")
    height = int(metrics.get("messagesHeight") or 0)
    if height < 240:
        raise AssertionError(f"playground timeline event list should have usable height, got {metrics}")
    if int(metrics.get("messagesScrollHeight") or 0) <= int(metrics.get("messagesClientHeight") or 0):
        raise AssertionError(f"playground timeline should keep overflowing events inside a scroll area, got {metrics}")
    setup_height = int(metrics.get("setupHeight") or 0)
    timeline_height = int(metrics.get("timelineHeight") or 0)
    if abs(setup_height - timeline_height) > 24:
        raise AssertionError(f"playground setup and timeline panels should have matching heights, got {metrics}")
    if int(metrics.get("outputTop") or 0) <= int(metrics.get("timelineBottom") or 0):
        raise AssertionError(f"playground output should remain below the bounded timeline, got {metrics}")


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


def assert_workspace_risk_surface(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#workspace",
        """
        (() => {
          const cards = Array.from(document.querySelectorAll(".workspace-risk-card"));
          return {
            hasCallout: Boolean(document.querySelector(".workspace-risk-callout")),
            cardCount: cards.length,
            titles: cards.map(card => card.querySelector("strong")?.textContent.trim() || ""),
            body: document.body.textContent
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    titles = " ".join(metrics.get("titles") or [])
    if not metrics.get("hasCallout") or int(metrics.get("cardCount") or 0) < 1:
        raise AssertionError(f"workspace should surface current action risk cards when approvals are pending, got {metrics}")
    if "Approval" not in titles and "Workspace" not in titles:
        raise AssertionError(f"workspace risk cards should explain approval/workspace impact, got {metrics}")

    zh = evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#workspace",
        """
        (() => ({
          hasCallout: Boolean(document.querySelector(".workspace-risk-callout")),
          cardCount: document.querySelectorAll(".workspace-risk-card").length,
          body: document.body.textContent
        }))()
        """,
        timeout,
        profile_dir,
    )
    body = str(zh.get("body", ""))
    if not zh.get("hasCallout") or int(zh.get("cardCount") or 0) < 1 or "审批可能触碰工作区" not in body:
        raise AssertionError(f"Chinese workspace risk surface should be localized and action-related, got {zh}")


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
          while (Date.now() < openDeadline && !document.querySelector('[data-new-resource="skill"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
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
          const goal = document.querySelector("#resourceBriefGoal");
          const inputs = document.querySelector("#resourceBriefInputs");
          const outputs = document.querySelector("#resourceBriefOutputs");
          const example = document.querySelector("#resourceBriefExample");
          const apply = document.querySelector("#resourceBriefApply");
          if (!designer || !type || !name || !goal || !inputs || !outputs || !example || !apply) return { missingForm: true };
          name.value = "smoke-brief-skill";
          name.dispatchEvent(new Event("input", { bubbles: true }));
          goal.value = "Summarize user requirements into clear implementation tasks.";
          inputs.value = "User request\\nCurrent project context";
          outputs.value = "Task list\\nAcceptance checks";
          example.value = "Create a task plan for a new settings page.";
          [goal, inputs, outputs, example].forEach(node => node.dispatchEvent(new Event("input", { bubbles: true })));
          document.querySelector("#skillInstructions").value = "";
          apply.click();
          await new Promise(resolve => setTimeout(resolve, 350));
          const preview = document.querySelector("#resourceBriefPreview");
          const instructions = document.querySelector("#skillInstructions");
          const dependencyPicker = document.querySelector("[data-resource-dependency-picker]");
          const advanced = document.querySelector("[data-resource-advanced], .resource-advanced-section, .resource-isolation-options");
          return {
            missingOpen: false,
            missingForm: false,
            designerOpen: !designer.classList.contains("hidden"),
            typeValue: type.value,
            previewText: preview ? preview.textContent : "",
            instructionsText: instructions ? instructions.value : "",
            hasDependencyPicker: Boolean(dependencyPicker),
            advancedHiddenOrSecondary: Boolean(advanced && (advanced.closest("details") || advanced.classList.contains("hidden") || advanced.closest("[data-designer-section-panel]")))
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingOpen") or metrics.get("missingForm"):
        raise AssertionError(f"resource brief creation path missing expected controls, got {metrics}")
    if not metrics.get("designerOpen") or metrics.get("typeValue") != "skill":
        raise AssertionError(f"resource brief creation should open the skill designer, got {metrics}")
    if "Summarize user requirements" not in str(metrics.get("previewText", "")):
        raise AssertionError(f"resource brief preview should reflect the user's plain-language goal, got {metrics}")
    if "Acceptance checks" not in str(metrics.get("instructionsText", "")):
        raise AssertionError(f"resource brief apply should generate reusable skill instructions, got {metrics}")
    if not metrics.get("hasDependencyPicker"):
        raise AssertionError(f"resource designer should expose visual dependency choices, got {metrics}")


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
          return {
            missingNode: false,
            hasMeta: Boolean(panel && !panel.classList.contains("hidden") && text),
            text,
            hasDescription: text.includes("负责此阶段的智能体配置。"),
            hasExample: text.includes("先生成一份可供后续阶段消费的计划。") && text.includes("下游节点引用 stages.plan.outputs.plan"),
            hasPlanExample: text.includes("计划阶段"),
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


def assert_settings_warning_resolution_path(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#settings",
        """
        (async () => {
          const deadline = Date.now() + 6000;
          while (Date.now() < deadline && !document.querySelector(".settings-essential-panel")) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const essential = document.querySelector(".settings-essential-panel");
          const providerForm = document.querySelector("[data-provider-setup-form]");
          const workspaceCard = document.querySelector(".settings-workspace-readiness");
          const confirm = document.querySelector("[data-settings-workspace-confirm]");
          const update = document.querySelector(".settings-update-basic");
          const advanced = document.querySelector(".settings-advanced-runtime");
          if (!essential || !providerForm || !workspaceCard || !update || !advanced) return { missing: true };
          const confirmTextBefore = confirm ? confirm.textContent.trim() : "";
          if (confirm) {
            confirm.click();
            const confirmDeadline = Date.now() + 5000;
            while (Date.now() < confirmDeadline && confirm.getAttribute("aria-busy") === "true") {
              await new Promise(resolve => setTimeout(resolve, 120));
            }
          }
          const output = document.querySelector("[data-settings-workspace-output]");
          const advancedWasOpen = advanced.open;
          advanced.open = true;
          await new Promise(resolve => setTimeout(resolve, 100));
          return {
            missing: false,
            hasEssential: Boolean(essential),
            hasProgress: document.querySelectorAll(".settings-setup-progress article").length,
            providerFields: Boolean(
              providerForm.querySelector("[data-provider-setup-type]") &&
              providerForm.querySelector("[data-provider-setup-model]") &&
              providerForm.querySelector("[data-provider-setup-api-key]")
            ),
            confirmTextBefore,
            confirmBusy: confirm ? confirm.getAttribute("aria-busy") : "",
            workspaceOutput: output ? output.textContent.trim() : "",
            hasUpdateBasics: Boolean(update.querySelector("[data-settings-update-check]") || update.classList.contains("disabled")),
            advancedDefaultClosed: advancedWasOpen === false,
            advancedOpenHasPaths: advanced.textContent.includes("Config file") || advanced.textContent.includes("Runtime home")
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError(f"settings ordinary setup path missing expected controls, got {metrics}")
    if int(metrics.get("hasProgress") or 0) < 3:
        raise AssertionError(f"settings should show setup progress cards, got {metrics}")
    if not metrics.get("providerFields"):
        raise AssertionError(f"settings should guide Provider/model/API Key configuration, got {metrics}")
    if metrics.get("confirmTextBefore") and metrics.get("confirmBusy") == "true":
        raise AssertionError(f"settings workspace confirmation should clear busy state, got {metrics}")
    if not metrics.get("hasUpdateBasics"):
        raise AssertionError(f"settings should expose a simple update policy action, got {metrics}")
    if not metrics.get("advancedDefaultClosed") or not metrics.get("advancedOpenHasPaths"):
        raise AssertionError(f"settings raw paths/env details should stay in closed advanced runtime details, got {metrics}")


def assert_playground_context_prompts(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
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
    required_actions = {"files", "approvals"}
    if metrics.get("workspaceRoot") and not metrics.get("workspaceConfirmed"):
        required_actions.add("workspace")
    if not required_actions.issubset(actions):
        raise AssertionError(f"playground context prompts should include relevant file, approval, and workspace actions, got {metrics}")
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
    if "引用精确文件" not in zh_text or "个审批等待处理" not in zh_text or (zh.get("workspaceRoot") and not zh.get("workspaceConfirmed") and "先确认工作区" not in zh_text):
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
          conditionButton.click();
          const nodeDeadline = Date.now() + 5000;
          while (Date.now() < nodeDeadline && !document.querySelector('.flow-node[data-stage-name^="condition"]')) {
            await new Promise(resolve => setTimeout(resolve, 120));
          }
          const modeToggle = document.querySelector("#workflowExpertMode");
          if (!modeToggle) return { missingModeToggle: true };
          if (!modeToggle.checked) {
            modeToggle.click();
            await new Promise(resolve => setTimeout(resolve, 350));
          }
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
            missingModeToggle: false,
            missingTabs: false,
            expertMode: Boolean(document.querySelector(".studio")?.classList.contains("expert-mode")),
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
    if metrics.get("missingPalette") or metrics.get("missingModeToggle") or metrics.get("missingTabs") or not metrics.get("expertMode"):
        raise AssertionError(f"workflow developer field smoke missing controls, got {metrics}")
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


def assert_run_history_artifacts_lazy_loading(browser: Path, base_url: str, timeout: float, profile_dir: Path, smoke_run: dict) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const artifactRef = {json.dumps(smoke_run["artifact_ref"])};
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
            hasArtifactRef: document.body.textContent.includes(artifactRef)
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
    if not metrics.get("hasArtifactRef"):
        raise AssertionError(f"run history artifacts should surface the ref in the DOM, got {metrics}")


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
    if "Saved tokens" not in token_text or "75 tokens" not in token_text:
        raise AssertionError(f"run history should show saved-token attribution, got {metrics}")
    if "Gross prompt baseline" not in token_text or "175 tokens" not in token_text:
        raise AssertionError(f"run history should show gross prompt baseline, got {metrics}")
    if "Net prompt sent" not in token_text or "100 tokens" not in token_text:
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
    node_text = str(metrics.get("nodeText", ""))
    if "budget:" not in node_text or "Prompt tokens" not in node_text:
        raise AssertionError(f"Workflow Studio node should show budget evidence, got {metrics}")
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
          const openButton = Array.from(detail?.querySelectorAll("button[data-history-action]") || [])
            .find(button => button.dataset.historyAction === "open_workflow_studio");
          if (!detail || !openButton) return {{ missingChatAction: true, detailText: detail ? detail.textContent : "" }};
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
            nodeExists: Boolean(node),
            nodeClass: node ? node.className : "",
            nodeText: node ? node.textContent : "",
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
    if not metrics.get("nodeExists") or "runtime-waiting" not in str(metrics.get("nodeClass", "")):
        raise AssertionError(f"Workflow Studio should mark the budget approval node as waiting, got {metrics}")
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
    required_timeline = ["model_escalated", "stage_retry", "stage.params.escalate_model"]
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
          if (!detail || !openButton) return {{ missingStudioAction: true, detailText: detail ? detail.textContent : "" }};
          openButton.click();
          const studioDeadline = Date.now() + 8000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="plan"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 120));
          }}
          const sideTab = document.querySelector('#workflowSideTabRuntime');
          if (sideTab && !sideTab.classList.contains("active")) sideTab.click();
          const stages = ["plan", "iteration", "final-validation", "final-report"];
          const texts = {{}};
          for (const stageName of stages) {{
            const node = document.querySelector(`.flow-node[data-stage-name="${{stageName}}"]`);
            const button = node?.querySelector('[data-node-runtime-open]');
            if (button) button.click();
            const deadline = Date.now() + 8000;
            let panel = document.querySelector("#stageRuntime");
            while (Date.now() < deadline && (!panel || panel.classList.contains("hidden") || !panel.textContent.includes(stageName === "plan" ? "Project plan" : stageName === "iteration" ? "Current iteration" : stageName === "final-validation" ? "Final validation" : "Final report"))) {{
              await new Promise(resolve => setTimeout(resolve, 120));
              panel = document.querySelector("#stageRuntime");
            }}
            texts[stageName] = panel ? panel.textContent : "";
          }}
          const saved = JSON.parse(localStorage.getItem("goflow.workflowStudio.activeRun") || "null");
          return {{
            missingStudioAction: false,
            hash: location.hash,
            savedRunID: saved && saved.runID,
            texts
          }};
        }})()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missingStudioAction"):
        raise AssertionError(f"complex delivery run should open in Workflow Studio, got {metrics}")
    if metrics.get("hash") != "#workflows" or metrics.get("savedRunID") != smoke_run["complex_delivery_run_id"]:
        raise AssertionError(f"Open in Studio should focus the complex delivery run, got {metrics}")
    texts = metrics.get("texts") or {}
    required = {
        "plan": ["Project plan", "Work slices", "Slice A", "Verification strategy", "go test ./...", "Definition of done"],
        "iteration": ["Current iteration", "Selected slice", "Slice A", "Remaining work", "none", "PROJECT_COMPLETE"],
        "final-validation": ["Final validation", "Commands run", "go test ./...", "Requirements coverage", "Slice A covered"],
        "final-report": ["Final report", "Requirements delivered", "README", "Verification evidence", "Artifact references"],
    }
    for stage, needles in required.items():
        text = str(texts.get(stage, ""))
        missing = [needle for needle in needles if needle not in text]
        if missing:
            raise AssertionError(f"complex delivery runtime visualization for {stage} is missing {missing}, got {metrics}")


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


def main() -> int:
    args = parse_args()
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
        smoke_run = write_smoke_session(workspace)
        write_smoke_workflow_graph(runtime_home)
        config_path = write_smoke_runtime_config(runtime_home)
        env.setdefault("GOCACHE", str(cache_dir))
        binary = args.binary
        if not args.skip_build:
            binary = build_binary(tmpdir, env)
        if binary is None:
            raise AssertionError("--skip-build requires --binary")
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
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        wait_for_server(base_url, proc, args.timeout)
        assert_asset_contains(
            base_url,
            "/assets/views/chat.js",
            [
                "runHistoryLazyPanelsHTML",
                "data-history-lazy-stack",
                "fetchAgentRun(run.id, options.summary ? { summary: true } : {})",
                "fetchAgentRunTimeline(run, { limit: 120 })",
                "include_content: true, limit: 12",
                "data-run-context-action",
            ],
        )
        assert_asset_contains(
            base_url,
            "/assets/i18n.js",
            ["chat.runHistoryLazyTitle", "chat.runHistoryLazySummaryFirst", "chat.runHistoryLazyLoad", "chat.runContextFilesTitle"],
        )
        assert_asset_contains(
            base_url,
            "/assets/styles.css",
            ["run-history-lazy-stack", "run-history-lazy-panel", "run-history-lazy-timeline"],
        )
        assert_asset_contains(
            base_url,
            "/assets/styles-enhanced.css",
            ["GoFlow Agent - Enhanced UI Styles", "--primary: #0d9488", "--workflow-node-selected-shadow"],
        )
        try:
            console_dom = render_dom(browser, f"{base_url}/console", args.timeout, profile_dir)
            workflows_dom = render_dom(browser, f"{base_url}/workflows", args.timeout, profile_dir)
            playground_dom = render_dom(browser, f"{base_url}/console#playground", args.timeout, profile_dir)
            approvals_dom = render_dom(browser, f"{base_url}/console#approvals", args.timeout, profile_dir)
            catalog_dom = render_dom(browser, f"{base_url}/console#catalog", args.timeout, profile_dir)
            status_dom = render_dom(browser, f"{base_url}/console#status", args.timeout, profile_dir)
            workspace_dom = render_dom(browser, f"{base_url}/console#workspace", args.timeout, profile_dir)
            memory_dom = render_dom(browser, f"{base_url}/console#memory", args.timeout, profile_dir)
            settings_dom = render_dom(browser, f"{base_url}/console#settings", args.timeout, profile_dir)
            playground_zh_dom = render_dom(browser, zh_url(base_url, "/console#playground"), args.timeout, profile_dir)
            approvals_zh_dom = render_dom(browser, zh_url(base_url, "/console#approvals"), args.timeout, profile_dir)
            memory_zh_dom = render_dom(browser, zh_url(base_url, "/console#memory"), args.timeout, profile_dir)
            workspace_zh_dom = render_dom(browser, zh_url(base_url, "/console#workspace"), args.timeout, profile_dir)
            status_zh_dom = render_dom(browser, zh_url(base_url, "/console#status"), args.timeout, profile_dir)
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
            ["overview-launchpad", "side-status-card", "Build and run Agent workflows visually."],
        )
        assert_rendered(
            "/workflows",
            workflows_dom,
            ["workflow-palette-panel", "workflow-inspector-panel", "canvas-guide", "workflow-context-contract", "Workflows"],
        )
        assert_rendered(
            "/console#playground",
            playground_dom,
            [
                "chat-grid",
                "playground-targets",
                "playground-timeline",
                "Run console",
                "Task request",
            ],
        )
        assert_playground_timeline_layout(browser, base_url, args.timeout, profile_dir)
        assert_playground_context_prompts(browser, base_url, args.timeout, profile_dir)
        assert_approval_queue_layout(browser, base_url, args.timeout, profile_dir)
        assert_ordinary_user_decision_surfaces(browser, base_url, args.timeout, profile_dir)
        assert_workspace_risk_surface(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console#approvals",
            approvals_dom,
            [
                "approvals-grid",
                "approvals-queue",
                "approval-detail",
                "Pending approvals",
                "Approve or deny this request",
                "Will run",
                "May touch",
                "Why approval",
            ],
        )
        assert_rendered(
            "/console#catalog",
            catalog_dom,
            ["resource-shell", "resource-starter-panel", "resource-relation-panel", "Create a linked starter", "resource-dependency-picker"],
        )
        assert_catalog_brief_creation_path(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console#status",
            status_dom,
            [
                "status-hero",
                "status-advanced",
                "status-mcp",
                "status-cost",
                "status-cost-overview",
                "status-cost-summary",
                "status-cost-tuning",
                "status-health-action",
                "Start a run",
                "Advanced diagnostics",
                "MCP tool pressure",
                "Tool pressure",
                "Model and cost diagnostics",
            ],
        )
        assert_rendered(
            "/console#workspace",
            workspace_dom,
            [
                "workspace-boundary",
                "workspace-switcher",
                "Workspace boundary",
                'data-workspace-action-name="confirm"',
                "What this protects",
            ],
        )
        assert_rendered(
            "/console#memory",
            memory_dom,
            [
                "memory-view",
                "memory-artifact-panel",
                "memory-artifact-form",
                "memory-artifact-index",
                "memory-artifact-load-button",
                "memory-solution-governance",
                "data-solution-supersede",
                "Smoke provider retry decision",
                "go test ./internal/memory",
                "Artifact viewer",
                "Recent artifacts",
                "Smoke Artifact Report",
            ],
        )
        assert_memory_artifact_layout(browser, base_url, args.timeout, profile_dir, smoke_artifact)
        assert_workflow_metadata_zh(browser, base_url, args.timeout, profile_dir)
        assert_simple_workflow_build_path(browser, base_url, args.timeout, profile_dir)
        assert_workflow_developer_fields_folded(browser, base_url, args.timeout, profile_dir)
        assert_run_history_lazy_loading(browser, base_url, args.timeout, profile_dir, smoke_run)
        assert_run_history_artifacts_lazy_loading(browser, base_url, args.timeout, profile_dir, smoke_run)
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
                "settings-essential-panel",
                "settings-advanced-runtime",
                "Essential setup",
                "Update policy",
                "settings-update-panel",
                "settings-update-check",
                "Check latest release",
                "Check for updates",
                "Configuration health",
            ],
        )
        assert_settings_warning_resolution_path(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console?lang=zh#memory",
            memory_zh_dom,
            [
                "memory-view",
                "memory-artifact-panel",
                "memory-artifact-form",
                "memory-artifact-index",
                "memory-artifact-load-button",
                "memory-solution-governance",
                "data-solution-supersede",
                "Smoke provider retry decision",
                "产物查看器",
                "最近产物",
                "只有需要 sha256 引用时才加载完整内容。",
                "Smoke Artifact Report",
            ],
        )
        assert_rendered(
            "/console?lang=zh#playground",
            playground_zh_dom,
            [
                "chat-grid",
                "playground-targets",
                "playground-timeline",
                "任务运行控制台",
                "任务输入",
            ],
        )
        assert_rendered(
            "/console?lang=zh#approvals",
            approvals_zh_dom,
            [
                "approvals-grid",
                "approvals-queue",
                "approval-detail",
                "待审批",
                "批准或拒绝这次请求",
                "将执行",
                "可能影响",
                "审批原因",
            ],
        )
        assert_rendered(
            "/console?lang=zh#workspace",
            workspace_zh_dom,
            [
                "workspace-boundary",
                "workspace-switcher",
                "工作区边界",
                'data-workspace-action-name="confirm"',
                "这里保护什么",
            ],
        )
        assert_rendered(
            "/console?lang=zh#status",
            status_zh_dom,
            [
                "status-hero",
                "status-advanced",
                "status-cost",
                "运行健康与执行状态",
                "发起运行",
                "高级诊断",
                "模型与成本诊断",
                "低成本路由调优",
            ],
        )
        assert_rendered(
            "/console?lang=zh#settings",
            settings_zh_dom,
            [
                "settings-essential-panel",
                "settings-advanced-runtime",
                "必要设置",
                "更新策略",
                "settings-update-panel",
                "settings-update-check",
                "检查最新版本",
                "检查更新",
                "配置健康",
            ],
        )
        assert_rendered(
            "/console?lang=en#settings",
            settings_en_dom,
            [
                "settings-update-panel",
                "settings-update-check",
                "Check latest release",
                "Check for updates",
                "Configuration health",
            ],
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
