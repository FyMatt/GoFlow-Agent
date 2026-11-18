# 本地资源扩展指南

面向本地管理员与资源开发者。本文说明当前通用资源机制，不代表首批资源包已经通过真实模型或生产验收。[English](./resource-extension-guide.md)

源码专用参考：文中的内联 `internal/...` 路径仅供源码检出目录查阅，发行包不包含这些文件。

## 1. 先选择扩展层

已有工具足够时，添加 Agent、Skill、Workflow、模板和 Kit；缺少执行能力时，另行实现或配置 MCP 适配器。Kit 汇总入口、依赖、任务匹配和领域验收声明，不会凭 YAML 创造工具或扩大权限。实际执行仍受 Agent、Skill、阶段工具范围、用户授权和审批约束。

可参考实际的 [软件 Kit](../kits/software-engineering-kit/kit.yaml)、[运维 Kit](../kits/operations-runbook-kit/kit.yaml) 和 [安全 Kit](../kits/security-research-kit/kit.yaml)。完整阶段说明见 [工作流文档](./workflows.zh-CN.md)。

## 2. routing v1：匹配、优先级与兼容路径

在 Kit 中声明，例如以下片段；入口必须同时列入 Kit 的 `workflows`：

```yaml
workflows: [field-notes-review]
routing:
  version: 1
  enabled: true
  priority: 10
  match_terms: [田野笔记整理, field notes review]
  entry_workflow: field-notes-review
  required_tools: [file_tools/read_file, file_tools/write_file]
```

- `version` 当前只支持 `1`；`priority` 范围为 `-100..100`，省略为 `0`。存在 routing 时，省略 `enabled` 表示启用；`false` 只退出自动分流，不卸载资源。
- `match_terms` 至少一个非空词项。匹配为大小写不敏感的子串包含；任一词项命中即可，不按词数、语义相似度或最长词项评分。
- `entry_agent` 与 `entry_workflow` 必须二选一，并列入相应 `agents` 或 `workflows`。`required_tools` 使用限定名 `server/tool`，表示依赖，不表示获得权限。
- 包级 `tools` 是资源库引用，不要求当前入口具备调用全部库工具的权限；未发现的限定工具产生警告。入口必须具备的工具应放在 `routing.required_tools`，预检会严格检查其可用性及入口权限，不满足则阻止运行。
- API 的自动选择使用 `type: auto`。显式 Workflow 优先；显式 Agent、Skill 或 Tool 选择也不会被 Kit 自动路由覆盖。省略 `type` 不等于请求自动分流。
- 最高优先级只有一个匹配项时，选择该入口，再走正常资源、环境、工具、目标和审批预检。最高优先级并列时返回 `kit_routing_ambiguous`；有启用合约却没有匹配时返回 `kit_routing_no_match`。两者都应修正匹配词或显式选择入口，不静默转成软件任务。
- 只有没有任何启用的 routing 合约时，才使用旧的本地任务推荐逻辑。Kit 没有 routing 本身不会参与新路由。已选最高优先级入口缺少依赖时报告失败，不继续尝试低优先级包。

可先向 `POST /api/runs/preflight` 提交 `{"type":"auto","input":"田野笔记整理：读取 input/notes.txt"}`，检查 `target`、`issues` 和 `resource_pins`；预检通过不等于批准执行或证明领域质量。实现与回归：合约校验 (`internal/scaffold/kit_routing.go`)、路由与依赖预检 (`internal/api/kit_routing.go`)、路由测试 (`internal/api/kit_routing_test.go`)。

## 2.1 验收声明（acceptance）

领域质量门随资源包声明，不需要改内核或验收脚本：

```yaml
acceptance:
  sample_request: 核验 source-catalog.json 的全部来源并裁决冲突
  verify_commands: [go test ./..., go vet ./...]
  deliverables: [deliverables/evidence.json, deliverables/report.md]
  min_quality_score: 80
```

- `sample_request` 必填，用来演练该领域的入口；
- `verify_commands` 与 `deliverables` 至少声明一个，否则清单校验报 `kit_acceptance_incomplete`；
- `min_quality_score` 取值 0–100，省略时使用默认阈值；
- 声明后会随 `GET /api/kits/{name}` 返回，验收流程可以直接读取该领域的门槛。

## 3. Kit 存放层与保存位置

以下按同名 Kit 的读取优先级从高到低排列。`rootData` / `Data` 均指当前有效 `DataRoot`，`runtime` 指 `RuntimeHome`，不是工作区目录。

| 优先级 | 位置 | 用途 |
| --- | --- | --- |
| 高 | `rootData/kits/<name>/kit.yaml` | API 保存和导入 Kit 清单的位置；本地覆盖 |
| 中 | `Data/resources/kits/<name>/kit.yaml` | 独立数据根中的资源层覆盖 |
| 低 | `runtime/kits/<name>/kit.yaml` | 随运行目录提供的基础包 |

兼容 `kit.yml` 和根目录下的 `<name>.yaml` / `<name>.yml`；相同根目录会去重。上层覆盖整个同名 Kit，不是逐字段合并；损坏或越界的上层文件不会偷偷露出下层副本。API 删除产生的 tombstone 会隐藏下层同名包，显式保存可恢复可见性。编辑发行目录副本可能被更高层覆盖，应查看资源详情中的实际 `path`。

实现与回归：Kit 分层 (`internal/api/kit_layers.go`)、分层测试 (`internal/api/kit_layers_test.go`)、数据根解析 (`internal/api/resources.go`)。此处只描述 Kit 清单层；其他资源有各自的保存路径。

## 4. 安装、启用、保存与导入

1. 在测试运行目录和工作区准备资源，检查 Agent、Skill、Workflow、Provider、MCP 等依赖；不要复制真实凭据。先安装依赖，再保存 Kit。文件安装后的新模块需要按提示 reload 或重启。
2. 用 `GET /api/resources/kits` 和 `GET /api/resources/kits/<name>` 查看生效清单与路径。`POST /api/resources/kits/validate` 校验提交的 YAML/JSON；`GET /api/resources/kits/<name>/validate` 校验已有清单。
3. 用 `PUT /api/resources/kits/<name>` 保存完整清单。准备期间可保留 `routing.enabled: false`，完成依赖预检后改为 `true` 并再次保存。这里没有独立的 Kit 启用端点；启用自动路由也不等于启用被禁用的 Provider 或 MCP。
4. `GET /api/resources/kits/<name>/export` 导出 bundle，默认不包含秘密值。`POST /api/resources/kits/import?overwrite=false` 导入并避免覆盖已有资源。当前导入省略 `overwrite` 时默认为覆盖，操作前应明确选择。
5. 检查导入结果的 saved、skipped、errors、warnings 及重启提示。导入可能部分成功，不能把已返回结果当作整包原子安装；包含 Agent、Provider、MCP 模块时尤其要确认已加载，再重新预检并启动一个新任务。

清单结构校验、依赖预检和运行权限检查是不同层次；一次保存成功不能代替运行预检。实际端点与导入行为见 Kit API (`internal/api/kits.go`)。

## 5. 资源 pins 与成熟度

自动路由任务会记录所选 Kit 及相关入口、配置和依赖的 `resource_pins`。Pin 的版本是公开配置的 SHA-256 内容身份，不是可任意改写的展示版本；Kit 还记录可移植的来源层身份，因此切换同名副本也可能改变 pin。新配置不会改写历史任务的 pins。

API 在继续或批准等相关操作前检查资源身份；不一致时返回 `409 resource_pin_drift`，需要用当前资源启动新任务。Pins 不会自动恢复旧配置或隔离出旧运行环境，也不证明外部二进制、凭据、远端状态和网页内容没有变化。见 pin 数据结构 (`internal/session/resource_pins.go`)、资源完整性检查 (`internal/api/resource_integrity.go`)。

`vertical_pack.maturity` 的 `generic`、`guided`、`professional`、`production-ready` 都是作者声明。目录中的 `claimed_maturity` 与 `verification_status: unverified` 分开呈现；结构验证或路由成功不会自动改变后者。真实执行与内容验收须保留独立证据，不能用 `production-ready` 文本代替。

## 6. 显式通用阶段能力

长 Skill 的正文不会全部自动注入：当前运行时默认压缩为约 900 字节的摘要，优先取前部标题和列表。应在正文开头用短列表放置关键执行约束，不把授权、输入选择或工具参数规则只写在长文末尾。用 prompt_budget 的 skill_instruction_mode、skill_source_bytes 和 skill_injected_bytes 核对实际注入；本轮测试覆盖七个首批技能的关键规则确实进入模型提示，而不是只检查文件包含这些文字。

能力由阶段 `params` 声明，未知领域也可使用，不按工作流名启用。

并行分派的 `domain_ref` / `domain` 仅匹配候选节点的名称、`params.domain` 或逗号分隔的 `params.domain_aliases`。例如将任意节点声明为 `domain_aliases: 田野笔记,field-notes-review` 即可接收该领域，不需要核心领域表。内置多领域工作流的别名已放入资源；旧自定义工作流若依赖“software 自动对应 software-worker”这类隐式映射，需要补充自己的别名。候选节点、分支上限和原有权限继续生效；匹配多个候选时仍按并行规则执行，不授予新工具。

声明 change_slice_policy 时，变更切片必须显式输出为 goflow-change-slices JSON 数组，mode 为 read 或 write；新增测试使用 write。只读目录/文件证据不会再被阶段快照自动推断为写入计划，非法声明也不会回退成猜测权限。当前默认开发模板对规划契约失败最多尝试两次修正；不会由框架代填计划或伪造模型产物。

input_files 可以引用已观察的现有文件，或明确依赖链中前序 write 切片声明的目标文件；不能引用无关切片、仅 read 的“生产者”或没有依赖声明的新文件。未来产物不会被伪造读取哈希。规划格式修正重试会保留本阶段已成功的只读观测，并按调用 ID 去重，避免重复读取和丢失绑定证据。

TLS 等临时传输错误在续跑中再次发生时仍应暂停并保留阶段。旧版本存为 action_failed 的运行，只有存在已保存的未完成阶段且错误可判定为临时模型连接故障，才开放同一运行的继续入口；鉴权、资源版本冲突和缺失上下文仍会阻止恢复。

| 参数 | 当前契约 |
| --- | --- |
| `require_workspace_mutation: "true"` | 要求本阶段实际工具写入或观察到的文件变更；报告声称的文件名、前一阶段的变更不算 |
| `change_slice_policy` | `off`、`required`、`if_missing`；未声明不强制生成 change slices |
| `seed_plan_intake: "true"` | 显式追加规划提示，以 `resource_helper` 标明框架来源，不冒充模型或工具证据 |
| `approved_plan_scope: "true"` | 必须同时有阶段 `approval: true`，仍需实际批准和原有工具边界 |
| `use_change_slice_scheduler: "true"` | 还要求上述已批准计划范围声明；不会自动授予写入工具 |

这些布尔能力只接受字符串 `"true"` / `"false"`。未声明的阶段不自动补写业务报告、代码、测试、Office 文档或网页样式；模型未交付时必须如实失败或标明未完成。需要写入的资源阶段应显式调用获准工具，而不是期待框架修补产物。能力检查与回归见 图执行器 (`internal/agent/workflow_graph.go`)、通用能力测试 (`internal/agent/workflow_stage_capabilities_test.go`)。

### 验证模式与 all / any 工具

结构化结果请显式声明 `require_json_output: "true"` 与 `required_json_keys`，不要依赖自然语言中的“输出 JSON”触发检查。安全资源已用此契约约束告警判断、严重程度、证据及通知回执；缺字段会在路由前阻塞。验收 `exists` 检查区分缺失引用和实际值：不存在的 `result.xxx` 不算证据，`false`、`0` 则是存在的值。

需要类型或枚举约束时，在 `params.output_schema_json` 声明 JSON 对象形式的规则，复用 MCP 参数校验支持的子集（例如 type、properties、required、enum、数组项及长度约束），不是完整 JSON Schema 实现，不解析外部引用，未支持的关键词不能作为验收依据。声明后默认要求 JSON 对象并阻塞不合规结果，已有显式契约失败策略仍有效。例如 `'{"type":"object","required":["alert_required"],"properties":{"alert_required":{"type":"boolean"}}}'` 会拒绝 null 和字符串 "false"，但接受布尔 false。通知回执也可用枚举限定实际投递状态；这些规则只验证结构，不证明专业判断正确。

```yaml
params:
  require_verification: "true"
  verification_mode: tool
  verification_tools: file_tools/write_file
  verification_any_tools: file_tools/read_file,web_tools/research_snapshot
```

上例要求写入工具成功，并至少有读取文件或公开资料快照中的一种成功证据；仅在任务确实需要写入时才声明写入工具。

- `verification_mode: command` 要求实际成功执行测试、构建或静态检查命令。旧 `require_verification: "true"` 未给模式时保持此默认，不会按领域猜测。
- `verification_mode: tool` 不要求软件测试命令，但必须声明验证工具。`verification_tools` 全部必需；`required_tools` 等既有必需工具声明继续叠加。
- `verification_any_tools` 只用于显式 tool 模式，列表非空且必须是限定名 `server/tool`，至少一个成功即可。与 all 列表同时存在时，两组条件都必须满足。
- 工具证据需要真实完成的调用、调用 ID，以及返回内容或持久化证据。失败、拒绝、等待审批、空结果、错误服务名和纯文字自评不能满足该工具要求。其他验收和门禁失败仍然有效；any 不会掩盖采集错误或把“已读取”变成“覆盖完整”。
- 这些字段只检查证据，不添加可调用工具。阶段 `tools` / `allowed_tools` 也只能在既有权限内缩小范围。

规划、验证可以按请求查询公开技术文档，使用获准的 `web_tools/web_search`、`web_tools/fetch_url`、`web_tools/research_snapshot`，保留来源 URL、产品/版本和抓取时间。明确仅本地、离线或仅既有证据时不联网；不得上传私有日志、配置、凭据或源码。公开文档检索不授权目标扫描、主机访问或修改。

## 7. 未知领域示例与首批包的限制

从 [田野笔记示例说明](../examples/custom-resource-pack/README.zh-CN.md) 开始，查看实际 [Kit](../examples/custom-resource-pack/kits/field-notes-kit/kit.yaml)、[Agent](../examples/custom-resource-pack/configs/agents/field-notes-reporter.yaml)、[Skill](../examples/custom-resource-pack/skills/field-notes-review/SKILL.md) 和 [Workflow](../examples/custom-resource-pack/workflows/field-notes-review/workflow.yaml)。它使用现有文件 MCP，不需要添加核心领域枚举。

在测试工作区提供真实输入，再请求：`田野笔记整理：读取 input/notes.txt，生成 deliverables/notes.json；缺失日期和单位标为未知，不修改源文件。` 检查实际 JSON 文件、源文件哈希、读写与读回证据、门禁结果和资源 pins。该示例的文件工具证据门禁不能独立证明内容准确、没有幻觉或达到生产质量；本地模拟 Provider 只能证明机制链路。

首批软件、运维、安全包提供可替换流程与已有适配器，不是任意任务都能完成的承诺。软件包仍需可用的项目环境和执行证据；运维包需要真实输入、目标与变更授权；安全包收集和复核授权证据，不等于完整扫描能力，也不能把日志缺失写成“无攻击”。新领域缺少工具时应报告依赖，领域判断与覆盖不足应保留未知或未验证，不自动修复、隔离或清除目标。参见 [本地运维](./local-operations.zh-CN.md) 与 [验收指南](./acceptance-guide.zh-CN.md)。

文档链接与双语配对可在源码根目录运行 `python scripts/validate_docs.py` 检查；它不执行 Provider、连接目标主机或认证资源包质量。
