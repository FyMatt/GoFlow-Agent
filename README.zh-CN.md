# GoFlow Agent

[English](./README.md) | [简体中文](./README.zh-CN.md)

GoFlow Agent 是一个本地 Agent 运行时，用 Go 编写，用于构建领域 Agent、多 Agent workflow、MCP 工具、动态 skill、按 workspace 隔离的 session 状态，以及可选的 HTTP/SSE 接口。

## 项目定位

GoFlow Agent 将 **runtime home** 和 **workspace root** 分开：

- runtime home 保存框架配置、agent、skill、MCP server、workflow 等运行时资源
- workspace root 是被 Agent 读取、修改、分析的目标项目目录

这使它适合：

- 在空目录中初始化项目
- 生成或维护文档
- 运行带工具调用的 Agent workflow
- 通过新增 skill 和 MCP tool 构建领域 Agent
- 用 CLI 或 HTTP 复用同一套运行时

完整二开示例见 [examples/extension-workflow](./examples/extension-workflow)，其中包含 Python MCP server、自定义只读 agent、自定义 skill 和 graph workflow。

可以用 `python scripts/validate_extension_workflow.py` 对这个示例做 smoke test。

部署文件可以用 `python scripts/validate_deployment_assets.py` 做本地静态检查，不需要安装 Docker。

## 核心能力

- 多 Agent 运行时，支持命名 agent profile
- 内置 workflow 注册表，并支持 runtime home 下的自定义 workflow graph
- CLI 启动 banner 展示 runtime/workspace/agent/mode/policy 等信息
- 交互式 CLI 中尽力设置 terminal title
- workflow 审批支持方向键加 Enter，并保留数字快捷输入
- OpenAI-compatible provider，支持 fallback provider
- 基于 stdio 的 MCP 工具执行
- 内置 Go 和 Python MCP server，支持跨语言工具开发
- 从 `SKILL.md` 动态加载 skill
- 按 workspace 持久化 session，包含 workflow 和待审批快照
- 审计日志、CLI trace、`/status`、`/session`
- 通过 `--workspace` 指向外部目标目录
- 支持 Windows、Linux 和 Docker 部署路径
- 通过 `--http` 暴露 HTTP JSON API 和 SSE 流式接口

## 快速开始

### 依赖

- Go 1.25+
- 如需使用 `python_notes` MCP server，`PATH` 中需要可用的 Python 3
- 一个 OpenAI-compatible 模型接口
- provider 所需的环境变量

### 配置环境变量

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL
```

也可以复制 `.env.example`，填入自己的模型地址、密钥和模型名。

Windows 下可以在终端中设置密钥，然后使用启动脚本：

```bat
set GOFLOW_API_KEY=your-deepseek-key
run-goflow.example.cmd
```

可以编辑 `run-goflow.example.cmd`，把 `--workspace` 指向自己的目标项目目录。该脚本提供 DeepSeek 默认值，但不会把凭据写进仓库。

### 启动 CLI

使用当前目录作为 workspace：

```bash
go run ./cmd/goflow
```

使用外部目录作为 workspace：

```bash
go run ./cmd/goflow --workspace D:/my-empty-project
```

交互式 CLI 启动时会显示紧凑的品牌 banner，包含 runtime/workspace 路径、当前 agent、mode、工具策略、允许的工具类型、显式工具白名单、workflow 摘要、待执行 handoff、待审批数量，以及当前 workspace 中已记住的工具审批。

`/agents` 会展示每个 agent profile 的 mode、tool policy、允许的工具类型和 `allowed_tools` 白名单，便于用户理解为什么某个工具能否被调用。

HTTP server 模式只输出普通启动行，不使用交互式 banner。

### 启动 HTTP API

```bash
go run ./cmd/goflow --http :8080
```

也可以同时指定外部 workspace：

```bash
go run ./cmd/goflow --workspace D:/my-empty-project --http :8080
```

启动后会输出 runtime、workspace 和监听地址：

```text
GoFlow HTTP API ready. runtime=C:\path\to\agent workspace=D:\my-empty-project addr=:8080
```

### 使用 Docker 运行

复制 `.env.example` 为 `.env`，填入模型配置后运行：

```bash
docker compose up --build
```

容器会监听 `http://127.0.0.1:8080`，并把宿主机的 `./workspace` 挂载为容器内的 `/workspace`。Windows、Linux 和 Docker 细节见 [Deployment](./docs/deployment.md)。

## 常用 CLI 命令

CLI 命令统一使用 `/` 前缀。

```text
/help
/skills
/tools
/agents
/use <agent>
/mode <chat|plan|audit|fix>
/workflow plan-fix-audit [--approve] <request>
/workflow skill-chain <request>
/workflow <custom-name> <request>
/status
/session
/trace on|off
/approve <tool-call-id>
/deny <tool-call-id>
/reload
/reload-tools
/skill-templates
/new-skill <template> <name>
/new-tool python <name>
/new-agent <name>
/new-workflow <name>
exit
```

`--approve` 用于预先批准 planner -> fixer 阶段切换。不带该参数时，GoFlow 会先询问是否进入 fixer 阶段；如果 fixer 调用的工具仍需确认，还会再进入工具级审批。

`/new-workflow <name>` 会创建 `workflows/<name>/workflow.yaml`。该文件可通过 `/workflow <name> <request>` 执行，并支持声明 `agent`、`skill`、可选 `approval`、可选 `next_strategy` 和可选 `next` 边。

不带参数运行 `/workflow` 会显示用法和已发现的自定义 workflow。

## Workflow 审批

当 `/workflow plan-fix-audit ...` 暂停时，CLI 可能停在两个审批边界：

- 进入 fixer 阶段前的阶段审批
- fixer 工具调用被挂起后的工具审批

两种审批都会阻塞当前 workflow。如果一次 workflow 同时遇到这两层审批，GoFlow 会停留在审批流程中，直到每一步都被明确处理。

如果用户选择 workflow 级别的“批准当前 workflow 中所有待处理工具调用”，后续匹配同一 workflow、同一 stage、同一 tool 作用域的调用会继续自动批准。该作用域不会泄漏到其他 workflow、stage 或 tool。

交互式终端中的审批提示支持：

- `Up` / `Down`：在审批动作之间移动
- `Enter`：确认当前高亮动作
- `1`、`2`、`3`：直接选择对应动作

可选动作：

- 同意当前工具调用
- 拒绝当前工具调用
- 同意当前 workflow 中所有待处理工具调用

当 stdin/stdout 不是交互式终端时，GoFlow 会回退到数字 `1/2/3` 提示，兼容重定向场景和简单终端。

workflow 暂停期间：

- `/status` 高亮当前 workflow 状态、下一阶段，以及每个待审批项的工具、agent、阶段和参数摘要
- `/session` 展示结构化的 `Workflow` 和 `Pending approvals` 区块
- 最新 workflow 快照会保存当前请求、摘要、下一阶段和最近审批提示/结果

当挂起的 fixer 工具调用获批并恢复 workflow 时，GoFlow 会把原本中断时的 assistant/tool-call 上下文连同已批准的 tool result 一起重新注入 fixer 对话，而不是从新 prompt 重新开始。

## 普通对话路由与计划确认

默认配置使用 `chat` agent 启动普通 CLI 会话，不会直接进入 planner 模式。

对于自然语言请求，runtime 会在明确需要计划、实现或审计时路由到 planner、fixer 或 auditor。runtime 仍然是最终权限边界：workflow 状态、待审批项和生命周期约束都可以阻止不安全切换。

如果普通 chat 先产出计划并等待批准执行，GoFlow 会在 session 中记录一个待执行 handoff。用户回复 `可以`、`继续`、`就按这个执行`、`全部添加`、`yes` 或 `go ahead` 等确认语句时，会切换到目标可执行 agent，并从已批准计划继续，而不是再次进入 planner 输出循环。

handoff 待处理期间：

- 重启后的启动 banner 可以展示待处理 handoff 摘要
- `/session` 会展示结构化的 `Pending handoff` 区块
- 真正开始写文件或执行命令后，仍然遵循工具审批流程

## HTTP API

当前端点：

- `POST /api/run`
- `POST /api/run/stream`
- `GET /api/session`
- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`
- `POST /api/approvals/{callID}/approve`
- `POST /api/approvals/{callID}/approve/stream`
- `POST /api/approvals/{callID}/deny`
- `POST /api/approvals/{callID}/deny/stream`
- `POST /api/approvals/approve-all`

### 示例：最终 JSON 响应

```bash
curl -s -X POST http://127.0.0.1:8080/api/run \
  -H 'Content-Type: application/json' \
  -d '{"input":"summarize this workspace"}'
```

### 示例：SSE 流

```bash
curl -N -X POST http://127.0.0.1:8080/api/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"input":"hello"}'
```

SSE endpoint 会直接转发 runtime 的 `schema.StreamEvent`，用事件类型作为 SSE `event:` 字段，并把 JSON 编码后的事件写入 `data:` 字段。

SSE 客户端应处理这些事件：

- `text`、`status`、`done`、`final_message`：模型输出和回合完成
- `task_stage`：inspect、plan、modify、verify、summarize 等高层阶段变化
- `tool_call`、`tool_result`、`approval`：工具执行和审批暂停
- `token_usage`：provider 返回的 prompt/output/cache token 数量
- `workflow_result`：workflow 最终或暂停状态

流式审批端点可以在工具调用获批或拒绝后恢复普通 chat 或 workflow 执行。

## 内置 MCP 工具

- `file_tools/list_dir`
- `file_tools/list_tree`
- `file_tools/file_info`
- `file_tools/read_file`
- `file_tools/search_files`
- `file_tools/write_file`
- `file_tools/delete_file`
- `web_tools/web_search`
- `web_tools/fetch_url`
- `web_tools/fetch_page_assets`
- `python_notes/read_note`
- `python_notes/write_note`
- `python_notes/python_ast_summary`
- `python_notes/json_query`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

文件、笔记、源码检查和二进制检查工具都会把路径解析限制在当前 workspace root 下，并拒绝路径穿越或符号链接逃逸。写操作会输出类似 git 的 CLI 摘要，包括状态字母、新增/删除行数、变更行范围、字节数和紧凑彩色 diff hunk。

交互式 CLI 中输入 `@` 会显示 workspace 文件建议；继续输入前缀后按 `Tab` 可以补全唯一匹配或显示候选。可以在请求中使用 `@relative/path.ext` 引用文件；GoFlow 会通过普通的 `file_tools/read_file` 路径读取该文件，打印同样的工具日志，并在请求发送给 agent 前附加文件内容。非交互输入中，单独输入 `@prefix` 可以列出匹配引用。

交互式 CLI 还支持 `/` 命令的 `Tab` 补全，并会消费箭头、Home、End、Delete 等终端转义序列，避免它们变成 `[A`、`[B`、`[H` 或 `[F` 这类文本。

## 使用示例

### 检查项目

```text
Please read the README and summarize the current project structure.
```

### 运行分阶段 workflow

```text
/workflow plan-fix-audit improve workflow error handling
```

### 在空 workspace 中生成文件

```bash
go run ./cmd/goflow --workspace D:/scratch-project
```

然后输入：

```text
Please generate an initial README and docs/overview.md for this empty project.
```

## 文档

- [Architecture](./docs/architecture.md)
- [Configuration](./docs/configuration.md)
- [Deployment](./docs/deployment.md)
- [MCP Integration](./docs/mcp.md)
- [MCP Isolation Strategy](./docs/mcp-isolation.md)
- [MCP Tool Authoring](./docs/mcp-authoring.md)
- [Scaffold Commands](./docs/scaffolds.md)
- [Skills](./docs/skills.md)
- [Skill Authoring](./docs/skill-authoring.md)
- [Workflow Graphs](./docs/workflows.md)

## 当前状态

框架已经端到端可用，包括：

- 外部 workspace 执行
- Windows、Linux 和 Docker 部署路径
- 内置 Go 和 Python MCP server
- 基于注册表的 workflow 执行和审批感知 session 状态
- CLI `/status` 和 `/session` 检查
- HTTP JSON 和 SSE runtime transport
- 不在仓库中内置凭据的 Windows 启动脚本
