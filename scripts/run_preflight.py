#!/usr/bin/env python3
"""Run GoFlow's shared CI/release preflight checks."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--browser-required",
        action="store_true",
        help="fail when the browser Studio smoke test cannot find or run Chrome, Edge, or Chromium",
    )
    parser.add_argument(
        "--skip-browser",
        action="store_true",
        help="skip the browser Studio smoke test for local environments without a browser",
    )
    parser.add_argument(
        "--skip-go-test",
        action="store_true",
        help="skip go test ./... when a caller already ran it",
    )
    parser.add_argument(
        "--release-target",
        action="append",
        help="also build and validate release archive target GOOS/GOARCH; may be repeated",
    )
    return parser.parse_args()


def run(label: str, command: list[str]) -> None:
    print(f"\n==> {label}", flush=True)
    print("+ " + " ".join(command), flush=True)
    completed = subprocess.run(command, cwd=ROOT, env=os.environ.copy())
    if completed.returncode != 0:
        raise SystemExit(completed.returncode)


def main() -> int:
    args = parse_args()
    if args.skip_browser and args.browser_required:
        raise SystemExit("--skip-browser and --browser-required cannot be used together")

    python = sys.executable
    if not args.skip_go_test:
        run("Go tests", ["go", "test", "./..."])

    checks = [
        ("Python MCP validation", [python, "scripts/validate_python_mcp.py"]),
        ("Extension workflow validation", [python, "scripts/validate_extension_workflow.py"]),
        ("Binary analysis kit validation", [python, "scripts/validate_binary_analysis_kit.py"]),
        ("Resource link validation", [python, "scripts/validate_resource_links.py"]),
        ("Web i18n validation", [python, "scripts/validate_web_i18n.py"]),
        ("Documentation validation", [python, "scripts/validate_docs.py"]),
        ("HTTP Studio smoke", [python, "scripts/smoke_http_studio.py"]),
    ]
    for label, command in checks:
        run(label, command)

    if not args.skip_browser:
        browser_command = [python, "scripts/smoke_http_browser.py"]
        if args.browser_required:
            browser_command.append("--required")
        run("Browser Studio smoke", browser_command)

    run("Deployment asset validation", [python, "scripts/validate_deployment_assets.py"])
    for target in args.release_target or []:
        run(
            f"Release archive build ({target})",
            [python, "scripts/build_release_assets.py", "--clean", "--version", "dev", "--target", target],
        )
        run(
            f"Release archive validation ({target})",
            [python, "scripts/validate_release_archives.py", "--dist", "dist", "--version", "dev", "--target", target],
        )
    print("\npreflight checks passed", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
