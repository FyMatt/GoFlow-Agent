package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type localOfficeCapabilities struct {
	WPSPath         string
	WordPath        string
	ExcelPath       string
	PowerPointPath  string
	LibreOfficePath string
	BrowserPath     string
}

func officeCapabilities() map[string]any {
	capabilities := detectOfficeCapabilities()
	wpsAvailable := capabilities.WPSPath != ""
	microsoftInstalled := capabilities.WordPath != "" && capabilities.ExcelPath != "" && capabilities.PowerPointPath != ""
	microsoftAvailable := microsoftInstalled && probeMicrosoftOfficeCOM()
	libreAvailable := capabilities.LibreOfficePath != ""
	browserAvailable := capabilities.BrowserPath != ""
	nativePDFPreviewAvailable := runtime.GOOS == "windows"
	preferred := "GoFlow 原生文本导出"
	if wpsAvailable {
		preferred = "WPS Office"
	} else if microsoftAvailable {
		preferred = "Microsoft Office"
	} else if libreAvailable {
		preferred = "LibreOffice"
	}
	return map[string]any{
		"platform": runtime.GOOS,
		"wps":      map[string]any{"available": wpsAvailable, "path": capabilities.WPSPath, "description": "WPS 文字、表格和演示兼容转换"},
		"microsoft_office": map[string]any{
			"available":       microsoftAvailable,
			"installed":       microsoftInstalled,
			"word_path":       capabilities.WordPath,
			"excel_path":      capabilities.ExcelPath,
			"powerpoint_path": capabilities.PowerPointPath,
			"description":     "Microsoft Office COM 真实打开和导出",
		},
		"libreoffice":           map[string]any{"available": libreAvailable, "path": capabilities.LibreOfficePath, "description": "LibreOffice 无界面转换"},
		"native_pdf_preview":    map[string]any{"available": nativePDFPreviewAvailable, "description": "Windows 原生 PDF 逐页 PNG 渲染和非空像素校验"},
		"browser_preview":       map[string]any{"available": browserAvailable, "path": capabilities.BrowserPath, "description": "非 Windows 环境的 Chromium PDF 预览后备"},
		"preferred_converter":   preferred,
		"can_create_docx":       true,
		"can_create_xlsx":       true,
		"can_create_pdf":        true,
		"can_create_pptx":       true,
		"pptx_creation_message": pptxCreationMessage(microsoftAvailable),
		"can_export_pdf":        true,
		"native_pdf_export":     map[string]any{"available": true, "description": "无办公套件时从可检索 OOXML 正文生成一致内容的 PDF；不替代版式转换"},
		"can_render_preview":    (nativePDFPreviewAvailable || browserAvailable) && (wpsAvailable || microsoftAvailable || libreAvailable),
		"message":               capabilityMessage(wpsAvailable, microsoftAvailable, libreAvailable, nativePDFPreviewAvailable, browserAvailable),
	}
}

func pptxCreationMessage(powerPointAvailable bool) string {
	if powerPointAvailable {
		return "Microsoft PowerPoint 当前可调用；系统优先使用原生引擎创建 PPTX，并保留结构化 OOXML 后备。"
	}
	return "可使用经过结构校验的 OOXML 引擎创建可编辑 PPTX；PDF 导出仍需要当前可调用的办公套件。"
}

func probeMicrosoftOfficeCOM() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	script := `$ErrorActionPreference='Stop'; $apps=@(); try { foreach($id in @('Word.Application','Excel.Application','PowerPoint.Application')) { $app=New-Object -ComObject $id; $apps += $app }; exit 0 } catch { exit 1 } finally { foreach($app in $apps) { try { $app.Quit() } catch {}; try { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($app) } catch {} } }`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "POWERSHELL_TELEMETRY_OPTOUT=1", "POWERSHELL_UPDATECHECK=Off")
	return command.Run() == nil
}

func validatePPTXOpenability(path string) error {
	if runtime.GOOS == "windows" {
		capabilities := detectOfficeCapabilities()
		if capabilities.PowerPointPath == "" {
			return fmt.Errorf("当前环境未检测到 Microsoft PowerPoint")
		}
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $app=New-Object -ComObject PowerPoint.Application; try { $deck=$app.Presentations.Open('%s',$true,$false,$false); if($deck.Slides.Count -lt 1){ throw 'presentation has no slides' }; $deck.Close() } finally { $app.Quit(); [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($app) }`, quotePowerShell(path))
		var lastErr error
		const attempts = 5
		for attempt := 1; attempt <= attempts; attempt++ {
			if lastErr = runEncodedPowerShell(script, 45*time.Second, ""); lastErr == nil {
				return nil
			}
			if attempt < attempts {
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
		}
		return lastErr
	}
	capabilities := detectOfficeCapabilities()
	if capabilities.LibreOfficePath == "" {
		return fmt.Errorf("当前环境没有可用的 PowerPoint 或 LibreOffice 打开验证器")
	}
	output, err := os.MkdirTemp("", "goflow-pptx-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(output)
	cmd := exec.Command(capabilities.LibreOfficePath, "--headless", "--convert-to", "pdf", "--outdir", output, path)
	if combined, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("LibreOffice 打开验证失败：%v：%s", runErr, strings.TrimSpace(string(combined)))
	}
	if _, statErr := os.Stat(filepath.Join(output, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".pdf")); statErr != nil {
		return fmt.Errorf("LibreOffice 未生成 PPTX 验证输出")
	}
	return nil
}

func capabilityMessage(wps, microsoft, libre, nativePDFPreview, browser bool) string {
	switch {
	case wps && nativePDFPreview:
		return "已检测到 WPS Office，可完成真实文档转换，并使用 Windows 原生 PDF 渲染生成逐页预览。"
	case microsoft && nativePDFPreview:
		return "未检测到 WPS，已自动使用 Microsoft Office 完成真实文档转换，并使用 Windows 原生 PDF 渲染生成逐页预览。"
	case libre && (nativePDFPreview || browser):
		return "未检测到 WPS 或 Microsoft Office，已使用 LibreOffice 完成转换并生成预览。"
	case (wps || microsoft) && browser:
		return "已检测到办公文档转换器，并使用 Chromium 生成预览。"
	case wps || microsoft || libre:
		return "已检测到文档转换器，但未检测到可用浏览器，暂时不能生成预览图。"
	default:
		return "未检测到 WPS、Microsoft Office 或 LibreOffice；将使用 GoFlow 原生文本导出生成 PDF，版式转换需办公套件。"
	}
}

func detectOfficeCapabilities() localOfficeCapabilities {
	capabilities := localOfficeCapabilities{}
	if runtime.GOOS == "windows" {
		capabilities.WPSPath = firstExistingPath(
			`C:\Program Files\Kingsoft\WPS Office\ksolaunch.exe`,
			`C:\Program Files (x86)\Kingsoft\WPS Office\ksolaunch.exe`,
		)
		capabilities.WordPath = firstExistingPath(
			`C:\Program Files\Microsoft Office\root\Office16\WINWORD.EXE`,
			`C:\Program Files (x86)\Microsoft Office\root\Office16\WINWORD.EXE`,
		)
		capabilities.ExcelPath = firstExistingPath(
			`C:\Program Files\Microsoft Office\root\Office16\EXCEL.EXE`,
			`C:\Program Files (x86)\Microsoft Office\root\Office16\EXCEL.EXE`,
		)
		capabilities.PowerPointPath = firstExistingPath(
			`C:\Program Files\Microsoft Office\root\Office16\POWERPNT.EXE`,
			`C:\Program Files (x86)\Microsoft Office\root\Office16\POWERPNT.EXE`,
		)
		capabilities.LibreOfficePath = firstExistingPath(
			`C:\Program Files\LibreOffice\program\soffice.exe`,
			`C:\Program Files (x86)\LibreOffice\program\soffice.exe`,
		)
		capabilities.BrowserPath = firstExistingPath(
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		)
	}
	if capabilities.LibreOfficePath == "" {
		capabilities.LibreOfficePath = lookPath("libreoffice", "soffice")
	}
	if capabilities.BrowserPath == "" {
		capabilities.BrowserPath = lookPath("msedge", "microsoft-edge", "chromium", "google-chrome")
	}
	return capabilities
}

func firstExistingPath(candidates ...string) string {
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func lookPath(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func exportOfficePDF(args map[string]any) (map[string]any, error) {
	rawSource, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	source, err := resolveWorkspacePath(rawSource)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(source); err != nil {
		return nil, fmt.Errorf("源文档不存在：%w", err)
	}
	ext := strings.ToLower(filepath.Ext(source))
	if ext != ".docx" && ext != ".xlsx" && ext != ".pptx" {
		return nil, fmt.Errorf("PDF 导出仅支持 DOCX、XLSX 和 PPTX")
	}
	rawOutput, err := stringArg(args, "output_path", true)
	if err != nil {
		return nil, err
	}
	output, err := resolveWorkspacePath(rawOutput)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Ext(output), ".pdf") {
		return nil, fmt.Errorf("output_path 必须使用 .pdf 扩展名")
	}
	if info, statErr := os.Stat(output); statErr == nil && !info.IsDir() && !boolArg(args, "overwrite", false) {
		return nil, fmt.Errorf("PDF 已存在；确认覆盖时请设置 overwrite=true")
	} else if statErr == nil && info.IsDir() {
		return nil, fmt.Errorf("PDF 输出路径不能是目录")
	}
	if err := ensureNoSymlinkParent(output); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return nil, err
	}
	converter, _ := args["converter"].(string)
	converter = strings.ToLower(strings.TrimSpace(converter))
	if converter == "" {
		converter = "auto"
	}
	used, err := exportOfficePDFFile(source, output, converter)
	if err != nil {
		return nil, err
	}
	return artifactPayload(output, "application/pdf", map[string]any{"format": "PDF", "source_path": relativeWorkspacePath(source), "converter": used})
}

func exportOfficePDFFile(source, output, converter string) (string, error) {
	capabilities := detectOfficeCapabilities()
	if converter == "auto" {
		// A detected executable is not proof that COM or headless conversion is
		// usable (for example, Office may have no interactive logon session).
		// Try each real converter and only then use the deterministic fallback.
		candidates := make([]string, 0, 3)
		if capabilities.WPSPath != "" {
			candidates = append(candidates, "wps")
		}
		if capabilities.WordPath != "" && capabilities.ExcelPath != "" && capabilities.PowerPointPath != "" {
			candidates = append(candidates, "microsoft")
		}
		if capabilities.LibreOfficePath != "" {
			candidates = append(candidates, "libreoffice")
		}
		for _, candidate := range candidates {
			used, err := exportOfficePDFFile(source, output, candidate)
			if err == nil {
				return used, nil
			}
			_ = os.Remove(output)
		}
		return exportWithNativeTextFallback(source, output)
	}
	switch converter {
	case "wps":
		if runtime.GOOS != "windows" || capabilities.WPSPath == "" {
			return "", fmt.Errorf("当前环境未检测到 WPS Office")
		}
		if err := exportWithCOM(source, output, true); err != nil {
			return "", err
		}
		return "WPS Office", nil
	case "microsoft":
		if runtime.GOOS != "windows" || capabilities.WordPath == "" || capabilities.ExcelPath == "" || capabilities.PowerPointPath == "" {
			return "", fmt.Errorf("当前环境未完整安装 Microsoft Office")
		}
		if err := exportWithCOM(source, output, false); err != nil {
			return "", err
		}
		return "Microsoft Office", nil
	case "libreoffice":
		if capabilities.LibreOfficePath == "" {
			return "", fmt.Errorf("当前环境未检测到 LibreOffice")
		}
		if err := exportWithLibreOffice(capabilities.LibreOfficePath, source, output); err != nil {
			return "", err
		}
		return "LibreOffice", nil
	default:
		return "", fmt.Errorf("converter 仅支持 auto、wps、microsoft 或 libreoffice")
	}
}

// exportWithNativeTextFallback keeps PDF delivery available on clean Windows
// and Linux installations. It extracts searchable source text and uses the
// deterministic Go PDF writer; installed Office suites remain preferred.
func exportWithNativeTextFallback(source, output string) (string, error) {
	inspection, err := inspectPath(source, 50000)
	if err != nil {
		return "", fmt.Errorf("native PDF fallback cannot inspect source: %w", err)
	}
	text := strings.TrimSpace(fmt.Sprint(inspection["text_preview"]))
	if text == "" {
		return "", fmt.Errorf("native PDF fallback found no searchable source text")
	}
	lines := strings.Split(text, "\n")
	title := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	data, _ := buildChinesePDF(pdfInput{
		Title:    title,
		Sections: []pdfSection{{Title: "源文档内容（GoFlow 原生文本导出）", Lines: lines}},
	})
	tmp := output + ".goflow.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, output); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return "GoFlow native text fallback", nil
}

func createPPTXWithPowerPoint(input pptxInput) ([]byte, error) {
	if runtime.GOOS != "windows" || detectOfficeCapabilities().PowerPointPath == "" {
		return nil, fmt.Errorf("当前环境未检测到 Microsoft PowerPoint")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "goflow-pptx-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	output := filepath.Join(tempDir, "presentation.pptx")
	encodedData := base64.StdEncoding.EncodeToString(payload)
	script := fmt.Sprintf(`
$ErrorActionPreference='Stop'
$data=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')) | ConvertFrom-Json
$app=$null
$deck=$null
function Get-Rgb([int]$r,[int]$g,[int]$b){ return $r + 256*$g + 65536*$b }
function Add-TextBox($slide,[double]$left,[double]$top,[double]$width,[double]$height,[string]$text,[double]$size,[bool]$bold,[int]$color){
  $shape=$slide.Shapes.AddTextbox(1,$left,$top,$width,$height)
  $shape.TextFrame.WordWrap=-1
  $shape.TextFrame.AutoSize=0
  $shape.TextFrame.MarginLeft=4
  $shape.TextFrame.MarginRight=4
  $shape.TextFrame.MarginTop=2
  $shape.TextFrame.MarginBottom=2
  $range=$shape.TextFrame.TextRange
  $range.Text=$text
  $range.Font.Name='Microsoft YaHei'
  try{$range.Font.NameFarEast='Microsoft YaHei'}catch{}
  $range.Font.Size=$size
  $range.Font.Bold=if($bold){-1}else{0}
  $range.Font.Color.RGB=$color
  return $shape
}
try {
  $app=New-Object -ComObject PowerPoint.Application
  $deck=$app.Presentations.Add()
  $deck.PageSetup.SlideWidth=960
  $deck.PageSetup.SlideHeight=540
  $slide=$deck.Slides.Add(1,12)
  $slide.FollowMasterBackground=0
  $slide.Background.Fill.Solid()
  $slide.Background.Fill.ForeColor.RGB=Get-Rgb 248 250 252
  $null=Add-TextBox $slide 70 150 820 90 ([string]$data.title) 30 $true (Get-Rgb 31 41 55)
  if(-not [string]::IsNullOrWhiteSpace([string]$data.subtitle)){
    $null=Add-TextBox $slide 74 255 810 55 ([string]$data.subtitle) 16 $false (Get-Rgb 75 85 99)
  }
  $index=2
  foreach($item in @($data.slides)){
    $slide=$deck.Slides.Add($index,12)
    $slide.FollowMasterBackground=0
    $slide.Background.Fill.Solid()
    $slide.Background.Fill.ForeColor.RGB=Get-Rgb 248 250 252
    $null=Add-TextBox $slide 62 45 835 62 ([string]$item.title) 24 $true (Get-Rgb 31 41 55)
    $body=''
    if(@($item.bullets).Count -gt 0){
      $body=(@($item.bullets) | ForEach-Object { '• ' + [string]$_ }) -join [Environment]::NewLine
    } elseif(-not [string]::IsNullOrWhiteSpace([string]$item.subtitle)) {
      $body=[string]$item.subtitle
    }
    if(-not [string]::IsNullOrWhiteSpace($body)){
      $shape=Add-TextBox $slide 72 135 810 310 $body 18 $false (Get-Rgb 55 65 81)
      $shape.TextFrame.TextRange.ParagraphFormat.SpaceAfter=10
    }
    $null=Add-TextBox $slide 870 505 45 20 ([string]$index) 9 $false (Get-Rgb 107 114 128)
    $index++
  }
  $deck.SaveAs('%s',24)
} finally {
  if($null -ne $deck){ try{$deck.Close()}catch{} }
  if($null -ne $app){ try{$app.Quit()}catch{} }
  [GC]::Collect()
  [GC]::WaitForPendingFinalizers()
}`, encodedData, quotePowerShell(output))
	if err := runEncodedPowerShell(script, 90*time.Second, "POWERPNT.EXE"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("读取 PowerPoint 产物失败：%w", err)
	}
	if len(data) < 1024 {
		return nil, fmt.Errorf("PowerPoint 生成的 PPTX 为空或不完整")
	}
	return data, nil
}

func exportWithCOM(source, output string, wps bool) error {
	ext := strings.ToLower(filepath.Ext(source))
	progID := ""
	if wps {
		switch ext {
		case ".docx":
			progID = "KWPS.Application"
		case ".xlsx":
			progID = "ket.Application"
		case ".pptx":
			progID = "kwpp.Application"
		}
	} else {
		switch ext {
		case ".docx":
			progID = "Word.Application"
		case ".xlsx":
			progID = "Excel.Application"
		case ".pptx":
			progID = "PowerPoint.Application"
		}
	}
	src := quotePowerShell(source)
	dst := quotePowerShell(output)
	var action string
	switch ext {
	case ".docx":
		action = fmt.Sprintf(`$doc=$app.Documents.Open('%s',$false,$true); $doc.ExportAsFixedFormat('%s',17); $doc.Close(0)`, src, dst)
	case ".xlsx":
		action = fmt.Sprintf(`$book=$app.Workbooks.Open('%s',0,$true); $book.ExportAsFixedFormat(0,'%s'); $book.Close($false)`, src, dst)
	case ".pptx":
		action = fmt.Sprintf(`$deck=$app.Presentations.Open('%s',$true,$false,$false); $deck.SaveAs('%s',32); $deck.Close()`, src, dst)
	}
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $app=$null; try { $app=New-Object -ComObject '%s'; try{$app.Visible=$false}catch{}; try{$app.DisplayAlerts=0}catch{}; try{$app.AutomationSecurity=3}catch{}; %s } finally { if($null -ne $app){ try{$app.Quit()}catch{} }; [GC]::Collect(); [GC]::WaitForPendingFinalizers() }`, progID, action)
	if err := runEncodedPowerShell(script, 75*time.Second, officeProcessName(ext)); err != nil {
		return fmt.Errorf("Office 导出 PDF 失败：%w", err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		return fmt.Errorf("Office 未生成有效 PDF")
	}
	return nil
}

func exportWithLibreOffice(binaryPath, source, output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	outDir := filepath.Dir(output)
	cmd := exec.CommandContext(ctx, binaryPath, "--headless", "--convert-to", "pdf", "--outdir", outDir, source)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("LibreOffice 转换失败：%v：%s", err, strings.TrimSpace(string(combined)))
	}
	generated := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".pdf")
	if !strings.EqualFold(generated, output) {
		if err := os.Rename(generated, output); err != nil {
			return err
		}
	}
	return nil
}

func runEncodedPowerShell(script string, timeout time.Duration, officeProcess string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("COM 转换仅支持 Windows")
	}
	utf16 := make([]byte, 0, len(script)*2)
	for _, r := range []rune(script) {
		if r <= 0xFFFF {
			var buffer [2]byte
			binary.LittleEndian.PutUint16(buffer[:], uint16(r))
			utf16 = append(utf16, buffer[:]...)
		} else {
			r -= 0x10000
			high := uint16(0xD800 + (r >> 10))
			low := uint16(0xDC00 + (r & 0x3FF))
			var buffer [4]byte
			binary.LittleEndian.PutUint16(buffer[0:2], high)
			binary.LittleEndian.PutUint16(buffer[2:4], low)
			utf16 = append(utf16, buffer[:]...)
		}
	}
	encoded := base64.StdEncoding.EncodeToString(utf16)
	existing := windowsProcessIDs(officeProcess)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	logFile, logErr := os.CreateTemp("", "goflow-office-com-*.log")
	if logErr != nil {
		return logErr
	}
	logPath := logFile.Name()
	defer os.Remove(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	err := cmd.Run()
	_ = logFile.Close()
	combined, _ := os.ReadFile(logPath)
	if ctx.Err() == context.DeadlineExceeded {
		cleanupNewOfficeProcesses(officeProcess, existing)
		return fmt.Errorf("转换超时")
	}
	if err != nil {
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

func officeProcessName(ext string) string {
	switch ext {
	case ".docx":
		return "WINWORD.EXE"
	case ".xlsx":
		return "EXCEL.EXE"
	case ".pptx":
		return "POWERPNT.EXE"
	default:
		return ""
	}
}

func windowsProcessIDs(imageName string) map[int]bool {
	ids := map[int]bool{}
	if runtime.GOOS != "windows" || imageName == "" {
		return ids
	}
	output, err := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq "+imageName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ids
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(strings.TrimSpace(line), `","`)
		if len(fields) < 2 {
			continue
		}
		pidText := strings.Trim(fields[1], `"`)
		if pid, parseErr := strconv.Atoi(pidText); parseErr == nil {
			ids[pid] = true
		}
	}
	return ids
}

func cleanupNewOfficeProcesses(imageName string, existing map[int]bool) {
	for pid := range windowsProcessIDs(imageName) {
		if existing[pid] {
			continue
		}
		_ = exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	}
}

func quotePowerShell(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func renderOfficePreview(args map[string]any) (map[string]any, error) {
	raw, err := stringArg(args, "path", true)
	if err != nil {
		return nil, err
	}
	source, err := resolveWorkspacePath(raw)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(source); err != nil {
		return nil, err
	}
	rawDir, err := stringArg(args, "output_dir", true)
	if err != nil {
		return nil, err
	}
	outputDir, err := resolveWorkspacePath(rawDir)
	if err != nil {
		return nil, err
	}
	if err := ensureNoSymlinkParent(filepath.Join(outputDir, "preview.png")); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, err
	}
	capabilities := detectOfficeCapabilities()
	if runtime.GOOS != "windows" && capabilities.BrowserPath == "" {
		return nil, fmt.Errorf("未检测到可用于预览的 Edge 或 Chromium 浏览器")
	}
	pdfPath := source
	generatedPDF := false
	if !strings.EqualFold(filepath.Ext(source), ".pdf") {
		pdfPath = filepath.Join(outputDir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".pdf")
		if _, err := exportOfficePDFFile(source, pdfPath, "auto"); err != nil {
			return nil, err
		}
		generatedPDF = true
	}
	hash, _, err := fileSHA256(source)
	if err != nil {
		return nil, err
	}
	width := intArg(args, "width", 1440, 640, 2400)
	height := intArg(args, "height", 1000, 480, 1800)
	maxPages := intArg(args, "max_pages", 5, 1, 20)
	prefix := "preview-" + hash[:12]
	var previews []string
	if runtime.GOOS == "windows" {
		previews, err = renderPDFPagesWithWindows(pdfPath, outputDir, prefix, maxPages, width)
	} else {
		preview := filepath.Join(outputDir, prefix+"-page-1.png")
		if err = renderPDFPageWithBrowser(capabilities.BrowserPath, pdfPath, preview, width, height); err == nil {
			previews = []string{preview}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(previews) == 0 {
		return nil, fmt.Errorf("未生成任何文档预览页")
	}
	previewPaths := make([]string, 0, len(previews))
	previewChecks := make([]map[string]any, 0, len(previews))
	for _, preview := range previews {
		if info, statErr := os.Stat(preview); statErr != nil || info.Size() == 0 {
			return nil, fmt.Errorf("预览图不存在或为空：%s", relativeWorkspacePath(preview))
		}
		previewCheck, inspectErr := inspectPreviewPNG(preview)
		if inspectErr != nil {
			return nil, inspectErr
		}
		previewPaths = append(previewPaths, relativeWorkspacePath(preview))
		previewChecks = append(previewChecks, previewCheck)
	}
	result := map[string]any{
		"source_path":    relativeWorkspacePath(source),
		"preview_paths":  previewPaths,
		"preview_mime":   "image/png",
		"rendered_pages": len(previewPaths),
		"preview_checks": previewChecks,
	}
	if generatedPDF {
		result["pdf_path"] = relativeWorkspacePath(pdfPath)
	}
	return result, nil
}

func renderPDFPagesWithWindows(pdfPath, outputDir, prefix string, maxPages, width int) ([]string, error) {
	for page := 1; page <= maxPages; page++ {
		_ = os.Remove(filepath.Join(outputDir, fmt.Sprintf("%s-page-%d.png", prefix, page)))
	}
	script := fmt.Sprintf(`
$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$null=[Windows.Storage.StorageFile,Windows.Storage,ContentType=WindowsRuntime]
$null=[Windows.Data.Pdf.PdfDocument,Windows.Data.Pdf,ContentType=WindowsRuntime]
$null=[Windows.Data.Pdf.PdfPageRenderOptions,Windows.Data.Pdf,ContentType=WindowsRuntime]
$null=[Windows.Storage.Streams.InMemoryRandomAccessStream,Windows.Storage.Streams,ContentType=WindowsRuntime]
$null=[Windows.Storage.Streams.DataReader,Windows.Storage.Streams,ContentType=WindowsRuntime]
$asTaskMethods=[System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' }
function Await-Result($operation,[Type]$resultType) {
  $method=$asTaskMethods | Where-Object { $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 } | Select-Object -First 1
  $task=$method.MakeGenericMethod($resultType).Invoke($null,@($operation))
  $task.Wait()
  return $task.Result
}
function Await-Action($operation) {
  $method=$asTaskMethods | Where-Object { -not $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 } | Select-Object -First 1
  $task=$method.Invoke($null,@($operation))
  $task.Wait()
}
$file=Await-Result ([Windows.Storage.StorageFile]::GetFileFromPathAsync('%s')) ([Windows.Storage.StorageFile])
$document=Await-Result ([Windows.Data.Pdf.PdfDocument]::LoadFromFileAsync($file)) ([Windows.Data.Pdf.PdfDocument])
$limit=[Math]::Min([int]$document.PageCount,%d)
for($index=0;$index -lt $limit;$index++) {
  $page=$document.GetPage([uint32]$index)
  $stream=New-Object Windows.Storage.Streams.InMemoryRandomAccessStream
  $options=New-Object Windows.Data.Pdf.PdfPageRenderOptions
  $options.DestinationWidth=[uint32]%d
  Await-Action ($page.RenderToStreamAsync($stream,$options))
  $stream.Seek(0)
  $reader=New-Object Windows.Storage.Streams.DataReader($stream.GetInputStreamAt(0))
  $loaded=Await-Result ($reader.LoadAsync([uint32]$stream.Size)) ([UInt32])
  $bytes=New-Object byte[] $loaded
  $reader.ReadBytes($bytes)
  $output=Join-Path '%s' ('%s-page-'+($index+1)+'.png')
  [IO.File]::WriteAllBytes($output,$bytes)
  $reader.Dispose()
  $stream.Dispose()
  $page.Dispose()
}
`, quotePowerShell(pdfPath), maxPages, width, quotePowerShell(outputDir), quotePowerShell(prefix))
	if err := runEncodedPowerShell(script, 90*time.Second, ""); err != nil {
		return nil, fmt.Errorf("Windows PDF 预览渲染失败：%w", err)
	}
	paths := make([]string, 0, maxPages)
	for page := 1; page <= maxPages; page++ {
		path := filepath.Join(outputDir, fmt.Sprintf("%s-page-%d.png", prefix, page))
		if _, err := os.Stat(path); err != nil {
			break
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func renderPDFPageWithBrowser(browserPath, pdfPath, preview string, width, height int) error {
	profileDir, err := os.MkdirTemp("", "goflow-office-preview-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profileDir)
	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(pdfPath)}).String() + "#page=1&zoom=page-width"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	browserImage := filepath.Base(browserPath)
	existingBrowserProcesses := windowsProcessIDs(browserImage)
	cmd := exec.CommandContext(ctx, browserPath,
		"--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
		"--allow-file-access-from-files", "--disable-background-networking", "--disable-component-update",
		"--run-all-compositor-stages-before-draw", "--virtual-time-budget=7000",
		"--user-data-dir="+profileDir,
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"--screenshot="+preview,
		fileURL,
	)
	logFile, logErr := os.CreateTemp("", "goflow-office-preview-*.log")
	if logErr != nil {
		return logErr
	}
	logPath := logFile.Name()
	defer os.Remove(logPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	err = cmd.Run()
	_ = logFile.Close()
	combined, _ := os.ReadFile(logPath)
	if runtime.GOOS == "windows" {
		cleanupNewOfficeProcesses(browserImage, existingBrowserProcesses)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("生成文档预览超时")
	}
	if err != nil {
		return fmt.Errorf("生成文档预览失败：%v：%s", err, strings.TrimSpace(string(combined)))
	}
	if info, err := os.Stat(preview); err != nil || info.Size() == 0 {
		return fmt.Errorf("浏览器未生成有效预览图")
	}
	return nil
}

func inspectPreviewPNG(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取预览图失败：%w", err)
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("预览图无法解码：%w", err)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 320 || height < 240 {
		return nil, fmt.Errorf("预览图尺寸异常：%dx%d", width, height)
	}
	step := 1
	for (width/step)*(height/step) > 100000 {
		step++
	}
	minLuma, maxLuma := 255, 0
	var count int
	var sum, sumSquares float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, _ := decoded.At(x, y).RGBA()
			luma := int((299*(r>>8) + 587*(g>>8) + 114*(b>>8)) / 1000)
			if luma < minLuma {
				minLuma = luma
			}
			if luma > maxLuma {
				maxLuma = luma
			}
			value := float64(luma)
			sum += value
			sumSquares += value * value
			count++
		}
	}
	if count == 0 {
		return nil, fmt.Errorf("预览图没有可检查像素")
	}
	mean := sum / float64(count)
	variance := sumSquares/float64(count) - mean*mean
	if maxLuma-minLuma < 15 || variance < 20 {
		return nil, fmt.Errorf("预览图像素接近单色，可能为空白或渲染失败：亮度范围=%d，方差=%.2f", maxLuma-minLuma, variance)
	}
	return map[string]any{
		"width":          width,
		"height":         height,
		"sampled_pixels": count,
		"luma_min":       minLuma,
		"luma_max":       maxLuma,
		"luma_mean":      mean,
		"luma_variance":  variance,
	}, nil
}
