package agent

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func workflowGraphContinuationReplan(graph workflowGraph, completed []WorkflowStageResult, incomplete WorkflowStageResult, slices []session.WorkflowChangeSliceSnapshot) (int, WorkflowStageResult, []WorkflowStageResult, bool) {
	if len(slices) == 0 {
		return 0, WorkflowStageResult{}, nil, false
	}
	for _, slice := range slices {
		if slice.ExecutionMode == "write" {
			return 0, WorkflowStageResult{}, nil, false
		}
	}
	currentIndex := workflowGraphStageIndexByName(graph, string(incomplete.Stage))
	for i := len(completed) - 1; i >= 0; i-- {
		plan := completed[i]
		index := workflowGraphStageIndexByName(graph, string(plan.Stage))
		if index < 0 || index >= currentIndex || !strings.EqualFold(graph.Stages[index].Params["change_slice_policy"], "required") || !workflowGraphNeedsWriteSlices(graph, graph.Stages[index]) {
			continue
		}
		plan.Status = "incomplete"
		plan.Result.Incomplete = true
		plan.Result.StopReason = schema.StopReasonStop
		plan.Result.IncompleteReason = "原计划只有只读分片，不能满足后续修改要求。保留调查证据，补齐未来实施所需的 write 分片，再重新确认计划；规划节点本身仍只读，不执行修改。"
		return index, plan, append([]WorkflowStageResult(nil), completed[:i]...), true
	}
	return 0, WorkflowStageResult{}, nil, false
}
