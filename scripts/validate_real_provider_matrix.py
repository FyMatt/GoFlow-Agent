#!/usr/bin/env python3
"""Validate legacy golden Provider case definitions without calling a model.

This structural check is useful as a fast regression gate, but it is not P7
real-Provider execution evidence and must never be counted toward the 18-round
complex-task acceptance matrix.
"""

from __future__ import annotations

from collections import Counter
from pathlib import Path
from tempfile import TemporaryDirectory

from real_provider_matrix import build_cases, find_linux_race_gate, is_go_race_command, is_unavailable_race_error


EXPECTED_DOMAINS = {"development", "operations", "security", "research", "documents"}
EXPECTED_LEVELS = Counter({"simple": 3, "complex": 2, "recovery": 1})
KNOWN_AGENTS = {
    "software-engineer",
    "operations-specialist",
    "security-researcher",
    "web-security-researcher",
    "research-specialist",
    "documentation-specialist",
}
KNOWN_WORKFLOWS = {
    "complex-project-delivery",
    "plan-fix-audit",
    "operations-runbook",
    "security-audit-evidence-gate",
    "security-fix-plan",
    "authorized-red-team-validation",
    "web-research-risk",
    "parallel-research-review",
    "docs-review-publish",
}


def main() -> int:
    cases = build_cases()
    if len(cases) != 30:
        raise AssertionError(f"expected 30 matrix cases, got {len(cases)}")
    ids = [case.id for case in cases]
    if len(ids) != len(set(ids)):
        raise AssertionError("matrix case IDs must be unique")
    domains = {case.domain for case in cases}
    if domains != EXPECTED_DOMAINS:
        raise AssertionError(f"unexpected matrix domains: {sorted(domains)}")
    for domain in sorted(domains):
        selected = [case for case in cases if case.domain == domain]
        levels = Counter(case.level for case in selected)
        if levels != EXPECTED_LEVELS:
            raise AssertionError(f"{domain} must contain 3 simple, 2 complex, and 1 recovery case: {levels}")
    for case in cases:
        if case.agent not in KNOWN_AGENTS:
            raise AssertionError(f"{case.id} references unknown Agent {case.agent!r}")
        if case.workflow and case.workflow not in KNOWN_WORKFLOWS:
            raise AssertionError(f"{case.id} references unknown Workflow {case.workflow!r}")
        if not case.prompt.strip() or not case.skill.strip():
            raise AssertionError(f"{case.id} is missing a prompt or Skill")
        if not case.required_files and not case.required_terms:
            raise AssertionError(f"{case.id} has no independent deliverable checks")
        if case.domain == "development" and not case.hidden_files:
            raise AssertionError(f"{case.id} has no hidden implementation test")
        if case.domain == "research" and not case.web_fixture:
            raise AssertionError(f"{case.id} must exercise the HTTP research fixture")
        if case.id in {"docs-02-excel", "docs-03-presentation"} and not case.office_expectations:
            raise AssertionError(f"{case.id} must independently parse and verify the produced OOXML")
        if case.id in {"sec-01-code-audit", "sec-02-passive-web", "sec-03-baseline", "sec-04-evidence-confirmation", "sec-05-fix-regression"} and not case.require_clean_run:
            raise AssertionError(f"{case.id} must reject runs containing error events")
        if any("-race" in command for command in case.verify_commands):
            if ".github/workflows/race.yml" not in case.prompt or "不得宣称本机 race 已通过" not in case.prompt:
                raise AssertionError(f"{case.id} must require an auditable CI fallback for unavailable local race checks")
    if not is_go_race_command(("go", "test", "-race", "./...")):
        raise AssertionError("Go race command detection is broken")
    if not is_unavailable_race_error("go: -race requires cgo; enable cgo"):
        raise AssertionError("local race infrastructure failures must be recognized")
    if is_unavailable_race_error("race detector found a data race"):
        raise AssertionError("product race failures must not be treated as infrastructure limitations")
    with TemporaryDirectory() as temp_dir:
        workflow = Path(temp_dir) / ".github" / "workflows" / "race.yml"
        workflow.parent.mkdir(parents=True)
        workflow.write_text("runs-on: ubuntu-latest\nsteps:\n  - run: go test -race ./...\n", encoding="utf-8")
        if find_linux_race_gate(Path(temp_dir)) != ".github/workflows/race.yml":
            raise AssertionError("Ubuntu race gate discovery is broken")
    print(
        "golden Provider case-definition validation passed "
        "(30 cases, 5 domains, 3/2/1 distribution; no model call; not P7 evidence)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
