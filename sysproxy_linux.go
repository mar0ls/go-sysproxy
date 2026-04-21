//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func runGsettings(args ...string) error {
	return exec.Command("gsettings", args...).Run() //nolint:noctx,gosec
}

func runKwriteconfig5(args ...string) error {
	return exec.Command("kwriteconfig5", args...).Run() //nolint:noctx,gosec
}

func setGlobal(p *proxy) error {
	switch detectDesktopEnv() {
	case "gnome":
		if isAvailable("gsettings") {
			_ = runGsettings("set", "org.gnome.system.proxy", "mode", "manual")
			_ = runGsettings("set", "org.gnome.system.proxy.http", "host", p.host)
			_ = runGsettings("set", "org.gnome.system.proxy.http", "port", p.port)
			_ = runGsettings("set", "org.gnome.system.proxy.https", "host", p.host)
			_ = runGsettings("set", "org.gnome.system.proxy.https", "port", p.port)
			_ = runGsettings("set", "org.gnome.system.proxy.socks", "host", p.host)
			_ = runGsettings("set", "org.gnome.system.proxy.socks", "port", p.port)
		}
	case "kde":
		if isAvailable("kwriteconfig5") {
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1")
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", p.rawURL)
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpsProxy", p.rawURL)
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ftpProxy", p.rawURL)
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "socksProxy", p.rawURL)
		}
	}
	return writeEtcEnvironment("/etc/environment", p.rawURL)
}

func unsetGlobal() error {
	if isAvailable("gsettings") {
		_ = runGsettings("set", "org.gnome.system.proxy", "mode", "none")
	}
	if isAvailable("kwriteconfig5") {
		_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0")
	}
	return clearEtcEnvironment("/etc/environment")
}

func getGlobal() (string, error) {
	if !isAvailable("gsettings") {
		return "", fmt.Errorf("sysproxy: gsettings not available")
	}
	out, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output() //nolint:noctx
	if err != nil || !strings.Contains(string(out), "manual") {
		return "", fmt.Errorf("sysproxy: proxy not set")
	}
	host, err1 := exec.Command("gsettings", "get", "org.gnome.system.proxy.http", "host").Output() //nolint:noctx
	port, err2 := exec.Command("gsettings", "get", "org.gnome.system.proxy.http", "port").Output() //nolint:noctx
	if err1 != nil || err2 != nil {
		return "", fmt.Errorf("sysproxy: cannot read proxy settings")
	}
	h := strings.Trim(strings.TrimSpace(string(host)), "'")
	p := strings.TrimSpace(string(port))
	if h == "" || p == "0" {
		return "", fmt.Errorf("sysproxy: proxy not set")
	}
	return "http://" + h + ":" + p, nil
}

func setGlobalPAC(pacURL string) error {
	switch detectDesktopEnv() {
	case "gnome":
		if isAvailable("gsettings") {
			_ = runGsettings("set", "org.gnome.system.proxy", "mode", "auto")
			_ = runGsettings("set", "org.gnome.system.proxy", "autoconfig-url", pacURL)
		}
	case "kde":
		if isAvailable("kwriteconfig5") {
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "2")
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "Proxy Config Script", pacURL)
		}
	}
	return nil
}

func setGlobalMulti(cfg ProxyConfig) error {
	switch detectDesktopEnv() {
	case "gnome":
		if isAvailable("gsettings") {
			if cfg.HTTP != "" {
				_ = runGsettings("set", "org.gnome.system.proxy", "mode", "manual")
				_ = runGsettings("set", "org.gnome.system.proxy.http", "host", hostFromURL(cfg.HTTP))
				_ = runGsettings("set", "org.gnome.system.proxy.http", "port", portFromURL(cfg.HTTP))
			}
			if cfg.HTTPS != "" {
				_ = runGsettings("set", "org.gnome.system.proxy.https", "host", hostFromURL(cfg.HTTPS))
				_ = runGsettings("set", "org.gnome.system.proxy.https", "port", portFromURL(cfg.HTTPS))
			}
			if cfg.SOCKS != "" {
				_ = runGsettings("set", "org.gnome.system.proxy.socks", "host", hostFromURL(cfg.SOCKS))
				_ = runGsettings("set", "org.gnome.system.proxy.socks", "port", portFromURL(cfg.SOCKS))
			}
		}
	case "kde":
		if isAvailable("kwriteconfig5") {
			_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "1")
			if cfg.HTTP != "" {
				_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy", cfg.HTTP)
			}
			if cfg.HTTPS != "" {
				_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpsProxy", cfg.HTTPS)
			}
			if cfg.SOCKS != "" {
				_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "socksProxy", cfg.SOCKS)
			}
			if cfg.NoProxy != "" {
				_ = runKwriteconfig5("--file", "kioslaverc", "--group", "Proxy Settings", "--key", "NoProxyFor", cfg.NoProxy)
			}
		}
	}
	return nil
}

func detectDesktopEnv() string {
	for _, env := range []string{"XDG_CURRENT_DESKTOP", "DESKTOP_SESSION", "GDMSESSION"} {
		v := strings.ToLower(os.Getenv(env))
		switch {
		case strings.Contains(v, "gnome"):
			return "gnome"
		case strings.Contains(v, "kde"):
			return "kde"
		}
	}
	return ""
}

func writeEtcEnvironment(path, proxyURL string) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	proxyKeys := map[string]bool{
		"http_proxy": true, "HTTP_PROXY": true,
		"https_proxy": true, "HTTPS_PROXY": true,
		"all_proxy": true, "ALL_PROXY": true,
		"no_proxy": true, "NO_PROXY": true,
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if !proxyKeys[strings.TrimSpace(key)] {
			kept = append(kept, line)
		}
	}
	kept = append(kept,
		"http_proxy="+proxyURL, "HTTP_PROXY="+proxyURL,
		"https_proxy="+proxyURL, "HTTPS_PROXY="+proxyURL,
		"all_proxy="+proxyURL, "ALL_PROXY="+proxyURL,
		"no_proxy=localhost,127.0.0.1,::1",
		"NO_PROXY=localhost,127.0.0.1,::1",
	)
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("sysproxy: %s write failed (root required for system-wide effect): %w", path, err)
	}
	return nil
}

func clearEtcEnvironment(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil // file absent is fine
	}
	proxyKeys := map[string]bool{
		"http_proxy": true, "HTTP_PROXY": true,
		"https_proxy": true, "HTTPS_PROXY": true,
		"all_proxy": true, "ALL_PROXY": true,
		"no_proxy": true, "NO_PROXY": true,
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if !proxyKeys[strings.TrimSpace(key)] {
			kept = append(kept, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644) //nolint:gosec
}
