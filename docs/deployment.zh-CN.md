# 部署说明

[English](./deployment.md) | [简体中文](./deployment.zh-CN.md)

GoFlow 支持 Windows、Linux 和 Docker 部署。对于 MCP 工具沙箱，推荐使用 Docker/Podman `isolation: container` 作为跨平台强边界。

## Windows 本地运行

设置模型凭据后运行：

```bat
set GOFLOW_BASE_URL=https://api.deepseek.com/v1
set GOFLOW_API_KEY=your-key
set GOFLOW_MODEL=deepseek-chat
set GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%
set GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%
set GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%

run-goflow.example.cmd D:\Projects\my-workspace
```

## Linux 本地运行

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL

go run ./cmd/goflow --workspace /path/to/workspace
```

启动 HTTP/Web Studio：

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

浏览器访问：

```text
http://127.0.0.1:8080/console
```

## MCP 容器隔离

当 MCP 工具具备写文件、执行命令、访问网络、处理不可信输入、由用户生成或来自第三方时，建议使用容器隔离：

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

GoFlow 不会在 Docker/Podman 缺失时静默降级到宿主机执行。

## Docker

构建：

```bash
docker build -t goflow-agent:local .
docker build -f docker/mcp-python/Dockerfile -t goflow-agent-mcp-python:local .
```

运行：

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  goflow-agent:local
```

Compose：

```bash
docker compose up --build
```

使用发布镜像：

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

容器默认启动：

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

## 配置选择

```bash
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

Release 压缩包使用 `configs/goflow.binary.yaml`。如果直接运行 `bin/goflow`，程序会识别上级压缩包根目录并读取该配置。

## 数据卷

目标项目应挂载到 `/workspace`：

```text
/workspace/.goflow/session.json
```

runtime 文件保留在 `/app`，目标项目文件保留在 `/workspace`，这样工具边界更清楚。

## CI 检查

仓库的 CI 包含：

- 共享预检入口：`python scripts/run_preflight.py --browser-required`
- `go test ./...`
- Python MCP smoke test
- 内置 Agent、Skill、Tool、Kit、Team、Policy Rule、Workflow Template、Skill
  交接、Team 角色和 Workflow 阶段引用校验
- Web Studio 中英文静态翻译键校验
- 公开 Markdown 链接、中英文配对和内部计划文件引用校验
- 嵌入式 HTTP Studio 冒烟测试，覆盖 `/console`、`/workflows`、静态资源和关键 JSON API
- CI 中强制执行真实浏览器 Studio 冒烟测试，会渲染
  `/console`、`/workflows`、`/console#playground`、`/console#approvals`、
  `/console#catalog`、`/console#status`、`/console#workspace`、
  `/console#settings`，以及中文 `?lang=zh` 深链；本地可不加 `--required`，没有
  Chrome、Edge 或 Chromium 时会跳过
- 部署资源校验
- Docker 镜像构建
- 容器启动检查

本地静态校验：

```bash
python scripts/validate_deployment_assets.py
```

本地运行同一套共享预检：

```bash
python scripts/run_preflight.py --browser-required
```
