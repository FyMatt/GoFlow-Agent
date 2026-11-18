#!/usr/bin/env python3
"""Validate the persisted six-domain P7 acceptance matrix."""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any

try:
    from .acceptance_evidence import DOMAIN_NAMES, validate_acceptance_round
    from .p7_complex_cases import cases
except ImportError:  # direct script execution
    from acceptance_evidence import DOMAIN_NAMES, validate_acceptance_round
    from p7_complex_cases import cases


def expected_cases() -> dict[str, str]:
    return {case.domain: case.id for case in cases()}


def validate_p7_matrix(root: Path, repetitions: int = 3) -> dict[str, Any]:
    if repetitions < 1:
        raise RuntimeError("repetitions must be positive")
    catalog = list(cases())
    case_ids = [case.id for case in catalog]
    domains = [case.domain for case in catalog]
    if len(catalog) != 6 or len(case_ids) != len(set(case_ids)) or len(domains) != len(set(domains)):
        raise RuntimeError("P7 case catalog must contain six unique cases and six unique domains")
    expected = {case.domain: case.id for case in catalog}
    required_domains = {"development", "security", "operations", "research", "documents", "cross-domain"}
    if set(expected) != required_domains:
        raise RuntimeError(f"P7 case catalog is incomplete: {sorted(expected)}")
    evidence_root = root.resolve() / "交付证据"
    if not evidence_root.is_dir():
        raise RuntimeError(f"acceptance evidence root is missing: {evidence_root}")

    seen_run_ids: set[str] = set()
    rounds: list[dict[str, Any]] = []
    for domain, case_id in sorted(expected.items()):
        case_root = evidence_root / DOMAIN_NAMES[domain] / case_id
        if not case_root.is_dir():
            raise RuntimeError(f"missing evidence case directory: {case_root}")
        round_dirs: dict[int, Path] = {}
        for path in case_root.iterdir():
            if not path.is_dir():
                continue
            match = re.fullmatch(r"第([1-9][0-9]*)轮", path.name)
            if match:
                round_dirs[int(match.group(1))] = path
        required_numbers = list(range(1, repetitions + 1))
        for number in required_numbers:
            target = round_dirs.get(number)
            if target is None:
                missing = [item for item in required_numbers[number - 1 :] if item not in round_dirs]
                raise RuntimeError(f"{case_id} is missing consecutive rounds: {missing}")
            result = validate_acceptance_round(target, require_success=True)
            report = json.loads((target / "report.json").read_text(encoding="utf-8"))
            if report.get("case_id") != case_id or report.get("domain") != domain or int(report.get("repetition", 0) or 0) != number:
                raise RuntimeError(f"{case_id} round {number} has inconsistent identity")
            provider = report.get("provider")
            if (
                not isinstance(provider, dict)
                or provider.get("valid") is not True
                or provider.get("configured") is not True
                or provider.get("complete") is not True
                or provider.get("skipped") is True
            ):
                raise RuntimeError(f"{case_id} round {number} lacks complete Provider evidence")
            run_id = str(report.get("run_id") or "").strip()
            if run_id in seen_run_ids:
                raise RuntimeError(f"duplicate task run ID in P7 matrix: {run_id}")
            seen_run_ids.add(run_id)
            rounds.append({
                "domain": domain,
                "case_id": case_id,
                "repetition": number,
                "run_id": run_id,
                "quality_score": result["quality_score"],
                "evidence_directory": str(target),
            })
    return {
        "schema_version": 1,
        "passed": len(rounds) == len(expected) * repetitions,
        "domains": len(expected),
        "repetitions": repetitions,
        "rounds": rounds,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("acceptance_workspace", type=Path)
    parser.add_argument("--repetitions", type=int, default=3)
    args = parser.parse_args()
    try:
        report = validate_p7_matrix(args.acceptance_workspace, args.repetitions)
    except RuntimeError as exc:
        print(json.dumps({"schema_version": 1, "passed": False, "error": str(exc)}, ensure_ascii=False, indent=2))
        return 1
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
