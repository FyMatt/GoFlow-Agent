import { escapeHTML, request } from "./api.js";
import {
  normalizeRuntimeWorkflowRuns,
  normalizeWorkflowRunList,
  pendingApprovalCount,
  setRuntimeVisibleApprovalCount,
  visibleApprovalCountFromActions
} from "./approval_counts.js";
import { applyStaticTranslations, currentLanguage, localizedText, setLanguage, t } from "./i18n.js";
import { notifyViewRendered, setupOnboarding } from "./onboarding.js";
import { renderOverview } from "./views/overview.js";
import { renderChat } from "./views/chat.js";
import { renderWorkspace } from "./views/workspace.js";
import { renderWorkflows } from "./views/workflows.js";
import { renderApprovals } from "./views/approvals.js";
import { renderCatalog } from "./views/catalog.js";
import { renderStatus } from "./views/status.js";
import { renderSettings } from "./views/settings.js";

const views = {
  overview: { title: "view.overview.title", eyebrow: "view.overview.eyebrow", render: renderOverview },
  playground: { title: "view.playground.title", eyebrow: "view.playground.eyebrow", render: renderChat },
  workspace: { title: "view.workspace.title", eyebrow: "view.workspace.eyebrow", render: renderWorkspace },
  workflows: { title: "view.workflows.title", eyebrow: "view.workflows.eyebrow", render: renderWorkflows },
  approvals: { title: "view.approvals.title", eyebrow: "view.approvals.eyebrow", render: renderApprovals },
  catalog: { title: "view.catalog.title", eyebrow: "view.catalog.eyebrow", render: renderCatalog },
  status: { title: "view.status.title", eyebrow: "view.status.eyebrow", render: renderStatus },
  settings: { title: "view.settings.title", eyebrow: "view.settings.eyebrow", render: renderSettings }
};

const themeStorageKey = "goflow.theme";

const app = document.getElementById("app");
const sectionTitle = document.getElementById("sectionTitle");
const sectionEyebrow = document.getElementById("sectionEyebrow");
const runtimePill = document.getElementById("runtimePill");
const sideStatus = document.getElementById("sideStatus");
const languageSelect = document.getElementById("languageSelect");
const themeToggle = document.getElementById("themeToggle");
const tourToggle = document.getElementById("tourToggle");

let runtime = null;
let configDiagnostics = null;
let theme = normalizeTheme(document.documentElement.dataset.theme || localStorage.getItem(themeStorageKey));
let runtimePollTimer = null;
let runtimePollInFlight = false;
let configDiagnosticsLastFetch = 0;
let runtimeShellSignature = "";
let runtimeEventSignature = "";
let approvalBadgeSignature = "";
let approvalBadgeLastFetch = 0;
let approvalBadgeResolvedCount = null;
const configDiagnosticsRefreshMs = 8000;
const approvalBadgeRefreshMs = 10000;
const userScrollIntentWindowMs = 420;
const shellConfigDiagnosticsPath = "/api/config/diagnostics?include_optional=0";

setupGlobalScrollIntentTracking();
window.addEventListener("goflow:config-diagnostics", event => {
  const diagnostics = event.detail && typeof event.detail === "object" ? event.detail : null;
  if (!diagnostics) return;
  configDiagnostics = diagnostics;
  configDiagnosticsLastFetch = Date.now();
  const approvalCount = approvalBadgeResolvedCount ?? (runtime ? pendingApprovalCount(runtime) : 0);
  if (runtime) setRuntimeVisibleApprovalCount(runtime, approvalCount);
  if (runtime) sideStatus.innerHTML = renderSideStatus(runtime, approvalCount, configDiagnostics);
  updateSettingsNavBadge(configDiagnostics);
  runtimeShellSignature = runtime ? runtimeShellStateSignature(runtime, approvalCount, configDiagnostics) : runtimeShellSignature;
});

setupOnboarding({
  trigger: tourToggle,
  overlay: document.getElementById("tourOverlay"),
  popover: document.querySelector("#tourOverlay .tour-popover"),
  badge: document.getElementById("tourBadge"),
  counter: document.getElementById("tourStepCounter"),
  title: document.getElementById("tourTitle"),
  body: document.getElementById("tourBody"),
  meta: document.getElementById("tourMeta"),
  prev: document.getElementById("tourPrev"),
  next: document.getElementById("tourNext"),
  skip: document.getElementById("tourSkip"),
  contentRoot: document.body,
  t
});

applyTheme(theme);
updateDocumentLanguage();

async function refreshRuntime() {
  runtime = await request("/api/runtime");
  configDiagnostics = await refreshConfigDiagnostics(configDiagnostics).catch(() => configDiagnostics);
  const approvalSignature = approvalBadgeStateSignature(runtime);
  const approvalCount = approvalSignature === approvalBadgeSignature && approvalBadgeResolvedCount !== null
    ? approvalBadgeResolvedCount
    : pendingApprovalCount(runtime);
  setRuntimeVisibleApprovalCount(runtime, approvalCount);
  const shellSignature = runtimeShellStateSignature(runtime, approvalCount, configDiagnostics);
  if (shellSignature !== runtimeShellSignature) {
    runtimeShellSignature = shellSignature;
    runtimePill.textContent = runtimePillText(runtime);
    sideStatus.innerHTML = renderSideStatus(runtime, approvalCount, configDiagnostics);
    updateApprovalNavBadge(approvalCount);
    updateSettingsNavBadge(configDiagnostics);
  }
  scheduleApprovalBadgeRefresh(runtime, approvalSignature);
  const nextEventSignature = runtimeViewEventSignature(runtime);
  if (nextEventSignature !== runtimeEventSignature) {
    runtimeEventSignature = nextEventSignature;
    window.dispatchEvent(new CustomEvent("goflow:runtime", { detail: runtime }));
  }
  return runtime;
}

function setupGlobalScrollIntentTracking() {
  let scrollFrame = 0;
  let inputFrame = 0;
  const mark = () => {
    window.__goflowLastUserScrollIntent = performance.now();
  };
  const markInputScroll = () => {
    if (inputFrame) return;
    inputFrame = window.requestAnimationFrame(() => {
      inputFrame = 0;
      mark();
    });
  };
  const markDocumentScroll = event => {
    const target = event.target;
    if (target !== document && target !== document.documentElement) return;
    if (scrollFrame) return;
    scrollFrame = window.requestAnimationFrame(() => {
      scrollFrame = 0;
      mark();
    });
  };
  window.addEventListener("wheel", markInputScroll, { capture: true, passive: true });
  window.addEventListener("touchmove", markInputScroll, { capture: true, passive: true });
  window.addEventListener("scroll", markDocumentScroll, { capture: true, passive: true });
  window.addEventListener("keydown", event => {
    if (["ArrowDown", "ArrowUp", "PageDown", "PageUp", "Home", "End", " "].includes(event.key)) mark();
  }, { capture: true });
  window.goflowCanRestoreWindowScroll = (maxAge = userScrollIntentWindowMs) => {
    const lastIntent = Number(window.__goflowLastUserScrollIntent || 0);
    return !lastIntent || performance.now() - lastIntent > maxAge;
  };
}

async function refreshConfigDiagnostics(fallback = null) {
  const now = Date.now();
  if (fallback && now - configDiagnosticsLastFetch < configDiagnosticsRefreshMs) return fallback;
  configDiagnosticsLastFetch = now;
  try {
    return await request(shellConfigDiagnosticsPath);
  } catch (error) {
    return { __error: error.message || String(error) };
  }
}

function startRuntimeAutoRefresh() {
  if (runtimePollTimer) return;
  runtimePollTimer = window.setInterval(async () => {
    if (document.visibilityState === "hidden" || runtimePollInFlight) return;
    if (window.goflowCanRestoreWindowScroll?.(720) === false) return;
    runtimePollInFlight = true;
    try {
      await refreshRuntime();
    } catch {
      // The visible view keeps its last good snapshot when the API is unavailable.
    } finally {
      runtimePollInFlight = false;
    }
  }, 5000);
}

function renderSideStatus(runtime, approvalCount = pendingApprovalCount(runtime), diagnostics = configDiagnostics) {
  const rows = [
    [t("runtime.workspace"), shellDisplayValue(runtime.workspace?.display || t("common.none")), ""],
    [t("runtime.trace"), runtime.trace ? t("common.on") : t("common.off"), ""],
    [t("runtime.pending"), approvalCount, approvalCount ? "warn" : ""],
    [t("runtime.tools"), runtime.tools?.length || 0, ""],
    [t("runtime.config"), configStatusText(diagnostics), configStatusTone(diagnostics)]
  ];
  return `<div class="side-status-card">${rows.map(([label, value, tone]) => `
    <div class="side-status-row ${escapeHTML(tone || "")}">
      <span>${escapeHTML(label)}</span>
      <strong>${escapeHTML(value)}</strong>
    </div>`).join("")}</div>`;
}

function runtimePillText(snapshot = {}) {
  return [
    shellDisplayValue(snapshot.active_agent || "-"),
    shellModeLabel(snapshot.mode || "-"),
    shellDisplayValue(snapshot.version || "dev")
  ].join(" / ");
}

function shellModeLabel(value) {
  const raw = String(value || "").trim();
  if (!raw || raw === "-") return raw || "-";
  const key = `catalog.mode.${raw}`;
  const translated = t(key);
  return translated === key ? shellDisplayValue(raw) : translated;
}

function configStatusText(diagnostics) {
  if (diagnostics?.__error) return t("runtime.configUnavailable");
  if (diagnostics?.restart_required) return t("runtime.configRestart");
  const status = String(diagnostics?.status || "").toLowerCase();
  const counts = configDiagnosticCounts(diagnostics);
  const hasItemDiagnostics = Array.isArray(diagnostics?.items) && diagnostics.items.length > 0;
  if (status === "warning" && hasItemDiagnostics && !counts.attention) return t("runtime.configOk");
  if (status === "error" || status === "warning") return t("runtime.configCheck");
  if (status === "ok") return t("runtime.configOk");
  return t("runtime.configUnknown");
}

function configStatusTone(diagnostics) {
  if (diagnostics?.__error) return "bad";
  if (diagnostics?.restart_required) return "warn";
  const status = String(diagnostics?.status || "").toLowerCase();
  const counts = configDiagnosticCounts(diagnostics);
  const hasItemDiagnostics = Array.isArray(diagnostics?.items) && diagnostics.items.length > 0;
  if (status === "warning" && hasItemDiagnostics && !counts.attention) return "";
  if (status === "error" || status === "warning") return "warn";
  return "";
}

async function refreshApprovalBadges(snapshot) {
  const payload = await request("/api/workflow-runs");
  if (runtime !== snapshot) return;
  const runs = normalizeWorkflowRunList(payload);
  const pendingTools = Array.isArray(snapshot?.session?.pending_approvals)
    ? snapshot.session.pending_approvals
    : [];
  const count = await visibleApprovalCountFromActions(pendingTools, runs);
  setRuntimeVisibleApprovalCount(snapshot, count);
  sideStatus.innerHTML = renderSideStatus(snapshot, count, configDiagnostics);
  updateApprovalNavBadge(count);
  updateSettingsNavBadge(configDiagnostics);
  runtimeShellSignature = runtimeShellStateSignature(snapshot, count, configDiagnostics);
  approvalBadgeSignature = approvalBadgeStateSignature(snapshot);
  approvalBadgeResolvedCount = count;
  window.dispatchEvent(new CustomEvent("goflow:runtime", { detail: snapshot }));
}

function scheduleApprovalBadgeRefresh(snapshot, signature = approvalBadgeStateSignature(snapshot)) {
  const now = Date.now();
  if (signature === approvalBadgeSignature && now - approvalBadgeLastFetch < approvalBadgeRefreshMs) return;
  approvalBadgeLastFetch = now;
  refreshApprovalBadges(snapshot).catch(() => {});
}

function runtimeShellStateSignature(snapshot, approvalCount, diagnostics) {
  return [
    snapshot?.active_agent || "",
    snapshot?.mode || "",
    snapshot?.version || "",
    snapshot?.workspace?.display || snapshot?.workspace?.root || "",
    snapshot?.trace ? "trace" : "no-trace",
    snapshot?.tools?.length || 0,
    approvalCount || 0,
    configStatusText(diagnostics),
    configStatusTone(diagnostics)
  ].join("|");
}

function runtimeViewEventSignature(snapshot) {
  const statusLines = Array.isArray(snapshot?.status_lines) ? snapshot.status_lines : [];
  const workflow = snapshot?.session?.workflow || {};
  const approvals = Array.isArray(snapshot?.session?.pending_approvals)
    ? snapshot.session.pending_approvals.map(item => [
      item?.id || item?.call_id || item?.tool_call_id || "",
      item?.tool_name || "",
      item?.workflow_name || "",
      item?.stage || item?.stage_name || ""
    ].join(":")).join(",")
    : "";
  const runs = normalizeRuntimeWorkflowRuns(snapshot).slice(0, 24).map(run => [
    run?.id || "",
    run?.name || "",
    run?.status || "",
    run?.next_stage || "",
    run?.pending_call_id || "",
    runtimeRunEventMarker(run),
    runtimeCollectionCount(run?.completed_stages, run?.stages_count),
    runtimeCollectionCount(run?.artifacts, run?.artifacts_count),
    runtimeCollectionCount(run?.diffs, run?.diffs_count),
    runtimeTextLength(run?.output || run?.summary || run?.final_message || "")
  ].join(":")).join(",");
  const agentRuns = normalizeRuntimeAgentRuns(snapshot).slice(0, 16).map(run => [
    run?.id || "",
    run?.agent_id || run?.agent || "",
    run?.status || "",
    run?.pending_call_id || "",
    runtimeRunEventMarker(run),
    runtimeCollectionCount(run?.diffs, run?.diffs_count),
    runtimeTextLength(run?.output || run?.summary || run?.final_message || "")
  ].join(":")).join(",");
  return [
    snapshot?.active_agent || "",
    snapshot?.mode || "",
    snapshot?.workspace?.display || snapshot?.workspace?.root || "",
    snapshot?.trace ? "trace" : "no-trace",
    snapshot?.tools?.length || 0,
    statusLines.length,
    statusLines.at(-1) || "",
    workflow?.name || "",
    workflow?.status || "",
    workflow?.next_stage || "",
    approvals,
    runs,
    agentRuns
  ].join("|");
}

function approvalBadgeStateSignature(snapshot) {
  const pendingTools = Array.isArray(snapshot?.session?.pending_approvals)
    ? snapshot.session.pending_approvals.map(item => [
      item?.id || item?.call_id || item?.tool_call_id || "",
      item?.tool_name || "",
      item?.workflow_name || "",
      item?.stage || item?.stage_name || ""
    ].join(":")).join(",")
    : "";
  const runs = normalizeRuntimeWorkflowRuns(snapshot).slice(0, 32).map(run => [
    run?.id || "",
    run?.status || "",
    run?.next_stage || "",
    run?.pending_call_id || ""
  ].join(":")).join(",");
  return `${pendingTools}|${runs}`;
}

function normalizeRuntimeAgentRuns(snapshot) {
  const value = snapshot?.session?.agent_runs;
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.agent_runs)) return value.agent_runs;
  if (Array.isArray(value?.items)) return value.items;
  return [];
}

function runtimeCollectionCount(value, fallback = 0) {
  if (Array.isArray(value)) return value.length;
  const count = Number(fallback || 0);
  return Number.isFinite(count) ? count : 0;
}

function runtimeTextLength(value) {
  return String(value || "").length;
}

function runtimeRunEventMarker(run = {}) {
  const events = Array.isArray(run.events) ? run.events : [];
  if (!events.length) return Number(run.events_count || 0) || 0;
  const lastSeq = events.reduce((max, event) => {
    const seq = Number(event?.seq || event?.sequence || event?.id || 0);
    return Number.isFinite(seq) ? Math.max(max, seq) : max;
  }, 0);
  return `${events.length}:${lastSeq || ""}`;
}

function updateApprovalNavBadge(runtime) {
  const button = document.querySelector('.nav button[data-view="approvals"]');
  if (!button) return;
  const count = typeof runtime === "number" ? runtime : pendingApprovalCount(runtime);
  const label = t("nav.approvals");
  button.innerHTML = count
    ? `${escapeHTML(label)} <span class="nav-badge">${escapeHTML(String(count))}</span>`
    : escapeHTML(label);
  button.setAttribute("aria-label", count ? `${label}: ${t("approvals.count", { count })}` : label);
}

function updateSettingsNavBadge(diagnostics = configDiagnostics) {
  const button = document.querySelector('.nav button[data-view="settings"]');
  if (!button) return;
  const label = t("nav.settings");
  const counts = configDiagnosticCounts(diagnostics);
  const status = String(diagnostics?.status || "").toLowerCase();
  const hasItemDiagnostics = Array.isArray(diagnostics?.items) && diagnostics.items.length > 0;
  const statusNeedsAttention = status === "error" || (status === "warning" && (!hasItemDiagnostics || counts.attention > 0));
  const needsAttention = Boolean(
    diagnostics?.restart_required ||
    diagnostics?.__error ||
    counts.errors ||
    counts.warnings ||
    statusNeedsAttention
  );
  const badgeValue = counts.errors || counts.warnings || (diagnostics?.restart_required ? "!" : counts.total || "!");
  const badgeTone = counts.errors || diagnostics?.__error || status === "error"
    ? "bad"
    : counts.warnings || diagnostics?.restart_required || statusNeedsAttention
      ? "warn"
      : "info";
  button.innerHTML = needsAttention
    ? `${escapeHTML(label)} <span class="nav-badge nav-badge-config nav-badge-config-${badgeTone}">${escapeHTML(String(badgeValue))}</span>`
    : escapeHTML(label);
  button.setAttribute("aria-label", needsAttention
    ? `${label}: ${configStatusText(diagnostics)}, ${configDiagnosticCountLabel(counts)}`
    : label);
}

function configDiagnosticCounts(diagnostics = {}) {
  const source = diagnostics?.diagnostics || diagnostics?.diagnostic_counts || diagnostics?.counts || diagnostics?.summary?.diagnostics || {};
  const items = Array.isArray(diagnostics?.items) ? diagnostics.items : [];
  const computed = configDiagnosticComputedCounts(items);
  const hasItems = items.length > 0;
  const errors = hasItems
    ? computed.errors
    : firstFiniteNumber(source.errors, source.error, source.error_count, diagnostics?.errors, diagnostics?.error, diagnostics?.error_count, computed.errors);
  const warnings = hasItems
    ? computed.warnings
    : firstFiniteNumber(source.warnings, source.warning, source.warn, source.warning_count, diagnostics?.warnings, diagnostics?.warning, diagnostics?.warning_count, computed.warnings);
  const info = hasItems
    ? computed.info
    : firstFiniteNumber(source.info, source.infos, source.information, source.info_count, diagnostics?.info, diagnostics?.infos, diagnostics?.info_count, computed.info);
  return {
    total: firstFiniteNumber(source.total, source.count, diagnostics?.total, diagnostics?.count, diagnostics?.diagnostics_count, computed.total),
    info,
    warnings,
    errors,
    optional: computed.optional,
    attention: errors + warnings
  };
}

function configDiagnosticCountLabel(counts) {
  return t("settings.navDiagnosticCounts", {
    total: counts.total,
    attention: counts.attention,
    errors: counts.errors,
    warnings: counts.warnings,
    info: counts.info,
    optional: counts.optional
  });
}

function shellDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return text;
  if (text.includes("\\") || text.includes("://")) return text;
  if (text.includes("/") && !/\s/.test(text)) return text;
  if (text.includes(".") && !/\s/.test(text)) return text;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return text;
  return localizedText(text);
}

function configDiagnosticComputedCounts(items = []) {
  return items.reduce((acc, item) => {
    const severity = String(item?.severity || "info").toLowerCase();
    const optional = isOptionalConfigDiagnostic(item);
    const actionable = isActionableConfigDiagnostic(item);
    if (optional) acc.optional += 1;
    if (severity === "error" && actionable) acc.errors += 1;
    else if ((severity === "warning" || severity === "warn") && actionable) acc.warnings += 1;
    else acc.info += 1;
    acc.total += 1;
    return acc;
  }, { total: 0, info: 0, warnings: 0, errors: 0, optional: 0 });
}

function isOptionalConfigDiagnostic(item = {}) {
  const severity = String(item.severity || "info").toLowerCase();
  const code = String(item.code || "");
  const category = String(item.category || "").toLowerCase();
  const optional = diagnosticBooleanFlag(item.optional);
  const nonActionable = diagnosticBooleanFlag(item.actionable) === false;
  if (optional || category === "optional" || category === "optional_extension") return true;
  if (severity === "info" && nonActionable) return true;
  return severity === "info" && (code === "module_dir_empty" || code === "config_load_ok");
}

function isActionableConfigDiagnostic(item = {}) {
  if (!item || item.code === "config_load_ok") return false;
  if (isOptionalConfigDiagnostic(item)) return false;
  if (isSecurityGuidanceConfigDiagnostic(item)) return false;
  return diagnosticBooleanFlag(item.actionable) !== false;
}

function isSecurityGuidanceConfigDiagnostic(item = {}) {
  const code = String(item.code || "").toLowerCase();
  if (!code) return false;
  if (code.startsWith("tool_risk_policy")) return true;
  if (code.startsWith("mcp_isolation") || code.startsWith("mcp_container")) return true;
  if (code.includes("_sandbox") || code.includes("sandboxed")) return true;
  const targetKind = String(item.target_kind || "").toLowerCase();
  return targetKind === "mcp_server" && String(item.field || "").toLowerCase().includes("isolation");
}

function diagnosticBooleanFlag(value) {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true") return true;
    if (normalized === "false") return false;
  }
  return undefined;
}

function firstFiniteNumber(...values) {
  for (const value of values) {
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  return 0;
}

async function show(viewName) {
  clearTransientHandlers();
  const resolvedViewName = views[viewName] ? viewName : defaultView();
  const view = views[resolvedViewName] || views.workflows;
  document.querySelectorAll(".nav button").forEach(button => {
    button.classList.toggle("active", button.dataset.view === resolvedViewName);
  });
  updateDocumentLanguage();
  applyStaticTranslations();
  updateThemeToggle();
  sectionTitle.textContent = t(view.title);
  sectionEyebrow.textContent = t(view.eyebrow);
  document.title = `${t(view.title)} - GoFlow`;
  app.dataset.view = resolvedViewName;
  app.innerHTML = `<div class="panel loading-panel">${t("common.loading")}</div>`;
  try {
    const current = await refreshRuntime();
    await view.render(app, current, refreshRuntime);
    notifyViewRendered(resolvedViewName);
  } catch (error) {
    app.innerHTML = `<div class="panel"><h2>${t("common.errorTitle")}</h2><p class="muted">${escapeHTML(shellDisplayValue(error?.message || String(error || "")))}</p></div>`;
  }
}

function normalizeTheme(value) {
  return value === "dark" ? "dark" : "light";
}

function applyTheme(nextTheme) {
  theme = normalizeTheme(nextTheme);
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  localStorage.setItem(themeStorageKey, theme);
}

function currentTheme() {
  return theme;
}

function updateThemeToggle() {
  if (!themeToggle) return;
  const nextTheme = currentTheme() === "dark" ? "light" : "dark";
  const label = t(`theme.switchTo.${nextTheme}`);
  themeToggle.textContent = label;
  themeToggle.setAttribute("aria-label", label);
  themeToggle.setAttribute("title", `${t("theme.current")}: ${t(`theme.${currentTheme()}`)}`);
}

function updateDocumentLanguage() {
  document.documentElement.lang = currentLanguage() === "zh" ? "zh-CN" : "en";
}

function toggleTheme() {
  applyTheme(currentTheme() === "dark" ? "light" : "dark");
  updateThemeToggle();
}

document.querySelectorAll(".nav button").forEach(button => {
  button.addEventListener("click", () => {
    location.hash = button.dataset.view;
  });
});

window.addEventListener("hashchange", () => show(location.hash.slice(1) || defaultView()));

languageSelect.value = currentLanguage();
languageSelect.addEventListener("change", () => {
  setLanguage(languageSelect.value);
  updateDocumentLanguage();
  updateThemeToggle();
  show(location.hash.slice(1) || defaultView());
});

themeToggle?.addEventListener("click", () => {
  toggleTheme();
});

function defaultView() {
  return location.pathname.startsWith("/workflows") ? "workflows" : "overview";
}

function clearTransientHandlers() {
  window.dispatchEvent(new CustomEvent("goflow:view-dispose"));
  document.querySelector("[data-app-shell]")?.classList.remove("workflow-focus-mode");
  window.onmousemove = null;
  window.onmouseup = null;
}

updateThemeToggle();
startRuntimeAutoRefresh();
show(location.hash.slice(1) || defaultView());
