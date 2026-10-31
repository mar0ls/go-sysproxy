//go:build unix

package sysproxy

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func ownerOf(t *testing.T, path string) (uid, gid uint32) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	return st.Uid, st.Gid
}

// S2: root creating a dotfile in a user's home (sudo with HOME kept) must not
// leave a root-owned file behind. Needs root to change owners; as an ordinary
// user the file naturally belongs to that user and there is nothing to check.
func TestWriteFileAtomic_NewFileTakesTheOwnerOfItsDirectoryWhenRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing file owners needs root")
	}
	dir := t.TempDir()
	const uid, gid = 4242, 4343
	if err := os.Chown(dir, uid, gid); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".zshrc")
	if err := writeFileAtomic(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u, g := ownerOf(t, path); u != uid || g != gid {
		t.Errorf("new file owner = %d:%d, want %d:%d", u, g, uid, gid)
	}

	// An existing file keeps its own owner, whatever the directory's.
	if err := os.Chown(path, 5151, 5252); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u, g := ownerOf(t, path); u != 5151 || g != 5252 {
		t.Errorf("existing file owner = %d:%d, want 5151:5252", u, g)
	}
}
