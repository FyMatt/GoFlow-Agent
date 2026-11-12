package agent

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

const reportIntegrityError = "报告证据校验失败"

// Opt-in SHA-256 claim validation. This does not certify report semantics.
var reportHashToken = regexp.MustCompile(`\b[0-9a-fA-F]{56,}\b`)
var reportLabeledHash = regexp.MustCompile(`(?i)sha[- ]?256["'\x60*\s]*[:：=]["'\x60*\s]*([a-z0-9+/=_-]+)`)

func reportEvidenceClaims(text string) []string {
	claims := []string{}
	seen := map[[2]int]bool{}
	for _, span := range reportHashToken.FindAllStringIndex(text, -1) {
		seen[[2]int{span[0], span[1]}] = true
		claims = append(claims, text[span[0]:span[1]])
	}
	for _, match := range reportLabeledHash.FindAllStringSubmatchIndex(text, -1) {
		span := [2]int{match[2], match[3]}
		token := text[span[0]:span[1]]
		if seen[span] || strings.EqualFold(token, "unknown") || strings.EqualFold(token, "unavailable") || strings.EqualFold(token, "none") {
			continue
		}
		claims = append(claims, token)
	}
	return claims
}

func validateReportHashes(text string, observations []schema.ToolResult) error {
	known := map[string]bool{}
	fingerprints := map[string]bool{}
	for _, observation := range observations {
		if observation.IsError || observation.Suspended || observation.Denied {
			continue
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(observation.Content), &payload) != nil {
			continue
		}
		var pin string
		if json.Unmarshal(payload["host_key_sha256"], &pin) == nil && strings.HasPrefix(pin, "SHA256:") {
			value := strings.TrimPrefix(pin, "SHA256:")
			decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(value, "="))
			if err == nil && len(decoded) == 32 {
				fingerprints[value] = true
			}
		}
		for _, key := range []string{"sha256", "file_sha256", "content_sha256"} {
			var value string
			if json.Unmarshal(payload[key], &value) == nil && len(value) == 64 {
				known[strings.ToLower(value)] = true
			}
		}
	}
	for _, token := range reportEvidenceClaims(text) {
		if fingerprints[token] {
			continue // SSH fingerprints are base64, not hexadecimal file hashes.
		}
		switch strings.ToLower(token) {
		case "unknown", "unavailable", "none":
			continue // Explicitly unavailable values are not digest claims.
		}
		if len(token) != 64 {
			return fmt.Errorf("%s：SHA-256 值长度为 %d，应为64；不得补猜字符，请引用证据索引条目", reportIntegrityError, len(token))
		}
		if _, err := hex.DecodeString(token); err != nil {
			return fmt.Errorf("%s：SHA-256 值包含非十六进制字符", reportIntegrityError)
		}
		if !known[strings.ToLower(token)] {
			return fmt.Errorf("%s：内联 SHA-256 没有成功采集结果支持；请使用已核对原值或引用证据索引条目", reportIntegrityError)
		}
	}
	return nil
}

func (w *WorkflowRunner) validateReportEvidence(stage workflowGraphStage, result schema.AgentResult) error {
	if !workflowGraphParamBool(stage, "verify_evidence_hashes") {
		return nil
	}
	if len(reportEvidenceClaims(result.Output)) == 0 {
		return nil // Index-only reports need no historical payload hydration.
	}
	observations := append([]schema.ToolResult(nil), result.ToolResults...)
	if w != nil && w.runtime != nil && w.runtime.session != nil {
		if run, ok := w.runtime.session.WorkflowRun(w.runID); ok {
			for _, completed := range run.CompletedStages {
				observations = append(observations, completed.Result.ToolResults...)
			}
		}
		for i, observation := range observations {
			if observation.IsError || observation.Denied || observation.Suspended || json.Valid([]byte(observation.Content)) || observation.ArtifactRef == "" {
				continue
			}
			content := ""
			if artifact, ok := w.runtime.SessionArtifact(observation.ArtifactRef); ok {
				content = artifact.Content
			} else if object, ok, err := w.runtime.ArtifactObject(observation.ArtifactRef); err == nil && ok {
				content = object.Content
			}
			// Retain only hash metadata, not every historical file/log body.
			var fields struct {
				SHA        string `json:"sha256,omitempty"`
				FileSHA    string `json:"file_sha256,omitempty"`
				ContentSHA string `json:"content_sha256,omitempty"`
				HostKey    string `json:"host_key_sha256,omitempty"`
			}
			if json.Unmarshal([]byte(content), &fields) == nil {
				minimal, _ := json.Marshal(fields)
				observations[i].Content = string(minimal)
			}
		}
	}
	return validateReportHashes(result.Output, observations)
}
