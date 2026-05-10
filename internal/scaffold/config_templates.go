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

//go:embed templates/config/agents/*.tmpl templates/config/providers/*.tmpl
var configTemplateFS embed.FS

type AgentConfigTemplateData struct {
	Name  string
	Title string
}

type ProviderConfigTemplateData struct {
	Name      string
	EnvAPIKey string
}

func RenderAgentConfigTemplate(runtimeHome, name string) (string, error) {
	name = normalizeName(name)
	data := AgentConfigTemplateData{
		Name:  name,
		Title: strings.ReplaceAll(name, "-", " "),
	}
	return renderConfigTemplate(runtimeHome, "agents", "default.yaml.tmpl", data)
}

func RenderProviderConfigTemplate(runtimeHome, name string) (string, error) {
	name = normalizeName(name)
	envName := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_API_KEY"
	return renderConfigTemplate(runtimeHome, "providers", "default.yaml.tmpl", ProviderConfigTemplateData{
		Name:      name,
		EnvAPIKey: envName,
	})
}

func renderConfigTemplate(runtimeHome, family, templateName string, data any) (string, error) {
	searchPaths := []string{}
	if strings.TrimSpace(runtimeHome) != "" {
		searchPaths = append(searchPaths, filepath.Join(runtimeHome, "templates", "config", family, templateName))
	}
	searchPaths = append(searchPaths, filepath.Join("templates", "config", family, templateName))
	content, err := readConfigTemplate(searchPaths...)
	if err != nil {
		return "", err
	}
	tmpl, err := template.New(templateName).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", fmt.Errorf("parse config template %s/%s: %w", family, templateName, err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render config template %s/%s: %w", family, templateName, err)
	}
	return out.String(), nil
}

func readConfigTemplate(paths ...string) (string, error) {
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
		if data, err := configTemplateFS.ReadFile(filepath.ToSlash(path)); err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("config template not found: %s", strings.Join(paths, ", "))
}
