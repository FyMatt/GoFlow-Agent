# Web Studio 前端优化计划

[English](./frontend-optimization-plan.md) | [简体中文](./frontend-optimization-plan.zh-CN.md)

本文记录 Web Studio（`internal/api/web/`）的前端现状测量、结论与分阶段改造方案。
目标是让这个本地优先的智能体工作流平台在“方便且好用”这个方向上站住：首屏更快、
交互不丢状态、样式可维护、无用资产不再累积。

## 1. 结论先行

**不更换前端框架。** 保留现有原生 ES module 渲染模型，补齐它缺失的三件东西：
传输压缩、DOM diff、样式体系。

理由是测量出来的，不是偏好：

- `internal/api/web/assets/views/workflows.js` 单文件 15,162 行、811 个函数，
  是一个完整的画布编辑器。用 Vue/React/Svelte 重写它属于季度级工程，风险极高。
- `assets/app.js`（341 行）本身质量不错：hash 路由、动态 `import()` 代码分割、
  `showSequence` 防竞态、滚动意图追踪。外壳不是问题所在。
- 视图层已经在**手搓 diff**。例如 `views/workflows.js:5331`：
  `if (select.innerHTML !== nextHTML) select.innerHTML = nextHTML;`
  `nextHTML` 这个变量名在 1972、2318、3120、5331 反复出现，是字符串级脏检查。
  这说明需要的是一个真正的 diff，而不是一套新框架。

## 2. 测量基线

以下数据来自当前工作树（`.tmp/console_audit.py` 可复现资产审计部分）。

### 2.1 首屏传输量

`web/console.html` 同步加载两个 CSS 与 `i18n.js`，之后才开始加载视图模块：

| 资产 | 原始 | gzip | 压缩率 |
|---|---|---|---|
| `assets/styles.css` | 477,876 B | 61,357 B | 12.8% |
| `assets/styles-workbench.css` | 91,075 B | 14,636 B | 16.1% |
| `assets/i18n.js` | 550,626 B | 139,421 B | 25.3% |
| `assets/views/workflows.js` | 768,416 B | 146,701 B | 19.1% |
| `assets/views/chat.js` | 433,451 B | 86,009 B | 19.8% |

**关键发现：`internal/api/api.go` 与 `internal/api/console.go` 均无 gzip 中间件，
`Content-Encoding` 在整个 `internal/api/` 下无任何实现。** 上表“原始”列就是实际
上线字节数。首屏约 1.18 MB 未压缩。这是投入产出比最高的一项，且只需标准库。

### 2.2 JavaScript 规模

```
assets/views/workflows.js   15,162 行   811 函数
assets/views/chat.js         9,289 行
assets/i18n.js               7,684 行   3,043 个唯一 key / 6,265 份语言副本
assets/views/settings.js     2,010 行
assets/views/catalog.js      1,093 行
assets/api.js                  678 行
assets/app.js                  341 行
合计                        37,060 行
```

### 2.3 焦点/滚动抢救代码

`innerHTML` 整片重写会销毁输入框，因此视图里手工保存并恢复光标与滚动位置：

| 文件 | `activeElement`/`selectionStart`/`scrollTop` | `innerHTML` |
|---|---|---|
| `views/chat.js` | 28 | 73 |
| `views/workflows.js` | 15 | 93 |
| `views/catalog.js` | **0** | 5 |
| `views/settings.js` | **0** | 7 |

`catalog.js` 与 `settings.js` 用同样的 `innerHTML` 模式但零抢救代码，其中的输入控件
在重渲染时很可能正在丢失焦点与光标位置（静态推断，未经界面复现）。

### 2.4 CSS 分层历史

`assets/styles.css` 19,665 行，尾部有 10 个被**追加**而非合并的重设计层：

```
16480  /* GoFlow visual refresh: dense, technical Agent operations surface. */
17524  /* Product workbench refresh: lower the AIGC feel... */
17802  /* Workflow side panel and runtime preview refinements. */
18290  /* Workflow run preview... */
18683  /* Unified workbench pass: focused defaults and calmer light/dark themes. */
19024  /* Run page and sidebar correction... */
19239  /* Workflow studio final pass... */
19321  /* EOF runtime highlight override: keep current node obvious after all theme passes. */
19454  /* Light theme final-output readability. */
19527  /* Runtime/current node polish. Kept at EOF to override older emphatic runtime styles. */
```

后果：

- `!important` 共 1,075 处（`styles.css` 604 + `styles-workbench.css` 471）
- `:root` 定义 5 次、`:root[data-theme='dark']` 5 次、`.sidebar` 4 次、`.flow-node` 4 次
- `--radius` 三次重定义（12px → 8px → 8px）；`--accent` 跨两个文件定义 4 次
- 字号无标度：12px×214、11px×178、10px×79、13px×62，另有 9px/10.5px/11.5px/14.5px
- 完全没有间距 token（`--space-*` 不存在）
- 48 条媒体查询使用 19 个不同断点（600/640/700/720/760/860/880/900/980/1080/
  1100/1120/1180/1200/1201/1280/1360/1500/1760），760 与 700、900 与 880 混用

### 2.5 由分层导致的真实缺陷

1. **深色模式运行态节点高亮失效。** 最终生效调色板在 `styles-workbench.css`
   （最后加载），其中 `--blue: #ececec`（深色）。而 `styles.css:19527` 起的高亮块
   完全基于 `var(--blue)` 构建边框、outline 与 box-shadow，深色下近白，
   与普通 hover/selected 难以区分。相邻的 `runtime-waiting` 用真琥珀色 `--amber`，
   导致“等待中”比“正在执行”更显眼，**语义倒挂**。
2. **语义色整体去饱和。** `--blue`/`--purple`/`--cyan`/`--teal`/`--brand` 在各层里
   收敛为灰阶，只剩 `--green`/`--red`/`--amber` 能表意。
3. **Inter 从未加载。** `--font-sans: Inter, ...` 已声明，但全项目无 `@font-face`，
   `console.html` 也无字体链接。只有本机装了 Inter 的用户看到的是设计意图。
4. **动画死代码。** `portPulse` 混用 `var(--blue)` 与硬编码旧蓝
   `rgba(37, 99, 235, .20)`；`styles.css:18659` 与 `19399` 两处
   `prefers-reduced-motion: no-preference` 精心开启的节点脉冲，被 19527 层的
   `animation: none !important` 全部覆盖。

### 2.6 无用资产审计

审计脚本需处理动态构造的 key，否则会误判。`resource_actions.js:82-105` 把 key 先拼进
变量再调用（`` const kindKey = `resource.action.${kind}.${actionName}` ``），
且 `actionName` 来自后端 `/api/capabilities`；只匹配内联 `` t(`...${ `` 会把 26 个
存活的 `resource.*` key 误报为死键。修正后结果：

| 命名空间 | 唯一 key | 使用中 | 死键 |
|---|---|---|---|
| `approvals` | 190 | 11 | **179** |
| `workspace` | 126 | 5 | **121** |
| `tour` | 77 | 0 | **77** |
| `chat` | 694 | 659 | 35 |
| `workflow` | 1463 | 1428 | 35 |
| `settings` | 345 | 312 | 33 |
| `common` | 25 | 16 | 9 |
| `runtime` | 10 | 1 | 9 |
| `catalog` | 55 | 54 | 1 |
| `resource` | 26 | 26 | 0 |
| `nav` / `view` / `theme` / `brand` | 32 | 32 | 0 |

其他：

- **图标**：`assets/icons.js` 定义 43 个，10 个在 icons.js 之外无引用：
  `book-open`、`chevrons-left`、`layout-grid`、`layers`、`library`、`maximize-2`、
  `list-todo`、`plug`、`users`、`wrench`
- **CSS 类**：1,177 个不同类名，245 个从未出现在存活 JS/HTML 中。已验证所有
  `classList.add/toggle` 调用都使用字面量字符串，因此“未出现”即真死。
- **导览（tour）已断链**：驱动引擎 `assets/onboarding.js` 已在工作树中删除，
  但残留约 30 个 `data-tour-id` 锚点、77 个 `tour.*` key、77 处 `styles.css` 引用
  与 3 处 workbench 引用。
- 同批删除的视图 `approvals.js`、`memory.js`、`overview.js`、`status.js`、
  `workspace.js` 留下了上表中 `approvals.*` / `workspace.*` 的死键。

## 3. 分阶段方案

### 阶段 0：止血（本阶段，低风险高收益）

| # | 项目 | 预期效果 |
|---|---|---|
| 0.1 | Go 标准库 gzip 中间件（`console.html` + `/assets/*`） | 首屏 ~1.18 MB → ~230 KB |
| 0.2 | 静态资源 `ETag` + `If-None-Match` 条件请求 | 二次访问 304，省重复传输 |
| 0.3 | 字体改为 system-ui 优先，去掉未加载的 Inter | 消除跨机器排版漂移 |
| 0.4 | 新增 `--runtime-active` 语义 token，高亮不再依赖 `--blue` | 修深色模式语义倒挂 |
| 0.5 | 清理 `portPulse` 硬编码旧蓝与被覆盖的 `no-preference` 块 | 删死代码 |
| 0.6 | 删除 tour 全链路（i18n key、CSS、`data-tour-id`） | 断链功能彻底移除 |
| 0.7 | 删除 `approvals.*` / `workspace.*` 死键与 10 个未用图标 | i18n 与图标瘦身 |
| 0.8 | 保留 `.tmp/console_audit.py` 为可复跑审计脚本 | 防止无用资产再次累积 |

### 阶段 1：构建层 + DOM diff（约 1 周）

- 引入 esbuild（Go 单二进制，可接 `go generate` 与现有 CI，不需要运行期 node_modules）：
  bundle、minify、按视图分 chunk。
- 引入 **morphdom**（~2 KB，vendor 进仓库，不引 node_modules）替换四个视图的
  `innerHTML` 赋值：`el.innerHTML = h` → `morph(el, h)`。视图代码零改动，
  可直接删除 43 处焦点/滚动抢救代码，并顺带修掉 `catalog.js` / `settings.js`
  的隐性焦点丢失。
- 新代码逐步改用 **uhtml**（~2.5 KB）：插值默认转义，替掉靠人工在 178 处
  `innerHTML` 里记得调用的 `escapeHTML()`。这个平台要渲染 LLM 输出、文件内容与
  命令回显，漏一处就是 XSS。
- 补前端测试：`api.js`、`approval_counts.js`、`run_context.js` 是纯逻辑模块，
  最容易测也最值得测。目前前端零测试覆盖，而后端测试写得不错，投入比例倒挂。

**依赖立场说明**：`go.mod` 仅 227 字节、纯标准库，这份克制是项目优点。因此第三方
前端库一律 vendor 单文件进 `assets/`，保持 clone 即 `go build`。

### 阶段 2：CSS 收敛（约 1~2 周，后续所有美化的前提）

- 5 份 `:root` 合并为单一 token 源；补 `--space-*` 与 `--text-*` 标度。
- 用 `@layer`（base / components / theme）替代 1,075 处 `!important` 竞争。
- 19 个断点收敛到 4~5 个。
- 清理 2.6 节中 245 个死 CSS 类。

### 阶段 3：拆分与视觉提升（持续）

- `views/workflows.js` 按职责拆模块（canvas / inspector / runtime / palette / edges），
  配合 esbuild 分 chunk。
- 视图切换过渡、列表骨架屏（当前 `skeleton`/`shimmer` 命中数为 0）、
  统一 toast 反馈（当前仅 2 处命中）。
- 图标 API 统一：`icon()` 现在硬编码 `width="16"`，且 `viewIcon()` 返回字符串
  而其他三个返回标记。
- `brand-mark.svg` 改为跟随主题：现在硬编码米色 `#E8E2D4`，深色模式下突兀。

## 4. 待决事项

- **排版密度**：现状 12px×214、11px×178、10px×79，16px 以上仅 12 处。若保留这种
  “技术工具”密度，则按此基准建标度；若整体放大一档，可读性与无障碍明显改善，
  但会影响全部既有布局。阶段 2 开始前需确认。
- **Inter**：阶段 0 先改 system-ui。若后续要恢复 Inter，需自托管并 subset，
  会给二进制增加约 100~300 KB。

## 5. 验证方式

- `go build ./...` 与 `go test ./internal/api/...`
- gzip 与 ETag 行为由 `internal/api/console_compression_test.go` 覆盖
- `python .tmp/console_audit.py` 复跑资产审计，确认死键与死图标为 0
