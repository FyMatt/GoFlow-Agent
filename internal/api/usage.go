package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

type usageTotals struct {
	PromptTokens int64   `json:"prompt_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	ModelCalls   int     `json:"model_calls"`
	CacheHitRate float64 `json:"cache_hit_rate"`
	Runs         int     `json:"runs"`
}

type usageBucket struct {
	Start string `json:"start"`
	usageTotals
}

type usageAgent struct {
	AgentID string `json:"agent_id"`
	usageTotals
}

type usageCoverage struct {
	EarliestAt   string `json:"earliest_at,omitempty"`
	LatestAt     string `json:"latest_at,omitempty"`
	Samples      int    `json:"samples"`
	RetainedRuns int    `json:"retained_runs"`
}

type usageResponse struct {
	GeneratedAt string        `json:"generated_at"`
	Range       string        `json:"range"`
	BucketUnit  string        `json:"bucket_unit"`
	Timezone    string        `json:"timezone"`
	Lifetime    usageTotals   `json:"lifetime"`
	Period      usageTotals   `json:"period"`
	Buckets     []usageBucket `json:"buckets"`
	Agents      []usageAgent  `json:"agents"`
	Coverage    usageCoverage `json:"coverage"`
}

type usageRecord struct {
	At      time.Time
	RunID   string
	AgentID string
	Prompt  int
	Output  int
	Cached  int
}

type usageRangeSpec struct {
	Name       string
	BucketUnit string
	Count      int
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rangeName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, buildUsageResponse(s.runtime.SessionSnapshot(), rangeName, time.Now()))
}

func buildUsageResponse(snapshot session.Snapshot, rangeName string, now time.Time) usageResponse {
	if now.IsZero() {
		now = time.Now()
	}
	location := now.Location()
	if location == nil {
		location = time.Local
	}
	now = now.In(location)
	spec := normalizeUsageRange(rangeName)
	start, bucketStarts := usageBucketStarts(now, spec)
	records := usageRecords(snapshot)
	response := usageResponse{
		GeneratedAt: now.Format(time.RFC3339),
		Range:       spec.Name,
		BucketUnit:  spec.BucketUnit,
		Timezone:    location.String(),
		Buckets:     make([]usageBucket, len(bucketStarts)),
		Coverage: usageCoverage{
			RetainedRuns: len(snapshot.AgentRuns) + len(snapshot.WorkflowRuns),
		},
	}
	for index, bucketStart := range bucketStarts {
		response.Buckets[index].Start = bucketStart.Format(time.RFC3339)
	}
	periodRuns := map[string]struct{}{}
	lifetimeRuns := map[string]struct{}{}
	agents := map[string]*usageAgent{}
	agentRuns := map[string]map[string]struct{}{}
	for _, record := range records {
		addUsageRecord(&response.Lifetime, record)
		lifetimeRuns[record.RunID] = struct{}{}
		if response.Coverage.EarliestAt == "" || record.At.Before(parseUsageTime(response.Coverage.EarliestAt)) {
			response.Coverage.EarliestAt = record.At.Format(time.RFC3339)
		}
		if response.Coverage.LatestAt == "" || record.At.After(parseUsageTime(response.Coverage.LatestAt)) {
			response.Coverage.LatestAt = record.At.Format(time.RFC3339)
		}
		response.Coverage.Samples++
		if record.At.Before(start) || record.At.After(now) {
			continue
		}
		addUsageRecord(&response.Period, record)
		periodRuns[record.RunID] = struct{}{}
		agentID := strings.TrimSpace(record.AgentID)
		if agentID == "" {
			agentID = "unknown"
		}
		item := agents[agentID]
		if item == nil {
			item = &usageAgent{AgentID: agentID}
			agents[agentID] = item
			agentRuns[agentID] = map[string]struct{}{}
		}
		addUsageRecord(&item.usageTotals, record)
		agentRuns[agentID][record.RunID] = struct{}{}
		if index := usageBucketIndex(record.At.In(location), bucketStarts, spec.BucketUnit); index >= 0 {
			addUsageRecord(&response.Buckets[index].usageTotals, record)
		}
	}
	response.Lifetime.Runs = len(lifetimeRuns)
	response.Period.Runs = len(periodRuns)
	finalizeUsageTotals(&response.Lifetime)
	finalizeUsageTotals(&response.Period)
	for index := range response.Buckets {
		finalizeUsageTotals(&response.Buckets[index].usageTotals)
	}
	response.Agents = make([]usageAgent, 0, len(agents))
	for _, item := range agents {
		item.Runs = len(agentRuns[item.AgentID])
		finalizeUsageTotals(&item.usageTotals)
		response.Agents = append(response.Agents, *item)
	}
	sort.Slice(response.Agents, func(i, j int) bool {
		if response.Agents[i].TotalTokens != response.Agents[j].TotalTokens {
			return response.Agents[i].TotalTokens > response.Agents[j].TotalTokens
		}
		return response.Agents[i].AgentID < response.Agents[j].AgentID
	})
	return response
}

func normalizeUsageRange(value string) usageRangeSpec {
	switch value {
	case "24h":
		return usageRangeSpec{Name: "24h", BucketUnit: "hour", Count: 24}
	case "30d":
		return usageRangeSpec{Name: "30d", BucketUnit: "day", Count: 30}
	default:
		return usageRangeSpec{Name: "7d", BucketUnit: "day", Count: 7}
	}
}

func usageBucketStarts(now time.Time, spec usageRangeSpec) (time.Time, []time.Time) {
	starts := make([]time.Time, spec.Count)
	if spec.BucketUnit == "hour" {
		last := now.Truncate(time.Hour)
		first := last.Add(-time.Duration(spec.Count-1) * time.Hour)
		for index := range starts {
			starts[index] = first.Add(time.Duration(index) * time.Hour)
		}
		return first, starts
	}
	last := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	first := last.AddDate(0, 0, -(spec.Count - 1))
	for index := range starts {
		starts[index] = first.AddDate(0, 0, index)
	}
	return first, starts
}

func usageBucketIndex(at time.Time, starts []time.Time, unit string) int {
	if len(starts) == 0 || at.Before(starts[0]) {
		return -1
	}
	if unit == "hour" {
		index := int(at.Sub(starts[0]) / time.Hour)
		if index >= 0 && index < len(starts) {
			return index
		}
		return -1
	}
	for index := len(starts) - 1; index >= 0; index-- {
		if !at.Before(starts[index]) {
			return index
		}
	}
	return -1
}

func usageRecords(snapshot session.Snapshot) []usageRecord {
	records := make([]usageRecord, 0)
	for _, sample := range session.SnapshotUsageRecords(snapshot) {
		if record, ok := usageRecordFromLedger(sample); ok {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })
	return records
}

func retainedAgentRunUsage(run session.AgentRunSnapshot) usageTotals {
	totals := usageTotals{}
	for _, sample := range session.AgentUsageRecords(run) {
		if record, ok := usageRecordFromLedger(sample); ok {
			addUsageRecord(&totals, record)
		}
	}
	finalizeUsageTotals(&totals)
	return totals
}

func retainedWorkflowRunUsage(run session.WorkflowRunSnapshot) usageTotals {
	totals := usageTotals{}
	for _, sample := range session.WorkflowUsageRecords(run) {
		if record, ok := usageRecordFromLedger(sample); ok {
			addUsageRecord(&totals, record)
		}
	}
	finalizeUsageTotals(&totals)
	return totals
}

func usageRecordFromLedger(sample session.UsageRecord) (usageRecord, bool) {
	at, err := time.Parse(time.RFC3339Nano, sample.At)
	if err != nil {
		return usageRecord{}, false
	}
	return usageRecord{At: at, RunID: sample.RunID, AgentID: sample.AgentID, Prompt: sample.PromptTokens, Output: sample.OutputTokens, Cached: sample.CachedTokens}, true
}

func addUsageRecord(totals *usageTotals, record usageRecord) {
	if totals == nil {
		return
	}
	totals.PromptTokens += int64(maxUsageInt(record.Prompt))
	totals.OutputTokens += int64(maxUsageInt(record.Output))
	totals.CachedTokens += int64(maxUsageInt(record.Cached))
	totals.ModelCalls++
}

func finalizeUsageTotals(totals *usageTotals) {
	if totals == nil {
		return
	}
	totals.TotalTokens = totals.PromptTokens + totals.OutputTokens
	if totals.PromptTokens > 0 {
		totals.CacheHitRate = float64(totals.CachedTokens) / float64(totals.PromptTokens)
		if totals.CacheHitRate > 1 {
			totals.CacheHitRate = 1
		}
	}
}

func maxUsageInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func parseUsageTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}
