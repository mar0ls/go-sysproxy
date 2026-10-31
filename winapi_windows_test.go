//go:build windows

package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F-17: rundll32 cannot call InternetSetOptionEx; the notification goes
// through the Win32 API directly and has to succeed.
func TestNotifyWinINET(t *testing.T) {
	if err := notifyWinINET(); err != nil {
		t.Fatalf("notifyWinINET: %v", err)
	}
}

func TestKnownFolderProfileAndDocuments(t *testing.T) {
	profile, err := knownFolder(folderIDProfile)
	if err != nil || profile == "" {
		t.Fatalf("profile folder = %q, %v", profile, err)
	}
	docs, err := knownFolder(folderIDDocuments)
	if err != nil || docs == "" {
		t.Fatalf("documents folder = %q, %v", docs, err)
	}
	if _, err := os.Stat(docs); err != nil {
		t.Errorf("documents folder %q does not exist: %v", docs, err)
	}
}

// With the real USERPROFILE the (possibly redirected) Documents folder is
// used; with an overridden USERPROFILE that directory wins.
func TestDocumentsDir(t *testing.T) {
	want, err := knownFolder(folderIDDocuments)
	if err != nil {
		t.Fatal(err)
	}
	got, err := documentsDir()
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("documentsDir = %q, %v; want %q", got, err, want)
	}

	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	got, err = documentsDir()
	if err != nil || got != filepath.Join(home, "Documents") {
		t.Fatalf("documentsDir with USERPROFILE=%q = %q, %v", home, got, err)
	}
}

func TestPowershellProfiles_PowerShell7OnlyWhenInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)

	profiles, err := powershellProfiles()
	if err != nil || len(profiles) != 1 || !strings.HasSuffix(profiles[0], `Documents\WindowsPowerShell\`+psProfileName) {
		t.Fatalf("without PowerShell 7: %v, %v", profiles, err)
	}

	if err := os.MkdirAll(filepath.Join(home, "Documents", "PowerShell"), 0o700); err != nil {
		t.Fatal(err)
	}
	profiles, err = powershellProfiles()
	if err != nil || len(profiles) != 2 || !strings.HasSuffix(profiles[1], `Documents\PowerShell\`+psProfileName) {
		t.Fatalf("with PowerShell 7: %v, %v", profiles, err)
	}

	// SetUser writes both and UnsetUser removes the block from both.
	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if b, err := readProfile(p); err != nil || !strings.Contains(b, "proxy.example.com") {
			t.Errorf("%s: %q, %v", p, b, err)
		}
	}
	if err := unsetUser(); err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if b, err := readProfile(p); err != nil || strings.Contains(b, "proxy.example.com") {
			t.Errorf("%s after unset: %q, %v", p, b, err)
		}
	}
}

func readProfile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from powershellProfiles
	return string(b), err
}
