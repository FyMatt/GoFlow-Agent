# 安装与部署

[English](./install.md) | [简体中文](./install.zh-CN.md)

这份文档面向想直接运行 GoFlow 的用户，不要求先理解框架内部实现。

常见使用方式：

- **Release 压缩包**：适合 Windows/Linux 本地交互式 CLI。
- **Docker 镜像**：适合 HTTP/API 服务部署和系统集成。
- **源码运行**：适合开发、调试和二开。

## 配置模型凭据

GoFlow 使用 OpenAI-compatible 模型接口。复制环境变量模板并填写 Provider 信息：

```bash
cp .env.example .env
```

必填值：

```env
GOFLOW_BASE_URL=https://api.deepseek.com/v1
GOFLOW_API_KEY=your-api-key
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_BASE_URL=https://api.deepseek.com/v1
GOFLOW_BACKUP_API_KEY=your-api-key
GOFLOW_BACKUP_MODEL=deepseek-chat
```

如果使用本地模型服务，把 `GOFLOW_BASE_URL` 设置为该服务的 OpenAI-compatible `/v1` 地址。

## 使用 Release 压缩包

从 GitHub Releases 下载最新版本：

```text
https://github.com/FyMatt/GoFlow-Agent/releases
```

选择对应平台：

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

压缩包内的启动脚本会使用 `configs/goflow.binary.yaml`，并把内置 Go MCP 工具指向 `bin/` 下的可执行文件。

## 源码运行

```bash
git clone https://github.com/FyMatt/GoFlow-Agent.git
cd GoFlow-Agent
go run ./cmd/goflow --workspace /path/to/workspace
```

启动 HTTP/Web Studio：

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

打开：

```text
http://127.0.0.1:8080/console
```

请使用 `http://`，不是 `https://`。

## Docker

本地构建：

```bash
docker build -t goflow-agent:local .
```

运行：

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  goflow-agent:local
```

使用发布镜像：

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

Docker 镜像默认启动 HTTP 模式：

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

检查服务状态：

```bash
curl http://127.0.0.1:8080/api/session
```

## Docker Compose

仓库内置 `docker-compose.yml`。填写 `.env` 后可以直接启动：

```bash
docker compose up --build
```

Compose 会构建 `goflow-agent:local`，把 `./workspace` 挂载到容器内的
`/workspace`，并在 `http://127.0.0.1:8080/console` 暴露 Web Studio。

## 工作区与会话文件

GoFlow 会把会话状态写入目标工作区：

```text
<workspace>/.goflow/session.json
```

Release 压缩包中的运行时文件位于解压目录；Docker 镜像中的运行时文件位于
`/app`，目标项目文件位于 `/workspace`。

## 发布产物校验

Release 会附带校验和、SBOM 和签名包：

```text
SHA256SUMS
SBOM.spdx.json
.sigstore.json
```

## 运行测试

源码开发时建议运行：

```bash
go test ./...
python scripts/validate_python_mcp.py
python scripts/validate_extension_workflow.py
python scripts/validate_deployment_assets.py
```

## 常见问题

### 提示 provider backup model is required

需要同时设置主模型和备份模型环境变量：

```bash
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_MODEL=deepseek-chat
```

如果两者使用同一个 Provider，可以让 `GOFLOW_BACKUP_*` 与主 Provider 相同。

### 直接运行 bin/goflow 找不到 configs

Release 压缩包中的 `bin/goflow` 会自动识别上级目录作为 runtime home，并默认读取 `configs/goflow.binary.yaml`。如果你移动了可执行文件，请显式指定：

```bash
goflow --config /path/to/goflow/configs/goflow.binary.yaml --workspace /path/to/workspace
```

### 浏览器报 SSL_ERROR_RX_RECORD_TOO_LONG

本地服务默认是 HTTP，不是 HTTPS。请访问：

```text
http://127.0.0.1:8080/console
```

### Python MCP server 不可用

确认 Python 在 `PATH` 中可用：

```bash
python --version
python3 --version
```

Windows 默认使用 `python`，Linux 默认使用 `python3`。可通过 `GOFLOW_PYTHON_CMD` 覆盖。
