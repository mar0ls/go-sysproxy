//go:build !windows

package sysproxy

// platformToolDirs are the directories of the OS-provided tools. /usr/local/bin
// is left out on purpose: it is writable by the user on Homebrew systems.
func platformToolDirs() []string {
	return []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}
}
