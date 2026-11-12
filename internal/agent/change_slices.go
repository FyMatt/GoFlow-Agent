package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/slicescheduler"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const changeSlicesMarker = "goflow-change-slices"

func changeSliceContractDiagnostic(output string, toolResults []schema.ToolResult) string {
	raw, found := extractChangeSlicesJSON(output)
	if !found {
		return "缺少 goflow-change-slices JSON 数组"
	}
	var plans []plannedChangeSlice
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) == 0 || len(plans) > 32 {
		return "变更切片必须是包含 1 至 32 项的合法 JSON 数组"
	}
	for _, plan := range plans {
		if mode := normalizeChangeSliceMode(plan.ExecutionMode); mode != "read" && mode != "write" {
			return fmt.Sprintf("切片 %q 的 mode=%q 无效，只允许 read 或 write；写入测试文件同样使用 write", plan.ID, plan.ExecutionMode)
		}
	}
	reads, discovered := changeSliceReadHashes(toolResults), changeSliceEvidencePaths(toolResults)
	for _, plan := range plans {
		paths, valid := cleanChangeSlicePaths(plan.InputFiles, 64)
		if !valid {
			return fmt.Sprintf("切片 %q 包含不安全的输入路径，必须使用工作区相对路径", plan.ID)
		}
		for _, path := range paths {
			if reads[path] == "" && !changeSliceEvidenceCovers(discovered, path) && !changeSliceDependencyProduces(path, plan.DependsOn, plans) {
				return fmt.Sprintf("切片 %q 的输入 %q 缺少读取证据且不是前置依赖的产物；新建目标不要列为已有输入", plan.ID, path)
			}
		}
	}
	// The generic message above was reached without a specific rule failing in
	// the loop, so replay the compile rules exactly and dump the raw array for
	// later inspection: a silent compile failure with a generic message made
	// this contract impossible to debug from a live run.
	dumpChangeSliceDiagnostic(raw)
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		id := strings.TrimSpace(plan.ID)
		title := strings.TrimSpace(plan.Title)
		if !changeSliceIDPattern.MatchString(id) || id == "" {
			return fmt.Sprintf("切片编号 %q 非法：只能使用字母数字及 . _ -，且必须以字母数字开头", plan.ID)
		}
		if title == "" {
			return fmt.Sprintf("切片 %q 缺少 title", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Sprintf("切片编号 %q 重复", id)
		}
		seen[id] = struct{}{}
		commands := cleanChangeSliceStrings(plan.VerificationCommands, 16, 512)
		if len(commands) == 0 {
			return fmt.Sprintf("切片 %q 没有可用的 verification_commands（不能为空且单条不超过 512 字符）", id)
		}
		targets, targetsOK := cleanChangeSlicePaths(plan.TargetFiles, 64)
		if !targetsOK {
			return fmt.Sprintf("切片 %q 的 target_files 必须是工作区相对路径", id)
		}
		if len(targets) == 0 {
			return fmt.Sprintf("切片 %q 缺少 target_files（若目标是新文件也必须写明相对路径）", id)
		}
		if mode := normalizeChangeSliceMode(plan.ExecutionMode); mode != "read" && mode != "write" {
			return fmt.Sprintf("切片 %q 的 mode=%q 归一化后不是 read 或 write", id, plan.ExecutionMode)
		}
	}
	// Mirror the dependency and scheduler validation: both can reject an
	// otherwise well-formed array without saying why.
	simulated := make([]session.WorkflowChangeSliceSnapshot, 0, len(plans))
	for _, plan := range plans {
		targets, _ := cleanChangeSlicePaths(plan.TargetFiles, 64)
		inputs, _ := cleanChangeSlicePaths(plan.InputFiles, 64)
		simulated = append(simulated, session.WorkflowChangeSliceSnapshot{
			ID:                   strings.TrimSpace(plan.ID),
			Title:                strings.TrimSpace(plan.Title),
			ExecutionMode:        normalizeChangeSliceMode(plan.ExecutionMode),
			DependsOn:            cleanChangeSliceIDs(plan.DependsOn, 16),
			InputFiles:           inputs,
			TargetFiles:          targets,
			VerificationCommands: cleanChangeSliceStrings(plan.VerificationCommands, 16, 512),
		})
	}
	if !validChangeSliceDependencies(simulated) {
		return "切片依赖关系无效：请检查 depends_on 是否指向存在的切片、是否存在循环依赖，以及多个切片是否声明了相同目标文件"
	}
	if _, err := slicescheduler.New(simulated); err != nil {
		return "切片调度校验失败：" + err.Error()
	}
	return "请核对切片编号、依赖、工作区相对路径、实际读取证据和非空验证命令；不得以虚构路径或扩大权限补齐"
}

// dumpChangeSliceDiagnostic stores the model's raw slice array next to the tool
// diagnostics so a failed plan can be inspected without re-running the task.
func dumpChangeSliceDiagnostic(raw []byte) {
	if len(raw) == 0 {
		return
	}
	dir := filepath.Join(os.TempDir(), "goflow-diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "change-slices-raw.json"), raw, 0o644)
}

var changeSliceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var plannedWritePathPattern = regexp.MustCompile("(?i)(?:^|[\\s`\"'(])((?:go\\.mod|README\\.md|requirements\\.txt|app\\.py|(?:cmd|internal|web|src|pkg|docs|templates|static|tests?)/[A-Za-z0-9._/-]+\\.(?:go|py|js|mjs|ts|tsx|jsx|html|css|md|json|ya?ml)))")

type plannedChangeSlice struct {
	ID                   string   `json:"id"`
	Title                string   `json:"title"`
	ExecutionMode        string   `json:"mode"`
	DependsOn            []string `json:"depends_on"`
	InputFiles           []string `json:"input_files"`
	TargetFiles          []string `json:"target_files"`
	VerificationCommands []string `json:"verification_commands"`
}

func compileWorkflowChangeSlices(output string, toolResults []schema.ToolResult) []session.WorkflowChangeSliceSnapshot {
	raw, ok := extractChangeSlicesJSON(output)
	if !ok {
		if strings.Contains(strings.ToLower(output), changeSlicesMarker) {
			return nil
		}
		return synthesizeChangeSlicesFromEvidence(toolResults)
	}
	var plans []plannedChangeSlice
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) == 0 || len(plans) > 32 {
		return nil
	}
	readHashes := changeSliceReadHashes(toolResults)
	discovered := changeSliceEvidencePaths(toolResults)
	seen := make(map[string]struct{}, len(plans))
	compiled := make([]session.WorkflowChangeSliceSnapshot, 0, len(plans))
	for _, plan := range plans {
		plan.ID = strings.TrimSpace(plan.ID)
		plan.Title = strings.TrimSpace(plan.Title)
		if !changeSliceIDPattern.MatchString(plan.ID) || plan.Title == "" {
			return nil
		}
		if _, exists := seen[plan.ID]; exists {
			return nil
		}
		seen[plan.ID] = struct{}{}
		plan.DependsOn = cleanChangeSliceIDs(plan.DependsOn, 16)
		inputFiles, inputOK := cleanChangeSlicePaths(plan.InputFiles, 64)
		targetFiles, targetOK := cleanChangeSlicePaths(plan.TargetFiles, 64)
		if !inputOK || !targetOK {
			return nil
		}
		plan.InputFiles = inputFiles
		plan.TargetFiles = targetFiles
		if len(plan.TargetFiles) == 0 {
			plan.TargetFiles = bindChangeSliceTargets(plan, discovered)
		}
		plan.VerificationCommands = cleanChangeSliceStrings(plan.VerificationCommands, 16, 512)
		plan.ExecutionMode = normalizeChangeSliceMode(plan.ExecutionMode)
		if plan.ExecutionMode != "read" && plan.ExecutionMode != "write" {
			return nil
		}
		if plan.DependsOn == nil {
			plan.DependsOn = []string{}
		}
		if len(plan.TargetFiles) == 0 || len(plan.VerificationCommands) == 0 {
			return nil
		}
		inputHashes := make(map[string]string)
		for _, path := range append(append([]string(nil), plan.InputFiles...), plan.TargetFiles...) {
			if hash := readHashes[path]; hash != "" {
				inputHashes[path] = hash
			}
		}
		for _, path := range plan.InputFiles {
			if inputHashes[path] == "" && !changeSliceEvidenceCovers(discovered, path) && !changeSliceDependencyProduces(path, plan.DependsOn, plans) {
				return nil
			}
		}
		canonical, _ := json.Marshal(struct {
			Plan        plannedChangeSlice `json:"plan"`
			InputHashes map[string]string  `json:"input_hashes"`
		}{Plan: plan, InputHashes: inputHashes})
		digest := sha256.Sum256(canonical)
		compiled = append(compiled, session.WorkflowChangeSliceSnapshot{
			ID:                   plan.ID,
			Title:                plan.Title,
			ExecutionMode:        normalizeChangeSliceMode(plan.ExecutionMode),
			DependsOn:            plan.DependsOn,
			InputFiles:           plan.InputFiles,
			InputHashes:          inputHashes,
			TargetFiles:          plan.TargetFiles,
			VerificationCommands: plan.VerificationCommands,
			ContractHash:         hex.EncodeToString(digest[:]),
			Status:               "planned",
		})
	}
	// Overlapping write slices are safe once ordered. Plans routinely touch the
	// same file in two steps (styles + template) and omitting the bookkeeping
	// dependency made the whole plan fail; serialise them by declared order.
	serializeOverlappingWriteSlices(compiled)
	if !validChangeSliceDependencies(compiled) {
		return nil
	}
	if _, err := slicescheduler.New(compiled); err != nil {
		return nil
	}
	return compiled
}

// serializeOverlappingWriteSlices adds an explicit dependency from the later
// write slice to the earlier one whenever their target files overlap, so the
// scheduler's conflict rule is satisfied without changing execution order.
func serializeOverlappingWriteSlices(slices []session.WorkflowChangeSliceSnapshot) {
	for i := 0; i < len(slices); i++ {
		if normalizeChangeSliceMode(slices[i].ExecutionMode) != "write" {
			continue
		}
		for j := i + 1; j < len(slices); j++ {
			if normalizeChangeSliceMode(slices[j].ExecutionMode) != "write" {
				continue
			}
			if !session.ChangeSliceTargetsOverlap(slices[i].TargetFiles, slices[j].TargetFiles) {
				continue
			}
			if changeSliceReaches(slices, slices[j].DependsOn, slices[i].ID) || changeSliceReaches(slices, slices[i].DependsOn, slices[j].ID) {
				continue
			}
			slices[j].DependsOn = append(slices[j].DependsOn, slices[i].ID)
		}
	}
}

// changeSliceReaches reports whether id is reachable from any of the roots.
func changeSliceReaches(slices []session.WorkflowChangeSliceSnapshot, roots []string, id string) bool {
	if id == "" {
		return false
	}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(current string) bool {
		if current == id {
			return true
		}
		if visited[current] {
			return false
		}
		visited[current] = true
		for _, slice := range slices {
			if slice.ID != current {
				continue
			}
			for _, next := range slice.DependsOn {
				if visit(next) {
					return true
				}
			}
		}
		return false
	}
	for _, root := range roots {
		if visit(root) {
			return true
		}
	}
	return false
}

func changeSliceDependencyProduces(path string, dependencies []string, plans []plannedChangeSlice) bool {
	seen := map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		id = strings.TrimSpace(id)
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, producer := range plans {
			if strings.TrimSpace(producer.ID) != id {
				continue
			}
			if normalizeChangeSliceMode(producer.ExecutionMode) == "write" {
				if targets, valid := cleanChangeSlicePaths(producer.TargetFiles, 64); valid {
					for _, target := range targets {
						if target == path {
							return true
						}
					}
				}
			}
			for _, dependency := range producer.DependsOn {
				if visit(dependency) {
					return true
				}
			}
		}
		return false
	}
	for _, dependency := range dependencies {
		if visit(dependency) {
			return true
		}
	}
	return false
}

func plannedWritePathsFromText(text string) []string {
	text = plannedWritePositiveScope(text)
	var writable []string
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "不修改") || strings.Contains(lower, "未修改") || strings.Contains(lower, "不要修改") || strings.Contains(lower, "只读") || strings.Contains(lower, "read-only") {
			continue
		}
		writeIntent := strings.Contains(lower, "新增") || strings.Contains(lower, "新建") || strings.Contains(lower, "修改") || strings.Contains(lower, "修复") || strings.Contains(lower, "更新") || strings.Contains(lower, "create ") || strings.Contains(lower, "modify ") || strings.Contains(lower, "update ")
		readIntent := strings.Contains(lower, "被测") || strings.Contains(lower, "读取") || strings.Contains(lower, "引用") || strings.Contains(lower, "验证目标")
		if readIntent && !writeIntent {
			continue
		}
		writable = append(writable, line)
	}
	text = strings.Join(writable, "\n")
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, match := range plannedWritePathPattern.FindAllStringSubmatch(text, 32) {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(match[1])))
		if path == "" || path == "." || strings.Contains(path, "..") {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func plannedWritePositiveScope(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	selected := make([]string, 0, len(lines))
	active := false
	foundSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#*-0123456789. "))
		lower := strings.ToLower(heading)
		negative := strings.Contains(lower, "不应修改") || strings.Contains(lower, "预计不修改") || strings.Contains(lower, "不要修改") || strings.Contains(lower, "do not modify") || strings.Contains(lower, "must not modify") || strings.Contains(lower, "out of scope")
		positive := strings.HasPrefix(lower, "target files") || strings.HasPrefix(lower, "files or modules") || strings.HasPrefix(lower, "files changed") || strings.Contains(lower, "预计修改") || strings.Contains(lower, "计划修改") || strings.Contains(lower, "expected modifications")
		switch {
		case negative:
			active = false
			foundSection = true
		case positive:
			active = true
			foundSection = true
		case strings.HasPrefix(trimmed, "#") && foundSection:
			active = false
		}
		if active {
			selected = append(selected, line)
		}
	}
	if !foundSection {
		return text
	}
	return strings.Join(selected, "\n")
}

func synthesizeChangeSlicesFromPaths(paths []string) []session.WorkflowChangeSliceSnapshot {
	results := make([]schema.ToolResult, 0, len(paths))
	for _, path := range uniqueChangeSlicePaths(paths) {
		results = append(results, schema.ToolResult{
			ToolName: "file_tools/list_tree",
			Content:  `{"entries":[{"relative_path":"` + path + `","is_dir":false}]}`,
		})
	}
	return synthesizeChangeSlicesFromEvidenceFiltered(results, false)
}

func synthesizeChangeSlicesFromEvidence(results []schema.ToolResult) []session.WorkflowChangeSliceSnapshot {
	return synthesizeChangeSlicesFromEvidenceFiltered(results, true)
}

func synthesizeChangeSlicesFromEvidenceFiltered(results []schema.ToolResult, skipTests bool) []session.WorkflowChangeSliceSnapshot {
	evidencePaths := changeSliceEvidencePaths(results)
	verificationCommands := defaultChangeSliceVerificationCommands(evidencePaths)
	groups := make(map[string][]string)
	for _, path := range preferChangeSliceEvidencePathsFiltered(evidencePaths, skipTests) {
		if skipChangeSliceEvidencePath(path) && (skipTests || !skipChangeSliceTestPath(path)) {
			continue
		}
		if skipTests && skipChangeSliceTestPath(path) {
			continue
		}
		if !changeSliceSynthesizeSourceFile(path) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir == "." || dir == "" {
			dir = path
		}
		parts := strings.Split(dir, "/")
		if len(parts) >= 2 {
			dir = strings.Join(parts[:2], "/")
		}
		if skipChangeSliceTestPath(path) {
			dir += "-test"
		}
		groups[dir] = append(groups[dir], path)
		if len(groups) >= 12 {
			break
		}
	}
	if len(groups) == 0 {
		return nil
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	compiled := make([]session.WorkflowChangeSliceSnapshot, 0, len(keys))
	for _, key := range keys {
		id := "slice-" + strings.ReplaceAll(key, "/", "-")
		if !changeSliceIDPattern.MatchString(id) {
			continue
		}
		targets := append([]string(nil), groups[key]...)
		sort.Strings(targets)
		if len(targets) == 0 {
			continue
		}
		if len(targets) > 8 {
			targets = targets[:8]
		}
		compiled = append(compiled, session.WorkflowChangeSliceSnapshot{
			ID:                   id,
			Title:                "implement " + key,
			ExecutionMode:        "write",
			DependsOn:            []string{},
			InputFiles:           nil,
			TargetFiles:          targets,
			VerificationCommands: append([]string(nil), verificationCommands...),
			Status:               "planned",
		})
	}
	if !validChangeSliceDependencies(compiled) {
		return nil
	}
	if _, err := slicescheduler.New(compiled); err != nil {
		return nil
	}
	return compiled
}

func defaultChangeSliceVerificationCommands(paths []string) []string {
	for _, path := range paths {
		base := strings.ToLower(filepath.Base(path))
		if base == "go.mod" || strings.EqualFold(filepath.Ext(path), ".go") {
			return []string{"go test ./..."}
		}
	}
	for _, path := range paths {
		if strings.EqualFold(filepath.ToSlash(path), "app.py") {
			return []string{"python -m py_compile app.py"}
		}
	}
	return nil
}

func mergeWriteChangeSlices(base, extra []session.WorkflowChangeSliceSnapshot) []session.WorkflowChangeSliceSnapshot {
	if len(extra) == 0 {
		return base
	}
	out := append([]session.WorkflowChangeSliceSnapshot(nil), base...)
	indexByID := make(map[string]int, len(out))
	for i := range out {
		indexByID[out[i].ID] = i
	}
	covered := func(path string) bool {
		for i := range out {
			if session.ChangeSliceTargetsCover(out[i].TargetFiles, path) {
				return true
			}
			for _, target := range out[i].TargetFiles {
				if filepath.Base(target) == filepath.Base(path) {
					return true
				}
			}
		}
		return false
	}
	for _, slice := range extra {
		if idx, ok := indexByID[slice.ID]; ok {
			out[idx].TargetFiles = uniqueChangeSlicePaths(append(out[idx].TargetFiles, slice.TargetFiles...))
			continue
		}
		missing := false
		for _, path := range slice.TargetFiles {
			if !covered(path) {
				missing = true
				break
			}
		}
		if !missing {
			continue
		}
		indexByID[slice.ID] = len(out)
		out = append(out, slice)
	}
	return out
}

// reopenBlockedChangeSlices gives a repair stage back the slices that an earlier
// failed stage blocked. Without this the repair stage is write-locked on exactly
// the slices it is meant to fix, so every real run ended with no mutations.
func (w *WorkflowRunner) reopenBlockedChangeSlices(stageName string) {
	if w == nil || w.runtime == nil || w.runtime.session == nil || strings.TrimSpace(w.runID) == "" {
		return
	}
	run, ok := w.runtime.session.WorkflowRun(w.runID)
	if !ok || len(run.ChangeSlices) == 0 {
		return
	}
	slices := append([]session.WorkflowChangeSliceSnapshot(nil), run.ChangeSlices...)
	changed := false
	for i := range slices {
		if !strings.EqualFold(strings.TrimSpace(slices[i].Status), slicescheduler.StatusBlocked) {
			continue
		}
		slices[i].Status = slicescheduler.StatusPending
		slices[i].BlockedReason = ""
		changed = true
	}
	if !changed {
		return
	}
	_ = w.runtime.session.ReplaceWorkflowRunChangeSlicesDurable(w.runID, slices)
	w.runtime.session.AppendWorkflowRunEvent(w.runID, session.WorkflowRunEventSnapshot{
		Type:           "change_slice_state",
		Stage:          stageName,
		Content:        "reopened blocked change slices for repair",
		WorkflowName:   w.runtime.SessionSnapshot().Workflow.Name,
		WorkflowStatus: "running",
		NextStage:      stageName,
		Reason:         "change_slice_reopened_for_repair",
	})
}

func reopenWriteSlicesForTargets(slices []session.WorkflowChangeSliceSnapshot, needed []string) []session.WorkflowChangeSliceSnapshot {
	if len(slices) == 0 || len(needed) == 0 {
		return slices
	}
	out := append([]session.WorkflowChangeSliceSnapshot(nil), slices...)
	for i := range out {
		if !strings.EqualFold(strings.TrimSpace(out[i].ExecutionMode), "write") {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(out[i].Status))
		if status != slicescheduler.StatusImplemented && status != slicescheduler.StatusBlocked {
			continue
		}
		matches := false
		for _, path := range needed {
			if session.ChangeSliceTargetsCover(out[i].TargetFiles, path) {
				matches = true
				break
			}
			for _, target := range out[i].TargetFiles {
				if filepath.Base(target) == filepath.Base(path) {
					matches = true
					break
				}
			}
			if matches {
				break
			}
		}
		if !matches {
			continue
		}
		out[i].Status = slicescheduler.StatusPending
		out[i].BlockedReason = ""
	}
	return out
}

func uniqueChangeSlicePaths(paths []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" || path == "." {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func changeSliceEvidenceCovers(discovered []string, path string) bool {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" {
		return false
	}
	for _, item := range discovered {
		item = filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		if item == "" {
			continue
		}
		if item == path {
			return true
		}
		if strings.HasPrefix(path, strings.TrimSuffix(item, "/")+"/") {
			return true
		}
		if strings.HasPrefix(item, strings.TrimSuffix(path, "/")+"/") {
			return true
		}
	}
	return false
}

func normalizeChangeSliceMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "write"
	}
	return mode
}

func extractChangeSlicesJSON(output string) ([]byte, bool) {
	lower := strings.ToLower(output)
	marker := strings.Index(lower, changeSlicesMarker)
	if marker < 0 {
		return nil, false
	}
	after := output[marker+len(changeSlicesMarker):]
	open := strings.Index(after, "[")
	if open < 0 {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(after[open:]))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	return raw, true
}

func changeSliceReadHashes(results []schema.ToolResult) map[string]string {
	hashes := make(map[string]string)
	for _, result := range results {
		if result.IsError || result.Denied {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(result.ToolName))
		if !strings.HasSuffix(name, "read_file") && !strings.HasSuffix(name, "read_file_range") {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(result.Content), &payload) != nil {
			continue
		}
		path, _ := payload["relative_path"].(string)
		hash, _ := payload["sha256"].(string)
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		hash = strings.ToLower(strings.TrimSpace(hash))
		if validChangeSlicePath(path) && len(hash) == 64 {
			hashes[path] = hash
		}
	}
	return hashes
}

func cleanChangeSliceIDs(values []string, limit int) []string {
	clean := cleanChangeSliceStrings(values, limit, 64)
	for _, value := range clean {
		if !changeSliceIDPattern.MatchString(value) {
			return nil
		}
	}
	return clean
}

func cleanChangeSlicePaths(values []string, limit int) ([]string, bool) {
	if len(values) > limit {
		return nil, false
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
		if unsafeChangeSlicePath(value) {
			return nil, false
		}
		if !validChangeSlicePath(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, true
}

func unsafeChangeSlicePath(value string) bool {
	return value == ".." || filepath.IsAbs(value) || strings.HasPrefix(value, "../") || strings.Contains(value, "\x00")
}

func validChangeSlicePath(value string) bool {
	return value != "" && value != "." && !unsafeChangeSlicePath(value) && !strings.ContainsAny(value, "<>*?|\"")
}

func cleanChangeSliceStrings(values []string, limit, maxLength int) []string {
	if len(values) > limit {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxLength {
			return nil
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

var changeSlicePathSynonyms = map[string][]string{
	"page":      {"web", "ui", "frontend", "html"},
	"pages":     {"web", "ui", "frontend", "html"},
	"cli":       {"cmd"},
	"command":   {"cmd"},
	"commands":  {"cmd"},
	"workflow":  {".github", "workflows"},
	"workflows": {".github", "workflows"},
	"ci":        {".github", "workflows"},
}

var changeSliceStopTokens = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "into": {},
	"add": {}, "new": {}, "fix": {}, "make": {}, "test": {}, "tests": {},
	"file": {}, "files": {}, "this": {}, "that": {}, "then": {}, "than": {},
	"must": {}, "will": {}, "only": {}, "also": {},
}

func bindChangeSliceTargets(plan plannedChangeSlice, discovered []string) []string {
	tokens := changeSliceMatchTokens(plan.ID, plan.Title)
	if len(tokens) == 0 || len(discovered) == 0 {
		return nil
	}
	matched := make([]string, 0, 8)
	seen := make(map[string]struct{}, len(discovered))
	for _, path := range preferChangeSliceEvidencePaths(discovered) {
		if !changeSlicePathMatchesTokens(path, tokens) {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		matched = append(matched, path)
		if len(matched) >= 16 {
			break
		}
	}
	sort.Strings(matched)
	return matched
}

func changeSliceMatchTokens(id, title string) []string {
	raw := strings.ToLower(strings.TrimSpace(id) + " " + strings.TrimSpace(title))
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := make(map[string]struct{}, len(parts))
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		if _, stop := changeSliceStopTokens[part]; stop {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		tokens = append(tokens, part)
	}
	return tokens
}

func changeSlicePathMatchesTokens(path string, tokens []string) bool {
	segments := strings.Split(filepath.ToSlash(path), "/")
	names := make([]string, 0, len(segments)*2)
	for _, segment := range segments {
		segment = strings.ToLower(strings.TrimSpace(segment))
		if segment == "" {
			continue
		}
		names = append(names, segment)
		if ext := filepath.Ext(segment); ext != "" {
			names = append(names, strings.TrimSuffix(segment, ext))
		}
	}
	for _, token := range tokens {
		candidates := append([]string{token}, changeSlicePathSynonyms[token]...)
		for _, candidate := range candidates {
			for _, name := range names {
				if name == candidate {
					return true
				}
			}
		}
	}
	return false
}

func preferChangeSliceEvidencePaths(paths []string) []string {
	return preferChangeSliceEvidencePathsFiltered(paths, true)
}

func preferChangeSliceEvidencePathsFiltered(paths []string, skipTests bool) []string {
	preferred := make([]string, 0, len(paths))
	rest := make([]string, 0)
	for _, path := range paths {
		if skipChangeSliceEvidencePath(path) && (skipTests || !skipChangeSliceTestPath(path)) {
			continue
		}
		if preferredChangeSliceEvidencePath(path) {
			preferred = append(preferred, path)
			continue
		}
		rest = append(rest, path)
	}
	// Explicit plan targets are already scoped by the planner; source-file
	// preferences must not erase its templates, stylesheets or tests.
	if !skipTests {
		return append(preferred, rest...)
	}
	if len(preferred) > 0 {
		return preferred
	}
	return rest
}

func changeSliceSynthesizeSourceFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "go.mod" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".mjs", ".ts", ".md", ".json", ".css", ".html":
		return true
	default:
		return false
	}
}

func skipChangeSliceTestPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".spec.js")
}

func skipChangeSliceEvidencePath(path string) bool {
	switch strings.Split(path, "/")[0] {
	case ".git", "node_modules", "vendor", ".cache", "testdata", "fixtures", ".github":
		return true
	default:
		base := strings.ToLower(filepath.Base(path))
		if skipChangeSliceTestPath(path) || strings.Contains(base, "p7_hidden") {
			return true
		}
		return false
	}
}

func preferredChangeSliceEvidencePath(path string) bool {
	if filepath.Dir(path) == "." {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".go", ".py", ".js", ".mjs", ".ts", ".css", ".html":
			return true
		}
	}
	switch strings.Split(path, "/")[0] {
	case "internal", "cmd", "web", "docs", "pkg", "skills", "configs", "mcp_servers", "workflows", ".github", "go.mod":
		return true
	default:
		return false
	}
}

func changeSliceEvidencePaths(results []schema.ToolResult) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(raw string) {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
		if !validChangeSlicePath(path) || path == ".git" || strings.HasPrefix(path, ".git/") {
			return
		}
		if _, exists := seen[path]; exists {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, result := range results {
		if result.IsError || result.Denied {
			continue
		}
		var payload any
		if json.Unmarshal([]byte(result.Content), &payload) != nil {
			continue
		}
		walkChangeSliceEvidence(payload, add)
	}
	sort.Strings(out)
	return out
}

func walkChangeSliceEvidence(value any, add func(string)) {
	switch typed := value.(type) {
	case map[string]any:
		if relative, ok := typed["relative_path"].(string); ok {
			add(relative)
		}
		if path, ok := typed["path"].(string); ok && !filepath.IsAbs(path) {
			add(path)
		}
		for _, child := range typed {
			walkChangeSliceEvidence(child, add)
		}
	case []any:
		for _, child := range typed {
			walkChangeSliceEvidence(child, add)
		}
	}
}

func validChangeSliceDependencies(slices []session.WorkflowChangeSliceSnapshot) bool {
	byID := make(map[string]session.WorkflowChangeSliceSnapshot, len(slices))
	for _, slice := range slices {
		byID[slice.ID] = slice
	}
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, dependency := range byID[id].DependsOn {
			if dependency == id {
				return false
			}
			if _, exists := byID[dependency]; !exists || !visit(dependency) {
				return false
			}
		}
		visiting[id] = false
		visited[id] = true
		return true
	}
	for id := range byID {
		if !visit(id) {
			return false
		}
	}
	return true
}
