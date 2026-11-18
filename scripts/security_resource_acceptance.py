#!/usr/bin/env python3
"""Check shipped security workflows with local model fixtures and real MCP I/O.

No real target, paid model, public request or external notification is used.
This checks execution/data flow, not the model's professional judgement.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import tempfile
import threading
import time
import urllib.error
from http.server import ThreadingHTTPServer
from pathlib import Path

from golden_provider_helpers import decode_tool_result
from golden_scenario_1 import ScenarioProvider, request_json, run_checked, yaml_quote
from smoke_http_browser import free_port, wait_for_server

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = "2026-10-24T00:01:00Z login failed user=demo source=198.51.100.8\n2026-10-24T00:02:00Z login succeeded user=demo source=198.51.100.8\n"


class SecurityProvider(ScenarioProvider):
    requests: list[dict] = []
    alert = True
    channel_id = "local"
    late_alert = False

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        self.requests.append(payload)
        prompts = [str(m.get("content", "")) for m in payload.get("messages", []) if m.get("role") == "user"]
        matches = re.findall(r'Run workflow "[^"]+" stage "([^"]+)"', "\n".join(prompts))
        if not matches:
            self.send_error(400, "Missing workflow stage")
            return
        stage = matches[-1]
        results = [m for m in payload.get("messages", []) if m.get("role") == "tool"]
        if stage in {"notify", "notify-investigation"}:
            if not results:
                response = self._tool_call("notify-local", "notification_tools/publish_event", {"channel_id": self.channel_id, "severity": "warning", "title": "本地夹具：登录异常待复核", "summary": "演示日志中的失败后成功登录需要核查，不能据此确认入侵。", "evidence_refs": ["input/events.log"]})
            else:
                receipt, _ = decode_tool_result(results[-1].get("content"))
                response = self._text_response(json.dumps(receipt, ensure_ascii=False))
        elif stage == "report":
            delivery = "本场景未触发通知。" if not self.alert else ("本地 stored，不代表外部送达。" if self.channel_id == "local" else "通知失败，未送达。")
            report = "## 本地机制验收报告\n\n范围：仅 input/events.log 演示材料。\n\n发现：失败后成功登录需要核查。现有证据不能确认漏洞或入侵，不能推断攻击者身份。\n\n证据：input/events.log；没有连接主机或修改目标。\n\n限制：缺少身份核验、完整日志和资产上下文。\n\n建议：由使用者核对该登录是否获授权。\n\n通知：" + delivery
            report += "\n\n## 长正文显示夹具\n" + "\n".join(f"{i}. 此段只检查完整正文传递，不添加安全结论。演示材料保持只读，来源引用为 input/events.log；缺失身份和主机上下文，不能确认攻击者、漏洞或修复效果。" for i in range(1, 33))
            response = self._text_response(report + "\n\n报告结束：完整性夹具最后一段。")
        elif not results:
            response = self._tool_call(stage + "-read", "file_tools/read_file", {"path": "input/events.log"})
        else:
            alert_required = self.alert and (not self.late_alert or stage != "collect")
            response = self._text_response(json.dumps({"summary": "已核对给定演示材料；证据不足以确认漏洞或入侵。", "decision": "passed", "alert_required": alert_required, "severity": "warning" if alert_required else "info", "evidence_refs": ["input/events.log"], "findings": [], "timeline": ["00:01 登录失败", "00:02 登录成功"], "limitations": ["只有演示日志，不代表完整覆盖"], "collection_gaps": ["身份核验与完整主机证据缺失"], "recommendations": ["核查该登录是否获授权"]}, ensure_ascii=False))
        self._write_sse(response)


def wait_run(url: str, run_id: str, timeout: float, previous: dict | None = None) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        run = request_json(url + "/api/workflow-runs/" + run_id, timeout=15)
        changed = previous is None or (run.get("status"), run.get("updated_at")) != (previous.get("status"), previous.get("updated_at"))
        if changed and run.get("status") not in {"running", "queued", "cancelling"}:
            return run
        time.sleep(0.1)
    raise AssertionError("security workflow timed out")


def execute(binary: Path, timeout: float) -> dict:
    scenarios = []
    with tempfile.TemporaryDirectory(prefix="goflow-security-resources-") as temporary:
        base = Path(temporary)
        connectors = {}
        for name in ["file_tools", "notification_tools", "web_tools", "host_tools", "ssh_tools"]:
            output = base / (name + (".exe" if os.name == "nt" else ""))
            run_checked(["go", "build", "-o", str(output), "./mcp_servers/" + name], ROOT)
            connectors[name] = output
        for workflow in ["security-assessment", "security-incident-investigation"]:
            variants = [(False, "local", False), (True, "local", False), (True, "missing-fixture", False)]
            if workflow == "security-incident-investigation":
                variants += [(True, "local", True), (True, "missing-fixture", True)]
            for alert, channel_id, late_alert in variants:
                runtime = base / (workflow + ("-" + channel_id if alert else "-quiet") + ("-late" if late_alert else ""))
                workspace, data = runtime / "workspace", runtime / "data"
                (workspace / "input").mkdir(parents=True)
                material = workspace / "input/events.log"
                material.write_text(EVIDENCE, encoding="utf-8")
                for skill in ["security-assessment", "security-incident-investigation", "security-evidence-review", "event-notification"]:
                    shutil.copytree(ROOT / "skills" / skill, runtime / "skills" / skill)
                shutil.copytree(ROOT / "workflows" / workflow, runtime / "workflows" / workflow)
                (runtime / "configs/agents").mkdir(parents=True)
                for name in ["chat", "security-researcher", "auditor"]:
                    shutil.copy2(ROOT / "configs/agents" / (name + ".yaml"), runtime / "configs/agents" / (name + ".yaml"))
                SecurityProvider.requests, SecurityProvider.alert, SecurityProvider.channel_id = [], alert, channel_id
                SecurityProvider.late_alert = late_alert
                provider = ThreadingHTTPServer(("127.0.0.1", 0), SecurityProvider)
                provider.daemon_threads = True
                thread = threading.Thread(target=provider.serve_forever, daemon=True)
                thread.start()
                refs = ""
                for name, executable in connectors.items():
                    refs += f"""  - name: {name}
    command: {yaml_quote(str(executable))}
    enabled: true
    timeout: 20s
    isolation: process_group
    allowed_commands: [{yaml_quote(str(executable))}]
    env_allowlist: [PATH, SystemRoot, USERPROFILE, LOCALAPPDATA, TMP, TEMP, GOFLOW_DATA_ROOT]
"""
                config = runtime / "configs/goflow.yaml"
                config.write_text(f"""agent:
  name: 本地资源链路验收
  max_iterations: 8
  timeout: 1m
default_agent: chat
providers:
  primary:
    provider: openai-compatible
    base_url: http://127.0.0.1:{provider.server_port}/v1
    api_key: local-fixture
    model: local-security-fixture
    timeout: 20s
    retry_count: 0
skill:
  directory: ./skills
audit:
  enabled: true
verifier:
  enabled: false
session:
  max_history: 8
mcp_servers:
{refs}""", encoding="utf-8")
                env = os.environ.copy()
                env["GOFLOW_DATA_ROOT"] = str(data)
                for name in ["GOFLOW_HTTP_TOKEN", "GOFLOW_API_KEY"]:
                    env.pop(name, None)
                port = free_port()
                with (runtime / "runtime.log").open("w", encoding="utf-8") as log:
                    process = subprocess.Popen([str(binary), "--config", str(config), "--workspace", str(workspace), "--http", f"127.0.0.1:{port}"], cwd=runtime, env=env, stdout=log, stderr=subprocess.STDOUT)
                    try:
                        url = f"http://127.0.0.1:{port}"
                        try:
                            wait_for_server(url, process, min(timeout, 45))
                        except AssertionError as exc:
                            raise AssertionError((runtime / "runtime.log").read_text(encoding="utf-8")[-3000:]) from exc
                        accepted = request_json(url + "/api/runs", {"type": "workflow", "workflow": workflow, "input": "仅分析 input/events.log 演示材料，不连接主机，不修改输入。", "approve": True, "background": True, "permission_mode": "full"}, timeout=30)
                        run_id = accepted.get("run_id") or accepted.get("id")
                        run = wait_run(url, run_id, timeout)
                        assert run["status"] == "awaiting_input" and not SecurityProvider.requests, run["status"]
                        request_json(url + f"/api/workflow-runs/{run_id}/input", {"inputs": {"scope": "只读 input/events.log；本地夹具授权；不得连接外部目标。", "authorized": True, "channel_id": channel_id}, "background": True}, timeout=30)
                        run = wait_run(url, run_id, timeout, run)
                        if run["status"] == "awaiting_approval":
                            request_json(url + f"/api/workflow-runs/{run_id}/approve", {"background": True}, timeout=30)
                            run = wait_run(url, run_id, timeout, run)
                        delivery_failed = alert and channel_id != "local"
                        expected_status = "blocked" if delivery_failed else "completed"
                        if run["status"] != expected_status:
                            stages = [(s.get("stage"), s.get("status")) for s in run.get("completed_stages", [])]
                            gates = [s.get("outputs") for s in run.get("completed_stages", []) if s.get("stage") == "quality"]
                            raise AssertionError(f"{workflow} alert={alert} channel={channel_id}: expected {expected_status}, got {run['status']}; stages={stages}; quality={gates}")
                        stages = run.get("completed_stages", [])
                        names = [s.get("stage") for s in stages]
                        expected_notification = "notify-investigation" if late_alert else "notify"
                        assert (expected_notification in names) == alert, names
                        assert not ("notify" in names and "notify-investigation" in names), "duplicate alert for the same investigation"
                        assert all(n in names for n in ["collect", "review", "quality", "report"]), names
                        quality = next(s for s in stages if s.get("stage") == "quality")
                        quality_status = quality.get("outputs", {}).get("quality_status")
                        assert quality_status == ("failed" if delivery_failed else "passed"), quality.get("outputs")
                        if delivery_failed:
                            quality_events = [event for event in run.get("events", []) if event.get("reason") == "quality_gate_failed"]
                            assert quality_events and all(event.get("workflow_status") == "running" for event in quality_events), "quality gate announced a terminal state before the final report"
                        tools = [t for s in stages for t in (s.get("result") or {}).get("tool_results", [])]
                        assert sum(t.get("tool_name", "").endswith("read_file") for t in tools) >= 2, tools
                        errors = [t for t in tools if t.get("is_error") or t.get("suspended")]
                        assert len(errors) == int(delivery_failed) and all(t.get("tool_name") == "notification_tools/publish_event" for t in errors), errors
                        assert all(t.get("tool_name") in {"file_tools/read_file", "notification_tools/publish_event"} for t in tools), tools
                        events = list((data / "notifications/outbox").glob("*.json"))
                        assert len(events) == int(alert), events
                        if alert:
                            assert json.loads(events[0].read_text(encoding="utf-8"))["delivery_status"] == ("failed" if delivery_failed else "stored")
                        assert material.read_text(encoding="utf-8") == EVIDENCE, "source was modified"
                        final = next(s for s in stages if s.get("stage") == "report")["result"]["output"]
                        assert len(final) > 2048 and final.endswith("报告结束：完整性夹具最后一段。"), "final report was truncated"
                        workbench = request_json(url + f"/api/runs/{run_id}?view=workbench", timeout=15)
                        assert final in workbench.get("final_output", ""), "workbench omitted the full final report"
                        if delivery_failed:
                            assert "未送达" in final, "failure report hides delivery outcome"
                        scenarios.append({"workflow": workflow, "alert": alert, "late_alert": late_alert, "channel_id": channel_id, "status": run["status"], "quality_status": quality_status, "stages": names, "tools": [t.get("tool_name") for t in tools], "local_events": len(events), "source_sha256": hashlib.sha256(material.read_bytes()).hexdigest(), "final_output_chars": len(final), "provider_requests": len(SecurityProvider.requests)})
                    finally:
                        process.terminate()
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.kill(); process.wait(timeout=5)
                        provider.shutdown(); provider.server_close(); thread.join(timeout=5)
    return {"passed": True, "mode": "deterministic_local_provider", "real_model_quality_verified": False, "scenarios": scenarios}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=90)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    try:
        result = execute(args.binary.resolve(), args.timeout)
    except urllib.error.HTTPError as exc:
        raise AssertionError(exc.read().decode("utf-8", errors="replace")[:6000]) from exc
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
