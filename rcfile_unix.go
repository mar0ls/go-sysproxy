//go:build linux || darwin

package sysproxy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

var unixRCFiles = []string{".bashrc", ".zshrc", ".profile", ".bash_profile"}

func exportLines(pairs []envPair) []string {
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		lines = append(lines, "export "+p.name+"="+shellQuote(p.value))
	}
	return lines
}

// setUser writes proxy env-var exports for proxyURL into the shell rc files.
func setUser(proxyURL string) error {
	return applyRCBlock(exportLines([]envPair{
		{"http_proxy", proxyURL},
		{"HTTP_PROXY", proxyURL},
		{"https_proxy", proxyURL},
		{"HTTPS_PROXY", proxyURL},
		{"all_proxy", proxyURL},
		{"ALL_PROXY", proxyURL},
		{"no_proxy", defaultNoProxy},
		{"NO_PROXY", defaultNoProxy},
	}), false, noProxyNames...)
}

// unsetUser removes the go-sysproxy block (and unmarked lines written by
// versions <= 0.5) from the shell rc files. Other lines are never touched.
func unsetUser() error {
	return removeRCBlock()
}

func setUserPAC(pacURL string) error {
	return applyRCBlock(exportLines([]envPair{{"AUTOPROXY", pacURL}}), false)
}

func setUserMulti(cfg ProxyConfig) error {
	var pairs []envPair
	if cfg.HTTP != "" {
		pairs = append(pairs, envPair{"http_proxy", cfg.HTTP}, envPair{"HTTP_PROXY", cfg.HTTP})
	}
	if cfg.HTTPS != "" {
		pairs = append(pairs, envPair{"https_proxy", cfg.HTTPS}, envPair{"HTTPS_PROXY", cfg.HTTPS})
	}
	if cfg.SOCKS != "" {
		pairs = append(pairs, envPair{"all_proxy", cfg.SOCKS}, envPair{"ALL_PROXY", cfg.SOCKS})
	}
	if cfg.NoProxy != "" {
		pairs = append(pairs, envPair{"no_proxy", cfg.NoProxy}, envPair{"NO_PROXY", cfg.NoProxy})
	}
	if len(pairs) == 0 {
		return nil
	}
	return applyRCBlock(exportLines(pairs), true)
}

// rcTargets returns the rc files to edit: those that already exist. Creating a
// file the user does not have can change shell behaviour — a new
// ~/.bash_profile, for instance, makes bash login shells stop reading
// ~/.profile — so a file is only created when none exists, and then only the
// one matching the user's shell. The exception is ~/.zshrc for zsh users.
func rcTargets(home string) []string {
	var targets []string
	for _, path := range rcPaths(home) {
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			targets = append(targets, path)
		}
	}
	// zsh reads neither ~/.profile nor ~/.bashrc, and creating ~/.zshrc shadows
	// nothing, so a zsh user always gets one.
	zshrc := zshrcPath(home)
	if filepath.Base(os.Getenv("SHELL")) == "zsh" && !slices.Contains(targets, zshrc) {
		targets = append(targets, zshrc)
	}
	if len(targets) == 0 {
		targets = append(targets, filepath.Join(home, defaultRCFile()))
	}
	return targets
}

// zshrcPath is the .zshrc zsh reads: in $ZDOTDIR when that is set.
func zshrcPath(home string) string {
	if dir := os.Getenv("ZDOTDIR"); dir != "" {
		return filepath.Join(dir, ".zshrc")
	}
	return filepath.Join(home, ".zshrc")
}

// rcPaths are the rc files go-sysproxy may have written, with zsh's .zshrc
// where zsh looks for it.
func rcPaths(home string) []string {
	paths := make([]string, 0, len(unixRCFiles)+1)
	for _, rc := range unixRCFiles {
		if rc == ".zshrc" {
			paths = append(paths, zshrcPath(home))
			continue
		}
		paths = append(paths, filepath.Join(home, rc))
	}
	return paths
}

func defaultRCFile() string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return ".zshrc"
	case "bash":
		return ".bashrc"
	}
	if runtime.GOOS == "darwin" {
		return ".zshrc"
	}
	return ".profile"
}

// applyRCBlock writes body into every target rc file. It fails only when no
// file could be written; partial failures are logged.
func applyRCBlock(body []string, merge bool, yield ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var errs []error
	written := 0
	for _, path := range rcTargets(home) {
		if err := applyBlockFile(path, body, merge, yield...); err != nil {
			logf("rcfile_unix: %s: %v", path, err)
			errs = append(errs, fmt.Errorf("sysproxy: %w", err))
			continue
		}
		written++
	}
	if written == 0 {
		return errors.Join(errs...)
	}
	return sudoHomeWarning()
}

func removeRCBlock() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var errs []error
	paths := rcPaths(home)
	// A ~/.zshrc written while ZDOTDIR was unset, or by an older version.
	if z := filepath.Join(home, ".zshrc"); !slices.Contains(paths, z) {
		paths = append(paths, z)
	}
	for _, path := range paths {
		if err := removeBlockFile(path); err != nil {
			errs = append(errs, fmt.Errorf("sysproxy: %w", err))
		}
	}
	return errors.Join(errs...)
}

// snapshotUser records the managed block of every rc file that setUser,
// setUserPAC or setUserMulti would write, for WithProxy to restore.
func snapshotUser() (func() error, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	snaps, err := snapshotBlockFiles(rcTargets(home))
	if err != nil {
		return nil, err
	}
	return func() error { return restoreBlockFiles(snaps) }, nil
}
