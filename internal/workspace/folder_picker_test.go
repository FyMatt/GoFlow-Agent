package workspace

import "testing"

func TestHostFolderPickerDisabledByDefault(t *testing.T) {
	t.Setenv(hostFolderPickerEnv, "")
	capability := HostFolderPickerCapability()
	if capability.Available {
		t.Fatalf("expected host folder picker to be disabled by default, got %#v", capability)
	}
	if capability.Mode != "client_or_text" {
		t.Fatalf("expected client_or_text fallback mode, got %#v", capability)
	}
	if capability.OptInEnv != hostFolderPickerEnv {
		t.Fatalf("expected opt-in env %q, got %#v", hostFolderPickerEnv, capability)
	}
}
