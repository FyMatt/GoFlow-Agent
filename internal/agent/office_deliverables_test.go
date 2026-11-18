package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type officePreviewMCP struct {
	calls int
}

func (m *officePreviewMCP) ListTools(context.Context) ([]schema.Tool, error)    { return nil, nil }
func (m *officePreviewMCP) RefreshTools(context.Context) ([]schema.Tool, error) { return nil, nil }
func (m *officePreviewMCP) CallTool(context.Context, string, []byte) (schema.ToolResult, error) {
	return schema.ToolResult{}, nil
}
func (m *officePreviewMCP) HealthStatus(context.Context) map[string]string { return nil }
func (m *officePreviewMCP) ToolNames() []string                            { return nil }
func (m *officePreviewMCP) CallToolInWorkspace(_ context.Context, name string, arguments []byte, root string) (schema.ToolResult, error) {
	m.calls++
	if name != "office_tools/render_office_preview" {
		return schema.ToolResult{}, fmt.Errorf("unexpected tool %s", name)
	}
	var input map[string]any
	if err := json.Unmarshal(arguments, &input); err != nil {
		return schema.ToolResult{}, err
	}
	if input["path"] != "deliverables/report.pdf" || input["output_dir"] != "deliverables/preview" {
		return schema.ToolResult{}, fmt.Errorf("unexpected arguments: %#v", input)
	}
	preview := filepath.Join(root, "deliverables", "preview", "preview-page-1.png")
	if err := os.MkdirAll(filepath.Dir(preview), 0o755); err != nil {
		return schema.ToolResult{}, err
	}
	if err := os.WriteFile(preview, []byte("rendered-png"), 0o644); err != nil {
		return schema.ToolResult{}, err
	}
	return schema.ToolResult{ToolName: name, Content: `{"preview_paths":["deliverables/preview/preview-page-1.png"]}`}, nil
}

func TestSeedOfficeTemplateDeliverablesWritesFromWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	for _, item := range officeTemplateDeliverables {
		src := filepath.Join(root, filepath.FromSlash(item.template))
		if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
			t.Fatal(err)
		}
		template, err := zipWithXML("word/document.xml", `<w:t>{{CLIENT_NAME}} {{REPORT_TITLE}} {{REPORT_DATE}} SEC-001 SEC-002 整改结果 SUM(</w:t>`)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, template, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inputs", "request.json"), []byte(`{"client":"星河科技","report_title":"Alpha 平台安全整改交付报告","report_date":"2026-08-18"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root}}}
	result := schema.AgentResult{}
	runner.seedOfficeTemplateDeliverables(&result)
	for _, item := range officeTemplateDeliverables {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.output)))
		if err != nil {
			t.Fatalf("missing %s: %v", item.output, err)
		}
		if officeZipHasUnresolvedPlaceholder(data) {
			t.Fatalf("%s still has placeholders", item.output)
		}
		if !officeZipContainsAny(data, "星河科技") {
			t.Fatalf("%s missing filled client name", item.output)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "deliverables", "report.pdf")); !os.IsNotExist(err) {
		t.Fatalf("template seeding must not fabricate report.pdf: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deliverables", "preview", "page-1.png")); !os.IsNotExist(err) {
		t.Fatalf("template seeding must not fabricate preview PNGs: %v", err)
	}
	for _, toolResult := range result.ToolResults {
		if strings.Contains(toolResult.ToolName, "export_office_pdf") || strings.Contains(toolResult.ToolName, "render_office_preview") {
			t.Fatalf("template seeding must not record fake conversion evidence: %#v", result.ToolResults)
		}
	}
	pptx := filepath.Join(root, "deliverables", "review.pptx")
	incomplete, err := zipWithXML("ppt/slides/slide2.xml", `<a:t>整改结果</a:t>`)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pptx, incomplete, 0o644); err != nil {
		t.Fatal(err)
	}
	result = schema.AgentResult{}
	runner.seedOfficeTemplateDeliverables(&result)
	filled, err := os.ReadFile(pptx)
	if err != nil {
		t.Fatal(err)
	}
	if !officeZipContainsAny(filled, "星河科技") {
		t.Fatal("pptx missing 星河科技 must be rewritten from the template")
	}
}

func TestEnsureOfficePreviewUsesRealWorkspaceTool(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"templates/security-report-template.docx",
		"deliverables/report.pdf",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mcpClient := &officePreviewMCP{}
	runner := &WorkflowRunner{runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root}, mcp: mcpClient}}
	result := schema.AgentResult{}
	runner.ensureOfficePreview(&result)
	if mcpClient.calls != 1 {
		t.Fatalf("render tool calls=%d, want 1", mcpClient.calls)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].ToolName != "office_tools/render_office_preview" || result.ToolResults[0].IsError {
		t.Fatalf("missing real preview evidence: %#v", result.ToolResults)
	}
	if _, err := os.Stat(filepath.Join(root, "deliverables", "preview", "preview-page-1.png")); err != nil {
		t.Fatalf("preview was not rendered: %v", err)
	}
	runner.ensureOfficePreview(&result)
	if mcpClient.calls != 1 {
		t.Fatalf("existing preview must not be rendered twice: calls=%d", mcpClient.calls)
	}
}

func TestLoadOfficePlaceholderValuesUsesFixtureVariantAndFindings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inputs", "request.json"), []byte(`{"client":"星河科技","report_title":"Alpha 平台安全整改交付报告","report_date":"2026-08-18"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fixture-manifest.json"), []byte(`{"variant":{"review_date":"2026-08-19"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := "finding_id,severity,evidence,recommendation\nSEC-009,高,审计日志记录完整访问令牌,删除明文并补充复测\n"
	if err := os.WriteFile(filepath.Join(root, "inputs", "findings.csv"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
	values := loadOfficePlaceholderValues(root)
	if values["{{REPORT_DATE}}"] != "2026-08-19" {
		t.Fatalf("variant date was not applied: %#v", values)
	}
	if values["{{FINDING_1_ID}}"] != "SEC-009" || !strings.Contains(values["{{FINDING_1_EVIDENCE}}"], "完整访问令牌") {
		t.Fatalf("finding placeholders were not derived from CSV: %#v", values)
	}
}

func TestCopyOfficeZipReplacesRequestPlaceholders(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "templates", "security-report-template.docx")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	template, err := zipWithXML("word/document.xml", `<w:t>{{CLIENT_NAME}} {{REPORT_TITLE}} {{REPORT_DATE}} SEC-001</w:t>`)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, template, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inputs", "request.json"), []byte(`{"client":"星河科技","report_title":"Alpha 平台安全整改交付报告","report_date":"2026-08-18"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	values := loadOfficePlaceholderValues(root)
	if values["{{CLIENT_NAME}}"] != "星河科技" {
		t.Fatalf("placeholder values=%#v", values)
	}
	filled, err := copyOfficeZipWithPlaceholders(src, values)
	if err != nil {
		t.Fatal(err)
	}
	if officeZipHasUnresolvedPlaceholder(filled) {
		t.Fatal("placeholders remain")
	}
	if !officeZipContainsAny(filled, "星河科技", "Alpha 平台安全整改交付报告", "2026-08-18") {
		xml, _ := officeZipXML(filled)
		t.Fatalf("filled doc missing markers: %s", xml)
	}
}

func zipWithXML(name, xml string) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, err := writer.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write([]byte(xml)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
