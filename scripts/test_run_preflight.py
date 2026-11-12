from __future__ import annotations

import unittest

from scripts.run_preflight import release_build_command


class ReleaseBuildCommandTests(unittest.TestCase):
    def test_builds_all_unique_targets_in_one_clean_command(self) -> None:
        command, targets = release_build_command(
            "python",
            ["windows/amd64", "windows/amd64"],
        )
        self.assertEqual(targets, ["windows/amd64"])
        self.assertEqual(command[:5], ["python", "scripts/build_release_assets.py", "--clean", "--version", "dev"])
        self.assertEqual(command[5:], ["--target", "windows/amd64"])

    def test_empty_targets_do_not_request_a_build(self) -> None:
        self.assertEqual(release_build_command("python", []), (["python", "scripts/build_release_assets.py", "--clean", "--version", "dev"], []))


if __name__ == "__main__":
    unittest.main()
