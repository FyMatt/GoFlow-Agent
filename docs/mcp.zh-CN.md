# MCP 集成

[English](./mcp.md) | [简体中文](./mcp.zh-CN.md)

## 当前范围

GoFlow 当前支持基于 stdio 的 MCP server。

MCP 层主要由两部分组成：

- `internal/mcp/client.go`：进程生命周期、JSON-RPC I/O、环境变量注入和重启行为。
- `internal/mcp/manager.go`：server 注册、工具缓存和工具路由。

## Manager 能力

运行时主要使用：

- `ListTools`
- `RefreshTools`
- `CallTool`
- `HealthStatus`
- `ToolNames`

工具发现会缓存结果，`/reload-tools` 或 HTTP 资源刷新会显式重建缓存和路由索引。

## 命名与路由

工具可以用短名或带 server 名的完整名调用：

```text
read_file
file_tools/read_file
```

发现工具时会校验：

- `name` 非空且匹配 `[A-Za-z0-9_-]+`
- `kind` 是 `read`、`write`、`exec`、`network` 或 `unknown`
- `input_schema` 是严格 object schema
- 同一个 server 内工具名不重复

## runtime home 与 workspace

MCP server 从 runtime home 启动，同时通过环境变量获得 active workspace：

```text
GOFLOW_WORKSPACE_ROOT=<workspace path>
```

这意味着：

- server 代码保存在 GoFlow 框架目录。
- server 读写目标项目时只能在 workspace root 下。
- 可以把 GoFlow 指向外部空目录，而不用复制 runtime 资源。

## 内置 `file_tools`

工具：

- `read_file`
- `write_file`
- `delete_file`
- `list_dir`
- `list_tree`
- `file_info`
- `search_files`

特性：

- 所有路径解析到 workspace root 下。
- 拒绝路径逃逸和符号链接逃逸。
- 写文件会创建父目录。
- 删除目录必须显式传 `recursive=true`。
- 文本读取统一按 UTF-8 解码，遇到 UTF-8 BOM 会自动去除。
- 返回结构化 JSON。
- CLI 写入日志显示类似 git 的状态、行数和 diff 摘要。

## 内置 `web_tools`

工具：

- `web_search`
- `fetch_url`
- `fetch_page_assets`
- `browser_snapshot`
- `browser_probe_points`

特性：

- 工具类型标记为 `network`。
- 支持搜索、抓取 URL、抓取网页及其 JS/CSS 资源。
- 仅允许 HTTP(S) URL。
- 适合 Web 代码分析和安全研究前置采集。
- `browser_snapshot` 和 `browser_probe_points` 要求 `authorized_scope=true`
  和非空 `allowed_hosts`，目标主机必须在白名单内。
- `browser_probe_points` 默认只枚举 GET 查询、GET 表单和同源链接等探测面；
  只有 `active_probe_approved=true` 时才会运行受限 GET canary 探测。
- 主动探测必须保持非破坏性、受限速率、可审计，并把证据摘要或截图哈希作为
  artifact 引用交给后续审计阶段。

## 内置 `network_tools`

工具：

- `device_discovery_plan`
- `device_command_plan`
- `device_config_dry_run`
- `device_restconf_live_apply`

特性：

- 工具类型标记为 `network`。
- 接收设备 IP 或主机名、平台、连接方式、凭据引用、授权标记、主机白名单和命令白名单。
- 要求 `authorized_scope=true`，并拒绝不在 `allowed_hosts` 内的目标。
- 生成只读发现计划、命令执行计划和配置变更干跑计划。
- 校验命令是否在操作者提供的白名单内。
- 配置变更规划必须带回滚、预检、后检、审批和审计证据要求。
- 不会连接设备，也不会下发配置。

`network_tools` 是运维领域默认的网络设备边界。它是规划与策略检查工具，不是原始
SSH/API 执行器。未来如果增加真实设备连接器，仍应沿用同一套契约：授权范围、
允许主机、凭据引用、命令白名单、干跑 diff、审批信息、回滚计划和变更后证据引用。

## 内置 `python_notes`

工具：

- `write_note`
- `read_note`
- `python_ast_summary`
- `json_query`
- `binary_file_info`
- `binary_strings`
- `hex_preview`
- `binary_format_summary`
- `binary_entropy_map`
- `binary_symbol_hints`
- `binary_extract_window`
- `markdown_link_check`
- `support_case_summary`
- `redaction_check`

这个 server 用于证明 MCP 工具可以跨语言开发。只要进程能按 stdio/JSON-RPC 协议通信，就不要求工具用 Go 编写。

这些工具保持工作区边界和只读默认：二进制工具提供格式/section 摘要、import/export 线索、entropy/packing hints、symbol hints 和 bounded artifact window；`markdown_link_check` 做本地 Markdown 链接与 anchor 检查且不联网；`support_case_summary` 结构化工单优先级、客户影响、升级和 privacy flags；`redaction_check` 用于发布、支持回复和安全报告前的脱敏检查。

## 内置 `skill_runner`

工具：

- `run_script`

这个 server 用于执行 Skill 在 `SKILL.md` 的 `scripts:` 中声明的确定性辅助脚本。它会以
`skill_runner/run_script` 的形式暴露为普通 `exec` 工具，因此脚本执行仍会经过
Agent 权限、审批、审计、风险展示和 MCP 隔离。

特性：

- 从 `--skills-dir` 或 `GOFLOW_SKILLS_DIR` 加载 Skill。
- 拒绝带路径特征的 skill/script 名称。
- 只执行 `SKILL.md` 中按名称声明过的脚本。
- 脚本路径必须位于该 Skill 的 `scripts/` 目录内。
- 根据声明的 `args_schema` 校验参数。
- 通过 stdin 和 `GOFLOW_SKILL_ARGS_JSON` 传入 JSON 参数。
- 返回 stdout、退出码、耗时、是否超时和声明元数据。

如果要执行生成脚本或第三方脚本，应在这个 MCP server 上使用
`isolation: container`，让脚本在 Docker/Podman 沙箱中运行。

## 可靠性控制

每个 MCP server 都支持单次调用超时、请求/响应大小上限、`restart_limit`、
`cooldown` 和 `max_concurrent_calls`。当前 stdio transport 是单连接模型，所以默认
`max_concurrent_calls: 1`，用于串行化请求并避免大量调用无限堆积。

`restart_limit` 加 `cooldown` 是轻量熔断层：server 连续启动或调用失败后会进入冷却，而不是无限重启。

`GET /api/runtime` 还会在 `mcp_servers[]` 中返回每个 server 的实时调用槽压力：
`max_concurrent_calls`、`active_calls`、`queued_calls` 和
`available_call_slots`。Web Studio 或运维侧可以用这些字段判断某个工具是否长时间占用、stdio server 是否饱和，或者工作流是否触发了过多并发调用。

Web Studio 的观测页面会把这些字段渲染为 MCP 工具压力面板，用户不用直接查看
JSON，也能区分当前是工具拥塞、模型等待、审批暂停，还是工作流编排本身造成的等待。

## 自定义 MCP server

推荐通过脚手架创建：

```text
/new-tool python <name>
```

或通过 Web Studio/API 使用工具 scaffold preset。

生成的 Python server 会包含：

- stdio JSON-RPC loop
- `tools/list`
- `tools/call`
- workspace path checking

## Controlled live RESTCONF connector

`network_tools/device_restconf_live_apply` is a controlled live HTTPS
RESTCONF/API connector. It requires `authorized_scope=true`,
`change_approved=true`, `dry_run_confirmed=true`, `credential_ref`,
`approval_ref`, `change_ticket`, `allowed_hosts`, `allowed_methods`,
`allowed_paths`, JSON payload, and `rollback_plan`.

The connector only sends a request when the MCP server environment explicitly
sets `GOFLOW_NETWORK_TOOLS_ENABLE_LIVE=1`; otherwise it returns a blocked audit
payload and sends no HTTP request. `credential_ref` supports `env:NAME` or
`env://NAME`, and the tool does not return secret material.

Production workflows must still use `stage.execution.mode: live` and
`allow_live_tools: [network_tools/device_restconf_live_apply]`, so Studio can
show approval, authorized scope, allowlist, rollback, and evidence diagnostics.
Raw SSH/NETCONF apply remains out of scope.
- 严格 JSON schema

更多见 [MCP 工具开发](./mcp-authoring.zh-CN.md)。
