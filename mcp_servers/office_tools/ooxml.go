package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type docxInput struct {
	Title    string        `json:"title"`
	Subtitle string        `json:"subtitle"`
	Author   string        `json:"author"`
	Sections []docxSection `json:"sections"`
}

type docxSection struct {
	Title      string      `json:"title"`
	Paragraphs []string    `json:"paragraphs"`
	Bullets    []string    `json:"bullets"`
	Tables     []docxTable `json:"tables"`
}

type docxTable struct {
	Title   string     `json:"title"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

type xlsxInput struct {
	Author string      `json:"author"`
	Sheets []xlsxSheet `json:"sheets"`
}

type xlsxSheet struct {
	Name       string  `json:"name"`
	Rows       [][]any `json:"rows"`
	HeaderRows int     `json:"header_rows"`
	FreezeRows int     `json:"freeze_rows"`
	AutoFilter *bool   `json:"auto_filter"`
}

type pptxInput struct {
	Title    string      `json:"title"`
	Subtitle string      `json:"subtitle"`
	Author   string      `json:"author"`
	Slides   []pptxSlide `json:"slides"`
}

type pptxSlide struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Bullets  []string `json:"bullets"`
	Notes    string   `json:"notes"`
}

func createDOCX(args map[string]any) (map[string]any, error) {
	target, err := prepareWriteTarget(args, ".docx")
	if err != nil {
		return nil, err
	}
	var input docxInput
	if err := remarshal(args, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" || len(input.Sections) == 0 {
		return nil, fmt.Errorf("title 和 sections 不能为空")
	}
	data, err := buildDOCX(input)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	paragraphs, tables := 0, 0
	for _, section := range input.Sections {
		paragraphs += 1 + len(section.Paragraphs) + len(section.Bullets)
		tables += len(section.Tables)
	}
	extra := map[string]any{"format": "DOCX", "sections": len(input.Sections), "paragraphs": paragraphs, "tables": tables}
	if target.backupPath != "" {
		extra["backup_path"] = relativeWorkspacePath(target.backupPath)
	}
	return artifactPayload(target.path, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", extra)
}

func createXLSX(args map[string]any) (map[string]any, error) {
	target, err := prepareWriteTarget(args, ".xlsx")
	if err != nil {
		return nil, err
	}
	var input xlsxInput
	if err := remarshal(args, &input); err != nil {
		return nil, err
	}
	if len(input.Sheets) == 0 {
		return nil, fmt.Errorf("sheets 不能为空")
	}
	data, normalized, formulaCount, err := buildXLSX(input)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	extra := map[string]any{
		"format":        "XLSX",
		"sheets":        normalized,
		"formula_count": formulaCount,
		"print_ready":   true,
		"print_setup":   "A4 landscape, fit to one page wide",
	}
	if target.backupPath != "" {
		extra["backup_path"] = relativeWorkspacePath(target.backupPath)
	}
	return artifactPayload(target.path, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", extra)
}

func createPPTX(args map[string]any) (map[string]any, error) {
	target, err := prepareWriteTarget(args, ".pptx")
	if err != nil {
		return nil, err
	}
	var input pptxInput
	if err := remarshal(args, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" || len(input.Slides) == 0 {
		return nil, fmt.Errorf("title 和 slides 不能为空")
	}
	data, generator, err := createPPTXBytes(input)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	extra := map[string]any{"format": "PPTX", "slides": len(input.Slides) + 1, "aspect_ratio": "16:9", "generator": generator}
	if target.backupPath != "" {
		extra["backup_path"] = relativeWorkspacePath(target.backupPath)
	}
	return artifactPayload(target.path, "application/vnd.openxmlformats-officedocument.presentationml.presentation", extra)
}

func createPPTXBytes(input pptxInput) ([]byte, string, error) {
	capabilities := detectOfficeCapabilities()
	if os.Getenv("GOFLOW_OFFICE_DISABLE_NATIVE") != "1" && capabilities.PowerPointPath != "" {
		data, err := createPPTXWithPowerPoint(input)
		if err == nil {
			return data, "Microsoft PowerPoint", nil
		}
		if os.Getenv("GOFLOW_OFFICE_REQUIRE_NATIVE_PPTX") == "1" {
			return nil, "", fmt.Errorf("PowerPoint 原生创建失败：%w", err)
		}
	}
	data, err := buildPPTX(input)
	if err != nil {
		return nil, "", fmt.Errorf("创建 PPTX OOXML 失败：%w", err)
	}
	return data, "Go OOXML", nil
}

func remarshal(input any, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("参数格式错误：%w", err)
	}
	return nil
}

func xmlEscape(value string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(strings.TrimSpace(value)))
	return buf.String()
}

func buildDOCX(input docxInput) ([]byte, error) {
	var body strings.Builder
	body.WriteString(docxParagraph(input.Title, "Title", false))
	if input.Subtitle != "" {
		body.WriteString(docxParagraph(input.Subtitle, "Subtitle", false))
	}
	for _, section := range input.Sections {
		body.WriteString(docxParagraph(section.Title, "Heading1", false))
		for _, paragraph := range section.Paragraphs {
			body.WriteString(docxParagraph(paragraph, "Normal", false))
		}
		for _, bullet := range section.Bullets {
			body.WriteString(docxParagraph("• "+bullet, "Normal", true))
		}
		for _, table := range section.Tables {
			if table.Title != "" {
				body.WriteString(docxParagraph(table.Title, "Heading2", false))
			}
			body.WriteString(docxTableXML(table))
		}
	}
	body.WriteString(`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` + body.String() + `</w:body></w:document>`
	files := map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`,
		"_rels/.rels":                  packageRelationships("word/document.xml"),
		"docProps/core.xml":            coreProperties(input.Title, input.Author),
		"docProps/app.xml":             appProperties("Microsoft Office Word"),
		"word/document.xml":            document,
		"word/styles.xml":              docxStyles(),
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
	}
	return zipStrings(files)
}

func docxParagraph(text, style string, bullet bool) string {
	indent := ""
	if bullet {
		indent = `<w:ind w:left="420" w:hanging="220"/>`
	}
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/>` + indent + `<w:spacing w:after="140" w:line="360" w:lineRule="auto"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Aptos" w:hAnsi="Aptos" w:eastAsia="Microsoft YaHei"/></w:rPr><w:t xml:space="preserve">` + xmlEscape(text) + `</w:t></w:r></w:p>`
}

func docxTableXML(table docxTable) string {
	var out strings.Builder
	rows := make([][]string, 0, len(table.Rows)+1)
	rows = append(rows, table.Headers)
	rows = append(rows, table.Rows...)
	widths := docxTableColumnWidths(rows, 9026)
	out.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="9026" w:type="dxa"/><w:tblLayout w:type="fixed"/><w:tblCellMar><w:top w:w="90" w:type="dxa"/><w:left w:w="110" w:type="dxa"/><w:bottom w:w="90" w:type="dxa"/><w:right w:w="110" w:type="dxa"/></w:tblCellMar><w:tblLook w:val="04A0"/></w:tblPr><w:tblGrid>`)
	for _, width := range widths {
		out.WriteString(fmt.Sprintf(`<w:gridCol w:w="%d"/>`, width))
	}
	out.WriteString(`</w:tblGrid>`)
	for rowIndex, row := range rows {
		rowProperties := `<w:trPr><w:cantSplit/></w:trPr>`
		if rowIndex == 0 {
			rowProperties = `<w:trPr><w:tblHeader/><w:cantSplit/></w:trPr>`
		}
		out.WriteString(`<w:tr>` + rowProperties)
		for columnIndex := range widths {
			cell := ""
			if columnIndex < len(row) {
				cell = row[columnIndex]
			}
			bold := ""
			shade := ""
			if rowIndex == 0 {
				bold = `<w:b/>`
				shade = `<w:shd w:val="clear" w:color="auto" w:fill="E8EEF7"/>`
			}
			out.WriteString(fmt.Sprintf(`<w:tc><w:tcPr><w:tcW w:w="%d" w:type="dxa"/><w:vAlign w:val="center"/>%s</w:tcPr><w:p><w:pPr><w:spacing w:after="40" w:line="300" w:lineRule="auto"/></w:pPr><w:r><w:rPr>%s<w:rFonts w:ascii="Aptos" w:hAnsi="Aptos" w:eastAsia="Microsoft YaHei"/><w:sz w:val="20"/><w:szCs w:val="20"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p></w:tc>`, widths[columnIndex], shade, bold, xmlEscape(cell)))
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl>`)
	return out.String()
}

func docxTableColumnWidths(rows [][]string, totalWidth int) []int {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return nil
	}
	weights := make([]int, columns)
	totalWeight := 0
	for column := 0; column < columns; column++ {
		weight := 6
		for _, row := range rows {
			if column >= len(row) {
				continue
			}
			length := 0
			for _, r := range []rune(row[column]) {
				if r > 127 {
					length += 2
				} else {
					length++
				}
			}
			if length > weight {
				weight = length
			}
		}
		if weight > 28 {
			weight = 28
		}
		weights[column] = weight
		totalWeight += weight
	}
	minimum := 720
	if minimum*columns > totalWidth {
		minimum = totalWidth / columns
	}
	remaining := totalWidth - minimum*columns
	widths := make([]int, columns)
	assigned := 0
	for column, weight := range weights {
		widths[column] = minimum
		if remaining > 0 && totalWeight > 0 {
			widths[column] += remaining * weight / totalWeight
		}
		assigned += widths[column]
	}
	widths[len(widths)-1] += totalWidth - assigned
	return widths
}

func docxStyles() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Aptos" w:hAnsi="Aptos" w:eastAsia="Microsoft YaHei"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:color w:val="111827"/><w:sz w:val="40"/><w:szCs w:val="40"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Subtitle"><w:name w:val="Subtitle"/><w:basedOn w:val="Normal"/><w:rPr><w:color w:val="4B5563"/><w:sz w:val="24"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:rPr><w:b/><w:color w:val="1F4B99"/><w:sz w:val="30"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:rPr><w:b/><w:color w:val="374151"/><w:sz w:val="25"/></w:rPr></w:style><w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:color="B8C2D1"/><w:left w:val="single" w:sz="4" w:color="B8C2D1"/><w:bottom w:val="single" w:sz="4" w:color="B8C2D1"/><w:right w:val="single" w:sz="4" w:color="B8C2D1"/><w:insideH w:val="single" w:sz="4" w:color="D7DEE8"/><w:insideV w:val="single" w:sz="4" w:color="D7DEE8"/></w:tblBorders></w:tblPr></w:style></w:styles>`
}

func buildXLSX(input xlsxInput) ([]byte, []string, int, error) {
	usedNames := map[string]bool{}
	names := make([]string, len(input.Sheets))
	formulaCount := 0
	files := map[string]string{
		"_rels/.rels":       packageRelationships("xl/workbook.xml"),
		"docProps/core.xml": coreProperties("工作簿", input.Author),
		"docProps/app.xml":  appProperties("Microsoft Office Excel"),
		"xl/styles.xml":     xlsxStyles(),
	}
	var sheetsXML, relsXML, overrides strings.Builder
	for i, sheet := range input.Sheets {
		name := uniqueSheetName(sheet.Name, usedNames)
		names[i] = name
		sheetsXML.WriteString(fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(name), i+1, i+1))
		relsXML.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1))
		overrides.WriteString(fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1))
		sheetXML, formulas, err := buildWorksheetXML(sheet)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("工作表 %s：%w", name, err)
		}
		formulaCount += formulas
		files[fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)] = sheetXML
	}
	relsXML.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`, len(input.Sheets)+1))
	files["xl/workbook.xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView/></bookViews><sheets>` + sheetsXML.String() + `</sheets><calcPr calcId="191029" fullCalcOnLoad="1"/></workbook>`
	files["xl/_rels/workbook.xml.rels"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + relsXML.String() + `</Relationships>`
	files["[Content_Types].xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` + overrides.String() + `<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`
	data, err := zipStrings(files)
	return data, names, formulaCount, err
}

func buildWorksheetXML(sheet xlsxSheet) (string, int, error) {
	if len(sheet.Rows) == 0 {
		return "", 0, fmt.Errorf("至少需要一行数据")
	}
	maxCols := 0
	for _, row := range sheet.Rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 || maxCols > 100 {
		return "", 0, fmt.Errorf("列数必须在 1 到 100 之间")
	}
	headerRows := sheet.HeaderRows
	if headerRows == 0 {
		headerRows = 1
	}
	freezeRows := sheet.FreezeRows
	if freezeRows == 0 {
		freezeRows = headerRows
	}
	autoFilter := true
	if sheet.AutoFilter != nil {
		autoFilter = *sheet.AutoFilter
	}
	var rows strings.Builder
	formulaCount := 0
	for rowIndex, row := range sheet.Rows {
		style := 0
		if rowIndex < headerRows {
			style = 1
		}
		rows.WriteString(fmt.Sprintf(`<row r="%d">`, rowIndex+1))
		for colIndex, value := range row {
			cellRef := columnName(colIndex+1) + strconv.Itoa(rowIndex+1)
			rows.WriteString(xlsxCellXML(cellRef, value, style, &formulaCount))
		}
		rows.WriteString(`</row>`)
	}
	var cols strings.Builder
	for col := 1; col <= maxCols; col++ {
		width := 14.0
		for _, row := range sheet.Rows {
			if col-1 < len(row) {
				width = math.Max(width, math.Min(40, float64(len([]rune(fmt.Sprint(row[col-1]))))+3))
			}
		}
		cols.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, col, col, width))
	}
	pane := ""
	if freezeRows > 0 {
		pane = fmt.Sprintf(`<pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/>`, freezeRows, freezeRows+1)
	}
	dimension := "A1:" + columnName(maxCols) + strconv.Itoa(len(sheet.Rows))
	filter := ""
	if autoFilter && len(sheet.Rows) > 1 {
		filter = `<autoFilter ref="A1:` + columnName(maxCols) + strconv.Itoa(len(sheet.Rows)) + `"/>`
	}
	printSetup := `<printOptions horizontalCentered="1" gridLines="0" headings="0"/><pageMargins left="0.25" right="0.25" top="0.5" bottom="0.5" header="0.2" footer="0.2"/><pageSetup paperSize="9" orientation="landscape" fitToWidth="1" fitToHeight="0"/>`
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetPr><pageSetUpPr fitToPage="1" autoPageBreaks="0"/></sheetPr><dimension ref="` + dimension + `"/><sheetViews><sheetView workbookViewId="0">` + pane + `</sheetView></sheetViews><sheetFormatPr defaultRowHeight="18"/><cols>` + cols.String() + `</cols><sheetData>` + rows.String() + `</sheetData>` + filter + printSetup + `</worksheet>`, formulaCount, nil
}

func xlsxCellXML(ref string, value any, style int, formulaCount *int) string {
	styleAttr := ""
	if style > 0 {
		styleAttr = fmt.Sprintf(` s="%d"`, style)
	}
	switch typed := value.(type) {
	case nil:
		return `<c r="` + ref + `"` + styleAttr + `/>`
	case float64:
		return `<c r="` + ref + `"` + styleAttr + `><v>` + strconv.FormatFloat(typed, 'f', -1, 64) + `</v></c>`
	case bool:
		v := "0"
		if typed {
			v = "1"
		}
		return `<c r="` + ref + `" t="b"` + styleAttr + `><v>` + v + `</v></c>`
	default:
		text := fmt.Sprint(value)
		if strings.HasPrefix(text, "=") && len(text) > 1 {
			*formulaCount++
			return `<c r="` + ref + `"` + styleAttr + `><f>` + xmlEscape(strings.TrimPrefix(text, "=")) + `</f><v>0</v></c>`
		}
		return `<c r="` + ref + `" t="inlineStr"` + styleAttr + `><is><t xml:space="preserve">` + xmlEscape(text) + `</t></is></c>`
	}
}

func xlsxStyles() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Microsoft YaHei"/><family val="2"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Microsoft YaHei"/><family val="2"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF1F4B99"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left style="thin"><color rgb="FFD7DEE8"/></left><right style="thin"><color rgb="FFD7DEE8"/></right><top style="thin"><color rgb="FFD7DEE8"/></top><bottom style="thin"><color rgb="FFD7DEE8"/></bottom><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="2"><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="1" fillId="2" borderId="1" xfId="0" applyFont="1" applyFill="1" applyBorder="1" applyAlignment="1"><alignment vertical="center" wrapText="1"/></xf></cellXfs><cellStyles count="1"><cellStyle name="常规" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
}

var invalidSheetChars = regexp.MustCompile(`[:\\/?*\[\]]`)

func uniqueSheetName(raw string, used map[string]bool) string {
	name := strings.TrimSpace(invalidSheetChars.ReplaceAllString(raw, "_"))
	if name == "" {
		name = "工作表"
	}
	runes := []rune(name)
	if len(runes) > 31 {
		name = string(runes[:31])
	}
	base := name
	for i := 2; used[strings.ToLower(name)]; i++ {
		suffix := fmt.Sprintf("_%d", i)
		limit := 31 - len([]rune(suffix))
		baseRunes := []rune(base)
		if len(baseRunes) > limit {
			baseRunes = baseRunes[:limit]
		}
		name = string(baseRunes) + suffix
	}
	used[strings.ToLower(name)] = true
	return name
}

func columnName(column int) string {
	var out string
	for column > 0 {
		column--
		out = string(rune('A'+column%26)) + out
		column /= 26
	}
	return out
}

func buildPPTX(input pptxInput) ([]byte, error) {
	slides := append([]pptxSlide{{Title: input.Title, Subtitle: input.Subtitle}}, input.Slides...)
	files := map[string]string{
		"_rels/.rels":                                  packageRelationships("ppt/presentation.xml"),
		"docProps/core.xml":                            coreProperties(input.Title, input.Author),
		"docProps/app.xml":                             appProperties("Microsoft Office PowerPoint"),
		"ppt/slideMasters/slideMaster1.xml":            pptxSlideMaster(),
		"ppt/slideMasters/_rels/slideMaster1.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>`,
		"ppt/slideLayouts/slideLayout1.xml":            pptxSlideLayout(),
		"ppt/slideLayouts/_rels/slideLayout1.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`,
		"ppt/theme/theme1.xml":                         pptxTheme(),
		"ppt/presProps.xml":                            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
		"ppt/viewProps.xml":                            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:viewPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:normalViewPr/><p:slideViewPr><p:cSldViewPr><p:cViewPr><p:scale><a:sx n="1" d="1"/><a:sy n="1" d="1"/></p:scale><p:origin x="0" y="0"/></p:cViewPr></p:cSldViewPr></p:slideViewPr><p:notesTextViewPr><p:cViewPr><p:scale><a:sx n="1" d="1"/><a:sy n="1" d="1"/></p:scale><p:origin x="0" y="0"/></p:cViewPr></p:notesTextViewPr><p:gridSpacing cx="72008" cy="72008"/></p:viewPr>`,
		"ppt/tableStyles.xml":                          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`,
	}
	var sldIDs, rels, overrides strings.Builder
	rels.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i, slide := range slides {
		rid := i + 2
		sldIDs.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 256+i, rid))
		rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, rid, i+1))
		overrides.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1))
		files[fmt.Sprintf("ppt/slides/slide%d.xml", i+1)] = pptxSlideXML(slide, i+1, i == 0)
		files[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1)] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>`
	}
	propertyRelID := len(slides) + 2
	rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps" Target="presProps.xml"/>`, propertyRelID))
	rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/viewProps" Target="viewProps.xml"/>`, propertyRelID+1))
	rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>`, propertyRelID+2))
	rels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles" Target="tableStyles.xml"/>`, propertyRelID+3))
	files["ppt/presentation.xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" saveSubsetFonts="1"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>` + sldIDs.String() + `</p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr><a:lvl1pPr marL="0" algn="l"><a:defRPr sz="1800"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill><a:latin typeface="+mn-lt"/><a:ea typeface="+mn-ea"/></a:defRPr></a:lvl1pPr></p:defaultTextStyle></p:presentation>`
	files["ppt/_rels/presentation.xml.rels"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels.String() + `</Relationships>`
	files["[Content_Types].xml"] = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/><Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/><Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/>` + overrides.String() + `<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`
	return zipStrings(files)
}

func pptxSlideXML(slide pptxSlide, page int, titleSlide bool) string {
	titleY, titleH, titleSize := 700000, 1000000, 3000
	bodyY := 1950000
	if titleSlide {
		titleY, titleH, titleSize, bodyY = 1750000, 1300000, 3600, 3300000
	}
	title := pptxTextShape(2, "标题", 850000, titleY, 10500000, titleH, slide.Title, titleSize, true, "1F2937")
	bodyText := slide.Subtitle
	if len(slide.Bullets) > 0 {
		parts := make([]string, 0, len(slide.Bullets))
		for _, bullet := range slide.Bullets {
			parts = append(parts, "• "+bullet)
		}
		bodyText = strings.Join(parts, "\n")
	}
	body := pptxTextShape(3, "正文", 950000, bodyY, 10100000, 2600000, bodyText, 1900, false, "374151")
	footer := pptxTextShape(4, "页码", 10800000, 6300000, 650000, 220000, strconv.Itoa(page), 900, false, "6B7280")
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="` + xmlEscape(slide.Title) + `"><p:bg><p:bgPr><a:solidFill><a:srgbClr val="F8FAFC"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` + title + body + footer + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
}

func pptxTextShape(id int, name string, x, y, cx, cy int, text string, size int, bold bool, color string) string {
	boldAttr := ""
	if bold {
		boldAttr = ` b="1"`
	}
	lines := strings.Split(text, "\n")
	var paragraphs strings.Builder
	for _, line := range lines {
		paragraphs.WriteString(`<a:p><a:pPr marL="0" indent="0"/><a:r><a:rPr lang="zh-CN" altLang="en-US" sz="` + strconv.Itoa(size) + `"` + boldAttr + ` dirty="0"><a:solidFill><a:srgbClr val="` + color + `"/></a:solidFill><a:latin typeface="Aptos"/><a:ea typeface="Microsoft YaHei"/></a:rPr><a:t>` + xmlEscape(line) + `</a:t></a:r><a:endParaRPr lang="zh-CN" sz="` + strconv.Itoa(size) + `"/></a:p>`)
	}
	return `<p:sp><p:nvSpPr><p:cNvPr id="` + strconv.Itoa(id) + `" name="` + xmlEscape(name) + `"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="` + strconv.Itoa(x) + `" y="` + strconv.Itoa(y) + `"/><a:ext cx="` + strconv.Itoa(cx) + `" cy="` + strconv.Itoa(cy) + `"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square" anchor="t"><a:spAutoFit/></a:bodyPr><a:lstStyle/>` + paragraphs.String() + `</p:txBody></p:sp>`
}

func pptxSlideMaster() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="简约主题"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle><a:lvl1pPr algn="l"><a:defRPr sz="3200"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr></p:titleStyle><p:bodyStyle><a:lvl1pPr marL="228600" indent="-228600"><a:buChar char="•"/><a:defRPr sz="2000"><a:solidFill><a:schemeClr val="tx1"/></a:solidFill></a:defRPr></a:lvl1pPr></p:bodyStyle><p:otherStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:otherStyle></p:txStyles></p:sldMaster>`
}

func pptxSlideLayout() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="空白"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
}

func pptxTheme() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="简约主题"><a:themeElements><a:clrScheme name="简约"><a:dk1><a:srgbClr val="111827"/></a:dk1><a:lt1><a:srgbClr val="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="374151"/></a:dk2><a:lt2><a:srgbClr val="F3F4F6"/></a:lt2><a:accent1><a:srgbClr val="1F4B99"/></a:accent1><a:accent2><a:srgbClr val="0F766E"/></a:accent2><a:accent3><a:srgbClr val="B45309"/></a:accent3><a:accent4><a:srgbClr val="7C3AED"/></a:accent4><a:accent5><a:srgbClr val="BE123C"/></a:accent5><a:accent6><a:srgbClr val="0369A1"/></a:accent6><a:hlink><a:srgbClr val="2563EB"/></a:hlink><a:folHlink><a:srgbClr val="7C3AED"/></a:folHlink></a:clrScheme><a:fontScheme name="简约字体"><a:majorFont><a:latin typeface="Aptos Display"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface=""/></a:majorFont><a:minorFont><a:latin typeface="Aptos"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface=""/></a:minorFont></a:fontScheme><a:fmtScheme name="简约格式"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"><a:tint val="95000"/></a:schemeClr></a:solidFill><a:solidFill><a:schemeClr val="phClr"><a:shade val="85000"/></a:schemeClr></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"><a:tint val="95000"/></a:schemeClr></a:solidFill><a:solidFill><a:schemeClr val="phClr"><a:shade val="90000"/></a:schemeClr></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
}

func packageRelationships(target string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="` + target + `"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`
}

func coreProperties(title, author string) string {
	if strings.TrimSpace(author) == "" {
		author = "GoFlow 本地智能体工作流平台"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + xmlEscape(title) + `</dc:title><dc:creator>` + xmlEscape(author) + `</dc:creator><cp:lastModifiedBy>` + xmlEscape(author) + `</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">` + now + `</dcterms:modified></cp:coreProperties>`
}

func appProperties(application string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>` + xmlEscape(application) + `</Application><AppVersion>16.0000</AppVersion></Properties>`
}

func zipStrings(files map[string]string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, filepath.ToSlash(name))
	}
	// Stable order makes hashes reproducible for identical inputs during one build.
	slicesSort(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
