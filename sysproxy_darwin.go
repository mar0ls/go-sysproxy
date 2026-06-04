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
	out, err := exec.CommandContext(normalizeContext(ctx), "networksetup", "-getwebproxy", services[0]).Output() //nolint:gosec
	if err != nil {
		return "", err
	}
	h, p, ok := parseNSProxyOutput(string(out))
	if ok && h != "" && p != "0" {
		return "http://" + h + ":" + p, nil
	}
	return "", fmt.Errorf("sysproxy: proxy not set")
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
		flag string
		dest *string
	}{
		{"-getwebproxy", &cfg.HTTP},
		{"-getsecurewebproxy", &cfg.HTTPS},
		{"-getsocksfirewallproxy", &cfg.SOCKS},
	} {
		out, err := exec.CommandContext(ctx, "networksetup", q.flag, svc).Output() //nolint:gosec
		if err == nil {
			h, p, ok := parseNSProxyOutput(string(out))
			if ok && h != "" && p != "0" {
				*q.dest = "http://" + h + ":" + p
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

	if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" {
		return ProxyConfig{}, fmt.Errorf("sysproxy: proxy not set")
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
