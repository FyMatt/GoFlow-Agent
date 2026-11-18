from __future__ import annotations

import tempfile
import unittest
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from complex_task_fixtures import (
    MIN_VISIBLE_FILES,
    augment_large_fixture,
    install_hidden_acceptance,
    inspect_large_fixture,
)


class ComplexTaskFixtureTests(unittest.TestCase):
    def test_six_fixture_families_are_large_and_hidden_checks_are_post_run(self) -> None:
        cases = (
            ("dev-04-ttl-cache", "development"),
            ("sec-05-fix-regression", "security"),
            ("ops-05-controlled-change", "operations"),
            ("research-05-cited-report", "research"),
            ("docs-05-semantic-edit", "documents"),
            ("docs-06-cross-domain", "documents"),
        )
        for case_id, domain in cases:
            with self.subTest(case_id=case_id):
                with tempfile.TemporaryDirectory() as raw:
                    workspace = Path(raw)
                    manifest = augment_large_fixture(workspace, case_id, domain)
                    self.assertGreaterEqual(manifest["visible_files"], MIN_VISIBLE_FILES)
                    before = inspect_large_fixture(workspace, case_id, domain)
                    self.assertFalse(before["passed"])
                    install_hidden_acceptance(workspace, case_id, domain)
                    after = inspect_large_fixture(workspace, case_id, domain)
                    self.assertTrue(after["passed"], after)


if __name__ == "__main__":
    unittest.main()
