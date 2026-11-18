#!/usr/bin/env python3
"""Validate release archive names, checksums, optional SBOM, and signatures."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import tarfile
import zipfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXPECTED_SUFFIXES = [
    "windows_amd64.zip",
]
COMMON_ARCHIVE_MEMBERS = [
    "README.md",
    "README.zh-CN.md",
    "LICENSE",
    ".env.example",
    "configs/goflow.binary.yaml",
    "configs/agents/chat.yaml",
    "configs/providers/primary.yaml",
    "configs/mcp_servers/file_tools.yaml",
    "configs/mcp_servers/command_tools.yaml",
    "configs/mcp_servers/python_notes.yaml",
    "configs/mcp_servers/skill_runner.yaml",
    "configs/mcp_servers/web_tools.yaml",
    "configs/mcp_servers/network_tools.yaml",
    "configs/mcp_servers/office_tools.yaml",
    "configs/mcp_servers/host_tools.yaml",
    "configs/mcp_servers/ssh_tools.yaml",
    "configs/mcp_servers/notification_tools.yaml",
    "skills/execution-plan/SKILL.md",
    "skills/local-host-diagnostics/SKILL.md",
    "skills/managed-local-config/SKILL.md",
    "skills/remote-host-diagnostics/SKILL.md",
    "skills/office-document-production/SKILL.md",
    "skills/authorized-red-team-validation/SKILL.md",
    "docs/install.md",
    "docs/install.zh-CN.md",
    "docs/resources.md",
    "docs/resources.zh-CN.md",
    "kits/multi-domain-agent-kit/kit.yaml",
    "kits/software-engineering-kit/kit.yaml",
    "kits/binary-analysis-kit/kit.yaml",
    "examples/README.md",
    "examples/README.zh-CN.md",
    "examples/extension-workflow/README.md",
    "examples/extension-workflow/configs/agents/workspace-reporter.yaml",
    "examples/extension-workflow/configs/mcp_servers/workspace_report.yaml",
    "examples/extension-workflow/mcp_servers/workspace_report.py",
    "examples/extension-workflow/skills/workspace-report/SKILL.md",
    "examples/extension-workflow/workflows/workspace-review/workflow.yaml",
    "mcp_servers/python_notes.py",
    "mcp_servers/notification_tools/README.md",
    "mcp_servers/notification_tools/README.zh-CN.md",
]
BINARIES = ["goflow", "goflow-install", "file_tools", "command_tools", "skill_runner", "web_tools", "network_tools", "office_tools", "host_tools", "ssh_tools", "notification_tools"]
FORBIDDEN_ARCHIVE_MARKERS = [
    "/__pycache__/",
    ".pyc",
    "/.artifacts/",
    "/.goflow/",
    "/backups/",
    "/secrets/",
    "/task-staging/",
    "/workspace/",
]


def parse_target(value: str) -> str:
    parts = value.split("/")
    if len(parts) != 2 or not parts[0] or not parts[1]:
        raise argparse.ArgumentTypeError("target must use GOOS/GOARCH, for example windows/amd64")
    goos, goarch = parts
    if (goos, goarch) != ("windows", "amd64"):
        raise argparse.ArgumentTypeError("supported release target is windows/amd64")
    return "windows_amd64.zip"


def read_json(path: Path) -> object:
    return json.loads(path.read_text(encoding="utf-8-sig"))


def parse_checksums(path: Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line_number, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw.strip()
        if not line:
            continue
        parts = line.split()
        if len(parts) != 2 or len(parts[0]) != 64 or any(ch not in "0123456789abcdefABCDEF" for ch in parts[0]):
            raise AssertionError(f"invalid SHA256SUMS entry at line {line_number}")
        name = parts[1].lstrip("*")
        if name in checksums:
            raise AssertionError(f"duplicate SHA256SUMS entry: {name}")
        checksums[name] = parts[0].lower()
    return checksums


def validate_checksum(path: Path, expected: str) -> None:
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    if actual != expected:
        raise AssertionError(f"SHA256 mismatch for {path.name}: expected {expected}, got {actual}")


def validate_license_report(path: Path) -> dict[str, str]:
    report = read_json(path)
    if not isinstance(report, dict) or report.get("schema_version") != "goflow.third-party-licenses.v1":
        raise AssertionError("THIRD_PARTY_LICENSES.json has an unsupported schema")
    packages = report.get("packages")
    if not isinstance(packages, list) or not packages:
        raise AssertionError("THIRD_PARTY_LICENSES.json must contain packages")
    licenses: dict[str, str] = {}
    for package in packages:
        if not isinstance(package, dict) or not package.get("module"):
            raise AssertionError("license report contains an invalid package")
        if not package.get("main") and package.get("license_concluded") == "NOASSERTION":
            raise AssertionError(f"dependency license is unresolved: {package.get('module')}")
        module = str(package["module"])
        if module in licenses:
            raise AssertionError(f"duplicate dependency license entry: {module}")
        licenses[module] = str(package.get("license_concluded") or "NOASSERTION")
        files = package.get("license_files")
        if not isinstance(files, list) or not files:
            raise AssertionError(f"license report has no license text for {package.get('module')}")
        for item in files:
            if not isinstance(item, dict) or not item.get("name") or not isinstance(item.get("text"), str):
                raise AssertionError(f"invalid license file entry for {package.get('module')}")
            actual = hashlib.sha256(str(item["text"]).encode("utf-8")).hexdigest()
            if actual != item.get("sha256"):
                raise AssertionError(f"license text hash mismatch for {package.get('module')}/{item.get('name')}")
    return licenses


def validate_sbom(path: Path) -> dict[str, str]:
    sbom = read_json(path)
    if not isinstance(sbom, dict) or sbom.get("spdxVersion") != "SPDX-2.3":
        raise AssertionError("SBOM.spdx.json must use SPDX-2.3")
    packages = sbom.get("packages")
    if not isinstance(packages, list) or not packages:
        raise AssertionError("SBOM.spdx.json must contain packages")
    licenses: dict[str, str] = {}
    for package in packages:
        if not isinstance(package, dict) or not package.get("name"):
            raise AssertionError("SBOM.spdx.json contains an invalid package")
        name = str(package["name"])
        if name in licenses:
            raise AssertionError(f"SBOM.spdx.json contains duplicate package {name}")
        licenses[name] = str(package.get("licenseConcluded") or "NOASSERTION")
    return licenses


def bundle_claims_digest(bundle: object, digest: bytes) -> bool:
    expected = {
        digest.hex().lower(),
        f"sha256:{digest.hex().lower()}",
        base64.b64encode(digest).decode("ascii"),
    }

    def visit(value: object) -> bool:
        if isinstance(value, str):
            return value.lower() in expected or value in expected
        if isinstance(value, dict):
            return any(visit(item) for item in value.values())
        if isinstance(value, list):
            return any(visit(item) for item in value)
        return False

    return visit(bundle)


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
        raise AssertionError(f"{path.name} contains generated or private runtime files: {forbidden[:8]}")


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
    parser.add_argument("--require-license-report", action="store_true")
    parser.add_argument("--require-signatures", action="store_true")
    args = parser.parse_args()

    dist = (ROOT / args.dist).resolve()
    if not dist.exists():
        raise AssertionError(f"missing dist directory: {dist}")

    expected_suffixes = args.target or EXPECTED_SUFFIXES
    expected = [f"goflow-agent_{args.version}_{suffix}" for suffix in expected_suffixes]
    if args.require_sbom:
        expected.append("SBOM.spdx.json")
    if args.require_license_report:
        expected.append("THIRD_PARTY_LICENSES.json")
    missing = [name for name in expected if not (dist / name).is_file()]
    if missing:
        raise AssertionError(f"missing release artifacts: {missing}")

    checksum_path = dist / "SHA256SUMS"
    if not checksum_path.is_file():
        raise AssertionError("missing SHA256SUMS")
    checksums = parse_checksums(checksum_path)
    for name in expected:
        if name not in checksums:
            raise AssertionError(f"SHA256SUMS missing {name}")
        validate_checksum(dist / name, checksums[name])
    for name in expected:
        if name.endswith((".zip", ".tar.gz")):
            validate_archive_layout(dist / name, args.version)

    sbom_licenses = validate_sbom(dist / "SBOM.spdx.json") if args.require_sbom else None
    report_licenses = validate_license_report(dist / "THIRD_PARTY_LICENSES.json") if args.require_license_report else None
    if sbom_licenses is not None and report_licenses is not None and sbom_licenses != report_licenses:
        raise AssertionError("SBOM and dependency license report package/license conclusions differ")

    if args.require_signatures:
        signed = [*expected, "SHA256SUMS"]
        missing_signatures = [f"{name}.sigstore.json" for name in signed if not (dist / f"{name}.sigstore.json").is_file()]
        if missing_signatures:
            raise AssertionError(f"missing signature bundles: {missing_signatures}")
        for name in signed:
            bundle = read_json(dist / f"{name}.sigstore.json")
            if not isinstance(bundle, dict) or not bundle:
                raise AssertionError(f"invalid signature bundle: {name}.sigstore.json")
            digest = hashlib.sha256((dist / name).read_bytes()).digest()
            if not bundle_claims_digest(bundle, digest):
                raise AssertionError(f"signature bundle is not bound to {name}")

    print("release artifact validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
