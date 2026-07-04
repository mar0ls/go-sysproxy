package sysproxy

import "testing"

func TestParseWindowsProxyServer_Single(t *testing.T) {
	cfg := parseWindowsProxyServer("proxy.example.com:8080")
	if cfg.HTTP != "http://proxy.example.com:8080" ||
		cfg.HTTPS != "http://proxy.example.com:8080" ||
		cfg.SOCKS != "socks5://proxy.example.com:8080" {
		t.Errorf("single-value parse mismatch: %+v", cfg)
	}
}

func TestParseWindowsProxyServer_PerProtocol(t *testing.T) {
	cfg := parseWindowsProxyServer("http=h.example.com:8080;https=s.example.com:8443;socks=x.example.com:1080")
	if cfg.HTTP != "http://h.example.com:8080" {
		t.Errorf("HTTP = %q", cfg.HTTP)
	}
	if cfg.HTTPS != "http://s.example.com:8443" {
		t.Errorf("HTTPS = %q", cfg.HTTPS)
	}
	if cfg.SOCKS != "socks5://x.example.com:1080" {
		t.Errorf("SOCKS = %q", cfg.SOCKS)
	}
}

func TestParseWindowsProxyServer_MalformedPart(t *testing.T) {
	// Garbage segments must be skipped without panicking.
	cfg := parseWindowsProxyServer("http=h:8080;=nope;unknown=1;https=s:8443")
	if cfg.HTTP != "http://h:8080" || cfg.HTTPS != "http://s:8443" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
}

func TestExtractRegValue(t *testing.T) {
	out := `
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Internet Settings
    ProxyServer    REG_SZ    proxy.example.com:8080
`
	if got := extractRegValue(out, "ProxyServer"); got != "proxy.example.com:8080" {
		t.Errorf("extractRegValue = %q", got)
	}
	if got := extractRegValue(out, "Missing"); got != "" {
		t.Errorf("expected empty for missing key, got %q", got)
	}
}
