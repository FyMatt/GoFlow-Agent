const dictionaries = {
  en: {
    "nav.overview": "Overview",
    "nav.workflows": "Workflow Studio",
    "nav.playground": "Playground",
    "nav.workspace": "Workspace",
    "nav.approvals": "Approvals",
    "nav.catalog": "Resources",
    "nav.status": "Observability",
    "nav.settings": "Settings",
    "view.overview.title": "Overview",
    "view.overview.eyebrow": "Agent platform",
    "view.playground.title": "Playground",
    "view.playground.eyebrow": "Prompt and run preview",
    "view.workspace.title": "Workspace",
    "view.workspace.eyebrow": "Safety boundary",
    "view.workflows.title": "Workflow Studio",
    "view.workflows.eyebrow": "Visual orchestration",
    "view.approvals.title": "Approvals",
    "view.approvals.eyebrow": "Operator gate",
    "view.catalog.title": "Resources",
    "view.catalog.eyebrow": "Agents, skills, and tools",
    "view.status.title": "Observability",
    "view.status.eyebrow": "Status, logs, and session",
    "view.settings.title": "Settings",
    "view.settings.eyebrow": "Setup, configuration, and updates",
    "common.loading": "Loading...",
    "common.errorTitle": "Could not load section",
    "runtime.workspace": "workspace",
    "runtime.trace": "trace",
    "runtime.pending": "pending approvals",
    "runtime.tools": "tools",
    "chat.conversation": "Conversation",
    "chat.agent": "Agent",
    "chat.run": "Run",
    "chat.clear": "Clear",
    "chat.placeholder": "Ask GoFlow. Use @path to attach a workspace file.",
    "chat.files": "File references",
    "chat.fileHelp": "Pick workspace files and insert @path references into the request.",
    "workflow.workflows": "Workflows",
    "workflow.new": "New",
    "workflow.library": "Node library",
    "workflow.save": "Save",
    "workflow.delete": "Delete",
    "workflow.settings": "Node settings",
    "workflow.empty": "Select a node on the canvas.",
    "workflow.runPreview": "Run preview",
    "workflow.run": "Run workflow",
    "workflow.dropHint": "Drag node types into the canvas, or click a node and connect it to another stage.",
    "workflow.connect": "Connect",
    "workflow.remove": "Remove node"
  },
  zh: {
    "nav.overview": "概览",
    "nav.workflows": "工作流 Studio",
    "nav.playground": "任务执行",
    "nav.workspace": "工作区",
    "nav.approvals": "审批",
    "nav.catalog": "资源",
    "nav.status": "观测",
    "nav.settings": "设置",
    "view.overview.title": "概览",
    "view.overview.eyebrow": "Agent 平台",
    "view.playground.title": "任务执行",
    "view.playground.eyebrow": "选择 Agent 并运行任务",
    "view.workspace.title": "工作区",
    "view.workspace.eyebrow": "安全边界",
    "view.workflows.title": "工作流 Studio",
    "view.workflows.eyebrow": "可视化编排",
    "view.approvals.title": "审批",
    "view.approvals.eyebrow": "人工确认",
    "view.catalog.title": "资源",
    "view.catalog.eyebrow": "Agent、Skill、Tool",
    "view.status.title": "观测",
    "view.status.eyebrow": "状态、日志和会话",
    "view.settings.title": "设置",
    "view.settings.eyebrow": "初始化、配置和更新",
    "common.loading": "加载中...",
    "common.errorTitle": "页面加载失败",
    "runtime.workspace": "工作区",
    "runtime.trace": "追踪",
    "runtime.pending": "待审批",
    "runtime.tools": "工具",
    "chat.conversation": "对话",
    "chat.agent": "Agent",
    "chat.run": "运行",
    "chat.clear": "清空",
    "chat.placeholder": "输入任务。可以用 @path 引用工作区文件。",
    "chat.files": "文件引用",
    "chat.fileHelp": "选择工作区文件并插入 @path 引用。",
    "workflow.workflows": "工作流",
    "workflow.new": "新建",
    "workflow.library": "节点库",
    "workflow.save": "保存",
    "workflow.delete": "删除",
    "workflow.settings": "节点设置",
    "workflow.empty": "请选择画布上的节点。",
    "workflow.runPreview": "运行预览",
    "workflow.run": "运行工作流",
    "workflow.dropHint": "拖拽节点到画布，或选择节点后连接到其他阶段。",
    "workflow.connect": "连接",
    "workflow.remove": "删除节点"
  }
};

let language = localStorage.getItem("goflow.language") || "en";

export function setLanguage(next) {
  language = next === "zh" ? "zh" : "en";
  localStorage.setItem("goflow.language", language);
}

export function currentLanguage() {
  return language;
}

export function t(key) {
  return dictionaries[language]?.[key] || dictionaries.en[key] || key;
}

export function applyStaticTranslations(root = document) {
  root.querySelectorAll("[data-i18n]").forEach(node => {
    node.textContent = t(node.dataset.i18n);
  });
}
