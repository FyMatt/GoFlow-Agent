# MCP 连接

GoFlow 使用标准 stdio MCP 连接承载确定性工具。模型负责理解意图，Go 代码负责 Schema、权限、工作区范围、审批、执行、证据和恢复。

## 内置连接

| 连接 | 用途 | 边界 |
| --- | --- | --- |
| `file_tools` | 读取、搜索、补丁和写入任务文件 | 已确认的真实工作区与逐文件恢复日志 |
| `command_tools` | 构建、测试、静态检查、批准的命令和受管开发服务 | 已确认的真实工作区 |
| `web_tools` | 搜索、抓取、浏览器证据和来源收集 | 显式网络工具 |
| `network_tools` | 网络运维计划和授权设备操作 | 审批与目标策略 |
| `office_tools` | Office/WPS 读取、新建、模板修改、合并、校验和 PDF 导出 | 任务副本与 Office 桥接 |
| `host_tools` | 本机事实、日志和受管配置 | `target_scope: host` |
| `ssh_tools` | 明确远程目标的检查与批准操作 | `target_scope: host` |
| `skill_runner` | 执行 Skill 声明的确定性脚本 | 原生执行策略 |

`host_tools` 只读取 `GOFLOW_HOST_LOG_ROOTS` 下的日志。`managed-local-config` Skill 管理受限本机变更，`remote-host-diagnostics` Skill 管理远程检查。所有凭据只使用引用并进行脱敏。

## 原生隔离

GoFlow V3 不使用容器运行时或虚拟机。支持：

- `auto`：选择当前可用的最小原生边界，不能满足要求时拒绝降级。
- `native_strong`：Windows 使用 AppContainer、受限令牌、ACL 与 Job Object；Linux 使用 Landlock、namespace、seccomp、rlimit 与 cgroup v2。
- `process_group`：可信内置工具的生命周期清理。
- `windows_job`、`windows_restricted_token`：显式 Windows 降级边界。
- `linux_cgroup`、`linux_netns`：显式 Linux 资源或网络边界。

未知代码和第三方 MCP 使用 `auto` 或 `native_strong`，仅能访问已确认工作区和明确允许的环境变量。写入仍受任务权限审批；任务前快照和文件日志负责恢复，质量门通过后可把当前状态保存为最近 3 个工作区版本之一。

## 运行契约

每个连接声明固定命令、`allowed_commands` 或 `allowed_command_paths`、最小 `env_allowlist`、请求响应上限、超时、重启限制、冷却时间和并发上限。

```yaml
mcp_servers:
  - name: file_tools
    command: go
    args: [run, ./mcp_servers/file_tools]
    enabled: true
    workdir: .
    allowed_commands: [go]
    isolation: process_group
```

`network_disabled` 只有在原生边界真实阻止网络出口时才算强制生效。

## 领域工具

- 网络运维：`network_tools` 要求 `authorized_scope`、`allowed_hosts`、凭据引用、干跑计划、审批、回滚信息和产物引用。`device_batch_command_plan` 用于多设备计划，`device_restconf_live_apply` 是高风险明确操作。
- 浏览器证据：`browser_snapshot` 保存页面与结构化状态，`browser_probe_points` 限定检查点，主动探测必须有 `active_probe_approved`。
- 二进制证据：`binary_format_summary`、`binary_entropy_map`、`binary_symbol_hints`、`binary_extract_window` 只产出受限静态证据。
- 资料核验：`research_snapshot` 记录来源，`compile_research_evidence` 使用缓存生成 claim-to-source 证据，并导出 Markdown、JSON、CSV。
- 文档：`office_tools` 支持新建、读取、基于模板修改、保留格式精确修改、合并、校验和导出 PDF。

## 成功证据

一次 MCP 调用不等于任务成功。交付必须由真实文件、Diff、构建、测试、静态检查、来源证据、变更后复测或文档校验证明，不能用模型自评替代。

参见 [配置](./configuration.zh-CN.md)、[隔离](./mcp-isolation.zh-CN.md) 和 [资源](./resources.zh-CN.md)。
