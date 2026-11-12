import { escapeHTML, postJSON, request } from "../api.js";
import { icon } from "../icons.js";
import { currentLanguage, localizedText, t } from "../i18n.js";

export async function renderSettings(root, runtime, refreshRuntime) {
  const [providerResources, workspaceVersions] = await Promise.all([
    loadOptional("/api/resources/providers"),
    loadOptional("/api/workspace-versions")
  ]);

  root.innerHTML = `
    <div class="settings-simple" data-settings-simple>
      <section class="settings-simple-section settings-model-section" data-settings-anchor="first-run" tabindex="-1">
        <header><h2 class="panel-title">${icon("sparkles")}<span>${escapeHTML(t("settings.simpleModelTitle"))}</span></h2></header>
        ${renderProviderSetup(runtime, providerResources)}
      </section>
      <section class="settings-simple-section settings-workspace-section" data-settings-anchor="workspace" tabindex="-1">
        <header><h2 class="panel-title">${icon("monitor")}<span>${escapeHTML(t("settings.simpleWorkspaceTitle"))}</span></h2></header>
        ${renderWorkspaceReadiness(runtime)}
        ${renderWorkspaceVersions(workspaceVersions)}
      </section>
      ${renderAppearanceSettings()}
    </div>`;
  bindSettingsActions(root, refreshRuntime, runtime);
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

function bindSettingsActions(root, refreshRuntime, runtime) {
  root.querySelectorAll("[data-settings-workspace-pick]").forEach(button => {
    button.addEventListener("click", async () => {
      const form = button.closest("[data-settings-workspace-switch]");
      const input = form?.querySelector("[data-settings-workspace-path]");
      const output = root.querySelector("[data-settings-workspace-output]");
      button.disabled = true;
      if (output) output.textContent = t("settings.workspacePickerOpening");
      try {
        const result = await postJSON("/api/workspace/pick-folder", { path: String(input?.value || "").trim() });
        if (result?.cancelled) {
          if (output) output.textContent = t("settings.workspacePickerCancelled");
          return;
        }
        const path = String(result?.normalized_path || result?.path || "").trim();
        if (!path) throw new Error(t("settings.workspacePickerNoPath"));
        if (input) input.value = path;
        form?.requestSubmit();
      } catch (error) {
        if (output) output.textContent = localizedSettingsErrorMessage(error, t("settings.workspacePickerFailed"));
      } finally {
        button.disabled = false;
      }
    });
  });
  root.querySelectorAll("[data-settings-workspace-switch]").forEach(form => {
    form.addEventListener("submit", async event => {
      event.preventDefault();
      const input = form.querySelector("[data-settings-workspace-path]");
      const button = form.querySelector("button[type='submit']");
      const output = root.querySelector("[data-settings-workspace-output]");
      const path = String(input?.value || "").trim();
      const current = String(form.dataset.currentWorkspace || "").trim();
      if (!path) {
        if (output) output.textContent = t("settings.workspaceSwitchPathRequired");
        input?.focus();
        return;
      }
      if (current && path.toLowerCase() !== current.toLowerCase() && !window.confirm(t("settings.workspaceSwitchConfirm", { path }))) return;
      if (button) button.disabled = true;
      if (output) output.textContent = t("settings.workspaceSwitching");
      try {
        await request("/api/workspace/select", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path })
        });
        const nextRuntime = typeof refreshRuntime === "function" ? await refreshRuntime() : runtime;
        await renderSettings(root, nextRuntime || runtime, refreshRuntime);
      } catch (error) {
        if (output) output.textContent = localizedSettingsErrorMessage(error, t("settings.workspaceSwitchFailed"));
      } finally {
        if (button) button.disabled = false;
      }
    });
  });
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
  root.querySelectorAll("[data-settings-workspace-clear]").forEach(button => {
    button.addEventListener("click", async () => {
      if (!window.confirm(t("settings.workspaceReadinessRevokeConfirm"))) return;
      const output = root.querySelector("[data-settings-workspace-output]");
      setSettingsWorkspaceBusy(root, true);
      if (output) output.textContent = t("workspace.clearingTitle");
      try {
        await settingsWorkspaceActionRequest(button.dataset.settingsWorkspaceClearPath || "/api/workspace/clear", "POST");
        const nextRuntime = typeof refreshRuntime === "function" ? await refreshRuntime() : null;
        if (nextRuntime) {
          await renderSettings(root, nextRuntime, refreshRuntime);
          return;
        }
        if (output) output.textContent = t("workspace.clearedBody");
      } catch (error) {
        if (output) output.textContent = localizedSettingsErrorMessage(error, t("workspace.actionFailedTitle"));
      } finally {
        setSettingsWorkspaceBusy(root, false);
      }
    });
  });
  root.querySelectorAll("[data-workspace-version-restore]").forEach(button => {
    button.addEventListener("click", async () => {
      if (!window.confirm(t("settings.workspaceVersionsRestoreConfirm"))) return;
      const output = root.querySelector("[data-workspace-versions-output]");
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      if (output) output.textContent = t("settings.workspaceVersionsRestoring");
      try {
        await postJSON(button.dataset.workspaceVersionRestore || "", {});
        const nextRuntime = typeof refreshRuntime === "function" ? await refreshRuntime() : runtime;
        await renderSettings(root, nextRuntime || runtime, refreshRuntime);
      } catch (error) {
        if (output) output.textContent = localizedSettingsErrorMessage(error, t("settings.workspaceVersionsRestoreFailed"));
      } finally {
        button.disabled = false;
        button.setAttribute("aria-busy", "false");
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
      const workspaceEntry = button.dataset.settingsWorkspaceEntry || "";
      if (target === "workspace" && workspaceEntry === "open") {
        root.querySelector(".settings-workspace-root")?.focus({ preventScroll: true });
      }
      if (target === "workspace" && workspaceEntry === "manage") {
        root.querySelector("[data-settings-workspace-clear], [data-settings-workspace-confirm]")?.focus({ preventScroll: true });
      }
    });
  });
  const providerForm = root.querySelector("[data-provider-setup-form]");
  if (providerForm) {
    providerForm.addEventListener("submit", event => {
      event.preventDefault();
      providerForm.querySelector("[data-provider-setup-save]")?.click();
    });
    providerForm.querySelector("[data-provider-setup-save]")?.addEventListener("click", async () => {
      await saveProviderSetup(providerForm, refreshRuntime, root);
    });
    providerForm.querySelector("[data-provider-setup-test]")?.addEventListener("click", async () => {
      await testProviderSetup(providerForm);
    });
    providerForm.querySelector("[data-provider-setup-fetch-models]")?.addEventListener("click", async () => {
      await fetchProviderSetupModels(providerForm);
    });
  }
  root.querySelectorAll("[data-settings-theme]").forEach(button => {
    button.addEventListener("click", () => {
      window.dispatchEvent(new CustomEvent("goflow:set-theme", { detail: { theme: button.dataset.settingsTheme } }));
      updateAppearanceSelection(root, button.dataset.settingsTheme);
    });
  });
}

function renderAppearanceSettings() {
  const current = document.documentElement.dataset.theme === "dark" ? "dark" : "light";
  return `<section class="settings-simple-section settings-appearance-section">
    <header><h2 class="panel-title">${icon("sun")}<span>${escapeHTML(t("settings.simpleAppearanceTitle"))}</span></h2></header>
    <div class="settings-theme-switch" role="group" aria-label="${escapeHTML(t("settings.simpleAppearanceTitle"))}">
      ${["light", "dark"].map(theme => `<button type="button" class="icon-text ${current === theme ? "active" : ""}" data-settings-theme="${theme}" aria-pressed="${current === theme}">${icon(theme === "dark" ? "moon" : "sun")}<span>${escapeHTML(t(`theme.${theme}`))}</span></button>`).join("")}
    </div>
  </section>`;
}

function updateAppearanceSelection(root, theme) {
  root.querySelectorAll("[data-settings-theme]").forEach(button => {
    const active = button.dataset.settingsTheme === theme;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  });
}

function renderWorkspaceVersions(payload = {}) {
  const versions = Array.isArray(payload?.versions) ? payload.versions : [];
  const retention = Number(payload?.retention || 3);
  const body = payload?.__error
    ? `<p class="muted">${escapeHTML(payload.__error)}</p>`
    : versions.length
      ? `<div class="settings-workspace-version-list">${versions.map(version => {
          const status = version.status === "restored" ? t("settings.workspaceVersionRestored") : t("settings.workspaceVersionSaved");
          const created = workspaceVersionDate(version.created_at);
          const runID = String(version.run_id || "");
          const versionID = String(version.version_id || "");
          return `<article class="settings-workspace-version">
            <div><strong>${escapeHTML(created)}</strong><small>${escapeHTML(t("settings.workspaceVersionFiles", { count: Number(version.change_count || 0) }))} · ${escapeHTML(status)}</small><code title="${escapeHTML(versionID)}">${escapeHTML(versionID.slice(0, 12))}</code></div>
            <button type="button" class="icon-text" data-workspace-version-restore="/api/workspace-versions/${encodeURIComponent(runID)}/restore"${version.can_restore ? "" : " disabled aria-disabled=\"true\""} title="${escapeHTML(version.reason || t("settings.workspaceVersionsRestore"))}">${icon("history")}<span>${escapeHTML(t("settings.workspaceVersionsRestore"))}</span></button>
          </article>`;
        }).join("")}</div>`
      : `<p class="muted">${escapeHTML(t("settings.workspaceVersionsEmpty"))}</p>`;
  return `<div class="settings-workspace-versions">
    <div class="settings-workspace-versions-head"><div><strong>${escapeHTML(t("settings.workspaceVersionsTitle"))}</strong><p>${escapeHTML(t("settings.workspaceVersionsHelp", { count: retention }))}</p></div><span class="badge good">${escapeHTML(t("settings.workspaceVersionsLive"))}</span></div>
    ${body}
    <div class="settings-workspace-readiness-output" data-workspace-versions-output role="status" aria-live="polite"></div>
  </div>`;
}

function workspaceVersionDate(value) {
  const date = new Date(value || "");
  if (Number.isNaN(date.getTime())) return t("common.none");
  return date.toLocaleString(currentLanguage() === "zh-CN" ? "zh-CN" : "en-US", { hour12: false });
}

function setSettingsWorkspaceBusy(root, busy) {
  root.querySelectorAll("[data-settings-workspace-confirm], [data-settings-workspace-clear]").forEach(button => {
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
  const modelSetup = settingsModelSetupState(runtime);
  const workspaceReady = Boolean(runtime?.workspace?.confirmed);
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
  const cards = [
    {
      tone: modelSetup.ready ? "good" : "warn",
      label: t("settings.heroModelLabel"),
      value: modelSetup.ready ? t("settings.providerSetupReady") : t("settings.providerSetupNeedsConfig")
    },
    {
      tone: workspaceReady ? "good" : "warn",
      label: t("settings.heroWorkspaceLabel"),
      value: workspaceReady ? t("workspace.confirmed") : t("workspace.needsConfirmation")
    },
    {
      tone: diagnostics?.restart_required ? "warn" : statusTone(status, diagnostics?.restart_required),
      label: t("settings.heroRuntimeLabel"),
      value: statusLabel(status, diagnostics?.restart_required)
    }
  ];

  return `<section class="panel span-12 settings-hero settings-hero-${tone}">
    <div class="settings-hero-copy">
      <span class="badge ${tone}">${statusLabel(status, diagnostics?.restart_required)}</span>
      <h2>${escapeHTML(title)}</h2>
      <p>${escapeHTML(body)}</p>
    </div>
    <div class="settings-hero-status-grid">
      ${cards.map(card => `<article class="${escapeHTML(card.tone)}">
        <span>${escapeHTML(card.label)}</span>
        <strong>${escapeHTML(card.value)}</strong>
      </article>`).join("")}
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
  const modelSetup = settingsModelSetupState(runtime);
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
  if (!modelSetup.ready) {
    actions.push({
      tone: "warn",
      title: t("settings.priorityEnvTitle"),
      body: t("settings.priorityEnvBody", { name: modelSetup.missingLabel || t("common.required") }),
      meta: t("common.required"),
      scroll: "first-run",
      cta: t("settings.priorityOpenProviderSetup")
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
    return { scroll: "first-run", cta: t("settings.priorityOpenProviderSetup") };
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

function renderEssentialSetup(runtime, diagnostics, providerResources, executionTargets) {
  return `<div class="settings-essential-grid">
    <div class="settings-essential-main">
      ${renderProviderSetup(runtime, providerResources)}
    </div>
    <div class="settings-essential-side">
      ${renderWorkspaceReadiness(runtime)}
    </div>
  </div>
  ${renderExecutionTargetManager(executionTargets)}`;
}

function renderExecutionTargetManager(payload) {
  const failed = payload && typeof payload === "object" && !Array.isArray(payload) && payload.__error;
  const targets = Array.isArray(payload) ? payload : [];
  const rows = targets.length
    ? targets.map(renderExecutionTargetRow).join("")
    : `<div class="settings-target-empty">${escapeHTML(t("settings.targetsEmpty"))}</div>`;
  return `<section class="settings-target-manager" data-target-manager>
    <div class="settings-target-manager-head">
      <div>
        <strong>${escapeHTML(t("settings.targetsTitle"))}</strong>
        <span>${escapeHTML(t("settings.targetsHelp"))}</span>
      </div>
      <button type="button" class="settings-target-add icon-text" data-target-add>
        ${icon("plus")}
        <span>${escapeHTML(t("settings.targetAdd"))}</span>
      </button>
    </div>
    <div class="settings-target-list" data-target-list>
      ${failed ? `<div class="settings-target-empty error">${escapeHTML(localizedSettingsErrorMessage(payload.__error))}</div>` : rows}
    </div>
    ${renderExecutionTargetForm()}
  </section>`;
}

function renderExecutionTargetRow(target = {}) {
  const local = target.id === "local" || target.kind === "local";
  const host = local ? t("settings.targetLocalAddress") : executionTargetAddress(target.host, target.port);
  const state = target.enabled !== false ? t("settings.targetEnabled") : t("settings.targetDisabled");
  const displayName = local ? t("settings.targetLocalName") : target.display_name || target.id || t("settings.targetUnnamed");
  return `<article class="settings-target-row" data-target-row="${escapeHTML(target.id || "")}">
    <span class="settings-target-kind" aria-hidden="true">
      ${icon("monitor")}
    </span>
    <div class="settings-target-row-copy">
      <strong>${escapeHTML(displayName)}</strong>
      <small>${escapeHTML(host)}${!local && target.user ? ` · ${escapeHTML(target.user)}` : ""}</small>
    </div>
    <span class="badge ${target.enabled !== false ? "good" : "neutral"}">${escapeHTML(local ? t("settings.targetBuiltIn") : state)}</span>
    ${local ? "" : `<div class="settings-target-row-actions">
      <button type="button" class="icon-button" data-target-edit="${escapeHTML(target.id || "")}" aria-label="${escapeHTML(t("settings.targetEdit"))}" title="${escapeHTML(t("settings.targetEdit"))}">${icon("pencil")}</button>
      <button type="button" class="icon-button danger" data-target-delete="${escapeHTML(target.id || "")}" aria-label="${escapeHTML(t("settings.targetDelete"))}" title="${escapeHTML(t("settings.targetDelete"))}">${icon("trash-2")}</button>
    </div>`}
  </article>`;
}

function renderExecutionTargetForm() {
  return `<form class="settings-target-form hidden" data-target-form data-target-id="">
    <div class="settings-target-form-head">
      <strong data-target-form-title>${escapeHTML(t("settings.targetAddTitle"))}</strong>
      <button type="button" class="icon-button" data-target-cancel aria-label="${escapeHTML(t("common.close"))}" title="${escapeHTML(t("common.close"))}">${icon("x")}</button>
    </div>
    <div class="settings-target-form-grid">
      <label><span>${escapeHTML(t("settings.targetName"))}</span><input data-target-name maxlength="100" required></label>
      <label><span>${escapeHTML(t("settings.targetHost"))}</span><input data-target-host maxlength="253" required placeholder="192.0.2.10"></label>
      <label><span>${escapeHTML(t("settings.targetPort"))}</span><input data-target-port type="number" min="1" max="65535" value="22" required></label>
      <label><span>${escapeHTML(t("settings.targetUser"))}</span><input data-target-user maxlength="64" required placeholder="operator"></label>
      <label class="wide"><span>${escapeHTML(t("settings.targetCredentialRef"))}</span><input data-target-credential required placeholder="secret://targets/server/private_key"></label>
      <label class="wide"><span>${escapeHTML(t("settings.targetCommands"))}</span><textarea data-target-commands rows="4" required></textarea></label>
    </div>
    <div class="settings-target-scan">
      <div class="settings-target-scan-head">
        <button type="button" data-target-scan>${escapeHTML(t("settings.targetScan"))}</button>
        <span data-target-scan-status role="status" aria-live="polite"></span>
      </div>
      <div class="settings-target-key-list" data-target-key-list></div>
      <input type="hidden" data-target-fingerprint required>
    </div>
    <label class="settings-target-enabled"><input type="checkbox" data-target-enabled checked><span>${escapeHTML(t("settings.targetEnabledLabel"))}</span></label>
    <div class="settings-target-form-actions">
      <button type="submit" class="primary" data-target-save>${escapeHTML(t("common.save"))}</button>
      <button type="button" data-target-cancel>${escapeHTML(t("common.cancel"))}</button>
      <span data-target-form-status role="status" aria-live="polite"></span>
    </div>
  </form>`;
}

function bindExecutionTargetManager(root, refreshRuntime, payload) {
  const manager = root.querySelector("[data-target-manager]");
  const form = manager?.querySelector("[data-target-form]");
  if (!manager || !form) return;
  const targets = Array.isArray(payload) ? payload : [];
  const field = name => form.querySelector(`[data-target-${name}]`);
  const defaultCommands = ["uptime", "df -h", "free -m", "systemctl --failed --no-pager"].join("\n");
  const setFormStatus = (message, error = false) => {
    const output = field("form-status");
    if (!output) return;
    output.textContent = message || "";
    output.classList.toggle("error", error);
  };
  const setScanStatus = (message, error = false) => {
    const output = field("scan-status");
    if (!output) return;
    output.textContent = message || "";
    output.classList.toggle("error", error);
  };
  const open = target => {
    const editing = target && target.id && target.id !== "local" ? target : null;
    form.dataset.targetId = editing?.id || "";
    field("form-title").textContent = t(editing ? "settings.targetEditTitle" : "settings.targetAddTitle");
    field("name").value = editing?.display_name || "";
    field("host").value = editing?.host || "";
    field("port").value = editing?.port || 22;
    field("user").value = editing?.user || "";
    field("credential").value = editing?.credential_ref || "";
    field("commands").value = Array.isArray(editing?.allowed_commands) ? editing.allowed_commands.join("\n") : defaultCommands;
    field("fingerprint").value = editing?.host_key_sha256 || "";
    field("enabled").checked = editing ? editing.enabled !== false : true;
    renderExecutionTargetKeys(field("key-list"), editing?.host_key_sha256 ? [{ key_type: t("settings.targetPinnedKey"), fingerprint: editing.host_key_sha256 }] : [], editing?.host_key_sha256 || "");
    setFormStatus("");
    setScanStatus(editing?.host_key_sha256 ? t("settings.targetFingerprintSaved") : "");
    form.classList.remove("hidden");
    field("name").focus();
    form.scrollIntoView({ block: "nearest", behavior: prefersReducedMotion() ? "auto" : "smooth" });
  };
  const close = () => {
    form.classList.add("hidden");
    form.dataset.targetId = "";
  };
  manager.querySelector("[data-target-add]")?.addEventListener("click", () => open(null));
  manager.querySelectorAll("[data-target-cancel]").forEach(button => button.addEventListener("click", close));
  manager.querySelectorAll("[data-target-edit]").forEach(button => button.addEventListener("click", () => {
    open(targets.find(item => item.id === button.dataset.targetEdit));
  }));
  manager.querySelectorAll("[data-target-delete]").forEach(button => button.addEventListener("click", async () => {
    const target = targets.find(item => item.id === button.dataset.targetDelete);
    if (!target || !window.confirm(t("settings.targetDeleteConfirm", { name: target.display_name || target.id }))) return;
    button.disabled = true;
    try {
      await deleteExecutionTarget(target.id);
      await rerenderSettingsAfterTargetChange(root, refreshRuntime);
    } catch (error) {
      button.disabled = false;
      if (form.classList.contains("hidden")) open(target);
      setFormStatus(localizedSettingsErrorMessage(error), true);
    }
  }));
  field("scan")?.addEventListener("click", async () => {
    const host = String(field("host")?.value || "").trim();
    const port = Number(field("port")?.value || 22);
    field("scan").disabled = true;
    setScanStatus(t("settings.targetScanning"));
    try {
      const result = await scanExecutionTargetHostKey({ host, port, authorized_scope: true, timeout_seconds: 12 });
      const keys = Array.isArray(result?.keys) ? result.keys : [];
      const preferred = keys.find(item => item.key_type === "ssh-ed25519")?.fingerprint || keys[0]?.fingerprint || "";
      field("fingerprint").value = preferred;
      renderExecutionTargetKeys(field("key-list"), keys, preferred);
      setScanStatus(t("settings.targetScanDone", { count: keys.length }));
    } catch (error) {
      field("fingerprint").value = "";
      renderExecutionTargetKeys(field("key-list"), [], "");
      setScanStatus(localizedSettingsErrorMessage(error), true);
    } finally {
      field("scan").disabled = false;
    }
  });
  [field("host"), field("port")].forEach(input => input?.addEventListener("input", () => {
    field("fingerprint").value = "";
    renderExecutionTargetKeys(field("key-list"), [], "");
    setScanStatus(t("settings.targetScanRequired"));
  }));
  field("key-list")?.addEventListener("change", event => {
    const input = event.target instanceof HTMLInputElement ? event.target : null;
    if (input?.name === "target-host-key") field("fingerprint").value = input.value;
  });
  form.addEventListener("submit", async event => {
    event.preventDefault();
    const fingerprint = String(field("fingerprint")?.value || "").trim();
    if (!fingerprint) {
      setFormStatus(t("settings.targetScanRequired"), true);
      return;
    }
    const id = String(form.dataset.targetId || "").trim();
    const body = {
      kind: "ssh",
      display_name: String(field("name")?.value || "").trim(),
      host: String(field("host")?.value || "").trim(),
      port: Number(field("port")?.value || 22),
      user: String(field("user")?.value || "").trim(),
      credential_ref: String(field("credential")?.value || "").trim(),
      host_key_sha256: fingerprint,
      allowed_commands: String(field("commands")?.value || "").split(/\r?\n/).map(value => value.trim()).filter(Boolean),
      enabled: Boolean(field("enabled")?.checked)
    };
    field("save").disabled = true;
    setFormStatus(t("settings.targetSaving"));
    try {
      if (id) await updateExecutionTarget(id, body); else await createExecutionTarget(body);
      await rerenderSettingsAfterTargetChange(root, refreshRuntime);
    } catch (error) {
      field("save").disabled = false;
      setFormStatus(localizedSettingsErrorMessage(error), true);
    }
  });
}

function renderExecutionTargetKeys(container, keys, selected) {
  if (!container) return;
  if (!Array.isArray(keys) || !keys.length) {
    container.innerHTML = "";
    return;
  }
  container.innerHTML = keys.map((key, index) => `<label class="settings-target-key">
    <input type="radio" name="target-host-key" value="${escapeHTML(key.fingerprint || "")}" ${key.fingerprint === selected || (!selected && index === 0) ? "checked" : ""}>
    <span><strong>${escapeHTML(key.key_type || t("settings.targetHostKey"))}</strong><small>${escapeHTML(key.fingerprint || "")}</small></span>
  </label>`).join("");
}

async function rerenderSettingsAfterTargetChange(root, refreshRuntime) {
  const runtime = typeof refreshRuntime === "function" ? await refreshRuntime() : await request("/api/runtime");
  if (runtime) await renderSettings(root, runtime, refreshRuntime);
}

function executionTargetAddress(host, port) {
  const value = String(host || "").trim();
  if (!value) return t("common.none");
  const displayHost = value.includes(":") && !value.startsWith("[") ? `[${value}]` : value;
  return `${displayHost}:${Number(port || 22)}`;
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
  return "";
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
    api_key: t("catalog.providerAPIKey"),
    env_key: t("catalog.providerAPIKey"),
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
    isolation_options: t("settings.field.isolationOptions"),
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

function renderProviderSetup(runtime, providerResources) {
  const provider = selectProviderSetup(runtime, providerResources);
  const status = providerSetupState(provider);
  const model = String(provider.model || "").trim();
  return `<form class="settings-provider-setup ${status.tone}" data-provider-setup-form data-provider-setup-api-key-set="${provider.api_key_set ? "true" : "false"}" data-provider-setup-reusable-key="${provider.reusable_api_key ? "true" : "false"}" data-provider-setup-wire-api="${escapeHTML(provider.wire_api || "responses")}" data-provider-setup-reasoning-effort="${escapeHTML(provider.reasoning_effort || "mid")}" data-provider-setup-message-fields="${escapeHTML(JSON.stringify(provider.provider_message_fields || []))}">
    <div class="settings-provider-setup-head">
      <div>
        <strong>${escapeHTML(t("settings.providerSetupTitle", { model: model || t("settings.providerSetupModelPlaceholder") }))}</strong>
        <p>${escapeHTML(t(status.ready ? "settings.providerSetupReadyHelp" : "settings.providerSetupHelp"))}</p>
      </div>
      <span class="badge ${status.badge}">${escapeHTML(status.label)}</span>
    </div>
    <div class="settings-provider-form-grid">
      <label class="wide">
        <span>${escapeHTML(t("catalog.providerBaseURL"))}</span>
        <input data-provider-setup-base-url type="url" value="${escapeHTML(provider.base_url || "")}" placeholder="https://api.openai.com/v1" autocomplete="off">
      </label>
      <label class="wide">
        <span>${escapeHTML(t("catalog.providerAPIKey"))}</span>
        <input data-provider-setup-api-key type="password" value="" placeholder="${escapeHTML(provider.api_key_set ? t("settings.providerSetupKeyKept") : t("settings.providerSetupKeyPlaceholder"))}" autocomplete="off">
      </label>
      <label class="wide">
        <span>${escapeHTML(t("catalog.providerDefaultModel"))}</span>
        <div class="settings-provider-model-controls">
          <select data-provider-setup-model>
            ${model ? `<option value="${escapeHTML(model)}" selected>${escapeHTML(model)}</option>` : `<option value="">${escapeHTML(t("settings.providerSetupModelPlaceholder"))}</option>`}
          </select>
          <button type="button" class="icon-text" data-provider-setup-fetch-models>${icon("refresh-cw")}<span>${escapeHTML(t("settings.providerSetupFetchModels"))}</span></button>
        </div>
      </label>
    </div>
    <div class="settings-provider-actions">
      <button type="button" class="primary icon-text" data-provider-setup-save>${icon("save")}<span>${escapeHTML(t("settings.providerSetupSave"))}</span></button>
      <button type="button" class="settings-provider-test-button icon-text" data-provider-setup-test>${icon("play")}<span>${escapeHTML(t("settings.providerSetupTest"))}</span></button>
    </div>
    <div class="settings-provider-output" data-provider-setup-output role="status" aria-live="polite"></div>
    <p class="settings-provider-footnote">${escapeHTML(t("settings.providerSetupRestartHint"))}</p>
  </form>`;
}

function selectProviderSetup(runtime = {}, providerResources) {
  const resources = Array.isArray(providerResources) ? providerResources : [];
  const runtimeProviders = Array.isArray(runtime.providers) ? runtime.providers : [];
  const setupByID = runtimeProviderSetupByID(runtime);
  const mergedResources = resources.map(provider => mergeProviderRuntimeSetup(provider, setupByID));
  const mergedRuntime = runtimeProviders.map(provider => mergeProviderRuntimeSetup(provider, setupByID));
  const first = [...mergedResources, ...mergedRuntime].find(provider => normalizeProviderSetupID(provider?.id || provider?.name || provider?.provider_id) === "primary") || {};
  return {
    id: "primary",
    provider: "openai-compatible",
    base_url: first.base_url || first.baseUrl || "",
    wire_api: first.wire_api || first.wireApi || "responses",
    model: first.model || first.default_model || "",
    reasoning_effort: first.reasoning_effort || first.reasoningEffort || "mid",
    provider_message_fields: Array.isArray(first.provider_message_fields) ? first.provider_message_fields : [],
    api_key_set: Boolean(first.api_key_set || first.apiKeySet),
    reusable_api_key: Boolean(first.api_key_set || first.apiKeySet)
  };
}

function mergeProviderRuntimeSetup(provider = {}, setupByID = new Map()) {
  const id = normalizeProviderSetupID(provider.id || provider.name || provider.provider_id || "primary");
  const setup = setupByID.get(id);
  if (!setup) return provider;
  return {
    ...provider,
    id,
    runtime_missing: Array.isArray(setup.missing) ? setup.missing : [],
    api_key_set: Array.isArray(setup.missing) && setup.missing.includes("api_key") ? false : (provider.api_key_set || provider.apiKeySet || false)
  };
}

function runtimeProviderSetupByID(runtime = {}) {
  const out = new Map();
  const providers = Array.isArray(runtime?.setup?.providers) ? runtime.setup.providers : [];
  providers.forEach(provider => {
    const id = normalizeProviderSetupID(provider?.id || provider?.name || "primary");
    out.set(id, provider || {});
  });
  return out;
}

function providerSetupState(provider = {}) {
  const missing = providerSetupMissing(provider);
  return {
    ready: missing.length === 0,
    tone: missing.length ? "warning" : "good",
    badge: missing.length ? "warn" : "good",
    label: missing.length ? t("settings.providerSetupNeedsConfig") : t("settings.providerSetupReady")
  };
}

function providerSetupMissing(provider = {}, options = {}) {
  const requireModel = options.requireModel !== false;
  if (Array.isArray(provider.runtime_missing) && provider.runtime_missing.length) {
    return provider.runtime_missing.map(field => String(field || "").split(".").pop()).filter(field => {
      if (field === "api_key") return true;
      if (field === "base_url") return true;
      return requireModel && field === "model";
    });
  }
  const missing = [];
  if (!String(provider.base_url || "").trim()) missing.push("base_url");
  if (!Boolean(provider.api_key_set) && !String(provider.api_key || "").trim()) missing.push("api_key");
  if (requireModel && !String(provider.model || "").trim()) missing.push("model");
  return missing;
}

function providerSetupFieldLabel(field) {
  if (field === "base_url") return t("catalog.providerBaseURL");
  if (field === "model") return t("catalog.providerDefaultModel");
  if (field === "api_key") return t("catalog.providerAPIKey");
  return localizedText(String(field || ""));
}

function settingsModelSetupState(runtime = {}) {
  const setup = runtime.setup || {};
  const primary = Array.isArray(setup.providers) ? setup.providers.find(provider => normalizeProviderSetupID(provider?.id) === "primary") : null;
  if (primary) {
    const missing = Array.isArray(primary.missing) ? primary.missing.filter(field => String(field || "").split(".").pop() === "api_key") : [];
    return {
      ready: missing.length === 0,
      missing,
      missingLabel: settingsProviderMissingLabel(missing)
    };
  }
  const provider = Array.isArray(runtime.providers) ? runtime.providers.find(item => normalizeProviderSetupID(item?.id) === "primary") : null;
  if (provider) {
    const missing = provider.api_key_set ? [] : ["primary.api_key"];
    return {
      ready: missing.length === 0,
      missing,
      missingLabel: settingsProviderMissingLabel(missing)
    };
  }
  const env = Array.isArray(setup.env) ? setup.env : [];
  const missing = env.filter(item => item.required && !item.set).map(item => item.name);
  return {
    ready: missing.length === 0,
    missing,
    missingLabel: settingsProviderMissingLabel(missing)
  };
}

function settingsProviderMissingLabel(fields = []) {
  const labels = fields.map(field => providerSetupFieldLabel(String(field || "").split(".").pop())).filter(Boolean);
  return labels.slice(0, 3).join(", ");
}

async function saveProviderSetup(form, refreshRuntime, root) {
  const output = form.querySelector("[data-provider-setup-output]");
  const button = form.querySelector("[data-provider-setup-save]");
  const doc = collectProviderSetupForm(form);
  if (output) output.textContent = "";
  const reusableKey = form.dataset.providerSetupReusableKey === "true";
  const missing = providerSetupMissing({ ...doc, api_key_set: reusableKey || Boolean(doc.api_key) });
  if (missing.length) {
    if (output) output.textContent = t("settings.providerSetupMissing", { fields: missing.map(providerSetupFieldLabel).join(", ") });
    form.classList.add("warning");
    return;
  }
  setProviderSetupBusy(button, true);
  if (output) output.textContent = t("settings.providerSetupSaving");
  try {
    const validation = await request(`/api/resources/providers/${encodeURIComponent(doc.id)}/validate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    if (validation && validation.valid === false) {
      const issue = Array.isArray(validation.issues) ? validation.issues[0] : null;
      throw new Error(issue?.message || t("settings.providerSetupValidationFailed"));
    }
    const saved = await request(`/api/resources/providers/${encodeURIComponent(doc.id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    markProviderSetupSaved(form, saved);
    if (output) output.textContent = providerSetupSaveMessage(saved);
    if (typeof refreshRuntime === "function") await refreshRuntime();
  } catch (error) {
    if (output) output.textContent = t("settings.providerSetupSaveFailed", { message: localizedSettingsErrorMessage(error, t("catalog.saveFailed")) });
  } finally {
    setProviderSetupBusy(button, false);
  }
}

async function testProviderSetup(form) {
  const output = form.querySelector("[data-provider-setup-output]");
  const button = form.querySelector("[data-provider-setup-test]");
  const doc = collectProviderSetupForm(form);
  if (output) {
    output.textContent = "";
    output.classList.remove("success", "error", "limited");
  }
  const reusableKey = form.dataset.providerSetupReusableKey === "true";
  const missing = providerSetupMissing({ ...doc, api_key_set: reusableKey || Boolean(doc.api_key) });
  if (missing.length) {
    if (output) output.textContent = t("settings.providerSetupMissing", { fields: missing.map(providerSetupFieldLabel).join(", ") });
    form.classList.add("warning");
    return;
  }
  setProviderTestBusy(button, true);
  if (output) output.textContent = t("settings.providerSetupTesting");
  try {
    const result = await request(`/api/resources/providers/${encodeURIComponent(doc.id)}/test`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    if (!result || result.valid === false || result.status === "error") {
      throw new Error(providerSetupTestError(result));
    }
    if (output) {
      output.innerHTML = renderProviderProbeResult(result);
      output.classList.remove("error");
      output.classList.toggle("success", result.status !== "limited");
      output.classList.toggle("limited", result.status === "limited");
    }
  } catch (error) {
    if (output) {
      output.textContent = t("settings.providerSetupTestFailed", { message: localizedSettingsErrorMessage(error, t("common.failed")) });
      output.classList.remove("success");
      output.classList.add("error");
    }
  } finally {
    setProviderTestBusy(button, false);
  }
}

function renderProviderProbeResult(result = {}) {
  const capabilities = Array.isArray(result.capabilities) ? result.capabilities : [];
  const limited = result.status === "limited" || capabilities.some(capability => !capability?.supported);
  const summary = t(limited ? "settings.providerSetupTestLimited" : "settings.providerSetupTestSucceeded", {
    ms: numberText(result.latency_ms || 0)
  });
  if (!capabilities.length) return escapeHTML(summary);
  return `<div class="settings-provider-probe-summary">${escapeHTML(summary)}</div>
    <div class="settings-provider-probe-list">
      ${capabilities.map(capability => {
        const supported = Boolean(capability?.supported);
        const name = String(capability?.name || "unknown");
        const message = String(capability?.message || "");
        return `<span class="${supported ? "supported" : "unavailable"}" ${message ? `title="${escapeHTML(message)}"` : ""}>
          <b>${escapeHTML(providerCapabilityLabel(name))}</b>
          <small>${escapeHTML(t(supported ? "settings.providerCapabilitySupported" : "settings.providerCapabilityUnavailable"))}</small>
        </span>`;
      }).join("")}
    </div>`;
}

function providerCapabilityLabel(name) {
  const key = `settings.providerCapability.${String(name || "").trim()}`;
  const translated = t(key);
  return translated === key ? localizedText(name) : translated;
}

function providerSetupTestError(result = {}) {
  const issue = Array.isArray(result?.issues) ? result.issues.find(item => item?.message) : null;
  return issue?.message || result?.message || t("settings.providerSetupTestUnknown");
}

function collectProviderSetupForm(form) {
  return {
    id: "primary",
    provider: "openai-compatible",
    base_url: String(form.querySelector("[data-provider-setup-base-url]")?.value || "").trim(),
    wire_api: String(form.dataset.providerSetupWireApi || "responses").trim() || "responses",
    model: String(form.querySelector("[data-provider-setup-model]")?.value || "").trim(),
    reasoning_effort: String(form.dataset.providerSetupReasoningEffort || "mid").trim() || "mid",
    api_key: String(form.querySelector("[data-provider-setup-api-key]")?.value || "").trim(),
    provider_message_fields: providerSetupExistingMessageFields(form)
  };
}

function providerSetupReusableKey(form) {
  return form?.dataset?.providerSetupReusableKey === "true" || form?.dataset?.providerSetupApiKeySet === "true";
}

function fillProviderSetupModels(form, models, selectedModel) {
  const select = form.querySelector("[data-provider-setup-model]");
  if (!select) return;
  const ids = [];
  const seen = {};
  (Array.isArray(models) ? models : []).forEach(item => {
    const id = String(item?.id || item?.name || "").trim();
    if (!id || seen[id]) return;
    seen[id] = true;
    ids.push(id);
  });
  const selected = String(selectedModel || "").trim();
  // The dropdown must list exactly what the provider returned. A previously
  // saved model that the provider does not advertise is not added back, or the
  // control shows a project-side name instead of provider data.
  const options = [`<option value="">${escapeHTML(t("settings.providerSetupModelPlaceholder"))}</option>`];
  ids.forEach(id => {
    options.push(`<option value="${escapeHTML(id)}">${escapeHTML(id)}</option>`);
  });
  select.innerHTML = options.join("");
  if (selected && seen[selected]) {
    select.value = selected;
  } else if (ids.length === 1) {
    select.value = ids[0];
  } else {
    select.value = "";
  }
}

async function fetchProviderSetupModels(form) {
  const output = form.querySelector("[data-provider-setup-output]");
  const button = form.querySelector("[data-provider-setup-fetch-models]");
  const select = form.querySelector("[data-provider-setup-model]");
  const doc = collectProviderSetupForm(form);
  if (output) {
    output.textContent = "";
    output.classList.remove("success", "error", "limited");
  }
  const missing = providerSetupMissing({
    ...doc,
    api_key_set: providerSetupReusableKey(form) || Boolean(doc.api_key)
  }, { requireModel: false });
  if (missing.length) {
    if (output) output.textContent = t("settings.providerSetupMissing", { fields: missing.map(providerSetupFieldLabel).join(", ") });
    form.classList.add("warning");
    return;
  }
  setProviderFetchBusy(button, true);
  if (select) select.disabled = true;
  if (output) output.textContent = t("settings.providerSetupFetchingModels");
  try {
    const result = await request(`/api/resources/providers/${encodeURIComponent(doc.id)}/models`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    if (!result || result.valid === false || result.status === "error") {
      throw new Error(providerSetupTestError(result));
    }
    const models = Array.isArray(result.models) ? result.models : [];
    fillProviderSetupModels(form, models, doc.model);
    if (!models.length) {
      if (output) {
        output.textContent = t("settings.providerSetupFetchEmpty");
        output.classList.add("error");
      }
      return;
    }
    if (output) {
      output.textContent = t("settings.providerSetupFetchLoaded", { count: String(models.length) });
      output.classList.add("success");
    }
    select?.focus();
  } catch (error) {
    if (output) {
      output.textContent = t("settings.providerSetupFetchFailed", { message: localizedSettingsErrorMessage(error, t("common.failed")) });
      output.classList.remove("success", "limited");
      output.classList.add("error");
    }
  } finally {
    setProviderFetchBusy(button, false);
    if (select) select.disabled = false;
  }
}

function providerSetupExistingMessageFields(form) {
  if (!form) return [];
  try {
    const raw = form.dataset.providerSetupMessageFields || "[]";
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.map(value => String(value || "").trim()).filter(Boolean) : [];
  } catch {
    return [];
  }
}

function normalizeProviderSetupID(value) {
  const normalized = String(value || "primary").trim().toLowerCase().replace(/[^\w-]+/g, "-").replace(/^-+|-+$/g, "");
  return normalized || "primary";
}

function setIconTextButton(button, iconName, label) {
  if (!button) return;
  button.innerHTML = `${icon(iconName)}<span>${escapeHTML(label)}</span>`;
}

function setProviderButtonBusy(button, busy, iconName, busyLabel, idleLabel) {
  if (!button) return;
  button.disabled = Boolean(busy);
  button.setAttribute("aria-disabled", busy ? "true" : "false");
  button.setAttribute("aria-busy", busy ? "true" : "false");
  setIconTextButton(button, iconName, busy ? busyLabel : idleLabel);
}

function setProviderSetupBusy(button, busy) {
  setProviderButtonBusy(button, busy, "save", t("settings.providerSetupSaving"), t("settings.providerSetupSave"));
}

function setProviderTestBusy(button, busy) {
  setProviderButtonBusy(button, busy, "play", t("settings.providerSetupTesting"), t("settings.providerSetupTest"));
}

function setProviderFetchBusy(button, busy) {
  setProviderButtonBusy(button, busy, "refresh-cw", t("settings.providerSetupFetchingModels"), t("settings.providerSetupFetchModels"));
}

function providerSetupSaveMessage(saved = {}) {
  if (saved?.restart_required || saved?.apply_state === "restart_required") {
    return t("settings.providerSetupSavedRestart");
  }
  return t("settings.providerSetupSaved");
}

function markProviderSetupSaved(form, saved = {}) {
  form.classList.remove("warning");
  form.classList.add("good");
  form.dataset.providerSetupApiKeySet = "true";
  form.dataset.providerSetupReusableKey = "true";
  form.querySelector(".settings-provider-missing")?.remove();
  const badge = form.querySelector(".settings-provider-setup-head .badge");
  if (badge) {
    badge.className = "badge good";
    badge.textContent = t("settings.providerSetupReady");
  }
  const apiKey = form.querySelector("[data-provider-setup-api-key]");
  if (apiKey) {
    apiKey.value = "";
    apiKey.placeholder = t("settings.providerSetupKeyKept");
  }
}

function renderDeveloperSetupChecks(runtime, diagnostics) {
  const checks = renderEnvChecks(runtime);
  return `<details class="settings-developer-checks">
    <summary>
      <span>${escapeHTML(t("settings.developerChecks"))}</span>
      <small>${escapeHTML(t("settings.developerChecksHelp"))}</small>
    </summary>
    ${diagnostics?.restart_required ? `<p class="settings-developer-note">${escapeHTML(t("settings.developerRestartNote"))}</p>` : ""}
    ${checks}
  </details>`;
}

function renderAdvancedRuntimeDetails(runtime, diagnostics, diagnosticCapabilities) {
  const paths = [
    [t("settings.configPath"), diagnostics?.config_path],
    [t("settings.runtimeHome"), diagnostics?.runtime_home || runtime?.runtime_home],
    [t("settings.dataRoot"), diagnostics?.data_root || runtime?.data_root],
    [t("settings.workspaceRoot"), diagnostics?.workspace_root || runtime?.workspace?.display]
  ].filter(([, value]) => String(value || "").trim());
  return `<details class="settings-advanced-runtime">
    <summary>
      <span>${escapeHTML(t("settings.advancedRuntimeTitle"))}</span>
      <small>${escapeHTML(t("settings.advancedRuntimeHelp"))}</small>
    </summary>
    ${renderDiagnosticCapabilities(diagnosticCapabilities)}
    ${paths.length ? `<div class="settings-path-stack">
      ${paths.map(([label, value]) => `<div class="settings-path-row"><span>${escapeHTML(label)}</span><strong title="${escapeHTML(value)}">${escapeHTML(value)}</strong></div>`).join("")}
    </div>` : ""}
    <div class="settings-inline-actions">
      <button type="button" data-settings-data-backup>${escapeHTML(t("settings.dataBackupCreate"))}</button>
      <span class="muted" data-settings-data-backup-output role="status" aria-live="polite"></span>
    </div>
    <div class="settings-advanced-runtime-grid">
      <section>
        <div class="settings-section-label">
          <strong>${escapeHTML(t("settings.configModules"))}</strong>
          <span>${escapeHTML(t("settings.configModulesHelp"))}</span>
        </div>
        ${renderModules(diagnostics)}
      </section>
      <section>
        ${renderDeveloperSetupChecks(runtime, diagnostics)}
      </section>
    </div>
  </details>`;
}

function renderEnvChecks(runtime) {
  const env = Array.isArray(runtime?.setup?.env) ? runtime.setup.env : [];
  const envHTML = env.length
    ? env.map(item => `<article class="settings-check-item ${item.set ? "good" : item.required ? "warning" : "neutral"}">
        <div>
          <strong>${escapeHTML(item.name)}</strong>
          <p>${escapeHTML(localizedText(item.description || ""))}</p>
        </div>
        <span class="badge ${item.set ? "good" : item.required ? "warn" : "neutral"}">${item.set ? t("common.set") : item.required ? t("common.required") : t("common.optional")}</span>
      </article>`).join("")
    : `<div class="settings-empty settings-empty-action">
        <strong>${escapeHTML(t("settings.noEnvChecksTitle"))}</strong>
        <span>${escapeHTML(t("settings.noEnvChecks"))}</span>
        <div class="settings-empty-actions">
          <button type="button" class="primary" data-settings-scroll="first-run">${escapeHTML(t("settings.priorityOpenProviderSetup"))}</button>
          <button type="button" data-settings-target="catalog">${escapeHTML(t("settings.priorityOpenResources"))}</button>
        </div>
      </div>`;
  return `<div class="settings-check-list">
    ${envHTML}
  </div>`;
}

function renderWorkspaceReadiness(runtime = {}) {
  const workspace = runtime.workspace || {};
  const capabilities = runtime.workspace_capabilities || workspace.capabilities || {};
  const actions = workspaceActionsFromRuntime(runtime, workspace, capabilities);
  const confirmed = Boolean(workspace.confirmed);
  const clearAction = actions.find(action => action?.name === "clear");
  const canClear = clearAction ? clearAction.available !== false : Boolean(workspace.root && confirmed);
  const root = workspace.display || workspace.root || t("common.none");
  const switchMode = String(capabilities.switch_mode || capabilities.switchMode || "");
  return `<article class="settings-workspace-readiness ${confirmed ? "good" : "warning"}">
    <div class="settings-workspace-readiness-head">
      <div>
        <strong>${escapeHTML(t("settings.workspaceReadinessTitle"))}</strong>
        <p>${escapeHTML(t(confirmed ? "settings.workspaceReadinessReadyBody" : "settings.workspaceReadinessBlockedBody"))}</p>
      </div>
      <span class="badge ${confirmed ? "good" : "warn"}">${confirmed ? t("workspace.confirmed") : t("workspace.needsConfirmation")}</span>
    </div>
    <form class="settings-workspace-switch" data-settings-workspace-switch data-current-workspace="${escapeHTML(root)}">
      <label><span>${escapeHTML(t("settings.workspaceReadinessRoot"))}</span><input type="text" value="${escapeHTML(root)}" data-settings-workspace-path autocomplete="off" spellcheck="false"></label>
      ${capabilities.can_open_host_folder_picker ? `<button type="button" class="icon-text" data-settings-workspace-pick>${icon("folder")}<span>${escapeHTML(t("settings.workspacePickerChoose"))}</span></button>` : ""}
      <button type="submit" class="primary icon-text">${icon("folder")}<span>${escapeHTML(t(confirmed ? "settings.workspaceSwitchApply" : "settings.workspaceSwitchConfirmCurrent"))}</span></button>
    </form>
    <p class="settings-workspace-switch-help">${escapeHTML(t(switchMode === "dynamic_rebind" ? "settings.workspaceSwitchLiveHelp" : "settings.workspaceSwitchRestartHelp"))}</p>
    <div class="settings-workspace-readiness-actions">
      ${confirmed && canClear ? `<button type="button" class="icon-text danger" data-settings-workspace-clear data-settings-workspace-clear-path="${escapeHTML(clearAction?.path || "/api/workspace/clear")}" title="${escapeHTML(t("settings.workspaceReadinessRevokeHelp"))}">${icon("x")}<span>${escapeHTML(t("settings.workspaceReadinessClear"))}</span></button>` : ""}
    </div>
    ${confirmed && canClear ? `<p class="settings-workspace-revoke-help">${escapeHTML(t("settings.workspaceReadinessRevokeHelp"))}</p>` : ""}
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

function renderUpdateBasics(update) {
  if (update?.__error) {
    return `<article class="settings-update-basic warning">
      <div>
        <strong>${escapeHTML(t("settings.updateBasicTitle"))}</strong>
        <p>${escapeHTML(t("settings.updateUnavailable", { message: update.__error }))}</p>
      </div>
    </article>`;
  }
  const checkEnabled = update?.check_enabled !== false;
  const endpoint = update?.check_endpoint || "/api/update-policy/check";
  return `<article class="settings-update-basic ${checkEnabled ? "neutral" : "disabled"}">
    <div>
      <strong>${escapeHTML(t("settings.updateBasicTitle"))}</strong>
      <p>${escapeHTML(checkEnabled ? t("settings.updateBasicHelp") : updateCheckDisabledText(update))}</p>
    </div>
    <div class="settings-update-basic-actions">
      <span class="badge ${checkEnabled ? "info" : "neutral"}">${escapeHTML(checkEnabled ? t("settings.updateBasicOptIn") : t("settings.updateCheckDisabledBadge"))}</span>
      ${checkEnabled ? `<button type="button" data-settings-update-check data-settings-update-check-path="${escapeHTML(endpoint)}">${escapeHTML(t("settings.updateCheckButton"))}</button>` : ""}
    </div>
    <div class="settings-update-check-output" data-settings-update-output role="status" aria-live="polite"></div>
  </article>`;
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
  if (/context deadline exceeded|client\.timeout exceeded|i\/o timeout/i.test(text)) {
    const protocol = /Responses request/i.test(text) ? "/responses" : "the selected API protocol";
    return `${t("settings.providerSetupTimeoutHint", { protocol })} ${text}`;
  }
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
