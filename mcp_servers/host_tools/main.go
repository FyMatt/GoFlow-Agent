package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	localTargetID          = "local"
	defaultRecordLimit     = 100
	maxRecordLimit         = 500
	defaultLogLines        = 100
	maxLogLines            = 500
	maxCommandOutputBytes  = 1024 * 1024
	diagnosticCommandLimit = 12 * time.Second
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

type diagnosticInput struct {
	Path              string   `json:"path,omitempty"`
	JSONPointer       string   `json:"json_pointer,omitempty"`
	Value             any      `json:"value,omitempty"`
	ExpectedSHA       string   `json:"expected_sha256,omitempty"`
	CurrentContent    string   `json:"current_content,omitempty"`
	CurrentSHA        string   `json:"current_sha256,omitempty"`
	DesiredContent    string   `json:"desired_content,omitempty"`
	DesiredSHA        string   `json:"desired_sha256,omitempty"`
	ChangeID          string   `json:"change_id,omitempty"`
	ChangeReason      string   `json:"change_reason,omitempty"`
	RecoveryReason    string   `json:"recovery_reason,omitempty"`
	BackupRequired    bool     `json:"backup_required,omitempty"`
	PostCheckRequired bool     `json:"post_check_required,omitempty"`
	Protocol          string   `json:"protocol,omitempty"`
	ListeningOnly     *bool    `json:"listening_only,omitempty"`
	PIDs              []int    `json:"pids,omitempty"`
	Names             []string `json:"names,omitempty"`
	Source            string   `json:"source,omitempty"`
	Service           string   `json:"service,omitempty"`
	Lines             int      `json:"lines,omitempty"`
	Limit             int      `json:"limit,omitempty"`
}

type commandOutput struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }

var runDiagnosticCommand = runFixedCommand

func main() {
	if err := serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch req.Method {
		case "tools/list":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Result: callTool(ctx, req.Params)})
		default:
			writeResponse(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{
		{Name: "host_facts", Description: "收集所选本机 Target 的受限只读事实，包括操作系统、架构、主机名、CPU 数量、运行环境和可用时的启动时间。", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), Kind: "read"},
		{Name: "host_diagnostic_snapshot", Description: "一次收集主机事实、磁盘、监听端口，并按端口 PID 精确关联进程；用于减少重复工具调用和无关进程输出，全程只读。", InputSchema: json.RawMessage(`{"type":"object","properties":{"protocol":{"type":"string","enum":["any","tcp","udp"]},"listening_only":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":200}},"additionalProperties":false}`), Kind: "read"},
		{Name: "disk_usage", Description: "收集所选本机 Target 的结构化文件系统容量和可用空间证据；不会挂载、修复或修改磁盘卷。", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","maxLength":4096,"description":"可选的现有绝对路径，用于检查其所在文件系统。"},"limit":{"type":"integer","minimum":1,"maximum":500}},"additionalProperties":false}`), Kind: "read"},
		{Name: "list_ports", Description: "使用固定的操作系统只读命令列出受限数量的本机 TCP/UDP 套接字证据，不会发起连接或探测。", InputSchema: json.RawMessage(`{"type":"object","properties":{"protocol":{"type":"string","enum":["any","tcp","udp"]},"listening_only":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":500}},"additionalProperties":false}`), Kind: "read"},
		{Name: "list_processes", Description: "使用原生只读接口或固定系统命令列出受限进程元数据；提供端口 PID 时优先使用 pids 精确查询，避免全量进程输出。", InputSchema: json.RawMessage(`{"type":"object","properties":{"pids":{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,"items":{"type":"integer","minimum":0}},"limit":{"type":"integer","minimum":1,"maximum":500}},"additionalProperties":false}`), Kind: "read"},
		{Name: "service_status", Description: "使用固定的只读服务管理命令列出或筛选本机服务状态，不提供服务启停或修改能力。", InputSchema: json.RawMessage(`{"type":"object","properties":{"names":{"type":"array","maxItems":50,"items":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9_.@ -]+$"}},"limit":{"type":"integer","minimum":1,"maximum":500}},"additionalProperties":false}`), Kind: "read"},
		{Name: "read_log_tail", Description: "读取系统、应用或服务日志的受限尾部内容，也可读取白名单目录中的本地日志文件；拒绝白名单外路径。", InputSchema: json.RawMessage(`{"type":"object","properties":{"source":{"type":"string","enum":["system","application","service","file"]},"service":{"type":"string","maxLength":128,"pattern":"^[A-Za-z0-9_.@-]+$"},"path":{"type":"string","maxLength":4096},"lines":{"type":"integer","minimum":1,"maximum":500}},"additionalProperties":false}`), Kind: "read"},
		{Name: "inspect_managed_json", Description: "检查部署白名单主机目录中现有 JSON 配置文件的结构和 SHA-256，不返回配置值。", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":4096}},"required":["path"],"additionalProperties":false}`), Kind: "read"},
		{Name: "plan_windows_json_change", Description: "校验给定 Windows JSON 当前/目标内容及 SHA-256，并确定性生成固定路径、原子替换、后检查和可恢复脚本；只规划，不访问或修改目标文件。", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":3,"maxLength":4096},"current_content":{"type":"string","minLength":1,"maxLength":65536},"current_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"desired_content":{"type":"string","minLength":1,"maxLength":65536},"desired_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"}},"required":["path","current_content","current_sha256","desired_content","desired_sha256"],"additionalProperties":false}`), Kind: "read"},
		{Name: "set_managed_json_value", Description: "修改部署白名单主机目录中现有 JSON 对象的一个键；要求修改前 SHA 匹配、自动备份、原子写入和写后校验，不接受任意文件内容或命令。", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":4096},"json_pointer":{"type":"string","minLength":1,"maxLength":1024,"pattern":"^/"},"value":{},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"change_reason":{"type":"string","minLength":1,"maxLength":500},"backup_required":{"type":"boolean"},"post_check_required":{"type":"boolean"}},"required":["path","json_pointer","value","expected_sha256","change_reason","backup_required","post_check_required"],"additionalProperties":false}`), Kind: "write"},
		{Name: "restore_managed_json_change", Description: "确认当前文件仍匹配记录的变更后 SHA 后，原子恢复一次已提交的智能体 JSON 配置修改，并验证恢复结果。", InputSchema: json.RawMessage(`{"type":"object","properties":{"change_id":{"type":"string","pattern":"^[a-f0-9]{24}$"},"expected_sha256":{"type":"string","pattern":"^[A-Fa-f0-9]{64}$"},"recovery_reason":{"type":"string","minLength":1,"maxLength":500},"post_check_required":{"type":"boolean"}},"required":["change_id","expected_sha256","recovery_reason","post_check_required"],"additionalProperties":false}`), Kind: "write"},
	}
}

func callTool(ctx context.Context, params json.RawMessage) map[string]any {
	if err := ensureLocalTarget(); err != nil {
		return toolError(err)
	}
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return toolError(err)
	}
	encoded, err := json.Marshal(input.Arguments)
	if err != nil {
		return toolError(err)
	}
	var args diagnosticInput
	if err := json.Unmarshal(encoded, &args); err != nil {
		return toolError(err)
	}
	var payload map[string]any
	switch input.Name {
	case "host_facts":
		payload, err = collectHostFacts(ctx)
	case "host_diagnostic_snapshot":
		payload, err = collectHostDiagnosticSnapshot(ctx, args)
	case "disk_usage":
		payload, err = collectDiskUsage(ctx, args)
	case "list_ports":
		payload, err = collectPorts(ctx, args)
	case "list_processes":
		payload, err = collectProcesses(ctx, args)
	case "service_status":
		payload, err = collectServices(ctx, args)
	case "read_log_tail":
		payload, err = collectLogTail(ctx, args)
	case "inspect_managed_json":
		payload, err = inspectManagedJSON(args)
	case "plan_windows_json_change":
		payload, err = planWindowsJSONChange(args)
	case "set_managed_json_value":
		if _, ok := input.Arguments["value"]; !ok {
			return toolError(fmt.Errorf("value is required"))
		}
		payload, err = setManagedJSONValue(args)
	case "restore_managed_json_change":
		payload, err = restoreManagedJSONChange(args)
	default:
		return toolError(fmt.Errorf("unknown tool %q", input.Name))
	}
	if err != nil {
		return toolError(err)
	}
	result, err := json.Marshal(payload)
	if err != nil {
		return toolError(err)
	}
	return map[string]any{"content": string(result), "is_error": false}
}

func collectHostDiagnosticSnapshot(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		return nil, fmt.Errorf("limit exceeds maximum of 200")
	}
	facts, err := collectHostFacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect host facts: %w", err)
	}
	disks, err := collectDiskUsage(ctx, diagnosticInput{Limit: 50})
	if err != nil {
		return nil, fmt.Errorf("collect disk usage: %w", err)
	}
	listening := true
	if input.ListeningOnly != nil {
		listening = *input.ListeningOnly
	}
	ports, err := collectPorts(ctx, diagnosticInput{Protocol: input.Protocol, ListeningOnly: &listening, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("collect ports: %w", err)
	}
	pids := processPIDsFromEnvelope(ports, 100)
	processes := listEnvelope("processes", []map[string]any{}, false)
	if len(pids) > 0 {
		processes, err = collectProcesses(ctx, diagnosticInput{PIDs: pids, Limit: len(pids)})
		if err != nil {
			return nil, fmt.Errorf("collect port processes: %w", err)
		}
	}
	return map[string]any{
		"kind":         "host_diagnostic_snapshot",
		"status":       "ok",
		"target_id":    localTargetID,
		"read_only":    true,
		"collected_at": time.Now().UTC().Format(time.RFC3339Nano),
		"facts":        facts,
		"disks":        disks,
		"ports":        ports,
		"processes":    processes,
		"limits": map[string]any{
			"port_limit":            limit,
			"process_pid_limit":     100,
			"unique_port_pid_count": portPIDCount(ports),
			"queried_process_count": len(pids),
			"process_pid_truncated": portPIDCount(ports) > len(pids),
		},
	}, nil
}

func processPIDsFromEnvelope(envelope map[string]any, limit int) []int {
	rawItems, _ := envelope["items"].([]map[string]any)
	seen := make(map[int64]struct{})
	pids := make([]int, 0)
	for _, item := range rawItems {
		pid := numberValue(item["pid"])
		if pid < 0 {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		pids = append(pids, int(pid))
	}
	sort.Ints(pids)
	if len(pids) > limit {
		pids = pids[:limit]
	}
	return pids
}

func portPIDCount(envelope map[string]any) int {
	rawItems, _ := envelope["items"].([]map[string]any)
	seen := make(map[int64]struct{})
	for _, item := range rawItems {
		pid := numberValue(item["pid"])
		if pid >= 0 {
			seen[pid] = struct{}{}
		}
	}
	return len(seen)
}

func ensureLocalTarget() error {
	target := strings.ToLower(strings.TrimSpace(os.Getenv("GOFLOW_TARGET_ID")))
	if target == "" || target == localTargetID {
		return nil
	}
	return fmt.Errorf("host diagnostics only support Target %q; got %q", localTargetID, target)
}

func baseEnvelope(kind, status string) map[string]any {
	return map[string]any{
		"kind":         kind,
		"status":       status,
		"target_id":    localTargetID,
		"collected_at": time.Now().UTC().Format(time.RFC3339Nano),
		"platform":     runtime.GOOS,
		"architecture": runtime.GOARCH,
		"read_only":    true,
	}
}

func unavailableEnvelope(kind, reason, recommendation string) map[string]any {
	payload := baseEnvelope(kind, "unavailable")
	payload["items"] = []any{}
	payload["count"] = 0
	payload["warning"] = strings.TrimSpace(reason)
	if strings.TrimSpace(recommendation) != "" {
		payload["recommendation"] = recommendation
	}
	return payload
}

func boundedLimit(value int) int {
	if value <= 0 {
		return defaultRecordLimit
	}
	if value > maxRecordLimit {
		return maxRecordLimit
	}
	return value
}

func boundedLogLines(value int) int {
	if value <= 0 {
		return defaultLogLines
	}
	if value > maxLogLines {
		return maxLogLines
	}
	return value
}

func runFixedCommand(parent context.Context, program string, args ...string) (commandOutput, error) {
	executable, err := exec.LookPath(program)
	if err != nil {
		return commandOutput{}, fmt.Errorf("required diagnostic command %s is unavailable", program)
	}
	ctx, cancel := context.WithTimeout(parent, diagnosticCommandLimit)
	defer cancel()
	stdout := &boundedBuffer{limit: maxCommandOutputBytes}
	stderr := &boundedBuffer{limit: 64 * 1024}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	result := commandOutput{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("diagnostic command %s timed out", program)
	}
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return result, fmt.Errorf("diagnostic command %s failed: %s", program, message)
	}
	return result, nil
}

func writeResponse(writer *bufio.Writer, value response) {
	data, _ := json.Marshal(value)
	_, _ = writer.Write(data)
	_ = writer.WriteByte('\n')
	_ = writer.Flush()
}

func toolError(err error) map[string]any {
	return map[string]any{"content": err.Error(), "is_error": true}
}

func collectHostFacts(ctx context.Context) (map[string]any, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	payload := baseEnvelope("host_facts", "ok")
	payload["hostname"] = hostname
	payload["cpu_count"] = runtime.NumCPU()
	payload["go_version"] = runtime.Version()
	payload["process_id"] = os.Getpid()
	if bootTime := collectBootTime(ctx); bootTime != "" {
		payload["boot_time"] = bootTime
	}
	return payload, nil
}

func collectBootTime(ctx context.Context) string {
	if runtime.GOOS == "windows" {
		output, err := runDiagnosticCommand(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')`)
		if err == nil {
			return strings.TrimSpace(output.Stdout)
		}
		return ""
	}
	data, err := os.ReadFile("/proc/stat")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "btime" {
				seconds, parseErr := strconv.ParseInt(fields[1], 10, 64)
				if parseErr == nil {
					return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
				}
			}
		}
	}
	return ""
}

func collectDiskUsage(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	limit := boundedLimit(input.Limit)
	if runtime.GOOS == "windows" {
		if items, err := collectNativeWindowsDisks(limit); err == nil && len(items) > 0 {
			return listEnvelope("disk_usage", items, len(items) == limit), nil
		}
		output, err := runDiagnosticCommand(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `Get-CimInstance Win32_LogicalDisk -Filter "DriveType=3" | Select-Object DeviceID,VolumeName,FileSystem,Size,FreeSpace | ConvertTo-Json -Compress`)
		if err != nil {
			cimErr := err
			output, err = runDiagnosticCommand(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `Get-PSDrive -PSProvider FileSystem | Select-Object Name,Root,Used,Free | ConvertTo-Json -Compress`)
			if err != nil {
				return unavailableEnvelope("disk_usage", fmt.Sprintf("CIM collector failed: %v; PSDrive fallback failed: %v", cimErr, err), "Enable PowerShell filesystem inspection or run the native binary with host inspection permissions."), nil
			}
		}
		items, err := parseWindowsDiskJSON(output.Stdout, limit)
		if err != nil {
			return nil, err
		}
		return listEnvelope("disk_usage", items, output.Truncated || len(items) == limit), nil
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = "/"
	}
	if !filepath.IsAbs(path) || strings.HasPrefix(path, "-") {
		return nil, fmt.Errorf("path must be an existing absolute path")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("inspect path: %w", err)
	}
	output, err := runDiagnosticCommand(ctx, "df", "-Pk", path)
	if err != nil {
		return unavailableEnvelope("disk_usage", err.Error(), "Install the standard df utility or use a native host with filesystem inspection support."), nil
	}
	items := parseDF(output.Stdout, limit)
	return listEnvelope("disk_usage", items, output.Truncated || len(items) == limit), nil
}

func parseWindowsDiskJSON(raw string, limit int) ([]map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []map[string]any{}, nil
	}
	var items []map[string]any
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil, fmt.Errorf("parse disk diagnostics: %w", err)
		}
	} else {
		var item map[string]any
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("parse disk diagnostics: %w", err)
		}
		items = []map[string]any{item}
	}
	for index, item := range items {
		free := numberValue(firstMapValue(item, "FreeSpace", "Free"))
		used := numberValue(item["Used"])
		size := numberValue(item["Size"])
		if size == 0 && (used > 0 || free > 0) {
			size = used + free
		}
		if used == 0 && size > 0 {
			used = maxInt64(0, size-free)
		}
		volume := stringValue(firstMapValue(item, "DeviceID", "Name"))
		mount := stringValue(firstMapValue(item, "Root", "DeviceID"))
		items[index] = map[string]any{
			"volume":     volume,
			"label":      stringValue(item["VolumeName"]),
			"filesystem": stringValue(item["FileSystem"]),
			"mount":      mount,
			"size_bytes": size,
			"free_bytes": free,
			"used_bytes": used,
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func parseDF(raw string, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 6 || strings.EqualFold(fields[0], "filesystem") {
			continue
		}
		blocks, err1 := strconv.ParseInt(fields[1], 10, 64)
		used, err2 := strconv.ParseInt(fields[2], 10, 64)
		available, err3 := strconv.ParseInt(fields[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		items = append(items, map[string]any{
			"filesystem":      fields[0],
			"size_bytes":      blocks * 1024,
			"used_bytes":      used * 1024,
			"available_bytes": available * 1024,
			"capacity":        fields[4],
			"mount":           strings.Join(fields[5:], " "),
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func collectPorts(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if protocol == "" {
		protocol = "any"
	}
	if protocol != "any" && protocol != "tcp" && protocol != "udp" {
		return nil, fmt.Errorf("protocol must be any, tcp, or udp")
	}
	listeningOnly := true
	if input.ListeningOnly != nil {
		listeningOnly = *input.ListeningOnly
	}
	limit := boundedLimit(input.Limit)
	var (
		items  []map[string]any
		output commandOutput
		err    error
	)
	if runtime.GOOS == "windows" {
		output, err = runDiagnosticCommand(ctx, "netstat.exe", "-ano")
		if err == nil {
			items = parseWindowsNetstat(output.Stdout, protocol, listeningOnly, limit)
		}
	} else {
		output, err = runDiagnosticCommand(ctx, "ss", "-H", "-lntuap")
		if err == nil {
			items = parseSS(output.Stdout, protocol, listeningOnly, limit)
		} else {
			output, err = runDiagnosticCommand(ctx, "netstat", "-an")
			if err == nil {
				items = parseUnixNetstat(output.Stdout, protocol, listeningOnly, limit)
			}
		}
	}
	if err != nil {
		return unavailableEnvelope("ports", err.Error(), "Install ss/netstat or run GoFlow with access to the host network namespace."), nil
	}
	return listEnvelope("ports", items, output.Truncated || len(items) == limit), nil
}

func parseWindowsNetstat(raw, protocol string, listeningOnly bool, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		proto := strings.ToLower(fields[0])
		if proto != "tcp" && proto != "udp" || protocol != "any" && proto != protocol {
			continue
		}
		item := socketEvidence(proto, fields[1], fields[2])
		if proto == "tcp" {
			if len(fields) < 5 || listeningOnly && !strings.EqualFold(fields[3], "LISTENING") {
				continue
			}
			item["state"] = strings.ToLower(fields[3])
			item["pid"], _ = strconv.Atoi(fields[4])
		} else {
			item["state"] = "unconnected"
			item["pid"], _ = strconv.Atoi(fields[3])
		}
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return items
}

var ssPIDPattern = regexp.MustCompile(`pid=(\d+)`)

func parseSS(raw, protocol string, listeningOnly bool, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		proto := strings.ToLower(fields[0])
		if protocol != "any" && proto != protocol {
			continue
		}
		state := strings.ToLower(fields[1])
		if listeningOnly && state != "listen" && state != "unconn" {
			continue
		}
		item := socketEvidence(proto, fields[4], fields[5])
		item["state"] = state
		if match := ssPIDPattern.FindStringSubmatch(line); len(match) == 2 {
			item["pid"], _ = strconv.Atoi(match[1])
		}
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return items
}

func parseUnixNetstat(raw, protocol string, listeningOnly bool, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := strings.ToLower(fields[0])
		if !strings.HasPrefix(proto, "tcp") && !strings.HasPrefix(proto, "udp") {
			continue
		}
		canonical := "tcp"
		if strings.HasPrefix(proto, "udp") {
			canonical = "udp"
		}
		if protocol != "any" && canonical != protocol {
			continue
		}
		state := strings.ToLower(fields[len(fields)-1])
		if listeningOnly && canonical == "tcp" && state != "listen" && state != "listening" {
			continue
		}
		item := socketEvidence(canonical, fields[3], fields[4])
		item["state"] = state
		items = append(items, item)
		if len(items) >= limit {
			break
		}
	}
	return items
}

func collectProcesses(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	if len(input.PIDs) > 100 {
		return nil, fmt.Errorf("pids exceeds maximum of 100")
	}
	for _, pid := range input.PIDs {
		if pid < 0 {
			return nil, fmt.Errorf("pid must not be negative")
		}
	}
	limit := boundedLimit(input.Limit)
	var (
		items  []map[string]any
		output commandOutput
		err    error
	)
	if runtime.GOOS == "windows" {
		items, err = collectNativeWindowsProcesses(limit, input.PIDs)
		if err != nil {
			output, err = runDiagnosticCommand(ctx, "tasklist.exe", "/FO", "CSV", "/NH")
			if err == nil {
				items = filterProcessItems(parseTasklistCSV(output.Stdout, maxRecordLimit), input.PIDs, limit)
			}
		}
	} else {
		output, err = runDiagnosticCommand(ctx, "ps", "-axo", "pid=,ppid=,user=,state=,etime=,comm=")
		if err == nil {
			items = filterProcessItems(parsePS(output.Stdout, maxRecordLimit), input.PIDs, limit)
		}
	}
	if err != nil {
		return unavailableEnvelope("processes", err.Error(), "Install the standard process inspection utility or grant access to the host process namespace."), nil
	}
	sort.SliceStable(items, func(i, j int) bool { return numberValue(items[i]["pid"]) < numberValue(items[j]["pid"]) })
	payload := listEnvelope("processes", items, output.Truncated || len(input.PIDs) == 0 && len(items) == limit)
	if len(input.PIDs) > 0 {
		payload["requested_pids"] = input.PIDs
		payload["missing_pids"] = missingProcessPIDs(items, input.PIDs)
	}
	return payload, nil
}

func filterProcessItems(items []map[string]any, pids []int, limit int) []map[string]any {
	if len(pids) == 0 {
		if len(items) > limit {
			return items[:limit]
		}
		return items
	}
	wanted := make(map[int64]struct{}, len(pids))
	for _, pid := range pids {
		wanted[int64(pid)] = struct{}{}
	}
	filtered := make([]map[string]any, 0, min(limit, len(pids)))
	for _, item := range items {
		if _, ok := wanted[numberValue(item["pid"])]; !ok {
			continue
		}
		filtered = append(filtered, item)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func missingProcessPIDs(items []map[string]any, pids []int) []int {
	found := make(map[int64]struct{}, len(items))
	for _, item := range items {
		found[numberValue(item["pid"])] = struct{}{}
	}
	missing := make([]int, 0)
	for _, pid := range pids {
		if _, ok := found[int64(pid)]; !ok {
			missing = append(missing, pid)
		}
	}
	return missing
}

func parseTasklistCSV(raw string, limit int) []map[string]any {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.FieldsPerRecord = -1
	items := make([]map[string]any, 0)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) < 5 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[1]))
		if err != nil {
			continue
		}
		items = append(items, map[string]any{
			"pid":          pid,
			"name":         strings.TrimSpace(record[0]),
			"session_name": strings.TrimSpace(record[2]),
			"session_id":   strings.TrimSpace(record[3]),
			"memory":       strings.TrimSpace(record[4]),
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func parsePS(raw string, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		items = append(items, map[string]any{
			"pid":     pid,
			"ppid":    ppid,
			"user":    fields[2],
			"state":   fields[3],
			"elapsed": fields[4],
			"name":    strings.Join(fields[5:], " "),
		})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func collectServices(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	if len(input.Names) > 50 {
		return nil, fmt.Errorf("names exceeds maximum of 50")
	}
	filter := make(map[string]struct{}, len(input.Names))
	for _, name := range input.Names {
		name = strings.ToLower(strings.TrimSpace(name))
		if !validServiceName(name, runtime.GOOS == "windows") {
			return nil, fmt.Errorf("invalid service name %q", name)
		}
		filter[name] = struct{}{}
	}
	limit := boundedLimit(input.Limit)
	var (
		items  []map[string]any
		output commandOutput
		err    error
	)
	switch runtime.GOOS {
	case "windows":
		output, err = runDiagnosticCommand(ctx, "sc.exe", "query", "state=", "all")
		if err == nil {
			items = parseWindowsServices(output.Stdout, filter, limit)
		}
	case "darwin":
		output, err = runDiagnosticCommand(ctx, "launchctl", "list")
		if err == nil {
			items = parseLaunchctl(output.Stdout, filter, limit)
		}
	default:
		output, err = runDiagnosticCommand(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager")
		if err == nil {
			items = parseSystemctl(output.Stdout, filter, limit)
		}
	}
	if err != nil {
		return unavailableEnvelope("services", err.Error(), "Run on a host with an available service manager and permission to list service state."), nil
	}
	return listEnvelope("services", items, output.Truncated || len(items) == limit), nil
}

func validServiceName(value string, windows bool) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	pattern := `^[a-z0-9_.@-]+$`
	if windows {
		pattern = `^[a-z0-9_.@ -]+$`
	}
	matched, _ := regexp.MatchString(pattern, value)
	return matched
}

func wantedService(filter map[string]struct{}, name string) bool {
	if len(filter) == 0 {
		return true
	}
	_, ok := filter[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func parseWindowsServices(raw string, filter map[string]struct{}, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	current := map[string]any{}
	flush := func() {
		name, _ := current["name"].(string)
		if name != "" && wantedService(filter, name) && len(items) < limit {
			items = append(items, current)
		}
		current = map[string]any{}
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "SERVICE_NAME:") {
			flush()
			current["name"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "SERVICE_NAME:"))
		} else if strings.HasPrefix(trimmed, "DISPLAY_NAME:") {
			current["display_name"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "DISPLAY_NAME:"))
		} else if strings.HasPrefix(trimmed, "STATE") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				stateFields := strings.Fields(parts[1])
				if len(stateFields) >= 2 {
					current["state_code"], _ = strconv.Atoi(stateFields[0])
					current["state"] = strings.ToLower(stateFields[1])
				}
			}
		}
	}
	flush()
	return items
}

func parseSystemctl(raw string, filter map[string]struct{}, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") || !wantedService(filter, fields[0]) {
			continue
		}
		items = append(items, map[string]any{"name": fields[0], "load": fields[1], "active": fields[2], "state": fields[3], "description": strings.Join(fields[4:], " ")})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func parseLaunchctl(raw string, filter map[string]struct{}, limit int) []map[string]any {
	items := make([]map[string]any, 0)
	for index, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if index == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !wantedService(filter, fields[2]) {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		status, _ := strconv.Atoi(fields[1])
		items = append(items, map[string]any{"name": fields[2], "pid": pid, "last_exit_status": status})
		if len(items) >= limit {
			break
		}
	}
	return items
}

func collectLogTail(ctx context.Context, input diagnosticInput) (map[string]any, error) {
	source := strings.ToLower(strings.TrimSpace(input.Source))
	if source == "" {
		source = "system"
	}
	lines := boundedLogLines(input.Lines)
	if source == "file" {
		path, err := allowedLogPath(input.Path)
		if err != nil {
			return nil, err
		}
		entries, truncated, err := tailLogFile(path, lines)
		if err != nil {
			return nil, err
		}
		payload := baseEnvelope("log_tail", "ok")
		payload["source"] = "file"
		payload["path"] = path
		payload["lines"] = entries
		payload["line_count"] = len(entries)
		payload["truncated"] = truncated
		return payload, nil
	}
	if source != "system" && source != "application" && source != "service" {
		return nil, fmt.Errorf("source must be system, application, service, or file")
	}
	var (
		output commandOutput
		err    error
	)
	if runtime.GOOS == "windows" {
		if source == "service" {
			return nil, fmt.Errorf("service log source is not supported on Windows; use system or application")
		}
		channel := "System"
		if source == "application" {
			channel = "Application"
		}
		output, err = runDiagnosticCommand(ctx, "wevtutil.exe", "qe", channel, "/c:"+strconv.Itoa(lines), "/rd:true", "/f:text")
	} else {
		args := []string{"--no-pager", "-n", strconv.Itoa(lines), "-o", "short-iso"}
		if source == "application" {
			args = append(args, "--user")
		}
		if source == "service" {
			service := strings.ToLower(strings.TrimSpace(input.Service))
			if !validServiceName(service, false) {
				return nil, fmt.Errorf("a valid service is required for service logs")
			}
			args = append(args, "-u", service)
		}
		output, err = runDiagnosticCommand(ctx, "journalctl", args...)
	}
	if err != nil {
		return unavailableEnvelope("log_tail", err.Error(), "Use source=file with an allowlisted log path, or grant access to the host journal/event log."), nil
	}
	entries := nonEmptyLines(output.Stdout, lines)
	payload := baseEnvelope("log_tail", "ok")
	payload["source"] = source
	if source == "service" {
		payload["service"] = strings.ToLower(strings.TrimSpace(input.Service))
	}
	payload["lines"] = entries
	payload["line_count"] = len(entries)
	payload["truncated"] = output.Truncated || len(entries) == lines
	return payload, nil
}

func allowedLogPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", fmt.Errorf("file log source requires an existing absolute path")
	}
	resolved, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("resolve log path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect log path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("log path must be a regular file")
	}
	for _, root := range allowedLogRoots() {
		rootResolved, err := filepath.Abs(filepath.Clean(root))
		if err != nil {
			continue
		}
		candidateReal, candidateErr := filepath.EvalSymlinks(resolved)
		rootReal, rootErr := filepath.EvalSymlinks(rootResolved)
		if candidateErr == nil && rootErr == nil && pathWithinRoot(rootReal, candidateReal) {
			return candidateReal, nil
		}
		if pathWithinRoot(rootResolved, resolved) && pathHasNoSymlinkFromRoot(rootResolved, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("log path is outside allowed host log roots")
}

func pathHasNoSymlinkFromRoot(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	current := filepath.Clean(root)
	if info, err := os.Lstat(current); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func allowedLogRoots() []string {
	roots := make([]string, 0, 5)
	if runtime.GOOS == "windows" {
		if windowsRoot := strings.TrimSpace(os.Getenv("SystemRoot")); windowsRoot != "" {
			roots = append(roots, filepath.Join(windowsRoot, "Logs"))
		}
		if programData := strings.TrimSpace(os.Getenv("ProgramData")); programData != "" {
			roots = append(roots, filepath.Join(programData, "GoFlow", "logs"))
		}
	} else {
		roots = append(roots, "/var/log")
	}
	for _, root := range filepath.SplitList(os.Getenv("GOFLOW_HOST_LOG_ROOTS")) {
		if root = strings.TrimSpace(root); root != "" && filepath.IsAbs(root) {
			roots = append(roots, filepath.Clean(root))
		}
	}
	return roots
}

func pathWithinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func tailLogFile(path string, limit int) ([]string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)
	ring := make([]string, limit)
	count := 0
	for scanner.Scan() {
		ring[count%limit] = scanner.Text()
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("read log file: %w", err)
	}
	length := count
	if length > limit {
		length = limit
	}
	lines := make([]string, 0, length)
	start := 0
	if count > limit {
		start = count % limit
	}
	for index := 0; index < length; index++ {
		lines = append(lines, ring[(start+index)%limit])
	}
	return lines, count > limit, nil
}

func listEnvelope(kind string, items []map[string]any, truncated bool) map[string]any {
	payload := baseEnvelope(kind, "ok")
	if items == nil {
		items = []map[string]any{}
	}
	payload["items"] = items
	payload["count"] = len(items)
	payload["truncated"] = truncated
	return payload
}

func nonEmptyLines(raw string, limit int) []string {
	items := make([]string, 0, limit)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			items = append(items, line)
		}
		if len(items) >= limit {
			break
		}
	}
	return items
}

func numberValue(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func firstMapValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func splitHostPortLoose(value string) (string, string) {
	value = strings.TrimSpace(value)
	if host, port, err := net.SplitHostPort(value); err == nil {
		return host, port
	}
	if index := strings.LastIndex(value, ":"); index >= 0 {
		return value[:index], value[index+1:]
	}
	return value, ""
}

func socketEvidence(protocol, localAddress, remoteAddress string) map[string]any {
	localHost, localPort := splitHostPortLoose(localAddress)
	remoteHost, remotePort := splitHostPortLoose(remoteAddress)
	return map[string]any{
		"protocol":       protocol,
		"local_address":  localAddress,
		"local_host":     localHost,
		"local_port":     localPort,
		"remote_address": remoteAddress,
		"remote_host":    remoteHost,
		"remote_port":    remotePort,
	}
}
