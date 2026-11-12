#!/usr/bin/env python3
"""Sign GoFlow release artifacts with cosign keyless signing.

The GitHub release workflow uses this after archives, SHA256SUMS, and the SBOM
have been generated. It writes one Sigstore bundle next to each signed file:

    artifact-name.sigstore.json

The script is intentionally small and delegates identity, certificate, and
transparency log handling to cosign.
"""

from __future__ import annotations

import argparse
import os
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def release_artifacts(dist: Path) -> list[Path]:
    artifacts = []
    for path in sorted(dist.iterdir()):
        if not path.is_file():
            continue
        if path.name.endswith(".sigstore.json"):
            continue
        artifacts.append(path)
    if not artifacts:
        raise AssertionError(f"no release artifacts found in {dist}")
    return artifacts


def verify_github_identity(cosign: str, artifact: Path, bundle: Path) -> None:
    workflow_ref = os.environ.get("GITHUB_WORKFLOW_REF", "").strip()
    if not workflow_ref:
        raise AssertionError("GITHUB_WORKFLOW_REF is required for GitHub identity verification")
    identity = f"https://github.com/{workflow_ref}"
    command = [
        cosign,
        "verify-blob",
        "--bundle",
        str(bundle),
        "--certificate-identity",
        identity,
        "--certificate-oidc-issuer",
        "https://token.actions.githubusercontent.com",
        str(artifact),
    ]
    print("+", " ".join(command))
    subprocess.run(command, cwd=ROOT, check=True)


def sign_artifact(cosign: str, artifact: Path, *, verify_identity: bool = False) -> Path:
    bundle = artifact.with_name(f"{artifact.name}.sigstore.json")
    if bundle.exists():
        bundle.unlink()
    command = [
        cosign,
        "sign-blob",
        "--yes",
        "--bundle",
        str(bundle),
        str(artifact),
    ]
    print("+", " ".join(command))
    subprocess.run(command, cwd=ROOT, check=True)
    if not bundle.is_file():
        raise AssertionError(f"cosign did not create bundle: {bundle}")
    if verify_identity:
        verify_github_identity(cosign, artifact, bundle)
    print(f"created {bundle.relative_to(ROOT)}")
    return bundle


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", default="dist")
    parser.add_argument("--cosign", default="cosign")
    parser.add_argument("--verify-github-identity", action="store_true")
    args = parser.parse_args()

    dist = (ROOT / args.dist).resolve()
    if not dist.exists():
        raise AssertionError(f"missing dist directory: {dist}")
    for artifact in release_artifacts(dist):
        sign_artifact(args.cosign, artifact, verify_identity=args.verify_github_identity)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
