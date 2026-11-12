package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

var workspaceRoot = mustWorkspaceRoot()

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch req.Method {
		case "tools/list":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: callTool(req.Params)})
		default:
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{
		{
			Name:        "office_capabilities",
			Description: "探测本机 WPS、Microsoft Office、LibreOffice 和浏览器渲染能力，返回可用的文档转换与预览路径。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "create_docx",
			Description: "根据中文结构化内容创建样式完整的 DOCX 文档，自动配置标题、正文、项目符号、表格、页边距和字体。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"title":{"type":"string","minLength":1},"subtitle":{"type":"string"},"author":{"type":"string"},"sections":{"type":"array","minItems":1,"maxItems":80,"items":{"type":"object","properties":{"title":{"type":"string","minLength":1},"paragraphs":{"type":"array","items":{"type":"string"},"maxItems":100},"bullets":{"type":"array","items":{"type":"string"},"maxItems":100},"tables":{"type":"array","maxItems":20,"items":{"type":"object","properties":{"title":{"type":"string"},"headers":{"type":"array","items":{"type":"string"},"maxItems":20},"rows":{"type":"array","maxItems":200,"items":{"type":"array","items":{"type":"string"},"maxItems":20}}},"required":["headers","rows"],"additionalProperties":false}}},"required":["title"],"additionalProperties":false}},"overwrite":{"type":"boolean"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","title","sections"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "create_xlsx",
			Description: "根据结构化行列数据创建带表头、筛选、冻结窗格、列宽和公式支持的 XLSX 工作簿。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"author":{"type":"string"},"sheets":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{"name":{"type":"string","minLength":1},"rows":{"type":"array","minItems":1,"maxItems":5000,"items":{"type":"array","maxItems":100,"items":{}}},"header_rows":{"type":"integer","minimum":0,"maximum":10},"freeze_rows":{"type":"integer","minimum":0,"maximum":20},"auto_filter":{"type":"boolean"}},"required":["name","rows"],"additionalProperties":false}},"overwrite":{"type":"boolean"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","sheets"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "create_pptx",
			Description: "根据结构化提纲创建 16:9 PPTX 演示文稿，自动应用简洁主题、标题层级、要点布局和页码。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"title":{"type":"string","minLength":1},"subtitle":{"type":"string"},"author":{"type":"string"},"slides":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{"title":{"type":"string","minLength":1},"subtitle":{"type":"string"},"bullets":{"type":"array","items":{"type":"string"},"maxItems":20},"notes":{"type":"string"}},"required":["title"],"additionalProperties":false}},"overwrite":{"type":"boolean"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","title","slides"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "create_pdf",
			Description: "仅在没有可编辑 Office 源文件时，根据标题和分节文本创建独立中文 PDF。已有或同时创建 DOCX、XLSX、PPTX 时不得使用本工具，必须调用 export_office_pdf 从最终源文件导出。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"title":{"type":"string","minLength":1},"sections":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{"title":{"type":"string","minLength":1},"lines":{"type":"array","items":{"type":"string"},"maxItems":200}},"required":["title","lines"],"additionalProperties":false}},"overwrite":{"type":"boolean"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","title","sections"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "inspect_office_document",
			Description: "检查 DOCX、XLSX、PPTX 或 PDF 的结构、文本、页表幻灯片数量、公式和文件哈希，不执行宏。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"max_text_chars":{"type":"integer","minimum":500,"maximum":50000}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "validate_office_document",
			Description: "对 Word、Excel、PPT 或 PDF 执行格式专项校验；可将文档中的编号、日期、风险术语和绝对化结论与输入源对照，阻止无依据扩写。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"require_pdf":{"type":"boolean"},"require_preview":{"type":"boolean"},"pdf_path":{"type":"string"},"preview_paths":{"type":"array","items":{"type":"string"},"maxItems":50},"require_formulas":{"type":"boolean"},"require_auto_filter":{"type":"boolean"},"require_frozen_rows":{"type":"boolean"},"require_print_setup":{"type":"boolean"},"min_slides":{"type":"integer","minimum":0,"maximum":200},"required_text":{"type":"array","items":{"type":"string","minLength":1},"maxItems":50},"source_path":{"type":"string","minLength":1},"require_source_fidelity":{"type":"boolean"},"allowed_source_text":{"type":"array","items":{"type":"string","minLength":1},"maxItems":100}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "replace_office_text",
			Description: "在校验原文件 SHA-256 后原子执行一处或多处 DOCX、XLSX、PPTX 精确文本修改，并自动保留修改前备份；任一修改不匹配时不会写入文件。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"expected_occurrences":{"type":"integer","minimum":1,"maximum":1000},"replacements":{"type":"array","minItems":1,"maxItems":200,"items":{"type":"object","properties":{"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"expected_occurrences":{"type":"integer","minimum":1,"maximum":1000},"meaning":{"type":"string"}},"required":["old_text","new_text"],"additionalProperties":false}}},"required":["path","expected_sha256"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "fill_office_template",
			Description: "基于现有 DOCX、XLSX 或 PPTX 模板生成新文件，原子执行多处精确替换，并保留模板样式、母版、公式和未修改内容。expected_source_sha256 可选；工作区 merge-map 中不存在的占位符会被跳过而不是整单失败。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"source_path":{"type":"string","minLength":1},"output_path":{"type":"string","minLength":1},"expected_source_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"replacements":{"type":"array","minItems":1,"maxItems":200,"items":{"type":"object","properties":{"old_text":{"type":"string","minLength":1},"new_text":{"type":"string"},"expected_occurrences":{"type":"integer","minimum":0,"maximum":1000},"meaning":{"type":"string"}},"required":["old_text","new_text"],"additionalProperties":false}},"overwrite":{"type":"boolean"},"expected_output_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["source_path","output_path","replacements"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "read_xlsx",
			Description: "读取 XLSX 工作表的行列值、公式和表名，用于核对或再修改，不执行宏。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"sheet":{"type":"string"},"max_rows":{"type":"integer","minimum":1,"maximum":5000}},"required":["path"],"additionalProperties":false}`),
			Kind:        "read",
		},
		{
			Name:        "update_xlsx_cells",
			Description: "在现有 XLSX 中写入指定单元格，保留其他单元格、公式、筛选、冻结和打印设置。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"sheet":{"type":"string","minLength":1},"cells":{"type":"array","minItems":1,"maxItems":500,"items":{"type":"object","properties":{"address":{"type":"string","minLength":2},"value":{}},"required":["address","value"],"additionalProperties":false}},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"overwrite":{"type":"boolean"}},"required":["path","sheet","cells"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "merge_csv_into_xlsx",
			Description: "把 CSV 合并进现有 XLSX 指定工作表，覆盖从 start_row 起的数据行，并保留公式列所在其他工作表、筛选、冻结和打印设置。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"csv_path":{"type":"string","minLength":1},"sheet":{"type":"string"},"start_row":{"type":"integer","minimum":1,"maximum":5000},"include_header":{"type":"boolean"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","csv_path"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "export_office_pdf",
			Description: "使用本机 WPS、Microsoft Office 或 LibreOffice 将最终 DOCX、XLSX、PPTX 导出为一致的 PDF；当任务同时要求可编辑源文件和 PDF 时必须使用本工具。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"output_path":{"type":"string","minLength":1},"converter":{"type":"string","enum":["auto","wps","microsoft","libreoffice"]},"overwrite":{"type":"boolean"}},"required":["path","output_path"],"additionalProperties":false}`),
			Kind:        "write",
		},
		{
			Name:        "render_office_preview",
			Description: "将办公文档或 PDF 渲染为预览图；优先使用 Office 或浏览器真实渲染，无法渲染时返回明确阻塞原因。",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"output_dir":{"type":"string","minLength":1},"max_pages":{"type":"integer","minimum":1,"maximum":20},"width":{"type":"integer","minimum":640,"maximum":2400},"height":{"type":"integer","minimum":480,"maximum":1800}},"required":["path","output_dir"],"additionalProperties":false}`),
			Kind:        "write",
		},
	}
}

func callTool(params json.RawMessage) map[string]any {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return toolError(err)
	}
	var payload map[string]any
	var err error
	switch input.Name {
	case "office_capabilities":
		payload = officeCapabilities()
	case "create_docx":
		payload, err = createDOCX(input.Arguments)
	case "create_xlsx":
		payload, err = createXLSX(input.Arguments)
	case "create_pptx":
		payload, err = createPPTX(input.Arguments)
	case "create_pdf":
		payload, err = createPDF(input.Arguments)
	case "inspect_office_document":
		payload, err = inspectOfficeDocument(input.Arguments)
	case "validate_office_document":
		payload, err = validateOfficeDocument(input.Arguments)
	case "replace_office_text":
		payload, err = replaceOfficeText(input.Arguments)
	case "fill_office_template":
		payload, err = fillOfficeTemplate(input.Arguments)
	case "read_xlsx":
		payload, err = readXLSXWorkbook(input.Arguments)
	case "update_xlsx_cells":
		payload, err = updateXLSXCells(input.Arguments)
	case "merge_csv_into_xlsx":
		payload, err = mergeCSVIntoXLSX(input.Arguments)
	case "export_office_pdf":
		payload, err = exportOfficePDF(input.Arguments)
	case "render_office_preview":
		payload, err = renderOfficePreview(input.Arguments)
	default:
		return map[string]any{"content": "未知工具", "is_error": true}
	}
	if err != nil {
		return toolError(err)
	}
	encoded, _ := json.Marshal(payload)
	return map[string]any{"content": string(encoded), "is_error": false}
}

func toolError(err error) map[string]any {
	return map[string]any{"content": err.Error(), "is_error": true}
}

func writeResponse(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}
