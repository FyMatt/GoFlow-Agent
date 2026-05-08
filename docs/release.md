# Release Packaging

[English](./release.md) | [简体中文](./release.zh-CN.md)

GoFlow release delivery has two paths:

- binary archives for Windows and Linux users
- Docker images for HTTP/API deployments

For direct end-user commands, see [Installation And Deployment](./install.md)
and [安装与部署](./install.zh-CN.md).

## Binary Archives

Build archives locally:

```bash
python scripts/build_release_assets.py --clean --version v0.1.3
```

Default targets:

- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

Build one target:

```bash
python scripts/build_release_assets.py --clean --version dev --target linux/amd64
```

Validate a full local build:

```bash
python scripts/generate_sbom.py --version v0.1.3 --output dist/SBOM.spdx.json --update-checksums
python scripts/validate_release_archives.py --dist dist --version v0.1.3 --require-sbom
```

If Cosign is available and configured for signing, validate signatures too:

```bash
python scripts/sign_release_artifacts.py --dist dist
python scripts/validate_release_archives.py --dist dist --version v0.1.3 --require-sbom --require-signatures
```

Each archive contains:

- `bin/goflow`
- `bin/file_tools`
- `bin/web_tools`
- `mcp_servers/python_notes.py`
- `configs/goflow.binary.yaml`
- `skills/`
- `docs/`
- launcher script: `run-goflow.sh` or `run-goflow.cmd`

The launcher sets MCP binary paths and defaults `GOFLOW_BACKUP_*` to the primary
Provider values when backup variables are unset.

Direct execution from `bin/goflow` or `bin/goflow.exe` is also supported. When
the executable is launched from an archive `bin` directory, GoFlow resolves
runtime home to the archive root and defaults to `configs/goflow.binary.yaml`
instead of looking for `bin/configs/goflow.yaml`.

The build script writes `dist/SHA256SUMS` for archive integrity checks. The SBOM
script writes `dist/SBOM.spdx.json` and can append its checksum to
`SHA256SUMS`. The release workflow signs archives, `SHA256SUMS`, and
`SBOM.spdx.json` with Sigstore/Cosign keyless signing.

## GitHub Release

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`:

```bash
git tag v0.1.3
git push origin v0.1.3
```

The workflow:

1. builds binary archives with `scripts/build_release_assets.py`
2. generates `SBOM.spdx.json` with `scripts/generate_sbom.py`
3. signs archives, `SHA256SUMS`, and `SBOM.spdx.json`
4. validates expected OS/architecture archive names, checksums, SBOM, and signatures
5. uploads workflow artifacts
6. attaches release files to the GitHub Release
7. builds the main Docker image and the MCP Python tool-runtime image
8. pushes both images to GHCR when the run is tag-triggered
9. signs pushed Docker image digests with Cosign keyless signing

GitHub Actions runs these scripts on GitHub-hosted runners, not on your local
machine. The runner checks out the repository, installs the requested toolchain,
runs the workflow steps, then uploads artifacts or pushes images using the
workflow permissions.

## Signature Verification

Verify a downloaded archive with its adjacent Sigstore bundle:

```bash
cosign verify-blob \
  --bundle goflow-agent_v0.1.3_linux_amd64.tar.gz.sigstore.json \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  goflow-agent_v0.1.3_linux_amd64.tar.gz
```

Verify the Docker image:

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/fymatt/goflow-agent:<tag>
```

Verify the MCP Python tool-runtime image the same way:

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/fymatt/goflow-agent-mcp-python:<tag>
```

## Docker Image

The release workflow publishes:

```text
ghcr.io/fymatt/goflow-agent:<tag>
ghcr.io/fymatt/goflow-agent:latest
ghcr.io/fymatt/goflow-agent-mcp-python:<tag>
ghcr.io/fymatt/goflow-agent-mcp-python:latest
```

The Docker image uses `configs/goflow.docker.yaml` and starts HTTP mode:

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

The MCP Python image is a minimal Python runtime for containerized tool
scaffold presets. GoFlow mounts the generated `mcp_servers/<name>.py` file into
`/goflow-tools/<name>.py` and runs it inside that image.

For local Docker usage, see [Deployment](./deployment.md).

## Validation

Run static release/deployment validation without Docker:

```bash
python scripts/validate_deployment_assets.py
```

Run the full smoke checks before cutting a release:

```bash
go test ./...
python scripts/validate_python_mcp.py
python scripts/validate_extension_workflow.py
python scripts/validate_deployment_assets.py
```
