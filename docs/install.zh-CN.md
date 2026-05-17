# 安装与部署

[English](./install.md) | [简体中文](./install.zh-CN.md)

这份文档面向想直接运行 GoFlow 的用户，不要求先理解框架内部实现。

常见使用方式：

- **Release 压缩包**：适合本地直接启动 Web Studio，也可以从 `bin/` 直接进入 CLI。
- **Docker 镜像**：适合 HTTP/API 服务部署和系统集成。
- **源码运行**：适合开发、调试和二开。

## 配置模型凭据

GoFlow 可以在模型 Provider 尚未完整配置时先启动。这是为了让新用户先打开 CLI 或
Web Studio，再到资源或设置页面填写 Provider 信息。真正发起 Agent/模型调用时仍然
需要 `base_url`、`api_key` 和 `model`；未配置完整时会返回明确的配置提示，而不是让
启动失败。

你可以在 Web Studio 中直接配置 Provider，也可以编辑 `configs/providers/*.yaml`，
或继续使用环境变量。使用环境变量时，可以复制模板：

Web Studio 会把 Provider 资源保存到 `configs/providers/<name>.yaml`。如果直接在
页面里填写字面量 API Key，它会以明文形式落到这个 YAML 文件里。共享仓库和发布版
建议在已提交的 Provider 文件中保留 `${GOFLOW_API_KEY}`，真实值通过 `.env`、当前
shell 或本地密钥存储加载。

```bash
cp .env.example .env
```

模型调用所需值：

```env
GOFLOW_BASE_URL=https://api.deepseek.com/v1
GOFLOW_API_KEY=your-api-key
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_BASE_URL=https://api.deepseek.com/v1
GOFLOW_BACKUP_API_KEY=your-api-key
GOFLOW_BACKUP_MODEL=deepseek-chat
```

如果使用本地模型服务，把 `GOFLOW_BASE_URL` 设置为该服务的 OpenAI-compatible `/v1` 地址。
如果只是先打开 Web Studio 再配置，这些值启动时可以暂时留空。

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

发布包启动脚本默认启动 HTTP/Web Studio。看到 ready 信息后打开 `http://127.0.0.1:8080/console`。如果想进入交互式 CLI，直接运行 `.\bin\goflow.exe`。

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

发布包启动脚本默认启动 HTTP/Web Studio。看到 ready 信息后打开 `http://127.0.0.1:8080/console`。如果想进入交互式 CLI，直接运行 `./bin/goflow`。

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

如果还没有创建 `.env`，可以先去掉 `--env-file .env` 启动 Web Studio；模型调用会在
Provider 未配置完整时提示你补全设置。

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

这个 JSON 文件是轻量索引。完整运行详情会保存在同目录的
`session.full.json.gz` 压缩归档中，运行时会优先读取归档，因此 Web Studio 可以
快速轮询状态，同时保留完整历史。

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

这通常来自旧配置或旧版本。当前 GoFlow 可以在 Provider 未完整配置时先启动，并在
模型调用时提示补全配置。请优先到 Web Studio 的资源或设置页面检查 Provider。

如果你仍在使用带 `fallback_provider` 的旧配置，需要同时设置主模型和备份模型，或
移除尚未配置好的 `fallback_provider`：

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
