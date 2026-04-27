import { escapeHTML, request } from "../api.js";

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
        <h2>First-run setup</h2>
        <p class="muted">These checks help non-developer users understand what must be configured before the visual Studio can call a model.</p>
        <table class="kv">
          ${(runtime.setup?.env || []).map(item => `
            <tr>
              <th>${escapeHTML(item.name)}</th>
              <td>
                <span class="badge ${item.set ? "good" : item.required ? "warn" : ""}">${item.set ? "set" : item.required ? "required" : "optional"}</span>
                <div class="muted">${escapeHTML(item.description || "")}</div>
              </td>
            </tr>`).join("")}
        </table>
      </section>
      <section class="panel span-5">
        <h2>Guided tour</h2>
        <div class="tour" style="grid-template-columns:1fr">
          <div class="tour-step"><strong>Workspace</strong><span>Confirm where tools may read, write, or execute.</span></div>
          <div class="tour-step"><strong>Workflow Studio</strong><span>Drag stages, bind agents/skills, and save reusable flows.</span></div>
          <div class="tour-step"><strong>Approvals</strong><span>Review risky tool calls before they run.</span></div>
          <div class="tour-step"><strong>Observability</strong><span>Inspect task stages, token usage, session state, and MCP health.</span></div>
        </div>
      </section>
      <section class="panel span-12">
        <h2>Updates</h2>
        <p class="muted">GoFlow can prompt for updates from GitHub Releases. Automatic replacement should remain opt-in and verify checksums/signatures before changing local files.</p>
        <pre class="log">${escapeHTML(JSON.stringify(update || {}, null, 2))}</pre>
      </section>
    </div>`;
}
