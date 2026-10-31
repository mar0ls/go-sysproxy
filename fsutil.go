package sysproxy

import (
	"os"
	"path/filepath"
	"sync"
)

// fileMu serialises read-modify-write cycles on config files within this
// process. It does not protect against other processes editing the same file.
var fileMu sync.Mutex

// writeFileAtomic replaces path with data using a temporary file in the same
// directory followed by a rename, so readers never observe a truncated file and
// a crash cannot leave one behind.
//
// The mode (and, where supported, owner) of an existing file is preserved and
// perm is used only for new files. Symlinks are followed, so dotfiles managed
// by stow/chezmoi keep pointing at their target. If the directory is not
// writable but the file itself is, it falls back to an in-place write.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := perm
	info, statErr := os.Stat(path)
	if statErr == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".sysproxy-*")
	if err != nil {
		if os.IsPermission(err) && statErr == nil {
			return os.WriteFile(path, data, mode) //nolint:gosec
		}
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if statErr == nil {
		preserveOwner(tmp, info)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
