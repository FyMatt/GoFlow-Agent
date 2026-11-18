#!/usr/bin/env python3
"""Run GoFlow's mandatory Linux x86_64 native product acceptance.

This runner must execute on a real Linux host. It intentionally hard-fails when
native sandbox primitives, Python, Node.js, or the install lifecycle are not
available, and writes a machine-readable report plus per-step logs.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import re
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
MAX_ARCHIVE_MEMBERS = 20_000
MAX_ARCHIVE_EXTRACTED_BYTES = 1 << 30


class Acceptance:
    def __init__(self, output: Path, step_timeout: float = 600.0, *, allow_virtual_machine: bool = False) -> None:
        self.output = output
        self.output.mkdir(parents=True, exist_ok=False)
        self.steps: list[dict[str, Any]] = []
        self.started = time.monotonic()
        self.started_at = datetime.now(timezone.utc)
        self.step_timeout = step_timeout
        self.allow_virtual_machine = allow_virtual_machine

    def run(
        self,
        name: str,
        command: list[str],
        *,
        env: dict[str, str] | None = None,
        expect_success: bool = True,
    ) -> int:
        log_path = self.output / f"{len(self.steps) + 1:02d}-{name}.log"
        started = time.monotonic()
        print(f"[{name}] {shlex.join(command)}", flush=True)
        timed_out = False
        with log_path.open("w", encoding="utf-8") as log:
            process = subprocess.Popen(
                command,
                cwd=ROOT,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                text=True,
                start_new_session=True,
            )
            try:
                exit_code = process.wait(timeout=self.step_timeout)
            except subprocess.TimeoutExpired:
                timed_out = True
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                exit_code = process.wait(timeout=10)
                log.write(f"\nacceptance step timed out after {self.step_timeout:.1f} seconds\n")
        passed = not timed_out and ((exit_code == 0) if expect_success else (exit_code != 0))
        step = {
            "name": name,
            "passed": passed,
            "exit_code": exit_code,
            "timed_out": timed_out,
            "expected": "success" if expect_success else "failure",
            "duration_ms": round((time.monotonic() - started) * 1000),
            "log": log_path.name,
            "command": command,
        }
        self.steps.append(step)
        if not passed:
            outcome = "timeout" if timed_out else f"exit code {exit_code}"
            raise RuntimeError(f"{name} failed with {outcome}; see {log_path}")
        return exit_code

    def check(self, name: str, condition: bool, detail: str) -> None:
        self.steps.append({"name": name, "passed": condition, "detail": detail})
        if not condition:
            raise RuntimeError(f"{name} failed: {detail}")

    def report(self, passed: bool, error: str = "") -> Path:
        payload = {
            "schema_version": 1,
            "passed": passed,
            "error": error,
            "started_at": self.started_at.isoformat(),
            "finished_at": datetime.now(timezone.utc).isoformat(),
            "duration_ms": round((time.monotonic() - self.started) * 1000),
            "host": {
                "system": platform.system(),
                "machine": platform.machine(),
                "release": platform.release(),
                "python": platform.python_version(),
                "go": tool_version(["go", "version"]),
                "node": tool_version(["node", "--version"]),
                "pid1_cgroup": host_text("/proc/1/cgroup"),
                "hypervisor_type": host_text("/sys/hypervisor/type"),
                "cpu_hypervisor_flag": cpu_has_hypervisor_flag(),
            },
            "host_policy": {
                "allow_virtual_machine": bool(getattr(self, "allow_virtual_machine", False)),
            },
            "steps": self.steps,
        }
        report_path = self.output / "report.json"
        report_path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return report_path


def tool_version(command: list[str]) -> str:
    try:
        return subprocess.check_output(command, text=True, stderr=subprocess.STDOUT, timeout=10).strip()
    except (OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return "unavailable"


def host_text(path: str) -> str:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").strip()[:4096]
    except OSError:
        return "unavailable"


def cpu_has_hypervisor_flag() -> bool:
    return bool(re.search(r"(?im)^flags\s*:.*\bhypervisor\b", host_text("/proc/cpuinfo")))


def kernel_version(release: str) -> tuple[int, int]:
    match = re.match(r"^(\d+)\.(\d+)", release.strip())
    if not match:
        return 0, 0
    return int(match.group(1)), int(match.group(2))


def detect_container_environment() -> str:
    if Path("/.dockerenv").exists():
        return "/.dockerenv"
    if Path("/run/.containerenv").exists():
        return "/run/.containerenv"
    if os.environ.get("container", "").strip():
        return "container environment variable"
    try:
        cgroup = Path("/proc/1/cgroup").read_text(encoding="utf-8", errors="replace").casefold()
    except OSError:
        cgroup = ""
    for marker in ("docker", "kubepods", "containerd", "podman", "lxc"):
        if marker in cgroup:
            return f"/proc/1/cgroup contains {marker}"
    return ""


def detect_virtual_machine(
    *,
    detector_path: str | None = None,
    read_text: Any | None = None,
) -> str:
    read_text = read_text or (lambda path: path.read_text(encoding="utf-8", errors="replace").strip())
    detector = detector_path if detector_path is not None else shutil.which("systemd-detect-virt")
    if detector:
        try:
            completed = subprocess.run(
                [detector, "--vm"],
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=10,
                check=False,
            )
            if completed.returncode == 0:
                return completed.stdout.strip() or "systemd-detect-virt reported a VM"
        except (OSError, subprocess.TimeoutExpired):
            pass
    dmi_markers = ("kvm", "qemu", "vmware", "virtualbox", "hyper-v", "virtual machine", "xen", "bhyve", "parallels")
    for path in (
        Path("/sys/class/dmi/id/product_name"),
        Path("/sys/class/dmi/id/sys_vendor"),
        Path("/sys/hypervisor/type"),
    ):
        try:
            value = read_text(path)
        except OSError:
            continue
        if any(marker in value.casefold() for marker in dmi_markers):
            return f"{path}: {value}"
    try:
        cpuinfo = read_text(Path("/proc/cpuinfo"))
    except OSError:
        cpuinfo = ""
    if re.search(r"(?im)^flags\s*:.*\bhypervisor\b", cpuinfo):
        return "/proc/cpuinfo exposes the hypervisor CPU flag"
    return ""


def require_host(acceptance: Acceptance, *, allow_virtual_machine: bool = False) -> None:
    acceptance.check("host-linux", platform.system() == "Linux", platform.system())
    acceptance.check("host-x86-64", platform.machine().lower() in {"x86_64", "amd64"}, platform.machine())
    container_reason = detect_container_environment()
    acceptance.check("host-not-containerized", not container_reason, container_reason or "native host")
    release = platform.release()
    acceptance.check("host-not-wsl", "microsoft" not in release.casefold(), release)
    virtual_machine = detect_virtual_machine()
    acceptance.check(
        "host-not-virtual-machine",
        not virtual_machine or allow_virtual_machine,
        (f"{virtual_machine}; accepted by explicit --allow-virtual-machine policy" if virtual_machine and allow_virtual_machine else virtual_machine or "physical host"),
    )
    acceptance.check("kernel-at-least-5-15", kernel_version(release) >= (5, 15), release)
    acceptance.check(
        "cgroup-v2-mounted",
        Path("/sys/fs/cgroup/cgroup.controllers").is_file(),
        "/sys/fs/cgroup/cgroup.controllers",
    )
    for command in ("go", "python3", "node"):
        acceptance.check(f"command-{command}", shutil.which(command) is not None, command)


def release_archive(dist: Path, version: str) -> Path:
    return dist / f"goflow-agent_{version}_linux_amd64.tar.gz"


def validate_go_test_log(log_path: Path, required_tests: tuple[str, ...]) -> None:
    content = log_path.read_text(encoding="utf-8", errors="replace")
    skipped = re.findall(r"(?m)^--- SKIP: ([^ ]+)", content)
    if skipped:
        raise RuntimeError(f"Go acceptance tests were skipped: {', '.join(skipped)}")
    missing = [name for name in required_tests if not re.search(rf"(?m)^--- PASS: {re.escape(name)}(?: \(|$)", content)]
    if missing:
        raise RuntimeError(f"Go acceptance log has no PASS evidence for: {', '.join(missing)}")


def validate_archive_digest(archive: Path, checksum_path: Path) -> str:
    expected = ""
    for line in checksum_path.read_text(encoding="utf-8").splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1].lstrip("*") == archive.name:
            expected = fields[0].lower()
            break
    if not expected:
        raise RuntimeError(f"SHA256SUMS has no entry for {archive.name}")
    actual = hashlib.sha256(archive.read_bytes()).hexdigest()
    if actual != expected:
        raise RuntimeError(f"release archive digest mismatch: expected {expected}, got {actual}")
    return actual


def extract_installer(archive: Path, destination: Path) -> Path:
    with tarfile.open(archive, "r:gz") as bundle:
        root = destination.resolve()
        targets: set[Path] = set()
        members = bundle.getmembers()
        if len(members) > MAX_ARCHIVE_MEMBERS:
            raise RuntimeError(f"release archive has too many members: {len(members)}")
        extracted_bytes = sum(member.size for member in members if member.isfile())
        if extracted_bytes > MAX_ARCHIVE_EXTRACTED_BYTES:
            raise RuntimeError(f"release archive expands beyond the safety limit: {extracted_bytes} bytes")
        for member in members:
            target = (destination / member.name).resolve()
            if target != root and root not in target.parents:
                raise RuntimeError(f"release archive member escapes extraction root: {member.name}")
            if member.issym() or member.islnk():
                raise RuntimeError(f"release archive contains a link: {member.name}")
            if not (member.isfile() or member.isdir()):
                raise RuntimeError(f"release archive contains a special member: {member.name}")
            if target in targets:
                raise RuntimeError(f"release archive contains a duplicate target: {member.name}")
            targets.add(target)
        for member in members:
            target = (destination / member.name).resolve()
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                target.chmod(member.mode & 0o755)
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            source = bundle.extractfile(member)
            if source is None:
                raise RuntimeError(f"release archive file has no payload: {member.name}")
            with source, target.open("wb") as output:
                shutil.copyfileobj(source, output)
            target.chmod(member.mode & 0o755)
    matches = list(destination.glob("goflow-agent_*/bin/goflow-install"))
    if len(matches) != 1:
        raise RuntimeError(f"release archive contains {len(matches)} installer binaries")
    matches[0].chmod(matches[0].stat().st_mode | 0o500)
    return matches[0]


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def verify_startup(acceptance: Acceptance, name: str, prefix: Path, data_root: Path, workspace: Path) -> None:
    port = free_port()
    log_path = acceptance.output / f"{len(acceptance.steps) + 1:02d}-{name}.log"
    env = os.environ.copy()
    env.update(
        {
            "GOFLOW_DATA_ROOT": str(data_root),
            "GOFLOW_FILE_TOOLS_CMD": str(prefix / "bin/file_tools"),
            "GOFLOW_COMMAND_TOOLS_CMD": str(prefix / "bin/command_tools"),
            "GOFLOW_SKILL_RUNNER_CMD": str(prefix / "bin/skill_runner"),
            "GOFLOW_WEB_TOOLS_CMD": str(prefix / "bin/web_tools"),
            "GOFLOW_NETWORK_TOOLS_CMD": str(prefix / "bin/network_tools"),
            "GOFLOW_OFFICE_TOOLS_CMD": str(prefix / "bin/office_tools"),
            "GOFLOW_HOST_TOOLS_CMD": str(prefix / "bin/host_tools"),
            "GOFLOW_SSH_TOOLS_CMD": str(prefix / "bin/ssh_tools"),
            "GOFLOW_NOTIFICATION_TOOLS_CMD": str(prefix / "bin/notification_tools"),
            "GOFLOW_PYTHON_CMD": shutil.which("python3") or "python3",
            "GOFLOW_PYTHON_NOTES_PATH": str(prefix / "mcp_servers/python_notes.py"),
        }
    )
    workspace.mkdir(parents=True, exist_ok=True)
    command = [
        str(prefix / "bin/goflow"),
        "--config",
        str(prefix / "configs/goflow.binary.yaml"),
        "--workspace",
        str(workspace),
        "--http",
        f"127.0.0.1:{port}",
    ]
    started = time.monotonic()
    with log_path.open("w", encoding="utf-8") as log:
        process = subprocess.Popen(
            command,
            cwd=prefix,
            env=env,
            stdout=log,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        error = ""
        ready = False
        try:
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    error = f"server exited early with code {process.returncode}"
                    break
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/api/session", timeout=2) as response:
                        if response.status == 200:
                            ready = True
                            error = ""
                            break
                except Exception as exc:  # noqa: BLE001 - retained as acceptance evidence.
                    error = str(exc)
                time.sleep(0.25)
            else:
                error = "server did not become ready within 30 seconds"
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)
    passed = ready
    if process.returncode is not None and process.returncode not in {0, -signal.SIGTERM} and passed:
        passed = False
        error = f"server shutdown returned {process.returncode}"
    acceptance.steps.append(
        {
            "name": name,
            "passed": passed,
            "duration_ms": round((time.monotonic() - started) * 1000),
            "log": log_path.name,
            "command": command,
            "detail": error,
        }
    )
    if not passed:
        raise RuntimeError(f"{name} failed: {error}; see {log_path}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", help="new evidence directory; defaults under .artifacts")
    parser.add_argument("--release-archive", help="final Linux amd64 release archive to use as the upgrade target")
    parser.add_argument("--checksums", default="dist/SHA256SUMS", help="checksum manifest for --release-archive")
    parser.add_argument("--step-timeout", type=float, default=600.0, help="maximum seconds for each external command")
    parser.add_argument(
        "--allow-virtual-machine",
        action="store_true",
        help="accept a detected VM for functional Linux acceptance; the report records this policy and does not claim physical-host evidence",
    )
    args = parser.parse_args()
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    configured_output = args.output or os.environ.get("GOFLOW_ACCEPTANCE_OUTPUT", "")
    output = Path(configured_output).resolve() if configured_output else ROOT / ".artifacts" / "linux-native-acceptance" / stamp
    acceptance = Acceptance(
        output,
        step_timeout=max(1.0, args.step_timeout),
        allow_virtual_machine=args.allow_virtual_machine,
    )
    generated_paths = [output / "go-cache", output / "go-tmp", output / "dist-a", output / "dist-b"]
    lifecycle: Path | None = None
    passed = False
    error = ""
    try:
        require_host(acceptance, allow_virtual_machine=args.allow_virtual_machine)
        env = os.environ.copy()
        env["GOFLOW_REQUIRE_LINUX_NATIVE_SANDBOX"] = "1"
        env["GOCACHE"] = str(output / "go-cache")
        env["GOTMPDIR"] = str(output / "go-tmp")
        Path(env["GOCACHE"]).mkdir(parents=True)
        Path(env["GOTMPDIR"]).mkdir(parents=True)

        acceptance.run(
            "native-sandbox-malicious-fixture",
            ["go", "test", "./internal/nativesandbox", "-run", "^TestLinuxNativeSandboxMaliciousFixture$", "-count=1", "-v"],
            env=env,
        )
        validate_go_test_log(
            acceptance.output / str(acceptance.steps[-1]["log"]),
            ("TestLinuxNativeSandboxMaliciousFixture",),
        )
        acceptance.check("native-sandbox-test-evidence", True, "required test passed without skips")
        acceptance.run(
            "python-node-mcp-protocol-boundary",
            ["go", "test", "./internal/mcp", "-run", "^TestLinuxNativeStrong(Python|Node)MCPProtocolAndBoundary$", "-count=1", "-v"],
            env=env,
        )
        validate_go_test_log(
            acceptance.output / str(acceptance.steps[-1]["log"]),
            (
                "TestLinuxNativeStrongPythonMCPProtocolAndBoundary",
                "TestLinuxNativeStrongNodeMCPProtocolAndBoundary",
            ),
        )
        acceptance.check("python-node-mcp-test-evidence", True, "both required tests passed without skips")

        dist_a, dist_b = output / "dist-a", output / "dist-b"
        versions = ("linux-acceptance-a", "linux-acceptance-b")
        builds = [("a", versions[0], dist_a)]
        if not args.release_archive:
            builds.append(("b", versions[1], dist_b))
        for label, version, dist in builds:
            acceptance.run(
                f"build-release-{label}",
                [sys.executable, "scripts/build_release_assets.py", "--clean", "--version", version, "--dist", str(dist), "--target", "linux/amd64"],
                env=env,
            )
            acceptance.run(
                f"validate-release-{label}",
                [sys.executable, "scripts/validate_release_archives.py", "--version", version, "--dist", str(dist), "--target", "linux/amd64"],
                env=env,
            )

        archive_a = release_archive(dist_a, versions[0])
        if args.release_archive:
            archive_b = Path(args.release_archive).resolve()
            checksum_path = Path(args.checksums).resolve()
            acceptance.check("final-release-archive-present", archive_b.is_file(), str(archive_b))
            digest = validate_archive_digest(archive_b, checksum_path)
            acceptance.check("final-release-archive-sha256", True, digest)
        else:
            archive_b = release_archive(dist_b, versions[1])
        lifecycle = Path(tempfile.mkdtemp(prefix="goflow-linux-lifecycle-", dir=output))
        generated_paths.append(lifecycle)
        extracted = lifecycle / "installer"
        extracted.mkdir()
        installer = extract_installer(archive_b, extracted)
        prefix, data_root, workspace = lifecycle / "app", lifecycle / "data", lifecycle / "workspace"

        acceptance.run("install-a", [str(installer), "install", "--archive", str(archive_a), "--prefix", str(prefix), "--data-root", str(data_root)])
        keep = data_root / "keep.txt"
        keep.write_text("persistent-data\n", encoding="utf-8")
        marker = prefix / "upgrade-marker.txt"
        marker.write_text("pre-upgrade\n", encoding="utf-8")
        verify_startup(acceptance, "first-start-a", prefix, data_root, workspace)

        bad_archive = lifecycle / "invalid.tar.gz"
        with tarfile.open(bad_archive, "w:gz") as bundle:
            placeholder = lifecycle / "invalid.txt"
            placeholder.write_text("invalid release\n", encoding="utf-8")
            bundle.add(placeholder, arcname="invalid/README.txt")
        acceptance.run(
            "reject-invalid-upgrade",
            [str(installer), "upgrade", "--archive", str(bad_archive), "--prefix", str(prefix), "--data-root", str(data_root)],
            expect_success=False,
        )
        acceptance.check("failed-upgrade-keeps-installation", marker.is_file(), str(marker))

        acceptance.run("upgrade-b", [str(installer), "upgrade", "--archive", str(archive_b), "--prefix", str(prefix), "--data-root", str(data_root)])
        backup_root = prefix.parent / ".goflow-upgrade-backups"
        backups = list(backup_root.glob("*/upgrade-marker.txt"))
        acceptance.check("upgrade-backup", len(backups) == 1, f"backup markers={len(backups)}")
        acceptance.check("upgrade-preserves-data", keep.read_text(encoding="utf-8") == "persistent-data\n", str(keep))
        verify_startup(acceptance, "restart-after-upgrade-b", prefix, data_root, workspace)

        acceptance.run("rollback-to-a", [str(installer), "rollback", "--prefix", str(prefix)])
        acceptance.check("rollback-restores-application", marker.read_text(encoding="utf-8") == "pre-upgrade\n", str(marker))
        acceptance.check("rollback-preserves-data", keep.read_text(encoding="utf-8") == "persistent-data\n", str(keep))
        verify_startup(acceptance, "restart-after-rollback-a", prefix, data_root, workspace)
        acceptance.run("upgrade-b-after-rollback", [str(installer), "upgrade", "--archive", str(archive_b), "--prefix", str(prefix), "--data-root", str(data_root)])

        acceptance.run("uninstall-preserve-data", [str(installer), "uninstall", "--prefix", str(prefix)])
        acceptance.check("uninstall-data-preserved", keep.is_file(), str(keep))
        acceptance.run("reinstall-b", [str(installer), "install", "--archive", str(archive_b), "--prefix", str(prefix), "--data-root", str(data_root)])
        acceptance.run("uninstall-purge-data", [str(installer), "uninstall", "--prefix", str(prefix), "--purge-data"])
        acceptance.check("purge-removes-data", not data_root.exists(), str(data_root))
        passed = True
    except Exception as exc:  # noqa: BLE001 - report every hard-gate failure.
        error = str(exc)
    finally:
        cleanup_failures: list[str] = []
        for path in generated_paths:
            try:
                if path.is_dir():
                    shutil.rmtree(path)
                elif path.exists():
                    path.unlink()
            except OSError as exc:
                cleanup_failures.append(f"{path.name}: {exc}")
        cleanup_passed = not cleanup_failures and all(not path.exists() for path in generated_paths)
        acceptance.steps.append(
            {
                "name": "generated-artifacts-cleaned",
                "passed": cleanup_passed,
                "detail": "; ".join(cleanup_failures) if cleanup_failures else "caches, build trees, and lifecycle directory removed",
            }
        )
        if not cleanup_passed:
            passed = False
            cleanup_error = "generated acceptance artifacts were not fully cleaned"
            error = f"{error}; {cleanup_error}" if error else cleanup_error

    report = acceptance.report(passed, error)
    if not passed:
        print(f"Linux native acceptance failed: {error}\nreport: {report}", file=sys.stderr)
        return 1
    print(f"Linux native acceptance passed\nreport: {report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
