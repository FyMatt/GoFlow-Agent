"""Kit acceptance checks for the P7 provider harness.

Kits declare how their domain is accepted in ``kit.yaml``:

    acceptance:
      sample_request: ...
      verify_commands: [go test ./..., go vet ./...]
      deliverables: [docs/upgrade.md]
      min_quality_score: 80

This module loads those declarations and checks a delivered workspace against
them, so a new domain is accepted by adding resources instead of editing the
harness cases.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent.parent

try:  # PyYAML is available in the harness environment; keep a clear failure.
    import yaml  # type: ignore
except Exception:  # pragma: no cover - reported at runtime
    yaml = None


DEFAULT_COMMAND_TIMEOUT_SECONDS = 900


@dataclass
class KitAcceptance:
    kit: str
    sample_request: str = ""
    verify_commands: list[str] = field(default_factory=list)
    deliverables: list[str] = field(default_factory=list)
    min_quality_score: int = 0


@dataclass
class AcceptanceCheck:
    kit: str
    passed: bool
    deliverables: list[dict[str, Any]]
    commands: list[dict[str, Any]]
    min_quality_score: int
    errors: list[str]

    def as_dict(self) -> dict[str, Any]:
        return {
            "kit": self.kit,
            "passed": self.passed,
            "deliverables": self.deliverables,
            "commands": self.commands,
            "min_quality_score": self.min_quality_score,
            "errors": self.errors,
        }


def load_kit_acceptance(kits_root: Path) -> dict[str, KitAcceptance]:
    if yaml is None:
        raise RuntimeError("PyYAML is required to read kit acceptance declarations")
    result: dict[str, KitAcceptance] = {}
    if not kits_root.is_dir():
        return result
    for path in sorted(kits_root.glob("*/kit.yaml")):
        try:
            document = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        except Exception:
            continue
        if not isinstance(document, dict):
            continue
        acceptance = document.get("acceptance")
        if not isinstance(acceptance, dict):
            continue
        name = str(document.get("name") or path.parent.name)
        result[name] = KitAcceptance(
            kit=name,
            sample_request=str(acceptance.get("sample_request") or ""),
            verify_commands=[str(item) for item in acceptance.get("verify_commands") or []],
            deliverables=[str(item) for item in acceptance.get("deliverables") or []],
            min_quality_score=int(acceptance.get("min_quality_score") or 0),
        )
    return result


def map_case_to_kit(case_id: str) -> str:
    """Map a harness case id to a kit that declares acceptance."""
    candidates = candidate_kits(case_id)
    return candidates[0] if candidates else ""


def candidate_kits(case_id: str) -> list[str]:
    """Ordered kit candidates for a case id; the first declared one wins."""
    lowered = (case_id or "").lower()
    mapping = (
        ("development", ["software-engineering-kit"]),
        ("operations", ["operations-runbook-kit"]),
        ("security", ["security-research-kit", "security-incident-kit", "web-security-kit"]),
        ("cross-domain", ["multi-domain-agent-kit"]),
        ("research", ["internet-research-kit"]),
        ("documents", ["documentation-kit"]),
    )
    for marker, kits in mapping:
        if marker in lowered:
            return list(kits)
    return []


def resolve_project_root(workspace: Path) -> Path:
    """Find the delivered project root under an acceptance evidence tree.

    Acceptance workspaces nest evidence as 交付证据/<domain>/<case>/第N轮/,
    while the P7 runner passes the prepared project root directly.
    """
    markers = ("go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml")
    if any((workspace / marker).is_file() for marker in markers):
        return workspace
    candidates: list[Path] = []
    for depth in range(1, 6):
        for path in workspace.glob("/".join(["*"] * depth)):
            if path.is_dir() and any((path / marker).is_file() for marker in markers):
                candidates.append(path)
        if candidates:
            candidates.sort(key=lambda item: len(item.parts))
            return candidates[0]
    return workspace


def check_workspace(
    workspace: Path,
    acceptance: KitAcceptance,
    timeout_seconds: int = DEFAULT_COMMAND_TIMEOUT_SECONDS,
) -> AcceptanceCheck:
    deliverables: list[dict[str, Any]] = []
    errors: list[str] = []
    for relative in acceptance.deliverables:
        path = (workspace / relative).resolve()
        exists = path.is_file()
        deliverables.append(
            {"path": relative, "exists": exists, "bytes": path.stat().st_size if exists else 0}
        )
        if not exists:
            errors.append(f"missing deliverable: {relative}")

    commands: list[dict[str, Any]] = []
    environment = os.environ.copy()
    environment.setdefault("GIT_PAGER", "cat")
    for command in acceptance.verify_commands:
        try:
            completed = subprocess.run(
                command,
                shell=True,
                cwd=str(workspace),
                capture_output=True,
                text=True,
                timeout=timeout_seconds,
                env=environment,
            )
        except subprocess.TimeoutExpired:
            commands.append({"command": command, "ok": False, "error": "timeout"})
            errors.append(f"command timed out: {command}")
            continue
        output = ((completed.stdout or "") + (completed.stderr or "")).strip()
        ok = completed.returncode == 0
        commands.append(
            {
                "command": command,
                "exit_code": completed.returncode,
                "ok": ok,
                "output_tail": output[-600:],
            }
        )
        if not ok:
            errors.append(f"command failed ({completed.returncode}): {command}")

    return AcceptanceCheck(
        kit=acceptance.kit,
        passed=not errors,
        deliverables=deliverables,
        commands=commands,
        min_quality_score=acceptance.min_quality_score,
        errors=errors,
    )


def check_case_workspace(
    case_id: str,
    workspace: Path,
    kits_root: Path | None = None,
    timeout_seconds: int = DEFAULT_COMMAND_TIMEOUT_SECONDS,
) -> dict[str, Any]:
    kits = load_kit_acceptance(kits_root or ROOT / "kits")
    candidates = candidate_kits(case_id)
    acceptance = next((kits[name] for name in candidates if name in kits), None)
    if acceptance is None:
        return {
            "kit": candidates[0] if candidates else "",
            "passed": False,
            "skipped": True,
            "errors": [f"no kit among {candidates!r} declares acceptance"],
        }
    return check_workspace(workspace, acceptance, timeout_seconds).as_dict()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kit", default="", help="kit name; omit to read all")
    parser.add_argument("--workspace", required=True, help="delivered workspace to check")
    parser.add_argument("--kits-root", default=str(ROOT / "kits"))
    parser.add_argument("--timeout", type=int, default=DEFAULT_COMMAND_TIMEOUT_SECONDS)
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    workspace = resolve_project_root(Path(args.workspace).resolve())
    kits = load_kit_acceptance(Path(args.kits_root))
    if args.kit:
        acceptance = kits.get(args.kit)
        if acceptance is None:
            print(f"unknown kit or missing acceptance: {args.kit}", file=sys.stderr)
            return 2
        results = [check_workspace(workspace, acceptance, args.timeout).as_dict()]
    else:
        results = [
            {"kit": acceptance.kit, "sample_request": acceptance.sample_request,
             "verify_commands": acceptance.verify_commands,
             "deliverables": acceptance.deliverables,
             "min_quality_score": acceptance.min_quality_score}
            for acceptance in kits.values()
        ]
    print(json.dumps(results, ensure_ascii=False, indent=2), file=sys.stdout)
    return 0 if all(item.get("passed", True) for item in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
