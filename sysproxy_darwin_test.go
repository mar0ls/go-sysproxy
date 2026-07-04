//go:build darwin

package sysproxy

import "testing"

func TestParseAutoProxyOutput(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantURL string
		wantOK  bool
	}{
		{
			name:    "enabled with URL",
			input:   "URL: https://config.example.com/proxy.pac\nEnabled: Yes\n",
			wantURL: "https://config.example.com/proxy.pac",
			wantOK:  true,
		},
		{
			name:  "disabled",
			input: "URL: https://config.example.com/proxy.pac\nEnabled: No\n",
		},
		{
			name:  "(null) placeholder",
			input: "URL: (null)\nEnabled: Yes\n",
		},
		{
			name:  "empty",
			input: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, ok := parseAutoProxyOutput(c.input)
			if url != c.wantURL || ok != c.wantOK {
				t.Errorf("parseAutoProxyOutput(%q) = (%q, %v), want (%q, %v)",
					c.input, url, ok, c.wantURL, c.wantOK)
			}
		})
	}
}

func TestParseNSProxyOutput(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantHost    string
		wantPort    string
		wantEnabled bool
	}{
		{
			name:     "enabled with host and port",
			input:    "Enabled: Yes\nServer: proxy.example.com\nPort: 8080\n",
			wantHost: "proxy.example.com", wantPort: "8080", wantEnabled: true,
		},
		{
			name:     "disabled",
			input:    "Enabled: No\nServer: proxy.example.com\nPort: 8080\n",
			wantHost: "proxy.example.com", wantPort: "8080", wantEnabled: false,
		},
		{
			name:        "empty output",
			input:       "",
			wantHost:    "",
			wantPort:    "",
			wantEnabled: false,
		},
		{
			name:        "no server line",
			input:       "Enabled: Yes\nPort: 8080\n",
			wantHost:    "",
			wantPort:    "8080",
			wantEnabled: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, p, ok := parseNSProxyOutput(c.input)
			if h != c.wantHost || p != c.wantPort || ok != c.wantEnabled {
				t.Errorf("parseNSProxyOutput(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.input, h, p, ok, c.wantHost, c.wantPort, c.wantEnabled)
			}
		})
	}
}
