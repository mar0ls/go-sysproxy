package sysproxy

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AppName identifies a supported application for WriteAppConfig.
type AppName string

const (
	AppCurl AppName = "curl" // ~/.curlrc
	AppGit  AppName = "git"  // git config --global
	AppNPM  AppName = "npm"  // npm config set
	AppPip  AppName = "pip"  // ~/.config/pip/pip.conf
	AppWget AppName = "wget" // ~/.wgetrc
)

// WriteAppConfig writes proxy settings to the tool-specific config for app.
// proxyURL must be a valid proxy URL (validated before writing).
//
//	err := sysproxy.WriteAppConfig(sysproxy.AppGit, "http://proxy.example.com:8080")
func WriteAppConfig(app AppName, proxyURL string) error {
	if err := validateProxyURL(proxyURL); err != nil {
		return err
	}
	var err error
	switch app {
	case AppCurl:
		err = writeCurlRC(proxyURL)
	case AppGit:
		err = writeGitProxy(proxyURL)
	case AppNPM:
		err = writeNPMProxy(proxyURL)
	case AppPip:
		err = writePipConf(proxyURL)
	case AppWget:
		err = writeWgetRC(proxyURL)
	default:
		return fmt.Errorf("sysproxy: unsupported app %q", app)
	}
	logf("WriteAppConfig app=%s url=%s err=%v", app, proxyURL, err)
	return err
}

// ClearAppConfig removes proxy settings from the tool-specific config for app.
func ClearAppConfig(app AppName) error {
	var err error
	switch app {
	case AppCurl:
		err = clearCurlRC()
	case AppGit:
		err = clearGitProxy()
	case AppNPM:
		err = clearNPMProxy()
	case AppPip:
		err = clearPipConf()
	case AppWget:
		err = clearWgetRC()
	default:
		return fmt.Errorf("sysproxy: unsupported app %q", app)
	}
	logf("ClearAppConfig app=%s err=%v", app, err)
	return err
}

// ── curl (~/.curlrc) ──────────────────────────────────────────────────────────

func writeCurlRC(proxyURL string) error {
	path, err := userConfigFile(".curlrc")
	if err != nil {
		return err
	}
	return editKeyValueFile(path, "proxy", proxyURL, " = ")
}

func clearCurlRC() error {
	path, err := userConfigFile(".curlrc")
	if err != nil {
		return err
	}
	return removeKeysFromFile(path, " = ", "proxy")
}

// ── git (git config --global) ─────────────────────────────────────────────────

func runGit(args ...string) error {
	return exec.Command("git", args...).Run() //nolint:noctx,gosec
}

func writeGitProxy(proxyURL string) error {
	if !isAvailable("git") {
		return fmt.Errorf("sysproxy: git not found in PATH")
	}
	if err := runGit("config", "--global", "http.proxy", proxyURL); err != nil {
		return fmt.Errorf("sysproxy: git config http.proxy: %w", err)
	}
	if err := runGit("config", "--global", "https.proxy", proxyURL); err != nil {
		return fmt.Errorf("sysproxy: git config https.proxy: %w", err)
	}
	return nil
}

func clearGitProxy() error {
	if !isAvailable("git") {
		return fmt.Errorf("sysproxy: git not found in PATH")
	}
	_ = runGit("config", "--global", "--unset", "http.proxy")
	_ = runGit("config", "--global", "--unset", "https.proxy")
	return nil
}

// ── npm (npm config set) ──────────────────────────────────────────────────────

func runNPM(args ...string) error {
	return exec.Command("npm", args...).Run() //nolint:noctx,gosec
}

func writeNPMProxy(proxyURL string) error {
	if !isAvailable("npm") {
		return fmt.Errorf("sysproxy: npm not found in PATH")
	}
	if err := runNPM("config", "set", "proxy", proxyURL); err != nil {
		return fmt.Errorf("sysproxy: npm config set proxy: %w", err)
	}
	if err := runNPM("config", "set", "https-proxy", proxyURL); err != nil {
		return fmt.Errorf("sysproxy: npm config set https-proxy: %w", err)
	}
	return nil
}

func clearNPMProxy() error {
	if !isAvailable("npm") {
		return fmt.Errorf("sysproxy: npm not found in PATH")
	}
	_ = runNPM("config", "delete", "proxy")
	_ = runNPM("config", "delete", "https-proxy")
	return nil
}

// ── pip (~/.config/pip/pip.conf) ─────────────────────────────────────────────

func writePipConf(proxyURL string) error {
	path, err := pipConfPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return editINIFile(path, "global", "proxy", proxyURL)
}

func clearPipConf() error {
	path, err := pipConfPath()
	if err != nil {
		return err
	}
	return removeINIKey(path, "global", "proxy")
}

func pipConfPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "pip", "pip.conf"), nil
}

// ── wget (~/.wgetrc) ──────────────────────────────────────────────────────────

func writeWgetRC(proxyURL string) error {
	path, err := userConfigFile(".wgetrc")
	if err != nil {
		return err
	}
	if err := editKeyValueFile(path, "http_proxy", proxyURL, " = "); err != nil {
		return err
	}
	return editKeyValueFile(path, "https_proxy", proxyURL, " = ")
}

func clearWgetRC() error {
	path, err := userConfigFile(".wgetrc")
	if err != nil {
		return err
	}
	return removeKeysFromFile(path, " = ", "http_proxy", "https_proxy")
}

// ── shared file helpers ───────────────────────────────────────────────────────

func userConfigFile(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, name), nil
}

// editKeyValueFile sets key=value in a simple key<sep>value file, replacing any
// existing line with the same key and appending if absent.
func editKeyValueFile(path, key, value, sep string) error {
	lines, err := readLines(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	prefix := key + sep
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			lines[i] = prefix + value
			found = true
		}
	}
	if !found {
		lines = append(lines, prefix+value)
	}
	return writeLines(path, lines)
}

// removeKeysFromFile removes all lines whose key (before sep) matches any of keys.
func removeKeysFromFile(path, sep string, keys ...string) error {
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	var kept []string
	for _, l := range lines {
		k := strings.SplitN(l, sep, 2)[0]
		if !keySet[strings.TrimSpace(k)] {
			kept = append(kept, l)
		}
	}
	return writeLines(path, kept)
}

// editINIFile sets [section] key = value in an INI-style file.
func editINIFile(path, section, key, value string) error {
	lines, err := readLines(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	header := "[" + section + "]"
	entry := key + " = " + value
	inSection := false
	found := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == header {
			inSection = true
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inSection = false
		}
		if inSection && strings.HasPrefix(trimmed, key+" ") {
			lines[i] = entry
			found = true
		}
	}
	if !found {
		// Append section header if missing, then the key.
		hasHeader := false
		for _, l := range lines {
			if strings.TrimSpace(l) == header {
				hasHeader = true
				break
			}
		}
		if !hasHeader {
			lines = append(lines, header)
		}
		lines = append(lines, entry)
	}
	return writeLines(path, lines)
}

// removeINIKey removes key from [section] in an INI file.
func removeINIKey(path, section, key string) error {
	lines, err := readLines(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	header := "[" + section + "]"
	inSection := false
	var kept []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == header {
			inSection = true
			kept = append(kept, l)
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inSection = false
		}
		if inSection && strings.HasPrefix(trimmed, key) {
			continue
		}
		kept = append(kept, l)
	}
	return writeLines(path, kept)
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logf("appconfig: close error: %v", err)
		}
	}()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func writeLines(path string, lines []string) error {
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
