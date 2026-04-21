//go:build linux || darwin

package sysproxy

import (
	"os"
	"strings"
)

var unixRCFiles = []string{".bashrc", ".zshrc", ".profile", ".bash_profile"}

// setUser appends proxy env-var exports to all common Unix shell rc files.
func setUser(proxyURL string) error {
	return appendToRCFiles(
		"export http_proxy='"+proxyURL+"'",
		"export HTTP_PROXY='"+proxyURL+"'",
		"export https_proxy='"+proxyURL+"'",
		"export HTTPS_PROXY='"+proxyURL+"'",
		"export all_proxy='"+proxyURL+"'",
		"export ALL_PROXY='"+proxyURL+"'",
		"export no_proxy='localhost,127.0.0.1,::1'",
		"export NO_PROXY='localhost,127.0.0.1,::1'",
	)
}

// unsetUser removes proxy env-var exports from all common Unix shell rc files.
func unsetUser() error {
	return removeProxyFromRCFiles()
}

func setUserPAC(pacURL string) error {
	return appendToRCFiles("export AUTOPROXY='" + pacURL + "'")
}

func setUserMulti(cfg ProxyConfig) error {
	var lines []string
	if cfg.HTTP != "" {
		lines = append(lines, "export http_proxy='"+cfg.HTTP+"'", "export HTTP_PROXY='"+cfg.HTTP+"'")
	}
	if cfg.HTTPS != "" {
		lines = append(lines, "export https_proxy='"+cfg.HTTPS+"'", "export HTTPS_PROXY='"+cfg.HTTPS+"'")
	}
	if cfg.SOCKS != "" {
		lines = append(lines, "export all_proxy='"+cfg.SOCKS+"'", "export ALL_PROXY='"+cfg.SOCKS+"'")
	}
	if cfg.NoProxy != "" {
		lines = append(lines, "export no_proxy='"+cfg.NoProxy+"'", "export NO_PROXY='"+cfg.NoProxy+"'")
	}
	return appendToRCFiles(lines...)
}

func appendToRCFiles(lines ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, rc := range unixRCFiles {
		path := home + "/" + rc
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			continue
		}
		for _, l := range lines {
			_, _ = f.WriteString(l + "\n")
		}
		f.Close() // explicit close inside loop — deferred would delay until function return
	}
	return nil
}

func removeProxyFromRCFiles() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	for _, rc := range unixRCFiles {
		path := home + "/" + rc
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var kept []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "_proxy=") || strings.Contains(line, "_PROXY=") ||
				strings.Contains(line, "AUTOPROXY=") {
				continue
			}
			kept = append(kept, line)
		}
		_ = os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0600)
	}
	return nil
}
