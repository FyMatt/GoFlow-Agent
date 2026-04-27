# 安装与部署

这份文档面向直接使用 GoFlow 的用户，不要求先理解框架内部实现。

常见使用路径：

- **Release 压缩包**：适合 Windows/Linux 本地交互式 CLI 使用。
- **Docker 镜像**：适合 HTTP/API 服务部署和系统集成。
- **源码运行**：适合二开、调试、自定义 agent/tool/skill/workflow。

## 配置模型凭据

GoFlow 使用 OpenAI-compatible 模型接口。可以复制环境变量模板：

```bash
cp .env.example .env
```

至少需要配置：

```env
GOFLOW_BASE_URL=https://api.deepseek.com/v1
GOFLOW_API_KEY=your-api-key
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_BASE_URL=https://api.deepseek.com/v1
GOFLOW_BACKUP_API_KEY=your-api-key
GOFLOW_BACKUP_MODEL=deepseek-chat
```

如果使用本地模型服务，把 `GOFLOW_BASE_URL` 改成本地服务的
OpenAI-compatible `/v1` 地址。

## 使用 Release 压缩包

从 GitHub Releases 下载最新版本：

```text
https://github.com/FyMatt/GoFlow-Agent/releases
```

按平台选择：

- Windows x64：`goflow-agent_<version>_windows_amd64.zip`
- Windows ARM64：`goflow-agent_<version>_windows_arm64.zip`
- Linux x64：`goflow-agent_<version>_linux_amd64.tar.gz`
- Linux ARM64：`goflow-agent_<version>_linux_arm64.tar.gz`

### Windows

解压 zip 后运行：

```powershell
$env:GOFLOW_BASE_URL="https://api.deepseek.com/v1"
$env:GOFLOW_API_KEY="your-api-key"
$env:GOFLOW_MODEL="deepseek-chat"
$env:GOFLOW_BACKUP_BASE_URL=$env:GOFLOW_BASE_URL
$env:GOFLOW_BACKUP_API_KEY=$env:GOFLOW_API_KEY
$env:GOFLOW_BACKUP_MODEL=$env:GOFLOW_MODEL

.\run-goflow.cmd D:\Projects\my-workspace
```

### Linux

解压 tarball 后运行：

```bash
tar -xzf goflow-agent_<version>_linux_amd64.tar.gz
cd goflow-agent_<version>_linux_amd64

export GOFLOW_BASE_URL=https://api.deepseek.com/v1
export GOFLOW_API_KEY=your-api-key
export GOFLOW_MODEL=deepseek-chat
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL

./run-goflow.sh /path/to/workspace
```

Release 压缩包使用 `configs/agent.binary.yaml`。启动脚本会自动设置编译好的
MCP 工具路径，所以目标机器不需要安装 Go。内置 Python MCP server 仍需要目标机器有 Python。

## 使用发布的 Docker 镜像

Release workflow 会把镜像发布到 GitHub Container Registry：

```text
ghcr.io/fymatt/goflow-agent:<version>
ghcr.io/fymatt/goflow-agent:latest
```

创建 workspace 并启动 HTTP 服务：

```bash
mkdir -p workspace
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

PowerShell：

```powershell
mkdir workspace
docker run --rm -it `
  --env-file .env `
  -p 8080:8080 `
  -v "${PWD}\workspace:/workspace" `
  ghcr.io/fymatt/goflow-agent:<version>
```

镜像默认启动 HTTP 模式：

```text
goflow --config /app/configs/agent.docker.yaml --workspace /workspace --http :8080
```

检查服务：

```bash
curl http://127.0.0.1:8080/api/session
```

发送请求：

```bash
curl -s -X POST http://127.0.0.1:8080/api/run \
  -H "Content-Type: application/json" \
  -d '{"input":"总结一下当前 workspace"}'
```

流式接口：

```bash
curl -N -X POST http://127.0.0.1:8080/api/run/stream \
  -H "Content-Type: application/json" \
  -d '{"input":"hello"}'
```

## 使用 Docker Compose

在源码仓库中运行：

```bash
cp .env.example .env
mkdir -p workspace
docker compose up --build
```

Compose 会构建 `goflow-agent:local`，把 `./workspace` 挂载为 `/workspace`，
并监听 `http://127.0.0.1:8080`。

## 从源码运行

开发框架或添加 tool、agent、skill、workflow 时使用这种方式：

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL

go run ./cmd/goflow --workspace /path/to/workspace
```

HTTP 模式：

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

## Workspace 和 Session 文件

GoFlow 会区分运行时文件和目标项目文件：

- Docker 运行时文件在 `/app`。
- Docker 目标项目文件在 `/workspace`。
- Release 压缩包运行时文件在解压目录中。
- 目标项目路径由 `run-goflow.cmd`、`run-goflow.sh` 或 `--workspace` 指定。

Session 状态保存在 workspace 内：

```text
<workspace>/.goflow/session.json
```

所有内置文件类工具都应限制在当前 workspace root 下执行。

## 校验发布产物

GitHub Release 包含：

- 各平台压缩包
- `SHA256SUMS`
- `SBOM.spdx.json`

`SHA256SUMS` 用于校验下载文件完整性。`SBOM.spdx.json` 记录该版本包含的
Go module 依赖，方便供应链审计。

Release signing 还未接入，后续会补充。
