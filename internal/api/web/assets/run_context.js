const skillScriptToolNames = new Set(["skill_runner/run_script", "run_script"]);
const skillScriptFields = [
  "toolName",
  "stage",
  "skill",
  "script",
  "path",
  "runtime",
  "declaredRuntime",
  "outputKind",
  "workspaceMount",
  "network",
  "approval",
  "timeout",
  "durationMS",
  "exitCode",
  "timedOut",
  "outputJSONValid",
  "sandboxNote",
  "argsPreview"
];

export function isSkillScriptToolName(name) {
  const value = String(name || "").trim().toLowerCase();
  if (!value) return false;
  if (skillScriptToolNames.has(value)) return true;
  return value.endsWith("/run_script") && value.includes("skill");
}

export function extractSkillScriptContextFromApproval(approval = {}) {
  let info = mergeSkillScriptInfo(null, extractSkillScriptContextFromRun(approval.run || {}));
  const sources = [
    approval.raw,
    ...(Array.isArray(approval.relatedApprovals) ? approval.relatedApprovals : [])
  ].filter(Boolean);
  sources.forEach(item => {
    const toolName = item.tool_name || item.tool || item.name;
    const sourceInfo = isSkillScriptToolName(toolName) ? { toolName } : {};
    info = mergeSkillScriptInfo(info, sourceInfo);
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(item.arguments));
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(item.arguments_summary));
  });
  if (!hasSkillScriptContext(info)) return null;
  return normalizeSkillScriptContext(info);
}

export function extractSkillScriptContextFromRun(run = {}) {
  let info = {};
  const pendingTool = run.pending_tool_name || run.pending_tool?.name || run.tool_name || "";
  if (isSkillScriptToolName(pendingTool)) {
    info = mergeSkillScriptInfo(info, { toolName: pendingTool, stage: run.next_stage || run.stage || "" });
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(run.pending_arguments));
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(run.pending_arguments_summary));
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(run.pending_tool?.arguments));
    info = mergeSkillScriptInfo(info, parseSkillScriptSource(run.pending_tool?.arguments_summary));
  }
  const events = Array.isArray(run.events) ? run.events : [];
  for (const event of events) {
    const toolName = event?.tool_name || event?.tool || "";
    const eventInfo = parseSkillScriptSource(event?.content);
    const eventArgs = parseSkillScriptSource(event?.arguments_summary);
    if (!isSkillScriptToolName(toolName) && !hasSkillScriptContext(eventInfo) && !hasSkillScriptContext(eventArgs)) {
      continue;
    }
    info = mergeSkillScriptInfo(info, {
      toolName: toolName || info.toolName,
      stage: event?.stage || event?.task_stage || info.stage || ""
    });
    info = mergeSkillScriptInfo(info, eventArgs);
    info = mergeSkillScriptInfo(info, eventInfo);
  }
  if (!hasSkillScriptContext(info)) return null;
  return normalizeSkillScriptContext(info);
}

export function skillScriptContextSignature(info = {}) {
  if (!hasSkillScriptContext(info)) return "";
  return skillScriptFields.map(field => String(info[field] ?? "")).join("|");
}

function hasSkillScriptContext(info = {}) {
  if (!info || typeof info !== "object") return false;
  return Boolean(
    info.toolName && isSkillScriptToolName(info.toolName) ||
    info.skill ||
    info.script ||
    info.path ||
    info.runtime ||
    info.outputKind
  );
}

function normalizeSkillScriptContext(info = {}) {
  return {
    toolName: stringValue(info.toolName || "skill_runner/run_script"),
    stage: stringValue(info.stage),
    skill: stringValue(info.skill),
    script: stringValue(info.script),
    path: stringValue(info.path),
    runtime: stringValue(info.runtime || info.declaredRuntime),
    declaredRuntime: stringValue(info.declaredRuntime),
    outputKind: stringValue(info.outputKind),
    workspaceMount: stringValue(info.workspaceMount),
    network: stringValue(info.network),
    approval: stringValue(info.approval),
    timeout: stringValue(info.timeout),
    durationMS: stringValue(info.durationMS),
    exitCode: stringValue(info.exitCode),
    timedOut: boolString(info.timedOut),
    outputJSONValid: boolString(info.outputJSONValid),
    sandboxNote: stringValue(info.sandboxNote),
    argsPreview: stringValue(info.argsPreview)
  };
}

function parseSkillScriptSource(value) {
  if (value == null || value === "") return null;
  if (typeof value === "object" && !Array.isArray(value)) {
    return normalizeSkillScriptObject(value);
  }
  const text = String(value || "").trim();
  if (!text) return null;
  const parsed = parseJSONText(text);
  if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
    return normalizeSkillScriptObject(parsed);
  }
  return parseSkillScriptSummary(text);
}

function parseJSONText(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function normalizeSkillScriptObject(value = {}) {
  const args = value.args && typeof value.args === "object" ? value.args : null;
  return {
    toolName: stringValue(value.tool_name || value.tool || value.name),
    stage: stringValue(value.stage || value.task_stage),
    skill: stringValue(value.skill || value.skill_name),
    script: stringValue(value.script || value.script_name),
    path: stringValue(value.path || value.script_path),
    runtime: stringValue(value.runtime),
    declaredRuntime: stringValue(value.declared_runtime),
    outputKind: stringValue(value.declared_output || value.output_type || value.output_kind),
    workspaceMount: stringValue(value.workspace_mount),
    network: stringValue(value.declared_network || value.network),
    approval: stringValue(value.approval),
    timeout: stringValue(value.timeout),
    durationMS: stringValue(value.duration_ms),
    exitCode: stringValue(value.exit_code),
    timedOut: value.timed_out,
    outputJSONValid: value.output_json_valid,
    sandboxNote: stringValue(value.sandbox_note),
    argsPreview: args ? compactJSON(args, 180) : ""
  };
}

function parseSkillScriptSummary(text) {
  const fields = {};
  const keyMap = {
    skill: "skill",
    skill_name: "skill",
    script: "script",
    script_name: "script",
    path: "path",
    runtime: "runtime",
    declared_runtime: "declaredRuntime",
    declared_output: "outputKind",
    output_type: "outputKind",
    output_kind: "outputKind",
    workspace_mount: "workspaceMount",
    declared_network: "network",
    network: "network",
    approval: "approval",
    timeout: "timeout",
    duration_ms: "durationMS",
    exit_code: "exitCode",
    timed_out: "timedOut",
    output_json_valid: "outputJSONValid"
  };
  Object.keys(keyMap).forEach(key => {
    const match = text.match(new RegExp(`(?:^|\\s)${escapeRegExp(key)}=([^\\s]+)`, "i"));
    if (match?.[1]) fields[keyMap[key]] = cleanSummaryValue(match[1]);
  });
  if (/\bskill_runner\/run_script\b/i.test(text)) fields.toolName = "skill_runner/run_script";
  return fields;
}

function mergeSkillScriptInfo(base, next) {
  const out = { ...(base || {}) };
  if (!next || typeof next !== "object") return out;
  skillScriptFields.forEach(field => {
    const value = next[field];
    if (value == null || value === "") return;
    out[field] = value;
  });
  return out;
}

function cleanSummaryValue(value) {
  return String(value || "").trim().replace(/[;,]+$/, "");
}

function stringValue(value) {
  if (value == null) return "";
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return "";
}

function boolString(value) {
  if (value === true || value === "true") return "true";
  if (value === false || value === "false") return "false";
  return "";
}

function compactJSON(value, limit) {
  try {
    const text = JSON.stringify(value);
    return text.length > limit ? `${text.slice(0, limit - 3)}...` : text;
  } catch {
    return "";
  }
}

function escapeRegExp(value) {
  return String(value || "").replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
