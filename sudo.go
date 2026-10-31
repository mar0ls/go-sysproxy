package sysproxy

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// Running the CLI (or a program using the library) through sudo makes
// per-user settings land in root's account: gsettings and kwriteconfig write
// root's configuration, and rc files and tool configs go to root's home unless
// HOME was kept. The call succeeds, so without a warning the caller believes
// the invoking user's settings changed.

// lookupUserHome returns the home directory of a user; tests replace it.
var lookupUserHome = func(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

// sudoCaller returns the user who started the process through sudo, or "" when
// the process does not run as root under sudo.
func sudoCaller() string {
	if !privileged() {
		return ""
	}
	if u := os.Getenv("SUDO_USER"); u != "root" {
		return u
	}
	return ""
}

// sudoHomeWarning is the non-critical warning for files written to a home
// directory that is not the invoking user's. It is nil outside sudo, when HOME
// is the invoking user's home, and when that home cannot be determined.
func sudoHomeWarning() error {
	caller := sudoCaller()
	if caller == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil //nolint:nilerr // no home, nothing was written there
	}
	callerHome, err := lookupUserHome(caller)
	if err != nil || filepath.Clean(callerHome) == filepath.Clean(home) {
		return nil //nolint:nilerr // unknown caller home: do not guess
	}
	return &nonCriticalError{err: fmt.Errorf(
		"sysproxy: running as root through sudo (SUDO_USER=%s): per-user files were written in %s, not in %s's home %s; "+
			"run without sudo, or keep HOME (sudo -E), to change the user's own files", caller, home, caller, callerHome)}
}

// withWarning returns err unless it is nil, in which case it returns warning.
// A hard error is never downgraded by a warning, and a warning never hides
// one.
func withWarning(err, warning error) error {
	if err != nil {
		return err
	}
	return warning
}
