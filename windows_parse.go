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

// proxyServerValue is the ProxyServer registry value for a single proxy. A
// bare host:port is an HTTP proxy for every protocol; SOCKS needs its own entry.
func proxyServerValue(p *proxy) string {
	if p.isSOCKS() {
		return "socks=" + p.hostPort()
	}
	return p.hostPort()
}

// proxyOverrideValue is the ProxyOverride value for a bypass list; the registry
// separates entries with semicolons.
func proxyOverrideValue(bypass []string) string { return strings.Join(bypass, ";") }

// firstProxyURL returns the first configured kind of cfg in the order HTTP,
// HTTPS, SOCKS, so that a proxy set from a socks5:// URL can be read back.
func firstProxyURL(cfg ProxyConfig) string {
	for _, u := range []string{cfg.HTTP, cfg.HTTPS, cfg.SOCKS} {
		if u != "" {
			return u
		}
	}
	return ""
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

// parseRegQuery returns the type and data of the value called name in
// `reg query` output (a line like "    ProxyOverride    REG_SZ    a;b c"). Unlike
// extractRegValue it keeps data that contains spaces.
func parseRegQuery(output, name string) (typ, data string, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		rest, found := strings.CutPrefix(line, name)
		if !found || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest = strings.TrimSpace(rest)
		typ, rest, _ = strings.Cut(rest, " ")
		if !strings.HasPrefix(typ, "REG_") {
			continue
		}
		return typ, strings.TrimSpace(rest), true
	}
	return "", "", false
}

// proxyServerHost returns the host of a ProxyServer registry value, which is
// either "host:port" or "http=host:port;https=…".
func proxyServerHost(server string) string {
	if strings.Contains(server, "=") {
		first, _, _ := strings.Cut(server, ";")
		_, server, _ = strings.Cut(first, "=")
	}
	if idx := strings.LastIndex(server, ":"); idx > 0 && !strings.HasSuffix(server, "]") {
		server = server[:idx]
	}
	return strings.Trim(server, "[]")
}
