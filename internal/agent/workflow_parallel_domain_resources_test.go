package agent

import (
	"reflect"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestParallelDomainSelectionUsesResourceDeclarations(t *testing.T) {
	for _, test := range []struct {
		name, domain string
		branch       workflowGraphStage
		want         []int
	}{
		{"custom alias", "材料复核", workflowGraphStage{Name: "review-notes", Params: map[string]string{"domain_aliases": "田野笔记,材料复核"}}, []int{0}},
		{"declared domain", "FIELD_NOTES", workflowGraphStage{Name: "observe", Params: map[string]string{"domain": "field-notes"}}, []int{0}},
		{"exact stage", "review-notes", workflowGraphStage{Name: "review-notes"}, []int{0}},
		{"no implicit software mapping", "software", workflowGraphStage{Name: "software-worker"}, nil},
		{"explicit legacy alias", "software", workflowGraphStage{Name: "software-worker", Params: map[string]string{"domain_aliases": "software"}}, []int{0}},
		{"multi leaves planner selection", "multi", workflowGraphStage{Name: "observe", Params: map[string]string{"domain_aliases": "multi"}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			graph := workflowGraph{Stages: []workflowGraphStage{test.branch, {Name: "outside", Params: map[string]string{"domain_aliases": test.domain}}}}
			stage := workflowGraphStage{Params: map[string]string{"domain": test.domain}}
			var runner WorkflowRunner
			got := runner.workflowGraphParallelDomainStageIndices(graph, stage, []int{0}, "", nil)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selected %v, want %v", got, test.want)
			}
		})
	}
}

func TestBundledParallelDomainAliasesComeFromTemplate(t *testing.T) {
	rendered, err := RenderWorkflowTemplateYAML("multi-domain-intake-router", "multi-domain-intake-router")
	if err != nil {
		t.Fatal(err)
	}
	var graph workflowGraph
	if err := yaml.Unmarshal([]byte(rendered), &graph); err != nil {
		t.Fatal(err)
	}
	var candidates []int
	for index := range graph.Stages {
		candidates = append(candidates, index)
	}
	for domain, target := range map[string]string{
		"software": "software-worker", "web": "web-security-worker", "research": "research-worker",
		"security": "security-worker", "binary": "binary-worker", "docs": "docs-worker",
		"ops": "ops-worker", "customer": "support-worker", "framework": "platform-worker", "general": "general-worker",
	} {
		stage := workflowGraphStage{Params: map[string]string{"domain": domain}}
		var runner WorkflowRunner
		selected := runner.workflowGraphParallelDomainStageIndices(graph, stage, candidates, "", nil)
		if names := workflowGraphStageNamesForIndicesList(graph, selected); !reflect.DeepEqual(names, []string{target}) {
			t.Fatalf("domain %s selected %v, want %s", domain, names, target)
		}
	}
}

func TestParallelUnknownDomainRunsOnlyResourceSelectedBranch(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "custom-domain-dispatch", `
name: custom-domain-dispatch
stages:
  - name: dispatch
    node_type: parallel
    params:
      domain: field-notes-review
      active_branches: unrelated
    next: [observe, unrelated]
  - name: observe
    agent: chat
    skill: execution-plan
    params:
      domain_aliases: field-notes-review,田野笔记
    next: [join]
  - name: unrelated
    agent: planner
    skill: execution-plan
    next: [join]
  - name: join
    node_type: join
    params:
      wait_for_ref: stages.dispatch.outputs.branches
`)
	selected := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "记录材料已复核"}}}}
	unrelated := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "不应执行"}}}}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"chat": selected, "planner": unrelated})
	result, err := runtime.WorkflowRunner().Run(t.Context(), "custom-domain-dispatch", "复核给定材料", true, nil)
	if err != nil || selected.calls != 1 || unrelated.calls != 0 {
		t.Fatalf("unknown-domain routing: selected=%d unrelated=%d err=%v", selected.calls, unrelated.calls, err)
	}
	dispatch := workflowGraphStageResultByName(t, result.CompletedStages, "dispatch")
	join := workflowGraphStageResultByName(t, result.CompletedStages, "join")
	if dispatch.Output.Variables["branches"] != "observe" || join.Output.Variables["completed_count"] != "1" {
		t.Fatalf("dispatch/join did not preserve the resource selection: %#v %#v", dispatch.Output, join.Output)
	}
}
