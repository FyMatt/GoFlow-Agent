import { fetchWorkflowRunActions } from "./api.js";

const visibleApprovalCountKey = "__goflow_visible_approval_count";

export function pendingApprovalCount(runtime) {
  const pendingTools = Array.isArray(runtime?.session?.pending_approvals)
    ? runtime.session.pending_approvals
    : [];
  const runs = normalizeRuntimeWorkflowRuns(runtime);
  return visibleApprovalCount(pendingTools, runs);
}

export function runtimeVisibleApprovalCount(runtime) {
  const resolved = Number(runtime?.[visibleApprovalCountKey]);
  return Number.isFinite(resolved) ? resolved : pendingApprovalCount(runtime);
}

export function setRuntimeVisibleApprovalCount(runtime, count) {
  if (!runtime || !Number.isFinite(Number(count))) return runtime;
  try {
    Object.defineProperty(runtime, visibleApprovalCountKey, {
      value: Number(count),
      writable: true,
      configurable: true,
      enumerable: false
    });
  } catch {
    runtime[visibleApprovalCountKey] = Number(count);
  }
  return runtime;
}

export function normalizeRuntimeWorkflowRuns(runtime) {
  const value = runtime?.session?.workflow_runs;
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  return [];
}

export function normalizeWorkflowRunList(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  if (Array.isArray(value?.items)) return value.items;
  return [];
}

export async function visibleApprovalCountFromActions(pendingTools, runs) {
  const workflowRuns = await workflowApprovalRunsWithActions(runs);
  return visibleApprovalCount(pendingTools, workflowRuns);
}

function visibleApprovalCount(pendingTools, runs) {
  const workflowRuns = workflowApprovalRuns(runs);
  const byCall = workflowToolRunIndex(workflowRuns);
  const byScope = workflowToolRunScopeIndex(workflowRuns);
  const hiddenToolCallIDs = pendingToolCallIDs(pendingTools);
  const representedRunIDs = new Set();
  let count = 0;

  for (const approval of pendingTools || []) {
    const workflowRun = matchingWorkflowToolRun(approval, byCall, byScope);
    if (!workflowRun) {
      if (!String(approval?.workflow_name || "").trim()) count += 1;
      continue;
    }
    if (!representedRunIDs.has(workflowRun.id)) {
      representedRunIDs.add(workflowRun.id);
      count += 1;
    }
  }

  for (const run of workflowRuns) {
    if (representedRunIDs.has(run.id)) continue;
    const status = String(run.status || "").toLowerCase();
    if (status !== "awaiting_tool_approval") {
      count += 1;
      continue;
    }
    const callID = String(run.pending_call_id || "").trim();
    if (!callID || !hiddenToolCallIDs.has(callID)) count += 1;
  }

  return count;
}

async function workflowApprovalRunsWithActions(runs) {
  const candidates = workflowApprovalRuns(runs).slice(0, 16);
  const hydrated = await Promise.all(candidates.map(async run => {
    if (!run?.id) return null;
    let actions = [];
    try {
      actions = normalizeCollection(await fetchWorkflowRunActions(run));
    } catch {
      actions = normalizeCollection(run.actions);
      if (!actions.length) return run;
    }
    const hasApprovalAction = actions.some(action => action?.name === "approve_stage" || action?.name === "approve_tool");
    return hasApprovalAction ? run : null;
  }));
  return hydrated.filter(Boolean);
}

function normalizeCollection(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.items)) return value.items;
  if (Array.isArray(value?.value)) return value.value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  return [];
}

function workflowApprovalRuns(runs) {
  return (runs || [])
    .filter(run => {
      const status = String(run.status || "").toLowerCase();
      return status === "awaiting_approval" || status === "awaiting_tool_approval";
    })
    .sort((a, b) => workflowRunSortValue(b) - workflowRunSortValue(a));
}

function workflowToolRunIndex(runs) {
  const indexed = new Map();
  for (const run of runs || []) {
    if (String(run.status || "").toLowerCase() !== "awaiting_tool_approval") continue;
    const callID = String(run.pending_call_id || "").trim();
    if (callID) indexed.set(callID, run);
  }
  return indexed;
}

function workflowToolRunScopeIndex(runs) {
  const indexed = new Map();
  for (const run of runs || []) {
    if (String(run.status || "").toLowerCase() !== "awaiting_tool_approval") continue;
    const key = workflowApprovalScopeKey(run.name, run.next_stage);
    if (key && !indexed.has(key)) indexed.set(key, run);
  }
  return indexed;
}

function matchingWorkflowToolRun(approval, byCall, byScope) {
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

function pendingToolCallIDs(approvals) {
  return new Set((approvals || [])
    .map(approval => String(approval?.call_id || "").trim())
    .filter(Boolean));
}

function workflowRunSortValue(run) {
  const raw = run?.updated_at || run?.completed_at || run?.started_at || "";
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}
