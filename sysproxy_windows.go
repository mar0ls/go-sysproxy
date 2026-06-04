//go:build windows

package sysproxy

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func runReg(ctx context.Context, args ...string) error {
	return exec.CommandContext(normalizeContext(ctx), "reg", args...).Run()
}

func runCmdkey(ctx context.Context, args ...string) error {
	return exec.CommandContext(normalizeContext(ctx), "cmdkey", args...).Run()
}

func runRundll32(ctx context.Context, args ...string) error {
	return exec.CommandContext(normalizeContext(ctx), "rundll32.exe", args...).Run()
}

func setGlobal(ctx context.Context, p *proxy) error {
	_ = runReg(ctx, "delete", regKey, "/v", "AutoConfigURL", "/f")
	_ = runReg(ctx, "add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	_ = runReg(ctx, "add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", p.host+":"+p.port, "/f")
	_ = runReg(ctx, "add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", "localhost;127.0.0.1;::1", "/f")
	if p.user != "" {
		_ = runCmdkey(ctx, "/add:"+p.host, "/user:"+p.user, "/pass:"+p.pass)
	}
	_ = runRundll32(ctx, "wininet.dll,InternetSetOptionEx")
	return nil
}

func unsetGlobal(ctx context.Context) error {
	_ = runReg(ctx, "add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	_ = runReg(ctx, "delete", regKey, "/v", "ProxyServer", "/f")
	_ = runReg(ctx, "delete", regKey, "/v", "ProxyOverride", "/f")
	_ = runReg(ctx, "delete", regKey, "/v", "AutoConfigURL", "/f")
	if host, err := currentProxyHost(ctx); err == nil && host != "" {
		_ = runCmdkey(ctx, "/delete:"+host)
	}
	return nil
}

// currentProxyHost reads the proxy host from the registry so Unset can clean
// up Credential Manager without requiring the caller to pass the original URL.
func currentProxyHost(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
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

func getGlobal(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyEnable").Output()
	if err != nil || !strings.Contains(string(out), "0x1") {
		return "", fmt.Errorf("sysproxy: proxy not enabled")
	}
	out, err = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
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

func setGlobalPAC(ctx context.Context, pacURL string) error {
	_ = runReg(ctx, "add", regKey, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", pacURL, "/f")
	_ = runReg(ctx, "add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	return nil
}

func setGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	_ = runReg(ctx, "delete", regKey, "/v", "AutoConfigURL", "/f")
	_ = runReg(ctx, "add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
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
		_ = runReg(ctx, "add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", strings.Join(servers, ";"), "/f")
	}
	if cfg.NoProxy != "" {
		_ = runReg(ctx, "add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", cfg.NoProxy, "/f")
	}
	return nil
}

// parseWindowsProxyServer converts a ProxyServer registry value to ProxyConfig.
// The value is either "host:port" (single proxy for all protocols) or
// "http=h:p;https=h:p;socks=h:p" (per-protocol).
func parseWindowsProxyServer(server string) ProxyConfig {
	var cfg ProxyConfig
	if !strings.Contains(server, "=") {
		cfg.HTTP = "http://" + server
		cfg.HTTPS = "http://" + server
		cfg.SOCKS = "socks5://" + server
		return cfg
	}
	for _, part := range strings.Split(server, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch k {
		case "http":
			cfg.HTTP = "http://" + v
		case "https":
			cfg.HTTPS = "http://" + v
		case "socks":
			cfg.SOCKS = "socks5://" + v
		}
	}
	return cfg
}

// extractRegValue returns the last whitespace-separated field on the line
// containing key in reg.exe output.
func extractRegValue(output, key string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, key) {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
	}
	return ""
}

func getGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyEnable").Output()
	if err != nil || !strings.Contains(string(out), "0x1") {
		return ProxyConfig{}, fmt.Errorf("sysproxy: proxy not enabled")
	}
	out, err = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
	if err != nil {
		return ProxyConfig{}, fmt.Errorf("sysproxy: cannot read ProxyServer")
	}
	server := extractRegValue(string(out), "ProxyServer")
	if server == "" {
		return ProxyConfig{}, fmt.Errorf("sysproxy: proxy not set")
	}
	cfg := parseWindowsProxyServer(server)

	out, _ = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyOverride").Output()
	cfg.NoProxy = extractRegValue(string(out), "ProxyOverride")

	return cfg, nil
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

// windowsBackend implements globalBackend using the Windows registry and WinINET.
type windowsBackend struct{}

func (windowsBackend) SetGlobal(ctx context.Context, p *proxy) error { return setGlobal(ctx, p) }
func (windowsBackend) UnsetGlobal(ctx context.Context) error         { return unsetGlobal(ctx) }
func (windowsBackend) GetGlobal(ctx context.Context) (string, error) { return getGlobal(ctx) }
func (windowsBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	return getGlobalConfig(ctx)
}
func (windowsBackend) SetGlobalPAC(ctx context.Context, u string) error { return setGlobalPAC(ctx, u) }
func (windowsBackend) SetGlobalMulti(ctx context.Context, c ProxyConfig) error {
	return setGlobalMulti(ctx, c)
}

func init() { activeBackend = windowsBackend{} }

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
