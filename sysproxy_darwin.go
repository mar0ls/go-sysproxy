//go:build darwin

package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

func runNetworkSetup(args ...string) error {
	return exec.Command("networksetup", args...).Run() //nolint:noctx
}

func setGlobal(p *proxy) error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		if p.user != "" {
			_ = runNetworkSetup("-setwebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = runNetworkSetup("-setsecurewebproxy", svc, p.host, p.port, "on", p.user, p.pass)
			_ = runNetworkSetup("-setsocksfirewallproxy", svc, p.host, p.port, "on", p.user, p.pass)
		} else {
			_ = runNetworkSetup("-setwebproxy", svc, p.host, p.port)
			_ = runNetworkSetup("-setsecurewebproxy", svc, p.host, p.port)
			_ = runNetworkSetup("-setsocksfirewallproxy", svc, p.host, p.port)
		}
		_ = runNetworkSetup("-setwebproxystate", svc, "on")
		_ = runNetworkSetup("-setsecurewebproxystate", svc, "on")
		_ = runNetworkSetup("-setsocksfirewallproxystate", svc, "on")
	}
	return nil
}

func unsetGlobal() error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		_ = runNetworkSetup("-setwebproxystate", svc, "off")
		_ = runNetworkSetup("-setsecurewebproxystate", svc, "off")
		_ = runNetworkSetup("-setsocksfirewallproxystate", svc, "off")
	}
	return nil
}

func getGlobal() (string, error) {
	services, err := macOSNetworkServices()
	if err != nil || len(services) == 0 {
		return "", fmt.Errorf("sysproxy: no network services found")
	}
	out, err := exec.Command("networksetup", "-getwebproxy", services[0]).Output() //nolint:noctx
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

func setGlobalPAC(pacURL string) error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		_ = runNetworkSetup("-setautoproxyurl", svc, pacURL)
		_ = runNetworkSetup("-setautoproxystate", svc, "on")
	}
	return nil
}

func setGlobalMulti(cfg ProxyConfig) error {
	services, err := macOSNetworkServices()
	if err != nil {
		return err
	}
	for _, svc := range services {
		if cfg.HTTP != "" {
			_ = runNetworkSetup("-setwebproxy", svc, hostFromURL(cfg.HTTP), portFromURL(cfg.HTTP))
			_ = runNetworkSetup("-setwebproxystate", svc, "on")
		}
		if cfg.HTTPS != "" {
			_ = runNetworkSetup("-setsecurewebproxy", svc, hostFromURL(cfg.HTTPS), portFromURL(cfg.HTTPS))
			_ = runNetworkSetup("-setsecurewebproxystate", svc, "on")
		}
		if cfg.SOCKS != "" {
			_ = runNetworkSetup("-setsocksfirewallproxy", svc, hostFromURL(cfg.SOCKS), portFromURL(cfg.SOCKS))
			_ = runNetworkSetup("-setsocksfirewallproxystate", svc, "on")
		}
		if cfg.NoProxy != "" {
			_ = runNetworkSetup("-setproxybypassdomains", svc, cfg.NoProxy)
		}
	}
	return nil
}

func macOSNetworkServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output() //nolint:noctx
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
