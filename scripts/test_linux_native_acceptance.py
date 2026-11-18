from __future__ import annotations

import hashlib
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from scripts.linux_native_acceptance import Acceptance, MAX_ARCHIVE_EXTRACTED_BYTES, cpu_has_hypervisor_flag, detect_virtual_machine, extract_installer, kernel_version, validate_archive_digest, validate_go_test_log


class LinuxNativeAcceptanceTests(unittest.TestCase):
    def test_archive_digest_is_recomputed(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            archive.write_bytes(b"release")
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            checksums = root / "SHA256SUMS"
            checksums.write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
            self.assertEqual(validate_archive_digest(archive, checksums), digest)
            archive.write_bytes(b"tampered")
            with self.assertRaisesRegex(RuntimeError, "digest mismatch"):
                validate_archive_digest(archive, checksums)

    def test_extract_installer_returns_release_binary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            self._write_archive(
                archive,
                [("goflow-agent_test_linux_amd64/bin/goflow-install", b"installer")],
            )
            installer = extract_installer(archive, root / "out")
            self.assertEqual(installer.read_bytes(), b"installer")
            self.assertNotEqual(installer.stat().st_mode & 0o500, 0)

    def test_extract_installer_rejects_path_traversal(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            self._write_archive(archive, [("../../outside.txt", b"escape")])
            with self.assertRaisesRegex(RuntimeError, "escapes extraction root"):
                extract_installer(archive, root / "out")

    def test_extract_installer_rejects_links(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                member = tarfile.TarInfo("goflow-agent_test_linux_amd64/bin/goflow-install")
                member.type = tarfile.SYMTYPE
                member.linkname = "/bin/sh"
                bundle.addfile(member)
            with self.assertRaisesRegex(RuntimeError, "contains a link"):
                extract_installer(archive, root / "out")

    def test_extract_installer_rejects_special_members(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                member = tarfile.TarInfo("goflow-agent_test_linux_amd64/bin/pipe")
                member.type = tarfile.FIFOTYPE
                bundle.addfile(member)
            with self.assertRaisesRegex(RuntimeError, "special member"):
                extract_installer(archive, root / "out")

    def test_extract_installer_rejects_duplicate_targets(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            self._write_archive(
                archive,
                [
                    ("goflow-agent_test_linux_amd64/bin/goflow-install", b"one"),
                    ("goflow-agent_test_linux_amd64/bin/goflow-install", b"two"),
                ],
            )
            with self.assertRaisesRegex(RuntimeError, "duplicate target"):
                extract_installer(archive, root / "out")

    def test_extract_installer_rejects_oversized_expansion(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.tar.gz"
            member = tarfile.TarInfo("goflow-agent_test_linux_amd64/bin/goflow-install")
            member.size = MAX_ARCHIVE_EXTRACTED_BYTES + 1
            bundle = MagicMock()
            bundle.getmembers.return_value = [member]
            context = MagicMock()
            context.__enter__.return_value = bundle
            with patch("scripts.linux_native_acceptance.tarfile.open", return_value=context):
                with self.assertRaisesRegex(RuntimeError, "safety limit"):
                    extract_installer(archive, root / "out")

    def test_kernel_version_parsing_is_numeric(self) -> None:
        self.assertEqual(kernel_version("5.15.0-100-generic"), (5, 15))
        self.assertGreater(kernel_version("6.1.0"), (5, 15))
        self.assertEqual(kernel_version("invalid"), (0, 0))

    def test_virtual_machine_detection_rejects_hypervisor_markers(self) -> None:
        def read_text(path: Path) -> str:
            if path.as_posix() == "/sys/hypervisor/type":
                return "kvm"
            raise OSError("not present")

        self.assertIn("hypervisor", detect_virtual_machine(detector_path="", read_text=read_text))

    def test_virtual_machine_detection_rejects_cpu_hypervisor_flag(self) -> None:
        def read_text(path: Path) -> str:
            if path.as_posix() == "/proc/cpuinfo":
                return "processor : 0\nflags : fpu hypervisor vmx\n"
            raise OSError("not present")

        self.assertIn("cpuinfo", detect_virtual_machine(detector_path="", read_text=read_text))

    def test_cpu_hypervisor_report_helper_returns_boolean(self) -> None:
        self.assertIsInstance(cpu_has_hypervisor_flag(), bool)

    def test_report_preserves_real_start_and_finish_times(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            acceptance = Acceptance(Path(temporary) / "evidence")
            report = acceptance.report(False, "fixture")
            payload = json.loads(report.read_text(encoding="utf-8"))
            self.assertIn("started_at", payload)
            self.assertIn("finished_at", payload)
            self.assertLessEqual(payload["started_at"], payload["finished_at"])

    def test_go_test_log_requires_each_named_pass(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "go-test.log"
            log.write_text(
                "=== RUN   TestOne\n--- PASS: TestOne (0.01s)\n"
                "=== RUN   TestTwo\n--- PASS: TestTwo (0.02s)\nPASS\n",
                encoding="utf-8",
            )
            validate_go_test_log(log, ("TestOne", "TestTwo"))
            with self.assertRaisesRegex(RuntimeError, "TestThree"):
                validate_go_test_log(log, ("TestOne", "TestThree"))

    def test_go_test_log_rejects_any_skip(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            log = Path(temporary) / "go-test.log"
            log.write_text("=== RUN   TestOne\n--- SKIP: TestOne (0.00s)\nPASS\n", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "skipped"):
                validate_go_test_log(log, ("TestOne",))

    @staticmethod
    def _write_archive(archive: Path, members: list[tuple[str, bytes]]) -> None:
        with tarfile.open(archive, "w:gz") as bundle:
            for name, content in members:
                member = tarfile.TarInfo(name)
                member.size = len(content)
                member.mode = 0o755
                bundle.addfile(member, io.BytesIO(content))


if __name__ == "__main__":
    unittest.main()
