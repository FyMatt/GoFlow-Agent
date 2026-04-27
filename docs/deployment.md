# Deployment

GoFlow is intended to run on Windows, Linux, and inside Docker.

For a user-facing install guide with release archive and published image
commands, see [Installation And Deployment](./install.md). For Chinese
instructions, see [安装与部署](./install.zh-CN.md).

## Local Windows

Use the provided command script after setting provider credentials:

```bat
set GOFLOW_API_KEY=your-key
run-goflow.example.cmd
```

The script runs the CLI against the workspace configured inside the script. Edit `--workspace` there when you want to point GoFlow at a different project.

## Local Linux

Run directly from the repo:

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL

go run ./cmd/goflow --workspace /path/to/workspace
```

Start the HTTP API:

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

Linux MCP servers can optionally use cgroup v2 resource controls:

```yaml
isolation: linux_cgroup
isolation_options:
  cgroup_parent: /sys/fs/cgroup/goflow
  memory_max: 256M
  pids_max: "64"
```

The cgroup parent must already be writable by the GoFlow process. This is resource control only; use containers or OS sandboxing when you need filesystem or network isolation.

## Docker

The Docker image uses `configs/agent.docker.yaml`. Unlike the development config, it runs compiled MCP binaries for the Go MCP servers:

- `/app/bin/file_tools`
- `/app/bin/web_tools`
- `python3 /app/mcp_servers/python_notes.py`

Build:

```bash
docker build -t goflow-agent:local .
```

Run:

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  goflow-agent:local
```

Compose:

```bash
docker compose up --build
```

Use the published GHCR image when you do not need a local source build:

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

The container defaults to:

```text
goflow --config /app/configs/agent.docker.yaml --workspace /workspace --http :8080
```

## Configuration Selection

Use `--config` to choose a runtime config:

```bash
goflow --config /app/configs/agent.docker.yaml --workspace /workspace --http :8080
```

Relative `--config` paths are resolved from the runtime home. Absolute paths are used directly.

Binary release archives use `configs/agent.binary.yaml`. The included launcher scripts set:

- `GOFLOW_FILE_TOOLS_CMD`
- `GOFLOW_WEB_TOOLS_CMD`
- `GOFLOW_PYTHON_CMD`
- `GOFLOW_PYTHON_NOTES_PATH`
- `GOFLOW_BACKUP_*` defaults copied from the primary provider variables when unset

This lets archive installs run compiled Go MCP tools without requiring `go run` on the target machine.

If an operator runs `bin/goflow` or `bin/goflow.exe` directly from the archive,
the executable detects the parent archive root, loads `configs/agent.binary.yaml`
by default, and uses the compiled MCP tools from `bin/`.

## Volumes

Mount the target project at `/workspace`. Session state is stored at:

```text
/workspace/.goflow/session.json
```

Keep runtime files under `/app` and target project files under `/workspace` so tool boundaries remain clear.

## CI Smoke Tests

The repository includes `.github/workflows/ci.yml` with:

- Linux `go test ./...`
- Python MCP smoke tests
- deployment asset validation
- Docker image build
- container startup check against `GET /api/session`

Run the local deployment asset check without Docker:

```bash
python scripts/validate_deployment_assets.py
```

For tag-based binary archives and GHCR image publishing, see [Release Packaging](./release.md).
