import { escapeHTML, formatList, request } from "../api.js";
import { localizedText, t } from "../i18n.js";
import { appendQuery, invalidateResourceCapabilities, loadResourceCapabilities, renderResourceActionButton, resourceAction, resourceActionDescription, resourceActionLabel, resourceCapability, resourceActionMethod, resourceActionNeedsName, resourceActionPath } from "../resource_actions.js";

let catalogContext = {};
let catalogFilterFrame = 0;
const examplePlaceholder = value => escapeHTML(t("common.exampleValue", { value }));

if (typeof window !== "undefined") {
  window.addEventListener("goflow:onboarding-step", event => {
    const step = event.detail?.step;
    if (!isCatalogTourStep(step)) return;
    const shell = document.querySelector(".resource-shell");
    if (!shell) return;
    const root = shell.closest("#app") || shell;
    if (resetCatalogTourFilters(root)) applyResourceFilters(root);
  });
}

export async function renderCatalog(root, runtime) {
  catalogContext = await loadCatalogContext(runtime);
  publishCatalogConfigDiagnostics();
  const providerOptions = catalogContext.providerOptions;
  const policyRules = catalogContext.policyRules;
  const teamTemplates = catalogContext.teamTemplates;
  const toolScaffolds = catalogContext.toolScaffolds || [];
  const workflowGraphs = catalogContext.workflowGraphs || [];
  const workflowTemplates = catalogContext.workflowTemplates;
  const kits = catalogContext.kits;
  const kitScaffolds = catalogContext.kitScaffolds;
  const nodeMetadataResources = catalogContext.nodeMetadataResources;
  const nodeMetadataItems = catalogNodeMetadataItems();
  const expressionHelperResources = catalogContext.expressionHelperResources;
  const expressionHelperItems = catalogExpressionHelperItems();
  const workflowSchemaResources = catalogContext.workflowSchemaResources;
  const observedWorkflowSchemas = catalogContext.workflowSchemas;
  const policyRuleScaffolds = catalogContext.policyRuleScaffolds || [];
  const resourceCapabilities = catalogContext.resourceCapabilities || [];
  const providerCount = providerOptions.length;
  const resourceCounts = renderResourceMetrics(runtime);
  root.innerHTML = `
    <div class="resource-shell">
      <section class="panel resource-hero span-12" data-tour-id="catalog-hero">
        <div class="resource-hero-copy">
          <p class="eyebrow">${t("catalog.resourceBuilder")}</p>
          <h2>${t("catalog.resourceBuilderTitle")}</h2>
          <p class="muted">${t("catalog.resourceBuilderHelp")}</p>
          <div id="resourceMetrics" class="resource-metrics">
            ${resourceCounts}
          </div>
        </div>
        <div class="resource-hero-actions">
          <div class="hero-actions">
            <button class="primary" data-scroll-resource="#resourceKitScaffolds">${t("catalog.starterCreateFull")}</button>
            <button data-new-resource="agent">${t("catalog.newAgent")}</button>
            <button data-new-resource="skill">${t("catalog.newSkill")}</button>
            <button data-new-resource="tool">${t("catalog.newTool")}</button>
            <button data-new-resource="provider">${t("catalog.newProvider")}</button>
            <button data-open-view="workflows">${t("catalog.starterOpenWorkflowStudio")}</button>
          </div>
          <div class="resource-hero-note">
            <strong>${t("catalog.advancedResourcesTitle")}</strong>
            <span>${t("catalog.advancedResourcesHelp")}</span>
            <button type="button" data-resource-advanced-toggle>${t("catalog.showAdvancedResources")}</button>
          </div>
        </div>
      </section>

      <section class="panel resource-filter-panel span-12" data-resource-filter-panel>
        <div class="resource-filter-copy">
          <p class="eyebrow">${t("catalog.resourceFinder")}</p>
          <h2>${t("catalog.resourceFinderTitle")}</h2>
          <p class="muted">${t("catalog.resourceFinderHelp")}</p>
        </div>
        <label class="stack">
          <span>${t("catalog.resourceSearch")}</span>
          <input id="resourceSearch" type="search" placeholder="${escapeHTML(t("catalog.resourceSearchPlaceholder"))}" autocomplete="off">
        </label>
        <label class="stack">
          <span>${t("catalog.resourceGroupFilter")}</span>
          <select id="resourceGroupFilter">
            <option value="recommended" selected>${t("catalog.resourceGroupRecommended")}</option>
            <option value="all">${t("catalog.resourceGroupAll")}</option>
            <option value="essential">${t("catalog.resourceGroupEssential")}</option>
            <option value="workflow">${t("catalog.resourceGroupWorkflow")}</option>
            <option value="advanced">${t("catalog.resourceGroupAdvanced")}</option>
            <option value="runtime">${t("catalog.resourceGroupRuntime")}</option>
            <option value="policy">${t("catalog.resourceGroupPolicy")}</option>
          </select>
        </label>
        <div class="resource-filter-actions">
          <span id="resourceFilterCount" class="badge"></span>
          <button id="resourceClearFilters" type="button">${t("catalog.clearFilters")}</button>
        </div>
      </section>

      <section class="panel resource-guide-panel span-12">
        ${renderResourceGuide()}
      </section>

      <section class="panel resource-starter-panel span-12" data-tour-id="catalog-starter">
        ${renderResourceStarter(kits, kitScaffolds)}
      </section>

      <section class="panel resource-relation-panel span-12">
        ${renderResourceRelationMap(runtime)}
      </section>

      <div class="resource-grid">
        <section class="panel span-4 resource-panel" data-resource-panel data-resource-group="essential" data-resource-panel-kind="agent" data-tour-id="catalog-agents">
          <div class="panel-head"><h2>${t("catalog.agents")}</h2><button data-new-resource="agent">${t("catalog.newAgent")}</button></div>
          <p class="resource-panel-copy muted">${t("tour.catalog.agents.body")}</p>
          <div class="list">${(runtime.agents || []).map(renderAgent).join("") || empty(t("catalog.noAgents"), { actionLabel: t("catalog.newAgent"), action: "agent" })}</div>
        </section>
        <section class="panel span-4 resource-panel" data-resource-panel data-resource-group="essential" data-resource-panel-kind="skill" data-tour-id="catalog-skills">
          <div class="panel-head"><h2>${t("catalog.skills")}</h2><button class="primary" data-new-resource="skill">${t("catalog.newSkill")}</button></div>
          <p class="resource-panel-copy muted">${t("tour.catalog.skills.body")}</p>
          <div class="list">${(runtime.skills || []).map(renderSkill).join("") || empty(t("catalog.noSkills"), { actionLabel: t("catalog.newSkill"), action: "skill" })}</div>
        </section>
        <section class="panel span-4 resource-panel" data-resource-panel data-resource-group="essential" data-resource-panel-kind="tool mcp mcp_server" data-tour-id="catalog-tools">
          <div class="panel-head"><h2>${t("catalog.tools")}</h2><button data-new-resource="tool">${t("catalog.newTool")}</button></div>
          <p class="resource-panel-copy muted">${t("tour.catalog.tools.body")}</p>
          <div id="resourceToolScroll" class="tool-resource-scroll">
            ${toolScaffolds.length ? `<div id="resourceToolScaffolds" class="tool-scaffold-strip">${renderToolScaffoldStrip(toolScaffolds)}</div>` : `<div id="resourceToolScaffolds" class="tool-scaffold-strip hidden"></div>`}
            <div id="resourceToolsList" class="list">${renderToolList(runtime)}</div>
          </div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="workflow" data-resource-panel-kind="team-template team_template">
          <div class="panel-head"><h2>${t("catalog.teamTemplates")}</h2><div class="hero-actions"><button data-new-resource="team-template">${t("catalog.newTeamTemplate")}</button><span id="resourceTeamTemplatesCount" class="badge">${teamTemplates.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.teamTemplatesHelp")}</p>
          <div id="resourceTeamTemplatesList" class="list">${teamTemplates.map(renderTeamTemplate).join("") || empty(t("catalog.noTeamTemplates"), { actionLabel: t("catalog.newTeamTemplate"), action: "team-template" })}</div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="workflow" data-resource-panel-kind="workflow-template workflow_template">
          <div class="panel-head"><h2>${t("catalog.workflowTemplates")}</h2><div class="hero-actions"><button data-new-resource="workflow-template">${t("catalog.newWorkflowTemplate")}</button><span id="resourceWorkflowTemplatesCount" class="badge">${workflowTemplates.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.workflowTemplatesHelp")}</p>
          <div id="resourceWorkflowTemplatesList" class="list">${workflowTemplates.map(renderWorkflowTemplate).join("") || empty(t("catalog.noWorkflowTemplates"), { actionLabel: t("catalog.newWorkflowTemplate"), action: "workflow-template" })}</div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="advanced" data-resource-panel-kind="workflow-schema workflow_schema">
          <div class="panel-head">
            <h2>${t("catalog.workflowSchemas")}</h2>
            <div class="hero-actions">
              <button data-new-resource="workflow-schema">${t("catalog.newWorkflowSchema")}</button>
              ${renderResourceActionButton(resourceCapabilities, "workflow-schema", "import_catalog", "", { fallback: `<button data-import-workflow-schema-catalog>${t("catalog.importWorkflowSchemaCatalog")}</button>` })}
              ${renderResourceActionButton(resourceCapabilities, "workflow-schema", "export_catalog", "", { fallback: `<button data-export-workflow-schema-catalog>${t("catalog.exportWorkflowSchemaCatalog")}</button>` })}
              <span id="resourceWorkflowSchemasCount" class="badge">${workflowSchemaResources.length}</span>
            </div>
          </div>
          <p class="resource-panel-copy muted">${t("catalog.workflowSchemasHelp")}</p>
          <div id="resourceWorkflowSchemaScroll" class="kit-resource-scroll workflow-schema-resource-scroll">
            <section class="kit-resource-section">
              <div class="kit-resource-section-head">
                <div>
                  <strong>${t("catalog.observedWorkflowSchemas")}</strong>
                  <span>${t("catalog.observedWorkflowSchemasHelp")}</span>
                </div>
              </div>
              <div id="resourceObservedWorkflowSchemasList" class="list">${observedWorkflowSchemas.map(renderObservedWorkflowSchema).join("") || empty(t("catalog.noObservedWorkflowSchemas"), { actionLabel: t("catalog.openRun"), view: "playground" })}</div>
            </section>
            <section class="kit-resource-section">
              <div class="kit-resource-section-head">
                <div>
                  <strong>${t("catalog.savedWorkflowSchemas")}</strong>
                  <span>${t("catalog.savedWorkflowSchemasHelp")}</span>
                </div>
              </div>
              <div id="resourceWorkflowSchemasList" class="list">${workflowSchemaResources.map(renderWorkflowSchemaResource).join("") || empty(t("catalog.noWorkflowSchemas"), { actionLabel: t("catalog.newWorkflowSchema"), action: "workflow-schema" })}</div>
            </section>
          </div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="essential" data-resource-panel-kind="kit">
          <div class="panel-head"><h2>${t("catalog.kits")}</h2><div class="hero-actions"><button data-new-resource="kit">${t("catalog.newKit")}</button>${renderResourceActionButton(resourceCapabilities, "kit", "import_bundle", "", { fallback: `<button data-import-kit-bundle>${t("catalog.importKitBundle")}</button>` })}<span id="resourceKitsCount" class="badge">${kits.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.kitsHelp")}</p>
          <div id="resourceKitScroll" class="kit-resource-scroll">
            ${renderKitResourceSections(kits, kitScaffolds)}
          </div>
        </section>
        <section class="panel span-6 resource-panel" data-resource-panel data-resource-group="advanced" data-resource-panel-kind="node-metadata workflow_node_metadata">
          <div class="panel-head"><h2>${t("catalog.nodeMetadata")}</h2><div class="hero-actions"><button data-new-resource="node-metadata">${t("catalog.newNodeMetadata")}</button><span id="resourceNodeMetadataCount" class="badge">${nodeMetadataItems.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.nodeMetadataHelp")}</p>
          <div id="resourceNodeMetadataList" class="list">${nodeMetadataItems.map(renderNodeMetadataResource).join("") || empty(t("catalog.noNodeMetadata"), { actionLabel: t("catalog.newNodeMetadata"), action: "node-metadata" })}</div>
        </section>
        <section class="panel span-6 resource-panel" data-resource-panel data-resource-group="advanced" data-resource-panel-kind="expression-helper expression_helper">
          <div class="panel-head"><h2>${t("catalog.expressionHelpers")}</h2><div class="hero-actions"><button data-new-resource="expression-helper">${t("catalog.newExpressionHelper")}</button><span id="resourceExpressionHelpersCount" class="badge">${expressionHelperItems.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.expressionHelpersHelp")}</p>
          <div id="resourceExpressionHelpersList" class="list">${expressionHelperItems.map(renderExpressionHelperResource).join("") || empty(t("catalog.noExpressionHelpers"), { actionLabel: t("catalog.newExpressionHelper"), action: "expression-helper" })}</div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="runtime" data-resource-panel-kind="provider">
          <div class="panel-head"><h2>${t("catalog.providers")}</h2><div class="hero-actions"><button data-new-resource="provider">${t("catalog.newProvider")}</button><span id="resourceProvidersCount" class="badge">${providerCount}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.providersHelp")}</p>
          <div id="resourceProvidersList" class="list">${providerOptions.map(renderProvider).join("") || empty(t("catalog.noProviders"), { actionLabel: t("catalog.newProvider"), action: "provider" })}</div>
        </section>
        <section class="panel span-12 resource-panel" data-resource-panel data-resource-group="policy" data-resource-panel-kind="policy-rule policy_rule">
          <div class="panel-head"><h2>${t("catalog.policyRules")}</h2><div class="hero-actions"><button data-new-resource="policy-rule">${t("catalog.newPolicyRule")}</button><span id="resourcePolicyRulesCount" class="badge">${policyRules.length}</span></div></div>
          <p class="resource-panel-copy muted">${t("catalog.policyRulesHelp")}</p>
          <div id="resourcePolicyRuleScroll" class="kit-resource-scroll policy-rule-resource-scroll">
            ${renderPolicyRuleResourceSections(policyRules, policyRuleScaffolds)}
          </div>
        </section>
        <section class="panel span-12 resource-mcp-health-panel">
          <div class="panel-head"><h2>${t("catalog.mcpHealth")}</h2><span class="badge">${Object.keys(runtime.mcp_health || {}).length}</span></div>
          <table class="kv">${Object.entries(runtime.mcp_health || {}).map(([name, status]) => `<tr><th>${escapeHTML(name)}</th><td>${escapeHTML(status)}</td></tr>`).join("") || `<tr><td class="muted">${t("catalog.noMCP")}</td></tr>`}</table>
        </section>
        <section class="panel span-12 resource-cli-parity-panel">
          <div class="panel-head"><h2>${t("catalog.cliParity")}</h2><span class="badge">${t("catalog.webCoverage")}</span></div>
          <div class="parity-grid">
            ${parityItem(t("catalog.parityVisualWorkflow"), t("catalog.parityVisualWorkflowHelp"))}
            ${parityItem(t("catalog.parityResourceEdit"), t("catalog.parityResourceEditHelp"))}
            ${parityItem(t("catalog.parityChatTargeting"), t("catalog.parityChatTargetingHelp"))}
          </div>
        </section>
      </div>

      <input id="kitBundleImportFile" class="hidden" type="file" accept=".json,.yaml,.yml,application/json,application/yaml">
      <input id="workflowSchemaImportFile" class="hidden" type="file" accept=".json,application/json">

      <div id="resourceDesigner" class="designer hidden" role="dialog" aria-modal="true">
        <div class="designer-backdrop" data-close-designer></div>
        <section class="designer-panel">
          <div class="designer-head">
            <div>
              <p class="eyebrow" id="designerEyebrow">${t("catalog.resourceBuilder")}</p>
              <h2 id="designerTitle">${t("catalog.resourceBuilderTitle")}</h2>
            </div>
            <button data-close-designer aria-label="${escapeHTML(t("catalog.closeDesigner"))}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 7l10 10M17 7 7 17"/></svg></button>
          </div>
          <nav id="resourceDesignerSections" class="designer-section-nav" aria-label="${escapeHTML(t("catalog.designerSections"))}">
            <button type="button" data-designer-section="identity">${t("catalog.designerSectionIdentity")}</button>
            <button type="button" data-designer-section="brief">${t("catalog.designerSectionBrief")}</button>
            <button type="button" data-designer-section="module">${t("catalog.designerSectionModule", { type: t("catalog.resourceSkill") })}</button>
            <button type="button" data-designer-section="actions">${t("catalog.designerSectionActions")}</button>
            <button type="button" data-designer-section="result">${t("catalog.designerSectionResult")}</button>
          </nav>
          <div class="designer-body">
            <div class="grid">
              <label class="span-4 stack"><span>${t("catalog.resourceType")}</span><select id="resourceType"><option value="skill">${t("catalog.resourceSkill")}</option><option value="agent">${t("catalog.resourceAgent")}</option><option value="tool">${t("catalog.resourceTool")}</option><option value="team-template">${t("catalog.resourceTeamTemplate")}</option><option value="kit">${t("catalog.resourceKit")}</option><option value="workflow-template">${t("catalog.resourceWorkflowTemplate")}</option><option value="workflow-schema">${t("catalog.resourceWorkflowSchema")}</option><option value="node-metadata">${t("catalog.resourceNodeMetadata")}</option><option value="expression-helper">${t("catalog.resourceExpressionHelper")}</option><option value="provider">${t("catalog.resourceProvider")}</option><option value="workflow">${t("catalog.resourceWorkflow")}</option><option value="policy-rule">${t("catalog.resourcePolicyRule")}</option></select></label>
              <label class="span-4 stack"><span>${t("catalog.name")}</span><input id="resourceName" placeholder="${examplePlaceholder("custom-code-review")}"></label>
              <label class="span-4 stack" data-resource-field="purpose"><span>${t("catalog.purpose")}</span><input id="resourcePurpose" placeholder="${escapeHTML(t("catalog.purpose"))}"></label>
              <label class="span-12 stack"><span>${t("catalog.description")}</span><input id="resourceDescription" placeholder="${escapeHTML(t("catalog.description"))}"></label>
              <div id="resourceCapabilitySummary" class="span-12 resource-capability-summary" aria-live="polite"></div>
              <section id="resourceBriefPanel" class="span-12 resource-brief-panel" data-designer-brief>
                <div class="resource-brief-head">
                  <div>
                    <span>${t("catalog.briefEyebrow")}</span>
                    <strong>${t("catalog.briefTitle")}</strong>
                    <p>${t("catalog.briefHelp")}</p>
                  </div>
                  <button id="resourceBriefApply" type="button">${t("catalog.briefApply")}</button>
                </div>
                <div class="resource-brief-grid">
                  <label class="stack"><span>${t("catalog.briefGoal")}</span><textarea id="resourceBriefGoal" data-resource-brief-field="goal" class="compact-textarea" placeholder="${escapeHTML(t("catalog.briefGoalPlaceholder"))}"></textarea></label>
                  <label class="stack"><span>${t("catalog.briefInputs")}</span><textarea id="resourceBriefInputs" data-resource-brief-field="inputs" class="compact-textarea" placeholder="${escapeHTML(t("catalog.briefInputsPlaceholder"))}"></textarea></label>
                  <label class="stack"><span>${t("catalog.briefOutputs")}</span><textarea id="resourceBriefOutputs" data-resource-brief-field="outputs" class="compact-textarea" placeholder="${escapeHTML(t("catalog.briefOutputsPlaceholder"))}"></textarea></label>
                  <label class="stack"><span>${t("catalog.briefExample")}</span><textarea id="resourceBriefExample" data-resource-brief-field="example" class="compact-textarea" placeholder="${escapeHTML(t("catalog.briefExamplePlaceholder"))}"></textarea></label>
                  <label class="stack resource-brief-wide"><span>${t("catalog.briefDependencies")}</span><textarea id="resourceBriefDependencies" data-resource-brief-field="dependencies" class="compact-textarea" placeholder="${escapeHTML(t("catalog.briefDependenciesPlaceholder"))}"></textarea></label>
                </div>
                <div id="resourceBriefPreview" class="resource-brief-preview" aria-live="polite"></div>
              </section>

              <div class="span-12 skill-only grid">
                <label class="span-3 stack"><span>${t("catalog.version")}</span><input id="skillVersion" placeholder="${examplePlaceholder("1.0.0")}"></label>
                <label class="span-3 stack"><span>${t("catalog.author")}</span><input id="skillAuthor" placeholder="${examplePlaceholder("GoFlow Studio")}"></label>
                <label class="span-3 stack"><span>${t("catalog.priority")}</span><input id="skillPriority" type="number" min="0" step="1"></label>
                <label class="span-3 stack"><span>${t("catalog.maxIterations")}</span><input id="skillMaxIterations" type="number" min="0" step="1"></label>
                <label class="span-4 stack"><span>${t("common.mode")}</span><select id="skillMode">${modeOptionsHTML()}</select></label>
                <label class="span-4 stack"><span>${t("catalog.preferredAgent")}</span><select id="skillAgent"></select></label>
                <label class="span-4 stack"><span>${t("catalog.outputKind")}</span><input id="skillOutputKind" placeholder="${examplePlaceholder("summary / findings / changes")}"></label>
                <label class="span-4 stack"><span>${t("catalog.allowedKinds")}</span><input id="skillAllowedKinds" placeholder="${examplePlaceholder("read, write, exec, network")}"></label>
                <label class="span-4 stack"><span>${t("catalog.nextSkills")}</span><input id="skillNext" placeholder="${examplePlaceholder("code-audit, docs-review")}"></label>
                <label class="span-4 stack"><span>${t("catalog.activationKeywords")}</span><textarea id="skillKeywords" class="compact-textarea" placeholder="${examplePlaceholder(`audit
review
security`)}"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.tools")}</span><textarea id="skillTools" class="compact-textarea" placeholder="${examplePlaceholder(`file_tools/read_file|required
web_tools/web_search`)}"></textarea></label>
                <section class="span-12 resource-script-editor">
                  <div class="resource-script-head">
                    <div>
                      <strong>${t("catalog.skillScripts")}</strong>
                      <span>${t("catalog.skillScriptsHelp")}</span>
                    </div>
                    <button type="button" data-add-skill-script>${t("catalog.addSkillScript")}</button>
                  </div>
                  <div id="skillScriptsList" class="resource-script-list"></div>
                  <textarea id="skillScriptsJSON" class="hidden"></textarea>
                </section>
                <label class="span-6 stack"><span>${t("catalog.params")}</span><textarea id="skillParams" class="compact-textarea" placeholder="${examplePlaceholder("target|string|Target path|true")}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.metadata")}</span><textarea id="skillMetadata" class="compact-textarea" placeholder="${examplePlaceholder(`owner=platform
risk=low`)}"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.embeddingDescription")}</span><input id="skillEmbeddingDescription" placeholder="${escapeHTML(t("catalog.embeddingDescription"))}"></label>
                <label class="span-12 stack"><span>${t("catalog.instructions")}</span><textarea id="skillInstructions" placeholder="${escapeHTML(t("catalog.defaultInstructions"))}"></textarea></label>
              </div>

              <div class="span-12 agent-only grid hidden">
                <label class="span-4 stack"><span>${commonLabel("provider")}</span><select id="agentProvider"></select></label>
                <label class="span-4 stack"><span>${t("catalog.providerStatus")}</span><input id="agentProviderStatus" disabled placeholder="${examplePlaceholder("runtime.providers -> /api/resources/providers")}"></label>
                <label class="span-4 stack"><span>${commonLabel("model")}</span><input id="agentModel" placeholder="${escapeHTML(t("common.model"))}"></label>
                <label class="span-4 stack"><span>${t("common.mode")}</span><select id="agentMode">${modeOptionsHTML()}</select></label>
                <label class="span-3 stack"><span>${t("catalog.temperature")}</span><input id="agentTemperature" type="number" min="0" max="2" step="0.1"></label>
                <label class="span-3 stack"><span>${t("catalog.maxTokens")}</span><input id="agentMaxTokens" type="number" min="0" step="1"></label>
                <label class="span-3 stack"><span>${t("catalog.maxIterations")}</span><input id="agentMaxIterations" type="number" min="1" step="1"></label>
                <label class="span-3 stack"><span>${t("common.policy")}</span><select id="agentToolPolicy">${policyOptionsHTML()}</select></label>
                <label class="span-6 stack"><span>${t("catalog.allowedKinds")}</span><input id="agentAllowedKinds" placeholder="${examplePlaceholder("read, write, exec, network")}"></label>
                <label class="span-6 stack"><span>${t("catalog.allowedTools")}</span><input id="agentAllowedTools" placeholder="${examplePlaceholder("file_tools/read_file, web_tools/web_search")}"></label>
                <label class="span-12 stack"><span>${t("catalog.systemPrompt")}</span><textarea id="agentSystemPrompt" placeholder="${escapeHTML(t("catalog.systemPrompt"))}"></textarea></label>
              </div>

              <div class="span-12 tool-only grid hidden">
                <div class="span-12 tool-template-box">
                  <div>
                    <strong>${t("catalog.toolScaffolds")}</strong>
                    <span>${t("catalog.toolScaffoldsHelp")}</span>
                  </div>
                  <label class="stack"><span>${t("catalog.toolScaffoldPreset")}</span><select id="toolScaffoldPreset"></select></label>
                  <label class="check"><input id="toolScaffoldOverwrite" type="checkbox"><span>${t("catalog.toolScaffoldOverwrite")}</span></label>
                  <div class="hero-actions">
                    <button id="applyToolScaffold" type="button">${t("catalog.applyToolScaffold")}</button>
                    <button id="createToolScaffold" type="button" class="primary">${t("catalog.createToolScaffold")}</button>
                  </div>
                  <div id="toolScaffoldPreview" class="tool-scaffold-preview"></div>
                </div>
                <label class="span-3 stack"><span>${t("catalog.language")}</span><select id="toolLanguage"><option value="python">python</option></select></label>
                <label class="span-3 stack"><span>${t("catalog.command")}</span><input id="toolCommand" placeholder="${examplePlaceholder("python")}"></label>
                <label class="span-6 stack"><span>${t("catalog.args")}</span><input id="toolArgs" placeholder="${examplePlaceholder("./mcp_servers/custom.py")}"></label>
                <label class="span-3 stack"><span>${t("catalog.timeout")}</span><input id="toolTimeout" placeholder="${examplePlaceholder("30s")}"></label>
                <label class="span-3 stack"><span>${t("catalog.workdir")}</span><input id="toolWorkdir" placeholder="${examplePlaceholder(".")}"></label>
                <label class="span-3 stack"><span>${t("catalog.isolation")}</span><select id="toolIsolation">${isolationOptionsHTML()}</select></label>
                <label class="span-3 stack"><span>${t("catalog.restartLimit")}</span><input id="toolRestartLimit" type="number" min="1" step="1"></label>
                <label class="span-4 stack"><span>${t("catalog.cooldown")}</span><input id="toolCooldown" placeholder="${examplePlaceholder("10s")}"></label>
                <label class="span-4 stack"><span>${t("catalog.maxRequestBytes")}</span><input id="toolMaxRequestBytes" type="number" min="1" step="1"></label>
                <label class="span-4 stack"><span>${t("catalog.maxResponseBytes")}</span><input id="toolMaxResponseBytes" type="number" min="1" step="1"></label>
                <label class="span-6 stack"><span>${t("catalog.envAllowlist")}</span><input id="toolEnvAllowlist" placeholder="${examplePlaceholder("PATH, HOME, USERPROFILE")}"></label>
                <label class="span-6 stack"><span>${t("catalog.allowedCommands")}</span><input id="toolAllowedCommands" placeholder="${examplePlaceholder("python")}"></label>
                <label class="span-6 stack"><span>${t("catalog.allowedCommandPaths")}</span><input id="toolAllowedCommandPaths" placeholder="${examplePlaceholder("/usr/bin, C:/Python")}"></label>
                <label class="span-6 check"><input id="toolEnabled" type="checkbox"><span>${t("catalog.enabled")}</span></label>
                <label class="span-6 check"><input id="toolNetworkDisabled" type="checkbox"><span>${t("catalog.networkDisabled")}</span></label>
                <div class="span-12 resource-isolation-options">
                  <div class="resource-isolation-head">
                    <div>
                      <strong>${t("catalog.isolationOptions")}</strong>
                      <span>${t("catalog.isolationOptionsHelp")}</span>
                    </div>
                    <span class="badge neutral">${t("catalog.containerIsolation")}</span>
                  </div>
                  <label class="stack"><span>${t("catalog.isolationProfile")}</span><select id="toolIsolationProfile">${isolationProfileOptionsHTML()}</select></label>
                  <small id="toolIsolationProfileHint" class="resource-isolation-hint neutral">${t("catalog.isolationProfileDefaultHint")}</small>
                  <label class="stack"><span>${t("catalog.isolationImage")}</span><input id="toolIsolationImage" placeholder="${examplePlaceholder("goflow/mcp-tools:latest")}"></label>
                  <label class="stack"><span>${t("catalog.isolationRuntime")}</span><input id="toolIsolationRuntime" placeholder="${examplePlaceholder("docker")}"></label>
                  <label class="stack"><span>${t("catalog.isolationPullPolicy")}</span><select id="toolIsolationPullPolicy"><option value="">${t("catalog.pullPolicyDefault")}</option><option value="missing">${t("catalog.pullPolicyMissing")}</option><option value="never">${t("catalog.pullPolicyNever")}</option><option value="always">${t("catalog.pullPolicyAlways")}</option></select></label>
                  <label class="stack"><span>${t("catalog.isolationWorkspaceMount")}</span><select id="toolIsolationWorkspaceMount"><option value=""></option><option value="ro">${t("catalog.mountReadOnly")}</option><option value="rw">${t("catalog.mountReadWrite")}</option><option value="none">${t("catalog.mountNone")}</option></select></label>
                  <label class="stack"><span>${t("catalog.isolationWorkspaceTarget")}</span><input id="toolIsolationWorkspaceTarget" placeholder="${examplePlaceholder("/workspace")}"></label>
                  <label class="stack"><span>${t("catalog.isolationContainerWorkdir")}</span><input id="toolIsolationContainerWorkdir" placeholder="${examplePlaceholder("/app")}"></label>
                  <label class="stack"><span>${t("catalog.isolationNetwork")}</span><input id="toolIsolationNetwork" list="toolIsolationNetworkOptions" aria-describedby="toolIsolationNetworkHint" placeholder="${examplePlaceholder("disabled / none / bridge / custom-network")}"></label>
                  <datalist id="toolIsolationNetworkOptions">
                    <option value="disabled"></option>
                    <option value="none"></option>
                    <option value="bridge"></option>
                    <option value="host"></option>
                  </datalist>
                  <small id="toolIsolationNetworkHint" class="resource-isolation-hint neutral">${t("catalog.isolationNetworkInheritedHint")}</small>
                  <label class="stack"><span>${t("catalog.isolationMemory")}</span><input id="toolIsolationMemory" placeholder="${examplePlaceholder("512m")}"></label>
                  <label class="stack"><span>${t("catalog.isolationMemorySwap")}</span><input id="toolIsolationMemorySwap" placeholder="${examplePlaceholder("512m")}"></label>
                  <label class="stack"><span>${t("catalog.isolationCpus")}</span><input id="toolIsolationCpus" placeholder="${examplePlaceholder("1.0")}"></label>
                  <label class="stack"><span>${t("catalog.isolationPidsLimit")}</span><input id="toolIsolationPidsLimit" type="number" min="1" step="1" placeholder="${examplePlaceholder("128")}"></label>
                  <label class="stack"><span>${t("settings.field.containerUser")}</span><input id="toolIsolationUser" placeholder="${examplePlaceholder("1000:1000")}"></label>
                  <label class="stack"><span>${t("settings.field.userNamespace")}</span><input id="toolIsolationUserns" placeholder="${examplePlaceholder("auto / nomap")}"></label>
                  <label class="stack"><span>${t("settings.field.containerTmpfs")}</span><input id="toolIsolationTmpfs" placeholder="${examplePlaceholder("/tmp:rw,size=64m")}"></label>
                  <label class="check"><input id="toolIsolationReadonlyRootfs" type="checkbox"><span>${t("catalog.isolationReadonlyRootfs")}</span></label>
                  <label class="check"><input id="toolIsolationNoNewPrivileges" type="checkbox"><span>${t("catalog.isolationNoNewPrivileges")}</span></label>
                  <label class="check"><input id="toolIsolationInit" type="checkbox"><span>${t("settings.field.containerInit")}</span></label>
                  <label class="stack"><span>${t("catalog.isolationCapDrop")}</span><input id="toolIsolationCapDrop" placeholder="${examplePlaceholder("all")}"></label>
                  <label class="stack"><span>${t("settings.field.containerToolSource")}</span><input id="toolIsolationToolSource" placeholder="${examplePlaceholder("./mcp_servers/tool.py")}"></label>
                  <label class="stack"><span>${t("settings.field.containerToolTarget")}</span><input id="toolIsolationToolTarget" placeholder="${examplePlaceholder("/tool/tool.py")}"></label>
                  <label class="stack"><span>${t("settings.field.containerToolMount")}</span><select id="toolIsolationToolMount"><option value=""></option><option value="ro">${t("catalog.mountReadOnly")}</option><option value="rw">${t("catalog.mountReadWrite")}</option><option value="none">${t("catalog.mountNone")}</option></select></label>
                  <label class="stack resource-isolation-raw"><span>${t("catalog.isolationOptionsRaw")}</span><textarea id="toolIsolationOptionsRaw" class="compact-textarea" placeholder="${examplePlaceholder("key=value")}"></textarea></label>
                </div>
                <label class="span-12 stack"><span>${t("catalog.uploadToolCode")}</span><input id="toolCodeFile" type="file" accept=".py,.txt"></label>
                <label class="span-12 stack"><span>${t("catalog.toolCode")}</span><textarea id="toolCode" class="code-textarea" spellcheck="false"></textarea></label>
              </div>
              <div class="span-12 workflow-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.workflowTemplate")}</span><select id="workflowTemplate"></select></label>
                <label class="span-4 stack"><span>${t("catalog.approvalStage")}</span><select id="workflowApproval"><option value="implement">${t("catalog.approvalStageImplement")}</option><option value="audit">${t("catalog.approvalStageAudit")}</option><option value="none">${t("catalog.approvalStageNone")}</option></select></label>
                <label class="span-4 stack"><span>${t("catalog.openAfterSave")}</span><select id="workflowOpenAfterSave"><option value="yes">${t("common.on")}</option><option value="no">${t("common.off")}</option></select></label>
                <div class="span-12 item muted">${t("catalog.workflowDesignerHelp")}</div>
              </div>

              <div class="span-12 workflow-template-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.templateSaveMode")}</span><select id="workflowTemplateSaveMode"><option value="fork">${t("catalog.templateForkMode")}</option><option value="capture">${t("catalog.templateCaptureMode")}</option><option value="edit">${t("catalog.templateEditMode")}</option></select></label>
                <label class="span-4 stack workflow-template-source-field"><span>${t("catalog.sourceTemplate")}</span><select id="workflowTemplateSource"></select></label>
                <label class="span-4 stack workflow-template-capture-field hidden"><span>${t("catalog.sourceWorkflow")}</span><select id="workflowTemplateCaptureSource"></select></label>
                <label class="span-4 stack"><span>${t("catalog.templateTitle")}</span><input id="workflowTemplateTitle" placeholder="${escapeHTML(t("catalog.workflowTemplateTitlePlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamCategory")}</span><input id="workflowTemplateCategory" placeholder="${examplePlaceholder("security")}"></label>
                <label class="span-8 stack"><span>${t("catalog.teamTags")}</span><input id="workflowTemplateTags" placeholder="${examplePlaceholder("security, approval, input")}"></label>
                <label class="span-12 stack workflow-template-graph-field"><span>${t("catalog.templateGraphJSON")}</span><textarea id="workflowTemplateGraph" class="code-textarea" spellcheck="false" placeholder="${examplePlaceholder('{"name":"custom-template","stages":[...]}')}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.workflowTemplateSaveHelp")}</div>
              </div>

              <div class="span-12 workflow-schema-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.schemaWorkflowName")}</span><select id="workflowSchemaSource"></select></label>
                <label class="span-4 check"><input id="workflowSchemaActivateMerge" type="checkbox"><span>${t("catalog.workflowSchemaMergeOnActivate")}</span></label>
                <div class="span-4 hero-actions workflow-schema-form-actions">
                  <button id="applyWorkflowSchemaSource" type="button">${t("catalog.applyObservedSchema")}</button>
                </div>
                <label class="span-12 stack"><span>${t("catalog.schemaJSON")}</span><textarea id="workflowSchemaJSON" class="code-textarea" spellcheck="false" placeholder="${escapeHTML(t("catalog.schemaJSONPlaceholder"))}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.workflowSchemaSaveHelp")}</div>
              </div>

              <div class="span-12 node-metadata-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.nodeType")}</span><select id="nodeMetadataType"></select></label>
                <label class="span-4 stack"><span>${t("catalog.policyLabel")}</span><input id="nodeMetadataLabel" placeholder="${escapeHTML(t("catalog.nodeLabelPlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamCategory")}</span><input id="nodeMetadataCategory" placeholder="${examplePlaceholder("control")}"></label>
                <label class="span-12 stack"><span>${t("catalog.description")}</span><textarea id="nodeMetadataDescription" class="compact-textarea" placeholder="${escapeHTML(t("catalog.nodeMetadataHelp"))}"></textarea></label>
                <label class="span-4 stack"><span>${t("catalog.teamTags")}</span><input id="nodeMetadataTags" placeholder="${examplePlaceholder("control, approval")}"></label>
                <label class="span-4 check"><input id="nodeMetadataControl" type="checkbox"><span>${t("catalog.nodeControl")}</span></label>
                <label class="span-4 check"><input id="nodeMetadataVisualOnly" type="checkbox"><span>${t("catalog.nodeVisualOnly")}</span></label>
                <label class="span-6 stack"><span>${t("catalog.nodeHints")}</span><textarea id="nodeMetadataHints" class="compact-textarea" placeholder="${escapeHTML(t("catalog.linePerItem"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeWarnings")}</span><textarea id="nodeMetadataWarnings" class="compact-textarea" placeholder="${escapeHTML(t("catalog.linePerItem"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeFieldsJSON")}</span><textarea id="nodeMetadataFields" class="compact-textarea" spellcheck="false" placeholder="${escapeHTML(t("catalog.nodeFieldsPlaceholder"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeOutputsJSON")}</span><textarea id="nodeMetadataOutputs" class="compact-textarea" spellcheck="false" placeholder="${escapeHTML(t("catalog.nodeOutputsPlaceholder"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeExamplesJSON")}</span><textarea id="nodeMetadataExamples" class="compact-textarea" spellcheck="false" placeholder="${escapeHTML(t("catalog.nodeExamplesPlaceholder"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeDefaultStageJSON")}</span><textarea id="nodeMetadataDefaultStage" class="compact-textarea" spellcheck="false" placeholder="${examplePlaceholder('{"node_type":"policy_guard","params":{"rule":"security"}}')}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.nodeMetadataSaveHelp")}</div>
              </div>

              <div class="span-12 expression-helper-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.expressionHelperBase")}</span><select id="expressionHelperSource"></select></label>
                <label class="span-4 stack"><span>${t("catalog.policyLabel")}</span><input id="expressionHelperLabel" placeholder="${escapeHTML(t("catalog.expressionHelperLabelPlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamCategory")}</span><input id="expressionHelperCategory" placeholder="${examplePlaceholder("risk")}"></label>
                <label class="span-12 stack"><span>${t("catalog.description")}</span><textarea id="expressionHelperDescription" class="compact-textarea" placeholder="${escapeHTML(t("catalog.expressionHelpersHelp"))}"></textarea></label>
                <label class="span-4 stack"><span>${t("catalog.expressionSignature")}</span><input id="expressionHelperSignature" placeholder="${examplePlaceholder("risk_rank(value)")}"></label>
                <label class="span-4 stack"><span>${t("catalog.expressionInsertText")}</span><input id="expressionHelperInsertText" placeholder="${examplePlaceholder("risk_rank(previous.summary)")}"></label>
                <label class="span-4 stack"><span>${t("catalog.expressionReturnType")}</span><input id="expressionHelperReturnType" placeholder="${examplePlaceholder("number")}"></label>
                <label class="span-3 stack"><span>${t("catalog.expressionMinArgs")}</span><input id="expressionHelperMinArgs" type="number" min="0" step="1"></label>
                <label class="span-3 stack"><span>${t("catalog.expressionMaxArgs")}</span><input id="expressionHelperMaxArgs" type="number" min="0" step="1"></label>
                <label class="span-3 stack"><span>${t("catalog.expressionModes")}</span><input id="expressionHelperModes" placeholder="${examplePlaceholder("condition, policy")}"></label>
                <label class="span-3 stack"><span>${t("catalog.expressionNodeTypes")}</span><input id="expressionHelperNodeTypes" placeholder="${examplePlaceholder("policy_guard, condition")}"></label>
                <label class="span-6 stack"><span>${t("catalog.expressionArgsJSON")}</span><textarea id="expressionHelperArgs" class="compact-textarea" spellcheck="false" placeholder="${examplePlaceholder('[{"name":"value","type":"string","required":true}]')}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.expressionExamples")}</span><textarea id="expressionHelperExamples" class="compact-textarea" placeholder="${examplePlaceholder("risk_rank(previous.summary) >= 4")}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeHints")}</span><textarea id="expressionHelperHints" class="compact-textarea" placeholder="${escapeHTML(t("catalog.linePerItem"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.nodeWarnings")}</span><textarea id="expressionHelperWarnings" class="compact-textarea" placeholder="${escapeHTML(t("catalog.linePerItem"))}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.expressionHelperSaveHelp")}</div>
              </div>

              <div class="span-12 team-template-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.teamTitle")}</span><input id="teamTitle" placeholder="${escapeHTML(t("catalog.teamTitlePlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamCategory")}</span><input id="teamCategory" placeholder="${examplePlaceholder("software")}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamTags")}</span><input id="teamTags" placeholder="${examplePlaceholder("review, audit, code")}"></label>
                <label class="span-6 stack"><span>${t("catalog.teamRecommendedWorkflow")}</span><input id="teamRecommendedWorkflow" placeholder="${examplePlaceholder("plan-fix-audit")}"></label>
                <label class="span-6 stack"><span>${t("catalog.teamEntryAgent")}</span><input id="teamEntryAgent" placeholder="${examplePlaceholder("planner")}"></label>
                <label class="span-12 stack"><span>${t("catalog.teamRoles")}</span><textarea id="teamRoles" class="compact-textarea" placeholder="${escapeHTML(t("catalog.teamRolesPlaceholder"))}"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.teamHandoffs")}</span><textarea id="teamHandoffs" class="compact-textarea" placeholder="${escapeHTML(t("catalog.teamHandoffsPlaceholder"))}"></textarea></label>
                <label class="span-12 stack"><span>${t("catalog.teamBlackboard")}</span><textarea id="teamBlackboard" class="compact-textarea" placeholder="${escapeHTML(t("catalog.teamBlackboardPlaceholder"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.teamOutputContract")}</span><textarea id="teamOutputContract" class="compact-textarea" placeholder="${examplePlaceholder(`final_report
changed_files
residual_risks`)}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.teamQuorumPresets")}</span><textarea id="teamQuorumPresets" class="compact-textarea" placeholder="${examplePlaceholder('[{"name":"security-review","required":1,"roles":["reviewer"],"reject_blocks":true}]')}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.teamTemplateSaveHelp")}</div>
              </div>

              <div class="span-12 kit-only grid hidden">
                <label class="span-4 stack"><span>${t("catalog.kitTitle")}</span><input id="kitTitle" placeholder="${escapeHTML(t("catalog.kitTitlePlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamCategory")}</span><input id="kitCategory" placeholder="${examplePlaceholder("software")}"></label>
                <label class="span-4 stack"><span>${t("catalog.teamTags")}</span><input id="kitTags" placeholder="${examplePlaceholder("software, review, delivery")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitAgents")}</span><input id="kitAgents" placeholder="${examplePlaceholder("planner, fixer")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitSkills")}</span><input id="kitSkills" placeholder="${examplePlaceholder("execution-plan, code-writing")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitTools")}</span><input id="kitTools" placeholder="${examplePlaceholder("file_tools/read_file")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitProviders")}</span><input id="kitProviders" placeholder="${examplePlaceholder("openai")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitWorkflows")}</span><input id="kitWorkflows" placeholder="${examplePlaceholder("plan-fix-audit")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitWorkflowTemplates")}</span><input id="kitWorkflowTemplates" placeholder="${examplePlaceholder("human-input-security-review")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitTeamTemplates")}</span><input id="kitTeamTemplates" placeholder="${examplePlaceholder("software-task-team")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitPolicyRules")}</span><input id="kitPolicyRules" placeholder="${examplePlaceholder("team_approval_gate")}"></label>
                <label class="span-4 stack"><span>${t("catalog.kitRequiredEnv")}</span><input id="kitRequiredEnv" placeholder="${examplePlaceholder("OPENAI_API_KEY")}"></label>
                <label class="span-6 stack"><span>${t("catalog.kitExamplesJSON")}</span><textarea id="kitExamples" class="compact-textarea" spellcheck="false" placeholder="${escapeHTML(t("catalog.kitExamplesPlaceholder"))}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.metadata")}</span><textarea id="kitMetadata" class="compact-textarea" placeholder="${examplePlaceholder(`owner=platform
tier=default`)}"></textarea></label>
                <div id="kitValidationSummary" class="span-12 item muted hidden"></div>
                <div class="span-12 item muted">${t("catalog.kitSaveHelp")}</div>
              </div>

              <div class="span-12 provider-only grid hidden">
                <label class="span-4 stack"><span>${commonLabel("provider")}</span><input id="providerID" placeholder="${examplePlaceholder("openai")}"></label>
                <label class="span-4 stack"><span>${t("catalog.providerType")}</span><input id="providerType" placeholder="${examplePlaceholder("openai-compatible")}"></label>
                <label class="span-4 stack"><span>${t("catalog.providerDefaultModel")}</span><input id="providerDefaultModel" placeholder="${examplePlaceholder("claude-opus-4-6")}"></label>
                <label class="span-6 stack"><span>${t("catalog.providerBaseURL")}</span><input id="providerBaseURL" placeholder="${examplePlaceholder("https://api.example.com")}"></label>
                <label class="span-6 stack"><span>${t("catalog.providerAPIKey")}</span><input id="providerAPIKey" type="password" autocomplete="off" placeholder="${examplePlaceholder("sk-... or ${OPENAI_API_KEY}")}"></label>
                <label class="span-6 stack"><span>${t("catalog.providerModels")}</span><input id="providerModels" placeholder="${examplePlaceholder("claude-opus-4-6, claude-sonnet-4-5")}"></label>
                <label class="span-6 stack"><span>${t("catalog.metadata")}</span><textarea id="providerMetadata" class="compact-textarea" placeholder="${examplePlaceholder(`owner=platform
tier=default`)}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.providerSetupHelp")}</div>
                <label class="span-12 stack"><span>${t("catalog.description")}</span><textarea id="providerDescription" class="compact-textarea" placeholder="${escapeHTML(t("catalog.providersHelp"))}"></textarea></label>
              </div>

              <div class="span-12 policy-rule-only grid hidden">
                <div class="span-12 policy-rule-template-box">
                  <div>
                    <strong>${t("catalog.policyRuleScaffolds")}</strong>
                    <span>${t("catalog.policyRuleScaffoldsHelp")}</span>
                  </div>
                  <label class="stack"><span>${t("catalog.policyRuleScaffoldPreset")}</span><select id="policyScaffoldPreset"></select></label>
                  <label class="check"><input id="policyScaffoldOverwrite" type="checkbox"><span>${t("catalog.policyRuleScaffoldOverwrite")}</span></label>
                  <div class="hero-actions">
                    <button id="applyPolicyRuleScaffold" type="button">${t("catalog.applyPolicyRuleScaffold")}</button>
                    <button id="createPolicyRuleScaffold" type="button" class="primary">${t("catalog.createPolicyRuleScaffold")}</button>
                  </div>
                  <div id="policyScaffoldPreview" class="policy-scaffold-preview"></div>
                </div>
                <label class="span-4 stack"><span>${t("catalog.policyLabel")}</span><input id="policyLabel" placeholder="${escapeHTML(t("catalog.policyLabelPlaceholder"))}"></label>
                <label class="span-4 stack"><span>${t("catalog.policyOperator")}</span><select id="policyOperator">${policyOperatorOptionsHTML()}</select></label>
                <label class="span-4 stack"><span>${t("catalog.policyReason")}</span><input id="policyReason" placeholder="${escapeHTML(t("catalog.policyReasonDefault"))}"></label>
                <label class="span-12 stack"><span>${t("catalog.policyExpression")}</span><textarea id="policyExpression" class="compact-textarea" placeholder="${examplePlaceholder('contains({{ref}}, "{{needle}}")')}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.policyDefaults")}</span><textarea id="policyDefaults" class="compact-textarea" placeholder="${examplePlaceholder(`ref=previous.raw_output
needle=critical`)}"></textarea></label>
                <label class="span-6 stack"><span>${t("catalog.policyParams")}</span><textarea id="policyParams" class="compact-textarea" placeholder="${escapeHTML(t("catalog.policyParamsPlaceholder"))}"></textarea></label>
                <div class="span-12 item muted">${t("catalog.policyRuleSaveHelp")}</div>
              </div>

              <label class="span-12 stack prompt-only hidden"><span>${t("catalog.extraRequirements")}</span><textarea id="resourceDetails" placeholder="${escapeHTML(t("catalog.extraRequirements"))}"></textarea></label>
            </div>
          </div>
          <div class="designer-actions">
            <button id="saveResource" class="primary">${t("catalog.saveResource")}</button>
            <button id="validateResource">${t("catalog.validateResource")}</button>
            <button id="openBuilder" class="primary">${t("catalog.openInPlayground")}</button>
            <button id="resetResource">${t("catalog.resetForm")}</button>
          </div>
          <div id="resourceOutput" class="mini-log resource-output" role="status" aria-live="polite"></div>
        </section>
      </div>
    </div>`;

  fillAgentSelect(root, runtime);
  fillProviderSelect(root, runtime, providerOptions);
  fillWorkflowTemplateSelects(root);
  fillWorkflowSchemaSelect(root);
  fillMetadataResourceSelects(root);
  fillToolScaffoldSelect(root);
  fillPolicyRuleScaffoldSelect(root);
  enhanceResourceDesignerUX(root);
  bindResourceCatalog(root, runtime, providerOptions);
  await focusCatalogTarget(root, runtime, providerOptions);
}

async function loadCatalogContext(runtime) {
  const configDiagnostics = await loadConfigDiagnosticsForCatalog();
  const resourceCapabilities = await loadResourceCapabilities();
  const providerOptions = await loadProviderOptions(runtime, resourceCapabilities);
  const toolResources = await loadToolResources(resourceCapabilities);
  const toolScaffolds = await loadToolScaffolds(resourceCapabilities);
  const policyRules = await loadPolicyRuleResources(resourceCapabilities);
  const teamTemplates = await loadTeamTemplateCatalog(resourceCapabilities);
  const kits = await loadKitResources(resourceCapabilities);
  const kitScaffolds = await loadKitScaffolds(resourceCapabilities);
  const policyRuleScaffolds = await loadPolicyRuleScaffolds(resourceCapabilities);
  const workflowGraphs = await loadWorkflowGraphsForCatalog(resourceCapabilities);
  const workflowTemplates = await loadWorkflowTemplateCatalog(resourceCapabilities);
  const workflowTemplateResources = await loadWorkflowTemplateResources(resourceCapabilities);
  const workflowOptions = await loadWorkflowOptionsForCatalog();
  const nodeMetadataResources = await loadWorkflowNodeMetadataResources(resourceCapabilities);
  const expressionHelperResources = await loadExpressionHelperResources(resourceCapabilities);
  const workflowSchemas = await loadWorkflowSchemaCatalog();
  const workflowSchemaResources = await loadWorkflowSchemaResources(resourceCapabilities);
  const nodeTypes = normalizeNodeTypeOptions(workflowOptions.node_types);
  const expressionHelpers = normalizeExpressionHelperOptions(workflowOptions.expression_functions);
  return {
    runtime,
    configDiagnostics,
    resourceCapabilities,
    providerOptions,
    toolResources,
    toolScaffolds,
    policyRules,
    teamTemplates,
    kits,
    kitScaffolds,
    policyRuleScaffolds,
    workflowGraphs,
    workflowTemplates,
    workflowTemplateResources,
    workflowOptions,
    workflowSchemas,
    workflowSchemaResources,
    nodeTypes,
    expressionHelpers,
    nodeMetadataResources,
    expressionHelperResources
  };
}

async function loadConfigDiagnosticsForCatalog() {
  try {
    return await request("/api/config/diagnostics?include_optional=0");
  } catch (error) {
    return { __error: localizedCatalogErrorMessage(error, t("catalog.loadFailed")), items: [] };
  }
}

function localizedCatalogErrorMessage(error, fallback = "") {
  const text = error?.message || (typeof error === "string" ? error : String(error || "")) || fallback;
  return resourceDisplayValue(text || fallback);
}

function renderResourceMetrics(runtime = catalogContext.runtime) {
  const providerOptions = catalogContext.providerOptions || [];
  const toolResources = catalogContext.toolResources || [];
  const policyRules = catalogContext.policyRules || [];
  const teamTemplates = catalogContext.teamTemplates || [];
  const kits = catalogContext.kits || [];
  const workflowTemplates = catalogContext.workflowTemplates || [];
  const workflowSchemaResources = catalogContext.workflowSchemaResources || [];
  const workflowSchemas = catalogContext.workflowSchemas || [];
  const nodeMetadataItems = catalogNodeMetadataItems();
  const expressionHelperItems = catalogExpressionHelperItems();
  return [
    resourceMetric(t("catalog.agents"), runtime?.agents?.length || 0, t("tour.catalog.agents.body")),
    resourceMetric(t("catalog.skills"), runtime?.skills?.length || 0, t("tour.catalog.skills.body")),
    resourceMetric(t("catalog.tools"), toolResources.length || runtime?.tools?.length || 0, t("tour.catalog.tools.body")),
    resourceMetric(t("catalog.teamTemplates"), teamTemplates.length, t("catalog.teamTemplatesHelp")),
    resourceMetric(t("catalog.kits"), kits.length, t("catalog.kitsHelp")),
    resourceMetric(t("catalog.workflowTemplates"), workflowTemplates.length, t("catalog.workflowTemplatesHelp")),
    resourceMetric(t("catalog.studioMetadata"), nodeMetadataItems.length + expressionHelperItems.length + workflowSchemaResources.length, t("catalog.studioMetadataHelp")),
    resourceMetric(t("catalog.workflowSchemas"), workflowSchemaResources.length || workflowSchemas.length, t("catalog.workflowSchemasHelp")),
    resourceMetric(t("catalog.providers"), providerOptions.length, t("catalog.providersHelp")),
    resourceMetric(t("catalog.policyRules"), policyRules.length, t("catalog.policyRulesHelp"))
  ].join("");
}

function renderResourceGuide() {
  const cards = [
    [t("catalog.guideKitTitle"), t("catalog.guideKitBody")],
    [t("catalog.guideAgentTitle"), t("catalog.guideAgentBody")],
    [t("catalog.guideSkillTitle"), t("catalog.guideSkillBody")],
    [t("catalog.guideToolTitle"), t("catalog.guideToolBody")],
    [t("catalog.guideWorkflowTitle"), t("catalog.guideWorkflowBody")],
    [t("catalog.guideMetadataTitle"), t("catalog.guideMetadataBody")]
  ];
  return `<div class="resource-guide-head">
    <div>
      <p class="eyebrow">${t("catalog.resourceGuideEyebrow")}</p>
      <h2>${t("catalog.resourceGuideTitle")}</h2>
      <p class="muted">${t("catalog.resourceGuideHelp")}</p>
    </div>
  </div>
  <div class="resource-guide-grid">
    ${cards.map(([title, body]) => `<article class="resource-guide-card"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(body)}</span></article>`).join("")}
  </div>`;
}

function renderResourceStarter(kits = [], scaffolds = []) {
  const presets = sortKitScaffolds(Array.isArray(scaffolds) ? scaffolds : []);
  const starter = presets.find(item => kitScaffoldName(item) === "multi-domain-agent") || presets[0] || null;
  const savedStarter = Array.isArray(kits) ? kits.find(item => String(item?.name || "").includes("multi-domain")) : null;
  const starterName = starter ? kitScaffoldName(starter) : "multi-domain-agent";
  const workflow = starter?.recommended_workflow || savedStarter?.recommended_workflow || "multi-domain-intake-router";
  const agent = starter?.recommended_agent || savedStarter?.recommended_agent || "chat";
  const resources = starter ? kitScaffoldResourceCounts(starter) : [];
  const example = Array.isArray(starter?.examples) ? starter.examples.find(item => item?.request || item?.title) : null;
  const steps = [
    {
      label: t("catalog.starterStepChoose"),
      title: t("catalog.starterChooseTitle"),
      body: starter ? localizedText(starter.description || t("catalog.kitDescriptionDefault")) : t("catalog.starterChooseBody")
    },
    {
      label: t("catalog.starterStepCreate"),
      title: t("catalog.starterCreateTitle"),
      body: t("catalog.starterCreateBody")
    },
    {
      label: t("catalog.starterStepRun"),
      title: t("catalog.starterRunTitle"),
      body: t("catalog.starterRunBody", { workflow, agent })
    }
  ];
  return `<div class="resource-starter-head">
    <div>
      <p class="eyebrow">${escapeHTML(t("catalog.starterEyebrow"))}</p>
      <h2>${escapeHTML(t("catalog.starterTitle"))}</h2>
      <p class="muted">${escapeHTML(t("catalog.starterHelp"))}</p>
    </div>
    <div class="resource-starter-actions">
      ${starter ? `<button class="primary" data-kit-scaffold="${escapeHTML(starterName)}" data-force-materialize="true">${escapeHTML(t("catalog.starterCreateFull"))}</button>` : ""}
      <button data-scroll-resource="#resourceKitScaffolds">${escapeHTML(t("catalog.starterBrowseKits"))}</button>
      <button data-open-view="workflows">${escapeHTML(t("catalog.starterOpenWorkflowStudio"))}</button>
    </div>
  </div>
  <div class="resource-starter-body">
    <div class="resource-starter-steps">
      ${steps.map((step, index) => `<article class="resource-starter-step">
        <span>${escapeHTML(step.label)}</span>
        <strong>${escapeHTML(step.title)}</strong>
        <p>${escapeHTML(step.body)}</p>
        ${index === 0 && resources.length ? `<div class="resource-starter-links">${resources.map(item => `<small>${escapeHTML(item.label)} <b>${escapeHTML(String(item.count))}</b></small>`).join("")}</div>` : ""}
      </article>`).join("")}
    </div>
    <aside class="resource-starter-example">
      <small>${escapeHTML(t("catalog.starterExample"))}</small>
      <strong>${escapeHTML(localizedText(example?.title || workflow))}</strong>
      <p>${escapeHTML(localizedText(example?.description || t("catalog.starterExampleHelp")))}</p>
      <code>${escapeHTML(localizedText(example?.request || t("catalog.starterExampleRequest")))}</code>
    </aside>
  </div>`;
}

function renderResourceRelationMap(runtime = catalogContext.runtime || {}) {
  const counts = {
    kits: (catalogContext.kits || []).length + (catalogContext.kitScaffolds || []).length,
    agents: runtime?.agents?.length || 0,
    skills: runtime?.skills?.length || 0,
    tools: (catalogContext.toolResources || []).length || runtime?.tools?.length || 0,
    workflows: (catalogContext.workflowTemplates || []).length + (catalogContext.workflowGraphs || []).length,
    teams: (catalogContext.teamTemplates || []).length,
    policies: (catalogContext.policyRules || []).length
  };
  const rows = [
    { from: t("catalog.resourceKit"), to: t("catalog.relationEverything"), body: t("catalog.relationKitBody"), count: counts.kits },
    { from: t("catalog.resourceAgent"), to: t("catalog.resourceSkill"), body: t("catalog.relationAgentSkillBody"), count: counts.agents },
    { from: t("catalog.resourceSkill"), to: t("catalog.resourceTool"), body: t("catalog.relationSkillToolBody"), count: counts.skills },
    { from: t("catalog.resourceWorkflow"), to: t("catalog.relationExecution"), body: t("catalog.relationWorkflowBody"), count: counts.workflows },
    { from: t("catalog.resourceTeamTemplate"), to: t("catalog.relationCollaboration"), body: t("catalog.relationTeamBody"), count: counts.teams },
    { from: t("catalog.resourcePolicyRule"), to: t("catalog.relationQuality"), body: t("catalog.relationPolicyBody"), count: counts.policies }
  ];
  return `<div class="resource-relation-head">
    <div>
      <p class="eyebrow">${escapeHTML(t("catalog.relationEyebrow"))}</p>
      <h2>${escapeHTML(t("catalog.relationTitle"))}</h2>
      <p class="muted">${escapeHTML(t("catalog.relationHelp"))}</p>
    </div>
  </div>
  <div class="resource-relation-flow">
    ${rows.map(row => `<article class="resource-relation-card">
      <span>${escapeHTML(String(row.count))}</span>
      <strong>${escapeHTML(row.from)} <em>${escapeHTML(t("catalog.relationArrow"))}</em> ${escapeHTML(row.to)}</strong>
      <p>${escapeHTML(row.body)}</p>
    </article>`).join("")}
  </div>
  <details class="resource-override-paths">
    <summary>
      <div>
        <strong>${escapeHTML(t("catalog.overridePathsTitle"))}</strong>
        <p>${escapeHTML(t("catalog.overridePathsHelp"))}</p>
      </div>
      <span>${escapeHTML(t("catalog.overridePathsSummary", { count: resourceOverridePathItems().length }))}</span>
    </summary>
    <div>
      ${resourceOverridePathItems().map(item => `<span><small>${escapeHTML(item.label)}</small><strong>${escapeHTML(item.hint)}</strong></span>`).join("")}
    </div>
  </details>`;
}

function resourceOverridePathItems() {
  return [
    { label: t("catalog.resourceProvider"), hint: t("catalog.overridePathProviderHint") },
    { label: t("catalog.resourceAgent"), hint: t("catalog.overridePathAgentHint") },
    { label: t("catalog.resourceSkill"), hint: t("catalog.overridePathSkillHint") },
    { label: t("catalog.resourceTool"), hint: t("catalog.overridePathToolHint") },
    { label: t("catalog.resourceWorkflow"), hint: t("catalog.overridePathWorkflowHint") },
    { label: t("catalog.resourceWorkflowTemplate"), hint: t("catalog.overridePathWorkflowTemplateHint") },
    { label: t("catalog.resourceTeamTemplate"), hint: t("catalog.overridePathTeamTemplateHint") },
    { label: t("catalog.resourcePolicyRule"), hint: t("catalog.overridePathPolicyHint") },
    { label: t("catalog.resourceKit"), hint: t("catalog.overridePathKitHint") },
    { label: t("catalog.kitScaffolds"), hint: t("catalog.overridePathMaterializedHint") }
  ];
}

function catalogNodeMetadataItems() {
  return mergeMetadataCatalogItems(
    catalogContext.nodeMetadataResources || [],
    catalogContext.nodeTypes || [],
    item => item?.type || item?.name || "",
    item => ({ ...item, source: item?.source || "built_in", custom: item?.custom === true })
  );
}

function catalogExpressionHelperItems() {
  return mergeMetadataCatalogItems(
    catalogContext.expressionHelperResources || [],
    catalogContext.expressionHelpers || [],
    item => item?.name || "",
    item => ({ ...item, source: item?.source || "built_in", custom: item?.custom === true })
  );
}

function mergeMetadataCatalogItems(saved = [], discovered = [], keyFn, discoveredShape) {
  const byKey = new Map();
  for (const item of Array.isArray(saved) ? saved : []) {
    const key = String(keyFn(item) || "").trim();
    if (!key) continue;
    byKey.set(key, { ...item, custom: item?.custom !== false, source: item?.source || "custom" });
  }
  for (const item of Array.isArray(discovered) ? discovered : []) {
    const key = String(keyFn(item) || "").trim();
    if (!key || byKey.has(key)) continue;
    byKey.set(key, discoveredShape(item));
  }
  return [...byKey.values()].sort((a, b) => {
    const sourceRank = metadataSourceRank(a) - metadataSourceRank(b);
    if (sourceRank) return sourceRank;
    return String(keyFn(a)).localeCompare(String(keyFn(b)), undefined, { sensitivity: "base" });
  });
}

function metadataSourceRank(item = {}) {
  if (item.custom || item.path) return 0;
  if (String(item.source || "").includes("custom")) return 0;
  return 1;
}

async function refreshCatalogResources(root) {
  const runtime = catalogContext.runtime || {};
  const viewState = captureCatalogViewState(root);
  invalidateResourceCapabilities();
  catalogContext = await loadCatalogContext(runtime);
  publishResourceCatalogChanged();
  publishCatalogConfigDiagnostics();
  setHTML(root, "#resourceMetrics", renderResourceMetrics(runtime));
  setHTMLIfChanged(root, "#resourceToolScaffolds", renderToolScaffoldStrip(catalogContext.toolScaffolds || []));
  toggleHidden(root, "#resourceToolScaffolds", !(catalogContext.toolScaffolds || []).length);
  setHTMLIfChanged(root, "#resourceToolsList", renderToolList(runtime));
  updateText(root, "#resourceTeamTemplatesCount", String(catalogContext.teamTemplates.length));
  setHTMLIfChanged(root, "#resourceTeamTemplatesList", catalogContext.teamTemplates.map(renderTeamTemplate).join("") || empty(t("catalog.noTeamTemplates"), { actionLabel: t("catalog.newTeamTemplate"), action: "team-template" }));
  updateText(root, "#resourceKitsCount", String(catalogContext.kits.length));
  setHTMLIfChanged(root, "#resourceKitScroll", renderKitResourceSections(catalogContext.kits, catalogContext.kitScaffolds));
  setHTMLIfChanged(root, "#resourcePolicyRuleScroll", renderPolicyRuleResourceSections(catalogContext.policyRules, catalogContext.policyRuleScaffolds));
  updateText(root, "#resourceWorkflowTemplatesCount", String(catalogContext.workflowTemplates.length));
  setHTMLIfChanged(root, "#resourceWorkflowTemplatesList", catalogContext.workflowTemplates.map(renderWorkflowTemplate).join("") || empty(t("catalog.noWorkflowTemplates"), { actionLabel: t("catalog.newWorkflowTemplate"), action: "workflow-template" }));
  updateText(root, "#resourceWorkflowSchemasCount", String(catalogContext.workflowSchemaResources.length));
  setHTMLIfChanged(root, "#resourceObservedWorkflowSchemasList", catalogContext.workflowSchemas.map(renderObservedWorkflowSchema).join("") || empty(t("catalog.noObservedWorkflowSchemas"), { actionLabel: t("catalog.openRun"), view: "playground" }));
  setHTMLIfChanged(root, "#resourceWorkflowSchemasList", catalogContext.workflowSchemaResources.map(renderWorkflowSchemaResource).join("") || empty(t("catalog.noWorkflowSchemas"), { actionLabel: t("catalog.newWorkflowSchema"), action: "workflow-schema" }));
  const nodeMetadataItems = catalogNodeMetadataItems();
  const expressionHelperItems = catalogExpressionHelperItems();
  updateText(root, "#resourceNodeMetadataCount", String(nodeMetadataItems.length));
  setHTMLIfChanged(root, "#resourceNodeMetadataList", nodeMetadataItems.map(renderNodeMetadataResource).join("") || empty(t("catalog.noNodeMetadata"), { actionLabel: t("catalog.newNodeMetadata"), action: "node-metadata" }));
  updateText(root, "#resourceExpressionHelpersCount", String(expressionHelperItems.length));
  setHTMLIfChanged(root, "#resourceExpressionHelpersList", expressionHelperItems.map(renderExpressionHelperResource).join("") || empty(t("catalog.noExpressionHelpers"), { actionLabel: t("catalog.newExpressionHelper"), action: "expression-helper" }));
  updateText(root, "#resourceProvidersCount", String(catalogContext.providerOptions.length));
  setHTMLIfChanged(root, "#resourceProvidersList", catalogContext.providerOptions.map(renderProvider).join("") || empty(t("catalog.noProviders"), { actionLabel: t("catalog.newProvider"), action: "provider" }));
  updateText(root, "#resourcePolicyRulesCount", String(catalogContext.policyRules.length));
  fillWorkflowTemplateSelects(root);
  fillWorkflowSchemaSelect(root);
  fillMetadataResourceSelects(root);
  fillToolScaffoldSelect(root);
  fillPolicyRuleScaffoldSelect(root);
  bindResourceCatalog(root, runtime, catalogContext.providerOptions);
  applyResourceFilters(root);
  applyConfigDiagnosticsToDesigner(root);
  restoreCatalogViewState(root, viewState);
  renderResourceDependencyPickers(root);
}

function publishCatalogConfigDiagnostics() {
  if (!catalogContext.configDiagnostics) return;
  window.dispatchEvent(new CustomEvent("goflow:config-diagnostics", { detail: catalogContext.configDiagnostics }));
}

function publishResourceCatalogChanged() {
  window.dispatchEvent(new CustomEvent("goflow:resources-changed", {
    detail: {
      cache_key: catalogContext.resourceCapabilities?.find(item => item?.catalog_cache_key)?.catalog_cache_key || ""
    }
  }));
}

function setHTML(root, selector, html) {
  const node = root.querySelector(selector);
  if (node) node.innerHTML = html;
}

function setHTMLIfChanged(root, selector, html) {
  const node = root.querySelector(selector);
  if (node && node.innerHTML !== html) node.innerHTML = html;
}

function toggleHidden(root, selector, hidden) {
  const node = root.querySelector(selector);
  node?.classList.toggle("hidden", Boolean(hidden));
}

function updateText(root, selector, text) {
  const node = root.querySelector(selector);
  if (node) node.textContent = text;
}

function captureCatalogViewState(root) {
  const active = document.activeElement instanceof HTMLElement && root.contains(document.activeElement)
    ? document.activeElement
    : null;
  const fieldValues = {};
  root.querySelectorAll("#resourceDesigner input, #resourceDesigner textarea, #resourceDesigner select").forEach(node => {
    if (!node.id) return;
    if (node.type === "checkbox") {
      fieldValues[node.id] = { checked: Boolean(node.checked) };
    } else {
      fieldValues[node.id] = {
        value: node.value,
        selectionStart: typeof node.selectionStart === "number" ? node.selectionStart : null,
        selectionEnd: typeof node.selectionEnd === "number" ? node.selectionEnd : null
      };
    }
  });
  const scrollSelectors = [
    "#resourceToolScroll",
    "#resourceToolsList",
    "#resourceToolScaffolds",
    "#resourceTeamTemplatesList",
    "#resourceKitScroll",
    "#resourceKitsList",
    "#resourcePolicyRuleScroll",
    "#resourcePolicyRuleScaffolds",
    "#resourceWorkflowTemplatesList",
    "#resourceWorkflowSchemaScroll",
    "#resourceObservedWorkflowSchemasList",
    "#resourceWorkflowSchemasList",
    "#resourceNodeMetadataList",
    "#resourceExpressionHelpersList",
    "#resourceProvidersList",
    "#resourcePolicyRulesList",
    ".designer-body",
    "#resourceOutput"
  ];
  return {
    activeID: active?.id || "",
    fieldValues,
    scroll: scrollSelectors.map(selector => {
      const node = root.querySelector(selector);
      return node ? { selector, top: node.scrollTop || 0, left: node.scrollLeft || 0 } : null;
    }).filter(Boolean)
  };
}

function restoreCatalogViewState(root, state) {
  if (!state) return;
  Object.entries(state.fieldValues || {}).forEach(([id, value]) => {
    const node = root.querySelector(`#${escapeSelectorID(id)}`);
    if (!node) return;
    if ("checked" in value) {
      node.checked = Boolean(value.checked);
    } else if (node.tagName === "SELECT") {
      if ([...node.options].some(option => option.value === value.value)) node.value = value.value;
    } else {
      node.value = value.value || "";
      if (typeof value.selectionStart === "number" && typeof node.setSelectionRange === "function") {
        try {
          node.setSelectionRange(value.selectionStart, value.selectionEnd ?? value.selectionStart);
        } catch {
          // Some inputs do not support selection ranges.
        }
      }
    }
  });
  syncSkillScriptsFromHidden(root);
  for (const item of state.scroll || []) {
    const node = root.querySelector(item.selector);
    if (!node) continue;
    node.scrollTop = Math.min(item.top || 0, Math.max(0, node.scrollHeight - node.clientHeight));
    node.scrollLeft = Math.min(item.left || 0, Math.max(0, node.scrollWidth - node.clientWidth));
  }
  const active = state.activeID ? root.querySelector(`#${escapeSelectorID(state.activeID)}`) : null;
  if (active && typeof active.focus === "function") active.focus({ preventScroll: true });
  requestAnimationFrame(() => {
    for (const item of state.scroll || []) {
      const node = root.querySelector(item.selector);
      if (!node) continue;
      node.scrollTop = Math.min(item.top || 0, Math.max(0, node.scrollHeight - node.clientHeight));
      node.scrollLeft = Math.min(item.left || 0, Math.max(0, node.scrollWidth - node.clientWidth));
    }
  });
}

function escapeSelectorID(id) {
  if (window.CSS?.escape) return CSS.escape(id);
  return String(id || "").replace(/["\\#.;:[\],>+~*'=|^$(){}\s]/g, "\\$&");
}

function renderResourceSaveOutcome(root, options = {}) {
  if (options.resourceChanged !== false) {
    invalidateResourceCapabilities();
    publishResourceCatalogChanged();
  }
  const output = root.querySelector("#resourceOutput");
  if (!output) return;
  const tone = options.tone || "good";
  const facts = Array.isArray(options.facts) ? options.facts.filter(item => item?.value) : [];
  const paths = [
    options.path ? { label: t("catalog.saveOutcomePath"), value: options.path } : null,
    ...(Array.isArray(options.paths) ? options.paths : [])
  ].filter(item => item?.value);
  const actions = Array.isArray(options.actions) ? options.actions : [];
  const sections = Array.isArray(options.sections) ? options.sections.filter(Boolean) : [];
  const factHTML = [...facts, ...paths].map(item => `
    <span class="resource-save-fact">
      <small>${escapeHTML(item.label || "")}</small>
      <strong>${escapeHTML(resourceDisplayValue(item.value))}</strong>
    </span>
  `).join("");
  const sectionHTML = sections.map(section => `
    <section class="resource-save-section">
      <div class="resource-save-section-head">
        <strong>${escapeHTML(localizedText(section.title || ""))}</strong>
        ${section.badge ? `<span>${escapeHTML(localizedText(section.badge))}</span>` : ""}
      </div>
      ${section.body ? `<p>${escapeHTML(localizedText(section.body))}</p>` : ""}
      ${section.html || ""}
    </section>
  `).join("");
  const actionHTML = actions.map(action => `
    <button type="button" class="${action.primary ? "primary" : ""}" data-resource-output-action="${escapeHTML(action.action || "")}" data-resource-output-value="${escapeHTML(action.value || "")}">
      ${escapeHTML(localizedText(action.label || ""))}
    </button>
  `).join("");
  output.innerHTML = `
    <div class="resource-save-outcome ${escapeHTML(tone)}">
      <div class="resource-save-status">
        <span class="resource-save-dot" aria-hidden="true"></span>
        <div>
          <strong>${escapeHTML(localizedText(options.title || t("catalog.saveOutcomeReadyTitle")))}</strong>
          <p>${escapeHTML(localizedText(options.body || t("catalog.saveOutcomeReadyBody")))}</p>
        </div>
      </div>
      ${factHTML ? `<div class="resource-save-facts">${factHTML}</div>` : ""}
      ${sectionHTML}
      ${actionHTML ? `<div class="resource-save-actions">${actionHTML}</div>` : ""}
    </div>
  `;
  output.querySelectorAll("[data-resource-output-action]").forEach(button => {
    button.onclick = () => {
      const action = button.dataset.resourceOutputAction;
      const value = button.dataset.resourceOutputValue || "";
      if (action === "settings") {
        try {
          sessionStorage.setItem("goflow.settings.focus", "config-health");
        } catch {
          // Ignore storage restrictions; navigation still works.
        }
        location.hash = "settings";
      } else if (action === "workflows") {
        if (value) localStorage.setItem("goflow.workflow.open", value);
        location.hash = "workflows";
      }
    };
  });
}

function resourceSaveOutcomeOptions(kind, result = {}, options = {}) {
  const apply = resourceSaveApplySummary(kind, result, options);
  const useApplyStatus = apply.hasBackendMetadata && options.useApplyStatus !== false;
  const facts = [
    ...(Array.isArray(options.facts) ? options.facts : []),
    ...apply.facts
  ];
  const paths = [
    ...(Array.isArray(options.paths) ? options.paths : []),
    ...apply.paths
  ];
  const actions = mergeResourceSaveActions([
    ...(Array.isArray(options.actions) ? options.actions : []),
    ...apply.actions
  ]);
  return {
    ...options,
    tone: useApplyStatus ? apply.tone : (options.tone || apply.tone),
    title: useApplyStatus ? apply.title : (options.title || apply.title),
    body: useApplyStatus ? apply.body : (options.body || apply.body),
    path: options.path || result?.path || "",
    facts,
    paths,
    actions
  };
}

function renderBackendResourceSaveOutcome(root, kind, result = {}, options = {}) {
  renderResourceSaveOutcome(root, resourceSaveOutcomeOptions(kind, result, options));
}

function resourceSaveApplySummary(kind, result = {}, options = {}) {
  const capability = resourceCapability(catalogContext.resourceCapabilities, kind);
  const hasBackendMetadata = resourceSaveHasBackendMetadata(result);
  const applyStateRaw = stringValue(
    result?.apply_state ??
    result?.applyState ??
    (hasBackendMetadata ? "" : capability?.apply_state_on_save)
  );
  const applyState = normalizeResourceApplyState(applyStateRaw);
  const restartRequired = result?.restart_required === true ||
    applyState === "restart_required" ||
    (!hasBackendMetadata && (capability?.restart_required_on_save || capability?.apply_state_on_save === "restart_required"));
  const hotReloadSupported = result?.hot_reload_supported === true ||
    (!hasBackendMetadata && Boolean(capability?.hot_reload_supported));
  const applyMessage = stringValue(result?.apply_message || result?.applyMessage);
  const diagnosticsPath = stringValue(result?.diagnostics_path || result?.diagnosticsPath || capability?.diagnostics_path);
  const shouldOpenSettings = restartRequired || Boolean(diagnosticsPath && hasBackendMetadata);
  let tone = hasBackendMetadata ? "good" : (options.tone || "good");
  let title = hasBackendMetadata ? t("catalog.saveOutcomeReadyTitle") : (options.title || t("catalog.saveOutcomeReadyTitle"));
  let body = hasBackendMetadata ? (applyMessage || t("catalog.saveOutcomeReadyBody")) : (options.body || t("catalog.saveOutcomeReadyBody"));

  if (restartRequired) {
    tone = "warn";
    title = t("catalog.saveOutcomeRestartTitle");
    body = applyMessage || options.restartBody || t("catalog.saveOutcomeRestartBody");
  } else if (applyState === "refresh_required") {
    tone = "warn";
    title = t("catalog.saveOutcomeRefreshTitle");
    body = applyMessage || t("catalog.saveOutcomeRefreshBody");
  } else if (applyState === "hot_reload" || hotReloadSupported) {
    tone = "good";
    title = t("catalog.saveOutcomeHotReloadTitle");
    body = applyMessage || t("catalog.saveOutcomeHotReloadBody");
  } else if (applyState === "active_metadata") {
    tone = "good";
    title = t("catalog.saveOutcomeMetadataTitle");
    body = applyMessage || t("catalog.saveOutcomeMetadataBody");
  } else if (applyState === "active") {
    tone = "good";
    title = t("catalog.saveOutcomeActiveTitle");
    body = applyMessage || t("catalog.saveOutcomeActiveBody");
  } else if (applyMessage && hasBackendMetadata) {
    body = applyMessage;
  }

  const facts = [
    applyState ? { label: t("catalog.saveOutcomeApplyState"), value: resourceApplyStateLabel(applyStateRaw || applyState) } : null,
    Object.prototype.hasOwnProperty.call(result || {}, "hot_reload_supported")
      ? { label: t("catalog.saveOutcomeHotReload"), value: result.hot_reload_supported ? t("common.yes") : t("common.no") }
      : null
  ].filter(Boolean);
  const paths = diagnosticsPath ? [{ label: t("catalog.saveOutcomeDiagnostics"), value: diagnosticsPath }] : [];
  const actions = [];
  if ((shouldOpenSettings || options.includeSettingsAction === true && !hasBackendMetadata) && !options.suppressSettingsAction) {
    actions.push({ action: "settings", label: t("catalog.saveOutcomeOpenSettings"), primary: restartRequired });
  }
  return {
    hasBackendMetadata,
    tone,
    title,
    body,
    facts,
    paths,
    actions
  };
}

function resourceSaveHasBackendMetadata(result = {}) {
  return ["restart_required", "apply_state", "applyState", "apply_message", "applyMessage", "hot_reload_supported", "diagnostics_path", "diagnosticsPath"]
    .some(key => Object.prototype.hasOwnProperty.call(result || {}, key));
}

function normalizeResourceApplyState(value) {
  const text = stringValue(value).toLowerCase().replace(/[\s-]+/g, "_");
  if (!text) return "";
  if (text.includes("restart")) return "restart_required";
  if (text.includes("refresh")) return "refresh_required";
  if (text.includes("hot") && text.includes("reload")) return "hot_reload";
  if (text.includes("metadata")) return "active_metadata";
  if (text.includes("active") || text.includes("applied") || text.includes("ready")) return "active";
  return text;
}

function resourceApplyStateLabel(value) {
  const normalized = normalizeResourceApplyState(value);
  const key = normalized ? `catalog.applyState.${normalized}` : "";
  if (key) {
    const translated = t(key);
    if (translated !== key) return translated;
  }
  return localizedText(value || normalized || "");
}

function mergeResourceSaveActions(actions = []) {
  const seen = new Set();
  return actions.filter(action => {
    if (!action?.action && !action?.label) return false;
    const key = `${action.action || ""}:${action.value || ""}:${action.label || ""}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function stringValue(value) {
  return String(value ?? "").trim();
}

function resourceMetric(label, value, detail) {
  return `<div class="resource-metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(String(value))}</strong><small>${escapeHTML(detail)}</small></div>`;
}

function bindResourceCatalog(root, runtime, providerOptions) {
  const search = root.querySelector("#resourceSearch");
  const group = root.querySelector("#resourceGroupFilter");
  const clearFilters = root.querySelector("#resourceClearFilters");
  if (search) search.oninput = () => scheduleResourceFilters(root);
  if (group) group.onchange = () => scheduleResourceFilters(root);
  if (clearFilters) {
    clearFilters.onclick = () => {
      if (search) search.value = "";
      if (group) group.value = "recommended";
      applyResourceFilters(root);
      search?.focus();
    };
  }
  root.querySelectorAll("[data-resource-advanced-toggle]").forEach(button => {
    button.onclick = () => {
      if (search) search.value = "";
      if (group) group.value = "advanced";
      applyResourceFilters(root);
      root.querySelector("[data-resource-panel][data-resource-group='advanced']")?.scrollIntoView({
        block: "start",
        behavior: prefersReducedMotion() ? "auto" : "smooth"
      });
    };
  });
  root.querySelectorAll("[data-new-resource]").forEach(button => {
    button.onclick = () => openDesigner(root, runtime, button.dataset.newResource, null, providerOptions);
  });
  root.querySelectorAll("[data-edit-resource]").forEach(button => {
    button.onclick = () => openDesigner(root, runtime, button.dataset.resourceType, button.dataset.resourceName, providerOptions);
  });
  root.querySelectorAll("[data-delete-team-template]").forEach(button => {
    button.onclick = () => deleteTeamTemplate(root, button.dataset.deleteTeamTemplate);
  });
  root.querySelectorAll("[data-delete-kit]").forEach(button => {
    button.onclick = () => deleteKit(root, button.dataset.deleteKit);
  });
  root.querySelectorAll("[data-resource-action]").forEach(button => {
    button.onclick = () => handleCatalogResourceAction(root, button);
  });
  bindResourceCapabilityActionDelegation(root);
  root.querySelectorAll("[data-export-kit]").forEach(button => {
    button.onclick = () => exportKitBundle(button.dataset.exportKit);
  });
  root.querySelectorAll("[data-kit-scaffold]").forEach(button => {
    button.onclick = () => createKitFromScaffold(root, button.dataset.kitScaffold, button);
  });
  root.querySelectorAll("[data-scroll-resource]").forEach(button => {
    button.onclick = () => scrollCatalogResourceTarget(root, button.dataset.scrollResource);
  });
  root.querySelectorAll("[data-kit-focus-kind]").forEach(button => {
    button.onclick = () => focusKitResource(root, button.dataset.kitFocusKind, button.dataset.kitFocusName);
  });
  root.querySelectorAll("[data-open-kit-workflow]").forEach(button => {
    button.onclick = () => openKitWorkflow(button.dataset.openKitWorkflow);
  });
  root.querySelectorAll("[data-open-view]").forEach(button => {
    button.onclick = () => {
      const view = String(button.dataset.openView || "").trim();
      if (view) location.hash = view;
    };
  });
  root.querySelectorAll("[data-tool-scaffold]").forEach(button => {
    button.onclick = () => openToolScaffoldDesigner(root, runtime, button.dataset.toolScaffold, providerOptions);
  });
  root.querySelectorAll("[data-policy-rule-scaffold]").forEach(button => {
    button.onclick = () => openPolicyRuleScaffoldDesigner(root, runtime, button.dataset.policyRuleScaffold, providerOptions);
  });
  root.querySelectorAll("[data-import-kit-bundle]").forEach(button => {
    button.onclick = () => root.querySelector("#kitBundleImportFile")?.click();
  });
  root.querySelector("#kitBundleImportFile").onchange = event => importKitBundle(root, event.target.files?.[0]);
  root.querySelectorAll("[data-import-workflow-schema-catalog]").forEach(button => {
    button.onclick = () => root.querySelector("#workflowSchemaImportFile")?.click();
  });
  root.querySelectorAll("[data-export-workflow-schema-catalog]").forEach(button => {
    button.onclick = () => exportWorkflowSchemaCatalog();
  });
  root.querySelector("#workflowSchemaImportFile").onchange = event => importWorkflowSchemaCatalog(root, event.target.files?.[0]);
  root.querySelectorAll("[data-delete-workflow-template]").forEach(button => {
    button.onclick = () => deleteWorkflowTemplateResource(root, button.dataset.deleteWorkflowTemplate);
  });
  root.querySelectorAll("[data-capture-workflow-schema]").forEach(button => {
    button.onclick = () => captureWorkflowSchemaResource(root, button.dataset.captureWorkflowSchema, null, button);
  });
  root.querySelectorAll("[data-activate-workflow-schema]").forEach(button => {
    button.onclick = () => activateWorkflowSchemaResource(root, button.dataset.activateWorkflowSchema, null, button);
  });
  root.querySelectorAll("[data-delete-workflow-schema]").forEach(button => {
    button.onclick = () => deleteWorkflowSchemaResource(root, button.dataset.deleteWorkflowSchema);
  });
  root.querySelectorAll("[data-delete-node-metadata]").forEach(button => {
    button.onclick = () => deleteWorkflowNodeMetadata(root, button.dataset.deleteNodeMetadata);
  });
  root.querySelectorAll("[data-delete-expression-helper]").forEach(button => {
    button.onclick = () => deleteExpressionHelper(root, button.dataset.deleteExpressionHelper);
  });
  root.querySelectorAll("[data-close-designer]").forEach(button => {
    button.onclick = () => closeDesigner(root);
  });
  root.querySelectorAll("[data-designer-section]").forEach(button => {
    button.onclick = () => scrollDesignerSection(root, button.dataset.designerSection || "identity");
  });
  root.querySelector("#resourceType").onchange = () => {
    syncDesignerMode(root);
    applyConfigDiagnosticsToDesigner(root);
  };
  root.querySelectorAll("[data-resource-brief-field]").forEach(input => {
    input.addEventListener("input", () => updateResourceBriefPreview(root));
    input.addEventListener("change", () => updateResourceBriefPreview(root));
  });
  root.querySelector("#resourceBriefApply").onclick = () => applyResourceBrief(root);
  bindSkillScriptEditor(root);
  bindResourceDependencyPickers(root);
  bindResourceCapabilityRefresh(root);
  root.querySelector("#validateResource").onclick = () => validateResourceDraft(root, { manual: true });
  root.querySelector("#workflowTemplateSaveMode").onchange = () => handleWorkflowTemplateModeChange(root);
  root.querySelector("#workflowTemplateSource").onchange = () => applyWorkflowTemplateSource(root);
  root.querySelector("#workflowTemplateCaptureSource").onchange = () => applyWorkflowTemplateCaptureSource(root);
  root.querySelector("#workflowSchemaSource").onchange = () => previewWorkflowSchemaSource(root);
  root.querySelector("#applyWorkflowSchemaSource").onclick = () => applyWorkflowSchemaSource(root);
  root.querySelector("#nodeMetadataType").onchange = () => applyNodeMetadataSource(root);
  root.querySelector("#expressionHelperSource").onchange = () => applyExpressionHelperSource(root);
  root.querySelector("#toolScaffoldPreset").onchange = () => previewToolScaffold(root);
  root.querySelector("#applyToolScaffold").onclick = () => applyToolScaffold(root);
  root.querySelector("#createToolScaffold").onclick = () => createToolFromScaffold(root);
  root.querySelector("#policyScaffoldPreset").onchange = () => previewPolicyRuleScaffold(root);
  root.querySelector("#applyPolicyRuleScaffold").onclick = () => applyPolicyRuleScaffold(root);
  root.querySelector("#createPolicyRuleScaffold").onclick = () => createPolicyRuleFromScaffold(root);
  root.querySelector("#saveResource").onclick = () => saveResource(root);
  root.querySelector("#resetResource").onclick = () => openDesigner(root, runtime, root.querySelector("#resourceType").value, null, providerOptions);
  root.querySelector("#toolCodeFile").onchange = event => loadToolCodeFile(root, event);
  bindToolIsolationHints(root);
  root.querySelector("#openBuilder").onclick = () => {
    const command = buildResourceRequest(collectResourceDraft(root));
    localStorage.setItem("goflow.playground.draft", command);
    location.hash = "playground";
  };
  applyResourceFilters(root);
}

function enhanceResourceDesignerUX(root) {
  const body = root.querySelector("#resourceDesigner .designer-body .grid");
  if (!body || body.dataset.resourceUxEnhanced === "true") return;
  body.dataset.resourceUxEnhanced = "true";
  const firstLabel = root.querySelector("#resourceType")?.closest("label");
  const descriptionLabel = root.querySelector("#resourceDescription")?.closest("label");
  const capability = root.querySelector("#resourceCapabilitySummary");
  const brief = root.querySelector("#resourceBriefPanel");
  const resourceDetails = root.querySelector("#resourceDetails")?.closest("label");
  if (firstLabel) {
    const basics = document.createElement("section");
    basics.className = "span-12 resource-designer-section resource-basics-section";
    basics.dataset.designerSectionPanel = "identity";
    basics.innerHTML = `<div class="resource-section-head">
      <span>${escapeHTML(t("catalog.quickStartEyebrow"))}</span>
      <strong>${escapeHTML(t("catalog.quickStartTitle"))}</strong>
      <p>${escapeHTML(t("catalog.quickStartHelp"))}</p>
    </div>
    <div class="resource-basics-grid"></div>`;
    body.insertBefore(basics, firstLabel);
    const grid = basics.querySelector(".resource-basics-grid");
    [
      firstLabel,
      root.querySelector("#resourceName")?.closest("label"),
      root.querySelector("#resourcePurpose")?.closest("label"),
      descriptionLabel
    ].filter(Boolean).forEach(node => grid.append(node));
  }
  if (capability) capability.dataset.designerSectionPanel = "identity";
  if (brief) brief.dataset.designerSectionPanel = "brief";
  if (resourceDetails) resourceDetails.dataset.resourceAdvanced = "developer";
  wrapDesignerTypeSection(root, ".skill-only", "skill");
  wrapDesignerTypeSection(root, ".agent-only", "agent");
  wrapDesignerTypeSection(root, ".tool-only", "tool");
  wrapDesignerTypeSection(root, ".workflow-only", "workflow");
  wrapDesignerTypeSection(root, ".workflow-template-only", "workflow-template");
  wrapDesignerTypeSection(root, ".workflow-schema-only", "workflow-schema");
  wrapDesignerTypeSection(root, ".node-metadata-only", "node-metadata");
  wrapDesignerTypeSection(root, ".expression-helper-only", "expression-helper");
  wrapDesignerTypeSection(root, ".team-template-only", "team-template");
  wrapDesignerTypeSection(root, ".kit-only", "kit");
  wrapDesignerTypeSection(root, ".provider-only", "provider");
  wrapDesignerTypeSection(root, ".policy-rule-only", "policy-rule");
  enhanceResourceDependencyPickers(root);
  bindDesignerAdvancedToggles(root);
}

function wrapDesignerTypeSection(root, selector, type) {
  const section = root.querySelector(selector);
  if (!section || section.dataset.resourceSectionEnhanced === "true") return;
  section.dataset.resourceSectionEnhanced = "true";
  section.dataset.designerSectionPanel = "module";
  const header = document.createElement("div");
  header.className = "span-12 resource-type-section-head";
  header.innerHTML = `<div>
    <span>${escapeHTML(t("catalog.typeSectionEyebrow"))}</span>
    <strong>${escapeHTML(resourceTypeLabel(type))}</strong>
    <p>${escapeHTML(resourceDesignerTypeHelp(type))}</p>
  </div>
  <div class="resource-type-section-actions">
    <button type="button" class="ghost-button" data-resource-apply-brief-shortcut>${escapeHTML(t("catalog.briefApplyShort"))}</button>
  </div>`;
  section.prepend(header);
  markResourceDesignerAdvancedFields(section, type);
  const advancedNodes = [...section.querySelectorAll("[data-resource-advanced]")].filter(node => node.parentElement === section);
  if (!advancedNodes.length) return;
  const details = document.createElement("details");
  details.className = "span-12 resource-developer-details";
  details.dataset.resourceDeveloperDetails = type;
  details.innerHTML = `<summary>
    <span>
      <strong>${escapeHTML(t("catalog.developerDetailsTitle"))}</strong>
      <small>${escapeHTML(resourceDesignerAdvancedHelp(type))}</small>
    </span>
    <i>${escapeHTML(t("catalog.showDeveloperDetails"))}</i>
  </summary>
  <div class="resource-developer-grid"></div>`;
  section.append(details);
  const grid = details.querySelector(".resource-developer-grid");
  advancedNodes.forEach(node => grid.append(node));
}

function markResourceDesignerAdvancedFields(section, type) {
  const byType = {
    skill: ["#skillVersion", "#skillAuthor", "#skillPriority", "#skillMaxIterations", "#skillAllowedKinds", "#skillNext", "#skillKeywords", "#skillTools", ".resource-script-editor", "#skillParams", "#skillMetadata", "#skillEmbeddingDescription"],
    agent: ["#agentTemperature", "#agentMaxTokens", "#agentMaxIterations", "#agentToolPolicy", "#agentAllowedKinds", "#agentAllowedTools"],
    tool: ["#toolTimeout", "#toolWorkdir", "#toolIsolation", "#toolRestartLimit", "#toolCooldown", "#toolMaxRequestBytes", "#toolMaxResponseBytes", "#toolEnvAllowlist", "#toolAllowedCommands", "#toolAllowedCommandPaths", "#toolEnabled", "#toolNetworkDisabled", ".resource-isolation-options", "#toolCodeFile", "#toolCode"],
    "workflow-template": ["#workflowTemplateGraph"],
    "workflow-schema": ["#workflowSchemaJSON"],
    "node-metadata": ["#nodeMetadataFields", "#nodeMetadataOutputs", "#nodeMetadataExamples", "#nodeMetadataDefaultStage"],
    "expression-helper": ["#expressionHelperArgs", "#expressionHelperHints", "#expressionHelperWarnings"],
    "team-template": ["#teamRoles", "#teamHandoffs", "#teamBlackboard", "#teamQuorumPresets"],
    kit: ["#kitExamples", "#kitMetadata"],
    provider: ["#providerMetadata"],
    "policy-rule": ["#policyExpression", "#policyDefaults", "#policyParams"]
  };
  (byType[type] || []).forEach(selector => {
    const node = section.querySelector(selector);
    const holder = node?.closest("label") || node;
    if (holder && !holder.classList.contains("resource-type-section-head")) holder.dataset.resourceAdvanced = "developer";
  });
}

function bindDesignerAdvancedToggles(root) {
  root.querySelectorAll("[data-resource-apply-brief-shortcut]").forEach(button => {
    button.onclick = () => {
      applyResourceBrief(root);
      scrollDesignerSection(root, "module");
    };
  });
}

function enhanceResourceDependencyPickers(root) {
  [
    [".skill-only", "skill"],
    [".agent-only", "agent"],
    [".team-template-only", "team-template"],
    [".kit-only", "kit"]
  ].forEach(([selector, type]) => {
    const section = root.querySelector(selector);
    if (!section || section.querySelector(`[data-resource-dependency-picker="${type}"]`)) return;
    const panel = document.createElement("section");
    panel.className = "span-12 resource-dependency-picker";
    panel.dataset.resourceDependencyPicker = type;
    const header = section.querySelector(".resource-type-section-head");
    if (header?.nextSibling) {
      section.insertBefore(panel, header.nextSibling);
    } else {
      section.append(panel);
    }
  });
  renderResourceDependencyPickers(root);
}

function bindResourceDependencyPickers(root) {
  if (root.dataset.resourceDependencyPickersBound === "true") return;
  root.dataset.resourceDependencyPickersBound = "true";
  root.addEventListener("click", event => {
    const button = event.target.closest("[data-resource-dependency-choice]");
    if (!button || !root.contains(button)) return;
    event.preventDefault();
    applyResourceDependencyChoice(root, button);
  });
  root.addEventListener("input", event => {
    if (event.target.matches?.(resourceDependencyFieldSelector())) renderResourceDependencyPickers(root);
  });
  root.addEventListener("change", event => {
    if (event.target.matches?.(resourceDependencyFieldSelector())) renderResourceDependencyPickers(root);
  });
}

function resourceDependencyFieldSelector() {
  return [
    "#skillAgent",
    "#skillAllowedKinds",
    "#skillNext",
    "#skillTools",
    "#agentProvider",
    "#agentAllowedKinds",
    "#agentAllowedTools",
    "#teamRecommendedWorkflow",
    "#teamEntryAgent",
    "#kitAgents",
    "#kitSkills",
    "#kitTools",
    "#kitProviders",
    "#kitWorkflows",
    "#kitWorkflowTemplates",
    "#kitTeamTemplates",
    "#kitPolicyRules"
  ].join(", ");
}

function renderResourceDependencyPickers(root) {
  root.querySelectorAll("[data-resource-dependency-picker]").forEach(panel => {
    const type = panel.dataset.resourceDependencyPicker || "";
    panel.innerHTML = resourceDependencyPickerHTML(root, type);
  });
}

function resourceDependencyPickerHTML(root, type) {
  const groups = resourceDependencyGroups(root, type).filter(group => group?.choices?.length || group?.showWhenEmpty);
  if (!groups.length) return "";
  return `<div class="resource-dependency-head">
    <div>
      <span>${escapeHTML(t("catalog.dependencyPickerTitle"))}</span>
      <strong>${escapeHTML(resourceDependencyTitle(type))}</strong>
      <p>${escapeHTML(t("catalog.dependencyPickerHelp"))}</p>
    </div>
  </div>
  <div class="resource-dependency-groups">
    ${groups.map(group => resourceDependencyGroupHTML(root, group)).join("")}
  </div>`;
}

function resourceDependencyTitle(type) {
  if (type === "skill") return t("catalog.dependencySkillTitle");
  if (type === "agent") return t("catalog.dependencyAgentTitle");
  if (type === "team-template") return t("catalog.dependencyTeamTitle");
  if (type === "kit") return t("catalog.dependencyKitTitle");
  return t("catalog.dependencyPickerTitle");
}

function resourceDependencyGroups(root, type) {
  if (type === "skill") {
    return [
      {
        title: t("catalog.preferredAgent"),
        help: t("catalog.dependencyPreferredAgentHelp"),
        choices: agentDependencyChoices("#skillAgent", "set"),
        showWhenEmpty: true
      },
      {
        title: t("catalog.allowedKinds"),
        help: t("catalog.dependencyToolKindsHelp"),
        choices: toolKindDependencyChoices("#skillAllowedKinds"),
        compact: true
      },
      {
        title: t("catalog.tools"),
        help: t("catalog.dependencyToolsHelp"),
        choices: toolDependencyChoices("#skillTools", "toggle-tool-line"),
        showWhenEmpty: true
      },
      {
        title: t("catalog.nextSkills"),
        help: t("catalog.dependencyNextSkillsHelp"),
        choices: skillDependencyChoices("#skillNext", "toggle-list", { excludeCurrent: true, root }),
        compact: true
      }
    ];
  }
  if (type === "agent") {
    return [
      {
        title: commonLabel("provider"),
        help: t("catalog.dependencyProvidersHelp"),
        choices: providerDependencyChoices("#agentProvider", "set-provider"),
        showWhenEmpty: true
      },
      {
        title: t("catalog.allowedKinds"),
        help: t("catalog.dependencyToolKindsHelp"),
        choices: toolKindDependencyChoices("#agentAllowedKinds"),
        compact: true
      },
      {
        title: t("catalog.allowedTools"),
        help: t("catalog.dependencyAllowedToolsHelp"),
        choices: toolDependencyChoices("#agentAllowedTools", "toggle-list"),
        showWhenEmpty: true
      }
    ];
  }
  if (type === "team-template") {
    return [
      {
        title: t("catalog.teamRecommendedWorkflow"),
        help: t("catalog.dependencyRecommendedWorkflowHelp"),
        choices: workflowDependencyChoices("#teamRecommendedWorkflow", "set"),
        showWhenEmpty: true
      },
      {
        title: t("catalog.teamEntryAgent"),
        help: t("catalog.dependencyEntryAgentHelp"),
        choices: agentDependencyChoices("#teamEntryAgent", "set"),
        showWhenEmpty: true
      }
    ];
  }
  if (type === "kit") {
    return [
      {
        title: t("catalog.kitProviders"),
        help: t("catalog.dependencyKitHelp"),
        choices: providerDependencyChoices("#kitProviders", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitAgents"),
        help: t("catalog.dependencyKitHelp"),
        choices: agentDependencyChoices("#kitAgents", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitSkills"),
        help: t("catalog.dependencyKitHelp"),
        choices: skillDependencyChoices("#kitSkills", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitTools"),
        help: t("catalog.dependencyKitHelp"),
        choices: toolDependencyChoices("#kitTools", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitWorkflows"),
        help: t("catalog.dependencyKitHelp"),
        choices: workflowDependencyChoices("#kitWorkflows", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitWorkflowTemplates"),
        help: t("catalog.dependencyKitHelp"),
        choices: workflowTemplateDependencyChoices("#kitWorkflowTemplates", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitTeamTemplates"),
        help: t("catalog.dependencyKitHelp"),
        choices: teamTemplateDependencyChoices("#kitTeamTemplates", "toggle-list"),
        compact: true
      },
      {
        title: t("catalog.kitPolicyRules"),
        help: t("catalog.dependencyKitHelp"),
        choices: policyRuleDependencyChoices("#kitPolicyRules", "toggle-list"),
        compact: true
      }
    ];
  }
  return [];
}

function resourceDependencyGroupHTML(root, group) {
  const limit = group.limit || (group.compact ? 12 : 8);
  const choices = Array.isArray(group.choices) ? group.choices : [];
  const visible = choices.slice(0, limit);
  const hidden = Math.max(0, choices.length - visible.length);
  const body = visible.length
    ? visible.map(choice => resourceDependencyChoiceHTML(root, choice, group.compact)).join("")
    : `<div class="resource-dependency-empty">${escapeHTML(t("catalog.dependencyEmpty"))}</div>`;
  return `<section class="resource-dependency-group ${group.compact ? "compact" : ""}">
    <div class="resource-dependency-group-head">
      <div>
        <strong>${escapeHTML(group.title || "")}</strong>
        <span>${escapeHTML(group.help || t("catalog.dependencyPickToFill"))}</span>
      </div>
      <span class="badge neutral">${escapeHTML(String(choices.length))}</span>
    </div>
    <div class="resource-dependency-list">
      ${body}
      ${hidden ? `<span class="resource-dependency-more">${escapeHTML(t("catalog.dependencyMoreCount", { count: hidden }))}</span>` : ""}
    </div>
  </section>`;
}

function resourceDependencyChoiceHTML(root, choice, compact = false) {
  const selected = resourceDependencyChoiceSelected(root, choice);
  const meta = (choice.meta || []).filter(Boolean).slice(0, compact ? 2 : 4);
  const action = selected ? t("catalog.dependencySelected") : (choice.mode || "").startsWith("set") ? t("catalog.dependencySet") : t("catalog.dependencyAdd");
  const title = resourceDisplayValue(choice.title || choice.value);
  const body = localizedText(choice.body || "");
  return `<button type="button"
      class="resource-dependency-card ${compact ? "compact" : ""} ${selected ? "selected" : ""}"
      data-resource-dependency-choice
      data-target="${escapeHTML(choice.target || "")}"
      data-value="${escapeHTML(choice.value || "")}"
      data-mode="${escapeHTML(choice.mode || "toggle-list")}"
      data-default-model="${escapeHTML(choice.defaultModel || "")}"
      aria-pressed="${selected ? "true" : "false"}">
    <span class="resource-dependency-icon" aria-hidden="true">${escapeHTML(choice.icon || resourceDependencyIcon(choice.kind))}</span>
    <span class="resource-dependency-copy">
      <strong>${escapeHTML(title)}</strong>
      ${body && !compact ? `<small>${escapeHTML(body)}</small>` : ""}
      ${meta.length ? `<em>${meta.map(item => escapeHTML(resourceDisplayValue(item))).join(" · ")}</em>` : ""}
    </span>
    <span class="resource-dependency-action">${escapeHTML(action)}</span>
  </button>`;
}

function resourceDependencyChoiceSelected(root, choice) {
  const field = root.querySelector(choice.target || "");
  if (!field) return false;
  const value = String(choice.value || "").trim();
  const mode = choice.mode || "toggle-list";
  if (!value) return false;
  if (mode === "set" || mode === "set-provider") return String(field.value || "").trim() === value;
  if (mode === "toggle-tool-line") {
    return parseTools(field.value).some(tool => String(tool.name || "").trim() === value);
  }
  return splitLines(field.value).includes(value);
}

function applyResourceDependencyChoice(root, button) {
  const target = button.dataset.target || "";
  const value = String(button.dataset.value || "").trim();
  const mode = button.dataset.mode || "toggle-list";
  const field = root.querySelector(target);
  if (!field || !value) return;
  if (mode === "set" || mode === "set-provider") {
    ensureSelectOption(field, value, value);
    field.value = value;
    if (mode === "set-provider") {
      const model = button.dataset.defaultModel || "";
      const modelInput = root.querySelector("#agentModel");
      if (modelInput && model && !modelInput.value.trim()) modelInput.value = model;
    }
  } else if (mode === "toggle-tool-line") {
    toggleToolLineValue(field, value);
  } else {
    toggleListValue(field, value);
  }
  field.dispatchEvent(new Event("input", { bubbles: true }));
  field.dispatchEvent(new Event("change", { bubbles: true }));
  renderResourceDependencyPickers(root);
}

function ensureSelectOption(field, value, label) {
  if (field?.tagName !== "SELECT") return;
  if ([...field.options].some(option => option.value === value)) return;
  const option = document.createElement("option");
  option.value = value;
  option.textContent = label || value;
  field.appendChild(option);
}

function toggleListValue(field, value) {
  const values = splitList(field.value);
  const index = values.indexOf(value);
  if (index >= 0) {
    values.splice(index, 1);
  } else {
    values.push(value);
  }
  field.value = values.join(", ");
}

function toggleToolLineValue(field, value) {
  const lines = String(field.value || "").split(/\r?\n/).map(line => line.trim()).filter(Boolean);
  const index = lines.findIndex(line => line.split("|")[0].trim() === value);
  if (index >= 0) {
    lines.splice(index, 1);
  } else {
    lines.push(value);
  }
  field.value = lines.join("\n");
}

function resourceDependencyIcon(kind) {
  return {
    agent: "A",
    skill: "S",
    tool: "T",
    provider: "P",
    workflow: "W",
    "workflow-template": "WT",
    team: "TM",
    policy: "P",
    kind: "K"
  }[kind] || "+";
}

function agentDependencyChoices(target, mode) {
  return (catalogContext.runtime?.agents || []).map(agent => ({
    kind: "agent",
    target,
    mode,
    value: agent.id || agent.name || "",
    title: agent.id || agent.name || "",
    body: agent.description || agent.name || "",
    meta: [agent.provider ? `${commonLabel("provider")}: ${agent.provider}` : "", enumLabel("mode", agent.mode || "")]
  })).filter(choice => choice.value);
}

function skillDependencyChoices(target, mode, options = {}) {
  const current = options.excludeCurrent ? String(options.root?.querySelector("#resourceName")?.value || "").trim() : "";
  return (catalogContext.runtime?.skills || []).map(skill => ({
    kind: "skill",
    target,
    mode,
    value: skill.name || "",
    title: skill.name || "",
    body: skill.description || "",
    meta: [skill.preferred_agent ? `${commonLabel("agent")}: ${skill.preferred_agent}` : "", enumLabel("mode", skill.mode || "")]
  })).filter(choice => choice.value && choice.value !== current);
}

function toolDependencyChoices(target, mode) {
  const resources = Array.isArray(catalogContext.toolResources) && catalogContext.toolResources.length
    ? catalogContext.toolResources
    : (catalogContext.runtime?.tools || []).map(item => typeof item === "string" ? { name: item } : item || {});
  return resources.map(tool => {
    const name = tool?.risk?.qualified_name || tool?.qualified_name || tool?.name || "";
    const riskLevel = tool?.risk?.risk_level || "";
    return {
      kind: "tool",
      target,
      mode,
      value: name,
      title: name,
      body: tool?.description || tool?.risk?.security_boundary || "",
      meta: [
        tool?.language || "",
        riskLevel ? toolRiskLabel(riskLevel) : "",
        tool?.isolation ? enumLabel("isolation", tool.isolation) : ""
      ]
    };
  }).filter(choice => choice.value);
}

function providerDependencyChoices(target, mode) {
  const providers = catalogContext.providerOptions?.length
    ? catalogContext.providerOptions
    : normalizeProviderOptions(catalogContext.runtime?.providers || []);
  return providers.map(provider => ({
    kind: "provider",
    target,
    mode,
    value: provider.id || "",
    title: provider.label || provider.id || "",
    body: provider.description || "",
    defaultModel: provider.defaultModel || "",
    meta: [provider.type || "", provider.defaultModel ? `${commonLabel("model")}: ${provider.defaultModel}` : ""]
  })).filter(choice => choice.value);
}

function workflowDependencyChoices(target, mode) {
  return (catalogContext.workflowGraphs || []).map(workflow => ({
    kind: "workflow",
    target,
    mode,
    value: workflow.name || "",
    title: workflow.name || "",
    body: workflow.description || "",
    meta: [Array.isArray(workflow.stages) ? t("catalog.dependencyStageCount", { count: workflow.stages.length }) : ""]
  })).filter(choice => choice.value);
}

function workflowTemplateDependencyChoices(target, mode) {
  return (catalogContext.workflowTemplates || []).map(template => ({
    kind: "workflow-template",
    target,
    mode,
    value: template.name || "",
    title: localizedText(template.title || template.name || ""),
    body: template.description || "",
    meta: [template.category || "", Array.isArray(template.tags) ? template.tags.slice(0, 3).join(", ") : ""]
  })).filter(choice => choice.value);
}

function teamTemplateDependencyChoices(target, mode) {
  return (catalogContext.teamTemplates || []).map(team => ({
    kind: "team",
    target,
    mode,
    value: team.name || "",
    title: localizedText(team.title || team.name || ""),
    body: team.description || "",
    meta: [team.category || "", Array.isArray(team.tags) ? team.tags.slice(0, 3).join(", ") : ""]
  })).filter(choice => choice.value);
}

function policyRuleDependencyChoices(target, mode) {
  return (catalogContext.policyRules || []).map(rule => ({
    kind: "policy",
    target,
    mode,
    value: rule.name || "",
    title: localizedText(rule.label || rule.name || ""),
    body: rule.description || rule.reason || "",
    meta: [enumLabel("policyOperator", rule.operator || "")]
  })).filter(choice => choice.value);
}

function toolKindDependencyChoices(target) {
  return ["read", "write", "exec", "network"].map(kind => ({
    kind: "kind",
    target,
    mode: "toggle-list",
    value: kind,
    title: enumLabel("toolRiskKind", kind),
    body: t(`catalog.dependencyKind.${kind}`)
  }));
}

function resourceDesignerTypeHelp(type) {
  const key = `catalog.typeHelp.${String(type || "").replaceAll("-", "_")}`;
  const value = t(key);
  return value === key ? t("catalog.typeHelp.default") : value;
}

function resourceDesignerAdvancedHelp(type) {
  const key = `catalog.advancedHelp.${String(type || "").replaceAll("-", "_")}`;
  const value = t(key);
  return value === key ? t("catalog.advancedHelp.default") : value;
}

function bindResourceCapabilityActionDelegation(root) {
  if (root.dataset.resourceCapabilityActionsBound === "true") return;
  root.dataset.resourceCapabilityActionsBound = "true";
  root.addEventListener("click", event => {
    const button = event.target.closest("[data-resource-capability-action]");
    if (!button || !root.contains(button)) return;
    event.preventDefault();
    void handleCatalogCapabilityAction(root, button);
  });
}

function scrollCatalogResourceTarget(root, selector) {
  const target = selector ? root.querySelector(selector) : null;
  const search = root.querySelector("#resourceSearch");
  const group = root.querySelector("#resourceGroupFilter");
  if (group) group.value = "essential";
  if (search) search.value = "";
  applyResourceFilters(root);
  if (!target) return;
  target.scrollIntoView({ block: "start", behavior: prefersReducedMotion() ? "auto" : "smooth" });
  target.classList.add("resource-focus-pulse");
  window.setTimeout(() => target.classList.remove("resource-focus-pulse"), prefersReducedMotion() ? 700 : 1600);
}

function focusKitResource(root, kind, name) {
  const normalizedKind = normalizeCatalogFocusKind(kind);
  const normalizedName = String(name || "").trim();
  const groupName = catalogFocusGroup(normalizedKind);
  const group = root.querySelector("#resourceGroupFilter");
  const search = root.querySelector("#resourceSearch");
  if (group && groupName && [...group.options].some(option => option.value === groupName)) group.value = groupName;
  if (search) search.value = normalizedName;
  applyResourceFilters(root);
  const node = findCatalogFocusItem(root, normalizedKind, normalizedName) || findCatalogFocusPanel(root, normalizedKind, groupName);
  if (!node) return;
  node.classList.add("resource-focus-pulse");
  node.focus?.({ preventScroll: true });
  node.scrollIntoView({ block: "center", behavior: prefersReducedMotion() ? "auto" : "smooth" });
  window.setTimeout(() => node.classList.remove("resource-focus-pulse"), prefersReducedMotion() ? 700 : 1600);
}

function openKitWorkflow(name) {
  const value = String(name || "").trim();
  if (value) {
    try {
      localStorage.setItem("goflow.workflow.template", value);
    } catch {
      // Navigation still opens Workflow Studio when browser storage is blocked.
    }
  }
  location.hash = "workflows";
}

function bindResourceCapabilityRefresh(root) {
  const selectors = ["#resourceName", "#providerID", "#nodeMetadataType", "#expressionHelperSource"];
  selectors.forEach(selector => {
    const node = root.querySelector(selector);
    if (!node || node.dataset.resourceCapabilityRefreshBound === "true") return;
    node.dataset.resourceCapabilityRefreshBound = "true";
    const refresh = () => {
      renderResourceCapabilitySummary(root, root.querySelector("#resourceType")?.value || "");
    };
    node.addEventListener("input", refresh);
    node.addEventListener("change", refresh);
  });
}

function bindSkillScriptEditor(root) {
  if (root.dataset.skillScriptEditorBound === "true") return;
  root.dataset.skillScriptEditorBound = "true";
  root.addEventListener("click", event => {
    const addButton = event.target.closest("[data-add-skill-script]");
    if (addButton && root.contains(addButton)) {
      event.preventDefault();
      addSkillScriptCard(root);
      return;
    }
    const removeButton = event.target.closest("[data-remove-skill-script]");
    if (removeButton && root.contains(removeButton)) {
      event.preventDefault();
      removeButton.closest("[data-skill-script-card]")?.remove();
      ensureSkillScriptEmptyState(root);
      updateSkillScriptsHidden(root);
    }
  });
  root.addEventListener("input", event => {
    if (!event.target.closest("#skillScriptsList")) return;
    clearSkillScriptFieldError(event.target);
    updateSkillScriptCardTitle(event.target.closest("[data-skill-script-card]"));
    updateSkillScriptsHidden(root);
  });
  root.addEventListener("change", event => {
    if (!event.target.closest("#skillScriptsList")) return;
    clearSkillScriptFieldError(event.target);
    updateSkillScriptsHidden(root);
  });
}

function scheduleResourceFilters(root) {
  if (catalogFilterFrame) return;
  catalogFilterFrame = requestAnimationFrame(() => {
    catalogFilterFrame = 0;
    applyResourceFilters(root);
  });
}

function isCatalogTourStep(step) {
  return step?.route === "catalog" && [
    "catalog-hero",
    "catalog-agents",
    "catalog-skills",
    "catalog-tools"
  ].includes(step.id);
}

function resetCatalogTourFilters(root) {
  const search = root.querySelector("#resourceSearch");
  const group = root.querySelector("#resourceGroupFilter");
  let changed = false;
  if (search && search.value) {
    search.value = "";
    changed = true;
  }
  if (group && group.value !== "recommended") {
    group.value = "recommended";
    changed = true;
  }
  return changed;
}

function applyResourceFilters(root) {
  const query = String(root.querySelector("#resourceSearch")?.value || "").trim().toLowerCase();
  const groupSelect = root.querySelector("#resourceGroupFilter");
  let group = groupSelect?.value || "all";
  if (catalogNormalExperience() && ["all", "advanced", "runtime", "policy"].includes(group)) {
    group = "recommended";
    if (groupSelect) groupSelect.value = group;
  }
  let visibleItems = 0;
  let visiblePanels = 0;
  const panels = [...root.querySelectorAll("[data-resource-panel]")];
  if (!query && group === "all") {
    panels.forEach(panel => {
      panel.classList.remove("resource-panel-filtered");
      const hiddenItems = panel.querySelectorAll(".resource-filter-hidden");
      hiddenItems.forEach(item => item.classList.remove("resource-filter-hidden"));
      visibleItems += panel.querySelectorAll(".resource-item, .kit-scaffold-card, .tool-scaffold-mini").length;
      visiblePanels += 1;
    });
    const count = root.querySelector("#resourceFilterCount");
    if (count) count.textContent = t("catalog.resourceFilterCount", { count: visibleItems, panels: visiblePanels });
    return;
  }
  panels.forEach(panel => {
    const groupOk = resourcePanelMatchesGroup(panel.dataset.resourceGroup || "", group);
    const items = [...panel.querySelectorAll(".resource-item, .kit-scaffold-card, .tool-scaffold-mini")];
    let visibleInPanel = 0;
    items.forEach(item => {
      const itemOk = groupOk && (!query || resourceSearchText(item).includes(query));
      toggleClass(item, "resource-filter-hidden", !itemOk);
      if (itemOk) visibleInPanel += 1;
    });
    const panelOk = groupOk && (!query || visibleInPanel > 0);
    toggleClass(panel, "resource-panel-filtered", !panelOk);
    if (panelOk) visiblePanels += 1;
    visibleItems += visibleInPanel;
  });
  const count = root.querySelector("#resourceFilterCount");
  if (count) count.textContent = t("catalog.resourceFilterCount", { count: visibleItems, panels: visiblePanels });
}

function resourcePanelMatchesGroup(panelGroup, selectedGroup) {
  const group = String(selectedGroup || "recommended");
  if (group === "all") return true;
  if (group === "recommended") return ["essential", "workflow", "runtime"].includes(panelGroup);
  return panelGroup === group;
}

function catalogNormalExperience() {
  const shell = document.querySelector("[data-app-shell]");
  if (shell?.classList.contains("simple-experience")) return true;
  if (shell?.classList.contains("expert-experience")) return false;
  return document.documentElement.dataset.experience !== "expert";
}

function resourceSearchText(item) {
  if (!item) return "";
  if (!item.dataset.resourceSearchText) item.dataset.resourceSearchText = item.textContent.toLowerCase();
  return item.dataset.resourceSearchText;
}

function toggleClass(node, className, enabled) {
  if (!node || node.classList.contains(className) === enabled) return;
  node.classList.toggle(className, enabled);
}

async function focusCatalogTarget(root, runtime = catalogContext.runtime || {}, providerOptions = catalogContext.providerOptions || []) {
  let focus = null;
  try {
    focus = JSON.parse(sessionStorage.getItem("goflow.catalog.focus") || "null");
    sessionStorage.removeItem("goflow.catalog.focus");
  } catch {
    focus = null;
  }
  const kind = normalizeCatalogFocusKind(focus?.kind);
  const name = String(focus?.name || "").trim();
  if (!kind && !name) return;

  const target = findCatalogFocusItem(root, kind, name);
  const groupName = catalogFocusGroup(kind);
  const group = root.querySelector("#resourceGroupFilter");
  const search = root.querySelector("#resourceSearch");
  if (group && groupName && [...group.options].some(option => option.value === groupName)) group.value = groupName;
  if (search) search.value = target && name ? name : "";
  applyResourceFilters(root);

  if (focus?.source === "settings-diagnostics" && kind && name && canOpenCatalogFocusDesigner(kind)) {
    await openDesigner(root, runtime, kind, name, providerOptions);
    requestAnimationFrame(() => focusCatalogDesignerTarget(root, focus));
    return;
  }

  requestAnimationFrame(() => {
    const node = target || findCatalogFocusPanel(root, kind, groupName);
    if (!node) return;
    node.classList.add("resource-focus-pulse");
    node.focus?.({ preventScroll: true });
    node.scrollIntoView({ block: "center", behavior: prefersReducedMotion() ? "auto" : "smooth" });
    window.setTimeout(() => node.classList.remove("resource-focus-pulse"), prefersReducedMotion() ? 700 : 1600);
  });
}

function canOpenCatalogFocusDesigner(kind) {
  return [
    "agent",
    "skill",
    "tool",
    "kit",
    "provider",
    "policy-rule",
    "team-template",
    "workflow-template",
    "workflow-schema",
    "node-metadata",
    "expression-helper"
  ].includes(kind);
}

function focusCatalogDesignerTarget(root, focus = {}) {
  const type = root.querySelector("#resourceType")?.value || normalizeCatalogFocusKind(focus.kind);
  const fieldName = String(focus.field || "").trim();
  const selector = fieldName
    ? resourceValidationFieldSelector({ field: fieldName, ref: fieldName, code: "" }, type)
    : "";
  const field = selector ? root.querySelector(selector) : null;
  const node = field?.closest("label, .resource-isolation-options, .stack, .check") || root.querySelector("#resourceDesigner .designer-panel");
  if (!node) return;
  node.classList.add("resource-focus-pulse");
  if (field && typeof field.focus === "function") field.focus({ preventScroll: true });
  node.scrollIntoView({ block: "center", behavior: prefersReducedMotion() ? "auto" : "smooth" });
  window.setTimeout(() => node.classList.remove("resource-focus-pulse"), prefersReducedMotion() ? 700 : 1600);
}

function findCatalogFocusItem(root, kind, name) {
  if (!kind && !name) return null;
  const normalizedName = name.toLowerCase();
  return [...root.querySelectorAll("[data-resource-kind]")].find(item => {
    const itemKinds = [item.dataset.resourceKind, item.dataset.resourceAliases]
      .join(" ")
      .split(/\s+/)
      .filter(Boolean)
      .map(normalizeCatalogFocusKind);
    const kindOk = !kind || itemKinds.includes(kind);
    const itemName = String(item.dataset.resourceName || "").trim().toLowerCase();
    const nameOk = !normalizedName || itemName === normalizedName;
    return kindOk && nameOk;
  }) || null;
}

function findCatalogFocusPanel(root, kind, groupName) {
  const panel = [...root.querySelectorAll("[data-resource-panel-kind]")].find(item => {
    const kinds = String(item.dataset.resourcePanelKind || "")
      .split(/\s+/)
      .filter(Boolean)
      .map(normalizeCatalogFocusKind);
    return kinds.includes(kind);
  });
  return panel || (groupName ? root.querySelector(`[data-resource-panel][data-resource-group="${groupName}"]`) : null);
}

function normalizeCatalogFocusKind(kind) {
  const value = String(kind || "").replaceAll("_", "-").toLowerCase();
  return {
    mcp: "tool",
    "mcp-server": "tool",
    tool: "tool",
    provider: "provider",
    agent: "agent",
    skill: "skill",
    "policy-rule": "policy-rule",
    "workflow-template": "workflow-template",
    "workflow-schema": "workflow-schema",
    "team-template": "team-template",
    kit: "kit",
    "expression-helper": "expression-helper",
    "workflow-node-metadata": "node-metadata",
    "node-metadata": "node-metadata"
  }[value] || value;
}

function catalogFocusGroup(kind) {
  return {
    agent: "essential",
    skill: "essential",
    tool: "essential",
    kit: "essential",
    provider: "runtime",
    "policy-rule": "policy",
    "team-template": "workflow",
    "workflow-template": "workflow",
    "workflow-schema": "advanced",
    "node-metadata": "advanced",
    "expression-helper": "advanced"
  }[kind] || "all";
}

function resourceItemAttrs(kind, name, aliases = []) {
  return [
    `data-resource-kind="${escapeHTML(kind)}"`,
    `data-resource-name="${escapeHTML(String(name || ""))}"`,
    aliases.length ? `data-resource-aliases="${escapeHTML(aliases.join(" "))}"` : "",
    `tabindex="-1"`
  ].filter(Boolean).join(" ");
}

function prefersReducedMotion() {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches;
}

function renderAgent(agent) {
  return `<div class="item resource-item" ${resourceItemAttrs("agent", agent.id)}>
    <strong>${escapeHTML(agent.id)}</strong>
    <span class="muted">${escapeHTML(localizedText(agent.name || ""))} ${escapeHTML(enumLabel("mode", agent.mode || ""))}</span>
    <div class="muted">${t("common.provider")}: ${escapeHTML(agent.provider || "-")} / ${t("common.model")}: ${escapeHTML(agent.model || "-")}</div>
    <div class="muted">${t("common.policy")}: ${escapeHTML(enumLabel("toolPolicy", agent.tool_policy || ""))} / ${t("common.tools")}: ${escapeHTML(formatList(agent.allowed_tool_kinds))}</div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="agent" data-resource-name="${escapeHTML(agent.id)}">${t("catalog.edit")}</button>
    </div>
  </div>`;
}

function renderSkill(skill) {
  return `<div class="item resource-item" ${resourceItemAttrs("skill", skill.name)}>
    <strong>${escapeHTML(skill.name)}</strong>
    <span class="muted">${escapeHTML(localizedText(skill.description || ""))}</span>
    <div class="muted">${t("common.agent")}: ${escapeHTML(skill.preferred_agent || "-")} / ${t("common.mode")}: ${escapeHTML(enumLabel("mode", skill.mode || ""))}</div>
    <div class="muted">${t("common.next")}: ${escapeHTML(formatList(skill.next_skills))}</div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="skill" data-resource-name="${escapeHTML(skill.name)}">${t("catalog.edit")}</button>
    </div>
  </div>`;
}

function renderTool(tool) {
  const doc = typeof tool === "string" ? toolResourceByName(tool) || { name: tool } : tool || {};
  const name = doc.name || String(tool || "");
  const risk = doc.risk || {};
  const container = toolContainerSummary(doc, name);
  const riskLevel = String(risk.risk_level || "").trim();
  const riskTone = toolRiskTone(riskLevel, risk);
  const workspaceScoped = risk.workspace_scoped_inputs === true;
  const workspaceEnforced = risk.workspace_scope_enforced === true;
  const chips = [
    riskLevel ? `<span class="resource-risk-chip ${riskTone}">${escapeHTML(toolRiskLabel(riskLevel))}</span>` : "",
    risk.requires_approval ? `<span class="resource-risk-chip warn">${escapeHTML(t("catalog.toolRiskApproval"))}</span>` : "",
    risk.destructive ? `<span class="resource-risk-chip bad">${escapeHTML(t("catalog.toolRiskDestructive"))}</span>` : "",
    risk.sandboxed ? `<span class="resource-risk-chip good">${escapeHTML(t("catalog.toolRiskSandboxed"))}</span>` : "",
    risk.external_sandbox_recommended ? `<span class="resource-risk-chip warn">${escapeHTML(t("catalog.toolRiskExternalSandbox"))}</span>` : "",
    workspaceScoped ? `<span class="resource-risk-chip ${workspaceEnforced ? "good" : "warn"}">${escapeHTML(t("catalog.toolRiskWorkspaceInputs"))}</span>` : "",
    workspaceScoped ? `<span class="resource-risk-chip ${workspaceEnforced ? "good" : "warn"}">${escapeHTML(workspaceEnforced ? t("catalog.toolRiskWorkspaceEnforced") : t("catalog.toolRiskWorkspaceNotEnforced"))}</span>` : "",
    ...toolContainerChips(container)
  ].filter(Boolean).join("");
  const summary = risk.security_boundary || doc.description || t("catalog.toolRiskNoProfile");
  const facts = [
    risk.kind ? [t("catalog.toolRiskKind"), enumLabel("toolRiskKind", risk.kind)] : null,
    risk.isolation_level || risk.isolation ? [t("catalog.toolRiskIsolation"), enumLabel("isolation", risk.isolation_level || risk.isolation)] : null,
    Array.isArray(risk.capabilities) && risk.capabilities.length ? [t("catalog.toolRiskCapabilities"), risk.capabilities.slice(0, 3).join(", ")] : null,
    workspaceScoped ? [t("catalog.toolRiskWorkspaceScope"), workspaceEnforced ? t("catalog.toolRiskWorkspaceEnforced") : t("catalog.toolRiskWorkspaceNotEnforced")] : null,
    container.isolation_profile ? [t("catalog.isolationProfile"), isolationProfileDisplayLabel(container.isolation_profile)] : null,
    container.container_image ? [t("catalog.isolationImage"), container.container_image] : null,
    container.container_pull_policy ? [t("catalog.isolationPullPolicy"), containerPullPolicyLabel(container.container_pull_policy)] : null,
    container.sandbox_features.length ? [t("catalog.sandboxFeatures"), sandboxFeatureListLabel(container.sandbox_features)] : null,
    container.missing_sandbox_features.length ? [t("catalog.sandboxMissingFeatures"), sandboxFeatureListLabel(container.missing_sandbox_features)] : null,
    container.windows_isolation ? [t("catalog.windowsIsolation"), windowsIsolationLabel(container.windows_isolation)] : null
  ].filter(Boolean);
  const warnings = [
    workspaceScoped && !workspaceEnforced ? t("catalog.toolRiskWorkspaceNotEnforcedHelp") : "",
    ...(Array.isArray(risk.warnings) ? risk.warnings : [])
  ].filter(Boolean).slice(0, 3);
  return `<div class="item resource-item resource-tool-item" ${resourceItemAttrs("tool", name, ["mcp", "mcp_server"])}>
    <div class="resource-tool-head">
      <strong>${escapeHTML(name)}</strong>
      ${chips ? `<div class="resource-risk-chips">${chips}</div>` : ""}
    </div>
    <span class="muted">${escapeHTML(localizedText(doc.description || t("catalog.resourceTool")))}</span>
    <div class="resource-risk-summary">${escapeHTML(localizedText(summary))}</div>
    ${facts.length ? `<div class="resource-facts compact">${facts.map(([label, value]) => `<span>${escapeHTML(label)}: ${escapeHTML(value)}</span>`).join("")}</div>` : ""}
    ${warnings.length ? `<div class="resource-risk-warnings">${warnings.map(item => `<span>${escapeHTML(localizedText(item))}</span>`).join("")}</div>` : ""}
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="tool" data-resource-name="${escapeHTML(name)}">${t("catalog.edit")}</button>
    </div>
  </div>`;
}

function renderToolList(runtime = catalogContext.runtime || {}) {
  const resources = Array.isArray(catalogContext.toolResources) ? catalogContext.toolResources : [];
  if (resources.length) return resources.map(renderTool).join("");
  return (runtime.tools || []).map(renderTool).join("") || empty(t("catalog.noTools"), { actionLabel: t("catalog.newTool"), action: "tool" });
}

function renderToolScaffoldStrip(scaffolds = []) {
  const items = sortToolScaffolds(Array.isArray(scaffolds) ? scaffolds : []);
  if (!items.length) return "";
  return `<section class="tool-scaffold-panel">
    <div class="tool-scaffold-head">
      <div>
        <strong>${escapeHTML(t("catalog.toolScaffolds"))}</strong>
        <span>${escapeHTML(t("catalog.toolScaffoldsHelp"))}</span>
      </div>
      <span class="badge">${escapeHTML(String(items.length))}</span>
    </div>
    <div class="tool-scaffold-list">
      ${items.map(renderToolScaffoldMini).join("")}
    </div>
  </section>`;
}

function renderToolScaffoldMini(scaffold = {}) {
  const name = toolScaffoldName(scaffold);
  const profile = scaffold.isolation_profile ? isolationProfileDisplayLabel(scaffold.isolation_profile) : "";
  const image = scaffold.default_image || scaffold.image || "";
  const facts = [
    scaffold.isolation ? enumLabel("isolation", scaffold.isolation) : "",
    profile,
    scaffold.workspace_mount ? `${t("catalog.isolationWorkspaceMount")}: ${scaffold.workspace_mount}` : "",
    scaffold.network_mode ? `${t("catalog.isolationNetwork")}: ${containerNetworkLabel(scaffold.network_mode)}` : ""
  ].filter(Boolean);
  return `<article class="tool-scaffold-mini ${name === "python-container-production" ? "featured" : ""}">
    <div>
      <strong>${escapeHTML(toolScaffoldTitle(scaffold))}</strong>
      <small>${escapeHTML([name, localizedText(scaffold.category || "")].filter(Boolean).join(" / "))}</small>
    </div>
    <p>${escapeHTML(toolScaffoldDescription(scaffold))}</p>
    ${image ? `<span class="tool-scaffold-image" title="${escapeHTML(image)}">${escapeHTML(image)}</span>` : ""}
    ${facts.length ? `<div class="resource-risk-chips">${facts.slice(0, 4).map(item => `<span class="resource-risk-chip neutral">${escapeHTML(item)}</span>`).join("")}</div>` : ""}
    <button type="button" data-tool-scaffold="${escapeHTML(name)}">${escapeHTML(t("catalog.useToolScaffold"))}</button>
  </article>`;
}

function toolResourceByName(name) {
  const normalized = String(name || "").toLowerCase();
  return (catalogContext.toolResources || []).find(tool => String(tool?.name || "").toLowerCase() === normalized);
}

function runtimeMCPServerByName(name) {
  const normalized = String(name || "").toLowerCase();
  return (catalogContext.runtime?.mcp_servers || []).find(server => String(server?.name || "").toLowerCase() === normalized) || null;
}

function toolContainerSummary(doc = {}, name = "") {
  const runtime = runtimeMCPServerByName(name) || {};
  const risk = doc?.risk || {};
  const options = doc?.isolation_options || {};
  return {
    isolation_profile: runtime.isolation_profile || doc.isolation_profile || "",
    container_image: runtime.container_image || options.image || "",
    container_image_reference_type: runtime.container_image_reference_type || (options.image ? "configured" : ""),
    container_image_digest_pinned: typeof runtime.container_image_digest_pinned === "boolean" ? runtime.container_image_digest_pinned : (options.image ? options.image.includes("@sha256:") : undefined),
    container_image_production_ready: typeof runtime.container_image_production_ready === "boolean" ? runtime.container_image_production_ready : (doc.isolation_profile === "production" ? options.pull_policy === "never" && String(options.image || "").includes("@sha256:") : undefined),
    container_pull_policy: runtime.container_pull_policy || options.pull_policy || "",
    sandbox_features: firstArray(runtime.sandbox_features, doc.sandbox_features, risk.sandbox_features),
    missing_sandbox_features: firstArray(runtime.missing_sandbox_features, doc.missing_sandbox_features, risk.missing_sandbox_features),
    windows_isolation: runtime.windows_isolation || doc.windows_isolation || risk.windows_isolation || null
  };
}

function toolContainerChips(container = {}) {
  const chips = [];
  const refType = String(container.container_image_reference_type || "").trim();
  if (refType) {
    chips.push(`<span class="resource-risk-chip ${containerImageReferenceTone(refType)}">${escapeHTML(containerImageReferenceLabel(refType))}</span>`);
  }
  if (typeof container.container_image_digest_pinned === "boolean") {
    chips.push(`<span class="resource-risk-chip ${container.container_image_digest_pinned ? "good" : "warn"}">${escapeHTML(container.container_image_digest_pinned ? t("catalog.containerDigestPinned") : t("catalog.containerDigestNotPinned"))}</span>`);
  }
  if (typeof container.container_image_production_ready === "boolean") {
    chips.push(`<span class="resource-risk-chip ${container.container_image_production_ready ? "good" : "warn"}">${escapeHTML(container.container_image_production_ready ? t("catalog.containerProductionReady") : t("catalog.containerProductionNotReady"))}</span>`);
  }
  if (container.container_pull_policy) {
    chips.push(`<span class="resource-risk-chip ${containerPullPolicyTone(container.container_pull_policy)}">${escapeHTML(containerPullPolicyLabel(container.container_pull_policy))}</span>`);
  }
  if (Array.isArray(container.sandbox_features) && container.sandbox_features.length) {
    chips.push(`<span class="resource-risk-chip good">${escapeHTML(t("catalog.sandboxEnabledCount", { count: container.sandbox_features.length }))}</span>`);
  }
  if (Array.isArray(container.missing_sandbox_features) && container.missing_sandbox_features.length) {
    chips.push(`<span class="resource-risk-chip warn">${escapeHTML(t("catalog.sandboxMissingCount", { count: container.missing_sandbox_features.length }))}</span>`);
  }
  return chips;
}

function containerImageReferenceTone(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized === "digest") return "good";
  if (normalized === "floating" || normalized === "missing") return "warn";
  return "neutral";
}

function containerImageReferenceLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `catalog.containerImageRef.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function containerPullPolicyTone(value) {
  const normalized = String(value || "").toLowerCase();
  if (normalized === "never") return "good";
  if (normalized === "always") return "warn";
  return "neutral";
}

function containerPullPolicyLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `catalog.pullPolicy.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function isolationProfileDisplayLabel(value) {
  const profile = isolationProfileOptions().find(item => item.name === value);
  if (profile) return isolationProfileLabel(profile);
  const key = `catalog.isolationProfile.${value}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function firstArray(...values) {
  const found = values.find(value => Array.isArray(value) && value.length);
  return found ? found.slice(0, 8) : [];
}

function sandboxFeatureListLabel(features = []) {
  return features.slice(0, 4).map(sandboxFeatureLabel).join(", ");
}

function sandboxFeatureLabel(value) {
  const normalized = String(value || "").trim().toLowerCase();
  const key = `catalog.sandboxFeature.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function windowsIsolationLabel(profile = {}) {
  const labels = [
    profile.job_object ? t("catalog.windowsIsolation.jobObject") : "",
    profile.restricted_token ? t("catalog.windowsIsolation.restrictedToken") : "",
    profile.app_container ? t("catalog.windowsIsolation.appContainer") : "",
    profile.lifecycle_only ? t("catalog.windowsIsolation.lifecycleOnly") : ""
  ].filter(Boolean);
  return labels.join(", ") || t("catalog.windowsIsolation.none");
}

function toolRiskTone(level, risk = {}) {
  const value = String(level || "").toLowerCase();
  if (risk.destructive || value === "critical" || value === "high") return "bad";
  if (risk.requires_approval || risk.external_sandbox_recommended || risk.workspace_scoped_inputs && risk.workspace_scope_enforced === false || value === "medium") return "warn";
  if (risk.sandboxed || value === "low") return "good";
  return "neutral";
}

function toolRiskLabel(level) {
  const value = String(level || "").toLowerCase();
  const key = `catalog.toolRisk.${value}`;
  const translated = t(key);
  return translated === key ? level : translated;
}

function renderTeamTemplate(template) {
  const name = template?.name || "team-template";
  const title = localizedText(template?.title || name);
  const source = template?.source || (template?.custom ? "custom" : "built_in");
  const tags = Array.isArray(template?.tags) ? template.tags.slice(0, 4).map(localizedText).filter(Boolean) : [];
  const quorumCount = Array.isArray(template?.quorum_presets) ? template.quorum_presets.length : Number(template?.quorum_presets || 0);
  const version = template?.version ? `v${template.version}` : "";
  return `<div class="item resource-item resource-template-item" ${resourceItemAttrs("team-template", name, ["team_template"])}>
    <strong>${escapeHTML(title)}</strong>
    ${resourceKicker([sourceChip(source, template?.custom), name, `${template?.roles || 0} ${t("catalog.teamRolesShort")}`])}
    <div class="muted">${escapeHTML(localizedText(template?.description || t("catalog.teamTemplate")))}</div>
    ${renderTemplateMeta([
      [t("catalog.teamRecommendedWorkflow"), template?.recommended_workflow || "-"],
      [t("catalog.teamEntryAgent"), template?.recommended_entry_agent || "-"],
      [t("catalog.teamQuorumPresets"), String(quorumCount)]
    ])}
    ${renderTemplateTags([...tags, version].filter(Boolean))}
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="team-template" data-resource-name="${escapeHTML(name)}">${template?.custom ? t("catalog.edit") : t("catalog.editCopy")}</button>
      ${template?.custom ? `<button class="danger" data-delete-team-template="${escapeHTML(name)}">${t("catalog.delete")}</button>` : ""}
    </div>
  </div>`;
}

function renderWorkflowTemplate(template) {
  const name = template?.name || "workflow-template";
  const title = localizedText(template?.title || name);
  const source = template?.source || (template?.custom ? "custom" : "built_in");
  const tags = Array.isArray(template?.tags) ? template.tags.slice(0, 5).map(localizedText).filter(Boolean) : [];
  const category = localizedText(template?.category || "workflow");
  const stages = Number(template?.stages || template?.graph?.stages?.length || 0);
  return `<div class="item resource-item resource-template-item" ${resourceItemAttrs("workflow-template", name, ["workflow_template"])}>
    <strong>${escapeHTML(title)}</strong>
    ${resourceKicker([sourceChip(source, template?.custom), name, `${stages} ${t("catalog.stagesShort")}`])}
    <div class="muted">${escapeHTML(localizedText(template?.description || t("catalog.workflowTemplate")))}</div>
    ${renderTemplateMeta([
      [t("catalog.teamCategory"), category],
      [t("catalog.schemaStages"), String(stages)],
      template?.path ? [t("catalog.resourcePath"), shortResourcePath(template.path), template.path] : null
    ])}
    ${renderTemplateTags(tags)}
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="workflow-template" data-resource-name="${escapeHTML(name)}">${template?.custom ? t("catalog.edit") : t("catalog.forkTemplate")}</button>
      ${template?.custom ? `<button class="danger" data-delete-workflow-template="${escapeHTML(name)}">${t("catalog.delete")}</button>` : ""}
    </div>
  </div>`;
}

function renderTemplateMeta(items = []) {
  const html = items
    .filter(item => Array.isArray(item) && String(item[1] || "").trim())
    .map(([label, value, title]) => `<span${title ? ` title="${escapeHTML(title)}"` : ""}><small>${escapeHTML(label)}</small><strong>${escapeHTML(String(value))}</strong></span>`)
    .join("");
  return html ? `<div class="resource-template-meta">${html}</div>` : "";
}

function renderTemplateTags(tags = []) {
  const html = tags
    .filter(tag => String(tag || "").trim())
    .map(tag => `<span>${escapeHTML(String(tag))}</span>`)
    .join("");
  return html ? `<div class="resource-template-tags">${html}</div>` : "";
}

function renderObservedWorkflowSchema(schema) {
  const name = schema?.workflow || "workflow";
  const stages = workflowSchemaStageCount(schema);
  const outputs = workflowSchemaOutputCount(schema);
  return `<div class="item resource-item workflow-schema-item" ${resourceItemAttrs("workflow-schema", name, ["workflow_schema"])}>
    <strong>${escapeHTML(name)}</strong>
    ${resourceKicker([sourceChip("runtime", false), `${stages} ${t("catalog.schemaStages")}`, `${outputs} ${t("catalog.schemaOutputs")}`])}
    <div class="muted">${escapeHTML(t("catalog.observedWorkflowSchemaDescription"))}</div>
    <div class="resource-facts">
      ${workflowSchemaFacts(schema).join("")}
    </div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="workflow-schema" data-resource-name="${escapeHTML(name)}">${t("catalog.editCopy")}</button>
      ${renderResourceActionButton(catalogContext.resourceCapabilities, "workflow-schema", "capture", name, { fallback: `<button data-capture-workflow-schema="${escapeHTML(name)}">${t("catalog.captureWorkflowSchema")}</button>` })}
    </div>
  </div>`;
}

function renderWorkflowSchemaResource(resource) {
  const name = resource?.name || resource?.workflow || "workflow-schema";
  const valid = resource?.valid !== false;
  return `<div class="item resource-item workflow-schema-item ${valid ? "" : "warning"}" ${resourceItemAttrs("workflow-schema", name, ["workflow_schema"])}>
    <strong>${escapeHTML(name)}</strong>
    ${resourceKicker([sourceChip("custom", true), resource?.workflow || name, valid ? t("catalog.schemaValid") : t("catalog.schemaInvalid")])}
    <div class="muted">${escapeHTML(localizedText(resource?.description || t("catalog.workflowSchemaDescriptionDefault")))}</div>
    <div class="resource-facts">
      ${workflowSchemaFacts(resource).join("")}
      ${resource?.error ? `<span title="${escapeHTML(resource.error)}">${escapeHTML(localizedCatalogErrorMessage(resource.error, t("catalog.schemaInvalid")))}</span>` : ""}
      ${resource?.path ? `<span title="${escapeHTML(resource.path)}">${escapeHTML(shortResourcePath(resource.path))}</span>` : ""}
    </div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="workflow-schema" data-resource-name="${escapeHTML(name)}">${t("catalog.edit")}</button>
      ${renderResourceActionButton(catalogContext.resourceCapabilities, "workflow-schema", "activate", name, { fallback: `<button data-activate-workflow-schema="${escapeHTML(name)}">${t("catalog.activateWorkflowSchema")}</button>` })}
      <button class="danger" data-delete-workflow-schema="${escapeHTML(name)}">${t("catalog.delete")}</button>
    </div>
  </div>`;
}

function workflowSchemaFacts(schemaOrResource = {}) {
  const schema = schemaOrResource.schema || schemaOrResource;
  const runs = Array.isArray(schema?.run_ids) ? schema.run_ids.length : 0;
  const updated = schemaOrResource.updated_at || schema?.updated_at || "";
  return [
    `<span>${escapeHTML(t("catalog.schemaStages"))}: ${escapeHTML(String(schemaOrResource.stages ?? workflowSchemaStageCount(schema)))}</span>`,
    `<span>${escapeHTML(t("catalog.schemaOutputs"))}: ${escapeHTML(String(schemaOrResource.outputs ?? workflowSchemaOutputCount(schema)))}</span>`,
    runs ? `<span>${escapeHTML(t("catalog.schemaRuns"))}: ${escapeHTML(String(runs))}</span>` : "",
    updated ? `<span>${escapeHTML(t("catalog.schemaUpdated"))}: ${escapeHTML(updated)}</span>` : ""
  ].filter(Boolean);
}

function workflowSchemaStageCount(schema = {}) {
  return Object.keys(schema?.stages || {}).length;
}

function workflowSchemaOutputCount(schema = {}) {
  return Object.values(schema?.stages || {}).reduce((sum, stage) => sum + Object.keys(stage?.outputs || {}).length, 0);
}

function renderKitResourceSections(kits = [], scaffolds = []) {
  const saved = Array.isArray(kits) ? kits : [];
  const presets = Array.isArray(scaffolds) ? scaffolds : [];
  const savedSection = saved.length || !presets.length ? `<section id="resourceKitsSection" class="kit-resource-section kit-saved-section ${saved.length ? "" : "kit-resource-empty-section"}">
    <div class="kit-resource-section-head">
      <div>
        <strong>${escapeHTML(t("catalog.savedKits"))}</strong>
        <span>${escapeHTML(t("catalog.savedKitsHelp"))}</span>
      </div>
      ${saved.length ? `<span class="badge">${escapeHTML(String(saved.length))}</span>` : ""}
    </div>
    <div id="resourceKitsList" class="list kit-saved-list">${saved.length ? saved.map(renderKit).join("") : empty(t("catalog.noKits"), { actionLabel: t("catalog.newKit"), action: "kit" })}</div>
  </section>` : "";
  return [
    presets.length ? `<section id="resourceKitScaffolds" class="kit-resource-section kit-scaffold-list">${renderKitScaffolds(presets)}</section>` : "",
    savedSection
  ].filter(Boolean).join("");
}

function renderKitScaffolds(scaffolds = []) {
  const items = sortKitScaffolds(Array.isArray(scaffolds) ? scaffolds : []);
  if (!items.length) return "";
  return `<div class="kit-scaffold-head">
    <div>
      <strong>${escapeHTML(t("catalog.kitScaffolds"))}</strong>
      <span>${escapeHTML(t("catalog.kitScaffoldsHelp"))}</span>
    </div>
  </div>
  <div class="kit-scaffold-grid">
    ${items.map(renderKitScaffold).join("")}
  </div>`;
}

function sortKitScaffolds(scaffolds = []) {
  return [...scaffolds].sort((a, b) => {
    const rank = kitScaffoldRank(a) - kitScaffoldRank(b);
    if (rank !== 0) return rank;
    return localizedText(a?.title || kitScaffoldName(a)).localeCompare(localizedText(b?.title || kitScaffoldName(b)), undefined, { sensitivity: "base" });
  });
}

function kitScaffoldName(scaffold) {
  return String(scaffold?.name || scaffold?.default_name || scaffold?.defaultKitName || scaffold?.title || "kit").trim() || "kit";
}

function kitScaffoldResourceCounts(scaffold = {}) {
  return [
    ["providers", t("catalog.kitProviders")],
    ["agents", t("catalog.kitAgents")],
    ["skills", t("catalog.kitSkills")],
    ["tools", t("catalog.kitTools")],
    ["workflows", t("catalog.kitWorkflows")],
    ["workflow_templates", t("catalog.kitWorkflowTemplates")],
    ["team_templates", t("catalog.kitTeamTemplates")],
    ["policy_rules", t("catalog.kitPolicyRules")]
  ]
    .map(([key, label]) => ({ key, label, count: Array.isArray(scaffold?.[key]) ? scaffold[key].length : 0, items: Array.isArray(scaffold?.[key]) ? scaffold[key] : [] }))
    .filter(item => item.count > 0);
}

function renderKitScaffoldLinks(scaffold = {}) {
  const counts = kitScaffoldResourceCounts(scaffold);
  if (!counts.length) return "";
  return `<div class="kit-scaffold-links" aria-label="${escapeHTML(t("catalog.kitScaffoldLinkedResources"))}">
    ${counts.map(item => `<span title="${escapeHTML(item.items.join(", "))}">
      <small>${escapeHTML(item.label)}</small>
      <strong>${escapeHTML(String(item.count))}</strong>
    </span>`).join("")}
  </div>`;
}

function firstArrayValue(values) {
  return Array.isArray(values) && values.length ? String(values[0] || "").trim() : "";
}

function firstKitExample(source = {}) {
  return Array.isArray(source?.examples) ? source.examples.find(item => item && (item.title || item.description || item.request || item.workflow || item.agent)) || null : null;
}

function kitRecommendedValue(source = {}, keys = [], fallback = "") {
  const example = firstKitExample(source) || {};
  const metadata = source?.metadata || {};
  for (const key of keys) {
    const value = source?.[key] || metadata?.[key] || example?.[key];
    if (value !== null && value !== undefined && String(value).trim()) return String(value).trim();
  }
  return fallback || "";
}

function kitRunPath(source = {}, options = {}) {
  const agentRefs = options.saved ? source?.agent_refs : source?.agents;
  const skillRefs = options.saved ? source?.skill_refs : source?.skills;
  const workflowRefs = options.saved ? (source?.workflow_template_refs || source?.workflow_refs) : (source?.workflow_templates || source?.workflows);
  const teamRefs = options.saved ? source?.team_template_refs : source?.team_templates;
  return [
    { kind: "agent", label: t("catalog.runPathAgent"), value: kitRecommendedValue(source, ["recommended_agent"], firstArrayValue(agentRefs)), hint: t("catalog.runPathAgentHelp") },
    { kind: "workflow-template", label: t("catalog.runPathWorkflow"), value: kitRecommendedValue(source, ["recommended_workflow"], firstArrayValue(workflowRefs)), hint: t("catalog.runPathWorkflowHelp") },
    { kind: "team-template", label: t("catalog.runPathTeam"), value: kitRecommendedValue(source, ["recommended_team"], firstArrayValue(teamRefs)), hint: t("catalog.runPathTeamHelp") },
    { kind: "skill", label: t("catalog.runPathSkill"), value: kitRecommendedValue(source, ["primary_skill", "recommended_skill"], firstArrayValue(skillRefs)), hint: t("catalog.runPathSkillHelp") }
  ].filter(item => item.value);
}

function renderKitRunPath(source = {}, options = {}) {
  const path = kitRunPath(source, options);
  if (!path.length) return "";
  const workflow = path.find(item => item.kind === "workflow-template")?.value || "";
  return `<div class="kit-run-path">
    <div class="kit-run-path-head">
      <div>
        <strong>${escapeHTML(t("catalog.runPathTitle"))}</strong>
        <span>${escapeHTML(t("catalog.runPathHelp"))}</span>
      </div>
      ${workflow ? `<button type="button" data-open-kit-workflow="${escapeHTML(workflow)}">${escapeHTML(t("catalog.openRecommendedWorkflow"))}</button>` : ""}
    </div>
    <div class="kit-run-path-grid">
      ${path.map(item => `<button type="button" class="kit-run-path-item" data-kit-focus-kind="${escapeHTML(item.kind)}" data-kit-focus-name="${escapeHTML(item.value)}" title="${escapeHTML(item.hint)}">
        <span>${escapeHTML(item.label)}</span>
        <strong>${escapeHTML(item.value)}</strong>
      </button>`).join("")}
    </div>
  </div>`;
}

function renderKitResourceChain(source = {}, options = {}) {
  const groups = options.saved ? kitSummaryResourceCounts(source) : kitScaffoldResourceCounts(source);
  const ordered = [
    ["agents", "agent_refs", t("catalog.resourceAgent")],
    ["skills", "skill_refs", t("catalog.resourceSkill")],
    ["tools", "tool_refs", t("catalog.resourceTool")],
    ["workflow_templates", "workflow_template_refs", t("catalog.resourceWorkflow")],
    ["team_templates", "team_template_refs", t("catalog.resourceTeamTemplate")],
    ["policy_rules", "policy_rule_refs", t("catalog.resourcePolicyRule")]
  ];
  const steps = ordered
    .map(([scaffoldKey, savedKey, label]) => {
      const match = groups.find(item => item.key === scaffoldKey || item.refsKey === savedKey || item.label === label);
      return match?.count ? { label, count: match.count, items: match.items || [] } : null;
    })
    .filter(Boolean);
  if (!steps.length) return "";
  return `<div class="kit-resource-chain" aria-label="${escapeHTML(t("catalog.resourceChainTitle"))}">
    <div class="kit-resource-chain-head">
      <strong>${escapeHTML(t("catalog.resourceChainTitle"))}</strong>
      <span>${escapeHTML(t("catalog.resourceChainHelp"))}</span>
    </div>
    <div class="kit-resource-chain-steps">
      ${steps.map((step, index) => `<span class="kit-resource-chain-step" title="${escapeHTML((step.items || []).join(", "))}">
        <small>${escapeHTML(String(index + 1))}</small>
        <b>${escapeHTML(step.label)}</b>
        <em>${escapeHTML(String(step.count))}</em>
      </span>`).join("")}
    </div>
  </div>`;
}

function kitSummaryResourceCounts(kit = {}) {
  return [
    ["provider_refs", "providers", t("catalog.kitProviders")],
    ["agent_refs", "agents", t("catalog.kitAgents")],
    ["skill_refs", "skills", t("catalog.kitSkills")],
    ["tool_refs", "tools", t("catalog.kitTools")],
    ["workflow_refs", "workflows", t("catalog.kitWorkflows")],
    ["workflow_template_refs", "workflow_templates", t("catalog.kitWorkflowTemplates")],
    ["team_template_refs", "team_templates", t("catalog.kitTeamTemplates")],
    ["policy_rule_refs", "policy_rules", t("catalog.kitPolicyRules")]
  ]
    .map(([refsKey, countKey, label]) => {
      const items = Array.isArray(kit?.[refsKey]) ? kit[refsKey] : [];
      const count = items.length || Number(kit?.[countKey] || 0);
      return { refsKey, key: countKey, label, count, items };
    })
    .filter(item => item.count > 0);
}

function renderKitSummaryLinks(kit = {}) {
  const counts = kitSummaryResourceCounts(kit);
  if (!counts.length) return "";
  return `<div class="kit-scaffold-links kit-summary-links" aria-label="${escapeHTML(t("catalog.kitScaffoldLinkedResources"))}">
    ${counts.map(item => `<span title="${escapeHTML(item.items.join(", "))}">
      <small>${escapeHTML(item.label)}</small>
      <strong>${escapeHTML(String(item.count))}</strong>
    </span>`).join("")}
  </div>`;
}

function kitScaffoldRank(scaffold) {
  const name = kitScaffoldName(scaffold);
  const ranks = {
    "multi-domain-agent": 0,
    "agent-framework": 1,
    "software-engineering": 2,
    "operations-runbook": 3,
    "customer-support": 4
  };
  if (Object.prototype.hasOwnProperty.call(ranks, name)) return ranks[name];
  const category = String(scaffold?.category || "").toLowerCase();
  const tags = Array.isArray(scaffold?.tags) ? scaffold.tags.map(tag => String(tag || "").toLowerCase()) : [];
  if (category === "starter" || tags.includes("starter")) return 5;
  return 20;
}

function kitScaffoldBadge(scaffold) {
  const name = kitScaffoldName(scaffold);
  if (name === "multi-domain-agent") return { tone: "primary", label: t("catalog.kitScaffoldDefaultStarter") };
  if (name === "agent-framework") return { tone: "accent", label: t("catalog.kitScaffoldBuilderStarter") };
  if (name === "operations-runbook" || name === "customer-support") return { tone: "neutral", label: t("catalog.kitScaffoldDomainStarter") };
  const category = String(scaffold?.category || "").toLowerCase();
  const tags = Array.isArray(scaffold?.tags) ? scaffold.tags.map(tag => String(tag || "").toLowerCase()) : [];
  if (category === "starter" || tags.includes("starter")) return { tone: "neutral", label: t("catalog.kitScaffoldStarter") };
  return null;
}

function renderKitScaffold(scaffold) {
  const name = kitScaffoldName(scaffold);
  const refs = kitScaffoldResourceCounts(scaffold).slice(0, 5).map(item => `${item.label}: ${item.count}`);
  const tags = Array.isArray(scaffold?.tags) ? scaffold.tags.slice(0, 4).map(localizedText).filter(Boolean) : [];
  const badge = kitScaffoldBadge(scaffold);
  const recommendation = [
    scaffold?.recommended_workflow ? `${t("catalog.teamRecommendedWorkflow")}: ${scaffold.recommended_workflow}` : "",
    scaffold?.recommended_agent ? `${t("catalog.teamEntryAgent")}: ${scaffold.recommended_agent}` : ""
  ].filter(Boolean);
  const example = Array.isArray(scaffold?.examples) ? scaffold.examples.find(item => item && (item.title || item.description || item.request)) : null;
  const valid = scaffold?.validation?.valid !== false;
  return `<article class="kit-scaffold-card ${badge ? "kit-scaffold-card-featured" : ""}">
    <div class="kit-scaffold-main">
      <div>
        <div class="kit-scaffold-title-row">
          <strong>${escapeHTML(localizedText(scaffold?.title || name))}</strong>
          ${badge ? `<span class="kit-scaffold-badge ${escapeHTML(badge.tone)}">${escapeHTML(badge.label)}</span>` : ""}
        </div>
        <div class="kit-scaffold-meta">
          <span>${escapeHTML(name)}</span>
          ${scaffold?.category ? `<span>${escapeHTML(localizedText(scaffold.category))}</span>` : ""}
        </div>
        ${tags.length ? `<div class="kit-scaffold-tags">${tags.map(tag => `<span>${escapeHTML(tag)}</span>`).join("")}</div>` : ""}
      </div>
      <p>${escapeHTML(localizedText(scaffold?.description || t("catalog.kitDescriptionDefault")))}</p>
      ${recommendation.length ? `<div class="kit-scaffold-recommendation">${recommendation.map(item => `<span>${escapeHTML(localizedText(item))}</span>`).join("")}</div>` : ""}
      ${renderKitRunPath(scaffold)}
      ${renderKitResourceChain(scaffold)}
      ${renderKitScaffoldLinks(scaffold)}
      ${example ? `<div class="kit-scaffold-example">
        <small>${escapeHTML(t("catalog.kitScaffoldExample"))}</small>
        <strong>${escapeHTML(localizedText(example.title || example.workflow || name))}</strong>
        ${example.description ? `<span>${escapeHTML(localizedText(example.description))}</span>` : ""}
      </div>` : ""}
    </div>
    <label class="kit-materialize-option">
      <input type="checkbox" data-kit-materialize>
      <span>
        <strong>${escapeHTML(t("catalog.kitMaterializeOption"))}</strong>
        <small>${escapeHTML(t("catalog.kitMaterializeHelp"))}</small>
      </span>
    </label>
    <div class="kit-scaffold-footer">
      <div class="resource-facts">
        ${refs.map(item => `<span>${escapeHTML(item)}</span>`).join("")}
        <span>${escapeHTML(valid ? t("catalog.kitValid") : t("catalog.kitNeedsReview"))}</span>
      </div>
      <div class="kit-scaffold-actions">
        <button data-kit-scaffold="${escapeHTML(name)}">${escapeHTML(t("catalog.useKitScaffold"))}</button>
        <button class="primary" data-kit-scaffold="${escapeHTML(name)}" data-force-materialize="true">${escapeHTML(t("catalog.starterCreateFull"))}</button>
      </div>
    </div>
  </article>`;
}

function renderSavedKitList(kits = []) {
  const saved = Array.isArray(kits) ? kits : [];
  return saved.length ? saved.map(renderKit).join("") : empty(t("catalog.noKits"), { actionLabel: t("catalog.newKit"), action: "kit" });
}

function renderPolicyRuleResourceSections(policyRules = [], scaffolds = []) {
  const rules = Array.isArray(policyRules) ? policyRules : [];
  const presets = Array.isArray(scaffolds) ? scaffolds : [];
  const savedSection = rules.length || !presets.length ? `<section id="resourcePolicyRulesSection" class="kit-resource-section kit-saved-section ${rules.length ? "" : "kit-resource-empty-section"}">
    <div class="kit-resource-section-head">
      <div>
        <strong>${escapeHTML(t("catalog.savedPolicyRules"))}</strong>
        <span>${escapeHTML(t("catalog.savedPolicyRulesHelp"))}</span>
      </div>
      ${rules.length ? `<span class="badge">${escapeHTML(String(rules.length))}</span>` : ""}
    </div>
    <div id="resourcePolicyRulesList" class="list kit-saved-list">${rules.length ? rules.map(renderPolicyRule).join("") : empty(t("catalog.noPolicyRules"), { actionLabel: t("catalog.newPolicyRule"), action: "policy-rule" })}</div>
  </section>` : "";
  return [
    presets.length ? `<section id="resourcePolicyRuleScaffolds" class="kit-resource-section kit-scaffold-list policy-rule-scaffold-list">${renderPolicyRuleScaffolds(presets)}</section>` : "",
    savedSection
  ].filter(Boolean).join("");
}

function renderPolicyRuleScaffolds(scaffolds = []) {
  const items = Array.isArray(scaffolds) ? scaffolds : [];
  if (!items.length) return "";
  return `<div class="kit-scaffold-head">
    <div>
      <strong>${escapeHTML(t("catalog.policyRuleScaffolds"))}</strong>
      <span>${escapeHTML(t("catalog.policyRuleScaffoldsHelp"))}</span>
    </div>
  </div>
  <div class="kit-scaffold-grid policy-rule-scaffold-grid">
    ${items.map(renderPolicyRuleScaffold).join("")}
  </div>`;
}

function renderPolicyRuleScaffold(scaffold) {
  const name = scaffold?.name || "policy-rule";
  const operator = scaffold?.operator || "expression";
  const defaults = Object.keys(scaffold?.defaults || {}).slice(0, 3);
  const params = Array.isArray(scaffold?.params) ? scaffold.params.length : 0;
  const tags = Array.isArray(scaffold?.tags) ? scaffold.tags.slice(0, 4).map(localizedText).join(", ") : "";
  return `<article class="kit-scaffold-card policy-rule-scaffold-card">
    <div class="kit-scaffold-main">
      <div>
        <strong>${escapeHTML(policyRuleScaffoldTitle(scaffold))}</strong>
        <small>${escapeHTML([name, localizedText(scaffold?.category || ""), tags].filter(Boolean).join(" / "))}</small>
      </div>
      <p>${escapeHTML(policyRuleScaffoldDescription(scaffold))}</p>
    </div>
    <div class="kit-scaffold-footer">
      <div class="resource-facts">
        <span>${escapeHTML(t("catalog.policyOperator"))}: ${escapeHTML(enumLabel("policyOperator", operator))}</span>
        <span>${escapeHTML(t("catalog.params"))}: ${escapeHTML(String(params))}</span>
        ${defaults.length ? `<span>${escapeHTML(t("catalog.policyDefaults"))}: ${escapeHTML(defaults.join(", "))}</span>` : ""}
      </div>
      <button data-policy-rule-scaffold="${escapeHTML(name)}">${escapeHTML(t("catalog.usePolicyRuleScaffold"))}</button>
    </div>
  </article>`;
}

function sortToolScaffolds(scaffolds = []) {
  return [...scaffolds].sort((a, b) => {
    const rank = toolScaffoldRank(a) - toolScaffoldRank(b);
    if (rank !== 0) return rank;
    return toolScaffoldTitle(a).localeCompare(toolScaffoldTitle(b), undefined, { sensitivity: "base" });
  });
}

function toolScaffoldName(scaffold = {}) {
  return String(scaffold.name || scaffold.default_name || scaffold.title || "python-local").trim() || "python-local";
}

function toolScaffoldRank(scaffold = {}) {
  const name = toolScaffoldName(scaffold);
  const ranks = {
    "python-container-readonly": 0,
    "python-container-writer": 1,
    "python-container-network": 2,
    "python-container-production": 3,
    "python-local": 10
  };
  if (Object.prototype.hasOwnProperty.call(ranks, name)) return ranks[name];
  if (String(scaffold.isolation || "").toLowerCase() === "container") return 5;
  return 20;
}

function toolScaffoldTitle(scaffold = {}) {
  const name = toolScaffoldName(scaffold);
  const key = `catalog.toolScaffold.${name}.title`;
  const translated = t(key);
  return translated === key ? localizedText(scaffold.title || name) : translated;
}

function toolScaffoldDescription(scaffold = {}) {
  const name = toolScaffoldName(scaffold);
  const key = `catalog.toolScaffold.${name}.description`;
  const translated = t(key);
  return translated === key ? localizedText(scaffold.description || t("catalog.toolScaffoldDescriptionDefault")) : translated;
}

function containerNetworkLabel(value) {
  const normalized = String(value || "").toLowerCase();
  const key = `catalog.containerNetwork.${normalized}`;
  const translated = t(key);
  return translated === key ? localizedText(value) : translated;
}

function renderKit(kit) {
  const name = kit?.name || "kit";
  const title = localizedText(kit?.title || name);
  const tags = Array.isArray(kit?.tags) ? kit.tags.slice(0, 5).map(localizedText).join(", ") : "";
  const valid = kit?.valid !== false;
  const issueCount = Array.isArray(kit?.issues) ? kit.issues.length : 0;
  const refGroups = kitSummaryResourceCounts(kit);
  const totalRefs = refGroups.reduce((sum, item) => sum + Number(item.count || 0), 0);
  return `<div class="item resource-item kit-resource-item" ${resourceItemAttrs("kit", name)}>
    <strong>${escapeHTML(title)}</strong>
    ${resourceKicker([sourceChip("custom", true), name, localizedText(kit?.category || t("catalog.resourceKit"))])}
    <div class="muted kit-resource-description">${escapeHTML(localizedText(kit?.description || t("catalog.kitDescriptionDefault")))}</div>
    ${renderKitRunPath(kit, { saved: true })}
    ${renderKitResourceChain(kit, { saved: true })}
    ${renderKitSummaryLinks(kit)}
    <div class="resource-facts">
      <span>${escapeHTML(t("catalog.kitRefs"))}: ${escapeHTML(String(totalRefs))}</span>
      <span>${escapeHTML(valid ? t("catalog.kitValid") : t("catalog.kitNeedsReview"))}</span>
      ${issueCount ? `<span>${escapeHTML(t("catalog.kitIssueCount", { count: issueCount }))}</span>` : ""}
      ${tags ? `<span>${escapeHTML(tags)}</span>` : ""}
      ${kit?.path ? `<span title="${escapeHTML(kit.path)}">${escapeHTML(shortResourcePath(kit.path))}</span>` : ""}
    </div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="kit" data-resource-name="${escapeHTML(name)}">${t("catalog.edit")}</button>
      ${renderResourceActionButton(catalogContext.resourceCapabilities, "kit", "validate_saved", name, { fallback: "" })}
      ${renderResourceActionButton(catalogContext.resourceCapabilities, "kit", "export_bundle", name, { fallback: `<button data-export-kit="${escapeHTML(name)}">${t("catalog.exportKitBundle")}</button>` })}
      <button class="danger" data-delete-kit="${escapeHTML(name)}">${t("catalog.delete")}</button>
    </div>
  </div>`;
}

function renderNodeMetadataResource(resource) {
  const type = resource?.type || resource?.name || "node";
  const label = localizedText(resource?.label || nodeDisplayLabel(type));
  const fields = Array.isArray(resource?.fields) ? resource.fields.length : 0;
  const hints = Array.isArray(resource?.hints) ? resource.hints.length : 0;
  const source = resource?.source || (resource?.custom ? "custom_metadata" : "custom");
  const canDelete = metadataItemCanDelete(resource);
  return `<div class="item resource-item" ${resourceItemAttrs("node-metadata", type, ["workflow_node_metadata"])}>
    <strong>${escapeHTML(label)}</strong>
    ${resourceKicker([sourceChip(source, resource?.custom), type])}
    <div class="muted">${escapeHTML(localizedText(resource?.description || t("catalog.nodeMetadata")))}</div>
    <div class="resource-facts">
      <span>${escapeHTML(t("catalog.nodeFields"))}: ${escapeHTML(String(fields))}</span>
      <span>${escapeHTML(t("catalog.nodeHints"))}: ${escapeHTML(String(hints))}</span>
      ${resource?.path ? `<span title="${escapeHTML(resource.path)}">${escapeHTML(shortResourcePath(resource.path))}</span>` : ""}
    </div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="node-metadata" data-resource-name="${escapeHTML(type)}">${canDelete ? t("catalog.edit") : t("catalog.editCopy")}</button>
      ${canDelete ? `<button class="danger" data-delete-node-metadata="${escapeHTML(type)}">${t("catalog.delete")}</button>` : ""}
    </div>
  </div>`;
}

function renderExpressionHelperResource(resource) {
  const name = resource?.name || "helper";
  const label = localizedText(resource?.label || expressionDisplayLabel(name));
  const modes = Array.isArray(resource?.modes) ? resource.modes.join(", ") : "";
  const examples = Array.isArray(resource?.examples) ? resource.examples.length : 0;
  const source = resource?.source || (resource?.custom ? "custom_metadata" : "custom");
  const canDelete = metadataItemCanDelete(resource);
  return `<div class="item resource-item" ${resourceItemAttrs("expression-helper", name, ["expression_helper"])}>
    <strong>${escapeHTML(label)}</strong>
    ${resourceKicker([sourceChip(source, resource?.custom), name])}
    <div class="muted">${escapeHTML(localizedText(resource?.description || t("catalog.expressionHelper")))}</div>
    <div class="resource-facts">
      ${resource?.signature ? `<span>${escapeHTML(resource.signature)}</span>` : ""}
      ${modes ? `<span>${escapeHTML(modes)}</span>` : ""}
      <span>${escapeHTML(t("catalog.expressionExamples"))}: ${escapeHTML(String(examples))}</span>
      ${resource?.path ? `<span title="${escapeHTML(resource.path)}">${escapeHTML(shortResourcePath(resource.path))}</span>` : ""}
    </div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="expression-helper" data-resource-name="${escapeHTML(name)}">${canDelete ? t("catalog.edit") : t("catalog.editCopy")}</button>
      ${canDelete ? `<button class="danger" data-delete-expression-helper="${escapeHTML(name)}">${t("catalog.delete")}</button>` : ""}
    </div>
  </div>`;
}

function metadataItemCanDelete(resource = {}) {
  if (!resource || typeof resource !== "object") return false;
  if (resource.custom === true || resource.path) return true;
  const source = String(resource.source || "").toLowerCase();
  return source.includes("custom") || source.includes("file");
}

function renderProvider(provider) {
  const id = provider?.id || provider?.name || provider?.provider || "provider";
  const type = provider?.type || provider?.kind || provider?.driver || "-";
  const model = provider?.default_model || provider?.model || provider?.defaultModel || "-";
  return `<div class="item resource-item" ${resourceItemAttrs("provider", id)}>
    <strong>${escapeHTML(id)}</strong>
    <span class="muted">${escapeHTML(type)}</span>
    <div class="muted">${t("common.model")}: ${escapeHTML(model)}</div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="provider" data-resource-name="${escapeHTML(id)}">${t("catalog.edit")}</button>
    </div>
  </div>`;
}

function renderPolicyRule(rule) {
  const name = rule?.name || "policy-rule";
  const label = rule?.label || name;
  const operator = rule?.operator || "expression";
  const paramCount = Array.isArray(rule?.params) ? rule.params.length : 0;
  return `<div class="item resource-item" ${resourceItemAttrs("policy-rule", name, ["policy_rule"])}>
    <strong>${escapeHTML(label)}</strong>
    ${resourceKicker([sourceChip("custom", true), name, enumLabel("policyOperator", operator)])}
    <div class="muted">${escapeHTML(localizedText(rule?.description || t("catalog.policyRule")))}</div>
    <div class="muted">${t("catalog.params")}: ${escapeHTML(String(paramCount))}</div>
    <div class="hero-actions">
      <button data-edit-resource data-resource-type="policy-rule" data-resource-name="${escapeHTML(name)}">${t("catalog.edit")}</button>
    </div>
  </div>`;
}

function policyRuleListHTML(policyRules = [], scaffolds = []) {
  const rules = Array.isArray(policyRules) ? policyRules : [];
  if (rules.length) return rules.map(renderPolicyRule).join("");
  return empty(t("catalog.noPolicyRules"), { actionLabel: t("catalog.newPolicyRule"), action: "policy-rule" });
}

function resourceKicker(items) {
  const html = items
    .filter(item => item !== null && item !== undefined && String(item).trim())
    .map(item => String(item).startsWith("<span") ? item : `<span>${escapeHTML(String(item))}</span>`)
    .join("");
  return `<div class="resource-kicker">${html}</div>`;
}

function sourceChip(source, custom) {
  const normalized = String(source || "").toLowerCase();
  const isCustom = Boolean(custom) || normalized.includes("custom");
  const isBuiltIn = normalized.includes("built") || normalized === "builtin";
  const label = isCustom ? t("catalog.sourceCustom") : isBuiltIn ? t("catalog.sourceBuiltIn") : source || t("catalog.sourceRuntime");
  const tone = isCustom ? "custom" : isBuiltIn ? "builtin" : "runtime";
  return `<span class="resource-source-chip ${tone}">${escapeHTML(label)}</span>`;
}

function selectedPolicyRuleScaffold(root) {
  const name = root.querySelector("#policyScaffoldPreset")?.value || "";
  return (catalogContext.policyRuleScaffolds || []).find(scaffold => scaffold.name === name) || catalogContext.policyRuleScaffolds?.[0] || null;
}

function selectedToolScaffold(root) {
  const name = root.querySelector("#toolScaffoldPreset")?.value || "";
  return (catalogContext.toolScaffolds || []).find(scaffold => scaffold.name === name) || catalogContext.toolScaffolds?.[0] || null;
}

function previewToolScaffold(root) {
  renderToolScaffoldPreview(root, selectedToolScaffold(root));
}

function renderToolScaffoldPreview(root, scaffold, doc = null) {
  const node = root.querySelector("#toolScaffoldPreview");
  if (!node) return;
  if (!scaffold?.name) {
    node.innerHTML = `<div class="resource-empty">${escapeHTML(t("catalog.noToolScaffolds"))}</div>`;
    return;
  }
  const defaults = normalizeStringMap(scaffold.default_isolation_options || doc?.isolation_options || {});
  const facts = [
    [t("catalog.toolScaffoldPreset"), scaffold.name],
    [t("catalog.language"), scaffold.language || doc?.language || "python"],
    [t("catalog.isolation"), enumLabel("isolation", doc?.isolation || scaffold.isolation || "")],
    [t("catalog.isolationProfile"), isolationProfileDisplayLabel(doc?.isolation_profile || scaffold.isolation_profile || "")],
    [t("catalog.isolationImage"), doc?.isolation_options?.image || scaffold.default_image || ""],
    [t("catalog.isolationWorkspaceMount"), doc?.isolation_options?.workspace_mount || scaffold.workspace_mount || ""],
    [t("catalog.isolationNetwork"), containerNetworkLabel(doc?.isolation_options?.network || scaffold.network_mode || "")]
  ].filter(([, value]) => String(value || "").trim());
  const defaultsHTML = Object.entries(defaults).slice(0, 10).map(([key, value]) => `<span><small>${escapeHTML(key)}</small><strong>${escapeHTML(resourceDisplayValue(value))}</strong></span>`).join("");
  const guidance = [
    ...(Array.isArray(scaffold.recommendations) ? scaffold.recommendations : []),
    ...(Array.isArray(scaffold.activation_steps) ? scaffold.activation_steps : [])
  ].slice(0, 4);
  node.innerHTML = `<div class="tool-scaffold-preview-card">
    <div>
      <strong>${escapeHTML(toolScaffoldTitle(scaffold))}</strong>
      <span>${escapeHTML(toolScaffoldDescription(scaffold))}</span>
    </div>
    ${facts.length ? `<div class="resource-facts compact">${facts.map(([label, value]) => `<span>${escapeHTML(label)}: ${escapeHTML(resourceDisplayValue(value))}</span>`).join("")}</div>` : ""}
    ${defaultsHTML ? `<div class="resource-template-meta tool-scaffold-defaults">${defaultsHTML}</div>` : ""}
    ${guidance.length ? `<div class="tool-scaffold-guidance">${guidance.map(item => `<span>${escapeHTML(localizedText(item))}</span>`).join("")}</div>` : ""}
  </div>`;
}

function toolScaffoldOutcomeSections(result = {}, preset = {}) {
  const actualPreset = result?.preset || preset || {};
  const tool = result?.tool || {};
  const sections = [];
  const generated = Array.isArray(actualPreset.generated_paths) ? actualPreset.generated_paths : [];
  const steps = Array.isArray(actualPreset.activation_steps) ? actualPreset.activation_steps : [];
  const recommendations = Array.isArray(actualPreset.recommendations) ? actualPreset.recommendations : [];
  const options = normalizeStringMap(tool.isolation_options || actualPreset.default_isolation_options || {});
  if (generated.length) {
    sections.push({
      title: t("catalog.toolScaffoldGeneratedPaths"),
      html: `<div class="resource-save-facts">${generated.map(path => `<span class="resource-save-fact"><strong>${escapeHTML(path)}</strong></span>`).join("")}</div>`
    });
  }
  if (steps.length) {
    sections.push({
      title: t("catalog.toolScaffoldActivation"),
      html: `<ol class="resource-save-list">${steps.map(step => `<li>${escapeHTML(localizedText(step))}</li>`).join("")}</ol>`
    });
  }
  if (recommendations.length) {
    sections.push({
      title: t("catalog.toolScaffoldRecommendations"),
      html: `<div class="tool-scaffold-guidance">${recommendations.map(item => `<span>${escapeHTML(localizedText(item))}</span>`).join("")}</div>`
    });
  }
  if (Object.keys(options).length) {
    sections.push({
      title: t("catalog.defaultIsolationOptions"),
      html: `<div class="resource-template-meta tool-scaffold-defaults">${Object.entries(options).slice(0, 12).map(([key, value]) => `<span><small>${escapeHTML(key)}</small><strong>${escapeHTML(resourceDisplayValue(value))}</strong></span>`).join("")}</div>`
    });
  }
  return sections;
}

function toolScaffoldRequestBody(root, preset = selectedToolScaffold(root)) {
  const rawName = normalizeName(root.querySelector("#resourceName")?.value || "");
  const name = rawName && rawName !== "custom-tool" ? rawName : normalizeName(preset?.default_name || preset?.name || "custom-tool");
  const options = collectToolIsolationOptions(root);
  return cleanEmptyFields({
    name,
    description: root.querySelector("#resourceDescription")?.value?.trim() || preset?.description || "",
    image: root.querySelector("#toolIsolationImage")?.value?.trim() || preset?.default_image || "",
    runtime: root.querySelector("#toolIsolationRuntime")?.value?.trim() || preset?.default_isolation_options?.runtime || "",
    workspace_mount: root.querySelector("#toolIsolationWorkspaceMount")?.value?.trim() || preset?.workspace_mount || preset?.default_isolation_options?.workspace_mount || "",
    network: root.querySelector("#toolIsolationNetwork")?.value?.trim() || preset?.network_mode || preset?.default_isolation_options?.network || "",
    isolation_profile: root.querySelector("#toolIsolationProfile")?.value?.trim() || preset?.isolation_profile || "",
    overwrite: Boolean(root.querySelector("#toolScaffoldOverwrite")?.checked),
    isolation_options: options
  });
}

function policyRuleScaffoldRequestBody(root, preset) {
  const defaults = parseMap(root.querySelector("#policyDefaults")?.value || "");
  if (String(preset?.operator || "").toLowerCase() === "expression") {
    const expression = root.querySelector("#policyExpression")?.value?.trim();
    if (expression) defaults.expression = expression;
  }
  return {
    name: normalizeName(root.querySelector("#resourceName")?.value || preset?.default_name || preset?.name || "policy-rule"),
    label: root.querySelector("#policyLabel")?.value?.trim() || policyRuleScaffoldTitle(preset),
    description: root.querySelector("#resourceDescription")?.value?.trim() || policyRuleScaffoldDescription(preset),
    reason: root.querySelector("#policyReason")?.value?.trim() || t("catalog.policyReasonDefault"),
    defaults,
    overwrite: Boolean(root.querySelector("#policyScaffoldOverwrite")?.checked)
  };
}

function previewPolicyRuleScaffold(root) {
  renderPolicyRuleScaffoldPreview(root, selectedPolicyRuleScaffold(root));
}

function renderPolicyRuleScaffoldPreview(root, scaffold, doc = null) {
  const node = root.querySelector("#policyScaffoldPreview");
  if (!node) return;
  if (!scaffold?.name) {
    node.innerHTML = `<div class="item muted">${escapeHTML(t("catalog.noPolicyRuleScaffolds"))}</div>`;
    return;
  }
  const document = doc || scaffold.document || null;
  const defaults = document?.defaults || scaffold.defaults || {};
  const params = Array.isArray(document?.params) ? document.params : Array.isArray(scaffold.params) ? scaffold.params : [];
  node.innerHTML = `<div class="policy-scaffold-preview-card">
    <div>
      <strong>${escapeHTML(policyRuleScaffoldTitle(scaffold))}</strong>
      <span>${escapeHTML(policyRuleScaffoldDescription(scaffold))}</span>
    </div>
    <div class="resource-facts">
      <span>${escapeHTML(t("catalog.name"))}: ${escapeHTML(document?.name || scaffold.default_name || scaffold.name)}</span>
      <span>${escapeHTML(t("catalog.policyOperator"))}: ${escapeHTML(document?.operator || scaffold.operator || "expression")}</span>
      <span>${escapeHTML(t("catalog.params"))}: ${escapeHTML(String(params.length))}</span>
      ${Object.keys(defaults).length ? `<span>${escapeHTML(t("catalog.policyDefaults"))}: ${escapeHTML(Object.keys(defaults).slice(0, 4).join(", "))}</span>` : ""}
    </div>
  </div>`;
}

function policyRuleScaffoldTitle(scaffold = {}) {
  const name = scaffold?.name || "";
  const key = `catalog.policyRuleScaffold.${name}.title`;
  const translated = t(key);
  return translated === key ? localizedText(scaffold?.title || name || t("catalog.policyRuleScaffolds")) : translated;
}

function policyRuleScaffoldDescription(scaffold = {}) {
  const name = scaffold?.name || "";
  const key = `catalog.policyRuleScaffold.${name}.description`;
  const translated = t(key);
  return translated === key ? localizedText(scaffold?.description || t("catalog.policyRuleScaffoldsHelp")) : translated;
}

function empty(text, options = {}) {
  const action = options.action
    ? `<button type="button" class="resource-empty-action" data-new-resource="${escapeHTML(options.action)}">${escapeHTML(options.actionLabel || t("catalog.createResource"))}</button>`
    : options.view
      ? `<button type="button" class="resource-empty-action" data-open-view="${escapeHTML(options.view)}">${escapeHTML(options.actionLabel || t("catalog.openRun"))}</button>`
      : "";
  return `<div class="resource-empty muted">
    <span>${escapeHTML(text)}</span>
    ${action}
  </div>`;
}

function parityItem(title, body) {
  return `<div class="item parity-item"><strong>${escapeHTML(title)}</strong><span class="muted">${escapeHTML(body)}</span></div>`;
}

function commonLabel(key) {
  return t(`common.${key}`);
}

function enumLabel(scope, value) {
  const raw = String(value || "");
  if (!raw) return "-";
  const key = `catalog.${scope}.${raw}`;
  const translated = t(key);
  return translated === key ? raw : translated;
}

function optionHTML(value, label) {
  return `<option value="${escapeHTML(value)}">${escapeHTML(label)}</option>`;
}

function enumOptionsHTML(scope, values) {
  return values.map(value => optionHTML(value, enumLabel(scope, value))).join("");
}

function modeOptionsHTML() {
  return enumOptionsHTML("mode", ["chat", "plan", "fix", "audit"]);
}

function policyOptionsHTML() {
  return enumOptionsHTML("toolPolicy", ["confirm", "allow", "deny"]);
}

function isolationOptionsHTML() {
  return enumOptionsHTML("isolation", ["process_group", "none", "windows_job", "linux_cgroup", "container"]);
}

function isolationProfileOptionsHTML(selected = "") {
  const profiles = isolationProfileOptions();
  return [
    optionHTML("", t("catalog.isolationProfileDefault")),
    ...profiles.map(profile => optionHTML(profile.name, isolationProfileLabel(profile)))
  ].map(html => {
    const value = html.match(/value="([^"]*)"/)?.[1] || "";
    return value === escapeHTML(selected) ? html.replace("<option", "<option selected") : html;
  }).join("");
}

function isolationProfileOptions() {
  const modes = catalogContext.runtime?.mcp_isolation_modes || catalogContext.runtime?.capabilities?.mcp_isolation_modes || [];
  const containerMode = Array.isArray(modes) ? modes.find(mode => mode?.name === "container") : null;
  const profiles = Array.isArray(containerMode?.profiles)
    ? containerMode.profiles.filter(profile => profile?.name)
    : [];
  if (profiles.length) return profiles;
  return ["readonly", "writer", "network", "production"].map(name => ({ name, label: t(`catalog.isolationProfile.${name}`) }));
}

function isolationProfileLabel(profile = {}) {
  return localizedText(profile.label || t(`catalog.isolationProfile.${profile.name}`) || profile.name);
}

function optionalEnumOptionsHTML(scope, values, selected = "") {
  return [optionHTML("", t("catalog.inheritDefault")), ...values.map(value => optionHTML(value, enumLabel(scope, value)))].map(html => {
    const value = html.match(/value="([^"]*)"/)?.[1] || "";
    return value === escapeHTML(selected) ? html.replace("<option", "<option selected") : html;
  }).join("");
}

function policyOperatorOptionsHTML() {
  return enumOptionsHTML("policyOperator", ["expression", "ref_truthy", "contains", "min_count", "risk_at_least", "team_approval_gate"]);
}

async function openDesigner(root, runtime, type, name, providerOptions = []) {
  const designer = root.querySelector("#resourceDesigner");
  designer.classList.remove("hidden");
  root.querySelector("#resourceType").value = type || "skill";
  clearDesigner(root, runtime, providerOptions);
  if (type === "skill" && name) {
    await loadSkillByName(root, runtime, name);
  } else if (type === "agent" && name) {
    await loadAgentByName(root, runtime, name, providerOptions);
  } else if (type === "provider" && name) {
    await loadProviderByName(root, name);
  } else if (type === "policy-rule" && name) {
    await loadPolicyRuleByName(root, name);
  } else if (type === "team-template" && name) {
    await loadTeamTemplateByName(root, name);
  } else if (type === "kit" && name) {
    await loadKitByName(root, name);
  } else if (type === "workflow-template" && name) {
    await loadWorkflowTemplateByName(root, name);
  } else if (type === "workflow-schema" && name) {
    await loadWorkflowSchemaByName(root, name);
  } else if (type === "node-metadata" && name) {
    await loadWorkflowNodeMetadataByName(root, name);
  } else if (type === "expression-helper" && name) {
    await loadExpressionHelperByName(root, name);
  } else if (type === "tool" && name) {
    await loadToolByName(root, name);
  } else if (type === "workflow" && name) {
    loadWorkflowForm(root, { name, description: t("catalog.resourceWorkflow") });
  }
  syncDesignerMode(root);
  applyConfigDiagnosticsToDesigner(root);
}

function closeDesigner(root) {
  root.querySelector("#resourceDesigner").classList.add("hidden");
}

function clearDesigner(root, runtime, providerOptions = []) {
  const type = root.querySelector("#resourceType").value || "skill";
  const defaultName = type === "skill" ? "custom-skill" : type === "agent" ? "custom-agent" : type === "tool" ? "custom-tool" : type === "team-template" ? "custom-review-team" : type === "kit" ? "custom-kit" : type === "workflow-template" ? defaultWorkflowTemplateName() : type === "workflow-schema" ? defaultWorkflowSchemaName() : type === "node-metadata" ? defaultNodeMetadataType() : type === "expression-helper" ? defaultExpressionHelperName() : type === "provider" ? "primary" : type === "workflow" ? "custom-workflow" : type === "policy-rule" ? "critical-finding" : "";
  const defaultDescription = type === "skill" ? t("catalog.customSkillDescription") : type === "team-template" ? t("catalog.teamTemplateDescriptionDefault") : type === "kit" ? t("catalog.kitDescriptionDefault") : type === "workflow-template" ? t("catalog.workflowTemplateDescriptionDefault") : type === "workflow-schema" ? t("catalog.workflowSchemaDescriptionDefault") : type === "node-metadata" ? t("catalog.nodeMetadataDescriptionDefault") : type === "expression-helper" ? t("catalog.expressionHelperDescriptionDefault") : type === "provider" ? t("catalog.providersHelp") : type === "policy-rule" ? t("catalog.policyRuleDescriptionDefault") : "";
  root.querySelector("#resourceName").value = defaultName;
  root.querySelector("#resourcePurpose").value = "";
  root.querySelector("#resourceDescription").value = defaultDescription;
  root.querySelector("#resourceDetails").value = "";
  root.dataset.resourceDesignerClearing = "true";
  clearResourceBrief(root);
  loadSkillForm(root, runtime, null);
  loadAgentForm(root, runtime, null, providerOptions);
  loadToolForm(root, type === "tool" ? "custom-tool" : "", null);
  loadTeamTemplateForm(root, null);
  loadKitForm(root, null);
  loadWorkflowTemplateResourceForm(root, null);
  loadWorkflowSchemaForm(root, null);
  loadNodeMetadataForm(root, null);
  loadExpressionHelperForm(root, null);
  loadProviderForm(root, null);
  loadWorkflowForm(root, null);
  if (type === "policy-rule") loadPolicyRuleForm(root, null);
  delete root.dataset.resourceDesignerClearing;
  root.querySelector("#resourceName").value = defaultName;
  root.querySelector("#resourceDescription").value = defaultDescription;
  root.querySelector("#resourceOutput").textContent = "";
  clearResourceBrief(root);
  seedResourceBrief(root, { description: defaultDescription }, { preserve: false });
  syncDesignerMode(root);
}

function syncDesignerMode(root) {
  const type = root.querySelector("#resourceType").value;
  const isSkill = type === "skill";
  root.querySelector("#designerTitle").textContent = isSkill ? t("catalog.skillEditor") : type === "agent" ? t("catalog.agentDesigner") : type === "tool" ? t("catalog.toolDesigner") : type === "team-template" ? t("catalog.teamTemplateDesigner") : type === "kit" ? t("catalog.kitDesigner") : type === "workflow-template" ? t("catalog.workflowTemplateDesigner") : type === "workflow-schema" ? t("catalog.workflowSchemaDesigner") : type === "node-metadata" ? t("catalog.nodeMetadataDesigner") : type === "expression-helper" ? t("catalog.expressionHelperDesigner") : type === "provider" ? t("catalog.providerDesigner") : type === "policy-rule" ? t("catalog.policyRuleDesigner") : t("catalog.workflowDesigner");
  root.querySelectorAll(".skill-only").forEach(node => node.classList.toggle("hidden", !isSkill));
  root.querySelectorAll(".agent-only").forEach(node => node.classList.toggle("hidden", type !== "agent"));
  root.querySelectorAll(".tool-only").forEach(node => node.classList.toggle("hidden", type !== "tool"));
  root.querySelectorAll(".team-template-only").forEach(node => node.classList.toggle("hidden", type !== "team-template"));
  root.querySelectorAll(".kit-only").forEach(node => node.classList.toggle("hidden", type !== "kit"));
  root.querySelectorAll(".workflow-template-only").forEach(node => node.classList.toggle("hidden", type !== "workflow-template"));
  root.querySelectorAll(".workflow-schema-only").forEach(node => node.classList.toggle("hidden", type !== "workflow-schema"));
  root.querySelectorAll(".node-metadata-only").forEach(node => node.classList.toggle("hidden", type !== "node-metadata"));
  root.querySelectorAll(".expression-helper-only").forEach(node => node.classList.toggle("hidden", type !== "expression-helper"));
  root.querySelectorAll(".provider-only").forEach(node => node.classList.toggle("hidden", type !== "provider"));
  root.querySelectorAll(".workflow-only").forEach(node => node.classList.toggle("hidden", type !== "workflow"));
  root.querySelectorAll(".policy-rule-only").forEach(node => node.classList.toggle("hidden", type !== "policy-rule"));
  const workflowTemplateMode = type === "workflow-template" ? root.querySelector("#workflowTemplateSaveMode")?.value : "";
  const workflowTemplateForkMode = workflowTemplateMode === "fork";
  const workflowTemplateCaptureMode = workflowTemplateMode === "capture";
  root.querySelectorAll(".workflow-template-source-field").forEach(node => node.classList.toggle("hidden", workflowTemplateCaptureMode));
  root.querySelectorAll(".workflow-template-capture-field").forEach(node => node.classList.toggle("hidden", !workflowTemplateCaptureMode));
  root.querySelectorAll(".workflow-template-graph-field").forEach(node => node.classList.toggle("hidden", workflowTemplateForkMode || workflowTemplateCaptureMode));
  root.querySelectorAll(".prompt-only").forEach(node => node.classList.toggle("hidden", type === "skill" || type === "agent" || type === "tool" || type === "team-template" || type === "kit" || type === "workflow-template" || type === "workflow-schema" || type === "node-metadata" || type === "expression-helper" || type === "provider" || type === "workflow" || type === "policy-rule"));
  root.querySelectorAll("[data-resource-field='purpose']").forEach(node => node.classList.toggle("hidden", type !== "agent"));
  root.querySelector("#saveResource").textContent = type === "skill" ? t("catalog.saveSkill") : type === "agent" ? t("catalog.saveAgent") : type === "tool" ? t("catalog.saveTool") : type === "team-template" ? t("catalog.saveTeamTemplate") : type === "kit" ? t("catalog.saveKit") : type === "workflow-template" ? t("catalog.saveWorkflowTemplate") : type === "workflow-schema" ? t("catalog.saveWorkflowSchema") : type === "node-metadata" ? t("catalog.saveNodeMetadata") : type === "expression-helper" ? t("catalog.saveExpressionHelper") : type === "provider" ? t("catalog.saveProvider") : type === "policy-rule" ? t("catalog.savePolicyRule") : t("catalog.saveWorkflow");
  const moduleButton = root.querySelector("[data-designer-section='module']");
  if (moduleButton) moduleButton.textContent = t("catalog.designerSectionModule", { type: resourceTypeLabel(type) });
  setDesignerSectionActive(root, root.querySelector("#resourceDesigner .designer-panel")?.dataset.activeDesignerSection || "identity");
  root.querySelector("#validateResource").classList.toggle("hidden", !resourceValidationEnabled(root, type));
  root.querySelector("#validateResource").textContent = t("catalog.validateResource");
  root.querySelector("#openBuilder").classList.toggle("hidden", type === "skill" || type === "agent" || type === "tool" || type === "team-template" || type === "kit" || type === "workflow-template" || type === "workflow-schema" || type === "node-metadata" || type === "expression-helper" || type === "provider" || type === "workflow" || type === "policy-rule");
  renderResourceCapabilitySummary(root, type);
  if (type === "policy-rule") {
    fillPolicyRuleScaffoldSelect(root);
  }
  if (type === "tool") {
    fillToolScaffoldSelect(root);
  }
  updateToolIsolationNetworkHint(root);
  updateResourceBriefPreview(root);
  renderResourceDependencyPickers(root);
}

function scrollDesignerSection(root, section = "identity") {
  const target = designerSectionTarget(root, section);
  if (!target) return;
  const panel = root.querySelector("#resourceDesigner .designer-panel");
  target.scrollIntoView({ block: "start", behavior: prefersReducedMotion() ? "auto" : "smooth" });
  if (panel) panel.dataset.activeDesignerSection = section;
  setDesignerSectionActive(root, section);
}

function setDesignerSectionActive(root, section = "identity") {
  root.querySelectorAll("[data-designer-section]").forEach(button => {
    const active = button.dataset.designerSection === section;
    button.classList.toggle("active", active);
    if (active) {
      button.setAttribute("aria-current", "true");
    } else {
      button.removeAttribute("aria-current");
    }
  });
}

function designerSectionTarget(root, section) {
  if (section === "actions") return root.querySelector("#resourceDesigner .designer-actions");
  if (section === "result") return root.querySelector("#resourceOutput");
  if (section === "brief") return root.querySelector("[data-designer-section-panel='brief']") || root.querySelector("#resourceBriefPanel");
  if (section === "module") {
    return [
      ".skill-only:not(.hidden)",
      ".agent-only:not(.hidden)",
      ".tool-only:not(.hidden)",
      ".team-template-only:not(.hidden)",
      ".kit-only:not(.hidden)",
      ".workflow-template-only:not(.hidden)",
      ".workflow-schema-only:not(.hidden)",
      ".node-metadata-only:not(.hidden)",
      ".expression-helper-only:not(.hidden)",
      ".provider-only:not(.hidden)",
      ".workflow-only:not(.hidden)",
      ".policy-rule-only:not(.hidden)"
    ].map(selector => root.querySelector(selector)).find(Boolean) || root.querySelector("#resourceType");
  }
  return root.querySelector("[data-designer-section-panel='identity']") || root.querySelector("#resourceType");
}

function clearResourceBrief(root) {
  root.querySelectorAll("[data-resource-brief-field]").forEach(input => {
    input.value = "";
  });
  updateResourceBriefPreview(root);
}

function seedResourceBrief(root, source = {}, options = {}) {
  if (root?.dataset?.resourceDesignerClearing === "true" && options.forceDuringClear !== true) return;
  const preserve = options.preserve === true;
  const setIfBlank = (selector, value) => {
    const input = root.querySelector(selector);
    if (!input || preserve && input.value.trim()) return;
    input.value = String(value || "").trim();
  };
  setIfBlank("#resourceBriefGoal", source.goal || source.purpose || source.description || "");
  setIfBlank("#resourceBriefInputs", formatBriefSeedList(source.inputs || source.params || source.fields));
  setIfBlank("#resourceBriefOutputs", formatBriefSeedList(source.outputs || source.output_contract || source.outputKind || source.output_kind));
  setIfBlank("#resourceBriefExample", formatBriefSeedList(source.examples || source.example || source.request));
  setIfBlank("#resourceBriefDependencies", formatBriefSeedList(source.dependencies || source.tools || source.agents || source.skills || source.resources));
  updateResourceBriefPreview(root);
}

function formatBriefSeedList(value) {
  if (Array.isArray(value)) {
    return value
      .map(item => {
        if (typeof item === "string") return item;
        if (!item || typeof item !== "object") return "";
        return item.name || item.label || item.title || item.request || item.description || JSON.stringify(item);
      })
      .filter(Boolean)
      .join("\n");
  }
  if (value && typeof value === "object") {
    return Object.entries(value)
      .map(([key, item]) => {
        if (typeof item === "string") return `${key}: ${item}`;
        if (item && typeof item === "object") return `${key}: ${item.description || item.label || item.type || JSON.stringify(item)}`;
        return key;
      })
      .join("\n");
  }
  return String(value || "");
}

function resourceBrief(root) {
  return {
    goal: root.querySelector("#resourceBriefGoal")?.value.trim() || "",
    inputs: root.querySelector("#resourceBriefInputs")?.value.trim() || "",
    outputs: root.querySelector("#resourceBriefOutputs")?.value.trim() || "",
    example: root.querySelector("#resourceBriefExample")?.value.trim() || "",
    dependencies: root.querySelector("#resourceBriefDependencies")?.value.trim() || ""
  };
}

function updateResourceBriefPreview(root) {
  const preview = root.querySelector("#resourceBriefPreview");
  if (!preview) return;
  const brief = resourceBrief(root);
  const items = [
    { label: t("catalog.briefGoal"), value: brief.goal },
    { label: t("catalog.briefInputs"), value: brief.inputs },
    { label: t("catalog.briefOutputs"), value: brief.outputs },
    { label: t("catalog.briefExample"), value: brief.example },
    { label: t("catalog.briefDependencies"), value: brief.dependencies }
  ].filter(item => item.value);
  if (!items.length) {
    preview.innerHTML = `<span>${escapeHTML(t("catalog.briefPreviewEmpty"))}</span>`;
    return;
  }
  preview.innerHTML = `
    <strong>${escapeHTML(t("catalog.briefPreviewTitle"))}</strong>
    <div>
      ${items.map(item => `<span><small>${escapeHTML(item.label)}</small><b>${escapeHTML(trimPreviewText(item.value, 120))}</b></span>`).join("")}
    </div>`;
}

function trimPreviewText(value, limit) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (!limit || text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 1)).trim()}...`;
}

function applyResourceBrief(root) {
  const type = root.querySelector("#resourceType")?.value || "skill";
  const brief = resourceBrief(root);
  const goal = brief.goal;
  const inputs = splitBriefLines(brief.inputs);
  const outputs = splitBriefLines(brief.outputs);
  const examples = splitBriefLines(brief.example);
  const dependencies = splitBriefLines(brief.dependencies);
  if (goal) {
    setValueIfBlank(root, "#resourceDescription", goal);
    setValueIfBlank(root, "#resourcePurpose", goal);
  }
  if (type === "skill") {
    applySkillBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "agent") {
    applyAgentBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "tool") {
    applyToolBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "workflow" || type === "workflow-template") {
    applyWorkflowBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "team-template") {
    applyTeamBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "kit") {
    applyKitBrief(root, brief, inputs, outputs, examples, dependencies);
  } else if (type === "policy-rule") {
    applyPolicyBrief(root, brief, inputs, outputs, examples);
  } else if (type === "node-metadata") {
    setValueIfBlank(root, "#nodeMetadataDescription", goal);
    if (outputs.length) setValueIfBlank(root, "#nodeMetadataOutputs", JSON.stringify(outputs.map(name => ({ name: normalizeName(name), description: name })), null, 2));
  } else if (type === "expression-helper") {
    setValueIfBlank(root, "#expressionHelperDescription", goal);
    if (examples.length) setValueIfBlank(root, "#expressionHelperExamples", examples.join("\n"));
  } else if (type === "provider") {
    setValueIfBlank(root, "#providerDescription", goal);
    if (outputs.length) setValueIfBlank(root, "#providerModels", outputs.join(", "));
  }
  updateResourceBriefPreview(root);
  const output = root.querySelector("#resourceOutput");
  if (output) output.textContent = t("catalog.briefApplied");
}

function splitBriefLines(value) {
  return String(value || "")
    .split(/\r?\n|,/)
    .map(item => item.trim())
    .filter(Boolean);
}

function setValueIfBlank(root, selector, value) {
  const input = root.querySelector(selector);
  if (!input || input.value.trim() || String(value || "").trim() === "") return;
  input.value = String(value || "").trim();
}

function appendTextareaLines(root, selector, values) {
  const input = root.querySelector(selector);
  if (!input || !values.length) return;
  const existing = splitBriefLines(input.value);
  const next = [...existing];
  const seen = new Set(existing.map(item => item.toLowerCase()));
  for (const value of values) {
    const normalized = String(value || "").trim();
    if (!normalized || seen.has(normalized.toLowerCase())) continue;
    next.push(normalized);
    seen.add(normalized.toLowerCase());
  }
  input.value = next.join("\n");
}

function applySkillBrief(root, brief, inputs, outputs, examples, dependencies) {
  const params = inputs.map(item => `${normalizeName(item)}|string|${item}|false`);
  appendTextareaLines(root, "#skillParams", params);
  if (outputs.length) setValueIfBlank(root, "#skillOutputKind", normalizeName(outputs[0]) || "summary");
  appendTextareaLines(root, "#skillTools", dependencies);
  appendTextareaLines(root, "#skillKeywords", [brief.goal, ...outputs].filter(Boolean).map(item => item.split(/\s+/).slice(0, 4).join(" ")));
  const instructions = resourceBriefInstructions(brief, inputs, outputs, examples, dependencies);
  setValueIfBlank(root, "#skillInstructions", instructions);
  setValueIfBlank(root, "#skillEmbeddingDescription", [brief.goal, ...outputs].filter(Boolean).join(" "));
}

function applyAgentBrief(root, brief, inputs, outputs, examples, dependencies) {
  appendInputList(root, "#agentAllowedTools", dependencies.filter(item => item.includes("/")));
  appendInputList(root, "#agentAllowedKinds", dependencies.filter(item => ["read", "write", "exec", "network"].includes(item.toLowerCase())));
  setValueIfBlank(root, "#agentSystemPrompt", resourceBriefInstructions(brief, inputs, outputs, examples, dependencies));
}

function applyToolBrief(root, brief, inputs, outputs, examples, dependencies) {
  if (dependencies.some(item => /web|http|api|network|url/i.test(item))) {
    const network = root.querySelector("#toolNetworkDisabled");
    if (network) network.checked = false;
  }
  if (inputs.length) {
    const schema = {
      type: "object",
      properties: Object.fromEntries(inputs.map(item => [normalizeName(item), { type: "string", description: item }])),
      additionalProperties: false
    };
    appendTextareaLines(root, "#toolCode", [
      "",
      `# ${t("catalog.briefGeneratedComment")}`,
      `# inputs: ${inputs.join(", ")}`,
      outputs.length ? `# outputs: ${outputs.join(", ")}` : ""
    ].filter(Boolean));
    root.querySelector("#toolCode")?.setAttribute("data-brief-schema", JSON.stringify(schema));
  }
}

function applyWorkflowBrief(root, brief, inputs, outputs, examples, dependencies) {
  if (examples.length) setValueIfBlank(root, "#resourceDetails", examples.join("\n"));
  if (dependencies.some(item => /audit|review|check|verify/i.test(item))) {
    const approval = root.querySelector("#workflowApproval");
    if (approval && approval.value === "none") approval.value = "audit";
  }
  setValueIfBlank(root, "#workflowTemplateCategory", normalizeName(outputs[0] || brief.goal || "custom"));
  appendInputList(root, "#workflowTemplateTags", [...outputs, ...dependencies].map(normalizeName).filter(Boolean));
}

function applyTeamBrief(root, brief, inputs, outputs, examples, dependencies) {
  appendInputList(root, "#teamTags", dependencies.map(normalizeName).filter(Boolean));
  appendTextareaLines(root, "#teamOutputContract", outputs.map(normalizeName).filter(Boolean));
  if (examples.length) appendTextareaLines(root, "#teamBlackboard", examples.map(item => `note|${item}|open|custom`));
}

function applyKitBrief(root, brief, inputs, outputs, examples, dependencies) {
  appendInputList(root, "#kitTags", outputs.map(normalizeName).filter(Boolean));
  appendInputList(root, "#kitAgents", dependencies.filter(item => /agent|planner|fixer|auditor/i.test(item)).map(normalizeName).filter(Boolean));
  appendInputList(root, "#kitSkills", dependencies.filter(item => /skill|plan|write|audit|review/i.test(item)).map(normalizeName).filter(Boolean));
  appendInputList(root, "#kitTools", dependencies.filter(item => item.includes("/")));
  if (examples.length) setValueIfBlank(root, "#kitExamples", JSON.stringify(examples.map(request => ({ title: request, request })), null, 2));
}

function applyPolicyBrief(root, brief, inputs, outputs, examples) {
  setValueIfBlank(root, "#policyReason", brief.goal || examples[0] || t("catalog.policyReasonDefault"));
  const ref = inputs[0] || "previous.raw_output";
  const needle = outputs[0] || examples[0] || "approved";
  setValueIfBlank(root, "#policyDefaults", formatMap({ ref, needle }));
  setValueIfBlank(root, "#policyExpression", `contains({{ref}}, "{{needle}}")`);
}

function appendInputList(root, selector, values) {
  const input = root.querySelector(selector);
  if (!input || !values.length) return;
  const existing = splitList(input.value);
  const next = [...existing];
  const seen = new Set(existing.map(item => item.toLowerCase()));
  for (const value of values) {
    const normalized = String(value || "").trim();
    if (!normalized || seen.has(normalized.toLowerCase())) continue;
    next.push(normalized);
    seen.add(normalized.toLowerCase());
  }
  input.value = next.join(", ");
}

function resourceBriefInstructions(brief, inputs, outputs, examples, dependencies) {
  return [
    `## ${t("catalog.briefInstructionRole")}`,
    brief.goal || "Describe what this resource should do.",
    "",
    inputs.length ? `## ${t("catalog.briefInstructionInputs")}\n` + inputs.map(item => `- ${item}`).join("\n") : "",
    outputs.length ? `## ${t("catalog.briefInstructionOutputs")}\n` + outputs.map(item => `- ${item}`).join("\n") : "",
    examples.length ? `## ${t("catalog.briefInstructionExamples")}\n` + examples.map(item => `- ${item}`).join("\n") : "",
    dependencies.length ? `## ${t("catalog.briefInstructionDependencies")}\n` + dependencies.map(item => `- ${item}`).join("\n") : "",
    `## ${t("catalog.briefInstructionConstraints")}`,
    `- ${t("catalog.briefInstructionConstraintSummary")}`,
    `- ${t("catalog.briefInstructionConstraintStructured")}`
  ].filter(Boolean).join("\n\n");
}

function bindToolIsolationHints(root) {
  const isolation = root.querySelector("#toolIsolation");
  const profile = root.querySelector("#toolIsolationProfile");
  const network = root.querySelector("#toolIsolationNetwork");
  const disabled = root.querySelector("#toolNetworkDisabled");
  if (isolation) isolation.onchange = () => {
    updateToolIsolationNetworkHint(root);
    updateToolIsolationProfileHint(root);
  };
  if (profile) profile.onchange = () => updateToolIsolationProfileHint(root);
  if (network) network.oninput = () => updateToolIsolationNetworkHint(root);
  if (disabled) {
    disabled.onchange = () => {
      if (disabled.checked && network && !network.value.trim()) network.value = "disabled";
      updateToolIsolationNetworkHint(root);
    };
  }
  updateToolIsolationNetworkHint(root);
  updateToolIsolationProfileHint(root);
}

function updateToolIsolationProfileHint(root) {
  const hint = root.querySelector("#toolIsolationProfileHint");
  if (!hint) return;
  const profileName = root.querySelector("#toolIsolationProfile")?.value || "";
  const isolation = root.querySelector("#toolIsolation")?.value || "";
  const profile = isolationProfileOptions().find(item => item.name === profileName);
  let tone = "neutral";
  let text = t("catalog.isolationProfileDefaultHint");
  if (profileName && isolation !== "container") {
    tone = "warn";
    text = t("catalog.isolationProfileContainerOnlyHint");
  } else if (profileName === "production") {
    tone = "warn";
    text = profile?.recommendation || profile?.description || t("catalog.isolationProfileProductionHint");
  } else if (profileName) {
    tone = profile?.risk_level === "low" ? "good" : "neutral";
    text = profile?.recommendation || profile?.description || t("catalog.isolationProfileSelectedHint", { profile: isolationProfileLabel(profile) });
  }
  hint.className = `resource-isolation-hint ${tone}`;
  hint.textContent = localizedText(text);
}

function updateToolIsolationNetworkHint(root) {
  const hint = root.querySelector("#toolIsolationNetworkHint");
  if (!hint) return;
  const isolation = root.querySelector("#toolIsolation")?.value || "";
  const disabled = !!root.querySelector("#toolNetworkDisabled")?.checked;
  const network = String(root.querySelector("#toolIsolationNetwork")?.value || "").trim().toLowerCase();
  let tone = "neutral";
  let key = "catalog.isolationNetworkInheritedHint";
  if (disabled || isNetworkDisabledValue(network)) {
    tone = "good";
    key = "catalog.isolationNetworkDisabledHint";
  } else if (isolation !== "container") {
    key = "catalog.isolationNetworkContainerOnlyHint";
  } else if (isNetworkOpenValue(network)) {
    tone = "warn";
    key = "catalog.isolationNetworkOpenHint";
  }
  hint.className = `resource-isolation-hint ${tone}`;
  hint.textContent = t(key);
}

function isNetworkDisabledValue(value) {
  return ["disabled", "none", "off", "false", "no"].includes(String(value || "").trim().toLowerCase());
}

function isNetworkOpenValue(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) return false;
  return !isNetworkDisabledValue(normalized);
}

function fillAgentSelect(root, runtime) {
  const select = root.querySelector("#skillAgent");
  select.innerHTML = "";
  for (const agent of runtime.agents || []) {
    const option = document.createElement("option");
    option.value = agent.id;
    option.textContent = `${agent.id} (${enumLabel("mode", agent.mode || "")})`;
    select.appendChild(option);
  }
}

function fillProviderSelect(root, runtime, providerOptions = []) {
  const select = root.querySelector("#agentProvider");
  const status = root.querySelector("#agentProviderStatus");
  if (!select) return;
  const runtimeOptions = normalizeProviderOptions(runtime?.providers || []);
  const fallbackOptions = normalizeProviderOptions(providerOptions);
  const options = runtimeOptions.length ? runtimeOptions : fallbackOptions;
  select.innerHTML = "";
  for (const provider of options) {
    const option = document.createElement("option");
    option.value = provider.id;
    option.textContent = provider.label || provider.id;
    if (provider.defaultModel) option.dataset.defaultModel = provider.defaultModel;
    select.appendChild(option);
  }
  if (!options.length) {
    const option = document.createElement("option");
    option.value = "primary";
    option.textContent = resourceDisplayValue("primary");
    select.appendChild(option);
  }
  if (status) {
    status.value = runtimeOptions.length ? t("catalog.providerSourceRuntime") : fallbackOptions.length ? t("catalog.providerSourceResource") : t("catalog.providerSourceEmpty");
  }
}

function fillWorkflowTemplateSelects(root) {
  const templates = catalogContext.workflowTemplates || [];
  const workflowGraphs = catalogContext.workflowGraphs || [];
  const workflowSelect = root.querySelector("#workflowTemplate");
  const sourceSelect = root.querySelector("#workflowTemplateSource");
  const captureSelect = root.querySelector("#workflowTemplateCaptureSource");
  if (workflowSelect) {
    workflowSelect.innerHTML = `<option value="blank">${escapeHTML(t("catalog.blankWorkflow"))}</option>${templates.map(template => {
      const name = template.name || "";
      return `<option value="${escapeHTML(name)}">${escapeHTML(localizedText(template.title || name))}</option>`;
    }).join("")}`;
  }
  if (sourceSelect) {
    sourceSelect.innerHTML = templates.map(template => {
      const name = template.name || "";
      return `<option value="${escapeHTML(name)}">${escapeHTML(localizedText(template.title || name))} (${escapeHTML(name)})</option>`;
    }).join("") || `<option value="">${escapeHTML(t("catalog.noWorkflowTemplates"))}</option>`;
  }
  if (captureSelect) {
    captureSelect.innerHTML = workflowGraphs.map(workflow => {
      const name = workflow.name || "";
      const description = localizedText(workflow.description || name);
      return `<option value="${escapeHTML(name)}">${escapeHTML(name)}${description && description !== name ? ` - ${escapeHTML(description)}` : ""}</option>`;
    }).join("") || `<option value="">${escapeHTML(t("catalog.noWorkflowGraphs"))}</option>`;
  }
}

function fillWorkflowSchemaSelect(root) {
  const select = root.querySelector("#workflowSchemaSource");
  if (!select) return;
  const schemas = catalogContext.workflowSchemas || [];
  select.innerHTML = schemas.map(schema => {
    const name = schema?.workflow || "";
    const detail = [
      `${workflowSchemaStageCount(schema)} ${t("catalog.schemaStages")}`,
      `${workflowSchemaOutputCount(schema)} ${t("catalog.schemaOutputs")}`
    ].join(" / ");
    return `<option value="${escapeHTML(name)}">${escapeHTML(name)} (${escapeHTML(detail)})</option>`;
  }).join("") || `<option value="">${escapeHTML(t("catalog.noObservedWorkflowSchemas"))}</option>`;
}

function fillMetadataResourceSelects(root) {
  const nodeSelect = root.querySelector("#nodeMetadataType");
  if (nodeSelect) {
    nodeSelect.innerHTML = (catalogContext.nodeTypes || []).map(option => {
      const type = option.type || "";
      return `<option value="${escapeHTML(type)}">${escapeHTML(localizedText(option.label || type))} (${escapeHTML(type)})</option>`;
    }).join("") || `<option value="custom">${escapeHTML(t("catalog.resourceNodeMetadata"))}</option>`;
  }
  const helperSelect = root.querySelector("#expressionHelperSource");
  if (helperSelect) {
    helperSelect.innerHTML = (catalogContext.expressionHelpers || []).map(helper => {
      const name = helper.name || "";
      return `<option value="${escapeHTML(name)}">${escapeHTML(localizedText(helper.label || name))} (${escapeHTML(name)})</option>`;
    }).join("") || `<option value="custom_helper">${escapeHTML(t("catalog.resourceExpressionHelper"))}</option>`;
  }
}

function fillToolScaffoldSelect(root) {
  const select = root.querySelector("#toolScaffoldPreset");
  if (!select) return;
  const scaffolds = sortToolScaffolds(catalogContext.toolScaffolds || []);
  select.innerHTML = scaffolds.map(scaffold => {
    const name = toolScaffoldName(scaffold);
    return `<option value="${escapeHTML(name)}">${escapeHTML(toolScaffoldTitle(scaffold))} (${escapeHTML(name)})</option>`;
  }).join("") || `<option value="">${escapeHTML(t("catalog.noToolScaffolds"))}</option>`;
  previewToolScaffold(root);
}

function fillPolicyRuleScaffoldSelect(root) {
  const select = root.querySelector("#policyScaffoldPreset");
  if (!select) return;
  const scaffolds = catalogContext.policyRuleScaffolds || [];
  select.innerHTML = scaffolds.map(scaffold => {
    const name = scaffold.name || "";
    const title = policyRuleScaffoldTitle(scaffold);
    return `<option value="${escapeHTML(name)}">${escapeHTML(title)} (${escapeHTML(name)})</option>`;
  }).join("") || `<option value="">${escapeHTML(t("catalog.noPolicyRuleScaffolds"))}</option>`;
  previewPolicyRuleScaffold(root);
}

function applyWorkflowTemplateSource(root) {
  const name = root.querySelector("#workflowTemplateSource").value;
  const template = (catalogContext.workflowTemplates || []).find(item => item.name === name);
  if (!template) {
    root.querySelector("#workflowTemplateSaveMode").value = "fork";
    root.querySelector("#workflowTemplateGraph").value = "";
    syncDesignerMode(root);
    return;
  }
  if (!root.querySelector("#resourceName").value || root.querySelector("#resourceName").value === defaultWorkflowTemplateName()) {
    root.querySelector("#resourceName").value = normalizeName(`${name}-copy`);
  }
  root.querySelector("#workflowTemplateTitle").value = t("catalog.copyOf", { name: localizedText(template.title || name) });
  root.querySelector("#resourceDescription").value = localizedText(template.description || t("catalog.workflowTemplateDescriptionDefault"));
  root.querySelector("#workflowTemplateCategory").value = template.category || "custom";
  root.querySelector("#workflowTemplateTags").value = Array.isArray(template.tags) ? [...new Set([...template.tags, "fork"])].join(", ") : "fork";
  root.querySelector("#workflowTemplateSaveMode").value = "fork";
  root.querySelector("#workflowTemplateGraph").value = "";
  syncDesignerMode(root);
}

function handleWorkflowTemplateModeChange(root) {
  const mode = root.querySelector("#workflowTemplateSaveMode")?.value || "";
  if (mode === "capture") {
    applyWorkflowTemplateCaptureSource(root);
    return;
  }
  if (mode === "fork") {
    applyWorkflowTemplateSource(root);
    return;
  }
  syncDesignerMode(root);
}

function applyWorkflowTemplateCaptureSource(root) {
  const name = root.querySelector("#workflowTemplateCaptureSource")?.value || "";
  const workflow = (catalogContext.workflowGraphs || []).find(item => item.name === name);
  if (!workflow) {
    root.querySelector("#workflowTemplateSaveMode").value = "capture";
    root.querySelector("#workflowTemplateGraph").value = "";
    syncDesignerMode(root);
    return;
  }
  const currentName = root.querySelector("#resourceName").value;
  if (!currentName || currentName === defaultWorkflowTemplateName()) {
    root.querySelector("#resourceName").value = normalizeName(`${name}-template`);
  }
  root.querySelector("#workflowTemplateTitle").value = workflow.name || name;
  root.querySelector("#resourceDescription").value = localizedText(workflow.description || t("catalog.workflowTemplateDescriptionDefault"));
  root.querySelector("#workflowTemplateCategory").value = "custom";
  root.querySelector("#workflowTemplateTags").value = "capture";
  root.querySelector("#workflowTemplateSaveMode").value = "capture";
  root.querySelector("#workflowTemplateGraph").value = "";
  syncDesignerMode(root);
}

function previewWorkflowSchemaSource(root) {
  const name = root.querySelector("#workflowSchemaSource")?.value || "";
  const schema = (catalogContext.workflowSchemas || []).find(item => item.workflow === name);
  if (!schema) return;
  const currentName = root.querySelector("#resourceName")?.value || "";
  if (!currentName || currentName === "custom-workflow-schema") {
    root.querySelector("#resourceName").value = normalizeName(schema.workflow || name);
  }
  if (!root.querySelector("#resourceDescription").value.trim()) {
    root.querySelector("#resourceDescription").value = t("catalog.workflowSchemaDescriptionDefault");
  }
}

function applyWorkflowSchemaSource(root) {
  const name = root.querySelector("#workflowSchemaSource")?.value || "";
  const schema = (catalogContext.workflowSchemas || []).find(item => item.workflow === name);
  if (!schema) return;
  loadWorkflowSchemaForm(root, workflowSchemaResourceFromSchema(schema, root.querySelector("#resourceDescription")?.value || ""));
  root.querySelector("#resourceOutput").textContent = `${t("catalog.loaded")} ${schema.workflow}`;
}

function applyNodeMetadataSource(root) {
  const type = root.querySelector("#nodeMetadataType").value;
  const option = (catalogContext.nodeTypes || []).find(item => item.type === type);
  root.querySelector("#resourceName").value = type || defaultNodeMetadataType();
  loadNodeMetadataForm(root, option || { type });
}

function applyExpressionHelperSource(root) {
  const name = root.querySelector("#expressionHelperSource").value;
  const option = (catalogContext.expressionHelpers || []).find(item => item.name === name);
  root.querySelector("#resourceName").value = name || defaultExpressionHelperName();
  loadExpressionHelperForm(root, option || { name });
}

async function loadSkillByName(root, runtime, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("skill", name, `/api/resources/skills/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadSkillForm(root, runtime, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadAgentByName(root, runtime, name, providerOptions = []) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("agent", name, `/api/resources/agents/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadAgentForm(root, runtime, doc, providerOptions);
    output.textContent = `${t("catalog.loaded")} ${doc.id || name}`;
  } catch {
    const agent = (runtime.agents || []).find(item => item.id === name);
    loadAgentForm(root, runtime, agent ? {
      id: agent.id,
      name: agent.name,
      description: agent.description,
      provider: agent.provider,
      model: agent.model,
      max_iterations: agent.max_iterations,
      allowed_tool_kinds: agent.allowed_tool_kinds,
      tool_policy: agent.tool_policy,
      mode: agent.mode
    } : null, providerOptions);
    output.textContent = `${t("catalog.loaded")} ${name}`;
  }
}

function renderSkillScripts(root, scripts = []) {
  const list = root.querySelector("#skillScriptsList");
  if (!list) return;
  const normalized = Array.isArray(scripts) ? scripts.filter(Boolean) : [];
  list.innerHTML = normalized.length
    ? normalized.map((script, index) => skillScriptCardHTML(script, index)).join("")
    : `<div class="resource-script-empty">${escapeHTML(t("catalog.skillScriptsEmpty"))}</div>`;
  updateSkillScriptsHidden(root);
}

function addSkillScriptCard(root, script = null) {
  const list = root.querySelector("#skillScriptsList");
  if (!list) return;
  list.querySelector(".resource-script-empty")?.remove();
  const next = script || defaultSkillScriptDraft(root);
  list.insertAdjacentHTML("beforeend", skillScriptCardHTML(next, list.querySelectorAll("[data-skill-script-card]").length));
  updateSkillScriptsHidden(root);
  const latest = list.querySelector("[data-skill-script-card]:last-child input[data-skill-script-field='name']");
  latest?.focus();
}

function skillScriptCardHTML(script = {}, index = 0) {
  const argsSchema = script.args_schema && typeof script.args_schema === "object"
    ? JSON.stringify(script.args_schema, null, 2)
    : "";
  const title = script.name || t("catalog.skillScriptNew");
  return `<article class="resource-script-card" data-skill-script-card>
    <div class="resource-script-card-head">
      <div>
        <small>${escapeHTML(t("catalog.skillScript"))} ${index + 1}</small>
        <strong data-skill-script-title>${escapeHTML(title)}</strong>
      </div>
      <button type="button" class="danger ghost" data-remove-skill-script>${t("catalog.removeSkillScript")}</button>
    </div>
    <div class="resource-script-grid">
      <label class="stack"><span>${t("catalog.name")}</span><input data-skill-script-field="name" value="${escapeHTML(script.name || "")}" placeholder="${examplePlaceholder("collect-context")}"></label>
      <label class="stack span-2"><span>${t("catalog.description")}</span><input data-skill-script-field="description" value="${escapeHTML(script.description || "")}" placeholder="${escapeHTML(t("catalog.skillScriptDescriptionPlaceholder"))}"></label>
      <label class="stack"><span>${t("catalog.skillScriptPath")}</span><input data-skill-script-field="path" value="${escapeHTML(script.path || "")}" placeholder="${examplePlaceholder("scripts/collect_context.py")}"></label>
      <label class="stack"><span>${t("catalog.skillScriptRuntime")}</span><select data-skill-script-field="runtime">${optionalEnumOptionsHTML("scriptRuntime", ["python", "node", "bash", "powershell", "go", "binary"], script.runtime || "")}</select></label>
      <label class="stack"><span>${t("catalog.skillScriptOutput")}</span><select data-skill-script-field="output">${optionalEnumOptionsHTML("scriptOutput", ["text", "json", "files", "artifact"], script.output || "")}</select></label>
      <label class="stack"><span>${t("catalog.timeout")}</span><input data-skill-script-field="timeout" value="${escapeHTML(script.timeout || "")}" placeholder="${examplePlaceholder("30s")}"></label>
      <label class="stack"><span>${t("catalog.isolation")}</span><select data-skill-script-field="isolation">${optionalEnumOptionsHTML("scriptIsolation", ["container", "process_group", "windows_job", "windows_restricted_token", "linux_cgroup", "linux_netns"], script.isolation || "")}</select></label>
      <label class="stack"><span>${t("catalog.skillScriptWorkspaceMount")}</span><select data-skill-script-field="workspace_mount">${optionalEnumOptionsHTML("scriptWorkspaceMount", ["none", "ro", "rw"], script.workspace_mount || "")}</select></label>
      <label class="stack"><span>${t("catalog.skillScriptNetwork")}</span><select data-skill-script-field="network">${optionalEnumOptionsHTML("scriptNetwork", ["disabled", "enabled", "host"], script.network || "")}</select></label>
      <label class="stack"><span>${t("catalog.skillScriptApproval")}</span><select data-skill-script-field="approval">${optionalEnumOptionsHTML("scriptApproval", ["required", "optional", "never"], script.approval || "")}</select></label>
      <label class="stack span-2"><span>${t("catalog.skillScriptArgsSchema")}</span><textarea data-skill-script-field="args_schema" class="compact-textarea code-mini" placeholder="${escapeHTML(t("catalog.skillScriptArgsSchemaPlaceholder"))}">${escapeHTML(argsSchema)}</textarea></label>
      <label class="stack"><span>${t("catalog.metadata")}</span><textarea data-skill-script-field="metadata" class="compact-textarea" placeholder="${escapeHTML(t("catalog.skillScriptMetadataPlaceholder"))}">${escapeHTML(formatMap(script.metadata || {}))}</textarea></label>
    </div>
  </article>`;
}

function defaultSkillScriptDraft(root) {
  const base = normalizeName(root.querySelector("#resourceName")?.value || "custom-skill");
  const name = base === "custom-skill" ? "helper" : `${base}-helper`;
  return {
    name,
    description: t("catalog.skillScriptDescriptionPlaceholder"),
    path: `scripts/${name.replaceAll("-", "_")}.py`,
    runtime: "python",
    output: "json",
    timeout: "30s",
    workspace_mount: "ro",
    network: "disabled",
    approval: "required",
    args_schema: { type: "object", additionalProperties: false }
  };
}

function ensureSkillScriptEmptyState(root) {
  const list = root.querySelector("#skillScriptsList");
  if (!list || list.querySelector("[data-skill-script-card]")) return;
  list.innerHTML = `<div class="resource-script-empty">${escapeHTML(t("catalog.skillScriptsEmpty"))}</div>`;
}

function updateSkillScriptCardTitle(card) {
  if (!card) return;
  const title = card.querySelector("[data-skill-script-title]");
  const name = card.querySelector("[data-skill-script-field='name']")?.value?.trim();
  if (title) title.textContent = name || t("catalog.skillScriptNew");
}

function updateSkillScriptsHidden(root) {
  const hidden = root.querySelector("#skillScriptsJSON");
  if (!hidden) return;
  try {
    hidden.value = JSON.stringify(collectSkillScripts(root, { validate: false }), null, 2);
  } catch {
    // Keep the last valid snapshot while the user is editing partial JSON.
  }
}

function syncSkillScriptsFromHidden(root) {
  const hidden = root.querySelector("#skillScriptsJSON");
  const list = root.querySelector("#skillScriptsList");
  if (!hidden || !list || !hidden.value.trim() || list.querySelector("[data-skill-script-card]")) return;
  try {
    const scripts = JSON.parse(hidden.value);
    renderSkillScripts(root, Array.isArray(scripts) ? scripts : []);
  } catch {
    // Ignore stale or partial snapshots; validation will handle invalid form content.
  }
}

function loadSkillForm(root, runtime, doc) {
  const fallbackAgent = runtime.active_agent || runtime.agents?.[0]?.id || "chat";
  root.querySelector("#resourceName").value = doc?.name || root.querySelector("#resourceName").value || "custom-skill";
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.customSkillDescription");
  root.querySelector("#skillVersion").value = doc?.version || "1.0.0";
  root.querySelector("#skillAuthor").value = doc?.author || "GoFlow Studio";
  root.querySelector("#skillPriority").value = doc?.priority || "";
  root.querySelector("#skillMaxIterations").value = doc?.max_iterations || "";
  root.querySelector("#skillMode").value = doc?.mode || "chat";
  root.querySelector("#skillAgent").value = doc?.preferred_agent || fallbackAgent;
  root.querySelector("#skillAllowedKinds").value = (doc?.allowed_tool_kinds || ["read"]).join(", ");
  root.querySelector("#skillOutputKind").value = doc?.output_kind || "summary";
  root.querySelector("#skillNext").value = (doc?.next_skills || []).join(", ");
  root.querySelector("#skillKeywords").value = (doc?.activation?.keywords || [doc?.name || "custom-skill"]).join("\n");
  root.querySelector("#skillTools").value = (doc?.tools || []).map(tool => `${tool.name}${tool.required ? "|required" : ""}`).join("\n");
  root.querySelector("#skillParams").value = (doc?.params || []).map(param => `${param.name}|${param.type || "string"}|${param.description || ""}|${param.required ? "true" : "false"}`).join("\n");
  renderSkillScripts(root, doc?.scripts || []);
  root.querySelector("#skillMetadata").value = formatMap(doc?.metadata);
  root.querySelector("#skillEmbeddingDescription").value = doc?.activation?.embedding_description || "";
  root.querySelector("#skillInstructions").value = doc?.instructions || t("catalog.defaultInstructions");
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    inputs: doc?.params,
    outputs: doc?.output_kind,
    dependencies: doc?.tools,
    example: doc?.instructions
  });
}

function loadAgentForm(root, runtime, doc, providerOptions = []) {
  const fallbackProvider = runtime.agents?.[0]?.provider || "primary";
  const id = doc?.id || root.querySelector("#resourceName").value || "custom-agent";
  fillProviderSelect(root, runtime, providerOptions);
  root.querySelector("#resourceName").value = id;
  root.querySelector("#resourceDescription").value = doc?.name || id;
  root.querySelector("#resourcePurpose").value = doc?.description || "";
  root.querySelector("#agentProvider").value = doc?.provider || fallbackProvider;
  root.querySelector("#agentModel").value = doc?.model || root.querySelector("#agentProvider").selectedOptions?.[0]?.dataset.defaultModel || "";
  root.querySelector("#agentMode").value = doc?.mode || "chat";
  root.querySelector("#agentTemperature").value = doc?.temperature || "";
  root.querySelector("#agentMaxTokens").value = doc?.max_tokens || "";
  root.querySelector("#agentMaxIterations").value = doc?.max_iterations || 6;
  root.querySelector("#agentToolPolicy").value = doc?.tool_policy || "confirm";
  root.querySelector("#agentAllowedKinds").value = (doc?.allowed_tool_kinds || ["read"]).join(", ");
  root.querySelector("#agentAllowedTools").value = (doc?.allowed_tools || []).join(", ");
  root.querySelector("#agentSystemPrompt").value = doc?.system_prompt || "";
  seedResourceBrief(root, {
    description: doc?.description || doc?.name || root.querySelector("#resourceDescription").value,
    dependencies: [...(doc?.allowed_tools || []), ...(doc?.allowed_tool_kinds || [])],
    example: doc?.system_prompt
  });
}

function loadToolForm(root, name, doc) {
  const normalized = normalizeName(name || doc?.name || root.querySelector("#resourceName").value || "custom-tool");
  root.querySelector("#resourceName").value = doc?.name || normalized;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.resourceTool");
  root.querySelector("#toolLanguage").value = doc?.language || "python";
  root.querySelector("#toolCommand").value = doc?.command || "python";
  root.querySelector("#toolArgs").value = (doc?.args || [`./mcp_servers/${normalized}.py`]).join(", ");
  root.querySelector("#toolEnabled").checked = doc?.enabled !== false;
  root.querySelector("#toolTimeout").value = doc?.timeout || "30s";
  root.querySelector("#toolWorkdir").value = doc?.workdir || ".";
  root.querySelector("#toolEnvAllowlist").value = (doc?.env_allowlist || ["PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "TMP", "TEMP"]).join(", ");
  root.querySelector("#toolNetworkDisabled").checked = !!doc?.network_disabled;
  root.querySelector("#toolIsolation").value = doc?.isolation || "process_group";
  root.querySelector("#toolIsolationProfile").value = doc?.isolation_profile || "";
  root.querySelector("#toolRestartLimit").value = doc?.restart_limit || 3;
  root.querySelector("#toolCooldown").value = doc?.cooldown || "10s";
  root.querySelector("#toolAllowedCommands").value = (doc?.allowed_commands || ["python"]).join(", ");
  root.querySelector("#toolAllowedCommandPaths").value = (doc?.allowed_command_paths || []).join(", ");
  root.querySelector("#toolMaxRequestBytes").value = doc?.max_request_bytes || 65536;
  root.querySelector("#toolMaxResponseBytes").value = doc?.max_response_bytes || 2097152;
  fillToolIsolationOptions(root, doc?.isolation_options || {});
  updateToolIsolationProfileHint(root);
  root.querySelector("#toolCode").value = doc?.code || defaultToolCode(normalized);
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    dependencies: [...(doc?.allowed_commands || []), ...(doc?.env_allowlist || [])],
    example: doc?.code
  });
}

async function loadToolByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  const normalized = normalizeName(name);
  output.textContent = `${t("catalog.loading")} ${normalized}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("tool", normalized, `/api/resources/tools/${encodeURIComponent(normalized)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadToolForm(root, normalized, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || normalized}`;
  } catch (error) {
    loadToolForm(root, normalized, toolResourceByName(normalized) || null);
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

function applyToolScaffoldDocument(root, doc, scaffold = selectedToolScaffold(root)) {
  if (!doc || typeof doc !== "object") return;
  loadToolForm(root, doc.name || scaffold?.default_name || scaffold?.name || "custom-tool", doc);
  if (scaffold?.name) root.querySelector("#toolScaffoldPreset").value = scaffold.name;
  if (!root.querySelector("#resourceDescription").value && scaffold?.description) {
    root.querySelector("#resourceDescription").value = toolScaffoldDescription(scaffold);
  }
}

function fillToolIsolationOptions(root, options = {}) {
  const normalized = normalizeStringMap(options);
  setValue(root, "#toolIsolationImage", normalized.image);
  setValue(root, "#toolIsolationRuntime", normalized.runtime);
  setValue(root, "#toolIsolationPullPolicy", normalized.pull_policy);
  setValue(root, "#toolIsolationWorkspaceMount", normalized.workspace_mount);
  setValue(root, "#toolIsolationWorkspaceTarget", normalized.workspace_target);
  setValue(root, "#toolIsolationContainerWorkdir", normalized.container_workdir);
  setValue(root, "#toolIsolationNetwork", normalized.network);
  setValue(root, "#toolIsolationMemory", normalized.memory);
  setValue(root, "#toolIsolationMemorySwap", normalized.memory_swap);
  setValue(root, "#toolIsolationCpus", normalized.cpus);
  setValue(root, "#toolIsolationPidsLimit", normalized.pids_limit);
  setValue(root, "#toolIsolationUser", normalized.user);
  setValue(root, "#toolIsolationUserns", normalized.userns);
  setValue(root, "#toolIsolationTmpfs", normalized.tmpfs);
  setChecked(root, "#toolIsolationReadonlyRootfs", normalized.readonly_rootfs);
  setChecked(root, "#toolIsolationNoNewPrivileges", normalized.no_new_privileges);
  setChecked(root, "#toolIsolationInit", normalized.init);
  setValue(root, "#toolIsolationCapDrop", normalized.cap_drop);
  setValue(root, "#toolIsolationToolSource", normalized.tool_source);
  setValue(root, "#toolIsolationToolTarget", normalized.tool_target);
  setValue(root, "#toolIsolationToolMount", normalized.tool_mount);
  const known = new Set(["image", "runtime", "pull_policy", "workspace_mount", "workspace_target", "container_workdir", "network", "memory", "memory_swap", "cpus", "pids_limit", "user", "userns", "tmpfs", "readonly_rootfs", "no_new_privileges", "init", "cap_drop", "tool_source", "tool_target", "tool_mount"]);
  const extra = Object.fromEntries(Object.entries(normalized).filter(([key]) => !known.has(key)));
  setValue(root, "#toolIsolationOptionsRaw", formatMap(extra));
  updateToolIsolationNetworkHint(root);
}

function setValue(root, selector, value) {
  const node = root.querySelector(selector);
  if (node) node.value = value || "";
}

function setChecked(root, selector, value) {
  const node = root.querySelector(selector);
  if (node) node.checked = stringBool(value);
}

function loadTeamTemplateForm(root, doc) {
  const name = normalizeName(doc?.name || root.querySelector("#resourceName").value || "custom-review-team");
  root.querySelector("#resourceName").value = name;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.teamTemplateDescriptionDefault");
  root.querySelector("#teamTitle").value = doc?.title || teamTemplateLabel(name);
  root.querySelector("#teamCategory").value = doc?.category || "custom";
  root.querySelector("#teamTags").value = (doc?.tags || ["custom"]).join(", ");
  root.querySelector("#teamRecommendedWorkflow").value = doc?.recommended_workflow || "plan-fix-audit";
  root.querySelector("#teamEntryAgent").value = doc?.recommended_entry_agent || "planner";
  root.querySelector("#teamRoles").value = formatTeamRoles(doc?.role_templates || defaultTeamRoles());
  root.querySelector("#teamHandoffs").value = formatTeamHandoffs(doc?.handoffs || defaultTeamHandoffs());
  root.querySelector("#teamBlackboard").value = formatTeamBlackboard(doc?.blackboard_templates || defaultTeamBlackboard());
  root.querySelector("#teamOutputContract").value = (doc?.output_contract || ["final_report", "changed_files", "residual_risks"]).join("\n");
  root.querySelector("#teamQuorumPresets").value = JSON.stringify(doc?.quorum_presets || [], null, 2);
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    outputs: doc?.output_contract,
    dependencies: doc?.role_templates,
    example: doc?.handoffs
  });
}

function loadKitForm(root, doc) {
  const name = normalizeName(doc?.name || root.querySelector("#resourceName").value || "custom-kit");
  root.querySelector("#resourceName").value = name;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.kitDescriptionDefault");
  root.querySelector("#kitTitle").value = doc?.title || kitLabel(name);
  root.querySelector("#kitCategory").value = doc?.category || "custom";
  root.querySelector("#kitTags").value = (doc?.tags || ["custom"]).join(", ");
  root.querySelector("#kitAgents").value = (doc?.agents || []).join(", ");
  root.querySelector("#kitSkills").value = (doc?.skills || []).join(", ");
  root.querySelector("#kitTools").value = (doc?.tools || []).join(", ");
  root.querySelector("#kitProviders").value = (doc?.providers || []).join(", ");
  root.querySelector("#kitWorkflows").value = (doc?.workflows || []).join(", ");
  root.querySelector("#kitWorkflowTemplates").value = (doc?.workflow_templates || []).join(", ");
  root.querySelector("#kitTeamTemplates").value = (doc?.team_templates || []).join(", ");
  root.querySelector("#kitPolicyRules").value = (doc?.policy_rules || []).join(", ");
  root.querySelector("#kitRequiredEnv").value = (doc?.required_env || []).join(", ");
  root.querySelector("#kitExamples").value = JSON.stringify(doc?.examples || [], null, 2);
  root.querySelector("#kitMetadata").value = formatMap(doc?.metadata);
  renderKitValidation(root, doc?.validation || null);
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    outputs: doc?.tags,
    dependencies: [
      ...(doc?.agents || []),
      ...(doc?.skills || []),
      ...(doc?.tools || []),
      ...(doc?.workflow_templates || []),
      ...(doc?.team_templates || []),
      ...(doc?.policy_rules || [])
    ],
    examples: doc?.examples
  });
}

function loadWorkflowTemplateResourceForm(root, doc) {
  const sourceName = doc?.name || firstWorkflowTemplateName();
  const isCustom = !!doc?.custom || doc?.source === "custom";
  const targetName = isCustom ? sourceName : normalizeName(`${sourceName || "workflow-template"}-copy`);
  root.querySelector("#resourceName").value = targetName || defaultWorkflowTemplateName();
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.workflowTemplateDescriptionDefault");
  root.querySelector("#workflowTemplateSaveMode").value = isCustom ? "edit" : "fork";
  root.querySelector("#workflowTemplateSource").value = sourceName || firstWorkflowTemplateName();
  root.querySelector("#workflowTemplateCaptureSource").value = firstWorkflowGraphName();
  root.querySelector("#workflowTemplateTitle").value = isCustom ? (doc?.title || sourceName) : t("catalog.copyOf", { name: doc?.title || sourceName || t("catalog.workflowTemplate") });
  root.querySelector("#workflowTemplateCategory").value = doc?.category || "custom";
  root.querySelector("#workflowTemplateTags").value = (doc?.tags || (isCustom ? ["custom"] : ["fork"])).join(", ");
  root.querySelector("#workflowTemplateGraph").value = isCustom && doc?.graph ? JSON.stringify(doc.graph, null, 2) : "";
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    outputs: doc?.tags,
    example: doc?.graph
  });
}

function loadWorkflowSchemaForm(root, doc) {
  const schema = doc?.schema || selectedWorkflowSchema(root) || { workflow: defaultWorkflowSchemaName(), stages: {} };
  const name = normalizeName(doc?.name || schema?.workflow || defaultWorkflowSchemaName());
  root.querySelector("#resourceName").value = name;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.workflowSchemaDescriptionDefault");
  ensureWorkflowSchemaSourceOption(root, schema?.workflow || name);
  root.querySelector("#workflowSchemaSource").value = schema?.workflow || name;
  root.querySelector("#workflowSchemaActivateMerge").checked = true;
  root.querySelector("#workflowSchemaJSON").value = JSON.stringify({
    kind: "goflow.workflow_schema_resource",
    version: 2,
    min_supported_version: 1,
    name,
    description: root.querySelector("#resourceDescription").value.trim(),
    schema
  }, null, 2);
}

function loadWorkflowForm(root, doc) {
  root.querySelector("#resourceName").value = doc?.name || root.querySelector("#resourceName").value || "custom-workflow";
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.resourceWorkflow");
  root.querySelector("#workflowTemplate").value = doc?.template || firstWorkflowTemplateName() || "blank";
  root.querySelector("#workflowApproval").value = doc?.approval_stage || "implement";
  root.querySelector("#workflowOpenAfterSave").value = doc?.open_after_save || "yes";
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    dependencies: doc?.template || root.querySelector("#workflowTemplate").value
  });
}

function loadNodeMetadataForm(root, doc) {
  const type = doc?.type || defaultNodeMetadataType();
  root.querySelector("#resourceName").value = type;
  root.querySelector("#nodeMetadataType").value = type;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.nodeMetadataDescriptionDefault");
  root.querySelector("#nodeMetadataLabel").value = doc?.label || nodeDisplayLabel(type);
  root.querySelector("#nodeMetadataCategory").value = doc?.category || "custom";
  root.querySelector("#nodeMetadataDescription").value = doc?.description || "";
  root.querySelector("#nodeMetadataTags").value = (doc?.tags || []).join(", ");
  root.querySelector("#nodeMetadataControl").checked = !!doc?.control;
  root.querySelector("#nodeMetadataVisualOnly").checked = !!doc?.visual_only;
  root.querySelector("#nodeMetadataHints").value = (doc?.hints || []).join("\n");
  root.querySelector("#nodeMetadataWarnings").value = (doc?.warnings || []).join("\n");
  root.querySelector("#nodeMetadataFields").value = JSON.stringify(doc?.fields || [], null, 2);
  root.querySelector("#nodeMetadataOutputs").value = JSON.stringify(doc?.outputs || [], null, 2);
  root.querySelector("#nodeMetadataExamples").value = JSON.stringify(doc?.examples || [], null, 2);
  root.querySelector("#nodeMetadataDefaultStage").value = doc?.default_stage ? JSON.stringify(doc.default_stage, null, 2) : "";
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    inputs: doc?.fields,
    outputs: doc?.outputs,
    examples: doc?.examples
  });
}

function loadExpressionHelperForm(root, doc) {
  const name = doc?.name || defaultExpressionHelperName();
  root.querySelector("#resourceName").value = name;
  root.querySelector("#expressionHelperSource").value = name;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.expressionHelperDescriptionDefault");
  root.querySelector("#expressionHelperLabel").value = doc?.label || expressionDisplayLabel(name);
  root.querySelector("#expressionHelperCategory").value = doc?.category || "custom";
  root.querySelector("#expressionHelperDescription").value = doc?.description || "";
  root.querySelector("#expressionHelperSignature").value = doc?.signature || `${name}(value)`;
  root.querySelector("#expressionHelperInsertText").value = doc?.insert_text || `${name}()`;
  root.querySelector("#expressionHelperReturnType").value = doc?.return_type || "";
  root.querySelector("#expressionHelperMinArgs").value = doc?.min_args ?? "";
  root.querySelector("#expressionHelperMaxArgs").value = doc?.max_args ?? "";
  root.querySelector("#expressionHelperModes").value = (doc?.modes || []).join(", ");
  root.querySelector("#expressionHelperNodeTypes").value = (doc?.node_types || []).join(", ");
  root.querySelector("#expressionHelperArgs").value = JSON.stringify(doc?.args || [], null, 2);
  root.querySelector("#expressionHelperExamples").value = (doc?.examples || []).join("\n");
  root.querySelector("#expressionHelperHints").value = (doc?.hints || []).join("\n");
  root.querySelector("#expressionHelperWarnings").value = (doc?.warnings || []).join("\n");
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    inputs: doc?.args,
    outputs: doc?.return_type,
    examples: doc?.examples
  });
}

async function saveResource(root) {
  const type = root.querySelector("#resourceType").value;
  if (shouldValidateResourceBeforeSave(root, type)) {
    const validation = await validateResourceDraft(root, { beforeSave: true });
    if (!validation?.valid) return;
  }
  if (type === "skill") await saveSkill(root);
  else if (type === "agent") await saveAgent(root);
  else if (type === "tool") await saveTool(root);
  else if (type === "team-template") await saveTeamTemplate(root);
  else if (type === "kit") await saveKit(root);
  else if (type === "workflow-template") await saveWorkflowTemplateResource(root);
  else if (type === "workflow-schema") await saveWorkflowSchemaResource(root);
  else if (type === "node-metadata") await saveWorkflowNodeMetadata(root);
  else if (type === "expression-helper") await saveExpressionHelper(root);
  else if (type === "provider") await saveProvider(root);
  else if (type === "workflow") await saveWorkflow(root);
  else if (type === "policy-rule") await savePolicyRule(root);
  await refreshConfigDiagnosticsAfterResourceSave(root);
}

function shouldValidateResourceBeforeSave(root, type) {
  return resourceValidationEnabled(root, type);
}

function resourceValidationEnabled(root, type) {
  const workflowTemplateMode = root.querySelector("#workflowTemplateSaveMode")?.value || "";
  if (type === "workflow-template" && ["fork", "capture"].includes(workflowTemplateMode)) return false;
  const capability = resourceCapability(catalogContext.resourceCapabilities, type);
  if (capability) return capability.can_validate !== false && Boolean(String(capability.validate_path || "").trim());
  if (!resourceValidationEndpointType(type)) return false;
  return true;
}

async function saveSkill(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectSkillForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("skill", doc.name, `/api/resources/skills/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    renderBackendResourceSaveOutcome(root, "skill", saved, {
      title: t("catalog.saveOutcomeReadyTitle"),
      body: t("catalog.saveOutcomeSkillBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceSkill") },
        { label: t("catalog.name"), value: saved.name || doc.name }
      ],
      path: saved.path
    });
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveAgent(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectAgentForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.id}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("agent", doc.id, `/api/resources/agents/${encodeURIComponent(doc.id)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    renderBackendResourceSaveOutcome(root, "agent", saved, {
      tone: "warn",
      title: t("catalog.saveOutcomeRestartTitle"),
      body: t("catalog.saveOutcomeRestartBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceAgent") },
        { label: t("catalog.name"), value: saved.id || doc.id }
      ],
      path: saved.path,
      includeSettingsAction: true,
      actions: [{ action: "settings", label: t("catalog.saveOutcomeOpenSettings"), primary: true }]
    });
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveProvider(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectProviderForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.id}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("provider", doc.id, `/api/resources/providers/${encodeURIComponent(doc.id)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    renderBackendResourceSaveOutcome(root, "provider", saved, {
      tone: "warn",
      title: t("catalog.saveOutcomeRestartTitle"),
      body: t("catalog.saveOutcomeRestartBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceProvider") },
        { label: t("catalog.name"), value: saved.id || doc.id }
      ],
      path: saved.path,
      includeSettingsAction: true,
      actions: [{ action: "settings", label: t("catalog.saveOutcomeOpenSettings"), primary: true }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveTool(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectToolForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("tool", doc.name, `/api/resources/tools/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    renderBackendResourceSaveOutcome(root, "tool", saved, {
      tone: "warn",
      title: t("catalog.saveOutcomeRestartTitle"),
      body: t("catalog.saveOutcomeToolBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceTool") },
        { label: t("catalog.name"), value: saved.name || doc.name }
      ],
      path: saved.path,
      paths: [{ label: t("catalog.saveOutcomeConfigPath"), value: saved.config_path }],
      includeSettingsAction: true,
      actions: [{ action: "settings", label: t("catalog.saveOutcomeOpenSettings"), primary: true }]
    });
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveTeamTemplate(root) {
  const output = root.querySelector("#resourceOutput");
  try {
    const doc = collectTeamTemplateForm(root);
    output.textContent = `${t("catalog.saving")} ${doc.name}...`;
    const endpoint = catalogResourceWriteEndpoint("team-template", doc.name, `/api/resources/team-templates/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    const options = await request("/api/workflow-options");
    const available = (options.team_templates || []).some(template => template.name === saved.name);
    renderBackendResourceSaveOutcome(root, "team-template", saved, {
      tone: available ? "good" : "warn",
      title: available ? t("catalog.saveOutcomeReadyTitle") : t("catalog.saveOutcomeRefreshTitle"),
      body: available ? t("catalog.saveOutcomeWorkflowOptionsBody") : t("catalog.saveOutcomeRefreshBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceTeamTemplate") },
        { label: t("catalog.name"), value: saved.name },
        { label: t("catalog.saveOutcomeStatus"), value: available ? t("catalog.saveOutcomeAvailableNow") : t("catalog.saveOutcomeRefreshOptions") }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: available }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveKit(root) {
  const output = root.querySelector("#resourceOutput");
  try {
    const doc = collectKitForm(root);
    output.textContent = `${t("catalog.saving")} ${doc.name}...`;
    const endpoint = catalogResourceWriteEndpoint("kit", doc.name, `/api/resources/kits/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    loadKitForm(root, saved);
    renderBackendResourceSaveOutcome(root, "kit", saved, {
      tone: saved.validation?.valid === false ? "warn" : "good",
      title: saved.validation?.valid === false ? t("catalog.saveOutcomeCheckTitle") : t("catalog.saveOutcomeReadyTitle"),
      body: t("catalog.saveOutcomeKitBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceKit") },
        { label: t("catalog.name"), value: saved.name || doc.name },
        { label: t("catalog.kitValidationResult"), value: kitValidationText(saved.validation) }
      ],
      path: saved.path
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveWorkflowTemplateResource(root) {
  const output = root.querySelector("#resourceOutput");
  const mode = root.querySelector("#workflowTemplateSaveMode").value;
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || defaultWorkflowTemplateName());
  output.textContent = `${t("catalog.saving")} ${name}...`;
  try {
    let saved;
    if (mode === "fork") {
      const source = root.querySelector("#workflowTemplateSource").value;
      if (!source) throw new Error(t("catalog.noWorkflowTemplates"));
      const resolved = catalogResourceActionEndpoint("workflow-template", "fork", name, {
        fallback: `/api/resources/workflow-templates/${encodeURIComponent(name)}/fork`,
        methodFallback: "POST"
      });
      if (!resolved.endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
      saved = await request(resolved.endpoint, {
        method: resolved.method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source,
          title: root.querySelector("#workflowTemplateTitle").value.trim(),
          description: root.querySelector("#resourceDescription").value.trim()
        })
      });
    } else if (mode === "capture") {
      const workflow = root.querySelector("#workflowTemplateCaptureSource").value;
      if (!workflow) throw new Error(t("catalog.noWorkflowGraphs"));
      const resolved = catalogResourceActionEndpoint("workflow-template", "capture", name, {
        fallback: `/api/resources/workflow-templates/${encodeURIComponent(name)}/capture`,
        methodFallback: "POST"
      });
      if (!resolved.endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
      saved = await request(resolved.endpoint, {
        method: resolved.method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          workflow,
          title: root.querySelector("#workflowTemplateTitle").value.trim(),
          description: root.querySelector("#resourceDescription").value.trim()
        })
      });
    } else {
      const doc = collectWorkflowTemplateResourceForm(root);
      const endpoint = catalogResourceWriteEndpoint("workflow-template", doc.name, `/api/resources/workflow-templates/${encodeURIComponent(doc.name)}`);
      if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
      saved = await request(endpoint, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(doc)
      });
    }
    renderBackendResourceSaveOutcome(root, "workflow-template", saved, {
      title: t("catalog.saveOutcomeReadyTitle"),
      body: t("catalog.saveOutcomeTemplateBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceWorkflowTemplate") },
        { label: t("catalog.name"), value: saved.name || name }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio") }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveWorkflowSchemaResource(root) {
  const output = root.querySelector("#resourceOutput");
  try {
    const doc = collectWorkflowSchemaForm(root);
    output.textContent = `${t("catalog.saving")} ${doc.name}...`;
    const endpoint = catalogResourceWriteEndpoint("workflow-schema", doc.name, `/api/resources/workflow-schemas/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    loadWorkflowSchemaForm(root, saved);
    renderBackendResourceSaveOutcome(root, "workflow-schema", saved, {
      title: t("catalog.saveOutcomeReadyTitle"),
      body: t("catalog.saveOutcomeWorkflowSchemaBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceWorkflowSchema") },
        { label: t("catalog.name"), value: saved.name || doc.name },
        { label: t("catalog.schemaStages"), value: saved.schema ? workflowSchemaStageCount(saved.schema) : workflowSchemaStageCount(doc.schema) },
        { label: t("catalog.schemaOutputs"), value: saved.schema ? workflowSchemaOutputCount(saved.schema) : workflowSchemaOutputCount(doc.schema) }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio") }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveWorkflowNodeMetadata(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectNodeMetadataForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.type}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("node-metadata", doc.type, `/api/resources/workflow-node-metadata/${encodeURIComponent(doc.type)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    const options = await request("/api/workflow-options");
    const available = (options.node_types || []).some(item => item.type === saved.type && (item.custom || String(item.source || "").includes("custom")));
    renderBackendResourceSaveOutcome(root, "node-metadata", saved, {
      tone: available ? "good" : "warn",
      title: available ? t("catalog.saveOutcomeReadyTitle") : t("catalog.saveOutcomeRefreshTitle"),
      body: available ? t("catalog.saveOutcomeWorkflowOptionsBody") : t("catalog.saveOutcomeRefreshBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceNodeMetadata") },
        { label: t("catalog.name"), value: saved.type },
        { label: t("catalog.saveOutcomeStatus"), value: available ? t("catalog.saveOutcomeAvailableNow") : t("catalog.saveOutcomeRefreshOptions") }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: available }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveExpressionHelper(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectExpressionHelperForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("expression-helper", doc.name, `/api/resources/expression-helpers/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    const options = await request("/api/workflow-options");
    const available = (options.expression_functions || []).some(item => item.name === saved.name && (item.custom || String(item.source || "").includes("custom")));
    renderBackendResourceSaveOutcome(root, "expression-helper", saved, {
      tone: available ? "good" : "warn",
      title: available ? t("catalog.saveOutcomeReadyTitle") : t("catalog.saveOutcomeRefreshTitle"),
      body: available ? t("catalog.saveOutcomeWorkflowOptionsBody") : t("catalog.saveOutcomeRefreshBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceExpressionHelper") },
        { label: t("catalog.name"), value: saved.name },
        { label: t("catalog.saveOutcomeStatus"), value: available ? t("catalog.saveOutcomeAvailableNow") : t("catalog.saveOutcomeRefreshOptions") }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: available }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function saveWorkflow(root) {
  const output = root.querySelector("#resourceOutput");
  let doc = collectWorkflowForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    if (doc.template && !doc.stages) {
      doc = await workflowGraphFromTemplate(doc);
    }
    const endpoint = catalogResourceWriteEndpoint("workflow", doc.name, `/api/workflow-graphs/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    if (root.querySelector("#workflowOpenAfterSave").value === "yes") {
      localStorage.setItem("goflow.workflow.open", saved.name);
      location.hash = "workflows";
    } else {
      renderBackendResourceSaveOutcome(root, "workflow", saved, {
        title: t("catalog.saveOutcomeReadyTitle"),
        body: t("catalog.saveOutcomeWorkflowBody"),
        facts: [
          { label: t("catalog.resourceType"), value: t("catalog.resourceWorkflow") },
          { label: t("catalog.name"), value: saved.name }
        ],
        path: saved.path,
        actions: [{ action: "workflows", value: saved.name, label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: true }]
      });
    }
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

async function savePolicyRule(root) {
  const output = root.querySelector("#resourceOutput");
  const doc = collectPolicyRuleForm(root);
  output.textContent = `${t("catalog.saving")} ${doc.name}...`;
  try {
    const endpoint = catalogResourceWriteEndpoint("policy-rule", doc.name, `/api/resources/policy-rules/${encodeURIComponent(doc.name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const saved = await request(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    const options = await request("/api/workflow-options");
    const available = (options.policy_rules || []).some(rule => rule.name === saved.name);
    renderBackendResourceSaveOutcome(root, "policy-rule", saved, {
      tone: available ? "good" : "warn",
      title: available ? t("catalog.saveOutcomeReadyTitle") : t("catalog.saveOutcomeRefreshTitle"),
      body: available ? t("catalog.saveOutcomeWorkflowOptionsBody") : t("catalog.saveOutcomeRefreshBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourcePolicyRule") },
        { label: t("catalog.name"), value: saved.name },
        { label: t("catalog.saveOutcomeStatus"), value: available ? t("catalog.saveOutcomeAvailableNow") : t("catalog.saveOutcomeRefreshOptions") }
      ],
      path: saved.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: available }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.saveFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.saveFailed"))}`;
  }
}

function collectSkillForm(root) {
  const scripts = collectSkillScripts(root);
  const doc = {
    name: root.querySelector("#resourceName").value.trim(),
    description: root.querySelector("#resourceDescription").value.trim(),
    version: root.querySelector("#skillVersion").value.trim() || "1.0.0",
    author: root.querySelector("#skillAuthor").value.trim() || "GoFlow Studio",
    mode: root.querySelector("#skillMode").value,
    preferred_agent: root.querySelector("#skillAgent").value,
    allowed_tool_kinds: splitList(root.querySelector("#skillAllowedKinds").value),
    output_kind: root.querySelector("#skillOutputKind").value.trim() || "summary",
    priority: numberOrZero(root.querySelector("#skillPriority").value),
    max_iterations: numberOrZero(root.querySelector("#skillMaxIterations").value),
    next_skills: splitList(root.querySelector("#skillNext").value),
    activation: { keywords: splitLines(root.querySelector("#skillKeywords").value), embedding_description: root.querySelector("#skillEmbeddingDescription").value.trim() },
    tools: parseTools(root.querySelector("#skillTools").value),
    params: parseParams(root.querySelector("#skillParams").value),
    metadata: parseMap(root.querySelector("#skillMetadata").value),
    instructions: root.querySelector("#skillInstructions").value
  };
  if (scripts.length) doc.scripts = scripts;
  return doc;
}

function collectAgentForm(root) {
  const id = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-agent");
  return {
    id,
    name: root.querySelector("#resourceDescription").value.trim() || id,
    description: root.querySelector("#resourcePurpose").value.trim() || root.querySelector("#resourceDescription").value.trim(),
    system_prompt: root.querySelector("#agentSystemPrompt").value,
    provider: root.querySelector("#agentProvider").value.trim() || "primary",
    model: root.querySelector("#agentModel").value.trim(),
    temperature: Number(root.querySelector("#agentTemperature").value || 0),
    max_tokens: numberOrZero(root.querySelector("#agentMaxTokens").value),
    max_iterations: numberOrZero(root.querySelector("#agentMaxIterations").value) || 6,
    allowed_tool_kinds: splitList(root.querySelector("#agentAllowedKinds").value),
    allowed_tools: splitList(root.querySelector("#agentAllowedTools").value),
    tool_policy: root.querySelector("#agentToolPolicy").value,
    mode: root.querySelector("#agentMode").value
  };
}

function collectProviderForm(root) {
  const id = normalizeName(root.querySelector("#providerID").value.trim() || root.querySelector("#resourceName").value.trim() || "primary");
  return {
    id,
    provider: root.querySelector("#providerType").value.trim(),
    model: root.querySelector("#providerDefaultModel").value.trim(),
    base_url: root.querySelector("#providerBaseURL").value.trim(),
    api_key: root.querySelector("#providerAPIKey").value.trim(),
    models: splitList(root.querySelector("#providerModels").value),
    metadata: parseMap(root.querySelector("#providerMetadata").value),
    description: root.querySelector("#providerDescription").value.trim()
  };
}

function collectPolicyRuleForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "critical-finding");
  return {
    name,
    label: root.querySelector("#policyLabel").value.trim() || policyRuleLabel(name),
    description: root.querySelector("#resourceDescription").value.trim(),
    operator: root.querySelector("#policyOperator").value,
    expression: root.querySelector("#policyExpression").value.trim(),
    reason: root.querySelector("#policyReason").value.trim(),
    defaults: parseMap(root.querySelector("#policyDefaults").value),
    params: parsePolicyParams(root.querySelector("#policyParams").value)
  };
}

function collectTeamTemplateForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-review-team");
  return {
    kind: "goflow.team_template_resource",
    version: 2,
    min_supported_version: 1,
    name,
    title: root.querySelector("#teamTitle").value.trim() || teamTemplateLabel(name),
    description: root.querySelector("#resourceDescription").value.trim(),
    category: root.querySelector("#teamCategory").value.trim() || "custom",
    tags: splitList(root.querySelector("#teamTags").value),
    recommended_workflow: root.querySelector("#teamRecommendedWorkflow").value.trim(),
    recommended_entry_agent: root.querySelector("#teamEntryAgent").value.trim(),
    role_templates: parseTeamRoles(root.querySelector("#teamRoles").value),
    handoffs: parseTeamHandoffs(root.querySelector("#teamHandoffs").value),
    blackboard_templates: parseTeamBlackboard(root.querySelector("#teamBlackboard").value),
    quorum_presets: parseJSONArray(root.querySelector("#teamQuorumPresets").value, [], t("catalog.teamQuorumPresets")),
    output_contract: splitLines(root.querySelector("#teamOutputContract").value)
  };
}

function collectKitForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-kit");
  return cleanEmptyFields({
    kind: "goflow.kit",
    version: 1,
    min_supported_version: 1,
    name,
    title: root.querySelector("#kitTitle").value.trim() || kitLabel(name),
    description: root.querySelector("#resourceDescription").value.trim(),
    category: root.querySelector("#kitCategory").value.trim() || "custom",
    tags: splitList(root.querySelector("#kitTags").value),
    agents: splitList(root.querySelector("#kitAgents").value),
    providers: splitList(root.querySelector("#kitProviders").value),
    skills: splitList(root.querySelector("#kitSkills").value),
    tools: splitList(root.querySelector("#kitTools").value),
    workflows: splitList(root.querySelector("#kitWorkflows").value),
    workflow_templates: splitList(root.querySelector("#kitWorkflowTemplates").value),
    team_templates: splitList(root.querySelector("#kitTeamTemplates").value),
    policy_rules: splitList(root.querySelector("#kitPolicyRules").value),
    required_env: splitList(root.querySelector("#kitRequiredEnv").value),
    examples: parseJSONArray(root.querySelector("#kitExamples").value, [], t("catalog.kitExamplesJSON")),
    metadata: parseMap(root.querySelector("#kitMetadata").value)
  });
}

function collectWorkflowTemplateResourceForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || defaultWorkflowTemplateName());
  const graphText = root.querySelector("#workflowTemplateGraph").value.trim();
  if (!graphText) throw new Error(t("catalog.templateGraphRequired"));
  const parsed = parseJSONValue(graphText, t("catalog.templateGraphJSON"));
  const graph = parsed?.graph && typeof parsed.graph === "object" ? parsed.graph : parsed;
  if (!graph || typeof graph !== "object" || !Array.isArray(graph.stages)) throw new Error(t("catalog.templateGraphInvalid"));
  graph.name = name;
  if (!graph.description) graph.description = root.querySelector("#resourceDescription").value.trim();
  return {
    kind: "goflow.workflow_template_resource",
    version: 2,
    min_supported_version: 1,
    name,
    title: root.querySelector("#workflowTemplateTitle").value.trim() || workflowTemplateLabel(name),
    description: root.querySelector("#resourceDescription").value.trim(),
    category: root.querySelector("#workflowTemplateCategory").value.trim() || "custom",
    tags: splitList(root.querySelector("#workflowTemplateTags").value),
    graph
  };
}

function collectWorkflowSchemaForm(root) {
  const text = root.querySelector("#workflowSchemaJSON").value.trim();
  if (!text) throw new Error(t("catalog.schemaJSONRequired"));
  const parsed = parseJSONValue(text, t("catalog.schemaJSON"));
  const schema = parsed?.schema && typeof parsed.schema === "object" ? parsed.schema : parsed;
  if (!schema || typeof schema !== "object" || Array.isArray(schema)) throw new Error(t("catalog.schemaJSONInvalid"));
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || parsed?.name || schema.workflow || defaultWorkflowSchemaName());
  schema.workflow = normalizeName(schema.workflow || name);
  if (schema.workflow !== name) {
    schema.workflow = name;
  }
  return {
    kind: "goflow.workflow_schema_resource",
    version: 2,
    min_supported_version: 1,
    name,
    description: root.querySelector("#resourceDescription").value.trim() || parsed?.description || t("catalog.workflowSchemaDescriptionDefault"),
    schema
  };
}

function collectNodeMetadataForm(root) {
  const type = normalizeName(root.querySelector("#nodeMetadataType").value || root.querySelector("#resourceName").value || defaultNodeMetadataType());
  const doc = {
    kind: "goflow.workflow_node_metadata",
    version: 1,
    min_supported_version: 1,
    type,
    label: root.querySelector("#nodeMetadataLabel").value.trim() || nodeDisplayLabel(type),
    category: root.querySelector("#nodeMetadataCategory").value.trim(),
    description: root.querySelector("#nodeMetadataDescription").value.trim() || root.querySelector("#resourceDescription").value.trim(),
    control: root.querySelector("#nodeMetadataControl").checked,
    visual_only: root.querySelector("#nodeMetadataVisualOnly").checked,
    tags: splitList(root.querySelector("#nodeMetadataTags").value),
    hints: splitLines(root.querySelector("#nodeMetadataHints").value),
    warnings: splitLines(root.querySelector("#nodeMetadataWarnings").value),
    fields: parseJSONValue(root.querySelector("#nodeMetadataFields").value, t("catalog.nodeFieldsJSON"), []),
    outputs: parseJSONValue(root.querySelector("#nodeMetadataOutputs").value, t("catalog.nodeOutputsJSON"), []),
    examples: parseJSONValue(root.querySelector("#nodeMetadataExamples").value, t("catalog.nodeExamplesJSON"), [])
  };
  const defaultStage = parseOptionalJSONObject(root.querySelector("#nodeMetadataDefaultStage").value, t("catalog.nodeDefaultStageJSON"));
  if (defaultStage) doc.default_stage = defaultStage;
  return cleanEmptyFields(doc);
}

function collectExpressionHelperForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || root.querySelector("#expressionHelperSource").value || defaultExpressionHelperName()).replaceAll("-", "_");
  const doc = {
    kind: "goflow.workflow_expression_function",
    version: 1,
    min_supported_version: 1,
    name,
    label: root.querySelector("#expressionHelperLabel").value.trim() || expressionDisplayLabel(name),
    category: root.querySelector("#expressionHelperCategory").value.trim(),
    description: root.querySelector("#expressionHelperDescription").value.trim() || root.querySelector("#resourceDescription").value.trim(),
    signature: root.querySelector("#expressionHelperSignature").value.trim(),
    insert_text: root.querySelector("#expressionHelperInsertText").value.trim(),
    return_type: root.querySelector("#expressionHelperReturnType").value.trim(),
    min_args: numberOrZero(root.querySelector("#expressionHelperMinArgs").value),
    max_args: numberOrZero(root.querySelector("#expressionHelperMaxArgs").value),
    modes: splitList(root.querySelector("#expressionHelperModes").value),
    node_types: splitList(root.querySelector("#expressionHelperNodeTypes").value),
    args: parseJSONValue(root.querySelector("#expressionHelperArgs").value, t("catalog.expressionArgsJSON"), []),
    examples: splitLines(root.querySelector("#expressionHelperExamples").value),
    hints: splitLines(root.querySelector("#expressionHelperHints").value),
    warnings: splitLines(root.querySelector("#expressionHelperWarnings").value)
  };
  return cleanEmptyFields(doc);
}

function collectToolForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-tool");
  const isolationOptions = collectToolIsolationOptions(root);
  const isolationProfile = root.querySelector("#toolIsolationProfile").value.trim();
  const isolation = root.querySelector("#toolIsolation").value;
  const doc = {
    name,
    language: root.querySelector("#toolLanguage").value,
    description: root.querySelector("#resourceDescription").value.trim(),
    command: root.querySelector("#toolCommand").value.trim() || "python",
    args: splitList(root.querySelector("#toolArgs").value),
    enabled: root.querySelector("#toolEnabled").checked,
    timeout: root.querySelector("#toolTimeout").value.trim() || "30s",
    workdir: root.querySelector("#toolWorkdir").value.trim() || ".",
    env_allowlist: splitList(root.querySelector("#toolEnvAllowlist").value),
    network_disabled: root.querySelector("#toolNetworkDisabled").checked,
    isolation: (Object.keys(isolationOptions).length || isolationProfile) && !["linux_cgroup", "container"].includes(isolation) ? "container" : isolation,
    isolation_profile: isolationProfile,
    restart_limit: numberOrZero(root.querySelector("#toolRestartLimit").value) || 3,
    cooldown: root.querySelector("#toolCooldown").value.trim() || "10s",
    allowed_commands: splitList(root.querySelector("#toolAllowedCommands").value),
    allowed_command_paths: splitList(root.querySelector("#toolAllowedCommandPaths").value),
    max_request_bytes: numberOrZero(root.querySelector("#toolMaxRequestBytes").value),
    max_response_bytes: numberOrZero(root.querySelector("#toolMaxResponseBytes").value),
    isolation_options: isolationOptions,
    code: root.querySelector("#toolCode").value
  };
  if (!doc.isolation_profile) delete doc.isolation_profile;
  if (!Object.keys(doc.isolation_options || {}).length) delete doc.isolation_options;
  return cleanEmptyFields(doc);
}

function collectToolIsolationOptions(root) {
  const options = {
    ...parseMap(root.querySelector("#toolIsolationOptionsRaw").value),
    image: root.querySelector("#toolIsolationImage").value.trim(),
    runtime: root.querySelector("#toolIsolationRuntime").value.trim(),
    pull_policy: root.querySelector("#toolIsolationPullPolicy").value.trim(),
    workspace_mount: root.querySelector("#toolIsolationWorkspaceMount").value.trim(),
    workspace_target: root.querySelector("#toolIsolationWorkspaceTarget").value.trim(),
    container_workdir: root.querySelector("#toolIsolationContainerWorkdir").value.trim(),
    network: root.querySelector("#toolNetworkDisabled").checked ? "disabled" : root.querySelector("#toolIsolationNetwork").value.trim(),
    memory: root.querySelector("#toolIsolationMemory").value.trim(),
    memory_swap: root.querySelector("#toolIsolationMemorySwap").value.trim(),
    cpus: root.querySelector("#toolIsolationCpus").value.trim(),
    pids_limit: root.querySelector("#toolIsolationPidsLimit").value.trim(),
    user: root.querySelector("#toolIsolationUser").value.trim(),
    userns: root.querySelector("#toolIsolationUserns").value.trim(),
    tmpfs: root.querySelector("#toolIsolationTmpfs").value.trim(),
    readonly_rootfs: root.querySelector("#toolIsolationReadonlyRootfs").checked ? "true" : "",
    no_new_privileges: root.querySelector("#toolIsolationNoNewPrivileges").checked ? "true" : "",
    init: root.querySelector("#toolIsolationInit").checked ? "true" : "",
    cap_drop: root.querySelector("#toolIsolationCapDrop").value.trim(),
    tool_source: root.querySelector("#toolIsolationToolSource").value.trim(),
    tool_target: root.querySelector("#toolIsolationToolTarget").value.trim(),
    tool_mount: root.querySelector("#toolIsolationToolMount").value.trim()
  };
  if (root.querySelector("#toolIsolation").value !== "container" && !root.querySelector("#toolIsolationProfile").value.trim() && !options.image && !options.runtime && !root.querySelector("#toolNetworkDisabled").checked) {
    const cleaned = cleanEmptyFields(normalizeStringMap(options));
    return Object.keys(cleaned).some(key => !["network"].includes(key)) ? cleaned : {};
  }
  return cleanEmptyFields(normalizeStringMap(options));
}

function loadProviderForm(root, doc) {
  const id = doc?.id || doc?.name || doc?.provider || root.querySelector("#resourceName").value || "primary";
  root.querySelector("#resourceName").value = id;
  root.querySelector("#providerID").value = id;
  root.querySelector("#resourceDescription").value = doc?.description || "";
  root.querySelector("#providerDescription").value = doc?.description || "";
  root.querySelector("#providerType").value = doc?.provider || doc?.type || doc?.kind || doc?.driver || "";
  root.querySelector("#providerDefaultModel").value = doc?.default_model || doc?.model || doc?.defaultModel || "";
  root.querySelector("#providerBaseURL").value = doc?.base_url || doc?.baseURL || "";
  root.querySelector("#providerAPIKey").value = doc?.api_key || doc?.env_key || doc?.api_key_env || doc?.envKey || "";
  root.querySelector("#providerModels").value = (doc?.models || doc?.supported_models || []).join(", ");
  root.querySelector("#providerMetadata").value = formatMap(doc?.metadata);
}

async function loadProviderByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("provider", name, `/api/resources/providers/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadProviderForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.id || name}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadPolicyRuleByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("policy-rule", name, `/api/resources/policy-rules/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadPolicyRuleForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadTeamTemplateByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const doc = await requestCatalogTeamTemplateDetail(name);
    loadTeamTemplateForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}${doc.custom ? "" : `\n${t("catalog.teamTemplateBuiltInCopy")}`}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadKitByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("kit", name, `/api/resources/kits/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadKitForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}\n${kitValidationText(doc.validation)}`.trim();
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadWorkflowTemplateByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const doc = await requestCatalogWorkflowTemplateDetail(name);
    loadWorkflowTemplateResourceForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}${doc.custom ? "" : `\n${t("catalog.workflowTemplateBuiltInCopy")}`}`;
  } catch (error) {
    output.textContent = `${t("catalog.loadFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.loadFailed"))}`;
  }
}

async function loadWorkflowSchemaByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("workflow-schema", name, `/api/resources/workflow-schemas/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadWorkflowSchemaForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}`;
  } catch {
    const fallback = (catalogContext.workflowSchemas || []).find(item => item.workflow === name);
    if (fallback) {
      loadWorkflowSchemaForm(root, workflowSchemaResourceFromSchema(fallback, t("catalog.workflowSchemaDescriptionDefault")));
      output.textContent = `${t("catalog.loaded")} ${name}\n${t("catalog.workflowSchemaObservedCopy")}`;
    } else {
      output.textContent = `${t("catalog.loadFailed")}: ${t("catalog.noObservedWorkflowSchemas")}`;
    }
  }
}

async function loadWorkflowNodeMetadataByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("node-metadata", name, `/api/resources/workflow-node-metadata/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadNodeMetadataForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.type || name}`;
  } catch {
    const fallback = (catalogContext.nodeTypes || []).find(item => item.type === name);
    loadNodeMetadataForm(root, fallback || { type: name });
    output.textContent = `${t("catalog.loaded")} ${name}\n${t("catalog.nodeMetadataBuiltInCopy")}`;
  }
}

async function loadExpressionHelperByName(root, name) {
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.loading")} ${name}...`;
  try {
    const endpoint = catalogResourceReadEndpoint("expression-helper", name, `/api/resources/expression-helpers/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const doc = await request(endpoint);
    loadExpressionHelperForm(root, doc);
    output.textContent = `${t("catalog.loaded")} ${doc.name || name}`;
  } catch {
    const fallback = (catalogContext.expressionHelpers || []).find(item => item.name === name);
    loadExpressionHelperForm(root, fallback || { name });
    output.textContent = `${t("catalog.loaded")} ${name}\n${t("catalog.expressionHelperBuiltInCopy")}`;
  }
}

function loadPolicyRuleForm(root, doc) {
  const name = normalizeName(doc?.name || root.querySelector("#resourceName").value || "critical-finding");
  root.querySelector("#resourceName").value = name;
  root.querySelector("#resourceDescription").value = doc?.description || t("catalog.policyRuleDescriptionDefault");
  root.querySelector("#policyLabel").value = doc?.label || policyRuleLabel(name);
  root.querySelector("#policyOperator").value = doc?.operator || "expression";
  root.querySelector("#policyReason").value = doc?.reason || t("catalog.policyReasonDefault");
  root.querySelector("#policyExpression").value = doc?.expression || "contains({{ref}}, \"{{needle}}\")";
  root.querySelector("#policyDefaults").value = formatMap(doc?.defaults || { ref: "previous.raw_output", needle: "critical" });
  root.querySelector("#policyParams").value = formatPolicyParams(doc?.params || defaultPolicyParams());
  seedResourceBrief(root, {
    description: doc?.description || root.querySelector("#resourceDescription").value,
    inputs: doc?.params,
    outputs: doc?.reason,
    example: doc?.expression
  });
}

async function loadProviderOptions(runtime, capabilities = []) {
  const runtimeOptions = normalizeProviderOptions(runtime?.providers || []);
  if (runtimeOptions.length) return runtimeOptions;
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "provider", "/api/resources/providers");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeProviderOptions(Array.isArray(payload) ? payload : payload?.items || payload?.providers || []);
  } catch {
    return [];
  }
}

async function loadToolResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "tool", "/api/resources/tools");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "tools", "resources"], isToolResource)
      .map(normalizeToolResourceSummary);
  } catch {
    return [];
  }
}

async function loadToolScaffolds(capabilities = []) {
  try {
    const endpoint = catalogResourceScaffoldEndpoint(capabilities, "tool", "/api/resources/tools/scaffolds");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "presets", "scaffolds", "tools"])
      .filter(isToolScaffoldResource)
      .map(normalizeToolScaffoldSummary);
  } catch {
    return [];
  }
}

async function loadPolicyRuleResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "policy-rule", "/api/resources/policy-rules");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizePolicyRuleCollection(payload);
  } catch {
    return [];
  }
}

function normalizePolicyRuleCollection(payload) {
  const direct = normalizeResourceCollection(payload, ["items", "rules", "policy_rules", "policyRules"]);
  return direct
    .map(item => item && typeof item === "object" ? item : null)
    .filter(isPolicyRuleResource)
    .filter(Boolean);
}

function normalizeResourceCollection(payload, keys = []) {
  return normalizeResourceCollectionValue(payload, keys, new Set());
}

function normalizeResourceItems(payload, keys = [], predicate = null) {
  const items = normalizeResourceCollection(payload, keys)
    .flatMap(item => normalizeResourceItem(item, predicate));
  if (items.length) return items;
  const single = normalizeSingleResource(payload, predicate);
  return single ? [single] : [];
}

function normalizeResourceItem(item, predicate = null) {
  if (!item || typeof item !== "object" || Array.isArray(item)) return [];
  if (looksLikeCollectionEnvelope(item)) return [];
  if (predicate && !predicate(item)) return [];
  return [item];
}

function normalizeSingleResource(payload, predicate = null) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return null;
  if (looksLikeCollectionEnvelope(payload)) return null;
  if (predicate && !predicate(payload)) return null;
  return payload;
}

function normalizeResourceCollectionValue(payload, keys = [], seen = new Set()) {
  if (Array.isArray(payload)) return payload;
  if (!payload || typeof payload !== "object") return [];
  if (seen.has(payload)) return [];
  seen.add(payload);

  for (const key of keys) {
    if (!(key in payload)) continue;
    const nested = normalizeResourceCollectionValue(payload[key], keys, seen);
    if (nested.length) return nested;
  }

  for (const key of ["items", "data", "results", "resources", "values"]) {
    if (!(key in payload)) continue;
    const nested = normalizeResourceCollectionValue(payload[key], keys, seen);
    if (nested.length) return nested;
  }

  return normalizeResourceMap(payload);
}

function normalizeResourceMap(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [];
  if (looksLikeCollectionEnvelope(value)) return [];
  const ignored = new Set(["status", "valid", "error", "message", "count", "total", "updated_at", "diagnostics", "summary"]);
  return Object.entries(value)
    .filter(([key]) => !ignored.has(key))
    .map(([key, item]) => {
      if (!item || typeof item !== "object" || Array.isArray(item)) return null;
      if (looksLikeCollectionEnvelope(item)) return null;
      return item.name ? item : { name: key, ...item };
    })
    .filter(Boolean);
}

function looksLikeCollectionEnvelope(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const keys = Object.keys(value);
  if (!keys.length) return false;
  const envelopeKeys = new Set(["status", "valid", "error", "message", "count", "total", "updated_at", "diagnostics", "summary", "restart_required"]);
  return keys.every(key => envelopeKeys.has(key));
}

function catalogResourceCollectionEndpoint(capabilities = [], kind = "", fallback = "") {
  const capability = resourceCapability(capabilities, kind);
  const declaredPath = String(capability?.collection_path || "").trim();
  if (capability) {
    if (capability.can_list === false || !declaredPath) return "";
    return declaredPath;
  }
  return fallback;
}

function catalogResourceScaffoldEndpoint(capabilities = [], kind = "", fallback = "") {
  const capability = resourceCapability(capabilities, kind);
  const declaredPath = String(capability?.scaffold_path || "").trim();
  if (capability) {
    if (capability.can_scaffold === false || !declaredPath) return "";
    return declaredPath;
  }
  return fallback;
}

function catalogResourceScaffoldPresetEndpoint(kind, presetName, query = "", fallback = "") {
  const base = catalogResourceScaffoldEndpoint(catalogContext.resourceCapabilities, kind, fallback);
  if (!base) return "";
  const encodedPreset = encodeURIComponent(String(presetName || "").trim());
  const path = /\{(?:name|id|preset|scaffold)\}/.test(base)
    ? base
      .replaceAll("{name}", encodedPreset)
      .replaceAll("{id}", encodedPreset)
      .replaceAll("{preset}", encodedPreset)
      .replaceAll("{scaffold}", encodedPreset)
    : `${base.replace(/\/+$/, "")}/${encodedPreset}`;
  return appendQuery(path, query);
}

async function loadTeamTemplateCatalog(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "team-template", "/api/team-templates");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "team_templates", "templates"], isTeamTemplateResource);
  } catch {
    return [];
  }
}

async function loadKitResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "kit", "/api/resources/kits");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "kits"]).filter(isKitResource);
  } catch {
    return [];
  }
}

async function loadKitScaffolds(capabilities = []) {
  try {
    const endpoint = catalogResourceScaffoldEndpoint(capabilities, "kit", "/api/resources/kits/scaffolds");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "presets", "scaffolds", "kits"]).filter(isKitScaffoldResource);
  } catch {
    return [];
  }
}

async function loadPolicyRuleScaffolds(capabilities = []) {
  try {
    const endpoint = catalogResourceScaffoldEndpoint(capabilities, "policy-rule", "/api/resources/policy-rules/scaffolds");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "presets", "scaffolds", "rules", "policy_rules", "policyRules"]).filter(isPolicyRuleScaffoldResource);
  } catch {
    return [];
  }
}

function isKitResource(item) {
  return Boolean(item && typeof item === "object" && String(item.name || "").trim());
}

function isToolResource(item) {
  if (!item || typeof item !== "object") return false;
  return Boolean(String(item.name || item.qualified_name || "").trim());
}

function normalizeToolResourceSummary(item) {
  const risk = item?.risk && typeof item.risk === "object" ? item.risk : {};
  return {
    name: item.name || item.qualified_name || "",
    language: item.language || item.kind || "",
    description: item.description || "",
    command: item.command || "",
    args: Array.isArray(item.args) ? item.args.slice(0, 8) : [],
    enabled: item.enabled,
    isolation: item.isolation || "",
    isolation_profile: item.isolation_profile || "",
    isolation_options: normalizeStringMap(item.isolation_options || {}),
    path: item.path || item.config_path || "",
    config_path: item.config_path || "",
    apply_state: item.apply_state || "",
    apply_message: item.apply_message || "",
    risk: {
      qualified_name: risk.qualified_name || item.name || "",
      server: risk.server || "",
      kind: risk.kind || "",
      risk_level: risk.risk_level || "",
      capabilities: Array.isArray(risk.capabilities) ? risk.capabilities.slice(0, 8) : [],
      workspace_scoped_inputs: risk.workspace_scoped_inputs === true,
      workspace_scope_enforced: risk.workspace_scope_enforced === true,
      destructive: risk.destructive === true,
      requires_approval: risk.requires_approval === true,
      external_sandbox_recommended: risk.external_sandbox_recommended === true,
      sandboxed: risk.sandboxed === true,
      isolation: risk.isolation || "",
      isolation_level: risk.isolation_level || "",
      security_boundary: risk.security_boundary || "",
      warnings: Array.isArray(risk.warnings) ? risk.warnings.slice(0, 4) : [],
      recommendations: Array.isArray(risk.recommendations) ? risk.recommendations.slice(0, 4) : []
    }
  };
}

function isKitScaffoldResource(item) {
  if (!item || typeof item !== "object") return false;
  return Boolean(String(item.name || item.title || item.default_name || "").trim());
}

function isToolScaffoldResource(item) {
  if (!item || typeof item !== "object") return false;
  return Boolean(String(item.name || item.default_name || item.title || "").trim() && (item.isolation || item.language || item.default_image || item.default_isolation_options || item.document || item.capabilities));
}

function normalizeToolScaffoldSummary(item = {}) {
  return {
    ...item,
    name: toolScaffoldName(item),
    title: item.title || "",
    description: item.description || "",
    tags: Array.isArray(item.tags) ? item.tags.slice(0, 8) : [],
    capabilities: Array.isArray(item.capabilities) ? item.capabilities.slice(0, 8) : [],
    safety_guards: Array.isArray(item.safety_guards) ? item.safety_guards.slice(0, 8) : [],
    generated_paths: Array.isArray(item.generated_paths) ? item.generated_paths.slice(0, 8) : [],
    activation_steps: Array.isArray(item.activation_steps) ? item.activation_steps.slice(0, 8) : [],
    recommendations: Array.isArray(item.recommendations) ? item.recommendations.slice(0, 8) : [],
    default_isolation_options: normalizeStringMap(item.default_isolation_options || {})
  };
}

function isPolicyRuleResource(item) {
  if (!item || typeof item !== "object") return false;
  return Boolean(String(item.name || "").trim() && (item.operator || item.expression || item.defaults || item.params || item.label || item.description));
}

function isPolicyRuleScaffoldResource(item) {
  if (!item || typeof item !== "object") return false;
  return Boolean(String(item.name || item.default_name || item.label || "").trim() && (item.operator || item.defaults || item.params || item.expression || item.description));
}

function isTeamTemplateResource(item) {
  if (!item || typeof item !== "object") return false;
  const name = String(item.name || "").trim();
  if (!name) return false;
  return Boolean(
    item.title ||
    item.description ||
    item.category ||
    item.tags ||
    item.roles ||
    item.quorum_presets ||
    item.recommended_workflow ||
    item.recommended_entry_agent ||
    item.source ||
    item.custom ||
    item.handoffs ||
    item.blackboard
  );
}

function isWorkflowTemplateResource(item) {
  if (!item || typeof item !== "object") return false;
  const name = String(item.name || item.workflow || "").trim();
  if (!name) return false;
  return Boolean(
    item.title ||
    item.description ||
    item.category ||
    item.tags ||
    item.stages ||
    item.graph ||
    item.source ||
    item.custom
  );
}

function isWorkflowNodeMetadataResource(item) {
  if (!item || typeof item !== "object") return false;
  const type = String(item.type || item.name || "").trim();
  if (!type) return false;
  return Boolean(
    item.label ||
    item.description ||
    item.fields ||
    item.hints ||
    item.tags ||
    item.source ||
    item.custom ||
    item.path
  );
}

function isExpressionHelperResource(item) {
  if (!item || typeof item !== "object") return false;
  const name = String(item.name || "").trim();
  if (!name) return false;
  return Boolean(
    item.label ||
    item.description ||
    item.signature ||
    item.modes ||
    item.examples ||
    item.args ||
    item.source ||
    item.custom
  );
}

async function loadWorkflowTemplateCatalog(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "workflow-template", "/api/workflow-templates");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "templates", "workflow_templates"], isWorkflowTemplateResource);
  } catch {
    return [];
  }
}

async function loadWorkflowGraphsForCatalog(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "workflow", "/api/workflow-graphs");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "graphs", "workflows"])
      .map(workflow => workflow && typeof workflow === "object" ? workflow : null)
      .filter(workflow => workflow?.name);
  } catch {
    return [];
  }
}

async function loadWorkflowTemplateResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "workflow-template", "/api/resources/workflow-templates");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "templates", "workflow_templates"], isWorkflowTemplateResource);
  } catch {
    return [];
  }
}

async function loadWorkflowSchemaCatalog() {
  try {
    const payload = await request("/api/workflow-schemas");
    return normalizeResourceCollection(payload, ["items", "schemas", "workflow_schemas"])
      .map(schema => schema && typeof schema === "object" ? schema : null)
      .filter(schema => schema?.workflow);
  } catch {
    return [];
  }
}

async function loadWorkflowSchemaResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "workflow-schema", "/api/resources/workflow-schemas");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceCollection(payload, ["items", "schemas", "workflow_schemas", "workflow_schema_resources"])
      .map(resource => resource && typeof resource === "object" ? resource : null)
      .filter(Boolean);
  } catch {
    return [];
  }
}

async function loadWorkflowOptionsForCatalog() {
  try {
    return await request("/api/workflow-options");
  } catch {
    return {};
  }
}

async function loadWorkflowNodeMetadataResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "node-metadata", "/api/resources/workflow-node-metadata");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "node_types", "workflow_node_metadata"], isWorkflowNodeMetadataResource);
  } catch {
    return [];
  }
}

async function loadExpressionHelperResources(capabilities = []) {
  try {
    const endpoint = catalogResourceCollectionEndpoint(capabilities, "expression-helper", "/api/resources/expression-helpers");
    if (!endpoint) return [];
    const payload = await request(endpoint);
    return normalizeResourceItems(payload, ["items", "expression_functions", "expression_helpers"], isExpressionHelperResource);
  } catch {
    return [];
  }
}

async function deleteTeamTemplate(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("team-template", name, `/api/resources/team-templates/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.teamTemplateSavedRefresh")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function deleteKit(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("kit", name, `/api/resources/kits/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.kitDeleted")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function handleCatalogResourceAction(root, button) {
  const kind = String(button.dataset.resourceActionKind || "").replaceAll("-", "_");
  const actionName = String(button.dataset.resourceAction || "").replaceAll("-", "_");
  const resourceName = button.dataset.resourceActionName || "";
  const action = resourceAction(catalogContext.resourceCapabilities, kind, actionName);
  if (kind === "kit" && actionName === "import_bundle") {
    root.querySelector("#kitBundleImportFile")?.click();
    return;
  }
  if (kind === "kit" && actionName === "export_bundle") {
    exportKitBundle(resourceName, action);
    return;
  }
  if (kind === "kit" && actionName === "validate_saved") {
    await validateSavedKit(root, resourceName, action, button);
    return;
  }
  if (kind === "workflow_schema" && actionName === "import_catalog") {
    root.querySelector("#workflowSchemaImportFile")?.click();
    return;
  }
  if (kind === "workflow_schema" && actionName === "export_catalog") {
    exportWorkflowSchemaCatalog(action);
    return;
  }
  if (kind === "workflow_schema" && actionName === "capture") {
    await captureWorkflowSchemaResource(root, resourceName, action, button);
    return;
  }
  if (kind === "workflow_schema" && actionName === "activate") {
    await activateWorkflowSchemaResource(root, resourceName, action, button);
    return;
  }
  await executeGenericCatalogResourceAction(root, button, kind, actionName, resourceName, action);
}

async function handleCatalogCapabilityAction(root, button) {
  const kind = String(button.dataset.resourceCapabilityKind || root.querySelector("#resourceType")?.value || "").replaceAll("-", "_");
  const actionName = String(button.dataset.resourceCapabilityAction || "").replaceAll("-", "_");
  const type = root.querySelector("#resourceType")?.value || kind.replaceAll("_", "-");
  const resourceName = button.dataset.resourceCapabilityResource || currentCatalogResourceName(root, type);
  const action = resourceAction(catalogContext.resourceCapabilities, kind, actionName);
  if (kind === "kit" && actionName === "import_bundle") {
    root.querySelector("#kitBundleImportFile")?.click();
    return;
  }
  if (kind === "kit" && actionName === "export_bundle") {
    exportKitBundle(resourceName, action);
    return;
  }
  if (kind === "kit" && actionName === "validate_saved") {
    await validateSavedKit(root, resourceName, action, button);
    return;
  }
  if (kind === "workflow_schema" && actionName === "import_catalog") {
    root.querySelector("#workflowSchemaImportFile")?.click();
    return;
  }
  if (kind === "workflow_schema" && actionName === "export_catalog") {
    exportWorkflowSchemaCatalog(action);
    return;
  }
  if (kind === "workflow_schema" && actionName === "capture") {
    await captureWorkflowSchemaResource(root, resourceName, action, button);
    return;
  }
  if (kind === "workflow_schema" && actionName === "activate") {
    await activateWorkflowSchemaResource(root, resourceName, action, button);
    return;
  }
  await executeGenericCatalogResourceAction(root, button, kind, actionName, resourceName, action);
}

async function executeGenericCatalogResourceAction(root, button, kind, actionName, resourceName, action = null) {
  const output = root.querySelector("#resourceOutput");
  const resourceType = root.querySelector("#resourceType")?.value || kind.replaceAll("_", "-");
  const normalized = normalizeName(resourceName || currentCatalogResourceName(root, resourceType));
  const endpoint = resourceActionPath(action, normalized, { format: "json" });
  if (!action || !endpoint) {
    if (output) output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  if (resourceActionNeedsName(action) && !normalized) {
    if (output) output.textContent = t("catalog.capabilityActionNeedsSavedResource");
    root.querySelector("#resourceName")?.focus();
    return;
  }
  const method = resourceActionMethod(action, button?.dataset.resourceCapabilityMethod || "POST");
  if (method === "GET" && catalogResourceActionLooksLikeDownload(action)) {
    downloadCatalogResourceAction(action, normalized);
    if (output) output.textContent = `${t("catalog.capabilityActionDownloadStarted")} ${resourceActionLabel(action, actionName, kind)}`;
    return;
  }
  setCatalogResourceActionPending(button, true);
  if (output) output.textContent = `${t("catalog.capabilityActionRunning")} ${resourceActionLabel(action, actionName, kind)}...`;
  try {
    const body = catalogResourceActionBody(root, resourceType, actionName);
    const options = { method };
    if (method !== "GET" && method !== "HEAD") {
      options.headers = { "Content-Type": "application/json" };
      options.body = JSON.stringify(body);
    }
    const result = await request(endpoint, options);
    renderCatalogResourceActionResult(root, action, result, normalized, kind);
    await refreshCatalogResources(root);
  } catch (error) {
    if (output) output.textContent = `${t("catalog.capabilityActionFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.capabilityActionFailed"))}`;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

function setCatalogResourceActionPending(button, pending) {
  if (!button) return;
  if (pending) {
    if (!button.dataset.pendingPreviousDisabled) {
      button.dataset.pendingPreviousDisabled = button.disabled ? "true" : "false";
    }
    button.disabled = true;
    button.setAttribute("aria-disabled", "true");
    button.setAttribute("aria-busy", "true");
    return;
  }
  const wasDisabled = button.dataset.pendingPreviousDisabled === "true";
  delete button.dataset.pendingPreviousDisabled;
  button.disabled = wasDisabled;
  button.setAttribute("aria-disabled", wasDisabled ? "true" : "false");
  button.setAttribute("aria-busy", "false");
}

function currentCatalogResourceName(root, type = "") {
  if (type === "provider") return normalizeName(root.querySelector("#providerID")?.value || root.querySelector("#resourceName")?.value || "");
  if (type === "node-metadata") return normalizeName(root.querySelector("#nodeMetadataType")?.value || root.querySelector("#resourceName")?.value || "");
  if (type === "expression-helper") return normalizeName(root.querySelector("#expressionHelperSource")?.value || root.querySelector("#resourceName")?.value || "").replaceAll("-", "_");
  return normalizeName(root.querySelector("#resourceName")?.value || "");
}

function catalogResourceReadEndpoint(kind, resourceName = "", fallback = "") {
  const capability = resourceCapability(catalogContext.resourceCapabilities, kind);
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) return declaredPath ? resourceActionPath({ path: declaredPath }, resourceName, { format: "json" }) : "";
  return fallback;
}

function catalogResourceReadEndpointCandidates(kind, resourceName = "", fallback = "") {
  const primary = catalogResourceReadEndpoint(kind, resourceName, fallback);
  return [...new Set([primary, fallback].map(value => String(value || "").trim()).filter(Boolean))];
}

async function requestFirstCatalogEndpoint(endpoints = [], fallbackMessage = "") {
  let lastError = null;
  for (const endpoint of endpoints) {
    try {
      return await request(endpoint);
    } catch (error) {
      lastError = error;
    }
  }
  if (lastError) throw lastError;
  throw new Error(fallbackMessage || t("catalog.capabilityActionUnavailable"));
}

function requestCatalogTeamTemplateDetail(name) {
  const fallback = `/api/team-templates/${encodeURIComponent(name)}`;
  return requestFirstCatalogEndpoint(catalogResourceReadEndpointCandidates("team-template", name, fallback), t("catalog.capabilityActionUnavailable"));
}

function requestCatalogWorkflowTemplateDetail(name) {
  const fallback = `/api/workflow-templates/${encodeURIComponent(name)}`;
  return requestFirstCatalogEndpoint(catalogResourceReadEndpointCandidates("workflow-template", name, fallback), t("catalog.capabilityActionUnavailable"));
}

function catalogResourceDetailEndpoint(kind, resourceName = "", fallback = "") {
  const capability = resourceCapability(catalogContext.resourceCapabilities, kind);
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) {
    if (capability.can_delete === false || !declaredPath) return "";
    return resourceActionPath({ path: declaredPath }, resourceName, { format: "json" });
  }
  return fallback;
}

function catalogResourceWriteEndpoint(kind, resourceName = "", fallback = "") {
  const capability = resourceCapability(catalogContext.resourceCapabilities, kind);
  const declaredPath = String(capability?.detail_path || "").trim();
  if (capability) {
    if ((capability.can_update === false && capability.can_create === false) || !declaredPath) return "";
    return resourceActionPath({ path: declaredPath }, resourceName, { format: "json" });
  }
  return fallback;
}

function catalogResourceActionEndpoint(kind, actionName, resourceName = "", options = {}) {
  const action = options.action || resourceAction(catalogContext.resourceCapabilities, kind, actionName);
  const pathOptions = options.pathOptions || {};
  const endpoint = resourceActionPath(action, resourceName, pathOptions);
  if (action && endpoint) {
    return {
      action,
      endpoint,
      method: resourceActionMethod(action, options.methodFallback || "POST"),
      fallback: false
    };
  }
  const capability = resourceCapability(catalogContext.resourceCapabilities, kind);
  if (!capability && options.fallback) {
    return {
      action: null,
      endpoint: options.fallback,
      method: String(options.fallbackMethod || "POST").trim().toUpperCase() || "POST",
      fallback: true
    };
  }
  return {
    action,
    endpoint: "",
    method: resourceActionMethod(action, options.methodFallback || "POST"),
    fallback: false
  };
}

function catalogResourceActionBody(root, type, actionName) {
  if (type === "workflow-template" && actionName === "fork") {
    return {
      source: root.querySelector("#workflowTemplateSource")?.value || root.querySelector("#resourceName")?.value || "",
      title: root.querySelector("#workflowTemplateTitle")?.value?.trim() || "",
      description: root.querySelector("#resourceDescription")?.value?.trim() || ""
    };
  }
  if (type === "workflow-template" && actionName === "capture") {
    return {
      workflow: root.querySelector("#workflowTemplateCaptureSource")?.value || root.querySelector("#workflowTemplateSource")?.value || "",
      title: root.querySelector("#workflowTemplateTitle")?.value?.trim() || "",
      description: root.querySelector("#resourceDescription")?.value?.trim() || ""
    };
  }
  if (type === "workflow-schema" && actionName === "activate") {
    return { merge: Boolean(root.querySelector("#workflowSchemaActivateMerge")?.checked ?? true) };
  }
  if (type === "workflow-schema" && actionName === "capture") {
    return { description: root.querySelector("#resourceDescription")?.value?.trim() || t("catalog.workflowSchemaDescriptionDefault") };
  }
  return collectResourceDraft(root);
}

function catalogResourceActionLooksLikeDownload(action = {}) {
  const name = String(action.name || "").toLowerCase();
  const returns = resourceActionReturnTokens(action.returns).join(" ").toLowerCase();
  return name.startsWith("export") || returns.includes("bundle") || returns.includes("graph");
}

function downloadCatalogResourceAction(action, resourceName) {
  const link = document.createElement("a");
  const returns = resourceActionReturnTokens(action.returns).join(" ").toLowerCase();
  const format = returns.includes("catalog") || returns.includes("json") && !returns.includes("bundle") ? "json" : "yaml";
  link.href = resourceActionPath(action, resourceName, { format }) || resourceActionPath(action, resourceName, { format: "yaml" });
  link.download = `${resourceName || "resource"}-${String(action.name || "export").replaceAll("_", "-")}.${format === "json" ? "json" : "yaml"}`;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

function renderCatalogResourceActionResult(root, action, result, resourceName, kind) {
  renderBackendResourceSaveOutcome(root, kind, result, {
    tone: result?.valid === false ? "warn" : "good",
    title: t("catalog.capabilityActionComplete"),
    body: resourceActionDescription(action, kind) || t("catalog.capabilityActionCompleteBody"),
    facts: [
      { label: t("catalog.capabilityActions"), value: resourceActionLabel(action, action?.name || "", kind) },
      { label: t("catalog.name"), value: resourceDisplayValue(result?.name || result?.id || result?.workflow || resourceName) },
      { label: t("catalog.capabilityActionReturnType"), value: resourceActionReturnLabel(action?.returns) }
    ],
    path: result?.path || result?.config_path || "",
    paths: [{ label: t("catalog.saveOutcomeConfigPath"), value: result?.config_path }]
  });
}

function exportKitBundle(name, action = null) {
  const normalized = normalizeName(name);
  if (!normalized) return;
  const resolved = catalogResourceActionEndpoint("kit", "export_bundle", normalized, {
    action,
    fallback: `/api/resources/kits/${encodeURIComponent(normalized)}/export?format=yaml`,
    methodFallback: "GET",
    pathOptions: { format: "yaml" }
  });
  if (!resolved.endpoint) {
    const output = document.querySelector("#resourceOutput");
    if (output) output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  const link = document.createElement("a");
  link.href = resolved.endpoint;
  link.download = `${normalized}-kit-bundle.yaml`;
  document.body.appendChild(link);
  link.click();
  link.remove();
}

async function importKitBundle(root, file) {
  const output = root.querySelector("#resourceOutput");
  const input = root.querySelector("#kitBundleImportFile");
  if (!file) return;
  const resolved = catalogResourceActionEndpoint("kit", "import_bundle", "", {
    fallback: "/api/resources/kits/import",
    methodFallback: "POST"
  });
  if (!resolved.endpoint) {
    output.textContent = t("catalog.capabilityActionUnavailable");
    if (input) input.value = "";
    return;
  }
  const endpoint = appendQuery(resolved.endpoint, "overwrite=false");
  output.textContent = `${t("catalog.importingKitBundle")} ${file.name}...`;
  try {
    const body = await readTextFile(file);
    const contentType = /\.(ya?ml)$/i.test(file.name) ? "application/yaml" : "application/json";
    const result = await request(endpoint, {
      method: resolved.method,
      headers: { "Content-Type": contentType },
      body
    });
    renderKitBundleImportOutcome(root, result);
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.importKitFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.importKitFailed"))}`;
  } finally {
    if (input) input.value = "";
  }
}

function exportWorkflowSchemaCatalog(action = null) {
  const resolved = catalogResourceActionEndpoint("workflow-schema", "export_catalog", "", {
    action,
    fallback: "/api/workflow-schemas/export?format=json",
    methodFallback: "GET",
    pathOptions: { format: "json" }
  });
  if (!resolved.endpoint) {
    const output = document.querySelector("#resourceOutput");
    if (output) output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  const link = document.createElement("a");
  link.href = resolved.endpoint;
  link.download = "workflow-schemas.json";
  document.body.appendChild(link);
  link.click();
  link.remove();
}

async function importWorkflowSchemaCatalog(root, file) {
  const output = root.querySelector("#resourceOutput");
  const input = root.querySelector("#workflowSchemaImportFile");
  if (!file) return;
  const resolved = catalogResourceActionEndpoint("workflow-schema", "import_catalog", "", {
    fallback: "/api/workflow-schemas/import",
    methodFallback: "POST"
  });
  if (!resolved.endpoint) {
    output.textContent = t("catalog.capabilityActionUnavailable");
    if (input) input.value = "";
    return;
  }
  const endpoint = appendQuery(resolved.endpoint, "merge=true");
  output.textContent = `${t("catalog.importingWorkflowSchemaCatalog")} ${file.name}...`;
  try {
    const body = await readTextFile(file);
    const result = await request(endpoint, {
      method: resolved.method,
      headers: { "Content-Type": "application/json" },
      body
    });
    renderWorkflowSchemaImportOutcome(root, result);
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.workflowSchemaImportFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.workflowSchemaImportFailed"))}`;
  } finally {
    if (input) input.value = "";
  }
}

async function activateWorkflowSchemaResource(root, name, action = null, button = null) {
  const output = root.querySelector("#resourceOutput");
  const normalized = normalizeName(name);
  if (!normalized) return;
  const resolved = catalogResourceActionEndpoint("workflow-schema", "activate", normalized, {
    action,
    fallback: `/api/resources/workflow-schemas/${encodeURIComponent(normalized)}/activate`,
    methodFallback: "POST"
  });
  if (!resolved.endpoint) {
    output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  const merge = Boolean(root.querySelector("#workflowSchemaActivateMerge")?.checked ?? true);
  setCatalogResourceActionPending(button, true);
  output.textContent = `${t("catalog.activatingWorkflowSchema")} ${normalized}...`;
  try {
    const schema = await request(resolved.endpoint, {
      method: resolved.method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ merge })
    });
    renderBackendResourceSaveOutcome(root, "workflow-schema", schema, {
      title: t("catalog.workflowSchemaActivated"),
      body: t("catalog.workflowSchemaActivatedBody"),
      facts: [
        { label: t("catalog.name"), value: schema.workflow || normalized },
        { label: t("catalog.schemaStages"), value: workflowSchemaStageCount(schema) },
        { label: t("catalog.schemaOutputs"), value: workflowSchemaOutputCount(schema) }
      ],
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio") }]
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.workflowSchemaActivateFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.workflowSchemaActivateFailed"))}`;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

async function captureWorkflowSchemaResource(root, name, action = null, button = null) {
  const output = root.querySelector("#resourceOutput");
  const normalized = normalizeName(name);
  if (!normalized) return;
  const resolved = catalogResourceActionEndpoint("workflow-schema", "capture", normalized, {
    action,
    fallback: `/api/resources/workflow-schemas/${encodeURIComponent(normalized)}/capture`,
    methodFallback: "POST"
  });
  if (!resolved.endpoint) {
    output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  setCatalogResourceActionPending(button, true);
  output.textContent = `${t("catalog.capturingWorkflowSchema")} ${normalized}...`;
  try {
    const saved = await request(resolved.endpoint, {
      method: resolved.method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ description: t("catalog.workflowSchemaDescriptionDefault") })
    });
    renderBackendResourceSaveOutcome(root, "workflow-schema", saved, {
      title: t("catalog.workflowSchemaCaptured"),
      body: t("catalog.workflowSchemaCapturedBody"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceWorkflowSchema") },
        { label: t("catalog.name"), value: saved.name || normalized },
        { label: t("catalog.schemaStages"), value: saved.schema ? workflowSchemaStageCount(saved.schema) : "" },
        { label: t("catalog.schemaOutputs"), value: saved.schema ? workflowSchemaOutputCount(saved.schema) : "" }
      ],
      path: saved.path
    });
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.workflowSchemaCaptureFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.workflowSchemaCaptureFailed"))}`;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

async function validateSavedKit(root, name, action = null, button = null) {
  const output = root.querySelector("#resourceOutput");
  const normalized = normalizeName(name);
  if (!normalized) return;
  const resolved = catalogResourceActionEndpoint("kit", "validate_saved", normalized, {
    action,
    fallback: `/api/resources/kits/${encodeURIComponent(normalized)}/validate`,
    methodFallback: "GET"
  });
  if (!resolved.endpoint) {
    output.textContent = t("catalog.capabilityActionUnavailable");
    return;
  }
  setCatalogResourceActionPending(button, true);
  output.textContent = `${t("catalog.validating")} ${normalized}...`;
  try {
    const result = await request(resolved.endpoint, { method: resolved.method });
    renderKitValidationActionOutcome(root, normalized, result);
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.validationFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.validationFailed"))}`;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

async function createKitFromScaffold(root, presetName, button = null) {
  const output = root.querySelector("#resourceOutput");
  const preset = String(presetName || "").trim();
  if (!preset) return;
  const card = button?.closest(".kit-scaffold-card");
  const materialize = button?.dataset.forceMaterialize === "true" || Boolean(card?.querySelector("[data-kit-materialize]")?.checked);
  setCatalogResourceActionPending(button, true);
  if (output) output.textContent = `${materialize ? t("catalog.creatingMaterializedKitFromScaffold") : t("catalog.creatingKitFromScaffold")} ${preset}...`;
  try {
    const endpoint = catalogResourceScaffoldPresetEndpoint("kit", preset, materialize ? "materialize=1" : "", "/api/resources/kits/scaffolds");
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const result = await request(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ materialize })
    });
    const kit = result?.kit || {};
    await refreshCatalogResources(root);
    if (kit.name) {
      await openDesigner(root, catalogContext.runtime || {}, "kit", kit.name, catalogContext.providerOptions || []);
    }
    renderKitScaffoldOutcome(root, result, { preset, materialize });
  } catch (error) {
    if (output) output.textContent = `${t("catalog.createKitScaffoldFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.createKitScaffoldFailed"))}`;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

async function openToolScaffoldDesigner(root, runtime, presetName, providerOptions = []) {
  await openDesigner(root, runtime, "tool", null, providerOptions);
  const select = root.querySelector("#toolScaffoldPreset");
  if (select && [...select.options].some(option => option.value === presetName)) {
    select.value = presetName;
  }
  await applyToolScaffold(root, { silent: true });
}

async function applyToolScaffold(root, options = {}) {
  const preset = selectedToolScaffold(root);
  const output = root.querySelector("#resourceOutput");
  if (!preset?.name) return;
  const rawName = normalizeName(root.querySelector("#resourceName").value || "");
  const name = rawName && rawName !== "custom-tool" ? rawName : normalizeName(preset.default_name || preset.name);
  if (!options.silent && output) output.textContent = `${t("catalog.loading")} ${toolScaffoldTitle(preset)}...`;
  try {
    const endpoint = catalogResourceScaffoldPresetEndpoint("tool", preset.name, `name=${encodeURIComponent(name)}`, "/api/resources/tools/scaffolds");
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const payload = await request(endpoint);
    const doc = payload?.document || payload?.preset?.document || payload?.tool || null;
    applyToolScaffoldDocument(root, doc, payload?.name ? payload : preset);
    renderToolScaffoldPreview(root, payload || preset, doc);
    if (!options.silent && output) output.textContent = t("catalog.toolScaffoldApplied");
  } catch (error) {
    if (output) output.textContent = `${t("catalog.toolScaffoldFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.toolScaffoldFailed"))}`;
  }
}

async function createToolFromScaffold(root) {
  const preset = selectedToolScaffold(root);
  const output = root.querySelector("#resourceOutput");
  if (!preset?.name) return;
  const body = toolScaffoldRequestBody(root, preset);
  output.textContent = `${t("catalog.creatingToolFromScaffold")} ${body.name || preset.name}...`;
  try {
    const query = body.overwrite ? "?overwrite=1" : "";
    const endpoint = catalogResourceScaffoldPresetEndpoint("tool", preset.name, query, "/api/resources/tools/scaffolds");
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const result = await request(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    const tool = result?.tool || {};
    await refreshCatalogResources(root);
    if (tool.name) {
      await openDesigner(root, catalogContext.runtime || {}, "tool", tool.name, catalogContext.providerOptions || []);
    }
    renderBackendResourceSaveOutcome(root, "tool", result, {
      tone: result?.restart_required ? "warn" : "good",
      title: result?.updated ? t("catalog.toolScaffoldUpdated") : t("catalog.toolScaffoldCreated"),
      body: result?.restart_required ? t("catalog.toolScaffoldSavedRestart") : t("catalog.toolScaffoldSavedRefresh"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourceTool") },
        { label: t("catalog.name"), value: tool.name || body.name },
        { label: t("catalog.isolation"), value: enumLabel("isolation", tool.isolation || preset.isolation || "") },
        { label: t("catalog.isolationProfile"), value: tool.isolation_profile ? isolationProfileDisplayLabel(tool.isolation_profile) : "" }
      ],
      path: tool.path || tool.config_path,
      paths: [{ label: t("catalog.saveOutcomeConfigPath"), value: tool.config_path }],
      sections: [
        ...starterFlowSections("tool", result, { materialized: true }),
        ...toolScaffoldOutcomeSections(result, preset)
      ],
      includeSettingsAction: result?.restart_required === true,
      actions: scaffoldOutcomeActions(result, { workflowPrimary: false })
    });
  } catch (error) {
    output.textContent = `${t("catalog.toolScaffoldFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.toolScaffoldFailed"))}`;
  }
}

async function openPolicyRuleScaffoldDesigner(root, runtime, presetName, providerOptions = []) {
  await openDesigner(root, runtime, "policy-rule", null, providerOptions);
  const select = root.querySelector("#policyScaffoldPreset");
  if (select && [...select.options].some(option => option.value === presetName)) {
    select.value = presetName;
  }
  await applyPolicyRuleScaffold(root, { silent: true });
}

async function applyPolicyRuleScaffold(root, options = {}) {
  const preset = selectedPolicyRuleScaffold(root);
  const output = root.querySelector("#resourceOutput");
  if (!preset?.name) return;
  const name = normalizeName(root.querySelector("#resourceName").value || preset.default_name || preset.name);
  if (!options.silent && output) output.textContent = `${t("catalog.loading")} ${policyRuleScaffoldTitle(preset)}...`;
  try {
    const endpoint = catalogResourceScaffoldPresetEndpoint("policy-rule", preset.name, `name=${encodeURIComponent(name)}`, "/api/resources/policy-rules/scaffolds");
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const payload = await request(endpoint);
    const doc = payload?.document || payload?.preset?.document || payload?.rule || null;
    if (doc) {
      loadPolicyRuleForm(root, doc);
      root.querySelector("#policyScaffoldPreset").value = preset.name;
    }
    renderPolicyRuleScaffoldPreview(root, payload || preset, doc);
    if (!options.silent && output) output.textContent = t("catalog.policyRuleScaffoldApplied");
  } catch (error) {
    if (output) output.textContent = `${t("catalog.policyRuleScaffoldFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.policyRuleScaffoldFailed"))}`;
  }
}

async function createPolicyRuleFromScaffold(root) {
  const preset = selectedPolicyRuleScaffold(root);
  const output = root.querySelector("#resourceOutput");
  if (!preset?.name) return;
  const body = policyRuleScaffoldRequestBody(root, preset);
  output.textContent = `${t("catalog.creatingPolicyRuleFromScaffold")} ${body.name}...`;
  try {
    const query = body.overwrite ? "?overwrite=1" : "";
    const endpoint = catalogResourceScaffoldPresetEndpoint("policy-rule", preset.name, query, "/api/resources/policy-rules/scaffolds");
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    const result = await request(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    const rule = result?.rule || {};
    renderResourceSaveOutcome(root, {
      tone: "good",
      title: result?.updated ? t("catalog.policyRuleScaffoldUpdated") : t("catalog.policyRuleScaffoldCreated"),
      body: t("catalog.policyRuleSavedRefresh"),
      facts: [
        { label: t("catalog.resourceType"), value: t("catalog.resourcePolicyRule") },
        { label: t("catalog.name"), value: rule.name || body.name },
        { label: t("catalog.policyOperator"), value: rule.operator || preset.operator || "expression" }
      ],
      path: rule.path,
      actions: [{ action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: true }]
    });
    await refreshCatalogResources(root);
    if (rule.name) {
      await openDesigner(root, catalogContext.runtime || {}, "policy-rule", rule.name, catalogContext.providerOptions || []);
    }
  } catch (error) {
    output.textContent = `${t("catalog.policyRuleScaffoldFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.policyRuleScaffoldFailed"))}`;
  }
}

function readTextFile(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error(t("catalog.readFileFailed")));
    reader.readAsText(file, "utf-8");
  });
}

function kitBundleImportSummary(result = {}) {
  const kit = result?.kit || {};
  const saved = Array.isArray(result?.saved) ? result.saved : [];
  const skipped = Array.isArray(result?.skipped) ? result.skipped : [];
  const errors = Array.isArray(result?.errors) ? result.errors : [];
  const warnings = Array.isArray(result?.warnings) ? result.warnings : [];
  return [
    `${t("catalog.importKitDone")} ${kit.name || t("catalog.resourceKit")}`,
    `${t("catalog.kitImportSaved", { count: saved.length })} / ${t("catalog.kitImportSkipped", { count: skipped.length })} / ${t("catalog.kitImportErrors", { count: errors.length })}`,
    result.restart_required ? t("catalog.restartRequired") : "",
    `${t("catalog.kitValidationResult")}: ${kitValidationText(result.validation || kit.validation)}`,
    formatKitBundleEntries(t("catalog.kitImportSavedItems"), saved),
    formatKitBundleEntries(t("catalog.kitImportSkippedItems"), skipped),
    formatKitBundleEntries(t("catalog.kitImportErrorItems"), errors),
    warnings.length ? `${t("catalog.kitImportWarnings")}\n${warnings.slice(0, 6).map(item => `- ${localizedText(item)}`).join("\n")}` : ""
  ].filter(Boolean).join("\n");
}

function workflowSchemaImportSummary(result = {}) {
  const schemas = Array.isArray(result?.schemas) ? result.schemas : [];
  const imported = Number(result?.imported || schemas.length || 0);
  return [
    t("catalog.workflowSchemaImported", { count: imported }),
    ...schemas.slice(0, 8).map(schema => `- ${schema.workflow || t("catalog.resourceWorkflowSchema")}: ${workflowSchemaStageCount(schema)} ${t("catalog.schemaStages")} / ${workflowSchemaOutputCount(schema)} ${t("catalog.schemaOutputs")}`)
  ].join("\n");
}

function renderKitBundleImportOutcome(root, result = {}) {
  const kit = result?.kit || {};
  const saved = Array.isArray(result?.saved) ? result.saved : [];
  const skipped = Array.isArray(result?.skipped) ? result.skipped : [];
  const errors = Array.isArray(result?.errors) ? result.errors : [];
  const warnings = Array.isArray(result?.warnings) ? result.warnings : [];
  const validation = result.validation || kit.validation;
  const sections = [
    saved.length ? {
      title: t("catalog.kitImportSavedItems"),
      badge: t("catalog.kitImportSaved", { count: saved.length }),
      html: kitBundleEntriesHTML(saved)
    } : null,
    skipped.length ? {
      title: t("catalog.kitImportSkippedItems"),
      badge: t("catalog.kitImportSkipped", { count: skipped.length }),
      html: kitBundleEntriesHTML(skipped)
    } : null,
    warnings.length ? {
      title: t("catalog.kitImportWarnings"),
      html: kitScaffoldListHTML(warnings)
    } : null,
    errors.length ? {
      title: t("catalog.kitImportErrorItems"),
      badge: t("catalog.kitImportErrors", { count: errors.length }),
      html: kitScaffoldListHTML(errors.map(item => typeof item === "string" ? item : item?.message || JSON.stringify(item)))
    } : null
  ].filter(Boolean);
  renderBackendResourceSaveOutcome(root, "kit", result, {
    tone: errors.length || validation?.valid === false ? "warn" : "good",
    title: t("catalog.importKitDone"),
    body: t("catalog.importKitBundleBody"),
    facts: [
      { label: t("catalog.resourceType"), value: t("catalog.resourceKit") },
      { label: t("catalog.name"), value: kit.name || result.name || t("catalog.resourceKit") },
      { label: t("catalog.kitValidationResult"), value: kitValidationText(validation) },
      { label: t("catalog.kitImportSavedItems"), value: saved.length },
      { label: t("catalog.kitImportSkippedItems"), value: skipped.length },
      { label: t("catalog.kitImportErrorItems"), value: errors.length }
    ],
    path: kit.path || result.path,
    sections
  });
}

function renderWorkflowSchemaImportOutcome(root, result = {}) {
  const schemas = Array.isArray(result?.schemas) ? result.schemas : [];
  const imported = Number(result?.imported || schemas.length || 0);
  renderBackendResourceSaveOutcome(root, "workflow-schema", result, {
    title: t("catalog.workflowSchemaImportedTitle"),
    body: t("catalog.workflowSchemaImportedBody"),
    facts: [
      { label: t("catalog.resourceType"), value: t("catalog.resourceWorkflowSchema") },
      { label: t("catalog.workflowSchemaImportedCount"), value: imported },
      { label: t("catalog.schemaStages"), value: schemas.reduce((sum, schema) => sum + workflowSchemaStageCount(schema), 0) },
      { label: t("catalog.schemaOutputs"), value: schemas.reduce((sum, schema) => sum + workflowSchemaOutputCount(schema), 0) }
    ],
    sections: schemas.length ? [{
      title: t("catalog.workflowSchemaImportedItems"),
      html: `<div class="kit-materialized-table" role="list">
        ${schemas.slice(0, 12).map(schema => `<span role="listitem">
          <small>${escapeHTML(t("catalog.resourceWorkflowSchema"))}</small>
          <strong>${escapeHTML(resourceDisplayValue(schema.workflow || schema.name || "-"))}</strong>
          <em>${escapeHTML(`${workflowSchemaStageCount(schema)} ${t("catalog.schemaStages")} / ${workflowSchemaOutputCount(schema)} ${t("catalog.schemaOutputs")}`)}</em>
        </span>`).join("")}
      </div>`
    }] : []
  });
}

function renderKitValidationActionOutcome(root, name, result = {}) {
  const issues = Array.isArray(result?.issues) ? result.issues : [];
  renderBackendResourceSaveOutcome(root, "kit", result, {
    tone: result?.valid === false ? "warn" : "good",
    title: t("catalog.kitValidationResult"),
    body: result?.valid === false ? t("catalog.kitValidationActionNeedsReview") : t("catalog.kitValidationActionPassed"),
    facts: [
      { label: t("catalog.resourceType"), value: t("catalog.resourceKit") },
      { label: t("catalog.name"), value: name },
      { label: t("catalog.kitValidationResult"), value: kitValidationText(result) }
    ],
    sections: issues.length ? [{
      title: t("catalog.kitValidationIssuesTitle"),
      badge: t("catalog.kitValidationIssues", { count: issues.length }),
      html: `<ol class="resource-save-list">${issues.slice(0, 10).map(issue => `<li>${escapeHTML(formatValidationItemLine(issue).replace(/^- /, ""))}</li>`).join("")}</ol>`
    }] : []
  });
}

function kitBundleEntriesHTML(entries = []) {
  const visible = entries.slice(0, 12);
  if (!visible.length) return "";
  return `<div class="kit-materialized-table" role="list">
    ${visible.map(item => {
      const entry = typeof item === "string" ? { name: item } : item || {};
      const label = entry.kind || entry.type || entry.status || t("catalog.resourceType");
      const name = entry.name || entry.path || entry.id || entry.workflow || "-";
      const status = entry.status || entry.reason || entry.message || entry.path || "";
      return `<span role="listitem">
        <small>${escapeHTML(localizedText(label))}</small>
        <strong>${escapeHTML(resourceDisplayValue(name))}</strong>
        ${status ? `<em>${escapeHTML(resourceDisplayValue(status))}</em>` : ""}
      </span>`;
    }).join("")}
  </div>`;
}

function renderKitScaffoldOutcome(root, result = {}, options = {}) {
  const kit = result?.kit || {};
  const resources = Array.isArray(result?.resources) ? result.resources : [];
  const activationSteps = Array.isArray(result?.activation_steps) ? result.activation_steps : [];
  const messages = Array.isArray(result?.messages) ? result.messages : [];
  const nextSteps = Array.isArray(result?.next_steps) ? result.next_steps : [];
  const materialized = result.materialized === true || options.materialize === true;
  const sections = [
    ...starterFlowSections("kit", result, { materialized }),
    resources.length ? {
      title: t("catalog.kitScaffoldResources"),
      badge: t("catalog.kitScaffoldResourcesCount", { count: resources.length }),
      body: t("catalog.kitScaffoldResourcesHelp"),
      html: kitScaffoldResourcesHTML(resources)
    } : null,
    materialized ? {
      title: t("catalog.kitScaffoldBindings"),
      body: t("catalog.kitScaffoldBindingsHelp")
    } : null,
    activationSteps.length ? {
      title: t("catalog.kitScaffoldActivation"),
      html: kitScaffoldListHTML(activationSteps)
    } : null,
    nextSteps.length ? {
      title: t("catalog.kitScaffoldNextSteps"),
      html: kitScaffoldListHTML(nextSteps)
    } : null,
    messages.length ? {
      title: t("catalog.kitScaffoldMessages"),
      html: kitScaffoldListHTML(messages)
    } : null
  ].filter(Boolean);
  renderResourceSaveOutcome(root, {
    tone: result.restart_required ? "warn" : "good",
    title: materialized ? t("catalog.kitScaffoldMaterializedTitle") : (result.updated ? t("catalog.kitScaffoldUpdated") : t("catalog.kitScaffoldCreated")),
    body: materialized ? t("catalog.kitScaffoldMaterializedBody") : t("catalog.kitScaffoldManifestBody"),
    facts: [
      { label: t("catalog.resourceType"), value: t("catalog.resourceKit") },
      { label: t("catalog.name"), value: kit.name || result.preset || options.preset || t("catalog.resourceKit") },
      { label: t("catalog.kitScaffoldMode"), value: materialized ? t("catalog.kitScaffoldModeMaterialized") : t("catalog.kitScaffoldModeManifest") },
      { label: t("catalog.kitValidationResult"), value: kitValidationText(kit.validation) },
      result.restart_required ? { label: t("catalog.saveOutcomeStatus"), value: t("catalog.restartRequired") } : null
    ].filter(Boolean),
    sections,
    actions: scaffoldOutcomeActions(result, { workflowPrimary: materialized })
  });
}

function starterFlowSections(kind, result = {}, options = {}) {
  const materialized = options.materialized === true;
  const restartRequired = result?.restart_required === true;
  const items = [
    {
      tone: "good",
      title: t("catalog.starterFlowSavedTitle"),
      body: materialized ? t("catalog.starterFlowSavedMaterialized") : t("catalog.starterFlowSavedManifest")
    },
    {
      tone: restartRequired ? "warn" : "good",
      title: restartRequired ? t("catalog.starterFlowRestartTitle") : t("catalog.starterFlowRefreshTitle"),
      body: restartRequired ? t("catalog.starterFlowRestartBody") : t("catalog.starterFlowRefreshBody")
    },
    {
      tone: "info",
      title: t("catalog.starterFlowNextTitle"),
      body: kind === "kit" ? t("catalog.starterFlowKitNextBody") : t("catalog.starterFlowToolNextBody")
    }
  ];
  return [{
    title: t("catalog.starterFlowTitle"),
    body: t("catalog.starterFlowHelp"),
    html: `<div class="resource-starter-flow">
      ${items.map(item => `<span class="${escapeHTML(item.tone)}">
        <strong>${escapeHTML(item.title)}</strong>
        <em>${escapeHTML(item.body)}</em>
      </span>`).join("")}
    </div>`
  }];
}

function scaffoldOutcomeActions(result = {}, options = {}) {
  const restartRequired = result?.restart_required === true;
  return [
    restartRequired ? { action: "settings", label: t("catalog.saveOutcomeOpenSettings"), primary: true } : null,
    { action: "workflows", label: t("catalog.saveOutcomeOpenWorkflowStudio"), primary: options.workflowPrimary === true && !restartRequired }
  ].filter(Boolean);
}

function kitScaffoldResourcesHTML(resources = []) {
  if (!resources.length) return "";
  return `<div class="kit-materialized-table" role="list">
    ${resources.slice(0, 12).map(item => `
      <span role="listitem">
        <small>${escapeHTML(localizedText(item?.kind || t("catalog.resourceType")))}</small>
        <strong>${escapeHTML(resourceDisplayValue(item?.name || "-"))}</strong>
        <em>${escapeHTML(localizedText(item?.status || t("catalog.saveOutcomeStatus")))}</em>
      </span>
    `).join("")}
  </div>`;
}

function kitScaffoldListHTML(items = []) {
  const visible = items.map(item => localizedText(item)).filter(Boolean).slice(0, 6);
  if (!visible.length) return "";
  return `<ul class="kit-materialized-list">${visible.map(item => `<li>${escapeHTML(item)}</li>`).join("")}</ul>`;
}

function kitScaffoldSummary(result = {}) {
  const kit = result?.kit || {};
  const warnings = Array.isArray(result?.warnings) ? result.warnings : [];
  const nextSteps = Array.isArray(result?.next_steps) ? result.next_steps : [];
  const state = result.updated ? t("catalog.kitScaffoldUpdated") : t("catalog.kitScaffoldCreated");
  return [
    `${state}: ${kit.name || result.preset || t("catalog.resourceKit")}`,
    `${t("catalog.kitValidationResult")}: ${kitValidationText(kit.validation)}`,
    warnings.length ? `${t("catalog.kitImportWarnings")}\n${warnings.slice(0, 6).map(formatValidationItemLine).join("\n")}` : "",
    nextSteps.length ? `${t("catalog.kitScaffoldNextSteps")}\n${nextSteps.slice(0, 5).map(item => `- ${localizedText(item)}`).join("\n")}` : "",
    t("catalog.kitSavedRefresh")
  ].filter(Boolean).join("\n");
}

function formatKitBundleEntries(label, entries = []) {
  if (!entries.length) return "";
  const lines = entries.slice(0, 8).map(item => {
    const name = item?.name || "-";
    const kind = localizedText(item?.kind || t("catalog.resourceKit"));
    const status = item?.status ? ` (${localizedText(item.status)})` : "";
    const message = item?.message ? ` - ${catalogValidationMessage(item)}` : "";
    return `- ${kind}/${name}${status}${message}`;
  });
  return `${label}\n${lines.join("\n")}`;
}

function formatValidationItemLine(item) {
  if (typeof item === "string") return `- ${localizedText(item)}`;
  const severity = catalogValidationSeverityLabel(item?.severity || "info");
  const ref = resourceValidationIssueLabel(item, "kit");
  const message = catalogValidationMessage(item);
  return `- ${[severity, ref, message].filter(Boolean).join(": ")}`;
}

async function deleteWorkflowTemplateResource(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("workflow-template", name, `/api/resources/workflow-templates/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.workflowTemplateSavedRefresh")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function deleteWorkflowSchemaResource(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("workflow-schema", name, `/api/resources/workflow-schemas/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.workflowSchemaDeleted")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function deleteWorkflowNodeMetadata(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("node-metadata", name, `/api/resources/workflow-node-metadata/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.nodeMetadataSavedRefresh")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function deleteExpressionHelper(root, name) {
  if (!name) return;
  const output = root.querySelector("#resourceOutput");
  output.textContent = `${t("catalog.deleting")} ${name}...`;
  try {
    const endpoint = catalogResourceDetailEndpoint("expression-helper", name, `/api/resources/expression-helpers/${encodeURIComponent(name)}`);
    if (!endpoint) throw new Error(t("catalog.capabilityActionUnavailable"));
    await request(endpoint, { method: "DELETE" });
    output.textContent = `${t("catalog.deleted")} ${name}\n${t("catalog.expressionHelperSavedRefresh")}`;
    await refreshCatalogResources(root);
  } catch (error) {
    output.textContent = `${t("catalog.deleteFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.deleteFailed"))}`;
  }
}

async function validateResourceDraft(root, options = {}) {
  const output = root.querySelector("#resourceOutput");
  const button = root.querySelector("#validateResource");
  const type = root.querySelector("#resourceType").value;
  const endpointType = resourceValidationEndpointType(type);
  if (!endpointType) return { valid: true };
  setCatalogResourceActionPending(button, true);
  clearResourceFieldIssues(root);
  try {
    let doc;
    try {
      doc = collectResourceValidationDocument(root, type);
    } catch (error) {
      const issue = localResourceValidationIssue(type, error);
      const result = { valid: false, name: root.querySelector("#resourceName").value.trim(), issues: [issue] };
      renderResourceValidationResult(root, result, type);
      return result;
    }
    const name = resourceValidationName(type, doc, root);
    const endpoint = resourceValidationEndpoint(type, name);
    if (!endpoint) {
      const result = { valid: false, name, issues: [{ severity: "error", field: "body", message: t("catalog.capabilityActionUnavailable") }] };
      renderResourceValidationResult(root, result, type);
      return result;
    }
    output.textContent = `${t("catalog.validating")} ${name || t("catalog.resourceBuilder")}...`;
    const result = await request(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(doc)
    });
    renderResourceValidationResult(root, result, type);
    return result;
  } catch (error) {
    const result = error.data && typeof error.data === "object"
      ? error.data
      : { valid: false, name: root.querySelector("#resourceName").value.trim(), issues: [{ severity: "error", field: "body", message: localizedCatalogErrorMessage(error, t("catalog.validationIssueFallback")) }] };
    renderResourceValidationResult(root, result, type);
    return result;
  } finally {
    setCatalogResourceActionPending(button, false);
  }
}

async function validateKit(root) {
  const output = root.querySelector("#resourceOutput");
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-kit");
  output.textContent = `${t("catalog.validating")} ${name}...`;
  try {
    const result = await request(`/api/resources/kits/${encodeURIComponent(name)}/validate`, { method: "POST" });
    renderKitValidation(root, result);
    output.textContent = `${t("catalog.kitValidationResult")}: ${kitValidationText(result)}`;
  } catch (error) {
    output.textContent = `${t("catalog.validationFailed")}: ${localizedCatalogErrorMessage(error, t("catalog.validationFailed"))}\n${t("catalog.kitValidateSaveFirst")}`;
  }
}

function collectResourceValidationDocument(root, type) {
  if (type === "skill") return collectSkillForm(root);
  if (type === "agent") return collectAgentForm(root);
  if (type === "tool") return collectToolForm(root);
  if (type === "team-template") return collectTeamTemplateForm(root);
  if (type === "kit") return collectKitForm(root);
  if (type === "workflow-template") return collectWorkflowTemplateResourceForm(root);
  if (type === "workflow-schema") return collectWorkflowSchemaForm(root);
  if (type === "node-metadata") return collectNodeMetadataForm(root);
  if (type === "expression-helper") return collectExpressionHelperForm(root);
  if (type === "provider") return collectProviderForm(root);
  if (type === "workflow") return collectWorkflowForm(root);
  if (type === "policy-rule") return collectPolicyRuleForm(root);
  return collectResourceDraft(root);
}

function resourceValidationEndpoint(type, name = "") {
  const capability = resourceCapability(catalogContext.resourceCapabilities, type);
  const declaredPath = String(capability?.validate_path || "").trim();
  if (declaredPath) return resourceActionPath({ path: declaredPath }, name, { format: "json" });
  if (capability) return "";
  const endpointType = resourceValidationEndpointType(type);
  const base = `/api/${endpointType === "workflow-graphs" ? "workflow-graphs" : `resources/${endpointType}`}`;
  const normalized = String(name || "").trim();
  return normalized ? `${base}/${encodeURIComponent(normalized)}/validate` : `${base}/validate`;
}

function resourceValidationEndpointType(type) {
  return {
    skill: "skills",
    agent: "agents",
    tool: "tools",
    "team-template": "team-templates",
    kit: "kits",
    "workflow-template": "workflow-templates",
    "workflow-schema": "workflow-schemas",
    "node-metadata": "workflow-node-metadata",
    "expression-helper": "expression-helpers",
    provider: "providers",
    workflow: "workflow-graphs",
    "policy-rule": "policy-rules"
  }[type] || "";
}

function resourceValidationName(type, doc, root) {
  if (type === "agent") return doc?.id || root.querySelector("#resourceName")?.value || "";
  if (type === "provider") return doc?.id || root.querySelector("#providerID")?.value || root.querySelector("#resourceName")?.value || "";
  if (type === "workflow-schema") return doc?.name || doc?.schema?.workflow || root.querySelector("#resourceName")?.value || "";
  if (type === "node-metadata") return doc?.type || root.querySelector("#nodeMetadataType")?.value || root.querySelector("#resourceName")?.value || "";
  return doc?.name || root.querySelector("#resourceName")?.value || "";
}

function renderResourceValidationResult(root, result = {}, type = "") {
  const issues = normalizeResourceValidationIssues(result);
  clearResourceFieldIssues(root);
  if (type === "kit") renderKitValidation(root, result);
  applyResourceValidationIssues(root, issues, type);
  renderResourceValidationPanel(root, result, issues, type);
}

function renderResourceValidationPanel(root, result, issues, type) {
  const output = root.querySelector("#resourceOutput");
  if (!output) return;
  const valid = result?.valid !== false;
  const normalizedName = result?.name || resourceValidationNormalizedName(result?.normalized) || root.querySelector("#resourceName")?.value || "";
  const blocking = issues.filter(issue => issue.severity !== "warning" && issue.severity !== "info").length;
  const warnings = issues.length - blocking;
  const normalizedPreview = valid ? renderResourceValidationNormalized(result?.normalized, type) : "";
  const issueHTML = issues.length ? `
    <div class="resource-validation-list">
      ${issues.slice(0, 10).map(issue => `
        <div class="resource-validation-issue ${escapeHTML(issue.severity || "error")}">
          <span>${escapeHTML(resourceValidationIssueLabel(issue, type))}</span>
          <strong>${escapeHTML(catalogValidationMessage(issue))}</strong>
        </div>
      `).join("")}
    </div>
  ` : "";
  output.innerHTML = `
    <div class="resource-validation-result ${valid ? "ok" : "error"}">
      <div class="resource-save-status">
        <span class="resource-save-dot" aria-hidden="true"></span>
        <div>
          <strong>${escapeHTML(valid ? t("catalog.validationPassed") : t("catalog.validationNeedsWork"))}</strong>
          <p>${escapeHTML(valid ? t("catalog.validationNoWrite") : t("catalog.validationFixFields"))}</p>
        </div>
      </div>
      <div class="resource-save-facts">
        <span class="resource-save-fact"><small>${escapeHTML(t("catalog.resourceType"))}</small><strong>${escapeHTML(resourceTypeLabel(type))}</strong></span>
        ${normalizedName ? `<span class="resource-save-fact"><small>${escapeHTML(t("catalog.name"))}</small><strong>${escapeHTML(resourceDisplayValue(normalizedName))}</strong></span>` : ""}
        <span class="resource-save-fact"><small>${escapeHTML(t("catalog.validationIssues"))}</small><strong>${escapeHTML(t("catalog.validationIssueCount", { errors: blocking, warnings }))}</strong></span>
      </div>
      ${issueHTML || normalizedPreview || `<p>${escapeHTML(t("catalog.validationNormalizedReady"))}</p>`}
    </div>
  `;
}

function renderResourceValidationNormalized(normalized, type) {
  if (!normalized || typeof normalized !== "object" || Array.isArray(normalized)) return "";
  const facts = resourceValidationNormalizedFacts(normalized, type);
  const json = resourceValidationNormalizedJSON(normalized);
  return `<details class="resource-validation-normalized">
    <summary>
      <span>
        <strong>${escapeHTML(t("catalog.validationNormalizedPreview"))}</strong>
        <small>${escapeHTML(t("catalog.validationNormalizedPreviewHelp"))}</small>
      </span>
      <em>${escapeHTML(t("catalog.validationNormalizedFactCount", { count: facts.length }))}</em>
    </summary>
    ${facts.length ? `<div class="resource-save-facts">${facts.map(fact => `
      <span class="resource-save-fact">
        <small>${escapeHTML(fact.label)}</small>
        <strong>${escapeHTML(fact.value)}</strong>
      </span>
    `).join("")}</div>` : `<p>${escapeHTML(t("catalog.validationNormalizedNoFacts"))}</p>`}
    <details class="resource-validation-json">
      <summary>${escapeHTML(t("catalog.validationNormalizedJson"))}</summary>
      <pre>${escapeHTML(json)}</pre>
    </details>
  </details>`;
}

function resourceValidationNormalizedFacts(normalized, type) {
  const fields = [
    "name",
    "id",
    "title",
    "kind",
    "type",
    "category",
    "mode",
    "provider",
    "model",
    "isolation",
    "stages",
    "roles",
    "agents",
    "skills",
    "tools",
    "providers",
    "workflows",
    "workflow_templates",
    "team_templates",
    "policy_rules",
    "required_env",
    "fields",
    "outputs",
    "examples",
    "args",
    "models"
  ];
  const facts = [];
  for (const field of fields) {
    if (!(field in normalized)) continue;
    const value = normalized[field];
    if (value == null || value === "") continue;
    facts.push({
      label: resourceValidationNormalizedLabel(field, type),
      value: resourceValidationNormalizedValue(value)
    });
    if (facts.length >= 8) break;
  }
  return facts;
}

function resourceValidationNormalizedName(normalized) {
  if (!normalized || typeof normalized !== "object") return "";
  return normalized.name || normalized.id || normalized.type || normalized.title || "";
}

function resourceValidationNormalizedLabel(field, type) {
  const labels = {
    name: t("catalog.name"),
    id: "ID",
    title: t("catalog.title"),
    kind: t("catalog.resourceType"),
    type: t("catalog.resourceType"),
    category: t("catalog.teamCategory"),
    mode: t("catalog.mode"),
    provider: t("catalog.provider"),
    model: t("catalog.model"),
    isolation: t("catalog.toolIsolation"),
    stages: t("workflow.stages"),
    roles: t("catalog.teamRolesShort"),
    agents: t("catalog.kitAgents"),
    skills: t("catalog.kitSkills"),
    tools: t("catalog.kitTools"),
    providers: t("catalog.kitProviders"),
    workflows: t("catalog.kitWorkflows"),
    workflow_templates: t("catalog.kitWorkflowTemplates"),
    team_templates: t("catalog.kitTeamTemplates"),
    policy_rules: t("catalog.kitPolicyRules"),
    required_env: t("catalog.kitRequiredEnv"),
    fields: t("catalog.nodeFieldsJSON"),
    outputs: t("catalog.nodeOutputsJSON"),
    examples: t("catalog.nodeExamplesJSON"),
    args: t("catalog.expressionArgsJSON"),
    models: t("catalog.providerModels")
  };
  return labels[field] || localizedText(field || type || t("catalog.resourceBuilder"));
}

function resourceValidationNormalizedValue(value) {
  if (Array.isArray(value)) return t("catalog.validationNormalizedArrayCount", { count: value.length });
  if (typeof value === "boolean") return value ? t("catalog.validationNormalizedTrue") : t("catalog.validationNormalizedFalse");
  if (typeof value === "object" && value) return t("catalog.validationNormalizedObjectCount", { count: Object.keys(value).length });
  return localizedText(String(value));
}

function resourceValidationNormalizedJSON(normalized) {
  const text = JSON.stringify(normalized || {}, null, 2);
  if (text.length <= 5000) return text;
  return `${text.slice(0, 5000)}\n${t("catalog.validationNormalizedTruncated")}`;
}

function clearResourceFieldIssues(root) {
  root.querySelectorAll(".resource-field-invalid").forEach(node => node.classList.remove("resource-field-invalid"));
  root.querySelectorAll(".resource-field-error").forEach(node => node.remove());
  root.querySelectorAll("#resourceDesigner [aria-invalid='true']").forEach(node => node.removeAttribute("aria-invalid"));
}

async function refreshConfigDiagnosticsAfterResourceSave(root) {
  catalogContext.configDiagnostics = await loadConfigDiagnosticsForCatalog();
  publishCatalogConfigDiagnostics();
  applyConfigDiagnosticsToDesigner(root);
}

function applyConfigDiagnosticsToDesigner(root) {
  clearResourceDiagnosticIssues(root);
  const diagnostics = currentResourceDiagnostics(root);
  renderResourceDiagnosticsPanel(root, diagnostics);
  if (!diagnostics.length) return;
  const type = root.querySelector("#resourceType")?.value || "";
  for (const item of diagnostics) {
    const fieldName = item.field || item.target_field || "";
    const selector = resourceValidationFieldSelector({ field: fieldName, ref: fieldName, code: item.code || "" }, type);
    if (!selector) continue;
    const field = root.querySelector(selector);
    if (!field) continue;
    const holder = field.closest("label, .resource-isolation-options, .stack, .check") || field.parentElement;
    if (!holder) continue;
    field.setAttribute("aria-invalid", "true");
    holder.classList.add("resource-diagnostic-invalid");
    const message = document.createElement("small");
    message.className = "resource-diagnostic-field-error";
    message.textContent = diagnosticResourceRecommendation(item);
    holder.appendChild(message);
  }
}

function clearResourceDiagnosticIssues(root) {
  root.querySelectorAll(".resource-diagnostic-invalid").forEach(node => {
    node.classList.remove("resource-diagnostic-invalid");
    if (!node.classList.contains("resource-field-invalid")) {
      node.querySelectorAll("[aria-invalid='true']").forEach(field => field.removeAttribute("aria-invalid"));
    }
  });
  root.querySelectorAll(".resource-diagnostic-field-error").forEach(node => node.remove());
  root.querySelector("#resourceOutput .resource-config-diagnostics")?.remove();
}

function currentResourceDiagnostics(root) {
  const type = root.querySelector("#resourceType")?.value || "";
  const name = currentResourceDiagnosticName(root, type).toLowerCase();
  const items = Array.isArray(catalogContext.configDiagnostics?.items) ? catalogContext.configDiagnostics.items : [];
  return items.filter(item => {
    if (!isActionableResourceDiagnostic(item)) return false;
    const itemType = diagnosticResourceType(item.target_kind);
    if (!itemType || itemType !== type) return false;
    const targetName = String(item.target_name || item.name || "").trim().toLowerCase();
    return targetName ? targetName === name : Boolean(item.field);
  });
}

function currentResourceDiagnosticName(root, type) {
  if (type === "provider") return root.querySelector("#providerID")?.value || root.querySelector("#resourceName")?.value || "";
  if (type === "node-metadata") return root.querySelector("#nodeMetadataType")?.value || root.querySelector("#resourceName")?.value || "";
  if (type === "expression-helper") return root.querySelector("#expressionHelperSource")?.value || root.querySelector("#resourceName")?.value || "";
  return root.querySelector("#resourceName")?.value || "";
}

function diagnosticResourceType(kind) {
  const normalized = String(kind || "").replaceAll("-", "_").toLowerCase();
  return {
    provider: "provider",
    agent: "agent",
    skill: "skill",
    tool: "tool",
    mcp: "tool",
    mcp_server: "tool",
    mcp_tool: "tool",
    policy_rule: "policy-rule",
    workflow_schema: "workflow-schema",
    workflow_template: "workflow-template",
    team_template: "team-template",
    kit: "kit",
    expression_helper: "expression-helper",
    workflow_node_metadata: "node-metadata"
  }[normalized] || "";
}

function diagnosticResourceRecommendation(item = {}) {
  const key = `settings.diagnosticRecommendation.${item.code || ""}`;
  const translated = t(key);
  if (translated !== key) return translated;
  const raw = resourceDisplayValue(item.recommendation || item.recommended_action || "");
  return raw || resourceDisplayValue(item.message || item.code || t("catalog.configDiagnosticNoRecommendation"));
}

function isActionableResourceDiagnostic(item = {}) {
  if (!item || item.code === "config_load_ok") return false;
  if (isOptionalResourceDiagnostic(item)) return false;
  return diagnosticBooleanFlag(item.actionable) !== false;
}

function isOptionalResourceDiagnostic(item = {}) {
  const severity = String(item.severity || "info").toLowerCase();
  const code = String(item.code || "");
  const category = String(item.category || "").toLowerCase();
  const optional = diagnosticBooleanFlag(item.optional);
  const nonActionable = diagnosticBooleanFlag(item.actionable) === false;
  if (optional || category === "optional" || category === "optional_extension") return true;
  if (severity === "info" && nonActionable) return true;
  return severity === "info" && (code === "module_dir_empty" || code === "config_load_ok");
}

function diagnosticBooleanFlag(value) {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (normalized === "true") return true;
    if (normalized === "false") return false;
  }
  return undefined;
}

function renderResourceDiagnosticsPanel(root, diagnostics = []) {
  const output = root.querySelector("#resourceOutput");
  if (!output || !diagnostics.length) return;
  const html = `<div class="resource-config-diagnostics">
    <div class="resource-config-diagnostics-head">
      <strong>${escapeHTML(t("catalog.configDiagnosticsTitle"))}</strong>
      <span>${escapeHTML(t("catalog.configDiagnosticsCount", { count: diagnostics.length }))}</span>
    </div>
    <div class="resource-validation-list">
      ${diagnostics.slice(0, 6).map(item => `<div class="resource-validation-issue ${escapeHTML(diagnosticSeverityTone(item.severity))}">
        <span>${escapeHTML(resourceValidationFieldDisplay(item.field || item.target_field || item.code || "", root.querySelector("#resourceType")?.value || ""))}</span>
        <strong>${escapeHTML(diagnosticResourceRecommendation(item))}</strong>
        ${diagnosticResourceDetailsHTML(item)}
      </div>`).join("")}
    </div>
  </div>`;
  output.insertAdjacentHTML("beforeend", html);
}

function diagnosticResourceDetailsHTML(item = {}) {
  const details = item?.details || item?.detail || {};
  if (!details || typeof details !== "object" || Array.isArray(details)) return "";
  const chips = [
    details.image ? [t("catalog.isolationImage"), details.image] : null,
    details.image_reference_type ? [t("catalog.containerImageReference"), containerImageReferenceLabel(details.image_reference_type)] : null,
    typeof details.digest_pinned === "boolean" ? [t("catalog.containerDigestState"), details.digest_pinned ? t("catalog.containerDigestPinned") : t("catalog.containerDigestNotPinned")] : null,
    details.pull_policy ? [t("catalog.isolationPullPolicy"), containerPullPolicyLabel(details.pull_policy)] : null,
    details.isolation_profile ? [t("catalog.isolationProfile"), isolationProfileDisplayLabel(details.isolation_profile)] : null
  ].filter(Boolean);
  if (!chips.length) return "";
  return `<div class="resource-diagnostic-details">${chips.map(([label, value]) => `<small><span>${escapeHTML(label)}</span><strong>${escapeHTML(localizedText(value))}</strong></small>`).join("")}</div>`;
}

function diagnosticSeverityTone(severity) {
  const normalized = String(severity || "info").toLowerCase();
  if (normalized === "error") return "error";
  if (normalized === "warning" || normalized === "warn") return "warning";
  return "info";
}

function applyResourceValidationIssues(root, issues, type) {
  let firstField = null;
  for (const issue of issues) {
    const selector = resourceValidationFieldSelector(issue, type);
    if (!selector) continue;
    const field = root.querySelector(selector);
    if (!field) continue;
    const holder = field.closest("label, .resource-isolation-options, .stack, .check") || field.parentElement;
    if (!holder) continue;
    field.setAttribute("aria-invalid", "true");
    holder.classList.add("resource-field-invalid");
    const message = document.createElement("small");
    message.className = "resource-field-error";
    message.textContent = catalogValidationMessage(issue);
    holder.appendChild(message);
    if (!firstField) firstField = field;
  }
  if (firstField && typeof firstField.focus === "function") firstField.focus({ preventScroll: false });
}

function normalizeResourceValidationIssues(result = {}) {
  const raw = Array.isArray(result.issues) ? result.issues : [];
  return raw.map(issue => ({
    severity: issue.severity || issue.level || "error",
    field: issue.field || issue.ref || issue.code || "",
    ref: issue.ref || "",
    code: issue.code || "",
    stage: issue.stage || "",
    message: catalogValidationMessage(issue)
  }));
}

function catalogValidationMessage(issue = {}) {
  const code = String(issue.code || "").trim();
  const key = code ? `catalog.validationCode.${code}` : "";
  const ref = issue.ref || issue.name || issue.target_name || issue.field || "";
  const translated = key ? t(key, { ref }) : "";
  if (translated && translated !== key) return translated;
  return resourceDisplayValue(issue.message || issue.detail || issue.reason || t("catalog.validationIssueFallback"));
}

function localResourceValidationIssue(type, error) {
  const message = error?.message || t("catalog.validationIssueFallback");
  return {
    severity: "error",
    field: localResourceValidationField(type, message),
    message
  };
}

function localResourceValidationField(type, message = "") {
  const lower = String(message || "").toLowerCase();
  if (lower.includes("json")) {
    if (type === "team-template") return "quorum_presets";
    if (type === "kit") return "examples";
    if (type === "workflow-template") return "graph";
    if (type === "workflow-schema") return "schema";
    if (type === "node-metadata") return lower.includes("output") ? "outputs" : lower.includes("example") ? "examples" : lower.includes("default") ? "default_stage" : "fields";
    if (type === "expression-helper") return "args";
  }
  return "body";
}

function resourceValidationFieldSelector(issue, type) {
  const field = normalizeValidationField(issue?.field || "");
  const ref = normalizeValidationField(issue?.ref || "");
  const code = String(issue?.code || "").toLowerCase();
  if (field === "body") return "";
  const map = resourceValidationFieldMap(type);
  if (map[field]) return `#${map[field]}`;
  const parent = field.split(".")[0];
  if (map[parent]) return `#${map[parent]}`;
  if (type === "kit") {
    const kitField = kitValidationFieldFromIssue(ref, code);
    if (kitField) return `#${kitField}`;
  }
  return "";
}

function normalizeValidationField(field) {
  return String(field || "")
    .trim()
    .toLowerCase()
    .replaceAll("-", "_");
}

function kitValidationFieldFromIssue(ref, code) {
  if (code.includes("provider") || ref.includes("provider")) return "kitProviders";
  if (code.includes("agent") || ref.includes("agent")) return "kitAgents";
  if (code.includes("skill") || ref.includes("skill")) return "kitSkills";
  if (code.includes("tool") || ref.includes("tool")) return "kitTools";
  if (code.includes("workflow_template")) return "kitWorkflowTemplates";
  if (code.includes("workflow")) return "kitWorkflows";
  if (code.includes("team")) return "kitTeamTemplates";
  if (code.includes("policy")) return "kitPolicyRules";
  if (code.includes("env")) return "kitRequiredEnv";
  return "";
}

function resourceValidationFieldMap(type) {
  const common = {
    name: "resourceName",
    id: "resourceName",
    description: "resourceDescription",
    kind: "resourceName",
    version: "resourceName"
  };
  const maps = {
    skill: {
      ...common,
      version: "skillVersion",
      author: "skillAuthor",
      mode: "skillMode",
      preferred_agent: "skillAgent",
      agent: "skillAgent",
      allowed_tool_kinds: "skillAllowedKinds",
      output_kind: "skillOutputKind",
      priority: "skillPriority",
      max_iterations: "skillMaxIterations",
      next_skills: "skillNext",
      activation: "skillKeywords",
      keywords: "skillKeywords",
      tools: "skillTools",
      scripts: "skillScriptsList",
      "scripts.args_schema": "skillScriptsList",
      args_schema: "skillScriptsList",
      params: "skillParams",
      metadata: "skillMetadata",
      instructions: "skillInstructions"
    },
    agent: {
      ...common,
      provider: "agentProvider",
      model: "agentModel",
      mode: "agentMode",
      tool_policy: "agentToolPolicy",
      max_iterations: "agentMaxIterations",
      allowed_tool_kinds: "agentAllowedKinds",
      allowed_tools: "agentAllowedTools",
      system_prompt: "agentSystemPrompt"
    },
    provider: {
      ...common,
      provider: "providerID",
      name: "providerID",
      id: "providerID",
      api_key: "providerAPIKey",
      type: "providerType",
      default_model: "providerDefaultModel",
      model: "providerDefaultModel",
      fallback_provider: "providerID",
      base_url: "providerBaseURL",
      env_key: "providerAPIKey",
      models: "providerModels",
      metadata: "providerMetadata"
    },
    tool: {
      ...common,
      command: "toolCommand",
      args: "toolArgs",
      timeout: "toolTimeout",
      workdir: "toolWorkdir",
      isolation: "toolIsolation",
      isolation_profile: "toolIsolationProfile",
      isolation_options: "toolIsolationOptionsRaw",
      "isolation_options.image": "toolIsolationImage",
      "isolation_options.runtime": "toolIsolationRuntime",
      "isolation_options.pull_policy": "toolIsolationPullPolicy",
      "isolation_options.workspace_mount": "toolIsolationWorkspaceMount",
      "isolation_options.workspace_target": "toolIsolationWorkspaceTarget",
      "isolation_options.container_workdir": "toolIsolationContainerWorkdir",
      "isolation_options.network": "toolIsolationNetwork",
      "isolation_options.memory": "toolIsolationMemory",
      "isolation_options.memory_swap": "toolIsolationMemorySwap",
      "isolation_options.cpus": "toolIsolationCpus",
      "isolation_options.pids_limit": "toolIsolationPidsLimit",
      "isolation_options.readonly_rootfs": "toolIsolationReadonlyRootfs",
      "isolation_options.no_new_privileges": "toolIsolationNoNewPrivileges",
      "isolation_options.cap_drop": "toolIsolationCapDrop",
      "isolation_options.user": "toolIsolationUser",
      "isolation_options.userns": "toolIsolationUserns",
      "isolation_options.tmpfs": "toolIsolationTmpfs",
      "isolation_options.init": "toolIsolationInit",
      "isolation_options.tool_source": "toolIsolationToolSource",
      "isolation_options.tool_target": "toolIsolationToolTarget",
      "isolation_options.tool_mount": "toolIsolationToolMount",
      code: "toolCode",
      config: "toolArgs",
      env_allowlist: "toolEnvAllowlist",
      network_disabled: "toolNetworkDisabled",
      allowed_commands: "toolAllowedCommands",
      allowed_command_paths: "toolAllowedCommandPaths",
      max_request_bytes: "toolMaxRequestBytes",
      max_response_bytes: "toolMaxResponseBytes"
    },
    "team-template": {
      ...common,
      title: "teamTitle",
      category: "teamCategory",
      tags: "teamTags",
      role_templates: "teamRoles",
      roles: "teamRoles",
      handoffs: "teamHandoffs",
      blackboard_templates: "teamBlackboard",
      blackboard: "teamBlackboard",
      quorum_presets: "teamQuorumPresets",
      output_contract: "teamOutputContract"
    },
    kit: {
      ...common,
      title: "kitTitle",
      category: "kitCategory",
      tags: "kitTags",
      agents: "kitAgents",
      providers: "kitProviders",
      skills: "kitSkills",
      tools: "kitTools",
      workflows: "kitWorkflows",
      workflow_templates: "kitWorkflowTemplates",
      team_templates: "kitTeamTemplates",
      policy_rules: "kitPolicyRules",
      required_env: "kitRequiredEnv",
      examples: "kitExamples",
      metadata: "kitMetadata"
    },
    "workflow-template": {
      ...common,
      title: "workflowTemplateTitle",
      category: "workflowTemplateCategory",
      tags: "workflowTemplateTags",
      graph: "workflowTemplateGraph",
      stages: "workflowTemplateGraph"
    },
    "workflow-schema": {
      ...common,
      schema: "workflowSchemaJSON",
      workflow: "workflowSchemaJSON",
      "schema.workflow": "workflowSchemaJSON",
      "schema.stages": "workflowSchemaJSON",
      "schema.stages.outputs": "workflowSchemaJSON",
      stages: "workflowSchemaJSON",
      outputs: "workflowSchemaJSON"
    },
    "node-metadata": {
      ...common,
      type: "nodeMetadataType",
      label: "nodeMetadataLabel",
      category: "nodeMetadataCategory",
      tags: "nodeMetadataTags",
      fields: "nodeMetadataFields",
      outputs: "nodeMetadataOutputs",
      examples: "nodeMetadataExamples",
      default_stage: "nodeMetadataDefaultStage"
    },
    "expression-helper": {
      ...common,
      name: "resourceName",
      label: "expressionHelperLabel",
      signature: "expressionHelperSignature",
      insert_text: "expressionHelperInsertText",
      return_type: "expressionHelperReturnType",
      min_args: "expressionHelperMinArgs",
      max_args: "expressionHelperMaxArgs",
      modes: "expressionHelperModes",
      node_types: "expressionHelperNodeTypes",
      args: "expressionHelperArgs",
      examples: "expressionHelperExamples"
    },
    "policy-rule": {
      ...common,
      label: "policyLabel",
      operator: "policyOperator",
      expression: "policyExpression",
      reason: "policyReason",
      defaults: "policyDefaults",
      params: "policyParams"
    },
    workflow: {
      ...common,
      graph: "workflowTemplate",
      stages: "workflowTemplate"
    }
  };
  return maps[type] || common;
}

function resourceValidationIssueLabel(issue, type) {
  const field = issue.field || issue.ref || issue.code || t("catalog.validationIssueFallback");
  const stage = issue.stage ? `${issue.stage} / ` : "";
  return `${stage}${resourceValidationFieldDisplay(field, type)}`;
}

function resourceValidationFieldDisplay(field, type = "") {
  const raw = String(field || "").trim();
  const normalized = normalizeValidationField(raw);
  const labels = {
    name: t("catalog.name"),
    id: "ID",
    title: t("catalog.title"),
    description: t("catalog.description"),
    kind: t("catalog.resourceType"),
    type: t("catalog.resourceType"),
    version: t("catalog.version"),
    category: t("catalog.teamCategory"),
    tags: t("catalog.teamTags"),
    mode: t("catalog.mode"),
    provider: t("catalog.provider"),
    model: t("catalog.model"),
    api_key: t("catalog.providerAPIKey"),
    env_key: t("catalog.providerAPIKey"),
    fallback_provider: t("settings.field.fallbackProvider"),
    default_model: t("catalog.providerDefaultModel"),
    base_url: t("catalog.providerBaseURL"),
    allowed_tool_kinds: t("settings.field.allowedToolKinds"),
    allowed_tools: t("settings.field.allowedTools"),
    tool_policy: t("settings.field.toolPolicy"),
    system_prompt: t("settings.field.systemPrompt"),
    max_iterations: t("settings.field.maxIterations"),
    command: t("settings.field.command"),
    args: t("settings.field.args"),
    timeout: t("settings.field.timeout"),
    workdir: t("settings.field.workdir"),
    env_allowlist: t("settings.field.envAllowlist"),
    allowed_commands: t("settings.field.allowedCommands"),
    allowed_command_paths: t("settings.field.allowedCommandPaths"),
    network_disabled: t("settings.field.networkDisabled"),
    isolation: t("catalog.toolIsolation"),
    isolation_profile: t("settings.field.isolationProfile"),
    isolation_options: t("settings.field.isolationOptions"),
    "isolation_options.image": t("settings.field.containerImage"),
    "isolation_options.runtime": t("settings.field.containerRuntime"),
    "isolation_options.pull_policy": t("settings.field.containerPullPolicy"),
    "isolation_options.workspace_mount": t("settings.field.workspaceMount"),
    "isolation_options.workspace_target": t("settings.field.workspaceTarget"),
    "isolation_options.container_workdir": t("settings.field.containerWorkdir"),
    "isolation_options.network": t("settings.field.containerNetwork"),
    "isolation_options.memory": t("settings.field.containerMemory"),
    "isolation_options.memory_swap": t("settings.field.containerMemorySwap"),
    "isolation_options.cpus": t("settings.field.containerCpus"),
    "isolation_options.pids_limit": t("settings.field.containerPids"),
    "isolation_options.readonly_rootfs": t("settings.field.readonlyRootfs"),
    "isolation_options.no_new_privileges": t("settings.field.noNewPrivileges"),
    "isolation_options.cap_drop": t("settings.field.capDrop"),
    "isolation_options.user": t("settings.field.containerUser"),
    "isolation_options.userns": t("settings.field.userNamespace"),
    "isolation_options.tmpfs": t("settings.field.containerTmpfs"),
    "isolation_options.init": t("settings.field.containerInit"),
    "isolation_options.tool_source": t("settings.field.containerToolSource"),
    "isolation_options.tool_target": t("settings.field.containerToolTarget"),
    "isolation_options.tool_mount": t("settings.field.containerToolMount"),
    roles: t("catalog.teamRolesShort"),
    role_templates: t("catalog.teamRolesShort"),
    handoffs: t("catalog.teamHandoffs"),
    quorum_presets: t("catalog.teamQuorumPresets"),
    output_contract: t("workflow.outputsMap"),
    agents: t("catalog.kitAgents"),
    providers: t("catalog.kitProviders"),
    skills: t("catalog.kitSkills"),
    tools: t("catalog.kitTools"),
    workflows: t("catalog.kitWorkflows"),
    workflow_templates: t("catalog.kitWorkflowTemplates"),
    team_templates: t("catalog.kitTeamTemplates"),
    policy_rules: t("catalog.kitPolicyRules"),
    required_env: t("catalog.kitRequiredEnv"),
    graph: t("catalog.templateGraphJSON"),
    schema: t("catalog.schemaJSON"),
    fields: t("catalog.nodeFieldsJSON"),
    outputs: t("catalog.nodeOutputsJSON"),
    examples: t("catalog.nodeExamplesJSON"),
    default_stage: t("catalog.nodeDefaultStageJSON"),
    signature: t("catalog.expressionSignature"),
    insert_text: t("catalog.expressionInsertText"),
    return_type: t("catalog.expressionReturnType"),
    min_args: t("catalog.expressionMinArgs"),
    max_args: t("catalog.expressionMaxArgs"),
    node_types: t("catalog.expressionNodeTypes"),
    operator: t("catalog.policyOperator"),
    expression: t("catalog.policyExpression"),
    reason: t("catalog.policyReason"),
    defaults: t("catalog.policyDefaults"),
    params: t("catalog.policyParams")
  };
  if (labels[normalized]) return labels[normalized];
  if (normalized.includes(".")) {
    return normalized.split(".").map(part => labels[part] || localizedText(part)).join(" / ");
  }
  return localizedText(raw.replaceAll("_", " ") || type || t("catalog.validationIssueFallback"));
}

function resourceValidationSummary(result, type) {
  const issues = normalizeResourceValidationIssues(result);
  const valid = result?.valid !== false;
  if (valid) return `${t("catalog.validationPassed")}\n${t("catalog.validationNoWrite")}`;
  return [
    t("catalog.validationNeedsWork"),
    ...issues.slice(0, 8).map(issue => `- ${resourceValidationIssueLabel(issue, type)}: ${catalogValidationMessage(issue)}`)
  ].join("\n");
}

function resourceTypeLabel(type) {
  return {
    skill: t("catalog.resourceSkill"),
    agent: t("catalog.resourceAgent"),
    tool: t("catalog.resourceTool"),
    "team-template": t("catalog.resourceTeamTemplate"),
    kit: t("catalog.resourceKit"),
    "workflow-template": t("catalog.resourceWorkflowTemplate"),
    "node-metadata": t("catalog.resourceNodeMetadata"),
    "expression-helper": t("catalog.resourceExpressionHelper"),
    provider: t("catalog.resourceProvider"),
    workflow: t("catalog.resourceWorkflow"),
    "workflow-schema": t("catalog.resourceWorkflowSchema"),
    "policy-rule": t("catalog.resourcePolicyRule")
  }[type] || localizedText(type || t("catalog.resourceBuilder"));
}

function renderResourceCapabilitySummary(root, type) {
  const node = root.querySelector("#resourceCapabilitySummary");
  if (!node) return;
  const previousType = node.dataset.resourceCapabilityType || "";
  const currentType = String(type || "");
  const previousDetails = node.querySelector(".resource-capability-action-details");
  const previousDetailsBody = previousDetails?.querySelector(":scope > div");
  const preserveDetails = previousType === currentType;
  const detailsOpen = preserveDetails && Boolean(previousDetails?.open);
  const detailsScrollTop = preserveDetails ? previousDetailsBody?.scrollTop || 0 : 0;
  node.dataset.resourceCapabilityType = currentType;
  const capability = resourceCapability(catalogContext.resourceCapabilities, type);
  const status = resourceCapabilitySaveStatus(capability);
  const flags = resourceCapabilityFlags(capability);
  const facts = resourceCapabilityFacts(capability);
  const actions = Array.isArray(capability?.actions) ? capability.actions : [];
  const visibleActions = actions.slice(0, 4);
  const extraActions = Math.max(0, actions.length - visibleActions.length);
  const currentName = currentCatalogResourceName(root, type);
  node.innerHTML = `
    <div class="resource-capability-main">
      <div class="resource-capability-status ${escapeHTML(status.tone)}">
        <span class="resource-capability-dot" aria-hidden="true"></span>
        <div>
          <strong>${escapeHTML(resourceTypeLabel(type))} / ${escapeHTML(status.title)}</strong>
          <small>${escapeHTML(status.help)}</small>
        </div>
      </div>
      <div class="resource-capability-chips" aria-label="${escapeHTML(t("catalog.capabilityFacts"))}">
        ${flags.map(flag => `<span class="resource-capability-chip ${escapeHTML(flag.tone || "")}">${escapeHTML(flag.label)}</span>`).join("")}
      </div>
    </div>
    ${facts.length ? `<div class="resource-capability-facts">${facts.map(fact => `
      <span>
        <small>${escapeHTML(fact.label)}</small>
        <strong>${escapeHTML(fact.value)}</strong>
      </span>
    `).join("")}</div>` : ""}
    <div class="resource-capability-actions">
      <small>${escapeHTML(t("catalog.capabilityActions"))}</small>
      <div class="resource-capability-action-buttons">
        ${visibleActions.length ? visibleActions.map(action => {
          const description = resourceActionDescription(action, capability?.kind || type);
          const actionLabel = resourceActionLabel(action, "", capability?.kind || type);
          const disabled = (action?.requires_saved_resource || resourceActionNeedsName(action)) && !String(currentName || "").trim();
          return `<button type="button" class="resource-capability-action-button ${escapeHTML(action?.destructive ? "danger" : "")}" data-resource-capability-action="${escapeHTML(action?.name || "")}" data-resource-capability-kind="${escapeHTML(capability?.kind || type)}" data-resource-capability-resource="${escapeHTML(currentName || "")}" data-resource-capability-method="${escapeHTML(resourceActionMethod(action))}" data-resource-capability-path="${escapeHTML(resourceActionPath(action, currentName || "", { format: "json" }))}" aria-disabled="${disabled ? "true" : "false"}" ${disabled ? "disabled" : ""} ${description ? `title="${escapeHTML(description)}"` : ""}>${escapeHTML(actionLabel)}</button>`;
        }).join("") : `<span>${escapeHTML(t("catalog.capabilityNoActions"))}</span>`}
        ${extraActions ? `<span>${escapeHTML(t("catalog.capabilityMoreActions", { count: extraActions }))}</span>` : ""}
      </div>
    </div>
    ${actions.length ? renderResourceCapabilityActionDetails(actions, capability?.kind || type) : ""}
  `;
  const nextDetails = node.querySelector(".resource-capability-action-details");
  if (nextDetails && detailsOpen) {
    nextDetails.open = true;
    const nextBody = nextDetails.querySelector(":scope > div");
    if (nextBody) {
      nextBody.scrollTop = Math.min(detailsScrollTop, Math.max(0, nextBody.scrollHeight - nextBody.clientHeight));
    }
  }
}

function renderResourceCapabilityActionDetails(actions, kind) {
  return `<details class="resource-capability-action-details">
    <summary>${escapeHTML(t("catalog.capabilityActionDetails"))}</summary>
    <div>
      ${actions.map(action => {
        const label = resourceActionLabel(action, "", kind);
        const description = resourceActionDescription(action, kind) || t("catalog.capabilityActionNoDescription");
        const returnsLabel = resourceActionReturnLabel(action?.returns);
        return `<section>
          <strong>${escapeHTML(label)}</strong>
          <p>${escapeHTML(description)}</p>
          <div class="resource-capability-action-meta">
            <span>${escapeHTML(t("catalog.capabilityActionMethod", { method: resourceActionMethod(action) }))}</span>
            <span>${escapeHTML(action?.requires_saved_resource ? t("catalog.capabilityActionRequiresSaved") : t("catalog.capabilityActionCanRunNow"))}</span>
            ${returnsLabel ? `<span>${escapeHTML(t("catalog.capabilityActionReturns", { value: returnsLabel }))}</span>` : ""}
          </div>
        </section>`;
      }).join("")}
    </div>
  </details>`;
}

function resourceCapabilitySaveStatus(capability) {
  if (!capability) {
    return {
      tone: "neutral",
      title: t("catalog.capabilitySaveUnknown"),
      help: t("catalog.capabilitySummaryHelp")
    };
  }
  if (capability.restart_required_on_save || capability.apply_state_on_save === "restart_required") {
    return {
      tone: "warn",
      title: t("catalog.capabilitySaveRestart"),
      help: t("catalog.capabilitySaveRestartHelp")
    };
  }
  if (capability.apply_state_on_save === "hot_reload_when_supported" || capability.hot_reload_supported) {
    return {
      tone: "good",
      title: t("catalog.capabilitySaveHotReload"),
      help: t("catalog.capabilitySaveHotReloadHelp")
    };
  }
  if (capability.apply_state_on_save === "active_metadata") {
    return {
      tone: "good",
      title: t("catalog.capabilitySaveMetadata"),
      help: t("catalog.capabilitySaveMetadataHelp")
    };
  }
  if (capability.apply_state_on_save === "active") {
    return {
      tone: "good",
      title: t("catalog.capabilitySaveActive"),
      help: t("catalog.capabilitySaveActiveHelp")
    };
  }
  return {
    tone: "neutral",
    title: t("catalog.capabilitySaveUnknown"),
    help: t("catalog.capabilitySummaryHelp")
  };
}

function resourceCapabilityFlags(capability) {
  if (!capability) return [{ label: t("catalog.capabilityContractMissing"), tone: "neutral" }];
  const flags = [];
  if (capability.file_backed) flags.push({ label: t("catalog.capabilityFileBacked"), tone: "neutral" });
  if (capability.can_validate) flags.push({ label: t("catalog.capabilityCanValidate"), tone: "good" });
  if (capability.can_scaffold) flags.push({ label: t("catalog.capabilityCanScaffold"), tone: "good" });
  if (capability.can_delete) flags.push({ label: t("catalog.capabilityCanDelete"), tone: "warn" });
  if (!flags.length) flags.push({ label: t("catalog.capabilityReadOnly"), tone: "neutral" });
  return flags;
}

function resourceCapabilityFacts(capability) {
  if (!capability) return [];
  return [
    capability.storage_root ? { label: t("catalog.capabilityStorageRoot"), value: capability.storage_root } : null,
    capability.diagnostics_path ? { label: t("catalog.capabilityDiagnostics"), value: capability.diagnostics_path } : null
  ].filter(Boolean);
}

function renderKitValidation(root, validation) {
  const node = root.querySelector("#kitValidationSummary");
  if (!node) return;
  if (!validation) {
    node.classList.add("hidden");
    node.innerHTML = "";
    return;
  }
  const issues = Array.isArray(validation.issues) ? validation.issues : [];
  const normalizedIssues = issues.map(issue => ({ ...issue, message: catalogValidationMessage(issue) }));
  node.className = `span-12 item resource-validation ${validation.valid === false ? "error" : "ok"}`;
  node.classList.remove("hidden");
  node.innerHTML = `
    <strong>${escapeHTML(kitValidationText(validation))}</strong>
    ${normalizedIssues.length ? `<div class="resource-facts">${normalizedIssues.slice(0, 8).map(issue => `<span title="${escapeHTML(issue.message || "")}">${escapeHTML(`${catalogValidationSeverityLabel(issue.severity || "info")}: ${resourceValidationIssueLabel(issue, "kit")}`)}</span>`).join("")}</div>` : `<p>${escapeHTML(t("catalog.kitValidationPassed"))}</p>`}
  `;
}

function resourceActionReturnLabel(value = "") {
  const tokens = resourceActionReturnTokens(value);
  if (!tokens.length) return "";
  const labels = {
    graph: t("catalog.capabilityReturnGraph"),
    workflow_graph: t("catalog.capabilityReturnWorkflowGraph"),
    workflow_template: t("catalog.resourceWorkflowTemplate"),
    workflow_schema: t("catalog.resourceWorkflowSchema"),
    schema: t("catalog.capabilityReturnSchema"),
    catalog: t("catalog.capabilityReturnCatalog"),
    bundle: t("catalog.capabilityReturnBundle"),
    kit_bundle: t("catalog.capabilityReturnKitBundle"),
    json: "JSON",
    yaml: "YAML",
    normalized: t("catalog.capabilityReturnNormalized"),
    validation: t("catalog.kitValidationResult"),
    resource: t("catalog.resourceType")
  };
  return tokens.map(token => {
    const raw = String(token || "").trim();
    const normalized = raw.replaceAll("-", "_").toLowerCase();
    if (labels[normalized]) return labels[normalized];
    const parts = normalized.split("_").filter(Boolean);
    if (parts.length > 1) return parts.map(part => labels[part] || localizedText(part)).join(" ");
    return localizedText(raw.replaceAll("_", " "));
  }).filter(Boolean).join(" / ");
}

function resourceActionReturnTokens(value) {
  if (value === null || value === undefined || value === "") return [];
  if (Array.isArray(value)) return value.flatMap(item => resourceActionReturnTokens(item)).filter(Boolean);
  if (typeof value === "object") {
    const preferred = [
      value.type,
      value.kind,
      value.format,
      value.content_type,
      value.contentType,
      value.schema,
      value.name,
      value.title
    ].flatMap(item => resourceActionReturnTokens(item));
    if (preferred.length) return preferred;
    const keys = Object.keys(value).filter(Boolean);
    return keys.length ? [t("catalog.capabilityReturnStructured"), ...keys.slice(0, 3)] : [t("catalog.capabilityReturnStructured")];
  }
  const text = String(value || "").trim();
  return text ? [text] : [];
}

function resourceDisplayValue(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const translated = localizedText(text);
  if (translated !== text) return translated;
  if (resourceLooksTechnical(text)) return text;
  return translated;
}

function resourceLooksTechnical(value) {
  const text = String(value || "").trim();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("[") || text.startsWith("@") || text.startsWith("$")) return true;
  if (text.includes("\\") || text.includes("://")) return true;
  if (text.includes("/") && !/\s/.test(text)) return true;
  if (text.includes("=") && !/\s/.test(text)) return true;
  if (/^[-\w.]+$/.test(text) && /[._-]/.test(text)) return true;
  if (/^(go|git|npm|pnpm|yarn|python|node|cargo|deno|bun)\s+/i.test(text)) return true;
  return false;
}

function catalogValidationSeverityLabel(value) {
  const key = `catalog.validationSeverity.${String(value || "info").toLowerCase()}`;
  const translated = t(key);
  return translated === key ? localizedText(value || "info") : translated;
}

function kitValidationText(validation) {
  if (!validation) return t("catalog.kitValidationUnknown");
  const issues = Array.isArray(validation.issues) ? validation.issues : [];
  if (validation.valid === false) return t("catalog.kitValidationIssues", { count: issues.length });
  if (issues.length) return t("catalog.kitValidationWarnings", { count: issues.length });
  return t("catalog.kitValidationPassed");
}

function normalizeProviderOptions(list) {
  return (Array.isArray(list) ? list : [])
    .map(item => {
      if (typeof item === "string") {
        return { id: item, label: item, defaultModel: "" };
      }
      const id = item?.id || item?.name || item?.provider || item?.key || "";
      if (!id) return null;
      const defaultModel = item?.default_model || item?.model || item?.defaultModel || "";
      return {
        id,
        label: item?.label || item?.title || id,
        defaultModel,
        type: item?.type || item?.kind || item?.driver || "",
        description: item?.description || ""
      };
    })
    .filter(Boolean);
}

function normalizeNodeTypeOptions(list) {
  const out = (Array.isArray(list) ? list : [])
    .map(item => typeof item === "string" ? { type: item, label: nodeDisplayLabel(item) } : item)
    .filter(item => item?.type);
  if (out.length) return out;
  return ["agent", "skill", "tool", "team", "condition", "switch", "policy_guard", "input_gate", "for_each", "loop", "sub_workflow"].map(type => ({ type, label: nodeDisplayLabel(type) }));
}

function normalizeExpressionHelperOptions(list) {
  const out = (Array.isArray(list) ? list : [])
    .map(item => typeof item === "string" ? { name: item, label: expressionDisplayLabel(item) } : item)
    .filter(item => item?.name);
  if (out.length) return out;
  return ["contains", "matches", "risk_rank"].map(name => ({ name, label: expressionDisplayLabel(name) }));
}

function collectWorkflowForm(root) {
  const name = normalizeName(root.querySelector("#resourceName").value.trim() || "custom-workflow");
  const description = root.querySelector("#resourceDescription").value.trim() || t("catalog.customWorkflowDescription");
  const approval = root.querySelector("#workflowApproval").value;
  const template = root.querySelector("#workflowTemplate").value;
  if (template === "blank") {
    return { name, description, stages: [{ name: "start", node_type: "start", next: ["end"], position: { x: 80, y: 180 } }, { name: "end", node_type: "end", position: { x: 380, y: 180 } }] };
  }
  if (template === "research-audit") {
    return {
      name,
      description,
      stages: [
        { name: "start", node_type: "start", next: ["research"], position: { x: 80, y: 180 } },
        { name: "research", node_type: "agent", agent: "planner", skill: "execution-plan", next_strategy: "select", next: ["audit"], position: { x: 330, y: 160 } },
        { name: "audit", node_type: "skill", agent: "auditor", skill: "code-audit", approval: approval === "audit", next: ["end"], position: { x: 620, y: 160 } },
      { name: "end", node_type: "end", position: { x: 910, y: 180 } }
      ]
    };
  }
  if (template && template !== "plan-fix-audit") {
    return { name, description, template };
  }
  return {
    name,
    description,
    stages: [
      { name: "start", node_type: "start", next: ["plan"], position: { x: 80, y: 180 } },
      { name: "plan", node_type: "agent", agent: "planner", skill: "execution-plan", next: ["implement"], position: { x: 330, y: 160 } },
      { name: "implement", node_type: "agent", agent: "fixer", skill: "code-writing", approval: approval === "implement", next: ["audit"], position: { x: 620, y: 160 } },
      { name: "audit", node_type: "skill", agent: "auditor", skill: "code-audit", approval: approval === "audit", next: ["end"], position: { x: 910, y: 160 } },
      { name: "end", node_type: "end", position: { x: 1200, y: 180 } }
    ]
  };
}

async function workflowGraphFromTemplate(doc) {
  const template = await requestCatalogWorkflowTemplateDetail(doc.template);
  const graph = template?.graph && typeof template.graph === "object" ? structuredCloneSafe(template.graph) : null;
  if (!graph || !Array.isArray(graph.stages)) throw new Error(t("catalog.templateGraphInvalid"));
  graph.name = doc.name;
  graph.description = doc.description || graph.description || template.description || "";
  return graph;
}

function collectResourceDraft(root) {
  const brief = resourceBrief(root);
  return {
    type: root.querySelector("#resourceType").value,
    name: root.querySelector("#resourceName").value.trim(),
    purpose: root.querySelector("#resourcePurpose").value.trim(),
    description: root.querySelector("#resourceDescription").value.trim(),
    details: root.querySelector("#resourceDetails").value.trim(),
    brief,
    provider: root.querySelector("#resourceType").value === "provider" ? root.querySelector("#providerID").value.trim() : "",
    workflowTemplate: root.querySelector("#resourceType").value === "workflow" ? root.querySelector("#workflowTemplate").value : ""
  };
}

function buildResourceRequest({ type, name, purpose, description, details, brief = {}, provider, workflowTemplate }) {
  const label = type === "tool" ? t("catalog.resourceTool") : type === "agent" ? t("catalog.resourceAgent") : type === "provider" ? t("catalog.resourceProvider") : type === "workflow" ? t("catalog.resourceWorkflow") : type === "policy-rule" ? t("catalog.resourcePolicyRule") : t("catalog.resourceSkill");
  return [
    `${t("catalog.resourceDraftPrefix")} ${label} ${t("catalog.resourceDraftNamed")} ${name || "<name>"} ${t("catalog.resourceDraftInProject")}`,
    description ? `${t("catalog.description")}: ${description}` : "",
    purpose ? `${t("catalog.resourceDraftPurpose")} ${purpose}` : "",
    brief.goal ? `${t("catalog.briefGoal")}: ${brief.goal}` : "",
    brief.inputs ? `${t("catalog.briefInputs")}:\n${brief.inputs}` : "",
    brief.outputs ? `${t("catalog.briefOutputs")}:\n${brief.outputs}` : "",
    brief.example ? `${t("catalog.briefExample")}:\n${brief.example}` : "",
    brief.dependencies ? `${t("catalog.briefDependencies")}:\n${brief.dependencies}` : "",
    provider ? `${t("common.provider")}: ${provider}` : "",
    workflowTemplate ? `${t("catalog.workflowTemplate")}: ${workflowTemplate}` : "",
    details ? `${t("catalog.resourceDraftRequirements")}\n${details}` : "",
    t("catalog.resourceDraftConventions"),
    t("catalog.resourceDraftSummary")
  ].filter(Boolean).join("\n\n");
}

function loadToolCodeFile(root, event) {
  const file = event.target.files?.[0];
  if (!file) return;
  const reader = new FileReader();
  reader.onload = () => {
    root.querySelector("#toolCode").value = String(reader.result || "");
    if (!root.querySelector("#resourceName").value.trim()) {
      root.querySelector("#resourceName").value = normalizeName(file.name.replace(/\.[^.]+$/, ""));
    }
  };
  reader.readAsText(file, "utf-8");
}

function defaultToolCode(name) {
  const server = normalizeName(name).replaceAll("-", "_");
  const pingDescription = JSON.stringify(t("catalog.defaultToolPingDescription"));
  const unknownTool = JSON.stringify(t("catalog.defaultToolUnknownTool"));
  const unknownMethod = JSON.stringify(t("catalog.defaultToolUnknownMethod"));
  return `#!/usr/bin/env python3
import json
import sys

SERVER_NAME = "${server}"

def respond(request_id, result=None, error=None):
    payload = {"jsonrpc": "2.0", "id": request_id}
    if error is not None:
        payload["error"] = {"code": -32000, "message": str(error)}
    else:
        payload["result"] = result
    print(json.dumps(payload), flush=True)

def list_tools():
    return {"tools": [{"name": "ping", "description": ${pingDescription}, "kind": "read", "input_schema": {"type": "object", "properties": {}, "additionalProperties": False}}]}

def call_tool(params):
    if params.get("name") == "ping":
        return {"content": json.dumps({"server": SERVER_NAME, "status": "ok"})}
    raise ValueError(${unknownTool})

def handle(request):
    method = request.get("method")
    if method == "initialize":
        return {"server": SERVER_NAME, "capabilities": {"tools": True}}
    if method == "tools/list":
        return list_tools()
    if method == "tools/call":
        return call_tool(request.get("params") or {})
    raise ValueError(${unknownMethod})

for line in sys.stdin:
    if not line.strip():
        continue
    try:
        request = json.loads(line)
        respond(request.get("id"), handle(request))
    except Exception as exc:
        respond(request.get("id") if "request" in locals() else None, error=exc)
`;
}

function parseParams(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [name, type, description, required] = line.split("|").map(part => part.trim());
      return { name, type: type || "string", description: description || "", required: required === "true" || required === "required" };
    })
    .filter(param => param.name);
}

function collectSkillScripts(root, options = {}) {
  const validate = options.validate !== false;
  if (validate) clearSkillScriptValidation(root);
  const errors = [];
  const scripts = [];
  [...root.querySelectorAll("[data-skill-script-card]")].forEach((card, index) => {
    const script = collectSkillScriptCard(card, index, validate ? errors : null);
    if (script) scripts.push(script);
  });
  if (validate && errors.length) {
    const first = errors[0];
    for (const error of errors) markSkillScriptFieldError(error.field, error.message);
    first.field?.focus();
    throw new Error(first.message);
  }
  return scripts;
}

function collectSkillScriptCard(card, index, errors = null) {
  const values = {};
  card.querySelectorAll("[data-skill-script-field]").forEach(field => {
    values[field.dataset.skillScriptField] = field.value?.trim() || "";
  });
  const hasContent = Object.values(values).some(Boolean);
  if (!hasContent) return null;
  const label = `${t("catalog.skillScript")} ${index + 1}`;
  const nameField = skillScriptField(card, "name");
  const pathField = skillScriptField(card, "path");
  const argsField = skillScriptField(card, "args_schema");
  if (errors && !values.name) errors.push({ field: nameField, message: `${label}: ${t("catalog.skillScriptNameRequired")}` });
  if (errors && !values.path) {
    errors.push({ field: pathField, message: `${label}: ${t("catalog.skillScriptPathRequired")}` });
  } else if (errors && !isValidSkillScriptPath(values.path)) {
    errors.push({ field: pathField, message: `${label}: ${t("catalog.skillScriptPathInvalid")}` });
  }
  const script = {
    name: values.name,
    description: values.description,
    path: values.path,
    runtime: values.runtime,
    output: values.output,
    timeout: values.timeout,
    isolation: values.isolation,
    workspace_mount: values.workspace_mount,
    network: values.network,
    approval: values.approval
  };
  if (values.args_schema) {
    try {
      script.args_schema = parseOptionalJSONObject(values.args_schema, `${label} ${t("catalog.skillScriptArgsSchema")}`);
    } catch (error) {
      if (errors) errors.push({ field: argsField, message: localizedCatalogErrorMessage(error, t("catalog.invalidJSONObject")) });
      else throw error;
    }
  }
  const metadata = parseMap(values.metadata);
  if (Object.keys(metadata).length) script.metadata = metadata;
  return cleanEmptyFields(script);
}

function skillScriptField(card, name) {
  return card?.querySelector(`[data-skill-script-field='${name}']`) || null;
}

function isValidSkillScriptPath(value) {
  const normalized = String(value || "").trim().replaceAll("\\", "/");
  if (!normalized || normalized.startsWith("/") || /^[A-Za-z]:\//.test(normalized)) return false;
  if (!normalized.startsWith("scripts/")) return false;
  return !normalized.split("/").some(part => part === "..");
}

function clearSkillScriptValidation(root) {
  root.querySelectorAll(".resource-script-field-invalid").forEach(node => node.classList.remove("resource-script-field-invalid"));
  root.querySelectorAll(".resource-script-field-error").forEach(node => node.remove());
  root.querySelectorAll("#skillScriptsList [aria-invalid='true']").forEach(node => node.removeAttribute("aria-invalid"));
}

function clearSkillScriptFieldError(field) {
  const holder = field?.closest("label");
  holder?.classList.remove("resource-script-field-invalid");
  holder?.querySelector(".resource-script-field-error")?.remove();
  field?.removeAttribute("aria-invalid");
}

function markSkillScriptFieldError(field, message) {
  if (!field) return;
  const holder = field.closest("label");
  if (!holder) return;
  holder.classList.add("resource-script-field-invalid");
  field.setAttribute("aria-invalid", "true");
  holder.querySelector(".resource-script-field-error")?.remove();
  const node = document.createElement("small");
  node.className = "resource-script-field-error";
  node.textContent = message;
  holder.append(node);
}

function parsePolicyParams(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [name, label, type, required, description, options] = line.split("|").map(part => part.trim());
      const param = {
        name,
        label: label || name,
        type: type || "text",
        description: description || "",
        required: required === "true" || required === "required",
        options: splitList(options || "")
      };
      if (!param.options.length) delete param.options;
      if (!param.description) delete param.description;
      return param;
    })
    .filter(param => param.name);
}

function parseTeamRoles(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [name, label, agent, skill, responsibilities, consumes, produces, tools, notes] = line.split("|").map(part => part.trim());
      return {
        name,
        label,
        agent: agent || "chat",
        skill,
        responsibilities: splitList(responsibilities || ""),
        consumes: splitList(consumes || ""),
        produces: splitList(produces || ""),
        tools: splitList(tools || ""),
        notes
      };
    })
    .filter(role => role.name)
    .map(cleanEmptyFields);
}

function parseTeamHandoffs(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [from, to, kind, subject, artifacts, blackboard, condition, description] = line.split("|").map(part => part.trim());
      return {
        from,
        to,
        kind,
        subject,
        artifacts: splitList(artifacts || ""),
        blackboard: splitList(blackboard || ""),
        condition,
        description
      };
    })
    .filter(handoff => handoff.from && handoff.to)
    .map(cleanEmptyFields);
}

function parseTeamBlackboard(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [kind, title, ownerRole, status, tags, description] = line.split("|").map(part => part.trim());
      return {
        kind,
        title,
        owner_role: ownerRole,
        status,
        tags: splitList(tags || ""),
        description
      };
    })
    .filter(item => item.kind)
    .map(cleanEmptyFields);
}

function formatTeamRoles(roles) {
  return (roles || [])
    .map(role => [
      role.name || "",
      role.label || "",
      role.agent || "",
      role.skill || "",
      (role.responsibilities || []).join(", "),
      (role.consumes || []).join(", "),
      (role.produces || []).join(", "),
      (role.tools || []).join(", "),
      role.notes || ""
    ].join("|"))
    .join("\n");
}

function formatTeamHandoffs(handoffs) {
  return (handoffs || [])
    .map(handoff => [
      handoff.from || "",
      handoff.to || "",
      handoff.kind || "",
      handoff.subject || "",
      (handoff.artifacts || []).join(", "),
      (handoff.blackboard || []).join(", "),
      handoff.condition || "",
      handoff.description || ""
    ].join("|"))
    .join("\n");
}

function formatTeamBlackboard(items) {
  return (items || [])
    .map(item => [
      item.kind || "",
      item.title || "",
      item.owner_role || "",
      item.status || "",
      (item.tags || []).join(", "),
      item.description || ""
    ].join("|"))
    .join("\n");
}

function defaultTeamRoles() {
  return [
    { name: "planner", label: t("catalog.teamRolePlannerLabel"), agent: "planner", skill: "execution-plan", responsibilities: [t("catalog.teamRolePlannerClarify"), t("catalog.teamRolePlannerPlan")], produces: ["plan"] },
    { name: "reviewer", label: t("catalog.teamRoleReviewerLabel"), agent: "auditor", skill: "code-audit", responsibilities: [t("catalog.teamRoleReviewerReview")], consumes: ["plan"], produces: ["findings"] }
  ];
}

function defaultTeamHandoffs() {
  return [
    { from: "planner", to: "reviewer", kind: "review_request", subject: t("catalog.teamHandoffSubjectDefault"), artifacts: ["plan"], blackboard: ["decisions"] }
  ];
}

function defaultTeamBlackboard() {
  return [
    { kind: "decision", title: t("catalog.teamBlackboardDecisionsTitle"), owner_role: "planner", status: "open" },
    { kind: "risk", title: t("catalog.teamBlackboardRisksTitle"), owner_role: "reviewer", status: "open" }
  ];
}

function parseJSONArray(value, fallback, label = "") {
  const text = String(value || "").trim();
  if (!text) return fallback;
  try {
    const parsed = JSON.parse(text);
    if (Array.isArray(parsed)) return parsed;
    if (label) throw new Error(`${label}: ${t("catalog.invalidJSONArray")}`);
    return fallback;
  } catch {
    if (label) throw new Error(`${label}: ${t("catalog.invalidJSONArray")}`);
    return fallback;
  }
}

function parseJSONValue(value, label = "", fallback) {
  const text = String(value || "").trim();
  if (!text && arguments.length >= 3) return fallback;
  if (!text) throw new Error(`${label || "JSON"} ${t("catalog.invalidJSON")}`);
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`${label || "JSON"} ${t("catalog.invalidJSON")}`);
  }
}

function parseOptionalJSONObject(value, label = "") {
  const text = String(value || "").trim();
  if (!text) return null;
  const parsed = parseJSONValue(text, label);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error(`${label || "JSON"} ${t("catalog.invalidJSONObject")}`);
  return parsed;
}

function cleanEmptyFields(value) {
  for (const key of Object.keys(value)) {
    if (Array.isArray(value[key]) && !value[key].length) delete value[key];
    if (value[key] === "") delete value[key];
  }
  return value;
}

function formatPolicyParams(params) {
  return (params || [])
    .map(param => [
      param.name || "",
      param.label || param.name || "",
      param.type || "text",
      param.required ? "true" : "false",
      param.description || "",
      (param.options || []).join(",")
    ].join("|"))
    .join("\n");
}

function defaultPolicyParams() {
  return [
    { name: "params.ref", label: t("catalog.policyParamReferenceLabel"), type: "reference", required: true, description: t("catalog.policyParamReferenceDescription") },
    { name: "params.needle", label: t("catalog.policyParamNeedleLabel"), type: "text", required: true, description: t("catalog.policyParamNeedleDescription") }
  ];
}

function policyRuleLabel(name) {
  return String(name || "policy-rule")
    .split(/[-_]+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function teamTemplateLabel(name) {
  return String(name || "team-template")
    .split(/[-_]+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function workflowTemplateLabel(name) {
  return localizedText(teamTemplateLabel(name || "workflow-template"));
}

function kitLabel(name) {
  return localizedText(teamTemplateLabel(name || "kit"));
}

function nodeDisplayLabel(type) {
  return localizedText(teamTemplateLabel(type || "node"));
}

function expressionDisplayLabel(name) {
  return localizedText(teamTemplateLabel(String(name || "helper").replaceAll("_", "-")));
}

function firstWorkflowTemplateName() {
  return catalogContext.workflowTemplates?.[0]?.name || "plan-fix-audit";
}

function firstWorkflowGraphName() {
  return catalogContext.workflowGraphs?.[0]?.name || firstWorkflowTemplateName();
}

function defaultWorkflowTemplateName() {
  const source = firstWorkflowTemplateName();
  return normalizeName(`${source || "workflow-template"}-copy`);
}

function defaultWorkflowSchemaName() {
  const source = catalogContext.workflowSchemas?.[0]?.workflow || firstWorkflowTemplateName() || "workflow-schema";
  return normalizeName(source);
}

function selectedWorkflowSchema(root = document) {
  const selected = root.querySelector("#workflowSchemaSource")?.value || "";
  return (catalogContext.workflowSchemas || []).find(schema => schema.workflow === selected) || catalogContext.workflowSchemas?.[0] || null;
}

function ensureWorkflowSchemaSourceOption(root, name) {
  const select = root.querySelector("#workflowSchemaSource");
  if (!select || !name) return;
  if ([...select.options].some(option => option.value === name)) return;
  const option = document.createElement("option");
  option.value = name;
  option.textContent = `${name} (${t("catalog.savedWorkflowSchemas")})`;
  select.appendChild(option);
}

function workflowSchemaResourceFromSchema(schema = {}, description = "") {
  const name = normalizeName(schema.workflow || defaultWorkflowSchemaName());
  return {
    kind: "goflow.workflow_schema_resource",
    version: 2,
    min_supported_version: 1,
    name,
    description: description || t("catalog.workflowSchemaDescriptionDefault"),
    schema: {
      ...structuredCloneSafe(schema),
      workflow: name
    }
  };
}

function defaultNodeMetadataType() {
  return catalogContext.nodeTypes?.[0]?.type || "policy_guard";
}

function defaultExpressionHelperName() {
  return catalogContext.expressionHelpers?.[0]?.name || "risk_rank";
}

function shortResourcePath(path) {
  const parts = String(path || "").split(/[\\/]+/).filter(Boolean);
  if (parts.length <= 2) return parts.join("/");
  return `${parts.at(-2)}/${parts.at(-1)}`;
}

function structuredCloneSafe(value) {
  return JSON.parse(JSON.stringify(value || {}));
}

function normalizeStringMap(value) {
  const out = {};
  for (const [key, item] of Object.entries(value || {})) {
    const normalizedKey = String(key || "").trim();
    if (!normalizedKey) continue;
    out[normalizedKey] = item == null ? "" : String(item).trim();
  }
  return out;
}

function stringBool(value) {
  return ["1", "true", "yes", "on"].includes(String(value || "").trim().toLowerCase());
}

function parseMap(value) {
  const out = {};
  for (const line of String(value || "").split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const index = trimmed.indexOf("=");
    if (index <= 0) continue;
    out[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim();
  }
  return out;
}

function formatMap(value) {
  return Object.entries(value || {}).map(([key, item]) => `${key}=${item}`).join("\n");
}

function numberOrZero(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function normalizeName(value) {
  return String(value || "")
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "") || "custom";
}

function splitList(value) {
  return String(value || "").split(",").map(item => item.trim()).filter(Boolean);
}

function splitLines(value) {
  return String(value || "").split(/\r?\n|,/).map(item => item.trim()).filter(Boolean);
}

function parseTools(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const parts = line.split("|").map(part => part.trim());
      return { name: parts[0], required: parts.slice(1).includes("required") };
    });
}
