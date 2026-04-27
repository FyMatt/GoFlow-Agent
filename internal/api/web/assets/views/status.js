import { escapeHTML } from "../api.js";
import { t } from "../i18n.js";

export async function renderStatus(root, runtime) {
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-5">
        <h2>${t("status.runtime")}</h2>
        <pre class="log">${escapeHTML((runtime.status_lines || []).join("\n"))}</pre>
      </section>
      <section class="panel span-7">
        <h2>${t("status.session")}</h2>
        <pre class="log">${escapeHTML(JSON.stringify(runtime.session || {}, null, 2))}</pre>
      </section>
    </div>`;
}
