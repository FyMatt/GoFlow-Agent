import { escapeHTML, request } from "../api.js";
import { currentLanguage, localizedText, t } from "../i18n.js";

export async function renderWorkspace(root, runtime, refreshRuntime) {
  const workspace = runtime.workspace || {};
  const capabilities = runtime.workspace_capabilities || workspace.capabilities || {};
  const actions = workspaceActionsFromRuntime(runtime, workspace, capabilities);
  const confirmed = Boolean(workspace.confirmed);
  const confirmAvailable = workspaceActionAvailable(actions, "confirm", !confirmed && Boolean(workspace.root));
  const clearAvailable = workspaceActionAvailable(actions, "clear", confirmed && Boolean(workspace.root));
  root.innerHTML = `
    <div class="grid workspace-grid">
      <section class="panel span-7 workspace-boundary" data-tour-id="workspace-boundary">
        <div class="workspace-copy">
          <p class="eyebrow">${t("workspace.boundaryEyebrow")}</p>
          <h2>${t("workspace.title")}</h2>
          <p class="muted">${t("workspace.boundaryHelp")}</p>
        </div>
        <div class="workspace-root-box">
          <span>${t("workspace.root")}</span>
          <code>${escapeHTML(workspace.root || t("common.none"))}</code>
        </div>
        <div class="workspace-meta-grid">
          <div>
            <span>${t("workspace.status")}</span>
            ${badge(confirmed ? t("workspace.confirmed") : t("workspace.needsConfirmation"), confirmed ? "good" : "warn")}
          </div>
          <div>
            <span>${t("workspace.source")}</span>
            <strong>${escapeHTML(workspace.source || "-")}</strong>
          </div>
        </div>
        ${renderWorkspaceCapabilitySummary(capabilities, actions)}
        ${renderWorkspaceSafetySummary(confirmed)}
        <div class="workspace-actions">
          <button id="confirm" class="primary" data-tour-id="workspace-confirm" data-workspace-action-name="confirm" data-workspace-available="${confirmAvailable ? "true" : "false"}" aria-disabled="${confirmAvailable ? "false" : "true"}"${confirmAvailable ? "" : " disabled"}>${workspaceActionLabel(workspaceActionForName(actions, "confirm") || "confirm")}</button>
          <button id="clear" data-workspace-action-name="clear" data-workspace-available="${clearAvailable ? "true" : "false"}" aria-disabled="${clearAvailable ? "false" : "true"}"${clearAvailable ? "" : " disabled"}>${workspaceActionLabel(workspaceActionForName(actions, "clear") || "clear")}</button>
        </div>
      </section>
      <section class="panel span-5 workspace-switcher" data-tour-id="workspace-switcher">
        <div class="workspace-copy">
          <p class="eyebrow">${t("workspace.switchEyebrow")}</p>
          <h2>${t("workspace.selectTitle")}</h2>
          <p class="muted">${t("workspace.selectHelp")}</p>
        </div>
        <label class="compact-field">
          <span>${t("workspace.targetPath")}</span>
          <input id="workspacePath" placeholder="${escapeHTML(t("workspace.pathPlaceholder"))}">
        </label>
        <div id="workspaceSelectHint" class="workspace-select-hint">${renderWorkspaceSelectHint(workspace, "")}</div>
        <div class="workspace-actions">
          <button id="select" class="primary" data-workspace-action-name="select" aria-disabled="false">${t("workspace.prepareRestart")}</button>
        </div>
        <div class="workspace-output-head">
          <strong>${t("workspace.outputTitle")}</strong>
          <span>${t("workspace.outputHelp")}</span>
        </div>
        <div id="workspaceOutput" class="workspace-output" role="status" aria-live="polite">
          <div class="workspace-output-empty">${escapeHTML(t("workspace.outputEmpty"))}</div>
        </div>
      </section>
    </div>`;

  const output = root.querySelector("#workspaceOutput");
  const workspacePath = root.querySelector("#workspacePath");
  workspacePath?.addEventListener("input", () => {
    const hint = root.querySelector("#workspaceSelectHint");
    if (hint) hint.innerHTML = renderWorkspaceSelectHint(workspace, workspacePath.value);
  });
  root.addEventListener("click", event => {
    const actionButton = event.target.closest("[data-workspace-action-name]");
    if (actionButton) {
      event.preventDefault();
      void handleWorkspaceAction(actionButton.dataset.workspaceActionName || "");
      return;
    }
    const selectButton = event.target.closest("[data-workspace-select-action]");
    if (selectButton) {
      event.preventDefault();
      void handleWorkspaceAction("select");
    }
  });
  root.querySelectorAll("[data-workspace-target]").forEach(button => {
    button.addEventListener("click", () => {
      const target = button.dataset.workspaceTarget;
      if (target) location.hash = target;
    });
  });

  async function handleWorkspaceAction(actionName) {
    if (actionName === "choose_folder") {
      const action = workspaceActionForName(actions, "choose_folder");
      if (action?.path && action.available !== false && !action.client_only) {
        setWorkspaceBusy(root, true);
        renderWorkspaceAction(output, { tone: "running", title: workspaceActionLabel(action), body: workspaceActionDescription(action) });
        try {
          const result = await workspaceActionRequest(action, action.path);
          const selected = result.normalized_path || result.path || result.selected_path || result.normalized_selected_path || "";
          if (selected && workspacePath) {
            workspacePath.value = selected;
            workspacePath.dispatchEvent(new Event("input", { bubbles: true }));
          }
          renderWorkspaceAction(output, {
            tone: result.cancelled ? "neutral" : selected ? "good" : "warn",
            title: result.cancelled ? t("workspace.actionReadyTitle") : workspaceActionLabel(action),
            body: workspaceDisplayValue(result.message || workspaceActionDescription(action)),
            result
          });
        } catch (error) {
          renderWorkspaceError(output, error);
        } finally {
          setWorkspaceBusy(root, false);
        }
        return;
      }
      openWorkspacePathEntry(workspace, workspacePath);
      return;
    }
    if (actionName === "confirm") {
      const action = workspaceActionForName(actions, "confirm");
      setWorkspaceBusy(root, true);
      renderWorkspaceAction(output, { tone: "running", title: t("workspace.confirmingTitle"), body: t("workspace.confirmingBody") });
      try {
        const result = await workspaceActionRequest(action, "/api/workspace/confirm");
        renderWorkspaceAction(output, {
          tone: "good",
          title: t("workspace.confirmedTitle"),
          body: t("workspace.confirmedBody"),
          result
        });
        await refreshRuntime();
      } catch (error) {
        renderWorkspaceError(output, error);
      } finally {
        setWorkspaceBusy(root, false);
      }
      return;
    }
    if (actionName === "clear") {
      const action = workspaceActionForName(actions, "clear");
      setWorkspaceBusy(root, true);
      renderWorkspaceAction(output, { tone: "running", title: t("workspace.clearingTitle"), body: t("workspace.clearingBody") });
      try {
        const result = await workspaceActionRequest(action, "/api/workspace/clear");
        renderWorkspaceAction(output, {
          tone: "warn",
          title: t("workspace.clearedTitle"),
          body: t("workspace.clearedBody"),
          result
        });
        await refreshRuntime();
      } catch (error) {
        renderWorkspaceError(output, error);
      } finally {
        setWorkspaceBusy(root, false);
      }
      return;
    }
    if (actionName === "select") {
      const path = workspacePath.value.trim();
      if (!path) {
        renderWorkspaceAction(output, {
          tone: "bad",
          title: t("workspace.actionFailedTitle"),
          body: t("workspace.pathRequired")
        });
        workspacePath.focus();
        return;
      }
      setWorkspaceBusy(root, true);
      renderWorkspaceAction(output, { tone: "running", title: t("workspace.preparingTitle"), body: t("workspace.preparingBody") });
      try {
        const action = workspaceSelectAction(actions, path, workspace);
        const result = await workspaceActionRequest(action, "/api/workspace/select", { path });
        renderWorkspaceAction(output, {
          tone: result.restart_required ? "warn" : "good",
          title: result.restart_required ? t("workspace.restartNeededTitle") : t("workspace.confirmedTitle"),
          body: result.restart_required ? t("workspace.restartNeededBody") : t("workspace.confirmedBody"),
          result
        });
      } catch (error) {
        const data = error.data || {};
        if (data.restart_required || Array.isArray(data.suggested_args)) {
          renderWorkspaceAction(output, {
            tone: "warn",
            title: t("workspace.restartNeededTitle"),
            body: workspaceDisplayValue(data.message || t("workspace.restartNeededBody")),
            result: data
          });
        } else {
          renderWorkspaceError(output, error);
        }
      } finally {
        setWorkspaceBusy(root, false);
      }
    }
  }
}

function badge(text, kind) {
  return `<span class="badge ${kind || ""}">${escapeHTML(text)}</span>`;
}

function renderWorkspaceSafetySummary(confirmed) {
  const nextTone = confirmed ? "good" : "warn";
  return `<div class="workspace-safety-panel">
    <div class="workspace-output-head">
      <strong>${t("workspace.safetyTitle")}</strong>
      <span>${t("workspace.safetyHelp")}</span>
    </div>
    <div class="workspace-safety-list">
      <article class="workspace-safety-card">
        <strong>${t("workspace.safetyFilesTitle")}</strong>
        <p>${t("workspace.safetyFilesBody")}</p>
      </article>
      <article class="workspace-safety-card">
        <strong>${t("workspace.safetyApprovalTitle")}</strong>
        <p>${t("workspace.safetyApprovalBody")}</p>
      </article>
    </div>
    <div class="workspace-next-step ${nextTone}">
      <div>
        <strong>${t(confirmed ? "workspace.nextReadyTitle" : "workspace.nextBlockedTitle")}</strong>
        <p>${t(confirmed ? "workspace.nextReadyBody" : "workspace.nextBlockedBody")}</p>
      </div>
      ${confirmed ? `<div class="workspace-next-actions">
        <button type="button" data-workspace-target="playground">${t("workspace.openRun")}</button>
        <button type="button" data-workspace-target="workflows">${t("workspace.openWorkflows")}</button>
      </div>` : ""}
    </div>
  </div>`;
}

function renderWorkspaceCapabilitySummary(capabilities = {}, actions = []) {
  const facts = workspaceCapabilityFacts(capabilities);
  return `<div class="workspace-capabilities-panel">
    <div class="workspace-output-head">
      <strong>${t("workspace.capabilitiesTitle")}</strong>
      <span>${t("workspace.capabilitiesHelp")}</span>
    </div>
    <div class="workspace-capability-grid">
      ${facts.map(renderWorkspaceCapabilityFact).join("")}
    </div>
    ${actions.length ? `<div class="workspace-action-hints">${actions.map(renderWorkspaceActionHint).join("")}</div>` : ""}
    ${capabilities.reason ? `<p class="workspace-capability-reason">${escapeHTML(workspaceCapabilityReason(capabilities.reason))}</p>` : ""}
  </div>`;
}

function workspaceCapabilityFacts(capabilities = {}) {
  return [
    {
      label: t("workspace.capabilityPureChat"),
      value: capabilities.pure_chat_without_workspace,
      help: t("workspace.capabilityPureChatHelp")
    },
    {
      label: t("workspace.capabilityFileTasks"),
      value: capabilities.workspace_required_for_file_tasks,
      help: t("workspace.capabilityFileTasksHelp")
    },
    {
      label: t("workspace.capabilityConfirm"),
      value: capabilities.can_confirm_current_workspace,
      help: t("workspace.capabilityConfirmHelp")
    },
    {
      label: t("workspace.capabilityClear"),
      value: capabilities.can_clear_confirmation,
      help: t("workspace.capabilityClearHelp")
    },
    {
      label: t("workspace.capabilitySwitch"),
      value: capabilities.can_switch_workspace_without_restart,
      help: capabilities.switch_requires_restart ? t("workspace.capabilitySwitchRestartHelp") : t("workspace.capabilitySwitchHelp")
    }
  ];
}

function renderWorkspaceCapabilityFact(item) {
  const enabled = Boolean(item.value);
  return `<article class="workspace-capability-card ${enabled ? "good" : "neutral"}">
    <div>
      <strong>${escapeHTML(item.label)}</strong>
      ${badge(enabled ? t("common.on") : t("common.off"), enabled ? "good" : "neutral")}
    </div>
    <p>${escapeHTML(item.help)}</p>
  </article>`;
}

function renderWorkspaceActionHint(action = {}) {
  const available = action.available !== false;
  const restart = Boolean(action.restart_required);
  const tone = available ? restart ? "warn" : "good" : "neutral";
  const meta = workspaceActionMeta(action);
  const button = workspaceActionCTA(action, available);
  return `<article class="workspace-action-hint ${tone}">
    <div class="workspace-action-hint-head">
      <strong>${escapeHTML(workspaceActionLabel(action))}</strong>
      ${badge(available ? restart ? t("workspace.restartRequiredLabel") : t("workspace.actionAvailable") : t("workspace.actionUnavailable"), tone)}
    </div>
    <p>${escapeHTML(workspaceActionDescription(action))}</p>
    ${meta.length ? `<div class="workspace-action-hint-meta">${meta.join("")}</div>` : ""}
    ${button}
  </article>`;
}

function workspaceActionCTA(action = {}, available = false) {
  const name = String(action.name || "");
  if (action.client_only || name === "choose_folder") {
    return `<button type="button" class="workspace-action-cta" data-workspace-action-name="choose_folder" data-workspace-available="${available ? "true" : "false"}" aria-disabled="${available ? "false" : "true"}"${available ? "" : " disabled"}>${escapeHTML(t("workspace.action.choose_folder.button"))}</button>`;
  }
  if (name === "confirm" || name === "clear") {
    return `<button type="button" class="workspace-action-cta" data-workspace-action-name="${escapeHTML(name)}" data-workspace-available="${available ? "true" : "false"}" aria-disabled="${available ? "false" : "true"}"${available ? "" : " disabled"}>${escapeHTML(workspaceActionLabel(action))}</button>`;
  }
  return "";
}

function renderWorkspaceSelectHint(workspace = {}, path = "") {
  const value = String(path || "").trim();
  if (!value) {
    return `<div class="workspace-action-hint neutral">
      <strong>${escapeHTML(t("workspace.selectHintTitle"))}</strong>
      <p>${escapeHTML(t("workspace.selectHintBody"))}</p>
    </div>`;
  }
  const same = sameWorkspacePath(value, workspace.root);
  return `<div class="workspace-action-hint ${same ? "good" : "warn"}">
    <strong>${escapeHTML(same ? t("workspace.selectSameTitle") : t("workspace.selectSwitchTitle"))}</strong>
    <p>${escapeHTML(same ? t("workspace.selectSameBody") : t("workspace.selectSwitchBody"))}</p>
    <button type="button" class="workspace-action-cta" data-workspace-select-action="select" aria-disabled="false">${escapeHTML(same ? t("workspace.confirm") : t("workspace.prepareRestart"))}</button>
  </div>`;
}

function setWorkspaceBusy(root, busy) {
  root.querySelectorAll("[data-workspace-action-name], [data-workspace-select-action]").forEach(button => {
    if (busy) {
      if (!button.dataset.workspaceBusyPreviousDisabled) {
        button.dataset.workspaceBusyPreviousDisabled = button.disabled ? "true" : "false";
      }
      button.disabled = true;
      button.setAttribute("aria-disabled", "true");
      button.setAttribute("aria-busy", "true");
      return;
    }
    const unavailable = button.dataset.workspaceAvailable === "false";
    const hasPrevious = Object.prototype.hasOwnProperty.call(button.dataset, "workspaceBusyPreviousDisabled");
    const wasDisabled = hasPrevious ? button.dataset.workspaceBusyPreviousDisabled === "true" : button.disabled;
    delete button.dataset.workspaceBusyPreviousDisabled;
    const disabled = unavailable || wasDisabled;
    button.disabled = disabled;
    button.setAttribute("aria-disabled", disabled ? "true" : "false");
    button.setAttribute("aria-busy", "false");
  });
  const workspacePath = root.querySelector("#workspacePath");
  if (!workspacePath) return;
  if (busy) {
    if (!workspacePath.dataset.workspaceBusyPreviousDisabled) {
      workspacePath.dataset.workspaceBusyPreviousDisabled = workspacePath.disabled ? "true" : "false";
    }
    workspacePath.disabled = true;
    workspacePath.setAttribute("aria-disabled", "true");
    return;
  }
  const wasPathDisabled = workspacePath.dataset.workspaceBusyPreviousDisabled === "true";
  delete workspacePath.dataset.workspaceBusyPreviousDisabled;
  workspacePath.disabled = wasPathDisabled;
  workspacePath.setAttribute("aria-disabled", wasPathDisabled ? "true" : "false");
}

function renderWorkspaceAction(node, options = {}) {
  if (!node) return;
  const result = options.result || {};
  const facts = workspaceFacts(result);
  node.innerHTML = `
    <div class="workspace-action-result ${escapeHTML(options.tone || "neutral")}">
      <div class="workspace-action-status">
        <span class="workspace-action-dot" aria-hidden="true"></span>
        <div>
          <strong>${escapeHTML(workspaceDisplayValue(options.title || result.message || t("workspace.actionReadyTitle")))}</strong>
          <p>${escapeHTML(workspaceDisplayValue(options.body || result.message || t("workspace.actionReadyBody")))}</p>
        </div>
      </div>
      ${facts.length ? `<div class="workspace-action-facts">${facts.map(renderWorkspaceFact).join("")}</div>` : ""}
      ${Array.isArray(result.suggested_args) && result.suggested_args.length ? `<div class="workspace-restart-command"><span>${escapeHTML(t("workspace.restartArgs"))}</span><code>${escapeHTML(result.suggested_args.join(" "))}</code></div>` : ""}
    </div>
  `;
}

function renderWorkspaceError(node, error) {
  const data = error?.data || {};
  renderWorkspaceAction(node, {
    tone: "bad",
    title: t("workspace.actionFailedTitle"),
    body: workspaceDisplayValue(data.message || data.error || error?.message || t("common.errorTitle")),
    result: data
  });
}

function workspaceFacts(result = {}) {
  return [
    [t("workspace.root"), result.root || result.workspace?.root],
    [t("workspace.status"), result.status || result.workspace?.status],
    [t("workspace.source"), result.source || result.workspace?.source],
    [t("workspace.restartRequiredLabel"), result.restart_required ? t("common.on") : ""]
  ].filter(([, value]) => String(value || "").trim()).map(([label, value]) => ({ label, value }));
}

function renderWorkspaceFact(item) {
  return `<span class="workspace-action-fact"><small>${escapeHTML(item.label)}</small><strong>${escapeHTML(workspaceDisplayValue(item.value))}</strong></span>`;
}

function workspaceActionsFromRuntime(runtime = {}, workspace = {}, capabilities = {}) {
  const sources = [
    capabilities.actions,
    runtime.workspace_actions,
    workspace.actions
  ];
  for (const source of sources) {
    if (Array.isArray(source) && source.length) return source;
  }
  return [];
}

function workspaceActionForName(actions = [], name = "") {
  return actions.find(item => item?.name === name) || null;
}

function workspaceActionAvailable(actions = [], name, fallback = false) {
  const action = workspaceActionForName(actions, name);
  return action ? action.available !== false : fallback;
}

function workspaceSelectAction(actions = [], path = "", workspace = {}) {
  const same = sameWorkspacePath(path, workspace.root);
  return workspaceActionForName(actions, same ? "select_same" : "switch")
    || workspaceActionForName(actions, "select")
    || null;
}

function workspaceActionRequest(action = {}, fallbackPath = "", body = null) {
  const path = action?.path || fallbackPath;
  const method = String(action?.method || "POST").toUpperCase();
  const options = { method };
  if (body && method !== "GET" && method !== "HEAD") {
    options.headers = { "Content-Type": "application/json" };
    options.body = JSON.stringify(body);
  }
  return request(path, options);
}

function workspaceActionLabel(actionOrName) {
  const name = typeof actionOrName === "object" ? actionOrName?.name : actionOrName;
  const key = `workspace.action.${name}.label`;
  const translated = t(key);
  if (translated !== key) return translated;
  const raw = typeof actionOrName === "object"
    ? String(actionOrName?.label || actionOrName?.title || name || "").trim()
    : String(name || "").trim();
  const localized = localizedText(raw);
  if (localized && localized !== raw) return localized;
  if (currentLanguage() !== "zh") return raw || "-";
  return workspaceGeneratedActionLabel(name) || raw || "-";
}

function workspaceActionDescription(action = {}) {
  const key = `workspace.action.${action.name}.description`;
  const translated = t(key);
  if (translated !== key) return translated;
  const description = workspaceDisplayValue(action.description || "");
  if (description) return description;
  const label = workspaceActionLabel(action);
  return currentLanguage() === "zh" && label && label !== "-"
    ? `执行“${label}”操作。`
    : "";
}

function workspaceActionMeta(action = {}) {
  const chips = [];
  if (action.client_only) {
    chips.push(badge(t("workspace.clientOnlyAction"), "neutral"));
  }
  if (action.method) {
    chips.push(badge(t("workspace.actionMethod", { value: action.method.toUpperCase() }), "neutral"));
  }
  if (action.path) {
    chips.push(badge(t("workspace.actionPath", { value: action.path }), "neutral"));
  }
  if (action.follow_up_action) {
    chips.push(badge(t("workspace.actionFollowUp", { value: workspaceActionLabel(action.follow_up_action) }), "neutral"));
  }
  return chips;
}

function openWorkspacePathEntry(workspace = {}, workspacePath) {
  if (workspacePath && !workspacePath.value && workspace.root) {
    workspacePath.value = workspace.root;
    workspacePath.dispatchEvent(new Event("input", { bubbles: true }));
  }
  const switcher = workspacePath?.closest?.(".workspace-switcher");
  switcher?.scrollIntoView?.({ behavior: prefersReducedMotion() ? "auto" : "smooth", block: "start" });
  workspacePath?.focus?.();
  workspacePath?.select?.();
}

function prefersReducedMotion() {
  return typeof window !== "undefined"
    && window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
}

function workspaceCapabilityReason(reason) {
  const value = String(reason || "");
  if (value.includes("switch workspaces by restarting")) return t("workspace.capabilityRestartReason");
  return workspaceDisplayValue(value);
}

function sameWorkspacePath(a, b) {
  const normalize = value => String(value || "").trim().replace(/\\/g, "/").replace(/\/+$/g, "").toLowerCase();
  return normalize(a) && normalize(a) === normalize(b);
}

function workspaceDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return text;
  if (text.includes("/") || text.includes("\\") || text.includes("://")) return text;
  if (text.includes(".") && !/\s/.test(text)) return text;
  return localizedText(text);
}

function workspaceGeneratedActionLabel(name) {
  const tokens = String(name || "").trim().toLowerCase().replaceAll("-", "_").split("_").filter(Boolean);
  if (!tokens.length) return "";
  const verbMap = {
    activate: "启用",
    apply: "应用",
    choose: "选择",
    clear: "清除",
    confirm: "确认",
    create: "创建",
    delete: "删除",
    disable: "禁用",
    enable: "启用",
    inspect: "查看",
    open: "打开",
    prepare: "准备",
    refresh: "刷新",
    reset: "重置",
    restart: "重启",
    run: "运行",
    save: "保存",
    select: "选择",
    switch: "切换",
    update: "更新",
    validate: "验证"
  };
  const nounMap = {
    action: "操作",
    confirmation: "确认状态",
    current: "当前",
    folder: "文件夹",
    path: "路径",
    root: "根目录",
    safe: "安全状态",
    status: "状态",
    workspace: "工作区"
  };
  const verbIndex = tokens.findIndex(token => verbMap[token]);
  const verb = verbIndex >= 0 ? verbMap[tokens[verbIndex]] : "";
  const rest = tokens
    .filter((_, index) => index !== verbIndex)
    .map(token => nounMap[token] || localizedText(token))
    .filter(Boolean);
  if (verb && rest.length) return `${verb}${rest.join("")}`;
  if (verb) return `${verb}工作区`;
  return rest.join("") || "";
}
