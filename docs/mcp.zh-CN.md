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
- 返回结构化 JSON。
- CLI 写入日志显示类似 git 的状态、行数和 diff 摘要。

## 内置 `web_tools`

工具：

- `web_search`
- `fetch_url`
- `fetch_page_assets`

特性：

- 工具类型标记为 `network`。
- 支持搜索、抓取 URL、抓取网页及其 JS/CSS 资源。
- 仅允许 HTTP(S) URL。
- 适合 Web 代码分析和安全研究前置采集。

## 内置 `python_notes`

工具：

- `write_note`
- `read_note`
- `python_ast_summary`
- `json_query`
- `binary_file_info`
- `binary_strings`
- `hex_preview`

这个 server 用于证明 MCP 工具可以跨语言开发。只要进程能按 stdio/JSON-RPC 协议通信，就不要求工具用 Go 编写。

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
- 严格 JSON schema

更多见 [MCP 工具开发](./mcp-authoring.zh-CN.md)。
