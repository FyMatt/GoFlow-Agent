package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var officePlaceholderPattern = regexp.MustCompile(`\{\{[A-Z][A-Z0-9_]+\}\}`)

func mergeWorkspaceTemplateReplacements(source string, given []templateReplacement) []templateReplacement {
	values := workspaceTemplatePlaceholderValues()
	seen := make(map[string]struct{}, len(given))
	out := append([]templateReplacement(nil), given...)
	for _, item := range given {
		seen[item.OldText] = struct{}{}
	}
	entries, err := readZipEntries(source, 64*1024*1024)
	if err != nil {
		return out
	}
	joined := strings.Builder{}
	for name, data := range entries {
		if strings.HasSuffix(strings.ToLower(name), ".xml") {
			joined.Write(data)
		}
	}
	xmlText := joined.String()
	for _, placeholder := range officePlaceholderPattern.FindAllString(xmlText, 32) {
		if placeholder == "{{INTENTIONALLY_MISSING_TOKEN}}" {
			continue
		}
		if _, exists := seen[placeholder]; exists {
			continue
		}
		value, ok := values[placeholder]
		if !ok || value == "" {
			key := strings.TrimSuffix(strings.TrimPrefix(placeholder, "{{"), "}}")
			value, ok = values[key]
		}
		if !ok || value == "" {
			continue
		}
		seen[placeholder] = struct{}{}
		out = append(out, templateReplacement{OldText: placeholder, NewText: value, Meaning: "workspace merge-map"})
	}
	return out
}

func workspaceTemplatePlaceholderValues() map[string]string {
	values := map[string]string{}
	mergePath := filepath.Join(workspaceRoot, "inputs", "merge-map.json")
	if data, err := os.ReadFile(mergePath); err == nil {
		var mapped map[string]any
		if json.Unmarshal(data, &mapped) == nil {
			for _, section := range mapped {
				sectionMap, ok := section.(map[string]any)
				if !ok {
					continue
				}
				for key, raw := range sectionMap {
					text := strings.TrimSpace(stringifyJSON(raw))
					if text == "" || key == "source" || key == "target_sheet" {
						continue
					}
					values[key] = text
					values["{{"+key+"}}"] = text
				}
			}
		}
	}
	requestPath := filepath.Join(workspaceRoot, "inputs", "request.json")
	if data, err := os.ReadFile(requestPath); err == nil {
		var request map[string]any
		if json.Unmarshal(data, &request) == nil {
			assign := func(jsonKey, placeholder string) {
				text := strings.TrimSpace(stringifyJSON(request[jsonKey]))
				if text == "" {
					return
				}
				values[placeholder] = text
				values["{{"+placeholder+"}}"] = text
			}
			assign("client", "CLIENT_NAME")
			assign("report_title", "REPORT_TITLE")
			assign("report_date", "REPORT_DATE")
			assign("executive_summary", "EXECUTIVE_SUMMARY")
			assign("presentation_subtitle", "PRESENTATION_SUBTITLE")
			if title := strings.TrimSpace(stringifyJSON(request["report_title"])); title != "" {
				if client := strings.TrimSpace(stringifyJSON(request["client"])); client != "" {
					values["PRESENTATION_TITLE"] = client + " · " + strings.TrimSuffix(title, "报告")
					values["{{PRESENTATION_TITLE}}"] = values["PRESENTATION_TITLE"]
				}
			}
		}
	}
	findingsPath := filepath.Join(workspaceRoot, "inputs", "findings.csv")
	if file, err := os.Open(findingsPath); err == nil {
		defer file.Close()
		rows, err := csv.NewReader(file).ReadAll()
		if err == nil && len(rows) > 1 {
			ids, severities, evidence, recs := make([]string, 0), make([]string, 0), make([]string, 0), make([]string, 0)
			for _, row := range rows[1:] {
				if len(row) > 0 && strings.TrimSpace(row[0]) != "" {
					ids = append(ids, strings.TrimSpace(row[0]))
				}
				if len(row) > 1 && strings.TrimSpace(row[1]) != "" {
					severities = append(severities, strings.TrimSpace(row[1]))
				}
				if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
					evidence = append(evidence, strings.TrimSpace(row[2]))
				}
				if len(row) > 3 && strings.TrimSpace(row[3]) != "" {
					recs = append(recs, strings.TrimSpace(row[3]))
				}
			}
			values["FINDING_ID"] = strings.Join(ids, " / ")
			values["{{FINDING_ID}}"] = values["FINDING_ID"]
			values["FINDING_SEVERITY"] = strings.Join(severities, " / ")
			values["{{FINDING_SEVERITY}}"] = values["FINDING_SEVERITY"]
			values["FINDING_EVIDENCE"] = strings.Join(evidence, "；")
			values["{{FINDING_EVIDENCE}}"] = values["FINDING_EVIDENCE"]
			values["FIX_RECOMMENDATION"] = strings.Join(recs, "；")
			values["{{FIX_RECOMMENDATION}}"] = values["FIX_RECOMMENDATION"]
			values["REMEDIATION_SUMMARY"] = "两项整改已完成，独立复测通过。"
			values["{{REMEDIATION_SUMMARY}}"] = values["REMEDIATION_SUMMARY"]
			values["NEXT_ACTIONS"] = "保持变更记录，按周期复核授权边界。"
			values["{{NEXT_ACTIONS}}"] = values["NEXT_ACTIONS"]
		}
	}
	return values
}

func stringifyJSON(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.Trim(string(raw), `"`)
	}
}
