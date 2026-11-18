package session

import (
	"strconv"
	"strings"
)

// UsageRecord is a reported model call, independent of replay retention.
// It contains no prompts, responses, tool arguments, or credentials.
type UsageRecord struct {
	RunID        string `json:"run_id"`
	RunType      string `json:"run_type"`
	Seq          int    `json:"seq"`
	At           string `json:"at"`
	AgentID      string `json:"agent_id,omitempty"`
	Stage        string `json:"stage,omitempty"`
	PromptTokens int    `json:"prompt_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CachedTokens int    `json:"cached_tokens"`
}

func WorkflowUsageRecords(run WorkflowRunSnapshot) []UsageRecord {
	if run.UsageRecords != nil {
		return run.UsageRecords
	}
	var records []UsageRecord
	for i, event := range run.Events {
		if strings.EqualFold(event.Type, "token_usage") {
			seq := event.Seq
			if seq == 0 {
				seq = i + 1
			}
			records = append(records, UsageRecord{RunID: run.ID, RunType: "workflow", Seq: seq, At: event.At, AgentID: event.AgentID, Stage: event.Stage, PromptTokens: maxInt(event.PromptTokens, event.BudgetReportedPromptTokens), OutputTokens: maxInt(event.OutputTokens, event.BudgetOutputTokens), CachedTokens: maxInt(event.CachedTokens, event.BudgetCachedTokens)})
		}
	}
	return records
}

func AgentUsageRecords(run AgentRunSnapshot) []UsageRecord {
	if run.UsageRecords != nil {
		return run.UsageRecords
	}
	var records []UsageRecord
	for i, event := range run.Events {
		if strings.EqualFold(event.Type, "token_usage") {
			seq := event.Seq
			if seq == 0 {
				seq = i + 1
			}
			agentID := event.AgentID
			if strings.TrimSpace(agentID) == "" {
				agentID = run.AgentID
			}
			records = append(records, UsageRecord{RunID: run.ID, RunType: "agent", Seq: seq, At: event.At, AgentID: agentID, Stage: event.TaskStage, PromptTokens: event.PromptTokens, OutputTokens: event.OutputTokens, CachedTokens: event.CachedTokens})
		}
	}
	return records
}

// SnapshotUsageRecords merges the durable ledger with readable legacy history.
// Deduplication also makes migration safe across repeated saves and restarts.
func SnapshotUsageRecords(snapshot Snapshot) []UsageRecord {
	seen := map[string]struct{}{}
	records := make([]UsageRecord, 0, len(snapshot.UsageRecords))
	add := func(items []UsageRecord) {
		for _, record := range items {
			key := record.RunType + ":" + record.RunID + ":" + strconv.Itoa(record.Seq)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			records = append(records, record)
		}
	}
	add(snapshot.UsageRecords)
	for _, run := range snapshot.AgentRuns {
		add(AgentUsageRecords(run))
	}
	for _, run := range snapshot.WorkflowRuns {
		add(WorkflowUsageRecords(run))
	}
	return records
}
