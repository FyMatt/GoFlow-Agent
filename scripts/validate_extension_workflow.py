#!/usr/bin/env python3
"""Smoke-test the end-to-end extension example."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[1]
EXAMPLE_ROOT = REPO_ROOT / "examples" / "extension-workflow"


def read_text(path: Path) -> str:
    if not path.exists():
        raise AssertionError(f"missing example file: {path}")
    return path.read_text(encoding="utf-8")


def jsonrpc(req_id: int, method: str, params: dict[str, Any] | None = None) -> str:
    payload: dict[str, Any] = {"jsonrpc": "2.0", "id": req_id, "method": method}
    if params is not None:
        payload["params"] = params
    return json.dumps(payload, ensure_ascii=False)


def call_tool(req_id: int, name: str, arguments: dict[str, Any]) -> str:
    return jsonrpc(req_id, "tools/call", {"name": name, "arguments": arguments})


def run_example_server(server: Path, workspace: Path) -> list[dict[str, Any]]:
    requests = "\n".join(
        [
            jsonrpc(1, "tools/list"),
            call_tool(2, "workspace_summary", {"max_files": 4}),
        ]
    )
    env = os.environ.copy()
    env["GOFLOW_WORKSPACE_ROOT"] = str(workspace)
    proc = subprocess.run(
        [sys.executable, str(server)],
        input=requests + "\n",
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        check=False,
    )
    if proc.returncode != 0:
        raise AssertionError(f"example MCP server exited {proc.returncode}: {proc.stderr}")
    lines = [line for line in proc.stdout.splitlines() if line.strip()]
    if len(lines) != 2:
        raise AssertionError(f"expected 2 responses, got {len(lines)}: {proc.stdout}")
    return [json.loads(line) for line in lines]


def assert_tool_schema(response: dict[str, Any]) -> None:
    tools = response.get("result", {}).get("tools", [])
    if len(tools) != 1:
        raise AssertionError(f"expected one tool, got {tools}")
    tool = tools[0]
    if tool.get("name") != "workspace_summary":
        raise AssertionError(f"unexpected tool name: {tool}")
    if tool.get("kind") != "read":
        raise AssertionError(f"expected read tool kind: {tool}")
    schema = tool.get("input_schema") or {}
    if schema.get("type") != "object":
        raise AssertionError(f"expected object schema: {schema}")
    if schema.get("additionalProperties") is not False:
        raise AssertionError(f"expected strict schema: {schema}")
    if "max_files" not in (schema.get("properties") or {}):
        raise AssertionError(f"expected max_files property: {schema}")


def assert_workspace_summary(response: dict[str, Any]) -> None:
    result = response.get("result", {})
    if result.get("is_error"):
        raise AssertionError(f"workspace_summary failed: {result.get('content')}")
    payload = json.loads(result.get("content", "{}"))
    paths = [item["path"] for item in payload.get("files", [])]
    largest = [item["path"] for item in payload.get("largest_files", [])]
    if payload.get("sampled_files") != 4:
        raise AssertionError(f"expected 4 sampled files: {payload}")
    if not any(item.get("extension") == ".py" for item in payload.get("top_extensions", [])):
        raise AssertionError(f"expected .py extension summary: {payload}")
    if "src/app.py" not in paths:
        raise AssertionError(f"expected src/app.py in sampled files: {paths}")
    if "large.bin" not in largest:
        raise AssertionError(f"expected large.bin in largest files: {largest}")
    if any(path.startswith(".git/") for path in paths + largest):
        raise AssertionError(f"expected .git files to be skipped: paths={paths} largest={largest}")


def assert_example_references() -> None:
    skill = read_text(EXAMPLE_ROOT / "skills" / "workspace-report" / "SKILL.md")
    agent = read_text(EXAMPLE_ROOT / "configs" / "agents" / "workspace-reporter.yaml")
    workflow = read_text(EXAMPLE_ROOT / "workflows" / "workspace-review" / "workflow.yaml")
    readme = read_text(EXAMPLE_ROOT / "README.md")

    required = {
        "skill": [
            "name: workspace-report",
            "workspace_report/workspace_summary",
            "preferred_agent: workspace-reporter",
            "next_skills: [code-audit]",
        ],
        "agent": [
            "workspace-reporter:",
            "allowed_tool_kinds: [read]",
            "workspace_report/workspace_summary",
        ],
        "workflow": [
            "name: workspace-review",
            "agent: workspace-reporter",
            "skill: workspace-report",
            "agent: auditor",
            "skill: code-audit",
        ],
        "readme": [
            "/reload-tools",
            "/workflow workspace-review",
            "workspace_report/workspace_summary",
        ],
    }
    contents = {"skill": skill, "agent": agent, "workflow": workflow, "readme": readme}
    for label, needles in required.items():
        for needle in needles:
            if needle not in contents[label]:
                raise AssertionError(f"{label} missing {needle!r}")


def create_workspace(root: Path) -> None:
    (root / "src").mkdir()
    (root / "docs").mkdir()
    (root / ".git").mkdir()
    (root / "README.md").write_text("# Demo\n", encoding="utf-8")
    (root / "src" / "app.py").write_text("print('hello')\n", encoding="utf-8")
    (root / "docs" / "guide.md").write_text("guide\n", encoding="utf-8")
    (root / "large.bin").write_bytes(b"x" * 2048)
    (root / ".git" / "config").write_text("ignored\n", encoding="utf-8")


def main() -> int:
    server = EXAMPLE_ROOT / "mcp_servers" / "workspace_report.py"
    assert_example_references()
    with tempfile.TemporaryDirectory(prefix="goflow-extension-example-") as workspace:
        workspace_root = Path(workspace)
        create_workspace(workspace_root)
        tool_list, summary = run_example_server(server, workspace_root)
        assert_tool_schema(tool_list)
        assert_workspace_summary(summary)
    print("extension workflow example validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

