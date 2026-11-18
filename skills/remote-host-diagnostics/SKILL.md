---
name: remote-host-diagnostics
description: 使用固定主机指纹和精确命令白名单，对已授权远程 SSH 主机执行可审计诊断。
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
  - name: file_tools/write_file
  - name: ssh_tools/ssh_host_key_scan
    required: true
  - name: ssh_tools/ssh_run_command
    required: true
params:
  - name: diagnostic_goal
    type: string
    description: 需要在远程主机确认的症状、状态或证据。
    required: true
activation:
  keywords: ["远程主机", "SSH 诊断", "服务器检查", "远程日志", "remote host", "ssh diagnostics"]
  embedding_description: 使用固定主机密钥和精确命令白名单诊断已授权的远程 SSH 目标。
mode: audit
preferred_agent: operations-specialist
allowed_tool_kinds: [read, write, network, exec]
output_kind: findings
next_skills: [execution-plan]
metadata:
  domain: network-operations
  target_scope: host
  role: remote-diagnostician
---

## 职责

对用户已经保存并授权的 SSH Target 收集最少、可复查的远程证据。不得把该能力当作通用终端，也不得绕过 Target 的固定主机指纹和命令白名单。

## 执行流程

1. 确认任务选择的 `target_id` 与用户要检查的主机一致。
2. 首先调用 `ssh_host_key_scan`，确认服务端当前公钥与 Target 固定指纹一致。
3. 从 Target 已允许的命令中选择完成诊断所需的最少命令；不得改写、拼接或扩展命令。
4. 调用 `ssh_run_command` 时始终传入相同 `target_id`，设置合理超时，并保留退出码、标准输出、标准错误和截断状态。
5. 将事实、推断和未验证内容分开。命令失败、连接超时或输出截断不能被描述为检查通过。
6. 如需配置修改、服务重启或提权，停止当前诊断并转入带审批、验证和恢复的独立工作流。

## 安全边界

- 不支持密码、交互式认证、交互式 `sudo`、任意 Shell、端口转发或文件传输。
- 不得要求用户关闭 `StrictHostKeyChecking`，也不得使用 `accept-new` 替代固定指纹。
- 不得把私钥、凭据引用解析结果或临时密钥路径写入最终报告。
- 不得执行 Target 白名单之外的命令，即使该命令看起来是只读的。
- 文件工具只能生成或校验用户明确要求的工作区诊断报告，不得用于远程文件传输或修改目标状态。

## 输出格式

用户指定交付路径时，必须写入完整报告并读回校验。连接失败或授权不足也必须生成真实失败报告，不得伪造成功。

### 目标与授权范围

### 主机指纹证据

### 已执行命令与退出码

### 发现与置信度

### 失败或未完成检查

### 下一步建议
