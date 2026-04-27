import { escapeHTML, postJSON, request, streamRun } from "../api.js";
import { t } from "../i18n.js";

export async function renderChat(root, runtime, refreshRuntime) {
  root.innerHTML = `
    <div class="grid">
      <section class="panel span-8">
        <div class="panel-head">
          <h2>${t("chat.conversation")}</h2>
          <label class="agent-picker"><span>${t("chat.agent")}</span><select id="agentSelect"></select></label>
        </div>
        <div id="messages" class="messages"></div>
        <div class="composer">
          <textarea id="prompt" placeholder="${escapeHTML(t("chat.placeholder"))}"></textarea>
          <div class="stack">
            <button id="send" class="primary">${t("chat.run")}</button>
            <button id="clear">${t("chat.clear")}</button>
          </div>
        </div>
      </section>
      <aside class="panel span-4">
        <h2>${t("chat.files")}</h2>
        <p class="muted">${t("chat.fileHelp")}</p>
        <input id="filePrefix" placeholder="${escapeHTML(t("chat.fileFilter"))}">
        <div id="fileSuggestions" class="file-suggestions" style="margin-top:10px"></div>
      </aside>
    </div>`;

  const messages = root.querySelector("#messages");
  const prompt = root.querySelector("#prompt");
  const send = root.querySelector("#send");
  const clear = root.querySelector("#clear");
  const agentSelect = root.querySelector("#agentSelect");
  const prefix = root.querySelector("#filePrefix");
  const suggestions = root.querySelector("#fileSuggestions");
  const draft = localStorage.getItem("goflow.playground.draft");
  if (draft) {
    prompt.value = draft;
    localStorage.removeItem("goflow.playground.draft");
  }

  for (const agent of runtime.agents || []) {
    const option = document.createElement("option");
    option.value = agent.id;
    option.textContent = `${agent.id} (${agent.mode || "-"})`;
    agentSelect.appendChild(option);
  }
  agentSelect.value = runtime.active_agent || agentSelect.value;

  for (const item of runtime.session?.recent_prompts || []) {
    appendMessage(messages, item, "user");
  }

  send.onclick = async () => {
    const input = prompt.value.trim();
    if (!input) return;
    appendMessage(messages, input, "user");
    prompt.value = "";
    send.disabled = true;
    let finalMessage = "";
    try {
      if (agentSelect.value && agentSelect.value !== runtime.active_agent) {
        await postJSON("/api/runtime/agent", { agent: agentSelect.value });
      }
      await streamRun("/api/run/stream", input, event => {
        if (event.type === "text") {
          finalMessage += event.content || "";
          appendOrUpdateAssistant(messages, finalMessage);
          return;
        }
        if (event.type === "final_message") {
          appendMessage(messages, event.content || "", "assistant");
          return;
        }
        if (event.type === "error") {
          appendMessage(messages, event.content || "error", "error");
          return;
        }
        if (event.type === "token_usage") {
          appendMessage(messages, `${t("chat.tokens")}: ${t("chat.input")} ${event.prompt_tokens || 0}, ${t("chat.output")} ${event.output_tokens || 0}, ${t("chat.cached")} ${event.cached_tokens || 0}`, "event");
          return;
        }
        if (event.type === "tool_call") {
          appendMessage(messages, `[${t("chat.tool")}] ${event.tool_name || ""} ${event.arguments_summary || ""}`, "event");
          return;
        }
        if (event.type === "tool_result") {
          appendMessage(messages, `[${t("chat.toolDone")}] ${event.tool_name || ""} ${event.arguments_summary || ""}`, "event");
          return;
        }
        if (event.type === "approval") {
          appendMessage(messages, `[${t("chat.approval")}] ${event.tool_name || ""} ${event.arguments_summary || ""}`, "event");
          location.hash = "approvals";
        }
        if (event.type === "task_stage") {
          appendMessage(messages, `[${t("chat.stage")}] ${event.task_stage || ""} ${event.content || ""}`, "event");
        }
      });
    } catch (error) {
      appendMessage(messages, error.message, "error");
    } finally {
      send.disabled = false;
      refreshRuntime().catch(() => {});
    }
  };
  clear.onclick = () => { messages.innerHTML = ""; };
  prefix.oninput = () => loadFiles(prefix.value, suggestions, prompt);
  await loadFiles("", suggestions, prompt);
}

async function loadFiles(prefix, target, prompt) {
  try {
    const data = await request(`/api/workspace-files?limit=40&prefix=${encodeURIComponent(prefix || "")}`);
    target.innerHTML = "";
    for (const entry of data.entries || []) {
      const button = document.createElement("button");
      button.type = "button";
      button.innerHTML = `${entry.is_dir ? "[dir]" : "[file]"} ${escapeHTML(entry.path)}`;
      button.onclick = () => {
        if (entry.is_dir) return;
        const spacer = prompt.value && !prompt.value.endsWith(" ") ? " " : "";
        prompt.value += `${spacer}@${entry.path} `;
        prompt.focus();
      };
      target.appendChild(button);
    }
    if (!target.innerHTML) target.innerHTML = `<div class="item muted">${t("chat.noFiles")}</div>`;
  } catch (error) {
    target.innerHTML = `<div class="item muted">${escapeHTML(error.message)}</div>`;
  }
}

function appendMessage(messages, text, type) {
  const div = document.createElement("div");
  div.className = `message ${type || "assistant"}`;
  div.innerHTML = escapeHTML(text || "");
  messages.appendChild(div);
  messages.scrollTop = messages.scrollHeight;
}

function appendOrUpdateAssistant(messages, text) {
  let node = messages.querySelector(".message.assistant.streaming");
  if (!node) {
    node = document.createElement("div");
    node.className = "message assistant streaming";
    messages.appendChild(node);
  }
  node.innerHTML = escapeHTML(text || "");
  messages.scrollTop = messages.scrollHeight;
}
