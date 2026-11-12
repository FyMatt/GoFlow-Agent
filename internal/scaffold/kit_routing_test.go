package scaffold

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKitRoutingRoundTripAndClone(t *testing.T) {
	enabled := false
	preset := KitPreset{Name: "lichen", Agents: []string{"curator"}, Routing: &KitRouting{Version: 1, Priority: -10, Enabled: &enabled, MatchTerms: []string{"Lichen survey", "苔藓"}, EntryAgent: "curator", RequiredTools: []string{"field/read"}}}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			var data []byte
			var err error
			var got KitPreset
			if format == "json" {
				data, err = json.Marshal(preset)
				if err == nil {
					err = json.Unmarshal(data, &got)
				}
			} else {
				data, err = yaml.Marshal(preset)
				if err == nil {
					err = yaml.Unmarshal(data, &got)
				}
			}
			if err != nil || !reflect.DeepEqual(preset.Routing, got.Routing) {
				t.Fatalf("roundtrip: got %#v err=%v", got.Routing, err)
			}
		})
	}
	clone := cloneKitPreset(preset)
	clone.Routing.MatchTerms[0] = "changed"
	clone.Routing.RequiredTools[0] = "other/tool"
	*clone.Routing.Enabled = true
	if preset.Routing.MatchTerms[0] != "Lichen survey" || preset.Routing.RequiredTools[0] != "field/read" || *preset.Routing.Enabled {
		t.Fatal("clone shares routing data")
	}
	if !(&KitRouting{}).IsEnabled() || (*KitRouting)(nil).IsEnabled() {
		t.Fatal("optional enable defaults changed")
	}
}

func TestKitRoutingValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		routing *KitRouting
		want    string
	}{
		{"legacy", nil, ""},
		{"agent", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, ""},
		{"workflow", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryWorkflow: "survey"}, ""},
		{"minimum priority", &KitRouting{Version: 1, Priority: -100, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, ""},
		{"maximum priority", &KitRouting{Version: 1, Priority: 100, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, ""},
		{"priority too low", &KitRouting{Version: 1, Priority: -101, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, "routing.priority"},
		{"priority too high", &KitRouting{Version: 1, Priority: 101, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, "routing.priority"},
		{"version", &KitRouting{Version: 2, MatchTerms: []string{"lichen"}, EntryAgent: "curator"}, "version"},
		{"empty terms", &KitRouting{Version: 1, EntryAgent: "curator"}, "match_terms"},
		{"both entries", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryAgent: "curator", EntryWorkflow: "survey"}, "exactly one"},
		{"undeclared", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryAgent: "stranger"}, "included"},
		{"traversal", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryAgent: "../curator"}, "resource name"},
		{"unqualified tool", &KitRouting{Version: 1, MatchTerms: []string{"lichen"}, EntryAgent: "curator", RequiredTools: []string{"read"}}, "server/tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateKitRouting(test.routing, []string{"curator"}, []string{"survey"})
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
		})
	}
}

func TestKitRoutingMaterializedContractUsesGeneratedEntry(t *testing.T) {
	for _, workflow := range []bool{false, true} {
		routing := &KitRouting{Version: 1, Priority: -10, MatchTerms: []string{"lichen specimen"}, EntryAgent: "curator", RequiredTools: []string{"field/read"}}
		if workflow {
			routing.EntryAgent = ""
			routing.EntryWorkflow = "survey"
		}
		content, err := RenderMaterializedKitYAML("", KitPreset{Name: "lichen", Title: "Lichen", Category: "custom", Routing: routing}, MaterializedKitNames{Kit: "generated-kit", Agent: "generated-agent", Workflow: "generated-flow", Skill: "generated-skill", Tool: "generated-helper", WorkflowTemplate: "generated-template", TeamTemplate: "generated-team", PolicyRule: "generated-policy"})
		if err != nil {
			t.Fatal(err)
		}
		var got KitPreset
		if err := yaml.Unmarshal([]byte(content), &got); err != nil {
			t.Fatal(err)
		}
		if got.Routing == nil || got.Routing.Version != 1 || got.Routing.Priority != routing.Priority || !reflect.DeepEqual(got.Routing.MatchTerms, routing.MatchTerms) || !reflect.DeepEqual(got.Routing.RequiredTools, routing.RequiredTools) {
			t.Fatalf("materialized contract lost: %s", content)
		}
		if workflow && got.Routing.EntryWorkflow != "generated-flow" || !workflow && got.Routing.EntryAgent != "generated-agent" {
			t.Fatalf("entry not mapped to generated resources: %#v", got.Routing)
		}
		if err := ValidateKitRouting(got.Routing, got.Agents, got.Workflows); err != nil {
			t.Fatal(err)
		}
		if routing.EntryAgent != "curator" && !workflow || routing.EntryWorkflow != "survey" && workflow {
			t.Fatal("materialization mutated source contract")
		}
	}
}
