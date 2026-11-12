#!/usr/bin/env python3
"""Run the durable complex-delivery Golden Scenario through the browser.

The scenario uses the real OpenAI-compatible provider adapter and real file
and command MCP servers. Model responses come from a deterministic local HTTP
provider so the check does not require an external API key.
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
ORIGINAL_MAIN = 'package greeting\n\nfunc Message() string {\n\treturn "hello"\n}\n'
ORIGINAL_README = "# Golden project\n\nInitial behavior.\n"
MAIN_SHA256 = hashlib.sha256(ORIGINAL_MAIN.encode("utf-8")).hexdigest()
README_SHA256 = hashlib.sha256(ORIGINAL_README.encode("utf-8")).hexdigest()


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


class ScenarioProvider(BaseHTTPRequestHandler):
    requests: list[dict]
    stage_requests: dict[str, int]
    lock = threading.Lock()
    injected_verification_failure = False

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
        with self.lock:
            self.requests.append(payload)
            self.stage_requests[stage] = self.stage_requests.get(stage, 0) + 1
            inject_failure = stage == "verify" and not type(self).injected_verification_failure
            if inject_failure:
                type(self).injected_verification_failure = True
        if inject_failure:
            body = b'{"error":{"message":"injected verification-stage provider failure"}}'
            self.send_response(500)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        response = self._response(stage, payload)
        self._write_sse(response)

    @staticmethod
    def _stage(payload: dict) -> str:
        text = json.dumps(payload.get("messages") or [], ensure_ascii=False)
        checks = [
            ("Delivery Status", "final-report"),
            ("Recovery Audit Decision", "recovery-audit"),
            ("Recovery Verification Result", "recovery-verify"),
            ("修复摘要", "repair"),
            ("Audit Decision", "audit"),
            ("Verification Result", "verify"),
            ("engineering_v1", "implement"),
            ("Change Summary", "implement"),
            ("变更摘要", "implement"),
            ("Delivery Plan", "plan"),
        ]
        for marker, stage in checks:
            if marker in text:
                return stage
        return "unknown"

    def _response(self, stage: str, payload: dict) -> list[dict]:
        messages = payload.get("messages") or []
        tool_messages = [message for message in messages if message.get("role") == "tool"]
        tool_names = [str(message.get("name", "")) for message in tool_messages]
        if stage == "plan":
            return self._text_response(
                "Delivery Plan: update the greeting behavior and its project documentation\n"
                "Target Files: main.go and README.md\n"
                "Change Slices: first update Message, then document the delivered behavior\n"
                "Dependencies: the implementation must finish before test and lint branches\n"
                "Acceptance Checks: Message returns hello, local agent and documentation matches\n"
                "Verification Commands: go test ./... and go vet ./...\n"
                "Failure Recovery: retain the failed attempt and retry only the failed verification stage\n"
                "Definition of Done: both files changed, tests and vet pass, and audit evidence is complete"
            )
        if stage == "implement":
            reads = sum(name.endswith("read_file") for name in tool_names)
            patches = sum(name.endswith("apply_patch") for name in tool_names)
            commands = sum(name.endswith("run_command") for name in tool_names)
            if reads == 0:
                return self._tool_call("implement-read-main", "file_tools/read_file", {"path": "main.go"})
            if reads == 1:
                return self._tool_call("implement-read-readme", "file_tools/read_file", {"path": "README.md"})
            if patches == 0:
                return self._tool_call(
                    "implement-patch-main",
                    "file_tools/apply_patch",
                    {
                        "path": "main.go",
                        "expected_sha256": MAIN_SHA256,
                        "edits": [{"old_text": 'return "hello"', "new_text": 'return "hello, local agent"'}],
                    },
                )
            if patches == 1:
                return self._tool_call(
                    "implement-patch-readme",
                    "file_tools/apply_patch",
                    {
                        "path": "README.md",
                        "expected_sha256": README_SHA256,
                        "edits": [
                            {
                                "old_text": "Initial behavior.",
                                "new_text": "Message returns `hello, local agent`. Verify with `go test ./...` and `go vet ./...`.",
                            }
                        ],
                    },
                )
            if commands == 0:
                return self._command_call("implement-test", "test")
            return self._text_response(
                "变更摘要：已更新问候语和对应项目文档\n"
                "变更文件：main.go 和 README.md\n"
                "需求覆盖：本地智能体问候语及配套文档\n"
                "尝试验证：go test ./... 已通过\n"
                "失败恢复：补丁使用源文件哈希约束\n"
                "剩余风险：无"
            )
        if stage == "verify":
            if len(tool_messages) == 0:
                return self._command_call("verify-go-test", "test")
            if len(tool_messages) == 1:
                return self._command_call("verify-go-vet", "vet")
            return self._text_response(
                "Verification Result: passed after one injected retry\n"
                "Commands Run: go test ./..., go vet ./...\n"
                "Tests and Build: go test covered compilation and passed\n"
                "Static Analysis: go vet passed\n"
                "Failure Evidence: first verification request returned injected HTTP 500\n"
                "Infrastructure Limits: none\n"
                "Residual Risk: none"
            )
        if stage == "audit":
            return self._text_response(
                "Audit Decision: pass\n"
                "Requirements Coverage: greeting and documentation requirements are covered\n"
                "Verification Evidence: go test, go vet, and gofmt passed after one injected retry\n"
                "Blocking Findings: none\n"
                "Recovery Status: injected test-stage failure is retained and recovered\n"
                "Recommendation: deliver"
            )
        if stage == "repair":
            return self._text_response(
                "修复摘要：未发现需要修改源文件的有效阻断项\n"
                "修复文件：无\n"
                "缺陷覆盖：保留初始验证证据供恢复阶段复核\n"
                "尝试验证：未重复执行已经通过的命令\n"
                "失败恢复：保留原始变更和质量门证据\n"
                "剩余风险：质量门误路由需要恢复审计确认"
            )
        if stage == "recovery-verify":
            return self._text_response(
                "Recovery Verification Result: passed\n"
                "Commands Run: go test ./..., go vet ./...\n"
                "Regression Evidence: original focused behavior remains covered\n"
                "Full Test Result: passed\n"
                "Static Analysis: passed\n"
                "Infrastructure Limits: none\n"
                "Residual Risk: none"
            )
        if stage == "recovery-audit":
            return self._text_response(
                "Recovery Audit Decision: pass\n"
                "Finding Resolution: no unresolved blocker\n"
                "Requirements Coverage: greeting and documentation remain covered\n"
                "Verification Evidence: tests and static analysis passed\n"
                "Blocking Findings: none\n"
                "Recommendation: deliver"
            )
        if stage == "final-report":
            return self._text_response(
                "Delivery Status: completed\n"
                "Changes Delivered: local-agent greeting and matching documentation\n"
                "Files Changed: main.go and README.md\n"
                "Verification Evidence: go test, go vet, and gofmt passed after retry\n"
                "Audit Decision: pass\n"
                "Recovery: injected HTTP 500 was preserved and recovered by stage retry\n"
                "Residual Risks: none\n"
                "Artifact References: delivery-plan, implementation-report, verification-report, delivery-audit, completion-report"
            )
        return self._text_response("Unknown stage: blocked")

    @staticmethod
    def _command_call(call_id: str, command: str) -> list[dict]:
        return ScenarioProvider._tool_call(
            call_id,
            "command_tools/run_command",
            {"program": "go", "args": [command, "./..."], "cwd": ".", "timeout_seconds": 60, "env_refs": ["GOCACHE"]},
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
    (workspace / "go.mod").write_bytes(b"module golden.local/delivery\n\ngo 1.20\n")
    (workspace / "main.go").write_bytes(ORIGINAL_MAIN.encode("utf-8"))
    (workspace / "main_test.go").write_bytes(
        b'package greeting\n\nimport "testing"\n\nfunc TestMessage(t *testing.T) {\n\tif Message() != "hello, local agent" {\n\t\tt.Fatal("Message should describe the local agent")\n\t}\n}\n'
    )
    (workspace / "README.md").write_bytes(ORIGINAL_README.encode("utf-8"))
    run_checked(["git", "init"], workspace)
    run_checked(["git", "config", "user.email", "golden@example.invalid"], workspace)
    run_checked(["git", "config", "user.name", "GoFlow Golden Scenario"], workspace)
    run_checked(["git", "add", "go.mod", "main.go", "main_test.go", "README.md"], workspace)
    run_checked(["git", "commit", "-m", "initial complex delivery fixture"], workspace)


def write_config(runtime_home: Path, workspace: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    repo = ROOT.as_posix()
    file_server = (ROOT / "mcp_servers" / "file_tools").as_posix()
    command_server = (ROOT / "mcp_servers" / "command_tools").as_posix()
    config = f"""
agent:
  name: GoFlow Complex Delivery Golden Scenario
  max_iterations: 10
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
  planner:
    kind: agent
    name: Planner
    description: Plans and reports complex local delivery work.
    provider: primary
    mode: plan
    tool_policy: allow
    allowed_tool_kinds: [read, exec]
    max_iterations: 6
  fixer:
    kind: agent
    name: Fixer
    description: Applies bounded local project changes.
    provider: primary
    mode: fix
    tool_policy: allow
    allowed_tool_kinds: [read, write, exec]
    max_iterations: 10
  auditor:
    kind: agent
    name: Auditor
    description: Verifies and audits delivery evidence.
    provider: primary
    mode: audit
    tool_policy: allow
    allowed_tool_kinds: [read, exec]
    max_iterations: 6
  software-engineer:
    kind: subagent
    name: Software Engineer
    description: Confirms that the complete project builds successfully.
    provider: primary
    mode: fix
    tool_policy: allow
    allowed_tool_kinds: [read, exec]
    max_iterations: 6
  quality-engineer:
    kind: subagent
    name: Quality Engineer
    description: Runs focused and project-level tests with durable evidence.
    provider: primary
    mode: audit
    tool_policy: allow
    allowed_tool_kinds: [read, exec]
    max_iterations: 6
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


def run_checked(command: list[str], cwd: Path, env: dict[str, str] | None = None) -> None:
    completed = subprocess.run(command, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if completed.returncode != 0:
        raise RuntimeError(f"{' '.join(command)} failed with {completed.returncode}\n{completed.stdout}")


def request_json(url: str, payload: dict | None = None, timeout: float = 10.0) -> dict | list:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"} if data else {})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.loads(response.read().decode("utf-8"))


def wait_for_status(base_url: str, run_id: str, expected: set[str], timeout: float) -> dict:
    deadline = time.time() + timeout
    last: dict = {}
    while time.time() < deadline:
        last = request_json(f"{base_url}/api/workflow-runs/{run_id}")  # type: ignore[assignment]
        if str(last.get("status", "")).lower() in expected:
            return last
        time.sleep(0.4)
    raise RuntimeError(f"workflow {run_id} did not reach {sorted(expected)}; last={last}")


def apply_workflow_staged_changes(base_url: str, run_id: str) -> dict:
    try:
        result = request_json(f"{base_url}/api/workflow-runs/{run_id}/apply-staged-changes", {})
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        delivery = request_json(f"{base_url}/api/runs/{run_id}/delivery")
        raise RuntimeError(
            f"verified workflow changes were rejected with HTTP {exc.code}: {body}; delivery={delivery}"
        ) from exc
    if not isinstance(result, dict) or str(result.get("status", "")).lower() != "applied":
        raise RuntimeError(f"verified workflow changes were not applied: {result}")
    return result


def apply_agent_staged_changes(base_url: str, run_id: str) -> dict:
    try:
        result = request_json(f"{base_url}/api/agent-runs/{run_id}/apply-staged-changes", {})
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"verified Agent changes were rejected with HTTP {exc.code}: {body}") from exc
    if not isinstance(result, dict) or str(result.get("status", "")).lower() != "applied":
        raise RuntimeError(f"verified Agent changes were not applied: {result}")
    return result


def start_app(binary: Path, config_path: Path, workspace: Path, port: int, env: dict[str, str]) -> subprocess.Popen[str]:
    return subprocess.Popen(
        [str(binary), "--config", str(config_path), "--workspace", str(workspace), "--http", f":{port}"],
        cwd=ROOT,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )


def stop_app(process: subprocess.Popen[str] | None) -> None:
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def crash_app(process: subprocess.Popen[str] | None) -> None:
    """Simulate a hard process loss without giving the app a shutdown hook."""
    if process is None or process.poll() is not None:
        return
    process.kill()
    process.wait(timeout=10)


def browser_start_workflow(browser: Path, base_url: str, timeout: float, profile: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#workflows",
        f"""
        (async () => {{
          const templateResponse = await fetch('/api/workflow-templates/complex-project-delivery');
          const template = await templateResponse.json();
          const saveResponse = await fetch('/api/workflow-graphs', {{
            method: 'POST',
            headers: {{'Content-Type': 'application/json'}},
            body: JSON.stringify(template.graph)
          }});
          const saved = await saveResponse.json();
          const runResponse = await fetch('/api/workflows/complex-project-delivery', {{
            method: 'POST',
            headers: {{'Content-Type': 'application/json'}},
            body: JSON.stringify({{
              input: 'Update the greeting and its documentation, verify the complete Go project, and preserve retry evidence.',
              background: true,
              approve: false
            }})
          }});
          const run = await runResponse.json();
          return {{
            templateStatus: templateResponse.status,
            saveStatus: saveResponse.status,
            savedName: saved.name,
            runStatus: runResponse.status,
            run
          }};
        }})()
        """,
        timeout,
        profile,
    )


def browser_approve_workflow(browser: Path, base_url: str, run_id: str, timeout: float, profile: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const before = await fetch('/api/workflow-runs/{run_id}').then(response => response.json());
          const response = await fetch('/api/workflow-runs/{run_id}/approve', {{
            method: 'POST',
            headers: {{'Content-Type': 'application/json'}},
            body: JSON.stringify({{background: true}})
          }});
          const accepted = await response.json();
          return {{beforeStatus: before.status, approvalStatus: response.status, accepted}};
        }})()
        """,
        timeout,
        profile,
    )


def browser_verify_result(browser: Path, base_url: str, run_id: str, timeout: float, profile: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const runKey = 'workflow:{run_id}';
          const deadline = Date.now() + 12000;
          while (Date.now() < deadline && !document.querySelector(`[data-history-run="${{runKey}}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const runButton = document.querySelector(`[data-history-run="${{runKey}}"]`);
          if (runButton && !runButton.classList.contains('active')) runButton.click();
          while (Date.now() < deadline && !document.querySelector(`[data-history-lazy-stack][data-run-id="{run_id}"]`)) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const stack = document.querySelector(`[data-history-lazy-stack][data-run-id="{run_id}"]`);
          const artifactPanel = stack?.querySelector('[data-history-lazy-panel="artifacts"]');
          const artifactButton = artifactPanel?.querySelector('[data-history-load="artifacts"]');
          const artifactContent = artifactPanel?.querySelector('[data-history-lazy-content="artifacts"]');
          artifactButton?.click();
          while (Date.now() < deadline && !(artifactContent?.textContent || '').includes('Completion report')) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const detail = document.querySelector('#runHistoryDetail');
          const openButton = Array.from(detail?.querySelectorAll('button[data-history-action]') || [])
            .find(button => button.dataset.historyAction === 'open_workflow_studio');
          openButton?.click();
          const studioDeadline = Date.now() + 15000;
          while (Date.now() < studioDeadline && !document.querySelector('.flow-node[data-stage-name="final-report"].has-runtime')) {{
            await new Promise(resolve => setTimeout(resolve, 150));
          }}
          const finalNode = document.querySelector('.flow-node[data-stage-name="final-report"]');
          const verifyNode = document.querySelector('.flow-node[data-stage-name="verify"]');
          const artifacts = await fetch('/api/workflow-runs/{run_id}/artifacts?include_content=true').then(response => response.json());
          const diffs = await fetch('/api/workflow-runs/{run_id}/diffs?include_content=true').then(response => response.json());
          return {{
            hasRunButton: Boolean(runButton),
            artifactLoaded: artifactPanel?.classList.contains('loaded') || false,
            artifactText: artifactContent?.textContent || '',
            hash: location.hash,
            finalRuntime: finalNode?.classList.contains('has-runtime') || false,
            verifyRuntime: verifyNode?.classList.contains('has-runtime') || false,
            runtimeNodes: document.querySelectorAll('.flow-node.has-runtime').length,
            stageNodes: document.querySelectorAll('.flow-node[data-stage-name]').length,
            artifactsCount: Array.isArray(artifacts) ? artifacts.length : (artifacts.items || artifacts.artifacts || []).length,
            diffsTotal: diffs.total || 0,
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
        print("golden scenario 2 skipped: no Chrome/Edge/Chromium binary found")
        return 0
    artifacts_root = ROOT / ".artifacts"
    artifacts_root.mkdir(parents=True, exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-golden-2-", dir=artifacts_root))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    workspace.mkdir(parents=True)
    runtime_home.mkdir(parents=True)
    provider_port = free_port()
    http_port = free_port()
    provider = ScenarioProvider
    provider.requests = []
    provider.stage_requests = {}
    provider.injected_verification_failure = False
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    provider_thread = threading.Thread(target=provider_server.serve_forever, daemon=True)
    provider_thread.start()
    app_process: subprocess.Popen[str] | None = None
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-golden-2-browser-"))
    try:
        write_workspace(workspace)
        config_path = write_config(runtime_home, workspace, provider_port)
        binary = temp_root / ("goflow-golden-2.exe" if os.name == "nt" else "goflow-golden-2")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)

        app_process = start_app(binary, config_path, workspace, http_port, build_env)
        base_url = f"http://127.0.0.1:{http_port}"
        wait_for_server(base_url, app_process, args.timeout)
        started = browser_start_workflow(browser, base_url, args.timeout, browser_profile)
        if started.get("templateStatus") != 200 or started.get("saveStatus") != 201 or started.get("runStatus") != 202:
            raise RuntimeError(f"browser could not materialize and start the reference workflow: {started}")
        run_id = str((started.get("run") or {}).get("run_id", ""))
        if not run_id:
            raise RuntimeError(f"background workflow response did not include run_id: {started}")
        paused = wait_for_status(base_url, run_id, {"awaiting_approval"}, args.timeout)
        if paused.get("next_stage") != "approve-plan":
            raise RuntimeError(f"workflow did not pause at its single plan checkpoint: {paused}")

        crash_app(app_process)
        app_process = None
        app_process = start_app(binary, config_path, workspace, http_port, build_env)
        wait_for_server(base_url, app_process, args.timeout)
        restored = request_json(f"{base_url}/api/workflow-runs/{run_id}")
        actions = request_json(f"{base_url}/api/workflow-runs/{run_id}/actions")
        approve_action = next((item for item in actions if item.get("name") == "approve_stage"), None)  # type: ignore[union-attr]
        if restored.get("status") != "awaiting_approval" or not approve_action or not approve_action.get("available"):
            raise RuntimeError(f"approval checkpoint was not durable across restart: run={restored}, actions={actions}")

        approval = browser_approve_workflow(browser, base_url, run_id, args.timeout, browser_profile)
        if approval.get("beforeStatus") != "awaiting_approval" or approval.get("approvalStatus") != 202:
            raise RuntimeError(f"browser approval did not resume in the background: {approval}")
        completed = wait_for_status(base_url, run_id, {"completed", "failed", "blocked", "action_failed"}, args.timeout)
        if completed.get("status") != "completed":
            raise RuntimeError(f"complex delivery did not complete: {completed}")

        apply_workflow_staged_changes(base_url, run_id)

        stages = completed.get("completed_stages") or []
        by_stage = {str(item.get("stage")): item for item in stages}
        if int((by_stage.get("verify") or {}).get("attempts", 0)) != 2:
            stage_summary = [(item.get("stage"), item.get("status"), item.get("attempts")) for item in stages]
            raise RuntimeError(f"verification stage did not preserve retry attempt 2: verify={by_stage.get('verify')}, stages={stage_summary}")
        repair_stages = {"repair", "recovery-verify", "recovery-audit", "final-quality"}
        unexpected_repair = sorted(repair_stages.intersection(by_stage))
        if unexpected_repair:
            command_diagnostics = {}
            result = (by_stage.get("verify") or {}).get("result", {})
            command_diagnostics["verify"] = [
                    {
                        "tool": item.get("tool_name"),
                        "is_error": item.get("is_error"),
                        "content": item.get("content"),
                    }
                    for item in result.get("tool_results", [])
                ]
            failures = (by_stage.get("quality") or {}).get("outputs", {}).get("failures")
            raise RuntimeError(
                f"passing quality evidence should not route through repair closure: "
                f"stages={unexpected_repair}, failures={failures}, commands={command_diagnostics}"
            )
        final_output = str((by_stage.get("final-report") or {}).get("result", {}).get("output", ""))
        for marker in ["Delivery Status", "Verification Evidence", "Artifact References"]:
            if marker not in final_output:
                raise RuntimeError(f"final report is missing {marker}: {final_output}")

        main_text = (workspace / "main.go").read_text(encoding="utf-8")
        readme_text = (workspace / "README.md").read_text(encoding="utf-8")
        if 'return "hello, local agent"' not in main_text or "go vet ./..." not in readme_text:
            raise RuntimeError("the workflow did not apply both expected file changes")
        run_checked(["go", "test", "./..."], workspace, build_env)
        run_checked(["go", "build", "./..."], workspace, build_env)
        run_checked(["go", "vet", "./..."], workspace, build_env)

        diffs = request_json(f"{base_url}/api/workflow-runs/{run_id}/diffs?include_content=true")
        diff_paths = {str(item.get("path", "")) for item in diffs.get("diffs", [])}  # type: ignore[union-attr]
        if int(diffs.get("total", 0)) < 2 or not {"main.go", "README.md"}.issubset(diff_paths):  # type: ignore[union-attr]
            raise RuntimeError(f"workflow did not persist both file diffs: {diffs}")
        artifacts = request_json(f"{base_url}/api/workflow-runs/{run_id}/artifacts?include_content=true")
        artifact_items = artifacts if isinstance(artifacts, list) else artifacts.get("items", artifacts.get("artifacts", []))
        artifact_titles = {str(item.get("title", "")) for item in artifact_items}
        required_artifacts = {"Delivery plan", "Implementation report", "Verification report", "Delivery audit", "Completion report"}
        if not required_artifacts.issubset(artifact_titles):
            plan_artifacts = (by_stage.get("plan") or {}).get("artifacts", [])
            raise RuntimeError(
                f"workflow artifacts are incomplete: titles={artifact_titles}, "
                f"plan_stage_artifacts={plan_artifacts}"
            )

        events = request_json(f"{base_url}/api/workflow-runs/{run_id}/events?limit=500")
        event_items = events if isinstance(events, list) else events.get("items", events.get("events", []))
        event_text = json.dumps(event_items, ensure_ascii=False)
        for marker in ["workflow_approval_submitted", "stage_retry", "workflow_result"]:
            if marker not in event_text:
                raise RuntimeError(f"workflow timeline is missing {marker}: {event_items}")
        if event_text.count("workflow_approval_submitted") != 1:
            raise RuntimeError(f"workflow should record exactly one plan approval: {event_items}")

        browser_view = browser_verify_result(browser, base_url, run_id, args.timeout, browser_profile)
        if not browser_view.get("hasRunButton") or not browser_view.get("artifactLoaded"):
            raise RuntimeError(f"Tasks page did not reopen the run and lazy-load artifacts: {browser_view}")
        if browser_view.get("hash") != "#workflows" or not browser_view.get("finalRuntime") or not browser_view.get("verifyRuntime"):
            raise RuntimeError(f"Workflow Studio did not render completed runtime stages: {browser_view}")
        if int(browser_view.get("artifactsCount", 0)) < len(required_artifacts) or int(browser_view.get("diffsTotal", 0)) < 2 or browser_view.get("overflowX"):
            raise RuntimeError(f"browser evidence or layout check failed: {browser_view}")

        print(
            json.dumps(
                {
                    "run_id": run_id,
                    "restart_restored": True,
                    "crash_recovery": True,
                    "approval_count": event_text.count("workflow_approval_submitted"),
                    "verification_attempts": (by_stage.get("verify") or {}).get("attempts"),
                    "verification_stages": ["verify"],
                    "provider_requests": len(provider.requests),
                    "provider_stage_requests": provider.stage_requests,
                    "diffs": diffs.get("total", 0),  # type: ignore[union-attr]
                    "diff_items": [
                        {
                            "path": item.get("path"),
                            "source": item.get("source"),
                            "tool_call_id": item.get("tool_call_id"),
                            "change_id": item.get("change_id"),
                        }
                        for item in diffs.get("diffs", [])  # type: ignore[union-attr]
                    ],
                    "artifacts": len(artifact_items),
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
