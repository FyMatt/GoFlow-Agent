//go:build windows

package nativesandbox

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsAppContainerProfileAndDirectoryACLLease(t *testing.T) {
	requireNativeSandboxHost(t)
	taskID := "acl-lease-" + time.Now().UTC().Format("20060102-150405.000000000")
	profile, err := EnsureWindowsAppContainerProfile(taskID)
	if err != nil {
		var nativeErr *WindowsHRESULTError
		if errors.As(err, &nativeErr) && windowsAppContainerUnavailableHRESULT(nativeErr.HRESULT) {
			t.Skipf("AppContainer profile service is unavailable on this test host: %v", err)
		}
		t.Fatalf("create AppContainer profile: %v", err)
	}
	t.Cleanup(func() {
		if err := profile.Delete(); err != nil {
			t.Errorf("delete AppContainer profile: %v", err)
		}
	})
	if profile.SID == nil || !profile.SID.IsValid() {
		t.Fatalf("invalid AppContainer profile: %#v", profile)
	}
	if !strings.HasPrefix(profile.Name, "GoFlow.Task.") || strings.Contains(profile.Name, taskID) {
		t.Fatalf("profile name should be stable and opaque, got %q", profile.Name)
	}

	assertWindowsDirectoryACLLease(t, profile.SID)
}

func TestWindowsDirectoryACLLease(t *testing.T) {
	requireNativeSandboxHost(t)
	appContainerGroup, err := windows.StringToSid("S-1-15-2-1")
	if err != nil {
		t.Fatalf("create AppContainer group SID: %v", err)
	}
	assertWindowsDirectoryACLLease(t, appContainerGroup)
}

func assertWindowsDirectoryACLLease(t *testing.T, sid *windows.SID) {
	t.Helper()
	root := t.TempDir()
	before := directorySecurityDescriptorString(t, root)
	lease, err := GrantWindowsDirectoryAccess(root, sid, WindowsDirectoryReadWrite)
	if err != nil {
		t.Fatalf("grant AppContainer directory access: %v", err)
	}
	if !directoryACLContainsSID(t, root, sid) {
		t.Fatalf("directory ACL does not contain AppContainer SID %s", sid.String())
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("restore directory ACL: %v", err)
	}
	if restored := directorySecurityDescriptorString(t, root); restored != before {
		t.Fatalf("directory ACL was not restored\nbefore: %s\nafter:  %s", before, restored)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second ACL lease close should be idempotent: %v", err)
	}
}

func windowsAppContainerUnavailableHRESULT(hresult uint32) bool {
	switch hresult {
	case 0x80070002, 0x80070005, 0x80070422:
		return true
	default:
		return false
	}
}

func TestWindowsAppContainerProfileNameRequiresTaskID(t *testing.T) {
	if _, err := windowsAppContainerProfileName("  "); err == nil {
		t.Fatal("expected an empty task ID to be rejected")
	}
	first, err := windowsAppContainerProfileName("task-123")
	if err != nil {
		t.Fatal(err)
	}
	second, err := windowsAppContainerProfileName("task-123")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("profile name should be deterministic: %q != %q", first, second)
	}
}

func TestGrantWindowsDirectoryAccessRejectsUnsafeInput(t *testing.T) {
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GrantWindowsDirectoryAccess("relative", sid, WindowsDirectoryReadOnly); err == nil {
		t.Fatal("expected a relative ACL path to be rejected")
	}
	if _, err := GrantWindowsDirectoryAccess(t.TempDir(), sid, 99); err == nil {
		t.Fatal("expected an unknown access level to be rejected")
	}
}

func directorySecurityDescriptorString(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read directory security descriptor: %v", err)
	}
	if descriptor == nil {
		t.Fatal("directory security descriptor is nil")
	}
	return descriptor.String()
}

func directoryACLContainsSID(t *testing.T, path string, want *windows.SID) bool {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read directory security descriptor: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("read directory DACL: %v", err)
	}
	if dacl == nil {
		return false
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatalf("read directory ACE %d: %v", index, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		entrySID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if entrySID.IsValid() && entrySID.Equals(want) {
			return true
		}
	}
	return false
}
