#!/usr/bin/env python3
"""Run the restart-safe repository-readiness Golden Scenario in the browser."""

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
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from golden_scenario_2 import request_json, run_checked, start_app, stop_app, wait_for_status, yaml_quote
from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server


ROOT = Path(__file__).resolve().parents[1]
BRANCH_STAGES = {"build-review", "test-review", "configuration-review"}


class ScenarioProvider(BaseHTTPRequestHandler):
    requests: list[dict]
    stage_requests: dict[str, int]
    active_branch_requests = 0
    max_active_branch_requests = 0
    lock = threading.Lock()

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
        stage = self._stage(payload)
        is_branch = stage in BRANCH_STAGES
        with self.lock:
            self.requests.append(payload)
            self.stage_requests[stage] = self.stage_requests.get(stage, 0) + 1
            if is_branch:
                type(self).active_branch_requests += 1
                type(self).max_active_branch_requests = max(
                    type(self).max_active_branch_requests,
                    type(self).active_branch_requests,
                )
        try:
            if is_branch:
                time.sleep(0.35)
            self._write_sse(self._response(stage, payload))
        finally:
            if is_branch:
                with self.lock:
                    type(self).active_branch_requests -= 1

    @staticmethod
    def _stage(payload: dict) -> str:
        text = json.dumps(payload.get("messages") or [], ensure_ascii=False)
        checks = [
            ("Readiness Decision", "report"),
            ("Configuration Readiness", "configuration-review"),
            ("Test Readiness", "test-review"),
            ("Build Readiness", "build-review"),
            ("Repository Scope", "inventory"),
        ]
        for marker, stage in checks:
            if marker in text:
                return stage
        return "unknown"

    def _response(self, stage: str, payload: dict) -> list[dict]:
        messages = payload.get("messages") or []
        tool_messages = [message for message in messages if message.get("role") == "tool"]
        tool_names = [str(message.get("name", "")) for message in tool_messages]
        if stage == "inventory":
            if not tool_messages:
                return self._tool_call(
                    "inventory-git-status",
                    "command_tools/run_command",
                    {"program": "git", "args": ["status", "--short"], "cwd": ".", "timeout_seconds": 30},
                )
            return self._text_response(
                "Repository Scope: Go module, CI workflow, application configuration, and documentation\n"
                "Instructions: preserve the repository and run read-only checks only\n"
                "Git State: clean tracked workspace with no untracked files\n"
                "Build Commands: go build ./...\n"
                "Test Commands: go test ./...\n"
                "Configuration Checks: inspect go.mod and .github/workflows/ci.yml\n"
                "Review Limits: no writes, formatting, generation, dependency changes, or secret values\n"
                "Evidence Plan: retain exact build, test, configuration, Git, and no-change evidence"
            )
        if stage == "build-review":
            if not tool_messages:
                return self._go_command("build-go", "build")
            return self._text_response(
                "Build Readiness: pass\n"
                "Commands Run: go build ./...\n"
                "Toolchain: local Go toolchain from the trusted workspace\n"
                "Results: all packages compiled successfully\n"
                "Failure Evidence: none\n"
                "Risks: generated artifacts were not requested or retained"
            )
        if stage == "test-review":
            if not tool_messages:
                return self._go_command("test-go", "test")
            return self._text_response(
                "Test Readiness: pass\n"
                "Commands Run: go test ./...\n"
                "Coverage: greeting package behavior and project test execution\n"
                "Results: all tests passed\n"
                "Failure Evidence: none\n"
                "Risks: fixture has focused unit coverage only"
            )
        if stage == "configuration-review":
            reads = sum(name.endswith("read_file") for name in tool_names)
            if reads == 0:
                return self._tool_call("config-read-module", "file_tools/read_file", {"path": "go.mod"})
            if reads == 1:
                return self._tool_call(
                    "config-read-ci",
                    "file_tools/read_file",
                    {"path": ".github/workflows/ci.yml"},
                )
            return self._text_response(
                "Configuration Readiness: pass\n"
                "Files Reviewed: go.mod and .github/workflows/ci.yml\n"
                "Findings: module version and CI build/test commands are aligned\n"
                "Secret Safety: no credential values were read or emitted\n"
                "Deployment Risks: CI covers one current Go toolchain\n"
                "Recommendation: ready for the fixture scope"
            )
        if stage == "report":
            return self._text_response(
                "Readiness Decision: ready\n"
                "Build Evidence: go build ./... passed in the build branch\n"
                "Test Evidence: go test ./... passed in the test branch\n"
                "Configuration Evidence: module and CI workflow are aligned\n"
                "Blocking Findings: none\n"
                "Residual Risks: focused fixture and single CI toolchain\n"
                "No-Change Confirmation: repository hashes and Git status remained unchanged\n"
                "Artifact References: readiness-inventory, build-readiness-report, test-readiness-report, configuration-readiness-report, repository-readiness-report"
            )
        return self._text_response("Unknown stage: blocked")

    @staticmethod
    def _go_command(call_id: str, command: str) -> list[dict]:
        return ScenarioProvider._tool_call(
            call_id,
            "command_tools/run_command",
            {
                "program": "go",
                "args": [command, "./..."],
                "cwd": ".",
                "timeout_seconds": 60,
                "env_refs": ["GOCACHE"],
            },
        )

    @staticmethod
    def _tool_call(call_id: str, name: str, arguments: dict) -> list[dict]:
        return [
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
            body = json.dumps({"choices": [choice], "usage": {"prompt_tokens": 64, "completion_tokens": 24}})
            self.wfile.write(f"data: {body}\n\n".encode("utf-8"))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def write_workspace(workspace: Path) -> None:
    files = {
        "go.mod": b"module golden.local/readiness\n\ngo 1.20\n",
        "main.go": b'package greeting\n\nfunc Message() string { return "ready" }\n',
        "main_test.go": b'package greeting\n\nimport "testing"\n\nfunc TestMessage(t *testing.T) {\n\tif Message() != "ready" { t.Fatal("unexpected message") }\n}\n',
        "README.md": b"# Readiness fixture\n\nRun `go build ./...` and `go test ./...`.\n",
        ".gitignore": b".env\n*.local\n.goflow/\n",
        ".github/workflows/ci.yml": b"name: ci\non: [push]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - uses: actions/setup-go@v5\n        with:\n          go-version: '1.20'\n      - run: go build ./...\n      - run: go test ./...\n",
    }
    for relative, content in files.items():
        path = workspace / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    run_checked(["git", "init"], workspace)
    run_checked(["git", "config", "user.email", "golden@example.invalid"], workspace)
    run_checked(["git", "config", "user.name", "GoFlow Golden Scenario"], workspace)
    run_checked(["git", "add", "."], workspace)
    run_checked(["git", "commit", "-m", "initial readiness fixture"], workspace)


def write_config(runtime_home: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    repo = ROOT.as_posix()
    file_server = (ROOT / "mcp_servers" / "file_tools").as_posix()
    command_server = (ROOT / "mcp_servers" / "command_tools").as_posix()
    agents = "\n".join(
        f"  {agent_id}:\n"
        f"    kind: agent\n"
        f"    name: {name}\n"
        f"    description: Read-only repository readiness role.\n"
        f"    provider: primary\n"
        f"    mode: {mode}\n"
        f"    tool_policy: allow\n"
        f"    allowed_tool_kinds: [read, exec]\n"
        f"    max_iterations: 6"
        for agent_id, name, mode in [
            ("planner", "Planner", "plan"),
            ("fixer", "Build Reviewer", "audit"),
            ("auditor", "Test Reviewer", "audit"),
            ("chat", "Readiness Reporter", "plan"),
        ]
    )
    config = f"""
agent:
  name: GoFlow Repository Readiness Golden Scenario
  max_iterations: 8
  timeout: 3m
default_agent: planner
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider_port}/v1
    api_key: golden-key
    model: golden-model
    timeout: 30s
    temperature: 0
    max_tokens: 768
    retry_count: 0
agents:
{agents}
skill:
  directory: {yaml_quote(str(ROOT / 'skills'))}
  match_threshold: 1
  hot_reload: false
audit:
  enabled: true
  redact_content: false
verifier:
  enabled: false
session:
  max_history: 12
  persist_path: {yaml_quote(str(runtime_home / 'session.json'))}
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
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 524288
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def workspace_hashes(workspace: Path) -> dict[str, str]:
    hashes: dict[str, str] = {}
    for path in sorted(workspace.rglob("*")):
        if not path.is_file() or ".git" in path.parts or ".goflow" in path.parts:
            continue
        hashes[path.relative_to(workspace).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return hashes


def provider_tool_names(payload: dict) -> set[str]:
    tools = payload.get("tools") or []
    return {
        str((tool.get("function") or {}).get("name", ""))
        for tool in tools
        if isinstance(tool, dict)
    }


def browser_start_workflow(browser: Path, base_url: str, timeout: float, profile: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#workflows",
        """
        (async () => {
          const templateResponse = await fetch('/api/workflow-templates/repository-readiness-review');
          const template = await templateResponse.json();
          const saveResponse = await fetch('/api/workflow-graphs', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(template.graph)
          });
          const saved = await saveResponse.json();
          const runResponse = await fetch('/api/workflows/repository-readiness-review', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({
              input: 'Review repository build, test, and configuration readiness without changing any file.',
              background: true,
              approve: false
            })
          });
          const run = await runResponse.json();
          return {
            templateStatus: templateResponse.status,
            saveStatus: saveResponse.status,
            savedName: saved.name,
            runStatus: runResponse.status,
            run
          };
        })()
        """,
        timeout,
        profile,
    )


def browser_verify_reopened_run(browser: Path, base_url: str, run_id: str, timeout: float, profile: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runKey = 'workflow:{run_id}';
          const historyDeadline = Date.now() + 15000;
          while (Date.now() < historyDeadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains('active')) runButton.click();
          while (Date.now() < historyDeadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="{run_id}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="{run_id}"]`);
          const artifacts = stack?.querySelector('[data-history-lazy-panel="artifacts"]');
          const artifactContent = artifacts?.querySelector('[data-history-lazy-content="artifacts"]');
          artifacts?.querySelector('[data-history-load="artifacts"]')?.click();
          while (Date.now() < historyDeadline && !(artifactContent?.textContent || '').includes('Repository readiness report')) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const detail = document.querySelector('#runHistoryDetail');
          const openButton = Array.from(detail?.querySelectorAll('button[data-history-action]') || [])
            .find(button => button.dataset.historyAction === 'open_workflow_studio');
          openButton?.click();
          const studioDeadline = Date.now() + 15000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="report"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const runtime = name => document.querySelector(`.flow-node[data-stage-name="${{name}}"]`)?.classList.contains('has-runtime') || false;
          return {{
            hash: location.hash,
            hasRunButton: Boolean(runButton),
            artifactLoaded: artifacts?.classList.contains('loaded') || false,
            reportRuntime: runtime('report'),
            buildRuntime: runtime('build-review'),
            testRuntime: runtime('test-review'),
            configurationRuntime: runtime('configuration-review'),
            runtimeNodes: document.querySelectorAll('.flow-node.has-runtime').length,
            overflowX: document.documentElement.scrollWidth > innerWidth
          }};
        }})()
        """,
        timeout,
        profile,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", type=Path, default=None)
    parser.add_argument("--required", action="store_true")
    parser.add_argument("--timeout", type=float, default=150.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("golden scenario 3 skipped: no Chrome/Edge/Chromium binary found")
        return 0
    artifacts_root = ROOT / ".artifacts"
    artifacts_root.mkdir(parents=True, exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-golden-3-", dir=artifacts_root))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    workspace.mkdir(parents=True)
    runtime_home.mkdir(parents=True)
    provider_port = free_port()
    http_port = free_port()
    provider = ScenarioProvider
    provider.requests = []
    provider.stage_requests = {}
    provider.active_branch_requests = 0
    provider.max_active_branch_requests = 0
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    threading.Thread(target=provider_server.serve_forever, daemon=True).start()
    app_process: subprocess.Popen[str] | None = None
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-golden-3-browser-"))
    try:
        write_workspace(workspace)
        baseline_hashes = workspace_hashes(workspace)
        baseline_status = subprocess.check_output(["git", "status", "--porcelain"], cwd=workspace, text=True)
        config_path = write_config(runtime_home, provider_port)
        binary = temp_root / ("goflow-golden-3.exe" if os.name == "nt" else "goflow-golden-3")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)

        app_process = start_app(binary, config_path, workspace, http_port, build_env)
        base_url = f"http://127.0.0.1:{http_port}"
        wait_for_server(base_url, app_process, args.timeout)
        started = browser_start_workflow(browser, base_url, args.timeout, browser_profile)
        if started.get("templateStatus") != 200 or started.get("saveStatus") != 201 or started.get("runStatus") != 202:
            raise RuntimeError(f"browser could not start repository readiness workflow: {started}")
        run_id = str((started.get("run") or {}).get("run_id", ""))
        if not run_id:
            raise RuntimeError(f"background workflow did not return run_id: {started}")
        completed = wait_for_status(base_url, run_id, {"completed", "failed", "blocked", "action_failed"}, args.timeout)
        if completed.get("status") != "completed":
            raise RuntimeError(f"repository readiness review did not complete: {completed}")

        stages = completed.get("completed_stages") or []
        by_stage = {str(item.get("stage")): item for item in stages}
        for stage_name in BRANCH_STAGES:
            if (by_stage.get(stage_name) or {}).get("metadata", {}).get("parallel_branch") != "true":
                raise RuntimeError(f"{stage_name} was not recorded as a parallel branch: {by_stage.get(stage_name)}")
        report_output = str((by_stage.get("report") or {}).get("result", {}).get("output", ""))
        for marker in ["Readiness Decision", "Build Evidence", "Test Evidence", "Configuration Evidence", "No-Change Confirmation", "Artifact References"]:
            if marker not in report_output:
                raise RuntimeError(f"readiness report is missing {marker}: {report_output}")
        if provider.max_active_branch_requests < 2:
            raise RuntimeError(f"parallel branches did not overlap at the provider: max={provider.max_active_branch_requests}")

        forbidden_tools = {"apply_patch", "write_file", "delete_file", "restore_file_change"}
        exposed_forbidden: set[str] = set()
        for payload in provider.requests:
            exposed_forbidden.update(name for name in provider_tool_names(payload) if name in forbidden_tools)
        if exposed_forbidden:
            raise RuntimeError(f"read-only review exposed write tools: {sorted(exposed_forbidden)}")

        current_status = subprocess.check_output(["git", "status", "--porcelain"], cwd=workspace, text=True)
        current_hashes = workspace_hashes(workspace)
        if baseline_status != current_status or baseline_hashes != current_hashes:
            raise RuntimeError(
                f"readiness review changed the repository: before={baseline_status!r}, after={current_status!r}, "
                f"hashes_equal={baseline_hashes == current_hashes}"
            )
        diffs = request_json(f"{base_url}/api/workflow-runs/{run_id}/diffs?include_content=true")
        if int(diffs.get("total", 0)) != 0:  # type: ignore[union-attr]
            raise RuntimeError(f"read-only workflow recorded file diffs: {diffs}")
        artifacts = request_json(f"{base_url}/api/workflow-runs/{run_id}/artifacts?include_content=true")
        items = artifacts if isinstance(artifacts, list) else artifacts.get("items", artifacts.get("artifacts", []))
        titles = {str(item.get("title", "")) for item in items}
        required_titles = {
            "Readiness inventory",
            "Build readiness report",
            "Test readiness report",
            "Configuration readiness report",
            "Repository readiness report",
        }
        if not required_titles.issubset(titles):
            raise RuntimeError(f"repository readiness artifacts are incomplete: {titles}")

        stop_app(app_process)
        app_process = None
        app_process = start_app(binary, config_path, workspace, http_port, build_env)
        wait_for_server(base_url, app_process, args.timeout)
        restored = request_json(f"{base_url}/api/workflow-runs/{run_id}")
        if restored.get("status") != "completed" or len(restored.get("completed_stages") or []) != len(stages):
            raise RuntimeError(f"completed readiness run was not preserved across restart: {restored}")
        browser_view = browser_verify_reopened_run(browser, base_url, run_id, args.timeout, browser_profile)
        required_runtime = [
            browser_view.get("reportRuntime"),
            browser_view.get("buildRuntime"),
            browser_view.get("testRuntime"),
            browser_view.get("configurationRuntime"),
        ]
        if (
            browser_view.get("hash") != "#workflows"
            or not browser_view.get("hasRunButton")
            or not browser_view.get("artifactLoaded")
            or not all(required_runtime)
            or browser_view.get("overflowX")
        ):
            raise RuntimeError(f"restarted browser could not reopen readiness evidence: {browser_view}")

        events = request_json(f"{base_url}/api/workflow-runs/{run_id}/events?limit=500")
        event_items = events if isinstance(events, list) else events.get("items", events.get("events", []))
        event_text = json.dumps(event_items, ensure_ascii=False)
        if "parallel_branch" not in event_text or "workflow_result" not in event_text:
            raise RuntimeError(f"readiness timeline lacks parallel or final evidence: {event_items}")
        if "workflow_approval_submitted" in event_text or '"type": "approval"' in event_text:
            raise RuntimeError(f"read-only readiness review unexpectedly requested approval: {event_items}")

        print(
            json.dumps(
                {
                    "run_id": run_id,
                    "restart_restored": True,
                    "parallel_stages": sorted(BRANCH_STAGES),
                    "max_parallel_provider_requests": provider.max_active_branch_requests,
                    "provider_stage_requests": provider.stage_requests,
                    "write_tools_exposed": 0,
                    "repository_changed": False,
                    "diffs": 0,
                    "artifacts": len(items),
                    "browser": "passed",
                },
                indent=2,
            )
        )
        return 0
    finally:
        provider_server.shutdown()
        provider_server.server_close()
        stop_app(app_process)
        shutil.rmtree(browser_profile, ignore_errors=True)
        shutil.rmtree(temp_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
