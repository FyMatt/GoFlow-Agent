#!/usr/bin/env python3
"""Validate that adversarial fixtures and their release-blocking tests remain wired."""

from __future__ import annotations

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FIXTURES = ROOT / "internal" / "promptguard" / "testdata" / "adversarial_cases.json"


def require_text(path: Path, needles: list[str]) -> None:
    text = path.read_text(encoding="utf-8")
    missing = [needle for needle in needles if needle not in text]
    if missing:
        raise SystemExit(f"{path.relative_to(ROOT)} missing security regression wiring: {missing}")


def main() -> int:
    cases = json.loads(FIXTURES.read_text(encoding="utf-8"))
    required = {"prompt_injection", "malicious_tool_result", "parameter_flow", "source_confusion"}
    categories = {str(case.get("category", "")) for case in cases}
    missing = sorted(required - categories)
    if missing:
        raise SystemExit(f"adversarial fixture categories missing: {missing}")
    if any(not case.get("content") or not case.get("signals") for case in cases):
        raise SystemExit("every adversarial fixture must define content and expected signals")

    require_text(
        ROOT / "internal" / "promptguard" / "guard_test.go",
        ["TestSecurityRegressionAdversarialFixturesAreDetectedAndFramed", "adversarial_cases.json"],
    )
    require_text(
        ROOT / "internal" / "agent" / "security_regression_test.go",
        ["TestSecurityRegressionUntrustedToolDataForcesFreshApproval", "ApprovedTaskScope"],
    )
    require_text(
        ROOT / "internal" / "agent" / "execution.go",
        ["UntrustedInstructionsObserved", "untrustedObservationRequiresApproval"],
    )
    print(f"security regression validation passed ({len(cases)} adversarial fixtures, {len(categories)} categories)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
