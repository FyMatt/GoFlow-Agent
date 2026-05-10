# Deployment

[English](./deployment.md) | [简体中文](./deployment.zh-CN.md)

GoFlow is intended to run on Windows, Linux, and inside Docker. For MCP tool
sandboxing, Docker/Podman container isolation is the recommended strong
boundary across operating systems. Native adapters are available for
compatibility, lifecycle cleanup, or resource control, but they are not a full
cross-platform sandbox.

For user-facing install commands, see [Installation And Deployment](./install.md).
For Chinese instructions, see [安装与部署](./install.zh-CN.md).

## Local Windows

Set provider credentials, then use the packaged command script:

```bat
set GOFLOW_BASE_URL=https://api.deepseek.com/v1
set GOFLOW_API_KEY=your-key
set GOFLOW_MODEL=deepseek-chat
set GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%
set GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%
set GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%

run-goflow.example.cmd D:\Projects\my-workspace
```

The script runs the CLI against the selected workspace. Edit the script or pass
an argument when you want a different target directory.

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

Start the HTTP API and Web Studio:

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

Open `http://127.0.0.1:8080/console`.

## MCP Container Isolation

MCP servers should run inside Docker/Podman when they are generated,
third-party, write-capable, exec-capable, network-capable, or otherwise
untrusted:

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-tools:latest
  workspace_mount: ro
  workspace_target: /workspace
  network: disabled
  memory: 256m
  cpus: "0.5"
  pids_limit: "64"
```

With container isolation, GoFlow runs Docker/Podman as the host process and
executes the configured MCP command inside the image. Missing Docker/Podman is
reported as an MCP startup error. GoFlow does not silently fall back to
unsandboxed host execution.

Native modes such as `process_group`, `windows_job`,
`windows_restricted_token`, `linux_cgroup`, and `linux_netns` are fallback or
helper modes. Use `isolation: container` when you need a broad sandbox.

## Docker

The Docker image uses `configs/goflow.docker.yaml` and compiled MCP binaries:

- `/app/bin/file_tools`
- `/app/bin/web_tools`
- `python3 /app/mcp_servers/python_notes.py`

Build:

```bash
docker build -t goflow-agent:local .
docker build -f docker/mcp-python/Dockerfile -t goflow-agent-mcp-python:local .
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

Use the published GHCR image:

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

The container starts:

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

## Configuration Selection

Use `--config` to choose a runtime config:

```bash
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

Relative config paths are resolved from runtime home. Absolute paths are used
directly.

Binary release archives use `configs/goflow.binary.yaml`. The included launcher
scripts set MCP binary paths and copy primary Provider variables into
`GOFLOW_BACKUP_*` when backup variables are unset.

If an operator runs `bin/goflow` or `bin/goflow.exe` directly from the archive,
the executable detects the parent archive root, loads
`configs/goflow.binary.yaml` by default, and uses compiled MCP tools from
`bin/`.

## Volumes

Mount the target project at `/workspace`. Session state is stored at:

```text
/workspace/.goflow/session.json
```

Keep runtime files under `/app` and target project files under `/workspace` so
tool boundaries remain clear.

## CI Smoke Tests

The repository includes `.github/workflows/ci.yml` with:

- shared preflight via `python scripts/run_preflight.py --browser-required`
- Linux `go test ./...`
- Python MCP smoke tests
- resource-link validation for bundled Agents, Skills, Tools, Kits, Teams,
  Policy Rules, Workflow Templates, Skill handoffs, Team roles, and Workflow
  stage references
- static Web Studio i18n validation for English/Chinese translation keys
- public Markdown link, bilingual-pair, and private-plan reference validation
- embedded HTTP Studio smoke test for `/console`, `/workflows`, static assets,
  and key JSON API endpoints
- required browser Studio smoke test for `/console`, `/workflows`,
  `/console#playground`, `/console#approvals`, `/console#catalog`,
  `/console#status`, `/console#workspace`, `/console#settings`, and Chinese
  `?lang=zh` deep links in CI; local runs can omit `--required` to skip when no
  Chrome, Edge, or Chromium browser is installed
- deployment asset validation
- Docker image builds for the runtime and MCP Python tool runtime
- container startup check against `GET /api/session`

Run local deployment validation without Docker:

```bash
python scripts/validate_deployment_assets.py
```

Run the same shared preflight locally:

```bash
python scripts/run_preflight.py --browser-required
```

For tag-based binary archives and GHCR image publishing, see
[Release Packaging](./release.md).
