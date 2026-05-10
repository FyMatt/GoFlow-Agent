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

Validate a single-target local build:

```bash
python scripts/build_release_assets.py --clean --version dev --target linux/amd64
python scripts/validate_release_archives.py --dist dist --version dev --target linux/amd64
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
- `kits/`
- `examples/`
- `docs/`
- launcher script: `run-goflow.sh` or `run-goflow.cmd`

The launcher sets MCP binary paths and defaults `GOFLOW_BACKUP_*` to the primary
Provider values when backup variables are unset.

Direct execution from `bin/goflow` or `bin/goflow.exe` is also supported. When
the executable is launched from an archive `bin` directory, GoFlow resolves
runtime home to the archive root and defaults to `configs/goflow.binary.yaml`
instead of looking for `bin/configs/goflow.yaml`.

The `kits/` and `examples/` directories are included so downloaded archives have
the same starter Kit catalog and copyable extension examples described in the
README and Web Studio. The archive validator checks expected filenames,
`SHA256SUMS`, optional SBOM and signature bundles, and the internal archive
layout, including representative Kit and example files. The build script writes
`dist/SHA256SUMS` for archive integrity checks. The SBOM script writes
`dist/SBOM.spdx.json` and can append its checksum to `SHA256SUMS`. The release
workflow signs archives, `SHA256SUMS`, and `SBOM.spdx.json` with Sigstore/Cosign
keyless signing.

## GitHub Release

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`:

```bash
git tag v0.1.3
git push origin v0.1.3
```

The workflow:

1. runs release preflight: `go test ./...`, Python MCP validation, resource
   links, Web Studio i18n/docs checks, HTTP Studio smoke, required browser
   Studio smoke, and deployment asset validation
2. builds binary archives with `scripts/build_release_assets.py`
3. generates `SBOM.spdx.json` with `scripts/generate_sbom.py`
4. signs archives, `SHA256SUMS`, and `SBOM.spdx.json`
5. validates expected OS/architecture archive names, checksums, SBOM, and signatures
6. uploads workflow artifacts
7. attaches release files to the GitHub Release
8. builds the main Docker image and the MCP Python tool-runtime image
9. pushes both images to GHCR when the run is tag-triggered
10. signs pushed Docker image digests with Cosign keyless signing

The archive and Docker jobs both depend on the preflight job. A failed browser
smoke test, resource validation, documentation check, or deployment validation
blocks publishing.

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

Run the full shared preflight before cutting a release:

```bash
python scripts/run_preflight.py --browser-required
```

To include local archive packaging in the same preflight, add one or more
release targets:

```bash
python scripts/run_preflight.py --browser-required --release-target windows/amd64
```

For a local machine without Chrome, Edge, or Chromium, use
`python scripts/run_preflight.py --skip-browser` for the non-browser checks.

`scripts/smoke_http_studio.py` builds a temporary `goflow` binary, starts HTTP
mode with placeholder model settings, and checks the embedded Studio pages,
assets, and API contracts. It is a lightweight HTTP/static/API smoke test, not
a full browser interaction test.

`scripts/smoke_http_browser.py` is an optional browser execution smoke test. It
uses a locally installed Chrome, Edge, or Chromium binary to render `/console`,
`/workflows`, `/console#playground`, `/console#approvals`,
`/console#catalog`, `/console#status`, `/console#workspace`,
`/console#settings`, and Chinese `?lang=zh` deep links. This catches
JavaScript white-screen failures, recursion failures, and mixed-language
regressions across the Overview, Workflow Studio, Playground, Approvals,
Resource Studio, Observability, Workspace, and Settings surfaces. It skips when
no browser is available by default. CI and release preflight should pass
`--required` so a missing or broken browser fails the check instead of silently
skipping Web Studio execution coverage.
