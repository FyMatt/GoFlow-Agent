# GoFlow V3 Session Handoff

## 2026-11-12 中午：邮件通知、受控漏洞验收与 Git 历史整理（最新）

### 邮件通知（真实送达）

- 配置：`configs/goflow.yaml` 的 `notifications.email`（host/port/user/password/from/to/tls/subject_prefix/timeout）。
- 实现：`internal/notify` 发送器（STARTTLS / 直连 TLS / 无加密），密码支持 `${ENV}` 与 `secret://`。
- 投递点：工作流运行结束时异步发送，失败只记审计条目，不影响运行结果。
- 测试：`internal/notify/email_test.go`（本地假 SMTP 服务器收信、头部注入清理）、`internal/config/notifications_test.go`（必填校验）。

### 受控漏洞正例 / 误报验收

`scripts/security_detection_acceptance.py`：为两个独立仓库（含漏洞 / 已修复）分别启动独立服务并跑安全审计工作流。

| 用例 | 结果 | 说明 |
| --- | --- | --- |
| 正例（含注入/穿越/硬编码密钥） | 通过 | 报告命中三类问题并引用 `app.py` |
| 负例（已修复） | 通过 | 结论"未发现"，无中高危误报 |

### UI 工作流 verify 收紧

`plan-implement-audit` 的 verify 阶段改为 6 次/轮、8 次整轮、1000 token 输出上限（此前无上限时单次 15 次调用）。

### Git 历史整理

- 本地 127 个提交压成 **1 个提交**（树哈希校验一致：`6fa0fef...`）；
- 备份分支 `backup/pre-squash-20261112` 保留原历史；
- 已推送到 `origin/master`（`e90aeea..7f52733`）。

## 2026-11-12 上午：UI 任务路由与计划切片修复（最新）

### 用户任务失败的真实链路

请求"帮我修改这个web的样式，美化一下，顺便修改布局和内容"：
1. 旧构建下软件 Kit 匹配词只有 `美化页面`/`页面样式`，子串不匹配 → 回退只读的 `chat` 智能体 → 5 次调用白跑、零改动；
2. 路由修好后进入 `plan-implement-audit`，plan 阶段因变更切片校验失败（3 次连续失败）。

### 修复清单

1. 软件 Kit 路由词扩充（美化/样式/布局/动画/页面/前端/博客等）；无匹配但属变更类请求时回退 `software-engineer` 而不是只读 chat。
2. plan 变更切片诊断细化：编号/标题/重复/验证命令/目标路径/依赖/调度逐条报错，并把模型原始切片写入 `%TEMP%/goflow-diagnostics/change-slices-raw.json`。
3. 重叠写切片**自动补依赖**（编译期串行化），不再因计划记账缺失整体失败。
4. `plan-implement-audit` 补工作流预算（soft 40 / hard 64，续跑 1/2）与各阶段调用/输出上限（此前完全没有）。

### 修复后同一任务的完整运行

| 指标 | 值 |
| --- | --- |
| 结果 | completed，DELIVERED / PASS，score 100，verification 16/16，acceptance 6/6 |
| 调用 | 67 |
| 缓存 | 67% |
| 未命中 / 输出 | 275.8k / 50.0k |
| 峰时成本 | 0.975 元（谷时约 0.49） |
| 实际改动 | 7 个文件：static/style.css 重写、base/index/post/about/admin/admin_new 模板重构；修复阶段移除 Google Fonts 外部依赖 |

阶段分布：implement 24（0.337）、verify 15（0.265）、recovery-verify 8、repair 6、audit 2、plan 3、clarify 4。

## 2026-11-12 凌晨：同构建六领域 6/6 通过（最终基线，最新）

### 修完的两个问题

1. **预算耗尽不再无限重试**：`StageBudgetExhausted` 标记 + 工作流接受部分阶段，
   消除了"每次重试只花 1 次调用却永不收敛"的循环（安全曾 88 次不收敛）。
2. **跨领域报告占位**：final-report 现在必须写入 `docs/unified-delivery.md` 并禁止模板占位内容。

### 最终六领域（同一构建，严格 Kit 验收）

| 领域 | 调用 | 缓存 | 未命中 | 输出 | 谷时成本 |
| --- | --- | --- | --- | --- | --- |
| 跨领域 | 18 | 79% | 26.6k | 11.4k | 0.07 |
| 开发 | 40 | 61% | 196.7k | 31.2k | 0.33 |
| 文档 | 20 | 65% | 69.9k | 14.3k | 0.13 |
| 运维 | 24 | 72% | 49.2k | 16.9k | 0.12 |
| 资料核验 | 19 | 64% | 77.4k | 26.2k | 0.19 |
| 安全 | 33 | 63% | 128.8k | 32.5k | 0.26 |
| **合计** | **154** | — | — | — | **1.10** |

对比早期：单领域 0.79–2.25 元 → 0.07–0.33 元；调用 60–100 → 18–40。

### 注意

- 最后一次批量运行时，文档/运维/资料核验三次**能力探测失败**（`no P7 task was counted`），
  重跑后全部通过；这属于基础设施瞬时问题，不是任务失败。
- 开发领域仍是最大单项（0.33，占 30%），主要是 implement 的真实读写量。

## 2026-11-11 晚：谷时六领域验收 4/6 与阶段计数持久化（最新）

### 谷时验收（19:41 起，约 1.8 元）

| 领域 | 结果 | 调用 |
| --- | --- | --- |
| 文档 | ✅ | — |
| 运维 | ✅ | — |
| 资料核验 | ✅ | — |
| 开发 | ✅ | — |
| 跨领域 | ❌ | 40 |
| 安全 | ❌ | 80（repair 56） |

### 两个失败的原因

1. **安全**：阶段预算的计数依赖 `model_call` 事件，而事件窗口只有 200 条；长运行里早期事件被
   reasoning 挤掉 → 计数偏小 → 上限永不触发。**已改为会话内持久计数**（`StageCalls` 字段 +
   `IncrementWorkflowRunStageCall`），不再依赖事件窗口。待验证。
2. **跨领域**：中途 `audit` 判定"未通过"被记录为 acceptance 失败，虽然 `final-quality` 100 分通过，
   验收器仍按历史失败判整例失败。**这属于验收口径问题**（历史失败 vs 最终状态），需要单独设计：
   只按最终状态计分，或允许后续审计覆盖前次结论。

### 状态

余额 0.57 元，两个修复（持久计数、跨域口径）待下一轮验证。

## 2026-11-11 傍晚：合并微阶段的尝试与两个预算漏洞（最新）

### A+B 合并（审计+报告）尝试：回滚

按设计对开发工作流做了"删初审 audit、删 final-report、recovery-audit 兼写报告"的改造，
结果 **8 处契约测试失败**（模板目录、路由、证据、fixture）。收益约 11%，维护成本过高，**已回滚**。
结论：微阶段合并必须配套一轮完整的契约与验收器改造，不建议在收尾阶段做。

### 顺带修掉的两个真实漏洞

1. **审计输出上限过紧**：`max_tokens: 1000` 截断必填章节 → 运行时判未完成 → 重试 21 次。
   已恢复 1400 并将整轮上限放宽到 6。
2. **审批恢复路径绕过阶段预算**：安全 `repair` 带 `approval: true`，其续跑走
   `continueAgentRunWithModelSkillOptionsAndResults`，该路径未携带 `max_model_calls`，
   导致 repair 跑到 54 次。已在该路径补齐预算参数。

### 最新单领域（修复后）

| 领域 | 调用 | 缓存 | 未命中 | 输出 | 结果 |
| --- | --- | --- | --- | --- | --- |
| 开发 | 48 | 71% | 170.7k | 39.4k | 100 分、Kit 通过 |
| 安全 | 44 | 62% | — | 33.0k | 95 分、Kit 通过 |

其余四个领域在 16:30–16:55 的同一构建上已通过；改动只触及审计上限与审批预算，不影响它们。

## 2026-11-11 傍晚：六领域 6/6 通过（严格验收，最新）

### 输出截断的原因

阶段 `max_tokens` 让提供方以 `finish_reason=length` 结束，运行时把"被截断"视为未完成并**续写/重试**；当时只有"每次进入"的预算，重入即重置，于是从 45 次胀到 94 次。修复：整轮总上限 + 只给审计类阶段加输出上限（动作类不加）。

### 本轮修复清单

1. 阶段预算推广到五个工作流（每轮 + 整轮总）。
2. 调用计数同时参考 `model_call` 事件（`token_usage` 缺 stage 时预算曾失效）。
3. 质量门：参数类/探索类工具失败豁免；允许 `max_tool_errors` 次工具错误。
4. **质量门 warning 直接前进**，不再重开 repair。
5. **`max_repair_rounds`**：两轮修复后按 warning 交付，杜绝 quality↔repair 无解循环。
6. repair 阶段允许"无遗留项时不做修改"（`require_workspace_mutation: false` + no-op 策略）。

### 最终六领域（严格 Kit 验收）

| 领域 | 调用 | 缓存 | 未命中 | 输出 | 峰时成本 |
| --- | --- | --- | --- | --- | --- |
| 跨领域 | 23 | 71% | 48.2k | 14.8k | 0.22 |
| 开发 | 41 | 64% | 177.6k | 33.6k | 0.64 |
| 文档 | 25 | 70% | 75.6k | 16.5k | 0.29 |
| 运维 | 30 | 70% | 76.2k | 22.4k | 0.34 |
| 资料核验 | 24 | 66% | 88.3k | 22.3k | 0.36 |
| 安全 | 41 | 79% | 100.5k | 32.1k | 0.47 |
| **合计** | 184 | — | — | — | **2.32（谷时约 1.16）** |

单领域从早期 0.79–2.25 元降到 0.22–0.64 元。

## 2026-11-11 下午：成本结构拆解与整轮总上限（最新）

### 成本结构（45 次调用那次，峰时 0.684 元）

| 阶段 | 调用 | 未命中 | 输出 | 成本 | 说明 |
| --- | --- | --- | --- | --- | --- |
| implement | 16 | 56.7k | 11.5k | 0.213 | 已撞每轮预算 16 |
| audit | 2 | 11.0k | **13.5k** | 0.130 | 2 次调用写 1.3 万输出 |
| repair | 6 | 28.8k | 4.5k | 0.096 | |
| plan | 7 | 14.9k | 6.2k | 0.081 | |
| recovery-verify | 6 | 21.4k | 2.6k | 0.065 | |
| recovery-audit | 2 | 12.9k | 1.9k | 0.041 | |
| verify | 5 | 11.2k | 2.2k | 0.041 | |
| final-report | 1 | 5.1k | 0.8k | 0.017 | |

**输出 token 合计 43k，占成本约 50%**；缓存命中已到 69%。

### 试过并回滚

阶段 `max_tokens` 输出上限（audit 1200 等）→ 某次运行 recovery-verify 反弹到 36 次、输出 91k、花费 1.91 元、交付阻塞。**已回滚**。

### 新增：整轮总调用上限

`max_total_model_calls` 阶段参数：重试与续跑共享同一预算，超出即强制输出阶段报告。开发工作流设：implement 24、repair 12、verify 10、recovery-verify 8、recovery-audit 4、audit 4、plan 8、final-report 3。

## 2026-11-11 下午：阶段模型调用预算（最新）

### 轮次为什么会大

阶段只有 `max_iterations`（实现 40）和续跑上限，没有"本阶段总共能花多少次模型调用"的硬约束；实现阶段可以 48 次、续跑再叠加。

### 机制

新增 `max_model_calls` 阶段参数（`internal/agent/agent.go`）：

- 计数来自用量账本，按"本次进入阶段"的基线差值计算，因此第二次 repair 仍有自己的预算；
- 超出后置 `finalResponseTurn`，注入"预算已用完，立即输出阶段报告"，不再允许工具调用；
- 同时写入 `stage_model_call_budget` 运行事件，便于事后审计。

### 开发工作流设的预算

plan 6、implement 16、verify 8、repair 8、recovery-verify 6、final-report 3。

### 实测（开发领域）

| 指标 | 无预算（最差样本） | 有预算 |
| --- | --- | --- |
| 调用 | 73 | **45**（implement 精确 16） |
| 缓存 | 56% | **69%** |
| 每次未命中 | 6,752 | **3,600** |
| 评分 | 100 | 100 |
| Kit 验收 | 通过 | 通过 |

## 2026-11-11 下午：补丁结果瘦身与只读参数容错（最新）

### 改动

1. `apply_patch` 结果瘦身：只回状态、行号摘要、新哈希、变更号，不再回显 diff（diff 仍存为变更工件）。
2. 只读工具忽略未声明参数：模型给 `read_file` 传 `target_id` 不再被拒，也不会再触发质量门误判。
3. 每处改动都补了单测（补丁载荷、参数容错 3 个）。

### 开发领域样本（同日，改动前后）

| 时间 | 调用 | 缓存 | 每次未命中 | 结果 |
| --- | --- | --- | --- | --- |
| 12:19 | 42 | 59% | — | blocked（补丁哈希冲突） |
| 12:31 | 73 | 56% | 6,752 | 通过（批量读上限，已回滚） |
| 12:39 | 41 | 64% | 4,269 | 通过 |
| 12:51 | 65 | 70% | 3,281 | 通过（瘦身） |
| 12:57 | 50 | 67% | 4,513 | blocked（只读参数被拒） |
| 13:04 | 37 | 56% | 5,935 | 通过（+参数容错） |

结论：工具错误已归零（`tool_errors=0`）；通过率从 2/6 提升到 4/6（最后 4 次 3 次通过）；剩余失败来自质量门的证据统计，而非真实交付缺陷。

## 2026-11-11 下午：逐阶段成本分析与补丁哈希宽松（最新）

### 逐阶段数据（开发领域，48 次调用那次）

| 阶段 | 调用 | 输入 | 缓存 | 每次未命中 |
| --- | --- | --- | --- | --- |
| implement | 17 | 281k | 47% | **8,621** |
| recovery-verify | 9 | 115k | 71% | 3,673 |
| verify | 8 | 94k | 74% | 3,042 |
| repair | 4 | 63k | 60% | 6,208 |
| audit/final-report | 3 | 17k | 10–17% | 4–5k |

结论：implement 占该领域未命中的 57%，主要来自批量读与补丁回显；微阶段冷启动占比很小。

### 改动与实测

1. **`apply_patch` 哈希宽松**：文件已变但 `old_text` 仍唯一匹配时照样应用，并返回 `sha256_relaxed`。修掉了导致 `final-quality` 误判 73 分阻塞的真实原因（repair 阶段一次 sha 冲突）。
2. **补丁 diff 摘要上限 1200 字符**。
3. **批量读总量上限（600 行）已回滚**：实测会把轮次从 41 推到 73，反而更贵。
4. 研究报告阶段 `max_iterations` 16 → 6 并加"每文件只写一次"约束。

开发领域最新实测：**41 次调用、缓存 64%、未命中 175k（每次 4,269）、100 分、Kit 验收通过、0.22 元**。

## 2026-11-11：并发缺陷修复与严格验收（最新）

### 缺陷修复

开发领域的并发缺陷（模型生成的缓存在并发下丢 key）用三层加固：

1. implement 提示加入并发规则：共享状态同一把锁、Set 后同一 key 必须立即可读、不要持读锁写入；
2. verify 增加 `flaky_rule`：并发改动必须 `go test ./... -count=2` 连跑两遍；
3. `software-engineering-kit.acceptance.verify_commands` 改为 `go test ./... -count=2`。

验证：开发领域 48 次调用、100 分，Kit 验收三项全过（含 `-count=2`）。

### 严格验收进度

| 领域 | 结果 |
| --- | --- |
| 开发 | ✅（含并发修复） |
| 文档 | ✅ |
| 跨领域 | ✅ |
| 运维 | ✅ |
| 安全 | ⏸ 402 余额不足中断（Kit 验收本身通过） |
| 资料核验 | ⏸ 402 在 provider 探测阶段中断 |

余额耗尽（0.28 元），后两个领域需充值后补跑。

## 2026-11-11 中午：收敛检查点、严格 Kit 验收与领域加强（最新）

1. **阶段收敛检查点**：迭代达到 2/3 上限时注入"立即落盘并产出报告"的提示（`deliveryCheckpointNudge`），压轮次方差。
2. **严格 Kit 验收**：`--kit-acceptance` 现在默认严格（失败即判用例失败），`--kit-acceptance-lenient` 可退回只记录。
3. **领域验收加强**：
   - 安全领域补 `deliverables/security-report.md`；
   - 资料核验补 JSON/CSV 解析命令（`json.load(evidence.json)` + `csv.reader(claims.csv)`），实跑验证通过（3 交付物 + 1 命令）。
4. **未做**：合并 audit/final-report 微阶段——实测收益约 4% 且破坏 6 处流程契约，改为用收敛检查点降低长尾轮次。

## 2026-11-11 上午：缓存优化验证（4 个样本）与失败调用熔断（最新）

### 新增代码

- `tool_failure_guard.go`：同一参数第 2 次失败后拦截该调用并提示换策略；4 个单测。
- 交接文档记录验证数据。

### 开发领域 4 个样本（同一构建）

| 指标 | 优化前（4 样本均值） | 优化后（4 样本均值） | 变化 |
| --- | --- | --- | --- |
| 缓存命中 | 55% | **66.5%** | +11.5 点 |
| 每次调用未命中 | 5,549 | **3,768** | **−32%** |
| 输出 token | 41.8k | 35.0k | −16% |
| 调用数 | 44.5 | 49.5 | 无改善（方差 34–78） |
| 通过率 | 3/4 | **4/4** | — |
| 单次成本 | 0.5–0.9 元 | 0.40–1.03 元 | 仍随轮次波动 |

### 结论

缓存与无效调用两项指标稳定改善；**任务级成本仍由轮次方差决定**，熔断只在病态重试时生效。

## 2026-11-11：缓存前缀稳定化与无效调用治理（最新）

### 根因

历史压缩边界**每轮前移 1 条消息**，导致提示前缀持续变化、缓存反复失效。改为**按 8 条分块推进**后，前缀在块内保持稳定。

### 代码改动

1. `pruneMessagesForConversation`：压缩边界按 8 条量化（`internal/agent/prompt.go`）。
2. `policy.EnforceToolPolicy`：拒绝信息补全允许的工具名，模型可一次纠正。
3. `research_snapshot` 最小字节 4096 → 1024，减少参数越界拒绝。
4. 新增不变量测试 `internal/agent/cost_invariants_test.go`（3 个）：
   - 提示工具清单绝不含策略拒绝的工具；
   - 压缩块内前缀逐轮不变、跨块只压一个块；
   - 拒绝信息必须包含允许的工具名。

### 实测（开发领域，同一构建）

| 指标 | 前（4 次样本均值） | 后（2 次样本均值） |
| --- | --- | --- |
| 缓存命中 | 55% | **67%** |
| 每次调用未命中 | 5,549 | **3,830（−31%）** |
| 无效/被拒调用 | 每轮约 10–13 次 | **每轮 2 次**（均为测试保护护栏） |

两次验证均通过（100 分、Kit 验收通过）。调用总数仍有波动（50 与 78），说明**轮次方差**仍是任务成本的主要变量。

## 2026-11-10 晚：六领域同构建验收 6/6 通过（最新）

### 验收结果（谷时，约 1.7 元）

| 领域 | 结果 | 调用 | 未命中 | 输出 | Kit 验收 |
| --- | --- | --- | --- | --- | --- |
| 运维 | ✅ | 36 | 109k | 26.6k | 通过 |
| 安全 | ✅ | 36 | 218k | 34.5k | 通过（改映射后） |
| 跨领域 | ✅ | 23 | 63k | 19.9k | 通过 |
| 资料核验 | ✅ | 42 | 203k | 38.5k | 通过 |
| 文档 | ✅ | 21 | 70k | 14.5k | 通过 |
| 开发 | ✅ | 52 | 257k | 54.0k | **失败：go test 报 missing key after Set** |

### 两个重要发现

1. **Kit 验收抓到了用例没抓到的真缺陷**：开发领域 P7 用例通过、得分 100，但交付物 `go test ./...` 失败
   （`TestConcurrentAccessIsRaceFree: missing key after Set`）。说明用例评分覆盖不到竞态语义，
   Kit 验收是必要的补充。当前 `--kit-acceptance` 非强制；若要拦截此类问题应加 `--kit-acceptance-strict`。
2. **质量门误判已修**：文档领域 final-quality 因修复阶段缺少“复核结论”小节判失败，
   已把输出契约改为强制五个小节标题，重跑通过（0.10 元）。

### 提交

`ff85287`（工具覆盖/Kit 验收/分片对齐/文档）、`424eea3`（文档契约 + Kit 候选映射），共 100 个提交。

## 2026-11-10 傍晚：定位优先与读取复用（最新）

### 已完成

1. **定位优先**：新增 `file_tools/read_file_outline`（函数/类型/类的行号大纲）；`search_files` 支持 `context_lines`（返回命中区间 `start_line/end_line/context`）；`read_file` 默认 300 行 + `next_offset`。提示要求"先定位再按区间读"。
2. **跨阶段读取复用**：阶段提示注入"前序阶段已读文件摘要"（路径/sha256/行数）；同一对话内重复读取折叠为存根（`prompt_read_dedupe.go`）。
3. **成本展示**：界面与运行详情早就有 `budget_estimated_total_cost` 字段，之前显示 0 是因为未配置价格；已补 `configs/providers/primary.yaml` 的 `pricing`。
4. **子代理隔离（研究类）**：设计文档 `docs/subagent-isolation-design.zh-CN.md`，推荐先用迭代节点隔离逐来源抓取，不改内核。

### 待验证

以上四项均未实跑验证（成本结构变化需对比未命中 token）。

## 2026-11-10 下午：拒绝诊断与分片死锁修复（最新）

### 诊断：开发领域一半的调用被拒，且集中在同一分片

新增 `internal/agent/tool_diagnostics.go`，把工具拒绝原因追加到 `%TEMP%/goflow-diagnostics/tool-rejections.log`（运行事件会被 reasoning 挤满 200 条窗口，看不到拒绝）。

一次运行抓到 35 次拒绝：**29 次是同一个分片 `cache-status-page` 未就绪**——它依赖的前置分片一直没完成，模型只会重试同一写入。

### 修复

1. **两次容忍后放宽依赖顺序**：同一分片第 3 次写入放行并记录 `change_slice_started_out_of_order`，正确性交给 verify 阶段。前两次仍拒绝，保持契约（相关测试全部保留）。
2. **依赖已阻塞但已有改动的分片视为完成**，避免死锁。
3. 拒绝原因落文件，便于后续继续定位。

### 效果

| 指标 | 修复前 | 修复后 |
| --- | --- | --- |
| 工具拒绝 | 35 次 | 18 次（不同分片） |
| 开发领域调用 | 60–100 次（失败） | **43 次（通过，100 分）** |
| 开发领域成本（峰时） | 0.79–2.25 元 | **0.73 元** |

### 六领域现状

| 领域 | 最近一次通过 | 调用 |
| --- | --- | --- |
| 运维 | 2026-11-10 01:38 | 35 |
| 安全 | 2026-11-10 01:42 | 37 |
| 跨领域 | 2026-11-10 01:45 | 64 |
| 文档 | 2026-11-10 01:54 | 26 |
| 资料核验 | 2026-11-10 02:04 | 20 |
| 开发 | 2026-11-10 16:37 | 43 |

四个领域在 8KB 读取阈值/两次容忍之前通过，需一轮同构建的完整复验。

## 2026-11-10 凌晨：六领域验收与两处回归修复（最新）

### 验收结果（谷时，共 2.24 元）

| 领域 | 结果 | 说明 |
| --- | --- | --- |
| 运维 | ✅ | — |
| 安全 | ✅ | — |
| 跨领域 | ✅ | — |
| 文档 | ✅ | — |
| 资料核验 | ❌ → ✅ | 首轮漏读 S015–S017、S004/S005 哈希错；根因是 2KB 裁剪截断了来源目录，改读取阈值后重跑通过（0.32 元） |
| 开发 | ❌ | 100 次调用、implement 90 次无写入，分片被预算阻塞 |

### 两处回归修复

1. **裁剪阈值按工具类型区分**：命令输出 2KB，文件读取 **8KB**（头 4KB/尾 1KB）。2KB 会截断 JSON 目录和大文件，导致漏读与重读。
2. **分片未就绪时告知可写分片**：拒绝写入时返回当前可写分片 ID 与目标文件，避免模型反复重试同一路径（实测有 ~74 次调用没有产生任何工具结果）。

### 未解决

开发领域仍不稳定：同一案例在 32–100 次调用之间波动，implement 阶段有时长时间只读不写。已加的守卫能阻止无限打转，但不能保证一次通过。

## 2026-11-09 傍晚：三项降本提案的实测结论（最新）

| 提案 | 实测结果 | 处置 |
| --- | --- | --- |
| 合并 final-report 到 recovery-audit | 省约 4%（5.6k 未命中/轮），但 6 处流程契约测试失败 | **回滚**：收益小于契约维护成本 |
| 阶段输出限额（plan 2000 等） | 输出从 31k 涨到 44.7k，plan 调用 4→16（限额逼其反复重规划） | **回滚** |
| implement 收尾构建检查 | 保留（一次调用换掉整轮返修的设计意图） | 保留，待更大样本验证 |

**保留的有效项**：工具结果 2KB 裁剪、`read_files` 批量读取、阶段输入 1600 token 上限、三处预算/分片守卫、内核领域分支删除、Kit routing + acceptance。

## 2026-11-09：架构级降本、领域分支删除与共用会话实测（最新）

### 一、修掉的三处架构缺陷（141 次打转的完整根因）

1. **预算基线存事件流**：事件列表有 200 条上限，起点被裁剪后预算永不触发。改为用**用量账本按阶段计数**（`2f6a71a`）。
2. **拦了分片但模型继续空转**：分片全部阻塞时阶段没有停止。改为**全阻塞立即结束阶段**（`dc9b803`）。
3. **写操作重开预算阻塞的分片**：形成 block/reopen 死循环。改为**同阶段内不可重开**，repair 阶段仍可重开（`859334b`、`2d0aeac`）。

效果：开发领域从 141 次调用/2.25 元（失败）降到 32–38 次/0.46–0.73 元（通过，评分 100）。

### 二、降本改动

- 工具结果裁剪阈值 6KB → **2KB**（头 1.4KB/尾 0.7KB），每轮新增内容从约 5.5k 降到约 2k。
- 新增 **`file_tools/read_files`** 批量读取（一次多个文件，少一轮就是一次完整提示）。
- 阶段映射输入上限 2600 → **1600 token**。

### 三、Kit 通用性

- 六个领域包补齐 **routing**，入口工作流纳入 `workflows`（否则清单校验不通过）。
- 新增 **`acceptance:` 段**（示例请求、验证命令、必需产物、质量阈值），随 `GET /api/kits/{name}` 返回；扩展指南已同步。
- **删除内核领域分支**：复杂度分析只保留通用信号，领域路由完全由 Kit 合约决定；未匹配的简单任务回退当前智能体，复杂任务要求显式选择（`0a70987`，净减 98 行）。

### 四、实测失败并回滚的两项（已记录，避免重复投入）

| 方案 | 结果 | 处置 |
| --- | --- | --- |
| 关闭思考模式 | 输出 −31%，但调试期调用翻倍、安全领域打转到 251 次 | 回滚，保留可选配置 |
| 系统提示稳定前缀重排 | 缓存无提升、调用 +40% | 回滚 |
| **阶段共用会话** | 缓存 72%→76%，但输入 +18%，总成本变高 | **关闭开关**，保留能力供未来使用 |

探针实测（2026-11-09）：**工具定义不影响缓存**；缓存按 256 token 块对齐；同 system 换 tools 仍命中 67%。

### 五、当前状态

- 回归：`go vet`、`go test ./...`、`gofmt` 干净
- 提交：`cb350ee`，工作区干净
- 缓存命中稳定在 **72–76%**，该架构下已接近上限

## 2026-11-05 傍晚：成本审计与验证阶段误判修复（最新）

### 成本审计（今日 23 次带用量运行，按峰谷价折算）

| 分类 | 次数 | 花费 |
| --- | --- | --- |
| 通过 | 10 | 3.73 元 |
| 未通过/中断 | 13 | 14.29 元 |
| 合计 | 23 | 18.02 元 |

未通过运行占 **79%**，集中在开发领域早期 6 次重跑（203/184/141/99/57/60 次调用）。结论：花在**试错**上的钱远多于花在有效运行上。

### 已验证的降本改动

- 会话裁剪窗口放大到 24 + 原地裁剪 + 标记固定：缓存命中 48.7% → **72%**。
- 输出瘦身：输出 token 下降约 20%，评分保持。
- **验证阶段判定修正**：原先把“尚未执行的检查”直接判为未通过，触发 17 次 repair 返修；修正后运维案例 44 次 → **20 次调用、0 元级返修、100 分**，成本 0.66 → 0.16 元。
- **验收加单次运行调用上限**（预算 2 倍）：超过即中止，避免再出现 255 次调用的失控账单。

### 被证伪的方案（已回滚）

- 关闭思考模式：输出降 31%，但 dev 调用 31→40、安全领域直接打转到 251 次调用，总账更贵。已回滚为供应商默认（思考开启），仅保留可选配置项。

### 当前余额与结论

余额 0.73 元。开发领域仍是重案例（55 次调用、0.84 元，评分 95，卡在自身 efficiency 预算 artifacts 63/60）。进一步降本需要提高首过质量（plan/implement 返修），而不是继续调参。

## 2026-11-05 下午：六领域全部通过 + 成本根因修复（最新）

### 最终结果（均为顶层 passed=true）

| 领域 | 调用 | 评分 | 交付 |
| --- | --- | --- | --- |
| 安全 | 38 | 100 | 4 处安全修复 + 回归 |
| 运维 | 19 | 100 | 分批灰度与回滚 |
| 跨领域 | 61 | 95 | 代码/安全/部署/文档统一交付 |
| 资料核验 | 60 | 95 | 18 来源去重、冲突、503 恢复 |
| 文档 | 24 | 95 | DOCX/XLSX/PPTX/PDF 模板保真 |
| 开发 | 31 | 100 | 配置迁移、并发缓存、中文状态页、CI |

### 成本根因（与 DSH 的差距）

以开发领域为例，修复前 **203 次模型调用 / 216 万输入 token / 约 2 元**；修复后 **31 次 / 29.5 万 token / 约 0.3 元**。根因不是模型单价，而是：

1. **回合数**：每次模型调用平均只做一次工具操作，一个文件一次调用。
2. **缓存命中率低（约 48%）**：历史窗口每轮滑动一格，前缀从被丢弃的消息处变化，供应商前缀缓存失效；可缓存前缀一度只有 1.9k/9.6k。

### 已修

- **跨文件批量编辑工具 `file_tools/apply_edits`**：一次调用改多个文件，任一失败整体回滚；已加入软件工程师、implement、repair 的允许工具列表与预检夹具。
- **历史压缩边界量化（每 8 轮移动一次）**：窗口在 8 轮内字节一致，前缀缓存可命中。
- **单分片模型调用预算（20 次）**：卡住的分片被阻塞并记录消耗，不再吸干整轮。
- **实现阶段约束**：每片最多两轮（批量读 + 批量改），测试移到 verify 阶段统一跑；禁止改既有测试、禁止重名测试函数。
- **门禁修复**：续跑段没有新写入时会误判 “produced no workspace file mutations”，现在会参考分片已实现的变更。

### 回归

`go vet`、`go test ./...`、`gofmt` 干净；Python 210 项通过 / 2 跳过。

## 2026-11-05 凌晨：五领域通过（最新）

### 结果

| 领域 | 顶层 passed | 调用 | 评分 |
| --- | --- | --- | --- |
| 安全 | ✅ | 38 | 100 |
| 运维 | ✅ | 26 | 100 |
| 跨领域 | ✅ | 61 | 95 |
| 资料核验 | ✅ | 60 | 95 |
| 文档/Office | ✅ | 24 | 95 |
| 开发 | 暂缓（按用户要求） | — | — |

### 本轮修掉的真问题

1. **研究夹具的哈希契约不可满足**：评估器要求文件字节哈希，web 工具返回正文文本哈希。改为在 `source-catalog.json` 里发布 `content_sha256`（字节哈希），并要求逐字复制；同时显式写清 `duplicate_of` 用 slug、C-stable 置信度“中/低”规则、CSV 必须逐字等于 JSON、`冲突` 标记、removed-source 的 `http_status` 记恢复后的 200。
2. **研究服务被 SSRF 策略挡住**：新增 `GOFLOW_RESEARCH_ALLOW_LOCAL` 显式开关（默认关闭，仅验收进程开启），并加入 MCP env 白名单。
3. **报告阶段输出上限不足**：`max_tokens` 4000→8000、`max_iterations` 12→16，改为“每个文件单独一轮 write_file”。
4. **跨领域夹具预置“待生成”**：模型局部编辑会留下占位符，改为只留标题，并要求整体覆盖写入。
5. **文档模板路径未写清**：模型自己猜成 `security-data-template.xlsx`，用例改为逐字给出三个模板路径。
6. **工具结果无耗时**：验收证据要求“可观测工具耗时”，Office 工具不返回 `duration_ms`。在 `schema.ToolResult` 增加 `DurationMS`，执行器统一用真实墙钟耗时填充，所有工具与证据链路同时受益。

### 回归

`go vet`、`go test ./...`、`gofmt` 干净；Python 210 项通过 / 2 跳过。

## 2026-11-02 凌晨：降本增效实测（最新）

### 做了什么

- 历史窗口收紧（16→12 条、1600/1200→1200/900 字节）。实测输入 87% 来自历史消息，这是主要成本项。
- 单段迭代：plan 8→12、implement 18→40（8 分片的案例需要更长单段，否则续跑重启反而更贵）。
- 写失败重试上限 3 次/分片/阶段；续跑次数上限 3 次。
- **执行层单次运行模型调用上限 120**：事件流会被保留窗口裁掉，改用持久用量账本 `session.WorkflowUsageRecords` 计数，任何路径都兜得住。

### 实测数据

| 轮次 | 调用 | 输入 token | 结果 |
| --- | --- | --- | --- |
| 失控轮 | 733 | — | 中断，约 3.8 元 |
| 加守卫后 | 242 | 2.44M | 撞 200 次工具上限 |
| 账本上限 120 | 42 | 0.44M | 提前 `incomplete` 暂停（迭代不足） |
| implement 提到 40 | 120 | — | 触发上限暂停（`paused_need_more_budget`），可分片进度 1 完成、2 进行、2 计划 |

### 结论

- 失控已止住：调用数从 733 → 120 封顶，成本可预测（约 0.01 元/次，单轮 1.2 元左右）。
- 但**该复杂案例在 flash 模型下确实需要 120–200 次调用**才能完成 8 个分片；把上限压低只会提前暂停，不会变便宜到"白跑"。
- 真正的降本只有三条路：缩小单次任务（少分片）、换更强的模型减少回合数、或接受"单轮 1–2 元"。
- 余额 1.14 元，本轮实测到此为止。

## 2026-11-01 中午：写入失败可重试与工具预算覆盖缺口（最新）

- **写入用法失败不再阻塞分片**：`recordChangeSliceToolFailure` 原先只把两种失败当可重试（哈希过期、patch 找不到目标），其余一律 `blocked`。复现确认 `expected_sha256 is required when replacing an existing file` 这类**用法错误**会把分片永久阻塞，阶段随即收尾。现在只有权限/边界类失败（denied、permission denied、outside the workspace、symbolic link 等）才阻塞，其余保留为可重试，并把失败原文（截断 2000 字符）写进 `change_slice_retryable_write_failure` 事件，便于复盘。回归 `TestChangeSliceUsageFailureKeepsSliceRetryable`（先禁用规则跑出线上同样的 blocked 原因）。
- **工具预算暂停覆盖不全**：上一轮把“工具操作上限”改成可续跑暂停，但只加在 `runWorkflowGraphQueue` 的阶段错误分支；实测 `20261101-103725` 仍以 `action_failed` 结束（172 次调用触顶），说明错误是从**续跑路径**（`continueWorkflowGraphExecutableStage`）抛出的，走了另一条 `fmt.Errorf("workflow stage %s: %w")`。需要把同样的暂停处理补到续跑路径与 agent 工具执行层。
- 全量回归仍绿：`go vet`、`go test ./...`、`gofmt` 干净，Python 210 项通过 / 2 跳过。
- DeepSeek 余额已降到 1.02 元，本轮之后无法继续实测；六领域重跑还没拿到一轮完整结果。

### 下一步（余额恢复后）

1. 把“工具操作上限”的暂停处理补到续跑路径与 agent 工具执行层（否则长任务仍会以 action_failed 收尾）。
2. 再跑六领域，重点验证：开发/运维/跨领域能否走完 final-report；研究/文档的新契约是否生效。
3. 每轮记录 `evidence_error` 与三源工具计数（event/stage/artifact），判定才可信。

## 2026-11-01 上午：证据计数与工具预算修正（最新）

- **证据计数改为多源取最大**：`collect_run_evidence` 原先只统计 event 流里的 `tool_result`，而事件有保留窗口，长运行会被截断成 0；现在同时统计阶段结果与产物投影，并新增 `event_tool_results` 便于区分。实测开发一轮：event=2、stage=12 → 采用 12，签名样本也从阶段补足到 12。
- **证据采集异常不再被吞**：runner 里 `collect_run_evidence` 的异常原本被 `except Exception: pass` 静默丢弃，现在记录到 `evidence_error` 并写入结果。
- **工具操作预算耗尽改为可续跑暂停**：`implement` 打到 200 次工具上限时原先整轮 `action_failed`，现在返回 `pauseWorkflowForIncompleteStage`，保留已完成的工作并允许“继续执行”。
- 全量回归：`go vet`、`go test ./...`、`gofmt` 干净，Python **210 项通过 / 2 跳过**。

### 重跑观察（开发领域，20261101-070806）

- 9 次调用后 `status=blocked`：`implement` 里对 `cache-ttl` 分片的一次写入失败，该分片被标 blocked，阶段随即结束，没有进入返修。事件流只保留了 read_file 结果，失败写入的原文已被截断，无法直接定位失败原因。
- 这说明“失败写入→分片 blocked→阶段收尾”的链路仍会让流程提前终止；`reopen_blocked_slices` 只在下一次写入尝试时生效，模型放弃后就没有下一次。

### 待办（按优先级）

1. 失败写入后应给出可重试的明确指令，或直接路由到 repair，而不是让阶段带着 blocked 分片结束。
2. 事件保留窗口对失败证据也要保留最近一次失败原文（当前只保留了成功读取）。
3. 研究与文档的产物契约（本轮已补进用例说明）尚未在新版本上重跑验证。
4. 余额约 3.19 元，全量重跑需先充值。

## 2026-11-01 清晨：验收加固与一处更正（最新）

### 验收加固（本轮）

- 评估器新增三项真实约束：
  1. `go build ./...` 必须通过（此前只跑 go test/vet，无测试的包不会被编译）；
  2. 既有公开测试不得被删除或改名（允许扩展与格式化——参考解本身就会改公开测试，最初写成"字节不变"被参考解测试挡下，已改正）；
  3. `required_paths` 必须存在且至少有一个真的被修改过（防止"什么都没改也通过"）。
- 研究与文档用例的契约补全：评估器要求的 `duplicate_of` 映射、`C-conflict` 保留双方且状态含"无法确认"、`C-deployments` 不得声称支持、C-stable 随目录重算 confidence、XLSX 保留汇总页与"完成率"、PPTX 保留"固定母版声明：内部授权交付材料"，此前都没有写进用例说明，属于不公平的隐含契约。

### 一处重要更正

- 我此前把"evaluation 通过（95/100 分）"当成了整例通过。复核 `report.json` 的顶层 `passed` 后确认：**只有开发（20261031-041517）与安全（20261031-193746、20261101-053810）真正 passed=true**；运维与跨领域那两轮交付评价是 100 分，但 harness 的额外判据（工具证据计数、staging 交付）没满足，顶层仍是 false。
- 根因线索：harness 的工具证据计数在部分运行里为 0（`tool_results=1` 这种勉强过线的值也出现过），而采集异常被 `except Exception: pass` 吞掉。已改为记录 `evidence_error`，不再静默丢弃。修完后安全案例重跑通过（38 次调用、100 分、含 go build 与完整性检查）。

### 当前真实状态

| 领域 | 顶层 passed | 说明 |
| --- | --- | --- |
| 开发 | ✅（加固前） | 加固后重跑因 implement 触发 200 次工具操作上限而 action_failed，交付本身仍 95 分 |
| 安全 | ✅（加固后） | 38 次调用、100 分、go build/完整性检查通过 |
| 运维 | ❌ | 交付评价 100，但 harness 证据判据未过 |
| 跨领域 | ❌ | 同上 |
| 资料核验 / 文档 | ❌ | 产物契约仍失败（本轮已把契约补进用例，尚未重跑验证） |

### 待办

- 工具证据采集不可靠（计数为 0/1，异常被吞）是当前最影响判定的问题，下一步应先修它与 staging 判据，再全量重跑。
- 余额约 3 元，全量重跑需先充值。

## 2026-11-01 凌晨（二）：切换 DeepSeek、修模型列表与消息链（最新）

### Provider 已切换到 DeepSeek

- `configs/providers/primary.yaml` 改为 `https://api.deepseek.com/v1`、`wire_api: chat_completions`、`model: deepseek-flash`（DeepSeek-V4.1-Flash，1M 上下文）。密钥只放在 `secrets/providers/primary/api_key`，旧 tokenmarket 密钥备份为同目录 `api_key.tokenmarket.bak`。
- 探针一次通过：chat / streaming / tool_calling / structured_output 均支持，vision 不支持。DeepSeek 明显更快：复杂开发案例原先 250–4200 秒，切换后同类案例 20–140 秒。

### 设置页“获取模型”修复

- 接口 `/api/resources/providers/{id}/models` 返回的一直是供应商原始数据（实测 `deepseek-flash`、`deepseek-v4-pro`，`owned_by=deepseek`）。
- 问题在前端：`fillProviderSetupModels` 会把“已保存但供应商未列出”的模型名重新插到列表最前面，于是界面出现项目侧自定义名称。现在只显示供应商返回的模型；保存的模型不在列表中时留空，由用户重新选择。

### 本轮新修的三处平台缺陷

1. `tool` 消息链强校验：DeepSeek 对消息顺序严格，文档案例连续 400（`Messages with role 'tool' must be a response to a preceding message with 'tool_calls'`）。在 LLM 客户端发送前统一规范化：孤立 tool 观察降级为用户消息保留原文，未响应的 tool_call 补占位响应。
2. 写入尝试自动重开被阻塞分片：运维案例里 `author-runbook` 分片被标 blocked 后，写入一直被拒，模型反复重试到 200 次工具操作上限。现在写入即视为重试并重开该分片；读取此前的放行逻辑保留。
3. `docs-review-publish` 模板补预算（soft/hard llm_calls 60/90、continuations 3/6）：之前没有预算，交付阶段反复请求截断续写，跑到 400 次调用仍未结束。

另外把运维夹具的 Go 文件改为 gofmt 规范格式（原先 5 个文件未格式化，`gofmt -l .` 在任何实现下都会失败，导致质量门必然拦住交付）。

### 领域矩阵（DeepSeek 实测）

| 领域 | 结果 | 数字 |
| --- | --- | --- |
| 开发 | ✅ 通过 | 250s / 56 调用 / 95 分 hard_gate |
| 安全 | ✅ 通过 | 4224s / 21 调用 / 100 分 hard_gate |
| 运维 | ✅ 通过 | 92s / 20 调用 / 100 分 hard_gate，8 阶段全过 |
| 资料核验 | ❌ 未通过 | 65 调用；产物契约失败：重复来源未映射、冲突结论过度收敛、不支持的部署数被断言等 |
| 文档/Office | ❌ 未通过 | 两次实测均在模板契约失败：缺 sheet2 与“完成率”标记、PPTX 缺母版声明、DOCX 缺失；不是平台崩溃 |
| 跨领域 | ✅ 通过 | 111s / 26 调用 / 100 分 hard_gate，四个分支全部实际修改并通过独立验证 |

### 夹具与预算的通用修正

- 夹具准备阶段统一执行 `gofmt`：运维与跨领域夹具原先用压缩写法生成 Go 文件，`gofmt -l .` 在**任何**实现下都会失败，质量门因此必然拦住好交付；现在所有 Go 夹具在 `prepare_workspace` 里格式化后再初始化 git。

### 其它状态

- 全量 `go vet`、`go test ./...`、`gofmt` 干净；服务 `http://127.0.0.1:8080/console` 200，二进制含上述全部修复。
- DeepSeek 账户余额约 3.79 元（本轮共消耗约 6.5 元，文档案例单轮约 1.5–2.5 元）。
- 已提交；工作区干净，未推送远端。

## 2026-11-01 凌晨：领域验收进展与供应商中断

### 领域矩阵现状

| 领域 | 结果 | 证据 |
| --- | --- | --- |
| 开发 | ✅ 通过（250s、56 次调用、95 分 hard_gate；按声明预算重算 100） | `.artifacts/p7-provider/20261031-041517-962133/` |
| 安全 | ✅ 通过（4224s、21 次调用、100 分 hard_gate、7 阶段全过） | `.artifacts/p7-provider/20261031-193746-789269/` |
| 运维 | ⚠️ 交付通过（95 分 hard_gate、`go test`/`go vet` 过、runbook 与 rollout-result 齐全），工作流被自身质量门拦在 `final-quality`；后续两次重跑分别因 3 小时窗口耗尽与供应商重置未完成 | `.artifacts/p7-provider/20261031-222852-641003/` 等 |
| 资料核验 / 文档 / 跨领域 | ❌ 未取得结果 | 供应商中断，探针连续 8 次失败 |

### 本轮修掉的问题（都有 fail-first 回归）

1. 阶段验收缺章节 → 本阶段可重试（`enforce_acceptance_sections`）。
2. 探索性只读失败不再判交付失败（`ExplorationErrors`，只读工具白名单）。
3. 被阻塞分片不再挡住读取；返修阶段可重开被阻塞分片（`reopen_blocked_slices`）。
4. 质量门把“同路径后续成功写入”视为恢复，避免一次失败的补丁把 95 分的交付拖成 72 分。
5. Provider 连接改为优先 IPv4（IPv6 路径出现过流式读取停滞）。
6. 安全返修读取额度 8→12；安全案例产物预算声明 60；效率预算按案例声明。

### 供应商状况（阻塞其余领域）

- 现象：`read tcp4 192.168.1.6:xxxxx->104.21.10.96:443: wsarecv: An existing connection was forcibly closed by the remote host`，以及流式连接长时间无输出。
- 证据：连续 8 次 `--probe-only` 全部失败；同时段用 curl 单发成功率约一半。
- 影响：案例无法稳定跑完；已跑的三轮里交付侧都能通过，失败点在传输与工作流内部环节（内部环节已修）。

### 恢复方法（供应商恢复后）

```powershell
# 1. 先确认探针可用
python scripts\p7_provider_runner.py --probe-only --provider-secret secrets\providers\primary\api_key
# 2. 按领域逐个跑，证据目录不要放在 .artifacts 下
python scripts\p7_provider_runner.py --case p7-operations-batched-rollout --acceptance-workspace D:\Projects\test\p7-acceptance-operations-r5 --provider-secret secrets\providers\primary\api_key --timeout 10800
python scripts\p7_provider_runner.py --case p7-research-source-verification --acceptance-workspace D:\Projects\test\p7-acceptance-research --provider-secret secrets\providers\primary\api_key --timeout 10800
python scripts\p7_provider_runner.py --case p7-documents-template-suite --acceptance-workspace D:\Projects\test\p7-acceptance-documents --provider-secret secrets\providers\primary\api_key --timeout 10800
python scripts\p7_provider_runner.py --case p7-cross-domain-unified-delivery --acceptance-workspace D:\Projects\test\p7-acceptance-cross --provider-secret secrets\providers\primary\api_key --timeout 10800
```

### 其它状态

- 服务 `http://127.0.0.1:8080/console` 200（PID 25116，二进制含 IPv4 优先与全部质量门修复）。
- 全量 `go vet`、`go test ./...`、`gofmt` 干净；Python 脚本测试通过。
- 工作区已提交，无未提交改动；未推送远端。

## 2026-10-31 夜：安全领域案例通过

- `p7-security-defensive-remediation` 第 6 次实测**通过**：`status=completed`，4224 秒、21 次模型调用，`audit → approve-remediation → remediate → verify → review → quality → final-report` 七个阶段全部完成（这次质量门一次通过，没有进入返修）。独立评价器 100 分、`hard_gate=true`、无任何扣分项。
- 正式证据：`D:\Projects\test\p7-acceptance-security-20261031-r6\交付证据\网络安全\p7-security-defensive-remediation\第1轮\`；运行报告 `.artifacts/p7-provider/20261031-193746-789269/report.json`。
- 这次通过依赖本轮四处修复：只读工具不再被 blocked 分片拦截、返修阶段可重开被阻塞分片、探索性读取失败不计入交付失败、安全返修读取额度 8→12。前五轮的交付侧其实一直是好的（100 分），失败都在工作流取证/返修链路，现在这条链路走通了。
- 领域矩阵进度：开发 ✅（`20261031-041517-962133`）、安全 ✅（本轮）；运维、资料核验、文档、跨领域尚未跑。
- 供应商仍然很慢（本轮耗时 70 分钟），后续领域案例按同样方式逐个跑。

## 2026-10-31 晚间（续）：被阻塞分片不再挡住读取

- 复跑 `20261031-183042-554523` 仍是 `repair` 零写入，但这次证据指出了更深一层的原因：计划里的**只读分片 `audit-read-001` 在 remediate 阶段失败后被标记为 blocked**，而分片校验把 blocked 状态一律拒绝，于是 repair 阶段连“读安全报告”都做不到，连续 8 次读取全部返回 `change slice audit-read-001 is blocked: stage remediate failed before this slice was verified`，最后模型无据可依、零写入收尾。
- 修复：`validateChangeSliceToolCall` 中读取类工具遇到 blocked/pending/planned 状态一律放行——读取没有副作用，挡住它既保护不了任何东西，又会让返修无从取证。写入类工具对 blocked 分片仍然拒绝，未放宽。
- 回归 `TestChangeSliceReadOfBlockedSliceIsAllowed`：先禁用修复跑出与线上完全相同的报错，再恢复后通过。全量 `go vet`/`go test`/`gofmt` 干净，服务已重建（PID 17488）。
- 以新构建复跑安全案例（`p7-acceptance-security-20261031-r5`）。
- 顺带说明：这三轮安全案例的**交付侧一直是好的**（最新两轮评价器 100 分、`hard_gate=true`、无扣分），失败都发生在工作流自身的返修取证环节。

## 2026-10-31 晚间：探索性读取错误不再判交付失败

- 处理上一轮暴露的两个问题：
  1. 安全返修阶段读取额度不足：`security-audit-evidence-gate` 的 `repair` 阶段 `max_read_calls` 由 8 提到 12（要读安全报告 + 四个实现文件再判断，8 次不够）。
  2. **工具调用错误被当成质量问题**：质量门原先把任何失败的工具结果都算 `ToolErrors` 并直接判失败，于是一次“读错了路径”的 `read_file` 就把本来 `go test`/`go vet` 全过的交付拖进返修。现在只读类工具（`list_tree`、`list_dir`、`read_file`、`read_file_range`、`search_files`、`repository_map`、`file_info`）的失败单独记为 `ExplorationErrors` 并给出一条 warning，**不再计入交付失败**；写类、命令类、验收类失败仍然照旧判失败。
- 回归 `workflow_quality_exploration_test.go` 两例：探索性读取失败不再失败门、写入失败仍然失败门；并先禁用豁免验证过会复现线上原话 `review tool file_tools/read_file returned an error`。
- 全量 `go vet`、`go test ./...`、`gofmt` 干净；服务已用新二进制重启（PID 16032，`/console` 200）。
- 随后以新构建复跑安全案例（`p7-acceptance-security-20261031-r4`）。上一轮 `20261031-182444-468785` 因二进制早于该修复已中止，不产生结论。

## 2026-10-31 傍晚：阶段验收增量重试 + 安全案例复跑

- 按方案 (b) 实现：新增 `enforce_acceptance_sections` 开关，打开后阶段输出缺少声明的 `contains` 标题时，`workflowGraphStageCompletionError` 返回可重试错误 `did not satisfy acceptance criteria`，本阶段按自身 `max_attempts` 重试并附上“必须逐字包含这些标题”的纠正提示；未打开开关的阶段行为不变。回归 `workflow_stage_acceptance_test.go`（3 例，已先禁用守卫验证过会失败）。安全模板的 `recovery-verify` 已打开该开关。
- 安全案例复跑 `p7-provider/20261031-170759-915633`（2156 秒、23 次调用）：**交付侧满分**——`go test`/`go vet` 通过、质量 100、`hard_gate=true`、无任何 note（效率维度也因为声明预算而不再扣分）。但工作流仍未跑完：终态 `action_failed`，停在 `quality` 之后的 `repair`，摘要为 `workflow stage repair produced no workspace file mutations`。
- 该失败的成因不是产品缺陷：模型在返修阶段报告“读取报告和请求头实现文件的调用被阶段读取上限拦截”（该阶段 `max_read_calls: 8`），因此未做任何修改就收尾；同时质量门记录到的失败原因是**报告读取工具错误**，不是验证失败。也就是说“工具调用错误被当成质量问题”这条链路仍会把好交付拖进返修。
- 下一步两条候选：把安全返修阶段的读取上限调高（数据调整），以及核查“工具调用错误是否应计入质量门失败”——后者要先确认现有设计再动。

## 2026-10-31 下午：里程碑提交与安全案例实测

- 已创建里程碑提交 `806c098`（711 个文件、工作区清零），提交前跑过密钥扫描（通过）。这是本仓库第一次把长期未提交的实现与验收修复固化，出问题可 `git reset` 回退。
- 安全领域案例 `p7-security-defensive-remediation` 首次实测（`p7-provider/20261031-154214-615364`，3751 秒、35 次调用）：**独立评价器通过**——`go test ./...`、`go vet ./...` 全过，质量分 95、`hard_gate=true`；交付文件为四个实现文件 + `deliverables/security-report.md`。
- 但工作流终态是 `blocked`：`recovery-verify` 阶段的验收项要求输出里出现“返修复测结论”“阻断项验证”两个字面标题，模型按结构化 YAML 输出、没有这两个标题，于是 `final-quality` 判失败。
- 定位到的平台缺陷：`seedRequiredOutputHeadings`（把验收要求里的 `contains` 标题补进输出）**只有单测调用、没有生产调用点**，是死代码；也就是说这类“必须出现某标题”的验收目前只能靠模型自觉。两种修法：(a) 接上这个补丁——会让 `contains` 检查变成形式检查，我不建议； (b) 让阶段验收不满足时按普通契约失败**在本阶段重试**（该阶段本来就配了 `max_attempts: 2`），保持检查有效。这条需要先定调，未擅自改。
- 安全案例的产物预算已按同一口径声明为 60（本次 51 个产物；调用 35 次仍在 40 以内）。
- 供应商当晚极不稳定（首字节 5–20 秒、多次流式挂起），单轮实测耗时约 1 小时；其余四个领域案例尚未跑。

## 2026-10-31 上午：效率门按案例规模声明并达成

- 按用户决定，效率预算不再对所有 P7 案例共用一个常量：`P7ComplexCase` 新增 `llm_call_budget` / `artifact_budget`（默认仍为 40/30，保持原单包案例口径），`evaluate_p7_quality` 改用案例声明的数值，提示里同时给出 `used/limit`。
- 复杂开发案例 `p7-development-platform-migration` 声明 `llm_call_budget=60`、`artifact_budget=60`，依据是 12 个阶段 + 6 个必需写入分片的结构下限（每分片至少读、写、即时检查各一次，再加计划、验证、修复、恢复验证、审计与最终报告），不是按某次观测值倒推。
- 端到端验证：用已通过那轮（`.artifacts/p7-provider/20261031-041517-962133/`，56 次调用 / 52 个产物）的真实计数在新口径下重算质量分，结果为 **100 分、`hard_gate=true`、效率维度 10/10、无任何 note**，重算记录 `.artifacts/budget-recompute-20261031.json`。这是对同一份真实证据的重算，不是新的模型调用。
- 工作流自身的预算同步上调为 soft 45 / hard 72，避免它在评价器仍接受的区间内误中止；`complex-project-delivery` 的续跑仍受 hard_continuations=4 约束。
- 回归：`go vet`、`go test ./...`、`gofmt -l cmd internal pkg mcp_servers` 干净，Python **206 项通过 / 2 跳过**（新增的两项分别是“案例声明预算生效”和“默认预算仍然生效”）。
- 服务：`http://127.0.0.1:8080/console` 200，PID 19056，二进制 SHA-256 `9aae7e6d…`。
- 一次未完成的实测：`p7-provider/20261031-085121-754689` 在 `implement` 续跑时遇到供应商流式长时间无输出（会话 5 分钟未更新），已停止；该轮不产生结论，也不用它改写任何记录。
- 仍未完成：受控漏洞正例/误报、实际业务通知送达、人工验收与正式发布。

## 2026-10-31 凌晨：效率门的定位结论

- 通过那一轮的 56 次调用分布已核实：`implement` 39 次是最大项，其余 `plan` 3、`verify` 5、`repair` 4、`recovery-verify` 3、`recovery-audit` 1、`final-report` 1。
- `implement` 的 39 次是 **18 + 18 + 3** 两段续跑：阶段 `max_iterations=18` 用尽后，平台按既有设计以完整工具观察重跑该阶段，模型重做了一部分发现。续跑提示已带“勿重跑已完成调用/成功写入清单”，但**无法续用原会话消息**：`schema.AgentResult` 不保存 messages，`agentRunOptions` 也没有 InitialMessages，所以每次续跑只能从摘要重建上下文。
- 复核软预算：运行在 `verify` 前确实触发了 `budget_soft_limit_hit`（`llm_calls used 43 of soft limit 40`），最终报告仍判定可交付；硬预算 60 未触发。也就是说效率超限被如实记录，但按设计不阻断交付。
- 结论：6 个写入分片 + 12 个阶段的任务，把总调用压到评价器的 40 次以内，**结构性上做不到**——仅“每分片读一次、改一次、验一次”就接近 18 次，加上计划、验证、修复、恢复验证、审计、最终报告至少 19 次。真正要过 40 需要在二选一里定口径：
  1. 让效率预算随案例规模声明（例如每条必需分片额外 N 次），而不是所有案例共用 40/30；
  2. 接受该维度扣分（当前 95 分、`hard_gate=true`、`passed=true` 已经通过，扣的是效率维度的 5 分）。
- 要做真正的调用数下降，需要把阶段续跑改成“续用原会话消息”，这涉及在 `AgentResult` 持久化 messages 并给 `agentRunOptions` 增加初始消息通道，属于跨包改造，不能在未确认前顺手做。
- 本轮没有新增模型调用：上一条通过记录（`.artifacts/p7-provider/20261031-041517-962133/`）仍是当前最佳证据；服务 `http://127.0.0.1:8080/console` 200、二进制 `cbad55ef…`、全量 Go/Python 回归通过。
- 待用户决定：效率口径按 (1) 校准预算还是按 (2) 接受扣分；其余未完成项仍是受控漏洞正例/误报、实际业务通知、人工验收与正式发布。

## 2026-10-31 凌晨续：复杂案例首次通过

- **真实复杂案例 `p7-development-platform-migration` 第一次通过**：`status=completed`，250 秒，56 次模型调用；独立评价器 `go test ./...`、`go vet ./...`、`gofmt -l .`、页面公开测试与隐藏测试全部通过，`go test -race` 按延期门禁处理；质量分 95、`hard_gate=true`、`passed=true`。证据在 `D:\Projects\test\p7-acceptance-20261031-r7\交付证据\开发编码\p7-development-platform-migration\第1轮\`，运行报告 `.artifacts/p7-provider/20261031-041517-962133/report.json`。
- 达到这一步之前修的三处：
  1. 续跑路径绕过硬预算：`continueWorkflowGraphExecutableStage` 重进阶段时没有检查工作流预算，一次 `implement` 可以连跑 199 次调用。现在重进前先检查；`complex-project-delivery` 模板补了 `budget`（soft/hard llm_calls 40/60、continuations 2/4）。回归 `workflow_budget_continuation_test.go`，先禁用守卫验证过它会失败。
  2. 阶段决策被基础设施限制误判：`workflowStageCanonicalDecision` 只要有一条工具结果出错就返回 failed，`go test -race` 因 CGO 退出码 2，于是恢复验证正文写“总体结论：通过”、结构化 decision 却是 failed，审计据矛盾阻塞。现在 race 不可用不算失败，首行“总体结论”成为契约来源，但“正文说通过、命令真失败”仍判 failed（回归 4 例）。
  3. 恢复审计口径与原始需求不一致：审计把“尚无 Ubuntu CI 运行结果”和“未单独复核变更分片”当作阻塞，而需求允许本机 CGO 不可用时以 CI 门禁延期。已按需求写明延期不算阻塞、变更分片由调度器跟踪。
- 调用量演进：221 次（预算缺失 + 续跑无上限）→ 31 次 → 25 次 → 本次 56 次通过。当前分布是 `implement` 39 次为最大项，评价器效率维度仍提示 `llm_calls=56, artifacts=52`（预算 40/30），**效率门尚未满足**。
- 部署与回归：服务 `http://127.0.0.1:8080/console` 200，PID 16616，二进制 SHA-256 `cbad55ef…`；`go vet`、`go test ./...`、`gofmt -l cmd internal pkg mcp_servers` 干净，Python 204 项通过、2 项跳过。
- 下一步：把 `implement` 的 39 次调用压到预算内（续跑分段带重复发现；应让续跑明确已有进度并禁止重读），再跑一轮确认效率维度；受控漏洞正例/误报、实际业务通知、人工验收与正式发布仍未完成。

## 2026-10-31 凌晨：分片读取门、过期哈希提示与三次真实验收

- 修复 `internal/agent/change_slice_runtime.go` 的读取门：读取属于某个**写入分片**的文件时，旧代码会顺手 `Start` 该分片，依赖未就绪就把这次读取也拒绝，真实运行里表现为 `change slice X is not ready: dependencies for slice "X" are not complete`，模型读不到自己要改的文件。现在读取只作准备，不启动分片、不触发依赖顺序。回归 `TestChangeSliceReadOfNotReadyWriteTargetIsAllowedWithoutStartingSlice`（先把旧代码还原验证过它确实报出同一条生产错误）。修复后同一案例的该类错误从多条降为 0。
- 修复过期哈希提示：`mcp_servers/file_tools` 在 `expected_sha256` 不匹配时现在返回 `current_sha256 is …`，让已经用满读取额度的调用方可直接重试而不用再读一次。回归断言写在 `main_test.go` 的 SHA 冲突用例里。
- 三次真实验收（`D:\Projects\test\p7-acceptance-20261031*`，供应商当夜约 1.8 秒 TTFB）：
  1. 修复前第一轮：11 个阶段全部执行到 `final-report`，仍因 `gofmt -l .` 列出 5 个文件在最终质量门失败，`waiting_input`；40 次模型调用；升级文档仅 29 字节占位。
  2. 修复后第二轮：`is not ready` 错误归零，但 `repair` 阶段 6 次读取用满后唯一补丁因过期哈希失败，报 `produced no workspace file mutations`，`action_failed`；25 次调用。
  3. 修复后第三轮：同样跑完全部 11 个阶段，`final-quality` 仍失败。这次是对评价器自己执行的结果不一致：模型报 `gofmt` 失败四个文件，而评价器在工作区上 `gofmt -l .` 通过、`go test ./...` 与 `go vet ./...` 失败；交付指标 221 次模型调用、4.14M prompt token、118 个产物，远超效率门。
- 结论：分片读取门与哈希提示两处确属真实缺陷并已修复；但复杂案例仍未通过，且第三轮出现**调用量与产物数暴涨**（221 次/118 个）这一新的效率异常，需要在下一轮先定位再评估是否重跑，不能把「跑完了 11 个阶段」当成通过。
- 服务与回归：`go vet`、`go test ./...`（全部包）、`gofmt -l cmd internal pkg mcp_servers` 干净；Python 204 项通过、2 项跳过；服务 `http://127.0.0.1:8080/console` 返回 200。
- 仍未完成：复杂案例通过（最终报告 + 效率门）、受控漏洞正例/误报、实际业务通知、人工验收与正式发布。

## 2026-10-30 傍晚收口：race 延期语义、格式修复、服务恢复（优先于下方全部记录）

- 服务已恢复：`http://127.0.0.1:8080/console` 返回 200，PID 19100，工作区 `D:\Projects\test\web`，启动日志 `.artifacts/restart-20261030/service.out.log`。程序未重建，SHA-256 仍为 `8880315...`（与 `.artifacts/complex-closeout-20261029/deployment-current.json` 一致）；本轮只改 Python 验收脚本与三个 Go 文件的空白格式，不改运行逻辑。
- 修复此前记录的自相矛盾：`scripts/p7_case_evaluator.py` 在本机 race 不可用时曾写入 `passed: true` 同时保留 `exit_code: 2`。现在改写为 `passed: false / deferred: true / blocking: false / status: deferred`，失败清单与工程完整性评分改按 `deferred` 放行，因此延期不阻断交付、但不再冒充“本机 race 通过”。`.artifacts/complex-closeout-20261029/review-after-format.json` 等旧证据保持原样，不回写。
- 新增回归 `test_unavailable_race_without_linux_gate_blocks_delivery`：没有真实 Ubuntu race 门禁时，同样的 cgo 不可用仍然阻塞交付，防止“延期”被当成无条件豁免。
- `mcp_servers/file_tools/repository_index.go`、`mcp_servers/office_tools/main_test.go`、`mcp_servers/office_tools/xlsx_edit.go` 三个真实源文件此前会被 `gofmt -l` 列出，已格式化；现在 `gofmt -l cmd internal pkg mcp_servers` 干净。`.artifacts/`、`task-staging/` 下的历史副本仍列出未格式化文件，它们是验收快照，本轮不改。
- 全量回归：`go vet ./...` 通过、`go test ./... -count=1` 37 个包全部通过；Python 202 项通过、2 项跳过（比上次多 1 项即上述新增回归）。
- 环境提示：在受限文件权限模式下跑测试时，Windows 受限令牌与目录权限类用例会因沙箱本身拒绝而失败，不是代码回归；全量回归需在完整权限下执行。
- 仍未完成：新一轮真实 Provider 复杂任务交付、受控漏洞正例与误报验收、实际业务通知送达、人工验收与正式发布。
- Provider 探针实测（19:01 返回）：`/api/resources/providers/primary/test` 的首个能力在 5 分钟上下文超时后失败，Python 侧重试三轮共约 15 分钟，报告 `.artifacts/p7-provider/20261030-184549-974575/report.json` 记为 `send Responses request: Post "https://tokenmarket.cheap/v1/responses": context deadline exceeded`，没有计入任何 P7 轮次。同一时段用 curl 直连同一接口：不带 `stream` 字段约 5–8 秒返回 200，带 `stream:true` + `Accept: text/event-stream` 约 8 秒返回 200 且 SSE 事件正常；另有多次 40–45 秒内连响应头都不到（HTTP 000）。结论是供应商 Responses 接口间歇性迟迟不返回响应头。
- 据此修了客户端超时结构：`internal/llm` 新增 `attemptTimeout = timeout/(retryCount+1)`，非流式的 `Chat` 与 Responses 路径每次尝试独立限时后重试，流式仍保留整段超时（长流是进展不是挂起）；`isRetryableError` 现在识别 `deadline exceeded`。回归 `internal/llm/attempt_timeout_test.go` 两个用例：首个尝试挂起后重试成功、全部挂起时有界返回 `ProviderRetryExhaustedError`。
- 修复后同一探针 22.4 秒通过（attempts=1；chat 9.9s、streaming 4.1s、tool_calling 3.2s、structured_output 5.1s；vision 不支持，属适配器既有边界），报告 `.artifacts/p7-provider/20261030-191349-750605/report.json`。
- 二进制已重建并重启：SHA-256 `a9a4362b...`，PID 18896，`/console` 返回 200，旧程序备份 `.artifacts/restart-20261030/goflow-prev.exe`，部署记录 `.artifacts/restart-20261030/deployment.json`。改动后再次全量 `go vet`/`go test`/`gofmt` 干净。
- 19:56 起发起新一轮真实复杂案例验收 `p7-development-platform-migration`（全新夹具工作区）。第一轮 19:54–20:12 卡在能力探针（再次 `context deadline exceeded`，`formal_p7_rounds=0`，`.artifacts/p7-provider/20261030-195420-256641/`）。
- 第二轮 20:10 发起，探针 20:14:51 通过，正式运行 `wf-complex-project-delivery-1793362496064375600` 在 30 分钟上限时仍停在 `plan`：12 次模型调用、43 次工具结果、2 个产物，工具为 `repository_map`/`read_file`/`search_files`，重复调用与重复整读均为 0。runner 记为 `status=running` + `workflow run ... timed out`，不算通过。报告 `.artifacts/p7-provider/20261030-201009-494240/report.json`。
- 同次运行还暴露一个流程问题：`--acceptance-workspace` 指到 `.artifacts` 下会被判为 `acceptance evidence cannot be stored in a disposable artifacts directory`，该轮因此不能作为正式 P7 证据。正式证据必须放在 `.artifacts`/`.tmp` 之外（例如 `D:\Projects\test` 下的目录）。
- 供应商当晚吞吐约为 10-29 的 1/5–1/10（12 次调用耗时 30 分钟，探针本身正常时 22 秒完成四项能力）。因此完整复杂案例需要更长的时限和更稳定的供应商窗口；客户端超时修复本身已由两次探针通过和两个单测证明有效，但**尚未取得通过的真实复杂任务结果**。
- 补了“修复阶段零写入”的确定性回归 `internal/agent/repair_zero_write_test.go`：修复阶段基线在实施之后快照，只重读文件的复查必须报 `produced no workspace file mutations` 且被判定为可重试（不是直接阻塞），而对同一已实施目标的真实写入必须算作进度。该用例在当前代码上直接通过，属于锁定既有正确语义的回归（10-29 的失败根因是写入工作区不可见，不是这道门判错），不是先失败后修复的补丁。
- 仍未完成：新一轮真实复杂任务通过结果、受控漏洞正例与误报验收、实际业务通知送达、人工验收与正式发布。

## 2026-10-30 最新进度（覆盖下方旧的“续跑中”状态）

- 修复已批准分片状态在恢复时被刷新、未完成分片误阻塞、空心跳重置流超时、Responses 完成后等待 EOF、预输出 EOF/HTTP2 中断恢复、GoFmt 重复程序参数规范化、只读计划进入写入流程等缺口；执行计划 Skill 的工具名单已补仓库结构图。
- 当前真实工作流 `wf-complex-project-delivery-1793292894180069200` 仍为 `action_failed`，没有 Agent 最终报告。不是“全任务已完成”。独立验收报告质量分 95 / hard_gate true，是格式修复后对保留工作区的只读检查；不改变原运行终态。
- 最终独立检查显示 Go 全量测试、vet、`gofmt -l .`、页面公开与隐藏测试通过。race 因 CGO 未启用而延期；Ubuntu workflow 尚无实际运行结果。脚本测试 201 项通过、2 项跳过；安全八场景、Edge、文档/资源链接/本地化/密钥扫描通过。
- 最终用户要求的模型完整流程和效率门未通过：53 次模型调用超过预算，报告阶段未运行。完整证据、人工格式调整说明和清理记录见 `.artifacts/complex-closeout-20261029/` 与 `.artifacts/complex-closeout-20261029/REPORT.md`。
- 当前服务以 `.artifacts/report-integrity-20261028/deployment.json` 核对；Windows x64 未签名候选安装生命周期 21 项通过。`D:\Projects\test\web`、数据库、历史运行和秘密均保留；博客 5003/5004 无监听。没有连接 VM、发送外部告警、操作论文、推送或发布。
- 后续应新建真实 Provider 工作流验收更新后的计划与格式分片、修复后复验、总体结论字段和最终 Agent 报告；仍需受控漏洞/误报、实际业务通知送达和人工验收。

## 2026-10-29 报告证据与复杂续跑（优先于下方历史状态）

- 新增 opt-in `verify_evidence_hashes`，验证当前运行成功结果的文件哈希和 SSH 指纹；外部存储按需回读元数据。错误只重试报告，不重做采集，不认证全部语义事实。安全及复杂工作流最终报告启用。
- 修复截断显示正文导致批准切片丢失：从已验证阶段合同恢复并持久化，不放宽切片边界。真实质量工程师预检补研究工具权限，验证阶段声明进程与浏览器工具。
- 复杂工作流15节点，初验与复验失败先走质量/修复，有通过证据再调用独立审计，保留一次修复闭环。旧审批不再令 running 概览显示 waiting_input。
- 真实多模块案例第一次被资源预检拒绝、第二次被截断计划阻塞、第三次审计超时；记录均保留。新运行 wf-complex-project-delivery-1793268935928490800 同ID续跑完成实施、审计、修复及复验，但最终限时观察仍在recovery-audit。独立代码、公开文件格式与页面检查通过；升级文档29字节占位、无最终报告，质量85且hard_gate=false，53模型调用、88已知工具ID，未计通过/P7轮次。最终原始报告 complex-continuation-final/report.json，另有不消耗模型的 quality-review.json。
- 续跑新复现：最近八次工具观察全是读取会漏掉早先成功写入，模型旧摘要甚至声称没有修改。现在从全部保留结果提取成功变更清单放到旧摘要之前，回归已通过。不从失败写入或纯模型叙述构造完成证据。
- 实测还发现格式检查0退出码误判、response整体失败被忽略、race豁免过宽及修复阶段重套已关闭分片调度，均补复现与修复。独立验收补gofmt检查，在注入隐藏测试之前执行；可信参考解只在其临时单测中格式化。修复节点取得批准计划，仍保留原授权、既有测试断言保护与复验门禁。
- 证据集中 `.artifacts/report-integrity-20261028/`，完整旧报告不改写。全量Go/vet、Python201项（2跳过）、八个安全本地分支、Edge通过；最后修复资源的实际调用回归另存。主服务8080保留博客工作区，博客预览未启动；当前进程以 deployment.json 为准。未连接VM、外发实际告警、操作论文或提交发布。
- 新Windows候选framework-20261029已打包，最新离线文档随包提供；安装/升级/回退等21项验收见 windows-native-final/report.json。重复程序和展开包因删除被策略拦截，移到cleanup.json记录的本机可恢复缓存，项目减少约133.25MiB，未声称永久释放磁盘。新资源修复逻辑仍需新真实运行验证，旧运行使用固定已批准图，禁止绕过指纹换资源。

## 2026-10-28 远程验收收尾（优先于下方历史状态）

- 用户已开启授权 VM；10月27日实测的两轮十条 SSH 只读检查成功，没有远程写入。真实事件调查 wf-security-incident-investigation-1793112643698444700 完成，门禁跨重启恢复，9次模型调用、10次工具调用，约190秒；真实日志、监听状态、复核和中文 Markdown 报告都有记录。不能把该案例当成漏洞扫描、确认入侵或全面安全证明。
- 修复恢复时选定 Target 丢失：session 快照按 durable run 保留目标，正常技能阶段把目标重新绑定到执行上下文；错目标仍拒绝。performed reflection 不再作为未知测试扣分，真正失败与未知测试不被放过。
- 安全资源最终报告改为 Markdown；关键规则以摘要可保留的条目声明并测试实际注入内容。通知必须有实际 event_id/回执；无告警不得虚称 stored。深入调查才出现的预警也会通知，早期告警不重复；八个本地分支覆盖正常、早期/后期告警及失败阻塞。
- 证据目录 `.artifacts/remote-scenarios-20261027/`。原稿有一个61位的 SHA-256，缺3字符；28日核对后另存“最终事件调查报告-核对稿.md”，仅纠正此值并标注，原始模型稿和 report.json 没有改记。详见 report-integrity-review.json。后续完整哈希优先引用索引，不由正文重抄。
- 本轮没有新模型调用、没有再次连接 VM、没有外部告警或发布 GitHub。27日 Go/vet、Python 199项（2 skipped）、Edge及8分支有通过日志；28日收尾补关键包与资源校验。当前在线状态以本轮 deployment.json 为准，不沿用3188等历史 PID。
- 剩余：更大范围复杂长任务、受控漏洞正例与误报验收、日常外部通知通道、用户人工和正式发布。论文目录不再操作，保留用户数据和仓库既有修改。

## 2026-10-27 恢复开发（优先于下方历史状态）

- 用户结束论文操作，恢复资源驱动框架方案。保留仓库大量既有未提交修改、博客工作区、数据库、原运行与密钥；本轮未连接 VM、投递真实外部通知或发布 GitHub。
- 复现并修复：交付清单用截断摘要判断命令证据，漏掉成功或失败的完整 JSON；只为命令证据回读持久对象，避免为清单加载全部产物。中文产物标题在 80 字节边界保留完整 UTF-8 字符。
- 新实测首次因模型的 program=go test / go vet 阻塞；已在原命令规范化入口纠正可明确分类的开发调用，保留未知程序、含引号路径、Shell 和破坏性调用的原边界，并补充工具说明和质量验证资源的参数示例。
- 修复交付统计仅数工具产物而遗漏读取的问题；按阶段结果、事件和产物中的调用 ID 去重。本轮新运行核对为 21 次，不再显示 7 次。
- 新实模型自动工作流 wf-plan-implement-audit-1793106485328174300 完成，约 234 秒，gpt-6.1-sol / low；审批跨重启恢复、代码实现、回归测试、验证、审计和最终报告通过，独立隐藏测试/vet/gofmt 通过。15 次模型调用、两个变更文件、无无意义重复调用。单包 TTL 缓存案例不代替任意复杂长任务或全部专业安全验收。
- 最新代码、回归、Provider 探针、安全六场景、Edge、部署和历史纠正证据在 `.artifacts/framework-resume-20261027/`。原始新任务报告在 `.artifacts/real-provider-acceptance/20261025-083104/workflow-auto-resume20261027-fixed/report.json`，第一次失败在相邻 `workflow-auto-resume20261027/`。Provider 原始任务报告沿用的旧探针元数据不作为本轮连接证据；本轮实际四能力探针另存新目录。
- 10 月 25 日环境修复后的真实模型 SSH 六命令诊断已通过，已同步方案；本轮不重连。专业安全、日常业务通知通道、进一步复杂长任务、用户人工与正式发布仍待完成。

## 2026-10-25 用户虚拟机远程验收（优先于下方记录）

- 用户已授权其本机 Ubuntu 虚拟机作测试目标。资源页新增 ubuntu-vm，专用密钥通过 secret 引用；保留原有 authorized_keys，仅追加 restrict 公钥。登录密码未保存；主机指纹首次从用户指定地址采集并固定，未冒充独立控制台核验。连接信息仅在本地数据和本轮证据中，不写入公共文档或发布包。
- 实测修复 Win32 keyscan 的 KEX 兼容、Windows 临时私钥 ACL、重复未认证扫描触发 noauth 限制，以及 MCP 环境白名单遗漏 Windows SSH 依赖。9 条只读命令成功；sudo -n 的拒绝保留；白名单外命令和错误指纹拒绝。生产 MCP Manager 的扫描/id/uptime 真实调用也通过。
- 临时 VM Webhook 验证 204 实际送达、503 失败、未允许私网拦截，接收端口已确认关闭；没有向业务通知通道发送消息，实际业务接收通道仍待配置。
- 规划模板和实际加载 YAML 同步只读工具、模式约束、有界重试、禁工具最终汇总；通用续跑提示保留当前阶段职责。另复现并修复运行续跑保留旧 CompletedAt。
- 新开发工作流 wf-plan-implement-audit-1792921074896369100 规划完成，但实施供应商 524/响应超时，验收器 1200 秒超时，未产生实施变更。本轮独立工作流尝试累计 6 个，完整通过仍为 0。
- 首次真实远程 Agent 生成了诚实的连接失败报告，没有完成六项诊断，不计通过。环境修复后的新实测另存 real-diagnostics-environment-fixed，结果以其 report.json 为准，不覆盖首轮失败。
- 当前完整证据、候选与部署结果集中 `.artifacts/remote-acceptance-20261025/`；主服务是否切换以该目录 deployment.json 为准。保留数据与原始历史；不提交/推送/发布。

## 2026-10-25 实模型验收与协议修复（优先于下方本地复核）

- 已按继续剩余验收的要求实际调用配置的 gpt-6.1-sol / low 中转供应商，使用隔离 Go 项目；未连接真实目标、发外部告警或发布 GitHub。证据集中 `.artifacts/real-provider-acceptance/20261025-083104/`，不能再说本日没有实模型调用。
- 基础 Agent 的 Clamp 代码独立隐藏测试、vet 和变更文件格式通过，但原验收器因初始夹具测试未 gofmt 而中断；原失败与模型未自主补测的 warn 保留。旧 plan-fix-audit TTL 产物也通过隐藏测试，但 echo 探测错误令流程 blocked。
- 自动入口真实运行又发现：切片模式说明缺失、只读观测被推断为写入切片、未来依赖产物被当作缺失文件、格式重试丢失读取证据、长 Skill 摘要截掉关键规则和审计默认输入路径。均补针对性回归并修复；最终汇总禁用工具以避免重新调查。
- TLS 超时此前会在续跑重试耗尽后落为 action_failed；现临时网络错误仍保持可继续，且旧失败记录若有完整上下文可恢复同一运行。鉴权、缺少上下文及资源身份漂移仍拒绝。
- 当前新实测复用 wf-plan-implement-audit-1792914736508929500，从已存 plan 续跑，不新建任务。新源码尚待最终打包/切换主服务；不要用旧线上二进制声称新修复已部署。实模型整体成功与否以最终报告为准。

## 2026-10-25 方案完成状态复核

- P0–P6 共 27 项实现已落实，本次未发现新增未实现项；六个关键包重新测试通过，源码同版本重建与在线程序 SHA256 完全一致。程序仍为 notification-failure-20261024，PID 25568，8080 HTTP 200。
- 尚未完成的是方案第 6 节四组真实场景/人工/发布验收；没有新建付费模型、远程扫描或外部通知任务，未把旧博客任务改记通过。
- 本次只更新状态记录，未新增功能补丁或候选归档。报告 `.artifacts/plan-recheck-20261025/REPORT.md`，测试日志同目录；旧候选保持其原有文档快照。

## 2026-10-24 通知失败与质量门时序补验（最新）

- 实际复现通知失败未纳入安全质量门而显示 completed；已将执行过的 notify 纳入门禁，并要求真实 publish_event 记录。非告警正常，告警失败保留调查和最终报告但整体 blocked；模型编造的成功回执不能通过。
- 进一步复现非中止质量门失败事件错误携带工作流 completed，存在提前结束轮询/SSE 的时间窗口；现继续生成报告或修订时保持 running，最终状态在真正结束后确定。
- 六场景使用本地模型、演示日志和真实 MCP；缺失通道配置在网络请求前失败，无真实目标、付费模型或外部投递。最新证据、候选与在线状态见 `.artifacts/notification-failure-20261024/REPORT.md`。
- 最终服务 PID 25568、8080 HTTP 200，与本轮 ZIP 程序一致。Go/vet、Python 194 项（2 skipped）、六场景、Edge、Windows 21 项生命周期通过。清理 100.13 MiB 重复展开包及最早被替代候选，送回收站；保留最近三个候选、全部报告和原回退资料。

## 2026-10-24 方案逐项复核（最新）

- 对照 P0–P6 时发现此前两处完成结论不够完整：并行领域别名仍在核心固定映射；结构字段存在但 null/字符串等类型异常可能静默走非告警分支。两处均先复现后修复。
- 并行节点用名称、params.domain 与 domain_aliases 自行声明领域，内置十类节点别名迁入多领域模板；未知领域实际执行只选择声明节点，未选节点不调用模型。
- 新增通用 output_schema_json 子集校验，默认阻塞不匹配的结果；安全资源和配套模板约束告警布尔值、严重程度、证据列表及通知状态。不是完整 JSON Schema，不解析外部引用，不改变工具权限。
- 最新测试、候选与在线状态见 `.artifacts/plan-audit-20261024/REPORT.md`。本轮不新增付费模型任务、不连接真实目标、不扫描、不发外部通知、不发布 GitHub；工作区及历史保留。
- 最终服务 PID 22444，8080 HTTP 200；Go/vet、Python 194 项（2 skipped）、未知领域 MCP、安全四分支、Edge 完整烟测及 Windows 21 项生命周期通过。包 SHA256 d24239e451f87b48bed7b93ca9ff46be9ab18a26ee0419b566b063af24f04c3d，与在线程序一致；约 71 MiB 重复展开包已送回收站。

## 2026-10-24 安全资源完整链路与字段验收修复（优先于此前候选）

- 新增 `scripts/security_resource_acceptance.py`，在临时运行目录使用本地模型夹具和真实 MCP，覆盖评估/调查各自的告警与非告警。检查人工授权、质量门、完整长正文回读、本地事件数量及输入哈希；不连接真实目标，不发外部通知，不新增付费模型任务。
- 修复 `exists` 将缺失 `result.xxx` 或映射后的引用文本当成证据；保留 false/0、空数组和显式字面量的既有语义。旧测试中叙述性文件名不再让缺失结构字段通过。
- 安全资源及配套模板显式声明 JSON 必需字段；缺告警判断或严重程度时在分支前阻塞。只添加领域资源契约，没有新增核心安全业务分支。
- 本轮构建、在线状态及验证以 `.artifacts/resource-chain-20261024/REPORT.md` 为准；此前 framework-20261024 包和 PID 是历史快照。工作区、原始运行、数据库、秘密和恢复资料保留，博客仍停止。真实模型与专业安全效果、远程和外部送达、人工验收及正式发布仍未替代。
- 最新服务 PID 13780、8080 HTTP 200；新包与在线程序一致。Go/vet、Python 194 项（2 skipped）、四链路、Edge 完整套件及 Windows 21 项生命周期通过；新增展开包 70.99 MiB 送回收站。包 SHA256 为 3838d9f227674348b584c49f8bfe51006acf420262a872d84ea4de2ac5a2dd9e，未修改旧 dist 或发布 GitHub。

## 2026-10-24 资源驱动框架改造（优先于历史状态）

- 通用 Kit routing v1 负责领域入口；开发、运维、安全为资源包，不再依靠具体工作流名称启用隐式业务产物。未知 field-notes 包已通过自动预检及真实文件 MCP 读写回读；模型使用本地确定性夹具，不是真实模型交付验收。
- 首批安全评估/事件调查含显式授权、只读证据和复核门禁；通知默认本地存储，外部送达需配置后另验。未连接真实主机、发起扫描或新增付费任务。
- 保留并增强公开文档检索：主动刷新、来源域过滤、重定向/缓存来源检查、时间/哈希/部分失败。浏览器真实视口与受限 CDP 验证通过。
- 工具复用有界且校验文件变化；记忆索引解码缓存、大文件流式哈希、任务并发读取、技能匹配有测量及失效回归。冷索引与校验成本等代价已记录，不能把模型 Token 缓存命中率等同于这些本地缓存。
- API 拒绝修改正在运行任务引用的资源；暂停恢复检查已知配置漂移。AgentRunner 热更新替换实例，避免修改旧运行对象。资源身份不是工具二进制或环境快照。
- 本轮状态和测试证据以 `.artifacts/framework-20261024/REPORT.md` 为准；入口文档 `docs/README.zh-CN.md`，方案 `docs/resource-driven-framework-plan.zh-CN.md`，扩展 `docs/resource-extension-guide.zh-CN.md`。旧 PID、5003 地址、dist 哈希不代表当前状态；博客预览保持停止。
- 最终本地服务 PID 4760，8080 返回 200，与本轮 ZIP 内程序一致。最新遗留 running/verify 工作流重启恢复为暂停；未请求模型。五类在线自动入口预检均通过，补齐包级工具库不应全部要求由当前入口调用的回归。清理 127.50 MiB 可恢复缓存/重复展开包；候选 Windows 21 项生命周期通过。
- 保留工作区、数据库、密钥、历史记录和现有未提交修改。只清理确认冗余项及本轮可再生中间产物；未提交、推送、打标签或正式发布。
- 仍待真实模型完整复杂任务、授权远程/安全专业场景、实际外部通知、用户人工验收及发布条件；旧博客任务的质量缺口没有冒充通过。

## 2026-10-22 最新任务复核与清理（优先于下方预览运行状态）

- 已跟踪 `wf-plan-implement-audit-1792603920548289600` 至结束；41 次模型调用、输入 306501、输出 13335、缓存 69909、命中率 22.81%。确有 CSS/base.html 修改，但内容扩充及完整页面验收未完成，不能算通过。
- 修复显式 Web 目标被源文件优先策略丢弃、阶段结束时间覆盖、补丁零写入的有限恢复、验证前提、只读复审、修复路径证据传递，以及最终硬失败仍存为 completed 的内部终态。原始失败历史不回写为成功，旧运行也不伪造新时间。
- 博客预览已全部停止；不要再按历史 5003 地址声称在线。GoFlow 本身保留运行，工作区不变。
- 全量 Go/vet、Python 188 项（2 skipped）、博客三项、文档/汉化/资源校验及清理后浏览器检查通过。648.15 MiB 缓存、浏览器 profile 和重复 ZIP 已送回收站；数据、回退、密钥、源码、必要归档与证据保留。
- 离线文档入口 `docs/README.zh-CN.md`；本机操作与清理 `docs/local-operations.zh-CN.md`。详报 `.artifacts/run-quality-20261022/REPORT.md`，真实页面 `.artifacts/run-quality-20261022/latest-run.png`。
- 本轮没有新建付费模型任务或正式发布；新流程真实完整交付、人工验收和新 dist 发布仍待完成。

## 2026-10-22 本地服务恢复与运行质量修复（优先于历史状态）

- 平台已从本次源码构建到仓库 `goflow.exe` 并启动；工作区保持 `D:\Projects\test\web`。博客通过项目 Python 3.15 虚拟环境及无重载 Flask CLI 在 5003 提供人工预览。
- 补齐 plan-implement-audit 的单次有界修复、复测、复审及最终门禁；同时修复会话历史污染新工作流预算、概览调用数与持久账本不一致、同文件重复显示、质量失败状态误报、中文摘要截断及 unittest/py_compile 验证分类。
- 最近工作流实际账本为 18 次调用、输入 129978、输出 10064、缓存读取 27520、命中率 21.17%；对外状态为 blocked，不再把原始失败当成交付成功。原始历史不改写；新修复分支要新建任务验证。
- 全量 Go/vet、Python 188 项（2 skipped）、博客 3 项、完整 Edge smoke 与最终构建真实页面定向检查通过。Windows Python 强隔离集成项因 C:\Python315 不满足受保护安装目录前提而跳过，拒绝不可信运行时的回归仍通过，未放宽隔离。
- 本轮无付费模型请求、数据清理、Git 提交/推送/发布或 dist 重打包；真实模型端到端、人工验收及最终发布包仍不能标记完成。详见 `.artifacts/runtime-recovery-20261022/REPORT.md`，截图 `.artifacts/runtime-recovery-browser-20261022/`。

## 2026-10-20 最终在线状态：流完整性补丁已部署

- 本轮末尾发现真实运行最终正文仅“人工”、该次主响应无用量。没有保存原始上游流，不能断言该轮具体丢失位置；本地故障注入已复现“有 delta 但未收到终结事件便 EOF”会被当作成功的确定性缺口，并已修复。
- 现在 Responses 必须收到 completed/incomplete/failed 终结事件；部分文本或函数调用后 EOF 返回可识别中断，不自动重放已输出的调用。工作流故障注入证明暂停后用户继续可完成同一运行，旧阶段不重放。新增协议/集成回归及最终全包 Go/vet 通过。
- 当前最终服务 PID 22220，程序 SHA-256 8a9bb7c6ffe3af73ea67972696690c384b7082704847f7bf68b673bd54d78a07；dist ZIP SHA-256 b8d4af0b28a244af01130dc1996bd549bd3aab07a0130aea6b7a2cb0c4e338be。最新 Windows 生命周期 `.artifacts/windows-stream-integrity-20261020/report.json`，21/21。
- 博客最终由独立 Flask CLI 进程恢复人工预览，启动器 PID 21188、实际 Python PID 17880。它不是新模型任务成功，也不属于旧受管进程 ID；http://127.0.0.1:5003/ 五地址仍 200。新任务启动验证请使用空闲端口（如 5004），不要直接在 5003 再开一个服务。
- 最新付费复测 run-agent-1792429224603666500 实际完成五个工具，但最终正文残缺，审查 warn、交付 partial/45；原始记录保留，不能计为通过。流补丁之后只做故障注入、机制回归和独立页面检查，未再付费重跑；真实完整报告复验仍待供应商稳定后进行。
- 最终完整 Edge smoke 已通过，截图 `.artifacts/stream-integrity-browser-20261020/`；没有活动中的模型任务。清理总计 388.45 MiB，均送回收站；保留数据、备份与正式证据。最终实页和部署检查 `.artifacts/deployment-final-20261020/final-state.json`，详报同目录 REPORT.md。

## 2026-10-20 本地部署已执行（优先于下方待确认状态）

- 用户本轮已授权直接处理可完成事项。正式 `.venv` 已在原路径重建为 Python 3.15，按 requirements 安装；博客 3 项测试在 ResourceWarning 视为错误时通过，12 个业务文件（含 blog.db）哈希不变。旧环境和旧主程序保存在 `.artifacts/deployment-backup-20261020/`，未删除业务数据。
- 平台已切换为最新候选，PID 6916，http://127.0.0.1:8080/console，工作区仍是 D:\Projects\test\web；新进程 PATH 包含 C:\Python315，Python MCP 已恢复。新二进制 SHA-256 e0ea0b1f04edb6d6d40b59b14475a0f8336984488fc7fb59aed64a4eba587b6a。
- 真实复验发现审查只收到 120 字节工具摘要，丢失 program 等关键证据。现在保留全部工具状态摘要和最近 8 条有界详细证据（每条最多 5000 字节）；新增回归先失败、修复后通过，不修改历史审查结论。
- run-agent-1792428544121791000 真实完成 start_process/process_status/browser_snapshot，审查 pass；4 次模型调用，输入 25265、输出 503、缓存 12416、总计 25768。浅深色实页统计与账本一致，五地址 200，源码/数据库哈希不变，详见 `.artifacts/deployment-final-20261020/live-acceptance.json`。
- 目视补齐审查启动/失败提示与历史 completed 状态中文；完整 Edge 回归及在线定向回归通过。追加修复“没有修改、创建或删除任何项目文件”等否定列表误判，以及审查正文被误当作实现变更；仅剥离有真实 Verification 记录且后缀完全一致的审查附录，不信任模型伪造标题。
- 最新 dist ZIP SHA-256 2ac0c7d931e6ec12e740c81883f9650083da44e05812db1a36ad257aa8b286e2。Windows 21 项生命周期通过，报告 `.artifacts/windows-deployment-release-20261020/report.json`；部署证据 `.artifacts/deployment-final-20261020/deployment.json`。
- 本轮约 301.6 MiB 自建展开目录与中间程序已送回收站，可恢复；正式包、独立候选 exe、报告、截图、旧正式环境和原部署前程序保留。模型复测既有通过，也有供应商 502/过载失败；最后状态以 `.artifacts/deployment-final-20261020/REPORT.md` 为准，不能用前次通过覆盖最后一次失败。
- GitHub 只读复核 runner 为 0、仓库 Actions variables 列表为空；未注册 runner、未改设置、未提交/推送/打标签/公开发布。人工验收、视觉 runner/Provider 文件、签名和正式版本仍需所有者处理。

## 2026-10-20 发布准备与待确认切换（优先于历史状态）

- 真实启动复验 run-agent-1792411179987430100 已完成，5003 博客仍在运行，独立五地址均 200；5003 进程 PID 26260，平台仍为 PID 3084。不要无提示停止预览。
- 新发现并在源码/候选修复：历史快照异步渲染重复加入 Token、恢复流没有使用 timeline 序号、无文件变更的启动任务被当成修改、Status: pass 被 Risks 标签误判，以及 none/partial 文案。原始失败证据未改写。
- x/sys 已从 v0.43.0 升至 v0.44.0，关闭 GO-2026-5024；全量 Go/vet、Python 186 项（2 skipped）、Edge smoke、共享预检通过，govulncheck 无漏洞。
- 新 dist Windows ZIP SHA-256 ba1d5f510a40dae8dc2b66a6a324d807d4940175f7e349522510685195c01598；21 项生命周期报告 `.artifacts/windows-release-readiness-20261020/report.json`。候选 exe 为 `.artifacts/release-readiness-20261020/goflow.exe`。
- 系统已升级至 Go 1.27.2 / Python 3.15，旧 Python 3.10 被删除，博客旧 .venv 无法再次启动。独立 3.15 环境按 requirements 验证 3 项通过、数据库哈希不变；正式 .venv 尚未切换。
- 已异步询问是否允许短暂重启、重建正式 venv 并部署新补丁，尚无答复。当前在线服务仍是原构建；不要误报新补丁已部署。切换时应重新创建原路径 venv，而不是搬动临时 venv；新服务进程 PATH 应包含真实 Python 3.15 路径，避免 WindowsApps 的 python 空壳。
- GitHub repo runner API 返回 0，release.yml 仍要求自托管 Windows 视觉验收；签名和版本号尚需所有者确认。未 stage/commit/push、未注册 runner、未改 GitHub 设置。
- 准备文档：GITHUB_RELEASE_PREPARATION.zh-CN.md、RELEASE_NOTES_DRAFT.zh-CN.md；详报 `.artifacts/release-readiness-20261020/REPORT.md`。历史 P7 严格矩阵本次只读复核仍为 18/18，非新模型重跑。

## 2026-10-19 运行可读性与统计收口

- 最终服务 PID 3084，http://127.0.0.1:8080/console，工作区 D:\Projects\test\web。Go 全包/vet、Python 185 项（2 skipped）、完整 Edge 及实页数据对照通过。
- dist/ 已更新；Windows ZIP SHA-256 bf1cd0962c67b44569b5d08dde7fcbc2108ff6ae3a338b2dc7cd53a10d2c9b0c，最终生命周期 21 项通过，报告 .artifacts/windows-human-workbench-final-20261019/report.json。未推送 GitHub。
- 修复最终正文被产物说明替代、文件变更行数丢失、预览网址与停止提示、进程退出码误报、用量受显示窗口裁剪和阶段累计重复相加。
- 原工作流 wf-plan-implement-audit-1792406170375055600 已完成，最终报告为有条件通过；博客测试的资源警告随后已修复，3 项通过且真实数据库哈希不变。
- 用量采用独立持久账本，单运行与小时/日图表共用来源，运行列表淘汰不删除全局用量。旧版本已丢失的明细不伪造补齐。
- 计划范围、阶段检查点、UTF-8 与续跑文件观察均有新增回归，详见 `.artifacts/human-workbench-20261019/REPORT.md`。
- 新启动验收 run-agent-1792409407184878700 在工具前被供应商三次 TLS 超时阻断，没有启动 5003；不能算 Provider 验收通过。

## 2026-10-19 中断修复（优先于下方历史结论）

- 当前产品仍是 Windows 本地平台，工作区为 `D:\Projects\test\web`；供应商地址保持不变，模型 `gpt-6.1-sol`、默认推理 `low`。不打印或复制密钥。
- 最近用户运行 `wf-plan-implement-audit-1792390194640247800` 在 `revision-summary` 被供应商过载中断；此前有真实模板/样式写入和多页面浏览器证据，不可把本次失败概括为没有执行。
- 已修复旧 PID 重放、精确补丁无写入失败重试、同文件后续成功写入的质量恢复、`py_compile` 分类、无工具的报告阶段中断暂停、不完整阶段被外层重复执行、Responses/工作流重试乘法以及取消终态覆盖。
- 页面推理选择不再沿用其他模型的旧默认值，运行时 API 补齐供应商推理强度；过载和 TLS 超时中文显示补齐。
- 故障注入证明：三次 HTTP 503 后暂停，用户继续后仅新增一次请求完成；已有阶段不重跑，并验证了会话落盘重载。
- Go 全包、Python 184 项（2 skipped）、vet、Edge smoke、汉化/密钥/文档/资源/安全校验通过。真实 MCP 博客两次启停和浏览器验证通过。
- 真实供应商复测 `wf-plan-implement-audit-1792406170375055600` 使用 `low`，三次请求出现 TLS/流读取超时及远端断开，已保存为 `paused_need_more_budget`，暂停在 `clarify`；没有得到有效模型输出，不能宣称复杂长任务已端到端通过。详见 `.artifacts/provider-recovery-20261019/REPORT.md`。
- 最终服务已同步本轮二进制，暂停记录及继续操作在重启后仍可用；历史 dist 归档不代表本轮源码，不要把旧归档标记为本轮已验收发布。
- 最终进程 PID `25132`。四个本轮临时构建二进制（77.26 MiB）已送回收站，保留真实验收证据与用户数据。
- 页面复核补充：不完整阶段延后完成性验收和声明式报告，产物条目不再默认显示成功；暂停概览正确显示等待继续，紧凑故障原因经过中文转换。

## 2026-10-12 latest handoff

- 当前 Windows 服务：`http://127.0.0.1:8080/console?lang=zh#playground`，工作区 `D:\Projects\test`。
- 最近运行的根因是审批恢复丢失对话、汇总阶段重复调用命令、事件流下动作发现滞后，以及已有工具证据后的 Provider 超时。
- 工作流审批恢复现已保存原系统提示、完整消息链和工具结果；`skill-chain` 的有界验证命令继承阶段审批，汇总阶段禁用工具。
- 已有工具结果后的 Provider 中断现在保存为可继续阶段；继续提示携带工具观察并禁止重放已完成调用。
- 运行页提供请求级推理和权限控制、桌面两栏、固定滚动时间线、下置运行总计，以及浅深色可读的 Markdown/代码/JSON/表格结果区。
- 两个历史 `skill-chain` 运行都没有 `reasoning_effort` / `permission_mode`，且固定的是旧工作流资源版本，不能用于判断本次修复；人工验收必须新建运行。
- 非 Provider 回归和 Edge 视觉烟测已通过。最终图片位于系统临时 QA 目录，不进入仓库或发布归档。
- MCP resource false negatives are fixed: caller cancellation no longer records a server failure. Live verification reports 9/9 internal connections ready before cancellation and healthy `idle` after a deliberately aborted HTTP request; 7/7 user-visible connections and 69/69 visible tools remain healthy with no dependency issues.
- Added `/api/usage` and a desktop Usage view. Global token labels now use K below 1M, M below 1B, and B at 1B+. Charts expose color-linked hover details and clickable legends with hidden-series state.
- The API no longer emits the English usage retention sentence. Dynamic Chinese mappings cover wrapped Responses cancellation, paused cancellation, and process-restart interruption messages.
- No Element Plus/ECharts runtime was added: the single-binary embedded frontend stays offline and dependency-free; the required interactions are implemented and browser-tested locally.
- 当前严格矩阵为 `16/18`：跨领域第 1 轮正式通过；第 2 轮在验证阶段因供应商 `403 insufficient_user_quota` 失败并保留为失败证据，第 3 轮未执行。余额恢复后需重跑第 2/3 轮；最新代码仍需用户人工验收。
- 用量页深色图表已完成目视与自动化复核：SVG 坐标文字、网格、折线、柱段和 Tooltip 均使用高对比配色；自动化直接验证四条序列色值、坐标 `fill`、悬浮详情和横向溢出。
- 最近博客运行的根因已修复：命令工具允许经工作区边界和符号链接检查后的项目内解释器路径；Python `-m pytest/unittest` 能正确分类；无测试文件时技能改用 Flask `test_client`/导入级验证，不再误判缺 pytest。
- 运行页最终输出改为完整最后阶段结果，终态事件立即刷新快照；结构化 `decision: blocked/failed` 不再标记为已完成。
- 工作台 Token 总计新增当前运行保留时间线聚合，避免与重试累计混算。历史博客运行的正确口径为 10 次调用、37,124 输入、1,583 输出、14,336 缓存、38.62% 命中。
- 资源与框架契约已补强：真实写工具路径覆盖模型猜测的 changed_files；运行元数据、虚拟环境和 Python 缓存不进入暂存交付；非 Git 工作区拒绝向父仓库发现；资源族、技能、工作流和 MCP 连接提供完整中文标题、说明和准确热加载状态。
- 普通任务暂存不再复制 `交付证据`、`人工验收`、`backups` 和 `task-staging`；已应用博客任务的暂存副本从 240.51 MiB 降至 0.34 MiB，CSS、恢复清单和回滚能力保留。
- 最近两次普通 Agent 超时过去只能“重新运行”，会重复目录和文件读取。现在中断时持久化完整对话和工具结果，同一运行提供“继续生成”，继续提示禁止重放已成功调用；不含恢复上下文的旧运行只允许取消或重新运行。
- 工作流将 HTTP/2 `PROTOCOL_ERROR` 识别为瞬时错误；文件树默认隐藏 `.venv`、缓存、证据和暂存目录，避免依赖树淹没上下文。Node 24 强隔离脚本包补充本地 `package.json` 边界，不再越权探测宿主用户目录。
- 任务页原有完整智能体下拉框被统一的高级功能样式隐藏，只剩两个推荐卡片。现在“运行方式”位于任务输入框上方；选择“智能体”可从运行时全部 15 个智能体中自定义，选择“工作流”则显示工作流列表。
- 最新 Windows amd64 发布归档已从本源码重建，并通过 SBOM、第三方许可证、SHA256SUMS 与完整安装/升级/回滚/卸载生命周期：`dist/goflow-agent_dev_windows_amd64.zip`，SHA-256 `49496ae7490577731ceda1fbe6b7f89a641d3562a8eb96dfb5a140cc7c238863`。

更新时间：2026-10-12（Asia/Shanghai）

## 当前结论

V3 的前端缺陷、Office 伪产物、P7 质量判定漏洞和 Windows 安装生命周期问题已经修复；当前正式产品范围只包含 Windows 10/11 x64 本地智能体工作流平台。Linux 兼容源码和历史证据可以保留，但 Linux 包、Ubuntu VM、Linux race 与原生生命周期不再是发布或完成硬门。

整体任务仍未结束：质量契约 v2 当前有效证据为开发、安全、运维、资料核验、Office/WPS 各 `3/3`，跨领域 `1/3`，合计 `16/18`。2026-10-09 主 Provider 四能力探针通过，跨领域第 1 轮通过；第 2 轮在验证阶段返回 `403 insufficient_user_quota`，余额恢复后从跨领域第 2 轮继续。

当前最新源码服务已覆盖构建并启动：

- 地址：`http://127.0.0.1:8080/console`
- 工作流：`http://127.0.0.1:8080/workflows`
- 工作区：`D:\Projects\test`
- 进程：`goflow.exe`，PID `17804`
- 博客源码、数据库和 `.venv` 已保留；当前未单独启动博客预览进程
- `/console` 与 `/workflows` 均返回 HTTP 200

## Provider

当前配置以 `configs/providers/primary.yaml` 为准：

```yaml
base_url: https://tokenmarket.cheap/v1
wire_api: responses
model: gpt-5.5
timeout: 5m
```

Key 只从 `secrets/providers/primary/api_key` 加载，不进入日志、报告、文档或命令输出。当前 `gpt-5.5` 四能力探针已通过；正式跨领域第 2 轮因供应商余额不足失败，不能计入成功轮次。

## 质量 v2 修复

- P7 成功不再只看总分；功能正确性、工程完整性、交付可用性和独立验证均为硬门。
- 旧质量契约证据自动失效，不能把旧 `18/18` 继续计入当前矩阵。
- 开发任务不再夹带未请求的安全报告；跨领域报告会从最终 `go test`/`go vet` 证据重写，拒绝过期结论。
- 研究交付统一生成稳定来源 ID、正文哈希、去重、冲突映射、503 恢复及 JSON/CSV/Markdown。
- Office 不再生成 1×1 PNG、641 字节 PDF 或不可打开 PPTX；DOCX/XLSX/PPTX/PDF 必须满足内容、结构、预览和实际办公软件打开门。
- Linux 修复 Windows 路径识别、压缩包盘符穿越、只读任务快照清理和测试生命周期释放。

## 实际产出质量

最终 Office 严格产物位于 `.artifacts/office-strict-20260901-02/deliverables/`：

- `report.docx`：Word 实际打开，2 页、31 段、1 个表格。
- `data.xlsx`：Excel 实际打开，明细与汇总 2 个工作表，含公式、冻结窗格、筛选和打印设置。
- `review.pptx`：PowerPoint 实际打开，3 页、16:9；逐页截图检查无重叠或越界。
- `report.pdf`：190,614 字节，正文可检索。
- DOCX 预览：2 张 1920×2485 非空 PNG。

这些产物已由 MCP 结构校验、独立 evaluator 和 Word/Excel/PowerPoint 实际应用三层验证。Linux 没有 Office/LibreOffice 的环境会明确跳过“实际应用打开”集成项，但仍执行 OOXML 结构与内容测试；Windows 负责实际应用门。

## 测试与发布

- 本地 `go test ./...`：通过。
- 本地 `go vet ./...`：通过。
- 本地 Python：`183 tests, OK, skipped=2`。
- 共享预检：密钥、安全、MCP、资源矩阵、Web i18n、文档、HTTP、Edge 浏览器和性能门均通过。
- 浏览器：任务、资源、工作流、设置的桌面/移动、浅色/深色和抽屉状态通过；最新 27 张截图位于 `.artifacts/frontend-connected-tools-20260903/`。
- Windows 生命周期：`.artifacts/p7-windows-native-only-final-20260917/report.json`，通过。

最新 `dist/`：

- Windows amd64：`goflow-agent_dev_windows_amd64.zip`，SHA-256 `49496ae7490577731ceda1fbe6b7f89a641d3562a8eb96dfb5a140cc7c238863`。
- 最新 Windows 生命周期：`.artifacts/p7-windows-native-20261012-agent-selector-final/report.json`，21 项检查全部通过。
- `SBOM.spdx.json`、`THIRD_PARTY_LICENSES.json`、`SHA256SUMS` 已生成并校验。

Windows 本机 `CGO_ENABLED=0`；Windows-only 产品不再把 Linux race 作为发布门。普通并发回归、Windows 原生沙箱、安装生命周期和复杂任务证据仍为硬门。

## 已清理

- 2026-09-17 的 Linux 源码传输包 `.tmp/goflow-source-tokenmarket-20260917.tar.gz` 因本机删除策略拒绝仍保留；它不包含 Secrets、缓存、运行数据或发布产物，且 `.tmp` 不进入发布归档。VM 未收到该文件。
- VM 上三批质量 v2 源码、归档输入、构建缓存和生命周期临时目录。
- 本地约 700MB 可再生 Go/依赖缓存、源码传输包、Python 缓存和被最新报告替代的中间验收目录。
- 本轮额外清理 VM 上约 548MB race/生命周期临时目录及本地传输包、发布缓存和旧截图；用户目录 GCC 15 工具链保留用于后续 CI/race 复验，不修改系统 `/usr`。
- 保留正式发布归档、Windows 与历史 Linux 报告、Office 质量产物、前端截图、Provider 脱敏报告、用户工作区和运行数据。
- 2026-10-11 清空历史测试运行、用量与事件缓存，回收两个旧 `task-staging`（约 239 MiB）、工作区 `__pycache__` 和可再生 P7 Go 缓存；`web` 源码、`blog.db`、`.venv`、人工验收和正式交付证据均保留。被回收内容仍可从 Windows 回收站恢复。

## 下一步

1. Provider 余额恢复后先运行只读四能力探针；只有探针通过才从跨领域第 2 轮继续正式 P7。
2. 五个领域 `15/15` 已独立验证；跨领域当前 `1/3`，第 2 轮失败证据会由 runner 自动归档后重跑，不会被误计为成功。
3. `python scripts/validate_p7_matrix.py D:\Projects\test` 必须按质量契约 v2 返回 `passed: true` 和 `18/18`。
4. P7 通过后再运行一次受影响回归并更新最终状态；Phase 2B/2C 视觉增强仍是可选项，不是发布硬门。

## 2026-09-10 Connected MCP 工具工作流

- 资源页从五类扩展为六类，单个 MCP 工具与 MCP 连接分开显示；当前服务发现 69 个工具。
- “用于工作流”会把工具节点插入当前/最近的模型节点之后并保留原后续连线，随后自动打开主设置区。
- 新工具节点默认 `tool_mode: connected`：上游 Agent/Skill 的 LLM 收到该工具完整输入 Schema、生成结构化参数并执行；工具节点不再二次调用 LLM。
- 工具结果、`tool_results`、`artifact_ref` 和 `artifact_hash` 自动发布给下游模型节点；写/执行/网络工具继续经过上游智能体权限、任务工作区隔离、幂等和审批恢复。
- 旧工具节点未设置 connected 模式时继续使用 assisted 兼容行为。
- Python MCP 标准流强制 UTF-8，资源页中文工具说明不再乱码。
- 端到端单测覆盖只暴露一个连接工具 Schema、参数原样执行、结果进入下游以及审批暂停/恢复；Edge 浏览器覆盖资源工具插入、Schema 主设置区、DOM 节点保持与 27 张截图。
- 当前非 Provider 门：Go 全包、`go vet`、Python `177 tests, OK, skipped=2`、共享预检、浏览器/性能、Windows-only 最终 ZIP、供应链元数据和 Windows 生命周期均通过。

## 2026-10-13 实时工作区版本

- 新任务在首次写入前创建外部只读快照，随后直接使用真实项目目录；项目自带 `.venv`、Node 依赖、数据库和启动脚本可用于真实构建与测试。
- 交付动作在实时模式下只负责“保存当前工作区版本”，不再重复复制文件。未保存变更可撤销，已保存版本按 SHA-256 安全回退。
- 设置页展示最近 3 个工作区版本；版本链按顺序回退，旧版本只有在当前状态匹配时才启用回退按钮。
- 命令工具除最长 240 秒的前台执行器外，新增受管开发进程的启动、状态和停止工具。常驻进程仅限已确认工作区、不接受 Shell、日志保留最近 64 KiB，并随 GoFlow/MCP 生命周期清理；保存版本不会停止服务，撤销或回退会停止关联服务。
- 最新服务真实发现 116 项资源、72 个 MCP 工具；三项受管进程工具均已加载。
- 当前服务：`http://127.0.0.1:8080/console`，最终 PID `25104`。非 Provider 回归：Go 全包通过，Python `183 tests, OK, skipped=2`，Edge smoke、Web i18n、密钥、资源和文档校验通过。

## 2026-10-14 最终运行工作台

- 时间线使用语义化工具摘要并合并调用/结果；0 缓存步骤不显示缓存字段，轨道改为细线圆点。当前运行直接显示取消/重试/继续等操作。
- Workbench API 新增完整 `final_output` 和结构化 `outcome`；执行概览优先显示验收结论、真实 Diff、失败验证与风险，并按 `token_usage` 准确统计模型调用。
- 设置页提供系统“选择文件夹”和可编辑路径，Windows 本地运行默认启用系统目录选择器；空闲时通过 `/api/workspace/select` 动态重绑定。数据维护和假工作区入口已移除。
- 用量范围按钮具有强选中态，指标卡完成四行基线对齐。工作流画布自动聚焦当前节点，并在节点设置区显示实时运行预览。
- 最终非 Provider 验收：Go 全包、Python 183 项、`go vet`、Edge 全页面、国际化、文档、密钥和资源校验通过。截图：`.artifacts/final-ui-20261014/`。

## 2026-10-18 自动编排交接

- 自动模式对“再帮我美化美化这个项目”返回 `workflow / plan-implement-audit / software`，并提供 9 个可解释阶段；对“留言按钮靠近昵称输入框”仍返回直接 `software-engineer`，避免小修改过度编排。
- 前端任务上下文新增“自动编排方案”，显示每阶段的中文名称、智能体、Skill、目的和产出；内部资源 ID 保存在稳定 `data-*` 字段供自动化与工作流联动。
- 现有通用工作流已适配 Flask/Node/Go 等项目：验证不再硬编码 Go；Web 任务要求实际启动地址、HTTP 成功、桌面截图和溢出检查。
- Provider 实测在 tokenmarket 出现 TLS 握手超时、连接重置和 HTTP/2 协议错误。客户端有限重试与原阶段继续生成均生效，但上游持续 5 分钟无正文时仍会产生真实失败/暂停证据。
- 非 Provider 共享预检和 Windows 生命周期通过。Windows 报告：`.artifacts/p7-windows-native-20261018-final/report.json`。
- P7 当前是 16 个成功轮次、跨领域第 2 轮失败、第 3 轮缺失；先恢复稳定 Provider，再只重跑跨领域第 2/3 轮并执行严格矩阵校验。

## 2026-10-18 最终交接

- 当前权威 P7 状态为质量契约 v2 `18/18`。`python scripts/validate_p7_matrix.py D:\Projects\test` 返回 `passed: true`，六领域均连续 `3/3`；旧的 16/18、17 份报告和 Provider 余额阻断只保留为历史记录。
- 跨领域第 3 轮暴露的根因是整文件写入使用了过期 `expected_sha256`，运行时曾把无副作用并发冲突错误地永久阻断。现已保留切片为可重试状态，权限拒绝仍阻断；新一轮完整报告质量分 95。
- 最终本地回归已通过 Go 全包、`go vet` 和 Python `183 tests, OK, skipped=2`。正式 Windows 生命周期报告路径为 `.artifacts/p7-windows-native-20261018-release-final/report.json`。
- 产品范围是 Windows 10/11 x64 本地智能体工作流平台；Ubuntu/Linux 验收不再是发布硬门。后续只需用户访问 `http://127.0.0.1:8080/console` 做最终人工体验与产出验收。

## 2026-10-18 长任务续跑交接

- “继续输出”已改为“继续执行”。工作流达到工具轮次上限时只保存现场并暂停；点击后在同一 run ID 和同一阶段继续调用工具，成功完成后自动进入后续验证、审计和最终报告。
- 实现阶段每段最多 16 轮，用户可在仍未完成时再次继续；续跑继承原运行的 `permission_mode` 和 `reasoning_effort`。`full` 模式免除普通重复审批，但不会绕过工作区、计划范围和安全边界。
- 前后续跑段会合并工具结果，提示中保留最近关键证据并禁止重复成功调用；运行中/暂停状态不再被旧的临时完整性记录误标成最终失败。
- 续跑初始化会直接装载上一段工具结果，因此 Provider 在任何新调用前再次中断也只会再次暂停，不会丢失现场或变成 `action_failed`。规划阶段现为 6 轮/8 次读取，并对宽泛的低风险任务采用安全默认值继续。
- Provider 静默等待在运行页显示动态耗时；可恢复暂停在服务重启后仍保持同一 run ID、阶段、权限和“继续执行”。旧 `interrupted` 记录若带完整 incomplete 阶段会在启动时自动恢复。
- 正向计划文件列表是轻量工作流的实际写入范围，负向/验证路径不会误入；旧范围阻断可在同一运行回退实现。Flask 浏览器验证必须 `debug=False, use_reloader=False`，空快照会重启复测一次。
