#!/usr/bin/env python3
"""Optional browser smoke test for the embedded Web Studio.

This script starts GoFlow HTTP mode with a temporary runtime and opens the
Studio pages in a locally installed headless Chrome/Edge/Chromium binary. It
uses no third-party Python packages. When no browser binary is found, the script
skips by default; pass --required to make that a failure in CI.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import re
import secrets
import shutil
import socket
import struct
import subprocess
import tempfile
import textwrap
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class BrowserSmokeUnavailable(RuntimeError):
    """Raised when a browser exists but cannot run in this host session."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=0, help="HTTP port to use. Defaults to a free local port.")
    parser.add_argument("--timeout", type=float, default=45.0, help="Seconds to wait for startup and page rendering.")
    parser.add_argument("--browser", type=Path, default=None, help="Path to Chrome, Edge, or Chromium.")
    parser.add_argument("--required", action="store_true", help="Fail when no usable browser is found.")
    parser.add_argument("--skip-build", action="store_true", help="Use an existing --binary instead of building.")
    parser.add_argument("--binary", type=Path, default=None, help="Path to an existing goflow binary.")
    return parser.parse_args()


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def run_checked(args: list[str], env: dict[str, str]) -> None:
    completed = subprocess.run(args, cwd=ROOT, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if completed.returncode != 0:
        raise AssertionError(f"{' '.join(args)} failed with {completed.returncode}\n{completed.stdout}")


def request_text(url: str, timeout: float = 5.0) -> tuple[int, str]:
    request = urllib.request.Request(url, headers={"User-Agent": "goflow-browser-smoke/1"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", errors="replace")


def build_binary(tmpdir: Path, env: dict[str, str]) -> Path:
    suffix = ".exe" if os.name == "nt" else ""
    binary = tmpdir / f"goflow-browser-smoke{suffix}"
    run_checked(["go", "build", "-o", str(binary), "./cmd/goflow"], env)
    return binary


def write_smoke_runtime_config(runtime_home: Path) -> Path:
    config_dir = runtime_home / "configs"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir_path = runtime_home / "skills"
    smoke_skill_dir = skill_dir_path / "smoke"
    smoke_skill_dir.mkdir(parents=True, exist_ok=True)
    (smoke_skill_dir / "SKILL.md").write_text(
        textwrap.dedent(
            """
            ---
            name: smoke
            description: Minimal skill used by the browser smoke test runtime.
            ---

            Use this placeholder skill only for local smoke tests.
            """
        ).lstrip(),
        encoding="utf-8",
    )
    config_text = textwrap.dedent(
        f"""
        agent:
          name: GoFlow Browser Smoke
          max_iterations: 4
          timeout: 30s

        default_agent: chat

        providers:
          primary:
            provider: openai-compatible
            base_url: ${{GOFLOW_BASE_URL}}
            api_key: ${{GOFLOW_API_KEY}}
            model: ${{GOFLOW_MODEL}}
            fallback_provider: backup
            timeout: 10s
            temperature: 0
            max_tokens: 128
            retry_count: 0
            retry_backoff: 1s
          backup:
            provider: openai-compatible
            base_url: ${{GOFLOW_BACKUP_BASE_URL}}
            api_key: ${{GOFLOW_BACKUP_API_KEY}}
            model: ${{GOFLOW_BACKUP_MODEL}}
            timeout: 10s
            temperature: 0
            max_tokens: 128
            retry_count: 0
            retry_backoff: 1s

        agents:
          chat:
            name: Chat
            description: Browser-smoke chat agent.
            provider: primary
            mode: chat
            tool_policy: confirm
            allowed_tool_kinds: [read, network]
            max_iterations: 4
          auditor:
            name: Auditor
            description: Browser-smoke verifier agent.
            provider: primary
            mode: audit
            tool_policy: confirm
            allowed_tool_kinds: [read]
            max_iterations: 2

        skill:
          directory: {json.dumps(str(skill_dir_path))}
          match_threshold: 1
          hot_reload: false

        audit:
          enabled: true
          redact_content: false
          show_trace_in_cli: false

        verifier:
          enabled: false
          agent: auditor
          modes: [fix, audit]
          max_tokens: 128

        tool_risk_policy:
          require_approval_for_unsandboxed_risky_tools: true
          disable_remember_for_unsandboxed_risky_tools: true
          reject_unsandboxed_risky_tools: false

        session:
          max_history: 4

        log:
          level: info
          format: text
        """
    ).lstrip()
    config_path = config_dir / "goflow.yaml"
    config_path.write_text(config_text, encoding="utf-8")
    return config_path


def wait_for_server(base_url: str, proc: subprocess.Popen[str], timeout: float) -> None:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        if proc.poll() is not None:
            output = proc.stdout.read() if proc.stdout is not None else ""
            raise AssertionError(f"goflow exited before HTTP became ready with {proc.returncode}\n{output}")
        try:
            status, _ = request_text(f"{base_url}/api/session", timeout=2.0)
            if status == 200:
                return
        except Exception as exc:  # noqa: BLE001 - startup context matters here.
            last_error = str(exc)
        time.sleep(0.25)
    raise AssertionError(f"HTTP server did not become ready at {base_url}: {last_error}")


def browser_candidates() -> list[Path]:
    names = [
        "chrome",
        "google-chrome",
        "google-chrome-stable",
        "chromium",
        "chromium-browser",
        "msedge",
        "microsoft-edge",
    ]
    paths: list[Path] = []
    for name in names:
        resolved = shutil.which(name)
        if resolved:
            paths.append(Path(resolved))
    if os.name == "nt":
        program_files = [os.environ.get("ProgramFiles"), os.environ.get("ProgramFiles(x86)"), os.environ.get("LocalAppData")]
        relative = [
            Path("Google/Chrome/Application/chrome.exe"),
            Path("Microsoft/Edge/Application/msedge.exe"),
            Path("Chromium/Application/chrome.exe"),
        ]
        for base in program_files:
            if not base:
                continue
            for suffix in relative:
                paths.append(Path(base) / suffix)
    return paths


def find_browser(explicit: Path | None, required: bool) -> Path | None:
    candidates = [explicit] if explicit else browser_candidates()
    for candidate in candidates:
        if candidate and candidate.is_file():
            return candidate
    if required:
        searched = ", ".join(str(path) for path in candidates if path)
        raise AssertionError(f"no Chrome/Edge/Chromium browser found for browser smoke test; searched: {searched}")
    return None


def render_dom(browser: Path, url: str, timeout: float, profile_dir: Path) -> str:
    base_command = [
        str(browser),
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-extensions",
        "--disable-background-networking",
        "--no-first-run",
        "--no-default-browser-check",
        "--no-sandbox",
        f"--user-data-dir={profile_dir}",
        "--virtual-time-budget=6000",
        "--dump-dom",
        url,
    ]
    failures = []
    for headless_flag in ("--headless=new", "--headless"):
        command = [base_command[0], headless_flag, *base_command[1:]]
        try:
            completed = subprocess.run(
                command,
                text=True,
                encoding="utf-8",
                errors="replace",
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=timeout,
            )
        except subprocess.TimeoutExpired as exc:
            output = (exc.stdout or "") if isinstance(exc.stdout, str) else ""
            failures.append(f"{headless_flag}: timed out after {timeout}s\n{output[-2000:]}")
            continue
        if completed.returncode == 0:
            return completed.stdout
        failures.append(f"{headless_flag}: exit {completed.returncode}\n{completed.stdout[-3000:]}")
    raise BrowserSmokeUnavailable(f"browser failed for {url}\n" + "\n".join(failures))


def evaluate_browser_json(browser: Path, url: str, script: str, timeout: float, profile_dir: Path) -> dict[str, object]:
    debug_port = free_port()
    debug_profile = profile_dir / f"debug-{secrets.token_hex(4)}"
    debug_profile.mkdir()
    command = [
        str(browser),
        "--headless=new",
        "--disable-gpu",
        "--disable-dev-shm-usage",
        "--disable-extensions",
        "--disable-background-networking",
        "--no-first-run",
        "--no-default-browser-check",
        "--no-sandbox",
        f"--user-data-dir={debug_profile}",
        f"--remote-debugging-port={debug_port}",
        "--window-size=1440,1100",
        url,
    ]
    proc = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    try:
        page = wait_for_debug_page(debug_port, url, proc, timeout)
        expression = textwrap.dedent(
            f"""
            (async () => {{
              await new Promise(resolve => setTimeout(resolve, 900));
              const result = {script};
              return result;
            }})()
            """
        )
        response = devtools_call(
            urllib.parse.urlparse(str(page["webSocketDebuggerUrl"])),
            {
                "id": 1,
                "method": "Runtime.evaluate",
                "params": {"expression": expression, "awaitPromise": True, "returnByValue": True},
            },
            timeout,
        )
        if response.get("exceptionDetails"):
            raise AssertionError(f"browser eval exception for {url}: {response['exceptionDetails']}")
        result = response.get("result", {}).get("result", {}).get("value")
        if not isinstance(result, dict):
            raise AssertionError(f"browser eval returned non-object for {url}: {response}")
        return result
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)


def wait_for_debug_page(port: int, expected_url: str, proc: subprocess.Popen[str], timeout: float) -> dict[str, object]:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        if proc.poll() is not None:
            output = proc.stdout.read() if proc.stdout is not None else ""
            raise BrowserSmokeUnavailable(f"debug browser exited before evaluation\n{output[-2000:]}")
        try:
            status, text = request_text(f"http://127.0.0.1:{port}/json", timeout=2.0)
            if status == 200:
                pages = json.loads(text)
                for page in pages:
                    if str(page.get("url", "")).startswith(expected_url.split("#", 1)[0]):
                        return page
        except Exception as exc:  # noqa: BLE001 - startup diagnostics matter.
            last_error = str(exc)
        time.sleep(0.2)
    raise BrowserSmokeUnavailable(f"debug browser did not expose page for {expected_url}: {last_error}")


def devtools_call(parsed_url: urllib.parse.ParseResult, payload: dict[str, object], timeout: float) -> dict[str, object]:
    host = parsed_url.hostname or "127.0.0.1"
    port = parsed_url.port or 80
    path = parsed_url.path or "/"
    key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
    request = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        "\r\n"
    ).encode("ascii")
    with socket.create_connection((host, port), timeout=timeout) as sock:
        sock.settimeout(timeout)
        sock.sendall(request)
        response = b""
        while b"\r\n\r\n" not in response:
            response += sock.recv(4096)
        if b" 101 " not in response.split(b"\r\n", 1)[0]:
            raise AssertionError(f"DevTools websocket handshake failed: {response[:200]!r}")
        send_websocket_text(sock, json.dumps(payload))
        while True:
            message = recv_websocket_text(sock)
            parsed = json.loads(message)
            if parsed.get("id") == payload.get("id"):
                return parsed


def send_websocket_text(sock: socket.socket, text: str) -> None:
    data = text.encode("utf-8")
    header = bytearray([0x81])
    length = len(data)
    if length < 126:
        header.append(0x80 | length)
    elif length < 65536:
        header.extend([0x80 | 126, *struct.pack("!H", length)])
    else:
        header.extend([0x80 | 127, *struct.pack("!Q", length)])
    mask = secrets.token_bytes(4)
    masked = bytes(byte ^ mask[index % 4] for index, byte in enumerate(data))
    sock.sendall(bytes(header) + mask + masked)


def recv_websocket_text(sock: socket.socket) -> str:
    first = recv_exact(sock, 2)
    opcode = first[0] & 0x0F
    length = first[1] & 0x7F
    if length == 126:
        length = struct.unpack("!H", recv_exact(sock, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", recv_exact(sock, 8))[0]
    mask = b""
    if first[1] & 0x80:
        mask = recv_exact(sock, 4)
    data = recv_exact(sock, length)
    if mask:
        data = bytes(byte ^ mask[index % 4] for index, byte in enumerate(data))
    if opcode == 0x8:
        raise AssertionError("DevTools websocket closed before response")
    return data.decode("utf-8", errors="replace")


def recv_exact(sock: socket.socket, size: int) -> bytes:
    chunks = bytearray()
    while len(chunks) < size:
        chunk = sock.recv(size - len(chunks))
        if not chunk:
            raise AssertionError("DevTools websocket closed unexpectedly")
        chunks.extend(chunk)
    return bytes(chunks)


def assert_rendered(label: str, dom: str, needles: list[str]) -> None:
    lowered = dom.lower()
    if "common.errortitle" in lowered or "too much recursion" in lowered or "maximum call stack" in lowered:
        raise AssertionError(f"{label} rendered an error page or recursion failure\n{dom[-2000:]}")
    for needle in needles:
        if needle not in dom:
            raise AssertionError(f"{label} missing rendered marker {needle!r}\n{dom[-2000:]}")


def assert_playground_timeline_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        """
        (() => {
          const messages = document.querySelector("#messages");
          const timeline = document.querySelector(".run-timeline");
          const setup = document.querySelector(".run-brief");
          const output = document.querySelector(".run-output-stack");
          if (!messages || !timeline || !setup || !output) return { missing: true };
          if (!messages.dataset.smokeFilled) {
            messages.dataset.smokeFilled = "1";
            for (let i = 0; i < 36; i += 1) {
              const item = document.createElement("div");
              item.className = "timeline-event stage";
              item.innerHTML = '<div class="timeline-marker"></div><div class="timeline-card"><strong>Smoke timeline event</strong><p>Repeated event ' + i + '</p></div>';
              messages.appendChild(item);
            }
          }
          const messagesStyle = getComputedStyle(messages);
          const timelineStyle = getComputedStyle(timeline);
          const setupStyle = getComputedStyle(setup);
          return {
            missing: false,
            overflowY: messagesStyle.overflowY,
            setupOverflowY: setupStyle.overflowY,
            messagesHeight: Math.round(messages.getBoundingClientRect().height),
            setupHeight: Math.round(setup.getBoundingClientRect().height),
            timelineHeight: Math.round(timeline.getBoundingClientRect().height),
            messagesScrollHeight: Math.round(messages.scrollHeight),
            messagesClientHeight: Math.round(messages.clientHeight),
            messagesMaxHeight: messagesStyle.maxHeight,
            timelineMaxHeight: timelineStyle.maxHeight,
            outputTop: Math.round(output.getBoundingClientRect().top),
            timelineBottom: Math.round(timeline.getBoundingClientRect().bottom)
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("playground timeline layout missing expected nodes")
    if metrics.get("overflowY") not in {"auto", "scroll"}:
        raise AssertionError(f"playground timeline should scroll internally, got {metrics}")
    if metrics.get("setupOverflowY") in {"auto", "scroll"}:
        raise AssertionError(f"playground setup panel should show fully without internal scroll, got {metrics}")
    height = int(metrics.get("messagesHeight") or 0)
    if height < 240:
        raise AssertionError(f"playground timeline event list should have usable height, got {metrics}")
    if int(metrics.get("messagesScrollHeight") or 0) <= int(metrics.get("messagesClientHeight") or 0):
        raise AssertionError(f"playground timeline should keep overflowing events inside a scroll area, got {metrics}")
    setup_height = int(metrics.get("setupHeight") or 0)
    timeline_height = int(metrics.get("timelineHeight") or 0)
    if abs(setup_height - timeline_height) > 24:
        raise AssertionError(f"playground setup and timeline panels should have matching heights, got {metrics}")
    if int(metrics.get("outputTop") or 0) <= int(metrics.get("timelineBottom") or 0):
        raise AssertionError(f"playground output should remain below the bounded timeline, got {metrics}")


def assert_approval_queue_layout(browser: Path, base_url: str, timeout: float, profile_dir: Path) -> None:
    metrics = evaluate_browser_json(
        browser,
        f"{base_url}/console#approvals",
        """
        (() => {
          const list = document.querySelector("#approvalList");
          if (!list) return { missing: true };
          const card = document.createElement("button");
          card.type = "button";
          card.className = "item approval-card";
          card.innerHTML = '<div class="approval-card-head"><strong>write_file</strong><span class="badge warn">Tool</span></div><p>path=demo.py content=1200 chars</p><small>call-1</small>';
          list.appendChild(card);
          const listStyle = getComputedStyle(list);
          return {
            missing: false,
            listHeight: Math.round(list.getBoundingClientRect().height),
            cardHeight: Math.round(card.getBoundingClientRect().height),
            alignContent: listStyle.alignContent,
            gridAutoRows: listStyle.gridAutoRows
          };
        })()
        """,
        timeout,
        profile_dir,
    )
    if metrics.get("missing"):
        raise AssertionError("approval queue layout missing expected nodes")
    card_height = int(metrics.get("cardHeight") or 0)
    list_height = int(metrics.get("listHeight") or 0)
    if card_height <= 0 or list_height <= 0:
        raise AssertionError(f"approval queue layout did not measure correctly, got {metrics}")
    if card_height > 220 or card_height > max(220, list_height // 2):
        raise AssertionError(f"single approval card should not stretch to fill the queue, got {metrics}")


def zh_url(base_url: str, path: str) -> str:
    if "#" in path:
        before_hash, fragment = path.split("#", 1)
        separator = "&" if "?" in before_hash else "?"
        return f"{base_url}{before_hash}{separator}lang=zh#{fragment}"
    separator = "&" if "?" in path else "?"
    return f"{base_url}{path}{separator}lang=zh"


def main() -> int:
    args = parse_args()
    browser = find_browser(args.browser, args.required)
    if browser is None:
        print("browser smoke skipped: no Chrome/Edge/Chromium binary found")
        return 0

    port = args.port or free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = os.environ.copy()
    env.update(
        {
            "GOFLOW_BASE_URL": env.get("GOFLOW_BASE_URL", "http://127.0.0.1:9/v1"),
            "GOFLOW_API_KEY": env.get("GOFLOW_API_KEY", "smoke-key"),
            "GOFLOW_MODEL": env.get("GOFLOW_MODEL", "smoke-model"),
            "GOFLOW_BACKUP_BASE_URL": env.get("GOFLOW_BACKUP_BASE_URL", "http://127.0.0.1:9/v1"),
            "GOFLOW_BACKUP_API_KEY": env.get("GOFLOW_BACKUP_API_KEY", "smoke-key"),
            "GOFLOW_BACKUP_MODEL": env.get("GOFLOW_BACKUP_MODEL", "smoke-backup-model"),
        }
    )
    tmpdir = Path(tempfile.mkdtemp(prefix="goflow-browser-smoke-"))
    proc: subprocess.Popen[str] | None = None
    try:
        runtime_home = tmpdir / "runtime"
        cache_dir = tmpdir / "gocache"
        gotmp_dir = tmpdir / "gotmp"
        workspace = tmpdir / "workspace"
        profile_dir = tmpdir / "browser-profile"
        runtime_home.mkdir()
        cache_dir.mkdir()
        gotmp_dir.mkdir()
        workspace.mkdir()
        profile_dir.mkdir()
        config_path = write_smoke_runtime_config(runtime_home)
        env.setdefault("GOCACHE", str(cache_dir))
        env.setdefault("GOTMPDIR", str(gotmp_dir))
        binary = args.binary
        if not args.skip_build:
            binary = build_binary(tmpdir, env)
        if binary is None:
            raise AssertionError("--skip-build requires --binary")
        proc = subprocess.Popen(
            [
                str(binary),
                "--config",
                str(config_path),
                "--workspace",
                str(workspace),
                "--http",
                f":{port}",
            ],
            cwd=ROOT,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        wait_for_server(base_url, proc, args.timeout)
        try:
            console_dom = render_dom(browser, f"{base_url}/console", args.timeout, profile_dir)
            workflows_dom = render_dom(browser, f"{base_url}/workflows", args.timeout, profile_dir)
            playground_dom = render_dom(browser, f"{base_url}/console#playground", args.timeout, profile_dir)
            approvals_dom = render_dom(browser, f"{base_url}/console#approvals", args.timeout, profile_dir)
            catalog_dom = render_dom(browser, f"{base_url}/console#catalog", args.timeout, profile_dir)
            status_dom = render_dom(browser, f"{base_url}/console#status", args.timeout, profile_dir)
            workspace_dom = render_dom(browser, f"{base_url}/console#workspace", args.timeout, profile_dir)
            settings_dom = render_dom(browser, f"{base_url}/console#settings", args.timeout, profile_dir)
            playground_zh_dom = render_dom(browser, zh_url(base_url, "/console#playground"), args.timeout, profile_dir)
            approvals_zh_dom = render_dom(browser, zh_url(base_url, "/console#approvals"), args.timeout, profile_dir)
            workspace_zh_dom = render_dom(browser, zh_url(base_url, "/console#workspace"), args.timeout, profile_dir)
            status_zh_dom = render_dom(browser, zh_url(base_url, "/console#status"), args.timeout, profile_dir)
            settings_zh_dom = render_dom(browser, zh_url(base_url, "/console#settings"), args.timeout, profile_dir)
            settings_en_dom = render_dom(browser, f"{base_url}/console?lang=en#settings", args.timeout, profile_dir)
        except BrowserSmokeUnavailable as exc:
            if args.required:
                raise AssertionError(str(exc)) from exc
            print(f"browser smoke skipped: browser could not run in this host session: {exc}")
            return 0
        assert_rendered(
            "/console",
            console_dom,
            ["overview-launchpad", "side-status-card", "Build and run Agent workflows visually."],
        )
        assert_rendered(
            "/workflows",
            workflows_dom,
            ["workflow-palette-panel", "workflow-inspector-panel", "canvas-guide", "Workflow Studio"],
        )
        assert_rendered(
            "/console#playground",
            playground_dom,
            [
                "chat-grid",
                "playground-targets",
                "playground-timeline",
                "Run console",
                "Task request",
            ],
        )
        assert_playground_timeline_layout(browser, base_url, args.timeout, profile_dir)
        assert_approval_queue_layout(browser, base_url, args.timeout, profile_dir)
        assert_rendered(
            "/console#approvals",
            approvals_dom,
            [
                "approvals-grid",
                "approvals-queue",
                "approval-detail",
                "Pending approvals",
                "Approve or deny this request",
            ],
        )
        assert_rendered(
            "/console#catalog",
            catalog_dom,
            ["resource-shell", "resource-starter-panel", "resource-relation-panel", "Create a linked starter"],
        )
        assert_rendered(
            "/console#status",
            status_dom,
            ["status-hero", "status-mcp", "MCP tool pressure", "Tool pressure"],
        )
        assert_rendered(
            "/console#workspace",
            workspace_dom,
            [
                "workspace-boundary",
                "workspace-switcher",
                "Workspace boundary",
                'data-workspace-action-name="confirm"',
                "What this protects",
            ],
        )
        assert_rendered(
            "/console#settings",
            settings_dom,
            [
                "settings-update-panel",
                "settings-update-check",
                "Check latest release",
                "Check for updates",
                "Configuration health",
            ],
        )
        assert_rendered(
            "/console?lang=zh#playground",
            playground_zh_dom,
            [
                "chat-grid",
                "playground-targets",
                "playground-timeline",
                "任务运行控制台",
                "任务输入",
            ],
        )
        assert_rendered(
            "/console?lang=zh#approvals",
            approvals_zh_dom,
            [
                "approvals-grid",
                "approvals-queue",
                "approval-detail",
                "待审批",
                "批准或拒绝这次请求",
            ],
        )
        assert_rendered(
            "/console?lang=zh#workspace",
            workspace_zh_dom,
            [
                "workspace-boundary",
                "workspace-switcher",
                "工作区边界",
                'data-workspace-action-name="confirm"',
                "这里保护什么",
            ],
        )
        assert_rendered(
            "/console?lang=zh#status",
            status_zh_dom,
            [
                "status-hero",
                "status-cost",
                "运行健康与执行状态",
                "模型与成本诊断",
                "低成本路由调优",
            ],
        )
        assert_rendered(
            "/console?lang=zh#settings",
            settings_zh_dom,
            [
                "settings-update-panel",
                "settings-update-check",
                "检查最新版本",
                "检查更新",
                "配置健康",
            ],
        )
        assert_rendered(
            "/console?lang=en#settings",
            settings_en_dom,
            [
                "settings-update-panel",
                "settings-update-check",
                "Check latest release",
                "Check for updates",
                "Configuration health",
            ],
        )
        print(f"browser smoke passed at {base_url} with {browser}")
        return 0
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
