package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlanWindowsJSONChangeProducesBoundedExecutableContract(t *testing.T) {
	current := `{"service":{"enabled":false,"port":8080}}`
	desired := `{"service":{"enabled":true,"port":8080}}`
	input := diagnosticInput{
		Path: `C:\ProgramData\Example\service.json`, CurrentContent: current,
		CurrentSHA: testSHA256(current), DesiredContent: desired, DesiredSHA: testSHA256(desired),
	}
	payload, err := planWindowsJSONChange(input)
	if err != nil {
		t.Fatal(err)
	}
	report := payload["report_markdown"].(string)
	for _, want := range []string{
		`$targetPath = 'C:\ProgramData\Example\service.json'`,
		`$tempPath = Join-Path $targetDir 'service.json.goflow.tmp'`,
		`$backupPath = Join-Path $targetDir 'service.json.goflow.bak'`,
		`$rollbackTemp = Join-Path $targetDir 'service.json.goflow.rollback.tmp'`,
		`$rollbackDisplaced = Join-Path $targetDir 'service.json.goflow.rollback.displaced.bak'`,
		`Copy-Item -LiteralPath $backupPath -Destination $rollbackTemp`,
		`[System.IO.File]::Replace($rollbackTemp, $targetPath, $rollbackDisplaced, $true)`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q", want)
		}
	}
	for _, forbidden := range []string{"Join-Path -LiteralPath", " -Force", "GetTempFileName", "GetRandomFileName"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("report contains unsafe or invalid term %q", forbidden)
		}
	}
	if strings.Count(report, input.CurrentSHA) != 1 || strings.Count(report, input.DesiredSHA) != 1 {
		t.Fatal("each supplied hash must appear exactly once")
	}
	if strings.Count(report, "```powershell") != 1 || strings.Count(report, "```") != 2 {
		t.Fatal("report must contain exactly one PowerShell block")
	}
	if len([]rune(report)) > 5500 {
		t.Fatalf("report is too long: %d characters", len([]rune(report)))
	}
}

func TestPlanWindowsJSONChangeRejectsHashMismatch(t *testing.T) {
	_, err := planWindowsJSONChange(diagnosticInput{
		Path: `C:\managed\app.json`, CurrentContent: `{}`, CurrentSHA: strings.Repeat("0", 64),
		DesiredContent: `{"enabled":true}`, DesiredSHA: testSHA256(`{"enabled":true}`),
	})
	if err == nil || !strings.Contains(err.Error(), "current_sha256 does not match") {
		t.Fatalf("expected current hash mismatch, got %v", err)
	}
}

func TestGeneratedWindowsJSONChangeScriptAppliesAndRollsBack(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell execution check is Windows-specific")
	}
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell is unavailable")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "service.json")
	current := `{"service":{"enabled":false,"port":8080}}`
	desired := `{"service":{"enabled":true,"port":8080}}`
	if err := os.WriteFile(target, []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}
	script := renderWindowsJSONChangeScript(target, testSHA256(current), testSHA256(desired), desired,
		"service.json.goflow.tmp", "service.json.goflow.bak", "service.json.goflow.rollback.tmp", "service.json.goflow.rollback.displaced.bak")
	scriptPath := filepath.Join(dir, "change.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"Apply", "Rollback"} {
		command := exec.Command(powerShell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Action", action)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v\n%s", action, err, output)
		}
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != current {
		t.Fatalf("rollback restored %q, want %q", actual, current)
	}
	backup, err := os.ReadFile(target + ".goflow.bak")
	if err != nil || string(backup) != current {
		t.Fatalf("backup was not preserved: content=%q err=%v", backup, err)
	}
	displaced, err := os.ReadFile(target + ".goflow.rollback.displaced.bak")
	if err != nil || string(displaced) != desired {
		t.Fatalf("displaced after-image was not preserved: content=%q err=%v", displaced, err)
	}
}

func testSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
