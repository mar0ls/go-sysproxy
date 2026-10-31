package sysproxy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Operating-system tools (networksetup, route, gsettings, kwriteconfig, reg)
// are not looked up through PATH alone. A directory earlier in PATH that an
// attacker can write to would otherwise run the attacker's binary with the
// caller's privileges and hand it the proxy credentials on its command line,
// which matters most when the CLI runs as root.
//
// The tool is taken from the system directories first. Only an unprivileged
// process falls back to PATH, which keeps Homebrew, Nix and similar layouts
// working; a root process never does and reports ErrToolMissing instead.
//
// git and npm (WriteAppConfig) are the user's own development tools and are
// still found through PATH.
var (
	// systemToolDirs lists the directories holding the system tools; tests
	// replace it.
	systemToolDirs = platformToolDirs
	// privileged reports whether the process runs with administrator rights;
	// tests replace it.
	privileged = func() bool { return os.Geteuid() == 0 }
)

// lookSystemTool returns the absolute path of the system tool name, or an
// error wrapping exec.ErrNotFound.
func lookSystemTool(name string) (string, error) {
	for _, dir := range systemToolDirs() {
		path := filepath.Join(dir, toolFileName(name))
		if isExecutableFile(path) {
			return path, nil
		}
	}
	if privileged() {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return exec.LookPath(name)
}

// hasSystemTool reports whether lookSystemTool finds name.
func hasSystemTool(name string) bool {
	_, err := lookSystemTool(name)
	return err == nil
}

// systemCommand is exec.CommandContext for a system tool: it runs the binary
// lookSystemTool resolved. When there is none, the returned command fails to
// start with an error that wraps exec.ErrNotFound (see execError).
func systemCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	ctx = normalizeContext(ctx)
	path, err := lookSystemTool(name)
	if err != nil {
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // never started: cmd.Err is set
		cmd.Err = err
		return cmd
	}
	return exec.CommandContext(ctx, path, args...) //nolint:gosec // path comes from lookSystemTool
}

// toolFileName is the file name of tool name on this OS.
func toolFileName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode()&0o111 != 0
}
