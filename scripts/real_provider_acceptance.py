#!/usr/bin/env python3
"""Run real-provider Agent and Workflow acceptance against isolated fixtures.

The script uses the configured primary Provider and its existing secret
reference. It never accepts, prints, or writes a literal API key.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


class RunDriveTimeout(RuntimeError):
    def __init__(self, kind: str, run_id: str, snapshot: dict[str, Any]):
        self.kind, self.run_id, self.last_snapshot = kind, run_id, snapshot
        summary = {key: snapshot.get(key) for key in ("status", "next_stage", "updated_at")}
        super().__init__(f"{kind} run {run_id} timed out; last={json.dumps(summary, ensure_ascii=False)}")


TERMINAL = {"completed", "succeeded", "failed", "cancelled", "denied", "blocked", "action_failed"}
ACTION_PREFERENCE = (
    "approve_task_scope",
    "approve_all_tools",
    "approve_tool",
    "submit_input",
    "approve_stage",
    "approve_budget",
    "continue_output",
    "resume_sub_workflow",
    "retry",
)
READ_ONLY_TOOL_NAMES = {
    "read_file",
    "read_file_range",
    "list_tree",
    "list_dir",
    "search_files",
    "file_info",
    "repository_map",
    "inspect_office_document",
    "office_capabilities",
    "research_snapshot",
    "web_search",
    "fetch_url",
    "compile_research_evidence",
}


def sanitize_provider_message(value: Any) -> str:
    """Keep provider diagnostics useful without retaining credential fragments."""
    text = str(value or "")
    # Gateways often echo a masked suffix (for example ``****a613``), which is
    # still credential material. Keep only the HTTP status/reason when a raw
    # response body follows it.
    text = re.sub(r"(?i)(provider\s+returned\s+\d{3}\s+[^:]+):.*$", r"\1", text)
    text = re.sub(r"(?i)(api[\s_-]*key\s*[:=]\s*)[^,\s}\"']+", r"\1[REDACTED]", text)
    text = re.sub(r"(?i)(bearer\s+)[a-z0-9._~+/-]{8,}", r"\1[REDACTED]", text)
    text = re.sub(r"(?i)sk-[a-z0-9_-]{8,}", "[REDACTED]", text)
    return text


def sanitize_provider_value(value: Any) -> Any:
    if isinstance(value, dict):
        return {str(key): sanitize_provider_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [sanitize_provider_value(item) for item in value]
    if isinstance(value, str):
        return sanitize_provider_message(value)
    return value


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timeout", type=float, default=900.0, help="Timeout per Agent or Workflow run in seconds.")
    parser.add_argument("--keep-workspaces", action="store_true", help="Keep fixture workspaces for manual inspection.")
    parser.add_argument("--skip-provider-probe", action="store_true", help="Skip the four-capability Provider connection probe.")
    parser.add_argument("--max-auto-retries", type=int, default=1, help="Maximum automatic run retries when approval context is unavailable.")
    return parser.parse_args()


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def run_checked(command: list[str], cwd: Path, env: dict[str, str], timeout: float = 180.0) -> dict[str, Any]:
    started = time.monotonic()
    completed = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=timeout,
    )
    result = {
        "command": command,
        "returncode": completed.returncode,
        "duration_ms": round((time.monotonic() - started) * 1000),
        "output": completed.stdout[-12000:],
    }
    if completed.returncode != 0:
        raise RuntimeError(json.dumps(result, ensure_ascii=False, indent=2))
    return result


def request_json(
    base_url: str,
    path: str,
    payload: dict[str, Any] | None = None,
    timeout: float = 30.0,
) -> tuple[int, Any]:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    attempts = 3 if data is None else 1
    for attempt in range(attempts):
        request = urllib.request.Request(
            base_url + path,
            data=data,
            headers={"Content-Type": "application/json"} if data is not None else {},
            method="POST" if data is not None else "GET",
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                raw = response.read().decode("utf-8")
                return response.status, json.loads(raw) if raw.strip() else {}
        except urllib.error.HTTPError as error:
            raw = error.read().decode("utf-8", errors="replace")
            try:
                body: Any = json.loads(raw)
            except ValueError:
                body = {"error": raw.strip()}
            raise RuntimeError(f"{request.method} {path} returned {error.code}: {body}") from error
        except (TimeoutError, urllib.error.URLError, OSError):
            if attempt+1 >= attempts:
                raise
            time.sleep(0.25 * (attempt + 1))
    raise RuntimeError(f"request unexpectedly exhausted retries: {path}")


def wait_for_server(base_url: str, process: subprocess.Popen[str], timeout: float = 90.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"GoFlow exited before becoming ready with code {process.returncode}")
        try:
            status, body = request_json(base_url, "/api/runtime", timeout=2.0)
            if status == 200 and isinstance(body, dict):
                return
        except (OSError, RuntimeError):
            pass
        time.sleep(0.3)
    raise RuntimeError("GoFlow did not become ready before timeout")


def start_server(
    binary: Path,
    workspace: Path,
    port: int,
    env: dict[str, str],
    log_path: Path,
) -> tuple[subprocess.Popen[str], Any]:
    log_handle = log_path.open("a", encoding="utf-8")
    process = subprocess.Popen(
        [
            str(binary),
            "--config",
            str(ROOT / "configs" / env.get("GOFLOW_ACCEPTANCE_CONFIG", "goflow.yaml")),
            "--workspace",
            str(workspace),
            "--http",
            f"127.0.0.1:{port}",
        ],
        cwd=ROOT,
        env=env,
        stdout=log_handle,
        stderr=subprocess.STDOUT,
        text=True,
    )
    wait_for_server(f"http://127.0.0.1:{port}", process)
    return process, log_handle


def stop_server(process: subprocess.Popen[str] | None, log_handle: Any | None) -> None:
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
    if log_handle is not None:
        log_handle.close()


def write_text(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def init_git(workspace: Path, env: dict[str, str]) -> None:
    run_checked(["git", "init"], workspace, env)
    run_checked(["git", "config", "user.email", "acceptance@example.invalid"], workspace, env)
    run_checked(["git", "config", "user.name", "GoFlow Acceptance"], workspace, env)
    run_checked(["git", "add", "."], workspace, env)
    run_checked(["git", "commit", "-m", "acceptance fixture"], workspace, env)


def create_agent_fixture(workspace: Path, env: dict[str, str]) -> None:
    write_text(workspace / "go.mod", "module acceptance.local/agentfixture\n\ngo 1.22\n")
    write_text(
        workspace / "mathutil" / "clamp.go",
        """package mathutil

// Clamp returns value constrained to the inclusive bounds.
func Clamp(value, minValue, maxValue int) int {
    if value < minValue {
        return minValue
    }
    return value
}
""",
    )
    write_text(
        workspace / "mathutil" / "clamp_test.go",
        """package mathutil

import "testing"

func TestClampUpperBound(t *testing.T) {
    if got := Clamp(12, 1, 10); got != 10 {
        t.Fatalf("Clamp upper bound = %d, want 10", got)
    }
}

func TestClampInsideRange(t *testing.T) {
    if got := Clamp(6, 1, 10); got != 6 {
        t.Fatalf("Clamp inside range = %d, want 6", got)
    }
}
""",
    )
    run_checked(["gofmt", "-w", "."], workspace, env)
    init_git(workspace, env)


def add_agent_hidden_tests(workspace: Path) -> None:
    write_text(
        workspace / "mathutil" / "acceptance_hidden_test.go",
        """package mathutil

import "testing"

func TestAcceptanceClampLowerBound(t *testing.T) {
    if got := Clamp(-4, 1, 10); got != 1 {
        t.Fatalf("Clamp lower bound = %d, want 1", got)
    }
}

func TestAcceptanceClampReversedBounds(t *testing.T) {
    tests := []struct {
        value int
        want  int
    }{{-1, 2}, {5, 5}, {12, 9}}
    for _, test := range tests {
        if got := Clamp(test.value, 9, 2); got != test.want {
            t.Fatalf("Clamp(%d, 9, 2) = %d, want %d", test.value, got, test.want)
        }
    }
}
""",
    )


def create_workflow_fixture(workspace: Path, env: dict[str, str]) -> None:
    write_text(workspace / "go.mod", "module acceptance.local/workflowfixture\n\ngo 1.22\n")
    write_text(
        workspace / "ttlcache" / "cache.go",
        """package ttlcache

import (
    "errors"
    "sync"
    "time"
)

var ErrInvalidTTL = errors.New("ttl must be positive")

type Clock func() time.Time

type entry struct {
    value     []byte
    expiresAt time.Time
}

type Cache struct {
    mu    sync.RWMutex
    now   Clock
    items map[string]entry
}

func New(clock Clock) *Cache {
    return nil
}

func (c *Cache) Set(key string, value []byte, ttl time.Duration) error {
    return errors.New("not implemented")
}

func (c *Cache) Get(key string) ([]byte, bool) {
    return nil, false
}

func (c *Cache) Delete(key string) {
}

func (c *Cache) Len() int {
    return 0
}
""",
    )
    write_text(
        workspace / "ttlcache" / "cache_test.go",
        """package ttlcache

import (
    "errors"
    "testing"
    "time"
)

func TestSetGetAndExpiry(t *testing.T) {
    current := time.Unix(100, 0)
    cache := New(func() time.Time { return current })
    if err := cache.Set("key", []byte("value"), time.Second); err != nil {
        t.Fatal(err)
    }
    if got, ok := cache.Get("key"); !ok || string(got) != "value" {
        t.Fatalf("Get before expiry = %q, %v", got, ok)
    }
    current = current.Add(2 * time.Second)
    if _, ok := cache.Get("key"); ok {
        t.Fatal("expired key should not be returned")
    }
    if cache.Len() != 0 {
        t.Fatal("expired key should be removed")
    }
}

func TestInvalidTTLAndDelete(t *testing.T) {
    cache := New(nil)
    if err := cache.Set("key", []byte("value"), 0); !errors.Is(err, ErrInvalidTTL) {
        t.Fatalf("Set invalid TTL error = %v", err)
    }
    if err := cache.Set("key", []byte("value"), time.Minute); err != nil {
        t.Fatal(err)
    }
    cache.Delete("key")
    if _, ok := cache.Get("key"); ok {
        t.Fatal("deleted key should not be returned")
    }
}
""",
    )
    run_checked(["gofmt", "-w", "."], workspace, env)
    init_git(workspace, env)


def add_workflow_hidden_tests(workspace: Path) -> None:
    write_text(
        workspace / "ttlcache" / "acceptance_hidden_test.go",
        """package ttlcache

import (
    "fmt"
    "sync"
    "testing"
    "time"
)

func TestAcceptanceDefensiveCopies(t *testing.T) {
    cache := New(nil)
    original := []byte("value")
    if err := cache.Set("key", original, time.Minute); err != nil {
        t.Fatal(err)
    }
    original[0] = 'X'
    first, ok := cache.Get("key")
    if !ok || string(first) != "value" {
        t.Fatalf("stored value changed through caller slice: %q, %v", first, ok)
    }
    first[0] = 'Y'
    second, ok := cache.Get("key")
    if !ok || string(second) != "value" {
        t.Fatalf("stored value changed through returned slice: %q, %v", second, ok)
    }
}

func TestAcceptanceConcurrentAccess(t *testing.T) {
    cache := New(nil)
    var wait sync.WaitGroup
    for worker := 0; worker < 12; worker++ {
        worker := worker
        wait.Add(1)
        go func() {
            defer wait.Done()
            for index := 0; index < 80; index++ {
                key := fmt.Sprintf("%d-%d", worker, index)
                if err := cache.Set(key, []byte(key), time.Minute); err != nil {
                    t.Errorf("Set(%q): %v", key, err)
                    return
                }
                if value, ok := cache.Get(key); !ok || string(value) != key {
                    t.Errorf("Get(%q) = %q, %v", key, value, ok)
                    return
                }
                if index%3 == 0 {
                    cache.Delete(key)
                }
            }
        }()
    }
    wait.Wait()
}

func TestAcceptanceLenPurgesAllExpired(t *testing.T) {
    current := time.Unix(200, 0)
    cache := New(func() time.Time { return current })
    for _, key := range []string{"a", "b", "c"} {
        if err := cache.Set(key, []byte(key), time.Second); err != nil {
            t.Fatal(err)
        }
    }
    current = current.Add(2 * time.Second)
    if got := cache.Len(); got != 0 {
        t.Fatalf("Len after expiry = %d, want 0", got)
    }
}
""",
    )


def provider_probe(base_url: str, api_key_reference: str = "") -> dict[str, Any]:
    _, provider = request_json(base_url, "/api/resources/providers/primary")
    if not isinstance(provider, dict):
        raise RuntimeError("primary Provider resource response is invalid")
    safe_doc = dict(provider)
    safe_doc.pop("api_key", None)
    if api_key_reference:
        safe_doc["api_key"] = api_key_reference
    _, result = request_json(base_url, "/api/resources/providers/primary/test", safe_doc, timeout=300.0)
    if not isinstance(result, dict):
        raise RuntimeError(f"primary Provider probe returned an invalid response: {result}")
    capabilities = {str(item.get("name")): bool(item.get("supported")) for item in result.get("capabilities", [])}
    return {
        "valid": bool(result.get("valid")),
        "configured": bool(provider.get("api_key_set")),
        "status": result.get("status"),
        "message": sanitize_provider_message(result.get("message")),
        "model": result.get("model"),
        "latency_ms": result.get("latency_ms"),
        "capabilities": capabilities,
        "capability_details": sanitize_provider_value(result.get("capabilities", [])),
        "complete": provider_probe_complete(result, provider, capabilities),
    }


def provider_probe_complete(result: dict[str, Any], provider: dict[str, Any], capabilities: dict[str, bool]) -> bool:
    required = {"chat", "streaming", "tool_calling", "structured_output"}
    supported = {name for name, ok in capabilities.items() if ok}
    return (
        bool(result.get("valid"))
        and bool(provider.get("api_key_set"))
        and bool(str(result.get("model") or "").strip())
        and required.issubset(supported)
    )


def durable_change_paths(change: Any) -> set[str]:
    """Extract workspace-relative paths only from structured tool evidence."""
    if not isinstance(change, dict):
        return set()
    candidates = [change.get("path")]
    summary = change.get("summary")
    if isinstance(summary, str):
        try:
            decoded = json.loads(summary)
        except json.JSONDecodeError:
            decoded = None
        if isinstance(decoded, dict):
            candidates.append(decoded.get("path"))
    paths: set[str] = set()
    for candidate in candidates:
        path = str(candidate or "").strip().replace("\\", "/")
        marker = "/workspace/"
        if marker in path:
            path = path.rsplit(marker, 1)[1]
        path = path.strip("/")
        if not path or path in {"apply_patch", "write_file", "replace_text"}:
            continue
        if path.startswith("../") or "/../" in path or path == "..":
            continue
        paths.add(path)
    return paths


def action_items(body: Any) -> list[dict[str, Any]]:
    if isinstance(body, list):
        return [item for item in body if isinstance(item, dict)]
    if isinstance(body, dict):
        items = body.get("actions", [])
        return [item for item in items if isinstance(item, dict)] if isinstance(items, list) else []
    return []


def select_action(items: list[dict[str, Any]]) -> dict[str, Any] | None:
    available = [item for item in items if item.get("available")]
    for name in ACTION_PREFERENCE:
        for item in available:
            if item.get("name") == name:
                return item
    return None


def transient_tool_approval_conflict(action_name: Any, error: Any) -> bool:
    if str(action_name or "") not in {
        "approve_tool", "approve_all_tools", "approve_remember_tool", "approve_remember_all_tools",
        "approve-tools", "approve-remember-tool", "approve-remember-tools",
    }:
        return False
    message = str(error or "").lower()
    return any(term in message for term in (
        "not awaiting tool approval",
        "pending approval not found",
        "approval context is not available yet",
        "missing from the persisted agent run snapshot",
    ))


def submit_action(base_url: str, action: dict[str, Any]) -> dict[str, Any]:
    path = str(action.get("path", ""))
    if not path:
        raise RuntimeError(f"available action has no path: {action}")
    payload = {"background": True} if action.get("supports_background") or action.get("background") else {}
    if action.get("name") == "submit_input":
        scope = os.environ.get("GOFLOW_DELIVERY_SCOPE", "MVP").strip() or "MVP"
        payload["inputs"] = {"delivery_scope": scope}
    status, body = request_json(base_url, path, payload, timeout=20.0)
    if status not in {200, 202}:
        raise RuntimeError(f"action {action.get('name')} returned {status}: {body}")
    result = {"name": action.get("name"), "path": path, "status": status}
    if isinstance(body, dict):
        result["response"] = body
    return result


def run_exceeded_call_ceiling(body: dict[str, Any], limit: int) -> bool:
    """True when a live run already used more model calls than the ceiling.

    Acceptance runs once burned 255 calls on a looping case. A ceiling turns
    that into a bounded failure instead of an open-ended bill.
    """

    if limit <= 0 or not isinstance(body, dict):
        return False
    try:
        used = int(body.get("budget_llm_calls") or 0)
    except (TypeError, ValueError):
        return False
    return used > limit


def drive_run(
    base_url: str,
    kind: str,
    run_id: str,
    timeout: float,
    max_auto_retries: int = 1,
    on_first_workflow_approval: Any | None = None,
    on_first_workflow_tool_approval: Any | None = None,
    max_llm_calls: int = 0,
) -> tuple[dict[str, Any], list[dict[str, Any]], str]:
    prefix = "agent-runs" if kind == "agent" else "workflow-runs"
    deadline = time.monotonic() + timeout
    approvals: list[dict[str, Any]] = []
    retry_count = 0
    restarted = False
    uncertain_submission: tuple[str, str, float] | None = None
    submitted_action: tuple[str, str, float, str] | None = None
    no_action_since: float | None = None
    last: dict[str, Any] = {}
    while time.monotonic() < deadline:
        _, body = request_json(base_url, f"/api/{prefix}/{run_id}")
        if not isinstance(body, dict):
            raise RuntimeError(f"{kind} run detail is invalid: {body}")
        last = body
        if run_exceeded_call_ceiling(body, max_llm_calls):
            raise RuntimeError(
                f"{kind} run exceeded the acceptance call ceiling: {body.get('budget_llm_calls')} > {max_llm_calls}"
            )
        status = str(body.get("status", "")).lower()
        has_tool_approval = bool(
            body.get("pending_call_id")
            or body.get("pending_tool")
            or body.get("pending_tool_name")
            or body.get("pending_approvals")
        )
        if kind == "workflow" and status == "running" and has_tool_approval:
            status = "awaiting_tool_approval"
        terminal_is_settled = not (
            kind == "workflow"
            and status in {"completed", "succeeded"}
            and bool(str(body.get("next_stage") or "").strip())
        )
        if status in TERMINAL and terminal_is_settled:
            return body, approvals, run_id
        if status.startswith("awaiting_") or status.startswith("paused_"):
            restart_hook = None
            if kind == "workflow" and not restarted:
                if status == "awaiting_approval" and on_first_workflow_approval:
                    restart_hook = on_first_workflow_approval
                elif status == "awaiting_tool_approval":
                    restart_hook = on_first_workflow_tool_approval or on_first_workflow_approval
            if restart_hook:
                expected_waiting_status = status
                restarted_base = restart_hook()
                if restarted_base:
                    base_url = str(restarted_base)
                restarted = True
                _, restored = request_json(base_url, f"/api/{prefix}/{run_id}")
                restored_status = str(restored.get("status", "")).lower() if isinstance(restored, dict) else ""
                restored_waiting = restored_status == expected_waiting_status
                if expected_waiting_status == "awaiting_tool_approval":
                    restored_waiting = restored_waiting or (
                        isinstance(restored, dict)
                        and bool(restored.get("pending_call_id") or restored.get("pending_tool_name") or restored.get("pending_approvals"))
                    )
                if not restored_waiting:
                    raise RuntimeError(
                        f"workflow approval state {expected_waiting_status} was not restored after restart: {restored}"
                    )
                last = restored
                body = restored
            _, actions_body = request_json(base_url, f"/api/{prefix}/{run_id}/actions")
            action = select_action(action_items(actions_body))
            if action is None:
                # The run and action stores are durable independently. During
                # the short commit window an awaiting run can briefly expose
                # only its lifecycle action; poll before treating it as stuck.
                if no_action_since is None:
                    no_action_since = time.monotonic()
                if time.monotonic() - no_action_since < 20.0:
                    time.sleep(0.6)
                    continue
                raise RuntimeError(f"{kind} run requires action but none is available: status={status} actions={actions_body}")
            no_action_since = None
            action_signature = (
                str(action.get("name", "")),
                str(body.get("pending_call_id") or body.get("pending_tool_name") or action.get("path", "")),
            )
            action_state_version = "|".join(str(body.get(key, "")) for key in (
                "updated_at", "events_count", "next_stage", "status",
            ))
            if submitted_action is not None:
                submitted_name, submitted_call, submitted_at, submitted_state_version = submitted_action
                if (submitted_name, submitted_call) == action_signature:
                    if submitted_name == "continue_output" and submitted_state_version != action_state_version:
                        submitted_action = None
                    else:
                        # Provider continuation responses can legitimately take several
                        # minutes during a recovery workflow; do not classify a slow but
                        # durable action as a duplicate submission.
                        if time.monotonic() - submitted_at < 300.0:
                            time.sleep(0.6)
                            continue
                        raise RuntimeError(
                            f"{kind} run did not settle submitted action {submitted_name} for {submitted_call}"
                        )
                else:
                    submitted_action = None
            if uncertain_submission is not None:
                uncertain_name, uncertain_call, retry_after = uncertain_submission
                if (uncertain_name, uncertain_call) == action_signature and time.monotonic() < retry_after:
                    time.sleep(0.6)
                    continue
                uncertain_submission = None
            if action.get("name") == "retry" and retry_count >= max(0, max_auto_retries):
                diagnostics = {
                    "pending_call_id": body.get("pending_call_id"),
                    "pending_tool": body.get("pending_tool"),
                    "pending_approvals": body.get("pending_approvals"),
                    "resume_context": body.get("resume_context"),
                }
                raise RuntimeError(
                    f"{kind} run still has no resumable approval context after {retry_count} retries: "
                    f"status={status} run_id={run_id} diagnostics={diagnostics} actions={actions_body}"
                )
            try:
                submitted = submit_action(base_url, action)
                approvals.append(submitted)
                submitted_action = (*action_signature, time.monotonic(), action_state_version)
                response = submitted.get("response")
                if action.get("name") == "retry" and isinstance(response, dict) and response.get("run_id"):
                    retry_count += 1
                    run_id = str(response["run_id"])
                    submitted_action = None
            except (TimeoutError, urllib.error.URLError, OSError) as error:
                # Background approval may be accepted even if its HTTP
                # response arrives after the client timeout. Poll the durable
                # run state before considering a retry so the same tool call
                # is never approved twice concurrently.
                if action.get("name") not in {
                    "approve_task_scope",
                    "approve_tool", "approve_all_tools", "approve_remember_tool", "approve_remember_all_tools",
                    "approve-remember-tool", "approve-remember-tools",
                    "approve_stage", "continue_output", "approve_budget", "resume_sub_workflow",
                }:
                    raise
                uncertain_submission = (*action_signature, time.monotonic() + 45.0)
                approvals.append({
                    "name": action.get("name"),
                    "path": action.get("path"),
                    "status": "response_timeout_state_polled",
                    "error": str(error),
                })
            except RuntimeError as error:
                # The run detail and action endpoints are independently
                # persisted. A worker may settle an approval between those
                # reads; treat only this documented short-lived conflict as a
                # poll, then retrieve the fresh action set.
                message = str(error).lower()
                transient_retry = action.get("name") == "retry" and (
                    "another agent run is already active" in message or "agent run is still active" in message
                )
                transient_approval = transient_tool_approval_conflict(action.get("name"), message)
                if not transient_retry and not transient_approval:
                    raise
            time.sleep(0.6)
            continue
        time.sleep(0.5)
    raise RunDriveTimeout(kind, run_id, last)


def parse_event_at_ms(value: Any) -> int | None:
    """Parse an event timestamp into UTC milliseconds."""

    text = str(value or "").strip()
    if not text:
        return None
    if text.endswith("Z") or text.endswith("z"):
        text = text[:-1] + "+00:00"
    if "." in text:
        head, rest = text.split(".", 1)
        digits = ""
        tz = ""
        for index, char in enumerate(rest):
            if char.isdigit():
                digits += char
            else:
                tz = rest[index:]
                break
        text = f"{head}.{(digits + '000000')[:6]}{tz}"
    try:
        parsed = dt.datetime.fromisoformat(text)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return int(parsed.timestamp() * 1000)


def event_pair_duration_ms(events: list[dict[str, Any]]) -> dict[str, int]:
    """Measure real tool duration from tool_call/tool_result event timestamps."""

    started: dict[str, int] = {}
    durations: dict[str, int] = {}
    for item in events:
        if not isinstance(item, dict):
            continue
        call_id = str(item.get("tool_call_id") or item.get("call_id") or "").strip()
        stamp = parse_event_at_ms(item.get("at"))
        if not call_id or stamp is None:
            continue
        event_type = str(item.get("type") or "")
        if event_type in {"tool_call", "approval"}:
            started[call_id] = stamp
        elif event_type == "tool_result" and call_id in started:
            durations[call_id] = max(0, stamp - started[call_id])
    return durations


def observable_duration_ms(item: dict[str, Any], content: Any = None) -> int | None:
    """Return a non-negative tool duration from event fields or JSON content."""

    candidates: list[Any] = [item.get("duration_ms")]
    payload = content
    if payload is None:
        payload = item.get("content")
    if isinstance(payload, str):
        try:
            payload = json.loads(payload)
        except (TypeError, ValueError, json.JSONDecodeError):
            payload = None
    if isinstance(payload, dict):
        candidates.extend([payload.get("duration_ms"), payload.get("index_duration_ms")])
    for raw in candidates:
        if raw is None:
            continue
        try:
            duration = int(str(raw).strip())
        except (TypeError, ValueError):
            continue
        if duration >= 0:
            return duration
    return None


def collect_run_evidence(base_url: str, kind: str, run_id: str) -> dict[str, Any]:
    prefix = "agent-runs" if kind == "agent" else "workflow-runs"
    _, events = request_json(base_url, f"/api/{prefix}/{run_id}/events?limit=0&include_content=1")
    stages: Any = []
    if kind == "workflow":
        _, stages = request_json(base_url, f"/api/{prefix}/{run_id}/stages?limit=0&include_content=1")
    _, diffs = request_json(base_url, f"/api/{prefix}/{run_id}/diffs")
    _, artifacts = request_json(base_url, f"/api/{prefix}/{run_id}/artifacts?include_content=1")
    delivery_status, delivery = request_json(base_url, f"/api/runs/{run_id}/delivery")
    if delivery_status != 200 or not isinstance(delivery, dict):
        raise RuntimeError(f"run delivery manifest is unavailable: status={delivery_status}")
    event_items = events if isinstance(events, list) else events.get("items", events.get("events", []))
    diff_items = diffs if isinstance(diffs, list) else diffs.get("diffs", diffs.get("items", []))
    artifact_items = artifacts if isinstance(artifacts, list) else artifacts.get("items", artifacts.get("artifacts", []))
    event_items = event_items if isinstance(event_items, list) else []
    stage_items = stages if isinstance(stages, list) else stages.get("items", stages.get("stages", [])) if isinstance(stages, dict) else []
    stage_items = stage_items if isinstance(stage_items, list) else []
    diff_items = diff_items if isinstance(diff_items, list) else []
    artifact_items = artifact_items if isinstance(artifact_items, list) else []
    token_events = [item for item in event_items if item.get("type") == "token_usage"]
    # A stable argument hash is intentionally the only argument identity used
    # here.  The raw arguments can contain credentials or customer data and
    # must never be copied into acceptance evidence.  Count only a call that
    # repeats after a successful result; retries after errors/approvals are
    # legitimate recovery behavior and are not wasteful duplicates.
    call_by_id: dict[str, tuple[str, str, str]] = {}
    successful_signatures: set[tuple[str, str, str]] = set()
    duplicate_tool_calls = 0
    duplicate_full_reads = 0
    signature_samples = 0
    seen_call_signatures: set[tuple[str, str, str]] = set()
    seen_tool_call_ids: set[str] = set()
    recovery_event_types = {
        "workflow_approval_submitted",
        "workflow_output_continue_submitted",
        "agent_approval_submitted",
    }
    for item in event_items:
        if not isinstance(item, dict):
            continue
        event_type = str(item.get("type") or "")
        reason = str(item.get("reason") or "").strip().lower()
        content = str(item.get("content") or "").lower()
        if (
            event_type in recovery_event_types
            or event_type == "status"
            or reason in {"stage_retry", "continuation_started", "budget_hard_limit_hit"}
            or "retry attempt" in content
        ):
            # Approval resume, output continuation, and stage retries may replay
            # already-successful reads. Those are recovery, not waste.
            successful_signatures.clear()
            continue
        tool_name = str(item.get("tool_name") or "").strip()
        arguments_hash = str(item.get("arguments_hash") or "").strip()
        call_id = str(item.get("tool_call_id") or "").strip()
        stage = str(item.get("stage") or item.get("task_stage") or "").strip().lower()
        if event_type in {"tool_call", "approval"} and tool_name:
            if not arguments_hash:
                arguments_hash = call_id or f"missing:{len(call_by_id)}"
            if call_id:
                call_by_id[call_id] = (stage, tool_name, arguments_hash)
            else:
                call_by_id[f"{tool_name}:{arguments_hash}:{len(call_by_id)}"] = (stage, tool_name, arguments_hash)
            if event_type == "tool_call":
                if call_id:
                    if call_id in seen_tool_call_ids:
                        continue
                    seen_tool_call_ids.add(call_id)
                signature = (stage, tool_name, arguments_hash)
                signature_samples += 1
                if signature in successful_signatures:
                    short_name = tool_name.rsplit("/", 1)[-1]
                    if short_name == "read_file":
                        duplicate_full_reads += 1
                    elif short_name not in READ_ONLY_TOOL_NAMES:
                        duplicate_tool_calls += 1
                seen_call_signatures.add(signature)
        elif event_type == "tool_result" and tool_name:
            if not arguments_hash and call_id:
                stored = call_by_id.get(call_id)
                if stored:
                    stage, _, arguments_hash = stored
            if arguments_hash and not bool(item.get("is_error")) and not bool(item.get("suspended")):
                successful_signatures.add((stage, tool_name, arguments_hash))
    tool_names = {
        str(item.get("tool_name"))
        for item in event_items
        if item.get("type") in {"tool_call", "tool_result"} and item.get("tool_name")
    }
    stage_tool_results = 0
    stage_signatures: set[tuple[str, str, str]] = set()
    tool_durations: dict[str, int] = {}
    for stage in stage_items:
        if not isinstance(stage, dict):
            continue
        for container in (stage, stage.get("result"), stage.get("output")):
            if not isinstance(container, dict):
                continue
            results = container.get("tool_results", [])
            if not isinstance(results, list):
                continue
            for result_index, result in enumerate(results):
                if not isinstance(result, dict):
                    continue
                stage_tool_results += 1
                name = str(result.get("tool_name", "")).strip()
                if name:
                    tool_names.add(name)
                content = result.get("content")
                if name:
                    stage_signatures.add((
                        str(stage.get("stage") or stage.get("name") or ""),
                        name,
                        hashlib.sha256(str(content).encode("utf-8", errors="replace")).hexdigest()[:16],
                    ))
                duration = observable_duration_ms(result, content)
                if duration is None:
                    continue
                call_id = str(result.get("call_id") or result.get("tool_call_id") or "").strip()
                fallback = hashlib.sha256(f"{name}\0{content}\0{result_index}".encode("utf-8", errors="replace")).hexdigest()
                tool_durations[call_id or fallback] = duration
    for item in artifact_items:
        if not isinstance(item, dict) or str(item.get("kind") or "").strip().lower() != "tool_result":
            continue
        metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
        duration = observable_duration_ms({**item, "duration_ms": metadata.get("duration_ms", item.get("duration_ms"))}, item.get("content"))
        if duration is None:
            continue
        call_id = str(item.get("tool_call_id") or item.get("call_id") or item.get("id") or "").strip()
        fallback = hashlib.sha256(json.dumps(item, ensure_ascii=False, sort_keys=True).encode("utf-8", errors="replace")).hexdigest()
        tool_durations.setdefault(call_id or fallback, duration)
    # Some MCP adapters persist tool results only in the event stream. Keep
    # duration evidence independent of stage/artifact projection details.
    for item in event_items:
        if not isinstance(item, dict) or item.get("type") != "tool_result":
            continue
        duration = observable_duration_ms(item)
        if duration is None:
            continue
        call_id = str(item.get("tool_call_id") or item.get("call_id") or "").strip()
        fallback = hashlib.sha256(json.dumps(item, ensure_ascii=False, sort_keys=True).encode("utf-8", errors="replace")).hexdigest()
        tool_durations.setdefault(call_id or fallback, duration)
    for call_id, duration in event_pair_duration_ms(event_items).items():
        tool_durations.setdefault(call_id, duration)
    tool_duration_ms = sum(tool_durations.values())
    tool_duration_samples = len(tool_durations)
    delivery_metrics = delivery.get("metrics") if isinstance(delivery.get("metrics"), dict) else {}
    delivery_quality = delivery.get("quality") if isinstance(delivery.get("quality"), dict) else {}
    delivery_gates = delivery.get("hard_gates") if isinstance(delivery.get("hard_gates"), list) else []
    event_tool_results = sum(1 for item in event_items if item.get("type") == "tool_result")
    artifact_tool_results = sum(
        1 for item in artifact_items if isinstance(item, dict) and str(item.get("kind") or "").strip().lower() == "tool_result"
    )
    # Events are retained in a bounded window, so a long run can lose its tool
    # results there. Stage and artifact projections keep the same evidence;
    # take the largest reliable count instead of trusting one projection.
    reliable_tool_results = max(event_tool_results, stage_tool_results, artifact_tool_results)
    reliable_signature_samples = max(signature_samples, len(stage_signatures))
    return {
        "events": len(event_items),
        "event_types": sorted({str(item.get("type")) for item in event_items if item.get("type")}),
        "tool_calls": sum(1 for item in event_items if item.get("type") == "tool_call"),
        "tool_results": reliable_tool_results,
        "event_tool_results": event_tool_results,
        "tool_names": sorted(tool_names),
        "completed_stages": len(stage_items),
        "stage_tool_results": stage_tool_results,
        "token_events": len(token_events),
        "prompt_tokens": sum(int(item.get("prompt_tokens", 0) or 0) for item in token_events),
        "output_tokens": sum(int(item.get("output_tokens", 0) or 0) for item in token_events),
        "diffs": len(diff_items),
        "diff_paths": sorted({path for item in diff_items for path in durable_change_paths(item)}),
        "artifacts": len(artifact_items),
        "artifact_titles": sorted({str(item.get("title")) for item in artifact_items if item.get("title")}),
        "tool_duration_ms": tool_duration_ms,
        "tool_duration_samples": tool_duration_samples,
        "duplicate_tool_calls": duplicate_tool_calls,
        "duplicate_full_reads": duplicate_full_reads,
        "tool_signature_samples": reliable_signature_samples,
        "tool_signature_count": len(seen_call_signatures),
        "delivery_schema_version": delivery.get("schema_version"),
        "delivery_conclusion": delivery.get("conclusion"),
        "delivery_metrics": delivery_metrics,
        "delivery_quality": delivery_quality,
        "delivery_hard_gates": delivery_gates,
    }


def format_check(workspace: Path, env: dict[str, str]) -> dict[str, Any]:
    go_files = [str(path.relative_to(workspace)) for path in sorted(workspace.rglob("*.go"))]
    result = run_checked(["gofmt", "-l", *go_files], workspace, env)
    if result["output"].strip():
        raise RuntimeError(f"gofmt found unformatted files: {result['output']}")
    return result


def validate_workspace(workspace: Path, env: dict[str, str]) -> dict[str, Any]:
    return {
        "tests": run_checked(["go", "test", "./..."], workspace, env, timeout=180.0),
        "vet": run_checked(["go", "vet", "./..."], workspace, env, timeout=180.0),
        "format": format_check(workspace, env),
    }


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def completed_quality_gate(run: dict[str, Any], score: int) -> bool:
    return str(run.get("status", "")).lower() in {"completed", "succeeded"} and score >= 85


def quality_score(run: dict[str, Any], evidence: dict[str, Any], checks: dict[str, Any]) -> tuple[int, list[str]]:
    score = 0
    notes: list[str] = []
    if str(run.get("status", "")).lower() in {"completed", "succeeded"}:
        score += 15
    else:
        notes.append("run did not complete successfully")
    if checks.get("tests"):
        score += 40
    if checks.get("vet"):
        score += 10
    if checks.get("format"):
        score += 5
    if evidence.get("diffs", 0) > 0:
        score += 10
    else:
        notes.append("no durable file diff was recorded")
    if evidence.get("tool_results", 0) > 0:
        score += 10
    else:
        notes.append("no tool result evidence was recorded")
    summary = str(run.get("summary") or run.get("output") or "").strip()
    if summary or run.get("completed_stages"):
        score += 5
    calls = int(run.get("budget_llm_calls", 0) or evidence.get("token_events", 0))
    artifacts = int(evidence.get("artifacts", 0))
    if calls <= 20 and artifacts <= 30:
        score += 5
    else:
        notes.append(f"efficiency budget exceeded: llm_calls={calls}, artifacts={artifacts}")
    return score, notes


def markdown_report(report: dict[str, Any]) -> str:
    lines = [
        "# GoFlow Real Provider Acceptance",
        "",
        f"- Generated: `{report['generated_at']}`",
        f"- Overall: `{'PASS' if report['passed'] else 'FAIL'}`",
        f"- Provider model: `{report.get('provider', {}).get('model', 'not probed')}`",
        "",
        "## Results",
        "",
        "| Scenario | Status | Quality | Duration | Diffs | Tools | Tokens |",
        "| --- | --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for name in ("agent", "workflow"):
        item = report.get(name) or {}
        evidence = item.get("evidence") or {}
        lines.append(
            f"| {name} | {item.get('status', 'not-run')} | {item.get('quality_score', 0)} | "
            f"{item.get('duration_ms', 0)} ms | {evidence.get('diffs', 0)} | "
            f"{evidence.get('tool_results', 0)} | "
            f"{int(evidence.get('prompt_tokens', 0)) + int(evidence.get('output_tokens', 0))} |"
        )
    lines.extend(["", "## Hard Gates", ""])
    for gate, passed in report["hard_gates"].items():
        lines.append(f"- [{'x' if passed else ' '}] {gate}")
    lines.extend(["", "## Notes", ""])
    notes = report.get("notes", [])
    lines.extend(f"- {note}" for note in notes)
    if not notes:
        lines.append("- No residual acceptance warnings.")
    return "\n".join(lines) + "\n"


def main() -> int:
    args = parse_args()
    secret_path = ROOT / "secrets" / "providers" / "primary" / "api_key"
    if not secret_path.is_file() or secret_path.stat().st_size == 0:
        raise RuntimeError("configured primary Provider secret is missing")

    stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    artifact_dir = ROOT / ".artifacts" / "real-provider-acceptance" / stamp
    artifact_dir.mkdir(parents=True, exist_ok=True)
    temp_root = Path(tempfile.mkdtemp(prefix="fixtures-", dir=artifact_dir))
    agent_workspace = temp_root / "agent-workspace"
    workflow_workspace = temp_root / "workflow-workspace"
    agent_workspace.mkdir()
    workflow_workspace.mkdir()

    env = os.environ.copy()
    env["GOFLOW_DATA_ROOT"] = str(ROOT)
    # Respect explicit cache locations on Windows. Go subprocesses launched by
    # MCP servers inherit these values through the configured environment
    # allowlist; callers can point them at a writable local-temp directory.
    env["GOCACHE"] = os.environ.get("GOCACHE", str(artifact_dir / "gocache"))
    env["GOTMPDIR"] = os.environ.get("GOTMPDIR", str(artifact_dir / "gotmp"))
    Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    Path(env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)

    binary = artifact_dir / ("goflow-real-acceptance.exe" if os.name == "nt" else "goflow-real-acceptance")
    build = run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, env, timeout=240.0)
    create_agent_fixture(agent_workspace, env)
    create_workflow_fixture(workflow_workspace, env)

    report: dict[str, Any] = {
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "passed": False,
        "build": {key: value for key, value in build.items() if key != "output"},
        "provider": {},
        "agent": {},
        "workflow": {},
        "hard_gates": {},
        "notes": [],
    }

    process: subprocess.Popen[str] | None = None
    log_handle: Any | None = None
    try:
        agent_port = free_port()
        agent_log = artifact_dir / "agent-server.log"
        process, log_handle = start_server(binary, agent_workspace, agent_port, env, agent_log)
        agent_base = f"http://127.0.0.1:{agent_port}"
        if not args.skip_provider_probe:
            report["provider"] = provider_probe(agent_base)
            if not report["provider"].get("complete"):
                report["hard_gates"]["Provider chat, streaming, tool calling, and structured output are available"] = False
                raise RuntimeError(
                    "real Provider capability gate failed before task execution: "
                    f"status={report['provider'].get('status')} message={report['provider'].get('message')}"
                )
        else:
            report["provider"] = {"skipped": True, "reason": "provider probe disabled for task-only acceptance"}

        agent_prompt = (
            "Fix mathutil.Clamp so it constrains values to inclusive bounds and swaps reversed bounds before clamping. "
            "Inspect the code, make a focused implementation, add or update tests when useful, run gofmt -w on every changed Go file, run go test ./..., "
            "and report changed files plus real verification evidence."
        )
        agent_started_at = time.monotonic()
        status, accepted = request_json(
            agent_base,
            "/api/runs",
            {"input": agent_prompt, "agent": "software-engineer", "skill": "code-writing", "background": True},
            timeout=120.0,
        )
        if status != 202 or not isinstance(accepted, dict) or not accepted.get("run_id"):
            raise RuntimeError(f"Agent start failed: status={status} body={accepted}")
        agent_run, agent_approvals, agent_run_id = drive_run(
            agent_base, "agent", str(accepted["run_id"]), args.timeout, max_auto_retries=args.max_auto_retries
        )
        agent_duration = round((time.monotonic() - agent_started_at) * 1000)
        agent_evidence = collect_run_evidence(agent_base, "agent", agent_run_id)
        stop_server(process, log_handle)
        process, log_handle = None, None

        report["agent"] = {
            "run_id": agent_run_id,
            "initial_run_id": accepted["run_id"],
            "status": agent_run.get("status"),
            "duration_ms": agent_duration,
            "approvals": agent_approvals,
            "evidence": agent_evidence,
        }
        add_agent_hidden_tests(agent_workspace)
        run_checked(["gofmt", "-w", "mathutil/acceptance_hidden_test.go"], agent_workspace, env)
        agent_checks = validate_workspace(agent_workspace, env)
        agent_score, agent_notes = quality_score(agent_run, agent_evidence, agent_checks)
        report["agent"].update({
            "quality_score": agent_score,
            "quality_notes": agent_notes,
            "checks": {key: {k: v for k, v in value.items() if k != "output"} for key, value in agent_checks.items()},
            "deliverable": {"path": "mathutil/clamp.go", "sha256": sha256_file(agent_workspace / "mathutil" / "clamp.go")},
        })

        workflow_port = free_port()
        workflow_log = artifact_dir / "workflow-server.log"
        process, log_handle = start_server(binary, workflow_workspace, workflow_port, env, workflow_log)
        workflow_base = f"http://127.0.0.1:{workflow_port}"
        workflow_restart_state = {"done": False}

        def restart_workflow_server() -> str:
            nonlocal process, log_handle, workflow_port, workflow_base
            stop_server(process, log_handle)
            workflow_port = free_port()
            workflow_base = f"http://127.0.0.1:{workflow_port}"
            process, log_handle = start_server(binary, workflow_workspace, workflow_port, env, workflow_log)
            workflow_restart_state["done"] = True
            return workflow_base

        workflow_prompt = (
            "Software development: implement the ttlcache package as a production-quality, concurrency-safe in-memory TTL cache. "
            "New(nil) must use time.Now. Set must reject non-positive TTL with ErrInvalidTTL and defensively copy input. "
            "Get must lazily remove expired entries and return a defensive copy. Delete must be safe and Len must purge "
            "all expired entries before counting. Keep the public API, add focused tests if needed, run gofmt -w on every changed Go file, run go test ./... and "
            "go vet ./..., and provide exact implementation, verification, and audit evidence."
        )
        workflow_started_at = time.monotonic()
        status, accepted = request_json(
            workflow_base,
            "/api/runs",
            {"type": "auto", "input": workflow_prompt, "background": True, "approve": False},
            timeout=120.0,
        )
        if status != 202 or not isinstance(accepted, dict) or not accepted.get("run_id"):
            raise RuntimeError(f"Workflow start failed: status={status} body={accepted}")
        workflow_run, workflow_approvals, workflow_run_id = drive_run(
            workflow_base,
            "workflow",
            str(accepted["run_id"]),
            args.timeout,
            max_auto_retries=args.max_auto_retries,
            on_first_workflow_approval=restart_workflow_server,
            on_first_workflow_tool_approval=restart_workflow_server,
        )
        workflow_duration = round((time.monotonic() - workflow_started_at) * 1000)
        workflow_evidence = collect_run_evidence(workflow_base, "workflow", workflow_run_id)
        report["workflow"] = {
            "run_id": workflow_run_id,
            "initial_run_id": accepted["run_id"],
            "status": workflow_run.get("status"),
            "duration_ms": workflow_duration,
            "approvals": workflow_approvals,
            "restart_restored_tool_approval": workflow_restart_state["done"],
            "evidence": workflow_evidence,
            "budget": {
                "llm_calls": workflow_run.get("budget_llm_calls", 0),
                "prompt_tokens": workflow_run.get("budget_prompt_tokens", 0),
                "output_tokens": workflow_run.get("budget_output_tokens", 0),
                "total_tokens": workflow_run.get("budget_total_tokens", 0),
            },
        }
        stop_server(process, log_handle)
        process, log_handle = None, None

        add_workflow_hidden_tests(workflow_workspace)
        run_checked(["gofmt", "-w", "ttlcache/acceptance_hidden_test.go"], workflow_workspace, env)
        workflow_checks = validate_workspace(workflow_workspace, env)
        workflow_score, workflow_notes = quality_score(workflow_run, workflow_evidence, workflow_checks)
        report["workflow"].update({
            "quality_score": workflow_score,
            "quality_notes": workflow_notes,
            "checks": {key: {k: v for k, v in value.items() if k != "output"} for key, value in workflow_checks.items()},
            "deliverable": {"path": "ttlcache/cache.go", "sha256": sha256_file(workflow_workspace / "ttlcache" / "cache.go")},
        })

        report["hard_gates"] = {
            "Agent completed and passed hidden tests": completed_quality_gate(agent_run, agent_score),
            "Agent produced durable diff and tool evidence": agent_evidence["diffs"] > 0 and agent_evidence["tool_results"] > 0,
            "Workflow completed and passed hidden tests": completed_quality_gate(workflow_run, workflow_score),
            "Workflow approval survived service restart": report["workflow"]["restart_restored_tool_approval"],
            "Workflow produced durable diff and tool evidence": workflow_evidence["diffs"] > 0 and workflow_evidence["tool_results"] > 0,
        }
        if not args.skip_provider_probe:
            report["hard_gates"] = {
                "Provider chat, streaming, tool calling, and structured output are available": bool(
                    report.get("provider", {}).get("complete")
                ),
                **report["hard_gates"],
            }
        report["notes"] = agent_notes + workflow_notes
        report["passed"] = all(report["hard_gates"].values())
    except Exception as error:
        if isinstance(error, RunDriveTimeout):
            snapshot_path = artifact_dir / f"{error.kind}-timeout-snapshot.json"
            snapshot_path.write_text(json.dumps(sanitize_provider_value(error.last_snapshot), ensure_ascii=False, indent=2), encoding="utf-8")
            report[error.kind].update({"run_id": error.run_id, "status": error.last_snapshot.get("status"), "timeout_snapshot": str(snapshot_path)})
        report["notes"].append(str(error))
        raise
    finally:
        stop_server(process, log_handle)
        report_path = artifact_dir / "report.json"
        report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        (artifact_dir / "report.md").write_text(markdown_report(report), encoding="utf-8")
        if report["passed"] and not args.keep_workspaces:
            shutil.rmtree(temp_root, ignore_errors=True)
        print(json.dumps({"passed": report["passed"], "report": str(report_path)}, ensure_ascii=False, indent=2))

    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
