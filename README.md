# go-sysproxy

[![Go Reference](https://pkg.go.dev/badge/github.com/mar0ls/go-sysproxy.svg)](https://pkg.go.dev/github.com/mar0ls/go-sysproxy)
[![CI](https://github.com/mar0ls/go-sysproxy/actions/workflows/test.yml/badge.svg)](https://github.com/mar0ls/go-sysproxy/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/mar0ls/go-sysproxy/graph/badge.svg)](https://codecov.io/gh/mar0ls/go-sysproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/mar0ls/go-sysproxy)](https://goreportcard.com/report/github.com/mar0ls/go-sysproxy)

Cross-platform system proxy management for Go — set, clear, and query the OS proxy from your application without shell scripts.

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := sysproxy.SetContext(ctx, "socks5://user:pass@proxy.example.com:1080", sysproxy.ScopeGlobal); err != nil {
    log.Fatal(err)
}
defer sysproxy.UnsetContext(ctx, sysproxy.ScopeGlobal)
```

## Why

Proxy-switching tools, VPN clients, and network-aware CLIs built in Go often need to set the OS system proxy — not just read it. The existing options are either buried inside a large SDK ([outline-sdk/x/sysproxy](https://pkg.go.dev/github.com/Jigsaw-Code/outline-sdk/x/sysproxy)), Windows-only, or rely on shipping pre-built binaries.

`go-sysproxy` wraps the native proxy tools on macOS (`networksetup`), Linux (GNOME + KDE + `/etc/environment`), and Windows (registry + Credential Manager). It covers system proxy changes, health checks, per-app config, and temporary proxy restore. Zero external dependencies.

## Installation

```sh
go get github.com/mar0ls/go-sysproxy
```

Requires Go 1.22+. Zero external dependencies.

## Quick start

```go
package main

import (
    "context"
    "log"
    "time"

    sysproxy "github.com/mar0ls/go-sysproxy"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Verify the proxy is reachable before committing
    if err := sysproxy.Check(ctx, "http://proxy.example.com:8080"); err != nil {
        log.Fatal(err)
    }

    // Apply the system proxy and restore it on exit
    if err := sysproxy.SetContext(ctx, "http://proxy.example.com:8080", sysproxy.ScopeGlobal); err != nil {
        log.Fatal(err)
    }
    defer sysproxy.UnsetContext(ctx, sysproxy.ScopeGlobal)
}
```

## Scope

| Scope | Effect |
|---|---|
| `ScopeShell` | Current process only — sets env vars (`http_proxy`, `HTTPS_PROXY`, …) |
| `ScopeUser` | Persisted for the user — rc files on Unix, PowerShell profile on Windows |
| `ScopeGlobal` | System-wide — `networksetup`, `gsettings`, registry |

## API

### Set / Unset / Get

```go
err := sysproxy.Set("http://user:pass@proxy.example.com:8080", sysproxy.ScopeGlobal)
err  = sysproxy.Unset(sysproxy.ScopeGlobal)

url, err := sysproxy.Get() // reads current system proxy (HTTP field)
```

The plain wrappers exist for convenience. Use the context-aware variants for cancellation and deadlines:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

err := sysproxy.SetContext(ctx, "http://user:pass@proxy.example.com:8080", sysproxy.ScopeGlobal)
err  = sysproxy.UnsetContext(ctx, sysproxy.ScopeGlobal)

url, err := sysproxy.GetContext(ctx)
```

### GetConfig — read per-protocol settings

`GetConfig` returns the full proxy configuration currently active in the OS, with each protocol field populated separately:

```go
cfg, err := sysproxy.GetConfig()
// cfg.HTTP    = "http://proxy.example.com:8080"
// cfg.HTTPS   = "http://proxy.example.com:8080"
// cfg.SOCKS   = "socks5://proxy.example.com:1080"
// cfg.NoProxy = "localhost,10.0.0.0/8"
// cfg.PAC     = ""  // populated when auto-proxy (PAC) is active instead of a manual proxy
```

`GetConfigContext` is also available.

### Per-protocol proxy

```go
err := sysproxy.SetMulti(sysproxy.ProxyConfig{
    HTTP:    "http://http-proxy.example.com:8080",
    HTTPS:   "http://https-proxy.example.com:8080",
    SOCKS:   "socks5://socks-proxy.example.com:1080",
    NoProxy: "localhost,10.0.0.0/8",
}, sysproxy.ScopeGlobal)
```

`SetMultiContext` is also available when you want the same API with cancellation support.

### PAC file

```go
err := sysproxy.SetPAC("https://config.example.com/proxy.pac", sysproxy.ScopeGlobal)
```

`SetPACContext` is also available for deadline-aware callers.

Note: `SetPAC` switches the OS into auto-proxy (PAC) mode. In that mode,
`Get` / `GetConfig` report manual proxy state, so they may return
"proxy not set" / "proxy not enabled" even though PAC is active.

### Temporary proxy

`WithProxy` sets the proxy for the duration of `fn` and restores the previous state on return — even if `fn` returns an error. The snapshot covers the full `ProxyConfig` (HTTP + HTTPS + SOCKS + NoProxy + PAC), not just the HTTP field.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

err := sysproxy.WithProxy(ctx, "socks5://proxy.example.com:1080", sysproxy.ScopeGlobal,
    func(ctx context.Context) error {
        return doSensitiveRequest(ctx)
    },
)
```

`WithProxyMulti` is the multi-protocol variant:

```go
err := sysproxy.WithProxyMulti(ctx, sysproxy.ProxyConfig{
    HTTP:  "http://http-proxy.example.com:8080",
    SOCKS: "socks5://socks-proxy.example.com:1080",
}, sysproxy.ScopeGlobal, func(ctx context.Context) error {
    return doWork(ctx)
})
```

### Error classification

The package exposes sentinel errors so callers can branch precisely with `errors.Is`:

```go
_, err := sysproxy.GetConfig()
switch {
case errors.Is(err, sysproxy.ErrProxyNotSet):
    // no manual proxy configured
case errors.Is(err, sysproxy.ErrProxyNotEnabled):
    // proxy entry exists but is disabled (e.g. Windows ProxyEnable=0)
case errors.Is(err, sysproxy.ErrUnsupportedPlatform):
    // this GOOS has no sysproxy backend compiled in
}

// non-critical failures (e.g. /etc/environment without root):
if err := sysproxy.Set(url, sysproxy.ScopeGlobal); err != nil {
    if sysproxy.RequiresElevation(err) {
        log.Println("sudo required for system-wide effect; per-desktop settings still applied")
    } else if sysproxy.IsNonCritical(err) {
        log.Println("partial success:", err)
    } else {
        return err
    }
}
```

### Health check

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := sysproxy.Check(ctx, "http://proxy.example.com:8080"); err != nil {
    log.Printf("proxy unreachable: %v", err)
}
```

### App config

Write proxy settings to tool-specific config files independently of the OS-level proxy.

```go
sysproxy.WriteAppConfig(sysproxy.AppGit,  "http://proxy.example.com:8080") // git config --global
sysproxy.WriteAppConfig(sysproxy.AppCurl, "http://proxy.example.com:8080") // ~/.curlrc
sysproxy.WriteAppConfig(sysproxy.AppNPM,  "http://proxy.example.com:8080") // npm config set
sysproxy.WriteAppConfig(sysproxy.AppPip,  "http://proxy.example.com:8080") // ~/.config/pip/pip.conf
sysproxy.WriteAppConfig(sysproxy.AppWget, "http://proxy.example.com:8080") // ~/.wgetrc

sysproxy.ClearAppConfig(sysproxy.AppGit)
```

`WriteAppConfigContext` and `ClearAppConfigContext` are available for `git` and `npm`, where configuration is applied through external commands.

### Logging / auditing

```go
type slogAdapter struct{ l *slog.Logger }
func (a slogAdapter) Log(msg string) { a.l.Info(msg) }

sysproxy.SetLogger(slogAdapter{slog.Default()})
// every Set/Unset/WriteAppConfig now emits a structured log line
```

## CLI

A standalone binary is available at `cmd/sysproxy`:

```sh
# build
make cli          # → dist/sysproxy

# or install directly
go install github.com/mar0ls/go-sysproxy/cmd/sysproxy@latest
```

```sh
sysproxy set http://127.0.0.1:8080
sysproxy set http://user:pass@proxy.corp.com:8080 --scope global
sysproxy get
sysproxy get --json
sysproxy unset
sysproxy pac https://config.example.com/proxy.pac
sysproxy check http://proxy.corp.com:8080 --timeout 10s
sysproxy version
```

Exit codes: `0` success · `1` error · `2` proxy not set (only `get`).

## Real-world example — residential proxy

The URL format works with any standard HTTP/SOCKS5 proxy provider:

```go
// SOCKS5 residential proxy (e.g. IPRoyal, Bright Data, Oxylabs)
err := sysproxy.Set(
    "socks5://username:password@geo.iproyal.com:32325",
    sysproxy.ScopeGlobal,
)

// HTTP proxy with authentication
err = sysproxy.Set(
    "http://username:password@proxy.provider.com:10000",
    sysproxy.ScopeGlobal,
)

// Also configure developer tools to use the same proxy
_ = sysproxy.WriteAppConfig(sysproxy.AppGit,  "http://username:password@proxy.provider.com:10000")
_ = sysproxy.WriteAppConfig(sysproxy.AppCurl, "http://username:password@proxy.provider.com:10000")
```

> Credentials in proxy URLs are handled by the OS — on Windows they are stored in Credential Manager, not in plaintext.

## Notes

- `Check` verifies TCP reachability of the proxy endpoint. It does not validate credentials or perform a protocol-level handshake.
- Context-aware APIs abort before starting side effects when the context is already canceled, and command-backed operations use `exec.CommandContext`.
- `ScopeGlobal` may still require elevated permissions depending on the platform and the target settings store.

## Platform support

| Feature | macOS | Linux (GNOME) | Linux (KDE) | Windows |
|---|:---:|:---:|:---:|:---:|
| Set / Unset | ✓ | ✓ | ✓ | ✓ |
| Get | ✓ | ✓ | ✓ | ✓ |
| GetConfig | ✓ | ✓ | ✓ | ✓ |
| GetConfig (PAC field) | ✓ | ✓ | ✓ | ✓ |
| SetMulti | ✓ | ✓ | ✓ | ✓ |
| SetPAC | ✓ | ✓ | ✓ | ✓ |
| ScopeUser (rc files) | ✓ | ✓ | ✓ | ✓ |
| Credential Manager | — | — | — | ✓ |

> **Linux:** `ScopeGlobal` writes `/etc/environment` (requires root) and calls `gsettings` **and** `kwriteconfig5` if available, so hybrid GNOME/KDE setups are covered without desktop detection. Failure to write `/etc/environment` is returned as a non-critical error — use `sysproxy.IsNonCritical(err)` to distinguish it from a hard failure.

## Comparison with alternatives

The table below compares `go-sysproxy` with other Go proxy-management libraries by API surface and behavior.

| | `mar0ls/go-sysproxy` | [`Jigsaw-Code/outline-sdk/x/sysproxy`](https://pkg.go.dev/github.com/Jigsaw-Code/outline-sdk/x/sysproxy) |
|---|:---:|:---:|
| macOS (`networksetup`) | ✓ | ✓ |
| Linux GNOME (`gsettings`) | ✓ | ✓ |
| Linux KDE (`kwriteconfig5`) | ✓ | — |
| Windows (registry + `cmdkey`) | ✓ | ✓ |
| `Get` / `GetConfig` per protocol | ✓ | partial |
| `SetMulti` (HTTP/HTTPS/SOCKS + NoProxy) | ✓ | — |
| `SetPAC` | ✓ | — |
| `Check` (TCP reachability) | ✓ | — |
| `WithProxy` (temporary, auto-restore) | ✓ | — |
| `WriteAppConfig` (rc files for git/npm/pip/…) | ✓ | — |
| Context-aware API (`*Context`) | ✓ | partial |
| Standalone module, no SDK to import | ✓ | — (part of outline-sdk) |
| Zero external dependencies | ✓ | — |
| CLI (`sysproxy` binary, `--json`) | ✓ | — |

## Security

- **Command allowlist** — `exec.Command` is restricted to a fixed set of permitted binaries. No user-supplied input reaches the shell.
- **Config files** — written with mode `0600`.
- **Static analysis** — code is checked with [golangci-lint](https://golangci-lint.run) (including `gosec`, `errcheck`, `staticcheck`) on every push.

## License

MIT
