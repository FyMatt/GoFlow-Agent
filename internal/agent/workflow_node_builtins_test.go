package agent

import "testing"

func TestBuiltInWorkflowNodeTypesLoadFromEmbeddedYAML(t *testing.T) {
	nodes := builtInWorkflowNodeTypes()
	if len(nodes) < 10 {
		t.Fatalf("expected built-in workflow node types, got %d", len(nodes))
	}
	byType := make(map[string]WorkflowNodeTypeOption, len(nodes))
	for _, node := range nodes {
		byType[node.Type] = node
	}
	agentNode, ok := byType["agent"]
	if !ok {
		t.Fatal("expected agent node type")
	}
	if agentNode.Source != "built_in" || agentNode.Category != "execute" {
		t.Fatalf("unexpected agent node metadata: %#v", agentNode)
	}
	if len(agentNode.Fields) == 0 || len(agentNode.Outputs) == 0 || len(agentNode.Hints) == 0 || len(agentNode.Examples) == 0 {
		t.Fatalf("expected rich agent node metadata, got %#v", agentNode)
	}
	policyGuard, ok := byType["policy_guard"]
	if !ok {
		t.Fatal("expected policy_guard node type")
	}
	if !policyGuard.Control || len(policyGuard.Fields) == 0 {
		t.Fatalf("unexpected policy_guard metadata: %#v", policyGuard)
	}
}

func TestBuiltInWorkflowNodeTypesAreCloned(t *testing.T) {
	first := builtInWorkflowNodeTypes()
	if len(first) == 0 {
		t.Fatal("expected node types")
	}
	index := -1
	for i, node := range first {
		if len(node.Fields) > 0 && len(node.Hints) > 0 {
			index = i
			break
		}
	}
	if index == -1 {
		t.Fatal("expected node with fields and hints")
	}
	first[index].Type = "mutated"
	first[index].Fields[0].Name = "mutated"
	first[index].Hints[0] = "mutated"

	second := builtInWorkflowNodeTypes()
	if second[index].Type == "mutated" || second[index].Fields[0].Name == "mutated" || second[index].Hints[0] == "mutated" {
		t.Fatalf("expected cloned node metadata, got %#v", second[index])
	}
}
