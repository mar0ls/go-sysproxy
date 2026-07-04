//go:build linux

package sysproxy

import (
	"context"
	"os/exec"
	"strings"
)

// kdeProxyGroup identifies the KDE kioslaverc section that stores proxy config.
const kdeProxyGroup = "Proxy Settings"

// kreadconfigBinary returns the name of the kreadconfig tool available in PATH,
// preferring kreadconfig5 (Plasma 5) and falling back to kreadconfig6 (Plasma 6).
// Returns "" when neither is installed.
func kreadconfigBinary() string {
	if isAvailable("kreadconfig5") {
		return "kreadconfig5"
	}
	if isAvailable("kreadconfig6") {
		return "kreadconfig6"
	}
	return ""
}

// kreadKey runs `kreadconfig{5,6} --file kioslaverc --group "Proxy Settings" --key <k>`
// and returns the trimmed value. Missing keys return "" without an error.
func kreadKey(ctx context.Context, key string) string {
	bin := kreadconfigBinary()
	if bin == "" {
		return ""
	}
	out, err := exec.CommandContext(normalizeContext(ctx), bin, //nolint:gosec // bin is a fixed allowlist
		"--file", "kioslaverc",
		"--group", kdeProxyGroup,
		"--key", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getGlobalKDE returns the primary (http) proxy URL from KDE's kioslaverc, or
// ErrProxyNotSet when ProxyType=0 (none) or the value is empty. ProxyType=2
// (auto) returns the PAC script URL instead.
func getGlobalKDE(ctx context.Context) (string, error) {
	pt := kreadKey(ctx, "ProxyType")
	if pt == "" || pt == "0" {
		return "", ErrProxyNotSet
	}
	if pt == "2" {
		if url := kreadKey(ctx, "Proxy Config Script"); url != "" {
			return url, nil
		}
		return "", ErrProxyNotSet
	}
	if url := kreadKey(ctx, "httpProxy"); url != "" {
		return normalizeKDEProxyURL(url), nil
	}
	return "", ErrProxyNotSet
}

// getGlobalConfigKDE returns the full per-protocol config stored in kioslaverc.
func getGlobalConfigKDE(ctx context.Context) (ProxyConfig, error) {
	pt := kreadKey(ctx, "ProxyType")
	if pt == "" || pt == "0" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	if pt == "2" {
		pac := kreadKey(ctx, "Proxy Config Script")
		if pac == "" {
			return ProxyConfig{}, ErrProxyNotSet
		}
		return ProxyConfig{PAC: pac}, nil
	}

	var cfg ProxyConfig
	if u := kreadKey(ctx, "httpProxy"); u != "" {
		cfg.HTTP = normalizeKDEProxyURL(u)
	}
	if u := kreadKey(ctx, "httpsProxy"); u != "" {
		cfg.HTTPS = normalizeKDEProxyURL(u)
	}
	if u := kreadKey(ctx, "socksProxy"); u != "" {
		cfg.SOCKS = normalizeKDESocksURL(u)
	}
	if v := kreadKey(ctx, "NoProxyFor"); v != "" {
		cfg.NoProxy = v
	}
	if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	return cfg, nil
}

// normalizeKDEProxyURL turns a kioslaverc value into a real URL. KDE stores
// either "http://host:port" (what SetGlobal writes) or the legacy "host port"
// pair separated by a single space.
func normalizeKDEProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	if idx := strings.LastIndex(raw, " "); idx > 0 {
		return "http://" + raw[:idx] + ":" + raw[idx+1:]
	}
	return "http://" + raw
}

// normalizeKDESocksURL is like normalizeKDEProxyURL but forces the socks5
// scheme for values that lack one, matching what other backends produce.
func normalizeKDESocksURL(raw string) string {
	u := normalizeKDEProxyURL(raw)
	if strings.HasPrefix(u, "http://") {
		return "socks5://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
