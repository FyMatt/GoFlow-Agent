package agent

import "strings"

func workflowGraphNeedsWriteSlices(graph workflowGraph, stage workflowGraphStage) bool {
	var queue []string
	appendNext := func(node workflowGraphStage) {
		queue = append(queue, node.Next...)
		for _, target := range node.Routes {
			queue = append(queue, target)
		}
		for _, target := range node.Cases {
			queue = append(queue, target)
		}
	}
	appendNext(stage)
	seen := map[string]bool{stage.Name: true}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		index := workflowGraphStageIndexByName(graph, name)
		if index < 0 {
			continue
		}
		node := graph.Stages[index]
		if !strings.EqualFold(node.Execution.Mode, "disabled") && workflowGraphParamBool(node, "require_workspace_mutation") {
			return true
		}
		appendNext(node)
	}
	return false
}
