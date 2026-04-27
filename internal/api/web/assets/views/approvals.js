import { escapeHTML, request } from "../api.js";

export async function renderApprovals(root, runtime, refreshRuntime) {
  const approvals = runtime.session?.pending_approvals || [];
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-7">
        <h2>Pending approvals</h2>
        <div id="approvalList" class="list"></div>
      </section>
      <section class="panel span-5">
        <h2>Resume log</h2>
        <pre id="approvalLog" class="log"></pre>
      </section>
    </div>`;

  const list = root.querySelector("#approvalList");
  const log = root.querySelector("#approvalLog");

  if (!approvals.length) {
    list.innerHTML = `<div class="item muted">No pending tool approvals.</div>`;
    return;
  }

  for (const approval of approvals) {
    const item = document.createElement("div");
    item.className = "item";
    item.innerHTML = `
      <strong>${escapeHTML(approval.tool_name || approval.call_id)}</strong>
      <div class="muted">${escapeHTML(approval.arguments_summary || "")}</div>
      <div class="toolbar" style="margin-top:10px">
        <button class="primary" data-action="approve">Approve</button>
        <button data-action="approve-remember">Approve and remember</button>
        <button class="danger" data-action="deny">Deny</button>
      </div>`;
    item.querySelectorAll("button").forEach(button => {
      button.onclick = async () => {
        const action = button.dataset.action;
        log.textContent = `Submitting ${action} for ${approval.call_id}...\n`;
        try {
          const text = await streamApproval(approval.call_id, action);
          log.textContent += text || "Done.";
          await refreshRuntime();
        } catch (error) {
          log.textContent += error.message;
        }
      };
    });
    list.appendChild(item);
  }
}

async function streamApproval(callID, action) {
  const response = await fetch(`/api/approvals/${encodeURIComponent(callID)}/${action}/stream`, { method: "POST" });
  const text = await response.text();
  if (!response.ok) throw new Error(text || response.statusText);
  return text;
}
