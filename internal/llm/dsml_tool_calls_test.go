package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestParseDSMLToolCallsConvertsCompleteEnvelope(t *testing.T) {
	content := `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="write_file">
<｜｜DSML｜｜parameter name="path" string="true">deliverables/report.md</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="content" string="true"># 报告

保留 &lt;TIMESTAMP&gt; 和 {"enabled":true} 原文。</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || cleaned != "" || len(calls) != 1 {
		t.Fatalf("unexpected parse result: ok=%v cleaned=%q calls=%#v", ok, cleaned, calls)
	}
	if calls[0].Name != "write_file" || calls[0].ID == "" {
		t.Fatalf("unexpected call identity: %#v", calls[0])
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if args["path"] != "deliverables/report.md" || !strings.Contains(args["content"].(string), `{"enabled":true}`) {
		t.Fatalf("unexpected arguments: %#v", args)
	}
}

func TestParseDSMLToolCallsSupportsTypedParameters(t *testing.T) {
	content := `<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="tool"><｜｜DSML｜｜parameter name="enabled">true</｜｜DSML｜｜parameter><｜｜DSML｜｜parameter name="ports">[80,443]</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`
	calls, _, ok := parseDSMLToolCalls(content)
	if !ok || len(calls) != 1 || string(calls[0].Arguments) != `{"enabled":true,"ports":[80,443]}` {
		t.Fatalf("unexpected typed call: ok=%v calls=%#v", ok, calls)
	}
}

func TestParseDSMLToolCallsSupportsPlainXMLFallback(t *testing.T) {
	content := `<tool_calls>
<tool_call name="compile_research_evidence">
<parameter name="evidence">基于原始来源核验，不合并发布与稳定阶段。</parameter>
<parameter name="claims">[{"id":"C1","status":"已支持"}]</parameter>
</tool_call>
</tool_calls>`
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || cleaned != "" || len(calls) != 1 || calls[0].Name != "compile_research_evidence" {
		t.Fatalf("unexpected XML fallback result: ok=%v cleaned=%q calls=%#v", ok, cleaned, calls)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if args["evidence"] != "基于原始来源核验，不合并发布与稳定阶段。" {
		t.Fatalf("unexpected string argument: %#v", args)
	}
	claims, ok := args["claims"].([]any)
	if !ok || len(claims) != 1 {
		t.Fatalf("unexpected structured argument: %#v", args)
	}
}

func TestParseDSMLToolCallsSupportsSingularFunctionEnvelope(t *testing.T) {
	content := `<tool_call>
<function name="compile_research_evidence">
<parameter name="evidence">以原始来源为准</parameter>
<parameter name="raw_sources">[{"status":200}]</parameter>
</function>
</tool_call>`
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || cleaned != "" || len(calls) != 1 || calls[0].Name != "compile_research_evidence" {
		t.Fatalf("unexpected singular function envelope: ok=%v cleaned=%q calls=%#v", ok, cleaned, calls)
	}
}

func TestParseDSMLToolCallsSupportsRepeatedWrappersAndInvoke(t *testing.T) {
	content := `<tool_calls><tool_calls>
<invoke name="compile_research_evidence">
<parameter name="evidence">逐项核验</parameter>
</invoke>
</tool_calls></tool_calls>`
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || cleaned != "" || len(calls) != 1 || calls[0].Name != "compile_research_evidence" {
		t.Fatalf("unexpected nested invoke envelope: ok=%v cleaned=%q calls=%#v", ok, cleaned, calls)
	}
}

func TestParseDSMLToolCallsRejectsUnsafePlainXMLFallback(t *testing.T) {
	inputs := []string{
		`example: <tool_calls><tool_call name="read_file"><parameter name="path">input/source.txt</parameter></tool_call></tool_calls>`,
		`example: <tool_call><function name="read_file"><parameter name="path">input/source.txt</parameter></function></tool_call>`,
		`<tool_calls><tool_call name="read file"><parameter name="path">input/source.txt</parameter></tool_call></tool_calls>`,
		`<tool_calls><tool_calls><invoke name="read_file"><parameter name="path">input/source.txt</parameter></invoke></tool_calls>`,
		`<tool_calls><tool_call name="read_file"><parameter name="path">input/source.txt</parameter><parameter name="path">other</parameter></tool_call></tool_calls>`,
		`<tool_calls><tool_call name="read_file"><parameter name="path">input/source.txt</parameter></tool_calls>`,
	}
	for _, input := range inputs {
		if calls, cleaned, ok := parseDSMLToolCalls(input); ok || calls != nil || cleaned != input {
			t.Fatalf("expected rejection for %q, got ok=%v cleaned=%q calls=%#v", input, ok, cleaned, calls)
		}
	}
}

func TestParseDSMLToolCallsSupportsFencedJSONFallback(t *testing.T) {
	content := "```json\n" + `{
  "tool": "compile_research_evidence",
  "arguments": {
    "evidence": "以原始快照为准",
    "raw_sources": [{"status": 200}]
  }
}` + "\n```"
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || cleaned != "" || len(calls) != 1 || calls[0].Name != "compile_research_evidence" {
		t.Fatalf("unexpected fenced JSON result: ok=%v cleaned=%q calls=%#v", ok, cleaned, calls)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil || args["evidence"] != "以原始快照为准" {
		t.Fatalf("unexpected arguments: args=%#v err=%v", args, err)
	}
}

func TestParseDSMLToolCallsSupportsToCodeEnvelopeAndDeduplicatesTransportRepeats(t *testing.T) {
	arguments := `{"path":"internal/filesafe/path.go","edits":[{"old_text":"old","new_text":"new"}]}`
	content := "to=file_tools.apply_patch 代引 code\n" + arguments + "\n" +
		"to=file_tools.apply_patch 代引 code\n" + arguments + "\n" +
		"summary: apply the approved boundary fix"
	calls, cleaned, ok := parseDSMLToolCalls(content)
	if !ok || len(calls) != 1 {
		t.Fatalf("unexpected to-code parse result: ok=%v calls=%#v cleaned=%q", ok, calls, cleaned)
	}
	if calls[0].Name != "file_tools/apply_patch" || cleaned != "summary: apply the approved boundary fix" {
		t.Fatalf("unexpected normalized call=%#v cleaned=%q", calls[0], cleaned)
	}
	if !json.Valid(calls[0].Arguments) || !strings.Contains(string(calls[0].Arguments), `"path":"internal/filesafe/path.go"`) {
		t.Fatalf("unexpected arguments: %s", calls[0].Arguments)
	}
}

func TestParseDSMLToolCallsRejectsUnsafeToCodeFallback(t *testing.T) {
	inputs := []string{
		"example: to=file_tools.apply_patch code\n{\"path\":\"a\"}",
		"to=file_tools.apply_patch\n{\"path\":\"a\"}",
		"to=file tools.apply_patch code\n{\"path\":\"a\"}",
		"to=file_tools.apply_patch code\nnot-json",
		"to=file_tools.apply_patch code\n[1,2,3]",
	}
	for _, input := range inputs {
		if calls, cleaned, ok := parseDSMLToolCalls(input); ok || calls != nil || cleaned != input {
			t.Fatalf("expected rejection for %q, got ok=%v calls=%#v cleaned=%q", input, ok, calls, cleaned)
		}
	}
}

func TestParseDSMLToolCallsRejectsUnsafeFencedJSONFallback(t *testing.T) {
	inputs := []string{
		"prefix\n```json\n{\"tool\":\"read_file\",\"arguments\":{\"path\":\"input/source.txt\"}}\n```",
		"```json\n{\"tool\":\"read file\",\"arguments\":{\"path\":\"input/source.txt\"}}\n```",
		"```json\n{\"tool\":\"read_file\",\"arguments\":\"input/source.txt\"}\n```",
		"```json\n{\"tool\":\"read_file\",\"arguments\":{\"path\":\"input/source.txt\"},\"comment\":\"run it\"}\n```",
		`{"tool":"read_file","arguments":{"path":"input/source.txt"}}`,
	}
	for _, input := range inputs {
		if calls, cleaned, ok := parseDSMLToolCalls(input); ok || calls != nil || cleaned != input {
			t.Fatalf("expected rejection for %q, got ok=%v cleaned=%q calls=%#v", input, ok, cleaned, calls)
		}
	}
}

func TestParseDSMLToolCallsRejectsPartialOrEmbeddedMarkup(t *testing.T) {
	inputs := []string{
		`example: <｜｜DSML｜｜tool_calls></｜｜DSML｜｜tool_calls>`,
		`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="write_file"></｜｜DSML｜｜tool_calls>`,
		`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="write file"></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`,
		`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="write_file"><｜｜DSML｜｜parameter name="path">not-json</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`,
	}
	for _, input := range inputs {
		if calls, cleaned, ok := parseDSMLToolCalls(input); ok || calls != nil || cleaned != input {
			t.Fatalf("expected rejection for %q, got ok=%v cleaned=%q calls=%#v", input, ok, cleaned, calls)
		}
	}
}

func TestNormalizeResponsePromotesDSMLToolCall(t *testing.T) {
	raw := openAIResponse{}
	raw.Choices = append(raw.Choices, struct {
		FinishReason string        `json:"finish_reason"`
		Message      openAIMessage `json:"message"`
	}{
		FinishReason: "stop",
		Message:      openAIMessage{Role: "assistant", Content: `<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="read_file"><｜｜DSML｜｜parameter name="path" string="true">input/source.txt</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`},
	})
	resp, err := normalizeResponse(raw, nil)
	if err != nil {
		t.Fatalf("normalize response: %v", err)
	}
	if resp.StopReason != schema.StopReasonToolCalls || resp.Message.Content != "" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestNormalizeResponsePromotesPlainXMLToolCall(t *testing.T) {
	raw := openAIResponse{}
	raw.Choices = append(raw.Choices, struct {
		FinishReason string        `json:"finish_reason"`
		Message      openAIMessage `json:"message"`
	}{
		FinishReason: "stop",
		Message:      openAIMessage{Role: "assistant", Content: `<tool_calls><tool_call name="read_file"><parameter name="path">input/source.txt</parameter></tool_call></tool_calls>`},
	})
	resp, err := normalizeResponse(raw, nil)
	if err != nil {
		t.Fatalf("normalize response: %v", err)
	}
	if resp.StopReason != schema.StopReasonToolCalls || resp.Message.Content != "" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestStreamAssemblerPromotesDSMLToolCall(t *testing.T) {
	assembler := newStreamAssembler(nil)
	assembler.role = "assistant"
	assembler.finishReason = "stop"
	assembler.content.WriteString(`<｜｜DSML｜｜tool_calls><｜｜DSML｜｜invoke name="read_file"><｜｜DSML｜｜parameter name="path" string="true">input/source.txt</｜｜DSML｜｜parameter></｜｜DSML｜｜invoke></｜｜DSML｜｜tool_calls>`)
	resp := assembler.response()
	if resp.StopReason != schema.StopReasonToolCalls || resp.Message.Content != "" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}
