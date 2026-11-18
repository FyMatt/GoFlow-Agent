//go:build windows

package mcp

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var advapi32 = windows.NewLazySystemDLL("advapi32.dll")

var (
	procSaferCreateLevel           = advapi32.NewProc("SaferCreateLevel")
	procSaferComputeTokenFromLevel = advapi32.NewProc("SaferComputeTokenFromLevel")
	procSaferCloseLevel            = advapi32.NewProc("SaferCloseLevel")
)

const (
	saferScopeUser        = 1
	saferLevelConstrained = 0x00010000
	saferOpenFlagOpen     = 1
	saferTokenNullIfEqual = 1
)

func newSysProcAttr(isolation string) (*syscall.SysProcAttr, func() error, error) {
	attr := &syscall.SysProcAttr{}
	if isolation == "process_group" || isolation == "windows_job" || isolation == "windows_restricted_token" {
		attr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
	}
	if isolation != "windows_restricted_token" {
		return attr, nil, nil
	}
	token, err := createRestrictedPrimaryToken()
	if err != nil {
		return nil, nil, err
	}
	attr.Token = syscall.Token(token)
	cleanup := func() error {
		if token == 0 {
			return nil
		}
		err := windows.CloseHandle(windows.Handle(token))
		token = 0
		return err
	}
	return attr, cleanup, nil
}

func createRestrictedPrimaryToken() (windows.Token, error) {
	var restricted windows.Token
	if err := createSaferRestrictedToken(&restricted); err != nil {
		return 0, err
	}
	defer restricted.Close()

	var primary windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return 0, fmt.Errorf("duplicate restricted primary token: %w", err)
	}
	if err := setLowIntegrityLevel(primary); err != nil {
		primary.Close()
		return 0, err
	}
	return primary, nil
}

func createSaferRestrictedToken(restricted *windows.Token) error {
	var level windows.Handle
	r1, _, callErr := procSaferCreateLevel.Call(
		uintptr(saferScopeUser),
		uintptr(saferLevelConstrained),
		uintptr(saferOpenFlagOpen),
		uintptr(unsafe.Pointer(&level)),
		0,
	)
	if r1 == 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("create SAFER constrained level: %w", callErr)
		}
		return fmt.Errorf("create SAFER constrained level failed")
	}
	defer procSaferCloseLevel.Call(uintptr(level))

	r1, _, callErr = procSaferComputeTokenFromLevel.Call(
		uintptr(level),
		0,
		uintptr(unsafe.Pointer(restricted)),
		uintptr(saferTokenNullIfEqual),
		0,
	)
	if r1 == 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("compute SAFER restricted token: %w", callErr)
		}
		return fmt.Errorf("compute SAFER restricted token failed")
	}
	return nil
}

func setLowIntegrityLevel(token windows.Token) error {
	sid, err := windows.StringToSid("S-1-16-4096")
	if err != nil {
		return fmt.Errorf("create low-integrity SID: %w", err)
	}
	label := windows.Tokenmandatorylabel{
		Label: windows.SIDAndAttributes{
			Sid:        sid,
			Attributes: windows.SE_GROUP_INTEGRITY,
		},
	}
	if err := windows.SetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		return fmt.Errorf("set low-integrity token level: %w", err)
	}
	return nil
}
