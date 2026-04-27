#!/usr/bin/env python3
"""Validate release archive names, checksums, and optional SBOM output."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXPECTED_SUFFIXES = [
    "linux_amd64.tar.gz",
    "linux_arm64.tar.gz",
    "windows_amd64.zip",
    "windows_arm64.zip",
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", default="dist")
    parser.add_argument("--version", required=True)
    parser.add_argument("--require-sbom", action="store_true")
    args = parser.parse_args()

    dist = (ROOT / args.dist).resolve()
    if not dist.exists():
        raise AssertionError(f"missing dist directory: {dist}")

    expected = [f"goflow-agent_{args.version}_{suffix}" for suffix in EXPECTED_SUFFIXES]
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

    if args.require_sbom:
        sbom = json.loads((dist / "SBOM.spdx.json").read_text(encoding="utf-8"))
        if sbom.get("spdxVersion") != "SPDX-2.3":
            raise AssertionError("SBOM.spdx.json must use SPDX-2.3")
        if not sbom.get("packages"):
            raise AssertionError("SBOM.spdx.json must contain packages")

    print("release artifact validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
