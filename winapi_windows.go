//go:build windows

package sysproxy

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modWininet   = syscall.NewLazyDLL("wininet.dll")
	modShell32   = syscall.NewLazyDLL("shell32.dll")
	modOle32     = syscall.NewLazyDLL("ole32.dll")
	procSetOptW  = modWininet.NewProc("InternetSetOptionW")
	procKnownDir = modShell32.NewProc("SHGetKnownFolderPath")
	procCoFree   = modOle32.NewProc("CoTaskMemFree")
)

const (
	internetOptionRefresh         = 37
	internetOptionSettingsChanged = 39
)

// notifyWinINET tells WinINET that the proxy settings in the registry changed
// (INTERNET_OPTION_SETTINGS_CHANGED) and to re-read them (INTERNET_OPTION_REFRESH),
// which is what running applications need to notice the change.
func notifyWinINET() error {
	var errs []error
	for _, opt := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		if r, _, e := procSetOptW.Call(0, opt, 0, 0); r == 0 {
			errs = append(errs, fmt.Errorf("sysproxy: InternetSetOption(%d): %w", opt, e))
		}
	}
	return errors.Join(errs...)
}

// FOLDERID_Profile and FOLDERID_Documents.
var (
	folderIDProfile   = syscall.GUID{Data1: 0x5E6C858F, Data2: 0x0E22, Data3: 0x4760, Data4: [8]byte{0x9A, 0xFE, 0xEA, 0x33, 0x17, 0xB6, 0x71, 0x73}}
	folderIDDocuments = syscall.GUID{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}
)

// knownFolder returns the real location of a shell folder, which follows
// redirections such as OneDrive's Documents.
func knownFolder(id syscall.GUID) (string, error) {
	var p *uint16
	hr, _, _ := procKnownDir.Call(uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&p))) //nolint:gosec // Win32 call
	if p != nil {
		defer func() { _, _, _ = procCoFree.Call(uintptr(unsafe.Pointer(p))) }() //nolint:gosec // Win32 call
	}
	if hr != 0 {
		return "", fmt.Errorf("sysproxy: SHGetKnownFolderPath: HRESULT 0x%x", hr)
	}
	n := 0
	for q := unsafe.Pointer(p); *(*uint16)(q) != 0; q = unsafe.Add(q, 2) { //nolint:gosec // walk the NUL-terminated string
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n)), nil //nolint:gosec // p points to n valid UTF-16 units
}
