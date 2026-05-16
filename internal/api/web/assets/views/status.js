import { escapeHTML, fetchTeamState, fetchWorkflowRunEvidence, renderSafeMarkdown } from "../api.js";
import { runtimeVisibleApprovalCount } from "../approval_counts.js";
import { localizedText, t } from "../i18n.js";

const statusHTMLCache = new WeakMap();
const statusEvidenceCache = new WeakMap();
const statusSessionCache = new WeakMap();

export async function renderStatus(root, runtime) {
  const statusLines = runtime.status_lines || [];
  const session = runtime.session || {};
  const workflow = session.workflow || {};
  const pending = runtimeVisibleApprovalCount(runtime);
  const healthItems = buildHealthItems(runtime, workflow, pending, statusLines.length);
  const runFocus = statusRunFocus(runtime);
  const cost = runtime.cost || {};
  const auxiliaryModels = Array.isArray(runtime.auxiliary_models) ? runtime.auxiliary_models : [];
  const mcpPressure = summarizeMCPPressure(runtime);
  const teamState = await loadStatusTeamState(runtime);
  const teamSignalCount = statusTeamSignalCount(teamState);

  root.innerHTML = `
    <section class="status-hero panel" data-tour-id="status-hero">
      <div class="status-hero-copy">
        <p class="eyebrow">${t("view.status.eyebrow")}</p>
        <h2>${t("status.title")}</h2>
        <p class="status-lead">${t("status.copy")}</p>
        <div class="status-highlight-list">
          <span>${t("status.learn.runtime")}</span>
          <span>${t("status.learn.workflow")}</span>
          <span>${t("status.learn.approvals")}</span>
          <span>${t("status.learn.session")}</span>
        </div>
      </div>
      <div>
        <span id="statusHeroBadge" class="badge ${pending ? "warn" : "good"}">${pending ? t("status.badgeAttention") : t("status.badgeHealthy")}</span>
      </div>
    </section>

    <div id="statusMetricGrid" class="metric-grid">
      ${metric(t("status.metric.pending"), String(pending), pending ? t("status.metric.pendingHelp") : t("status.metric.clearHelp"), pending ? "warn" : "good")}
      ${metric(t("status.metric.workflow"), escapeHTML(localizedText(workflow.name || t("common.none"))), workflowStatusMetricDetail(workflow), workflow.status ? "neutral" : "warn")}
      ${metric(t("status.metric.tools"), numberText(mcpPressure.active + mcpPressure.queued), mcpPressure.metricHelp, mcpPressure.tone)}
      ${metric(t("status.metric.agent"), escapeHTML(localizedText(runtime.active_agent || "-")), `${t("status.metric.mode")} ${escapeHTML(modeLabel(runtime.mode))}`, "neutral")}
      ${metric(t("status.metric.logs"), String(statusLines.length), t("status.metric.logsHelp"), statusLines.length ? "good" : "warn")}
    </div>

    <div class="grid status-grid">
      <section class="panel span-5 status-health" data-tour-id="status-health">
        <div class="panel-head">
          <div>
            <h2>${t("status.healthTitle")}</h2>
            <p class="muted">${t("status.healthHelp")}</p>
          </div>
        </div>
        <div id="statusHealthCard" class="status-health-card">
          ${healthItems.map(renderHealthItem).join("")}
        </div>
      </section>

      <section class="panel span-7 status-run-focus" data-tour-id="status-run-focus">
        <div class="panel-head">
          <div>
            <h2>${t("status.runFocusTitle")}</h2>
            <p class="muted">${t("status.runFocusHelp")}</p>
          </div>
          <span id="statusRunFocusBadge" class="badge ${runFocus.tone}">${escapeHTML(runFocus.badge)}</span>
        </div>
        <div id="statusRunFocusBody">${renderStatusRunFocus(runFocus)}</div>
      </section>

      <section class="panel span-12 status-team" data-tour-id="status-team">
        <div class="panel-head">
          <div>
            <h2>${t("status.teamTitle")}</h2>
            <p class="muted">${t("status.teamHelp")}</p>
          </div>
          <span id="statusTeamBadge" class="badge ${teamSignalCount ? "neutral" : "warn"}">${teamSignalCount ? t("status.teamSignalCount", { count: teamSignalCount }) : t("status.teamNoSignals")}</span>
        </div>
        <div id="statusTeamBody">${renderStatusTeamState(teamState)}</div>
      </section>

      <details class="panel span-12 status-advanced" data-tour-id="status-advanced" data-status-detail-key="advanced-diagnostics">
        <summary class="status-advanced-summary">
          <span>
            <strong>${t("status.advancedTitle")}</strong>
            <small>${t("status.advancedHelp")}</small>
          </span>
          <span class="status-advanced-badges">
            <span id="statusMCPBadge" class="badge ${mcpPressure.tone}">${escapeHTML(mcpPressure.badge)}</span>
            <span id="statusCostBadge" class="badge ${costAttentionCount(cost) ? "warn" : "neutral"}">${costAttentionCount(cost) ? t("status.costRecommendationsCount", { count: costAttentionCount(cost) }) : t("status.costNoRecommendations")}</span>
            <span id="statusRuntimeBadge" class="badge ${statusLines.length ? "good" : "warn"}">${statusLines.length ? t("status.runtimeActive") : t("status.runtimeIdle")}</span>
          </span>
        </summary>
        <div class="status-advanced-body">
          <section class="status-runtime" data-tour-id="status-logs">
            <div class="panel-head compact">
              <div>
                <h2>${t("status.runtime")}</h2>
                <p class="muted">${t("status.runtimeHelp")}</p>
              </div>
            </div>
            <div class="status-log-card">
              <pre id="statusLog" class="log status-log">${escapeHTML(statusLines.length ? statusLines.join("\n") : t("status.noRuntimeLines"))}</pre>
            </div>
          </section>

          <section class="status-mcp" data-tour-id="status-mcp">
            <div class="panel-head compact">
              <div>
                <h2>${t("status.mcpTitle")}</h2>
                <p class="muted">${t("status.mcpHelp")}</p>
              </div>
            </div>
            <div id="statusMCPBody">${renderMCPPressure(mcpPressure)}</div>
          </section>

          <section class="status-cost" data-tour-id="status-cost">
            <div class="panel-head compact">
              <div>
                <h2>${t("status.costTitle")}</h2>
                <p class="muted">${t("status.costHelp")}</p>
              </div>
            </div>
            <div id="statusCostBody">${renderCostDiagnostics(cost, auxiliaryModels)}</div>
          </section>

          <section class="status-session" data-tour-id="status-session">
            <div class="panel-head compact">
              <div>
                <h2>${t("status.session")}</h2>
                <p class="muted">${t("status.sessionHelp")}</p>
              </div>
            </div>
            <div class="status-json-card">
              <div id="statusSessionSummary">${renderStatusSessionSummary(session, runtime)}</div>
              <details class="status-json-toggle" data-status-detail-key="session-json">
                <summary>${t("status.showSessionJson")}</summary>
                <pre id="statusSessionJson" class="log status-json">${escapeHTML(t("status.sessionJsonDeferred"))}</pre>
              </details>
            </div>
          </section>
        </div>
      </details>
    </div>`;

  primeStatusHTMLCache(root);
  root.dataset.statusSessionSummarySignature = statusSessionSummarySignature(session, runtime);
  setStatusSessionSnapshot(root, session);
  const initialTeamContext = statusTeamContext(runtime);
  root.dataset.statusTeamKey = [initialTeamContext.runID, initialTeamContext.workflow, initialTeamContext.status, initialTeamContext.nextStage].join("|");
  root.dataset.statusTeamFetchedAt = String(Date.now());
  let teamRefreshSeq = 0;
  let evidenceRefreshSeq = 0;
  let runtimeFrame = 0;
  let queuedRuntime = null;
  const scheduleEvidenceRefresh = runtimeValue => {
    const focus = statusRunFocus(runtimeValue);
    if (!focus.runID || focus.runType !== "workflow") {
      statusEvidenceCache.delete(root);
      root.dataset.statusRunEvidenceKey = "";
      return;
    }
    const key = statusRunEvidenceFetchKey(focus);
    if (root.dataset.statusRunEvidenceKey === key) return;
    evidenceRefreshSeq += 1;
    root.dataset.statusRunEvidenceKey = key;
    root.dataset.statusRunEvidenceSeq = String(evidenceRefreshSeq);
    updateStatusRunEvidence(root, runtimeValue, evidenceRefreshSeq).catch(() => {
      if (root.dataset.statusRunEvidenceSeq === String(evidenceRefreshSeq)) {
        root.dataset.statusRunEvidenceKey = "";
      }
    });
  };
  const onNavigate = event => {
    const button = event.target instanceof Element ? event.target.closest("[data-status-link]") : null;
    if (!button) return;
    const view = button.dataset.statusLink || "";
    persistStatusRunFocusNavigation(button, view);
    if (view) location.hash = view;
  };
  const applyRuntime = runtimeValue => {
    if (!root.isConnected) return;
    const viewState = captureStatusViewState(root);
    updateStatusRuntime(root, runtimeValue);
    restoreStatusViewState(root, viewState);
    if (shouldRefreshStatusTeam(root, runtimeValue)) {
      teamRefreshSeq += 1;
      root.dataset.statusTeamSeq = String(teamRefreshSeq);
      updateStatusTeamRuntime(root, runtimeValue, teamRefreshSeq).catch(() => {});
    }
    scheduleEvidenceRefresh(runtimeValue);
  };
  const onRuntime = event => {
    queuedRuntime = event.detail;
    if (runtimeFrame) return;
    runtimeFrame = requestAnimationFrame(() => {
      runtimeFrame = 0;
      const nextRuntime = queuedRuntime;
      queuedRuntime = null;
      if (nextRuntime) applyRuntime(nextRuntime);
    });
  };
  const onDetailsToggle = event => {
    const target = event.target;
    if (!(target instanceof Element) || !target.matches(".status-json-toggle")) return;
    if (target.open) materializeStatusSessionJson(root, { force: true });
  };
  const cleanup = () => {
    if (runtimeFrame) cancelAnimationFrame(runtimeFrame);
    root.removeEventListener("click", onNavigate);
    root.removeEventListener("toggle", onDetailsToggle, true);
    window.removeEventListener("goflow:runtime", onRuntime);
  };
  root.addEventListener("click", onNavigate);
  root.addEventListener("toggle", onDetailsToggle, true);
  window.addEventListener("goflow:runtime", onRuntime);
  window.addEventListener("goflow:view-dispose", cleanup, { once: true });
  scheduleEvidenceRefresh(runtime);
}

function updateStatusRuntime(root, runtime) {
  const statusLines = runtime.status_lines || [];
  const session = runtime.session || {};
  const workflow = session.workflow || {};
  const pending = runtimeVisibleApprovalCount(runtime);
  const healthItems = buildHealthItems(runtime, workflow, pending, statusLines.length);
  const baseRunFocus = statusRunFocus(runtime);
  const runFocus = statusRunFocus(runtime, getCachedStatusEvidence(root, baseRunFocus.runID));
  const cost = runtime.cost || {};
  const auxiliaryModels = Array.isArray(runtime.auxiliary_models) ? runtime.auxiliary_models : [];
  const mcpPressure = summarizeMCPPressure(runtime);

  const heroBadge = root.querySelector("#statusHeroBadge");
  if (heroBadge) {
    heroBadge.className = `badge ${pending ? "warn" : "good"}`;
    heroBadge.textContent = pending ? t("status.badgeAttention") : t("status.badgeHealthy");
  }

  setHTMLIfChanged(root.querySelector("#statusMetricGrid"), `
      ${metric(t("status.metric.pending"), String(pending), pending ? t("status.metric.pendingHelp") : t("status.metric.clearHelp"), pending ? "warn" : "good")}
      ${metric(t("status.metric.workflow"), escapeHTML(localizedText(workflow.name || t("common.none"))), workflowStatusMetricDetail(workflow), workflow.status ? "neutral" : "warn")}
      ${metric(t("status.metric.tools"), numberText(mcpPressure.active + mcpPressure.queued), mcpPressure.metricHelp, mcpPressure.tone)}
      ${metric(t("status.metric.agent"), escapeHTML(localizedText(runtime.active_agent || "-")), `${t("status.metric.mode")} ${escapeHTML(modeLabel(runtime.mode))}`, "neutral")}
      ${metric(t("status.metric.logs"), String(statusLines.length), t("status.metric.logsHelp"), statusLines.length ? "good" : "warn")}`);
  setHTMLIfChanged(root.querySelector("#statusHealthCard"), healthItems.map(renderHealthItem).join(""));
  const runFocusBadge = root.querySelector("#statusRunFocusBadge");
  if (runFocusBadge) {
    runFocusBadge.className = `badge ${runFocus.tone}`;
    runFocusBadge.textContent = runFocus.badge;
  }
  setHTMLIfChanged(root.querySelector("#statusRunFocusBody"), renderStatusRunFocus(runFocus));

  const runtimeBadge = root.querySelector("#statusRuntimeBadge");
  if (runtimeBadge) {
    runtimeBadge.className = `badge ${statusLines.length ? "good" : "warn"}`;
    runtimeBadge.textContent = statusLines.length ? t("status.runtimeActive") : t("status.runtimeIdle");
  }
  setTextIfChanged(root.querySelector("#statusLog"), statusLines.length ? statusLines.join("\n") : t("status.noRuntimeLines"));

  const costBadge = root.querySelector("#statusCostBadge");
  if (costBadge) {
    const count = costAttentionCount(cost);
    costBadge.className = `badge ${count ? "warn" : "neutral"}`;
    costBadge.textContent = count ? t("status.costRecommendationsCount", { count }) : t("status.costNoRecommendations");
  }
  setHTMLIfChanged(root.querySelector("#statusCostBody"), renderCostDiagnostics(cost, auxiliaryModels));
  const mcpBadge = root.querySelector("#statusMCPBadge");
  if (mcpBadge) {
    mcpBadge.className = `badge ${mcpPressure.tone}`;
    mcpBadge.textContent = mcpPressure.badge;
  }
  setHTMLIfChanged(root.querySelector("#statusMCPBody"), renderMCPPressure(mcpPressure));
  setStatusSessionSummary(root, session, runtime);
  setStatusSessionSnapshot(root, session);
}

async function updateStatusRunEvidence(root, runtime, requestSeq) {
  const baseFocus = statusRunFocus(runtime);
  if (!baseFocus.runID || baseFocus.runType !== "workflow") return;
  const payload = await fetchWorkflowRunEvidence(baseFocus.run || baseFocus.runID);
  if (requestSeq !== Number(root.dataset.statusRunEvidenceSeq || requestSeq)) return;
  const evidence = summarizeStatusRunEvidence(payload);
  statusEvidenceCache.set(root, { runID: baseFocus.runID, evidence });
  const viewState = captureStatusViewState(root);
  const focus = statusRunFocus(runtime, evidence);
  const badge = root.querySelector("#statusRunFocusBadge");
  if (badge) {
    badge.className = `badge ${focus.tone}`;
    badge.textContent = focus.badge;
  }
  setHTMLIfChanged(root.querySelector("#statusRunFocusBody"), renderStatusRunFocus(focus));
  restoreStatusViewState(root, viewState);
}

async function updateStatusTeamRuntime(root, runtime, requestSeq) {
  const teamState = await loadStatusTeamState(runtime);
  if (requestSeq !== Number(root.dataset.statusTeamSeq || requestSeq)) {
    return;
  }
  const viewState = captureStatusViewState(root);
  const count = statusTeamSignalCount(teamState);
  const badge = root.querySelector("#statusTeamBadge");
  if (badge) {
    badge.className = `badge ${count ? "neutral" : "warn"}`;
    badge.textContent = count ? t("status.teamSignalCount", { count }) : t("status.teamNoSignals");
  }
  setHTMLIfChanged(root.querySelector("#statusTeamBody"), renderStatusTeamState(teamState));
  restoreStatusViewState(root, viewState);
}

function setHTMLIfChanged(node, html) {
  if (!node || statusHTMLCache.get(node) === html) return;
  if (node.innerHTML === html) {
    statusHTMLCache.set(node, html);
    return;
  }
  node.innerHTML = html;
  statusHTMLCache.set(node, html);
}

function primeStatusHTMLCache(root) {
  [
    "#statusMetricGrid",
    "#statusHealthCard",
    "#statusRunFocusBody",
    "#statusTeamBody",
    "#statusMCPBody",
    "#statusCostBody",
    "#statusSessionSummary"
  ].forEach(selector => {
    const node = root.querySelector(selector);
    if (node) statusHTMLCache.set(node, node.innerHTML);
  });
}

function setTextIfChanged(node, text) {
  if (!node || node.textContent === text) return;
  node.textContent = text;
}

function setStatusSessionSummary(root, session, runtime) {
  const signature = statusSessionSummarySignature(session, runtime);
  if (root.dataset.statusSessionSummarySignature === signature) return;
  root.dataset.statusSessionSummarySignature = signature;
  setHTMLIfChanged(root.querySelector("#statusSessionSummary"), renderStatusSessionSummary(session, runtime));
}

function setStatusSessionSnapshot(root, session) {
  const previous = statusSessionCache.get(root) || {};
  const signature = statusSessionJsonSignature(session);
  statusSessionCache.set(root, {
    session,
    signature,
    renderedSignature: previous.renderedSignature || ""
  });
  if (root.querySelector(".status-json-toggle")?.open) {
    materializeStatusSessionJson(root);
  }
}

function materializeStatusSessionJson(root, options = {}) {
  const cached = statusSessionCache.get(root);
  const node = root.querySelector("#statusSessionJson");
  if (!cached || !node) return;
  if (!options.force && cached.renderedSignature === cached.signature) return;
  const text = JSON.stringify(cached.session || {}, null, 2);
  setTextIfChanged(node, text);
  statusSessionCache.set(root, {
    ...cached,
    renderedSignature: cached.signature
  });
}

function statusSessionJsonSignature(session = {}) {
  const workflow = session.workflow || {};
  const workflowRuns = normalizeCollection(session.workflow_runs);
  const agentRuns = normalizeCollection(session.agent_runs);
  const pending = normalizeCollection(session.pending_approvals);
  return [
    workflow.run_id || "",
    workflow.name || "",
    workflow.status || "",
    workflow.next_stage || "",
    workflowRuns.length,
    statusCollectionChangeMarker(workflowRuns),
    agentRuns.length,
    statusCollectionChangeMarker(agentRuns),
    pending.length,
    pending.slice(0, 8).map(item => item?.id || item?.run_id || item?.tool || item?.name || "").join(",")
  ].join("|");
}

function statusCollectionChangeMarker(items) {
  return normalizeCollection(items).slice(0, 32).map(statusRunChangeMarker).join(",");
}

function statusSessionSummarySignature(session = {}, runtime = {}) {
  const workflow = session.workflow || {};
  const workflowRuns = normalizeCollection(session.workflow_runs);
  const agentRuns = normalizeCollection(session.agent_runs);
  const pending = runtimeVisibleApprovalCount(runtime);
  const focus = statusRunFocus(runtime);
  const activeWorkflowRuns = workflowRuns.filter(run => isActiveWorkflowStatus(run?.status)).length;
  const activeAgentRuns = agentRuns.filter(run => isActiveAgentStatus(run?.status)).length;
  return [
    workflow.run_id || "",
    workflow.name || "",
    workflow.status || "",
    workflow.next_stage || "",
    workflowRuns.length,
    agentRuns.length,
    pending,
    activeWorkflowRuns,
    activeAgentRuns,
    focus.runType || "",
    focus.runID || "",
    focus.status || "",
    focus.nextStage || "",
    focus.stages || 0,
    focus.artifacts || 0,
    focus.diffs || 0,
    focus.acceptance?.total || 0,
    focus.acceptance?.pass || 0,
    focus.acceptance?.fail || 0,
    focus.acceptance?.warn || 0,
    focus.acceptance?.unknown || 0
  ].join("|");
}

function shouldRefreshStatusTeam(root, runtime) {
  const context = statusTeamContext(runtime);
  const key = [context.runID, context.workflow, context.status, context.nextStage].join("|");
  const now = Date.now();
  const lastFetch = Number(root.dataset.statusTeamFetchedAt || 0);
  const stale = now - lastFetch > 10000;
  if (root.dataset.statusTeamKey === key && !stale) return false;
  root.dataset.statusTeamKey = key;
  root.dataset.statusTeamFetchedAt = String(now);
  return true;
}

function captureStatusViewState(root) {
  const jsonToggle = root.querySelector(".status-json-toggle");
  return {
    windowX: window.scrollX || 0,
    windowY: window.scrollY || 0,
    detailOpen: captureStatusDetailState(root),
    scroll: captureStatusScrollState(root),
    jsonOpen: Boolean(jsonToggle?.open),
    jsonTop: root.querySelector(".status-json")?.scrollTop || 0,
    logTop: root.querySelector(".status-log")?.scrollTop || 0
  };
}

function restoreStatusViewState(root, state, options = {}) {
  if (!state) return;
  restoreStatusScrollNodes(root, state);
  requestAnimationFrame(() => restoreStatusScrollNodes(root, state, options));
}

function restoreStatusScrollNodes(root, state, options = {}) {
  const jsonToggle = root.querySelector(".status-json-toggle");
  const json = root.querySelector(".status-json");
  const log = root.querySelector(".status-log");
  restoreStatusDetailState(root, state.detailOpen);
  restoreStatusScrollState(root, state.scroll);
  if (jsonToggle) jsonToggle.open = Boolean(state.jsonOpen);
  if (json) json.scrollTop = Math.min(state.jsonTop || 0, Math.max(0, json.scrollHeight - json.clientHeight));
  if (log) log.scrollTop = Math.min(state.logTop || 0, Math.max(0, log.scrollHeight - log.clientHeight));
  if (options.restoreWindow && window.goflowCanRestoreWindowScroll?.() !== false && (
    Math.abs((window.scrollY || 0) - (state.windowY || 0)) > 1 ||
    Math.abs((window.scrollX || 0) - (state.windowX || 0)) > 1
  )) {
    window.scrollTo({ left: state.windowX || 0, top: state.windowY || 0, behavior: "auto" });
  }
}

function captureStatusDetailState(root) {
  const state = new Map();
  root.querySelectorAll("details[data-status-detail-key]").forEach(node => {
    state.set(node.dataset.statusDetailKey || "", Boolean(node.open));
  });
  return state;
}

function restoreStatusDetailState(root, state) {
  if (!state?.size) return;
  root.querySelectorAll("details[data-status-detail-key]").forEach(node => {
    const key = node.dataset.statusDetailKey || "";
    if (state.has(key)) node.open = Boolean(state.get(key));
  });
}

function captureStatusScrollState(root) {
  const selectors = [
    "#statusLog",
    "#statusRunFocusBody",
    "#statusTeamBody",
    "#statusCostBody",
    "#statusSessionJson"
  ];
  return selectors.map(selector => {
    const node = root.querySelector(selector);
    return node ? {
      selector,
      top: node.scrollTop || 0,
      left: node.scrollLeft || 0
    } : null;
  }).filter(Boolean);
}

function restoreStatusScrollState(root, state) {
  if (!Array.isArray(state)) return;
  for (const item of state) {
    const node = root.querySelector(item.selector);
    if (!node) continue;
    node.scrollTop = Math.min(item.top || 0, Math.max(0, node.scrollHeight - node.clientHeight));
    node.scrollLeft = Math.min(item.left || 0, Math.max(0, node.scrollWidth - node.clientWidth));
  }
}

async function loadStatusTeamState(runtime) {
  const context = statusTeamContext(runtime);
  if (!context.runID) {
    return normalizeStatusTeamState(null, context);
  }
  try {
    return normalizeStatusTeamState(await fetchTeamState({ run_id: context.runID }), context);
  } catch {
    return normalizeStatusTeamState(null, context);
  }
}

function statusTeamContext(runtime) {
  const session = runtime?.session || {};
  const workflow = session.workflow || {};
  const runs = normalizeCollection(session.workflow_runs);
  const activeRun = runs.find(run => run?.id && isActiveWorkflowStatus(run.status) && (!workflow.name || run.name === workflow.name))
    || runs.find(run => run?.id && isActiveWorkflowStatus(run.status))
    || runs.slice().sort((a, b) => statusRunSortValue(b) - statusRunSortValue(a))[0]
    || {};
  return {
    runID: String(workflow.run_id || activeRun.id || "").trim(),
    workflow: String(workflow.name || activeRun.name || "").trim(),
    status: String(workflow.status || activeRun.status || "").trim(),
    nextStage: String(workflow.next_stage || activeRun.next_stage || "").trim()
  };
}

function isActiveWorkflowStatus(status) {
  const value = String(status || "").trim().toLowerCase();
  return ["running", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow", "cancelling"].includes(value);
}

function isActiveAgentStatus(status) {
  const value = String(status || "").trim().toLowerCase();
  return ["running", "awaiting_tool_approval", "cancelling"].includes(value);
}

function statusRunSortValue(run) {
  const raw = run?.updated_at || run?.completed_at || run?.started_at || "";
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

function statusRunChangeMarker(run = {}) {
  return [
    run?.id || run?.run_id || "",
    run?.name || run?.workflow || run?.workflow_name || run?.agent_id || run?.agent || "",
    run?.status || "",
    run?.next_stage || "",
    run?.pending_call_id || "",
    statusRunEventMarker(run),
    statusRunCount(run, "completed_stages", "stages", "stages_count"),
    statusRunCount(run, "artifacts", "artifacts_count"),
    statusRunCount(run, "diffs", "diffs_count"),
    statusTextLength(run?.output || run?.summary || run?.final_message || "")
  ].join(":");
}

function statusRunEventMarker(run = {}) {
  const events = Array.isArray(run.events) ? run.events : [];
  if (!events.length) return Number(run.events_count || 0) || 0;
  const lastSeq = events.reduce((max, event) => {
    const seq = Number(event?.seq || event?.sequence || event?.id || event?.sse_id || 0);
    return Number.isFinite(seq) ? Math.max(max, seq) : max;
  }, 0);
  return `${events.length}:${lastSeq || ""}`;
}

function statusTextLength(value) {
  return String(value || "").length;
}

function statusRunFocus(runtime, evidence = null) {
  const session = runtime?.session || {};
  const workflow = session.workflow || {};
  const active = statusCurrentRun(runtime);
  const isWorkflow = active.run_type !== "agent";
  const status = String((isWorkflow ? workflow.status : "") || active.status || "").trim();
  const focus = {
    run: active,
    runType: isWorkflow ? "workflow" : "agent",
    runID: String(active.id || active.run_id || "").trim(),
    workflow: isWorkflow
      ? String(workflow.name || active.name || active.workflow || active.workflow_name || "").trim()
      : String(active.agent_id || active.agent || active.mode || "").trim(),
    status,
    nextStage: isWorkflow ? String(workflow.next_stage || active.next_stage || "").trim() : String(active.mode || active.agent_id || "").trim(),
    stages: isWorkflow ? statusRunCount(active, "completed_stages", "stages", "stages_count") : 0,
    artifacts: isWorkflow ? statusRunCount(active, "artifacts", "artifacts_count") : 0,
    diffs: statusRunCount(active, "diffs", "diffs_count"),
    acceptance: isWorkflow ? statusRunAcceptanceSummary(active) : { total: 0, pass: 0, fail: 0, warn: 0, unknown: 0, tone: "neutral" },
    evidence: isWorkflow ? evidence : null,
    updatedAt: String(active.updated_at || active.completed_at || active.started_at || "").trim()
  };
  const activeStatus = isWorkflow ? isActiveWorkflowStatus(focus.status) : isActiveAgentStatus(focus.status);
  focus.tone = !focus.runID ? "warn" : activeStatus ? "info" : statusRunTerminalError(focus.status) ? "bad" : statusRunTerminalSuccess(focus.status) ? "good" : "neutral";
  focus.badge = !focus.runID ? t("status.runFocusIdleBadge") : activeStatus ? t("status.runFocusActiveBadge") : t("status.runFocusRecentBadge");
  return focus;
}

function statusCurrentRun(runtime) {
  const session = runtime?.session || {};
  const workflow = session.workflow || {};
  const workflowRuns = normalizeCollection(session.workflow_runs).map(run => ({ ...run, run_type: "workflow" }));
  const agentRuns = normalizeCollection(session.agent_runs).map(run => ({ ...run, run_type: "agent" }));
  const workflowRunID = String(workflow.run_id || "").trim();
  const matchingWorkflow = workflowRunID
    ? workflowRuns.find(run => String(run?.id || run?.run_id || "") === workflowRunID)
    : null;
  if (matchingWorkflow && isActiveWorkflowStatus(workflow.status || matchingWorkflow.status)) {
    return matchingWorkflow;
  }
  const activeWorkflow = workflowRuns.find(run => isActiveWorkflowStatus(run?.status));
  const activeAgent = agentRuns.find(run => isActiveAgentStatus(run?.status));
  if (activeWorkflow && activeAgent) {
    return statusRunSortValue(activeAgent) > statusRunSortValue(activeWorkflow) ? activeAgent : activeWorkflow;
  }
  return matchingWorkflow
    || activeWorkflow
    || activeAgent
    || [...workflowRuns, ...agentRuns].sort((a, b) => statusRunSortValue(b) - statusRunSortValue(a))[0]
    || {};
}

function renderStatusSessionSummary(session = {}, runtime = {}) {
  const workflowRuns = normalizeCollection(session.workflow_runs);
  const agentRuns = normalizeCollection(session.agent_runs);
  const pendingApprovals = runtimeVisibleApprovalCount(runtime);
  const activeRuns = [
    ...workflowRuns.filter(run => isActiveWorkflowStatus(run?.status)),
    ...agentRuns.filter(run => isActiveAgentStatus(run?.status))
  ];
  const focus = statusRunFocus(runtime);
  const hasRun = Boolean(focus.runID || focus.workflow || focus.status);
  const badgeTone = pendingApprovals ? "warn" : activeRuns.length ? "info" : hasRun ? "neutral" : "good";
  const badgeText = pendingApprovals
    ? t("status.sessionPendingBadge", { count: pendingApprovals })
    : activeRuns.length
      ? t("status.sessionActiveBadge", { count: activeRuns.length })
      : t("status.sessionStableBadge");
  const facts = [
    [t("status.sessionWorkflowRuns"), workflowRuns.length, t("status.sessionWorkflowRunsHelp")],
    [t("status.sessionAgentRuns"), agentRuns.length, t("status.sessionAgentRunsHelp")],
    [t("status.sessionPendingApprovals"), pendingApprovals, t("status.sessionPendingApprovalsHelp")],
    [t("status.sessionActiveRuns"), activeRuns.length, t("status.sessionActiveRunsHelp")]
  ];
  return `<div class="status-session-summary">
    <div class="status-session-summary-head">
      <div>
        <strong>${escapeHTML(t("status.sessionSummaryTitle"))}</strong>
        <p class="muted">${escapeHTML(t("status.sessionSummaryHelp"))}</p>
      </div>
      <span class="badge ${badgeTone}">${escapeHTML(badgeText)}</span>
    </div>
    <div class="status-session-facts">
      ${facts.map(([label, value, help]) => `<span>
        <small>${escapeHTML(label)}</small>
        <strong>${escapeHTML(numberText(value))}</strong>
        <em>${escapeHTML(help)}</em>
      </span>`).join("")}
    </div>
    ${hasRun ? renderStatusSessionFocus(focus) : renderStatusSessionEmpty()}
  </div>`;
}

function renderStatusSessionFocus(focus) {
  const facts = [
    [t("status.sessionRun"), statusDisplayValue(shortStatusRunID(focus.runID) || focus.workflow)],
    [t("status.sessionRunType"), focus.runType === "agent" ? t("status.runFocusTypeAgent") : t("status.runFocusTypeWorkflow")],
    [t("status.sessionRunStatus"), statusRunLabel(focus.status)],
    [t("status.sessionRunNext"), statusDisplayValue(focus.nextStage || t("common.none"))],
    [t("status.sessionRunUpdated"), focus.updatedAt ? formatStatusTime(focus.updatedAt) : t("common.none")]
  ].filter(([, value]) => String(value || "").trim());
  return `<article class="status-session-focus">
    <div>
      <strong>${escapeHTML(t("status.sessionCurrentRun"))}</strong>
      <span>${escapeHTML(statusRunFocusBody(focus))}</span>
    </div>
    <div class="status-session-focus-grid">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><b>${escapeHTML(statusDisplayValue(value))}</b></span>`).join("")}
    </div>
  </article>`;
}

function renderStatusSessionEmpty() {
  return `<article class="status-session-empty">
    <strong>${escapeHTML(t("status.sessionNoRunTitle"))}</strong>
    <span>${escapeHTML(t("status.sessionNoRunHelp"))}</span>
    <div class="status-empty-actions">
      <button type="button" data-status-link="playground">${escapeHTML(t("status.openRun"))}</button>
      <button type="button" data-status-link="workflows">${escapeHTML(t("status.openWorkflows"))}</button>
    </div>
  </article>`;
}

function getCachedStatusEvidence(root, runID) {
  const cached = statusEvidenceCache.get(root);
  return cached?.runID && cached.runID === runID ? cached.evidence : null;
}

function statusRunEvidenceFetchKey(focus = {}) {
  return [
    focus.runType || "workflow",
    focus.runID || "",
    focus.status || "",
    focus.stages || 0,
    focus.artifacts || 0,
    focus.diffs || 0,
    focus.acceptance?.total || 0,
    focus.acceptance?.pass || 0,
    focus.acceptance?.fail || 0,
    focus.acceptance?.warn || 0,
    focus.acceptance?.unknown || 0
  ].join("|");
}

function renderStatusRunFocus(focus) {
  if (!focus.runID && !focus.workflow) {
    return `<div class="status-run-empty">
      <strong>${escapeHTML(t("status.runFocusEmptyTitle"))}</strong>
      <span>${escapeHTML(t("status.runFocusEmptyHelp"))}</span>
      <div class="status-empty-actions">
        <button type="button" data-status-link="playground">${escapeHTML(t("status.openRun"))}</button>
        <button type="button" data-status-link="workspace">${escapeHTML(t("status.openWorkspace"))}</button>
      </div>
    </div>`;
  }
  const isAgent = focus.runType === "agent";
  const facts = isAgent ? [
    [t("status.runFocusType"), t("status.runFocusTypeAgent")],
    [t("status.runFocusAgent"), statusDisplayValue(focus.workflow || t("common.none"))],
    [t("status.runFocusStatus"), statusRunLabel(focus.status)],
    [t("status.runFocusMode"), statusDisplayValue(focus.nextStage || t("common.none"))],
    [t("status.runFocusDiffs"), String(focus.diffs)]
  ] : [
    [t("status.runFocusType"), t("status.runFocusTypeWorkflow")],
    [t("status.runFocusWorkflow"), statusDisplayValue(focus.workflow || t("common.none"))],
    [t("status.runFocusStatus"), statusRunLabel(focus.status)],
    [t("status.runFocusNextStage"), statusDisplayValue(focus.nextStage || t("common.none"))],
    [t("status.runFocusStages"), String(focus.stages)],
    [t("status.runFocusArtifacts"), String(focus.artifacts)],
    [t("status.runFocusDiffs"), String(focus.diffs)],
    [t("status.runFocusAcceptance"), focus.acceptance.total ? statusRunAcceptanceLabel(focus.acceptance) : t("status.runFocusAcceptanceEmpty")],
    focus.evidence?.total ? [t("status.runFocusEvidence"), statusRunEvidenceLabel(focus.evidence)] : null
  ].filter(Boolean);
  const evidence = renderStatusRunEvidence(focus.evidence);
  return `<div class="status-run-card ${escapeHTML(focus.tone)}">
    <div class="status-run-main">
      <span>${escapeHTML(isAgent ? t("status.runFocusTypeAgent") : t("status.runFocusTypeWorkflow"))}</span>
      <strong>${escapeHTML(shortStatusRunID(focus.runID) || focus.workflow || t("common.none"))}</strong>
      <p>${escapeHTML(statusRunFocusBody(focus))}</p>
      ${focus.updatedAt ? `<small>${escapeHTML(t("status.runFocusUpdated"))}: ${escapeHTML(formatStatusTime(focus.updatedAt))}</small>` : ""}
      ${renderStatusRunAcceptance(focus.acceptance)}
      ${evidence}
      ${renderStatusRunFocusActions(focus)}
    </div>
    <div class="status-run-facts">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(statusDisplayValue(value))}</strong></span>`).join("")}
    </div>
  </div>`;
}

function statusRunAcceptanceSummary(run) {
  const items = [
    ...normalizeCollection(run?.completed_stages).flatMap(stage => normalizeCollection(stage?.acceptance)),
    ...normalizeCollection(run?.artifacts)
      .filter(artifact => String(artifact?.kind || "").trim().toLowerCase() === "acceptance")
      .map(artifact => artifact?.metadata && typeof artifact.metadata === "object" ? artifact.metadata : artifact)
  ];
  const counts = { total: items.length, pass: 0, fail: 0, warn: 0, unknown: 0 };
  for (const item of items) {
    const key = statusRunAcceptanceStatusKey(item?.status);
    counts[key] += 1;
  }
  counts.tone = counts.fail ? "bad" : counts.warn ? "warn" : counts.pass ? "good" : "neutral";
  return counts;
}

function summarizeStatusRunEvidence(payload) {
  const normalized = normalizeStatusRunEvidencePayload(payload);
  const summary = {
    total: normalized.items.length,
    checks: 0,
    pass: 0,
    fail: 0,
    warn: 0,
    unknown: 0,
    quality: statusRunQualityFromEvidence(normalized)
  };
  for (const item of normalized.items) {
    const kind = statusEvidenceItemKind(item);
    const status = item?.status ?? item?.metadata?.status ?? item?.result?.status;
    const checkLike = Boolean(status) || ["acceptance", "check", "quality", "quality_gate", "gate"].includes(kind);
    if (!checkLike) continue;
    summary.checks += 1;
    summary[statusRunAcceptanceStatusKey(status)] += 1;
  }
  if (!summary.checks && summary.quality?.status) {
    summary.checks = 1;
    summary[statusRunAcceptanceStatusKey(summary.quality.status)] += 1;
  }
  summary.tone = summary.fail || summary.quality?.tone === "bad"
    ? "bad"
    : summary.warn || summary.quality?.tone === "warn"
      ? "warn"
      : summary.pass || summary.quality?.tone === "good"
        ? "good"
        : "neutral";
  return summary.total || summary.quality ? summary : null;
}

function normalizeStatusRunEvidencePayload(payload) {
  const source = payload && typeof payload === "object" ? payload : {};
  const items = Array.isArray(payload)
    ? payload
    : Array.isArray(source.items)
      ? source.items
      : Array.isArray(source.evidence)
        ? source.evidence
        : Array.isArray(source.artifacts)
          ? source.artifacts
          : [];
  const quality = source.quality || source.quality_summary || source.quality_gate || {};
  return { items: items.filter(Boolean), quality };
}

function statusRunQualityFromEvidence(normalized = {}) {
  const direct = normalized.quality && typeof normalized.quality === "object" && !Array.isArray(normalized.quality) ? normalized.quality : {};
  const qualityItem = normalizeCollection(normalized.items).find(item => {
    const kind = statusEvidenceItemKind(item);
    return ["quality", "quality_gate", "gate"].includes(kind);
  }) || {};
  const source = { ...qualityItem, ...direct };
  const outputs = source.outputs || source.result?.outputs || {};
  const status = source.quality_status || outputs.quality_status || source.status || source.result?.status || "";
  const score = source.score ?? source.quality_score ?? outputs.score ?? source.result?.score ?? "";
  const stage = source.stage || source.stage_name || source.name || "";
  const failures = source.failures || outputs.failures || source.failure_reasons || source.summary || source.reason || "";
  if (!status && score === "" && !stage && !failures) return null;
  return {
    status,
    score,
    stage,
    failures,
    tone: statusRunEvidenceTone(status)
  };
}

function statusEvidenceItemKind(item = {}) {
  return String(item.kind || item.type || item.evidence_kind || "").trim().toLowerCase();
}

function statusRunEvidenceTone(status) {
  const key = statusRunAcceptanceStatusKey(status);
  if (key === "pass") return "good";
  if (key === "fail") return "bad";
  if (key === "warn") return "warn";
  return "neutral";
}

function statusRunEvidenceLabel(summary = {}) {
  if (!summary) return t("status.runFocusEvidenceEmpty");
  const parts = [
    summary.total ? t("status.runFocusEvidenceCountShort", { count: summary.total }) : "",
    summary.checks ? t("status.runFocusEvidenceChecksShort", { count: summary.checks }) : "",
    summary.quality?.status ? t("status.runFocusQualityStatusShort", { status: statusRunEvidenceStatusLabel(summary.quality.status) }) : ""
  ].filter(Boolean);
  return parts.join(" / ") || t("status.runFocusEvidenceEmpty");
}

function renderStatusRunEvidence(summary) {
  if (!summary) return "";
  const chips = [
    summary.total ? ["neutral", t("status.runFocusEvidenceCountShort", { count: summary.total })] : null,
    summary.pass ? ["good", t("status.runFocusAcceptancePassedCount", { count: summary.pass })] : null,
    summary.fail ? ["bad", t("status.runFocusAcceptanceFailedCount", { count: summary.fail })] : null,
    summary.warn ? ["warn", t("status.runFocusAcceptanceWarningCount", { count: summary.warn })] : null,
    summary.unknown ? ["neutral", t("status.runFocusAcceptanceUnknownCount", { count: summary.unknown })] : null
  ].filter(Boolean);
  const quality = summary.quality;
  const qualityMeta = quality ? [
    quality.status ? [t("status.runFocusQualityStatus"), statusRunEvidenceStatusLabel(quality.status)] : null,
    quality.score !== "" && quality.score !== null && quality.score !== undefined ? [t("status.runFocusQualityScore"), String(quality.score)] : null,
    quality.stage ? [t("status.runFocusQualityStage"), statusDisplayValue(quality.stage)] : null
  ].filter(Boolean) : [];
  return `<div class="status-run-evidence ${escapeHTML(summary.tone || "neutral")}">
    <div class="status-run-evidence-head">
      <div>
        <strong>${escapeHTML(t("status.runFocusEvidenceTitle"))}</strong>
        <small>${escapeHTML(t("status.runFocusEvidenceHelp"))}</small>
      </div>
      ${quality?.status ? `<span class="badge ${escapeHTML(statusRunEvidenceTone(quality.status))}">${escapeHTML(statusRunEvidenceStatusLabel(quality.status))}</span>` : ""}
    </div>
    ${chips.length ? `<div class="status-run-acceptance-chips">${chips.map(([tone, label]) => `<span class="badge ${escapeHTML(tone)}">${escapeHTML(label)}</span>`).join("")}</div>` : ""}
    ${quality ? `<div class="status-run-quality">
      ${quality.failures ? `<div class="run-markdown status-run-quality-text">${renderSafeMarkdown(truncateText(localizedText(String(quality.failures)), 220))}</div>` : ""}
      ${qualityMeta.length ? `<div class="status-run-evidence-meta">${qualityMeta.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(statusDisplayValue(value))}</strong></span>`).join("")}</div>` : ""}
    </div>` : ""}
  </div>`;
}

function statusRunEvidenceStatusLabel(status) {
  const key = statusRunAcceptanceStatusKey(status);
  if (key === "pass") return t("status.runFocusQualityPassed");
  if (key === "fail") return t("status.runFocusQualityFailed");
  if (key === "warn") return t("status.runFocusQualityWarning");
  return localizedText(status || t("status.runFocusQualityUnknown"));
}

function statusRunAcceptanceStatusKey(status) {
  const value = String(status || "").trim().toLowerCase();
  if (["pass", "passed", "ok", "success", "true"].includes(value)) return "pass";
  if (["fail", "failed", "error", "false"].includes(value)) return "fail";
  if (["warn", "warning", "partial"].includes(value)) return "warn";
  return "unknown";
}

function statusRunAcceptanceLabel(summary = {}) {
  return [
    summary.pass ? t("status.runFocusAcceptancePassedCount", { count: summary.pass }) : "",
    summary.fail ? t("status.runFocusAcceptanceFailedCount", { count: summary.fail }) : "",
    summary.warn ? t("status.runFocusAcceptanceWarningCount", { count: summary.warn }) : "",
    summary.unknown ? t("status.runFocusAcceptanceUnknownCount", { count: summary.unknown }) : ""
  ].filter(Boolean).join(" / ");
}

function renderStatusRunAcceptance(summary = {}) {
  if (!summary.total) return "";
  const chips = [
    summary.pass ? ["good", t("status.runFocusAcceptancePassedCount", { count: summary.pass })] : null,
    summary.fail ? ["bad", t("status.runFocusAcceptanceFailedCount", { count: summary.fail })] : null,
    summary.warn ? ["warn", t("status.runFocusAcceptanceWarningCount", { count: summary.warn })] : null,
    summary.unknown ? ["neutral", t("status.runFocusAcceptanceUnknownCount", { count: summary.unknown })] : null
  ].filter(Boolean);
  return `<div class="status-run-acceptance ${escapeHTML(summary.tone)}">
    <div>
      <strong>${escapeHTML(t("status.runFocusAcceptanceTitle"))}</strong>
      <small>${escapeHTML(t("status.runFocusAcceptanceHelp"))}</small>
    </div>
    <div class="status-run-acceptance-chips">
      ${chips.map(([tone, label]) => `<span class="badge ${escapeHTML(tone)}">${escapeHTML(label)}</span>`).join("")}
    </div>
  </div>`;
}

function renderStatusRunFocusActions(focus) {
  const status = String(focus.status || "").toLowerCase();
  const actions = [];
  if (!focus.runID && !focus.workflow) {
    actions.push(["workflows", t("status.runFocusOpenWorkflows"), "primary"]);
    actions.push(["playground", t("status.runFocusOpenRun"), ""]);
  } else if (status === "awaiting_tool_approval" || status === "awaiting_approval") {
    actions.push(["approvals", t("status.runFocusOpenApprovals"), "primary"]);
    actions.push(["playground", t("status.runFocusOpenRun"), ""]);
  } else {
    actions.push(["playground", t("status.runFocusOpenRun"), "primary"]);
  }
  const runKey = statusRunFocusNavigationKey(focus);
  const runLabel = shortStatusRunID(focus.runID) || focus.workflow || t("common.none");
  return `<div class="status-run-actions">
    ${actions.map(([view, label, tone]) => {
      const title = t(runKey ? "status.runFocusActionTitle" : "status.runFocusActionEmptyTitle", { action: label, run: runLabel });
      return `<button type="button" class="status-run-action ${escapeHTML(tone)}" data-status-link="${escapeHTML(view)}" data-status-run-key="${escapeHTML(runKey)}" aria-label="${escapeHTML(title)}" title="${escapeHTML(title)}">${escapeHTML(label)}</button>`;
    }).join("")}
  </div>`;
}

function statusRunFocusNavigationKey(focus = {}) {
  const runID = String(focus.runID || "").trim();
  if (!runID) return "";
  return `${focus.runType === "agent" ? "agent" : "workflow"}:${runID}`;
}

function persistStatusRunFocusNavigation(button, view = "") {
  if (view !== "playground") return;
  const runKey = String(button?.dataset?.statusRunKey || "").trim();
  if (!runKey) return;
  try {
    sessionStorage.setItem("goflow.runHistory.focus", JSON.stringify({
      runKey,
      source: "status-run-focus",
      createdAt: Date.now()
    }));
  } catch {
    // Navigation still works when session storage is unavailable.
  }
}

function statusRunCount(run, arrayKey, countKey, altCountKey = "") {
  if (Array.isArray(run?.[arrayKey])) return run[arrayKey].length;
  if (Array.isArray(run?.[countKey])) return run[countKey].length;
  if (altCountKey && Array.isArray(run?.[altCountKey])) return run[altCountKey].length;
  const value = Number(run?.[countKey] ?? (altCountKey ? run?.[altCountKey] : undefined) ?? 0);
  return Number.isFinite(value) ? value : 0;
}

function statusRunTerminalSuccess(status) {
  return ["completed", "complete", "done", "success", "succeeded", "finished"].includes(String(status || "").toLowerCase());
}

function statusRunTerminalError(status) {
  return ["failed", "error", "denied", "blocked", "cancelled", "canceled"].includes(String(status || "").toLowerCase());
}

function statusRunLabel(status) {
  const value = String(status || "").toLowerCase();
  if (value === "awaiting_tool_approval") return t("status.runStatus.awaitingToolApproval");
  if (value === "awaiting_approval") return t("status.runStatus.awaitingApproval");
  if (value === "awaiting_input") return t("status.runStatus.awaitingInput");
  if (value === "awaiting_sub_workflow") return t("status.runStatus.awaitingSubWorkflow");
  if (value === "running") return t("status.runStatus.running");
  if (statusRunTerminalSuccess(value)) return t("status.runStatus.completed");
  if (["failed", "error"].includes(value)) return t("status.runStatus.failed");
  if (value === "denied") return t("status.runStatus.denied");
  if (["cancelled", "canceled"].includes(value)) return t("status.runStatus.cancelled");
  if (value === "blocked") return t("status.runStatus.blocked");
  return status || t("status.workflowIdle");
}

function statusRunFocusBody(focus) {
  const status = String(focus.status || "").toLowerCase();
  if (focus.runType === "agent") {
    if (status === "awaiting_tool_approval") return t("status.runFocusAgentApprovalBody", { agent: focus.workflow || t("common.none") });
    if (isActiveAgentStatus(status)) return t("status.runFocusAgentRunningBody", { agent: focus.workflow || t("common.none") });
    if (statusRunTerminalError(status)) return t("status.runFocusAgentErrorBody");
    if (statusRunTerminalSuccess(status)) return t("status.runFocusAgentDoneBody");
    return t("status.runFocusAgentReviewBody");
  }
  if (status === "awaiting_input") return t("status.runFocusInputBody", { stage: statusDisplayValue(focus.nextStage || t("common.none")) });
  if (status === "awaiting_tool_approval" || status === "awaiting_approval") return t("status.runFocusApprovalBody", { stage: statusDisplayValue(focus.nextStage || t("common.none")) });
  if (status === "awaiting_sub_workflow") return t("status.runFocusSubWorkflowBody", { stage: statusDisplayValue(focus.nextStage || t("common.none")) });
  if (isActiveWorkflowStatus(status)) return t("status.runFocusRunningBody", { stage: statusDisplayValue(focus.nextStage || t("common.none")) });
  if (statusRunTerminalError(status)) return t("status.runFocusErrorBody");
  if (statusRunTerminalSuccess(status)) return t("status.runFocusDoneBody");
  return t("status.runFocusReviewBody");
}

function shortStatusRunID(id) {
  const value = String(id || "").trim();
  if (!value || value.length <= 28) return value;
  return `${value.slice(0, 18)}...${value.slice(-8)}`;
}

function formatStatusTime(value) {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  });
}

function normalizeStatusTeamState(payload, context = {}) {
  const state = payload && typeof payload === "object" ? payload : {};
  const result = {
    run_id: String(state.run_id || context.runID || "").trim(),
    workflow: String(state.workflow || context.workflow || "").trim(),
    status: String(state.status || context.status || "").trim(),
    team: String(state.team || "").trim(),
    active_owner: String(state.active_owner || "").trim(),
    next_stage: String(state.next_stage || context.nextStage || "").trim(),
    team_stage: String(state.team_stage || "").trim(),
    pending_input: Boolean(state.pending_input),
    approval_gate: state.approval_gate && typeof state.approval_gate === "object" ? state.approval_gate : null,
    messages: normalizeCollection(state.messages),
    handoffs: normalizeCollection(state.handoffs),
    blackboard: normalizeCollection(state.blackboard),
    decisions: normalizeCollection(state.decisions),
    critiques: normalizeCollection(state.critiques),
    questions: normalizeCollection(state.questions),
    risks: normalizeCollection(state.risks),
    assignments: normalizeCollection(state.assignments),
    escalations: normalizeCollection(state.escalations),
    approvals: normalizeCollection(state.approvals),
    rejections: normalizeCollection(state.rejections),
    unresolved_items: normalizeCollection(state.unresolved_items),
    pending_approvals: normalizeCollection(state.pending_approvals),
    other_records: []
  };
  result.other_records = statusTeamOtherRecords(result);
  return result;
}

function renderStatusTeamState(teamState) {
  const count = statusTeamSignalCount(teamState);
  const hasContext = Boolean(teamState.run_id || teamState.workflow || teamState.status || teamState.team);
  if (!count && !hasContext) {
    return `<div class="status-team-card status-team-empty">
      <strong>${escapeHTML(t("status.teamEmptyTitle"))}</strong>
      <span>${escapeHTML(t("status.teamEmptyHelp"))}</span>
      <div class="status-empty-actions">
        <button type="button" data-status-link="workflows">${escapeHTML(t("status.openWorkflows"))}</button>
        <button type="button" data-status-link="approvals">${escapeHTML(t("status.openApprovals"))}</button>
      </div>
    </div>`;
  }
  return `<div class="status-team-card">
    ${renderStatusTeamFacts(teamState)}
    ${renderStatusApprovalGate(teamState.approval_gate)}
    <div class="status-team-groups">
      ${renderStatusTeamGroup(t("status.teamDecisions"), teamState.decisions, "decision")}
      ${renderStatusTeamGroup(t("status.teamRisks"), teamState.risks, "risk")}
      ${renderStatusTeamGroup(t("status.teamQuestions"), teamState.questions, "question")}
      ${renderStatusTeamGroup(t("status.teamCritiques"), teamState.critiques, "critique")}
      ${renderStatusTeamGroup(t("status.teamOtherRecords"), teamState.other_records, "record")}
    </div>
    ${renderStatusTeamBadges(teamState)}
  </div>`;
}

function renderStatusTeamFacts(teamState) {
  const facts = [
    [t("status.teamRun"), teamState.run_id],
    [t("status.teamWorkflow"), teamState.workflow],
    [t("status.teamTeam"), teamState.team],
    [t("status.teamOwner"), teamState.active_owner],
    [t("status.teamStage"), teamState.next_stage || teamState.team_stage],
    [t("status.teamState"), localizedText(teamState.status)]
  ].filter(([, value]) => String(value || "").trim());
  if (!facts.length) return "";
  return `<div class="status-team-facts">
    ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(statusDisplayValue(value))}</strong></span>`).join("")}
  </div>`;
}

function renderStatusApprovalGate(gate) {
  if (!gate || typeof gate !== "object" || !String(gate.status || "").trim()) return "";
  const detail = [
    gate.required ? t("status.teamApprovalGateProgress", { approved: Number(gate.approved || 0), required: Number(gate.required || 0) }) : "",
    gate.rejected ? t("status.teamRejected", { count: Number(gate.rejected || 0) }) : "",
    Array.isArray(gate.roles) && gate.roles.length ? gate.roles.map(localizedText).join(", ") : ""
  ].filter(Boolean).join(" / ");
  return `<div class="status-team-gate">
    <span class="badge ${String(gate.status).toLowerCase() === "approved" ? "good" : "warn"}">${escapeHTML(localizedText(gate.status))}</span>
    <div>
      <strong>${escapeHTML(t("status.teamApprovalGate"))}</strong>
      ${detail ? `<small>${escapeHTML(detail)}</small>` : ""}
    </div>
  </div>`;
}

function renderStatusTeamGroup(title, items, tone) {
  const visible = normalizeCollection(items).slice(0, 2);
  if (!visible.length) return "";
  return `<section class="status-team-group ${tone}">
    <div class="status-team-group-head">
      <strong>${escapeHTML(title)}</strong>
      <small>${visible.length}</small>
    </div>
    ${visible.map(item => renderStatusTeamItem(item, tone)).join("")}
  </section>`;
}

function renderStatusTeamItem(item, tone) {
  const meta = [
    item.stage ? `${t("status.teamStage")}: ${localizedText(item.stage)}` : "",
    item.agent_id ? `${t("chat.agent")}: ${localizedText(item.agent_id)}` : "",
    item.status ? `${t("status.teamState")}: ${localizedText(item.status)}` : ""
  ].filter(Boolean).join(" / ");
  const title = localizedText(item.title || item.subject || statusTeamFallbackTitle(tone));
  const content = localizedText(item.content || item.summary || t("status.teamNoContent"));
  return `<article class="status-team-item ${tone}">
    <strong>${escapeHTML(title)}</strong>
    ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
    <div class="run-markdown status-team-markdown">${renderSafeMarkdown(truncateText(content, 220))}</div>
  </article>`;
}

function renderStatusTeamBadges(teamState) {
  const badges = [];
  if (teamState.pending_input) badges.push(t("status.teamPendingInput"));
  if (teamState.pending_approvals?.length) badges.push(t("status.teamPendingApprovals", { count: teamState.pending_approvals.length }));
  if (teamState.handoffs?.length) badges.push(t("status.teamHandoffs", { count: teamState.handoffs.length }));
  if (teamState.unresolved_items?.length) badges.push(t("status.teamUnresolved", { count: teamState.unresolved_items.length }));
  if (!badges.length) return "";
  return `<div class="status-team-tags">${badges.map(item => `<span>${escapeHTML(item)}</span>`).join("")}</div>`;
}

function statusTeamFallbackTitle(tone) {
  if (tone === "decision") return t("status.teamDecisions");
  if (tone === "risk") return t("status.teamRisks");
  if (tone === "question") return t("status.teamQuestions");
  if (tone === "critique") return t("status.teamCritiques");
  if (tone === "record") return t("status.teamOtherRecords");
  return t("status.teamTitle");
}

function statusTeamSignalCount(teamState) {
  return ["decisions", "risks", "questions", "critiques", "other_records", "handoffs", "unresolved_items", "pending_approvals"]
    .reduce((total, key) => total + (teamState[key]?.length || 0), 0)
    + (teamState.pending_input ? 1 : 0)
    + (teamState.approval_gate ? 1 : 0);
}

function summarizeMCPPressure(runtime = {}) {
  const servers = Array.isArray(runtime.mcp_servers) ? runtime.mcp_servers : [];
  const visible = servers.filter(server => server && server.enabled !== false);
  const totals = visible.reduce((acc, server) => {
    const active = safeCount(server.active_calls);
    const queued = safeCount(server.queued_calls);
    const max = safeCount(server.max_concurrent_calls);
    const available = server.available_call_slots == null
      ? Math.max(0, max - active)
      : safeCount(server.available_call_slots);
    acc.active += active;
    acc.queued += queued;
    acc.available += available;
    acc.max += max;
    if (queued > 0) acc.blocked += 1;
    if (active > 0) acc.busy += 1;
    return acc;
  }, { active: 0, queued: 0, available: 0, max: 0, busy: 0, blocked: 0 });
  const tone = totals.queued ? "warn" : totals.active ? "neutral" : "good";
  const badge = !visible.length
    ? t("status.mcpNoServers")
    : totals.queued
      ? t("status.mcpQueuedBadge", { count: totals.queued })
      : totals.active
        ? t("status.mcpActiveBadge", { count: totals.active })
        : t("status.mcpIdleBadge");
  const metricHelp = !visible.length
    ? t("status.metric.toolsNone")
    : totals.queued
      ? t("status.metric.toolsQueued", { active: totals.active, queued: totals.queued })
      : totals.active
        ? t("status.metric.toolsActive", { active: totals.active })
        : t("status.metric.toolsIdle", { count: visible.length });
  return { servers: visible, ...totals, tone, badge, metricHelp };
}

function renderMCPPressure(summary) {
  if (!summary.servers.length) {
    return `<div class="status-mcp-shell status-mcp-empty">
      <strong>${escapeHTML(t("status.mcpEmptyTitle"))}</strong>
      <span>${escapeHTML(t("status.mcpEmptyHelp"))}</span>
      <div class="status-empty-actions">
        <button type="button" data-status-link="settings">${escapeHTML(t("status.openSettings"))}</button>
      </div>
    </div>`;
  }
  return `<div class="status-mcp-shell">
    <div class="status-mcp-summary">
      ${mcpPressureStat(t("status.mcpActiveCalls"), numberText(summary.active), t("status.mcpActiveCallsHelp"))}
      ${mcpPressureStat(t("status.mcpQueuedCalls"), numberText(summary.queued), t("status.mcpQueuedCallsHelp"))}
      ${mcpPressureStat(t("status.mcpAvailableSlots"), numberText(summary.available), t("status.mcpAvailableSlotsHelp"))}
      ${mcpPressureStat(t("status.mcpServers"), numberText(summary.servers.length), t("status.mcpServersHelp"))}
    </div>
    <div class="status-mcp-list">
      ${summary.servers.map(renderMCPServerPressure).join("")}
    </div>
  </div>`;
}

function mcpPressureStat(label, value, help) {
  return `<div class="status-mcp-stat">
    <span>${escapeHTML(label)}</span>
    <strong>${escapeHTML(value)}</strong>
    <small>${escapeHTML(help)}</small>
  </div>`;
}

function renderMCPServerPressure(server = {}) {
  const active = safeCount(server.active_calls);
  const queued = safeCount(server.queued_calls);
  const max = safeCount(server.max_concurrent_calls);
  const available = server.available_call_slots == null ? Math.max(0, max - active) : safeCount(server.available_call_slots);
  const tone = queued ? "warn" : active ? "neutral" : "good";
  const health = statusDisplayValue(server.health || t("status.mcpHealthUnknown"));
  const utilization = max > 0 ? Math.min(100, Math.round((active / max) * 100)) : 0;
  const detail = queued
    ? t("status.mcpServerQueued", { queued })
    : active
      ? t("status.mcpServerActive", { active })
      : t("status.mcpServerIdle");
  return `<article class="status-mcp-server ${tone}">
    <div class="status-mcp-server-head">
      <div>
        <strong>${escapeHTML(statusDisplayValue(server.name || t("status.mcpServer")))}</strong>
        <span>${escapeHTML(detail)}</span>
      </div>
      <span class="badge ${tone}">${escapeHTML(health)}</span>
    </div>
    <div class="status-mcp-bar" aria-label="${escapeHTML(t("status.mcpUtilization", { value: utilization }))}">
      <span style="width: ${utilization}%"></span>
    </div>
    <div class="status-mcp-facts">
      ${mcpPressureFact(t("status.mcpFactActive"), active)}
      ${mcpPressureFact(t("status.mcpFactQueued"), queued)}
      ${mcpPressureFact(t("status.mcpFactAvailable"), available)}
      ${mcpPressureFact(t("status.mcpFactMax"), max || t("common.none"))}
    </div>
  </article>`;
}

function mcpPressureFact(label, value) {
  const display = typeof value === "number" ? numberText(value) : statusDisplayValue(value);
  return `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(display)}</strong></span>`;
}

function safeCount(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number < 0) return 0;
  return Math.floor(number);
}

function statusTeamOtherRecords(teamState) {
  const explicit = [
    ...normalizeCollection(teamState.assignments),
    ...normalizeCollection(teamState.escalations),
    ...normalizeCollection(teamState.approvals),
    ...normalizeCollection(teamState.rejections)
  ];
  const knownIDs = new Set(
    ["decisions", "risks", "questions", "critiques", "assignments", "escalations", "approvals", "rejections"]
      .flatMap(key => normalizeCollection(teamState[key]))
      .map(statusTeamRecordID)
      .filter(Boolean)
  );
  const knownKinds = [
    "team_decision", "decision", "approval",
    "team_critique", "critique", "finding", "findings",
    "team_unresolved_question", "question", "issue",
    "team_risk", "risk",
    "team_assignment", "team_escalation", "team_approval", "team_rejection"
  ];
  const fallback = normalizeCollection(teamState.blackboard).filter(item => {
    const id = statusTeamRecordID(item);
    if (id && knownIDs.has(id)) return false;
    return !knownKinds.includes(String(item?.kind || "").trim().toLowerCase());
  });
  return [...explicit, ...fallback];
}

function statusTeamRecordID(item) {
  return String(item?.id || item?.ID || "").trim();
}

function renderCostDiagnostics(cost = {}, auxiliaryModels = []) {
  const recommendations = Array.isArray(cost.recommendations) ? cost.recommendations : [];
  const tuning = Array.isArray(cost.tuning) ? cost.tuning : [];
  const toolDiagnostics = renderToolSchemaDiagnostics(cost);
  const contextDiagnostics = renderPromptContextDiagnostics(cost);
  return `<div class="status-cost-shell">
    <div class="status-cost-overview">
      <div class="status-cost-summary">
        ${costStat(t("status.cost.samples"), numberText(cost.samples), t("status.cost.samplesHelp"), "samples")}
        ${costStat(t("status.cost.avgPrompt"), numberText(cost.average_estimated_prompt_tokens), t("status.cost.avgPromptHelp"), "prompt")}
        ${costStat(t("status.cost.nonCacheable"), numberText(cost.average_non_cacheable_tokens), t("status.cost.nonCacheableHelp"), "cache")}
        ${costStat(t("status.cost.promptPrefixes"), numberText(cost.unique_prompt_prefixes), t("status.cost.promptPrefixesHelp"), "prefix")}
      </div>
    </div>
    ${toolDiagnostics || contextDiagnostics ? `<div class="status-cost-diagnostics-grid">${contextDiagnostics}${toolDiagnostics}</div>` : ""}
    ${renderCostTuning(tuning)}
    <div class="status-cost-columns">
      <section>
        <div class="status-cost-copy">
          <strong>${t("status.cost.recommendations")}</strong>
          <p class="muted">${escapeHTML(t("status.cost.recommendationsHelp"))}</p>
        </div>
        <div class="status-recommendation-list">
          ${recommendations.length ? recommendations.slice(0, 4).map(renderCostRecommendation).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noAdvice"))}</div>`}
        </div>
      </section>
      <section>
        <strong>${t("status.cost.auxiliaryModels")}</strong>
        <div class="status-aux-list">
          ${auxiliaryModels.length ? auxiliaryModels.map(renderAuxiliaryModel).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noAuxiliaryModels"))}</div>`}
        </div>
      </section>
    </div>
    <div class="status-trend-grid">
      ${renderCostTrendGroup(t("status.cost.byAgent"), cost.by_agent, "agent")}
      ${renderCostTrendGroup(t("status.cost.byMode"), cost.by_mode, "mode")}
      ${renderCostTrendGroup(t("status.cost.byStage"), cost.by_stage, "stage")}
    </div>
  </div>`;
}

function renderPromptContextDiagnostics(cost = {}) {
  const latest = cost.latest && typeof cost.latest === "object" ? cost.latest : {};
  const memoryBlocks = Array.isArray(latest.memory_blocks) ? latest.memory_blocks : [];
  const omitted = Array.isArray(latest.omitted_context) ? latest.omitted_context : [];
  const artifactRefs = Array.isArray(latest.artifact_refs) ? latest.artifact_refs : [];
  const hasData = memoryBlocks.length || omitted.length || artifactRefs.length || latest.skill_instruction_mode || cost.memory_block_samples || cost.artifact_ref_samples || cost.skill_omitted_tokens;
  if (!hasData) return "";
  const savedTokens = (latest.memory_estimated_saved_tokens || cost.memory_estimated_saved_tokens || 0)
    + (latest.artifact_omitted_tokens || cost.artifact_omitted_tokens || 0)
    + (latest.skill_omitted_tokens || cost.skill_omitted_tokens || 0)
    + (latest.history_estimated_saved_tokens || cost.history_estimated_saved_tokens || 0);
  const omittedCount = omitted.length || cost.omitted_context_count || 0;
  const refCount = latest.artifact_ref_count || cost.artifact_ref_samples || artifactRefs.length || 0;
  const flow = [
    [t("status.cost.contextInputBudget"), numberText(latest.estimated_prompt_tokens || cost.average_estimated_prompt_tokens), t("status.cost.contextInputBudgetHelp"), "input"],
    [t("status.cost.contextSavedBudget"), numberText(savedTokens), t("status.cost.contextSavedBudgetHelp"), "saved"],
    [t("status.cost.contextLazyRefs"), numberText(refCount), t("status.cost.contextLazyRefsHelp"), "refs"],
    [t("status.cost.contextOmitted"), numberText(omittedCount), t("status.cost.contextOmittedHelp"), "omitted"]
  ];
  const stats = [
    [t("status.cost.memoryBlocks"), numberText(latest.memory_block_count || cost.memory_block_samples)],
    [t("status.cost.memorySaved"), numberText(latest.memory_estimated_saved_tokens || cost.memory_estimated_saved_tokens)],
    [t("status.cost.artifactRefs"), numberText(latest.artifact_ref_count || cost.artifact_ref_samples)],
    [t("status.cost.artifactOmitted"), numberText(latest.artifact_omitted_tokens || cost.artifact_omitted_tokens)],
    [t("status.cost.skillMode"), statusDisplayValue(latest.skill_instruction_mode || "")],
    [t("status.cost.skillSaved"), numberText(latest.skill_omitted_tokens || cost.skill_omitted_tokens)]
  ].filter(([, value]) => value && value !== "0");
  return `<section class="status-context-diagnostics">
    <div class="status-cost-section-head">
      <div>
        <strong>${escapeHTML(t("status.cost.contextTitle"))}</strong>
        <span>${escapeHTML(t("status.cost.contextHelp"))}</span>
      </div>
      <small>${escapeHTML(t("status.cost.contextOmittedCount", { count: omitted.length || cost.omitted_context_count || 0 }))}</small>
    </div>
    <div class="status-context-flow">
      ${flow.map(([label, value, help, tone]) => `<span class="status-context-flow-card ${escapeHTML(tone)}"><small>${escapeHTML(label)}</small><strong>${escapeHTML(statusDisplayValue(value))}</strong><em>${escapeHTML(help)}</em></span>`).join("")}
    </div>
    ${stats.length ? `<div class="status-context-stats">${stats.map(([label, value]) => costTuningFactHTML(label, value)).join("")}</div>` : ""}
    <div class="status-context-columns">
      <section>
        <strong>${escapeHTML(t("status.cost.injectedMemory"))}</strong>
        <div class="status-context-list">
          ${memoryBlocks.length ? memoryBlocks.slice(0, 5).map(renderPromptMemoryBlock).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noMemoryBlocks"))}</div>`}
        </div>
      </section>
      <section>
        <strong>${escapeHTML(t("status.cost.refsAndOmitted"))}</strong>
        <div class="status-context-list">
          ${artifactRefs.slice(0, 3).map(ref => renderPromptContextLine(t("status.cost.artifactRef"), ref, "artifact")).join("")}
          ${omitted.slice(0, 4).map(item => renderPromptContextLine(t("status.cost.omitted"), item, "omitted")).join("")}
          ${!artifactRefs.length && !omitted.length ? `<div class="status-cost-empty">${escapeHTML(t("status.cost.noOmittedContext"))}</div>` : ""}
        </div>
      </section>
    </div>
  </section>`;
}

function renderPromptMemoryBlock(block = {}) {
  const label = [statusDisplayValue(block.kind), statusDisplayValue(block.title)].filter(Boolean).join(" / ") || t("status.cost.memoryBlock");
  const reason = promptMemoryBlockReason(block);
  const meta = [
    block.ref ? `ref=${block.ref}` : "",
    block.hash ? `hash=${String(block.hash).slice(0, 12)}` : "",
    block.language ? statusDisplayValue(block.language) : "",
    block.size ? `${numberText(block.size)} B` : "",
    block.content_mode ? statusDisplayValue(block.content_mode) : "",
    block.tokens ? `${numberText(block.tokens)} ${t("chat.tokens")}` : "",
    block.estimated_saved_tokens ? `${t("status.cost.memorySaved")} ${numberText(block.estimated_saved_tokens)}` : "",
    block.score ? `score=${numberText(block.score)}` : ""
  ].filter(Boolean).join(" | ");
  const tone = block.kind === "file" ? "file" : "";
  return `<article class="status-context-line ${escapeHTML(tone)}">
    <span class="status-context-mark" aria-hidden="true"></span>
    <div class="status-context-line-body">
      <span class="badge neutral">${escapeHTML(statusDisplayValue(block.kind || t("status.cost.memoryBlock")))}</span>
      <strong>${escapeHTML(label)}</strong>
      ${reason ? `<em>${escapeHTML(reason)}</em>` : ""}
      ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
    </div>
  </article>`;
}

function promptMemoryBlockReason(block = {}) {
  const mode = String(block.content_mode || "").trim().toLowerCase();
  if (mode === "summary") return t("status.cost.contextReasonSummary");
  if (mode === "full") return t("status.cost.contextReasonFull");
  if (String(block.kind || "").trim().toLowerCase() === "file") return t("status.cost.contextReasonFile");
  if (block.ref) return t("status.cost.contextReasonRef");
  return "";
}

function renderPromptContextLine(label, value, tone) {
  const reason = tone === "artifact"
    ? t("status.cost.contextReasonLazyRef")
    : tone === "omitted"
      ? t("status.cost.contextReasonOmitted")
      : "";
  return `<article class="status-context-line ${escapeHTML(tone || "")}">
    <span class="status-context-mark" aria-hidden="true"></span>
    <div class="status-context-line-body">
      <span class="badge neutral">${escapeHTML(label)}</span>
      <strong>${escapeHTML(statusDisplayValue(value))}</strong>
      ${reason ? `<em>${escapeHTML(reason)}</em>` : ""}
    </div>
  </article>`;
}

function costAttentionCount(cost = {}) {
  const recommendations = Array.isArray(cost.recommendations) ? cost.recommendations.length : 0;
  const tuning = Array.isArray(cost.tuning) ? cost.tuning.filter(item => costTuningNeedsAttention(item)).length : 0;
  return recommendations + tuning;
}

function costTuningNeedsAttention(item = {}) {
  const state = String(item.state || "").toLowerCase();
  if (state === "candidate" || state === "observed_costly") return true;
  return Boolean(item.recommendation || item.action) && state !== "observed_saving";
}

function renderToolSchemaDiagnostics(cost = {}) {
  const latest = cost.latest && typeof cost.latest === "object" ? cost.latest : {};
  const injected = Array.isArray(latest.injected_tool_schemas) ? latest.injected_tool_schemas : [];
  const filtered = Array.isArray(latest.filtered_tool_schemas) ? latest.filtered_tool_schemas : [];
  const hasData = injected.length || filtered.length || latest.exposed_tool_count || latest.filtered_tool_count || latest.tool_schema_tokens;
  if (!hasData) return "";
  const stats = [
    [t("status.cost.injectedTools"), numberText(latest.exposed_tool_count || injected.length)],
    [t("status.cost.filteredTools"), numberText(latest.filtered_tool_count || filtered.length)],
    [t("status.cost.toolSchemaTokens"), numberText(latest.tool_schema_tokens)],
    [t("status.cost.toolSelection"), statusDisplayValue(latest.tool_schema_selection || "")]
  ].filter(([, value]) => value && value !== "0");
  return `<section class="status-tool-schema-diagnostics">
    <div class="status-cost-section-head">
      <div>
        <strong>${escapeHTML(t("status.cost.toolSchemaTitle"))}</strong>
        <span>${escapeHTML(t("status.cost.toolSchemaHelp"))}</span>
      </div>
      <small>${escapeHTML(t("status.cost.toolSchemaOmittedCount", { count: latest.tool_schema_diagnostic_omitted || cost.tool_schema_diagnostic_omitted || 0 }))}</small>
    </div>
    ${stats.length ? `<div class="status-context-stats">${stats.map(([label, value]) => costTuningFactHTML(label, value)).join("")}</div>` : ""}
    <div class="status-context-columns">
      <section>
        <strong>${escapeHTML(t("status.cost.injectedToolSchemas"))}</strong>
        <div class="status-context-list">
          ${injected.length ? injected.slice(0, 6).map(renderToolSchemaLine).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noInjectedToolSchemas"))}</div>`}
        </div>
      </section>
      <section>
        <strong>${escapeHTML(t("status.cost.filteredToolSchemas"))}</strong>
        <div class="status-context-list">
          ${filtered.length ? filtered.slice(0, 6).map(renderToolSchemaLine).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noFilteredToolSchemas"))}</div>`}
        </div>
      </section>
    </div>
  </section>`;
}

function renderToolSchemaLine(item = {}) {
  const label = statusDisplayValue(item.qualified_name || item.name || t("status.cost.toolSchema"));
  const meta = [
    item.kind ? statusDisplayValue(item.kind) : "",
    item.tokens ? `${numberText(item.tokens)} ${t("chat.tokens")}` : "",
    item.schema_hash ? `hash=${item.schema_hash}` : ""
  ].filter(Boolean).join(" | ");
  const reason = statusDisplayValue(item.reason || "");
  return `<article class="status-context-line tool-schema ${escapeHTML(item.status || "")}">
    <span class="status-context-mark" aria-hidden="true"></span>
    <div class="status-context-line-body">
      <span class="badge neutral">${escapeHTML(statusDisplayValue(item.status || t("status.cost.toolSchema")))}</span>
      <strong>${escapeHTML(label)}</strong>
      ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
      ${reason ? `<small>${escapeHTML(reason)}</small>` : ""}
    </div>
  </article>`;
}

function renderCostTuning(items = []) {
  const rows = Array.isArray(items) ? items : [];
  return `<section class="status-cost-tuning">
    <div class="status-cost-section-head">
      <div>
        <strong>${escapeHTML(t("status.cost.tuningTitle"))}</strong>
        <span>${escapeHTML(t("status.cost.tuningHelp"))}</span>
      </div>
      <small>${escapeHTML(rows.length ? t("status.cost.tuningRoutes", { count: rows.length }) : t("status.cost.tuningNoRoutes"))}</small>
    </div>
    <div class="status-tuning-list">
      ${rows.length ? rows.map(renderCostTuningCard).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.tuningEmpty"))}</div>`}
    </div>
  </section>`;
}

function renderCostTuningCard(item = {}) {
  const tone = costTuningTone(item);
  const kind = costRouteKindLabel(item.kind || t("status.cost.tuningRoute"));
  const state = costTuningStateLabel(item.state);
  const route = [kind, statusDisplayValue(item.provider), statusDisplayValue(item.model)].filter(Boolean).join(" / ") || t("status.cost.tuningRoute");
  const facts = [
    [t("status.cost.tuningEnabled"), item.enabled ? t("status.cost.enabled") : t("status.cost.disabled")],
    [t("status.cost.tuningConfigured"), item.configured ? t("status.cost.tuningConfiguredYes") : t("status.cost.tuningConfiguredNo")],
    [t("status.cost.tuningObservedSamples"), numberText(item.observed_samples)],
    [t("status.cost.tuningPromptBudgetSamples"), numberText(item.prompt_budget_samples)],
    [t("status.cost.tuningTokenUsageSamples"), numberText(item.token_usage_samples)],
    [t("status.cost.tuningObservedTokens"), numberText(item.observed_total_tokens)],
    [t("status.cost.tuningCandidatePrompt"), numberText(item.candidate_average_prompt_tokens)],
    [t("status.cost.tuningExtraCall"), numberText(item.estimated_extra_call_tokens)],
    [t("status.cost.tuningMainPrompt"), numberText(item.estimated_main_prompt_tokens)],
    [t("status.cost.tuningNetSignal"), signedNumberText(item.net_savings_signal)]
  ].filter(([, value]) => value !== "" && value !== "0").slice(0, 7);
  const checklist = Array.isArray(item.quality_checklist) ? item.quality_checklist.filter(Boolean).slice(0, 3) : [];
  const guidance = statusDisplayValue(item.recommendation || item.action || costTuningStateHelp(item.state));
  return `<article class="status-tuning-card ${escapeHTML(tone)}">
    <div class="status-tuning-head">
      <div>
        <strong>${escapeHTML(route)}</strong>
        <span>${escapeHTML(guidance)}</span>
      </div>
      <span class="badge ${escapeHTML(tone)}">${escapeHTML(state)}</span>
    </div>
    ${facts.length ? `<div class="status-tuning-facts">${facts.map(([label, value]) => costTuningFactHTML(label, value)).join("")}</div>` : ""}
    ${checklist.length ? `<div class="status-tuning-checklist">
      <small>${escapeHTML(t("status.cost.tuningQualityChecklist"))}</small>
      <ul>${checklist.map(item => `<li>${escapeHTML(statusDisplayValue(item))}</li>`).join("")}</ul>
    </div>` : ""}
  </article>`;
}

function costTuningFactHTML(label, value) {
  return `<span class="status-cost-fact"><small>${escapeHTML(label)}</small><strong>${escapeHTML(statusDisplayValue(value))}</strong></span>`;
}

function costTuningTone(item = {}) {
  const state = String(item.state || "").toLowerCase();
  if (state === "observed_saving") return "good";
  if (state === "observed_costly") return "warn";
  if (state === "candidate") return "neutral";
  return "neutral";
}

function costTuningStateLabel(state) {
  const value = String(state || "not_enough_data").toLowerCase();
  const key = `status.cost.tuningState.${value}`;
  const translated = t(key);
  return translated === key ? statusDisplayValue(value.replaceAll("_", " ")) : translated;
}

function costTuningStateHelp(state) {
  const value = String(state || "not_enough_data").toLowerCase();
  const key = `status.cost.tuningStateHelp.${value}`;
  const translated = t(key);
  return translated === key ? t("status.cost.tuningStateHelp.not_enough_data") : translated;
}

function costStat(label, value, help, tone = "") {
  return `<div class="status-cost-stat ${escapeHTML(tone)}">
    <span>${escapeHTML(label)}</span>
    <strong>${escapeHTML(value)}</strong>
    <small>${escapeHTML(help)}</small>
  </div>`;
}

function renderCostRecommendation(item) {
  const tone = String(item.level || "").toLowerCase() === "warning" ? "warn" : "neutral";
  const meta = costDimensionMeta([item.agent_id, item.mode, item.workflow_name, item.task_stage]);
  const message = costRecommendationMessage(item);
  return `<article class="status-recommendation ${tone}">
    <span class="badge ${tone}">${escapeHTML(statusDisplayValue(item.level || t("status.tone.neutral")))}</span>
    <p>${escapeHTML(message)}</p>
    ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
  </article>`;
}

function costRecommendationMessage(item = {}) {
  const code = String(item.code || "").trim();
  const key = code ? `status.costRecommendation.${code}` : "";
  const translated = key ? t(key) : "";
  if (translated && translated !== key) return translated;
  return statusDisplayValue(item.message || t("status.cost.recommendationFallback"));
}

function renderAuxiliaryModel(model) {
  const label = [costRouteKindLabel(model.kind), statusDisplayValue(model.provider), statusDisplayValue(model.model)].filter(Boolean).join(" / ") || t("status.cost.auxiliaryModel");
  const detail = [
    model.max_tokens ? `${t("status.cost.maxTokens")} ${numberText(model.max_tokens)}` : "",
    model.temperature != null ? `${t("status.cost.temperature")} ${model.temperature}` : "",
    model.provider_override || model.model_override ? t("status.cost.override") : ""
  ].filter(Boolean).join(" / ");
  return `<article class="status-aux-item ${model.enabled ? "" : "disabled"}">
    <strong>${escapeHTML(label)}</strong>
    <small>${escapeHTML(detail || (model.enabled ? t("status.cost.enabled") : t("status.cost.disabled")))}</small>
  </article>`;
}

function renderCostTrendGroup(title, items, kind) {
  const rows = Array.isArray(items) ? items.slice(0, 3) : [];
  return `<section class="status-trend-card">
    <div class="status-trend-head">
      <strong>${escapeHTML(title)}</strong>
      <span>${escapeHTML(rows.length ? t("status.cost.topItems") : t("status.cost.noData"))}</span>
    </div>
    <div class="status-trend-list">
      ${rows.length ? rows.map(item => renderCostTrendItem(item, kind)).join("") : `<div class="status-cost-empty">${escapeHTML(t("status.cost.noTrendData"))}</div>`}
    </div>
  </section>`;
}

function renderCostTrendItem(item, kind) {
  const label = costDimensionLabel(item.agent_id || item.mode || item.key || item.task_stage || "-");
  const sub = kind === "stage" && item.workflow_name
    ? costDimensionMeta([item.workflow_name, item.task_stage || item.key])
    : costDimensionLabel(item.task_stage || "");
  const total = item.total_tokens || item.total_prompt_tokens + item.total_output_tokens || item.average_estimated_prompt_tokens || 0;
  return `<div class="status-trend-item">
    <span>
      <strong>${escapeHTML(label)}</strong>
      ${sub ? `<small>${escapeHTML(sub)}</small>` : ""}
    </span>
    <b>${escapeHTML(numberText(total))}</b>
  </div>`;
}

function costDimensionLabel(value) {
  return statusDisplayValue(value || "");
}

function costRouteKindLabel(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  const normalized = raw.toLowerCase().replaceAll("_", "-");
  const key = `status.cost.routeKind.${normalized}`;
  const translated = t(key);
  return translated === key ? statusDisplayValue(raw) : translated;
}

function costDimensionMeta(values = []) {
  return values.filter(Boolean).map(costDimensionLabel).filter(Boolean).join(" / ");
}

function numberText(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? number.toLocaleString() : "0";
}

function signedNumberText(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) return "0";
  return `${number > 0 ? "+" : ""}${number.toLocaleString()}`;
}

function statusDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const translated = localizedText(text);
  if (translated !== text) return translated;
  if (statusLooksTechnical(text)) return text;
  return translated;
}

function statusLooksTechnical(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return true;
  if (text.includes("\\") || text.includes("://")) return true;
  if (text.includes("/") && !/\s/.test(text)) return true;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(text)) return true;
  return false;
}

function normalizeCollection(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.items)) return value.items;
  if (Array.isArray(value?.entries)) return value.entries;
  if (Array.isArray(value?.messages)) return value.messages;
  return [];
}

function truncateText(value, limit) {
  const text = String(value || "").trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}...`;
}

function metric(label, value, detail, kind) {
  return `<section class="metric ${kind || ""}">
    <span>${label}</span>
    <strong>${value}</strong>
    <small>${detail}</small>
  </section>`;
}

function modeLabel(value) {
  const raw = String(value || "");
  if (!raw) return "-";
  const key = `catalog.mode.${raw}`;
  const translated = t(key);
  return translated === key ? statusDisplayValue(raw) : translated;
}

function workflowStatusMetricDetail(workflow = {}) {
  return escapeHTML(workflow.status ? statusDisplayValue(workflow.status) : t("status.metric.workflowIdle"));
}

function workflowHealthDetail(workflow = {}) {
  if (!workflow.status) return escapeHTML(t("status.workflowHelp"));
  return escapeHTML(`${t("status.health.nextStage")} ${statusDisplayValue(workflow.next_stage || "-")}`);
}

function buildHealthItems(runtime, workflow, pending, statusCount) {
  const workspaceReady = !!runtime.workspace?.confirmed;
  const workflowActive = !!workflow.status;
  const hasLogs = !!statusCount;
  return [
    {
      title: t("status.health.workspace"),
      body: workspaceReady ? t("status.workspaceReady") : t("status.workspaceNeedsConfirmation"),
      detail: escapeHTML(runtime.workspace?.display || t("common.none")),
      tone: workspaceReady ? "good" : "warn",
      action: workspaceReady ? null : { view: "workspace", label: t("status.fixWorkspace"), tone: "primary" }
    },
    {
      title: t("status.health.workflow"),
      body: escapeHTML(localizedText(workflow.name || t("status.workflowIdle"))),
      detail: workflowHealthDetail(workflow),
      tone: workflowActive ? "good" : "neutral",
      action: workflowActive ? { view: "playground", label: t("status.inspectRun"), tone: "" } : { view: "playground", label: t("status.startRun"), tone: "" }
    },
    {
      title: t("status.health.approvals"),
      body: pending ? t("status.pendingApprovals", { count: pending }) : t("status.noPendingApprovals"),
      detail: pending ? t("status.pendingApprovalsHelp") : t("status.noPendingApprovalsHelp"),
      tone: pending ? "warn" : "good",
      action: pending ? { view: "approvals", label: t("status.fixApprovals"), tone: "primary" } : null
    },
    {
      title: t("status.health.logs"),
      body: hasLogs ? t("status.runtimeLines", { count: statusCount }) : t("status.runtimeLinesEmpty"),
      detail: t("status.runtimeLinesHelp"),
      tone: hasLogs ? "good" : "neutral",
      action: hasLogs ? null : { view: "playground", label: t("status.startRun"), tone: "" }
    }
  ];
}

function renderHealthItem(item) {
  const action = item.action?.view
    ? `<button type="button" class="status-health-action ${escapeHTML(item.action.tone || "")}" data-status-link="${escapeHTML(item.action.view)}">${escapeHTML(item.action.label || t("status.inspectIssue"))}</button>`
    : "";
  return `<div class="status-health-row">
    <div>
      <strong>${item.title}</strong>
      <span>${item.body}</span>
      <small>${item.detail}</small>
      ${action}
    </div>
    <span class="badge ${item.tone}">${t(`status.tone.${item.tone}`)}</span>
  </div>`;
}
