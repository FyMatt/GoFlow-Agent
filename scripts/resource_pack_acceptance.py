#!/usr/bin/env python3
"""Exercise an unknown-domain resource pack with real local MCP file I/O.

The Provider is a deterministic loopback fixture, not a paid model or a content
quality certification. All runtime, workspace and credential data is temporary.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import threading
import time
from http.server import ThreadingHTTPServer
from pathlib import Path

from golden_provider_helpers import decode_tool_result
from golden_scenario_1 import ScenarioProvider, request_json, run_checked, yaml_quote
from smoke_http_browser import free_port, wait_for_server

ROOT = Path(__file__).resolve().parents[1]
SOURCE = "地点：样地 A\n记录：第二次观察值缺少单位。\n日期未提供。\n"


class ResourceProvider(ScenarioProvider):
    requests: list[dict] = []

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        try:
            payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        except (ValueError, UnicodeDecodeError):
            self.send_error(400)
            return
        self.requests.append(payload)
        messages = [item for item in payload.get("messages", []) if item.get("role") == "tool"]
        tools = payload.get("tools") or []
        report = {"summary": "按原始记录整理", "source": "input/notes.txt", "observations": ["第二次观察值缺少单位"], "unknowns": ["日期", "单位"], "evidence_refs": ["input/notes.txt"]}
        if not tools:
            response = self._text_response("报告 deliverables/notes.json 已写入并读回；日期和单位仍未知。")
        elif not messages:
            response = self._tool_call("notes-read", "file_tools/read_file", {"path": "input/notes.txt"})
        else:
            decoded = [decode_tool_result(item.get("content"))[0] or {} for item in messages]
            wrote = any(str(item.get("name", "")).endswith("write_file") for item in messages)
            readback = any(item.get("relative_path") == "deliverables/notes.json" and "content" in item for item in decoded)
            if not wrote:
                response = self._tool_call("notes-write", "file_tools/write_file", {"path": "deliverables/notes.json", "content": json.dumps(report, ensure_ascii=False)})
            elif not readback:
                response = self._tool_call("notes-readback", "file_tools/read_file", {"path": "deliverables/notes.json"})
            else:
                response = self._text_response(json.dumps({"summary": "已整理并校验", "decision": "passed", "report_path": "deliverables/notes.json", "source": "input/notes.txt", "unknowns": report["unknowns"], "evidence_refs": report["evidence_refs"]}, ensure_ascii=False))
        self._write_sse(response)


def execute(binary: Path, timeout: float) -> dict:
    with tempfile.TemporaryDirectory(prefix="goflow-resource-pack-") as temporary:
        base = Path(temporary)
        runtime, workspace = base / "runtime", base / "workspace"
        runtime.mkdir(); (workspace / "input").mkdir(parents=True)
        (workspace / "input/notes.txt").write_text(SOURCE, encoding="utf-8")
        example = ROOT / "examples/custom-resource-pack"
        for name in ("configs", "skills", "kits", "workflows"):
            shutil.copytree(example / name, runtime / name)
        file_binary = base / ("file_tools.exe" if os.name == "nt" else "file_tools")
        run_checked(["go", "build", "-o", str(file_binary), "./mcp_servers/file_tools"], ROOT)
        ResourceProvider.requests = []
        provider = ThreadingHTTPServer(("127.0.0.1", 0), ResourceProvider)
        provider.daemon_threads = True
        thread = threading.Thread(target=provider.serve_forever, daemon=True)
        thread.start()
        port = free_port()
        config = runtime / "configs/goflow.yaml"
        config.write_text(f"""agent:
  name: Resource pack acceptance
  max_iterations: 8
  timeout: 1m
default_agent: field-notes-reporter
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider.server_port}/v1
    api_key: local-fixture
    model: local-resource-fixture
    timeout: 20s
    retry_count: 0
skill:
  directory: ./skills
  match_threshold: 1
audit:
  enabled: true
verifier:
  enabled: false
session:
  max_history: 8
mcp_servers:
  - name: file_tools
    command: {yaml_quote(str(file_binary))}
    enabled: true
    timeout: 20s
    workdir: {yaml_quote(str(runtime))}
    env_allowlist: [PATH, SystemRoot, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    allowed_commands: [{yaml_quote(str(file_binary))}]
""", encoding="utf-8")
        env = os.environ.copy()
        env["GOFLOW_DATA_ROOT"] = str(base / "data")
        env.pop("GOFLOW_HTTP_TOKEN", None)
        env.pop("GOFLOW_API_KEY", None)
        with (base / "runtime.log").open("w", encoding="utf-8") as log:
            process = subprocess.Popen([str(binary), "--config", str(config), "--workspace", str(workspace), "--http", f"127.0.0.1:{port}"], cwd=runtime, env=env, stdout=log, stderr=subprocess.STDOUT)
            try:
                url = f"http://127.0.0.1:{port}"
                wait_for_server(url, process, min(timeout, 45))
                task = "田野笔记整理：读取 input/notes.txt，写入 deliverables/notes.json 并读回。日期和单位标为未知，不修改输入。"
                preflight = request_json(url + "/api/runs/preflight", {"type": "auto", "input": task}, timeout=30)
                if not preflight.get("valid") or preflight.get("target") != "field-notes-review" or preflight.get("domain") != "field-notes":
                    raise AssertionError(f"unknown-domain preflight failed: {preflight}")
                accepted = request_json(url + "/api/runs", {"type": "auto", "input": task, "approve": True, "background": True, "permission_mode": "full"}, timeout=30)
                run_id = accepted.get("run_id") or accepted.get("id")
                if not run_id:
                    raise AssertionError(f"run was not accepted: {accepted}")
                deadline = time.monotonic() + timeout
                while time.monotonic() < deadline:
                    workbench = request_json(url + f"/api/runs/{run_id}?view=workbench", timeout=15)
                    status = (workbench.get("run") or {}).get("status")
                    if status in {"completed", "blocked", "failed", "cancelled", "denied", "paused_need_more_budget"}:
                        break
                    time.sleep(0.2)
                else:
                    raise AssertionError("resource workflow timed out")
                if status != "completed":
                    raise AssertionError(f"resource workflow did not complete: {workbench.get('execution')} {workbench.get('final_output')}")
                output = json.loads((workspace / "deliverables/notes.json").read_text(encoding="utf-8"))
                if output.get("unknowns") != ["日期", "单位"] or output.get("source") != "input/notes.txt":
                    raise AssertionError(f"unexpected report: {output}")
                if (workspace / "input/notes.txt").read_text(encoding="utf-8") != SOURCE:
                    raise AssertionError("source file was modified")
                full = request_json(url + f"/api/workflow-runs/{run_id}", timeout=15)
                pins = full.get("resource_pins") or []
                if not any(item.get("kind") == "kit" and item.get("name") == "field-notes-kit" for item in pins):
                    raise AssertionError(f"selected Kit was not pinned: {pins}")
                tool_names = [result.get("tool_name", "") for stage in full.get("completed_stages", []) for result in (stage.get("result") or {}).get("tool_results", [])]
                if not any(name.endswith("write_file") for name in tool_names) or sum(name.endswith("read_file") for name in tool_names) < 2:
                    raise AssertionError(f"real read/write/readback evidence missing: {tool_names}")
                return {"passed": True, "mode": "deterministic_local_provider", "domain": preflight["domain"], "workflow": preflight["target"], "run_id": run_id, "status": status, "source_sha256": hashlib.sha256(SOURCE.encode()).hexdigest(), "report": output, "tools": tool_names, "provider_requests": len(ResourceProvider.requests), "real_model_quality_verified": False}
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait(timeout=5)
                provider.shutdown(); provider.server_close(); thread.join(timeout=5)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=90)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    report = execute(args.binary.resolve(), args.timeout)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
