import { escapeHTML } from "../api.js";

export async function renderOverview(root, runtime) {
  const pending = runtime.session?.pending_approvals?.length || 0;
  const workflow = runtime.session?.workflow || {};
  const envReady = (runtime.setup?.env || []).filter(item => item.required).every(item => item.set);
  root.innerHTML = `
    <div class="hero">
      <div>
        <p class="eyebrow">Low barrier, high ceiling</p>
        <h2>Build and run Agent workflows visually.</h2>
        <p>Use GoFlow Studio to compose agents, skills, tools, approvals, and workspace-safe actions without editing YAML first.</p>
      </div>
      <div class="hero-actions">
        <button class="primary" id="newWorkflow">Create workflow</button>
        <button id="openSettings">First-run setup</button>
      </div>
    </div>
    <div class="metric-grid">
      ${metric("Workspace", runtime.workspace?.confirmed ? "Confirmed" : "Needs confirmation", runtime.workspace?.display || "(none)", runtime.workspace?.confirmed ? "good" : "warn")}
      ${metric("Approvals", String(pending), pending ? "Waiting for operator action" : "No pending approvals", pending ? "warn" : "good")}
      ${metric("Runtime", `${escapeHTML(runtime.active_agent || "-")} / ${escapeHTML(runtime.mode || "-")}`, `Version ${escapeHTML(runtime.version || "dev")}`, "neutral")}
      ${metric("Setup", envReady ? "Ready" : "Needs env vars", "Configure model provider before first use", envReady ? "good" : "warn")}
    </div>
    <div class="grid">
      <section class="panel span-8">
        <h2>Quick start</h2>
        <div class="tour">
          <div class="tour-step"><strong>1. Confirm workspace</strong><span>Pick the project directory that tools are allowed to touch.</span></div>
          <div class="tour-step"><strong>2. Compose workflow</strong><span>Drag stages onto the canvas and bind agents or skills.</span></div>
          <div class="tour-step"><strong>3. Run with guardrails</strong><span>Review approvals, tool logs, token usage, and stage state.</span></div>
          <div class="tour-step"><strong>4. Reuse as config</strong><span>Saved workflows remain YAML files under runtime home.</span></div>
        </div>
      </section>
      <section class="panel span-4">
        <h2>Current workflow</h2>
        <table class="kv compact">
          <tr><th>Name</th><td>${escapeHTML(workflow.name || "-")}</td></tr>
          <tr><th>Status</th><td>${escapeHTML(workflow.status || "-")}</td></tr>
          <tr><th>Next</th><td>${escapeHTML(workflow.next_stage || "-")}</td></tr>
        </table>
      </section>
    </div>`;
  root.querySelector("#newWorkflow").onclick = () => { location.hash = "workflows"; };
  root.querySelector("#openSettings").onclick = () => { location.hash = "settings"; };
}

function metric(label, value, detail, kind) {
  return `<section class="metric ${kind || ""}">
    <span>${label}</span>
    <strong>${value}</strong>
    <small>${escapeHTML(detail)}</small>
  </section>`;
}
