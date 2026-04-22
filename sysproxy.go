// Package sysproxy provides cross-platform system proxy management.
//
// It supports setting, clearing, and querying the OS system proxy on
// macOS (networksetup), Linux (gsettings / kwriteconfig5, /etc/environment),
// and Windows (registry + Credential Manager).
//
// Quick start:
//
//	err := sysproxy.Set("http://proxy.example.com:8080", sysproxy.ScopeGlobal)
//	err  = sysproxy.Unset(sysproxy.ScopeGlobal)
//	err  = sysproxy.SetContext(ctx, "http://proxy.example.com:8080", sysproxy.ScopeGlobal)
//
//	// temporary proxy — auto-restored on return
//	err = sysproxy.WithProxy(ctx, "socks5://proxy.example.com:1080", sysproxy.ScopeGlobal, func(ctx context.Context) error {
//	    return doRequest(ctx)
//	})
package sysproxy

import (
	"context"
	"fmt"
)

// ProxyScope defines the lifetime and reach of a proxy setting.
type ProxyScope int

const (
	// ScopeShell sets proxy only for the current process via env vars.
	ScopeShell ProxyScope = iota
	// ScopeUser persists proxy for the current user (rc files on Unix, PS profile on Windows).
	ScopeUser
	// ScopeGlobal sets the system-wide proxy (requires elevated privileges on most platforms).
	ScopeGlobal
)

// ProxyConfig allows configuring a different proxy URL per protocol.
type ProxyConfig struct {
	HTTP    string
	HTTPS   string
	SOCKS   string
	NoProxy string // comma-separated bypass list, e.g. "localhost,10.0.0.0/8"
}

// Set configures the OS system proxy to proxyURL for the given scope.
//
//	err := sysproxy.Set("http://user:pass@proxy.example.com:8080", sysproxy.ScopeGlobal)
func Set(proxyURL string, scope ProxyScope) error {
	return SetContext(context.Background(), proxyURL, scope)
}

// SetContext configures the OS system proxy to proxyURL for the given scope,
// aborting before side effects if ctx is already canceled.
func SetContext(ctx context.Context, proxyURL string, scope ProxyScope) error {
	if err := validateProxyURL(proxyURL); err != nil {
		return err
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := parse(proxyURL)
	if err != nil {
		return err
	}
	switch scope {
	case ScopeShell:
		setEnvVars(proxyURL)
		logf("set proxy scope=shell url=%s", proxyURL)
		return nil
	case ScopeUser:
		setEnvVars(proxyURL)
		err = setUser(proxyURL)
		logf("set proxy scope=user url=%s err=%v", proxyURL, err)
		return err
	case ScopeGlobal:
		setEnvVars(proxyURL)
		err = setGlobal(ctx, p)
		logf("set proxy scope=global url=%s err=%v", proxyURL, err)
		return err
	default:
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
}

// Unset clears the OS system proxy for the given scope.
//
//	err := sysproxy.Unset(sysproxy.ScopeGlobal)
func Unset(scope ProxyScope) error {
	return UnsetContext(context.Background(), scope)
}

// UnsetContext clears the OS system proxy for the given scope, aborting before
// side effects if ctx is already canceled.
func UnsetContext(ctx context.Context, scope ProxyScope) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	switch scope {
	case ScopeShell:
		unsetEnvVars()
		logf("unset proxy scope=shell")
		return nil
	case ScopeUser:
		unsetEnvVars()
		err := unsetUser()
		logf("unset proxy scope=user err=%v", err)
		return err
	case ScopeGlobal:
		unsetEnvVars()
		err := unsetGlobal(ctx)
		logf("unset proxy scope=global err=%v", err)
		return err
	default:
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
}

// Get returns the current system proxy URL, or an error if none is configured.
func Get() (string, error) {
	return GetContext(context.Background())
}

// GetContext returns the current system proxy URL, or an error if none is
// configured.
func GetContext(ctx context.Context) (string, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return getGlobal(ctx)
}

// SetMulti configures per-protocol proxies. Any field left empty is not changed.
func SetMulti(cfg ProxyConfig, scope ProxyScope) error {
	return SetMultiContext(context.Background(), cfg, scope)
}

// SetMultiContext configures per-protocol proxies. Any field left empty is not
// changed.
func SetMultiContext(ctx context.Context, cfg ProxyConfig, scope ProxyScope) error {
	for _, u := range []string{cfg.HTTP, cfg.HTTPS, cfg.SOCKS} {
		if u != "" {
			if err := validateProxyURL(u); err != nil {
				return err
			}
		}
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	switch scope {
	case ScopeShell:
		setEnvVarsMulti(cfg)
		return nil
	case ScopeUser:
		setEnvVarsMulti(cfg)
		return setUserMulti(cfg)
	case ScopeGlobal:
		setEnvVarsMulti(cfg)
		return setGlobalMulti(ctx, cfg)
	default:
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
}

// SetPAC configures the OS system proxy to use a Proxy Auto-Config (PAC) URL.
// pacURL must start with http://, https://, or file://.
func SetPAC(pacURL string, scope ProxyScope) error {
	return SetPACContext(context.Background(), pacURL, scope)
}

// SetPACContext configures the OS system proxy to use a Proxy Auto-Config
// (PAC) URL. pacURL must start with http://, https://, or file://.
func SetPACContext(ctx context.Context, pacURL string, scope ProxyScope) error {
	if err := validatePACURL(pacURL); err != nil {
		return err
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	switch scope {
	case ScopeShell:
		setEnvVarsPAC(pacURL)
		return nil
	case ScopeUser:
		setEnvVarsPAC(pacURL)
		return setUserPAC(pacURL)
	case ScopeGlobal:
		setEnvVarsPAC(pacURL)
		return setGlobalPAC(ctx, pacURL)
	default:
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
}

// WithProxy temporarily sets the proxy for the duration of fn, then restores
// the previous proxy state (or clears it if no proxy was set before).
//
//	err := sysproxy.WithProxy(ctx, "socks5://proxy:1080", sysproxy.ScopeGlobal, func(ctx context.Context) error {
//	    return doRequest(ctx)
//	})
func WithProxy(ctx context.Context, proxyURL string, scope ProxyScope, fn func(context.Context) error) error {
	ctx = normalizeContext(ctx)
	prev, prevErr := Get()
	if err := SetContext(ctx, proxyURL, scope); err != nil {
		return err
	}
	defer func() {
		if prevErr == nil && prev != "" {
			_ = Set(prev, scope)
		} else {
			_ = Unset(scope)
		}
	}()
	return fn(ctx)
}
