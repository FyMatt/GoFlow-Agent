#!/usr/bin/env python3
"""Run GoFlow's shared CI/release preflight checks."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

# Windows consoles commonly default to GBK while Go/JSON output can contain
# arbitrary UTF-8 text. Keep the preflight report printable instead of turning
# a successful check into a UnicodeEncodeError.
for _stream in (sys.stdout, sys.stderr):
    try:
        _stream.reconfigure(encoding="utf-8", errors="replace")
    except (AttributeError, ValueError):
        pass


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


def release_build_command(python: str, targets: list[str]) -> tuple[list[str], list[str]]:
    """Return one clean build command and its de-duplicated target list."""

    unique_targets = list(dict.fromkeys(targets))
    command = [python, "scripts/build_release_assets.py", "--clean", "--version", "dev"]
    for target in unique_targets:
        command.extend(["--target", target])
    return command, unique_targets


def run(
    label: str,
    command: list[str],
    *,
    attempts: int = 1,
    retry_markers: list[str] | None = None,
    retry_delay_seconds: float = 2.0,
) -> None:
    print(f"\n==> {label}", flush=True)
    print("+ " + " ".join(command), flush=True)
    last_returncode = 0
    for attempt in range(1, attempts + 1):
        if attempts > 1:
            print(f"attempt {attempt}/{attempts}", flush=True)
        completed = subprocess.run(
            command,
            cwd=ROOT,
            env=os.environ.copy(),
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            errors="replace",
        )
        output = completed.stdout or ""
        if output:
            print(output, end="" if output.endswith("\n") else "\n", flush=True)
        if completed.returncode == 0:
            return
        last_returncode = completed.returncode
        retryable = attempt < attempts
        if retryable and retry_markers:
            retryable = any(marker in output for marker in retry_markers)
        if not retryable:
            raise SystemExit(completed.returncode)
        print(f"{label} failed with a retryable startup error; retrying in {retry_delay_seconds:g}s", flush=True)
        time.sleep(retry_delay_seconds)
    raise SystemExit(last_returncode)


def normalize_windows_temp() -> None:
    """Expand 8.3 short TEMP/TMP names (CI uses RUNNER~1) before running checks.

    Windows GitHub runners expose the temporary directory through a short path,
    while Go tests assert the caller-provided path form. Fixing every package to
    compare both forms is a larger refactor; normalising the environment keeps
    the whole preflight on one canonical form.
    """
    if os.name != "nt":
        return
    for name in ("TEMP", "TMP"):
        value = os.environ.get(name)
        if not value or "~" not in value:
            continue
        try:
            long_form = os.path.realpath(value)
        except OSError:
            continue
        if long_form and os.path.isdir(long_form) and os.path.normcase(long_form) != os.path.normcase(value):
            os.environ[name] = long_form
            tempfile.tempdir = None
            print(f"normalised {name} short path to {long_form}")


def main() -> int:
    normalize_windows_temp()
    args = parse_args()
    if args.skip_browser and args.browser_required:
        raise SystemExit("--skip-browser and --browser-required cannot be used together")

    python = sys.executable
    if not args.skip_go_test:
        run("Go tests", ["go", "test", "./..."])

    checks = [
        ("Secret validation", [python, "scripts/validate_no_secrets.py"]),
        ("Security regression validation", [python, "scripts/validate_security_regressions.py"]),
        ("Python MCP validation", [python, "scripts/validate_python_mcp.py"]),
        ("Extension workflow validation", [python, "scripts/validate_extension_workflow.py"]),
        ("Resource link validation", [python, "scripts/validate_resource_links.py"]),
        (
            "Golden Provider case-definition validation (non-P7)",
            [python, "scripts/validate_real_provider_matrix.py"],
        ),
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
        browser_retry_markers = [
            "Access is denied",
            "Crashpad",
            "Mojo",
            "crash server failed to launch",
            "拒绝访问",
        ]
        run(
            "Browser Studio smoke",
            browser_command,
            attempts=3,
            retry_markers=browser_retry_markers,
            retry_delay_seconds=3.0,
        )
        if args.browser_required:
            run(
                "Browser performance gates",
                [python, "scripts/smoke_http_browser.py", "--required", "--performance-only"],
                attempts=3,
                retry_markers=browser_retry_markers,
                retry_delay_seconds=3.0,
            )

    run("Deployment asset validation", [python, "scripts/validate_deployment_assets.py"])
    build_command, release_targets = release_build_command(python, args.release_target or [])
    if release_targets:
        run(
            f"Release archive build ({', '.join(release_targets)})",
            build_command,
        )
        run(
            "Release SBOM and license inventory",
            [
                python,
                "scripts/generate_sbom.py",
                "--version",
                "dev",
                "--output",
                "dist/SBOM.spdx.json",
                "--license-output",
                "dist/THIRD_PARTY_LICENSES.json",
                "--update-checksums",
            ],
        )
    for target in release_targets:
        run(
            f"Release archive validation ({target})",
            [
                python,
                "scripts/validate_release_archives.py",
                "--dist",
                "dist",
                "--version",
                "dev",
                "--target",
                target,
                "--require-sbom",
                "--require-license-report",
            ],
        )
    print("\npreflight checks passed", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
