import { escapeHTML, formatList, request } from "../api.js";

export async function renderCatalog(root, runtime) {
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-4">
        <h2>Agents</h2>
        <div class="list">${(runtime.agents || []).map(renderAgent).join("") || empty("No agents configured.")}</div>
      </section>
      <section class="panel span-4">
        <h2>Skills</h2>
        <div class="toolbar" style="margin-bottom:10px"><button id="newSkill" class="primary">New skill</button></div>
        <div class="list">${(runtime.skills || []).map(renderSkill).join("") || empty("No skills loaded.")}</div>
      </section>
      <section class="panel span-4">
        <h2>Tools</h2>
        <div class="list">${(runtime.tools || []).map(tool => `<div class="item"><strong>${escapeHTML(tool)}</strong></div>`).join("") || empty("No tools discovered.")}</div>
      </section>
      <section class="panel span-12">
        <h2>MCP health</h2>
        <table class="kv">${Object.entries(runtime.mcp_health || {}).map(([name, status]) => `<tr><th>${escapeHTML(name)}</th><td>${escapeHTML(status)}</td></tr>`).join("") || `<tr><td class="muted">No MCP health data.</td></tr>`}</table>
      </section>
      <section class="panel span-12">
        <h2>Skill editor</h2>
        <p class="muted">Create or update `skills/&lt;name&gt;/SKILL.md` from a validated form. The server parses the generated file before saving and reloads skills when hot reload is available.</p>
        <div class="grid">
          <label class="span-4 stack"><span>Name</span><input id="skillName" placeholder="custom-audit"></label>
          <label class="span-4 stack"><span>Mode</span><select id="skillMode"><option value="chat">chat</option><option value="plan">plan</option><option value="fix">fix</option><option value="audit">audit</option></select></label>
          <label class="span-4 stack"><span>Preferred agent</span><select id="skillAgent"></select></label>
          <label class="span-12 stack"><span>Description</span><input id="skillDescription" placeholder="What this skill is for"></label>
          <label class="span-4 stack"><span>Allowed tool kinds</span><input id="skillAllowedKinds" placeholder="read, write, exec, network"></label>
          <label class="span-4 stack"><span>Output kind</span><input id="skillOutputKind" placeholder="summary / findings / changes"></label>
          <label class="span-4 stack"><span>Next skills</span><input id="skillNext" placeholder="code-audit, docs-review"></label>
          <label class="span-6 stack"><span>Activation keywords</span><textarea id="skillKeywords" class="compact-textarea" placeholder="audit&#10;review&#10;security"></textarea></label>
          <label class="span-6 stack"><span>Tools</span><textarea id="skillTools" class="compact-textarea" placeholder="file_tools/read_file|required&#10;web_tools/web_search"></textarea></label>
          <label class="span-12 stack"><span>Instructions</span><textarea id="skillInstructions" placeholder="## Role&#10;&#10;Describe the skill behavior."></textarea></label>
        </div>
        <div class="toolbar" style="margin-top:10px">
          <button id="saveSkill" class="primary">Save skill</button>
          <button id="resetSkill">Reset form</button>
        </div>
        <pre id="skillEditorOutput" class="mini-log"></pre>
      </section>
      <section class="panel span-12">
        <h2>Resource builder</h2>
        <p class="muted">Create or edit resources through the same agent/tool approval path as the CLI. The request is opened in Playground, where file writes still require approval.</p>
        <div class="grid">
          <label class="span-4 stack"><span>Resource type</span><select id="resourceType"><option value="skill">Skill</option><option value="tool">Python MCP Tool</option><option value="agent">Agent</option></select></label>
          <label class="span-4 stack"><span>Name</span><input id="resourceName" placeholder="custom-code-review"></label>
          <label class="span-4 stack"><span>Purpose</span><input id="resourcePurpose" placeholder="What should it do?"></label>
          <label class="span-12 stack"><span>Extra requirements</span><textarea id="resourceDetails" placeholder="Tools, trigger keywords, permissions, workflow usage, examples..."></textarea></label>
        </div>
        <button id="openBuilder" class="primary" style="margin-top:10px">Open in Playground</button>
      </section>
    </div>`;
  fillAgentSelect(root, runtime);
  root.querySelector("#newSkill").onclick = () => loadSkillForm(root, runtime, null);
  root.querySelectorAll("[data-edit-skill]").forEach(button => {
    button.onclick = () => loadSkillByName(root, runtime, button.dataset.editSkill);
  });
  root.querySelector("#saveSkill").onclick = () => saveSkill(root, runtime);
  root.querySelector("#resetSkill").onclick = () => loadSkillForm(root, runtime, null);
  root.querySelector("#openBuilder").onclick = () => {
    const type = root.querySelector("#resourceType").value;
    const name = root.querySelector("#resourceName").value.trim();
    const purpose = root.querySelector("#resourcePurpose").value.trim();
    const details = root.querySelector("#resourceDetails").value.trim();
    const command = buildResourceRequest(type, name, purpose, details);
    localStorage.setItem("goflow.playground.draft", command);
    location.hash = "playground";
  };
  loadSkillForm(root, runtime, null);
}

function renderAgent(agent) {
  return `<div class="item">
    <strong>${escapeHTML(agent.id)}</strong>
    <span class="muted">${escapeHTML(agent.name || "")} ${escapeHTML(agent.mode || "")}</span>
    <div class="muted">provider: ${escapeHTML(agent.provider || "-")} / model: ${escapeHTML(agent.model || "-")}</div>
    <div class="muted">policy: ${escapeHTML(agent.tool_policy || "-")} / tools: ${escapeHTML(formatList(agent.allowed_tool_kinds))}</div>
  </div>`;
}

function renderSkill(skill) {
  return `<div class="item">
    <strong>${escapeHTML(skill.name)}</strong>
    <span class="muted">${escapeHTML(skill.description || "")}</span>
    <div class="muted">agent: ${escapeHTML(skill.preferred_agent || "-")} / mode: ${escapeHTML(skill.mode || "-")}</div>
    <div class="muted">next: ${escapeHTML(formatList(skill.next_skills))}</div>
    <button data-edit-skill="${escapeHTML(skill.name)}" style="margin-top:8px">Edit</button>
  </div>`;
}

function empty(text) {
  return `<div class="item muted">${escapeHTML(text)}</div>`;
}

function buildResourceRequest(type, name, purpose, details) {
  const label = type === "tool" ? "Python MCP tool" : type;
  return [
    `Create or update a ${label} named ${name || "<name>"} in this GoFlow project.`,
    purpose ? `Purpose: ${purpose}` : "",
    details ? `Requirements:\n${details}` : "",
    "Use the existing scaffold conventions, keep generated files in the correct framework directories, and let tool approvals protect any file writes.",
    "After editing, summarize changed files and verification."
  ].filter(Boolean).join("\n\n");
}

function fillAgentSelect(root, runtime) {
  const select = root.querySelector("#skillAgent");
  select.innerHTML = "";
  for (const agent of runtime.agents || []) {
    const option = document.createElement("option");
    option.value = agent.id;
    option.textContent = `${agent.id} (${agent.mode || "-"})`;
    select.appendChild(option);
  }
}

async function loadSkillByName(root, runtime, name) {
  const output = root.querySelector("#skillEditorOutput");
  output.textContent = `Loading ${name}...`;
  try {
    const doc = await request(`/api/resources/skills/${encodeURIComponent(name)}`);
    loadSkillForm(root, runtime, doc);
    output.textContent = `Loaded ${doc.name}`;
  } catch (error) {
    output.textContent = `Load failed: ${error.message}`;
  }
}

function loadSkillForm(root, runtime, doc) {
  const fallbackAgent = runtime.active_agent || runtime.agents?.[0]?.id || "chat";
  root.querySelector("#skillName").value = doc?.name || "custom-skill";
  root.querySelector("#skillMode").value = doc?.mode || "chat";
  root.querySelector("#skillAgent").value = doc?.preferred_agent || fallbackAgent;
  root.querySelector("#skillDescription").value = doc?.description || "Custom GoFlow skill.";
  root.querySelector("#skillAllowedKinds").value = (doc?.allowed_tool_kinds || ["read"]).join(", ");
  root.querySelector("#skillOutputKind").value = doc?.output_kind || "summary";
  root.querySelector("#skillNext").value = (doc?.next_skills || []).join(", ");
  root.querySelector("#skillKeywords").value = (doc?.activation?.keywords || [doc?.name || "custom-skill"]).join("\n");
  root.querySelector("#skillTools").value = (doc?.tools || []).map(tool => `${tool.name}${tool.required ? "|required" : ""}`).join("\n");
  root.querySelector("#skillInstructions").value = doc?.instructions || "## Role\n\nDescribe what this skill should do.\n\n## Workflow\n\n1. Inspect relevant context.\n2. Execute the task safely.\n3. Summarize results and verification.";
}

async function saveSkill(root, runtime) {
  const output = root.querySelector("#skillEditorOutput");
  const doc = collectSkillForm(root);
  output.textContent = `Saving ${doc.name}...`;
  try {
    const saved = await request(`/api/resources/skills/${encodeURIComponent(doc.name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    output.textContent = `Saved ${saved.name}\n${saved.path || ""}`;
  } catch (error) {
    output.textContent = `Save failed: ${error.message}`;
  }
}

function collectSkillForm(root) {
  return {
    name: root.querySelector("#skillName").value.trim(),
    description: root.querySelector("#skillDescription").value.trim(),
    version: "1.0.0",
    author: "GoFlow Studio",
    mode: root.querySelector("#skillMode").value,
    preferred_agent: root.querySelector("#skillAgent").value,
    allowed_tool_kinds: splitList(root.querySelector("#skillAllowedKinds").value),
    output_kind: root.querySelector("#skillOutputKind").value.trim() || "summary",
    next_skills: splitList(root.querySelector("#skillNext").value),
    activation: { keywords: splitLines(root.querySelector("#skillKeywords").value) },
    tools: parseTools(root.querySelector("#skillTools").value),
    instructions: root.querySelector("#skillInstructions").value
  };
}

function splitList(value) {
  return String(value || "").split(",").map(item => item.trim()).filter(Boolean);
}

function splitLines(value) {
  return String(value || "").split(/\r?\n|,/).map(item => item.trim()).filter(Boolean);
}

function parseTools(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const parts = line.split("|").map(part => part.trim());
      return { name: parts[0], required: parts.slice(1).includes("required") };
    });
}
