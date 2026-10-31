//go:build unix

package sysproxy

import (
	"os"
	"syscall"
)

// preserveOwner gives f the owner of the file it is about to replace. It is a
// best-effort step: only root can change the owner, and when the owner is
// already the current user nothing needs to change.
func preserveOwner(f *os.File, info os.FileInfo) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = f.Chown(int(st.Uid), int(st.Gid))
	}
}
