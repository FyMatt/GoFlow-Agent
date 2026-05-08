# 工作流图

[English](./workflows.md) | [简体中文](./workflows.zh-CN.md)

GoFlow 支持两类工作流：

- 内置工作流，例如 `plan-fix-audit` 和 `skill-chain`。
- runtime home 下的图工作流：`workflows/<name>/workflow.yaml`。

当任务需要命名阶段、明确 Agent 分工、分支选择、审批边界、控制流或输出传递时，应使用工作流图。

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

Studio 应支持：

- 列出内置和自定义 workflow。
- 创建自定义 workflow。
- 拖拽节点到画布。
- 拖动节点位置。
- 自定义连线。
- 编辑节点属性。
- 保存到 `workflows/<name>/workflow.yaml`。
- 运行 workflow 并显示 stage、token、approval、artifact、diff 和事件流。

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

## 输出传递

工作流应支持把前一个节点输出作为后一个节点输入：

```yaml
outputs:
  plan: result.output
input:
  approved_plan: stages.triage.outputs.plan
```

这样工作流不是简单串行执行，而是真正协作。

## 内置多领域模板

`multi-domain-intake-router` 是推荐的新手入口模板。它会先收集结构化需求，然后根据领域路由到对应的 Team Template，并把团队输出传给综合、质量门禁和最终交接节点。当前覆盖：

- 软件研发：`software-task-team`
- Web 安全：`web-research-team`
- 安全研究：`audit-security-team`
- 二进制分析：`binary-triage-team`
- 文档：`documentation-team`
- 运维：`operations-runbook-team`
- 客服：`customer-support-team`
- 框架二开：`framework-extension-team`

这个模板适合用来展示 Agent、Skill、Tool、Team、Workflow Template、Policy Rule 和 Kit 如何在同一条工作流里联动。用户可以从它开始 fork，再替换其中的领域团队、技能、工具或门禁规则。

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
