---
name: managed-local-config
description: 在自动备份和明确审批下检查、修改、验证并恢复白名单内的本地 JSON 配置项。
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
  - name: file_tools/write_file
  - name: host_tools/inspect_managed_json
    required: true
  - name: host_tools/plan_windows_json_change
    required: false
  - name: host_tools/set_managed_json_value
    required: true
  - name: host_tools/restore_managed_json_change
    required: true
params:
  - name: change_goal
    type: string
    description: 操作者要求执行的精确受管 JSON 键修改或恢复操作。
    required: true
activation:
  keywords: ["managed config", "local JSON config", "configuration recovery", "restore config change"]
  embedding_description: 在明确白名单内修改或恢复本地 JSON 配置的一个现有键，并完成备份、审批、验证和冲突安全恢复。
mode: fix
preferred_agent: operations-specialist
allowed_tool_kinds: [read, write]
output_kind: change_evidence
next_skills: [local-host-diagnostics]
metadata:
  domain: local-operations
  target_scope: host
  role: managed-config-operator
---

## 职责

只执行操作者明确要求的本地 JSON 配置修改。部署环境必须已经将文件根目录和独立备份根目录加入白名单。

## 变更流程

离线规划且输入已经提供 Windows JSON 当前/目标内容及两项 SHA-256 时，调用 `plan_windows_json_change`，将返回的 `report_markdown` 原样写入用户指定报告路径；不得自行生成或改写安全关键脚本。

1. 调用 `inspect_managed_json`，记录目标、结构、大小、文件模式和准确 SHA-256；配置值会被有意遮盖。
2. 确认请求的 JSON 指针已经存在，并且新值完整明确。不得推测密钥或环境特定值。
3. 使用检查所得 SHA、简明变更原因、`backup_required: true` 和 `post_check_required: true` 调用 `set_managed_json_value`。
4. 等待明确审批（explicit approval）。不得用命令工具或普通文件工具替代受控写入工具。
5. 报告返回的变更 ID、变更前后 SHA、备份证据、后检结果、恢复工具和恢复范围。
6. 操作者请求恢复时，使用准确变更 ID 和已记录的变更后 SHA 调用 `restore_managed_json_change`，随后报告恢复后的 SHA 和后检结果。

## 安全规则

- 不得完整重写文件、创建键、遍历数组、运行命令、重启服务，或编辑 `GOFLOW_HOST_MANAGED_CONFIG_ROOTS` 之外的路径。
- `file_tools/write_file` 只能写用户明确要求的工作区交付报告，不能替代受管 JSON 修改工具。
- 检查 SHA 已变化、备份被禁用、后检被禁用或备份根目录与受管根目录重叠时，不得继续。
- 不得暴露无关键的配置值；结构和哈希已足以用于规划。
- 恢复操作必须拒绝并发编辑。文件与记录的变更后镜像不同时，绝不能强制恢复。
- 配置文件发生变化不代表服务已经重启或运行行为已经变化，必须明确说明这一限制。

## 输出格式

用户指定报告路径时，必须写入报告并用 `file_tools/read_file` 读回校验。

### 目标与请求键

### 变更前证据

### 审批与备份证据

### 已应用值类型与哈希

### 后检

### 恢复 ID 与范围

### 运行时限制
