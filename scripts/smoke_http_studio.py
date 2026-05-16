#!/usr/bin/env python3
"""Smoke-test the embedded HTTP Studio without a real model backend.

The script builds a temporary goflow binary, starts it with placeholder model
environment variables, checks key Studio pages/assets/API endpoints, and then
shuts the server down. It uses only Python's standard library so it can run in
CI on Linux and locally on Windows.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import textwrap
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=0, help="HTTP port to use. Defaults to a free local port.")
    parser.add_argument("--timeout", type=float, default=45.0, help="Seconds to wait for startup.")
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


def request_text(
    url: str,
    timeout: float = 5.0,
    *,
    method: str = "GET",
    data: bytes | None = None,
    headers: dict[str, str] | None = None,
) -> tuple[int, str, str]:
    request_headers = {"User-Agent": "goflow-http-smoke/1"}
    if headers:
        request_headers.update(headers)
    request = urllib.request.Request(url, data=data, headers=request_headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            content_type = response.headers.get("Content-Type", "")
            return response.status, response.read().decode("utf-8", errors="replace"), content_type
    except urllib.error.HTTPError as exc:
        content_type = exc.headers.get("Content-Type", "")
        return exc.code, exc.read().decode("utf-8", errors="replace"), content_type


def assert_contains(label: str, text: str, needles: list[str]) -> None:
    for needle in needles:
        if needle not in text:
            raise AssertionError(f"{label} missing {needle!r}")


def build_binary(tmpdir: Path, env: dict[str, str]) -> Path:
    suffix = ".exe" if os.name == "nt" else ""
    binary = tmpdir / f"goflow-smoke{suffix}"
    run_checked(["go", "build", "-o", str(binary), "./cmd/goflow"], env)
    return binary


def write_smoke_runtime_config(runtime_home: Path) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir_path = runtime_home / "skills"
    skill_dir_path.mkdir(parents=True, exist_ok=True)
    smoke_skill_dir = skill_dir_path / "smoke"
    smoke_skill_dir.mkdir(parents=True, exist_ok=True)
    (smoke_skill_dir / "SKILL.md").write_text(
        textwrap.dedent(
            """
            ---
            name: smoke
            description: Minimal skill used by the HTTP Studio smoke test runtime.
            ---

            Use this placeholder skill only for local smoke tests.
            """
        ).lstrip(),
        encoding="utf-8",
    )
    skill_dir = json.dumps(str(skill_dir_path))
    config_text = textwrap.dedent(
        f"""
        agent:
          name: GoFlow HTTP Smoke
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
            description: Smoke-test chat agent.
            provider: primary
            mode: chat
            tool_policy: confirm
            allowed_tool_kinds: [read, network]
            max_iterations: 4
          auditor:
            name: Auditor
            description: Smoke-test verifier agent.
            provider: primary
            mode: audit
            tool_policy: confirm
            allowed_tool_kinds: [read]
            max_iterations: 2

        skill:
          directory: {skill_dir}
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


def smoke_endpoint(base_url: str, path: str, needles: list[str], expected_status: int = 200) -> None:
    status, body, _ = request_text(f"{base_url}{path}")
    if status != expected_status:
        raise AssertionError(f"{path} returned {status}, expected {expected_status}: {body[:500]}")
    assert_contains(path, body, needles)


def smoke_json_endpoint(base_url: str, path: str, required_keys: list[str]) -> dict:
    status, body, content_type = request_text(f"{base_url}{path}")
    if status != 200:
        raise AssertionError(f"{path} returned {status}: {body[:500]}")
    if "json" not in content_type.lower():
        raise AssertionError(f"{path} returned non-JSON content type {content_type!r}")
    payload = json.loads(body)
    for key in required_keys:
        if key not in payload:
            raise AssertionError(f"{path} missing JSON key {key!r}: {payload!r}")
    return payload


def post_json_endpoint(base_url: str, path: str, payload: dict, expected_statuses: set[int]) -> dict:
    status, body, content_type = request_text(
        f"{base_url}{path}",
        method="POST",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    if status not in expected_statuses:
        raise AssertionError(f"{path} returned {status}, expected {sorted(expected_statuses)}: {body[:500]}")
    if "json" not in content_type.lower():
        raise AssertionError(f"{path} returned non-JSON content type {content_type!r}")
    return json.loads(body)


def assert_named(label: str, items: list[dict], name: str) -> dict:
	for item in items:
		if item.get("name") == name or item.get("type") == name:
			return item
	raise AssertionError(f"{label} missing named item {name!r}: {items!r}")


def assert_mcp_runtime_metrics(runtime: dict) -> None:
    servers = runtime.get("mcp_servers")
    if servers is None:
        return
    if not isinstance(servers, list):
        raise AssertionError(f"/api/runtime mcp_servers should be a list: {runtime!r}")
    for server in servers:
        if not isinstance(server, dict) or server.get("enabled") is False:
            continue
        for key in ("max_concurrent_calls", "active_calls", "queued_calls", "available_call_slots"):
            if key not in server:
                raise AssertionError(f"/api/runtime MCP server missing {key!r}: {server!r}")
            value = server.get(key)
            if not isinstance(value, int) or value < 0:
                raise AssertionError(f"/api/runtime MCP server {key!r} should be a non-negative integer: {server!r}")


def assert_resource_statuses(label: str, items: list[dict], expected: dict[str, str]) -> None:
    got = {(item.get("kind"), item.get("name")) for item in items}
    missing = {(kind, name) for kind, name in expected.items() if (kind, name) not in got}
    if missing:
        raise AssertionError(f"{label} missing materialized resources {sorted(missing)!r}: {items!r}")


def wait_for_server(base_url: str, proc: subprocess.Popen[str], timeout: float) -> None:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        if proc.poll() is not None:
            output = ""
            if proc.stdout is not None:
                output = proc.stdout.read() or ""
            raise AssertionError(f"goflow exited before HTTP became ready with {proc.returncode}\n{output}")
        try:
            status, _, _ = request_text(f"{base_url}/api/session", timeout=2.0)
            if status == 200:
                return
        except Exception as exc:  # noqa: BLE001 - include startup failure context.
            last_error = str(exc)
        time.sleep(0.25)
    raise AssertionError(f"HTTP server did not become ready at {base_url}: {last_error}")


def main() -> int:
    args = parse_args()
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
    tmpdir = Path(tempfile.mkdtemp(prefix="goflow-http-smoke-"))
    proc: subprocess.Popen[str] | None = None
    try:
        runtime_home = tmpdir / "runtime"
        cache_dir = tmpdir / "gocache"
        gotmp_dir = tmpdir / "gotmp"
        workspace = tmpdir / "workspace"
        runtime_home.mkdir()
        cache_dir.mkdir()
        gotmp_dir.mkdir()
        workspace.mkdir()
        smoke_artifact = write_smoke_artifact(workspace)
        config_path = write_smoke_runtime_config(runtime_home)
        env.setdefault("GOCACHE", str(cache_dir))
        env.setdefault("GOTMPDIR", str(gotmp_dir))
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
        smoke_endpoint(base_url, "/console", ["GoFlow Console", "Agent Workbench", "/assets/app.js"])
        smoke_endpoint(base_url, "/workflows", ["GoFlow Console", "Workflows", "/assets/app.js"])
        smoke_endpoint(base_url, "/assets/app.js", ["renderWorkflows", "renderCatalog", "renderSettings", "renderMemory"])
        smoke_endpoint(
            base_url,
            "/assets/views/chat.js",
            [
                "runHistoryLazyPanelsHTML",
                "data-history-lazy-stack",
                "fetchWorkflowRun(run.id, options.summary ? { summary: true } : {})",
                "include_content: true, limit: 12",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/views/memory.js",
            [
                "renderMemory",
                "fetchArtifactObjects",
                "memory-artifact-form",
                "memory-artifact-index",
                "data-artifact-open",
                "memory-artifact-load-button",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/views/status.js",
            [
                "summarizeMCPPressure",
                "status-mcp",
                "queued_calls",
                "available_call_slots",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/views/settings.js",
            [
                "data-settings-update-check",
                "/api/update-policy/check",
                "renderUpdateCheckResult",
                "renderUpdateAssetSummary",
                "updateVerifySteps",
                "updateCheckDisabledText",
                "settings-update-check-card",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/views/workflows.js",
            [
                "data-node-type-action",
                "data-advanced-guide-example",
                "data-artifact-guide-action",
                "data-acceptance-guide-action",
                "workflow-context-contract",
                "stageContextInclude",
                "stageContextMaxTokens",
                "workflowTemplateResourceRefs",
                "workflow-stage-data-flow",
                "workflow-resource-picker",
                "data-stage-resource-choice",
                "refreshActiveWorkflowInspectorPanel",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/views/catalog.js",
            [
                "renderCatalog",
                "resource",
                "kit",
                "resource-starter-panel",
                "resource-relation-panel",
                "resource-override-paths",
                "resource-dependency-picker",
                "data-resource-dependency-choice",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/i18n.js",
            [
                "workflow.nodeTypeOverridePath",
                "workflow.expressionFunctionOverridePath",
                "workflow.templateResourceAgent",
                "status.mcpTitle",
                "status.metric.tools",
                "settings.updateCheckTitle",
                "settings.updateAvailableTitle",
                "settings.updateCheckDisabledTitle",
                "settings.updateIntegrityReady",
                "settings.updateAssetChecksums",
                "settings.updateVerifySteps",
                "catalog.relationTitle",
                "catalog.starterCreateFull",
                "catalog.toolScaffolds",
                "catalog.dependencyPickerTitle",
                "catalog.dependencySkillTitle",
                "catalog.starterFlowTitle",
                "memory.recentArtifacts",
                "memory.artifactLoading",
                "memory.loadArtifact",
                "workflow.contextContractTitle",
                "workflow.contextInclude",
                "workflow.contextMaxTokens",
                "workflow.resourcePicker.agent.title",
                "workflow.resourcePicker.team_template.title",
                "chat.runHistoryLazyTitle",
                "chat.runHistoryLazySummaryFirst",
                "chat.runHistoryLazyLoad",
            ],
        )
        smoke_endpoint(
            base_url,
            "/assets/styles.css",
            [
                "workflow-expression-assist",
                "workflow-node-type-meta",
                "workflow-template-resource-row",
                "resource-relation-card",
                "resource-starter-step",
                "status-mcp-server",
                "status-mcp-bar",
                "settings-update-check-card",
                "settings-update-integrity",
                "settings-update-verify",
                "workflow-context-contract",
                "workflow-resource-picker",
                "workflow-resource-card",
                "resource-dependency-picker",
                "resource-dependency-card",
                "resource-starter-flow",
                "content-visibility",
                "memory-artifact-index",
                "memory-artifact-load-button",
                "memory-artifact-body",
                "run-history-lazy-stack",
                "run-history-lazy-panel",
                "run-history-lazy-timeline",
            ],
        )
        session = smoke_json_endpoint(base_url, "/api/session", ["active_agent", "mode"])
        memory = smoke_json_endpoint(base_url, "/api/memory", ["project", "file_index", "errors"])
        if "Full artifact body for memory viewer" in json.dumps(memory):
            raise AssertionError(f"/api/memory should not include full artifact content: {memory!r}")
        artifact_index = smoke_json_endpoint(base_url, "/api/artifacts?limit=1", ["objects"])
        index_text = json.dumps(artifact_index)
        if smoke_artifact["hash"] not in index_text or smoke_artifact["title"] not in index_text:
            raise AssertionError(f"/api/artifacts did not include smoke artifact metadata: {artifact_index!r}")
        if smoke_artifact["content"] in index_text:
            raise AssertionError(f"/api/artifacts should omit full artifact content: {artifact_index!r}")
        artifact_meta = smoke_json_endpoint(base_url, f"/api/artifacts/{smoke_artifact['hash']}", ["ref", "hash"])
        if artifact_meta.get("content"):
            raise AssertionError(f"/api/artifacts/{{hash}} should omit content by default: {artifact_meta!r}")
        artifact_detail = smoke_json_endpoint(base_url, f"/api/artifacts/{smoke_artifact['hash']}?content=1", ["ref", "hash", "content"])
        if artifact_detail.get("content") != smoke_artifact["content"]:
            raise AssertionError(f"/api/artifacts/{{hash}}?content=1 did not return full content: {artifact_detail!r}")
        runtime = smoke_json_endpoint(base_url, "/api/runtime", ["active_agent", "mode", "agents", "tools"])
        assert_mcp_runtime_metrics(runtime)
        update_policy = smoke_json_endpoint(base_url, "/api/update-policy", ["current_version", "release_feed", "check_endpoint", "network_opt_in", "check_enabled"])
        if update_policy.get("check_endpoint") != "/api/update-policy/check" or update_policy.get("network_opt_in") is not True or update_policy.get("check_enabled") is not True:
            raise AssertionError(f"/api/update-policy missing opt-in check metadata: {update_policy!r}")
        resources = smoke_json_endpoint(base_url, "/api/resources", ["meta", "counts", "families"])
        options = smoke_json_endpoint(base_url, "/api/workflow-options", ["agents", "skills", "node_types", "workflow_executors"])
        tool_scaffolds = smoke_json_endpoint(base_url, "/api/resources/tools/scaffolds", [])
        kit_scaffolds = smoke_json_endpoint(base_url, "/api/resources/kits/scaffolds", [])
        if not runtime.get("agents"):
            raise AssertionError("/api/runtime returned no agents")
        if not options.get("node_types"):
            raise AssertionError("/api/workflow-options returned no node types")
        if "chat" not in json.dumps(session):
            raise AssertionError(f"/api/session did not include chat context: {session!r}")
        family_kinds = {family.get("kind") for family in resources.get("families", [])}
        for required_family in ("agent", "skill", "kit"):
            if required_family not in family_kinds:
                raise AssertionError(f"/api/resources missing {required_family!r} family: {resources!r}")
        for required_node_type in ("agent", "skill", "tool", "condition", "quality_gate", "checkpoint"):
            assert_named("/api/workflow-options node_types", options.get("node_types", []), required_node_type)
        for required_executor in ("plan-fix-audit", "skill-chain"):
            executor = assert_named("/api/workflow-options workflow_executors", options.get("workflow_executors", []), required_executor)
            if executor.get("source") != "legacy_executor" or not executor.get("legacy") or not executor.get("compatibility"):
                raise AssertionError(f"workflow executor missing legacy compatibility metadata: {executor!r}")
        readonly_tool = assert_named("/api/resources/tools/scaffolds", tool_scaffolds, "python-container-readonly")
        if readonly_tool.get("isolation") != "container" or readonly_tool.get("workspace_mount") != "ro":
            raise AssertionError(f"readonly tool scaffold missing container read-only defaults: {readonly_tool!r}")
        if not readonly_tool.get("default_image") or not readonly_tool.get("default_isolation_options"):
            raise AssertionError(f"readonly tool scaffold missing image/isolation defaults: {readonly_tool!r}")
        multi_domain = assert_named("/api/resources/kits/scaffolds", kit_scaffolds, "multi-domain-agent")
        if not multi_domain.get("recommended_workflow") or not multi_domain.get("recommended_agent"):
            raise AssertionError(f"multi-domain kit scaffold missing recommendations: {multi_domain!r}")
        if len(multi_domain.get("agents", [])) < 4 or len(multi_domain.get("workflow_templates", [])) < 4:
            raise AssertionError(f"multi-domain kit scaffold lost linked resources: {multi_domain!r}")
        tool_detail = smoke_json_endpoint(
            base_url,
            "/api/resources/tools/scaffolds/python-container-readonly?name=smoke-reader",
            ["name", "document"],
        )
        tool_doc = tool_detail.get("document") or {}
        if tool_doc.get("isolation") != "container" or tool_doc.get("isolation_options", {}).get("workspace_mount") != "ro":
            raise AssertionError(f"tool scaffold document missing container isolation defaults: {tool_detail!r}")
        materialized = post_json_endpoint(
            base_url,
            "/api/resources/kits/scaffolds/agent-framework?materialize=1",
            {"name": "smoke-agent-framework", "overwrite": True, "materialize": True},
            {200, 201},
        )
        if not materialized.get("materialized") or not materialized.get("restart_required"):
            raise AssertionError(f"kit materialization missing status flags: {materialized!r}")
        assert_resource_statuses(
            "agent-framework materialized kit",
            materialized.get("resources", []),
            {
                "kit": "smoke-agent-framework",
                "agent": "smoke-agent-framework-agent",
                "skill": "smoke-agent-framework-skill",
                "tool": "smoke-agent-framework-helper",
                "workflow": "smoke-agent-framework-workflow",
                "workflow_template": "smoke-agent-framework-template",
                "team_template": "smoke-agent-framework-team",
                "policy_rule": "smoke-agent-framework-gate",
            },
        )
        saved_kit = smoke_json_endpoint(base_url, "/api/resources/kits/smoke-agent-framework", ["name", "agents", "skills", "tools", "workflow_templates"])
        if saved_kit.get("metadata", {}).get("materialized") != "true":
            raise AssertionError(f"materialized kit metadata missing materialized marker: {saved_kit!r}")
        saved_kits = smoke_json_endpoint(base_url, "/api/kits", [])
        if not isinstance(saved_kits, list):
            raise AssertionError(f"/api/kits should return a list after materialization: {saved_kits!r}")
        assert_named("/api/kits", saved_kits, "smoke-agent-framework")
        saved_tool = smoke_json_endpoint(base_url, "/api/resources/tools/smoke-agent-framework-helper", ["name", "isolation", "isolation_options"])
        if saved_tool.get("isolation") != "container" or saved_tool.get("isolation_options", {}).get("workspace_mount") != "ro":
            raise AssertionError(f"materialized helper tool lost container sandbox defaults: {saved_tool!r}")
        print(f"HTTP Studio smoke passed at {base_url}")
        return 0
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
