//go:build darwin

package sysproxy

import "testing"

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
