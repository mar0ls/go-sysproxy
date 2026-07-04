package sysproxy

import "strings"

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
