import { escapeHTML, request } from "../api.js";
import { t } from "../i18n.js";

export async function renderSettings(root, runtime) {
  let update = null;
  try {
    update = await request("/api/update-policy");
  } catch {
    update = null;
  }
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-7">
        <h2>${t("settings.firstRun")}</h2>
        <p class="muted">${t("settings.firstRunHelp")}</p>
        <table class="kv">
          ${(runtime.setup?.env || []).map(item => `
            <tr>
              <th>${escapeHTML(item.name)}</th>
              <td>
                <span class="badge ${item.set ? "good" : item.required ? "warn" : ""}">${item.set ? t("common.set") : item.required ? t("common.required") : t("common.optional")}</span>
                <div class="muted">${escapeHTML(item.description || "")}</div>
              </td>
            </tr>`).join("")}
        </table>
      </section>
      <section class="panel span-5">
        <h2>${t("settings.guidedTour")}</h2>
        <div class="tour" style="grid-template-columns:1fr">
          <div class="tour-step"><strong>${t("settings.tourWorkspace")}</strong><span>${t("settings.tourWorkspaceHelp")}</span></div>
          <div class="tour-step"><strong>${t("settings.tourWorkflow")}</strong><span>${t("settings.tourWorkflowHelp")}</span></div>
          <div class="tour-step"><strong>${t("settings.tourApprovals")}</strong><span>${t("settings.tourApprovalsHelp")}</span></div>
          <div class="tour-step"><strong>${t("settings.tourObservability")}</strong><span>${t("settings.tourObservabilityHelp")}</span></div>
        </div>
      </section>
      <section class="panel span-12">
        <h2>${t("settings.updates")}</h2>
        <p class="muted">${t("settings.updatesHelp")}</p>
        <pre class="log">${escapeHTML(JSON.stringify(update || {}, null, 2))}</pre>
      </section>
    </div>`;
}
