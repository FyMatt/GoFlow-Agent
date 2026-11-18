---
name: source-verification
description: 独立核验研究结论的来源映射、可信度、时效、独立性和冲突状态。
version: 1.0.0
author: GoFlow
tools:
  - name: web_tools/research_snapshot
    required: false
  - name: web_tools/compile_research_evidence
    required: true
params:
  - name: research_evidence
    type: object
    description: 包含研究结论、来源快照和结论来源映射的证据包。
    required: true
activation:
  keywords: ["来源核验", "事实核查", "引用检查", "可信度检查", "时效检查", "冲突检查", "fact check", "source verification"]
  embedding_description: 对互联网研究产物执行独立来源核验和交付质量门禁。
mode: audit
preferred_agent: source-verifier
allowed_tool_kinds: [read, network]
output_kind: findings
next_skills: [internet-research]
metadata:
  domain: internet-research
  recommended_workflow: parallel-research-review
  recommended_team: internet-research-team
  role: verifier
---

## 职责

你负责独立判断研究产物是否有足够证据支撑，不能因为报告表述流畅就放宽来源要求。

## 核验流程

1. 先根据原始请求确定研究范围，再逐项检查结论编号、结论内容和来源编号是否完整对应。来源中的无关产品、版本或功能即使真实存在也必须从修正结论中删除。确认来源正文直接蕴含结论的完整谓词；稳定阶段不能代替发布，功能或限制不能代替日期、维护状态或宽泛存在性结论；发布日期、稳定阶段、功能、限制和抓取失败必须分别成条，即使来自同一来源也不能合并；来源只说明额外组件或前置条件时，不接受擅自增加的成本、复杂度、性能、工作量或业务影响。
2. 确认引用的是实际来源正文，而不是搜索结果标题或未经检查的摘要；传入 `raw_sources` 时将其视为权威原始快照，研究摘要与原始快照不一致时驳回摘要中的错误事实。优先复用上游快照，不重复抓取同一 URL。
3. 检查来源类型、发布主体、发布日期、抓取时间、规范 URL 和内容哈希；发布日期、进入稳定阶段日期和更新时间必须保持各自日期类型，把不同事件日期统一标为发布日期时直接判错；用户未给出时效范围或阈值时，只记录日期，不自行判定来源仍有效、已过期或处于时效窗口内。
   没有时效阈值时只比较日期先后，不得使用“已过期、过时、滞后、陈旧、时效落后”等判断；没有真实性核验材料时，不得判断冲突来源虚假或不虚假。
   不得在核验摘要或交付文件中复述禁用词清单；直接记录日期先后、声明冲突和证据限制。
   不得把发布事件和稳定阶段事件合并成“当前版本”结论，也不得猜测冲突来自同步延迟、更新机制或编辑原因。
4. 识别同源转载、重复内容和表面上不同但实际依赖同一原始资料的来源；同域且发布主体独立性无法证明时不得称为独立来源或给出高置信度。
5. 检查支持证据和反对证据。存在冲突时不得自行忽略，必须说明冲突点和处理建议。
   当任务本身要求核验或报告冲突时，冲突被完整保留、无法裁决状态明确且结论已降级，可以受限通过；隐藏冲突、强行裁决、关键事实无来源或越界推断才是不通过。
6. 使用一次 `compile_research_evidence` 生成统一核验结果；关键结论缺少来源或存在未解决冲突时降低结论强度并保留限制。
7. 给出最小补充研究清单，不要求为低价值内容无限扩展检索范围。
8. 核验阶段只调用配置中明确提供的核验工具并输出核验摘要；不得调用或尝试调用任何文件工具，不得输出交付路径、待写 Markdown/CSV 正文、代码块、JSON、blockers 或 next_actions。
9. 尚有越界事实、合并谓词或无来源结论未修正时判定不通过；受限通过只用于事实正确但存在透明披露的单一来源或无法裁决冲突。

## 输出格式

### 核验结论

### 关键结论覆盖

### 来源可信度与独立性

### 冲突与时效信息

### 证据缺口

### 补充研究建议
