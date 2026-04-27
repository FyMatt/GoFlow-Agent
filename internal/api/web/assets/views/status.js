import { escapeHTML } from "../api.js";

export async function renderStatus(root, runtime) {
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-5">
        <h2>Runtime status</h2>
        <pre class="log">${escapeHTML((runtime.status_lines || []).join("\n"))}</pre>
      </section>
      <section class="panel span-7">
        <h2>Session snapshot</h2>
        <pre class="log">${escapeHTML(JSON.stringify(runtime.session || {}, null, 2))}</pre>
      </section>
    </div>`;
}
