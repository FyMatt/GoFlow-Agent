import { agentRunEventsURL, agentRunExportURL, checkWorkspaceRequirement, createCollaborationBlackboardEntry, createCollaborationMessage, escapeHTML, fetchAgentRun, fetchAgentRunActions, fetchAgentRunArtifacts, fetchAgentRunContext, fetchAgentRunDiffs, fetchAgentRunReplay, fetchAgentRuns, fetchCollaborationBlackboard, fetchCollaborationMessages, fetchRun, fetchRunActionDiscovery, fetchRunArtifacts, fetchRunContext, fetchRunDelivery, fetchRunDiffs, fetchRunEvidence, fetchRunStages, fetchRunTimeline, fetchRuns, fetchRunWorkbench, fetchTeamState, fetchWorkflowRun, fetchWorkflowRunActions, fetchWorkflowRunArtifacts, fetchWorkflowRunContext, fetchWorkflowRunDiffs, fetchWorkflowRunEvidence, fetchWorkflowRunReplay, fetchWorkflowRuns, fetchWorkflowRunStages, postJSON, preflightTask, renderSafeMarkdown, request, runExportURL, startAgentRun, startWorkflowRun, streamAgentRunEvents, streamWorkflowRunEvents, submitWorkflowRunInput, updateCollaborationBlackboardAction, uploadAttachments, workflowRunEventsURL, workflowRunExportURL } from "../api.js";
import { currentLanguage, localizedText, t } from "../i18n.js";
import { icon } from "../icons.js";
import { extractSkillScriptContextFromRun, isSkillScriptToolName, skillScriptContextSignature } from "../run_context.js";
import { formatTokenText } from "../token_format.js";

const runStateStorageKey = "goflow.playground.runState";
const workflowStudioRunKey = "goflow.workflowStudio.activeRun";
const runTargetRecentStorageKey = "goflow.playground.recentTargets";
const executionTargetStorageKey = "goflow.playground.executionTarget";
const resourceTaskDraftKey = "goflow.playground.resourceDraft";
const reasoningEffortStorageKey = "goflow.playground.reasoningEffort";
const permissionModeStorageKey = "goflow.playground.permissionMode";
const workflowRunPollIntervalMS = 2200;
const workflowRunHistoryPollIntervalMS = 6600;
const defaultRunEventReconnectMS = 2200;
const timelineEventBuffers = new Map();
const timelineRunStates = new WeakMap();
let timelineEventFlushFrame = 0;
const examplePlaceholder = value => escapeHTML(t("common.exampleValue", { value }));

export async function renderChat(root, runtime, refreshRuntime) {
  const [options, workflows] = await Promise.all([
    safeRequest("/api/workflow-options", { agents: [], skills: [], tools: [] }),
    safeRequest("/api/workflow-graphs", [])
  ]);
  root.innerHTML = `
    <div class="grid chat-grid">
      <section class="panel span-8 chat-panel">
        <div class="run-console-grid">
          <section class="run-brief panel inset">
            <div class="panel-head compact run-task-head">
              <div>
                <h3 class="panel-title">${icon("clipboard-list")}<span>${t("chat.taskBriefTitle")}</span></h3>
              </div>
              <div class="chat-statusbar">
                <span id="runStatus" class="run-status idle ${runtime?.setup?.model_ready === false ? "attention" : ""}" role="status" aria-live="polite"><i></i><span>${runtime?.setup?.model_ready === false ? t("chat.setupRequired") : t("chat.ready")}</span></span>
                <button id="taskDrawerToggle" type="button" class="icon-button task-drawer-toggle" aria-controls="taskHistoryDrawer" aria-expanded="false" aria-label="${escapeHTML(t("chat.taskDrawerOpen"))}" title="${escapeHTML(t("chat.taskDrawerOpen"))}">
                  ${icon("history")}
                </button>
              </div>
            </div>
            <div id="taskExecutionContext" class="task-execution-context" aria-live="polite"></div>
            <div id="executionTargetField" class="task-execution-target hidden">
              ${icon("monitor")}
              <label><span>${t("chat.executionTargetLabel")}</span><select id="executionTargetSelect"></select></label>
            </div>
            <div class="run-control-bar" aria-label="${escapeHTML(t("chat.runControlTitle"))}">
              <label>
                <span>${t("chat.runMode")}</span>
                <select id="runMode"><option value="auto">${t("chat.modeAuto")}</option><option value="agent">${t("chat.modeAgent")}</option><option value="workflow">${t("chat.modeWorkflow")}</option></select>
              </label>
              <label id="agentField" class="hidden">
                <span>${t("chat.agent")}</span>
                <select id="agentSelect"></select>
              </label>
              <label id="workflowField" class="hidden">
                <span>${t("chat.workflow")}</span>
                <select id="workflowSelect"></select>
              </label>
              <label>
                <span>${t("chat.reasoningEffortLabel")}</span>
                <select id="reasoningEffort">
                  <option value="low">${t("chat.reasoningLow")}</option>
                  <option value="mid">${t("chat.reasoningMedium")}</option>
                  <option value="high">${t("chat.reasoningHigh")}</option>
                </select>
              </label>
              <label>
                <span>${t("chat.permissionModeLabel")}</span>
                <select id="permissionMode">
                  <option value="confirm">${t("chat.permissionConfirm")}</option>
                  <option value="workspace">${t("chat.permissionWorkspace")}</option>
                  <option value="full">${t("chat.permissionFull")}</option>
                </select>
              </label>
              <span id="permissionModeHint" class="run-permission-hint"></span>
            </div>
            <div class="composer-shell">
              <div class="composer-toolbar">
                <div class="reference-picker">
                  <button id="referenceToggle" type="button" class="reference-toggle" aria-haspopup="dialog" aria-expanded="false" aria-label="${escapeHTML(t("chat.referenceFiles"))}" title="${escapeHTML(t("chat.referenceFiles"))}">
                    ${icon("file-plus")}
                    <span>${t("chat.referenceFiles")}</span>
                  </button>
                  <span id="referenceFeedback" class="reference-feedback" role="status" aria-live="polite"></span>
                </div>
                <button id="attachmentPicker" type="button" class="icon-button" aria-label="${escapeHTML(t("chat.attachmentsAdd"))}" title="${escapeHTML(t("chat.attachmentsAdd"))}">${icon("paperclip")}</button>
                <input id="attachmentInput" type="file" multiple hidden>
              </div>
              <div class="composer">
                <textarea id="prompt" aria-label="${escapeHTML(t("chat.promptInput"))}" placeholder="${escapeHTML(t("chat.placeholder"))}"></textarea>
                <div class="stack">
                  <button id="send" class="primary icon-text" aria-label="${escapeHTML(t("chat.run"))}" title="${escapeHTML(t("chat.run"))}" aria-disabled="${runtime?.setup?.model_ready === false ? "true" : "false"}" ${runtime?.setup?.model_ready === false ? "disabled" : ""}>${icon("send")}<span>${t("chat.run")}</span></button>
                  <button id="clear" class="icon-text" aria-label="${escapeHTML(t("chat.clear"))}" title="${escapeHTML(t("chat.clear"))}">${icon("eraser")}<span>${t("chat.clear")}</span></button>
                </div>
              </div>
              <div id="attachmentTray" class="attachment-tray hidden" aria-live="polite"></div>
              <div id="referencePopover" class="reference-popover hidden" role="dialog" aria-label="${escapeHTML(t("chat.referenceFiles"))}">
                <div class="reference-popover-head">
                  <div>
                    <strong>${t("chat.referenceFiles")}</strong>
                    <small>${t("chat.referenceFilesHelp")}</small>
                  </div>
                  <button id="referenceClose" type="button" class="icon-button" aria-label="${escapeHTML(t("chat.referenceClose"))}" title="${escapeHTML(t("chat.referenceClose"))}">
                    ${icon("x")}
                  </button>
                </div>
                <input id="referencePrefix" class="reference-search" aria-label="${escapeHTML(t("chat.referenceFilterLabel"))}" placeholder="${escapeHTML(t("chat.referenceFilter"))}">
                <div id="fileSuggestions" class="file-suggestions reference-list"></div>
              </div>
            </div>
            <details class="run-recommended-targets">
              <summary class="run-section-copy">
                <strong>${t("chat.recommendedTargetsTitle")}</strong>
                <span>${t("chat.recommendedTargetsHelp")}</span>
              </summary>
              <div id="runTargetCards" class="run-target-cards" aria-label="${escapeHTML(t("chat.targetCardsAria"))}"></div>
            </details>
            <details class="run-options-panel advanced-only-section">
              <summary>
                <span>
                  <strong>${t("chat.runOptionsTitle")}</strong>
                  <small>${t("chat.runOptionsHelp")}</small>
                </span>
                <span id="runOptionsSummary" class="badge neutral"></span>
              </summary>
              <div class="run-target-panel">
                <label class="compact-field"><span>${t("chat.skill")}</span><select id="skillSelect"></select></label>
                <label class="compact-field"><span>${t("chat.tool")}</span><select id="toolSelect"></select></label>
                <div class="context-actions">
                  <button id="insertContext" aria-label="${escapeHTML(t("chat.insertContext"))}" title="${escapeHTML(t("chat.insertContext"))}">${t("chat.insertContext")}</button>
                  <span id="contextFeedback" role="status" aria-live="polite"></span>
                </div>
              </div>
            </details>
            <div id="runContextGuide" class="run-context-guide advanced-only-section" aria-live="polite"></div>
          </section>

          <section class="run-timeline panel inset">
            <div class="panel-head compact">
              <div>
                <h3 class="panel-title">${icon("activity")}<span>${t("chat.timelineTitle")}</span></h3>
                <p class="muted">${t("chat.timelineHelp")}</p>
              </div>
              <div class="run-live-stack">
                <span id="runLiveStatus" class="live-badge idle" role="status" aria-live="polite"><i></i>${t("chat.realtimeIdle")}</span>
                <span id="runTimelineHint" class="badge">${t("chat.timelineIdle")}</span>
              </div>
            </div>
            <div id="runAttention" class="run-attention hidden"></div>
            <div class="timeline-feed">
              <div id="messages" class="messages"><div class="timeline-empty-state"><strong>${t("chat.timelineEmptyTitle")}</strong><span>${t("chat.timelineEmptyBody")}</span></div></div>
              <button id="timelineJumpLatest" class="timeline-jump-latest hidden icon-text" type="button" aria-label="${escapeHTML(t("chat.timelineJumpLatest"))}" title="${escapeHTML(t("chat.timelineJumpLatest"))}">${icon("arrow-up")}<span>${t("chat.timelineJumpLatest")}</span></button>
            </div>
          </section>

          <section id="runTimelineTotals" class="timeline-run-totals run-totals-strip hidden" aria-live="polite"></section>

          <section class="run-output-stack" aria-label="${escapeHTML(t("chat.resultTitle"))}">
            <aside class="run-results panel inset hidden">
              <div class="panel-head compact">
                <div>
                  <h3 class="panel-title">${icon("clipboard-list")}<span>${t("chat.resultTitle")}</span></h3>
                  <p class="muted">${t("chat.resultHelp")}</p>
                </div>
                <span id="resultStatus" class="badge">${t("chat.resultIdle")}</span>
              </div>
              <div id="resultEmpty" class="result-empty item muted">${t("chat.resultEmpty")}</div>
              <div id="resultPreview" class="result-preview hidden" aria-live="polite"></div>
              <div id="resultOutput" class="result-output hidden"></div>
            </aside>
            <details id="runCollabPanel" class="run-collab panel inset collab-panel advanced-only-section">
              <summary class="collab-summary">
                <div class="panel-head compact collab-head">
                  <div>
                    <h3>${t("chat.collabTitle")}</h3>
                    <p class="muted">${t("chat.collabHelp")}</p>
                  </div>
                  <span id="collabStatus" class="badge">${t("chat.collabIdle")}</span>
                </div>
              </summary>
              <div class="collab-panel-body">
                <div class="collab-filter-grid">
                  <label class="compact-field"><span>${t("chat.collabRunId")}</span><input id="collabRunId" placeholder="${examplePlaceholder("run-123")}"></label>
                  <label class="compact-field"><span>${t("chat.collabTeam")}</span><input id="collabTeam" placeholder="${examplePlaceholder("software-task-team")}"></label>
                  <label class="compact-field"><span>${t("chat.collabStage")}</span><input id="collabStage" placeholder="${examplePlaceholder("plan")}"></label>
                  <label class="compact-field"><span>${t("chat.collabAgent")}</span><input id="collabAgent" placeholder="${examplePlaceholder("planner")}"></label>
                  <label class="compact-field"><span>${t("chat.collabKind")}</span><input id="collabKind" placeholder="${examplePlaceholder("handoff")}"></label>
                  <label class="compact-field"><span>${t("chat.collabScope")}</span><input id="collabScope" placeholder="${examplePlaceholder("workflow")}"></label>
                  <label class="compact-field"><span>${t("chat.collabStatusField")}</span><input id="collabEntryStatus" placeholder="${examplePlaceholder("active")}"></label>
                  <label class="compact-field"><span>${t("chat.collabLimit")}</span><input id="collabLimit" type="number" min="1" max="50" value="12"></label>
                  <div class="context-actions collab-actions">
                    <button id="collabRefresh" type="button">${t("chat.collabRefresh")}</button>
                    <span id="collabFeedback" role="status" aria-live="polite"></span>
                  </div>
                </div>
                <form id="collabComposer" class="collab-compose" autocomplete="off">
                  <div class="collab-compose-head">
                    <div>
                      <strong>${t("chat.collabRecordTitle")}</strong>
                      <small>${t("chat.collabRecordHelp")}</small>
                    </div>
                  </div>
                  <div class="collab-compose-grid">
                    <label class="compact-field"><span>${t("chat.collabRecordDestination")}</span><select id="collabRecordDestination">
                      <option value="blackboard">${t("chat.collabRecordDestinationBlackboard")}</option>
                      <option value="message">${t("chat.collabRecordDestinationMessage")}</option>
                    </select></label>
                    <label class="compact-field"><span>${t("chat.collabRecordKind")}</span><select id="collabRecordKind">
                      <option value="decision">${t("chat.collabRecordDecision")}</option>
                      <option value="risk">${t("chat.collabRecordRisk")}</option>
                      <option value="question">${t("chat.collabRecordQuestion")}</option>
                      <option value="note">${t("chat.collabRecordNote")}</option>
                      <option value="handoff">${t("chat.collabRecordHandoff")}</option>
                    </select></label>
                    <label class="compact-field"><span>${t("chat.collabRecordSubject")}</span><input id="collabRecordSubject" maxlength="160" placeholder="${escapeHTML(t("chat.collabRecordSubjectPlaceholder"))}"></label>
                    <label class="compact-field"><span>${t("chat.collabRecordToAgent")}</span><input id="collabRecordToAgent" maxlength="80" placeholder="${escapeHTML(t("chat.collabRecordToAgentPlaceholder"))}"></label>
                    <label class="compact-field collab-compose-content"><span>${t("chat.collabRecordContent")}</span><textarea id="collabRecordContent" rows="3" placeholder="${escapeHTML(t("chat.collabRecordContentPlaceholder"))}"></textarea></label>
                    <div class="collab-compose-actions">
                      <button id="collabRecordSave" type="submit" class="primary">${t("chat.collabRecordSave")}</button>
                      <small>${t("chat.collabRecordTip")}</small>
                    </div>
                  </div>
                </form>
                <div class="collab-grid">
                  <section class="collab-card">
                    <div class="collab-card-head">
                      <strong>${t("chat.collabTimelineTitle")}</strong>
                      <small id="collabMessageCount">0</small>
                    </div>
                    <div id="collabMessages" class="collab-list"></div>
                  </section>
                  <section class="collab-card">
                    <div class="collab-card-head">
                      <strong>${t("chat.teamStateTitle")}</strong>
                      <small id="collabBlackboardCount">0</small>
                    </div>
                    <div id="collabBlackboard" class="collab-list"></div>
                  </section>
                </div>
              </div>
            </details>
          </section>
        </div>
      </section>
      <button id="taskDrawerBackdrop" type="button" class="task-drawer-backdrop hidden" aria-label="${escapeHTML(t("common.close"))}"></button>
      <aside id="taskHistoryDrawer" class="panel span-4 file-panel history-aside task-context-drawer" aria-hidden="true" aria-label="${escapeHTML(t("chat.taskDrawerTitle"))}" inert>
        <div class="task-drawer-head">
          <div>
            <strong>${t("chat.taskDrawerTitle")}</strong>
            <small>${t("chat.taskDrawerHelp")}</small>
          </div>
          <button id="taskDrawerClose" type="button" class="icon-button" aria-label="${escapeHTML(t("common.close"))}" title="${escapeHTML(t("common.close"))}">
            ${icon("x")}
          </button>
        </div>
        <section class="run-history-panel">
          <div class="panel-head compact">
            <div>
              <h3 class="panel-title">${icon("history")}<span>${t("chat.runHistoryTitle")}</span></h3>
              <p class="muted">${t("chat.runHistoryHelp")}</p>
            </div>
            <button id="runHistoryRefresh" type="button" class="ghost-button icon-text">${icon("refresh-cw")}<span>${t("chat.runHistoryRefresh")}</span></button>
          </div>
          <span id="runHistoryStatus" class="badge">${t("chat.runHistoryLoading")}</span>
          <div id="runHistoryFilters" class="run-history-filters advanced-only-section" aria-label="${escapeHTML(t("chat.runHistoryFilters"))}"></div>
          <div id="runHistoryList" class="run-history-list"></div>
          <section class="run-history-detail-shell hidden" aria-label="${escapeHTML(t("chat.runHistoryDetailTitle"))}">
            <div class="run-history-detail-label">
              <div>
                <strong>${t("chat.runHistoryDetailTitle")}</strong>
                <small>${t("chat.runHistoryDetailHelp")}</small>
              </div>
              <span class="badge neutral advanced-only-section">${t("chat.runHistoryDetailBadge")}</span>
            </div>
            <div id="runHistoryDetail" class="run-history-detail"></div>
          </section>
        </section>
      </aside>
    </div>`;

  const messages = root.querySelector("#messages");
  const timelineJumpLatest = root.querySelector("#timelineJumpLatest");
  const resultPreview = root.querySelector("#resultPreview");
  const resultOutput = root.querySelector("#resultOutput");
  const resultEmpty = root.querySelector("#resultEmpty");
  const resultStatus = root.querySelector("#resultStatus");
  const runAttention = root.querySelector("#runAttention");
  const runTimelineHint = root.querySelector("#runTimelineHint");
  const runLiveStatus = root.querySelector("#runLiveStatus");
  const prompt = root.querySelector("#prompt");
  const send = root.querySelector("#send");
  const clear = root.querySelector("#clear");
  const taskDrawer = root.querySelector("#taskHistoryDrawer");
  const taskDrawerToggle = root.querySelector("#taskDrawerToggle");
  const taskDrawerClose = root.querySelector("#taskDrawerClose");
  const taskDrawerBackdrop = root.querySelector("#taskDrawerBackdrop");
  const agentSelect = root.querySelector("#agentSelect");
  const agentField = root.querySelector("#agentField");
  const runMode = root.querySelector("#runMode");
  const workflowField = root.querySelector("#workflowField");
  const workflowSelect = root.querySelector("#workflowSelect");
  const skillSelect = root.querySelector("#skillSelect");
  const toolSelect = root.querySelector("#toolSelect");
  const insertContext = root.querySelector("#insertContext");
  const runTargetCards = root.querySelector("#runTargetCards");
  const runOptionsSummary = root.querySelector("#runOptionsSummary");
  const taskExecutionContext = root.querySelector("#taskExecutionContext");
  const executionTargetField = root.querySelector("#executionTargetField");
  const executionTargetSelect = root.querySelector("#executionTargetSelect");
  const reasoningEffort = root.querySelector("#reasoningEffort");
  const permissionMode = root.querySelector("#permissionMode");
  const permissionModeHint = root.querySelector("#permissionModeHint");
  const runCollabPanel = root.querySelector("#runCollabPanel");
  const referenceToggle = root.querySelector("#referenceToggle");
  const referenceClose = root.querySelector("#referenceClose");
  const referencePopover = root.querySelector("#referencePopover");
  const referencePrefix = root.querySelector("#referencePrefix");
  const referenceFeedback = root.querySelector("#referenceFeedback");
  const attachmentPicker = root.querySelector("#attachmentPicker");
  const attachmentInput = root.querySelector("#attachmentInput");
  const attachmentTray = root.querySelector("#attachmentTray");
  const targetSummary = root.querySelector("#targetSummary");
  const runStatus = root.querySelector("#runStatus");
  const contextFeedback = root.querySelector("#contextFeedback");
  const suggestions = root.querySelector("#fileSuggestions");
  let promptSelection = { start: 0, end: 0 };
  let taskAttachments = [];
  let pendingAttachmentUploads = [];
  let attachmentUploadCount = 0;
  const rememberPromptSelection = () => {
    const end = prompt.value.length;
    promptSelection = {
      start: typeof prompt.selectionStart === "number" ? prompt.selectionStart : end,
      end: typeof prompt.selectionEnd === "number" ? prompt.selectionEnd : end
    };
  };
  const draft = localStorage.getItem("goflow.playground.draft");
  if (draft) {
    prompt.value = draft;
    localStorage.removeItem("goflow.playground.draft");
  }
  rememberPromptSelection();

  const renderTaskAttachments = () => {
    if (!attachmentTray) return;
    const items = [...taskAttachments.map(item => ({ ...item, uploaded: true })), ...pendingAttachmentUploads];
    attachmentTray.classList.toggle("hidden", items.length === 0);
    attachmentTray.innerHTML = items.map((item, index) => {
      const imageURL = item.previewURL || (item.kind === "image" && item.hash ? `/api/attachments/${encodeURIComponent(item.hash)}` : "");
      return `
      <span class="attachment-chip ${item.error ? "error" : item.uploaded ? "ready" : "uploading"}" title="${escapeHTML(item.name || "附件")}">
        ${imageURL ? `<img src="${escapeHTML(imageURL)}" alt="">` : ""}
        <span class="attachment-chip-kind">${item.kind === "image" ? "图" : "件"}</span>
        <span class="attachment-chip-name">${escapeHTML(item.name || "附件")}</span>
        ${item.uploaded ? "" : `<small>${item.error ? "失败" : "上传中"}</small>`}
        <button type="button" data-remove-attachment="${index}" data-attachment-uploaded="${item.uploaded ? "true" : "false"}" aria-label="删除附件"${!item.uploaded && !item.error ? " disabled aria-disabled=\"true\"" : ""}>×</button>
      </span>`;
    }).join("");
  };
  const uploadTaskFiles = async files => {
    const list = Array.from(files || []).filter(file => file && file.size > 0);
    if (!list.length) return;
    if (taskAttachments.length + pendingAttachmentUploads.length + list.length > 12) {
      showContextFeedback(referenceFeedback, "单次最多添加 12 个附件", true);
      return;
    }
    const batchID = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
    const pending = list.map((file, index) => ({
      id: `${batchID}-${index}`,
      batchID,
      file,
      name: file.name || "附件",
      kind: file.type.startsWith("image/") ? "image" : "file",
      previewURL: file.type.startsWith("image/") ? URL.createObjectURL(file) : "",
      uploaded: false,
      error: false
    }));
    pendingAttachmentUploads.push(...pending);
    renderTaskAttachments();
    attachmentUploadCount += 1;
    attachmentPicker?.setAttribute("aria-busy", "true");
    try {
      const response = await uploadAttachments(list);
      const uploaded = Array.isArray(response?.attachments) ? response.attachments : [];
      taskAttachments = [...taskAttachments, ...uploaded].filter((item, index, all) => item?.hash && all.findIndex(other => other.hash === item.hash) === index);
      for (const item of pending) {
        if (item.previewURL) URL.revokeObjectURL(item.previewURL);
      }
      pendingAttachmentUploads = pendingAttachmentUploads.filter(item => item.batchID !== batchID);
    } catch (error) {
      pendingAttachmentUploads = pendingAttachmentUploads.map(item => item.batchID === batchID ? { ...item, error: true } : item);
      showContextFeedback(referenceFeedback, error?.message || "附件上传失败", true);
    } finally {
      attachmentUploadCount = Math.max(0, attachmentUploadCount - 1);
      if (attachmentUploadCount === 0) attachmentPicker?.removeAttribute("aria-busy");
      if (attachmentInput) attachmentInput.value = "";
      renderTaskAttachments();
    }
  };
  attachmentPicker?.addEventListener("click", () => attachmentInput?.click());
  attachmentInput?.addEventListener("change", event => uploadTaskFiles(event.target.files));
  attachmentTray?.addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-remove-attachment]") : null;
    if (!button) return;
    const index = Number(button.dataset.removeAttachment);
    if (button.dataset.attachmentUploaded === "true") {
      taskAttachments.splice(index, 1);
    } else {
      const pendingIndex = index - taskAttachments.length;
      const [removed] = pendingAttachmentUploads.splice(pendingIndex, 1);
      if (removed?.previewURL) URL.revokeObjectURL(removed.previewURL);
    }
    renderTaskAttachments();
  });
  prompt?.addEventListener("paste", event => {
    const files = Array.from(event.clipboardData?.files || []).filter(file => file.type.startsWith("image/"));
    if (files.length) {
      event.preventDefault();
      uploadTaskFiles(files);
    }
  });
  prompt?.addEventListener("dragover", event => {
    if (event.dataTransfer?.types?.includes("Files")) {
      event.preventDefault();
      prompt.classList.add("drop-active");
    }
  });
  prompt?.addEventListener("dragleave", () => prompt.classList.remove("drop-active"));
  prompt?.addEventListener("drop", event => {
    if (!event.dataTransfer?.files?.length) return;
    event.preventDefault();
    prompt.classList.remove("drop-active");
    uploadTaskFiles(event.dataTransfer.files);
  });

  if (!(runtime.agents || []).length) {
    const option = document.createElement("option");
    option.value = runtime.active_agent || "";
    option.textContent = runtime.active_agent || "-";
    agentSelect.appendChild(option);
  }
  for (const agent of runtime.agents || []) {
    const option = document.createElement("option");
    option.value = agent.id;
    option.textContent = `${agent.name || agent.id} (${modeLabel(agent.mode)})`;
    agentSelect.appendChild(option);
  }
  agentSelect.value = runtime.active_agent || agentSelect.value;
  const skillValues = (options.skills?.length ? options.skills : runtime.skills || []).map(item => ({
    value: item.name,
    label: taskResourceDisplayName("skill", item.name, item.name),
    title: item.description || ""
  }));
  const toolValues = options.tools?.length ? options.tools : runtime.tools || [];
  const workflowEntries = workflowExecutorEntries(options, workflows);
  fillSelect(workflowSelect, workflowEntries, t("chat.noWorkflow"));
  fillSelect(skillSelect, skillValues, t("chat.autoSkill"));
  fillSelect(toolSelect, toolValues, t("chat.noTool"));
  populateExecutionTargetSelect(executionTargetSelect, runtime);
  const reasoningStorageKey = reasoningEffortKey(runtime);
  reasoningEffort.value = storedRunControl(reasoningStorageKey, runtimeReasoningEffort(runtime));
  permissionMode.value = storedRunControl(permissionModeStorageKey, "confirm");
  applyResourceTaskDraft(readResourceTaskDraft(), { runMode, agentSelect, workflowSelect, skillSelect, toolSelect });

  const runState = createRunState(messages, timelineJumpLatest, resultPreview, resultOutput, resultEmpty, resultStatus, runAttention, runTimelineHint, runLiveStatus, root, runStatus, send);
  runState.refreshRuntime = refreshRuntime;
  let latestRuntime = runtime;
  let latestPreflight = null;
  const syncTaskExecutionContext = () => renderTaskExecutionContext(taskExecutionContext, latestRuntime, {
    agent: agentSelect.value,
    workflow: workflowSelect.value,
    skill: skillSelect.value,
    tool: toolSelect.value,
    targetID: executionTargetSelect.value || "local",
    reasoningEffort: reasoningEffort.value,
    permissionMode: permissionMode.value,
    preflight: latestPreflight
  });
  const syncExecutionTargetField = () => {
    const targets = Array.isArray(latestRuntime?.execution_targets) ? latestRuntime.execution_targets : [];
    const show = targets.length > 1 || taskSelectionRequiresExecutionTarget(latestRuntime, {
      agent: agentSelect.value,
      skill: skillSelect.value
    });
    executionTargetField?.classList.toggle("hidden", !show);
  };
  const syncTaskReadiness = () => {
    const modelReady = latestRuntime?.setup?.model_ready !== false;
    const active = currentRunStillActive(runState) || runState.requestInFlight;
    const blocked = !modelReady && !active;
    if (blocked) {
      runState.setupBlocked = true;
      runStatus?.classList.add("attention");
      const label = runStatus?.querySelector("span");
      if (label) label.textContent = t("chat.setupRequired");
      send.disabled = true;
      send.setAttribute("aria-disabled", "true");
      send.title = t("chat.setupRequired");
      runAttention.className = "run-attention setup-required";
      runAttention.innerHTML = `<strong>${escapeHTML(t("chat.setupRequiredTitle"))}</strong><p>${escapeHTML(t("chat.setupRequiredBody"))}</p><div class="run-attention-actions"><button type="button" data-open-settings>${escapeHTML(t("chat.openSettings"))}</button></div>`;
      runAttention.classList.remove("hidden");
    } else if (runState.setupBlocked) {
      runState.setupBlocked = false;
      runStatus?.classList.remove("attention");
      send.title = "";
      if (!active) setRunStatus(root, runStatus, send, false);
      if (runAttention.classList.contains("setup-required")) {
        runAttention.innerHTML = "";
        runAttention.className = "run-attention hidden";
      }
    }
  };
  const disposeTimelineLayoutSync = bindRunTimelineHeightSync(root);
  const disposeTimelineFollow = bindTimelineFollowMode(runState);
  const disposeRunControlHints = bindRunControlHints(root);
  const collaborationState = createCollaborationState(root, runtime, runState);
  const historyState = createWorkflowHistoryState(root, runState, collaborationState, refreshRuntime);
  bindRunEmptyStateActions(root, prompt);
  const referencePickerController = new AbortController();
  runState.submitWorkflowInput = createWorkflowInputSubmitHandler(runState, messages, collaborationState, refreshRuntime);
  const taskDrawerIsOpen = () => taskDrawer?.classList.contains("open") === true;
  const closeTaskDrawer = () => {
    taskDrawer?.classList.remove("open");
    taskDrawer?.setAttribute("aria-hidden", "true");
    taskDrawer?.setAttribute("inert", "");
    taskDrawerToggle?.setAttribute("aria-expanded", "false");
    taskDrawerBackdrop?.classList.add("hidden");
    document.body.classList.remove("task-drawer-open");
    stopWorkflowHistoryPolling(historyState);
  };
  const openTaskDrawer = async ({ focus = "" } = {}) => {
    taskDrawer?.classList.add("open");
    taskDrawer?.setAttribute("aria-hidden", "false");
    taskDrawer?.removeAttribute("inert");
    taskDrawerToggle?.setAttribute("aria-expanded", "true");
    taskDrawerBackdrop?.classList.remove("hidden");
    document.body.classList.add("task-drawer-open");
    await loadWorkflowRunHistory(historyState, focus || currentHistoryRunKey(runState), { forceDetail: Boolean(focus) }).catch(() => {});
    startWorkflowHistoryPolling(historyState);
  };
  taskDrawerToggle?.addEventListener("click", () => {
    if (taskDrawerIsOpen()) closeTaskDrawer();
    else openTaskDrawer();
  });
  taskDrawerClose?.addEventListener("click", closeTaskDrawer);
  taskDrawerBackdrop?.addEventListener("click", closeTaskDrawer);
  const closeTaskDrawerOnEscape = event => {
    if (event.key === "Escape" && taskDrawerIsOpen()) {
      closeTaskDrawer();
      taskDrawerToggle?.focus();
    }
  };
  document.addEventListener("keydown", closeTaskDrawerOnEscape);
  runState.historyRefresh = () => loadWorkflowRunHistory(historyState, currentHistoryRunKey(runState), { background: true }).catch(() => {});
  const disposeRunView = () => {
    referencePickerController.abort();
    flushRunStateSave(runState);
    stopWorkflowRunPolling(runState);
    stopAgentRunPolling(runState);
    stopWorkflowHistoryPolling(historyState);
    disposeTimelineLayoutSync();
    disposeTimelineFollow();
    disposeRunControlHints();
    document.removeEventListener("keydown", closeTaskDrawerOnEscape);
    document.body.classList.remove("task-drawer-open");
    syncRunUnloadGuard(runState);
  };
  window.addEventListener("goflow:view-dispose", disposeRunView, { once: true });
  let syncTargetCards = () => {};
  renderRunTargetCards(runTargetCards, {
    runtime,
    workflowEntries,
    agentSelect,
    workflowSelect,
    runMode,
    onSelect: () => syncTarget()
  });
  const rerenderRunTargetCards = () => renderRunTargetCards(runTargetCards, {
    runtime,
    workflowEntries,
    agentSelect,
    workflowSelect,
    runMode,
    onSelect: () => syncTarget()
  });
  syncTargetCards = () => updateRunTargetCards(runTargetCards, runMode.value, agentSelect.value, workflowSelect.value);
  const syncRunContextGuide = () => updateRunContextGuide(root, {
    mode: runMode.value,
    skill: skillSelect.value,
    tool: toolSelect.value,
    runtime: latestRuntime,
    promptValue: prompt.value,
    referenceOpen: !referencePopover.classList.contains("hidden"),
    runState
  });
  runState.syncContextGuide = syncRunContextGuide;
  const syncTarget = () => {
    latestPreflight = null;
    agentField.classList.toggle("hidden", runMode.value !== "agent");
    workflowField.classList.toggle("hidden", runMode.value !== "workflow");
    const target = runMode.value === "auto"
      ? t("chat.autoTarget")
      : runMode.value === "workflow" && workflowSelect.value
        ? workflowSelect.value
        : agentSelect.value || "-";
    if (targetSummary) targetSummary.textContent = `${runModeLabel(runMode.value)}: ${target}`;
    if (runOptionsSummary) runOptionsSummary.textContent = t("chat.runOptionsSummary", { mode: runModeLabel(runMode.value), target });
    permissionModeHint.textContent = permissionModeHelp(permissionMode.value);
    permissionModeHint.classList.toggle("danger", permissionMode.value === "full");
    syncTargetCards();
    syncTaskExecutionContext();
    syncExecutionTargetField();
    syncRunContextGuide();
  };
  runMode.addEventListener("change", syncTarget);
  agentSelect.addEventListener("change", () => {
    runMode.value = "agent";
    syncTarget();
  });
  workflowSelect.addEventListener("change", () => {
    runMode.value = "workflow";
    syncTarget();
  });
  [skillSelect, toolSelect].forEach(input => input.addEventListener("change", syncTarget));
  [reasoningEffort, permissionMode].forEach(input => {
    input.addEventListener("change", () => {
      try {
        localStorage.setItem(input === reasoningEffort ? reasoningStorageKey : permissionModeStorageKey, input.value);
      } catch {
        // Storage is optional; the selected control still applies to this task.
      }
      syncTarget();
    });
  });
  executionTargetSelect.addEventListener("change", () => {
    try {
      localStorage.setItem(executionTargetStorageKey, executionTargetSelect.value || "local");
    } catch {
      // Storage is optional; the selected value still applies to this task.
    }
    syncTarget();
  });
  syncTarget();
  bindCollaborationControls(collaborationState);
  bindWorkflowHistoryControls(historyState);
  bindCollaborationLazyLoad(runCollabPanel, collaborationState);
  await restorePlaygroundRunState(runState, collaborationState, runtime);
  if (runState.currentRunType === "agent" && shouldPollAgentRunStatus(runState.lastWorkflowStatus)) {
    startAgentRunPolling(runState, collaborationState, { runID: runState.currentRunID, eventsURL: runState.eventsURL });
  } else if (runState.currentRunType !== "agent" && shouldPollWorkflowRunStatus(runState.lastWorkflowStatus)) {
    startWorkflowRunPolling(runState, collaborationState, { runID: runState.currentRunID, workflowName: runState.currentWorkflowName });
  }
  const initialHistoryFocus = initialRunHistoryFocus(runState);
  if (initialHistoryFocus) {
    await openTaskDrawer({ focus: initialHistoryFocus });
  } else {
    loadWorkflowRunHistory(historyState, "", { background: true, listOnly: true })
      .then(() => {
        if (historyState.status && !historyState.runs?.length) {
          historyState.status.textContent = t("chat.taskDrawerIdle");
        }
      })
      .catch(() => {});
  }
  syncTaskReadiness();
  if (globalThis.performance?.now) {
    globalThis.__GOFLOW_PERFORMANCE__ = {
      ...(globalThis.__GOFLOW_PERFORMANCE__ || {}),
      taskPageInteractiveMS: Math.round(globalThis.performance.now())
    };
  }

  insertContext.onclick = () => {
    const lines = contextDirectives(root);
    if (!lines.length) {
      showContextFeedback(contextFeedback, t("chat.contextEmpty"), true);
      return;
    }
    const spacer = prompt.value && !prompt.value.endsWith("\n") ? "\n" : "";
    prompt.value += `${spacer}${lines.join("\n")}\n`;
    showContextFeedback(contextFeedback, t(lines.length === 1 ? "chat.contextInsertedOne" : "chat.contextInsertedMany", { count: lines.length }), false);
    prompt.focus();
  };
  const closeReferencePicker = () => {
    if (referenceSearchTimer) {
      window.clearTimeout(referenceSearchTimer);
      referenceSearchTimer = 0;
    }
    referenceLoadSeq += 1;
    referencePopover.classList.add("hidden");
    referencePopover.setAttribute("aria-hidden", "true");
    referenceToggle.setAttribute("aria-expanded", "false");
    syncRunContextGuide();
  };
  let referenceSearchTimer = 0;
  let referenceLoadSeq = 0;
  const loadReferencesForPrompt = () => {
    const seq = ++referenceLoadSeq;
    return loadReferenceFiles(referencePrefix.value, suggestions, prompt, referenceFeedback, () => {
      closeReferencePicker();
      rememberPromptSelection();
    }, () => promptSelection, () => seq === referenceLoadSeq);
  };
  const queueReferenceSearch = () => {
    if (referenceSearchTimer) window.clearTimeout(referenceSearchTimer);
    referenceSearchTimer = window.setTimeout(() => {
      referenceSearchTimer = 0;
      loadReferencesForPrompt().catch(() => {});
    }, 140);
  };
  const openReferencePicker = async () => {
    positionReferencePicker(referencePopover, referenceToggle, root);
    referencePopover.classList.remove("hidden");
    referencePopover.setAttribute("aria-hidden", "false");
    referenceToggle.setAttribute("aria-expanded", "true");
    syncRunContextGuide();
    referencePrefix.focus();
    referencePrefix.select();
    if (!(await ensureWorkspaceRequirementForReference(referenceFeedback, suggestions))) return;
    await loadReferencesForPrompt();
  };
  referenceToggle.onclick = async event => {
    event.stopPropagation();
    rememberPromptSelection();
    if (referencePopover.classList.contains("hidden")) {
      await openReferencePicker();
    } else {
      closeReferencePicker();
    }
  };
  referenceClose.onclick = event => {
    event.stopPropagation();
    closeReferencePicker();
    prompt.focus();
  };
  referencePrefix.oninput = queueReferenceSearch;
  referencePopover.addEventListener("click", event => event.stopPropagation());
  referencePopover.addEventListener("keydown", event => {
    if (event.key === "Escape") {
      closeReferencePicker();
      prompt.focus();
    }
  });
  ["resize", "scroll"].forEach(type => {
    window.addEventListener(type, () => {
      if (!referencePopover.classList.contains("hidden")) {
        positionReferencePicker(referencePopover, referenceToggle, root);
      }
    }, { passive: true, signal: referencePickerController.signal });
  });
  root.querySelector("#runContextGuide")?.addEventListener("click", event => {
    const action = event.target instanceof Element ? event.target.closest("[data-run-context-action]") : null;
    if (!action) return;
    event.stopPropagation();
    const name = action.dataset.runContextAction || "";
    if (name === "workspace") {
      location.hash = "workspace";
      return;
    }
    if (name === "cost") {
      location.hash = "status";
      return;
    }
    if (name === "files") {
      referenceToggle.click();
    }
  });
  root.addEventListener("click", event => {
    if (event.target.closest("[data-open-settings]")) {
      location.hash = "settings";
      return;
    }
    const officeEdit = event.target instanceof Element ? event.target.closest("[data-office-edit-path]") : null;
    if (officeEdit) {
      const path = String(officeEdit.dataset.officeEditPath || "").trim();
      if (path) {
        const instruction = `${t("chat.officeArtifactContinue")} @${path}：`;
        prompt.value = prompt.value.trim() ? `${prompt.value.trim()}\n${instruction}` : instruction;
        prompt.focus();
        prompt.setSelectionRange(prompt.value.length, prompt.value.length);
        prompt.dispatchEvent(new Event("input", { bubbles: true }));
      }
      return;
    }
    if (!event.target.closest(".reference-picker") && !event.target.closest(".reference-popover")) closeReferencePicker();
  });
  ["click", "keyup", "select", "input"].forEach(type => prompt.addEventListener(type, rememberPromptSelection));
  prompt.addEventListener("input", syncRunContextGuide);
  window.addEventListener("goflow:runtime", event => {
    latestRuntime = event.detail || latestRuntime;
    populateExecutionTargetSelect(executionTargetSelect, latestRuntime, executionTargetSelect.value);
    syncTaskExecutionContext();
    syncExecutionTargetField();
    syncRunContextGuide();
    syncTaskReadiness();
  }, { signal: referencePickerController.signal });

  send.onclick = async () => {
    if (attachmentUploadCount > 0) {
      showContextFeedback(referenceFeedback, "附件仍在上传，请稍候", true);
      return;
    }
    if (pendingAttachmentUploads.some(item => item.error)) {
      showContextFeedback(referenceFeedback, "请删除上传失败的附件后重试", true);
      return;
    }
    const rawInput = prompt.value.trim();
    const directives = contextDirectives(root);
    const taskInput = rawInput || (taskAttachments.length ? "请分析附件并根据其中的信息完成任务。" : "");
    const input = [directives.join("\n"), taskInput].filter(Boolean).join("\n\n");
    if (!input) return;
    if (runMode.value === "workflow" && !workflowSelect.value) {
      appendTimelineEvent(messages, {
        tone: "error",
        title: t("chat.errorTitle"),
        detail: t("chat.chooseWorkflowError")
      });
      return;
    }
    const workspaceReady = await ensureWorkspaceRequirementBeforeRun({
      root,
      runStatus,
      send,
      runState,
      messages,
      mode: runMode.value === "auto" ? "agent" : runMode.value,
      input,
      workflowName: workflowSelect.value,
      agentName: agentSelect.value || ""
    });
    if (!workspaceReady) return;
    let preflight;
    const requestedTargetID = executionTargetSelect.value || "local";
    try {
      preflight = await preflightTask(runMode.value === "auto"
        ? { type: "auto", input, approve: false, target_id: requestedTargetID }
        : runMode.value === "workflow"
          ? { type: "workflow", workflow: workflowSelect.value, input, approve: false, target_id: requestedTargetID }
          : { type: "agent", agent: agentSelect.value || "", skill: skillSelect.value || "", tool: toolSelect.value || "", input, target_id: requestedTargetID });
    } catch (error) {
      showRunAttention(runState, t("chat.preflightTitle"), chatDisplayText(error.message || t("chat.preflightFallback")), "error");
      return;
    }
    latestPreflight = preflight;
    syncTaskExecutionContext();
    if (!preflight?.valid) {
      showTaskPreflightAttention(runState, preflight);
      return;
    }
    const effectiveMode = preflight.kind === "workflow" ? "workflow" : "agent";
    const targetValue = String(preflight.target || (effectiveMode === "workflow" ? workflowSelect.value : agentSelect.value) || "").trim();
    rememberRunTarget(effectiveMode, targetValue, targetValue);
    rerenderRunTargetCards();
    stopWorkflowRunPolling(runState);
    stopAgentRunPolling(runState);
    resetRunState(runState);
    resetCollaborationState(collaborationState, targetValue);
    runState.currentRunType = effectiveMode;
    runState.currentWorkflowName = effectiveMode === "workflow" ? targetValue : "";
    runState.submitWorkflowInput = createWorkflowInputSubmitHandler(runState, messages, collaborationState, refreshRuntime);
    saveRunState(runState, { mode: effectiveMode, target: targetValue });
    appendTimelineEvent(messages, {
      tone: "stage",
      title: t("chat.requestSubmitted"),
      detail: compactTimelineText(rawInput || runState.currentWorkflowName || agentSelect.value || t("chat.run"))
    });
    prompt.value = "";
    setRunStatus(root, runStatus, send, true);
    syncRunContextGuide();
    setTimelineHint(runState, t("chat.timelineRunning"));
    setResultStatus(runState, t("chat.resultStreaming"));
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
    setCollaborationStatus(collaborationState, t("chat.collabRunning"));
    saveRunState(runState, { mode: effectiveMode, target: targetValue, running: true });
    runState.requestInFlight = true;
    runState.durableWorkflowActive = true;
    syncRunUnloadGuard(runState);
    try {
      const targetID = preflight?.execution_target?.id || "local";
      if (effectiveMode === "agent" && targetValue && targetValue !== runtime.active_agent) {
        await postJSON("/api/runtime/agent", { agent: targetValue });
      }
      if (effectiveMode === "workflow") {
        const response = await startWorkflowRun(targetValue, { type: runMode.value === "auto" ? "auto" : "workflow", input, attachments: taskAttachments, approve: false, background: true, target_id: targetID, reasoning_effort: reasoningEffort.value, permission_mode: permissionMode.value });
        await handleWorkflowBackgroundResponse(response, messages, runState, collaborationState, targetValue, "start");
        startWorkflowRunPolling(runState, collaborationState, {
          runID: runState.currentRunID,
          workflowName: targetValue,
          eventsURL: runEventsURLFromActionResponse(response)
        });
      } else {
        const response = await startAgentRun(input, {
          type: runMode.value === "auto" ? "auto" : "agent",
          agent: runMode.value === "auto" ? "" : targetValue,
          skill: runMode.value === "agent" ? skillSelect.value || "" : "",
          tool: runMode.value === "agent" ? toolSelect.value || "" : "",
          attachments: taskAttachments,
          target_id: targetID,
          reasoning_effort: reasoningEffort.value,
          permission_mode: permissionMode.value
        });
        await handleAgentBackgroundResponse(response, messages, runState, collaborationState, targetValue, "start");
        startAgentRunPolling(runState, collaborationState, {
          runID: runState.currentRunID,
          eventsURL: runEventsURLFromActionResponse(response)
        });
      }
      if (!currentRunStillActive(runState)) finalizeStreamingResult(runState);
      if (!currentRunStillActive(runState) && !runState.hasError && !runState.awaitingApproval && !runState.awaitingInput) {
        setTimelineHint(runState, t("chat.timelineDone"));
        setResultStatus(runState, runState.hasResult ? t("chat.resultReady") : t("chat.resultIdle"));
      }
      markCollaborationStale(collaborationState);
    } catch (error) {
      const message = chatDisplayText(error.message || t("chat.errorTitle"));
      finalizeStreamingResult(runState);
      appendTimelineEvent(messages, {
        tone: "error",
        title: t("chat.errorTitle"),
        detail: message
      });
      showRunAttention(runState, t("chat.errorTitle"), message, "error");
      setTimelineHint(runState, t("chat.timelineError"));
      setResultStatus(runState, t("chat.resultError"));
      setCollaborationStatus(collaborationState, t("chat.collabError"));
      runState.hasError = true;
    } finally {
      runState.requestInFlight = false;
      const runStillActive = currentRunStillActive(runState);
      if (!runStillActive) finalizeStreamingResult(runState);
      setRunStatus(root, runStatus, send, runStillActive, currentRunStatusLabel(runState));
      if (runState.currentRunType === "workflow" && !shouldPollWorkflowRunStatus(runState.lastWorkflowStatus)) {
        stopWorkflowRunPolling(runState);
      } else if (runState.currentRunType === "agent" && !shouldPollAgentRunStatus(runState.lastWorkflowStatus)) {
        stopAgentRunPolling(runState);
      }
      saveRunState(runState, { running: runStillActive });
      syncRunUnloadGuard(runState);
      syncRunContextGuide();
      refreshRuntime().catch(() => {});
      runState.historyRefresh?.();
    }
  };
  clear.onclick = () => {
    stopWorkflowRunPolling(runState);
    stopAgentRunPolling(runState);
    resetRunState(runState);
    clearSavedRunState();
    setTimelineHint(runState, t("chat.timelineIdle"));
    setResultStatus(runState, t("chat.resultIdle"));
    setRunStatus(root, runStatus, send, false, t("chat.ready"));
    setRunLiveStatus(runState, t("chat.realtimeIdle"), "idle");
    resetCollaborationState(collaborationState, "");
    markCollaborationStale(collaborationState);
    taskAttachments = [];
    for (const item of pendingAttachmentUploads) {
      if (item.previewURL) URL.revokeObjectURL(item.previewURL);
    }
    pendingAttachmentUploads = [];
    renderTaskAttachments();
    syncRunContextGuide();
  };
}

async function ensureWorkspaceRequirementForReference(feedback, suggestions) {
  suggestions.innerHTML = `<div class="item muted">${escapeHTML(t("chat.workspacePreflightChecking"))}</div>`;
  try {
    const requirement = await checkWorkspaceRequirement({ operation: "@file" });
    if (requirement?.blocked) {
      const message = workspaceRequirementBody(requirement);
      suggestions.innerHTML = `
        <div class="reference-empty-note warn">
          <strong>${escapeHTML(t("chat.workspacePreflightTitle"))}</strong>
          <span>${escapeHTML(message)}</span>
          <button type="button" data-open-workspace>${escapeHTML(t("chat.workspacePreflightAction"))}</button>
        </div>`;
      suggestions.querySelector("[data-open-workspace]")?.addEventListener("click", () => {
        location.hash = "workspace";
      });
      showContextFeedback(feedback, message, true);
      return false;
    }
    return true;
  } catch (error) {
    const message = chatDisplayText(error?.message || t("chat.workspacePreflightFailed"));
    suggestions.innerHTML = `<div class="reference-empty-note warn">${escapeHTML(message)}</div>`;
    showContextFeedback(feedback, message, true);
    return false;
  }
}

async function ensureWorkspaceRequirementBeforeRun({ root, runStatus, send, runState, messages, mode, input, workflowName, agentName }) {
  setRunStatus(root, runStatus, send, true, t("chat.workspacePreflightChecking"));
  try {
    const payload = mode === "workflow"
      ? { input, operation: "workflow", workflow: workflowName || "" }
      : { input, operation: "run", agent: agentName || "" };
    const requirement = await checkWorkspaceRequirement(payload);
    if (requirement?.blocked) {
      const message = workspaceRequirementBody(requirement);
      appendTimelineEvent(messages, {
        tone: "approval",
        title: t("chat.workspacePreflightTitle"),
        detail: message
      });
      showWorkspaceRequirementAttention(runState, requirement);
      setTimelineHint(runState, t("chat.workspacePreflightBlockedStatus"));
      setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
      return false;
    }
    return true;
  } catch (error) {
    const message = chatDisplayText(error?.message || t("chat.workspacePreflightFailed"));
    appendTimelineEvent(messages, {
      tone: "error",
      title: t("chat.workspacePreflightFailed"),
      detail: message
    });
    showRunAttention(runState, t("chat.workspacePreflightFailed"), message, "error");
    setTimelineHint(runState, t("chat.timelineError"));
    return false;
  } finally {
    setRunStatus(root, runStatus, send, false, t("chat.ready"));
  }
}

async function ensureWorkspaceRequirementBeforeWorkflowInput(runState, messages, request, values) {
  try {
    const requirement = await checkWorkspaceRequirement({
      operation: "workflow",
      input: JSON.stringify(values || {}),
      workflow: runState.currentWorkflowName || request?.workflow || ""
    });
    if (!requirement?.blocked) return true;
    const message = workspaceRequirementBody(requirement);
    appendTimelineEvent(messages, {
      tone: "approval",
      title: t("chat.workspacePreflightTitle"),
      detail: message
    });
    showWorkflowInputWorkspaceRequirement(runState, message);
    setTimelineHint(runState, t("chat.workspacePreflightBlockedStatus"));
    setResultStatus(runState, t("chat.resultBlocked"));
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
    return false;
  } catch (error) {
    const message = chatDisplayText(error?.message || t("chat.workspacePreflightFailed"));
    showWorkflowInputError(runState, message);
    appendTimelineEvent(messages, {
      tone: "error",
      title: t("chat.workspacePreflightFailed"),
      detail: message
    });
    return false;
  }
}

async function ensureWorkspaceRequirementBeforeAction(runState, action, runType = "") {
  const operation = workspaceOperationForAction(action, runType);
  if (!operation) return true;
  try {
    const requirement = await checkWorkspaceRequirement({
      operation,
      input: [workflowRunActionLabel(action.name, action.label), localizedWorkflowActionReason(action.reason || "")].filter(Boolean).join("\n"),
      workflow: runType === "workflow" ? runState.currentWorkflowName || "" : "",
      agent: runType === "agent" ? runState.currentStage || "" : ""
    });
    if (!requirement?.blocked) return true;
    appendTimelineEvent(runState.messages, {
      tone: "approval",
      title: t("chat.workspacePreflightTitle"),
      detail: workspaceRequirementBody(requirement)
    });
    showWorkspaceRequirementAttention(runState, requirement);
    setTimelineHint(runState, t("chat.workspacePreflightBlockedStatus"));
    setResultStatus(runState, t("chat.resultBlocked"));
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
    return false;
  } catch (error) {
    const message = chatDisplayText(error?.message || t("chat.workspacePreflightFailed"));
    appendTimelineEvent(runState.messages, {
      tone: "error",
      title: t("chat.workspacePreflightFailed"),
      detail: message
    });
    showRunAttention(runState, t("chat.workspacePreflightFailed"), message, "error");
    setTimelineHint(runState, t("chat.timelineError"));
    return false;
  }
}

function workspaceOperationForAction(action = {}, runType = "") {
  const name = String(action.name || "").toLowerCase();
  if (["approve_task_scope", "approve_tool", "approve_all_tools", "approve_remember_tool"].includes(name)) return "tool";
  if (["approve_stage", "resume_sub_workflow", "submit_input"].includes(name)) return "workflow";
  if (name === "retry") return runType === "workflow" ? "workflow" : "run";
  return "";
}

function positionReferencePicker(popover, toggle, root) {
  if (!popover || !toggle) return;
  const rect = toggle.getBoundingClientRect();
  const rootRect = root?.getBoundingClientRect?.() || rect;
  const viewportWidth = window.innerWidth || document.documentElement.clientWidth || 1024;
  const viewportHeight = window.innerHeight || document.documentElement.clientHeight || 720;
  const gap = 12;
  const gutter = 16;
  const availableRight = viewportWidth - rect.right - gap - gutter;
  const canUseSide = viewportWidth >= 980 && availableRight >= 320;
  const maxWidth = canUseSide
    ? Math.min(520, Math.max(320, availableRight))
    : Math.min(Math.max(rootRect.width, rect.width, 300), Math.max(260, viewportWidth - gutter * 2));
  const rawLeft = canUseSide ? rect.right + gap : Math.min(rect.left, viewportWidth - maxWidth - gutter);
  const left = Math.max(gutter, Math.min(rawLeft, viewportWidth - maxWidth - gutter));
  const rawTop = canUseSide ? rect.top - 2 : rect.bottom + 10;
  const maxHeight = Math.min(460, Math.max(260, viewportHeight - rawTop - gutter));
  const top = Math.max(gutter, Math.min(rawTop, viewportHeight - maxHeight - gutter));
  popover.dataset.placement = canUseSide ? "side" : "below";
  popover.style.setProperty("--reference-popover-left", `${Math.round(left)}px`);
  popover.style.setProperty("--reference-popover-top", `${Math.round(top)}px`);
  popover.style.setProperty("--reference-popover-width", `${Math.round(maxWidth)}px`);
  popover.style.setProperty("--reference-popover-max-height", `${Math.round(maxHeight)}px`);
}

async function loadReferenceFiles(prefix, target, prompt, feedback, onPick, selectionProvider, isCurrent) {
  const normalizedPrefix = String(prefix || "").trim();
  target.innerHTML = `<div class="item muted">${escapeHTML(t("chat.referenceLoading"))}</div>`;
  try {
    const params = new URLSearchParams({ limit: "200" });
    if (normalizedPrefix) {
      params.set("q", normalizedPrefix);
    } else {
      params.set("prefix", "");
    }
    const data = await request(`/api/workspace-files?${params.toString()}`);
    if (isCurrent?.() === false) return;
    target.innerHTML = "";
    const rawEntries = Array.isArray(data.entries) ? data.entries : [];
    const hiddenEntries = rawEntries.filter(entry => entry?.path && shouldHideReferenceEntry(entry, normalizedPrefix));
    const entries = rawEntries
      .filter(entry => entry?.path && !shouldHideReferenceEntry(entry, normalizedPrefix))
      .filter(entry => referenceEntryMatches(entry, normalizedPrefix));
    for (const entry of entries) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = entry.is_dir ? "reference-entry is-dir" : "reference-entry is-file";
      button.innerHTML = `<span>${entry.is_dir ? t("chat.referenceDir") : t("chat.referenceFile")}</span><strong>${escapeHTML(entry.path)}</strong>`;
      button.onclick = () => {
        insertTextAtCursor(prompt, `@${entry.path}`, selectionProvider?.());
        showContextFeedback(feedback, t("chat.referenceInserted", { path: entry.path }), false);
        onPick?.();
        prompt.focus();
      };
      target.appendChild(button);
    }
    if (target.innerHTML) {
      appendReferenceDiagnostics(target, data);
      showContextFeedback(feedback, "", false);
      return;
    }
    const message = referenceEmptyMessage(data, hiddenEntries, rawEntries, normalizedPrefix);
    target.innerHTML = `<div class="reference-empty-note">${escapeHTML(message)}${referenceMetaHTML(data)}</div>`;
    showContextFeedback(feedback, message, true);
  } catch (error) {
    if (isCurrent?.() === false) return;
    const message = error?.status === 428
      ? t("chat.referenceWorkspaceRequired")
      : chatDisplayText(error.message || t("chat.referenceLoadFailed"));
    target.innerHTML = `<div class="reference-empty-note warn">${escapeHTML(message)}</div>`;
    showContextFeedback(feedback, message, true);
  }
}

function appendReferenceDiagnostics(target, data = {}) {
  const notes = [];
  if (data.truncated) notes.push({ tone: "", text: t("chat.referenceTruncated") });
  if (data.traversal_truncated) notes.push({ tone: "warn", text: t("chat.referenceTraversalTruncated", { count: referenceNumberText(data.visited || 0) }) });
  if (Array.isArray(data.ignored_dirs) && data.ignored_dirs.length) {
    notes.push({ tone: "", text: t("chat.referenceIgnoredDirs", { dirs: data.ignored_dirs.slice(0, 4).join(", ") }) });
  }
  for (const note of notes.slice(0, 3)) {
    const node = document.createElement("div");
    node.className = `reference-result-note ${note.tone}`.trim();
    node.textContent = note.text;
    target.appendChild(node);
  }
}

function referenceEmptyMessage(data = {}, hiddenEntries = [], rawEntries = [], normalizedPrefix = "") {
  if (data.traversal_truncated) return t("chat.referenceTraversalEmpty", { count: referenceNumberText(data.visited || 0) });
  if (hiddenEntries.length && hiddenEntries.length === rawEntries.length) {
    return t("chat.referenceHiddenOnly");
  }
  return normalizedPrefix
    ? t("chat.referenceEmptyWithHint")
    : t("chat.referenceEmpty");
}

function referenceMetaHTML(data = {}) {
  const meta = [
    data.search_mode ? t("chat.referenceSearchMode", { mode: referenceSearchModeLabel(data.search_mode) }) : "",
    data.visited ? t("chat.referenceVisited", { count: referenceNumberText(data.visited) }) : "",
    Array.isArray(data.ignored_dirs) && data.ignored_dirs.length ? t("chat.referenceIgnoredDirs", { dirs: data.ignored_dirs.slice(0, 4).join(", ") }) : ""
  ].filter(Boolean);
  return meta.length ? `<span>${escapeHTML(meta.join(" / "))}</span>` : "";
}

function referenceNumberText(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? number.toLocaleString() : "0";
}

function referenceSearchModeLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  const key = `chat.referenceSearchMode.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function shouldHideReferenceEntry(entry, prefix) {
  if (String(prefix || "").trim()) return false;
  const blocked = new Set([".git", ".venv", "node_modules", "__pycache__", "vendor", "dist", "build"]);
  const first = String(entry?.path || "").split(/[\\/]/)[0];
  return blocked.has(first);
}

function referenceEntryMatches(entry, prefix) {
  const query = String(prefix || "").trim().toLowerCase();
  if (!query) return true;
  const path = String(entry?.path || "").toLowerCase();
  return path.startsWith(query) || path.includes(query);
}

function insertTextAtCursor(textarea, text, selection) {
  const value = textarea.value || "";
  const fallback = value.length;
  const rawStart = Number.isFinite(selection?.start) ? selection.start : textarea.selectionStart;
  const rawEnd = Number.isFinite(selection?.end) ? selection.end : textarea.selectionEnd;
  const start = Math.max(0, Math.min(value.length, Number.isFinite(rawStart) ? rawStart : fallback));
  const end = Math.max(start, Math.min(value.length, Number.isFinite(rawEnd) ? rawEnd : start));
  const before = value.slice(0, start);
  const after = value.slice(end);
  const prefix = before && !/[\s([{]$/.test(before) ? " " : "";
  const suffix = after && !/^[\s,.;:)\]}]/.test(after) ? " " : "";
  const insert = `${prefix}${text}${suffix}`;
  textarea.value = `${before}${insert}${after}`;
  const caret = before.length + insert.length;
  textarea.setSelectionRange(caret, caret);
  textarea.dispatchEvent(new Event("input", { bubbles: true }));
}

async function safeRequest(path, fallback) {
  try {
    return await request(path);
  } catch {
    return fallback;
  }
}

function fillSelect(select, values, emptyLabel) {
  select.innerHTML = "";
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = emptyLabel || "-";
  select.appendChild(empty);
  for (const value of values || []) {
    const option = document.createElement("option");
    if (value && typeof value === "object") {
      option.value = value.value || value.name || "";
      option.textContent = value.label || value.name || value.value || "";
      if (value.title) option.title = value.title;
      if (value.disabled) option.disabled = true;
    } else {
      option.value = value;
      option.textContent = value;
    }
    select.appendChild(option);
  }
}

function readResourceTaskDraft() {
  try {
    const raw = localStorage.getItem(resourceTaskDraftKey);
    if (!raw) return null;
    localStorage.removeItem(resourceTaskDraftKey);
    const draft = JSON.parse(raw);
    return draft && typeof draft === "object" ? draft : null;
  } catch {
    localStorage.removeItem(resourceTaskDraftKey);
    return null;
  }
}

function applyResourceTaskDraft(draft, controls) {
  const kind = String(draft?.kind || "").toLowerCase();
  const name = String(draft?.name || "").trim();
  if (!kind || !name) return false;
  if ((kind === "agent" || kind === "subagent") && selectHasValue(controls.agentSelect, name)) {
    controls.runMode.value = "agent";
    controls.agentSelect.value = name;
    return true;
  }
  if (kind === "workflow" && selectHasValue(controls.workflowSelect, name)) {
    controls.runMode.value = "workflow";
    controls.workflowSelect.value = name;
    return true;
  }
  if (kind === "skill" && selectHasValue(controls.skillSelect, name)) {
    controls.skillSelect.value = name;
    return true;
  }
  if (kind === "mcp") {
    const prefix = `${name.toLowerCase()}/`;
    const option = [...controls.toolSelect.options].find(candidate => String(candidate.value || "").toLowerCase().startsWith(prefix));
    if (option) {
      controls.toolSelect.value = option.value;
      return true;
    }
  }
  return false;
}

function selectHasValue(select, value) {
  return [...(select?.options || [])].some(option => option.value === value);
}

function renderRunTargetCards(container, options = {}) {
  if (!container) return;
  const cards = runTargetCardItems(options.runtime, options.workflowEntries);
  if (!cards.length) {
    container.innerHTML = `<div class="run-target-empty">${escapeHTML(t("chat.targetCardsEmpty"))}</div>`;
    return;
  }
  container.innerHTML = cards.map(card => `
    <button type="button" class="run-target-card" data-run-card-mode="${escapeHTML(card.mode)}" data-run-card-value="${escapeHTML(card.value)}" aria-pressed="false">
      <span class="run-target-card-icon" aria-hidden="true">${runTargetCardIconHTML(card.mode)}</span>
      <span class="run-target-card-copy">
        <strong>${escapeHTML(card.title)}</strong>
        <small>${escapeHTML(card.help)}</small>
      </span>
      <span class="run-target-card-badges">
        ${card.recent ? `<span class="run-target-card-recent">${escapeHTML(t("chat.targetCardRecent"))}</span>` : ""}
        <span class="run-target-card-kind">${escapeHTML(card.kind)}</span>
      </span>
    </button>`).join("");
  container.querySelectorAll(".run-target-card").forEach(button => {
    button.addEventListener("click", () => {
      const mode = button.dataset.runCardMode || "agent";
      const value = button.dataset.runCardValue || "";
      options.runMode.value = mode;
      if (mode === "workflow" && options.workflowSelect) {
        options.workflowSelect.value = value;
      } else if (options.agentSelect) {
        options.agentSelect.value = value;
      }
      options.onSelect?.();
    });
  });
  updateRunTargetCards(container, options.runMode?.value, options.agentSelect?.value, options.workflowSelect?.value);
}

function runTargetCardItems(runtime = {}, workflowEntries = []) {
  const items = [];
  const seen = new Set();
  const recent = readRecentRunTargets();
  const add = item => {
    if (!item?.value) return;
    const key = `${item.mode}:${item.value}`;
    if (seen.has(key)) return;
    seen.add(key);
    const recentItem = recent.find(entry => entry.key === key);
    items.push({
      ...item,
      recent: Boolean(recentItem),
      recentUpdatedAt: recentItem?.updatedAt || 0,
      recentCount: recentItem?.count || 0
    });
  };
  const agents = Array.isArray(runtime.agents) && runtime.agents.length
    ? runtime.agents
    : (runtime.active_agent ? [{ id: runtime.active_agent, mode: runtime.mode || "" }] : []);
  const active = runtime.active_agent || agents[0]?.id || "";
  const sortedAgents = sortRunTargetsByRecent(agents.map(agent => ({
    raw: agent,
    key: `agent:${agent.id || agent.name || ""}`,
    active: (agent.id || agent.name || "") === active
  })), recent).map(item => item.raw);
  sortedAgents.slice(0, 3).forEach(agent => {
    const name = agent.id || agent.name || "";
    add({
      mode: "agent",
      value: name,
      title: agent.name || name || t("chat.modeAgent"),
      help: agent.description || t(agent.id === active ? "chat.targetCardActiveAgentHelp" : "chat.targetCardAgentHelp", { mode: modeLabel(agent.mode) }),
      kind: t("chat.modeAgent")
    });
  });
  sortRunTargetsByRecent((workflowEntries || []).filter(item => item?.value).map(item => ({
    raw: item,
    key: `workflow:${item.value}`,
    active: false
  })), recent).map(item => item.raw).slice(0, 4).forEach(item => {
    add({
      mode: "workflow",
      value: item.value,
      title: item.label || item.name || item.value,
      help: item.title || t("chat.targetCardWorkflowHelp"),
      kind: t("chat.modeWorkflow")
    });
  });
  return items.sort((a, b) => runTargetCardSortScore(b) - runTargetCardSortScore(a)).slice(0, 2);
}

function sortRunTargetsByRecent(items = [], recent = []) {
  const byKey = new Map(recent.map((item, index) => [item.key, { ...item, index }]));
  return [...items].sort((a, b) => {
    const recentA = byKey.get(a.key);
    const recentB = byKey.get(b.key);
    if (recentA || recentB) {
      return (recentB?.updatedAt || 0) - (recentA?.updatedAt || 0)
        || (recentB?.count || 0) - (recentA?.count || 0)
        || (recentA?.index ?? 999) - (recentB?.index ?? 999);
    }
    if (a.active !== b.active) return a.active ? -1 : 1;
    return 0;
  });
}

function runTargetCardSortScore(card = {}) {
  return (card.recentUpdatedAt || 0) + (card.recentCount || 0) * 1000 + (card.recent ? 10000000000000 : 0);
}

function readRecentRunTargets() {
  try {
    const raw = JSON.parse(localStorage.getItem(runTargetRecentStorageKey) || "[]");
    return Array.isArray(raw)
      ? raw
        .filter(item => item && typeof item === "object" && item.key && item.mode && item.value)
        .sort((a, b) => Number(b.updatedAt || 0) - Number(a.updatedAt || 0))
        .slice(0, 12)
      : [];
  } catch {
    return [];
  }
}

function rememberRunTarget(mode, value, label = "") {
  const normalizedMode = mode === "workflow" ? "workflow" : "agent";
  const normalizedValue = String(value || "").trim();
  if (!normalizedValue) return;
  try {
    const key = `${normalizedMode}:${normalizedValue}`;
    const current = readRecentRunTargets();
    const previous = current.find(item => item.key === key);
    const next = [
      {
        key,
        mode: normalizedMode,
        value: normalizedValue,
        label: label || normalizedValue,
        count: Math.min(999, Number(previous?.count || 0) + 1),
        updatedAt: Date.now()
      },
      ...current.filter(item => item.key !== key)
    ].slice(0, 12);
    localStorage.setItem(runTargetRecentStorageKey, JSON.stringify(next));
  } catch {
    // Ignore storage failures; recommendation cards can still render.
  }
}

function updateRunTargetCards(container, mode, agentValue, workflowValue) {
  if (!container) return;
  const current = mode === "workflow" ? workflowValue : agentValue;
  container.querySelectorAll(".run-target-card").forEach(button => {
    const active = button.dataset.runCardMode === mode && button.dataset.runCardValue === current;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  });
}

function renderTaskExecutionContext(container, runtime = {}, selection = {}) {
  if (!container) return;
  const preflight = selection.preflight && typeof selection.preflight === "object" ? selection.preflight : {};
  const orchestration = Array.isArray(preflight.orchestration) ? preflight.orchestration : [];
  const plannedWorkflow = preflight.kind === "workflow" ? String(preflight.target || "").trim() : String(selection.workflow || "").trim();
  const agents = Array.isArray(runtime.agents) ? runtime.agents : [];
  const plannedAgent = preflight.kind === "agent" ? String(preflight.target || "").trim() : "";
  const firstPlannedAgent = orchestration.find(step => String(step?.agent || "").trim())?.agent || "";
  const agentID = plannedAgent || firstPlannedAgent || selection.agent || runtime.active_agent || agents[0]?.id || "-";
  const agent = agents.find(item => item.id === agentID) || {};
  const providers = Array.isArray(runtime.providers) ? runtime.providers : [];
  const provider = providers.find(item => item.id === agent.provider || item.provider === agent.provider) || {};
  const workspace = runtime.workspace || {};
  const workspaceValue = workspace.display || workspace.root || t("chat.executionContextWorkspaceDefault");
  const providerValue = provider.id || agent.provider || t("chat.executionContextProviderUnset");
  const modelValue = provider.model || agent.model || t("chat.executionContextModelUnset");
  const orchestrationAgents = [...new Set(orchestration.map(step => String(step?.agent || "").trim()).filter(Boolean))];
  const agentValue = plannedWorkflow && orchestrationAgents.length
    ? t("chat.orchestrationAgentCount", { count: orchestrationAgents.length })
    : taskResourceDisplayName("agent", agentID, agent.name || agentID);
  const reasoningValue = reasoningEffortLabel(selection.reasoningEffort || provider.reasoning_effort || agent.reasoning_effort || "");
  const permissionValue = permissionModeLabel(selection.permissionMode || "confirm");
  const orchestrationSkills = [...new Set(orchestration.map(step => String(step?.skill || "").trim()).filter(Boolean))];
  const resources = [
    plannedWorkflow ? `${t("chat.workflow")}: ${taskResourceDisplayName("workflow", plannedWorkflow, plannedWorkflow)}` : "",
    selection.skill ? `${t("chat.skill")}: ${taskResourceDisplayName("skill", selection.skill, selection.skill)}` : "",
    !selection.skill && orchestrationSkills.length ? `${t("chat.skill")}: ${orchestrationSkills.map(value => taskResourceDisplayName("skill", value, value)).join(" / ")}` : "",
    selection.tool ? `${t("chat.tool")}: ${taskToolDisplayName(selection.tool)}` : ""
  ].filter(Boolean).join(" · ") || t("chat.executionContextResourcesAuto");
  const executionTargets = Array.isArray(runtime.execution_targets) ? runtime.execution_targets : [];
  const executionTarget = selection.preflight?.execution_target
    || executionTargets.find(item => item.id === selection.targetID)
    || executionTargets[0]
    || { id: "local" };
  const requiresExecutionTarget = Boolean(selection.preflight?.requires_execution_target)
    || taskSelectionRequiresExecutionTarget(runtime, selection)
    || Boolean(selection.targetID && selection.targetID !== "local");
  const executionTargetValue = executionTarget.id === "local" ? t("chat.executionContextTargetLocal") : executionTarget.display_name || executionTarget.id;
  const items = [
    [t("chat.executionContextWorkspace"), workspaceValue, workspace.confirmed ? "ready" : "attention"],
    ...(requiresExecutionTarget ? [[t("chat.executionContextTarget"), executionTargetValue, "ready"]] : []),
    [t("chat.executionContextAgent"), agentValue, ""],
    [t("chat.executionContextModel"), `${modelValue} · ${providerValue}`, provider.api_key_set === false ? "attention" : ""],
    [t("chat.executionContextReasoning"), reasoningValue, ""],
    [t("chat.executionContextPermission"), permissionValue, ""],
    [t("chat.executionContextResources"), resources, ""]
  ];
  const chips = items.map(([label, value, tone]) => `<span class="task-context-chip ${tone}"><small>${escapeHTML(label)}</small><strong title="${escapeHTML(value)}">${escapeHTML(value)}</strong></span>`).join("");
  container.innerHTML = `${chips}${taskOrchestrationPlanHTML(orchestration, preflight.reason || "", plannedWorkflow)}`;
}

function taskOrchestrationPlanHTML(steps = [], reason = "", workflow = "") {
  if (!Array.isArray(steps) || !steps.length) return "";
  const rows = steps.map((step, index) => {
    const stage = chatDisplayText(step.stage || t("chat.orchestrationStage", { index: index + 1 }));
    const agent = String(step.agent || "").trim();
    const skill = String(step.skill || "").trim();
    const resources = [
      agent ? taskResourceDisplayName("agent", agent, localizedText(agent)) : "",
      skill ? taskResourceDisplayName("skill", skill, localizedText(skill)) : ""
    ].filter(Boolean).join(" · ");
    const outcome = [chatDisplayText(step.purpose || ""), step.expected_output ? `${t("chat.orchestrationOutput")}: ${chatDisplayText(step.expected_output)}` : ""].filter(Boolean).join(" ");
    return `<li data-orchestration-stage="${escapeHTML(String(step.stage || ""))}" data-orchestration-agent="${escapeHTML(agent)}" data-orchestration-skill="${escapeHTML(skill)}">
      <span class="task-orchestration-index">${index + 1}</span>
      <span class="task-orchestration-copy"><strong>${escapeHTML(stage)}</strong>${resources ? `<small>${escapeHTML(resources)}</small>` : ""}${outcome ? `<p>${escapeHTML(outcome)}</p>` : ""}</span>
    </li>`;
  }).join("");
  return `<details class="task-orchestration-plan" data-orchestration-workflow="${escapeHTML(workflow)}" data-orchestration-agent-count="${new Set(steps.map(step => String(step?.agent || "").trim()).filter(Boolean)).size}" open>
    <summary><span><strong>${escapeHTML(t("chat.orchestrationTitle"))}</strong><small>${escapeHTML(reason || t("chat.orchestrationHelp"))}</small></span><em>${escapeHTML(t("chat.orchestrationSteps", { count: steps.length }))}</em></summary>
    <ol>${rows}</ol>
  </details>`;
}

function runtimeReasoningEffort(runtime = {}) {
  const agents = Array.isArray(runtime.agents) ? runtime.agents : [];
  const agent = agents.find(item => item.id === runtime.active_agent) || agents[0] || {};
  const providers = Array.isArray(runtime.providers) ? runtime.providers : [];
  const provider = providers.find(item => item.id === agent.provider || item.provider === agent.provider) || providers[0] || {};
  const normalized = String(provider.reasoning_effort || agent.reasoning_effort || "mid").toLowerCase();
  if (["minimal", "low"].includes(normalized)) return "low";
  if (["high", "xhigh", "max", "heavy"].includes(normalized)) return "high";
  return "mid";
}

function reasoningEffortKey(runtime = {}) {
  const agents = Array.isArray(runtime.agents) ? runtime.agents : [];
  const agent = agents.find(item => item.id === runtime.active_agent) || agents[0] || {};
  const providers = Array.isArray(runtime.providers) ? runtime.providers : [];
  const provider = providers.find(item => item.id === agent.provider || item.provider === agent.provider) || providers[0] || {};
  return `${reasoningEffortStorageKey}:${JSON.stringify([provider.id || agent.provider || "", agent.model || provider.model || "", runtimeReasoningEffort(runtime)])}`;
}

function storedRunControl(key, fallback) {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
}

function permissionModeLabel(value) {
  if (value === "full") return t("chat.permissionFull");
  if (value === "workspace") return t("chat.permissionWorkspace");
  return t("chat.permissionConfirm");
}

function permissionModeHelp(value) {
  if (value === "full") return t("chat.permissionFullHelp");
  if (value === "workspace") return t("chat.permissionWorkspaceHelp");
  return t("chat.permissionConfirmHelp");
}

function reasoningEffortLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) return t("chat.executionContextReasoningUnset");
  if (["mid", "med", "medium"].includes(normalized)) return t("chat.reasoningMedium");
  if (["minimal", "low"].includes(normalized)) return t("chat.reasoningLow");
  if (["high", "xhigh", "max", "heavy"].includes(normalized)) return t("chat.reasoningHigh");
  return localizedText(normalized);
}

function populateExecutionTargetSelect(select, runtime = {}, preferredValue = "") {
  if (!select) return;
  const targets = Array.isArray(runtime.execution_targets) && runtime.execution_targets.length
    ? runtime.execution_targets
    : [{ id: "local", kind: "local", display_name: t("chat.executionContextTargetLocal"), enabled: true }];
  let stored = "";
  try {
    stored = localStorage.getItem(executionTargetStorageKey) || "";
  } catch {
    stored = "";
  }
  const requested = preferredValue || stored || "local";
  select.innerHTML = targets.map(target => {
    const disabled = target.enabled === false;
    const label = target.id === "local"
      ? t("chat.executionContextTargetLocal")
      : target.display_name || target.id;
    const suffix = disabled ? ` (${t("chat.executionTargetDisabled")})` : "";
    return `<option value="${escapeHTML(target.id || "")}" ${disabled ? "disabled" : ""}>${escapeHTML(label + suffix)}</option>`;
  }).join("");
  const requestedOption = Array.from(select.options).find(option => option.value === requested && !option.disabled);
  select.value = requestedOption?.value || Array.from(select.options).find(option => !option.disabled)?.value || "local";
}

function taskSelectionRequiresExecutionTarget(runtime = {}, selection = {}) {
  const targetScoped = value => String(value || "").trim().toLowerCase() === "host";
  const agents = Array.isArray(runtime.agents) ? runtime.agents : [];
  const skills = Array.isArray(runtime.skills) ? runtime.skills : [];
  const agent = agents.find(item => item.id === selection.agent);
  const skill = skills.find(item => item.name === selection.skill);
  return targetScoped(agent?.target_scope) || targetScoped(skill?.target_scope);
}

function runTargetCardIconHTML(mode) {
  return icon(mode === "workflow" ? "workflow" : "bot");
}

function workflowExecutorEntries(options = {}, workflowGraphs = []) {
  const executors = Array.isArray(options.workflow_executors) ? options.workflow_executors : [];
  const source = executors.length ? executors : (workflowGraphs || []).filter(item => item.valid !== false);
  return source
    .filter(item => item && item.valid !== false && item.name)
    .map(item => {
      const suffix = item.legacy || item.compatibility ? ` (${t("chat.workflowCompatibility")})` : "";
      return {
        value: item.name,
        name: item.name,
        label: `${taskResourceDisplayName("workflow", item.name, item.name)}${suffix}`,
        title: item.detail || item.description || ""
      };
    });
}

function taskResourceDisplayName(kind, name, fallback = "") {
  if (currentLanguage() !== "zh") return fallback || name;
  const labels = {
    "agent:auditor": "质量审计智能体",
    "agent:binary-analyst": "二进制分析智能体",
    "agent:chat": "通用任务智能体",
    "agent:documentation-specialist": "办公文档智能体",
    "agent:fixer": "代码修复智能体",
    "agent:framework-extension-architect": "平台扩展架构智能体",
    "agent:operations-specialist": "网络运维智能体",
    "agent:planner": "任务规划智能体",
    "agent:quality-engineer": "质量验证智能体",
    "agent:research-specialist": "互联网资料研究智能体",
    "agent:security-researcher": "网络安全智能体",
    "agent:software-engineer": "软件开发智能体",
    "agent:source-verifier": "来源核验智能体",
    "agent:support-specialist": "问题支持智能体",
    "agent:web-security-researcher": "Web 安全智能体",
    "workflow:multi-domain-intake-router": "多领域复杂任务协作",
    "workflow:plan-fix-audit": "规划、实现与质量审计",
    "workflow:plan-implement-audit": "规划、实现与最终验收",
    "workflow:skill-chain": "专业技能链协作",
    "workflow:task-decomposition-plan": "复杂任务拆解规划",
    "skill:authorized-red-team-validation": "授权红队验证",
    "skill:binary-vulnerability-research": "二进制漏洞研究",
    "skill:code-audit": "代码审计",
    "skill:code-writing": "代码实现",
    "skill:controlled-operations": "受控运维操作",
    "skill:execution-plan": "实施计划",
    "skill:internet-research": "互联网资料研究",
    "skill:local-host-diagnostics": "本机诊断",
    "skill:managed-local-config": "受管本地配置",
    "skill:office-document-production": "办公文档制作",
    "skill:remote-host-diagnostics": "远程主机诊断",
    "skill:source-verification": "来源核验",
    "skill:reverse-engineering": "逆向工程",
    "skill:vulnerability-research": "漏洞研究",
    "skill:web-vulnerability-research": "Web 漏洞研究"
  };
  return labels[`${kind}:${name}`] || fallback || name;
}

function taskToolDisplayName(value) {
  const raw = String(value || "").trim();
  if (!raw || currentLanguage() !== "zh") return raw;
  const name = raw.toLowerCase().split("/").pop();
  const labels = {
    apply_patch: "应用精确补丁",
    binary_entropy_map: "二进制熵图",
    binary_extract_window: "提取二进制片段",
    binary_file_info: "二进制文件信息",
    binary_format_summary: "二进制格式摘要",
    binary_strings: "提取二进制字符串",
    binary_symbol_hints: "二进制符号提示",
    browser_probe_points: "浏览器探测面",
    browser_snapshot: "浏览器快照",
    compile_research_evidence: "编译研究证据",
    create_docx: "创建 Word 文档",
    create_pdf: "创建 PDF 文档",
    create_pptx: "创建演示文稿",
    create_xlsx: "创建 Excel 工作簿",
    delete_file: "删除文件",
    device_batch_command_plan: "批量设备命令计划",
    device_command_plan: "设备命令计划",
    device_config_dry_run: "设备配置干跑",
    device_discovery_plan: "设备发现计划",
    device_restconf_live_apply: "应用设备 RESTCONF 变更",
    disk_usage: "磁盘使用情况",
    fetch_page_assets: "获取页面资源",
    fetch_url: "获取网页",
    file_info: "文件信息",
    hex_preview: "十六进制预览",
    host_facts: "主机事实",
    inspect_managed_json: "检查受管 JSON 配置",
    inspect_office_document: "检查办公文档",
    json_read: "读取 JSON",
    list_dir: "列出目录",
    list_ports: "列出端口",
    list_processes: "列出进程",
    list_tree: "列出文件树",
    markdown_link_check: "检查 Markdown 链接",
    mkdir: "创建目录",
    move_path: "移动路径",
    note_read: "读取笔记",
    note_write: "写入笔记",
    python_outline: "分析 Python 结构",
    read_file: "读取文件",
    read_file_range: "分段读取文件",
    read_log_tail: "读取日志尾部",
    redaction_check: "检查敏感信息",
    research_snapshot: "批量研究快照",
    render_office_preview: "生成文档预览",
    replace_office_text: "修改办公文档文本",
    replace_text: "替换文本",
    restore_file_change: "恢复文件修改",
    restore_managed_json_change: "恢复受管 JSON 配置",
    office_capabilities: "检测办公套件能力",
    export_office_pdf: "导出办公文档 PDF",
    fill_office_template: "基于模板生成文档",
    run_command: "运行命令",
    start_process: "启动开发服务",
    process_status: "查看服务状态",
    stop_process: "停止开发服务",
    run_script: "运行 Skill 脚本",
    search_files: "搜索文件",
    service_status: "服务状态",
    set_managed_json_value: "修改受管 JSON 配置",
    ssh_host_key_scan: "扫描 SSH 主机指纹",
    ssh_run_command: "运行 SSH 白名单命令",
    support_case_summary: "汇总支持案例",
    validate_office_document: "校验办公文档质量",
    web_search: "搜索互联网",
    write_file: "写入文件"
  };
  return labels[name] || raw;
}

function taskMachineDisplayName(value) {
  const raw = String(value || "").trim();
  if (!raw || currentLanguage() !== "zh") return raw;
  const labels = {
    completed: "已完成",
    denied: "已拒绝",
    exec: "执行",
    failed: "失败",
    inspect: "检查",
    modify: "修改",
    network: "网络",
    read: "读取",
    running: "运行中",
    started: "已启动",
    starting: "正在启动",
    succeeded: "已成功",
    write: "写入"
  };
  const direct = labels[raw.toLowerCase()];
  if (direct) return direct;
  const tool = taskToolDisplayName(raw);
  if (tool !== raw) return tool;
  return taskResourceDisplayName("agent", raw, raw);
}

function modeLabel(value) {
  const raw = String(value || "");
  if (!raw) return "-";
  const key = `catalog.mode.${raw}`;
  const translated = t(key);
  return translated === key ? raw : translated;
}

function runModeLabel(value) {
  if (value === "auto") return t("chat.modeAuto");
  if (value === "workflow") return t("chat.modeWorkflow");
  if (value === "agent") return t("chat.modeAgent");
  return value || "-";
}

function chatDisplayText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (/^(?:provider (?:response|request)|workflow stage .+: llm chat:|llm chat:|send Responses stream request:|read Responses stream:)/i.test(text)) return localizedText(text);
  if (chatLooksTechnical(text)) return text;
  return localizedText(text);
}

function chatLooksTechnical(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return true;
  if (text.includes("\\") || text.includes("://")) return true;
  if (text.includes("/") && !/\s/.test(text)) return true;
  if (text.includes(".") && !/\s/.test(text)) return true;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(text)) return true;
  return false;
}

function contextDirectives(root) {
  const skill = root.querySelector("#skillSelect").value;
  const tool = root.querySelector("#toolSelect").value;
  const lines = [];
  if (skill) lines.push(`Use skill: ${skill}`);
  if (tool) lines.push(`Prefer tool: ${tool}`);
  return lines;
}

function updateRunContextGuide(root, selection = {}) {
  const target = root.querySelector("#runContextGuide");
  if (!target) return;
  const runtime = selection.runtime || {};
  const guideItems = runContextGuideItems(selection, runtime);
  const actionItems = runContextActionItems(selection, runtime);
  const items = [...actionItems, ...guideItems].map(runContextGuideItem).filter(Boolean);
  target.classList.toggle("hidden", !items.length);
  target.innerHTML = items.join("");
}

function runContextGuideItems(selection = {}, runtime = {}) {
  const items = [];
  if (selection.mode === "workflow") {
    items.push({ kind: "target", title: t("chat.runContextGuideWorkflow"), body: t("chat.runContextGuideWorkflowHelp") });
  }
  if (selection.skill) {
    items.push({ kind: "skill", title: t("chat.runContextGuideSkill"), body: t("chat.runContextGuideSkillHelp", { skill: taskResourceDisplayName("skill", selection.skill, selection.skill) }) });
  }
  if (selection.tool) {
    items.push({ kind: "tool", title: t("chat.runContextGuideTool"), body: t("chat.runContextGuideToolHelp", { tool: taskToolDisplayName(selection.tool) }) });
  }
  const latestCost = runtime?.cost?.latest || runtime?.session?.prompt_budget || null;
  const historySamples = Number(runtime?.cost?.samples || 0);
  if (latestCost || historySamples) {
    const tokens = Number(latestCost?.estimated_prompt_tokens || runtime?.cost?.average_estimated_prompt_tokens || 0);
    items.push({
      kind: "cost",
      title: t("chat.runContextGuideCost"),
      body: tokens ? t("chat.runContextGuideCostHelp", { tokens: tokenCountText(tokens) }) : t("chat.runContextGuideCostHelpNoData")
    });
  }
  return items;
}

function runContextActionItems(selection = {}, runtime = {}) {
  const items = [];
  const workspace = runtime?.workspace || {};
  const needsWorkspace = workspace.root && !workspace.confirmed && runPromptLooksWorkspaceScoped(selection.promptValue);
  if (needsWorkspace) {
    items.push({
      kind: "workspace action",
      title: t("chat.runContextWorkspaceTitle"),
      body: t("chat.runContextWorkspaceBody"),
      action: t("chat.workspacePreflightAction"),
      actionName: "workspace"
    });
  }
  if (!selection.referenceOpen && runPromptLooksFileScoped(selection.promptValue)) {
    items.push({
      kind: "file action",
      title: t("chat.runContextFilesTitle"),
      body: t("chat.runContextFilesBody"),
      action: t("chat.referenceFiles"),
      actionName: "files"
    });
  }
  const latestCost = runtime?.cost?.latest || runtime?.session?.prompt_budget || null;
  const hasCostWarning = Array.isArray(runtime?.cost?.recommendations) && runtime.cost.recommendations.length > 0;
  if (latestCost || hasCostWarning) {
    items.push({
      kind: "cost action",
      title: t("chat.runContextCostTitle"),
      body: hasCostWarning ? t("chat.runContextCostBodyWarnings", { count: runtime.cost.recommendations.length }) : t("chat.runContextCostBody"),
      action: t("chat.runContextualOpenContext"),
      actionName: "cost"
    });
  }
  return items;
}

function runPromptLooksWorkspaceScoped(value = "") {
  const text = String(value || "").toLowerCase();
  if (!text.trim()) return false;
  return /(^|\s)@[\w./\\-]+/.test(text) || /(file|folder|workspace|repo|project|code|test|build|run|edit|write|delete|read|diff|文件|目录|工作区|项目|代码|测试|构建|运行|修改|写入|删除|读取)/i.test(text);
}

function runPromptLooksFileScoped(value = "") {
  const text = String(value || "");
  if (!text.trim()) return false;
  return /(^|\s)@[\w./\\-]+/.test(text) || /(\b[\w.-]+\.(go|js|ts|tsx|jsx|py|md|yaml|yml|json|css|html)\b|文件|目录|路径|代码)/i.test(text);
}

function runPendingApprovalCount(runtime = {}, runState = null) {
  const pending = Array.isArray(runtime?.session?.pending_approvals) ? runtime.session.pending_approvals.length : 0;
  const workflowRuns = normalizeWorkflowRunList(runtime?.session?.workflow_runs);
  const agentRuns = normalizeAgentRunList(runtime?.session?.agent_runs);
  const pausedRuns = [...workflowRuns, ...agentRuns].filter(run => isApprovalStatus(run?.status) || String(run?.status || "").toLowerCase() === "awaiting_tool_approval").length;
  const current = runState?.awaitingApproval ? 1 : 0;
  return Math.max(pending, pausedRuns, current);
}

function runContextGuideItem(item) {
  if (!item?.title && !item?.body) return "";
  const action = item.actionName
    ? `<button type="button" data-run-context-action="${escapeHTML(item.actionName)}">${escapeHTML(item.action || t("chat.runContextActionOpen"))}</button>`
    : "";
  return `<span class="run-context-guide-item ${escapeHTML(item.kind || "")}">
    <i aria-hidden="true"></i>
    <strong>${escapeHTML(item.title || "")}</strong>
    <small>${escapeHTML(item.body || "")}</small>
    ${action}
  </span>`;
}

function setRunStatus(root, runStatus, send, running, labelText = "") {
  root.querySelector(".chat-panel")?.classList.toggle("is-running", running);
  if (runStatus) {
    runStatus.classList.toggle("running", running);
    runStatus.classList.toggle("idle", !running);
    const label = runStatus.querySelector("span");
    if (label) label.textContent = labelText || (running ? t("chat.running") : t("chat.ready"));
  }
  send.disabled = running;
  send.setAttribute("aria-disabled", running ? "true" : "false");
  send.setAttribute("aria-busy", running ? "true" : "false");
  send.textContent = running ? t("chat.running") : t("chat.run");
}

function currentRunStatusLabel(state) {
  if (state.awaitingInput) return t("chat.timelineInput");
  if (state.awaitingApproval) return t("chat.timelineApproval");
  if (state.hasError) return t("chat.timelineError");
  const label = state.runTimelineHint?.textContent || "";
  return label && label !== t("chat.timelineIdle") ? label : "";
}

function currentRunStillActive(state) {
  if (!state) return false;
  if (state.currentRunType === "agent") return shouldPollAgentRunStatus(state.lastWorkflowStatus);
  return shouldPollWorkflowRunStatus(state.lastWorkflowStatus);
}

function showContextFeedback(target, text, warn = false) {
  if (!target) return;
  target.textContent = text;
  target.classList.toggle("warn", warn);
  target.classList.remove("flash");
  window.requestAnimationFrame(() => target.classList.add("flash"));
}

function appendMessage(messages, text, type) {
  const div = document.createElement("div");
  div.className = `message markdown ${type || "assistant"}`;
  div.innerHTML = renderMarkdown(text || "");
  messages.appendChild(div);
  messages.scrollTop = messages.scrollHeight;
}

function createRunState(messages, timelineJumpLatest, resultPreview, resultOutput, resultEmpty, resultStatus, runAttention, runTimelineHint, runLiveStatus, root, runStatus, send) {
  return {
    root,
    messages,
    timelineJumpLatest,
    timelineTotals: root?.querySelector("#runTimelineTotals"),
    timelinePinnedToLatest: true,
    timelineSectionVisible: true,
    timelineUnseenEvents: 0,
    tokenTotals: emptyTimelineTokenTotals(),
    tokenSeen: new Set(),
    lastPromptEstimate: 0,
    resultPreview,
    resultOutput,
    resultEmpty,
    resultPanel: root?.querySelector(".run-results"),
    resultStatus,
    runAttention,
    runTimelineHint,
    runLiveStatus,
    runStatus,
    send,
    hasResult: false,
    hasError: false,
    setupBlocked: false,
    awaitingApproval: false,
    awaitingInput: false,
    inputRequest: null,
    submitWorkflowInput: null,
    currentRunType: "",
    currentWorkflowName: "",
    lastWorkflowStatus: "",
    lastSnapshotKey: "",
    requestInFlight: false,
    actionInFlight: false,
    cancelActionInFlight: false,
    blockingActionName: "",
    activeActionName: "",
    refreshRuntime: null,
    pollTimer: null,
    pollInFlight: false,
    actionRefreshTimer: null,
    actionRefreshInFlight: false,
    actionRefreshQueued: false,
    pollRunID: "",
    pollWorkflowName: "",
    historyPollTimer: null,
    eventAbortController: null,
    eventReconnectTimer: null,
    eventReconnectDelay: defaultRunEventReconnectMS,
    eventStreamInFlight: false,
    eventStreamRunID: "",
    eventsURL: "",
    lastEventSeq: 0,
    previewNode: null,
    previewRenderFrame: 0,
    pendingPreviewRaw: "",
    pendingSaveFrame: 0,
    pendingSavePatch: null,
    lastSavedRunStateSignature: "",
    finalNode: null,
    hasPreview: false,
    durableWorkflowActive: false,
    currentRunID: "",
    currentStage: "",
    historyRefresh: null,
    syncContextGuide: null
  };
}

function bindRunTimelineHeightSync(root) {
  const setup = root?.querySelector(".run-brief");
  const timeline = root?.querySelector(".run-timeline");
  if (!setup || !timeline) return () => {};
  const media = window.matchMedia("(max-width: 900px)");
  let frame = 0;
  const clear = () => {
    timeline.style.removeProperty("--run-setup-height");
  };
  const sync = () => {
    frame = 0;
    if (!root.isConnected) return;
    if (media.matches) {
      clear();
      return;
    }
    if (timeline.classList.contains("has-workflow-input")) {
      clear();
      return;
    }
    const height = Math.max(1, Math.ceil(setup.getBoundingClientRect().height));
    timeline.style.setProperty("--run-setup-height", `${height}px`);
  };
  const schedule = () => {
    if (frame) return;
    frame = requestAnimationFrame(sync);
  };
  const observer = typeof ResizeObserver === "function" ? new ResizeObserver(schedule) : null;
  observer?.observe(setup);
  window.addEventListener("resize", schedule);
  media.addEventListener?.("change", schedule);
  schedule();
  return () => {
    if (frame) cancelAnimationFrame(frame);
    observer?.disconnect();
    window.removeEventListener("resize", schedule);
    media.removeEventListener?.("change", schedule);
    clear();
  };
}

function bindTimelineFollowMode(runState) {
  const messages = runState?.messages;
  const jump = runState?.timelineJumpLatest;
  const section = runState?.root?.querySelector(".run-timeline");
  if (!messages) return () => {};
  timelineRunStates.set(messages, runState);
  const syncPin = () => {
    runState.timelineSectionVisible = isTimelineSectionInView(runState);
    if (!runState.timelineSectionVisible) return;
    const pinned = isFollowingTimelineLatest(messages);
    runState.timelinePinnedToLatest = pinned;
    if (pinned) {
      runState.timelineUnseenEvents = 0;
      setTimelineJumpLatestVisible(runState, false);
    }
  };
  const jumpToLatest = () => {
    runState.timelinePinnedToLatest = true;
    runState.timelineUnseenEvents = 0;
    followTimelineLatest(runState, messages);
    setTimelineJumpLatestVisible(runState, false);
  };
  const onWindowScroll = () => syncPin();
  const pauseForHistoryReview = event => {
    if (event.deltaY <= 0 || !timelineCanScroll(messages)) return;
    runState.timelinePinnedToLatest = false;
  };
  messages.addEventListener("scroll", syncPin, { passive: true });
  messages.addEventListener("wheel", pauseForHistoryReview, { passive: true });
  window.addEventListener("scroll", onWindowScroll, { passive: true });
  jump?.addEventListener("click", jumpToLatest);
  const observer = typeof IntersectionObserver === "function"
    ? new IntersectionObserver(entries => {
      const visible = entries.some(entry => entry.isIntersecting && entry.intersectionRatio > 0.18);
      runState.timelineSectionVisible = visible;
      if (visible && runState.timelinePinnedToLatest) followTimelineLatest(runState, messages);
    }, { threshold: [0.18, 0.4, 0.75] })
    : null;
  if (section) observer?.observe(section);
  requestAnimationFrame(syncPin);
  return () => {
    messages.removeEventListener("scroll", syncPin);
    messages.removeEventListener("wheel", pauseForHistoryReview);
    window.removeEventListener("scroll", onWindowScroll);
    jump?.removeEventListener("click", jumpToLatest);
    observer?.disconnect();
    timelineRunStates.delete(messages);
  };
}

function bindRunControlHints(root) {
  if (!root) return () => {};
  let frame = 0;
  const sync = () => {
    frame = 0;
    if (!root.isConnected || currentLanguage() !== "zh") return;
    root.querySelectorAll("button, [role=button]").forEach(control => {
      const visible = String(control.textContent || "").replace(/\s+/g, " ").trim();
      const aria = localizedText(control.getAttribute("aria-label") || "");
      const title = localizedText(control.getAttribute("title") || "");
      const actionName = control.dataset.agentAction || control.dataset.runAction || control.dataset.historyAction || "";
      const actionLabel = actionName ? workflowRunActionLabel(actionName, agentRunActionLabel(actionName, "")) : "";
      const candidates = [visible, aria, title, actionLabel].filter(Boolean);
      const label = candidates.find(text => /[\u3400-\u9fff]/.test(text)) || candidates[0] || "";
      if (!label) return;
      if (control.getAttribute("title") !== label) control.setAttribute("title", label);
      if (control.getAttribute("aria-label") !== label) control.setAttribute("aria-label", label);
    });
  };
  const schedule = () => {
    if (frame) return;
    frame = requestAnimationFrame(sync);
  };
  const observer = typeof MutationObserver === "function" ? new MutationObserver(schedule) : null;
  observer?.observe(root, { childList: true, characterData: true, subtree: true });
  schedule();
  return () => {
    if (frame) cancelAnimationFrame(frame);
    observer?.disconnect();
  };
}

function createWorkflowInputSubmitHandler(runState, messages, collaborationState, refreshRuntime) {
  return async request => {
    let values = {};
    try {
      values = collectWorkflowInputValues(runState.runAttention, request.fields || []);
    } catch (error) {
      showWorkflowInputError(runState, chatDisplayText(error.message || t("chat.resumeFailed")));
      return;
    }
    const workspaceReady = await ensureWorkspaceRequirementBeforeWorkflowInput(runState, messages, request, values);
    if (!workspaceReady) return;
    appendTimelineEvent(messages, {
      tone: "stage",
      title: t("chat.resumeRun"),
      detail: workflowInputSummary(request)
    });
    setRunStatus(runState.root, runState.runStatus, runState.send, true, t("chat.timelineResuming"));
    setTimelineHint(runState, t("chat.timelineResuming"));
    setResultStatus(runState, t("chat.resultResuming"));
    setCollaborationStatus(collaborationState, t("chat.collabRunning"));
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
    runState.requestInFlight = true;
    runState.currentRunType = "workflow";
    runState.durableWorkflowActive = true;
    syncRunUnloadGuard(runState);
    try {
      setWorkflowInputSubmitting(runState, true);
      const response = await submitWorkflowRunInput(runState.currentRunID, values, { background: true });
      clearRunAttention(runState);
      runState.awaitingInput = false;
      runState.inputRequest = null;
      await handleWorkflowBackgroundResponse(response, messages, runState, collaborationState, runState.currentWorkflowName, "input");
      startWorkflowRunPolling(runState, collaborationState, {
        runID: runState.currentRunID,
        workflowName: runState.currentWorkflowName,
        eventsURL: runEventsURLFromActionResponse(response)
      });
      markCollaborationStale(collaborationState);
    } catch (error) {
      const message = chatDisplayText(error.message || t("chat.resumeFailed"));
      showWorkflowInputError(runState, message);
      appendTimelineEvent(messages, {
        tone: "error",
        title: t("chat.resumeFailed"),
        detail: message
      });
      setTimelineHint(runState, t("chat.timelineError"));
      setResultStatus(runState, t("chat.resultError"));
      runState.hasError = true;
    } finally {
      runState.requestInFlight = false;
      setWorkflowInputSubmitting(runState, false);
      setRunStatus(runState.root, runState.runStatus, runState.send, shouldPollWorkflowRunStatus(runState.lastWorkflowStatus), currentRunStatusLabel(runState));
      if (!shouldPollWorkflowRunStatus(runState.lastWorkflowStatus)) {
        stopWorkflowRunPolling(runState);
      }
      saveRunState(runState, { running: shouldPollWorkflowRunStatus(runState.lastWorkflowStatus) });
      syncRunUnloadGuard(runState);
      refreshRuntime().catch(() => {});
    }
  };
}

async function restorePlaygroundRunState(runState, collaborationState, runtime) {
  const workflow = runtime?.session?.workflow || {};
  const saved = readSavedRunState();
  if (saved?.mode === "agent") {
    let agentRunID = saved.runID || latestRelevantAgentRunID(runtime, saved);
    if (saved.runID && !(await savedRunIsAvailable(saved.runID, "agent"))) {
      clearSavedRunState();
      return;
    }
    if (!agentRunID) {
      try {
        const runs = normalizeAgentRunList(await fetchAgentRuns());
        agentRunID = latestRelevantAgentRunID({ session: { agent_runs: runs } }, saved);
      } catch {
        agentRunID = "";
      }
    }
    if (agentRunID) {
      try {
        const run = await fetchWorkbenchRunSnapshot(agentRunID, "agent");
        await renderAgentRunSnapshot(runState, collaborationState, run, { hydrate: false });
        return;
      } catch (error) {
        if (Number(error?.status) === 404) {
          clearSavedRunState();
          return;
        }
        // Fall back to local restoration below.
      }
    }
    if (saved && Date.now() - Number(saved.updatedAt || 0) < 24 * 60 * 60 * 1000) {
      restoreLocalRunSnapshot(runState, saved);
      return;
    }
  }
  let runID = workflow.run_id || saved?.runID || "";
  if (!runID) {
    // Do not scan historical runs during the first task render. The history
    // drawer loads them on demand; a missing saved run means there is nothing
    // to restore into the active task.
    return;
  }
  if (!(await savedRunIsAvailable(runID, "workflow"))) {
    clearSavedRunState();
    return;
  }
  if (runID) {
    try {
      const run = await fetchWorkbenchRunSnapshot(runID, "workflow");
      await renderWorkflowRunSnapshot(runState, collaborationState, run, { hydrate: false });
      return;
    } catch (error) {
      if (Number(error?.status) === 404) {
        clearSavedRunState();
        return;
      }
      // Fall back to local restoration below.
    }
  }
  if (saved && Date.now() - Number(saved.updatedAt || 0) < 24 * 60 * 60 * 1000) {
    restoreLocalRunSnapshot(runState, saved);
  }
}

async function fetchWorkbenchRunSnapshot(runID, expectedType) {
  try {
    const payload = await fetchRunWorkbench(runID);
    const run = payload?.run && typeof payload.run === "object" ? { ...payload.run } : {};
    const task = payload?.task && typeof payload.task === "object" ? payload.task : {};
    const actions = Array.isArray(payload?.actions) ? payload.actions : [];
    const timeline = Array.isArray(payload?.timeline) ? payload.timeline : [];
    return {
      ...run,
      final_output: String(payload?.final_output || ""),
      output: String(payload?.final_output || run.output || ""),
      summary: String(payload?.final_output || run.summary || ""),
      execution: payload?.execution && typeof payload.execution === "object" ? payload.execution : run.execution,
      run_type: task.type || task.kind || expectedType,
      actions,
      actions_summary: task.actions_summary || run.actions_summary,
      timeline,
      token_usage: payload?.token_usage && typeof payload.token_usage === "object" ? payload.token_usage : run.token_usage,
      events_url: task.events_url || run.events_url,
      events_path: task.events_path || run.events_path,
      timeline_path: task.timeline_path || run.timeline_path,
      replay_path: task.replay_path || run.replay_path,
      artifacts_path: task.artifacts_path || run.artifacts_path,
      context_path: task.context_path || run.context_path,
      actions_path: task.actions_path || run.actions_path,
      diffs_path: task.diffs_path || run.diffs_path,
      delivery_path: task.delivery_path || run.delivery_path,
      export_path: task.export_path || run.export_path,
      needs_action: Boolean(task.needs_action || run.needs_action),
      resumable: Boolean(task.resumable || run.resumable)
    };
  } catch (error) {
    if (Number(error?.status) === 404) throw error;
    return fetchRun(runID, { summary: true });
  }
}

async function savedRunIsAvailable(runID, expectedType) {
  try {
    const payload = await fetchRuns({ limit: 100 });
    return normalizeRunCollection(payload).some(run => {
      const type = run?.run_type === "agent" ? "agent" : "workflow";
      return type === expectedType && run.id === runID;
    });
  } catch {
    // A temporary history failure should not discard a possibly live run.
    return true;
  }
}

function latestRelevantAgentRunID(runtime, saved) {
  const runs = normalizeAgentRunList(runtime?.session?.agent_runs);
  const active = runs.find(run => shouldPollAgentRunStatus(run.status));
  if (active?.id) return active.id;
  if (saved?.runID && runs.some(run => run.id === saved.runID)) return saved.runID;
  return runs[0]?.id || "";
}

function latestRelevantWorkflowRunID(runtime, saved) {
  const runs = normalizeWorkflowRunList(runtime?.session?.workflow_runs);
  const active = runs.find(run => shouldPollWorkflowRunStatus(run.status));
  if (active?.id) return active.id;
  if (saved?.runID && runs.some(run => run.id === saved.runID)) return saved.runID;
  return runs[0]?.id || "";
}

function normalizeWorkflowRunList(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  return [];
}

function normalizeAgentRunList(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.agent_runs)) return value.agent_runs;
  return [];
}

function restoreLocalRunSnapshot(runState, saved) {
  if (!saved || (!saved.resultRaw && !saved.timelineHint && !saved.resultStatus && !saved.running)) return;
  runState.currentRunType = saved.mode || "";
  runState.currentRunID = saved.runID || "";
  runState.currentWorkflowName = saved.workflowName || "";
  runState.currentStage = saved.stage || "";
  runState.lastWorkflowStatus = saved.status || "";
  runState.eventsURL = saved.eventsURL || "";
  runState.lastEventSeq = Number(saved.lastEventSeq || 0);
  if (saved.resultRaw && !isInternalWorkflowPrompt(saved.resultRaw)) replaceResult(runState, saved.resultRaw);
  setTimelineHint(runState, saved.timelineHint || t("chat.timelineIdle"));
  setResultStatus(runState, saved.resultStatus || t("chat.resultIdle"));
  setRunStatus(runState.root, runState.runStatus, runState.send, false, saved.timelineHint || "");
  saveRunState(runState, { running: false });
}

function startWorkflowRunPolling(runState, collaborationState, options = {}) {
  if (!runState) return;
  runState.currentRunType = "workflow";
  runState.pollRunID = options.runID || runState.currentRunID || "";
  runState.pollWorkflowName = options.workflowName || runState.currentWorkflowName || "";
  runState.eventsURL = options.eventsURL || runState.eventsURL || "";
  if (runState.pollRunID) {
    startWorkflowRunEventStream(runState, collaborationState, {
      runID: runState.pollRunID,
      eventsURL: runState.eventsURL
    });
  }
  if (runState.pollTimer) return;
  setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  syncRunUnloadGuard(runState);
  const tick = () => pollWorkflowRunSnapshot(runState, collaborationState).catch(() => {
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
  });
  runState.pollTimer = window.setInterval(tick, workflowRunPollIntervalMS);
  window.setTimeout(tick, 350);
}

function stopWorkflowRunPolling(runState) {
  stopWorkflowRunEventStream(runState);
  if (!runState?.pollTimer) {
    syncRunUnloadGuard(runState);
    return;
  }
  window.clearInterval(runState.pollTimer);
  runState.pollTimer = null;
  runState.pollInFlight = false;
  setRunLiveStatus(runState, t("chat.realtimeIdle"), "idle");
  syncRunUnloadGuard(runState);
}

function startWorkflowRunEventStream(runState, collaborationState, options = {}) {
  const runID = options.runID || runState.currentRunID || "";
  if (!runID) return;
  const eventsURL = options.eventsURL || runState.eventsURL || "";
  if (runState.eventStreamInFlight && runState.eventStreamRunID === runID) return;
  stopWorkflowRunEventStream(runState, { keepStatus: true });
  runState.eventAbortController = new AbortController();
  runState.eventStreamRunID = runID;
  runState.eventStreamInFlight = true;
  runState.eventsURL = eventsURL;
  setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  saveRunState(runState, { eventsURL, running: true });
  streamWorkflowRunEvents(runID, {
    eventsURL,
    since: runState.lastEventSeq || 0,
    signal: runState.eventAbortController.signal
  }, event => handleWorkflowRunStreamEvent(event, runState, collaborationState)).then(async () => {
    runState.eventStreamInFlight = false;
    runState.eventAbortController = null;
    const run = runState.currentRunID ? await fetchWorkflowRun(runState.currentRunID).catch(() => null) : null;
    if (run) await renderWorkflowRunSnapshot(runState, collaborationState, run);
    await runState.historyRefresh?.();
    if (!shouldPollWorkflowRunStatus(runState.lastWorkflowStatus) && !runState.requestInFlight) {
      stopWorkflowRunPolling(runState);
    } else {
      setRunLiveStatus(runState, t("chat.realtimeUpdated"), "idle");
    }
  }).catch(error => {
    if (error?.name === "AbortError") return;
    runState.eventStreamInFlight = false;
    runState.eventAbortController = null;
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
    if (shouldPollWorkflowRunStatus(runState.lastWorkflowStatus) && !runState.eventReconnectTimer) {
      runState.eventReconnectTimer = window.setTimeout(() => {
        runState.eventReconnectTimer = null;
        startWorkflowRunEventStream(runState, collaborationState, { runID, eventsURL });
      }, runEventReconnectDelay(runState));
    }
  });
}

function stopWorkflowRunEventStream(runState, options = {}) {
  if (!runState) return;
  if (runState.eventReconnectTimer) {
    window.clearTimeout(runState.eventReconnectTimer);
    runState.eventReconnectTimer = null;
  }
  if (runState.eventAbortController) {
    runState.eventAbortController.abort();
    runState.eventAbortController = null;
  }
  runState.eventStreamInFlight = false;
  runState.eventStreamRunID = "";
  if (!options.keepStatus) setRunLiveStatus(runState, t("chat.realtimeIdle"), "idle");
}

function startAgentRunPolling(runState, collaborationState, options = {}) {
  if (!runState) return;
  runState.currentRunType = "agent";
  runState.pollRunID = options.runID || runState.currentRunID || "";
  runState.eventsURL = options.eventsURL || runState.eventsURL || "";
  if (runState.pollRunID) {
    startAgentRunEventStream(runState, collaborationState, {
      runID: runState.pollRunID,
      eventsURL: runState.eventsURL
    });
  }
  if (runState.pollTimer) return;
  setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  syncRunUnloadGuard(runState);
  const tick = () => pollAgentRunSnapshot(runState, collaborationState).catch(() => {
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
  });
  runState.pollTimer = window.setInterval(tick, workflowRunPollIntervalMS);
  window.setTimeout(tick, 350);
}

function stopAgentRunPolling(runState) {
  stopWorkflowRunEventStream(runState);
  if (!runState?.pollTimer) {
    syncRunUnloadGuard(runState);
    return;
  }
  window.clearInterval(runState.pollTimer);
  runState.pollTimer = null;
  runState.pollInFlight = false;
  setRunLiveStatus(runState, t("chat.realtimeIdle"), "idle");
  syncRunUnloadGuard(runState);
}

function startAgentRunEventStream(runState, collaborationState, options = {}) {
  const runID = options.runID || runState.currentRunID || "";
  if (!runID) return;
  const eventsURL = options.eventsURL || runState.eventsURL || "";
  if (runState.eventStreamInFlight && runState.eventStreamRunID === runID) return;
  stopWorkflowRunEventStream(runState, { keepStatus: true });
  runState.eventAbortController = new AbortController();
  runState.eventStreamRunID = runID;
  runState.eventStreamInFlight = true;
  runState.currentRunType = "agent";
  runState.eventsURL = eventsURL;
  setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  saveRunState(runState, { mode: "agent", eventsURL, running: true });
  streamAgentRunEvents(runID, {
    eventsURL,
    since: runState.lastEventSeq || 0,
    signal: runState.eventAbortController.signal
  }, event => handleAgentRunStreamEvent(event, runState, collaborationState)).then(async () => {
    runState.eventStreamInFlight = false;
    runState.eventAbortController = null;
    const run = runState.currentRunID ? await fetchAgentRun(runState.currentRunID).catch(() => null) : null;
    if (run) await renderAgentRunSnapshot(runState, collaborationState, run);
    await runState.historyRefresh?.();
    if (!shouldPollAgentRunStatus(runState.lastWorkflowStatus) && !runState.requestInFlight) {
      stopAgentRunPolling(runState);
    } else {
      setRunLiveStatus(runState, t("chat.realtimeUpdated"), "idle");
    }
  }).catch(error => {
    if (error?.name === "AbortError") return;
    runState.eventStreamInFlight = false;
    runState.eventAbortController = null;
    setRunLiveStatus(runState, t("chat.realtimeOffline"), "offline");
    if (shouldPollAgentRunStatus(runState.lastWorkflowStatus) && !runState.eventReconnectTimer) {
      runState.eventReconnectTimer = window.setTimeout(() => {
        runState.eventReconnectTimer = null;
        startAgentRunEventStream(runState, collaborationState, { runID, eventsURL });
      }, runEventReconnectDelay(runState));
    }
  });
}

async function handleAgentRunStreamEvent(event, runState, collaborationState) {
  const seq = workflowRunEventSeq(event);
  if (seq > 0 && seq <= runState.lastEventSeq) return;
  if (seq > runState.lastEventSeq) runState.lastEventSeq = seq;
  if (applyRunEventRetryDirective(event, runState)) return;
  if (event?.type === "agent_run_error") {
    throw new Error(chatDisplayText(event.error || event.message || t("chat.runHistoryActionFailed")));
  }
  if (event?.type === "agent_run_snapshot") {
    await renderAgentRunSnapshot(runState, collaborationState, event);
    scheduleRunStateSave(runState, { mode: "agent", lastEventSeq: runState.lastEventSeq, eventsURL: runState.eventsURL });
    return;
  }
  runState.currentRunType = "agent";
  handleRunEvent(event, runState.messages, runState, collaborationState);
  if (event?.needs_action || event?.suspended) {
    runState.awaitingApproval = true;
    setTimelineHint(runState, t("chat.timelineApproval"));
    setResultStatus(runState, t("chat.resultBlocked"));
    queueRunActionSnapshotRefresh(runState, collaborationState, "agent");
  }
  setRunLiveStatus(runState, t("chat.realtimeLive"), "live");
  scheduleRunStateSave(runState, {
    mode: "agent",
    running: shouldPollAgentRunStatus(runState.lastWorkflowStatus),
    status: runState.lastWorkflowStatus,
    lastEventSeq: runState.lastEventSeq,
    eventsURL: runState.eventsURL
  });
}

async function pollAgentRunSnapshot(runState, collaborationState) {
  if (!runState || runState.pollInFlight) return;
  if (document.visibilityState === "hidden") return;
  if (runState.eventStreamInFlight && runState.currentRunID) return;
  runState.pollInFlight = true;
  try {
    let run = await resolvePolledAgentRun(runState);
    if (!run?.id) return;
    run = await hydrateAgentRunDetails(run);
    const key = agentRunSnapshotKey(run);
    if (key !== runState.lastSnapshotKey) {
      await renderAgentRunSnapshot(runState, collaborationState, run);
      markCollaborationStale(collaborationState);
      runState.historyRefresh?.();
    } else {
      setRunLiveStatus(runState, shouldPollAgentRunStatus(run.status) ? t("chat.realtimeLive") : t("chat.realtimeUpdated"), shouldPollAgentRunStatus(run.status) ? "live" : "idle");
    }
    if (!shouldPollAgentRunStatus(run.status) && !runState.requestInFlight) {
      stopAgentRunPolling(runState);
    }
  } finally {
    runState.pollInFlight = false;
  }
}

async function resolvePolledAgentRun(runState) {
  if (runState.currentRunID || runState.pollRunID) {
    const runID = runState.currentRunID || runState.pollRunID;
    const run = await fetchAgentRun(runID).catch(() => null);
    if (run) return run;
  }
  const runs = normalizeAgentRunList(await fetchAgentRuns().catch(() => []));
  return runs.find(run => shouldPollAgentRunStatus(run.status)) || runs[0] || null;
}

async function handleWorkflowRunStreamEvent(event, runState, collaborationState) {
  const seq = workflowRunEventSeq(event);
  if (seq > 0 && seq <= runState.lastEventSeq) return;
  if (seq > runState.lastEventSeq) runState.lastEventSeq = seq;
  if (applyRunEventRetryDirective(event, runState)) return;
  if (event?.type === "workflow_run_error") {
    throw new Error(chatDisplayText(event.error || event.message || t("chat.runHistoryActionFailed")));
  }
  if (event?.type === "workflow_run_snapshot") {
    const snapshot = event.execution || event.change_slices
      ? event
      : await fetchWorkbenchRunSnapshot(event.id || runState.currentRunID, "workflow").catch(() => event);
    await renderWorkflowRunSnapshot(runState, collaborationState, snapshot);
    scheduleRunStateSave(runState, { lastEventSeq: runState.lastEventSeq, eventsURL: runState.eventsURL });
    return;
  }
  if (event?.workflow_status) runState.lastWorkflowStatus = event.workflow_status;
  if (event?.workflow_name) runState.currentWorkflowName = event.workflow_name;
  if (event?.next_stage) runState.currentStage = event.next_stage;
  handleRunEvent(event, runState.messages, runState, collaborationState);
  if (workflowEventNeedsActionSnapshot(event)) {
    queueRunActionSnapshotRefresh(runState, collaborationState, "workflow");
  }
  setRunLiveStatus(runState, t("chat.realtimeLive"), "live");
  scheduleRunStateSave(runState, {
    running: shouldPollWorkflowRunStatus(runState.lastWorkflowStatus),
    status: runState.lastWorkflowStatus,
    lastEventSeq: runState.lastEventSeq,
    eventsURL: runState.eventsURL
  });
}

function workflowEventNeedsActionSnapshot(event = {}) {
  const type = String(event.type || "").toLowerCase();
  const status = String(event.workflow_status || event.status || "").toLowerCase();
  return Boolean(
    event.needs_action ||
    event.suspended ||
    type === "approval" ||
    type === "workflow_tool_approval_submitted" ||
    (type === "workflow_result" && isWorkflowRunTerminal(status)) ||
    (type === "workflow_result" && (isApprovalStatus(status) || status === "awaiting_input" || status === "awaiting_sub_workflow"))
  );
}

function queueRunActionSnapshotRefresh(runState, collaborationState, runType) {
  if (!runState?.currentRunID) return;
  runState.actionRefreshQueued = true;
  if (runState.actionRefreshTimer || runState.actionRefreshInFlight) return;
  runState.actionRefreshTimer = window.setTimeout(async () => {
    runState.actionRefreshTimer = null;
    if (runState.actionRefreshInFlight || !runState.actionRefreshQueued) return;
    const runID = runState.currentRunID;
    runState.actionRefreshQueued = false;
    runState.actionRefreshInFlight = true;
    try {
      const snapshot = await fetchWorkbenchRunSnapshot(runID, runType);
      if (runState.currentRunID !== runID) return;
      if (runType === "agent") {
        await renderAgentRunSnapshot(runState, collaborationState, snapshot, { hydrate: false });
      } else {
        await renderWorkflowRunSnapshot(runState, collaborationState, snapshot, { hydrate: false });
      }
      runState.historyRefresh?.();
    } catch {
      // The regular reconnect/poll path remains active when this eager refresh fails.
    } finally {
      runState.actionRefreshInFlight = false;
      if (runState.actionRefreshQueued) queueRunActionSnapshotRefresh(runState, collaborationState, runType);
    }
  }, 60);
}

function workflowRunEventSeq(event) {
  const fromSeq = Number(event?.seq || 0);
  if (Number.isFinite(fromSeq) && fromSeq > 0) return fromSeq;
  const fromSSE = Number(event?.sse_id || 0);
  return Number.isFinite(fromSSE) && fromSSE > 0 ? fromSSE : 0;
}

function applyRunEventRetryDirective(event, runState) {
  const retry = Number(event?.sse_retry || event?.retry || 0);
  if (Number.isFinite(retry) && retry > 0) {
    runState.eventReconnectDelay = Math.min(30000, Math.max(750, retry));
  }
  return event?.type === "sse_retry";
}

function runEventReconnectDelay(runState) {
  const delay = Number(runState?.eventReconnectDelay || defaultRunEventReconnectMS);
  return Number.isFinite(delay) && delay > 0 ? delay : defaultRunEventReconnectMS;
}

async function pollWorkflowRunSnapshot(runState, collaborationState) {
  if (!runState || runState.pollInFlight) return;
  if (document.visibilityState === "hidden") return;
  if (runState.eventStreamInFlight && runState.currentRunID) return;
  runState.pollInFlight = true;
  try {
    const run = await resolvePolledWorkflowRun(runState);
    if (!run?.id) return;
    const hydrated = await hydrateWorkflowRunDetails(run);
    const key = workflowRunSnapshotKey(hydrated);
    if (key !== runState.lastSnapshotKey) {
      await renderWorkflowRunSnapshot(runState, collaborationState, hydrated);
      markCollaborationStale(collaborationState);
      runState.historyRefresh?.();
    } else {
      setRunLiveStatus(runState, shouldPollWorkflowRunStatus(hydrated.status) ? t("chat.realtimeLive") : t("chat.realtimeUpdated"), shouldPollWorkflowRunStatus(hydrated.status) ? "live" : "idle");
    }
    if (!shouldPollWorkflowRunStatus(hydrated.status) && !runState.requestInFlight) {
      stopWorkflowRunPolling(runState);
    }
  } finally {
    runState.pollInFlight = false;
  }
}

async function resolvePolledWorkflowRun(runState) {
  if (runState.currentRunID || runState.pollRunID) {
    const runID = runState.currentRunID || runState.pollRunID;
    try {
      const run = await fetchWorkbenchRunSnapshot(runID, "workflow");
      if (run) return run;
    } catch (error) {
      if (Number(error?.status) === 404) {
        clearSavedRunState();
        runState.currentRunID = "";
        runState.pollRunID = "";
        runState.lastWorkflowStatus = "";
        stopWorkflowRunPolling(runState);
        return null;
      }
    }
  }
  const runs = normalizeWorkflowRunList(await fetchWorkflowRuns().catch(() => []));
  const workflowName = String(runState.pollWorkflowName || runState.currentWorkflowName || "").trim();
  const matching = workflowName ? runs.filter(run => run.name === workflowName) : runs;
  return matching.find(run => shouldPollWorkflowRunStatus(run.status)) || matching[0] || runs[0] || null;
}

function shouldPollWorkflowRunStatus(status) {
  const value = String(status || "").toLowerCase();
  return isWorkflowRunActive(value) || isApprovalStatus(value) || value === "awaiting_input" || value === "awaiting_sub_workflow";
}

function shouldPollAgentRunStatus(status) {
  const value = String(status || "").toLowerCase();
  return value === "running" || value === "cancelling" || value === "awaiting_tool_approval";
}

async function renderAgentRunSnapshot(runState, collaborationState, run, options = {}) {
  if (options.hydrate !== false) run = await hydrateAgentRunDetails(run);
  const snapshotKey = agentRunSnapshotKey(run);
  const viewState = captureRunViewState(runState);
  resetRunState(runState, { keepStorage: true });
  runState.currentRunType = "agent";
  runState.currentRunID = run.id || "";
  runState.currentWorkflowName = "";
  runState.currentStage = run.agent_id || run.mode || "";
  runState.lastWorkflowStatus = String(run.status || "");
  runState.lastSnapshotKey = snapshotKey;
  runState.eventsURL = runState.eventsURL || agentRunEventsURL(run);
  runState.lastEventSeq = Math.max(runState.lastEventSeq || 0, agentRunLatestEventSeq(run));
  syncCollaborationContext(runState, collaborationState);
  const events = Array.isArray(run.events) ? run.events : [];
  const replayTimeline = Array.isArray(run.timeline) && run.timeline.length
    ? timelineItemsForTaskRun(run.timeline, run)
    : [];
  if (replayTimeline.length) {
    replayTimeline.forEach(event => appendSnapshotTimelineEvent(runState.messages, event));
    for (const event of events) {
      if (event?.needs_action || event?.suspended) runState.awaitingApproval = true;
    }
  } else {
    for (const event of events) {
      handleRunEvent({ ...event, replay_snapshot: true }, runState.messages, runState, collaborationState);
      if (event?.needs_action || event?.suspended) {
        runState.awaitingApproval = true;
      }
    }
  }
  const output = formatStructuredValue(run.output || run.result?.output || run.result?.final_message || "");
  if (output && !isInternalWorkflowPrompt(output)) {
    replaceResult(runState, output);
  }
  renderRunExecutionSummary(runState, run);
  renderRunSupplementalDetails(runState, run);
  syncTimelineTotalsFromRun(runState, run);
  let terminalActions = [];
  if (run.error) {
    runState.hasError = true;
    showRunAttention(runState, t("chat.errorTitle"), run.error, "error");
  } else if (String(run.status || "").toLowerCase() === "awaiting_tool_approval") {
    runState.awaitingApproval = true;
    const actions = await safeAgentRunActions(run);
    renderAgentRunApprovalAttention(runState, actions, collaborationState);
  } else {
    runState.awaitingApproval = false;
    terminalActions = await safeAgentRunActions(run);
    renderAgentRunTaskActions(runState, terminalActions, collaborationState);
  }
  const active = shouldPollAgentRunStatus(run.status);
  if (runAwaitingStagedApply(terminalActions)) {
    setTimelineHint(runState, "等待应用已验证变更");
    setResultStatus(runState, "待应用");
  } else if (String(run.status || "").toLowerCase() === "completed") {
    setTimelineHint(runState, t("chat.timelineDone"));
    setResultStatus(runState, runState.hasResult ? t("chat.resultReady") : t("chat.resultIdle"));
  } else if (runState.awaitingApproval) {
    setTimelineHint(runState, t("chat.timelineApproval"));
    setResultStatus(runState, t("chat.resultBlocked"));
  } else if (runState.hasError) {
    setTimelineHint(runState, t("chat.timelineError"));
    setResultStatus(runState, t("chat.resultError"));
  } else {
    setTimelineHint(runState, active ? t("chat.timelineRunning") : (run.status || t("chat.timelineIdle")));
    setResultStatus(runState, active ? t("chat.resultStreaming") : (runState.hasResult ? t("chat.resultReady") : t("chat.resultIdle")));
  }
  setRunLiveStatus(runState, active ? t("chat.realtimeLive") : t("chat.realtimeUpdated"), active ? "live" : "idle");
  setRunStatus(runState.root, runState.runStatus, runState.send, active, currentRunStatusLabel(runState));
  saveRunState(runState, {
    mode: "agent",
    running: active,
    status: runState.lastWorkflowStatus,
    eventsURL: runState.eventsURL,
    lastEventSeq: runState.lastEventSeq
  });
  runState.syncContextGuide?.();
  restoreRunViewState(runState, viewState);
}

function agentRunSnapshotKey(run) {
  if (!run) return "";
  const diffs = Array.isArray(run.diffs) ? run.diffs.length : 0;
  const risk = toolRiskSignature(runToolRiskContext(run).risk);
  return [
    run.id || "",
    run.status || "",
    run.pending_call_id || "",
    run.error || "",
    agentRunLatestEventSeq(run),
    risk,
    String(run.output || "").length,
    diffs
  ].join("|");
}

async function hydrateAgentRunDetails(run, options = {}) {
  if (!run?.id) return run || {};
  const next = { ...run };
  const detailMode = options.details || "summary";
  const requests = [fetchAgentRunReplay(run)];
  if (detailMode === "full") requests.push(fetchAgentRunDiffs(run, { include_content: true }));
  const [replay, diffs] = await Promise.allSettled(requests);
  if (replay.status === "fulfilled") {
    mergeAgentRunReplay(next, replay.value);
  }
  const timeline = await fetchRunTimeline(run, { limit: 80 }).catch(() => null);
  if (Array.isArray(timeline)) next.timeline = timeline;
  if (detailMode === "full" && diffs?.status === "fulfilled" && Array.isArray(diffs.value?.diffs)) next.diffs = diffs.value.diffs;
  return next;
}

function mergeAgentRunReplay(run, replay) {
  if (!run || !replay || typeof replay !== "object") return run;
  if (replay.run && typeof replay.run === "object") Object.assign(run, replay.run);
  if (Array.isArray(replay.events)) run.events = replay.events;
  if (Array.isArray(replay.timeline)) run.timeline = replay.timeline;
  if (Array.isArray(replay.diffs)) run.diffs = replay.diffs;
  if (Array.isArray(replay.actions)) run.actions = replay.actions;
  return run;
}

function agentRunLatestEventSeq(run) {
  const events = [...(run?.events || []), ...(run?.timeline || [])];
  return events.reduce((max, event) => Math.max(max, workflowRunEventSeq(event)), 0);
}

async function safeAgentRunActions(run) {
  if (Array.isArray(run?.actions)) return run.actions;
  if (!runIDForAction(run)) return [];
  try {
    const actions = await fetchAgentRunActions(run);
    return Array.isArray(actions) ? actions : [];
  } catch {
    return [];
  }
}

function runIDForAction(run) {
  if (run && typeof run === "object") return String(run.id || run.run_id || "").trim();
  return String(run || "").trim();
}

function runEventsURLFromActionResponse(response, action = {}) {
  const payload = response && typeof response === "object" ? response : {};
  return String(
    payload.events_url ||
    payload.eventsURL ||
    payload.events_path ||
    payload.eventsPath ||
    action.events_url ||
    action.eventsURL ||
    action.events_path ||
    action.eventsPath ||
    ""
  ).trim();
}

function renderAgentRunApprovalAttention(runState, actions, collaborationState) {
  const visible = (actions || []).filter(action =>
    ["approve_task_scope", "approve_tool", "approve_remember_tool", "deny_tool", "cancel"].includes(action.name) &&
    !(action.name === "approve_remember_tool" && action.available === false)
  );
	const hasTaskScope = visible.some(action => action.name === "approve_task_scope" && action.available !== false);
  const buttons = visible.length
    ? visible.map(action => agentRunActionButton(action, hasTaskScope)).join("")
    : `<p class="workflow-run-action-help">${escapeHTML(t("chat.awaitingApprovalHelp"))}</p>`;
  const help = agentRunActionUnavailableHelp(visible);
  runState.runAttention.className = "run-attention approval";
  runState.runAttention.innerHTML = `
    <strong>${escapeHTML(t("chat.awaitingApprovalTitle"))}</strong>
    <div class="workflow-run-actions agent-run-actions">${buttons}</div>
    ${help ? `<p class="workflow-run-action-help">${escapeHTML(help)}</p>` : ""}`;
  runState.runAttention.classList.remove("hidden");
  runState.runAttention.querySelectorAll("button[data-agent-action]").forEach(button => {
    const action = visible.find(item => item.name === button.dataset.agentAction);
    button.addEventListener("click", () => {
      if (button.disabled) return;
      if (runState.actionInFlight && action?.name !== "cancel") return;
      setWorkflowActionButtonsPending(runState.runAttention, true, action?.name || "");
      executeAgentRunAction(runState, action, collaborationState).finally(() => {
        syncWorkflowActionButtonsPending(runState.runAttention, runState);
      });
    });
  });
}

function renderAgentRunTaskActions(runState, actions, collaborationState) {
  const visibleNames = ["continue_output", "retry", "cancel", "apply_staged_changes", "restore_staged_changes", "discard_staged_changes"];
  const visible = (actions || []).filter(action => action?.path && visibleNames.includes(action.name));
  if (!visible.length) {
    clearRunAttention(runState);
    return;
  }
  const summary = stagedRunActionSummary(visible);
  runState.runAttention.className = "run-attention";
  runState.runAttention.innerHTML = `
    <strong>${escapeHTML(t("chat.runActionsTitle"))}</strong>
    <div class="workflow-run-actions agent-run-actions">${visible.map(action => agentRunActionButton(action)).join("")}</div>
    ${summary ? `<p class="run-action-summary">${escapeHTML(summary)}</p>` : ""}`;
  runState.runAttention.classList.remove("hidden");
  runState.runAttention.querySelectorAll("button[data-agent-action]").forEach(button => {
    const action = visible.find(item => item.name === button.dataset.agentAction);
    button.addEventListener("click", () => {
      if (button.disabled) return;
      if (runState.actionInFlight && action?.name !== "cancel") return;
      setWorkflowActionButtonsPending(runState.runAttention, true, action?.name || "");
      executeAgentRunAction(runState, action, collaborationState).finally(() => {
        syncWorkflowActionButtonsPending(runState.runAttention, runState);
      });
    });
  });
}

function agentRunActionButton(action, hasTaskScope = false) {
  const unavailable = action.available === false || !action.path;
  const reasonText = localizedWorkflowActionReason(action.reason || "") || (action.durable === false ? t("chat.agentActionNonDurable") : "");
  const label = agentRunActionLabel(action.name, action.label);
  const tooltip = reasonText || label;
  const primaryActions = hasTaskScope ? ["approve_task_scope"] : ["approve_tool", "approve_remember_tool", "apply_staged_changes", "continue_output", "retry"];
  const classes = action.destructive || action.name === "deny_tool" ? "danger" : action.recommended || primaryActions.includes(action.name) ? "primary" : "";
  return `<button type="button" class="${classes}" data-agent-action="${escapeHTML(action.name)}" data-unavailable="${unavailable ? "true" : "false"}" aria-label="${escapeHTML(label)}" title="${escapeHTML(tooltip)}"${unavailable ? " disabled aria-disabled=\"true\"" : ""}>${escapeHTML(label)}</button>`;
}

function agentRunActionLabel(name, fallback = "") {
  if (name === "cancel") return t("chat.cancelRun");
  if (name === "retry") return t("chat.retryRun");
  if (name === "continue_output") return t("chat.continueExecution");
  if (name === "approve_task_scope") return t("approvals.approveTaskScope");
  if (name === "approve_tool") return t("approvals.approveTool");
  if (name === "approve_all_tools") return t("approvals.approveAllTools");
  if (name === "approve_remember_tool") return t("approvals.approveRemember");
  if (name === "approve_remember_all_tools") return t("approvals.approveRememberAllTools");
  if (name === "deny_tool") return t("approvals.denyTool");
  if (name === "deny_all_tools") return t("approvals.denyAllTools");
  if (name === "apply_staged_changes") return fallback ? localizedText(fallback) : t("chat.applyStagedChanges");
  if (name === "restore_staged_changes") return fallback ? localizedText(fallback) : t("chat.restoreStagedChanges");
  if (name === "discard_staged_changes") return fallback ? localizedText(fallback) : t("chat.discardStagedChanges");
  return runActionFallbackLabel(name, fallback, t("chat.runActionsTitle"));
}

function agentRunActionUnavailableHelp(actions) {
  const unavailable = (actions || []).filter(action => action.available === false);
  if (!unavailable.length) return "";
  const remember = unavailable.find(action => action.name === "approve_remember_tool" || action.name === "approve_remember_all_tools");
  if (remember) return localizedWorkflowActionReason(remember.reason || "") || t("chat.toolRememberBlockedByRiskPolicy");
  const approval = unavailable.find(action => action.name === "approve_task_scope" || action.name === "approve_tool" || action.name === "deny_tool");
  if (approval) return localizedWorkflowActionReason(approval.reason || "") || t("chat.agentToolContextUnavailable");
  return localizedWorkflowActionReason(unavailable[0].reason || "") || t("chat.workflowActionUnavailableHelp");
}

async function executeAgentRunAction(runState, action, collaborationState) {
  if (!action || action.available === false || !action.path) return;
  const isCancelAction = action.name === "cancel";
  if (runState.cancelActionInFlight) return;
  if (runState.actionInFlight && !isCancelAction) return;
  if (!(await ensureWorkspaceRequirementBeforeAction(runState, action, "agent"))) return;
  if (isCancelAction) {
    runState.cancelActionInFlight = true;
    runState.activeActionName = "cancel";
  } else {
    runState.actionInFlight = true;
    runState.blockingActionName = action.name || "";
    runState.activeActionName = action.name || "";
  }
  const actionLabel = agentRunActionLabel(action.name, action.label);
  appendTimelineEvent(runState.messages, {
    tone: action.destructive ? "approval" : "stage",
    title: actionLabel,
    detail: localizedWorkflowActionReason(action.reason || "") || action.path
  });
  try {
    setRunStatus(runState.root, runState.runStatus, runState.send, true, actionLabel);
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
    runState.requestInFlight = true;
    runState.durableWorkflowActive = action.durable !== false;
    syncRunUnloadGuard(runState);
    const response = await request(action.path, {
      method: action.method || "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ background: true })
    });
    if (isStagedRunAction(action)) {
      appendTimelineEvent(runState.messages, {
        tone: "success",
        title: actionLabel,
        detail: stagedRunActionResponseSummary(response, action)
      });
      setResultStatus(runState, t("chat.resultReady"));
    } else {
      await handleAgentBackgroundResponse(response, runState.messages, runState, collaborationState, "", action.name || "");
      startAgentRunPolling(runState, collaborationState, {
        runID: runState.currentRunID,
        eventsURL: runEventsURLFromActionResponse(response, action)
      });
    }
  } catch (error) {
    const message = chatDisplayText(error.message || t("chat.errorTitle"));
    appendTimelineEvent(runState.messages, {
      tone: "error",
      title: t("chat.errorTitle"),
      detail: message
    });
    setTimelineHint(runState, t("chat.timelineError"));
    setResultStatus(runState, t("chat.resultError"));
  } finally {
    if (isCancelAction) {
      runState.cancelActionInFlight = false;
      runState.activeActionName = runState.actionInFlight ? runState.blockingActionName : "";
    } else {
      runState.actionInFlight = false;
      runState.blockingActionName = "";
      if (!runState.cancelActionInFlight) runState.activeActionName = "";
    }
    runState.requestInFlight = runState.actionInFlight || runState.cancelActionInFlight;
    await refreshAgentRunAfterAction(runState, collaborationState);
    setRunStatus(runState.root, runState.runStatus, runState.send, shouldPollAgentRunStatus(runState.lastWorkflowStatus), currentRunStatusLabel(runState));
    if (!shouldPollAgentRunStatus(runState.lastWorkflowStatus)) {
      stopAgentRunPolling(runState);
    }
    syncRunUnloadGuard(runState);
  }
}

async function refreshAgentRunAfterAction(runState, collaborationState) {
  const runID = runState.currentRunID || "";
  if (runID) {
    const run = await fetchAgentRun(runID).catch(() => null);
    if (run) await renderAgentRunSnapshot(runState, collaborationState, run);
  }
  await runState.refreshRuntime?.().catch(() => {});
  runState.historyRefresh?.();
  markCollaborationStale(collaborationState);
}

async function renderWorkflowRunSnapshot(runState, collaborationState, run, options = {}) {
  if (options.hydrate !== false) run = await hydrateWorkflowRunDetails(run);
  run = { ...run, status: workflowRunEffectiveStatus(run) };
  const snapshotKey = workflowRunSnapshotKey(run);
  const viewState = captureRunViewState(runState);
  resetRunState(runState, { keepStorage: true });
  runState.currentRunType = "workflow";
  runState.currentRunID = run.id || "";
  runState.currentWorkflowName = run.name || "";
  runState.currentStage = run.next_stage || lastCompletedStageName(run);
  runState.lastWorkflowStatus = String(run.status || "");
  runState.lastSnapshotKey = snapshotKey;
  runState.eventsURL = runState.eventsURL || workflowRunEventsURL(run);
  runState.lastEventSeq = Math.max(runState.lastEventSeq || 0, workflowRunLatestEventSeq(run));
  const actions = await workflowRunActions(run);
  const actionNames = new Set(actions.filter(action => action.available).map(action => action.name));
  const awaitingStagedApply = runAwaitingStagedApply(actions);
  runState.awaitingApproval = actionNames.has("approve_tool") || actionNames.has("deny_tool") || actionNames.has("approve_stage") || isApprovalStatus(run.status);
  runState.awaitingInput = actionNames.has("submit_input") || run.status === "awaiting_input";
  const events = focusWorkflowTimelineEvents(run);
  const timelineEvents = Array.isArray(run.timeline) && run.timeline.length
    ? timelineItemsForTaskRun(run.timeline, run)
    : timelineEventsForWorkflowRun(events, run);
  if (timelineEvents.length) {
    timelineEvents.forEach(event => appendSnapshotTimelineEvent(runState.messages, event));
  } else {
    appendTimelineEvent(runState.messages, {
      tone: isWorkflowRunActive(run.status) ? "stage" : "success",
      title: run.name || t("chat.workflow"),
      detail: run.status || t("chat.timelineIdle")
    });
  }
  const output = workflowRunResultText(run);
  if (output) replaceResult(runState, output);
  renderRunExecutionSummary(runState, run);
  renderRunSupplementalDetails(runState, run);
  syncTimelineTotalsFromRun(runState, run);
  if (runState.awaitingInput) {
    runState.inputRequest = {
      runID: run.id || "",
      stage: run.next_stage || "",
      workflowName: run.name || "",
      status: run.status || "",
      fields: normalizeWorkflowInputFields(run.pending_input_fields || []),
      summary: run.summary || "",
      detail: run.approval_prompt || t("chat.awaitingInputHelp")
    };
    renderWorkflowInputAttention(runState, runState.inputRequest);
  } else if (runState.awaitingApproval) {
    showRunAttention(runState, t("chat.awaitingApprovalTitle"), run.approval_prompt || t("chat.awaitingApprovalHelp"), "approval");
  }
  renderWorkflowRunActions(runState, actions, collaborationState);
  setTimelineHint(runState, awaitingStagedApply ? "等待应用已验证变更" : workflowRunTimelineLabel(run.status));
  setResultStatus(runState, awaitingStagedApply ? "待应用" : workflowRunResultLabel(run.status, Boolean(output)));
  setRunStatus(runState.root, runState.runStatus, runState.send, actionNames.has("cancel") && isWorkflowRunActive(run.status), awaitingStagedApply ? "待应用" : workflowRunTimelineLabel(run.status));
  setRunLiveStatus(runState, shouldPollWorkflowRunStatus(run.status) ? t("chat.realtimeLive") : t("chat.realtimeUpdated"), shouldPollWorkflowRunStatus(run.status) ? "live" : "idle");
  syncCollaborationContext(runState, collaborationState);
  saveRunState(runState, {
    running: shouldPollWorkflowRunStatus(run.status),
    status: run.status || "",
    eventsURL: runState.eventsURL,
    lastEventSeq: runState.lastEventSeq
  });
  runState.syncContextGuide?.();
  restoreRunViewState(runState, viewState);
}

export function workflowRunEffectiveStatus(run = {}) {
  const status = String(run?.status || "").toLowerCase();
  if (!["completed", "complete", "success", "succeeded", "finished", "done"].includes(status)) return run?.status || "";
  const stages = Array.isArray(run?.completed_stages) ? run.completed_stages : [];
  for (const stage of [...stages].reverse()) {
    if (["quality_gate", "quality_guard"].includes(stage?.node_type)) {
      const quality = String(stage?.output_values?.quality_status || stage?.outputs?.quality_status || "").toLowerCase().trim();
      if (["failed", "fail", "failure", "error", "blocked", "denied", "invalid", "rejected"].includes(quality)) return "blocked";
      if (["passed", "pass", "success", "ok", "completed", "complete", "verified", "valid"].includes(quality)) return run.status;
    }
    const outputs = stage?.output_values || stage?.outputs || {};
    let decision = String(outputs?.decision || "").toLowerCase().trim();
    if (!decision) {
      try {
        decision = String(JSON.parse(String(stage?.result?.output || "{}"))?.decision || "").toLowerCase().trim();
      } catch {
        decision = "";
      }
    }
    if (["blocked", "needs_action", "needs-action"].includes(decision)) return "blocked";
    if (["failed", "failure", "error", "rejected"].includes(decision)) return "failed";
  }
  return run?.status || "";
}

function workflowRunLatestEventSeq(run) {
  const events = [...(run?.events || []), ...(run?.timeline || [])];
  return events.reduce((max, event) => Math.max(max, workflowRunEventSeq(event)), 0);
}

function workflowRunSnapshotKey(run) {
  const events = Array.isArray(run?.events) ? run.events.length : 0;
  const stages = Array.isArray(run?.completed_stages) ? run.completed_stages.length : 0;
  const artifacts = Array.isArray(run?.artifacts) ? run.artifacts.length : run?.artifacts_count || 0;
  const diffs = Array.isArray(run?.diffs) ? run.diffs.length : 0;
  const risk = toolRiskSignature(runToolRiskContext(run).risk);
  const slices = Array.isArray(run?.execution?.change_slices)
    ? run.execution.change_slices
    : Array.isArray(run?.change_slices) ? run.change_slices : [];
  const sliceSignature = slices.map(slice => `${slice?.id || ""}:${slice?.status || ""}:${slice?.blocked_reason || ""}:${Array.isArray(slice?.changed_files) ? slice.changed_files.join(",") : ""}`).join(",");
  return [
    run?.id || "",
    run?.status || "",
    run?.next_stage || "",
    run?.pending_call_id || "",
    events,
    stages,
    artifacts,
    risk,
    diffs,
    sliceSignature
  ].join("|");
}

async function hydrateWorkflowRunDetails(run, options = {}) {
  if (!run?.id) return run || {};
  const next = { ...run };
  const detailMode = options.details || "summary";
  const [replay, artifacts, stages, diffs, evidence] = await Promise.allSettled([
    fetchWorkflowRunReplay(run),
    fetchWorkflowRunArtifacts(run, { limit: 6 }),
    fetchWorkflowRunStages(run, { limit: 6 }),
    detailMode === "full" ? fetchWorkflowRunDiffs(run, { include_content: true }) : Promise.resolve(null),
    detailMode === "full" ? fetchWorkflowRunEvidence(run) : Promise.resolve(null)
  ]);
  if (replay.status === "fulfilled") {
    mergeWorkflowRunReplay(next, replay.value);
  }
  const timeline = await fetchRunTimeline(run, { limit: 80 }).catch(() => null);
  if (Array.isArray(timeline)) next.timeline = timeline;
  if (artifacts.status === "fulfilled" && Array.isArray(artifacts.value)) {
    next.artifacts = artifacts.value;
  }
  if (stages.status === "fulfilled" && Array.isArray(stages.value)) {
    next.completed_stages = stages.value;
  }
  if (detailMode === "full" && diffs.status === "fulfilled" && Array.isArray(diffs.value?.diffs)) {
    next.diffs = diffs.value.diffs;
  } else if (!Array.isArray(next.diffs)) {
    next.diffs = [];
  }
  if (detailMode === "full" && evidence.status === "fulfilled") {
    const normalized = normalizeWorkflowRunEvidencePayload(evidence.value);
    if (normalized.items.length) next.evidence = normalized.items;
    if (Object.keys(normalized.quality).length) next.quality = normalized.quality;
  }
  return next;
}

function mergeWorkflowRunReplay(run, replay) {
  if (!run || !replay || typeof replay !== "object") return run;
  if (replay.run && typeof replay.run === "object") Object.assign(run, replay.run);
  if (Array.isArray(replay.stages)) run.completed_stages = replay.stages;
  if (Array.isArray(replay.events)) run.events = replay.events;
  if (Array.isArray(replay.artifacts)) run.artifacts = replay.artifacts;
  if (Array.isArray(replay.timeline)) run.timeline = replay.timeline;
  if (Array.isArray(replay.diffs)) run.diffs = replay.diffs;
  if (Array.isArray(replay.actions)) run.actions = replay.actions;
  if (replay.navigation && typeof replay.navigation === "object") run.navigation = replay.navigation;
  if (replay.counts && typeof replay.counts === "object") run.counts = replay.counts;
  if (replay.quality && typeof replay.quality === "object") run.quality = replay.quality;
  if (Array.isArray(replay.collaboration)) run.collaboration = replay.collaboration;
  if (Array.isArray(replay.blackboard)) run.blackboard = replay.blackboard;
  if (replay.team_state && typeof replay.team_state === "object") run.team_state = replay.team_state;
  return run;
}

async function workflowRunActions(run) {
  if (Array.isArray(run?.actions)) return run.actions;
  if (!run?.id) return Array.isArray(run?.actions) ? run.actions : [];
  try {
    const actions = await fetchWorkflowRunActions(run);
    return Array.isArray(actions) ? actions : [];
  } catch {
    return Array.isArray(run.actions) ? run.actions : fallbackWorkflowRunActions(run);
  }
}

function fallbackWorkflowRunActions(run) {
  const actions = [];
  const unavailableReason = t("chat.workflowActionUnavailableHelp");
  if (run.status === "awaiting_input") actions.push({ name: "submit_input", label: t("chat.submitWorkflowInput"), available: true, durable: true });
  if (run.status === "awaiting_sub_workflow") actions.push({ name: "resume_sub_workflow", label: t("chat.resumeSubWorkflow"), available: false, durable: true, background: true, reason: unavailableReason });
  if (isWorkflowRunActive(run.status)) actions.push({ name: "cancel", label: t("chat.cancelRun"), available: false, durable: true, destructive: true, reason: unavailableReason });
  if (!isWorkflowRunActive(run.status) && run.name && run.request) actions.push({ name: "retry", label: t("chat.retryRun"), available: false, durable: true, reason: unavailableReason });
  return actions;
}

function renderWorkflowRunActions(runState, actions, collaborationState) {
  const visible = (actions || []).filter(action => action.name !== "submit_input");
  runState.runAttention.querySelector("[data-workflow-run-actions]")?.remove();
  runState.runAttention.querySelector("[data-workflow-run-action-help]")?.remove();
  runState.runAttention.querySelector(".run-action-summary")?.remove();
  if (!visible.length) return;
  if (runState.runAttention.classList.contains("hidden")) {
    runState.runAttention.className = "run-attention";
    runState.runAttention.innerHTML = `<strong>${escapeHTML(t("chat.runActionsTitle"))}</strong>`;
    runState.runAttention.classList.remove("hidden");
  }
  const wrapper = document.createElement("div");
  wrapper.className = "workflow-run-actions";
  wrapper.dataset.workflowRunActions = "true";
  wrapper.innerHTML = visible.map(action => workflowRunActionButton(action)).join("");
  runState.runAttention.appendChild(wrapper);
  const summary = stagedRunActionSummary(visible);
  if (summary) {
    const note = document.createElement("p");
    note.className = "run-action-summary";
    note.textContent = summary;
    runState.runAttention.appendChild(note);
  }
  syncWorkflowActionButtonsPending(wrapper, runState);
  const help = workflowRunActionUnavailableHelp(visible);
  if (help) {
    const note = document.createElement("p");
    note.className = "workflow-run-action-help";
    note.dataset.workflowRunActionHelp = "true";
    note.textContent = help;
    runState.runAttention.appendChild(note);
  }
  wrapper.querySelectorAll("button[data-run-action]").forEach(button => {
    const action = visible.find(item => item.name === button.dataset.runAction);
    button.addEventListener("click", () => {
      if (button.disabled) return;
      if (runState.actionInFlight && action?.name !== "cancel") return;
      setWorkflowActionButtonsPending(wrapper, true, action?.name || "");
      executeWorkflowRunAction(runState, action, collaborationState).finally(() => {
        syncWorkflowActionButtonsPending(wrapper, runState);
      });
    });
  });
}

function workflowRunActionButton(action) {
  const unavailable = action.available === false || (!action.path && !workflowRunActionCanOpenLocally(action));
  const reasonText = localizedWorkflowActionReason(action.reason || "");
  const label = workflowRunActionLabel(action.name, action.label);
  const tooltip = reasonText || label;
  const primaryActions = ["apply_staged_changes", "retry", "escalate_model", "approve_budget", "continue_output", "approve_stage", "approve_tool", "approve_all_tools", "submit_input", "resume_sub_workflow"];
  const classes = action.destructive ? "danger" : action.recommended || primaryActions.includes(action.name) ? "primary" : "";
  return `<button type="button" class="${classes}" data-run-action="${escapeHTML(action.name)}" data-unavailable="${unavailable ? "true" : "false"}" aria-label="${escapeHTML(label)}" title="${escapeHTML(tooltip)}"${unavailable ? " disabled aria-disabled=\"true\"" : ""}>${escapeHTML(label)}</button>`;
}

function workflowRunActionLabel(name, fallback = "") {
  if (name === "cancel") return t("chat.cancelRun");
  if (name === "retry") return t("chat.retryRun");
  if (name === "open_workflow_studio") return t("chat.openWorkflowStudio");
  if (name === "approve_stage") return t("approvals.approveStage");
  if (name === "approve_tool") return t("approvals.approveTool");
  if (name === "approve_all_tools") return t("approvals.approveAllTools");
  if (name === "approve_remember_tool") return t("approvals.approveRemember");
  if (name === "approve_remember_all_tools") return t("approvals.approveRememberAllTools");
  if (name === "deny_tool") return t("approvals.denyTool");
  if (name === "deny_all_tools") return t("approvals.denyAllTools");
  if (name === "submit_input") return t("chat.submitWorkflowInput");
  if (name === "resume_sub_workflow") return t("chat.resumeSubWorkflow");
  if (name === "approve_budget") return t("workflow.approveBudget");
  if (name === "continue_output") return t("workflow.continueOutput");
  if (name === "escalate_model") return t("workflow.escalateModel");
  if (name === "apply_staged_changes") return fallback ? localizedText(fallback) : t("chat.applyStagedChanges");
  if (name === "restore_staged_changes") return fallback ? localizedText(fallback) : t("chat.restoreStagedChanges");
  if (name === "discard_staged_changes") return fallback ? localizedText(fallback) : t("chat.discardStagedChanges");
  return runActionFallbackLabel(name, fallback, t("chat.runActionsTitle"));
}

function runActionFallbackLabel(name, fallback = "", defaultLabel = "") {
  const raw = String(fallback || "").trim();
  if (raw) return localizedText(raw);
  const generated = String(name || "").trim().replace(/[_-]+/g, " ");
  return generated ? localizedText(generated) : defaultLabel;
}

function isStagedRunAction(action = {}) {
  return action.name === "apply_staged_changes" || action.name === "restore_staged_changes" || action.name === "discard_staged_changes";
}

function runAwaitingStagedApply(actions = []) {
  return actions.some(action => action?.name === "apply_staged_changes" && action.available !== false && action.workspace_mode !== "live");
}

function stagedRunActionSummary(actions = []) {
  const action = actions.find(isStagedRunAction);
  if (!action) return "";
  const parts = [];
  const changeCount = Number(action.change_count || 0);
  if (changeCount > 0) parts.push(`${changeCount} 个文件变更`);
  const recoveryID = String(action.recovery_id || "").trim();
  if (recoveryID) parts.push(`恢复编号 ${recoveryID}`);
  const reason = localizedWorkflowActionReason(action.reason || "");
  if (reason) parts.push(reason);
  return parts.join(" · ");
}

function stagedRunActionResponseSummary(response = {}, action = {}) {
  const manifest = response?.manifest || {};
  const apply = response?.apply || response?.recovery || {};
  const changeCount = Array.isArray(manifest.changes) ? manifest.changes.length : Number(action.change_count || 0);
  const recoveryID = String(apply.apply_id || action.recovery_id || "").trim();
  const live = manifest.workspace_mode === "live" || action.workspace_mode === "live";
  const resultLabel = action.name === "restore_staged_changes"
    ? (live ? "已回退到上一工作区版本" : "已恢复应用前状态")
    : action.name === "discard_staged_changes"
      ? (live ? "已撤销本次工作区变更" : "已放弃暂存变更")
      : (live ? "已保存当前工作区版本" : "已写回真实工作区");
  const parts = [resultLabel];
  if (changeCount > 0) parts.push(`${changeCount} 个文件变更`);
  if (recoveryID) parts.push(`恢复编号 ${recoveryID}`);
  return parts.join(" · ");
}

async function executeWorkflowRunAction(runState, action, collaborationState) {
  if (!action || action.available === false || !action.path) return;
  const isCancelAction = action.name === "cancel";
  const useBackgroundAction = workflowRunActionPrefersBackground(action);
  if (runState.cancelActionInFlight) return;
  if (runState.actionInFlight && !isCancelAction) return;
  if (!(await ensureWorkspaceRequirementBeforeAction(runState, action, "workflow"))) return;
  if (isCancelAction) {
    runState.cancelActionInFlight = true;
    runState.activeActionName = "cancel";
  } else {
    runState.actionInFlight = true;
    runState.blockingActionName = action.name || "";
    runState.activeActionName = action.name || "";
  }
  const actionLabel = workflowRunActionLabel(action.name, action.label);
  appendTimelineEvent(runState.messages, {
    tone: action.destructive ? "approval" : "stage",
    title: actionLabel,
    detail: localizedWorkflowActionReason(action.reason || "") || action.path
  });
  try {
    setRunStatus(runState.root, runState.runStatus, runState.send, true, actionLabel);
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
    runState.requestInFlight = true;
    runState.currentRunType = "workflow";
    runState.durableWorkflowActive = useBackgroundAction;
    if (!(useBackgroundAction && action.path)) {
      startWorkflowRunPolling(runState, collaborationState, { runID: runState.currentRunID, workflowName: runState.currentWorkflowName });
    }
    syncRunUnloadGuard(runState);
    if (useBackgroundAction && action.path) {
      const response = await request(action.path, {
        method: action.method || "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ background: true })
      });
      await handleWorkflowBackgroundResponse(response, runState.messages, runState, collaborationState, response.name || runState.currentWorkflowName, action.name || "");
      startWorkflowRunPolling(runState, collaborationState, {
        runID: runState.currentRunID,
        workflowName: runState.currentWorkflowName,
        eventsURL: runEventsURLFromActionResponse(response, action)
      });
    } else {
      const response = await request(action.path, { method: action.method || "POST" });
      if (action.name === "retry") {
        await handleWorkflowResponse(response, runState.messages, runState, collaborationState, response.name || runState.currentWorkflowName, true);
      } else if (action.name === "approve_stage" || action.name === "approve_tool" || action.name === "deny_tool") {
        await handleWorkflowResponse(response, runState.messages, runState, collaborationState, response.name || response.workflow_name || runState.currentWorkflowName, true);
        if (runState.currentRunID) {
          const run = await fetchWorkflowRun(runState.currentRunID).catch(() => null);
          if (run) await renderWorkflowRunSnapshot(runState, collaborationState, run);
        }
      } else if (action.name === "cancel") {
        await renderWorkflowRunSnapshot(runState, collaborationState, response);
      } else if (response?.id) {
        await renderWorkflowRunSnapshot(runState, collaborationState, response);
      }
    }
  } catch (error) {
    const message = chatDisplayText(error.message || t("chat.errorTitle"));
    appendTimelineEvent(runState.messages, {
      tone: "error",
      title: t("chat.errorTitle"),
      detail: message
    });
    setTimelineHint(runState, t("chat.timelineError"));
    setResultStatus(runState, t("chat.resultError"));
  } finally {
    if (isCancelAction) {
      runState.cancelActionInFlight = false;
      runState.activeActionName = runState.actionInFlight ? runState.blockingActionName : "";
    } else {
      runState.actionInFlight = false;
      runState.blockingActionName = "";
      if (!runState.cancelActionInFlight) runState.activeActionName = "";
    }
    runState.requestInFlight = runState.actionInFlight || runState.cancelActionInFlight;
    await refreshWorkflowRunAfterAction(runState, collaborationState);
    setRunStatus(runState.root, runState.runStatus, runState.send, shouldPollWorkflowRunStatus(runState.lastWorkflowStatus), currentRunStatusLabel(runState));
    if (!shouldPollWorkflowRunStatus(runState.lastWorkflowStatus)) {
      stopWorkflowRunPolling(runState);
    }
    syncRunUnloadGuard(runState);
  }
}

async function refreshWorkflowRunAfterAction(runState, collaborationState) {
  const runID = runState.currentRunID || "";
  if (runID) {
    const run = await fetchWorkbenchRunSnapshot(runID, "workflow").catch(() => null);
    if (run) await renderWorkflowRunSnapshot(runState, collaborationState, run, { hydrate: false });
  }
  await runState.refreshRuntime?.().catch(() => {});
  runState.historyRefresh?.();
  markCollaborationStale(collaborationState);
}

function syncWorkflowActionButtonsPending(container, runState) {
  if (!container || !runState) return;
  const pending = runState.actionInFlight || runState.cancelActionInFlight;
  const activeActionName = runState.activeActionName || runState.blockingActionName || "";
  setWorkflowActionButtonsPending(container, pending, activeActionName);
}

function setWorkflowActionButtonsPending(container, pending, activeActionName = "") {
  if (!container) return;
  container.classList.toggle("is-action-pending", pending);
  container.setAttribute("aria-busy", pending ? "true" : "false");
  container.querySelectorAll("button[data-run-action], button[data-history-action], button[data-agent-action]").forEach(button => {
    const name = button.dataset.runAction || button.dataset.historyAction || button.dataset.agentAction || "";
    const unavailable = button.dataset.unavailable === "true";
    const canCancel = pending && activeActionName !== "cancel" && name === "cancel" && !unavailable;
    const disabled = unavailable || (pending && !canCancel);
    button.disabled = disabled;
    button.setAttribute("aria-disabled", disabled ? "true" : "false");
  });
}

function workflowRunActionPrefersBackground(action = {}) {
  if (!action?.path || action.name === "cancel") return false;
  if (action.background === true) return true;
  return ["approve_stage", "approve_tool", "approve_all_tools", "approve_budget", "continue_output", "escalate_model", "retry", "resume_sub_workflow"].includes(action.name);
}

function workflowRunActionCanOpenLocally(action = {}) {
  return ["open_workflow_studio", "submit_input"].includes(action.name);
}

function workflowRunActionUnavailableHelp(actions) {
  const unavailable = (actions || []).filter(action => action.available === false);
  if (!unavailable.length) return "";
  const approval = unavailable.find(action => ["approve_tool", "deny_tool"].includes(action.name));
  if (approval) {
    return t("chat.workflowToolApprovalUnavailableHelp");
  }
  const remember = unavailable.find(action => ["approve_all_tools", "approve_remember_tool", "approve_remember_all_tools"].includes(action.name));
  if (remember) return localizedWorkflowActionReason(remember.reason || "") || t("chat.toolRememberBlockedByRiskPolicy");
  return localizedWorkflowActionReason(unavailable[0].reason || "") || t("chat.workflowActionUnavailableHelp");
}

function localizedWorkflowActionReason(reason) {
  const value = String(reason || "").trim();
  if (!value) return "";
  const lower = value.toLowerCase();
  if (lower.includes("tool_risk_policy") && lower.includes("cannot be remembered")) {
    return t("chat.toolRememberBlockedByRiskPolicy");
  }
  if (lower.includes("tool approval context") && lower.includes("retry or cancel")) {
    return t("approvals.workflowToolContextLost");
  }
  if (lower.includes("continue incomplete stage") || lower.includes("paused graph workflow run with an incomplete next stage")) {
    return t("workflow.continueOutputHelp");
  }
  if (lower.includes("approve hard workflow budget") || lower.includes("hard budget boundary") || lower.includes("budget approval requires")) {
    return t("workflow.runtimeActionApproveBudgetHelp");
  }
  if (lower.includes("pending tool approval context is not available")) {
    return t("chat.agentToolContextUnavailable");
  }
  if (lower.includes("cannot be resumed durably") || lower.includes("approval type cannot be resumed")) {
    return t("approvals.workflowApprovalNotResumable");
  }
  if (lower.includes("in-memory model/tool context")) {
    return t("approvals.workflowToolContextRequired");
  }
  return localizedText(value);
}

function createWorkflowHistoryState(root, runState, collaborationState, refreshRuntime) {
  return {
    root,
    runState,
    collaborationState,
    refreshRuntime,
    list: root.querySelector("#runHistoryList"),
    detail: root.querySelector("#runHistoryDetail"),
    detailShell: root.querySelector(".run-history-detail-shell"),
    status: root.querySelector("#runHistoryStatus"),
    filters: root.querySelector("#runHistoryFilters"),
    refresh: root.querySelector("#runHistoryRefresh"),
    runs: [],
    historyMeta: {},
    historyFilter: "all",
    historyActionFilter: "",
    selectedRunID: "",
    userSelectedRunID: "",
    listKey: "",
    detailKey: "",
    detailSummaryKey: "",
    detailHydratedAt: 0,
    detailRunID: "",
    historyPollTimer: null,
    historyInFlight: false
  };
}

function bindRunEmptyStateActions(root, prompt) {
  root.addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-run-empty-action]") : null;
    if (!button) return;
    event.preventDefault();
    const action = button.dataset.runEmptyAction || "";
    if (action === "focus-prompt") {
      prompt?.focus();
      prompt?.scrollIntoView({ block: "center", behavior: prefersReducedMotion() ? "auto" : "smooth" });
      return;
    }
    if (action === "open-workflows") {
      location.hash = "workflows";
      return;
    }
    if (action === "refresh-history") {
      root.querySelector("#runHistoryRefresh")?.click();
    }
  });
}

function prefersReducedMotion() {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
}

function bindWorkflowHistoryControls(state) {
  state.refresh?.addEventListener("click", () => {
    loadWorkflowRunHistory(state, state.selectedRunID || currentHistoryRunKey(state.runState), { manual: true, forceDetail: true }).catch(error => {
      renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryLoadFailed")));
    });
  });
  state.filters?.addEventListener("click", event => {
    const actionButton = event.target.closest("button[data-history-action-filter]");
    if (actionButton) {
      const nextAction = actionButton.dataset.historyActionFilter || "";
      if (state.historyActionFilter === nextAction) return;
      state.historyActionFilter = nextAction;
      state.userSelectedRunID = "";
      loadWorkflowRunHistory(state, currentHistoryRunKey(state.runState), { manual: true, forceDetail: true }).catch(error => {
        renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryLoadFailed")));
      });
      return;
    }
    const button = event.target.closest("button[data-history-filter]");
    if (!button) return;
    const next = button.dataset.historyFilter || "all";
    if (state.historyFilter === next) return;
    state.historyFilter = next;
    state.userSelectedRunID = "";
    loadWorkflowRunHistory(state, currentHistoryRunKey(state.runState), { manual: true, forceDetail: true }).catch(error => {
      renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryLoadFailed")));
    });
  });
}

function startWorkflowHistoryPolling(state) {
  if (!state || state.historyPollTimer) return;
  state.historyPollTimer = window.setInterval(() => {
    if (document.visibilityState === "hidden") return;
    loadWorkflowRunHistory(state, currentHistoryRunKey(state.runState), { background: true }).catch(() => {});
  }, workflowRunHistoryPollIntervalMS);
}

function stopWorkflowHistoryPolling(state) {
  if (!state?.historyPollTimer) return;
  window.clearInterval(state.historyPollTimer);
  state.historyPollTimer = null;
}

function currentHistoryRunKey(runState) {
  if (!runState?.currentRunID) return "";
  return historyRunKey({
    run_type: runState.currentRunType === "agent" ? "agent" : "workflow",
    id: runState.currentRunID
  });
}

function initialRunHistoryFocus(runState) {
  return consumeRunHistoryNavigationFocus();
}

function consumeRunHistoryNavigationFocus() {
  try {
    const raw = sessionStorage.getItem("goflow.runHistory.focus") || "";
    sessionStorage.removeItem("goflow.runHistory.focus");
    if (!raw) return "";
    const parsed = JSON.parse(raw);
    const runKey = normalizeHistoryRunKey(parsed?.runKey || parsed?.runID || parsed?.id || "", parsed?.runType || parsed?.type || "");
    const age = Date.now() - Number(parsed?.createdAt || 0);
    if (!runKey || age > 5 * 60 * 1000) return "";
    return runKey;
  } catch {
    return "";
  }
}

async function loadWorkflowRunHistory(state, preferredRunID = "", options = {}) {
  if (!state?.list || !state.detail) return;
  if (state.historyInFlight && options.background) return;
  state.historyInFlight = true;
  try {
    if (!options.background) setRunHistoryStatus(state, t("chat.runHistoryLoading"));
    const history = await loadRunHistoryList(state.historyFilter, state.historyActionFilter);
    const runs = history.runs;
    state.historyMeta = history.meta || {};
    state.runs = runs;
    renderRunHistoryFilters(state);
    const selected = options.listOnly ? "" : selectedWorkflowHistoryRunID(state, runs, preferredRunID);
    renderWorkflowRunHistoryList(state, selected, { preserveScroll: options.background });
    state.detailShell?.classList.toggle("hidden", !selected);
    setRunHistoryStatus(state, runHistoryStatusText(runs, state.historyMeta, state.historyFilter, state.historyActionFilter));
    if (options.listOnly) return;
    if (selected) {
      const listed = runs.find(run => historyRunKey(run) === selected) || parseHistoryRunKey(selected);
      const detailSummaryKey = workflowRunHistoryDetailSummaryKey(listed);
      const detailStale = Date.now() - (state.detailHydratedAt || 0) > 15000;
      if (
        options.background &&
        !options.forceDetail &&
        state.detailRunID === selected &&
        state.detailSummaryKey === detailSummaryKey &&
        !detailStale
      ) {
        return;
      }
      await renderWorkflowRunHistoryDetail(state, selected, {
        background: options.background,
        force: options.forceDetail,
        preserveScroll: options.background,
        summaryKey: detailSummaryKey
      });
    } else {
      state.detail.innerHTML = renderRunHistoryEmptyState({
        title: t("chat.runHistoryEmpty"),
        body: t("chat.runHistoryEmptyHelp"),
        primaryLabel: t("chat.runHistoryStartTask"),
        primaryAction: "focus-prompt",
        secondaryLabel: t("chat.runHistoryOpenWorkflows"),
        secondaryAction: "open-workflows"
      });
      state.detailKey = "";
      state.detailSummaryKey = "";
      state.detailHydratedAt = 0;
      state.detailRunID = "";
    }
  } catch (error) {
    renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryLoadFailed")));
  } finally {
    state.historyInFlight = false;
  }
}

async function loadRunHistoryList(historyFilter = "all", actionFilter = "") {
  try {
    const response = await fetchRuns(runHistoryQueryForFilter(historyFilter, actionFilter));
    return normalizeRunCollectionEnvelope(response);
  } catch {
    const [workflowResult, agentResult] = await Promise.allSettled([
      fetchWorkflowRuns(),
      fetchAgentRuns()
    ]);
    const workflowRuns = workflowResult.status === "fulfilled" ? normalizeWorkflowRunList(workflowResult.value) : [];
    const agentRuns = agentResult.status === "fulfilled" ? normalizeAgentRunList(agentResult.value) : [];
    const allRuns = [
      ...workflowRuns.map(run => normalizeHistoryRun(run, "workflow")),
      ...agentRuns.map(run => normalizeHistoryRun(run, "agent"))
    ].slice().sort((a, b) => workflowRunSortValue(b) - workflowRunSortValue(a));
    const counts = runHistoryFallbackCounts(allRuns);
    const runs = allRuns
      .filter(run => historyRunMatchesFilter(run, historyFilter))
      .filter(run => historyRunMatchesActionFilter(run, actionFilter))
      .slice(0, 14);
    return {
      runs,
      meta: {
        source: "fallback",
        counts: {
          ...counts,
          matched: allRuns
            .filter(run => historyRunMatchesFilter(run, historyFilter))
            .filter(run => historyRunMatchesActionFilter(run, actionFilter)).length,
          returned: runs.length
        }
      }
    };
  }
}

function runHistoryQueryForFilter(historyFilter = "all", actionFilter = "") {
  const query = { limit: 14 };
  if (historyFilter === "needs_action") query.needs_action = true;
  if (historyFilter === "active") query.active = true;
  if (historyFilter === "errors") query.errors_only = true;
  if (actionFilter) {
    query.action = actionFilter;
    query.action_name = actionFilter;
  }
  return query;
}

function historyRunMatchesFilter(run, historyFilter = "all") {
  if (!historyFilter || historyFilter === "all") return true;
  if (historyFilter === "needs_action") return Boolean(run?.needs_action || isApprovalStatus(run?.status) || run?.status === "awaiting_input" || run?.status === "awaiting_sub_workflow");
  if (historyFilter === "errors") return Boolean(run?.has_error || ["failed", "error", "action_failed", "denied", "cancelled", "blocked", "interrupted"].includes(String(run?.status || "").toLowerCase()));
  if (historyFilter === "active") {
    const status = String(run?.status || "").toLowerCase();
    return Boolean(status) && !isWorkflowRunTerminal(status);
  }
  return true;
}

function historyRunMatchesActionFilter(run, actionFilter = "") {
  const normalized = String(actionFilter || "").trim();
  if (!normalized) return true;
  const summary = run?.actions_summary || {};
  const names = new Set(runActionSummaryItems(summary).map(action => String(action?.name || "").trim()).filter(Boolean));
  [summary.recommended, summary.recommended_action, summary.recommendedAction].forEach(name => {
    const value = String(name || "").trim();
    if (value) names.add(value);
  });
  if (Array.isArray(run?.actions)) {
    run.actions.forEach(action => {
      const value = String(action?.name || action || "").trim();
      if (value) names.add(value);
    });
  }
  return names.has(normalized);
}

function runHistoryFallbackCounts(runs = []) {
  return runs.reduce((counts, run) => {
    counts.total += 1;
    if (run?.run_type === "agent") counts.agent += 1;
    else counts.workflow += 1;
    if (historyRunMatchesFilter(run, "active")) counts.active += 1;
    if (historyRunMatchesFilter(run, "needs_action")) counts.needs_action += 1;
    if (historyRunMatchesFilter(run, "errors")) counts.errors += 1;
    return counts;
  }, { total: 0, matched: 0, returned: 0, agent: 0, workflow: 0, active: 0, needs_action: 0, errors: 0 });
}

function normalizeRunCollectionEnvelope(value) {
  const runs = normalizeRunCollection(value).slice(0, 14);
  const counts = value?.counts && typeof value.counts === "object" ? value.counts : {};
  const meta = {
    source: "unified",
    filters: value?.filters && typeof value.filters === "object" ? value.filters : {},
    facets: value?.facets && typeof value.facets === "object" ? value.facets : {},
    counts: {
      total: runCollectionCount(counts.total, runs.length),
      matched: runCollectionCount(counts.matched, runs.length),
      returned: runCollectionCount(counts.returned, runs.length),
      active: runCollectionCount(counts.active, 0),
      needs_action: runCollectionCount(counts.needs_action ?? counts.needsAction, 0),
      errors: runCollectionCount(counts.errors, 0),
      agent: runCollectionCount(counts.agent, 0),
      workflow: runCollectionCount(counts.workflow, 0)
    }
  };
  return { runs, meta };
}

function runCollectionCount(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) && number >= 0 ? number : fallback;
}

function runHistoryStatusText(runs, meta = {}, historyFilter = "all", actionFilter = "") {
  if (!runs.length) return (historyFilter && historyFilter !== "all") || actionFilter ? t("chat.runHistoryNoFilterMatches") : t("chat.runHistoryEmpty");
  const counts = meta.counts || {};
  const returned = runCollectionCount(counts.returned, runs.length);
  const matched = runCollectionCount(counts.matched, returned);
  const visibleTotal = Math.max(matched, returned);
  const parts = [
    visibleTotal > returned
      ? t("chat.runHistoryShowing", { returned, total: visibleTotal })
      : t("chat.runHistoryCount", { count: returned })
  ];
  const active = runCollectionCount(counts.active, 0);
  const needsAction = runCollectionCount(counts.needs_action ?? counts.needsAction, 0);
  const errors = runCollectionCount(counts.errors, 0);
  if (needsAction) parts.push(t("chat.runHistoryNeedsActionCount", { count: needsAction }));
  if (errors) parts.push(t("chat.runHistoryErrorCount", { count: errors }));
  if (active) parts.push(t("chat.runHistoryActiveCount", { count: active }));
  if (actionFilter) parts.push(t("chat.runHistoryActionFilterStatus", { action: historyRunActionFilterLabel(actionFilter) }));
  return parts.join(" / ");
}

function renderRunHistoryFilters(state) {
  if (!state?.filters) return;
  const counts = state.historyMeta?.counts || {};
  const total = runCollectionCount(counts.total, state.runs.length);
  const filters = [
    ["all", t("chat.runHistoryFilterAll"), total],
    ["needs_action", t("chat.runHistoryFilterNeedsAction"), runCollectionCount(counts.needs_action ?? counts.needsAction, 0)],
    ["active", t("chat.runHistoryFilterActive"), runCollectionCount(counts.active, 0)],
    ["errors", t("chat.runHistoryFilterErrors"), runCollectionCount(counts.errors, 0)]
  ];
  const actionFacets = runHistoryActionFacets(state.historyMeta?.facets).slice(0, 5);
  if (state.historyActionFilter && !actionFacets.some(item => item.name === state.historyActionFilter)) {
    actionFacets.unshift({ name: state.historyActionFilter, label: "", count: 0 });
  }
  const actionTotal = actionFacets.reduce((sum, item) => sum + runCollectionCount(item.count, 0), 0);
  const actionFilters = actionFacets.length || state.historyActionFilter
    ? [
      { name: "", label: t("chat.runHistoryActionFilterAll"), count: actionTotal || total },
      ...actionFacets.map(item => ({ ...item, label: historyRunActionFilterLabel(item.name, item.label) }))
    ]
    : [];
  state.filters.innerHTML = filters.map(([id, label, count]) => `
    <button type="button" class="${state.historyFilter === id ? "active" : ""}" data-history-filter="${escapeHTML(id)}" aria-pressed="${state.historyFilter === id ? "true" : "false"}">
      <span>${escapeHTML(label)}</span>
      <i>${escapeHTML(String(count))}</i>
    </button>`).join("") + actionFilters.map(item => `
    <button type="button" class="action-filter ${state.historyActionFilter === item.name ? "active" : ""}" data-history-action-filter="${escapeHTML(item.name)}" aria-pressed="${state.historyActionFilter === item.name ? "true" : "false"}">
      <span>${escapeHTML(item.label)}</span>
      <i>${escapeHTML(String(item.count))}</i>
    </button>`).join("");
}

function runHistoryActionFacets(facets = {}) {
  const raw = facets?.actions || facets?.action || facets?.available_actions || facets?.availableActions || [];
  const list = Array.isArray(raw)
    ? raw.map(item => ({
      name: String(item?.name || item?.action || item?.id || item?.value || "").trim(),
      label: String(item?.label || item?.title || "").trim(),
      count: runCollectionCount(item?.count ?? item?.total ?? item?.value_count, 0)
    }))
    : raw && typeof raw === "object"
      ? Object.entries(raw).map(([name, count]) => ({ name, label: "", count: runCollectionCount(count, 0) }))
      : [];
  return list
    .filter(item => item.name && item.count > 0)
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

function normalizeRunCollection(value) {
  const runs = Array.isArray(value)
    ? value
    : Array.isArray(value?.runs)
      ? value.runs
      : Array.isArray(value?.items)
        ? value.items
        : Array.isArray(value?.value)
          ? value.value
          : [];
  return runs
    .map(normalizeRunCollectionItem)
    .filter(run => run.id)
    .sort((a, b) => workflowRunSortValue(b) - workflowRunSortValue(a));
}

function normalizeRunCollectionItem(run) {
  const type = historyRunCollectionType(run);
  const actionsSummary = run?.actions_summary && typeof run.actions_summary === "object"
    ? run.actions_summary
    : run?.actions && typeof run.actions === "object" && !Array.isArray(run.actions)
      ? run.actions
      : null;
  return {
    ...(run || {}),
    run_type: type,
    name: type === "workflow" ? run?.name || run?.workflow || "" : run?.name || run?.agent_id || run?.agent || "",
    workflow: run?.workflow || (type === "workflow" ? run?.name || "" : ""),
    agent_id: run?.agent_id || run?.agent || "",
    events_url: run?.events_url || run?.eventsURL || run?.events_path || run?.eventsPath || "",
    timeline_url: run?.timeline_url || run?.timelineURL || run?.timeline_path || run?.timelinePath || "",
    replay_path: run?.replay_path || run?.replayPath || run?.replay_url || run?.replayURL || "",
    replay_url: run?.replay_url || run?.replayURL || run?.replay_path || run?.replayPath || "",
    artifacts_url: run?.artifacts_url || run?.artifactsURL || run?.artifacts_path || run?.artifactsPath || "",
    stages_url: run?.stages_url || run?.stagesURL || run?.stages_path || run?.stagesPath || "",
    evidence_path: run?.evidence_path || run?.evidencePath || run?.evidence_url || run?.evidenceURL || "",
    evidence_url: run?.evidence_url || run?.evidenceURL || run?.evidence_path || run?.evidencePath || "",
    context_path: run?.context_path || run?.contextPath || run?.context_url || run?.contextURL || "",
    context_url: run?.context_url || run?.contextURL || run?.context_path || run?.contextPath || "",
    actions_path: run?.actions_path || run?.actionsPath || run?.actions_url || run?.actionsURL || "",
    actions_url: run?.actions_url || run?.actionsURL || run?.actions_path || run?.actionsPath || "",
    diffs_path: run?.diffs_path || run?.diffsPath || run?.diffs_url || run?.diffsURL || "",
    diffs_url: run?.diffs_url || run?.diffsURL || run?.diffs_path || run?.diffsPath || "",
    export_url: run?.export_url || run?.exportURL || run?.export_path || run?.exportPath || "",
    cancel_url: run?.cancel_url || run?.cancelURL || run?.cancel_path || run?.cancelPath || "",
    artifacts_count: runCollectionCount(run?.artifacts_count ?? run?.artifactsCount ?? (Array.isArray(run?.artifacts) ? run.artifacts.length : 0), 0),
    actions_summary: actionsSummary
  };
}

function historyRunCollectionType(run) {
  const value = String(run?.run_type || run?.type || run?.source || "").toLowerCase();
  return value.includes("agent") ? "agent" : "workflow";
}

function selectedWorkflowHistoryRunID(state, runs, preferredRunID = "") {
  if (state.userSelectedRunID && runs.some(run => historyRunKey(run) === state.userSelectedRunID)) {
    return state.userSelectedRunID;
  }
  const normalizedPreferred = normalizeHistoryRunKey(preferredRunID, state.runState?.currentRunType);
  if (normalizedPreferred && runs.some(run => historyRunKey(run) === normalizedPreferred)) {
    return normalizedPreferred;
  }
  if (preferredRunID && runs.some(run => run.id === preferredRunID)) {
    return preferredRunID;
  }
  if (state.selectedRunID && runs.some(run => historyRunKey(run) === state.selectedRunID)) {
    return state.selectedRunID;
  }
  return runs[0] ? historyRunKey(runs[0]) : "";
}

function renderWorkflowHistoryError(state, message) {
  setRunHistoryStatus(state, t("chat.runHistoryLoadFailed"));
  state.detailShell?.classList.remove("hidden");
  if (state.detail) state.detail.innerHTML = renderRunHistoryEmptyState({
    title: t("chat.runHistoryLoadFailed"),
    body: message,
    primaryLabel: t("chat.runHistoryRefresh"),
    primaryAction: "refresh-history"
  });
}

function setRunHistoryStatus(state, text) {
  if (!state?.status) return;
  const next = String(text || "");
  if (state.status.textContent !== next) state.status.textContent = next;
}

function renderWorkflowRunHistoryList(state, selectedRunID, options = {}) {
  state.selectedRunID = selectedRunID || "";
  if (!state.runs.length) {
    state.list.innerHTML = `<div class="run-history-empty run-history-compact-empty">${escapeHTML(t("chat.runHistoryEmptyHelp"))}</div>`;
    state.listKey = "";
    return;
  }
  const listKey = workflowRunHistoryListKey(state.runs, selectedRunID);
  if (listKey === state.listKey) return;
  const scrollTop = state.list.scrollTop;
  state.list.innerHTML = state.runs.map(run => workflowRunHistoryItem(run, historyRunKey(run) === selectedRunID)).join("");
  state.listKey = listKey;
  if (options.preserveScroll) {
    state.list.scrollTop = Math.min(scrollTop, Math.max(0, state.list.scrollHeight - state.list.clientHeight));
  }
  state.list.querySelectorAll("[data-history-run]").forEach(button => {
    button.addEventListener("click", () => {
      state.userSelectedRunID = button.dataset.historyRun;
      renderWorkflowRunHistoryDetail(state, button.dataset.historyRun, { force: true }).catch(error => {
        renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryLoadFailed")));
      });
    });
  });
}

function workflowRunHistoryListKey(runs, selectedRunID) {
  return [
    selectedRunID || "",
    ...(runs || []).map(run => [
      historyRunKey(run),
      run.run_type || "",
      historyRunTitle(run),
      run.status || "",
      run.next_stage || "",
      run.pending_call_id || "",
      run.needs_action ? "needs-action" : "",
      run.updated_at || "",
      run.completed_at || "",
      run.started_at || "",
      formatRunTime(run.completed_at || run.updated_at || run.started_at || ""),
      runHistoryActionSummarySignature(run)
    ].join(":"))
  ].join("|");
}

function workflowRunHistoryDetailSummaryKey(run) {
  if (!run) return "";
  return [
    historyRunKey(run),
    run.id || "",
    run.run_type || "",
    run.status || "",
    run.next_stage || "",
    run.pending_call_id || "",
    run.pending_tool_name || "",
    run.agent_id || "",
    run.mode || "",
    run.summary ? String(run.summary).length : 0,
    run.output ? String(run.output).length : 0,
    run.final_message ? String(run.final_message).length : 0,
    run.needs_action ? "needs-action" : "",
    run.updated_at || "",
    run.completed_at || "",
    run.started_at || "",
    runHistoryActionSummarySignature(run),
    Array.isArray(run.completed_stages) ? run.completed_stages.length : run.stages_count || "",
    Array.isArray(run.artifacts) ? run.artifacts.length : run.artifacts_count || "",
    Array.isArray(run.diffs) ? run.diffs.length : run.diffs_count || "",
    Array.isArray(run.events) ? run.events.length : run.events_count || ""
  ].join("|");
}

function workflowRunHistoryItem(run, selected) {
  const status = String(run.status || t("chat.timelineIdle"));
  const label = historyRunTitle(run);
  const time = run.completed_at || run.updated_at || run.started_at || "";
  const actionSignal = historyRunActionSignal(run);
  return `<button type="button" class="run-history-item ${selected ? "active" : ""}" data-history-run="${escapeHTML(historyRunKey(run))}" title="${escapeHTML(run.id || "")}">
    <span class="run-history-item-main">
      <span class="run-history-title-row">
        <strong>${escapeHTML(label)}</strong>
        <i class="badge ${workflowRunStatusTone(status)}">${escapeHTML(workflowRunStatusLabel(status))}</i>
      </span>
      <small class="run-history-id">${escapeHTML(historyRunTypeLabel(run))} / ${escapeHTML(shortRunID(run.id || ""))}</small>
      ${actionSignal ? `<span class="run-history-action-signal ${escapeHTML(actionSignal.tone)}">${escapeHTML(actionSignal.label)}</span>` : ""}
    </span>
    ${time ? `<small class="run-history-time">${escapeHTML(formatRunTime(time))}</small>` : ""}
  </button>`;
}

function historyRunActionSignal(run) {
  const summary = run?.actions_summary || {};
  const recommended = String(summary.recommended || summary.recommended_action || summary.recommendedAction || "").trim();
  const available = Number(summary.available ?? summary.available_count ?? 0);
  if (run?.needs_action) {
    return {
      tone: "warn",
      label: recommended
        ? t("chat.runHistoryRecommendedAction", { action: historyRunActionLabelForSummary(run, recommended, summary) })
        : t("chat.runHistoryNeedsAction")
    };
  }
  if (recommended) {
    return {
      tone: "info",
      label: t("chat.runHistoryRecommendedAction", { action: historyRunActionLabelForSummary(run, recommended, summary) })
    };
  }
  if (available > 0) {
    return {
      tone: "neutral",
      label: t("chat.runHistoryAvailableActions", { count: available })
    };
  }
  return null;
}

function historyRunActionLabelForSummary(run, name, summary = {}) {
  const items = runActionSummaryItems(summary);
  const item = items.find(action => action?.name === name);
  return run?.run_type === "agent"
    ? agentRunActionLabel(name, item?.label)
    : workflowRunActionLabel(name, item?.label);
}

function historyRunActionFilterLabel(name, fallback = "") {
  return workflowRunActionLabel(name, agentRunActionLabel(name, fallback || name));
}

function runActionSummaryItems(summary = {}) {
  if (Array.isArray(summary.available_items)) return summary.available_items;
  if (Array.isArray(summary.availableItems)) return summary.availableItems;
  if (Array.isArray(summary.items)) return summary.items;
  if (Array.isArray(summary.actions)) return summary.actions;
  return [];
}

function runHistoryActionSummarySignature(run = {}) {
  const summary = run?.actions_summary && typeof run.actions_summary === "object" ? run.actions_summary : {};
  const summaryItems = runActionSummaryItems(summary);
  const runActions = Array.isArray(run?.actions) ? run.actions : [];
  const counts = [
    summary.available ?? summary.available_count ?? "",
    summary.unavailable ?? summary.unavailable_count ?? "",
    summary.total ?? summary.total_count ?? "",
    summary.recommended || summary.recommended_action || summary.recommendedAction || ""
  ].join("/");
  return [
    counts,
    ...summaryItems.map(runActionSignature),
    ...runActions.map(runActionSignature)
  ].filter(Boolean).join(",");
}

function runActionSignature(action = {}) {
  if (!action || typeof action !== "object") return String(action || "").trim();
  return [
    action.name || action.action || action.id || "",
    action.label || action.title || "",
    action.available === false ? "0" : "1",
    action.recommended ? "recommended" : "",
    action.destructive ? "destructive" : "",
    action.durable === false ? "non-durable" : "",
    action.method || "",
    action.path || action.url || action.href || "",
    action.reason || ""
  ].map(value => String(value || "").trim()).join("~");
}

async function renderWorkflowRunHistoryDetail(state, runKey, options = {}) {
  if (!runKey) return;
  const listed = state.runs.find(run => historyRunKey(run) === runKey) || parseHistoryRunKey(runKey);
  if (!listed?.id) return;
  state.detailShell?.classList.remove("hidden");
  state.selectedRunID = historyRunKey(listed);
  renderWorkflowRunHistoryList(state, state.selectedRunID, { preserveScroll: options.background || options.preserveScroll });
  const previousScrollNode = workflowRunHistoryDetailScrollNode(state.detail);
  const scrollTop = previousScrollNode?.scrollTop || 0;
  if (!options.background) {
    state.detail.innerHTML = `<div class="run-history-empty">${escapeHTML(t("chat.runHistoryLoading"))}</div>`;
  }
  const run = await hydrateHistoryRun(listed, { summary: true });
  const actions = await historyRunActions(run);
  const detailKey = workflowRunHistoryDetailKey(run, actions);
  if (!options.force && state.detailRunID === state.selectedRunID && state.detailKey === detailKey) {
    state.detailSummaryKey = options.summaryKey || workflowRunHistoryDetailSummaryKey(listed);
    state.detailHydratedAt = Date.now();
    return;
  }
  const detailsState = captureWorkflowRunHistoryDetailsState(state.detail);
  state.detail.innerHTML = workflowRunHistoryDetail(run, actions);
  state.detailRunID = state.selectedRunID;
  state.detailKey = detailKey;
  state.detailSummaryKey = options.summaryKey || workflowRunHistoryDetailSummaryKey(listed);
  state.detailHydratedAt = Date.now();
  restoreWorkflowRunHistoryDetailsState(state.detail, detailsState);
  syncWorkflowActionButtonsPending(state.detail, state.runState);
  const nextScrollNode = workflowRunHistoryDetailScrollNode(state.detail);
  if (nextScrollNode && options.preserveScroll) {
    nextScrollNode.scrollTop = Math.min(scrollTop, Math.max(0, nextScrollNode.scrollHeight - nextScrollNode.clientHeight));
  } else if (nextScrollNode) {
    nextScrollNode.scrollTop = 0;
  }
  state.detail.querySelectorAll("button[data-history-action]").forEach(button => {
    const action = workflowRunHistoryVisibleActions(run, actions).find(item => item.name === button.dataset.historyAction);
    button.addEventListener("click", () => {
      if (button.disabled) return;
      if (state.runState.actionInFlight && action?.name !== "cancel") return;
      setWorkflowActionButtonsPending(state.detail, true, action?.name || "");
      executeHistoryRunAction(state, run, action).catch(error => {
        renderWorkflowHistoryError(state, chatDisplayText(error.message || t("chat.runHistoryActionFailed")));
      }).finally(() => {
        syncWorkflowActionButtonsPending(state.detail, state.runState);
      });
    });
  });
  bindRunHistoryLazyPanels(state, run);
}

function renderRunHistoryEmptyState(options = {}) {
  const primary = options.primaryLabel
    ? `<button type="button" class="primary" data-run-empty-action="${escapeHTML(options.primaryAction || "")}">${escapeHTML(options.primaryLabel)}</button>`
    : "";
  const secondary = options.secondaryLabel
    ? `<button type="button" data-run-empty-action="${escapeHTML(options.secondaryAction || "")}">${escapeHTML(options.secondaryLabel)}</button>`
    : "";
  return `<div class="run-history-empty run-history-action-empty">
    <strong>${escapeHTML(options.title || t("chat.runHistoryEmpty"))}</strong>
    <span>${escapeHTML(options.body || t("chat.runHistoryEmptyHelp"))}</span>
    ${primary || secondary ? `<div class="run-history-empty-actions">${primary}${secondary}</div>` : ""}
  </div>`;
}

function workflowRunHistoryDetailScrollNode(container) {
  return container?.querySelector("[data-history-detail-body]") || container || null;
}

function captureWorkflowRunHistoryDetailsState(container) {
  const state = { open: new Map(), scroll: new Map() };
  container?.querySelectorAll("details[data-detail-key]").forEach(node => {
    const key = node.dataset.detailKey || "";
    state.open.set(key, Boolean(node.open));
    const scrollNode = node.querySelector(".run-diff-code");
    if (scrollNode) {
      state.scroll.set(key, {
        top: scrollNode.scrollTop || 0,
        left: scrollNode.scrollLeft || 0,
        atBottom: isScrollNearBottom(scrollNode)
      });
    }
  });
  return state;
}

function restoreWorkflowRunHistoryDetailsState(container, state) {
  if (!state) return;
  const openState = state instanceof Map ? state : state.open;
  const scrollState = state instanceof Map ? new Map() : state.scroll;
  container?.querySelectorAll("details[data-detail-key]").forEach(node => {
    const key = node.dataset.detailKey || "";
    if (openState?.has(key)) node.open = Boolean(openState.get(key));
    const scrollNode = node.querySelector(".run-diff-code");
    const scroll = scrollState?.get(key);
    if (scrollNode && scroll) restoreRunScrollNode(scrollNode, scroll);
  });
}

function normalizeHistoryRun(run, type) {
  return {
    ...(run || {}),
    ...normalizeRunContractPaths(run),
    run_type: type === "agent" ? "agent" : "workflow"
  };
}

function normalizeRunContractPaths(run = {}) {
  return {
    events_url: run?.events_url || run?.eventsURL || run?.events_path || run?.eventsPath || "",
    timeline_url: run?.timeline_url || run?.timelineURL || run?.timeline_path || run?.timelinePath || "",
    replay_path: run?.replay_path || run?.replayPath || run?.replay_url || run?.replayURL || "",
    replay_url: run?.replay_url || run?.replayURL || run?.replay_path || run?.replayPath || "",
    artifacts_url: run?.artifacts_url || run?.artifactsURL || run?.artifacts_path || run?.artifactsPath || "",
    stages_url: run?.stages_url || run?.stagesURL || run?.stages_path || run?.stagesPath || "",
    evidence_path: run?.evidence_path || run?.evidencePath || run?.evidence_url || run?.evidenceURL || "",
    evidence_url: run?.evidence_url || run?.evidenceURL || run?.evidence_path || run?.evidencePath || "",
    context_path: run?.context_path || run?.contextPath || run?.context_url || run?.contextURL || "",
    context_url: run?.context_url || run?.contextURL || run?.context_path || run?.contextPath || "",
    actions_path: run?.actions_path || run?.actionsPath || run?.actions_url || run?.actionsURL || "",
    actions_url: run?.actions_url || run?.actionsURL || run?.actions_path || run?.actionsPath || "",
    diffs_path: run?.diffs_path || run?.diffsPath || run?.diffs_url || run?.diffsURL || "",
    diffs_url: run?.diffs_url || run?.diffsURL || run?.diffs_path || run?.diffsPath || "",
    export_url: run?.export_url || run?.exportURL || run?.export_path || run?.exportPath || "",
    cancel_url: run?.cancel_url || run?.cancelURL || run?.cancel_path || run?.cancelPath || "",
    artifacts_count: runCollectionCount(run?.artifacts_count ?? run?.artifactsCount ?? (Array.isArray(run?.artifacts) ? run.artifacts.length : 0), 0)
  };
}

function historyRunKey(run) {
  const type = run?.run_type === "agent" ? "agent" : "workflow";
  return run?.id ? `${type}:${run.id}` : "";
}

function normalizeHistoryRunKey(value, fallbackType = "") {
  const raw = String(value || "").trim();
  if (!raw) return "";
  if (raw.includes(":")) return raw;
  const type = fallbackType === "agent" ? "agent" : "workflow";
  return `${type}:${raw}`;
}

function parseHistoryRunKey(value) {
  const raw = String(value || "").trim();
  const match = raw.match(/^(agent|workflow):(.+)$/);
  if (!match) return { run_type: "workflow", id: raw };
  return { run_type: match[1], id: match[2] };
}

function historyRunTypeLabel(run) {
  return run?.run_type === "agent" ? t("chat.runHistoryTypeAgent") : t("chat.runHistoryTypeWorkflow");
}

function historyRunTitle(run) {
  if (run?.run_type === "agent") {
    const agentID = run.agent_id || run.agent || t("chat.agent");
    return [taskResourceDisplayName("agent", agentID, localizedText(agentID)), run.mode ? modeLabel(run.mode) : ""].filter(Boolean).join(" / ");
  }
  return [localizedText(run.name || t("chat.workflow")), `#${run.attempt || 1}`].filter(Boolean).join(" ");
}

async function hydrateHistoryRun(run, options = {}) {
  const type = run?.run_type === "agent" ? "agent" : "workflow";
  const fresh = await fetchRun(run, options.summary ? { summary: true } : {});
  const next = normalizeHistoryRun({ ...run, ...fresh }, type);
  if (options.summary) return next;
  return type === "agent"
    ? normalizeHistoryRun(await hydrateAgentRunDetails(next), type)
    : normalizeHistoryRun(await hydrateWorkflowRunDetails(next), type);
}

async function historyRunActions(run) {
  let actions = [];
  try {
    actions = (await fetchRunActionDiscovery(run)).actions || [];
  } catch {
    actions = Array.isArray(run?.actions) ? run.actions : [];
  }
  return (actions || []).map(action => ({
    ...action,
    run_type: run?.run_type === "agent" ? "agent" : "workflow"
  }));
}

function workflowRunHistoryDetailKey(run, actions) {
  const stages = Array.isArray(run?.completed_stages) ? run.completed_stages.length : 0;
  const artifacts = Array.isArray(run?.artifacts) ? run.artifacts.length : 0;
  const events = Array.isArray(run?.events) ? run.events.length : 0;
  const diffs = Array.isArray(run?.diffs) ? run.diffs.length : 0;
  const risk = toolRiskSignature(runToolRiskContext(run).risk);
  const skillScript = skillScriptContextSignature(extractSkillScriptContextFromRun(run));
  const actionKey = (actions || []).map(runActionSignature).join(",");
  return [
    run?.run_type || "workflow",
    run?.id || "",
    run?.status || "",
    run?.next_stage || "",
    run?.agent_id || "",
    run?.mode || "",
    run?.pending_call_id || "",
    run?.output ? String(run.output).length : 0,
    stages,
    artifacts,
    events,
    diffs,
    risk,
    skillScript,
    runHistoryActionSummarySignature(run),
    actionKey
  ].join("|");
}

function workflowRunHistoryDetail(run, actions) {
  const output = historyRunResultText(run);
  const stages = Array.isArray(run.completed_stages) ? run.completed_stages : [];
  const artifacts = Array.isArray(run.artifacts) ? run.artifacts : [];
  const events = Array.isArray(run.events) ? run.events : [];
  const diffs = Array.isArray(run.diffs) ? run.diffs : [];
  const isAgent = run.run_type === "agent";
  const stageCount = stages.length || run.stages_count || 0;
  const artifactCount = artifacts.length || run.artifacts_count || 0;
  const eventCount = events.length || run.events_count || 0;
  const diffCount = diffs.length || run.diffs_count || 0;
  const facts = [
    detailChipHTML(t("chat.runHistoryStatus"), workflowRunStatusLabel(run.status || "")),
    detailChipHTML(isAgent ? t("chat.runHistoryAgent") : t("chat.runHistoryStage"), isAgent ? taskResourceDisplayName("agent", run.agent_id || run.agent || "-", localizedText(run.agent_id || run.agent || "-")) : localizedText(run.next_stage || lastCompletedStageName(run) || "-")),
    isAgent ? detailChipHTML(t("chat.runHistoryMode"), modeLabel(run.mode || "")) : detailChipHTML(t("chat.runHistoryStages"), String(stageCount)),
    detailChipHTML(t("chat.runHistoryArtifacts"), String(artifactCount)),
    detailChipHTML(t("chat.runHistoryDiffs"), String(diffCount)),
    detailChipHTML(t("chat.runHistoryEvents"), String(eventCount))
  ].filter(Boolean).join("");
  const visibleActions = workflowRunHistoryVisibleActions(run, actions);
  const actionHTML = visibleActions.length
    ? visibleActions.map(historyRunActionButton).join("")
    : `<p class="muted">${escapeHTML(t("chat.runHistoryNoActions"))}</p>`;
  const exportHTML = historyRunExportButtons(run);
  const outputSummary = output || publicResultText(run.summary || "");
  const summary = outputSummary || publicResultText(run.approval_prompt || run.request || "") || t("chat.runHistoryNoSummary");
  const nextStep = workflowRunHistoryNextStep(run, visibleActions);
  const delivery = workflowRunDeliveryHTML(run, { summary, hasOutput: Boolean(outputSummary) });
  const lazyPanels = runHistoryLazyPanelsHTML(run, { isAgent, stageCount, artifactCount, eventCount, diffCount });
  const contextual = runHistoryContextualSummaryHTML(run, { isAgent, stageCount, artifactCount, eventCount, diffCount });
  const evidenceStack = [contextual, lazyPanels].filter(Boolean).join("");
  const actionCount = visibleActions.length
    ? `<small>${escapeHTML(String(visibleActions.length))}</small>`
    : "";
  return `<article class="run-history-card">
    <div class="run-history-card-head">
      <div>
        <strong>${escapeHTML(historyRunTitle(run))}</strong>
        <small>${escapeHTML(run.id || "")}</small>
      </div>
      <span class="badge ${workflowRunStatusTone(run.status)}">${escapeHTML(historyRunTypeLabel(run))} / ${escapeHTML(workflowRunStatusLabel(run.status || ""))}</span>
    </div>
    <div class="run-history-card-content">
      <div class="run-history-card-body" data-history-detail-body>
        <div class="run-history-next-step ${escapeHTML(nextStep.tone)}">
          <strong>${escapeHTML(nextStep.title)}</strong>
          <span>${escapeHTML(nextStep.body)}</span>
        </div>
        ${delivery}
        <div class="run-history-facts">${facts}</div>
        ${evidenceStack ? `<div class="run-history-evidence-stack">${evidenceStack}</div>` : ""}
      </div>
      <div class="run-history-actions-shell">
        <div class="run-history-actions-head">
          <div>
            <strong>${escapeHTML(t("chat.runActionsTitle"))}</strong>
            <span>${escapeHTML(t("chat.runActionsHelp"))}</span>
          </div>
          ${actionCount}
        </div>
        <div class="workflow-run-actions run-history-actions">${actionHTML}</div>
        ${exportHTML}
      </div>
    </div>
  </article>`;
}

function workflowRunHistoryVisibleActions(run, actions) {
  const visible = Array.isArray(actions)
    ? actions.filter(action => !(run?.run_type === "agent" && action?.name === "approve_remember_tool" && action.available === false))
    : [];
  if (run?.run_type !== "agent" && run?.id && !visible.some(action => action?.name === "open_workflow_studio")) {
    visible.push({
      name: "open_workflow_studio",
      label: t("chat.openWorkflowStudio"),
      available: true,
      run_type: "workflow"
    });
  }
  return visible;
}

function runHistoryLazyPanelsHTML(run, counts = {}) {
  const panels = [
    runHistoryLazyPanelHTML("delivery", t("chat.runHistoryLazyDelivery"), t("chat.runHistoryLazyDeliveryHelp"), 1, t("chat.runDeliveryTitle")),
    runHistoryLazyPanelHTML("context", t("chat.runHistoryLazyContext"), t("chat.runHistoryLazyContextHelp"), 1, t("chat.runContextTitle")),
    runHistoryLazyPanelHTML("timeline", t("chat.runHistoryLazyTimeline"), t("chat.runHistoryLazyTimelineHelp"), counts.eventCount || 0, t("chat.runHistoryEvents")),
    runHistoryLazyPanelHTML("artifacts", t("chat.runHistoryLazyArtifacts"), t("chat.runHistoryLazyArtifactsHelp"), counts.artifactCount || 0, t("chat.runArtifactsTitle")),
    runHistoryLazyPanelHTML("diffs", t("chat.runHistoryLazyDiffs"), t("chat.runHistoryLazyDiffsHelp"), counts.diffCount || 0, t("chat.runHistoryDiffs"))
  ].filter(Boolean).join("");
  if (!panels) return "";
  return `<section class="run-history-lazy-stack" data-history-lazy-stack data-run-kind="${escapeHTML(run?.run_type || "workflow")}" data-run-id="${escapeHTML(run?.id || "")}">
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.runHistoryLazyTitle"))}</strong>
        <span>${escapeHTML(t("chat.runHistoryLazyHelp"))}</span>
      </div>
      <small>${escapeHTML(t("chat.runHistoryLazySummaryFirst"))}</small>
    </div>
    ${panels}
  </section>`;
}

function runHistoryContextualSummaryHTML(run, counts = {}) {
  const tokenSummary = runTokenUsageSummary(run);
  const toolRisk = runToolRiskContext(run);
  const tokenDetails = tokenSummary ? runHistoryTokenDetailHTML(tokenSummary) : "";
  const items = [
    tokenSummary ? {
      tone: "cost",
      label: t("chat.runContextualCostTitle"),
      value: tokenCountText(tokenSummary.grossPrompt || tokenSummary.estimatedPrompt || tokenSummary.total || tokenSummary.prompt || 0),
      help: t("chat.runContextualHistoryCostHelp"),
      detail: tokenDetails
    } : null,
    toolRisk?.risk ? {
      tone: "tool",
      label: t("chat.runContextualToolTitle"),
      value: runToolRiskLevelLabel(toolRisk.risk.level || toolRisk.risk.risk_level || ""),
      help: t("chat.runContextualHistoryToolHelp")
    } : null,
    (counts.artifactCount || counts.diffCount || counts.stageCount) ? {
      tone: "evidence",
      label: t("chat.runContextualEvidenceTitle"),
      value: t("chat.runContextualHistoryEvidenceValue", {
        artifacts: counts.artifactCount || 0,
        diffs: counts.diffCount || 0
      }),
      help: t("chat.runContextualHistoryEvidenceHelp")
    } : null
  ].filter(Boolean);
  if (!items.length) return "";
  return `<section class="run-history-contextual-summary">
    ${items.map(item => `<span class="${escapeHTML(item.tone)}">
      <small>${escapeHTML(item.label)}</small>
      <strong>${escapeHTML(item.value || "-")}</strong>
      <em>${escapeHTML(item.help)}</em>
      ${item.detail || ""}
    </span>`).join("")}
  </section>`;
}

function runHistoryTokenDetailHTML(summary = {}) {
  const facts = [
    summary.savedTokens ? [t("workflow.runtimeField.budgetSavedTokens"), tokenCountText(summary.savedTokens)] : null,
    summary.grossPrompt ? [t("workflow.runtimeField.budgetGrossPromptTokens"), tokenCountText(summary.grossPrompt)] : null,
    summary.netPrompt ? [t("workflow.runtimeField.budgetNetPromptTokens"), tokenCountText(summary.netPrompt)] : null,
    summary.budgetLimit ? [t("workflow.runtimeField.budgetLimit"), summary.budgetLimit] : null
  ].filter(Boolean);
  if (!facts.length) return "";
  return `<div class="run-history-token-details">${facts.map(([label, value]) => `<b><i>${escapeHTML(label)}</i><strong>${escapeHTML(value)}</strong></b>`).join("")}</div>`;
}

function runHistoryLazyPanelHTML(kind, title, help, count, fallbackLabel) {
  const disabled = Number(count || 0) <= 0;
  return `<section class="run-history-lazy-panel" data-history-lazy-panel="${escapeHTML(kind)}">
    <div>
      <strong class="run-history-lazy-panel-title">${escapeHTML(title || fallbackLabel)}</strong>
      <span>${escapeHTML(help || "")}</span>
    </div>
    <button type="button" class="ghost-button" data-history-load="${escapeHTML(kind)}" ${disabled ? "disabled aria-disabled=\"true\"" : ""}>
      <span class="run-history-load-icon" aria-hidden="true"></span>
      <span class="run-history-load-copy">
        <strong>${escapeHTML(disabled ? t("chat.runHistoryLazyEmpty") : t("chat.runHistoryLazyLoad", { count }))}</strong>
        <small>${escapeHTML(title || fallbackLabel || kind)}</small>
      </span>
    </button>
    <div class="run-history-lazy-content" data-history-lazy-content="${escapeHTML(kind)}"></div>
  </section>`;
}

function bindRunHistoryLazyPanels(state, run) {
  const stack = state.detail?.querySelector("[data-history-lazy-stack]");
  if (!stack || !run?.id) return;
  stack.querySelectorAll("[data-history-load]").forEach(button => {
    button.addEventListener("click", async () => {
      if (button.disabled) return;
      const kind = button.dataset.historyLoad || "";
      const panel = button.closest("[data-history-lazy-panel]");
      const content = panel?.querySelector("[data-history-lazy-content]");
      if (!panel || !content) return;
      const label = button.querySelector(".run-history-load-copy strong");
      const previousLabel = label?.textContent || "";
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      content.innerHTML = `<div class="run-history-lazy-loading">${escapeHTML(t("chat.runHistoryLazyLoading"))}</div>`;
      try {
        const html = await loadRunHistoryLazyPanel(run, kind);
        content.innerHTML = html || `<div class="run-history-empty">${escapeHTML(t("chat.runHistoryLazyNoDetails"))}</div>`;
        panel.classList.add("loaded");
        if (label) label.textContent = t("chat.runHistoryLazyLoaded");
      } catch (error) {
        content.innerHTML = `<div class="run-history-empty">${escapeHTML(chatDisplayText(error.message || t("chat.runHistoryLoadFailed")))}</div>`;
        if (label) label.textContent = previousLabel;
        button.disabled = false;
      } finally {
        button.setAttribute("aria-busy", "false");
      }
    });
  });
}

async function loadRunHistoryLazyPanel(run, kind) {
  if (kind === "delivery") return runHistoryLazyDeliveryHTML(run);
  if (kind === "context") return runHistoryLazyContextHTML(run);
  if (kind === "timeline") return runHistoryLazyTimelineHTML(run);
  if (kind === "artifacts") return runHistoryLazyArtifactsHTML(run);
  if (kind === "evidence") return runHistoryLazyEvidenceHTML(run);
  if (kind === "diffs") return runHistoryLazyDiffsHTML(run);
  return "";
}

async function runHistoryLazyDeliveryHTML(run) {
  const manifest = await fetchRunDelivery(run);
  return runDeliveryManifestHTML(manifest);
}

function runDeliveryManifestHTML(manifest = {}) {
  const conclusion = String(manifest.conclusion || "unverified").trim();
  const quality = manifest.quality && typeof manifest.quality === "object" ? manifest.quality : {};
  const metrics = manifest.metrics && typeof manifest.metrics === "object" ? manifest.metrics : {};
  const gates = Array.isArray(manifest.hard_gates) ? manifest.hard_gates : [];
  const checks = [
    ...(Array.isArray(manifest.acceptance) ? manifest.acceptance : []),
    ...(Array.isArray(manifest.verification) ? manifest.verification : [])
  ];
  const failedGates = gates.filter(gate => gate?.required && !gate?.passed);
  const failedChecks = checks.filter(check => ["failed", "unknown", "warning"].includes(String(check?.status || "").toLowerCase()));
  const unverified = Array.isArray(manifest.unverified) ? manifest.unverified : [];
  const risks = Array.isArray(manifest.remaining_risks) ? manifest.remaining_risks : [];
  const score = Number(quality.score ?? 0);
  const facts = [
    [t("chat.runDeliveryScore"), String(Number.isFinite(score) ? score : 0)],
    [t("chat.runDeliveryStrongEvidence"), String(Number(quality.strong_evidence || 0))],
    [t("chat.runDeliveryChanges"), String(Number(metrics.changes || 0))],
    [t("chat.runDeliveryToolCalls"), String(Number(metrics.tool_calls || 0))],
    [t("chat.runDeliveryTokens"), tokenCountText(metrics.total_tokens || 0)]
  ];
  const issueItems = [
    ...failedGates.map(gate => gate.detail || gate.name),
    ...failedChecks.map(check => check.detail || check.name || check.kind),
    ...unverified,
    ...risks
  ].filter(Boolean);
  const checksHTML = checks.length
    ? `<div class="run-delivery-checks">${checks.slice(0, 10).map(check => `<div class="run-delivery-check ${acceptanceToneClass(check.status)}">
        <span>${escapeHTML(localizedText(check.name || check.kind || t("chat.runDeliveryCheck")))}</span>
        <strong>${escapeHTML(acceptanceStatusLabel(check.status || "unknown"))}</strong>
      </div>`).join("")}</div>`
    : `<div class="run-history-empty">${escapeHTML(t("chat.runDeliveryNoChecks"))}</div>`;
  return `<section class="run-delivery-manifest ${acceptanceToneClass(conclusion)}">
    <div class="run-evidence-card-head">
      <div>
        <strong>${escapeHTML(t("chat.runDeliveryTitle"))}</strong>
        <small>${escapeHTML(t("chat.runDeliverySchema", { version: manifest.schema_version || "v1" }))}</small>
      </div>
      <span class="badge ${workflowRunStatusTone(conclusion)}">${escapeHTML(runDeliveryConclusionLabel(conclusion))}</span>
    </div>
    <p>${escapeHTML(localizedText(manifest.summary || manifest.request || t("chat.runDeliveryNoSummary")))}</p>
    <div class="run-evidence-meta">${facts.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>
    ${checksHTML}
    ${issueItems.length ? `<div class="run-delivery-issues"><strong>${escapeHTML(t("chat.runDeliveryIssues"))}</strong><ul>${issueItems.slice(0, 10).map(item => `<li>${escapeHTML(localizedText(item))}</li>`).join("")}</ul></div>` : `<div class="run-delivery-pass">${escapeHTML(t("chat.runDeliveryPassed"))}</div>`}
  </section>`;
}

function runDeliveryConclusionLabel(value) {
  const key = String(value || "unverified").toLowerCase();
  const labels = {
    success: t("chat.runDeliveryConclusion.success"),
    partial: t("chat.runDeliveryConclusion.partial"),
    unverified: t("chat.runDeliveryConclusion.unverified"),
    failed: t("chat.runDeliveryConclusion.failed"),
    waiting_input: t("chat.runDeliveryConclusion.waiting_input"),
    running: t("chat.runDeliveryConclusion.running")
  };
  return labels[key] || localizedText(value || t("chat.runDeliveryConclusion.unverified"));
}

async function runHistoryLazyContextHTML(run) {
  const payload = await fetchRunContext(run);
  return runContextDiagnosticsHTML(payload);
}

function runContextDiagnosticsHTML(context = {}) {
  if (!context || typeof context !== "object") return "";
  const latest = context.latest?.budget || context.latest || {};
  const counts = context.counts || {};
  const memoryBlocks = Array.isArray(context.memory_blocks) ? context.memory_blocks : Array.isArray(latest.memory_blocks) ? latest.memory_blocks : [];
  const omitted = Array.isArray(context.omitted_context) ? context.omitted_context : Array.isArray(latest.omitted_context) ? latest.omitted_context : [];
  const artifacts = Array.isArray(context.artifact_refs) ? context.artifact_refs : Array.isArray(latest.artifact_refs) ? latest.artifact_refs : [];
  const toolSchema = context.tool_schema && typeof context.tool_schema === "object" ? context.tool_schema : {};
  const injected = Array.isArray(toolSchema.injected) ? toolSchema.injected : Array.isArray(latest.injected_tool_schemas) ? latest.injected_tool_schemas : [];
  const filtered = Array.isArray(toolSchema.filtered) ? toolSchema.filtered : Array.isArray(latest.filtered_tool_schemas) ? latest.filtered_tool_schemas : [];
  const filteredToolCount = toolSchema.filtered_tool_count ?? latest.filtered_tool_count ?? filtered.length ?? 0;
  const totalToolCount = toolSchema.total_tool_count ?? latest.total_tool_count ?? "";
  const estimatedTokens = latest.estimated_prompt_tokens || counts.latest_estimated_prompt_tokens || 0;
  const memorySaved = latest.memory_estimated_saved_tokens || counts.memory_estimated_saved_tokens || 0;
  const artifactSaved = latest.artifact_omitted_tokens || counts.artifact_omitted_tokens || 0;
  const skillSaved = latest.skill_omitted_tokens || counts.skill_omitted_tokens || 0;
  const historySaved = latest.history_estimated_saved_tokens || counts.history_estimated_saved_tokens || 0;
  const savedTokens = memorySaved + artifactSaved + skillSaved + historySaved;
  const omittedCount = counts.omitted_context || omitted.length || 0;
  const lazyRefCount = latest.artifact_ref_count || counts.artifact_refs || artifacts.length || 0;
  const flow = [
    [t("chat.runContextInputBudget"), tokenCountText(estimatedTokens), t("chat.runContextInputBudgetHelp"), "input"],
    [t("chat.runContextSavedBudget"), tokenCountText(savedTokens), t("chat.runContextSavedBudgetHelp"), "saved"],
    [t("chat.runContextLazyRefs"), String(lazyRefCount), t("chat.runContextLazyRefsHelp"), "refs"],
    [t("chat.runContextOmittedBudget"), String(omittedCount), t("chat.runContextOmittedBudgetHelp"), "omitted"]
  ];
  const stats = [
    [t("chat.runContextEstimatedTokens"), tokenCountText(estimatedTokens)],
    [t("chat.runContextCacheablePrefix"), tokenCountText(latest.cacheable_prefix_tokens || counts.cacheable_prefix_tokens || 0)],
    [t("chat.runContextSamples"), String(counts.prompt_budget_samples || context.history?.length || 0)],
    [t("chat.runContextMemorySaved"), tokenCountText(memorySaved)],
    [t("chat.runContextArtifactRefs"), String(lazyRefCount)],
    [t("chat.runContextFilteredTools"), `${filteredToolCount}/${totalToolCount}`.replace(/\/$/, "")]
  ].filter(([, value]) => value && value !== "0" && value !== "-");
  return `<section class="run-context-diagnostics">
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.runContextTitle"))}</strong>
        <span>${escapeHTML(t("chat.runContextHelp"))}</span>
      </div>
      <small>${escapeHTML(context.run_type === "agent" ? t("chat.runHistoryTypeAgent") : t("chat.runHistoryTypeWorkflow"))}</small>
    </div>
    <div class="run-context-flow">
      ${flow.map(([label, value, help, tone]) => `<span class="run-context-flow-card ${escapeHTML(tone)}"><small>${escapeHTML(label)}</small><strong>${escapeHTML(value)}</strong><em>${escapeHTML(help)}</em></span>`).join("")}
    </div>
    ${stats.length ? `<div class="run-context-stat-grid">${stats.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
    <div class="run-context-grid">
      <section>
        <strong>${escapeHTML(t("chat.runContextMemoryBlocks"))}</strong>
        <div class="run-context-list">
          ${memoryBlocks.length ? memoryBlocks.slice(0, 8).map(runContextMemoryBlockHTML).join("") : runContextEmptyHTML(t("chat.runContextNoMemory"))}
        </div>
      </section>
      <section>
        <strong>${escapeHTML(t("chat.runContextRefsOmitted"))}</strong>
        <div class="run-context-list">
          ${artifacts.slice(0, 5).map(ref => runContextLineHTML(t("chat.runContextArtifactRef"), ref, "artifact")).join("")}
          ${omitted.slice(0, 6).map(item => runContextLineHTML(t("chat.runContextOmitted"), item, "omitted")).join("")}
          ${!artifacts.length && !omitted.length ? runContextEmptyHTML(t("chat.runContextNoOmitted")) : ""}
        </div>
      </section>
      <section>
        <strong>${escapeHTML(t("chat.runContextInjectedTools"))}</strong>
        <div class="run-context-list">
          ${injected.length ? injected.slice(0, 8).map(runContextToolSchemaHTML).join("") : runContextEmptyHTML(t("chat.runContextNoInjectedTools"))}
        </div>
      </section>
      <section>
        <strong>${escapeHTML(t("chat.runContextFilteredToolsList"))}</strong>
        <div class="run-context-list">
          ${filtered.length ? filtered.slice(0, 8).map(runContextToolSchemaHTML).join("") : runContextEmptyHTML(t("chat.runContextNoFilteredTools"))}
        </div>
      </section>
    </div>
  </section>`;
}

function runContextMemoryBlockHTML(block = {}) {
  const title = [block.kind, block.title].filter(Boolean).map(localizedText).join(" / ") || t("chat.runContextMemoryBlock");
  const reason = runContextMemoryBlockReason(block);
  const meta = [
    block.ref ? `ref=${block.ref}` : "",
    block.hash ? `hash=${String(block.hash).slice(0, 12)}` : "",
    block.language ? localizedText(block.language) : "",
    block.size ? `${formatRunArtifactNumber(block.size)} B` : "",
    block.content_mode ? localizedText(block.content_mode) : "",
    block.tokens ? tokenCountText(block.tokens) : "",
    block.estimated_saved_tokens ? `${t("chat.runContextSaved")} ${tokenCountText(block.estimated_saved_tokens)}` : "",
    block.score ? `score=${block.score}` : ""
  ].filter(Boolean).join(" | ");
  const tone = block.kind === "file" ? "file" : "";
  return `<article class="run-context-line ${escapeHTML(tone)}">
    <span class="badge neutral">${escapeHTML(localizedText(block.kind || t("chat.runContextMemoryBlock")))}</span>
    <strong>${escapeHTML(title)}</strong>
    ${reason ? `<em>${escapeHTML(reason)}</em>` : ""}
    ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
  </article>`;
}

function runContextMemoryBlockReason(block = {}) {
  const mode = String(block.content_mode || "").trim().toLowerCase();
  if (mode === "summary") return t("chat.runContextIncludedSummary");
  if (mode === "full") return t("chat.runContextIncludedFull");
  if (String(block.kind || "").trim().toLowerCase() === "file") return t("chat.runContextIncludedFile");
  if (block.ref) return t("chat.runContextIncludedRef");
  return "";
}

function runContextToolSchemaHTML(item = {}) {
  const status = String(item.status || "").trim();
  const title = item.qualified_name || item.name || t("chat.runContextToolSchema");
  const meta = [
    item.kind ? localizedText(item.kind) : "",
    item.tokens ? tokenCountText(item.tokens) : "",
    item.schema_hash ? `hash=${item.schema_hash}` : ""
  ].filter(Boolean).join(" | ");
  const reason = item.reason ? localizedText(item.reason) : "";
  return `<article class="run-context-line tool ${escapeHTML(status)}">
    <span class="badge neutral">${escapeHTML(localizedText(status || t("chat.runContextToolSchema")))}</span>
    <strong>${escapeHTML(localizedText(title))}</strong>
    ${meta ? `<small>${escapeHTML(meta)}</small>` : ""}
    ${reason ? `<small>${escapeHTML(reason)}</small>` : ""}
  </article>`;
}

function runContextLineHTML(label, value, tone = "") {
  const reason = tone === "artifact"
    ? t("chat.runContextLazyLoadReason")
    : tone === "omitted"
      ? t("chat.runContextOmittedReason")
      : "";
  return `<article class="run-context-line ${escapeHTML(tone)}">
    <span class="badge neutral">${escapeHTML(label)}</span>
    <strong>${escapeHTML(localizedText(value))}</strong>
    ${reason ? `<em>${escapeHTML(reason)}</em>` : ""}
  </article>`;
}

function runContextEmptyHTML(text) {
  return `<div class="run-context-empty">${escapeHTML(text)}</div>`;
}

async function runHistoryLazyTimelineHTML(run) {
  const items = await fetchRunTimeline(run, { limit: 120 });
  const events = timelineItemsForTaskRun(items, run);
  if (!events.length) return "";
  return `<div class="run-history-lazy-timeline">${events.map(runHistoryTimelineItemHTML).join("")}</div>`;
}

async function runHistoryLazyArtifactsHTML(run) {
  const payload = await fetchRunArtifacts(run, { envelope: true, include_content: true, limit: 12 });
  return runArtifactViewerHTML(payload, { compact: true });
}

function runHistoryTimelineItemHTML(item = {}) {
  return `<article class="run-history-timeline-item ${escapeHTML(item.tone || "neutral")}">
    <span aria-hidden="true"></span>
    <div>
      <strong>${escapeHTML(localizedText(item.title || t("chat.timelineIdle")))}</strong>
      ${item.detail ? `<p>${escapeHTML(localizedText(item.detail))}</p>` : ""}
    </div>
  </article>`;
}

async function runHistoryLazyEvidenceHTML(run) {
  const [artifacts, stages, evidence] = await Promise.allSettled([
    fetchRunArtifacts(run, { include_content: true, limit: 8 }),
    fetchRunStages(run, { include_content: true, limit: 8 }),
    fetchRunEvidence(run, { limit: 16 })
  ]);
  const next = { ...run };
  if (artifacts.status === "fulfilled") {
    const items = Array.isArray(artifacts.value)
      ? artifacts.value
      : Array.isArray(artifacts.value?.artifacts)
        ? artifacts.value.artifacts
        : Array.isArray(artifacts.value?.items)
          ? artifacts.value.items
          : [];
    if (items.length) next.artifacts = items;
  }
  if (stages.status === "fulfilled" && Array.isArray(stages.value)) next.completed_stages = stages.value;
  if (evidence.status === "fulfilled") {
    const normalized = normalizeWorkflowRunEvidencePayload(evidence.value);
    if (normalized.items.length) next.evidence = normalized.items;
    if (Object.keys(normalized.quality).length) next.quality = normalized.quality;
  }
  return workflowRunEvidenceHTML(next, { compact: true, maxArtifacts: 8, maxStages: 8 });
}

async function runHistoryLazyDiffsHTML(run) {
  const payload = await fetchRunDiffs(run, { include_content: true, limit: 12 });
  return runDiffViewerHTML(payload?.diffs || [], { compact: true, maxDiffs: 12, maxLines: 40 });
}

function historyRunExportButtons(run) {
  if (!run?.id) return "";
  const jsonURL = runExportURL(run, { format: "json", include_content: true });
  const markdownURL = runExportURL(run, { format: "md", include_content: true });
  return `<div class="run-history-export-actions" aria-label="${escapeHTML(t("chat.exportRun"))}">
    <span>${escapeHTML(t("chat.exportRun"))}</span>
    <a href="${escapeHTML(jsonURL)}" target="_blank" rel="noreferrer">${escapeHTML(t("chat.exportRunJSON"))}</a>
    <a href="${escapeHTML(markdownURL)}" target="_blank" rel="noreferrer">${escapeHTML(t("chat.exportRunMarkdown"))}</a>
  </div>`;
}

function detailChipHTML(label, value) {
  return `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(localizedText(value))}</strong></span>`;
}

function workflowRunHistoryNextStep(run, actions = []) {
  const available = (actions || []).filter(action => action.available !== false);
  const has = name => available.some(action => action.name === name);
  const actionLabel = name => {
    const action = available.find(item => item.name === name);
    return run?.run_type === "agent" ? agentRunActionLabel(name, action?.label) : workflowRunActionLabel(name, action?.label);
  };
  const status = String(run?.status || "").toLowerCase();
  if (run?.run_type === "agent") {
    if (has("approve_task_scope") || has("approve_tool") || has("approve_remember_tool") || status === "awaiting_tool_approval") {
      const action = has("approve_task_scope") ? actionLabel("approve_task_scope") : has("approve_tool") ? actionLabel("approve_tool") : has("approve_remember_tool") ? actionLabel("approve_remember_tool") : t("approvals.approveTool");
      return {
        tone: "waiting",
        title: t("chat.runNextStep.approvalTitle"),
        body: t("chat.runNextStep.agentApprovalBody", { action })
      };
    }
    if (shouldPollAgentRunStatus(status)) {
      return {
        tone: "running",
        title: t("chat.runNextStep.runningTitle"),
        body: has("cancel")
          ? t("chat.runNextStep.runningCancelableBody", { action: actionLabel("cancel") })
          : t("chat.runNextStep.runningBody")
      };
    }
    if (["failed", "error", "denied", "blocked", "cancelled", "canceled"].includes(status)) {
      return {
        tone: "error",
        title: t("chat.runNextStep.errorTitle"),
        body: t("chat.runNextStep.agentErrorBody")
      };
    }
    if (["completed", "complete", "done", "success", "succeeded", "finished"].includes(status)) {
      return {
        tone: "success",
        title: t("chat.runNextStep.doneTitle"),
        body: t("chat.runNextStep.agentDoneBody")
      };
    }
    return {
      tone: "neutral",
      title: t("chat.runNextStep.reviewTitle"),
      body: t("chat.runNextStep.agentReviewBody")
    };
  }
  if (has("submit_input") || status === "awaiting_input") {
    return {
      tone: "waiting",
      title: t("chat.runNextStep.inputTitle"),
      body: t("chat.runNextStep.inputBody", { action: actionLabel("submit_input") })
    };
  }
  if (has("approve_stage")) {
    return {
      tone: "waiting",
      title: t("chat.runNextStep.approvalTitle"),
      body: t("chat.runNextStep.approveStageBody", { action: actionLabel("approve_stage") })
    };
  }
  if (has("approve_tool") || has("approve_all_tools") || status === "awaiting_tool_approval" || status === "awaiting_approval") {
    const action = has("approve_all_tools") ? actionLabel("approve_all_tools") : has("approve_tool") ? actionLabel("approve_tool") : t("approvals.approveTool");
    return {
      tone: "waiting",
      title: t("chat.runNextStep.approvalTitle"),
      body: t("chat.runNextStep.approvalBody", { action })
    };
  }
  if (has("resume_sub_workflow") || status === "awaiting_sub_workflow") {
    return {
      tone: "waiting",
      title: t("chat.runNextStep.resumeTitle"),
      body: t("chat.runNextStep.resumeBody", { action: actionLabel("resume_sub_workflow") })
    };
  }
  if (has("approve_budget") || status === "awaiting_budget_approval") {
    return {
      tone: "waiting",
      title: t("workflow.runtimeExplain.budget.title"),
      body: t("chat.runNextStep.approveBudgetBody", { action: actionLabel("approve_budget") })
    };
  }
  if (has("continue_output") || status === "paused_need_more_budget") {
    return {
      tone: "waiting",
      title: t("workflow.runtimeExplain.incomplete.title"),
      body: t("chat.runNextStep.continueOutputBody", { action: actionLabel("continue_output") })
    };
  }
  if (has("escalate_model")) {
    return {
      tone: "waiting",
      title: t("workflow.runtimeExplain.contract.title"),
      body: t("chat.runNextStep.escalateModelBody", { action: actionLabel("escalate_model") })
    };
  }
  if (isWorkflowRunActive(status)) {
    return {
      tone: "running",
      title: t("chat.runNextStep.runningTitle"),
      body: has("cancel")
        ? t("chat.runNextStep.runningCancelableBody", { action: actionLabel("cancel") })
        : t("chat.runNextStep.runningBody")
    };
  }
  if (["failed", "error", "denied", "blocked", "cancelled", "canceled"].includes(status)) {
    return {
      tone: "error",
      title: t("chat.runNextStep.errorTitle"),
      body: has("retry")
        ? t("chat.runNextStep.retryBody", { action: actionLabel("retry") })
        : t("chat.runNextStep.errorBody")
    };
  }
  if (["completed", "complete", "done", "success", "succeeded", "finished"].includes(status)) {
    return {
      tone: "success",
      title: t("chat.runNextStep.doneTitle"),
      body: t("chat.runNextStep.doneBody")
    };
  }
  return {
    tone: "neutral",
    title: t("chat.runNextStep.reviewTitle"),
    body: t("chat.runNextStep.reviewBody")
  };
}

function shortRunID(id) {
  const value = String(id || "").trim();
  if (!value || value.length <= 28) return value || "-";
  const parts = value.split("-");
  if (parts.length >= 4) {
    return `${parts.slice(0, 3).join("-")}...${value.slice(-8)}`;
  }
  return `${value.slice(0, 18)}...${value.slice(-8)}`;
}

function formatRunTime(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString(undefined, {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  });
}

function historyRunActionButton(action) {
  const unavailable = action.available === false || (!action.path && !workflowRunActionCanOpenLocally(action));
  const classes = action.destructive || action.name === "deny_tool" ? "danger" : action.recommended || ["retry", "escalate_model", "approve_stage", "approve_task_scope", "approve_tool", "approve_all_tools", "approve_remember_tool", "submit_input", "resume_sub_workflow"].includes(action.name) ? "primary" : "";
  const label = action.run_type === "agent" ? agentRunActionLabel(action.name, action.label) : workflowRunActionLabel(action.name, action.label);
  const tooltip = localizedWorkflowActionReason(action.reason || "") || label;
  return `<button type="button" class="${classes}" data-history-action="${escapeHTML(action.name)}" data-unavailable="${unavailable ? "true" : "false"}" aria-label="${escapeHTML(label)}" title="${escapeHTML(tooltip)}"${unavailable ? " disabled aria-disabled=\"true\"" : ""}>${escapeHTML(label)}</button>`;
}

async function executeHistoryRunAction(state, run, action) {
  if (run?.run_type === "agent") return executeAgentRunHistoryAction(state, run, action);
  return executeWorkflowRunHistoryAction(state, run, action);
}

async function executeWorkflowRunHistoryAction(state, run, action) {
  if (!action || action.available === false) return;
  state.runState.currentRunID = run.id || state.runState.currentRunID;
  state.runState.currentRunType = "workflow";
  state.runState.currentWorkflowName = run.name || state.runState.currentWorkflowName;
  state.runState.currentStage = run.next_stage || lastCompletedStageName(run) || state.runState.currentStage;
  state.runState.lastWorkflowStatus = run.status || state.runState.lastWorkflowStatus;
  if (action.name === "open_workflow_studio") {
    openWorkflowRunInStudio(run);
    return;
  }
  if (action.name === "submit_input") {
    await renderWorkflowRunSnapshot(state.runState, state.collaborationState, run);
    markCollaborationStale(state.collaborationState);
    return;
  }
  await executeWorkflowRunAction(state.runState, action, state.collaborationState);
  await state.refreshRuntime?.().catch(() => {});
  markCollaborationStale(state.collaborationState);
  state.userSelectedRunID = currentHistoryRunKey(state.runState) || historyRunKey(run) || state.userSelectedRunID;
  await loadWorkflowRunHistory(state, state.userSelectedRunID, { forceDetail: true });
}

function openWorkflowRunInStudio(run = {}) {
  const runID = String(run.id || run.run_id || "").trim();
  if (!runID) return;
  const workflowName = String(run.name || run.workflow_name || "").trim();
  try {
    localStorage.setItem(workflowStudioRunKey, JSON.stringify({
      runID,
      workflowName,
      eventsURL: String(run.events_url || run.eventsURL || run.events_path || run.eventsPath || "").trim(),
      lastEventSeq: 0,
      updatedAt: Date.now()
    }));
    if (workflowName) localStorage.setItem("goflow.workflow.open", workflowName);
  } catch {
    // Studio can still load the view; persistence is only used to focus the run.
  }
  location.hash = "workflows";
}

async function executeAgentRunHistoryAction(state, run, action) {
  if (!action || action.available === false) return;
  state.runState.currentRunID = run.id || state.runState.currentRunID;
  state.runState.currentRunType = "agent";
  state.runState.currentWorkflowName = "";
  state.runState.currentStage = run.agent_id || run.mode || state.runState.currentStage;
  state.runState.lastWorkflowStatus = run.status || state.runState.lastWorkflowStatus;
  await executeAgentRunAction(state.runState, action, state.collaborationState);
  await state.refreshRuntime?.().catch(() => {});
  markCollaborationStale(state.collaborationState);
  state.userSelectedRunID = currentHistoryRunKey(state.runState) || historyRunKey(run) || state.userSelectedRunID;
  await loadWorkflowRunHistory(state, state.userSelectedRunID, { forceDetail: true });
}

function workflowRunSortValue(run) {
  const raw = run?.updated_at || run?.completed_at || run?.started_at || "";
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

function workflowRunStatusTone(status) {
  const value = String(status || "").toLowerCase();
  if (isWorkflowRunActive(value)) return "info";
  if (isApprovalStatus(value) || value === "awaiting_input" || value === "awaiting_sub_workflow" || value === "paused_need_more_budget") return "warn";
  if (["completed", "complete", "done", "success", "succeeded", "finished"].includes(value)) return "good";
  if (["failed", "error", "action_failed", "denied", "blocked", "cancelled", "canceled", "interrupted"].includes(value)) return "bad";
  return "";
}

function workflowRunStatusLabel(status) {
  const value = String(status || "").toLowerCase();
  if (value === "awaiting_tool_approval") return t("chat.status.awaitingToolApproval");
  if (value === "awaiting_approval") return t("chat.status.awaitingApproval");
  if (value === "awaiting_input") return t("chat.status.awaitingInput");
  if (value === "awaiting_sub_workflow") return t("chat.status.awaitingSubWorkflow");
  if (value === "running") return t("chat.status.running");
  if (value === "cancelling") return t("chat.status.cancelling");
  if (value === "paused_need_more_budget") return t("chat.status.pausedContinue");
  if (["completed", "complete", "done", "success", "succeeded", "finished"].includes(value)) return t("chat.status.completed");
  if (["failed", "error", "action_failed"].includes(value)) return t("chat.status.failed");
  if (value === "denied") return t("chat.status.denied");
  if (["cancelled", "canceled"].includes(value)) return t("chat.status.cancelled");
  if (value === "blocked") return t("chat.status.blocked");
  if (value === "interrupted") return t("chat.status.interrupted");
  return status || t("chat.timelineIdle");
}

function workflowRunEventToTimeline(event, run) {
  const type = String(event.type || "").toLowerCase();
  const workflowStatus = String(event.workflow_status || event.status || "").toLowerCase();
  const runStatus = String(run?.status || "").toLowerCase();
  if (type === "token_usage") {
    return timelineTokenUsageEvent(event);
  }
  const lowLevelTypes = new Set(["text", "delta", "token", "message", "final_message", "prompt_budget", "status", "reasoning"]);
  const budgetReason = String(event.budget_reason || event.budgetReason || event.reason || "").toLowerCase();
  const budgetDiagnostic = Boolean(
    budgetReason.includes("budget_") ||
    event.budget_metric ||
    event.budgetMetric ||
    event.budget_soft_limit ||
    event.budgetSoftLimit ||
    event.budget_hard_limit ||
    event.budgetHardLimit
  );
  const providerStatus = isProviderRequestStatusEvent(event);
  if ((lowLevelTypes.has(type) && !budgetDiagnostic && !providerStatus) || type.endsWith("_delta") || type.includes("stream_chunk")) {
    return null;
  }
  const isError = event.is_error || type === "error";
  if (isError) {
    return {
      tone: "error",
      title: t("chat.errorTitle"),
      detail: timelineDetail([event.content, event.arguments_summary])
    };
  }
  if (providerStatus) {
    return {
      tone: String(event.reason || "").toLowerCase() === "provider_request_retry" ? "warn" : "info",
      title: t("chat.providerRequestTitle"),
      detail: providerRequestTimelineDetail(event, run, event.content)
    };
  }
  if (budgetDiagnostic && budgetReason === "budget_soft_limit_hit" && type !== "workflow_result") {
    const budgetLimit = runWorkflowBudgetLimitText({
      budgetMetric: String(event.budget_metric || event.budgetMetric || "").trim(),
      budgetUsed: runTokenNumber(event, ["budget_used", "budgetUsed"]),
      budgetSoftLimit: runTokenNumber(event, ["budget_soft_limit", "budgetSoftLimit"]),
      budgetHardLimit: runTokenNumber(event, ["budget_hard_limit", "budgetHardLimit"]),
      budgetRemaining: runTokenNumber(event, ["budget_remaining", "budgetRemaining"])
    });
    return {
      tone: "warn",
      title: t("chat.timelineBudgetWarning"),
      detail: timelineDetail([event.stage || event.task_stage, event.content || event.detail, budgetLimit])
    };
  }
  if (budgetDiagnostic && budgetReason === "budget_model_route_applied") {
    return {
      tone: "stage",
      title: t("chat.timelineBudgetRoute"),
      detail: timelineDetail([event.stage || event.task_stage, event.content || event.detail, event.source_ref])
    };
  }
  if (budgetDiagnostic && budgetReason === "budget_branch_limit_applied") {
    return {
      tone: "warn",
      title: t("chat.timelineBudgetBranchLimit"),
      detail: timelineDetail([event.stage || event.task_stage, event.content || event.detail, event.arguments_summary, event.source_ref])
    };
  }
  if (event.needs_action || event.suspended || type === "approval" || type.includes("approval")) {
    const scriptInfo = skillScriptEventContext(event, run);
    return {
      tone: "approval",
      title: scriptInfo ? t("chat.skillScriptApprovalTitle") : t("chat.awaitingApprovalTitle"),
      detail: scriptInfo ? skillScriptTimelineDetail(scriptInfo) : timelineDetail([taskToolDisplayName(event.tool_name), event.arguments_summary, approvalDetailText(event.content)])
    };
  }
  if (type.includes("input")) {
    return {
      tone: "approval",
      title: t("chat.awaitingInputTitle"),
      detail: timelineDetail([event.stage, event.content])
    };
  }
  if (type === "workflow_result" || type.includes("workflow_complete") || type.includes("workflow_completed")) {
    if (!isWorkflowRunTerminal(workflowStatus)) return null;
    if (runStatus && !isWorkflowRunTerminal(runStatus)) return null;
    return {
      tone: workflowStatus === "failed" ? "error" : "success",
      title: t("chat.workflowComplete"),
      detail: timelineDetail([event.workflow_name, workflowRunStatusLabel(workflowStatus)])
    };
  }
  if (type.includes("cancelled") || type.includes("canceled")) {
    return {
      tone: "error",
      title: workflowRunStatusLabel("cancelled"),
      detail: timelineDetail([event.workflow_name, event.stage, event.content || event.detail])
    };
  }
  if (type.includes("failed")) {
    return {
      tone: "error",
      title: t("chat.timelineError"),
      detail: timelineDetail([event.workflow_name, event.stage, event.content || event.detail])
    };
  }
  if (type === "tool_call") {
    const toolInfo = taskToolTimelineInfo(event, "call");
    if (toolInfo) return toolInfo;
    const scriptInfo = skillScriptEventContext(event, run);
    return {
      tone: "tool",
      title: scriptInfo ? t("chat.skillScriptCalling") : t("chat.toolCalling"),
      detail: scriptInfo ? skillScriptTimelineDetail(scriptInfo) : timelineDetail([taskToolDisplayName(event.tool_name), event.arguments_summary])
    };
  }
  if (type === "tool_result") {
    if (isApprovalRequiredToolEvent(event)) return null;
    const toolInfo = taskToolTimelineInfo(event, "result");
    if (toolInfo) return toolInfo;
    const scriptInfo = skillScriptEventContext(event, run);
    const commandInfo = commandToolResultTimelineInfo(event);
    return {
      tone: commandInfo?.failed ? "error" : "success",
      title: scriptInfo ? t("chat.skillScriptReturned") : t("chat.toolReturned"),
      detail: scriptInfo ? skillScriptTimelineDetail(scriptInfo) : timelineDetail([taskToolDisplayName(event.tool_name), event.arguments_summary, commandInfo?.detail])
    };
  }
  if (type.includes("tool")) {
    const scriptInfo = skillScriptEventContext(event, run);
    return {
      tone: "tool",
      title: scriptInfo ? t("chat.skillScriptTitle") : event.tool_name ? `${t("chat.tool")}: ${taskToolDisplayName(event.tool_name)}` : t("chat.tool"),
      detail: scriptInfo ? skillScriptTimelineDetail(scriptInfo) : timelineDetail([event.arguments_summary])
    };
  }
  if (type === "task_stage" || type.includes("stage")) {
    if (isLowValueTimelineChunk(event)) return null;
    return {
      tone: "stage",
      title: t("chat.stageProgressTitle"),
      detail: timelineDetail([event.task_stage || event.stage, event.content || event.detail])
    };
  }
  if (type.includes("workflow")) {
    if (isWorkflowWaitingState(event, run)) return null;
    return {
      tone: "stage",
      title: event.workflow_name ? localizedText(event.workflow_name) : t("chat.workflow"),
      detail: timelineDetail([workflowRunStatusLabel(workflowStatus)])
    };
  }
  return null;
}

function commandToolResultTimelineInfo(event = {}) {
  const toolName = String(event.tool_name || "").toLowerCase().split("/").pop();
  if (toolName !== "run_command") return null;
  let payload;
  try {
    payload = JSON.parse(String(event.content || "{}"));
  } catch {
    return null;
  }
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  const exitCode = Number(payload.exit_code);
  const timedOut = payload.timed_out === true;
  const cancelled = payload.cancelled === true;
  const parts = [];
  if (Number.isFinite(exitCode)) parts.push(`exit=${exitCode}`);
  if (Number.isFinite(Number(payload.duration_ms))) parts.push(`${Number(payload.duration_ms)}ms`);
  if (timedOut) parts.push("timeout");
  if (cancelled) parts.push("cancelled");
  const stdout = compactCommandTimelineText(payload.stdout);
  const stderr = compactCommandTimelineText(payload.stderr);
  if (stdout) parts.push(`stdout=${stdout}`);
  if (stderr) parts.push(`stderr=${stderr}`);
  if (payload.stdout_artifact) parts.push(`stdout_log=${payload.stdout_artifact}`);
  if (payload.stderr_artifact) parts.push(`stderr_log=${payload.stderr_artifact}`);
  if (payload.classification) parts.push(`${t("chat.commandClass")}=${payload.classification}`);
  if (payload.risk_level) parts.push(`${t("chat.commandRisk")}=${payload.risk_level}`);
  if (payload.requires_explicit_approval === true) parts.push(t("chat.commandApproval"));
  return {
    failed: timedOut || cancelled || Number.isFinite(exitCode) && exitCode !== 0,
    detail: parts.join(" · ")
  };
}

function compactCommandTimelineText(value) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (!text) return "";
  return text.length > 160 ? `${text.slice(0, 157)}...` : text;
}

function focusWorkflowTimelineEvents(run) {
  const events = Array.isArray(run?.events) ? run.events : [];
  if (!events.length) return [];
  const status = String(run?.status || "").toLowerCase();
  if (!isWorkflowRunActive(status) && !isApprovalStatus(status) && status !== "awaiting_input") {
    return events.slice(-32);
  }
  let lastIndex = events.length - 1;
  while (lastIndex > 0 && isWorkflowTimelineBoundary(events[lastIndex])) {
    lastIndex -= 1;
  }
  let boundary = -1;
  for (let index = lastIndex - 1; index >= 0; index -= 1) {
    if (isWorkflowTimelineBoundary(events[index])) {
      boundary = index;
      break;
    }
  }
  const focused = boundary >= 0 ? events.slice(boundary + 1) : events.slice(-18);
  return focused.length ? focused : events.slice(-18);
}

function isWorkflowTimelineBoundary(event) {
  const type = String(event?.type || "").toLowerCase();
  return type === "workflow_result" || type === "workflow_state";
}

function timelineEventsForWorkflowRun(events, run) {
  const seen = new Set();
  const mapped = [];
  for (const event of events || []) {
    const item = workflowRunEventToTimeline(event, run);
    if (!item) continue;
    const key = `${item.tone || ""}::${item.title || ""}::${item.detail || ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    mapped.push(item);
  }
  if (isApprovalStatus(run?.status) && !mapped.some(item => item.tone === "approval")) {
    mapped.push({
      tone: "approval",
      title: t("chat.awaitingApprovalTitle"),
      detail: timelineDetail([run?.pending_tool_name || run?.next_stage, run?.approval_prompt])
    });
  }
  return mapped;
}

export function timelineItemsForTaskRun(items, run) {
  const seen = new Set();
  const runType = String(run?.run_type || items?.[0]?.run_type || "").toLowerCase();
  const fallbackTitle = runType === "agent" ? t("chat.modeAgent") : t("chat.workflow");
  const events = collapseParallelTaskTimeline(taskTimelineItemsWithoutCompletedCalls(items || [])
    .map(item => workflowReplayTimelineItem(item, run, fallbackTitle))
    .filter(Boolean));
  const mapped = [];
  for (const event of events) {
    const key = `${event.tone || ""}::${event.title || ""}::${event.detail || ""}`;
    if (seen.has(key)) continue;
    seen.add(key);
    mapped.push(event);
  }
  if (runType !== "agent" && isApprovalStatus(run?.status) && !mapped.some(item => item.tone === "approval")) {
    mapped.push({
      tone: "approval",
      title: t("chat.awaitingApprovalTitle"),
      detail: timelineDetail([run?.pending_tool_name || run?.next_stage, run?.approval_prompt])
    });
  }
  return mapped;
}

function taskTimelineItemsWithoutCompletedCalls(items) {
  const completed = new Set((items || [])
    .filter(item => String(item?.type || item?.kind || "").toLowerCase() === "tool_result")
    .map(item => String(item?.tool_call_id || item?.toolCallID || "").trim())
    .filter(Boolean));
  return (items || []).filter(item => {
    const type = String(item?.type || item?.kind || "").toLowerCase();
    const callID = String(item?.tool_call_id || item?.toolCallID || "").trim();
    return !(type === "tool_call" && callID && completed.has(callID));
  });
}

function collapseParallelTaskTimeline(items) {
  const output = [];
  const groups = new Map();
  for (const item of items || []) {
    const groupKind = String(item.groupKind || "").toLowerCase();
    const groupID = String(item.groupID || "").trim();
    if (groupKind !== "parallel" || !groupID) {
      output.push(item);
      continue;
    }
    const key = `${groupKind}:${groupID}`;
    const group = groups.get(key);
    if (group) {
      group.items.push(item);
      continue;
    }
    groups.set(key, { index: output.length, items: [item] });
    output.push(item);
  }
  for (const group of groups.values()) {
    if (group.items.length < 2) continue;
    const descriptions = [...new Set(group.items.map(item => {
      const title = localizedText(item.title || "");
      const detail = localizedText(item.detail || "");
      return detail && detail !== title ? `${title}: ${detail}` : title;
    }).filter(Boolean))];
    const current = group.items.find(item => item.current);
    const visible = descriptions.slice(0, 4);
    if (descriptions.length > visible.length) visible.push(`+${descriptions.length - visible.length}`);
    output[group.index] = {
      tone: parallelTaskTimelineTone(group.items),
      title: t("chat.timelineParallelBranches"),
      detail: compactTimelineText(timelineDetail([
        current ? t("chat.timelineCurrentStep", { step: localizedText(current.title || "") }) : "",
        visible.join(" / ")
      ]), 360),
      groupKind: "parallel",
      groupID: group.items[0].groupID,
      parentID: group.items[0].parentID,
      current: Boolean(current)
    };
  }
  return output;
}

function parallelTaskTimelineTone(items) {
  const tones = new Set((items || []).map(item => item.tone));
  if (tones.has("error")) return "error";
  if (tones.has("warn")) return "warn";
  if (tones.has("approval")) return "approval";
  if (tones.size === 1 && tones.has("success")) return "success";
  return "stage";
}

function workflowReplayTimelineItem(item = {}, run = {}, fallbackTitle = t("chat.workflow")) {
  const envelopeKind = String(item.kind || "").trim().toLowerCase();
  const eventType = String(item.type || "").trim().toLowerCase();
  const diagnosticStatus = eventType === "status" && Boolean(
    item.reason ||
    item.source_ref ||
    item.sourceRef ||
    item.needs_action ||
    item.suspended ||
    ["warning", "error"].includes(String(item.severity || "").toLowerCase())
  );
  const kind = envelopeKind === "event" && !diagnosticStatus ? eventType : (envelopeKind || eventType);
  const plan = Array.isArray(item.plan) ? item.plan : [];
  if (kind === "plan") {
    const planSteps = plan.map(step => localizedText(step?.name || "")).filter(Boolean);
    const currentStep = plan.find(step => step?.current)?.name || "";
    return {
      tone: "stage",
      title: t("chat.timelinePlanPreview"),
      detail: timelineDetail([
        planSteps.join(" -> ") || item.summary,
        currentStep ? t("chat.timelineCurrentStep", { step: localizedText(currentStep) }) : ""
      ]),
      current: Boolean(currentStep)
    };
  }
  if (kind === "token_usage") {
    return timelineTokenUsageEvent(item);
  }
  if (kind === "tool_call" || kind === "tool_result") {
    return taskToolTimelineInfo(item, kind === "tool_call" ? "call" : "result");
  }
  const lowLevelKinds = new Set(["text", "delta", "token", "message", "final_message", "prompt_budget", "status", "reasoning"]);
  const budgetReason = String(item.budget_reason || item.budgetReason || item.reason || "").toLowerCase();
  const budgetDiagnostic = Boolean(
    budgetReason.includes("budget_") ||
    item.budget_metric ||
    item.budgetMetric ||
    item.budget_soft_limit ||
    item.budgetSoftLimit ||
    item.budget_hard_limit ||
    item.budgetHardLimit
  );
  const providerStatus = isProviderRequestStatusEvent(item);
  if ((lowLevelKinds.has(kind) && !budgetDiagnostic && !providerStatus) || kind.endsWith("_delta") || kind.includes("stream_chunk")) return null;
  if (providerStatus) {
    return {
      tone: String(item.reason || "").toLowerCase() === "provider_request_retry" ? "warn" : "info",
      title: t("chat.providerRequestTitle"),
      detail: providerRequestTimelineDetail(item, run, item.summary || item.content || item.detail),
      stage: String(item.task_stage || item.stage || "").trim(),
      agent: String(item.agent_id || item.agentID || "").trim()
    };
  }
  if (isLowValueTimelineChunk(item)) return null;
  const rawTitle = item.title || item.stage || item.tool_name || item.agent_id || item.kind || item.type || run.name || fallbackTitle;
  const title = item.tool_name
    ? taskToolDisplayName(item.tool_name)
    : item.agent_id
      ? taskResourceDisplayName("agent", item.agent_id, localizedText(item.agent_id))
      : taskMachineDisplayName(rawTitle);
  const summary = taskMachineDisplayName(item.summary || item.content || item.status || "");
  if (!title && !summary) return null;
  const budgetLimit = runWorkflowBudgetLimitText({
    budgetMetric: String(item.budget_metric || item.budgetMetric || "").trim(),
    budgetUsed: runTokenNumber(item, ["budget_used", "budgetUsed"]),
    budgetSoftLimit: runTokenNumber(item, ["budget_soft_limit", "budgetSoftLimit"]),
    budgetHardLimit: runTokenNumber(item, ["budget_hard_limit", "budgetHardLimit"]),
    budgetRemaining: runTokenNumber(item, ["budget_remaining", "budgetRemaining"])
  });
  const budgetTitle = budgetReason === "budget_model_route_applied"
    ? t("chat.timelineBudgetRoute")
    : budgetReason === "budget_branch_limit_applied"
      ? t("chat.timelineBudgetBranchLimit")
      : budgetReason === "budget_soft_limit_hit"
        ? t("chat.timelineBudgetWarning")
        : "";
  const action = String(item.action || "").toLowerCase();
  const attempt = Number(item.attempt || 0);
  const current = Boolean(item.current);
  const currentStep = item.stage || item.next_stage || item.nextStage || title;
  const tone = item.is_error
    ? "error"
    : (budgetReason === "budget_soft_limit_hit" && kind === "status") || String(item.severity || "").toLowerCase() === "warning"
      ? "warn"
      : item.needs_action || item.suspended || kind.includes("approval") || ["approval", "input", "budget"].includes(action)
      ? "approval"
      : kind.includes("tool")
        ? "tool"
        : kind.includes("complete")
          ? "success"
          : "stage";
  return {
    tone,
    title: budgetTitle || localizedText(title),
    detail: timelineDetail([
      summary,
      item.status && item.status !== summary ? taskMachineDisplayName(item.status) : "",
      item.reason && item.reason !== summary ? item.reason : "",
      item.contract_check || item.contractCheck,
      budgetLimit,
      /^sha256:[a-f\d]+$/i.test(String(item.source_ref || item.sourceRef || "")) ? "" : item.source_ref || item.sourceRef,
      attempt > 1 ? t("chat.timelineAttempt", { attempt }) : "",
      current ? t("chat.timelineCurrentStep", { step: localizedText(currentStep) }) : ""
    ]),
    stage: String(item.task_stage || item.stage || item.next_stage || "").trim(),
    agent: String(item.agent_id || item.agentID || "").trim(),
    kindLabel: kind.includes("tool") ? t("chat.toolCalling") : "",
    groupKind: item.group_kind || item.groupKind || "",
    groupID: item.group_id || item.groupID || "",
    parentID: item.parent_id || item.parentID || "",
    current
  };
}

function taskToolTimelineInfo(item = {}, phase = "result") {
  const toolName = String(item.tool_name || item.toolName || "").trim();
  if (!toolName) return null;
  const tool = toolName.toLowerCase().split("/").pop();
  const displayName = taskToolDisplayName(toolName);
  if (phase === "call") {
    return {
      tone: "tool",
      title: t("chat.timelineToolRunning", { tool: displayName }),
      detail: taskToolCallDetail(item),
      stage: String(item.task_stage || item.stage || "").trim(),
      agent: String(item.agent_id || item.agentID || "").trim(),
      kindLabel: t("chat.timelineInProgress"),
      current: true
    };
  }
  const payload = item.tool_result && typeof item.tool_result === "object" ? item.tool_result
    : parseTimelineToolPayload(item.content) || parseTimelineToolPayload(item.summary) || parseTimelineToolPayload(item.detail);
  const processTool = ["start_process", "process_status", "stop_process"].includes(tool);
  const expectedProcessState = processTool && (payload?.running === true || payload?.stop_requested === true);
  const failed = Boolean(item.is_error || payload?.is_error || payload?.timed_out || payload?.cancelled || !expectedProcessState && payload?.exit_code != null && Number.isFinite(Number(payload.exit_code)) && Number(payload.exit_code) !== 0);
  return {
    tone: failed ? "error" : "success",
    title: t(failed ? "chat.timelineToolFailed" : "chat.timelineToolCompleted", { tool: displayName }),
    detail: taskToolResultDetail(tool, payload, item),
    usage: extractTimelineTokenUsage(item),
    stage: String(item.task_stage || item.stage || "").trim(),
    agent: String(item.agent_id || item.agentID || "").trim(),
    kindLabel: taskToolCategoryLabel(tool),
    current: false,
    preview: processTool ? timelineProcessPreview(payload) : null
  };
}

function parseTimelineToolPayload(value) {
  const raw = String(value || "").trim();
  if (!raw.startsWith("{") || !raw.endsWith("}")) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function taskToolCallDetail(item = {}) {
  const summary = String(item.arguments_summary || item.argumentsSummary || item.summary || "").trim();
  if (!summary || ["read", "write", "exec", "inspect", "network"].includes(summary.toLowerCase())) return "";
  return compactTimelineText(chatDisplayText(summary), 180);
}

function taskToolResultDetail(tool, payload, item = {}) {
  const raw = String(item.summary || item.content || item.detail || "").trim();
  if (!payload) return compactTimelineText(chatDisplayText(raw), 220);
  const path = timelineWorkspacePath(payload.relative_path || payload.path || payload.cwd || "");
  const count = Array.isArray(payload.entries) ? payload.entries.length : Array.isArray(payload.matches) ? payload.matches.length : 0;
  const exitCode = Number(payload.exit_code);
  const duration = Number(payload.duration_ms);
  const parts = [];
  if (["read_file", "read_file_range", "file_info", "python_ast_summary"].includes(tool)) {
    if (path) parts.push(path);
    if (Number(payload.start_line) > 0 || Number(payload.end_line) > 0) parts.push(t("chat.timelineLineRange", { start: payload.start_line || 1, end: payload.end_line || payload.start_line || 1 }));
    if (Number(payload.size) > 0) parts.push(formatTimelineBytes(payload.size));
  } else if (["list_dir", "list_tree", "search_files"].includes(tool)) {
    if (path) parts.push(path);
    parts.push(t("chat.timelineItemsFound", { count }));
    if (payload.truncated) parts.push(t("chat.timelineResultTruncated"));
  } else if (["write_file", "apply_patch", "replace_text", "delete_file", "move_path", "mkdir"].includes(tool)) {
    if (tool === "move_path") parts.push(`${timelineWorkspacePath(payload.source_relative || payload.source)} → ${timelineWorkspacePath(payload.destination_relative || payload.destination)}`);
    else if (path) parts.push(path);
    if (payload.status) parts.push(taskMachineDisplayName(payload.status));
    if (payload.added_lines != null && payload.deleted_lines != null) parts.push(t("chat.timelineLineChanges", { added: payload.added_lines, deleted: payload.deleted_lines }));
    else if (tool === "move_path" && !payload.overwrote && !payload.is_dir) parts.push(t("chat.timelineLineChanges", { added: 0, deleted: 0 }));
  } else if (tool === "run_command") {
    const command = [payload.program, ...(Array.isArray(payload.args) ? payload.args : [])].filter(Boolean).join(" ");
    if (command) parts.push(compactTimelineText(command, 150));
    if (Number.isFinite(exitCode)) parts.push(t("chat.timelineExitCode", { code: exitCode }));
    if (Number.isFinite(duration) && duration >= 0) parts.push(formatTimelineDuration(duration));
    const signal = compactCommandTimelineText(exitCode === 0 ? payload.stdout : payload.stderr || payload.stdout);
    if (signal) parts.push(signal);
    if (payload.recommendation) parts.push(chatDisplayText(payload.recommendation));
  } else if (["start_process", "process_status", "stop_process"].includes(tool)) {
    if (payload.process_id) parts.push(t("chat.timelineProcessID", { id: String(payload.process_id).slice(0, 12) }));
    if (Number(payload.pid) > 0) parts.push(`PID ${payload.pid}`);
    parts.push(payload.running ? t("chat.timelineProcessRunning") : t("chat.timelineProcessStopped"));
    const address = `${payload.stdout || ""} ${payload.stderr || ""}`.match(/https?:\/\/[^\s]+/i)?.[0];
    if (address) parts.push(address.replace(/[.,;]+$/, ""));
  } else {
    if (path) parts.push(path);
    if (count) parts.push(t("chat.timelineItemsFound", { count }));
    if (payload.status) parts.push(taskMachineDisplayName(payload.status));
  }
  if (!parts.length && raw) parts.push(compactTimelineText(chatDisplayText(raw), 220));
  return timelineDetail(parts);
}

function taskToolCategoryLabel(tool) {
  if (["read_file", "read_file_range", "file_info", "list_dir", "list_tree", "search_files", "python_ast_summary"].includes(tool)) return t("chat.timelineCategoryRead");
  if (["write_file", "apply_patch", "replace_text", "delete_file", "move_path", "mkdir"].includes(tool)) return t("chat.timelineCategoryChange");
  if (["run_command", "start_process", "process_status", "stop_process"].includes(tool)) return t("chat.timelineCategoryRun");
  return t("chat.timelineCategoryTool");
}

function timelineWorkspacePath(value) {
  let path = String(value || "").trim();
  if (!path) return "";
  path = path.replaceAll("\\", "/");
  const staged = path.toLowerCase().lastIndexOf("/workspace/");
  if (staged >= 0) path = path.slice(staged + "/workspace/".length);
  const drive = path.match(/^[a-z]:\/.*?\/(web|src|internal|cmd|pkg|docs|scripts|configs|skills|workflows)\//i);
  if (drive) path = path.slice(path.toLowerCase().indexOf(`/${drive[1].toLowerCase()}/`) + 1);
  return path;
}

function safePreviewURL(value) {
  try {
    const url = new URL(String(value || ""));
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return "";
    if (["0.0.0.0", "[::]"].includes(url.hostname)) url.hostname = "127.0.0.1";
    return url.href;
  } catch { return ""; }
}

function timelineProcessPreview(payload) {
  if (!payload) return null;
  const address = `${payload.stdout || ""}\n${payload.stderr || ""}`.match(/https?:\/\/[^\s\u001b]+/i)?.[0];
  const url = safePreviewURL(payload.preview_url || address?.replace(/[.,;]+$/, ""));
  return url ? { url, running: payload.running === true && !payload.stop_requested } : null;
}

function runPreviewLinksHTML(previews = []) {
  const items = previews.map(item => ({ ...item, url: safePreviewURL(item.url) })).filter(item => item.url);
  if (!items.length) return "";
  return `<div class="run-preview-links" aria-label="${escapeHTML(t("chat.previewAddress"))}">${items.map(item => `<div class="run-preview-link ${item.running ? "running" : "stopped"}"><strong>${escapeHTML(t("chat.previewAddress"))}</strong>${item.running ? `<a href="${escapeHTML(item.url)}" target="_blank" rel="noopener noreferrer">${escapeHTML(item.url)} ↗</a>` : `<span>${escapeHTML(item.url)}</span>`}<small>${escapeHTML(t(item.running ? "chat.previewOpen" : "chat.previewUnavailable"))}</small></div>`).join("")}</div>`;
}

function formatTimelineBytes(value) {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 10 * 1024 ? 1 : 0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTimelineDuration(value) {
  const ms = Number(value || 0);
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`;
  const minutes = Math.floor(ms / 60000);
  const seconds = Math.round(ms % 60000 / 1000);
  return `${minutes} min ${seconds} s`;
}

function isWorkflowRunTerminal(status) {
  const value = String(status || "").toLowerCase();
  return ["completed", "complete", "done", "success", "succeeded", "finished", "failed", "error", "denied", "blocked", "cancelled", "canceled", "interrupted"].includes(value);
}

function isProviderRequestStatusEvent(event = {}) {
  if (String(event.type || "").toLowerCase() !== "status") return false;
  return ["provider_request_started", "provider_response_connected", "provider_request_retry"].includes(String(event.reason || "").toLowerCase());
}

function providerRequestTimelineDetail(event = {}, run = {}, text = "") {
  const detail = [text];
  if (providerRequestIsLive(event, run)) {
    const startedAt = Date.parse(event.at || event.created_at || event.createdAt || "");
    if (Number.isFinite(startedAt)) {
      detail.push(t("chat.providerWaitingElapsed", { duration: providerWaitingDuration(Date.now() - startedAt) }));
    }
  }
  return timelineDetail(detail);
}

function providerRequestIsLive(event = {}, run = {}) {
  if (!isWorkflowRunActive(run?.status)) return false;
  const events = Array.isArray(run?.timeline)
    ? run.timeline
    : Array.isArray(run?.events)
      ? run.events
      : [];
  const providerEvents = events.filter(isProviderRequestStatusEvent);
  if (!providerEvents.length) return Boolean(event.current);
  const latest = providerEvents[providerEvents.length - 1];
  const eventSeq = Number(event.seq || 0);
  const latestSeq = Number(latest.seq || 0);
  if (eventSeq && latestSeq) return eventSeq === latestSeq;
  return String(event.at || "") !== "" && String(event.at || "") === String(latest.at || "") && String(event.reason || "") === String(latest.reason || "");
}

function providerWaitingDuration(value) {
  const totalSeconds = Math.max(0, Math.floor(Number(value || 0) / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes > 0) return t("chat.providerWaitingMinutes", { minutes, seconds });
  return t("chat.providerWaitingSeconds", { seconds });
}

function isApprovalRequiredToolEvent(event) {
  const text = `${event?.content || ""} ${event?.detail || ""} ${event?.outcome || ""}`.toLowerCase();
  return text.includes("requires approval") || text.includes("approval required");
}

function isLowValueTimelineChunk(item = {}) {
  const kind = String(item.kind || item.type || "").trim().toLowerCase();
  const stage = String(item.task_stage || item.stage || item.title || "").trim();
  const content = String(item.content || item.summary || item.detail || "").trim();
  if (!content) return false;
  if (!stage && !kind.includes("stage")) return false;
  if (item.needs_action || item.suspended || item.is_error) return false;
  if (kind.includes("approval") || kind.includes("tool") || kind.includes("input")) return false;
  if (/workflow stage|workflow complete|workflow completed|waiting for|approval|required|failed|error|started|starting|running|completed/i.test(content)) return false;
  if (isInternalWorkflowPrompt(content)) return true;
  if (content.length <= 2) return true;
  if (content.length <= 24 && !/[=:]/.test(content)) return true;
  if (/^[\p{L}\p{N}_'"`.,;:!?(){}\[\]-]{1,32}$/u.test(content)) return true;
  return false;
}

function approvalDetailText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (/approval required/i.test(text)) return "";
  if (/requires approval before execution/i.test(text)) return "";
  if (/waiting for model response/i.test(text)) return "";
  if (isInternalWorkflowPrompt(text)) return "";
  return stripInternalWorkflowPromptBlocks(text);
}

function isWorkflowWaitingState(event, run) {
  const text = `${event?.content || ""} ${event?.detail || ""}`.toLowerCase();
  const status = String(event?.workflow_status || event?.status || run?.status || "").toLowerCase();
  return String(event?.type || "").toLowerCase() === "workflow_state" &&
    (text.includes("waiting for approval") ||
      text.includes("waiting for input") ||
      status === "awaiting_input" ||
      status === "awaiting_sub_workflow" ||
      isApprovalStatus(status));
}

function timelineDetail(parts) {
  return parts
    .map(value => publicTimelineText(value))
    .filter(Boolean)
    .map(value => compactTimelineText(value))
    .join(" / ");
}

function skillScriptEventContext(event, run) {
  if (!isSkillScriptToolName(event?.tool_name || event?.tool)) return null;
  return extractSkillScriptContextFromRun({ ...(run || {}), events: [event] });
}

function skillScriptTimelineDetail(info) {
  if (!info) return "";
  const label = [taskResourceDisplayName("skill", info.skill, info.skill), info.script].filter(Boolean).join(" / ") || taskToolDisplayName(info.toolName) || t("chat.skillScriptUnknown");
  return timelineDetail([
    label,
    info.path,
    info.runtime ? `${t("chat.skillScriptRuntime")}: ${info.runtime}` : "",
    info.outputKind ? `${t("chat.skillScriptOutput")}: ${info.outputKind}` : ""
  ]);
}

function publicTimelineText(value) {
  const text = stripInternalWorkflowPromptBlocks(value);
  if (!text || isInternalWorkflowPrompt(text)) return "";
  return localizedText(text);
}

function isInternalWorkflowPrompt(value) {
  const normalized = String(value || "").replace(/\s+/g, " ").trim().toLowerCase();
  if (!normalized) return false;
  return normalized.startsWith("run workflow ") ||
    normalized.includes("workflow description:") && normalized.includes("candidate next stages:") ||
    normalized.includes("use the configured stage skill instructions");
}

function stripInternalWorkflowPromptBlocks(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const blockPattern = /Run workflow\s+"[^"]*"\s+stage\s+"[^"]*"[\s\S]*?(?:Use the configured stage skill instructions\.\s*Produce a concise stage result\.|(?=\n\s*Run workflow\s+"[^"]*"\s+stage\s+"[^"]*")|$)/gi;
  const stripped = text
    .replace(blockPattern, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
  return stripped;
}

function compactTimelineText(value, maxLength = 160) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (!text) return "";
  return text.length > maxLength ? `${text.slice(0, maxLength)}...` : text;
}

function workflowRunTimelineLabel(status) {
  const value = String(status || "").toLowerCase();
  if (value === "blocked") return t("chat.timelineBlocked");
  if (value === "completed" || value === "success") return t("chat.timelineDone");
  if (["failed", "error", "action_failed", "denied", "blocked", "cancelled", "canceled", "interrupted"].includes(value)) return t("chat.timelineError");
	if (value === "paused_need_more_budget") return t("chat.timelinePausedContinue");
  if (value === "awaiting_input") return t("chat.timelineInput");
  if (value === "awaiting_sub_workflow") return t("chat.timelineSubWorkflow");
  if (isApprovalStatus(value)) return t("chat.timelineApproval");
  if (isWorkflowRunActive(value)) return t("chat.timelineRunning");
  return value || t("chat.timelineIdle");
}

function workflowRunResultLabel(status, hasOutput) {
  const value = String(status || "").toLowerCase();
  if (value === "blocked") return t("chat.resultQualityBlocked");
  if (["failed", "error", "action_failed", "denied", "blocked", "cancelled", "canceled"].includes(value)) return t("chat.resultError");
	if (value === "paused_need_more_budget") return t("chat.resultPausedContinue");
  if (isWorkflowRunActive(value)) return t("chat.resultStreaming");
  if (value === "awaiting_sub_workflow") return t("chat.resultBlocked");
  if (hasOutput) return t("chat.resultReady");
  return t("chat.resultIdle");
}

export function workflowRunResultText(run) {
  const explicitOutput = workflowRunFirstPublicOutput([run.final_output, run.output]);
  if (explicitOutput) return explicitOutput;
  const stages = Array.isArray(run.completed_stages) ? run.completed_stages : [];
  const finalStage = workflowRunPreferredFinalStage(stages);
  const finalStageText = workflowRunStagePublicOutput(finalStage);
  if (finalStageText) return finalStageText;
  if (isWorkflowRunTerminal(run.status)) {
    const summary = workflowRunFirstPublicOutput([run.summary]);
    if (summary) return summary;
  }
  const artifacts = Array.isArray(run.artifacts) ? run.artifacts : [];
  const finalArtifact = workflowRunPreferredFinalArtifact(artifacts);
  const finalArtifactText = workflowRunArtifactPublicOutput(finalArtifact);
  if (finalArtifactText) return finalArtifactText;
  for (const stage of [...stages].reverse()) {
    const text = workflowRunStagePublicOutput(stage);
    if (text) return text;
  }
  for (const artifact of [...artifacts].reverse()) {
    const text = workflowRunArtifactPublicOutput(artifact);
    if (text) return text;
  }
  return publicResultText(run.summary || "");
}

function workflowRunPreferredFinalStage(stages = []) {
  return [...(Array.isArray(stages) ? stages : [])].reverse().find(stage => workflowRunNameLooksFinal(stage?.stage || stage?.name || ""));
}

function workflowRunPreferredFinalArtifact(artifacts = []) {
  return [...(Array.isArray(artifacts) ? artifacts : [])].reverse().find(item => {
    const label = [item?.stage, item?.kind, item?.title, item?.id].filter(Boolean).join(" ");
    return workflowRunNameLooksFinal(label);
  });
}

function workflowRunStagePublicOutput(stage = {}) {
  if (!stage) return "";
  const outputs = stage.output_values || stage.outputs || stage.result?.outputs || {};
  const candidates = [
    outputs.final_report,
    outputs.final_summary,
    stage.result?.output,
    stage.summary,
    outputs.output,
    outputs.report,
    outputs.summary
  ];
  return workflowRunFirstPublicOutput(candidates);
}

function workflowRunArtifactPublicOutput(artifact = {}) {
  if (!artifact) return "";
  return workflowRunFirstPublicOutput([artifact.content]);
}

function workflowRunFirstPublicOutput(values = []) {
  for (const value of values) {
    const text = publicResultText(formatStructuredValue(value, true));
    if (text && !workflowRunLooksLikeInternalArtifact(text)) return text;
  }
  return "";
}

function workflowRunNameLooksFinal(value = "") {
  const text = String(value || "").toLowerCase();
  return /\b(final|report|handoff|delivery|summary|validation)\b/.test(text) ||
    text.includes("final-") ||
    text.includes("-final") ||
    text.includes("最终") ||
    text.includes("交付") ||
    text.includes("总结") ||
    text.includes("consolidate");
}

function workflowRunLooksLikeInternalArtifact(value = "") {
  const text = String(value || "").trim();
  if (!text) return true;
  if (/^Implementation summary, files changed, verification attempted, deviations, remaining work, and risk notes\.?$/i.test(text)) return true;
  if (isInternalWorkflowPrompt(text)) return true;
  if (text.length > 80 && /^[\[{]/.test(text)) {
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === "object") {
        const keys = Object.keys(parsed);
        if (keys.some(key => ["path", "size", "content", "compilerOptions", "artifact_ref"].includes(key))) return true;
      }
    } catch {
      // Keep non-JSON reports.
    }
  }
  return false;
}

function historyRunResultText(run) {
  if (run?.run_type === "agent") {
    return publicResultText(formatStructuredValue(run.output || run.result?.output || run.result?.final_message || run.summary || ""));
  }
  return workflowRunResultText(run);
}

function publicResultText(value) {
  const text = stripInternalWorkflowPromptBlocks(value);
  return text && !isInternalWorkflowPrompt(text) ? text : "";
}

function localizedRunMarkdownText(value) {
  const raw = String(value || "");
  const direct = localizedText(raw);
  if (direct !== raw || currentLanguage() !== "zh") return direct;
  return raw
    .replace(/The model response timed out after tool execution\. This stage paused safely without replaying tools\. (\d+) tool results were preserved \(([^)]*)\); use Continue task to finish the remaining implementation and verification in the saved task workspace\./gi, (_, count, tools) => {
      const names = String(tools || "").split(",").map(name => name.trim()).filter(Boolean).join("、");
      return `模型响应在工具执行后超时，本阶段已安全暂停，没有重放工具调用。已保留 ${count} 条工具结果（${names}）；可点击“继续执行”从已保存的任务暂存区完成剩余实现和验证。`;
    })
    .replace(/\n\s*Verification pass:\s*/gi, "\n\n## 最终质量审查\n\n")
    .replace(/^(\s*)Status:\s*fail\s*$/gmi, "$1**结论：未通过**")
    .replace(/^(\s*)Status:\s*pass\s*$/gmi, "$1**结论：通过**")
    .replace(/^(\s*)Status:\s*warn(?:ing)?\s*$/gmi, "$1**结论：需要关注**")
    .replace(/^(\s*)(Risks|Next):\s*(?:none|n\/a)\s*$/gmi, (_, prefix, label) => `${prefix}${label.toLowerCase() === "risks" ? "风险" : "下一步"}：无`)
    .replace(/^(\s*)Checked:\s*/gmi, "$1已检查：")
    .replace(/^(\s*)Risks:\s*/gmi, "$1风险：")
    .replace(/^(\s*)Next:\s*/gmi, "$1下一步：")
    .replace(/^(\s{0,3}#{1,6}\s*)([A-Za-z][A-Za-z\s/-]{2,64})(\s*)$/gm, (match, prefix, label, suffix) => {
      const translated = localizedText(label.trim());
      return translated && translated !== label.trim() ? `${prefix}${translated}${suffix}` : match;
    })
    .replace(/^(\s*)(Original request|Workflow description|Visual node type|Stage parameters|Completed prior stages|Candidate next stages|Stage output|Final output|Run Time|Start Time|Trace ID|Output|Summary)\s*:\s*/gmi, (match, prefix, label) => {
      const translated = localizedText(label.trim());
      return translated && translated !== label.trim() ? `${prefix}${translated}: ` : match;
    });
}

function renderRunTokenUsage(state, run) {
  if (!state?.resultOutput) return;
  state.resultOutput.querySelector("[data-result-token-usage]")?.remove();
  const tokenHTML = runTokenUsageHTML(run);
  if (!tokenHTML) return;
  if (!state.resultOutput.querySelector(".result-section-label")) {
    state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  }
  const wrapper = document.createElement("div");
  wrapper.dataset.resultTokenUsage = "true";
  wrapper.innerHTML = tokenHTML;
  state.resultOutput.appendChild(wrapper);
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function runTokenUsageHTML(run, options = {}) {
  const summary = runTokenUsageSummary(run);
  if (!summary) return "";
  const coreFacts = [
    summary.prompt ? [t("chat.tokenInput"), tokenCountText(summary.prompt)] : null,
    summary.output ? [t("chat.tokenOutput"), tokenCountText(summary.output)] : null,
    summary.total ? [t("chat.tokenRunTotal"), tokenCountText(summary.total, { allowZero: true })] : null,
    summary.cached ? [t("chat.tokenCached"), tokenCountText(summary.cached)] : null,
    summary.cached ? [t("chat.tokenCacheHit"), tokenCacheHitRateText(summary)] : null,
    summary.samples ? [t("chat.tokenSamples"), String(summary.samples)] : null
  ];
  const diagnosticFacts = [
    summary.estimatedPrompt ? [t("chat.tokenEstimatedPrompt"), tokenCountText(summary.estimatedPrompt)] : null,
    summary.savedTokens ? [t("workflow.runtimeField.budgetSavedTokens"), tokenCountText(summary.savedTokens)] : null,
    summary.grossPrompt ? [t("workflow.runtimeField.budgetGrossPromptTokens"), tokenCountText(summary.grossPrompt)] : null,
    summary.netPrompt ? [t("workflow.runtimeField.budgetNetPromptTokens"), tokenCountText(summary.netPrompt)] : null,
    summary.budgetLimit ? [t("workflow.runtimeField.budgetLimit"), summary.budgetLimit] : null,
    summary.estimatedInputCost ? [t("workflow.runtimeField.budgetEstimatedInputCost"), costText(summary.estimatedInputCost, summary.costCurrency)] : null,
    summary.estimatedOutputCost ? [t("workflow.runtimeField.budgetEstimatedOutputCost"), costText(summary.estimatedOutputCost, summary.costCurrency)] : null,
    summary.estimatedTotalCost ? [t("workflow.runtimeField.budgetEstimatedTotalCost"), costText(summary.estimatedTotalCost, summary.costCurrency)] : null,
    summary.pricingSource ? [t("workflow.runtimeField.budgetPricingSource"), summary.pricingSource] : null,
    summary.prefix ? [t("chat.tokenPrefix"), shortRunID(summary.prefix)] : null
  ];
  const usageFacts = [...coreFacts, ...(options.compact ? [] : diagnosticFacts)].filter(Boolean);
  const total = summary.total || summary.prompt + summary.output;
  const badge = total ? tokenCountText(total) : summary.estimatedPrompt ? tokenCountText(summary.estimatedPrompt) : t("chat.tokenTitle");
  return `<section class="run-token-usage ${options.compact ? "compact" : ""}" data-run-token-usage>
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.tokenTitle"))}</strong>
        <span>${escapeHTML(t("chat.tokenHelp"))}</span>
      </div>
      <small>${escapeHTML(badge)}</small>
    </div>
    ${usageFacts.length ? `<div class="run-token-grid">${usageFacts.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
  </section>`;
}

function runTokenCallLedgerHTML(run) {
  const calls = runTokenCallLedger(run);
  if (!calls.length) return "";
  return `<div class="run-token-ledger">
    <strong>${escapeHTML(t("chat.tokenCallLedger"))}</strong>
    <ol>${calls.map(call => `<li><span>${escapeHTML(call.kind)}</span>${timelineTokenMetricsHTML(call.usage)}${call.detail ? `<small>${escapeHTML(call.detail)}</small>` : ""}</li>`).join("")}</ol>
  </div>`;
}

function runTokenCallLedger(run) {
  const events = Array.isArray(run?.events) ? run.events : Array.isArray(run?.token_usage_history) ? run.token_usage_history : [];
  const samples = Array.isArray(run?.token_usage_history) ? run.token_usage_history : [];
  const rows = [];
  const source = events.length ? events : samples;
  for (const event of source) {
    const type = String(event?.type || event?.output_kind || event?.outputKind || "").toLowerCase();
    const isUsage = type === "token_usage" || event?.prompt_tokens || event?.promptTokens || event?.output_kind || event?.outputKind;
    if (!isUsage && type && type !== "token_usage") continue;
    if (type && type !== "token_usage" && !event?.output_kind && !event?.outputKind && !(event?.prompt_tokens || event?.promptTokens)) continue;
    const prompt = runTokenNumber(event, ["prompt_tokens", "input_tokens", "promptTokens", "inputTokens"]);
    const output = runTokenNumber(event, ["output_tokens", "completion_tokens", "outputTokens", "completionTokens"]);
    const cached = runTokenNumber(event, ["cached_tokens", "cachedTokens", "budget_cached_tokens", "budgetCachedTokens"]);
    const kind = tokenOutputKindLabel(event.output_kind || event.outputKind || event.content || "");
    const agent = String(event.agent_id || event.agentID || "").trim();
    const mode = String(event.mode || "").trim();
    const tools = Array.isArray(event.tool_names) ? event.tool_names.join(", ") : String(event.tool_name || event.toolName || "").trim();
    if (!prompt && !output && !cached && !kind) continue;
    const usage = extractTimelineTokenUsage(event) || { prompt, input: prompt, output, cached, total: prompt + output, cacheHitRate: prompt > 0 ? cached / prompt : 0 };
    const detailParts = [
      agent && mode ? `${agent}/${mode}` : agent || mode,
      tools
    ].filter(Boolean);
    rows.push({ kind, detail: detailParts.join(" · "), usage });
  }
  return rows;
}

function tokenOutputKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  if (value === "text" || value === "tool_calls" || value === "mixed" || value === "empty") {
    return t(`chat.tokenKind.${value}`);
  }
  return t("chat.tokenKind.unknown");
}

export function runTokenUsageSummary(run) {
  const events = Array.isArray(run?.events) ? run.events : [];
  const retained = run?.token_usage && typeof run.token_usage === "object" ? run.token_usage : {};
  const retainedCalls = runTokenNumber(retained, ["model_calls", "modelCalls"]);
  const persisted = {
    prompt: runTokenNumber(run, ["budget_prompt_tokens", "prompt_tokens", "input_tokens", "PromptTokens", "promptTokens"]),
    output: runTokenNumber(run, ["budget_output_tokens", "output_tokens", "completion_tokens", "OutputTokens", "outputTokens"]),
    cached: runTokenNumber(run, ["budget_cached_tokens", "cached_tokens", "CachedTokens", "cachedTokens"]),
    total: runTokenNumber(run, ["budget_total_tokens", "total_tokens", "TotalTokens", "totalTokens"])
  };
  const summary = {
    reported: Boolean(run?.token_usage && typeof run.token_usage === "object"),
    prompt: retainedCalls ? runTokenNumber(retained, ["prompt_tokens", "promptTokens"]) : 0,
    output: retainedCalls ? runTokenNumber(retained, ["output_tokens", "outputTokens"]) : 0,
    cached: retainedCalls ? runTokenNumber(retained, ["cached_tokens", "cachedTokens"]) : 0,
    cacheHitRate: 0,
    total: retainedCalls ? runTokenNumber(retained, ["total_tokens", "totalTokens"]) : 0,
    estimatedPrompt: runTokenNumber(run, ["budget_estimated_prompt_tokens", "budgetEstimatedPromptTokens"]),
    netPrompt: runTokenNumber(run, ["budget_net_prompt_tokens", "budgetNetPromptTokens"]),
    grossPrompt: runTokenNumber(run, ["budget_gross_prompt_tokens", "budgetGrossPromptTokens"]),
    savedTokens: runTokenNumber(run, ["budget_saved_tokens", "budgetSavedTokens"]),
    memorySavedTokens: runTokenNumber(run, ["budget_memory_saved_tokens", "budgetMemorySavedTokens"]),
    historySavedTokens: runTokenNumber(run, ["budget_history_saved_tokens", "budgetHistorySavedTokens"]),
    artifactSavedTokens: runTokenNumber(run, ["budget_artifact_saved_tokens", "budgetArtifactSavedTokens"]),
    skillSavedTokens: runTokenNumber(run, ["budget_skill_saved_tokens", "budgetSkillSavedTokens"]),
    toolSchemaSavedTokens: runTokenNumber(run, ["budget_tool_schema_saved_tokens", "budgetToolSchemaSavedTokens"]),
    estimatedInputCost: runTokenNumber(run, ["budget_estimated_input_cost", "budgetEstimatedInputCost"]),
    estimatedOutputCost: runTokenNumber(run, ["budget_estimated_output_cost", "budgetEstimatedOutputCost"]),
    estimatedTotalCost: runTokenNumber(run, ["budget_estimated_total_cost", "budgetEstimatedTotalCost"]),
    costCurrency: String(run?.budget_cost_currency || run?.budgetCostCurrency || "").trim(),
    pricingSource: String(run?.budget_pricing_source || run?.budgetPricingSource || "").trim(),
    budgetMetric: String(run?.budget_metric || run?.budgetMetric || "").trim(),
    budgetUsed: runTokenNumber(run, ["budget_used", "budgetUsed"]),
    budgetSoftLimit: runTokenNumber(run, ["budget_soft_limit", "budgetSoftLimit"]),
    budgetHardLimit: runTokenNumber(run, ["budget_hard_limit", "budgetHardLimit"]),
    budgetRemaining: runTokenNumber(run, ["budget_remaining", "budgetRemaining"]),
    budgetLimit: "",
    samples: retainedCalls,
    prefix: ""
  };
  const eventBudgetTotals = runEmptyBudgetAttribution();
  const eventBudgetCumulative = runEmptyBudgetAttribution();
  let eventBudgetSamples = 0;
  for (const event of events) {
    const type = String(event?.type || "").toLowerCase();
    if (type === "token_usage" && !retainedCalls) {
      const prompt = runTokenNumber(event, ["prompt_tokens", "input_tokens", "promptTokens", "inputTokens"]);
      const output = runTokenNumber(event, ["output_tokens", "completion_tokens", "outputTokens", "completionTokens"]);
      const cached = runTokenNumber(event, ["cached_tokens", "cachedTokens", "budget_cached_tokens", "budgetCachedTokens"]);
      const total = runTokenNumber(event, ["total_tokens", "totalTokens"]);
      summary.prompt += prompt;
      summary.output += output;
      summary.cached += cached;
      summary.total += total || prompt + output;
      summary.samples += 1;
    }
    if (type === "prompt_budget" || event?.prompt_budget || event?.promptBudget) {
      const budget = event.prompt_budget || event.promptBudget || event;
      runBudgetAttributionAdd(eventBudgetTotals, runPromptBudgetAttribution(budget));
      eventBudgetSamples += 1;
      summary.prefix = summary.prefix || String(budget.prompt_prefix_hash || budget.promptPrefixHash || "").trim();
    }
    runBudgetAttributionMax(eventBudgetCumulative, {
      estimatedPrompt: runTokenNumber(event, ["budget_estimated_prompt_tokens", "budgetEstimatedPromptTokens"]),
      netPrompt: runTokenNumber(event, ["budget_net_prompt_tokens", "budgetNetPromptTokens"]),
      grossPrompt: runTokenNumber(event, ["budget_gross_prompt_tokens", "budgetGrossPromptTokens"]),
      savedTokens: runTokenNumber(event, ["budget_saved_tokens", "budgetSavedTokens"]),
      memorySavedTokens: runTokenNumber(event, ["budget_memory_saved_tokens", "budgetMemorySavedTokens"]),
      historySavedTokens: runTokenNumber(event, ["budget_history_saved_tokens", "budgetHistorySavedTokens"]),
      artifactSavedTokens: runTokenNumber(event, ["budget_artifact_saved_tokens", "budgetArtifactSavedTokens"]),
      skillSavedTokens: runTokenNumber(event, ["budget_skill_saved_tokens", "budgetSkillSavedTokens"]),
      toolSchemaSavedTokens: runTokenNumber(event, ["budget_tool_schema_saved_tokens", "budgetToolSchemaSavedTokens"]),
      estimatedInputCost: runTokenNumber(event, ["budget_estimated_input_cost", "budgetEstimatedInputCost"]),
      estimatedOutputCost: runTokenNumber(event, ["budget_estimated_output_cost", "budgetEstimatedOutputCost"]),
      estimatedTotalCost: runTokenNumber(event, ["budget_estimated_total_cost", "budgetEstimatedTotalCost"]),
      costCurrency: String(event?.budget_cost_currency || event?.budgetCostCurrency || "").trim(),
      pricingSource: String(event?.budget_pricing_source || event?.budgetPricingSource || "").trim()
    });
    if (event?.budget_metric || event?.budgetMetric || event?.budget_hard_limit || event?.budgetHardLimit || event?.budget_soft_limit || event?.budgetSoftLimit) {
      summary.budgetMetric = String(event.budget_metric || event.budgetMetric || summary.budgetMetric || "").trim();
      summary.budgetUsed = runTokenNumber(event, ["budget_used", "budgetUsed"]) || summary.budgetUsed;
      summary.budgetSoftLimit = runTokenNumber(event, ["budget_soft_limit", "budgetSoftLimit"]) || summary.budgetSoftLimit;
      summary.budgetHardLimit = runTokenNumber(event, ["budget_hard_limit", "budgetHardLimit"]) || summary.budgetHardLimit;
      summary.budgetRemaining = runTokenNumber(event, ["budget_remaining", "budgetRemaining"]) || summary.budgetRemaining;
    }
  }
  if (summary.samples === 0 && !summary.reported) {
    summary.prompt = persisted.prompt;
    summary.output = persisted.output;
    summary.cached = persisted.cached;
    summary.total = persisted.total || persisted.prompt + persisted.output;
  }
  const directBudget = run?.prompt_budget || run?.promptBudget;
  if (eventBudgetSamples > 0) {
    runBudgetAttributionMax(summary, eventBudgetTotals);
  }
  if (directBudget && typeof directBudget === "object") {
    runBudgetAttributionMax(summary, runPromptBudgetAttribution(directBudget));
  }
  runBudgetAttributionMax(summary, eventBudgetCumulative);
  if (directBudget && typeof directBudget === "object") {
    summary.prefix = summary.prefix || String(directBudget.prompt_prefix_hash || directBudget.promptPrefixHash || "").trim();
  }
  if (!summary.savedTokens) {
    summary.savedTokens = summary.memorySavedTokens + summary.historySavedTokens + summary.artifactSavedTokens + summary.skillSavedTokens + summary.toolSchemaSavedTokens;
  }
  if (!summary.estimatedPrompt && summary.netPrompt) summary.estimatedPrompt = summary.netPrompt;
  if (!summary.netPrompt) summary.netPrompt = summary.estimatedPrompt;
  if (!summary.grossPrompt && (summary.netPrompt || summary.savedTokens)) summary.grossPrompt = summary.netPrompt + summary.savedTokens;
  if (!summary.total && (summary.prompt || summary.output)) summary.total = summary.prompt + summary.output;
  if (!summary.estimatedTotalCost && (summary.estimatedInputCost || summary.estimatedOutputCost)) summary.estimatedTotalCost = summary.estimatedInputCost + summary.estimatedOutputCost;
  const input = Number(summary.prompt || summary.input || 0);
  summary.cacheHitRate = input > 0 ? Number(summary.cached || 0) / input : 0;
  summary.budgetLimit = runWorkflowBudgetLimitText(summary);
  return summary.reported || summary.prompt || summary.output || summary.cached || summary.total || summary.estimatedPrompt || summary.savedTokens || summary.grossPrompt || summary.budgetLimit || summary.estimatedTotalCost || summary.estimatedInputCost || summary.estimatedOutputCost || summary.samples ? summary : null;
}

function runEmptyBudgetAttribution() {
  return {
    estimatedPrompt: 0,
    netPrompt: 0,
    grossPrompt: 0,
    savedTokens: 0,
    memorySavedTokens: 0,
    historySavedTokens: 0,
    artifactSavedTokens: 0,
    skillSavedTokens: 0,
    toolSchemaSavedTokens: 0,
    estimatedInputCost: 0,
    estimatedOutputCost: 0,
    estimatedTotalCost: 0,
    costCurrency: "",
    pricingSource: ""
  };
}

function runPromptBudgetAttribution(budget = {}) {
  const attribution = runEmptyBudgetAttribution();
  attribution.estimatedPrompt = runTokenNumber(budget, ["estimated_prompt_tokens", "estimatedPromptTokens"]);
  attribution.netPrompt = attribution.estimatedPrompt;
  attribution.memorySavedTokens = runTokenNumber(budget, ["memory_estimated_saved_tokens", "memoryEstimatedSavedTokens"]);
  attribution.historySavedTokens = runTokenNumber(budget, ["history_estimated_saved_tokens", "historyEstimatedSavedTokens"]);
  attribution.artifactSavedTokens = runTokenNumber(budget, ["artifact_omitted_tokens", "artifactOmittedTokens"]);
  attribution.skillSavedTokens = runTokenNumber(budget, ["skill_omitted_tokens", "skillOmittedTokens"]);
  attribution.toolSchemaSavedTokens = runTokenNumber(budget, ["tool_schema_estimated_saved_tokens", "toolSchemaEstimatedSavedTokens"]);
  attribution.savedTokens = attribution.memorySavedTokens + attribution.historySavedTokens + attribution.artifactSavedTokens + attribution.skillSavedTokens + attribution.toolSchemaSavedTokens;
  attribution.grossPrompt = attribution.netPrompt + attribution.savedTokens;
  attribution.estimatedInputCost = runTokenNumber(budget, ["estimated_input_cost", "estimatedInputCost"]);
  attribution.estimatedOutputCost = runTokenNumber(budget, ["estimated_output_cost", "estimatedOutputCost"]);
  attribution.estimatedTotalCost = runTokenNumber(budget, ["estimated_total_cost", "estimatedTotalCost"]) || attribution.estimatedInputCost + attribution.estimatedOutputCost;
  attribution.costCurrency = String(budget.cost_currency || budget.costCurrency || "").trim();
  attribution.pricingSource = String(budget.pricing_source || budget.pricingSource || "").trim();
  return attribution;
}

function runBudgetAttributionAdd(target, attribution = {}) {
  if (!target || !attribution) return;
  target.estimatedPrompt += Number(attribution.estimatedPrompt || 0);
  target.netPrompt += Number(attribution.netPrompt || 0);
  target.grossPrompt += Number(attribution.grossPrompt || 0);
  target.savedTokens += Number(attribution.savedTokens || 0);
  target.memorySavedTokens += Number(attribution.memorySavedTokens || 0);
  target.historySavedTokens += Number(attribution.historySavedTokens || 0);
  target.artifactSavedTokens += Number(attribution.artifactSavedTokens || 0);
  target.skillSavedTokens += Number(attribution.skillSavedTokens || 0);
  target.toolSchemaSavedTokens += Number(attribution.toolSchemaSavedTokens || 0);
  target.estimatedInputCost += Number(attribution.estimatedInputCost || 0);
  target.estimatedOutputCost += Number(attribution.estimatedOutputCost || 0);
  target.estimatedTotalCost += Number(attribution.estimatedTotalCost || 0);
  if (!target.costCurrency && attribution.costCurrency) target.costCurrency = attribution.costCurrency;
  if (!target.pricingSource && attribution.pricingSource) target.pricingSource = attribution.pricingSource;
}

function runBudgetAttributionMax(target, attribution = {}) {
  if (!target || !attribution) return;
  for (const key of ["estimatedPrompt", "netPrompt", "grossPrompt", "savedTokens", "memorySavedTokens", "historySavedTokens", "artifactSavedTokens", "skillSavedTokens", "toolSchemaSavedTokens", "estimatedInputCost", "estimatedOutputCost", "estimatedTotalCost"]) {
    target[key] = Math.max(Number(target[key] || 0), Number(attribution[key] || 0));
  }
  if (!target.costCurrency && attribution.costCurrency) target.costCurrency = attribution.costCurrency;
  if (!target.pricingSource && attribution.pricingSource) target.pricingSource = attribution.pricingSource;
}

function runTokenNumber(source, keys = []) {
  if (!source || typeof source !== "object") return 0;
  for (const key of keys) {
    const value = Number(source[key]);
    if (Number.isFinite(value) && value > 0) return value;
  }
  return 0;
}

function runWorkflowBudgetLimitText(summary = {}) {
  const metric = String(summary.budgetMetric || "").trim();
  const soft = Number(summary.budgetSoftLimit || 0);
  const hard = Number(summary.budgetHardLimit || 0);
  if (!metric && !soft && !hard) return "";
  const limit = hard || soft;
  const metricKey = metric ? `workflow.budgetMetric.${metric}` : "";
  const metricLabel = metricKey && t(metricKey) !== metricKey ? t(metricKey) : localizedText(metric.replace(/_/g, " ") || t("workflow.runtimeField.budgetUsed"));
  const parts = [`${metricLabel}: ${Number(summary.budgetUsed || 0)}${limit ? ` / ${limit}` : ""}`];
  if (soft && hard && soft !== hard) parts.push(`${t("workflow.runtimeField.budgetSoftLimit")}: ${soft}`);
  if (hard) parts.push(`${t("workflow.runtimeField.budgetRemaining")}: ${Number(summary.budgetRemaining || 0)}`);
  return parts.join(" / ");
}

function tokenCountText(value, options = {}) {
  const number = Number(value || 0);
  return formatTokenText(number, t("chat.tokens"), { allowZero: Boolean(options.allowZero) });
}

function tokenCacheHitRateText(usage = {}) {
  let rate = Number(usage.cacheHitRate);
  if (!Number.isFinite(rate) || rate < 0) {
    const input = Number(usage.input || usage.prompt || 0);
    rate = input > 0 ? Number(usage.cached || 0) / input : 0;
  }
  if (rate > 1 && rate <= 100) rate /= 100;
  if (rate > 1) rate = 1;
  const percent = rate * 100;
  return `${percent.toFixed(2)}%`;
}

function costText(value, currency = "") {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number <= 0) return "-";
  const code = String(currency || "").trim().toUpperCase();
  const digits = number < 0.01 ? 6 : number < 1 ? 4 : 2;
  if (/^[A-Z]{3}$/.test(code)) {
    try {
      return new Intl.NumberFormat(undefined, {
        style: "currency",
        currency: code,
        minimumFractionDigits: Math.min(2, digits),
        maximumFractionDigits: digits
      }).format(number);
    } catch {
      // Fall through to a plain numeric display for custom currency labels.
    }
  }
  const text = number.toLocaleString(undefined, {
    minimumFractionDigits: number < 1 ? Math.min(2, digits) : 0,
    maximumFractionDigits: digits
  });
  return code ? `${text} ${code}` : text;
}

function renderRunToolRisk(state, run) {
  if (!state?.resultOutput) return;
  state.resultOutput.querySelector("[data-result-tool-risk]")?.remove();
  const scriptHTML = runSkillScriptContextHTML(run);
  const riskHTML = runToolRiskMiniHTML(run);
  if (!scriptHTML && !riskHTML) return;
  if (!state.resultOutput.querySelector(".result-section-label")) {
    state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  }
  const wrapper = document.createElement("div");
  wrapper.dataset.resultToolRisk = "true";
  wrapper.innerHTML = `${scriptHTML}${riskHTML}`;
  state.resultOutput.appendChild(wrapper);
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function renderRunSupplementalDetails(state, run) {
  if (!state?.resultOutput) return;
  state.resultOutput.querySelector("[data-result-supplemental]")?.remove();
  const items = runSupplementalDetailItems(run);
  if (!items.length) {
    updateResultEmptyState(state);
    return;
  }
  ensureResultOutputShell(state);
  const wrapper = document.createElement("div");
  wrapper.dataset.resultSupplemental = "true";
  wrapper.innerHTML = `<section class="run-contextual-prompts">
    <div class="run-contextual-prompts-head">
      <strong>${escapeHTML(t("chat.runContextualTitle"))}</strong>
      <span>${escapeHTML(t("chat.runContextualHelp"))}</span>
    </div>
    <div class="run-contextual-prompt-grid">
      ${items.map(runSupplementalDetailItemHTML).join("")}
    </div>
  </section>`;
  state.resultOutput.appendChild(wrapper);
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function renderRunExecutionSummary(state, run) {
  if (!state?.resultOutput) return;
  state.resultOutput.querySelector("[data-result-execution]")?.remove();
  const execution = normalizedRunExecutionSummary(run);
  if (!execution) {
    updateResultEmptyState(state);
    return;
  }
  ensureResultOutputShell(state);
  const wrapper = document.createElement("div");
  wrapper.dataset.resultExecution = "true";
  wrapper.innerHTML = runPreviewLinksHTML(run?.execution?.previews || []) + runExecutionSummaryHTML(execution);
  const firstContent = state.resultOutput.querySelector(":scope > :not(.result-section-label)");
  if (firstContent) state.resultOutput.insertBefore(wrapper, firstContent);
  else state.resultOutput.appendChild(wrapper);
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function normalizedRunExecutionSummary(run = {}) {
  const source = run?.execution && typeof run.execution === "object" ? run.execution : {};
  const slices = Array.isArray(source.change_slices)
    ? source.change_slices.slice(0, 12)
    : Array.isArray(run.change_slices) ? run.change_slices.slice(0, 12) : [];
  const current = source.current_slice && typeof source.current_slice === "object"
    ? source.current_slice
    : slices.find(slice => ["running", "in_progress", "active", "blocked", "awaiting_approval", "awaiting_input"].includes(String(slice?.status || "").toLowerCase()))
      || slices.find(slice => !["completed", "succeeded", "verified", "skipped"].includes(String(slice?.status || "").toLowerCase()))
      || slices.at(-1)
      || null;
  const contractSource = source.contract && typeof source.contract === "object" ? source.contract : {};
  const contract = {
    request: String(contractSource.request || run.request || "").trim(),
    workflow: String(contractSource.workflow || run.name || "").trim(),
    status: String(contractSource.status || run.status || "").trim(),
    current_stage: String(contractSource.current_stage || run.next_stage || "").trim(),
    target_id: String(contractSource.target_id || run.target_id || "").trim()
  };
  const blockers = Array.isArray(source.blockers) ? source.blockers.filter(Boolean).slice(0, 6) : [];
  if (!blockers.length && current?.blocked_reason) blockers.push(current.blocked_reason);
  const verification = Array.isArray(source.verification) ? source.verification.slice(0, 12) : [];
  const artifacts = Array.isArray(source.artifacts) ? source.artifacts.slice(0, 12) : [];
  const outcomeSource = source.outcome && typeof source.outcome === "object" ? source.outcome : {};
  const outcome = {
    conclusion: String(outcomeSource.conclusion || "").trim(),
    qualityScore: Number(outcomeSource.quality_score || 0),
    durationMS: Number(outcomeSource.duration_ms || 0),
    modelCalls: Number(outcomeSource.model_calls || 0),
    toolCalls: Number(outcomeSource.tool_calls || 0),
    changeCount: Number(outcomeSource.change_count || 0),
    artifactCount: Number(outcomeSource.artifact_count || 0),
    changedFiles: Array.isArray(outcomeSource.changed_files) ? outcomeSource.changed_files.filter(Boolean) : [],
    unverified: Array.isArray(outcomeSource.unverified) ? outcomeSource.unverified.filter(Boolean) : [],
    remainingRisks: Array.isArray(outcomeSource.remaining_risks) ? outcomeSource.remaining_risks.filter(Boolean) : []
  };
  if (!slices.length && !blockers.length && !verification.length && !artifacts.length && !contract.request && !contract.workflow) return null;
  return {
    contract,
    outcome,
    current,
    slices,
    blockers,
    verification,
    artifacts,
    omittedSlices: Number(source.omitted_slices || 0),
    omittedChecks: Number(source.omitted_verification || 0),
    omittedArtifacts: Number(source.omitted_artifacts || 0)
  };
}

function runExecutionSummaryHTML(execution) {
  const current = execution.current || {};
  const outcome = execution.outcome || {};
  const conclusion = execution.contract.status === "paused_need_more_budget" ? execution.contract.status : outcome.conclusion;
  const currentTitle = runExecutionOutcomeTitle(conclusion) || current.title || execution.contract.current_stage || execution.contract.workflow || t("chat.executionSummaryNoSlice");
  const currentStatus = conclusion || current.status || execution.contract.status || "";
  const blocker = execution.blockers[0] || "";
  const facts = [
    outcome.changeCount ? t("chat.executionSummaryChangedCount", { count: outcome.changeCount }) : "",
    outcome.toolCalls ? t("chat.executionSummaryToolCalls", { count: outcome.toolCalls }) : "",
    outcome.modelCalls ? t("chat.executionSummaryModelCalls", { count: outcome.modelCalls }) : "",
    outcome.durationMS ? formatTimelineDuration(outcome.durationMS) : "",
    execution.slices.length ? t("chat.executionSummarySliceCount", { count: execution.slices.length + execution.omittedSlices }) : "",
    execution.verification.length ? t("chat.executionSummaryVerificationCount", { count: execution.verification.length + execution.omittedChecks }) : "",
    execution.artifacts.length ? t("chat.executionSummaryArtifactCount", { count: execution.artifacts.length + execution.omittedArtifacts }) : ""
  ].filter(Boolean).join(" · ");
  const contractRows = [
    [t("chat.executionSummaryGoal"), execution.contract.request],
    [t("chat.executionSummaryWorkflow"), execution.contract.workflow],
    [t("chat.executionSummaryStage"), execution.contract.current_stage],
    [t("chat.executionSummaryTarget"), execution.contract.target_id]
  ].filter(([, value]) => value);
  const blockers = execution.blockers.length
    ? `<section><h4>${escapeHTML(t("chat.executionSummaryBlockers"))}</h4><ul class="run-execution-blockers">${execution.blockers.map(value => `<li>${escapeHTML(chatDisplayText(value))}</li>`).join("")}</ul></section>`
    : "";
  const slices = execution.slices.length
    ? `<section><h4>${escapeHTML(t("chat.executionSummarySlices"))}</h4><div class="run-execution-slices">${execution.slices.map(runExecutionSliceHTML).join("")}</div></section>`
    : "";
  const verification = execution.verification.length
    ? `<section><h4>${escapeHTML(t("chat.executionSummaryVerification"))}</h4><ul class="run-execution-checks">${execution.verification.map(runExecutionVerificationHTML).join("")}</ul></section>`
    : "";
  const artifacts = execution.artifacts.length
    ? `<section><h4>${escapeHTML(t("chat.executionSummaryArtifacts"))}</h4><ul class="run-execution-artifacts">${execution.artifacts.map(runExecutionArtifactHTML).join("")}</ul></section>`
    : "";
  const outcomeIssues = [...outcome.unverified, ...outcome.remainingRisks];
  const outcomeHTML = `<section class="run-execution-outcome ${escapeHTML(runExecutionStatusTone(outcome.conclusion))}">
    <div class="run-execution-outcome-head"><strong>${escapeHTML(runExecutionOutcomeTitle(conclusion) || t("chat.executionSummaryTitle"))}</strong>${outcome.conclusion ? `<span>${escapeHTML(t("chat.executionSummaryQuality", { score: outcome.qualityScore }))}</span>` : ""}</div>
    ${outcome.changedFiles.length ? `<p>${escapeHTML(t("chat.executionSummaryChangedFiles", { files: outcome.changedFiles.join(", ") }))}</p>` : ""}
    ${outcomeIssues.length ? `<ul>${outcomeIssues.map(value => `<li>${escapeHTML(chatDisplayText(value))}</li>`).join("")}</ul>` : `<p>${escapeHTML(t("chat.executionSummaryNoOutstandingIssues"))}</p>`}
  </section>`;
  const open = runExecutionStatusTone(outcome.conclusion) === "bad" || execution.blockers.length > 0;
  return `<details class="run-execution-summary"${open ? " open" : ""}>
    <summary>
      <span class="run-execution-heading"><strong>${escapeHTML(t("chat.executionSummaryTitle"))}</strong><small>${escapeHTML(chatDisplayText(currentTitle))}</small></span>
      ${currentStatus ? `<span class="run-execution-status ${escapeHTML(runExecutionStatusTone(currentStatus))}">${escapeHTML(runExecutionOutcomeTitle(currentStatus) || workflowRunStatusLabel(currentStatus))}</span>` : ""}
      <span class="run-execution-facts">${escapeHTML(chatDisplayText(blocker || facts || t("chat.executionSummaryEmpty")))}</span>
    </summary>
    <div class="run-execution-body">
      ${outcomeHTML}
      ${contractRows.length ? `<section><h4>${escapeHTML(t("chat.executionSummaryContract"))}</h4><dl>${contractRows.map(([label, value]) => `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(chatDisplayText(value))}</dd></div>`).join("")}</dl></section>` : ""}
      ${blockers}${slices}${verification}${artifacts}
    </div>
  </details>`;
}

function runExecutionOutcomeTitle(conclusion) {
  const value = String(conclusion || "").toLowerCase();
  if (["partial", "unverified"].includes(value)) return t(`chat.runDeliveryConclusion.${value}`);
  if (value === "paused_need_more_budget") return workflowRunStatusLabel(value);
  if (value === "success") return t("chat.executionOutcomePassed");
  if (value === "failed") return t("chat.executionOutcomeFailed");
  if (value === "running") return t("chat.executionOutcomeRunning");
  if (value === "waiting_input") return t("chat.executionOutcomeWaiting");
  return "";
}

function runExecutionSliceHTML(slice = {}) {
  const targets = Array.isArray(slice.target_files) ? slice.target_files.filter(Boolean) : [];
  const checks = Array.isArray(slice.verification_commands) ? slice.verification_commands.filter(Boolean) : [];
  const detail = [
    targets.length ? t("chat.executionSummaryTargets", { value: targets.join(", ") }) : "",
    Array.isArray(slice.changed_files) && slice.changed_files.length ? `${t("chat.deliveryChangedFiles")}: ${slice.changed_files.filter(Boolean).join(", ")}` : "",
    checks.length ? t("chat.executionSummaryCommands", { value: checks.join(" · ") }) : "",
    slice.blocked_reason ? chatDisplayText(slice.blocked_reason) : ""
  ].filter(Boolean).join(" · ");
  return `<div class="run-execution-slice${slice.blocked_reason ? " blocked" : ""}">
    <span><strong>${escapeHTML(chatDisplayText(slice.title || slice.id || t("chat.executionSummaryNoSlice")))}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</span>
    <em class="${escapeHTML(runExecutionStatusTone(slice.status))}">${escapeHTML(workflowRunStatusLabel(slice.status))}</em>
  </div>`;
}

function runExecutionVerificationHTML(check = {}) {
  const kind = String(check.kind || "").toLowerCase();
  const title = kind === "verifier"
    ? t("chat.executionSummaryVerifier")
    : kind === "tool"
      ? taskToolDisplayName(check.name || check.kind)
      : check.name || check.kind || t("chat.executionSummaryVerification");
  const detail = [check.stage, check.detail].filter(Boolean).map(chatDisplayText).join(" · ");
  return `<li><span><strong>${escapeHTML(chatDisplayText(title))}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</span><em class="${escapeHTML(runExecutionStatusTone(check.status))}">${escapeHTML(workflowRunStatusLabel(check.status))}</em></li>`;
}

function runExecutionArtifactHTML(artifact = {}) {
  const title = artifact.title || artifact.id || artifact.artifact_ref || t("chat.executionSummaryArtifact");
  const detail = [artifact.kind, artifact.mime, artifact.summary].filter(Boolean).map(chatDisplayText).join(" · ");
  return `<li><strong>${escapeHTML(chatDisplayText(title))}</strong>${detail ? `<small>${escapeHTML(detail)}</small>` : ""}</li>`;
}

function runExecutionStatusTone(status) {
  const value = String(status || "").toLowerCase();
  if (["completed", "complete", "passed", "verified", "success", "succeeded"].includes(value)) return "good";
  if (["failed", "error", "blocked", "denied", "cancelled", "canceled"].includes(value)) return "bad";
  if (["running", "in_progress", "active"].includes(value)) return "active";
  return "neutral";
}

function ensureResultOutputShell(state) {
  if (!state?.resultOutput) return;
  if (!state.resultOutput.querySelector(".result-section-label")) {
    state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  }
}

function runSupplementalDetailItems(run) {
  const items = [];
  const tokenSummary = runTokenUsageSummary(run);
  if (tokenSummary) {
    const actual = tokenSummary.total || tokenSummary.prompt + tokenSummary.output || 0;
    items.push({
      kind: "cost",
      title: t("chat.runContextualCostTitle"),
      body: actual ? t("chat.runContextualCostBody", { tokens: tokenCountText(actual) }) : t("chat.tokenHelp"),
      action: t("chat.runContextualOpenContext"),
      detail: runTokenUsageHTML(run, { compact: true }),
      open: false
    });
  }
  const toolRisk = `${runSkillScriptContextHTML(run, { compact: true })}${runToolRiskMiniHTML(run, { compact: true })}`;
  if (toolRisk) {
    items.push({
      kind: "tool",
      title: t("chat.runContextualToolTitle"),
      body: t("chat.runContextualToolBody"),
      action: t("chat.runContextualOpenTool"),
      detail: toolRisk
    });
  }
  const diffCount = Array.isArray(run?.diffs) ? run.diffs.length : run?.diffs_count || 0;
  const artifactCount = workflowRunArtifacts(run).length || run?.artifacts_count || 0;
  const stageCount = workflowRunStages(run).length || run?.stages_count || 0;
  if (diffCount || artifactCount || stageCount || workflowRunQualitySummary(run)) {
    const quality = workflowRunQualitySummary(run);
    items.push({
      kind: "evidence",
      title: t("chat.runContextualEvidenceTitle"),
      body: t("chat.runContextualEvidenceBody", { artifacts: artifactCount, diffs: diffCount }),
      action: t("chat.runContextualOpenEvidence"),
      detail: runEvidenceSummaryHTML(run),
      open: Boolean(quality && ["failed", "blocked", "error"].includes(String(quality.status || "").toLowerCase()))
    });
  }
  return items;
}

function runResourcePins(run) {
	const pins = Array.isArray(run?.resource_pins) ? run.resource_pins : [];
	return pins
		.map(pin => ({
			kind: String(pin?.kind || "").trim(),
			name: String(pin?.name || "").trim(),
			version: String(pin?.version || "").trim()
		}))
		.filter(pin => pin.kind && pin.name && pin.version)
		.sort((left, right) => `${left.kind}/${left.name}`.localeCompare(`${right.kind}/${right.name}`));
}

function runResourcePinsHTML(pins) {
	return `<div class="run-evidence-meta">${pins.map(pin => {
		const hash = pin.version.startsWith("sha256:") ? pin.version.slice(7) : pin.version;
		const compact = hash.length > 16 ? `${hash.slice(0, 12)}...` : hash;
		return `<span><small>${escapeHTML(`${pin.kind} / ${pin.name}`)}</small><code title="${escapeHTML(pin.version)}">${escapeHTML(compact)}</code></span>`;
	}).join("")}</div>`;
}

function runSupplementalDetailItemHTML(item) {
  return `<details class="run-contextual-prompt ${escapeHTML(item.kind || "")}"${item.open ? " open" : ""}>
    <summary>
      <span>
        <strong>${escapeHTML(item.title)}</strong>
        <small>${escapeHTML(item.body)}</small>
      </span>
      <em>${escapeHTML(item.action)}</em>
    </summary>
    <div class="run-contextual-prompt-body">${item.detail || ""}</div>
  </details>`;
}

function runEvidenceSummaryHTML(run) {
  const isAgent = run?.run_type === "agent" || !workflowRunStages(run).length && Array.isArray(run?.diffs);
  const evidenceHTML = isAgent ? "" : workflowRunEvidenceHTML(run, { compact: true, maxArtifacts: 4, maxStages: 4 });
  const diffHTML = runDiffViewerHTML(run?.diffs || [], { compact: true, maxDiffs: 4, maxLines: 60 });
  if (evidenceHTML || diffHTML) return `${evidenceHTML}${diffHTML}`;
  const facts = [
    [t("chat.runHistoryArtifacts"), String(run?.artifacts_count || 0)],
    [t("chat.runHistoryDiffs"), String(run?.diffs_count || 0)],
    [t("chat.runHistoryStages"), String(run?.stages_count || 0)]
  ].filter(([, value]) => value && value !== "0");
  return facts.length
    ? `<div class="run-contextual-facts">${facts.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>`
    : `<div class="run-context-empty">${escapeHTML(t("chat.runHistoryLazyNoDetails"))}</div>`;
}

function runSkillScriptContextHTML(run, options = {}) {
  const info = extractSkillScriptContextFromRun(run);
  if (!info) return "";
  const chips = [
    info.runtime ? runToolRiskChipHTML(info.runtime, "neutral") : "",
    info.outputKind ? runToolRiskChipHTML(t("chat.skillScriptOutputChip", { output: info.outputKind }), "neutral") : "",
    info.approval ? runToolRiskChipHTML(skillScriptApprovalLabel(info.approval), info.approval === "required" ? "warn" : "good") : "",
    info.workspaceMount ? runToolRiskChipHTML(t("chat.skillScriptWorkspaceChip", { mount: info.workspaceMount }), info.workspaceMount === "rw" ? "warn" : "good") : "",
    info.network ? runToolRiskChipHTML(t("chat.skillScriptNetworkChip", { network: info.network }), info.network === "disabled" ? "good" : "warn") : "",
    info.timedOut === "true" ? runToolRiskChipHTML(t("chat.skillScriptTimedOut"), "bad") : ""
  ].filter(Boolean).join("");
  const facts = [
    info.skill ? [t("chat.skillScriptSkill"), info.skill] : null,
    info.script ? [t("chat.skillScriptScript"), info.script] : null,
    info.path ? [t("chat.skillScriptPath"), info.path] : null,
    info.runtime ? [t("chat.skillScriptRuntime"), info.runtime] : null,
    info.outputKind ? [t("chat.skillScriptOutput"), info.outputKind] : null,
    info.workspaceMount ? [t("chat.skillScriptWorkspaceMount"), skillScriptWorkspaceMountLabel(info.workspaceMount)] : null,
    info.network ? [t("chat.skillScriptNetwork"), skillScriptNetworkLabel(info.network)] : null,
    info.timeout ? [t("chat.skillScriptTimeout"), info.timeout] : null,
    info.durationMS ? [t("chat.skillScriptDuration"), t("chat.skillScriptDurationValue", { ms: info.durationMS })] : null,
    info.exitCode ? [t("chat.skillScriptExitCode"), info.exitCode] : null,
    info.outputJSONValid ? [t("chat.skillScriptJSONOutput"), skillScriptBoolLabel(info.outputJSONValid)] : null
  ].filter(Boolean);
  const summary = info.sandboxNote || t("chat.skillScriptHelp");
  const label = [info.skill, info.script].filter(Boolean).join(" / ") || info.path || t("chat.skillScriptUnknown");
  return `<section class="run-tool-risk run-skill-script ${options.compact ? "compact" : ""}">
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.skillScriptTitle"))}</strong>
        <span>${escapeHTML(label)}</span>
      </div>
      <small>${escapeHTML(info.toolName || "skill_runner/run_script")}</small>
    </div>
    ${chips ? `<div class="resource-risk-chips">${chips}</div>` : ""}
    <p>${escapeHTML(localizedText(summary))}</p>
    ${facts.length ? `<div class="run-evidence-meta">${facts.map(([labelText, value]) => evidenceMetaItemHTML(labelText, value)).join("")}</div>` : ""}
    ${info.argsPreview && !options.compact ? `<div class="resource-risk-warnings"><span>${escapeHTML(t("chat.skillScriptArgsPreview", { args: info.argsPreview }))}</span></div>` : ""}
  </section>`;
}

function runToolRiskMiniHTML(run, options = {}) {
  const { risk, toolName, doc } = runToolRiskContext(run);
  if (!risk) return "";
  const sandbox = runToolSandboxSummary(risk, doc);
  const level = String(risk.risk_level || "").trim();
  const tone = runToolRiskTone(risk);
  const workspaceScoped = risk.workspace_scoped_inputs === true;
  const workspaceEnforced = risk.workspace_scope_enforced === true;
  const chips = [
    level ? runToolRiskChipHTML(runToolRiskLevelLabel(level), tone) : runToolRiskChipHTML(t("chat.toolRiskUnknown"), "neutral"),
    risk.requires_approval ? runToolRiskChipHTML(t("chat.toolRiskApproval"), "warn") : "",
    risk.destructive ? runToolRiskChipHTML(t("chat.toolRiskDestructive"), "bad") : "",
    risk.sandboxed ? runToolRiskChipHTML(t("chat.toolRiskSandboxed"), "good") : "",
    risk.external_sandbox_recommended ? runToolRiskChipHTML(t("chat.toolRiskExternalSandbox"), "warn") : "",
    workspaceScoped ? runToolRiskChipHTML(t("chat.toolRiskWorkspaceInputs"), workspaceEnforced ? "good" : "warn") : "",
    workspaceScoped ? runToolRiskChipHTML(workspaceEnforced ? t("chat.toolRiskWorkspaceEnforced") : t("chat.toolRiskWorkspaceNotEnforced"), workspaceEnforced ? "good" : "warn") : "",
    ...runToolSandboxChips(sandbox)
  ].filter(Boolean).join("");
  const facts = [
    toolName ? [t("chat.tool"), taskToolDisplayName(toolName)] : null,
    risk.kind ? [t("chat.toolRiskKind"), runToolRiskKindLabel(risk.kind)] : null,
    risk.isolation_level || risk.isolation ? [t("chat.toolRiskIsolation"), runToolRiskIsolationLabel(risk.isolation_level || risk.isolation)] : null,
    Array.isArray(risk.capabilities) && risk.capabilities.length ? [t("chat.toolRiskCapabilities"), risk.capabilities.slice(0, 4).join(", ")] : null,
    workspaceScoped ? [t("chat.toolRiskWorkspaceScope"), workspaceEnforced ? t("chat.toolRiskWorkspaceEnforced") : t("chat.toolRiskWorkspaceNotEnforced")] : null,
    sandbox.sandbox_features.length ? [t("catalog.sandboxFeatures"), runToolSandboxFeatureListLabel(sandbox.sandbox_features)] : null,
    sandbox.missing_sandbox_features.length ? [t("catalog.sandboxMissingFeatures"), runToolSandboxFeatureListLabel(sandbox.missing_sandbox_features)] : null,
    sandbox.windows_isolation ? [t("catalog.windowsIsolation"), runToolWindowsIsolationLabel(sandbox.windows_isolation)] : null
  ].filter(Boolean);
  const warnings = [
    workspaceScoped && !workspaceEnforced ? t("chat.toolRiskWorkspaceNotEnforcedHelp") : "",
    ...(Array.isArray(risk.warnings) ? risk.warnings : [])
  ].filter(Boolean).slice(0, 4);
  const summary = risk.security_boundary || (workspaceScoped
    ? workspaceEnforced ? t("chat.toolRiskWorkspaceEnforcedHelp") : t("chat.toolRiskWorkspaceNotEnforcedHelp")
    : t("chat.toolRiskNoSummary"));
  return `<section class="run-tool-risk ${options.compact ? "compact" : ""} ${escapeHTML(tone)}">
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.toolRiskTitle"))}</strong>
        <span>${escapeHTML(taskToolDisplayName(toolName) || t("chat.toolRiskProfile"))}</span>
      </div>
      <small>${escapeHTML(runToolRiskLevelLabel(level || "unknown"))}</small>
    </div>
    ${chips ? `<div class="resource-risk-chips">${chips}</div>` : ""}
    <p>${escapeHTML(localizedText(summary))}</p>
    ${facts.length ? `<div class="run-evidence-meta">${facts.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
    ${warnings.length ? `<div class="resource-risk-warnings">${warnings.map(item => `<span>${escapeHTML(localizedText(item))}</span>`).join("")}</div>` : ""}
  </section>`;
}

function runToolRiskContext(run = {}) {
  const candidates = [
    { risk: run.pending_tool_risk, doc: run.pending_tool, toolName: run.pending_tool_name || run.pending_tool?.name || run.tool_name || "" },
    { risk: run.pending_tool?.risk, doc: run.pending_tool, toolName: run.pending_tool?.name || run.pending_tool_name || run.tool_name || "" },
    { risk: run.tool_risk, doc: run.tool || run.pending_tool, toolName: run.pending_tool_name || run.tool_name || "" },
    { risk: run.risk, doc: run.tool || run.pending_tool, toolName: run.pending_tool_name || run.tool_name || "" }
  ];
  const direct = candidates.find(item => isToolRiskProfile(item.risk));
  if (direct) return direct;
  const events = Array.isArray(run?.events) ? [...run.events].reverse() : [];
  for (const event of events) {
    const risk = event?.risk || event?.tool_risk;
    if (isToolRiskProfile(risk)) {
      return {
        risk,
        doc: event.tool || event.pending_tool || {},
        toolName: event.tool_name || event.tool?.name || (typeof event.tool === "string" ? event.tool : "") || run.pending_tool_name || ""
      };
    }
  }
  return { risk: null, doc: run.pending_tool || run.tool || {}, toolName: run.pending_tool_name || run.tool_name || "" };
}

function isToolRiskProfile(risk) {
  if (!risk || typeof risk !== "object" || Array.isArray(risk)) return false;
  return [
    "risk_level",
    "requires_approval",
    "destructive",
    "sandboxed",
    "external_sandbox_recommended",
    "workspace_scoped_inputs",
    "workspace_scope_enforced",
    "kind",
    "capabilities",
    "warnings",
    "security_boundary",
    "isolation_level",
    "isolation",
    "isolation_options",
    "sandbox_features",
    "missing_sandbox_features",
    "windows_isolation"
  ].some(key => Object.prototype.hasOwnProperty.call(risk, key));
}

function toolRiskSignature(risk) {
  if (!isToolRiskProfile(risk)) return "";
  return [
    risk.risk_level || "",
    risk.kind || "",
    risk.isolation_level || risk.isolation || "",
    risk.requires_approval === true ? "approval" : "",
    risk.destructive === true ? "destructive" : "",
    risk.sandboxed === true ? "sandboxed" : "",
    risk.external_sandbox_recommended === true ? "external" : "",
    risk.workspace_scoped_inputs === true ? "path-inputs" : "",
    risk.workspace_scope_enforced === true ? "workspace-enforced" : risk.workspace_scope_enforced === false ? "workspace-open" : "",
    Array.isArray(risk.sandbox_features) ? risk.sandbox_features.join(",") : "",
    Array.isArray(risk.missing_sandbox_features) ? risk.missing_sandbox_features.join(",") : "",
    risk.windows_isolation ? JSON.stringify(risk.windows_isolation) : "",
    Array.isArray(risk.capabilities) ? risk.capabilities.join(",") : "",
    Array.isArray(risk.warnings) ? risk.warnings.length : ""
  ].join("|");
}

function runToolRiskTone(risk = {}) {
  const value = String(risk.risk_level || "").toLowerCase();
  if (risk.destructive || value === "critical" || value === "high") return "bad";
  if (risk.requires_approval || risk.external_sandbox_recommended || risk.workspace_scoped_inputs && risk.workspace_scope_enforced === false || value === "medium") return "warn";
  if (risk.sandboxed || risk.workspace_scope_enforced || value === "low") return "good";
  return "neutral";
}

function runToolRiskChipHTML(label, tone = "") {
  return `<span class="resource-risk-chip ${escapeHTML(tone)}">${escapeHTML(label)}</span>`;
}

function runToolSandboxSummary(risk = {}, doc = {}) {
  return {
    sandbox_features: runFirstArray(risk.sandbox_features, doc.sandbox_features),
    missing_sandbox_features: runFirstArray(risk.missing_sandbox_features, doc.missing_sandbox_features),
    windows_isolation: risk.windows_isolation || doc.windows_isolation || null
  };
}

function runToolSandboxChips(sandbox = {}) {
  const chips = [];
  if (Array.isArray(sandbox.sandbox_features) && sandbox.sandbox_features.length) {
    chips.push(runToolRiskChipHTML(t("catalog.sandboxEnabledCount", { count: sandbox.sandbox_features.length }), "good"));
  }
  if (Array.isArray(sandbox.missing_sandbox_features) && sandbox.missing_sandbox_features.length) {
    chips.push(runToolRiskChipHTML(t("catalog.sandboxMissingCount", { count: sandbox.missing_sandbox_features.length }), "warn"));
  }
  return chips;
}

function runToolSandboxFeatureListLabel(features = []) {
  return features.slice(0, 4).map(runToolSandboxFeatureLabel).join(", ");
}

function runToolSandboxFeatureLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  const key = `catalog.sandboxFeature.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function runToolWindowsIsolationLabel(profile = {}) {
  const labels = [
    profile.job_object ? t("catalog.windowsIsolation.jobObject") : "",
    profile.restricted_token ? t("catalog.windowsIsolation.restrictedToken") : "",
    profile.app_container ? t("catalog.windowsIsolation.appContainer") : "",
    profile.lifecycle_only ? t("catalog.windowsIsolation.lifecycleOnly") : ""
  ].filter(Boolean);
  return labels.join(", ") || t("catalog.windowsIsolation.none");
}

function runFirstArray(...values) {
  const found = values.find(value => Array.isArray(value) && value.length);
  return found ? found.slice(0, 8) : [];
}

function skillScriptApprovalLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `chat.skillScriptApproval.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function skillScriptWorkspaceMountLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `chat.skillScriptWorkspaceMount.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function skillScriptNetworkLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `chat.skillScriptNetwork.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function skillScriptBoolLabel(value) {
  return String(value || "").toLowerCase() === "true" ? t("chat.yes") : t("chat.no");
}

function runToolRiskLevelLabel(level) {
  const value = String(level || "").toLowerCase();
  const key = `chat.toolRisk.${value}`;
  const translated = t(key);
  return translated === key ? level : translated;
}

function runToolRiskKindLabel(kind) {
  const value = String(kind || "").toLowerCase();
  const key = `catalog.toolRiskKind.${value}`;
  const translated = t(key);
  return translated === key ? localizedText(kind) : translated;
}

function runToolRiskIsolationLabel(isolation) {
  const value = String(isolation || "").toLowerCase();
  const key = `catalog.isolation.${value}`;
  const translated = t(key);
  return translated === key ? localizedText(isolation) : translated;
}

function renderWorkflowRunEvidence(state, run) {
  if (!state?.resultOutput) return;
  const evidenceHTML = workflowRunEvidenceHTML(run, { maxArtifacts: 6, maxStages: 5 });
  const diffHTML = runDiffViewerHTML(run?.diffs || [], { maxDiffs: 6, maxLines: 120 });
  state.resultOutput.querySelector("[data-result-evidence]")?.remove();
  state.resultOutput.querySelector("[data-result-diffs]")?.remove();
  if (!evidenceHTML && !diffHTML) {
    updateResultEmptyState(state);
    return;
  }
  if (!state.resultOutput.querySelector(".result-section-label")) {
    state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  }
  if (evidenceHTML) {
    const wrapper = document.createElement("div");
    wrapper.dataset.resultEvidence = "true";
    wrapper.innerHTML = evidenceHTML;
    state.resultOutput.appendChild(wrapper);
  }
  if (diffHTML) {
    const wrapper = document.createElement("div");
    wrapper.dataset.resultDiffs = "true";
    wrapper.innerHTML = diffHTML;
    state.resultOutput.appendChild(wrapper);
  }
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function renderRunDiffs(state, run) {
  if (!state?.resultOutput) return;
  const diffHTML = runDiffViewerHTML(run?.diffs || [], { maxDiffs: 6, maxLines: 120 });
  state.resultOutput.querySelector("[data-result-diffs]")?.remove();
  if (!diffHTML) {
    updateResultEmptyState(state);
    return;
  }
  if (!state.resultOutput.querySelector(".result-section-label")) {
    state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  }
  const wrapper = document.createElement("div");
  wrapper.dataset.resultDiffs = "true";
  wrapper.innerHTML = diffHTML;
  state.resultOutput.appendChild(wrapper);
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
}

function workflowRunDeliveryHTML(run, options = {}) {
  const artifacts = workflowRunArtifacts(run);
  const stages = workflowRunStages(run);
  const diffs = Array.isArray(run?.diffs) ? run.diffs : [];
  const diffCount = diffs.length || Number(run?.diffs_count || 0);
  const publicSummary = publicResultText(options.summary || "");
  const hasOutput = Boolean(options.hasOutput && publicSummary && publicSummary !== t("chat.runHistoryNoSummary"));
  const summary = publicSummary || t("chat.deliveryNoSummary");
  const lastStage = stages.length ? stages[stages.length - 1] : null;
  const finalSource = workflowRunFinalSourceLabel(run);
  const filePreview = diffPathPreview(diffs);
  const artifactPreview = artifactKindPreview(artifacts);
  const items = [
    {
      label: t("chat.deliveryFinalOutput"),
      value: hasOutput ? t("chat.deliveryReady") : t("chat.deliveryNoSummary"),
      detail: hasOutput ? finalSource || t("chat.deliveryFinalOutputHelp") : t("chat.deliveryContextHelp"),
      tone: hasOutput ? "good" : "neutral"
    },
    {
      label: t("chat.deliveryChangedFiles"),
      value: diffCount ? t("chat.deliveryChangedFilesCount", { count: diffCount }) : t("chat.deliveryNone"),
      detail: filePreview || (diffCount ? t("chat.runHistoryLazyDiffsHelp") : t("chat.deliveryChangedFilesHelp")),
      tone: diffCount ? "info" : "neutral"
    },
    {
      label: t("chat.deliveryArtifacts"),
      value: artifacts.length ? t("chat.deliveryArtifactsCount", { count: artifacts.length }) : t("chat.deliveryNone"),
      detail: artifactPreview || t("chat.deliveryArtifactsHelp"),
      tone: artifacts.length ? "good" : "neutral"
    },
    {
      label: t("chat.deliveryStages"),
      value: stages.length ? t("chat.deliveryStagesCount", { count: stages.length }) : t("chat.deliveryNone"),
      detail: lastStage ? t("chat.deliveryLastStage", { stage: localizedText(lastStage.stage || lastStage.name || t("chat.runHistoryStage")) }) : t("chat.deliveryStagesHelp"),
      tone: stages.length ? "info" : "neutral"
    }
  ];
  return `<section class="run-delivery-summary">
    <div class="run-delivery-head">
      <div>
        <strong>${escapeHTML(t("chat.deliveryTitle"))}</strong>
        <span>${escapeHTML(t("chat.deliveryHelp"))}</span>
      </div>
      <small>${escapeHTML(workflowRunStatusLabel(run.status || ""))}</small>
    </div>
    <div class="run-markdown run-delivery-text">${renderMarkdown(truncateEvidenceText(localizedRunMarkdownText(summary), 520))}</div>
    <div class="run-delivery-grid">
      ${items.map(item => `<article class="${escapeHTML(item.tone)}">
        <small>${escapeHTML(item.label)}</small>
        <strong>${escapeHTML(item.value)}</strong>
        <span>${escapeHTML(truncateEvidenceText(item.detail, 120))}</span>
      </article>`).join("")}
    </div>
  </section>`;
}

function workflowRunFinalSourceLabel(run = {}) {
  const stages = workflowRunStages(run);
  const stage = workflowRunPreferredFinalStage(stages);
  if (stage?.stage || stage?.name) return t("chat.deliveryFinalStage", { stage: localizedText(stage.stage || stage.name) });
  const artifacts = workflowRunArtifacts(run);
  const artifact = workflowRunPreferredFinalArtifact(artifacts);
  const label = artifact?.title || artifact?.kind || artifact?.id || "";
  if (label) return t("chat.deliveryFinalArtifact", { artifact: localizedText(label) });
  return "";
}

function diffPathPreview(diffs) {
  const paths = (Array.isArray(diffs) ? diffs : [])
    .map(item => String(item?.path || "").trim())
    .filter(Boolean);
  if (!paths.length) return "";
  const visible = paths.slice(0, 3).join(", ");
  return paths.length > 3 ? t("chat.deliveryChangedFilesPreviewMore", { files: visible, count: paths.length - 3 }) : visible;
}

function artifactKindPreview(artifacts) {
  const labels = (Array.isArray(artifacts) ? artifacts : [])
    .map(item => artifactKindLabel(item?.kind) || String(item?.title || item?.name || "").trim())
    .filter(Boolean);
  if (!labels.length) return "";
  const visible = labels.slice(0, 3).join(", ");
  return labels.length > 3 ? t("chat.deliveryArtifactsPreviewMore", { artifacts: visible, count: labels.length - 3 }) : visible;
}

function workflowRunEvidenceHTML(run, options = {}) {
  const artifacts = workflowRunArtifacts(run).slice(0, options.maxArtifacts || 6);
  const stages = workflowRunStages(run).slice(-1 * (options.maxStages || 5)).reverse();
  const quality = workflowRunQualitySummary(run);
  if (!artifacts.length && !stages.length && !quality) return "";
  return `<section class="run-evidence ${options.compact ? "compact" : ""}" data-result-evidence>
    <div class="run-evidence-head">
      <div>
        <strong>${escapeHTML(t("chat.runEvidenceTitle"))}</strong>
        <span>${escapeHTML(t("chat.runEvidenceHelp"))}</span>
      </div>
      <small>${escapeHTML(t("chat.runEvidenceCount", { artifacts: artifacts.length, stages: stages.length }))}</small>
    </div>
    ${quality ? runQualityPanelHTML(quality) : ""}
    ${artifacts.length ? `<div class="run-evidence-section">
      <strong>${escapeHTML(t("chat.runArtifactsTitle"))}</strong>
      <div class="run-artifact-grid">${artifacts.map(runArtifactCard).join("")}</div>
    </div>` : ""}
    ${stages.length ? `<div class="run-evidence-section">
      <strong>${escapeHTML(t("chat.runStagesTitle"))}</strong>
      <div class="run-stage-list">${stages.map(runStageCard).join("")}</div>
    </div>` : ""}
  </section>`;
}

function runArtifactViewerHTML(payload, options = {}) {
  const items = normalizeRunArtifactPayload(payload);
  const counts = payload && typeof payload === "object" && !Array.isArray(payload) ? payload.counts || {} : {};
  if (!items.length) {
    return `<section class="run-artifact-viewer ${options.compact ? "compact" : ""}">
      <div class="run-artifact-viewer-empty">
        <strong>${escapeHTML(t("chat.artifactViewerEmpty"))}</strong>
        <span>${escapeHTML(t("chat.artifactViewerEmptyHelp"))}</span>
      </div>
    </section>`;
  }
  const total = counts.total || items.length;
  const withContent = counts.with_content || items.filter(item => String(item.content || "").trim()).length;
  const externalRefs = counts.external_refs || items.filter(item => item.ref || item.artifact_ref || item.hash).length;
  const selected = selectRunArtifact(items);
  return `<section class="run-artifact-viewer ${options.compact ? "compact" : ""}">
    <div class="run-artifact-viewer-head">
      <div>
        <strong>${escapeHTML(t("chat.artifactViewerTitle"))}</strong>
        <span>${escapeHTML(t("chat.artifactViewerHelp"))}</span>
      </div>
      <small>${escapeHTML(t("chat.artifactViewerCount", { count: total }))}</small>
    </div>
    <div class="run-artifact-viewer-stats">
      ${evidenceMetaItemHTML(t("chat.artifactViewerLoadedContent"), formatRunArtifactNumber(withContent))}
      ${evidenceMetaItemHTML(t("chat.artifactViewerExternalRefs"), formatRunArtifactNumber(externalRefs))}
      ${counts.content_omitted ? evidenceMetaItemHTML(t("chat.artifactViewerSummaryOnly"), formatRunArtifactNumber(counts.content_omitted)) : ""}
    </div>
    <div class="run-artifact-browser">
      <div class="run-artifact-list" role="list">
        ${items.map(item => runArtifactListItemHTML(item, item === selected)).join("")}
      </div>
      <article class="run-artifact-preview">
        ${runArtifactPreviewHTML(selected)}
      </article>
    </div>
  </section>`;
}

function normalizeRunArtifactPayload(payload) {
  const raw = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.items)
      ? payload.items
      : Array.isArray(payload?.artifacts)
        ? payload.artifacts
        : [];
  return raw.filter(item => item && typeof item === "object").map(normalizeRunArtifactItem);
}

function normalizeRunArtifactItem(item = {}) {
  const ref = String(item.ref || item.artifact_ref || item.artifactRef || (item.hash ? `sha256:${item.hash}` : "") || "").trim();
  let sizeValue = item.size;
  if (sizeValue === null || sizeValue === undefined) sizeValue = item.content_bytes;
  if (sizeValue === null || sizeValue === undefined) sizeValue = item.contentBytes;
  if (sizeValue === null || sizeValue === undefined) sizeValue = String(item.content || item.summary || "").length;
  let storedBytesValue = item.stored_bytes;
  if (storedBytesValue === null || storedBytesValue === undefined) storedBytesValue = item.storedBytes;
  if (storedBytesValue === null || storedBytesValue === undefined) storedBytesValue = 0;
  const workspaceArtifact = workspaceArtifactPayload(item);
  return {
    ...item,
    id: item.id || item.name || ref || item.title || item.kind || "artifact",
    kind: item.kind || item.type || "artifact",
    title: item.title || item.name || item.id || item.kind || t("chat.runArtifact"),
    summary: publicResultText(item.summary || item.description || ""),
    content: item.content || item.body || "",
    ref,
    artifact_ref: item.artifact_ref || item.artifactRef || ref,
    size: Number(sizeValue || 0),
    stored_bytes: Number(storedBytesValue || 0),
    metadata: item.metadata && typeof item.metadata === "object" ? item.metadata : {},
    workspace_artifact: workspaceArtifact
  };
}

function workspaceArtifactPayload(item = {}) {
  const candidates = [item, item.metadata];
  const content = String(item.content || "").trim();
  if (content.startsWith("{") && content.endsWith("}")) {
    try {
      candidates.push(JSON.parse(content));
    } catch {
      // Tool summaries are allowed to be ordinary text.
    }
  }
  for (const candidate of candidates) {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) continue;
    const artifactPath = String(candidate.artifact_path || candidate.artifactPath || "").trim();
    if (!artifactPath) continue;
    return {
      artifact_path: artifactPath,
      pdf_path: String(candidate.pdf_path || candidate.pdfPath || "").trim(),
      preview_paths: Array.isArray(candidate.preview_paths)
        ? candidate.preview_paths.map(value => String(value || "").trim()).filter(Boolean)
        : Array.isArray(candidate.previewPaths)
          ? candidate.previewPaths.map(value => String(value || "").trim()).filter(Boolean)
          : [],
      mime: String(candidate.mime || item.mime || "").trim(),
      sha256: String(candidate.sha256 || "").trim()
    };
  }
  return null;
}

function selectRunArtifact(items) {
  const officeArtifact = items.find(item => item.workspace_artifact?.artifact_path);
  if (officeArtifact) return officeArtifact;
  return items.find(item => String(item.content || "").trim()) || items[0];
}

function runArtifactListItemHTML(item, selected) {
  const label = artifactKindLabel(item.kind) || localizedText(item.kind || t("chat.runArtifact"));
  const summary = compactRunArtifactText(item.summary || item.content || t("chat.runArtifactNoSummary"), 150);
  const meta = [
    item.stage ? localizedText(item.stage) : "",
    taskToolDisplayName(item.tool_name || item.toolName || ""),
    item.size ? `${formatRunArtifactNumber(item.size)} B` : ""
  ].filter(Boolean);
  return `<article class="run-artifact-list-item ${selected ? "active" : ""} ${item.is_error ? "error" : ""}" role="listitem">
    <div class="run-artifact-list-main">
      <span class="run-artifact-list-mark" aria-hidden="true"></span>
      <span class="run-artifact-list-title">
        <span class="badge neutral">${escapeHTML(label)}</span>
        <strong>${escapeHTML(localizedText(item.title))}</strong>
      </span>
    </div>
    <p>${escapeHTML(localizedText(summary))}</p>
    ${meta.length ? `<div class="run-artifact-list-meta">${meta.map(value => `<span>${escapeHTML(localizedText(value))}</span>`).join("")}</div>` : ""}
  </article>`;
}

function runArtifactPreviewHTML(item = {}) {
  if (!item) return "";
  const content = publicResultText(item.content || item.summary || "") || t("chat.runArtifactNoSummary");
  const summary = publicResultText(item.summary || "");
  const ref = item.ref || item.artifact_ref || (item.hash ? `sha256:${item.hash}` : "");
  const metadata = item.metadata && typeof item.metadata === "object" ? item.metadata : {};
  const meta = [
    item.kind ? [t("chat.runArtifactKind"), artifactKindLabel(item.kind) || item.kind] : null,
    item.stage ? [t("chat.runHistoryStage"), localizedText(item.stage)] : null,
    item.tool_name ? [t("chat.tool"), taskToolDisplayName(item.tool_name)] : null,
    item.mime ? [t("chat.artifactViewerMime"), item.mime] : null,
    item.size ? [t("chat.artifactViewerSize"), `${formatRunArtifactNumber(item.size)} B`] : null,
    item.stored_bytes ? [t("chat.artifactViewerStored"), `${formatRunArtifactNumber(item.stored_bytes)} B`] : null,
    metadata.status ? [t("chat.runHistoryStatus"), localizedText(metadata.status)] : null,
    metadata.files ? [t("chat.deliveryChangedFiles"), metadata.files] : null
  ].filter(Boolean);
  const officeArtifact = officeArtifactHTML(item.workspace_artifact, { preview: true });
  return `<div class="run-artifact-preview-inner ${item.is_error ? "error" : ""}">
    <div class="run-artifact-preview-head">
      <div>
        <span>${escapeHTML(artifactKindLabel(item.kind) || localizedText(item.kind || t("chat.runArtifact")))}</span>
        <strong>${escapeHTML(localizedText(item.title || t("chat.runArtifact")))}</strong>
      </div>
      ${ref ? `<code>${escapeHTML(ref)}</code>` : ""}
    </div>
    ${summary && item.content ? `<p class="run-artifact-preview-summary">${escapeHTML(compactRunArtifactText(localizedText(summary), 260))}</p>` : ""}
    ${meta.length ? `<div class="run-evidence-meta">${meta.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
    ${officeArtifact}
    <div class="run-artifact-content-shell">
      <div class="run-artifact-content-head">
        <span>${escapeHTML(item.content ? t("chat.artifactViewerContent") : t("chat.artifactViewerSummary"))}</span>
        <span>${escapeHTML(item.content ? t("chat.artifactViewerLoaded") : t("chat.artifactViewerSummaryOnly"))}</span>
      </div>
      <div class="run-markdown run-artifact-content">${renderMarkdown(markdownResultText(localizedRunMarkdownText(content)))}</div>
    </div>
  </div>`;
}

function compactRunArtifactText(value, limit) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 3))}...`;
}

function formatRunArtifactNumber(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) return "0";
  return new Intl.NumberFormat().format(number);
}

function workflowRunQualitySummary(run) {
  const direct = run?.quality && typeof run.quality === "object" && !Array.isArray(run.quality) ? run.quality : {};
  const items = workflowRunEvidenceItems(run);
  const qualityItem = items.find(item => ["quality", "quality_gate", "gate"].includes(evidenceItemKind(item)));
  const stage = workflowRunStages(run).find(item => String(item.node_type || "").includes("quality") || String(item.stage || item.name || "").toLowerCase().includes("quality"));
  const outputs = stage ? runStageOutputs(stage) || {} : {};
  const merged = { ...(stage || {}), ...outputs, ...(qualityItem || {}), ...direct };
  return Object.keys(merged).length ? merged : null;
}

function runQualityPanelHTML(quality = {}) {
  const outputs = quality.outputs || quality.result?.outputs || {};
  const status = quality.quality_status || outputs.quality_status || quality.status || quality.result?.status || "";
  const score = quality.score ?? quality.quality_score ?? outputs.score ?? quality.result?.score ?? "";
  const stage = quality.stage || quality.stage_name || quality.name || "";
  const failures = publicResultText(quality.failures || outputs.failures || quality.failure_reasons || quality.summary || quality.reason || "");
  const meta = [
    status ? [t("chat.qualityStatus"), acceptanceStatusLabel(status)] : null,
    score !== "" && score !== null && score !== undefined ? [t("chat.qualityScore"), String(score)] : null,
    stage ? [t("chat.qualityStage"), localizedText(stage)] : null
  ].filter(Boolean);
  return `<div class="run-quality-panel ${acceptanceToneClass(status)}">
    <div class="run-evidence-card-head">
      <strong>${escapeHTML(t("chat.qualityPanelTitle"))}</strong>
      ${status ? `<span>${escapeHTML(acceptanceStatusLabel(status))}</span>` : ""}
    </div>
    <p>${escapeHTML(failures ? truncateEvidenceText(localizedText(String(failures)), 320) : t("chat.qualityPanelHelp"))}</p>
    ${meta.length ? `<div class="run-evidence-meta">${meta.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
  </div>`;
}

function workflowRunArtifacts(run) {
  const evidenceArtifacts = workflowRunEvidenceItems(run)
    .filter(item => evidenceItemKind(item) !== "stage")
    .map(evidenceItemToArtifact);
  const direct = Array.isArray(run?.artifacts) ? run.artifacts : [];
  const stageArtifacts = workflowRunStages(run).flatMap(stage => Array.isArray(stage.artifacts) ? stage.artifacts : []);
  return dedupeEvidenceArtifacts([...evidenceArtifacts, ...direct, ...stageArtifacts]);
}

function workflowRunStages(run) {
  const direct = Array.isArray(run?.completed_stages) ? run.completed_stages : [];
  const evidenceStages = workflowRunEvidenceItems(run)
    .filter(item => evidenceItemKind(item) === "stage")
    .map(evidenceItemToStage);
  return dedupeEvidenceStages([...direct, ...evidenceStages]);
}

function workflowRunEvidenceItems(run) {
  return Array.isArray(run?.evidence) ? run.evidence : [];
}

function normalizeWorkflowRunEvidencePayload(payload) {
  const items = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.items)
      ? payload.items
      : Array.isArray(payload?.evidence)
        ? payload.evidence
        : Array.isArray(payload?.artifacts)
          ? payload.artifacts
          : [];
  const quality = payload?.quality || payload?.quality_summary || payload?.quality_gate || {};
  return { items: items.filter(Boolean), quality };
}

function evidenceItemKind(item = {}) {
  return String(item.kind || item.type || item.evidence_kind || "").trim().toLowerCase();
}

function evidenceItemToArtifact(item = {}) {
  const metadata = item.metadata && typeof item.metadata === "object" ? item.metadata : {};
  return {
    ...item,
    id: item.id || item.evidence_id || item.name,
    name: item.name || item.id || item.evidence_id,
    kind: item.kind || item.type || "evidence",
    title: item.title || item.label || item.name || item.id,
    summary: publicResultText(item.summary || item.reason || item.message || item.description || item.content || ""),
    content: item.content || item.body || item.raw || "",
    stage: item.stage || item.stage_name || metadata.stage,
    tool_name: item.tool_name || item.tool || metadata.tool_name,
    metadata: { ...metadata, status: metadata.status || item.status, expected: metadata.expected || item.expected }
  };
}

function evidenceItemToStage(item = {}) {
  return {
    ...item,
    stage: item.stage || item.stage_name || item.name,
    name: item.stage || item.stage_name || item.name,
    summary: publicResultText(item.summary || item.message || item.description || item.content || ""),
    status: item.status || item.result?.status || "",
    node_type: item.node_type || item.nodeType || "",
    acceptance: Array.isArray(item.acceptance) ? item.acceptance : []
  };
}

function dedupeEvidenceArtifacts(items) {
  const seen = new Set();
  const out = [];
  for (const item of items.filter(Boolean)) {
    const key = [item.id || "", item.name || "", item.stage || "", item.kind || "", item.title || ""].join("|");
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}

function dedupeEvidenceStages(items) {
  const seen = new Set();
  const out = [];
  for (const item of items.filter(Boolean)) {
    const key = [item.stage || item.name || "", item.status || "", item.summary || item.result?.output || ""].join("|");
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(item);
  }
  return out;
}

function artifactKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  if (!value) return "";
  if (value === "acceptance") return t("chat.acceptanceArtifact");
  const key = `chat.artifactKind.${value}`;
  const translated = t(key);
  return translated === key ? kind : translated;
}

function runArtifactCard(artifact) {
  const title = artifact.title || artifact.name || artifact.id || artifact.kind || t("chat.runArtifact");
  const summary = publicResultText(artifact.summary || artifact.content || "") || t("chat.runArtifactNoSummary");
  const isAcceptance = String(artifact.kind || "").toLowerCase() === "acceptance";
  const metadata = artifact.metadata || {};
  const meta = [
    artifact.stage ? [t("chat.runHistoryStage"), localizedText(artifact.stage)] : null,
    artifact.kind ? [t("chat.runArtifactKind"), artifactKindLabel(artifact.kind)] : null,
    artifact.tool_name ? [t("chat.tool"), taskToolDisplayName(artifact.tool_name)] : null,
    isAcceptance && metadata.status ? [t("chat.acceptanceStatus"), acceptanceStatusLabel(metadata.status)] : null,
    isAcceptance && metadata.expected ? [t("chat.acceptanceExpected"), metadata.expected] : null
  ].filter(Boolean);
  const workspaceArtifact = artifact.workspace_artifact || workspaceArtifactPayload(artifact);
  return `<article class="run-artifact-card ${artifact.is_error ? "error" : ""} ${isAcceptance ? acceptanceToneClass(metadata.status) : ""}">
    <div class="run-evidence-card-head">
      <strong>${escapeHTML(localizedText(title))}</strong>
      ${artifact.kind ? `<span>${escapeHTML(artifactKindLabel(artifact.kind))}</span>` : ""}
    </div>
    <div class="run-markdown run-evidence-text">${renderMarkdown(truncateEvidenceText(localizedRunMarkdownText(summary), 420))}</div>
    ${meta.length ? `<div class="run-evidence-meta">${meta.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
    ${officeArtifactHTML(workspaceArtifact, { compact: true })}
  </article>`;
}

function officeArtifactHTML(payload, options = {}) {
  if (!payload?.artifact_path) return "";
  const path = String(payload.artifact_path || "").trim();
  const previewPath = String(payload.preview_paths?.[0] || "").trim();
  const pdfPath = String(payload.pdf_path || "").trim();
  const downloadURL = workspaceArtifactURL(path, true);
  const previewTarget = previewPath || pdfPath || (String(payload.mime || "").toLowerCase() === "application/pdf" ? path : "");
  const previewURL = previewTarget ? workspaceArtifactURL(previewTarget, false) : "";
  return `<div class="office-artifact ${options.compact ? "compact" : ""}">
    ${options.preview && previewPath ? `<img loading="lazy" src="${escapeHTML(previewURL)}" alt="${escapeHTML(t("chat.officeArtifactPreviewAlt"))}">` : ""}
    <div class="office-artifact-actions">
      <a href="${escapeHTML(downloadURL)}">${escapeHTML(t("chat.officeArtifactDownload"))}</a>
      ${previewURL ? `<a href="${escapeHTML(previewURL)}" target="_blank" rel="noopener">${escapeHTML(t("chat.officeArtifactPreview"))}</a>` : ""}
      <button type="button" data-office-edit-path="${escapeHTML(path)}">${escapeHTML(t("chat.officeArtifactContinue"))}</button>
    </div>
  </div>`;
}

function workspaceArtifactURL(path, download) {
  const params = new URLSearchParams({ path: String(path || "") });
  if (download) params.set("download", "1");
  return `/api/workspace-file?${params.toString()}`;
}

function runStageCard(stage) {
  const title = localizedText(stage.stage || stage.name || t("chat.runHistoryStage"));
  const summary = publicResultText(stage.summary || stage.result?.output || "") || t("chat.runStageNoSummary");
  const routeText = runStageRouteText(stage);
  const outputCount = runStageOutputCount(stage);
  const acceptance = Array.isArray(stage.acceptance) ? stage.acceptance : [];
  const acceptanceSummary = runStageAcceptanceSummary(acceptance);
  const meta = [
    stage.status ? [t("chat.runHistoryStatus"), workflowRunTimelineLabel(stage.status)] : null,
    stage.node_type ? [t("workflow.runtimeField.nodeType"), localizedText(stage.node_type)] : null,
    stage.agent_id ? [t("chat.agent"), taskResourceDisplayName("agent", stage.agent_id, localizedText(stage.agent_id))] : null,
    stage.skill ? [t("chat.skill"), taskResourceDisplayName("skill", stage.skill, localizedText(stage.skill))] : null,
    stage.tool ? [t("chat.tool"), taskToolDisplayName(stage.tool)] : null,
    stage.attempts ? [t("workflow.runtimeField.attempts"), stage.attempts] : null,
    routeText ? [t("chat.runStageRoute"), routeText] : null,
    outputCount ? [t("workflow.runtimeField.outputs"), t("chat.runStageOutputsCount", { count: outputCount })] : null,
    acceptance.length ? [t("chat.acceptanceTitle"), acceptanceSummary] : null
  ].filter(Boolean);
  return `<article class="run-stage-card ${workflowRunStageHasError(stage) ? "error" : ""} ${routeText ? "has-route" : ""} ${acceptance.length ? "has-acceptance" : ""}">
    <div class="run-evidence-card-head">
      <strong>${escapeHTML(title)}</strong>
      ${stage.status ? `<span>${escapeHTML(workflowRunTimelineLabel(stage.status))}</span>` : ""}
    </div>
    ${routeText ? `<div class="run-stage-route">${escapeHTML(routeText)}</div>` : ""}
    <div class="run-markdown run-evidence-text">${renderMarkdown(truncateEvidenceText(localizedRunMarkdownText(summary), 360))}</div>
    ${acceptance.length ? `<div class="run-acceptance-list">${acceptance.slice(0, 4).map(runAcceptanceItem).join("")}</div>` : ""}
    ${meta.length ? `<div class="run-evidence-meta">${meta.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
  </article>`;
}

function runAcceptanceItem(item = {}) {
  const status = item.status || "";
  const title = localizedText(item.name || item.description || t("chat.acceptanceCriterion"));
  const body = localizedRunMarkdownText(publicResultText(item.reason || item.actual || item.expected || "") || t("chat.acceptanceNoDetail"));
  return `<div class="run-acceptance-item ${acceptanceToneClass(status)}">
    <div>
      <strong>${escapeHTML(title)}</strong>
      <span>${escapeHTML(acceptanceStatusLabel(status))}</span>
    </div>
    <div class="run-markdown run-acceptance-text">${renderMarkdown(truncateEvidenceText(body, 220))}</div>
  </div>`;
}

function runStageAcceptanceSummary(items = []) {
  const counts = items.reduce((acc, item) => {
    const key = acceptanceStatusKey(item?.status);
    acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
  return [
    counts.pass ? t("chat.acceptancePassedCount", { count: counts.pass }) : "",
    counts.fail ? t("chat.acceptanceFailedCount", { count: counts.fail }) : "",
    counts.warn ? t("chat.acceptanceWarningCount", { count: counts.warn }) : "",
    counts.unknown ? t("chat.acceptanceUnknownCount", { count: counts.unknown }) : ""
  ].filter(Boolean).join(" / ");
}

function acceptanceStatusKey(status) {
  const value = String(status || "").toLowerCase();
  if (["pass", "passed", "ok", "success", "succeeded", "true", "completed", "complete", "done"].includes(value)) return "pass";
  if (["fail", "failed", "error", "false", "blocked", "denied", "cancelled", "canceled"].includes(value)) return "fail";
  if (["warn", "warning", "partial", "needs_review", "awaiting_approval", "awaiting_tool_approval", "awaiting_input"].includes(value)) return "warn";
  return "unknown";
}

function acceptanceToneClass(status) {
  const key = acceptanceStatusKey(status);
  if (key === "pass") return "acceptance-pass";
  if (key === "fail") return "acceptance-fail";
  if (key === "warn") return "acceptance-warn";
  return "acceptance-unknown";
}

function acceptanceStatusLabel(status) {
  const key = `chat.acceptanceStatus.${acceptanceStatusKey(status)}`;
  const translated = t(key);
  return translated === key ? String(status || t("common.none")) : translated;
}

function runStageRouteText(stage) {
  const outputs = runStageOutputs(stage);
  const rawRoute = stage.route ?? stage.result?.route ?? outputs?.route ?? "";
  const rawValue = stage.value ?? stage.result?.value ?? outputs?.value;
  const rawTarget = stage.target ?? stage.result?.target ?? outputs?.target ?? "";
  const rawPassed = stage.passed ?? stage.result?.passed ?? outputs?.passed;
  const route = valueText(rawRoute) || valueText(rawValue) || (rawPassed === true ? t("workflow.branchPassed") : rawPassed === false ? t("workflow.branchBlocked") : "");
  const target = valueText(rawTarget);
  if (route && target) return t("chat.runStageRouteTarget", { route, target });
  if (route) return t("chat.runStageRouteValue", { route });
  if (target) return t("chat.runStageRouteTarget", { route: t("chat.runStageRoute"), target });
  return "";
}

function runStageOutputCount(stage) {
  const outputs = runStageOutputs(stage);
  if (!outputs || typeof outputs !== "object" || Array.isArray(outputs)) return 0;
  return Object.keys(outputs).length;
}

function runStageOutputs(stage) {
  const outputs = stage?.outputs ?? stage?.result?.outputs ?? null;
  return outputs && typeof outputs === "object" ? outputs : null;
}

function valueText(value) {
  if (value === null || value === undefined || value === "") return "";
  if (typeof value === "string") return value.trim();
  return String(value);
}

function evidenceMetaItemHTML(label, value) {
  return `<small><span>${escapeHTML(label)}</span><strong>${escapeHTML(localizedText(value))}</strong></small>`;
}

function workflowRunStageHasError(stage) {
  const status = String(stage?.status || "").toLowerCase();
  if (["failed", "error", "denied", "blocked", "cancelled", "canceled"].includes(status)) return true;
  return Array.isArray(stage?.result?.tool_results) && stage.result.tool_results.some(item => item?.is_error);
}

function truncateEvidenceText(value, limit) {
  const text = String(value || "").trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}...`;
}

function runDiffViewerHTML(diffs, options = {}) {
  const items = (Array.isArray(diffs) ? diffs : []).filter(Boolean);
  if (!items.length) return "";
  const maxDiffs = options.maxDiffs || 6;
  const visible = items.slice(0, maxDiffs);
  return `<section class="run-diff-viewer ${options.compact ? "compact" : ""}">
    <div class="run-diff-head">
      <div>
        <strong>${escapeHTML(t("chat.diffViewerTitle"))}</strong>
        <span>${escapeHTML(t("chat.diffViewerHelp"))}</span>
      </div>
      <small>${escapeHTML(t("chat.diffViewerCount", { count: items.length }))}</small>
    </div>
    ${runDiffSummaryHTML(items)}
    <div class="run-diff-list">
      ${visible.map(item => runDiffItemHTML(item, options)).join("")}
    </div>
    ${items.length > visible.length ? `<small class="run-diff-more">${escapeHTML(t("chat.diffViewerMore", { count: items.length - visible.length }))}</small>` : ""}
  </section>`;
}

function runDiffItemHTML(item, options = {}) {
  const patch = String(item.patch || item.diff_preview || "").trimEnd();
  const maxLines = options.maxLines || 120;
  const statusKey = diffStatusKey(item);
  const statText = diffStatText(item);
  const meta = [
    item.stage ? [t("chat.runHistoryStage"), localizedText(item.stage)] : null,
    statusKey ? [t("chat.diffStatus"), diffStatusLabel(statusKey)] : null,
    item.line_range ? [t("chat.diffLineRange"), item.line_range] : null,
    item.tool_name ? [t("chat.tool"), taskToolDisplayName(item.tool_name)] : null,
    item.change_id ? [t("chat.diffChangeID"), item.change_id] : null,
    item.reversible ? [t("chat.diffRecoveryScope"), item.recovery_scope || "file_only"] : null
  ].filter(Boolean);
  const detailKey = ["diff", item.path || "", item.stage || "", statusKey, statText].join("|");
  return `<details class="run-diff-item" data-detail-key="${escapeHTML(detailKey)}" ${options.compact ? "" : "open"}>
    <summary>
      <span>
        <strong>${escapeHTML(item.path || t("chat.diffUnknownPath"))}</strong>
        <small>${escapeHTML(item.summary || diffLineSummary(item))}</small>
      </span>
      <span class="run-diff-summary-actions">
        ${statusKey ? `<em class="run-diff-status ${escapeHTML(diffStatusTone(statusKey))}">${escapeHTML(diffStatusLabel(statusKey))}</em>` : ""}
        ${statText ? `<b>${escapeHTML(statText)}</b>` : ""}
      </span>
    </summary>
    ${meta.length ? `<div class="run-evidence-meta">${meta.map(([label, value]) => evidenceMetaItemHTML(label, value)).join("")}</div>` : ""}
    ${patch ? `<pre class="run-diff-code">${renderDiffLines(patch, maxLines)}</pre>` : `<p class="muted">${escapeHTML(t("chat.diffNoPreview"))}</p>`}
  </details>`;
}

function runDiffSummaryHTML(items) {
  const summary = diffSummary(items);
  const statusCards = summary.statuses.slice(0, 3).map(([status, count]) => `
    <article class="${escapeHTML(diffStatusTone(status))}">
      <small>${escapeHTML(t("chat.diffSummaryStatus"))}</small>
      <strong>${escapeHTML(diffStatusLabel(status))}</strong>
      <span>${escapeHTML(t("chat.diffSummaryStatusCount", { count }))}</span>
    </article>
  `).join("");
  return `<div class="run-diff-summary-grid">
    <article class="info">
      <small>${escapeHTML(t("chat.diffSummaryFiles"))}</small>
      <strong>${escapeHTML(String(summary.files))}</strong>
      <span>${escapeHTML(t("chat.diffSummaryFilesHelp"))}</span>
    </article>
    <article class="good">
      <small>${escapeHTML(t("chat.diffSummaryAdded"))}</small>
      <strong>${escapeHTML(`+${summary.added}`)}</strong>
      <span>${escapeHTML(t("chat.diffSummaryLines"))}</span>
    </article>
    <article class="bad">
      <small>${escapeHTML(t("chat.diffSummaryDeleted"))}</small>
      <strong>${escapeHTML(`-${summary.deleted}`)}</strong>
      <span>${escapeHTML(t("chat.diffSummaryLines"))}</span>
    </article>
    ${statusCards}
  </div>`;
}

function diffSummary(items) {
  const statusCounts = new Map();
  const paths = new Set();
  let added = 0;
  let deleted = 0;
  for (const item of items || []) {
    const path = String(item?.path || "").trim();
    if (path) paths.add(path);
    added += Number(item?.added_lines || 0) || 0;
    deleted += Number(item?.deleted_lines || 0) || 0;
    const status = diffStatusKey(item) || "modified";
    statusCounts.set(status, (statusCounts.get(status) || 0) + 1);
  }
  return {
    files: paths.size || (items || []).length,
    added,
    deleted,
    statuses: [...statusCounts.entries()].sort((a, b) => b[1] - a[1])
  };
}

function renderDiffLines(patch, maxLines) {
  const lines = String(patch || "").split(/\r?\n/);
  const visible = lines.slice(0, maxLines);
  const rendered = visible.map(line => `<span class="${escapeHTML(diffLineClass(line))}">${escapeHTML(line || " ")}</span>`).join("");
  const hidden = lines.length > visible.length
    ? `<span class="diff-meta">${escapeHTML(t("chat.diffViewerMoreLines", { count: lines.length - visible.length }))}</span>`
    : "";
  return `${rendered}${hidden}`;
}

function diffLineClass(line) {
  if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("@@") || line.startsWith("diff ")) return "diff-meta";
  if (line.startsWith("+")) return "diff-add";
  if (line.startsWith("-")) return "diff-del";
  return "diff-context";
}

function diffStatText(item) {
  const added = Number(item.added_lines || 0);
  const deleted = Number(item.deleted_lines || 0);
  if (!added && !deleted) return "";
  return `+${added} / -${deleted}`;
}

function diffLineSummary(item) {
  const parts = [
    diffStatusLabel(diffStatusKey(item)),
    item.bytes_written ? t("chat.diffBytesWritten", { count: item.bytes_written }) : ""
  ].filter(Boolean);
  return parts.join(" / ") || t("chat.diffNoSummary");
}

function diffStatusKey(item = {}) {
  const raw = String(item.status_code || item.status || item.action || item.kind || "").trim().toLowerCase();
  if (!raw) return "";
  if (["add", "added", "create", "created", "new", "write", "written"].includes(raw)) return "added";
  if (["delete", "deleted", "remove", "removed", "unlink"].includes(raw)) return "deleted";
  if (["rename", "renamed", "move", "moved"].includes(raw)) return "renamed";
  if (["modify", "modified", "update", "updated", "change", "changed", "edit", "edited"].includes(raw)) return "modified";
  return raw.replace(/[^a-z0-9_-]+/g, "_");
}

function diffStatusLabel(status) {
  const key = String(status || "").trim();
  if (!key) return "";
  const translated = t(`chat.diffStatus.${key}`);
  return translated === `chat.diffStatus.${key}` ? localizedText(key) : translated;
}

function diffStatusTone(status) {
  const key = String(status || "").trim();
  if (key === "added") return "good";
  if (key === "deleted") return "bad";
  if (key === "renamed") return "warn";
  return "info";
}

function lastCompletedStageName(run) {
  const stages = Array.isArray(run.completed_stages) ? run.completed_stages : [];
  return stages.length ? stages[stages.length - 1].stage || "" : "";
}

function isWorkflowRunActive(status) {
  return ["running", "cancelling"].includes(String(status || "").toLowerCase());
}

function isApprovalStatus(status) {
  return ["awaiting_approval", "awaiting_tool_approval"].includes(String(status || "").toLowerCase());
}

function readSavedRunState() {
  try {
    return JSON.parse(localStorage.getItem(runStateStorageKey) || "null");
  } catch {
    return null;
  }
}

function saveRunState(state, patch = {}) {
  try {
    const current = readSavedRunState() || {};
    const finalNode = finalResultNode(state);
    const previewNode = previewResultNode(state);
    const payload = {
      ...current,
      ...patch,
      mode: patch.mode || state.currentRunType || current.mode || "",
      runID: state.currentRunID || current.runID || "",
      workflowName: state.currentWorkflowName || current.workflowName || "",
      stage: state.currentStage || current.stage || "",
      status: patch.status || state.lastWorkflowStatus || current.status || "",
      timelineHint: state.runTimelineHint?.textContent || current.timelineHint || "",
      resultStatus: state.resultStatus?.textContent || current.resultStatus || "",
      resultRaw: finalNode?.dataset?.raw || "",
      previewRaw: previewNode?.dataset?.raw || "",
      eventsURL: patch.eventsURL || state.eventsURL || current.eventsURL || "",
      lastEventSeq: Number(patch.lastEventSeq ?? state.lastEventSeq ?? current.lastEventSeq ?? 0)
    };
    const signature = JSON.stringify(payload);
    if (signature === state.lastSavedRunStateSignature && !patch.force) return;
    state.lastSavedRunStateSignature = signature;
    localStorage.setItem(runStateStorageKey, JSON.stringify({
      ...payload,
      updatedAt: Date.now()
    }));
  } catch {
    // Ignore storage failures; live UI should keep working.
  }
}

function scheduleRunStateSave(state, patch = {}) {
  if (!state) return;
  state.pendingSavePatch = {
    ...(state.pendingSavePatch || {}),
    ...patch
  };
  if (state.pendingSaveFrame) return;
  state.pendingSaveFrame = requestAnimationFrame(() => {
    const nextPatch = state.pendingSavePatch || {};
    state.pendingSavePatch = null;
    state.pendingSaveFrame = 0;
    saveRunState(state, nextPatch);
  });
}

function flushRunStateSave(state) {
  if (!state?.pendingSaveFrame) return;
  cancelAnimationFrame(state.pendingSaveFrame);
  const nextPatch = state.pendingSavePatch || {};
  state.pendingSavePatch = null;
  state.pendingSaveFrame = 0;
  saveRunState(state, nextPatch);
}

function clearSavedRunState() {
  try {
    localStorage.removeItem(runStateStorageKey);
  } catch {
    // Ignore storage failures.
  }
}

function createCollaborationState(root, runtime, runState) {
  return {
    runtime,
    runState,
    status: root.querySelector("#collabStatus"),
    feedback: root.querySelector("#collabFeedback"),
    refresh: root.querySelector("#collabRefresh"),
    messageCount: root.querySelector("#collabMessageCount"),
    blackboardCount: root.querySelector("#collabBlackboardCount"),
    messages: root.querySelector("#collabMessages"),
    blackboard: root.querySelector("#collabBlackboard"),
    composer: root.querySelector("#collabComposer"),
    compose: {
      destination: root.querySelector("#collabRecordDestination"),
      kind: root.querySelector("#collabRecordKind"),
      subject: root.querySelector("#collabRecordSubject"),
      toAgent: root.querySelector("#collabRecordToAgent"),
      content: root.querySelector("#collabRecordContent"),
      save: root.querySelector("#collabRecordSave")
    },
    pendingRecordSave: false,
    pendingBlackboardAction: "",
    loaded: false,
    loading: false,
    stale: false,
    fields: {
      runID: root.querySelector("#collabRunId"),
      team: root.querySelector("#collabTeam"),
      stage: root.querySelector("#collabStage"),
      agent: root.querySelector("#collabAgent"),
      kind: root.querySelector("#collabKind"),
      scope: root.querySelector("#collabScope"),
      status: root.querySelector("#collabEntryStatus"),
      limit: root.querySelector("#collabLimit")
    }
  };
}

function bindCollaborationControls(state) {
  state.refresh?.addEventListener("click", () => {
    loadCollaborationData(state, true);
  });
  Object.values(state.fields).forEach(field => {
    field?.addEventListener("change", () => loadCollaborationData(state));
  });
  state.composer?.addEventListener("submit", event => {
    event.preventDefault();
    saveCollaborationRecord(state);
  });
  state.blackboard?.addEventListener("click", event => {
    const target = event.target instanceof Element ? event.target : event.target?.parentElement;
    const button = target?.closest("[data-blackboard-action]");
    if (!button) return;
    event.preventDefault();
    runBlackboardAction(state, button.dataset.blackboardId || "", button.dataset.blackboardAction || "");
  });
}

function bindCollaborationLazyLoad(panel, state) {
  if (!panel || !state) return;
  const load = () => {
    if (!panel.open || state.loaded || state.loading) return;
    loadCollaborationData(state).catch(() => {});
  };
  panel.addEventListener("toggle", load);
  load();
}

function resetCollaborationState(state, target) {
  if (state.fields.runID) state.fields.runID.value = state.runState.currentRunID || "";
  if (state.fields.stage) state.fields.stage.value = state.runState.currentStage || "";
  if (!state.fields.agent.value && state.runtime.active_agent) {
    state.fields.agent.value = target && target === state.runtime.active_agent ? state.runtime.active_agent : "";
  }
  if (state.feedback) state.feedback.textContent = "";
  setCollaborationStatus(state, t("chat.collabWaiting"));
  if (state.loaded) state.stale = true;
}

async function saveCollaborationRecord(state) {
  if (state.pendingRecordSave) return;
  const filters = collaborationFilters(state);
  const destination = state.compose.destination?.value || "blackboard";
  const kind = state.compose.kind?.value || "note";
  const subject = String(state.compose.subject?.value || "").trim();
  const content = String(state.compose.content?.value || "").trim();
  const toAgent = String(state.compose.toAgent?.value || "").trim();
  if (!subject && !content) {
    state.feedback.textContent = t("chat.collabRecordMissing");
    state.compose.subject?.focus();
    return;
  }
  state.pendingRecordSave = true;
  setCollaborationSavePending(state, true);
  state.feedback.textContent = t("chat.collabRecordSaving");
  try {
    if (destination === "message") {
      await createCollaborationMessage({
        run_id: filters.run_id || undefined,
        stage: filters.stage || undefined,
        from_agent: filters.agent || state.runtime.active_agent || "user",
        to_agent: toAgent || undefined,
        kind,
        subject,
        content
      });
    } else {
      await createCollaborationBlackboardEntry({
        scope: filters.scope || "workflow",
        run_id: filters.run_id || undefined,
        stage: filters.stage || undefined,
        agent_id: filters.agent || state.runtime.active_agent || "user",
        kind,
        title: subject,
        content,
        status: collaborationDefaultRecordStatus(kind),
        tags: filters.team ? [filters.team] : undefined,
        metadata: filters.team ? { team: filters.team } : undefined
      });
    }
    if (state.compose.subject) state.compose.subject.value = "";
    if (state.compose.content) state.compose.content.value = "";
    await loadCollaborationData(state);
    state.feedback.textContent = t("chat.collabRecordSaved");
  } catch (error) {
    state.feedback.textContent = chatDisplayText(error.message || t("chat.collabRecordFailed"));
  } finally {
    state.pendingRecordSave = false;
    setCollaborationSavePending(state, false);
  }
}

function setCollaborationSavePending(state, pending) {
  const form = state.compose.save?.closest?.("form");
  form?.setAttribute("aria-busy", pending ? "true" : "false");
  [
    state.compose.save,
    state.compose.destination,
    state.compose.kind,
    state.compose.subject,
    state.compose.toAgent,
    state.compose.content
  ].filter(Boolean).forEach(node => {
    node.disabled = pending;
    node.setAttribute("aria-disabled", pending ? "true" : "false");
  });
}

function collaborationDefaultRecordStatus(kind) {
  if (kind === "decision") return "resolved";
  if (kind === "note" || kind === "handoff") return "active";
  return "open";
}

async function runBlackboardAction(state, id, action) {
  if (!id || !action || state.pendingBlackboardAction) return;
  state.pendingBlackboardAction = `${id}:${action}`;
  setBlackboardActionPending(state, id, action, true);
  state.feedback.textContent = t("chat.collabActionSaving");
  try {
    await updateCollaborationBlackboardAction(id, action, {
      note: action === "resolve" ? t("chat.collabResolveNote") : t("chat.collabReopenNote")
    });
    await loadCollaborationData(state);
    state.feedback.textContent = t("chat.collabActionSaved");
  } catch (error) {
    state.feedback.textContent = chatDisplayText(error.message || t("chat.collabActionFailed"));
  } finally {
    state.pendingBlackboardAction = "";
    setBlackboardActionPending(state, id, action, false);
  }
}

function setBlackboardActionPending(state, id, action, pending) {
  for (const button of state.blackboard?.querySelectorAll("[data-blackboard-action]") || []) {
    if (button.dataset.blackboardId === id && button.dataset.blackboardAction === action) {
      button.disabled = pending;
      button.setAttribute("aria-disabled", pending ? "true" : "false");
      button.setAttribute("aria-busy", pending ? "true" : "false");
    }
  }
}

async function loadCollaborationData(state, manual = false) {
  if (!state) return;
  if (!manual && state.loading) return;
  if (!manual && state.loaded && !state.stale) return;
  const filters = collaborationFilters(state);
  const scrollState = captureCollaborationScrollState(state);
  if (manual) state.feedback.textContent = t("chat.collabRefreshing");
  setCollaborationStatus(state, t("chat.collabLoading"));
  state.loading = true;
  try {
    const [messages, blackboard, teamState] = await Promise.all([
      fetchCollaborationMessages(filters),
      fetchCollaborationBlackboard(filters),
      fetchTeamState(teamStateFilters(filters))
    ]);
    const normalizedTeamState = normalizeTeamState(teamState, normalizeCollection(blackboard));
    renderCollaborationMessages(state, normalizeTeamMessages(normalizedTeamState, normalizeCollection(messages)));
    renderTeamStatePanel(state, normalizedTeamState);
    restoreCollaborationScrollState(state, scrollState);
    const hasContext = Boolean(filters.run_id || filters.team || filters.stage || filters.agent || filters.kind || filters.scope || filters.status);
    setCollaborationStatus(state, hasContext ? t("chat.collabScoped") : t("chat.collabIdle"));
    state.feedback.textContent = manual ? t("chat.collabRefreshed") : "";
    state.loaded = true;
    state.stale = false;
  } catch (error) {
    const message = chatDisplayText(error.message || t("chat.collabLoadFailed"));
    state.messages.innerHTML = renderEmptyCard(t("chat.collabLoadFailed"), message);
    state.blackboard.innerHTML = renderEmptyCard(t("chat.collabLoadFailed"), message);
    state.messageCount.textContent = "0";
    state.blackboardCount.textContent = "0";
    setCollaborationStatus(state, t("chat.collabError"));
    state.feedback.textContent = message;
  } finally {
    state.loading = false;
  }
}

function captureCollaborationScrollState(state) {
  return {
    messagesTop: state.messages?.scrollTop || 0,
    blackboardTop: state.blackboard?.scrollTop || 0
  };
}

function restoreCollaborationScrollState(state, snapshot) {
  if (!snapshot) return;
  if (state.messages) {
    state.messages.scrollTop = Math.min(snapshot.messagesTop || 0, Math.max(0, state.messages.scrollHeight - state.messages.clientHeight));
  }
  if (state.blackboard) {
    state.blackboard.scrollTop = Math.min(snapshot.blackboardTop || 0, Math.max(0, state.blackboard.scrollHeight - state.blackboard.clientHeight));
  }
}

function collaborationFilters(state) {
  const limitValue = Number.parseInt(state.fields.limit.value, 10);
  return {
    run_id: state.fields.runID.value || state.runState.currentRunID || "",
    team: state.fields.team.value || "",
    stage: state.fields.stage.value || state.runState.currentStage || "",
    agent: state.fields.agent.value || "",
    kind: state.fields.kind.value || "",
    scope: state.fields.scope.value || "",
    status: state.fields.status.value || "",
    limit: Number.isFinite(limitValue) && limitValue > 0 ? limitValue : 12
  };
}

function teamStateFilters(filters) {
  return {
    run_id: filters.run_id || "",
    team: filters.team || ""
  };
}

function normalizeCollection(payload) {
  if (Array.isArray(payload)) return payload;
  if (Array.isArray(payload?.items)) return payload.items;
  if (Array.isArray(payload?.entries)) return payload.entries;
  if (Array.isArray(payload?.messages)) return payload.messages;
  return [];
}

function normalizeTeamMessages(teamState, fallbackMessages) {
  if (Array.isArray(teamState.messages) && teamState.messages.length) return teamState.messages;
  return fallbackMessages;
}

function normalizeTeamState(payload, fallbackBlackboard = []) {
  const state = payload && typeof payload === "object" ? payload : {};
  const result = {
    run_id: String(state.run_id || "").trim(),
    workflow: String(state.workflow || "").trim(),
    status: String(state.status || "").trim(),
    team: String(state.team || "").trim(),
    team_stage: String(state.team_stage || "").trim(),
    active_owner: String(state.active_owner || "").trim(),
    next_stage: String(state.next_stage || "").trim(),
    template: state.template && typeof state.template === "object" ? state.template : null,
    pending_input: Boolean(state.pending_input),
    messages: normalizeCollection(state.messages),
    handoffs: normalizeCollection(state.handoffs),
    blackboard: normalizeCollection(state.blackboard).length ? normalizeCollection(state.blackboard) : fallbackBlackboard,
    decisions: normalizeCollection(state.decisions),
    critiques: normalizeCollection(state.critiques),
    questions: normalizeCollection(state.questions),
    risks: normalizeCollection(state.risks),
    other_records: [],
    unresolved_items: normalizeCollection(state.unresolved_items),
    pending_approvals: normalizeCollection(state.pending_approvals)
  };
  if (!result.decisions.length && !result.critiques.length && !result.questions.length && !result.risks.length && fallbackBlackboard.length) {
    result.decisions = fallbackBlackboard.filter(item => teamStateKind(item, ["team_decision", "decision", "approval"]));
    result.critiques = fallbackBlackboard.filter(item => teamStateKind(item, ["team_critique", "critique", "finding", "findings"]));
    result.questions = fallbackBlackboard.filter(item => teamStateKind(item, ["team_unresolved_question", "question", "issue"]));
    result.risks = fallbackBlackboard.filter(item => teamStateKind(item, ["team_risk", "risk"]));
  }
  result.other_records = teamStateOtherRecords(result);
  return result;
}

function teamStateKind(item, kinds) {
  const kind = String(item?.kind || "").trim().toLowerCase();
  return kinds.includes(kind);
}

function teamStateOtherRecords(teamState) {
  const knownIDs = new Set(
    ["decisions", "critiques", "questions", "risks"]
      .flatMap(key => teamState[key] || [])
      .map(blackboardEntryID)
      .filter(Boolean)
  );
  const knownKinds = [
    "team_decision", "decision", "approval",
    "team_critique", "critique", "finding", "findings",
    "team_unresolved_question", "question", "issue",
    "team_risk", "risk"
  ];
  return (teamState.blackboard || []).filter(item => {
    const id = blackboardEntryID(item);
    if (id && knownIDs.has(id)) return false;
    return !teamStateKind(item, knownKinds);
  });
}

function renderCollaborationMessages(state, items) {
  state.messageCount.textContent = String(items.length);
  if (!items.length) {
    state.messages.innerHTML = renderEmptyCard(t("chat.collabTimelineEmpty"), t("chat.collabTimelineEmptyHelp"));
    return;
  }
  state.messages.innerHTML = items.map(renderCollaborationMessageCard).join("");
}

function renderTeamStatePanel(state, teamState) {
  const count = teamStateSignalCount(teamState);
  state.blackboardCount.textContent = String(count);
  if (!count && !teamState.team && !teamState.workflow && !teamState.active_owner) {
    state.blackboard.innerHTML = renderEmptyCard(t("chat.teamStateEmpty"), t("chat.teamStateEmptyHelp"));
    return;
  }
  state.blackboard.innerHTML = `
    ${renderTeamStateSummary(teamState)}
    ${renderTeamStateGroup(t("chat.teamDecisionTitle"), teamState.decisions, "decision")}
    ${renderTeamStateGroup(t("chat.teamCritiqueTitle"), teamState.critiques, "critique")}
    ${renderTeamStateGroup(t("chat.teamQuestionTitle"), teamState.questions, "question")}
    ${renderTeamStateGroup(t("chat.teamRiskTitle"), teamState.risks, "risk")}
    ${renderTeamStateGroup(t("chat.teamOtherRecordTitle"), teamState.other_records, "record")}
    ${renderTeamStateBadges(teamState)}`;
}

function teamStateSignalCount(teamState) {
  return ["decisions", "critiques", "questions", "risks", "other_records"].reduce((total, key) => total + (teamState[key]?.length || 0), 0);
}

function renderTeamStateSummary(teamState) {
  const facts = [
    teamState.team ? [t("chat.collabTeam"), localizedText(teamState.team)] : null,
    teamState.active_owner ? [t("chat.teamActiveOwner"), localizedText(teamState.active_owner)] : null,
    teamState.status ? [t("chat.runHistoryStatus"), workflowRunTimelineLabel(teamState.status)] : null,
    teamState.next_stage ? [t("chat.runHistoryStage"), localizedText(teamState.next_stage)] : null
  ].filter(Boolean);
  if (!facts.length) return "";
  return `<div class="team-state-summary">
    ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(value)}</strong></span>`).join("")}
  </div>`;
}

function renderTeamStateBadges(teamState) {
  const badges = [];
  if (teamState.pending_input) badges.push(t("chat.teamPendingInput"));
  if (teamState.pending_approvals?.length) badges.push(t("chat.teamPendingApprovals", { count: teamState.pending_approvals.length }));
  if (teamState.handoffs?.length) badges.push(t("chat.teamHandoffs", { count: teamState.handoffs.length }));
  if (teamState.unresolved_items?.length) badges.push(t("chat.teamUnresolved", { count: teamState.unresolved_items.length }));
  if (!badges.length) return "";
  return `<div class="collab-tags team-state-tags">${badges.map(item => `<span>${escapeHTML(item)}</span>`).join("")}</div>`;
}

function renderTeamStateGroup(title, items, tone) {
  if (!items.length) return "";
  return `<section class="team-state-group ${tone}">
    <div class="team-state-group-head">
      <strong>${escapeHTML(title)}</strong>
      <small>${items.length}</small>
    </div>
    ${items.map(item => renderTeamStateItem(item, tone)).join("")}
  </section>`;
}

function renderTeamStateItem(item, tone) {
  const meta = [
    item.stage ? `${t("chat.collabStage")}: ${escapeHTML(localizedText(item.stage))}` : "",
    item.agent_id ? `${t("chat.collabAgent")}: ${escapeHTML(localizedText(item.agent_id))}` : "",
    item.status ? `${t("chat.runHistoryStatus")}: ${escapeHTML(localizedText(item.status))}` : ""
  ].filter(Boolean).join(" / ");
  const id = blackboardEntryID(item);
  const action = id ? blackboardItemAction(item) : null;
  const title = localizedText(item.title || item.subject || teamStateFallbackTitle(tone));
  const content = localizedText(item.content || item.summary || t("chat.collabNoContent"));
  return `<article class="team-state-item ${tone}">
    <div>
      <strong>${escapeHTML(title)}</strong>
      ${meta ? `<small>${meta}</small>` : ""}
    </div>
    <div class="run-markdown collab-markdown">${renderMarkdown(content)}</div>
    ${action ? `<div class="collab-inline-actions"><button type="button" data-blackboard-id="${escapeHTML(id)}" data-blackboard-action="${escapeHTML(action.name)}">${escapeHTML(action.label)}</button></div>` : ""}
  </article>`;
}

function blackboardEntryID(item) {
  return String(item?.id || item?.ID || "").trim();
}

function blackboardItemAction(item) {
  const status = String(item?.status || "").trim().toLowerCase();
  if (status === "resolved") return { name: "reopen", label: t("chat.collabReopen") };
  return { name: "resolve", label: t("chat.collabResolve") };
}

function teamStateFallbackTitle(tone) {
  if (tone === "decision") return t("chat.teamDecisionTitle");
  if (tone === "critique") return t("chat.teamCritiqueTitle");
  if (tone === "question") return t("chat.teamQuestionTitle");
  if (tone === "risk") return t("chat.teamRiskTitle");
  if (tone === "record") return t("chat.teamOtherRecordTitle");
  return t("chat.teamStateTitle");
}

function renderCollaborationMessageCard(item) {
  const meta = [
    item.stage ? `${t("chat.collabStage")}: ${escapeHTML(localizedText(item.stage))}` : "",
    item.kind ? `${t("chat.collabKind")}: ${escapeHTML(collabKindLabel(item.kind))}` : "",
    item.run_id ? `${t("chat.collabRunId")}: ${escapeHTML(item.run_id)}` : ""
  ].filter(Boolean).join(" / ");
  const route = [item.from_agent || "?", item.to_agent || t("chat.collabBroadcast")].join(" -> ");
  const subject = localizedText(item.subject || route);
  const content = localizedText(item.content || t("chat.collabNoContent"));
  return `<article class="collab-entry message-entry">
    <div class="collab-entry-head">
      <strong>${escapeHTML(subject)}</strong>
      <span class="badge">${escapeHTML(item.at || "-")}</span>
    </div>
    <p class="collab-entry-route">${escapeHTML(route)}</p>
    ${meta ? `<small>${meta}</small>` : ""}
    <div class="run-markdown collab-markdown">${renderMarkdown(content)}</div>
  </article>`;
}

function renderEmptyCard(title, body) {
  return `<div class="item muted collab-empty"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(body)}</span></div>`;
}

function collabKindLabel(kind) {
  const value = String(kind || "").trim().toLowerCase();
  const labels = {
    decision: t("chat.collabRecordDecision"),
    risk: t("chat.collabRecordRisk"),
    question: t("chat.collabRecordQuestion"),
    note: t("chat.collabRecordNote"),
    handoff: t("chat.collabRecordHandoff"),
    critique: t("chat.teamCritiqueTitle")
  };
  return labels[value] || kind || "-";
}

function setCollaborationStatus(state, text) {
  if (state.status) state.status.textContent = text;
}

function markCollaborationStale(state) {
  if (!state) return;
  state.stale = true;
  if (state.status && !state.loading) state.status.textContent = t("chat.collabStale");
}

function detectRunContext(event, runState, collaborationState) {
  const previousRunID = runState.currentRunID;
  const previousStage = runState.currentStage;
  if (event.run_id) runState.currentRunID = event.run_id;
  if (event.task_stage) runState.currentStage = event.task_stage;
  if (event.stage) runState.currentStage = event.stage;
  if (collaborationState) {
    if (collaborationState.fields.runID && !collaborationState.fields.runID.value && runState.currentRunID && runState.currentRunID !== previousRunID) {
      collaborationState.fields.runID.value = runState.currentRunID;
    }
    if (collaborationState.fields.stage && !collaborationState.fields.stage.value && runState.currentStage && runState.currentStage !== previousStage) {
      collaborationState.fields.stage.value = runState.currentStage;
    }
  }
  return previousRunID !== runState.currentRunID || previousStage !== runState.currentStage;
}

function resetRunState(state) {
  state.messages.innerHTML = "";
  state.timelinePinnedToLatest = true;
  state.timelineSectionVisible = true;
  state.timelineUnseenEvents = 0;
  state.tokenTotals = emptyTimelineTokenTotals();
  state.tokenSeen = new Set();
  state.lastPromptEstimate = 0;
  setTimelineJumpLatestVisible(state, false);
  renderTimelineRunTotals(state);
  state.resultPreview.innerHTML = "";
  state.resultPreview.classList.add("hidden");
  state.resultOutput.innerHTML = "";
  state.resultOutput.classList.add("hidden");
  state.resultEmpty.classList.remove("hidden");
  state.resultPanel?.classList.add("hidden");
  state.runAttention.innerHTML = "";
  state.runAttention.classList.add("hidden");
  state.hasResult = false;
  state.hasError = false;
  state.awaitingApproval = false;
  state.awaitingInput = false;
  state.inputRequest = null;
  state.currentRunType = "";
  state.currentWorkflowName = "";
  state.previewNode = null;
  state.finalNode = null;
  state.hasPreview = false;
  state.currentRunID = "";
  state.currentStage = "";
  state.lastWorkflowStatus = "";
  state.lastSnapshotKey = "";
  state.eventsURL = "";
  state.lastEventSeq = 0;
  state.durableWorkflowActive = false;
  state.syncContextGuide?.();
}

async function handleWorkflowResponse(response, messages, runState, collaborationState, workflowName, resumed) {
  const normalized = normalizeWorkflowResponse(response, workflowName);
  if (normalized.runID) runState.currentRunID = normalized.runID;
  if (normalized.stage) runState.currentStage = normalized.stage;
  runState.currentWorkflowName = normalized.workflowName || workflowName || runState.currentWorkflowName;
  syncCollaborationContext(runState, collaborationState);

  if (normalized.error) {
    appendTimelineEvent(messages, {
      tone: "error",
      title: t("chat.errorTitle"),
      detail: normalized.error
    });
    showRunAttention(runState, t("chat.errorTitle"), normalized.error, "error");
    setTimelineHint(runState, t("chat.timelineError"));
    setResultStatus(runState, t("chat.resultError"));
    if (collaborationState) setCollaborationStatus(collaborationState, t("chat.collabError"));
    runState.hasError = true;
    return;
  }

  if (normalized.output) {
    replaceResult(runState, normalized.output);
    setResultStatus(runState, t("chat.resultReady"));
  }

  if (normalized.awaitingInput) {
    runState.awaitingInput = true;
    runState.awaitingApproval = false;
    runState.inputRequest = normalized.inputRequest;
    appendTimelineEvent(messages, {
      tone: "approval",
      title: t("chat.awaitingInputTitle"),
      detail: workflowInputSummary(normalized.inputRequest)
    });
    renderWorkflowInputAttention(runState, normalized.inputRequest);
    setTimelineHint(runState, t("chat.timelineInput"));
    setResultStatus(runState, normalized.output ? t("chat.resultReady") : t("chat.resultAwaitingInput"));
    if (collaborationState) setCollaborationStatus(collaborationState, t("chat.collabScoped"));
    return;
  }

  runState.awaitingInput = false;
  runState.inputRequest = null;
  clearRunAttention(runState);

  const completed = normalized.completed || Boolean(normalized.output);
  if (completed) {
    appendTimelineEvent(messages, {
      tone: "success",
      title: resumed ? t("chat.resumeRunDone") : t("chat.workflowComplete"),
      detail: timelineDetail([normalized.workflowName, workflowRunStatusLabel(normalized.statusLabel)])
    });
  } else if (normalized.statusLabel) {
    appendTimelineEvent(messages, {
      tone: "stage",
      title: t("chat.stageProgressTitle"),
      detail: timelineDetail([normalized.workflowName, workflowRunStatusLabel(normalized.statusLabel)])
    });
  }
  if (completed) {
    setTimelineHint(runState, t("chat.timelineDone"));
    setResultStatus(runState, runState.hasResult ? t("chat.resultReady") : t("chat.resultIdle"));
    if (collaborationState) setCollaborationStatus(collaborationState, t("chat.collabScoped"));
  }
}

async function handleWorkflowBackgroundResponse(response, messages, runState, collaborationState, workflowName, actionName = "") {
  const payload = response && typeof response === "object" ? response : {};
  const runID = String(payload.run_id || payload.runID || payload.id || payload.run?.id || runState.currentRunID || "").trim();
  const name = String(payload.name || payload.workflow_name || payload.run?.name || workflowName || runState.currentWorkflowName || "").trim();
  if (runID) runState.currentRunID = runID;
  if (name) runState.currentWorkflowName = name;
  runState.currentRunType = "workflow";
  runState.eventsURL = runEventsURLFromActionResponse(payload) || runState.eventsURL || (runID ? workflowRunEventsURL(runID) : "");
  runState.lastWorkflowStatus = payload.status || payload.run?.status || runState.lastWorkflowStatus || "running";
  runState.durableWorkflowActive = true;
  syncCollaborationContext(runState, collaborationState);
  const run = payload.run && typeof payload.run === "object" ? payload.run : null;
  if (run?.id) {
    await renderWorkflowRunSnapshot(runState, collaborationState, run);
  } else {
    appendTimelineEvent(messages, {
      tone: "stage",
      title: actionName === "start" ? t("chat.workflowBackgroundStarted") : t("chat.workflowBackgroundActionStarted"),
      detail: [name, runID].filter(Boolean).join(" / ")
    });
    setTimelineHint(runState, t("chat.timelineRunning"));
    setResultStatus(runState, t("chat.resultStreaming"));
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  }
  saveRunState(runState, {
    running: true,
    status: runState.lastWorkflowStatus || "running",
    eventsURL: runState.eventsURL,
    lastEventSeq: runState.lastEventSeq || workflowRunLatestEventSeq(run)
  });
}

async function handleAgentBackgroundResponse(response, messages, runState, collaborationState, agentName, actionName = "") {
  const payload = response && typeof response === "object" ? response : {};
  const runID = String(payload.run_id || payload.runID || payload.id || payload.run?.id || runState.currentRunID || "").trim();
  if (runID) runState.currentRunID = runID;
  runState.currentRunType = "agent";
  runState.currentWorkflowName = "";
  runState.currentStage = String(payload.run?.agent_id || agentName || runState.currentStage || "").trim();
  runState.eventsURL = runEventsURLFromActionResponse(payload) || runState.eventsURL || (runID ? agentRunEventsURL(runID) : "");
  runState.lastWorkflowStatus = payload.status || payload.run?.status || runState.lastWorkflowStatus || "running";
  runState.durableWorkflowActive = true;
  syncCollaborationContext(runState, collaborationState);
  const run = payload.run && typeof payload.run === "object" ? payload.run : null;
  if (run?.id) {
    await renderAgentRunSnapshot(runState, collaborationState, run);
  } else {
    appendTimelineEvent(messages, {
      tone: "stage",
      title: actionName === "start" ? t("chat.agentBackgroundStarted") : t("chat.agentBackgroundActionStarted"),
      detail: [agentName, runID].filter(Boolean).join(" / ")
    });
    setTimelineHint(runState, t("chat.timelineRunning"));
    setResultStatus(runState, t("chat.resultStreaming"));
    setRunLiveStatus(runState, t("chat.realtimeSyncing"), "syncing");
  }
  saveRunState(runState, {
    mode: "agent",
    running: true,
    status: runState.lastWorkflowStatus || "running",
    eventsURL: runState.eventsURL,
    lastEventSeq: runState.lastEventSeq || agentRunLatestEventSeq(run)
  });
}

function normalizeWorkflowResponse(response, workflowName) {
  const payload = response && typeof response === "object" ? response : {};
  const result = payload.result && typeof payload.result === "object" ? payload.result : {};
  const status = String(
    payload.status ||
    payload.workflow_status ||
    result.status ||
    result.workflow_status ||
    ""
  ).trim();
  const pendingFields = normalizeWorkflowInputFields(
    payload.pending_input_fields ||
    payload.input_fields ||
    result.pending_input_fields ||
    result.input_fields ||
    []
  );
  const runID = String(payload.run_id || payload.runID || result.run_id || result.runID || payload.id || "").trim();
  const stage = String(
    payload.current_stage ||
    payload.stage ||
    payload.task_stage ||
    result.current_stage ||
    result.stage ||
    ""
  ).trim();
  const error = extractWorkflowError(payload, result);
  const output = extractWorkflowOutput(payload, result);
  const workflow = String(
    payload.workflow_name ||
    result.workflow_name ||
    workflowName ||
    ""
  ).trim();
  const awaitingInput = status === "awaiting_input" || pendingFields.length > 0;
  const completed = ["completed", "complete", "done", "success", "succeeded", "finished"].includes(status.toLowerCase()) || Boolean(output && !awaitingInput && !error);
  return {
    runID,
    stage,
    error,
    output,
    awaitingInput,
    completed,
    workflowName: workflow,
    statusLabel: status || (awaitingInput ? "awaiting_input" : completed ? "completed" : ""),
    inputRequest: {
      runID,
      stage,
      workflowName: workflow,
      status,
      fields: pendingFields,
      summary: String(payload.summary || result.summary || "").trim(),
      detail: String(payload.detail || result.detail || payload.message || result.message || "").trim()
    }
  };
}

function extractWorkflowError(payload, result) {
  const payloadStatus = String(payload?.status || payload?.workflow_status || "").toLowerCase();
  const resultStatus = String(result?.status || result?.workflow_status || "").toLowerCase();

  if (payload?.error) return localizedText(String(payload.error).trim());
  if (payload?.message && /error|failed/.test(payloadStatus)) return localizedText(String(payload.message).trim());
  if (result?.error) return localizedText(String(result.error).trim());
  if (result?.message && /error|failed/.test(resultStatus)) return localizedText(String(result.message).trim());

  return "";
}

function extractWorkflowOutput(payload, result) {
  const direct = [
    payload.final_message,
    payload.output,
    payload.content,
    payload.message,
    result.final_message,
    result.output,
    result.content,
    result.message
  ];
  for (const value of direct) {
    const normalized = publicResultText(formatStructuredValue(value));
    if (normalized) return normalized;
  }
  const structured = [payload.outputs, result.outputs, payload.response, result.response];
  for (const value of structured) {
    const normalized = publicResultText(formatStructuredValue(value, true));
    if (normalized) return normalized;
  }
  return "";
}

function formatStructuredValue(value, allowObjects = false) {
  if (value == null) return "";
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (allowObjects && typeof value === "object") {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return "";
    }
  }
  return "";
}

function normalizeWorkflowInputFields(fields) {
  if (!Array.isArray(fields)) return [];
  return fields
    .map(field => normalizeWorkflowInputField(field))
    .filter(Boolean);
}

function normalizeWorkflowInputField(field) {
  if (!field) return null;
  if (typeof field === "string") {
    const name = field.trim();
    return name ? {
      name,
      type: "string",
      label: name,
      description: "",
      placeholder: "",
      required: false,
      defaultValue: "",
      options: [],
      rows: 4,
      min: "",
      max: "",
      step: "",
      pattern: "",
      multiple: false,
      advanced: false,
      group: ""
    } : null;
  }
  if (typeof field !== "object") return null;
  const name = String(field.name || field.key || field.id || "").trim();
  if (!name) return null;
  const options = normalizeWorkflowInputOptions(field.options || field.enum || field.values || []);
  const type = normalizeWorkflowInputType(field.type || field.kind || field.format || "string", options);
  return {
    name,
    type,
    label: String((currentLanguage() === "zh" ? (field.label_zh || field.labelZH) : "") || field.label || field.title || name).trim(),
    description: String((currentLanguage() === "zh" ? (field.description_zh || field.descriptionZH) : "") || field.description || field.help || "").trim(),
    placeholder: String(field.placeholder || "").trim(),
    required: Boolean(field.required),
    defaultValue: field.default ?? field.default_value ?? "",
    options,
    rows: Number.parseInt(field.rows, 10) || (["json", "array", "object"].includes(type) ? 6 : 4),
    min: field.min ?? field.minimum ?? "",
    max: field.max ?? field.maximum ?? "",
    step: field.step ?? "",
    pattern: String(field.pattern || "").trim(),
    multiple: Boolean(field.multiple),
    advanced: Boolean(field.advanced),
    group: String(field.group || "").trim()
  };
}

function normalizeWorkflowInputOptions(options) {
  if (!Array.isArray(options)) return [];
  return options.map(option => {
    if (option && typeof option === "object") {
      const rawValue = option.value ?? option.id ?? option.name ?? option.label;
      const value = rawValue == null ? "" : String(rawValue).trim();
      const label = String(option.label ?? option.name ?? value).trim();
      return value ? { value, label: label || value } : null;
    }
    const value = String(option ?? "").trim();
    return value ? { value, label: value } : null;
  }).filter(Boolean);
}

function normalizeWorkflowInputType(type, options = []) {
  const normalized = String(type || "string").trim().toLowerCase();
  if (normalized === "enum" || options.length) return "select";
  if (normalized === "multiline") return "textarea";
  if (normalized === "int") return "integer";
  if (normalized === "float" || normalized === "double") return "number";
  if (normalized === "bool" || normalized === "checkbox") return "boolean";
  if (normalized === "datetime-local") return "datetime";
  if (["string", "text", "textarea", "number", "integer", "select", "url", "path", "file", "json", "password", "email", "date", "time", "datetime", "hidden", "boolean", "array", "object"].includes(normalized)) {
    return normalized;
  }
  return "string";
}

function renderWorkflowInputAttention(state, request) {
  const fields = request?.fields || [];
  const title = t("chat.awaitingInputTitle");
  const detail = request?.detail || t("chat.awaitingInputHelp");
  const summary = workflowInputSummary(request);
  setRunTimelineWorkflowInput(state, true);
  state.runAttention.className = "run-attention workflow-input";
  state.runAttention.innerHTML = `
    <div class="run-attention-head">
      <div>
        <strong>${escapeHTML(title)}</strong>
        <p>${escapeHTML(localizedText(detail))}</p>
      </div>
      <span class="badge warn">${escapeHTML(summary)}</span>
    </div>
    <form class="workflow-input-form" data-workflow-input-form novalidate>
      <div class="workflow-input-grid">
        ${renderWorkflowInputFields(fields)}
      </div>
      <div class="workflow-input-error hidden" data-workflow-input-error></div>
      <div class="workflow-input-actions">
        <span class="muted">${escapeHTML(t("chat.awaitingInputDetail"))}</span>
        <button type="submit" class="primary" data-workflow-input-submit>${escapeHTML(t("chat.submitWorkflowInput"))}</button>
      </div>
    </form>`;
  state.runAttention.classList.remove("hidden");
  const form = state.runAttention.querySelector("[data-workflow-input-form]");
  form?.addEventListener("submit", event => {
    event.preventDefault();
    state.submitWorkflowInput?.(request);
  });
}

function setRunTimelineWorkflowInput(state, enabled) {
  const timeline = state?.root?.querySelector(".run-timeline");
  if (!timeline) return;
  timeline.classList.toggle("has-workflow-input", Boolean(enabled));
  if (enabled) {
    timeline.style.removeProperty("--run-setup-height");
  }
}

function renderWorkflowInputFields(fields) {
  const hidden = fields.filter(field => field.type === "hidden").map(renderWorkflowInputField).join("");
  const primaryFields = fields.filter(field => field.type !== "hidden" && !field.advanced);
  const advancedFields = fields.filter(field => field.type !== "hidden" && field.advanced);
  const primary = renderWorkflowInputFieldGroups(primaryFields);
  const advanced = advancedFields.length
    ? `<details class="workflow-input-advanced"><summary><span>${escapeHTML(t("chat.workflowInputAdvanced"))}</span><small>${escapeHTML(t("chat.workflowInputAdvancedHelp"))}</small></summary>${renderWorkflowInputFieldGroups(advancedFields)}</details>`
    : "";
  return `${hidden}${primary}${advanced}`;
}

function renderWorkflowInputFieldGroups(fields) {
  if (!fields.length) return "";
  const groups = [];
  const byGroup = new Map();
  for (const field of fields) {
    const group = field.group || "";
    if (!byGroup.has(group)) {
      byGroup.set(group, []);
      groups.push(group);
    }
    byGroup.get(group).push(field);
  }
  return groups.map(group => {
    const body = byGroup.get(group).map(renderWorkflowInputField).join("");
    if (!group) return body;
    return `
      <section class="workflow-input-group">
        <div class="workflow-input-group-head">
          <strong>${escapeHTML(group)}</strong>
          <span>${escapeHTML(t("chat.workflowInputGroupCount", { count: byGroup.get(group).length }))}</span>
        </div>
        <div>${body}</div>
      </section>`;
  }).join("");
}

function renderWorkflowInputField(field) {
  const name = escapeHTML(field.name);
  const label = escapeHTML(localizedText(field.label || field.name));
  const description = field.description ? `<small>${escapeHTML(localizedText(field.description))}</small>` : "";
  const required = field.required ? `<em>${escapeHTML(t("common.required"))}</em>` : `<em>${escapeHTML(t("common.optional"))}</em>`;
  const value = workflowInputDefaultValue(field);
  const meta = workflowInputFieldMeta(field);
  if (field.type === "hidden") {
    return `<input type="hidden" value="${escapeHTML(String(value || ""))}" data-field-name="${name}" data-field-type="hidden">`;
  }
  const attrs = workflowInputFieldAttrs(field);
  if (field.type === "select") {
    const multiple = field.multiple ? " multiple" : "";
    const selectedValues = workflowInputSelectedValues(field, value);
    const options = [
      field.required ? "" : `<option value="">${escapeHTML(t("common.none"))}</option>`,
      ...field.options.map(option => `<option value="${escapeHTML(option.value)}"${selectedValues.has(option.value) ? " selected" : ""}>${escapeHTML(localizedText(option.label || option.value))}</option>`)
    ].filter(Boolean).join("");
    return `<label class="workflow-input-field"><span class="workflow-input-field-title"><span>${label}</span>${required}</span>${description}<select data-field-name="${name}" data-field-type="select"${multiple}${attrs}>${options}</select>${meta}</label>`;
  }
  if (["text", "textarea", "json", "array", "object"].includes(field.type)) {
    const dataType = ["json", "array", "object"].includes(field.type) ? field.type : "textarea";
    return `<label class="workflow-input-field workflow-input-field-wide"><span class="workflow-input-field-title"><span>${label}</span>${required}</span>${description}<textarea rows="${field.rows}" placeholder="${escapeHTML(localizedText(field.placeholder || workflowInputPlaceholder(field)))}" data-field-name="${name}" data-field-type="${escapeHTML(dataType)}"${attrs}>${escapeHTML(String(value || ""))}</textarea>${meta}</label>`;
  }
  if (field.type === "boolean") {
    const checked = value === true || String(value).toLowerCase() === "true";
    return `<label class="workflow-input-toggle"><input type="checkbox" data-field-name="${name}" data-field-type="boolean"${checked ? " checked" : ""}><span><strong>${label}</strong>${description || ""}${meta}</span></label>`;
  }
  const inputType = workflowInputHTMLType(field.type);
  return `<label class="workflow-input-field"><span class="workflow-input-field-title"><span>${label}</span>${required}</span>${description}<input type="${inputType}" placeholder="${escapeHTML(localizedText(field.placeholder || ""))}" value="${escapeHTML(String(value || ""))}" data-field-name="${name}" data-field-type="${escapeHTML(field.type || "string")}"${attrs}>${meta}</label>`;
}

function workflowInputDefaultValue(field) {
  const value = field.defaultValue ?? "";
  if (value && typeof value === "object") {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return "";
    }
  }
  return value;
}

function workflowInputSelectedValues(field, value) {
  if (Array.isArray(value)) return new Set(value.map(item => String(item)));
  const normalized = String(value ?? "");
  if (field.multiple) {
    return new Set(normalized.split(",").map(item => item.trim()).filter(Boolean));
  }
  return new Set(normalized ? [normalized] : []);
}

function workflowInputPlaceholder(field) {
  if (field.type === "array") return "[\n  \"item\"\n]";
  if (field.type === "object" || field.type === "json") return "{\n  \"key\": \"value\"\n}";
  return "";
}

function workflowInputFieldMeta(field) {
  const chips = [workflowInputTypeLabel(field)];
  if (field.multiple) chips.push(t("chat.workflowInputMetaMultiple"));
  if (field.min !== "" || field.max !== "") {
    chips.push(t("chat.workflowInputMetaRange", {
      min: field.min !== "" ? field.min : "-",
      max: field.max !== "" ? field.max : "-"
    }));
  }
  if (field.pattern) chips.push(t("chat.workflowInputMetaPattern"));
  return `<span class="workflow-input-meta">${chips.map(chip => `<span>${escapeHTML(chip)}</span>`).join("")}</span>`;
}

function workflowInputTypeLabel(field) {
  const type = field.multiple ? "multiple" : field.type || "string";
  return t(`chat.workflowInputType.${type}`);
}

function workflowInputHTMLType(type) {
  if (type === "number" || type === "integer") return "number";
  if (["url", "password", "email", "date", "time"].includes(type)) return type;
  if (type === "datetime") return "datetime-local";
  if (type === "file" || type === "path") return "text";
  return "text";
}

function workflowInputFieldAttrs(field) {
  const attrs = [];
  if (field.required) attrs.push("required");
  if (field.type === "number" || field.type === "integer") {
    if (field.min !== "") attrs.push(`min="${escapeHTML(field.min)}"`);
    if (field.max !== "") attrs.push(`max="${escapeHTML(field.max)}"`);
    if (field.step !== "") attrs.push(`step="${escapeHTML(field.step)}"`);
    if (field.type === "integer" && field.step === "") attrs.push('step="1"');
  } else {
    if (isIntegerString(field.min)) attrs.push(`minlength="${escapeHTML(field.min)}"`);
    if (isIntegerString(field.max)) attrs.push(`maxlength="${escapeHTML(field.max)}"`);
    if (field.pattern) attrs.push(`pattern="${escapeHTML(field.pattern)}"`);
  }
  return attrs.length ? ` ${attrs.join(" ")}` : "";
}

function isIntegerString(value) {
  return /^-?\d+$/.test(String(value ?? "").trim());
}

function collectWorkflowInputValues(container, fields) {
  const values = {};
  container.querySelectorAll(".workflow-input-invalid").forEach(node => node.classList.remove("workflow-input-invalid"));
  for (const field of fields || []) {
    const node = container.querySelector(`[data-field-name="${cssEscape(field.name)}"]`);
    if (!node) continue;
    const isBoolean = node.dataset.fieldType === "boolean";
    const isMultiple = node.multiple;
    let value;
    if (isBoolean) {
      value = Boolean(node.checked);
    } else if (isMultiple) {
      value = Array.from(node.selectedOptions || []).map(option => option.value).filter(Boolean);
    } else {
      value = String(node.value ?? "").trim();
    }
    if (field.required) {
      const missing = isBoolean ? false : Array.isArray(value) ? value.length === 0 : !value;
      if (missing) {
        markWorkflowInputInvalid(node);
        throw new Error(`${field.label || field.name} ${t("chat.workflowInputRequiredSuffix")}`);
      }
    }
    if (!isBoolean && value !== "" && typeof node.checkValidity === "function" && !node.checkValidity()) {
      markWorkflowInputInvalid(node);
      throw new Error(`${field.label || field.name} ${t("chat.workflowInputInvalidSuffix")}`);
    }
    if (!isBoolean && value !== "" && field.pattern && !workflowInputPatternMatches(field.pattern, value)) {
      markWorkflowInputInvalid(node);
      throw new Error(`${field.label || field.name} ${t("chat.workflowInputInvalidSuffix")}`);
    }
    if (!isBoolean && !Array.isArray(value) && value === "" && !field.required) continue;
    if (Array.isArray(value) && !value.length && !field.required) continue;
    try {
      values[field.name] = coerceWorkflowInputValue(field, value);
    } catch (error) {
      markWorkflowInputInvalid(node);
      throw error;
    }
  }
  return values;
}

function coerceWorkflowInputValue(field, value) {
  if (value == null) return value;
  if (field.type === "boolean") return Boolean(value);
  if (field.multiple || Array.isArray(value)) return value;
  if (field.type === "number") {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : value;
  }
  if (field.type === "integer") {
    const parsed = Number.parseInt(value, 10);
    return Number.isFinite(parsed) ? parsed : value;
  }
  if (["json", "array", "object"].includes(field.type)) {
    try {
      const parsed = JSON.parse(value);
      if (field.type === "array" && !Array.isArray(parsed)) throw new Error(t("chat.workflowInputJSONInvalid"));
      if (field.type === "object" && (!parsed || Array.isArray(parsed) || typeof parsed !== "object")) throw new Error(t("chat.workflowInputJSONInvalid"));
      return parsed;
    } catch (error) {
      throw new Error(`${field.label || field.name} ${t("chat.workflowInputJSONInvalid")}`);
    }
  }
  return value;
}

function markWorkflowInputInvalid(node) {
  const field = node.closest(".workflow-input-field, .workflow-input-toggle");
  field?.classList.add("workflow-input-invalid");
  if (typeof node.focus === "function") node.focus();
}

function workflowInputPatternMatches(pattern, value) {
  try {
    return new RegExp(pattern).test(String(value ?? ""));
  } catch {
    return true;
  }
}

function workflowInputSummary(request) {
  const parts = [];
  if (request?.workflowName) parts.push(request.workflowName);
  if (request?.stage) parts.push(request.stage);
  const count = Array.isArray(request?.fields) ? request.fields.length : 0;
  if (count) parts.push(t("chat.workflowInputCount", { count }));
  return parts.join(" / ") || t("chat.awaitingInputDetail");
}

function setWorkflowInputSubmitting(state, submitting) {
  const form = state.runAttention.querySelector("[data-workflow-input-form]");
  if (!form) return;
  form.setAttribute("aria-busy", submitting ? "true" : "false");
  form.querySelectorAll("input, select, textarea, button").forEach(node => {
    node.disabled = submitting;
    node.setAttribute("aria-disabled", submitting ? "true" : "false");
  });
  const button = form.querySelector("[data-workflow-input-submit]");
  if (button) {
    button.setAttribute("aria-busy", submitting ? "true" : "false");
    button.textContent = submitting ? t("common.loading") : t("chat.submitWorkflowInput");
  }
}

function showWorkflowInputError(state, message) {
  const error = state.runAttention.querySelector("[data-workflow-input-error]");
  if (!error) return;
  error.textContent = message;
  error.classList.remove("hidden");
}

function showWorkflowInputWorkspaceRequirement(state, message) {
  const error = state.runAttention.querySelector("[data-workflow-input-error]");
  if (!error) return;
  error.innerHTML = `
    <strong>${escapeHTML(t("chat.workspacePreflightTitle"))}</strong>
    <span>${escapeHTML(message)}</span>
    <button type="button" data-open-workspace>${escapeHTML(t("chat.workspacePreflightAction"))}</button>`;
  error.classList.remove("hidden");
  error.querySelector("[data-open-workspace]")?.addEventListener("click", () => {
    location.hash = "workspace";
  });
}

function clearRunAttention(state) {
  setRunTimelineWorkflowInput(state, false);
  state.runAttention.innerHTML = "";
  state.runAttention.className = "run-attention hidden";
  state.syncContextGuide?.();
}

function syncCollaborationContext(runState, collaborationState) {
  if (!collaborationState) return;
  if (collaborationState.fields.runID && runState.currentRunID) {
    collaborationState.fields.runID.value = runState.currentRunID;
  }
  if (collaborationState.fields.stage && runState.currentStage) {
    collaborationState.fields.stage.value = runState.currentStage;
  }
}

function cssEscape(value) {
  if (window.CSS?.escape) return window.CSS.escape(value);
  return String(value).replace(/([\\"\]])/g, "\\$1");
}

function handleRunEvent(event, messages, runState, collaborationState) {
  const contextChanged = detectRunContext(event, runState, collaborationState);
  if (contextChanged && collaborationState && runState.currentRunID) {
    markCollaborationStale(collaborationState);
  }
  rememberTimelinePromptEstimate(runState, event);
  if (isProviderRequestStatusEvent(event)) {
    appendRunTimelineEvent(messages, event);
    return;
  }
  if (event.type === "text") {
    updateStreamingResult(runState, event.content || "");
    return;
  }
  if (event.type === "reasoning") {
    updateStreamingReasoning(runState, event.content || "");
    return;
  }
  if (event.type === "final_message") {
    replaceResult(runState, event.content || "");
    setResultStatus(runState, t("chat.resultReady"));
    return;
  }
  if (event.type === "error") {
    appendRunTimelineEvent(messages, event);
    showRunAttention(runState, t("chat.errorTitle"), event.content || "error", "error");
    setTimelineHint(runState, t("chat.timelineError"));
    setResultStatus(runState, t("chat.resultError"));
    if (collaborationState) setCollaborationStatus(collaborationState, t("chat.collabError"));
    runState.hasError = true;
    return;
  }
  if (event.type === "token_usage") {
    appendRunTimelineEvent(messages, event);
    return;
  }
  if (event.type === "workflow_result") {
    appendRunTimelineEvent(messages, event);
    const status = String(event.workflow_status || event.status || "").toLowerCase();
    if (isWorkflowRunTerminal(status)) {
      const finalOutput = publicResultText(formatStructuredValue(event.content || event.summary || "", true));
      if (finalOutput) replaceResult(runState, finalOutput);
      setTimelineHint(runState, t("chat.timelineDone"));
      setResultStatus(runState, t("chat.resultReady"));
    } else if (isApprovalStatus(status)) {
      runState.awaitingApproval = true;
      setTimelineHint(runState, t("chat.timelineApproval"));
      setResultStatus(runState, t("chat.resultBlocked"));
    } else if (status === "awaiting_input") {
      runState.awaitingInput = true;
      setTimelineHint(runState, t("chat.timelineInput"));
      setResultStatus(runState, t("chat.resultAwaitingInput"));
    } else if (status === "awaiting_sub_workflow") {
      setTimelineHint(runState, t("chat.timelineSubWorkflow"));
      setResultStatus(runState, t("chat.resultBlocked"));
    }
    runState.syncContextGuide?.();
    return;
  }
  if (event.type === "tool_call") {
    appendRunTimelineEvent(messages, event);
    return;
  }
  if (event.type === "tool_result") {
    appendRunTimelineEvent(messages, event);
    return;
  }
  if (event.type === "approval") {
    runState.awaitingApproval = true;
    appendRunTimelineEvent(messages, event);
    showRunAttention(runState, t("chat.awaitingApprovalTitle"), t("chat.awaitingApprovalHelp"), "approval");
    setTimelineHint(runState, t("chat.timelineApproval"));
    setResultStatus(runState, t("chat.resultBlocked"));
    if (collaborationState) {
      setCollaborationStatus(collaborationState, t("chat.collabScoped"));
      markCollaborationStale(collaborationState);
    }
    runState.syncContextGuide?.();
    return;
  }
  if (event.type === "task_stage") {
    appendRunTimelineEvent(messages, event);
  }
}

function appendRunTimelineEvent(messages, event) {
  appendTimelineEvent(messages, workflowRunEventToTimeline(event, null));
}

function appendSnapshotTimelineEvent(messages, event) {
  if (event) appendTimelineEvent(messages, { ...event, accountUsage: false });
}

function appendTimelineEvent(messages, event, options = {}) {
  if (!event) return;
  const buffer = timelineEventBuffers.get(messages) || [];
  buffer.push({ event, options });
  timelineEventBuffers.set(messages, buffer);
  if (timelineEventFlushFrame) return;
  timelineEventFlushFrame = requestAnimationFrame(flushTimelineEvents);
}

function flushTimelineEvents() {
  timelineEventFlushFrame = 0;
  for (const [messages, entries] of timelineEventBuffers) {
    if (!messages?.isConnected || !entries.length) continue;
    messages.querySelector(".timeline-empty-state")?.remove();
    const runState = timelineRunStates.get(messages);
    const stickToLatest = shouldStickTimelineToLatest(messages, entries);
    const heightBefore = messages.scrollHeight;
    const created = [];
    let changed = 0;
    for (const { event, options } of entries) {
      const decorated = decorateTimelineEvent(event, runState);
      if (!decorated) continue;
      const signature = timelineEventSignature(decorated);
      const coalesceKey = timelineEventCoalesceKey(decorated);
      const previous = options.coalesce === false ? null : findRecentTimelineEventNode(messages, created, signature, coalesceKey);
      if (previous) {
        incrementTimelineRepeat(previous, decorated);
        applyTimelineEventUsage(runState, previous, decorated);
        changed += 1;
        continue;
      }
      const div = document.createElement("div");
      div.className = `timeline-event ${decorated.tone || "neutral"}`;
      div.dataset.timelineSignature = signature;
      div.dataset.timelineCoalesceKey = coalesceKey;
      div.dataset.timelineRepeatCount = "1";
      if (decorated.stage) div.dataset.timelineStage = decorated.stage;
      if (decorated.agent) div.dataset.timelineAgent = decorated.agent;
      div.innerHTML = timelineEventCardHTML(decorated);
      applyTimelineEventUsage(runState, div, decorated);
      created.push(div);
      changed += 1;
    }
    const fragment = document.createDocumentFragment();
    for (let index = created.length - 1; index >= 0; index -= 1) fragment.appendChild(created[index]);
    const anchor = messages.querySelector(":scope > .timeline-event, :scope > .timeline-window-notice");
    if (anchor) messages.insertBefore(fragment, anchor);
    else messages.appendChild(fragment);
    trimTimelineWindow(messages, stickToLatest);
    if (stickToLatest) {
      if (runState) {
        runState.timelinePinnedToLatest = true;
        runState.timelineUnseenEvents = 0;
        setTimelineJumpLatestVisible(runState, false);
      }
      followTimelineLatest(runState, messages);
    } else {
      preserveTimelineScrollAfterPrepend(messages, messages.scrollHeight - heightBefore);
      if (runState && changed > 0) {
        runState.timelinePinnedToLatest = false;
        runState.timelineUnseenEvents = Math.min(99, (runState.timelineUnseenEvents || 0) + changed);
        setTimelineJumpLatestVisible(runState, true);
      }
    }
  }
  timelineEventBuffers.clear();
}

function findRecentTimelineEventNode(messages, created, signature, coalesceKey) {
  const match = node => {
    if (!node?.classList?.contains("timeline-event")) return false;
    if (node.dataset.timelineSignature === signature) return true;
    return Boolean(coalesceKey && node.dataset.timelineCoalesceKey === coalesceKey);
  };
  for (let index = created.length - 1; index >= 0; index -= 1) {
    if (match(created[index])) return created[index];
  }
  let scanned = 0;
  for (let node = messages?.firstElementChild; node && scanned < 18; node = node.nextElementSibling, scanned += 1) {
    if (match(node)) return node;
  }
  return null;
}

function timelineEventSignature(event = {}) {
  return [
    event.tone || "neutral",
    event.title || "",
    event.detail || ""
  ].map(timelineSignaturePart).join("\u001f");
}

function timelineEventCoalesceKey(event = {}) {
  const tone = timelineSignaturePart(event.tone || "neutral");
  const title = timelineSignaturePart(event.title || "");
  const detail = timelineSignaturePart(event.detail || "");
  if (isLowSignalTimelineRepeat(title, detail) || isFragmentaryTimelineEvent(title, detail)) {
    return [tone, title].join("\u001f");
  }
  return [tone, title, detail].join("\u001f");
}

function timelineEventDisplayDetail(event = {}) {
  const title = timelineSignaturePart(event.title || "");
  const detail = timelineSignaturePart(event.detail || "");
  return isFragmentaryTimelineEvent(title, detail) ? "" : detail;
}

function timelineTokenUsageEvent(source = {}) {
  const usage = extractTimelineTokenUsage(source);
  const rawTools = Array.isArray(source.tool_names) ? source.tool_names : String(source.tool_name || source.toolName || "").split(",");
  const tools = rawTools.map(taskToolDisplayName).filter(Boolean).join("、");
  const outputKind = tokenOutputKindLabel(source.output_kind || source.outputKind || source.content || "");
  const knownKind = outputKind === t("chat.tokenKind.unknown") ? "" : outputKind;
  const agentID = String(source.agent_id || source.agentID || "").trim();
  return {
    accountUsage: !source.replay_snapshot,
    tone: "info",
    title: knownKind ? `${t("chat.tokenCallTitle")} · ${knownKind}` : t("chat.tokenCallTitle"),
    detail: timelineDetail([
      agentID ? taskResourceDisplayName("agent", agentID, agentID) : "",
      source.mode ? modeLabel(source.mode) : "",
      source.task_stage || source.stage ? taskMachineDisplayName(source.task_stage || source.stage) : "",
      tools
    ]),
    usage,
    stage: String(source.task_stage || source.stage || "").trim(),
    agent: String(source.agent_id || source.agentID || "").trim(),
    kindLabel: t("chat.tokenCallTitle")
  };
}

export function extractTimelineTokenUsage(source = {}) {
  const budget = source.prompt_budget || source.promptBudget || source.usage || {};
  const estimated = runTokenNumber(source, ["budget_estimated_prompt_tokens", "estimated_prompt_tokens", "estimatedPromptTokens"])
    || runTokenNumber(budget, ["estimated_prompt_tokens", "estimatedPromptTokens", "budget_estimated_prompt_tokens"]);
  const input = runTokenNumber(source, ["prompt_tokens", "input_tokens", "promptTokens", "inputTokens", "budget_prompt_tokens", "budget_reported_prompt_tokens"])
    || runTokenNumber(budget, ["prompt_tokens", "input_tokens", "promptTokens"]);
  const output = runTokenNumber(source, ["output_tokens", "completion_tokens", "outputTokens", "completionTokens", "budget_output_tokens"])
    || runTokenNumber(budget, ["output_tokens", "completion_tokens", "outputTokens"]);
  const cached = runTokenNumber(source, ["cached_tokens", "cachedTokens", "budget_cached_tokens"])
    || runTokenNumber(budget, ["cached_tokens", "cachedTokens", "budget_cached_tokens"]);
  const total = runTokenNumber(source, ["total_tokens", "totalTokens", "budget_total_tokens"])
    || runTokenNumber(budget, ["total_tokens", "totalTokens"])
    || (input + output);
  const prompt = estimated || input;
  if (!prompt && !input && !output && !cached && !total) return null;
  const usage = {
    prompt,
    input: input || prompt,
    output,
    cached,
    total: total || input + output,
    cacheHitRate: 0
  };
  usage.cacheHitRate = usage.input > 0 ? usage.cached / usage.input : 0;
  return usage;
}

function decorateTimelineEvent(event, runState) {
  if (!event) return event;
  const usage = event.usage || extractTimelineTokenUsage(event);
  if (usage) {
    if (runState?.lastPromptEstimate && usage.prompt === usage.input && runState.lastPromptEstimate !== usage.input) {
      usage.prompt = runState.lastPromptEstimate;
    } else if (runState?.lastPromptEstimate && !usage.prompt) {
      usage.prompt = runState.lastPromptEstimate;
    }
    event.usage = usage;
  }
  if (!event.stage && runState?.currentStage) event.stage = runState.currentStage;
  return event;
}

function rememberTimelinePromptEstimate(runState, event = {}) {
  if (!runState) return;
  const budget = event.prompt_budget || event.promptBudget || event;
  const estimated = runTokenNumber(event, ["budget_estimated_prompt_tokens", "estimated_prompt_tokens", "estimatedPromptTokens"])
    || runTokenNumber(budget, ["estimated_prompt_tokens", "estimatedPromptTokens"]);
  if (estimated > 0) runState.lastPromptEstimate = estimated;
}

function timelineEventCardHTML(event = {}) {
  const detail = timelineEventDisplayDetail(event);
  const kind = event.kindLabel || "";
  return `
    <div class="timeline-marker"></div>
    <div class="timeline-card">
      <div class="timeline-card-head">
        <strong>${escapeHTML(event.title || "")}</strong>
        ${kind ? `<em>${escapeHTML(kind)}</em>` : ""}
      </div>
      ${detail ? `<p>${escapeHTML(detail)}</p>` : ""}
      ${event.preview ? runPreviewLinksHTML([event.preview]) : ""}
      ${timelineTokenMetricsHTML(event.usage)}
    </div>`;
}

function timelineTokenMetricsHTML(usage) {
  if (!usage) return "";
  const items = [
    Number(usage.input || 0) > 0 ? ["input", t("chat.tokenInput"), tokenCountText(usage.input)] : null,
    Number(usage.output || 0) > 0 ? ["output", t("chat.tokenOutput"), tokenCountText(usage.output)] : null,
    Number(usage.cached || 0) > 0 ? ["cached", t("chat.tokenCached"), tokenCountText(usage.cached)] : null,
    Number(usage.cached || 0) > 0 ? ["hit", t("chat.tokenCacheHit"), tokenCacheHitRateText(usage)] : null,
    Number(usage.total || 0) > 0 ? ["total", t("chat.tokenStepTotal"), tokenCountText(usage.total)] : null
  ].filter(Boolean);
  if (!items.length) return "";
  return `<dl class="timeline-token-metrics">${items.map(([kind, label, value]) => `
    <div data-metric="${escapeHTML(kind)}"><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl>`;
}

function emptyTimelineTokenTotals() {
  return { prompt: 0, input: 0, output: 0, cached: 0, total: 0, samples: 0, cacheHitRate: 0 };
}

function applyTimelineEventUsage(runState, node, event = {}) {
  const usage = event.usage;
  if (!node || !usage) return;
  node.dataset.timelineUsage = JSON.stringify(usage);
  syncTimelineEventUsageRow(node, usage);
  if (!runState) return;
  if (!runState.tokenSeen) runState.tokenSeen = new Set();
  if (!runState.tokenTotals) runState.tokenTotals = emptyTimelineTokenTotals();
  const key = node.dataset.timelineSignature || "";
  if (event.accountUsage && key && !runState.tokenSeen.has(key)) {
    runState.tokenSeen.add(key);
    runState.tokenTotals.prompt += Number(usage.prompt || 0);
    runState.tokenTotals.input += Number(usage.input || 0);
    runState.tokenTotals.output += Number(usage.output || 0);
    runState.tokenTotals.cached += Number(usage.cached || 0);
    runState.tokenTotals.total += Number(usage.total || 0);
    runState.tokenTotals.samples += 1;
    runState.tokenTotals.cacheHitRate = runState.tokenTotals.input > 0
      ? runState.tokenTotals.cached / runState.tokenTotals.input
      : 0;
    renderTimelineRunTotals(runState);
  }
}

function syncTimelineEventUsageRow(node, usage) {
  if (!node || !usage) return;
  const card = node.querySelector(".timeline-card");
  if (!card) return;
  let metrics = card.querySelector(".timeline-token-metrics");
  const html = timelineTokenMetricsHTML(usage);
  if (!html) return;
  if (metrics) metrics.outerHTML = html;
  else card.insertAdjacentHTML("beforeend", html);
}

function syncTimelineTotalsFromRun(runState, run) {
  if (!runState) return;
  const summary = runTokenUsageSummary(run);
  if (!summary) {
    renderTimelineRunTotals(runState);
    return;
  }
  const current = runState.tokenTotals || emptyTimelineTokenTotals();
  const reported = summary.reported;
  runState.tokenTotals = {
    prompt: reported ? summary.prompt : Math.max(current.prompt || 0, summary.prompt || 0),
    input: reported ? summary.prompt : Math.max(current.input || 0, summary.prompt || 0),
    output: reported ? summary.output : Math.max(current.output || 0, summary.output || 0),
    cached: reported ? summary.cached : Math.max(current.cached || 0, summary.cached || 0),
    total: reported ? summary.total : Math.max(current.total || 0, summary.total || 0),
    samples: reported ? summary.samples : Math.max(current.samples || 0, summary.samples || 0)
  };
  runState.tokenTotals.cacheHitRate = runState.tokenTotals.input > 0
    ? runState.tokenTotals.cached / runState.tokenTotals.input
    : Number(summary.cacheHitRate || 0);
  renderTimelineRunTotals(runState);
}

function renderTimelineRunTotals(runState) {
  const node = runState?.timelineTotals;
  if (!node) return;
  const totals = runState.tokenTotals || emptyTimelineTokenTotals();
  if (!(totals.total || totals.input || totals.output || totals.prompt)) {
    node.classList.add("hidden");
    node.innerHTML = "";
    return;
  }
  node.classList.remove("hidden");
  node.innerHTML = `
    <div>
      <strong>${escapeHTML(t("chat.tokenRunTotal"))}</strong>
      <span>${escapeHTML(t("chat.tokenSamples"))} ${escapeHTML(String(totals.samples || 0))}</span>
    </div>
    <dl>
      ${Number(totals.input || 0) > 0 ? `<div data-metric="input"><dt>${escapeHTML(t("chat.tokenInput"))}</dt><dd>${escapeHTML(tokenCountText(totals.input))}</dd></div>` : ""}
      ${Number(totals.output || 0) > 0 ? `<div data-metric="output"><dt>${escapeHTML(t("chat.tokenOutput"))}</dt><dd>${escapeHTML(tokenCountText(totals.output))}</dd></div>` : ""}
      ${Number(totals.cached || 0) > 0 ? `<div data-metric="cached"><dt>${escapeHTML(t("chat.tokenCached"))}</dt><dd>${escapeHTML(tokenCountText(totals.cached))}</dd></div>` : ""}
      ${Number(totals.cached || 0) > 0 ? `<div data-metric="hit"><dt>${escapeHTML(t("chat.tokenCacheHit"))}</dt><dd>${escapeHTML(tokenCacheHitRateText(totals))}</dd></div>` : ""}
      <div data-metric="total"><dt>${escapeHTML(t("chat.tokenRunTotal"))}</dt><dd>${escapeHTML(tokenCountText(totals.total || totals.input + totals.output))}</dd></div>
    </dl>`;
}

function isFragmentaryTimelineEvent(title, detail) {
  if (!title || !detail) return false;
  if (!/^[a-z0-9][a-z0-9_-]{2,}$/i.test(title) || !title.includes("-")) return false;
  if (detail.length > 40) return false;
  if (/[=\\]|https?:|approval|required|failed|error|completed|running/i.test(detail)) return false;
  if (/已完成|失败|错误|审批|运行中|等待/.test(detail)) return false;
  return true;
}

function isLowSignalTimelineRepeat(title, detail) {
  const text = `${title} ${detail}`.toLowerCase();
  return /\b(activated|activating|initialized|initializing|starting|started|syncing|reconnecting)\b/.test(text)
    || /已激活|激活中|初始化|开始执行|正在同步|重新连接/.test(text);
}

function timelineSignaturePart(value) {
  return String(value || "").replace(/\s+/g, " ").trim();
}

function incrementTimelineRepeat(node, event = {}) {
  const next = Math.max(1, Number(node.dataset.timelineRepeatCount || "1")) + 1;
  node.dataset.timelineRepeatCount = String(next);
  const card = node.querySelector(".timeline-card");
  if (!card) return;
  const title = timelineSignaturePart(event.title || "");
  const detail = timelineEventDisplayDetail(event);
  const body = card.querySelector("p");
  if (detail && body && body.textContent !== detail) body.textContent = detail;
  if (!detail && body && isFragmentaryTimelineEvent(title, timelineSignaturePart(event.detail || ""))) body.remove();
  let badge = card.querySelector(".timeline-repeat-count");
  if (!badge) {
    badge = document.createElement("span");
    badge.className = "timeline-repeat-count";
    card.appendChild(badge);
  }
  badge.textContent = `x${next}`;
  badge.setAttribute("aria-label", t("chat.timelineRepeatCount", { count: next }));
  syncTimelineEventUsageRow(node, event.usage);
}

function shouldStickTimelineToLatest(messages, entries = []) {
  if (!messages) return true;
  if (entries.some(item => item.options.stickToLatest === true || item.options.stickToBottom === true)) return true;
  if (entries.some(item => item.options.stickToLatest === false || item.options.stickToBottom === false)) return false;
  const runState = timelineRunStates.get(messages);
  if (runState && runState.timelinePinnedToLatest === false) return false;
  if (runState && runState.timelineSectionVisible === false) return false;
  const eventCount = messages.querySelectorAll(".timeline-event").length;
  if (eventCount < 3) return true;
  return isFollowingTimelineLatest(messages);
}

function followTimelineLatest(runState, messages) {
  if (!messages) return;
  if (timelineCanScroll(messages)) {
    messages.scrollTop = 0;
    requestAnimationFrame(() => {
      messages.scrollTop = 0;
    });
    return;
  }
  if (runState && !isTimelineSectionInView(runState)) return;
  const first = messages.querySelector(":scope > .timeline-event");
  first?.scrollIntoView({ block: "nearest", inline: "nearest" });
}

function preserveTimelineScrollAfterPrepend(messages, delta) {
  if (!messages || !(delta > 0)) return;
  if (timelineCanScroll(messages)) {
    messages.scrollTop += delta;
    return;
  }
  window.scrollBy(0, delta);
}

function setTimelineJumpLatestVisible(runState, visible) {
  const button = runState?.timelineJumpLatest;
  if (!button) return;
  const count = Math.max(0, Number(runState.timelineUnseenEvents || 0));
  button.classList.toggle("hidden", !visible || count <= 0);
  const label = count > 0
    ? t("chat.timelineJumpLatestCount", { count })
    : t("chat.timelineJumpLatest");
  button.innerHTML = `${icon("arrow-up")}<span>${escapeHTML(label)}</span>`;
  button.setAttribute("aria-label", label);
  button.setAttribute("title", label);
}

function captureRunViewState(state) {
  return {
    windowX: window.scrollX || 0,
    windowY: window.scrollY || 0,
    timeline: captureRunScrollNode(state?.messages),
    resultPreview: captureRunScrollNode(state?.resultPreview),
    resultOutput: captureRunScrollNode(state?.resultOutput),
    attention: captureRunScrollNode(state?.runAttention)
  };
}

function restoreRunViewState(state, viewState, options = {}) {
  if (!viewState) return;
  restoreRunScrollNode(state?.messages, viewState.timeline);
  if (state?.messages && viewState.timeline) {
    state.timelinePinnedToLatest = Boolean(viewState.timeline.atLatest ?? viewState.timeline.atBottom);
    state.timelineUnseenEvents = 0;
    setTimelineJumpLatestVisible(state, false);
    if (state.timelinePinnedToLatest) followTimelineLatest(state, state.messages);
  }
  restoreRunScrollNode(state?.resultPreview, viewState.resultPreview);
  restoreRunScrollNode(state?.resultOutput, viewState.resultOutput);
  restoreRunScrollNode(state?.runAttention, viewState.attention);
  requestAnimationFrame(() => {
    restoreRunScrollNode(state?.messages, viewState.timeline);
    if (state?.messages && viewState.timeline) {
      state.timelinePinnedToLatest = isFollowingTimelineLatest(state.messages);
      if (state.timelinePinnedToLatest) {
        state.timelineUnseenEvents = 0;
        setTimelineJumpLatestVisible(state, false);
      }
    }
    restoreRunScrollNode(state?.resultPreview, viewState.resultPreview);
    restoreRunScrollNode(state?.resultOutput, viewState.resultOutput);
    restoreRunScrollNode(state?.runAttention, viewState.attention);
    if (options.restoreWindow && window.goflowCanRestoreWindowScroll?.() !== false && (
      Math.abs((window.scrollY || 0) - (viewState.windowY || 0)) > 1 ||
      Math.abs((window.scrollX || 0) - (viewState.windowX || 0)) > 1
    )) {
      window.scrollTo({ left: viewState.windowX || 0, top: viewState.windowY || 0, behavior: "auto" });
    }
  });
}

function captureRunScrollNode(node) {
  if (!node) return null;
  return {
    top: node.scrollTop || 0,
    left: node.scrollLeft || 0,
    atBottom: isScrollNearBottom(node),
    atLatest: isScrollNearLatest(node)
  };
}

function restoreRunScrollNode(node, state) {
  if (!node || !state) return;
  const maxTop = Math.max(0, node.scrollHeight - node.clientHeight);
  const maxLeft = Math.max(0, node.scrollWidth - node.clientWidth);
  if (state.atLatest && node.id === "messages") node.scrollTop = 0;
  else node.scrollTop = state.atBottom ? maxTop : Math.min(state.top || 0, maxTop);
  node.scrollLeft = Math.min(state.left || 0, maxLeft);
}

function isScrollNearBottom(node, threshold = 48) {
  if (!node) return true;
  return node.scrollHeight - node.scrollTop - node.clientHeight <= threshold;
}

function isScrollNearLatest(node, threshold = 8) {
  if (!node) return true;
  return (node.scrollTop || 0) <= threshold;
}

function timelineCanScroll(node) {
  if (!node) return false;
  const overflow = window.getComputedStyle(node).overflowY;
  if (overflow === "visible" || overflow === "clip") return false;
  return node.scrollHeight > node.clientHeight + 8;
}

function isFollowingTimelineLatest(messages) {
  if (!messages) return true;
  if (timelineCanScroll(messages)) return isScrollNearLatest(messages, 8);
  const first = messages.querySelector(":scope > .timeline-event") || messages;
  const rect = first.getBoundingClientRect();
  return rect.top >= -48 && rect.top < window.innerHeight * 0.62;
}

function isTimelineSectionInView(runState) {
  const section = runState?.root?.querySelector(".run-timeline") || runState?.messages;
  if (!section) return true;
  const rect = section.getBoundingClientRect();
  return rect.bottom > 96 && rect.top < window.innerHeight - 72;
}

function updateStreamingResult(state, chunk) {
  const node = ensurePreviewNode(state);
  const next = `${node.dataset.raw || ""}${chunk}`;
  node.dataset.raw = next;
  schedulePreviewRender(state, next);
  state.hasPreview = true;
  updateResultEmptyState(state);
  scheduleRunStateSave(state);
}

function updateStreamingReasoning(state, chunk) {
  if (!state?.resultOutput || !chunk) return;
  let node = state.resultOutput.querySelector("[data-result-reasoning] .reasoning-body");
  if (!node) {
    if (!state.resultOutput.querySelector(".result-section-label")) {
      state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
    }
    const wrapper = document.createElement("section");
    wrapper.className = "run-reasoning";
    wrapper.dataset.resultReasoning = "true";
    wrapper.innerHTML = `<div class="run-evidence-head"><div><strong>${escapeHTML(t("chat.reasoningTitle"))}</strong><span>${escapeHTML(t("chat.reasoningHelp"))}</span></div></div><pre class="reasoning-body"></pre>`;
    const preview = previewResultNode(state);
    if (preview?.parentNode === state.resultOutput) {
      state.resultOutput.insertBefore(wrapper, preview);
    } else {
      state.resultOutput.appendChild(wrapper);
    }
    node = wrapper.querySelector(".reasoning-body");
    state.resultOutput.classList.remove("hidden");
    state.hasResult = true;
    updateResultEmptyState(state);
  }
  node.textContent = `${node.textContent || ""}${chunk}`;
}

function schedulePreviewRender(state, raw) {
  if (!state) return;
  state.pendingPreviewRaw = raw;
  if (state.previewRenderFrame) return;
  state.previewRenderFrame = requestAnimationFrame(() => {
    state.previewRenderFrame = 0;
    const node = previewResultNode(state);
    if (!node || !node.isConnected) return;
    const next = state.pendingPreviewRaw ?? node.dataset.raw ?? "";
    if (node.dataset.renderedRaw !== next) {
      node.innerHTML = renderMarkdown(next);
      node.dataset.renderedRaw = next;
    }
    node.classList.add("streaming");
  });
}

function replaceResult(state, text) {
  clearResultPreview(state);
  const node = document.createElement("div");
  node.className = "result-card markdown final";
  node.dataset.resultFinal = "true";
  node.dataset.raw = text;
  node.innerHTML = renderMarkdown(markdownResultText(localizedRunMarkdownText(text || "")));
  state.resultOutput.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultOutputTitle"))}</div>`;
  state.resultOutput.appendChild(node);
  state.finalNode = node;
  state.resultOutput.classList.remove("hidden");
  state.hasResult = true;
  updateResultEmptyState(state);
  saveRunState(state);
}

function finalizeStreamingResult(state) {
  const previewNode = previewResultNode(state);
  const finalNode = finalResultNode(state);
  if (previewNode && !finalNode && !state.awaitingApproval && !state.awaitingInput && !state.hasError) {
    replaceResult(state, previewNode.dataset.raw || "");
    return;
  }
  if (finalNode) {
    clearResultPreview(state);
  } else if (previewNode) {
    previewNode.classList.remove("streaming");
  }
  updateResultEmptyState(state);
  saveRunState(state);
}

function ensurePreviewNode(state) {
  const existing = previewResultNode(state);
  if (existing) return existing;
  state.resultPreview.innerHTML = `<div class="result-section-label">${escapeHTML(t("chat.resultPreviewTitle"))}</div>`;
  const node = document.createElement("div");
  node.className = "result-card markdown streaming";
  node.dataset.resultPreview = "true";
  node.dataset.raw = "";
  state.resultPreview.appendChild(node);
  state.resultPreview.classList.remove("hidden");
  state.previewNode = node;
  return node;
}

function clearResultPreview(state) {
  if (!state?.resultPreview) return;
  if (state.previewRenderFrame) {
    cancelAnimationFrame(state.previewRenderFrame);
    state.previewRenderFrame = 0;
  }
  state.pendingPreviewRaw = "";
  state.resultPreview.innerHTML = "";
  state.resultPreview.classList.add("hidden");
  state.previewNode = null;
  state.hasPreview = false;
}

function previewResultNode(state) {
  state.previewNode = state.previewNode || state.resultPreview?.querySelector("[data-result-preview]") || null;
  return state.previewNode;
}

function finalResultNode(state) {
  state.finalNode = state.finalNode || state.resultOutput?.querySelector("[data-result-final]") || null;
  return state.finalNode;
}

function updateResultEmptyState(state) {
  const hasSupplement = state.resultOutput?.querySelector("[data-result-evidence], [data-result-diffs], [data-result-supplemental], [data-result-execution]");
  const hasVisibleResult = Boolean(previewResultNode(state) || finalResultNode(state) || hasSupplement);
  state.resultPanel?.classList.toggle("hidden", !hasVisibleResult);
  state.resultEmpty.classList.toggle("hidden", hasVisibleResult);
  state.resultPreview.classList.toggle("hidden", !previewResultNode(state));
  state.resultOutput.classList.toggle("hidden", !finalResultNode(state) && !hasSupplement);
}

function setTimelineHint(state, text) {
  if (state.runTimelineHint && state.runTimelineHint.textContent !== text) state.runTimelineHint.textContent = text;
  scheduleRunStateSave(state);
}

function setResultStatus(state, text) {
  if (state.resultStatus && state.resultStatus.textContent !== text) state.resultStatus.textContent = text;
  scheduleRunStateSave(state);
}

function setRunLiveStatus(state, text, tone = "idle") {
  if (!state?.runLiveStatus) return;
  const nextClass = `live-badge ${tone || "idle"}`;
  const nextText = text || t("chat.realtimeIdle");
  if (state.runLiveStatus.className === nextClass && state.runLiveStatus.dataset.liveText === nextText) return;
  state.runLiveStatus.className = nextClass;
  state.runLiveStatus.dataset.liveText = nextText;
  const dot = state.runLiveStatus.querySelector("i") ? "<i></i>" : "";
  state.runLiveStatus.innerHTML = `${dot}${escapeHTML(nextText)}`;
}

function syncRunUnloadGuard(state) {
  if (!state) return;
  if (state.requestInFlight && !state.durableWorkflowActive) {
    window.addEventListener("beforeunload", warnBeforeLeavingActiveRun);
  } else {
    window.removeEventListener("beforeunload", warnBeforeLeavingActiveRun);
  }
}

function trimTimelineWindow(messages, stickToLatest, limit = 240) {
  const events = Array.from(messages.querySelectorAll(":scope > .timeline-event"));
  if (events.length <= limit) return;
  const previousTop = messages.scrollTop;
  const previousHeight = messages.scrollHeight;
  const removeCount = events.length - limit;
  for (let index = events.length - removeCount; index < events.length; index += 1) events[index].remove();
  const totalTrimmed = Number(messages.dataset.timelineTrimmedCount || 0) + removeCount;
  messages.dataset.timelineTrimmedCount = String(totalTrimmed);
  let notice = messages.querySelector(":scope > .timeline-window-notice");
  if (!notice) {
    notice = document.createElement("div");
    notice.className = "timeline-window-notice";
    messages.appendChild(notice);
  }
  notice.textContent = t("chat.timelineEarlierEventsCollapsed", { count: totalTrimmed });
  if (!stickToLatest && timelineCanScroll(messages)) {
    const removedHeight = Math.max(0, previousHeight - messages.scrollHeight);
    messages.scrollTop = Math.max(0, previousTop - removedHeight);
  }
}

function warnBeforeLeavingActiveRun(event) {
  event.preventDefault();
  event.returnValue = t("chat.leaveWarning");
  return event.returnValue;
}

function showRunAttention(state, title, body, tone) {
  setRunTimelineWorkflowInput(state, false);
  state.runAttention.className = `run-attention ${tone || "neutral"}`;
  state.runAttention.innerHTML = `
    <strong>${escapeHTML(title)}</strong>
    <p>${escapeHTML(body)}</p>`;
  state.runAttention.classList.remove("hidden");
  state.syncContextGuide?.();
}

function showTaskPreflightAttention(state, preflight = {}) {
  const issues = Array.isArray(preflight.issues) ? preflight.issues : [];
  const issue = issues.find(item => String(item?.severity || "").toLowerCase() === "error") || issues[0] || {};
  const detail = [issue.message, issue.recommendation].map(value => chatDisplayText(value || "")).filter(Boolean).join(" ");
  showRunAttention(state, t("chat.preflightTitle"), detail || t("chat.preflightFallback"), "error");
}

function showWorkspaceRequirementAttention(state, requirement) {
  const message = workspaceRequirementBody(requirement);
  setRunTimelineWorkflowInput(state, false);
  state.runAttention.className = "run-attention approval workspace-required";
  state.runAttention.innerHTML = `
    <strong>${escapeHTML(t("chat.workspacePreflightTitle"))}</strong>
    <p>${escapeHTML(message)}</p>
    <div class="run-attention-actions">
      <button type="button" data-open-workspace>${escapeHTML(t("chat.workspacePreflightAction"))}</button>
    </div>`;
  state.runAttention.classList.remove("hidden");
  state.runAttention.querySelector("[data-open-workspace]")?.addEventListener("click", () => {
    location.hash = "workspace";
  });
  state.syncContextGuide?.();
}

function workspaceRequirementBody(requirement = {}) {
  const reason = chatDisplayText(requirement.reason || t("chat.workspacePreflightReasonFallback"));
  const workspace = requirement.workspace?.display || requirement.workspace?.root || t("common.none");
  return t("chat.workspacePreflightBody", {
    reason,
    workspace
  });
}

function renderMarkdown(value) {
  return renderSafeMarkdown(value);
}

function markdownResultText(value) {
  const text = String(value || "").trim();
  if (!text || text.startsWith("```")) return text;
  if (!text.startsWith("{") && !text.startsWith("[")) return text;
  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object") return text;
    const pretty = JSON.stringify(parsed, null, 2).replaceAll("```", "\\u0060\\u0060\\u0060");
    return `\`\`\`json\n${pretty}\n\`\`\``;
  } catch {
    return text;
  }
}
