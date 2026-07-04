package sysproxy

import "testing"

// FuzzParse checks that parse() never panics on arbitrary input.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"http://proxy.example.com:8080",
		"socks5://user:pass@proxy.example.com:1080",
		"",
		"://bad",
		"http://[::1]:8080",
		"http://user@:8080",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parse(raw)
	})
}

// FuzzValidateProxyURL checks that validateProxyURL never panics, and that
// any URL it accepts also parses without error.
func FuzzValidateProxyURL(f *testing.F) {
	seeds := []string{
		"http://proxy.example.com:8080",
		"https://proxy.example.com:443",
		"socks5://proxy.example.com:1080",
		"http://user:pass@proxy.example.com:8080",
		"http://localhost:8080",
		"://bad",
		"http://",
		"http://x:0",
		"http://x:99999",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if err := validateProxyURL(raw); err == nil {
			if _, perr := parse(raw); perr != nil {
				t.Fatalf("validateProxyURL accepted %q but parse rejected it: %v", raw, perr)
			}
		}
	})
}

// FuzzValidatePACURL checks that validatePACURL never panics and only accepts
// URLs with http, https, or file schemes.
func FuzzValidatePACURL(f *testing.F) {
	seeds := []string{
		"http://config.example.com/proxy.pac",
		"https://config.example.com/proxy.pac",
		"file:///etc/proxy.pac",
		"ftp://bad.example.com/proxy.pac",
		"",
		"http",
		"://",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		err := validatePACURL(raw)
		if err == nil {
			ok := false
			for _, p := range []string{"http://", "https://", "file://"} {
				if len(raw) >= len(p) && raw[:len(p)] == p {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("validatePACURL accepted non-supported scheme %q", raw)
			}
		}
	})
}
