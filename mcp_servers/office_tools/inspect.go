package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var pdfPagePattern = regexp.MustCompile(`/Type\s*/Page\b`)
var officeTextNodePattern = regexp.MustCompile(`(?s)(<(?:w:t|a:t|t)(?:\s[^>]*)?>)(.*?)(</(?:w:t|a:t|t)>)`)
var sourceFidelityPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{name: "标识符", pattern: regexp.MustCompile(`\b[A-Z][A-Z0-9_-]{1,}-[0-9]+\b`)},
	{name: "技术缩写", pattern: regexp.MustCompile(`\b[A-Z][A-Z0-9.+#_-]{1,}\b`)},
	{name: "日期", pattern: regexp.MustCompile(`(?:\b[0-9]{4}[-/.][0-9]{1,2}(?:[-/.][0-9]{1,2})?\b|[0-9]{4}年[0-9]{1,2}月(?:[0-9]{1,2}日)?)`)},
	{name: "比例", pattern: regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?%`)},
	{name: "版本", pattern: regexp.MustCompile(`\bv?[0-9]+\.[0-9]+(?:\.[0-9]+)?\b`)},
	{name: "网络地址", pattern: regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)},
}

var sourceFidelityClaimTerms = []string{
	"sql注入", "注入", "xss", "rce", "漏洞", "攻击", "泄露", "提权", "越权", "后门", "恶意", "cvss", "cve",
	"严重", "危急", "高危", "中危", "低危", "高风险", "中风险", "低风险",
	"全部", "所有", "完全", "零错误", "零风险", "无风险", "不存在", "已完成", "符合预期", "确保", "一致", "证明", "确认",
	"有效", "缓解", "已修复", "修复完成", "按设计要求", "验收测试", "进一步复核", "全面复核",
	"安全隐患", "未发现", "成功", "可靠", "达标", "满足要求", "建议措施", "建议执行", "已通过", "待复核",
}

var sourceFidelityTechnicalAllowlist = map[string]bool{
	"DOCX": true, "XLSX": true, "PPTX": true, "PDF": true, "WPS": true, "WORD": true, "EXCEL": true, "PPT": true,
}

func inspectOfficeDocument(args map[string]any) (map[string]any, error) {
	raw, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(raw)
	if err != nil {
		return nil, err
	}
	maxText := intArg(args, "max_text_chars", 12000, 500, 50000)
	return inspectPath(path, maxText)
}

func inspectPath(path string, maxText int) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("路径必须指向文档文件")
	}
	hash, size, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	base := map[string]any{"artifact_path": relativeWorkspacePath(path), "sha256": hash, "size": size}
	switch ext {
	case ".docx":
		return inspectDOCX(path, maxText, base)
	case ".xlsx":
		return inspectXLSX(path, maxText, base)
	case ".pptx":
		return inspectPPTX(path, maxText, base)
	case ".pdf":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) {
			return nil, fmt.Errorf("文件不是有效的 PDF")
		}
		base["format"] = "PDF"
		base["mime"] = "application/pdf"
		base["pages"] = len(pdfPagePattern.FindAll(data, -1))
		base["header"] = string(data[:8])
		return base, nil
	default:
		return nil, fmt.Errorf("仅支持 DOCX、XLSX、PPTX 和 PDF")
	}
}

func inspectDOCX(path string, maxText int, base map[string]any) (map[string]any, error) {
	entries, err := readZipEntries(path, 16*1024*1024)
	if err != nil {
		return nil, err
	}
	document, ok := entries["word/document.xml"]
	if !ok {
		return nil, fmt.Errorf("DOCX 缺少 word/document.xml")
	}
	text, counts, err := parseXMLText(document, map[string]bool{"t": true}, map[string]bool{"p": true, "tbl": true, "tr": true, "tc": true})
	if err != nil {
		return nil, fmt.Errorf("解析 DOCX 失败：%w", err)
	}
	base["format"] = "DOCX"
	base["mime"] = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	base["paragraphs"] = counts["p"]
	base["tables"] = counts["tbl"]
	base["table_rows"] = counts["tr"]
	base["table_cells"] = counts["tc"]
	base["text_preview"] = truncateRunes(text, maxText)
	base["package_entries"] = len(entries)
	return base, nil
}

func inspectXLSX(path string, maxText int, base map[string]any) (map[string]any, error) {
	entries, err := readZipEntries(path, 32*1024*1024)
	if err != nil {
		return nil, err
	}
	workbook, ok := entries["xl/workbook.xml"]
	if !ok {
		return nil, fmt.Errorf("XLSX 缺少 xl/workbook.xml")
	}
	sheetNames := xmlAttributeValues(workbook, "sheet", "name")
	worksheetNames := make([]string, 0)
	for name := range entries {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			worksheetNames = append(worksheetNames, name)
		}
	}
	sort.Strings(worksheetNames)
	rows, cells, formulas := 0, 0, 0
	autoFilters, frozenPanes, printSetups, landscapeSheets, fitToPageSheets := 0, 0, 0, 0, 0
	var textParts []string
	for _, name := range worksheetNames {
		worksheet := entries[name]
		text, counts, parseErr := parseXMLText(worksheet, map[string]bool{"t": true, "v": true}, map[string]bool{
			"row": true, "c": true, "f": true, "autoFilter": true, "pane": true, "pageSetup": true, "pageSetUpPr": true,
		})
		if parseErr != nil {
			return nil, fmt.Errorf("解析工作表失败：%w", parseErr)
		}
		rows += counts["row"]
		cells += counts["c"]
		formulas += counts["f"]
		autoFilters += counts["autoFilter"]
		if bytes.Contains(worksheet, []byte(`state="frozen"`)) {
			frozenPanes++
		}
		printSetups += counts["pageSetup"]
		if strings.EqualFold(firstXMLAttribute(worksheet, "pageSetup", "orientation"), "landscape") {
			landscapeSheets++
		}
		if firstXMLAttribute(worksheet, "pageSetUpPr", "fitToPage") == "1" && firstXMLAttribute(worksheet, "pageSetup", "fitToWidth") == "1" {
			fitToPageSheets++
		}
		textParts = append(textParts, text)
	}
	base["format"] = "XLSX"
	base["mime"] = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	base["sheets"] = len(worksheetNames)
	base["sheet_names"] = sheetNames
	base["rows"] = rows
	base["cells"] = cells
	base["formulas"] = formulas
	base["auto_filters"] = autoFilters
	base["frozen_panes"] = frozenPanes
	base["print_setups"] = printSetups
	base["landscape_sheets"] = landscapeSheets
	base["fit_to_page_sheets"] = fitToPageSheets
	base["text_preview"] = truncateRunes(strings.Join(textParts, "\n"), maxText)
	base["package_entries"] = len(entries)
	return base, nil
}

func inspectPPTX(path string, maxText int, base map[string]any) (map[string]any, error) {
	entries, err := readZipEntries(path, 32*1024*1024)
	if err != nil {
		return nil, err
	}
	presentation, ok := entries["ppt/presentation.xml"]
	if !ok {
		return nil, fmt.Errorf("PPTX 缺少 ppt/presentation.xml")
	}
	slideNames := make([]string, 0)
	for name := range entries {
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") && !strings.Contains(name, "_rels") {
			slideNames = append(slideNames, name)
		}
	}
	sort.Slice(slideNames, func(i, j int) bool { return naturalSlideOrder(slideNames[i]) < naturalSlideOrder(slideNames[j]) })
	var textParts []string
	shapes := 0
	for _, name := range slideNames {
		text, counts, parseErr := parseXMLText(entries[name], map[string]bool{"t": true}, map[string]bool{"sp": true})
		if parseErr != nil {
			return nil, fmt.Errorf("解析幻灯片失败：%w", parseErr)
		}
		shapes += counts["sp"]
		textParts = append(textParts, text)
	}
	cx := firstXMLAttribute(presentation, "sldSz", "cx")
	cy := firstXMLAttribute(presentation, "sldSz", "cy")
	base["format"] = "PPTX"
	base["mime"] = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	base["slides"] = len(slideNames)
	base["shapes"] = shapes
	base["slide_size"] = map[string]string{"cx": cx, "cy": cy}
	base["aspect_ratio"] = aspectRatioLabel(cx, cy)
	base["text_preview"] = truncateRunes(strings.Join(textParts, "\n"), maxText)
	base["package_entries"] = len(entries)
	return base, nil
}

func validateOfficeDocument(args map[string]any) (map[string]any, error) {
	raw, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(raw)
	if err != nil {
		return nil, err
	}
	maxText := 5000
	if boolArg(args, "require_source_fidelity", false) {
		maxText = 50000
	}
	inspection, inspectErr := inspectPath(path, maxText)
	issues := make([]string, 0)
	warnings := make([]string, 0)
	previewChecks := make([]map[string]any, 0)
	if inspectErr != nil {
		issues = append(issues, inspectErr.Error())
	} else {
		switch inspection["format"] {
		case "DOCX":
			if asInt(inspection["paragraphs"]) < 2 {
				issues = append(issues, "Word 文档正文过少")
			}
			if strings.TrimSpace(fmt.Sprint(inspection["text_preview"])) == "" {
				issues = append(issues, "Word 文档没有可读取文本")
			}
		case "XLSX":
			if asInt(inspection["sheets"]) < 1 || asInt(inspection["cells"]) < 1 {
				issues = append(issues, "Excel 工作簿没有有效工作表或单元格")
			}
			if asInt(inspection["formulas"]) == 0 {
				warnings = append(warnings, "工作簿未包含公式；纯数据交付时可忽略")
			}
			if boolArg(args, "require_formulas", false) && asInt(inspection["formulas"]) == 0 {
				issues = append(issues, "Excel 任务要求公式，但工作簿没有公式")
			}
			if boolArg(args, "require_auto_filter", false) && asInt(inspection["auto_filters"]) < 1 {
				issues = append(issues, "Excel 任务要求筛选，但工作簿没有自动筛选")
			}
			if boolArg(args, "require_frozen_rows", false) && asInt(inspection["frozen_panes"]) < 1 {
				issues = append(issues, "Excel 任务要求冻结标题行，但工作簿没有冻结窗格")
			}
			if boolArg(args, "require_print_setup", false) && asInt(inspection["print_setups"]) < 1 {
				issues = append(issues, "Excel 任务要求打印设置，但至少一个适用工作表缺少打印页面设置")
			}
		case "PPTX":
			if asInt(inspection["slides"]) < 2 {
				warnings = append(warnings, "演示文稿仅有一页，可能缺少内容页")
			}
			if inspection["aspect_ratio"] != "16:9" {
				issues = append(issues, "演示文稿不是推荐的 16:9 比例")
			}
			if minimum := intArg(args, "min_slides", 0, 0, 200); minimum > 0 && asInt(inspection["slides"]) < minimum {
				issues = append(issues, fmt.Sprintf("演示文稿少于要求的 %d 页", minimum))
			}
			if openErr := validatePPTXOpenability(path); openErr != nil {
				issues = append(issues, "演示文稿无法由实际办公软件打开："+openErr.Error())
			}
		case "PDF":
			if asInt(inspection["pages"]) < 1 {
				issues = append(issues, "PDF 没有可识别页面")
			}
		}
	}
	if required, ok := args["required_text"].([]any); ok && inspectErr == nil {
		text := strings.ToLower(fmt.Sprint(inspection["text_preview"]))
		for _, item := range required {
			term := strings.TrimSpace(fmt.Sprint(item))
			if term != "" && !strings.Contains(text, strings.ToLower(term)) {
				issues = append(issues, "文档缺少要求文本："+term)
			}
		}
	}
	if boolArg(args, "require_source_fidelity", false) && inspectErr == nil {
		sourceRaw, sourceArgErr := stringArg(args, "source_path", true)
		if sourceArgErr != nil {
			issues = append(issues, "来源忠实度校验缺少 source_path")
		} else if inspection["format"] == "PDF" {
			issues = append(issues, "PDF 不支持直接来源文本对照；请校验其可编辑源文件并验证真实导出关系")
		} else if sourcePath, pathErr := resolveWorkspacePath(sourceRaw); pathErr != nil {
			issues = append(issues, "来源文件路径无效")
		} else {
			sourceText, sourceErr := sourceFidelityText(sourcePath)
			if sourceErr != nil {
				issues = append(issues, "无法读取来源文件："+sourceErr.Error())
			} else {
				for _, allowed := range stringValues(args["allowed_source_text"]) {
					sourceText += "\n" + allowed
				}
				targetText := fmt.Sprint(inspection["text_preview"])
				fidelity := evaluateSourceFidelity(sourceText, targetText)
				inspection["source_fidelity"] = fidelity
				if fidelity["passed"] != true {
					facts, _ := fidelity["unsupported_facts"].([]string)
					issues = append(issues, "文档包含来源未支持的事实信号："+strings.Join(facts, "、"))
				}
			}
		}
	}
	if boolArg(args, "require_pdf", false) {
		pdfRaw, _ := args["pdf_path"].(string)
		pdfPath, pathErr := resolveWorkspacePath(pdfRaw)
		if pathErr != nil {
			issues = append(issues, "缺少有效的 PDF 交付路径")
		} else if _, statErr := os.Stat(pdfPath); statErr != nil {
			issues = append(issues, "要求的 PDF 交付文件不存在")
		}
	}
	if boolArg(args, "require_preview", false) {
		previews, _ := args["preview_paths"].([]any)
		if len(previews) == 0 {
			issues = append(issues, "缺少预览图")
		}
		for _, rawPreview := range previews {
			preview, pathErr := resolveWorkspacePath(fmt.Sprint(rawPreview))
			if pathErr != nil {
				issues = append(issues, "预览图路径无效")
				continue
			}
			if info, statErr := os.Stat(preview); statErr != nil || info.Size() == 0 {
				issues = append(issues, "预览图不存在或为空："+relativeWorkspacePath(preview))
				continue
			}
			check, inspectErr := inspectPreviewPNG(preview)
			if inspectErr != nil {
				issues = append(issues, "预览图无有效页面内容："+relativeWorkspacePath(preview)+"："+inspectErr.Error())
				continue
			}
			previewChecks = append(previewChecks, check)
		}
	}
	status := "通过"
	if len(issues) > 0 {
		status = "不通过"
	} else if len(warnings) > 0 {
		status = "通过（有提醒）"
	}
	return map[string]any{"status": status, "deliverable": len(issues) == 0, "issues": issues, "warnings": warnings, "inspection": inspection, "preview_checks": previewChecks}, nil
}

func evaluateSourceFidelity(source, target string) map[string]any {
	sourceNormalized := normalizeSourceFidelityText(source)
	unsupported := make([]string, 0)
	seen := map[string]bool{}
	add := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(kind + ":" + value)
		if seen[key] {
			return
		}
		seen[key] = true
		unsupported = append(unsupported, kind+":"+value)
	}
	for _, item := range sourceFidelityPatterns {
		for _, value := range item.pattern.FindAllString(target, -1) {
			upper := strings.ToUpper(value)
			if item.name == "技术缩写" && sourceFidelityTechnicalAllowlist[upper] {
				continue
			}
			if !strings.Contains(sourceNormalized, normalizeSourceFidelityText(value)) {
				add(item.name, value)
			}
		}
	}
	targetLower := strings.ToLower(target)
	for _, term := range sourceFidelityClaimTerms {
		if strings.Contains(targetLower, strings.ToLower(term)) && !strings.Contains(sourceNormalized, normalizeSourceFidelityText(term)) {
			add("结论词", term)
		}
	}
	sort.Strings(unsupported)
	return map[string]any{
		"passed":            len(unsupported) == 0,
		"unsupported_facts": unsupported,
		"checks":            len(sourceFidelityPatterns) + 1,
	}
}

func normalizeSourceFidelityText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), ""))
}

func stringValues(value any) []string {
	var values []string
	switch raw := value.(type) {
	case []string:
		values = append(values, raw...)
	case []any:
		for _, item := range raw {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, text)
			}
		}
	}
	return values
}

func sourceFidelityText(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".docx" || ext == ".xlsx" || ext == ".pptx" {
		inspection, err := inspectPath(path, 50000)
		if err != nil {
			return "", err
		}
		return fmt.Sprint(inspection["text_preview"]), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > 2*1024*1024 {
		return "", fmt.Errorf("来源文件超过 2 MiB 忠实度校验上限")
	}
	return string(data), nil
}

func replaceOfficeText(args map[string]any) (map[string]any, error) {
	raw, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(raw)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".docx" && ext != ".xlsx" && ext != ".pptx" {
		return nil, fmt.Errorf("文本替换仅支持 DOCX、XLSX 和 PPTX")
	}
	expected, err := stringArg(args, "expected_sha256", true)
	if err != nil {
		return nil, err
	}
	actual, _, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(actual, expected) {
		return nil, fmt.Errorf("文件已被其他操作修改：SHA-256 不匹配")
	}
	var replacements []templateReplacement
	if rawReplacements, ok := args["replacements"]; ok && rawReplacements != nil {
		if err := remarshal(rawReplacements, &replacements); err != nil {
			return nil, err
		}
	} else {
		oldText, err := stringArg(args, "old_text", true)
		if err != nil {
			return nil, err
		}
		newText, _ := args["new_text"].(string)
		replacements = []templateReplacement{{
			OldText:             oldText,
			NewText:             newText,
			ExpectedOccurrences: intArg(args, "expected_occurrences", 1, 1, 1000),
		}}
	}
	if len(replacements) == 0 {
		return nil, fmt.Errorf("replacements 不能为空")
	}
	entries, err := readZipEntries(path, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	reports := make([]map[string]any, 0, len(replacements))
	totalOccurrences := 0
	for index, replacement := range replacements {
		replacement.OldText = strings.TrimSpace(replacement.OldText)
		if replacement.OldText == "" {
			return nil, fmt.Errorf("第 %d 个修改的 old_text 不能为空", index+1)
		}
		expectedOccurrences := replacement.ExpectedOccurrences
		if expectedOccurrences == 0 {
			expectedOccurrences = 1
		}
		occurrences := 0
		for name, data := range entries {
			if !strings.HasSuffix(strings.ToLower(name), ".xml") {
				continue
			}
			updated, count, replaceErr := replaceOOXMLText(string(data), replacement.OldText, replacement.NewText, 1000-occurrences)
			if replaceErr != nil {
				return nil, replaceErr
			}
			if count > 0 {
				occurrences += count
				entries[name] = []byte(updated)
			}
		}
		if occurrences != expectedOccurrences {
			return nil, fmt.Errorf("第 %d 个修改匹配 %d 处，与 expected_occurrences=%d 不一致；未修改文件", index+1, occurrences, expectedOccurrences)
		}
		totalOccurrences += occurrences
		reports = append(reports, map[string]any{
			"old_text":    replacement.OldText,
			"new_text":    replacement.NewText,
			"meaning":     replacement.Meaning,
			"occurrences": occurrences,
		})
	}
	backupDir := filepath.Join(workspaceRoot, ".goflow", "backups", "office")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}
	backup := filepath.Join(backupDir, filepath.Base(path)+"."+actual[:12]+".bak")
	if err := copyFile(path, backup); err != nil {
		return nil, err
	}
	data, err := zipBytes(entries)
	if err != nil {
		return nil, err
	}
	target := writeTarget{path: path, mode: 0o644}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	payload, err := artifactPayload(path, mimeForExtension(ext), map[string]any{
		"replacements":         totalOccurrences,
		"replacement_count":    len(replacements),
		"modifications":        reports,
		"backup_path":          relativeWorkspacePath(backup),
		"source_sha256_before": actual,
	})
	return payload, err
}

type templateReplacement struct {
	OldText             string `json:"old_text"`
	NewText             string `json:"new_text"`
	ExpectedOccurrences int    `json:"expected_occurrences"`
	Meaning             string `json:"meaning"`
}

func fillOfficeTemplate(args map[string]any) (map[string]any, error) {
	rawSource, err := stringArg(args, "source_path", true)
	if err != nil {
		return nil, err
	}
	source, err := resolveWorkspacePath(rawSource)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(source))
	if ext != ".docx" && ext != ".xlsx" && ext != ".pptx" {
		return nil, fmt.Errorf("模板仅支持 DOCX、XLSX 和 PPTX")
	}
	expectedSource, err := stringArg(args, "expected_source_sha256", false)
	if err != nil {
		return nil, err
	}
	actualSource, _, err := fileSHA256(source)
	if err != nil {
		return nil, err
	}
	if expectedSource != "" && !strings.EqualFold(actualSource, expectedSource) {
		return nil, fmt.Errorf("模板已被其他操作修改：SHA-256 不匹配")
	}
	rawOutput, err := stringArg(args, "output_path", true)
	if err != nil {
		return nil, err
	}
	output, err := resolveWorkspacePath(rawOutput)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(source, output) {
		return nil, fmt.Errorf("模板输出必须是新路径；原地修改请使用 replace_office_text")
	}
	if !strings.EqualFold(filepath.Ext(output), ext) {
		return nil, fmt.Errorf("输出文件必须与模板使用相同扩展名")
	}
	expectedOutput, _ := args["expected_output_sha256"].(string)
	outputArgs := map[string]any{
		"path":            rawOutput,
		"overwrite":       boolArg(args, "overwrite", false),
		"expected_sha256": strings.TrimSpace(expectedOutput),
	}
	target, err := prepareWriteTarget(outputArgs, ext)
	if err != nil {
		return nil, err
	}
	var replacements []templateReplacement
	if err := remarshal(args["replacements"], &replacements); err != nil {
		return nil, err
	}
	replacements = mergeWorkspaceTemplateReplacements(source, replacements)
	if len(replacements) == 0 {
		return nil, fmt.Errorf("replacements 不能为空")
	}
	entries, err := readZipEntries(source, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	reports := make([]map[string]any, 0, len(replacements))
	for index, replacement := range replacements {
		replacement.OldText = strings.TrimSpace(replacement.OldText)
		if replacement.OldText == "" {
			return nil, fmt.Errorf("第 %d 个修改的 old_text 不能为空", index+1)
		}
		expected := replacement.ExpectedOccurrences
		count := 0
		for name, data := range entries {
			if !strings.HasSuffix(strings.ToLower(name), ".xml") {
				continue
			}
			updated, matches, replaceErr := replaceOOXMLText(string(data), replacement.OldText, replacement.NewText, 1000-count)
			if replaceErr != nil {
				return nil, replaceErr
			}
			if matches > 0 {
				entries[name] = []byte(updated)
				count += matches
			}
		}
		if count == 0 && strings.Contains(replacement.Meaning, "workspace merge-map") {
			continue
		}
		if expected == 0 {
			if count == 0 {
				return nil, fmt.Errorf("第 %d 个修改未匹配到 %q；未生成文件", index+1, replacement.OldText)
			}
		} else if count != expected {
			return nil, fmt.Errorf("第 %d 个修改匹配 %d 处，与 expected_occurrences=%d 不一致；未生成文件", index+1, count, expected)
		}
		reports = append(reports, map[string]any{
			"old_text":    replacement.OldText,
			"new_text":    replacement.NewText,
			"meaning":     replacement.Meaning,
			"occurrences": count,
		})
	}
	data, err := zipBytes(entries)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	extra := map[string]any{
		"format":            strings.ToUpper(strings.TrimPrefix(ext, ".")),
		"template_path":     relativeWorkspacePath(source),
		"template_sha256":   actualSource,
		"replacements":      reports,
		"replacement_count": len(replacements),
	}
	if target.backupPath != "" {
		extra["backup_path"] = relativeWorkspacePath(target.backupPath)
	}
	return artifactPayload(target.path, mimeForExtension(ext), extra)
}

type officeTextNode struct {
	fullStart  int
	fullEnd    int
	innerStart int
	innerEnd   int
	textStart  int
	textEnd    int
	text       string
}

func replaceOOXMLText(document, oldText, newText string, limit int) (string, int, error) {
	if oldText == "" {
		return document, 0, fmt.Errorf("old_text 不能为空")
	}
	if limit <= 0 {
		return document, 0, fmt.Errorf("替换数量超过限制")
	}
	marker := fmt.Sprintf("__GOFLOW_OFFICE_%x__", sha256Hex([]byte(oldText + newText + document))[:16])
	for strings.Contains(document, marker) {
		marker += "X"
	}
	count := 0
	for count < limit {
		nodes, combined := collectOfficeTextNodes(document)
		matchStart := strings.Index(combined, oldText)
		if matchStart < 0 {
			break
		}
		matchEnd := matchStart + len(oldText)
		updated, ok := replaceOfficeTextSpan(document, nodes, matchStart, matchEnd, marker)
		if !ok {
			return document, count, fmt.Errorf("无法安全定位 Office 文本节点")
		}
		document = updated
		count++
	}
	if count == limit {
		_, combined := collectOfficeTextNodes(document)
		if strings.Contains(combined, oldText) {
			return document, count, fmt.Errorf("替换数量超过 %d 处限制", limit)
		}
	}
	document = strings.ReplaceAll(document, xmlEscape(marker), xmlEscape(newText))
	return document, count, nil
}

func collectOfficeTextNodes(document string) ([]officeTextNode, string) {
	matches := officeTextNodePattern.FindAllStringSubmatchIndex(document, -1)
	nodes := make([]officeTextNode, 0, len(matches))
	var combined strings.Builder
	for _, match := range matches {
		if len(match) < 8 {
			continue
		}
		text := html.UnescapeString(document[match[4]:match[5]])
		start := combined.Len()
		combined.WriteString(text)
		nodes = append(nodes, officeTextNode{
			fullStart: match[0], fullEnd: match[1], innerStart: match[4], innerEnd: match[5],
			textStart: start, textEnd: combined.Len(), text: text,
		})
	}
	return nodes, combined.String()
}

func replaceOfficeTextSpan(document string, nodes []officeTextNode, matchStart, matchEnd int, replacement string) (string, bool) {
	startNode, endNode := -1, -1
	for index, node := range nodes {
		if startNode < 0 && matchStart >= node.textStart && matchStart < node.textEnd {
			startNode = index
		}
		if matchEnd > node.textStart && matchEnd <= node.textEnd {
			endNode = index
			break
		}
	}
	if startNode < 0 || endNode < startNode {
		return document, false
	}
	updatedTexts := make(map[int]string, endNode-startNode+1)
	startOffset := matchStart - nodes[startNode].textStart
	endOffset := matchEnd - nodes[endNode].textStart
	if startNode == endNode {
		text := nodes[startNode].text
		updatedTexts[startNode] = text[:startOffset] + replacement + text[endOffset:]
	} else {
		updatedTexts[startNode] = nodes[startNode].text[:startOffset] + replacement
		for index := startNode + 1; index < endNode; index++ {
			updatedTexts[index] = ""
		}
		updatedTexts[endNode] = nodes[endNode].text[endOffset:]
	}
	var out strings.Builder
	cursor := 0
	for index, node := range nodes {
		updated, ok := updatedTexts[index]
		if !ok {
			continue
		}
		out.WriteString(document[cursor:node.innerStart])
		out.WriteString(xmlEscape(updated))
		cursor = node.innerEnd
	}
	out.WriteString(document[cursor:])
	return out.String(), true
}

func readZipEntries(path string, maxTotal int64) (map[string][]byte, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开 Office 文档包：%w", err)
	}
	defer reader.Close()
	entries := make(map[string][]byte, len(reader.File))
	var total int64
	for _, file := range reader.File {
		if file.UncompressedSize64 > uint64(maxTotal) {
			return nil, fmt.Errorf("文档内部文件过大")
		}
		total += int64(file.UncompressedSize64)
		if total > maxTotal {
			return nil, fmt.Errorf("文档解压后内容超过限制")
		}
		stream, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxTotal+1))
		_ = stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		entries[filepath.ToSlash(file.Name)] = data
	}
	return entries, nil
}

func zipBytes(entries map[string][]byte) ([]byte, error) {
	textEntries := make(map[string]string, len(entries))
	for name, data := range entries {
		textEntries[name] = string(data)
	}
	return zipStrings(textEntries)
}

func parseXMLText(data []byte, textElements, countElements map[string]bool) (string, map[string]int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	counts := map[string]int{}
	var text strings.Builder
	textBoundaries := map[string]bool{"p": true, "sp": true, "tc": true, "c": true, "row": true, "si": true}
	appendBoundary := func() {
		if text.Len() == 0 {
			return
		}
		value := text.String()
		if value[len(value)-1] != '\n' {
			text.WriteByte('\n')
		}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if countElements[typed.Name.Local] {
				counts[typed.Name.Local]++
			}
			if textBoundaries[typed.Name.Local] {
				appendBoundary()
			}
			if textElements[typed.Name.Local] {
				var value string
				if err := decoder.DecodeElement(&value, &typed); err != nil {
					return "", nil, err
				}
				if strings.TrimSpace(value) != "" {
					text.WriteString(value)
				}
			}
		}
	}
	return strings.TrimSpace(text.String()), counts, nil
}

func xmlAttributeValues(data []byte, element, attribute string) []string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var values []string
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != element {
			continue
		}
		for _, attr := range start.Attr {
			if attr.Name.Local == attribute {
				values = append(values, attr.Value)
			}
		}
	}
	return values
}

func firstXMLAttribute(data []byte, element, attribute string) string {
	values := xmlAttributeValues(data, element, attribute)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func naturalSlideOrder(name string) int {
	base := filepath.Base(name)
	base = strings.TrimSuffix(strings.TrimPrefix(base, "slide"), ".xml")
	value := 0
	_, _ = fmt.Sscanf(base, "%d", &value)
	return value
}

func aspectRatioLabel(cx, cy string) string {
	if cx == "12192000" && cy == "6858000" {
		return "16:9"
	}
	return cx + ":" + cy
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func asInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o644)
}

func mimeForExtension(ext string) string {
	switch strings.ToLower(ext) {
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
