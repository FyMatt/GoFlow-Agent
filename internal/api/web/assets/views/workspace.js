import { escapeHTML, postJSON } from "../api.js";
import { t } from "../i18n.js";

export async function renderWorkspace(root, runtime, refreshRuntime) {
  const workspace = runtime.workspace || {};
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-7">
        <h2>${t("workspace.title")}</h2>
        <table class="kv">
          <tr><th>${t("workspace.root")}</th><td><code>${escapeHTML(workspace.root || t("common.none"))}</code></td></tr>
          <tr><th>${t("workspace.status")}</th><td>${badge(workspace.confirmed ? t("workspace.confirmed") : t("workspace.needsConfirmation"), workspace.confirmed ? "good" : "warn")}</td></tr>
          <tr><th>${t("workspace.source")}</th><td>${escapeHTML(workspace.source || "-")}</td></tr>
        </table>
        <div class="toolbar" style="margin-top:14px">
          <button id="confirm" class="primary">${t("workspace.confirm")}</button>
          <button id="clear">${t("workspace.clear")}</button>
        </div>
      </section>
      <section class="panel span-5">
        <h2>${t("workspace.selectTitle")}</h2>
        <p class="muted">${t("workspace.selectHelp")}</p>
        <input id="workspacePath" placeholder="${escapeHTML(t("workspace.pathPlaceholder"))}">
        <button id="select" style="margin-top:10px">${t("workspace.prepareRestart")}</button>
        <pre id="workspaceOutput" class="log" style="margin-top:12px;min-height:120px"></pre>
      </section>
    </div>`;

  const output = root.querySelector("#workspaceOutput");
  root.querySelector("#confirm").onclick = async () => {
    output.textContent = JSON.stringify(await postJSON("/api/workspace/confirm"), null, 2);
    await refreshRuntime();
  };
  root.querySelector("#clear").onclick = async () => {
    output.textContent = JSON.stringify(await postJSON("/api/workspace/clear"), null, 2);
    await refreshRuntime();
  };
  root.querySelector("#select").onclick = async () => {
    try {
      output.textContent = JSON.stringify(await postJSON("/api/workspace/select", { path: root.querySelector("#workspacePath").value }), null, 2);
    } catch (error) {
      output.textContent = JSON.stringify(error.data || { error: error.message }, null, 2);
    }
  };
}

function badge(text, kind) {
  return `<span class="badge ${kind || ""}">${escapeHTML(text)}</span>`;
}
