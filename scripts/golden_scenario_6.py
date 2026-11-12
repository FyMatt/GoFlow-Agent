#!/usr/bin/env python3
"""Verify a browser-created one-shot schedule survives restart exactly once."""

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
from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server

ROOT = Path(__file__).resolve().parents[1]


class Provider(BaseHTTPRequestHandler):
    calls = 0
    lock = threading.Lock()

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        with self.lock:
            type(self).calls += 1
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for item in (
            {"choices": [{"delta": {"role": "assistant", "content": "Read-only scheduled review completed."}, "finish_reason": None}]},
            {"choices": [{"delta": {}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 12, "completion_tokens": 8}},
        ):
            self.wfile.write(("data: " + json.dumps(item) + "\n\n").encode())
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def write_config(runtime_home: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir = runtime_home / "skills" / "scheduled-review"
    skill_dir.mkdir(parents=True, exist_ok=True)
    (skill_dir / "SKILL.md").write_text("---\nname: scheduled-review\ndescription: Read-only repository review.\n---\n\nRead-only repository review.\n", encoding="utf-8")
    config = f"""
agent:
  name: GoFlow Scheduler Golden Scenario
  max_iterations: 2
  timeout: 30s
default_agent: auditor
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider_port}/v1
    api_key: golden-key
    model: golden-model
    timeout: 10s
    temperature: 0
    max_tokens: 128
    retry_count: 0
agents:
  auditor:
    kind: agent
    name: Auditor
    description: Reads and summarizes evidence.
    provider: primary
    mode: audit
    tool_policy: allow
    allowed_tool_kinds: [read]
    max_iterations: 2
skill:
  directory: {yaml_quote(str(runtime_home / 'skills'))}
  hot_reload: false
audit:
  enabled: false
verifier:
  enabled: false
session:
  max_history: 8
  persist_path: {yaml_quote(str(runtime_home / 'session.json'))}
mcp_servers: []
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def browser_create_schedule(browser: Path, base_url: str, profile: Path, run_at: str, timeout: float) -> dict:
    profile.mkdir(parents=True, exist_ok=True)
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const response = await fetch('/api/schedules', {{
            method: 'POST',
            headers: {{'Content-Type': 'application/json'}},
            body: JSON.stringify({{
              name: 'browser-once', enabled: true,
              trigger: {{kind: 'once', run_at: '{run_at}'}},
              task: {{kind: 'agent', agent_id: 'auditor', request: 'Read the repository status and summarize it.', read_only: true}}
            }})
          }});
          return {{status: response.status, body: await response.json()}};
        }})()
        """,
        timeout,
        profile,
    )


def wait_for_job(base_url: str, schedule_id: str, timeout: float) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        payload = request_json(f"{base_url}/api/schedules/{schedule_id}/jobs")
        jobs = payload.get("items", [])
        if jobs and jobs[0].get("status") in {"succeeded", "failed", "timed_out", "approval_expired"}:
            return jobs[0]
        time.sleep(0.2)
    raise AssertionError("scheduled Job did not reach a terminal state")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--required", action="store_true")
    parser.add_argument("--timeout", type=float, default=120)
    parser.add_argument("--browser", default=None)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("golden scenario 6 skipped: no browser available")
        return 0
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-golden-6-", dir=ROOT / ".artifacts"))
    process = None
    provider = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    provider_thread = threading.Thread(target=provider.serve_forever, daemon=True)
    provider_thread.start()
    try:
        runtime_home = temp_root / "runtime"
        workspace = temp_root / "workspace"
        runtime_home.mkdir()
        workspace.mkdir()
        config_path = write_config(runtime_home, provider.server_address[1])
        binary = temp_root / "goflow-scheduler-golden.exe"
        build_env = os.environ.copy()
        build_env.update({"GOCACHE": str(temp_root / "gocache"), "TMP": str(temp_root / "tmp"), "TEMP": str(temp_root / "tmp")})
        Path(build_env["TMP"]).mkdir()
        Path(build_env["TEMP"]).mkdir(exist_ok=True)
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)
        port = free_port()
        process = start_app(binary, config_path, workspace, port, build_env)
        base_url = f"http://127.0.0.1:{port}"
        wait_for_server(base_url, process, args.timeout)
        run_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 2))
        created = browser_create_schedule(browser, base_url, temp_root / "browser-1", run_at, args.timeout)
        if created.get("status") != 201:
            raise AssertionError(f"browser schedule creation failed: {created}")
        schedule_id = created["body"]["id"]
        first_job = wait_for_job(base_url, schedule_id, args.timeout)
        if first_job.get("status") != "succeeded" or not first_job.get("run_id"):
            raise AssertionError(f"one-shot schedule did not complete successfully: {first_job}")
        first_run_id = first_job["run_id"]
        first_calls = Provider.calls
        stop_app(process)
        process = None
        port = free_port()
        process = start_app(binary, config_path, workspace, port, build_env)
        base_url = f"http://127.0.0.1:{port}"
        wait_for_server(base_url, process, args.timeout)
        restarted_job = wait_for_job(base_url, schedule_id, args.timeout)
        runs = request_json(f"{base_url}/api/runs").get("runs", [])
        matching = [run for run in runs if run.get("schedule_job_id") == restarted_job.get("id")]
        if restarted_job.get("run_id") != first_run_id or len(matching) != 1 or Provider.calls != first_calls:
            raise AssertionError({"first_job": first_job, "restarted_job": restarted_job, "matching_runs": matching, "provider_calls": Provider.calls})
        print(json.dumps({"scenario": 6, "status": "passed", "schedule_id": schedule_id, "job_id": first_job["id"], "run_id": first_run_id, "provider_calls": Provider.calls}))
        return 0
    finally:
        stop_app(process)
        provider.shutdown()
        provider.server_close()
        shutil.rmtree(temp_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
