package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var windowsAbsolutePathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^\x00-\x1f]+$`)

func planWindowsJSONChange(input diagnosticInput) (map[string]any, error) {
	targetPath := strings.TrimSpace(input.Path)
	if !windowsAbsolutePathPattern.MatchString(targetPath) || strings.HasSuffix(targetPath, `\`) {
		return nil, fmt.Errorf("target_path must be an absolute Windows file path")
	}
	currentContent := input.CurrentContent
	desiredContent := input.DesiredContent
	if len(currentContent) == 0 || len(desiredContent) == 0 {
		return nil, fmt.Errorf("current_content and desired_content are required")
	}
	if len(currentContent) > maxManagedConfigBytes || len(desiredContent) > maxManagedConfigBytes {
		return nil, fmt.Errorf("managed JSON content exceeds %d bytes", maxManagedConfigBytes)
	}
	if !json.Valid([]byte(currentContent)) || !json.Valid([]byte(desiredContent)) {
		return nil, fmt.Errorf("current_content and desired_content must both be valid JSON")
	}
	currentSHA := strings.ToLower(strings.TrimSpace(input.CurrentSHA))
	desiredSHA := strings.ToLower(strings.TrimSpace(input.DesiredSHA))
	if !isManagedSHA256(currentSHA) || !isManagedSHA256(desiredSHA) {
		return nil, fmt.Errorf("current_sha256 and desired_sha256 must contain 64 hexadecimal characters")
	}
	if managedSHA256([]byte(currentContent)) != currentSHA {
		return nil, fmt.Errorf("current_sha256 does not match current_content")
	}
	if managedSHA256([]byte(desiredContent)) != desiredSHA {
		return nil, fmt.Errorf("desired_sha256 does not match desired_content")
	}
	if currentContent == desiredContent {
		return nil, fmt.Errorf("current_content and desired_content are identical")
	}

	baseName := targetPath[strings.LastIndex(targetPath, `\`)+1:]
	tempName := baseName + ".goflow.tmp"
	backupName := baseName + ".goflow.bak"
	rollbackName := baseName + ".goflow.rollback.tmp"
	displacedName := baseName + ".goflow.rollback.displaced.bak"
	script := renderWindowsJSONChangeScript(targetPath, currentSHA, desiredSHA, desiredContent, tempName, backupName, rollbackName, displacedName)
	report := renderWindowsJSONChangeReport(targetPath, currentContent, desiredContent, script, tempName, backupName, rollbackName, displacedName)

	return map[string]any{
		"kind":            "windows_json_change_plan",
		"status":          "ready",
		"read_only":       true,
		"target_os":       "windows",
		"report_markdown": report,
		"report_chars":    len([]rune(report)),
	}, nil
}

func renderWindowsJSONChangeScript(targetPath, currentSHA, desiredSHA, desiredContent, tempName, backupName, rollbackName, displacedName string) string {
	escape := func(value string) string { return strings.ReplaceAll(value, "'", "''") }
	return fmt.Sprintf(`param(
    [Parameter(Mandatory=$true)]
    [ValidateSet('Apply','Rollback')]
    [string]$Action
)

$targetPath = '%s'
$expectedCurrentSha256 = '%s'
$desiredSha256 = '%s'
$desiredContent = '%s'
$targetDir = Split-Path -Parent $targetPath
$tempPath = Join-Path $targetDir '%s'
$backupPath = Join-Path $targetDir '%s'
$rollbackTemp = Join-Path $targetDir '%s'
$rollbackDisplaced = Join-Path $targetDir '%s'

function Get-Sha256([string]$Path) {
    # Use the .NET API directly so the script works on Windows PowerShell 5.1
    # installations where the Microsoft.PowerShell.Utility module is unavailable.
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        [BitConverter]::ToString($sha.ComputeHash([System.IO.File]::ReadAllBytes($Path))).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

if ($Action -eq 'Apply') {
    if (-not (Test-Path -LiteralPath $targetPath -PathType Leaf)) { throw "Target file not found: $targetPath" }
    foreach ($path in @($tempPath, $backupPath, $rollbackTemp, $rollbackDisplaced)) {
        if (Test-Path -LiteralPath $path) { throw "Auxiliary path already exists: $path" }
    }
    if ((Get-Sha256 $targetPath) -ne $expectedCurrentSha256) { throw 'Pre-change hash mismatch' }

    [System.IO.File]::WriteAllText($tempPath, $desiredContent, [System.Text.UTF8Encoding]::new($false))
    if ((Get-Sha256 $tempPath) -ne $desiredSha256) { throw 'Temporary file hash mismatch' }
    [System.IO.File]::Replace($tempPath, $targetPath, $backupPath, $true)

    if ((Get-Sha256 $targetPath) -ne $desiredSha256) { throw 'Post-check target hash mismatch' }
    if ((Get-Sha256 $backupPath) -ne $expectedCurrentSha256) { throw 'Post-check backup hash mismatch' }
    Write-Output 'Apply succeeded'
    return
}

if (-not (Test-Path -LiteralPath $targetPath -PathType Leaf)) { throw "Target file not found: $targetPath" }
if (-not (Test-Path -LiteralPath $backupPath -PathType Leaf)) { throw "Backup file not found: $backupPath" }
if (Test-Path -LiteralPath $rollbackTemp) { throw "Rollback temporary path already exists: $rollbackTemp" }
if (Test-Path -LiteralPath $rollbackDisplaced) { throw "Rollback displaced path already exists: $rollbackDisplaced" }
if ((Get-Sha256 $targetPath) -ne $desiredSha256) { throw 'Pre-rollback target hash mismatch' }
if ((Get-Sha256 $backupPath) -ne $expectedCurrentSha256) { throw 'Pre-rollback backup hash mismatch' }

Copy-Item -LiteralPath $backupPath -Destination $rollbackTemp
if ((Get-Sha256 $rollbackTemp) -ne $expectedCurrentSha256) { throw 'Rollback copy hash mismatch' }
[System.IO.File]::Replace($rollbackTemp, $targetPath, $rollbackDisplaced, $true)

if ((Get-Sha256 $targetPath) -ne $expectedCurrentSha256) { throw 'Post-rollback target hash mismatch' }
if ((Get-Sha256 $backupPath) -ne $expectedCurrentSha256) { throw 'Post-rollback backup hash mismatch' }
if ((Get-Sha256 $rollbackDisplaced) -ne $desiredSha256) { throw 'Post-rollback displaced hash mismatch' }
Write-Output 'Rollback succeeded'`, escape(targetPath), currentSHA, desiredSHA, escape(desiredContent), escape(tempName), escape(backupName), escape(rollbackName), escape(displacedName))
}

func renderWindowsJSONChangeReport(targetPath, currentContent, desiredContent, script, tempName, backupName, rollbackName, displacedName string) string {
	return fmt.Sprintf(`# Windows JSON 受控变更方案

## 快照

- 目标路径：`+"`%s`"+`
- 当前内容：`+"`%s`"+`
- 目标内容：`+"`%s`"+`
- 当前与目标哈希在脚本中分别定义为 `+"`$expectedCurrentSha256`"+` 和 `+"`$desiredSha256`"+`。

## 预期差异

以目标内容整体替换当前 JSON；辅助文件固定为 `+"`%s`"+`、`+"`%s`"+`、`+"`%s`"+` 和 `+"`%s`"+`，均位于目标目录。

## 审批点

操作者核对目标、当前内容、目标内容和两项哈希后，显式选择 `+"`Apply`"+`；需要恢复时重新审批并选择 `+"`Rollback`"+`。本方案未在目标系统执行。

## 执行与回滚脚本

`+"```powershell"+`
%s
`+"```"+`

## 成功信号

- `+"`Apply succeeded`"+`：目标哈希等于 `+"`$desiredSha256`"+`，唯一备份哈希等于 `+"`$expectedCurrentSha256`"+`。
- `+"`Rollback succeeded`"+`：目标和唯一备份哈希均等于 `+"`$expectedCurrentSha256`"+`，回滚时移出的版本哈希等于 `+"`$desiredSha256`"+`。

## 未验证项

目标目录权限、目标系统 PowerShell/.NET 版本、并发写入者和服务重载要求未验证；任一前置条件或哈希不匹配时脚本停止。
`, targetPath, currentContent, desiredContent, tempName, backupName, rollbackName, displacedName, script)
}
