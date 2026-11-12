from __future__ import annotations

import hashlib
import tempfile
import unittest
from unittest.mock import MagicMock, patch
import zipfile
from pathlib import Path

from scripts import windows_native_acceptance
from scripts.windows_native_acceptance import (
    cleanup_generated_tree,
    detect_windows_native_machine,
    extract_installer,
    run_installer_with_sharing_retry,
    validate_archive_digest,
)


class WindowsNativeAcceptanceTests(unittest.TestCase):

    def test_startup_uses_installed_notification_binary_without_launching(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            acceptance = MagicMock(output=root, steps=[])
            with (
                patch.object(windows_native_acceptance, "free_port", return_value=1234),
                patch.object(windows_native_acceptance.subprocess, "Popen") as popen,
                patch.object(windows_native_acceptance.subprocess, "run"),
                patch.object(windows_native_acceptance.urllib.request, "urlopen") as urlopen,
                patch.object(windows_native_acceptance, "wait_for_path_release"),
                patch.dict("os.environ", {"GOFLOW_NOTIFICATION_TOOLS_CMD": "stale-command"}),
            ):
                popen.return_value.poll.return_value = None
                urlopen.return_value.__enter__.return_value.status = 200
                windows_native_acceptance.verify_startup(acceptance, "fixture", root / "installed", root / "data", root / "workspace")
                self.assertEqual(popen.call_args.kwargs["env"]["GOFLOW_NOTIFICATION_TOOLS_CMD"], str(root / "installed/bin/notification_tools.exe"))
                self.assertTrue(acceptance.steps[0]["passed"])

    def test_installer_sharing_retry_is_bounded(self) -> None:
        calls = []

        class StubAcceptance:
            def run(self, name, command):
                calls.append((name, command))
                if len(calls) < 3:
                    raise RuntimeError("sharing violation")

        with patch("scripts.windows_native_acceptance.time.sleep"):
            run_installer_with_sharing_retry(
                StubAcceptance(), "uninstall", ["installer", "uninstall"], attempts=3
            )
        self.assertEqual(len(calls), 3)
    def test_detect_native_machine_falls_back_to_windows_system_info(self) -> None:
        self.assertEqual(detect_windows_native_machine("", {}, 9), "amd64")
        self.assertEqual(detect_windows_native_machine("", {}, 12), "arm64")

    def test_detect_native_machine_prefers_process_environment(self) -> None:
        self.assertEqual(
            detect_windows_native_machine("", {"PROCESSOR_ARCHITEW6432": "AMD64"}, 12),
            "amd64",
        )

    def test_extract_installer_returns_release_binary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.zip"
            with zipfile.ZipFile(archive, "w") as bundle:
                bundle.writestr("goflow-agent_test_windows_amd64/bin/goflow-install.exe", b"installer")
            installer = extract_installer(archive, root / "out")
            self.assertEqual(installer.read_bytes(), b"installer")

    def test_extract_installer_rejects_path_traversal(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.zip"
            with zipfile.ZipFile(archive, "w") as bundle:
                bundle.writestr("../../outside.txt", b"outside")
            with self.assertRaisesRegex(RuntimeError, "escapes extraction root"):
                extract_installer(archive, root / "out")

    def test_archive_digest_is_recomputed(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "release.zip"
            archive.write_bytes(b"release")
            checksum = root / "SHA256SUMS"
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            checksum.write_text(f"{digest}  {archive.name}\n", encoding="utf-8")
            self.assertEqual(validate_archive_digest(archive, checksum), digest)
            archive.write_bytes(b"tampered")
            with self.assertRaisesRegex(RuntimeError, "digest mismatch"):
                validate_archive_digest(archive, checksum)

    def test_cleanup_generated_tree_removes_readonly_files(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            lifecycle = Path(temporary) / "lifecycle"
            nested = lifecycle / "module-cache"
            nested.mkdir(parents=True)
            readonly = nested / "module.go"
            readonly.write_text("package module\n", encoding="utf-8")
            readonly.chmod(0o400)
            passed, detail = cleanup_generated_tree(lifecycle)
            self.assertTrue(passed, detail)
            self.assertFalse(lifecycle.exists())


if __name__ == "__main__":
    unittest.main()
