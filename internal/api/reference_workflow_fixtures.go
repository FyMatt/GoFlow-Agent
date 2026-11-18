package api

import "strings"

type resourceCatalogDryRunFixture struct {
	Method         string               `json:"method"`
	Path           string               `json:"path"`
	Request        taskPreflightRequest `json:"request"`
	ExpectedStages []string             `json:"expected_stages"`
	ReadOnly       bool                 `json:"read_only,omitempty"`
}

func referenceWorkflowDryRunFixture(name string) *resourceCatalogDryRunFixture {
	name = strings.ToLower(strings.TrimSpace(name))
	fixture := resourceCatalogDryRunFixture{
		Method: "POST",
		Path:   "/api/runs/preflight",
		Request: taskPreflightRequest{
			Type:     "workflow",
			Workflow: name,
			Approve:  false,
		},
	}
	switch name {
	case "plan-fix-audit":
		fixture.Request.Input = "Inspect a focused repository defect, propose a bounded fix, verify it, and report evidence."
		fixture.ExpectedStages = []string{"intake", "plan", "implement", "verify", "audit", "quality", "final-handoff"}
	case "complex-project-delivery":
		fixture.Request.Input = "Deliver a multi-file project change in reviewed slices, preserve retry evidence, and complete full validation."
		fixture.ExpectedStages = []string{"plan", "approve-plan", "implement", "verify", "audit", "quality", "final-report"}
	case "repository-readiness-review":
		fixture.Request.Input = "Review repository build, test, and configuration readiness without changing any file."
		fixture.ExpectedStages = []string{"inventory", "review-split", "build-review", "test-review", "configuration-review", "report"}
		fixture.ReadOnly = true
	default:
		return nil
	}
	return &fixture
}
