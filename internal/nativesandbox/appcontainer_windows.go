//go:build windows

package nativesandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var userenv = windows.NewLazySystemDLL("userenv.dll")

var (
	procCreateAppContainerProfile              = userenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile              = userenv.NewProc("DeleteAppContainerProfile")
	procDeriveAppContainerSidFromContainerName = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
)

var (
	windowsAppContainerProbeOnce      sync.Once
	windowsAppContainerProbeAvailable bool
)

const hresultAlreadyExists = 0x800700B7

// WindowsAppContainerProfile owns one task-scoped AppContainer identity.
// The SID is copied to Go-managed memory before the Windows allocation is freed.
type WindowsAppContainerProfile struct {
	Name string
	SID  *windows.SID

	mu      sync.Mutex
	deleted bool
}

// WindowsHRESULTError preserves the HRESULT returned by a native sandbox API
// so capability probes can distinguish an unavailable host from invalid input.
type WindowsHRESULTError struct {
	Action  string
	HRESULT uint32
	Err     error
}

func (err *WindowsHRESULTError) Error() string {
	if err == nil {
		return ""
	}
	if err.Err != nil {
		return fmt.Sprintf("%s failed (HRESULT 0x%08x): %v", err.Action, err.HRESULT, err.Err)
	}
	return fmt.Sprintf("%s failed (HRESULT 0x%08x)", err.Action, err.HRESULT)
}

func (err *WindowsHRESULTError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

// EnsureWindowsAppContainerProfile creates or reopens a deterministic,
// task-scoped AppContainer identity without embedding the task identifier in
// the Windows profile name.
func EnsureWindowsAppContainerProfile(taskID string) (*WindowsAppContainerProfile, error) {
	profileName, err := windowsAppContainerProfileName(taskID)
	if err != nil {
		return nil, err
	}
	displayName, err := windows.UTF16PtrFromString("GoFlow task sandbox")
	if err != nil {
		return nil, fmt.Errorf("encode AppContainer display name: %w", err)
	}
	description, err := windows.UTF16PtrFromString("Temporary GoFlow task execution boundary")
	if err != nil {
		return nil, fmt.Errorf("encode AppContainer description: %w", err)
	}
	name, err := windows.UTF16PtrFromString(profileName)
	if err != nil {
		return nil, fmt.Errorf("encode AppContainer profile name: %w", err)
	}

	var allocatedSID *windows.SID
	hresult, _, callErr := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(displayName)),
		uintptr(unsafe.Pointer(description)),
		0,
		0,
		uintptr(unsafe.Pointer(&allocatedSID)),
	)
	if uint32(hresult) == hresultAlreadyExists {
		hresult, _, callErr = procDeriveAppContainerSidFromContainerName.Call(
			uintptr(unsafe.Pointer(name)),
			uintptr(unsafe.Pointer(&allocatedSID)),
		)
	}
	if uint32(hresult) != 0 {
		return nil, windowsHRESULTError("create or derive AppContainer profile", uint32(hresult), callErr)
	}
	if allocatedSID == nil {
		return nil, fmt.Errorf("create or derive AppContainer profile returned an empty SID")
	}
	defer windows.FreeSid(allocatedSID)

	sid, err := allocatedSID.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy AppContainer SID: %w", err)
	}
	return &WindowsAppContainerProfile{Name: profileName, SID: sid}, nil
}

func windowsAppContainerAvailable() bool {
	windowsAppContainerProbeOnce.Do(func() {
		probeID := fmt.Sprintf("capability-probe-%d-%d", os.Getpid(), time.Now().UnixNano())
		profile, err := EnsureWindowsAppContainerProfile(probeID)
		if err != nil {
			return
		}
		if err := profile.Delete(); err != nil {
			return
		}
		windowsAppContainerProbeAvailable = true
	})
	return windowsAppContainerProbeAvailable
}

func windowsAppContainerProfileName(taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task ID is required for AppContainer isolation")
	}
	sum := sha256.Sum256([]byte(taskID))
	return fmt.Sprintf("GoFlow.Task.%x", sum[:12]), nil
}

// Delete removes the task-scoped AppContainer profile. It is idempotent.
func (profile *WindowsAppContainerProfile) Delete() error {
	if profile == nil {
		return nil
	}
	profile.mu.Lock()
	defer profile.mu.Unlock()
	if profile.deleted {
		return nil
	}
	name, err := windows.UTF16PtrFromString(profile.Name)
	if err != nil {
		return fmt.Errorf("encode AppContainer profile name: %w", err)
	}
	hresult, _, callErr := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name)))
	if uint32(hresult) != 0 {
		return windowsHRESULTError("delete AppContainer profile", uint32(hresult), callErr)
	}
	profile.deleted = true
	return nil
}

// WindowsDirectoryAccess describes the minimum directory rights granted to an
// AppContainer SID. The real workspace must never receive a read-write grant.
type WindowsDirectoryAccess uint8

const (
	WindowsDirectoryReadOnly WindowsDirectoryAccess = iota + 1
	WindowsDirectoryReadWrite
)

// WindowsACLLease restores the original directory DACL when closed.
type WindowsACLLease struct {
	path       string
	original   *windows.SECURITY_DESCRIPTOR
	protection windows.SECURITY_INFORMATION

	mu     sync.Mutex
	closed bool
}

// GrantWindowsDirectoryAccess grants inheritable access for a task AppContainer
// SID and returns a lease that restores the previous ACL.
func GrantWindowsDirectoryAccess(path string, sid *windows.SID, access WindowsDirectoryAccess) (*WindowsACLLease, error) {
	if sid == nil || !sid.IsValid() {
		return nil, fmt.Errorf("a valid AppContainer SID is required")
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("AppContainer ACL path must be absolute")
	}
	permissions, err := windowsDirectoryPermissions(access)
	if err != nil {
		return nil, err
	}

	original, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, fmt.Errorf("read directory ACL %s: %w", path, err)
	}
	if original == nil {
		return nil, fmt.Errorf("directory %s has no security descriptor", path)
	}
	originalDACL, _, err := original.DACL()
	if err != nil {
		return nil, fmt.Errorf("read directory DACL %s: %w", path, err)
	}
	control, _, err := original.Control()
	if err != nil {
		return nil, fmt.Errorf("read directory ACL control %s: %w", path, err)
	}
	protection := windows.SECURITY_INFORMATION(windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		protection = windows.SECURITY_INFORMATION(windows.PROTECTED_DACL_SECURITY_INFORMATION)
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: permissions,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, originalDACL)
	if err != nil {
		return nil, fmt.Errorf("build AppContainer directory ACL %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|protection,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return nil, fmt.Errorf("grant AppContainer directory ACL %s: %w", path, err)
	}
	return &WindowsACLLease{path: path, original: original, protection: protection}, nil
}

func windowsDirectoryPermissions(access WindowsDirectoryAccess) (windows.ACCESS_MASK, error) {
	switch access {
	case WindowsDirectoryReadOnly:
		return windows.GENERIC_READ | windows.GENERIC_EXECUTE, nil
	case WindowsDirectoryReadWrite:
		return windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE | windows.DELETE, nil
	default:
		return 0, fmt.Errorf("unsupported Windows directory access level %d", access)
	}
}

// Close restores the directory DACL captured before the AppContainer grant.
func (lease *WindowsACLLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	if lease.original == nil {
		return fmt.Errorf("cannot restore directory ACL without the original descriptor")
	}
	dacl, _, err := lease.original.DACL()
	if err != nil {
		return fmt.Errorf("read original directory DACL %s: %w", lease.path, err)
	}
	if err := windows.SetNamedSecurityInfo(
		lease.path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|lease.protection,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("restore directory ACL %s: %w", lease.path, err)
	}
	lease.closed = true
	return nil
}

func windowsHRESULTError(action string, hresult uint32, callErr error) error {
	if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
		return &WindowsHRESULTError{Action: action, HRESULT: hresult, Err: callErr}
	}
	return &WindowsHRESULTError{Action: action, HRESULT: hresult}
}
