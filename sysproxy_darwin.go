//go:build darwin

package sysproxy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func runNetworkSetup(ctx context.Context, args ...string) error {
	return exec.CommandContext(normalizeContext(ctx), "networksetup", args...).Run() //nolint:gosec
}

func setGlobal(ctx context.Context, p *proxy) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	for _, svc := range services {
		// Ensure manual proxy mode is authoritative and PAC does not stay active.
		_ = runNetworkSetup(ctx, "-setautoproxystate", svc, "off")
		if p.user != "" {
			_ = runNetworkSetup(ctx, "-setwebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = runNetworkSetup(ctx, "-setsecurewebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = runNetworkSetup(ctx, "-setsocksfirewallproxy", svc, p.host, p.port, "on", p.user, p.pass)
		} else {
			_ = runNetworkSetup(ctx, "-setwebproxy", svc, p.host, p.port)
			_ = runNetworkSetup(ctx, "-setsecurewebproxy", svc, p.host, p.port)
			_ = runNetworkSetup(ctx, "-setsocksfirewallproxy", svc, p.host, p.port)
		}
		_ = runNetworkSetup(ctx, "-setwebproxystate", svc, "on")
		_ = runNetworkSetup(ctx, "-setsecurewebproxystate", svc, "on")
		_ = runNetworkSetup(ctx, "-setsocksfirewallproxystate", svc, "on")
	}
	return nil
}

func unsetGlobal(ctx context.Context) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	for _, svc := range services {
		// Unset should clear both manual and auto-proxy modes.
		_ = runNetworkSetup(ctx, "-setautoproxystate", svc, "off")
		_ = runNetworkSetup(ctx, "-setwebproxystate", svc, "off")
		_ = runNetworkSetup(ctx, "-setsecurewebproxystate", svc, "off")
		_ = runNetworkSetup(ctx, "-setsocksfirewallproxystate", svc, "off")
		// Clear host/port so the previous values do not stay visible in System Settings.
		_ = runNetworkSetup(ctx, "-setwebproxy", svc, "", "0")
		_ = runNetworkSetup(ctx, "-setsecurewebproxy", svc, "", "0")
		_ = runNetworkSetup(ctx, "-setsocksfirewallproxy", svc, "", "0")
	}
	return nil
}

// parseNSProxyOutput extracts host, port and enabled state from networksetup output.
func parseNSProxyOutput(output string) (host, port string, enabled bool) {
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "Enabled: Yes"):
			enabled = true
		case strings.HasPrefix(line, "Server:"):
			host = strings.TrimSpace(strings.TrimPrefix(line, "Server:"))
		case strings.HasPrefix(line, "Port:"):
			port = strings.TrimSpace(strings.TrimPrefix(line, "Port:"))
		}
	}
	return
}

func getGlobal(ctx context.Context) (string, error) {
	services, err := macOSNetworkServices(ctx)
	if err != nil || len(services) == 0 {
		return "", fmt.Errorf("sysproxy: no network services found")
	}
	svc := services[0]
	out, err := exec.CommandContext(normalizeContext(ctx), "networksetup", "-getwebproxy", svc).Output() //nolint:gosec
	if err != nil {
		return "", err
	}
	h, p, ok := parseNSProxyOutput(string(out))
	if ok && h != "" && p != "0" {
		return "http://" + h + ":" + p, nil
	}
	if pac, ok := readMacPAC(ctx, svc); ok {
		return pac, nil
	}
	return "", ErrProxyNotSet
}

// readMacPAC returns the auto-proxy (PAC) URL for a network service when
// auto-proxy is on. The second return value is false when auto-proxy is off
// or the URL is empty.
func readMacPAC(ctx context.Context, svc string) (string, bool) {
	out, err := exec.CommandContext(normalizeContext(ctx), "networksetup", "-getautoproxyurl", svc).Output() //nolint:gosec
	if err != nil {
		return "", false
	}
	return parseAutoProxyOutput(string(out))
}

// parseAutoProxyOutput extracts a PAC URL from `networksetup -getautoproxyurl`
// output. It returns ok=false when auto-proxy is disabled or the URL is a
// placeholder (empty, "(null)").
func parseAutoProxyOutput(out string) (string, bool) {
	var url string
	var enabled bool
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "URL:"):
			url = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		case strings.HasPrefix(line, "Enabled: Yes"):
			enabled = true
		}
	}
	if !enabled || url == "" || url == "(null)" {
		return "", false
	}
	return url, true
}

func getGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	services, err := macOSNetworkServices(ctx)
	if err != nil || len(services) == 0 {
		return ProxyConfig{}, fmt.Errorf("sysproxy: no network services found")
	}
	svc := services[0]
	ctx = normalizeContext(ctx)

	var cfg ProxyConfig
	for _, q := range []struct {
		flag   string
		scheme string
		dest   *string
	}{
		{"-getwebproxy", "http", &cfg.HTTP},
		{"-getsecurewebproxy", "http", &cfg.HTTPS},
		{"-getsocksfirewallproxy", "socks5", &cfg.SOCKS},
	} {
		out, err := exec.CommandContext(ctx, "networksetup", q.flag, svc).Output() //nolint:gosec
		if err == nil {
			h, p, ok := parseNSProxyOutput(string(out))
			if ok && h != "" && p != "0" {
				*q.dest = q.scheme + "://" + h + ":" + p
			}
		}
	}

	out, err := exec.CommandContext(ctx, "networksetup", "-getproxybypassdomains", svc).Output() //nolint:gosec
	if err == nil {
		var parts []string
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				parts = append(parts, l)
			}
		}
		cfg.NoProxy = strings.Join(parts, ",")
	}

	if pac, ok := readMacPAC(ctx, svc); ok {
		cfg.PAC = pac
	}

	if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" && cfg.PAC == "" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	return cfg, nil
}

func setGlobalPAC(ctx context.Context, pacURL string) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	for _, svc := range services {
		_ = runNetworkSetup(ctx, "-setautoproxyurl", svc, pacURL)
		_ = runNetworkSetup(ctx, "-setautoproxystate", svc, "on")
	}
	return nil
}

func setGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	for _, svc := range services {
		// Multi-protocol manual config should disable PAC mode.
		_ = runNetworkSetup(ctx, "-setautoproxystate", svc, "off")
		if cfg.HTTP != "" {
			_ = runNetworkSetup(ctx, "-setwebproxy", svc, hostFromURL(cfg.HTTP), portFromURL(cfg.HTTP))
			_ = runNetworkSetup(ctx, "-setwebproxystate", svc, "on")
		}
		if cfg.HTTPS != "" {
			_ = runNetworkSetup(ctx, "-setsecurewebproxy", svc, hostFromURL(cfg.HTTPS), portFromURL(cfg.HTTPS))
			_ = runNetworkSetup(ctx, "-setsecurewebproxystate", svc, "on")
		}
		if cfg.SOCKS != "" {
			_ = runNetworkSetup(ctx, "-setsocksfirewallproxy", svc, hostFromURL(cfg.SOCKS), portFromURL(cfg.SOCKS))
			_ = runNetworkSetup(ctx, "-setsocksfirewallproxystate", svc, "on")
		}
		if cfg.NoProxy != "" {
			_ = runNetworkSetup(ctx, "-setproxybypassdomains", svc, cfg.NoProxy)
		}
	}
	return nil
}

// darwinBackend implements globalBackend using macOS networksetup.
type darwinBackend struct{}

func (darwinBackend) SetGlobal(ctx context.Context, p *proxy) error { return setGlobal(ctx, p) }
func (darwinBackend) UnsetGlobal(ctx context.Context) error         { return unsetGlobal(ctx) }
func (darwinBackend) GetGlobal(ctx context.Context) (string, error) { return getGlobal(ctx) }
func (darwinBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	return getGlobalConfig(ctx)
}
func (darwinBackend) SetGlobalPAC(ctx context.Context, u string) error { return setGlobalPAC(ctx, u) }
func (darwinBackend) SetGlobalMulti(ctx context.Context, c ProxyConfig) error {
	return setGlobalMulti(ctx, c)
}

func init() { activeBackend = darwinBackend{} }

func macOSNetworkServices(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("sysproxy: networksetup: %w", err)
	}
	var svcs []string
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, strings.TrimSpace(line))
	}
	return svcs, nil
}
