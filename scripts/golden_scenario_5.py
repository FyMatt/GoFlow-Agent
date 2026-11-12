#!/usr/bin/env python3
"""Run the approved managed-configuration change and recovery Golden Scenario."""

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

from golden_scenario_2 import request_json, run_checked, start_app, stop_app, yaml_quote
from golden_provider_helpers import decode_tool_result
from smoke_http_browser import evaluate_browser_json, find_browser, free_port, wait_for_server


ROOT = Path(__file__).resolve().parents[1]
BEFORE = b'{\n  "service": {\n    "enabled": false,\n    "workers": 2\n  }\n}\n'
BEFORE_SHA = hashlib.sha256(BEFORE).hexdigest()
CHANGE_REPORT = "Managed configuration change completed with an automatic backup, verified after SHA, and recovery ID. No service was restarted."
RECOVERY_REPORT = "Managed configuration recovery completed. The original SHA was restored and the recovery post-check passed. No service was restarted."


class ScenarioProvider(BaseHTTPRequestHandler):
    requests: list[dict]
    change_evidence: dict
    recovery_evidence: dict
    managed_path: str
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
        messages = payload.get("messages") or []
        serialized = json.dumps(messages, ensure_ascii=False).lower()
        results = self._tool_results(messages)
        restore = "restore the managed configuration" in serialized
        if restore:
            response = self._recovery_response(results)
        else:
            response = self._change_response(results)
        self._write_sse(response)

    def _change_response(self, results: list[dict]) -> list[dict]:
        inspection = next((item for item in results if item.get("kind") == "managed_json_inspection"), None)
        change = next((item for item in results if item.get("kind") == "managed_json_change"), None)
        if inspection is None:
            return self._tool_call("inspect-managed", "host_tools/inspect_managed_json", {"path": self.managed_path})
        if change is None:
            return self._tool_call(
                "set-managed",
                "host_tools/set_managed_json_value",
                {
                    "path": self.managed_path,
                    "json_pointer": "/service/enabled",
                    "value": True,
                    "expected_sha256": inspection["sha256"],
                    "change_reason": "enable the managed fixture after operator approval",
                    "backup_required": True,
                    "post_check_required": True,
                },
            )
        type(self).change_evidence = change
        return self._text_response(CHANGE_REPORT)

    def _recovery_response(self, results: list[dict]) -> list[dict]:
        recovery = next((item for item in results if item.get("kind") == "managed_json_recovery"), None)
        if recovery is None:
            change = type(self).change_evidence
            return self._tool_call(
                "restore-managed",
                "host_tools/restore_managed_json_change",
                {
                    "change_id": change["change_id"],
                    "expected_sha256": change["after_sha256"],
                    "recovery_reason": "restore the verified fixture baseline",
                    "post_check_required": True,
                },
            )
        type(self).recovery_evidence = recovery
        return self._text_response(RECOVERY_REPORT)

    @staticmethod
    def _tool_results(messages: list[dict]) -> list[dict]:
        decoded: list[dict] = []
        for message in messages:
            if message.get("role") != "tool":
                continue
            item, _protected = decode_tool_result(message.get("content"))
            if item is not None:
                decoded.append(item)
        return decoded

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
            body = json.dumps({"choices": [choice], "usage": {"prompt_tokens": 64, "completion_tokens": 20}})
            self.wfile.write(f"data: {body}\n\n".encode("utf-8"))
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


def write_config(runtime_home: Path, provider_port: int) -> Path:
    config_dir = runtime_home / "configs"
    skill_dir = runtime_home / "skills" / "managed-local-config"
    config_dir.mkdir(parents=True, exist_ok=True)
    skill_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(ROOT / "skills" / "managed-local-config" / "SKILL.md", skill_dir / "SKILL.md")
    host_server = (ROOT / "mcp_servers" / "host_tools").as_posix()
    config = f"""
agent:
  name: GoFlow Managed Configuration Golden Scenario
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
    description: Bounded managed local configuration changes.
    provider: primary
    mode: fix
    tool_policy: confirm
    allowed_tool_kinds: [read, write]
    allowed_tools:
      - host_tools/inspect_managed_json
      - host_tools/set_managed_json_value
      - host_tools/restore_managed_json_change
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
    env_allowlist: [PATH, HOME, USERPROFILE, SystemRoot, ProgramData, LOCALAPPDATA, APPDATA, TMP, TEMP, GOPATH, GOCACHE, GOMODCACHE, GOFLOW_TARGET_ID, GOFLOW_HOST_MANAGED_CONFIG_ROOTS, GOFLOW_HOST_CHANGE_BACKUP_ROOT]
    isolation: process_group
    allowed_commands: [go]
    max_request_bytes: 65536
    max_response_bytes: 2097152
""".lstrip()
    path = config_dir / "goflow.yaml"
    path.write_text(config, encoding="utf-8")
    return path


def browser_start_task(browser: Path, base_url: str, prompt_text: str, previous_id: str, timeout: float, profile: Path, screenshot: Path) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const agent = document.querySelector('#agentSelect');
          const skill = document.querySelector('#skillSelect');
          const prompt = document.querySelector('#prompt');
          agent.value = 'operations-specialist';
          agent.dispatchEvent(new Event('change', {{bubbles: true}}));
          skill.value = 'managed-local-config';
          skill.dispatchEvent(new Event('change', {{bubbles: true}}));
          await new Promise(resolve => setTimeout(resolve, 250));
          const contextBeforeSend = document.querySelector('#taskExecutionContext')?.innerText || '';
          prompt.value = {json.dumps(prompt_text)};
          prompt.dispatchEvent(new Event('input', {{bubbles: true}}));
          document.querySelector('#send').click();
          const deadline = Date.now() + {int(timeout * 1000)};
          let run = null;
          while (Date.now() < deadline) {{
            const body = await fetch('/api/runs?type=agent&limit=10').then(response => response.json());
            run = (body.runs || []).find(item => item.id !== {json.dumps(previous_id)}) || null;
            if (run && String(run.status || '').toLowerCase() === 'awaiting_tool_approval') break;
            await new Promise(resolve => setTimeout(resolve, 250));
          }}
          const detail = run ? await fetch(`/api/runs/${{run.id}}?full=1`).then(response => response.json()) : null;
          const actions = run ? await fetch(`/api/runs/${{run.id}}/actions?envelope=1`).then(response => response.json()) : null;
          while (Date.now() < deadline && !document.querySelector('[data-agent-action="approve_tool"], [data-history-action="approve_tool"]')) {{
            document.querySelector('#runHistoryRefresh')?.click();
            await new Promise(resolve => setTimeout(resolve, 300));
            document.querySelector(`[data-history-run="agent:${{run?.id || ''}}"]`)?.click();
            await new Promise(resolve => setTimeout(resolve, 250));
          }}
          return {{
            run: detail || run,
            actions: actions?.actions || actions || [],
            contextBeforeSend,
            approvalVisible: Boolean(document.querySelector('[data-agent-action="approve_tool"], [data-history-action="approve_tool"]')),
            bodyText: document.body.innerText
          }};
        }})()
        """,
        timeout + 20,
        profile,
        screenshot_path=screenshot,
    )


def browser_approve_task(browser: Path, base_url: str, run_id: str, report: str, timeout: float, profile: Path, screenshot: Path, viewport: tuple[int, int] | None = None) -> dict:
    return evaluate_browser_json(
        browser,
        f"{base_url}/console#playground",
        f"""
        (async () => {{
          const deadline = Date.now() + {int(timeout * 1000)};
          let button = null;
          while (Date.now() < deadline) {{
            button = document.querySelector('[data-agent-action="approve_tool"], [data-history-action="approve_tool"]');
            if (button && !button.disabled) break;
            document.querySelector('#runHistoryRefresh')?.click();
            await new Promise(resolve => setTimeout(resolve, 300));
            document.querySelector('[data-history-run="agent:{run_id}"]')?.click();
            await new Promise(resolve => setTimeout(resolve, 250));
          }}
          const clicked = Boolean(button && !button.disabled);
          button?.click();
          let run = null;
          while (Date.now() < deadline) {{
            run = await fetch('/api/runs/{run_id}?full=1').then(response => response.json());
            if (['succeeded', 'completed', 'failed', 'cancelled'].includes(String(run.status || '').toLowerCase())) break;
            await new Promise(resolve => setTimeout(resolve, 300));
          }}
          while (Date.now() < deadline && !document.body.innerText.includes({json.dumps(report)})) {{
            await new Promise(resolve => setTimeout(resolve, 200));
          }}
          while (Date.now() < deadline) {{
            const composer = (document.querySelector('#runStatus')?.innerText || '').toLowerCase();
            const history = document.querySelector('#runHistoryList')?.innerText || '';
            if (!composer.includes('waiting') && !history.includes('Waiting for tool approval')) break;
            await new Promise(resolve => setTimeout(resolve, 250));
          }}
          if (innerWidth <= 500) {{
            document.querySelector('#resultOutput')?.scrollIntoView({{block: 'start'}});
            await new Promise(resolve => setTimeout(resolve, 300));
          }}
          return {{
            clicked,
            run,
            reportVisible: document.body.innerText.includes({json.dumps(report)}),
            composerStatus: document.querySelector('#runStatus')?.innerText || '',
            timelineText: document.querySelector('#messages')?.innerText || '',
            historyText: document.querySelector('#runHistoryList')?.innerText || '',
            viewportWidth: innerWidth,
            documentWidth: document.documentElement.scrollWidth
          }};
        }})()
        """,
        timeout + 20,
        profile,
        viewport=viewport,
        screenshot_path=screenshot,
    )


def approval_action(actions: object) -> dict | None:
    if not isinstance(actions, list):
        return None
    return next((item for item in actions if isinstance(item, dict) and item.get("name") == "approve_tool"), None)


def action_by_name(actions: object, name: str) -> dict | None:
    if not isinstance(actions, list):
        return None
    return next((item for item in actions if isinstance(item, dict) and item.get("name") == name), None)


def timeline_tool_names(base_url: str, run_id: str) -> set[str]:
    timeline = request_json(f"{base_url}/api/runs/{run_id}/timeline?limit=100&include_content=true")
    items = timeline.get("items", timeline) if isinstance(timeline, dict) else timeline
    return {str(item.get("tool_name", "")) for item in items if isinstance(item, dict) and item.get("tool_name")}


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
        print("golden scenario 5 skipped: no Chrome/Edge/Chromium binary found")
        return 0
    artifacts = ROOT / ".artifacts"
    artifacts.mkdir(exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="goflow-managed-golden-", dir=artifacts))
    workspace = temp_root / "workspace"
    runtime_home = temp_root / "runtime"
    managed_root = temp_root / "managed"
    backup_root = temp_root / "backups"
    for path in (workspace, runtime_home, managed_root, backup_root):
        path.mkdir()
    managed_path = managed_root / "service.json"
    managed_path.write_bytes(BEFORE)
    (workspace / "README.md").write_text("# Managed configuration fixture\n", encoding="utf-8")
    browser_profile = Path(tempfile.mkdtemp(prefix="goflow-managed-browser-"))
    provider_port = free_port()
    app_port = free_port()
    provider = ScenarioProvider
    provider.requests = []
    provider.change_evidence = {}
    provider.recovery_evidence = {}
    provider.managed_path = str(managed_path.resolve())
    provider_server = ThreadingHTTPServer(("127.0.0.1", provider_port), provider)
    threading.Thread(target=provider_server.serve_forever, daemon=True).start()
    app_process: subprocess.Popen[str] | None = None
    try:
        config_path = write_config(runtime_home, provider_port)
        binary = temp_root / ("goflow-managed-golden.exe" if os.name == "nt" else "goflow-managed-golden")
        build_env = os.environ.copy()
        build_env["GOCACHE"] = str(temp_root / "gocache")
        build_env["TMP"] = str(temp_root / "tmp")
        build_env["TEMP"] = str(temp_root / "tmp")
        Path(build_env["TMP"]).mkdir()
        build_env["GOFLOW_HOST_MANAGED_CONFIG_ROOTS"] = str(managed_root.resolve())
        build_env["GOFLOW_HOST_CHANGE_BACKUP_ROOT"] = str(backup_root.resolve())
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, build_env)
        app_process = start_app(binary, config_path, workspace, app_port, build_env)
        base_url = f"http://127.0.0.1:{app_port}"
        wait_for_server(base_url, app_process, args.timeout)

        change_paused = browser_start_task(
            browser, base_url,
            "Inspect the allowlisted service JSON, then enable /service/enabled with backup and post-check. Do not restart a service.",
            "", args.timeout, browser_profile, artifacts / "golden-scenario-5-change-approval.png",
        )
        change_run = change_paused.get("run") or {}
        change_id = str(change_run.get("id", ""))
        approve = approval_action(change_paused.get("actions"))
        remember = action_by_name(change_paused.get("actions"), "approve_remember_tool")
        if not change_id or str(change_run.get("status", "")).lower() != "awaiting_tool_approval" or not change_paused.get("approvalVisible"):
            raise RuntimeError(f"change did not pause in the Tasks approval UI: {change_paused}")
        if not approve or approve.get("available") is False or str((approve.get("risk") or {}).get("kind", "")) != "write":
            raise RuntimeError(f"change approval action lacks write risk evidence: {change_paused.get('actions')}")
        if not remember or remember.get("available") is not False or "Approve and remember" in str(change_paused.get("bodyText", "")):
            raise RuntimeError(f"managed change approval was incorrectly rememberable: {change_paused.get('actions')}")
        if managed_path.read_bytes() != BEFORE or any(backup_root.iterdir()):
            raise RuntimeError("managed file or backup directory changed before explicit approval")
        if "local machine" not in str(change_paused.get("contextBeforeSend", "")).lower():
            raise RuntimeError(f"managed-config Skill did not expose the local Target: {change_paused}")

        change_done = browser_approve_task(
            browser, base_url, change_id, CHANGE_REPORT, args.timeout, browser_profile,
            artifacts / "golden-scenario-5-change-completed.png",
        )
        if not change_done.get("clicked") or not change_done.get("reportVisible") or str((change_done.get("run") or {}).get("status", "")).lower() not in {"completed", "succeeded"}:
            raise RuntimeError(f"approved managed change did not complete in Tasks: {change_done}")
        if "waiting" in str(change_done.get("composerStatus", "")).lower():
            raise RuntimeError(f"completed managed change kept a stale approval state: {change_done}")
        changed = json.loads(managed_path.read_text(encoding="utf-8"))
        evidence = provider.change_evidence
        if changed.get("service", {}).get("enabled") is not True or changed.get("service", {}).get("workers") != 2:
            raise RuntimeError(f"managed JSON change was not bounded to the requested key: {changed}")
        if evidence.get("before_sha256") != BEFORE_SHA or evidence.get("backup_stored") is not True or evidence.get("post_check_passed") is not True:
            raise RuntimeError(f"managed change lacks backup or post-check evidence: {evidence}")
        if evidence.get("read_only") is not False:
            raise RuntimeError(f"managed change was incorrectly labeled read-only: {evidence}")
        journal = backup_root / str(evidence.get("change_id", ""))
        manifest = json.loads((journal / "manifest.json").read_text(encoding="utf-8"))
        if (journal / "before.json").read_bytes() != BEFORE or manifest.get("status") != "committed" or manifest.get("after_sha256") != evidence.get("after_sha256"):
            raise RuntimeError(f"managed change journal is incomplete: {manifest}")
        change_tools = timeline_tool_names(base_url, change_id)
        if not any(name.endswith("inspect_managed_json") for name in change_tools) or not any(name.endswith("set_managed_json_value") for name in change_tools):
            raise RuntimeError(f"change timeline lacks inspect/set tools: {change_tools}")

        recovery_paused = browser_start_task(
            browser, base_url,
            "Restore the managed configuration change from its verified backup and run the recovery post-check. Do not restart a service.",
            change_id, args.timeout, browser_profile, artifacts / "golden-scenario-5-recovery-approval.png",
        )
        recovery_run = recovery_paused.get("run") or {}
        recovery_id = str(recovery_run.get("id", ""))
        recovery_approve = approval_action(recovery_paused.get("actions"))
        recovery_remember = action_by_name(recovery_paused.get("actions"), "approve_remember_tool")
        if not recovery_id or recovery_id == change_id or str(recovery_run.get("status", "")).lower() != "awaiting_tool_approval" or not recovery_approve:
            raise RuntimeError(f"recovery did not pause for a second explicit approval: {recovery_paused}")
        if not recovery_remember or recovery_remember.get("available") is not False or "Approve and remember" in str(recovery_paused.get("bodyText", "")):
            raise RuntimeError(f"managed recovery approval was incorrectly rememberable: {recovery_paused.get('actions')}")
        if hashlib.sha256(managed_path.read_bytes()).hexdigest() != evidence.get("after_sha256") or manifest.get("status") != "committed":
            raise RuntimeError("managed file changed before recovery approval")

        recovery_done = browser_approve_task(
            browser, base_url, recovery_id, RECOVERY_REPORT, args.timeout, browser_profile,
            artifacts / "golden-scenario-5-recovery-completed-mobile.png", viewport=(390, 844),
        )
        if not recovery_done.get("clicked") or not recovery_done.get("reportVisible") or managed_path.read_bytes() != BEFORE:
            raise RuntimeError(f"approved recovery did not restore the original file: {recovery_done}")
        if "waiting" in str(recovery_done.get("composerStatus", "")).lower():
            raise RuntimeError(f"completed managed recovery kept a stale approval state: {recovery_done}")
        if int(recovery_done.get("documentWidth", 0)) > int(recovery_done.get("viewportWidth", 0)) + 1:
            raise RuntimeError(f"mobile recovery result has horizontal overflow: {recovery_done}")
        recovery_evidence = provider.recovery_evidence
        manifest = json.loads((journal / "manifest.json").read_text(encoding="utf-8"))
        if recovery_evidence.get("restored_sha256") != BEFORE_SHA or recovery_evidence.get("post_check") != "passed" or recovery_evidence.get("recovery_post_check_passed") is not True:
            raise RuntimeError(f"recovery result lacks restored SHA or post-check: {recovery_evidence}")
        if recovery_evidence.get("read_only") is not False:
            raise RuntimeError(f"managed recovery was incorrectly labeled read-only: {recovery_evidence}")
        if manifest.get("status") != "restored" or manifest.get("recovery_post_check_passed") is not True or not manifest.get("restored_at"):
            raise RuntimeError(f"recovery journal is incomplete: {manifest}")
        recovery_tools = timeline_tool_names(base_url, recovery_id)
        if not any(name.endswith("restore_managed_json_change") for name in recovery_tools):
            raise RuntimeError(f"recovery timeline lacks restore tool: {recovery_tools}")
        for run_id in (change_id, recovery_id):
            detail = request_json(f"{base_url}/api/runs/{run_id}?full=1")
            if detail.get("target_id") != "local":  # type: ignore[union-attr]
                raise RuntimeError(f"managed configuration run lacks target_id=local: {detail}")
            diffs = request_json(f"{base_url}/api/runs/{run_id}/diffs?include_content=true")
            if int(diffs.get("total", 0)) != 0:  # type: ignore[union-attr]
                raise RuntimeError(f"host change incorrectly recorded workspace diffs: {diffs}")

        print(json.dumps({
            "change_run_id": change_id,
            "recovery_run_id": recovery_id,
            "change_id": evidence.get("change_id"),
            "before_sha256": BEFORE_SHA,
            "after_sha256": evidence.get("after_sha256"),
            "backup_stored": True,
            "change_post_check": "passed",
            "recovery_post_check": "passed",
            "approvals": 2,
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
