import { checkWorkspaceRequirement, escapeHTML, fetchTeamState, fetchWorkflowRun, fetchWorkflowRunActions, fetchWorkflowRunArtifacts, fetchWorkflowRunDiffs, fetchWorkflowRunEvidence, fetchWorkflowRunReplay, fetchWorkflowRuns, fetchWorkflowRunStages, request, streamWorkflowRunEvents } from "../api.js";
import { localizedText, t } from "../i18n.js";
import { extractSkillScriptContextFromApproval } from "../run_context.js";

const approvalActionEventPreviewMS = 2400;

export async function renderApprovals(root, runtime, refreshRuntime, options = {}) {
  const approvals = Array.isArray(options.preloadedApprovals)
    ? options.preloadedApprovals
    : await collectApprovalItems(runtime);
  const previousSelectedID = root.dataset.selectedApprovalId || "";
  const initial = approvals.find(item => item.id === previousSelectedID) || approvals[0] || null;
  root.dataset.approvalsSignature = approvalItemsSignature(approvals);
  root.dataset.approvalsRuntimeSignature = approvalRuntimeSignature(runtime);
  root.dataset.approvalsRuntimeCheckedAt = String(Date.now());
  root.innerHTML = `
    <div class="grid approvals-grid">
      <section class="panel span-5 approvals-queue" data-tour-id="approvals-queue">
        <div class="panel-head">
          <div>
            <p class="eyebrow">${t("view.approvals.eyebrow")}</p>
            <h2>${t("approvals.pending")}</h2>
            <p class="muted">${t("approvals.pendingHelp")}</p>
          </div>
          <span class="badge ${approvals.length ? "warn" : "good"}">${approvals.length ? t("approvals.count", { count: approvals.length }) : t("approvals.clear")}</span>
        </div>
        ${approvals.length ? `<div class="approval-notice" role="status">
          <strong>${escapeHTML(t("approvals.noticeTitle", { count: approvals.length }))}</strong>
          <span>${escapeHTML(t("approvals.noticeHelp"))}</span>
        </div>` : ""}
        <div id="approvalList" class="list approval-list"></div>
      </section>

      <section class="panel span-7 approval-detail" data-tour-id="approvals-log">
        <div class="panel-head">
          <div>
            <p class="eyebrow">${t("approvals.detailEyebrow")}</p>
            <h2>${t("approvals.detailTitle")}</h2>
            <p class="muted">${t("approvals.detailHelp")}</p>
          </div>
          <span id="approvalStateBadge" class="badge">${initial ? t("approvals.needsDecision") : t("approvals.nothingSelected")}</span>
        </div>

        <div id="approvalEmpty" class="${initial ? "hidden" : ""}">${renderApprovalsEmptyDetail()}</div>
        <div id="approvalDetailBody" class="approval-detail-body ${initial ? "" : "hidden"}">
          <div class="approval-summary-card">
            <div class="approval-summary-head">
              <div>
                <strong id="approvalToolName">-</strong>
                <p id="approvalSummary" class="muted"></p>
              </div>
              <span id="approvalRisk" class="badge warn">${t("approvals.needsDecision")}</span>
            </div>
            <div class="approval-user-summary">
              <span><small>${t("approvals.subject")}</small><b id="approvalToolField">-</b></span>
              <span><small>${t("approvals.arguments")}</small><b id="approvalArgs">-</b></span>
            </div>
            <details class="approval-tech-details">
              <summary>
                <span>${t("approvals.technicalDetails")}</span>
                <small>${t("approvals.technicalDetailsHelp")}</small>
              </summary>
              <table class="kv compact">
                <tr><th>${t("approvals.type")}</th><td id="approvalType">-</td></tr>
                <tr><th>${t("approvals.callId")}</th><td id="approvalCallId">-</td></tr>
              </table>
            </details>
          </div>

          <div id="approvalContext" class="approval-context"></div>

          <div class="approval-decision-note item">
            <strong>${t("approvals.decisionTitle")}</strong>
            <p class="muted" id="approvalDecisionHelp">${t("approvals.decisionHelp")}</p>
            <div class="approval-action-grid">
              <button class="primary" data-action="approve">${t("approvals.approveLabel")}</button>
              <button class="primary hidden" data-action="approve-all">${t("approvals.approveAllTools")}</button>
              <button data-action="approve-remember">${t("approvals.approveRememberLabel")}</button>
              <button class="danger" data-action="deny">${t("approvals.denyLabel")}</button>
              <button type="button" class="subtle" data-approval-inspect>${t("approvals.inspectRequest")}</button>
              <button class="danger hidden" data-action="cancel">${t("chat.cancelRun")}</button>
            </div>
          </div>

          <details class="approval-log-card">
            <summary class="panel-head compact">
              <div>
                <h3>${t("approvals.resumeLog")}</h3>
                <p class="muted">${t("approvals.resumeHelp")}</p>
              </div>
            </summary>
            <pre id="approvalLog" class="log approval-log"></pre>
          </details>
        </div>
      </section>
    </div>`;

  const list = root.querySelector("#approvalList");
  const log = root.querySelector("#approvalLog");
  const empty = root.querySelector("#approvalEmpty");
  const detailBody = root.querySelector("#approvalDetailBody");
  const badge = root.querySelector("#approvalStateBadge");
  const toolName = root.querySelector("#approvalToolName");
  const summary = root.querySelector("#approvalSummary");
  const risk = root.querySelector("#approvalRisk");
  const type = root.querySelector("#approvalType");
  const callId = root.querySelector("#approvalCallId");
  const toolField = root.querySelector("#approvalToolField");
  const args = root.querySelector("#approvalArgs");
  const decisionHelp = root.querySelector("#approvalDecisionHelp");
  const context = root.querySelector("#approvalContext");
  const inspectButton = root.querySelector("[data-approval-inspect]");

  if (!approvals.length) {
    root.dataset.selectedApprovalId = "";
    list.innerHTML = renderApprovalsEmptyList();
    root.querySelectorAll("[data-approvals-target]").forEach(button => {
      button.addEventListener("click", () => {
        const target = button.dataset.approvalsTarget || "";
        if (target) location.hash = target;
      });
    });
    registerApprovalsAutoRefresh(root, refreshRuntime, () => false);
    return;
  }

  let selectedID = initial?.id || "";

  const updateSelection = approval => {
    selectedID = approval.id;
    root.dataset.selectedApprovalId = approval.id;
    empty.classList.add("hidden");
    detailBody.classList.remove("hidden");
    toolName.textContent = approvalDisplayValue(approval.title || "-");
    summary.textContent = approvalDisplayText(approval.summary || t("approvals.summaryFallback"));
    const riskBadge = approvalRiskBadge(approval);
    risk.textContent = riskBadge.label;
    risk.className = `badge ${riskBadge.tone}`.trim();
    badge.textContent = approval.available === false ? t("approvals.unavailable") : t("approvals.needsDecision");
    badge.className = `badge ${approval.available === false ? "bad" : riskBadge.tone || "warn"}`;
    type.textContent = approvalDisplayValue(approval.typeLabel);
    callId.textContent = approval.callID || "-";
    toolField.textContent = approvalDisplayValue(approval.subject || "-");
    args.textContent = approvalDisplayText(approval.summary || "-");
    decisionHelp.textContent = approvalDecisionHelp(approval);
    context.innerHTML = approvalContextHTML(approval);
    context.classList.toggle("hidden", !context.innerHTML.trim());
    if (inspectButton) {
      inspectButton.textContent = approvalInspectLabel(approval);
      inspectButton.title = t("approvals.inspectRequestTitle");
      inspectButton.dataset.approvalInspectTarget = approvalInspectTarget(approval);
    }
    list.querySelectorAll(".approval-card").forEach(card => {
      card.classList.toggle("active", card.dataset.approvalId === approval.id);
    });
    syncActionButtons(approval, detailBody);
    if (log && !log.textContent.trim()) {
      log.textContent = t("approvals.logIdle");
    }
  };

  let submitting = false;
  const setSubmitting = (pending, actionLabel = "", actionName = "") => {
    submitting = pending;
    detailBody.classList.toggle("is-action-pending", pending);
    detailBody.setAttribute("aria-busy", pending ? "true" : "false");
    detailBody.querySelectorAll("[data-action]").forEach(button => {
      const unavailable = button.dataset.unavailable === "true";
      const canCancel = pending &&
        actionName !== "cancel" &&
        button.dataset.action === "cancel" &&
        !button.classList.contains("hidden") &&
        !unavailable;
      const disabled = unavailable || (pending && !canCancel);
      button.disabled = disabled;
      button.setAttribute("aria-disabled", disabled ? "true" : "false");
    });
    if (pending) {
      badge.textContent = `${t("approvals.submitting")} ${actionLabel}`.trim();
      risk.textContent = t("approvals.submitting");
    } else {
      const approval = approvals.find(item => item.id === selectedID);
      badge.textContent = approval?.available === false ? t("approvals.unavailable") : t("approvals.needsDecision");
      risk.textContent = badge.textContent;
    }
  };

  const submitAction = async actionName => {
    const approval = approvals.find(item => item.id === selectedID);
    if (!approval) return;
    if (submitting && actionName !== "cancel") return;
    const label = approvalActionLabel(approval, actionName);
    setSubmitting(true, t("chat.workspacePreflightChecking"), actionName);
    const workspaceReady = await ensureWorkspaceRequirementBeforeApprovalAction(approval, actionName);
    if (!workspaceReady.ready) {
      log.textContent = workspaceReady.message || t("chat.workspacePreflightBlockedStatus");
      setSubmitting(false);
      badge.textContent = t("chat.workspacePreflightBlockedStatus");
      risk.textContent = t("chat.workspacePreflightTitle");
      badge.className = "badge warn";
      risk.className = "badge warn";
      return;
    }
    log.textContent = `${t("approvals.submitting")} ${label} ${t("approvals.for")} ${approval.callID}...\n`;
    setSubmitting(true, label, actionName);
    try {
      if (approval.kind === "workflow") {
        const result = await submitWorkflowApprovalAction(approval, actionName);
        log.textContent += formatActionResult(result);
      } else {
        const text = await streamApproval(approval.callID, actionName);
        log.textContent += text || t("common.done");
      }
      log.textContent += `\n${t("approvals.refreshingStatus")}\n`;
      badge.textContent = t("approvals.refreshingStatus");
      risk.textContent = t("approvals.refreshingStatus");
      const nextRuntime = await refreshRuntimeAfterApproval(refreshRuntime);
      root.dataset.selectedApprovalId = "";
      await renderApprovals(root, nextRuntime, refreshRuntime);
    } catch (error) {
      log.textContent += localizedApprovalErrorMessage(error, t("approvals.actionFailed"));
      badge.textContent = t("approvals.actionFailed");
      risk.textContent = t("approvals.actionFailed");
      badge.className = "badge bad";
      setSubmitting(false);
    }
  };

  list.innerHTML = renderApprovalGroups(approvals);
  list.querySelectorAll(".approval-card").forEach(item => {
    const approval = approvals.find(candidate => candidate.id === item.dataset.approvalId);
    item.addEventListener("click", () => updateSelection(approval));
  });

  detailBody.querySelectorAll("[data-action]").forEach(button => {
    button.addEventListener("click", () => submitAction(button.dataset.action));
  });
  inspectButton?.addEventListener("click", () => {
    const approval = approvals.find(item => item.id === selectedID);
    location.hash = approvalInspectTarget(approval);
  });

  updateSelection(initial);

  registerApprovalsAutoRefresh(root, refreshRuntime, () => submitting);
}

function renderApprovalsEmptyList() {
  return `<div class="approval-empty-card">
    <strong>${escapeHTML(t("approvals.empty"))}</strong>
    <p>${escapeHTML(t("approvals.emptyQueueHelp"))}</p>
    <button type="button" data-approvals-target="playground">${escapeHTML(t("approvals.openRun"))}</button>
  </div>`;
}

function renderApprovalsEmptyDetail() {
  return `<div class="approval-empty-card detail">
    <strong>${escapeHTML(t("approvals.emptyDetailTitle"))}</strong>
    <p>${escapeHTML(t("approvals.emptyDetail"))}</p>
    <div class="approval-empty-actions">
      <button type="button" data-approvals-target="status">${escapeHTML(t("approvals.openStatus"))}</button>
      <button type="button" data-approvals-target="workflows">${escapeHTML(t("approvals.openWorkflows"))}</button>
    </div>
  </div>`;
}

async function refreshRuntimeAfterApproval(refreshRuntime) {
  const first = await refreshRuntime();
  await delay(350);
  return refreshRuntime().catch(() => first);
}

function delay(ms) {
  return new Promise(resolve => window.setTimeout(resolve, ms));
}

function registerApprovalsAutoRefresh(root, refreshRuntime, isSubmitting) {
  let refreshing = false;
  let refreshFrame = 0;
  let queuedRuntime = null;
  const cleanup = () => {
    if (refreshFrame) cancelAnimationFrame(refreshFrame);
    window.removeEventListener("goflow:runtime", onRuntime);
  };
  const refreshFromRuntime = async runtimeSnapshot => {
    if (!root.isConnected || refreshing || isSubmitting()) return;
    const runtimeSignature = approvalRuntimeSignature(runtimeSnapshot);
    const lastChecked = Number(root.dataset.approvalsRuntimeCheckedAt || 0);
    const stale = Date.now() - lastChecked > 15000;
    if (runtimeSignature === root.dataset.approvalsRuntimeSignature && !stale) return;
    root.dataset.approvalsRuntimeSignature = runtimeSignature;
    root.dataset.approvalsRuntimeCheckedAt = String(Date.now());
    refreshing = true;
    const viewState = captureApprovalViewState(root);
    try {
      const approvals = await collectApprovalItems(runtimeSnapshot);
      const signature = approvalItemsSignature(approvals);
      if (signature === root.dataset.approvalsSignature) return;
      cleanup();
      await renderApprovals(root, runtimeSnapshot, refreshRuntime, { preloadedApprovals: approvals });
      restoreApprovalViewState(root, viewState);
    } catch {
      // Keep the current approval view stable if a background refresh fails.
    } finally {
      refreshing = false;
    }
  };
  const onRuntime = event => {
    queuedRuntime = event.detail;
    if (refreshFrame) return;
    refreshFrame = requestAnimationFrame(() => {
      refreshFrame = 0;
      const runtimeSnapshot = queuedRuntime;
      queuedRuntime = null;
      if (runtimeSnapshot) refreshFromRuntime(runtimeSnapshot);
    });
  };
  window.addEventListener("goflow:runtime", onRuntime);
  window.addEventListener("goflow:view-dispose", cleanup, { once: true });
}

function approvalItemsSignature(approvals) {
  return (approvals || []).map(item => [
    item.id || "",
    item.kind || "",
    item.title || "",
    item.summary || "",
    item.callID || "",
    item.subject || "",
    item.toolRisk?.doc?.name || "",
    item.toolRisk?.risk?.risk_level || "",
    item.typeLabel || "",
    item.available === false ? "0" : "1"
  ].join("\u0001")).join("\u0002");
}

function approvalRuntimeSignature(runtime) {
  const pendingTools = Array.isArray(runtime?.session?.pending_approvals)
    ? runtime.session.pending_approvals
    : [];
  const workflowRuns = normalizeWorkflowRunList(runtime?.session?.workflow_runs)
    .filter(run => isWorkflowApprovalStatus(run?.status));
  return [
    pendingTools.length,
    pendingTools.map(approval => [
      approval?.call_id || "",
      approval?.tool_name || "",
      approval?.workflow_name || "",
      approval?.stage || "",
      approval?.arguments_summary || ""
    ].join(":")).join("|"),
    workflowRuns.length,
    workflowRuns.map(run => [
      run?.id || run?.run_id || "",
      run?.name || run?.workflow || "",
      run?.status || "",
      run?.next_stage || "",
      run?.pending_call_id || "",
      run?.pending_tool_name || ""
    ].join(":")).join("|")
  ].join("\u0002");
}

function captureApprovalViewState(root) {
  return {
    selectedID: root.dataset.selectedApprovalId || "",
    windowX: window.scrollX || 0,
    windowY: window.scrollY || 0,
    listTop: root.querySelector("#approvalList")?.scrollTop || 0,
    detailTop: root.querySelector("#approvalDetailBody")?.scrollTop || 0,
    logTop: root.querySelector("#approvalLog")?.scrollTop || 0
  };
}

function restoreApprovalViewState(root, state, options = {}) {
  if (!state) return;
  root.dataset.selectedApprovalId = state.selectedID || root.dataset.selectedApprovalId || "";
  restoreApprovalScrollNodes(root, state);
  requestAnimationFrame(() => restoreApprovalScrollNodes(root, state, options));
}

function restoreApprovalScrollNodes(root, state, options = {}) {
  const list = root.querySelector("#approvalList");
  const detail = root.querySelector("#approvalDetailBody");
  const log = root.querySelector("#approvalLog");
  if (list) list.scrollTop = Math.min(state.listTop || 0, Math.max(0, list.scrollHeight - list.clientHeight));
  if (detail) detail.scrollTop = Math.min(state.detailTop || 0, Math.max(0, detail.scrollHeight - detail.clientHeight));
  if (log) log.scrollTop = Math.min(state.logTop || 0, Math.max(0, log.scrollHeight - log.clientHeight));
  if (options.restoreWindow && window.goflowCanRestoreWindowScroll?.() !== false && (
    Math.abs((window.scrollY || 0) - (state.windowY || 0)) > 1 ||
    Math.abs((window.scrollX || 0) - (state.windowX || 0)) > 1
  )) {
    window.scrollTo({ left: state.windowX || 0, top: state.windowY || 0, behavior: "auto" });
  }
}

async function collectApprovalItems(runtime) {
  const rawToolApprovals = runtime?.session?.pending_approvals || [];
  const [workflowItems, toolResources] = await Promise.all([
    collectWorkflowApprovalItems(runtime),
    loadApprovalToolResources()
  ]);
  const workflowToolItems = workflowToolApprovalIndex(workflowItems);
  const workflowToolScopes = workflowToolApprovalScopeIndex(workflowItems);
  const representedWorkflowItems = new Set();
  const hiddenWorkflowCalls = pendingToolCallIDs(rawToolApprovals);
  const toolItems = [];
  const aggregatedWorkflowItems = new Map();
  rawToolApprovals.forEach((approval, index) => {
    const workflowItem = matchingWorkflowToolItem(approval, workflowToolItems, workflowToolScopes);
    if (workflowItem) {
      representedWorkflowItems.add(workflowItem.id);
      if (aggregatedWorkflowItems.has(workflowItem.id)) {
        appendToolApprovalToWorkflowItem(aggregatedWorkflowItems.get(workflowItem.id), approval);
      } else {
        const merged = mergeToolApprovalIntoWorkflowItem(approval, workflowItem);
        aggregatedWorkflowItems.set(workflowItem.id, merged);
        toolItems.push(merged);
      }
      return;
    }
    if (String(approval.workflow_name || "").trim()) return;
    toolItems.push(toolApprovalItem(approval, index));
  });
  return hydrateApprovalToolRisk([
    ...toolItems,
    ...workflowItems.filter(item => {
      if (representedWorkflowItems.has(item.id)) return false;
      if (item.workflowApprovalType !== "tool") return true;
      const callID = String(item.run?.pending_call_id || "").trim();
      return !callID || !hiddenWorkflowCalls.has(callID);
    })
  ], toolResources, runtime);
}

async function loadApprovalToolResources() {
  try {
    const payload = await request("/api/resources/tools");
    return Array.isArray(payload) ? payload : payload?.items || payload?.tools || [];
  } catch {
    return [];
  }
}

function hydrateApprovalToolRisk(items, resources = [], runtime = {}) {
  const indexed = new Map((Array.isArray(resources) ? resources : [])
    .filter(item => item?.name)
    .map(item => [String(item.name).trim().toLowerCase(), item]));
  const runtimeIndexed = new Map((Array.isArray(runtime?.mcp_servers) ? runtime.mcp_servers : [])
    .filter(item => item?.name)
    .map(item => [String(item.name).trim().toLowerCase(), item]));
  return (items || []).map(item => {
    const names = approvalToolNames(item);
    const docs = [];
    const seen = new Set();
    names.forEach(name => {
      const doc = indexed.get(name.toLowerCase());
      if (!doc || seen.has(String(doc.name).toLowerCase())) return;
      seen.add(String(doc.name).toLowerCase());
      docs.push({
        ...doc,
        runtime: runtimeIndexed.get(name.toLowerCase()) || null
      });
    });
    const directRisk = approvalDirectToolRisk(item);
    if (!docs.length && !names.length && !directRisk) return item;
    return {
      ...item,
      toolNames: names,
      toolRiskDocs: docs,
      toolRisk: strongestToolRisk(docs, directRisk)
    };
  });
}

function approvalToolNames(approval) {
  const names = [
    approval?.raw?.tool_name,
    approval?.run?.pending_tool_name,
    approval?.run?.pending_tool?.name,
    ...(Array.isArray(approval?.relatedApprovals) ? approval.relatedApprovals.map(item => item?.tool_name) : [])
  ];
  if (approval?.kind === "tool") names.push(approval.subject, approval.title);
  return [...new Set(names.map(value => String(value || "").trim()).filter(Boolean))];
}

function approvalDirectToolRisk(approval = {}) {
  const candidates = [
    approval?.raw?.risk,
    approval?.raw?.tool_risk,
    approval?.raw?.pending_tool_risk,
    approval?.run?.pending_tool_risk,
    approval?.run?.pending_tool?.risk,
    approval?.run?.tool_risk,
    approval?.run?.risk
  ];
  return candidates.find(isApprovalToolRiskProfile) || null;
}

function isApprovalToolRiskProfile(risk) {
  if (!risk || typeof risk !== "object" || Array.isArray(risk)) return false;
  return [
    "risk_level",
    "requires_approval",
    "destructive",
    "sandboxed",
    "external_sandbox_recommended",
    "workspace_scoped_inputs",
    "workspace_scope_enforced",
    "kind",
    "capabilities",
    "warnings",
    "security_boundary",
    "isolation_level",
    "isolation",
    "isolation_options",
    "isolation_profile",
    "container_image",
    "container_image_reference_type",
    "container_image_digest_pinned",
    "container_image_production_ready",
    "container_pull_policy",
    "sandbox_features",
    "missing_sandbox_features",
    "windows_isolation"
  ].some(key => Object.prototype.hasOwnProperty.call(risk, key));
}

function strongestToolRisk(docs = [], directRisk = null) {
  const directEntries = directRisk ? [{ doc: docs[0] || {}, risk: directRisk, rank: toolRiskRank(directRisk) }] : [];
  const ranked = [
    ...directEntries,
    ...docs
    .map(doc => ({ doc, risk: doc?.risk || {}, rank: toolRiskRank(doc?.risk || {}) }))
  ]
    .sort((a, b) => b.rank - a.rank);
  return ranked[0] || null;
}

function toolRiskRank(risk = {}) {
  const level = String(risk.risk_level || "").toLowerCase();
  if (risk.destructive || level === "critical") return 5;
  if (level === "high") return 4;
  if (risk.requires_approval || risk.external_sandbox_recommended || level === "medium") return 3;
  if (risk.sandboxed || level === "low") return 1;
  return 0;
}

async function collectWorkflowApprovalItems(runtime) {
  const runs = await loadWorkflowRuns(runtime);
  const candidates = runs
    .filter(run => isWorkflowApprovalStatus(run.status))
    .sort((a, b) => workflowRunSortValue(b) - workflowRunSortValue(a))
    .slice(0, 12);
  const items = await Promise.all(candidates.map(async run => {
    if (!run?.id) return null;
    const actions = normalizeCollection(await fetchWorkflowRunActions(run).catch(() => Array.isArray(run.actions) ? run.actions : []));
    const approve = actions.find(action => action.name === "approve_stage" || action.name === "approve_tool");
    const approveAll = actions.find(action => action.name === "approve_all_tools");
    if (!approve && !approveAll) return null;
    const deny = actions.find(action => action.name === "deny_tool");
    const cancel = actions.find(action => action.name === "cancel");
    const retry = actions.find(action => action.name === "retry");
    const context = await loadWorkflowApprovalContext(run);
    const hydratedRun = context.run || run;
    const primaryApprove = approve || approveAll;
    const availableApprove = [approve, approveAll].find(action => action?.available !== false && hasWorkflowActionEndpoint(action)) || primaryApprove;
    const isToolApproval = primaryApprove.name === "approve_tool" || primaryApprove.name === "approve_all_tools" || String(hydratedRun.status || "").toLowerCase() === "awaiting_tool_approval";
    return {
      id: `workflow:${run.id}`,
      kind: "workflow",
      callID: run.id || "",
      workflowApprovalType: isToolApproval ? "tool" : "stage",
      title: approvalWorkflowTitle(hydratedRun, isToolApproval),
      subject: approvalWorkflowSubject(hydratedRun, isToolApproval),
      summary: hydratedRun.approval_prompt || hydratedRun.pending_arguments || hydratedRun.summary || hydratedRun.request || t("approvals.workflowSummaryFallback"),
      typeLabel: isToolApproval ? t("approvals.typeWorkflowTool") : t("approvals.typeWorkflow"),
      available: availableApprove?.available !== false && hasWorkflowActionEndpoint(availableApprove),
      reason: localizedApprovalReason(availableApprove?.reason || ""),
      run: hydratedRun,
      context,
      actions: { approve, approveAll, deny, cancel, retry }
    };
  }));
  return items.filter(Boolean);
}

function workflowToolApprovalIndex(items) {
  const indexed = new Map();
  for (const item of items || []) {
    if (item.workflowApprovalType !== "tool") continue;
    const callID = String(item.run?.pending_call_id || "").trim();
    if (callID) indexed.set(callID, item);
  }
  return indexed;
}

function workflowToolApprovalScopeIndex(items) {
  const indexed = new Map();
  for (const item of items || []) {
    if (item.workflowApprovalType !== "tool") continue;
    const key = workflowApprovalScopeKey(item.run?.name, item.run?.next_stage);
    if (key && !indexed.has(key)) indexed.set(key, item);
  }
  return indexed;
}

function matchingWorkflowToolItem(approval, byCall, byScope) {
  const callID = String(approval?.call_id || "").trim();
  if (callID && byCall.has(callID)) return byCall.get(callID);
  const key = workflowApprovalScopeKey(approval?.workflow_name, approval?.stage);
  return key ? byScope.get(key) || null : null;
}

function workflowApprovalScopeKey(workflowName, stage) {
  const workflow = String(workflowName || "").trim().toLowerCase();
  const nextStage = String(stage || "").trim().toLowerCase();
  if (!workflow || !nextStage) return "";
  return `${workflow}::${nextStage}`;
}

function toolApprovalItem(approval, index) {
  const workflowName = String(approval.workflow_name || "").trim();
  return {
    id: `tool:${approval.call_id || approval.tool_name || index}`,
    kind: "tool",
    callID: approval.call_id || "",
    title: approval.tool_name || approval.call_id || "-",
    subject: approval.tool_name || "-",
    summary: approval.arguments_summary || t("approvals.summaryFallback"),
    typeLabel: workflowName ? t("approvals.typeWorkflowTool") : t("approvals.typeTool"),
    available: !workflowName,
    reason: workflowName ? t("approvals.workflowToolContextLost") : "",
    raw: approval
  };
}

function mergeToolApprovalIntoWorkflowItem(approval, workflowItem) {
  const relatedApprovals = [approval];
  return {
    ...workflowItem,
    id: `workflow-tool:${workflowItem.run?.id || workflowItem.id}`,
    title: workflowItem.title || approval.tool_name || approval.call_id || "-",
    subject: workflowItem.subject || approval.tool_name || "-",
    summary: workflowToolApprovalSummary(relatedApprovals, workflowItem.summary),
    raw: approval,
    relatedApprovals
  };
}

function appendToolApprovalToWorkflowItem(item, approval) {
  item.relatedApprovals = Array.isArray(item.relatedApprovals) ? item.relatedApprovals : [];
  item.relatedApprovals.push(approval);
  item.summary = workflowToolApprovalSummary(item.relatedApprovals, item.summary);
  return item;
}

function workflowToolApprovalSummary(approvals, fallback) {
  const items = Array.isArray(approvals) ? approvals : [];
  if (items.length > 1) {
    return t("approvals.workflowToolBatchSummary", { count: items.length });
  }
  return items[0]?.arguments_summary || fallback || t("approvals.summaryFallback");
}

function pendingToolCallIDs(approvals) {
  return new Set((approvals || [])
    .map(approval => String(approval?.call_id || "").trim())
    .filter(Boolean));
}

function approvalDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (approvalLooksIdentifier(text)) return text;
  return localizedText(text);
}

function approvalDisplayText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (approvalLooksTechnical(text)) return text;
  return localizedText(text);
}

function approvalLooksTechnical(text) {
  const value = String(text || "").trim();
  if (!value) return false;
  if (value.startsWith("{") || value.startsWith("[") || value.startsWith("@") || value.startsWith("$")) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(value)) return true;
  if (!/\s/.test(value) && approvalLooksIdentifier(value)) return true;
  if (/^[-\w.]+$/.test(value) && /[._-]/.test(value)) return true;
  return false;
}

function approvalLooksIdentifier(text) {
  const value = String(text || "").trim();
  if (!value) return false;
  if (value.startsWith("@") || value.startsWith("$")) return true;
  if (value.includes("\\") || value.includes("/api/") || value.includes("://")) return true;
  if (value.includes("/") && !/\s/.test(value)) return true;
  if (value.includes(".") && !/\s/.test(value)) return true;
  return false;
}

function isWorkflowApprovalStatus(status) {
  const value = String(status || "").toLowerCase();
  return value === "awaiting_approval" || value === "awaiting_tool_approval";
}

function approvalWorkflowTitle(run, isToolApproval) {
  const subject = isToolApproval ? run.pending_tool_name : run.next_stage;
  return [run.name || t("chat.workflow"), subject || ""].filter(Boolean).join(" / ");
}

function approvalWorkflowSubject(run, isToolApproval) {
  const subject = isToolApproval
    ? run.pending_tool_name || run.pending_call_id || ""
    : run.next_stage || "";
  return `${run.name || t("chat.workflow")} ${subject ? `- ${subject}` : ""}`;
}

async function loadWorkflowRuns(runtime) {
  try {
    return normalizeWorkflowRunList(await fetchWorkflowRuns());
  } catch {
    return normalizeWorkflowRunList(runtime?.session?.workflow_runs);
  }
}

function normalizeWorkflowRunList(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  if (Array.isArray(value?.value)) return value.value;
  return [];
}

function renderApprovalGroups(approvals) {
  return approvalGroups(approvals).map(group => {
    const risk = group.risk || { label: t("approvals.needsDecision"), tone: "warn" };
    return `<section class="approval-group" data-approval-group="${escapeHTML(group.key)}">
      <div class="approval-group-head">
        <div>
          <strong>${escapeHTML(group.title)}</strong>
          <span>${escapeHTML(group.subtitle)}</span>
        </div>
        <span class="badge ${escapeHTML(risk.tone || "warn")}">${escapeHTML(risk.label)}</span>
      </div>
      <div class="approval-group-list">
        ${group.items.map(renderApprovalCard).join("")}
      </div>
    </section>`;
  }).join("");
}

function approvalGroups(approvals) {
  const groups = new Map();
  (approvals || []).forEach(approval => {
    const meta = approvalGroupMeta(approval);
    if (!groups.has(meta.key)) {
      groups.set(meta.key, { ...meta, items: [], risk: null, riskRank: -1 });
    }
    const group = groups.get(meta.key);
    group.items.push(approval);
    const risk = approvalRiskBadge(approval);
    const rank = approvalRiskDisplayRank(risk);
    if (rank > group.riskRank) {
      group.risk = risk;
      group.riskRank = rank;
    }
  });
  return [...groups.values()].map(group => ({
    ...group,
    subtitle: approvalGroupSubtitle(group)
  }));
}

function approvalGroupMeta(approval) {
  if (approval?.kind === "workflow") {
    const run = approval.run || {};
    const id = String(run.id || approval.callID || approval.id || "").trim();
    const name = approvalDisplayValue(run.name || run.workflow || t("approvals.groupWorkflow"));
    const position = approvalDisplayValue(run.next_stage || run.pending_tool_name || run.pending_call_id || "");
    return {
      key: `workflow:${id || name}`,
      title: name,
      kindLabel: t("approvals.groupWorkflow"),
      position
    };
  }
  const raw = approval?.raw || {};
  const agentRunID = String(raw.agent_run_id || approval.agentRunID || "").trim();
  if (agentRunID) {
    return {
      key: `agent-run:${agentRunID}`,
      title: t("approvals.groupAgentRun"),
      kindLabel: approvalDisplayValue(raw.agent_id || t("approvals.groupAgentRun")),
      position: agentRunID
    };
  }
  const agentID = String(raw.agent_id || approval.agentID || "").trim();
  if (agentID) {
    return {
      key: `agent:${agentID}`,
      title: approvalDisplayValue(agentID),
      kindLabel: t("approvals.groupAgent"),
      position: approvalDisplayValue(approval.subject || approval.title || "")
    };
  }
  return {
    key: "tool-queue",
    title: t("approvals.groupToolQueue"),
    kindLabel: t("approvals.typeTool"),
    position: approvalDisplayValue(approval.subject || approval.title || "")
  };
}

function approvalGroupSubtitle(group) {
  const parts = [
    t("approvals.groupCount", { count: group.items.length }),
    group.kindLabel,
    group.position
  ].filter(Boolean);
  return parts.join(" / ");
}

function approvalRiskDisplayRank(risk) {
  const tone = String(risk?.tone || "").toLowerCase();
  if (tone === "bad") return 4;
  if (tone === "warn") return 3;
  if (tone === "neutral") return 2;
  if (tone === "good") return 1;
  return 0;
}

function renderApprovalCard(approval) {
  const badge = approvalDisplayValue(approval.typeLabel || (approval.kind === "workflow" ? t("approvals.typeWorkflow") : t("approvals.typeTool")));
  const workflowMeta = approval.workflowApprovalType === "tool"
    ? approval.run?.pending_tool_name || approval.run?.pending_call_id || approval.run?.next_stage
    : approval.run?.next_stage;
  const meta = approval.kind === "workflow" && workflowMeta
    ? `${approval.workflowApprovalType === "tool" ? t("approvals.pendingTool") : t("approvals.currentStage")}: ${approvalDisplayValue(workflowMeta)}`
    : approval.callID || "";
  const summary = approvalDisplayText(approval.summary || t("approvals.summaryFallback"));
  const risk = approvalRiskBadge(approval);
  const answers = approvalCardAnswers(approval);
  return `<button type="button" class="item approval-card ${approval.kind === "workflow" ? "workflow-approval" : ""}" data-approval-id="${escapeHTML(approval.id)}">
    <div class="approval-card-head">
      <strong>${escapeHTML(approvalDisplayValue(approval.title || "-"))}</strong>
      <span class="badge ${escapeHTML(risk.tone || (approval.available === false ? "bad" : "warn"))}">${escapeHTML(badge)}</span>
    </div>
    <div class="approval-card-answers">
      ${answers.map(item => `<span>
        <small>${escapeHTML(item.label)}</small>
        <b>${escapeHTML(item.value)}</b>
      </span>`).join("")}
    </div>
    <p>${escapeHTML(truncateText(summary, 180))}</p>
    <small>${escapeHTML(meta || t("approvals.inspectRequestTitle"))}</small>
  </button>`;
}

function approvalCardAnswers(approval) {
  return [
    { label: t("approvals.cardWillRun"), value: approvalCardWillRun(approval) },
    { label: t("approvals.cardMayTouch"), value: approvalCardMayTouch(approval) },
    { label: t("approvals.cardWhyNeeded"), value: approvalCardWhyNeeded(approval) }
  ];
}

function approvalCardWillRun(approval) {
  if (approval?.kind === "workflow" && approval.workflowApprovalType === "tool") {
    return approvalDisplayValue(approval.run?.pending_tool_name || approval.run?.pending_call_id || approval.subject || approval.title || t("approvals.approveTool"));
  }
  if (approval?.kind === "workflow") {
    return approvalDisplayValue(approval.run?.next_stage || approval.subject || approval.title || t("approvals.approveStage"));
  }
  return approvalDisplayValue(approval?.subject || approval?.title || t("approvals.typeTool"));
}

function approvalCardMayTouch(approval) {
  const risk = approval?.toolRisk?.risk || {};
  const kind = String(risk.kind || "").toLowerCase();
  const capabilities = (Array.isArray(risk.capabilities) ? risk.capabilities : [])
    .map(item => String(item || "").toLowerCase());
  const names = [
    approval?.title,
    approval?.subject,
    approval?.raw?.tool_name,
    approval?.run?.pending_tool_name,
    ...(Array.isArray(approval?.toolNames) ? approval.toolNames : [])
  ].map(item => String(item || "").toLowerCase());
  const hasCapability = pattern => capabilities.some(item => item.includes(pattern));
  const hasName = pattern => names.some(item => item.includes(pattern));
  if (risk.destructive) return t("approvals.scopeDestructive");
  if (kind.includes("exec") || kind.includes("command") || hasCapability("exec") || hasCapability("command") || hasCapability("process") || hasName("exec") || hasName("shell") || hasName("command")) {
    return t("approvals.scopeCommands");
  }
  if (kind.includes("network") || hasCapability("network") || hasCapability("http") || hasName("web") || hasName("http") || hasName("fetch")) {
    return t("approvals.scopeNetwork");
  }
  if (kind.includes("write") || hasCapability("write") || hasCapability("filesystem") || hasName("write") || hasName("delete") || hasName("remove") || hasName("patch")) {
    return t("approvals.scopeWorkspaceWrite");
  }
  if (risk.workspace_scoped_inputs === true) {
    return risk.workspace_scope_enforced === true ? t("approvals.scopeWorkspaceBoundary") : t("approvals.scopePathInputs");
  }
  const diffCount = approval?.kind === "workflow" ? workflowRunDiffs(approval.run).length : 0;
  if (diffCount) return t("approvals.scopeChangedFiles", { count: diffCount });
  if (approval?.kind === "workflow") return t("approvals.scopeWorkflowState");
  return t("approvals.scopeCurrentRun");
}

function approvalCardWhyNeeded(approval) {
  if (approval?.available === false) return t("approvals.unavailable");
  const risk = approval?.toolRisk?.risk || {};
  if (risk.destructive) return t("approvals.reasonDestructive");
  if (risk.requires_approval) return t("approvals.reasonToolPolicy");
  const reason = localizedApprovalReason(approval?.reason || "");
  if (reason) return truncateText(reason, 90);
  if (approval?.kind === "workflow" && approval.workflowApprovalType === "tool") {
    return t("approvals.reasonWorkflowTool");
  }
  if (approval?.kind === "workflow") return t("approvals.reasonWorkflowGate");
  return t("approvals.reasonProtectedTool");
}

function approvalInspectTarget(approval) {
  if (approval?.kind === "workflow" || approval?.raw?.agent_run_id) return "playground";
  return "status";
}

function approvalInspectLabel(approval) {
  return approval?.kind === "workflow" || approval?.raw?.agent_run_id
    ? t("approvals.inspectRun")
    : t("approvals.inspectRequest");
}

function syncActionButtons(approval, detailBody) {
  const approve = detailBody.querySelector('[data-action="approve"]');
  const approveAll = detailBody.querySelector('[data-action="approve-all"]');
  const remember = detailBody.querySelector('[data-action="approve-remember"]');
  const deny = detailBody.querySelector('[data-action="deny"]');
  const cancel = detailBody.querySelector('[data-action="cancel"]');
  const unavailable = approval.available === false;
  approve.textContent = approval.kind === "workflow"
    ? workflowApproveButtonLabel(approval)
    : t("approvals.approveLabel");
  const hasApproveAll = approval.kind === "workflow" &&
    approval.workflowApprovalType === "tool" &&
    hasWorkflowActionEndpoint(approval.actions?.approveAll);
  const hasWorkflowApprove = approval.kind === "workflow" && hasWorkflowActionEndpoint(approval.actions?.approve);
  approve.classList.toggle("hidden", approval.kind === "workflow" && !hasWorkflowApprove && hasApproveAll);
  if (approval.kind === "workflow") {
    if (hasWorkflowApprove) {
      syncWorkflowActionAvailability(approve, approval.actions?.approve);
    } else {
      approve.dataset.unavailable = "true";
      approve.title = localizedApprovalReason(approval.reason || "") || t("approvals.unavailable");
      approve.disabled = true;
    }
  } else {
    approve.dataset.unavailable = unavailable ? "true" : "false";
    approve.title = unavailable ? localizedApprovalReason(approval.reason || "") || t("approvals.unavailable") : "";
    approve.disabled = unavailable;
  }
  approveAll.classList.toggle("hidden", !hasApproveAll);
  approveAll.textContent = t("approvals.approveAllTools");
  approveAll.dataset.action = "approve-all";
  if (hasApproveAll) {
    syncWorkflowActionAvailability(approveAll, approval.actions?.approveAll);
  } else {
    approveAll.dataset.unavailable = "true";
    approveAll.disabled = true;
    approveAll.title = "";
  }
  if (approval.kind === "workflow" && approval.actions?.retry?.path) {
    remember.classList.remove("hidden");
    remember.classList.add("primary");
    remember.textContent = t("chat.retryRun");
    remember.dataset.action = "retry";
    remember.dataset.unavailable = approval.actions.retry.available === false ? "true" : "false";
    remember.title = approval.actions.retry.available === false ? localizedApprovalReason(approval.actions.retry.reason || "") : "";
    remember.disabled = approval.actions.retry.available === false;
  } else {
    remember.classList.remove("primary");
    remember.textContent = t("approvals.approveRememberLabel");
    remember.dataset.action = "approve-remember";
    remember.classList.toggle("hidden", approval.kind === "workflow");
    remember.dataset.unavailable = unavailable ? "true" : "false";
    remember.title = unavailable ? localizedApprovalReason(approval.reason || "") || t("approvals.unavailable") : "";
    remember.disabled = unavailable;
  }
  const workflowDenyAction = workflowDenyButtonAction(approval);
  deny.classList.toggle("hidden", approval.kind === "workflow" && workflowDenyAction !== "deny");
  if (approval.kind === "workflow" && workflowDenyAction === "deny") {
    deny.textContent = t("approvals.denyTool");
    deny.dataset.action = "deny";
    syncWorkflowActionAvailability(deny, approval.actions?.deny);
  } else {
    deny.textContent = t("approvals.denyLabel");
    deny.dataset.action = "deny";
    deny.dataset.unavailable = unavailable ? "true" : "false";
    deny.title = unavailable ? localizedApprovalReason(approval.reason || "") || t("approvals.unavailable") : "";
    deny.disabled = unavailable;
  }
  const hasCancel = approval.kind === "workflow" && hasWorkflowActionEndpoint(approval.actions?.cancel);
  cancel.classList.toggle("hidden", !hasCancel);
  cancel.textContent = t("chat.cancelRun");
  cancel.dataset.action = "cancel";
  if (hasCancel) {
    syncWorkflowActionAvailability(cancel, approval.actions?.cancel);
  } else {
    cancel.dataset.unavailable = "true";
    cancel.disabled = true;
  }
  detailBody.querySelectorAll("[data-action]").forEach(button => {
    const disabled = button.disabled || button.dataset.unavailable === "true";
    button.disabled = disabled;
    button.setAttribute("aria-disabled", disabled ? "true" : "false");
    button.setAttribute("aria-busy", "false");
  });
}

function approvalRiskBadge(approval) {
  if (approval.available === false) {
    return { label: t("approvals.unavailable"), tone: "bad" };
  }
  const risk = approval.toolRisk?.risk || {};
  const level = String(risk.risk_level || "").trim();
  if (!approval.toolRiskDocs?.length && !level) {
    return { label: t("approvals.needsDecision"), tone: "warn" };
  }
  return {
    label: level ? approvalToolRiskLabel(level) : t("approvals.toolRiskProfile"),
    tone: approvalToolRiskTone(risk)
  };
}

function approvalToolRiskTone(risk = {}) {
  const value = String(risk.risk_level || "").toLowerCase();
  if (risk.destructive || value === "critical" || value === "high") return "bad";
  if (risk.requires_approval || risk.external_sandbox_recommended || risk.workspace_scoped_inputs && risk.workspace_scope_enforced === false || value === "medium") return "warn";
  if (risk.sandboxed || value === "low") return "good";
  return "neutral";
}

function approvalToolRiskLabel(level) {
  const value = String(level || "").toLowerCase();
  const key = `approvals.toolRisk.${value}`;
  const translated = t(key);
  return translated === key ? level : translated;
}

function approvalDecisionHelp(approval) {
  if (approval.available === false) {
    if (approval.kind === "workflow" && approval.workflowApprovalType === "tool") {
      return t("approvals.workflowToolContextLostHelp");
    }
    return localizedApprovalReason(approval.reason || "") || t("approvals.unavailableHelp");
  }
  if (approval.kind === "workflow" && approval.workflowApprovalType === "tool") {
    return t("approvals.workflowToolDecisionHelp");
  }
  if (approval.kind === "workflow") {
    return t("approvals.workflowDecisionHelp");
  }
  return t("approvals.decisionHelp");
}

function localizedApprovalErrorMessage(error, fallback = "") {
  const text = error?.message || (typeof error === "string" ? error : String(error || "")) || fallback;
  return localizedApprovalReason(text) || localizedText(text || fallback);
}

function workflowApproveButtonLabel(approval) {
  return approval.workflowApprovalType === "tool" ? t("approvals.approveTool") : t("approvals.approveStage");
}

function workflowDenyButtonAction(approval) {
  if (approval.kind !== "workflow") return "deny";
  if (hasWorkflowActionEndpoint(approval.actions?.deny) && approval.actions.deny.available !== false) return "deny";
  if (hasWorkflowActionEndpoint(approval.actions?.cancel) && approval.actions.cancel.available !== false) return "cancel";
  if (hasWorkflowActionEndpoint(approval.actions?.deny)) return "deny";
  return "";
}

function syncWorkflowActionAvailability(button, action) {
  const unavailable = !hasWorkflowActionEndpoint(action) || action.available === false;
  button.dataset.unavailable = unavailable ? "true" : "false";
  button.title = unavailable ? localizedApprovalReason(action?.reason || "") || t("approvals.unavailable") : "";
  button.disabled = unavailable;
  button.setAttribute("aria-disabled", unavailable ? "true" : "false");
  button.setAttribute("aria-busy", "false");
}

function hasWorkflowActionEndpoint(action) {
  return Boolean(action?.path);
}

function approvalActionEventsURL(accepted, action = {}) {
  const payload = accepted && typeof accepted === "object" ? accepted : {};
  return String(
    payload.events_url ||
    payload.eventsURL ||
    payload.events_path ||
    payload.eventsPath ||
    action.events_url ||
    action.eventsURL ||
    action.events_path ||
    action.eventsPath ||
    ""
  ).trim();
}

function approvalActionLabel(approval, actionName) {
  if (approval.kind === "workflow" && actionName === "approve") {
    return workflowApproveButtonLabel(approval);
  }
  if (approval.kind === "workflow" && actionName === "approve-all") {
    return t("approvals.approveAllTools");
  }
  if (approval.kind === "workflow" && actionName === "retry") {
    return t("chat.retryRun");
  }
  if (approval.kind === "workflow" && actionName === "cancel") {
    return t("chat.cancelRun");
  }
  if (approval.kind === "workflow" && actionName === "deny") {
    return t("approvals.denyTool");
  }
  if (actionName === "approve") return t("approvals.approve");
  if (actionName === "approve-remember") return t("approvals.approveRemember");
  if (actionName === "deny") return t("approvals.deny");
  return actionName || t("approvals.needsDecision");
}

async function ensureWorkspaceRequirementBeforeApprovalAction(approval, actionName) {
  const operation = workspaceOperationForApprovalAction(approval, actionName);
  if (!operation) return { ready: true };
  try {
    const requirement = await checkWorkspaceRequirement({
      operation,
      input: [approvalActionLabel(approval, actionName), approval.summary, approval.subject].filter(Boolean).join("\n"),
      workflow: approval.kind === "workflow" ? approval.run?.name || "" : approval.raw?.workflow_name || "",
      agent: approval.raw?.agent_id || ""
    });
    if (!requirement?.blocked) return { ready: true };
    return {
      ready: false,
      message: workspaceRequirementMessage(requirement)
    };
  } catch (error) {
    return {
      ready: false,
      message: localizedApprovalErrorMessage(error, t("chat.workspacePreflightFailed"))
    };
  }
}

function workspaceOperationForApprovalAction(approval, actionName) {
  if (actionName === "cancel" || actionName === "deny") return "";
  if (approval?.kind === "workflow") {
    if (actionName === "approve-all") return "tool";
    if (actionName === "approve") return approval.workflowApprovalType === "tool" ? "tool" : "workflow";
    if (actionName === "retry") return "workflow";
    return "";
  }
  if (["approve", "approve-remember"].includes(actionName)) return "tool";
  return "";
}

function workspaceRequirementMessage(requirement = {}) {
  const reason = approvalDisplayText(requirement.reason || t("chat.workspacePreflightReasonFallback"));
  const workspace = requirement.workspace?.display || requirement.workspace?.root || t("common.none");
  return `${t("chat.workspacePreflightTitle")}\n${t("chat.workspacePreflightBody", { reason, workspace })}\n${t("chat.workspacePreflightAction")}: #workspace`;
}

async function submitWorkflowApprovalAction(approval, actionName) {
  const action = workflowApprovalActionForName(approval, actionName);
  if (!action?.path) {
    throw new Error(localizedApprovalReason(action?.reason || approval.reason || "") || t("approvals.unavailable"));
  }
  if (action.available === false) {
    throw new Error(localizedApprovalReason(action.reason || approval.reason || "") || t("approvals.unavailable"));
  }
  if (workflowApprovalActionPrefersBackground(actionName, action)) {
    return submitWorkflowApprovalBackgroundAction(approval, action);
  }
  const response = await request(action.path, { method: action.method || "POST" });
  if (approval.callID) {
    return fetchWorkflowRun(approval.callID).catch(() => response);
  }
  return response;
}

async function submitWorkflowApprovalBackgroundAction(approval, action) {
  const accepted = await request(action.path, {
    method: action.method || "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ background: true })
  });
  const runID = String(accepted?.run_id || accepted?.runID || approval.callID || "").trim();
  const lines = [formatActionResult(accepted)].filter(Boolean);
  const since = workflowRunLatestSeq(accepted?.run || approval.run);
  if (runID) {
    await collectWorkflowApprovalActionPreview(runID, accepted, action, since, lines);
  }
  return lines.join("\n") || t("common.done");
}

async function collectWorkflowApprovalActionPreview(runID, accepted, action, since, lines) {
  const controller = new AbortController();
  let seenEvent = false;
  const timer = window.setTimeout(() => controller.abort(), approvalActionEventPreviewMS);
  try {
    await streamWorkflowRunEvents(runID, {
      eventsURL: approvalActionEventsURL(accepted, action),
      since,
      signal: controller.signal
    }, event => {
      if (workflowApprovalActionEventIsError(event)) {
        throw new Error(streamEventMessage(event, event?.content || event?.error || "") || t("approvals.actionFailed"));
      }
      const line = approvalStreamLogLine(event);
      if (line) {
        seenEvent = true;
        lines.push(line);
      }
      if (seenEvent && workflowApprovalActionEventConfirmsProgress(event)) {
        controller.abort();
      }
    });
  } catch (error) {
    if (error?.name !== "AbortError") throw error;
  } finally {
    window.clearTimeout(timer);
  }
}

function workflowApprovalActionEventIsError(event) {
  const type = String(event?.type || "").toLowerCase();
  return type === "workflow_run_error" || type === "error" || event?.is_error === true;
}

function workflowApprovalActionEventConfirmsProgress(event) {
  const type = String(event?.type || "").toLowerCase();
  if (!type || type === "heartbeat" || type === "sse_retry") return false;
  return [
    "workflow_result",
    "workflow_run",
    "workflow_stage_started",
    "stage_started",
    "stage_start",
    "tool_call",
    "tool_result",
    "approval_resolved",
    "action_accepted"
  ].includes(type) || Boolean(event?.status || event?.workflow_status || event?.stage || event?.next_stage);
}

function workflowApprovalActionForName(approval, actionName) {
  if (actionName === "cancel") return approval.actions?.cancel;
  if (actionName === "retry") return approval.actions?.retry;
  if (actionName === "deny") return approval.actions?.deny;
  if (actionName === "approve-all") return approval.actions?.approveAll;
  return approval.actions?.approve;
}

function workflowApprovalActionPrefersBackground(actionName, action = {}) {
  if (!action?.path || actionName === "cancel") return false;
  if (action.background === true) return true;
  return ["approve", "approve-all", "retry"].includes(actionName);
}

function formatActionResult(value) {
  if (!value) return t("common.done");
  if (typeof value === "string") return approvalDisplayText(value.trim()) || t("common.done");
  const summary = value.summary || value.final_message || value.message || value.status || value.workflow_status;
  if (summary) return approvalDisplayText(summary);
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return t("common.done");
  }
}

async function loadWorkflowApprovalContext(run) {
  const [hydrated, teamState] = await Promise.allSettled([
    hydrateWorkflowRunForApproval(run),
    run?.id ? fetchTeamState({ run_id: run.id }) : Promise.resolve(null)
  ]);
  return {
    run: hydrated.status === "fulfilled" ? hydrated.value : run,
    teamState: teamState.status === "fulfilled" ? normalizeTeamState(teamState.value) : normalizeTeamState(null)
  };
}

async function hydrateWorkflowRunForApproval(run) {
  if (!run?.id) return run;
  const next = { ...run };
  const [replay, artifacts, stages, diffs, evidence] = await Promise.allSettled([
    fetchWorkflowRunReplay(run),
    fetchWorkflowRunArtifacts(run),
    fetchWorkflowRunStages(run),
    fetchWorkflowRunDiffs(run, { include_content: true }),
    fetchWorkflowRunEvidence(run)
  ]);
  if (replay.status === "fulfilled") {
    mergeWorkflowReplayForApproval(next, replay.value);
  }
  if (artifacts.status === "fulfilled" && Array.isArray(artifacts.value)) {
    next.artifacts = artifacts.value;
  }
  if (stages.status === "fulfilled" && Array.isArray(stages.value)) {
    next.completed_stages = stages.value;
  }
  if (diffs.status === "fulfilled" && Array.isArray(diffs.value?.diffs)) {
    next.diffs = diffs.value.diffs;
  }
  if (evidence.status === "fulfilled") {
    const normalized = normalizeWorkflowRunEvidencePayload(evidence.value);
    next.evidence = normalized.items;
    if (normalized.quality && Object.keys(normalized.quality).length) {
      next.quality = normalized.quality;
    }
  }
  return next;
}

function mergeWorkflowReplayForApproval(run, replay) {
  if (!run || !replay || typeof replay !== "object") return run;
  if (replay.run && typeof replay.run === "object") Object.assign(run, replay.run);
  if (Array.isArray(replay.stages)) run.completed_stages = replay.stages;
  if (Array.isArray(replay.events)) run.events = replay.events;
  if (Array.isArray(replay.artifacts)) run.artifacts = replay.artifacts;
  if (Array.isArray(replay.timeline)) run.timeline = replay.timeline;
  if (Array.isArray(replay.diffs)) run.diffs = replay.diffs;
  if (Array.isArray(replay.actions)) run.actions = replay.actions;
  if (replay.navigation && typeof replay.navigation === "object") run.navigation = replay.navigation;
  if (replay.counts && typeof replay.counts === "object") run.counts = replay.counts;
  if (replay.quality && typeof replay.quality === "object") run.quality = replay.quality;
  if (Array.isArray(replay.collaboration)) run.collaboration = replay.collaboration;
  if (Array.isArray(replay.blackboard)) run.blackboard = replay.blackboard;
  if (replay.team_state && typeof replay.team_state === "object") run.team_state = replay.team_state;
  return run;
}

function approvalContextHTML(approval) {
  const parts = [
    approvalImpactSummaryHTML(approval),
    approvalSkillScriptHTML(approval),
    approvalToolRiskHTML(approval),
    approvalPauseReasonHTML(approval),
    approvalRunFactsHTML(approval),
    approvalDiffEvidenceHTML(approval),
    approvalEvidenceHTML(approval),
    approvalTeamContextHTML(approval)
  ].filter(Boolean);
  if (!parts.length) return "";
  const signalCount = approvalSignalCount(approval);
  return `<section class="approval-context-shell" aria-label="${escapeHTML(t("approvals.contextTitle"))}">
    <div class="approval-context-head">
      <div>
        <strong>${escapeHTML(t("approvals.contextTitle"))}</strong>
        <span>${escapeHTML(t("approvals.contextHelp"))}</span>
      </div>
      <small>${escapeHTML(t("approvals.contextSignals", { count: signalCount }))}</small>
    </div>
    ${parts.join("")}
  </section>`;
}

function approvalImpactSummaryHTML(approval) {
  const run = approval.run || {};
  const artifacts = workflowRunArtifacts(run);
  const stages = workflowRunStages(run);
  const diffs = workflowRunDiffs(run);
  const pendingTarget = approvalPendingTarget(approval);
  const items = [
    {
      label: t("approvals.impactRequestedAction"),
      value: approvalDisplayValue(approval.subject || approval.title || t("approvals.needsDecision")),
      detail: approval.workflowApprovalType === "tool" || approval.kind === "tool" ? t("approvals.impactToolHelp") : t("approvals.impactStageHelp"),
      tone: "warn"
    },
    {
      label: t("approvals.impactRunPosition"),
      value: approvalDisplayValue(approval.kind === "workflow" ? run.next_stage || t("approvals.currentStage") : approval.typeLabel),
      detail: approvalDisplayValue(approval.kind === "workflow" ? run.name || run.id || t("chat.workflow") : t("approvals.typeTool")),
      tone: "info"
    },
    {
      label: t("approvals.impactChangedFiles"),
      value: diffs.length ? t("approvals.impactChangedFilesCount", { count: diffs.length }) : t("approvals.impactNone"),
      detail: diffPathPreview(diffs) || t("approvals.impactChangedFilesHelp"),
      tone: diffs.length ? "info" : "neutral"
    },
    {
      label: t("approvals.impactEvidence"),
      value: t("approvals.impactEvidenceCount", { artifacts: artifacts.length, stages: stages.length }),
      detail: pendingTarget || t("approvals.impactEvidenceHelp"),
      tone: artifacts.length || stages.length ? "good" : "neutral"
    }
  ];
  return `<section class="approval-context-card approval-impact-card">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.impactTitle"))}</strong>
      <span>${escapeHTML(t("approvals.impactHelp"))}</span>
    </div>
    <div class="approval-impact-grid">
      ${items.map(item => `<article class="${escapeHTML(item.tone)}">
        <small>${escapeHTML(item.label)}</small>
        <strong>${escapeHTML(item.value)}</strong>
        <span>${escapeHTML(truncateText(item.detail, 130))}</span>
      </article>`).join("")}
    </div>
  </section>`;
}

function approvalSkillScriptHTML(approval) {
  const info = extractSkillScriptContextFromApproval(approval);
  if (!info) return "";
  const facts = [
    info.skill ? [t("approvals.skillScriptSkill"), info.skill] : null,
    info.script ? [t("approvals.skillScriptScript"), info.script] : null,
    info.path ? [t("approvals.skillScriptPath"), info.path] : null,
    info.runtime ? [t("approvals.skillScriptRuntime"), info.runtime] : null,
    info.outputKind ? [t("approvals.skillScriptOutput"), info.outputKind] : null,
    info.workspaceMount ? [t("approvals.skillScriptWorkspaceMount"), approvalSkillScriptWorkspaceMountLabel(info.workspaceMount)] : null,
    info.network ? [t("approvals.skillScriptNetwork"), approvalSkillScriptNetworkLabel(info.network)] : null,
    info.approval ? [t("approvals.skillScriptApproval"), approvalSkillScriptApprovalLabel(info.approval)] : null,
    info.timeout ? [t("approvals.skillScriptTimeout"), info.timeout] : null
  ].filter(Boolean);
  const chips = [
    info.runtime ? approvalRiskChipHTML(info.runtime, "neutral") : "",
    info.approval ? approvalRiskChipHTML(approvalSkillScriptApprovalLabel(info.approval), info.approval === "required" ? "warn" : "good") : "",
    info.workspaceMount ? approvalRiskChipHTML(t("approvals.skillScriptWorkspaceChip", { mount: info.workspaceMount }), info.workspaceMount === "rw" ? "warn" : "good") : "",
    info.network ? approvalRiskChipHTML(t("approvals.skillScriptNetworkChip", { network: info.network }), info.network === "disabled" ? "good" : "warn") : ""
  ].filter(Boolean).join("");
  const label = [info.skill, info.script].filter(Boolean).join(" / ") || info.path || t("approvals.skillScriptUnknown");
  return `<section class="approval-context-card approval-skill-script-card">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.skillScriptTitle"))}</strong>
      <span>${escapeHTML(label)}</span>
    </div>
    ${chips ? `<div class="resource-risk-chips approval-risk-chips">${chips}</div>` : ""}
    <p>${escapeHTML(localizedText(info.sandboxNote || t("approvals.skillScriptHelp")))}</p>
    ${facts.length ? `<div class="approval-fact-grid compact">${facts.map(([labelText, value]) => `<span><small>${escapeHTML(labelText)}</small><b>${escapeHTML(approvalDisplayValue(value))}</b></span>`).join("")}</div>` : ""}
    ${info.argsPreview ? `<div class="resource-risk-warnings approval-risk-warnings"><span>${escapeHTML(t("approvals.skillScriptArgsPreview", { args: info.argsPreview }))}</span></div>` : ""}
  </section>`;
}

function approvalToolRiskHTML(approval) {
  const docs = Array.isArray(approval.toolRiskDocs) ? approval.toolRiskDocs : [];
  const names = Array.isArray(approval.toolNames) ? approval.toolNames : [];
  if (!docs.length && !names.length) return "";
  const strongest = approval.toolRisk || strongestToolRisk(docs);
  const risk = strongest?.risk || {};
  const doc = strongest?.doc || {};
  const container = approvalToolContainerSummary(risk, doc);
  const level = String(risk.risk_level || "").trim();
  const tone = approvalToolRiskTone(risk);
  const workspaceScoped = risk.workspace_scoped_inputs === true;
  const workspaceEnforced = risk.workspace_scope_enforced === true;
  const chips = [
    level ? approvalRiskChipHTML(approvalToolRiskLabel(level), tone) : approvalRiskChipHTML(t("approvals.toolRiskUnknown"), "neutral"),
    risk.requires_approval ? approvalRiskChipHTML(t("approvals.toolRiskApproval"), "warn") : "",
    risk.destructive ? approvalRiskChipHTML(t("approvals.toolRiskDestructive"), "bad") : "",
    risk.sandboxed ? approvalRiskChipHTML(t("approvals.toolRiskSandboxed"), "good") : "",
    risk.external_sandbox_recommended ? approvalRiskChipHTML(t("approvals.toolRiskExternalSandbox"), "warn") : "",
    workspaceScoped ? approvalRiskChipHTML(t("approvals.toolRiskWorkspaceInputs"), workspaceEnforced ? "good" : "warn") : "",
    workspaceScoped ? approvalRiskChipHTML(workspaceEnforced ? t("approvals.toolRiskWorkspaceEnforced") : t("approvals.toolRiskWorkspaceNotEnforced"), workspaceEnforced ? "good" : "warn") : "",
    ...approvalToolContainerChips(container)
  ].filter(Boolean).join("");
  const facts = [
    risk.kind ? [t("approvals.toolRiskKind"), approvalKindLabel(risk.kind)] : null,
    risk.isolation_level || risk.isolation ? [t("approvals.toolRiskIsolation"), approvalIsolationLabel(risk.isolation_level || risk.isolation)] : null,
    Array.isArray(risk.capabilities) && risk.capabilities.length ? [t("approvals.toolRiskCapabilities"), risk.capabilities.slice(0, 4).join(", ")] : null,
    workspaceScoped ? [t("approvals.toolRiskWorkspaceScope"), workspaceEnforced ? t("approvals.toolRiskWorkspaceEnforced") : t("approvals.toolRiskWorkspaceNotEnforced")] : null,
    container.isolation_profile ? [t("catalog.isolationProfile"), approvalIsolationProfileLabel(container.isolation_profile)] : null,
    container.container_image ? [t("catalog.isolationImage"), container.container_image] : null,
    container.container_pull_policy ? [t("catalog.isolationPullPolicy"), approvalContainerPullPolicyLabel(container.container_pull_policy)] : null,
    container.sandbox_features.length ? [t("catalog.sandboxFeatures"), approvalSandboxFeatureListLabel(container.sandbox_features)] : null,
    container.missing_sandbox_features.length ? [t("catalog.sandboxMissingFeatures"), approvalSandboxFeatureListLabel(container.missing_sandbox_features)] : null,
    container.windows_isolation ? [t("catalog.windowsIsolation"), approvalWindowsIsolationLabel(container.windows_isolation)] : null
  ].filter(Boolean);
  const warnings = [
    workspaceScoped && !workspaceEnforced ? t("approvals.toolRiskWorkspaceNotEnforcedHelp") : "",
    ...(Array.isArray(risk.warnings) ? risk.warnings : [])
  ].filter(Boolean).slice(0, 4);
  const summary = risk.security_boundary || doc.description || (docs.length ? t("approvals.toolRiskNoSummary") : t("approvals.toolRiskNoProfile"));
  return `<section class="approval-context-card approval-tool-risk-card ${escapeHTML(tone)}">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.toolRiskTitle"))}</strong>
      <span>${escapeHTML(names.slice(0, 4).join(", ") || doc.name || t("approvals.subject"))}</span>
    </div>
    <div class="resource-risk-chips approval-risk-chips">${chips}</div>
    <p>${escapeHTML(approvalDisplayText(summary))}</p>
    ${facts.length ? `<div class="approval-fact-grid compact">${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><b>${escapeHTML(approvalDisplayValue(value))}</b></span>`).join("")}</div>` : ""}
    ${warnings.length ? `<div class="resource-risk-warnings approval-risk-warnings">${warnings.map(item => `<span>${escapeHTML(approvalDisplayText(item))}</span>`).join("")}</div>` : ""}
  </section>`;
}

function approvalRiskChipHTML(label, tone = "") {
  return `<span class="resource-risk-chip ${escapeHTML(tone)}">${escapeHTML(label)}</span>`;
}

function approvalToolContainerSummary(risk = {}, doc = {}) {
  const runtime = doc?.runtime || {};
  const riskOptions = risk?.isolation_options || {};
  const docOptions = doc?.isolation_options || {};
  return {
    isolation_profile: firstNonEmpty(runtime.isolation_profile, risk.isolation_profile, doc.isolation_profile, riskOptions.profile, docOptions.profile),
    container_image: firstNonEmpty(runtime.container_image, risk.container_image, doc.container_image, riskOptions.image, docOptions.image),
    container_image_reference_type: firstNonEmpty(runtime.container_image_reference_type, risk.container_image_reference_type, doc.container_image_reference_type),
    container_image_digest_pinned: firstBoolean(runtime.container_image_digest_pinned, risk.container_image_digest_pinned, doc.container_image_digest_pinned),
    container_image_production_ready: firstBoolean(runtime.container_image_production_ready, risk.container_image_production_ready, doc.container_image_production_ready),
    container_pull_policy: firstNonEmpty(runtime.container_pull_policy, risk.container_pull_policy, doc.container_pull_policy, riskOptions.pull_policy, docOptions.pull_policy),
    sandbox_features: firstArray(runtime.sandbox_features, risk.sandbox_features, doc.sandbox_features),
    missing_sandbox_features: firstArray(runtime.missing_sandbox_features, risk.missing_sandbox_features, doc.missing_sandbox_features),
    windows_isolation: runtime.windows_isolation || risk.windows_isolation || doc.windows_isolation || null
  };
}

function approvalToolContainerChips(container = {}) {
  const chips = [];
  const refType = String(container.container_image_reference_type || "").trim();
  if (refType) {
    chips.push(approvalRiskChipHTML(approvalContainerImageReferenceLabel(refType), approvalContainerImageReferenceTone(refType)));
  }
  if (typeof container.container_image_digest_pinned === "boolean") {
    chips.push(approvalRiskChipHTML(container.container_image_digest_pinned ? t("catalog.containerDigestPinned") : t("catalog.containerDigestNotPinned"), container.container_image_digest_pinned ? "good" : "warn"));
  }
  if (typeof container.container_image_production_ready === "boolean") {
    chips.push(approvalRiskChipHTML(container.container_image_production_ready ? t("catalog.containerProductionReady") : t("catalog.containerProductionNotReady"), container.container_image_production_ready ? "good" : "warn"));
  }
  if (container.container_pull_policy) {
    chips.push(approvalRiskChipHTML(approvalContainerPullPolicyLabel(container.container_pull_policy), approvalContainerPullPolicyTone(container.container_pull_policy)));
  }
  if (Array.isArray(container.sandbox_features) && container.sandbox_features.length) {
    chips.push(approvalRiskChipHTML(t("catalog.sandboxEnabledCount", { count: container.sandbox_features.length }), "good"));
  }
  if (Array.isArray(container.missing_sandbox_features) && container.missing_sandbox_features.length) {
    chips.push(approvalRiskChipHTML(t("catalog.sandboxMissingCount", { count: container.missing_sandbox_features.length }), "warn"));
  }
  return chips;
}

function approvalContainerImageReferenceTone(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized === "digest") return "good";
  if (normalized === "floating" || normalized === "missing") return "warn";
  return "neutral";
}

function approvalContainerImageReferenceLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.containerImageRef.${normalized}`);
  return translated === `catalog.containerImageRef.${normalized}` ? localizedText(value) : translated;
}

function approvalContainerPullPolicyTone(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized === "never") return "good";
  if (normalized === "always") return "warn";
  return "neutral";
}

function approvalContainerPullPolicyLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.pullPolicy.${normalized}`);
  return translated === `catalog.pullPolicy.${normalized}` ? localizedText(value) : translated;
}

function approvalIsolationProfileLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const translated = t(`catalog.isolationProfile.${normalized}`);
  return translated === `catalog.isolationProfile.${normalized}` ? localizedText(value) : translated;
}

function approvalSandboxFeatureListLabel(features = []) {
  return features.slice(0, 4).map(approvalSandboxFeatureLabel).join(", ");
}

function approvalSandboxFeatureLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  const key = `catalog.sandboxFeature.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function approvalWindowsIsolationLabel(profile = {}) {
  const labels = [
    profile.job_object ? t("catalog.windowsIsolation.jobObject") : "",
    profile.restricted_token ? t("catalog.windowsIsolation.restrictedToken") : "",
    profile.app_container ? t("catalog.windowsIsolation.appContainer") : "",
    profile.lifecycle_only ? t("catalog.windowsIsolation.lifecycleOnly") : ""
  ].filter(Boolean);
  return labels.join(", ") || t("catalog.windowsIsolation.none");
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = String(value || "").trim();
    if (text) return text;
  }
  return "";
}

function firstBoolean(...values) {
  return values.find(value => typeof value === "boolean");
}

function firstArray(...values) {
  const found = values.find(value => Array.isArray(value) && value.length);
  return found ? found.slice(0, 8) : [];
}

function approvalSkillScriptApprovalLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `approvals.skillScriptApproval.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function approvalSkillScriptWorkspaceMountLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `approvals.skillScriptWorkspaceMount.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function approvalSkillScriptNetworkLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `approvals.skillScriptNetwork.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function approvalPendingTarget(approval) {
  const candidates = [
    approval.run?.pending_arguments,
    approval.run?.pending_arguments_summary,
    approval.raw?.arguments_summary,
    approval.summary
  ];
  const text = candidates.map(value => String(value || "").trim()).find(Boolean) || "";
  const pathMatch = text.match(/\bpath=([^\s]+)/i);
  if (pathMatch?.[1]) return t("approvals.impactPendingPath", { path: pathMatch[1] });
  return text ? approvalDisplayText(truncateText(text, 120)) : "";
}

function approvalPauseReasonHTML(approval) {
  const reason = localizedApprovalReason(approval.reason || "") || approvalDisplayText(approval.summary || t("approvals.pauseReasonFallback"));
  return `<section class="approval-context-card approval-reason-card">
    <strong>${escapeHTML(t("approvals.pauseReasonTitle"))}</strong>
    <p>${escapeHTML(reason)}</p>
  </section>`;
}

function approvalRunFactsHTML(approval) {
  if (approval.kind !== "workflow" || !approval.run) return "";
  const run = approval.run;
  const facts = [
    [t("approvals.workflowName"), run.name],
    [t("approvals.currentStage"), run.next_stage],
    [t("approvals.pendingTool"), run.pending_tool_name],
    [t("approvals.runStatus"), approvalDisplayValue(run.status)],
    [t("approvals.runID"), run.id]
  ].filter(([, value]) => String(value || "").trim());
  if (!facts.length) return "";
  return `<section class="approval-context-card">
    <strong>${escapeHTML(t("approvals.runFactsTitle"))}</strong>
    <div class="approval-fact-grid">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><b>${escapeHTML(approvalDisplayValue(value))}</b></span>`).join("")}
    </div>
  </section>`;
}

function approvalDiffEvidenceHTML(approval) {
  if (approval.kind !== "workflow" || !approval.run) return "";
  const diffs = workflowRunDiffs(approval.run).slice(0, 4);
  if (!diffs.length) return "";
  return `<section class="approval-context-card approval-diff-card">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.diffTitle"))}</strong>
      <span>${escapeHTML(t("approvals.diffCount", { count: workflowRunDiffs(approval.run).length }))}</span>
    </div>
    <div class="approval-diff-list">
      ${diffs.map(approvalDiffRow).join("")}
    </div>
  </section>`;
}

function approvalDiffRow(diff) {
  const summary = approvalDisplayText(diff.summary || diffLineSummary(diff));
  const meta = [
    diff.stage ? `${t("approvals.currentStage")}: ${approvalDisplayValue(diff.stage)}` : "",
    diff.status_code || diff.status ? `${t("approvals.diffStatus")}: ${approvalDisplayValue(diff.status_code || diff.status)}` : "",
    diff.tool_name ? `${t("chat.tool")}: ${approvalDisplayValue(diff.tool_name)}` : "",
    diffStatText(diff)
  ].filter(Boolean).join(" / ");
  return approvalEvidenceRowHTML(t("approvals.diffLabel"), diff.path || t("approvals.diffUnknownPath"), summary, meta, "");
}

function approvalEvidenceHTML(approval) {
  if (approval.kind !== "workflow" || !approval.run) return "";
  const artifacts = workflowRunArtifacts(approval.run).slice(0, 3);
  const stages = workflowRunStages(approval.run).slice(-3).reverse();
  if (!artifacts.length && !stages.length) return "";
  return `<section class="approval-context-card">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.evidenceTitle"))}</strong>
      <span>${escapeHTML(t("approvals.evidenceCount", { artifacts: artifacts.length, stages: stages.length }))}</span>
    </div>
    <div class="approval-evidence-list">
      ${artifacts.map(approvalArtifactRow).join("")}
      ${stages.map(approvalStageRow).join("")}
    </div>
  </section>`;
}

function approvalArtifactRow(artifact) {
  const title = approvalDisplayValue(artifact.title || artifact.name || artifact.id || artifact.kind || t("approvals.artifactFallback"));
  const summary = approvalDisplayText(artifact.summary || artifact.content || t("approvals.noEvidenceSummary"));
  const meta = [
    artifact.stage ? `${t("approvals.currentStage")}: ${approvalDisplayValue(artifact.stage)}` : "",
    artifact.kind ? `${t("approvals.artifactKind")}: ${approvalArtifactKindLabel(artifact.kind)}` : ""
  ].filter(Boolean).join(" / ");
  return approvalEvidenceRowHTML(t("approvals.artifactLabel"), title, summary, meta, artifact.is_error ? "bad" : "");
}

function approvalStageRow(stage) {
  const title = approvalDisplayValue(stage.stage || stage.name || t("approvals.stageFallback"));
  const summary = approvalDisplayText(stage.summary || stage.result?.output || t("approvals.noEvidenceSummary"));
  const meta = [
    stage.status ? `${t("approvals.runStatus")}: ${approvalDisplayValue(stage.status)}` : "",
    stage.agent_id ? `${t("chat.agent")}: ${approvalDisplayValue(stage.agent_id)}` : "",
    stage.skill ? `${t("chat.skill")}: ${approvalDisplayValue(stage.skill)}` : "",
    stage.tool ? `${t("chat.tool")}: ${approvalDisplayValue(stage.tool)}` : ""
  ].filter(Boolean).join(" / ");
  return approvalEvidenceRowHTML(t("approvals.stageLabel"), title, summary, meta, workflowRunStageHasError(stage) ? "bad" : "");
}

function approvalEvidenceRowHTML(label, title, summary, meta, tone) {
  return `<article class="approval-evidence-row ${tone}">
    <div>
      <span>${escapeHTML(label)}</span>
      <strong>${escapeHTML(title)}</strong>
      ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
    </div>
    <p>${escapeHTML(truncateText(summary, 300))}</p>
  </article>`;
}

function approvalKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  if (!value) return "";
  const key = `catalog.toolRiskKind.${value}`;
  const translated = t(key);
  return translated === key ? kind : translated;
}

function approvalIsolationLabel(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  const key = `catalog.isolation.${raw}`;
  const translated = t(key);
  return translated === key ? raw : translated;
}

function approvalArtifactKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  if (!value) return "";
  if (value === "acceptance") return t("chat.acceptanceArtifact");
  const key = `chat.artifactKind.${value}`;
  const translated = t(key);
  return translated === key ? kind : translated;
}

function approvalTeamContextHTML(approval) {
  const teamState = approval.context?.teamState || normalizeTeamState(null);
  const groups = [
    [t("approvals.teamDecisions"), teamState.decisions, "decision"],
    [t("approvals.teamRisks"), teamState.risks, "risk"],
    [t("approvals.teamQuestions"), teamState.questions, "question"],
    [t("approvals.teamCritiques"), teamState.critiques, "critique"],
    [t("approvals.teamOtherRecords"), teamState.other_records, "record"]
  ];
  const groupHTML = groups.map(([title, items, tone]) => approvalTeamGroupHTML(title, items, tone)).filter(Boolean).join("");
  const facts = [
    [t("approvals.team"), teamState.team],
    [t("approvals.teamOwner"), teamState.active_owner],
    [t("approvals.currentStage"), teamState.next_stage || teamState.team_stage]
  ].filter(([, value]) => String(value || "").trim());
  if (!groupHTML && !facts.length) return "";
  return `<section class="approval-context-card approval-team-card">
    <div class="approval-context-subhead">
      <strong>${escapeHTML(t("approvals.teamContextTitle"))}</strong>
      <span>${escapeHTML(t("approvals.teamContextHelp"))}</span>
    </div>
    ${facts.length ? `<div class="approval-fact-grid compact">${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><b>${escapeHTML(approvalDisplayValue(value))}</b></span>`).join("")}</div>` : ""}
    ${groupHTML}
  </section>`;
}

function approvalTeamGroupHTML(title, items, tone) {
  const visible = normalizeCollection(items).slice(0, 2);
  if (!visible.length) return "";
  return `<div class="approval-team-group ${tone}">
    <div class="approval-team-group-head">
      <strong>${escapeHTML(title)}</strong>
      <small>${visible.length}</small>
    </div>
    ${visible.map(item => approvalTeamItemHTML(item, tone)).join("")}
  </div>`;
}

function approvalTeamItemHTML(item, tone) {
  const meta = [
    item.stage ? `${t("approvals.currentStage")}: ${approvalDisplayValue(item.stage)}` : "",
    item.agent_id ? `${t("chat.agent")}: ${approvalDisplayValue(item.agent_id)}` : "",
    item.status ? `${t("approvals.runStatus")}: ${approvalDisplayValue(item.status)}` : ""
  ].filter(Boolean).join(" / ");
  const title = approvalDisplayValue(item.title || item.subject || teamFallbackTitle(tone));
  const content = approvalDisplayText(item.content || item.summary || t("approvals.noTeamContent"));
  return `<article class="approval-team-item ${tone}">
    <strong>${escapeHTML(title)}</strong>
    ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
    <p>${escapeHTML(truncateText(content, 260))}</p>
  </article>`;
}

function teamFallbackTitle(tone) {
  if (tone === "decision") return t("approvals.teamDecisions");
  if (tone === "risk") return t("approvals.teamRisks");
  if (tone === "question") return t("approvals.teamQuestions");
  if (tone === "critique") return t("approvals.teamCritiques");
  if (tone === "record") return t("approvals.teamOtherRecords");
  return t("approvals.teamContextTitle");
}

function approvalSignalCount(approval) {
  const run = approval.run || {};
  const teamState = approval.context?.teamState || {};
  const count = workflowRunArtifacts(run).length
    + workflowRunStages(run).length
    + workflowRunDiffs(run).length
    + ["decisions", "risks", "questions", "critiques", "other_records"].reduce((total, key) => total + (teamState[key]?.length || 0), 0);
  return Math.max(1, count);
}

function workflowRunArtifacts(run) {
  const direct = Array.isArray(run?.artifacts) ? run.artifacts : [];
  const stageArtifacts = workflowRunStages(run).flatMap(stage => Array.isArray(stage.artifacts) ? stage.artifacts : []);
  const evidenceArtifacts = workflowRunEvidenceItems(run)
    .filter(item => workflowRunEvidenceKind(item) !== "stage")
    .map(workflowRunEvidenceArtifact);
  return dedupeWorkflowApprovalArtifacts([...evidenceArtifacts, ...direct, ...stageArtifacts]);
}

function workflowRunStages(run) {
  const direct = Array.isArray(run?.completed_stages) ? run.completed_stages : [];
  const evidenceStages = workflowRunEvidenceItems(run)
    .filter(item => workflowRunEvidenceKind(item) === "stage")
    .map(workflowRunEvidenceStage);
  return dedupeWorkflowApprovalStages([...direct, ...evidenceStages]);
}

function workflowRunDiffs(run) {
  return Array.isArray(run?.diffs) ? run.diffs : [];
}

function normalizeWorkflowRunEvidencePayload(payload) {
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

function workflowRunEvidenceItems(run) {
  return Array.isArray(run?.evidence) ? run.evidence : [];
}

function workflowRunEvidenceKind(item = {}) {
  return String(item.kind || item.type || item.evidence_kind || "").trim().toLowerCase();
}

function workflowRunEvidenceArtifact(item = {}) {
  const metadata = item.metadata && typeof item.metadata === "object" ? item.metadata : {};
  return {
    ...item,
    id: item.id || item.evidence_id || item.name,
    name: item.name || item.id || item.evidence_id,
    kind: item.kind || item.type || "evidence",
    title: item.title || item.label || item.name || item.id,
    summary: item.summary || item.reason || item.message || item.description || item.content || "",
    content: item.content || item.body || item.raw || "",
    stage: item.stage || item.stage_name || metadata.stage,
    is_error: item.is_error || ["fail", "failed", "error"].includes(String(item.status || metadata.status || "").toLowerCase()),
    metadata
  };
}

function workflowRunEvidenceStage(item = {}) {
  return {
    ...item,
    stage: item.stage || item.stage_name || item.name,
    name: item.stage || item.stage_name || item.name,
    summary: item.summary || item.message || item.description || item.content || "",
    status: item.status || item.result?.status || "",
    artifacts: Array.isArray(item.artifacts) ? item.artifacts : []
  };
}

function dedupeWorkflowApprovalArtifacts(items) {
  const seen = new Set();
  const out = [];
  for (const item of items.filter(Boolean)) {
    const key = [item.id || "", item.name || "", item.stage || "", item.kind || "", item.title || ""].join("|");
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}

function dedupeWorkflowApprovalStages(items) {
  const seen = new Set();
  const out = [];
  for (const item of items.filter(Boolean)) {
    const key = [item.stage || item.name || "", item.status || "", item.summary || item.result?.output || ""].join("|");
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}

function diffPathPreview(diffs) {
  const paths = (Array.isArray(diffs) ? diffs : [])
    .map(item => String(item?.path || "").trim())
    .filter(Boolean);
  if (!paths.length) return "";
  const visible = paths.slice(0, 3).join(", ");
  return paths.length > 3 ? t("approvals.diffPreviewMore", { files: visible, count: paths.length - 3 }) : visible;
}

function diffStatText(diff) {
  const added = Number(diff?.added_lines || 0);
  const deleted = Number(diff?.deleted_lines || 0);
  if (!added && !deleted) return "";
  return `+${added} / -${deleted}`;
}

function diffLineSummary(diff) {
  const parts = [
    diff?.status || diff?.status_code || "",
    diff?.bytes_written ? t("approvals.diffBytesWritten", { count: diff.bytes_written }) : ""
  ].filter(Boolean);
  return parts.join(" / ") || t("approvals.diffNoSummary");
}

function workflowRunStageHasError(stage) {
  const status = String(stage?.status || "").toLowerCase();
  if (["failed", "error", "denied", "blocked", "cancelled", "canceled"].includes(status)) return true;
  return Array.isArray(stage?.result?.tool_results) && stage.result.tool_results.some(item => item?.is_error);
}

function normalizeTeamState(payload) {
  const state = payload && typeof payload === "object" ? payload : {};
  const result = {
    team: String(state.team || "").trim(),
    active_owner: String(state.active_owner || "").trim(),
    next_stage: String(state.next_stage || "").trim(),
    team_stage: String(state.team_stage || "").trim(),
    blackboard: normalizeCollection(state.blackboard),
    decisions: normalizeCollection(state.decisions),
    risks: normalizeCollection(state.risks),
    questions: normalizeCollection(state.questions),
    critiques: normalizeCollection(state.critiques),
    assignments: normalizeCollection(state.assignments),
    escalations: normalizeCollection(state.escalations),
    approvals: normalizeCollection(state.approvals),
    rejections: normalizeCollection(state.rejections),
    other_records: []
  };
  result.other_records = teamOtherRecords(result);
  return result;
}

function teamOtherRecords(teamState) {
  const explicit = [
    ...normalizeCollection(teamState.assignments),
    ...normalizeCollection(teamState.escalations),
    ...normalizeCollection(teamState.approvals),
    ...normalizeCollection(teamState.rejections)
  ];
  const knownIDs = new Set(
    ["decisions", "risks", "questions", "critiques", "assignments", "escalations", "approvals", "rejections"]
      .flatMap(key => normalizeCollection(teamState[key]))
      .map(teamRecordID)
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
    const id = teamRecordID(item);
    if (id && knownIDs.has(id)) return false;
    return !knownKinds.includes(String(item?.kind || "").trim().toLowerCase());
  });
  return [...explicit, ...fallback];
}

function teamRecordID(item) {
  return String(item?.id || item?.ID || "").trim();
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

function workflowRunSortValue(run) {
  const raw = run?.updated_at || run?.completed_at || run?.started_at || "";
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

function workflowRunLatestSeq(run) {
  const events = Array.isArray(run?.events) ? run.events : [];
  return events.reduce((max, event) => {
    const seq = Number(event?.seq || event?.sse_id || 0);
    return Number.isFinite(seq) && seq > max ? seq : max;
  }, 0);
}

async function streamApproval(callID, action) {
  return streamActionPath(`/api/approvals/${encodeURIComponent(callID)}/${action}/stream`, "POST");
}

async function streamActionPath(path, method = "POST") {
  const response = await fetch(path, { method });
  const text = await response.text();
  if (!response.ok) throw new Error(text || response.statusText);
  return parseApprovalStreamText(text);
}

function parseApprovalStreamText(text) {
  const frames = String(text || "").split(/\r?\n\r?\n/).map(frame => frame.trim()).filter(Boolean);
  if (!frames.length) return text || t("common.done");
  const lines = [];
  for (const frame of frames) {
    const event = parseApprovalSSEFrame(frame);
    if (!event) continue;
    const type = String(event.payload?.type || event.eventType || "").toLowerCase();
    if (type === "error" || event.eventType === "error") {
      throw new Error(streamEventMessage(event.payload, event.rawData) || t("approvals.actionFailed"));
    }
    const line = approvalStreamLogLine(event.payload);
    if (line) lines.push(line);
  }
  return lines.join("\n") || t("common.done");
}

function parseApprovalSSEFrame(frame) {
  const eventLine = frame.split(/\r?\n/).find(line => line.startsWith("event:"));
  const eventType = String(eventLine ? eventLine.slice(6) : "").trim();
  const rawData = frame.split(/\r?\n/)
    .filter(line => line.startsWith("data:"))
    .map(line => line.slice(5).trim())
    .join("\n");
  if (!rawData) return null;
  try {
    const payload = JSON.parse(rawData);
    payload.type = payload.type || eventType;
    return { eventType, payload, rawData };
  } catch {
    return { eventType, payload: { type: eventType || "text", content: rawData }, rawData };
  }
}

function approvalStreamLogLine(payload) {
  const type = String(payload?.type || "").toLowerCase();
  if (type === "workflow_result") {
    return [payload.workflow_name || t("chat.workflow"), approvalDisplayValue(payload.workflow_status || payload.status), approvalDisplayValue(payload.next_stage)]
      .filter(Boolean)
      .join(" - ");
  }
  if (type === "final_message") return approvalDisplayText(truncateText(payload.content || payload.message || "", 500));
  if (type === "tool_result") {
    return `${approvalDisplayValue(payload.tool_name || t("chat.tool"))}: ${approvalDisplayText(truncateText(payload.content || payload.message || "", 500))}`;
  }
  if (payload?.content || payload?.message) return approvalDisplayText(truncateText(payload.content || payload.message, 500));
  return "";
}

function streamEventMessage(payload, rawData) {
  return String(payload?.content || payload?.message || payload?.error || rawData || "").trim();
}

function localizedApprovalReason(reason) {
  const value = String(reason || "").trim();
  if (!value) return "";
  const lower = value.toLowerCase();
  if (lower.includes("tool approval context") && lower.includes("retry or cancel")) {
    return t("approvals.workflowToolContextLost");
  }
  if (lower.includes("cannot be resumed durably") || lower.includes("approval type cannot be resumed")) {
    return t("approvals.workflowApprovalNotResumable");
  }
  if (lower.includes("in-memory model/tool context")) {
    return t("approvals.workflowToolContextRequired");
  }
  return localizedText(value);
}
