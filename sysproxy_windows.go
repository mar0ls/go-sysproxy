//go:build windows

package sysproxy

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func runReg(args ...string) error {
	return exec.Command("reg", args...).Run() //nolint:noctx
}

func runCmdkey(args ...string) error {
	return exec.Command("cmdkey", args...).Run() //nolint:noctx
}

func runRundll32(args ...string) error {
	return exec.Command("rundll32.exe", args...).Run() //nolint:noctx
}

func setGlobal(p *proxy) error {
	_ = runReg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	_ = runReg("add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", p.host+":"+p.port, "/f")
	_ = runReg("add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", "localhost;127.0.0.1;::1", "/f")
	if p.user != "" {
		_ = runCmdkey("/add:"+p.host, "/user:"+p.user, "/pass:"+p.pass)
	}
	_ = runRundll32("wininet.dll,InternetSetOptionEx")
	return nil
}

func unsetGlobal() error {
	_ = runReg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	_ = runReg("delete", regKey, "/v", "ProxyServer", "/f")
	_ = runReg("delete", regKey, "/v", "ProxyOverride", "/f")
	if host, err := currentProxyHost(); err == nil && host != "" {
		_ = runCmdkey("/delete:" + host)
	}
	return nil
}

// currentProxyHost reads the proxy host from the registry so Unset can clean
// up Credential Manager without requiring the caller to pass the original URL.
func currentProxyHost() (string, error) {
	out, err := exec.Command("reg", "query", regKey, "/v", "ProxyServer").Output() //nolint:noctx
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "ProxyServer") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				hp := parts[len(parts)-1]
				if idx := strings.LastIndex(hp, ":"); idx > 0 {
					return hp[:idx], nil
				}
				return hp, nil
			}
		}
	}
	return "", fmt.Errorf("sysproxy: ProxyServer not found in registry")
}

func getGlobal() (string, error) {
	out, err := exec.Command("reg", "query", regKey, "/v", "ProxyEnable").Output() //nolint:noctx
	if err != nil || !strings.Contains(string(out), "0x1") {
		return "", fmt.Errorf("sysproxy: proxy not enabled")
	}
	out, err = exec.Command("reg", "query", regKey, "/v", "ProxyServer").Output() //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("sysproxy: cannot read ProxyServer")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "ProxyServer") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return "http://" + parts[len(parts)-1], nil
			}
		}
	}
	return "", fmt.Errorf("sysproxy: proxy not set")
}

func setGlobalPAC(pacURL string) error {
	_ = runReg("add", regKey, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", pacURL, "/f")
	_ = runReg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	return nil
}

func setGlobalMulti(cfg ProxyConfig) error {
	_ = runReg("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	var servers []string
	if cfg.HTTP != "" {
		servers = append(servers, "http="+hostPortFromURL(cfg.HTTP))
	}
	if cfg.HTTPS != "" {
		servers = append(servers, "https="+hostPortFromURL(cfg.HTTPS))
	}
	if cfg.SOCKS != "" {
		servers = append(servers, "socks="+hostPortFromURL(cfg.SOCKS))
	}
	if len(servers) > 0 {
		_ = runReg("add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", strings.Join(servers, ";"), "/f")
	}
	if cfg.NoProxy != "" {
		_ = runReg("add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", cfg.NoProxy, "/f")
	}
	return nil
}

func setUser(proxyURL string) error {
	psProfile, err := powershellProfile()
	if err != nil {
		return nil // best-effort
	}
	f, err := os.OpenFile(psProfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	defer f.Close()
	for _, l := range []string{
		"$env:http_proxy = '" + proxyURL + "'",
		"$env:HTTP_PROXY = '" + proxyURL + "'",
		"$env:https_proxy = '" + proxyURL + "'",
		"$env:HTTPS_PROXY = '" + proxyURL + "'",
		"$env:all_proxy = '" + proxyURL + "'",
		"$env:ALL_PROXY = '" + proxyURL + "'",
		"$env:no_proxy = 'localhost,127.0.0.1,::1'",
		"$env:NO_PROXY = 'localhost,127.0.0.1,::1'",
	} {
		_, _ = f.WriteString(l + "\n")
	}
	return nil
}

func unsetUser() error {
	psProfile, err := powershellProfile()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(psProfile)
	if err != nil {
		return nil
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "$env:http_proxy") || strings.Contains(line, "$env:HTTP_PROXY") ||
			strings.Contains(line, "$env:https_proxy") || strings.Contains(line, "$env:HTTPS_PROXY") ||
			strings.Contains(line, "$env:all_proxy") || strings.Contains(line, "$env:ALL_PROXY") ||
			strings.Contains(line, "$env:no_proxy") || strings.Contains(line, "$env:NO_PROXY") ||
			strings.Contains(line, "$env:AUTOPROXY") {
			continue
		}
		kept = append(kept, line)
	}
	_ = os.WriteFile(psProfile, []byte(strings.Join(kept, "\n")), 0600)
	return nil
}

func setUserPAC(pacURL string) error {
	psProfile, err := powershellProfile()
	if err != nil {
		return nil
	}
	f, err := os.OpenFile(psProfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	defer f.Close()
	_, _ = f.WriteString("$env:AUTOPROXY = '" + pacURL + "'\n")
	return nil
}

func setUserMulti(cfg ProxyConfig) error {
	psProfile, err := powershellProfile()
	if err != nil {
		return nil
	}
	f, err := os.OpenFile(psProfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil
	}
	defer f.Close()
	if cfg.HTTP != "" {
		_, _ = f.WriteString("$env:http_proxy = '" + cfg.HTTP + "'\n")
		_, _ = f.WriteString("$env:HTTP_PROXY = '" + cfg.HTTP + "'\n")
	}
	if cfg.HTTPS != "" {
		_, _ = f.WriteString("$env:https_proxy = '" + cfg.HTTPS + "'\n")
		_, _ = f.WriteString("$env:HTTPS_PROXY = '" + cfg.HTTPS + "'\n")
	}
	if cfg.SOCKS != "" {
		_, _ = f.WriteString("$env:all_proxy = '" + cfg.SOCKS + "'\n")
		_, _ = f.WriteString("$env:ALL_PROXY = '" + cfg.SOCKS + "'\n")
	}
	if cfg.NoProxy != "" {
		_, _ = f.WriteString("$env:no_proxy = '" + cfg.NoProxy + "'\n")
		_, _ = f.WriteString("$env:NO_PROXY = '" + cfg.NoProxy + "'\n")
	}
	return nil
}

func hostPortFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return u.Hostname() + ":" + p
	}
	return u.Hostname()
}

func powershellProfile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home + `\Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1`, nil
}
