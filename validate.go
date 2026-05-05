package sysproxy

import (
	"fmt"
	"net/url"
	"strings"
)

// proxy holds a parsed proxy URL for internal use by platform backends.
type proxy struct {
	rawURL string
	host   string
	port   string
	user   string
	pass   string
}

func parse(rawURL string) (*proxy, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("sysproxy: invalid URL: %w", err)
	}
	p := &proxy{
		rawURL: rawURL,
		host:   u.Hostname(),
		port:   u.Port(),
	}
	if u.User != nil {
		p.user = u.User.Username()
		p.pass, _ = u.User.Password()
	}
	return p, nil
}

func validateProxyURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("sysproxy: cannot parse proxy URL %q: %w", rawURL, err)
	}
	if u.Scheme == "" {
		return fmt.Errorf("sysproxy: proxy URL %q missing scheme (e.g. http://host:port)", rawURL)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("sysproxy: proxy URL %q missing host", rawURL)
	}
	if port := u.Port(); port != "" {
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("sysproxy: port %q out of range 1–65535 in proxy URL %q", port, rawURL)
		}
	}
	return nil
}

func validatePACURL(pacURL string) error {
	if !strings.HasPrefix(pacURL, "http://") &&
		!strings.HasPrefix(pacURL, "https://") &&
		!strings.HasPrefix(pacURL, "file://") {
		return fmt.Errorf("sysproxy: PAC URL must use http, https, or file scheme (got %q)", pacURL)
	}
	return nil
}
