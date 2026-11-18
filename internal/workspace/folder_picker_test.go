package workspace

import "testing"

func TestHostFolderPickerCanBeDisabledExplicitly(t *testing.T) {
	t.Setenv(hostFolderPickerEnv, "false")
	capability := HostFolderPickerCapability()
	if capability.Available {
		t.Fatalf("expected host folder picker to be disabled explicitly, got %#v", capability)
	}
	if capability.Mode != "client_or_text" {
		t.Fatalf("expected client_or_text fallback mode, got %#v", capability)
	}
	if capability.OptInEnv != hostFolderPickerEnv {
		t.Fatalf("expected opt-in env %q, got %#v", hostFolderPickerEnv, capability)
	}
}

func TestHostFolderPickerDefaultsToNativeDialogWhenAvailable(t *testing.T) {
	t.Setenv(hostFolderPickerEnv, "")
	command, _ := hostFolderPickerCommand()
	capability := HostFolderPickerCapability()
	if command != "" && !capability.Available {
		t.Fatalf("expected native folder picker to be enabled by default, got %#v", capability)
	}
}
