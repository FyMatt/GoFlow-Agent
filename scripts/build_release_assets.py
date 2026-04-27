#!/usr/bin/env python3
"""Build cross-platform GoFlow release archives.

The script intentionally uses only the Python standard library so it can run in
local shells and GitHub Actions without extra packaging dependencies.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import shutil
import subprocess
import tarfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_TARGETS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("windows", "amd64"),
    ("windows", "arm64"),
]
GO_PROJECTS = [
    ("goflow", "./cmd/goflow"),
    ("file_tools", "./mcp_servers/file_tools"),
    ("web_tools", "./mcp_servers/web_tools"),
]
COMMON_DIRS = ["configs", "skills", "docs"]
COMMON_FILES = [
    ".env.example",
    "README.md",
    "README.zh-CN.md",
    "MASTER-PLAN.md",
    "mcp_servers/python_notes.py",
]


def parse_target(value: str) -> tuple[str, str]:
    parts = value.split("/")
    if len(parts) != 2 or not parts[0] or not parts[1]:
        raise argparse.ArgumentTypeError("target must use GOOS/GOARCH, for example linux/amd64")
    return parts[0], parts[1]


def run(command: list[str], env: dict[str, str]) -> None:
    print("+", " ".join(command))
    subprocess.run(command, cwd=ROOT, env=env, check=True)


def binary_name(name: str, goos: str) -> str:
    if goos == "windows":
        return f"{name}.exe"
    return name


def build_go_binary(name: str, package: str, version: str, goos: str, goarch: str, out_dir: Path) -> None:
    env = os.environ.copy()
    env.update({"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch})
    output = out_dir / binary_name(name, goos)
    ldflags = "-s -w"
    if name == "goflow":
        ldflags += f" -X github.com/FyMatt/GoFlow-Agent/internal/version.Version={version}"
    run(
        [
            "go",
            "build",
            "-trimpath",
            "-ldflags",
            ldflags,
            "-o",
            str(output),
            package,
        ],
        env,
    )


def copy_common_assets(stage_dir: Path) -> None:
    for dirname in COMMON_DIRS:
        src = ROOT / dirname
        if src.exists():
            shutil.copytree(src, stage_dir / dirname, ignore=shutil.ignore_patterns("__pycache__"))
    for filename in COMMON_FILES:
        src = ROOT / filename
        if src.exists():
            dst = stage_dir / filename
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)


def write_launchers(stage_dir: Path, goos: str) -> None:
    if goos == "windows":
        script = stage_dir / "run-goflow.cmd"
        script.write_text(
            """@echo off
setlocal
set "ROOT=%~dp0"
set "GOFLOW_FILE_TOOLS_CMD=%ROOT%bin\\file_tools.exe"
set "GOFLOW_WEB_TOOLS_CMD=%ROOT%bin\\web_tools.exe"
if "%GOFLOW_PYTHON_CMD%"=="" set "GOFLOW_PYTHON_CMD=python"
set "GOFLOW_PYTHON_NOTES_PATH=%ROOT%mcp_servers\\python_notes.py"
if "%GOFLOW_BACKUP_BASE_URL%"=="" set "GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%"
if "%GOFLOW_BACKUP_API_KEY%"=="" set "GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%"
if "%GOFLOW_BACKUP_MODEL%"=="" set "GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%"
if "%~1"=="" (
  set "WORKSPACE=%CD%\\workspace"
) else (
  set "WORKSPACE=%~1"
)
"%ROOT%bin\\goflow.exe" --config "%ROOT%configs\\agent.binary.yaml" --workspace "%WORKSPACE%"
""",
            encoding="utf-8",
            newline="\r\n",
        )
        return

    script = stage_dir / "run-goflow.sh"
    script.write_text(
        """#!/usr/bin/env sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export GOFLOW_FILE_TOOLS_CMD="$ROOT/bin/file_tools"
export GOFLOW_WEB_TOOLS_CMD="$ROOT/bin/web_tools"
export GOFLOW_PYTHON_CMD="${GOFLOW_PYTHON_CMD:-python3}"
export GOFLOW_PYTHON_NOTES_PATH="$ROOT/mcp_servers/python_notes.py"
export GOFLOW_BACKUP_BASE_URL="${GOFLOW_BACKUP_BASE_URL:-${GOFLOW_BASE_URL:-}}"
export GOFLOW_BACKUP_API_KEY="${GOFLOW_BACKUP_API_KEY:-${GOFLOW_API_KEY:-}}"
export GOFLOW_BACKUP_MODEL="${GOFLOW_BACKUP_MODEL:-${GOFLOW_MODEL:-}}"
WORKSPACE="${1:-$PWD/workspace}"
exec "$ROOT/bin/goflow" --config "$ROOT/configs/agent.binary.yaml" --workspace "$WORKSPACE"
""",
        encoding="utf-8",
    )
    script.chmod(0o755)


def archive_target(stage_dir: Path, archive_base: Path, goos: str) -> Path:
    if goos == "windows":
        archive_path = archive_base.parent / f"{archive_base.name}.zip"
        with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for path in sorted(stage_dir.rglob("*")):
                archive.write(path, path.relative_to(stage_dir.parent))
        return archive_path

    archive_path = archive_base.parent / f"{archive_base.name}.tar.gz"
    with tarfile.open(archive_path, "w:gz") as archive:
        archive.add(stage_dir, arcname=stage_dir.name)
    return archive_path


def build_target(version: str, goos: str, goarch: str, dist_dir: Path) -> Path:
    package_name = f"goflow-agent_{version}_{goos}_{goarch}"
    stage_dir = dist_dir / package_name
    if stage_dir.exists():
        shutil.rmtree(stage_dir)
    (stage_dir / "bin").mkdir(parents=True)

    for name, package in GO_PROJECTS:
        build_go_binary(name, package, version, goos, goarch, stage_dir / "bin")
    copy_common_assets(stage_dir)
    write_launchers(stage_dir, goos)
    archive = archive_target(stage_dir, dist_dir / package_name, goos)
    print(f"created {archive.relative_to(ROOT)}")
    return archive


def write_checksums(archives: list[Path], dist_dir: Path) -> Path:
    checksum_path = dist_dir / "SHA256SUMS"
    lines = []
    for archive in sorted(archives):
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        lines.append(f"{digest}  {archive.name}")
    checksum_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"created {checksum_path.relative_to(ROOT)}")
    return checksum_path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default=os.environ.get("GOFLOW_VERSION", "dev"))
    parser.add_argument("--dist", default="dist")
    parser.add_argument("--target", action="append", type=parse_target, help="GOOS/GOARCH target; may be repeated")
    parser.add_argument("--clean", action="store_true", help="remove the dist directory before building")
    args = parser.parse_args()

    dist_dir = (ROOT / args.dist).resolve()
    if args.clean and dist_dir.exists():
        shutil.rmtree(dist_dir)
    dist_dir.mkdir(parents=True, exist_ok=True)

    targets = args.target or DEFAULT_TARGETS
    archives = []
    for goos, goarch in targets:
        archives.append(build_target(args.version, goos, goarch, dist_dir))
    write_checksums(archives, dist_dir)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
