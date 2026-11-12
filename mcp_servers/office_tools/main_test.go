package main

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withTestWorkspace(t *testing.T) string {
	t.Helper()
	t.Setenv("GOFLOW_OFFICE_DISABLE_NATIVE", "1")
	t.Setenv("GOFLOW_OFFICE_ALLOW_EXPERIMENTAL_OOXML", "1")
	root := t.TempDir()
	previous := workspaceRoot
	workspaceRoot = root
	t.Cleanup(func() { workspaceRoot = previous })
	return root
}

func TestCreateAndInspectOfficeDocuments(t *testing.T) {
	root := withTestWorkspace(t)
	docx, err := createDOCX(map[string]any{
		"path": "交付/项目报告.docx", "title": "项目周报", "subtitle": "真实工程交付",
		"sections": []any{
			map[string]any{"title": "完成情况", "paragraphs": []string{"已完成核心功能并通过测试。"}, "bullets": []string{"功能闭环", "质量验收"}},
			map[string]any{"title": "风险", "tables": []any{map[string]any{"headers": []string{"风险", "措施"}, "rows": [][]string{{"接口变化", "保持兼容"}}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, docx, ".docx")
	docxInspection, err := inspectPath(filepath.Join(root, "交付", "项目报告.docx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if docxInspection["format"] != "DOCX" || asInt(docxInspection["tables"]) != 1 {
		t.Fatalf("unexpected DOCX inspection: %#v", docxInspection)
	}

	xlsx, err := createXLSX(map[string]any{
		"path": "交付/数据分析.xlsx",
		"sheets": []any{
			map[string]any{"name": "任务统计", "rows": []any{[]any{"任务", "数量", "完成率"}, []any{"开发", 12, 0.75}, []any{"总计", "=SUM(B2:B2)", "=AVERAGE(C2:C2)"}}},
			map[string]any{"name": "风险/清单", "rows": []any{[]any{"风险", "等级"}, []any{"服务不可用", "高"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, xlsx, ".xlsx")
	xlsxInspection, err := inspectPath(filepath.Join(root, "交付", "数据分析.xlsx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if asInt(xlsxInspection["sheets"]) != 2 || asInt(xlsxInspection["formulas"]) != 2 ||
		asInt(xlsxInspection["auto_filters"]) != 2 || asInt(xlsxInspection["frozen_panes"]) != 2 ||
		asInt(xlsxInspection["print_setups"]) != 2 || asInt(xlsxInspection["landscape_sheets"]) != 2 ||
		asInt(xlsxInspection["fit_to_page_sheets"]) != 2 {
		t.Fatalf("unexpected XLSX inspection: %#v", xlsxInspection)
	}
	xlsxValidation, err := validateOfficeDocument(map[string]any{
		"path": "交付/数据分析.xlsx", "require_formulas": true, "require_auto_filter": true,
		"require_frozen_rows": true, "require_print_setup": true, "required_text": []any{"开发", "服务不可用"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if xlsxValidation["deliverable"] != true {
		t.Fatalf("expected print-ready XLSX validation, got %#v", xlsxValidation)
	}
	legacyEntries, err := readZipEntries(filepath.Join(root, "交付", "数据分析.xlsx"), 32*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range legacyEntries {
		if !strings.HasPrefix(name, "xl/worksheets/sheet") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		content := string(data)
		content = strings.ReplaceAll(content, `<sheetPr><pageSetUpPr fitToPage="1" autoPageBreaks="0"/></sheetPr>`, "")
		content = strings.ReplaceAll(content, `<pageSetup paperSize="9" orientation="landscape" fitToWidth="1" fitToHeight="0"/>`, "")
		legacyEntries[name] = []byte(content)
	}
	legacyData, err := zipBytes(legacyEntries)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "交付", "缺少打印设置.xlsx"), legacyData, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyValidation, err := validateOfficeDocument(map[string]any{"path": "交付/缺少打印设置.xlsx", "require_print_setup": true})
	if err != nil {
		t.Fatal(err)
	}
	if legacyValidation["deliverable"] != false || !strings.Contains(strings.Join(stringSliceAny(legacyValidation["issues"]), " "), "缺少打印") {
		t.Fatalf("expected missing print setup to block delivery, got %#v", legacyValidation)
	}

	pptx, err := createPPTX(map[string]any{
		"path": "交付/方案汇报.pptx", "title": "本地智能体工作流平台", "subtitle": "方案汇报",
		"slides": []any{
			map[string]any{"title": "建设目标", "bullets": []string{"完成真实任务", "简化资源配置", "保证工程质量"}},
			map[string]any{"title": "验收结果", "bullets": []string{"产物可打开", "过程可追溯"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, pptx, ".pptx")
	pptxInspection, err := inspectPath(filepath.Join(root, "交付", "方案汇报.pptx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if asInt(pptxInspection["slides"]) != 3 || pptxInspection["aspect_ratio"] != "16:9" {
		t.Fatalf("unexpected PPTX inspection: %#v", pptxInspection)
	}

	pdf, err := createPDF(map[string]any{
		"path": "交付/交付说明.pdf", "title": "交付说明",
		"sections": []any{map[string]any{"title": "结论", "lines": []string{"四类办公文档均已生成。", "所有产物需要通过结构校验。"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, pdf, ".pdf")
	pdfInspection, err := inspectPath(filepath.Join(root, "交付", "交付说明.pdf"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if asInt(pdfInspection["pages"]) < 1 {
		t.Fatalf("unexpected PDF inspection: %#v", pdfInspection)
	}
}

func TestExportOfficePDFFallsBackToNativeText(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createDOCX(map[string]any{
		"path": "源文档.docx", "title": "原生导出验收",
		"sections": []any{map[string]any{"title": "事实", "paragraphs": []string{"SEC-001 高风险"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exportWithNativeTextFallback(filepath.Join(root, "源文档.docx"), filepath.Join(root, "源文档.pdf")); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectPath(filepath.Join(root, "源文档.pdf"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	pdfData, err := os.ReadFile(filepath.Join(root, "源文档.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if asInt(inspection["pages"]) < 1 || !bytes.Contains(pdfData, []byte(utf16BEHex("SEC-001"))) {
		t.Fatalf("native PDF fallback lost source content: %#v", inspection)
	}
	if created["sha256"] == "" {
		t.Fatalf("source artifact evidence missing: %#v", created)
	}
}

func TestOfficeWriteConflictBackupAndReplacement(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createDOCX(map[string]any{
		"path": "报告.docx", "title": "原始标题",
		"sections": []any{map[string]any{"title": "正文", "paragraphs": []string{"待替换内容"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := created["sha256"].(string)
	if _, err := createDOCX(map[string]any{
		"path": "报告.docx", "title": "覆盖标题", "overwrite": true, "expected_sha256": strings.Repeat("0", 64),
		"sections": []any{map[string]any{"title": "正文", "paragraphs": []string{"新内容"}}},
	}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected hash conflict, got %v", err)
	}
	replaced, err := replaceOfficeText(map[string]any{
		"path": "报告.docx", "expected_sha256": hash,
		"old_text": "待替换内容", "new_text": "已经替换", "expected_occurrences": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced["backup_path"] == "" {
		t.Fatalf("replacement did not return backup: %#v", replaced)
	}
	inspection, err := inspectPath(filepath.Join(root, "报告.docx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspection["text_preview"].(string), "已经替换") {
		t.Fatalf("replacement not visible: %#v", inspection)
	}
}

func TestReplaceOfficeTextAppliesMultipleChangesAtomically(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createDOCX(map[string]any{
		"path": "需求文档.docx", "title": "系统需求说明",
		"sections": []any{
			map[string]any{"title": "目标", "paragraphs": []string{"旧目标：仅支持创建文档。"}},
			map[string]any{"title": "验收", "paragraphs": []string{"旧验收：文件能够生成。"}},
			map[string]any{"title": "固定约束", "paragraphs": []string{"本段及既有样式必须保持不变。"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	originalHash := created["sha256"].(string)
	if _, err := replaceOfficeText(map[string]any{
		"path": "需求文档.docx", "expected_sha256": originalHash,
		"replacements": []any{
			map[string]any{"old_text": "旧目标：仅支持创建文档。", "new_text": "新目标：支持新建、模板生成和按意图修改文档。", "meaning": "更新能力目标"},
			map[string]any{"old_text": "不存在的验收文本", "new_text": "不得写入", "meaning": "模拟定位失败"},
		},
	}); err == nil || !strings.Contains(err.Error(), "未修改文件") {
		t.Fatalf("expected atomic batch rejection, got %v", err)
	}
	unchangedHash, _, err := fileSHA256(filepath.Join(root, "需求文档.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if unchangedHash != originalHash {
		t.Fatal("failed batch modification changed the source file")
	}

	modified, err := replaceOfficeText(map[string]any{
		"path": "需求文档.docx", "expected_sha256": originalHash,
		"replacements": []any{
			map[string]any{"old_text": "旧目标：仅支持创建文档。", "new_text": "新目标：支持新建、模板生成和按意图修改文档。", "meaning": "更新能力目标"},
			map[string]any{"old_text": "旧验收：文件能够生成。", "new_text": "新验收：文件可打开、可编辑、可预览并通过格式校验。", "meaning": "提升验收标准"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asInt(modified["replacement_count"]) != 2 || asInt(modified["replacements"]) != 2 || modified["backup_path"] == "" {
		t.Fatalf("unexpected batch modification evidence: %#v", modified)
	}
	inspection, err := inspectPath(filepath.Join(root, "需求文档.docx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	preview := inspection["text_preview"].(string)
	for _, expected := range []string{"支持新建、模板生成和按意图修改文档", "可预览并通过格式校验", "本段及既有样式必须保持不变"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("modified document missing %q: %s", expected, preview)
		}
	}
}

func TestFillOfficeTemplatePreservesSourceAndAppliesAtomicEdits(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createDOCX(map[string]any{
		"path": "模板.docx", "title": "{{报告标题}}",
		"sections": []any{
			map[string]any{"title": "项目结论", "paragraphs": []string{"{{结论内容}}"}},
			map[string]any{"title": "固定说明", "paragraphs": []string{"本段必须保持不变。"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	templateHash := created["sha256"].(string)
	filled, err := fillOfficeTemplate(map[string]any{
		"source_path": "模板.docx", "output_path": "交付/安全评估报告.docx",
		"expected_source_sha256": templateHash,
		"replacements": []any{
			map[string]any{"old_text": "{{报告标题}}", "new_text": "网络安全评估报告", "meaning": "填写正式报告名称"},
			map[string]any{"old_text": "{{结论内容}}", "new_text": "发现两项高风险问题，建议按整改清单修复。", "meaning": "写入安全评估结论"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, filled, ".docx")
	actualTemplateHash, _, err := fileSHA256(filepath.Join(root, "模板.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if actualTemplateHash != templateHash {
		t.Fatal("template source was modified")
	}
	inspection, err := inspectPath(filepath.Join(root, "交付", "安全评估报告.docx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	preview := inspection["text_preview"].(string)
	for _, expected := range []string{"网络安全评估报告", "两项高风险问题", "本段必须保持不变"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("filled document missing %q: %s", expected, preview)
		}
	}
}

func TestFillOfficeTemplateMergesWorkspaceRequestPlaceholders(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createDOCX(map[string]any{
		"path":  "template.docx",
		"title": "{{REPORT_TITLE}}",
		"sections": []any{
			map[string]any{"title": "客户", "paragraphs": []string{"{{CLIENT_NAME}} · {{REPORT_DATE}}"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inputs", "request.json"), []byte(`{"client":"星河科技","report_title":"Alpha 平台安全整改交付报告","report_date":"2026-08-18"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	filled, err := fillOfficeTemplate(map[string]any{
		"source_path": "template.docx", "output_path": "deliverables/report.docx",
		"expected_source_sha256": created["sha256"],
		"replacements": []any{
			map[string]any{"old_text": "{{CLIENT_NAME}}", "new_text": "星河科技"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, filled, ".docx")
	inspection, err := inspectPath(filepath.Join(root, "deliverables", "report.docx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	preview := inspection["text_preview"].(string)
	for _, expected := range []string{"星河科技", "Alpha 平台安全整改交付报告", "2026-08-18"} {
		if !strings.Contains(preview, expected) {
			t.Fatalf("merge-map fill missing %q: %s", expected, preview)
		}
	}
}

func TestReplaceOOXMLTextAcrossSplitRuns(t *testing.T) {
	document := `<w:p><w:r><w:t>需要更新的</w:t></w:r><w:r><w:t>完整结论</w:t></w:r></w:p>`
	updated, count, err := replaceOOXMLText(document, "需要更新的完整结论", "新的准确结论", 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(updated, "新的准确结论") || strings.Contains(updated, "完整结论") {
		t.Fatalf("unexpected split-run replacement: count=%d xml=%s", count, updated)
	}
}

func TestParseXMLTextJoinsSplitRunsWithinParagraph(t *testing.T) {
	document := []byte(`<p:sld xmlns:p="urn:p" xmlns:a="urn:a"><p:sp><a:p><a:r><a:t>SEC-</a:t></a:r><a:r><a:t>001</a:t></a:r></a:p></p:sp><p:sp><a:p><a:r><a:t>剩余风险</a:t></a:r></a:p></p:sp></p:sld>`)
	text, counts, err := parseXMLText(document, map[string]bool{"t": true}, map[string]bool{"sp": true})
	if err != nil {
		t.Fatal(err)
	}
	if counts["sp"] != 2 || !strings.Contains(text, "SEC-001") || !strings.Contains(text, "SEC-001\n剩余风险") {
		t.Fatalf("unexpected split-run text extraction: counts=%#v text=%q", counts, text)
	}
}

func TestValidateOfficeDocumentRejectsUnsupportedSourceClaims(t *testing.T) {
	root := withTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("SEC-001 高风险，参数化查询修复后公开与隐藏测试通过。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createDOCX(map[string]any{
		"path": "报告.docx", "title": "安全报告",
		"sections": []any{map[string]any{"title": "发现", "paragraphs": []string{"SEC-001 高风险：存在 SQL 注入漏洞，所有测试已完成。"}}},
	}); err != nil {
		t.Fatal(err)
	}
	validation, err := validateOfficeDocument(map[string]any{
		"path": "报告.docx", "source_path": "input.txt", "require_source_fidelity": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if validation["deliverable"] != false || !strings.Contains(strings.Join(stringSliceAny(validation["issues"]), " "), "来源未支持") {
		t.Fatalf("expected unsupported source claims to block delivery, got %#v", validation)
	}

	if _, err := createDOCX(map[string]any{
		"path": "忠实报告.docx", "title": "安全报告",
		"sections": []any{map[string]any{"title": "发现", "paragraphs": []string{"SEC-001 高风险，参数化查询修复后公开与隐藏测试通过。"}}},
	}); err != nil {
		t.Fatal(err)
	}
	validation, err = validateOfficeDocument(map[string]any{
		"path": "忠实报告.docx", "source_path": "input.txt", "require_source_fidelity": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if validation["deliverable"] != true {
		t.Fatalf("expected source-faithful document to pass, got %#v", validation)
	}
}

func TestValidateOfficeDocumentAcceptsApprovedTemplateReplacements(t *testing.T) {
	root := withTestWorkspace(t)
	if _, err := createDOCX(map[string]any{
		"path": "模板.docx", "title": "{{REPORT_TITLE}}",
		"sections": []any{map[string]any{"title": "固定章节", "paragraphs": []string{"固定内容"}}},
	}); err != nil {
		t.Fatal(err)
	}
	sourceHash, _, err := fileSHA256(filepath.Join(root, "模板.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fillOfficeTemplate(map[string]any{
		"source_path": "模板.docx", "output_path": "结果.docx", "expected_source_sha256": sourceHash,
		"replacements": []any{map[string]any{"old_text": "{{REPORT_TITLE}}", "new_text": "已批准的模板标题"}},
	}); err != nil {
		t.Fatal(err)
	}
	validation, err := validateOfficeDocument(map[string]any{
		"path": "结果.docx", "source_path": "模板.docx", "require_source_fidelity": true,
		"allowed_source_text": []any{"已批准的模板标题"},
		"required_text":       []any{"已批准的模板标题", "固定内容"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if validation["deliverable"] != true {
		t.Fatalf("approved template replacement should pass source fidelity: %#v", validation)
	}
}

func TestSourceFidelityRejectsUnsupportedEffectAndActionClaims(t *testing.T) {
	source := "SEC-001 高风险，参数化查询修复后公开与隐藏测试通过；DEV-001 功能验收通过；剩余风险为部署配置复核。"
	target := strings.Join([]string{
		"公开与隐藏测试通过，表明参数化查询修复措施有效。",
		"该功能已按设计要求完成开发并通过验收测试。",
		"部署环境配置可能存在未发现的安全隐患。",
		"建议措施：执行部署配置全面复核。",
		"状态：已通过；处置状态：待复核。",
	}, "\n")

	result := evaluateSourceFidelity(source, target)
	if result["passed"] != false {
		t.Fatalf("expected unsupported effect and action claims to fail, got %#v", result)
	}
	facts := strings.Join(result["unsupported_facts"].([]string), " ")
	for _, term := range []string{"有效", "按设计要求", "验收测试", "未发现", "安全隐患", "建议措施", "全面复核", "已通过", "待复核"} {
		if !strings.Contains(facts, term) {
			t.Fatalf("expected unsupported term %q in %#v", term, result)
		}
	}
}

func TestSourceFidelityAllowsExactAtomicFactsWithNeutralStructure(t *testing.T) {
	source := "SEC-001 高风险，参数化查询修复后公开与隐藏测试通过；DEV-001 功能验收通过；剩余风险为部署配置复核。"
	target := strings.Join([]string{
		"综合报告",
		"安全发现",
		"SEC-001｜高风险",
		"修复验证",
		"参数化查询修复后公开与隐藏测试通过",
		"开发验收",
		"DEV-001｜功能验收通过",
		"剩余风险",
		"部署配置复核",
	}, "\n")

	result := evaluateSourceFidelity(source, target)
	if result["passed"] != true {
		t.Fatalf("expected exact atomic facts with neutral structure to pass, got %#v", result)
	}
}

func TestOfficePathEscapeRejected(t *testing.T) {
	withTestWorkspace(t)
	_, err := createPDF(map[string]any{
		"path": "../逃逸.pdf", "title": "测试",
		"sections": []any{map[string]any{"title": "正文", "lines": []string{"内容"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "工作区") {
		t.Fatalf("expected workspace escape rejection, got %v", err)
	}
}

func TestInspectPreviewPNGRejectsBlankImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.png")
	blank := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			blank.Set(x, y, color.RGBA{R: 48, G: 48, B: 48, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, blank); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectPreviewPNG(path); err == nil || !strings.Contains(err.Error(), "单色") {
		t.Fatalf("expected blank preview rejection, got %v", err)
	}

	document := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			shade := uint8(250)
			if x > 80 && x < 560 && y > 70 && y < 410 && (x%37 < 4 || y%53 < 3) {
				shade = 35
			}
			document.Set(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	file, err = os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, document); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	check, err := inspectPreviewPNG(path)
	if err != nil {
		t.Fatal(err)
	}
	if asInt(check["luma_max"])-asInt(check["luma_min"]) < 15 {
		t.Fatalf("expected non-blank preview evidence, got %#v", check)
	}
}

func TestValidateOfficeDocumentRejectsBlankPreview(t *testing.T) {
	root := withTestWorkspace(t)
	_, err := createDOCX(map[string]any{
		"path": "报告.docx", "title": "验收报告",
		"sections": []any{map[string]any{"title": "结论", "paragraphs": []string{"文档内容有效。"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	previewPath := filepath.Join(root, "空白预览.png")
	blank := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			blank.Set(x, y, color.RGBA{R: 48, G: 48, B: 48, A: 255})
		}
	}
	file, err := os.Create(previewPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, blank); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	validation, err := validateOfficeDocument(map[string]any{
		"path": "报告.docx", "require_preview": true, "preview_paths": []any{"空白预览.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if validation["deliverable"] != false || !strings.Contains(strings.Join(stringSliceAny(validation["issues"]), " "), "无有效页面内容") {
		t.Fatalf("expected blank preview to block delivery: %#v", validation)
	}
}

func stringSliceAny(value any) []string {
	raw, _ := value.([]string)
	if raw != nil {
		return raw
	}
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

func TestGeneratedOOXMLHasRequiredEntries(t *testing.T) {
	root := withTestWorkspace(t)
	_, err := createPPTX(map[string]any{
		"path": "演示.pptx", "title": "演示",
		"slides": []any{map[string]any{"title": "内容", "bullets": []string{"一", "二"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(filepath.Join(root, "演示.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	required := map[string]bool{
		"[Content_Types].xml":               false,
		"ppt/presentation.xml":              false,
		"ppt/slides/slide1.xml":             false,
		"ppt/slideMasters/slideMaster1.xml": false,
		"ppt/theme/theme1.xml":              false,
	}
	for _, file := range reader.File {
		if _, ok := required[file.Name]; ok {
			required[file.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("missing %s", name)
		}
	}
}

func TestPPTXCreationUsesStructuredFallbackWithoutNativeOffice(t *testing.T) {
	root := withTestWorkspace(t)
	payload, err := createPPTX(map[string]any{
		"path": "后备演示.pptx", "title": "后备演示",
		"slides": []any{map[string]any{"title": "结论", "bullets": []string{"可编辑", "可校验"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload["generator"] != "Go OOXML" {
		t.Fatalf("unexpected PPTX generator: %#v", payload)
	}
	inspection, err := inspectPath(filepath.Join(root, "后备演示.pptx"), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if inspection["format"] != "PPTX" || asInt(inspection["slides"]) != 2 {
		t.Fatalf("fallback PPTX is not structurally valid: %#v", inspection)
	}
}

func TestGeneratedDOCXTableUsesReadableFixedColumnGrid(t *testing.T) {
	root := withTestWorkspace(t)
	_, err := createDOCX(map[string]any{
		"path": "表格报告.docx", "title": "安全报告",
		"sections": []any{map[string]any{
			"title": "发现清单",
			"tables": []any{map[string]any{
				"headers": []string{"编号", "风险", "等级", "整改建议"},
				"rows":    [][]string{{"SEC-001", "参数未绑定可能导致注入风险", "高", "使用参数化查询并执行公开与隐藏回归测试"}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := readZipEntries(filepath.Join(root, "表格报告.docx"), 8*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	document := string(entries["word/document.xml"])
	for _, expected := range []string{`<w:tblLayout w:type="fixed"/>`, `<w:tblGrid>`, `<w:tblCellMar>`, `<w:tblHeader/>`, `<w:cantSplit/>`, `<w:tcW w:w="`} {
		if !strings.Contains(document, expected) {
			t.Fatalf("generated DOCX table is missing %q: %s", expected, document)
		}
	}
	if strings.Count(document, `<w:gridCol `) != 4 || strings.Count(document, `<w:tcW `) != 8 {
		t.Fatalf("expected a four-column grid with widths on every cell: %s", document)
	}
	widths := docxTableColumnWidths([][]string{{"编号", "风险", "等级", "整改建议"}, {"SEC-001", "参数未绑定可能导致注入风险", "高", "使用参数化查询并执行公开与隐藏回归测试"}}, 9026)
	total := 0
	for _, width := range widths {
		total += width
	}
	if len(widths) != 4 || total != 9026 || widths[3] <= widths[2] {
		t.Fatalf("unexpected content-weighted table widths: %#v", widths)
	}
}

func TestOfficeCOMRoundTrip(t *testing.T) {
	if os.Getenv("GOFLOW_OFFICE_INTEGRATION") != "1" {
		t.Skip("set GOFLOW_OFFICE_INTEGRATION=1 to run installed Office and browser integration")
	}
	capabilities := detectOfficeCapabilities()
	if capabilities.WordPath == "" || capabilities.ExcelPath == "" || capabilities.PowerPointPath == "" {
		t.Skip("Microsoft Office is not fully installed")
	}
	root := withTestWorkspace(t)
	t.Setenv("GOFLOW_OFFICE_DISABLE_NATIVE", "0")
	t.Setenv("GOFLOW_OFFICE_ALLOW_EXPERIMENTAL_OOXML", "0")
	documents := []struct {
		name   string
		create func() error
	}{
		{name: "报告.docx", create: func() error {
			_, err := createDOCX(map[string]any{"path": "报告.docx", "title": "办公文档验收", "sections": []any{map[string]any{"title": "结果", "paragraphs": []string{"Word 文档可以真实打开并导出。"}}}})
			return err
		}},
		{name: "数据.xlsx", create: func() error {
			_, err := createXLSX(map[string]any{"path": "数据.xlsx", "sheets": []any{map[string]any{"name": "验收", "rows": []any{[]any{"项目", "结果"}, []any{"公式", "=1+1"}}}}})
			return err
		}},
		{name: "汇报.pptx", create: func() error {
			_, err := createPPTX(map[string]any{"path": "汇报.pptx", "title": "办公文档验收", "slides": []any{map[string]any{"title": "结果", "bullets": []string{"PPT 可以真实打开", "支持 PDF 导出"}}}})
			return err
		}},
	}
	for _, document := range documents {
		t.Run(document.name, func(t *testing.T) {
			if err := document.create(); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, document.name)
			output := filepath.Join(root, strings.TrimSuffix(document.name, filepath.Ext(document.name))+".pdf")
			if _, err := exportOfficePDFFile(source, output, "microsoft"); err != nil {
				t.Fatal(err)
			}
			inspection, err := inspectPath(output, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if asInt(inspection["pages"]) < 1 {
				t.Fatalf("exported PDF has no pages: %#v", inspection)
			}
		})
	}
	if capabilities.BrowserPath != "" {
		preview, err := renderOfficePreview(map[string]any{"path": "报告.docx", "output_dir": "预览", "width": 1024, "height": 768})
		if err != nil {
			t.Fatal(err)
		}
		paths, ok := preview["preview_paths"].([]string)
		if !ok || len(paths) != 1 {
			t.Fatalf("missing preview paths: %#v", preview)
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(paths[0]))); err != nil || info.Size() < 1000 {
			t.Fatalf("preview is missing or too small: %v", err)
		}
	}
}

func TestReadUpdateAndMergeXLSXPreserveFormulas(t *testing.T) {
	root := withTestWorkspace(t)
	created, err := createXLSX(map[string]any{
		"path": "账本.xlsx",
		"sheets": []any{
			map[string]any{
				"name": "明细",
				"rows": []any{
					[]any{"项目", "数量", "金额"},
					[]any{"占位", 1, 10},
				},
			},
			map[string]any{
				"name": "汇总",
				"rows": []any{
					[]any{"合计", "=SUM(明细!C2:C10)"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, created, ".xlsx")
	if err := os.WriteFile(filepath.Join(root, "入库.csv"), []byte("项目,数量,金额\n服务器,2,8000\n交换机,1,3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := mergeCSVIntoXLSX(map[string]any{
		"path": "账本.xlsx", "csv_path": "入库.csv", "sheet": "明细", "include_header": true, "start_row": 1,
		"expected_sha256": created["sha256"],
	})
	if err != nil {
		t.Fatal(err)
	}
	if asInt(merged["rows_written"]) != 3 {
		t.Fatalf("expected 3 csv rows written, got %#v", merged)
	}
	updated, err := updateXLSXCells(map[string]any{
		"path": "账本.xlsx", "sheet": "明细",
		"expected_sha256": merged["sha256"],
		"cells":           []any{map[string]any{"address": "B3", "value": 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asInt(updated["updated_cells"]) != 1 {
		t.Fatalf("expected one cell update, got %#v", updated)
	}
	readback, err := readXLSXWorkbook(map[string]any{"path": "账本.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	sheets, _ := readback["sheets"].([]map[string]any)
	if len(sheets) != 2 {
		t.Fatalf("expected two sheets, got %#v", readback)
	}
	detailRows, _ := sheets[0]["rows"].([][]string)
	if len(detailRows) < 3 || detailRows[1][0] != "服务器" || detailRows[2][1] != "5" {
		t.Fatalf("merged and updated detail rows missing: %#v", detailRows)
	}
	summaryRows, _ := sheets[1]["rows"].([][]string)
	if len(summaryRows) == 0 || !strings.Contains(summaryRows[0][1], "SUM(明细!C2:C10)") {
		t.Fatalf("summary formula must be preserved: %#v", summaryRows)
	}
	inspection, err := inspectPath(filepath.Join(root, "账本.xlsx"), 8000)
	if err != nil {
		t.Fatal(err)
	}
	if asInt(inspection["auto_filters"]) < 1 || asInt(inspection["frozen_panes"]) < 1 || asInt(inspection["print_setups"]) < 1 {
		t.Fatalf("filter/freeze/print setup must survive edits: %#v", inspection)
	}
}

func TestFillOfficeTemplateSkipsMissingWorkspacePlaceholders(t *testing.T) {
	root := withTestWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, "inputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inputs", "request.json"), []byte(`{"client":"星河科技","report_title":"月报"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := createDOCX(map[string]any{
		"path": "模板.docx", "title": "{{REPORT_TITLE}}",
		"sections": []any{map[string]any{"title": "客户", "paragraphs": []string{"客户：{{CLIENT_NAME}}"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	filled, err := fillOfficeTemplate(map[string]any{
		"source_path": "模板.docx",
		"output_path": "交付/月报.docx",
		"overwrite":   true,
		"replacements": []any{
			map[string]any{"old_text": "{{CLIENT_NAME}}", "new_text": "星河科技"},
			map[string]any{"old_text": "{{REPORT_TITLE}}", "new_text": "月报"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifact(t, root, filled, ".docx")
	inspection, err := inspectPath(filepath.Join(root, "交付", "月报.docx"), 4000)
	if err != nil {
		t.Fatal(err)
	}
	text := inspection["text_preview"].(string)
	if !strings.Contains(text, "星河科技") || !strings.Contains(text, "月报") {
		t.Fatalf("filled document missing values: %s", text)
	}
	_ = created
}

func assertArtifact(t *testing.T, root string, payload map[string]any, extension string) {
	t.Helper()
	relative, ok := payload["artifact_path"].(string)
	if !ok || !strings.HasSuffix(strings.ToLower(relative), extension) {
		t.Fatalf("invalid artifact payload: %#v", payload)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil || info.Size() == 0 {
		t.Fatalf("artifact missing or empty: %v", err)
	}
	if payload["sha256"] == "" || payload["size"] == nil {
		t.Fatalf("artifact evidence missing: %#v", payload)
	}
}
