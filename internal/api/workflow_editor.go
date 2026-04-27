package api

import "net/http"

func (s *Server) handleWorkflowEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(workflowEditorHTML))
}

const workflowEditorHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>GoFlow Workflows</title>
  <style>
    :root { color-scheme: dark; --bg:#101418; --panel:#161c22; --ink:#e6edf3; --muted:#8b949e; --line:#30363d; --accent:#2f81f7; --ok:#3fb950; --warn:#d29922; --bad:#f85149; }
    * { box-sizing:border-box; }
    body { margin:0; font:14px/1.4 ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif; background:var(--bg); color:var(--ink); }
    header { height:54px; display:flex; align-items:center; justify-content:space-between; padding:0 18px; border-bottom:1px solid var(--line); background:#0d1117; }
    header strong { font-size:16px; }
    button, input, textarea, select { font:inherit; color:var(--ink); background:#0d1117; border:1px solid var(--line); border-radius:6px; }
    button { padding:7px 10px; cursor:pointer; }
    button.primary { background:var(--accent); border-color:var(--accent); color:white; }
    button.danger { border-color:#7d2b31; color:#ffb3b8; }
    input, textarea, select { width:100%; padding:7px 8px; }
    textarea { min-height:72px; resize:vertical; }
    .app { display:grid; grid-template-columns:260px 1fr 320px; height:calc(100vh - 54px); }
    aside, .props { border-right:1px solid var(--line); background:var(--panel); overflow:auto; }
    .props { border-left:1px solid var(--line); border-right:0; padding:14px; }
    aside { padding:12px; }
    .list { display:flex; flex-direction:column; gap:8px; margin-top:12px; }
    .item { text-align:left; padding:10px; border:1px solid var(--line); background:#0d1117; border-radius:6px; }
    .item.active { border-color:var(--accent); }
    .item small { display:block; color:var(--muted); margin-top:3px; }
    .toolbar { display:flex; gap:8px; flex-wrap:wrap; }
    .canvas-wrap { position:relative; overflow:hidden; background:
      linear-gradient(var(--line) 1px, transparent 1px),
      linear-gradient(90deg, var(--line) 1px, transparent 1px);
      background-size:24px 24px; }
    svg { position:absolute; inset:0; width:100%; height:100%; pointer-events:none; }
    .node { position:absolute; width:190px; min-height:92px; padding:10px; border:1px solid #44515f; border-radius:8px; background:#111923; box-shadow:0 10px 28px rgba(0,0,0,.24); cursor:grab; user-select:none; }
    .node.selected { border-color:var(--accent); box-shadow:0 0 0 2px rgba(47,129,247,.25); }
    .node .name { font-weight:700; margin-bottom:5px; }
    .node .meta { color:var(--muted); font-size:12px; }
    .node .approval { color:var(--warn); margin-top:7px; font-size:12px; }
    .form { display:grid; gap:10px; }
    label span { display:block; color:var(--muted); font-size:12px; margin:0 0 4px; }
    .status { color:var(--muted); white-space:pre-wrap; margin-top:10px; min-height:20px; }
    .hint { color:var(--muted); font-size:12px; margin-top:10px; }
    .row { display:grid; grid-template-columns:1fr 1fr; gap:8px; }
    .edge { stroke:#58a6ff; stroke-width:2; marker-end:url(#arrow); opacity:.9; }
  </style>
</head>
<body>
<header>
  <strong>GoFlow Workflow Editor</strong>
  <div class="toolbar">
    <button id="refresh">Refresh</button>
    <button id="new">New</button>
    <button id="save" class="primary">Save</button>
    <button id="run">Run</button>
    <button id="delete" class="danger">Delete</button>
  </div>
</header>
<main class="app">
  <aside>
    <div class="toolbar"><button id="reload">Reload workflows</button></div>
    <div id="workflowList" class="list"></div>
    <div class="hint">Custom workflows are persisted under runtime-home <code>workflows/&lt;name&gt;/workflow.yaml</code>.</div>
  </aside>
  <section class="canvas-wrap" id="canvas">
    <svg id="edges"><defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto"><path d="M0,0 L0,6 L7,3 z" fill="#58a6ff"/></marker></defs></svg>
  </section>
  <section class="props">
    <div class="form">
      <label><span>Workflow name</span><input id="wfName" placeholder="release-check"></label>
      <label><span>Description</span><textarea id="wfDescription" placeholder="What this workflow is for"></textarea></label>
      <div class="toolbar">
        <button id="addStage">Add stage</button>
        <button id="removeStage" class="danger">Remove stage</button>
      </div>
      <hr style="width:100%;border:0;border-top:1px solid var(--line)">
      <label><span>Stage name</span><input id="stageName"></label>
      <div class="row">
        <label><span>Agent</span><select id="stageAgent"></select></label>
        <label><span>Skill</span><select id="stageSkill"></select></label>
      </div>
      <label><span>Next stages, comma separated</span><input id="stageNext" placeholder="implement,audit"></label>
      <label><span>Next strategy</span><input id="stageNextStrategy" placeholder="select"></label>
      <label><span><input id="stageApproval" type="checkbox" style="width:auto"> Require approval before this stage</span></label>
      <label><span>Run request</span><textarea id="runInput" placeholder="improve CLI diff output"></textarea></label>
      <div id="status" class="status"></div>
    </div>
  </section>
</main>
<script>
const state = { workflows: [], graph: { name: "", description: "", stages: [] }, selected: -1, options: { agents: [], skills: [] }, dragging: null };
const $ = (id) => document.getElementById(id);
const api = async (url, opts={}) => {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
};
const say = (msg) => $("status").textContent = msg || "";
const slug = (value) => value.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "");
async function loadOptions() {
  state.options = await api("/api/workflow-options");
  fillSelect("stageAgent", state.options.agents.map(a => a.name), "planner");
  fillSelect("stageSkill", state.options.skills.map(s => s.name), "execution-plan");
}
function fillSelect(id, values, fallback) {
  const select = $(id);
  const current = select.value || fallback;
  select.innerHTML = "";
  for (const value of values.length ? values : [fallback]) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = value;
    select.appendChild(option);
  }
  select.value = values.includes(current) ? current : (values[0] || fallback);
}
async function loadWorkflows() {
  state.workflows = await api("/api/workflow-graphs");
  renderWorkflowList();
}
function renderWorkflowList() {
  const list = $("workflowList");
  list.innerHTML = "";
  for (const wf of state.workflows) {
    const item = document.createElement("button");
    item.className = "item" + (wf.name === state.graph.name ? " active" : "");
    item.innerHTML = "<strong>" + wf.name + "</strong><small>" + wf.source + (wf.valid ? "" : " - invalid") + (wf.description ? " - " + wf.description : "") + "</small>";
    item.onclick = () => openWorkflow(wf.name);
    list.appendChild(item);
  }
}
async function openWorkflow(name) {
  state.graph = await api("/api/workflow-graphs/" + encodeURIComponent(name));
  state.selected = state.graph.stages.length ? 0 : -1;
  normalizePositions();
  syncForm();
  render();
  say("");
}
function normalizePositions() {
  state.graph.stages.forEach((stage, i) => {
    if (!stage.position) stage.position = {};
    if (!Number.isFinite(stage.position.x)) stage.position.x = 80 + i * 240;
    if (!Number.isFinite(stage.position.y)) stage.position.y = 120;
  });
}
function syncGraphFromForm() {
  state.graph.name = slug($("wfName").value);
  state.graph.description = $("wfDescription").value.trim();
  const stage = state.graph.stages[state.selected];
  if (!stage) return;
  stage.name = slug($("stageName").value);
  stage.agent = $("stageAgent").value;
  stage.skill = $("stageSkill").value;
  stage.approval = $("stageApproval").checked;
  stage.next_strategy = $("stageNextStrategy").value.trim();
  stage.next = $("stageNext").value.split(",").map(v => slug(v)).filter(Boolean);
}
function syncForm() {
  $("wfName").value = state.graph.name || "";
  $("wfDescription").value = state.graph.description || "";
  const stage = state.graph.stages[state.selected] || {};
  $("stageName").value = stage.name || "";
  $("stageAgent").value = stage.agent || $("stageAgent").value;
  $("stageSkill").value = stage.skill || $("stageSkill").value;
  $("stageApproval").checked = !!stage.approval;
  $("stageNextStrategy").value = stage.next_strategy || "";
  $("stageNext").value = (stage.next || []).join(", ");
}
function render() {
  renderWorkflowList();
  const canvas = $("canvas");
  canvas.querySelectorAll(".node").forEach(n => n.remove());
  for (const [i, stage] of state.graph.stages.entries()) {
    const node = document.createElement("div");
    node.className = "node" + (i === state.selected ? " selected" : "");
    node.style.left = (stage.position?.x || 80) + "px";
    node.style.top = (stage.position?.y || 120) + "px";
    node.innerHTML = "<div class=\"name\">" + (stage.name || "stage") + "</div><div class=\"meta\">" + (stage.agent || "-") + " / " + (stage.skill || "-") + "</div>" + (stage.approval ? "<div class=\"approval\">approval required</div>" : "");
    node.onmousedown = (event) => startDrag(event, i);
    node.onclick = () => { state.selected = i; syncForm(); render(); };
    canvas.appendChild(node);
  }
  drawEdges();
}
function drawEdges() {
  const svg = $("edges");
  svg.querySelectorAll("line").forEach(line => line.remove());
  const byName = new Map(state.graph.stages.map((s, i) => [s.name, {s, i}]));
  for (const stage of state.graph.stages) {
    for (const next of stage.next || []) {
      const target = byName.get(next);
      if (!target) continue;
      const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
      line.setAttribute("class", "edge");
      line.setAttribute("x1", (stage.position?.x || 80) + 190);
      line.setAttribute("y1", (stage.position?.y || 120) + 46);
      line.setAttribute("x2", (target.s.position?.x || 80));
      line.setAttribute("y2", (target.s.position?.y || 120) + 46);
      svg.appendChild(line);
    }
  }
}
function startDrag(event, index) {
  state.selected = index;
  syncForm();
  const stage = state.graph.stages[index];
  state.dragging = { index, dx: event.clientX - stage.position.x, dy: event.clientY - stage.position.y };
  render();
}
window.onmousemove = (event) => {
  if (!state.dragging) return;
  const stage = state.graph.stages[state.dragging.index];
  stage.position.x = Math.max(0, event.clientX - state.dragging.dx);
  stage.position.y = Math.max(0, event.clientY - state.dragging.dy - 54);
  render();
};
window.onmouseup = () => state.dragging = null;
["wfName","wfDescription","stageName","stageAgent","stageSkill","stageApproval","stageNext","stageNextStrategy"].forEach(id => {
  $(id).addEventListener("input", () => { syncGraphFromForm(); render(); });
  $(id).addEventListener("change", () => { syncGraphFromForm(); render(); });
});
$("addStage").onclick = () => {
  syncGraphFromForm();
  const index = state.graph.stages.length + 1;
  state.graph.stages.push({ name: "stage-" + index, agent: "planner", skill: "execution-plan", approval: false, next: [], position: { x: 80 + (index - 1) * 240, y: 120 } });
  state.selected = state.graph.stages.length - 1;
  syncForm(); render();
};
$("removeStage").onclick = () => {
  const stage = state.graph.stages[state.selected];
  if (!stage) return;
  state.graph.stages.splice(state.selected, 1);
  for (const other of state.graph.stages) other.next = (other.next || []).filter(n => n !== stage.name);
  state.selected = Math.min(state.selected, state.graph.stages.length - 1);
  syncForm(); render();
};
$("new").onclick = () => {
  state.graph = { name: "new-workflow", description: "", stages: [
    { name:"plan", agent:"planner", skill:"execution-plan", next:["implement"], position:{x:80,y:120} },
    { name:"implement", agent:"fixer", skill:"code-writing", approval:true, next:["audit"], position:{x:340,y:120} },
    { name:"audit", agent:"auditor", skill:"code-audit", position:{x:600,y:120} }
  ] };
  state.selected = 0; syncForm(); render(); say("Edit and save the new workflow.");
};
$("save").onclick = async () => {
  try {
    syncGraphFromForm();
    if (!state.graph.name) throw new Error("workflow name is required");
    await api("/api/workflow-graphs/" + encodeURIComponent(state.graph.name), { method:"PUT", headers:{ "Content-Type":"application/json" }, body: JSON.stringify(state.graph) });
    await loadWorkflows();
    say("Saved " + state.graph.name);
  } catch (err) { say("Save failed: " + err.message); }
};
$("delete").onclick = async () => {
  try {
    if (!state.graph.name || !confirm("Delete workflow " + state.graph.name + "?")) return;
    await fetch("/api/workflow-graphs/" + encodeURIComponent(state.graph.name), { method:"DELETE" });
    state.graph = { name:"", description:"", stages:[] };
    state.selected = -1;
    await loadWorkflows(); syncForm(); render(); say("Deleted.");
  } catch (err) { say("Delete failed: " + err.message); }
};
$("run").onclick = async () => {
  try {
    const input = $("runInput").value.trim();
    if (!state.graph.name || !input) throw new Error("workflow and run request are required");
    say("Running workflow...");
    const result = await api("/api/workflows/" + encodeURIComponent(state.graph.name), { method:"POST", headers:{ "Content-Type":"application/json" }, body: JSON.stringify({input}) });
    say(JSON.stringify(result, null, 2));
  } catch (err) { say("Run failed: " + err.message); }
};
$("refresh").onclick = $("reload").onclick = async () => { await loadWorkflows(); say("Reloaded."); };
(async () => { await loadOptions(); await loadWorkflows(); if (state.workflows[0]) await openWorkflow(state.workflows[0].name); })().catch(err => say(err.message));
</script>
</body>
</html>`
