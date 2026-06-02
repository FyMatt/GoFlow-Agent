package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Kind        string          `json:"kind,omitempty"`
}

type deviceInput struct {
	DeviceID        string         `json:"device_id,omitempty"`
	Host            string         `json:"host,omitempty"`
	Platform        string         `json:"platform,omitempty"`
	Connection      string         `json:"connection,omitempty"`
	Endpoint        string         `json:"endpoint,omitempty"`
	Method          string         `json:"method,omitempty"`
	CredentialRef   string         `json:"credential_ref,omitempty"`
	ApprovalRef     string         `json:"approval_ref,omitempty"`
	ChangeTicket    string         `json:"change_ticket,omitempty"`
	AuthorizedScope bool           `json:"authorized_scope,omitempty"`
	AllowedHosts    []string       `json:"allowed_hosts,omitempty"`
	AllowedCommands []string       `json:"allowed_commands,omitempty"`
	AllowedMethods  []string       `json:"allowed_methods,omitempty"`
	AllowedPaths    []string       `json:"allowed_paths,omitempty"`
	Commands        []string       `json:"commands,omitempty"`
	ChangeCommands  []string       `json:"change_commands,omitempty"`
	Payload         map[string]any `json:"payload,omitempty"`
	DryRun          bool           `json:"dry_run,omitempty"`
	DryRunConfirmed bool           `json:"dry_run_confirmed,omitempty"`
	ChangeApproved  bool           `json:"change_approved,omitempty"`
	RollbackPlan    []string       `json:"rollback_plan,omitempty"`
}

var restconfHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	writer := bufio.NewWriter(out)
	defer writer.Flush()
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		switch req.Method {
		case "tools/list":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": builtinTools()}})
		case "tools/call":
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Result: callTool(req.Params)})
		default:
			write(writer, response{JSONRPC: "2.0", ID: req.ID, Error: map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	return scanner.Err()
}

func builtinTools() []tool {
	return []tool{
		{
			Name:        "device_discovery_plan",
			Description: "Validate an authorized network-device target and return a compact read-only discovery plan. This tool does not open a device connection.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"device_id":{"type":"string"},"host":{"type":"string","minLength":1},"platform":{"type":"string"},"connection":{"type":"string","enum":["ssh","api","console","unknown"]},"credential_ref":{"type":"string"},"authorized_scope":{"type":"boolean"},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"allowed_commands":{"type":"array","items":{"type":"string","minLength":1},"maxItems":50}},"required":["host","authorized_scope","allowed_hosts"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "device_command_plan",
			Description: "Validate read-only network-device commands against an allowlist and return an execution plan for operator approval. This tool does not run commands.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"device_id":{"type":"string"},"host":{"type":"string","minLength":1},"platform":{"type":"string"},"connection":{"type":"string","enum":["ssh","api","console","unknown"]},"credential_ref":{"type":"string"},"authorized_scope":{"type":"boolean"},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"allowed_commands":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"commands":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":20}},"required":["host","authorized_scope","allowed_hosts","allowed_commands","commands"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "device_config_dry_run",
			Description: "Validate a proposed network-device configuration change, rollback plan, and approval metadata. It only returns dry-run evidence and never applies changes.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"device_id":{"type":"string"},"host":{"type":"string","minLength":1},"platform":{"type":"string"},"connection":{"type":"string","enum":["ssh","api","console","unknown"]},"credential_ref":{"type":"string"},"authorized_scope":{"type":"boolean"},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"allowed_commands":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"change_commands":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"rollback_plan":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"dry_run":{"type":"boolean"},"change_approved":{"type":"boolean"}},"required":["host","authorized_scope","allowed_hosts","allowed_commands","change_commands","rollback_plan","dry_run","change_approved"],"additionalProperties":false}`),
			Kind:        "network",
		},
		{
			Name:        "device_restconf_live_apply",
			Description: "Apply one approved HTTPS RESTCONF/API change to an allowlisted network target. Requires live enablement, authorization, approval, credential ref, dry-run confirmation, rollback evidence, method/path allowlists, and a bounded JSON payload.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"device_id":{"type":"string"},"host":{"type":"string","minLength":1},"platform":{"type":"string"},"connection":{"type":"string","enum":["api","restconf"]},"endpoint":{"type":"string","minLength":1},"method":{"type":"string","enum":["POST","PUT","PATCH"]},"credential_ref":{"type":"string","minLength":1},"approval_ref":{"type":"string","minLength":1},"change_ticket":{"type":"string","minLength":1},"authorized_scope":{"type":"boolean"},"allowed_hosts":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"allowed_methods":{"type":"array","items":{"type":"string","enum":["POST","PUT","PATCH"]},"minItems":1,"maxItems":3},"allowed_paths":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"payload":{"type":"object"},"rollback_plan":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":50},"dry_run_confirmed":{"type":"boolean"},"change_approved":{"type":"boolean"}},"required":["host","endpoint","method","credential_ref","approval_ref","change_ticket","authorized_scope","allowed_hosts","allowed_methods","allowed_paths","payload","rollback_plan","dry_run_confirmed","change_approved"],"additionalProperties":false}`),
			Kind:        "network",
		},
	}
}

func callTool(params json.RawMessage) map[string]any {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	args := decodeDeviceInput(input.Arguments)
	var (
		payload map[string]any
		err     error
	)
	switch input.Name {
	case "device_discovery_plan":
		payload, err = deviceDiscoveryPlan(args)
	case "device_command_plan":
		payload, err = deviceCommandPlan(args)
	case "device_config_dry_run":
		payload, err = deviceConfigDryRun(args)
	case "device_restconf_live_apply":
		payload, err = deviceRestconfLiveApply(args)
	default:
		return map[string]any{"content": "unknown tool", "is_error": true}
	}
	if err != nil {
		return map[string]any{"content": err.Error(), "is_error": true}
	}
	encoded, _ := json.Marshal(payload)
	return map[string]any{"content": string(encoded), "is_error": false}
}

func deviceDiscoveryPlan(input deviceInput) (map[string]any, error) {
	target, err := validateTarget(input)
	if err != nil {
		return nil, err
	}
	allowed := normalizeCommands(input.AllowedCommands, defaultDiscoveryCommands(input.Platform))
	return map[string]any{
		"status":           "planned",
		"mode":             "read_only_discovery",
		"target":           target,
		"credential_ref":   input.CredentialRef,
		"allowed_commands": allowed,
		"recommended_steps": []string{
			"Confirm device identity and maintenance window.",
			"Run only allowlisted show/read commands.",
			"Summarize outputs as evidence artifacts before proposing changes.",
		},
		"requires_approval": false,
		"audit_note":        "No network connection was opened by this planning tool.",
	}, nil
}

func deviceCommandPlan(input deviceInput) (map[string]any, error) {
	target, err := validateTarget(input)
	if err != nil {
		return nil, err
	}
	commands := normalizeCommands(input.Commands, nil)
	if len(commands) == 0 {
		return nil, fmt.Errorf("commands are required")
	}
	allowed, denied := splitAllowedCommands(commands, input.AllowedCommands)
	if len(denied) > 0 {
		return nil, fmt.Errorf("commands outside allowlist: %s", strings.Join(denied, "; "))
	}
	return map[string]any{
		"status":            "approved_plan",
		"mode":              "read_only_commands",
		"target":            target,
		"credential_ref":    input.CredentialRef,
		"commands":          allowed,
		"command_count":     len(allowed),
		"requires_approval": true,
		"approval_reason":   "Read-only device commands should be approved before execution by a real connector.",
		"evidence_contract": []string{"command", "exit_status", "stdout_summary", "raw_output_artifact_ref", "timestamp"},
		"audit_note":        "This MCP server returned a command plan only; it did not connect to the device.",
	}, nil
}

func deviceConfigDryRun(input deviceInput) (map[string]any, error) {
	target, err := validateTarget(input)
	if err != nil {
		return nil, err
	}
	if !input.DryRun {
		return nil, fmt.Errorf("dry_run must be true; this tool never applies device changes")
	}
	if input.ChangeApproved {
		return nil, fmt.Errorf("change_approved must remain false for dry-run planning; use a separate approved connector for real changes")
	}
	changes := normalizeCommands(input.ChangeCommands, nil)
	if len(changes) == 0 {
		return nil, fmt.Errorf("change_commands are required")
	}
	rollback := normalizeCommands(input.RollbackPlan, nil)
	if len(rollback) == 0 {
		return nil, fmt.Errorf("rollback_plan is required")
	}
	allowed, denied := splitAllowedCommands(changes, input.AllowedCommands)
	if len(denied) > 0 {
		return nil, fmt.Errorf("change commands outside allowlist: %s", strings.Join(denied, "; "))
	}
	return map[string]any{
		"status":             "dry_run_only",
		"mode":               "config_change_plan",
		"target":             target,
		"credential_ref":     input.CredentialRef,
		"change_commands":    allowed,
		"rollback_plan":      rollback,
		"precheck_commands":  defaultDiscoveryCommands(input.Platform),
		"postcheck_commands": defaultDiscoveryCommands(input.Platform),
		"requires_approval":  true,
		"approval_reason":    "Real device configuration requires operator approval, a dedicated connector, rollback confirmation, and post-change verification.",
		"risk_level":         "high",
		"audit_note":         "No device configuration was applied. This is a dry-run plan and policy check only.",
	}, nil
}

func deviceRestconfLiveApply(input deviceInput) (map[string]any, error) {
	target, endpoint, payload, authHeader, err := validateLiveApply(input)
	if err != nil {
		return nil, err
	}
	if !liveNetworkToolsEnabled() {
		return map[string]any{
			"status":              "blocked",
			"reason":              "live network tools are disabled; set GOFLOW_NETWORK_TOOLS_ENABLE_LIVE=1 in the MCP server environment after operator approval",
			"mode":                "restconf_live_apply",
			"target":              target,
			"endpoint_path":       endpoint.EscapedPath(),
			"method":              strings.ToUpper(strings.TrimSpace(input.Method)),
			"approval_ref":        strings.TrimSpace(input.ApprovalRef),
			"change_ticket":       strings.TrimSpace(input.ChangeTicket),
			"rollback_step_count": len(normalizeCommands(input.RollbackPlan, nil)),
			"credential_ref":      strings.TrimSpace(input.CredentialRef),
			"live_enabled":        false,
			"requires_env":        "GOFLOW_NETWORK_TOOLS_ENABLE_LIVE=1",
			"audit_note":          "No HTTP request was sent because live execution is disabled.",
		}, nil
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	request, err := http.NewRequest(method, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build RESTCONF request: %w", err)
	}
	request.Header.Set("Authorization", authHeader)
	request.Header.Set("Content-Type", "application/yang-data+json")
	request.Header.Set("Accept", "application/yang-data+json, application/json")
	request.Header.Set("User-Agent", "goflow-network-tools/controlled-live")
	request.Header.Set("X-GoFlow-Approval-Ref", strings.TrimSpace(input.ApprovalRef))
	request.Header.Set("X-GoFlow-Change-Ticket", strings.TrimSpace(input.ChangeTicket))

	started := time.Now().UTC()
	response, err := restconfHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("RESTCONF request failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	status := "remote_rejected"
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		status = "applied"
	}
	return map[string]any{
		"status":              status,
		"mode":                "restconf_live_apply",
		"target":              target,
		"endpoint_path":       endpoint.EscapedPath(),
		"method":              method,
		"http_status":         response.StatusCode,
		"response_preview":    limitString(strings.TrimSpace(string(body)), 1200),
		"approval_ref":        strings.TrimSpace(input.ApprovalRef),
		"change_ticket":       strings.TrimSpace(input.ChangeTicket),
		"rollback_step_count": len(normalizeCommands(input.RollbackPlan, nil)),
		"rollback_summary":    limitedStrings(normalizeCommands(input.RollbackPlan, nil), 3),
		"credential_ref":      strings.TrimSpace(input.CredentialRef),
		"live_enabled":        true,
		"timestamp":           started.Format(time.RFC3339),
		"audit_note":          "A single approved HTTPS RESTCONF/API request was sent. Authorization material was not returned.",
	}, nil
}

func validateLiveApply(input deviceInput) (map[string]any, *url.URL, []byte, string, error) {
	target, err := validateTarget(deviceInput{
		DeviceID:        input.DeviceID,
		Host:            input.Host,
		Platform:        input.Platform,
		Connection:      fallbackString(input.Connection, "api"),
		CredentialRef:   input.CredentialRef,
		AuthorizedScope: input.AuthorizedScope,
		AllowedHosts:    input.AllowedHosts,
	})
	if err != nil {
		return nil, nil, nil, "", err
	}
	if !input.ChangeApproved {
		return nil, nil, nil, "", fmt.Errorf("change_approved must be true for live RESTCONF/API apply")
	}
	if !input.DryRunConfirmed {
		return nil, nil, nil, "", fmt.Errorf("dry_run_confirmed must be true before live RESTCONF/API apply")
	}
	if strings.TrimSpace(input.ApprovalRef) == "" {
		return nil, nil, nil, "", fmt.Errorf("approval_ref is required for live RESTCONF/API apply")
	}
	if strings.TrimSpace(input.ChangeTicket) == "" {
		return nil, nil, nil, "", fmt.Errorf("change_ticket is required for live RESTCONF/API apply")
	}
	rollback := normalizeCommands(input.RollbackPlan, nil)
	if len(rollback) == 0 {
		return nil, nil, nil, "", fmt.Errorf("rollback_plan is required for live RESTCONF/API apply")
	}
	endpoint, err := validateLiveEndpoint(input.Endpoint, input.Host, input.AllowedHosts, input.AllowedPaths)
	if err != nil {
		return nil, nil, nil, "", err
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		return nil, nil, nil, "", fmt.Errorf("method is required for live RESTCONF/API apply")
	}
	if !liveMethodAllowed(method, input.AllowedMethods) {
		return nil, nil, nil, "", fmt.Errorf("method %s is outside allowed_methods", method)
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("payload must be JSON serializable: %w", err)
	}
	if len(payload) == 0 || string(payload) == "null" || string(payload) == "{}" {
		return nil, nil, nil, "", fmt.Errorf("payload is required for live RESTCONF/API apply")
	}
	if len(payload) > 32768 {
		return nil, nil, nil, "", fmt.Errorf("payload exceeds 32768 byte live apply limit")
	}
	authHeader, err := authorizationHeaderFromCredentialRef(input.CredentialRef)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return target, endpoint, payload, authHeader, nil
}

func validateLiveEndpoint(rawEndpoint, expectedHost string, allowedHosts, allowedPaths []string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(rawEndpoint))
	if err != nil {
		return nil, fmt.Errorf("endpoint is invalid: %w", err)
	}
	if endpoint.Scheme != "https" {
		return nil, fmt.Errorf("endpoint must use https")
	}
	host := strings.ToLower(endpoint.Hostname())
	if host == "" {
		return nil, fmt.Errorf("endpoint host is required")
	}
	expected := strings.ToLower(strings.TrimSpace(expectedHost))
	if expected != "" && host != expected {
		return nil, fmt.Errorf("endpoint host %q must match host %q", host, expected)
	}
	if !hostAllowed(host, normalizeHosts(allowedHosts)) {
		return nil, fmt.Errorf("endpoint host %q is not in allowed_hosts", host)
	}
	path := endpoint.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !pathAllowed(path, allowedPaths) {
		return nil, fmt.Errorf("endpoint path %q is outside allowed_paths", path)
	}
	if endpoint.User != nil {
		return nil, fmt.Errorf("endpoint must not contain user info")
	}
	return endpoint, nil
}

func liveMethodAllowed(method string, allowed []string) bool {
	switch method {
	case "POST", "PUT", "PATCH":
	default:
		return false
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), method) {
			return true
		}
	}
	return false
}

func pathAllowed(path string, allowed []string) bool {
	path = "/" + strings.TrimPrefix(strings.TrimSpace(path), "/")
	for _, item := range allowed {
		item = "/" + strings.TrimPrefix(strings.TrimSpace(item), "/")
		switch {
		case item == path:
			return true
		case strings.HasSuffix(item, "*") && strings.HasPrefix(path, strings.TrimSuffix(item, "*")):
			return true
		}
	}
	return false
}

func authorizationHeaderFromCredentialRef(ref string) (string, error) {
	envName := credentialEnvName(ref)
	if envName == "" {
		return "", fmt.Errorf("credential_ref must use env:NAME or env://NAME")
	}
	if !validCredentialEnvName(envName) {
		return "", fmt.Errorf("credential_ref env var name %q is invalid", envName)
	}
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		return "", fmt.Errorf("credential_ref %s is not available in the MCP server environment", ref)
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") {
		return value, nil
	}
	return "Bearer " + value, nil
}

func credentialEnvName(ref string) string {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"env://", "env:"} {
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		}
	}
	return ""
}

var credentialEnvNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func validCredentialEnvName(value string) bool {
	return credentialEnvNameRe.MatchString(value)
}

func liveNetworkToolsEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOFLOW_NETWORK_TOOLS_ENABLE_LIVE")))
	return value == "1" || value == "true" || value == "yes"
}

func validateTarget(input deviceInput) (map[string]any, error) {
	if !input.AuthorizedScope {
		return nil, fmt.Errorf("authorized_scope must be true for network device planning")
	}
	host := strings.TrimSpace(input.Host)
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if !validHost(host) {
		return nil, fmt.Errorf("host %q is not a valid IP or hostname", host)
	}
	allowedHosts := normalizeHosts(input.AllowedHosts)
	if len(allowedHosts) == 0 {
		return nil, fmt.Errorf("allowed_hosts is required")
	}
	if !hostAllowed(host, allowedHosts) {
		return nil, fmt.Errorf("host %q is not in allowed_hosts", host)
	}
	connection := strings.ToLower(strings.TrimSpace(input.Connection))
	if connection == "" {
		connection = "unknown"
	}
	switch connection {
	case "ssh", "api", "restconf", "console", "unknown":
	default:
		return nil, fmt.Errorf("unsupported connection %q", input.Connection)
	}
	return map[string]any{
		"device_id":        strings.TrimSpace(input.DeviceID),
		"host":             host,
		"platform":         strings.ToLower(strings.TrimSpace(input.Platform)),
		"connection":       connection,
		"allowed_hosts":    allowedHosts,
		"authorized_scope": true,
	}, nil
}

func decodeDeviceInput(args map[string]any) deviceInput {
	return deviceInput{
		DeviceID:        stringArg(args, "device_id"),
		Host:            stringArg(args, "host"),
		Platform:        stringArg(args, "platform"),
		Connection:      stringArg(args, "connection"),
		Endpoint:        stringArg(args, "endpoint"),
		Method:          stringArg(args, "method"),
		CredentialRef:   stringArg(args, "credential_ref"),
		ApprovalRef:     stringArg(args, "approval_ref"),
		ChangeTicket:    stringArg(args, "change_ticket"),
		AuthorizedScope: boolArg(args, "authorized_scope"),
		AllowedHosts:    stringListArg(args, "allowed_hosts", 50),
		AllowedCommands: stringListArg(args, "allowed_commands", 50),
		AllowedMethods:  stringListArg(args, "allowed_methods", 3),
		AllowedPaths:    stringListArg(args, "allowed_paths", 50),
		Commands:        stringListArg(args, "commands", 20),
		ChangeCommands:  stringListArg(args, "change_commands", 50),
		Payload:         objectArg(args, "payload"),
		DryRun:          boolArg(args, "dry_run"),
		DryRunConfirmed: boolArg(args, "dry_run_confirmed"),
		ChangeApproved:  boolArg(args, "change_approved"),
		RollbackPlan:    stringListArg(args, "rollback_plan", 50),
	}
}

func splitAllowedCommands(commands, allowed []string) ([]string, []string) {
	allowedPatterns := normalizeCommands(allowed, nil)
	if len(allowedPatterns) == 0 {
		return nil, commands
	}
	accepted := make([]string, 0, len(commands))
	denied := make([]string, 0)
	for _, command := range commands {
		if commandAllowed(command, allowedPatterns) {
			accepted = append(accepted, command)
			continue
		}
		denied = append(denied, command)
	}
	return accepted, denied
}

func commandAllowed(command string, allowed []string) bool {
	normalized := normalizeCommand(command)
	for _, pattern := range allowed {
		pattern = normalizeCommand(pattern)
		if pattern == normalized {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(normalized, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func normalizeCommands(values, fallback []string) []string {
	if len(values) == 0 {
		values = fallback
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizeCommand(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeCommand(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func defaultDiscoveryCommands(platform string) []string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "juniper", "junos":
		return []string{"show version", "show chassis hardware", "show interfaces terse", "show configuration | display set"}
	case "huawei", "vrp":
		return []string{"display version", "display device", "display interface brief", "display current-configuration"}
	default:
		return []string{"show version", "show inventory", "show interfaces status", "show running-config"}
	}
}

func normalizeHosts(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
		value = strings.TrimPrefix(value, "http://")
		value = strings.TrimPrefix(value, "https://")
		value = strings.Trim(value, "/")
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, item := range allowed {
		switch {
		case item == host:
			return true
		case strings.Contains(item, "/"):
			_, network, err := net.ParseCIDR(item)
			if err == nil {
				ip := net.ParseIP(host)
				if ip != nil && network.Contains(ip) {
					return true
				}
			}
		case strings.HasPrefix(item, "*.") && strings.HasSuffix(host, strings.TrimPrefix(item, "*")):
			return true
		}
	}
	return false
}

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

func validHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	return hostnameRe.MatchString(host)
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func boolArg(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func stringListArg(args map[string]any, key string, maximum int) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
		if maximum > 0 && len(out) >= maximum {
			break
		}
	}
	return out
}

func objectArg(args map[string]any, key string) map[string]any {
	value, _ := args[key].(map[string]any)
	return value
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func limitString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func limitedStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return append([]string(nil), values...)
	}
	return append([]string(nil), values[:limit]...)
}

func write(writer *bufio.Writer, resp response) {
	data, _ := json.Marshal(resp)
	_, _ = writer.Write(append(data, '\n'))
	_ = writer.Flush()
}
