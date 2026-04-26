#!/usr/bin/env python3
"""Example GoFlow MCP server that reports workspace inventory."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


WORKSPACE_ROOT = Path(os.environ.get("GOFLOW_WORKSPACE_ROOT", os.getcwd())).resolve()
SKIP_DIRS = {".git", ".goflow", "__pycache__", ".pytest_cache", "node_modules", ".venv", "venv"}


def respond(req_id: Any, result: dict[str, Any] | None = None, error: str | None = None) -> str:
    if error:
        payload = {"content": error, "is_error": True}
    else:
        payload = {"content": json.dumps(result or {}, ensure_ascii=False), "is_error": False}
    return json.dumps({"jsonrpc": "2.0", "id": req_id, "result": payload}, ensure_ascii=False)


def tools_list() -> dict[str, Any]:
    return {
        "tools": [
            {
                "name": "workspace_summary",
                "description": "Summarize workspace files, extensions, and largest files.",
                "kind": "read",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "max_files": {
                            "type": "integer",
                            "description": "Maximum number of files to include in the sample.",
                            "minimum": 1,
                            "maximum": 500,
                        }
                    },
                    "required": [],
                    "additionalProperties": False,
                },
            }
        ]
    }


def workspace_summary(arguments: dict[str, Any]) -> dict[str, Any]:
    max_files = int(arguments.get("max_files", 200))
    max_files = max(1, min(max_files, 500))
    files: list[dict[str, Any]] = []
    largest_candidates: list[dict[str, Any]] = []
    extensions: dict[str, int] = {}
    total_bytes = 0

    for current, dirs, names in os.walk(WORKSPACE_ROOT):
        dirs[:] = [name for name in dirs if name not in SKIP_DIRS]
        for name in sorted(names):
            resolved = (Path(current) / name).resolve()
            try:
                resolved.relative_to(WORKSPACE_ROOT)
            except ValueError:
                continue
            if not resolved.is_file():
                continue
            rel = resolved.relative_to(WORKSPACE_ROOT).as_posix()
            try:
                size = resolved.stat().st_size
            except OSError:
                continue
            total_bytes += size
            suffix = resolved.suffix.lower() or "<none>"
            extensions[suffix] = extensions.get(suffix, 0) + 1
            item = {"path": rel, "bytes": size}
            largest_candidates.append(item)
            if len(files) < max_files:
                files.append(item)

    largest = sorted(largest_candidates, key=lambda item: item["bytes"], reverse=True)[:10]
    top_extensions = [
        {"extension": ext, "count": count}
        for ext, count in sorted(extensions.items(), key=lambda item: (-item[1], item[0]))[:20]
    ]
    return {
        "workspace_root": str(WORKSPACE_ROOT),
        "sampled_files": len(files),
        "total_files_seen": sum(extensions.values()),
        "total_bytes_seen": total_bytes,
        "top_extensions": top_extensions,
        "largest_files": largest,
        "files": files,
    }


def handle(payload: dict[str, Any]) -> str:
    req_id = payload.get("id")
    method = payload.get("method")
    if method == "tools/list":
        return json.dumps({"jsonrpc": "2.0", "id": req_id, "result": tools_list()}, ensure_ascii=False)
    if method != "tools/call":
        return respond(req_id, error=f"unsupported method: {method}")

    params = payload.get("params") or {}
    name = params.get("name")
    arguments = params.get("arguments") or {}
    if not isinstance(arguments, dict):
        return respond(req_id, error="arguments must be an object")
    if name == "workspace_summary":
        return respond(req_id, workspace_summary(arguments))
    return respond(req_id, error=f"unknown tool: {name}")


def main() -> int:
    for line in os.sys.stdin:
        if not line.strip():
            continue
        try:
            payload = json.loads(line)
            print(handle(payload), flush=True)
        except Exception as exc:  # Keep stdio alive for recoverable request errors.
            print(respond(None, error=str(exc)), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
