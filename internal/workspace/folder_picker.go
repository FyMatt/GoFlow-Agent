package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const hostFolderPickerEnv = "GOFLOW_ENABLE_HOST_FOLDER_PICKER"

type FolderPickerCapability struct {
	Available bool
	Mode      string
	Reason    string
	Platform  string
	OptInEnv  string
	Command   string
}

func HostFolderPickerCapability() FolderPickerCapability {
	enabled := folderPickerEnvEnabled()
	platform := runtime.GOOS
	capability := FolderPickerCapability{
		Available: false,
		Mode:      "client_or_text",
		Reason:    "host folder picker is disabled by default; use a browser/client picker or type a workspace path",
		Platform:  platform,
		OptInEnv:  hostFolderPickerEnv,
	}
	if !enabled {
		return capability
	}
	command, reason := hostFolderPickerCommand()
	if strings.TrimSpace(command) == "" {
		capability.Mode = "unavailable"
		capability.Reason = reason
		return capability
	}
	capability.Available = true
	capability.Mode = "host_dialog"
	capability.Reason = "host folder picker is enabled for this local runtime"
	capability.Command = command
	return capability
}

func PickFolder(ctx context.Context, initialPath string) (string, bool, error) {
	capability := HostFolderPickerCapability()
	if !capability.Available {
		return "", false, fmt.Errorf("host folder picker unavailable: %s", capability.Reason)
	}
	initialPath = NormalizePath(initialPath)
	output, err := runHostFolderPicker(ctx, capability.Command, initialPath)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", true, nil
		}
		return "", false, err
	}
	path := NormalizePath(strings.TrimSpace(output))
	if path == "" {
		return "", true, nil
	}
	if stat, err := os.Stat(path); err != nil {
		return "", false, fmt.Errorf("selected workspace does not exist: %w", err)
	} else if !stat.IsDir() {
		return "", false, fmt.Errorf("selected workspace is not a directory: %s", path)
	}
	return path, false, nil
}

func folderPickerEnvEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(hostFolderPickerEnv))) {
	case "1", "true", "yes", "y", "on", "enabled":
		return true
	default:
		return false
	}
}

func hostFolderPickerCommand() (string, string) {
	switch runtime.GOOS {
	case "windows":
		for _, name := range []string{"powershell.exe", "powershell", "pwsh.exe", "pwsh"} {
			if path, err := exec.LookPath(name); err == nil {
				return path, ""
			}
		}
		return "", "PowerShell was not found; type a path or enable a client-side picker"
	case "darwin":
		if path, err := exec.LookPath("osascript"); err == nil {
			return path, ""
		}
		return "", "osascript was not found; type a path or use a client-side picker"
	default:
		if path, err := exec.LookPath("zenity"); err == nil {
			return path, ""
		}
		if path, err := exec.LookPath("kdialog"); err == nil {
			return path, ""
		}
		if strings.TrimSpace(os.Getenv("DISPLAY")) == "" && strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
			return "", "no graphical Linux session was detected; type a path or use a client-side picker"
		}
		return "", "no supported Linux folder picker command was found; install zenity or kdialog, or type a path"
	}
}

func runHostFolderPicker(ctx context.Context, command, initialPath string) (string, error) {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(command), ".exe"))
	switch base {
	case "powershell", "pwsh":
		args := []string{"-NoProfile"}
		if base == "powershell" {
			args = append(args, "-STA")
		}
		args = append(args, "-Command", windowsFolderPickerScript(initialPath))
		data, err := exec.CommandContext(ctx, command, args...).Output()
		return string(data), err
	case "osascript":
		script := `POSIX path of (choose folder with prompt "Choose GoFlow workspace"` + macOSDefaultLocationClause(initialPath) + `)`
		data, err := exec.CommandContext(ctx, command, "-e", script).Output()
		return string(data), err
	case "zenity":
		args := []string{"--file-selection", "--directory", "--title=Choose GoFlow workspace"}
		if initialPath != "" {
			args = append(args, "--filename="+filepath.Clean(initialPath)+string(os.PathSeparator))
		}
		data, err := exec.CommandContext(ctx, command, args...).Output()
		return string(data), err
	case "kdialog":
		args := []string{"--getexistingdirectory"}
		if initialPath != "" {
			args = append(args, initialPath)
		}
		data, err := exec.CommandContext(ctx, command, args...).Output()
		return string(data), err
	default:
		return "", fmt.Errorf("unsupported host folder picker command: %s", command)
	}
}

func windowsFolderPickerScript(initialPath string) string {
	escaped := strings.ReplaceAll(initialPath, "'", "''")
	return `
Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = "Choose GoFlow workspace"
$dialog.ShowNewFolderButton = $true
$initial = '` + escaped + `'
if ($initial -and (Test-Path -LiteralPath $initial -PathType Container)) {
  $dialog.SelectedPath = $initial
}
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
  [Console]::Out.WriteLine($dialog.SelectedPath)
}
`
}

func macOSDefaultLocationClause(initialPath string) string {
	initialPath = strings.TrimSpace(initialPath)
	if initialPath == "" {
		return ""
	}
	escaped := strings.ReplaceAll(initialPath, `"`, `\"`)
	return ` default location POSIX file "` + escaped + `"`
}
