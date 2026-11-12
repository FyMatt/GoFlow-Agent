# 通用通知连接器

[English](README.md)

`notification_tools/publish_event` 是领域无关的通用事件工具。资源工作流决定何时发布
事件；连接器不执行扫描、调查、证据抓取或修复。可选配置
`configs/mcp_servers/notification_tools.yaml` 注册此标准输入输出 MCP 服务。
每次调用（包括默认本地收集）都声明 `kind: network`，沿用现有工具权限和审批路径。
启用资源或保存通道配置不会授予执行权限。

参数示例（未声明的字段会被拒绝）：

```json
{"channel_id":"local","severity":"warning","title":"需要复核","summary":"请复核已保存的报告。","evidence_refs":["artifact://run/report.json"]}
```

| 参数 | 约束 |
| --- | --- |
| `channel_id` | 可选，默认 `local`。1–64 个 ASCII 字母、数字、下划线或连字符，以字母或数字开头；不能传入 URL。 |
| `severity` | 必填：`info`、`warning`、`error` 或 `critical`。 |
| `title` | 必填，非空，最多 256 个 UTF-8 字节。 |
| `summary` | 必填，非空，最多 8192 个 UTF-8 字节。 |
| `evidence_refs` | 可选，最多 32 个引用，每个非空且最多 512 字节；仅原样携带，不访问其指向的内容。 |

编码后的 HTTP 载荷上限为 32 KiB；标准输入输出请求上限为 64 KiB。

数据根目录沿用 `config.ResolveDataRoot`：优先使用 `GOFLOW_DATA_ROOT`；相对值以
服务工作目录为基准，未配置则使用服务工作目录。随附配置的 `workdir: ./` 由 GoFlow
解析为运行时根目录，并通过环境变量白名单传递数据根目录设置。自定义启动器必须
使用相同工作目录，或设置绝对路径的 `GOFLOW_DATA_ROOT`。

二进制发行版通过 `configs/goflow.binary.yaml` 使用
`GOFLOW_NOTIFICATION_TOOLS_CMD`，Windows 默认值为
`<运行时根目录>/bin/notification_tools.exe`，无需 `go run` 或连接器源代码。
发行版启动器和 CLI 设置该命令；CLI 保留用户已设置的环境变量覆盖值。
发行包包含可选服务配置以及中英文连接器说明。

数据根目录必须由用户或启动器可信配置，工具参数不能指定根目录、事件 ID 或 outbox
路径。相对根目录（包括用户明确选择的 `../data`）在使用前转换为绝对路径。
事件文件名仅使用生成的 32 位十六进制 ID。通道、凭据读取及 outbox 写入复用持久化
模块的相对路径边界检查，并拒绝配置根目录以下的符号链接或 Windows 目录联接。根目录及其父目录应由
用户控制；这些检查不提供针对并发恶意文件系统替换的隔离。事件写入沿用原子写入
和同步机制。通道令牌仅用于请求认证，不写入事件记录、工具结果或日志。

任何发送尝试之前，事件都会持久化到：

```text
<数据根目录>/notifications/outbox/<event_id>.json
```

工具结果包含 `event_id`、`channel_id`、`severity`、`delivery_status`；收到 HTTP
响应时附带 `http_status`，失败时附带 `error`。

| `delivery_status` | 含义 |
| --- | --- |
| `stored` | 已持久化到本地收集器，未尝试外部发送。 |
| `delivered` | 已保存的 webhook 返回 HTTP 2xx；不代表下游业务已完成处理。 |
| `failed` | 通道校验或发送失败；MCP 结果为 `is_error: true`。 |
| `pending` | 仅出现在 outbox 中：没有持久化的最终发送结果，可能由进程中断导致。 |

连接器不安装后台发送器，也不自动重试。每次调用生成一个新的持久事件 ID，并将其
作为 `Idempotency-Key` 请求头发送。进程中断后，先检查 outbox 和接收端，再决定
是否手动重发；`pending` 事件可能已经到达接收端。若 HTTP 发送成功但最终结果保存
失败，工具仍返回错误，结果中的状态描述实际观测到的发送结果，而磁盘记录可能
仍为 `pending`，不能据此自动重发。

默认只有保留的 `local` 收集器，同名配置不能覆盖它。启用外部发送时，用户需在
LLM 工具参数之外自行保存 JSON 通道配置：

```text
<数据根目录>/notifications/channels/<channel_id>.json
```

```json
{"kind":"webhook","url":"https://notifications.example.org/events","credential_ref":"secret://notifications/team-token","allow_private":false}
```

`credential_ref` 可选，必须采用现有 `secret://` 引用格式。示例读取
`<数据根目录>/secrets/notifications/team-token`，将内容作为 bearer token 使用。
不接受内联令牌字段、URL 用户名或密码、查询字符串、片段；也不要将凭据放在 URL
路径中。通道文件和凭据文件分别限制为 16 KiB、4 KiB，且必须是普通文件。
连接器不提供通道编辑器、凭据创建工具或任意 URL 参数。

目标必须为 HTTP(S)。默认检查所有解析地址并阻止私网或本地地址；用户确需使用
本地端点时，必须在已保存配置中显式设置 `allow_private: true`，本地 HTTP 测试也
使用此选项。认证默认要求 HTTPS；仅在显式开启该选项且所有解析地址均为私网或
本地地址时允许通过 HTTP 认证。传输固定使用已校验的地址，正常验证 TLS，不使用
环境代理、不跟随重定向，响应头上限为 16 KiB，不读取或记录响应正文。
发送有 10 秒期限，遵守上下文取消；GoFlow 现有 MCP 客户端在调用方取消时终止
子进程。网络禁用环境会阻止外部发送，但本地收集仍可用。错误和 outbox 记录不包含
通道 URL、通道凭据或接收端响应正文。

本地验证（不向真实外部接收端发送消息）：

```sh
go test ./internal/notification ./mcp_servers/notification_tools
```
