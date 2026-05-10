#!/usr/bin/env python3
"""Validate the packaged binary-analysis-kit example and helper MCP server."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[1]
EXAMPLE_ROOT = REPO_ROOT / "examples" / "binary-analysis-kit"


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


def decode_result(line: str) -> dict[str, Any]:
    payload = json.loads(line)
    result = payload.get("result", {})
    if result.get("is_error"):
        raise AssertionError(f"request {payload.get('id')} failed: {result.get('content')}")
    content = result.get("content", "{}")
    return json.loads(content)


def run_example_server(server: Path, workspace: Path) -> list[dict[str, Any]]:
    requests = "\n".join(
        [
            jsonrpc(1, "tools/list"),
            call_tool(2, "binary_file_info", {"path": "sample.bin"}),
            call_tool(3, "binary_strings", {"path": "sample.bin", "min_length": 5, "max_results": 10}),
            call_tool(4, "hex_preview", {"path": "sample.bin", "length": 16}),
            call_tool(5, "read_text", {"path": "notes.txt"}),
            call_tool(6, "binary_file_info", {"path": "../outside.bin"}),
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
    if len(lines) != 6:
        raise AssertionError(f"expected 6 responses, got {len(lines)}: {proc.stdout}")
    return [json.loads(line) for line in lines]


def assert_tool_schema(response: dict[str, Any]) -> None:
    tools = response.get("result", {}).get("tools", [])
    names = {tool.get("name") for tool in tools}
    expected = {"ping", "read_text", "binary_file_info", "binary_strings", "hex_preview"}
    if names != expected:
        raise AssertionError(f"unexpected tool names: {tools}")
    for tool in tools:
        if tool.get("kind") != "read":
            raise AssertionError(f"expected read-only tool kinds: {tool}")
        schema = tool.get("input_schema") or {}
        if schema.get("type") != "object" or schema.get("additionalProperties") is not False:
            raise AssertionError(f"expected strict object schema: {schema}")


def assert_binary_results(responses: list[dict[str, Any]]) -> None:
    binary_info = decode_result(json.dumps(responses[1], ensure_ascii=False))
    strings_payload = decode_result(json.dumps(responses[2], ensure_ascii=False))
    hex_payload = decode_result(json.dumps(responses[3], ensure_ascii=False))
    notes_payload = decode_result(json.dumps(responses[4], ensure_ascii=False))

    if binary_info.get("relative_path") != "sample.bin" or binary_info.get("size") != 16:
        raise AssertionError(f"unexpected binary info: {binary_info}")
    if binary_info.get("magic_ascii") != "MZ..Hello.World!":
        raise AssertionError(f"unexpected binary ascii summary: {binary_info}")
    if not isinstance(binary_info.get("entropy_sample"), float):
        raise AssertionError(f"expected entropy sample: {binary_info}")

    values = [item.get("value") for item in strings_payload.get("strings", [])]
    if values != ["Hello", "World!"]:
        raise AssertionError(f"unexpected extracted strings: {strings_payload}")

    lines = hex_payload.get("lines", [])
    if not lines or lines[0].get("offset") != 0 or "4d 5a" not in lines[0].get("hex", ""):
        raise AssertionError(f"unexpected hex preview: {hex_payload}")

    if notes_payload.get("content") not in {"analyst note\n", "analyst note\r\n"}:
        raise AssertionError(f"expected UTF-8 note read, got {notes_payload}")

    escape = responses[5].get("result", {})
    if not escape.get("is_error") or "escapes workspace root" not in escape.get("content", ""):
        raise AssertionError(f"expected workspace escape rejection, got {escape}")


def assert_example_references() -> None:
    helper = read_text(EXAMPLE_ROOT / "mcp_servers" / "binary-analysis-kit-helper.py")
    skill = read_text(EXAMPLE_ROOT / "skills" / "binary-analysis-kit-skill" / "SKILL.md")
    agent = read_text(EXAMPLE_ROOT / "configs" / "agents" / "binary-analysis-kit-agent.yaml")
    mcp_config = read_text(EXAMPLE_ROOT / "configs" / "mcp_servers" / "binary-analysis-kit-helper.yaml")
    workflow = read_text(EXAMPLE_ROOT / "workflows" / "binary-analysis-kit-workflow" / "workflow.yaml")
    team = read_text(EXAMPLE_ROOT / "templates" / "teams" / "binary-analysis-kit-team.yaml")
    kit = read_text(EXAMPLE_ROOT / "kits" / "binary-analysis-kit" / "kit.yaml")
    readme = read_text(EXAMPLE_ROOT / "README.md")

    required = {
        "helper": [
            '"name": "binary_file_info"',
            '"name": "binary_strings"',
            '"name": "hex_preview"',
            'return data.decode("utf-8-sig")',
        ],
        "skill": [
            "binary-analysis-kit-helper/binary_file_info",
            "binary-analysis-kit-helper/binary_strings",
            "binary-analysis-kit-helper/hex_preview",
        ],
        "agent": [
            "binary-analysis-kit-helper/binary_file_info",
            "binary-analysis-kit-helper/binary_strings",
            "binary-analysis-kit-helper/hex_preview",
        ],
        "mcp_config": [
            "isolation: container",
            "network_disabled: true",
            "workspace_mount: ro",
            "tool_source: ${GOFLOW_BINARY_ANALYSIS_KIT_TOOL_SOURCE}",
        ],
        "workflow": [
            "agent: binary-analysis-kit-agent",
            "skill: binary-analysis-kit-skill",
            "rule: binary-analysis-kit-gate",
        ],
        "team": [
            "recommended_workflow: binary-analysis-kit-workflow",
            "binary-analysis-kit-helper/binary_file_info",
            "binary-analysis-kit-helper/binary_strings",
        ],
        "kit": [
            "recommended_agent: binary-analysis-kit-agent",
            "recommended_workflow: binary-analysis-kit-workflow",
            "recommended_team: binary-analysis-kit-team",
            "request: Perform static triage on sample.bin and report likely risk areas without executing it.",
        ],
        "readme": [
            "/workflow binary-analysis-kit-workflow inspect sample.bin and report likely attack surfaces",
            "Do not use `@sample.bin` here",
            "python scripts/validate_binary_analysis_kit.py",
        ],
    }
    contents = {
        "helper": helper,
        "skill": skill,
        "agent": agent,
        "mcp_config": mcp_config,
        "workflow": workflow,
        "team": team,
        "kit": kit,
        "readme": readme,
    }
    for label, needles in required.items():
        for needle in needles:
            if needle not in contents[label]:
                raise AssertionError(f"{label} missing {needle!r}")


def create_workspace(root: Path) -> None:
    (root / "sample.bin").write_bytes(b"MZ\x00\x01Hello\x00World!")
    (root / "notes.txt").write_text("analyst note\n", encoding="utf-8")
    outside_dir = Path(tempfile.mkdtemp(prefix="goflow-binary-example-outside-"))
    (outside_dir / "outside.bin").write_bytes(b"outside")


def main() -> int:
    server = EXAMPLE_ROOT / "mcp_servers" / "binary-analysis-kit-helper.py"
    assert_example_references()
    with tempfile.TemporaryDirectory(prefix="goflow-binary-analysis-example-") as workspace:
        workspace_root = Path(workspace)
        create_workspace(workspace_root)
        responses = run_example_server(server, workspace_root)
        assert_tool_schema(responses[0])
        assert_binary_results(responses)
    print("binary-analysis-kit example validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
