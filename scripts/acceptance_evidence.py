#!/usr/bin/env python3
"""Persist complex-task evidence outside disposable build directories."""

from __future__ import annotations

import hashlib
import json
import re
import shutil
import subprocess
import tempfile
from datetime import datetime
from pathlib import Path
from typing import Any


SENSITIVE_FIELD = re.compile(r"(?i)(api[_-]?key|authorization|private[_-]?key|access[_-]?token|secret)")
SECRET_TEXT = re.compile(r"(?i)(?:sk-[a-z0-9_-]{16,}|bearer\s+[a-z0-9._~+/-]{16,})")
DOMAIN_NAMES = {
    "development": "开发编码",
    "security": "网络安全",
    "operations": "网络运维",
    "research": "资料核验",
    "documents": "Office-WPS文档",
    "cross-domain": "跨领域交付",
}
REQUIRED_ROUND_FILES = {
    "request.txt",
    "task-contract.json",
    "timeline.json",
    "approvals.json",
    "tool-summary.json",
    "diff.patch",
    "verification.json",
    "quality-score.json",
    "report.json",
}
QUALITY_CONTRACT_VERSION = 2


def redact(value: Any, key: str = "") -> Any:
    if SENSITIVE_FIELD.search(key):
        return "[REDACTED]"
    if isinstance(value, dict):
        return {str(item_key): redact(item_value, str(item_key)) for item_key, item_value in value.items()}
    if isinstance(value, list):
        return [redact(item) for item in value]
    if isinstance(value, tuple):
        return [redact(item) for item in value]
    if isinstance(value, str):
        return SECRET_TEXT.sub("[REDACTED]", value)
    return value


def safe_component(value: str) -> str:
    normalized = re.sub(r"[<>:\"/\\|?*\x00-\x1f]", "-", value.strip())
    normalized = normalized.strip(" .-")
    return normalized[:96] or "未命名"


def acceptance_round_path(
    output_root: Path,
    *,
    domain: str,
    case_id: str,
    repetition: int,
) -> Path:
    family = "cross-domain" if case_id.endswith("cross-domain") else domain
    return (
        output_root.resolve()
        / "交付证据"
        / safe_component(DOMAIN_NAMES.get(family, family))
        / safe_component(case_id)
        / f"第{max(1, repetition)}轮"
    )


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(redact(value), ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def git_output(workspace: Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", *args], cwd=workspace, text=True, encoding="utf-8", errors="replace",
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)} failed: {completed.stderr.strip()}")
    return completed.stdout


def workspace_diff(workspace: Path, excluded_paths: set[str] | None = None) -> str:
    excluded = {item.replace("\\", "/") for item in (excluded_paths or set())}
    parts = [git_output(workspace, "diff", "--binary", "HEAD", "--")]
    untracked = git_output(workspace, "ls-files", "--others", "--exclude-standard").splitlines()
    for relative in sorted(untracked):
        normalized = relative.replace("\\", "/")
        if normalized in excluded or normalized.startswith((".goflow/", ".cache/", ".tmp/", "acceptance/hidden/")):
            continue
        completed = subprocess.run(
            ["git", "diff", "--no-index", "--binary", "--", "/dev/null", normalized],
            cwd=workspace, text=True, encoding="utf-8", errors="replace",
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
        )
        if completed.returncode not in {0, 1}:
            raise RuntimeError(f"git diff for {relative} failed: {completed.stderr.strip()}")
        parts.append(completed.stdout)
    return "".join(parts)


def copy_deliverables(
    workspace: Path,
    target: Path,
    required_paths: tuple[str, ...] | list[str] | None = None,
) -> list[dict[str, Any]]:
    source = workspace / "deliverables"
    if target.exists():
        shutil.rmtree(target)
    target.mkdir(parents=True, exist_ok=True)
    manifest: list[dict[str, Any]] = []
    selected: dict[str, tuple[Path, Path]] = {}
    if source.is_dir():
        for path in sorted(source.rglob("*")):
            if path.is_file():
                relative = path.relative_to(source)
                selected[relative.as_posix()] = (path, relative)
    for raw_relative in required_paths or ():
        relative = Path(str(raw_relative).replace("\\", "/"))
        if relative.is_absolute() or ".." in relative.parts:
            raise RuntimeError(f"deliverable path escapes the workspace: {raw_relative}")
        path = workspace / relative
        candidates = [path] if path.is_file() else sorted(path.rglob("*")) if path.is_dir() else []
        for candidate in candidates:
            if not candidate.is_file():
                continue
            workspace_relative = candidate.relative_to(workspace)
            destination_relative = (
                Path(*workspace_relative.parts[1:])
                if workspace_relative.parts and workspace_relative.parts[0] == "deliverables"
                else workspace_relative
            )
            selected[destination_relative.as_posix()] = (candidate, destination_relative)
    for key in sorted(selected):
        path, relative = selected[key]
        destination = target / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
        manifest.append({
            "path": relative.as_posix(),
            "bytes": destination.stat().st_size,
            "sha256": sha256_file(destination),
        })
    return manifest


def _parse_timestamp(value: Any, label: str) -> datetime:
    text = str(value or "").strip()
    if not text:
        raise RuntimeError(f"acceptance report has no {label}")
    try:
        return datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError as exc:
        raise RuntimeError(f"acceptance report has invalid {label}") from exc


def validate_acceptance_round(target: Path, *, require_success: bool = False) -> dict[str, Any]:
    """Validate a persisted P7 round before it can count toward acceptance."""

    target = target.resolve()
    missing = sorted(name for name in REQUIRED_ROUND_FILES if not (target / name).is_file())
    empty = sorted(name for name in REQUIRED_ROUND_FILES if (target / name).is_file() and (target / name).stat().st_size == 0)
    if missing or empty:
        raise RuntimeError(f"acceptance evidence files are incomplete: missing={missing}, empty={empty}")

    def read_json(name: str) -> Any:
        try:
            return json.loads((target / name).read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise RuntimeError(f"acceptance evidence has invalid {name}: {exc}") from exc

    report = read_json("report.json")
    verification = read_json("verification.json")
    quality = read_json("quality-score.json")
    tool_summary = read_json("tool-summary.json")
    if not isinstance(tool_summary, dict):
        raise RuntimeError("acceptance evidence has an invalid tool-summary object")
    if not isinstance(report, dict) or report.get("schema_version") != 1:
        raise RuntimeError("acceptance report has an unsupported schema")
    if int(report.get("quality_contract_version", 0) or 0) != QUALITY_CONTRACT_VERSION:
        raise RuntimeError("acceptance report uses an obsolete quality contract")

    started = _parse_timestamp(report.get("started_at"), "started_at")
    finished = _parse_timestamp(report.get("finished_at"), "finished_at")
    if finished < started or int(report.get("duration_ms", 0) or 0) <= 0:
        raise RuntimeError("acceptance report has an invalid bounded duration")
    baseline = str(report.get("workspace_baseline_hash") or "").strip().lower()
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", baseline):
        raise RuntimeError("acceptance report has no valid workspace baseline hash")
    provider = report.get("provider")
    if not isinstance(provider, dict) or not str(provider.get("model") or "").strip():
        raise RuntimeError("acceptance report has no Provider model evidence")
    if not str(report.get("run_id") or "").strip():
        raise RuntimeError("acceptance report has no task run ID")

    token_usage = report.get("token_usage")
    try:
        prompt_tokens = int(token_usage.get("prompt_tokens", -1)) if isinstance(token_usage, dict) else -1
        output_tokens = int(token_usage.get("output_tokens", -1)) if isinstance(token_usage, dict) else -1
        total_tokens = int(token_usage.get("total_tokens", -1)) if isinstance(token_usage, dict) else -1
    except (TypeError, ValueError) as exc:
        raise RuntimeError("acceptance report has invalid token evidence") from exc
    if prompt_tokens < 0 or output_tokens < 0 or total_tokens != prompt_tokens + output_tokens:
        raise RuntimeError("acceptance report has inconsistent token evidence")
    tool_metrics = report.get("tool_metrics")
    try:
        tool_results = int(tool_metrics.get("results", -1)) if isinstance(tool_metrics, dict) else -1
        tool_duration_ms = int(tool_metrics.get("duration_ms", -1)) if isinstance(tool_metrics, dict) else -1
        tool_duration_samples = int(tool_metrics.get("duration_samples", -1)) if isinstance(tool_metrics, dict) else -1
        duplicate_tool_calls = int(tool_metrics.get("duplicate_tool_calls", -1)) if isinstance(tool_metrics, dict) else -1
        duplicate_full_reads = int(tool_metrics.get("duplicate_full_reads", -1)) if isinstance(tool_metrics, dict) else -1
        signature_samples = int(tool_metrics.get("signature_samples", -1)) if isinstance(tool_metrics, dict) else -1
    except (TypeError, ValueError) as exc:
        raise RuntimeError("acceptance report has invalid tool metrics") from exc
    if (
        tool_results < 0
        or tool_duration_ms < 0
        or tool_duration_samples < 0
        or duplicate_tool_calls < 0
        or duplicate_full_reads < 0
        or signature_samples < 0
    ):
        raise RuntimeError("acceptance report has invalid tool metrics")
    if not isinstance(report.get("failure_recovery"), dict):
        raise RuntimeError("acceptance report has no failure recovery evidence")

    # The report is a compact index, while tool-summary.json preserves the
    # observed run metrics. When both expose a metric, they must agree; this
    # prevents editing one evidence file to manufacture a passing hard gate.
    summary_metric_aliases = {
        "results": ("tool_results", "stage_tool_results"),
        "duration_ms": ("tool_duration_ms", "duration_ms"),
        "duration_samples": ("tool_duration_samples", "duration_samples"),
        "duplicate_tool_calls": ("duplicate_tool_calls",),
        "duplicate_full_reads": ("duplicate_full_reads",),
        "signature_samples": ("tool_signature_samples", "signature_samples"),
    }
    report_metric_values = {
        "results": tool_results,
        "duration_ms": tool_duration_ms,
        "duration_samples": tool_duration_samples,
        "duplicate_tool_calls": duplicate_tool_calls,
        "duplicate_full_reads": duplicate_full_reads,
        "signature_samples": signature_samples,
    }
    for report_key, aliases in summary_metric_aliases.items():
        observed = next((tool_summary[key] for key in aliases if key in tool_summary), None)
        if observed is None:
            continue
        try:
            observed_int = int(observed)
        except (TypeError, ValueError) as exc:
            raise RuntimeError(f"acceptance tool-summary has invalid {report_key}") from exc
        if observed_int != report_metric_values[report_key]:
            raise RuntimeError(f"acceptance tool metrics disagree for {report_key}")

    deliverables = report.get("deliverables")
    if not isinstance(deliverables, list):
        raise RuntimeError("acceptance report has no deliverable manifest")
    for item in deliverables:
        if not isinstance(item, dict):
            raise RuntimeError("acceptance report contains an invalid deliverable entry")
        relative = Path(str(item.get("path") or ""))
        if relative.is_absolute() or ".." in relative.parts:
            raise RuntimeError("acceptance report contains an unsafe deliverable path")
        path = target / "deliverables" / relative
        if not path.is_file() or sha256_file(path) != str(item.get("sha256") or ""):
            raise RuntimeError(f"acceptance deliverable hash mismatch: {relative.as_posix()}")
        if path.stat().st_size != int(item.get("bytes", -1)):
            raise RuntimeError(f"acceptance deliverable size mismatch: {relative.as_posix()}")
    declared_deliverables = {str(item.get("path") or "") for item in deliverables if isinstance(item, dict)}
    actual_deliverables: set[str] = set()
    deliverable_root = target / "deliverables"
    if deliverable_root.is_dir():
        actual_deliverables = {
            path.relative_to(deliverable_root).as_posix()
            for path in deliverable_root.rglob("*")
            if path.is_file()
        }
    if actual_deliverables != declared_deliverables:
        raise RuntimeError(
            "acceptance deliverable manifest does not match persisted files: "
            f"declared={sorted(declared_deliverables)}, actual={sorted(actual_deliverables)}"
        )

    diff_path = target / "diff.patch"
    if str(report.get("diff_sha256") or "") != sha256_file(diff_path):
        raise RuntimeError("acceptance Diff hash does not match the report")

    if require_success:
        if report.get("passed") is not True:
            raise RuntimeError("acceptance round is not successful")
        if not isinstance(verification, dict) or verification.get("passed") is not True or verification.get("independent") is not True:
            raise RuntimeError("acceptance round has no successful independent verification")
        if (
            not isinstance(quality, dict)
            or quality.get("hard_gate") is not True
            or int(quality.get("score", 0) or 0) < 80
            or quality.get("source") != "deterministic_external_evaluator"
        ):
            raise RuntimeError("acceptance round has no passing independent quality score")
        results = int(tool_metrics.get("results", 0) or tool_summary.get("tool_results", 0) or tool_summary.get("stage_tool_results", 0) or 0)
        if results <= 0:
            raise RuntimeError("acceptance round has no real tool result evidence")
        if tool_duration_samples <= 0:
            raise RuntimeError("acceptance round has no observed tool duration evidence")
        if signature_samples <= 0:
            raise RuntimeError("acceptance round has no redacted tool argument signature evidence")
        if duplicate_tool_calls != 0:
            raise RuntimeError(f"acceptance round contains {duplicate_tool_calls} duplicate tool calls")
        if duplicate_full_reads != 0:
            raise RuntimeError(f"acceptance round contains {duplicate_full_reads} duplicate full-file reads")
        if total_tokens <= 0:
            raise RuntimeError("acceptance round has no real Provider token evidence")
        if not deliverables:
            raise RuntimeError("acceptance round has no persisted deliverables")
        if diff_path.stat().st_size == 0:
            raise RuntimeError("acceptance round has no persisted Diff")

    return {
        "valid": True,
        "passed": report.get("passed") is True,
        "run_id": report.get("run_id"),
        "deliverables": len(deliverables),
        "quality_score": int(quality.get("score", 0) or 0) if isinstance(quality, dict) else 0,
    }


def validate_acceptance_output_root(output_root: Path) -> Path:
    """Reject an evidence root that may only hold disposable artifacts.

    The runner calls this before starting a Provider run so a path that could
    never store formal evidence fails immediately instead of after a full,
    paid run.
    """
    resolved = Path(output_root).resolve()
    if any(part.casefold() in {".artifacts", ".tmp"} for part in resolved.parts):
        raise RuntimeError("acceptance evidence cannot be stored in a disposable artifacts directory")
    return resolved


def write_acceptance_round(
    output_root: Path,
    *,
    domain: str,
    case_id: str,
    repetition: int,
    request_text: str,
    task_contract: dict[str, Any],
    timeline: Any,
    approvals: Any,
    tool_summary: dict[str, Any],
    verification: dict[str, Any],
    quality_score: dict[str, Any],
    report: dict[str, Any],
    workspace: Path,
    excluded_diff_paths: set[str] | None = None,
    deliverable_paths: tuple[str, ...] | list[str] | None = None,
) -> Path:
    output_root = validate_acceptance_output_root(output_root)
    workspace = workspace.resolve()
    if output_root == workspace or workspace in output_root.parents:
        raise RuntimeError("acceptance evidence must be stored outside the disposable task workspace")
    target = acceptance_round_path(
        output_root,
        domain=domain,
        case_id=case_id,
        repetition=repetition,
    )
    target.parent.mkdir(parents=True, exist_ok=True)
    if target.exists():
        raise RuntimeError(f"acceptance round already exists and cannot be overwritten: {target}")
    temporary = Path(tempfile.mkdtemp(prefix=f".{target.name}-writing-", dir=target.parent))
    try:
        (temporary / "request.txt").write_text(str(redact(request_text)).strip() + "\n", encoding="utf-8")
        write_json(temporary / "task-contract.json", task_contract)
        write_json(temporary / "timeline.json", timeline)
        write_json(temporary / "approvals.json", approvals)
        write_json(temporary / "tool-summary.json", tool_summary)
        diff_path = temporary / "diff.patch"
        diff_path.write_text(redact(workspace_diff(workspace, excluded_diff_paths)), encoding="utf-8", newline="\n")
        write_json(temporary / "verification.json", verification)
        write_json(temporary / "quality-score.json", quality_score)
        deliverables = copy_deliverables(workspace, temporary / "deliverables", deliverable_paths)
        final_report = dict(report)
        final_report["quality_contract_version"] = QUALITY_CONTRACT_VERSION
        final_report["deliverables"] = deliverables
        final_report["diff_sha256"] = sha256_file(diff_path)
        final_report.setdefault("workspace_baseline_hash", git_output(workspace, "rev-parse", "HEAD").strip())
        final_report["evidence_directory"] = str(target)
        write_json(temporary / "report.json", final_report)
        if target.exists():
            raise RuntimeError(f"acceptance round was published concurrently: {target}")
        temporary.rename(target)
    except Exception:
        shutil.rmtree(temporary, ignore_errors=True)
        raise
    return target
