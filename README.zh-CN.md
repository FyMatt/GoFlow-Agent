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

内置案例：

- [examples](./examples)：可复制扩展示例的总览。
- [examples/extension-workflow](./examples/extension-workflow)：一个紧凑的二开扩展示例，组合了 Python MCP server、自定义只读 Agent、自定义 Skill 和图工作流。可用 `python scripts/validate_extension_workflow.py` 做 smoke test。
- [examples/binary-analysis-kit](./examples/binary-analysis-kit)：一个更完整的 materialized kit 示例，把 Agent、Skill、容器化 MCP helper、Workflow、Workflow Template、Team Template 和 Policy Rule 串联成二进制分析流程。可用 `python scripts/validate_binary_analysis_kit.py` 验证。

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

## 内置领域资源

GoFlow 内置了一组可以直接联动的领域资源：

- Agent：`software-engineer`、`security-researcher`、`web-security-researcher`、`binary-analyst`、`documentation-specialist`、`operations-specialist`、`support-specialist`、`framework-extension-architect`
- Skill：`execution-plan`、`code-writing`、`code-audit`、`vulnerability-research`、`web-vulnerability-research`、`binary-vulnerability-research`、`reverse-engineering`
- Kit preset：`multi-domain-agent`、`software-engineering`、`agent-framework`、`web-security`、`security-research`、`binary-analysis`、`documentation`、`operations-runbook`、`customer-support`
- 对应领域的 Workflow Template 和 Team Template，其中 `multi-domain-intake-router` 是推荐的新手入口。内置 Workflow Template 从 `internal/agent/templates/workflows/*.yaml` 内嵌进二进制，运行目录可以通过 `templates/workflows/*.yaml` 覆盖。

可以用 `/kits multi-domain-agent-kit` 或 `/workflow-templates multi-domain-intake-router`
查看 Agent、Skill、Tool、Team、Workflow Template、Policy Rule 和 Kit 如何互相关联。

推荐起步路径：

| 目标 | 从哪里开始 | 会得到什么 |
| --- | --- | --- |
| 通用入门 | `multi-domain-agent-kit` | 一组串联好的 Agent、Skill、Tool、Team、Policy Gate 和 `multi-domain-intake-router` 工作流模板 |
| 软件研发 | `software-engineering-kit` | 规划、实现、审计和质量门禁式代码变更流程 |
| Web 安全 | `web-security-kit` | 页面与前端资源采集、证据化安全审查和风险报告 |
| 二进制初筛 | `binary-analysis-kit` | 静态元数据、字符串、十六进制预览、团队评审和报告交接 |
| 框架二开 | `agent-framework-kit` | 创建新 Agent、Skill、Tool、Workflow、Team、Policy、Kit 的起步资源 |

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

### 使用 Web Studio

HTTP 模式启动后打开：

```text
http://127.0.0.1:8080/console
```

需要分享固定语言入口时，可以带 `?lang=zh` 或 `?lang=en`，例如：

```text
http://127.0.0.1:8080/console?lang=zh#workspace
```

最低门槛的使用顺序：

1. 打开 **Resources**。
2. 选择 `multi-domain-agent` starter kit。
3. 点击 **创建联动起步资源**，一次生成它引用的 Agent、Skill、Tool、Workflow、Workflow Template、Team Template、Policy Rule 和 Kit 文件。
4. 打开 **Workflow Studio**。
5. 从 `multi-domain-intake-router` 开始，先看模板卡片里的节点构成、引用资源和能力标签，再运行或 fork 这个工作流。

Workflow Studio 会展示节点元数据、字段示例、输入输出引用、审批门禁、质量门禁和模板构成信息，用户不需要先猜 YAML 字段怎么填。

**Observability / 观测** 页面会展示运行健康、当前任务焦点、模型成本诊断和 MCP
工具压力。MCP 工具压力面板可以用来判断工具调用是否正在执行、是否排队、某个
tool server 是否已经饱和，再决定是否提高工作流并发或重试卡住的运行。

如果你在 Studio 里不知道某个节点怎么配置、`input` / `outputs` /
`artifacts` / `acceptance_criteria` 应该怎么写，直接看
[工作流图](./docs/workflows.zh-CN.md) 里的“节点配置速查”“对照 Studio 右侧面板填写”和“各节点怎么用”。

如果你不清楚 Agent、Skill、Tool、Workflow、Team Template、Policy Rule、Kit
分别是什么、放在哪里、怎么互相关联，直接看
[资源与设置指南](./docs/resources.zh-CN.md)。

## 安装与部署

- [安装与部署](./docs/install.zh-CN.md)
- [部署说明](./docs/deployment.zh-CN.md)
- [发布与校验](./docs/release.zh-CN.md)
- [配置说明](./docs/configuration.zh-CN.md)
- [资源与设置指南](./docs/resources.zh-CN.md)

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

GoFlow 的文件工具会把所有读写限制在 active workspace root 下，并拒绝路径逃逸和符号链接逃逸。文本文件统一按 UTF-8 读取，UTF-8 BOM 会被自动去除；如果文件不是 UTF-8 文本，就交给二进制分析工具处理。

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

`/new-kit <preset> <name> --materialize` 会同时生成 Kit、Agent、Skill、容器化 MCP Tool、Workflow、Workflow Template、Team Template 和 Policy Rule，并让 Workflow 节点之间通过输出/输入引用串联起来。HTTP Studio 可用 `POST /api/resources/kits/scaffolds/{preset}?materialize=1` 调用同样能力。内置 Workflow Template 资源也已经文件化：默认模板从 `internal/agent/templates/workflows/*.yaml` 内嵌进二进制；运行目录可以通过 `templates/workflows/*.yaml` 新增或覆盖模板。

`plan-fix-audit` 和 `skill-chain` 仍然是可运行的兼容执行器；真正可编辑的是
`workflows/<name>/workflow.yaml` 这种图工作流文件。HTTP 中
`/api/workflow-graphs` 用于列出和编辑图文件，
`/api/workflow-options.workflow_executors` 用于列出所有可运行 workflow 入口。
如果保存了同名且有效的图工作流，它会覆盖对应兼容执行器。

内置 Kit preset 包括 `multi-domain-agent`、`software-engineering`、`agent-framework`、`web-security`、`security-research`、`binary-analysis`、`documentation`、`operations-runbook` 和 `customer-support`。其中 `agent-framework` 用于二开 GoFlow 本身：创建新的垂直 Agent、Skill、Tool、Workflow、Team、Policy 或 Kit。

`binary-analysis` 在 materialize 时会生成静态二进制分析 MCP helper，内置 `binary_file_info`、`binary_strings` 和 `hex_preview`。二进制输入应使用普通路径，例如 `sample.bin`；`@file` 只用于 UTF-8 文本内容。这个 helper 也已经模板化：内置模板位于
`internal/scaffold/templates/tools/python/binary-analysis-server.py.tmpl`，运行目录可以通过
`templates/tools/python/binary-analysis-server.py.tmpl` 覆盖。

Tool scaffold preset 已经文件化。内置 preset 从 `internal/scaffold/templates/tools/scaffolds/presets.yaml` 内嵌进二进制；运行目录可以通过 `templates/tools/scaffolds/*.yaml` 添加或覆盖 preset。
生成的 Python MCP Tool server 和模块化 MCP 配置也已经文件化：内置模板位于
`internal/scaffold/templates/tools/python/server.py.tmpl` 和
`internal/scaffold/templates/tools/python/config.yaml.tmpl`，运行目录可以通过
`templates/tools/python/server.py.tmpl` 和
`templates/tools/python/config.yaml.tmpl` 覆盖。`/new-tool python` 与 Studio
默认 Python Tool 资源使用同一套渲染器。

Kit scaffold preset 已经文件化。内置 preset 从 `internal/scaffold/templates/kits/scaffolds/presets.yaml` 内嵌进发布二进制；运行目录可以通过 `templates/kits/scaffolds/*.yaml` 添加或覆盖 preset。
Materialized kit 的资源模板也已经文件化：默认的 `kit.yaml`、`agent.yaml`、`SKILL.md`、容器 MCP 配置、Workflow、Workflow Template、Team Template、Policy Rule 和 Workflow 说明模板从 `internal/scaffold/templates/kits/materialized/generic/*.tmpl` 内嵌进二进制。运行目录可以通过 `templates/kits/materialized/<preset>/*.tmpl` 覆盖某个 preset，也可以通过 `templates/kits/materialized/generic/*.tmpl` 覆盖通用兜底模板。

Team Template 也已经文件化。内置团队模板从 `internal/agent/templates/teams/*.yaml` 内嵌进二进制；运行目录可以通过 `templates/teams/*.yaml` 新增或覆盖团队模板。同一套目录会被 `/teams`、Workflow 的 `team` 节点校验、Kit materialize 和 Studio 资源表单共同使用。

## 使用案例

查看内置领域包：

```text
/kits binary-analysis-kit
/workflow-templates binary-triage
```

复制完整二进制分析 kit：

```text
examples/binary-analysis-kit
```

运行内置轻量二进制分析链路：

```text
/workflow skill-chain analyze sample.bin for suspicious imports, strings, and likely attack surfaces
```

原始二进制请直接写工作区路径，不要用 `@file`。`@file` 只用于 UTF-8 文本内容。

## 当前状态

当前基线已经可以通过 CLI 和 Web Studio 端到端使用：资源管理、MCP 工具、Skill、Workflow、普通 Agent durable run、Workflow durable run、审批、工作区生命周期、成本诊断、Docker 优先隔离、CI/Release 发布链路都已具备。

内置资源基线已经包含可串联的多领域 Agent、Skill、Tool、Team、Workflow、Policy 和 Kit 模板。Agent/Provider 默认脚手架、Skill scaffold template、Team scaffold preset、Policy Rule scaffold preset、Workflow Node metadata、Workflow Policy Rule metadata 与 Expression Helper metadata 也已经文件化：Agent/Provider 默认模板位于 `internal/scaffold/templates/config/agents/default.yaml.tmpl` 和 `internal/scaffold/templates/config/providers/default.yaml.tmpl`，Skill 内置模板位于 `internal/scaffold/templates/skills/scaffolds/templates.yaml`，Team 内置 preset 位于 `internal/scaffold/templates/teams/scaffolds/presets.yaml`，Policy Rule 内置 preset 位于 `internal/scaffold/templates/policies/scaffolds/presets.yaml`，Workflow Node 内置 metadata 位于 `internal/agent/templates/workflow_nodes/*.yaml`，Workflow Policy Rule 内置 metadata 位于 `internal/agent/templates/policy_rules/*.yaml`，Expression Helper 内置 metadata 位于 `internal/agent/templates/expression_helpers/*.yaml`。运行目录可通过对应 `templates/`、`metadata/` 或 `policies/workflow_rules/` 路径添加或覆盖。

设置页会显示发布更新策略，但不会自动访问 GitHub。用户需要显式点击检查更新按钮，页面才会调用 `POST /api/update-policy/check`，并把当前运行版本和配置的发布源进行比较。

后续重点应由真实部署反馈、更多领域模板沉淀，以及基于实测数据的成本路由和 Docker/Podman 沙箱加固来驱动，而不是补齐缺失的核心运行时能力。
