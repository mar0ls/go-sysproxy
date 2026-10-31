package sysproxy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileAtomic_NewFileUsesPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := writeFileAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestWriteFileAtomic_PreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want the existing 0644 kept", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) != "new" { //nolint:gosec
		t.Errorf("content = %q", b)
	}
}

func TestWriteFileAtomic_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	for i := 0; i < 3; i++ {
		if err := writeFileAtomic(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("want only the target file, got %v", entries)
	}
}

func TestWriteFileAtomic_FollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeFileAtomic(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != "new" { //nolint:gosec
		t.Errorf("target content = %q", b)
	}
}

func TestWriteFileAtomic_FallsBackToInPlaceWhenDirReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec

	if err := writeFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("expected in-place fallback, got %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" { //nolint:gosec
		t.Errorf("content = %q", b)
	}
}

func TestWriteFileAtomic_ReportsPermissionError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec

	err := writeFileAtomic(filepath.Join(dir, "new"), []byte("x"), 0o600)
	if !os.IsPermission(err) {
		t.Errorf("want a permission error, got %v", err)
	}
}

// Concurrent read-modify-write cycles must not lose or duplicate the block.
func TestApplyBlockFile_ConcurrentCallsKeepOneBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	if err := os.WriteFile(path, []byte("echo hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := applyBlockFile(path, []string{"A=1"}, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	b, _ := os.ReadFile(path) //nolint:gosec
	if got := strings.Count(string(b), blockBegin); got != 1 {
		t.Errorf("want 1 block, got %d:\n%s", got, b)
	}
	if !strings.HasPrefix(string(b), "echo hi\n") {
		t.Errorf("user content lost:\n%s", b)
	}
}
