package nativesandbox

import (
	"runtime"
	"strings"
	"testing"
)

func TestProbeDoesNotOverstateStrongSandbox(t *testing.T) {
	report := Probe()
	if report.Platform != runtime.GOOS || report.Architecture != runtime.GOARCH {
		t.Fatalf("unexpected platform report: %#v", report)
	}
	if report.StrongReady {
		if report.Level != "strong" || len(report.Missing) != 0 || report.RefusalReason != "" {
			t.Fatalf("ready native executor has inconsistent capability report: %#v", report)
		}
	} else {
		if report.Level != "controlled" || len(report.Missing) == 0 || !strings.Contains(report.RefusalReason, "拒绝") {
			t.Fatalf("expected explicit Chinese refusal reason: %#v", report)
		}
	}
	appAvailable := false
	for _, feature := range report.Features {
		if feature.Name == "appcontainer" {
			appAvailable = feature.Available
		}
	}
	for _, feature := range report.Features {
		if feature.Enforced && !feature.Implemented {
			t.Fatalf("feature %q cannot be enforced before implementation", feature.Name)
		}
		if runtime.GOOS == "windows" && feature.Name == "workspace_acl" && !feature.Implemented {
			t.Fatalf("Windows ACL primitive should be implemented: %#v", feature)
		}
		if runtime.GOOS == "windows" && (feature.Name == "appcontainer" || feature.Name == "network_policy") && !feature.Implemented {
			t.Fatalf("Windows AppContainer primitive should be implemented: %#v", feature)
		}
		if runtime.GOOS == "windows" && (feature.Name == "appcontainer" || feature.Name == "workspace_acl" || feature.Name == "network_policy") && feature.Enforced != appAvailable {
			t.Fatalf("Windows native executor enforcement must track the live AppContainer probe: %#v", feature)
		}
	}
}
