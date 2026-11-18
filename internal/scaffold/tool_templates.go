package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/tools/python/*.tmpl
var pythonToolTemplateFS embed.FS

type PythonMCPToolTemplateData struct {
	Name       string
	ServerName string
}

func RenderPythonMCPToolCode(runtimeHome, name string) (string, error) {
	return renderPythonToolTemplate(runtimeHome, "server.py.tmpl", pythonToolTemplateData(name))
}

func RenderBinaryAnalysisPythonMCPToolCode(runtimeHome, name string) (string, error) {
	return renderPythonToolTemplate(runtimeHome, "binary-analysis-server.py.tmpl", pythonToolTemplateData(name))
}

func RenderPythonMCPServerConfig(runtimeHome, name string) (string, error) {
	return renderPythonToolTemplate(runtimeHome, "config.yaml.tmpl", pythonToolTemplateData(name))
}

func pythonToolTemplateData(name string) PythonMCPToolTemplateData {
	name = normalizeName(name)
	return PythonMCPToolTemplateData{
		Name:       name,
		ServerName: strings.ReplaceAll(name, "-", "_"),
	}
}

func renderPythonToolTemplate(runtimeHome, templateName string, data PythonMCPToolTemplateData) (string, error) {
	searchPaths := []string{}
	if strings.TrimSpace(runtimeHome) != "" {
		searchPaths = append(searchPaths, filepath.Join(runtimeHome, "templates", "tools", "python", templateName))
	}
	searchPaths = append(searchPaths, filepath.Join("templates", "tools", "python", templateName))
	content, err := readPythonToolTemplate(searchPaths...)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateName).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse python tool template %s: %w", templateName, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render python tool template %s: %w", templateName, err)
	}
	return out.String(), nil
}

func readPythonToolTemplate(paths ...string) (string, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if filepath.IsAbs(path) || len(path) > 1 && path[1] == ':' {
			if data, err := os.ReadFile(path); err == nil {
				return string(data), nil
			}
			continue
		}
		if data, err := pythonToolTemplateFS.ReadFile(filepath.ToSlash(path)); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("python tool template not found: %s", strings.Join(paths, ", "))
}
