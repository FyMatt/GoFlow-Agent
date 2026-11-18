#!/usr/bin/env python3
"""Run the focused code-fix Golden Scenario through the browser.

The scenario uses the real OpenAI-compatible Go provider adapter and the real
file/command MCP servers. The model responses are deterministic and served by
an in-process HTTP endpoint so the check does not require an external API key.
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
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server


ROOT = Path(__file__).resolve().parents[1]
ORIGINAL_CALC = "package calc\n\nfunc Add(left, right int) int {\n\treturn left - right\n}\n"
EXPECTED_SHA256 = hashlib.sha256(ORIGINAL_CALC.encode("utf-8")).hexdigest()


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


class ScenarioProvider(BaseHTTPRequestHandler):
    workspace: Path
    requests: list[dict]

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
        messages = payload.get("messages") or []
        tool_messages = [item for item in messages if item.get("role") == "tool"]
        last_tool = str(tool_messages[-1].get("name", "")) if tool_messages else ""
        if not tool_messages:
            response = self._tool_call("call-read", "file_tools/read_file", {"path": "calc.go"})
        elif last_tool.endswith("read_file"):
            response = self._tool_call(
                "call-patch",
                "file_tools/apply_patch",
                {
                    "path": "calc.go",
                    "expected_sha256": EXPECTED_SHA256,
                    "edits": [{"old_text": "return left - right", "new_text": "return left + right"}],
                },
            )
        elif last_tool.endswith("apply_patch"):
            response = self._tool_call(
                "call-test",
                "command_tools/run_command",
                {"program": "go", "args": ["test", "./..."], "cwd": ".", "timeout_seconds": 60},
            )
        else:
            response = self._text_response("Fixed calc.go and verified it with `go test ./...`.")
        self._write_sse(response)

    @staticmethod
    def _tool_call(call_id: str, name: str, arguments: dict) -> dict:
        return {
            "choices": [
                {
                    "delta": {
                        "role": "assistant",
                        "tool_calls": [
                            {
                                "index": 0,
                                "id": call_id,
                                "type": "function",
                                "function": {"name": name, "arguments": json.dumps(arguments, separators=(",", ":"))},
                            }
                        ],
                    },
                    "finish_reason": "tool_calls",
                }
            ]
        }

    @staticmethod
    def _text_response(content: str) -> dict:
        return {
            "choices": [
                {"delta": {"role": "assistant", "content": content}, "finish_reason": None},
                {"delta": {}, "finish_reason": "stop"},
            ]
        }

    def _write_sse(self, chunks: dict) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for choice in chunks["choices"]:
            body = json.dumps({"choices": [choice], "usage": {"prompt_tokens": 32, "completion_tokens": 8}})
            self.wfile.write(f"data: {body}\n\n".encode("utf-8"))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def write_workspace(workspace: Path) -> None:
    (workspace / "go.mod").write_bytes(b"module golden.local/calc\n\ngo 1.20\n")
    (workspace / "calc.go").write_bytes(ORIGINAL_CALC.encode("utf-8"))
    (workspace / "calc_test.go").write_bytes(
        "package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"Add should sum values\")\n\t}\n}\n".encode("utf-8")
    )
    run_checked(["git", "init"], workspace)
    run_checked(["git", "config", "user.email", "golden@example.invalid"], workspace)
    run_checked(["git", "config", "user.name", "GoFlow Golden Scenario"], workspace)
    run_checked(["git", "add", "go.mod", "calc.go", "calc_test.go"], workspace)
    run_checked(["git", "commit", "-m", "initial failing fixture"], workspace)


def write_config(runtime_home: Path, workspace: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    skill_dir = runtime_home / "skills"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir = skill_dir / "golden"
    skill_dir.mkdir(parents=True, exist_ok=True)
    (skill_dir / "SKILL.md").write_text(
        "---\nname: golden\ndescription: Focused local code fix\n---\nInspect, patch, test, and report evidence.\n",
        encoding="utf-8",
    )
    repo = ROOT.as_posix()
    file_server = (ROOT / "mcp_servers" / "file_tools").as_posix()
    command_server = (ROOT / "mcp_servers" / "command_tools").as_posix()
    config = f"""
agent:
  name: GoFlow Golden Scenario
  max_iterations: 8
  timeout: 2m
default_agent: software-engineer
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider_port}/v1
    api_key: golden-key
    model: golden-model
    timeout: 30s
    temperature: 0
    max_tokens: 256
    retry_count: 0
agents:
  software-engineer:
    kind: agent
    name: Software Engineer
    description: Focused code fixer for the Golden Scenario.
    provider: primary
    mode: fix
    tool_policy: allow
    allowed_tool_kinds: [read, write, exec]
    max_iterations: 8
skill:
  directory: {yaml_quote(str(skill_dir.parent))}
  match_threshold: 1
  hot_reload: false
audit:
  enabled: true
  redact_content: false
verifier:
  enabled: false
session:
  max_history: 8
mcp_servers:
  - name: file_tools
    command: go
    args: [run, {yaml_quote(file_server)}]
    enabled: true
    timeout: 30s
    workdir: {yaml_quote(repo)}
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 2097152
  - name: command_tools
    command: go
    args: [run, {yaml_quote(command_server)}]
    enabled: true
    timeout: 2m
    workdir: {yaml_quote(repo)}
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE, GOFLOW_COMMAND_ARTIFACT_RETENTION_HOURS, GOFLOW_COMMAND_ARTIFACT_MAX_FILES]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 524288
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def run_checked(command: list[str], cwd: Path, env: dict[str, str] | None = None) -> None:
    completed = subprocess.run(command, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if completed.returncode != 0:
        raise RuntimeError(f"{' '.join(command)} failed with {completed.returncode}\n{completed.stdout}")


def request_json(url: str, payload: dict | None = None, timeout: float = 5.0) -> dict:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"} if data else {})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_for_terminal(base_url: str, run_id: str, timeout: float) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        detail = request_json(f"{base_url}/api/runs/{run_id}?full=1")
        if str(detail.get("status", "")).lower() in {"succeeded", "completed", "failed", "cancelled"}:
            return detail
        time.sleep(0.4)
    raise RuntimeError(f"run {run_id} did not reach a terminal state")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", type=Path, default=None)
    parser.add_argument("--required", action="store_true")
    parser.add_argument("--timeout", type=float, default=90.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("golden scenario skipped: no Chrome/Edge/Chromium binary found")
        return 0
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-golden-", dir=ROOT / ".artifacts"))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    workspace.mkdir(parents=True)
    runtime_home.mkdir(parents=True)
    provider_port = free_port()
    http_port = free_port()
    provider = ScenarioProvider
    provider.workspace = workspace
    provider.requests = []
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    provider_thread = threading.Thread(target=provider_server.serve_forever, daemon=True)
    provider_thread.start()
    app_process: subprocess.Popen[str] | None = None
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-golden-browser-"))
    try:
        write_workspace(workspace)
        config_path = write_config(runtime_home, workspace, provider_port)
        binary = temp_root / ("goflow-golden.exe" if os.name == "nt" else "goflow-golden")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)
        app_process = subprocess.Popen(
            [str(binary), "--config", str(config_path), "--workspace", str(workspace), "--http", f":{http_port}"],
            cwd=ROOT,
            env=build_env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        base_url = f"http://127.0.0.1:{http_port}"
        wait_for_server(base_url, app_process, args.timeout)
        prompt = "Fix the failing Add test in calc.go, run the relevant test, and report the changed file and verification evidence."
        trigger = evaluate_browser_json(
            browser,
            f"{base_url}/console#playground",
            f"""
            (async () => {{
              const prompt = document.querySelector('#prompt');
              const send = document.querySelector('#send');
              prompt.value = {json.dumps(prompt)};
              prompt.dispatchEvent(new Event('input', {{bubbles: true}}));
              send.click();
              const deadline = Date.now() + {int(args.timeout * 1000)};
              while (Date.now() < deadline) {{
                const response = await fetch('/api/runs?limit=10');
                const body = await response.json();
                const run = (body.runs || [])[0];
                if (run && ['succeeded', 'completed', 'failed', 'cancelled'].includes(String(run.status || '').toLowerCase())) {{
                  return {{id: run.id, status: run.status}};
                }}
                await new Promise(resolve => setTimeout(resolve, 400));
              }}
              return {{error: 'browser task polling timed out'}};
            }})()
            """,
            args.timeout + 15,
            browser_profile,
        )
        if not trigger.get("id") or str(trigger.get("status", "")).lower() not in {"succeeded", "completed"}:
            raise RuntimeError(f"browser task did not succeed: {trigger}")
        run_id = str(trigger["id"])
        detail = wait_for_terminal(base_url, run_id, args.timeout)
        if str(detail.get("status", "")).lower() not in {"succeeded", "completed"}:
            raise RuntimeError(f"task detail is not successful: {detail}")
        # Writes execute in a task copy. Applying the verified diff is an
        # explicit delivery action so the real workspace is never modified by
        # an unapproved model/tool call.
        apply = request_json(f"{base_url}/api/agent-runs/{run_id}/apply-staged-changes", {})
        if str(apply.get("status", "")).lower() != "applied":
            raise RuntimeError(f"verified staged changes were not applied: {apply}")
        calc = (workspace / "calc.go").read_text(encoding="utf-8")
        if "return left + right" not in calc:
            raise RuntimeError(f"the Agent did not apply the expected code fix; status={detail.get('status')}, requests={len(provider.requests)}, detail={detail}")
        run_checked(["go", "test", "./..."], workspace, build_env)
        diffs = request_json(f"{base_url}/api/runs/{run_id}/diffs?include_content=true")
        if int(diffs.get("total", 0)) < 1:
            raise RuntimeError(f"run did not persist a file diff: {diffs}")
        browser_view = evaluate_browser_json(
            browser,
            f"{base_url}/console#playground",
            f"""
            (async () => {{
              const stateKey = 'goflow.playground.runState';
              const current = JSON.parse(localStorage.getItem(stateKey) || '{{}}');
              if (current.runID !== {json.dumps(run_id)} || current.mode !== 'agent') {{
                localStorage.setItem(stateKey, JSON.stringify({{
                  mode: 'agent',
                  runID: {json.dumps(run_id)},
                  status: 'completed',
                  updatedAt: Date.now()
                }}));
                location.reload();
                await new Promise(resolve => setTimeout(resolve, 2000));
              }}
              const deadline = Date.now() + 10000;
              while (Date.now() < deadline && !document.body.innerText.includes('Fixed calc.go')) {{
                await new Promise(resolve => setTimeout(resolve, 150));
              }}
              return {{
                bodyText: document.body.innerText,
                hasTimeline: Boolean(document.querySelector('.run-timeline')),
                hasTaskContext: Boolean(document.querySelector('.task-execution-context')),
                changedFilesHonest: !document.body.innerText.includes('No file changes were captured for this run.')
              }};
            }})()
            """,
            args.timeout,
            browser_profile,
        )
        if (
            "Fixed calc.go" not in str(browser_view.get("bodyText", ""))
            or not browser_view.get("hasTimeline")
            or not browser_view.get("changedFilesHonest")
        ):
            raise RuntimeError(f"Tasks page did not render final evidence: {browser_view}")
        print(json.dumps({"run_id": run_id, "provider_requests": len(provider.requests), "diffs": diffs.get("total", 0), "browser": "passed"}, indent=2))
        return 0
    finally:
        provider_server.shutdown()
        provider_server.server_close()
        if app_process is not None:
            app_process.terminate()
            try:
                app_process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                app_process.kill()
                app_process.wait(timeout=5)
        shutil.rmtree(browser_profile, ignore_errors=True)
        shutil.rmtree(temp_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
