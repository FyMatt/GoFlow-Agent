from __future__ import annotations

import argparse
import base64
import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from scripts import generate_sbom
from scripts import build_release_assets
from scripts import validate_deployment_assets
from scripts import validate_docs
from scripts import validate_release_archives


class ReleaseSupplyChainTests(unittest.TestCase):
    def test_notification_connector_release_assets_without_building(self) -> None:
        self.assertIn(("notification_tools", "./mcp_servers/notification_tools"), build_release_assets.GO_PROJECTS)
        self.assertIn("notification_tools", validate_release_archives.BINARIES)
        docs = ["mcp_servers/notification_tools/README.md", "mcp_servers/notification_tools/README.zh-CN.md"]
        for asset in docs:
            self.assertIn(asset, build_release_assets.COMMON_FILES)
            self.assertIn(asset, validate_release_archives.COMMON_ARCHIVE_MEMBERS)
            path = build_release_assets.ROOT / asset
            self.assertEqual(validate_docs.validate_bilingual_pair(path), [])
            self.assertEqual(validate_docs.validate_links(path, path.read_text(encoding="utf-8")), [])
        self.assertIn("configs/mcp_servers/notification_tools.yaml", validate_release_archives.COMMON_ARCHIVE_MEMBERS)
        with tempfile.TemporaryDirectory() as tmp:
            stage = Path(tmp)
            with patch.object(build_release_assets, "COMMON_DIRS", []), patch.object(build_release_assets, "COMMON_FILES", docs):
                build_release_assets.copy_common_assets(stage)
            for asset in docs:
                self.assertEqual((stage / asset).read_bytes(), (build_release_assets.ROOT / asset).read_bytes())
            build_release_assets.write_launchers(stage, "windows")
            self.assertIn('GOFLOW_NOTIFICATION_TOOLS_CMD=%ROOT%bin\\notification_tools.exe', (stage / "run-goflow.cmd").read_text(encoding="utf-8"))

    def test_archive_layout_requires_each_notification_asset_without_zip(self) -> None:
        archive = Path("goflow-agent_test_windows_amd64.zip")
        root = archive.stem
        assets = ["bin/notification_tools.exe", "configs/mcp_servers/notification_tools.yaml", "mcp_servers/notification_tools/README.md", "mcp_servers/notification_tools/README.zh-CN.md"]
        names = {f"{root}/run-goflow.cmd"}
        names.update(f"{root}/{asset}" for asset in validate_release_archives.COMMON_ARCHIVE_MEMBERS)
        names.update(f"{root}/bin/{binary}.exe" for binary in validate_release_archives.BINARIES)
        with patch.object(validate_release_archives, "archive_member_names", return_value=names):
            validate_release_archives.validate_archive_layout(archive, "test")
        for asset in assets:
            with self.subTest(asset=asset), patch.object(validate_release_archives, "archive_member_names", return_value=names - {f"{root}/{asset}"}):
                with self.assertRaisesRegex(AssertionError, "notification_tools"):
                    validate_release_archives.validate_archive_layout(archive, "test")

    def test_windows_is_the_only_release_target(self) -> None:
        self.assertEqual(build_release_assets.DEFAULT_TARGETS, [("windows", "amd64")])
        self.assertEqual(validate_release_archives.EXPECTED_SUFFIXES, ["windows_amd64.zip"])
        self.assertEqual(build_release_assets.parse_target("windows/amd64"), ("windows", "amd64"))
        self.assertEqual(validate_release_archives.parse_target("windows/amd64"), "windows_amd64.zip")
        with self.assertRaises(argparse.ArgumentTypeError):
            build_release_assets.parse_target("linux/amd64")
        with self.assertRaises(argparse.ArgumentTypeError):
            validate_release_archives.parse_target("linux/amd64")

    def test_retired_ui_and_provider_files_are_release_blockers(self) -> None:
        original_root = validate_deployment_assets.ROOT
        try:
            for retired in (
                "configs/providers/backup.yaml",
                "internal/api/web/assets/views/approvals.js",
            ):
                with self.subTest(retired=retired), tempfile.TemporaryDirectory() as tmp:
                    root = Path(tmp)
                    target = root / retired
                    target.parent.mkdir(parents=True)
                    target.write_text("retired\n", encoding="utf-8")
                    validate_deployment_assets.ROOT = root
                    with self.assertRaisesRegex(AssertionError, "retired deployment files"):
                        validate_deployment_assets.validate_retired_files()
        finally:
            validate_deployment_assets.ROOT = original_root

    def test_release_display_path_accepts_external_dist_directory(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            external = Path(tmp).resolve() / "artifact.tar.gz"
            self.assertEqual(build_release_assets.display_path(external), external)

    def test_detects_composite_and_common_spdx_licenses(self) -> None:
        self.assertEqual(
            generate_sbom.detect_spdx_license(
                "Apache License Version 2.0\nPermission is hereby granted, free of charge"
            ),
            "Apache-2.0 AND MIT",
        )
        self.assertEqual(
            generate_sbom.detect_spdx_license(
                "Redistribution and use in source and binary forms are permitted. "
                "Neither the name of Example nor contributors may endorse products."
            ),
            "BSD-3-Clause",
        )

    def test_license_report_contains_hashed_text_without_local_paths(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "main"
            dependency = Path(tmp) / "dependency"
            root.mkdir()
            dependency.mkdir()
            (root / "LICENSE").write_text("Apache License\nVersion 2.0\n", encoding="utf-8")
            (dependency / "LICENSE").write_text(
                "Permission is hereby granted, free of charge, to any person obtaining a copy.",
                encoding="utf-8",
            )
            modules = [
                {"Path": "example/main", "Main": True, "Dir": str(root)},
                {"Path": "example/dependency", "Version": "v1.2.3", "Dir": str(dependency)},
            ]
            report = generate_sbom.build_license_report("dev", modules)
            serialized = json.dumps(report)
            self.assertNotIn(str(Path(tmp)), serialized)
            self.assertEqual(report["packages"][1]["license_concluded"], "MIT")
            license_file = report["packages"][1]["license_files"][0]
            self.assertEqual(
                license_file["sha256"],
                hashlib.sha256(license_file["text"].encode("utf-8")).hexdigest(),
            )

    def test_license_report_validation_rejects_tampered_text(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "THIRD_PARTY_LICENSES.json"
            report = {
                "schema_version": "goflow.third-party-licenses.v1",
                "packages": [
                    {
                        "module": "example/dependency",
                        "main": False,
                        "license_concluded": "MIT",
                        "license_files": [{"name": "LICENSE", "sha256": "0" * 64, "text": "changed"}],
                    }
                ],
            }
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaisesRegex(AssertionError, "hash mismatch"):
                validate_release_archives.validate_license_report(path)

    def test_checksum_validation_recomputes_file_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            artifact = root / "artifact.zip"
            artifact.write_bytes(b"release")
            digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
            checksum_path = root / "SHA256SUMS"
            checksum_path.write_text(f"{digest}  {artifact.name}\n", encoding="utf-8")
            checksums = validate_release_archives.parse_checksums(checksum_path)
            validate_release_archives.validate_checksum(artifact, checksums[artifact.name])
            artifact.write_bytes(b"tampered")
            with self.assertRaisesRegex(AssertionError, "SHA256 mismatch"):
                validate_release_archives.validate_checksum(artifact, checksums[artifact.name])

    def test_signature_bundle_must_claim_artifact_digest(self) -> None:
        digest = hashlib.sha256(b"release").digest()
        bundle = {"messageSignature": {"messageDigest": {"digest": base64.b64encode(digest).decode("ascii")}}}
        self.assertTrue(validate_release_archives.bundle_claims_digest(bundle, digest))
        self.assertFalse(validate_release_archives.bundle_claims_digest({"signed": True}, digest))

    def test_sbom_and_license_report_expose_matching_license_maps(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            sbom_path = root / "SBOM.spdx.json"
            report_path = root / "THIRD_PARTY_LICENSES.json"
            sbom_path.write_text(
                json.dumps(
                    {
                        "spdxVersion": "SPDX-2.3",
                        "packages": [{"name": "example/dependency", "licenseConcluded": "MIT"}],
                    }
                ),
                encoding="utf-8",
            )
            text = "Permission is hereby granted, free of charge"
            report_path.write_text(
                json.dumps(
                    {
                        "schema_version": "goflow.third-party-licenses.v1",
                        "packages": [
                            {
                                "module": "example/dependency",
                                "main": False,
                                "license_concluded": "MIT",
                                "license_files": [
                                    {
                                        "name": "LICENSE",
                                        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
                                        "text": text,
                                    }
                                ],
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )
            self.assertEqual(
                validate_release_archives.validate_sbom(sbom_path),
                validate_release_archives.validate_license_report(report_path),
            )


if __name__ == "__main__":
    unittest.main()
