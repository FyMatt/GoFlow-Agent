import { escapeHTML } from "../api.js";
import { t } from "../i18n.js";

let tourTimer = 0;

export async function renderOverview(root, runtime) {
  window.clearInterval(tourTimer);
  const pending = runtime.session?.pending_approvals?.length || 0;
  const workflow = runtime.session?.workflow || {};
  const envReady = (runtime.setup?.env || []).filter(item => item.required).every(item => item.set);
  const steps = tourSteps();
  root.innerHTML = `
    <div class="hero">
      <div>
        <p class="eyebrow">${t("overview.eyebrow")}</p>
        <h2>${t("overview.title")}</h2>
        <p>${t("overview.copy")}</p>
      </div>
      <div class="hero-actions">
        <button class="primary" id="newWorkflow">${t("overview.createWorkflow")}</button>
        <button id="openSettings">${t("overview.firstRun")}</button>
      </div>
    </div>
    <div class="metric-grid">
      ${metric(t("overview.workspace"), runtime.workspace?.confirmed ? t("overview.workspaceConfirmed") : t("overview.workspaceNeedsConfirmation"), runtime.workspace?.display || t("common.none"), runtime.workspace?.confirmed ? "good" : "warn")}
      ${metric(t("overview.approvals"), String(pending), pending ? t("overview.approvalsWaiting") : t("overview.approvalsEmpty"), pending ? "warn" : "good")}
      ${metric(t("overview.runtime"), `${escapeHTML(runtime.active_agent || "-")} / ${escapeHTML(runtime.mode || "-")}`, `${t("overview.version")} ${escapeHTML(runtime.version || "dev")}`, "neutral")}
      ${metric(t("overview.setup"), envReady ? t("overview.setupReady") : t("overview.setupNeedsEnv"), t("overview.setupHelp"), envReady ? "good" : "warn")}
    </div>
    <div class="grid">
      <section class="panel span-8 quickstart-panel">
        <div class="panel-head">
          <div>
            <h2>${t("overview.quickStart")}</h2>
            <p class="muted">${t("overview.quickStartHelp")}</p>
          </div>
          <span class="badge">${t("overview.guided")}</span>
        </div>
        <div class="tour-progress"><span></span></div>
        <div class="tour">
          ${steps.map((step, index) => tourStep(step, index)).join("")}
        </div>
      </section>
      <section class="panel span-4">
        <h2>${t("overview.currentWorkflow")}</h2>
        <table class="kv compact">
          <tr><th>${t("overview.workflowName")}</th><td>${escapeHTML(workflow.name || "-")}</td></tr>
          <tr><th>${t("overview.workflowStatus")}</th><td>${escapeHTML(workflow.status || "-")}</td></tr>
          <tr><th>${t("overview.workflowNext")}</th><td>${escapeHTML(workflow.next_stage || "-")}</td></tr>
        </table>
      </section>
    </div>`;
  root.querySelector("#newWorkflow").onclick = () => { location.hash = "workflows"; };
  root.querySelector("#openSettings").onclick = () => { location.hash = "settings"; };
  bindTour(root, steps.length);
}

function metric(label, value, detail, kind) {
  return `<section class="metric ${kind || ""}">
    <span>${label}</span>
    <strong>${value}</strong>
    <small>${escapeHTML(detail)}</small>
  </section>`;
}

function tourSteps() {
  return [
    { target: "workspace", title: t("overview.step1.title"), body: t("overview.step1.body"), action: t("overview.step1.action") },
    { target: "workflows", title: t("overview.step2.title"), body: t("overview.step2.body"), action: t("overview.step2.action") },
    { target: "playground", title: t("overview.step3.title"), body: t("overview.step3.body"), action: t("overview.step3.action") },
    { target: "catalog", title: t("overview.step4.title"), body: t("overview.step4.body"), action: t("overview.step4.action") }
  ];
}

function tourStep(step, index) {
  return `<button class="tour-step ${index === 0 ? "active" : ""}" data-tour-index="${index}" data-tour-target="${step.target}">
    <em>${String(index + 1).padStart(2, "0")}</em>
    <strong>${step.title}</strong>
    <span>${step.body}</span>
    <small>${step.action}</small>
  </button>`;
}

function bindTour(root, count) {
  const steps = Array.from(root.querySelectorAll(".tour-step"));
  const progress = root.querySelector(".tour-progress span");
  let active = 0;
  const activate = index => {
    active = index;
    steps.forEach((step, stepIndex) => step.classList.toggle("active", stepIndex === index));
    if (progress) progress.style.width = `${((index + 1) / count) * 100}%`;
  };
  steps.forEach((step, index) => {
    step.onmouseenter = () => activate(index);
    step.onfocus = () => activate(index);
    step.onclick = () => { location.hash = step.dataset.tourTarget; };
  });
  activate(0);
  tourTimer = window.setInterval(() => activate((active + 1) % count), 2600);
}
