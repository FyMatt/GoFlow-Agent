#!/usr/bin/env python3
"""Execute P7 specialized cases through the real GoFlow Provider path."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

try:  # Package imports are used by tests and direct imports.
    from .acceptance_evidence import (
        acceptance_round_path,
        git_output,
        validate_acceptance_output_root,
        validate_acceptance_round,
        write_acceptance_round,
    )
    from .p7_case_evaluator import evaluate_p7_case
    from .p7_complex_cases import P7ComplexCase, cases
    from .real_provider_acceptance import (
        collect_run_evidence,
        drive_run,
        free_port,
        init_git,
        provider_probe,
        request_json,
        sanitize_provider_message,
        start_server,
        stop_server,
    )
    from .real_provider_matrix import apply_staged_changes_if_available
except ImportError:  # Direct ``python scripts/p7_provider_runner.py`` execution.
    from acceptance_evidence import (
        acceptance_round_path,
        git_output,
        validate_acceptance_output_root,
        validate_acceptance_round,
        write_acceptance_round,
    )
    from p7_case_evaluator import evaluate_p7_case
    from p7_complex_cases import P7ComplexCase, cases
    from real_provider_acceptance import (
        collect_run_evidence,
        drive_run,
        free_port,
        init_git,
        provider_probe,
        request_json,
        sanitize_provider_message,
        start_server,
        stop_server,
    )
    from real_provider_matrix import apply_staged_changes_if_available


ROOT = Path(__file__).resolve().parents[1]
ROUND_DIRECTORY = re.compile(r"第([1-9][0-9]*)轮")


def configured_primary_probe(*, skipped: bool = False) -> dict[str, Any]:
    """Record the configured Provider identity even when the live probe is skipped."""

    path = ROOT / "configs" / "providers" / "primary.yaml"
    model = ""
    provider = "openai-compatible"
    try:
        for line in path.read_text(encoding="utf-8").splitlines():
            stripped = line.strip()
            if stripped.startswith("model:"):
                model = stripped.split(":", 1)[1].strip()
            elif stripped.startswith("provider:"):
                provider = stripped.split(":", 1)[1].strip()
    except OSError:
        pass
    configured = bool(str(provider or "").strip()) and bool(str(model or "").strip())
    probe = {
        "provider": provider,
        "model": model,
        "valid": configured,
        "configured": configured,
        "complete": configured,
        "status": "configured",
    }
    if skipped:
        probe["skipped"] = True
        probe["message"] = "live probe skipped; using configured primary Provider identity"
    return probe


def stage_provider_secret(root: Path, data_root: Path, requested: str | Path) -> Path:
    """Copy one approved primary-provider secret into an isolated run root."""

    allowed_root = (root / "secrets" / "providers" / "primary").resolve()
    source = Path(requested).expanduser()
    if not source.is_absolute():
        source = root / source
    if source.is_symlink():
        raise ValueError("provider secret must be a regular non-symlink file")
    try:
        source = source.resolve(strict=True)
        source.relative_to(allowed_root)
    except (FileNotFoundError, OSError, ValueError) as error:
        raise ValueError("provider secret must be an existing file under secrets/providers/primary") from error
    if not source.is_file():
        raise ValueError("provider secret must be a regular file")
    if source.stat().st_size == 0 or source.stat().st_size > 512:
        raise ValueError("provider secret size is outside the accepted bounds")
    destination = data_root / "secrets" / "providers" / "primary" / "api_key"
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    try:
        destination.chmod(0o600)
    except OSError:
        # Windows ACLs are enforced by the filesystem; chmod is best effort.
        pass
    return destination


def stage_provider_secret_value(data_root: Path, value: str) -> Path:
    """Stage a secret supplied by the parent process without retaining its source."""

    value = str(value or "").strip()
    if not value or len(value) > 512 or "\r" in value or "\n" in value:
        raise ValueError("provider secret value is empty or outside the accepted bounds")
    destination = data_root / "secrets" / "providers" / "primary" / "api_key"
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(value + "\n", encoding="utf-8")
    try:
        destination.chmod(0o600)
    except OSError:
        pass
    return destination


class ResearchHandler(SimpleHTTPRequestHandler):
    """Serve the local source catalog and fail removed-source exactly once."""

    failed_once = False

    def do_GET(self) -> None:  # noqa: N802
        if self.path.rstrip("/").endswith("removed-source.html") and not self.__class__.failed_once:
            self.__class__.failed_once = True
            self.send_response(503)
            self.end_headers()
            self.wfile.write(b"temporary source failure")
            return
        super().do_GET()

    def log_message(self, _format: str, *_args: object) -> None:
        return


class ResearchServer:
    def __init__(self, workspace: Path) -> None:
        self.workspace = workspace
        self.server: ThreadingHTTPServer | None = None
        self.thread: threading.Thread | None = None

    def start(self) -> str:
        ResearchHandler.failed_once = False
        handler = lambda *args, **kwargs: ResearchHandler(*args, directory=str(self.workspace), **kwargs)
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host, port = self.server.server_address
        return f"http://{host}:{port}"

    def close(self) -> None:
        if self.server:
            self.server.shutdown()
            self.server.server_close()
        if self.thread:
            self.thread.join(timeout=5)


def run_checked(command: list[str], cwd: Path, env: dict[str, str], timeout: float = 240.0) -> None:
    completed = subprocess.run(
        command, cwd=cwd, env=env, text=True, encoding="utf-8", errors="replace",
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout, check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"command failed: {' '.join(command)}\n{completed.stdout[-8000:]}")


def cleanup_runtime_path(path: Path, attempts: int = 5) -> tuple[bool, str]:
    if not path.exists():
        return True, "runtime path is absent"
    error = ""
    for attempt in range(max(1, attempts)):
        try:
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink()
            return not path.exists(), "runtime path removed"
        except OSError as exc:
            error = str(exc)
            if path.is_dir():
                for item in [path, *path.rglob("*")]:
                    try:
                        item.chmod(0o700)
                    except OSError:
                        pass
            else:
                try:
                    path.chmod(0o600)
                except OSError:
                    pass
            if attempt + 1 < max(1, attempts):
                time.sleep(0.2 * (attempt + 1))
    return False, error or "runtime path still exists"


def build_binary(output: Path, env: dict[str, str]) -> None:
    run_checked(["go", "build", "-buildvcs=false", "-o", str(output), "./cmd/goflow"], ROOT, env)


def variant_index(case: P7ComplexCase, repetition: int, seed: int = 0) -> int:
    if not case.variant_mutators:
        return 0
    return (int(seed) + repetition - 1) % len(case.variant_mutators)


def prepare_case_resume(
    acceptance_workspace: Path,
    case: P7ComplexCase,
    target_repetitions: int,
    archive_stamp: str,
) -> tuple[set[int], Path | None]:
    """Return valid consecutive rounds or preserve an invalid series for restart."""

    case_root = acceptance_round_path(
        acceptance_workspace,
        domain=case.domain,
        case_id=case.id,
        repetition=1,
    ).parent
    if not case_root.is_dir():
        return set(), None
    round_dirs: dict[int, Path] = {}
    for path in case_root.iterdir():
        match = ROUND_DIRECTORY.fullmatch(path.name) if path.is_dir() else None
        if match:
            round_dirs[int(match.group(1))] = path
    if not round_dirs:
        return set(), None

    completed: set[int] = set()
    failure = ""
    numbers = sorted(round_dirs)
    for number in range(1, target_repetitions + 1):
        target = round_dirs.get(number)
        if target is None:
            break
        try:
            validate_acceptance_round(target, require_success=True)
            report = json.loads((target / "report.json").read_text(encoding="utf-8"))
            provider = report.get("provider")
            if (
                report.get("case_id") != case.id
                or report.get("domain") != case.domain
                or int(report.get("repetition", 0) or 0) != number
                or not isinstance(provider, dict)
                or provider.get("valid") is not True
                or provider.get("configured") is not True
                or provider.get("complete") is not True
                or provider.get("skipped") is True
            ):
                raise RuntimeError("round identity or Provider capability evidence is inconsistent")
        except (OSError, ValueError, RuntimeError) as error:
            failure = f"round {number} cannot continue the consecutive series: {error}"
            break
        completed.add(number)
    archive_numbers = [number for number in numbers if number <= target_repetitions and number not in completed]
    if not archive_numbers:
        return completed, None
    if not failure:
        failure = f"rounds {archive_numbers} are outside the validated consecutive prefix {sorted(completed)}"

    archive_root = case_root / "失败批次" / archive_stamp
    suffix = 1
    while archive_root.exists():
        suffix += 1
        archive_root = case_root / "失败批次" / f"{archive_stamp}-{suffix}"
    archive_root.mkdir(parents=True)
    for number in archive_numbers:
        round_dirs[number].rename(archive_root / round_dirs[number].name)
    (archive_root / "archive.json").write_text(
        json.dumps(
            {
                "schema_version": 1,
                "case_id": case.id,
                "domain": case.domain,
                "archived_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                "reason": failure,
                "rounds": archive_numbers,
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    return completed, archive_root


def format_go_fixtures(workspace: Path) -> None:
    """Format every Go fixture file so the baseline is gofmt-clean.

    Fixtures are written as compact source strings; leaving them unformatted
    makes ``gofmt -l .`` fail no matter what the agent does, which turns a
    fixture-hygiene problem into an unfixable delivery failure.
    """

    go_files = [path for path in workspace.rglob("*.go") if path.is_file()]
    if not go_files:
        return
    try:
        subprocess.run(
            ["gofmt", "-w", *[str(path) for path in go_files]],
            cwd=workspace, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
    except OSError:
        # No Go toolchain available: the case itself needs Go, so this only
        # happens on a host that cannot run the case anyway.
        return


def prepare_workspace(case: P7ComplexCase, parent: Path, env: dict[str, str], repetition: int, variant_seed: int = 0) -> Path:
    workspace = parent / f"repeat-{repetition}" / case.id
    if workspace.exists():
        shutil.rmtree(workspace)
    workspace.mkdir(parents=True)
    case.builder(workspace)
    if case.variant_mutators:
        case.variant_mutators[variant_index(case, repetition, variant_seed)](workspace)
    format_go_fixtures(workspace)
    init_git(workspace, env)
    return workspace


def find_staging_workspace(run_id: str, env: dict[str, str]) -> Path | None:
    """Locate the durable task copy for pre-delivery independent evaluation."""

    data_root = Path(str(env.get("GOFLOW_DATA_ROOT") or ROOT)).resolve()
    candidate = data_root / "task-staging" / str(run_id).strip() / "workspace"
    return candidate if candidate.is_dir() else None


def remove_hidden(case: P7ComplexCase, workspace: Path) -> None:
    """Remove evaluator-only files before any staged delivery can be applied."""

    for relative in case.hidden_files:
        path = workspace / relative
        if path.is_file() or path.is_symlink():
            path.unlink()
        elif path.is_dir():
            shutil.rmtree(path)


def hidden_paths_present(case: P7ComplexCase, workspace: Path) -> list[str]:
    leaked: list[str] = []
    for relative in case.hidden_files:
        if (workspace / relative).exists():
            leaked.append(relative.replace("\\", "/"))
    return leaked


def evaluate_on_isolated_copy(
    case: P7ComplexCase,
    workspace: Path,
    evidence: dict[str, Any] | None = None,
    run: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Run hidden acceptance on a disposable copy of the durable tree.

    The source workspace is never mutated, so hidden files cannot be written
    back through the public apply-staged API.
    """

    with tempfile.TemporaryDirectory(prefix="goflow-p7-hidden-") as raw:
        eval_root = Path(raw) / "workspace"
        copy_workspace_for_hidden_eval(workspace, eval_root)
        return evaluate_p7_case(case, eval_root, evidence=evidence, run=run)


def copy_workspace_for_hidden_eval(src: Path, dst: Path) -> None:
    """Copy current file bytes, skipping git and runtime dirs that can stale a worktree copy."""

    dst.mkdir(parents=True, exist_ok=True)
    skip = {".git", ".goflow", ".cache"}
    for path in src.rglob("*"):
        rel = path.relative_to(src)
        if any(part in skip for part in rel.parts):
            continue
        dest = dst / rel
        if path.is_dir():
            dest.mkdir(parents=True, exist_ok=True)
            continue
        if path.is_file() and not path.is_symlink():
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(path.read_bytes())


WORKSPACE_CONFLICT_PATH = re.compile(r"workspace conflict for ([^:]+):")


def restore_conflicted_workspace_files(workspace: Path | None, error: str) -> list[str]:
    """Restore conflicted real-workspace files to HEAD so staged apply can replay."""

    if workspace is None or not workspace.is_dir():
        return []
    restored: list[str] = []
    for match in WORKSPACE_CONFLICT_PATH.finditer(str(error or "")):
        relative = match.group(1).strip().replace("\\", "/")
        if not relative or ".." in Path(relative).parts:
            continue
        git_output(workspace, "checkout", "HEAD", "--", relative)
        restored.append(relative)
    return restored


def verify_staging_then_apply(
    case: P7ComplexCase,
    staging_workspace: Path | None,
    base_url: str,
    run_id: str,
    approvals: list[dict[str, Any]],
    evidence: dict[str, Any],
    run: dict[str, Any],
    workspace: Path | None = None,
) -> tuple[dict[str, Any], bool]:
    """Hidden-eval the durable copy, then call the public apply API only on pass."""

    if staging_workspace is None or not staging_workspace.is_dir():
        return {"passed": False, "failures": ["durable task-staging workspace is missing"]}, False
    staging_evaluation = evaluate_on_isolated_copy(case, staging_workspace, evidence=evidence, run=run)
    leaked = hidden_paths_present(case, staging_workspace)
    if leaked:
        remove_hidden(case, staging_workspace)
        staging_evaluation = dict(staging_evaluation)
        staging_evaluation["passed"] = False
        failures = list(staging_evaluation.get("failures") or [])
        failures.append("hidden acceptance files leaked into durable staging: " + ", ".join(leaked))
        staging_evaluation["failures"] = failures
        return staging_evaluation, False
    if not staging_evaluation.get("passed"):
        return staging_evaluation, False
    try:
        applied = apply_staged_changes_if_available(base_url, "workflow", run_id, approvals)
        if applied:
            return staging_evaluation, True
        if workspace is not None:
            copied = copy_staging_workspace_into_live(staging_workspace, workspace, case)
            if copied:
                staging_evaluation = dict(staging_evaluation)
                staging_evaluation["writeback_fallback"] = "copied_staging_workspace"
                return staging_evaluation, True
        return staging_evaluation, False
    except RuntimeError as error:
        message = str(error)
        staging_evaluation = dict(staging_evaluation)
        if workspace is not None and "workspace conflict" in message:
            restored = restore_conflicted_workspace_files(workspace, message)
            staging_evaluation["restored_conflict_paths"] = restored
            try:
                return staging_evaluation, apply_staged_changes_if_available(base_url, "workflow", run_id, approvals)
            except RuntimeError as retry_error:
                staging_evaluation["apply_error"] = str(retry_error)
                return staging_evaluation, False
        staging_evaluation["apply_error"] = message
        return staging_evaluation, False


def copy_staging_workspace_into_live(staging_workspace: Path, workspace: Path, case: P7ComplexCase) -> bool:
    """Copy independently verified staged files into the live workspace when apply is unavailable."""

    staging_workspace = staging_workspace.resolve()
    workspace = workspace.resolve()
    if not staging_workspace.is_dir() or staging_workspace == workspace:
        return False
    hidden = {Path(path).as_posix() for path in case.hidden_files}
    copied = False
    for path in staging_workspace.rglob("*"):
        if not path.is_file():
            continue
        relative = path.relative_to(staging_workspace).as_posix()
        if relative in hidden or relative.split("/", 1)[0] in {".git", ".goflow", ".cache"}:
            continue
        destination = workspace / path.relative_to(staging_workspace)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
        copied = True
    return copied


def adopt_live_workspace_delivery(
    staging_evaluation: dict[str, Any],
    evaluation: dict[str, Any],
    delivery_applied: bool,
) -> tuple[dict[str, Any], bool]:
    """If writes already landed in the real workspace, missing staging is not a fail."""

    failures = list(staging_evaluation.get("failures") or [])
    if delivery_applied or not evaluation.get("passed"):
        return staging_evaluation, delivery_applied
    if "durable task-staging workspace is missing" not in failures:
        return staging_evaluation, delivery_applied
    adopted = dict(evaluation)
    adopted["passed"] = True
    adopted["live_workspace_delivery"] = True
    return adopted, True


def provider_temporarily_unavailable(result: dict[str, Any]) -> bool:
    text = " ".join(
        str(part or "")
        for part in (
            result.get("execution_error"),
            result.get("status"),
            (result.get("provider_run") or {}).get("summary") if isinstance(result.get("provider_run"), dict) else "",
        )
    ).lower()
    return (
        "503" in text
        or "service temporarily unavailable" in text
        or "429 too many requests" in text
        or "524" in text
        or "no such host" in text
        or "temporary failure in name resolution" in text
        or "dial tcp" in text
        or "server misbehaving" in text
        or "winerror 10054" in text
        or "wsarecv" in text
        or "forcibly closed by the remote host" in text
        or "远程主机强迫关闭了一个现有的连接" in text
        or "context deadline exceeded" in text
        or "client.timeout" in text
        or "protocol_error" in text
    )


def provider_probe_retryable(probe: dict[str, Any]) -> bool:
    if probe.get("complete"):
        return False
    text = json.dumps(probe, ensure_ascii=False).lower()
    return any(
        marker in text
        for marker in (
            "provider returned an empty",
            "context deadline exceeded",
            "client.timeout",
            "timeout",
            "timed out",
            "429 too many requests",
            "provider returned 400 bad request",
            "502 bad gateway",
            "503 service unavailable",
            "504 gateway timeout",
            "provider returned 524",
            "connection reset",
            "winerror 10054",
            "wsarecv",
            "forcibly closed by the remote host",
            "远程主机强迫关闭了一个现有的连接",
            "protocol_error",
        )
    )


def provider_probe_with_retries(base_url: str, api_key_reference: str = "", attempts: int = 3) -> dict[str, Any]:
    attempts = max(1, attempts)
    probe: dict[str, Any] = {}
    for attempt in range(1, attempts + 1):
        try:
            probe = dict(provider_probe(base_url, api_key_reference=api_key_reference))
        except Exception as error:
            identity = configured_primary_probe()
            probe = {
                "valid": False,
                "configured": identity.get("configured", False),
                "status": "error",
                "message": sanitize_provider_message(error),
                "model": identity.get("model", ""),
                "capabilities": {},
                "complete": False,
            }
        probe["attempts"] = attempt
        if probe.get("complete") or not provider_probe_retryable(probe) or attempt == attempts:
            return probe
        time.sleep(2 ** (attempt - 1))
    return probe


def terminal_delivery_recovered(
    execution_error: str,
    run: dict[str, Any],
    staging_evaluation: dict[str, Any],
    evaluation: dict[str, Any],
    delivery_applied: bool,
) -> bool:
    return (
        bool(execution_error)
        and provider_temporarily_unavailable({"execution_error": execution_error})
        and str(run.get("status", "")).lower() in {"completed", "succeeded"}
        and staging_evaluation.get("passed") is True
        and evaluation.get("passed") is True
        and delivery_applied
    )


def run_case(
    case: P7ComplexCase,
    workspace: Path,
    binary: Path,
    env: dict[str, str],
    timeout: float,
    artifact_dir: Path,
) -> dict[str, Any]:
    port = free_port()
    log_path = artifact_dir / f"{case.id}.log"
    process = log_handle = None
    research_server: ResearchServer | None = None
    base_url = ""
    run_id = ""
    run: dict[str, Any] = {"status": "failed"}
    approvals: list[dict[str, Any]] = []
    evidence: dict[str, Any] = {}
    evidence_error = ""
    staging_workspace: Path | None = None
    staging_evaluation: dict[str, Any] = {}
    execution_error = ""
    recovered_execution_error = ""
    restart_state = {"done": False}
    started_at = dt.datetime.now(dt.timezone.utc).isoformat()
    started = time.monotonic()
    baseline_hash = git_output(workspace, "rev-parse", "HEAD").strip()
    prompt = case.prompt
    try:
        if case.domain == "research":
            research_server = ResearchServer(workspace)
            web_base = research_server.start()
            # The fixture origin is loopback; the public-document guard rejects
            # non-public addresses unless this explicit acceptance switch is set.
            env["GOFLOW_RESEARCH_ALLOW_LOCAL"] = "1"
            prompt = case.prompt.format(base_url=web_base)
        else:
            prompt = case.prompt
        process, log_handle = start_server(binary, workspace, port, env, log_path)
        base_url = f"http://127.0.0.1:{port}"

        def restart() -> None:
            nonlocal process, log_handle
            stop_server(process, log_handle)
            process, log_handle = start_server(binary, workspace, port, env, log_path)
            restart_state["done"] = True

        status, accepted = request_json(
            base_url,
            f"/api/workflows/{case.workflow}",
            {"input": prompt, "background": True, "approve": False},
            timeout=120.0,
        )
        if status != 202 or not isinstance(accepted, dict) or not accepted.get("run_id"):
            raise RuntimeError(f"case start failed: status={status} body={accepted}")
        run_id = str(accepted["run_id"])
        run, approvals, run_id = drive_run(
            base_url,
            "workflow",
            run_id,
            timeout,
            max_auto_retries=1,
            on_first_workflow_approval=restart,
            # Hard ceiling: a looping case used to burn hundreds of calls (and
            # several yuan) before any other guard stopped it.
            max_llm_calls=max(80, case.llm_call_budget * 2),
        )
        try:
            evidence = collect_run_evidence(base_url, "workflow", run_id)
        except Exception as error:  # noqa: BLE001 - recorded, never silently dropped
            evidence = {}
            evidence_error = f"{type(error).__name__}: {error}"
        staging_workspace = find_staging_workspace(run_id, env)
        staging_evaluation, delivery_applied = verify_staging_then_apply(
            case, staging_workspace, base_url, run_id, approvals, evidence, run, workspace=workspace
        )
        evaluation = evaluate_on_isolated_copy(case, workspace, evidence=evidence, run=run)
        staging_evaluation, delivery_applied = adopt_live_workspace_delivery(
            staging_evaluation, evaluation, delivery_applied
        )
        evaluation["staging_evaluation"] = staging_evaluation
        if hidden_paths_present(case, workspace):
            remove_hidden(case, workspace)
    except Exception as error:
        execution_error = str(error)
        delivery_applied = False
        if base_url and run_id:
            try:
                _, run = request_json(base_url, f"/api/workflow-runs/{run_id}")
            except Exception:
                pass
            try:
                evidence = collect_run_evidence(base_url, "workflow", run_id)
            except Exception as error:  # noqa: BLE001 - recorded, never silently dropped
                evidence_error = f"{type(error).__name__}: {error}"
        if str(run.get("status", "")).lower() in {"completed", "succeeded"}:
            staging_workspace = staging_workspace or find_staging_workspace(run_id, env)
            try:
                staging_evaluation, delivery_applied = verify_staging_then_apply(
                    case, staging_workspace, base_url, run_id, approvals, evidence, run, workspace=workspace
                )
            except Exception:
                delivery_applied = False
        evaluation = evaluate_on_isolated_copy(case, workspace, evidence=evidence, run=run)
        if staging_evaluation:
            staging_evaluation, delivery_applied = adopt_live_workspace_delivery(
                staging_evaluation, evaluation, delivery_applied
            )
            evaluation["staging_evaluation"] = staging_evaluation
        if terminal_delivery_recovered(execution_error, run, staging_evaluation, evaluation, delivery_applied):
            recovered_execution_error = execution_error
            execution_error = ""
        if hidden_paths_present(case, workspace):
            remove_hidden(case, workspace)
    finally:
        stop_server(process, log_handle)
        if research_server:
            research_server.close()

    status_ok = str(run.get("status", "")).lower() in {"completed", "succeeded"}
    quality = evaluation.get("quality_score") or {}
    pass_conditions = {
        "status_ok": status_ok,
        "evaluation_passed": bool(evaluation.get("passed", False)),
        "staging_passed": bool(staging_evaluation.get("passed", False)),
        "no_execution_error": not execution_error,
        "tool_results": int(evidence.get("tool_results", 0) or 0) > 0,
        "tool_signature_samples": int(evidence.get("tool_signature_samples", 0) or 0) > 0,
        "duplicate_tool_calls": int(evidence.get("duplicate_tool_calls", 0) or 0) == 0,
        "duplicate_full_reads": int(evidence.get("duplicate_full_reads", 0) or 0) == 0,
        "score": int(quality.get("score", 0) or 0) >= 80,
        "hard_gate": quality.get("hard_gate") is True,
        "delivery_applied": bool(delivery_applied),
    }
    passed = all(pass_conditions.values())
    if case.workflow and not restart_state["done"] and case.domain in {"development", "security", "operations", "research", "cross-domain"}:
        # A complex workflow that never exposed a persistent approval is not
        # automatically a failure: the result records that no restart point
        # existed, while the case's own failure injection remains authoritative.
        restart_state["not_required"] = True
    return {
        "case_id": case.id,
        "domain": case.domain,
        "passed": passed,
        "pass_conditions": pass_conditions,
        "evidence_error": evidence_error,
        "status": run.get("status"),
        "run_id": run_id,
        "duration_ms": round((time.monotonic() - started) * 1000),
        "started_at": started_at,
        "finished_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "workspace_baseline_hash": baseline_hash,
        "request_text": prompt,
        "approvals": approvals,
        "restart_restored": restart_state,
        "delivery_applied": delivery_applied,
        "staging_workspace": str(staging_workspace) if staging_workspace else "",
        "staging_evaluation": staging_evaluation,
        "execution_error": execution_error,
        "recovered_execution_error": recovered_execution_error,
        "evidence": evidence,
        "evaluation": evaluation,
        "provider_run": run,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=[case.id for case in cases()])
    parser.add_argument(
        "--repeat",
        type=int,
        default=1,
        help="target consecutive round count; valid persisted rounds are resumed without rerunning",
    )
    parser.add_argument("--timeout", type=float, default=1800.0)
    parser.add_argument(
        "--kit-acceptance",
        action="store_true",
        help="also evaluate the delivered workspace against the domain kit's acceptance declaration",
    )
    parser.add_argument(
        "--kit-acceptance-strict",
        action="store_true",
        help="fail the case when kit acceptance checks fail (implied by --kit-acceptance)",
    )
    parser.add_argument(
        "--kit-acceptance-lenient",
        action="store_true",
        help="attach kit acceptance results without failing the case",
    )
    parser.add_argument(
        "--acceptance-workspace",
        type=Path,
        help="workspace that receives formal P7 evidence; required unless --probe-only is used",
    )
    parser.add_argument("--keep-workspaces", action="store_true")
    parser.add_argument("--skip-provider-probe", action="store_true")
    parser.add_argument(
        "--provider-secret",
        type=Path,
        help="optional approved secret file under secrets/providers/primary; staged only in the isolated run root",
    )
    parser.add_argument(
        "--provider-secret-env",
        help="optional environment variable containing an approved secret; value is staged only in the isolated run root",
    )
    parser.add_argument(
        "--probe-only",
        action="store_true",
        help="run only the generic Provider capability probe; do not create fixtures or formal P7 rounds",
    )
    args = parser.parse_args()
    if args.probe_only and args.skip_provider_probe:
        parser.error("--probe-only cannot be combined with --skip-provider-probe")
    if args.provider_secret is not None and args.provider_secret_env:
        parser.error("--provider-secret and --provider-secret-env are mutually exclusive")
    if not args.probe_only and args.acceptance_workspace is None:
        parser.error("--acceptance-workspace is required unless --probe-only is used")
    return args


def main() -> int:
    args = parse_args()
    if args.acceptance_workspace is not None and not args.probe_only:
        # Fail in a second rather than after a full paid run: a root under
        # .artifacts/.tmp can never hold formal acceptance evidence.
        try:
            validate_acceptance_output_root(args.acceptance_workspace)
        except RuntimeError as error:
            print(f"error: {error}", file=sys.stderr)
            return 2
    selected = [case for case in cases() if not args.case or case.id == args.case]
    stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S-%f")
    artifact_dir = ROOT / ".artifacts" / "p7-provider" / stamp
    artifact_dir.mkdir(parents=True)
    temp_root = Path(tempfile.mkdtemp(prefix="p7-workspaces-", dir=artifact_dir))
    variant_seed = int(stamp.replace("-", "")) % 2147483647
    env = os.environ.copy()
    env["GOFLOW_DATA_ROOT"] = str(ROOT)
    owned_runtime_paths: list[Path] = []
    if not env.get("GOCACHE"):
        env["GOCACHE"] = str(artifact_dir / "gocache")
        owned_runtime_paths.append(Path(env["GOCACHE"]))
    if not env.get("GOTMPDIR"):
        env["GOTMPDIR"] = str(artifact_dir / "gotmp")
        owned_runtime_paths.append(Path(env["GOTMPDIR"]))
    Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    Path(env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
    binary = artifact_dir / ("goflow-p7.exe" if os.name == "nt" else "goflow-p7")
    results: list[dict[str, Any]] = []
    cleanup_errors: list[str] = []
    try:
        provider_secret_env = str(getattr(args, "provider_secret_env", "") or "").strip()
        if provider_secret_env:
            provider_secret_value = os.environ.get(provider_secret_env, "")
            if not provider_secret_value:
                raise ValueError(f"provider secret environment variable {provider_secret_env!r} is empty")
            provider_data_root = temp_root / "provider-data"
            stage_provider_secret_value(provider_data_root, provider_secret_value)
            env["GOFLOW_DATA_ROOT"] = str(provider_data_root)
            env.pop(provider_secret_env, None)
        elif getattr(args, "provider_secret", None) is not None:
            provider_data_root = temp_root / "provider-data"
            stage_provider_secret(ROOT, provider_data_root, args.provider_secret)
            env["GOFLOW_DATA_ROOT"] = str(provider_data_root)
        build_binary(binary, env)
        if not args.skip_provider_probe:
            probe_workspace = temp_root / "provider-probe"
            probe_workspace.mkdir()
            probe_port = free_port()
            probe_process = probe_log = None
            try:
                probe_process, probe_log = start_server(binary, probe_workspace, probe_port, env, artifact_dir / "provider-probe.log")
                secret_reference = "secret://providers/primary/api_key" if (getattr(args, "provider_secret", None) is not None or getattr(args, "provider_secret_env", None)) else ""
                probe = provider_probe_with_retries(f"http://127.0.0.1:{probe_port}", api_key_reference=secret_reference)
            finally:
                stop_server(probe_process, probe_log)
            (artifact_dir / "provider-probe.json").write_text(
                json.dumps(probe, ensure_ascii=False, indent=2) + "\n",
                encoding="utf-8",
            )
            if not probe.get("complete"):
                safe_summary = {
                    "status": probe.get("status"),
                    "message": probe.get("message"),
                    "model": probe.get("model"),
                    "capabilities": probe.get("capabilities"),
                }
                report = {
                    "schema_version": 1,
                    "mode": "provider-probe" if args.probe_only else "formal-p7",
                    "provider": safe_summary,
                    "results": [],
                    "passed": False,
                    "formal_p7_rounds": 0,
                    "error": "Provider capability probe failed; no P7 task was counted",
                }
                report_path = artifact_dir / "report.json"
                report_path.write_text(
                    json.dumps(report, ensure_ascii=False, indent=2) + "\n",
                    encoding="utf-8",
                )
                print(
                    json.dumps(
                        {
                            "passed": False,
                            "mode": report["mode"],
                            "report": str(report_path),
                            "cases": 0,
                            "formal_p7_rounds": 0,
                            "error": report["error"],
                        },
                        ensure_ascii=False,
                        indent=2,
                    )
                )
                return 1
        else:
            probe = configured_primary_probe(skipped=True)

        if args.probe_only:
            report = {
                "schema_version": 1,
                "mode": "provider-probe",
                "provider": probe,
                "results": [],
                "passed": True,
                "formal_p7_rounds": 0,
            }
            report_path = artifact_dir / "report.json"
            report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            print(
                json.dumps(
                    {
                        "passed": True,
                        "mode": "provider-probe",
                        "report": str(report_path),
                        "cases": 0,
                        "formal_p7_rounds": 0,
                    },
                    ensure_ascii=False,
                    indent=2,
                )
            )
            return 0

        target_repetitions = max(1, args.repeat)
        completed_rounds: dict[str, set[int]] = {}
        blocked_cases: set[str] = set()
        for case in selected:
            completed, archived = prepare_case_resume(
                args.acceptance_workspace,
                case,
                target_repetitions,
                stamp,
            )
            completed_rounds[case.id] = completed
            if archived:
                print(f"[p7] {case.id} previous non-consecutive series archived at {archived}", flush=True)
            for repetition in sorted(completed):
                results.append({
                    "case_id": case.id,
                    "domain": case.domain,
                    "repetition": repetition,
                    "passed": True,
                    "resumed_existing_evidence": True,
                    "acceptance_evidence": str(
                        acceptance_round_path(
                            args.acceptance_workspace,
                            domain=case.domain,
                            case_id=case.id,
                            repetition=repetition,
                        )
                    ),
                })
                print(f"[p7] {case.id} repetition={repetition} resumed from validated evidence", flush=True)

        for repetition in range(1, target_repetitions + 1):
            for case in selected:
                if repetition in completed_rounds[case.id] or case.id in blocked_cases:
                    continue
                workspace = prepare_workspace(case, temp_root, env, repetition, variant_seed)
                result = run_case(case, workspace, binary, env, args.timeout, artifact_dir)
                if not result.get("passed") and provider_temporarily_unavailable(result):
                    print(f"[p7] {case.id} provider unavailable, retrying once", flush=True)
                    workspace = prepare_workspace(case, temp_root, env, repetition + 100, variant_seed)
                    result = run_case(case, workspace, binary, env, args.timeout, artifact_dir)
                    result["repetition"] = repetition
                result["repetition"] = repetition
                result["variant_seed"] = variant_seed
                result["variant_index"] = variant_index(case, repetition, variant_seed)
                if getattr(args, "kit_acceptance", False):
                    try:
                        from kit_acceptance import check_case_workspace

                        result["kit_acceptance"] = check_case_workspace(case.id, workspace)
                        strict = getattr(args, "kit_acceptance_strict", False) or not getattr(
                            args, "kit_acceptance_lenient", False
                        )
                        if strict and not result["kit_acceptance"].get("passed"):
                            result["passed"] = False
                            result.setdefault("evidence_errors", []).append(
                                "kit acceptance failed: "
                                + "; ".join(result["kit_acceptance"].get("errors") or [])
                            )
                    except Exception as error:  # keep the harness usable without PyYAML
                        result["kit_acceptance"] = {"skipped": True, "errors": [str(error)]}
                results.append(result)
                passed = bool(result["passed"])
                contract = {
                    "case_id": case.id,
                    "domain": case.domain,
                    "workflow": case.workflow,
                    "goal": case.title,
                    "required_deliverables": list(case.required_deliverables),
                    "stop_conditions": ["任一硬验收失败", "超出任务授权范围", "一次有界返修失败"],
                }
                try:
                    evidence_dir = write_acceptance_round(
                        args.acceptance_workspace,
                        domain=case.domain,
                        case_id=case.id,
                        repetition=repetition,
                        request_text=str(result.get("request_text") or case.prompt),
                        task_contract=contract,
                        timeline={"provider_run": result.get("provider_run"), "evidence": result.get("evidence")},
                        approvals=result.get("approvals"),
                        tool_summary=result.get("evidence") or {},
                        verification=result.get("evaluation") or {},
                        quality_score=result.get("evaluation", {}).get("quality_score") or {"passed": False, "score": 0, "source": "deterministic_external_evaluator"},
                        report={
                        "schema_version": 1,
                        "passed": passed,
                        "provider": probe,
                        "case_id": case.id,
                        "domain": case.domain,
                        "repetition": repetition,
                        "variant_seed": result.get("variant_seed"),
                        "variant_index": result.get("variant_index"),
                        "run_id": result.get("run_id"),
                        "workspace_baseline_hash": result.get("workspace_baseline_hash"),
                        "started_at": result.get("started_at"),
                        "finished_at": result.get("finished_at"),
                        "duration_ms": result.get("duration_ms"),
                        "token_usage": {
                            "prompt_tokens": int(result.get("evidence", {}).get("prompt_tokens", 0) or 0),
                            "output_tokens": int(result.get("evidence", {}).get("output_tokens", 0) or 0),
                            "total_tokens": int(result.get("evidence", {}).get("prompt_tokens", 0) or 0)
                            + int(result.get("evidence", {}).get("output_tokens", 0) or 0),
                        },
                        "tool_metrics": {
                            "calls": int(result.get("evidence", {}).get("tool_calls", 0) or 0),
                            "results": int(result.get("evidence", {}).get("tool_results", 0) or result.get("evidence", {}).get("stage_tool_results", 0) or 0),
                            "duration_ms": int(result.get("evidence", {}).get("tool_duration_ms", 0) or 0),
                            "duration_samples": int(result.get("evidence", {}).get("tool_duration_samples", 0) or 0),
                            "duplicate_tool_calls": int(result.get("evidence", {}).get("duplicate_tool_calls", 0) or 0),
                            "duplicate_full_reads": int(result.get("evidence", {}).get("duplicate_full_reads", 0) or 0),
                            "signature_samples": int(result.get("evidence", {}).get("tool_signature_samples", 0) or 0),
                            "unique_tools": list(result.get("evidence", {}).get("tool_names") or []),
                        },
                        "failure_recovery": result.get("restart_restored") or {},
                        "independent_retest": bool(result.get("evaluation", {}).get("independent")),
                        "delivery_applied": result.get("delivery_applied"),
                        "execution_error": result.get("execution_error"),
                        },
                        workspace=workspace,
                        excluded_diff_paths=set(case.hidden_files),
                        deliverable_paths=case.required_deliverables,
                    )
                    result["acceptance_evidence"] = str(evidence_dir)
                    result["evidence_validation"] = validate_acceptance_round(evidence_dir, require_success=passed)
                except Exception as error:
                    result["passed"] = False
                    result["evidence_write_error"] = str(error)
                    passed = False
                if not passed:
                    blocked_cases.add(case.id)
                print(f"[p7] {case.id} repetition={repetition} passed={passed}", flush=True)
    finally:
        if not args.keep_workspaces:
            cleaned, detail = cleanup_runtime_path(temp_root)
            if not cleaned:
                cleanup_errors.append(f"{temp_root}: {detail}")
        for runtime_path in owned_runtime_paths:
            cleaned, detail = cleanup_runtime_path(runtime_path)
            if not cleaned:
                cleanup_errors.append(f"{runtime_path}: {detail}")
        cleaned, detail = cleanup_runtime_path(binary)
        if not cleaned:
            cleanup_errors.append(f"{binary}: {detail}")
    expected_results = len(selected) * max(1, args.repeat)
    report = {
        "schema_version": 1,
        "provider": probe if "probe" in locals() else {},
        "results": results,
        "passed": len(results) == expected_results and all(item["passed"] for item in results) and not cleanup_errors,
        "generated_artifacts_cleaned": not cleanup_errors,
        "cleanup_errors": cleanup_errors,
    }
    report_path = artifact_dir / "report.json"
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": report["passed"], "report": str(report_path), "cases": len(results)}, ensure_ascii=False, indent=2))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
