import { escapeHTML, request } from "./api.js";
import {
  normalizeRuntimeWorkflowRuns
} from "./approval_counts.js";
import { icon, navIcon, viewIcon } from "./icons.js";
import { applyStaticTranslations, currentLanguage, localizedText, t } from "./i18n.js";

const views = {
  playground: { title: "view.playground.title", eyebrow: "view.playground.eyebrow", module: "./views/chat.js", exportName: "renderChat" },
  workflows: { title: "view.workflows.title", eyebrow: "view.workflows.eyebrow", module: "./views/workflows.js", exportName: "renderWorkflows" },
  catalog: { title: "view.catalog.title", eyebrow: "view.catalog.eyebrow", module: "./views/catalog.js", exportName: "renderCatalog" },
  usage: { title: "view.usage.title", eyebrow: "view.usage.eyebrow", module: "./views/usage.js", exportName: "renderUsage" },
  settings: { title: "view.settings.title", eyebrow: "view.settings.eyebrow", module: "./views/settings.js", exportName: "renderSettings" }
};

const themeStorageKey = "goflow.theme";
const sidebarStorageKey = "goflow.sidebar.collapsed";

const app = document.getElementById("app");
const shellRoot = document.querySelector("[data-app-shell]");
const sectionTitle = document.getElementById("sectionTitle");
const sectionEyebrow = document.getElementById("sectionEyebrow");
const sidebarToggle = document.getElementById("sidebarToggle");
const navButtons = Array.from(document.querySelectorAll(".nav button[data-view]"));

let runtime = null;
let theme = normalizeTheme(document.documentElement.dataset.theme || localStorage.getItem(themeStorageKey));
let runtimePollTimer = null;
let runtimePollInFlight = false;
let runtimeEventSignature = "";
let sidebarCollapsed = loadSidebarCollapsed();
let showSequence = 0;
const userScrollIntentWindowMs = 420;

setupGlobalScrollIntentTracking();
applyTheme(theme);
updateDocumentLanguage();
renderAllNavButtons();
setSidebarCollapsed(sidebarCollapsed, { persist: false });

async function refreshRuntime() {
  runtime = await request("/api/runtime");
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

async function show(viewName) {
  const sequence = ++showSequence;
  clearTransientHandlers();
  const resolvedViewName = views[viewName] ? viewName : defaultView();
  const view = views[resolvedViewName] || views.workflows;
  syncNavButtons(resolvedViewName);
  updateDocumentLanguage();
  applyStaticTranslations();
  renderAllNavButtons();
  updateSidebarToggle();
  sectionTitle.innerHTML = `<span class="section-title-icon">${icon(viewIcon(resolvedViewName))}</span><span>${escapeHTML(t(view.title))}</span>`;
  sectionEyebrow.textContent = t(view.eyebrow);
  document.title = `${t(view.title)} - GoFlow`;
  app.dataset.view = resolvedViewName;
  app.classList.remove("view-ready");
  app.innerHTML = `<div class="panel loading-panel">${t("common.loading")}</div>`;
  try {
    const [current, render] = await Promise.all([refreshRuntime(), loadViewRenderer(view)]);
    if (sequence !== showSequence) return;
    await render(app, current, refreshRuntime);
    if (sequence !== showSequence) return;
    requestAnimationFrame(() => {
      if (sequence === showSequence) app.classList.add("view-ready");
    });
  } catch (error) {
    app.innerHTML = `<div class="panel"><h2>${t("common.errorTitle")}</h2><p class="muted">${escapeHTML(shellDisplayValue(error?.message || String(error || "")))}</p></div>`;
    app.classList.add("view-ready");
  }
}

async function loadViewRenderer(view) {
  if (view.render) return view.render;
  const loaded = await import(view.module);
  const render = loaded?.[view.exportName];
  if (typeof render !== "function") throw new Error(`View module ${view.module} does not export ${view.exportName}.`);
  view.render = render;
  return render;
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

function updateDocumentLanguage() {
  document.documentElement.lang = currentLanguage() === "zh" ? "zh-CN" : "en";
}

navButtons.forEach(button => {
  button.addEventListener("click", () => {
    shellRoot?.classList.remove("mobile-nav-open");
    location.hash = button.dataset.view;
  });
});

window.addEventListener("hashchange", () => show(location.hash.slice(1) || defaultView()));

window.addEventListener("goflow:set-theme", event => {
  applyTheme(event.detail?.theme);
});

sidebarToggle?.addEventListener("click", () => {
  if (window.matchMedia("(max-width: 700px)").matches) {
    const expanded = shellRoot?.classList.toggle("mobile-nav-open") || false;
    sidebarToggle.setAttribute("aria-expanded", String(expanded));
    return;
  }
  toggleSidebarCollapsed();
});

function defaultView() {
  return location.pathname.startsWith("/workflows") ? "workflows" : "playground";
}

function clearTransientHandlers() {
  window.dispatchEvent(new CustomEvent("goflow:view-dispose"));
  document.querySelector("[data-app-shell]")?.classList.remove("workflow-focus-mode");
  window.onmousemove = null;
  window.onmouseup = null;
}

startRuntimeAutoRefresh();
show(location.hash.slice(1) || defaultView());

function renderAllNavButtons() {
  navButtons.forEach(button => renderNavButton(button));
}

function renderNavButton(button, options = {}) {
  if (!button) return;
  const label = t(button.dataset.labelKey || `nav.${button.dataset.view || ""}`);
  const badge = String(options.badge || "").trim();
  const badgeClass = String(options.badgeClass || "nav-badge").trim();
  button.innerHTML = `
    <span class="nav-icon" aria-hidden="true">${navIcon(button.dataset.icon || button.dataset.view || "")}</span>
    <span class="nav-label">${escapeHTML(label)}</span>
    ${badge ? `<span class="${escapeHTML(badgeClass)}">${escapeHTML(badge)}</span>` : ""}
  `;
  const ariaLabel = String(options.ariaLabel || label).trim() || label;
  button.setAttribute("aria-label", ariaLabel);
  button.dataset.tooltip = label;
  button.title = sidebarCollapsed ? ariaLabel : "";
}

function syncNavButtons(activeView) {
  navButtons.forEach(button => {
    button.classList.toggle("active", button.dataset.view === activeView);
  });
}

function loadSidebarCollapsed() {
  try {
    return localStorage.getItem(sidebarStorageKey) === "1";
  } catch {
    return false;
  }
}

function setSidebarCollapsed(next, options = {}) {
  sidebarCollapsed = Boolean(next);
  shellRoot?.classList.toggle("sidebar-collapsed", sidebarCollapsed);
  if (options.persist !== false) {
    try {
      localStorage.setItem(sidebarStorageKey, sidebarCollapsed ? "1" : "0");
    } catch {
      // Ignore storage failures; the UI still updates for this session.
    }
  }
  renderAllNavButtons();
  updateSidebarToggle();
}

function toggleSidebarCollapsed() {
  setSidebarCollapsed(!sidebarCollapsed);
}

function updateSidebarToggle() {
  if (!sidebarToggle) return;
  const expanded = !sidebarCollapsed;
  const label = expanded ? t("nav.collapseSidebar") : t("nav.expandSidebar");
  sidebarToggle.setAttribute("aria-label", label);
  sidebarToggle.setAttribute("title", label);
  sidebarToggle.setAttribute("aria-pressed", sidebarCollapsed ? "true" : "false");
  sidebarToggle.setAttribute("aria-expanded", expanded ? "true" : "false");
  updateSidebarToggleIcon();
}

function updateSidebarToggleIcon() {
  const mark = sidebarToggle?.querySelector(".sidebar-toggle-icon");
  if (!mark) return;
  mark.innerHTML = icon(sidebarCollapsed ? "chevrons-right" : "panel-left");
}
