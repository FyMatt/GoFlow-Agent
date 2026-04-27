#!/usr/bin/env python3
"""Generate a lightweight SPDX SBOM for GoFlow release assets.

The script intentionally uses only Python's standard library and `go list` so
release jobs do not need an additional SBOM action or package manager.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import quote


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUTPUT = "dist/SBOM.spdx.json"


def run_go_list() -> list[dict[str, object]]:
    result = subprocess.run(
        ["go", "list", "-m", "-json", "all"],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=True,
    )
    decoder = json.JSONDecoder()
    text = result.stdout
    index = 0
    modules: list[dict[str, object]] = []
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            break
        module, index = decoder.raw_decode(text, index)
        if isinstance(module, dict):
            modules.append(module)
    if not modules:
        raise AssertionError("go list returned no modules")
    return modules


def spdx_id(value: str) -> str:
    cleaned = re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")
    return f"SPDXRef-Package-{cleaned or 'unknown'}"


def purl(module_path: str, version: str | None) -> str:
    encoded_path = quote(module_path, safe="/._-")
    if version:
        return f"pkg:golang/{encoded_path}@{quote(version, safe='._-+')}"
    return f"pkg:golang/{encoded_path}"


def package_for(module: dict[str, object], version_override: str | None = None) -> dict[str, object]:
    path = str(module.get("Path", "unknown"))
    version = version_override or str(module.get("Version", "") or "")
    package_id = spdx_id(f"{path}-{version}" if version else path)
    package: dict[str, object] = {
        "name": path,
        "SPDXID": package_id,
        "downloadLocation": "NOASSERTION",
        "filesAnalyzed": False,
        "licenseConcluded": "NOASSERTION",
        "licenseDeclared": "NOASSERTION",
        "copyrightText": "NOASSERTION",
        "externalRefs": [
            {
                "referenceCategory": "PACKAGE-MANAGER",
                "referenceType": "purl",
                "referenceLocator": purl(path, version or None),
            }
        ],
    }
    if version:
        package["versionInfo"] = version
    return package


def build_document(version: str, modules: list[dict[str, object]]) -> dict[str, object]:
    root_module = next((module for module in modules if module.get("Main")), modules[0])
    created = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    root_package = package_for(root_module, version)
    dependency_packages = [package_for(module) for module in modules if module is not root_module]
    packages = [root_package, *dependency_packages]

    relationships: list[dict[str, str]] = [
        {
            "spdxElementId": "SPDXRef-DOCUMENT",
            "relationshipType": "DESCRIBES",
            "relatedSpdxElement": str(root_package["SPDXID"]),
        }
    ]
    for package in dependency_packages:
        relationships.append(
            {
                "spdxElementId": str(root_package["SPDXID"]),
                "relationshipType": "DEPENDS_ON",
                "relatedSpdxElement": str(package["SPDXID"]),
            }
        )

    return {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"GoFlow-Agent {version}",
        "documentNamespace": f"https://github.com/FyMatt/GoFlow-Agent/sbom/{quote(version, safe='._-')}",
        "creationInfo": {
            "created": created,
            "creators": ["Tool: scripts/generate_sbom.py"],
        },
        "packages": packages,
        "relationships": relationships,
    }


def update_checksums(output_path: Path) -> None:
    checksum_path = output_path.parent / "SHA256SUMS"
    if not checksum_path.exists():
        raise AssertionError(f"cannot update missing checksum file: {checksum_path}")
    digest = hashlib.sha256(output_path.read_bytes()).hexdigest()
    entry = f"{digest}  {output_path.name}"
    existing = checksum_path.read_text(encoding="utf-8").splitlines()
    kept = [line for line in existing if not line.endswith(f"  {output_path.name}")]
    kept.append(entry)
    checksum_path.write_text("\n".join(kept) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default=os.environ.get("GOFLOW_VERSION", "dev"))
    parser.add_argument("--output", default=DEFAULT_OUTPUT)
    parser.add_argument("--update-checksums", action="store_true")
    args = parser.parse_args()

    output_path = (ROOT / args.output).resolve()
    output_path.parent.mkdir(parents=True, exist_ok=True)
    document = build_document(args.version, run_go_list())
    output_path.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"created {output_path.relative_to(ROOT)}")
    if args.update_checksums:
        update_checksums(output_path)
        print(f"updated {(output_path.parent / 'SHA256SUMS').relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
