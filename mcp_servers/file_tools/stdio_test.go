package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestServeProcessesMultipleRequestsInOneSession(t *testing.T) {
	workspaceRoot = t.TempDir()
	input := bytes.NewBuffer(nil)
	writer := bufio.NewWriter(input)
	write(writer, response{})
	input.Reset()

	_, _ = input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n")
	_, _ = input.WriteString("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
	output := bytes.NewBuffer(nil)

	if err := serve(input, output); err != nil {
		t.Fatalf("serve: %v", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	count := 0
	for scanner.Scan() {
		count++
		var resp response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response %d: %v", count, err)
		}
		if resp.Error != nil {
			t.Fatalf("unexpected rpc error in response %d: %#v", count, resp.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan output: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 responses, got %d", count)
	}
}
