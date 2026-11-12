from __future__ import annotations

import sys
import os
import hashlib
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from p7_complex_cases import cases, install_hidden
from p7_provider_runner import variant_index


class P7ComplexCasesTests(unittest.TestCase):
    def test_case_catalog_has_six_unique_ready_domains(self) -> None:
        catalog = cases()
        self.assertEqual(len(catalog), 6)
        self.assertEqual(len({case.id for case in catalog}), 6)
        self.assertEqual(
            {case.domain for case in catalog},
            {"development", "security", "operations", "research", "documents", "cross-domain"},
        )
        self.assertFalse([case.id for case in catalog if not case.ready])

    def test_development_prompt_requires_simplified_chinese_without_leaking_hidden_checks(self) -> None:
        case = next(item for item in cases() if item.domain == "development")
        self.assertIn("简体中文", case.prompt)
        self.assertIn("ready", case.prompt)
        self.assertNotIn("p7_hidden", case.prompt)
        self.assertNotIn("就绪", case.prompt)
        for relative in case.hidden_files:
            self.assertNotIn(relative, case.prompt)

    def test_security_hidden_boundary_test_cannot_silently_skip_windows_links(self) -> None:
        case = next(item for item in cases() if item.domain == "security")
        hidden = case.hidden_files["internal/filesafe/p7_hidden_test.go"]
        self.assertIn('mklink","/J"', hidden)
        self.assertIn("missing-secret", hidden)
        self.assertIn("cannot create Windows junction", hidden)
        self.assertNotIn("if os.Symlink(outside,link)==nil", hidden)

    def test_ready_code_cases_use_real_modules_and_post_run_hidden_tests(self) -> None:
        for case in (case for case in cases() if case.ready):
            with self.subTest(case=case.id):
                with tempfile.TemporaryDirectory() as raw:
                    root = Path(raw)
                    case.builder(root)
                    files_before = [path for path in root.rglob("*") if path.is_file()]
                    self.assertTrue(case.required_paths, "complexity must be structural, not file-count based")
                    self.assertGreaterEqual(len(case.variant_mutators), 2, "each case needs independent input variants")
                    for relative in case.required_paths:
                        self.assertTrue((root / relative).is_file(), relative)
                    self.assertGreaterEqual(len(files_before), len(case.required_paths))
                    if case.kind == "go":
                        self.assertTrue((root / "go.mod").is_file())
                    top_level = {path.relative_to(root).parts[0] for path in files_before}
                    if case.kind == "go":
                        self.assertGreaterEqual(len(top_level), 4)
                    elif case.kind == "research":
                        self.assertIn("sources", top_level)
                    elif case.kind == "documents":
                        self.assertIn("templates", top_level)
                        self.assertIn("inputs", top_level)
                        for relative in (
                            "templates/security-report-template.docx",
                            "templates/remediation-data-template.xlsx",
                            "templates/review-template.pptx",
                            "fixture-manifest.json",
                        ):
                            self.assertTrue((root / relative).is_file(), relative)
                    for relative in case.hidden_files:
                        self.assertFalse((root / relative).exists())
                    install_hidden(case, root)
                    for relative in case.hidden_files:
                        self.assertTrue((root / relative).is_file())
                    if case.kind == "go":
                        self.assertTrue(case.verify_commands)
                    self.assertTrue(case.required_deliverables)
                    self.assertGreaterEqual(len(case.complexity_signals), 3)

    def test_ready_case_baselines_compile_and_hidden_behavior_gates_fail(self) -> None:
        for case in (case for case in cases() if case.ready):
            if case.kind != "go":
                continue
            with self.subTest(case=case.id):
                with tempfile.TemporaryDirectory() as raw:
                    root = Path(raw)
                    case.builder(root)
                    env = {**os.environ, "GOCACHE": str(root / ".cache")}
                    compile_only = subprocess.run(
                        ["go", "test", "-count=0", "./..."], cwd=root, env=env, text=True,
                        encoding="utf-8", errors="replace", stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT, timeout=180, check=False,
                    )
                    self.assertEqual(compile_only.returncode, 0, compile_only.stdout)
                    baseline = subprocess.run(
                        ["go", "test", "./..."], cwd=root, env=env, text=True,
                        encoding="utf-8", errors="replace", stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT, timeout=180, check=False,
                    )
                    if case.domain in {"development", "security", "operations", "cross-domain"}:
                        self.assertNotEqual(
                            baseline.returncode,
                            0,
                            f"unfixed {case.domain} baseline must fail public behavior tests",
                        )
                    else:
                        self.assertEqual(baseline.returncode, 0, baseline.stdout)
                    install_hidden(case, root)
                    hidden = subprocess.run(
                        ["go", "test", "./...", "-run", "^TestP7", "-v"], cwd=root,
                        env=env, text=True, encoding="utf-8", errors="replace",
                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=180, check=False,
                    )
                    self.assertNotEqual(hidden.returncode, 0, "hidden gate unexpectedly passed on stub baseline")
                    self.assertIn("--- FAIL: TestP7", hidden.stdout)
                    self.assertNotIn("[build failed]", hidden.stdout)

    def test_each_input_variant_changes_consumed_fixture_data(self) -> None:
        for case in (case for case in cases() if case.ready):
            with self.subTest(case=case.id):
                with tempfile.TemporaryDirectory() as raw:
                    root = Path(raw)
                    case.builder(root)
                    before = {
                        path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
                        for path in root.rglob("*") if path.is_file()
                    }
                    for mutator in case.variant_mutators:
                        variant_root = root / f"variant-{case.variant_mutators.index(mutator)}"
                        shutil.copytree(root, variant_root, ignore=shutil.ignore_patterns("variant-*"))
                        mutator(variant_root)
                        after = {
                            path.relative_to(variant_root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
                            for path in variant_root.rglob("*") if path.is_file()
                        }
                        self.assertNotEqual(before, after, "variant did not change fixture data")

    def test_variant_selection_is_seeded_and_rotates_between_repetitions(self) -> None:
        for case in cases():
            first = variant_index(case, 1, seed=17)
            second = variant_index(case, 2, seed=17)
            self.assertNotEqual(first, second, case.id)
            self.assertEqual(first, variant_index(case, 1, seed=17), case.id)


if __name__ == "__main__":
    unittest.main()
