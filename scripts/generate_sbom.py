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
DEFAULT_LICENSE_OUTPUT = "dist/THIRD_PARTY_LICENSES.json"
LICENSE_FILE_RE = re.compile(r"^(?:licen[cs]e|copying)(?:[._-].*)?$", re.IGNORECASE)
NOTICE_FILE_RE = re.compile(r"^notice(?:[._-].*)?$", re.IGNORECASE)


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


def package_for(
    module: dict[str, object],
    version_override: str | None = None,
    license_concluded: str = "NOASSERTION",
) -> dict[str, object]:
    path = str(module.get("Path", "unknown"))
    version = version_override or str(module.get("Version", "") or "")
    package_id = spdx_id(f"{path}-{version}" if version else path)
    package: dict[str, object] = {
        "name": path,
        "SPDXID": package_id,
        "downloadLocation": "NOASSERTION",
        "filesAnalyzed": False,
        "licenseConcluded": license_concluded,
        "licenseDeclared": license_concluded,
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


def build_document(
    version: str,
    modules: list[dict[str, object]],
    licenses: dict[str, str] | None = None,
) -> dict[str, object]:
    licenses = licenses or {}
    root_module = next((module for module in modules if module.get("Main")), modules[0])
    created = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    root_path = str(root_module.get("Path", "unknown"))
    root_package = package_for(root_module, version, licenses.get(root_path, "NOASSERTION"))
    dependency_packages = [
        package_for(module, license_concluded=licenses.get(str(module.get("Path", "unknown")), "NOASSERTION"))
        for module in modules
        if module is not root_module
    ]
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


def detect_spdx_license(text: str) -> str:
    normalized = " ".join(text.lower().split())
    licenses: list[str] = []
    if "apache license" in normalized and "version 2.0" in normalized:
        licenses.append("Apache-2.0")
    if "permission is hereby granted, free of charge" in normalized:
        licenses.append("MIT")
    if "redistribution and use in source and binary forms" in normalized:
        if "neither the name of" in normalized or "may be used to endorse or promote" in normalized:
            licenses.append("BSD-3-Clause")
        else:
            licenses.append("BSD-2-Clause")
    if "permission to use, copy, modify, and/or distribute this software for any purpose" in normalized:
        licenses.append("ISC")
    if "mozilla public license version 2.0" in normalized:
        licenses.append("MPL-2.0")
    if "gnu lesser general public license" in normalized:
        licenses.append("LGPL")
    elif "gnu general public license" in normalized:
        licenses.append("GPL")
    unique = list(dict.fromkeys(licenses))
    return " AND ".join(unique) if unique else "NOASSERTION"


def module_license_entry(module: dict[str, object]) -> dict[str, object]:
    module_path = str(module.get("Path", "unknown"))
    module_dir_value = str(module.get("Dir", "") or "")
    if not module_dir_value:
        raise AssertionError(f"module {module_path} has no source directory; run go mod download before release")
    module_dir = Path(module_dir_value)
    if not module_dir.is_dir():
        raise AssertionError(f"module {module_path} source directory does not exist")

    candidates = sorted(
        path
        for path in module_dir.iterdir()
        if path.is_file() and (LICENSE_FILE_RE.match(path.name) or NOTICE_FILE_RE.match(path.name))
    )
    license_files = [path for path in candidates if LICENSE_FILE_RE.match(path.name)]
    if not license_files:
        raise AssertionError(f"module {module_path} has no top-level license file")

    files: list[dict[str, object]] = []
    detected: list[str] = []
    for path in candidates:
        data = path.read_bytes()
        text = data.decode("utf-8", errors="replace")
        if LICENSE_FILE_RE.match(path.name):
            expression = detect_spdx_license(text)
            if expression != "NOASSERTION":
                detected.extend(expression.split(" AND "))
        files.append(
            {
                "name": path.name,
                "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
                "text": text,
            }
        )
    unique = list(dict.fromkeys(detected))
    concluded = " AND ".join(unique) if unique else "NOASSERTION"
    if not module.get("Main") and concluded == "NOASSERTION":
        raise AssertionError(f"module {module_path} license could not be identified")
    return {
        "module": module_path,
        "version": str(module.get("Version", "") or ""),
        "main": bool(module.get("Main")),
        "license_concluded": concluded,
        "license_files": files,
    }


def build_license_report(version: str, modules: list[dict[str, object]]) -> dict[str, object]:
    entries = [module_license_entry(module) for module in modules]
    return {
        "schema_version": "goflow.third-party-licenses.v1",
        "project": "github.com/FyMatt/GoFlow-Agent",
        "version": version,
        "generated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "packages": entries,
    }


def update_checksums(output_paths: list[Path]) -> None:
    if not output_paths:
        return
    checksum_path = output_paths[0].parent / "SHA256SUMS"
    if not checksum_path.exists():
        raise AssertionError(f"cannot update missing checksum file: {checksum_path}")
    existing = checksum_path.read_text(encoding="utf-8").splitlines()
    output_names = {path.name for path in output_paths}
    kept = [line for line in existing if not any(line.endswith(f"  {name}") for name in output_names)]
    for output_path in sorted(output_paths):
        digest = hashlib.sha256(output_path.read_bytes()).hexdigest()
        kept.append(f"{digest}  {output_path.name}")
    checksum_path.write_text("\n".join(kept) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default=os.environ.get("GOFLOW_VERSION", "dev"))
    parser.add_argument("--output", default=DEFAULT_OUTPUT)
    parser.add_argument("--license-output", default=DEFAULT_LICENSE_OUTPUT)
    parser.add_argument("--update-checksums", action="store_true")
    args = parser.parse_args()

    output_path = (ROOT / args.output).resolve()
    license_output_path = (ROOT / args.license_output).resolve()
    output_path.parent.mkdir(parents=True, exist_ok=True)
    license_output_path.parent.mkdir(parents=True, exist_ok=True)
    modules = run_go_list()
    license_report = build_license_report(args.version, modules)
    licenses = {
        str(package["module"]): str(package["license_concluded"])
        for package in license_report["packages"]
    }
    document = build_document(args.version, modules, licenses)
    output_path.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    license_output_path.write_text(json.dumps(license_report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"created {output_path.relative_to(ROOT)}")
    print(f"created {license_output_path.relative_to(ROOT)}")
    if args.update_checksums:
        update_checksums([output_path, license_output_path])
        print(f"updated {(output_path.parent / 'SHA256SUMS').relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
