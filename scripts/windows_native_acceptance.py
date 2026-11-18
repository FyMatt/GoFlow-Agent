#!/usr/bin/env python3
"""Run the Windows x64 release archive installation lifecycle acceptance."""

from __future__ import annotations

import argparse
import ctypes
import hashlib
import json
import os
import platform
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request
import zipfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]


def _normalize_windows_machine(value: str | None) -> str:
    machine = (value or "").strip().lower()
    return {
        "amd64": "amd64",
        "x86_64": "amd64",
        "x64": "amd64",
        "arm64": "arm64",
        "aarch64": "arm64",
        "x86": "x86",
        "i386": "x86",
        "i686": "x86",
    }.get(machine, machine)


def _windows_system_info_architecture() -> int | None:
    class SystemInfo(ctypes.Structure):
        _fields_ = [
            ("wProcessorArchitecture", ctypes.c_ushort),
            ("wReserved", ctypes.c_ushort),
            ("dwPageSize", ctypes.c_ulong),
            ("lpMinimumApplicationAddress", ctypes.c_void_p),
            ("lpMaximumApplicationAddress", ctypes.c_void_p),
            ("dwActiveProcessorMask", ctypes.c_size_t),
            ("dwNumberOfProcessors", ctypes.c_ulong),
            ("dwProcessorType", ctypes.c_ulong),
            ("dwAllocationGranularity", ctypes.c_ulong),
            ("wProcessorLevel", ctypes.c_ushort),
            ("wProcessorRevision", ctypes.c_ushort),
        ]

    try:
        system_info = SystemInfo()
        ctypes.windll.kernel32.GetNativeSystemInfo(ctypes.byref(system_info))
        return int(system_info.wProcessorArchitecture)
    except (AttributeError, OSError):
        return None


def detect_windows_native_machine(
    machine: str | None = None,
    environ: dict[str, str] | os._Environ[str] | None = None,
    system_info_architecture: int | None = None,
) -> str:
    environment = os.environ if environ is None else environ
    for candidate in (
        platform.machine() if machine is None else machine,
        environment.get("PROCESSOR_ARCHITEW6432"),
        environment.get("PROCESSOR_ARCHITECTURE"),
    ):
        normalized = _normalize_windows_machine(candidate)
        if normalized:
            return normalized

    architecture = system_info_architecture
    if architecture is None and platform.system() == "Windows":
        architecture = _windows_system_info_architecture()
    return {9: "amd64", 12: "arm64", 0: "x86"}.get(architecture, "")


class Acceptance:
    def __init__(self, output: Path) -> None:
        self.output = output
        self.output.mkdir(parents=True, exist_ok=False)
        self.steps: list[dict[str, Any]] = []
        self.started = time.monotonic()

    def run(self, name: str, command: list[str], *, env: dict[str, str] | None = None) -> None:
        log_path = self.output / f"{len(self.steps) + 1:02d}-{name}.log"
        started = time.monotonic()
        print(f"[{name}] {' '.join(command)}", flush=True)
        with log_path.open("w", encoding="utf-8") as log:
            completed = subprocess.run(
                command,
                cwd=ROOT,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                text=True,
                check=False,
            )
        self.steps.append(
            {
                "name": name,
                "passed": completed.returncode == 0,
                "exit_code": completed.returncode,
                "duration_ms": round((time.monotonic() - started) * 1000),
                "log": log_path.name,
                "command": command,
            }
        )
        if completed.returncode != 0:
            raise RuntimeError(f"{name} failed with exit code {completed.returncode}; see {log_path}")

    def check(self, name: str, condition: bool, detail: str) -> None:
        self.steps.append({"name": name, "passed": condition, "detail": detail})
        if not condition:
            raise RuntimeError(f"{name} failed: {detail}")

    def report(self, passed: bool, error: str = "") -> Path:
        report_path = self.output / "report.json"
        report_path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "passed": passed,
                    "error": error,
                    "created_at": datetime.now(timezone.utc).isoformat(),
                    "duration_ms": round((time.monotonic() - self.started) * 1000),
                    "host": {
                        "system": platform.system(),
                        "machine": platform.machine(),
                        "native_machine": detect_windows_native_machine() if platform.system() == "Windows" else "",
                        "release": platform.release(),
                        "python": platform.python_version(),
                    },
                    "steps": self.steps,
                },
                ensure_ascii=False,
                indent=2,
            )
            + "\n",
            encoding="utf-8",
        )
        return report_path


def require_host() -> None:
    if platform.system() != "Windows" or detect_windows_native_machine() != "amd64":
        raise RuntimeError("acceptance requires a native Windows x64 host")


def parse_checksums(path: Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        fields = line.split()
        if len(fields) == 2:
            checksums[fields[1].lstrip("*")] = fields[0].lower()
    return checksums


def validate_archive_digest(archive: Path, checksum_path: Path) -> str:
    expected = parse_checksums(checksum_path).get(archive.name)
    if not expected:
        raise RuntimeError(f"SHA256SUMS has no entry for {archive.name}")
    actual = hashlib.sha256(archive.read_bytes()).hexdigest()
    if actual != expected:
        raise RuntimeError(f"release archive digest mismatch: expected {expected}, got {actual}")
    return actual


def extract_installer(archive: Path, destination: Path) -> Path:
    root = destination.resolve()
    with zipfile.ZipFile(archive) as bundle:
        for member in bundle.infolist():
            target = (destination / member.filename).resolve()
            if target != root and root not in target.parents:
                raise RuntimeError(f"release archive member escapes extraction root: {member.filename}")
            if member.is_dir():
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            with bundle.open(member) as source, target.open("wb") as output:
                shutil.copyfileobj(source, output)
    matches = list(destination.glob("goflow-agent_*/bin/goflow-install.exe"))
    if len(matches) != 1:
        raise RuntimeError(f"release archive contains {len(matches)} installer binaries")
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
            "GOFLOW_FILE_TOOLS_CMD": str(prefix / "bin/file_tools.exe"),
            "GOFLOW_COMMAND_TOOLS_CMD": str(prefix / "bin/command_tools.exe"),
            "GOFLOW_SKILL_RUNNER_CMD": str(prefix / "bin/skill_runner.exe"),
            "GOFLOW_WEB_TOOLS_CMD": str(prefix / "bin/web_tools.exe"),
            "GOFLOW_NETWORK_TOOLS_CMD": str(prefix / "bin/network_tools.exe"),
            "GOFLOW_OFFICE_TOOLS_CMD": str(prefix / "bin/office_tools.exe"),
            "GOFLOW_HOST_TOOLS_CMD": str(prefix / "bin/host_tools.exe"),
            "GOFLOW_SSH_TOOLS_CMD": str(prefix / "bin/ssh_tools.exe"),
            "GOFLOW_NOTIFICATION_TOOLS_CMD": str(prefix / "bin/notification_tools.exe"),
            "GOFLOW_PYTHON_CMD": shutil.which("python") or "python",
            "GOFLOW_PYTHON_NOTES_PATH": str(prefix / "mcp_servers/python_notes.py"),
        }
    )
    workspace.mkdir(parents=True, exist_ok=True)
    command = [
        str(prefix / "bin/goflow.exe"),
        "--config",
        str(prefix / "configs/goflow.binary.yaml"),
        "--workspace",
        str(workspace),
        "--http",
        f"127.0.0.1:{port}",
    ]
    started = time.monotonic()
    error = ""
    ready = False
    with log_path.open("w", encoding="utf-8") as log:
        process = subprocess.Popen(command, cwd=prefix, env=env, stdout=log, stderr=subprocess.STDOUT, text=True)
        try:
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    error = f"server exited early with code {process.returncode}"
                    break
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/api/session", timeout=2) as response:
                        ready = response.status == 200
                    if ready:
                        break
                except Exception as exc:  # noqa: BLE001 - evidence captures the last startup error.
                    error = str(exc)
                time.sleep(0.1)
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            if os.name == "nt":
                subprocess.run(
                    ["taskkill.exe", "/PID", str(process.pid), "/T", "/F"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    check=False,
                    timeout=15,
                )
            wait_for_path_release(prefix / "bin/goflow.exe")
    acceptance.steps.append(
        {
            "name": name,
            "passed": ready,
            "duration_ms": round((time.monotonic() - started) * 1000),
            "log": log_path.name,
            "command": command,
            "detail": "" if ready else error or "server did not become ready",
        }
    )
    if not ready:
        raise RuntimeError(f"{name} failed: {error}; see {log_path}")


def wait_for_path_release(path: Path, attempts: int = 20) -> None:
    """Wait until Windows allows an installed binary to be opened exclusively."""

    if os.name != "nt" or not path.exists():
        return
    for attempt in range(max(1, attempts)):
        try:
            handle = os.open(path, os.O_RDWR)
            os.close(handle)
            return
        except OSError:
            if attempt + 1 < max(1, attempts):
                time.sleep(0.1 * (attempt + 1))


def run_installer_with_sharing_retry(
    acceptance: Acceptance,
    name: str,
    command: list[str],
    *,
    attempts: int = 4,
) -> None:
    for attempt in range(1, max(1, attempts) + 1):
        try:
            acceptance.run(name, command)
            return
        except RuntimeError:
            if attempt >= max(1, attempts):
                raise
            time.sleep(0.3 * attempt)


def create_invalid_archive(path: Path) -> None:
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as bundle:
        bundle.writestr("invalid/README.txt", "invalid release\n")


def cleanup_generated_tree(path: Path | None, attempts: int = 5) -> tuple[bool, str]:
    if path is None or not path.exists():
        return True, "generated lifecycle directory is absent"
    error = ""
    for attempt in range(max(1, attempts)):
        try:
            shutil.rmtree(path)
            return not path.exists(), "generated lifecycle directory removed"
        except OSError as exc:
            error = str(exc)
            for item in [path, *path.rglob("*")]:
                try:
                    item.chmod(0o700)
                except OSError:
                    pass
            if attempt + 1 < max(1, attempts):
                time.sleep(0.2 * (attempt + 1))
    return False, error or "generated lifecycle directory still exists"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", default="dist/goflow-agent_dev_windows_amd64.zip")
    parser.add_argument("--checksums", default="dist/SHA256SUMS")
    parser.add_argument("--output", help="new evidence directory; defaults under .artifacts")
    args = parser.parse_args()
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    configured_output = args.output or os.environ.get("GOFLOW_ACCEPTANCE_OUTPUT", "")
    output = Path(configured_output).resolve() if configured_output else ROOT / ".artifacts" / "windows-native-acceptance" / stamp
    acceptance = Acceptance(output)
    lifecycle: Path | None = None
    passed = False
    error = ""
    try:
        require_host()
        archive = (ROOT / args.archive).resolve()
        checksum_path = (ROOT / args.checksums).resolve()
        acceptance.check("release-archive-present", archive.is_file(), str(archive))
        digest = validate_archive_digest(archive, checksum_path)
        acceptance.check("release-archive-sha256", True, digest)

        lifecycle = Path(tempfile.mkdtemp(prefix="goflow-windows-lifecycle-", dir=output))
        extracted = lifecycle / "installer"
        extracted.mkdir()
        installer = extract_installer(archive, extracted)
        acceptance.check("installer-extracted-from-release", installer.is_file(), str(installer))
        prefix, data_root, workspace = lifecycle / "app", lifecycle / "data", lifecycle / "workspace"

        acceptance.run("install", [str(installer), "install", "--archive", str(archive), "--prefix", str(prefix), "--data-root", str(data_root)])
        keep = data_root / "keep.txt"
        keep.write_text("persistent-data\n", encoding="utf-8")
        marker = prefix / "upgrade-marker.txt"
        marker.write_text("pre-upgrade\n", encoding="utf-8")
        verify_startup(acceptance, "first-start-without-key", prefix, data_root, workspace)

        bad_archive = lifecycle / "invalid.zip"
        create_invalid_archive(bad_archive)
        failed = subprocess.run(
            [str(installer), "upgrade", "--archive", str(bad_archive), "--prefix", str(prefix), "--data-root", str(data_root)],
            cwd=ROOT,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        acceptance.check("failed-upgrade-keeps-installation", failed.returncode != 0 and marker.is_file(), f"exit={failed.returncode}")

        acceptance.run("upgrade", [str(installer), "upgrade", "--archive", str(archive), "--prefix", str(prefix), "--data-root", str(data_root)])
        backups = list((prefix.parent / ".goflow-upgrade-backups").glob("*/upgrade-marker.txt"))
        acceptance.check("upgrade-backup", len(backups) == 1, f"backup markers={len(backups)}")
        acceptance.check("upgrade-preserves-data", keep.read_text(encoding="utf-8") == "persistent-data\n", str(keep))
        verify_startup(acceptance, "restart-after-upgrade", prefix, data_root, workspace)

        acceptance.run("rollback", [str(installer), "rollback", "--prefix", str(prefix)])
        acceptance.check("rollback-restores-application", marker.read_text(encoding="utf-8") == "pre-upgrade\n", str(marker))
        acceptance.check("rollback-preserves-data", keep.read_text(encoding="utf-8") == "persistent-data\n", str(keep))
        verify_startup(acceptance, "restart-after-rollback", prefix, data_root, workspace)
        acceptance.run("upgrade-after-rollback", [str(installer), "upgrade", "--archive", str(archive), "--prefix", str(prefix), "--data-root", str(data_root)])

        acceptance.run("uninstall-preserve-data", [str(installer), "uninstall", "--prefix", str(prefix)])
        acceptance.check("uninstall-data-preserved", keep.is_file(), str(keep))
        acceptance.run("reinstall", [str(installer), "install", "--archive", str(archive), "--prefix", str(prefix), "--data-root", str(data_root)])
        run_installer_with_sharing_retry(
            acceptance,
            "uninstall-purge-data",
            [str(installer), "uninstall", "--prefix", str(prefix), "--purge-data"],
        )
        acceptance.check("purge-removes-data", not data_root.exists(), str(data_root))
        passed = True
    except Exception as exc:  # noqa: BLE001 - every hard-gate failure must become report evidence.
        error = str(exc)
    finally:
        cleanup_passed, cleanup_detail = cleanup_generated_tree(lifecycle)
        acceptance.steps.append(
            {
                "name": "generated-artifacts-cleaned",
                "passed": cleanup_passed,
                "detail": cleanup_detail,
            }
        )
        if not cleanup_passed:
            passed = False
            cleanup_error = "generated acceptance artifacts were not fully cleaned"
            error = f"{error}; {cleanup_error}" if error else cleanup_error

    report = acceptance.report(passed, error)
    if not passed:
        print(f"Windows native acceptance failed: {error}\nreport: {report}", file=os.sys.stderr)
        return 1
    print(f"Windows native acceptance passed\nreport: {report}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
