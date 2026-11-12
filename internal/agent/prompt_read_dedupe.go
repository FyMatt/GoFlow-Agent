package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// dedupeRepeatedFileReads collapses repeated reads of the same unchanged file
// inside one conversation. Measured runs read the same file in plan, implement
// and verify; every repeat is new prompt content that is billed as a cache
// miss. The first occurrence keeps the full content; later identical reads keep
// path/hash metadata and a pointer, so the model can still ask for a range.
func dedupeRepeatedFileReads(messages []schema.Message) []schema.Message {
	if len(messages) < 2 {
		return messages
	}
	type observation struct {
		content string
	}
	seen := make(map[string]observation, len(messages))
	out := make([]schema.Message, 0, len(messages))
	for _, message := range messages {
		if !strings.EqualFold(message.Role, "tool") {
			out = append(out, message)
			continue
		}
		path, digest, ok := fileReadIdentity(message.Content)
		if !ok {
			out = append(out, message)
			continue
		}
		key := path + "\x00" + digest
		if first, exists := seen[key]; exists {
			copied := schema.CopyMessage(message)
			copied.Content = repeatedFileReadStub(path, digest, len(first.content))
			out = append(out, copied)
			continue
		}
		seen[key] = observation{content: message.Content}
		out = append(out, message)
	}
	return out
}

// fileReadIdentity extracts the workspace path and content hash from a file
// tool result. Only structured file-tool payloads qualify.
func fileReadIdentity(content string) (string, string, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return "", "", false
	}
	var payload struct {
		RelativePath string `json:"relative_path"`
		SHA256       string `json:"sha256"`
		Files        []struct {
			Path    string `json:"path"`
			SHA256  string `json:"sha256"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return "", "", false
	}
	path := strings.TrimSpace(payload.RelativePath)
	digest := strings.TrimSpace(payload.SHA256)
	if path == "" || digest == "" {
		return "", "", false
	}
	return path, digest, true
}

func repeatedFileReadStub(path, digest string, originalBytes int) string {
	hash := sha256.Sum256([]byte(path + "\x00" + digest))
	stub := map[string]any{
		"relative_path":  path,
		"sha256":         digest,
		"repeated_read":  true,
		"original_bytes": originalBytes,
		"stub_id":        hex.EncodeToString(hash[:8]),
		"hint":           "该文件在本对话中已完整读取过且内容未变；需要细节时用 read_file_range 读取指定区间。",
	}
	encoded, err := json.Marshal(stub)
	if err != nil {
		return "[repeated read of " + path + "; content identical to the earlier observation]"
	}
	return string(encoded)
}
