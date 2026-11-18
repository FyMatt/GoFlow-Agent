#!/usr/bin/env python3
"""Run the read-only local-host diagnostics Golden Scenario in the browser."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from golden_scenario_2 import request_json, run_checked, start_app, stop_app, yaml_quote
from golden_provider_helpers import UNTRUSTED_TOOL_DATA_MARKER, decode_tool_result
from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server


ROOT = Path(__file__).resolve().parents[1]
FINAL_REPORT = (
    "Local host diagnostic complete. Target local was inspected with read-only tools. "
    "Host facts and disk capacity evidence were collected; no system or workspace changes were made."
)
class ScenarioProvider(BaseHTTPRequestHandler):
    requests: list[dict]
    tool_results: list[dict]
    protected_tool_results: int

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            self.send_error(400)
            return
        self.requests.append(payload)
        tool_messages = [message for message in payload.get("messages") or [] if message.get("role") == "tool"]
        decoded_results: list[dict] = []
        protected_results = 0
        for message in tool_messages:
            decoded, protected = decode_tool_result(message.get("content"))
            if protected:
                protected_results += 1
            if decoded is not None:
                decoded_results.append(decoded)
        type(self).tool_results = decoded_results
        type(self).protected_tool_results = protected_results
        if len(tool_messages) == 0:
            response = self._tool_call("host-facts", "host_tools/host_facts", {})
        elif len(tool_messages) == 1:
            response = self._tool_call("disk-usage", "host_tools/disk_usage", {"limit": 20})
        else:
            response = self._text_response(FINAL_REPORT)
        self._write_sse(response)

    @staticmethod
    def _tool_call(call_id: str, name: str, arguments: dict) -> list[dict]:
        return [{
            "delta": {
                "role": "assistant",
                "tool_calls": [{
                    "index": 0,
                    "id": call_id,
                    "type": "function",
                    "function": {"name": name, "arguments": json.dumps(arguments, separators=(",", ":"))},
                }],
            },
            "finish_reason": "tool_calls",
        }]

    @staticmethod
    def _text_response(content: str) -> list[dict]:
        return [
            {"delta": {"role": "assistant", "content": content}, "finish_reason": None},
            {"delta": {}, "finish_reason": "stop"},
        ]

    def _write_sse(self, choices: list[dict]) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for choice in choices:
            body = json.dumps({"choices": [choice], "usage": {"prompt_tokens": 48, "completion_tokens": 16}})
            self.wfile.write(f"data: {body}\n\n".encode("utf-8"))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def write_workspace(workspace: Path) -> None:
    (workspace / "README.md").write_text("# Local host diagnostics fixture\n", encoding="utf-8")


def write_config(runtime_home: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    skill_dir = runtime_home / "skills" / "local-host-diagnostics"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(ROOT / "skills" / "local-host-diagnostics" / "SKILL.md", skill_dir / "SKILL.md")
    host_server = (ROOT / "mcp_servers" / "host_tools").as_posix()
    config = f"""
agent:
  name: GoFlow Local Host Diagnostics Golden Scenario
  max_iterations: 6
  timeout: 2m
default_agent: operations-specialist
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider_port}/v1
    api_key: golden-key
    model: golden-model
    timeout: 30s
    temperature: 0
    max_tokens: 384
    retry_count: 0
agents:
  operations-specialist:
    kind: agent
    name: Operations Specialist
    description: Read-only local host diagnostics.
    provider: primary
    mode: audit
    tool_policy: allow
    allowed_tool_kinds: [read]
    allowed_tools:
      - host_tools/host_facts
      - host_tools/disk_usage
      - host_tools/list_ports
      - host_tools/list_processes
      - host_tools/service_status
      - host_tools/read_log_tail
    max_iterations: 6
skill:
  directory: {yaml_quote(str(runtime_home / 'skills'))}
  match_threshold: 1
  hot_reload: false
audit:
  enabled: true
  redact_content: false
verifier:
  enabled: false
session:
  max_history: 8
  persist_path: {yaml_quote(str(runtime_home / 'session.json'))}
mcp_servers:
  - name: host_tools
    target_scope: host
    command: go
    args: [run, {yaml_quote(host_server)}]
    enabled: true
    timeout: 30s
    workdir: {yaml_quote(str(ROOT))}
    env_allowlist: [PATH, HOME, USERPROFILE, SystemRoot, ProgramData, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE, GOFLOW_TARGET_ID, GOFLOW_HOST_LOG_ROOTS]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 2097152
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def wait_for_agent_run(base_url: str, run_id: str, timeout: float) -> dict:
    deadline = time.time() + timeout
    last: dict = {}
    while time.time() < deadline:
        last = request_json(f"{base_url}/api/runs/{run_id}?full=1")  # type: ignore[assignment]
        if str(last.get("status", "")).lower() in {"succeeded", "completed", "failed", "cancelled"}:
            return last
        time.sleep(0.4)
    raise RuntimeError(f"Agent run {run_id} did not finish; last={last}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", type=Path, default=None)
    parser.add_argument("--required", action="store_true")
    parser.add_argument("--timeout", type=float, default=120.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("golden scenario skipped: no Chrome/Edge/Chromium binary found")
        return 0
    artifacts = ROOT / ".artifacts"
    artifacts.mkdir(exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-host-golden-", dir=artifacts))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    workspace.mkdir()
    runtime_home.mkdir()
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-host-golden-browser-"))
    provider_port = free_port()
    app_port = free_port()
    provider = ScenarioProvider
    provider.requests = []
    provider.tool_results = []
    provider.protected_tool_results = 0
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    threading.Thread(target=provider_server.serve_forever, daemon=True).start()
    app_process: subprocess.Popen[str] | None = None
    try:
        write_workspace(workspace)
        config_path = write_config(runtime_home, provider_port)
        binary = temp_root / ("goflow-host-golden.exe" if os.name == "nt" else "goflow-host-golden")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)
        app_process = start_app(binary, config_path, workspace, app_port, build_env)
        base_url = f"http://127.0.0.1:{app_port}"
        wait_for_server(base_url, app_process, args.timeout)
        browser_result = evaluate_browser_json(
            browser,
            f"{base_url}/console#playground",
            f"""
            (async () => {{
              const agent = document.querySelector('#agentSelect');
              const skill = document.querySelector('#skillSelect');
              const prompt = document.querySelector('#prompt');
              const send = document.querySelector('#send');
              agent.value = 'operations-specialist';
              agent.dispatchEvent(new Event('change', {{bubbles: true}}));
              skill.value = 'local-host-diagnostics';
              skill.dispatchEvent(new Event('change', {{bubbles: true}}));
              await new Promise(resolve => setTimeout(resolve, 250));
              const contextBeforeSend = document.querySelector('#taskExecutionContext')?.innerText || '';
              prompt.value = 'Inspect this local machine and summarize host facts and disk capacity using read-only evidence.';
              prompt.dispatchEvent(new Event('input', {{bubbles: true}}));
              send.click();
              const deadline = Date.now() + {int(args.timeout * 1000)};
              let run = null;
              while (Date.now() < deadline) {{
                const response = await fetch('/api/runs?type=agent&limit=10');
                const body = await response.json();
                run = (body.runs || [])[0] || null;
                if (run && ['succeeded', 'completed', 'failed', 'cancelled'].includes(String(run.status || '').toLowerCase())) break;
                await new Promise(resolve => setTimeout(resolve, 400));
              }}
              await new Promise(resolve => setTimeout(resolve, 2500));
              return {{
                id: run?.id || '',
                status: run?.status || '',
                targetID: run?.target_id || '',
                contextBeforeSend,
                bodyText: document.body.innerText,
                timelineText: document.querySelector('#messages')?.innerText || '',
                historyText: document.querySelector('#runHistoryList')?.innerText || ''
              }};
            }})()
            """,
            args.timeout + 20,
            browser_profile,
            screenshot_path=artifacts / "golden-scenario-4-tasks.png",
        )
        if not browser_result.get("id") or str(browser_result.get("status", "")).lower() not in {"succeeded", "completed"}:
            raise RuntimeError(f"browser task did not succeed: {browser_result}")
        context_before = str(browser_result.get("contextBeforeSend", ""))
        context_before_lower = context_before.lower()
        if "target" not in context_before_lower or "local machine" not in context_before_lower:
            raise RuntimeError(f"host Skill did not expose the local Target before send: {context_before!r}")
        run_id = str(browser_result["id"])
        detail = wait_for_agent_run(base_url, run_id, args.timeout)
        if detail.get("target_id") != "local":
            raise RuntimeError(f"Agent run did not persist target_id=local: {detail}")
        tool_results = provider.tool_results
        if len(tool_results) != 2:
            request_summary = []
            for request in provider.requests:
                messages = request.get("messages") or []
                request_summary.append({
                    "tool_names": [item.get("function", {}).get("name", "") for item in request.get("tools", []) if isinstance(item, dict)],
                    "roles": [item.get("role", "") for item in messages if isinstance(item, dict)],
                    "last_content": str(messages[-1].get("content", ""))[-500:] if messages and isinstance(messages[-1], dict) else "",
                })
            raise RuntimeError(
                f"Provider did not receive both host tool results: {tool_results}; "
                f"requests={request_summary}; run={detail}"
            )
        if provider.protected_tool_results != 2:
            raise RuntimeError(
                "Provider did not receive both host tool results through the untrusted-data guard: "
                f"protected={provider.protected_tool_results}; results={tool_results}"
            )
        for result in tool_results:
            if result.get("target_id") != "local" or result.get("read_only") is not True:
                raise RuntimeError(f"host tool result lacks local read-only evidence: {result}")
        timeline = request_json(f"{base_url}/api/runs/{run_id}/timeline?limit=100&include_content=true")
        items = timeline.get("items", timeline) if isinstance(timeline, dict) else timeline
        tool_names = {str(item.get("tool_name", "")) for item in items if isinstance(item, dict)}
        for expected in {"host_facts", "disk_usage"}:
            if not any(name == expected or name.endswith("/" + expected) for name in tool_names):
                raise RuntimeError(f"timeline lacks {expected}: {items}")
        diffs = request_json(f"{base_url}/api/runs/{run_id}/diffs?include_content=true")
        if int(diffs.get("total", 0)) != 0:  # type: ignore[union-attr]
            raise RuntimeError(f"read-only host task recorded workspace diffs: {diffs}")
        page_text = str(browser_result.get("bodyText", ""))
        if FINAL_REPORT not in page_text:
            raise RuntimeError(f"Tasks page did not render the final diagnostic: {page_text[-2000:]}")
        history_text = str(browser_result.get("historyText", ""))
        if "Running" in history_text or "Cancel run" in history_text:
            raise RuntimeError(f"completed Agent run remained stale in Tasks history: {history_text}")
        mobile = evaluate_browser_json(
            browser,
            f"{base_url}/console#playground",
            """
            (async () => {
              const agent = document.querySelector('#agentSelect');
              const skill = document.querySelector('#skillSelect');
              agent.value = 'operations-specialist';
              agent.dispatchEvent(new Event('change', {bubbles: true}));
              skill.value = 'local-host-diagnostics';
              skill.dispatchEvent(new Event('change', {bubbles: true}));
              await new Promise(resolve => setTimeout(resolve, 300));
              return {
                viewportWidth: window.innerWidth,
                documentWidth: document.documentElement.scrollWidth,
                contextText: document.querySelector('#taskExecutionContext')?.innerText || ''
              };
            })()
            """,
            args.timeout,
            browser_profile,
            viewport=(390, 844),
            screenshot_path=artifacts / "golden-scenario-4-tasks-mobile.png",
        )
        if int(mobile.get("documentWidth", 0)) > int(mobile.get("viewportWidth", 0)) + 1:
            raise RuntimeError(f"mobile Tasks page has horizontal overflow: {mobile}")
        if "local machine" not in str(mobile.get("contextText", "")).lower():
            raise RuntimeError(f"mobile host context does not show the local Target: {mobile}")
        print(json.dumps({
            "run_id": run_id,
            "target_id": detail.get("target_id"),
            "tool_calls": sorted(tool_names),
            "provider_requests": len(provider.requests),
            "diffs": 0,
            "browser": "passed",
            "mobile": "passed",
        }, indent=2))
        return 0
    finally:
        provider_server.shutdown()
        provider_server.server_close()
        stop_app(app_process)
        shutil.rmtree(browser_profile, ignore_errors=True)
        shutil.rmtree(temp_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
