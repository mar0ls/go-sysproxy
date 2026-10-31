package sysproxy

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// proxy holds a parsed proxy URL for internal use by platform backends.
type proxy struct {
	rawURL string
	scheme string
	host   string
	port   string
	user   string
	pass   string
}

func parse(rawURL string) (*proxy, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("sysproxy: invalid URL: %w", unwrapURLError(err))
	}
	p := &proxy{
		rawURL: rawURL,
		scheme: u.Scheme,
		host:   u.Hostname(),
		port:   u.Port(),
	}
	if p.port == "" && p.host != "" {
		// A URL without a port means the scheme's default; backends must never
		// be handed an empty port.
		p.port = defaultProxyPort(u.Scheme)
	}
	if u.User != nil {
		p.user = u.User.Username()
		p.pass, _ = u.User.Password()
	}
	return p, nil
}

// hasUnsafeChars reports whether s contains whitespace or control characters.
// None of the values this package accepts (proxy URL, PAC URL) may contain
// them, and they are the raw material for line-injection into config files.
func hasUnsafeChars(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// unwrapURLError drops the *url.Error wrapper, whose message repeats the full
// input URL — including any password.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// redactURL masks the password in the userinfo of raw so the value is safe to
// put in logs and error messages. It works on a best-effort textual basis, so
// it also masks input that url.Parse would reject.
func redactURL(raw string) string {
	prefix, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 {
		prefix, rest = raw[:i+3], raw[i+3:]
	}
	authority := rest
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		authority = rest[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw
	}
	userinfo := authority[:at]
	if c := strings.Index(userinfo, ":"); c >= 0 {
		userinfo = userinfo[:c] + ":***"
	}
	return prefix + userinfo + rest[at:]
}

func validateProxyURL(rawURL string) error {
	shown := redactURL(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("sysproxy: cannot parse proxy URL %q: %w", shown, unwrapURLError(err))
	}
	if hasUnsafeChars(rawURL) {
		return fmt.Errorf("sysproxy: proxy URL %q contains whitespace or control characters", shown)
	}
	if u.Scheme == "" {
		return fmt.Errorf("sysproxy: proxy URL %q missing scheme (e.g. http://host:port)", shown)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("sysproxy: proxy URL %q missing host", shown)
	}
	if port := u.Port(); port != "" {
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("sysproxy: port %q out of range 1–65535 in proxy URL %q", port, shown)
		}
	}
	return nil
}

func validatePACURL(pacURL string) error {
	shown := redactURL(pacURL)
	if !strings.HasPrefix(pacURL, "http://") &&
		!strings.HasPrefix(pacURL, "https://") &&
		!strings.HasPrefix(pacURL, "file://") {
		return fmt.Errorf("sysproxy: PAC URL must use http, https, or file scheme (got %q)", shown)
	}
	if hasUnsafeChars(pacURL) {
		return fmt.Errorf("sysproxy: PAC URL %q contains whitespace or control characters", shown)
	}
	u, err := url.Parse(pacURL)
	if err != nil {
		return fmt.Errorf("sysproxy: cannot parse PAC URL %q: %w", shown, unwrapURLError(err))
	}
	if u.Scheme != "file" && u.Hostname() == "" {
		return fmt.Errorf("sysproxy: PAC URL %q missing host", shown)
	}
	return nil
}

// validateNoProxy checks a comma/semicolon separated bypass list. Only
// ASCII characters that can appear in host names, IP addresses, CIDR ranges,
// ports, wildcards and the Windows "<local>" token are accepted; quotes,
// backslashes, '$', backticks and every kind of control character are not.
// Internationalised host names must be given in punycode: the profile files and
// code pages involved do not all read non-ASCII text back unchanged, and the
// punycode form is what a client compares against anyway.
func validateNoProxy(noProxy string) error {
	for _, r := range noProxy {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-*:/,;<>[]% ", r):
		case r > unicode.MaxASCII && unicode.IsLetter(r):
			return fmt.Errorf("sysproxy: NoProxy contains the non-ASCII character %q: write an internationalised host name in its punycode form (xn--…)", r)
		default:
			return fmt.Errorf("sysproxy: NoProxy contains unsupported character %q", r)
		}
	}
	return nil
}
