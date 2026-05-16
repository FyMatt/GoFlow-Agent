import {
  compactSessionContext,
  escapeHTML,
  fetchArtifactObject,
  fetchArtifactObjects,
  fetchMemoryDashboard,
  rebuildMemoryIndex,
  renderSafeMarkdown,
  searchMemory,
  updateMemoryProject
} from "../api.js";
import { localizedText, t } from "../i18n.js";

export async function renderMemory(root) {
  root.innerHTML = `<div class="panel loading">${escapeHTML(t("common.loading"))}</div>`;
  const state = {
    dashboard: await fetchMemoryDashboard({ tasks: 12 }),
    artifacts: await fetchArtifactObjects({ limit: 8 }).catch(() => ({ objects: [] })),
    search: null,
    artifact: null,
    searchQuery: "",
    artifactRef: "",
    artifactAttached: false,
    artifactWorkflowAttached: false,
    copiedArtifactRef: false,
    busy: false,
    busyAction: "",
    error: ""
  };
  drawMemory(root, state);
}

function drawMemory(root, state) {
  const dashboard = state.dashboard || {};
  const project = dashboard.project || {};
  const tasks = Array.isArray(dashboard.tasks) ? dashboard.tasks : [];
  const errors = Array.isArray(dashboard.errors?.errors) ? dashboard.errors.errors : [];
  const files = Array.isArray(dashboard.file_index?.files) ? dashboard.file_index.files : [];
  const context = dashboard.context || {};
  const stats = [
    [t("memory.metric.tasks"), tasks.length],
    [t("memory.metric.errors"), errors.length],
    [t("memory.metric.files"), dashboard.file_index?.total_files || files.length],
    [t("memory.metric.bytes"), formatNumber(dashboard.file_index?.indexed_bytes || 0)],
    [t("memory.metric.contextSaved"), formatNumber(context.estimated_saved_tokens || context.estimatedSavedTokens || 0)]
  ];
  root.innerHTML = `
    <section class="memory-view">
      <div class="memory-toolbar panel">
        <div class="memory-toolbar-copy">
          <h2>${escapeHTML(t("memory.title"))}</h2>
          <p>${escapeHTML(t("memory.copy"))}</p>
        </div>
        <form data-memory-search class="memory-search">
          <label class="sr-only" for="memorySearch">${escapeHTML(t("memory.search"))}</label>
          <input id="memorySearch" name="q" type="search" value="${escapeHTML(state.searchQuery || "")}" placeholder="${escapeHTML(t("memory.searchPlaceholder"))}">
          <button type="submit" class="primary" ${state.busy ? "disabled" : ""} aria-busy="${isBusyAction(state, "search") ? "true" : "false"}">${escapeHTML(actionLabel(state, "search", "memory.search", "memory.searching"))}</button>
        </form>
        <button type="button" class="memory-compact-button" data-memory-compact ${state.busy ? "disabled" : ""} aria-busy="${isBusyAction(state, "compact") ? "true" : "false"}">
          <span class="memory-compact-icon" aria-hidden="true"></span>
          <span>
            <strong>${escapeHTML(actionLabel(state, "compact", "memory.compactNow", "memory.compacting"))}</strong>
            <small>${escapeHTML(t("memory.compactNowHelp"))}</small>
          </span>
        </button>
      </div>

      ${state.error ? `<div class="notice danger">${escapeHTML(state.error)}</div>` : ""}

      <div class="memory-primer">
        ${renderPrimerCard(t("memory.primer.projectTitle"), t("memory.primer.projectBody"))}
        ${renderPrimerCard(t("memory.primer.tasksTitle"), t("memory.primer.tasksBody"))}
        ${renderPrimerCard(t("memory.primer.artifactTitle"), t("memory.primer.artifactBody"))}
      </div>

      <div class="memory-metrics">
        ${stats.map(([label, value], index) => `
          <article class="metric-card memory-metric memory-metric-${index + 1}">
            <span>${escapeHTML(label)}</span>
            <strong>${escapeHTML(value)}</strong>
          </article>`).join("")}
      </div>

      ${renderContextSummary(context, state)}

      <div class="memory-grid">
        <section class="panel memory-project-panel">
          <div class="section-heading memory-panel-head">
            <div>
              <h3>${escapeHTML(t("memory.project"))}</h3>
              <p>${escapeHTML(project.path || ".goflow/memory/project.md")}</p>
            </div>
            <button type="button" class="primary" data-memory-save ${state.busy ? "disabled" : ""} aria-busy="${isBusyAction(state, "save") ? "true" : "false"}">${escapeHTML(actionLabel(state, "save", "common.save", "memory.saving"))}</button>
          </div>
          <div class="memory-project-summary">
            <div class="memory-inline-label">${escapeHTML(t("memory.projectSummary"))}</div>
            <p>${escapeHTML(localizeMemoryText(project.summary || t("memory.projectSummaryHelp")))}</p>
            ${(project.updated_at || project.updatedAt) ? `<small>${escapeHTML(t("memory.projectUpdated", { time: formatDateTime(project.updated_at || project.updatedAt) }))}</small>` : ""}
          </div>
          <div class="memory-project-effect">
            <strong>${escapeHTML(t("memory.projectEffectTitle"))}</strong>
            <p>${escapeHTML(t("memory.projectEffectBody"))}</p>
          </div>
          <details class="memory-project-editor">
            <summary>
              <span>
                <strong>${escapeHTML(t("memory.projectEditorTitle"))}</strong>
                <small>${escapeHTML(t("memory.projectEditorHelp"))}</small>
              </span>
            </summary>
            <textarea data-memory-project rows="16" spellcheck="false">${escapeHTML(project.content || "")}</textarea>
          </details>
        </section>

        <section class="panel memory-results-panel">
          <div class="section-heading memory-panel-head">
            <div>
              <h3>${escapeHTML(t("memory.searchResults"))}</h3>
              <p>${escapeHTML(searchSummaryText(state.search))}</p>
            </div>
          </div>
          ${renderSearchResults(state.search)}
        </section>
      </div>

      <div class="memory-grid memory-grid-three">
        <section class="panel">
          <div class="section-heading memory-panel-head">
            <div>
              <h3>${escapeHTML(t("memory.tasks"))}</h3>
              <p>${escapeHTML(t("memory.tasksHelp"))}</p>
            </div>
          </div>
          ${renderTasks(tasks)}
        </section>

        <section class="panel">
          <div class="section-heading memory-panel-head">
            <div>
              <h3>${escapeHTML(t("memory.errors"))}</h3>
              <p>${escapeHTML(t("memory.errorsHelp"))}</p>
            </div>
          </div>
          ${renderErrors(errors)}
        </section>

        <section class="panel">
          <div class="section-heading memory-panel-head">
            <div>
              <h3>${escapeHTML(t("memory.files"))}</h3>
              <p>${escapeHTML(t("memory.filesHelp"))}</p>
            </div>
            <button type="button" data-memory-rebuild ${state.busy ? "disabled" : ""} aria-busy="${isBusyAction(state, "rebuild") ? "true" : "false"}">${escapeHTML(actionLabel(state, "rebuild", "memory.rebuild", "memory.rebuilding"))}</button>
          </div>
          ${renderFiles(files)}
        </section>
      </div>

      <section class="panel memory-artifact-panel">
        <div class="section-heading memory-panel-head">
          <div>
            <h3>${escapeHTML(t("memory.artifactViewer"))}</h3>
            <p>${escapeHTML(t("memory.artifactHelp"))}</p>
          </div>
        </div>
        <div class="memory-artifact-ref-note">
          <strong>${escapeHTML(t("memory.artifactRefUsageTitle"))}</strong>
          <p>${escapeHTML(t("memory.artifactRefUsageBody"))}</p>
        </div>
        <div class="memory-artifact-intro">
          ${renderPrimerCard(t("memory.artifactPersistent"), t("memory.artifactPersistentHelp"))}
          ${renderPrimerCard(t("memory.artifactLazyTitle"), t("memory.artifactLazyBody"))}
        </div>
        <div class="memory-artifact-workbench">
          <div class="memory-artifact-rail">
        <form data-artifact-load class="memory-artifact-form">
          <div class="memory-artifact-field">
            <label for="artifactRef">${escapeHTML(t("memory.artifactRef"))}</label>
            <div class="memory-artifact-input-row">
              <input id="artifactRef" name="ref" value="${escapeHTML(state.artifactRef || "")}" placeholder="sha256:...">
              <button type="submit" class="primary memory-artifact-load-button" ${state.busy ? "disabled" : ""} aria-busy="${isBusyAction(state, "artifact") ? "true" : "false"}">
                <span class="memory-artifact-load-icon" aria-hidden="true">
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h9l5 5v11H5V4Zm8 2H7v12h10v-8h-4V6Zm1.8 9.2 2.7-2.7 1.4 1.4-5.1 5.1-5.1-5.1 1.4-1.4 2.7 2.7V9h2v6.2Z"/></svg>
                </span>
                <span class="memory-artifact-load-copy">
                  <strong>${escapeHTML(actionLabel(state, "artifact", "memory.loadArtifact", "memory.artifactLoading"))}</strong>
                  <small>${escapeHTML(isBusyAction(state, "artifact") ? t("memory.artifactLoadingHint") : t("memory.artifactLoadHint"))}</small>
                </span>
              </button>
            </div>
          </div>
        </form>
            ${renderArtifactIndex(state.artifacts, state)}
          </div>
          <div class="memory-artifact-stage">
            ${renderArtifact(state.artifact, state)}
          </div>
        </div>
      </section>
    </section>`;

  root.querySelector("[data-memory-search]")?.addEventListener("submit", async event => {
    event.preventDefault();
    const query = String(new FormData(event.currentTarget).get("q") || "").trim();
    state.searchQuery = query;
    await runMemoryAction(root, state, async () => {
      state.search = await searchMemory(query, { limit: 20 });
    }, "search");
  });
  root.querySelector("[data-memory-save]")?.addEventListener("click", async () => {
    const content = root.querySelector("[data-memory-project]")?.value ?? (state.dashboard.project?.content || "");
    await runMemoryAction(root, state, async () => {
      state.dashboard.project = await updateMemoryProject(content);
    }, "save");
  });
  root.querySelector("[data-memory-rebuild]")?.addEventListener("click", async () => {
    await runMemoryAction(root, state, async () => {
      state.dashboard.file_index = await rebuildMemoryIndex();
    }, "rebuild");
  });
  root.querySelectorAll("[data-memory-empty-action='rebuild']").forEach(button => {
    button.addEventListener("click", async () => {
      await runMemoryAction(root, state, async () => {
        state.dashboard.file_index = await rebuildMemoryIndex();
      }, "rebuild");
    });
  });
  root.querySelectorAll("[data-memory-target]").forEach(button => {
    button.addEventListener("click", () => {
      const target = button.dataset.memoryTarget || "";
      if (target) location.hash = target;
    });
  });
  root.querySelector("[data-memory-compact]")?.addEventListener("click", async () => {
    await runMemoryAction(root, state, async () => {
      state.dashboard.context = await compactSessionContext(t("memory.compactManualReason"));
    }, "compact");
  });
  root.querySelector("[data-artifact-load]")?.addEventListener("submit", async event => {
    event.preventDefault();
    const ref = String(new FormData(event.currentTarget).get("ref") || "").trim();
    state.artifactRef = ref;
    if (!ref) {
      state.error = t("memory.artifactRefRequired");
      drawMemory(root, state);
      return;
    }
    await runMemoryAction(root, state, async () => {
      state.artifact = await fetchArtifactObject(ref, { content: true });
    }, "artifact");
  });
  root.querySelector("[data-artifact-copy]")?.addEventListener("click", async event => {
    const ref = event.currentTarget?.dataset?.artifactCopy || "";
    if (!ref) return;
    try {
      await navigator.clipboard.writeText(ref);
      state.copiedArtifactRef = true;
      drawMemory(root, state);
      window.setTimeout(() => {
        state.copiedArtifactRef = false;
        drawMemory(root, state);
      }, 1400);
    } catch (error) {
      state.error = error?.message || String(error);
      drawMemory(root, state);
    }
  });
  root.querySelectorAll("[data-artifact-open]").forEach(button => {
    button.addEventListener("click", async event => {
      const ref = event.currentTarget?.dataset?.artifactOpen || "";
      if (!ref) return;
      state.artifactRef = ref;
      await runMemoryAction(root, state, async () => {
        state.artifact = await fetchArtifactObject(ref, { content: true });
      }, "artifact");
    });
  });
  root.querySelector("[data-artifact-attach]")?.addEventListener("click", event => {
    const ref = event.currentTarget?.dataset?.artifactAttach || "";
    if (!ref) return;
    localStorage.setItem("goflow.playground.draft", t("memory.artifactPromptDraft", { ref }));
    state.artifactAttached = true;
    drawMemory(root, state);
    window.setTimeout(() => {
      state.artifactAttached = false;
      location.hash = "playground";
    }, 260);
  });
  root.querySelector("[data-artifact-workflow]")?.addEventListener("click", event => {
    const ref = event.currentTarget?.dataset?.artifactWorkflow || "";
    if (!ref) return;
    localStorage.setItem("goflow.workflow.artifactDraft", JSON.stringify({
      ref,
      title: state.artifact?.title || state.artifact?.kind || t("memory.artifact"),
      summary: state.artifact?.summary || "",
      createdAt: new Date().toISOString()
    }));
    state.artifactWorkflowAttached = true;
    drawMemory(root, state);
    window.setTimeout(() => {
      state.artifactWorkflowAttached = false;
      location.hash = "workflows";
    }, 260);
  });
}

async function runMemoryAction(root, state, action, actionName = "") {
  state.busy = true;
  state.busyAction = actionName;
  state.error = "";
  drawMemory(root, state);
  try {
    await action();
  } catch (error) {
    state.error = localizedText(error?.message || String(error));
  } finally {
    state.busy = false;
    state.busyAction = "";
    drawMemory(root, state);
  }
}

function renderSearchResults(search) {
  const results = Array.isArray(search?.results) ? search.results : [];
  if (!results.length) return `<div class="empty-state">${escapeHTML(t("memory.searchEmpty"))}</div>`;
  return `<div class="memory-list">${results.map(result => renderMemoryItem({
    eyebrow: [memoryKindLabel(result.kind || "memory"), result.score ? `score ${result.score}` : ""].filter(Boolean).join(" / "),
    title: result.title || result.path || t("memory.item"),
    summary: result.summary,
    meta: result.path || result.metadata?.task_id || result.metadata?.error_id || ""
  })).join("")}</div>`;
}

function renderTasks(tasks) {
  if (!tasks.length) return renderMemoryActionEmpty(t("memory.tasksEmpty"), t("memory.tasksGenerationHint"), t("memory.openRun"), "playground");
  return `<div class="memory-list">${tasks.map(task => renderMemoryItem({
    eyebrow: localizedText(task.mode || task.agent_id || t("memory.task")),
    title: task.user_goal || task.id || t("memory.task"),
    summary: [
      ...(task.key_decisions || []),
      ...(task.test_results || []),
      ...(task.failure_reasons || []),
      ...(task.next_todos || [])
    ].join("\n"),
    meta: [(task.modified_files || []).join(", "), formatDateTime(task.updated_at || task.updatedAt)].filter(Boolean).join(" / ")
  })).join("")}</div>`;
}

function renderErrors(errors) {
  if (!errors.length) return renderMemoryActionEmpty(t("memory.errorsEmpty"), t("memory.errorsGenerationHint"), t("memory.openStatus"), "status");
  return `<div class="memory-list">${errors.map(item => renderMemoryItem({
    eyebrow: item.resolved ? t("memory.resolved") : t("memory.open"),
    title: item.error || item.id,
    summary: [
      item.root_cause ? `${t("memory.errorRootCause")}: ${item.root_cause}` : "",
      item.fix ? `${t("memory.errorFix")}: ${item.fix}` : "",
      item.verification_command ? `${t("memory.errorVerify")}: ${item.verification_command}` : ""
    ].filter(Boolean).join("\n"),
    meta: [(item.related_files || []).join(", "), formatDateTime(item.updated_at || item.updatedAt)].filter(Boolean).join(" / ")
  })).join("")}</div>`;
}

function renderContextSummary(context = {}, state = {}) {
  const hasContext = Boolean(context && (context.summary || context.id || context.updated_at || context.updatedAt));
  const saved = context.estimated_saved_tokens ?? context.estimatedSavedTokens ?? 0;
  const budget = context.prompt_budget || context.promptBudget || {};
  const sourceCounts = context.source_counts || context.sourceCounts || {};
  const facts = [
    [t("memory.contextAgent"), [context.active_agent || context.activeAgent, context.mode].filter(Boolean).join(" / ") || t("common.none")],
    [t("memory.contextSavedTokens"), formatNumber(saved)],
    [t("memory.contextPromptTokens"), formatNumber(budget.estimated_prompt_tokens ?? budget.estimatedPromptTokens ?? 0)],
    [t("memory.contextArtifacts"), formatNumber((context.artifact_refs || context.artifactRefs || []).length || budget.artifact_refs || budget.artifactRefs || 0)]
  ];
  const lists = [
    [t("memory.contextGoals"), context.recent_goals || context.recentGoals || []],
    [t("memory.contextDecisions"), context.decisions || []],
    [t("memory.contextPending"), context.pending_actions || context.pendingActions || []],
    [t("memory.contextFiles"), context.relevant_files || context.relevantFiles || []],
    [t("memory.contextArtifactRefs"), context.artifact_refs || context.artifactRefs || []]
  ];
  return `<section class="panel memory-context-panel ${hasContext ? "has-context" : "empty-context"}">
    <div class="section-heading memory-panel-head">
      <div>
        <h3>${escapeHTML(t("memory.contextTitle"))}</h3>
        <p>${escapeHTML(hasContext ? t("memory.contextHelp") : t("memory.contextEmptyHelp"))}</p>
      </div>
      <span class="badge ${context.auto ? "good" : "neutral"}">${escapeHTML(context.auto ? t("memory.contextAuto") : t("memory.contextManual"))}</span>
    </div>
    ${hasContext ? `<div class="memory-context-body">
      <div class="memory-context-summary">
        <strong>${escapeHTML(localizeMemoryText(context.summary || t("memory.contextNoSummary")))}</strong>
        <span>${escapeHTML(context.updated_at || context.updatedAt ? t("memory.contextUpdated", { time: formatDateTime(context.updated_at || context.updatedAt) }) : t("memory.contextNotUpdated"))}</span>
      </div>
      <div class="memory-context-facts">${facts.map(([label, value]) => `<div><span>${escapeHTML(label)}</span><strong>${escapeHTML(String(value))}</strong></div>`).join("")}</div>
      <div class="memory-context-lists">
        ${lists.map(([label, values]) => renderContextList(label, values)).join("")}
      </div>
      ${Object.keys(sourceCounts).length ? `<div class="memory-context-source-counts">${Object.entries(sourceCounts).slice(0, 6).map(([key, value]) => `<span><b>${escapeHTML(memorySourceLabel(key))}</b>${escapeHTML(formatNumber(value))}</span>`).join("")}</div>` : ""}
    </div>` : `<div class="empty-state"><div class="memory-empty-copy"><strong>${escapeHTML(t("memory.contextEmpty"))}</strong><p>${escapeHTML(t("memory.contextEmptyAction"))}</p></div></div>`}
  </section>`;
}

function memorySourceLabel(key) {
  const normalized = String(key || "").trim().toLowerCase().replace(/[\s-]+/g, "_");
  const map = {
    agent_runs: "memory.source.agentRuns",
    workflow_runs: "memory.source.workflowRuns",
    artifact_refs: "memory.source.artifactRefs",
    pending_approvals: "memory.source.pendingApprovals",
    prompt_budget_rows: "memory.source.promptBudgetRows",
    recent_prompts: "memory.source.recentPrompts",
    recent_tools: "memory.source.recentTools"
  };
  const mapped = map[normalized];
  return mapped ? t(mapped) : localizedText(normalized || key);
}

function renderContextList(label, values) {
  const items = Array.isArray(values) ? values.filter(Boolean).slice(0, 5) : [];
  return `<section>
    <strong>${escapeHTML(label)}</strong>
    ${items.length ? `<div>${items.map(item => `<span>${escapeHTML(localizeMemoryText(item))}</span>`).join("")}</div>` : `<p>${escapeHTML(t("memory.contextListEmpty"))}</p>`}
  </section>`;
}

function renderFiles(files) {
  if (!files.length) return renderMemoryActionEmpty(t("memory.filesEmpty"), t("memory.filesHelp"), t("memory.rebuild"), "", "rebuild");
  return `<div class="memory-list memory-file-list">${files.slice(0, 40).map(file => renderMemoryItem({
    eyebrow: [file.language, `${formatNumber(file.size || 0)} B`].filter(Boolean).join(" / "),
    title: file.path,
    summary: file.summary,
    meta: (file.symbols || []).slice(0, 8).join(", ")
  })).join("")}</div>`;
}

function renderMemoryActionEmpty(title, body, actionLabel = "", target = "", action = "") {
  return `<div class="empty-state memory-action-empty">
    <div class="memory-empty-copy">
      <strong>${escapeHTML(title)}</strong>
      <p>${escapeHTML(body)}</p>
    </div>
    ${actionLabel ? `<button type="button" class="memory-empty-action" ${target ? `data-memory-target="${escapeHTML(target)}"` : ""} ${action ? `data-memory-empty-action="${escapeHTML(action)}"` : ""}>${escapeHTML(actionLabel)}</button>` : ""}
  </div>`;
}

function renderArtifact(artifact, state = {}) {
  if (isBusyAction(state, "artifact")) {
    return `<div class="memory-artifact-loading" role="status" aria-live="polite">
      <span class="memory-artifact-spinner" aria-hidden="true"></span>
      <div>
        <strong>${escapeHTML(t("memory.artifactLoading"))}</strong>
        <p>${escapeHTML(state.artifactRef || "sha256:...")}</p>
      </div>
    </div>`;
  }
  if (!artifact) {
    return `<div class="memory-artifact-empty" role="status">
      <span class="memory-artifact-empty-mark" aria-hidden="true"></span>
      <div>
        <strong>${escapeHTML(t("memory.artifactEmpty"))}</strong>
        <p>${escapeHTML(t("memory.artifactHelp"))}</p>
      </div>
    </div>`;
  }
  const content = artifact.content || artifact.summary || "";
  const ref = artifact.ref || (artifact.hash ? `sha256:${artifact.hash}` : "");
  const storedBytes = artifact.stored_bytes ?? artifact.storedBytes ?? 0;
  const meta = [
    [t("memory.artifactRef"), ref || artifact.hash || "artifact"],
    [t("memory.artifactSize"), `${formatNumber(artifact.size || content.length || 0)} B`],
    [t("memory.artifactMime"), artifact.mime || artifact.kind || "text/plain"],
    storedBytes ? [t("memory.artifactStored"), `${formatNumber(storedBytes)} B`] : null
  ].filter(Boolean);
  const extraMeta = Object.entries(artifact.metadata || {}).slice(0, 4);
  return `
    <article class="memory-artifact">
      <header class="memory-artifact-head">
        <div>
          <span class="memory-artifact-kicker">${escapeHTML(localizedText(artifact.kind || artifact.mime || t("memory.artifact")))}</span>
          <strong>${escapeHTML(localizedText(artifact.title || artifact.kind || t("memory.artifact")))}</strong>
        </div>
        ${ref ? renderArtifactActions(ref, state) : ""}
      </header>
      ${artifact.summary ? `<p class="memory-artifact-summary">${escapeHTML(localizeMemoryText(artifact.summary))}</p>` : ""}
      <dl class="memory-artifact-meta">
        ${meta.map(([label, value]) => `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}
        ${extraMeta.map(([label, value]) => `<div><dt>${escapeHTML(localizedText(label))}</dt><dd>${escapeHTML(localizedText(value))}</dd></div>`).join("")}
      </dl>
      <section class="memory-artifact-body" aria-label="${escapeHTML(t("memory.artifactContent"))}">
        <div class="memory-artifact-body-head">
          <span>${escapeHTML(t("memory.artifactContent"))}</span>
          <span>${escapeHTML(formatNumber(artifact.size || content.length || 0))} B</span>
        </div>
        <div class="markdown-body">${renderSafeMarkdown(content)}</div>
      </section>
    </article>`;
}

function renderArtifactActions(ref, state = {}) {
  return `<div class="memory-artifact-actions">
    <div class="memory-artifact-refbar">
      <span>${escapeHTML(t("memory.artifactRefLabel"))}</span>
      <code>${escapeHTML(ref)}</code>
    </div>
    <div class="memory-artifact-action-grid" aria-label="${escapeHTML(t("memory.artifactActions"))}">
      ${renderArtifactActionButton({
        className: "memory-artifact-action primary-action",
        attr: `data-artifact-attach="${escapeHTML(ref)}"`,
        icon: artifactActionIcon("task"),
        title: t(state.artifactAttached ? "memory.artifactAttached" : "memory.artifactAttachPrompt"),
        help: t("memory.artifactAttachHelp")
      })}
      ${renderArtifactActionButton({
        className: "memory-artifact-action",
        attr: `data-artifact-workflow="${escapeHTML(ref)}"`,
        icon: artifactActionIcon("workflow"),
        title: t(state.artifactWorkflowAttached ? "memory.artifactWorkflowReady" : "memory.artifactUseWorkflow"),
        help: t("memory.artifactUseWorkflowHelp")
      })}
      ${renderArtifactActionButton({
        className: "memory-artifact-action",
        attr: `data-artifact-copy="${escapeHTML(ref)}"`,
        icon: artifactActionIcon("copy"),
        title: t(state.copiedArtifactRef ? "memory.artifactCopied" : "memory.artifactCopy"),
        help: t("memory.artifactCopyHelp")
      })}
    </div>
    <div class="memory-artifact-safe-note">
      <strong>${escapeHTML(t("memory.artifactDeleteUnavailableTitle"))}</strong>
      <span>${escapeHTML(t("memory.artifactDeleteUnavailableBody"))}</span>
    </div>
  </div>`;
}

function renderArtifactActionButton({ className = "", attr = "", icon = "", title = "", help = "" }) {
  return `<button type="button" class="${escapeHTML(className)}" ${attr}>
    <span class="memory-artifact-action-icon" aria-hidden="true">${icon}</span>
    <span>
      <strong>${escapeHTML(title)}</strong>
      <small>${escapeHTML(help)}</small>
    </span>
  </button>`;
}

function artifactActionIcon(kind) {
  const icons = {
    task: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14v16H5V4Zm2 2v12h10V6H7Zm2 3h6v2H9V9Zm0 4h4v2H9v-2Z"/></svg>`,
    workflow: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h6v6H4V5Zm2 2v2h2V7H6Zm8-2h6v6h-6V5Zm2 2v2h2V7h-2ZM4 15h6v6H4v-6Zm2 2v2h2v-2H6Zm5-9h2v2h-2V8Zm1 4h2v4h-3v-2h1v-2Zm3 3h2v-2h2v4h-4v-2Z"/></svg>`,
    copy: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 7V5c0-1.1.9-2 2-2h8c1.1 0 2 .9 2 2v10c0 1.1-.9 2-2 2h-2v2c0 1.1-.9 2-2 2H6c-1.1 0-2-.9-2-2V9c0-1.1.9-2 2-2h2Zm2 0h4c1.1 0 2 .9 2 2v6h2V5h-8v2ZM6 9v10h8V9H6Z"/></svg>`
  };
  return icons[kind] || "";
}

function renderArtifactIndex(index, state = {}) {
  const objects = Array.isArray(index?.objects) ? index.objects : [];
  if (!objects.length) return "";
  return `<div class="memory-artifact-index" aria-label="${escapeHTML(t("memory.recentArtifacts"))}">
    <div class="memory-artifact-index-head">
      <div>
        <span>${escapeHTML(t("memory.recentArtifacts"))}</span>
        <span>${escapeHTML(t("memory.artifactIndexHelp"))}</span>
      </div>
      <span class="memory-artifact-index-count">${escapeHTML(t("memory.artifactIndexCount", { count: objects.length }))}</span>
    </div>
    <div class="memory-artifact-index-list">
      ${objects.map(object => renderArtifactIndexItem(object, state)).join("")}
    </div>
  </div>`;
}

function renderArtifactIndexItem(object, state = {}) {
  const ref = object.ref || (object.hash ? `sha256:${object.hash}` : "");
  const active = ref && ref === (state.artifact?.ref || state.artifactRef);
  const title = object.title || object.kind || object.mime || t("memory.artifact");
  const summary = localizeMemoryText(object.summary || "");
  const storedBytes = object.stored_bytes ?? object.storedBytes ?? 0;
  const meta = [
    localizedText(object.mime || object.kind || ""),
    `${formatNumber(object.size || 0)} B`,
    storedBytes ? t("memory.artifactStoredInline", { bytes: formatNumber(storedBytes) }) : ""
  ].filter(Boolean);
  return `<button type="button" class="memory-artifact-index-item${active ? " active" : ""}" data-artifact-open="${escapeHTML(ref)}" ${state.busy ? "disabled" : ""} aria-pressed="${active ? "true" : "false"}">
    <span class="memory-artifact-index-main">
      <span class="memory-artifact-index-mark" aria-hidden="true"></span>
      <span class="memory-artifact-index-title">
        <strong>${escapeHTML(localizedText(title))}</strong>
        <code>${escapeHTML(ref || object.hash || "artifact")}</code>
      </span>
    </span>
    ${meta.length ? `<div class="memory-artifact-index-meta">${meta.map(value => `<span>${escapeHTML(value)}</span>`).join("")}</div>` : ""}
    ${summary ? `<p class="memory-artifact-index-summary">${escapeHTML(summary)}</p>` : ""}
  </button>`;
}

function renderMemoryItem(item) {
  const summary = localizeMemoryText(item.summary || "");
  const meta = String(item.meta || "").trim();
  return `
    <article class="memory-item">
      <div class="memory-item-head">
        ${item.eyebrow ? `<span>${escapeHTML(localizedText(item.eyebrow))}</span>` : ""}
        <strong>${escapeHTML(localizedText(item.title || ""))}</strong>
      </div>
      ${summary ? `<p>${escapeHTML(summary)}</p>` : ""}
      ${meta ? `<div class="memory-meta">${escapeHTML(compactText(meta))}</div>` : ""}
    </article>`;
}

function renderPrimerCard(title, body) {
  return `<article class="memory-primer-card">
    <strong>${escapeHTML(title)}</strong>
    <p>${escapeHTML(body)}</p>
  </article>`;
}

function searchSummaryText(search) {
  if (!search) return t("memory.searchHelp");
  return t("memory.searchCount", { count: search.returned || 0, total: search.total || 0 });
}

function memoryKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  if (!value) return t("memory.item");
  const key = `memory.kind.${value}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function localizeMemoryText(value) {
  const text = String(value || "").replace(/\r\n/g, "\n").trim();
  if (!text) return "";
  const lines = text
    .split("\n")
    .map(line => localizedText(line.trim()))
    .filter(Boolean);
  return compactText(lines.join(" / "));
}

function isBusyAction(state, action) {
  return Boolean(state?.busy && state?.busyAction === action);
}

function actionLabel(state, action, idleKey, busyKey) {
  return t(isBusyAction(state, action) ? busyKey : idleKey);
}

function compactText(value) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  return text.length > 220 ? `${text.slice(0, 217)}...` : text;
}

function formatNumber(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) return "0";
  return new Intl.NumberFormat().format(number);
}

function formatDateTime(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const date = new Date(text);
  if (Number.isNaN(date.getTime())) return text;
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(date);
}
