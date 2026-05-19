#!/usr/bin/env python3
"""Validate release archive names, checksums, optional SBOM, and signatures."""

from __future__ import annotations

import argparse
import json
import tarfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXPECTED_SUFFIXES = [
    "linux_amd64.tar.gz",
    "linux_arm64.tar.gz",
    "windows_amd64.zip",
    "windows_arm64.zip",
]
COMMON_ARCHIVE_MEMBERS = [
    "README.md",
    "README.zh-CN.md",
    ".env.example",
    "configs/goflow.binary.yaml",
    "configs/agents/chat.yaml",
    "configs/providers/primary.yaml",
    "configs/providers/backup.yaml",
    "configs/mcp_servers/file_tools.yaml",
    "configs/mcp_servers/python_notes.yaml",
    "configs/mcp_servers/skill_runner.yaml",
    "configs/mcp_servers/web_tools.yaml",
    "configs/mcp_servers/network_tools.yaml",
    "skills/execution-plan/SKILL.md",
    "docs/install.md",
    "docs/install.zh-CN.md",
    "docs/resources.md",
    "docs/resources.zh-CN.md",
    "kits/multi-domain-agent-kit/kit.yaml",
    "kits/software-engineering-kit/kit.yaml",
    "kits/binary-analysis-kit/kit.yaml",
    "templates/workflows/engineering-parallel-delivery.yaml",
    "examples/README.md",
    "examples/README.zh-CN.md",
    "examples/extension-workflow/README.md",
    "examples/extension-workflow/configs/agents/workspace-reporter.yaml",
    "examples/extension-workflow/configs/mcp_servers/workspace_report.yaml",
    "examples/extension-workflow/mcp_servers/workspace_report.py",
    "examples/extension-workflow/skills/workspace-report/SKILL.md",
    "examples/extension-workflow/workflows/workspace-review/workflow.yaml",
    "examples/binary-analysis-kit/README.md",
    "examples/binary-analysis-kit/configs/agents/binary-analysis-kit-agent.yaml",
    "examples/binary-analysis-kit/configs/mcp_servers/binary-analysis-kit-helper.yaml",
    "examples/binary-analysis-kit/kits/binary-analysis-kit/kit.yaml",
    "examples/binary-analysis-kit/mcp_servers/binary-analysis-kit-helper.py",
    "examples/binary-analysis-kit/policies/workflow_rules/binary-analysis-kit-gate.yaml",
    "examples/binary-analysis-kit/skills/binary-analysis-kit-skill/SKILL.md",
    "examples/binary-analysis-kit/templates/teams/binary-analysis-kit-team.yaml",
    "examples/binary-analysis-kit/templates/workflows/binary-analysis-kit-template.yaml",
    "examples/binary-analysis-kit/workflows/binary-analysis-kit-workflow/workflow.yaml",
    "mcp_servers/python_notes.py",
]
BINARIES = ["goflow", "file_tools", "skill_runner", "web_tools", "network_tools"]
FORBIDDEN_ARCHIVE_MARKERS = ["/__pycache__/", ".pyc"]


def parse_target(value: str) -> str:
    parts = value.split("/")
    if len(parts) != 2 or not parts[0] or not parts[1]:
        raise argparse.ArgumentTypeError("target must use GOOS/GOARCH, for example linux/amd64")
    goos, goarch = parts
    extension = "zip" if goos == "windows" else "tar.gz"
    return f"{goos}_{goarch}.{extension}"


def read_json(path: Path) -> object:
    return json.loads(path.read_text(encoding="utf-8-sig"))


def archive_member_names(path: Path) -> set[str]:
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            return {name.replace("\\", "/") for name in archive.namelist()}
    if path.name.endswith(".tar.gz"):
        with tarfile.open(path, "r:gz") as archive:
            return {member.name.replace("\\", "/") for member in archive.getmembers()}
    raise AssertionError(f"unsupported archive type: {path.name}")


def validate_archive_layout(path: Path, version: str) -> None:
    names = archive_member_names(path)
    suffix = path.name.removeprefix(f"goflow-agent_{version}_")
    root = path.name.removesuffix(".zip").removesuffix(".tar.gz")
    is_windows = suffix.startswith("windows_")
    launcher = "run-goflow.cmd" if is_windows else "run-goflow.sh"
    binary_ext = ".exe" if is_windows else ""
    expected = [f"{root}/{launcher}"]
    expected.extend(f"{root}/{member}" for member in COMMON_ARCHIVE_MEMBERS)
    expected.extend(f"{root}/bin/{name}{binary_ext}" for name in BINARIES)
    missing = [member for member in expected if member not in names]
    if missing:
        raise AssertionError(f"{path.name} missing archive members: {missing}")
    forbidden = sorted(
        name for name in names for marker in FORBIDDEN_ARCHIVE_MARKERS if marker in f"/{name}"
    )
    if forbidden:
        raise AssertionError(f"{path.name} contains generated Python cache files: {forbidden[:8]}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", default="dist")
    parser.add_argument("--version", required=True)
    parser.add_argument(
        "--target",
        action="append",
        type=parse_target,
        help="validate one GOOS/GOARCH target; may be repeated; defaults to all release targets",
    )
    parser.add_argument("--require-sbom", action="store_true")
    parser.add_argument("--require-signatures", action="store_true")
    args = parser.parse_args()

    dist = (ROOT / args.dist).resolve()
    if not dist.exists():
        raise AssertionError(f"missing dist directory: {dist}")

    expected_suffixes = args.target or EXPECTED_SUFFIXES
    expected = [f"goflow-agent_{args.version}_{suffix}" for suffix in expected_suffixes]
    if args.require_sbom:
        expected.append("SBOM.spdx.json")
    missing = [name for name in expected if not (dist / name).is_file()]
    if missing:
        raise AssertionError(f"missing release artifacts: {missing}")

    checksum_path = dist / "SHA256SUMS"
    if not checksum_path.is_file():
        raise AssertionError("missing SHA256SUMS")
    checksum_text = checksum_path.read_text(encoding="utf-8")
    for name in expected:
        if name not in checksum_text:
            raise AssertionError(f"SHA256SUMS missing {name}")
    for name in expected:
        if name.endswith((".zip", ".tar.gz")):
            validate_archive_layout(dist / name, args.version)

    if args.require_sbom:
        sbom = read_json(dist / "SBOM.spdx.json")
        if sbom.get("spdxVersion") != "SPDX-2.3":
            raise AssertionError("SBOM.spdx.json must use SPDX-2.3")
        if not sbom.get("packages"):
            raise AssertionError("SBOM.spdx.json must contain packages")

    if args.require_signatures:
        signed = [*expected, "SHA256SUMS"]
        missing_signatures = [f"{name}.sigstore.json" for name in signed if not (dist / f"{name}.sigstore.json").is_file()]
        if missing_signatures:
            raise AssertionError(f"missing signature bundles: {missing_signatures}")
        for name in signed:
            bundle = read_json(dist / f"{name}.sigstore.json")
            if not isinstance(bundle, dict) or not bundle:
                raise AssertionError(f"invalid signature bundle: {name}.sigstore.json")

    print("release artifact validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
