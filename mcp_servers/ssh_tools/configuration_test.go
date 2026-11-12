package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestShippedSSHEnvironmentSupportsWindowsClient(t *testing.T) {
	for _, path := range []string{"mcp_servers/ssh_tools.yaml", "goflow.binary.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "configs", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Servers []struct {
				Name string   `yaml:"name"`
				Env  []string `yaml:"env_allowlist"`
			} `yaml:"mcp_servers"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, server := range doc.Servers {
			if server.Name != "ssh_tools" {
				continue
			}
			found = true
			allowed := map[string]bool{}
			for _, name := range server.Env {
				allowed[strings.ToUpper(name)] = true
			}
			for _, required := range []string{"PROGRAMFILES", "PROGRAMDATA", "USERNAME", "USERDOMAIN", "SYSTEMROOT", "USERPROFILE"} {
				if !allowed[required] {
					t.Fatalf("%s filters Windows SSH dependency %s", path, required)
				}
			}
		}
		if !found {
			t.Fatalf("%s has no SSH server", path)
		}
	}
}
