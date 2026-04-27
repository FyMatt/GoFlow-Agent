# Release Packaging

GoFlow release delivery has two paths:

- binary archives for Windows and Linux users
- Docker images for HTTP/API deployments

For direct end-user commands, see [Installation And Deployment](./install.md)
and [安装与部署](./install.zh-CN.md).

## Binary Archives

Build archives locally:

```bash
python scripts/build_release_assets.py --clean --version v0.1.2
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
python scripts/generate_sbom.py --version v0.1.2 --output dist/SBOM.spdx.json --update-checksums
python scripts/validate_release_archives.py --dist dist --version v0.1.2 --require-sbom
```

Each archive contains:

- `bin/goflow`
- `bin/file_tools`
- `bin/web_tools`
- `mcp_servers/python_notes.py`
- `configs/agent.binary.yaml`
- `skills/`
- `docs/`
- launcher script: `run-goflow.sh` or `run-goflow.cmd`

The launcher sets the MCP binary paths through environment variables, then starts:

```text
goflow --config configs/agent.binary.yaml --workspace <workspace>
```

The Python MCP server still requires Python on the target machine. Linux launchers default to `python3`; Windows launchers default to `python`. Override `GOFLOW_PYTHON_CMD` when needed.

The build script writes `dist/SHA256SUMS` for archive integrity checks. The SBOM
script writes `dist/SBOM.spdx.json` and can append its checksum to `SHA256SUMS`.

## GitHub Release

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`:

```bash
git tag v0.1.2
git push origin v0.1.2
```

The workflow:

1. builds the binary archives with `scripts/build_release_assets.py`
2. generates `SBOM.spdx.json` with `scripts/generate_sbom.py`
3. validates the expected OS/architecture archive names, `SHA256SUMS`, and SBOM
4. uploads workflow artifacts
5. attaches the archives, `SHA256SUMS`, and `SBOM.spdx.json` to the GitHub Release
6. builds the Docker image
7. pushes the Docker image to GHCR when the run is tag-triggered

## Docker Image

The release workflow publishes:

```text
ghcr.io/fymatt/goflow-agent:<tag>
ghcr.io/fymatt/goflow-agent:latest
```

The Docker image uses `configs/agent.docker.yaml` and starts HTTP mode by default:

```text
goflow --config /app/configs/agent.docker.yaml --workspace /workspace --http :8080
```

For local Docker usage, see [Deployment](./deployment.md).

## Validation

Run static release/deployment validation without Docker:

```bash
python scripts/validate_deployment_assets.py
```

Run the full Go and Python smoke tests before cutting a release:

```bash
go test ./...
python scripts/validate_python_mcp.py
python scripts/validate_extension_workflow.py
python scripts/validate_deployment_assets.py
```
