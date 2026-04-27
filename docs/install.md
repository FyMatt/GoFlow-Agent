# Installation And Deployment

This guide is for users who want to run GoFlow without reading the framework
internals first.

Use one of these paths:

- **Release archive**: best for local interactive CLI usage on Windows or Linux.
- **Docker image**: best for HTTP/API deployments and service integration.
- **Source checkout**: best for development and second-development work.

## Configure Provider Credentials

GoFlow talks to an OpenAI-compatible model endpoint. Copy the environment
template and fill in your provider values:

```bash
cp .env.example .env
```

Required values:

```env
GOFLOW_BASE_URL=https://api.deepseek.com/v1
GOFLOW_API_KEY=your-api-key
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_BASE_URL=https://api.deepseek.com/v1
GOFLOW_BACKUP_API_KEY=your-api-key
GOFLOW_BACKUP_MODEL=deepseek-chat
```

For local model servers, set `GOFLOW_BASE_URL` to the server's OpenAI-compatible
`/v1` endpoint.

## Run A Release Archive

Download the latest archive from:

```text
https://github.com/FyMatt/GoFlow-Agent/releases
```

Choose the archive for your platform:

- Windows x64: `goflow-agent_<version>_windows_amd64.zip`
- Windows ARM64: `goflow-agent_<version>_windows_arm64.zip`
- Linux x64: `goflow-agent_<version>_linux_amd64.tar.gz`
- Linux ARM64: `goflow-agent_<version>_linux_arm64.tar.gz`

### Windows

Extract the zip, then run:

```powershell
$env:GOFLOW_BASE_URL="https://api.deepseek.com/v1"
$env:GOFLOW_API_KEY="your-api-key"
$env:GOFLOW_MODEL="deepseek-chat"

.\run-goflow.cmd D:\Projects\my-workspace
```

### Linux

Extract the tarball, then run:

```bash
tar -xzf goflow-agent_<version>_linux_amd64.tar.gz
cd goflow-agent_<version>_linux_amd64

export GOFLOW_BASE_URL=https://api.deepseek.com/v1
export GOFLOW_API_KEY=your-api-key
export GOFLOW_MODEL=deepseek-chat

./run-goflow.sh /path/to/workspace
```

Release archives use `configs/agent.binary.yaml`. The launcher scripts set the
compiled MCP tool paths automatically, so Go is not required on the target
machine. They also default the backup provider environment variables to the
primary provider values when `GOFLOW_BACKUP_*` is not set. Python is still
required for the built-in Python MCP server.

You can also run `bin/goflow` or `bin/goflow.exe` directly from an extracted
archive. When launched from `bin`, GoFlow resolves runtime home to the archive
root and uses `configs/agent.binary.yaml` by default.

## Run The Published Docker Image

The release workflow publishes images to GitHub Container Registry:

```text
ghcr.io/fymatt/goflow-agent:<version>
ghcr.io/fymatt/goflow-agent:latest
```

Create a workspace directory and run the HTTP server:

```bash
mkdir -p workspace
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

PowerShell:

```powershell
mkdir workspace
docker run --rm -it `
  --env-file .env `
  -p 8080:8080 `
  -v "${PWD}\workspace:/workspace" `
  ghcr.io/fymatt/goflow-agent:<version>
```

The image starts HTTP mode by default:

```text
goflow --config /app/configs/agent.docker.yaml --workspace /workspace --http :8080
```

Check the server:

```bash
curl http://127.0.0.1:8080/api/session
```

Send a request:

```bash
curl -s -X POST http://127.0.0.1:8080/api/run \
  -H "Content-Type: application/json" \
  -d '{"input":"summarize this workspace"}'
```

For streaming clients, use:

```bash
curl -N -X POST http://127.0.0.1:8080/api/run/stream \
  -H "Content-Type: application/json" \
  -d '{"input":"hello"}'
```

## Run With Docker Compose

From a source checkout:

```bash
cp .env.example .env
mkdir -p workspace
docker compose up --build
```

Compose builds `goflow-agent:local`, mounts `./workspace` as `/workspace`, and
listens on `http://127.0.0.1:8080`.

## Run From Source

Use this path when developing the framework or adding tools, agents, skills, or
workflows:

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL

go run ./cmd/goflow --workspace /path/to/workspace
```

HTTP mode:

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

## Workspace And Session Files

GoFlow separates runtime files from target project files:

- Docker runtime files live under `/app`.
- Docker target files live under `/workspace`.
- Release archive runtime files live in the extracted archive directory.
- The target project is the path passed to `run-goflow.cmd`, `run-goflow.sh`, or `--workspace`.

Session state is stored inside the workspace:

```text
<workspace>/.goflow/session.json
```

All built-in file-oriented tools are expected to stay under the active workspace
root.

## Verify Release Artifacts

GitHub Releases include:

- platform archives
- `SHA256SUMS`
- `SBOM.spdx.json`
- one `.sigstore.json` signature bundle per signed release file

Use `SHA256SUMS` to verify downloaded archive integrity. `SBOM.spdx.json`
describes the Go module dependencies included in that release. The Sigstore
bundles prove the files were signed by this repository's GitHub Actions release
workflow.

Verify an asset with Cosign:

```bash
cosign verify-blob \
  --bundle goflow-agent_<version>_linux_amd64.tar.gz.sigstore.json \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  goflow-agent_<version>_linux_amd64.tar.gz
```
