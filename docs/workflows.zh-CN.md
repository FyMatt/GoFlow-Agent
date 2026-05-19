# 工作流图

[English](./workflows.md) | [简体中文](./workflows.zh-CN.md)

GoFlow 支持两类工作流：

- 内置工作流，例如 `plan-fix-audit` 和 `skill-chain`。
- runtime home 下的图工作流：`workflows/<name>/workflow.yaml`。

它们会通过两个相关但不同的目录暴露：

- `/api/workflow-graphs`：只列出可编辑的图工作流文件。
- `/api/workflow-options.workflow_executors`：列出可运行的 workflow
  入口，包括可编辑图工作流，以及 `plan-fix-audit`、`skill-chain` 这类
  兼容执行器。

如果你保存了一个同名且有效的图工作流，比如
`workflows/plan-fix-audit/workflow.yaml`，它会覆盖对应的兼容执行器。这样
可以在不修改 Go 源码的情况下定制内置工作流。

内置 Workflow Template 是文件化资源，默认从 `internal/agent/templates/workflows/*.yaml` 内嵌进二进制。运行目录下的 `templates/workflows/*.yaml` 可以新增模板，也可以用同名文件覆盖内置模板；同一套目录会影响 `/workflow-templates`、Workflow 创建、校验、fork 和 Studio 模板选择。

当任务需要命名阶段、明确 Agent 分工、分支选择、审批边界、控制流或输出传递时，应使用工作流图。

完整案例：

- `examples/extension-workflow`：紧凑的 Python MCP server、只读 Agent、自定义 Skill 和图工作流示例。
- `examples/binary-analysis-kit`：更完整的 materialized kit 示例，工作流会把 Team 上下文传给计划 Skill，再经过 Policy Gate，最后进入报告或修订分支。

## 创建与运行

```text
/new-workflow release-check
/workflow release-check review the auth changes
```

生成路径：

```text
workflows/release-check/workflow.yaml
workflows/release-check/WORKFLOW.md
```

工作流文件属于框架配置，不属于目标项目文件。active workspace 仍然是文件工具读写目标。

## Visual Workflow Studio

启动 HTTP：

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

打开：

```text
http://127.0.0.1:8080/workflows
```

Studio 可以：

- 列出内置和自定义 workflow。
- 创建自定义 workflow。
- 拖拽节点到画布。
- 拖动节点位置。
- 自定义连线。
- 编辑节点属性。
- 保存到 `workflows/<name>/workflow.yaml`。
- 运行 workflow 并显示 stage、token、approval、artifact、diff 和事件流。

Workflow Studio 有两种编辑模式：

- **简洁模式** 是默认模式，面向普通用户。它把界面集中在画布、起步模板、
  任务卡片、资源选择、常用输入输出预设和可视化走向配置上；高级配置分区、
  原始路由映射、上下文契约、执行顺序细节和运行调试面板默认隐藏。
- **专家模式** 会记住用户选择，面向开发者和进阶操作者。它恢复完整节点库、
  全部配置分区、原始参数、routes/cases 映射、上下文契约、输入输出映射、
  执行顺序预览和运行诊断。它不会改变 workflow schema，只是把同一套图模型的
  更多字段暴露出来。

为了让右侧节点配置面板更容易上手，Studio 现在还支持：
- 在“节点元数据”里直接套用默认配置或示例配置到当前节点。
- 在“高级行为”说明卡片里一键把示例填入对应字段。
- 在“结果与证据”里同时配置 `artifacts` 和 `acceptance_criteria`，直接把证据和验收标准写进工作流。
- 在“结果与证据”说明卡片里一键添加常见报告产物、证据产物、包含检查和存在性检查。

画布会把普通顺序流和控制语义分开显示：

- 控制节点卡片会显示“控制节点”摘要，说明它控制循环体、分支或结束后的路径。
- 分支连线会显示 `通过`、`失败`、`为真`、`默认` 等结果标签。
- `loop` 和 `for_each` 的循环体会显示为辅助连线，区别于普通 `next` 顺序连线。
- 选中或悬浮控制节点时，画布会用淡色范围框标出它控制的阶段，用户不需要先理解 YAML 字段也能看出节点作用范围。

## 节点类型

可视节点：

- `start`
- `end`

执行节点：

- `agent`
- `skill`
- `tool`
- `custom`

控制流节点：

- `condition`
- `switch`
- `router`
- `policy_guard`
- `quality_gate`
- `parallel`
- `join`
- `for_each`
- `loop`
- `sub_workflow`
- `checkpoint`
- `input_gate`

控制流节点不一定自己调用 LLM，但可以选择路径、并行分支、等待汇合、重复执行、调用子工作流、暂停等待输入或阻断流程。
对重复节点来说，`params.stage` 表示被控制的循环体阶段，`next` 表示循环结束后继续进入的阶段；这两种关系在运行时和 Studio 画布中都会区别显示。

这些内置节点的字段、输出、提示、示例和默认 stage 来自
`internal/agent/templates/workflow_nodes/*.yaml`，发布时会内嵌进二进制。
运行目录可以通过 `templates/workflow_nodes/*.yaml` 或
`metadata/workflow_nodes/*.yaml` 覆盖编辑器展示信息；这不会新增新的可执行
节点类型，只会调整 Studio 和 CLI 看到的元数据。

## 输出传递

工作流应支持把前一个节点输出作为后一个节点输入：

```yaml
outputs:
  plan: result.output
input:
  approved_plan: stages.triage.outputs.plan
```

这样工作流不是简单串行执行，而是真正协作。

## 节点配置速查

Web Studio 会从 `/api/workflow-options` 读取节点字段元数据。配置节点时可以按下面的规则填写：

当界面需要“运行某个 workflow”的下拉框时，应优先使用
`workflow_executors`；当界面需要“编辑某个图文件”时，才使用
`/api/workflow-graphs`。这样用户既能运行兼容入口，也不会误把兼容执行器当成
可直接编辑的图文件。

| 字段 | 用途 | 常见值 |
| --- | --- | --- |
| `agent` | 指定哪个 Agent profile 执行这个阶段 | `planner`、`software-engineer`、`web-security-researcher`、`binary-analyst` |
| `skill` | 指定这个阶段使用哪个可复用流程 | `execution-plan`、`code-writing`、`code-audit`、`web-vulnerability-research` |
| `tool` | 给 tool 节点指定优先工具提示 | `file_tools/write_file`、`web_tools/fetch_page_assets`、`python_notes/binary_strings` |
| `input` | 把上游输出映射成本阶段输入 | `plan: stages.plan.outputs.plan`、`target: workflow.input` |
| `outputs` | 给下游发布稳定变量名 | `summary: result.summary`、`report: result.output`、`findings: result.findings` |
| `params` | 节点参数、团队名、规则名、表单字段、输出契约 | `team: software-task-team`、`rule: risk_at_least`、`fields: target:string:Target:required` |
| `artifacts` | 声明报告、证据、diff 等可回放产物 | `name: audit-report, kind: report, ref: result.output` |
| `acceptance_criteria` | 给质量门禁和回放声明验收检查 | `name: has-risk, ref: result.output, contains: risk` |
| `approval` | 在高风险阶段前暂停审批 | 写文件、执行命令、外部副作用、发布阶段建议填 `true` |
| `retry.max_attempts` | 限制自动重试次数 | `2`、`3` |
| `on_error` | 重试失败后的备用阶段 | `revise`、`report-failure` |

常用引用：

- `workflow.input`：原始工作流请求。
- `params.<name>`：当前节点参数。
- `stages.<stage>.outputs.<name>`：上游阶段声明的输出。
- `stages.<stage>.output_values.<name>`：可用时保留 JSON 类型的上游输出。
- `stages.<stage>.result.output`：兼容旧图的上游原始输出。
- `result.output`、`result.summary`、`result.findings`、`result.tool_results`、`result.changes`、`result.verification`：当前执行阶段可用于 `outputs`、artifact 和验收条件的结果引用。

推荐写法：

1. 用 `input_gate` 或 planning agent 收集目标、范围、输出要求。
2. 从 planning 节点发布少量稳定输出，例如 `plan` 和 `scope`。
3. 下游实现、审计或领域专家节点只消费这些命名输出。
4. 用 `condition`、`switch` 或 `policy_guard` 根据结构化输出路由。
5. 把最终报告、发现、diff、证据声明为 `artifacts`，避免前端从自然语言里猜。

## 对照 Studio 右侧面板填写

如果你是在 Web Studio 里编辑节点，可以把右侧面板简单理解成三块：

1. 基础信息：这个节点是谁来做、做什么、接到什么输入、做完去哪里。
2. 高级行为：这个节点失败怎么办、怎么分支、是否暂停审批。
3. 结果与证据：这个节点做完以后，要把哪些结果正式发布给下游，哪些内容要保留成可回放证据。

大多数普通节点一开始只需要填这 5 个核心字段：

- `agent`
- `skill`
- `input`
- `outputs`
- `next`

只有当你真的需要“分支”“重试”“兜底”“审批”“证据”时，再去展开右侧的高级区和结果区。

### 基础信息怎么填

最常见的一个执行节点，最小可用写法通常是：

```yaml
- name: plan
  node_type: agent
  agent: planner
  skill: execution-plan
  input:
    request: workflow.input
  outputs:
    plan: result.output
  next: [implement]
```

这里分别表示：

- `agent`：谁来执行，决定模型、权限和工具边界。
- `skill`：按什么方法执行，决定流程和输出风格。
- `input`：给这个节点看的上游信息。
- `outputs`：这个节点做完后，正式对外公布哪些结果名。
- `next`：正常完成后进入哪个节点。

如果你不知道 `input` 和 `outputs` 怎么写，就先记住这一条：

- `input` 左边写“你想给本节点取的局部变量名”
- `input` 右边写“从哪里取值”
- `outputs` 左边写“下游以后要怎么称呼这个结果”
- `outputs` 右边写“当前节点结果里的哪个字段”

例如：

```yaml
input:
  target_url: stages.collect.outputs.target
  scope: stages.collect.outputs.scope
outputs:
  evidence: result.output
  summary: result.summary
```

这表示：

- 把 `collect` 节点产出的 `target` 作为当前节点里的 `target_url`
- 把 `collect` 节点产出的 `scope` 作为当前节点里的 `scope`
- 当前节点完成后，把主结果发布成 `evidence`
- 把摘要发布成 `summary`

### 高级行为怎么填

Studio 里的“高级行为”主要解决 5 件事：

- 成功后怎么继续：`next`
- 有多个候选下一步时怎么选：`next_strategy`
- 失败后是否重试：`retry.max_attempts`
- 重试后还是失败怎么办：`on_error`
- 进入这个节点前是否必须人工确认：`approval`

如果你没有明确需求，这一块可以大部分留空。推荐规则是：

- 单一路径就只写 `next`
- 有真假分支就用 `condition + routes`
- 有多路分支就用 `switch_on + cases`
- 写文件、执行命令、外部副作用节点加 `approval: true`
- 可能偶发失败的节点加 `retry.max_attempts`
- 高风险流程给失败路径补 `on_error`

最常见示例：

```yaml
approval: true
retry:
  max_attempts: 2
on_error: [report-failure]
next: [verify]
```

这表示：

- 进入节点前先审批
- 最多自动重试 2 次
- 还是失败就跳到 `report-failure`
- 成功后进入 `verify`

`next_strategy` 只在“存在多个候选后续节点”时才值得填。常见理解：

- `select`：从多个候选里选一个
- `best`：根据结果选最优分支
- `conditional`：结合条件决定
- `dynamic`：运行时动态决定

如果你的节点本身已经是 `condition`、`switch`、`router` 这类控制流节点，通常优先直接写它们自己的 `routes` / `cases`，而不是再额外堆 `next_strategy`。

控制流字段的最小用法：

```yaml
condition: contains(stages.audit.outputs.report, "high")
routes:
  true: verify
  false: handoff
```

或：

```yaml
switch_on: stages.collect.outputs.domain
cases:
  software: plan-code
  web-security: web-review
  default: general-plan
```

### 结果与证据怎么填

Studio 里的“结果与证据”主要对应 3 组字段：

- `outputs`：给后续节点继续消费的正式输出
- `artifacts`：给运行结果页、导出、回放、审批查看的证据产物
- `acceptance_criteria`：给 `quality_gate` 或复核阶段使用的验收条件

这三者的区别很重要：

- `outputs` 是“工作流内部继续传值”
- `artifacts` 是“运行结束后还要保留并展示的结果/证据”
- `acceptance_criteria` 是“怎么判断这个节点结果合格不合格”

#### 1. `outputs`

最常见：

```yaml
outputs:
  report: result.output
  summary: result.summary
  findings: result.findings
```

意思是把当前阶段结果中的这些字段，正式命名后发布给下游节点。

#### 2. `artifacts`

只有当这个节点确实产出了“报告、证据、差异、日志、发现”这类值得保留的内容时才添加。

```yaml
artifacts:
  - name: audit-report
    kind: report
    title: 审计报告
    ref: result.output
    summary: result.summary
```

建议这样理解每个字段：

- `name`：稳定 ID，后面界面和回放会靠它识别
- `kind`：类别，如 `report`、`evidence`、`diff`、`log`、`finding`
- `title`：给人看的标题
- `ref`：正文或主要内容来自哪里，常见是 `result.output`
- `summary`：简短摘要来自哪里，常见是 `result.summary`

如果你只是想“让下游用这个结果”，只写 `outputs` 就够了；如果你还想“让审批页、运行历史、导出结果里也能稳定看到它”，再补 `artifacts`。

#### 3. `acceptance_criteria`

用于声明“这个节点做成什么样才算通过”。

```yaml
acceptance_criteria:
  - name: has-scope
    ref: result.output
    contains: scope
```

常见思路：

- 是否包含关键字段
- 是否生成了报告
- 是否存在发现
- 是否写出了证据产物
- 是否满足某个最低风险/评分阈值

如果后面要接 `quality_gate`，建议上游节点至少声明一部分 `acceptance_criteria`，否则质量门禁只能根据有限上下文猜测是否通过。

### 新手推荐的最小套路

如果你还不熟，先按这个固定模式写：

1. `input_gate`：收集结构化输入
2. `agent/skill`：做计划
3. `agent/skill/tool`：执行或分析
4. `quality_gate` 或 `policy_guard`：判断是否通过
5. `end`：结束

对应到字段就是：

```yaml
input:
  request: workflow.input
outputs:
  main_result: result.output
next: [next-stage]
```

然后只在下面这些场景再逐步加复杂度：

- 要分真假路径：加 `condition` 和 `routes`
- 要分多条路径：加 `switch_on` 和 `cases`
- 要人工确认：加 `approval: true`
- 要失败兜底：加 `retry.max_attempts` 和 `on_error`
- 要保留报告/证据：加 `artifacts`
- 要做质量门禁：加 `acceptance_criteria`

## 各节点怎么用

最简单的理解方式是：

```text
收集输入 -> 执行工作 -> 发布输出 -> 分支/门禁 -> 生成产物
```

执行节点通常需要 `agent` 和 `skill`。控制流节点通常读取上游输出，然后通过
`routes`、`cases` 或 `next` 选择后续节点。

### `start` 与 `end`

只做可视化标记，不调用模型。

```yaml
- name: start
  node_type: start
  next: [collect]
- name: end
  node_type: end
```

常用字段：

- `next`：起点连到哪个节点。
- `position`：画布位置，不影响执行。

### `input_gate`

用于让用户先填写结构化输入，比如目标 URL、范围、严重性、输出类型、发布时间窗口等。

```yaml
- name: collect
  node_type: input_gate
  params:
    manual: true
    prompt: Provide target and scope.
    fields: target:url:Target URL:required,scope:text:Scope:required
    strict: true
  next: [plan]
```

常用参数：

- `manual: true`：暂停等待用户输入。
- `prompt`：给用户看的提示。
- `fields`：简写字段，格式是 `name:type:label:required`。
- `fields_json`：更复杂的 JSON 表单 schema。
- `required`：必填字段名，多个用逗号分隔。
- `strict: true`：拒绝未声明字段。

下游引用：

- `stages.collect.outputs.target`
- `stages.collect.outputs.scope`

### `agent`

普通 LLM 阶段。适合规划、实现、审计、文档、客服、领域分析等。

```yaml
- name: plan
  node_type: agent
  agent: planner
  skill: execution-plan
  input:
    target: stages.collect.outputs.target
    scope: stages.collect.outputs.scope
  outputs:
    plan: result.output
    summary: result.summary
  next: [implement]
```

常用字段：

- `agent`：从 `/agents` 中选择，例如 `planner`、`software-engineer`。
- `skill`：从 `/skills` 中选择，例如 `execution-plan`。
- `input`：这个节点需要看的上游数据。
- `outputs`：发布给下游的稳定变量名。
- `approval`：高风险阶段填 `true`。
- `retry.max_attempts`：失败后重试次数。
- `on_error`：重试失败后跳到哪些节点。

### `skill`

当“流程/能力”比 Agent 身份更重要时使用。仍然需要 `agent`，因为权限来自 Agent。

```yaml
- name: audit
  node_type: skill
  agent: auditor
  skill: code-audit
  input:
    changed_files: stages.implement.outputs.changes
  outputs:
    findings: result.findings
    audit_report: result.output
  next: [quality]
```

常见 Skill：

- `code-audit`
- `web-vulnerability-research`
- `binary-vulnerability-research`
- `reverse-engineering`
- `execution-plan`

### `tool`

用于强提示某个阶段优先使用某个 MCP 工具。注意：`tool` 只是提示和元数据，不会绕过 Agent 权限。

```yaml
- name: write-report
  node_type: tool
  agent: fixer
  skill: code-writing
  tool: file_tools/write_file
  input:
    report: stages.audit.outputs.audit_report
  approval: true
  outputs:
    tool_results: result.tool_results
  next: [end]
```

常见工具：

- `file_tools/read_file`
- `file_tools/search_files`
- `file_tools/write_file`
- `web_tools/web_search`
- `web_tools/fetch_url`
- `web_tools/fetch_page_assets`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

### `team`

用于引用可复用多 Agent 团队模板。默认只发布团队上下文；设置 `params.execute: "true"` 后，会展开并执行团队角色。

```yaml
- name: team
  node_type: team
  params:
    team: software-task-team
    execute: "true"
    approval_preset: software-review
  input:
    goal: workflow.input
  outputs:
    handoff: result.summary
  next: [team-gate]
```

常用参数：

- `team`：团队模板名，例如 `software-task-team`、`web-research-team`、`binary-triage-team`。
- `execute`：`"true"` 表示执行团队角色；不填则只输出团队上下文。
- `approval_preset`：给 `team_approval_gate` 使用的审批预设。

### `condition`

二选一分支。表达式为真走 `routes.true`，否则走 `routes.false`。

```yaml
- name: has-risk
  node_type: condition
  condition: contains(stages.audit.outputs.audit_report, "High")
  routes:
    true: verify
    false: report-clean
```

常见表达式：

- `exists(stages.audit.outputs.findings)`
- `contains(stages.audit.outputs.audit_report, "vulnerability")`
- `len(stages.collect.outputs.targets) > 0`
- `risk_rank(stages.audit.outputs.risk) >= 4`
- `stages.collect.outputs.domain == "web-security"`

### `switch` / `router`

多分支路由。适合按领域、严重性、输出类型分流。

```yaml
- name: route-domain
  node_type: switch
  switch_on: stages.collect.outputs.domain
  cases:
    software: plan-code
    web-security: web-review
    binary: binary-triage
    docs: docs-update
    default: general-plan
```

常用字段：

- `switch_on`：要判断的引用。
- `cases`：值到目标节点的映射。
- 建议始终写 `default`、`else` 或 `*`。

### `policy_guard`

可复用门禁。适合风险阈值、团队审批、通用规则判断。

```yaml
- name: risk-gate
  node_type: policy_guard
  params:
    rule: risk_at_least
    ref: stages.audit.outputs.findings
    minimum: high
  routes:
    allow: verify
    deny: report
```

常见规则：

- `expression`：执行 `policy` 或 `condition`。
- `ref_truthy`：`params.ref` 有值即通过。
- `contains`：`params.ref` 包含 `params.needle` 即通过。
- `min_count`：`len(params.ref) >= params.minimum` 即通过。
- `risk_at_least`：风险等级达到阈值即通过。
- `team_approval_gate`：团队审批满足预设即通过。

自定义 Policy Rule 存放在 `policies/workflow_rules/*.yaml`。如果不想从空 YAML 开始，可以使用脚手架 preset：`GET /api/resources/policy-rules/scaffolds`、`GET /api/resources/policy-rules/scaffolds/{preset}?name=<rule-name>`、`POST /api/resources/policy-rules/scaffolds/{preset}`，或者 CLI `/new-policy-rule <preset> <name>`。内置 preset 从 `internal/scaffold/templates/policies/scaffolds/presets.yaml` 内嵌进二进制，运行目录可以通过 `templates/policies/scaffolds/*.yaml` 添加或覆盖。`/api/workflow-options` 里的内置规则 metadata 也已经文件化，来自 `internal/agent/templates/policy_rules/*.yaml`；它只负责编辑器展示和参数说明，真正的自定义可执行规则仍然放在 `policies/workflow_rules/*.yaml`。

### `quality_gate`

质量门禁。通常放在实现、审计、报告之后，用于检查验收标准和产物证据。

```yaml
- name: quality
  node_type: quality_gate
  input:
    report: stages.audit.outputs.audit_report
  params:
    min_score: "80"
  routes:
    pass: handoff
    fail: revise
```

建议上游执行节点声明 `acceptance_criteria` 和 `artifacts`，否则质量门禁只能基于有限上下文判断。

### `parallel` 与 `join`

用于并行分支，然后汇合。

```yaml
- name: split
  node_type: parallel
  next: [research, audit]
- name: research
  agent: planner
  skill: execution-plan
  next: [join]
- name: audit
  agent: auditor
  skill: code-audit
  next: [join]
- name: join
  node_type: join
  params:
    wait_for: research,audit
  next: [synthesize]
```

常用参数：

- `parallel.params.concurrent: true`：只在安全分支形状下启用真正并发。
- `join.params.wait_for`：等待哪些分支，多个用逗号分隔。

### `for_each`

对列表里的每个元素执行一次 body 节点。

```yaml
- name: each-target
  node_type: for_each
  params:
    items_ref: stages.collect.outputs.targets
    stage: review-one
  next: [summarize]
- name: review-one
  agent: auditor
  skill: code-audit
  outputs:
    finding: result.output
```

常用参数：

- `items`：逗号/换行文本或 JSON 数组。
- `items_ref` / `ref`：引用上游列表。
- `stage` / `body`：要重复执行的节点。

body 节点会收到 `iteration.item`、`iteration.index` 等迭代变量。

### `loop`

有上限的循环改进。必须设置最大次数。

```yaml
- name: improve-loop
  node_type: loop
  params:
    stage: revise
    until: contains(previous.raw_output, "ready")
    max_iterations: "3"
  next: [handoff]
- name: revise
  agent: fixer
  skill: code-writing
  approval: true
```

常用参数：

- `stage` / `body`：循环体节点。
- `until`：每轮结束后判断的表达式。
- `max_iterations`：硬上限。

### `sub_workflow`

调用另一个工作流作为子流程。

```yaml
- name: binary-subflow
  node_type: sub_workflow
  params:
    workflow: binary-triage
    request: stages.collect.outputs.target
  outputs:
    sub_run_id: result.sub_run_id
    summary: result.summary
  next: [handoff]
```

常用参数：

- `workflow`：内置或自定义 workflow 名称。
- `request`：传给子工作流的文本或引用。

如果子工作流暂停，父工作流也会暂停，直到子工作流恢复。

### `checkpoint`

人工检查点，不调用模型。

```yaml
- name: approve-release
  node_type: checkpoint
  params:
    prompt: Approve release execution?
  next: [release]
```

常用参数：

- `prompt` / `message` / `reason`：展示给操作员的审批说明。

## 完整串联示例

这个工作流会收集目标 URL，抓取网页资产，审计风险，根据风险门禁分支，然后生成报告：

```yaml
name: web-risk-review
description: Collect a URL, inspect web assets, audit findings, and route by risk.
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target:url:Target URL:required,scope:text:Scope:required
      strict: true
    next: [fetch]

  - name: fetch
    node_type: skill
    agent: web-security-researcher
    skill: web-vulnerability-research
    input:
      target_url: stages.collect.outputs.target
      scope: stages.collect.outputs.scope
    outputs:
      evidence: result.output
      summary: result.summary
    artifacts:
      - name: collected-assets
        kind: evidence
        title: Collected web assets
        ref: result.output
    next: [audit]

  - name: audit
    node_type: skill
    agent: security-researcher
    skill: vulnerability-research
    input:
      evidence: stages.fetch.outputs.evidence
    outputs:
      findings: result.findings
      report: result.output
    acceptance_criteria:
      - name: has-scope
        ref: result.output
        contains: scope
    next: [risk-gate]

  - name: risk-gate
    node_type: policy_guard
    params:
      rule: risk_at_least
      ref: stages.audit.outputs.findings
      minimum: medium
    routes:
      allow: verify
      deny: clean-report

  - name: verify
    node_type: agent
    agent: auditor
    skill: code-audit
    input:
      findings: stages.audit.outputs.findings
    approval: true
    outputs:
      verification: result.verification
    next: [handoff]

  - name: clean-report
    node_type: agent
    agent: documentation-specialist
    skill: code-writing
    input:
      report: stages.audit.outputs.report
    outputs:
      report: result.output
    next: [handoff]

  - name: handoff
    node_type: quality_gate
    input:
      audit_report: stages.audit.outputs.report
      verification: stages.verify.outputs.verification
    routes:
      pass: end
      fail: clean-report

  - name: end
    node_type: end
```

## 内置多领域模板

`multi-domain-intake-router` 是推荐的新手入口模板。它会先收集结构化需求，再由强模型规划节点把请求拆成有边界的领域切片，只激活被选中的领域 worker 并行执行，然后动态 join 这些分支，把 compact report 和 artifact 引用交给综合、审计、质量门禁和最终交接节点。当前可选分支覆盖：

- 软件研发：`software-task-team`
- Web 安全：`web-research-team`
- 安全研究：`audit-security-team`
- 二进制分析：`binary-triage-team`
- 文档：`documentation-team`
- 运维：`operations-runbook-team`
- 客服：`customer-support-team`
- 框架二开：`framework-extension-team`
- 通用兜底：`general-worker`

普通模式主要展示需求录入、选中的领域、质量状态和最终交接；专家模式会展开 `active_branches_ref`、`wait_for_ref`、branch contracts、模型路由、上下文预算、artifact refs 和 `team_template_ref`。这样它不是固定跑所有领域，而是用强模型做拆解和质量控制，用低成本 worker 做边界执行，避免为了工程化而浪费 token。用户可以从它开始 fork，再替换其中的领域 worker、团队模板引用、技能、工具或门禁规则。

`complex-project-delivery` 是复杂实现任务的推荐模板。它会把用户输入先转换为需求分析、功能需求、验收标准和项目计划，并通过检查点等待用户确认。确认后进入可持久运行的交付循环：每轮选择一个计划切片实现、验证、审查并更新计划，直到迭代输出声明 `PROJECT_COMPLETE`。循环结束后会执行整体验证，并输出包含已交付需求、变更文件、验证证据、剩余风险和产物引用的最终完成报告。需要工程化拆分、并行执行、最终汇总的复杂任务，可以使用 `engineering-parallel-delivery`：强模型节点负责任务拆解、汇总和审计，轻量模型节点执行小切片，并用 `model.max_tokens`、`context.max_tokens`、`context.prompt_max_tokens`、`context.request_max_tokens`、`context.inputs_max_tokens`、`context.parameters_max_tokens` 和摘要/产物引用控制 token。边界清晰但仍需要完整交付闭环的任务，可以优先使用 `plan-implement-audit`。

复杂工程任务建议采用“质量优先的省 token”模式：强模型先拆成有归属边界的小任务，worker 阶段使用 `worker_contract: engineering_v1` 产出稳定 JSON 字段（`summary`、`changed_files`、`evidence`、`verification`、`blockers`、`next_actions`），并把完整报告保存为 artifact；汇总和审计阶段只消费摘要、变更文件、验证结果、阻塞项和 artifact 引用。这样普通用户在普通模式里主要选择模板、资源和预设，二开开发者在专家模式里再调整模型路由、上下文引用、prompt 预算、输出映射、验收条件和运行诊断。

工作流模板列表会返回可视化选择器需要的构成信息：`node_types`、`agents`、`skills`、`tools`、`team_templates`、`policy_rules`、`has_control_flow`、`has_data_flow`、`has_approval` 和 `has_quality_gate`。Studio 会把这些字段展示成“节点构成、引用资源、能力标签”，让用户在应用模板前就知道它会创建什么、依赖哪些资源、是否包含分支、数据传递、审批或质量门禁。

所有内置模板都按“可交付蓝图”维护，而不是松散示例。内置 Workflow Template 必须能通过图校验，包含明确结束节点，产出报告或证据产物，声明验收标准，经过 `quality_gate` 或 `policy_guard`，并最终收口到面向用户的报告、交接、发布摘要或可落地的工作流草案。这样简单起步模板也能作为复杂任务的稳定基础。

## Durable Run

浏览器运行 workflow 时应使用后台 durable run：

- 启动接口立即返回 `run_id`。
- 刷新页面或切换板块不会取消 run。
- 取消只能通过显式 cancel。
- 客户端可通过 SSE 重新连接。
- retry、input、approve、approve-tools 写入同一个 run 快照。

## 审批边界

Stage approval 在阶段开始前暂停：

```yaml
- name: implement
  agent: fixer
  skill: code-writing
  approval: true
```

Tool approval 来自 Agent profile 和 tool policy。一个阶段开始后，工具调用仍可能再次触发审批。

## 编写建议

1. 每个阶段只负责一个清晰角色。
2. 写文件或执行命令的阶段加 `approval: true`。
3. `next_strategy: select` 只用于真实分支。
4. 显式声明 `next`，避免无限扩散。
5. 用 `outputs` 和 `input` 传递关键结果。
6. 为长任务加入 checkpoint、quality gate 和 retry。
7. 先用小任务试跑，再放到复杂任务中。
