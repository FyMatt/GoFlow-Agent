# 配置

GoFlow V3 面向普通用户：选择工作区，在设置页配置模型服务后提交任务。平台提供默认资源，智能体、工作流、Skill 和 MCP 连接可在资源页添加或替换；实际模型和推理强度以当前配置为准。

## 启动

```powershell
go run ./cmd/goflow --config configs/goflow.yaml --workspace D:\Projects\test --http 127.0.0.1:8080
```

正式产品只支持 Windows 10/11 x64 的 PC Web 界面。Linux 服务器或虚拟机可作为授权的远程目标。

模型服务的 `timeout` 对非流式请求表示整段请求上限；流式请求则限制初次有效输出的等待，以及文本、推理或工具参数停止推进的时间。持续返回有效内容的长响应可超过该时长，空心跳不能无限延长等待。任务取消或调用方截止时间仍会结束请求。Responses 接口出现协议错误、停滞或传输中断且尚未返回助手内容时，下一次既有重试尝试 HTTP/1.1，重试总次数不增加。

## 模型

默认模型提供方使用当前配置的 OpenAI 兼容接口。设置页通过密钥存储保存 Key，公开配置只引用密钥引用，不写明文：

```yaml
providers:
  primary:
    provider: openai-compatible
    base_url: https://tokenmarket.cheap/v1
    wire_api: responses
    api_key: secret://providers/primary/api_key
    model: gpt-5.6-sol
```

默认产品不配置备用模型链。

## 资源

用户资源只保留智能体、子智能体、工作流、Skill 和 MCP 连接。`configs/agents`、`configs/mcp_servers`、`workflows` 与 `skills` 下的文件会自动加载，并兼容常见 `AGENTS.md`、`CLAUDE.md` 和 `SKILL.md`。

普通用户只需编辑中文名称、中文说明、权限和启用状态。运行限制与原生沙箱参数由系统管理。

## MCP 连接

本地 MCP 连接只需要名称、命令、参数、命令白名单和隔离模式：

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

支持 `auto`、`native_strong`、`process_group`、`windows_job`、`windows_restricted_token`、`linux_cgroup` 和 `linux_netns`。第三方连接使用 `auto`；GoFlow 自动选择原生边界，强边界不可用时拒绝未知代码。已经移除的容器配置会直接返回迁移错误。

`isolation_options` 只允许用于 `linux_cgroup` 和 `linux_netns`。`network_disabled` 只有在原生执行边界真正提供网络隔离时才算强制生效。

## 主机目标

检查物理主机的连接使用 `target_scope: host`。内置 `host_tools` 处理本机事实与受管配置变更，`ssh_tools` 处理用户明确选择的远程目标。

受管本机 JSON 变更只能修改 `GOFLOW_HOST_MANAGED_CONFIG_ROOTS` 下的现有文件，备份与恢复证据写入 `GOFLOW_HOST_CHANGE_BACKUP_ROOT`。日志目录单独加入白名单，凭据只使用引用，不写入资源文件。

## 安全默认值

- 获批命令和第三方 MCP 只能在已确认的真实工作区内运行，不能越界访问其他目录；写入前系统保留恢复快照。
- 写入、执行、网络和主机变更按风险触发内联审批。
- 工作区快照、最近 3 个版本、检查点、质量门和恢复由系统自动管理。
- API Key、SSH 私钥和敏感环境变量不得记录或提交。
- 只有真实文件、Diff、测试、检查、证据或文档校验通过，任务才算成功。

参见 [MCP](./mcp.zh-CN.md)、[MCP 隔离](./mcp-isolation.zh-CN.md) 和 [资源](./resources.zh-CN.md)。

## 运行完成邮件通知

在 `configs/goflow.yaml` 增加 `notifications.email`，运行结束（完成、失败、阻塞、取消）后会向配置的收件人发送一封纯文本邮件：

```yaml
notifications:
  email:
    enabled: true
    smtp_host: smtp.example.com
    smtp_port: 587
    username: bot@example.com
    password: ${SMTP_PASSWORD}          # 或 secret://notifications/email
    from: bot@example.com
    to: [ops@example.com]
    tls: starttls                        # starttls | tls | none
    subject_prefix: "[GoFlow]"
    timeout: 20s
```

说明：

- `tls: starttls` 使用 587 端口升级加密，`tls` 用于 465 端口直连 TLS，`none` 仅用于本地测试；
- `from` 省略时回退到 `username`；
- 密码支持环境变量展开和 `secret://` 引用；投递失败只记录审计事件，不影响运行结果；
- 关闭时（`enabled: false`）不会产生任何网络请求。
