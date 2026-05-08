#!/usr/bin/env python3
"""Validate the built-in Python MCP server with a temporary workspace."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path


def request(req_id: int, name: str, arguments: dict) -> str:
    return json.dumps(
        {
            "jsonrpc": "2.0",
            "id": req_id,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        },
        ensure_ascii=False,
    )


def decode_response(line: str) -> tuple[int, dict]:
    payload = json.loads(line)
    result = payload.get("result", {})
    if result.get("is_error"):
        raise AssertionError(f"request {payload.get('id')} failed: {result.get('content')}")
    return int(payload["id"]), json.loads(result["content"])


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--server",
        default=str(Path(__file__).resolve().parents[1] / "mcp_servers" / "python_notes.py"),
        help="Path to python_notes.py",
    )
    args = parser.parse_args()

    server = Path(args.server).resolve()
    if not server.exists():
        raise SystemExit(f"server not found: {server}")

    with tempfile.TemporaryDirectory(prefix="goflow-python-mcp-") as workspace:
        root = Path(workspace)
        (root / "sample.py").write_text(
            "import os\n\n\ndef hello(name):\n    return f'hello {name}'\n",
            encoding="utf-8-sig",
        )
        (root / "data.json").write_text('{"app":{"name":"demo"}}', encoding="utf-8-sig")
        (root / "sample.bin").write_bytes(b"MZ\x00\x01Hello\x00World")
        outside = Path(tempfile.mkdtemp(prefix="goflow-python-mcp-outside-"))
        outside_file = outside / "outside.txt"
        outside_file.write_text("outside", encoding="utf-8")
        symlink_supported = True
        try:
            (root / "outside-link.txt").symlink_to(outside_file)
        except OSError:
            symlink_supported = False

        request_lines = [
            request(1, "python_ast_summary", {"path": "sample.py"}),
            request(2, "json_query", {"path": "data.json", "query": "app.name"}),
            request(3, "binary_file_info", {"path": "sample.bin"}),
            request(4, "binary_strings", {"path": "sample.bin", "min_length": 4, "max_results": 5}),
            request(5, "hex_preview", {"path": "sample.bin", "length": 16}),
            request(6, "read_note", {"path": "../outside.txt"}),
        ]
        if symlink_supported:
            request_lines.append(request(7, "read_note", {"path": "outside-link.txt"}))
        requests = "\n".join(request_lines)

        env = os.environ.copy()
        env["GOFLOW_WORKSPACE_ROOT"] = str(root)
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
            raise SystemExit(f"server exited {proc.returncode}: {proc.stderr}")

        lines = [line for line in proc.stdout.splitlines() if line.strip()]
        expected_lines = 7 if symlink_supported else 6
        if len(lines) != expected_lines:
            raise AssertionError(f"expected {expected_lines} responses, got {len(lines)}: {proc.stdout}")

        decoded = {}
        for line in lines[:5]:
            req_id, payload = decode_response(line)
            decoded[req_id] = payload

        escape = json.loads(lines[5])["result"]
        if not escape.get("is_error") or "escapes workspace root" not in escape.get("content", ""):
            raise AssertionError(f"expected workspace escape rejection, got {escape}")
        if symlink_supported:
            symlink_escape = json.loads(lines[6])["result"]
            if not symlink_escape.get("is_error") or "resolves outside workspace root" not in symlink_escape.get("content", ""):
                raise AssertionError(f"expected symlink escape rejection, got {symlink_escape}")
        if decoded[1]["functions"][0]["name"] != "hello":
            raise AssertionError(f"unexpected AST summary: {decoded[1]}")
        if decoded[2]["value"] != "demo":
            raise AssertionError(f"unexpected JSON query: {decoded[2]}")
        if decoded[3]["magic_ascii"] != "MZ..Hello.World":
            raise AssertionError(f"unexpected binary info: {decoded[3]}")
        if [item["value"] for item in decoded[4]["strings"]] != ["Hello", "World"]:
            raise AssertionError(f"unexpected strings: {decoded[4]}")
        if decoded[5]["lines"][0]["offset"] != 0:
            raise AssertionError(f"unexpected hex preview: {decoded[5]}")

    print("python_notes MCP validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
