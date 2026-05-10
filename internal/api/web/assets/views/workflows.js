import { checkWorkspaceRequirement, escapeHTML, fetchWorkflowRun, fetchWorkflowRuns, renderSafeMarkdown, request, startWorkflowRun, streamWorkflowRunEvents, workflowRunEventsURL } from "../api.js";
import { currentLanguage, localizedText, t } from "../i18n.js";
import { loadResourceCapabilities, resourceAction, resourceActionLabel, resourceActionMethod, resourceActionPath, resourceCapability } from "../resource_actions.js";

const executableTypes = new Set(["agent", "skill", "tool", "team", "custom"]);
const controlTypes = new Set(["condition", "switch", "router", "policy_guard", "guard", "quality_gate", "quality_guard", "parallel", "join", "input_gate", "checkpoint", "for_each", "loop", "sub_workflow"]);
const visualTypes = new Set(["start", "end"]);
const expressionAssistFields = {
  condition: { inputID: "stageCondition", mode: "condition", field: "condition", labelKey: "workflow.condition", requestKey: "expression" },
  policy: { inputID: "stagePolicy", mode: "policy", field: "policy", labelKey: "workflow.policy", requestKey: "expression" },
  switch_on: { inputID: "stageSwitchOn", mode: "switch", field: "switch_on", labelKey: "workflow.switchOn", requestKey: "reference" }
};
const expressionAssistFieldOrder = ["condition", "policy", "switch_on"];
const workflowStudioRunKey = "goflow.workflowStudio.activeRun";
const defaultWorkflowStudioEventReconnectMS = 2200;
const workflowNodeMetrics = {
  regularWidth: 392,
  regularHeight: 158,
  compactWidth: 332,
  compactHeight: 122,
  minCanvasWidth: 960,
  minCanvasHeight: 640,
  leftPadding: 112,
  topPadding: 108
};
const runtimeStatusTone = {
  idle: "",
  running: "info",
  waiting: "warn",
  success: "good",
  error: "bad"
};
const state = {
  workflows: [],
  workflowTemplates: [],
  options: { agents: [], skills: [] },
  graph: { name: "new-workflow", description: "", stages: [] },
  selected: -1,
  dragging: null,
  panning: null,
  connectSource: "",
  connecting: null,
  zoom: 1,
  canvas: { width: workflowNodeMetrics.minCanvasWidth, height: workflowNodeMetrics.minCanvasHeight },
  focusMode: false,
  expressionAssist: createExpressionAssistState(),
  teamTemplateDetails: {},
  teamTemplateRequests: {},
  workflowTemplateFilter: { query: "", category: "" },
  nodePaletteFilter: "",
  resourceCapabilities: [],
  activeRoot: null,
  shortcutsBound: false,
  graphValidation: null,
  graphTransfer: null,
  runtime: createRuntimeState()
};
let workflowRepaintFrame = 0;
let workflowEdgeRepaintFrame = 0;
let workflowRuntimeRefreshFrame = 0;
let workflowRuntimeRefreshNeedsRender = false;
let workflowRuntimeRefreshNeedsRepaint = false;
let workflowRuntimeRefreshNeedsSave = false;
let workflowLogFlushFrame = 0;
const workflowLogBuffers = new Map();
let workflowZoomEndTimer = 0;
let expressionAssistTimer = 0;
let expressionAssistRequestSeq = 0;
const examplePlaceholder = value => escapeHTML(t("common.exampleValue", { value }));
const workflowInspectorWidthStorageKey = "goflow.workflow.inspectorWidth";
const workflowInspectorWidthBounds = { min: 340, max: 720, defaultValue: 440 };

function clampWorkflowInspectorWidth(width) {
  const parsed = Number.parseInt(width, 10);
  if (!Number.isFinite(parsed)) return workflowInspectorWidthBounds.defaultValue;
  return Math.min(workflowInspectorWidthBounds.max, Math.max(workflowInspectorWidthBounds.min, parsed));
}

function workflowInspectorWidth() {
  try {
    return clampWorkflowInspectorWidth(localStorage.getItem(workflowInspectorWidthStorageKey));
  } catch {
    return workflowInspectorWidthBounds.defaultValue;
  }
}

function applyWorkflowInspectorWidth(root, width = workflowInspectorWidth()) {
  const nextWidth = clampWorkflowInspectorWidth(width);
  const studio = root.querySelector(".studio");
  if (studio) studio.style.setProperty("--workflow-inspector-width", `${nextWidth}px`);
  const resizer = root.querySelector("#workflowInspectorResizer");
  if (resizer) {
    resizer.setAttribute("aria-valuemin", String(workflowInspectorWidthBounds.min));
    resizer.setAttribute("aria-valuemax", String(workflowInspectorWidthBounds.max));
    resizer.setAttribute("aria-valuenow", String(nextWidth));
  }
  return nextWidth;
}

function setWorkflowInspectorWidth(root, width) {
  const nextWidth = applyWorkflowInspectorWidth(root, width);
  try {
    localStorage.setItem(workflowInspectorWidthStorageKey, String(nextWidth));
  } catch {
    // Persisting the preference is nice to have; layout still updates without it.
  }
  return nextWidth;
}

function bindWorkflowInspectorResizer(root) {
  const resizer = root.querySelector("#workflowInspectorResizer");
  const studio = root.querySelector(".studio");
  if (!resizer || !studio) return;

  const pointerWidth = event => {
    const rect = studio.getBoundingClientRect();
    return rect.right - event.clientX - 4;
  };
  const stopResize = () => {
    studio.classList.remove("resizing-inspector");
    document.removeEventListener("pointermove", moveResize);
    document.removeEventListener("pointerup", stopResize);
    document.removeEventListener("pointercancel", stopResize);
  };
  const moveResize = event => {
    event.preventDefault();
    setWorkflowInspectorWidth(root, pointerWidth(event));
  };

  resizer.addEventListener("pointerdown", event => {
    if (event.button !== 0) return;
    event.preventDefault();
    studio.classList.add("resizing-inspector");
    setWorkflowInspectorWidth(root, pointerWidth(event));
    document.addEventListener("pointermove", moveResize);
    document.addEventListener("pointerup", stopResize, { once: true });
    document.addEventListener("pointercancel", stopResize, { once: true });
  });

  resizer.addEventListener("keydown", event => {
    const current = workflowInspectorWidth();
    const step = event.shiftKey ? 80 : 24;
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setWorkflowInspectorWidth(root, current + step);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setWorkflowInspectorWidth(root, current - step);
    } else if (event.key === "Home") {
      event.preventDefault();
      setWorkflowInspectorWidth(root, workflowInspectorWidthBounds.max);
    } else if (event.key === "End") {
      event.preventDefault();
      setWorkflowInspectorWidth(root, workflowInspectorWidthBounds.min);
    }
  });
}

export async function renderWorkflows(root) {
  state.activeRoot = root;
  const [options, resourceCapabilities] = await Promise.all([
    request("/api/workflow-options"),
    loadResourceCapabilities()
  ]);
  state.options = options;
  state.resourceCapabilities = resourceCapabilities;
  state.workflowTemplates = normalizeWorkflowTemplateSummaries(state.options.templates);
  root.innerHTML = `
    <div class="studio">
      <aside class="studio-left" aria-label="${escapeHTML(t("workflow.leftRail"))}">
        <div class="panel flat workflow-list-panel" data-tour-id="workflow-library-list">
          <div class="panel-head">
            <h2>${t("workflow.workflows")}</h2>
            <button id="newGraph" type="button">${t("workflow.new")}</button>
          </div>
          <div id="workflowList" class="flow-list"></div>
        </div>
        <div id="workflowTemplatePanel" class="panel flat workflow-template-panel">
          <div class="panel-head">
            <div>
              <h2>${t("workflow.templates")}</h2>
              <p class="muted">${t("workflow.templatesHelp")}</p>
            </div>
            <span id="workflowTemplateCount" class="badge"></span>
          </div>
          <div class="workflow-template-filter">
            <input id="workflowTemplateSearch" placeholder="${escapeHTML(t("workflow.templateSearchPlaceholder"))}" aria-label="${escapeHTML(t("workflow.templateSearch"))}">
            <select id="workflowTemplateCategory" aria-label="${escapeHTML(t("workflow.templateCategory"))}"></select>
          </div>
          <div id="workflowTemplateList" class="workflow-template-list"></div>
        </div>
        <div class="panel flat workflow-palette-panel" data-tour-id="workflow-palette">
          <div class="panel-head">
            <div>
              <h2>${t("workflow.library")}</h2>
              <p class="muted">${t("workflow.dropHint")}</p>
            </div>
            <span class="badge">${t("workflow.libraryActionHint")}</span>
          </div>
          <div class="workflow-palette-filter">
            <input id="nodePaletteSearch" placeholder="${escapeHTML(t("workflow.nodePaletteSearchPlaceholder"))}" aria-label="${escapeHTML(t("workflow.nodePaletteSearch"))}">
            <span id="nodePaletteFilterStatus" class="badge neutral">${escapeHTML(t("workflow.nodePaletteCount", { count: nodeTypeOptions().length }))}</span>
          </div>
          <div class="node-palette">
            ${renderNodePalette()}
          </div>
        </div>
      </aside>

      <section class="workflow-board" aria-label="${escapeHTML(t("workflow.canvasArea"))}">
        <div class="board-toolbar">
          <div class="workflow-board-title">
            <input id="graphName" class="title-input" aria-label="${escapeHTML(t("workflow.graphName"))}" placeholder="${escapeHTML(t("workflow.graphNamePlaceholder"))}">
            <input id="graphDescription" class="description-input" aria-label="${escapeHTML(t("workflow.graphDescription"))}" placeholder="${escapeHTML(t("workflow.graphDescriptionPlaceholder"))}">
          </div>
          <div class="toolbar workflow-board-actions">
            <div class="workflow-toolbar-group compact" aria-label="${escapeHTML(t("workflow.zoomControls"))}">
              <button id="zoomOut" type="button" class="icon-button" title="${escapeHTML(t("workflow.zoomOut"))}" aria-label="${escapeHTML(t("workflow.zoomOut"))}">-</button>
              <span id="zoomLabel" class="badge">100%</span>
              <button id="zoomIn" type="button" class="icon-button" title="${escapeHTML(t("workflow.zoomIn"))}" aria-label="${escapeHTML(t("workflow.zoomIn"))}">+</button>
              <button id="zoomFit" type="button">${t("workflow.zoomFit")}</button>
              <button id="zoomReset" type="button">${t("workflow.zoomReset")}</button>
            </div>
            <div class="workflow-toolbar-group" aria-label="${escapeHTML(t("workflow.layoutControls"))}">
              <button id="autoLayoutGraph" type="button">${t("workflow.autoLayout")}</button>
              <button id="focusCanvas" type="button">${t("workflow.focusMode")}</button>
            </div>
            <div class="workflow-toolbar-group workflow-toolbar-status" aria-label="${escapeHTML(t("workflow.statusControls"))}">
              <span id="stageCount" class="badge" aria-live="polite"></span>
              <span id="workflowReadinessBadge" class="workflow-readiness-badge neutral" aria-live="polite"></span>
            </div>
            <div class="workflow-toolbar-group" aria-label="${escapeHTML(t("workflow.fileControls"))}">
              <button id="validateGraph" type="button">${t("workflow.validateGraph")}</button>
              <button id="importGraph" type="button">${workflowResourceActionLabel("import", t("workflow.importGraph"))}</button>
              <button id="exportGraph" type="button">${workflowResourceActionLabel("export", t("workflow.exportGraph"))}</button>
              <input id="importGraphFile" class="hidden" type="file" accept=".json,.yaml,.yml,application/json,application/x-yaml,text/yaml,text/plain" aria-label="${escapeHTML(t("workflow.importFileLabel"))}">
            </div>
            <div class="workflow-toolbar-group workflow-toolbar-primary" aria-label="${escapeHTML(t("workflow.saveControls"))}">
              <button id="saveGraph" type="button" class="primary">${t("workflow.save")}</button>
              <button id="deleteGraph" type="button" class="danger">${t("workflow.delete")}</button>
            </div>
          </div>
        </div>
        <div id="workflowBoardStatus" class="workflow-board-status hidden">
          <div id="workflowValidationPanel" class="workflow-validation-panel hidden" aria-live="polite"></div>
          <div id="workflowTransferPanel" class="workflow-validation-panel workflow-transfer-panel hidden" aria-live="polite"></div>
        </div>
        <div id="canvas" class="canvas" data-tour-id="workflow-canvas" tabindex="0" aria-label="${escapeHTML(t("workflow.canvasAria"))}" aria-keyshortcuts="Escape Delete Backspace">
          <div id="canvasSpace" class="canvas-space">
            <div id="canvasSurface" class="canvas-surface">
              <div class="canvas-guide" data-tour-id="workflow-canvas-guide">
                <strong>${t("workflow.canvasPanHint")}</strong>
                <span>${t("workflow.canvasZoomHint")}</span>
                <span>${t("workflow.canvasExecutionHint")}</span>
              </div>
              <svg id="edges" aria-hidden="true">
                <defs><marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="5" orient="auto" markerUnits="userSpaceOnUse"><path d="M1,1 L9,5 L1,9 Z" fill="currentColor"/></marker></defs>
              </svg>
            </div>
          </div>
        </div>
      </section>

      <div id="workflowInspectorResizer" class="workflow-inspector-resizer" role="separator" aria-orientation="vertical" aria-controls="stageForm" aria-label="${escapeHTML(t("workflow.inspectorResize"))}" tabindex="0"></div>
      <aside class="studio-right" aria-label="${escapeHTML(t("workflow.inspectorRail"))}">
        <div class="panel flat workflow-inspector-panel" data-tour-id="workflow-inspector">
          <div class="workflow-inspector-head">
            <h2>${t("workflow.settings")}</h2>
            <span id="stageSelectionBadge" class="badge neutral">${t("workflow.noSelection")}</span>
          </div>
          <div id="stageEmpty" class="muted">${t("workflow.empty")}</div>
          <div id="stageForm" class="stack hidden">
            <div id="stagePlainSummary" class="workflow-stage-summary"></div>
            <div id="stageRoutePreview" class="workflow-route-preview hidden"></div>
            <div class="workflow-form-section" data-workflow-form-section="basic">
              <div class="workflow-form-section-head">
                <strong>${t("workflow.basicConfig")}</strong>
                <span>${t("workflow.basicConfigHelp")}</span>
              </div>
              <label data-stage-field="node_type"><span>${t("workflow.nodeType")}</span><select id="stageNodeType">
                ${nodeTypeOptions().map(option => `<option value="${escapeHTML(option.type)}">${escapeHTML(nodeTypeLabel(option))}</option>`).join("")}
              </select></label>
              <div id="stageNodeTypeMeta" data-stage-field="node_type" class="workflow-node-type-meta hidden"></div>
              <label data-stage-field="name"><span>${t("workflow.stageName")}</span><input id="stageName"></label>
              <label data-stage-field="agent"><span>${t("workflow.agent")}</span><select id="stageAgent"></select></label>
              <label data-stage-field="skill"><span>${t("workflow.skill")}</span><select id="stageSkill"></select></label>
              <label data-stage-field="tool"><span>${t("workflow.toolMetadata")}</span><select id="stageTool"></select></label>
              <label data-stage-field="team_template"><span>${t("workflow.teamTemplate")}</span><select id="stageParamTeam"></select></label>
              <div id="stageTeamTemplatePreview" data-stage-field="team_template" class="workflow-team-template-preview hidden"></div>
              <label data-stage-field="team_quorum_preset"><span>${t("workflow.teamQuorumPreset")}</span><select id="stageTeamQuorumPreset"></select></label>
              <label class="check" data-stage-field="team_execute"><input id="stageTeamExecute" type="checkbox"> ${t("workflow.teamExecuteRoles")}</label>
              <label data-stage-field="next"><span>${t("workflow.nextStages")}</span><input id="stageNext" placeholder="${escapeHTML(t("workflow.nextPlaceholder"))}"></label>
              <label class="check" data-stage-field="approval"><input id="stageApproval" type="checkbox"> ${t("workflow.requireApproval")}</label>
            </div>
            <div id="stageGuidance" class="workflow-stage-guidance"></div>
            <div id="stageDataFlow" class="workflow-stage-data-flow hidden"></div>
            <details id="stageAdvancedPanel" class="workflow-advanced-panel">
              <summary>
                <span>
                  <strong>${t("workflow.advancedConfig")}</strong>
                  <small>${t("workflow.advancedConfigHelp")}</small>
                </span>
                <i id="stageAdvancedCount">${t("workflow.optional")}</i>
              </summary>
              <div class="workflow-advanced-body">
                <div id="stageAdvancedGuide" class="workflow-advanced-guide"></div>
                <label data-stage-field="next_strategy"><span>${t("workflow.branchStrategy")}</span><input id="stageNextStrategy" placeholder="${escapeHTML(t("workflow.branchPlaceholder"))}"><small class="workflow-field-hint">${t("workflow.branchStrategyHelp")}</small></label>
                <div id="stageAdvancedFields" class="workflow-advanced-fields">
                  <strong>${t("workflow.advancedFields")}</strong>
                  <div id="stageControlHelp" class="workflow-control-help hidden"></div>
                  <label data-field="condition"><span>${t("workflow.condition")}</span><input id="stageCondition" data-expression-field="condition" placeholder="${escapeHTML(t("workflow.conditionPlaceholder"))}"></label>
                  <label data-field="policy_rule"><span>${t("workflow.policyRule")}</span><select id="stagePolicyRule">${policyRuleOptions().map(option => `<option value="${escapeHTML(option.name)}">${escapeHTML(policyRuleLabel(option))}</option>`).join("")}</select></label>
                  <div data-field="policy_rule" id="stagePolicyRuleHelp" class="workflow-policy-rule-help"></div>
                  <label data-field="policy"><span>${t("workflow.policy")}</span><input id="stagePolicy" data-expression-field="policy" placeholder="${escapeHTML(t("workflow.policyPlaceholder"))}"></label>
                  <label data-field="switch_on"><span>${t("workflow.switchOn")}</span><input id="stageSwitchOn" data-expression-field="switch_on" placeholder="${escapeHTML(t("workflow.switchOnPlaceholder"))}"></label>
                  <div id="stageExpressionAssist" class="workflow-expression-assist hidden" role="status" aria-live="polite"></div>
                  <label data-field="routes"><span>${t("workflow.routes")}</span><textarea id="stageRoutes" class="compact-textarea" placeholder="${escapeHTML(t("workflow.routesPlaceholder"))}"></textarea></label>
                  <label data-field="cases"><span>${t("workflow.cases")}</span><textarea id="stageCases" class="compact-textarea" placeholder="${escapeHTML(t("workflow.casesPlaceholder"))}"></textarea></label>
                  <div data-field="input_fields_json" id="stageInputFieldsBuilder" class="workflow-input-builder"></div>
                  <label data-field="input_fields_json" class="workflow-input-json-field"><span>${t("workflow.inputFieldsJson")}</span><textarea id="stageInputFieldsJson" class="compact-textarea" placeholder="${escapeHTML(t("workflow.inputFieldsJsonPlaceholder"))}"></textarea></label>
                  <label data-field="param_workflow"><span>${t("workflow.subWorkflowName")}</span><input id="stageParamWorkflow" placeholder="${escapeHTML(t("workflow.subWorkflowNamePlaceholder"))}"></label>
                  <label data-field="param_request"><span>${t("workflow.subWorkflowRequest")}</span><input id="stageParamRequest" placeholder="${escapeHTML(t("workflow.subWorkflowRequestPlaceholder"))}"></label>
                  <label data-field="param_items"><span>${t("workflow.eachItems")}</span><input id="stageParamItems" placeholder="${escapeHTML(t("workflow.eachItemsPlaceholder"))}"></label>
                  <label data-field="param_stage"><span>${t("workflow.bodyStage")}</span><input id="stageParamStage" placeholder="${escapeHTML(t("workflow.bodyStagePlaceholder"))}"></label>
                  <label data-field="param_until"><span>${t("workflow.loopUntil")}</span><input id="stageParamUntil" placeholder="${escapeHTML(t("workflow.loopUntilPlaceholder"))}"></label>
                  <label data-field="param_max_iterations"><span>${t("workflow.loopMaxIterations")}</span><input id="stageParamMaxIterations" type="number" min="1" placeholder="${examplePlaceholder("3")}"></label>
                  <label data-field="param_wait_for"><span>${t("workflow.joinWaitFor")}</span><input id="stageParamWaitFor" placeholder="${escapeHTML(t("workflow.joinWaitForPlaceholder"))}"></label>
                  <label data-field="param_prompt"><span>${t("workflow.checkpointPrompt")}</span><input id="stageParamPrompt" placeholder="${escapeHTML(t("workflow.checkpointPromptPlaceholder"))}"></label>
                  <label data-field="input"><span>${t("workflow.inputMap")}</span><textarea id="stageInputMap" class="compact-textarea" placeholder="${escapeHTML(t("workflow.inputMapPlaceholder"))}"></textarea><small class="workflow-field-hint">${t("workflow.inputMapHelp")}</small></label>
                  <label data-field="outputs"><span>${t("workflow.outputsMap")}</span><textarea id="stageOutputsMap" class="compact-textarea" placeholder="${escapeHTML(t("workflow.outputsMapPlaceholder"))}"></textarea><small class="workflow-field-hint">${t("workflow.outputsMapHelp")}</small></label>
                </div>
                <label data-stage-field="params"><span>${t("workflow.parameters")}</span><textarea id="stageParams" class="compact-textarea" placeholder="${escapeHTML(t("workflow.paramsPlaceholder"))}"></textarea><small class="workflow-field-hint">${t("workflow.paramsHelp")}</small></label>
              </div>
            </details>
            <details id="stageArtifactsPanel" class="workflow-advanced-panel workflow-artifacts-panel">
              <summary>
                <span>
                  <strong>${t("workflow.resultConfig")}</strong>
                  <small>${t("workflow.resultConfigHelp")}</small>
                </span>
                <i id="stageArtifactsCount">${t("workflow.optional")}</i>
              </summary>
              <div id="stageArtifactsEditor" class="artifact-editor" data-stage-field="artifacts">
                <div id="stageArtifactsGuide" class="workflow-artifact-guide"></div>
                <div class="artifact-editor-head">
                  <div>
                    <strong>${t("workflow.artifacts")}</strong>
                    <span>${t("workflow.artifactsHelp")}</span>
                  </div>
                  <button id="addArtifact" type="button" class="ghost-button">${t("workflow.addArtifact")}</button>
                </div>
                <div id="stageArtifactsList" class="artifact-list"></div>
                <div id="stageAcceptanceEditor" class="artifact-editor workflow-acceptance-editor" data-stage-field="acceptance_criteria">
                  <div id="stageAcceptanceGuide" class="workflow-artifact-guide workflow-acceptance-guide"></div>
                  <div class="artifact-editor-head">
                    <div>
                      <strong>${t("workflow.acceptanceCriteria")}</strong>
                      <span>${t("workflow.acceptanceCriteriaHelp")}</span>
                    </div>
                    <button id="addAcceptanceCriterion" type="button" class="ghost-button">${t("workflow.addAcceptanceCriterion")}</button>
                  </div>
                  <div id="stageAcceptanceList" class="artifact-list"></div>
                </div>
              </div>
            </details>
            <div class="toolbar">
              <button id="connectStage" type="button">${t("workflow.connect")}</button>
              <button id="removeStage" type="button" class="danger">${t("workflow.remove")}</button>
            </div>
            <div id="connectHint" class="muted"></div>
            <div id="stageRuntime" class="workflow-stage-runtime hidden"></div>
            <div class="muted">${t("workflow.edgeHint")}</div>
          </div>
        </div>
        <div class="panel flat workflow-order-card">
          <div class="panel-head">
            <h2>${t("workflow.executionOrderTitle")}</h2>
            <span class="badge">${t("workflow.nextListBadge")}</span>
          </div>
          <div id="workflowOrderPath" class="workflow-order-path"></div>
          <p class="muted">${t("workflow.executionOrderHelp")}</p>
        </div>
        <div class="panel flat workflow-run-panel" data-tour-id="workflow-run-preview">
          <div class="panel-head workflow-run-head">
            <div>
              <h2>${t("workflow.runPreview")}</h2>
              <p class="muted">${t("workflow.runPreviewHelp")}</p>
            </div>
            <span id="workflowRuntimeBadge" class="badge">${t("workflow.runtimeIdle")}</span>
          </div>
          <textarea id="runInput" aria-label="${escapeHTML(t("workflow.runInputLabel"))}" placeholder="${escapeHTML(t("workflow.runInputPlaceholder"))}"></textarea>
          <button id="runGraph" type="button" class="primary workflow-run-button">${t("workflow.run")}</button>
          <div id="workflowRuntimeSummary" class="workflow-runtime-summary hidden"></div>
          <pre id="runOutput" class="mini-log"></pre>
        </div>
      </aside>
    </div>`;

  applyWorkflowInspectorWidth(root);
  await loadWorkflowList();
  await loadWorkflowTemplates();
  await openRequestedWorkflow();
  if (!state.graph.stages.length) createPresetGraph();
  bind(root);
  bindWorkflowShortcuts();
  renderAll(root);
  restoreWorkflowStudioRuntime(root).catch(() => {});
  window.addEventListener("goflow:view-dispose", () => stopWorkflowStudioEventStream(), { once: true });
}

function createRuntimeState() {
  return {
    status: "idle",
    workflowName: "",
    workflowStatus: "",
    runInput: "",
    currentStage: "",
    lastStage: "",
    approval: null,
    error: "",
    tokenUsage: null,
    route: null,
    runID: "",
    eventsURL: "",
    lastEventSeq: 0,
    eventReconnectDelay: defaultWorkflowStudioEventReconnectMS,
    streamReconnectTimer: null,
    streamAbortController: null,
    streamInFlight: false,
    eventStreamRunID: "",
    stageDetails: {},
    timeline: [],
    artifacts: 0,
    outputs: 0,
    startedAt: 0,
    finishedAt: 0
  };
}

function createExpressionAssistState() {
  return {
    field: "",
    status: "idle",
    result: null,
    error: "",
    staleRun: false,
    signature: "",
    pendingSignature: ""
  };
}

function resetRuntimeState(input = "") {
  state.runtime = createRuntimeState();
  state.runtime.status = "running";
  state.runtime.runInput = input;
  state.runtime.startedAt = Date.now();
}

function selectedStageRuntime() {
  const stage = selectedStage();
  if (!stage?.name) return null;
  return state.runtime.stageDetails[stage.name] || null;
}

function ingestRuntimeEvent(event) {
  if (!event || typeof event !== "object") return false;
  state.runtime.timeline.push(event);
  if (state.runtime.timeline.length > 80) state.runtime.timeline.shift();

  if (event.type === "workflow_result") {
    state.runtime.workflowName = event.workflow_name || state.graph.name || "";
    state.runtime.workflowStatus = event.workflow_status || "";
    state.runtime.status = workflowStatusState(event.workflow_status);
    markCurrentStageTerminal(state.runtime.status);
    state.runtime.finishedAt = Date.now();
    return true;
  }

  let changed = false;
  if (event.type === "approval") {
    state.runtime.status = "waiting";
    state.runtime.approval = {
      tool: event.tool_name || "",
      summary: event.arguments_summary || event.content || ""
    };
    changed = true;
  }

  if (event.type === "error") {
    state.runtime.status = "error";
    state.runtime.error = workflowDisplayText(event.message || event.content || "");
    markCurrentStageTerminal("error");
    state.runtime.finishedAt = Date.now();
    changed = true;
  }

  if (event.type === "token_usage") {
    state.runtime.tokenUsage = {
      prompt: event.prompt_tokens || 0,
      output: event.output_tokens || 0,
      cached: event.cached_tokens || 0
    };
    changed = true;
  }

  const stageRuntime = normalizeStageRuntimeEvent(event);
  if (!stageRuntime) return changed;
  const previousStage = state.runtime.currentStage;
  if (previousStage && previousStage !== stageRuntime.name) markStageRuntimeComplete(previousStage);
  state.runtime.currentStage = stageRuntime.name;
  state.runtime.lastStage = stageRuntime.name;
  state.runtime.route = deriveRoute(stageRuntime);
  state.runtime.stageDetails[stageRuntime.name] = {
    ...(state.runtime.stageDetails[stageRuntime.name] || {}),
    ...stageRuntime,
    updatedAt: Date.now()
  };
  return true;
}

function markStageRuntimeComplete(stageName) {
  const runtime = state.runtime.stageDetails[stageName];
  if (!runtime) return;
  const current = String(runtime.status || "").toLowerCase();
  if (["completed", "success", "succeeded", "done", "finished", "failed", "error", "denied", "cancelled"].includes(current)) return;
  runtime.status = "completed";
}

function markCurrentStageTerminal(status) {
  const stageName = state.runtime.currentStage;
  if (!stageName) return;
  const runtime = state.runtime.stageDetails[stageName];
  if (!runtime) return;
  if (status === "success") runtime.status = "completed";
  if (status === "error") runtime.status = "error";
}

function normalizeStageRuntimeEvent(event) {
  const candidates = [
    event.stage_data,
    event.stage_state,
    event.stage,
    event.data,
    event.payload,
    event.details
  ];
  const payload = candidates.find(value => value && typeof value === "object" && !Array.isArray(value)) || {};
  const name = String(
    payload.name ||
    payload.stage_name ||
    event.stage_name ||
    event.task_stage ||
    event.stage ||
    event.node_name ||
    ""
  ).trim();
  if (!name) return null;
  const outputs = payload.outputs ?? event.outputs ?? null;
  const artifacts = Array.isArray(payload.artifacts)
    ? payload.artifacts.length
    : Array.isArray(event.artifacts)
      ? event.artifacts.length
      : null;
  const acceptance = normalizeAcceptanceItems(payload.acceptance ?? event.acceptance ?? null);
  const normalized = {
    name,
    status: payload.status || event.status || event.workflow_status || event.type || "task_stage",
    content: payload.content || event.content || "",
    node_type: payload.node_type || event.node_type || "",
    skill: payload.skill || event.skill || "",
    tool: payload.tool || event.tool || event.tool_name || "",
    inputs: payload.inputs ?? event.inputs ?? null,
    outputs,
    attempts: payload.attempts ?? event.attempts ?? null,
    artifacts,
    metadata: payload.metadata ?? event.metadata ?? null,
    route: payload.route ?? event.route ?? outputs?.route ?? "",
    value: payload.value ?? event.value ?? outputs?.value,
    target: payload.target ?? event.target ?? outputs?.target ?? "",
    passed: payload.passed ?? event.passed ?? outputs?.passed
  };
  if (acceptance.length) normalized.acceptance = acceptance;
  return normalized;
}

function normalizeAcceptanceItems(items) {
  return (Array.isArray(items) ? items : [])
    .map(item => {
      if (!item || typeof item !== "object") return null;
      return {
        name: normalizeAcceptanceText(item.name || item.title || item.description || ""),
        description: normalizeAcceptanceText(item.description || ""),
        ref: normalizeAcceptanceText(item.ref || ""),
        expected: normalizeAcceptanceText(item.expected || ""),
        actual: normalizeAcceptanceText(item.actual || ""),
        status: normalizeAcceptanceText(item.status || ""),
        reason: normalizeAcceptanceText(item.reason || "")
      };
    })
    .filter(Boolean);
}

function normalizeAcceptanceText(value) {
  if (value === undefined || value === null || value === "") return "";
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return formatRuntimeValue(value);
}

function truncateWorkflowText(value, limit) {
  const text = String(value || "").trim();
  if (!text || text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 3)).trimEnd()}...`;
}

function deriveRoute(stageRuntime) {
  if (!stageRuntime) return null;
  const hasRoute = [stageRuntime.route, stageRuntime.target, stageRuntime.value, stageRuntime.passed]
    .some(value => value !== "" && value !== null && value !== undefined);
  if (!hasRoute) return null;
  return {
    route: stageRuntime.route || "",
    target: stageRuntime.target || "",
    value: stageRuntime.value,
    passed: stageRuntime.passed
  };
}

function workflowStatusState(status) {
  const value = String(status || "").toLowerCase();
  if (["completed", "success", "succeeded", "done", "finished"].includes(value)) return "success";
  if (["failed", "error", "denied", "cancelled"].includes(value)) return "error";
  if (["waiting", "paused", "approval_required", "approval", "pending", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow"].includes(value)) return "waiting";
  if (value) return "running";
  return "idle";
}

function renderRuntime(root) {
  renderRuntimeBadge(root);
  renderRuntimeSummary(root);
  renderSelectedStageRuntime(root);
}

function renderRuntimeBadge(root) {
  const badge = root.querySelector("#workflowRuntimeBadge");
  if (!badge) return;
  const status = state.runtime.status || "idle";
  const nextClass = `badge ${runtimeStatusTone[status] || ""}`.trim();
  const nextText = t(`workflow.runtimeStatus.${status}`);
  if (badge.className !== nextClass) badge.className = nextClass;
  if (badge.textContent !== nextText) badge.textContent = nextText;
}

function renderRuntimeSummary(root) {
  const summary = root.querySelector("#workflowRuntimeSummary");
  if (!summary) return;
  const status = state.runtime.status || "idle";
  if (status === "idle") {
    summary.classList.add("hidden");
    if (summary.innerHTML) summary.innerHTML = "";
    summary.dataset.runtimeHtml = "";
    return;
  }
  const metrics = [];
  if (state.runtime.workflowStatus) metrics.push(summaryMetric(t("workflow.runtimeSummary.workflowStatus"), localizedText(state.runtime.workflowStatus)));
  if (state.runtime.currentStage) metrics.push(summaryMetric(t("workflow.runtimeSummary.currentStage"), workflowDisplayValue(state.runtime.currentStage)));
  if (state.runtime.route?.target || state.runtime.route?.route) metrics.push(summaryMetric(t("workflow.runtimeSummary.route"), formatRouteText(state.runtime.route)));
  if (state.runtime.tokenUsage) {
    metrics.push(summaryMetric(
      t("workflow.runtimeSummary.tokens"),
      `${t("workflow.in")} ${state.runtime.tokenUsage.prompt || 0} / ${t("workflow.out")} ${state.runtime.tokenUsage.output || 0}`
    ));
  }
  if (state.runtime.approval?.tool) metrics.push(summaryMetric(t("workflow.runtimeSummary.approval"), localizedText(state.runtime.approval.tool)));
  if (state.runtime.error) metrics.push(summaryMetric(t("workflow.runtimeSummary.error"), localizedText(state.runtime.error)));
  if (!metrics.length && state.runtime.runInput) metrics.push(summaryMetric(t("workflow.runtimeSummary.input"), state.runtime.runInput));
  const progress = workflowRuntimeProgress();
  summary.classList.remove("hidden");
  const nextHTML = `
    <div class="workflow-runtime-summary-head">
      <strong>${t(`workflow.runtimeStatus.${status}`)}</strong>
      <span>${escapeHTML(runtimeMetaLine())}</span>
    </div>
    ${renderWorkflowRuntimeProgress(progress)}
    <div class="workflow-runtime-summary-grid">${metrics.join("")}</div>`;
  if (summary.dataset.runtimeHtml === nextHTML && summary.innerHTML === nextHTML) return;
  summary.innerHTML = nextHTML;
  summary.dataset.runtimeHtml = nextHTML;
}

function workflowRuntimeProgress() {
  const total = (state.graph.stages || []).length;
  const details = Object.values(state.runtime.stageDetails || {});
  const completed = details.filter(item => ["completed", "success", "succeeded", "done", "finished"].includes(String(item.status || "").toLowerCase())).length;
  const failed = details.filter(item => ["failed", "error", "denied", "cancelled", "canceled"].includes(String(item.status || "").toLowerCase())).length;
  const touched = new Set(details.map(item => item?.name).filter(Boolean));
  const percent = total ? Math.min(100, Math.round((Math.min(completed + failed, total) / total) * 100)) : 0;
  return {
    total,
    completed,
    failed,
    touched: touched.size,
    pending: Math.max(total - completed - failed, 0),
    percent,
    outputs: Math.max(state.runtime.outputs || 0, countRuntimeOutputs(details)),
    artifacts: state.runtime.artifacts || 0
  };
}

function countRuntimeOutputs(details) {
  return details.reduce((sum, item) => {
    if (!item?.outputs) return sum;
    if (Array.isArray(item.outputs)) return sum + item.outputs.length;
    if (typeof item.outputs === "object") return sum + Object.keys(item.outputs).length;
    return sum + 1;
  }, 0);
}

function renderWorkflowRuntimeProgress(progress) {
  return `
    <div class="workflow-runtime-progress">
      <div class="workflow-runtime-progress-track" aria-label="${escapeHTML(t("workflow.runtimeProgressLabel"))}">
        <span style="width: ${progress.percent}%"></span>
      </div>
      <div class="workflow-runtime-progress-facts">
        <span><small>${escapeHTML(t("workflow.runtimeProgress.completed"))}</small><strong>${escapeHTML(String(progress.completed))}/${escapeHTML(String(progress.total || 0))}</strong></span>
        <span><small>${escapeHTML(t("workflow.runtimeProgress.active"))}</small><strong>${escapeHTML(String(progress.touched))}</strong></span>
        <span><small>${escapeHTML(t("workflow.runtimeProgress.pending"))}</small><strong>${escapeHTML(String(progress.pending))}</strong></span>
        ${progress.failed ? `<span class="bad"><small>${escapeHTML(t("workflow.runtimeProgress.failed"))}</small><strong>${escapeHTML(String(progress.failed))}</strong></span>` : ""}
        ${progress.outputs ? `<span><small>${escapeHTML(t("workflow.runtimeProgress.outputs"))}</small><strong>${escapeHTML(String(progress.outputs))}</strong></span>` : ""}
        ${progress.artifacts ? `<span><small>${escapeHTML(t("workflow.runtimeProgress.artifacts"))}</small><strong>${escapeHTML(String(progress.artifacts))}</strong></span>` : ""}
      </div>
    </div>`;
}

function renderSelectedStageRuntime(root) {
  const container = root.querySelector("#stageRuntime");
  if (!container) return;
  const stage = selectedStage();
  const runtime = selectedStageRuntime();
  if (!stage || !runtime) {
    container.classList.add("hidden");
    container.innerHTML = "";
    container.dataset.runtimeStageName = "";
    return;
  }
  const stageName = String(stage.name || "");
  const previousStageName = container.dataset.runtimeStageName || "";
  const sameStage = previousStageName === stageName;
  const technicalOpen = sameStage && !!container.querySelector(".workflow-runtime-technical")?.open;
  const scrollState = captureWorkflowRuntimeScroll(container, sameStage);
  const facts = [];
  if (runtime.status) facts.push(detailChip(t("workflow.runtimeField.status"), localizedText(runtime.status)));
  if (runtime.node_type) facts.push(detailChip(t("workflow.runtimeField.nodeType"), nodeDisplayType(runtime.node_type)));
  if (runtime.skill) facts.push(detailChip(t("workflow.runtimeField.skill"), localizedText(runtime.skill)));
  if (runtime.tool) facts.push(detailChip(t("workflow.runtimeField.tool"), localizedText(runtime.tool)));
  if (runtime.attempts !== null && runtime.attempts !== undefined && runtime.attempts !== "") facts.push(detailChip(t("workflow.runtimeField.attempts"), String(runtime.attempts)));
  const acceptance = Array.isArray(runtime.acceptance) ? runtime.acceptance : [];
  if (acceptance.length) facts.push(detailChip(t("workflow.runtimeField.acceptance"), workflowAcceptanceSummary(acceptance)));
  if (runtime.route || runtime.target || runtime.value !== undefined || runtime.passed !== undefined) {
    facts.push(detailChip(t("workflow.runtimeField.route"), formatRouteText(runtime)));
  }
  const summaryBlock = runtime.content
    ? runtimeBlock(t("workflow.runtimeField.summary"), `<div class="run-markdown workflow-runtime-markdown">${renderSafeMarkdown(runtime.content)}</div>`)
    : "";
  const acceptanceBlock = renderWorkflowAcceptanceBlock(acceptance);
  const technicalBlocks = [
    renderValueBlock(t("workflow.runtimeField.inputs"), runtime.inputs),
    renderValueBlock(t("workflow.runtimeField.outputs"), runtime.outputs),
    renderValueBlock(t("workflow.runtimeField.metadata"), runtime.metadata)
  ].filter(Boolean);
  const detailBlocks = [
    summaryBlock,
    acceptanceBlock,
    runtimeTechnicalDetails(technicalBlocks, technicalOpen)
  ].filter(Boolean).join("");
  const explanation = stageRuntimeExplanation(stage, runtime);

  container.classList.remove("hidden");
  const nextHTML = `
    <div class="workflow-stage-runtime-head">
      <strong>${t("workflow.runtimeDetailTitle")}</strong>
      <span>${escapeHTML(stage.name || "")}</span>
    </div>
    <div class="workflow-stage-runtime-explain ${escapeHTML(explanation.tone)}">
      <strong>${escapeHTML(explanation.title)}</strong>
      <span>${escapeHTML(explanation.body)}</span>
    </div>
    ${facts.length ? `<div class="workflow-stage-runtime-facts">${facts.join("")}</div>` : ""}
    ${detailBlocks || `<p class="muted">${t("workflow.runtimeNoDetails")}</p>`}`;
  if (container.dataset.runtimeHtml === nextHTML && container.innerHTML === nextHTML) return;
  container.innerHTML = nextHTML;
  container.dataset.runtimeHtml = nextHTML;
  container.dataset.runtimeStageName = stageName;
  const technical = container.querySelector(".workflow-runtime-technical");
  if (technical) technical.open = technicalOpen;
  restoreWorkflowRuntimeScroll(container, scrollState);
}

function captureWorkflowRuntimeScroll(container, preserve = true) {
  if (!container || !preserve) return { top: 0, left: 0, atBottom: false };
  return {
    top: container.scrollTop || 0,
    left: container.scrollLeft || 0,
    atBottom: container.scrollHeight - container.scrollTop - container.clientHeight <= 48
  };
}

function restoreWorkflowRuntimeScroll(container, state = {}) {
  if (!container) return;
  const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
  const maxLeft = Math.max(0, container.scrollWidth - container.clientWidth);
  container.scrollTop = state.atBottom ? maxTop : Math.min(state.top || 0, maxTop);
  container.scrollLeft = Math.min(state.left || 0, maxLeft);
}

function stageRuntimeExplanation(stage, runtime) {
  const stateName = nodeRuntimeState(runtime, stage?.name || "");
  const route = runtimeRouteTarget(stage || {}, runtime || {});
  if (stateName === "waiting") {
    return {
      tone: "waiting",
      title: t("workflow.runtimeExplain.waiting.title"),
      body: t("workflow.runtimeExplain.waiting.body")
    };
  }
  if (stateName === "error") {
    return {
      tone: "error",
      title: t("workflow.runtimeExplain.error.title"),
      body: runtime?.content || state.runtime.error || t("workflow.runtimeExplain.error.body")
    };
  }
  if (route.target || route.route) {
    return {
      tone: "route",
      title: t("workflow.runtimeExplain.route.title"),
      body: route.target
        ? t("workflow.runtimeExplain.route.target", { route: route.route || t("workflow.runtimeNoDetails"), target: route.target })
        : t("workflow.runtimeExplain.route.value", { route: route.route })
    };
  }
  if (stateName === "success") {
    return {
      tone: "success",
      title: t("workflow.runtimeExplain.success.title"),
      body: runtime?.content ? t("workflow.runtimeExplain.success.bodyWithSummary") : t("workflow.runtimeExplain.success.body")
    };
  }
  return {
    tone: "running",
    title: t("workflow.runtimeExplain.running.title"),
    body: t("workflow.runtimeExplain.running.body")
  };
}

function runtimeMetaLine() {
  if (state.runtime.lastStage) return `${t("workflow.runtimeSummary.lastUpdated")} / ${workflowDisplayValue(state.runtime.lastStage)}`;
  if (state.runtime.runInput) return t("workflow.runtimeSummary.inputBound");
  return t("workflow.runtimeSummary.waiting");
}

function summaryMetric(label, value) {
  return `<div class="workflow-runtime-metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(String(value))}</strong></div>`;
}

function detailChip(label, value) {
  return `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(String(value))}</strong></span>`;
}

function renderValueBlock(label, value) {
  if (value === null || value === undefined || value === "") return "";
  return runtimeBlock(label, `<pre>${escapeHTML(formatRuntimeValue(value))}</pre>`);
}

function runtimeTechnicalDetails(blocks, open = false) {
  if (!blocks.length) return "";
  return `<details class="workflow-runtime-technical"${open ? " open" : ""}>
    <summary>
      <span>
        <strong>${escapeHTML(t("workflow.runtimeTechnicalTitle"))}</strong>
        <small>${escapeHTML(t("workflow.runtimeTechnicalHelp"))}</small>
      </span>
      <i>${escapeHTML(t("workflow.runtimeTechnicalCount", { count: blocks.length }))}</i>
    </summary>
    <div class="workflow-runtime-technical-body">${blocks.join("")}</div>
  </details>`;
}

function runtimeBlock(label, body) {
  return `<div class="workflow-runtime-block"><span>${escapeHTML(label)}</span>${body}</div>`;
}

function renderWorkflowAcceptanceBlock(items = []) {
  if (!items.length) return "";
  const summary = workflowAcceptanceSummary(items);
  return `<div class="workflow-runtime-block workflow-runtime-acceptance">
    <span>${escapeHTML(t("workflow.runtimeField.acceptance"))}</span>
    <div class="workflow-runtime-acceptance-head">
      <strong>${escapeHTML(t("workflow.runtimeAcceptanceTitle"))}</strong>
      <small>${escapeHTML(summary)}</small>
    </div>
    <div class="run-acceptance-list">${items.slice(0, 4).map(workflowAcceptanceItem).join("")}</div>
  </div>`;
}

function workflowAcceptanceItem(item = {}) {
  const status = item.status || "";
  const title = workflowDisplayText(item.name || item.description || t("workflow.runtimeAcceptanceCriterion"));
  const body = workflowDisplayText(item.reason || item.actual || item.expected || t("workflow.runtimeAcceptanceNoDetail"));
  const meta = [
    item.ref ? `${t("workflow.runtimeAcceptanceRef")}: ${workflowDisplayValue(item.ref)}` : "",
    item.expected ? `${t("workflow.runtimeAcceptanceExpected")}: ${workflowDisplayText(item.expected)}` : "",
    item.actual ? `${t("workflow.runtimeAcceptanceActual")}: ${workflowDisplayText(item.actual)}` : ""
  ].filter(Boolean);
  return `<article class="run-acceptance-item ${workflowAcceptanceToneClass(status)}">
    <div>
      <strong>${escapeHTML(title)}</strong>
      <span>${escapeHTML(workflowAcceptanceStatusLabel(status))}</span>
    </div>
    <p>${escapeHTML(truncateWorkflowText(body, 220))}</p>
    ${meta.length ? `<small>${escapeHTML(meta.join(" / "))}</small>` : ""}
  </article>`;
}

function workflowAcceptanceSummary(items = []) {
  const counts = items.reduce((acc, item) => {
    const key = workflowAcceptanceStatusKey(item?.status);
    acc[key] = (acc[key] || 0) + 1;
    return acc;
  }, {});
  return [
    counts.pass ? t("workflow.runtimeAcceptancePassedCount", { count: counts.pass }) : "",
    counts.fail ? t("workflow.runtimeAcceptanceFailedCount", { count: counts.fail }) : "",
    counts.warn ? t("workflow.runtimeAcceptanceWarningCount", { count: counts.warn }) : "",
    counts.unknown ? t("workflow.runtimeAcceptanceUnknownCount", { count: counts.unknown }) : ""
  ].filter(Boolean).join(" / ");
}

function workflowAcceptanceStatusKey(status) {
  const value = String(status || "").toLowerCase();
  if (["pass", "passed", "ok", "success", "true"].includes(value)) return "pass";
  if (["fail", "failed", "error", "false"].includes(value)) return "fail";
  if (["warn", "warning", "partial"].includes(value)) return "warn";
  return "unknown";
}

function workflowAcceptanceToneClass(status) {
  const key = workflowAcceptanceStatusKey(status);
  if (key === "pass") return "acceptance-pass";
  if (key === "fail") return "acceptance-fail";
  if (key === "warn") return "acceptance-warn";
  return "acceptance-unknown";
}

function workflowAcceptanceToneFromItems(items = []) {
  const keys = new Set(items.map(item => workflowAcceptanceStatusKey(item?.status)));
  if (keys.has("fail")) return "bad";
  if (keys.has("warn")) return "warn";
  if (keys.has("pass")) return "good";
  return "neutral";
}

function workflowAcceptanceStatusLabel(status) {
  const key = `workflow.runtimeAcceptanceStatus.${workflowAcceptanceStatusKey(status)}`;
  const translated = t(key);
  return translated === key ? String(status || t("common.none")) : translated;
}

function formatRuntimeValue(value) {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function formatRouteText(route) {
  const parts = [];
  if (route.route) parts.push(workflowDisplayValue(route.route));
  if (route.target) parts.push(`-> ${workflowDisplayValue(route.target)}`);
  if (route.value !== undefined && route.value !== null && route.value !== "") parts.push(`(${workflowDisplayValue(route.value)})`);
  if (route.passed !== undefined && route.passed !== null && route.passed !== "") parts.push(route.passed ? t("workflow.branchPassed") : t("workflow.branchBlocked"));
  return parts.join(" ") || t("workflow.runtimeNoDetails");
}

function paletteButton(option) {
  const template = option.type;
  const group = nodeLibraryGroup(option);
  const tags = Array.isArray(option.tags) && option.tags.length
    ? `<small>${escapeHTML(option.tags.slice(0, 3).map(workflowDisplayValue).join(" / "))}</small>`
    : "";
  const fields = Array.isArray(option.fields) ? option.fields.length : 0;
  const warnings = Array.isArray(option.warnings) ? option.warnings.length : 0;
  const hints = Array.isArray(option.hints) ? option.hints.length : 0;
  const chips = [
    isCustomWorkflowMetadata(option) ? t("workflow.nodeTypeCustom") : "",
    fields ? t("workflow.nodePaletteFields", { count: fields }) : "",
    hints ? t("workflow.nodePaletteHints", { count: hints }) : "",
    warnings ? t("workflow.nodePaletteWarnings", { count: warnings }) : ""
  ].filter(Boolean);
  return `<button type="button" class="palette-${escapeHTML(template)} palette-${escapeHTML(group)}" draggable="true" data-template="${escapeHTML(template)}" data-node-type="${escapeHTML(template)}">
    <i></i><strong>${escapeHTML(nodeTypeLabel(option))}</strong><span>${escapeHTML(nodeTypeHelp(option))}</span>
    ${tags}
    ${chips.length ? `<div class="palette-meta">${chips.slice(0, 3).map(chip => `<em>${escapeHTML(chip)}</em>`).join("")}</div>` : ""}
  </button>`;
}

function renderNodePalette() {
  const groups = nodeLibraryGroups();
  if (!groups.length) {
    return `<div class="workflow-palette-empty">${escapeHTML(t("workflow.nodePaletteNoMatches"))}</div>`;
  }
  return groups.map(group => `
    <section class="palette-section palette-section-${escapeHTML(group.id)}">
      <div class="palette-section-head">
        <strong>${escapeHTML(t(`workflow.library.group.${group.id}`))}</strong>
        <span>${escapeHTML(t("workflow.library.groupCount", { count: group.options.length }))}</span>
      </div>
      <div class="palette-section-grid">
        ${group.options.map(option => paletteButton(option)).join("")}
      </div>
    </section>`).join("");
}

function refreshNodePalette(root) {
  const palette = root.querySelector(".node-palette");
  const status = root.querySelector("#nodePaletteFilterStatus");
  const input = root.querySelector("#nodePaletteSearch");
  if (!palette) return;
  if (input && input.value !== state.nodePaletteFilter) input.value = state.nodePaletteFilter;
  palette.innerHTML = renderNodePalette();
  if (status) {
    const total = nodeTypeOptions().length;
    const matched = filteredNodeTypeOptions().length;
    const text = state.nodePaletteFilter
      ? t("workflow.nodePaletteFilteredCount", { matched, total })
      : t("workflow.nodePaletteCount", { count: total });
    status.textContent = text;
    status.className = `badge ${state.nodePaletteFilter ? "info" : "neutral"}`;
  }
}

function nodeLibraryGroups() {
  const order = ["visual", "execute", "control", "quality", "integration"];
  const buckets = new Map(order.map(id => [id, []]));
  for (const option of filteredNodeTypeOptions()) {
    const group = nodeLibraryGroup(option);
    if (!buckets.has(group)) buckets.set(group, []);
    buckets.get(group).push(option);
  }
  return [...buckets.entries()]
    .filter(([, options]) => options.length)
    .map(([id, options]) => ({ id, options }));
}

function filteredNodeTypeOptions() {
  const filter = normalizeNodePaletteSearch(state.nodePaletteFilter);
  const options = nodeTypeOptions();
  if (!filter) return options;
  return options.filter(option => normalizeNodePaletteSearch(nodePaletteSearchText(option)).includes(filter));
}

function nodePaletteSearchText(option) {
  const values = [
    option?.type,
    nodeTypeLabel(option),
    nodeTypeHelp(option),
    option?.category,
    option?.source,
    ...(Array.isArray(option?.tags) ? option.tags : []),
    ...(Array.isArray(option?.hints) ? option.hints : []),
    ...(Array.isArray(option?.warnings) ? option.warnings : []),
    ...(Array.isArray(option?.examples) ? option.examples.map(example => [example?.name, example?.title, example?.description, example?.expression].filter(Boolean).join(" ")) : []),
    ...(Array.isArray(option?.fields) ? option.fields.map(field => [field?.name, field?.label, field?.description].filter(Boolean).join(" ")) : []),
    ...(Array.isArray(option?.outputs) ? option.outputs.map(output => [output?.name, output?.label, output?.description].filter(Boolean).join(" ")) : [])
  ];
  return values.filter(Boolean).join(" ");
}

function normalizeNodePaletteSearch(value) {
  return String(value || "").trim().toLowerCase();
}

function isCustomWorkflowMetadata(item = {}) {
  const source = String(item?.source || "").trim().toLowerCase();
  return item?.custom === true || source === "custom" || source === "custom_metadata";
}

function nodeLibraryGroup(optionOrType) {
  const option = typeof optionOrType === "object" && optionOrType ? optionOrType : null;
  const type = option ? option.type : optionOrType;
  const category = String(option?.category || "").trim();
  if (["quality_gate", "quality_guard", "policy_guard", "guard"].includes(type)) return "quality";
  if (["tool", "team", "agent_team", "team_template", "sub_workflow", "subworkflow", "workflow"].includes(type)) return "integration";
  if (["visual", "execute", "control", "quality", "integration"].includes(category)) return category;
  if (category === "data") return "integration";
  if (visualTypes.has(type)) return "visual";
  if (controlTypes.has(type)) return "control";
  return "execute";
}

function nodeTypeOptions() {
  const remote = (state.options?.node_types || [])
    .filter(option => option?.type)
    .map(option => ({ ...option, type: String(option.type).trim() }))
    .filter(option => option.type);
  if (remote.length) return remote;
  return [
    { type: "start", label: t("workflow.node.start"), description: t("workflow.node.startHelp"), category: "visual" },
    { type: "agent", label: t("workflow.node.agent"), description: t("workflow.node.agentHelp"), category: "execute" },
    { type: "skill", label: t("workflow.node.skill"), description: t("workflow.node.skillHelp"), category: "execute" },
    { type: "tool", label: t("workflow.node.tool"), description: t("workflow.node.toolHelp"), category: "execute" },
    { type: "team", label: t("workflow.node.team"), description: t("workflow.node.teamHelp"), category: "execute" },
    { type: "condition", label: t("workflow.node.condition"), description: t("workflow.node.conditionHelp"), category: "control" },
    { type: "switch", label: t("workflow.node.switch"), description: t("workflow.node.switchHelp"), category: "control" },
    { type: "policy_guard", label: t("workflow.node.policy_guard"), description: t("workflow.node.policy_guardHelp"), category: "control" },
    { type: "parallel", label: t("workflow.node.parallel"), description: t("workflow.node.parallelHelp"), category: "control" },
    { type: "join", label: t("workflow.node.join"), description: t("workflow.node.joinHelp"), category: "control" },
    { type: "input_gate", label: t("workflow.node.input_gate"), description: t("workflow.node.input_gateHelp"), category: "control" },
    { type: "for_each", label: t("workflow.node.for_each"), description: t("workflow.node.for_eachHelp"), category: "control" },
    { type: "loop", label: t("workflow.node.loop"), description: t("workflow.node.loopHelp"), category: "control" },
    { type: "sub_workflow", label: t("workflow.node.sub_workflow"), description: t("workflow.node.sub_workflowHelp"), category: "control" },
    { type: "checkpoint", label: t("workflow.node.checkpoint"), description: t("workflow.node.checkpointHelp"), category: "control" },
    { type: "custom", label: t("workflow.node.custom"), description: t("workflow.node.customHelp"), category: "execute" },
    { type: "end", label: t("workflow.node.end"), description: t("workflow.node.endHelp"), category: "visual" }
  ];
}

function policyRuleOptions() {
  const remote = (state.options?.policy_rules || [])
    .filter(rule => rule?.name)
    .map(rule => ({ ...rule, name: String(rule.name).trim() }))
    .filter(rule => rule.name);
  if (remote.length) return remote;
  return [
    { name: "expression", label: t("catalog.policyOperator.expression"), operator: "expression", description: t("workflow.policyRuleExpressionHelp") },
    { name: "ref_truthy", label: t("catalog.policyOperator.ref_truthy"), operator: "ref_truthy", description: t("workflow.policyRuleRefTruthyHelp") },
    { name: "contains", label: t("catalog.policyOperator.contains"), operator: "contains", description: t("workflow.policyRuleContainsHelp") },
    { name: "min_count", label: t("catalog.policyOperator.min_count"), operator: "min_count", description: t("workflow.policyRuleMinCountHelp") },
    { name: "risk_at_least", label: t("catalog.policyOperator.risk_at_least"), operator: "risk_at_least", description: t("workflow.policyRuleRiskHelp") }
  ];
}

function policyRuleLabel(option) {
  const label = option?.label || option?.name || "";
  const source = option?.source === "custom" ? t("workflow.policyRuleCustom") : "";
  const localized = localizedText(label);
  return source ? `${localized} / ${source}` : localized;
}

function selectedPolicyRuleName(stage) {
  const paramsRule = String(stage?.params?.rule || stage?.params?.policy_rule || stage?.params?.guard_rule || "").trim();
  if (paramsRule) return paramsRule;
  const policy = String(stage?.policy || "").trim().toLowerCase();
  if (["ref_truthy", "contains", "min_count", "risk_at_least"].includes(policy)) return policy;
  return "expression";
}

function updatePolicyRuleHelp(root) {
  const help = root.querySelector("#stagePolicyRuleHelp");
  const select = root.querySelector("#stagePolicyRule");
  if (!help || !select) return;
  const option = policyRuleOptions().find(rule => rule.name === select.value) || policyRuleOptions()[0];
  if (!option) {
    help.innerHTML = "";
    return;
  }
  const params = Array.isArray(option.params) && option.params.length
    ? option.params.map(param => localizedText(param.label || param.name)).filter(Boolean).join(", ")
    : t("workflow.policyRuleNoParams");
  help.innerHTML = `
    <span>${escapeHTML(policyOperatorLabel(option.operator || option.name || ""))}</span>
    <strong>${escapeHTML(localizedText(option.description || t("workflow.policyRuleHelp")))}</strong>
    <small>${escapeHTML(t("workflow.policyRuleParams", { params }))}</small>`;
}

function policyOperatorLabel(value) {
  const raw = String(value || "");
  if (!raw) return "";
  const key = `catalog.policyOperator.${raw}`;
  const translated = t(key);
  return translated === key ? raw : translated;
}

function nodeTypeOption(type) {
  return nodeTypeOptions().find(option => option.type === type) || { type, label: type, description: "", category: nodeTypeCategory(type) };
}

function nodeTypeLabel(option) {
  const type = option.type === "quality_guard" ? "quality_gate" : option.type;
  const key = `workflow.node.${type}`;
  const translated = t(key);
  return translated === key ? workflowDisplayValue(option.label || type) : translated;
}

function nodeTypeHelp(option) {
  const type = option.type === "quality_guard" ? "quality_gate" : option.type;
  const key = `workflow.node.${type}Help`;
  const translated = t(key);
  return translated === key ? workflowDisplayText(option.description || "") : translated;
}

function nodeTypeCategory(type) {
  const option = (state.options?.node_types || []).find(item => item.type === type);
  if (option?.category) return option.category;
  if (controlTypes.has(type)) return "control";
  if (visualTypes.has(type)) return "visual";
  return "execute";
}

async function loadWorkflowList() {
  const endpoint = workflowGraphCollectionEndpoint();
  state.workflows = endpoint ? normalizeWorkflowGraphSummaries(await request(endpoint)) : [];
}

async function loadWorkflowTemplates() {
  const fallback = "/api/workflow-templates";
  const endpoints = workflowTemplateCollectionEndpoints(fallback);
  const payloads = [state.options.templates];
  const loaded = new Map();
  for (const endpoint of endpoints) {
    try {
      loaded.set(endpoint, await request(endpoint));
    } catch {
      // Older runtimes may only publish templates through workflow options.
    }
  }
  if (loaded.has(fallback)) payloads.push(loaded.get(fallback));
  for (const endpoint of endpoints) {
    if (endpoint !== fallback && loaded.has(endpoint)) payloads.push(loaded.get(endpoint));
  }
  state.workflowTemplates = mergeWorkflowTemplateSummaries(payloads);
}

async function openRequestedWorkflow() {
  const requestedTemplate = localStorage.getItem("goflow.workflow.template");
  if (requestedTemplate) {
    localStorage.removeItem("goflow.workflow.template");
    await applyWorkflowTemplate(null, requestedTemplate, { force: true });
    return;
  }
  const requested = localStorage.getItem("goflow.workflow.open");
  if (!requested) return;
  localStorage.removeItem("goflow.workflow.open");
  try {
    const endpoint = workflowGraphDetailEndpoint(requested, `/api/workflow-graphs/${encodeURIComponent(requested)}`);
    if (!endpoint) throw new Error(t("workflow.graphUnavailable"));
    const doc = await request(endpoint);
    state.graph = normalizeGraph(doc);
    state.selected = state.graph.stages.length ? 0 : -1;
    state.connectSource = "";
  } catch {
    createPresetGraph();
    state.graph.name = requested;
  }
}

function bind(root) {
  const canvas = root.querySelector("#canvas");
  bindWorkflowInspectorResizer(root);
  root.querySelector("#newGraph").onclick = () => { createPresetGraph(); clearWorkflowValidation(root); clearWorkflowTransfer(root); renderAll(root); };
  root.querySelector("#saveGraph").onclick = () => saveGraph(root);
  root.querySelector("#deleteGraph").onclick = () => deleteGraph(root);
  root.querySelector("#runGraph").onclick = () => runGraph(root);
  root.querySelector("#validateGraph").onclick = () => validateGraph(root);
  root.querySelector("#exportGraph").onclick = () => exportGraph(root);
  root.querySelector("#importGraph").onclick = () => root.querySelector("#importGraphFile").click();
  root.querySelector("#importGraphFile").onchange = event => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file) importGraphFile(root, file);
  };
  root.querySelector("#zoomOut").onclick = () => setCanvasZoom(root, state.zoom - zoomStep());
  root.querySelector("#zoomIn").onclick = () => setCanvasZoom(root, state.zoom + zoomStep());
  root.querySelector("#zoomReset").onclick = () => resetCanvasZoom(root);
  root.querySelector("#zoomFit").onclick = () => fitCanvas(root);
  root.querySelector("#autoLayoutGraph").onclick = () => autoLayoutGraph(root);
  root.querySelector("#focusCanvas").onclick = () => toggleWorkflowFocusMode(root);
  root.querySelector("#graphName").oninput = () => { state.graph.name = slug(root.querySelector("#graphName").value); clearWorkflowValidation(root); clearWorkflowTransfer(root); renderWorkflowList(root); };
  root.querySelector("#graphDescription").oninput = () => { state.graph.description = root.querySelector("#graphDescription").value; clearWorkflowTransfer(root); };
  root.querySelector("#workflowTemplateSearch").oninput = event => {
    state.workflowTemplateFilter.query = event.target.value;
    renderWorkflowTemplateList(root);
  };
  root.querySelector("#workflowTemplateCategory").onchange = event => {
    state.workflowTemplateFilter.category = event.target.value;
    renderWorkflowTemplateList(root);
  };
  root.querySelector("#nodePaletteSearch").oninput = event => {
    state.nodePaletteFilter = event.target.value;
    refreshNodePalette(root);
  };
  const palette = root.querySelector(".node-palette");
  palette.ondragstart = event => {
    const button = event.target.closest("button[data-template]");
    if (button) event.dataTransfer.setData("text/plain", button.dataset.template);
  };
  palette.onclick = event => {
    const button = event.target.closest("button[data-template]");
    if (!button) return;
    addStageFromTemplate(button.dataset.template);
    clearWorkflowValidation(root);
    clearWorkflowTransfer(root);
    renderAll(root);
  };
  canvas.ondragover = event => event.preventDefault();
  canvas.onwheel = event => {
    if (!event.ctrlKey && !event.metaKey) return;
    event.preventDefault();
    const direction = event.deltaY < 0 ? 1 : -1;
    zoomCanvasAt(root, state.zoom + direction * zoomStep(), event);
  };
  canvas.onmousedown = event => {
    if (event.button !== 0) return;
    const target = event.target instanceof Element ? event.target : null;
    if (target?.closest(".flow-node, .node-port, .edge-action, button, input, textarea, select, .canvas-guide")) return;
    if (state.connectSource) {
      state.connectSource = "";
      event.preventDefault();
      renderStageForm(root);
      renderCanvas(root);
      return;
    }
    state.panning = {
      x: event.clientX,
      y: event.clientY,
      left: canvas.scrollLeft,
      top: canvas.scrollTop
    };
    canvas.classList.add("panning");
    event.preventDefault();
  };
  canvas.ondrop = event => {
    event.preventDefault();
    const template = event.dataTransfer.getData("text/plain");
    if (!template) return;
    const point = graphPoint(root, event);
    addStageFromTemplate(template, point.x, point.y);
    clearWorkflowValidation(root);
    clearWorkflowTransfer(root);
    renderAll(root);
  };
  canvas.onkeydown = event => handleCanvasKeyboard(root, event);
  [
    "stageNodeType",
    "stageName",
    "stageAgent",
    "stageSkill",
    "stageTool",
    "stageNext",
    "stageNextStrategy",
    "stageCondition",
    "stagePolicyRule",
    "stagePolicy",
    "stageSwitchOn",
    "stageRoutes",
    "stageCases",
    "stageInputFieldsJson",
    "stageParamTeam",
    "stageTeamQuorumPreset",
    "stageParamWorkflow",
    "stageParamRequest",
    "stageParamItems",
    "stageParamStage",
    "stageParamUntil",
    "stageParamMaxIterations",
    "stageParamWaitFor",
    "stageParamPrompt",
    "stageInputMap",
    "stageOutputsMap",
    "stageParams",
    "stageTeamExecute",
    "stageApproval"
  ].forEach(id => {
    const input = root.querySelector(`#${id}`);
    input.addEventListener("input", () => syncStageFromForm(root));
    input.addEventListener("change", () => syncStageFromForm(root));
    if (input.dataset.expressionField) {
      input.addEventListener("focus", () => {
        state.expressionAssist.field = input.dataset.expressionField;
        scheduleExpressionValidation(root, { immediate: true });
      });
    }
  });
  root.querySelector("#stageInputFieldsJson").addEventListener("input", () => renderInputFieldsBuilder(root));
  root.querySelector("#stageInputFieldsBuilder").addEventListener("input", event => {
    if (!(event.target instanceof Element) || !event.target.closest("[data-input-field-item]")) return;
    syncInputFieldsBuilderToJSON(root);
    updateInputFieldsPreview(root);
    syncStageFromForm(root);
  });
  root.querySelector("#stageInputFieldsBuilder").addEventListener("change", event => {
    if (!(event.target instanceof Element) || !event.target.closest("[data-input-field-item]")) return;
    syncInputFieldsBuilderToJSON(root);
    updateInputFieldsPreview(root);
    syncStageFromForm(root);
  });
  root.querySelector("#stageInputFieldsBuilder").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-input-field-action]") : null;
    if (!button) return;
    const action = button.dataset.inputFieldAction || "";
    const fields = readInputFieldsBuilder(root);
    if (action === "add") {
      fields.push(createInputFieldDraft(fields.length));
    } else if (action === "remove") {
      const index = Number.parseInt(button.dataset.inputFieldIndex || "", 10);
      if (Number.isFinite(index)) fields.splice(index, 1);
    }
    writeInputFieldsJSON(root, fields);
    renderInputFieldsBuilder(root);
    syncStageFromForm(root);
  });
  root.querySelector("#stageAdvancedPanel").addEventListener("toggle", () => {
    scheduleExpressionValidation(root, { immediate: true });
  });
  root.querySelector("#stageExpressionAssist").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-expression-suggestion]") : null;
    if (!button) return;
    insertExpressionSuggestion(root, button.dataset.expressionSuggestion || "");
  });
  root.querySelector("#stageDataFlow").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-copy-stage-ref]") : null;
    if (!button) return;
    addDataFlowReferenceToStageInput(root, button.dataset.copyStageRef || "");
  });
  root.querySelector("#stageNodeTypeMeta").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-node-type-action]") : null;
    if (!button) return;
    const action = button.dataset.nodeTypeAction || "";
    if (action === "apply-default") {
      applyNodeTypeDefaultStage(root, button.dataset.nodeType || "");
      return;
    }
    if (action === "apply-example") {
      const index = Number.parseInt(button.dataset.exampleIndex || "", 10);
      if (!Number.isFinite(index)) return;
      applyNodeTypeExampleStage(root, button.dataset.nodeType || "", index);
    }
  });
  root.querySelector("#stageAdvancedGuide").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-advanced-guide-example]") : null;
    if (!button) return;
    applyAdvancedGuideExample(root, button.dataset.advancedGuideExample || "");
  });
  root.querySelector("#stageArtifactsGuide").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-artifact-guide-action]") : null;
    if (!button) return;
    addArtifactGuideExample(root, button.dataset.artifactGuideAction || "");
  });
  root.querySelector("#stageAcceptanceGuide").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-acceptance-guide-action]") : null;
    if (!button) return;
    addAcceptanceGuideExample(root, button.dataset.acceptanceGuideAction || "");
  });
  root.querySelector("#addArtifact").onclick = () => {
    const stage = selectedStage();
    if (!stage) return;
    stage.artifacts = normalizeArtifacts(stage.artifacts);
    stage.artifacts.push(createArtifactDraft(stage));
    renderArtifactsEditor(root, stage);
    updateStageArtifactsGuide(root, stage, normalizedNodeType(stage));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, stage, normalizedNodeType(stage));
    renderCanvas(root);
  };
  root.querySelector("#addAcceptanceCriterion").onclick = () => {
    const stage = selectedStage();
    if (!stage) return;
    stage.acceptance_criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria);
    stage.acceptance_criteria.push(createAcceptanceCriterionDraft(stage));
    renderAcceptanceEditor(root, stage);
    updateStageAcceptanceGuide(root, stage, normalizedNodeType(stage));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, stage, normalizedNodeType(stage));
    renderCanvas(root);
  };
  root.querySelector("#stageArtifactsList").addEventListener("input", () => {
    syncArtifactsFromForm(root);
    updateStageArtifactsGuide(root, selectedStage(), normalizedNodeType(selectedStage()));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, selectedStage(), normalizedNodeType(selectedStage()));
    scheduleWorkflowRepaint(root);
  });
  root.querySelector("#stageArtifactsList").addEventListener("change", () => {
    syncArtifactsFromForm(root);
    updateStageArtifactsGuide(root, selectedStage(), normalizedNodeType(selectedStage()));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, selectedStage(), normalizedNodeType(selectedStage()));
    scheduleWorkflowRepaint(root);
  });
  root.querySelector("#stageAcceptanceList").addEventListener("input", () => {
    syncAcceptanceCriteriaFromForm(root);
    updateStageAcceptanceGuide(root, selectedStage(), normalizedNodeType(selectedStage()));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, selectedStage(), normalizedNodeType(selectedStage()));
    scheduleWorkflowRepaint(root);
  });
  root.querySelector("#stageAcceptanceList").addEventListener("change", () => {
    syncAcceptanceCriteriaFromForm(root);
    updateStageAcceptanceGuide(root, selectedStage(), normalizedNodeType(selectedStage()));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, selectedStage(), normalizedNodeType(selectedStage()));
    scheduleWorkflowRepaint(root);
  });
  root.querySelector("#stageArtifactsList").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-remove-artifact]") : null;
    if (!button) return;
    const stage = selectedStage();
    if (!stage) return;
    const index = Number.parseInt(button.dataset.removeArtifact || "", 10);
    stage.artifacts = normalizeArtifacts(stage.artifacts).filter((_, itemIndex) => itemIndex !== index);
    renderArtifactsEditor(root, stage);
    updateStageArtifactsGuide(root, stage, normalizedNodeType(stage));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, stage, normalizedNodeType(stage));
    renderCanvas(root);
  });
  root.querySelector("#stageAcceptanceList").addEventListener("click", event => {
    const button = event.target instanceof Element ? event.target.closest("[data-remove-acceptance]") : null;
    if (!button) return;
    const stage = selectedStage();
    if (!stage) return;
    const index = Number.parseInt(button.dataset.removeAcceptance || "", 10);
    stage.acceptance_criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria).filter((_, itemIndex) => itemIndex !== index);
    renderAcceptanceEditor(root, stage);
    updateStageAcceptanceGuide(root, stage, normalizedNodeType(stage));
    updateWorkflowFormPanels(root);
    updateStagePlainSummary(root, stage, normalizedNodeType(stage));
    renderCanvas(root);
  });
  root.querySelector("#connectStage").onclick = () => {
    const stage = selectedStage();
    if (!stage) return;
    state.connectSource = state.connectSource === stage.name ? "" : stage.name;
    renderStageForm(root);
    renderCanvas(root);
  };
  root.querySelector("#removeStage").onclick = () => {
    removeSelectedStage();
    renderAll(root);
  };
  window.onmousemove = event => {
    if (state.connecting) {
      updateConnectionDraft(root, event);
      scheduleWorkflowEdgeRepaint(root);
      return;
    }
    if (state.panning) {
      canvas.scrollLeft = state.panning.left - (event.clientX - state.panning.x);
      canvas.scrollTop = state.panning.top - (event.clientY - state.panning.y);
      return;
    }
    if (!state.dragging) return;
    const point = graphPoint(root, event);
    const stage = state.graph.stages[state.dragging.index];
    if (!stage) return;
    stage.position.x = Math.round(Math.max(workflowNodeMetrics.leftPadding, point.x - state.dragging.dx));
    stage.position.y = Math.round(Math.max(workflowNodeMetrics.topPadding, point.y - state.dragging.dy));
    if (stageNeedsCanvasGeometry(stage)) applyCanvasGeometry(root);
    updateStageNodePosition(root, stage);
    scheduleWorkflowEdgeRepaint(root);
  };
  window.onmouseup = event => {
    if (state.connecting) {
      finishConnectionDrag(root, event);
      return;
    }
    const wasDragging = !!state.dragging;
    state.panning = null;
    canvas.classList.remove("panning");
    state.dragging = null;
    if (wasDragging) scheduleWorkflowRepaint(root);
  };
}

function handleCanvasKeyboard(root, event) {
  const target = event.target instanceof Element ? event.target : null;
  if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
  if (event.key === "Escape") {
    if (cancelWorkflowCanvasInteraction(root)) event.preventDefault();
    return;
  }
  if ((event.key === "Delete" || event.key === "Backspace") && selectedStage()) {
    event.preventDefault();
    removeSelectedStage();
    renderAll(root);
  }
}

function cancelWorkflowCanvasInteraction(root) {
  const hadInteraction = Boolean(state.connectSource || state.connecting || state.dragging || state.panning);
  state.connectSource = "";
  state.connecting = null;
  state.dragging = null;
  state.panning = null;
  root.querySelector("#canvas")?.classList.remove("panning");
  if (!hadInteraction) return false;
  renderStageForm(root);
  renderCanvas(root);
  return true;
}

function createPresetGraph() {
  state.graphValidation = null;
  state.graphTransfer = null;
  state.graph = {
    name: "plan-implement-audit",
    description: t("workflow.presetPlanImplementAuditDescription"),
    stages: [
      { name: "start", node_type: "start", next: ["plan"], position: { x: 60, y: 165 } },
      { name: "plan", node_type: "agent", agent: "planner", skill: "execution-plan", next: ["implement"], position: { x: 310, y: 150 } },
      { name: "implement", node_type: "agent", agent: "fixer", skill: "code-writing", approval: true, next: ["audit"], position: { x: 590, y: 150 } },
      { name: "audit", node_type: "skill", agent: "auditor", skill: "code-audit", next: ["end"], position: { x: 870, y: 150 } },
      { name: "end", node_type: "end", position: { x: 1150, y: 165 } }
    ]
  };
  state.selected = 0;
  state.connectSource = "";
}

function addStageFromTemplate(template, x, y) {
  state.graphValidation = null;
  const index = state.graph.stages.length + 1;
  const firstAgent = state.options.agents?.[0]?.name || "planner";
  const firstSkill = state.options.skills?.[0]?.name || "execution-plan";
  const firstTool = state.options.tools?.[0] || "";
  const remoteDefault = defaultStageForType(template);
  const presets = {
    start: { name: uniqueStageName("start"), node_type: "start" },
    agent: { name: uniqueStageName("agent"), node_type: "agent", agent: firstAgent, skill: firstSkill },
    skill: { name: uniqueStageName("skill"), node_type: "skill", agent: "planner", skill: firstSkill },
    tool: { name: uniqueStageName("tool"), node_type: "tool", agent: "fixer", skill: "code-writing", tool: firstTool, approval: true },
    team: { name: uniqueStageName("team"), node_type: "team", agent: firstAgent, params: { team: state.options.team_templates?.[0]?.name || "software-task-team" } },
    condition: { name: uniqueStageName("condition"), node_type: "condition", condition: `contains(previous.raw_output, "risk")`, routes: { true: "yes", false: "no" } },
    switch: { name: uniqueStageName("switch"), node_type: "switch", switch_on: "previous.summary", cases: { default: "next" } },
    policy_guard: { name: uniqueStageName("guard"), node_type: "policy_guard", policy: `contains(previous.raw_output, "approved")`, routes: { allow: "next", deny: "blocked" } },
    parallel: { name: uniqueStageName("parallel"), node_type: "parallel", next: ["branch-a", "branch-b"] },
    join: { name: uniqueStageName("join"), node_type: "join", next: ["next"] },
    input_gate: { name: uniqueStageName("collect"), node_type: "input_gate", params: { manual: "true", fields: "target,severity" }, next: ["next"] },
    for_each: { name: uniqueStageName("for-each"), node_type: "for_each", params: { stage: "process-item", items: "alpha,beta,gamma" }, next: ["next"] },
    loop: { name: uniqueStageName("loop"), node_type: "loop", params: { stage: "review", max_iterations: "3", until: `contains(previous.raw_output, "done")` }, next: ["next"] },
    sub_workflow: { name: uniqueStageName("sub-workflow"), node_type: "sub_workflow", params: { workflow: "plan-fix-audit", request: "workflow.input" }, next: ["next"] },
    checkpoint: { name: uniqueStageName("checkpoint"), node_type: "checkpoint", params: { prompt: t("workflow.checkpointPromptDefault") } },
    custom: { name: uniqueStageName("stage"), node_type: "custom", agent: firstAgent, skill: firstSkill },
    end: { name: uniqueStageName("end"), node_type: "end" }
  };
  const base = remoteDefault || presets[template] || { name: uniqueStageName(template || "stage"), node_type: template || "custom" };
  if (base.name) base.name = uniqueStageName(base.name);
  if (!base.node_type) base.node_type = template || "custom";
  if (executableTypes.has(base.node_type) && !base.agent) base.agent = firstAgent;
  if (["agent", "skill", "tool", "custom"].includes(base.node_type) && !base.skill) base.skill = firstSkill;
  if (base.node_type === "tool" && !base.tool) base.tool = firstTool;
  state.graph.stages.push({
    ...base,
    next: base.next || [],
    params: base.params || {},
    routes: base.routes || {},
    cases: base.cases || {},
    input: base.input || {},
    outputs: base.outputs || {},
    position: { x: Number.isFinite(x) ? x : 120 + index * 70, y: Number.isFinite(y) ? y : 120 + index * 50 }
  });
  state.selected = state.graph.stages.length - 1;
}

function defaultStageForType(type) {
  const source = nodeTypeOption(type)?.default_stage;
  if (!source || typeof source !== "object") return null;
  return JSON.parse(JSON.stringify(source));
}

function exampleStageForType(type, index) {
  const examples = Array.isArray(nodeTypeOption(type)?.examples) ? nodeTypeOption(type).examples : [];
  const source = examples[index]?.stage;
  if (!source || typeof source !== "object" || Array.isArray(source)) return null;
  return JSON.parse(JSON.stringify(source));
}

function normalizeStagePresetForApply(source, fallbackNodeType) {
  if (!source || typeof source !== "object" || Array.isArray(source)) return null;
  const preset = JSON.parse(JSON.stringify(source));
  const nodeType = fallbackNodeType || normalizedNodeType(preset);
  return {
    node_type: nodeType,
    agent: String(preset.agent || ""),
    skill: String(preset.skill || ""),
    tool: String(preset.tool || ""),
    next: Array.isArray(preset.next) ? preset.next.map(item => slug(item)).filter(Boolean) : [],
    next_strategy: String(preset.next_strategy || ""),
    condition: String(preset.condition || ""),
    policy: String(preset.policy || ""),
    switch_on: String(preset.switch_on || ""),
    routes: preset.routes && typeof preset.routes === "object" && !Array.isArray(preset.routes) ? preset.routes : {},
    cases: preset.cases && typeof preset.cases === "object" && !Array.isArray(preset.cases) ? preset.cases : {},
    input: preset.input && typeof preset.input === "object" && !Array.isArray(preset.input) ? preset.input : {},
    outputs: preset.outputs && typeof preset.outputs === "object" && !Array.isArray(preset.outputs) ? preset.outputs : {},
    params: preset.params && typeof preset.params === "object" && !Array.isArray(preset.params) ? preset.params : {},
    artifacts: normalizeArtifacts(preset.artifacts),
    acceptance_criteria: normalizeAcceptanceCriteria(preset.acceptance_criteria || preset.acceptance),
    approval: !!preset.approval
  };
}

function applyStagePresetToSelection(root, source, nodeType) {
  const stage = selectedStage();
  if (!stage) return;
  const preset = normalizeStagePresetForApply(source, nodeType || normalizedNodeType(stage));
  if (!preset) return;
  const keepName = stage.name || uniqueStageName(preset.node_type || "stage");
  const keepPosition = stage.position ? { ...stage.position } : undefined;
  for (const key of Object.keys(stage)) delete stage[key];
  Object.assign(stage, preset, {
    name: keepName,
    position: keepPosition
  });
  state.graphValidation = null;
  state.graphTransfer = null;
  pruneUnsupportedStageFields(stage, normalizedNodeType(stage));
  pruneEmptyStageFields(stage);
  renderAll(root);
  renderCanvas(root);
}

function applyNodeTypeDefaultStage(root, nodeType) {
  const preset = defaultStageForType(nodeType);
  if (!preset) return;
  applyStagePresetToSelection(root, preset, nodeType);
}

function applyNodeTypeExampleStage(root, nodeType, index) {
  const preset = exampleStageForType(nodeType, index);
  if (!preset) return;
  applyStagePresetToSelection(root, preset, nodeType);
}

function renderAll(root) {
  cancelScheduledWorkflowRepaint();
  applyWorkflowFocusMode(root);
  root.querySelector("#graphName").value = state.graph.name || "";
  root.querySelector("#graphDescription").value = state.graph.description || "";
  updateWorkflowToolbarStatus(root);
  updateZoomLabel(root);
  renderWorkflowList(root);
  renderWorkflowTemplateList(root);
  refreshNodePalette(root);
  fillSelect(root.querySelector("#stageAgent"), (state.options.agents || []).map(item => item.name));
  fillSelect(root.querySelector("#stageSkill"), (state.options.skills || []).map(item => item.name));
  fillSelect(root.querySelector("#stageTool"), state.options.tools || []);
  fillTeamTemplateSelect(root.querySelector("#stageParamTeam"));
  renderCanvas(root);
  renderStageForm(root);
  renderExecutionOrder(root);
  renderWorkflowValidation(root);
  renderWorkflowTransfer(root);
  renderRuntime(root);
}

function toggleWorkflowFocusMode(root) {
  state.focusMode = !state.focusMode;
  applyWorkflowFocusMode(root);
  window.setTimeout(() => {
    applyCanvasGeometry(root);
    fitCanvas(root);
  }, 60);
}

function applyWorkflowFocusMode(root) {
  const studio = root.querySelector(".studio");
  studio?.classList.toggle("focus-mode", state.focusMode);
  document.querySelector("[data-app-shell]")?.classList.toggle("workflow-focus-mode", state.focusMode);
  const button = root.querySelector("#focusCanvas");
  if (button) {
    button.textContent = state.focusMode ? t("workflow.exitFocusMode") : t("workflow.focusMode");
    button.setAttribute("aria-pressed", state.focusMode ? "true" : "false");
  }
}

function scheduleWorkflowRepaint(root) {
  if (workflowRepaintFrame) return;
  workflowRepaintFrame = window.requestAnimationFrame(() => {
    workflowRepaintFrame = 0;
    renderCanvas(root);
    renderExecutionOrder(root);
  });
}

function scheduleWorkflowEdgeRepaint(root) {
  if (workflowEdgeRepaintFrame) return;
  workflowEdgeRepaintFrame = window.requestAnimationFrame(() => {
    workflowEdgeRepaintFrame = 0;
    drawEdges(root);
  });
}

function scheduleWorkflowRuntimeRefresh(root, options = {}) {
  workflowRuntimeRefreshNeedsRender = workflowRuntimeRefreshNeedsRender || options.render !== false;
  workflowRuntimeRefreshNeedsRepaint = workflowRuntimeRefreshNeedsRepaint || options.repaint !== false;
  workflowRuntimeRefreshNeedsSave = workflowRuntimeRefreshNeedsSave || !!options.save;
  if (workflowRuntimeRefreshFrame) return;
  workflowRuntimeRefreshFrame = window.requestAnimationFrame(() => {
    const needsRender = workflowRuntimeRefreshNeedsRender;
    const needsRepaint = workflowRuntimeRefreshNeedsRepaint;
    const needsSave = workflowRuntimeRefreshNeedsSave;
    workflowRuntimeRefreshFrame = 0;
    workflowRuntimeRefreshNeedsRender = false;
    workflowRuntimeRefreshNeedsRepaint = false;
    workflowRuntimeRefreshNeedsSave = false;
    if (needsRender) renderRuntime(root);
    if (needsRepaint) scheduleWorkflowRepaint(root);
    if (needsSave) saveWorkflowStudioActiveRun();
  });
}

function cancelScheduledWorkflowRepaint() {
  if (workflowRepaintFrame) {
    window.cancelAnimationFrame(workflowRepaintFrame);
    workflowRepaintFrame = 0;
  }
  if (workflowEdgeRepaintFrame) {
    window.cancelAnimationFrame(workflowEdgeRepaintFrame);
    workflowEdgeRepaintFrame = 0;
  }
  if (workflowRuntimeRefreshFrame) {
    window.cancelAnimationFrame(workflowRuntimeRefreshFrame);
    workflowRuntimeRefreshFrame = 0;
    workflowRuntimeRefreshNeedsRender = false;
    workflowRuntimeRefreshNeedsRepaint = false;
    workflowRuntimeRefreshNeedsSave = false;
  }
}

function updateWorkflowToolbarStatus(root) {
  const count = state.graph.stages.length;
  const stageCount = root.querySelector("#stageCount");
  if (stageCount) stageCount.textContent = `${count} ${t("workflow.stages")}`;
  const readiness = root.querySelector("#workflowReadinessBadge");
  if (!readiness) return;
  const summary = workflowReadinessSummary();
  readiness.className = `workflow-readiness-badge ${summary.tone}`;
  readiness.textContent = summary.label;
  readiness.title = summary.title;
}

function workflowReadinessSummary() {
  const stages = Array.isArray(state.graph.stages) ? state.graph.stages : [];
  if (!stages.length) {
    return {
      tone: "neutral",
      label: t("workflow.readiness.empty"),
      title: t("workflow.readiness.emptyHelp")
    };
  }
  let errors = 0;
  let warnings = 0;
  for (const stage of stages) {
    const status = nodeSetupStatus(stage, normalizedNodeType(stage));
    if (status.tone === "error") errors += 1;
    if (status.tone === "warn") warnings += 1;
  }
  if (errors) {
    return {
      tone: "error",
      label: t("workflow.readiness.error", { count: errors }),
      title: t("workflow.readiness.errorHelp", { errors, warnings })
    };
  }
  if (warnings) {
    return {
      tone: "warn",
      label: t("workflow.readiness.warn", { count: warnings }),
      title: t("workflow.readiness.warnHelp", { count: warnings })
    };
  }
  return {
    tone: "ready",
    label: t("workflow.readiness.ready"),
    title: t("workflow.readiness.readyHelp")
  };
}

function renderWorkflowList(root) {
  const list = root.querySelector("#workflowList");
  list.innerHTML = "";
  for (const workflow of state.workflows || []) {
    const item = document.createElement("button");
    item.className = "flow-card" + (workflow.name === state.graph.name ? " active" : "");
    item.innerHTML = `<strong>${escapeHTML(workflow.name)}</strong><span>${escapeHTML(localizedText(workflow.source || ""))} - ${workflow.stages || 0} ${t("workflow.stages")}</span>`;
    item.onclick = async () => {
      const endpoint = workflowGraphDetailEndpoint(workflow.name, `/api/workflow-graphs/${encodeURIComponent(workflow.name)}`);
      if (!endpoint) return;
      const doc = await request(endpoint);
      state.graph = normalizeGraph(doc);
      state.selected = state.graph.stages.length ? 0 : -1;
      state.connectSource = "";
      clearWorkflowValidation(root);
      clearWorkflowTransfer(root);
      renderAll(root);
    };
    list.appendChild(item);
  }
}

function renderWorkflowTemplateList(root) {
  const panel = root.querySelector("#workflowTemplatePanel");
  const list = root.querySelector("#workflowTemplateList");
  const count = root.querySelector("#workflowTemplateCount");
  const search = root.querySelector("#workflowTemplateSearch");
  const category = root.querySelector("#workflowTemplateCategory");
  const templates = state.workflowTemplates || [];
  if (!panel || !list) return;
  panel.classList.toggle("hidden", !templates.length);
  if (search && search.value !== state.workflowTemplateFilter.query) search.value = state.workflowTemplateFilter.query || "";
  renderWorkflowTemplateCategoryOptions(category, templates);
  const filtered = filteredWorkflowTemplates(templates);
  if (count) count.textContent = t("workflow.templateFilteredCount", { shown: filtered.length, total: templates.length });
  list.innerHTML = "";
  if (!filtered.length) {
    list.innerHTML = `<div class="workflow-template-empty">${escapeHTML(t("workflow.templateNoMatches"))}</div>`;
    return;
  }
  for (const template of filtered) {
    const button = document.createElement("button");
    button.type = "button";
    const badge = workflowTemplateBadge(template);
    const hint = workflowTemplateHint(template);
    const nodeTypes = workflowTemplateNodeTypeChips(template);
    const resourceRefs = workflowTemplateResourceRefs(template);
    const capabilities = workflowTemplateCapabilityChips(template);
    button.className = `workflow-template-card ${badge ? "workflow-template-card-featured" : ""}`.trim();
    const tags = Array.isArray(template.tags) ? template.tags.slice(0, 3).map(localizedText).filter(Boolean) : [];
    button.innerHTML = `
      <div class="workflow-template-title-row">
        <strong>${escapeHTML(localizedText(template.title || template.name))}</strong>
        ${badge ? `<span class="workflow-template-badge ${escapeHTML(badge.tone)}">${escapeHTML(badge.label)}</span>` : ""}
      </div>
      <small>${escapeHTML(localizedText(template.description || t("workflow.templateNoDescription")))}</small>
      ${hint ? `<div class="workflow-template-hint">${escapeHTML(hint)}</div>` : ""}
      <div class="workflow-template-meta">
        <span>${escapeHTML(workflowTemplateCategoryLabel(template.category || template.source || t("workflow.templateStarter")))}</span>
        <em>${escapeHTML(t("workflow.templateStageCount", { count: template.stages || 0 }))}</em>
      </div>
      ${nodeTypes.length ? `
        <div class="workflow-template-section">
          <b>${escapeHTML(t("workflow.templateNodeTypes"))}</b>
          <div class="workflow-template-chip-row">${nodeTypes.map(item => `<span>${escapeHTML(item)}</span>`).join("")}</div>
        </div>` : ""}
      ${resourceRefs.length ? `
        <div class="workflow-template-section">
          <b>${escapeHTML(t("workflow.templateUses"))}</b>
          <div class="workflow-template-chip-row workflow-template-resource-row">${resourceRefs.map(item => `<span title="${escapeHTML(item.title)}"><i>${escapeHTML(item.kind)}</i>${escapeHTML(item.value)}</span>`).join("")}</div>
        </div>` : ""}
      ${capabilities.length ? `<div class="workflow-template-capabilities">${capabilities.map(item => `<span class="${escapeHTML(item.tone)}">${escapeHTML(item.label)}</span>`).join("")}</div>` : ""}
      ${tags.length ? `<div class="workflow-template-tags">${tags.map(tag => `<span>${escapeHTML(tag)}</span>`).join("")}</div>` : ""}`;
    button.onclick = () => applyWorkflowTemplate(root, template.name);
    list.appendChild(button);
  }
}

function renderWorkflowTemplateCategoryOptions(select, templates) {
  if (!select) return;
  const current = state.workflowTemplateFilter.category || "";
  const categories = [...new Set((templates || []).map(template => template.category || template.source || "").filter(Boolean))].sort((left, right) => left.localeCompare(right));
  const options = [`<option value="">${escapeHTML(t("workflow.templateAllCategories"))}</option>`]
    .concat(categories.map(value => `<option value="${escapeHTML(value)}">${escapeHTML(workflowTemplateCategoryLabel(value))}</option>`));
  const nextHTML = options.join("");
  if (select.innerHTML !== nextHTML) select.innerHTML = nextHTML;
  select.value = categories.includes(current) ? current : "";
  if (select.value !== current) state.workflowTemplateFilter.category = "";
}

function filteredWorkflowTemplates(templates) {
  const query = String(state.workflowTemplateFilter.query || "").trim().toLowerCase();
  const category = String(state.workflowTemplateFilter.category || "").trim();
  return (templates || []).filter(template => {
    if (category && (template.category || template.source || "") !== category) return false;
    if (!query) return true;
    const haystack = [
      template.name,
      template.title,
      localizedText(template.title),
      template.description,
      localizedText(template.description),
      template.category,
      workflowTemplateCategoryLabel(template.category),
      template.source,
      workflowTemplateCategoryLabel(template.source),
      ...workflowTemplateValues(template, "node_types"),
      ...workflowTemplateValues(template, "agents"),
      ...workflowTemplateValues(template, "skills"),
      ...workflowTemplateValues(template, "tools"),
      ...workflowTemplateValues(template, "team_templates"),
      ...workflowTemplateValues(template, "policy_rules"),
      ...(Array.isArray(template.tags) ? template.tags : [])
    ].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(query);
  });
}

function workflowTemplateName(template) {
  return String(template?.name || template?.id || template?.title || "").trim();
}

function workflowTemplateRank(template) {
  const name = workflowTemplateName(template);
  const priority = {
    "multi-domain-intake-router": 0,
    "agent-framework-extension": 1,
    "task-decomposition-plan": 2,
    "plan-fix-audit": 3,
    "operations-runbook": 4,
    "customer-support-triage": 5,
    "software-team-review-gate": 6
  };
  if (Object.prototype.hasOwnProperty.call(priority, name)) return priority[name];
  const category = String(template?.category || template?.source || "").toLowerCase();
  const tags = Array.isArray(template?.tags) ? template.tags.map(tag => String(tag || "").toLowerCase()) : [];
  if (category === "starter" || tags.includes("starter")) return 20;
  if (tags.includes("team") || tags.includes("quality_gate")) return 30;
  return 80;
}

function workflowTemplateBadge(template) {
  const name = workflowTemplateName(template);
  if (name === "multi-domain-intake-router") return { tone: "primary", label: t("workflow.templateDefaultStarter") };
  if (name === "agent-framework-extension") return { tone: "builder", label: t("workflow.templateBuilderStarter") };
  if (name === "operations-runbook" || name === "customer-support-triage") return { tone: "domain", label: t("workflow.templateDomainStarter") };
  const category = String(template?.category || "").toLowerCase();
  const tags = Array.isArray(template?.tags) ? template.tags.map(tag => String(tag || "").toLowerCase()) : [];
  if (category === "starter" || tags.includes("starter")) return { tone: "neutral", label: t("workflow.templateStarterBadge") };
  return null;
}

function workflowTemplateHint(template) {
  const name = workflowTemplateName(template);
  if (name === "multi-domain-intake-router") return t("workflow.templateDefaultStarterHint");
  if (name === "agent-framework-extension") return t("workflow.templateBuilderStarterHint");
  if (name === "operations-runbook") return t("workflow.templateOperationsStarterHint");
  if (name === "customer-support-triage") return t("workflow.templateSupportStarterHint");
  return "";
}

function workflowTemplateNodeTypeChips(template) {
  const values = workflowTemplateValues(template, "node_types").slice(0, 6);
  return values.map(type => nodeTypeLabel(nodeTypeOption(type))).filter(Boolean);
}

function workflowTemplateResourceRefs(template) {
  const groups = [
    { key: "agents", kind: t("workflow.templateResourceAgent"), title: t("workflow.templateResourceAgentTitle") },
    { key: "skills", kind: t("workflow.templateResourceSkill"), title: t("workflow.templateResourceSkillTitle") },
    { key: "tools", kind: t("workflow.templateResourceTool"), title: t("workflow.templateResourceToolTitle") },
    { key: "team_templates", kind: t("workflow.templateResourceTeam"), title: t("workflow.templateResourceTeamTitle") },
    { key: "policy_rules", kind: t("workflow.templateResourcePolicy"), title: t("workflow.templateResourcePolicyTitle") }
  ];
  const refs = [];
  for (const group of groups) {
    for (const value of workflowTemplateValues(template, group.key)) {
      refs.push({ kind: group.kind, value: localizedText(value), title: group.title });
      if (refs.length >= 6) return refs;
    }
  }
  return refs;
}

function workflowTemplateCapabilityChips(template) {
  const chips = [];
  const nodeTypes = workflowTemplateValues(template, "node_types");
  const hasControl = Boolean(template?.has_control_flow) || nodeTypes.some(type => controlTypes.has(type));
  const hasQuality = Boolean(template?.has_quality_gate) || nodeTypes.some(type => type === "quality_gate" || type === "quality_guard");
  if (hasControl) chips.push({ tone: "control", label: t("workflow.templateCapabilityControl") });
  if (template?.has_data_flow) chips.push({ tone: "data", label: t("workflow.templateCapabilityData") });
  if (template?.has_approval) chips.push({ tone: "approval", label: t("workflow.templateCapabilityApproval") });
  if (hasQuality) chips.push({ tone: "quality", label: t("workflow.templateCapabilityQuality") });
  return chips;
}

function workflowTemplateValues(template, key) {
  const values = Array.isArray(template?.[key]) ? template[key] : [];
  return workflowStudioEndpointCandidates(...values);
}

function workflowTemplateCategoryLabel(value) {
  return localizedText(value || "");
}

function renderCanvas(root) {
  updateWorkflowToolbarStatus(root);
  const canvas = root.querySelector("#canvas");
  const surface = root.querySelector("#canvasSurface");
  canvas.classList.toggle("connecting", !!state.connecting);
  canvas.classList.toggle("panning", !!state.panning);
  state.graph.stages.forEach((stage, index) => ensurePosition(stage, index));
  keepGraphInsideCanvas();
  surface.querySelectorAll(".flow-node").forEach(node => node.remove());
  const tourNodeIndex = Math.max(0, state.graph.stages.findIndex(stage => executableTypes.has(normalizedNodeType(stage))));
  for (const [index, stage] of state.graph.stages.entries()) {
    const nodeType = normalizedNodeType(stage);
    const canReceive = nodeType !== "start";
    const canSend = nodeType !== "end";
    const category = nodeTypeCategory(nodeType);
    const categoryLabel = nodeCategoryLabel(category);
    const runtime = state.runtime.stageDetails[stage.name] || null;
    const runtimeState = nodeRuntimeState(runtime, stage.name);
    const routeText = nodeRuntimeRouteText(stage, runtime);
    const evidenceChips = nodeRuntimeEvidenceChips(runtime);
    const setupStatus = nodeSetupStatus(stage, nodeType);
    const node = document.createElement("div");
    node.dataset.stageName = stage.name;
    node.dataset.tourId = index === tourNodeIndex ? "workflow-node-agent" : "";
    node.tabIndex = 0;
    node.setAttribute("role", "button");
    node.setAttribute("aria-keyshortcuts", "Escape Delete Backspace");
    node.setAttribute("aria-label", `${nodeDisplayType(nodeType)} ${stage.name || t("workflow.noStage")} ${setupStatus.label}`);
    node.className = `flow-node ${nodeType} ${category}` +
      (index === state.selected ? " selected" : "") +
      (stage.approval ? " needs-approval" : "") +
      ` setup-${setupStatus.tone}` +
      (runtimeState ? ` has-runtime runtime-${runtimeState}` : "") +
      (state.runtime.currentStage === stage.name ? " runtime-current" : "") +
      (routeText ? " runtime-route-hit" : "") +
      (state.connectSource === stage.name || state.connecting?.sourceName === stage.name ? " connecting" : "") +
      (state.connecting && state.connecting.sourceName !== stage.name && canReceive ? " connect-target" : "");
    node.style.left = `${stage.position.x}px`;
    node.style.top = `${stage.position.y}px`;
    node.innerHTML = `
      ${canReceive ? `<span class="node-port port-in" title="${escapeHTML(t("workflow.dropConnection"))}"></span>` : ""}
      ${canSend ? `<span class="node-port port-out" data-tour-id="${index === tourNodeIndex ? "workflow-connector" : ""}" title="${escapeHTML(t("workflow.dragToConnect"))}"></span>` : ""}
      <button type="button" class="node-delete" title="${escapeHTML(t("workflow.remove"))}" aria-label="${escapeHTML(t("workflow.remove"))}" aria-keyshortcuts="Enter Space" data-action-hint="${escapeHTML(t("workflow.remove"))}">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4c0-1.1.9-2 2-2h4c1.1 0 2 .9 2 2v2"/><path d="M19 6l-1 14c-.1 1.1-1 2-2.1 2H8.1c-1.1 0-2-.9-2.1-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>
      </button>
      <div class="node-head">
        <span class="node-icon">${nodeIcon(nodeType)}</span>
        <div class="node-heading">
          <div class="node-kicker">
            <span class="node-index">${index + 1}</span>
            <span class="node-type">${escapeHTML(nodeDisplayType(nodeType))}</span>
            <span class="node-category-pill ${escapeHTML(category)}">${escapeHTML(categoryLabel)}</span>
          </div>
          <div class="node-title">${escapeHTML(stage.name || t("workflow.noStage"))}</div>
        </div>
      </div>
      <div class="node-meta">
        ${nodeMetaLines(stage, nodeType).map(line => `<span>${escapeHTML(line)}</span>`).join("")}
      </div>
      ${nodeSetupBadge(setupStatus)}
      ${runtimeState ? nodeRuntimeBadge(runtimeState) : ""}
      ${evidenceChips}
      ${routeText ? `<div class="node-runtime-route">${escapeHTML(routeText)}</div>` : ""}
      ${stage.approval ? `<div class="node-flag">${t("workflow.approvalGate")}</div>` : ""}`;
    node.onmousedown = event => {
      if (event.target.closest("button") || event.target.closest(".node-port")) return;
      const point = graphPoint(root, event);
      state.selected = index;
      state.dragging = { index, dx: point.x - stage.position.x, dy: point.y - stage.position.y };
      renderStageForm(root);
      renderCanvas(root);
    };
    node.onclick = event => {
      event.stopPropagation();
      if (state.connectSource && state.connectSource !== stage.name) {
        addConnection(state.connectSource, stage.name);
        state.connectSource = "";
        renderExecutionOrder(root);
      }
      state.selected = index;
      renderStageForm(root);
      renderCanvas(root);
    };
    node.onkeydown = event => {
      if (event.key === "Escape") {
        if (cancelWorkflowCanvasInteraction(root)) event.preventDefault();
        return;
      }
      if (event.key === "Delete" || event.key === "Backspace") {
        event.preventDefault();
        event.stopPropagation();
        removeStageAt(index);
        renderAll(root);
        return;
      }
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      state.selected = index;
      renderStageForm(root);
      renderCanvas(root);
    };
    node.querySelector(".node-delete").onclick = event => {
      event.preventDefault();
      event.stopPropagation();
      removeStageAt(index);
      renderAll(root);
    };
    const outPort = node.querySelector(".port-out");
    if (outPort) {
      outPort.onmousedown = event => {
        event.preventDefault();
        event.stopPropagation();
        startConnectionDrag(root, event, stage.name, index);
      };
    }
    const inPort = node.querySelector(".port-in");
    if (inPort) {
      inPort.onmouseup = event => {
        event.preventDefault();
        event.stopPropagation();
        finishConnectionDrag(root, event, stage.name);
      };
    }
    surface.appendChild(node);
  }
  applyCanvasGeometry(root);
  cancelScheduledWorkflowEdgeRepaint();
  drawEdges(root);
}

function updateStageNodePosition(root, stage) {
  if (!stage?.name) return;
  const surface = root.querySelector("#canvasSurface");
  const node = Array.from(surface?.querySelectorAll(".flow-node") || [])
    .find(item => item.dataset.stageName === stage.name);
  if (!node) return;
  node.style.left = `${stage.position.x}px`;
  node.style.top = `${stage.position.y}px`;
}

function stageNeedsCanvasGeometry(stage) {
  if (!stage) return false;
  const type = normalizedNodeType(stage);
  const right = stage.position.x + workflowNodeWidth(type);
  const bottom = stage.position.y + workflowNodeHeight(type);
  return right > state.canvas.width - 260 || bottom > state.canvas.height - 220;
}

function cancelScheduledWorkflowEdgeRepaint() {
  if (!workflowEdgeRepaintFrame) return;
  window.cancelAnimationFrame(workflowEdgeRepaintFrame);
  workflowEdgeRepaintFrame = 0;
}

function drawEdges(root) {
  const surface = root.querySelector("#canvasSurface");
  const svg = root.querySelector("#edges");
  svg.querySelectorAll("path.edge").forEach(edge => edge.remove());
  surface.querySelectorAll(".edge-action").forEach(button => button.remove());
  surface.querySelectorAll(".edge-runtime-label").forEach(label => label.remove());
  const rects = nodeRects(root);
  const byName = new Map(state.graph.stages.map(stage => [stage.name, stage]));
  const edges = [];
  for (const stage of state.graph.stages) {
    for (const link of workflowOutgoingLinks(stage)) {
      const target = byName.get(link.target);
      if (!target) continue;
      const sourceRect = rects.get(stage.name);
      const targetRect = rects.get(target.name);
      if (!sourceRect || !targetRect) continue;
      edges.push({ stage, sourceName: stage.name, targetName: target.name, link, sourceRect, targetRect });
    }
  }
  const slots = workflowEdgeSlots(edges);
  for (const edge of edges) {
    const anchors = edgeAnchors(edge.sourceRect, edge.targetRect, slots.get(workflowEdgeKey(edge)));
    const runtimeRouteEdge = isRuntimeRouteEdge(edge.stage, edge.targetName);
    const midpoint = edgeMidpoint(anchors);
    const routeClass = workflowLinkHasBranch(edge.link) ? " route-link" : "";
    svg.appendChild(edgePath(edgeD(anchors), `edge edge-underlay${runtimeRouteEdge ? " route-hit" : ""}${routeClass}`));
    svg.appendChild(edgePath(edgeD(anchors), `edge${runtimeRouteEdge ? " route-hit" : ""}${routeClass}`));
    if (runtimeRouteEdge) surface.appendChild(edgeRuntimeLabel(edge.stage, edge.targetName, midpoint));
    surface.appendChild(edgeDeleteButton(root, edge.sourceName, edge.targetName, midpoint, edge.link));
  }
  if (state.connecting) {
    const sourceRect = rects.get(state.connecting.sourceName);
    if (sourceRect) {
      const from = { x: sourceRect.left + sourceRect.width, y: sourceRect.top + sourceRect.height / 2, direction: 1 };
      const to = { x: state.connecting.x, y: state.connecting.y, direction: -1 };
      svg.appendChild(edgePath(edgeD({ from, to }), "edge draft"));
    }
  }
}

function workflowEdgeSlots(edges = []) {
  const slots = new Map();
  const sourceGroups = new Map();
  const targetGroups = new Map();
  const add = (map, key, edge) => {
    if (!map.has(key)) map.set(key, []);
    map.get(key).push(edge);
  };
  for (const edge of edges) {
    const leftToRight = workflowEdgeLeftToRight(edge.sourceRect, edge.targetRect);
    add(sourceGroups, `${edge.sourceName}:${leftToRight ? "right" : "left"}`, edge);
    add(targetGroups, `${edge.targetName}:${leftToRight ? "left" : "right"}`, edge);
    slots.set(workflowEdgeKey(edge), { sourceIndex: 0, sourceCount: 1, targetIndex: 0, targetCount: 1 });
  }
  for (const group of sourceGroups.values()) {
    group
      .sort((left, right) => workflowEdgeTargetRank(left) - workflowEdgeTargetRank(right) || left.link.order - right.link.order || left.targetName.localeCompare(right.targetName))
      .forEach((edge, index) => Object.assign(slots.get(workflowEdgeKey(edge)), { sourceIndex: index, sourceCount: group.length }));
  }
  for (const group of targetGroups.values()) {
    group
      .sort((left, right) => workflowEdgeSourceRank(left) - workflowEdgeSourceRank(right) || left.link.order - right.link.order || left.sourceName.localeCompare(right.sourceName))
      .forEach((edge, index) => Object.assign(slots.get(workflowEdgeKey(edge)), { targetIndex: index, targetCount: group.length }));
  }
  return slots;
}

function workflowEdgeKey(edge) {
  return `${edge.sourceName}\u001f${edge.targetName}`;
}

function workflowEdgeTargetRank(edge) {
  return edge.targetRect.top + edge.targetRect.height / 2;
}

function workflowEdgeSourceRank(edge) {
  return edge.sourceRect.top + edge.sourceRect.height / 2;
}

function workflowLinkHasBranch(link = {}) {
  return (link.entries || []).some(entry => entry.kind === "routes" || entry.kind === "cases");
}

function edgeRuntimeLabel(stage, targetName, point) {
  const label = document.createElement("div");
  const runtime = state.runtime.stageDetails[stage.name];
  const route = runtimeRouteTarget(stage, runtime);
  label.className = "edge-runtime-label";
  label.textContent = route.route
    ? t("workflow.nodeRuntime.edgeMatchedRoute", { route: route.route })
    : t("workflow.nodeRuntime.edgeMatched");
  label.title = `${stage.name} -> ${targetName}`;
  label.style.left = `${point.x}px`;
  label.style.top = `${point.y}px`;
  return label;
}

function nodeRuntimeState(runtime, stageName) {
  if (!runtime && state.runtime.currentStage !== stageName) return "";
  if (state.runtime.currentStage === stageName && state.runtime.status === "waiting") return "waiting";
  if (state.runtime.currentStage === stageName && state.runtime.status === "running") return "running";
  const status = String(runtime?.status || "").toLowerCase();
  if (["failed", "error", "denied", "cancelled", "canceled"].includes(status)) return "error";
  if (["waiting", "paused", "approval_required", "approval", "pending", "awaiting_input"].includes(status)) return "waiting";
  if (["completed", "success", "succeeded", "done", "finished"].includes(status)) return "success";
  if (runtime) return state.runtime.status === "success" ? "success" : "running";
  return "";
}

function nodeRuntimeBadge(runtimeState) {
  return `<div class="node-runtime-badge ${escapeHTML(runtimeState)}"><span></span>${escapeHTML(t(`workflow.nodeRuntime.${runtimeState}`))}</div>`;
}

function nodeRuntimeEvidenceChips(runtime) {
  if (!runtime) return "";
  const chips = [];
  const outputs = countRuntimeOutputs([runtime]);
  const artifacts = runtime.artifacts === null || runtime.artifacts === undefined ? 0 : Number(runtime.artifacts || 0);
  if (outputs) chips.push(nodeRuntimeChipHTML(t("workflow.nodeRuntime.outputs", { count: outputs })));
  if (artifacts) chips.push(nodeRuntimeChipHTML(t("workflow.nodeRuntime.artifacts", { count: artifacts })));
  if (runtime.attempts !== null && runtime.attempts !== undefined && runtime.attempts !== "") {
    chips.push(nodeRuntimeChipHTML(t("workflow.nodeRuntime.attempts", { count: runtime.attempts })));
  }
  const acceptance = Array.isArray(runtime.acceptance) ? runtime.acceptance : [];
  if (acceptance.length) {
    chips.push(nodeRuntimeChipHTML(t("workflow.nodeRuntime.acceptance", { count: acceptance.length }), workflowAcceptanceToneFromItems(acceptance)));
  }
  if (!chips.length) return "";
  return `<div class="node-runtime-evidence">${chips.slice(0, 4).join("")}</div>`;
}

function nodeRuntimeChipHTML(text, tone = "") {
  return `<span${tone ? ` class="${escapeHTML(tone)}"` : ""}>${escapeHTML(text)}</span>`;
}

function nodeSetupStatus(stage, nodeType) {
  const items = stageGuidanceItems(stage, nodeType);
  const issues = items.filter(item => item.tone === "error" || item.tone === "warn");
  const tone = issues.some(item => item.tone === "error") ? "error" : issues.length ? "warn" : "ready";
  const label = tone === "ready"
    ? t("workflow.nodeSetup.ready")
    : tone === "error"
      ? t("workflow.nodeSetup.error", { count: issues.length })
      : t("workflow.nodeSetup.warn", { count: issues.length });
  return {
    tone,
    label,
    title: issues[0]?.text || t("workflow.stageGuide.readyItem")
  };
}

function nodeSetupBadge(status) {
  return `<div class="node-setup-badge ${escapeHTML(status.tone)}" title="${escapeHTML(status.title)}"><span></span>${escapeHTML(status.label)}</div>`;
}

function nodeCategoryLabel(category) {
  const key = `workflow.nodeCategory.${category}`;
  const label = t(key);
  return label === key ? category : label;
}

function nodeRuntimeRouteText(stage, runtime) {
  if (!runtime) return "";
  const route = runtimeRouteTarget(stage, runtime);
  if (route.target) return t("workflow.nodeRuntime.routeTarget", { target: route.target });
  if (route.route) return t("workflow.nodeRuntime.routeValue", { route: route.route });
  return "";
}

function isRuntimeRouteEdge(sourceStage, targetName) {
  const runtime = state.runtime.stageDetails[sourceStage.name];
  if (!runtime) return false;
  const route = runtimeRouteTarget(sourceStage, runtime);
  return Boolean(route.target && route.target === targetName);
}

function runtimeRouteTarget(stage, runtime) {
  if (!runtime) return { route: "", target: "" };
  const routeValue = runtime.route !== undefined && runtime.route !== null && runtime.route !== ""
    ? String(runtime.route)
    : runtime.value !== undefined && runtime.value !== null && runtime.value !== ""
      ? String(runtime.value)
      : runtime.passed === true
        ? "true"
        : runtime.passed === false
          ? "false"
          : "";
  const routes = { ...(stage.cases || {}), ...(stage.routes || {}) };
  const target = runtime.target || routes[routeValue] || routes.default || "";
  return { route: routeValue, target };
}

function renderStageForm(root) {
  const form = root.querySelector("#stageForm");
  const empty = root.querySelector("#stageEmpty");
  const selectionBadge = root.querySelector("#stageSelectionBadge");
  const stage = selectedStage();
  form.classList.toggle("hidden", !stage);
  empty.classList.toggle("hidden", !!stage);
  form.style.display = stage ? "grid" : "none";
  empty.style.display = stage ? "none" : "";
  if (!stage) {
    if (selectionBadge) {
      selectionBadge.textContent = t("workflow.noSelection");
      selectionBadge.className = "badge neutral";
    }
    renderSelectedStageRuntime(root);
    return;
  }
  const nodeType = normalizedNodeType(stage);
  const basicSection = root.querySelector("[data-workflow-form-section='basic']");
  basicSection?.classList.remove("hidden");
  const stageKey = `${state.selected}:${stage.name || ""}:${nodeType}`;
  if (form.dataset.stageKey !== stageKey) {
    form.dataset.stageKey = stageKey;
    root.querySelector(".workflow-inspector-panel")?.scrollTo({ top: 0, left: 0 });
  }
  if (selectionBadge) {
    selectionBadge.textContent = nodeDisplayType(nodeType);
    selectionBadge.className = `badge ${controlTypes.has(nodeType) ? "info" : executableTypes.has(nodeType) ? "good" : "neutral"}`.trim();
  }
  root.querySelector("#stageNodeType").value = normalizedNodeType(stage);
  root.querySelector("#stageName").value = stage.name || "";
  root.querySelector("#stageAgent").value = stage.agent || "";
  root.querySelector("#stageSkill").value = stage.skill || "";
  root.querySelector("#stageTool").value = stage.tool || "";
  root.querySelector("#stageNext").value = (stage.next || []).join(", ");
  root.querySelector("#stageNextStrategy").value = stage.next_strategy || "";
  root.querySelector("#stageCondition").value = stage.condition || "";
  const policyRuleSelect = root.querySelector("#stagePolicyRule");
  const policyRule = selectedPolicyRuleName(stage);
  ensureSelectOption(policyRuleSelect, policyRule, policyRule);
  policyRuleSelect.value = policyRule;
  root.querySelector("#stagePolicy").value = stage.policy || "";
  root.querySelector("#stageSwitchOn").value = stage.switch_on || "";
  const params = stage.params || {};
  root.querySelector("#stageRoutes").value = formatMap(stage.routes);
  root.querySelector("#stageCases").value = formatMap(stage.cases);
  root.querySelector("#stageInputFieldsJson").value = stage.params?.fields_json || "";
  renderInputFieldsBuilder(root, stage);
  const teamTemplateName = params.team || params.template || "";
  ensureSelectOption(root.querySelector("#stageParamTeam"), teamTemplateName, teamTemplateName);
  root.querySelector("#stageParamTeam").value = teamTemplateName;
  updateTeamQuorumPresetOptions(root, stage, nodeType);
  root.querySelector("#stageParamWorkflow").value = params.workflow || "";
  root.querySelector("#stageParamRequest").value = params.request || "";
  root.querySelector("#stageParamItems").value = params.items || params.items_ref || "";
  root.querySelector("#stageParamStage").value = params.stage || "";
  root.querySelector("#stageParamUntil").value = params.until || "";
  root.querySelector("#stageParamMaxIterations").value = params.max_iterations || "";
  root.querySelector("#stageParamWaitFor").value = params.wait_for || "";
  root.querySelector("#stageParamPrompt").value = params.prompt || "";
  root.querySelector("#stageInputMap").value = formatMap(stage.input);
  root.querySelector("#stageOutputsMap").value = formatMap(stage.outputs);
  root.querySelector("#stageParams").value = formatParams(genericStageParams(stage, nodeType));
  root.querySelector("#stageTeamExecute").checked = isTruthyParam(stage.params?.execute);
  root.querySelector("#stageApproval").checked = !!stage.approval;
  renderArtifactsEditor(root, stage);
  updateStageArtifactsGuide(root, stage, nodeType);
  renderAcceptanceEditor(root, stage);
  updateStageAcceptanceGuide(root, stage, nodeType);
  updateStageFieldVisibility(root, nodeType);
  updateAdvancedFieldVisibility(root, nodeType);
  updateControlHelp(root, nodeType);
  updateStageAdvancedGuide(root, stage, nodeType);
  updateNodeTypeMeta(root, nodeType);
  updateTeamQuorumPresetOptions(root, stage, nodeType);
  updateTeamTemplatePreview(root, stage, nodeType);
  updateStagePlainSummary(root, stage, nodeType);
  updateStageRoutePreview(root, stage, nodeType);
  updateStageGuidance(root, stage, nodeType);
  updateStageDataFlow(root, stage, nodeType);
  updateWorkflowFormPanels(root);
  updatePolicyRuleHelp(root);
  scheduleExpressionValidation(root);
  root.querySelector("#connectHint").textContent = state.connectSource
    ? `${t("workflow.connectingHint")} ${state.connectSource}. ${t("workflow.connectingInstruction")}`
    : t("workflow.dragHint");
  renderSelectedStageRuntime(root);
}

function updateAdvancedFieldVisibility(root, nodeType) {
  const container = root.querySelector("#stageAdvancedFields");
  if (!container) return;
  const visible = advancedFieldSet(nodeType);
  container.querySelectorAll("[data-field]").forEach(node => {
    node.classList.toggle("hidden", !visible.has(node.dataset.field));
  });
  const hasVisibleField = Boolean(container.querySelector("[data-field]:not(.hidden)"));
  container.classList.toggle("hidden", !hasVisibleField && !controlNodeHelp(nodeType));
}

function updateControlHelp(root, nodeType) {
  const help = root.querySelector("#stageControlHelp");
  if (!help) return;
  const detail = controlNodeHelp(nodeType);
  help.classList.toggle("hidden", !detail);
  if (!detail) {
    help.innerHTML = "";
    return;
  }
  help.innerHTML = `
    <span>${escapeHTML(detail.kicker)}</span>
    <strong>${escapeHTML(detail.title)}</strong>
    <small>${escapeHTML(detail.body)}</small>
    ${detail.example ? `<code>${escapeHTML(detail.example)}</code>` : ""}`;
}

function updateStageAdvancedGuide(root, stage, nodeType) {
  const guide = root.querySelector("#stageAdvancedGuide");
  if (!guide || !stage) return;
  const visible = new Set([
    ...stageFieldSet(nodeType),
    ...advancedFieldSet(nodeType)
  ]);
  const items = advancedGuideItems(nodeType, visible);
  guide.classList.toggle("hidden", !items.length);
  if (!items.length) {
    guide.innerHTML = "";
    return;
  }
  guide.innerHTML = `
    <div class="workflow-advanced-guide-head">
      <span>${escapeHTML(t("workflow.advancedGuideKicker"))}</span>
      <strong>${escapeHTML(t("workflow.advancedGuideTitle"))}</strong>
    </div>
    <p>${escapeHTML(advancedGuideBody(nodeType))}</p>
    <div class="workflow-advanced-guide-list">
      ${items.map(item => `
        <section>
          <strong>${escapeHTML(item.title)}</strong>
          <span>${escapeHTML(item.body)}</span>
          ${item.example ? `<div class="workflow-advanced-guide-example">
            <code>${escapeHTML(item.example)}</code>
            ${item.fillable ? `<button type="button" class="ghost-button" data-advanced-guide-example="${escapeHTML(item.field)}">${escapeHTML(t("workflow.advancedGuideUseExample"))}</button>` : ""}
          </div>` : ""}
        </section>`).join("")}
    </div>`;
}

function advancedGuideItems(nodeType, visible) {
  const items = [];
  const add = (field, titleKey, bodyKey, exampleKey) => {
    if (!visible.has(field)) return;
    items.push({
      field,
      title: t(titleKey),
      body: t(bodyKey),
      example: exampleKey ? t(exampleKey) : "",
      fillable: Boolean(exampleKey && advancedGuideExampleTarget(field))
    });
  };
  add("next_strategy", "workflow.advancedGuide.nextStrategyTitle", "workflow.advancedGuide.nextStrategyBody", "workflow.advancedGuide.nextStrategyExample");
  add("condition", "workflow.advancedGuide.conditionTitle", "workflow.advancedGuide.conditionBody", "workflow.advancedGuide.conditionExample");
  add("switch_on", "workflow.advancedGuide.switchTitle", "workflow.advancedGuide.switchBody", "workflow.advancedGuide.switchExample");
  add("routes", "workflow.advancedGuide.routesTitle", "workflow.advancedGuide.routesBody", "workflow.advancedGuide.routesExample");
  add("cases", "workflow.advancedGuide.casesTitle", "workflow.advancedGuide.casesBody", "workflow.advancedGuide.casesExample");
  add("policy_rule", "workflow.advancedGuide.policyTitle", "workflow.advancedGuide.policyBody", "workflow.advancedGuide.policyExample");
  add("input", "workflow.advancedGuide.inputTitle", "workflow.advancedGuide.inputBody", "workflow.advancedGuide.inputExample");
  add("outputs", "workflow.advancedGuide.outputsTitle", "workflow.advancedGuide.outputsBody", "workflow.advancedGuide.outputsExample");
  add("params", "workflow.advancedGuide.paramsTitle", "workflow.advancedGuide.paramsBody", "workflow.advancedGuide.paramsExample");
  add("input_fields_json", "workflow.advancedGuide.inputGateTitle", "workflow.advancedGuide.inputGateBody", "workflow.advancedGuide.inputGateExample");
  add("param_items", "workflow.advancedGuide.eachTitle", "workflow.advancedGuide.eachBody", "workflow.advancedGuide.eachExample");
  add("param_stage", "workflow.advancedGuide.bodyStageTitle", "workflow.advancedGuide.bodyStageBody", "workflow.advancedGuide.bodyStageExample");
  add("param_until", "workflow.advancedGuide.loopTitle", "workflow.advancedGuide.loopBody", "workflow.advancedGuide.loopExample");
  add("param_max_iterations", "workflow.advancedGuide.maxIterationsTitle", "workflow.advancedGuide.maxIterationsBody", "workflow.advancedGuide.maxIterationsExample");
  add("param_wait_for", "workflow.advancedGuide.joinTitle", "workflow.advancedGuide.joinBody", "workflow.advancedGuide.joinExample");
  add("param_workflow", "workflow.advancedGuide.subWorkflowTitle", "workflow.advancedGuide.subWorkflowBody", "workflow.advancedGuide.subWorkflowExample");
  add("param_prompt", "workflow.advancedGuide.checkpointTitle", "workflow.advancedGuide.checkpointBody", "workflow.advancedGuide.checkpointExample");
  if (!items.length && nodeType !== "start" && nodeType !== "end") {
    items.push({
      field: "",
      title: t("workflow.advancedGuide.defaultTitle"),
      body: t("workflow.advancedGuide.defaultBody"),
      example: "",
      fillable: false
    });
  }
  return items.slice(0, 5);
}

function advancedGuideExampleTarget(field) {
  return {
    next_strategy: "stageNextStrategy",
    condition: "stageCondition",
    switch_on: "stageSwitchOn",
    routes: "stageRoutes",
    cases: "stageCases",
    input: "stageInputMap",
    outputs: "stageOutputsMap",
    params: "stageParams",
    input_fields_json: "stageInputFieldsJson",
    param_workflow: "stageParamWorkflow",
    param_items: "stageParamItems",
    param_stage: "stageParamStage",
    param_until: "stageParamUntil",
    param_max_iterations: "stageParamMaxIterations",
    param_wait_for: "stageParamWaitFor",
    param_prompt: "stageParamPrompt"
  }[field] || "";
}

function advancedGuideExampleValue(field) {
  const keys = {
    next_strategy: "workflow.advancedGuide.nextStrategyExample",
    condition: "workflow.advancedGuide.conditionExample",
    switch_on: "workflow.advancedGuide.switchExample",
    routes: "workflow.advancedGuide.routesExample",
    cases: "workflow.advancedGuide.casesExample",
    input: "workflow.advancedGuide.inputExample",
    outputs: "workflow.advancedGuide.outputsExample",
    params: "workflow.advancedGuide.paramsExample",
    input_fields_json: "workflow.advancedGuide.inputGateExample",
    param_workflow: "workflow.advancedGuide.subWorkflowExample",
    param_items: "workflow.advancedGuide.eachExample",
    param_stage: "workflow.advancedGuide.bodyStageExample",
    param_until: "workflow.advancedGuide.loopExample",
    param_max_iterations: "workflow.advancedGuide.maxIterationsExample",
    param_wait_for: "workflow.advancedGuide.joinExample",
    param_prompt: "workflow.advancedGuide.checkpointExample"
  };
  return keys[field] ? t(keys[field]) : "";
}

function applyAdvancedGuideExample(root, field) {
  const targetId = advancedGuideExampleTarget(field);
  if (!targetId) return;
  const input = root.querySelector(`#${targetId}`);
  if (!input) return;
  const value = advancedGuideExampleValue(field);
  if (!value) return;
  input.value = value;
  if (field === "input_fields_json") {
    renderInputFieldsBuilder(root);
    updateInputFieldsPreview(root);
  }
  syncStageFromForm(root);
  if (typeof input.focus === "function") input.focus();
}

function advancedGuideBody(nodeType) {
  if (controlTypes.has(nodeType)) return t("workflow.advancedGuideBody.control");
  if (executableTypes.has(nodeType)) return t("workflow.advancedGuideBody.execute");
  return t("workflow.advancedGuideBody.default");
}

function renderInputFieldsBuilder(root, stage = selectedStage()) {
  const node = root.querySelector("#stageInputFieldsBuilder");
  const textarea = root.querySelector("#stageInputFieldsJson");
  if (!node || !textarea) return;
  const draft = parseInputFieldsDraft(textarea.value || stage?.params?.fields_json || "");
  node.innerHTML = `
    <div class="workflow-input-builder-head">
      <div>
        <strong>${escapeHTML(t("workflow.inputFieldsBuilderTitle"))}</strong>
        <span>${escapeHTML(t("workflow.inputFieldsBuilderHelp"))}</span>
      </div>
      <button type="button" data-input-field-action="add">${escapeHTML(t("workflow.inputFieldsAdd"))}</button>
    </div>
    ${draft.error ? `<div class="workflow-input-builder-error">${escapeHTML(t("workflow.inputFieldsInvalid", { message: draft.error }))}</div>` : ""}
    <div class="workflow-input-builder-list">
      ${draft.fields.length ? draft.fields.map(renderInputFieldDraft).join("") : `<div class="workflow-input-builder-empty">${escapeHTML(t("workflow.inputFieldsEmpty"))}</div>`}
    </div>
    ${draft.error ? "" : renderInputFieldsPreview(draft.fields)}`;
}

function parseInputFieldsDraft(text) {
  const raw = String(text || "").trim();
  if (!raw) return { fields: [], error: "" };
  try {
    const parsed = JSON.parse(raw);
    const fields = Array.isArray(parsed)
      ? parsed
      : Array.isArray(parsed?.fields)
        ? parsed.fields
        : [];
    return { fields: fields.map(normalizeInputFieldDraft).filter(Boolean), error: "" };
  } catch (error) {
    return { fields: [], error: error?.message || t("workflow.inputFieldsInvalidFallback") };
  }
}

function normalizeInputFieldDraft(field, index = 0) {
  if (!field) return null;
  if (typeof field === "string") {
    const name = field.trim();
    return name ? createInputFieldDraft(index, { name, label: name }) : null;
  }
  if (typeof field !== "object") return null;
  const name = String(field.name || field.key || field.id || "").trim();
  const label = String(field.label || field.title || name || "").trim();
  const editable = new Set(["name", "key", "id", "label", "title", "type", "kind", "format", "required", "advanced", "multiple", "group", "description", "help", "placeholder", "options", "enum", "values"]);
  const extra = Object.fromEntries(Object.entries(field).filter(([key]) => !editable.has(key)));
  return {
    name,
    label,
    type: normalizeInputFieldDraftType(field.type || field.kind || field.format, field.options || field.enum || field.values),
    required: Boolean(field.required),
    advanced: Boolean(field.advanced),
    multiple: Boolean(field.multiple),
    group: String(field.group || "").trim(),
    description: String(field.description || field.help || "").trim(),
    placeholder: String(field.placeholder || "").trim(),
    options: inputFieldOptionsText(field.options || field.enum || field.values || []),
    extra
  };
}

function normalizeInputFieldDraftType(type, options = []) {
  const normalized = String(type || "").trim().toLowerCase();
  if (Array.isArray(options) && options.length) return "select";
  if (["text", "textarea"].includes(normalized)) return "text";
  if (["number", "integer", "boolean", "select", "url", "path", "file", "date", "email", "password", "json", "array", "object", "hidden"].includes(normalized)) return normalized;
  return "string";
}

function inputFieldOptionsText(options) {
  if (!Array.isArray(options)) return "";
  return options.map(option => {
    if (option && typeof option === "object") {
      const value = option.value ?? option.id ?? option.name ?? option.label ?? "";
      return String(value || "").trim();
    }
    return String(option || "").trim();
  }).filter(Boolean).join(", ");
}

function createInputFieldDraft(index = 0, overrides = {}) {
  return {
    name: overrides.name || `field_${index + 1}`,
    label: overrides.label || t("workflow.inputFieldsNewLabel", { count: index + 1 }),
    type: overrides.type || "string",
    required: Boolean(overrides.required),
    advanced: Boolean(overrides.advanced),
    multiple: Boolean(overrides.multiple),
    group: overrides.group || "",
    description: overrides.description || "",
    placeholder: overrides.placeholder || "",
    options: overrides.options || ""
  };
}

function renderInputFieldDraft(field, index) {
  const type = field.type || "string";
  const extra = field.extra && Object.keys(field.extra).length ? JSON.stringify(field.extra) : "";
  return `<article class="workflow-input-builder-item" data-input-field-item="${index}" data-input-field-extra="${escapeHTML(extra)}">
    <div class="workflow-input-builder-item-head">
      <strong>${escapeHTML(field.label || field.name || t("workflow.inputFieldsNewLabel", { count: index + 1 }))}</strong>
      <button type="button" class="danger-text" data-input-field-action="remove" data-input-field-index="${index}">${escapeHTML(t("workflow.inputFieldsRemove"))}</button>
    </div>
    <div class="workflow-input-builder-grid">
      ${inputFieldDraftInput("name", t("workflow.inputFieldName"), field.name, "target")}
      ${inputFieldDraftInput("label", t("workflow.inputFieldLabel"), field.label, t("workflow.inputFieldLabelPlaceholder"))}
      <label><span>${escapeHTML(t("workflow.inputFieldType"))}</span><select data-input-field-prop="type">
        ${inputFieldDraftTypes().map(item => `<option value="${escapeHTML(item)}"${item === type ? " selected" : ""}>${escapeHTML(inputFieldTypeLabel(item))}</option>`).join("")}
      </select></label>
      ${inputFieldDraftInput("group", t("workflow.inputFieldGroup"), field.group, t("workflow.inputFieldGroupPlaceholder"))}
      ${inputFieldDraftInput("description", t("workflow.inputFieldDescription"), field.description, t("workflow.inputFieldDescriptionPlaceholder"), true)}
      ${inputFieldDraftInput("placeholder", t("workflow.inputFieldPlaceholder"), field.placeholder, t("workflow.inputFieldPlaceholderExample"), true)}
      ${inputFieldDraftInput("options", t("workflow.inputFieldOptions"), field.options, t("workflow.inputFieldOptionsPlaceholder"), true)}
      <label class="check"><input type="checkbox" data-input-field-prop="required"${field.required ? " checked" : ""}> ${escapeHTML(t("workflow.inputFieldRequired"))}</label>
      <label class="check"><input type="checkbox" data-input-field-prop="advanced"${field.advanced ? " checked" : ""}> ${escapeHTML(t("workflow.inputFieldAdvanced"))}</label>
      <label class="check"><input type="checkbox" data-input-field-prop="multiple"${field.multiple ? " checked" : ""}> ${escapeHTML(t("workflow.inputFieldMultiple"))}</label>
    </div>
  </article>`;
}

function renderInputFieldsPreview(fields) {
  const items = (Array.isArray(fields) ? fields : []).filter(Boolean);
  const visible = items.filter(field => String(field.type || "").toLowerCase() !== "hidden");
  const primary = visible.filter(field => !field.advanced);
  const advanced = visible.filter(field => field.advanced);
  const hidden = items.length - visible.length;
  return `<section class="workflow-input-preview" data-input-fields-preview>
    <div class="workflow-input-preview-head">
      <div>
        <strong>${escapeHTML(t("workflow.inputFieldsPreviewTitle"))}</strong>
        <span>${escapeHTML(t("workflow.inputFieldsPreviewHelp"))}</span>
      </div>
      <small>${escapeHTML(t("chat.workflowInputCount", { count: items.length }))}</small>
    </div>
    ${visible.length
      ? `${renderInputFieldPreviewGroups(primary)}
        ${advanced.length ? `<details class="workflow-input-preview-advanced"><summary>${escapeHTML(t("workflow.inputFieldsPreviewAdvanced", { count: advanced.length }))}</summary>${renderInputFieldPreviewGroups(advanced, true)}</details>` : ""}`
      : `<div class="workflow-input-preview-empty">${escapeHTML(t("workflow.inputFieldsPreviewEmpty"))}</div>`}
    ${hidden ? `<div class="workflow-input-preview-hidden">${escapeHTML(t("workflow.inputFieldsPreviewHidden", { count: hidden }))}</div>` : ""}
  </section>`;
}

function updateInputFieldsPreview(root) {
  const preview = root.querySelector("[data-input-fields-preview]");
  if (!preview) return;
  const next = renderInputFieldsPreview(readInputFieldsBuilder(root));
  const template = document.createElement("template");
  template.innerHTML = next.trim();
  const replacement = template.content.firstElementChild;
  if (replacement) preview.replaceWith(replacement);
}

function renderInputFieldPreviewGroups(fields, advanced = false) {
  const items = (Array.isArray(fields) ? fields : []).filter(Boolean);
  if (!items.length) return "";
  const groups = [];
  const lookup = new Map();
  items.forEach((field, index) => {
    const group = String(field.group || "").trim();
    const key = group || "__default";
    if (!lookup.has(key)) {
      const entry = { key, title: group, fields: [] };
      lookup.set(key, entry);
      groups.push(entry);
    }
    lookup.get(key).fields.push({ field, index });
  });
  return groups.map(group => `<div class="workflow-input-preview-group ${advanced ? "advanced" : ""}">
    ${group.title ? `<div class="workflow-input-preview-group-title">${escapeHTML(localizedText(group.title))}</div>` : ""}
    <div class="workflow-input-preview-grid">
      ${group.fields.map(({ field, index }) => renderInputFieldPreview(field, index)).join("")}
    </div>
  </div>`).join("");
}

function renderInputFieldPreview(field, index) {
  const type = String(field.type || "string").toLowerCase();
  const label = field.label || field.name || t("workflow.inputFieldsNewLabel", { count: index + 1 });
  const description = field.description || "";
  const meta = [
    inputFieldTypeLabel(type),
    field.required ? t("workflow.inputFieldRequired") : "",
    field.multiple ? t("workflow.inputFieldMultiple") : ""
  ].filter(Boolean);
  return `<article class="workflow-input-preview-field">
    <div class="workflow-input-preview-label">
      <strong>${escapeHTML(localizedText(label))}</strong>
      ${field.required ? `<em>${escapeHTML(t("workflow.nodeTypeRequired"))}</em>` : ""}
    </div>
    ${description ? `<p>${escapeHTML(localizedText(description))}</p>` : ""}
    ${renderInputFieldPreviewControl(field, type)}
    ${meta.length ? `<div class="workflow-input-preview-meta">${meta.map(item => `<span>${escapeHTML(item)}</span>`).join("")}</div>` : ""}
  </article>`;
}

function renderInputFieldPreviewControl(field, type) {
  const placeholder = inputFieldPreviewPlaceholder(field, type);
  if (type === "boolean") {
    return `<div class="workflow-input-preview-toggle"><span></span><b>${escapeHTML(placeholder)}</b></div>`;
  }
  if (["select", "multiple"].includes(type) || field.multiple) {
    const options = inputFieldPreviewOptions(field);
    return `<div class="workflow-input-preview-options">${options.length
      ? options.slice(0, 5).map(option => `<span>${escapeHTML(localizedText(option))}</span>`).join("")
      : `<span>${escapeHTML(placeholder)}</span>`}</div>`;
  }
  if (["json", "array", "object"].includes(type)) {
    return `<pre class="workflow-input-preview-code">${escapeHTML(placeholder)}</pre>`;
  }
  if (type === "text" || type === "textarea") {
    return `<div class="workflow-input-preview-box long">${escapeHTML(placeholder)}</div>`;
  }
  return `<div class="workflow-input-preview-box">${escapeHTML(placeholder)}</div>`;
}

function inputFieldPreviewPlaceholder(field, type) {
  if (field.placeholder) return localizedText(field.placeholder);
  if (type === "boolean") return t("workflow.inputFieldsPreviewBoolean");
  if (type === "json" || type === "object") return "{ }";
  if (type === "array") return "[ ]";
  if (type === "number" || type === "integer") return "123";
  if (type === "date") return "2026-05-04";
  if (type === "email") return "name@example.com";
  if (type === "url") return "https://example.com";
  if (type === "path" || type === "file") return "@path/to/file";
  if (type === "password") return "********";
  return t("workflow.inputFieldsPreviewPlaceholder");
}

function inputFieldPreviewOptions(field) {
  const options = field.options;
  if (Array.isArray(options)) {
    return options.map(option => {
      if (option && typeof option === "object") return String(option.label || option.value || option.name || option.id || "").trim();
      return String(option || "").trim();
    }).filter(Boolean);
  }
  return String(options || "").split(",").map(item => item.trim()).filter(Boolean);
}

function inputFieldDraftInput(prop, label, value, placeholder = "", wide = false) {
  return `<label class="${wide ? "wide" : ""}"><span>${escapeHTML(label)}</span><input data-input-field-prop="${escapeHTML(prop)}" value="${escapeHTML(value || "")}" placeholder="${escapeHTML(placeholder || "")}"></label>`;
}

function inputFieldDraftTypes() {
  return ["string", "text", "number", "integer", "boolean", "select", "url", "path", "file", "date", "email", "password", "json", "array", "object", "hidden"];
}

function inputFieldTypeLabel(type) {
  const key = `chat.workflowInputType.${type}`;
  const translated = t(key);
  return translated === key ? localizedText(type) : translated;
}

function readInputFieldsBuilder(root) {
  return [...root.querySelectorAll("#stageInputFieldsBuilder [data-input-field-item]")].map((item, index) => {
    const value = prop => {
      const input = item.querySelector(`[data-input-field-prop="${prop}"]`);
      if (!input) return "";
      if (input.type === "checkbox") return Boolean(input.checked);
      return String(input.value || "").trim();
    };
    const label = value("label");
    const name = workflowInputFieldName(value("name"), label, index);
    const field = {
      ...parseInputFieldExtra(item.dataset.inputFieldExtra || ""),
      name,
      label: label || name,
      type: value("type") || "string"
    };
    if (value("description")) field.description = value("description");
    if (value("placeholder")) field.placeholder = value("placeholder");
    if (value("required")) field.required = true;
    if (value("advanced")) field.advanced = true;
    if (value("multiple")) field.multiple = true;
    if (value("group")) field.group = value("group");
    const options = value("options").split(",").map(item => item.trim()).filter(Boolean);
    if (options.length) {
      field.options = options.map(option => ({ value: option, label: option }));
      if (field.type === "string") field.type = "select";
    }
    return field;
  });
}

function parseInputFieldExtra(value) {
  if (!value) return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function workflowInputFieldName(name, label, index) {
  const raw = String(name || slug(label || "") || `field_${index + 1}`).trim();
  return raw
    .replace(/\s+/g, "_")
    .replace(/[^\w.-]/g, "_")
    .replace(/^_+|_+$/g, "") || `field_${index + 1}`;
}

function writeInputFieldsJSON(root, fields) {
  const textarea = root.querySelector("#stageInputFieldsJson");
  if (!textarea) return;
  textarea.value = fields.length ? JSON.stringify({ fields }, null, 2) : "";
}

function syncInputFieldsBuilderToJSON(root) {
  writeInputFieldsJSON(root, readInputFieldsBuilder(root));
}

function updateNodeTypeMeta(root, nodeType) {
  const panel = root.querySelector("#stageNodeTypeMeta");
  if (!panel) return;
  const option = nodeTypeOption(nodeType);
  const tags = Array.isArray(option.tags) ? option.tags.filter(Boolean).slice(0, 5) : [];
  const hints = Array.isArray(option.hints) ? option.hints.filter(Boolean).slice(0, 3) : [];
  const warnings = Array.isArray(option.warnings) ? option.warnings.filter(Boolean).slice(0, 3) : [];
  const examples = Array.isArray(option.examples) ? option.examples.filter(Boolean).slice(0, 2) : [];
  const fields = Array.isArray(option.fields) ? option.fields.filter(field => String(field?.name || "").trim()) : [];
  const outputs = Array.isArray(option.outputs) ? option.outputs.filter(output => String(output?.name || "").trim()) : [];
  const hasDefaultStage = option.default_stage && typeof option.default_stage === "object" && Object.keys(option.default_stage).length;
  const hasApplicableExample = examples.some(example => example?.stage && typeof example.stage === "object" && !Array.isArray(example.stage));
  const sourcePath = nodeTypeMetadataSourcePath(option);
  const hasMeta = option.source || sourcePath || tags.length || hints.length || warnings.length || examples.length || fields.length || outputs.length || hasDefaultStage;
  panel.classList.toggle("hidden", !hasMeta);
  if (!hasMeta) {
    panel.innerHTML = "";
    return;
  }
  const source = isCustomWorkflowMetadata(option) ? t("workflow.nodeTypeCustom") : option.source ? t("workflow.nodeTypeBuiltIn") : "";
  panel.innerHTML = `
    <div class="workflow-node-type-meta-head">
      <strong>${escapeHTML(t("workflow.nodeTypeMetadata"))}</strong>
      ${source ? `<span>${escapeHTML(source)}</span>` : ""}
    </div>
    ${renderNodeTypeMetaSummary({ fields, outputs, examples, hints, warnings, hasDefaultStage })}
    ${hasDefaultStage || hasApplicableExample ? `<div class="workflow-node-type-actions">
      <small>${escapeHTML(t("workflow.nodeTypeApplyHelp"))}</small>
    </div>` : ""}
    ${sourcePath ? `<div class="workflow-node-type-source"><span>${escapeHTML(t("workflow.nodeTypeOverridePath"))}</span><code title="${escapeHTML(sourcePath)}">${escapeHTML(shortWorkflowResourcePath(sourcePath))}</code></div>` : ""}
    ${tags.length ? `<div class="workflow-node-type-tags">${tags.map(tag => `<em>${escapeHTML(workflowDisplayValue(tag))}</em>`).join("")}</div>` : ""}
    ${hints.length ? `<ul class="workflow-node-type-hints">${hints.map(item => `<li>${escapeHTML(workflowDisplayText(item))}</li>`).join("")}</ul>` : ""}
    ${warnings.length ? `<ul class="workflow-node-type-warnings">${warnings.map(item => `<li>${escapeHTML(workflowDisplayText(item))}</li>`).join("")}</ul>` : ""}
    ${fields.length ? renderNodeTypeFieldSummary(fields) : ""}
    ${outputs.length ? renderNodeTypeOutputSummary(outputs) : ""}
    ${hasDefaultStage ? renderNodeTypeDefaultStage(option.default_stage, nodeType) : ""}
    ${examples.length ? `<div class="workflow-node-type-examples">${examples.map((example, index) => renderNodeTypeExample(example, index, nodeType)).join("")}</div>` : ""}`;
}

function nodeTypeMetadataSourcePath(option = {}) {
  if (!option?.path || !isCustomWorkflowMetadata(option)) return "";
  return String(option.path || "").trim();
}

function shortWorkflowResourcePath(path) {
  const parts = String(path || "").split(/[\\/]+/).filter(Boolean);
  if (parts.length <= 2) return parts.join("/");
  return `${parts.at(-2)}/${parts.at(-1)}`;
}

function renderNodeTypeMetaSummary({ fields = [], outputs = [], examples = [], hints = [], warnings = [], hasDefaultStage = false } = {}) {
  const chips = [
    fields.length ? { tone: "info", text: t("workflow.nodeTypeFieldsCount", { count: fields.length }) } : null,
    outputs.length ? { tone: "good", text: t("workflow.nodeTypeOutputsCount", { count: outputs.length }) } : null,
    examples.length ? { tone: "neutral", text: t("workflow.nodeTypeExamplesCount", { count: examples.length }) } : null,
    hints.length ? { tone: "info", text: t("workflow.nodePaletteHints", { count: hints.length }) } : null,
    warnings.length ? { tone: "warn", text: t("workflow.nodePaletteWarnings", { count: warnings.length }) } : null,
    hasDefaultStage ? { tone: "good", text: t("workflow.nodeTypeDefaultReady") } : null
  ].filter(Boolean);
  if (!chips.length) return "";
  return `<div class="workflow-node-type-summary">
    ${chips.map(chip => `<span class="${escapeHTML(chip.tone)}">${escapeHTML(chip.text)}</span>`).join("")}
  </div>`;
}

function renderNodeTypeFieldSummary(fields) {
  const visible = fields.slice(0, 5);
  return `<section class="workflow-node-type-meta-section">
    <div class="workflow-node-type-meta-section-head">
      <strong>${escapeHTML(t("workflow.nodeTypeFields"))}</strong>
      <span>${escapeHTML(t("workflow.nodeTypeFieldsCount", { count: fields.length }))}</span>
    </div>
    <div class="workflow-node-type-field-list">
      ${visible.map(renderNodeTypeField).join("")}
    </div>
    ${fields.length > visible.length ? `<small>${escapeHTML(t("workflow.nodeTypeMoreFields", { count: fields.length - visible.length }))}</small>` : ""}
  </section>`;
}

function renderNodeTypeField(field) {
  const name = String(field?.name || "").trim();
  const label = localizedText(field?.label || name);
  const type = localizedText(field?.type || t("workflow.nodeTypeFieldAny"));
  const options = Array.isArray(field?.options) ? field.options.filter(Boolean).slice(0, 4) : [];
  const examples = Array.isArray(field?.examples) ? field.examples.filter(Boolean).slice(0, 3) : [];
  const hints = Array.isArray(field?.hints) ? field.hints.filter(Boolean).slice(0, 2) : [];
  const details = [
    field?.default ? [t("workflow.nodeTypeFieldDefault"), field.default] : null,
    field?.placeholder ? [t("workflow.nodeTypeFieldPlaceholder"), field.placeholder] : null
  ].filter(Boolean);
  return `<article class="workflow-node-type-field">
    <div>
      <strong>${escapeHTML(label)}</strong>
      <code>${escapeHTML(name)}</code>
    </div>
    <span>${escapeHTML(type)}${field?.required ? ` / ${escapeHTML(t("workflow.nodeTypeRequired"))}` : ""}</span>
    ${field?.description ? `<p>${escapeHTML(workflowDisplayText(field.description))}</p>` : ""}
    ${options.length ? `<div class="workflow-node-type-options">${options.map(item => `<em>${escapeHTML(workflowDisplayText(item))}</em>`).join("")}</div>` : ""}
    ${details.length ? `<div class="workflow-node-type-field-details">${details.map(([key, value]) => `<small><b>${escapeHTML(key)}</b><code>${escapeHTML(workflowDisplayText(value))}</code></small>`).join("")}</div>` : ""}
    ${examples.length ? `<div class="workflow-node-type-field-examples"><b>${escapeHTML(t("workflow.nodeTypeFieldExamples"))}</b>${examples.map(item => `<code>${escapeHTML(workflowDisplayText(item))}</code>`).join("")}</div>` : ""}
    ${hints.length ? `<ul class="workflow-node-type-field-hints">${hints.map(item => `<li>${escapeHTML(workflowDisplayText(item))}</li>`).join("")}</ul>` : ""}
  </article>`;
}

function renderNodeTypeOutputSummary(outputs) {
  const visible = outputs.slice(0, 5);
  return `<section class="workflow-node-type-meta-section">
    <div class="workflow-node-type-meta-section-head">
      <strong>${escapeHTML(t("workflow.nodeTypeOutputs"))}</strong>
      <span>${escapeHTML(t("workflow.nodeTypeOutputsCount", { count: outputs.length }))}</span>
    </div>
    <div class="workflow-node-type-field-list compact">
      ${visible.map(renderNodeTypeOutput).join("")}
    </div>
    ${outputs.length > visible.length ? `<small>${escapeHTML(t("workflow.nodeTypeMoreOutputs", { count: outputs.length - visible.length }))}</small>` : ""}
  </section>`;
}

function renderNodeTypeOutput(output) {
  const name = String(output?.name || "").trim();
  const label = localizedText(output?.label || output?.title || name);
  const type = localizedText(output?.type || output?.kind || t("workflow.nodeTypeOutputValue"));
  const description = output?.description || output?.summary || "";
  const ref = output?.ref || output?.reference || output?.path || "";
  return `<article class="workflow-node-type-field output">
    <div>
      <strong>${escapeHTML(label || name)}</strong>
      <code>${escapeHTML(name || t("workflow.nodeTypeOutputValue"))}</code>
    </div>
    <span>${escapeHTML(type)}</span>
    ${description ? `<p>${escapeHTML(workflowDisplayText(description))}</p>` : ""}
    ${ref ? `<div class="workflow-node-type-options"><em>${escapeHTML(t("workflow.nodeTypeOutputRef"))}: ${escapeHTML(workflowDisplayValue(ref))}</em></div>` : ""}
  </article>`;
}

function renderNodeTypeDefaultStage(stage, nodeType) {
  const summary = nodeTypeDefaultStageSummary(stage);
  return `<section class="workflow-node-type-default">
    <strong>${escapeHTML(t("workflow.nodeTypeDefaultStage"))}</strong>
    <span>${escapeHTML(t("workflow.nodeTypeDefaultStageHelp"))}</span>
    ${summary ? `<p>${escapeHTML(summary)}</p>` : ""}
    <div class="workflow-node-type-actions">
      <button type="button" class="ghost-button" data-node-type-action="apply-default" data-node-type="${escapeHTML(nodeType)}">${escapeHTML(t("workflow.nodeTypeApplyDefault"))}</button>
    </div>
  </section>`;
}

function nodeTypeDefaultStageSummary(stage) {
  const parts = [
    stage?.agent ? `${t("workflow.agent")}: ${workflowDisplayValue(stage.agent)}` : "",
    stage?.skill ? `${t("workflow.skill")}: ${workflowDisplayValue(stage.skill)}` : "",
    stage?.tool ? `${t("workflow.toolMetadata")}: ${workflowDisplayValue(stage.tool)}` : "",
    Array.isArray(stage?.next) && stage.next.length ? `${t("workflow.nextStages")}: ${workflowDisplayList(stage.next)}` : ""
  ].filter(Boolean);
  return parts.join(" / ");
}

function renderNodeTypeExample(example, index, nodeType) {
  if (typeof example === "string") return `<code>${escapeHTML(workflowDisplayText(example))}</code>`;
  const title = workflowDisplayText(example?.title || example?.name || t("workflow.nodeTypeExample"));
  const body = workflowDisplayText(example?.description || example?.body || example?.example || "");
  const yaml = example?.yaml || "";
  const stageSummary = nodeTypeExampleStageSummary(example?.stage);
  const stageCode = yaml || workflowExampleStageCode(example?.stage);
  const notes = Array.isArray(example?.notes) ? example.notes.filter(Boolean).slice(0, 3).map(localizedText) : [];
  const canApply = example?.stage && typeof example.stage === "object" && !Array.isArray(example.stage);
  return `<article>
    <strong>${escapeHTML(title)}</strong>
    ${body ? `<small>${escapeHTML(body)}</small>` : ""}
    ${stageSummary ? `<small><b>${escapeHTML(t("workflow.nodeTypeExampleStage"))}</b> ${escapeHTML(stageSummary)}</small>` : ""}
    ${canApply ? `<div class="workflow-node-type-actions">
      <button type="button" class="ghost-button" data-node-type-action="apply-example" data-node-type="${escapeHTML(nodeType)}" data-example-index="${index}">${escapeHTML(t("workflow.nodeTypeApplyExample"))}</button>
    </div>` : ""}
    ${stageCode ? `<pre class="workflow-node-type-example-code"><code>${escapeHTML(stageCode)}</code></pre>` : ""}
    ${notes.length ? `<ul>${notes.map(note => `<li>${escapeHTML(note)}</li>`).join("")}</ul>` : ""}
  </article>`;
}

function workflowExampleStageCode(stage) {
  const cleaned = cleanWorkflowExampleValue(stage);
  if (!cleaned || typeof cleaned !== "object" || Array.isArray(cleaned)) return "";
  try {
    return JSON.stringify(cleaned, null, 2);
  } catch {
    return "";
  }
}

function cleanWorkflowExampleValue(value) {
  if (Array.isArray(value)) {
    const items = value.map(cleanWorkflowExampleValue).filter(item => !isEmptyWorkflowExampleValue(item));
    return items.length ? items : null;
  }
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      const cleaned = cleanWorkflowExampleValue(item);
      if (!isEmptyWorkflowExampleValue(cleaned)) out[key] = cleaned;
    }
    return Object.keys(out).length ? out : null;
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    return trimmed ? trimmed : null;
  }
  if (value === null || value === undefined) return null;
  return value;
}

function isEmptyWorkflowExampleValue(value) {
  return value === null || value === undefined || value === "" ||
    (Array.isArray(value) && !value.length) ||
    (value && typeof value === "object" && !Array.isArray(value) && !Object.keys(value).length);
}

function nodeTypeExampleStageSummary(stage) {
  if (!stage || typeof stage !== "object" || Array.isArray(stage)) return "";
  const parts = [
    stage.name ? `${t("catalog.name")}: ${workflowDisplayValue(stage.name)}` : "",
    stage.node_type ? `${t("workflow.nodeType")}: ${nodeDisplayType(stage.node_type)}` : "",
    stage.agent ? `${t("workflow.agent")}: ${workflowDisplayValue(stage.agent)}` : "",
    stage.skill ? `${t("workflow.skill")}: ${workflowDisplayValue(stage.skill)}` : "",
    stage.tool ? `${t("workflow.toolMetadata")}: ${workflowDisplayValue(stage.tool)}` : "",
    Array.isArray(stage.next) && stage.next.length ? `${t("workflow.nextStages")}: ${workflowDisplayList(stage.next)}` : ""
  ].filter(Boolean);
  return parts.join(" / ");
}

function updateTeamTemplatePreview(root, stage = selectedStage(), nodeType = stage ? normalizedNodeType(stage) : "") {
  const panel = root.querySelector("#stageTeamTemplatePreview");
  if (!panel) return;
  const isTeam = nodeType === "team";
  panel.classList.toggle("hidden", !isTeam);
  if (!isTeam) {
    panel.innerHTML = "";
    return;
  }
  const select = root.querySelector("#stageParamTeam");
  const name = String(select?.value || stage?.params?.team || stage?.params?.template || "").trim();
  if (!name) {
    panel.innerHTML = `
      <div class="workflow-team-template-empty">
        <strong>${escapeHTML(t("workflow.teamTemplateEmptyTitle"))}</strong>
        <span>${escapeHTML(t("workflow.teamTemplateEmptyHelp"))}</span>
      </div>`;
    return;
  }
  const detail = state.teamTemplateDetails[name];
  if (!detail && !state.teamTemplateRequests[name]) loadTeamTemplateDetail(root, name);
  const template = detail && !detail.error ? { ...teamTemplateSummary(name), ...detail } : teamTemplateSummary(name) || { name };
  const source = template?.source === "custom" || template?.custom ? t("workflow.teamTemplateCustom") : t("workflow.teamTemplateBuiltIn");
  const roles = Array.isArray(template?.role_templates) ? template.role_templates : [];
  const handoffs = Array.isArray(template?.handoffs) ? template.handoffs : [];
  const outputs = Array.isArray(template?.output_contract) ? template.output_contract : [];
  const presets = teamQuorumPresets(template);
  const quorumCount = presets.length || Number(template?.quorum_presets || 0);
  const selectedPreset = selectedTeamQuorumPreset(stage);
  const preset = presets.find(item => item.name === selectedPreset);
  const execute = root.querySelector("#stageTeamExecute")?.checked || isTruthyParam(stage?.params?.execute);
  const roleCount = roles.length || Number(template?.roles || 0);
  const tags = Array.isArray(template?.tags) ? template.tags.slice(0, 4).map(localizedText) : [];
  panel.innerHTML = `
    <div class="workflow-team-template-head">
      <div>
        <span>${escapeHTML(source)}</span>
        <strong>${escapeHTML(localizedText(template?.title || template?.name || name))}</strong>
      </div>
      <em>${escapeHTML(roleCount ? t("workflow.teamTemplateRolesCount", { count: roleCount }) : t("workflow.teamTemplateNoRoles"))}</em>
    </div>
    <p>${escapeHTML(localizedText(template?.description || t("workflow.teamTemplateNoDescription")))}</p>
    <div class="workflow-team-template-facts">
      ${teamTemplateFact(t("workflow.teamTemplateRecommendedWorkflow"), template?.recommended_workflow || "-")}
      ${teamTemplateFact(t("workflow.teamTemplateEntryAgent"), template?.recommended_entry_agent || "-")}
      ${teamTemplateFact(t("workflow.teamTemplateQuorumCount"), preset ? teamQuorumPresetLabel(preset) : String(quorumCount || 0))}
      ${tags.length ? teamTemplateFact(t("workflow.teamTemplateTags"), tags.join(", ")) : ""}
    </div>
    ${preset ? renderTeamQuorumPresetPreview(preset) : ""}
    <div class="workflow-team-template-mode ${execute ? "execute" : "context"}">
      <strong>${escapeHTML(execute ? t("workflow.teamTemplateExecuteOnTitle") : t("workflow.teamTemplateContextOnlyTitle"))}</strong>
      <span>${escapeHTML(execute ? t("workflow.teamTemplateExecuteOnHelp", { stage: stage?.name || name }) : t("workflow.teamTemplateContextOnlyHelp"))}</span>
    </div>
    <div class="workflow-team-template-grid">
      ${teamTemplatePreviewBlock(t("workflow.teamTemplateRolesTitle"), roles, renderTeamRolePreview, roleCount ? t("workflow.teamTemplateRolesSummary", { count: roleCount }) : t("workflow.teamTemplateRolesUnavailable"))}
      ${teamTemplatePreviewBlock(t("workflow.teamTemplateHandoffsTitle"), handoffs, renderTeamHandoffPreview, t("workflow.teamTemplateHandoffsEmpty"))}
      ${renderTeamOutputPreview(outputs)}
    </div>
    ${state.teamTemplateRequests[name] ? `<small class="workflow-team-template-loading">${escapeHTML(t("workflow.teamTemplateLoadingDetail"))}</small>` : ""}
    ${detail?.error ? `<small class="workflow-team-template-error">${escapeHTML(t("workflow.teamTemplateLoadFailed"))}: ${escapeHTML(detail.error)}</small>` : ""}`;
}

function updateTeamQuorumPresetOptions(root, stage = selectedStage(), nodeType = stage ? normalizedNodeType(stage) : "") {
  const select = root.querySelector("#stageTeamQuorumPreset");
  if (!select) return;
  const previous = selectedTeamQuorumPreset(stage) || select.value;
  select.innerHTML = "";
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = t("workflow.teamQuorumPresetNone");
  select.appendChild(empty);
  if (nodeType === "team") {
    const templateName = String(stage?.params?.team || stage?.params?.template || root.querySelector("#stageParamTeam")?.value || "").trim();
    const template = teamTemplateSummary(templateName);
    for (const preset of teamQuorumPresets(template)) {
      const option = document.createElement("option");
      option.value = preset.name;
      option.textContent = teamQuorumPresetLabel(preset);
      select.appendChild(option);
    }
  }
  if (previous) ensureSelectOption(select, previous, previous);
  select.value = previous;
}

function selectedTeamQuorumPreset(stage) {
  return String(stage?.params?.approval_preset || stage?.params?.review_preset || stage?.params?.quorum_preset || stage?.params?.preset || "").trim();
}

function teamQuorumPresets(template) {
  return Array.isArray(template?.quorum_presets)
    ? template.quorum_presets.filter(preset => preset?.name)
    : [];
}

function teamQuorumPresetLabel(preset) {
  const title = localizedText(preset?.title || preset?.name || "");
  const parts = [];
  if (preset?.required) parts.push(t("workflow.teamQuorumPresetRequired", { count: preset.required }));
  if (Array.isArray(preset?.roles) && preset.roles.length) parts.push(preset.roles.map(localizedText).join(", "));
  return parts.length ? `${title} / ${parts.join(" / ")}` : title;
}

function renderTeamQuorumPresetPreview(preset) {
  const roles = Array.isArray(preset?.roles) && preset.roles.length ? preset.roles.map(localizedText).join(", ") : t("workflow.teamQuorumPresetAnyRole");
  return `<div class="workflow-team-quorum-preset">
    <strong>${escapeHTML(localizedText(preset?.title || preset?.name || ""))}</strong>
    ${preset?.description ? `<span>${escapeHTML(localizedText(preset.description))}</span>` : ""}
    <small>${escapeHTML(t("workflow.teamQuorumPresetPreview", { count: preset?.required || 0, roles, blocks: preset?.reject_blocks ? t("common.on") : t("common.off") }))}</small>
  </div>`;
}

async function loadTeamTemplateDetail(root, name) {
  state.teamTemplateRequests[name] = true;
  try {
    state.teamTemplateDetails[name] = await loadWorkflowStudioTeamTemplateDetail(name);
  } catch (error) {
    state.teamTemplateDetails[name] = { name, error: localizedWorkflowErrorMessage(error, t("workflow.teamTemplateLoadFailed")) };
  } finally {
    delete state.teamTemplateRequests[name];
    const stage = selectedStage();
    if (stage && normalizedNodeType(stage) === "team" && (stage.params?.team || stage.params?.template || "") === name) {
      updateTeamQuorumPresetOptions(root, stage, "team");
      updateTeamTemplatePreview(root, stage, "team");
    }
  }
}

function teamTemplateFact(label, value) {
  return `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(value)}</strong></span>`;
}

function teamTemplatePreviewBlock(title, items, renderer, emptyText) {
  const visible = (items || []).slice(0, 3);
  return `<section class="workflow-team-template-block">
    <strong>${escapeHTML(title)}</strong>
    <div>${visible.length ? visible.map(renderer).join("") : `<span class="muted">${escapeHTML(emptyText)}</span>`}</div>
  </section>`;
}

function renderTeamRolePreview(role) {
  const title = localizedText(role?.label || role?.name || t("workflow.teamTemplateRole"));
  const agent = [role?.agent, role?.skill].filter(Boolean).join(" / ") || "-";
  const body = localizedText(formatInlineList(role?.responsibilities) || formatInlineList(role?.produces) || role?.notes || "");
  return `<article>
    <strong>${escapeHTML(title)}</strong>
    <small>${escapeHTML(agent)}</small>
    ${body ? `<span>${escapeHTML(body)}</span>` : ""}
  </article>`;
}

function renderTeamHandoffPreview(handoff) {
  const route = [handoff?.from, handoff?.to].filter(Boolean).join(" -> ") || t("workflow.teamTemplateHandoff");
  const body = localizedText(handoff?.subject || handoff?.description || handoff?.kind || "");
  return `<article>
    <strong>${escapeHTML(route)}</strong>
    ${body ? `<span>${escapeHTML(body)}</span>` : ""}
  </article>`;
}

function renderTeamOutputPreview(outputs) {
  return `<section class="workflow-team-template-block workflow-team-template-output">
    <strong>${escapeHTML(t("workflow.teamTemplateOutputsTitle"))}</strong>
    <div>${outputs.length ? outputs.slice(0, 6).map(item => `<em>${escapeHTML(localizedText(item))}</em>`).join("") : `<span class="muted">${escapeHTML(t("workflow.teamTemplateOutputsEmpty"))}</span>`}</div>
  </section>`;
}

function formatInlineList(value) {
  if (Array.isArray(value)) return value.filter(Boolean).join(", ");
  return String(value || "").trim();
}

function scheduleExpressionValidation(root, options = {}) {
  if (expressionAssistTimer) {
    window.clearTimeout(expressionAssistTimer);
    expressionAssistTimer = 0;
  }
  const context = expressionAssistContext(root);
  if (!context.visible) {
    renderExpressionAssist(root, context);
    return;
  }
  const signature = expressionAssistSignature(context);
  if (!options.force && (state.expressionAssist.signature === signature || state.expressionAssist.pendingSignature === signature)) {
    renderExpressionAssist(root, context);
    return;
  }
  state.expressionAssist.status = context.value ? "loading" : "idle";
  state.expressionAssist.result = null;
  state.expressionAssist.error = "";
  state.expressionAssist.staleRun = false;
  state.expressionAssist.pendingSignature = signature;
  renderExpressionAssist(root, context);
  const delay = options.immediate ? 0 : options.delay ?? 360;
  expressionAssistTimer = window.setTimeout(() => {
    validateExpressionAssist(root, signature).catch(error => {
      if (state.expressionAssist.pendingSignature && state.expressionAssist.pendingSignature !== signature) return;
      if (!state.expressionAssist.pendingSignature && state.expressionAssist.signature !== signature) return;
      state.expressionAssist.status = "error";
      state.expressionAssist.error = localizedWorkflowErrorMessage(error, t("workflow.expressionAssistUnavailable"));
      state.expressionAssist.pendingSignature = "";
      renderExpressionAssist(root);
    });
  }, delay);
}

async function validateExpressionAssist(root, expectedSignature) {
  const context = expressionAssistContext(root);
  if (!context.visible || expressionAssistSignature(context) !== expectedSignature) return;
  const seq = ++expressionAssistRequestSeq;
  const payload = {
    workflow: workflowExpressionDocument(root),
    expression: context.value,
    mode: context.def.mode,
    stage: context.stageName,
    field: context.def.field
  };
  if (context.def.requestKey === "reference") payload.reference = context.value;
  if (state.runtime.runID) payload.run_id = state.runtime.runID;

  let staleRun = false;
  let result;
  try {
    result = await validateWorkflowExpression(payload);
  } catch (error) {
    if (error.status !== 404 || !payload.run_id) throw error;
    staleRun = true;
    delete payload.run_id;
    result = await validateWorkflowExpression(payload);
  }
  if (seq !== expressionAssistRequestSeq) return;
  state.expressionAssist.status = "ready";
  state.expressionAssist.result = result;
  state.expressionAssist.error = "";
  state.expressionAssist.staleRun = staleRun;
  state.expressionAssist.signature = expectedSignature;
  state.expressionAssist.pendingSignature = "";
  renderExpressionAssist(root);
}

function validateWorkflowExpression(payload) {
  return request("/api/workflow-graphs/validate-expression", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload || {})
  });
}

function expressionAssistContext(root) {
  const panel = root.querySelector("#stageExpressionAssist");
  const stage = selectedStage();
  if (!panel || !stage) return { visible: false };
  const advancedPanel = root.querySelector("#stageAdvancedPanel");
  const active = document.activeElement instanceof Element ? document.activeElement : null;
  const focusedField = active?.dataset?.expressionField || "";
  if (advancedPanel && !advancedPanel.open && !focusedField) return { visible: false };
  const visibleFields = visibleExpressionAssistFields(root);
  if (!visibleFields.length) return { visible: false };
  let field = focusedField && visibleFields.includes(focusedField) ? focusedField : state.expressionAssist.field;
  if (!visibleFields.includes(field)) {
    field = visibleFields.find(name => String(root.querySelector(`#${expressionAssistFields[name].inputID}`)?.value || "").trim()) || visibleFields[0];
  }
  state.expressionAssist.field = field;
  const def = expressionAssistFields[field];
  const input = root.querySelector(`#${def.inputID}`);
  return {
    visible: true,
    stage,
    stageName: stage.name || "",
    field,
    def,
    input,
    value: String(input?.value || "").trim()
  };
}

function visibleExpressionAssistFields(root) {
  return expressionAssistFieldOrder.filter(field => {
    const input = root.querySelector(`#${expressionAssistFields[field].inputID}`);
    const wrapper = input?.closest("[data-field]");
    return input && wrapper && !wrapper.classList.contains("hidden");
  });
}

function expressionAssistSignature(context) {
  return [
    state.graph.name || "",
    context.stageName || "",
    context.field || "",
    context.value || "",
    state.runtime.runID || "",
    state.runtime.lastEventSeq || 0
  ].join("::");
}

function workflowExpressionDocument(root) {
  const nameInput = root.querySelector("#graphName");
  const descriptionInput = root.querySelector("#graphDescription");
  return {
    ...state.graph,
    name: slug(nameInput?.value || state.graph.name || ""),
    description: String(descriptionInput?.value || state.graph.description || "").trim(),
    stages: state.graph.stages || []
  };
}

function renderExpressionAssist(root, context = expressionAssistContext(root)) {
  const panel = root.querySelector("#stageExpressionAssist");
  if (!panel) return;
  if (!context.visible) {
    panel.className = "workflow-expression-assist hidden";
    panel.innerHTML = "";
    return;
  }
  const assist = state.expressionAssist;
  const result = assist.result || {};
  const issues = context.value ? normalizeExpressionIssues(result.issues) : [];
  const hasErrors = issues.some(issue => String(issue.level || "").toLowerCase() === "error");
  const tone = assist.status === "loading"
    ? "loading"
    : assist.status === "error"
      ? "error"
      : context.value && result.valid
        ? "valid"
        : hasErrors
          ? "warn"
          : "idle";
  const suggestions = normalizeExpressionSuggestions(result.suggestions, context);
  const statusText = expressionAssistStatusText(tone, context);
  const runText = expressionAssistSourceText(assist);
  const valueType = result.value_type ? `<span>${escapeHTML(t("workflow.expressionAssistValueType", { type: localizedText(result.value_type) }))}</span>` : "";
  const issueMarkup = issues.length
    ? `<ul class="workflow-expression-issues">${issues.slice(0, 3).map(issue => `<li>${escapeHTML(workflowValidationMessage(issue) || issue.field || "")}</li>`).join("")}</ul>`
    : "";
  const suggestionMarkup = suggestions.length
    ? suggestions.map(suggestion => renderExpressionSuggestionChip(suggestion)).join("")
    : `<span class="workflow-expression-empty">${escapeHTML(t("workflow.expressionAssistNoSuggestions"))}</span>`;
  const functionMarkup = renderExpressionFunctionHints(context);

  panel.className = `workflow-expression-assist ${tone}`;
  panel.innerHTML = `
    <div class="workflow-expression-assist-head">
      <div>
        <span>${escapeHTML(t("workflow.expressionAssistField", { field: t(context.def.labelKey) }))}</span>
        <strong>${escapeHTML(t("workflow.expressionAssistTitle"))}</strong>
        <small>${escapeHTML(runText)}</small>
      </div>
      <em>${escapeHTML(statusText)}</em>
    </div>
    <p>${escapeHTML(t("workflow.expressionAssistHelp"))}</p>
    <div class="workflow-expression-meta">
      ${valueType}
      <span>${escapeHTML(t("workflow.expressionAssistClickHint"))}</span>
    </div>
    ${assist.status === "error" ? `<div class="workflow-expression-error">${escapeHTML(workflowValidationMessage({ message: assist.error }))}</div>` : issueMarkup}
    <div class="workflow-expression-suggestions" aria-label="${escapeHTML(t("workflow.expressionAssistSuggestions"))}">
      ${suggestionMarkup}
    </div>
    ${functionMarkup}`;
}

function expressionAssistStatusText(tone, context) {
  if (tone === "loading") return t("workflow.expressionAssistChecking");
  if (tone === "error") return t("workflow.expressionAssistUnavailable");
  if (!context.value) return t("workflow.expressionAssistIdle");
  if (tone === "valid") return t("workflow.expressionAssistValid");
  return t("workflow.expressionAssistNeedsEdit");
}

function expressionAssistSourceText(assist) {
  if (assist.staleRun) return t("workflow.expressionAssistRunStale");
  if (state.runtime.runID) return t("workflow.expressionAssistRunBound", { run: shortRunID(state.runtime.runID) });
  return t("workflow.expressionAssistStatic");
}

function normalizeExpressionIssues(value) {
  return Array.isArray(value) ? value.filter(issue => issue && typeof issue === "object") : [];
}

function normalizeExpressionSuggestions(value, context) {
  if (!Array.isArray(value)) return [];
  const fragment = expressionAssistFragment(context.input).toLowerCase();
  const seen = new Set();
  const suggestions = value
    .filter(item => item && typeof item === "object" && item.reference)
    .filter(item => {
      const ref = String(item.reference).toLowerCase();
      if (!fragment) return true;
      return ref.includes(fragment) || String(item.stage || "").toLowerCase().includes(fragment);
    })
    .sort((left, right) => expressionSuggestionRank(left, fragment) - expressionSuggestionRank(right, fragment));
  const normalized = [];
  for (const item of suggestions) {
    const reference = String(item.reference || "").trim();
    if (!reference || seen.has(reference)) continue;
    seen.add(reference);
    normalized.push(item);
    if (normalized.length >= 14) break;
  }
  return normalized;
}

function expressionSuggestionRank(item, fragment) {
  const ref = String(item.reference || "").toLowerCase();
  let rank = item.source === "run_output" ? 0 : 20;
  if (fragment && ref.startsWith(fragment)) rank -= 8;
  if (fragment && ref.includes(fragment)) rank -= 4;
  if (item.stage === selectedStage()?.name) rank += 4;
  return rank;
}

function expressionAssistFragment(input) {
  if (!input) return "";
  const value = String(input.value || "");
  const caret = Number.isFinite(input.selectionStart) ? input.selectionStart : value.length;
  const prefix = value.slice(0, caret);
  const match = prefix.match(/[A-Za-z0-9_.\[\]-]+$/);
  return match ? match[0] : "";
}

function renderExpressionSuggestionChip(suggestion) {
  const reference = String(suggestion.reference || "");
  const source = expressionSuggestionSourceLabel(suggestion.source);
  const type = suggestion.type ? `${source} / ${localizedText(suggestion.type)}` : source;
  const stage = suggestion.stage ? `<small>${escapeHTML(suggestion.stage)}</small>` : "";
  return `<button type="button" class="workflow-expression-chip" data-expression-suggestion="${escapeHTML(reference)}" title="${escapeHTML(localizedText(suggestion.description || reference))}">
    <strong>${escapeHTML(reference)}</strong>
    <span>${escapeHTML(type)}</span>
    ${stage}
  </button>`;
}

function renderExpressionFunctionHints(context) {
  const functions = expressionFunctionOptionsForContext(context);
  if (!functions.length) return "";
  return `<div class="workflow-expression-functions">
    <div class="workflow-expression-functions-head">
      <strong>${escapeHTML(t("workflow.expressionFunctionsTitle"))}</strong>
      <span>${escapeHTML(t("workflow.expressionFunctionsHelp"))}</span>
    </div>
    <div class="workflow-expression-function-list">
      ${functions.slice(0, 5).map(renderExpressionFunctionCard).join("")}
    </div>
  </div>`;
}

function expressionFunctionOptionsForContext(context) {
  const nodeType = normalizedNodeType(context.stage);
  const mode = context.def?.mode || "";
  return (state.options?.expression_functions || [])
    .filter(fn => fn?.name)
    .filter(fn => !Array.isArray(fn.node_types) || !fn.node_types.length || fn.node_types.includes(nodeType))
    .filter(fn => !Array.isArray(fn.modes) || !fn.modes.length || fn.modes.includes(mode))
    .sort((left, right) => expressionFunctionRank(left, nodeType, mode) - expressionFunctionRank(right, nodeType, mode));
}

function expressionFunctionRank(fn, nodeType, mode) {
  let rank = 20;
  if (Array.isArray(fn.node_types) && fn.node_types.includes(nodeType)) rank -= 6;
  if (Array.isArray(fn.modes) && fn.modes.includes(mode)) rank -= 4;
  if (isCustomWorkflowMetadata(fn)) rank -= 1;
  return rank;
}

function renderExpressionFunctionCard(fn) {
  const insert = fn.insert_text || expressionFunctionExampleText(fn.examples?.[0]) || `${fn.name}()`;
  const source = isCustomWorkflowMetadata(fn) ? t("workflow.expressionFunctionCustom") : t("workflow.expressionFunctionBuiltIn");
  const sourcePath = expressionFunctionSourcePath(fn);
  const signature = fn.signature || `${fn.name}(...)`;
  const returnType = fn.return_type ? localizedText(fn.return_type) : "";
  const allHints = Array.isArray(fn.hints) ? fn.hints : [];
  const allWarnings = Array.isArray(fn.warnings) ? fn.warnings : [];
  const allExamples = Array.isArray(fn.examples) ? fn.examples : [];
  const allArgs = Array.isArray(fn.args) ? fn.args : [];
  const hints = allHints.slice(0, 2);
  const warnings = allWarnings.slice(0, 1);
  const examples = allExamples.slice(0, 2);
  const args = allArgs.slice(0, 4);
  return `<article class="workflow-expression-function-card">
    <button type="button" data-expression-suggestion="${escapeHTML(insert)}">
      <strong>${escapeHTML(workflowDisplayValue(fn.label || fn.name))}</strong>
      <code>${escapeHTML(signature)}</code>
    </button>
    <span>${escapeHTML(source)}${returnType ? ` / ${escapeHTML(returnType)}` : ""}</span>
    ${renderExpressionFunctionSummary(fn, { args: allArgs, examples: allExamples, hints: allHints, warnings: allWarnings })}
    ${fn.description ? `<p>${escapeHTML(workflowDisplayText(fn.description))}</p>` : ""}
    ${sourcePath ? `<code class="workflow-expression-function-path" title="${escapeHTML(sourcePath)}">${escapeHTML(t("workflow.expressionFunctionOverridePath"))}: ${escapeHTML(shortWorkflowResourcePath(sourcePath))}</code>` : ""}
    ${args.length ? `<div class="workflow-expression-function-args">${args.map(renderExpressionFunctionArg).join("")}</div>` : ""}
    ${hints.length ? `<ul class="hint">${hints.map(item => `<li>${escapeHTML(workflowDisplayText(item))}</li>`).join("")}</ul>` : ""}
    ${warnings.length ? `<ul class="warn">${warnings.map(item => `<li>${escapeHTML(workflowDisplayText(item))}</li>`).join("")}</ul>` : ""}
    ${examples.length ? `<div class="workflow-expression-function-examples">${examples.map(item => {
      const text = expressionFunctionExampleText(item);
      return `<button type="button" data-expression-suggestion="${escapeHTML(text)}">${escapeHTML(text)}</button>`;
    }).join("")}</div>` : ""}
  </article>`;
}

function expressionFunctionSourcePath(fn = {}) {
  if (!fn?.path || !isCustomWorkflowMetadata(fn)) return "";
  return String(fn.path || "").trim();
}

function renderExpressionFunctionSummary(fn, { args = [], examples = [], hints = [], warnings = [] } = {}) {
  const modes = Array.isArray(fn.modes) ? fn.modes.filter(Boolean) : [];
  const nodeTypes = Array.isArray(fn.node_types) ? fn.node_types.filter(Boolean) : [];
  const chips = [
    args.length ? { tone: "info", text: t("workflow.expressionFunctionArgsCount", { count: args.length }) } : null,
    fn.return_type ? { tone: "good", text: t("workflow.expressionFunctionReturns", { type: localizedText(fn.return_type) }) } : null,
    examples.length ? { tone: "neutral", text: t("workflow.expressionFunctionExamplesCount", { count: examples.length }) } : null,
    hints.length ? { tone: "info", text: t("workflow.nodePaletteHints", { count: hints.length }) } : null,
    warnings.length ? { tone: "warn", text: t("workflow.nodePaletteWarnings", { count: warnings.length }) } : null,
    modes.length ? { tone: "neutral", text: t("workflow.expressionFunctionModesCount", { count: modes.length }) } : null,
    nodeTypes.length ? { tone: "neutral", text: t("workflow.expressionFunctionNodeTypesCount", { count: nodeTypes.length }) } : null
  ].filter(Boolean);
  if (!chips.length) return "";
  return `<div class="workflow-expression-function-summary">
    ${chips.map(chip => `<span class="${escapeHTML(chip.tone)}">${escapeHTML(chip.text)}</span>`).join("")}
  </div>`;
}

function renderExpressionFunctionArg(arg) {
  const name = localizedText(arg?.label || arg?.name || t("workflow.expressionFunctionArg"));
  const type = localizedText(arg?.type || t("workflow.nodeTypeFieldAny"));
  const required = arg?.required ? ` / ${t("workflow.nodeTypeRequired")}` : "";
  const accepts = Array.isArray(arg?.accepts) && arg.accepts.length ? ` / ${arg.accepts.slice(0, 3).map(localizedText).join(", ")}` : "";
  const title = [arg?.name, localizedText(arg?.description)].filter(Boolean).join(" - ");
  return `<em title="${escapeHTML(title)}"><strong>${escapeHTML(name)}</strong><span>${escapeHTML(type)}${escapeHTML(required)}${escapeHTML(accepts)}</span></em>`;
}

function expressionFunctionExampleText(example) {
  if (typeof example === "string") return example;
  return example?.expression || example?.insert_text || example?.example || example?.body || "";
}

function expressionSuggestionSourceLabel(source) {
  if (source === "run_output") return t("workflow.expressionAssistSourceRun");
  if (source === "graph_output") return t("workflow.expressionAssistSourceGraph");
  return t("workflow.expressionAssistSourceDefault");
}

function insertExpressionSuggestion(root, reference) {
  if (!reference) return;
  const context = expressionAssistContext(root);
  const input = context.input;
  if (!input) return;
  const value = String(input.value || "");
  const start = Number.isFinite(input.selectionStart) ? input.selectionStart : value.length;
  const end = Number.isFinite(input.selectionEnd) ? input.selectionEnd : start;
  const fragment = expressionAssistFragment(input);
  const replaceStart = fragment ? Math.max(0, start - fragment.length) : start;
  input.value = `${value.slice(0, replaceStart)}${reference}${value.slice(end)}`;
  const nextCaret = replaceStart + reference.length;
  input.focus();
  input.setSelectionRange(nextCaret, nextCaret);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  scheduleExpressionValidation(root, { delay: 120, force: true });
}

function shortRunID(value) {
  const text = String(value || "");
  return text.length > 14 ? `${text.slice(0, 6)}...${text.slice(-5)}` : text;
}

function updateStageGuidance(root, stage, nodeType) {
  const target = root.querySelector("#stageGuidance");
  if (!target) return;
  const items = stageGuidanceItems(stage, nodeType);
  const tone = items.some(item => item.tone === "error") ? "error" : items.some(item => item.tone === "warn") ? "warn" : "ready";
  target.className = `workflow-stage-guidance ${tone}`;
  target.innerHTML = `
    <div class="workflow-stage-guidance-head">
      <span>${escapeHTML(nodeDisplayType(nodeType))}</span>
      <strong>${escapeHTML(t("workflow.stageGuideTitle"))}</strong>
    </div>
    <p>${escapeHTML(t(`workflow.stageGuide.${tone}`))}</p>
    <ul>${items.map(item => `<li class="${escapeHTML(item.tone)}">${escapeHTML(item.text)}</li>`).join("")}</ul>`;
}

function updateStageDataFlow(root, stage, nodeType) {
  const target = root.querySelector("#stageDataFlow");
  if (!target || !stage) return;
  const inputs = workflowStageMapEntries(stage.input);
  const outputs = workflowStageMapEntries(stage.outputs);
  const refs = workflowStageReferenceEntries(stage);
  const available = workflowAvailableOutputRefs(stage).slice(0, 8);
  const hasData = inputs.length || outputs.length || refs.length || available.length;
  target.classList.toggle("hidden", !hasData);
  if (!hasData) {
    target.innerHTML = "";
    return;
  }
  target.innerHTML = `
    <div class="workflow-stage-data-flow-head">
      <div>
        <span>${escapeHTML(t("workflow.dataFlowKicker"))}</span>
        <strong>${escapeHTML(t("workflow.dataFlowTitle"))}</strong>
      </div>
      <em>${escapeHTML(nodeDisplayType(nodeType))}</em>
    </div>
    <div class="workflow-stage-data-flow-grid">
      ${renderWorkflowDataFlowBlock(t("workflow.dataFlowInputs"), inputs, t("workflow.dataFlowNoInputs"), "input")}
      ${renderWorkflowDataFlowBlock(t("workflow.dataFlowOutputs"), outputs, t("workflow.dataFlowNoOutputs"), "output")}
    </div>
    ${refs.length ? `<div class="workflow-stage-data-flow-refs"><strong>${escapeHTML(t("workflow.dataFlowReads"))}</strong>${refs.slice(0, 6).map(ref => `<code>${escapeHTML(ref)}</code>`).join("")}</div>` : ""}
    ${available.length ? `<div class="workflow-stage-data-flow-refs muted-list"><strong>${escapeHTML(t("workflow.dataFlowAvailable"))}</strong>${available.map(ref => `<button type="button" data-copy-stage-ref="${escapeHTML(ref)}">${escapeHTML(ref)}</button>`).join("")}</div>` : ""}`;
}

function workflowStageMapEntries(map) {
  return Object.entries(map || {})
    .map(([key, value]) => [String(key || "").trim(), String(value || "").trim()])
    .filter(([key, value]) => key || value);
}

function renderWorkflowDataFlowBlock(title, entries, emptyText, kind) {
  return `<section class="workflow-stage-data-flow-block ${escapeHTML(kind)}">
    <strong>${escapeHTML(title)}</strong>
    ${entries.length
      ? `<div>${entries.slice(0, 5).map(([key, value]) => `<span><b>${escapeHTML(key || "-")}</b><code>${escapeHTML(value || "-")}</code></span>`).join("")}</div>`
      : `<p>${escapeHTML(emptyText)}</p>`}
    ${entries.length > 5 ? `<small>${escapeHTML(t("workflow.dataFlowMore", { count: entries.length - 5 }))}</small>` : ""}
  </section>`;
}

function workflowStageReferenceEntries(stage) {
  const values = [
    ...Object.values(stage.input || {}),
    ...Object.values(stage.params || {}),
    stage.condition,
    stage.policy,
    stage.switch_on
  ];
  const refs = [];
  const seen = new Set();
  const pattern = /stages\.([A-Za-z0-9_.-]+)\.outputs\.([A-Za-z0-9_.-]+)/g;
  for (const value of values) {
    const text = String(value || "");
    let match;
    while ((match = pattern.exec(text)) !== null) {
      const ref = `stages.${match[1]}.outputs.${match[2]}`;
      if (!seen.has(ref)) {
        seen.add(ref);
        refs.push(ref);
      }
    }
  }
  return refs;
}

function workflowAvailableOutputRefs(stage) {
  const selectedName = String(stage?.name || "");
  const selectedIndex = state.graph.stages.findIndex(item => item === stage || item.name === selectedName);
  const stages = (state.graph.stages || []).filter((item, index) => item && item.name && item.name !== selectedName && (selectedIndex < 0 || index < selectedIndex));
  const refs = [];
  for (const item of stages) {
    const outputs = Object.keys(item.outputs || {}).filter(Boolean);
    for (const output of outputs) refs.push(`stages.${item.name}.outputs.${output}`);
  }
  return refs;
}

function addDataFlowReferenceToStageInput(root, ref) {
  const stage = selectedStage();
  const value = String(ref || "").trim();
  if (!stage || !value) return;
  stage.input = stage.input && typeof stage.input === "object" && !Array.isArray(stage.input) ? { ...stage.input } : {};
  if (Object.values(stage.input).includes(value)) return;
  const base = workflowInputKeyFromRef(value);
  let key = base;
  let index = 2;
  while (stage.input[key]) {
    key = `${base}_${index}`;
    index += 1;
  }
  stage.input[key] = value;
  clearWorkflowValidation(root);
  clearWorkflowTransfer(root);
  renderStageForm(root);
  renderCanvas(root);
}

function workflowInputKeyFromRef(ref) {
  const parts = String(ref || "").split(".");
  const output = parts[parts.length - 1] || "upstream";
  return output
    .trim()
    .replace(/[^A-Za-z0-9_]+/g, "_")
    .replace(/^_+|_+$/g, "") || "upstream";
}

function updateStagePlainSummary(root, stage, nodeType) {
  const target = root.querySelector("#stagePlainSummary");
  if (!target || !stage) return;
  const guidance = stageGuidanceItems(stage, nodeType);
  const nextItem = guidance.find(item => item.tone === "error" || item.tone === "warn") || guidance[0];
  const facts = [
    [t("workflow.stageSummaryRuns"), stageSummaryRunTarget(stage, nodeType)],
    [t("workflow.stageSummaryNext"), stageSummaryNext(stage, nodeType)],
    [t("workflow.stageSummaryApproval"), stage.approval ? t("common.on") : t("common.off")],
    [t("workflow.stageSummaryEvidence"), stageSummaryEvidence(stage)]
  ];
  target.innerHTML = `
    <div class="workflow-stage-summary-head">
      <div>
        <span>${escapeHTML(nodeDisplayType(nodeType))}</span>
        <strong>${escapeHTML(t("workflow.stageSummaryTitle"))}</strong>
      </div>
      <small>${escapeHTML(stage.name || t("workflow.stageName"))}</small>
    </div>
    <p>${escapeHTML(stageSummaryBody(stage, nodeType))}</p>
    <div class="workflow-stage-summary-facts">
      ${facts.map(([label, value]) => `<span><small>${escapeHTML(label)}</small><strong>${escapeHTML(value)}</strong></span>`).join("")}
    </div>
    <div class="workflow-stage-summary-next">
      <small>${escapeHTML(t("workflow.stageSummaryNextStep"))}</small>
      <strong>${escapeHTML(nextItem?.text || t("workflow.stageGuide.readyItem"))}</strong>
    </div>`;
}

function updateStageRoutePreview(root, stage, nodeType) {
  const target = root.querySelector("#stageRoutePreview");
  if (!target || !stage) return;
  const preview = stageRoutePreview(stage, nodeType);
  target.classList.toggle("hidden", !preview);
  if (!preview) {
    target.innerHTML = "";
    return;
  }
  target.innerHTML = `
    <div class="workflow-route-preview-head">
      <div>
        <span>${escapeHTML(nodeDisplayType(nodeType))}</span>
        <strong>${escapeHTML(t("workflow.routePreviewTitle"))}</strong>
      </div>
      <small>${escapeHTML(t("workflow.routePreviewCount", { count: preview.rows.length }))}</small>
    </div>
    <p>${escapeHTML(preview.body)}</p>
    <div class="workflow-route-preview-list">
      ${preview.rows.map(renderRoutePreviewRow).join("")}
    </div>`;
}

function stageRoutePreview(stage, nodeType) {
  if (!controlTypes.has(nodeType)) return null;
  const rows = [];
  const addRouteRows = (map, detailKey) => {
    for (const [label, target] of Object.entries(map || {})) {
      rows.push(routePreviewRow(label, target, t(detailKey), { validateTarget: true }));
    }
  };
  if (nodeType === "condition") {
    addRouteRows(stage.routes, "workflow.routePreview.conditionRoute");
    addFallbackNextRows(rows, stage, "workflow.routePreview.conditionFallback");
    return routePreviewResult("workflow.routePreview.conditionBody", { rule: stage.condition || t("workflow.condition") }, rows);
  }
  if (nodeType === "switch" || nodeType === "router") {
    addRouteRows(stage.cases, "workflow.routePreview.caseRoute");
    addFallbackNextRows(rows, stage, "workflow.routePreview.caseFallback");
    return routePreviewResult("workflow.routePreview.switchBody", { rule: stage.switch_on || t("workflow.switchOn") }, rows);
  }
  if (nodeType === "policy_guard") {
    addRouteRows(stage.routes, "workflow.routePreview.policyRoute");
    addFallbackNextRows(rows, stage, "workflow.routePreview.policyFallback");
    return routePreviewResult("workflow.routePreview.policyBody", { rule: selectedPolicyRuleName(stage) || stage.policy || t("workflow.policy") }, rows);
  }
  if (nodeType === "parallel") {
    addNextRows(rows, stage, "workflow.routePreview.parallelBranch");
    return routePreviewResult("workflow.routePreview.parallelBody", {}, rows);
  }
  if (nodeType === "join") {
    rows.push(routePreviewRow(t("workflow.routePreview.waitFor"), stage.params?.wait_for, t("workflow.routePreview.waitForDetail"), { validateTarget: false }));
    addNextRows(rows, stage, "workflow.routePreview.joinContinue");
    return routePreviewResult("workflow.routePreview.joinBody", {}, rows);
  }
  if (nodeType === "input_gate") {
    addFallbackNextRows(rows, stage, "workflow.routePreview.inputContinue");
    return routePreviewResult("workflow.routePreview.inputBody", {}, rows);
  }
  if (nodeType === "checkpoint") {
    addFallbackNextRows(rows, stage, "workflow.routePreview.checkpointContinue");
    return routePreviewResult("workflow.routePreview.checkpointBody", {}, rows);
  }
  if (nodeType === "for_each") {
    rows.push(routePreviewRow(t("workflow.routePreview.eachItems"), stage.params?.items || stage.params?.items_ref, t("workflow.routePreview.eachItemsDetail"), { validateTarget: false }));
    rows.push(routePreviewRow(t("workflow.routePreview.bodyStage"), stage.params?.stage, t("workflow.routePreview.bodyStageDetail"), { validateTarget: true }));
    addNextRows(rows, stage, "workflow.routePreview.eachContinue");
    return routePreviewResult("workflow.routePreview.eachBody", {}, rows);
  }
  if (nodeType === "loop") {
    rows.push(routePreviewRow(t("workflow.routePreview.bodyStage"), stage.params?.stage, t("workflow.routePreview.loopBodyStageDetail"), { validateTarget: true }));
    rows.push(routePreviewRow(t("workflow.routePreview.until"), stage.params?.until, t("workflow.routePreview.untilDetail"), { validateTarget: false }));
    if (stage.params?.max_iterations) rows.push(routePreviewRow(t("workflow.routePreview.maxIterations"), stage.params.max_iterations, t("workflow.routePreview.maxIterationsDetail"), { validateTarget: false }));
    addNextRows(rows, stage, "workflow.routePreview.loopContinue");
    return routePreviewResult("workflow.routePreview.loopBody", {}, rows);
  }
  if (nodeType === "sub_workflow") {
    rows.push(routePreviewRow(t("workflow.routePreview.subWorkflow"), stage.params?.workflow, t("workflow.routePreview.subWorkflowDetail"), { validateTarget: false }));
    addNextRows(rows, stage, "workflow.routePreview.subWorkflowContinue");
    return routePreviewResult("workflow.routePreview.subWorkflowBody", {}, rows);
  }
  addFallbackNextRows(rows, stage, "workflow.routePreview.defaultContinue");
  return routePreviewResult("workflow.routePreview.defaultBody", {}, rows);
}

function routePreviewResult(bodyKey, vars, rows) {
  const visibleRows = rows.length ? rows : [
    routePreviewRow(t("workflow.routePreview.noRoutes"), "", t("workflow.routePreview.noRoutesHelp"), { validateTarget: false })
  ];
  return { body: t(bodyKey, vars), rows: visibleRows };
}

function addFallbackNextRows(rows, stage, detailKey) {
  if (rows.length) return;
  addNextRows(rows, stage, detailKey);
}

function addNextRows(rows, stage, detailKey) {
  const next = Array.isArray(stage.next) ? stage.next.filter(Boolean) : [];
  next.forEach((target, index) => {
    rows.push(routePreviewRow(
      next.length > 1 ? t("workflow.routePreview.branch", { index: index + 1 }) : t("workflow.routePreview.next"),
      target,
      t(detailKey),
      { validateTarget: true }
    ));
  });
}

function routePreviewRow(label, target, detail, options = {}) {
  const value = routePreviewValue(target);
  const validateTarget = options.validateTarget !== false;
  const knownTargets = new Set((state.graph.stages || []).map(stage => stage.name).filter(Boolean));
  const unknown = Boolean(validateTarget && value && !knownTargets.has(value));
  const tone = !value || unknown ? "warn" : "ready";
  return {
    label: String(label || "").trim(),
    target: value,
    detail: unknown ? t("workflow.routePreview.unknownTarget") : detail,
    tone
  };
}

function routePreviewValue(value) {
  if (Array.isArray(value)) return value.map(routePreviewValue).filter(Boolean).join(", ");
  if (value && typeof value === "object") return JSON.stringify(value);
  return String(value || "").trim();
}

function renderRoutePreviewRow(row) {
  return `<article class="${escapeHTML(row.tone)}">
    <span>${escapeHTML(workflowDisplayValue(row.label || t("workflow.routePreview.route")))}</span>
    <strong>${escapeHTML(row.target ? workflowDisplayValue(row.target) : t("workflow.routePreview.noTarget"))}</strong>
    <small>${escapeHTML(row.detail || "")}</small>
  </article>`;
}

function stageSummaryBody(stage, nodeType) {
  if (nodeType === "start") return t("workflow.stageSummaryStart");
  if (nodeType === "end") return t("workflow.stageSummaryEnd");
  if (nodeType === "team") return t("workflow.stageSummaryTeam");
  if (nodeType === "tool") return t("workflow.stageSummaryTool");
  if (nodeType === "skill") return t("workflow.stageSummarySkill");
  if (nodeType === "agent" || nodeType === "custom") return t("workflow.stageSummaryAgent");
  if (controlTypes.has(nodeType)) return t("workflow.stageSummaryControl");
  return t("workflow.stageSummaryDefault");
}

function stageSummaryRunTarget(stage, nodeType) {
  if (nodeType === "team") return workflowDisplayValue(teamTemplateTitle(stage.params?.team || stage.params?.template || "") || t("workflow.teamTemplate"));
  if (nodeType === "tool") return workflowDisplayValue(stage.tool || t("workflow.toolMetadata"));
  if (nodeType === "skill") return workflowDisplayValue(stage.skill || t("workflow.skill"));
  if (nodeType === "agent" || nodeType === "custom") return workflowDisplayValue(stage.agent || stage.skill || t("workflow.agent"));
  if (nodeType === "input_gate") return t("workflow.node.input_gate");
  if (nodeType === "sub_workflow") return workflowDisplayValue(stage.params?.workflow || t("workflow.node.sub_workflow"));
  if (controlTypes.has(nodeType)) return nodeDisplayType(nodeType);
  return nodeDisplayType(nodeType);
}

function stageSummaryNext(stage, nodeType) {
  if (nodeType === "end") return t("workflow.stageSummaryNoNext");
  const next = workflowOutgoingTargetNames(stage);
  return next.length ? workflowDisplayList(next) : t("workflow.stageSummaryNoNextYet");
}

function stageSummaryEvidence(stage) {
  const artifacts = normalizeArtifacts(stage.artifacts).length;
  const outputs = Object.keys(stage.outputs || {}).length;
  const acceptance = normalizeAcceptanceCriteria(stage.acceptance_criteria).length;
  if (artifacts) return t("workflow.stageSummaryArtifactCount", { count: artifacts });
  if (outputs) return t("workflow.stageSummaryOutputCount", { count: outputs });
  if (acceptance) return t("workflow.stageSummaryAcceptanceCount", { count: acceptance });
  return t("workflow.stageSummaryNoEvidence");
}

function stageGuidanceItems(stage, nodeType) {
  const items = [];
  const add = (tone, key) => items.push({ tone, text: t(key) });
  if (!String(stage.name || "").trim()) add("error", "workflow.stageGuide.missingName");
  if (nodeType !== "end" && !workflowOutgoingTargetNames(stage).length) add("warn", "workflow.stageGuide.missingNext");
  if (["agent", "skill", "custom", "team"].includes(nodeType) && !stage.agent && nodeType !== "team") {
    add("warn", "workflow.stageGuide.missingAgent");
  }
  if (nodeType === "team" && !stage.params?.team) add("warn", "workflow.stageGuide.missingTeamTemplate");
  if (nodeType === "tool" && !stage.tool) add("error", "workflow.stageGuide.missingTool");
  if (nodeType === "condition") {
    if (!stage.condition) add("error", "workflow.stageGuide.missingCondition");
    if (!Object.keys(stage.routes || {}).length) add("warn", "workflow.stageGuide.missingRoutes");
  }
  if (nodeType === "switch" || nodeType === "router") {
    if (!stage.switch_on) add("error", "workflow.stageGuide.missingSwitch");
    if (!Object.keys(stage.cases || {}).length) add("warn", "workflow.stageGuide.missingCases");
  }
  if (nodeType === "policy_guard" && !selectedPolicyRuleName(stage) && !stage.policy) add("warn", "workflow.stageGuide.missingPolicy");
  if (nodeType === "input_gate") {
    const fieldsStatus = workflowInputFieldsSchemaStatus(stage.params?.fields_json);
    if (fieldsStatus === "missing") add("warn", "workflow.stageGuide.missingInputForm");
    if (fieldsStatus === "invalid") add("error", "workflow.stageGuide.invalidInputForm");
  }
  if (nodeType === "for_each") {
    if (!stage.params?.items && !stage.params?.items_ref) add("error", "workflow.stageGuide.missingItems");
    if (!stage.params?.stage) add("warn", "workflow.stageGuide.missingBodyStage");
  }
  if (nodeType === "loop") {
    if (!stage.params?.stage) add("warn", "workflow.stageGuide.missingBodyStage");
    if (!stage.params?.until) add("warn", "workflow.stageGuide.missingLoopCondition");
  }
  if (nodeType === "sub_workflow") {
    if (!stage.params?.workflow) add("error", "workflow.stageGuide.missingSubWorkflow");
    if (!stage.params?.request) add("warn", "workflow.stageGuide.missingSubWorkflowRequest");
  }
  if (nodeType === "join" && !stage.params?.wait_for) add("warn", "workflow.stageGuide.missingJoinWaitFor");
  if (nodeType === "checkpoint" && !stage.params?.prompt) add("warn", "workflow.stageGuide.missingCheckpointPrompt");
  if (!items.length) add("ready", "workflow.stageGuide.readyItem");
  return items;
}

function workflowInputFieldsSchemaStatus(value) {
  const raw = String(value || "").trim();
  if (!raw) return "missing";
  try {
    const parsed = JSON.parse(raw);
    const fields = Array.isArray(parsed) ? parsed : parsed.fields;
    return Array.isArray(fields) && fields.length ? "ready" : "invalid";
  } catch {
    return "invalid";
  }
}

function updateStageFieldVisibility(root, nodeType) {
  const visible = stageFieldSet(nodeType);
  root.querySelectorAll("[data-stage-field]").forEach(node => {
    node.classList.toggle("hidden", !visible.has(node.dataset.stageField));
  });
}

function updateWorkflowFormPanels(root) {
  const advanced = root.querySelector("#stageAdvancedPanel");
  if (advanced) {
    const visibleAdvancedNodes = Array.from(advanced.querySelectorAll("[data-stage-field]:not(.hidden), [data-field]:not(.hidden)"));
    const visibleAdvanced = visibleAdvancedNodes[0];
    advanced.classList.toggle("hidden", !visibleAdvanced);
    const badge = root.querySelector("#stageAdvancedCount");
    if (badge) {
      badge.textContent = visibleAdvancedNodes.length
        ? t("workflow.advancedVisibleCount", { count: visibleAdvancedNodes.length })
        : t("workflow.optional");
    }
    if (!visibleAdvanced) advanced.open = false;
  }
  const artifacts = root.querySelector("#stageArtifactsPanel");
  const artifactEditor = root.querySelector("#stageArtifactsEditor");
  const acceptanceEditor = root.querySelector("#stageAcceptanceEditor");
  if (artifacts && artifactEditor && acceptanceEditor) {
    const visibleArtifacts = !artifactEditor.classList.contains("hidden");
    const visibleAcceptance = !acceptanceEditor.classList.contains("hidden");
    artifacts.classList.toggle("hidden", !visibleArtifacts && !visibleAcceptance);
    const badge = root.querySelector("#stageArtifactsCount");
    if (badge) {
      const stage = selectedStage();
      const count = normalizeArtifacts(stage?.artifacts).length + normalizeAcceptanceCriteria(stage?.acceptance_criteria).length;
      badge.textContent = count ? t("workflow.resultConfigCount", { count }) : t("workflow.optional");
    }
    if (!visibleArtifacts && !visibleAcceptance) artifacts.open = false;
  }
}

function stageFieldSet(nodeType) {
  const visible = new Set(["node_type", "name"]);
  if (nodeType !== "end") visible.add("next");

  const fields = nodeFieldNames(nodeType);
  for (const field of fields) {
    const base = field.split(".")[0];
    if (["agent", "skill", "tool", "next", "next_strategy", "artifacts", "acceptance_criteria", "approval"].includes(base)) {
      visible.add(base);
    }
    if (base === "params") visible.add("params");
  }
  if (executableTypes.has(nodeType)) {
    visible.add("params");
    visible.add("artifacts");
    visible.add("acceptance_criteria");
    visible.add("approval");
  }

  if (!fields.size) {
    if (["agent", "skill", "custom"].includes(nodeType)) {
      visible.add("agent");
      visible.add("skill");
      visible.add("acceptance_criteria");
    }
    if (nodeType === "tool") {
      visible.add("agent");
      visible.add("skill");
      visible.add("tool");
      visible.add("acceptance_criteria");
    }
    if (nodeType === "team") {
      visible.add("team_template");
      visible.add("params");
      visible.add("acceptance_criteria");
    }
    if (controlTypes.has(nodeType)) visible.add("params");
  }

  if (["condition", "switch", "router", "policy_guard", "parallel"].includes(nodeType) || executableTypes.has(nodeType)) {
    visible.add("next_strategy");
  }
  if (["team", "policy_guard", "input_gate", "for_each", "loop", "sub_workflow", "join", "checkpoint"].includes(nodeType)) {
    visible.add("params");
  }
  if (nodeType === "team") {
    visible.add("team_template");
    visible.add("team_quorum_preset");
    visible.add("team_execute");
  }
  if (nodeType === "policy_guard") visible.add("params");
  if (nodeType === "start" || nodeType === "end") {
    visible.delete("agent");
    visible.delete("skill");
    visible.delete("tool");
    visible.delete("params");
    visible.delete("artifacts");
    visible.delete("acceptance_criteria");
    visible.delete("approval");
    visible.delete("next_strategy");
  }
  if (nodeType !== "team") visible.delete("team_execute");
  return visible;
}

function advancedFieldSet(nodeType) {
  const visible = new Set();
  const fields = nodeFieldNames(nodeType);
  for (const field of fields) {
    const base = field.split(".")[0];
    if (["condition", "policy", "switch_on", "routes", "cases", "input", "outputs"].includes(base)) {
      visible.add(base);
    }
  }
  if (nodeType === "policy_guard") {
    visible.add("policy_rule");
    visible.add("policy");
    visible.add("routes");
  }
  if (!fields.size) {
    if (executableTypes.has(nodeType)) {
      visible.add("input");
      visible.add("outputs");
    }
    if (nodeType === "condition") {
      visible.add("condition");
      visible.add("routes");
    }
    if (nodeType === "switch" || nodeType === "router") {
      visible.add("switch_on");
      visible.add("cases");
    }
    if (nodeType === "input_gate") {
      visible.add("input_fields_json");
    }
    if (nodeType === "for_each") {
      visible.add("param_items");
      visible.add("param_stage");
    }
    if (nodeType === "loop") {
      visible.add("param_stage");
      visible.add("param_until");
      visible.add("param_max_iterations");
    }
    if (nodeType === "sub_workflow") {
      visible.add("param_workflow");
      visible.add("param_request");
    }
    if (nodeType === "join") {
      visible.add("param_wait_for");
    }
    if (nodeType === "checkpoint") {
      visible.add("param_prompt");
    }
  }
  if (nodeType === "for_each") {
    visible.add("param_items");
    visible.add("param_stage");
  }
  if (nodeType === "loop") {
    visible.add("param_stage");
    visible.add("param_until");
    visible.add("param_max_iterations");
  }
  if (nodeType === "sub_workflow") {
    visible.add("param_workflow");
    visible.add("param_request");
  }
  if (nodeType === "join") visible.add("param_wait_for");
  if (nodeType === "checkpoint") visible.add("param_prompt");
  if (nodeType === "start" || nodeType === "end") return new Set();
  return visible;
}

function controlNodeHelp(nodeType) {
  const supported = new Set(["condition", "switch", "router", "policy_guard", "parallel", "join", "input_gate", "checkpoint", "for_each", "loop", "sub_workflow"]);
  if (!supported.has(nodeType)) return null;
  return {
    kicker: t("workflow.controlHelp.kicker"),
    title: t(`workflow.controlHelp.${nodeType}.title`),
    body: t(`workflow.controlHelp.${nodeType}.body`),
    example: t(`workflow.controlHelp.${nodeType}.example`)
  };
}

function nodeFieldNames(nodeType) {
  const option = nodeTypeOption(nodeType);
  if (!Array.isArray(option?.fields)) return new Set();
  return new Set(option.fields.map(field => String(field?.name || "").trim()).filter(Boolean));
}

function renderArtifactsEditor(root, stage) {
  const list = root.querySelector("#stageArtifactsList");
  if (!list) return;
  const artifacts = normalizeArtifacts(stage.artifacts);
  if (!artifacts.length) {
    list.innerHTML = `<div class="artifact-empty">${escapeHTML(t("workflow.artifactsEmpty"))}</div>`;
    return;
  }
  list.innerHTML = artifacts.map((artifact, index) => artifactForm(index, artifact)).join("");
}

function renderAcceptanceEditor(root, stage) {
  const list = root.querySelector("#stageAcceptanceList");
  if (!list) return;
  const criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria);
  if (!criteria.length) {
    list.innerHTML = `<div class="artifact-empty">${escapeHTML(t("workflow.acceptanceCriteriaEmpty"))}</div>`;
    return;
  }
  list.innerHTML = criteria.map((criterion, index) => acceptanceCriterionForm(index, criterion)).join("");
}

function updateStageArtifactsGuide(root, stage, nodeType) {
  const guide = root.querySelector("#stageArtifactsGuide");
  if (!guide || !stage) return;
  const supportsArtifacts = stageFieldSet(nodeType).has("artifacts");
  guide.classList.toggle("hidden", !supportsArtifacts);
  if (!supportsArtifacts) {
    guide.innerHTML = "";
    return;
  }
  const artifacts = normalizeArtifacts(stage.artifacts);
  const tone = artifacts.length ? "ready" : "neutral";
  guide.className = `workflow-artifact-guide ${tone}`;
  guide.innerHTML = `
    <div class="workflow-artifact-guide-head">
      <span>${escapeHTML(t("workflow.artifactGuideKicker"))}</span>
      <strong>${escapeHTML(t("workflow.artifactGuideTitle"))}</strong>
    </div>
    <p>${escapeHTML(t("workflow.artifactGuideBody"))}</p>
    <div class="workflow-artifact-guide-grid">
      <section>
        <strong>${escapeHTML(t("workflow.artifactGuideRefTitle"))}</strong>
        <span>${escapeHTML(t("workflow.artifactGuideRefBody"))}</span>
        <code>ref=result.output</code>
        <button type="button" class="ghost-button" data-artifact-guide-action="report">${escapeHTML(t("workflow.artifactGuideAddReport"))}</button>
      </section>
      <section>
        <strong>${escapeHTML(t("workflow.artifactGuideContentTitle"))}</strong>
        <span>${escapeHTML(t("workflow.artifactGuideContentBody"))}</span>
        <code>content=...</code>
        <button type="button" class="ghost-button" data-artifact-guide-action="evidence">${escapeHTML(t("workflow.artifactGuideAddEvidence"))}</button>
      </section>
      <section>
        <strong>${escapeHTML(t("workflow.artifactGuideDownstreamTitle"))}</strong>
        <span>${escapeHTML(t("workflow.artifactGuideDownstreamBody"))}</span>
        <code>stages.${escapeHTML(stage.name || "stage")}.artifacts.report</code>
      </section>
    </div>`;
}

function updateStageAcceptanceGuide(root, stage, nodeType) {
  const guide = root.querySelector("#stageAcceptanceGuide");
  if (!guide || !stage) return;
  const supportsCriteria = stageFieldSet(nodeType).has("acceptance_criteria");
  guide.parentElement?.classList.toggle("hidden", !supportsCriteria);
  guide.classList.toggle("hidden", !supportsCriteria);
  if (!supportsCriteria) {
    guide.innerHTML = "";
    return;
  }
  const criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria);
  const tone = criteria.length ? "ready" : "neutral";
  guide.className = `workflow-artifact-guide workflow-acceptance-guide ${tone}`;
  guide.innerHTML = `
    <div class="workflow-artifact-guide-head">
      <span>${escapeHTML(t("workflow.acceptanceGuideKicker"))}</span>
      <strong>${escapeHTML(t("workflow.acceptanceGuideTitle"))}</strong>
    </div>
    <p>${escapeHTML(t("workflow.acceptanceGuideBody"))}</p>
    <div class="workflow-artifact-guide-grid">
      <section>
        <strong>${escapeHTML(t("workflow.acceptanceGuideRefTitle"))}</strong>
        <span>${escapeHTML(t("workflow.acceptanceGuideRefBody"))}</span>
        <code>ref=result.output</code>
        <button type="button" class="ghost-button" data-acceptance-guide-action="contains">${escapeHTML(t("workflow.acceptanceGuideAddContains"))}</button>
      </section>
      <section>
        <strong>${escapeHTML(t("workflow.acceptanceGuideCheckTitle"))}</strong>
        <span>${escapeHTML(t("workflow.acceptanceGuideCheckBody"))}</span>
        <code>contains=scope</code>
        <button type="button" class="ghost-button" data-acceptance-guide-action="exists">${escapeHTML(t("workflow.acceptanceGuideAddExists"))}</button>
      </section>
      <section>
        <strong>${escapeHTML(t("workflow.acceptanceGuideGateTitle"))}</strong>
        <span>${escapeHTML(t("workflow.acceptanceGuideGateBody"))}</span>
        <code>quality_gate</code>
      </section>
    </div>`;
}

function artifactForm(index, artifact) {
  return `<section class="artifact-item" data-artifact-index="${index}">
    <div class="artifact-item-head">
      <strong>${escapeHTML(artifact.name || t("workflow.artifact"))}</strong>
      <button type="button" class="ghost-button danger-text" data-remove-artifact="${index}">${escapeHTML(t("workflow.removeArtifact"))}</button>
    </div>
    <div class="artifact-grid">
      ${artifactInput(index, "name", t("workflow.artifactName"), artifact.name, "audit-report", t("workflow.artifactNameHelp"))}
      ${artifactInput(index, "kind", t("workflow.artifactKind"), artifact.kind, "report", t("workflow.artifactKindHelp"))}
      ${artifactInput(index, "title", t("workflow.artifactTitle"), artifact.title, t("workflow.artifactTitlePlaceholder"), t("workflow.artifactTitleHelp"))}
      ${artifactInput(index, "ref", t("workflow.artifactRef"), artifact.ref, "result.output", t("workflow.artifactRefHelp"))}
      ${artifactInput(index, "summary", t("workflow.artifactSummary"), artifact.summary, "result.summary", t("workflow.artifactSummaryHelp"))}
      <label class="artifact-field artifact-field-wide">
        <span>${escapeHTML(t("workflow.artifactContent"))}</span>
        <textarea data-artifact-field="content" class="compact-textarea" placeholder="${escapeHTML(t("workflow.artifactContentPlaceholder"))}">${escapeHTML(artifact.content || "")}</textarea>
        <small class="workflow-field-hint">${escapeHTML(t("workflow.artifactContentHelp"))}</small>
      </label>
      <label class="artifact-field artifact-field-wide">
        <span>${escapeHTML(t("workflow.artifactMetadata"))}</span>
        <textarea data-artifact-field="metadata" class="compact-textarea" placeholder="${escapeHTML(t("workflow.artifactMetadataPlaceholder"))}">${escapeHTML(formatMap(artifact.metadata))}</textarea>
        <small class="workflow-field-hint">${escapeHTML(t("workflow.artifactMetadataHelp"))}</small>
      </label>
    </div>
  </section>`;
}

function acceptanceCriterionForm(index, criterion) {
  return `<section class="artifact-item workflow-acceptance-item" data-acceptance-index="${index}">
    <div class="artifact-item-head">
      <strong>${escapeHTML(criterion.name || t("workflow.acceptanceCriterion"))}</strong>
      <button type="button" class="ghost-button danger-text" data-remove-acceptance="${index}">${escapeHTML(t("workflow.removeAcceptanceCriterion"))}</button>
    </div>
    <div class="artifact-grid workflow-acceptance-grid">
      ${artifactInput(index, "name", t("workflow.acceptanceName"), criterion.name, "has-scope", t("workflow.acceptanceNameHelp"), "data-acceptance-field")}
      ${artifactInput(index, "ref", t("workflow.acceptanceRef"), criterion.ref, "result.output", t("workflow.acceptanceRefHelp"), "data-acceptance-field")}
      ${artifactInput(index, "contains", t("workflow.acceptanceContains"), criterion.contains, "scope", t("workflow.acceptanceContainsHelp"), "data-acceptance-field")}
      ${artifactInput(index, "equals", t("workflow.acceptanceEquals"), criterion.equals, "passed", t("workflow.acceptanceEqualsHelp"), "data-acceptance-field")}
      ${artifactInput(index, "expected", t("workflow.acceptanceExpected"), criterion.expected, "Must include scope and report", t("workflow.acceptanceExpectedHelp"), "data-acceptance-field")}
      <label class="artifact-field">
        <span>${escapeHTML(t("workflow.acceptanceExists"))}</span>
        <select data-acceptance-field="exists">
          <option value="">${escapeHTML(t("workflow.acceptanceExistsAny"))}</option>
          <option value="true"${criterion.exists === true ? " selected" : ""}>${escapeHTML(t("workflow.acceptanceExistsTrue"))}</option>
          <option value="false"${criterion.exists === false ? " selected" : ""}>${escapeHTML(t("workflow.acceptanceExistsFalse"))}</option>
        </select>
        <small class="workflow-field-hint">${escapeHTML(t("workflow.acceptanceExistsHelp"))}</small>
      </label>
      <label class="artifact-field artifact-field-wide">
        <span>${escapeHTML(t("workflow.acceptanceDescription"))}</span>
        <textarea data-acceptance-field="description" class="compact-textarea" placeholder="${escapeHTML(t("workflow.acceptanceDescriptionPlaceholder"))}">${escapeHTML(criterion.description || "")}</textarea>
        <small class="workflow-field-hint">${escapeHTML(t("workflow.acceptanceDescriptionHelp"))}</small>
      </label>
    </div>
  </section>`;
}

function artifactInput(index, field, label, value, placeholder, help = "", attributeName = "data-artifact-field") {
  return `<label class="artifact-field">
    <span>${escapeHTML(label)}</span>
    <input ${attributeName}="${escapeHTML(field)}" value="${escapeHTML(value || "")}" placeholder="${escapeHTML(placeholder)}">
    ${help ? `<small class="workflow-field-hint">${escapeHTML(help)}</small>` : ""}
  </label>`;
}

function syncArtifactsFromForm(root) {
  const stage = selectedStage();
  if (!stage) return;
  stage.artifacts = readArtifactsFromForm(root);
  pruneEmptyStageFields(stage);
}

function readArtifactsFromForm(root) {
  const rows = root.querySelectorAll("#stageArtifactsList .artifact-item");
  return [...rows].map(row => {
    const artifact = {};
    row.querySelectorAll("[data-artifact-field]").forEach(input => {
      const field = input.dataset.artifactField;
      const value = input.value.trim();
      if (field === "metadata") {
        const metadata = parseMap(input.value);
        if (Object.keys(metadata).length) artifact.metadata = metadata;
        return;
      }
      if (value) artifact[field] = value;
    });
    return artifact;
  }).filter(hasArtifactValue);
}

function syncAcceptanceCriteriaFromForm(root) {
  const stage = selectedStage();
  if (!stage) return;
  stage.acceptance_criteria = readAcceptanceCriteriaFromForm(root);
  pruneEmptyStageFields(stage);
}

function readAcceptanceCriteriaFromForm(root) {
  const rows = root.querySelectorAll("#stageAcceptanceList .workflow-acceptance-item");
  return [...rows].map(row => {
    const criterion = {};
    row.querySelectorAll("[data-acceptance-field]").forEach(input => {
      const field = input.dataset.acceptanceField;
      const value = typeof input.value === "string" ? input.value.trim() : "";
      if (field === "exists") {
        if (value === "true") criterion.exists = true;
        if (value === "false") criterion.exists = false;
        return;
      }
      if (value) criterion[field] = value;
    });
    return criterion;
  }).filter(hasAcceptanceCriterionValue);
}

function normalizeArtifacts(value) {
  if (!Array.isArray(value)) return [];
  return value
    .filter(item => item && typeof item === "object")
    .map(item => ({
      name: String(item.name || ""),
      kind: String(item.kind || ""),
      title: String(item.title || ""),
      ref: String(item.ref || ""),
      summary: String(item.summary || ""),
      content: String(item.content || ""),
      metadata: item.metadata && typeof item.metadata === "object" && !Array.isArray(item.metadata) ? item.metadata : {}
    }))
    .filter(hasArtifactValue);
}

function normalizeAcceptanceCriteria(value) {
  if (!Array.isArray(value)) return [];
  return value
    .filter(item => item && typeof item === "object")
    .map(item => ({
      name: String(item.name || ""),
      description: String(item.description || ""),
      ref: String(item.ref || ""),
      equals: String(item.equals || ""),
      contains: String(item.contains || ""),
      expected: String(item.expected || ""),
      exists: typeof item.exists === "boolean" ? item.exists : null
    }))
    .filter(hasAcceptanceCriterionValue);
}

function hasArtifactValue(artifact) {
  return Boolean(
    artifact.name ||
    artifact.kind ||
    artifact.title ||
    artifact.ref ||
    artifact.summary ||
    artifact.content ||
    Object.keys(artifact.metadata || {}).length
  );
}

function hasAcceptanceCriterionValue(criterion) {
  return Boolean(
    criterion.name ||
    criterion.description ||
    criterion.ref ||
    criterion.equals ||
    criterion.contains ||
    criterion.expected ||
    typeof criterion.exists === "boolean"
  );
}

function createArtifactDraft(stage) {
  const used = new Set(normalizeArtifacts(stage.artifacts).map(item => item.name));
  let index = used.size + 1;
  let name = `artifact-${index}`;
  while (used.has(name)) {
    index++;
    name = `artifact-${index}`;
  }
  return {
    name,
    kind: "report",
    title: "",
    ref: "result.output",
    summary: "result.summary",
    content: "",
    metadata: {}
  };
}

function createNamedArtifactDraft(stage, baseName, overrides = {}) {
  const used = new Set(normalizeArtifacts(stage.artifacts).map(item => item.name));
  const safeBase = slug(baseName || "artifact") || "artifact";
  let name = safeBase;
  let index = 2;
  while (used.has(name)) {
    name = `${safeBase}-${index}`;
    index++;
  }
  return {
    ...createArtifactDraft(stage),
    name,
    ...overrides
  };
}

function addArtifactGuideExample(root, kind) {
  const stage = selectedStage();
  if (!stage) return;
  const artifact = kind === "evidence"
    ? createNamedArtifactDraft(stage, "evidence", {
      kind: "evidence",
      title: t("workflow.artifactGuideEvidenceTitle"),
      ref: "result.output",
      summary: "result.summary"
    })
    : createNamedArtifactDraft(stage, "report", {
      kind: "report",
      title: t("workflow.artifactGuideReportTitle"),
      ref: "result.output",
      summary: "result.summary"
    });
  stage.artifacts = normalizeArtifacts(stage.artifacts);
  stage.artifacts.push(artifact);
  renderArtifactsEditor(root, stage);
  updateStageArtifactsGuide(root, stage, normalizedNodeType(stage));
  updateWorkflowFormPanels(root);
  updateStagePlainSummary(root, stage, normalizedNodeType(stage));
  renderCanvas(root);
}

function createAcceptanceCriterionDraft(stage) {
  const used = new Set(normalizeAcceptanceCriteria(stage.acceptance_criteria).map(item => item.name));
  let index = used.size + 1;
  let name = `criterion-${index}`;
  while (used.has(name)) {
    index++;
    name = `criterion-${index}`;
  }
  return {
    name,
    description: "",
    ref: "result.output",
    equals: "",
    contains: "",
    expected: "",
    exists: null
  };
}

function createNamedAcceptanceCriterionDraft(stage, baseName, overrides = {}) {
  const used = new Set(normalizeAcceptanceCriteria(stage.acceptance_criteria).map(item => item.name));
  const safeBase = slug(baseName || "criterion") || "criterion";
  let name = safeBase;
  let index = 2;
  while (used.has(name)) {
    name = `${safeBase}-${index}`;
    index++;
  }
  return {
    ...createAcceptanceCriterionDraft(stage),
    name,
    ...overrides
  };
}

function addAcceptanceGuideExample(root, kind) {
  const stage = selectedStage();
  if (!stage) return;
  const criterion = kind === "exists"
    ? createNamedAcceptanceCriterionDraft(stage, "has-output", {
      description: t("workflow.acceptanceGuideExistsDescription"),
      ref: "result.output",
      exists: true
    })
    : createNamedAcceptanceCriterionDraft(stage, "contains-scope", {
      description: t("workflow.acceptanceGuideContainsDescription"),
      ref: "result.output",
      contains: "scope",
      expected: t("workflow.acceptanceGuideContainsExpected")
    });
  stage.acceptance_criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria);
  stage.acceptance_criteria.push(criterion);
  renderAcceptanceEditor(root, stage);
  updateStageAcceptanceGuide(root, stage, normalizedNodeType(stage));
  updateWorkflowFormPanels(root);
  updateStagePlainSummary(root, stage, normalizedNodeType(stage));
  renderCanvas(root);
}

function renderExecutionOrder(root) {
  const target = root.querySelector("#workflowOrderPath");
  if (!target) return;
  const order = workflowOrderPreview();
  if (!order.length) {
    target.innerHTML = `<span class="muted">${escapeHTML(t("workflow.executionOrderEmpty"))}</span>`;
    return;
  }
  target.innerHTML = order.map((name, index) => {
    const stage = state.graph.stages.find(item => item.name === name);
    const type = normalizedNodeType(stage);
    return `<span class="order-chip ${type} ${nodeTypeCategory(type)}"><small>${index + 1}</small>${escapeHTML(name)}</span>`;
  }).join("");
}

function workflowOrderPreview() {
  const stages = state.graph.stages || [];
  if (!stages.length) return [];
  const byName = new Map(stages.map(stage => [stage.name, stage]));
  const start = stages.find(stage => normalizedNodeType(stage) === "start") || stages[0];
  const queue = [start.name];
  const seen = new Set();
  const order = [];
  while (queue.length && order.length < stages.length) {
    const name = queue.shift();
    if (!name || seen.has(name)) continue;
    const stage = byName.get(name);
    if (!stage) continue;
    seen.add(name);
    order.push(name);
    const explicitNext = workflowOutgoingTargetNames(stage, byName);
    if (explicitNext.length) {
      queue.push(...explicitNext);
      continue;
    }
    const index = stages.findIndex(item => item.name === name);
    const fallback = stages[index + 1];
    if (fallback && !seen.has(fallback.name)) queue.push(fallback.name);
  }
  return order;
}

function workflowOutgoingTargetNames(stage, byName = null) {
  const seen = new Set();
  return workflowOutgoingLinks(stage)
    .map(link => link.target)
    .filter(name => {
      if (!name || seen.has(name) || (byName && !byName.has(name))) return false;
      seen.add(name);
      return true;
    });
}

function workflowOutgoingLinks(stage = {}) {
  const links = new Map();
  const type = normalizedNodeType(stage);
  const routeFirst = ["condition", "switch", "router", "policy_guard", "quality_gate", "quality_guard", "input_gate"].includes(type);
  const add = (target, entry = {}) => {
    const name = String(target || "").trim();
    if (!name || name === stage.name) return;
    const key = name;
    const current = links.get(key) || { target: name, order: entry.order || 0, labels: [], entries: [] };
    current.order = Math.min(current.order, entry.order || 0);
    if (entry.label && !current.labels.includes(entry.label)) current.labels.push(entry.label);
    current.entries.push(entry);
    links.set(key, current);
  };
  const addNext = (baseOrder = 0) => {
    (Array.isArray(stage.next) ? stage.next : []).forEach((target, index) => {
      add(target, { kind: "next", key: "next", order: baseOrder + index, label: t("workflow.nextStages") });
    });
  };
  const addMap = (values, kind, baseOrder = 0) => {
    Object.entries(values || {}).forEach(([key, value], index) => {
      workflowReferenceTargets(value).forEach((target, targetIndex) => {
        add(target, {
          kind,
          key,
          order: baseOrder + index * 10 + targetIndex,
          label: `${workflowDisplayValue(key)}: ${workflowDisplayValue(target)}`
        });
      });
    });
  };
  if (routeFirst) {
    addMap(stage.routes, "routes", 0);
    addMap(stage.cases, "cases", 100);
    addNext(1000);
  } else {
    addNext(0);
    addMap(stage.routes, "routes", 1000);
    addMap(stage.cases, "cases", 1100);
  }
  return [...links.values()].sort((left, right) => left.order - right.order || left.target.localeCompare(right.target));
}

function workflowReferenceTargets(value) {
  if (Array.isArray(value)) return value.flatMap(workflowReferenceTargets).filter(Boolean);
  if (value && typeof value === "object") {
    const direct = [value.target, value.stage, value.next, value.to].flatMap(workflowReferenceTargets).filter(Boolean);
    if (direct.length) return direct;
    return Object.values(value).flatMap(workflowReferenceTargets).filter(Boolean);
  }
  return String(value || "")
    .split(",")
    .map(item => item.trim())
    .filter(Boolean);
}

function syncStageFromForm(root) {
  const stage = selectedStage();
  if (!stage) return;
  clearWorkflowValidation(root);
  clearWorkflowTransfer(root);
  const oldName = stage.name;
  const oldTeamTemplate = stage.params?.team || stage.params?.template || "";
  stage.node_type = root.querySelector("#stageNodeType").value;
  stage.name = slug(root.querySelector("#stageName").value);
  if (oldName && stage.name && oldName !== stage.name) renameStageReferences(oldName, stage.name);
  stage.agent = root.querySelector("#stageAgent").value;
  stage.skill = root.querySelector("#stageSkill").value;
  stage.tool = root.querySelector("#stageTool").value;
  stage.next = root.querySelector("#stageNext").value.split(",").map(value => slug(value)).filter(Boolean);
  stage.next_strategy = root.querySelector("#stageNextStrategy").value.trim();
  stage.condition = root.querySelector("#stageCondition").value.trim();
  stage.policy = root.querySelector("#stagePolicy").value.trim();
  stage.switch_on = root.querySelector("#stageSwitchOn").value.trim();
  stage.routes = parseMap(root.querySelector("#stageRoutes").value);
  stage.cases = parseMap(root.querySelector("#stageCases").value);
  stage.input = parseMap(root.querySelector("#stageInputMap").value);
  stage.outputs = parseMap(root.querySelector("#stageOutputsMap").value);
  stage.params = parseParams(root.querySelector("#stageParams").value);
  applyDedicatedParamsFromForm(root, stage, normalizedNodeType(stage));
  if (normalizedNodeType(stage) === "team" && oldTeamTemplate && oldTeamTemplate !== stage.params.team) {
    delete stage.params.approval_preset;
    const presetSelect = root.querySelector("#stageTeamQuorumPreset");
    if (presetSelect) presetSelect.value = "";
  }
  if (normalizedNodeType(stage) === "team") {
    if (root.querySelector("#stageTeamExecute").checked) {
      stage.params.execute = true;
    } else if (stage.params.execute) {
      delete stage.params.execute;
    }
  }
  if (normalizedNodeType(stage) === "input_gate") {
    const fieldsJSON = root.querySelector("#stageInputFieldsJson").value.trim();
    if (fieldsJSON) {
      stage.params.fields_json = fieldsJSON;
    } else if (stage.params.fields_json) {
      delete stage.params.fields_json;
    }
  }
  const policyRule = root.querySelector("#stagePolicyRule").value.trim();
  if (policyRule) {
    stage.params.rule = policyRule;
  } else if (stage.params.rule) {
    delete stage.params.rule;
  }
  stage.artifacts = readArtifactsFromForm(root);
  stage.acceptance_criteria = readAcceptanceCriteriaFromForm(root);
  stage.approval = root.querySelector("#stageApproval").checked;
  const nodeType = normalizedNodeType(stage);
  if (!executableTypes.has(nodeType)) {
    stage.agent = "";
    stage.skill = "";
    stage.tool = "";
    stage.approval = false;
  }
  pruneUnsupportedStageFields(stage, nodeType);
  pruneEmptyStageFields(stage);
  renderArtifactsEditor(root, stage);
  updateStageArtifactsGuide(root, stage, nodeType);
  renderAcceptanceEditor(root, stage);
  updateStageAcceptanceGuide(root, stage, nodeType);
  updateStageFieldVisibility(root, nodeType);
  updateAdvancedFieldVisibility(root, nodeType);
  updateControlHelp(root, nodeType);
  updateStageAdvancedGuide(root, stage, nodeType);
  updateNodeTypeMeta(root, nodeType);
  updateTeamTemplatePreview(root, stage, nodeType);
  updateStagePlainSummary(root, stage, nodeType);
  updateStageRoutePreview(root, stage, nodeType);
  updateStageGuidance(root, stage, nodeType);
  updateStageDataFlow(root, stage, nodeType);
  updatePolicyRuleHelp(root);
  scheduleExpressionValidation(root, { force: true });
  scheduleWorkflowRepaint(root);
}

function pruneUnsupportedStageFields(stage, nodeType) {
  const baseVisible = stageFieldSet(nodeType);
  const advancedVisible = advancedFieldSet(nodeType);
  if (!baseVisible.has("next")) delete stage.next;
  if (!baseVisible.has("next_strategy")) delete stage.next_strategy;
  if (!baseVisible.has("agent")) delete stage.agent;
  if (!baseVisible.has("skill")) delete stage.skill;
  if (!baseVisible.has("tool")) delete stage.tool;
  if (!baseVisible.has("approval")) delete stage.approval;
  if (!baseVisible.has("artifacts")) delete stage.artifacts;
  if (!baseVisible.has("acceptance_criteria")) delete stage.acceptance_criteria;
  if (!baseVisible.has("params")) {
    if (advancedVisible.has("policy_rule")) {
      const rule = stage.params?.rule;
      stage.params = rule ? { rule } : {};
    } else {
      delete stage.params;
    }
  }
  if (nodeType !== "team" && stage.params) delete stage.params.execute;
  if (!advancedVisible.has("condition")) delete stage.condition;
  if (!advancedVisible.has("policy")) delete stage.policy;
  if (!advancedVisible.has("switch_on")) delete stage.switch_on;
  if (!advancedVisible.has("routes")) delete stage.routes;
  if (!advancedVisible.has("cases")) delete stage.cases;
  if (!advancedVisible.has("input")) delete stage.input;
  if (!advancedVisible.has("outputs")) delete stage.outputs;
}

function pruneEmptyStageFields(stage) {
  for (const key of ["condition", "policy", "switch_on", "next_strategy"]) {
    if (!stage[key]) delete stage[key];
  }
  for (const key of ["routes", "cases", "input", "outputs", "params"]) {
    if (!stage[key] || !Object.keys(stage[key]).length) delete stage[key];
  }
  if (!Array.isArray(stage.artifacts) || !stage.artifacts.length) delete stage.artifacts;
  if (!Array.isArray(stage.acceptance_criteria) || !stage.acceptance_criteria.length) delete stage.acceptance_criteria;
}

function addConnection(sourceName, targetName) {
  const source = state.graph.stages.find(stage => stage.name === sourceName);
  if (!source || !targetName || sourceName === targetName) return;
  source.next = source.next || [];
  if (!source.next.includes(targetName)) source.next.push(targetName);
  state.graphValidation = null;
  state.graphTransfer = null;
}

function removeConnection(root, sourceName, targetName) {
  const source = state.graph.stages.find(stage => stage.name === sourceName);
  if (!source) return;
  source.next = (source.next || []).filter(name => name !== targetName);
  removeStageMapReference(source.routes, targetName);
  removeStageMapReference(source.cases, targetName);
  clearWorkflowValidation(root);
  clearWorkflowTransfer(root);
  renderAll(root);
}

function startConnectionDrag(root, event, sourceName, index) {
  state.selected = index;
  state.connectSource = "";
  state.dragging = null;
  state.connecting = { sourceName, x: 0, y: 0 };
  updateConnectionDraft(root, event);
  renderStageForm(root);
  renderCanvas(root);
}

function finishConnectionDrag(root, event, explicitTargetName = "") {
  const sourceName = state.connecting?.sourceName;
  const targetName = explicitTargetName || stageNameFromEvent(event);
  state.connecting = null;
  state.dragging = null;
  if (sourceName && targetName && sourceName !== targetName) addConnection(sourceName, targetName);
  renderAll(root);
}

function updateConnectionDraft(root, event) {
  if (!state.connecting) return;
  const point = graphPoint(root, event);
  state.connecting.x = Math.max(0, point.x);
  state.connecting.y = Math.max(0, point.y);
}

function stageNameFromEvent(event) {
  const target = event?.target instanceof Element ? event.target.closest(".flow-node") : null;
  return target?.dataset?.stageName || "";
}

function renameStageReferences(oldName, newName) {
  for (const stage of state.graph.stages) {
    stage.next = (stage.next || []).map(name => name === oldName ? newName : name);
    replaceStageMapReference(stage.routes, oldName, newName);
    replaceStageMapReference(stage.cases, oldName, newName);
  }
  if (state.connectSource === oldName) state.connectSource = newName;
  if (state.connecting?.sourceName === oldName) state.connecting.sourceName = newName;
}

function replaceStageMapReference(values, oldName, newName) {
  Object.entries(values || {}).forEach(([key, value]) => {
    const nextValue = replaceReferenceTarget(value, oldName, newName);
    if (nextValue == null || nextValue === "") {
      delete values[key];
    } else {
      values[key] = nextValue;
    }
  });
}

function replaceReferenceTarget(value, oldName, newName) {
  if (Array.isArray(value)) return value.map(item => replaceReferenceTarget(item, oldName, newName)).filter(item => item != null && item !== "");
  if (value && typeof value === "object") {
    const next = { ...value };
    Object.keys(next).forEach(key => {
      const nextValue = replaceReferenceTarget(next[key], oldName, newName);
      if (nextValue == null || nextValue === "") {
        delete next[key];
      } else {
        next[key] = nextValue;
      }
    });
    return next;
  }
  const raw = String(value || "");
  const parts = raw.split(",");
  if (parts.length > 1) {
    return parts.map(part => part.trim() === oldName ? newName : part.trim()).filter(Boolean).join(", ");
  }
  return raw.trim() === oldName ? newName : raw;
}

function removeStageMapReference(values, targetName) {
  Object.entries(values || {}).forEach(([key, value]) => {
    const targets = workflowReferenceTargets(value);
    if (!targets.includes(targetName)) return;
    const remaining = targets.filter(target => target !== targetName);
    if (!remaining.length) {
      delete values[key];
    } else {
      values[key] = remaining.join(", ");
    }
  });
}

function nodeRects(root) {
  const surface = root.querySelector("#canvasSurface");
  const surfaceRect = surface.getBoundingClientRect();
  const rects = new Map();
  surface.querySelectorAll(".flow-node").forEach(node => {
    const rect = node.getBoundingClientRect();
    rects.set(node.dataset.stageName, {
      left: (rect.left - surfaceRect.left) / state.zoom,
      top: (rect.top - surfaceRect.top) / state.zoom,
      width: rect.width / state.zoom,
      height: rect.height / state.zoom
    });
  });
  return rects;
}

function edgeAnchors(sourceRect, targetRect, slot = {}) {
  const leftToRight = workflowEdgeLeftToRight(sourceRect, targetRect);
  const sourceOffset = edgeSlotOffset(slot.sourceIndex || 0, slot.sourceCount || 1, sourceRect.height);
  const targetOffset = edgeSlotOffset(slot.targetIndex || 0, slot.targetCount || 1, targetRect.height);
  const from = {
    x: leftToRight ? sourceRect.left + sourceRect.width : sourceRect.left,
    y: sourceRect.top + sourceRect.height / 2 + sourceOffset,
    direction: leftToRight ? 1 : -1
  };
  const to = {
    x: leftToRight ? targetRect.left : targetRect.left + targetRect.width,
    y: targetRect.top + targetRect.height / 2 + targetOffset,
    direction: leftToRight ? -1 : 1
  };
  return { from, to };
}

function workflowEdgeLeftToRight(sourceRect, targetRect) {
  const sourceCenter = sourceRect.left + sourceRect.width / 2;
  const targetCenter = targetRect.left + targetRect.width / 2;
  return sourceCenter <= targetCenter;
}

function edgeSlotOffset(index, count, height) {
  if (count <= 1) return 0;
  const span = Math.max(0, height / 2 - 32);
  if (!span) return 0;
  const step = Math.min(24, (span * 2) / Math.max(1, count - 1));
  return Math.round((index - (count - 1) / 2) * step);
}

function edgeD({ from, to }) {
  const { c1, c2 } = edgeControlPoints(from, to);
  return `M ${from.x} ${from.y} C ${c1.x} ${c1.y}, ${c2.x} ${c2.y}, ${to.x} ${to.y}`;
}

function edgeControlPoints(from, to) {
  const distance = Math.max(72, Math.abs(to.x - from.x) * 0.45);
  return {
    c1: { x: from.x + distance * from.direction, y: from.y },
    c2: { x: to.x + distance * to.direction, y: to.y }
  };
}

function edgePath(d, className) {
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("class", className);
  path.setAttribute("d", d);
  return path;
}

function edgeMidpoint({ from, to }) {
  const { c1, c2 } = edgeControlPoints(from, to);
  const t = 0.5;
  const mt = 1 - t;
  return {
    x: mt ** 3 * from.x + 3 * mt ** 2 * t * c1.x + 3 * mt * t ** 2 * c2.x + t ** 3 * to.x,
    y: mt ** 3 * from.y + 3 * mt ** 2 * t * c1.y + 3 * mt * t ** 2 * c2.y + t ** 3 * to.y
  };
}

function edgeDeleteButton(root, sourceName, targetName, point, link = null) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "edge-action";
  const label = link?.labels?.length ? link.labels.join(" / ") : `${sourceName} -> ${targetName}`;
  button.title = `${t("workflow.deleteEdgeTitle")} ${label}`;
  button.setAttribute("aria-label", `${t("workflow.deleteEdge")}: ${sourceName} -> ${targetName}`);
  button.setAttribute("aria-keyshortcuts", "Enter Space");
  button.dataset.actionHint = t("workflow.deleteEdge");
  button.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4c0-1.1.9-2 2-2h4c1.1 0 2 .9 2 2v2"/><path d="M19 6l-1 14c-.1 1.1-1 2-2.1 2H8.1c-1.1 0-2-.9-2.1-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>`;
  button.style.left = `${point.x}px`;
  button.style.top = `${point.y}px`;
  button.onclick = event => {
    event.stopPropagation();
    removeConnection(root, sourceName, targetName);
  };
  return button;
}

function removeSelectedStage() {
  removeStageAt(state.selected);
}

function removeStageAt(index) {
  if (index < 0 || index >= state.graph.stages.length) return;
  state.graphValidation = null;
  state.graphTransfer = null;
  state.selected = index;
  const stage = selectedStage();
  if (!stage) return;
  state.graph.stages.splice(index, 1);
  for (const other of state.graph.stages) {
    other.next = (other.next || []).filter(name => name !== stage.name);
    removeStageMapReference(other.routes, stage.name);
    removeStageMapReference(other.cases, stage.name);
  }
  if (state.connectSource === stage.name) state.connectSource = "";
  if (state.connecting?.sourceName === stage.name) state.connecting = null;
  state.selected = Math.min(index, state.graph.stages.length - 1);
}

async function validateGraph(root) {
  const button = root.querySelector("#validateGraph");
  try {
    state.graph.name = slug(root.querySelector("#graphName").value);
    state.graph.description = root.querySelector("#graphDescription").value.trim();
    state.graphValidation = { loading: true };
    renderWorkflowValidation(root);
    setWorkflowActionPending(button, true);
    const result = await request("/api/workflow-graphs/validate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(state.graph)
    });
    state.graphValidation = { result };
  } catch (error) {
    state.graphValidation = { error: localizedWorkflowErrorMessage(error, t("workflow.validationIssueFallback")) };
  } finally {
    setWorkflowActionPending(button, false);
    renderWorkflowValidation(root);
  }
}

function renderWorkflowValidation(root) {
  const panel = root.querySelector("#workflowValidationPanel");
  if (!panel) return;
  const snapshot = state.graphValidation;
  panel.classList.toggle("hidden", !snapshot);
  if (!snapshot) {
    panel.innerHTML = "";
    updateWorkflowBoardStatusVisibility(root);
    return;
  }
  if (snapshot.loading) {
    panel.className = "workflow-validation-panel loading";
    panel.innerHTML = `<strong>${escapeHTML(t("workflow.validationChecking"))}</strong><span>${escapeHTML(t("workflow.validationCheckingHelp"))}</span>`;
    updateWorkflowBoardStatusVisibility(root);
    return;
  }
  if (snapshot.error) {
    panel.className = "workflow-validation-panel error";
    panel.innerHTML = `<strong>${escapeHTML(t("workflow.validationFailed"))}</strong><span>${escapeHTML(workflowValidationMessage({ message: snapshot.error }))}</span>`;
    updateWorkflowBoardStatusVisibility(root);
    return;
  }
  const result = snapshot.result || {};
  const issues = Array.isArray(result.issues) ? result.issues : [];
  const errors = issues.filter(issue => String(issue.level || "").toLowerCase() === "error").length;
  const warnings = issues.length - errors;
  const parallel = Array.isArray(result.parallel) ? result.parallel : [];
  const parallelWarnings = parallel.reduce((total, item) => total + (Array.isArray(item.issues) ? item.issues.length : 0), 0);
  const warningTotal = warnings + parallelWarnings;
  const tone = result.valid && !warningTotal ? "ready" : errors ? "error" : "warn";
  const title = result.valid ? t("workflow.validationReady") : t("workflow.validationNeedsWork");
  const issueRows = issues.slice(0, 6).map(issue => workflowValidationIssueRow(issue)).join("");
  const parallelRows = parallel.slice(0, 3).map(item => workflowParallelValidationRow(item)).join("");
  panel.className = `workflow-validation-panel ${tone}`;
  panel.innerHTML = `
    <div class="workflow-validation-head">
      <div>
        <strong>${escapeHTML(title)}</strong>
        <span>${escapeHTML(t("workflow.validationSummary", { stages: result.stages || state.graph.stages.length, errors, warnings: warningTotal }))}</span>
      </div>
      <small>${escapeHTML(result.name || state.graph.name || t("workflow.graphName"))}</small>
    </div>
    ${issueRows ? `<div class="workflow-validation-list">${issueRows}</div>` : `<p>${escapeHTML(t("workflow.validationNoIssues"))}</p>`}
    ${parallelRows ? `<div class="workflow-validation-parallel">${parallelRows}</div>` : ""}`;
  updateWorkflowBoardStatusVisibility(root);
}

const workflowValidationMessagePatterns = [
  { pattern: /^invalid workflow name:\s*(.+)$/, key: "workflow.validationMessage.invalidWorkflowName", vars: match => ({ name: match[1] }) },
  { pattern: /^invalid workflow name "([^"]+)": use lowercase letters, numbers, hyphen, or underscore$/, key: "workflow.validationMessage.invalidWorkflowNameFormat", vars: match => ({ name: match[1] }) },
  { pattern: /^workflow graph missing name$/, key: "workflow.validationMessage.graphMissingName" },
  { pattern: /^workflow graph name "([^"]+)" does not match "([^"]+)"$/, key: "workflow.validationMessage.nameMismatch", vars: match => ({ actual: match[1], expected: match[2] }) },
  { pattern: /^workflow graph (.+) has no stages$/, key: "workflow.validationMessage.noStages", vars: match => ({ graph: match[1] }) },
  { pattern: /^workflow graph (.+) stage (\d+) missing name$/, key: "workflow.validationMessage.stageMissingName", vars: match => ({ graph: match[1], index: match[2] }) },
  { pattern: /^workflow graph (.+) has duplicate stage "([^"]+)"$/, key: "workflow.validationMessage.duplicateStage", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) references unknown team template (.+)$/, key: "workflow.validationMessage.unknownTeamTemplate", vars: match => ({ graph: match[1], stage: match[2], team: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) missing repeat body params\.stage\/body$/, key: "workflow.validationMessage.missingRepeatBody", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) missing sub workflow params\.workflow$/, key: "workflow.validationMessage.missingSubWorkflow", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) cannot call itself as a sub workflow$/, key: "workflow.validationMessage.selfSubWorkflow", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) missing agent$/, key: "workflow.validationMessage.missingAgent", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) missing skill$/, key: "workflow.validationMessage.missingSkill", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) retry\.max_attempts must not be negative$/, key: "workflow.validationMessage.negativeRetry", vars: match => ({ graph: match[1], stage: match[2] }) },
  { pattern: /^workflow graph (.+) stage (.+) references unknown repeat body stage "([^"]+)"$/, key: "workflow.validationMessage.unknownRepeatBody", vars: match => ({ graph: match[1], stage: match[2], target: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) references unknown next stage "([^"]+)"$/, key: "workflow.validationMessage.unknownNext", vars: match => ({ graph: match[1], stage: match[2], target: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) references invalid sub workflow "([^"]+)": (.+)$/, key: "workflow.validationMessage.invalidSubWorkflow", vars: match => ({ graph: match[1], stage: match[2], workflow: match[3], issue: localizeWorkflowValidationMessage(match[4]) }) },
  { pattern: /^workflow graph (.+) stage (.+) artifact (.+) has invalid name$/, key: "workflow.validationMessage.invalidArtifactName", vars: match => ({ graph: match[1], stage: match[2], artifact: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) has duplicate artifact "([^"]+)"$/, key: "workflow.validationMessage.duplicateArtifact", vars: match => ({ graph: match[1], stage: match[2], artifact: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) artifact (.+) must declare ref or content$/, key: "workflow.validationMessage.artifactNeedsRefOrContent", vars: match => ({ graph: match[1], stage: match[2], artifact: match[3] }) },
  { pattern: /^workflow graph (.+) stage (.+) artifact (.+) has invalid kind$/, key: "workflow.validationMessage.invalidArtifactKind", vars: match => ({ graph: match[1], stage: match[2], artifact: match[3] }) },
  { pattern: /^complex workflow (.+) should declare acceptance criteria on key stages$/, key: "workflow.validationMessage.complexNeedsAcceptance", vars: match => ({ graph: match[1] }) },
  { pattern: /^complex workflow (.+) should include a quality_gate before final delivery or release$/, key: "workflow.validationMessage.complexNeedsQualityGate", vars: match => ({ graph: match[1] }) },
  { pattern: /^complex workflow (.+) should include a verifier, auditor, reviewer, or quality stage$/, key: "workflow.validationMessage.complexNeedsVerifier", vars: match => ({ graph: match[1] }) },
  { pattern: /^complex workflow (.+) should declare replay artifacts for important evidence$/, key: "workflow.validationMessage.complexNeedsArtifacts", vars: match => ({ graph: match[1] }) },
  { pattern: /^complex workflow (.+) should declare output contracts for downstream data flow$/, key: "workflow.validationMessage.complexNeedsOutputs", vars: match => ({ graph: match[1] }) },
  { pattern: /^complex workflow (.+) should include a checkpoint, approval, input gate, or explicit human review path for risky tasks$/, key: "workflow.validationMessage.complexNeedsHumanReview", vars: match => ({ graph: match[1] }) },
  { pattern: /^workflow expression is required$/, key: "workflow.validationMessage.expressionRequired" },
  { pattern: /^malformed workflow expression function (.+)$/, key: "workflow.validationMessage.malformedExpressionFunction", vars: match => ({ name: match[1] }) },
  { pattern: /^unsupported workflow expression function (.+)$/, key: "workflow.validationMessage.unsupportedExpressionFunction", vars: match => ({ name: match[1] }) },
  { pattern: /^(.+)\(\) expects (.+)$/, key: "workflow.validationMessage.functionExpects", vars: match => ({ name: match[1], expected: workflowValidationExpectedArgs(match[2]) }) },
  { pattern: /^concurrent execution is not requested; set params\.concurrent=true to enable runtime parallel branch execution$/, key: "workflow.validationMessage.concurrentNotRequested" },
  { pattern: /^concurrent execution requires at least two branch targets$/, key: "workflow.validationMessage.concurrentNeedsTargets" },
  { pattern: /^branch (.+) joins (.+), but previous branches join (.+)$/, key: "workflow.validationMessage.branchJoinMismatch", vars: match => ({ branch: match[1], join: match[2], expected: match[3] }) },
  { pattern: /^branch (.+) overlaps stage (.+) already used by branch (.+)$/, key: "workflow.validationMessage.branchOverlap", vars: match => ({ branch: match[1], stage: match[2], owner: match[3] }) },
  { pattern: /^agent (.+) is used by both branch (.+) and branch (.+)$/, key: "workflow.validationMessage.agentBranchConflict", vars: match => ({ agent: match[1], left: match[2], right: match[3] }) },
  { pattern: /^branch target index is out of range$/, key: "workflow.validationMessage.branchTargetOutOfRange" },
  { pattern: /^branch path leaves the workflow stage list$/, key: "workflow.validationMessage.branchPathLeavesList" },
  { pattern: /^branch path loops before reaching a join node$/, key: "workflow.validationMessage.branchPathLoops" },
  { pattern: /^branch stage uses next_strategy, so runtime cannot determine a deterministic join path before execution$/, key: "workflow.validationMessage.branchNextStrategy" },
  { pattern: /^branch is blocked by a prior exclusive control decision$/, key: "workflow.validationMessage.branchBlocked" },
  { pattern: /^branch stage must have exactly one deterministic next target before the join; found (\d+)$/, key: "workflow.validationMessage.branchNeedsSingleNext", vars: match => ({ count: match[1] }) },
  { pattern: /^branch next target index is out of range$/, key: "workflow.validationMessage.branchNextOutOfRange" },
  { pattern: /^visual-only start\/end nodes cannot execute as concurrent branch stages$/, key: "workflow.validationMessage.visualConcurrentUnsupported" },
  { pattern: /^repeat nodes cannot execute inside concurrent branches$/, key: "workflow.validationMessage.repeatConcurrentUnsupported" },
  { pattern: /^sub-workflow nodes cannot execute inside concurrent branches$/, key: "workflow.validationMessage.subWorkflowConcurrentUnsupported" },
  { pattern: /^join nodes terminate branches and cannot be branch work stages$/, key: "workflow.validationMessage.joinConcurrentUnsupported" },
  { pattern: /^control nodes cannot execute inside concurrent branches$/, key: "workflow.validationMessage.controlConcurrentUnsupported" },
  { pattern: /^approval-gated branch stages cannot run concurrently$/, key: "workflow.validationMessage.approvalConcurrentUnsupported" },
  { pattern: /^workflow stage (.+) references unknown skill (.+)$/, key: "workflow.validationMessage.unknownStageSkill", vars: match => ({ stage: match[1], skill: match[2] }) },
  { pattern: /^workflow agent not configured: (.+)$/, key: "workflow.validationMessage.workflowAgentMissing", vars: match => ({ agent: match[1] }) },
  { pattern: /^unknown agent: (.+)$/, key: "workflow.validationMessage.workflowAgentMissing", vars: match => ({ agent: match[1] }) },
  { pattern: /^default agent not configured$/, key: "workflow.validationMessage.defaultAgentMissing" },
  { pattern: /^skill manager not configured$/, key: "workflow.validationMessage.skillManagerMissing" },
  { pattern: /^unsupported workflow reference "([^"]+)"$/, key: "workflow.validationMessage.unsupportedReference", vars: match => ({ ref: match[1] }) },
  { pattern: /^workflow reference "([^"]+)" must include stage and result\/output path$/, key: "workflow.validationMessage.referenceNeedsStagePath", vars: match => ({ ref: match[1] }) },
  { pattern: /^workflow reference "([^"]+)" uses unknown stage "([^"]+)"$/, key: "workflow.validationMessage.referenceUnknownStage", vars: match => ({ ref: match[1], stage: match[2] }) },
  { pattern: /^workflow reference "([^"]+)" uses dynamic result path$/, key: "workflow.validationMessage.referenceDynamicResult", vars: match => ({ ref: match[1] }) },
  { pattern: /^workflow reference "([^"]+)" must use outputs or result$/, key: "workflow.validationMessage.referenceNeedsResultOrOutputs", vars: match => ({ ref: match[1] }) },
  { pattern: /^workflow reference "([^"]+)" uses unknown output "([^"]+)" on stage "([^"]+)"$/, key: "workflow.validationMessage.referenceUnknownOutput", vars: match => ({ ref: match[1], output: match[2], stage: match[3] }) },
  { pattern: /^workflow reference "([^"]+)" indexes an array with non-numeric segment "([^"]+)"$/, key: "workflow.validationMessage.referenceBadArrayIndex", vars: match => ({ ref: match[1], segment: match[2] }) },
  { pattern: /^workflow reference "([^"]+)" indexes an array outside observed bounds$/, key: "workflow.validationMessage.referenceArrayOutOfBounds", vars: match => ({ ref: match[1] }) },
  { pattern: /^workflow reference "([^"]+)" tries to access nested path on (.+) output$/, key: "workflow.validationMessage.referenceNestedOnType", vars: match => ({ ref: match[1], type: localizedText(match[2]) }) },
  { pattern: /^workflow reference "([^"]+)" uses missing object key "([^"]+)"$/, key: "workflow.validationMessage.referenceMissingObjectKey", vars: match => ({ ref: match[1], key: match[2] }) }
];

function workflowValidationMessage(issue = {}) {
  return localizeWorkflowValidationMessage(issue.message || issue.detail || issue.reason || "", issue);
}

function localizeWorkflowValidationMessage(message, issue = {}) {
  const text = String(message || "").trim();
  if (!text) return t("workflow.validationIssueFallback");
  const nested = localizeWorkflowValidationPrefix(text, issue);
  if (nested) return nested;
  for (const entry of workflowValidationMessagePatterns) {
    const match = text.match(entry.pattern);
    if (!match) continue;
    return t(entry.key, entry.vars ? entry.vars(match, issue) : undefined);
  }
  return workflowValidationFallbackMessage(text, issue);
}

function localizeWorkflowValidationPrefix(text, issue) {
  let match = text.match(/^invalid workflow graph: (.+)$/);
  if (match) {
    return t("workflow.validationMessage.invalidGraph", { issue: localizeWorkflowValidationMessage(match[1], issue) });
  }
  match = text.match(/^graph validation issue may affect expression validation: (.+)$/);
  if (match) {
    return t("workflow.validationMessage.graphIssueAffectsExpression", { issue: localizeWorkflowValidationMessage(match[1], issue) });
  }
  return "";
}

function workflowValidationExpectedArgs(value) {
  const text = String(value || "").trim();
  const exact = {
    "one argument": t("workflow.validationArgs.one"),
    "two arguments": t("workflow.validationArgs.two"),
    "one or two arguments": t("workflow.validationArgs.oneOrTwo")
  };
  if (exact[text]) return exact[text];
  let match = text.match(/^exactly (.+) arguments$/);
  if (match) return t("workflow.validationArgs.exactly", { count: workflowValidationArgCount(match[1]) });
  match = text.match(/^(.+) to (.+) arguments$/);
  if (match) return t("workflow.validationArgs.range", { min: workflowValidationArgCount(match[1]), max: workflowValidationArgCount(match[2]) });
  return localizedText(text);
}

function workflowValidationArgCount(value) {
  return {
    zero: "0",
    one: "1",
    two: "2",
    three: "3"
  }[String(value || "").trim()] || String(value || "").trim();
}

function workflowValidationFallbackMessage(text, issue = {}) {
  const localized = localizedText(text);
  if (localized !== text) return localized;
  const stage = String(issue.stage || "").trim();
  const field = workflowValidationFieldLabel(issue.field || "");
  const lower = text.toLowerCase();
  if (lower.includes("required") || lower.includes("missing")) {
    return stage
      ? t("workflow.validationFallbackMissingStage", { stage, field })
      : t("workflow.validationFallbackMissingField", { field });
  }
  if (lower.includes("unknown") || lower.includes("not found")) {
    return stage
      ? t("workflow.validationFallbackUnknownStage", { stage, field, detail: text })
      : t("workflow.validationFallbackUnknown", { field, detail: text });
  }
  if (lower.includes("invalid") || lower.includes("unsupported") || lower.includes("malformed")) {
    return stage
      ? t("workflow.validationFallbackInvalidStage", { stage, field, detail: text })
      : t("workflow.validationFallbackInvalid", { field, detail: text });
  }
  if (lower.includes("complex workflow") || lower.includes("quality") || lower.includes("acceptance") || lower.includes("evidence")) {
    return t("workflow.validationFallbackSuggestion", { detail: workflowPlainDisplayText(text) });
  }
  return workflowPlainDisplayText(text);
}

function workflowValidationIssueRow(issue = {}) {
  const level = String(issue.level || "warning").toLowerCase();
  const label = workflowValidationIssueLabel(issue);
  return `<article class="${escapeHTML(level)}">
    <span>${escapeHTML(level === "error" ? t("workflow.validationError") : t("workflow.validationWarning"))}</span>
    <strong>${escapeHTML(label)}</strong>
    <small>${escapeHTML(workflowValidationMessage(issue))}</small>
  </article>`;
}

function workflowValidationIssueLabel(issue = {}) {
  const parts = [];
  if (issue.stage) parts.push(issue.stage);
  if (issue.field) parts.push(workflowValidationFieldLabel(issue.field));
  return parts.filter(Boolean).join(" / ") || t("workflow.validationGraphLevel");
}

function workflowValidationFieldLabel(field) {
  const value = String(field || "").trim();
  const labels = {
    name: t("workflow.graphName"),
    stages: t("workflow.stages"),
    node_type: t("workflow.nodeType"),
    agent: t("workflow.agent"),
    skill: t("workflow.skill"),
    tool: t("workflow.toolMetadata"),
    next: t("workflow.nextStages"),
    next_strategy: t("workflow.nextStrategy"),
    condition: t("workflow.condition"),
    switch_on: t("workflow.switchOn"),
    routes: t("workflow.routes"),
    cases: t("workflow.cases"),
    input: t("workflow.inputMap"),
    params: t("workflow.parameters"),
    "params.concurrent": t("workflow.concurrent"),
    "params.stage": t("workflow.bodyStage"),
    "params.body": t("workflow.bodyStage"),
    "params.workflow": t("workflow.workflow"),
    "params.team": t("workflow.teamTemplate"),
    "params.items": t("workflow.eachItems"),
    "params.items_ref": t("workflow.eachItems"),
    "params.until": t("workflow.loopUntil"),
    "params.max_iterations": t("workflow.loopMaxIterations"),
    "params.prompt": t("workflow.checkpointPrompt"),
    "params.policy_rule": t("workflow.policyRule"),
    "params.fields": t("workflow.inputFieldsJson"),
    "params.fields_json": t("workflow.inputFieldsJson"),
    policy: t("workflow.policy"),
    policy_rule: t("workflow.policyRule"),
    "retry.max_attempts": t("workflow.maxAttempts"),
    acceptance_criteria: t("workflow.runtimeField.acceptance"),
    outputs: t("workflow.outputsMap"),
    approval: t("workflow.approvalGate"),
    artifacts: t("workflow.artifacts"),
    "artifacts.name": t("workflow.artifactName"),
    "artifacts.ref": t("workflow.artifactRef"),
    "artifacts.kind": t("workflow.artifactKind"),
    expression: t("workflow.expressionAssistTitle")
  };
  return labels[value] || localizedText(value || t("workflow.validationGraphLevel"));
}

function workflowParallelValidationRow(item = {}) {
  const tone = item.eligible ? "ready" : "warn";
  const branchCount = Number(item.branch_count || item.branchCount || 0);
  const firstIssue = Array.isArray(item.issues) ? item.issues.find(issue => issue?.message || issue?.reason) : null;
  const body = item.eligible
    ? t("workflow.validationParallelReady", { count: branchCount })
    : t("workflow.validationParallelReview", { count: branchCount });
  const detail = firstIssue ? workflowValidationMessage(firstIssue) : "";
  return `<article class="${tone}">
    <span>${escapeHTML(t("workflow.validationParallel"))}</span>
    <strong>${escapeHTML(item.stage || "-")}</strong>
    <small>${escapeHTML(detail && detail !== body ? `${body} ${detail}` : body)}</small>
  </article>`;
}

function clearWorkflowValidation(root) {
  if (!state.graphValidation) return;
  state.graphValidation = null;
  if (root) renderWorkflowValidation(root);
}

function workflowResourceActionLabel(actionName, fallback) {
  const action = resourceAction(state.resourceCapabilities, "workflow", actionName);
  return action ? resourceActionLabel(action, fallback) : fallback;
}

function workflowGraphCollectionEndpoint(fallback = "/api/workflow-graphs") {
  const capability = workflowResourceCapability();
  const declaredPath = String(capability?.collection_path || "").trim();
  if (capability) {
    if (capability.can_list === false || !declaredPath) return "";
    return declaredPath;
  }
  return fallback;
}

function workflowGraphDetailEndpoint(name, fallback = "") {
  const capability = workflowResourceCapability();
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) return declaredPath ? resourceActionPath({ path: declaredPath }, name, { format: "json" }) : "";
  return fallback;
}

function workflowGraphWriteEndpoint(name, fallback = "") {
  const capability = workflowResourceCapability();
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) {
    if ((capability.can_update === false && capability.can_create === false) || !declaredPath) return "";
    return resourceActionPath({ path: declaredPath }, name, { format: "json" });
  }
  return fallback;
}

function workflowGraphDeleteEndpoint(name, fallback = "") {
  const capability = workflowResourceCapability();
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) {
    if (capability.can_delete === false || !declaredPath) return "";
    return resourceActionPath({ path: declaredPath }, name, { format: "json" });
  }
  return fallback;
}

function workflowResourceCapability() {
  return workflowStudioResourceCapability("workflow");
}

function workflowStudioResourceCapability(kind) {
  return resourceCapability(state.resourceCapabilities, kind);
}

function workflowTemplateCollectionEndpoint(fallback = "/api/workflow-templates") {
  const capability = workflowStudioResourceCapability("workflow-template");
  const declaredPath = String(capability?.collection_path || "").trim();
  if (capability) {
    if (capability.can_list === false || !declaredPath) return "";
    return declaredPath;
  }
  return fallback;
}

function workflowTemplateCollectionEndpoints(fallback = "/api/workflow-templates") {
  const endpoint = workflowTemplateCollectionEndpoint(fallback);
  return workflowStudioEndpointCandidates(endpoint, fallback);
}

function workflowTemplateDetailEndpoint(name, fallback = "") {
  const capability = workflowStudioResourceCapability("workflow-template");
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) return declaredPath ? resourceActionPath({ path: declaredPath }, name, { format: "json" }) : "";
  return fallback;
}

function teamTemplateDetailEndpoint(name, fallback = "") {
  const capability = workflowStudioResourceCapability("team-template");
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) return declaredPath ? resourceActionPath({ path: declaredPath }, name, { format: "json" }) : "";
  return fallback;
}

function workflowStudioEndpointCandidates(...values) {
  return [...new Set(values.map(value => String(value || "").trim()).filter(Boolean))];
}

async function requestFirstWorkflowStudioEndpoint(endpoints = [], fallbackMessage = "") {
  let lastError = null;
  for (const endpoint of endpoints) {
    try {
      return await request(endpoint);
    } catch (error) {
      lastError = error;
    }
  }
  if (lastError) throw lastError;
  throw new Error(fallbackMessage || t("workflow.graphUnavailable"));
}

function loadWorkflowStudioWorkflowTemplateDetail(name) {
  const fallback = `/api/workflow-templates/${encodeURIComponent(name)}`;
  const primary = workflowTemplateDetailEndpoint(name, fallback);
  return requestFirstWorkflowStudioEndpoint(workflowStudioEndpointCandidates(primary, fallback), t("workflow.templateApplyFailed"));
}

function loadWorkflowStudioTeamTemplateDetail(name) {
  const fallback = `/api/team-templates/${encodeURIComponent(name)}`;
  const primary = teamTemplateDetailEndpoint(name, fallback);
  return requestFirstWorkflowStudioEndpoint(workflowStudioEndpointCandidates(primary, fallback), t("workflow.teamTemplateLoadFailed"));
}

async function exportGraph(root) {
  const button = root.querySelector("#exportGraph");
  const name = slug(root.querySelector("#graphName").value || state.graph.name || "");
  if (!name) {
    state.graphTransfer = { tone: "error", title: t("workflow.exportFailed"), body: t("workflow.nameRequired") };
    renderWorkflowTransfer(root);
    return;
  }
  try {
    setWorkflowActionPending(button, true);
    state.graphTransfer = { tone: "loading", title: t("workflow.exportLoading"), body: t("workflow.exportLoadingHelp", { name }) };
    renderWorkflowTransfer(root);
    const action = resourceAction(state.resourceCapabilities, "workflow", "export");
    const endpoint = resourceActionPath(action, name, { format: "yaml" }) || `/api/workflow-graphs/${encodeURIComponent(name)}/export?format=yaml`;
    const response = await fetch(endpoint);
    if (!response.ok) throw new Error((await response.text()) || response.statusText);
    const blob = await response.blob();
    const filename = workflowExportFilename(response, `${name}.yaml`);
    triggerWorkflowDownload(blob, filename);
    state.graphTransfer = { tone: "ready", title: t("workflow.exportedGraph"), body: t("workflow.exportedGraphHelp", { name: filename }) };
  } catch (error) {
    state.graphTransfer = { tone: "error", title: t("workflow.exportFailed"), body: t("workflow.exportFailedHelp", { message: localizedWorkflowErrorMessage(error, t("workflow.exportFailed")) }) };
  } finally {
    setWorkflowActionPending(button, false);
    renderWorkflowTransfer(root);
  }
}

async function importGraphFile(root, file) {
  if (!file) return;
  if (!confirm(t("workflow.importConfirm"))) return;
  const button = root.querySelector("#importGraph");
  try {
    setWorkflowActionPending(button, true);
    state.graphTransfer = { tone: "loading", title: t("workflow.importLoading"), body: t("workflow.importLoadingHelp", { name: file.name }) };
    renderWorkflowTransfer(root);
    const body = await file.text();
    const action = resourceAction(state.resourceCapabilities, "workflow", "import");
    const endpoint = resourceActionPath(action) || "/api/workflow-graphs/import";
    const imported = await request(endpoint, {
      method: resourceActionMethod(action, "POST"),
      headers: { "Content-Type": workflowImportContentType(file.name) },
      body
    });
    state.graph = normalizeGraph(imported);
    state.selected = state.graph.stages.length ? 0 : -1;
    state.connectSource = "";
    state.graphValidation = null;
    state.graphTransfer = { tone: "ready", title: t("workflow.importedGraph"), body: t("workflow.importedGraphHelp", { name: state.graph.name || file.name }) };
    await loadWorkflowList();
    renderAll(root);
  } catch (error) {
    state.graphTransfer = { tone: "error", title: t("workflow.importFailed"), body: t("workflow.importFailedHelp", { message: localizedWorkflowErrorMessage(error, t("workflow.importFailed")) }) };
    renderWorkflowTransfer(root);
  } finally {
    setWorkflowActionPending(button, false);
  }
}

function renderWorkflowTransfer(root) {
  if (!root) return;
  const panel = root.querySelector("#workflowTransferPanel");
  if (!panel) return;
  const snapshot = state.graphTransfer;
  panel.classList.toggle("hidden", !snapshot);
  if (!snapshot) {
    panel.innerHTML = "";
    updateWorkflowBoardStatusVisibility(root);
    return;
  }
  panel.className = `workflow-validation-panel workflow-transfer-panel ${snapshot.tone || "warn"}`;
  panel.innerHTML = `<strong>${escapeHTML(snapshot.title || "")}</strong><span>${escapeHTML(snapshot.body || "")}</span>`;
  updateWorkflowBoardStatusVisibility(root);
}

function clearWorkflowTransfer(root) {
  if (!state.graphTransfer) return;
  state.graphTransfer = null;
  if (root) renderWorkflowTransfer(root);
}

async function applyWorkflowTemplate(root, name, options = {}) {
  if (!name) return;
  if (!options.force && state.graph.stages.length && !confirm(t("workflow.templateApplyConfirm"))) return;
  try {
    state.graphTransfer = { tone: "loading", title: t("workflow.templateLoading"), body: t("workflow.templateLoadingHelp", { name }) };
    renderWorkflowTransfer(root);
    const template = await loadWorkflowStudioWorkflowTemplateDetail(name);
    const graph = normalizeGraph(template.graph || {});
    graph.name = uniqueWorkflowGraphName(graph.name || template.name || name);
    graph.description = graph.description || template.description || "";
    state.graph = graph;
    state.selected = state.graph.stages.length ? 0 : -1;
    state.connectSource = "";
    state.graphValidation = null;
    state.graphTransfer = { tone: "ready", title: t("workflow.templateApplied"), body: t("workflow.templateAppliedHelp", { name: template.title || template.name || name, graph: state.graph.name }) };
    if (root) {
      renderAll(root);
      window.setTimeout(() => fitCanvas(root), 40);
    }
  } catch (error) {
    state.graphTransfer = { tone: "error", title: t("workflow.templateApplyFailed"), body: t("workflow.templateApplyFailedHelp", { message: localizedWorkflowErrorMessage(error, t("workflow.templateApplyFailed")) }) };
    renderWorkflowTransfer(root);
  }
}

function updateWorkflowBoardStatusVisibility(root) {
  const wrapper = root.querySelector("#workflowBoardStatus");
  if (!wrapper) return;
  const hasVisiblePanel = !!wrapper.querySelector(".workflow-validation-panel:not(.hidden)");
  wrapper.classList.toggle("hidden", !hasVisiblePanel);
}

function workflowImportContentType(filename) {
  return /\.ya?ml$/i.test(filename || "") ? "application/x-yaml" : "application/json";
}

function workflowExportFilename(response, fallback) {
  const header = response.headers.get("Content-Disposition") || "";
  const match = header.match(/filename="?([^";]+)"?/i);
  return sanitizeWorkflowFilename(match?.[1] || fallback);
}

function sanitizeWorkflowFilename(filename) {
  return String(filename || "workflow.yaml").replace(/[\\/:*?"<>|]+/g, "-");
}

function triggerWorkflowDownload(blob, filename) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.style.display = "none";
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function normalizeWorkflowTemplateSummaries(value) {
  const list = workflowTemplateCollectionItems(value);
  return list
    .filter(item => item?.name)
    .map(item => ({ ...item, name: String(item.name).trim() }))
    .filter(item => item.name)
    .sort((left, right) => workflowTemplateRank(left) - workflowTemplateRank(right) || String(left.category || "").localeCompare(String(right.category || "")) || String(left.title || left.name).localeCompare(String(right.title || right.name)));
}

function mergeWorkflowTemplateSummaries(payloads = []) {
  const byName = new Map();
  for (const payload of payloads) {
    for (const item of normalizeWorkflowTemplateSummaries(payload)) {
      byName.set(item.name, { ...(byName.get(item.name) || {}), ...item });
    }
  }
  return normalizeWorkflowTemplateSummaries([...byName.values()]);
}

function workflowTemplateCollectionItems(value) {
  return workflowStudioCollectionItems(value, ["items", "templates", "workflow_templates"]);
}

function workflowStudioCollectionItems(value, keys = []) {
  if (Array.isArray(value)) return value;
  if (!value || typeof value !== "object") return [];
  for (const key of keys) {
    if (Array.isArray(value[key])) return value[key];
  }
  const ignored = new Set(["status", "valid", "error", "message", "count", "total", "updated_at", "diagnostics", "summary", "meta"]);
  return Object.entries(value)
    .filter(([key]) => !ignored.has(key))
    .map(([key, item]) => {
      if (!item || typeof item !== "object" || Array.isArray(item)) return null;
      return item.name ? item : { name: key, ...item };
    })
    .filter(Boolean);
}

function normalizeWorkflowGraphSummaries(value) {
  const list = Array.isArray(value)
    ? value
    : Array.isArray(value?.items)
      ? value.items
      : Array.isArray(value?.workflows)
        ? value.workflows
        : Array.isArray(value?.workflow_graphs)
          ? value.workflow_graphs
          : Array.isArray(value?.graphs)
            ? value.graphs
            : [];
  return list
    .filter(item => item && typeof item === "object" && String(item.name || "").trim())
    .map(item => ({
      ...item,
      name: String(item.name || "").trim(),
      stages: workflowGraphStageCount(item)
    }));
}

function workflowGraphStageCount(item = {}) {
  if (Number.isFinite(Number(item.stages_count))) return Number(item.stages_count);
  if (Number.isFinite(Number(item.stage_count))) return Number(item.stage_count);
  if (Array.isArray(item.stages)) return item.stages.length;
  if (Number.isFinite(Number(item.stages))) return Number(item.stages);
  return 0;
}

function uniqueWorkflowGraphName(base) {
  const fallback = slug(base) || "workflow";
  const existing = new Set((state.workflows || []).map(workflow => workflow.name).filter(Boolean));
  if (!existing.has(fallback)) return fallback;
  for (let index = 2; index < 1000; index += 1) {
    const candidate = `${fallback}-${index}`;
    if (!existing.has(candidate)) return candidate;
  }
  return `${fallback}-${Date.now()}`;
}

async function saveGraph(root) {
  const output = root.querySelector("#runOutput");
  const button = root.querySelector("#saveGraph");
  try {
    setWorkflowActionPending(button, true);
    state.graph.name = slug(root.querySelector("#graphName").value);
    state.graph.description = root.querySelector("#graphDescription").value.trim();
    if (!state.graph.name) throw new Error(t("workflow.nameRequired"));
    const endpoint = workflowGraphWriteEndpoint(state.graph.name, `/api/workflow-graphs/${encodeURIComponent(state.graph.name)}`);
    if (!endpoint) throw new Error(t("workflow.graphUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(state.graph)
    });
    state.graph = normalizeGraph(saved);
    await loadWorkflowList();
    renderAll(root);
    output.textContent = `${t("workflow.saved")} ${state.graph.name}`;
  } catch (error) {
    output.textContent = `${t("workflow.saveFailed")}: ${localizedWorkflowErrorMessage(error, t("workflow.saveFailed"))}`;
  } finally {
    setWorkflowActionPending(button, false);
  }
}

async function deleteGraph(root) {
  const output = root.querySelector("#runOutput");
  const button = root.querySelector("#deleteGraph");
  try {
    if (!state.graph.name) return;
    const current = (state.workflows || []).find(item => item.name === state.graph.name);
    if (current?.source === "builtin") throw new Error(t("workflow.builtinDeleteDenied"));
    if (!confirm(`${t("workflow.deleteConfirm")} ${state.graph.name}${t("workflow.deleteConfirmSuffix")}`)) return;
    setWorkflowActionPending(button, true);
    const endpoint = workflowGraphDeleteEndpoint(state.graph.name, `/api/workflow-graphs/${encodeURIComponent(state.graph.name)}`);
    if (!endpoint) throw new Error(t("workflow.graphUnavailable"));
    const response = await fetch(endpoint, { method: "DELETE" });
    if (!response.ok) throw new Error(await response.text());
    await loadWorkflowList();
    createPresetGraph();
    renderAll(root);
    output.textContent = t("workflow.deleted");
  } catch (error) {
    output.textContent = `${t("workflow.deleteFailed")}: ${localizedWorkflowErrorMessage(error, t("workflow.deleteFailed"))}`;
  } finally {
    setWorkflowActionPending(button, false);
  }
}

async function runGraph(root) {
  const output = root.querySelector("#runOutput");
  const button = root.querySelector("#runGraph");
  const input = root.querySelector("#runInput").value.trim();
  if (!state.graph.name || !input) return;
  setWorkflowActionPending(button, true);
  button.textContent = t("chat.workspacePreflightChecking");
  try {
    const requirement = await checkWorkspaceRequirement({ input, operation: "workflow", workflow: state.graph.name });
    if (requirement?.blocked) {
      output.classList.remove("is-running");
      output.textContent = workspaceRequirementOutput(requirement);
      return;
    }
    stopWorkflowStudioEventStream();
    resetRuntimeState(input);
    renderRuntime(root);
    scheduleWorkflowRepaint(root);
    button.textContent = t("workflow.runtimeRunning");
    output.classList.add("is-running");
    output.textContent = `${t("workflow.runtimeRunning")}...\n`;
    const accepted = await startWorkflowRun(state.graph.name, { input, approve: false, background: true });
    applyWorkflowStudioAcceptedRun(accepted, input);
    renderRuntime(root);
    renderStageForm(root);
    scheduleWorkflowRepaint(root);
    output.textContent += `${t("workflow.backgroundAccepted")} ${state.runtime.runID || ""}\n`;
    await startWorkflowStudioEventStream(root, output, {
      runID: state.runtime.runID,
      eventsURL: state.runtime.eventsURL
    });
    if (state.runtime.status === "running" && !workflowStudioShouldFollowStatus(state.runtime.workflowStatus)) {
      state.runtime.status = "success";
      state.runtime.finishedAt = Date.now();
      renderRuntime(root);
      scheduleWorkflowRepaint(root);
    }
  } catch (error) {
    const message = localizedWorkflowErrorMessage(error, t("workflow.runFailed"));
    state.runtime.status = "error";
    state.runtime.error = message;
    state.runtime.finishedAt = Date.now();
    renderRuntime(root);
    scheduleWorkflowRepaint(root);
    output.textContent += `\n${t("workflow.runFailed")}: ${message}`;
  } finally {
    setWorkflowActionPending(button, false);
    setWorkflowActionDisabled(button, state.runtime.status === "running");
    button.textContent = t("workflow.run");
    output.classList.remove("is-running");
  }
}

function setWorkflowActionPending(button, pending) {
  if (!button) return;
  if (pending) {
    if (!button.dataset.workflowPendingPreviousDisabled) {
      button.dataset.workflowPendingPreviousDisabled = button.disabled ? "true" : "false";
    }
    button.disabled = true;
    button.setAttribute("aria-disabled", "true");
    button.setAttribute("aria-busy", "true");
    return;
  }
  if (!Object.prototype.hasOwnProperty.call(button.dataset, "workflowPendingPreviousDisabled")) {
    button.setAttribute("aria-disabled", button.disabled ? "true" : "false");
    button.setAttribute("aria-busy", "false");
    return;
  }
  const wasDisabled = button.dataset.workflowPendingPreviousDisabled === "true";
  delete button.dataset.workflowPendingPreviousDisabled;
  setWorkflowActionDisabled(button, wasDisabled);
  button.setAttribute("aria-busy", "false");
}

function setWorkflowActionDisabled(button, disabled) {
  if (!button) return;
  button.disabled = Boolean(disabled);
  button.setAttribute("aria-disabled", disabled ? "true" : "false");
}

function workspaceRequirementOutput(requirement = {}) {
  const reason = localizedText(requirement.reason || t("chat.workspacePreflightReasonFallback"));
  const workspace = requirement.workspace?.display || requirement.workspace?.root || t("common.none");
  return `${t("chat.workspacePreflightTitle")}\n${t("chat.workspacePreflightBody", { reason, workspace })}\n${t("chat.workspacePreflightAction")}: #workspace`;
}

function workflowStudioEventsURL(response = {}) {
  const payload = response && typeof response === "object" ? response : {};
  return String(payload.events_url || payload.eventsURL || payload.events_path || payload.eventsPath || "").trim();
}

function applyWorkflowStudioAcceptedRun(response, input = "") {
  const payload = response && typeof response === "object" ? response : {};
  const run = payload.run && typeof payload.run === "object" ? payload.run : null;
  state.runtime.runID = String(payload.run_id || payload.runID || run?.id || state.runtime.runID || "").trim();
  state.runtime.eventsURL = workflowStudioEventsURL(payload) || state.runtime.eventsURL || (state.runtime.runID ? workflowRunEventsURL(state.runtime.runID) : "");
  state.runtime.workflowName = payload.name || run?.name || state.graph.name || "";
  state.runtime.workflowStatus = payload.status || run?.status || "running";
  state.runtime.status = workflowStatusState(state.runtime.workflowStatus);
  state.runtime.runInput = input || run?.request || state.runtime.runInput || "";
  if (run) applyWorkflowStudioRunSnapshot(run);
  saveWorkflowStudioActiveRun();
}

function applyWorkflowStudioRunSnapshot(run) {
  if (!run || typeof run !== "object") return;
  state.runtime.runID = run.id || state.runtime.runID;
  state.runtime.workflowName = run.name || state.runtime.workflowName || state.graph.name || "";
  state.runtime.workflowStatus = run.status || state.runtime.workflowStatus || "";
  state.runtime.status = workflowStatusState(run.status || state.runtime.workflowStatus);
  state.runtime.runInput = run.request || state.runtime.runInput || "";
  state.runtime.currentStage = run.next_stage || state.runtime.currentStage || "";
  state.runtime.lastEventSeq = Math.max(state.runtime.lastEventSeq || 0, workflowStudioLatestSeq(run));
  let artifactCount = Array.isArray(run.artifacts) ? run.artifacts.length : 0;
  for (const event of run.events || []) {
    ingestRuntimeEvent(event);
  }
  let outputCount = 0;
  for (const stage of run.completed_stages || []) {
    const name = stage.stage || stage.name || "";
    if (!name) continue;
    const outputs = stage.outputs || stage.result?.outputs || null;
    const stageRuntime = {
      ...(state.runtime.stageDetails[name] || {}),
      name,
      status: stage.status || "completed",
      content: stage.summary || stage.result?.output || "",
      node_type: stage.node_type || "",
      skill: stage.skill || "",
      tool: stage.tool || "",
      inputs: stage.inputs || null,
      outputs,
      attempts: stage.attempts ?? null,
      artifacts: Array.isArray(stage.artifacts) ? stage.artifacts.length : 0,
      acceptance: normalizeAcceptanceItems(stage.acceptance),
      metadata: stage.metadata || null,
      route: stage.route ?? stage.result?.route ?? outputs?.route ?? "",
      value: stage.value ?? stage.result?.value ?? outputs?.value,
      target: stage.target ?? stage.result?.target ?? outputs?.target ?? "",
      passed: stage.passed ?? stage.result?.passed ?? outputs?.passed,
      updatedAt: Date.now()
    };
    state.runtime.stageDetails[name] = stageRuntime;
    outputCount += countRuntimeOutputs([stageRuntime]);
    if (Array.isArray(stage.artifacts)) artifactCount += stage.artifacts.length;
    const route = deriveRoute(stageRuntime);
    if (route) state.runtime.route = route;
    state.runtime.lastStage = name;
  }
  state.runtime.outputs = Math.max(state.runtime.outputs || 0, outputCount);
  state.runtime.artifacts = Math.max(state.runtime.artifacts || 0, artifactCount);
  saveWorkflowStudioActiveRun();
}

async function startWorkflowStudioEventStream(root, output, options = {}) {
  const runID = options.runID || state.runtime.runID || "";
  if (!runID) return;
  stopWorkflowStudioEventStream({ keepStatus: true });
  state.runtime.streamAbortController = new AbortController();
  state.runtime.streamInFlight = true;
  state.runtime.eventStreamRunID = runID;
  state.runtime.eventsURL = options.eventsURL || state.runtime.eventsURL || "";
  let shouldReconnect = false;
  try {
    await streamWorkflowRunEvents(runID, {
      eventsURL: state.runtime.eventsURL,
      since: state.runtime.lastEventSeq || 0,
      signal: state.runtime.streamAbortController.signal
    }, async event => {
      const seq = workflowStudioEventSeq(event);
      if (seq > state.runtime.lastEventSeq) state.runtime.lastEventSeq = seq;
      if (applyWorkflowStudioEventRetryDirective(event)) {
        saveWorkflowStudioActiveRun();
        return;
      }
      if (event?.type === "workflow_run_error" || event?.is_error) {
        const errorStatus = String(event.workflow_status || state.runtime.workflowStatus || "").toLowerCase();
        state.runtime.status = "error";
        state.runtime.workflowStatus = workflowStudioShouldFollowStatus(errorStatus) ? "failed" : errorStatus || "failed";
        state.runtime.error = workflowDisplayText(event.error || event.message || event.content || t("workflow.runFailed"));
        state.runtime.finishedAt = Date.now();
        scheduleWorkflowRuntimeRefresh(root, { render: true, repaint: true, save: true });
        const line = workflowStudioEventLogLine({ type: "error", message: state.runtime.error });
        if (line) appendWorkflowStudioLog(output, line);
        return;
      }
      if (event?.type === "workflow_run_snapshot") {
        applyWorkflowStudioRunSnapshot(event);
        scheduleWorkflowRuntimeRefresh(root, { save: true });
      } else {
        const changed = ingestRuntimeEvent(event);
        const line = workflowStudioEventLogLine(event);
        if (line) appendWorkflowStudioLog(output, line);
        scheduleWorkflowRuntimeRefresh(root, { render: changed, repaint: changed, save: true });
      }
    });
    const run = await fetchWorkflowRun(runID).catch(() => null);
    if (run) applyWorkflowStudioRunSnapshot(run);
    shouldReconnect = workflowStudioShouldKeepStreamOpen(root, runID, run);
    renderRuntime(root);
    renderStageForm(root);
    scheduleWorkflowRepaint(root);
  } catch (error) {
    if (error?.name === "AbortError") return;
    shouldReconnect = workflowStudioShouldKeepStreamOpen(root, runID);
    if (!shouldReconnect) {
      state.runtime.status = "error";
      state.runtime.error = localizedWorkflowErrorMessage(error, t("workflow.runFailed"));
      state.runtime.finishedAt = Date.now();
      renderRuntime(root);
      scheduleWorkflowRepaint(root);
      if (output) output.textContent += `\n${t("workflow.runFailed")}: ${state.runtime.error}`;
    }
  } finally {
    state.runtime.streamInFlight = false;
    state.runtime.streamAbortController = null;
    state.runtime.eventStreamRunID = "";
    if (shouldReconnect) scheduleWorkflowStudioEventReconnect(root, output, { runID, eventsURL: state.runtime.eventsURL });
  }
}

function stopWorkflowStudioEventStream(options = {}) {
  if (state.runtime.streamReconnectTimer) {
    window.clearTimeout(state.runtime.streamReconnectTimer);
    state.runtime.streamReconnectTimer = null;
  }
  if (state.runtime.streamAbortController) {
    state.runtime.streamAbortController.abort();
    state.runtime.streamAbortController = null;
  }
  state.runtime.streamInFlight = false;
  state.runtime.eventStreamRunID = "";
  if (!options.keepStatus && state.runtime.status === "running") {
    state.runtime.status = "idle";
  }
}

async function restoreWorkflowStudioRuntime(root) {
  const saved = readWorkflowStudioActiveRun();
  let runID = saved?.runID || "";
  if (!runID) {
    const runs = normalizeWorkflowRunList(await fetchWorkflowRuns().catch(() => []));
    const active = runs.find(run => run.name === state.graph.name && workflowStudioShouldFollowStatus(run.status));
    runID = active?.id || "";
  }
  if (!runID) return;
  const run = await fetchWorkflowRun(runID).catch(() => null);
  if (!run || run.name !== state.graph.name) return;
  resetRuntimeState(run.request || "");
  applyWorkflowStudioRunSnapshot(run);
  renderRuntime(root);
  renderStageForm(root);
  scheduleWorkflowRepaint(root);
  const output = root.querySelector("#runOutput");
  if (output && workflowStudioShouldFollowStatus(run.status)) {
    output.textContent = `${t("workflow.restoredRun")} ${run.id}\n`;
    startWorkflowStudioEventStream(root, output, {
      runID: run.id,
      eventsURL: workflowRunEventsURL(run) || saved?.eventsURL || ""
    }).catch(error => {
      const message = localizedWorkflowErrorMessage(error, t("workflow.runFailed"));
      state.runtime.status = "error";
      state.runtime.error = message;
      renderRuntime(root);
      output.textContent += `\n${t("workflow.runFailed")}: ${message}`;
    });
  }
}

function workflowStudioEventLogLine(event) {
  if (event.type === "workflow_result") return `${t("workflow.workflowEvent")} ${event.workflow_name || state.graph.name}: ${event.workflow_status || ""}`;
  if (event.type === "task_stage") return `${t("workflow.stageEvent")}: ${event.task_stage || event.stage || ""} ${event.content || ""}`.trim();
  if (event.type === "approval") return `${t("workflow.approvalRequired")}: ${event.tool_name || ""} ${event.arguments_summary || ""}`.trim();
  if (event.type === "token_usage") return `${t("workflow.tokens")}: ${t("workflow.in")} ${event.prompt_tokens || 0}, ${t("workflow.out")} ${event.output_tokens || 0}`;
  if (event.type === "error") return `${t("workflow.runFailed")}: ${workflowDisplayText(event.message || event.content || "")}`;
  if (["text", "delta", "message_delta", "response_delta"].includes(String(event.type || ""))) return "";
  return truncateWorkflowText(event.content || "", 520);
}

function localizedWorkflowErrorMessage(error, fallback = "") {
  const text = error?.message || (typeof error === "string" ? error : String(error || "")) || fallback;
  return workflowDisplayText(text || fallback);
}

function appendWorkflowStudioLog(output, line) {
  if (!output || !line) return;
  const buffer = workflowLogBuffers.get(output) || [];
  buffer.push(line);
  workflowLogBuffers.set(output, buffer);
  if (workflowLogFlushFrame) return;
  workflowLogFlushFrame = window.requestAnimationFrame(() => {
    workflowLogFlushFrame = 0;
    for (const [node, lines] of workflowLogBuffers.entries()) {
      if (!document.body.contains(node)) continue;
      const prefix = node.textContent && !node.textContent.endsWith("\n") ? "\n" : "";
      node.textContent += `${prefix}${lines.join("\n")}`;
    }
    workflowLogBuffers.clear();
  });
}

function workflowStudioEventSeq(event) {
  const fromSeq = Number(event?.seq || 0);
  if (Number.isFinite(fromSeq) && fromSeq > 0) return fromSeq;
  const fromSSE = Number(event?.sse_id || 0);
  return Number.isFinite(fromSSE) && fromSSE > 0 ? fromSSE : 0;
}

function applyWorkflowStudioEventRetryDirective(event) {
  const retry = Number(event?.sse_retry || event?.retry || 0);
  if (Number.isFinite(retry) && retry > 0) {
    state.runtime.eventReconnectDelay = Math.min(30000, Math.max(750, retry));
  }
  return event?.type === "sse_retry";
}

function workflowStudioEventReconnectDelay() {
  const delay = Number(state.runtime?.eventReconnectDelay || defaultWorkflowStudioEventReconnectMS);
  return Number.isFinite(delay) && delay > 0 ? delay : defaultWorkflowStudioEventReconnectMS;
}

function workflowStudioShouldKeepStreamOpen(root, runID, run = null) {
  if (!root?.isConnected || state.activeRoot !== root) return false;
  if (!runID || state.runtime.runID !== runID) return false;
  const status = String(run?.status || state.runtime.workflowStatus || "").toLowerCase();
  if (workflowStudioShouldFollowStatus(status)) return true;
  return !status && state.runtime.status === "running";
}

function scheduleWorkflowStudioEventReconnect(root, output, options = {}) {
  const runID = options.runID || state.runtime.runID || "";
  if (!workflowStudioShouldKeepStreamOpen(root, runID) || state.runtime.streamReconnectTimer) return;
  const eventsURL = options.eventsURL || state.runtime.eventsURL || "";
  saveWorkflowStudioActiveRun();
  state.runtime.streamReconnectTimer = window.setTimeout(() => {
    state.runtime.streamReconnectTimer = null;
    if (!workflowStudioShouldKeepStreamOpen(root, runID)) return;
    const targetOutput = output?.isConnected ? output : root.querySelector("#runOutput");
    startWorkflowStudioEventStream(root, targetOutput, { runID, eventsURL }).catch(error => {
      if (error?.name === "AbortError") return;
      state.runtime.status = "error";
      state.runtime.error = localizedWorkflowErrorMessage(error, t("workflow.runFailed"));
      state.runtime.finishedAt = Date.now();
      renderRuntime(root);
      scheduleWorkflowRepaint(root);
      if (targetOutput) targetOutput.textContent += `\n${t("workflow.runFailed")}: ${state.runtime.error}`;
    });
  }, workflowStudioEventReconnectDelay());
}

function saveWorkflowStudioActiveRun() {
  try {
    if (!state.runtime.runID) return;
    localStorage.setItem(workflowStudioRunKey, JSON.stringify({
      runID: state.runtime.runID,
      workflowName: state.runtime.workflowName || state.graph.name || "",
      eventsURL: state.runtime.eventsURL || "",
      lastEventSeq: state.runtime.lastEventSeq || 0,
      updatedAt: Date.now()
    }));
  } catch {
    // Ignore storage failures.
  }
}

function readWorkflowStudioActiveRun() {
  try {
    return JSON.parse(localStorage.getItem(workflowStudioRunKey) || "null");
  } catch {
    return null;
  }
}

function workflowStudioLatestSeq(run) {
  return (run?.events || []).reduce((max, event) => {
    const seq = Number(event?.seq || event?.sse_id || 0);
    return Number.isFinite(seq) && seq > max ? seq : max;
  }, 0);
}

function workflowStudioShouldFollowStatus(status) {
  const value = String(status || "").toLowerCase();
  return ["running", "cancelling", "awaiting_approval", "awaiting_tool_approval", "awaiting_input", "awaiting_sub_workflow"].includes(value);
}

function normalizeWorkflowRunList(value) {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.runs)) return value.runs;
  if (Array.isArray(value?.workflow_runs)) return value.workflow_runs;
  return [];
}

function normalizeGraph(doc) {
  const graph = { name: doc.name || "", description: doc.description || "", stages: doc.stages || [] };
  graph.stages.forEach((stage, index) => {
    stage.node_type = normalizedNodeType(stage);
    stage.params = stage.params || {};
    stage.routes = stage.routes || {};
    stage.cases = stage.cases || {};
    stage.input = stage.input || {};
    stage.outputs = stage.outputs || {};
    stage.artifacts = normalizeArtifacts(stage.artifacts);
    stage.acceptance_criteria = normalizeAcceptanceCriteria(stage.acceptance_criteria || stage.acceptance);
    ensurePosition(stage, index);
  });
  return graph;
}

function normalizedNodeType(stage) {
  const raw = String(stage?.node_type || "").trim().toLowerCase();
  if (raw) return raw;
  return stage?.name === "start" ? "start" : stage?.name === "end" ? "end" : "agent";
}

function ensurePosition(stage, index = 0) {
  if (!stage.position) stage.position = {};
  if (!Number.isFinite(stage.position.x)) stage.position.x = 80 + index * 280;
  if (!Number.isFinite(stage.position.y)) stage.position.y = 150;
}

function nodeIcon(type) {
  const icons = {
    start: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14l11-7-11-7Z"/></svg>`,
    agent: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 4h8v3h3v8a5 5 0 0 1-5 5h-4a5 5 0 0 1-5-5V7h3V4Zm2 2v1h4V6h-4Zm-2 6a2 2 0 1 0 4 0 2 2 0 0 0-4 0Zm6 0a2 2 0 1 0 4 0 2 2 0 0 0-4 0Zm-4 5h4v-2h-4v2Z"/></svg>`,
    skill: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3l1.4 4.2L18 5.8l-1.4 4.6L21 12l-4.4 1.6L18 18.2l-4.6-1.4L12 21l-1.4-4.2L6 18.2l1.4-4.6L3 12l4.4-1.6L6 5.8l4.6 1.4L12 3Z"/></svg>`,
    tool: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 7.8a6.2 6.2 0 0 1-7.7 7.5l-5.8 5.8a2.1 2.1 0 0 1-3-3l5.8-5.8A6.2 6.2 0 0 1 17.8 4l-3.3 3.3 2.2 2.2L21 7.8Z"/></svg>`,
    team: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 11a3 3 0 1 1 .01 0Zm8 0a3 3 0 1 1 .01 0ZM4 20a4 4 0 0 1 8 0H4Zm8 0a4 4 0 0 1 8 0h-8Z"/></svg>`,
    condition: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 4h10l4 8-4 8H7l-4-8 4-8Zm1.2 2L5.3 12l2.9 6h7.6l2.9-6-2.9-6H8.2Z"/></svg>`,
    switch: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5h5v2H7v10h3v2H5V5Zm9 0h5v6h-5V9h3V7h-3V5Zm0 8h5v6h-5v-2h3v-2h-3v-2Zm-4-2h4v2h-4v-2Z"/></svg>`,
    router: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h6v6H4V5Zm10 0h6v6h-6V5ZM4 15h6v4H4v-4Zm4-5h2v2h5v3h-2v-1H8v-4Z"/></svg>`,
    policy_guard: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 20 6v6c0 4.6-3.2 7.8-8 9-4.8-1.2-8-4.4-8-9V6l8-3Zm0 2.2L6 7.4V12c0 3.4 2.2 5.8 6 6.9 3.8-1.1 6-3.5 6-6.9V7.4l-6-2.2Zm-1 8.6 4.6-4.6L17 10.6l-6 6-3.2-3.2 1.4-1.4 1.8 1.8Z"/></svg>`,
    quality_gate: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 20 6v6c0 4.6-3.2 7.8-8 9-4.8-1.2-8-4.4-8-9V6l8-3Zm0 2.2L6 7.4V12c0 3.4 2.2 5.8 6 6.9 3.8-1.1 6-3.5 6-6.9V7.4l-6-2.2Zm-3 7.1 1.5-1.4 1.1 1.2L14.9 9l1.4 1.5-4.7 4.5L9 12.3Z"/></svg>`,
    parallel: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5h4v4H5V5Zm10 0h4v4h-4V5ZM5 15h4v4H5v-4Zm10 0h4v4h-4v-4Zm-4-8h2v4h4v2h-4v4h-2v-4H7v-2h4V7Z"/></svg>`,
    join: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h4v4H7v3h4V8h2v3h4V8h-2V4h4v4h-2v5h-4v3h2v4H9v-4h2v-3H7V8H5V4Z"/></svg>`,
    input_gate: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h16v14H4V5Zm2 2v10h12V7H6Zm2 2h8v2H8V9Zm0 4h5v2H8v-2Z"/></svg>`,
    for_each: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 4h7v2H8v12h5v2H6V4Zm8 2 5 6-5 6-1.5-1.3 3.3-3.7H10v-2h5.8l-3.3-3.7L14 6Z"/></svg>`,
    loop: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 7h8.6L13 4.4 14.4 3 19.4 8l-5 5L13 11.6 15.6 9H7a3 3 0 0 0 0 6h3v2H7A5 5 0 0 1 7 7Zm10 10H8.4L11 19.6 9.6 21l-5-5 5-5L11 12.4 8.4 15H17a3 3 0 0 0 0-6h-3V7h3a5 5 0 0 1 0 10Z"/></svg>`,
    sub_workflow: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h7v6H4V5Zm2 2v2h3V7H6Zm7-2h7v6h-7V5Zm2 2v2h3V7h-3ZM4 15h7v6H4v-6Zm2 2v2h3v-2H6Zm8-3h2v3h3v2h-5v-5ZM9 11h2v3H8v1H6v-3h3v-1Zm3-3h2v2h-2V8Z"/></svg>`,
    checkpoint: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 2a10 10 0 1 1 0 20 10 10 0 0 1 0-20Zm1 5h-2v6h6v-2h-4V7Z"/></svg>`,
    custom: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8.8 7.2 4 12l4.8 4.8 1.4-1.4L6.8 12l3.4-3.4-1.4-1.4Zm6.4 0-1.4 1.4 3.4 3.4-3.4 3.4 1.4 1.4L20 12l-4.8-4.8Z"/></svg>`,
    end: `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h11.2l.6 3H20v9h-8.2l-.6-3H7v7H5V4Zm2 2v5h5.8l.6 3H18V9h-2.8l-.6-3H7Z"/></svg>`
  };
  return icons[type] || icons.custom;
}

function nodeMetaLines(stage, nodeType) {
  if (controlTypes.has(nodeType)) {
    if (nodeType === "condition") return [workflowDisplayValue(stage.condition || t("workflow.condition")), routeSummary(stage.routes)];
    if (nodeType === "switch" || nodeType === "router") return [workflowDisplayValue(stage.switch_on || t("workflow.switchOn")), routeSummary(stage.cases)];
    if (nodeType === "policy_guard") return [workflowDisplayValue(stage.params?.rule || stage.policy || t("workflow.policy")), routeSummary(stage.routes)];
    if (nodeType === "quality_gate" || nodeType === "quality_guard") return [nodeDisplayType(nodeType), routeSummary(stage.routes)];
    if (nodeType === "parallel") return [t("workflow.node.parallel"), workflowDisplayList(stage.next || []) || t("workflow.nextStages")];
    if (nodeType === "join") return [t("workflow.node.join"), workflowDisplayValue(stage.params?.wait_for || t("workflow.nextStages"))];
    if (nodeType === "input_gate") return [t("workflow.node.input_gate"), inputGateFieldSummary(stage.params)];
    if (nodeType === "for_each") return [workflowDisplayValue(stage.params?.stage || t("workflow.node.for_each")), workflowDisplayValue(stage.params?.items || stage.params?.items_ref || t("workflow.nextStages"))];
    if (nodeType === "loop") return [workflowDisplayValue(stage.params?.stage || t("workflow.node.loop")), workflowDisplayValue(stage.params?.until || stage.params?.max_iterations || t("workflow.condition"))];
    if (nodeType === "sub_workflow") return [workflowDisplayValue(stage.params?.workflow || t("workflow.node.sub_workflow")), workflowDisplayValue(stage.params?.request || t("workflow.inputMap"))];
    if (nodeType === "checkpoint") return [t("workflow.node.checkpoint"), workflowDisplayValue(stage.params?.prompt || t("workflow.approvalGate"))];
  }
  if (nodeType === "team") {
    const template = stage.params?.team || stage.params?.template || "";
    return [workflowDisplayValue(template ? teamTemplateTitle(template) : t("workflow.node.team")), isTruthyParam(stage.params?.execute) ? t("workflow.teamExecuteEnabled") : t("workflow.teamTemplateContextOnlyCard")];
  }
  if (visualTypes.has(nodeType)) {
    return [nodeType === "start" ? t("workflow.node.startHelp") : t("workflow.node.endHelp"), workflowDisplayList(stage.next || []) || "-"];
  }
  return [workflowDisplayValue(stage.agent || t("workflow.noAgent")), workflowDisplayValue(stage.skill || stage.tool || t("workflow.noSkill"))];
}

function inputGateFieldSummary(params = {}) {
  if (params.fields_json) {
    try {
      const parsed = JSON.parse(params.fields_json);
      const fields = Array.isArray(parsed) ? parsed : parsed.fields;
      if (Array.isArray(fields) && fields.length) {
        return t("workflow.richInputFormCount", { count: fields.length });
      }
    } catch {
      return t("workflow.richInputForm");
    }
    return t("workflow.richInputForm");
  }
  return params.fields || t("workflow.inputMap");
}

function routeSummary(values) {
  const entries = Object.entries(values || {});
  if (!entries.length) return t("workflow.routes");
  return entries.map(([key, value]) => `${workflowDisplayValue(key)} -> ${workflowDisplayValue(value)}`).join(", ");
}

function workflowDisplayList(values) {
  return (Array.isArray(values) ? values : String(values || "").split(","))
    .map(value => workflowDisplayValue(value))
    .filter(Boolean)
    .join(", ");
}

function workflowDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (workflowLooksTechnical(text)) return text;
  return localizedText(text);
}

function workflowDisplayText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const validation = localizeWorkflowValidationMessage(text);
  if (validation && validation !== text) return validation;
  return workflowPlainDisplayText(text);
}

function workflowPlainDisplayText(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const translated = localizedText(text);
  if (translated !== text) return translated;
  if (workflowLooksTechnical(text)) return text;
  const generated = workflowGeneratedText(text);
  return generated || translated;
}

function workflowLooksTechnical(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return true;
  if (text.includes("\\") || text.includes("://")) return true;
  if (text.includes("/") && !/\s/.test(text)) return true;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(text)) return true;
  return false;
}

function workflowGeneratedText(text) {
  if (currentLanguage() !== "zh") return "";
  const normalized = String(text || "").trim().toLowerCase();
  if (!normalized || !/[a-z]/.test(normalized)) return "";
  const generated = workflowGeneratedZhText(normalized);
  if (generated) return generated;
  if (normalized.includes("quality gate")) return "质量门禁会检查本阶段结果是否达到继续执行的标准。";
  if (normalized.includes("acceptance")) return "验收信息用于说明本阶段是否满足用户目标。";
  if (normalized.includes("artifact")) return "产物会作为运行证据保存，方便后续查看和复用。";
  if (normalized.includes("output")) return "输出字段会成为后续节点可引用的结果。";
  if (normalized.includes("input")) return "输入字段用于收集运行该节点需要的信息。";
  if (normalized.includes("approval") || normalized.includes("human")) return "该节点可能需要人工确认后才能继续。";
  if (normalized.includes("route") || normalized.includes("branch") || normalized.includes("condition")) return "该节点会根据条件决定下一步走向。";
  if (normalized.includes("policy")) return "策略规则用于在运行前检查是否满足安全或质量要求。";
  if (normalized.includes("team")) return "团队节点会组织多个角色协作完成同一阶段。";
  if (normalized.includes("tool")) return "工具节点会调用外部能力或本地工具完成具体操作。";
  if (normalized.includes("workflow")) return "工作流节点用于连接或复用一段流程。";
  if (normalized.includes("expression") || normalized.includes("reference")) return "表达式用于引用上游结果或计算分支条件。";
  return "";
}

function nodeInitial(type) {
  const labels = { start: "IN", agent: "A", skill: "K", tool: "T", for_each: "FE", loop: "LP", sub_workflow: "SW", custom: "C", end: "OUT" };
  return labels[type] || "N";
}

function workflowGeneratedZhText(normalized) {
  if (normalized.includes("quality gate")) return "质量门禁会检查本阶段结果是否达到继续执行的标准。";
  if (normalized.includes("acceptance")) return "验收信息用于说明本阶段是否满足用户目标。";
  if (normalized.includes("artifact")) return "产物会作为运行证据保存，方便后续查看和复用。";
  if (normalized.includes("output")) return "输出字段会成为后续节点可引用的结果。";
  if (normalized.includes("input")) return "输入字段用于收集运行该节点需要的信息。";
  if (normalized.includes("approval") || normalized.includes("human")) return "该节点可能需要人工确认后才能继续。";
  if (normalized.includes("route") || normalized.includes("branch") || normalized.includes("condition")) return "该节点会根据条件决定下一步走向。";
  if (normalized.includes("policy")) return "策略规则用于在运行前检查是否满足安全或质量要求。";
  if (normalized.includes("team")) return "团队节点会组织多个角色协作完成同一阶段。";
  if (normalized.includes("tool")) return "工具节点会调用外部能力或本地工具完成具体操作。";
  if (normalized.includes("workflow")) return "工作流节点用于连接或复用一段流程。";
  if (normalized.includes("expression") || normalized.includes("reference")) return "表达式用于引用上游结果或计算分支条件。";
  return "";
}

function nodeDisplayType(type) {
  const option = nodeTypeOption(type);
  const labels = { start: t("workflow.nodeDisplay.start"), end: t("workflow.nodeDisplay.end") };
  return labels[type] || nodeTypeLabel(option);
}

function graphPoint(root, event) {
  const canvas = root.querySelector("#canvas");
  const rect = canvas.getBoundingClientRect();
  return {
    x: (event.clientX - rect.left + canvas.scrollLeft) / state.zoom,
    y: (event.clientY - rect.top + canvas.scrollTop) / state.zoom
  };
}

function bindWorkflowShortcuts() {
  if (state.shortcutsBound) return;
  state.shortcutsBound = true;
  window.addEventListener("keydown", event => {
    const root = currentWorkflowRoot();
    if (!root || event.defaultPrevented) return;
    const target = event.target instanceof Element ? event.target : null;
    const isEditing = !!target?.closest("input, textarea, select, [contenteditable='true']");
    const isWorkflowCanvasFocus = target === document.body || target?.closest("#canvas, .flow-node");
    if (!isEditing && event.key === "Escape") {
      if (cancelWorkflowCanvasInteraction(root)) event.preventDefault();
      return;
    }
    if (!isEditing && (event.key === "Delete" || event.key === "Backspace")) {
      if (!isWorkflowCanvasFocus) return;
      if (!selectedStage()) return;
      event.preventDefault();
      removeSelectedStage();
      renderAll(root);
      return;
    }
    if (!event.ctrlKey && !event.metaKey) return;
    const key = event.key;
    if (key === "+" || key === "=") {
      event.preventDefault();
      setCanvasZoom(root, state.zoom + zoomStep());
      return;
    }
    if (key === "-" || key === "_") {
      event.preventDefault();
      setCanvasZoom(root, state.zoom - zoomStep());
      return;
    }
    if (key === "0") {
      event.preventDefault();
      resetCanvasZoom(root);
    }
  });
}

function currentWorkflowRoot() {
  const root = state.activeRoot;
  if (!root || !document.body.contains(root) || !root.querySelector("#canvas")) return null;
  return root;
}

function zoomStep() {
  return state.zoom < 0.8 ? 0.05 : 0.1;
}

function zoomCanvasAt(root, value, anchor) {
  setCanvasZoom(root, value, anchor);
}

function resetCanvasZoom(root) {
  setCanvasZoom(root, 1);
}

function canvasCenterAnchor(root) {
  const canvas = root.querySelector("#canvas");
  const rect = canvas.getBoundingClientRect();
  return { clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2 };
}

function setCanvasZoom(root, value, anchor = canvasCenterAnchor(root)) {
  const canvas = root.querySelector("#canvas");
  const rect = canvas.getBoundingClientRect();
  const graphAnchor = graphPoint(root, anchor);
  const anchorOffsetX = anchor.clientX - rect.left;
  const anchorOffsetY = anchor.clientY - rect.top;
  const nextZoom = Math.max(0.35, Math.min(1.8, Math.round(value * 100) / 100));
  if (nextZoom === state.zoom) return;
  markCanvasZooming(canvas);
  state.zoom = nextZoom;
  applyCanvasGeometry(root);
  updateZoomLabel(root);
  scheduleWorkflowEdgeRepaint(root);
  canvas.scrollLeft = Math.max(0, graphAnchor.x * state.zoom - anchorOffsetX);
  canvas.scrollTop = Math.max(0, graphAnchor.y * state.zoom - anchorOffsetY);
}

function markCanvasZooming(canvas) {
  if (!canvas) return;
  canvas.classList.add("is-zooming");
  if (workflowZoomEndTimer) window.clearTimeout(workflowZoomEndTimer);
  workflowZoomEndTimer = window.setTimeout(() => {
    canvas.classList.remove("is-zooming");
    workflowZoomEndTimer = 0;
  }, 180);
}

function autoLayoutGraph(root) {
  const stages = state.graph.stages || [];
  if (!stages.length) return;
  const layout = workflowLayoutPlan(root, stages);
  state.canvas.width = layout.layoutWidth;
  state.canvas.height = layout.layoutHeight;
  for (const column of layout.columns) {
    let y = layout.originY + (layout.maxColumnHeight - column.height) / 2;
    for (const item of column.items) {
      const width = workflowNodeWidth(normalizedNodeType(item.stage));
      item.stage.position = {
        x: Math.round(column.x + (column.width - width) / 2),
        y: Math.round(y)
      };
      y += item.height + layout.yGap;
    }
  }
  keepGraphInsideCanvas();
  applyCanvasGeometry(root);
  renderAll(root);
  fitCanvas(root);
}

function workflowLayoutPlan(root, stages) {
  const canvas = root.querySelector("#canvas");
  const graph = workflowLayoutGraph(stages);
  const depth = workflowLayoutDepths(stages, graph);
  const columns = workflowLayoutColumns(stages, depth, graph);
  workflowRefineColumnOrder(columns, graph);

  const xGap = 178;
  const yGap = 104;
  const layoutColumns = columns.map(column => {
    const items = column.stages.map(stage => ({
      stage,
      width: workflowNodeWidth(normalizedNodeType(stage)),
      height: workflowNodeHeight(normalizedNodeType(stage))
    }));
    return {
      index: column.index,
      items,
      width: Math.max(...items.map(item => item.width)),
      height: items.reduce((total, item) => total + item.height, 0) + Math.max(0, items.length - 1) * yGap,
      x: 0
    };
  });

  const graphWidth = layoutColumns.reduce((total, column) => total + column.width, 0) + Math.max(0, layoutColumns.length - 1) * xGap;
  const maxColumnHeight = Math.max(...layoutColumns.map(column => column.height), workflowNodeMetrics.compactHeight);
  const viewportWidth = Math.max(320, Math.ceil((canvas?.clientWidth || workflowNodeMetrics.minCanvasWidth) / Math.max(state.zoom, 0.1)));
  const viewportHeight = Math.max(260, Math.ceil((canvas?.clientHeight || workflowNodeMetrics.minCanvasHeight) / Math.max(state.zoom, 0.1)));
  const layoutWidth = Math.max(viewportWidth, graphWidth + workflowNodeMetrics.leftPadding * 2);
  const layoutHeight = Math.max(viewportHeight, maxColumnHeight + workflowNodeMetrics.topPadding * 2);
  const originX = Math.max(workflowNodeMetrics.leftPadding, Math.round((layoutWidth - graphWidth) / 2));
  const originY = Math.max(workflowNodeMetrics.topPadding, Math.round((layoutHeight - maxColumnHeight) / 2));

  let x = originX;
  for (const column of layoutColumns) {
    column.x = x;
    x += column.width + xGap;
  }
  return { columns: layoutColumns, originX, originY, xGap, yGap, graphWidth, maxColumnHeight, layoutWidth, layoutHeight };
}

function workflowLayoutGraph(stages) {
  const byName = new Map(stages.map(stage => [stage.name, stage]));
  const originalIndex = new Map(stages.map((stage, index) => [stage.name, index]));
  const incoming = new Map(stages.map(stage => [stage.name, []]));
  const outgoing = new Map(stages.map(stage => [stage.name, []]));
  for (const stage of stages) {
    const links = workflowOutgoingLinks(stage).filter(link => byName.has(link.target));
    outgoing.set(stage.name, links);
    for (const link of links) {
      incoming.get(link.target)?.push({
        source: stage.name,
        target: link.target,
        order: Number.isFinite(link.order) ? link.order : 0,
        kind: link.entries?.[0]?.kind || "next",
        link
      });
    }
  }
  return { byName, originalIndex, incoming, outgoing };
}

function workflowLayoutDepths(stages, graph) {
  const starts = stages.filter(stage => normalizedNodeType(stage) === "start" || !graph.incoming.get(stage.name)?.length);
  const depth = new Map();
  const visit = (stage, nextDepth = 0, seen = new Set()) => {
    if (!stage?.name || seen.has(stage.name)) return;
    const currentDepth = depth.get(stage.name);
    if (currentDepth != null && currentDepth >= nextDepth) return;
    depth.set(stage.name, nextDepth);
    const nextSeen = new Set(seen);
    nextSeen.add(stage.name);
    for (const link of graph.outgoing.get(stage.name) || []) visit(graph.byName.get(link.target), nextDepth + 1, nextSeen);
  };
  (starts.length ? starts : stages.slice(0, 1)).forEach(stage => visit(stage, 0));
  let fallbackDepth = Math.max(0, ...depth.values());
  stages.forEach(stage => {
    if (depth.has(stage.name)) return;
    fallbackDepth += 1;
    depth.set(stage.name, fallbackDepth);
  });
  return depth;
}

function workflowLayoutColumns(stages, depth, graph) {
  const buckets = new Map();
  for (const stage of stages) {
    const column = depth.get(stage.name) || 0;
    if (!buckets.has(column)) buckets.set(column, []);
    buckets.get(column).push(stage);
  }
  return [...buckets.keys()].sort((a, b) => a - b).map(index => {
    const stagesInColumn = buckets.get(index)
      .slice()
      .sort((left, right) => workflowLayoutCompare(left, right, graph, new Map(), "initial"));
    return { index, stages: stagesInColumn };
  });
}

function workflowRefineColumnOrder(columns, graph) {
  if (columns.length <= 1) return;
  for (let pass = 0; pass < 6; pass++) {
    let rows = workflowLayoutRowMap(columns);
    for (let index = 1; index < columns.length; index++) {
      columns[index].stages.sort((left, right) => workflowLayoutCompare(left, right, graph, rows, "incoming"));
      rows = workflowLayoutRowMap(columns);
    }
    for (let index = columns.length - 2; index >= 0; index--) {
      columns[index].stages.sort((left, right) => workflowLayoutCompare(left, right, graph, rows, "outgoing"));
      rows = workflowLayoutRowMap(columns);
    }
  }
}

function workflowLayoutRowMap(columns) {
  const rows = new Map();
  columns.forEach((column, columnIndex) => {
    column.stages.forEach((stage, rowIndex) => {
      rows.set(stage.name, { column: columnIndex, row: rowIndex });
    });
  });
  return rows;
}

function workflowLayoutCompare(left, right, graph, rows, mode) {
  const priority = workflowLayoutNodePriority(left) - workflowLayoutNodePriority(right);
  if (priority) return priority;
  const leftRank = workflowLayoutRank(left, graph, rows, mode);
  const rightRank = workflowLayoutRank(right, graph, rows, mode);
  if (leftRank !== rightRank) return leftRank - rightRank;
  return (graph.originalIndex.get(left.name) || 0) - (graph.originalIndex.get(right.name) || 0);
}

function workflowLayoutNodePriority(stage) {
  const type = normalizedNodeType(stage);
  if (type === "start") return -10;
  if (type === "end") return 10;
  if (type === "join") return 4;
  return 0;
}

function workflowLayoutRank(stage, graph, rows, mode) {
  if (mode === "incoming") {
    const rank = workflowLayoutNeighborRank(graph.incoming.get(stage.name) || [], rows, "source", "order");
    if (Number.isFinite(rank)) return rank;
  }
  if (mode === "outgoing") {
    const rank = workflowLayoutNeighborRank(graph.outgoing.get(stage.name) || [], rows, "target", "order");
    if (Number.isFinite(rank)) return rank;
  }
  const incomingRank = workflowLayoutNeighborRank(graph.incoming.get(stage.name) || [], workflowLayoutOriginalRows(graph), "source", "order");
  if (Number.isFinite(incomingRank)) return incomingRank;
  return graph.originalIndex.get(stage.name) || 0;
}

function workflowLayoutNeighborRank(links, rows, nameKey, orderKey) {
  const values = links
    .map(link => {
      const name = link[nameKey];
      const row = rows.get(name);
      if (!row) return null;
      const order = Number.isFinite(link[orderKey]) ? link[orderKey] : 0;
      return row.row * 1000 + order;
    })
    .filter(value => Number.isFinite(value));
  if (!values.length) return Number.POSITIVE_INFINITY;
  return values.reduce((total, value) => total + value, 0) / values.length;
}

function workflowLayoutOriginalRows(graph) {
  const rows = new Map();
  for (const [name, index] of graph.originalIndex.entries()) {
    rows.set(name, { column: 0, row: index });
  }
  return rows;
}

function fitCanvas(root) {
  const canvas = root.querySelector("#canvas");
  applyCanvasGeometry(root);
  const bounds = graphBounds();
  const availableWidth = Math.max(320, canvas.clientWidth - 48);
  const availableHeight = Math.max(260, canvas.clientHeight - 48);
  const next = Math.min(1.2, availableWidth / bounds.width, availableHeight / bounds.height);
  setCanvasZoom(root, next);
  applyCanvasGeometry(root);
  centerGraphInViewport(root);
}

function centerGraphInViewport(root) {
  const canvas = root.querySelector("#canvas");
  const space = root.querySelector("#canvasSpace");
  if (!canvas) return;
  const bounds = graphBounds();
  const graphCenterX = ((bounds.left + bounds.right) / 2) * state.zoom;
  const graphCenterY = ((bounds.top + bounds.bottom) / 2) * state.zoom;
  const maxLeft = Math.max(0, (space?.scrollWidth || 0) - canvas.clientWidth);
  const maxTop = Math.max(0, (space?.scrollHeight || 0) - canvas.clientHeight);
  canvas.scrollLeft = clamp(graphCenterX - canvas.clientWidth / 2, 0, maxLeft);
  canvas.scrollTop = clamp(graphCenterY - canvas.clientHeight / 2, 0, maxTop);
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

function updateZoomLabel(root) {
  const label = root.querySelector("#zoomLabel");
  if (label) label.textContent = `${Math.round(state.zoom * 100)}%`;
}

function applyCanvasGeometry(root) {
  keepGraphInsideCanvas();
  const canvas = root.querySelector("#canvas");
  const space = root.querySelector("#canvasSpace");
  const surface = root.querySelector("#canvasSurface");
  if (!space || !surface) return;
  const bounds = graphBounds();
  const viewportWidth = Math.ceil((canvas?.clientWidth || workflowNodeMetrics.minCanvasWidth) / Math.max(state.zoom, 0.1));
  const viewportHeight = Math.ceil((canvas?.clientHeight || workflowNodeMetrics.minCanvasHeight) / Math.max(state.zoom, 0.1));
  const horizontalPad = Math.max(300, Math.ceil(viewportWidth * 0.28));
  const verticalPad = Math.max(260, Math.ceil(viewportHeight * 0.30));
  state.canvas.width = Math.max(
    workflowNodeMetrics.minCanvasWidth,
    viewportWidth,
    Math.ceil(bounds.right + horizontalPad),
    Math.ceil(bounds.width + workflowNodeMetrics.leftPadding * 2)
  );
  state.canvas.height = Math.max(
    workflowNodeMetrics.minCanvasHeight,
    viewportHeight,
    Math.ceil(bounds.bottom + verticalPad),
    Math.ceil(bounds.height + workflowNodeMetrics.topPadding * 2)
  );
  space.style.width = `${Math.ceil(state.canvas.width * state.zoom)}px`;
  space.style.height = `${Math.ceil(state.canvas.height * state.zoom)}px`;
  surface.style.width = `${state.canvas.width}px`;
  surface.style.height = `${state.canvas.height}px`;
  surface.style.transform = `translateZ(0) scale(${state.zoom})`;
}

function graphBounds() {
  if (!state.graph.stages.length) return { left: 0, top: 0, right: workflowNodeMetrics.minCanvasWidth, bottom: workflowNodeMetrics.minCanvasHeight, width: workflowNodeMetrics.minCanvasWidth, height: workflowNodeMetrics.minCanvasHeight };
  let left = Infinity;
  let top = Infinity;
  let right = 0;
  let bottom = 0;
  for (const [index, stage] of state.graph.stages.entries()) {
    ensurePosition(stage, index);
    const type = normalizedNodeType(stage);
    const width = workflowNodeWidth(type);
    const height = workflowNodeHeight(type);
    left = Math.min(left, stage.position.x);
    top = Math.min(top, stage.position.y);
    right = Math.max(right, stage.position.x + width);
    bottom = Math.max(bottom, stage.position.y + height);
  }
  const width = Math.max(420, right - left + 120);
  const height = Math.max(300, bottom - top + 120);
  return { left, top, right, bottom, width, height };
}

function keepGraphInsideCanvas() {
  const stages = state.graph.stages || [];
  if (!stages.length) return;
  let minX = Infinity;
  let minY = Infinity;
  for (const [index, stage] of stages.entries()) {
    ensurePosition(stage, index);
    minX = Math.min(minX, stage.position.x);
    minY = Math.min(minY, stage.position.y);
  }
  const dx = minX < workflowNodeMetrics.leftPadding ? workflowNodeMetrics.leftPadding - minX : 0;
  const dy = minY < workflowNodeMetrics.topPadding ? workflowNodeMetrics.topPadding - minY : 0;
  if (!dx && !dy) return;
  stages.forEach(stage => {
    stage.position = {
      ...stage.position,
      x: Math.round(stage.position.x + dx),
      y: Math.round(stage.position.y + dy)
    };
  });
}

function workflowNodeWidth(type) {
  return type === "start" || type === "end" ? workflowNodeMetrics.compactWidth : workflowNodeMetrics.regularWidth;
}

function workflowNodeHeight(type) {
  return type === "start" || type === "end" ? workflowNodeMetrics.compactHeight : workflowNodeMetrics.regularHeight;
}

function selectedStage() {
  return state.graph.stages[state.selected];
}

function fillSelect(select, values) {
  const previous = select.value;
  select.innerHTML = "";
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = "-";
  select.appendChild(empty);
  for (const value of values || []) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = value;
    select.appendChild(option);
  }
  if ((values || []).includes(previous)) select.value = previous;
}

function fillTeamTemplateSelect(select) {
  if (!select) return;
  const previous = select.value;
  select.innerHTML = "";
  const empty = document.createElement("option");
  empty.value = "";
  empty.textContent = "-";
  select.appendChild(empty);
  for (const template of teamTemplateOptions()) {
    const option = document.createElement("option");
    option.value = template.name;
    option.textContent = teamTemplateOptionLabel(template);
    select.appendChild(option);
  }
  if (previous) ensureSelectOption(select, previous, teamTemplateTitle(previous));
  select.value = previous;
}

function ensureSelectOption(select, value, label = value) {
  if (!select || !value || [...select.options].some(option => option.value === value)) return;
  const option = document.createElement("option");
  option.value = value;
  option.textContent = label;
  select.appendChild(option);
}

function teamTemplateOptions() {
  return (state.options?.team_templates || [])
    .filter(template => template?.name)
    .map(template => ({ ...template, name: String(template.name).trim() }))
    .filter(template => template.name);
}

function teamTemplateSummary(name) {
  const key = String(name || "").trim();
  if (!key) return null;
  return teamTemplateOptions().find(template => template.name === key) || state.teamTemplateDetails[key] || null;
}

function teamTemplateTitle(name) {
  const template = teamTemplateSummary(name);
  return template?.title || template?.name || String(name || "");
}

function teamTemplateOptionLabel(template) {
  const title = template?.title || template?.name || "";
  const source = template?.source === "custom" || template?.custom ? t("workflow.teamTemplateCustom") : t("workflow.teamTemplateBuiltIn");
  const roles = Number(template?.roles || template?.role_templates?.length || 0);
  return `${title} / ${source} / ${roles} ${t("workflow.teamTemplateRoleCount")}`;
}

function genericStageParams(stage, nodeType) {
  const params = { ...(stage.params || {}) };
  for (const key of dedicatedParamKeys(nodeType)) {
    delete params[key];
  }
  return params;
}

function dedicatedParamKeys(nodeType) {
  const keys = new Set();
  if (nodeType === "team") ["team", "template", "execute", "approval_preset", "review_preset", "quorum_preset", "preset"].forEach(key => keys.add(key));
  if (nodeType === "input_gate") keys.add("fields_json");
  if (nodeType === "policy_guard") keys.add("rule");
  if (nodeType === "for_each") ["items", "items_ref", "stage"].forEach(key => keys.add(key));
  if (nodeType === "loop") ["stage", "until", "max_iterations"].forEach(key => keys.add(key));
  if (nodeType === "sub_workflow") ["workflow", "request"].forEach(key => keys.add(key));
  if (nodeType === "join") keys.add("wait_for");
  if (nodeType === "checkpoint") keys.add("prompt");
  return keys;
}

function applyDedicatedParamsFromForm(root, stage, nodeType) {
  stage.params = stage.params || {};
  setParamValue(stage.params, "team", nodeType === "team" ? root.querySelector("#stageParamTeam").value : "");
  if (nodeType === "team") delete stage.params.template;
  setParamValue(stage.params, "approval_preset", nodeType === "team" ? root.querySelector("#stageTeamQuorumPreset").value : "");
  if (nodeType === "team") {
    delete stage.params.review_preset;
    delete stage.params.quorum_preset;
    delete stage.params.preset;
  }
  setParamValue(stage.params, "workflow", nodeType === "sub_workflow" ? root.querySelector("#stageParamWorkflow").value : "");
  setParamValue(stage.params, "request", nodeType === "sub_workflow" ? root.querySelector("#stageParamRequest").value : "");
  if (nodeType === "for_each") {
    setParamValue(stage.params, "items", root.querySelector("#stageParamItems").value);
    setParamValue(stage.params, "stage", root.querySelector("#stageParamStage").value);
  } else {
    delete stage.params.items;
    delete stage.params.items_ref;
  }
  if (nodeType === "loop") {
    setParamValue(stage.params, "stage", root.querySelector("#stageParamStage").value);
    setParamValue(stage.params, "until", root.querySelector("#stageParamUntil").value);
    setParamValue(stage.params, "max_iterations", root.querySelector("#stageParamMaxIterations").value);
  } else if (nodeType !== "for_each") {
    delete stage.params.stage;
  }
  setParamValue(stage.params, "wait_for", nodeType === "join" ? root.querySelector("#stageParamWaitFor").value : "");
  setParamValue(stage.params, "prompt", nodeType === "checkpoint" ? root.querySelector("#stageParamPrompt").value : "");
}

function setParamValue(params, key, value) {
  const normalized = String(value || "").trim();
  if (normalized) {
    params[key] = normalized;
  } else {
    delete params[key];
  }
}

function parseParams(raw) {
  return parseMap(raw);
}

function isTruthyParam(value) {
  return ["true", "1", "yes", "on", "execute"].includes(String(value ?? "").trim().toLowerCase()) || value === true;
}

function parseMap(raw) {
  const out = {};
  for (const line of String(raw || "").split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const index = trimmed.indexOf("=");
    if (index <= 0) continue;
    out[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim();
  }
  return out;
}

function formatParams(params) {
  return formatMap(params);
}

function formatMap(values) {
  return Object.entries(values || {}).map(([key, value]) => `${key}=${value}`).join("\n");
}

function uniqueStageName(prefix) {
  const used = new Set(state.graph.stages.map(stage => stage.name));
  let index = 1;
  let candidate = prefix;
  while (used.has(candidate)) {
    index++;
    candidate = `${prefix}-${index}`;
  }
  return candidate;
}

function slug(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
