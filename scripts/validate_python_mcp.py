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


def sample_pe_bytes() -> bytes:
    data = bytearray(2048)
    data[0:2] = b"MZ"
    data[0x3C:0x40] = (0x80).to_bytes(4, "little")
    data[0x80:0x84] = b"PE\x00\x00"
    data[0x84:0x86] = (0x8664).to_bytes(2, "little")
    data[0x86:0x88] = (1).to_bytes(2, "little")
    data[0x94:0x96] = (0xF0).to_bytes(2, "little")
    optional = 0x98
    data[optional:optional + 2] = (0x20B).to_bytes(2, "little")
    data[optional + 16:optional + 20] = (0x1000).to_bytes(4, "little")
    section = optional + 0xF0
    data[section:section + 8] = b".text\x00\x00\x00"
    data[section + 8:section + 12] = (0x200).to_bytes(4, "little")
    data[section + 12:section + 16] = (0x1000).to_bytes(4, "little")
    data[section + 16:section + 20] = (0x200).to_bytes(4, "little")
    data[section + 20:section + 24] = (0x400).to_bytes(4, "little")
    data[section + 36:section + 40] = (0x60000020).to_bytes(4, "little")
    payload = b"CreateRemoteThread\x00https://example.test\x00api_key=SECRET123456\x00kernel32.dll\x00"
    data[0x400:0x400 + len(payload)] = payload
    return bytes(data)


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
        (root / "sample-pe.bin").write_bytes(sample_pe_bytes())
        (root / "guide.md").write_text(
            "# Guide\n\nSee [Setup](#setup), [Missing](missing.md), and [External](https://example.test).\n\n## Setup\nReady.\n",
            encoding="utf-8",
        )
        (root / "ticket.json").write_text(
            json.dumps(
                {
                    "ticket_id": "CASE-1",
                    "product_area": "api",
                    "priority": "P1",
                    "customer_impact": "Enterprise customer cannot call the API.",
                    "message": "User alice@example.com saw token=SECRET123456 during login.",
                    "internal_notes": "Possible security escalation.",
                }
            ),
            encoding="utf-8",
        )
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
            request(6, "binary_format_summary", {"path": "sample-pe.bin"}),
            request(7, "binary_entropy_map", {"path": "sample-pe.bin", "block_size": 512, "max_blocks": 4}),
            request(8, "binary_symbol_hints", {"path": "sample-pe.bin", "max_results": 10}),
            request(9, "binary_extract_window", {"path": "sample-pe.bin", "offset": 1024, "length": 32, "encoding": "hex"}),
            request(10, "markdown_link_check", {"path": "guide.md"}),
            request(11, "support_case_summary", {"path": "ticket.json"}),
            request(12, "redaction_check", {"path": "ticket.json"}),
            request(13, "read_note", {"path": "../outside.txt"}),
        ]
        if symlink_supported:
            request_lines.append(request(14, "read_note", {"path": "outside-link.txt"}))
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
        expected_lines = 14 if symlink_supported else 13
        if len(lines) != expected_lines:
            raise AssertionError(f"expected {expected_lines} responses, got {len(lines)}: {proc.stdout}")

        decoded = {}
        for line in lines[:12]:
            req_id, payload = decode_response(line)
            decoded[req_id] = payload

        escape = json.loads(lines[12])["result"]
        if not escape.get("is_error") or "escapes workspace root" not in escape.get("content", ""):
            raise AssertionError(f"expected workspace escape rejection, got {escape}")
        if symlink_supported:
            symlink_escape = json.loads(lines[13])["result"]
            symlink_content = symlink_escape.get("content", "")
            if not symlink_escape.get("is_error") or (
                "resolves outside workspace root" not in symlink_content
                and "escapes workspace root" not in symlink_content
            ):
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
        if decoded[6]["format"] != "pe" or decoded[6]["architecture"] != "x86_64":
            raise AssertionError(f"unexpected PE summary: {decoded[6]}")
        if not decoded[7]["blocks"] or decoded[7]["blocks_analyzed"] != 4:
            raise AssertionError(f"unexpected entropy map: {decoded[7]}")
        suspicious_values = [item["value"] for item in decoded[8]["suspicious_apis"]]
        if "CreateRemoteThread" not in suspicious_values:
            raise AssertionError(f"unexpected symbol hints: {decoded[8]}")
        if decoded[9]["offset"] != 1024 or not decoded[9]["bounded_artifact"]:
            raise AssertionError(f"unexpected extract window: {decoded[9]}")
        if decoded[10]["broken_count"] != 1 or decoded[10]["publish_ready"]:
            raise AssertionError(f"unexpected markdown link check: {decoded[10]}")
        if decoded[11]["priority"] != "P1" or not decoded[11]["needs_escalation"]:
            raise AssertionError(f"unexpected support summary: {decoded[11]}")
        if not decoded[12]["redaction_required"] or decoded[12]["finding_count"] < 2:
            raise AssertionError(f"unexpected redaction check: {decoded[12]}")

    print("python_notes MCP validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
