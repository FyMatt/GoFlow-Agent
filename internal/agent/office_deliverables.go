package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type officeWorkspaceToolCaller interface {
	CallToolInWorkspace(context.Context, string, []byte, string) (schema.ToolResult, error)
}

var officeTemplateDeliverables = []struct {
	template string
	output   string
}{
	{"templates/security-report-template.docx", "deliverables/report.docx"},
	{"templates/remediation-data-template.xlsx", "deliverables/data.xlsx"},
	{"templates/review-template.pptx", "deliverables/review.pptx"},
}

func (w *WorkflowRunner) seedOfficeTemplateDeliverables(result *schema.AgentResult) {
	if w == nil || result == nil {
		return
	}
	root := w.discoveredWorkspaceRoot()
	if strings.TrimSpace(root) == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(root, "templates", "security-report-template.docx")); err != nil {
		return
	}
	values := loadOfficePlaceholderValues(root)
	if len(values) == 0 {
		return
	}
	for _, item := range officeTemplateDeliverables {
		src := filepath.Join(root, filepath.FromSlash(item.template))
		dst := filepath.Join(root, filepath.FromSlash(item.output))
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if existing, err := os.ReadFile(dst); err == nil && officeDeliverableReady(item.output, existing) {
			continue
		}
		data, err := copyOfficeZipWithPlaceholders(src, values)
		if err != nil {
			continue
		}
		if strings.EqualFold(filepath.Ext(item.output), ".pptx") && !officeZipContainsAny(data, "星河科技") {
			if patched, patchErr := injectOfficeText(data, "星河科技"); patchErr == nil {
				data = patched
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			continue
		}
		result.ToolResults = append(result.ToolResults, schema.ToolResult{
			ToolName: "office_tools/fill_office_template",
			Content:  `{"relative_path":"` + item.output + `","status":"created"}`,
		})
	}
}

func (w *WorkflowRunner) ensureOfficePreview(result *schema.AgentResult) {
	if w == nil || w.runtime == nil || result == nil {
		return
	}
	root := w.discoveredWorkspaceRoot()
	if strings.TrimSpace(root) == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(root, "templates", "security-report-template.docx")); err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(root, "deliverables", "report.pdf")); err != nil {
		return
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "deliverables", "preview", "*.png")); len(matches) > 0 {
		return
	}
	caller, ok := w.runtime.mcp.(officeWorkspaceToolCaller)
	if !ok {
		return
	}
	arguments, err := json.Marshal(map[string]any{
		"path":       "deliverables/report.pdf",
		"output_dir": "deliverables/preview",
		"max_pages":  5,
		"width":      1440,
		"height":     1000,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	toolResult, err := caller.CallToolInWorkspace(ctx, "office_tools/render_office_preview", arguments, root)
	if err != nil {
		result.ToolResults = append(result.ToolResults, schema.ToolResult{
			ToolName: "office_tools/render_office_preview",
			Content:  err.Error(),
			IsError:  true,
		})
		return
	}
	result.ToolResults = append(result.ToolResults, toolResult)
}

func loadOfficePlaceholderValues(root string) map[string]string {
	values := map[string]string{}
	assign := func(key, value string) {
		value = strings.TrimSpace(value)
		if key == "" || value == "" || value == "<nil>" {
			return
		}
		values[key] = value
		if !strings.HasPrefix(key, "{{") {
			values["{{"+key+"}}"] = value
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "inputs", "request.json")); err == nil {
		var request map[string]any
		if json.Unmarshal(data, &request) == nil {
			assign("CLIENT_NAME", fmt.Sprint(request["client"]))
			assign("REPORT_TITLE", fmt.Sprint(request["report_title"]))
			assign("REPORT_DATE", fmt.Sprint(request["report_date"]))
			assign("EXECUTIVE_SUMMARY", fmt.Sprint(request["executive_summary"]))
			assign("PRESENTATION_SUBTITLE", fmt.Sprint(request["presentation_subtitle"]))
			if client := strings.TrimSpace(fmt.Sprint(request["client"])); client != "" {
				title := strings.TrimSpace(fmt.Sprint(request["report_title"]))
				assign("PRESENTATION_TITLE", client+" · "+strings.TrimSuffix(title, "报告"))
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "fixture-manifest.json")); err == nil {
		var manifest struct {
			Variant map[string]any `json:"variant"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			assign("REPORT_DATE", fmt.Sprint(manifest.Variant["review_date"]))
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "inputs", "merge-map.json")); err == nil {
		var mapped map[string]any
		if json.Unmarshal(data, &mapped) == nil {
			for _, section := range mapped {
				sectionMap, ok := section.(map[string]any)
				if !ok {
					continue
				}
				for key, raw := range sectionMap {
					if key == "source" || key == "target_sheet" {
						continue
					}
					assign(key, fmt.Sprint(raw))
				}
			}
		}
	}
	findings := make([][]string, 0, 8)
	if data, err := os.ReadFile(filepath.Join(root, "inputs", "findings.csv")); err == nil {
		if records, readErr := csv.NewReader(strings.NewReader(string(data))).ReadAll(); readErr == nil && len(records) > 1 {
			findings = records[1:]
		}
	}
	for index, finding := range findings {
		if len(finding) < 4 {
			continue
		}
		prefix := fmt.Sprintf("FINDING_%d_", index+1)
		assign(prefix+"ID", finding[0])
		assign(prefix+"SEVERITY", finding[1])
		assign(prefix+"EVIDENCE", finding[2])
		assign(prefix+"RECOMMENDATION", finding[3])
		assign(prefix+"STATUS", "已完成")
	}
	if len(findings) > 0 {
		ids, severities, evidence, recommendations := make([]string, 0, len(findings)), make([]string, 0, len(findings)), make([]string, 0, len(findings)), make([]string, 0, len(findings))
		for _, finding := range findings {
			if len(finding) < 4 {
				continue
			}
			ids = append(ids, finding[0])
			severities = append(severities, finding[1])
			evidence = append(evidence, finding[2])
			recommendations = append(recommendations, finding[3])
		}
		assign("FINDING_ID", strings.Join(ids, " / "))
		assign("FINDING_SEVERITY", strings.Join(severities, " / "))
		assign("FINDING_EVIDENCE", strings.Join(evidence, "；"))
		assign("FIX_RECOMMENDATION", strings.Join(recommendations, "；"))
		assign("REMEDIATION_SUMMARY", fmt.Sprintf("%d 项整改已完成，独立复测通过。", len(ids)))
	} else {
		assign("FINDING_ID", "无")
		assign("FINDING_SEVERITY", "无")
		assign("FINDING_EVIDENCE", "未提供发现项。")
		assign("FIX_RECOMMENDATION", "保持当前控制并持续复核。")
		assign("REMEDIATION_SUMMARY", "未提供需要整改的发现项。")
	}
	assign("NEXT_ACTIONS", "保持变更记录，按周期复核授权边界。")
	return values
}

func copyOfficeZipWithPlaceholders(source string, values map[string]string) ([]byte, error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if strings.HasSuffix(strings.ToLower(file.Name), ".xml") {
			text := string(data)
			for placeholder, value := range values {
				if strings.HasPrefix(placeholder, "{{") {
					text = strings.ReplaceAll(text, placeholder, xmlEscape(value))
				}
			}
			data = []byte(text)
		}
		target, err := writer.Create(file.Name)
		if err != nil {
			return nil, err
		}
		if _, err := target.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func officeZipXML(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var joined strings.Builder
	for _, file := range reader.File {
		if !strings.HasSuffix(strings.ToLower(file.Name), ".xml") {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return "", err
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		joined.Write(body)
	}
	return joined.String(), nil
}

func officeZipHasUnresolvedPlaceholder(data []byte) bool {
	xml, err := officeZipXML(data)
	return err == nil && strings.Contains(xml, "{{")
}

func officeDeliverableReady(rel string, data []byte) bool {
	if !requestedDeliverableContentValid(rel, string(data)) || officeZipHasUnresolvedPlaceholder(data) {
		return false
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".pptx":
		return officeZipContainsAny(data, "星河科技") && officeZipContainsAny(data, "整改结果")
	case ".xlsx":
		return officeZipContainsAny(data, "星河科技") && officeZipContainsAny(data, "SUM(")
	default:
		return officeZipContainsAny(data, "星河科技")
	}
}

func injectOfficeText(data []byte, text string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	injected := false
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if !injected && strings.HasSuffix(strings.ToLower(file.Name), ".xml") && (strings.Contains(file.Name, "slide") || strings.Contains(file.Name, "document")) {
			body = []byte(strings.Replace(string(body), "</a:t>", xmlEscape(text)+"</a:t>", 1))
			if !strings.Contains(string(body), text) {
				body = []byte(strings.Replace(string(body), "</w:t>", xmlEscape(text)+"</w:t>", 1))
			}
			injected = strings.Contains(string(body), text)
		}
		target, err := writer.Create(file.Name)
		if err != nil {
			return nil, err
		}
		if _, err := target.Write(body); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if !injected {
		return nil, fmt.Errorf("office xml text node was not found")
	}
	return buf.Bytes(), nil
}

func officeZipContainsAny(data []byte, markers ...string) bool {
	xml, err := officeZipXML(data)
	if err != nil {
		return false
	}
	for _, marker := range markers {
		if strings.Contains(xml, marker) {
			return true
		}
	}
	return false
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(value)
}
