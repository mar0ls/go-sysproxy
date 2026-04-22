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
	}
	return nil
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
	var host, port string
	var enabled bool
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "Enabled: Yes"):
			enabled = true
		case strings.HasPrefix(line, "Server:"):
			host = strings.TrimSpace(strings.TrimPrefix(line, "Server:"))
		case strings.HasPrefix(line, "Port:"):
			port = strings.TrimSpace(strings.TrimPrefix(line, "Port:"))
		}
	}
	if enabled && host != "" && port != "0" {
		return "http://" + host + ":" + port, nil
	}
	return "", fmt.Errorf("sysproxy: proxy not set")
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
