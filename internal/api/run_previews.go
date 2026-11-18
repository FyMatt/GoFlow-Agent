package api

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type runPreview struct {
	URL     string `json:"url"`
	Running bool   `json:"running"`
}

var previewAddressPattern = regexp.MustCompile(`https?://[^\s\x1b]+`)

func processPreview(payload map[string]any) *runPreview {
	for _, address := range previewAddressPattern.FindAllString(runDiffString(payload, "stdout")+"\n"+runDiffString(payload, "stderr"), -1) {
		u, err := url.Parse(strings.TrimRight(address, `"',.;)`))
		if err != nil || u.Hostname() == "" || u.User != nil {
			continue
		}
		host := u.Hostname()
		if u.Port() == "" && host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
			continue
		}
		if host == "0.0.0.0" || host == "::" {
			port := u.Port()
			u.Host = "127.0.0.1"
			if port != "" {
				u.Host = net.JoinHostPort("127.0.0.1", port)
			}
		}
		running, _ := payload["running"].(bool)
		stopped, _ := payload["stop_requested"].(bool)
		return &runPreview{URL: u.String(), Running: running && !stopped}
	}
	return nil
}

func timelineToolMetadata(kind, content string) map[string]any {
	if kind != "tool_result" {
		return nil
	}
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil {
		return nil
	}
	if payload["added_lines"] == nil && payload["deleted_lines"] == nil && payload["process_id"] == nil && payload["status"] == nil && payload["source_relative"] == nil {
		return nil
	}
	result := map[string]any{}
	for _, key := range []string{"relative_path", "path", "status", "added_lines", "deleted_lines", "source_relative", "destination_relative", "is_dir", "overwrote", "process_id", "pid", "running", "stop_requested", "program", "args", "exit_code", "duration_ms", "stdout", "stderr", "timed_out", "cancelled"} {
		if value, ok := payload[key]; ok {
			if text, yes := value.(string); yes {
				value = boundedWorkbenchText(text, 2048)
			}
			result[key] = value
		}
	}
	if preview := processPreview(payload); preview != nil {
		result["preview_url"] = preview.URL
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func runPreviews(results []schema.ToolResult) []runPreview {
	byAddress := map[string]runPreview{}
	for _, result := range results {
		name := result.ToolName
		if slash := strings.LastIndex(name, "/"); slash >= 0 {
			name = name[slash+1:]
		}
		if name != "start_process" && name != "process_status" && name != "stop_process" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(result.Content), &payload) != nil {
			continue
		}
		if preview := processPreview(payload); preview != nil {
			byAddress[preview.URL] = *preview
		}
	}
	previews := make([]runPreview, 0, len(byAddress))
	for _, preview := range byAddress {
		previews = append(previews, preview)
	}
	sort.Slice(previews, func(i, j int) bool { return previews[i].URL < previews[j].URL })
	return previews
}
