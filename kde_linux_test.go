//go:build linux

package sysproxy

import "testing"

func TestNormalizeKDEProxyURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"http://proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"https://proxy.example.com:443", "https://proxy.example.com:443"},
		// Legacy KControl form: "host port" separated by space.
		{"proxy.example.com 8080", "http://proxy.example.com:8080"},
		{" proxy.example.com 8080 ", "http://proxy.example.com:8080"},
		// Value without space or scheme: treat as host only, no port injection.
		{"proxy.example.com", "http://proxy.example.com"},
	}
	for _, c := range cases {
		if got := normalizeKDEProxyURL(c.in); got != c.want {
			t.Errorf("normalizeKDEProxyURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeKDESocksURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"proxy.example.com 1080", "socks5://proxy.example.com:1080"},
		{"socks5://proxy.example.com:1080", "socks5://proxy.example.com:1080"},
		{"http://proxy.example.com:1080", "socks5://proxy.example.com:1080"},
	}
	for _, c := range cases {
		if got := normalizeKDESocksURL(c.in); got != c.want {
			t.Errorf("normalizeKDESocksURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
