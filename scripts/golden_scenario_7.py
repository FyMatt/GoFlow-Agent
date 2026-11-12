#!/usr/bin/env python3
"""Run the deterministic network-security delivery Golden Scenario."""

from __future__ import annotations

import argparse
import hashlib
import html
import json
import os
import shutil
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse

from golden_scenario_2 import apply_agent_staged_changes, request_json, run_checked, start_app, stop_app, yaml_quote
from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server


ROOT = Path(__file__).resolve().parents[1]
ORIGINAL_SOURCE = '''package securequery

type Query struct {
\tSQL  string
\tArgs []string
}

func UserQuery(name string) Query {
\treturn Query{SQL: "SELECT id, name FROM users WHERE name = '" + name + "'"}
}
'''
PATCHED_SOURCE = '''package securequery

type Query struct {
\tSQL  string
\tArgs []string
}

func UserQuery(name string) Query {
\treturn Query{
\t\tSQL:  "SELECT id, name FROM users WHERE name = ?",
\t\tArgs: []string{name},
\t}
}
'''
SOURCE_SHA256 = hashlib.sha256(ORIGINAL_SOURCE.encode("utf-8")).hexdigest()
AUDIT_REPORT = """安全审计结论：确认存在 SQL 注入风险，已保留失败回归测试证据。
发现编号 finding_id: SEC-001
严重程度 severity: high
置信度 confidence: high
证据等级 evidence_level: A
可利用性 exploitability: confirmed
受影响目标 affected_target: app.go:8 UserQuery
证据引用 evidence_refs: app.go:9；TestUserQueryRejectsSQLInjection 失败输出
利用前置条件 prerequisites: 攻击者能够控制 name 参数，返回的 SQL 被数据库执行
影响 impact: 可改变查询条件并导致越权读取
修复建议 remediation: 使用参数化查询并将输入放入 Args
回归测试 regression_tests: TestUserQueryRejectsSQLInjection
剩余风险 remaining_risks: 修复前风险仍然存在，禁止将该查询直接用于生产。"""
FIX_REPORT = """安全修复与复审结论：已改为参数化查询，原 SQL 注入路径消失，回归测试通过。
发现编号 finding_id: SEC-001
严重程度 severity: high
置信度 confidence: high
证据等级 evidence_level: A
可利用性 exploitability: unlikely
受影响目标 affected_target: app.go:8 UserQuery
证据引用 evidence_refs: app.go 参数占位符与 Args；go test ./... 通过
利用前置条件 prerequisites: 原始前置条件不再能把输入拼接进 SQL
影响 impact: 已阻断通过 name 改写 SQL 语义的路径
修复措施 remediation: 使用 ? 占位符和独立参数列表
回归测试 regression_tests: TestUserQueryRejectsSQLInjection 通过
剩余风险 remaining_risks: 调用方仍需使用支持参数绑定的数据库接口。"""


class ScenarioProvider(BaseHTTPRequestHandler):
    requests: list[dict]
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
        with self.lock:
            self.requests.append(payload)
        response = self._response(payload.get("messages") or [])
        self._write_sse(response)

    def _response(self, messages: list[dict]) -> list[dict]:
        last_user_index = max((index for index, item in enumerate(messages) if item.get("role") == "user"), default=-1)
        current = messages[last_user_index + 1 :]
        user_text = str(messages[last_user_index].get("content", "")) if last_user_index >= 0 else ""
        tool_messages = [item for item in current if item.get("role") == "tool"]
        tool_names = [str(item.get("name", "")) for item in tool_messages]
        fixing = "修复" in user_text

        if not tool_messages:
            return self._tool_call("security-read", "file_tools/read_file", {"path": "app.go"})
        if not any(name.endswith("run_command") for name in tool_names) and not fixing:
            return self._tool_call(
                "security-reproduce",
                "command_tools/run_command",
                {
                    "program": "go",
                    "args": ["test", "-run", "TestUserQueryRejectsSQLInjection", "-v", "./..."],
                    "cwd": ".",
                    "timeout_seconds": 60,
                },
            )
        if not fixing:
            return self._text_response(AUDIT_REPORT)
        if not any(name.endswith("apply_patch") for name in tool_names):
            return self._tool_call(
                "security-fix",
                "file_tools/apply_patch",
                {
                    "path": "app.go",
                    "expected_sha256": SOURCE_SHA256,
                    "edits": [{"old_text": ORIGINAL_SOURCE, "new_text": PATCHED_SOURCE}],
                },
            )
        if not any(name.endswith("run_command") for name in tool_names):
            return self._tool_call(
                "security-regression",
                "command_tools/run_command",
                {"program": "go", "args": ["test", "./..."], "cwd": ".", "timeout_seconds": 60},
            )
        return self._text_response(FIX_REPORT)

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
            body = json.dumps({"choices": [choice], "usage": {"prompt_tokens": 96, "completion_tokens": 32}})
            self.wfile.write(f"data: {body}\n\n".encode("utf-8"))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


class ProbeTarget(BaseHTTPRequestHandler):
    probe_requests: list[tuple[float, str]]
    lock = threading.Lock()

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        decoded = unquote(self.path)
        if "goflow-ref-" in decoded or "goflow-sql-" in decoded:
            with self.lock:
                self.probe_requests.append((time.monotonic(), decoded))
        values = [value for items in parse_qs(parsed.query, keep_blank_values=True).values() for value in items]
        reflected = values[0] if values else ""
        sql_error = any("goflow-sql-" in value for value in values)
        error_text = "<p>SQL syntax error near quote in SQLite</p>" if sql_error else ""
        page = f"""<!doctype html>
<html><head><title>本地安全验收靶场</title><script>document.write(location.hash)</script></head>
<body>
  <h1>本地安全验收靶场</h1>
  <p id=\"result\">{html.escape(reflected)}</p>{error_text}
  <form method=\"get\" action=\"/search\"><input name=\"term\"><input name=\"page\"></form>
  <a href=\"/item?id=42\">项目</a>
  <a href=\"/lookup?keyword=test\">查找</a>
</body></html>"""
        body = page.encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class MCPProcess:
    def __init__(self, browser: Path, workspace: Path) -> None:
        env = os.environ.copy()
        env["GOFLOW_BROWSER_PATH"] = str(browser)
        env["GOFLOW_WORKSPACE_ROOT"] = str(workspace)
        self.process = subprocess.Popen(
            ["go", "run", "./mcp_servers/web_tools"],
            cwd=ROOT,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            bufsize=1,
        )
        self.request_id = 0

    def call(self, name: str, arguments: dict) -> dict:
        self.request_id += 1
        request = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        }
        if self.process.stdin is None or self.process.stdout is None:
            raise RuntimeError("web_tools MCP stdio is unavailable")
        self.process.stdin.write(json.dumps(request, separators=(",", ":")) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            stderr = self.process.stderr.read() if self.process.stderr is not None else ""
            raise RuntimeError(f"web_tools MCP exited before responding: {stderr}")
        response = json.loads(line)
        result = response.get("result") or {}
        if result.get("is_error"):
            return {"is_error": True, "error": result.get("content", "")}
        return {"is_error": False, "payload": json.loads(str(result.get("content", "{}")))}

    def close(self) -> None:
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)


def write_workspace(workspace: Path) -> None:
    (workspace / "go.mod").write_text("module golden.local/securequery\n\ngo 1.22\n", encoding="utf-8")
    (workspace / "app.go").write_bytes(ORIGINAL_SOURCE.encode("utf-8"))
    (workspace / "app_test.go").write_text(
        '''package securequery

import (
\t"strings"
\t"testing"
)

func TestUserQueryRejectsSQLInjection(t *testing.T) {
\tpayload := "' OR 1=1 --"
\tquery := UserQuery(payload)
\tif strings.Contains(query.SQL, payload) || query.SQL != "SELECT id, name FROM users WHERE name = ?" {
\t\tt.Fatalf("user input reached SQL text: %q", query.SQL)
\t}
\tif len(query.Args) != 1 || query.Args[0] != payload {
\t\tt.Fatalf("query arguments were not preserved: %#v", query.Args)
\t}
}
''',
        encoding="utf-8",
    )
    run_checked(["git", "init"], workspace)
    run_checked(["git", "config", "user.email", "golden@example.invalid"], workspace)
    run_checked(["git", "config", "user.name", "GoFlow Golden Scenario"], workspace)
    run_checked(["git", "add", "go.mod", "app.go", "app_test.go"], workspace)
    run_checked(["git", "commit", "-m", "insecure security fixture"], workspace)


def write_config(runtime_home: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    for skill_name in ("code-audit", "code-writing"):
        skill_dir = runtime_home / "skills" / skill_name
        skill_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / "skills" / skill_name / "SKILL.md", skill_dir / "SKILL.md")
    file_server = (ROOT / "mcp_servers" / "file_tools").as_posix()
    command_server = (ROOT / "mcp_servers" / "command_tools").as_posix()
    config = f"""
agent:
  name: GoFlow 安全黄金场景
  max_iterations: 8
  timeout: 2m
default_agent: security-researcher
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
  security-researcher:
    kind: agent
    name: 网络安全智能体
    description: 在已授权工作区内完成证据化代码安全审计、修复和复审。
    provider: primary
    mode: fix
    tool_policy: allow
    allowed_tool_kinds: [read, write, exec]
    allowed_tools:
      - file_tools/read_file
      - file_tools/read_file_range
      - file_tools/list_tree
      - file_tools/write_file
      - file_tools/apply_patch
      - command_tools/run_command
    max_iterations: 8
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
  max_history: 12
  persist_path: {yaml_quote(str(runtime_home / 'session.json'))}
mcp_servers:
  - name: file_tools
    command: go
    args: [run, {yaml_quote(file_server)}]
    enabled: true
    timeout: 30s
    workdir: {yaml_quote(str(ROOT))}
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE, GOFLOW_WORKSPACE_ROOT]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 2097152
  - name: command_tools
    command: go
    args: [run, {yaml_quote(command_server)}]
    enabled: true
    timeout: 2m
    workdir: {yaml_quote(str(ROOT))}
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE, GOFLOW_WORKSPACE_ROOT]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 524288
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def wait_for_run(base_url: str, run_id: str, timeout: float) -> dict:
    deadline = time.time() + timeout
    last: dict = {}
    while time.time() < deadline:
        last = request_json(f"{base_url}/api/runs/{run_id}?full=1")  # type: ignore[assignment]
        if str(last.get("status", "")).lower() in {"succeeded", "completed", "failed", "cancelled"}:
            return last
        time.sleep(0.35)
    raise RuntimeError(f"run {run_id} did not finish: {last}")


def browser_task(browser: Path, base_url: str, skill_name: str, prompt_text: str, previous_id: str, timeout: float, profile: Path, screenshot: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console?lang=zh#playground",
        f"""
        (async () => {{
          const runMode = document.querySelector('#runMode');
          const agent = document.querySelector('#agentSelect');
          const skill = document.querySelector('#skillSelect');
          runMode.value = 'agent';
          runMode.dispatchEvent(new Event('change', {{bubbles: true}}));
          agent.value = 'security-researcher';
          agent.dispatchEvent(new Event('change', {{bubbles: true}}));
          skill.value = {json.dumps(skill_name)};
          skill.dispatchEvent(new Event('change', {{bubbles: true}}));
          await new Promise(resolve => setTimeout(resolve, 250));
          const prompt = document.querySelector('#prompt');
          prompt.value = {json.dumps(prompt_text, ensure_ascii=False)};
          prompt.dispatchEvent(new Event('input', {{bubbles: true}}));
          document.querySelector('#send').click();
          const deadline = Date.now() + {int(timeout * 1000)};
          let run = null;
          while (Date.now() < deadline) {{
            const body = await fetch('/api/runs?type=agent&limit=20').then(response => response.json());
            run = (body.runs || []).find(item => item.id !== {json.dumps(previous_id)}) || null;
            if (run && ['succeeded', 'completed', 'failed', 'cancelled'].includes(String(run.status || '').toLowerCase())) break;
            const pageText = document.body.innerText;
            if (!run && (pageText.includes('任务暂时无法启动') || pageText.includes('Task cannot start'))) break;
            await new Promise(resolve => setTimeout(resolve, 350));
          }}
          if (run) document.querySelector(`[data-history-run="agent:${{run.id}}"]`)?.click();
          await new Promise(resolve => setTimeout(resolve, 500));
          return {{
            id: run?.id || '',
            status: run?.status || '',
            bodyText: document.body.innerText,
            agentLabel: agent.options[agent.selectedIndex]?.textContent || '',
            skillLabel: skill.options[skill.selectedIndex]?.textContent || '',
            primaryResourceTexts: [
              ...document.querySelectorAll('#runContextGuide small, #messages .timeline-card > strong, #runHistoryList .run-history-title-row > strong')
            ].map(node => node.textContent || ''),
            documentWidth: document.documentElement.scrollWidth,
            viewportWidth: innerWidth
          }};
        }})()
        """,
        timeout + 20,
        profile,
        screenshot_path=screenshot,
    )


def timeline_tools(base_url: str, run_id: str) -> set[str]:
    timeline = request_json(f"{base_url}/api/runs/{run_id}/timeline?limit=100&include_content=true")
    items = timeline.get("items", timeline) if isinstance(timeline, dict) else timeline
    return {str(item.get("tool_name", "")) for item in items if isinstance(item, dict) and item.get("tool_name")}


def assert_structured_report(detail: dict, required_report: str) -> None:
    serialized = json.dumps(detail, ensure_ascii=False)
    required = [
        required_report.splitlines()[0],
        "finding_id",
        "severity",
        "confidence",
        "evidence_level",
        "exploitability",
        "affected_target",
        "evidence_refs",
        "prerequisites",
        "impact",
        "remediation",
        "regression_tests",
        "remaining_risks",
    ]
    missing = [marker for marker in required if marker not in serialized]
    if missing:
        raise RuntimeError(f"security report is missing structured quality fields {missing}: {serialized[-4000:]}")


def run_hidden_regression(workspace: Path, env: dict[str, str]) -> None:
    hidden = workspace / "security_hidden_test.go"
    hidden.write_text(
        '''package securequery

import "testing"

func TestHiddenParameterizedQueryContract(t *testing.T) {
\tpayload := "admin' UNION SELECT secret, token FROM credentials --"
\tquery := UserQuery(payload)
\tif query.SQL != "SELECT id, name FROM users WHERE name = ?" {
\t\tt.Fatalf("unexpected SQL template: %q", query.SQL)
\t}
\tif len(query.Args) != 1 || query.Args[0] != payload {
\t\tt.Fatalf("payload was not isolated as one argument: %#v", query.Args)
\t}
}
''',
        encoding="utf-8",
    )
    try:
        run_checked(["go", "test", "./..."], workspace, env)
    finally:
        hidden.unlink(missing_ok=True)


def validate_web_security(browser: Path, workspace: Path, target_url: str) -> dict:
    mcp = MCPProcess(browser, workspace)
    try:
        denied_scope = mcp.call(
            "browser_probe_points",
            {
                "url": target_url,
                "allowed_hosts": ["127.0.0.1"],
                "authorized_scope": False,
                "active_probe_approved": False,
            },
        )
        if not denied_scope.get("is_error") or "authorized_scope" not in str(denied_scope.get("error", "")):
            raise RuntimeError(f"missing authorization scope was not blocked: {denied_scope}")

        passive = mcp.call(
            "browser_snapshot",
            {
                "url": target_url,
                "allowed_hosts": ["127.0.0.1"],
                "authorized_scope": True,
                "wait_ms": 500,
                "timeout_seconds": 15,
                "max_dom_chars": 5000,
                "screenshot": True,
            },
        )
        if passive.get("is_error"):
            raise RuntimeError(f"authorized passive browser evidence failed: {passive}")
        passive_payload = passive["payload"]
        if passive_payload.get("title") != "本地安全验收靶场" or not passive_payload.get("dom_sha256") or not passive_payload.get("forms"):
            raise RuntimeError(f"passive evidence is incomplete: {passive_payload}")
        screenshot = passive_payload.get("screenshot") or {}
        screenshot_path = Path(str(screenshot.get("path", "")))
        if not screenshot.get("sha256") or not screenshot_path.is_file():
            raise RuntimeError(f"passive evidence screenshot is missing: {screenshot}")

        blocked = mcp.call(
            "browser_probe_points",
            {
                "url": target_url,
                "allowed_hosts": ["127.0.0.1"],
                "authorized_scope": True,
                "active_probe_approved": False,
                "probe_types": ["reflection", "sql_error"],
                "max_probes": 8,
                "wait_ms": 500,
                "timeout_seconds": 15,
            },
        )
        if blocked.get("is_error"):
            raise RuntimeError(f"passive probe-surface enumeration failed: {blocked}")
        blocked_payload = blocked["payload"]
        if blocked_payload.get("active_probe_count") != 0 or "active_probe_approved" not in str(blocked_payload.get("active_probe_skipped", "")):
            raise RuntimeError(f"unapproved active probes were not blocked: {blocked_payload}")

        approved = mcp.call(
            "browser_probe_points",
            {
                "url": target_url,
                "allowed_hosts": ["127.0.0.1"],
                "authorized_scope": True,
                "active_probe_approved": True,
                "probe_types": ["reflection", "sql_error"],
                "max_probes": 8,
                "wait_ms": 500,
                "timeout_seconds": 15,
                "max_dom_chars": 5000,
            },
        )
        if approved.get("is_error"):
            raise RuntimeError(f"approved canary validation failed: {approved}")
        approved_payload = approved["payload"]
        candidates = approved_payload.get("probe_candidates") or []
        results = approved_payload.get("active_probe_results") or []
        rate_limit = approved_payload.get("rate_limit") or {}
        if not results or len(candidates) > 8 or len(results) > 16:
            raise RuntimeError(f"active probe bounds are invalid: candidates={len(candidates)} results={len(results)}")
        if rate_limit.get("max_candidates") != 8 or int(rate_limit.get("minimum_interval_ms", 0)) < 500:
            raise RuntimeError(f"active probe rate-limit metadata is invalid: {rate_limit}")
        if {str(item.get("type", "")) for item in results} - {"reflection", "sql_error"}:
            raise RuntimeError(f"unexpected active probe type: {results}")

        with ProbeTarget.lock:
            recorded = list(ProbeTarget.probe_requests)
        unique: list[tuple[float, str]] = []
        seen: set[str] = set()
        for item in recorded:
            if item[1] not in seen:
                seen.add(item[1])
                unique.append(item)
        if len(unique) < len(results):
            raise RuntimeError(f"target did not observe every active probe: observed={len(unique)} results={len(results)}")
        gaps = [right[0] - left[0] for left, right in zip(unique, unique[1:])]
        if gaps and min(gaps) < 0.45:
            raise RuntimeError(f"active probe interval fell below the 500ms policy allowance: min={min(gaps):.3f}s")
        return {
            "passive_title": passive_payload.get("title"),
            "passive_forms": len(passive_payload.get("forms") or []),
            "unapproved_active_count": blocked_payload.get("active_probe_count"),
            "approved_candidates": len(candidates),
            "approved_requests": len(results),
            "minimum_interval_ms": round(min(gaps) * 1000) if gaps else 0,
        }
    finally:
        mcp.close()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--browser", type=Path, default=None)
    parser.add_argument("--required", action="store_true")
    parser.add_argument("--timeout", type=float, default=180.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("安全黄金场景已跳过：未找到 Chrome、Edge 或 Chromium")
        return 0
    artifact_dir = ROOT / ".artifacts" / "golden-scenario-7"
    artifact_dir.mkdir(parents=True, exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-security-golden-", dir=ROOT / ".artifacts"))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    workspace.mkdir(parents=True)
    runtime_home.mkdir(parents=True)
    provider_port = free_port()
    app_port = free_port()
    target_port = free_port()
    provider = ScenarioProvider
    provider.requests = []
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    provider_thread = threading.Thread(target=provider_server.serve_forever, daemon=True)
    provider_thread.start()
    ProbeTarget.probe_requests = []
    target_server = ThreadingHTTPServer(("127.0.0.1", target_port), ProbeTarget)
    target_thread = threading.Thread(target=target_server.serve_forever, daemon=True)
    target_thread.start()
    app_process: subprocess.Popen[str] | None = None
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-security-browser-"))
    try:
        write_workspace(workspace)
        config_path = write_config(runtime_home, provider_port)
        binary = temp_root / ("goflow-security-golden.exe" if os.name == "nt" else "goflow-security-golden")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)
        app_process = start_app(binary, config_path, workspace, app_port, build_env)
        base_url = f"http://127.0.0.1:{app_port}"
        wait_for_server(base_url, app_process, args.timeout)

        audit_ui = browser_task(
            browser,
            base_url,
            "code-audit",
            "只审计 app.go，不要修改。复现并判断 SQL 注入风险，输出统一结构化安全发现和剩余风险。",
            "",
            args.timeout,
            browser_profile,
            artifact_dir / "01-code-audit.png",
        )
        if not audit_ui.get("id") or str(audit_ui.get("status", "")).lower() not in {"succeeded", "completed"}:
            raise RuntimeError(f"code security audit did not succeed: {audit_ui}")
        if "网络安全" not in str(audit_ui.get("agentLabel", "")) or "审计" not in str(audit_ui.get("skillLabel", "")):
            raise RuntimeError(f"security resources were not shown in Chinese: {audit_ui}")
        if int(audit_ui.get("documentWidth", 0)) > int(audit_ui.get("viewportWidth", 0)) + 2:
            raise RuntimeError(f"Tasks page overflowed during the security audit: {audit_ui}")
        primary_text = "\n".join(str(item) for item in audit_ui.get("primaryResourceTexts", []))
        for raw_resource_id in ("security-researcher", "code-audit", "run_command"):
            if raw_resource_id in primary_text:
                raise RuntimeError(f"Tasks page exposed the compatibility ID {raw_resource_id!r} as a primary label: {primary_text}")
        audit_id = str(audit_ui["id"])
        audit_detail = wait_for_run(base_url, audit_id, args.timeout)
        assert_structured_report(audit_detail, AUDIT_REPORT)
        audit_tools = timeline_tools(base_url, audit_id)
        if not any(name.endswith("read_file") for name in audit_tools) or not any(name.endswith("run_command") for name in audit_tools):
            raise RuntimeError(f"audit timeline lacks source and reproduction evidence: {audit_tools}")
        audit_diffs = request_json(f"{base_url}/api/runs/{audit_id}/diffs?include_content=true")
        if int(audit_diffs.get("total", 0)) != 0:  # type: ignore[union-attr]
            raise RuntimeError(f"read-only audit unexpectedly changed the workspace: {audit_diffs}")

        fix_ui = browser_task(
            browser,
            base_url,
            "code-writing",
            "修复 SEC-001，运行回归测试并复审原风险是否消失。最终报告必须包含修复证据、回归结果和剩余风险。",
            audit_id,
            args.timeout,
            browser_profile,
            artifact_dir / "02-fix-and-review.png",
        )
        if not fix_ui.get("id") or str(fix_ui.get("status", "")).lower() not in {"succeeded", "completed"}:
            raise RuntimeError(f"security remediation did not succeed: {fix_ui}")
        primary_text = "\n".join(str(item) for item in fix_ui.get("primaryResourceTexts", []))
        for raw_resource_id in ("security-researcher", "code-writing", "run_command"):
            if raw_resource_id in primary_text:
                raise RuntimeError(f"Tasks page exposed the compatibility ID {raw_resource_id!r} as a primary label: {primary_text}")
        fix_id = str(fix_ui["id"])
        fix_detail = wait_for_run(base_url, fix_id, args.timeout)
        assert_structured_report(fix_detail, FIX_REPORT)
        apply_agent_staged_changes(base_url, fix_id)
        if (workspace / "app.go").read_text(encoding="utf-8") != PATCHED_SOURCE:
            raise RuntimeError("security remediation did not produce the expected parameterized query")
        run_hidden_regression(workspace, build_env)
        fix_tools = timeline_tools(base_url, fix_id)
        for expected in {"read_file", "apply_patch", "run_command"}:
            if not any(name == expected or name.endswith("/" + expected) for name in fix_tools):
                raise RuntimeError(f"fix timeline lacks {expected}: {fix_tools}")
        fix_diffs = request_json(f"{base_url}/api/runs/{fix_id}/diffs?include_content=true")
        if int(fix_diffs.get("total", 0)) < 1:  # type: ignore[union-attr]
            raise RuntimeError(f"security remediation did not persist a Diff: {fix_diffs}")

        web_result = validate_web_security(browser, workspace, f"http://127.0.0.1:{target_port}/?q=baseline")
        summary = {
            "代码安全发现": "SEC-001 已用失败测试复现",
            "修复复审": "参数化查询已落盘，公开与隐藏回归均通过",
            "审计运行": audit_id,
            "修复运行": fix_id,
            "修复 Diff 数": fix_diffs.get("total", 0),
            "Web 安全验收": web_result,
            "页面截图": [
                str((artifact_dir / "01-code-audit.png").relative_to(ROOT)),
                str((artifact_dir / "02-fix-and-review.png").relative_to(ROOT)),
            ],
            "Provider 请求数": len(provider.requests),
        }
        (artifact_dir / "result.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        return 0
    finally:
        provider_server.shutdown()
        provider_server.server_close()
        target_server.shutdown()
        target_server.server_close()
        stop_app(app_process)
        shutil.rmtree(browser_profile, ignore_errors=True)
        shutil.rmtree(temp_root, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
