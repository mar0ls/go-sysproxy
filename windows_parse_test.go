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

func TestParseRegQuery(t *testing.T) {
	out := "\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\r\n" +
		"    ProxyEnable    REG_DWORD    0x1\r\n" +
		"    ProxyOverride    REG_SZ    localhost;127.0.0.1;<local> *.corp.internal\r\n" +
		"    ProxyServerOld    REG_SZ    wrong:1\r\n" +
		"    AutoConfigURL    REG_SZ    \r\n\r\n"
	cases := []struct {
		name, typ, data string
		ok              bool
	}{
		{"ProxyEnable", "REG_DWORD", "0x1", true},
		{"ProxyOverride", "REG_SZ", "localhost;127.0.0.1;<local> *.corp.internal", true},
		{"AutoConfigURL", "REG_SZ", "", true},
		{"ProxyServer", "", "", false}, // must not match ProxyServerOld
		{"Missing", "", "", false},
	}
	for _, c := range cases {
		typ, data, ok := parseRegQuery(out, c.name)
		if typ != c.typ || data != c.data || ok != c.ok {
			t.Errorf("parseRegQuery(%q) = (%q, %q, %v), want (%q, %q, %v)", c.name, typ, data, ok, c.typ, c.data, c.ok)
		}
	}
}

func TestProxyServerHost(t *testing.T) {
	cases := map[string]string{
		"proxy.example.com:8080":                        "proxy.example.com",
		"proxy.example.com":                             "proxy.example.com",
		"http=a.example.com:80;https=b.example.com:443": "a.example.com",
		"socks=s.example.com:1080":                      "s.example.com",
		"":                                              "",
		"[::1]:8080":                                    "::1",
		"socks=[2001:db8::1]:1080":                      "2001:db8::1",
	}
	for in, want := range cases {
		if got := proxyServerHost(in); got != want {
			t.Errorf("proxyServerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// F-13/F-14: the registry value follows the scheme, has a port, and brackets IPv6.
func TestProxyServerValue(t *testing.T) {
	cases := map[string]string{
		"http://proxy.example.com:8080":   "proxy.example.com:8080",
		"http://proxy.example.com":        "proxy.example.com:80",
		"https://proxy.example.com":       "proxy.example.com:443",
		"socks5://proxy.example.com:1080": "socks=proxy.example.com:1080",
		"socks5://proxy.example.com":      "socks=proxy.example.com:1080",
		"http://[::1]:3128":               "[::1]:3128",
	}
	for raw, want := range cases {
		p, err := parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := proxyServerValue(p); got != want {
			t.Errorf("proxyServerValue(%q) = %q, want %q", raw, got, want)
		}
	}
}

// What a SOCKS Set writes must be readable as a SOCKS proxy.
func TestProxyServerValue_RoundTrip(t *testing.T) {
	p, _ := parse("socks5://s.example.com:1080")
	if got := firstProxyURL(parseWindowsProxyServer(proxyServerValue(p))); got != "socks5://s.example.com:1080" {
		t.Errorf("round trip = %q", got)
	}
	p, _ = parse("http://h.example.com:3128")
	if got := firstProxyURL(parseWindowsProxyServer(proxyServerValue(p))); got != "http://h.example.com:3128" {
		t.Errorf("round trip = %q", got)
	}
}

func TestProxyOverrideValue(t *testing.T) {
	if got := proxyOverrideValue(splitNoProxy("localhost, 10.0.0.0/8,<local>")); got != "localhost;10.0.0.0/8;<local>" {
		t.Errorf("proxyOverrideValue = %q", got)
	}
}
