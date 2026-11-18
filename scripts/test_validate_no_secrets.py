from __future__ import annotations

import unittest

from scripts.validate_no_secrets import EXCLUDED_DIRS, text_files


class ValidateNoSecretsTests(unittest.TestCase):
    def test_compiler_cache_directories_are_excluded_from_secret_scan(self) -> None:
        self.assertIn(".tmp-go-cache", EXCLUDED_DIRS)
        self.assertIn(".gocache", EXCLUDED_DIRS)
        leaked = [path for path in text_files() if ".tmp-go-cache" in path.parts or ".gocache" in path.parts]
        self.assertEqual(leaked, [])


if __name__ == "__main__":
    unittest.main()
