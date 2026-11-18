//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSSHIdentityInheritsOnlyCurrentUserAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := secureSSHDirectory(dir); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(dir, "identity")
	if err := os.WriteFile(identity, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, identity} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil || acl.AceCount == 0 {
			t.Fatalf("SSH identity or parent grants access beyond current user: %s, %v", sd.String(), err)
		}
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &ace); err != nil {
				t.Fatal(err)
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
				!(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
				t.Fatalf("SSH identity or parent grants another principal: %s", sd.String())
			}
		}
	}
}
