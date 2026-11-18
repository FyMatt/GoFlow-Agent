# Windows 本机操作与维护

本指南适用于 Windows 10/11 x64 的 PC Web 平台。无需 GitHub、Ubuntu 或虚拟机；可直接在本地阅读。更多文档见[本地导航](./README.zh-CN.md)。

## 启动与停止

在源码仓库运行（已安装 Go 时）：

```powershell
go build -o goflow.exe ./cmd/goflow
.\goflow.exe --config .\configs\goflow.yaml --workspace D:\Projects\example --http 127.0.0.1:8080
```

发布包使用包内的 `run-goflow.cmd` 或 `configs/goflow.binary.yaml`，不要把源码配置和发布包工具布局混用。打开 <http://127.0.0.1:8080/console>。前台运行时按 Ctrl+C 正常关闭；重启前先确认没有执行中的任务。

若端口占用，先核对进程，不能批量结束所有 Python 或 Go 程序：

```powershell
Get-NetTCPConnection -State Listen -LocalPort 8080,5000,5001,5003 -ErrorAction SilentlyContinue |
  Select-Object LocalAddress,LocalPort,OwningProcess
Get-CimInstance Win32_Process -Filter 'ProcessId=1234' |
  Select-Object ProcessId,ParentProcessId,ExecutablePath,CommandLine
```

`1234` 需替换成确认属于目标服务的 PID。Python 虚拟环境可能具有启动器和实际子进程：两个 PID 不等于两个监听服务。任务启动的预览应使用 `process_status` / `stop_process` 管理；只停止本轮持有的进程，不接管其他任务的服务。没有要求保留预览时，验证结束应停止预览实例。

## 判断是否真的完成

- `running` 期间看当前阶段、模型请求是否连接/重试、最后工具结果和更新时间。供应商过载或断流不等于本地死循环。
- “继续执行”恢复已保存的同一运行和工具观察；“重试运行”是新的尝试。运行中的引用资源通过 API 修改会被拒绝；暂停后资源已变化，继续/审批会返回版本冲突，需要使用当前资源新建任务。版本记录不是工具程序或环境的完整快照。
- 质量门禁没有解除时不能验收。阶段结束、公共冒烟测试通过、报告写了 PASS，都不能代替需求覆盖。
- 对 Web 任务，逐项检查真实文件、动画/布局、用户要求的内容、修改后的测试及页面级证据。只增加动画不等于完成内容扩充；只有初始种子数据不代表已有数据库出现了新文章。
- 总计、执行概览与用量页面应使用同一持久模型账本。缓存命中率为缓存读取 Token / 输入 Token；缓存量不再重复加入总 Token。
- 原始历史中的模型报告可能有错误。校正展示与新增验证不能冒充历史任务已重新通过。

## Python 与补丁问题

优先使用目标项目现有 `.venv\Scripts\python.exe` 和项目实际的 unittest/pytest，不因系统 Python 缺包就重新安装依赖。`expected_sha256` 冲突与“文本匹配为 0”是不同问题；匹配失败无写入，应重新读取小范围准确文本，再做一次精确编辑，不能凭猜测替换旧文本。

审计节点主要复核已有测试和文件证据，不应重复执行不适用的测试套件。用户没有要求的既有风险应单列说明，不能借此把任务扩展成无关重构或安全整改。

命令工具的 `program` 只填写可执行程序，子命令和选项逐项放入 `args`，例如 `{"program":"go","args":["test","./..."]}`。含空格的真实程序路径应作为完整路径填写，不要手动添加 Shell 引号。平台会对可明确识别的开发检查调用纠正这类字段混用；引号、Shell 语法、未知程序和破坏性操作不会自动拆分。参数填写错误不代表本机缺少 Go，不能通过改用 Shell 绕过工具约束。

交付概览按持久化的完整命令结果核对程序、参数和退出状态。长命令日志或外置证据的显示摘要可能被截断，摘要不用于替代完整验证结果；失败退出也不能因为摘要缺少尾部字段而被当成通过。

## 数据与安全清理

默认源码运行可能把会话放在工作区 `.goflow/session.json`；设置独立 `GOFLOW_DATA_ROOT` 时，资源覆盖、密钥及按工作区分组的数据会位于该数据根。以当前配置的路径为准，不能仅按文件夹名字判断是否可删。

| 内容 | 处理方式 |
| --- | --- |
| 源码、测试、构建与验收脚本、配置、技能、工作流、文档 | 保留；测试和脚本不是运行时依赖，但仍是维护与发布所需 |
| `secrets/`、`.goflow/`、`task-staging/`、版本、备份、用户资源及 `.git/` | 不作为杂项删除；可能承担历史恢复、未保存变更和密钥存储 |
| 不再使用的隔离 Go 构建/模块缓存 | 确认无构建使用后清理，可重新生成 |
| 历史浏览器调试 profile、空临时目录 | 确认无进程使用后清理；截图与报告分开保留 |
| 重复中间二进制与归档 | 核对哈希及用途后处理；保留当前可用版本和必要回退副本 |

清理应限定已核对的绝对路径；优先送回收站并记录目录、体积和保留项。不要删除用户的项目虚拟环境、数据库或业务文件来“瘦身”平台。

## 正式 P7 验收运行

真实复杂案例的正式证据不能放在 `.artifacts`、`.tmp` 或任务工作区里：运行器会在开始前直接拒绝，避免跑完一整轮（含付费模型调用）才发现证据无处安放。

```powershell
python scripts\p7_provider_runner.py --case p7-development-platform-migration `
  --acceptance-workspace D:\Projects\test\p7-acceptance `
  --provider-secret secrets\providers\primary\api_key --timeout 5400
```

- `--timeout` 按当时供应商速度留足余量：该案例在正常吞吐下约 15 分钟，供应商变慢时可能超过 30 分钟。
- 先跑 `--probe-only` 确认四项能力可用，再发起完整运行；探针通过不代表整轮一定完成。
- 同一轮目录不会被覆盖，重跑请换新的证据根目录或轮次。
- 运行结束后以该目录的 `report.json` 与 `verification.json` 为准，不用模型自述替代。

## 当前验收边界

新增领域使用[资源扩展指南](./resource-extension-guide.zh-CN.md)。安全评估与事件调查先填写授权范围，未明确授权不会进入工具阶段。通知默认写入本地事件存储；外部通道需单独配置，`stored`、`delivered` 与 `failed` 的含义不同。

接入虚拟机或服务器参阅[远程目标配置与验收](./remote-target-validation.zh-CN.md)。平台仍在 Windows 本机运行；SSH 目标固定指纹、使用密钥引用和精确命令白名单，不把连接成功当作安全检查通过。

联网资料可显式刷新并限定来源域；取得页面、文章发布日期和目标适用版本分别核对，不能用 HTTP 最后修改时间推断发布日期。资源提供工具不代表任务必须联网；私密数据与仅本地任务仍按原边界处理。

自动回归通过证明平台机制，不保证供应商每次都稳定或每个复杂任务都完成。未经新一轮真实任务与人工检查，不得把失败记录标为通过；本地源码服务更新也不代表 `dist/` 旧包已经更新或已发布 GitHub。
