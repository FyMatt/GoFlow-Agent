import { discoveryMetaSupported, escapeHTML, postJSON, request } from "../api.js";
import { currentLanguage, localizedText, t } from "../i18n.js";

export async function renderSettings(root, runtime, refreshRuntime) {
  const [update, diagnostics, discovery] = await Promise.all([
    loadOptional("/api/update-policy"),
    loadOptional("/api/config/diagnostics?include_optional=0"),
    loadClientDiscovery()
  ]);
  const diagnosticCapabilities = configDiagnosticCapabilities(discovery);
  if (diagnostics && typeof diagnostics === "object") {
    window.dispatchEvent(new CustomEvent("goflow:config-diagnostics", { detail: diagnostics }));
  }

  root.innerHTML = `
    <div class="grid settings-grid">
      ${renderSettingsHero(diagnostics, runtime)}
      <section class="panel span-8 settings-health-panel" data-settings-focus="config-health" tabindex="-1">
        <div class="panel-head">
          <div>
            <h2>${t("settings.configHealth")}</h2>
            <p class="muted">${t("settings.configHealthHelp")}</p>
          </div>
          ${statusBadge(diagnostics?.status, diagnostics?.restart_required)}
        </div>
        ${renderSummaryGrid(diagnostics, diagnosticCapabilities)}
        ${renderDiagnosticCapabilities(diagnosticCapabilities)}
        ${renderPrioritySummary(diagnostics, runtime, diagnosticCapabilities)}
        ${renderDiagnosticsList(diagnostics, diagnosticCapabilities)}
      </section>
      <section class="panel span-4 settings-check-panel" data-settings-anchor="first-run" tabindex="-1">
        <div class="panel-head">
          <div>
            <h2>${t("settings.firstRun")}</h2>
            <p class="muted">${t("settings.firstRunHelp")}</p>
          </div>
        </div>
        ${renderEnvChecks(runtime)}
      </section>
      <section class="panel span-7 settings-module-panel">
        <div class="panel-head">
          <div>
            <h2>${t("settings.configModules")}</h2>
            <p class="muted">${t("settings.configModulesHelp")}</p>
          </div>
          <span class="badge neutral">${t("settings.moduleCount", { count: diagnostics?.modules?.length || 0 })}</span>
        </div>
        ${renderModules(diagnostics)}
      </section>
      <section class="panel span-5 settings-tour-panel">
        <div class="panel-head">
          <div>
            <h2>${t("settings.guidedTour")}</h2>
            <p class="muted">${t("settings.guidedTourHelp")}</p>
          </div>
        </div>
        ${renderTourPath()}
      </section>
      <section class="panel span-12 settings-update-panel">
        <div class="panel-head">
          <div>
            <h2>${t("settings.updates")}</h2>
            <p class="muted">${t("settings.updatesHelp")}</p>
          </div>
          <span class="badge info">${escapeHTML(update?.current_version || runtime.version || "dev")}</span>
        </div>
        ${renderUpdatePolicy(update)}
      </section>
    </div>`;
  bindSettingsActions(root, refreshRuntime);
  focusSettingsTarget(root);
}

async function loadOptional(path) {
  try {
    return await request(path);
  } catch (error) {
    return {
      __error: localizedSettingsErrorMessage(error, t("common.errorTitle"))
    };
  }
}

async function loadClientDiscovery() {
  let capabilitiesError = null;
  try {
    const capabilities = await request("/api/capabilities");
    if (discoveryMetaSupported(capabilities)) {
      return { ...capabilities, discovery_source: "capabilities" };
    }
    capabilitiesError = unsupportedDiscoveryError("capabilities");
  } catch (error) {
    capabilitiesError = error;
  }
  try {
    const help = await request("/api/help");
    if (discoveryMetaSupported(help)) {
      return { ...help, discovery_source: "help" };
    }
    return fallbackDiscovery(unsupportedDiscoveryError("help"));
  } catch (helpError) {
    return fallbackDiscovery(helpError || capabilitiesError);
  }
}

function unsupportedDiscoveryError(source) {
  return new Error(t("settings.discoveryUnsupported", { source }));
}

function fallbackDiscovery(error) {
  return {
    __error: localizedSettingsErrorMessage(error, t("common.errorTitle")),
    discovery_source: "fallback",
    capabilities: {}
  };
}

function localizedSettingsErrorMessage(error, fallback = "") {
  const text = error?.message || (typeof error === "string" ? error : String(error || "")) || fallback;
  return settingsDisplayText(text || fallback);
}

function bindSettingsActions(root, refreshRuntime) {
  root.querySelectorAll("[data-settings-workspace-confirm]").forEach(button => {
    button.addEventListener("click", async () => {
      const output = root.querySelector("[data-settings-workspace-output]");
      setSettingsWorkspaceBusy(root, true);
      if (output) output.textContent = t("workspace.confirmingTitle");
      try {
        await settingsWorkspaceActionRequest(button.dataset.settingsWorkspaceConfirmPath || "/api/workspace/confirm", button.dataset.settingsWorkspaceConfirmMethod || "POST");
        const nextRuntime = typeof refreshRuntime === "function" ? await refreshRuntime() : null;
        if (nextRuntime) {
          await renderSettings(root, nextRuntime, refreshRuntime);
          return;
        }
        if (output) output.textContent = t("workspace.confirmedBody");
      } catch (error) {
        if (output) output.textContent = localizedSettingsErrorMessage(error, t("workspace.actionFailedTitle"));
      } finally {
        setSettingsWorkspaceBusy(root, false);
      }
    });
  });
  root.querySelectorAll("[data-settings-target]").forEach(button => {
    button.addEventListener("click", () => {
      const target = button.dataset.settingsTarget;
      const resourceKind = button.dataset.settingsResourceKind || "";
      if (target === "catalog" && resourceKind) {
        try {
          sessionStorage.setItem("goflow.catalog.focus", JSON.stringify({
            kind: resourceKind,
            name: button.dataset.settingsResourceName || "",
            field: button.dataset.settingsResourceField || "",
            source: "settings-diagnostics"
          }));
        } catch {
          // Ignore storage restrictions; the plain catalog navigation still works.
        }
      }
      if (target) location.hash = target;
    });
  });
  root.querySelectorAll("[data-settings-scroll]").forEach(button => {
    button.addEventListener("click", () => {
      const target = button.dataset.settingsScroll;
      const escapedTarget = window.CSS?.escape ? CSS.escape(target) : String(target || "").replaceAll('"', '\\"');
      const node = root.querySelector(`[data-settings-anchor="${escapedTarget}"]`);
      if (!node) return;
      node.focus({ preventScroll: true });
      node.scrollIntoView({ block: "start", behavior: prefersReducedMotion() ? "auto" : "smooth" });
    });
  });
  root.querySelectorAll("[data-settings-update-check]").forEach(button => {
    button.addEventListener("click", async () => {
      const output = root.querySelector("[data-settings-update-output]");
      const endpoint = button.dataset.settingsUpdateCheckPath || "/api/update-policy/check";
      setSettingsUpdateBusy(button, true);
      if (output) output.innerHTML = renderUpdateCheckLoading();
      try {
        const result = await postJSON(endpoint, {});
        if (output) output.innerHTML = renderUpdateCheckResult(result);
      } catch (error) {
        if (output) output.innerHTML = renderUpdateCheckError(error);
      } finally {
        setSettingsUpdateBusy(button, false);
      }
    });
  });
}

function setSettingsWorkspaceBusy(root, busy) {
  root.querySelectorAll("[data-settings-workspace-confirm]").forEach(button => {
    if (busy) {
      if (!button.dataset.settingsBusyPreviousDisabled) {
        button.dataset.settingsBusyPreviousDisabled = button.disabled ? "true" : "false";
      }
      button.disabled = true;
      button.setAttribute("aria-disabled", "true");
      button.setAttribute("aria-busy", "true");
      return;
    }
    if (!Object.prototype.hasOwnProperty.call(button.dataset, "settingsBusyPreviousDisabled")) {
      button.setAttribute("aria-disabled", button.disabled ? "true" : "false");
      button.setAttribute("aria-busy", "false");
      return;
    }
    const wasDisabled = button.dataset.settingsBusyPreviousDisabled === "true";
    delete button.dataset.settingsBusyPreviousDisabled;
    button.disabled = wasDisabled;
    button.setAttribute("aria-disabled", wasDisabled ? "true" : "false");
    button.setAttribute("aria-busy", "false");
  });
}

function settingsWorkspaceActionRequest(path, method = "POST") {
  return request(path, { method: String(method || "POST").toUpperCase() });
}

function prefersReducedMotion() {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
}

function focusSettingsTarget(root) {
  let target = "";
  try {
    target = sessionStorage.getItem("goflow.settings.focus") || "";
    sessionStorage.removeItem("goflow.settings.focus");
  } catch {
    target = "";
  }
  if (target !== "config-health") return;
  const node = root.querySelector("[data-settings-focus='config-health']");
  if (!node) return;
  requestAnimationFrame(() => {
    const reduceMotion = prefersReducedMotion();
    node.focus({ preventScroll: true });
    node.scrollIntoView({ block: "start", behavior: reduceMotion ? "auto" : "smooth" });
    node.classList.add("settings-focus-pulse");
    window.setTimeout(() => node.classList.remove("settings-focus-pulse"), reduceMotion ? 700 : 1600);
  });
}

function configDiagnosticCapabilities(discovery) {
  const raw = discovery?.capabilities && typeof discovery.capabilities === "object" ? discovery.capabilities : {};
  const discoveryUnavailable = Boolean(discovery?.__error);
  const supported = key => discoveryUnavailable ? true : raw[key] === true;
  return {
    discoveryAvailable: !discoveryUnavailable,
    discoverySource: discovery?.discovery_source || "",
    fieldTargets: supported("config_diagnostics_field_targets"),
    severityCounts: supported("config_diagnostics_severity_counts"),
    recommendations: supported("config_diagnostics_recommendations"),
    mcpHardening: supported("config_diagnostics_mcp_hardening"),
    loadFailureFieldTargets: supported("config_load_failure_field_targets")
  };
}

function renderSettingsHero(diagnostics, runtime) {
  const status = diagnostics?.status || (diagnostics?.__error ? "error" : "unknown");
  const tone = statusTone(status, diagnostics?.restart_required);
  const title = diagnostics?.restart_required
    ? t("settings.heroRestartTitle")
    : status === "error"
      ? t("settings.heroErrorTitle")
      : status === "warning"
        ? t("settings.heroWarningTitle")
        : t("settings.heroReadyTitle");
  const body = diagnostics?.__error
    ? t("settings.diagnosticsLoadFailed", { message: settingsDisplayText(diagnostics.__error) })
    : diagnostics?.restart_required
      ? t("settings.heroRestartBody")
      : status === "error"
        ? t("settings.heroErrorBody")
        : status === "warning"
          ? t("settings.heroWarningBody")
          : t("settings.heroReadyBody");
  const paths = [
    [t("settings.configPath"), diagnostics?.config_path],
    [t("settings.runtimeHome"), diagnostics?.runtime_home || runtime?.runtime_home],
    [t("settings.workspaceRoot"), diagnostics?.workspace_root || runtime?.workspace?.display]
  ].filter(([, value]) => String(value || "").trim());

  return `<section class="panel span-12 settings-hero settings-hero-${tone}">
    <div class="settings-hero-copy">
      <span class="badge ${tone}">${statusLabel(status, diagnostics?.restart_required)}</span>
      <h2>${escapeHTML(title)}</h2>
      <p>${escapeHTML(body)}</p>
    </div>
    <div class="settings-path-stack">
      ${paths.map(([label, value]) => `<div class="settings-path-row"><span>${escapeHTML(label)}</span><strong title="${escapeHTML(value)}">${escapeHTML(value)}</strong></div>`).join("") || `<div class="settings-path-row"><span>${t("settings.configPath")}</span><strong>${t("common.none")}</strong></div>`}
    </div>
  </section>`;
}

function renderSummaryGrid(diagnostics, capabilities = {}) {
  const summary = diagnostics?.summary || {};
  const counts = diagnosticCounts(diagnostics);
  const countItems = capabilities.severityCounts === false ? [] : [
    ["diagnostic-error", "settings.summary.diagnosticErrors", counts.errors],
    ["diagnostic-warning", "settings.summary.diagnosticWarnings", counts.warnings],
    ["diagnostic-total", "settings.summary.diagnosticTotal", counts.total]
  ];
  const items = [
    ...countItems,
    ["providers", "settings.summary.providers", summary.providers],
    ["agents", "settings.summary.agents", summary.agents],
    ["mcp", "settings.summary.mcpServers", summary.mcp_servers],
    ["skills", "settings.summary.skills", summary.skills],
    ["workflows", "settings.summary.workflows", summary.workflows],
    ["policies", "settings.summary.policyRules", summary.policy_rules]
  ];
  return `<div class="settings-summary-grid">
    ${items.map(([kind, label, value]) => `<div class="settings-summary-card ${kind}">
      <span>${t(label)}</span>
      <strong>${numberText(value)}</strong>
    </div>`).join("")}
  </div>`;
}

function renderDiagnosticCapabilities(capabilities) {
  const facts = [
    ["severityCounts", "settings.diagnosticCapabilitySeverityCounts"],
    ["fieldTargets", "settings.diagnosticCapabilityFieldTargets"],
    ["recommendations", "settings.diagnosticCapabilityRecommendations"],
    ["mcpHardening", "settings.diagnosticCapabilityMcpHardening"],
    ["loadFailureFieldTargets", "settings.diagnosticCapabilityLoadFailureTargets"]
  ];
  return `<div class="settings-diagnostic-capabilities" aria-label="${escapeHTML(t("settings.diagnosticCapabilitiesTitle"))}">
    <div>
      <strong>${t("settings.diagnosticCapabilitiesTitle")}</strong>
      <span>${t(capabilities.discoveryAvailable ? "settings.diagnosticCapabilitiesHelp" : "settings.diagnosticCapabilitiesFallbackHelp")}</span>
    </div>
    <div class="settings-diagnostic-capability-list">
      ${facts.map(([key, label]) => `<span class="${capabilities[key] ? "enabled" : "muted"}">
        ${t(label)}
      </span>`).join("")}
    </div>
  </div>`;
}

function renderPrioritySummary(diagnostics, runtime, capabilities) {
  const actions = buildPriorityActions(diagnostics, runtime, capabilities);
  const attentionCount = actions.filter(action => action.tone !== "good").length;
  return `<div class="settings-priority-panel">
    <div class="settings-section-label">
      <strong>${t("settings.priorityTitle")}</strong>
      <span>${t(attentionCount ? "settings.priorityActionCount" : "settings.priorityReadyCount", { count: attentionCount })}</span>
    </div>
    <div class="settings-priority-list">
      ${actions.map((action, index) => renderPriorityAction(action, index)).join("")}
    </div>
  </div>`;
}

function renderPriorityAction(action, index) {
  const attrs = settingsActionAttrs(action);
  return `<article class="settings-priority-card ${escapeHTML(action.tone || "neutral")}">
    <span class="settings-priority-rank">${index + 1}</span>
    <div class="settings-priority-copy">
      <div class="settings-priority-head">
        <strong>${escapeHTML(action.title)}</strong>
        ${action.meta ? `<span>${escapeHTML(action.meta)}</span>` : ""}
      </div>
      <p>${escapeHTML(action.body)}</p>
      ${action.path ? `<small>${t("settings.pathLabel")} ${escapeHTML(action.path)}</small>` : ""}
    </div>
    ${action.cta ? `<button type="button" class="settings-priority-action" ${attrs}>${escapeHTML(action.cta)}</button>` : ""}
  </article>`;
}

function settingsActionAttrs(action = {}) {
  const attrs = [];
  if (action.target) attrs.push(`data-settings-target="${escapeHTML(action.target)}"`);
  if (action.scroll) attrs.push(`data-settings-scroll="${escapeHTML(action.scroll)}"`);
  if (action.resourceFocus?.kind) attrs.push(`data-settings-resource-kind="${escapeHTML(action.resourceFocus.kind)}"`);
  if (action.resourceFocus?.name) attrs.push(`data-settings-resource-name="${escapeHTML(action.resourceFocus.name)}"`);
  if (action.resourceFocus?.field) attrs.push(`data-settings-resource-field="${escapeHTML(action.resourceFocus.field)}"`);
  return attrs.join(" ");
}

function buildPriorityActions(diagnostics, runtime, capabilities) {
  const actions = [];
  const items = Array.isArray(diagnostics?.items) ? prioritizeDiagnostics(diagnostics.items) : [];
  const actionable = items.filter(isActionableDiagnostic);
  const topError = actionable.find(item => String(item.severity || "").toLowerCase() === "error");
  const topWarning = actionable.find(item => String(item.severity || "").toLowerCase() === "warning" && item.code !== "restart_required");
  const requiredEnv = (Array.isArray(runtime?.setup?.env) ? runtime.setup.env : []).filter(item => item.required && !item.set);
  const summary = diagnostics?.summary || {};

  if (diagnostics?.__error) {
    actions.push({
      tone: "bad",
      title: t("settings.priorityDiagnosticsUnavailableTitle"),
      body: t("settings.priorityDiagnosticsUnavailableBody", { message: settingsDisplayText(diagnostics.__error) }),
      meta: t("settings.statusUnknown"),
      scroll: "config-health",
      cta: t("settings.priorityInspectDiagnostics")
    });
  }
  if (diagnostics?.restart_required) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityRestartTitle"),
      body: t("settings.priorityRestartBody"),
      meta: t("settings.restartRequired"),
      scroll: "config-health",
      cta: t("settings.priorityInspectDiagnostics")
    });
  }
  if (topError) {
    actions.push(diagnosticPriorityAction(topError, "bad", t("settings.priorityFixErrorTitle"), capabilities));
  }
  if (requiredEnv.length) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityEnvTitle"),
      body: t("settings.priorityEnvBody", { name: requiredEnv[0].name || t("common.required") }),
      meta: t("common.required"),
      scroll: "first-run",
      cta: t("settings.priorityOpenEnvChecks")
    });
  }
  if (!runtime?.workspace?.confirmed) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityWorkspaceTitle"),
      body: t("settings.priorityWorkspaceBody"),
      meta: t("runtime.workspace"),
      target: "workspace",
      cta: t("settings.priorityOpenWorkspace")
    });
  }
  if (!Number(summary.providers || 0)) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityProviderTitle"),
      body: t("settings.priorityProviderBody"),
      meta: t("settings.summary.providers"),
      target: "catalog",
      cta: t("settings.priorityOpenResources")
    });
  }
  if (!Number(summary.agents || 0)) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityAgentTitle"),
      body: t("settings.priorityAgentBody"),
      meta: t("settings.summary.agents"),
      target: "catalog",
      cta: t("settings.priorityOpenResources")
    });
  }
  if (topWarning) {
    actions.push(diagnosticPriorityAction(topWarning, "warn", t("settings.priorityReviewWarningTitle"), capabilities));
  }
  if (!Number(summary.workflows || 0)) {
    actions.push({
      tone: "info",
      title: t("settings.priorityWorkflowTitle"),
      body: t("settings.priorityWorkflowBody"),
      meta: t("settings.summary.workflows"),
      target: "workflows",
      cta: t("settings.priorityOpenWorkflowStudio")
    });
  }
  if (!actions.length) {
    actions.push({
      tone: "good",
      title: t("settings.priorityReadyTitle"),
      body: t("settings.priorityReadyBody"),
      meta: t("settings.statusOk"),
      target: "playground",
      cta: t("settings.priorityOpenRun")
    });
  }
  return dedupePriorityActions(actions).slice(0, 3);
}

function diagnosticPriorityAction(item, tone, title, capabilities) {
  const action = diagnosticItemAction(item);
  const recommendation = capabilities?.recommendations ? diagnosticRecommendation(item) : "";
  return {
    tone,
    title,
    body: recommendation || diagnosticMessage(item),
    meta: diagnosticMetaLabel(item) || item.code || item.severity || "",
    path: item.path,
    ...action
  };
}

function diagnosticAction(item) {
  const code = String(item?.code || "");
  const targetKind = String(item?.target_kind || "").toLowerCase();
  const resourceKind = diagnosticResourceKind(targetKind);
  if (resourceKind) {
    return {
      target: "catalog",
      cta: t("settings.priorityOpenResources"),
      resourceFocus: {
        kind: resourceKind,
        name: String(item?.target_name || item?.name || "").trim(),
        field: String(item?.field || item?.target_field || "").trim()
      }
    };
  }
  if (targetKind.includes("workflow") || code.includes("workflow")) {
    return { target: "workflows", cta: t("settings.priorityOpenWorkflowStudio") };
  }
  if (code.includes("provider") || code === "runtime_missing") {
    return { scroll: "first-run", cta: t("settings.priorityOpenEnvChecks") };
  }
  return { scroll: "config-health", cta: t("settings.priorityInspectDiagnostics") };
}

function diagnosticResourceKind(kind) {
  const normalized = String(kind || "").replaceAll("-", "_").toLowerCase();
  return {
    provider: "provider",
    agent: "agent",
    skill: "skill",
    tool: "tool",
    mcp: "tool",
    mcp_server: "tool",
    mcp_tool: "tool",
    policy_rule: "policy-rule",
    workflow_template: "workflow-template",
    team_template: "team-template",
    kit: "kit",
    expression_helper: "expression-helper",
    workflow_node_metadata: "node-metadata"
  }[normalized] || "";
}

function dedupePriorityActions(actions) {
  const seen = new Set();
  return actions.filter(action => {
    const key = [action.title, action.meta, action.target || "", action.scroll || ""].join("|");
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function renderDiagnosticsList(diagnostics, capabilities) {
  if (diagnostics?.__error) {
    return `<div class="settings-diagnostic-list">
      <div class="settings-diagnostic-item error">
        <span class="settings-diagnostic-dot" aria-hidden="true"></span>
        <div><strong>${t("settings.diagnosticsUnavailable")}</strong><p>${escapeHTML(localizedText(diagnostics.__error))}</p></div>
      </div>
    </div>`;
  }
  const items = Array.isArray(diagnostics?.items) ? diagnostics.items : [];
  const attentionItems = items.filter(item => !isOptionalDiagnosticInfo(item));
  const securityItems = attentionItems.filter(isSecurityGuidanceDiagnostic);
  const primaryItems = attentionItems.filter(item => !isSecurityGuidanceDiagnostic(item));
  const optionalItems = items.filter(isOptionalDiagnosticInfo);
  const visible = prioritizeDiagnostics(primaryItems).slice(0, 10);
  return `<div class="settings-diagnostic-section">
    <div class="settings-section-label">
      <strong>${t("settings.diagnosticItems")}</strong>
      <span>${t("settings.diagnosticItemsHelp")}</span>
    </div>
    <div class="settings-diagnostic-list">
      ${visible.length ? visible.map(item => renderDiagnosticItem(item, capabilities)).join("") : `<div class="settings-empty">${securityItems.length ? t("settings.noBlockingDiagnostics") : t("settings.noDiagnostics")}</div>`}
    </div>
    ${renderSecurityDiagnostics(securityItems, capabilities)}
    ${renderOptionalDiagnostics(optionalItems, capabilities)}
  </div>`;
}

function renderSecurityDiagnostics(items, capabilities = {}) {
  if (!items.length) return "";
  const visible = prioritizeDiagnostics(items).slice(0, 32);
  return `<details class="settings-diagnostic-optional settings-diagnostic-security">
    <summary>
      <span>${t("settings.diagnosticSecurityTitle", { count: items.length })}</span>
      <small>${t("settings.diagnosticSecurityHelp")}</small>
    </summary>
    <div class="settings-diagnostic-list">
      ${visible.map(item => renderDiagnosticItem(item, capabilities)).join("")}
    </div>
  </details>`;
}

function renderOptionalDiagnostics(items, capabilities = {}) {
  if (!items.length) return "";
  const visible = prioritizeDiagnostics(items).slice(0, 24);
  return `<details class="settings-diagnostic-optional">
    <summary>
      <span>${t("settings.diagnosticOptionalTitle", { count: items.length })}</span>
      <small>${t("settings.diagnosticOptionalHelp")}</small>
    </summary>
    <div class="settings-diagnostic-list">
      ${visible.map(item => renderDiagnosticItem(item, capabilities)).join("")}
    </div>
  </details>`;
}

function renderDiagnosticItem(item = {}, capabilities = {}) {
  const severity = String(item.severity || "info").toLowerCase();
  const tone = severity === "error" ? "error" : severity === "warning" ? "warning" : "info";
  const localized = diagnosticMessage(item);
  const original = item.message && localized !== item.message ? item.message : "";
  const originalHTML = original && (severity === "error" || severity === "warning")
    ? diagnosticOriginalHTML(original)
    : "";
  const target = capabilities.fieldTargets ? diagnosticTargetHTML(item) : "";
  const details = diagnosticDetailsHTML(item);
  const recommendation = capabilities.recommendations ? diagnosticRecommendation(item, localized) : "";
  const action = diagnosticItemAction(item);
  const actionHTML = diagnosticItemActionHTML(action);
  const badge = diagnosticBadgeLabel(item);
  return `<article class="settings-diagnostic-item ${tone}">
    <span class="settings-diagnostic-dot" aria-hidden="true"></span>
    <div>
      <div class="settings-diagnostic-head">
        <strong>${escapeHTML(localized)}</strong>
        <span title="${escapeHTML(item.code || severity)}">${escapeHTML(badge)}</span>
      </div>
      ${originalHTML}
      ${target}
      ${details}
      ${recommendation ? `<div class="settings-diagnostic-recommendation">
        <small>${t("settings.diagnosticRecommendation")}</small>
        <p>${escapeHTML(recommendation)}</p>
      </div>` : ""}
      ${item.path ? `<small>${t("settings.pathLabel")} ${escapeHTML(item.path)}</small>` : ""}
      ${actionHTML}
    </div>
  </article>`;
}

function diagnosticBadgeLabel(item = {}) {
  if (isOptionalDiagnosticInfo(item)) return t("settings.diagnosticOptionalBadge");
  if (isActionableDiagnostic(item)) return t("settings.diagnosticActionableBadge");
  const severity = String(item.severity || "info").toLowerCase();
  const key = `settings.diagnosticSeverity.${severity}`;
  const translated = t(key);
  return translated === key ? localizedText(severity) : translated;
}

function diagnosticOriginalHTML(original) {
  return `<details class="settings-diagnostic-original">
    <summary>${t("settings.diagnosticOriginalSummary")}</summary>
    <div class="settings-diagnostic-original-body">
      <span>${t("settings.diagnosticOriginalHelp")}</span>
      <p>${escapeHTML(original)}</p>
    </div>
  </details>`;
}

function diagnosticItemActionHTML(action = {}) {
  if (!action.target && !action.scroll) return "";
  const label = action.target === "catalog" && action.resourceFocus?.field
    ? t("settings.diagnosticOpenField")
    : action.cta || t("settings.diagnosticOpenTarget");
  return `<div class="settings-diagnostic-actions">
    <button type="button" ${settingsActionAttrs(action)}>${escapeHTML(localizedText(label))}</button>
  </div>`;
}

function diagnosticItemAction(item = {}) {
  if (!isActionableDiagnostic(item)) return {};
  const action = diagnosticAction(item);
  if (action.target || action.resourceFocus?.kind) return action;
  if (action.scroll === "first-run") return action;
  return {};
}

function diagnosticTargetHTML(item = {}) {
  const chips = [];
  const target = diagnosticMetaLabel(item);
  if (target) chips.push([t("settings.diagnosticTarget"), target]);
  const field = item.field || item.target_field || "";
  if (field) chips.push([t("settings.diagnosticField"), diagnosticFieldLabel(field)]);
  if (!chips.length) return "";
  return `<div class="settings-diagnostic-meta">${chips.map(([label, value]) => `
    <span><small>${escapeHTML(label)}</small><strong>${escapeHTML(localizedText(value))}</strong></span>`).join("")}</div>`;
}

function diagnosticDetailsHTML(item = {}) {
  const details = item?.details || item?.detail || {};
  if (!details || typeof details !== "object" || Array.isArray(details)) return "";
  const chips = [
    details.image ? [t("catalog.isolationImage"), details.image] : null,
    details.image_reference_type ? [t("catalog.containerImageReference"), diagnosticContainerImageReferenceLabel(details.image_reference_type)] : null,
    typeof details.digest_pinned === "boolean" ? [t("catalog.containerDigestState"), details.digest_pinned ? t("catalog.containerDigestPinned") : t("catalog.containerDigestNotPinned")] : null,
    details.pull_policy ? [t("catalog.isolationPullPolicy"), diagnosticContainerPullPolicyLabel(details.pull_policy)] : null,
    details.isolation_profile ? [t("catalog.isolationProfile"), diagnosticIsolationProfileLabel(details.isolation_profile)] : null
  ].filter(Boolean);
  if (!chips.length) return "";
  return `<div class="settings-diagnostic-meta settings-diagnostic-details">${chips.map(([label, value]) => `
    <span><small>${escapeHTML(label)}</small><strong>${escapeHTML(localizedText(value))}</strong></span>`).join("")}</div>`;
}

function diagnosticMetaLabel(item = {}) {
  const targetName = diagnosticDisplayValue(item.target_name || item.name || "");
  const targetKind = diagnosticTargetKindLabel(item.target_kind);
  if (targetKind && targetName) return `${targetKind}: ${targetName}`;
  return targetName || targetKind;
}

function diagnosticTargetKindLabel(kind) {
  const raw = String(kind || "").trim();
  if (!raw) return "";
  const normalized = raw.replaceAll("-", "_").toLowerCase();
  const key = `settings.targetKind.${normalized}`;
  const translated = t(key);
  if (translated !== key) return translated;
  return localizedText(raw.replaceAll("_", " "));
}

function diagnosticFieldLabel(field) {
  const raw = String(field || "").trim();
  const normalized = raw.replaceAll("-", "_").toLowerCase();
  const labels = {
    name: t("catalog.name"),
    id: "ID",
    description: t("catalog.description"),
    title: t("catalog.title"),
    provider: t("catalog.provider"),
    model: t("catalog.model"),
    api_key: t("catalog.providerEnvKey"),
    env_key: t("catalog.providerEnvKey"),
    fallback_provider: t("settings.field.fallbackProvider"),
    default_model: t("catalog.providerDefaultModel"),
    base_url: t("catalog.providerBaseURL"),
    tool_policy: t("settings.field.toolPolicy"),
    allowed_tools: t("settings.field.allowedTools"),
    allowed_tool_kinds: t("settings.field.allowedToolKinds"),
    max_iterations: t("settings.field.maxIterations"),
    system_prompt: t("settings.field.systemPrompt"),
    command: t("settings.field.command"),
    args: t("settings.field.args"),
    workdir: t("settings.field.workdir"),
    env_allowlist: t("settings.field.envAllowlist"),
    allowed_commands: t("settings.field.allowedCommands"),
    allowed_command_paths: t("settings.field.allowedCommandPaths"),
    network_disabled: t("settings.field.networkDisabled"),
    isolation: t("catalog.toolIsolation"),
    isolation_profile: t("settings.field.isolationProfile"),
    isolation_options: t("settings.field.isolationOptions"),
    "isolation_options.image": t("settings.field.containerImage"),
    "isolation_options.runtime": t("settings.field.containerRuntime"),
    "isolation_options.pull_policy": t("settings.field.containerPullPolicy"),
    "isolation_options.workspace_mount": t("settings.field.workspaceMount"),
    "isolation_options.workspace_target": t("settings.field.workspaceTarget"),
    "isolation_options.container_workdir": t("settings.field.containerWorkdir"),
    "isolation_options.network": t("settings.field.containerNetwork"),
    "isolation_options.memory": t("settings.field.containerMemory"),
    "isolation_options.memory_swap": t("settings.field.containerMemorySwap"),
    "isolation_options.cpus": t("settings.field.containerCpus"),
    "isolation_options.pids_limit": t("settings.field.containerPids"),
    "isolation_options.readonly_rootfs": t("settings.field.readonlyRootfs"),
    "isolation_options.no_new_privileges": t("settings.field.noNewPrivileges"),
    "isolation_options.cap_drop": t("settings.field.capDrop"),
    "isolation_options.user": t("settings.field.containerUser"),
    "isolation_options.userns": t("settings.field.userNamespace"),
    "isolation_options.tmpfs": t("settings.field.containerTmpfs"),
    "isolation_options.init": t("settings.field.containerInit"),
    "isolation_options.tool_source": t("settings.field.containerToolSource"),
    "isolation_options.tool_target": t("settings.field.containerToolTarget"),
    "isolation_options.tool_mount": t("settings.field.containerToolMount"),
    workflow: t("workflow.workflow"),
    stages: t("workflow.stages"),
    node_type: t("workflow.nodeType"),
    skill: t("workflow.skill"),
    agent: t("workflow.agent"),
    tool: t("workflow.toolMetadata"),
    policy_rule: t("catalog.resourcePolicyRule"),
    params: t("workflow.parameters"),
    artifacts: t("workflow.artifacts"),
    outputs: t("workflow.outputsMap")
  };
  if (labels[normalized]) return labels[normalized];
  if (normalized.includes(".")) {
    return normalized.split(".").map(part => labels[part] || localizedText(part)).join(" / ");
  }
  return localizedText(raw.replaceAll("_", " "));
}

function diagnosticContainerImageReferenceLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.containerImageRef.${normalized}`);
  return translated === `catalog.containerImageRef.${normalized}` ? localizedText(value) : translated;
}

function diagnosticContainerPullPolicyLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.pullPolicy.${normalized}`);
  return translated === `catalog.pullPolicy.${normalized}` ? localizedText(value) : translated;
}

function diagnosticIsolationProfileLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.isolationProfile.${normalized}`);
  return translated === `catalog.isolationProfile.${normalized}` ? localizedText(value) : translated;
}

function renderEnvChecks(runtime) {
  const env = Array.isArray(runtime?.setup?.env) ? runtime.setup.env : [];
  const workspaceCard = renderWorkspaceReadiness(runtime);
  const envHTML = env.length
    ? env.map(item => `<article class="settings-check-item ${item.set ? "good" : item.required ? "warning" : "neutral"}">
        <div>
          <strong>${escapeHTML(item.name)}</strong>
          <p>${escapeHTML(localizedText(item.description || ""))}</p>
        </div>
        <span class="badge ${item.set ? "good" : item.required ? "warn" : "neutral"}">${item.set ? t("common.set") : item.required ? t("common.required") : t("common.optional")}</span>
      </article>`).join("")
    : `<div class="settings-empty">${t("settings.noEnvChecks")}</div>`;
  return `<div class="settings-check-list">
    ${workspaceCard}
    ${envHTML}
  </div>`;
}

function renderWorkspaceReadiness(runtime = {}) {
  const workspace = runtime.workspace || {};
  const capabilities = runtime.workspace_capabilities || workspace.capabilities || {};
  const actions = workspaceActionsFromRuntime(runtime, workspace, capabilities);
  const confirmed = Boolean(workspace.confirmed);
  const confirmAction = actions.find(action => action?.name === "confirm");
  const clearAction = actions.find(action => action?.name === "clear");
  const canConfirm = confirmAction ? confirmAction.available !== false : Boolean(workspace.root && !confirmed);
  const canClear = clearAction ? clearAction.available !== false : Boolean(workspace.root && confirmed);
  const facts = [
    [t("settings.workspaceReadinessRoot"), workspace.display || workspace.root || t("common.none")],
    [t("settings.workspaceReadinessFileTasks"), capabilities.workspace_required_for_file_tasks ? t("settings.workspaceReadinessRequiresConfirm") : t("settings.workspaceReadinessNotRequired")],
    [t("settings.workspaceReadinessSwitch"), capabilities.switch_requires_restart ? t("settings.workspaceReadinessRestart") : t("settings.workspaceReadinessHotSwitch")]
  ];
  return `<article class="settings-workspace-readiness ${confirmed ? "good" : "warning"}">
    <div class="settings-workspace-readiness-head">
      <div>
        <strong>${escapeHTML(t("settings.workspaceReadinessTitle"))}</strong>
        <p>${escapeHTML(t(confirmed ? "settings.workspaceReadinessReadyBody" : "settings.workspaceReadinessBlockedBody"))}</p>
      </div>
      <span class="badge ${confirmed ? "good" : "warn"}">${confirmed ? t("workspace.confirmed") : t("workspace.needsConfirmation")}</span>
    </div>
    <div class="settings-workspace-readiness-facts">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong title="${escapeHTML(value)}">${escapeHTML(localizedText(value))}</strong></span>`).join("")}
    </div>
    <div class="settings-workspace-readiness-actions">
      ${!confirmed && canConfirm ? `<button type="button" data-settings-workspace-confirm data-settings-workspace-confirm-path="${escapeHTML(confirmAction?.path || "/api/workspace/confirm")}" data-settings-workspace-confirm-method="${escapeHTML(confirmAction?.method || "POST")}">${escapeHTML(t("settings.workspaceReadinessConfirm"))}</button>` : ""}
      ${confirmed && canClear ? `<button type="button" data-settings-target="workspace">${escapeHTML(t("settings.workspaceReadinessManage"))}</button>` : ""}
      <button type="button" data-settings-target="workspace">${escapeHTML(t("settings.priorityOpenWorkspace"))}</button>
    </div>
    <div class="settings-workspace-readiness-output" data-settings-workspace-output role="status" aria-live="polite"></div>
  </article>`;
}

function workspaceActionsFromRuntime(runtime = {}, workspace = {}, capabilities = {}) {
  const sources = [
    capabilities.actions,
    runtime.workspace_actions,
    workspace.actions
  ];
  for (const source of sources) {
    if (Array.isArray(source) && source.length) return source;
  }
  return [];
}

function renderModules(diagnostics) {
  if (diagnostics?.__error) return `<div class="settings-empty">${t("settings.modulesUnavailable")}</div>`;
  const modules = Array.isArray(diagnostics?.modules) ? diagnostics.modules : [];
  if (!modules.length) return `<div class="settings-empty">${t("settings.noModules")}</div>`;
  return `<div class="settings-module-list">
    ${modules.map(module => {
      const files = Array.isArray(module.files) ? module.files : [];
      const optional = isOptionalModule(module, diagnostics);
      return `<article class="settings-module-card">
        <div class="settings-module-head">
          <div>
            <strong>${moduleLabel(module.kind)}</strong>
            <small title="${escapeHTML(module.root || "")}">${escapeHTML(module.root || t("common.none"))}</small>
          </div>
          <span class="badge ${files.length ? "info" : optional ? "neutral" : "warn"}">${files.length ? t("settings.fileCount", { count: files.length }) : optional ? t("settings.moduleOptionalEmpty") : t("settings.fileCount", { count: 0 })}</span>
        </div>
        <div class="settings-module-files">
          ${files.length ? files.slice(0, 4).map(file => `<span title="${escapeHTML(file)}">${escapeHTML(shortPath(file))}</span>`).join("") : `<span>${optional ? t("settings.noOptionalModuleFiles") : t("settings.noModuleFiles")}</span>`}
          ${files.length > 4 ? `<span>${t("settings.moreFiles", { count: files.length - 4 })}</span>` : ""}
        </div>
      </article>`;
    }).join("")}
  </div>`;
}

function isOptionalModule(module = {}, diagnostics = {}) {
  const root = String(module.root || "").trim();
  const kind = String(module.kind || "").trim();
  const items = Array.isArray(diagnostics?.items) ? diagnostics.items : [];
  return items.some(item => {
    if (!isOptionalDiagnosticInfo(item)) return false;
    const code = String(item.code || "");
    if (code && code !== "module_dir_empty") return false;
    const path = String(item.path || item.target_name || item.name || "").trim();
    return (root && path && samePathText(root, path)) || (kind && String(item.target_kind || "").includes(kind));
  });
}

function renderTourPath() {
  const steps = [
    ["settings.tourWorkspace", "settings.tourWorkspaceHelp"],
    ["settings.tourCatalog", "settings.tourCatalogHelp"],
    ["settings.tourWorkflow", "settings.tourWorkflowHelp"],
    ["settings.tourPlayground", "settings.tourPlaygroundHelp"],
    ["settings.tourApprovals", "settings.tourApprovalsHelp"],
    ["settings.tourObservability", "settings.tourObservabilityHelp"]
  ];
  return `<div class="settings-tour-path">
    ${steps.map(([title, body], index) => `<div class="settings-tour-step">
      <span>${index + 1}</span>
      <div><strong>${t(title)}</strong><p>${t(body)}</p></div>
    </div>`).join("")}
  </div>`;
}

function renderUpdatePolicy(update) {
  if (update?.__error) {
    return `<div class="settings-empty">${t("settings.updateUnavailable", { message: update.__error })}</div>`;
  }
  const strategies = Array.isArray(update?.strategies) ? update.strategies : [];
  const notes = Array.isArray(update?.notes) ? update.notes : [];
  const endpoint = update?.check_endpoint || "/api/update-policy/check";
  const checkEnabled = update?.check_enabled !== false;
  return `<div class="settings-update-layout">
    <div class="settings-update-meta">
      ${updateMeta(t("settings.repository"), update?.repository)}
      ${updateMeta(t("settings.releaseFeed"), update?.release_feed)}
      ${updateMeta(t("settings.currentVersion"), update?.current_version)}
    </div>
    <div class="settings-update-check ${checkEnabled ? "" : "disabled"}">
      <div>
        <strong>${escapeHTML(t(checkEnabled ? "settings.updateCheckTitle" : "settings.updateCheckDisabledTitle"))}</strong>
        <p>${escapeHTML(checkEnabled ? t(update?.network_opt_in ? "settings.updateCheckOptInHelp" : "settings.updateCheckHelp") : updateCheckDisabledText(update))}</p>
      </div>
      ${checkEnabled ? `<button type="button" class="primary" data-settings-update-check data-settings-update-check-path="${escapeHTML(endpoint)}">${escapeHTML(t("settings.updateCheckButton"))}</button>` : `<span class="badge neutral">${escapeHTML(t("settings.updateCheckDisabledBadge"))}</span>`}
    </div>
    <div class="settings-update-check-output" data-settings-update-output role="status" aria-live="polite"></div>
    <div class="settings-update-strategies">
      ${strategies.length ? strategies.map(renderUpdateStrategy).join("") : `<div class="settings-empty settings-update-empty">${t("settings.noUpdateStrategies")}</div>`}
    </div>
    ${notes.length ? `<div class="settings-update-notes">
      <strong>${t("settings.updateNotes")}</strong>
      ${notes.map(note => `<p>${escapeHTML(localizedText(note))}</p>`).join("")}
    </div>` : ""}
  </div>`;
}

function updateCheckDisabledText(update = {}) {
  const reason = settingsDisplayText(update.disabled_reason || "");
  return reason || t("settings.updateCheckDisabledHelp");
}

function setSettingsUpdateBusy(button, busy) {
  if (!button) return;
  button.disabled = Boolean(busy);
  button.setAttribute("aria-disabled", busy ? "true" : "false");
  button.setAttribute("aria-busy", busy ? "true" : "false");
  button.textContent = busy ? t("settings.updateCheckChecking") : t("settings.updateCheckButton");
}

function renderUpdateCheckLoading() {
  return `<article class="settings-update-check-card neutral">
    <strong>${escapeHTML(t("settings.updateCheckChecking"))}</strong>
    <p>${escapeHTML(t("settings.updateCheckCheckingHelp"))}</p>
  </article>`;
}

function renderUpdateCheckResult(result = {}) {
  const available = Boolean(result.update_available);
  const tone = available ? "warn" : "good";
  const title = available ? t("settings.updateAvailableTitle") : t("settings.updateCurrentTitle");
  const assets = Array.isArray(result.assets) ? result.assets : [];
  const facts = [
    [t("settings.currentVersion"), result.current_version],
    [t("settings.latestVersion"), result.latest_version],
    [t("settings.checkedAt"), result.checked_at],
    [t("settings.releaseFeed"), result.release_feed]
  ].filter(([, value]) => String(value || "").trim());
  return `<article class="settings-update-check-card ${tone}">
    <div class="settings-update-check-head">
      <div>
        <strong>${escapeHTML(title)}</strong>
        <p>${escapeHTML(updateCheckMessage(result, available))}</p>
      </div>
      <span class="badge ${available ? "warn" : "good"}">${escapeHTML(available ? t("settings.updateAvailableBadge") : t("settings.updateCurrentBadge"))}</span>
    </div>
    <div class="settings-update-check-facts">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong title="${escapeHTML(value || "")}">${escapeHTML(value || t("common.none"))}</strong></span>`).join("")}
    </div>
    ${result.release_url ? `<a class="settings-update-release-link" href="${escapeHTML(result.release_url)}" target="_blank" rel="noreferrer">${escapeHTML(t("settings.openRelease"))}</a>` : ""}
    ${result.notes_preview ? `<p class="settings-update-notes-preview">${escapeHTML(result.notes_preview)}</p>` : ""}
    ${renderUpdateAssetSummary(result.asset_summary)}
    ${assets.length ? `<div class="settings-update-assets">
      <span>${escapeHTML(t("settings.releaseAssets"))}</span>
      ${assets.slice(0, 5).map(asset => `<small title="${escapeHTML(asset.url || "")}">${escapeHTML(asset.name || t("common.none"))}${asset.size ? ` · ${escapeHTML(formatBytes(asset.size))}` : ""}</small>`).join("")}
    </div>` : ""}
  </article>`;
}

function renderUpdateAssetSummary(summary = {}) {
  if (!summary || typeof summary !== "object" || !Number.isFinite(Number(summary.asset_count))) return "";
  const ready = Boolean(summary.verification_ready);
  const missing = Array.isArray(summary.missing) ? summary.missing.filter(Boolean) : [];
  const steps = Array.isArray(summary.verify_steps) ? summary.verify_steps.filter(Boolean) : [];
  const facts = [
    [t("settings.updateAssetPlatform"), summary.current_platform],
    [t("settings.updateAssetArchive"), summary.matching_archive || summary.expected_archive],
    [t("settings.updateAssetChecksums"), summary.has_checksums ? t("common.yes") : t("common.no")],
    [t("settings.updateAssetSBOM"), summary.has_sbom ? t("common.yes") : t("common.no")],
    [t("settings.updateAssetSignatures"), summary.sigstore_bundle_count]
  ].filter(([, value]) => String(value ?? "").trim());
  return `<div class="settings-update-integrity ${ready ? "good" : "warn"}">
    <div class="settings-update-integrity-head">
      <strong>${escapeHTML(t(ready ? "settings.updateIntegrityReady" : "settings.updateIntegrityMissing"))}</strong>
      <span class="badge ${ready ? "good" : "warn"}">${escapeHTML(ready ? t("settings.updateIntegrityReadyBadge") : t("settings.updateIntegrityMissingBadge"))}</span>
    </div>
    <div class="settings-update-integrity-facts">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong title="${escapeHTML(value)}">${escapeHTML(value)}</strong></span>`).join("")}
    </div>
    ${missing.length ? `<p>${escapeHTML(t("settings.updateIntegrityMissingList", { items: missing.join(", ") }))}</p>` : ""}
    ${steps.length ? `<div class="settings-update-verify">
      <span>${escapeHTML(t("settings.updateVerifySteps"))}</span>
      ${steps.map(step => `<code>${escapeHTML(step)}</code>`).join("")}
    </div>` : ""}
  </div>`;
}

function renderUpdateCheckError(error) {
  return `<article class="settings-update-check-card bad">
    <strong>${escapeHTML(t("settings.updateCheckFailedTitle"))}</strong>
    <p>${escapeHTML(localizedSettingsErrorMessage(error, t("settings.updateCheckFailedBody")))}</p>
  </article>`;
}

function updateCheckMessage(result, available) {
  if (result.message) return localizedText(result.message);
  if (available) return t("settings.updateAvailableBody", { version: result.latest_version || t("common.none") });
  return t("settings.updateCurrentBody");
}

function formatBytes(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number <= 0) return "";
  if (number < 1024) return `${number} B`;
  if (number < 1024 * 1024) return `${(number / 1024).toFixed(1)} KB`;
  return `${(number / (1024 * 1024)).toFixed(1)} MB`;
}

function renderUpdateStrategy(strategy = {}) {
  return `<article class="settings-update-card">
    <div class="settings-update-card-head">
      <strong>${escapeHTML(localizedText(strategy.install_type || t("settings.updateStrategy")))}</strong>
    </div>
    ${renderUpdateSteps(t("settings.promptFlow"), strategy.prompt_flow)}
    ${renderUpdateSteps(t("settings.autoUpdate"), strategy.auto_update)}
  </article>`;
}

function renderUpdateSteps(title, steps) {
  const list = Array.isArray(steps) ? steps : [];
  if (!list.length) return "";
  return `<div class="settings-update-steps">
    <span>${escapeHTML(title)}</span>
    ${list.map(step => `<p>${escapeHTML(localizedText(step))}</p>`).join("")}
  </div>`;
}

function updateMeta(label, value) {
  return `<div><span>${escapeHTML(label)}</span><strong title="${escapeHTML(value || "")}">${escapeHTML(value || t("common.none"))}</strong></div>`;
}

function statusBadge(status, restartRequired) {
  const tone = statusTone(status, restartRequired);
  return `<span class="badge ${tone}">${statusLabel(status, restartRequired)}</span>`;
}

function statusLabel(status, restartRequired) {
  if (restartRequired) return t("settings.restartRequired");
  switch (String(status || "").toLowerCase()) {
    case "ok": return t("settings.statusOk");
    case "warning": return t("settings.statusWarning");
    case "error": return t("settings.statusError");
    default: return t("settings.statusUnknown");
  }
}

function statusTone(status, restartRequired) {
  if (restartRequired) return "warn";
  switch (String(status || "").toLowerCase()) {
    case "ok": return "good";
    case "warning": return "warn";
    case "error": return "bad";
    default: return "neutral";
  }
}

function prioritizeDiagnostics(items) {
  const rank = { error: 0, warning: 1, info: 2 };
  return [...items].sort((a, b) => (rank[String(a.severity || "info").toLowerCase()] ?? 3) - (rank[String(b.severity || "info").toLowerCase()] ?? 3));
}

function isOptionalDiagnosticInfo(item = {}) {
  const severity = String(item.severity || "info").toLowerCase();
  const code = String(item.code || "");
  const category = String(item.category || "").toLowerCase();
  const optional = diagnosticFlag(item.optional);
  const explicitlyNonActionable = diagnosticFlag(item.actionable) === false;
  if (optional || category === "optional" || category === "optional_extension") return true;
  if (severity === "info" && explicitlyNonActionable) return true;
  return severity === "info" && (code === "module_dir_empty" || code === "config_load_ok");
}

function isSecurityGuidanceDiagnostic(item = {}) {
  const code = String(item.code || "").toLowerCase();
  if (!code) return false;
  if (code.startsWith("tool_risk_policy")) return true;
  if (code.startsWith("mcp_isolation") || code.startsWith("mcp_container")) return true;
  if (code.includes("_sandbox") || code.includes("sandboxed")) return true;
  const targetKind = String(item.target_kind || "").toLowerCase();
  return targetKind === "mcp_server" && String(item.field || "").toLowerCase().includes("isolation");
}

function isActionableDiagnostic(item = {}) {
  if (!item || item.code === "config_load_ok") return false;
  if (isOptionalDiagnosticInfo(item)) return false;
  if (isSecurityGuidanceDiagnostic(item)) return false;
  return diagnosticFlag(item.actionable) !== false;
}

function diagnosticFlag(value) {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true") return true;
    if (normalized === "false") return false;
  }
  return undefined;
}

function diagnosticMessage(item) {
  const key = `settings.diagnostic.${item.code || ""}`;
  const translated = t(key);
  if (translated !== key) return translated;
  const generated = generatedDiagnosticMessage(item);
  if (generated) return generated;
  const displayed = settingsDisplayText(item.message || "");
  if (displayed) return displayed;
  return settingsDisplayText(item.code || t("settings.diagnosticFallback"));
}

function diagnosticRecommendation(item, message = "") {
  const key = `settings.diagnosticRecommendation.${item?.code || ""}`;
  const translated = t(key);
  if (translated !== key) return translated;
  const generated = generatedDiagnosticRecommendation(item);
  if (generated && generated !== message) return generated;
  const raw = settingsDisplayText(item?.recommendation || item?.recommended_action || "");
  const text = String(raw || "").trim();
  if (text && text !== message && text !== item?.message) return text;
  return "";
}

function moduleLabel(kind) {
  const key = `settings.module.${kind || ""}`;
  const translated = t(key);
  return translated === key ? localizedText(String(kind || t("common.none")).replaceAll("_", " ")) : translated;
}

function diagnosticCounts(diagnostics) {
  const items = Array.isArray(diagnostics?.items) ? diagnostics.items : [];
  const computed = items.reduce((acc, item) => {
    const severity = String(item?.severity || "info").toLowerCase();
    const optional = isOptionalDiagnosticInfo(item);
    const actionable = isActionableDiagnostic(item);
    if (optional) acc.optional += 1;
    if (severity === "error" && actionable) acc.errors += 1;
    else if ((severity === "warning" || severity === "warn") && actionable) acc.warnings += 1;
    else acc.info += 1;
    acc.total += 1;
    return acc;
  }, { errors: 0, warnings: 0, info: 0, total: 0, optional: 0 });
  const source = diagnostics?.diagnostics || diagnostics?.diagnostic_counts || diagnostics?.counts || diagnostics?.summary?.diagnostics || {};
  const hasItems = items.length > 0;
  const errors = hasItems
    ? computed.errors
    : firstNumber(source.errors, source.error, source.error_count, diagnostics?.errors, diagnostics?.error_count, computed.errors);
  const warnings = hasItems
    ? computed.warnings
    : firstNumber(source.warnings, source.warning, source.warn, source.warning_count, diagnostics?.warnings, diagnostics?.warning_count, computed.warnings);
  const info = hasItems
    ? computed.info
    : firstNumber(source.info, source.infos, source.information, source.info_count, diagnostics?.info, diagnostics?.info_count, computed.info);
  return {
    errors,
    warnings,
    info,
    optional: computed.optional,
    attention: errors + warnings,
    total: firstNumber(source.total, source.count, source.diagnostics, diagnostics?.total, diagnostics?.count, diagnostics?.diagnostics, diagnostics?.diagnostics_count, diagnostics?.diagnostic_count, computed.total)
  };
}

function firstNumber(...values) {
  for (const value of values) {
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  return 0;
}

function numberText(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? number.toLocaleString() : "0";
}

function shortPath(path) {
  const parts = String(path || "").split(/[\\/]+/).filter(Boolean);
  if (parts.length <= 2) return parts.join("/");
  return `${parts.at(-2)}/${parts.at(-1)}`;
}

function samePathText(left, right) {
  const normalize = value => String(value || "").replaceAll("\\", "/").replace(/\/+$/, "").toLowerCase();
  return normalize(left) === normalize(right);
}

function diagnosticDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  return settingsDisplayText(text);
}

function settingsDisplayText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const translated = localizedText(text);
  if (translated !== text) return translated;
  if (settingsLooksTechnical(text)) return text;
  return translated;
}

function settingsLooksTechnical(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return true;
  if (text.includes("\\") || text.includes("://")) return true;
  if (text.includes("/") && !/\s/.test(text)) return true;
  if (text.includes("=") && !/\s/.test(text)) return true;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(text)) return true;
  return false;
}

function generatedDiagnosticMessage(item = {}) {
  if (currentLanguage() !== "zh") return "";
  const target = diagnosticMetaLabel(item);
  const field = diagnosticFieldLabel(item.field || item.target_field || "");
  const subject = [target, field].filter(Boolean).join(" / ");
  const codeText = diagnosticCodeLabel(item.code || "");
  if (subject && codeText) return `${subject} 需要检查：${codeText}。`;
  if (subject) return `${subject} 需要检查。`;
  if (codeText) return `有一项配置需要检查：${codeText}。`;
  return "";
}

function generatedDiagnosticRecommendation(item = {}) {
  if (currentLanguage() !== "zh") return "";
  const code = String(item?.code || "").toLowerCase();
  if (code.includes("restart")) return "按提示重启 GoFlow 后，再回到设置页确认诊断状态。";
  if (code.includes("provider")) return "打开资源页检查对应模型供应商的模型、密钥和备用配置。";
  if (code.includes("workflow")) return "打开工作流编排页或资源页，检查工作流引用的节点、工具和字段是否存在。";
  if (code.includes("tool") || code.includes("mcp")) return "打开资源页检查对应工具配置，重点确认隔离、命令、路径和审批策略。";
  if (code.includes("workspace")) return "打开工作区页确认当前根目录，并重新检查需要访问文件的操作。";
  if (code.includes("template") || code.includes("metadata") || code.includes("schema")) return "打开资源页检查对应扩展资源；可选扩展为空时通常不需要处理。";
  if (isActionableDiagnostic(item)) return "按上方定位信息打开对应页面，修正字段后重新查看诊断结果。";
  return "";
}

function diagnosticCodeLabel(code = "") {
  const normalized = String(code || "").trim().toLowerCase().replaceAll("-", "_");
  if (!normalized) return "";
  const words = normalized.split("_").filter(Boolean);
  const labels = {
    action: "操作",
    agent: "智能体",
    api: "API",
    command: "命令",
    config: "配置",
    container: "容器",
    default: "默认",
    diagnostics: "诊断",
    env: "环境变量",
    expression: "表达式",
    field: "字段",
    file: "文件",
    helper: "助手",
    isolation: "隔离",
    key: "密钥",
    load: "加载",
    mcp: "MCP",
    metadata: "元数据",
    missing: "缺失",
    model: "模型",
    network: "网络",
    node: "节点",
    policy: "策略",
    provider: "模型供应商",
    fallback: "备用",
    resource: "资源",
    restart: "重启",
    rule: "规则",
    schema: "结构",
    server: "服务",
    template: "模板",
    tool: "工具",
    unseen: "未发现",
    warning: "警告",
    workflow: "工作流",
    workspace: "工作区"
  };
  return words.map(word => labels[word] || localizedText(word)).filter(Boolean).join("");
}
