package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type pdfInput struct {
	Title    string       `json:"title"`
	Sections []pdfSection `json:"sections"`
}

type pdfSection struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

type pdfLine struct {
	Text string
	Size int
	Gap  int
}

func createPDF(args map[string]any) (map[string]any, error) {
	target, err := prepareWriteTarget(args, ".pdf")
	if err != nil {
		return nil, err
	}
	var input pdfInput
	if err := remarshal(args, &input); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" || len(input.Sections) == 0 {
		return nil, fmt.Errorf("title 和 sections 不能为空")
	}
	data, pages := buildChinesePDF(input)
	if err := target.commit(data); err != nil {
		return nil, err
	}
	extra := map[string]any{"format": "PDF", "pages": pages}
	if target.backupPath != "" {
		extra["backup_path"] = relativeWorkspacePath(target.backupPath)
	}
	return artifactPayload(target.path, "application/pdf", extra)
}

func buildChinesePDF(input pdfInput) ([]byte, int) {
	lines := []pdfLine{{Text: input.Title, Size: 22, Gap: 32}}
	for _, section := range input.Sections {
		lines = append(lines, pdfLine{Text: section.Title, Size: 16, Gap: 25})
		for _, line := range section.Lines {
			wrapped := wrapRunes(line, 42)
			for _, item := range wrapped {
				lines = append(lines, pdfLine{Text: item, Size: 11, Gap: 18})
			}
		}
		lines = append(lines, pdfLine{Text: "", Size: 11, Gap: 8})
	}
	pages := paginatePDFLines(lines)
	objectCount := 4 + len(pages)*2
	objects := make([][]byte, objectCount+1)
	pageRefs := make([]string, len(pages))
	for i := range pages {
		pageID := 5 + i*2
		contentID := pageID + 1
		pageRefs[i] = fmt.Sprintf("%d 0 R", pageID)
		content := pdfPageContent(pages[i], i+1, len(pages))
		objects[contentID] = []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		objects[pageID] = []byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", contentID))
	}
	objects[1] = []byte("<< /Type /Catalog /Pages 2 0 R >>")
	objects[2] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(pageRefs, " "), len(pages)))
	objects[3] = []byte("<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [4 0 R] >>")
	objects[4] = []byte("<< /Type /Font /Subtype /CIDFontType0 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 4 >> >>")

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, objectCount+1)
	for id := 1; id <= objectCount; id++ {
		offsets[id] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", id)
		out.Write(objects[id])
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", objectCount+1)
	out.WriteString("0000000000 65535 f \n")
	for id := 1; id <= objectCount; id++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objectCount+1, xref)
	return out.Bytes(), len(pages)
}

func paginatePDFLines(lines []pdfLine) [][]pdfLine {
	pages := make([][]pdfLine, 1)
	y := 785
	for _, line := range lines {
		if y-line.Gap < 55 && len(pages[len(pages)-1]) > 0 {
			pages = append(pages, nil)
			y = 785
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], line)
		y -= line.Gap
	}
	return pages
}

func pdfPageContent(lines []pdfLine, page, total int) string {
	var out strings.Builder
	y := 785
	for _, line := range lines {
		if line.Text != "" {
			fmt.Fprintf(&out, "BT /F1 %d Tf 1 0 0 1 55 %d Tm <%s> Tj ET\n", line.Size, y, utf16BEHex(line.Text))
		}
		y -= line.Gap
	}
	footer := "第 " + strconv.Itoa(page) + " / " + strconv.Itoa(total) + " 页"
	fmt.Fprintf(&out, "BT /F1 9 Tf 1 0 0 1 475 28 Tm <%s> Tj ET", utf16BEHex(footer))
	return out.String()
}

func utf16BEHex(value string) string {
	var raw bytes.Buffer
	for _, r := range value {
		if r <= 0xFFFF {
			_ = binary.Write(&raw, binary.BigEndian, uint16(r))
			continue
		}
		r -= 0x10000
		high := uint16(0xD800 + (r >> 10))
		low := uint16(0xDC00 + (r & 0x3FF))
		_ = binary.Write(&raw, binary.BigEndian, high)
		_ = binary.Write(&raw, binary.BigEndian, low)
	}
	return strings.ToUpper(hex.EncodeToString(raw.Bytes()))
}

func wrapRunes(value string, limit int) []string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return []string{""}
	}
	var lines []string
	for len(runes) > limit {
		breakAt := limit
		for i := limit; i > limit/2; i-- {
			if runes[i-1] == ' ' || runes[i-1] == '，' || runes[i-1] == '。' || runes[i-1] == '；' {
				breakAt = i
				break
			}
		}
		lines = append(lines, strings.TrimSpace(string(runes[:breakAt])))
		runes = runes[breakAt:]
	}
	if len(runes) > 0 {
		lines = append(lines, strings.TrimSpace(string(runes)))
	}
	return lines
}
