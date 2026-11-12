# 研究类子代理隔离设计（草案）

## 一、问题

研究领域的成本结构与其他领域不同：主要开销不是"改代码"，而是**把多个来源的正文读进同一个上下文**。

实测（2026-11-10 02:04 通过轮）：20 次调用、25 万输入 token、8.1 万未命中、2.1 万输出。其中抓取来源正文占了未命中的大头；后续每个 claim 都要引用这些正文，模型倾向于反复回看。

根因：**所有来源共享一个上下文**。来源越多，后面每一轮都要重发前面所有来源的内容。

## 二、目标

主链路只持有**结构化结论**（每来源：id、url、状态、published_at、content_sha256、关键摘录 ≤200 字符），来源正文只存在于隔离的子上下文中。

预期：未命中输入下降 30–50%，主链路调用数不增。

## 三、两种落地方式

### 方式 A：用现有迭代节点做隔离（推荐，改动小）

已确认工作流图**原生支持 `for_each` 节点**（`workflow_graph.go` 的 `workflowGraphRepeatKind`），配置形态：

```yaml
- name: per-source
  node_type: for_each
  params:
    stage: fetch-source        # 重复执行的 body 阶段
    items_ref: stages.catalog.outputs.urls
```

把"逐来源抓取"改成循环：

```
catalog → loop(per source) → source-fetch(子上下文) → 结构化结果 → verify → report
```

- `source-fetch` 阶段：`disable_tools: false`、`max_iterations: 3`、输出契约强制 JSON 字段；
- 每次迭代是**全新 agent 上下文**（本阶段只看到当前来源），天然隔离；
- 父阶段只拿每次迭代的结构化输出（工作流已有 `outputs` 传递）。

改动范围：`parallel-research-review.yaml` 增加 loop + fetch 阶段，report 阶段输入改为聚合结果。**不动内核。**

风险：迭代节点的迭代上限与预算相互作用，需要限制 `max_iterations` 与 `max_llm_calls`。

### 方式 B：新增子代理原语（改动大，通用性更强）

在 `AgentRunner` 增加 `spawn_subagent(task, schema)`：子代理独立上下文、独立预算，返回结构化 JSON，父上下文只插入结果。

- 新增工具 `agent_tools/spawn_subagent`（kind=read，结果小）；
- 子代理共享同一 runtime/workspace，但不共享消息；
- 预算：子代理调用计入运行总量，受 `hard_llm_calls` 约束。

优点：任何领域都能用（研究、审计、批量核验）；缺点：需要新工具、新预算传递、新测试，改动面大。

## 四、建议

1. 先用**方式 A** 把研究领域做成样板（只改工作流 YAML + 验收）；
2. 若方式 A 的收益确认（未命中下降 ≥30%），再抽象成方式 B 的通用原语。

## 五、验收方式

- 同一案例跑两次（隔离前/后），比较：未命中 token、调用数、来源覆盖率与哈希一致性；
- 验收器（P7）继续用现有材料，不需要改。

## 六、与已有改动的关系

- 已实现的"定位优先"（`read_file_outline`、`search_files context_lines`）减少的是**单次读取体积**；
- 本设计的子代理隔离减少的是**上下文累积**；
- 两者叠加才是研究领域的完整降本方案。
