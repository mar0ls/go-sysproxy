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
	"errors"
	"fmt"
	"strings"
	"time"
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

// String returns a human-readable name for the scope.
func (s ProxyScope) String() string {
	switch s {
	case ScopeShell:
		return "shell"
	case ScopeUser:
		return "user"
	case ScopeGlobal:
		return "global"
	default:
		return "unknown"
	}
}

// ProxyConfig holds per-protocol proxy URLs for SetMulti and the current
// system configuration returned by GetConfig. Any field left empty is ignored
// by SetMulti; empty fields returned by GetConfig indicate that protocol has
// no proxy configured. PAC is populated by GetConfig when the OS is in
// auto-proxy mode; SetMulti ignores it — use SetPAC to switch modes.
type ProxyConfig struct {
	HTTP    string `json:"http,omitempty"`
	HTTPS   string `json:"https,omitempty"`
	SOCKS   string `json:"socks,omitempty"`
	NoProxy string `json:"no_proxy,omitempty"` // comma-separated bypass list, e.g. "localhost,10.0.0.0/8"
	PAC     string `json:"pac,omitempty"`      // populated by GetConfig when auto-proxy is active
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
		logf("set proxy scope=shell url=%s", redactURL(proxyURL))
		return nil
	case ScopeUser:
		setEnvVars(proxyURL)
		err = setUser(proxyURL)
		logf("set proxy scope=user url=%s err=%v", redactURL(proxyURL), err)
		return err
	case ScopeGlobal:
		setEnvVars(proxyURL)
		err = activeBackend.SetGlobal(ctx, p)
		logf("set proxy scope=global url=%s err=%v", redactURL(proxyURL), err)
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
		err := activeBackend.UnsetGlobal(ctx)
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
	return activeBackend.GetGlobal(ctx)
}

// GetConfig returns the per-protocol proxy configuration currently active in
// the OS system settings. Fields are empty when that protocol has no proxy set.
// Only ScopeGlobal is supported; read os.Getenv for shell-scope values.
func GetConfig() (ProxyConfig, error) {
	return GetConfigContext(context.Background())
}

// GetConfigContext is like GetConfig but respects context cancellation.
func GetConfigContext(ctx context.Context) (ProxyConfig, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return ProxyConfig{}, err
	}
	return activeBackend.GetGlobalConfig(ctx)
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
	if err := validateNoProxy(cfg.NoProxy); err != nil {
		return err
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	switch scope {
	case ScopeShell:
		setEnvVarsMulti(cfg)
		logf("set multi scope=shell %s", describeMulti(cfg))
		return nil
	case ScopeUser:
		setEnvVarsMulti(cfg)
		err := setUserMulti(cfg)
		logf("set multi scope=user %s err=%v", describeMulti(cfg), err)
		return err
	case ScopeGlobal:
		setEnvVarsMulti(cfg)
		err := activeBackend.SetGlobalMulti(ctx, cfg)
		logf("set multi scope=global %s err=%v", describeMulti(cfg), err)
		return err
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
		logf("set pac scope=shell url=%s", redactURL(pacURL))
		return nil
	case ScopeUser:
		setEnvVarsPAC(pacURL)
		err := setUserPAC(pacURL)
		logf("set pac scope=user url=%s err=%v", redactURL(pacURL), err)
		return err
	case ScopeGlobal:
		setEnvVarsPAC(pacURL)
		err := activeBackend.SetGlobalPAC(ctx, pacURL)
		logf("set pac scope=global url=%s err=%v", redactURL(pacURL), err)
		return err
	default:
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
}

// WithProxy temporarily sets the proxy for the duration of fn, then puts the
// previous state back exactly: for ScopeGlobal every setting the OS stores
// (including fields that were empty), for ScopeUser the go-sysproxy block of
// the rc files / PowerShell profile, and for every scope the proxy environment
// variables of the process.
//
// If the previous state cannot be read, WithProxy returns an error before
// changing anything. If restoring fails, the error is returned (joined with
// fn's error, if any). Credentials of a previously configured authenticated
// proxy cannot be read back and are not restored.
//
//	err := sysproxy.WithProxy(ctx, "socks5://proxy:1080", sysproxy.ScopeGlobal, func(ctx context.Context) error {
//	    return doRequest(ctx)
//	})
func WithProxy(ctx context.Context, proxyURL string, scope ProxyScope, fn func(context.Context) error) error {
	if err := validateProxyURL(proxyURL); err != nil {
		return err
	}
	return withState(ctx, scope, fn, func(ctx context.Context) error {
		return SetContext(ctx, proxyURL, scope)
	})
}

// WithProxyMulti is the multi-protocol variant of WithProxy: it applies cfg
// via SetMulti and restores the previous state on return.
func WithProxyMulti(ctx context.Context, cfg ProxyConfig, scope ProxyScope, fn func(context.Context) error) error {
	return withState(ctx, scope, fn, func(ctx context.Context) error {
		return SetMultiContext(ctx, cfg, scope)
	})
}

// restoreTimeout bounds the restore step, which runs even when the caller's
// context is already canceled.
const restoreTimeout = 60 * time.Second

// withState snapshots the state apply is going to change, runs apply and fn,
// and restores the snapshot on the way out — also when apply fails half way
// or fn panics. Non-critical apply errors (e.g. /etc/environment not
// writable) do not stop fn.
func withState(ctx context.Context, scope ProxyScope, fn func(context.Context) error, apply func(context.Context) error) (err error) {
	ctx = normalizeContext(ctx)
	if scope < ScopeShell || scope > ScopeGlobal {
		return fmt.Errorf("sysproxy: invalid scope %d", scope)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	restore, err := snapshotState(ctx, scope)
	if err != nil {
		return fmt.Errorf("sysproxy: cannot save the current proxy state, nothing was changed: %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()
		if rerr := restore(rctx); rerr != nil {
			logf("WithProxy: restore failed: %v", rerr)
			err = combine(err, fmt.Errorf("sysproxy: restoring the previous proxy state failed: %w", rerr))
		}
	}()

	if aerr := apply(ctx); aerr != nil && !IsNonCritical(aerr) {
		return aerr
	}
	return fn(ctx)
}

// snapshotState captures what Set* of the given scope can change and returns
// the function that puts it back.
func snapshotState(ctx context.Context, scope ProxyScope) (func(context.Context) error, error) {
	env := snapshotEnv()
	var parts []func(context.Context) error

	switch scope {
	case ScopeUser:
		restoreUser, err := snapshotUser()
		if err != nil {
			return nil, err
		}
		parts = append(parts, func(context.Context) error { return restoreUser() })
	case ScopeGlobal:
		restoreGlobal, err := activeBackend.SnapshotGlobal(ctx)
		if err != nil {
			return nil, err
		}
		parts = append(parts, restoreGlobal)
	}
	parts = append(parts, func(context.Context) error { return env.restore() })

	return func(ctx context.Context) error {
		var errs []error
		for _, restore := range parts {
			if err := restore(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}, nil
}

// describeMulti renders a ProxyConfig for the log without credentials.
func describeMulti(cfg ProxyConfig) string {
	var parts []string
	for _, f := range []struct{ name, url string }{{"http", cfg.HTTP}, {"https", cfg.HTTPS}, {"socks", cfg.SOCKS}} {
		if f.url != "" {
			parts = append(parts, f.name+"="+redactURL(f.url))
		}
	}
	if cfg.NoProxy != "" {
		parts = append(parts, "no_proxy="+cfg.NoProxy)
	}
	return strings.Join(parts, " ")
}
