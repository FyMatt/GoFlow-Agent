# MCP 隔离策略

[English](./mcp-isolation.md) | [简体中文](./mcp-isolation.zh-CN.md)

GoFlow 支持把 MCP server 作为本地子进程运行，但推荐的强隔离边界是 Docker/Podman `isolation: container`。

## 当前基线

已实现的保护包括：

- 内置文件工具的 workspace-root 路径检查。
- MCP 启动命令白名单。
- 最小化子进程环境变量。
- 请求/响应大小限制。
- Agent 层 tool kind policy。
- write、exec、network 或 confirm-policy 调用审批。
- `isolation: container` 容器隔离。
- `process_group`、`windows_job`、`windows_restricted_token`、`linux_cgroup`、`linux_netns` 等 fallback/helper 模式。
- `/api/runtime.mcp_servers` 和 `/api/runtime.mcp_isolation_modes` 风险与隔离能力摘要，包括容器镜像引用类型和生产可用性字段。
- `/api/config/diagnostics` 和 `/api/resources/tools` 风险诊断，镜像相关诊断会带 `details` 结构化字段。
- 可选 `tool_risk_policy`，对未沙箱化高风险工具强制审批或拒绝。

## 重要结论

`container` 是 GoFlow 认可的广义强沙箱边界。它的实际强度取决于镜像、挂载、网络、权限和资源限制。

原生模式的定位：

- `process_group`：生命周期清理。
- `windows_job`：Windows Job Object 生命周期清理。
- `windows_restricted_token`：Windows 降权和低完整性，但不是完整文件系统/网络沙箱。
- `linux_cgroup`：资源控制。
- `linux_netns`：网络隔离。

`windows_appcontainer` 不在计划中；类似值会被配置校验拒绝，并建议改用 `isolation: container`。

## 推荐配置

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-python:<version>
  workspace_mount: ro
  workspace_target: /workspace
  network: disabled
  readonly_rootfs: true
  no_new_privileges: true
  cap_drop: all
  ipc: none
  userns: auto
  tmpfs:
    - /tmp
  memory: 256m
  memory_swap: 256m
  cpus: "0.5"
  pids_limit: "64"
  pull_policy: missing
  init: "true"
```

`memory_swap` 会传给 Docker/Podman 的 `--memory-swap`，读-only helper 通常建议和 `memory` 保持一致。`pull_policy` 会传给 `--pull`，普通开发可用 `missing`，生产环境如果已经预拉取可信镜像可以用 `never`。

生产环境建议使用 `image@sha256:...` 这种不可变镜像引用。`/api/runtime.mcp_servers[]` 会返回 `container_image_reference_type`（`missing` / `floating` / `version_tag` / `digest`）、`container_image_digest_pinned`、`container_image_production_ready` 和 `container_pull_policy`，前端可以直接用这些字段显示镜像风险徽标。`/api/config/diagnostics` 会额外报告 production profile 未使用 digest、production profile 未使用 `pull_policy: never` 等字段级问题。

如果工具必须联网，把 `network` 改成受控网络模式，并在 UI 上明确显示风险。

## 前端展示建议

Studio 应把 `container` 放在隔离模式首位，并标记为推荐强沙箱。

对于 fallback/native 模式，应说明它们只能提供生命周期、资源、网络或权限层面的局部保护，不能替代容器隔离。

如果容器运行时缺失，不要自动降级到宿主机执行；应让用户明确选择并展示风险。
