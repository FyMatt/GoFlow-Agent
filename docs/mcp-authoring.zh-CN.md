# MCP 工具开发

[English](./mcp-authoring.md) | [简体中文](./mcp-authoring.zh-CN.md)

这份文档说明如何向 GoFlow 添加自定义 MCP server。

## CLI 脚手架

创建 Python MCP server：

```text
/new-tool python <name>
```

生成文件：

```text
mcp_servers/<name>.py
configs/mcp_servers/<name>.yaml
```

生成的 server 包含：

- stdio JSON-RPC 处理。
- `initialize`、`tools/list`、`tools/call`。
- `ping`、`read_text`、`write_text` 示例工具。
- `GOFLOW_WORKSPACE_ROOT` 路径边界检查。
- 路径穿越和符号链接逃逸检查。
- 严格 JSON schema。

## HTTP/Web Studio 脚手架

Web Studio 和 API 客户端可以使用：

```text
GET /api/resources/tools/scaffolds
GET /api/resources/tools/scaffolds/{preset}?name=<tool-name>
POST /api/resources/tools/scaffolds/{preset}
```

可用 preset：

- `python-local`
- `python-container-readonly`
- `python-container-writer`
- `python-container-network`

容器 preset 会生成 Docker/Podman `isolation: container` 配置，并默认启用更安全的限制。

## 推荐安全默认值

对第三方、生成的、写文件、执行命令、访问网络或处理不可信输入的工具，优先使用：

```yaml
isolation: container
network_disabled: true
restart_limit: 3
cooldown: 10s
max_concurrent_calls: 1
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-python:<version>
  workspace_mount: ro
  network: disabled
  readonly_rootfs: true
  no_new_privileges: true
  cap_drop: all
  memory: 256m
  cpus: "0.5"
  pids_limit: "64"
```

需要写 workspace 时，把 `workspace_mount` 改成 `rw`，但仍建议保留审批和风险提示。

## 校验

保存自定义工具前，后端会校验：

- 启动命令白名单。
- 不支持的隔离模式。
- container 必填字段。
- 网络、挂载、资源限制和 root 用户风险。
- schema 是否严格。

Web Studio 应把返回的 field-level issue 显示到对应表单字段上。

## 内置参考实现

可以参考这些内置 MCP server 的实现方式：

- `mcp_servers/file_tools/main.go`
- `mcp_servers/skill_runner/main.go`
- `mcp_servers/python_notes.py`

## 生效方式

新 MCP server 作为资源保存后会立即出现在资源列表中，但实际 server 进程通常需要重启 runtime 或未来 MCP rebootstrap 后才会启动。

CLI 可用：

```text
/reload-tools
/tools
```
