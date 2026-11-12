#!/usr/bin/env python3
"""Independent evaluator for repository-scale P7 complex cases."""

from __future__ import annotations

import hashlib
import os
import re
import subprocess
import tempfile
from pathlib import Path
from typing import Any

try:
    from .p7_complex_cases import P7ComplexCase, install_hidden
    from .p7_office_evaluator import evaluate_documents_case
    from .p7_research_evaluator import evaluate_research_case
except ImportError:  # direct script execution
    from p7_complex_cases import P7ComplexCase, install_hidden
    from p7_office_evaluator import evaluate_documents_case
    from p7_research_evaluator import evaluate_research_case


SECRET_PATTERN = re.compile(r"(?i)(?:sk-[a-z0-9_-]{16,}|-----begin [^-]+ private key-----|bearer\s+[a-z0-9._~+/-]{16,})")
PLACEHOLDER_MARKERS = ("待补充", "待生成", "todo: fill", "placeholder")
RACE_UNAVAILABLE_MARKERS = (
    "-race requires cgo",
    "c compiler",
    "gcc: executable file not found",
    'exec: "gcc"',
    "cgo: c compiler",
)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run_command(command: tuple[str, ...], workspace: Path, timeout: float = 240.0) -> dict[str, Any]:
    runtime_root = Path(os.environ.get("GOFLOW_P7_EVALUATOR_TEMP_ROOT", tempfile.gettempdir())) / "goflow-p7-evaluator"
    cache_root = runtime_root / "gocache"
    temp_root = runtime_root / "gotmp"
    cache_root.mkdir(parents=True, exist_ok=True)
    temp_root.mkdir(parents=True, exist_ok=True)
    try:
        completed = subprocess.run(
            list(command), cwd=workspace, text=True, encoding="utf-8", errors="replace",
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout, check=False,
            env={**os.environ, "GOCACHE": str(cache_root), "GOTMPDIR": str(temp_root)},
        )
    except subprocess.TimeoutExpired as exc:
        return {"command": list(command), "passed": False, "timed_out": True, "output": str(exc)}
    return {
        "command": list(command),
        "passed": completed.returncode == 0 and not (
            Path(command[0]).stem.lower() == "gofmt" and "-l" in command[1:] and completed.stdout.strip()
        ),
        "exit_code": completed.returncode,
        "output": completed.stdout[-12000:],
    }


def is_unavailable_race_result(result: dict[str, Any]) -> bool:
    output = str(result.get("output") or "").lower()
    return any(marker in output for marker in RACE_UNAVAILABLE_MARKERS)


def find_linux_race_gate(workspace: Path) -> str:
    workflow_root = workspace / ".github" / "workflows"
    if not workflow_root.is_dir():
        return ""
    for path in sorted((*workflow_root.glob("*.yml"), *workflow_root.glob("*.yaml"))):
        content = path.read_text(encoding="utf-8", errors="replace").lower()
        if "ubuntu" in content and "go test -race ./..." in content:
            return path.relative_to(workspace).as_posix()
    return ""


def _pristine_fixture_baseline(case: P7ComplexCase) -> tuple[dict[str, str], dict[str, str]]:
    """Capture the fixture's public tests and required-path hashes before any work.

    Rebuilding the fixture is the only trustworthy baseline: the delivered
    workspace alone cannot distinguish "untouched" from "weakened".
    """

    tests: dict[str, str] = {}
    required: dict[str, str] = {}
    with tempfile.TemporaryDirectory() as raw:
        root = Path(raw)
        case.builder(root)
        for path in root.rglob("*"):
            if not path.is_file():
                continue
            relative = path.relative_to(root).as_posix()
            if path.name.endswith(("_test.go", ".test.js", "_test.py")):
                tests[relative] = path.read_text(encoding="utf-8", errors="replace")
        for relative in case.required_paths:
            candidate = root / relative
            required[relative] = sha256_file(candidate) if candidate.is_file() else ""
    return tests, required


TEST_NAME_PATTERNS = (
    re.compile(r"func\s+(Test[A-Za-z0-9_]*)\s*\("),
    re.compile(r"\b(?:test|it)\s*\(\s*['\"]([^'\"]+)['\"]"),
)


def _test_names(text: str) -> set[str]:
    names: set[str] = set()
    for pattern in TEST_NAME_PATTERNS:
        names.update(pattern.findall(text))
    return names


def _delivery_integrity_issues(case: P7ComplexCase, workspace: Path) -> list[str]:
    """Guard against a delivery that only looks done.

    Existing public tests may be extended or formatted (the reference solution
    does both), but no existing test case may be removed or renamed, and the
    required source paths must exist with at least one actually changed.
    """

    pristine_tests, pristine_required = _pristine_fixture_baseline(case)
    issues: list[str] = []
    for relative, pristine_text in pristine_tests.items():
        path = workspace / relative
        if not path.is_file():
            issues.append(f"public test removed: {relative}")
            continue
        current_names = _test_names(path.read_text(encoding="utf-8", errors="replace"))
        missing = sorted(_test_names(pristine_text) - current_names)
        if missing:
            issues.append(f"public test cases removed from {relative}: {', '.join(missing)}")
    changed = 0
    for relative, expected in pristine_required.items():
        path = workspace / relative
        if not path.is_file():
            issues.append(f"required path missing: {relative}")
            continue
        if not expected or sha256_file(path) != expected:
            changed += 1
    if pristine_required and changed == 0:
        issues.append("no required source path was modified")
    return issues


def evaluate_p7_case(
    case: P7ComplexCase,
    workspace: Path,
    evidence: dict[str, Any] | None = None,
    run: dict[str, Any] | None = None,
    include_race: bool = True,
) -> dict[str, Any]:
    if not case.ready:
        return {"passed": False, "blocked": "case fixture is not implemented yet"}
    integrity_issues = _delivery_integrity_issues(case, workspace)
    # Hidden tests are evaluator-owned and must not enter the user's format gate.
    format_checks = {
        command: run_command(command, workspace)
        for command in case.verify_commands if command[:2] == ("gofmt", "-l")
    }
    install_hidden(case, workspace)
    if case.domain == "documents":
        result = evaluate_documents_case(workspace)
        if integrity_issues:
            result["errors"] = list(result.get("errors") or []) + integrity_issues
            result["passed"] = False
        result["quality_score"] = evaluate_p7_quality(case, workspace, result, evidence, run)
        return result
    if case.domain == "research":
        result = evaluate_research_case(workspace)
        if integrity_issues:
            result["errors"] = list(result.get("errors") or []) + integrity_issues
            result["passed"] = False
        result["quality_score"] = evaluate_p7_quality(case, workspace, result, evidence, run)
        return result
    checks = [format_checks[command] if command in format_checks else run_command(command, workspace) for command in case.verify_commands]
    if case.kind == "go" and (workspace / "go.mod").is_file():
        # go test compiles packages that have tests; an explicit build proves the
        # delivered program still links, including packages without tests.
        checks.append(run_command(("go", "build", "./..."), workspace))
    if integrity_issues:
        checks.append({
            "command": ["delivery-integrity"],
            "passed": False,
            "output": "; ".join(integrity_issues),
        })
    if case.domain == "development" and include_race:
        race = run_command(("go", "test", "-race", "./..."), workspace, timeout=360.0)
        if not race.get("passed") and is_unavailable_race_result(race):
            gate = find_linux_race_gate(workspace)
            ordinary_go_test_passed = any(
                check.get("passed")
                and check.get("command") == ["go", "test", "./..."]
                for check in checks
            )
            if ordinary_go_test_passed and gate:
                # The command did not pass, so keep that fact: a deferred gate
                # must never read as a locally verified race run. Deferral only
                # means the check no longer blocks delivery.
                race.update({
                    "passed": False,
                    "deferred": True,
                    "blocking": False,
                    "status": "deferred",
                    "infrastructure_status": "go_race_unavailable",
                    "gate": gate,
                })
        checks.append(race)
    failures = [check for check in checks if not check["passed"] and not check.get("deferred")]
    missing = [relative for relative in case.required_deliverables if not (workspace / relative).is_file()]
    # Delivery prose must agree with the independently executed verification.
    if case.domain == "cross-domain" and not failures:
        report_path = workspace / "docs/unified-delivery.md"
        report_text = report_path.read_text(encoding="utf-8", errors="replace") if report_path.is_file() else ""
        stale_markers = ("静态检查：未获得成功证据", "未能取得 `go vet ./...`", "静态检查判定为已完成")
        if any(marker in report_text for marker in stale_markers):
            issue = {
                "command": ["delivery-report-consistency"],
                "passed": False,
                "output": "docs/unified-delivery.md contradicts successful go vet evidence",
            }
            checks.append(issue)
            failures.append(issue)

    required_normalized = {Path(relative).as_posix() for relative in case.required_deliverables}
    unexpected = []
    deliverable_root = workspace / "deliverables"
    if deliverable_root.is_dir() and case.domain not in {"documents", "research"}:
        for path in deliverable_root.rglob("*"):
            if path.is_file():
                relative = path.relative_to(workspace).as_posix()
                if relative not in required_normalized:
                    unexpected.append(relative)
        if unexpected:
            issue = {
                "command": ["deliverable-boundary"],
                "passed": False,
                "output": "unexpected deliverables: " + ", ".join(sorted(unexpected)),
            }
            checks.append(issue)
            failures.append(issue)
    hashes = []
    for relative in case.required_deliverables:
        path = workspace / relative
        if path.is_file():
            hashes.append({"path": relative, "sha256": sha256_file(path), "bytes": path.stat().st_size})
    result = {
        "passed": not failures and not missing,
        "case_id": case.id,
        "domain": case.domain,
        "verification": checks,
        "missing_deliverables": missing,
        "unexpected_deliverables": unexpected,
        "deliverables": hashes,
        "independent": True,
    }
    result["quality_score"] = evaluate_p7_quality(case, workspace, result, evidence, run)
    return result


def evaluate_p7_quality(
    case: P7ComplexCase,
    workspace: Path,
    evaluation: dict[str, Any],
    evidence: dict[str, Any] | None,
    run: dict[str, Any] | None,
) -> dict[str, Any]:
    """Score only independently observable quality; model prose is never used."""

    dimensions = {
        "functional_correctness": 0,
        "engineering_completeness": 0,
        "security_scope": 0,
        "maintainability": 0,
        "efficiency": 0,
        "delivery_usability": 0,
    }
    notes: list[str] = []
    if not evaluation.get("passed"):
        notes.append("hard acceptance gate failed; score is zero")
        return {"score": 0, "dimensions": dimensions, "notes": notes, "hard_gate": False, "source": "deterministic_external_evaluator"}

    dimensions["functional_correctness"] = 35
    checks = evaluation.get("verification") or []
    if checks and all(bool(item.get("passed")) or bool(item.get("deferred")) for item in checks):
        dimensions["engineering_completeness"] = 20
    else:
        notes.append("one or more independent verification checks are missing or failed")

    secret_files: list[str] = []
    oversized_files: list[str] = []
    hidden = {path.replace("\\", "/") for path in case.hidden_files}
    for path in workspace.rglob("*"):
        if not path.is_file():
            continue
        relative = path.relative_to(workspace).as_posix()
        if relative in hidden or relative.startswith((".git/", ".cache/", ".goflow/", "acceptance/hidden/")):
            continue
        if path.stat().st_size > 4 * 1024 * 1024:
            oversized_files.append(relative)
            continue
        if path.suffix.lower() in {".png", ".jpg", ".jpeg", ".gif", ".pdf", ".docx", ".xlsx", ".pptx", ".exe", ".dll", ".so"}:
            continue
        try:
            content = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        if SECRET_PATTERN.search(content):
            secret_files.append(relative)
    if not secret_files:
        dimensions["security_scope"] = 15
    else:
        notes.append(f"possible secret material found in {len(secret_files)} file(s)")
    if not oversized_files:
        dimensions["maintainability"] = 10
    else:
        notes.append(f"oversized generated file(s): {', '.join(oversized_files[:3])}")

    evidence = evidence or {}
    run = run or {}
    tool_results = int(evidence.get("tool_results", 0) or evidence.get("stage_tool_results", 0) or 0)
    calls = int(run.get("budget_llm_calls", 0) or evidence.get("token_events", 0) or 0)
    artifacts = int(evidence.get("artifacts", 0) or 0)
    if tool_results > 0:
        dimensions["efficiency"] += 5
    else:
        notes.append("no real tool result evidence was recorded")
    if evidence and calls <= case.llm_call_budget and artifacts <= case.artifact_budget:
        dimensions["efficiency"] += 5
    else:
        notes.append(
            "efficiency budget exceeded: "
            f"llm_calls={calls}/{case.llm_call_budget}, artifacts={artifacts}/{case.artifact_budget}"
        )

    missing = evaluation.get("missing_deliverables") or []
    empty: list[str] = []
    placeholders: list[str] = []
    for relative in case.required_deliverables:
        path = workspace / relative
        if not path.is_file() or path.stat().st_size == 0:
            empty.append(relative)
            continue
        if path.suffix.lower() in {".md", ".txt", ".json", ".csv", ".xml", ".html"}:
            try:
                text = path.read_text(encoding="utf-8", errors="replace").lower()
            except OSError:
                text = ""
            if any(marker in text for marker in PLACEHOLDER_MARKERS):
                placeholders.append(relative)
    if not missing and not empty and not placeholders:
        dimensions["delivery_usability"] = 10
    else:
        if empty:
            notes.append(f"empty deliverable(s): {', '.join(empty[:3])}")
        if placeholders:
            notes.append(f"placeholder deliverable(s): {', '.join(placeholders[:3])}")
    score = sum(dimensions.values())
    hard_gate = (
        score >= 80
        and dimensions["functional_correctness"] == 35
        and dimensions["engineering_completeness"] == 20
        and dimensions["delivery_usability"] == 10
    )
    return {
        "score": score,
        "dimensions": dimensions,
        "notes": notes,
        "hard_gate": hard_gate,
        "secret_files": secret_files,
        "oversized_files": oversized_files,
        "source": "deterministic_external_evaluator",
    }
