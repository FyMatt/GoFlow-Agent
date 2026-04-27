import { request } from "./api.js";
import { applyStaticTranslations, currentLanguage, setLanguage, t } from "./i18n.js";
import { renderOverview } from "./views/overview.js";
import { renderChat } from "./views/chat.js";
import { renderWorkspace } from "./views/workspace.js";
import { renderWorkflows } from "./views/workflows.js";
import { renderApprovals } from "./views/approvals.js";
import { renderCatalog } from "./views/catalog.js";
import { renderStatus } from "./views/status.js";
import { renderSettings } from "./views/settings.js";

const views = {
  overview: { title: "view.overview.title", eyebrow: "view.overview.eyebrow", render: renderOverview },
  playground: { title: "view.playground.title", eyebrow: "view.playground.eyebrow", render: renderChat },
  workspace: { title: "view.workspace.title", eyebrow: "view.workspace.eyebrow", render: renderWorkspace },
  workflows: { title: "view.workflows.title", eyebrow: "view.workflows.eyebrow", render: renderWorkflows },
  approvals: { title: "view.approvals.title", eyebrow: "view.approvals.eyebrow", render: renderApprovals },
  catalog: { title: "view.catalog.title", eyebrow: "view.catalog.eyebrow", render: renderCatalog },
  status: { title: "view.status.title", eyebrow: "view.status.eyebrow", render: renderStatus },
  settings: { title: "view.settings.title", eyebrow: "view.settings.eyebrow", render: renderSettings }
};

const app = document.getElementById("app");
const sectionTitle = document.getElementById("sectionTitle");
const sectionEyebrow = document.getElementById("sectionEyebrow");
const runtimePill = document.getElementById("runtimePill");
const sideStatus = document.getElementById("sideStatus");
const languageSelect = document.getElementById("languageSelect");

let runtime = null;

async function refreshRuntime() {
  runtime = await request("/api/runtime");
  runtimePill.textContent = `${runtime.active_agent || "-"} / ${runtime.mode || "-"} / ${runtime.version || "dev"}`;
  sideStatus.textContent = [
    `${t("runtime.workspace")}: ${runtime.workspace?.display || "(none)"}`,
    `${t("runtime.trace")}: ${runtime.trace ? "on" : "off"}`,
    `${t("runtime.pending")}: ${runtime.session?.pending_approvals?.length || 0}`,
    `${t("runtime.tools")}: ${runtime.tools?.length || 0}`
  ].join("\n");
  return runtime;
}

async function show(viewName) {
  const view = views[viewName] || views.workflows;
  document.querySelectorAll(".nav button").forEach(button => {
    button.classList.toggle("active", button.dataset.view === viewName);
  });
  applyStaticTranslations();
  sectionTitle.textContent = t(view.title);
  sectionEyebrow.textContent = t(view.eyebrow);
  app.innerHTML = `<div class="panel">${t("common.loading")}</div>`;
  try {
    const current = await refreshRuntime();
    await view.render(app, current, refreshRuntime);
  } catch (error) {
    app.innerHTML = `<div class="panel"><h2>${t("common.errorTitle")}</h2><p class="muted">${error.message}</p></div>`;
  }
}

document.querySelectorAll(".nav button").forEach(button => {
  button.addEventListener("click", () => {
    location.hash = button.dataset.view;
  });
});

window.addEventListener("hashchange", () => show(location.hash.slice(1) || defaultView()));

languageSelect.value = currentLanguage();
languageSelect.addEventListener("change", () => {
  setLanguage(languageSelect.value);
  show(location.hash.slice(1) || defaultView());
});

function defaultView() {
  return location.pathname.startsWith("/workflows") ? "workflows" : "overview";
}

show(location.hash.slice(1) || defaultView());
