package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/notification"
)

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind"`
}

func builtinTools() []tool {
	return []tool{{
		Name: "publish_event", Kind: "network",
		Description: "将通用事件持久化到本地收集器（默认 channel_id: local），或投递一次到用户预先保存的 Webhook。所有通道均遵循工具审批，不抓取证据引用。delivery_status 的 stored 仅表示本地存储，delivered 表示 HTTP 2xx，failed 表示失败；禁止传入 URL 或凭据。",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":{"type":"string","pattern":"^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$","default":"local"},"severity":{"type":"string","enum":["info","warning","error","critical"]},"title":{"type":"string","minLength":1,"maxLength":256},"summary":{"type":"string","minLength":1,"maxLength":8192},"evidence_refs":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":512}}},"required":["severity","title","summary"],"additionalProperties":false}`),
	}}
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "notification runtime directory unavailable")
		os.Exit(1)
	}
	publisher := notification.Publisher{DataRoot: config.ResolveDataRoot(root), NetworkDisabled: os.Getenv("GOFLOW_NETWORK_DISABLED") == "1"}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := serve(ctx, os.Stdin, os.Stdout, publisher); err != nil {
		fmt.Fprintln(os.Stderr, "notification MCP stream failed")
		os.Exit(1)
	}
}

func callTool(ctx context.Context, params json.RawMessage, publisher notification.Publisher) map[string]any {
	var input struct {
		Name      string             `json:"name"`
		Arguments notification.Input `json:"arguments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Name != "publish_event" || len(params) > 65536 {
		return map[string]any{"content": "invalid publish_event arguments", "is_error": true}
	}
	event, err := publisher.Publish(ctx, input.Arguments)
	if event.EventID == "" {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	payload := map[string]any{"event_id": event.EventID, "channel_id": event.ChannelID, "severity": event.Severity, "delivery_status": event.DeliveryStatus}
	if event.HTTPStatus != 0 {
		payload["http_status"] = event.HTTPStatus
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	data, _ := json.Marshal(payload)
	return map[string]any{"content": string(data), "is_error": err != nil}
}

func serve(ctx context.Context, in io.Reader, out io.Writer, publisher notification.Publisher) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 65536)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		resp := map[string]any{"jsonrpc": "2.0"}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			resp["id"] = nil
			resp["error"] = map[string]any{"code": -32700, "message": "invalid JSON"}
		} else {
			if len(req.ID) == 0 { // Notifications have no response.
				continue
			}
			resp["id"] = req.ID
			switch req.Method {
			case "tools/list":
				resp["result"] = map[string]any{"tools": builtinTools()}
			case "tools/call":
				resp["result"] = callTool(ctx, req.Params, publisher)
			default:
				resp["error"] = map[string]any{"code": -32601, "message": "method not found"}
			}
		}
		if err := encoder.Encode(resp); err != nil {
			return errors.New("write response failed")
		}
	}
	return scanner.Err()
}
