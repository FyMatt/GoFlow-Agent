from __future__ import annotations

import hashlib
import os
import stat
import struct
import tempfile
import unittest
from pathlib import Path

from scripts.real_vision_acceptance import SCREENSHOT_LINES, TASK_PROMPT, acceptance_workspace_data_root, check_formal_inputs, install_provider_override, png_dimensions, redact_secret_value, remove_file_with_retry, remove_tree_with_retry, render_fixture_png, sanitize_evidence_files, validate_formal_input, validate_formal_vision_inputs, validate_provider_module_contract, vision_provider_probe_gates


class RealVisionAcceptanceTests(unittest.TestCase):
    def test_fixture_is_deterministic_nonempty_png(self) -> None:
        first = render_fixture_png()
        second = render_fixture_png()
        self.assertEqual(hashlib.sha256(first).digest(), hashlib.sha256(second).digest())
        width, height = png_dimensions(first)
        self.assertGreaterEqual(width, 900)
        self.assertGreaterEqual(height, 250)
        self.assertEqual(struct.unpack(">I", first[8:12])[0], 13)
        self.assertGreater(len(first), 1000)

    def test_implementation_hash_detects_real_content_change(self) -> None:
        before = hashlib.sha256(b"return requested\n").hexdigest()
        after = hashlib.sha256(b"if valid { return requested }\n").hexdigest()
        self.assertNotEqual(before, after)

    def test_task_prompt_does_not_leak_image_only_contract(self) -> None:
        prompt = TASK_PROMPT.casefold()
        for secret_term in ("effectiveport", "65535", "8080", "portcfg", "fallback"):
            self.assertNotIn(secret_term, prompt)
        self.assertTrue(any("8080" in line for line in SCREENSHOT_LINES))

    def test_renderer_rejects_unmapped_characters(self) -> None:
        with self.assertRaisesRegex(ValueError, "unsupported glyphs"):
            render_fixture_png(("NOT_SUPPORTED!",))

    def test_workspace_cleanup_reports_real_result(self) -> None:
        root = Path(tempfile.mkdtemp())
        (root / "nested").mkdir()
        (root / "nested" / "file.txt").write_text("fixture", encoding="utf-8")
        self.assertTrue(remove_tree_with_retry(root))
        self.assertFalse(root.exists())

    def test_workspace_cleanup_removes_read_only_git_objects(self) -> None:
        root = Path(tempfile.mkdtemp())
        git_object = root / ".git" / "objects" / "ab" / "fixture"
        git_object.parent.mkdir(parents=True)
        git_object.write_bytes(b"fixture")
        os.chmod(git_object, stat.S_IREAD)
        self.assertTrue(remove_tree_with_retry(root))
        self.assertFalse(root.exists())

    def test_generated_file_cleanup_reports_real_result(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "binary"
            path.write_bytes(b"fixture")
            self.assertTrue(remove_file_with_retry(path))
            self.assertFalse(path.exists())

    def test_cleanup_helpers_report_failure_for_missing_or_unremovable_state(self) -> None:
        missing = Path(tempfile.mkdtemp()) / "missing"
        self.assertTrue(remove_tree_with_retry(missing))
        self.assertTrue(remove_file_with_retry(missing))

    def test_secret_redaction_covers_report_and_retained_evidence(self) -> None:
        secret = "fixture-secret-value-123456"
        report, count = redact_secret_value({"error": f"provider returned {secret}", "nested": [secret]}, secret)
        self.assertEqual(count, 2)
        self.assertNotIn(secret, str(report))
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "server.log").write_text(f"request failed: {secret}\n", encoding="utf-8")
            (root / "vision.png").write_bytes(b"png-fixture")
            result = sanitize_evidence_files(root, secret)
            self.assertTrue(result["scan_performed"])
            self.assertTrue(result["scan_complete"])
            self.assertEqual(result["scan_errors"], [])
            self.assertEqual(result["files_scanned"], 2)
            self.assertEqual(result["leaks_detected"], 1)
            self.assertEqual(result["redacted_files"], ["server.log"])
            self.assertNotIn(secret, (root / "server.log").read_text(encoding="utf-8"))

    def test_secret_scan_without_loaded_secret_is_complete(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = sanitize_evidence_files(Path(temporary), "")
        self.assertFalse(result["scan_performed"])
        self.assertTrue(result["scan_complete"])
        self.assertEqual(result["scan_errors"], [])
        self.assertEqual(result["leaks_detected"], 0)

    def test_provider_override_is_installed_in_user_resource_layer(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "outside" / "vision.yaml"
            source.parent.mkdir()
            content = (
                "primary:\n"
                "  provider: openai-compatible\n"
                "  base_url: https://provider.invalid/v1\n"
                "  api_key: secret://providers/primary/api_key\n"
                "  model: fixture-vision\n"
                "  vision: true\n"
            )
            source.write_text(content, encoding="utf-8")
            target = install_provider_override(root / "data", str(source))
            self.assertEqual(target, root / "data" / "resources" / "providers" / "primary.yaml")
            self.assertEqual(target.read_text(encoding="utf-8"), content)

    def test_provider_override_rejects_missing_file(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaisesRegex(RuntimeError, "missing or empty"):
                install_provider_override(Path(temporary) / "data", str(Path(temporary) / "missing.yaml"))

    def test_provider_module_rejects_plaintext_or_duplicate_keys(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "isolated primary secret"):
            validate_provider_module_contract(
                "primary:\n"
                "  provider: openai-compatible\n"
                "  base_url: https://provider.invalid/v1\n"
                "  model: fixture-vision\n"
                "  vision: true\n"
                "  api_key: plaintext\n"
            )
        with self.assertRaisesRegex(RuntimeError, "isolated primary secret"):
            validate_provider_module_contract(
                "primary:\n"
                "  provider: openai-compatible\n"
                "  base_url: https://provider.invalid/v1\n"
                "  model: fixture-vision\n"
                "  vision: true\n"
                "  api_key: secret://providers/primary/api_key\n"
                "  api_key: second-value\n"
            )

    def test_provider_module_requires_complete_supported_connection(self) -> None:
        valid = (
            "primary:\n"
            "  provider: anthropic\n"
            "  base_url: https://api.example.invalid/v1\n"
            "  api_key: secret://providers/primary/api_key\n"
            "  model: fixture-vision\n"
            "  vision: true\n"
        )
        metadata = validate_provider_module_contract(valid)
        self.assertEqual(metadata["provider"], "anthropic")
        self.assertEqual(metadata["model"], "fixture-vision")

        invalid_modules = {
            "provider": valid.replace("provider: anthropic", "provider: unknown"),
            "base_url": valid.replace("https://api.example.invalid/v1", "file:///tmp/provider"),
            "model": valid.replace("  model: fixture-vision\n", ""),
            "vision": valid.replace("  vision: true\n", "  vision: false\n"),
            "exactly one nonempty model": valid.replace("  model: fixture-vision\n", "  model: fixture-vision\n  model: duplicate\n"),
            "exactly one top-level primary": valid + "primary:\n  provider: anthropic\n",
        }
        for expected, module in invalid_modules.items():
            with self.subTest(expected=expected):
                with self.assertRaisesRegex(RuntimeError, expected):
                    validate_provider_module_contract(module)

    def test_formal_input_must_be_external_regular_and_read_only(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "input.yaml"
            path.write_text("fixture", encoding="utf-8")
            os.chmod(path, stat.S_IREAD)
            self.assertEqual(validate_formal_input(path, "fixture"), path.resolve())

    def test_formal_input_rejects_repository_file(self) -> None:
        with self.assertRaisesRegex(RuntimeError, "outside the repository"):
            validate_formal_input(Path(__file__), "fixture")

    def test_formal_input_bundle_preflight_is_sanitized_and_offline(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            secret = root / "vision.key"
            module = root / "vision.yaml"
            secret.write_text("fixture-secret-value-123456\n", encoding="utf-8")
            module.write_text(
                "primary:\n"
                "  provider: openai-compatible\n"
                "  base_url: https://provider.invalid/v1\n"
                "  api_key: secret://providers/primary/api_key\n"
                "  model: fixture-vision\n"
                "  vision: true\n",
                encoding="utf-8",
            )
            os.chmod(secret, stat.S_IREAD)
            os.chmod(module, stat.S_IREAD)
            env = {
                "GOFLOW_VISION_SECRET_FILE": str(secret),
                "GOFLOW_VISION_PROVIDER_MODULE": str(module),
            }
            secret_path, module_path, secret_value = validate_formal_vision_inputs(env)
            self.assertEqual(secret_path, secret.resolve())
            self.assertEqual(module_path, module.resolve())
            self.assertEqual(secret_value, "fixture-secret-value-123456")
            result = check_formal_inputs(env)
            self.assertTrue(result["ready"])
            self.assertFalse(result["network_request_performed"])
            self.assertNotIn(str(secret), str(result))
            self.assertNotIn(secret_value, str(result))

    def test_formal_input_bundle_preflight_reports_missing_configuration(self) -> None:
        result = check_formal_inputs({})
        self.assertFalse(result["ready"])
        self.assertFalse(result["network_request_performed"])
        self.assertIn("external secret", result["reason"])

    def test_provider_probe_gates_require_real_vision_and_tool_calling(self) -> None:
        complete = vision_provider_probe_gates(
            {
                "configured": True,
                "valid": True,
                "capabilities": {"vision": True, "tool_calling": True},
            }
        )
        self.assertTrue(all(complete.values()))
        missing_vision = vision_provider_probe_gates(
            {
                "configured": True,
                "valid": True,
                "capabilities": {"vision": False, "tool_calling": True},
            }
        )
        self.assertFalse(missing_vision["provider_vision"])
        self.assertTrue(missing_vision["provider_tool_calling"])

    def test_acceptance_workspace_data_root_matches_workspace_hash_layout(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            workspace = root / "workspace"
            expected_key = hashlib.sha256(os.path.normpath(str(workspace)).encode("utf-8")).hexdigest()[:16]
            self.assertEqual(acceptance_workspace_data_root(root / "data", workspace), root / "data" / "workspaces" / expected_key)

    @unittest.skipIf(os.name == "nt", "POSIX permission bits are not authoritative on Windows")
    def test_formal_secret_rejects_group_read_access(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "secret"
            path.write_text("fixture", encoding="utf-8")
            os.chmod(path, 0o440)
            with self.assertRaisesRegex(RuntimeError, "group or other"):
                validate_formal_input(path, "fixture", private=True)


if __name__ == "__main__":
    unittest.main()
