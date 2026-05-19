export async function request(path, options = {}) {
  const response = await fetch(path, options);
  const text = await response.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { message: text };
    }
  }
  if (!response.ok) {
    const message = data?.message || data?.error || text || response.statusText;
    const error = new Error(message);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

export function postJSON(path, body) {
  return request(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {})
  });
}

export function checkWorkspaceRequirement(payload = {}) {
  return postJSON("/api/workspace/requirement", payload);
}

export function discoveryMetaSupported(payload, options = {}) {
  const envelope = payload && typeof payload === "object" ? payload : {};
  const meta = envelope.meta && typeof envelope.meta === "object" ? envelope.meta : {};
  const hasDiscoveryMeta = Boolean(Object.keys(meta).length)
    || "schema_version" in envelope
    || "schemaVersion" in envelope
    || "min_supported_schema_version" in envelope
    || "minSupportedSchemaVersion" in envelope;
  if (!hasDiscoveryMeta) return true;
  const expectedSchema = options.schema || "goflow.api.discovery";
  const supportedVersion = Number(options.supportedVersion || 1);
  const schema = String(meta.schema || envelope.schema || "").trim();
  const schemaVersion = Number(meta.schema_version ?? meta.schemaVersion ?? envelope.schema_version ?? envelope.schemaVersion ?? 0);
  const minSupportedRaw = meta.min_supported_schema_version ?? meta.minSupportedSchemaVersion ?? envelope.min_supported_schema_version ?? envelope.minSupportedSchemaVersion;
  const minSupported = Number(minSupportedRaw ?? 0);
  if (schema && expectedSchema && schema !== expectedSchema) return false;
  if (Number.isFinite(minSupported) && minSupported > supportedVersion) return false;
  return !(minSupportedRaw == null && Number.isFinite(schemaVersion) && schemaVersion > supportedVersion);
}

export function streamWorkflowRunEvents(runID, options = {}, onEvent = () => {}) {
  const eventsURL = options.eventsURL || workflowRunEventsURL(runID);
  const path = appendQuery(eventsURL, { since: options.since || 0 });
  return fetch(path, {
    method: "GET",
    headers: eventStreamHeaders(options),
    signal: options.signal
  }).then(async response => {
    if (!response.ok || !response.body) {
      const message = await response.text();
      throw new Error(message || response.statusText);
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split("\n\n");
      buffer = frames.pop() || "";
      for (const frame of frames) {
        const event = parseSSEFrame(frame);
        if (event) await onEvent(event);
      }
    }
    if (buffer.trim()) {
      const event = parseSSEFrame(buffer);
      if (event) await onEvent(event);
    }
  });
}

export function streamAgentRunEvents(runID, options = {}, onEvent = () => {}) {
  const eventsURL = options.eventsURL || agentRunEventsURL(runID);
  const path = appendQuery(eventsURL, { since: options.since || 0, include_content: true });
  return fetch(path, {
    method: "GET",
    headers: eventStreamHeaders(options),
    signal: options.signal
  }).then(async response => {
    if (!response.ok || !response.body) {
      const message = await response.text();
      throw new Error(message || response.statusText);
    }
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split("\n\n");
      buffer = frames.pop() || "";
      for (const frame of frames) {
        const event = parseSSEFrame(frame);
        if (event) await onEvent(event);
      }
    }
    if (buffer.trim()) {
      const event = parseSSEFrame(buffer);
      if (event) await onEvent(event);
    }
  });
}

export function buildQuery(params = {}) {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params || {})) {
    if (value == null) continue;
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item == null) continue;
        const normalized = `${item}`.trim();
        if (normalized) search.append(key, normalized);
      }
      continue;
    }
    if (typeof value === "number") {
      if (Number.isFinite(value)) search.set(key, String(value));
      continue;
    }
    if (typeof value === "boolean") {
      search.set(key, value ? "true" : "false");
      continue;
    }
    const normalized = `${value}`.trim();
    if (normalized) search.set(key, normalized);
  }
  const query = search.toString();
  return query ? `?${query}` : "";
}

function appendQuery(path, params = {}) {
  const query = buildQuery(params);
  if (!query) return path;
  return `${path}${path.includes("?") ? "&" : "?"}${query.slice(1)}`;
}

function eventStreamHeaders(options = {}) {
  const since = Number(options.since || 0);
  return Number.isFinite(since) && since > 0
    ? { "Last-Event-ID": String(Math.trunc(since)) }
    : {};
}

function runIDValue(runOrID) {
  if (runOrID && typeof runOrID === "object") return String(runOrID.id || runOrID.run_id || "").trim();
  return String(runOrID || "").trim();
}

function runURLValue(runOrID, keys = []) {
  if (!runOrID || typeof runOrID !== "object") return "";
  for (const key of keys) {
    const value = String(runOrID[key] || "").trim();
    if (value) return value;
  }
  return "";
}

function runEndpoint(runOrID, keys, fallbackPath, params = {}) {
  const id = runIDValue(runOrID);
  const path = runURLValue(runOrID, keys) || (id ? fallbackPath(id) : "");
  if (!path) throw new Error("missing run id");
  return appendQuery(path, params);
}

export function fetchCollaborationMessages(filters = {}) {
  return request(`/api/collaboration/messages${buildQuery(filters)}`);
}

export function fetchCollaborationBlackboard(filters = {}) {
  return request(`/api/collaboration/blackboard${buildQuery(filters)}`);
}

export function createCollaborationMessage(body = {}) {
  return postJSON("/api/collaboration/messages", body);
}

export function createCollaborationBlackboardEntry(body = {}) {
  return postJSON("/api/collaboration/blackboard", body);
}

export function updateCollaborationBlackboardAction(id, action, body = {}) {
  return postJSON(`/api/collaboration/blackboard/${encodeURIComponent(id)}/${encodeURIComponent(action)}`, body);
}

export function fetchTeamState(filters = {}) {
  return request(`/api/team-state${buildQuery(filters)}`);
}

export function fetchMemoryDashboard(filters = {}) {
  return request(`/api/memory${buildQuery(filters)}`);
}

export function fetchMemoryProject() {
  return request("/api/memory/project");
}

export function updateMemoryProject(content) {
  return request("/api/memory/project", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content: content || "" })
  });
}

export function searchMemory(query, filters = {}) {
  return request(`/api/memory/search${buildQuery({ ...filters, q: query })}`);
}

export function rebuildMemoryIndex() {
  return postJSON("/api/memory/rebuild", {});
}

export function updateMemorySolutionLifecycle(id, action, body = {}) {
  return postJSON(`/api/memory/solutions/${encodeURIComponent(id)}/${encodeURIComponent(action)}`, body);
}

export function compactSessionContext(reason = "") {
  return postJSON("/api/session/compact", { reason });
}

export function fetchArtifactObjects(filters = {}) {
  return request(`/api/artifacts${buildQuery(filters)}`);
}

export function fetchArtifactObject(hashOrRef, options = {}) {
  const ref = String(hashOrRef || "").replace(/^sha256:/, "").trim();
  return request(`/api/artifacts/${encodeURIComponent(ref)}${buildQuery(options)}`);
}

export function fetchWorkflowRuns() {
  return request("/api/workflow-runs?summary=1&limit=100");
}

export function fetchRuns(filters = {}) {
  return request(`/api/runs${buildQuery(filters)}`);
}

export function fetchWorkflowRun(runID, filters = {}) {
  return request(`/api/workflow-runs/${encodeURIComponent(runID)}${buildQuery(filters)}`);
}

export function fetchWorkflowRunActions(run) {
  return request(runEndpoint(run, ["actions_url", "actionsURL", "actions_path", "actionsPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/actions`, { envelope: true }))
    .then(payload => normalizeRunActionDiscovery(payload).actions);
}

export function fetchWorkflowRunActionDiscovery(run) {
  return request(runEndpoint(run, ["actions_url", "actionsURL", "actions_path", "actionsPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/actions`, { envelope: true }))
    .then(normalizeRunActionDiscovery);
}

export function fetchWorkflowRunReplay(run) {
  return request(runEndpoint(run, ["replay_url", "replayURL", "replay_path", "replayPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/replay`));
}

export function fetchWorkflowRunTimeline(run, filters = {}) {
  return request(runEndpoint(run, ["timeline_url", "timelineURL", "timeline_path", "timelinePath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/timeline`, filters));
}

export function fetchWorkflowRunContext(run) {
  return request(runEndpoint(run, ["context_url", "contextURL", "context_path", "contextPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/context`));
}

export function fetchWorkflowRunDiffs(run, filters = {}) {
  return request(runEndpoint(run, ["diffs_url", "diffsURL", "diffs_path", "diffsPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/diffs`, filters));
}

export function fetchWorkflowRunArtifacts(run, filters = {}) {
  return request(runEndpoint(run, ["artifacts_url", "artifactsURL", "artifacts_path", "artifactsPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/artifacts`, filters));
}

export function fetchRunArtifacts(run, filters = {}) {
  return request(runEndpoint(run, ["artifacts_url", "artifactsURL", "artifacts_path", "artifactsPath"], id => `/api/runs/${encodeURIComponent(id)}/artifacts`, filters));
}

export function fetchWorkflowRunEvidence(run, filters = {}) {
  return request(runEndpoint(run, ["evidence_url", "evidenceURL", "evidence_path", "evidencePath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/evidence`, filters));
}

export function fetchWorkflowRunStages(run, filters = {}) {
  return request(runEndpoint(run, ["stages_url", "stagesURL", "stages_path", "stagesPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/stages`, filters));
}

export function workflowRunEventsURL(run) {
  return runEndpoint(run, ["events_url", "eventsURL", "events_path", "eventsPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/events/stream`);
}

export function workflowRunExportURL(run, filters = {}) {
  return runEndpoint(run, ["export_url", "exportURL", "export_path", "exportPath"], id => `/api/workflow-runs/${encodeURIComponent(id)}/export`, filters);
}

export function fetchAgentRuns() {
  return request("/api/agent-runs?summary=1&limit=100");
}

export function fetchAgentRun(runID, filters = {}) {
  return request(`/api/agent-runs/${encodeURIComponent(runID)}${buildQuery(filters)}`);
}

export function fetchAgentRunReplay(run) {
  return request(runEndpoint(run, ["replay_url", "replayURL", "replay_path", "replayPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/replay`));
}

export function fetchAgentRunTimeline(run, filters = {}) {
  return request(runEndpoint(run, ["timeline_url", "timelineURL", "timeline_path", "timelinePath"], id => `/api/agent-runs/${encodeURIComponent(id)}/timeline`, filters));
}

export function fetchAgentRunContext(run) {
  return request(runEndpoint(run, ["context_url", "contextURL", "context_path", "contextPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/context`));
}

export function fetchAgentRunArtifacts(run, filters = {}) {
  return request(runEndpoint(run, ["artifacts_url", "artifactsURL", "artifacts_path", "artifactsPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/artifacts`, filters));
}

export function fetchAgentRunActions(run) {
  return request(runEndpoint(run, ["actions_url", "actionsURL", "actions_path", "actionsPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/actions`, { envelope: true }))
    .then(payload => normalizeRunActionDiscovery(payload).actions);
}

export function fetchAgentRunActionDiscovery(run) {
  return request(runEndpoint(run, ["actions_url", "actionsURL", "actions_path", "actionsPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/actions`, { envelope: true }))
    .then(normalizeRunActionDiscovery);
}

export function fetchAgentRunDiffs(run, filters = {}) {
  return request(runEndpoint(run, ["diffs_url", "diffsURL", "diffs_path", "diffsPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/diffs`, filters));
}

export function agentRunEventsURL(run) {
  return runEndpoint(run, ["events_url", "eventsURL", "events_path", "eventsPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/events/stream`);
}

export function agentRunExportURL(run, filters = {}) {
  return runEndpoint(run, ["export_url", "exportURL", "export_path", "exportPath"], id => `/api/agent-runs/${encodeURIComponent(id)}/export`, filters);
}

export function normalizeRunActionDiscovery(payload) {
  const envelope = payload && typeof payload === "object" && !Array.isArray(payload) ? payload : {};
  const rawActions = Array.isArray(payload)
    ? payload
    : Array.isArray(envelope.actions)
      ? envelope.actions
      : Array.isArray(envelope.items)
        ? envelope.items
        : Array.isArray(envelope.value)
          ? envelope.value
          : [];
  const recommended = String(envelope.recommended_action || envelope.recommendedAction || envelope.recommended || "").trim();
  const eventsPath = String(envelope.events_path || envelope.eventsPath || envelope.events_url || envelope.eventsURL || "").trim();
  const actionsPath = String(envelope.actions_path || envelope.actionsPath || envelope.actions_url || envelope.actionsURL || "").trim();
  const timelinePath = String(envelope.timeline_path || envelope.timelinePath || envelope.timeline_url || envelope.timelineURL || "").trim();
  const replayPath = String(envelope.replay_path || envelope.replayPath || envelope.replay_url || envelope.replayURL || "").trim();
  const diffsPath = String(envelope.diffs_path || envelope.diffsPath || envelope.diffs_url || envelope.diffsURL || "").trim();
  const exportPath = String(envelope.export_path || envelope.exportPath || envelope.export_url || envelope.exportURL || "").trim();
  const cancelPath = String(envelope.cancel_path || envelope.cancelPath || envelope.cancel_url || envelope.cancelURL || "").trim();
  const actions = rawActions
    .filter(action => action && typeof action === "object")
    .map(action => ({
      ...action,
      events_path: action.events_path || action.eventsPath || eventsPath || undefined,
      actions_path: action.actions_path || action.actionsPath || actionsPath || undefined,
      timeline_path: action.timeline_path || action.timelinePath || timelinePath || undefined,
      replay_path: action.replay_path || action.replayPath || replayPath || undefined,
      diffs_path: action.diffs_path || action.diffsPath || diffsPath || undefined,
      export_path: action.export_path || action.exportPath || exportPath || undefined,
      cancel_path: action.cancel_path || action.cancelPath || cancelPath || undefined,
      recommended: action.recommended === true || Boolean(recommended && action.name === recommended)
    }));
  return {
    ...envelope,
    recommended_action: recommended || envelope.recommended_action || "",
    events_path: eventsPath || envelope.events_path || "",
    actions_path: actionsPath || envelope.actions_path || "",
    timeline_path: timelinePath || envelope.timeline_path || "",
    replay_path: replayPath || envelope.replay_path || "",
    diffs_path: diffsPath || envelope.diffs_path || "",
    export_path: exportPath || envelope.export_path || "",
    cancel_path: cancelPath || envelope.cancel_path || "",
    actions
  };
}

export function startAgentRun(input, payload = {}) {
  return postJSON("/api/run", {
    ...payload,
    input,
    background: true
  });
}

export function startWorkflowRun(name, payload = {}) {
  return postJSON(`/api/workflows/${encodeURIComponent(name)}`, payload);
}

export function submitWorkflowRunInput(runID, inputs, options = {}) {
  return postJSON(`/api/workflow-runs/${encodeURIComponent(runID)}/input`, {
    inputs: inputs || {},
    background: Boolean(options.background)
  });
}

function parseSSEFrame(frame) {
  const lines = frame.split(/\r?\n/);
  const eventType = (lines.find(line => line.startsWith("event:")) || "").slice(6).trim();
  const idLine = lines.find(line => line.startsWith("id:"));
  const retryLine = lines.find(line => line.startsWith("retry:"));
  const retry = retryLine ? Number(retryLine.slice(6).trim()) : 0;
  const data = lines
    .filter(line => line.startsWith("data:"))
    .map(line => line.slice(5).trim())
    .join("\n");
  if (!data) {
    return Number.isFinite(retry) && retry > 0
      ? { type: "sse_retry", sse_event: eventType || "retry", sse_retry: retry }
      : null;
  }
  try {
    const payload = JSON.parse(data);
    payload.type = payload.type || eventType;
    payload.sse_event = eventType;
    if (idLine) payload.sse_id = idLine.slice(3).trim();
    if (Number.isFinite(retry) && retry > 0) payload.sse_retry = retry;
    return payload;
  } catch {
    const payload = { type: eventType || "text", content: data };
    if (eventType) payload.sse_event = eventType;
    if (idLine) payload.sse_id = idLine.slice(3).trim();
    if (Number.isFinite(retry) && retry > 0) payload.sse_retry = retry;
    return payload;
  }
}

export function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

export function renderSafeMarkdown(value) {
  const raw = String(value || "");
  const segments = [];
  const fencePattern = /```([\w-]*)\s*\r?\n([\s\S]*?)```/g;
  let lastIndex = 0;
  let match;
  while ((match = fencePattern.exec(raw)) !== null) {
    segments.push({ type: "text", value: raw.slice(lastIndex, match.index) });
    segments.push({ type: "code", language: match[1], value: match[2] });
    lastIndex = match.index + match[0].length;
  }
  const tail = raw.slice(lastIndex);
  const openFence = tail.match(/```([\w-]*)\s*\r?\n([\s\S]*)$/);
  if (openFence) {
    const fenceIndex = openFence.index ?? tail.indexOf("```");
    segments.push({ type: "text", value: tail.slice(0, fenceIndex) });
    segments.push({ type: "code", language: openFence[1], value: openFence[2] });
  } else {
    segments.push({ type: "text", value: tail });
  }
  return segments.map(segment => {
    if (segment.type === "code") {
      const language = segment.language ? `<span>${escapeHTML(segment.language)}</span>` : "";
      return `<pre>${language}<code>${escapeHTML(segment.value).trim()}</code></pre>`;
    }
    return renderSafeMarkdownText(segment.value);
  }).join("");
}

function renderSafeMarkdownText(value) {
  const lines = escapeHTML(value).replace(/\r\n/g, "\n").split("\n");
  const out = [];
  let listType = "";
  let paragraph = [];
  const closeList = () => {
    if (!listType) return;
    out.push(`</${listType}>`);
    listType = "";
  };
  const closeParagraph = () => {
    if (!paragraph.length) return;
    out.push(`<p>${inlineSafeMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  };
  const closeFlow = () => {
    closeParagraph();
    closeList();
  };
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trim();
    if (!trimmed) {
      closeFlow();
      continue;
    }
    if (isMarkdownTableHeader(lines, index)) {
      closeFlow();
      const rows = [trimmed];
      index += 2;
      while (index < lines.length && isMarkdownTableRow(lines[index])) {
        rows.push(lines[index].trim());
        index += 1;
      }
      index -= 1;
      out.push(renderSafeMarkdownTable(rows));
      continue;
    }
    const unordered = trimmed.match(/^[-*]\s+(.+)$/);
    if (unordered) {
      closeParagraph();
      if (listType !== "ul") {
        closeList();
        out.push("<ul>");
        listType = "ul";
      }
      out.push(`<li>${inlineSafeMarkdown(unordered[1])}</li>`);
      continue;
    }
    const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
    if (ordered) {
      closeParagraph();
      if (listType !== "ol") {
        closeList();
        out.push("<ol>");
        listType = "ol";
      }
      out.push(`<li>${inlineSafeMarkdown(ordered[1])}</li>`);
      continue;
    }
    const heading = trimmed.match(/^(#{1,4})\s+(.+)$/);
    if (heading) {
      closeFlow();
      out.push(`<h${heading[1].length}>${inlineSafeMarkdown(heading[2])}</h${heading[1].length}>`);
      continue;
    }
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      closeFlow();
      out.push("<hr>");
      continue;
    }
    if (trimmed.startsWith("&gt; ")) {
      closeFlow();
      out.push(`<blockquote>${inlineSafeMarkdown(trimmed.slice(5))}</blockquote>`);
      continue;
    }
    closeList();
    paragraph.push(trimmed);
  }
  closeFlow();
  return out.join("");
}

function isMarkdownTableHeader(lines, index) {
  return isMarkdownTableRow(lines[index]) && isMarkdownTableDivider(lines[index + 1] || "");
}

function isMarkdownTableRow(line) {
  const trimmed = String(line || "").trim();
  return trimmed.includes("|") && !/^```/.test(trimmed);
}

function isMarkdownTableDivider(line) {
  const cells = markdownTableCells(line);
  return cells.length > 1 && cells.every(cell => /^:?-{3,}:?$/.test(cell.trim()));
}

function markdownTableCells(line) {
  const trimmed = String(line || "").trim().replace(/^\|/, "").replace(/\|$/, "");
  return trimmed.split("|").map(cell => cell.trim());
}

function renderSafeMarkdownTable(rows) {
  const header = markdownTableCells(rows[0] || "");
  const body = rows.slice(1).map(markdownTableCells);
  const head = `<thead><tr>${header.map(cell => `<th>${inlineSafeMarkdown(cell)}</th>`).join("")}</tr></thead>`;
  const rowsHTML = body.map(row => `<tr>${header.map((_, index) => `<td>${inlineSafeMarkdown(row[index] || "")}</td>`).join("")}</tr>`).join("");
  return `<table>${head}<tbody>${rowsHTML}</tbody></table>`;
}

function inlineSafeMarkdown(value) {
  return value
    .replace(/`([^`\n]+)`/g, "<code>$1</code>")
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, `<a href="$2" target="_blank" rel="noreferrer">$1</a>`)
    .replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>")
    .replace(/__([^_\n]+)__/g, "<strong>$1</strong>")
    .replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
}

export function formatList(values) {
  if (!values || values.length === 0) return "-";
  return values.join(", ");
}
