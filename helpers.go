package sysproxy

import (
	"net"
	"os/exec"
	"strings"
)

// joinHostPort is net.JoinHostPort for a host that may already carry the
// brackets of an IPv6 literal, as some tools store it.
func joinHostPort(host, port string) string {
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func isAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// isSOCKSScheme reports whether scheme names a SOCKS proxy protocol. Every
// other scheme (http, https) is an HTTP proxy.
func isSOCKSScheme(scheme string) bool {
	switch scheme {
	case "socks", "socks4", "socks4a", "socks5", "socks5h":
		return true
	}
	return false
}

// isSOCKS reports whether the proxy speaks SOCKS and so belongs in the SOCKS
// field of the system settings rather than the HTTP/HTTPS ones.
func (p *proxy) isSOCKS() bool { return isSOCKSScheme(p.scheme) }

// hostPort returns "host:port", bracketing an IPv6 literal.
func (p *proxy) hostPort() string { return net.JoinHostPort(p.host, p.port) }

// endpointFromURL returns the host and port of a proxy URL, with the default
// port of its scheme when the URL has none. Both are empty if rawURL does not
// parse; callers validate beforehand.
func endpointFromURL(rawURL string) (host, port string) {
	p, err := parse(rawURL)
	if err != nil {
		return "", ""
	}
	return p.host, p.port
}
