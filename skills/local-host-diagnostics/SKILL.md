---
name: local-host-diagnostics
description: 在不接受任意命令或修改系统状态的前提下，收集本机结构化只读证据。
version: 1.0.0
author: GoFlow
tools:
  - name: host_tools/host_diagnostic_snapshot
    required: false
  - name: file_tools/read_file
  - name: file_tools/write_file
  - name: host_tools/host_facts
    required: true
  - name: host_tools/disk_usage
    required: false
  - name: host_tools/list_ports
    required: false
  - name: host_tools/list_processes
    required: false
  - name: host_tools/service_status
    required: false
  - name: host_tools/read_log_tail
    required: false
params:
  - name: diagnostic_goal
    type: string
    description: 要调查的本机症状、问题或证据请求。
    required: true
activation:
  keywords: ["local host", "host diagnostics", "disk usage", "listening ports", "service status", "process list", "system logs"]
  embedding_description: 在不改变系统状态的前提下检查本机信息、磁盘、端口、进程、服务和日志。
mode: audit
preferred_agent: operations-specialist
allowed_tool_kinds: [read, write]
output_kind: findings
next_skills: [execution-plan]
metadata:
  domain: local-operations
  target_scope: host
  role: diagnostician
---

## 职责

只收集回答请求所需的最小本机只读证据（read-only）。观察结果与假设必须分开，不得暗示诊断操作改变了系统状态。

## 执行流程

1. 确认执行目标为 `local`，并复述诊断目标。
2. 同时需要主机事实、磁盘、端口和端口进程关联时，优先只调用一次 `host_diagnostic_snapshot`；单项诊断才选择对应的磁盘、端口、进程、服务或日志工具。
3. 使用有边界的筛选和数量限制。已知服务或白名单日志文件时，不得请求大范围日志。
4. 下结论前关联时间戳、进程 ID、端口、服务状态、文件系统和容器标识。端口结果包含 PID 时，使用 `list_processes` 的 `pids` 参数精确查询相关进程，不拉取大批无关进程。
5. 无法使用的采集器应报告为环境限制，并附带工具返回的建议。
6. 最终输出证据、可能原因、不确定性和下一项只读检查。任何变更建议都必须进入单独的已审批工作流。
7. 只调用请求明确需要的采集器。请求只关注磁盘、端口和进程时，不采集服务或日志；不得为了填满报告扩大范围。
8. 工具返回 `unavailable`、零值、截断或缺少进程关联时，只能记录对应限制，不能自行归因于权限、操作系统已知限制或软件缺陷，除非工具错误文本明确支持。
9. 端口号不能单独证明具体软件、漏洞或风险。只有进程名称、服务证据或用户材料与端口相互印证时才能做归属判断；否则标为未验证。
10. 数据截断或关键采集器不可用时，不得写“未发现异常”“正常范围”或同义结论。报告必须包含标题“未验证项”，逐项列出无法判断的范围。
11. 用户没有提供磁盘阈值、进程白名单、端口基线或组织策略时，不评严重程度，不写“充足”“正常”“暂不构成风险”“应移除/关闭”等判断；只报告数值和待确认事项。
12. 进程名只证明采集到该名称，不自动证明产品用途、授权状态、父子关系含义或安全性。常见端口用途只能标为“常见关联，未验证”，不能写成确认说明。
13. 用户只说“监听端口”且没有指定协议时，`host_diagnostic_snapshot` 默认使用 `protocol=tcp`、`listening_only=true`；UDP 没有 TCP 式 LISTEN 状态，只有用户明确要求 UDP 时才采集，并在报告中说明其状态语义。

## 安全规则

- 不得调用命令执行、网络探测、服务控制、软件包安装或容器变更工具。文件写入只允许用于用户明确要求的工作区报告，不得修改系统配置、日志或诊断目标。
- 不得读取 `host_tools/read_log_tail` 强制根目录之外的日志。
- 不得泄露日志中的密钥值。最终答复只总结信号，并遮盖类似凭据的内容。

## 输出格式

用户指定工作区交付路径时，必须用 `file_tools/write_file` 写入完整报告，再用 `file_tools/read_file` 读回确认路径和内容；不得只在聊天中展示报告。

### 目标与范围

### 已收集证据

### 未验证项

### 采集限制
