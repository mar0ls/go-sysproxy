//go:build windows

package sysproxy

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var procGetSystemDir = modKernel32.NewProc("GetSystemDirectoryW")

// platformToolDirs is the Windows system directory (C:\Windows\System32),
// where reg.exe lives. It is asked from the OS rather than read from the
// environment, which the caller can change.
func platformToolDirs() []string {
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, _ := procGetSystemDir.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))) //nolint:gosec // Win32 call
	if n > 0 && n < uintptr(len(buf)) {
		return []string{syscall.UTF16ToString(buf[:n])}
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		return []string{filepath.Join(root, "System32")}
	}
	return nil
}
