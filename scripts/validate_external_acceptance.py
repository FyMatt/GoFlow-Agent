#!/usr/bin/env python3
"""Validate completed Linux-native or real-vision acceptance evidence."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any


LINUX_REQUIRED_STEPS = {
    "host-linux",
    "host-x86-64",
    "host-not-containerized",
    "host-not-wsl",
    "host-not-virtual-machine",
    "kernel-at-least-5-15",
    "cgroup-v2-mounted",
    "command-go",
    "command-python3",
    "command-node",
    "native-sandbox-malicious-fixture",
    "native-sandbox-test-evidence",
    "python-node-mcp-protocol-boundary",
    "python-node-mcp-test-evidence",
    "final-release-archive-present",
    "final-release-archive-sha256",
    "install-a",
    "first-start-a",
    "reject-invalid-upgrade",
    "failed-upgrade-keeps-installation",
    "upgrade-b",
    "upgrade-backup",
    "upgrade-preserves-data",
    "restart-after-upgrade-b",
    "rollback-to-a",
    "rollback-restores-application",
    "rollback-preserves-data",
    "restart-after-rollback-a",
    "upgrade-b-after-rollback",
    "uninstall-preserve-data",
    "uninstall-data-preserved",
    "reinstall-b",
    "uninstall-purge-data",
    "purge-removes-data",
    "generated-artifacts-cleaned",
}

VISION_REQUIRED_GATES = {
    "provider_vision",
    "provider_tool_calling",
    "secret_not_leaked",
    "completed",
    "attachment_persisted",
    "real_diff",
    "tool_evidence",
    "terminal_quality",
    "delivery_applied",
    "delivery_success",
    "persisted_session",
    "hidden_tests",
    "go_vet",
    "gofmt",
    "implementation_changed",
}

VISION_PROBE_REQUIRED_GATES = {
    "provider_configured",
    "provider_valid",
    "provider_vision",
    "provider_tool_calling",
    "secret_not_leaked",
}


def read_report(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"cannot read acceptance report {path}: {exc}") from exc
    if not isinstance(payload, dict) or payload.get("schema_version") != 1:
        raise RuntimeError("acceptance report has an unsupported schema")
    if payload.get("passed") is not True or str(payload.get("error") or "").strip():
        raise RuntimeError("acceptance report is not a successful terminal report")
    return payload


def evidence_file(root: Path, name: str, label: str) -> Path:
    if not name or Path(name).name != name:
        raise RuntimeError(f"{label} has an invalid evidence file reference")
    path = root / name
    if not path.is_file() or path.stat().st_size == 0:
        raise RuntimeError(f"{label} evidence file is missing or empty: {name}")
    return path


def validate_linux_report(report: dict[str, Any], evidence_root: Path | None = None) -> None:
    host = report.get("host")
    if not isinstance(host, dict):
        raise RuntimeError("Linux acceptance report has no host evidence")
    if host.get("system") != "Linux" or str(host.get("machine") or "").casefold() not in {"x86_64", "amd64"}:
        raise RuntimeError("Linux acceptance report is not from Linux x86_64")
    if not isinstance(host.get("pid1_cgroup"), str) or not isinstance(host.get("hypervisor_type"), str) or not isinstance(host.get("cpu_hypervisor_flag"), bool):
        raise RuntimeError("Linux acceptance report has incomplete physical-host evidence")
    virtualization_detected = host.get("cpu_hypervisor_flag") is True or str(host.get("hypervisor_type")).strip().casefold() not in {"", "unavailable", "none"}
    policy = report.get("host_policy")
    if virtualization_detected and not (isinstance(policy, dict) and policy.get("allow_virtual_machine") is True):
        raise RuntimeError("Linux acceptance report indicates a virtualized host without explicit VM policy")
    if not str(report.get("started_at") or "") or not str(report.get("finished_at") or ""):
        raise RuntimeError("Linux acceptance report has no bounded execution timestamps")
    steps = report.get("steps")
    if not isinstance(steps, list):
        raise RuntimeError("Linux acceptance report has no step evidence")
    by_name: dict[str, dict[str, Any]] = {}
    for step in steps:
        if not isinstance(step, dict) or not str(step.get("name") or ""):
            raise RuntimeError("Linux acceptance report contains an invalid step")
        name = str(step["name"])
        if name in by_name:
            raise RuntimeError(f"Linux acceptance report contains duplicate step {name}")
        by_name[name] = step
    missing = sorted(LINUX_REQUIRED_STEPS - by_name.keys())
    failed = sorted(name for name in LINUX_REQUIRED_STEPS if name in by_name and by_name[name].get("passed") is not True)
    if missing or failed:
        raise RuntimeError(f"Linux acceptance evidence is incomplete: missing={missing}, failed={failed}")
    if by_name["reject-invalid-upgrade"].get("expected") != "failure":
        raise RuntimeError("Linux invalid-upgrade step did not require a real failure")
    for name in ("native-sandbox-malicious-fixture", "python-node-mcp-protocol-boundary"):
        if by_name[name].get("timed_out") is not False or int(by_name[name].get("exit_code", -1)) != 0:
            raise RuntimeError(f"Linux test step {name} has invalid process evidence")
        log_name = str(by_name[name].get("log") or "")
        if not log_name or Path(log_name).name != log_name:
            raise RuntimeError(f"Linux test step {name} has an invalid log reference")
        if evidence_root is not None:
            evidence_file(evidence_root, log_name, f"Linux test step {name}")


def validate_vision_input_policy(report: dict[str, Any]) -> None:
    policy = report.get("input_policy")
    if not isinstance(policy, dict) or not all(
        policy.get(name) is True
        for name in ("external_inputs_required", "external_secret", "external_provider_module", "read_only_enforced")
    ):
        raise RuntimeError("vision acceptance did not enforce formal external inputs")


def validate_vision_provider_evidence(report: dict[str, Any]) -> None:
    provider = report.get("provider")
    capabilities = provider.get("capabilities") if isinstance(provider, dict) else None
    if not isinstance(provider, dict) or provider.get("valid") is not True or provider.get("configured") is not True:
        raise RuntimeError("vision acceptance has no valid configured Provider evidence")
    if provider.get("complete") is not True:
        raise RuntimeError("vision acceptance Provider probe is incomplete")
    if not isinstance(capabilities, dict) or capabilities.get("vision") is not True:
        raise RuntimeError("vision acceptance has no verified vision capability")
    if capabilities.get("tool_calling") is not True:
        raise RuntimeError("vision acceptance has no verified tool-calling capability")
    details = provider.get("capability_details")
    if not isinstance(details, list):
        raise RuntimeError("vision acceptance has no raw Provider capability evidence")
    by_name: dict[str, dict[str, Any]] = {}
    for detail in details:
        if not isinstance(detail, dict) or not str(detail.get("name") or ""):
            raise RuntimeError("vision acceptance contains invalid Provider capability evidence")
        name = str(detail["name"])
        if name in by_name:
            raise RuntimeError(f"vision acceptance contains duplicate Provider capability evidence for {name}")
        by_name[name] = detail
    for name in ("vision", "tool_calling"):
        if name not in by_name or by_name[name].get("supported") is not True or by_name[name].get("status") != "supported":
            raise RuntimeError(f"vision acceptance has no successful raw {name} capability evidence")


def validate_vision_cleanup_and_secret(report: dict[str, Any]) -> None:
    if report.get("workspace_preserved") is not False or report.get("regenerated_artifacts_cleaned") is not True:
        raise RuntimeError("vision acceptance did not clean its sensitive generated state")
    secret_safety = report.get("secret_safety")
    if (
        not isinstance(secret_safety, dict)
        or secret_safety.get("scan_performed") is not True
        or secret_safety.get("scan_complete") is not True
        or int(secret_safety.get("total_leaks_detected", -1)) != 0
        or secret_safety.get("redacted_files") != []
        or secret_safety.get("scan_errors") != []
    ):
        raise RuntimeError("vision acceptance has no clean Provider secret scan evidence")


def validate_vision_report(report: dict[str, Any], evidence_root: Path | None = None) -> None:
    if report.get("mode") not in (None, "vision_engineering"):
        raise RuntimeError("vision acceptance report has the wrong execution mode")
    validate_vision_input_policy(report)
    validate_vision_provider_evidence(report)
    fixture = report.get("fixture")
    if not isinstance(fixture, dict) or fixture.get("requirements_only_in_image") is not True:
        raise RuntimeError("vision acceptance fixture did not keep requirements exclusively in the image")
    if int(fixture.get("width", 0)) < 900 or int(fixture.get("height", 0)) < 250 or len(str(fixture.get("sha256") or "")) != 64:
        raise RuntimeError("vision acceptance fixture evidence is invalid")
    if evidence_root is not None:
        image_path = evidence_file(evidence_root, str(fixture.get("image") or ""), "vision fixture")
        actual_digest = hashlib.sha256(image_path.read_bytes()).hexdigest()
        if actual_digest != str(fixture.get("sha256") or ""):
            raise RuntimeError("vision fixture digest does not match the report")
        evidence_file(evidence_root, "server.log", "vision server")
    verification = report.get("verification")
    gates = verification.get("hard_gates") if isinstance(verification, dict) else None
    if not isinstance(gates, dict):
        raise RuntimeError("vision acceptance report has no hard-gate evidence")
    missing = sorted(VISION_REQUIRED_GATES - gates.keys())
    failed = sorted(name for name in VISION_REQUIRED_GATES if gates.get(name) is not True)
    if missing or failed:
        raise RuntimeError(f"vision acceptance evidence is incomplete: missing={missing}, failed={failed}")
    run = report.get("run")
    if not isinstance(run, dict) or str(run.get("status") or "").casefold() not in {"completed", "succeeded"}:
        raise RuntimeError("vision acceptance has no successful terminal run")
    validate_vision_cleanup_and_secret(report)


def validate_vision_probe_report(report: dict[str, Any], evidence_root: Path | None = None) -> None:
    if report.get("mode") != "provider_probe":
        raise RuntimeError("vision Provider probe report has the wrong execution mode")
    validate_vision_input_policy(report)
    validate_vision_provider_evidence(report)
    verification = report.get("verification")
    gates = verification.get("hard_gates") if isinstance(verification, dict) else None
    if not isinstance(gates, dict):
        raise RuntimeError("vision Provider probe report has no hard-gate evidence")
    missing = sorted(VISION_PROBE_REQUIRED_GATES - gates.keys())
    failed = sorted(name for name in VISION_PROBE_REQUIRED_GATES if gates.get(name) is not True)
    if missing or failed:
        raise RuntimeError(f"vision Provider probe evidence is incomplete: missing={missing}, failed={failed}")
    if report.get("run") not in ({}, None) or report.get("evidence") not in ({}, None):
        raise RuntimeError("vision Provider probe unexpectedly contains Agent task evidence")
    if int(report.get("duration_ms", 0)) <= 0:
        raise RuntimeError("vision Provider probe has no bounded duration evidence")
    if evidence_root is not None:
        evidence_file(evidence_root, "server.log", "vision Provider probe server")
    validate_vision_cleanup_and_secret(report)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("linux", "vision", "vision-probe"))
    parser.add_argument("report")
    args = parser.parse_args()
    report_path = Path(args.report).resolve()
    report = read_report(report_path)
    if args.kind == "linux":
        validate_linux_report(report, report_path.parent)
    elif args.kind == "vision-probe":
        validate_vision_probe_report(report, report_path.parent)
    else:
        validate_vision_report(report, report_path.parent)
    print(f"{args.kind} acceptance report validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
