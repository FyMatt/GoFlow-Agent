# 架构说明

[English](./architecture.md) | [简体中文](./architecture.zh-CN.md)

## 设计原则

GoFlow Agent 的架构围绕几条规则展开：

1. 运行时由小而明确的原语组成。
2. 把 Agent 推理、工具执行、会话状态和审计日志分开。
3. 工具权限必须可见、可解释、可执行。
4. CLI、HTTP 和后续传输层复用同一套运行时契约。
5. 明确区分 **runtime home** 和 **workspace root**。
6. Web Studio 是 CLI 能力的可视化实现，而不是另一套独立运行时。
7. 读取工作区文本文件时统一按 UTF-8 处理；可以接受 UTF-8 BOM 并自动去除，非 UTF-8 字节应交给二进制分析工具，而不是文本读取器。

## runtime home 与 workspace root

- **runtime home**：GoFlow 框架安装目录，包含 `configs/goflow.yaml`、`configs/agents/`、`configs/providers/`、`configs/mcp_servers/`、`skills/`、`mcp_servers/` 和 `workflows/`。
- **workspace root**：目标项目目录。文件工具、`@file` 引用、会话状态和任务产物都以这里为边界。

这样可以让 GoFlow 安装一次，指向多个不同项目使用。

## 主要模块

### `cmd/goflow`

CLI 入口，负责：

- 解析 runtime home、config 和 workspace。
- 启动共享 runtime app。
- 渲染 CLI 流式事件、启动 banner、状态输出和审批菜单。
- 处理 `/` 命令、`@file` 引用、Tab 补全、工作区确认和工作区切换。

### `internal/app`

CLI、HTTP 和未来传输层共享的启动层，负责：

- 加载配置。
- 初始化 Provider、Skill、MCP、Session、Audit 和 Agent runtime。
- 绑定 runtime home 与 workspace root。

### `internal/agent`

Agent 请求生命周期和多 Agent 协作核心，负责：

- 选择 active agent。
- 匹配 Skill。
- 构建系统提示词。
- 执行 LLM/工具循环。
- 发出流式事件。
- 执行 built-in workflow 和 graph workflow。
- 从 `internal/agent/templates/workflows/*.yaml` 和 `internal/agent/templates/teams/*.yaml` 加载内置 Workflow/Team 模板，并允许 runtime home 用 `templates/workflows/*.yaml`、`templates/teams/*.yaml` 覆盖。
- 处理审批、取消、重试、durable run、输出传递、控制流和协作状态。

### `internal/api`

HTTP/Web Studio 后端，负责：

- `/api/run` 与 `/api/run/stream`
- durable Agent run 与 Workflow run
- 资源管理：Agent、Provider、Skill、Tool、Workflow、Team、Kit
- 配置诊断、运行时状态、工作区生命周期、文件引用搜索
- SSE 事件流、审批、取消、重试和事件回放

### `internal/mcp`

MCP server 管理层，负责：

- stdio MCP 进程生命周期。
- 工具发现、缓存、路由和健康状态。
- 启动命令白名单。
- 每个 server 的超时、请求/响应大小上限、`max_concurrent_calls` 队列上限、
  `restart_limit` 和 `cooldown` 轻量熔断。
- Docker/Podman container isolation。
- 原生 fallback 隔离模式和风险诊断。

### `internal/skill`

加载 `skills/<name>/SKILL.md`，兼容 GoFlow 原生 Skill 与 Claude/Codex 风格 Skill。

### `internal/session`

保存 workspace-scoped 状态，包括：

- 对话历史。
- pending approval。
- ordinary Agent durable run。
- Workflow durable run。
- artifacts、diffs、协作和 team 状态。

`.goflow/session.json` 是轻量索引，服务于快速启动状态和 Web Studio 轮询。
完整运行详情、工具输出和阶段结果保存在 `.goflow/session.full.json.gz` 压缩归档中。
加载时优先读取完整归档；旧版大 `session.json` 会自动迁移为“轻量索引 + 完整压缩归档”。

## 运行时数据流

1. 用户通过 CLI 或 HTTP 发送请求。
2. Runtime 根据 routing、active agent、skill 和 workflow 判断执行路径。
3. Agent 构建提示词并调用模型 Provider。
4. 模型请求工具时，Agent 进入策略和审批流程。
5. MCP manager 调用目标工具。
6. 工具结果回到 Agent，Agent 继续推理。
7. 结果、工具调用、审批、token 和状态以统一事件模型输出。
8. Session 持久化关键状态，支持刷新、重连和恢复。

## 工作流生命周期

GoFlow 会区分“可编辑图资源”和“可运行 workflow 入口”：

- `/api/workflow-graphs` 只暴露 runtime home 中持久化的图工作流文件，适合
  Web Studio 编辑和保存。
- `/api/workflow-options.workflow_executors` 暴露可运行入口，包括有效的图工作流
  和 `plan-fix-audit`、`skill-chain` 这类兼容执行器，适合聊天页和运行页做
  workflow 下拉框。

如果保存了一个与兼容执行器同名且有效的图工作流，它会覆盖兼容执行器；如果同名
图存在但无效，兼容执行器会被阻断并暴露图错误，避免用户以为运行的是旧逻辑。

## Web Studio

Web Studio 应当被视为 CLI 能力的图形化入口。新增后端能力时，优先考虑：

- 是否有 HTTP API。
- 是否有能力发现字段。
- 是否有配置诊断。
- 是否有前端可渲染的 schema 或 contract。
- 是否能和 CLI 共享同一套 runtime 逻辑。

## 当前边界

- MCP transport 当前以 stdio 为主。
- Docker/Podman 是推荐强沙箱；原生模式只作为辅助。
- Skill 匹配以关键词为主，后续可扩展为更强的检索和路由。
- 更新检查应由用户显式开启，因为它需要访问 GitHub 网络。
