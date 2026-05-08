# 配置说明

[English](./configuration.md) | [简体中文](./configuration.zh-CN.md)

## 主配置

`configs/goflow.yaml` 是主要运行时配置文件。

当前推荐结构是：

- `configs/goflow.yaml`：全局运行时设置。
- `configs/agents/*.yaml`：Agent profile。
- `configs/providers/*.yaml`：模型 Provider。
- `configs/mcp_servers/*.yaml`：MCP server。

旧版顶层 `llm` 配置仍可被加载，但新项目建议使用模块化配置。

## 路径模型

GoFlow 区分两类路径：

- **runtime-relative**：相对 runtime home 解析，用于配置、Skill、MCP server、Workflow。
- **workspace-relative**：相对 active workspace root 解析，用于目标项目文件和 session。

示例：

```bash
go run ./cmd/goflow --workspace D:/target-dir
```

此时：

- 配置仍来自 `runtime-home/configs/goflow.yaml`。
- Skill 来自 runtime home 的 `skills/`。
- MCP server 从 runtime home 启动。
- 文件工具只读写 `D:/target-dir`。
- 会话状态保存到 `D:/target-dir/.goflow/`。

## 选择配置文件

```bash
go run ./cmd/goflow --config configs/goflow.docker.yaml --workspace /workspace --http :8080
```

相对 `--config` 从 runtime home 解析，绝对路径直接使用。

## Agent 配置

Agent 建议放在 `configs/agents/<name>.yaml`，避免把所有 Agent 塞进一个大配置文件。

常见字段：

- `name`
- `mode`
- `model`
- `provider`
- `tool_policy`
- `allowed_tool_kinds`
- `allowed_tools`
- `system_prompt`

Chat agent 应尽量保持低权限。需要写文件、执行命令或修改项目时，应通过 routing、workflow 或用户选择切换到 fixer/auditor 等更合适的 Agent。

## Provider 配置

Provider 建议放在 `configs/providers/<name>.yaml`。

GoFlow 使用 OpenAI-compatible 接口，常见环境变量：

```env
GOFLOW_BASE_URL=https://api.deepseek.com/v1
GOFLOW_API_KEY=your-api-key
GOFLOW_MODEL=deepseek-chat
GOFLOW_BACKUP_BASE_URL=https://api.deepseek.com/v1
GOFLOW_BACKUP_API_KEY=your-api-key
GOFLOW_BACKUP_MODEL=deepseek-chat
```

Web Studio 可通过资源 API 创建、编辑、删除 Provider，并同步到配置文件。

## MCP server 配置

MCP server 建议放在 `configs/mcp_servers/<name>.yaml`。

强隔离推荐：

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-python:<version>
  workspace_mount: ro
  workspace_target: /workspace
  network: disabled
  memory: 256m
  cpus: "0.5"
  pids_limit: "64"
```

只要工具是第三方、生成的、写文件、执行命令、访问网络或处理不可信输入，就优先使用 Docker/Podman container isolation。

## 工作区确认

如果没有显式指定 workspace，GoFlow 会把当前执行目录作为默认 workspace，但在以下操作前要求确认：

- 读写文件。
- 执行命令。
- `@file` 引用。
- 运行 workflow。
- 生成文档或项目文件。

CLI 命令：

```text
/workspace status
/workspace confirm
/workspace clear
/workspace use <path>
```

HTTP 会通过 workspace API 暴露同样能力，前端应按后端 guidance 渲染选择、确认、切换和重启提示。

## 资源 API

Web Studio 应优先通过后端资源 API 管理配置，不要直接在浏览器里拼 YAML：

- Agent
- Provider
- Skill
- Tool/MCP server
- Workflow
- Team
- Kit
- Policy rule

保存资源后，后端会进行校验，并在可热加载的场景中刷新 runtime。

Kit scaffold 支持 `multi-domain-agent`、`software-engineering`、`agent-framework`、`web-security`、`security-research`、`binary-analysis`、`documentation`、`operations-runbook` 和 `customer-support`。`multi-domain-agent` 是推荐的新手入口，会把多领域 Agent、Skill、Tool、Team、Workflow Template 和 Kit 串起来；`agent-framework` 是面向二开的 starter，用于生成互相关联的 Agent、Skill、Tool、Workflow、Team、Policy 和 Kit 资源。

## Durable run 与运行成本

普通 Agent 和 Workflow 都支持后台 durable run。浏览器启动任务时应使用后台模式，
然后通过 run id 重连事件流、查看 timeline/replay/diff/export，并通过后端 action
接口执行取消、重试或审批。刷新页面或切换路由不应取消任务。

GoFlow 会记录 prompt budget 和 provider 返回的 token usage。可通过以下接口查看：

```text
GET /api/runtime
GET /api/runtime/cost
```

CLI 可使用：

```text
/cost
```

这些诊断用于观察 system prompt、历史记录、工具 schema、Skill 和消息正文分别消耗多少上下文，以及 provider 是否返回 cached tokens。

`GET /api/runtime` 和 `GET /api/runtime/cost` 还会返回 `cost.tuning[]`，用于低成本路由的实测调优。GoFlow 不会因为某个 helper 模型便宜就默认启用，而是先根据真实 prompt budget 和 token usage 判断是否值得做 A/B 测试。当前会输出 `router` 和 `summarizer` 两类路线：

- `state`：`candidate`、`configured_waiting_for_samples`、`observed_saving`、`observed_costly` 或 `not_enough_data`。
- `observed_samples`、`prompt_budget_samples`、`token_usage_samples`：样本数量。
- `observed_prompt_tokens`、`observed_output_tokens`、`observed_cached_tokens`、`observed_total_tokens`：provider 返回的实际 token 用量。
- `candidate_average_prompt_tokens` 和 `estimated_extra_call_tokens`：判断一次额外 helper 调用是否可能省钱。
- `net_savings_signal`、`quality_checklist`、`recommendation` 和 `action`：给前端展示状态、建议和质量检查项。

`candidate` 只表示“值得配置便宜模型做一次受控实验”，不是自动启用建议；`observed_saving` 说明 token 信号为正，但仍要人工检查质量；`observed_costly` 表示当前样本看起来额外调用不划算，除非质量收益明显，否则应关闭或缩小适用范围。

## 诊断

`/api/config/diagnostics` 提供配置问题、警告、可选扩展状态和建议操作。

前端应区分：

- 阻断性错误。
- 可操作警告。
- 可选目录为空这类 info。
- 需要重启才生效的变更。
