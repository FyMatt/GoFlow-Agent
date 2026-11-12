---
name: controlled-operations
description: 在选定执行目标上完成基线采集、干跑、审批后变更、后检和失败恢复。
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
  - name: file_tools/write_file
  - name: web_tools/web_search
    required: false
  - name: web_tools/fetch_url
    required: false
  - name: web_tools/research_snapshot
    required: false
  - name: host_tools/host_facts
  - name: host_tools/disk_usage
  - name: host_tools/list_ports
  - name: host_tools/list_processes
  - name: host_tools/service_status
  - name: host_tools/read_log_tail
  - name: host_tools/inspect_managed_json
  - name: host_tools/plan_windows_json_change
  - name: host_tools/set_managed_json_value
  - name: host_tools/restore_managed_json_change
  - name: ssh_tools/ssh_host_key_scan
  - name: ssh_tools/ssh_run_command
  - name: network_tools/device_discovery_plan
  - name: network_tools/device_command_plan
  - name: network_tools/device_batch_command_plan
  - name: network_tools/device_config_dry_run
  - name: network_tools/device_restconf_live_apply
params:
  - name: operations_goal
    type: string
    description: 需要诊断、变更、验证或恢复的明确运维目标。
    required: true
activation:
  keywords: ["受控运维", "配置变更", "运维恢复", "维护窗口", "controlled operations", "operations recovery"]
  embedding_description: 执行绑定目标、包含干跑、审批、验证和恢复的有证据运维任务。
mode: fix
preferred_agent: operations-specialist
allowed_tool_kinds: [read, write, exec, network]
output_kind: change_evidence
next_skills: [local-host-diagnostics, remote-host-diagnostics]
metadata:
  domain: network-operations
  target_scope: host
  role: controlled-operator
---

## 关键执行约束

- 只用明确输入路径、阶段映射或内联材料，不猜默认文件。
- 默认只读取证或干跑；真实变更必须符合原请求、已保存目标和审批边界。
- 使用实际工具结果形成报告，保留验证与回滚证据，失败不写成成功。
- 公开文档查询不授权扫描或主机变更；仅本地任务不联网，不上传秘密。

## 职责

需要核对厂商命令、操作系统版本或配置语义时，允许查询公开技术文档；这与对目标主机的操作授权分开。先确认目标实际版本，必要时使用 refresh 获取最新资料，记录来源、时间与适用性。不得把私有日志或凭据放进搜索，也不得因为查到文档就执行未授权操作。用户要求仅用既有或本地证据时不联网。

在任务选择的同一个 Target 上执行有证据、可停止、可恢复的运维工作。阶段提供的工具白名单优先于本 Skill 的完整工具集合。

## 必须遵守的顺序

1. 确认 Target 身份、授权范围和任务验收标准。
2. 使用只读工具记录基线；无法采集基线时停止变更。
3. 生成精确干跑、预期差异、后检和回滚信息。
4. 在显式审批前不调用写入、应用或恢复工具。
5. 执行前重新检查状态；发现漂移立即停止。
6. 只执行批准的最小动作，记录真实工具结果、退出码、前后哈希或配置差异。
7. 使用独立只读调用完成后检。后检失败时只能使用已经准备好的恢复引用。

## 输入与工具选择

- 如果用户已经在请求或明确指定的文件中提供完整快照、日志或配置内容，直接使用这些内容；不得猜测其他文件名或为了寻找材料列目录。
- 用户以“输入”“输入配置”“输入日志”或“输入快照”描述材料时，使用其明确给出的具体路径或工作流映射。没有路径且未提供内联材料时，说明缺失输入，不假设任何默认文件；读取失败时明确阻塞，不猜测替代路径。
- `file_tools/read_file` 只能读取已明确给出的具体文件；目录不是文件，禁止把 `deliverables` 或工作区根目录作为读取路径。报告写入后只读回报告本身。
- 网络设备 Dry Run 必须调用 `network_tools/device_config_dry_run`，并传入授权范围、允许主机、允许命令、变更命令、回滚计划和 `dry_run=true`；用户给出的禁止项或操作边界作为 `operator_constraints` 原样传入。工具成功返回 `report_markdown` 后必须把该字段逐字写入用户指定的报告文件，不得重新排版、翻译、删减或补充。`device_command_plan` 仅用于纯只读 show/查询命令，不能用来规划 ACL、接口或其他配置变更。
- 工具结果中的命令、路径和占位符必须逐字保留，尤其是 `<NAME>` 形式的占位符；不得改写为 `&lt;NAME&gt;`、`&gt;`、Unicode 转义或其他 HTML/Markdown 实体。
- 用户明确限定报告字段时，只能写这些字段，不得补充同一工具返回的其他字段，也不得把被排除字段的值改写后放入报告。协议字段必须保留原名；网络设备 Dry Run 的审批字段固定逐字写为 `requires_approval: true`，不得翻译成“需要审批”。
- 多设备只读汇总优先调用 `network_tools/device_batch_command_plan` 或直接整理用户提供的逐设备快照，不得为每台设备猜测外部快照文件。
- 受控本地配置方案只基于明确输入文件生成快照、审批、原子写入、后检查和回滚说明；输入不存在时报告阻塞，不得换用猜测路径。
- 设备平台、主机、凭据引用、允许主机、允许命令或回滚命令缺失时，不得自创 Cisco/Huawei 等平台、CLI 语法、凭据名、团体字或风险等级；必须输出缺失项并停止 Dry Run 工具调用。
- 多设备快照没有提供阈值、拓扑、日志或进程证据时，只比较输入中的值和状态；不得补写 80% 等行业阈值、严重度、因果链、故障传播、邻居关系、P0/P1 等级或 shutdown/write memory 等操作。
- 受控配置变更必须同时有当前值和目标值。目标值缺失时停止；目标值已给出时也不得把配置字段自动等同于服务运行状态、端口监听状态或防火墙策略。
- Windows 离线 JSON 原子替换方案必须调用一次 `plan_windows_json_change`，把输入提供的目标路径、当前/目标内容和两项 SHA-256 原样传入，并将工具返回的 `report_markdown` 原样写入交付文件。该工具确定性生成固定临时、备份、回滚临时和回滚移出路径，执行前检查路径冲突，使用原子替换，并在应用和回滚后校验目标、唯一备份及回滚移出版本。不得自行重写工具生成的 PowerShell。
- 离线变更报告必须给出一个变量定义完整、可整体复制执行的 PowerShell 代码块；目标、哈希、固定路径和目标内容都在同一代码块内定义。需要同时给出应用与回滚时，使用一个带 `Apply`/`Rollback` 动作参数的脚本和共享变量，不拆成两份重复脚本。报告正文不要复述禁用 API、禁用参数或危险命令示例，避免操作者误复制，也避免仅靠“禁止项清单”替代可执行约束。
- 报告中的备份文件必须在变更前生成一次并记录为精确变量；回滚只能复用该变量。不得用 `*`、按时间排序取“最新备份”或其他可能恢复错文件的选择逻辑。
- 用户提供“目标不可达”的既有失败证据并明确禁止再次连接时，只整理该证据和离线恢复清单，不调用 SSH、设备发现、ping、端口扫描或其他网络工具，不补写退出码、日期、每日重试策略、敏感文件路径或未执行命令结果。

## 禁止事项

- 不得切换到其他 Target，不得绕过固定 SSH 主机指纹、主机白名单或命令白名单。
- 不得使用通用文件或命令工具替代受管配置、SSH 或 RESTCONF 的受控接口。
- 通用文件工具只能创建或校验用户明确要求的工作区报告，绝不能借此修改目标配置或绕过审批。
- 不得在缺少备份、回滚信息、审批或后检时声称变更成功。
- 不得在恢复过程中覆盖并发修改或扩大批准范围。

## 输出要求

必须区分基线、计划、审批、实际动作、实际差异、后检、恢复引用和剩余风险。没有工具证据的步骤必须标记为未执行。
面向用户的报告标题和说明默认使用中文；设备命令、路径、协议字段、值和占位符保持工具返回的字面原文，不翻译、不转义、不改写。网络设备 Dry Run 报告必须明确使用“前置检查”“后置检查”“回滚方案”三个中文标题，并逐字写入 `requires_approval: true`。
用户指定交付路径时，必须将最终报告写入该工作区相对路径并读回校验；不得为了确认目录或文件是否存在而预读交付路径。即使任务只要求 Dry Run 或停止报告，也不能只返回聊天文本。
当任务明确要求“只读 input/source.txt 并生成报告”时，文件调用固定为：读取一次 input/source.txt、写入一次指定报告、读回一次同一报告。不得创建哈希临时文件、暂存报告、目录或其他交付物，也不得调用删除工具清理自行创建的文件。
