// Package sysproxy sets and clears the OS-level HTTP/HTTPS/SOCKS proxy on
// macOS (networksetup), Linux (gsettings + /etc/environment), and Windows
// (registry + Credential Manager).
package sysproxy

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type proxy struct {
	rawURL string
	host   string
	port   string
	user   string
	pass   string
}

// Set configures the system proxy to proxyURL (e.g. "http://user:pass@host:port").
// It also exports HTTP_PROXY / HTTPS_PROXY / ALL_PROXY for the current process.
func Set(proxyURL string) error {
	p, err := parse(proxyURL)
	if err != nil {
		return err
	}
	setEnvVars(proxyURL)
	switch runtime.GOOS {
	case "linux":
		return p.setLinux()
	case "darwin":
		return p.setMacOS()
	case "windows":
		return p.setWindows()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// Unset clears the system proxy. Pass the same URL used in Set so that
// stored credentials can be removed on Windows.
func Unset(proxyURL string) error {
	p, _ := parse(proxyURL)
	unsetEnvVars()
	switch runtime.GOOS {
	case "linux":
		return unsetLinux()
	case "darwin":
		return unsetMacOS()
	case "windows":
		return p.unsetWindows()
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func parse(rawURL string) (*proxy, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	p := &proxy{
		rawURL: rawURL,
		host:   u.Hostname(),
		port:   u.Port(),
	}
	if u.User != nil {
		p.user = u.User.Username()
		p.pass, _ = u.User.Password()
	}
	return p, nil
}

func setEnvVars(proxyURL string) {
	for _, v := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		_ = os.Setenv(v, proxyURL)
	}
	_ = os.Setenv("no_proxy", "localhost,127.0.0.1,::1")
	_ = os.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
}

func unsetEnvVars() {
	for _, v := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY",
	} {
		_ = os.Unsetenv(v)
	}
}

// ── Linux ────────────────────────────────────────────────────────────────────

func (p *proxy) setLinux() error {
	if isAvailable("gsettings") {
		_ = run("gsettings", "set", "org.gnome.system.proxy", "mode", "manual")
		_ = run("gsettings", "set", "org.gnome.system.proxy.http", "host", p.host)
		_ = run("gsettings", "set", "org.gnome.system.proxy.http", "port", p.port)
		_ = run("gsettings", "set", "org.gnome.system.proxy.https", "host", p.host)
		_ = run("gsettings", "set", "org.gnome.system.proxy.https", "port", p.port)
		_ = run("gsettings", "set", "org.gnome.system.proxy.socks", "host", p.host)
		_ = run("gsettings", "set", "org.gnome.system.proxy.socks", "port", p.port)
	}
	return writeEtcEnvironment(p.rawURL)
}

func unsetLinux() error {
	if isAvailable("gsettings") {
		_ = run("gsettings", "set", "org.gnome.system.proxy", "mode", "none")
	}
	return clearEtcEnvironment()
}

func writeEtcEnvironment(proxyURL string) error {
	const path = "/etc/environment"
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	proxyKeys := map[string]bool{
		"http_proxy": true, "HTTP_PROXY": true,
		"https_proxy": true, "HTTPS_PROXY": true,
		"all_proxy": true, "ALL_PROXY": true,
		"no_proxy": true, "NO_PROXY": true,
	}

	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if !proxyKeys[strings.TrimSpace(key)] {
			lines = append(lines, line)
		}
	}
	lines = append(lines,
		"http_proxy="+proxyURL, "HTTP_PROXY="+proxyURL,
		"https_proxy="+proxyURL, "HTTPS_PROXY="+proxyURL,
		"all_proxy="+proxyURL, "ALL_PROXY="+proxyURL,
		"no_proxy=localhost,127.0.0.1,::1",
		"NO_PROXY=localhost,127.0.0.1,::1",
	)

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("/etc/environment write failed (requires root for system-wide effect): %w", err)
	}
	return nil
}

func clearEtcEnvironment() error {
	const path = "/etc/environment"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	proxyKeys := map[string]bool{
		"http_proxy": true, "HTTP_PROXY": true,
		"https_proxy": true, "HTTPS_PROXY": true,
		"all_proxy": true, "ALL_PROXY": true,
		"no_proxy": true, "NO_PROXY": true,
	}

	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		key := strings.SplitN(line, "=", 2)[0]
		if !proxyKeys[strings.TrimSpace(key)] {
			lines = append(lines, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644) //nolint:gosec
}

// ── macOS ────────────────────────────────────────────────────────────────────

func (p *proxy) setMacOS() error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		if p.user != "" {
			_ = run("networksetup", "-setwebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = run("networksetup", "-setsecurewebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = run("networksetup", "-setsocksfirewallproxy", svc, p.host, p.port, "on", p.user, p.pass)
		} else {
			_ = run("networksetup", "-setwebproxy", svc, p.host, p.port)
			_ = run("networksetup", "-setsecurewebproxy", svc, p.host, p.port)
			_ = run("networksetup", "-setsocksfirewallproxy", svc, p.host, p.port)
		}
		_ = run("networksetup", "-setwebproxystate", svc, "on")
		_ = run("networksetup", "-setsecurewebproxystate", svc, "on")
		_ = run("networksetup", "-setsocksfirewallproxystate", svc, "on")
	}
	return nil
}

func unsetMacOS() error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		_ = run("networksetup", "-setwebproxystate", svc, "off")
		_ = run("networksetup", "-setsecurewebproxystate", svc, "off")
		_ = run("networksetup", "-setsocksfirewallproxystate", svc, "off")
	}
	return nil
}

func macOSNetworkServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output() //nolint:gosec,noctx
	if err != nil {
		return nil, fmt.Errorf("networksetup list: %w", err)
	}
	var services []string
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, strings.TrimSpace(line))
	}
	return services, nil
}

// ── Windows ──────────────────────────────────────────────────────────────────

func (p *proxy) setWindows() error {
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	_ = run("reg", "add", key, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	_ = run("reg", "add", key, "/v", "ProxyServer", "/t", "REG_SZ", "/d", p.host+":"+p.port, "/f")
	_ = run("reg", "add", key, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", "localhost;127.0.0.1;::1", "/f")
	if p.user != "" {
		_ = run("cmdkey", "/add:"+p.host, "/user:"+p.user, "/pass:"+p.pass)
	}
	_ = run("rundll32.exe", "wininet.dll,InternetSetOptionEx")
	return nil
}

func (p *proxy) unsetWindows() error {
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	_ = run("reg", "add", key, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	_ = run("reg", "delete", key, "/v", "ProxyServer", "/f")
	if p.host != "" {
		_ = run("cmdkey", "/delete:"+p.host)
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func run(name string, args ...string) error {
	return exec.Command(name, args...).Run() //nolint:gosec,noctx
}

func isAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
