//go:build windows

package sysproxy

import (
	"path/filepath"
	"strings"
	"testing"
)

// reg.exe must come from the system directory even when PATH or SystemRoot
// point somewhere an attacker controls.
func TestLookSystemTool_RegComesFromTheSystemDirectory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SystemRoot", t.TempDir())
	got, err := lookSystemTool("reg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Base(got), "reg.exe") || !strings.EqualFold(filepath.Base(filepath.Dir(got)), "System32") {
		t.Errorf("reg resolved to %q, want <windir>\\System32\\reg.exe", got)
	}
}
