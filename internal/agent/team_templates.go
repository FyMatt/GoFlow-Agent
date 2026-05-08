package agent

import (
	"sort"
)

// TeamTemplateSummary is a compact reusable multi-agent team row.
type TeamTemplateSummary struct {
	Kind                  string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Version               int      `json:"version,omitempty" yaml:"version,omitempty"`
	MinVersion            int      `json:"min_supported_version,omitempty" yaml:"min_supported_version,omitempty"`
	MigratedFromVersion   int      `json:"migrated_from_version,omitempty" yaml:"migrated_from_version,omitempty"`
	Name                  string   `json:"name" yaml:"name"`
	Title                 string   `json:"title" yaml:"title"`
	Description           string   `json:"description,omitempty" yaml:"description,omitempty"`
	Category              string   `json:"category,omitempty" yaml:"category,omitempty"`
	Tags                  []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Roles                 int      `json:"roles" yaml:"roles"`
	QuorumPresetCount     int      `json:"quorum_presets,omitempty" yaml:"quorum_presets,omitempty"`
	RecommendedWorkflow   string   `json:"recommended_workflow,omitempty" yaml:"recommended_workflow,omitempty"`
	RecommendedEntryAgent string   `json:"recommended_entry_agent,omitempty" yaml:"recommended_entry_agent,omitempty"`
	Source                string   `json:"source,omitempty" yaml:"source,omitempty"`
	Path                  string   `json:"path,omitempty" yaml:"path,omitempty"`
	Custom                bool     `json:"custom,omitempty" yaml:"custom,omitempty"`
}

// TeamTemplate describes a reusable collaboration pattern for Studio and future team execution.
type TeamTemplate struct {
	TeamTemplateSummary
	RoleTemplates       []TeamRoleTemplate       `json:"role_templates,omitempty" yaml:"role_templates,omitempty"`
	Handoffs            []TeamHandoffTemplate    `json:"handoffs,omitempty" yaml:"handoffs,omitempty"`
	BlackboardTemplates []TeamBlackboardTemplate `json:"blackboard_templates,omitempty" yaml:"blackboard_templates,omitempty"`
	QuorumPresets       []TeamQuorumPreset       `json:"quorum_presets,omitempty" yaml:"quorum_presets,omitempty"`
	OutputContract      []string                 `json:"output_contract,omitempty" yaml:"output_contract,omitempty"`
}

// TeamRoleTemplate describes one role in a reusable team.
type TeamRoleTemplate struct {
	Name             string   `json:"name" yaml:"name"`
	Label            string   `json:"label,omitempty" yaml:"label,omitempty"`
	Agent            string   `json:"agent" yaml:"agent"`
	Skill            string   `json:"skill,omitempty" yaml:"skill,omitempty"`
	Responsibilities []string `json:"responsibilities,omitempty" yaml:"responsibilities,omitempty"`
	Consumes         []string `json:"consumes,omitempty" yaml:"consumes,omitempty"`
	Produces         []string `json:"produces,omitempty" yaml:"produces,omitempty"`
	Tools            []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Notes            string   `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// TeamHandoffTemplate describes an expected handoff between roles.
type TeamHandoffTemplate struct {
	From        string   `json:"from" yaml:"from"`
	To          string   `json:"to" yaml:"to"`
	Kind        string   `json:"kind,omitempty" yaml:"kind,omitempty"`
	Subject     string   `json:"subject,omitempty" yaml:"subject,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Blackboard  []string `json:"blackboard,omitempty" yaml:"blackboard,omitempty"`
	Condition   string   `json:"condition,omitempty" yaml:"condition,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

// TeamBlackboardTemplate describes shared-state slots a team should maintain.
type TeamBlackboardTemplate struct {
	Kind        string   `json:"kind" yaml:"kind"`
	Title       string   `json:"title,omitempty" yaml:"title,omitempty"`
	OwnerRole   string   `json:"owner_role,omitempty" yaml:"owner_role,omitempty"`
	Status      string   `json:"status,omitempty" yaml:"status,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

// TeamQuorumPreset describes a reusable team approval gate policy.
type TeamQuorumPreset struct {
	Name         string   `json:"name" yaml:"name"`
	Title        string   `json:"title,omitempty" yaml:"title,omitempty"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	Required     int      `json:"required,omitempty" yaml:"required,omitempty"`
	Roles        []string `json:"roles,omitempty" yaml:"roles,omitempty"`
	RejectBlocks bool     `json:"reject_blocks,omitempty" yaml:"reject_blocks,omitempty"`
	Default      bool     `json:"default,omitempty" yaml:"default,omitempty"`
}

// TeamTemplates returns built-in reusable multi-agent team summaries.
func TeamTemplates() []TeamTemplateSummary {
	return (&WorkflowRunner{}).TeamTemplates()
}

// LoadTeamTemplate loads one built-in team template by name.
func LoadTeamTemplate(name string) (TeamTemplate, bool) {
	return (&WorkflowRunner{}).TeamTemplate(name)
}

// TeamTemplates returns built-in and runtime custom reusable multi-agent team summaries.
func (w *WorkflowRunner) TeamTemplates() []TeamTemplateSummary {
	templates := builtInTeamTemplates()
	if custom := w.customTeamTemplateMap(); len(custom) > 0 {
		for name, template := range custom {
			templates[name] = template
		}
	}
	out := make([]TeamTemplateSummary, 0, len(templates))
	for _, template := range templates {
		summary := template.TeamTemplateSummary
		summary.Roles = len(template.RoleTemplates)
		summary.QuorumPresetCount = len(template.QuorumPresets)
		if summary.Source == "" {
			summary.Source = "built_in"
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TeamTemplate loads one built-in or runtime custom team template by name.
func (w *WorkflowRunner) TeamTemplate(name string) (TeamTemplate, bool) {
	if template, ok := w.customTeamTemplate(name); ok {
		template.TeamTemplateSummary.Roles = len(template.RoleTemplates)
		return template, true
	}
	template, ok := builtInTeamTemplates()[normalizePersistedWorkflowName(name)]
	if !ok {
		return TeamTemplate{}, false
	}
	template.TeamTemplateSummary.Roles = len(template.RoleTemplates)
	template.TeamTemplateSummary.QuorumPresetCount = len(template.QuorumPresets)
	if template.TeamTemplateSummary.Source == "" {
		template.TeamTemplateSummary.Source = "built_in"
	}
	return template, true
}

func builtInTeamTemplates() map[string]TeamTemplate {
	templates := []TeamTemplate{
		softwareTaskTeamTemplate(),
		frameworkExtensionTeamTemplate(),
		auditSecurityTeamTemplate(),
		webResearchTeamTemplate(),
		binaryTriageTeamTemplate(),
		documentationTeamTemplate(),
		operationsRunbookTeamTemplate(),
		customerSupportTeamTemplate(),
	}
	out := make(map[string]TeamTemplate, len(templates))
	for _, template := range templates {
		name := normalizePersistedWorkflowName(template.Name)
		template.Name = name
		template.TeamTemplateSummary.Roles = len(template.RoleTemplates)
		out[name] = template
	}
	return out
}

func frameworkExtensionTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "framework-extension-team",
			Title:                 "Framework Extension Team",
			Category:              "platform",
			Description:           "Product, platform, safety, and authoring roles for building linked Agent/Skill/Tool/Workflow/Team extensions.",
			Tags:                  []string{"agent-framework", "extension", "kit", "second-development"},
			RecommendedWorkflow:   "agent-framework-extension",
			RecommendedEntryAgent: "planner",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "product-architect", Label: "Product Architect", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"clarify vertical use case", "define user journey", "identify required resources and acceptance criteria"}, Produces: []string{"extension_brief", "acceptance_criteria"}},
			{Name: "platform-engineer", Label: "Platform Engineer", Agent: "fixer", Skill: "code-writing", Responsibilities: []string{"map the brief to modular resource files", "create or update Agent/Skill/Tool/Workflow/Team definitions", "keep generated resources versionable"}, Consumes: []string{"extension_brief"}, Produces: []string{"resource_changes", "validation_notes"}},
			{Name: "workflow-designer", Label: "Workflow Designer", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"define workflow node contracts", "connect outputs to downstream inputs", "add quality gates and approval checkpoints"}, Consumes: []string{"extension_brief"}, Produces: []string{"workflow_design", "data_flow"}},
			{Name: "safety-reviewer", Label: "Safety Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"review tool permissions", "check sandbox and approval boundaries", "flag over-broad prompts or missing validation"}, Consumes: []string{"resource_changes", "workflow_design"}, Produces: []string{"safety_review", "required_changes"}},
			{Name: "handoff-writer", Label: "Handoff Writer", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"summarize created resources", "state restart or reload steps", "prepare front-end/operator guidance"}, Consumes: []string{"safety_review", "validation_notes"}, Produces: []string{"operator_handoff"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "product-architect", To: "workflow-designer", Kind: "brief_handoff", Subject: "Extension brief and acceptance criteria", Artifacts: []string{"extension_brief", "acceptance_criteria"}, Blackboard: []string{"request", "plan"}},
			{From: "workflow-designer", To: "platform-engineer", Kind: "resource_plan", Subject: "Workflow data flow and resource map", Artifacts: []string{"workflow_design", "data_flow"}, Blackboard: []string{"plan", "decision"}},
			{From: "platform-engineer", To: "safety-reviewer", Kind: "review_request", Subject: "Generated extension resources", Artifacts: []string{"resource_changes", "validation_notes"}, Blackboard: []string{"evidence", "finding"}},
			{From: "safety-reviewer", To: "handoff-writer", Kind: "final_review", Subject: "Safety review and required operator steps", Artifacts: []string{"safety_review", "required_changes"}, Blackboard: []string{"decision", "finding"}},
		},
		BlackboardTemplates: commonTeamBlackboard("product-architect"),
		QuorumPresets: []TeamQuorumPreset{
			{Name: "extension-review", Title: "Extension safety review", Description: "Require workflow designer and safety reviewer approval before handoff.", Required: 2, Roles: []string{"workflow-designer", "safety-reviewer"}, RejectBlocks: true, Default: true},
			{Name: "full-extension-team", Title: "Full extension team approval", Description: "Require every extension role to approve high-impact framework changes.", Required: 5, Roles: []string{"product-architect", "platform-engineer", "workflow-designer", "safety-reviewer", "handoff-writer"}, RejectBlocks: true},
		},
		OutputContract: []string{"extension_brief", "workflow_design", "resource_changes", "safety_review", "operator_handoff"},
	}
}

func softwareTaskTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "software-task-team",
			Title:                 "Software Task Team",
			Category:              "software",
			Description:           "Planner, implementer, auditor, and verifier pattern for code changes.",
			Tags:                  []string{"coding", "plan", "fix", "audit"},
			RecommendedWorkflow:   "plan-fix-audit",
			RecommendedEntryAgent: "planner",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "planner", Label: "Planner", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"clarify task scope", "identify files and risks", "produce executable plan"}, Produces: []string{"implementation_plan", "risk_notes"}},
			{Name: "implementer", Label: "Implementer", Agent: "fixer", Skill: "code-writing", Responsibilities: []string{"apply scoped changes", "run focused verification", "summarize changed files"}, Consumes: []string{"implementation_plan"}, Produces: []string{"changes", "verification"}},
			{Name: "reviewer", Label: "Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"review behavior changes", "find regressions", "check missing tests"}, Consumes: []string{"changes", "verification"}, Produces: []string{"review_findings"}},
			{Name: "reporter", Label: "Reporter", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"summarize outcome", "surface residual risk", "prepare user handoff"}, Consumes: []string{"review_findings", "changes"}, Produces: []string{"final_handoff"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "planner", To: "implementer", Kind: "plan_handoff", Subject: "Implementation plan", Artifacts: []string{"implementation_plan"}, Blackboard: []string{"plan", "risk_notes"}},
			{From: "implementer", To: "reviewer", Kind: "review_request", Subject: "Changes ready for audit", Artifacts: []string{"changes", "verification"}, Blackboard: []string{"changes", "verification"}},
			{From: "reviewer", To: "reporter", Kind: "final_review", Subject: "Review findings", Artifacts: []string{"review_findings"}, Blackboard: []string{"findings", "decisions"}},
		},
		BlackboardTemplates: commonTeamBlackboard("planner"),
		QuorumPresets: []TeamQuorumPreset{
			{Name: "software-review", Title: "Planner and reviewer approval", Description: "Require planner and reviewer approval before handoff.", Required: 2, Roles: []string{"planner", "reviewer"}, RejectBlocks: true, Default: true},
			{Name: "full-team", Title: "Full team approval", Description: "Require every software team role to approve.", Required: 4, Roles: []string{"planner", "implementer", "reviewer", "reporter"}, RejectBlocks: true},
		},
		OutputContract: []string{"final_handoff", "changed_files", "verification", "residual_risks"},
	}
}

func auditSecurityTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "audit-security-team",
			Title:                 "Audit And Security Team",
			Category:              "security",
			Description:           "Scope, threat-model, audit, policy gate, and evidence report pattern.",
			Tags:                  []string{"audit", "security", "risk", "policy"},
			RecommendedWorkflow:   "human-input-security-review",
			RecommendedEntryAgent: "auditor",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "scope-lead", Label: "Scope Lead", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"collect target scope", "define allowed actions", "set evidence requirements"}, Produces: []string{"scope", "constraints"}},
			{Name: "security-analyst", Label: "Security Analyst", Agent: "auditor", Skill: "vulnerability-research", Responsibilities: []string{"enumerate likely weaknesses", "map evidence to risks", "avoid unsupported claims"}, Consumes: []string{"scope"}, Produces: []string{"risk_register", "evidence"}},
			{Name: "code-auditor", Label: "Code Auditor", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"review relevant source", "classify findings", "propose safe validation"}, Consumes: []string{"risk_register", "evidence"}, Produces: []string{"findings"}},
			{Name: "policy-reviewer", Label: "Policy Reviewer", Agent: "auditor", Skill: "execution-plan", Responsibilities: []string{"check scope compliance", "flag approval needs", "prepare disclosure-safe summary"}, Consumes: []string{"findings", "constraints"}, Produces: []string{"policy_decision", "final_report"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "scope-lead", To: "security-analyst", Kind: "scope_handoff", Subject: "Authorized scope", Artifacts: []string{"scope", "constraints"}},
			{From: "security-analyst", To: "code-auditor", Kind: "evidence_handoff", Subject: "Evidence and risk register", Artifacts: []string{"risk_register", "evidence"}},
			{From: "code-auditor", To: "policy-reviewer", Kind: "policy_review", Subject: "Findings require policy review", Artifacts: []string{"findings"}, Condition: "risk_at_least(high)"},
		},
		BlackboardTemplates: commonTeamBlackboard("scope-lead"),
		OutputContract:      []string{"scope", "evidence", "findings", "policy_decision", "final_report"},
	}
}

func webResearchTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "web-research-team",
			Title:                 "Web Research Team",
			Category:              "security",
			Description:           "Collect web assets, audit client/server evidence, and branch on risk.",
			Tags:                  []string{"web", "research", "assets", "risk"},
			RecommendedWorkflow:   "web-research-risk",
			RecommendedEntryAgent: "auditor",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "asset-collector", Label: "Asset Collector", Agent: "auditor", Skill: "web-vulnerability-research", Responsibilities: []string{"fetch target page", "collect linked scripts and source evidence", "record reachable surfaces"}, Tools: []string{"web_tools/fetch_url", "web_tools/fetch_page_assets"}, Produces: []string{"asset_inventory", "page_evidence"}},
			{Name: "js-reviewer", Label: "JavaScript Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"inspect collected scripts", "identify risky sinks and sources", "separate evidence from speculation"}, Consumes: []string{"asset_inventory", "page_evidence"}, Produces: []string{"client_findings"}},
			{Name: "risk-analyst", Label: "Risk Analyst", Agent: "auditor", Skill: "vulnerability-research", Responsibilities: []string{"classify impact", "decide whether deeper verification is needed", "prepare remediation outline"}, Consumes: []string{"client_findings"}, Produces: []string{"risk_decision", "report"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "asset-collector", To: "js-reviewer", Kind: "asset_handoff", Subject: "Fetched web evidence", Artifacts: []string{"asset_inventory", "page_evidence"}},
			{From: "js-reviewer", To: "risk-analyst", Kind: "finding_handoff", Subject: "Client-side findings", Artifacts: []string{"client_findings"}},
		},
		BlackboardTemplates: commonTeamBlackboard("asset-collector"),
		OutputContract:      []string{"asset_inventory", "client_findings", "risk_decision", "report"},
	}
}

func binaryTriageTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "binary-triage-team",
			Title:                 "Binary Triage Team",
			Category:              "security",
			Description:           "Static binary triage, reverse-engineering notes, and vulnerability review.",
			Tags:                  []string{"binary", "reverse", "triage"},
			RecommendedWorkflow:   "binary-triage",
			RecommendedEntryAgent: "auditor",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "triage-lead", Label: "Triage Lead", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"define binary analysis scope", "choose safe static-only checks first", "set artifact naming"}, Produces: []string{"triage_plan"}},
			{Name: "reverse-analyst", Label: "Reverse Analyst", Agent: "auditor", Skill: "reverse-engineering", Responsibilities: []string{"summarize binary metadata", "extract strings and suspicious imports", "map code areas for review"}, Tools: []string{"python_notes/binary_file_info", "python_notes/binary_strings", "python_notes/hex_preview"}, Consumes: []string{"triage_plan"}, Produces: []string{"reverse_notes", "evidence"}},
			{Name: "vulnerability-analyst", Label: "Vulnerability Analyst", Agent: "auditor", Skill: "binary-vulnerability-research", Responsibilities: []string{"review evidence for vulnerability classes", "classify exploitability cautiously", "recommend next static/dynamic checks"}, Consumes: []string{"reverse_notes", "evidence"}, Produces: []string{"findings", "next_checks"}},
			{Name: "reporter", Label: "Reporter", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"produce defensive report", "preserve uncertainty", "list reproduction prerequisites"}, Consumes: []string{"findings", "next_checks"}, Produces: []string{"final_report"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "triage-lead", To: "reverse-analyst", Kind: "triage_plan", Subject: "Static analysis plan", Artifacts: []string{"triage_plan"}},
			{From: "reverse-analyst", To: "vulnerability-analyst", Kind: "evidence_handoff", Subject: "Reverse notes and evidence", Artifacts: []string{"reverse_notes", "evidence"}},
			{From: "vulnerability-analyst", To: "reporter", Kind: "report_handoff", Subject: "Findings and next checks", Artifacts: []string{"findings", "next_checks"}},
		},
		BlackboardTemplates: commonTeamBlackboard("triage-lead"),
		OutputContract:      []string{"reverse_notes", "findings", "next_checks", "final_report"},
	}
}

func documentationTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "documentation-team",
			Title:                 "Documentation Team",
			Category:              "documentation",
			Description:           "Plan, draft, review, and publication-check documentation work.",
			Tags:                  []string{"docs", "review", "publish"},
			RecommendedWorkflow:   "docs-review-publish",
			RecommendedEntryAgent: "planner",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "docs-planner", Label: "Docs Planner", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"identify audience", "define outline", "spot stale docs"}, Produces: []string{"outline", "doc_targets"}},
			{Name: "docs-writer", Label: "Docs Writer", Agent: "fixer", Skill: "code-writing", Responsibilities: []string{"edit documentation files", "keep examples runnable", "avoid unrelated rewrites"}, Consumes: []string{"outline", "doc_targets"}, Produces: []string{"draft_changes"}},
			{Name: "docs-reviewer", Label: "Docs Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"check accuracy", "check install/deployment steps", "flag missing warnings"}, Consumes: []string{"draft_changes"}, Produces: []string{"review_notes"}},
			{Name: "publish-operator", Label: "Publish Operator", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"prepare release handoff", "list verification and publish commands", "wait for operator approval"}, Consumes: []string{"review_notes"}, Produces: []string{"publish_handoff"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "docs-planner", To: "docs-writer", Kind: "outline_handoff", Subject: "Documentation outline", Artifacts: []string{"outline", "doc_targets"}},
			{From: "docs-writer", To: "docs-reviewer", Kind: "review_request", Subject: "Documentation draft", Artifacts: []string{"draft_changes"}},
			{From: "docs-reviewer", To: "publish-operator", Kind: "publish_gate", Subject: "Reviewed documentation", Artifacts: []string{"review_notes"}},
		},
		BlackboardTemplates: commonTeamBlackboard("docs-planner"),
		OutputContract:      []string{"doc_targets", "draft_changes", "review_notes", "publish_handoff"},
	}
}

func operationsRunbookTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "operations-runbook-team",
			Title:                 "Operations Runbook Team",
			Category:              "operations",
			Description:           "Plan operational changes, draft runbooks, risk-review steps, and hand off execution.",
			Tags:                  []string{"ops", "runbook", "risk", "approval"},
			RecommendedWorkflow:   "operations-runbook",
			RecommendedEntryAgent: "planner",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "incident-planner", Label: "Incident Planner", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"understand operational goal", "identify prechecks and rollback", "define success signals"}, Produces: []string{"runbook_plan", "rollback_plan"}},
			{Name: "runbook-author", Label: "Runbook Author", Agent: "fixer", Skill: "code-writing", Responsibilities: []string{"write or update runbook files", "include commands and expected outputs", "preserve safety checks"}, Consumes: []string{"runbook_plan", "rollback_plan"}, Produces: []string{"runbook_changes"}},
			{Name: "risk-reviewer", Label: "Risk Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"review destructive steps", "check rollback coverage", "flag approval gates"}, Consumes: []string{"runbook_changes"}, Produces: []string{"risk_review"}},
			{Name: "operator-handoff", Label: "Operator Handoff", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"summarize exact operator actions", "separate automated from manual steps", "record unresolved decisions"}, Consumes: []string{"risk_review"}, Produces: []string{"operator_handoff"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "incident-planner", To: "runbook-author", Kind: "runbook_plan", Subject: "Operational plan and rollback", Artifacts: []string{"runbook_plan", "rollback_plan"}},
			{From: "runbook-author", To: "risk-reviewer", Kind: "risk_review", Subject: "Runbook changes ready for review", Artifacts: []string{"runbook_changes"}},
			{From: "risk-reviewer", To: "operator-handoff", Kind: "operator_handoff", Subject: "Reviewed runbook and unresolved decisions", Artifacts: []string{"risk_review"}},
		},
		BlackboardTemplates: commonTeamBlackboard("incident-planner"),
		OutputContract:      []string{"runbook_plan", "rollback_plan", "risk_review", "operator_handoff"},
	}
}

func customerSupportTeamTemplate() TeamTemplate {
	return TeamTemplate{
		TeamTemplateSummary: TeamTemplateSummary{
			Name:                  "customer-support-team",
			Title:                 "Customer Support Team",
			Category:              "support",
			Description:           "Triage, context gathering, response drafting, and customer-readiness review pattern.",
			Tags:                  []string{"support", "triage", "response", "handoff"},
			RecommendedWorkflow:   "customer-support-triage",
			RecommendedEntryAgent: "chat",
		},
		RoleTemplates: []TeamRoleTemplate{
			{Name: "triage-lead", Label: "Triage Lead", Agent: "planner", Skill: "execution-plan", Responsibilities: []string{"classify the customer request", "identify priority and impact", "list missing context"}, Produces: []string{"triage", "priority", "missing_context"}},
			{Name: "context-researcher", Label: "Context Researcher", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"collect workspace or knowledge-base context", "separate facts from assumptions", "summarize relevant constraints"}, Consumes: []string{"triage", "missing_context"}, Produces: []string{"known_facts", "context_summary"}},
			{Name: "response-writer", Label: "Response Writer", Agent: "chat", Skill: "execution-plan", Responsibilities: []string{"draft a concise customer-facing response", "include next steps", "avoid unsupported promises"}, Consumes: []string{"triage", "known_facts"}, Produces: []string{"customer_reply", "internal_notes"}},
			{Name: "support-reviewer", Label: "Support Reviewer", Agent: "auditor", Skill: "code-audit", Responsibilities: []string{"review clarity and accuracy", "flag privacy or escalation risk", "approve or request revision"}, Consumes: []string{"customer_reply", "internal_notes"}, Produces: []string{"review_decision", "risk_notes"}},
		},
		Handoffs: []TeamHandoffTemplate{
			{From: "triage-lead", To: "context-researcher", Kind: "context_request", Subject: "Support context needed", Artifacts: []string{"triage", "missing_context"}, Blackboard: []string{"request", "evidence"}},
			{From: "context-researcher", To: "response-writer", Kind: "response_context", Subject: "Known facts for response", Artifacts: []string{"known_facts", "context_summary"}, Blackboard: []string{"evidence", "decision"}},
			{From: "response-writer", To: "support-reviewer", Kind: "review_request", Subject: "Customer reply ready for review", Artifacts: []string{"customer_reply", "internal_notes"}, Blackboard: []string{"finding", "decision"}},
		},
		BlackboardTemplates: commonTeamBlackboard("triage-lead"),
		QuorumPresets: []TeamQuorumPreset{
			{Name: "support-review", Title: "Support reviewer approval", Description: "Require reviewer approval before sending a customer-facing response.", Required: 1, Roles: []string{"support-reviewer"}, RejectBlocks: true, Default: true},
			{Name: "triage-and-review", Title: "Triage plus review approval", Description: "Require both triage and support review approval for high-impact issues.", Required: 2, Roles: []string{"triage-lead", "support-reviewer"}, RejectBlocks: true},
		},
		OutputContract: []string{"triage", "known_facts", "customer_reply", "review_decision", "risk_notes"},
	}
}

func commonTeamBlackboard(owner string) []TeamBlackboardTemplate {
	return []TeamBlackboardTemplate{
		{Kind: "request", Title: "Original request and constraints", OwnerRole: owner, Status: "open", Tags: []string{"scope"}, Description: "The user request, allowed scope, and assumptions that should survive handoffs."},
		{Kind: "plan", Title: "Working plan", OwnerRole: owner, Status: "open", Tags: []string{"plan"}, Description: "Current executable plan, stage ordering, and known blockers."},
		{Kind: "evidence", Title: "Evidence and observations", OwnerRole: owner, Status: "open", Tags: []string{"evidence"}, Description: "Tool observations, source references, test output, or collected artifacts."},
		{Kind: "decision", Title: "Decisions and approvals", OwnerRole: owner, Status: "open", Tags: []string{"decision"}, Description: "Decisions made by agents or the operator, including approval gates."},
		{Kind: "finding", Title: "Findings and risks", OwnerRole: owner, Status: "open", Tags: []string{"risk"}, Description: "Bugs, vulnerabilities, regressions, or unresolved questions."},
	}
}
