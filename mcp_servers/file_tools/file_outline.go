package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// outlineDeclaration is one navigable unit in a source file.
type outlineDeclaration struct {
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
}

var outlinePatterns = []struct {
	ext     []string
	kind    string
	pattern *regexp.Regexp
	nameAt  int
}{
	{[]string{".go"}, "func", regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(`), 1},
	{[]string{".go"}, "type", regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s+`), 1},
	{[]string{".js", ".ts", ".jsx", ".tsx", ".mjs"}, "function", regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`), 1},
	{[]string{".js", ".ts", ".jsx", ".tsx", ".mjs"}, "class", regexp.MustCompile(`^(?:export\s+)?class\s+([A-Za-z_$][\w$]*)`), 1},
	{[]string{".js", ".ts", ".jsx", ".tsx", ".mjs"}, "export", regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`), 1},
	{[]string{".py"}, "def", regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`), 1},
	{[]string{".py"}, "class", regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`), 1},
	{[]string{".rs"}, "fn", regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), 1},
	{[]string{".java"}, "method", regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?[\w<>\[\]]+\s+([A-Za-z_]\w*)\s*\(`), 1},
}

// fileOutline returns the navigable declarations of a source file with their
// line numbers, so the model can jump straight to a range instead of reading a
// whole file and paying for context it does not need.
func fileOutline(path string, maxEntries int) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s", normalizeWorkspaceFileError("read_file_outline", path, err))
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory")
	}
	if info.Size() > maxReadBytes {
		return nil, fmt.Errorf("file too large: %d bytes exceeds %d", info.Size(), maxReadBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content, err := decodeUTF8Text(data)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		// A trailing newline is not an extra line.
		lines = lines[:len(lines)-1]
	}
	ext := strings.ToLower(filepath.Ext(path))
	declarations := make([]outlineDeclaration, 0, 64)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, pattern := range outlinePatterns {
			if !outlineExtMatches(pattern.ext, ext) {
				continue
			}
			match := pattern.pattern.FindStringSubmatch(trimmed)
			if match == nil || len(match) <= pattern.nameAt {
				continue
			}
			signature := trimmed
			if len(signature) > 120 {
				signature = signature[:120]
			}
			declarations = append(declarations, outlineDeclaration{
				Line:      index + 1,
				Kind:      pattern.kind,
				Name:      match[pattern.nameAt],
				Signature: signature,
			})
			break
		}
		if maxEntries > 0 && len(declarations) >= maxEntries {
			break
		}
	}
	return map[string]any{
		"path":         relativeWorkspacePath(path),
		"size":         info.Size(),
		"total_lines":  len(lines),
		"declarations": declarations,
		"count":        len(declarations),
		"hint":         "用 read_file_range 带 offset/limit 只读需要声明的行区间。",
	}, nil
}

func outlineExtMatches(exts []string, ext string) bool {
	for _, candidate := range exts {
		if candidate == ext {
			return true
		}
	}
	return false
}
