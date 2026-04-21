package sysproxy

import (
	"context"
	"fmt"
	"net"
	"net/url"
)

// Check verifies that proxyURL is reachable by opening a TCP connection.
// A nil error means the proxy port is open; it does not verify credentials or
// proxy-level protocol. Use a context with a deadline to control the timeout:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	err := sysproxy.Check(ctx, "http://proxy.example.com:8080")
func Check(ctx context.Context, proxyURL string) error {
	if err := validateProxyURL(proxyURL); err != nil {
		return err
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("sysproxy: %w", err)
	}
	port := u.Port()
	if port == "" {
		port = defaultProxyPort(u.Scheme)
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("sysproxy: proxy %s unreachable: %w", addr, err)
	}
	return conn.Close()
}

func defaultProxyPort(scheme string) string {
	switch scheme {
	case "https":
		return "443"
	case "socks5", "socks4", "socks":
		return "1080"
	default:
		return "80"
	}
}
