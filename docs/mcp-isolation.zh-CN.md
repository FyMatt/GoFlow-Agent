# 原生 MCP 隔离

GoFlow V3 不使用 Docker、Podman、容器或虚拟机。MCP 连接作为本机原生进程运行，并由宿主操作系统真实提供受控边界。

## 普通用户规则

普通用户只选择连接并查看能力摘要，不配置底层隔离、挂载、镜像、namespace 或资源参数。新连接默认使用 `isolation: auto`。

## 边界选择

- `native_strong`：宿主能力探针确认所有必需原语就绪时的广泛原生边界。
- `windows_restricted_token`：Windows 权限降低和 Job Object 生命周期控制。
- `windows_job`：可信本机工具的 Windows 进程树清理。
- `linux_landlock`：Linux 工作区文件系统边界。
- `linux_netns`：Linux 仅网络边界。
- `linux_cgroup`：Linux CPU、内存和进程数限制。
- `process_group`：仅生命周期清理，适用于可信本机工具。
- `none`：仅可信本机进程，禁止用于未知代码。

API 根据真实宿主适配器报告 `sandboxed`、`filesystem_sandboxed`、`network_enforced`、`privilege_sandboxed` 和 `resource_limited`。生命周期或单能力适配器不会被描述成完整沙箱。

## 未知和第三方 MCP

未知代码只接收已确认工作区、环境白名单、命令白名单和标准句柄；获批后可以在该工作区内运行，但不能越界访问其他目录。写入前已有外部只读快照，文件工具同时保留恢复日志。所需原生边界不可用时，启动直接拒绝，并返回中文原因和处理动作。

## 网络默认策略

除非连接明确需要并获得用户批准，否则默认不允许网络访问。`network_disabled` 本身只是声明，只有原生网络适配器真正执行时才算强制生效。

## 验证

使用运行时能力报告、配置诊断、任务证据和进程审计事件确认实际边界。不能用模型自评或模拟结果替代宿主能力缺失。
