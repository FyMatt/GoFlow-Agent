# GoFlow V3 发布候选盘点

更新时间：2026-10-20

## 最终候选：含流完整性补丁

- 在线平台 PID 22220，最终程序 SHA-256 8a9bb7c6ffe3af73ea67972696690c384b7082704847f7bf68b673bd54d78a07。
- 最终 ZIP SHA-256 b8d4af0b28a244af01130dc1996bd549bd3aab07a0130aea6b7a2cb0c4e338be；Windows 21/21 生命周期证据 `.artifacts/windows-stream-integrity-20261020/report.json`。下方其他哈希均为中间候选。
- 修复无终态 EOF 被当成完整响应；新增协议和同运行继续回归通过。最新真实模型正文残缺的记录仍为 partial，不算修复后端到端通过。最终独立预览与模型运行结论分开记录于 `.artifacts/deployment-final-20261020/REPORT.md`。

## 2026-10-20 已部署候选（优先于下方历史状态）

- 正式 Python 环境已迁移为 3.15，最新服务已部署并重启；平台 PID 6916，工作区 D:\Projects\test\web。
- dist 最新 Windows ZIP SHA-256：2ac0c7d931e6ec12e740c81883f9650083da44e05812db1a36ad257aa8b286e2；在线 exe SHA-256：e0ea0b1f04edb6d6d40b59b14475a0f8336984488fc7fb59aed64a4eba587b6a。
- 归档、SBOM、许可证及 SHA256SUMS 已同步；21 项 Windows 生命周期通过，原始报告 `.artifacts/windows-deployment-release-20261020/report.json`。
- 审查详细证据、动态中文及历史回放统计已完成回归和实页对账。备份位于 `.artifacts/deployment-backup-20261020/`，业务数据不变。
- 未签名、未提交、未发布；用户人工验收和 GitHub 自托管视觉 runner 等发布条件仍未完成。最终详报 `.artifacts/deployment-final-20261020/REPORT.md`。

## 2026-10-20 候选与在线状态

- 本轮候选修复历史回放双计数、服务启动被误认为文件修改、审查状态与英文残留，并将 x/sys 升至 v0.44.0。
- 新 dist ZIP SHA-256 ba1d5f510a40dae8dc2b66a6a324d807d4940175f7e349522510685195c01598；Go/vet、Python 186 项、Edge 与 21 项 Windows 生命周期通过，漏洞扫描无发现。
- 当前在线平台和博客为保留预览而维持旧进程；新补丁部署和失效 Python venv 重建待短暂停机确认。
- 真实启动复验已通过，博客 http://127.0.0.1:5003/ 可浏览；GitHub runner 数量仍为 0，签名、版本确认与公开发布未执行。详见 GITHUB_RELEASE_PREPARATION.zh-CN.md。


## 最新收口（覆盖下方历史基线的当前状态）

- 最终报告、文件增删行数、服务预览地址、用量持久账本、阶段检查点及测试隔离均已修复回归。
- 当前服务工作区 D:\Projects\test\web；Windows 发布包已重建并通过 21 项原生生命周期验收。
- 归档 SHA-256：bf1cd0962c67b44569b5d08dde7fcbc2108ff6ae3a338b2dc7cd53a10d2c9b0c；SBOM、许可证与 SHA256SUMS 已校验。
- 最新证据见 .artifacts/human-workbench-20261019/REPORT.md；供应商 TLS 不稳定仍是外部限制，用户最终人工验收尚需进行。
- 下方变更数量和基线提交是 2026-10-12 的历史盘点，不代表今天的工作树数量。

这份清单记录 V3 收口阶段的源码基线，不是新的产品功能说明。后续清理、测试和发布归档都必须以当前源码为输入，不能回退用户已有修改。

## 基线

- 分支：`master`
- 基线提交：`e90aeea`
- 基线提交标题：`Enhance workflow orchestration and studio resources`
- 工作区：`D:\Projects\Golang\goflow_agent`
- 当前验收服务工作区：`D:\Projects\test`
- 正式目标：Windows 10/11 x64 本地应用
- 容器路径：不作为产品运行方式

## 变更规模

当前工作区相对基线提交包含：

| 状态 | 数量 | 当前处理结论 |
| --- | ---: | --- |
| 修改 | 221 | 保留，逐项纳入最终回归 |
| 删除 | 30 | 保留删除，确认无迁移和恢复依赖 |
| 新增 | 162 | 先按 V3 功能、测试、验收和文档分类，禁止盲删 |
| 合计 | 413 | 未完成最终清理和发布冻结 |

## 按目录分类

| 目录 | 数量 | 归类 |
| --- | ---: | --- |
| `internal/` | 229 | Agent、状态机、任务暂存、原生沙箱、MCP 和 API 核心实现 |
| `scripts/` | 37 | 浏览器烟测、黄金场景、平台验收、发布和安全校验 |
| `configs/` | 29 | 中文资源、Provider、连接和默认运行配置 |
| `docs/` | 28 | V3 架构、安装、资源、Skill、MCP 和发布说明 |
| `cmd/` | 12 | 主程序、安装器、数据维护和平台编排 |
| `mcp_servers/` | 15 | 文件、网络、主机、命令、Office、SSH 和 Skill 工具 |
| `skills/` | 15 | 开发、网安、运维、资料核验和文档能力 |
| `kits/` | 10 | 内置领域资源包 |
| 其他 | 38 | CI、工作流、模板、README、Schema 和计划文档 |

## 必须保留

- `internal/agent`、`internal/session`、`internal/taskstaging`、`internal/nativesandbox`、`internal/mcp` 的 V3 执行闭环。
- `internal/api/web/assets/views/workflows.js` 和 `styles-workbench.css` 的当前画布、抽屉、拖放和响应式实现。
- 五类领域 Skill、内置工作流、工具代理、附件和 Office 处理能力。
- Windows 原生验收器、真实 Provider 矩阵、浏览器烟测和安全回归脚本；Linux 验收器仅作为历史兼容工具保留。
- `PROJECT_IMPLEMENTATION_PLAN.zh-CN.md` 与 `V3_BASELINE_AND_MIGRATION.zh-CN.md`。

## 进入清理复核的内容

- 已被 V3 页面替代的旧页面、旧 API、旧翻译和无调用 CSS。
- 重复工作流、重复 Skill、重复 Kit 和不再出现在普通资源目录的内部辅助资源。
- 仅用于一次性调试的探针、临时截图脚本、生成缓存和旧服务日志。
- Docker/Podman 文件、容器专用配置和仅服务于旧容器路径的兼容代码。
- 无调用的示例、旧测试夹具和不再被发布或验收引用的资源。

清理前必须通过引用搜索、资源链接检查和会话/导入/恢复依赖检查；本阶段不直接删除上述内容。

## 当前安全基线

- `python scripts/validate_no_secrets.py`：通过。
- API Key、SSH 私钥和外部视觉 Provider 临时输入不写入源码、报告或公开文档。
- `.artifacts/`、缓存、临时工作区和 `secrets/` 不纳入版本发布内容。
- 用户已有工作区修改不回退。

## 发布前硬门

1. 工作流画布拖放、抽屉和线路最终回归。
2. 旧实现和无调用资源清理后，资源链接、中文化和文档校验通过。
3. `go test ./... -count=1 -p 1`、`go vet ./...` 和脚本安全回归通过。
4. 从最新源码重建 Windows amd64 ZIP、SBOM、许可证报告和摘要。
5. Windows 真机完成安装、升级、回滚、恢复和卸载检查。
6. 重新截图检查桌面、移动、浅色、深色和抽屉状态。
7. 计划书更新为唯一最终状态，启动最终服务并记录访问地址。

当前状态：产品范围已收敛为 Windows-only；旧 P7 `18/18` 已被质量契约 v2 作废，当前质量 v2 有效轮次为 `3/18`，因此尚未进入最终发布冻结。

## 2026-08-16 阶段 3 清理结果

- 已删除无任何源码、发布脚本或测试引用的 `.dockerignore`。
- 已删除仓库根目录遗留的 `FULLSTACK-PLAN.md` 本地旧计划，并移除对应 `.gitignore` 条目；该文件不属于 V3 产品文档。
- 已确认并保留 `scripts/validate_deployment_assets.py` 中的容器路径拒绝清单。它用于阻止 Docker/Podman 文件回流，不是产品运行代码。
- 已确认并保留 `human-input-security-review` 兼容别名。该别名仍由模板 API 和恢复测试消费，删除会破坏已有工作流标识。
- 已保留 `.artifacts/`、测试验收器和真实 Provider 报告；它们是发布证据，不纳入发布归档，也不属于运行时资源。
- 清理后 `validate_resource_links.py`、`validate_web_i18n.py`、`validate_docs.py` 和 `git diff --check` 均通过。

当前状态：阶段 3 低风险清理、阶段 4 全仓最终回归、阶段 5 双平台归档与安装生命周期、阶段 6 截图审查和 Linux VM 验收已完成；P7 质量 v2 严格矩阵尚未完成。最新 Linux 原生验收报告位于 `.artifacts/p7-linux-native-offline-final-20260902/report.json`，报告为通过；VM 口径和 `allow_virtual_machine` 策略已明确记录。

### 2026-10-09 Windows 本地平台最新增量

- 新增时间线用量 API、PC 用量页、K/M/B 全局单位、小时/日趋势、构成图、智能体排行、颜色关联 Tooltip 与可点击图例。
- 深色模式显式设置图表 SVG 坐标文字 `fill`、网格、四条序列、柱段、图表表面和 Tooltip 高对比配色；Edge 自动化与最终截图均通过。
- 运行控制位置、审批恢复、取消状态、Provider 超时暂停、MCP 健康误报和动态中文均已修复。
- 当前源码完成五页面 PC 浏览器验收；发布范围继续仅为 Windows amd64。
- 严格 Provider 证据为 `16/18`：跨领域第 1 轮通过，第 2 轮因供应商 `403 insufficient_user_quota` 失败，第 3 轮未执行；该项不以本地假响应测试替代。
- 最新 Windows amd64 归档已从本轮源码重建并通过 SBOM、第三方许可证、摘要及安装/升级/回滚/卸载生命周期校验：`dist/goflow-agent_dev_windows_amd64.zip`，SHA-256 `9299d661c441c9bc6e820572e02da0348d455a05da80a1276ed8f5439845a8a7`；生命周期报告为 `.artifacts/p7-windows-native-20261010-resource-scope-final/report.json`。
- 运行结果与 Token 口径已收口：完整最终阶段输出直接进入结果区；`blocked/failed` 决策映射真实终态；工作台返回当前运行保留时间线 Token 聚合，缓存命中率固定按 `cached/input` 计算，不与 retry 链累计相加。
- 工作区内虚拟环境解释器可在路径边界检查后由命令工具直接运行；Flask 博客已完成四路由 test client 验证并在 `127.0.0.1:5000` 启动。
- 资源目录完成内容级补强：中文友好标题与说明覆盖默认 113 项目录；智能体、技能、工作流、MCP 连接和工具的热加载/激活状态与实际运行一致；模型 changed_files 由成功写工具证据校正。
- 普通任务暂存排除正式交付证据、人工验收、备份和嵌套 task-staging；已应用任务仅保留交付文件与恢复元数据，不复制无关验收资产。

### 2026-10-12 当前发布候选（权威增量）

- 普通 Agent 的 Provider 中断现在保存可恢复消息与工具证据，并提供同一运行“继续生成”；旧运行缺少快照时不会提供伪继续。工作流已覆盖 HTTP/2 `PROTOCOL_ERROR` 有限重试，文件树排除 `.venv` 和运行噪声。
- Node 24 Windows AppContainer 模块解析兼容已通过真实 Node MCP 协议与边界测试。当前 Go 全包、`go vet`、Python 183 项、共享预检、Edge 功能/性能、资源、中文化、文档、安全和部署资产均通过。
- 任务页输入框上方现提供自动路由、具体智能体和具体工作流选择；当前真实运行时 15 个智能体均可选，Edge 回归验证目标选择会同步实际运行模式。
- 发布目录仅包含 Windows amd64 ZIP、SBOM、第三方许可证与摘要；ZIP SHA-256 为 `49496ae7490577731ceda1fbe6b7f89a641d3562a8eb96dfb5a140cc7c238863`，强归档校验通过。
- `.artifacts/p7-windows-native-20261012-agent-selector-final/report.json` 记录 21 项检查全部通过，包括首次无 Key 启动、失败升级保护、升级、回滚、重启、数据保留、重装、清除数据卸载和临时目录清理。
- 历史测试运行、用量、事件缓存、两个旧任务暂存和可再生缓存已清理；用户博客源码、数据库、虚拟环境、人工验收和正式交付证据保留。
- GitHub Actions 的 CI/Release 仍为 Windows-only，归档未签名的本地 `dev` 构建只用于候选验收；正式 GitHub Release 应由 OIDC/Cosign 工作流生成签名。
- Provider 质量 v2 仍为 `16/18`，剩余跨领域第 2/3 轮及用户人工验收未被本地测试替代，因此候选可供验收但尚不宣称质量矩阵完全冻结。

## 2026-08-16 Linux 最终验收证据

- `.artifacts/linux-native-acceptance-20260816/report.json`：`passed: true`，主机、原生恶意沙箱、Python/Node MCP、Linux amd64 发布构建、归档摘要、安装、升级、回滚、重装、卸载和数据清理全部通过。
- `.artifacts/linux-native-acceptance-20260816/11-native-sandbox-malicious-fixture.log`：恶意沙箱夹具未跳过并通过。
- `.artifacts/linux-native-acceptance-20260816/13-python-node-mcp-protocol-boundary.log`：Python 与 Node MCP 协议和边界夹具均未跳过并通过。
- 报告使用 `allow_virtual_machine: true`，因此结论是“授权 VMware Linux x86_64 功能与隔离验收通过”，不宣称物理宿主验收。
- 发布构建阶段使用已同步的离线 Go 模块缓存重跑通过；任何仅因远端联网失败产生的旧日志不纳入通过证据。

## 发布门状态（历史清单，当前以 2026-09-02 复核为准）

1. [x] 工作流画布、抽屉、拖放、线路和响应式回归。
2. [x] 旧实现清理、资源链接、中文化和文档校验。
3. [x] 全仓 Go/Python/安全回归、`go vet` 和 `git diff --check`。
4. [x] Windows amd64 ZIP、SBOM、许可证报告和 SHA-256 摘要按 Windows-only 发布链重新冻结。
5. [x] Windows 安装、升级、回滚、恢复、卸载和数据保留按新的 Windows-only 最终 ZIP 重新验收。
6. [x] 桌面/移动、浅色/深色真实浏览器截图审查。
7. [x] 计划书、发布清单和最终服务地址更新。
8. [ ] 质量契约 v2 的真实 Provider 六领域连续三轮 `18/18`。

最新 Windows 归档验收证据：`.artifacts/p7-windows-native-20260831/report.json`；报告绑定当前 `dist/goflow-agent_dev_windows_amd64.zip` 的 SHA-256，并证明生命周期目录已清理。当前源码服务的 `/console` 和 `/workflows` 均返回 HTTP 200。

## 2026-08-18 P7 当前源码候选复核

- 当前 P7 源码已通过 `go test ./... -count=1 -p 1` 与 `go vet ./...`；Go 缓存和临时目录均显式位于 `.artifacts/`，避免把用户全局缓存权限问题误判为代码失败。
- 使用当前源码生成 Windows/Linux amd64 临时候选归档，位于 `.artifacts/p7-release-current/`；两个归档及 `SHA256SUMS` 通过 `validate_release_archives.py`。该目录是 P7 中间证据，不替代最终 `dist/` 冻结。
- 当前 Windows P7 候选完成安装、无效升级保护、升级、回滚、回滚后再升级、保留数据卸载、重装和清除数据卸载，报告位于 `.artifacts/p7-windows-native-current/report.json`。
- 历史记录：2026-08-18 的 Linux P7 候选曾因 SSH/审批服务问题未复验；该历史失败不代表当前状态，当前最终归档已在授权 Linux VM 完成复验。
- 文件系统中没有 Docker/Podman 产品文件或被 V3 替代的旧页面文件；`docker/` 仅剩未跟踪的空目录，不进入 Git 或发布归档。现有 `configs/agents`、`configs/providers` 和 `configs/mcp_servers` 仍被加载、资源管理、归档与链接验收消费，继续保留。

## 2026-08-31 最终收口复核

- P7 严格矩阵通过：六领域各连续 3 轮，共 `18/18`；权威校验命令为 `python scripts/validate_p7_matrix.py D:\\Projects\\test`。
- 最新源码全量回归通过：`go test ./... -count=1 -p 1`、`go vet ./...`、Python `167 tests, OK, skipped=2`，以及安全、资源、Web i18n、文档、部署资产和浏览器 smoke。
- 最新 `dist/` 产物已重新构建并校验：
  - Windows amd64：`goflow-agent_dev_windows_amd64.zip`，SHA-256 `46eb743ee14b96621dbe4e4ae6d528530e509fd3122d3bb23d4e8d1382b4bd7e`。
  - Linux amd64：`goflow-agent_dev_linux_amd64.tar.gz`，SHA-256 `8471b7b8d11bef8a0bee4b81afc6291208353b3987e6a1f97d0f975c6d7f20cb`。
- 最新 Windows 生命周期报告为 `.artifacts/p7-windows-native-20260831/report.json`；最新 Linux VM 原生报告为 `.artifacts/p7-linux-native-20260831/report.json`，两份报告均为通过，Linux 报告已由外部验收校验器独立验证。
- Linux VM 验收使用 `systemd-run --user --scope -p Delegate=yes`、授权 VM 策略和固定离线 Go 模块缓存；结论是授权 Linux x86_64 VM 功能/隔离验收通过，不宣称物理宿主验收。
- 发布归档不包含 `.artifacts`、`.goflow`、`backups`、`secrets`、`task-staging`、运行 workspace 或 Python 缓存；可再生测试/构建缓存在最终清理阶段删除。
- Windows 工具链 `CGO_ENABLED=0`，本机 race 未执行；Ubuntu CI 仍保留并承担 `go test -race ./...` 门禁。该限制不影响普通测试、vet 或 Linux 原生验收结论。
- 最终清理删除约 366 MB 重复 P7 runner 历史目录，仅保留最新成功批次和已引用的失败分析批次；另删除可再生 MCP 二进制缓存、smoke 临时目录、旧截图和异常根目录文件。正式 P7 轮次证据、双平台报告、最新 26 张截图与 `dist/` 均保留。

## 2026-09-02 质量 v2 发布候选复核（当前权威状态）

- 旧 P7 `18/18` 使用已废止质量契约，不能作为当前发布证据；当前质量 v2 正式轮次为 `0/18`。最新 Provider 探针返回 `403 Forbidden`：`.artifacts/p7-provider/20260902-083756-053628/report.json`。
- 最新回归通过：本地 Go 全包、`go vet`、Python `172 tests, OK, skipped=2`、共享预检和 Edge 浏览器/性能门；Ubuntu VM 通过普通全量测试及 `go test -race ./... -count=1`，race 日志记录 36 个包通过、0 个数据竞争。
- 最新 `dist/`：Windows `ac5e745c9595f762a586080235d5787fd994f70e8572302cd9172fd64b8a9aae`；Linux `38af3e04eeadb87cd62c9b0da9e336dfcd84f91f3b52e375e7e3d8cf5dce3e43`。SBOM、许可证和 `SHA256SUMS` 已校验。
- 最新生命周期报告：Windows `.artifacts/p7-windows-native-offline-final-20260902/report.json`；Linux `.artifacts/p7-linux-native-offline-final-20260902/report.json`，后者已由外部校验器独立验证；同目录保留 `race.log`。
- Office 严格产物位于 `.artifacts/office-strict-20260901-02/deliverables/`，已通过独立 evaluator、Word/Excel/PowerPoint 实际打开和视觉检查。
- 发布候选只剩一个外部门：Provider 恢复后重新完成质量 v2 六领域连续三轮并由 `validate_p7_matrix.py` 返回 `18/18`。Linux VM race 已实跑通过，CI 继续保留相同门禁。

## 2026-09-10 Connected MCP 工具最终复核（当前权威状态）

- 资源目录正式公开六类资源：智能体、子智能体、工作流、Skill、单个 MCP 工具和 MCP 连接；当前最新服务发现 69 个工具及 69 份输入 Schema。
- connected 工具节点实现 Agent/Skill → MCP Tool → Agent：上游模型按唯一工具 Schema 生成参数并执行，工具节点发布结果和产物引用，下游模型通过自动上下文或显式 input 映射消费。
- 工具调用继续受上游智能体权限、原生任务工作区、幂等和审批恢复约束；旧 assisted 工具节点保持兼容。
- 资源列表 keyed DOM 更新、工具一键插入、自动打开主设置区、工具模式和 Schema 必填字段均通过 Edge 浏览器验收；最终截图目录 `.artifacts/frontend-connected-tools-20260903/` 共 27 张。
- Python MCP 标准流强制 UTF-8，工具目录中文说明通过真实协议和截图复核。
- 最终本地 Go 全包、`go vet`、Python `177 tests, OK, skipped=2`、共享预检、浏览器和性能门通过。
- Windows-only 发布链已接线：构建器、归档校验器、CI 和 Release 默认只接受 `windows/amd64`；Linux 兼容代码和历史脚本保留但不进入发布或完成硬门。最终 ZIP SHA-256 为 `0782db1a076596b5468361deea7c1a9a28de6837c5d802fb84b28aa30da7f72c`，生命周期报告为 `.artifacts/p7-windows-native-only-final-20260917/report.json`。
- Provider 配置已改为 `https://tokenmarket.cheap/v1`、Responses API、`gpt-5.4-mini`、`mid` 推理和 5 分钟超时。Key 只允许通过 Web Studio 设置页写入 Secret 文件；新 Key 保存后必须重新执行四能力探针。
- 质量 v2 当前有效进度为开发领域 `3/3`、其余领域 `0/3`，合计 `3/18`。安全领域最新尝试在修复阶段返回 `403 insufficient_user_quota`，充值后必须从安全第 1 轮重跑。
- Windows-only 最终 ZIP 与生命周期门已关闭；剩余发布硬门只有新 Key 下的 Provider 四能力探针和 `15` 个有效复杂任务轮次。Ubuntu/Linux 验收不再是任务。

## 2026-10-13 实时工作区版本候选

- 新任务改为真实工作区执行：任务前外部只读快照、直接使用项目环境、逐文件恢复日志、Diff/质量门和最近 3 个版本共同组成交付边界。
- 设置页公开版本时间、文件数、状态和可用回退；恢复接口仅接受当前 SHA-256 与版本完成态一致的最近版本，防止覆盖后续修改。
- `command_tools` 新增 3 个受管进程工具，支持开发服务器跨任务结果页持续运行、状态/日志查询和进程树停止；工具不接受 Shell，仍受工作区路径、环境白名单和执行审批约束。
- 最新运行时资源目录为 116 项，其中 MCP 工具 72 个；三项新增工具已通过真实资源发现和 Flask 项目集成测试。
- 旧 task-staging 任务仍按原策略加载，避免历史运行失效；新版本保留上限为 3，过期版本目录自动清理。
- 非 Provider 回归已通过：Go 全包、Python 183 项、Web 国际化、密钥、资源、文档和 Edge 全页面 smoke。当前二进制已部署到 `http://127.0.0.1:8080/console`。

## 2026-10-14 Windows 工作台最终交互候选

- 运行页完成语义时间线、当前运行操作、完整最终输出、结构化验收结论和精简证据；模型调用与缓存数据统一来源于保留的 `token_usage` 事件。
- 设置页完成 Windows 系统文件夹选择、动态工作区重绑定、三版本回退和授权撤销；普通页面不再展示数据备份和无实际行为的工作区按钮。
- 用量范围与指标卡完成清晰选中态和对齐；工作流画布与节点运行预览共用实时运行状态，当前节点会自动聚焦并展示动作、输出和审批。
- 最新 Go/Python/vet/Edge/i18n/文档/密钥/资源门全部通过；截图目录 `.artifacts/final-ui-20261014/`。

## 2026-10-18 自动编排 Windows 候选

- 自动任务预检包含领域、复杂度、策略、推荐目标、资源固定版本和逐阶段编排计划；开放式 Web 美化使用规划、实现、质量验证、审计和最终交付链。
- 运行页自动编排预览、动态中文、浏览器回归和性能门通过；Edge smoke 真实验证工作流 ID、Agent ID、阶段顺序和用户可见中文互不冲突。
- Windows amd64 归档、`SHA256SUMS`、`SBOM.spdx.json` 和 `THIRD_PARTY_LICENSES.json` 已按当前源码重新生成并校验。
- 当前 Windows 生命周期报告为 `.artifacts/p7-windows-native-20261018-final/report.json`，结果通过。
- 外部发布门尚未关闭：P7 现有 17 份报告中 16 份通过，跨领域第 2 轮失败且第 3 轮缺失；严格矩阵当前返回失败。

## 2026-10-18 Windows 最终发布候选

- 质量契约 v2 严格矩阵已关闭：六领域各连续 3 轮，合计 `18/18`；`validate_p7_matrix.py` 独立检查 Provider 完整性、唯一 run ID、交付产物、质量分和连续轮次后返回通过。
- 跨领域三轮质量分均为 95，资料核验三轮均为 100，其余正式轮次均为 95；失败轮和跳过 Provider 探针的调试轮保留在 `失败批次`，不计入候选。
- 全量 Go、`go vet` 和 Python 183 项回归通过；前端自动编排、中文化、浏览器 smoke、资源链、实时工作区版本和 Windows 目录选择均纳入当前源码。
- 最终候选仅发布 `windows/amd64`。归档包含 SBOM、第三方许可证和 `SHA256SUMS`，最终生命周期报告为 `.artifacts/p7-windows-native-20261018-release-final/report.json`。
- 工程发布门均已完成；当前候选进入用户人工验收，不再要求 Ubuntu/Linux 复验。
