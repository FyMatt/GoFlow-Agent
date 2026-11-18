# GitHub 发布准备与阻断清单

## 2026-10-24 通知失败门禁与时序候选（最新）

当前候选 `.artifacts/notification-failure-20261024/package/goflow-agent_notification-failure-20261024_windows_amd64.zip`，服务 PID 25568。修复安全通知失败仍显示 completed，以及非中止质量门失败事件提前宣布整个工作流 completed 的问题。六场景、全量回归、Edge 与 Windows 21 项生命周期通过；最新详报 `.artifacts/notification-failure-20261024/REPORT.md`。最近三个候选保留，最早被替代候选送回收站；真实模型、真实目标、外部送达及人工/正式发布验收仍未执行。

## 2026-10-24 方案复核后的最新候选

最新本地包为 `.artifacts/plan-audit-20261024/package/goflow-agent_plan-audit-20261024_windows_amd64.zip`，对应服务 PID 22444。并行领域别名已迁入资源，安全输出增加通用类型/取值契约；全量回归、Edge 及 Windows 21 项生命周期通过。完整方案对照与未验事项见 `.artifacts/plan-audit-20261024/REPORT.md`。旧候选保留回退；真实模型、授权专业目标、外部送达、人工验收与正式签名/提交/推送/发布仍未完成。

## 2026-10-24 后续候选：字段验收与安全完整链路

最新本地候选为 `.artifacts/resource-chain-20261024/package/goflow-agent_resource-chain-20261024_windows_amd64.zip`，已部署且程序哈希与包内一致。修复缺失结果引用误通过、增加显式 JSON 字段契约，四条本地真实 MCP 场景、全量回归、Edge 与 Windows 21 项生命周期通过。完整证据见同目录上层 `REPORT.md`；旧 framework 候选保留为回退，不代表最新版。仍需真实模型、授权专业目标与外部送达、人工验收和正式版本/签名；本轮未提交、推送或发布。

## 2026-10-24 资源驱动候选（优先于历史结论）

本轮候选包含 Kit 动态入口、显式通用阶段、安全/运维资源、保留并增强的联网研究、通用通知及读取性能修复。离线指南和资源扩展示例随包携带；最新归档、校验和与 Windows 生命周期证据见 `.artifacts/framework-20261024/REPORT.md`。它是本地候选，不能把历史 `dist/` 或旧 PID 作为本轮证据。

本地回归与确定性模型机制验收不代替真实模型复杂任务、授权远程/安全专业效果、实际外部通知和用户验收。签名、正式版本及 GitHub 提交/推送/公开发布仍未执行；本轮未新增付费任务、连接真实主机或发起扫描。

## 2026-10-22 当前补充（优先于历史在线结论）

最新真实博客任务未完整交付内容扩充和页面验证，门禁未通过。确定的框架问题已经修复并通过本地回归，但本轮没有重跑真实模型任务，也没有重建正式 dist、签名或推送发布。博客预览全部停止，只有 GoFlow 保持在线；不要按旧段落声称 5003 仍可访问。新本地文档已更新到 `docs/`，操作导航为 `docs/README.zh-CN.md`。清理了 648.15 MiB 可恢复缓存及重复归档，必要证据和回退保留，详见 `.artifacts/run-quality-20261022/REPORT.md`。

检查日期：2026-10-20。仓库：FyMatt/GoFlow-Agent，分支 master。

本文件是发布准备，不表示已提交、已打标签或已公开发布。现有工作树包含大量历史修改；本轮没有暂存、提交、推送或修改 GitHub 设置。

## 已完成

- 真实启动任务 run-agent-1792411179987430100 已完成：模型 gpt-6.1-sol、低推理，完成 start_process、process_status 和 browser_snapshot；博客 http://127.0.0.1:5003/ 仍在运行，五个页面/资源返回 200。
- 历史复杂任务矩阵严格校验为六领域各三轮，即 18/18。本轮没有重新调用模型执行该矩阵。
- Go/页面/脚本回归和共享预检已有通过证据；独立的 Python 3.15 环境完成博客三项测试，真实数据库哈希不变。
- 已修复本轮复核发现的历史回放重复累计、无文件变更的启动任务被误判、显式通过被 Risks 标签误判为警告及 none 文案问题。
- golang.org/x/sys 升级至 v0.44.0，关闭 GO-2026-5024；升级后 govulncheck 未发现漏洞。

## 正式发布前仍需处理

最新流完整性补丁已部署，但最后一次真实模型输出残缺，不能计为完整报告验收通过；需供应商稳定后另做真实任务复测。本地故障注入和独立 HTTP 验证不能替代这一门。

1. **本地部署已完成，等待用户验收。** 用户授权后，正式 `.venv` 已在原路径重建为 Python 3.15，最新补丁已部署；旧环境和程序可从 `.artifacts/deployment-backup-20261020/` 恢复。博客测试通过，业务文件与数据库哈希不变；平台和博客均已提供人工预览入口。最新归档与部署证据见 `.artifacts/deployment-final-20261020/REPORT.md`。
2. **视觉验收 runner。** GitHub API 当前返回仓库 runner 数量为 0，而 `.github/workflows/release.yml` 的 real-vision-engineering 作业要求 self-hosted、windows、x64、goflow-vision-acceptance。没有该 runner，发布作业不会满足依赖；本轮未绕过或删除此硬门。
3. **视觉 Provider 输入。** 本轮只读查询仓库 Actions variables 列表为空。runner 需要有效的 GOFLOW_VISION_SECRET_FILE、GOFLOW_VISION_PROVIDER_MODULE 仓库变量所指向的本机文件。密钥放仓库外，模型必须实际支持所需视觉与工具能力，不能拿文本任务结果替代。
4. **签名。** 本地候选包不冒充 GitHub 身份签名。发布工作流会通过 GitHub OIDC 生成并验证 Sigstore bundles；在真实工作流执行前，不可宣称已经完成正式签名。
5. **版本号与用户验收。** dev 仅为候选标识。正式版本号、人工验收结论和是否发布由仓库所有者确认。

## 提交与发布顺序

1. 先查看 `git status --short`、`git diff --stat` 和具体差异，分组审查核心执行、前端、Windows 发布、测试与文档；不能不审查就把大量历史更改全部提交。
2. 使用当前可用解释器执行 `py -3 scripts/validate_no_secrets.py`、`git diff --check`、`go test ./...`、`go vet ./...`；再执行 `py -3 scripts/run_preflight.py --browser-required`。
3. 漏洞扫描使用与当前 Go 兼容的构建，例如 `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`。不要把旧扫描器无法解析新工具链当成项目漏洞。
4. 先提交并推送审核后的分支，让 CI 验证准确提交；不要提前打正式标签。
5. 确认 Windows 生命周期、视觉 runner 与 Provider 门全部通过，再选择正式版本号。标签会触发签名与发布，不在本轮自动执行。

## 人工查看入口

- 平台：http://127.0.0.1:8080/console
- 当前博客：http://127.0.0.1:5003/
- 新修复的构建和环境检查记录：`.artifacts/release-readiness-20261020/REPORT.md`

Python 切换和新补丁部署已经在本轮授权后完成；正式提交、版本号、GitHub runner 配置、签名和公开发布仍未执行。
