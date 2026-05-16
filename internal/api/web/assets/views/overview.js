import { escapeHTML, request } from "../api.js";
import { runtimeVisibleApprovalCount } from "../approval_counts.js";
import { localizedText, t } from "../i18n.js";
import { getOnboardingState, resumeOnboarding, startOnboarding, stopOnboarding } from "../onboarding.js";

export async function renderOverview(root, runtime, refreshRuntime) {
  const pending = runtimeVisibleApprovalCount(runtime);
  const workflow = runtime.session?.workflow || {};
  const modelSetup = overviewModelSetupState(runtime);
  const onboarding = getOnboardingState();
  const launchpad = launchpadState(onboarding);
  const nextSteps = recommendedNextSteps(runtime, launchpad, modelSetup);
  root.innerHTML = `
    <section class="overview-launchpad panel" data-tour-id="overview-launchpad">
      <div class="overview-launchpad-copy">
        <p class="eyebrow">${t("overview.eyebrow")}</p>
        <h2>${t("overview.title")}</h2>
        <p class="overview-lead">${t("overview.copy")}</p>
        <div class="overview-learn-list">
          <span>${t("overview.learn.workflow")}</span>
          <span>${t("overview.learn.catalog")}</span>
          <span>${t("overview.learn.execution")}</span>
          <span>${t("overview.learn.observe")}</span>
        </div>
      </div>
      <div class="overview-launchpad-actions">
        <span class="badge ${launchpad.badgeClass}">${launchpad.badge}</span>
        <div class="hero-actions">
          <button class="primary" id="overviewTourPrimary">${launchpad.primaryLabel}</button>
          <button id="overviewTourSecondary">${launchpad.secondaryLabel}</button>
        </div>
      </div>
    </section>

    <div class="metric-grid">
      ${metric(t("overview.workspace"), runtime.workspace?.confirmed ? t("overview.workspaceConfirmed") : t("overview.workspaceNeedsConfirmation"), overviewDisplayValue(runtime.workspace?.display || t("common.none")), runtime.workspace?.confirmed ? "good" : "warn")}
      ${metric(t("overview.approvals"), String(pending), pending ? t("overview.approvalsWaiting") : t("overview.approvalsEmpty"), pending ? "warn" : "good")}
      ${metric(t("overview.runtime"), `${escapeHTML(overviewDisplayValue(runtime.active_agent || "-"))} / ${escapeHTML(modeLabel(runtime.mode))}`, `${t("overview.version")} ${escapeHTML(runtime.version || "dev")}`, "neutral")}
      ${metric(t("overview.setup"), modelSetup.ready ? t("overview.setupReady") : t("overview.setupNeedsEnv"), modelSetup.detail || t("overview.setupHelp"), modelSetup.ready ? "good" : "warn")}
    </div>

    <div class="grid overview-grid">
      <section class="panel span-7 spotlight-panel" data-tour-id="overview-spotlight-card">
        <div class="panel-head">
          <div>
            <h2>${t("overview.spotlightTitle")}</h2>
            <p class="muted">${t("overview.spotlightHelp")}</p>
          </div>
          <span class="badge">${launchpad.progress}</span>
        </div>
        <div class="spotlight-card">
          <div class="spotlight-orbit" aria-hidden="true">
            <span></span>
            <span></span>
            <span></span>
          </div>
          <div class="spotlight-content">
            <strong>${launchpad.title}</strong>
            <p>${launchpad.body}</p>
            <div class="spotlight-actions">
              <button class="primary" id="overviewSpotlightAction">${launchpad.primaryLabel}</button>
              <button id="overviewSpotlightFallback">${t("overview.openWorkflowStudio")}</button>
            </div>
          </div>
        </div>
      </section>

      <section class="panel span-5 overview-snapshot" data-tour-id="overview-runtime-snapshot">
        <div class="panel-head">
          <h2>${t("overview.currentWorkflow")}</h2>
          <span class="badge ${workflow.status ? "good" : "warn"}">${workflow.status ? escapeHTML(overviewDisplayValue(workflow.status)) : t("common.none")}</span>
        </div>
        <table class="kv compact">
          <tr><th>${t("overview.workflowName")}</th><td>${escapeHTML(overviewDisplayValue(workflow.name || "-"))}</td></tr>
          <tr><th>${t("overview.workflowStatus")}</th><td>${escapeHTML(workflow.status ? overviewDisplayValue(workflow.status) : "-")}</td></tr>
          <tr><th>${t("overview.workflowNext")}</th><td>${escapeHTML(overviewDisplayValue(workflow.next_stage || "-"))}</td></tr>
        </table>
      </section>

      <section class="panel span-7" data-tour-id="overview-next-steps">
        <div class="panel-head">
          <div>
            <h2>${t("overview.recommendedNext")}</h2>
            <p class="muted">${t("overview.recommendedHelp")}</p>
          </div>
        </div>
        <div class="next-step-list">
          ${nextSteps.map(item => nextStepCard(item)).join("")}
        </div>
      </section>

      <section class="panel span-5" data-tour-id="overview-learn-by-doing">
        <div class="panel-head">
          <div>
            <h2>${t("overview.learnByDoing")}</h2>
            <p class="muted">${t("overview.learnByDoingHelp")}</p>
          </div>
        </div>
        <div class="learning-actions">
          ${learnAction("workflows", t("overview.learnAction.workflow"), t("overview.learnAction.workflowBody"))}
          ${learnAction("catalog", t("overview.learnAction.catalog"), t("overview.learnAction.catalogBody"))}
          ${learnAction("playground", t("overview.learnAction.playground"), t("overview.learnAction.playgroundBody"))}
          ${learnAction("approvals", t("overview.learnAction.approvals"), t("overview.learnAction.approvalsBody"))}
        </div>
      </section>
    </div>`;

  bindOverviewActions(root, launchpad, refreshRuntime);
}

function metric(label, value, detail, kind) {
  return `<section class="metric ${kind || ""}">
    <span>${label}</span>
    <strong>${value}</strong>
    <small>${escapeHTML(detail)}</small>
  </section>`;
}

function modeLabel(value) {
  const raw = String(value || "");
  if (!raw) return "-";
  const key = `catalog.mode.${raw}`;
  const translated = t(key);
  return translated === key ? overviewDisplayValue(raw) : translated;
}

function launchpadState(onboarding) {
  if (onboarding.active) {
    return {
      badge: t("overview.launchpad.active"),
      badgeClass: "good",
      progress: t("overview.launchpad.progressActive"),
      title: t("overview.launchpad.activeTitle"),
      body: t("overview.launchpad.activeBody"),
      primaryLabel: t("overview.continueTour"),
      secondaryLabel: t("overview.pauseTour"),
      primaryAction: () => resumeOnboarding(),
      secondaryAction: () => stopOnboarding(true)
    };
  }
  if (onboarding.completed) {
    return {
      badge: t("overview.launchpad.completed"),
      badgeClass: "good",
      progress: t("overview.launchpad.progressCompleted"),
      title: t("overview.launchpad.completedTitle"),
      body: t("overview.launchpad.completedBody"),
      primaryLabel: t("overview.restartTour"),
      secondaryLabel: t("overview.openWorkflowStudio"),
      primaryAction: () => startOnboarding("shell", 0),
      secondaryAction: () => { location.hash = "workflows"; }
    };
  }
  if (onboarding.tourId || onboarding.stepIndex) {
    return {
      badge: t("overview.launchpad.paused"),
      badgeClass: "warn",
      progress: t("overview.launchpad.progressPaused"),
      title: t("overview.launchpad.pausedTitle"),
      body: t("overview.launchpad.pausedBody"),
      primaryLabel: t("overview.continueTour"),
      secondaryLabel: t("overview.restartTour"),
      primaryAction: () => resumeOnboarding(),
      secondaryAction: () => startOnboarding("shell", 0)
    };
  }
  return {
    badge: t("overview.launchpad.ready"),
    badgeClass: "neutral",
    progress: t("overview.launchpad.progressReady"),
    title: t("overview.launchpad.readyTitle"),
    body: t("overview.launchpad.readyBody"),
    primaryLabel: t("overview.startTour"),
    secondaryLabel: t("overview.firstRun"),
    primaryAction: () => startOnboarding("shell", 0),
    secondaryAction: () => { location.hash = "settings"; }
  };
}

function recommendedNextSteps(runtime, launchpad, modelSetup = overviewModelSetupState(runtime)) {
  const pending = runtimeVisibleApprovalCount(runtime);
  const workspaceReady = !!runtime.workspace?.confirmed;
  const workspaceActions = overviewWorkspaceActions(runtime);
  const confirmAction = workspaceActions.find(action => action?.name === "confirm") || null;
  const canConfirmWorkspace = !workspaceReady && overviewWorkspaceActionAvailable(workspaceActions, "confirm", Boolean(runtime.workspace?.root));
  const steps = [
    {
      tone: workspaceReady ? "good" : "warn",
      badge: workspaceReady ? t("overview.next.readyBadge") : t("overview.next.safetyBadge"),
      title: workspaceReady ? t("overview.next.workspaceReady") : t("overview.next.workspaceSetup"),
      body: workspaceReady ? t("overview.next.workspaceReadyBody") : t("overview.next.workspaceSetupBody"),
      target: "workspace",
      action: canConfirmWorkspace ? "confirm-workspace" : "",
      actionPath: confirmAction?.path || "",
      actionMethod: confirmAction?.method || ""
    },
    {
      tone: pending ? "warn" : "good",
      badge: pending ? t("overview.next.approvalBadge") : t("overview.next.runBadge"),
      title: pending ? t("overview.next.approvalsPending") : t("overview.next.executionReady"),
      body: pending ? t("overview.next.approvalsPendingBody") : t("overview.next.executionReadyBody"),
      target: pending ? "approvals" : "playground"
    }
  ];
  if (!modelSetup.ready) {
    steps.splice(1, 0, {
      tone: "warn",
      badge: t("overview.next.modelBadge"),
      title: t("overview.next.modelSetup"),
      body: modelSetup.detail || t("overview.next.modelSetupBody"),
      target: "settings"
    });
  } else {
    steps.splice(1, 0, {
      tone: "neutral",
      badge: t("overview.next.practiceBadge"),
      title: t("overview.next.workflowTour"),
      body: launchpad.body,
      target: "workflows"
    });
  }
  return steps;
}

function nextStepCard(item) {
  const target = escapeHTML(item.target || "overview");
  const primaryAction = item.action === "confirm-workspace"
    ? `<button type="button" class="primary" data-overview-action="confirm-workspace" data-overview-action-path="${escapeHTML(item.actionPath || "/api/workspace/confirm")}" data-overview-action-method="${escapeHTML(item.actionMethod || "POST")}">${escapeHTML(t("overview.next.confirmWorkspace"))}</button>`
    : `<button type="button" class="primary subtle" data-target="${target}">${escapeHTML(t("overview.next.openTarget"))}</button>`;
  const secondaryAction = item.action === "confirm-workspace"
    ? `<button type="button" data-target="${target}">${escapeHTML(t("overview.next.openWorkspace"))}</button>`
    : "";
  return `<article class="next-step-card ${escapeHTML(item.tone || "neutral")}">
    <span class="badge ${escapeHTML(item.tone || "neutral")}">${escapeHTML(item.badge || item.title)}</span>
    <strong>${escapeHTML(item.title)}</strong>
    <p>${escapeHTML(item.body)}</p>
    <div class="next-step-actions">
      ${primaryAction}
      ${secondaryAction}
    </div>
    <div class="next-step-status" data-overview-status="${escapeHTML(item.action || item.target || "")}" role="status" aria-live="polite"></div>
  </article>`;
}

function learnAction(target, title, body) {
  return `<button class="learning-action" data-target="${target}">
    <strong>${escapeHTML(title)}</strong>
    <span>${escapeHTML(body)}</span>
  </button>`;
}

function bindOverviewActions(root, launchpad, refreshRuntime) {
  const triggerLaunch = () => launchpad.primaryAction();
  root.querySelector("#overviewTourPrimary").onclick = triggerLaunch;
  root.querySelector("#overviewSpotlightAction").onclick = triggerLaunch;
  root.querySelector("#overviewTourSecondary").onclick = () => launchpad.secondaryAction();
  root.querySelector("#overviewSpotlightFallback").onclick = () => { location.hash = "workflows"; };
  root.querySelectorAll("[data-overview-action]").forEach(button => {
    button.addEventListener("click", event => {
      event.preventDefault();
      const action = button.dataset.overviewAction || "";
      if (action === "confirm-workspace") {
        void confirmWorkspaceFromOverview(root, button, refreshRuntime);
      }
    });
  });
  root.querySelectorAll("[data-target]").forEach(button => {
    button.addEventListener("click", () => {
      location.hash = button.dataset.target;
    });
  });
}

async function confirmWorkspaceFromOverview(root, button, refreshRuntime) {
  const status = root.querySelector('[data-overview-status="confirm-workspace"]');
  setOverviewActionBusy(root, true);
  if (status) status.textContent = t("overview.next.workspaceConfirming");
  try {
    await overviewWorkspaceActionRequest(button.dataset.overviewActionPath || "/api/workspace/confirm", button.dataset.overviewActionMethod || "POST");
    if (status) status.textContent = t("overview.next.workspaceConfirmed");
    if (typeof refreshRuntime === "function") {
      const nextRuntime = await refreshRuntime();
      await renderOverview(root, nextRuntime, refreshRuntime);
    }
  } catch (error) {
    if (status) status.textContent = overviewDisplayValue(error?.data?.message || error?.data?.error || error?.message || t("overview.next.workspaceConfirmFailed"));
  } finally {
    setOverviewActionBusy(root, false);
  }
}

function setOverviewActionBusy(root, busy) {
  root.querySelectorAll("[data-overview-action]").forEach(button => {
    if (busy) {
      if (!button.dataset.overviewBusyPreviousDisabled) {
        button.dataset.overviewBusyPreviousDisabled = button.disabled ? "true" : "false";
      }
      button.disabled = true;
      button.setAttribute("aria-disabled", "true");
      button.setAttribute("aria-busy", "true");
      return;
    }
    const wasDisabled = button.dataset.overviewBusyPreviousDisabled === "true";
    delete button.dataset.overviewBusyPreviousDisabled;
    button.disabled = wasDisabled;
    button.setAttribute("aria-disabled", wasDisabled ? "true" : "false");
    button.setAttribute("aria-busy", busy ? "true" : "false");
  });
}

function overviewWorkspaceActionAvailable(actions = [], name, fallback = false) {
  const action = actions.find(item => item?.name === name);
  return action ? action.available !== false : fallback;
}

function overviewWorkspaceActions(runtime = {}) {
  const workspace = runtime.workspace || {};
  const capabilities = runtime.workspace_capabilities || workspace.capabilities || {};
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

function overviewWorkspaceActionRequest(path, method = "POST") {
  return request(path, { method: String(method || "POST").toUpperCase() });
}

function overviewModelSetupState(runtime = {}) {
  const setup = runtime.setup || {};
  if (typeof setup.model_ready === "boolean") {
    const missing = Array.isArray(setup.missing_provider_fields) ? setup.missing_provider_fields : [];
    return {
      ready: setup.model_ready,
      detail: setup.model_ready
        ? t("overview.setupReadyHelp")
        : t("overview.setupMissingHelp", { fields: overviewSetupFieldsLabel(missing) })
    };
  }
  const providers = Array.isArray(runtime.providers) ? runtime.providers : [];
  if (providers.length) {
    const missing = [];
    providers.forEach(provider => {
      const id = String(provider?.id || "provider").trim() || "provider";
      if (!String(provider?.base_url || "").trim()) missing.push(`${id}.base_url`);
      if (!provider?.api_key_set) missing.push(`${id}.api_key`);
      if (!String(provider?.model || "").trim()) missing.push(`${id}.model`);
    });
    return {
      ready: missing.length === 0,
      detail: missing.length ? t("overview.setupMissingHelp", { fields: overviewSetupFieldsLabel(missing) }) : t("overview.setupReadyHelp")
    };
  }
  const env = Array.isArray(setup.env) ? setup.env : [];
  const required = env.filter(item => item.required);
  const missing = required.filter(item => !item.set).map(item => item.name);
  return {
    ready: required.length > 0 && missing.length === 0,
    detail: missing.length ? t("overview.setupMissingHelp", { fields: overviewSetupFieldsLabel(missing) }) : t("overview.setupHelp")
  };
}

function overviewSetupFieldsLabel(fields = []) {
  const labels = fields.map(field => overviewSetupFieldLabel(field)).filter(Boolean);
  if (!labels.length) return t("overview.setupFieldsUnknown");
  return labels.slice(0, 3).join(", ");
}

function overviewSetupFieldLabel(field) {
  const raw = String(field || "").trim();
  const normalized = raw.split(".").pop();
  if (normalized === "base_url") return t("catalog.providerBaseURL");
  if (normalized === "api_key") return t("catalog.providerAPIKey");
  if (normalized === "model") return t("catalog.providerDefaultModel");
  return overviewDisplayValue(raw);
}

function overviewDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return text;
  if (text.includes("\\") || text.includes("://")) return text;
  if (text.includes("/") && !/\s/.test(text)) return text;
  if (text.includes(".") && !/\s/.test(text)) return text;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return text;
  return localizedText(text);
}
