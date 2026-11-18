from __future__ import annotations

import unittest
import tempfile
from pathlib import Path

from scripts.validate_external_acceptance import (
    LINUX_REQUIRED_STEPS,
    VISION_PROBE_REQUIRED_GATES,
    VISION_REQUIRED_GATES,
    validate_linux_report,
    validate_vision_probe_report,
    validate_vision_report,
)


def valid_vision_provider() -> dict:
    return {
        "valid": True,
        "configured": True,
        "complete": True,
        "capabilities": {"vision": True, "tool_calling": True},
        "capability_details": [
            {"name": "vision", "status": "supported", "supported": True},
            {"name": "tool_calling", "status": "supported", "supported": True},
        ],
    }


class ExternalAcceptanceValidationTests(unittest.TestCase):
    def test_linux_report_requires_all_hard_gate_steps(self) -> None:
        steps = [{"name": name, "passed": True} for name in sorted(LINUX_REQUIRED_STEPS)]
        by_name = {step["name"]: step for step in steps}
        by_name["reject-invalid-upgrade"]["expected"] = "failure"
        for name in ("native-sandbox-malicious-fixture", "python-node-mcp-protocol-boundary"):
            by_name[name].update({"timed_out": False, "exit_code": 0, "log": f"{name}.log"})
        report = {
            "host": {"system": "Linux", "machine": "x86_64", "pid1_cgroup": "0::/", "hypervisor_type": "unavailable", "cpu_hypervisor_flag": False},
            "started_at": "2026-01-01T00:00:00+00:00",
            "finished_at": "2026-01-01T00:01:00+00:00",
            "steps": steps,
        }
        validate_linux_report(report)
        by_name["python-node-mcp-test-evidence"]["passed"] = False
        with self.assertRaisesRegex(RuntimeError, "python-node-mcp-test-evidence"):
            validate_linux_report(report)

    def test_linux_report_rejects_duplicate_steps(self) -> None:
        report = {
            "host": {"system": "Linux", "machine": "x86_64", "pid1_cgroup": "0::/", "hypervisor_type": "unavailable", "cpu_hypervisor_flag": False},
            "started_at": "start",
            "finished_at": "finish",
            "steps": [{"name": "host-linux", "passed": True}, {"name": "host-linux", "passed": True}],
        }
        with self.assertRaisesRegex(RuntimeError, "duplicate"):
            validate_linux_report(report)

    def test_linux_report_rejects_virtualized_host_evidence(self) -> None:
        report = {
            "host": {"system": "Linux", "machine": "x86_64", "pid1_cgroup": "0::/", "hypervisor_type": "kvm", "cpu_hypervisor_flag": True},
            "started_at": "start",
            "finished_at": "finish",
            "steps": [],
        }
        with self.assertRaisesRegex(RuntimeError, "virtualized host"):
            validate_linux_report(report)

    def test_vision_report_requires_every_engineering_gate(self) -> None:
        report = {
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": valid_vision_provider(),
            "fixture": {"requirements_only_in_image": True, "width": 1092, "height": 352, "sha256": "a" * 64},
            "verification": {"hard_gates": {name: True for name in VISION_REQUIRED_GATES}},
            "run": {"status": "completed"},
            "workspace_preserved": False,
            "regenerated_artifacts_cleaned": True,
            "secret_safety": {"scan_performed": True, "scan_complete": True, "scan_errors": [], "total_leaks_detected": 0, "redacted_files": []},
        }
        validate_vision_report(report)
        report["verification"]["hard_gates"]["hidden_tests"] = False
        with self.assertRaisesRegex(RuntimeError, "hidden_tests"):
            validate_vision_report(report)

    def test_vision_report_requires_clean_secret_scan(self) -> None:
        report = {
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": valid_vision_provider(),
            "fixture": {"requirements_only_in_image": True, "width": 1092, "height": 352, "sha256": "a" * 64},
            "verification": {"hard_gates": {name: True for name in VISION_REQUIRED_GATES}},
            "run": {"status": "completed"},
            "workspace_preserved": False,
            "regenerated_artifacts_cleaned": True,
            "secret_safety": {"scan_performed": True, "scan_complete": True, "scan_errors": [], "total_leaks_detected": 1, "redacted_files": ["server.log"]},
        }
        with self.assertRaisesRegex(RuntimeError, "secret scan"):
            validate_vision_report(report)

    def test_vision_report_rejects_incomplete_secret_scan(self) -> None:
        report = {
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": valid_vision_provider(),
            "fixture": {"requirements_only_in_image": True, "width": 1092, "height": 352, "sha256": "a" * 64},
            "verification": {"hard_gates": {name: True for name in VISION_REQUIRED_GATES}},
            "run": {"status": "completed"},
            "workspace_preserved": False,
            "regenerated_artifacts_cleaned": True,
            "secret_safety": {"scan_performed": True, "scan_complete": False, "scan_errors": ["server.log: access denied"], "total_leaks_detected": 0, "redacted_files": []},
        }
        with self.assertRaisesRegex(RuntimeError, "secret scan"):
            validate_vision_report(report)

    def test_vision_report_requires_formal_external_inputs(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "formal external inputs"):
            validate_vision_report({"input_policy": {"external_inputs_required": False}})

    def test_vision_report_requires_tool_calling_probe_evidence(self) -> None:
        report = {
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": {**valid_vision_provider(), "capabilities": {"vision": True, "tool_calling": False}},
        }
        with self.assertRaisesRegex(RuntimeError, "tool-calling"):
            validate_vision_report(report)

    def test_vision_report_requires_raw_capability_evidence(self) -> None:
        report = {
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": {**valid_vision_provider(), "capability_details": []},
        }
        with self.assertRaisesRegex(RuntimeError, "raw vision"):
            validate_vision_report(report)

    def test_vision_probe_report_requires_capabilities_cleanup_and_no_agent_run(self) -> None:
        report = {
            "mode": "provider_probe",
            "input_policy": {
                "external_inputs_required": True,
                "external_secret": True,
                "external_provider_module": True,
                "read_only_enforced": True,
            },
            "provider": valid_vision_provider(),
            "verification": {"hard_gates": {name: True for name in VISION_PROBE_REQUIRED_GATES}},
            "run": {},
            "evidence": {},
            "duration_ms": 100,
            "workspace_preserved": False,
            "regenerated_artifacts_cleaned": True,
            "secret_safety": {"scan_performed": True, "scan_complete": True, "scan_errors": [], "total_leaks_detected": 0, "redacted_files": []},
        }
        validate_vision_probe_report(report)
        report["run"] = {"status": "completed"}
        with self.assertRaisesRegex(RuntimeError, "Agent task evidence"):
            validate_vision_probe_report(report)

    def test_linux_report_requires_referenced_logs_in_evidence_directory(self) -> None:
        steps = [{"name": name, "passed": True} for name in sorted(LINUX_REQUIRED_STEPS)]
        by_name = {step["name"]: step for step in steps}
        by_name["reject-invalid-upgrade"]["expected"] = "failure"
        for name in ("native-sandbox-malicious-fixture", "python-node-mcp-protocol-boundary"):
            by_name[name].update({"timed_out": False, "exit_code": 0, "log": f"{name}.log"})
        report = {
            "host": {"system": "Linux", "machine": "x86_64", "pid1_cgroup": "0::/", "hypervisor_type": "unavailable", "cpu_hypervisor_flag": False},
            "started_at": "start",
            "finished_at": "finish",
            "steps": steps,
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with self.assertRaisesRegex(RuntimeError, "missing or empty"):
                validate_linux_report(report, root)
            for name in ("native-sandbox-malicious-fixture", "python-node-mcp-protocol-boundary"):
                (root / f"{name}.log").write_text("PASS\n", encoding="utf-8")
            validate_linux_report(report, root)


if __name__ == "__main__":
    unittest.main()
