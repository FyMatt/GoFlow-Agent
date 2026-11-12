import { escapeHTML, request } from "../api.js";
import { icon, resourceKindIcon } from "../icons.js";
import { currentLanguage, localizedText } from "../i18n.js";

const resourceKinds = ["agent", "subagent", "workflow", "skill", "tool", "mcp"];
const resourceTabs = ["all", ...resourceKinds];
const resourceTaskDraftKey = "goflow.playground.resourceDraft";
const workflowToolDraftKey = "goflow.workflow.toolDraft";

const copy = {
  en: {
    title: "Resources",
    subtitle: "Agents, subagents, workflows, Skills, and tools",
    search: "Search resources",
    filters: "Filters",
    all: "All",
    allSummary: "Search and use every task resource from one place",
    capability: "Capability",
    anyCapability: "Any capability",
    health: "Health",
    anyHealth: "Any health",
    validation: "Validation",
    anyValidation: "Any validation",
    healthy: "Healthy",
    degraded: "Degraded",
    error: "Unavailable",
    disabled: "Disabled",
    unknown: "Unknown",
    valid: "Valid",
    useInTask: "Use in task",
    useInWorkflow: "Use in workflow",
    exactVersion: "Exact version",
    dependencyIssue: "{count} dependency issue",
    dependencyIssues: "{count} dependency issues",
    repairRequired: "Repair {kind} {name} before using this resource.",
    repairCheck: "Check {kind} {name}, then refresh its status.",
    add: "New",
    edit: "Edit",
    view: "View",
    remove: "Delete",
    open: "Open",
    close: "Close",
    cancel: "Cancel",
    save: "Save",
    saving: "Saving...",
    checking: "Checking...",
    saved: "Resource saved.",
    deleted: "Resource deleted.",
    empty: "No matching resources.",
    name: "Name",
    displayName: "Display name",
    purpose: "Purpose",
    instructions: "Instructions",
    whenToUse: "When to delegate",
    advanced: "Advanced settings",
    provider: "Provider",
    model: "Model",
    mode: "Mode",
    maxIterations: "Maximum steps",
    toolPolicy: "Tool approval",
    allowedKinds: "Allowed tool kinds",
    allowedTools: "Allowed tools",
    version: "Version",
    author: "Author",
    preferredAgent: "Preferred agent",
    keywords: "Activation keywords",
    command: "Command",
    arguments: "Arguments",
    workdir: "Working directory",
    environment: "Environment variable names",
    timeout: "Timeout",
    enabled: "Enabled",
    optional: "Optional at startup",
    workflowAgent: "Starter agent",
    openDesigner: "Open workflow designer",
    active: "Active",
    restart: "Restart required",
    hotReload: "Available now",
    invalid: "Needs attention",
    agents: "Agents",
    subagents: "Subagents",
    workflows: "Workflows",
    skills: "Skills",
    tools: "MCP tools",
    mcp: "MCP connections",
    agentSummary: "Primary task agents",
    subagentSummary: "Focused delegated roles",
    workflowSummary: "Runnable task flows",
    skillSummary: "Reusable SKILL.md instructions",
    toolSummary: "Individual MCP tools that can be connected between workflow agents",
    mcpSummary: "MCP server connections that provide tools",
    builtIn: "Built in",
    custom: "Custom",
    readOnly: "Built-in resources are maintained by the platform and are read-only.",
    toolCount: "{count} tools",
    toolAvailability: "Tools",
    toolCountPending: "Tools load on first use",
    mcpStepConnection: "1 Tool",
    mcpStepParameters: "2 Parameters",
    mcpStepReview: "3 Check and save",
    next: "Next",
    back: "Back",
    checkAndSave: "Check and save",
    mcpReviewHelp: "Confirm the command and secret variable names. The platform checks availability without running the command.",
    mcpCheckPassed: "Command and working directory are available.",
    runtimeEnvironment: "Runtime environment",
    permissionLevel: "Permission",
    permissionRead: "Read-only inspection",
    permissionWorkspace: "May modify the task workspace",
    permissionSystem: "System or remote operation",
    importCodex: "Import Codex",
    importClaude: "Import Claude",
  exportCodex: "Codex",
  exportClaude: "Claude",
  export: "Export",
    imported: "Import complete.",
    confirmDelete: "Delete {name}? This removes the saved resource files.",
    identifierHelp: "Lowercase letters, numbers, hyphens, or underscores",
    argsHelp: "One argument per line. Order is preserved.",
    envHelp: "Names only, one per line. Secret values stay in your environment.",
    workflowHelp: "The visual graph is edited in Workflows.",
    errorPrefix: "Unable to complete the action",
    result: "Result"
  },
  zh: {
    title: "资源",
    subtitle: "智能体、子智能体、工作流、技能与工具",
    search: "搜索资源",
    filters: "筛选",
    all: "全部",
    allSummary: "在一个页面查找并使用全部任务资源",
    capability: "能力",
    anyCapability: "全部能力",
    health: "健康状态",
    anyHealth: "全部健康状态",
    validation: "验证状态",
    anyValidation: "全部验证状态",
    healthy: "正常",
    degraded: "需关注",
    error: "不可用",
    disabled: "已停用",
    unknown: "未知",
    valid: "验证通过",
    useInTask: "用于任务",
    useInWorkflow: "用于工作流",
    exactVersion: "精确版本",
    dependencyIssue: "{count} 个依赖问题",
    dependencyIssues: "{count} 个依赖问题",
    repairRequired: "请先修复 {kind} {name}，再使用此资源。",
    repairCheck: "请检查 {kind} {name}，然后刷新状态。",
    add: "新建",
    edit: "编辑",
    view: "查看",
    remove: "删除",
    open: "打开",
    close: "关闭",
    cancel: "取消",
    save: "保存",
    saving: "正在保存...",
    checking: "正在检查...",
    saved: "资源已保存。",
    deleted: "资源已删除。",
    empty: "没有匹配的资源。",
    name: "名称",
    displayName: "显示名称",
    purpose: "用途",
    instructions: "指令",
    whenToUse: "何时委派",
    advanced: "高级设置",
    provider: "模型供应商",
    model: "模型",
    mode: "运行模式",
    maxIterations: "最大步骤数",
    toolPolicy: "工具审批",
    allowedKinds: "允许的工具类型",
    allowedTools: "允许的工具",
    version: "版本",
    author: "作者",
    preferredAgent: "首选智能体",
    keywords: "触发关键词",
    command: "命令",
    arguments: "参数",
    workdir: "工作目录",
    environment: "环境变量名称",
    timeout: "超时",
    enabled: "启用",
    optional: "启动失败时降级",
    workflowAgent: "起始智能体",
    openDesigner: "打开工作流设计器",
    active: "已生效",
    restart: "需要重启",
    hotReload: "立即可用",
    invalid: "需要处理",
    agents: "智能体",
    subagents: "子智能体",
    workflows: "工作流",
    skills: "技能",
    tools: "MCP 工具",
    mcp: "MCP 连接",
    agentSummary: "直接处理任务的主智能体",
    subagentSummary: "按需委派的专用角色",
    workflowSummary: "可直接运行的任务流程",
    skillSummary: "兼容 SKILL.md 的复用指令",
    toolSummary: "可连接在工作流智能体之间的单个 MCP 工具",
    mcpSummary: "提供工具的 MCP 服务连接",
    builtIn: "内置",
    custom: "自定义",
    readOnly: "内置资源由平台维护，仅展示运行状态，不开放技术配置。",
    toolCount: "{count} 个工具",
    toolAvailability: "工具",
    toolCountPending: "工具按需加载",
    mcpStepConnection: "1 工具信息",
    mcpStepParameters: "2 参数与环境",
    mcpStepReview: "3 检查并保存",
    next: "下一步",
    back: "上一步",
    checkAndSave: "检查并保存",
    mcpReviewHelp: "确认命令、参数和环境变量名。平台只检查可用性，不会执行该命令。",
    mcpCheckPassed: "命令和工作目录可用。",
    runtimeEnvironment: "运行环境",
    permissionLevel: "权限范围",
    permissionRead: "只读检查",
    permissionWorkspace: "允许修改任务工作区",
    permissionSystem: "系统或远程操作",
    importCodex: "导入 Codex",
    importClaude: "导入 Claude",
    exportCodex: "Codex",
    exportClaude: "Claude",
    export: "导出",
    imported: "导入完成。",
    confirmDelete: "确定删除 {name}？对应的资源文件会被移除。",
    identifierHelp: "使用小写字母、数字、连字符或下划线",
    argsHelp: "每行一个参数，顺序会原样保留。",
    envHelp: "每行一个变量名；密钥值仍保存在本机环境中。",
    workflowHelp: "流程图在“工作流”页面中编辑。",
    errorPrefix: "操作未完成",
    result: "结果"
  }
};


const state = {
  root: null,
  runtime: {},
  activeKind: "all",
  query: "",
  capability: "",
  health: "",
  validation: "",
  items: {},
  catalogItems: [],
  editing: null,
  notice: null
};

const resourceRowMarkup = new WeakMap();
const resourceRowNodes = new Map();

function tx(key, vars = {}) {
  const language = currentLanguage() === "zh" ? "zh" : "en";
  let value = copy[language][key] || copy.en[key] || key;
  for (const [name, replacement] of Object.entries(vars)) {
    value = value.replaceAll(`{${name}}`, String(replacement));
  }
  return value;
}

export async function renderCatalog(root, runtime = {}) {
  state.root = root;
  state.runtime = runtime || {};
  const loaded = await loadResources();
  state.items = loaded.items;
  state.catalogItems = loaded.catalogItems;
  const availableKeys = new Set(state.catalogItems.map(resourceCardKey));
  for (const key of resourceRowNodes.keys()) {
    if (!availableKeys.has(key)) resourceRowNodes.delete(key);
  }

  // Route entry creates the shell once. Filters and kind changes patch only
  // keyed resource rows so the stable navigation and list nodes are preserved.
  root.innerHTML = renderResourceLibrary();
  bindResourceLibrary(root);
  renderResourceList(root);
}

async function loadResources() {
  let catalog;
  try {
    catalog = await request("/api/resources");
  } catch (error) {
    state.notice = { tone: "error", text: `${tx("errorPrefix")}: ${localizedText(error.message)}` };
    return { items: Object.fromEntries(resourceKinds.map(kind => [kind, []])), catalogItems: [] };
  }
  const items = {};
  for (const kind of resourceKinds) {
    items[kind] = Array.isArray(catalog?.items)
      ? catalog.items.filter(item => String(item?.kind || "").toLowerCase() === kind)
      : [];
  }
  return {
    items,
    catalogItems: Array.isArray(catalog?.items) ? catalog.items : fallbackCatalogItems(items)
  };
}

function renderResourceLibrary() {
  return `
    <div class="resource-library" data-active-kind="${escapeHTML(state.activeKind)}">
      <header class="resource-library-head">
        <div>
          <p class="eyebrow">${escapeHTML(tx("title"))}</p>
          <h2>${escapeHTML(tx("subtitle"))}</h2>
        </div>
        <button class="primary icon-text ${["all", "workflow", "tool"].includes(state.activeKind) ? "is-invisible" : ""}" type="button" data-new-resource="${escapeHTML(state.activeKind)}">${icon("plus")}<span>${escapeHTML(tx("add"))}</span></button>
      </header>

      <div id="resourceNotice" class="resource-notice ${state.notice ? "" : "hidden"} ${escapeHTML(state.notice?.tone || "")}" role="status">
        ${escapeHTML(state.notice?.text || "")}
      </div>

      <div class="resource-library-layout">
        <nav class="resource-kind-nav" aria-label="${escapeHTML(tx("title"))}" role="tablist">
          ${resourceTabs.map(renderKindButton).join("")}
        </nav>
        <section class="resource-browser" aria-live="polite">
          <div class="resource-browser-toolbar">
            <label class="resource-search">
              <span class="sr-only">${escapeHTML(tx("search"))}</span>
              <span class="resource-search-icon" aria-hidden="true">${icon("search")}</span>
              <input id="resourceSearch" type="search" value="${escapeHTML(state.query)}" placeholder="${escapeHTML(tx("search"))}" autocomplete="off">
            </label>
            <div id="resourceImportActions" class="resource-import-actions">${renderImportActions(state.activeKind)}</div>
          </div>
          <details class="resource-filter-panel">
            <summary>${escapeHTML(tx("filters"))}</summary>
            <div class="resource-filter-bar" aria-label="${escapeHTML(tx("filters"))}">
              <label><span>${escapeHTML(tx("capability"))}</span><select id="resourceCapabilityFilter">${renderCapabilityOptions()}</select></label>
              <label><span>${escapeHTML(tx("health"))}</span><select id="resourceHealthFilter">${renderFilterOptions(["healthy", "degraded", "error", "disabled", "unknown"], state.health, "anyHealth")}</select></label>
              <label><span>${escapeHTML(tx("validation"))}</span><select id="resourceValidationFilter">${renderFilterOptions(["valid", "invalid"], state.validation, "anyValidation")}</select></label>
            </div>
          </details>
          <div class="resource-browser-heading">
            <div>
              <h3 id="resourceKindTitle">${escapeHTML(kindLabel(state.activeKind))}</h3>
              <p id="resourceKindSummary">${escapeHTML(kindSummary(state.activeKind))}</p>
            </div>
            <span id="resourceKindCount" class="badge">0</span>
          </div>
          <div id="resourceList" class="resource-list"></div>
        </section>
      </div>

      <input id="resourceImportFile" class="hidden" type="file">
      <div id="resourceDesigner" class="designer hidden" role="dialog" aria-modal="true" aria-labelledby="designerTitle">
        <div class="designer-backdrop" data-close-designer></div>
        <section class="designer-panel resource-editor-panel">
          <div class="designer-head">
            <div>
              <p class="eyebrow">${escapeHTML(tx("title"))}</p>
              <h2 id="designerTitle">${escapeHTML(tx("add"))}</h2>
            </div>
            <button class="icon-button" type="button" data-close-designer aria-label="${escapeHTML(tx("close"))}" title="${escapeHTML(tx("close"))}">
              ${icon("x")}
            </button>
          </div>
          <form id="resourceForm" class="designer-body resource-simple-form"></form>
        </section>
      </div>
    </div>`;
}

function renderKindButton(kind) {
  const active = kind === state.activeKind;
  const count = kind === "all" ? state.catalogItems.length : state.catalogItems.filter(item => item.kind === kind).length;
  return `<button type="button" role="tab" data-resource-kind="${escapeHTML(kind)}" class="${active ? "active" : ""}" aria-selected="${active}">
    ${resourceKindIcon(kind)}<span>${escapeHTML(kindLabel(kind))}</span><strong>${count}</strong>
  </button>`;
}

function bindResourceLibrary(root) {
  root.addEventListener("click", async event => {
    const kindButton = event.target.closest("[data-resource-kind]");
    if (kindButton) {
      state.activeKind = kindButton.dataset.resourceKind;
      updateActiveKind(root);
      return;
    }
    const createButton = event.target.closest("[data-new-resource]");
    if (createButton) {
      await openResourceEditor(createButton.dataset.newResource);
      return;
    }
    const editButton = event.target.closest("[data-edit-resource]");
    if (editButton) {
      await openResourceEditor(editButton.dataset.kind, editButton.dataset.editResource);
      return;
    }
    const deleteButton = event.target.closest("[data-delete-resource]");
    if (deleteButton) {
      await deleteResource(deleteButton.dataset.kind, deleteButton.dataset.deleteResource);
      return;
    }
    const useButton = event.target.closest("[data-use-resource]");
    if (useButton) {
      useResourceInTask(useButton.dataset.kind, useButton.dataset.useResource);
      return;
    }
    const workflowButton = event.target.closest("[data-open-workflow]");
    if (workflowButton) {
      localStorage.setItem("goflow.workflow.open", workflowButton.dataset.openWorkflow);
      window.location.hash = "workflows";
      return;
    }
    const importButton = event.target.closest("[data-import-resource]");
    if (importButton) openImportPicker(importButton.dataset.importResource, importButton.dataset.format);
    if (event.target.closest("[data-close-designer]")) closeResourceEditor();
  });

  root.querySelector("#resourceSearch")?.addEventListener("input", event => {
    state.query = event.target.value;
    renderResourceList(root);
  });
  root.querySelector("#resourceCapabilityFilter")?.addEventListener("change", event => {
    state.capability = event.target.value;
    renderResourceList(root);
  });
  root.querySelector("#resourceHealthFilter")?.addEventListener("change", event => {
    state.health = event.target.value;
    renderResourceList(root);
  });
  root.querySelector("#resourceValidationFilter")?.addEventListener("change", event => {
    state.validation = event.target.value;
    renderResourceList(root);
  });
  root.querySelector("#resourceImportFile")?.addEventListener("change", importSelectedResource);
}

function updateActiveKind(root) {
  const library = root.querySelector(".resource-library");
  if (library) library.dataset.activeKind = state.activeKind;
  root.querySelectorAll("[data-resource-kind]").forEach(button => {
    const active = button.dataset.resourceKind === state.activeKind;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  const create = root.querySelector("[data-new-resource]");
  if (create) {
    create.dataset.newResource = state.activeKind;
    create.classList.toggle("is-invisible", ["all", "workflow", "tool"].includes(state.activeKind));
  }
  root.querySelector("#resourceKindTitle").textContent = kindLabel(state.activeKind);
  root.querySelector("#resourceKindSummary").textContent = kindSummary(state.activeKind);
  root.querySelector("#resourceImportActions").innerHTML = renderImportActions(state.activeKind);
  const capability = root.querySelector("#resourceCapabilityFilter");
  if (capability) capability.innerHTML = renderCapabilityOptions();
  renderResourceList(root);
}

function renderResourceList(root) {
  const values = filteredResources();
  root.querySelector("#resourceKindCount").textContent = String(values.length);
  const list = root.querySelector("#resourceList");
  if (!list) return;
  patchResourceRows(list, values);
}

function patchResourceRows(list, values) {
  if (!values.length) {
    const existing = list.firstElementChild;
    if (existing?.classList.contains("resource-empty") && list.childElementCount === 1) {
      existing.textContent = tx("empty");
      return;
    }
    list.innerHTML = `<div class="resource-empty">${escapeHTML(tx("empty"))}</div>`;
    return;
  }

  const existing = new Map(
    Array.from(list.querySelectorAll(":scope > [data-resource-card]"))
      .map(node => [node.dataset.resourceCard, node])
  );
  const desired = values.map(item => {
    const key = resourceCardKey(item);
    const markup = renderResourceRow(state.activeKind, item);
    let node = existing.get(key) || resourceRowNodes.get(key);
    if (!node || resourceRowMarkup.get(node) !== markup) {
      const template = document.createElement("template");
      template.innerHTML = markup.trim();
      node = template.content.firstElementChild;
      resourceRowMarkup.set(node, markup);
      resourceRowNodes.set(key, node);
    }
    existing.delete(key);
    return node;
  });

  desired.forEach((node, index) => {
    const current = list.children[index];
    if (current !== node) list.insertBefore(node, current || null);
  });
  while (list.children.length > desired.length) {
    list.lastElementChild.remove();
  }
}

function resourceCardKey(item) {
  return `${String(item?.kind || "")}:${resourceName(item)}`;
}

function filteredResources() {
  const query = state.query.trim().toLowerCase();
  return state.catalogItems
    .filter(item => state.activeKind === "all" || item.kind === state.activeKind)
    .filter(item => !query || resourceSearchText(item).includes(query))
    .filter(item => !state.capability || (item.capabilities || []).includes(state.capability))
    .filter(item => !state.health || item.health === state.health)
    .filter(item => !state.validation || item.validation_status === state.validation)
    .sort((left, right) => left.kind.localeCompare(right.kind) || resourceName(left).localeCompare(resourceName(right)));
}

function renderResourceRow(_, item) {
  const kind = item.kind;
  const name = resourceName(item);
  const title = resourceTitle(item);
  const status = resourceStatus(kind, item);
  const editLabel = tx(kind === "workflow" ? "open" : (kind === "mcp" && item.builtin ? "view" : "edit"));
  const editAction = kind === "workflow"
    ? `<button type="button" class="icon-button" data-open-workflow="${escapeHTML(name)}" aria-label="${escapeHTML(editLabel)}" title="${escapeHTML(editLabel)}">${icon("workflow")}</button>`
    : kind === "tool"
      ? ""
      : `<button type="button" class="icon-button" data-edit-resource="${escapeHTML(name)}" data-kind="${escapeHTML(kind)}" aria-label="${escapeHTML(editLabel)}" title="${escapeHTML(editLabel)}">${icon("pencil")}</button>`;
  const dependencyIssues = (item.dependencies || []).filter(dependency => dependency.status !== "ready").length;
  const repair = firstDependencyRepair(item);
  return `<article class="resource-row" data-resource-card="${escapeHTML(kind)}:${escapeHTML(name)}">
    <button class="resource-row-main" type="button" ${kind === "workflow" ? `data-open-workflow="${escapeHTML(name)}"` : `data-edit-resource="${escapeHTML(name)}" data-kind="${escapeHTML(kind)}"`}>
      <span class="resource-row-icon" aria-hidden="true">${resourceKindIcon(kind)}</span>
      <span class="resource-row-copy">
      <span class="resource-row-title"><strong>${escapeHTML(title)}</strong>${title !== name ? `<code>${escapeHTML(name)}</code>` : ""}<span class="resource-kind-chip">${escapeHTML(kindLabel(kind))}</span></span>
      <span class="resource-row-description">${escapeHTML(resourceDescription(kind, item))}</span>
      <span class="resource-row-facts">
        ${kind === "mcp" ? `<span>${escapeHTML(tx(item.builtin ? "builtIn" : "custom"))}</span><span>${escapeHTML(mcpToolCountText(item))}</span>` : ""}
        ${(item.capabilities || []).slice(0, 2).map(value => `<span>${escapeHTML(capabilityLabel(value))}</span>`).join("")}
        ${dependencyIssues ? `<span class="warning">${escapeHTML(tx(dependencyIssues === 1 ? "dependencyIssue" : "dependencyIssues", { count: dependencyIssues }))}</span>` : ""}
      </span>
      ${repair ? `<span class="resource-row-guidance" title="${escapeHTML(repair.detail)}">${escapeHTML(repair.label)}</span>` : ""}
      </span>
    </button>
    <div class="resource-row-meta">
      <span class="resource-state ${escapeHTML(status.tone)}">${escapeHTML(status.label)}</span>
      <button class="primary resource-use-button icon-text" type="button" data-use-resource="${escapeHTML(name)}" data-kind="${escapeHTML(kind)}">${icon("square-play")}<span>${escapeHTML(tx(kind === "tool" ? "useInWorkflow" : "useInTask"))}</span></button>
      ${editAction}
      ${kind === "subagent" || kind === "mcp" ? renderExportActions(kind, name) : ""}
    </div>
  </article>`;
}

function renderImportActions(kind) {
  if (kind !== "subagent" && kind !== "mcp") return "";
  return `<button type="button" class="icon-text" data-import-resource="${escapeHTML(kind)}" data-format="codex">${icon("folder")}<span>${escapeHTML(tx("importCodex"))}</span></button>
    <button type="button" class="icon-text" data-import-resource="${escapeHTML(kind)}" data-format="claude">${icon("folder")}<span>${escapeHTML(tx("importClaude"))}</span></button>`;
}

function renderExportActions(kind, name) {
  const base = kind === "subagent" ? "/api/resources/subagents" : "/api/resources/mcp";
  return `<div class="resource-export-links" aria-label="${escapeHTML(tx("export"))}">
    <a href="${base}/${encodeURIComponent(name)}/export?format=codex" download>${escapeHTML(tx("exportCodex"))}</a>
    <a href="${base}/${encodeURIComponent(name)}/export?format=claude" download>${escapeHTML(tx("exportClaude"))}</a>
  </div>`;
}

async function openResourceEditor(kind, name = "") {
  if (!resourceKinds.includes(kind)) return;
  if (kind === "workflow") {
    if (name) localStorage.setItem("goflow.workflow.open", name);
    window.location.hash = "workflows";
    return;
  }
  let doc = null;
  if (name) {
    try {
      doc = await request(resourceDetailEndpoint(kind, name));
    } catch (error) {
      showNotice("error", `${tx("errorPrefix")}: ${localizedText(error.message)}`);
      return;
    }
  }
  const catalogItem = state.catalogItems.find(item => item.kind === kind && resourceName(item) === name) || {};
  const readOnly = kind === "mcp" && Boolean(doc?.read_only || catalogItem.builtin);
  state.editing = { kind, name, doc: doc || {}, catalogItem, readOnly };
  const designer = state.root.querySelector("#resourceDesigner");
  const form = state.root.querySelector("#resourceForm");
  state.root.querySelector("#designerTitle").textContent = `${readOnly ? tx("view") : name ? tx("edit") : tx("add")} ${kindLabel(kind)}`;
  form.innerHTML = renderResourceForm(kind, doc || {}, Boolean(name), readOnly, catalogItem);
  if (!readOnly) form.addEventListener("submit", saveResourceForm, { once: true });
  designer.classList.remove("hidden");
  document.body.classList.add("modal-open");
  form.querySelector("#resourceName")?.focus();
}

function closeResourceEditor() {
  state.root?.querySelector("#resourceDesigner")?.classList.add("hidden");
  document.body.classList.remove("modal-open");
  state.editing = null;
}

function renderResourceForm(kind, doc, editing, readOnly = false, catalogItem = {}) {
  if (kind === "mcp" && readOnly) return renderBuiltinMCPStatus(doc, catalogItem);
  const name = resourceName(doc);
  const common = `
    <input id="resourceType" type="hidden" value="${escapeHTML(kind)}">
    <div class="resource-form-grid">
      <label class="stack">
        <span>${escapeHTML(tx("name"))}</span>
        <input id="resourceName" value="${escapeHTML(name)}" ${editing ? "readonly" : ""} required pattern="[a-z0-9][a-z0-9_-]*" aria-describedby="resourceNameHelp">
        <small id="resourceNameHelp">${escapeHTML(tx("identifierHelp"))}</small>
      </label>
      ${renderPrimaryFields(kind, doc)}
    </div>
    ${renderAdvancedFields(kind, doc)}
    <div id="resourceOutput" class="resource-output" role="status"></div>
    ${renderResourceFormActions(kind, name, editing)}`;
  return common;
}

function renderResourceFormActions(kind, name, editing) {
  if (kind === "mcp") {
    return `<div class="designer-actions">
      ${editing ? `<button type="button" class="danger ghost-button" data-delete-resource="${escapeHTML(name)}" data-kind="mcp">${escapeHTML(tx("remove"))}</button>` : ""}
      <button type="button" data-close-designer>${escapeHTML(tx("cancel"))}</button>
      <button class="primary" type="submit" data-mcp-submit>${escapeHTML(tx("checkAndSave"))}</button>
    </div>`;
  }
  return `<div class="designer-actions">
    ${editing ? `<button type="button" class="danger ghost-button" data-delete-resource="${escapeHTML(name)}" data-kind="${escapeHTML(kind)}">${escapeHTML(tx("remove"))}</button>` : ""}
    <button type="button" data-close-designer>${escapeHTML(tx("cancel"))}</button>
    <button class="primary" type="submit">${escapeHTML(tx("save"))}</button>
  </div>`;
}

function renderBuiltinMCPStatus(doc, catalogItem) {
  const item = { ...catalogItem, ...doc, kind: "mcp", name: doc.name || catalogItem.name };
  const display = builtinResourceText("mcp", item);
  const status = resourceStatus("mcp", item);
  return `<div class="mcp-readonly-status">
    <div class="mcp-readonly-heading">
      <span class="resource-state ${escapeHTML(status.tone)}">${escapeHTML(status.label)}</span>
      <span class="resource-state neutral">${escapeHTML(tx("builtIn"))}</span>
    </div>
    <h3>${escapeHTML(display?.[0] || item.name || "MCP")}</h3>
    <p>${escapeHTML(display?.[1] || resourceDescription("mcp", item))}</p>
    <dl class="mcp-review-list">
      <div><dt>${escapeHTML(tx("health"))}</dt><dd>${escapeHTML(status.label)}</dd></div>
      <div><dt>${escapeHTML(tx("toolAvailability"))}</dt><dd>${escapeHTML(mcpToolCountText(item))}</dd></div>
      <div><dt>${escapeHTML(tx("permissionLevel"))}</dt><dd>${escapeHTML(mcpPermissionLevel(item))}</dd></div>
    </dl>
    <p class="resource-form-note">${escapeHTML(tx("readOnly"))}</p>
  </div>
  <div class="designer-actions"><button type="button" data-close-designer>${escapeHTML(tx("close"))}</button></div>`;
}

function mcpToolCountText(item = {}) {
  const count = Number(item.tool_count || 0);
  if (count > 0) return tx("toolCount", { count });
  const detail = String(item.health_detail || "").toLowerCase();
  if (detail.startsWith("idle") || item.health === "healthy") return tx("toolCountPending");
  return tx("toolCount", { count: 0 });
}

function mcpPermissionLevel(item = {}) {
  const name = String(item.name || item.id || "").toLowerCase();
  if (["host_tools", "network_tools", "web_tools", "ssh_tools"].includes(name)) return tx("permissionSystem");
  const capabilities = new Set((item.capabilities || []).map(value => String(value || "").toLowerCase()));
  if (["write", "exec", "document", "office"].some(value => capabilities.has(value)) || ["file_tools", "command_tools", "office_tools"].includes(name)) {
    return tx("permissionWorkspace");
  }
  return tx("permissionRead");
}

function renderPrimaryFields(kind, doc) {
  if (kind === "agent" || kind === "subagent") {
    return `
      <label class="stack resource-form-wide"><span>${escapeHTML(kind === "subagent" ? tx("whenToUse") : tx("purpose"))}</span><textarea id="resourceDescription" rows="3" required>${escapeHTML(doc.description || "")}</textarea></label>
      <label class="stack resource-form-wide"><span>${escapeHTML(tx("instructions"))}</span><textarea id="agentSystemPrompt" rows="10" required>${escapeHTML(doc.system_prompt || "")}</textarea></label>`;
  }
  if (kind === "skill") {
    return `
      <label class="stack resource-form-wide"><span>${escapeHTML(tx("purpose"))}</span><textarea id="resourceDescription" rows="3" required>${escapeHTML(doc.description || "")}</textarea></label>
      <label class="stack resource-form-wide"><span>${escapeHTML(tx("instructions"))}</span><textarea id="skillInstructions" rows="12" required>${escapeHTML(doc.instructions || "")}</textarea></label>`;
  }
  if (kind === "mcp") {
    return `<div class="resource-form-wide resource-connection-form">
      <label class="stack"><span>${escapeHTML(tx("command"))}</span><input id="mcpCommand" value="${escapeHTML(doc.command || "")}" required></label>
      <label class="check"><input id="mcpEnabled" type="checkbox" ${doc.enabled !== false ? "checked" : ""}><span>${escapeHTML(tx("enabled"))}</span></label>
      <details class="resource-advanced-section"><summary>${escapeHTML(tx("advanced"))}</summary><div class="resource-form-grid">
        <label class="stack"><span>${escapeHTML(tx("arguments"))}</span><textarea id="mcpArgs" rows="5" aria-describedby="mcpArgsHelp">${escapeHTML((doc.args || []).join("\n"))}</textarea><small id="mcpArgsHelp">${escapeHTML(tx("argsHelp"))}</small></label>
        <label class="stack"><span>${escapeHTML(tx("environment"))}</span><textarea id="mcpEnv" rows="4" aria-describedby="mcpEnvHelp">${escapeHTML((doc.env_allowlist || []).join("\n"))}</textarea><small id="mcpEnvHelp">${escapeHTML(tx("envHelp"))}</small></label>
        <label class="stack"><span>${escapeHTML(tx("workdir"))}</span><input id="mcpWorkdir" value="${escapeHTML(doc.workdir || ".")}"></label>
      </div></details>
    </div>`;
  }
  return `
    <label class="stack resource-form-wide"><span>${escapeHTML(tx("purpose"))}</span><textarea id="resourceDescription" rows="4" required>${escapeHTML(doc.description || "")}</textarea></label>
    <p class="resource-form-note resource-form-wide">${escapeHTML(tx("workflowHelp"))}</p>`;
}

function renderAdvancedFields(kind, doc) {
  return "";
}

async function saveResourceForm(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector('[type="submit"]');
  submit.disabled = true;
  submit.textContent = tx("saving");
  try {
    const draft = collectResourceDraft(form, state.editing);
    await validateResourceDraft(state.editing.kind, draft.name, draft);
    if (state.editing.kind === "mcp") {
      submit.textContent = tx("checking");
      await request(`/api/resources/mcp/${encodeURIComponent(draft.name)}/test`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(draft)
      });
    }
    await request(resourceDetailEndpoint(state.editing.kind, draft.name), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(draft)
    });
    state.notice = { tone: "success", text: tx("saved") };
    closeResourceEditor();
    await renderCatalog(state.root, state.runtime);
  } catch (error) {
    const output = form.querySelector("#resourceOutput");
    output.textContent = `${tx("errorPrefix")}: ${localizedText(error.message)}`;
    output.classList.add("error");
    submit.disabled = false;
    submit.textContent = tx(state.editing.kind === "mcp" ? "checkAndSave" : "save");
    form.addEventListener("submit", saveResourceForm, { once: true });
  }
}

function collectResourceDraft(form, editing) {
  const kind = editing.kind;
  const name = normalizeName(form.querySelector("#resourceName").value);
  const current = { ...(editing.doc || {}) };
  if (kind === "agent" || kind === "subagent") {
    return {
      ...current,
      id: name,
      kind,
      name: current.name || humanize(name),
      description: value(form, "resourceDescription"),
      system_prompt: value(form, "agentSystemPrompt"),
      provider: current.provider || defaultProvider(),
      model: current.model || "",
      mode: current.mode || (kind === "subagent" ? "plan" : "chat"),
      max_iterations: current.max_iterations || 8,
      tool_policy: current.tool_policy || "confirm",
      allowed_tool_kinds: Array.isArray(current.allowed_tool_kinds) && current.allowed_tool_kinds.length
        ? current.allowed_tool_kinds
        : ["read", "write", "exec", "network", "unknown"],
      allowed_tools: Array.isArray(current.allowed_tools) ? current.allowed_tools : []
    };
  }
  if (kind === "skill") {
    return {
      ...current,
      name,
      description: value(form, "resourceDescription"),
      instructions: value(form, "skillInstructions"),
      version: current.version || "1.0.0",
      author: current.author || "GoFlow Studio",
      preferred_agent: current.preferred_agent || "chat",
      activation: { ...(current.activation || {}), keywords: deriveSkillKeywords(name, value(form, "resourceDescription"), value(form, "skillInstructions")) }
    };
  }
  if (kind === "mcp") {
    return {
      ...current,
      name,
      transport: "stdio",
      command: value(form, "mcpCommand"),
      args: splitLines(value(form, "mcpArgs")),
      enabled: Boolean(form.querySelector("#mcpEnabled")?.checked),
      optional: Boolean(current.optional),
      workdir: value(form, "mcpWorkdir") || current.workdir || ".",
      timeout: current.timeout || "30s",
      env_allowlist: splitLines(value(form, "mcpEnv"))
    };
  }
  const stages = Array.isArray(current.stages) && current.stages.length
    ? current.stages
    : [{ name: "task", node_type: "agent", agent: "chat" }];
  return { ...current, name, description: value(form, "resourceDescription"), stages };
}

function deriveSkillKeywords(name, description, instructions) {
  const values = [name, ...String(name || "").split(/[-_\s]+/u)];
  const descriptionParts = String(description || "").split(/[，。；、,.;:：!?！？\n]+/u);
  values.push(...descriptionParts.slice(0, 6));
  const headings = String(instructions || "")
    .split(/\r?\n/u)
    .map(line => line.replace(/^#{1,6}\s*/u, "").trim())
    .filter((line, index) => index < 20 && line.length >= 2 && line.length <= 32);
  values.push(...headings.slice(0, 5));
  const seen = new Set();
  return values
    .map(item => String(item || "").trim())
    .filter(item => item.length >= 2 && item.length <= 48)
    .filter(item => {
      const key = item.toLocaleLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .slice(0, 12);
}

async function validateResourceDraft(kind, name, draft) {
  let endpoint = "";
  if (kind === "agent") endpoint = `/api/resources/agents/${encodeURIComponent(name)}/validate`;
  if (kind === "subagent") endpoint = `/api/resources/subagents/${encodeURIComponent(name)}/validate`;
  if (kind === "skill") endpoint = `/api/resources/skills/${encodeURIComponent(name)}/validate`;
  if (kind === "workflow") endpoint = `/api/workflow-graphs/${encodeURIComponent(name)}/validate`;
  if (!endpoint) return;
  const result = await request(endpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(draft)
  });
  if (result?.valid === false) {
    const issues = result.issues || result.errors || [];
    throw new Error(issues.map(item => item.message || item.error || String(item)).filter(Boolean).join("; ") || tx("invalid"));
  }
}

async function deleteResource(kind, name) {
  if (!window.confirm(tx("confirmDelete", { name }))) return;
  try {
    await request(resourceDetailEndpoint(kind, name), { method: "DELETE" });
    closeResourceEditor();
    state.notice = { tone: "success", text: tx("deleted") };
    await renderCatalog(state.root, state.runtime);
  } catch (error) {
    showNotice("error", `${tx("errorPrefix")}: ${localizedText(error.message)}`);
  }
}

function openImportPicker(kind, format) {
  const input = state.root.querySelector("#resourceImportFile");
  input.value = "";
  input.dataset.kind = kind;
  input.dataset.format = format;
  input.accept = format === "codex" ? ".toml,text/plain" : kind === "subagent" ? ".md,text/markdown" : ".json,application/json";
  input.click();
}

async function importSelectedResource(event) {
  const input = event.currentTarget;
  const file = input.files?.[0];
  if (!file) return;
  const kind = input.dataset.kind;
  const format = input.dataset.format;
  const endpoint = kind === "subagent" ? "/api/resources/subagents/import" : "/api/resources/mcp/import";
  try {
    await request(`${endpoint}?format=${encodeURIComponent(format)}`, { method: "POST", body: await file.text() });
    state.notice = { tone: "success", text: tx("imported") };
    await renderCatalog(state.root, state.runtime);
  } catch (error) {
    showNotice("error", `${tx("errorPrefix")}: ${localizedText(error.message)}`);
  }
}

function showNotice(tone, text) {
  state.notice = { tone, text };
  const notice = state.root?.querySelector("#resourceNotice");
  if (!notice) return;
  notice.className = `resource-notice ${tone}`;
  notice.textContent = text;
}

function resourceDetailEndpoint(kind, name) {
  const encoded = encodeURIComponent(name);
  if (kind === "workflow") return `/api/workflow-graphs/${encoded}`;
  return `/api/resources/${kind === "subagent" ? "subagents" : kind === "skill" ? "skills" : kind === "agent" ? "agents" : "mcp"}/${encoded}`;
}

function resourceName(item = {}) {
  return String(item.id || item.name || "").trim();
}

const builtinResourceDisplay = {
  "agent:chat": ["通用对话智能体", "处理日常需求，并在需要时转交给规划、实现或审计流程。"],
  "subagent:auditor": ["质量审计智能体", "检查已完成工作中的回归、风险和后续事项。"],
  "subagent:binary-analyst": ["二进制分析智能体", "在不执行未知样本的前提下进行静态二进制与逆向初筛。"],
  "subagent:documentation-specialist": ["办公文档智能体", "从零制作、基于模板生成、按用户意图精确修改并校验 Word、Excel、PPT 和 PDF。"],
  "subagent:fixer": ["修复智能体", "依据已批准的计划进行最小范围代码修改。"],
  "subagent:framework-extension-architect": ["框架扩展架构师", "设计相互关联的智能体、技能、工具和工作流扩展。"],
  "subagent:operations-specialist": ["运维智能体", "编写运行手册、部署与回滚方案、故障检查清单和交接说明。"],
  "subagent:planner": ["规划智能体", "在改动前产出边界清晰的实施计划。"],
  "subagent:quality-engineer": ["质量验证智能体", "执行聚焦测试和项目级验证，保留失败证据并报告覆盖缺口。"],
  "agent:research-specialist": ["互联网资料研究智能体", "搜索、筛选和整理互联网资料，交付带来源映射的研究报告与结构化数据。"],
  "subagent:security-researcher": ["安全研究智能体", "在授权范围内收集证据、分析可利用性并给出防护建议。"],
  "subagent:software-engineer": ["软件工程智能体", "实现范围明确的软件改动，并完成测试、验证和交接。"],
  "subagent:source-verifier": ["来源核验智能体", "独立核验研究结论的来源对应、时效、可信度、冲突和证据缺口。"],
  "subagent:support-specialist": ["支持智能体", "分诊问题、收集上下文、起草回复并识别升级路径。"],
  "subagent:web-security-researcher": ["Web 安全智能体", "收集页面和前端资源，审查攻击面并输出有证据的风险结论。"],
  "skill:authorized-red-team-validation": ["授权红队验证", "在授权边界内执行非破坏性红队验证，并提供修复交接。"],
  "skill:authorized-pentest-helper": ["授权渗透辅助", "仅在明确授权范围内实现小型渗透辅助工具，并完成测试与审计。"],
  "skill:binary-vulnerability-research": ["二进制漏洞研究", "分析二进制和逆向产物中的内存安全、协议和攻击面风险信号。"],
  "skill:code-audit": ["代码审计", "审查源代码中的安全风险、不安全模式和工程质量问题。"],
  "skill:code-writing": ["代码实现", "在工作区内完成聚焦的实现、扩展、重构或修复，并验证结果。"],
  "skill:controlled-operations": ["受控运维操作", "在明确授权、审批和回滚约束下执行网络与主机运维操作。"],
  "skill:crypto-learning": ["密码学学习", "实现常见加密解密学习网站或实验室，包含可运行的编码、解码流程、样式和测试。"],
  "skill:execution-plan": ["实施计划", "生成可由后续智能体安全高效执行的具体实施计划。"],
  "skill:internet-research": ["互联网资料研究", "搜索和整理互联网资料，生成结论可追溯且冲突可见的研究产物。"],
  "skill:local-host-diagnostics": ["本机诊断", "在不执行任意命令或修改系统状态的前提下收集本机只读证据。"],
  "skill:managed-local-config": ["受管本地配置", "在明确审批下检查、修改、验证和恢复白名单 JSON 配置项。"],
  "skill:office-document-production": ["办公文档制作", "从零制作、基于模板生成或精确修改办公文档，并交付真实文件、PDF 和预览证据。"],
  "skill:ops-study": ["运维学习", "整理可运行的运维学习资料、检查清单或小型运维辅助工具，并保留验证证据。"],
  "skill:remote-host-diagnostics": ["远程主机诊断", "通过受控 SSH 连接收集远程主机只读诊断证据。"],
  "skill:source-verification": ["来源核验", "检查研究结论的来源映射、可信度、时效、独立性和冲突状态。"],
  "skill:reverse-engineering": ["逆向工程", "分析二进制、反编译结果或可疑代码路径，并输出结构化评估。"],
  "skill:vulnerability-research": ["漏洞研究", "调查代码和依赖中的漏洞模式、可利用性与修复策略。"],
  "skill:web-vulnerability-research": ["Web 漏洞研究", "获取目标页面及前端资源，评估 Web 漏洞信号和攻击面。"],
  "mcp:command_tools": ["命令工具", "在审批和策略约束下执行工作区命令并返回结构化结果。"],
  "mcp:file_tools": ["文件工具", "高效读取、搜索和修改工作区文件，并保留操作证据。"],
  "mcp:host_tools": ["本机诊断工具", "收集本机只读诊断信息，并在明确授权下处理受管配置。"],
  "mcp:network_tools": ["网络工具", "生成网络设备发现、检查和变更计划，支持受控执行。"],
  "mcp:office_tools": ["办公文档工具", "创建、套用模板、精确修改、转换和校验 Word、Excel、PPT 与 PDF。"],
  "mcp:python_notes": ["Python 笔记工具", "执行受限 Python 辅助操作并记录结果。"],
  "mcp:skill_runner": ["技能运行器", "运行内置技能定义的受控辅助脚本。"],
  "mcp:ssh_tools": ["SSH 远程工具", "通过受控 SSH 会话进行远程诊断和经批准的运维操作。"],
  "mcp:web_tools": ["网页工具", "获取网页及其前端资源，用于研究和安全审查。"],
  "workflow:agent-framework-extension": ["智能体框架扩展", "设计并生成相互关联的智能体、技能、工具、工作流和团队扩展。"],
  "workflow:authorized-red-team-validation": ["授权红队验证", "在明确范围和审批下执行非破坏性安全验证，并完成修复交接。"],
  "workflow:binary-triage": ["二进制风险初筛", "收集静态分析证据、判定风险等级并给出后续步骤。"],
  "workflow:complex-project-delivery": ["复杂项目交付", "面向复杂软件任务的长期交付流程：确认计划、迭代实现、验证、审计并输出报告。"],
  "workflow:customer-support-triage": ["用户问题分诊", "分类用户问题、补齐上下文、起草答复并审查内容质量。"],
  "workflow:docs-review-publish": ["办公文档制作与交付", "从需求整理到真实文件制作、格式校验、失败修正和最终交付。"],
  "workflow:multi-domain-intake-router": ["多领域任务分流", "为复杂任务拆解领域工作项，并行执行后汇总和质量审查。"],
  "workflow:operations-runbook": ["受控运维执行", "完成基线采集、变更计划、审批、受控执行、后检和失败恢复。"],
  "workflow:parallel-research-review": ["互联网资料研究与来源核验", "搜索并快照互联网来源，独立核验结论、冲突和时效，再交付多格式产物。"],
  "workflow:plan-fix-audit": ["规划、修复与审计", "先规划，再实施已批准改动，完成验证、质量门禁和审计交付。"],
  "workflow:plan-implement-audit": ["规划、实现与审计", "澄清任务，规划批准后的修改，再实现、验证、审计并输出最终报告。"],
  "workflow:repository-readiness-review": ["仓库交付就绪审查", "并行执行构建、测试和配置审查，汇总仓库交付证据。"],
  "workflow:security-audit-evidence-gate": ["安全审计证据门禁", "收集安全证据、审查发现并通过质量门禁后输出报告。"],
  "workflow:security-fix-plan": ["安全修复计划", "基于现有证据生成最小补丁、回归、兼容和回滚方案。"],
  "workflow:skill-chain": ["技能链协作", "将请求交给最合适的专业技能路径，再汇总为审阅后的结果。"],
  "workflow:software-quality-gate": ["软件质量门禁", "完成规划、实现、验证和审计，并依据证据决定是否交付。"],
  "workflow:software-team-review-gate": ["软件团队复核门禁", "运行软件任务团队，依据复核结果决定交付或返工。"],
  "workflow:task-decomposition-plan": ["复杂任务拆解", "在执行前将复杂需求拆解为可评审的工作流计划，不直接修改项目文件。"],
  "workflow:web-research-risk": ["网页研究与风险审计", "收集授权网页证据，完成风险审计并生成报告。"]
};


function builtinResourceText(kind, item = {}) {
  if (currentLanguage() !== "zh") return null;
  const name = resourceName(item);
  const exact = builtinResourceDisplay[`${kind}:${name}`];
  if (exact) return exact;
  if (kind === "agent") return builtinResourceDisplay[`subagent:${name}`] || null;
  if (kind === "subagent") return builtinResourceDisplay[`agent:${name}`] || null;
  return null;
}

function resourceTitle(item = {}) {
  const display = builtinResourceText(item.kind, item);
  if (display) return display[0];
  if (item.kind === "tool") return toolDisplayName(item.name || item.title || item.id);
  return localizedText(String(item.title || item.display_name || item.name || item.id || "").trim()) || resourceName(item);
}

function toolDisplayName(value) {
  const raw = String(value || "").trim();
  if (!raw || currentLanguage() !== "zh") return raw;
  const name = raw.toLowerCase().split("/").pop();
  const labels = {
    apply_patch: "应用精确补丁",
    binary_entropy_map: "二进制熵图",
    binary_extract_window: "提取二进制片段",
    binary_file_info: "二进制文件信息",
    binary_format_summary: "二进制格式摘要",
    binary_strings: "提取二进制字符串",
    binary_symbol_hints: "二进制符号提示",
    browser_probe_points: "浏览器探测面",
    browser_snapshot: "浏览器快照",
    compile_research_evidence: "编译研究证据",
    create_docx: "创建 Word 文档",
    create_pdf: "创建 PDF 文档",
    create_pptx: "创建演示文稿",
    create_xlsx: "创建 Excel 工作簿",
    delete_file: "删除文件",
    device_batch_command_plan: "批量设备命令计划",
    device_command_plan: "设备命令计划",
    device_config_dry_run: "设备配置预演",
    device_discovery_plan: "设备发现计划",
    device_restconf_live_apply: "应用设备 RESTCONF 变更",
    disk_usage: "磁盘使用情况",
    export_office_pdf: "导出办公文档 PDF",
    fetch_page_assets: "获取页面资源",
    fetch_url: "获取网页",
    file_info: "文件信息",
    fill_office_template: "基于模板生成文档",
    hex_preview: "十六进制预览",
    host_diagnostic_snapshot: "主机诊断快照",
    host_facts: "主机信息",
    inspect_managed_json: "检查受管 JSON 配置",
    inspect_office_document: "检查办公文档",
    json_query: "查询 JSON 数据",
    list_dir: "列出目录",
    list_ports: "列出端口",
    list_processes: "列出进程",
    list_tree: "列出文件树",
    markdown_link_check: "检查 Markdown 链接",
    merge_csv_into_xlsx: "合并 CSV 到 Excel",
    mkdir: "创建目录",
    move_path: "移动路径",
    office_capabilities: "检测办公套件能力",
    plan_windows_json_change: "规划 Windows JSON 变更",
    python_ast_summary: "分析 Python 结构",
    read_file: "读取文件",
    read_file_range: "分段读取文件",
    read_log_tail: "读取日志尾部",
    read_note: "读取笔记",
    read_xlsx: "读取 Excel 工作簿",
    redaction_check: "检查敏感信息",
    render_office_preview: "生成文档预览",
    replace_office_text: "修改办公文档文本",
    replace_text: "替换文本",
    repository_map: "生成仓库结构图",
    research_snapshot: "批量研究快照",
    restore_file_change: "恢复文件修改",
    restore_managed_json_change: "恢复受管 JSON 配置",
    run_command: "运行命令",
    run_script: "运行技能脚本",
    search_files: "搜索文件",
    service_status: "服务状态",
    set_managed_json_value: "修改受管 JSON 配置",
    ssh_host_key_scan: "扫描 SSH 主机指纹",
    ssh_run_command: "运行 SSH 白名单命令",
    support_case_summary: "汇总支持案例",
    update_xlsx_cells: "更新 Excel 单元格",
    validate_office_document: "校验办公文档质量",
    web_search: "搜索互联网",
    write_file: "写入文件",
    write_note: "写入笔记"
  };
  return labels[name] || raw;
}

function resourceDescription(kind, item = {}) {
  const display = builtinResourceText(kind, item);
  if (display) return display[1];
  if (kind === "mcp") return [item.command, ...(item.args || [])].filter(Boolean).join(" ");
  if (kind === "workflow") return localizedText(item.description) || (currentLanguage() === "zh" ? `${item.stages || 0} 个阶段` : `${item.stages || 0} stages`);
  return localizedText(item.description || "");
}

function resourceSearchText(item) {
  return [
    item.kind,
    resourceName(item),
    resourceTitle(item),
    item.description,
    item.command,
    item.health,
    item.validation_status,
    item.version,
    item.declared_version,
    ...(item.args || []),
    ...(item.capabilities || []),
    ...(item.dependencies || []).flatMap(dependency => [dependency.kind, dependency.name, dependency.status])
  ].filter(Boolean).join(" ").toLowerCase();
}

function resourceStatus(kind, item = {}) {
  if (item.validation_status === "invalid" || item.valid === false || item.error) return { label: tx("invalid"), tone: "error" };
  if (item.health === "error") return { label: tx("error"), tone: "error" };
  if (item.health === "degraded") return { label: tx("degraded"), tone: "warning" };
  if (item.health === "disabled") return { label: tx("disabled"), tone: "neutral" };
  if (item.health === "unknown") return { label: tx("unknown"), tone: "neutral" };
  if (item.health === "healthy") return { label: tx("healthy"), tone: "success" };
  if (item.restart_required || item.apply_state === "restart_required") return { label: tx("restart"), tone: "warning" };
  if (kind === "skill") return { label: tx("hotReload"), tone: "success" };
  return { label: tx("active"), tone: "neutral" };
}

function kindLabel(kind) {
  return tx({ all: "all", agent: "agents", subagent: "subagents", workflow: "workflows", skill: "skills", tool: "tools", mcp: "mcp" }[kind] || kind);
}

function kindSummary(kind) {
  return tx({ all: "allSummary", agent: "agentSummary", subagent: "subagentSummary", workflow: "workflowSummary", skill: "skillSummary", tool: "toolSummary", mcp: "mcpSummary" }[kind] || kind);
}

function capabilityLabel(value) {
  if (currentLanguage() !== "zh") return value;
  const labels = {
    agent: "智能体",
    subagent: "子智能体",
    workflow: "工作流",
    skill: "技能",
    mcp: "MCP",
    multi_stage: "多阶段",
    start: "开始",
    end: "结束",
    input_gate: "输入确认",
    tool: "工具调用",
    condition: "条件分支",
    parallel: "并行",
    join: "汇合",
    checkpoint: "人工确认",
    quality_gate: "质量门禁",
    team: "智能体团队",
    repeat: "重复执行",
    sub_workflow: "子工作流",
    chat: "对话",
    plan: "规划",
    fix: "实现",
    audit: "审计",
    read: "读取",
    write: "写入",
    exec: "执行命令",
    network: "网络访问",
    filesystem: "文件系统",
    browser: "网页访问",
    search: "资料搜索",
    security: "安全分析",
    operations: "网络运维",
    documentation: "文档处理",
    code: "软件开发",
    validation: "质量验证",
    approval: "审批",
    change_evidence: "变更证据",
    changes: "文件变更",
    findings: "问题发现",
    office_deliverable: "办公文档产物",
    report: "报告",
    "security-findings": "安全发现",
    stdio: "标准输入输出",
    tool_connection: "工具连接",
    artifacts: "产物",
    evidence: "证据",
    quality: "质量控制",
    remediation: "修复"
  };
  return labels[String(value || "").toLowerCase()] || value;
}

function renderCapabilityOptions() {
  const visible = state.activeKind === "all"
    ? state.catalogItems
    : state.catalogItems.filter(item => item.kind === state.activeKind);
  const values = [...new Set(visible.flatMap(item => item.capabilities || []))].sort();
  if (state.capability && !values.includes(state.capability)) state.capability = "";
  return renderFilterOptions(values, state.capability, "anyCapability", capabilityLabel);
}

function renderFilterOptions(values, selected, emptyKey, label = value => tx(value)) {
  return `<option value="">${escapeHTML(tx(emptyKey))}</option>${values.map(value => `<option value="${escapeHTML(value)}" ${value === selected ? "selected" : ""}>${escapeHTML(label(value))}</option>`).join("")}`;
}

function resourceVersionLabel(item = {}) {
  const exact = String(item.version || "").replace(/^sha256:/, "");
  const short = exact ? exact.slice(0, 8) : "-";
  return item.declared_version ? `${item.declared_version} · ${short}` : short;
}

function firstDependencyRepair(item = {}) {
  const dependency = (item.dependencies || []).find(candidate => candidate.status !== "ready");
  if (!dependency) return null;
  const required = dependency.required && ["missing", "error", "disabled"].includes(dependency.status);
  const issue = (item.issues || []).find(candidate => String(candidate.field || "").endsWith(`.${dependency.kind}.${dependency.name}`));
  return {
    label: tx(required ? "repairRequired" : "repairCheck", { kind: kindLabel(dependency.kind), name: dependency.name }),
    detail: [issue?.message, issue?.recommendation, dependency.detail].filter(Boolean).map(localizedText).join(" ")
  };
}

function useResourceInTask(kind, name) {
  const item = state.catalogItems.find(candidate => candidate.kind === kind && candidate.name === name);
  if (!item) return;
  const target = item.use_in_task || { route: "#playground", kind, name, version: item.version };
  if (kind === "tool") {
    localStorage.setItem(workflowToolDraftKey, JSON.stringify({
      name: target.name || name,
      version: target.version || item.version,
      source: "resources"
    }));
    window.location.hash = "workflows";
    return;
  }
  localStorage.setItem(resourceTaskDraftKey, JSON.stringify({
    kind: target.kind || kind,
    name: target.name || name,
    version: target.version || item.version,
    source: "resources"
  }));
  window.location.hash = String(target.route || "#playground").replace(/^#/, "");
}

function fallbackCatalogItems(items) {
  return resourceKinds.flatMap(kind => (items[kind] || []).map(item => ({
    ...item,
    kind,
    name: resourceName(item),
    title: resourceTitle(item),
    health: item.valid === false ? "error" : "unknown",
    validation_status: item.valid === false ? "invalid" : "valid",
    valid: item.valid !== false,
    version: item.version || "unavailable",
    capabilities: [kind],
    dependencies: [],
    use_in_task: { route: "#playground", kind, name: resourceName(item), version: item.version || "unavailable" }
  })));
}

function defaultProvider() {
  return state.items.agent?.find(item => item.provider)?.provider || "primary";
}

function agentOptions(selected) {
  const agents = state.items.agent || [];
  const names = agents.map(resourceName).filter(Boolean);
  if (!names.includes(selected)) names.unshift(selected || "chat");
  return options([...new Set(names)], selected || "chat");
}

function options(values, selected) {
  return values.map(item => `<option value="${escapeHTML(item)}" ${item === selected ? "selected" : ""}>${escapeHTML(item)}</option>`).join("");
}

function value(form, id) {
  return form.querySelector(`#${id}`)?.value.trim() || "";
}

function numberValue(form, id, fallback) {
  const parsed = Number(value(form, id));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function splitValues(input) {
  return String(input || "").split(/[\n,]/).map(item => item.trim()).filter(Boolean);
}

function splitLines(input) {
  return String(input || "").split(/\r?\n/).map(item => item.trim()).filter(Boolean);
}

function normalizeName(input) {
  return String(input || "").trim().toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "");
}

function humanize(input) {
  return String(input || "").replaceAll(/[-_]+/g, " ").replace(/\b\w/g, letter => letter.toUpperCase());
}
