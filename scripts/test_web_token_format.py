import json
import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MODULE_URI = (ROOT / "internal" / "api" / "web" / "assets" / "token_format.js").resolve().as_uri()


class WebTokenFormatTests(unittest.TestCase):
    def test_global_k_m_b_boundaries(self) -> None:
        script = f"""
          import {{ formatTokenCompact }} from {json.dumps(MODULE_URI)};
          const values = [0, 999, 1000, 999999, 1000000, 999999999, 1000000000, 2500000000];
          console.log(JSON.stringify(values.map(formatTokenCompact)));
        """
        completed = subprocess.run(
            ["node", "--input-type=module", "--eval", script],
            cwd=ROOT,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            check=False,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout)
        self.assertEqual(
            json.loads(completed.stdout),
            ["0K", "0.999K", "1K", "1000K", "1M", "1000M", "1B", "2.5B"],
        )


if __name__ == "__main__":
    unittest.main()
