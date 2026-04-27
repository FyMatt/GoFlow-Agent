import { escapeHTML, request, streamRun } from "../api.js";
import { t } from "../i18n.js";

const executableTypes = new Set(["agent", "skill", "tool", "custom"]);
const state = {
  workflows: [],
  options: { agents: [], skills: [] },
  graph: { name: "new-workflow", description: "", stages: [] },
  selected: -1,
  dragging: null,
  connectSource: ""
};

export async function renderWorkflows(root) {
  root.innerHTML = `
    <div class="studio">
      <aside class="studio-left">
        <div class="panel flat">
          <div class="panel-head">
            <h2>${t("workflow.workflows")}</h2>
            <button id="newGraph">${t("workflow.new")}</button>
          </div>
          <div id="workflowList" class="flow-list"></div>
        </div>
        <div class="panel flat">
          <h2>${t("workflow.library")}</h2>
          <p class="muted">${t("workflow.dropHint")}</p>
          <div class="node-palette">
            ${paletteButton("start", "Start", "Entry point")}
            ${paletteButton("agent", "Agent", "Run an agent stage")}
            ${paletteButton("skill", "Skill", "Apply a skill")}
            ${paletteButton("tool", "Tool", "Tool-focused stage")}
            ${paletteButton("custom", "Custom", "Manual stage")}
            ${paletteButton("end", "End", "Terminal marker")}
          </div>
        </div>
      </aside>

      <section class="workflow-board">
        <div class="board-toolbar">
          <div>
            <input id="graphName" class="title-input" placeholder="workflow-name">
            <input id="graphDescription" class="description-input" placeholder="Describe what this workflow does">
          </div>
          <div class="toolbar">
            <button id="saveGraph" class="primary">${t("workflow.save")}</button>
            <button id="deleteGraph" class="danger">${t("workflow.delete")}</button>
          </div>
        </div>
        <div id="canvas" class="canvas">
          <svg id="edges" aria-hidden="true">
            <defs><marker id="arrow" markerWidth="9" markerHeight="9" refX="8" refY="4" orient="auto"><path d="M0,0 L0,8 L8,4 z" fill="#2563eb"/></marker></defs>
          </svg>
        </div>
      </section>

      <aside class="studio-right">
        <div class="panel flat">
          <h2>${t("workflow.settings")}</h2>
          <div id="stageEmpty" class="muted">${t("workflow.empty")}</div>
          <div id="stageForm" class="stack hidden">
            <label><span>Node type</span><select id="stageNodeType">
              <option value="start">Start</option>
              <option value="agent">Agent</option>
              <option value="skill">Skill</option>
              <option value="tool">Tool</option>
              <option value="custom">Custom</option>
              <option value="end">End</option>
            </select></label>
            <label><span>Stage name</span><input id="stageName"></label>
            <label><span>Agent</span><select id="stageAgent"></select></label>
            <label><span>Skill</span><select id="stageSkill"></select></label>
            <label><span>Tool metadata</span><select id="stageTool"></select></label>
            <label><span>Next stages</span><input id="stageNext" placeholder="audit, publish"></label>
            <label><span>Branch strategy</span><input id="stageNextStrategy" placeholder="select / conditional"></label>
            <label><span>Parameters</span><textarea id="stageParams" class="compact-textarea" placeholder="key=value&#10;timeout=60s"></textarea></label>
            <label class="check"><input id="stageApproval" type="checkbox"> Require approval before stage</label>
            <div class="toolbar">
              <button id="connectStage">${t("workflow.connect")}</button>
              <button id="removeStage" class="danger">${t("workflow.remove")}</button>
            </div>
            <div id="connectHint" class="muted"></div>
          </div>
        </div>
        <div class="panel flat">
          <h2>${t("workflow.runPreview")}</h2>
          <textarea id="runInput" placeholder="Describe the task to run through this workflow"></textarea>
          <button id="runGraph" class="primary" style="margin-top:10px">${t("workflow.run")}</button>
          <pre id="runOutput" class="mini-log"></pre>
        </div>
      </aside>
    </div>`;

  state.options = await request("/api/workflow-options");
  await loadWorkflowList();
  if (!state.graph.stages.length) createPresetGraph();
  bind(root);
  renderAll(root);
}

function paletteButton(template, title, subtitle) {
  return `<button draggable="true" data-template="${template}"><strong>${title}</strong><span>${subtitle}</span></button>`;
}

async function loadWorkflowList() {
  state.workflows = await request("/api/workflow-graphs");
}

function bind(root) {
  const canvas = root.querySelector("#canvas");
  root.querySelector("#newGraph").onclick = () => { createPresetGraph(); renderAll(root); };
  root.querySelector("#saveGraph").onclick = () => saveGraph(root);
  root.querySelector("#deleteGraph").onclick = () => deleteGraph(root);
  root.querySelector("#runGraph").onclick = () => runGraph(root);
  root.querySelector("#graphName").oninput = () => { state.graph.name = slug(root.querySelector("#graphName").value); renderWorkflowList(root); };
  root.querySelector("#graphDescription").oninput = () => { state.graph.description = root.querySelector("#graphDescription").value; };
  root.querySelectorAll(".node-palette button").forEach(button => {
    button.ondragstart = event => event.dataTransfer.setData("text/plain", button.dataset.template);
    button.onclick = () => {
      addStageFromTemplate(button.dataset.template);
      renderAll(root);
    };
  });
  canvas.ondragover = event => event.preventDefault();
  canvas.ondrop = event => {
    event.preventDefault();
    const template = event.dataTransfer.getData("text/plain");
    if (!template) return;
    const rect = canvas.getBoundingClientRect();
    addStageFromTemplate(template, event.clientX - rect.left, event.clientY - rect.top);
    renderAll(root);
  };
  ["stageNodeType", "stageName", "stageAgent", "stageSkill", "stageTool", "stageNext", "stageNextStrategy", "stageParams", "stageApproval"].forEach(id => {
    const input = root.querySelector(`#${id}`);
    input.addEventListener("input", () => syncStageFromForm(root));
    input.addEventListener("change", () => syncStageFromForm(root));
  });
  root.querySelector("#connectStage").onclick = () => {
    const stage = selectedStage();
    if (!stage) return;
    state.connectSource = state.connectSource === stage.name ? "" : stage.name;
    renderStageForm(root);
    renderCanvas(root);
  };
  root.querySelector("#removeStage").onclick = () => {
    removeSelectedStage();
    renderAll(root);
  };
  window.onmousemove = event => {
    if (!state.dragging) return;
    const rect = canvas.getBoundingClientRect();
    const stage = state.graph.stages[state.dragging.index];
    stage.position.x = Math.max(24, event.clientX - rect.left - state.dragging.dx);
    stage.position.y = Math.max(24, event.clientY - rect.top - state.dragging.dy);
    renderCanvas(root);
  };
  window.onmouseup = () => { state.dragging = null; };
}

function createPresetGraph() {
  state.graph = {
    name: "plan-implement-audit",
    description: "Plan a task, implement approved changes, then audit the result.",
    stages: [
      { name: "start", node_type: "start", next: ["plan"], position: { x: 60, y: 165 } },
      { name: "plan", node_type: "agent", agent: "planner", skill: "execution-plan", next: ["implement"], position: { x: 310, y: 150 } },
      { name: "implement", node_type: "agent", agent: "fixer", skill: "code-writing", approval: true, next: ["audit"], position: { x: 590, y: 150 } },
      { name: "audit", node_type: "skill", agent: "auditor", skill: "code-audit", next: ["end"], position: { x: 870, y: 150 } },
      { name: "end", node_type: "end", position: { x: 1150, y: 165 } }
    ]
  };
  state.selected = 0;
  state.connectSource = "";
}

function addStageFromTemplate(template, x, y) {
  const index = state.graph.stages.length + 1;
  const firstAgent = state.options.agents?.[0]?.name || "planner";
  const firstSkill = state.options.skills?.[0]?.name || "execution-plan";
  const firstTool = state.options.tools?.[0] || "";
  const presets = {
    start: { name: uniqueStageName("start"), node_type: "start" },
    agent: { name: uniqueStageName("agent"), node_type: "agent", agent: firstAgent, skill: firstSkill },
    skill: { name: uniqueStageName("skill"), node_type: "skill", agent: "planner", skill: firstSkill },
    tool: { name: uniqueStageName("tool"), node_type: "tool", agent: "fixer", skill: "code-writing", tool: firstTool, approval: true },
    custom: { name: uniqueStageName("stage"), node_type: "custom", agent: firstAgent, skill: firstSkill },
    end: { name: uniqueStageName("end"), node_type: "end" }
  };
  const base = presets[template] || presets.custom;
  state.graph.stages.push({
    ...base,
    next: [],
    params: base.params || {},
    position: { x: Number.isFinite(x) ? x : 120 + index * 70, y: Number.isFinite(y) ? y : 120 + index * 50 }
  });
  state.selected = state.graph.stages.length - 1;
}

function renderAll(root) {
  root.querySelector("#graphName").value = state.graph.name || "";
  root.querySelector("#graphDescription").value = state.graph.description || "";
  renderWorkflowList(root);
  fillSelect(root.querySelector("#stageAgent"), (state.options.agents || []).map(item => item.name));
  fillSelect(root.querySelector("#stageSkill"), (state.options.skills || []).map(item => item.name));
  fillSelect(root.querySelector("#stageTool"), state.options.tools || []);
  renderCanvas(root);
  renderStageForm(root);
}

function renderWorkflowList(root) {
  const list = root.querySelector("#workflowList");
  list.innerHTML = "";
  for (const workflow of state.workflows || []) {
    const item = document.createElement("button");
    item.className = "flow-card" + (workflow.name === state.graph.name ? " active" : "");
    item.innerHTML = `<strong>${escapeHTML(workflow.name)}</strong><span>${escapeHTML(workflow.source || "")} · ${workflow.stages || 0} stages</span>`;
    item.onclick = async () => {
      const doc = await request(`/api/workflow-graphs/${encodeURIComponent(workflow.name)}`);
      state.graph = normalizeGraph(doc);
      state.selected = state.graph.stages.length ? 0 : -1;
      state.connectSource = "";
      renderAll(root);
    };
    list.appendChild(item);
  }
}

function renderCanvas(root) {
  const canvas = root.querySelector("#canvas");
  canvas.querySelectorAll(".flow-node").forEach(node => node.remove());
  for (const [index, stage] of state.graph.stages.entries()) {
    ensurePosition(stage, index);
    const nodeType = normalizedNodeType(stage);
    const node = document.createElement("div");
    node.className = `flow-node ${nodeType}` + (index === state.selected ? " selected" : "") + (stage.approval ? " needs-approval" : "") + (state.connectSource === stage.name ? " connecting" : "");
    node.style.left = `${stage.position.x}px`;
    node.style.top = `${stage.position.y}px`;
    node.innerHTML = `
      <div class="node-type">${escapeHTML(nodeType)}</div>
      <div class="node-top"><span>${escapeHTML(stage.name || "stage")}</span><b>${escapeHTML(stage.agent || "")}</b></div>
      <div class="node-skill">${escapeHTML(stage.skill || stage.tool || "-")}</div>
      ${stage.approval ? `<div class="node-flag">approval gate</div>` : ""}`;
    node.onmousedown = event => {
      if (event.target.closest("button")) return;
      const rect = root.querySelector("#canvas").getBoundingClientRect();
      state.selected = index;
      state.dragging = { index, dx: event.clientX - rect.left - stage.position.x, dy: event.clientY - rect.top - stage.position.y };
      renderStageForm(root);
      renderCanvas(root);
    };
    node.onclick = event => {
      event.stopPropagation();
      if (state.connectSource && state.connectSource !== stage.name) {
        addConnection(state.connectSource, stage.name);
        state.connectSource = "";
      }
      state.selected = index;
      renderStageForm(root);
      renderCanvas(root);
    };
    canvas.appendChild(node);
  }
  drawEdges(root);
}

function drawEdges(root) {
  const svg = root.querySelector("#edges");
  svg.querySelectorAll("path.edge").forEach(edge => edge.remove());
  const byName = new Map(state.graph.stages.map(stage => [stage.name, stage]));
  for (const stage of state.graph.stages) {
    ensurePosition(stage, 0);
    for (const next of stage.next || []) {
      const target = byName.get(next);
      if (!target) continue;
      ensurePosition(target, 0);
      const x1 = stage.position.x + 218;
      const y1 = stage.position.y + 48;
      const x2 = target.position.x;
      const y2 = target.position.y + 48;
      const mid = Math.max(50, Math.abs(x2 - x1) / 2);
      const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
      path.setAttribute("class", "edge");
      path.setAttribute("d", `M ${x1} ${y1} C ${x1 + mid} ${y1}, ${x2 - mid} ${y2}, ${x2} ${y2}`);
      svg.appendChild(path);
    }
  }
}

function renderStageForm(root) {
  const form = root.querySelector("#stageForm");
  const empty = root.querySelector("#stageEmpty");
  const stage = selectedStage();
  form.classList.toggle("hidden", !stage);
  empty.classList.toggle("hidden", !!stage);
  if (!stage) return;
  root.querySelector("#stageNodeType").value = normalizedNodeType(stage);
  root.querySelector("#stageName").value = stage.name || "";
  root.querySelector("#stageAgent").value = stage.agent || "";
  root.querySelector("#stageSkill").value = stage.skill || "";
  root.querySelector("#stageTool").value = stage.tool || "";
  root.querySelector("#stageNext").value = (stage.next || []).join(", ");
  root.querySelector("#stageNextStrategy").value = stage.next_strategy || "";
  root.querySelector("#stageParams").value = formatParams(stage.params);
  root.querySelector("#stageApproval").checked = !!stage.approval;
  root.querySelector("#connectHint").textContent = state.connectSource ? `Connecting from ${state.connectSource}. Click a target node.` : "";
}

function syncStageFromForm(root) {
  const stage = selectedStage();
  if (!stage) return;
  stage.node_type = root.querySelector("#stageNodeType").value;
  stage.name = slug(root.querySelector("#stageName").value);
  stage.agent = root.querySelector("#stageAgent").value;
  stage.skill = root.querySelector("#stageSkill").value;
  stage.tool = root.querySelector("#stageTool").value;
  stage.next = root.querySelector("#stageNext").value.split(",").map(value => slug(value)).filter(Boolean);
  stage.next_strategy = root.querySelector("#stageNextStrategy").value.trim();
  stage.params = parseParams(root.querySelector("#stageParams").value);
  stage.approval = root.querySelector("#stageApproval").checked;
  if (!executableTypes.has(normalizedNodeType(stage))) {
    stage.agent = "";
    stage.skill = "";
    stage.tool = "";
    stage.approval = false;
  }
  renderCanvas(root);
}

function addConnection(sourceName, targetName) {
  const source = state.graph.stages.find(stage => stage.name === sourceName);
  if (!source || !targetName || sourceName === targetName) return;
  source.next = source.next || [];
  if (!source.next.includes(targetName)) source.next.push(targetName);
}

function removeSelectedStage() {
  const stage = selectedStage();
  if (!stage) return;
  state.graph.stages.splice(state.selected, 1);
  for (const other of state.graph.stages) {
    other.next = (other.next || []).filter(name => name !== stage.name);
  }
  if (state.connectSource === stage.name) state.connectSource = "";
  state.selected = Math.min(state.selected, state.graph.stages.length - 1);
}

async function saveGraph(root) {
  const output = root.querySelector("#runOutput");
  try {
    state.graph.name = slug(root.querySelector("#graphName").value);
    state.graph.description = root.querySelector("#graphDescription").value.trim();
    if (!state.graph.name) throw new Error("workflow name is required");
    const saved = await request(`/api/workflow-graphs/${encodeURIComponent(state.graph.name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(state.graph)
    });
    state.graph = normalizeGraph(saved);
    await loadWorkflowList();
    renderAll(root);
    output.textContent = `Saved ${state.graph.name}`;
  } catch (error) {
    output.textContent = `Save failed: ${error.message}`;
  }
}

async function deleteGraph(root) {
  const output = root.querySelector("#runOutput");
  try {
    if (!state.graph.name) return;
    const current = (state.workflows || []).find(item => item.name === state.graph.name);
    if (current?.source === "builtin") throw new Error("built-in workflows cannot be deleted");
    if (!confirm(`Delete workflow ${state.graph.name}?`)) return;
    const response = await fetch(`/api/workflow-graphs/${encodeURIComponent(state.graph.name)}`, { method: "DELETE" });
    if (!response.ok) throw new Error(await response.text());
    await loadWorkflowList();
    createPresetGraph();
    renderAll(root);
    output.textContent = "Deleted.";
  } catch (error) {
    output.textContent = `Delete failed: ${error.message}`;
  }
}

async function runGraph(root) {
  const output = root.querySelector("#runOutput");
  const input = root.querySelector("#runInput").value.trim();
  if (!state.graph.name || !input) return;
  output.textContent = "";
  try {
    await streamRun(`/api/workflows/${encodeURIComponent(state.graph.name)}/stream`, input, event => {
      if (event.type === "workflow_result") {
        output.textContent += `\nworkflow ${event.workflow_name}: ${event.workflow_status}\n`;
      } else if (event.type === "task_stage") {
        output.textContent += `\nstage: ${event.task_stage || ""} ${event.content || ""}`;
      } else if (event.type === "approval") {
        output.textContent += `\napproval required: ${event.tool_name || ""} ${event.arguments_summary || ""}`;
      } else if (event.type === "token_usage") {
        output.textContent += `\ntokens: in ${event.prompt_tokens || 0}, out ${event.output_tokens || 0}`;
      } else if (event.content) {
        output.textContent += `\n${event.content}`;
      }
    });
  } catch (error) {
    output.textContent += `\nRun failed: ${error.message}`;
  }
}

function normalizeGraph(doc) {
  const graph = { name: doc.name || "", description: doc.description || "", stages: doc.stages || [] };
  graph.stages.forEach((stage, index) => {
    stage.node_type = normalizedNodeType(stage);
    stage.params = stage.params || {};
    ensurePosition(stage, index);
  });
  return graph;
}

function normalizedNodeType(stage) {
  const raw = String(stage?.node_type || "").trim().toLowerCase();
  if (raw) return raw;
  return stage?.name === "start" ? "start" : stage?.name === "end" ? "end" : "agent";
}

function ensurePosition(stage, index = 0) {
  if (!stage.position) stage.position = {};
  if (!Number.isFinite(stage.position.x)) stage.position.x = 80 + index * 280;
  if (!Number.isFinite(stage.position.y)) stage.position.y = 150;
}

function selectedStage() {
  return state.graph.stages[state.selected];
}

function fillSelect(select, values) {
  const previous = select.value;
  select.innerHTML = "";
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = "-";
  select.appendChild(empty);
  for (const value of values || []) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = value;
    select.appendChild(option);
  }
  if ((values || []).includes(previous)) select.value = previous;
}

function parseParams(raw) {
  const out = {};
  for (const line of String(raw || "").split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const index = trimmed.indexOf("=");
    if (index <= 0) continue;
    out[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim();
  }
  return out;
}

function formatParams(params) {
  return Object.entries(params || {}).map(([key, value]) => `${key}=${value}`).join("\n");
}

function uniqueStageName(prefix) {
  const used = new Set(state.graph.stages.map(stage => stage.name));
  let index = 1;
  let candidate = prefix;
  while (used.has(candidate)) {
    index++;
    candidate = `${prefix}-${index}`;
  }
  return candidate;
}

function slug(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
