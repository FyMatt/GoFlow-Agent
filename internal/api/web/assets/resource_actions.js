import { discoveryMetaSupported, escapeHTML, request } from "./api.js";
import { currentLanguage, localizedText, t } from "./i18n.js";

let cachedCapabilities = null;
let cachedCapabilitiesLoadedAt = 0;
const resourceCapabilityCacheTTLMS = 30000;

const kindAliases = {
  agent: "agent",
  provider: "provider",
  tool: "tool",
  skill: "skill",
  workflow: "workflow",
  "workflow-template": "workflow_template",
  workflow_template: "workflow_template",
  "workflow-schema": "workflow_schema",
  workflow_schema: "workflow_schema",
  "policy-rule": "policy_rule",
  policy_rule: "policy_rule",
  "team-template": "team_template",
  team_template: "team_template",
  kit: "kit",
  "node-metadata": "workflow_node_metadata",
  workflow_node_metadata: "workflow_node_metadata",
  "expression-helper": "expression_helper_metadata",
  expression_helper: "expression_helper_metadata",
  expression_helper_metadata: "expression_helper_metadata"
};

export async function loadResourceCapabilities(options = {}) {
  const force = options === true || options.force === true;
  if (!force && cachedCapabilities && Date.now() - cachedCapabilitiesLoadedAt < resourceCapabilityCacheTTLMS) return cachedCapabilities;
  const [discoveryCapabilities, catalogCapabilities] = await Promise.all([
    loadDiscoveryResourceCapabilities(),
    loadResourceCatalogCapabilities()
  ]);
  cachedCapabilities = mergeResourceCapabilities(discoveryCapabilities, catalogCapabilities);
  cachedCapabilitiesLoadedAt = Date.now();
  return cachedCapabilities;
}

export function invalidateResourceCapabilities() {
  cachedCapabilities = null;
  cachedCapabilitiesLoadedAt = 0;
}

if (typeof window !== "undefined") {
  window.addEventListener("goflow:resources-changed", invalidateResourceCapabilities);
}

export function resourceCapability(capabilities, kind) {
  const normalized = canonicalResourceKind(kind);
  return (Array.isArray(capabilities) ? capabilities : []).find(item => canonicalResourceKind(item?.kind) === normalized) || null;
}

export function resourceAction(capabilities, kind, actionName) {
  const capability = resourceCapability(capabilities, kind);
  const normalizedName = canonicalActionName(actionName);
  return (capability?.actions || []).find(action => canonicalActionName(action?.name) === normalizedName) || null;
}

export function resourceActionMethod(action, fallback = "POST") {
  const method = String(action?.method || fallback || "POST").trim().toUpperCase();
  return method || "POST";
}

export function resourceActionPath(action, resourceName = "", options = {}) {
  const raw = String(action?.path || "").trim();
  if (!raw) return "";
  const format = String(options.format || "yaml").trim() || "yaml";
  const encodedName = encodeURIComponent(String(resourceName || "").trim());
  return raw
    .replaceAll("{name}", encodedName)
    .replaceAll("{id}", encodedName)
    .replaceAll("{type}", encodedName)
    .replace(/format=json\|yaml/g, `format=${encodeURIComponent(format)}`)
    .replace(/format=yaml\|json/g, `format=${encodeURIComponent(format)}`)
    .replace(/format=json\|md/g, `format=${encodeURIComponent(options.markdown ? "md" : "json")}`);
}

export function resourceActionLabel(action, fallback = "", kind = "") {
  const actionName = canonicalActionName(action?.name);
  const kindKey = kind ? `resource.action.${canonicalResourceKind(kind)}.${actionName}` : "";
  if (kindKey) {
    const kindTranslated = t(kindKey);
    if (kindTranslated !== kindKey) return kindTranslated;
  }
  const key = `resource.action.${actionName}`;
  const translated = t(key);
  if (translated !== key) return translated;
  return resourceActionFallbackLabel(action, fallback, actionName);
}

export function resourceActionDescription(action, kind = "") {
  const actionName = canonicalActionName(action?.name);
  const kindKey = kind ? `resource.actionDescription.${canonicalResourceKind(kind)}.${actionName}` : "";
  if (kindKey) {
    const kindTranslated = t(kindKey);
    if (kindTranslated !== kindKey) return kindTranslated;
  }
  const key = `resource.actionDescription.${actionName}`;
  const translated = t(key);
  if (translated !== key) return translated;
  return resourceActionFallbackDescription(action, actionName);
}

export function renderResourceActionButton(capabilities, kind, actionName, resourceName = "", options = {}) {
  const capability = resourceCapability(capabilities, kind);
  const action = resourceAction(capabilities, kind, actionName);
  if (!action) return capability ? "" : options.fallback || "";
  const requiresSaved = !!action.requires_saved_resource;
  const needsName = resourceActionNeedsName(action);
  const disabled = (requiresSaved || needsName) && !String(resourceName || "").trim();
  const classes = [options.className || "", action.destructive ? "danger" : "", options.primary ? "primary" : ""].filter(Boolean).join(" ");
  const attrs = Object.entries(options.attrs || {})
    .filter(([, value]) => value !== false && value !== null && value !== undefined)
    .map(([name, value]) => value === true ? escapeHTML(name) : `${escapeHTML(name)}="${escapeHTML(String(value))}"`)
    .join(" ");
  const title = resourceActionDescription(action, kind);
  return `<button type="button" class="${escapeHTML(classes)}" data-resource-action="${escapeHTML(action.name || actionName)}" data-resource-action-kind="${escapeHTML(canonicalResourceKind(kind))}" data-resource-action-name="${escapeHTML(resourceName || "")}" data-resource-action-method="${escapeHTML(resourceActionMethod(action))}" aria-disabled="${disabled ? "true" : "false"}" ${disabled ? "disabled" : ""} ${title ? `title="${escapeHTML(title)}"` : ""} ${attrs}>
    ${escapeHTML(resourceActionLabel(action, options.label || "", kind))}
  </button>`;
}

export function resourceActionNeedsName(action) {
  return /\{(?:name|id|type)\}/.test(String(action?.path || ""));
}

export function appendQuery(path, query) {
  const normalized = String(path || "").trim();
  const suffix = String(query || "").replace(/^\?/, "");
  if (!normalized || !suffix) return normalized;
  return `${normalized}${normalized.includes("?") ? "&" : "?"}${suffix}`;
}

function normalizeResourceCapabilities(list) {
  return (Array.isArray(list) ? list : [])
    .map(item => ({
      ...item,
      kind: canonicalResourceKind(item?.kind),
      actions: Array.isArray(item?.actions) ? item.actions.filter(action => action?.name && action?.path) : []
    }))
    .filter(item => item.kind);
}

async function loadResourceCatalogCapabilities() {
  try {
    const catalog = await request("/api/resources");
    return normalizeResourceCapabilities(capabilitiesFromResourceCatalog(catalog));
  } catch {
    return [];
  }
}

async function loadDiscoveryResourceCapabilities() {
  try {
    const capabilities = await request("/api/capabilities");
    if (discoveryMetaSupported(capabilities) && Array.isArray(capabilities?.resource_capabilities)) {
      return normalizeResourceCapabilities(capabilities.resource_capabilities);
    }
  } catch {
    // Older runtimes can still expose resource capabilities through /api/help.
  }
  try {
    const help = await request("/api/help");
    if (discoveryMetaSupported(help)) {
      return normalizeResourceCapabilities(help?.resource_capabilities);
    }
    return [];
  } catch {
    return [];
  }
}

function mergeResourceCapabilities(...lists) {
  const byKind = new Map();
  for (const list of lists) {
    for (const item of normalizeResourceCapabilities(list)) {
      const existing = byKind.get(item.kind) || { kind: item.kind, actions: [] };
      byKind.set(item.kind, {
        ...existing,
        ...item,
        actions: mergeResourceActions(existing.actions, item.actions)
      });
    }
  }
  return [...byKind.values()];
}

function mergeResourceActions(existing = [], next = []) {
  const byName = new Map();
  for (const action of [...existing, ...next]) {
    const name = canonicalActionName(action?.name);
    if (!name) continue;
    byName.set(name, { ...(byName.get(name) || {}), ...action, name: action.name || name });
  }
  return [...byName.values()];
}

function capabilitiesFromResourceCatalog(catalog) {
  const families = Array.isArray(catalog?.families) ? catalog.families : [];
  const cacheKey = catalog?.meta?.cache_key || "";
  return families.map(family => ({
    ...family,
    source: "resources_catalog",
    catalog_cache_key: cacheKey,
    actions: Array.isArray(family?.actions) ? family.actions : []
  }));
}

function canonicalResourceKind(kind) {
  const normalized = String(kind || "").trim().toLowerCase().replaceAll("-", "_");
  return kindAliases[normalized] || kindAliases[String(kind || "").trim().toLowerCase()] || normalized;
}

function canonicalActionName(name) {
  return String(name || "").trim().toLowerCase().replaceAll("-", "_");
}

function resourceActionFallbackLabel(action, fallback, actionName) {
  const raw = String(action?.label || fallback || action?.name || "").trim();
  const translated = localizedText(raw);
  if (translated && translated !== raw) return translated;
  if (currentLanguage() !== "zh") return raw;
  const generated = zhActionLabel(actionName);
  return generated || raw;
}

function resourceActionFallbackDescription(action, actionName) {
  const raw = String(action?.description || "").trim();
  const translated = localizedText(raw);
  if (translated && translated !== raw) return translated;
  if (currentLanguage() !== "zh") return raw;
  const label = zhActionLabel(actionName);
  return label ? `执行“${label}”操作。` : raw;
}

function zhActionLabel(actionName) {
  const tokens = canonicalActionName(actionName).split("_").filter(Boolean);
  if (!tokens.length) return "";
  const verbMap = {
    activate: "启用",
    apply: "应用",
    build: "构建",
    capture: "捕获",
    check: "检查",
    clone: "复制",
    create: "创建",
    delete: "删除",
    disable: "禁用",
    download: "下载",
    enable: "启用",
    export: "导出",
    fork: "复制",
    generate: "生成",
    import: "导入",
    install: "安装",
    open: "打开",
    preview: "预览",
    rebuild: "重建",
    refresh: "刷新",
    remove: "删除",
    reset: "重置",
    run: "运行",
    save: "保存",
    scaffold: "从模板创建",
    sync: "同步",
    test: "测试",
    update: "更新",
    upload: "上传",
    validate: "验证"
  };
  const nounMap = {
    action: "操作",
    bundle: "资源包",
    catalog: "目录",
    config: "配置",
    expression: "表达式",
    graph: "图",
    helper: "助手",
    kit: "Kit",
    metadata: "元数据",
    node: "节点",
    policy: "策略",
    resource: "资源",
    rule: "规则",
    saved: "已保存",
    schema: "结构",
    template: "模板",
    tool: "工具",
    workflow: "工作流"
  };
  const verbIndex = tokens.findIndex(token => verbMap[token]);
  const verb = verbIndex >= 0 ? verbMap[tokens[verbIndex]] : "";
  const rest = tokens
    .filter((_, index) => index !== verbIndex)
    .map(token => nounMap[token] || localizedText(token))
    .filter(Boolean);
  if (verb && rest.length) return `${verb}${rest.join("")}`;
  if (verb) return `${verb}资源`;
  return rest.join("") || localizedText(actionName);
}
