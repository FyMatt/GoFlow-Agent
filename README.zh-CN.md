# GoFlow Agent

[English](./README.md) | [简体中文](./README.zh-CN.md)

GoFlow Agent 是一个用 Go 编写的本地通用 Agent 运行时，用于构建具备工具调用、记忆、规划、工作流、多 Agent 协作和可视化编排能力的领域 Agent。

## 项目定位

GoFlow Agent 把 **runtime home** 和 **workspace root** 分开：

- **runtime home**：GoFlow 框架目录，保存配置、Agent、Skill、MCP server、Workflow、模板和文档。
- **workspace root**：目标工作目录，文件工具只在这里读写，`.goflow/session.json` 等会话状态也保存在这里。

这种设计让你可以在一个固定的 GoFlow 安装目录中运行框架，同时把 Agent 指向任意项目目录。

它适合：

- 在空目录中初始化项目和文档。
- 维护现有代码库。
- 运行带工具调用、审批和持久化状态的 Agent 工作流。
- 通过配置扩展 Agent、Skill、Tool 和 Workflow，快速二开垂直领域 Agent。
- 同时通过 CLI 和 HTTP/Web Studio 使用同一套运行时能力。

完整二开示例见 [examples/extension-workflow](./examples/extension-workflow)。它组合了 Python MCP server、自定义只读 Agent、自定义 Skill 和图工作流。

## 核心能力

- 多 Agent 运行时，支持命名 Agent profile。
- 模块化配置：默认 Agent、Provider、MCP server 可分别放在 `configs/agents/`、`configs/providers/`、`configs/mcp_servers/`。
- 运行时工作流图，支持 `workflows/<name>/workflow.yaml` 持久化存储。
- CLI 品牌启动信息、状态查看、会话查看、工具审批、文件引用补全和工作区确认。
- HTTP JSON API 和 SSE 流式输出，可支撑 Web Studio。
- 普通 Agent run 和 Workflow run 支持后台 durable run、重连、取消、审批和事件回放。
- OpenAI-compatible 模型 Provider，支持 fallback Provider。
- stdio MCP 工具执行，内置 Go 和 Python MCP server，支持跨语言工具开发。
- `SKILL.md` 动态加载，兼容 GoFlow 原生 Skill 和 Claude/Codex 风格 Skill。
- 复杂 Skill 可以声明确定性辅助脚本，并通过 `skill_runner/run_script`
  作为普通 `exec` 工具执行，继续走权限、审批、审计和 MCP 隔离链路。
- 工作区级会话持久化，包含审批、运行、工作流和协作状态。
- `@file` 工作区文件引用，路径解析和文件读取走同一套工具日志与安全边界。
- Docker/Podman `isolation: container` 作为推荐的强 MCP 沙箱方案。
- Windows、Linux、Docker 部署路径。
- GitHub Actions CI、Release、SBOM、签名和 GHCR 镜像发布。

## 快速开始

### 依赖

- Go 1.25+
- 如需使用 Python MCP server，`PATH` 中需要可用的 Python 3
- 如需推荐的强 MCP 沙箱，需要 Docker 或 Podman
- 一个 OpenAI-compatible 模型接口
- 模型 Provider 所需环境变量

### 配置模型环境变量

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL
```

也可以复制 `.env.example`，填入自己的模型地址、密钥和模型名。

Windows 示例：

```bat
set GOFLOW_BASE_URL=https://api.deepseek.com/v1
set GOFLOW_API_KEY=your-api-key
set GOFLOW_MODEL=deepseek-chat
set GOFLOW_BACKUP_BASE_URL=%GOFLOW_BASE_URL%
set GOFLOW_BACKUP_API_KEY=%GOFLOW_API_KEY%
set GOFLOW_BACKUP_MODEL=%GOFLOW_MODEL%

run-goflow.example.cmd D:\Projects\my-workspace
```

### 启动 CLI

使用当前目录作为默认 workspace：

```bash
go run ./cmd/goflow
```

使用外部目录作为 workspace：

```bash
go run ./cmd/goflow --workspace D:/my-project
```

如果没有显式指定 workspace，纯聊天可以直接使用；一旦需要读写文件、执行命令、引用 `@file` 或运行 workflow，CLI 会要求先确认 workspace。

常用命令：

```text
/help
/status
/session
/agents
/skills
/tools
/workflows
/workflow <name> <request>
/workspace status
/workspace confirm
/workspace use <path>
```

### 启动 HTTP/Web Studio

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

浏览器打开：

```text
http://127.0.0.1:8080/console
```

注意：本地 HTTP 服务默认不是 HTTPS。如果浏览器报 `SSL_ERROR_RX_RECORD_TOO_LONG`，通常是访问了 `https://127.0.0.1:8080`，请改用 `http://127.0.0.1:8080`。

## 安装与部署

- [安装与部署](./docs/install.zh-CN.md)
- [部署说明](./docs/deployment.zh-CN.md)
- [发布与校验](./docs/release.zh-CN.md)
- [配置说明](./docs/configuration.zh-CN.md)

## 二开入口

- [架构说明](./docs/architecture.zh-CN.md)
- [MCP 集成](./docs/mcp.zh-CN.md)
- [MCP 工具开发](./docs/mcp-authoring.zh-CN.md)
- [MCP 隔离策略](./docs/mcp-isolation.zh-CN.md)
- [Skill](./docs/skills.zh-CN.md)
- [Skill 开发](./docs/skill-authoring.zh-CN.md)
- [脚手架命令](./docs/scaffolds.zh-CN.md)
- [工作流图](./docs/workflows.zh-CN.md)
- [更新策略](./docs/updates.zh-CN.md)

## Docker 使用

本地构建：

```bash
docker build -t goflow-agent:local .
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  goflow-agent:local
```

发布镜像：

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

容器默认使用：

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

## 安全边界

GoFlow 的文件工具会把所有读写限制在 active workspace root 下，并拒绝路径逃逸和符号链接逃逸。

MCP 工具的推荐强隔离方式是 Docker/Podman：

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-python:<version>
  workspace_mount: ro
  workspace_target: /workspace
  network: disabled
  memory: 256m
  memory_swap: 256m
  cpus: "0.5"
  pids_limit: "64"
  pull_policy: missing
```

原生隔离模式只作为兼容、生命周期清理或资源控制辅助，不应被当作完整跨平台沙箱。

## 发布

推送普通提交会触发 CI：

```bash
git push origin master
```

推送 `v*` 标签会触发 Release：

```bash
git tag v0.1.3
git push origin v0.1.3
```

Release 工作流会构建 Windows/Linux 二进制压缩包、生成 SBOM、签名产物，并发布 Docker 镜像到 GHCR。

## 脚手架模板

`/new-kit <preset> <name> --materialize` 会同时生成 Kit、Agent、Skill、容器化 MCP Tool、Workflow、Workflow Template、Team Template 和 Policy Rule，并让 Workflow 节点之间通过输出/输入引用串联起来。HTTP Studio 可用 `POST /api/resources/kits/scaffolds/{preset}?materialize=1` 调用同样能力。

内置 Kit preset 包括 `software-engineering`、`agent-framework`、`web-security`、`security-research`、`binary-analysis`、`documentation`、`operations-runbook` 和 `customer-support`。其中 `agent-framework` 用于二开 GoFlow 本身：创建新的垂直 Agent、Skill、Tool、Workflow、Team、Policy 或 Kit。

## 当前状态

后端运行时当前基线已完成：CLI、HTTP API、Web Studio 支撑接口、模块化资源、MCP 工具、Skill、Workflow、durable run、审批、工作区生命周期、成本诊断、Docker 优先隔离、CI/Release 发布链路都已具备。

后续重点主要在前端体验完善、复杂工作流产品化、更多垂直模板和真实部署场景打磨。
