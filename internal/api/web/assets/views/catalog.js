import { escapeHTML, formatList, request } from "../api.js";
import { t } from "../i18n.js";

export async function renderCatalog(root, runtime) {
  root.innerHTML = `
    <div class="resource-shell">
      <section class="panel resource-hero span-12">
        <div>
          <p class="eyebrow">${t("catalog.resourceBuilder")}</p>
          <h2>${t("catalog.resourceBuilderTitle")}</h2>
          <p class="muted">${t("catalog.resourceBuilderHelp")} <code>skills/&lt;name&gt;/SKILL.md</code></p>
        </div>
        <div class="hero-actions">
          <button class="primary" data-new-resource="skill">${t("catalog.newSkill")}</button>
          <button data-new-resource="agent">${t("catalog.newAgent")}</button>
          <button data-new-resource="tool">${t("catalog.newTool")}</button>
        </div>
      </section>

      <div class="grid">
        <section class="panel span-4 resource-panel">
          <div class="panel-head"><h2>${t("catalog.agents")}</h2><button data-new-resource="agent">${t("catalog.newAgent")}</button></div>
          <div class="list">${(runtime.agents || []).map(renderAgent).join("") || empty(t("catalog.noAgents"))}</div>
        </section>
        <section class="panel span-4 resource-panel">
          <div class="panel-head"><h2>${t("catalog.skills")}</h2><button class="primary" data-new-resource="skill">${t("catalog.newSkill")}</button></div>
          <div class="list">${(runtime.skills || []).map(renderSkill).join("") || empty(t("catalog.noSkills"))}</div>
        </section>
        <section class="panel span-4 resource-panel">
          <div class="panel-head"><h2>${t("catalog.tools")}</h2><button data-new-resource="tool">${t("catalog.newTool")}</button></div>
          <div class="list">${(runtime.tools || []).map(renderTool).join("") || empty(t("catalog.noTools"))}</div>
        </section>
        <section class="panel span-12">
          <h2>${t("catalog.mcpHealth")}</h2>
          <table class="kv">${Object.entries(runtime.mcp_health || {}).map(([name, status]) => `<tr><th>${escapeHTML(name)}</th><td>${escapeHTML(status)}</td></tr>`).join("") || `<tr><td class="muted">${t("catalog.noMCP")}</td></tr>`}</table>
        </section>
      </div>

      <div id="resourceDesigner" class="designer hidden" role="dialog" aria-modal="true">
        <div class="designer-backdrop" data-close-designer></div>
        <section class="designer-panel">
          <div class="designer-head">
            <div>
              <p class="eyebrow" id="designerEyebrow">${t("catalog.resourceBuilder")}</p>
              <h2 id="designerTitle">${t("catalog.resourceBuilderTitle")}</h2>
            </div>
            <button data-close-designer aria-label="${escapeHTML(t("catalog.closeDesigner"))}">×</button>
          </div>
          <div class="designer-body">
            <div class="grid">
              <label class="span-4 stack"><span>${t("catalog.resourceType")}</span><select id="resourceType"><option value="skill">${t("catalog.resourceSkill")}</option><option value="agent">${t("catalog.resourceAgent")}</option><option value="tool">${t("catalog.resourceTool")}</option></select></label>
              <label class="span-4 stack"><span>${t("catalog.name")}</span><input id="resourceName" placeholder="custom-code-review"></label>
              <label class="span-4 stack"><span>${t("catalog.purpose")}</span><input id="resourcePurpose" placeholder="${escapeHTML(t("catalog.purpose"))}"></label>
              <label class="span-12 stack"><span>${t("catalog.description")}</span><input id="resourceDescription" placeholder="${escapeHTML(t("catalog.description"))}"></label>

              <div class="span-12 skill-only grid">
                <label class="span-4 stack"><span>${t("common.mode")}</span><select id="skillMode"><option value="chat">chat</option><option value="plan">plan</option><option value="fix">fix</option><option value="audit">audit</option></select></label>
                <label class="span-4 stack"><span>${t("catalog.preferredAgent")}</span><select id="skillAgent"></select></label>
                <label class="span-4 stack"><span>${t("catalog.outputKind")}</span><input id="skillOutputKind" placeholder="summary / findings / changes"></label>
                <label class="span-4 stack"><span>${t("catalog.allowedKinds")}</span><input id="skillAllowedKinds" placeholder="read, write, exec, network"></label>
                <label class="span-4 stack"><span>${t("catalog.nextSkills")}</span><input id="skillNext" placeholder="code-audit, docs-review"></label>
                <label class="span-4 stack"><span>${t("catalog.activationKeywords")}</span><textarea id="skillKeywords" class="compact-textarea" placeholder="audit&#10;review&#10;security"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.tools")}</span><textarea id="skillTools" class="compact-textarea" placeholder="file_tools/read_file|required&#10;web_tools/web_search"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.instructions")}</span><textarea id="skillInstructions" placeholder="${escapeHTML(t("catalog.defaultInstructions"))}"></textarea></label>
              </div>

              <label class="span-12 stack non-skill-only"><span>${t("catalog.extraRequirements")}</span><textarea id="resourceDetails" placeholder="${escapeHTML(t("catalog.extraRequirements"))}"></textarea></label>
            </div>
          </div>
          <div class="designer-actions">
            <button id="saveSkill" class="primary">${t("catalog.saveSkill")}</button>
            <button id="openBuilder" class="primary">${t("catalog.openInPlayground")}</button>
            <button id="resetResource">${t("catalog.resetForm")}</button>
          </div>
          <pre id="resourceOutput" class="mini-log"></pre>
        </section>
      </div>
    </div>`;

  fillAgentSelect(root, runtime);
  bindResourceCatalog(root, runtime);
}

function bindResourceCatalog(root, runtime) {
  root.querySelectorAll("[data-new-resource]").forEach(button => {
    button.onclick = () => openDesigner(root, runtime, button.dataset.newResource, null);
  });
  root.querySelectorAll("[data-edit-resource]").forEach(button => {
    button.onclick = () => openDesigner(root, runtime, button.dataset.resourceType, button.dataset.resourceName);
  });
  root.querySelectorAll("[data-close-designer]").forEach(button => {
    button.onclick = () => closeDesigner(root);
  });
  root.querySelector("#resourceType").onchange = () => syncDesignerMode(root);
  root.querySelector("#saveSkill").onclick = () => saveSkill(root);
  root.querySelector("#resetResource").onclick = () => openDesigner(root, runtime, root.querySelector("#resourceType").value, null);
  root.querySelector("#openBuilder").onclick = () => {
    const command = buildResourceRequest(collectResourceDraft(root));
    localStorage.setItem("goflow.playground.draft", command);
    location.hash = "playground";
  };
}

function renderAgent(agent) {
  return `<div class="item resource-item">
    <strong>${escapeHTML(agent.id)}</strong>
    <span class="muted">${escapeHTML(agent.name || "")} ${escapeHTML(agent.mode || "")}</span>
    <div class="muted">${t("common.provider")}: ${escapeHTML(agent.provider || "-")} / ${t("common.model")}: ${escapeHTML(agent.model || "-")}</div>
    <div class="muted">${t("common.policy")}: ${escapeHTML(agent.tool_policy || "-")} / ${t("common.tools")}: ${escapeHTML(formatList(agent.allowed_tool_kinds))}</div>
    <button data-edit-resource data-resource-type="agent" data-resource-name="${escapeHTML(agent.id)}">${t("catalog.edit")}</button>
  </div>`;
}

function renderSkill(skill) {
  return `<div class="item resource-item">
    <strong>${escapeHTML(skill.name)}</strong>
    <span class="muted">${escapeHTML(skill.description || "")}</span>
    <div class="muted">${t("common.agent")}: ${escapeHTML(skill.preferred_agent || "-")} / ${t("common.mode")}: ${escapeHTML(skill.mode || "-")}</div>
    <div class="muted">${t("common.next")}: ${escapeHTML(formatList(skill.next_skills))}</div>
    <button data-edit-resource data-resource-type="skill" data-resource-name="${escapeHTML(skill.name)}">${t("catalog.edit")}</button>
  </div>`;
}

function renderTool(tool) {
  return `<div class="item resource-item">
    <strong>${escapeHTML(tool)}</strong>
    <span class="muted">${t("catalog.resourceTool")}</span>
    <button data-edit-resource data-resource-type="tool" data-resource-name="${escapeHTML(tool)}">${t("catalog.edit")}</button>
  </div>`;
}

function empty(text) {
  return `<div class="item muted">${escapeHTML(text)}</div>`;
}

async function openDesigner(root, runtime, type, name) {
  const designer = root.querySelector("#resourceDesigner");
  designer.classList.remove("hidden");
  root.querySelector("#resourceType").value = type || "skill";
  clearDesigner(root, runtime);
  if (type === "skill" && name) {
    await loadSkillByName(root, runtime, name);
  } else if (type === "agent" && name) {
    const agent = (runtime.agents || []).find(item => item.id === name);
    root.querySelector("#resourceName").value = agent?.id || name;
    root.querySelector("#resourceDescription").value = agent?.name || "";
    root.querySelector("#resourcePurpose").value = agent?.mode || "";
    root.querySelector("#resourceDetails").value = `${t("common.provider")}: ${agent?.provider || ""}\n${t("common.model")}: ${agent?.model || ""}\n${t("common.policy")}: ${agent?.tool_policy || ""}`;
  } else if (type === "tool" && name) {
    root.querySelector("#resourceName").value = name;
    root.querySelector("#resourceDescription").value = t("catalog.resourceTool");
  }
  syncDesignerMode(root);
}

function closeDesigner(root) {
  root.querySelector("#resourceDesigner").classList.add("hidden");
}

function clearDesigner(root, runtime) {
  const type = root.querySelector("#resourceType").value || "skill";
  root.querySelector("#resourceName").value = type === "skill" ? "custom-skill" : "";
  root.querySelector("#resourcePurpose").value = "";
  root.querySelector("#resourceDescription").value = type === "skill" ? t("catalog.customSkillDescription") : "";
  root.querySelector("#resourceDetails").value = "";
  loadSkillForm(root, runtime, null);
  root.querySelector("#resourceOutput").textContent = "";
  syncDesignerMode(root);
}

function syncDesignerMode(root) {
  const type = root.querySelector("#resourceType").value;
  const isSkill = type === "skill";
  root.querySelector("#designerTitle").textContent = isSkill ? t("catalog.skillEditor") : type === "agent" ? t("catalog.agentDesigner") : t("catalog.toolDesigner");
  root.querySelectorAll(".skill-only").forEach(node => node.classList.toggle("hidden", !isSkill));
  root.querySelectorAll(".non-skill-only").forEach(node => node.classList.toggle("hidden", isSkill));
  root.querySelector("#saveSkill").classList.toggle("hidden", !isSkill);
  root.querySelector("#openBuilder").classList.toggle("hidden", isSkill);
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
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const doc = await request(`/api/resources/skills/${encodeURIComponent(name)}`);
    loadSkillForm(root, runtime, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${error.message}`;
  }
}

function loadSkillForm(root, runtime, doc) {
  const fallbackAgent = runtime.active_agent || runtime.agents?.[0]?.id || "chat";
  root.querySelector("#resourceName").value = doc?.name || root.querySelector("#resourceName").value || "custom-skill";
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.customSkillDescription");
  root.querySelector("#skillMode").value = doc?.mode || "chat";
  root.querySelector("#skillAgent").value = doc?.preferred_agent || fallbackAgent;
  root.querySelector("#skillAllowedKinds").value = (doc?.allowed_tool_kinds || ["read"]).join(", ");
  root.querySelector("#skillOutputKind").value = doc?.output_kind || "summary";
  root.querySelector("#skillNext").value = (doc?.next_skills || []).join(", ");
  root.querySelector("#skillKeywords").value = (doc?.activation?.keywords || [doc?.name || "custom-skill"]).join("\n");
  root.querySelector("#skillTools").value = (doc?.tools || []).map(tool => `${tool.name}${tool.required ? "|required" : ""}`).join("\n");
  root.querySelector("#skillInstructions").value = doc?.instructions || t("catalog.defaultInstructions");
}

async function saveSkill(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectSkillForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    const saved = await request(`/api/resources/skills/${encodeURIComponent(doc.name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    output.textContent = `${t("catalog.saved")} ${saved.name}\n${saved.path || ""}`;
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${error.message}`;
  }
}

function collectSkillForm(root) {
  return {
    name: root.querySelector("#resourceName").value.trim(),
    description: root.querySelector("#resourceDescription").value.trim(),
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

function collectResourceDraft(root) {
  return {
    type: root.querySelector("#resourceType").value,
    name: root.querySelector("#resourceName").value.trim(),
    purpose: root.querySelector("#resourcePurpose").value.trim(),
    description: root.querySelector("#resourceDescription").value.trim(),
    details: root.querySelector("#resourceDetails").value.trim()
  };
}

function buildResourceRequest({ type, name, purpose, description, details }) {
  const label = type === "tool" ? t("catalog.resourceTool") : type === "agent" ? t("catalog.resourceAgent") : t("catalog.resourceSkill");
  return [
    `${t("catalog.resourceDraftPrefix")} ${label} ${t("catalog.resourceDraftNamed")} ${name || "<name>"} ${t("catalog.resourceDraftInProject")}`,
    description ? `${t("catalog.description")}: ${description}` : "",
    purpose ? `${t("catalog.resourceDraftPurpose")} ${purpose}` : "",
    details ? `${t("catalog.resourceDraftRequirements")}\n${details}` : "",
    t("catalog.resourceDraftConventions"),
    t("catalog.resourceDraftSummary")
  ].filter(Boolean).join("\n\n");
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
