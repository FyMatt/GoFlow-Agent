package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	xlsxSheetNamePattern  = regexp.MustCompile(`<sheet name="([^"]+)"[^>]* r:id="([^"]+)"`)
	xlsxRelTargetPattern  = regexp.MustCompile(`Id="([^"]+)"[^>]*Target="([^"]+)"|Target="([^"]+)"[^>]*Id="([^"]+)"`)
	xlsxRowPattern        = regexp.MustCompile(`(?s)<row r="(\d+)"[^>]*>(.*?)</row>`)
	xlsxCellPattern       = regexp.MustCompile(`(?s)<c r="([A-Z]+)(\d+)"([^>/]*)(?:/>|>(.*?)</c>)`)
	xlsxSheetDataPattern  = regexp.MustCompile(`(?s)(<sheetData>)(.*?)(</sheetData>)`)
	xlsxAutoFilterPattern = regexp.MustCompile(`<autoFilter ref="[^"]*"`)
	xlsxDimensionPattern  = regexp.MustCompile(`<dimension ref="[^"]*"`)
)

func readXLSXWorkbook(args map[string]any) (map[string]any, error) {
	rawPath, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(rawPath)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(path)) != ".xlsx" {
		return nil, fmt.Errorf("只支持 XLSX")
	}
	wantedSheet, _ := stringArg(args, "sheet", false)
	maxRows := intArg(args, "max_rows", 200, 1, 5000)
	workbook, err := loadXLSXWorkbook(path)
	if err != nil {
		return nil, err
	}
	sheets := make([]map[string]any, 0, len(workbook.sheets))
	for _, sheet := range workbook.sheets {
		if wantedSheet != "" && sheet.name != wantedSheet {
			continue
		}
		rows := sheet.rows
		if len(rows) > maxRows {
			rows = rows[:maxRows]
		}
		sheets = append(sheets, map[string]any{
			"name":      sheet.name,
			"rows":      rows,
			"row_count": len(sheet.rows),
			"formulas":  sheet.formulas,
		})
	}
	if wantedSheet != "" && len(sheets) == 0 {
		return nil, fmt.Errorf("未找到工作表 %s", wantedSheet)
	}
	return map[string]any{
		"path":   relativeWorkspacePath(path),
		"format": "XLSX",
		"sheets": sheets,
	}, nil
}

func updateXLSXCells(args map[string]any) (map[string]any, error) {
	rawPath, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(rawPath)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(path)) != ".xlsx" {
		return nil, fmt.Errorf("只支持 XLSX")
	}
	if err := verifyOptionalSHA256(path, args, "expected_sha256"); err != nil {
		return nil, err
	}
	sheetName, err := stringArg(args, "sheet", true)
	if err != nil {
		return nil, err
	}
	var cells []struct {
		Address string `json:"address"`
		Value   any    `json:"value"`
	}
	if err := remarshal(args["cells"], &cells); err != nil || len(cells) == 0 {
		return nil, fmt.Errorf("cells 不能为空")
	}
	entries, err := readZipEntries(path, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	sheetPath, err := xlsxSheetPath(entries, sheetName)
	if err != nil {
		return nil, err
	}
	xmlText := string(entries[sheetPath])
	updated := 0
	for _, cell := range cells {
		address := strings.ToUpper(strings.TrimSpace(cell.Address))
		if !regexp.MustCompile(`^[A-Z]+[1-9][0-9]*$`).MatchString(address) {
			return nil, fmt.Errorf("非法单元格地址 %s", cell.Address)
		}
		xmlText, err = replaceOrInsertXLSXCell(xmlText, address, cell.Value)
		if err != nil {
			return nil, err
		}
		updated++
	}
	entries[sheetPath] = []byte(refreshXLSXSheetRange(xmlText))
	actual, _, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	target, err := prepareWriteTarget(map[string]any{"path": rawPath, "overwrite": true, "expected_sha256": actual}, ".xlsx")
	if err != nil {
		return nil, err
	}
	data, err := zipBytes(entries)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	payload, err := artifactPayload(path, mimeForExtension(".xlsx"), map[string]any{
		"sheet":         sheetName,
		"updated_cells": updated,
	})
	return payload, err
}

func mergeCSVIntoXLSX(args map[string]any) (map[string]any, error) {
	rawPath, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	path, err := resolveWorkspacePath(rawPath)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(path)) != ".xlsx" {
		return nil, fmt.Errorf("只支持 XLSX")
	}
	if err := verifyOptionalSHA256(path, args, "expected_sha256"); err != nil {
		return nil, err
	}
	rawCSV, err := stringArg(args, "csv_path", true)
	if err != nil {
		return nil, err
	}
	csvPath, err := resolveWorkspacePath(rawCSV)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(csvPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取 CSV：%w", err)
	}
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil || len(records) == 0 {
		return nil, fmt.Errorf("CSV 为空或无法解析")
	}
	sheetName, _ := stringArg(args, "sheet", false)
	includeHeader := boolArg(args, "include_header", true)
	startRow := intArg(args, "start_row", 0, 0, 5000)
	entries, err := readZipEntries(path, 64*1024*1024)
	if err != nil {
		return nil, err
	}
	if sheetName == "" {
		names := xlsxSheetNames(entries)
		if len(names) == 0 {
			return nil, fmt.Errorf("工作簿没有工作表")
		}
		sheetName = names[0]
	}
	sheetPath, err := xlsxSheetPath(entries, sheetName)
	if err != nil {
		return nil, err
	}
	xmlText := string(entries[sheetPath])
	if startRow <= 0 {
		if includeHeader {
			startRow = 1
		} else {
			startRow = 2
		}
	}
	dataRows := records
	if !includeHeader && len(records) > 1 {
		dataRows = records[1:]
	}
	xmlText, written, err := replaceXLSXDataRows(xmlText, startRow, dataRows)
	if err != nil {
		return nil, err
	}
	entries[sheetPath] = []byte(xmlText)
	actual, _, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	target, err := prepareWriteTarget(map[string]any{"path": rawPath, "overwrite": true, "expected_sha256": actual}, ".xlsx")
	if err != nil {
		return nil, err
	}
	data, err := zipBytes(entries)
	if err != nil {
		return nil, err
	}
	if err := target.commit(data); err != nil {
		return nil, err
	}
	payload, err := artifactPayload(path, mimeForExtension(".xlsx"), map[string]any{
		"sheet":        sheetName,
		"csv_path":     relativeWorkspacePath(csvPath),
		"rows_written": written,
		"start_row":    startRow,
	})
	return payload, err
}

type xlsxLoadedSheet struct {
	name     string
	rows     [][]string
	formulas int
}

type xlsxLoadedWorkbook struct {
	sheets []xlsxLoadedSheet
}

func loadXLSXWorkbook(path string) (xlsxLoadedWorkbook, error) {
	entries, err := readZipEntries(path, 64*1024*1024)
	if err != nil {
		return xlsxLoadedWorkbook{}, err
	}
	var out xlsxLoadedWorkbook
	for _, name := range xlsxSheetNames(entries) {
		sheetPath, err := xlsxSheetPath(entries, name)
		if err != nil {
			return xlsxLoadedWorkbook{}, err
		}
		rows, formulas := parseXLSXSheetRows(string(entries[sheetPath]))
		out.sheets = append(out.sheets, xlsxLoadedSheet{name: name, rows: rows, formulas: formulas})
	}
	return out, nil
}

func xlsxSheetNames(entries map[string][]byte) []string {
	workbook := string(entries["xl/workbook.xml"])
	matches := xlsxSheetNamePattern.FindAllStringSubmatch(workbook, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			names = append(names, match[1])
		}
	}
	return names
}

func xlsxSheetPath(entries map[string][]byte, sheetName string) (string, error) {
	workbook := string(entries["xl/workbook.xml"])
	var rid string
	for _, match := range xlsxSheetNamePattern.FindAllStringSubmatch(workbook, -1) {
		if len(match) > 2 && match[1] == sheetName {
			rid = match[2]
			break
		}
	}
	if rid == "" {
		return "", fmt.Errorf("未找到工作表 %s", sheetName)
	}
	rels := string(entries["xl/_rels/workbook.xml.rels"])
	target := ""
	for _, match := range xlsxRelTargetPattern.FindAllStringSubmatch(rels, -1) {
		id, path := match[1], match[2]
		if id == "" {
			id, path = match[4], match[3]
		}
		if id == rid {
			target = path
			break
		}
	}
	if target == "" {
		return "", fmt.Errorf("工作表 %s 没有对应文件", sheetName)
	}
	target = strings.TrimPrefix(target, "/")
	if !strings.HasPrefix(target, "xl/") {
		target = "xl/" + strings.TrimPrefix(target, "../")
		if !strings.HasPrefix(target, "xl/worksheets/") {
			target = "xl/worksheets/" + filepath.Base(target)
		}
	}
	if _, ok := entries[target]; !ok {
		for name := range entries {
			if filepath.Base(name) == filepath.Base(target) {
				return name, nil
			}
		}
		return "", fmt.Errorf("工作表文件不存在：%s", target)
	}
	return target, nil
}

func parseXLSXSheetRows(xmlText string) ([][]string, int) {
	rowMatches := xlsxRowPattern.FindAllStringSubmatch(xmlText, -1)
	maxRow, maxCol := 0, 0
	type cellVal struct {
		row, col int
		text     string
		formula  bool
	}
	cells := make([]cellVal, 0)
	formulas := 0
	for _, rowMatch := range rowMatches {
		rowNum, _ := strconv.Atoi(rowMatch[1])
		for _, cellMatch := range xlsxCellPattern.FindAllStringSubmatch(rowMatch[2], -1) {
			col := columnIndex(cellMatch[1])
			row, _ := strconv.Atoi(cellMatch[2])
			if row == 0 {
				row = rowNum
			}
			body := cellMatch[4]
			text := xlsxCellText(cellMatch[3], body)
			formula := strings.Contains(body, "<f>")
			if formula {
				formulas++
			}
			if row > maxRow {
				maxRow = row
			}
			if col > maxCol {
				maxCol = col
			}
			cells = append(cells, cellVal{row: row, col: col, text: text, formula: formula})
		}
	}
	if maxRow == 0 {
		return nil, formulas
	}
	rows := make([][]string, maxRow)
	for i := range rows {
		rows[i] = make([]string, maxCol)
	}
	for _, cell := range cells {
		if cell.row <= 0 || cell.col <= 0 {
			continue
		}
		rows[cell.row-1][cell.col-1] = cell.text
	}
	return rows, formulas
}

func xlsxCellText(attrs, body string) string {
	if strings.Contains(body, "<f>") {
		inner := body
		if start := strings.Index(inner, "<f>"); start >= 0 {
			inner = inner[start+3:]
			if end := strings.Index(inner, "</f>"); end >= 0 {
				return "=" + htmlUnescape(inner[:end])
			}
		}
	}
	if strings.Contains(attrs, `t="inlineStr"`) || strings.Contains(body, "<is>") {
		if start := strings.Index(body, "<t"); start >= 0 {
			rest := body[start:]
			if gt := strings.Index(rest, ">"); gt >= 0 {
				rest = rest[gt+1:]
				if end := strings.Index(rest, "</t>"); end >= 0 {
					return htmlUnescape(rest[:end])
				}
			}
		}
	}
	if start := strings.Index(body, "<v>"); start >= 0 {
		rest := body[start+3:]
		if end := strings.Index(rest, "</v>"); end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

func htmlUnescape(value string) string {
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	return replacer.Replace(value)
}

func columnIndex(name string) int {
	n := 0
	for _, r := range name {
		if r < 'A' || r > 'Z' {
			return 0
		}
		n = n*26 + int(r-'A'+1)
	}
	return n
}

func replaceOrInsertXLSXCell(xmlText, address string, value any) (string, error) {
	_, row, err := splitCellAddress(address)
	if err != nil {
		return xmlText, err
	}
	formulaCount := 0
	cellXML := xlsxCellXML(address, value, 0, &formulaCount)
	rowPattern := regexp.MustCompile(fmt.Sprintf(`(?s)(<row r="%d"[^>]*>)(.*?)(</row>)`, row))
	if match := rowPattern.FindStringSubmatchIndex(xmlText); match != nil {
		rowXML := xmlText[match[4]:match[5]]
		cellPattern := regexp.MustCompile(fmt.Sprintf(`(?s)<c r="%s"(?:[^>/]*/>|[^>]*>.*?</c>)`, regexp.QuoteMeta(address)))
		if cellPattern.MatchString(rowXML) {
			rowXML = cellPattern.ReplaceAllString(rowXML, cellXML)
		} else {
			rowXML += cellXML
		}
		return xmlText[:match[4]] + rowXML + xmlText[match[5]:], nil
	}
	dataMatch := xlsxSheetDataPattern.FindStringSubmatchIndex(xmlText)
	if dataMatch == nil {
		return "", fmt.Errorf("工作表缺少 sheetData")
	}
	insert := fmt.Sprintf(`<row r="%d">%s</row>`, row, cellXML)
	return xmlText[:dataMatch[5]] + insert + xmlText[dataMatch[5]:], nil
}

func splitCellAddress(address string) (col, row int, err error) {
	i := 0
	for i < len(address) && address[i] >= 'A' && address[i] <= 'Z' {
		i++
	}
	if i == 0 || i == len(address) {
		return 0, 0, fmt.Errorf("非法单元格地址 %s", address)
	}
	row, convErr := strconv.Atoi(address[i:])
	if convErr != nil || row < 1 {
		return 0, 0, fmt.Errorf("非法单元格地址 %s", address)
	}
	col = columnIndex(address[:i])
	if col < 1 {
		return 0, 0, fmt.Errorf("非法单元格地址 %s", address)
	}
	return col, row, nil
}

func replaceXLSXDataRows(xmlText string, startRow int, rows [][]string) (string, int, error) {
	dataMatch := xlsxSheetDataPattern.FindStringSubmatchIndex(xmlText)
	if dataMatch == nil {
		return "", 0, fmt.Errorf("工作表缺少 sheetData")
	}
	existing := xmlText[dataMatch[4]:dataMatch[5]]
	var kept strings.Builder
	maxKept := 0
	for _, rowMatch := range xlsxRowPattern.FindAllStringSubmatch(existing, -1) {
		rowNum, _ := strconv.Atoi(rowMatch[1])
		if rowNum < startRow {
			kept.WriteString(rowMatch[0])
			if rowNum > maxKept {
				maxKept = rowNum
			}
		}
	}
	formulaCount := 0
	maxCol := 0
	for i, row := range rows {
		if len(row) > maxCol {
			maxCol = len(row)
		}
		rowNum := startRow + i
		kept.WriteString(fmt.Sprintf(`<row r="%d">`, rowNum))
		for col, value := range row {
			kept.WriteString(xlsxCellXML(columnName(col+1)+strconv.Itoa(rowNum), value, 0, &formulaCount))
		}
		kept.WriteString(`</row>`)
	}
	lastRow := startRow + len(rows) - 1
	if lastRow < maxKept {
		lastRow = maxKept
	}
	if maxCol < 1 {
		maxCol = 1
	}
	updated := xmlText[:dataMatch[4]] + kept.String() + xmlText[dataMatch[5]:]
	updated = refreshXLSXSheetRangeWithSize(updated, maxCol, lastRow)
	return updated, len(rows), nil
}

func refreshXLSXSheetRange(xmlText string) string {
	rows, _ := parseXLSXSheetRows(xmlText)
	maxCol, lastRow := 1, len(rows)
	for _, row := range rows {
		if len(row) > maxCol {
			maxCol = len(row)
		}
	}
	if lastRow < 1 {
		lastRow = 1
	}
	return refreshXLSXSheetRangeWithSize(xmlText, maxCol, lastRow)
}

func refreshXLSXSheetRangeWithSize(xmlText string, maxCol, lastRow int) string {
	end := columnName(maxCol) + strconv.Itoa(lastRow)
	xmlText = xlsxDimensionPattern.ReplaceAllString(xmlText, `<dimension ref="A1:`+end+`"`)
	if xlsxAutoFilterPattern.MatchString(xmlText) {
		xmlText = xlsxAutoFilterPattern.ReplaceAllString(xmlText, `<autoFilter ref="A1:`+end+`"`)
	}
	return xmlText
}

func verifyOptionalSHA256(path string, args map[string]any, key string) error {
	expected, _ := stringArg(args, key, false)
	if expected == "" {
		return nil
	}
	actual, _, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("文件 SHA-256 不匹配，已有其他修改")
	}
	return nil
}
