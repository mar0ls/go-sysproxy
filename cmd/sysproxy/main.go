package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	sysproxy "github.com/mar0ls/go-sysproxy"
	"github.com/mar0ls/go-sysproxy/internal/buildinfo"
)

const usage = `sysproxy — cross-platform system proxy manager

Usage:
  sysproxy <command> [flags]

Commands:
  set   <url>    Set system proxy  (e.g. http://127.0.0.1:8080)
  get            Print current system proxy URL
  unset          Clear system proxy
  pac   <url>    Set PAC (Proxy Auto-Config) URL
  check <url>    Test TCP reachability of a proxy endpoint
  version        Show build information

Flags:
  --scope  shell|user|global   Scope for set/unset/pac (default: global)
  --json                       Output in JSON format
  --timeout  duration          Timeout for check (default: 5s)

Exit codes:
  0  success (also when only a non-critical step failed, e.g. /etc/environment
     without root: the warning goes to stderr, or to "warning" in --json)
  1  error
  2  proxy not set (get); plain get also exits 2 when only a PAC URL is active
     (get --json prints it and exits 0)

Examples:
  sysproxy set http://127.0.0.1:8080
  sysproxy set http://user:pass@proxy.corp.com:8080 --scope global
  sysproxy get --json
  sysproxy unset
  sysproxy pac https://config.example.com/proxy.pac
  sysproxy check http://proxy.corp.com:8080 --timeout 10s
`

// The library entry points the commands call. Tests replace them so that the
// exit-code logic can be exercised without touching the OS proxy settings.
var (
	setProxy    = sysproxy.SetContext
	unsetProxy  = sysproxy.UnsetContext
	setPAC      = sysproxy.SetPACContext
	getProxy    = sysproxy.GetContext
	getProxyCfg = sysproxy.GetConfigContext

	isNonCritical     = sysproxy.IsNonCritical
	requiresElevation = sysproxy.RequiresElevation
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses argv and dispatches to a command, returning the process exit code.
// It is separated from main so it can be exercised in unit tests without
// spawning a subprocess or calling os.Exit.
func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 1 {
		_, _ = fmt.Fprint(stderr, usage)
		return 1
	}

	cmd := argv[0]
	args := argv[1:]

	// shared flags
	scopeStr := "global"
	jsonOut := false
	timeoutStr := "5s"
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			if i+1 >= len(args) {
				return die(stderr, "--scope requires a value: shell|user|global")
			}
			scopeStr = args[i+1] //nolint:gosec // bounds checked on the line above
			i++
		case "--json":
			jsonOut = true
		case "--timeout":
			if i+1 >= len(args) {
				return die(stderr, "--timeout requires a duration value, e.g. 5s")
			}
			timeoutStr = args[i+1] //nolint:gosec // bounds checked on the line above
			i++
		default:
			positional = append(positional, args[i])
		}
	}

	scope, err := parseScope(scopeStr)
	if err != nil {
		return die(stderr, err.Error())
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return die(stderr, "invalid --timeout value: "+err.Error())
	}

	switch cmd {
	case "set":
		if len(positional) < 1 {
			return die(stderr, "usage: sysproxy set <url>")
		}
		return cmdSet(positional[0], scope, jsonOut, stdout, stderr)
	case "get":
		return cmdGet(jsonOut, stdout, stderr)
	case "unset":
		return cmdUnset(scope, jsonOut, stdout, stderr)
	case "pac":
		if len(positional) < 1 {
			return die(stderr, "usage: sysproxy pac <url>")
		}
		return cmdPAC(positional[0], scope, jsonOut, stdout, stderr)
	case "check":
		if len(positional) < 1 {
			return die(stderr, "usage: sysproxy check <url>")
		}
		return cmdCheck(positional[0], timeout, jsonOut, stdout, stderr)
	case "version":
		_, _ = fmt.Fprintln(stdout, buildinfo.Summary())
		return 0
	case "--help", "-h", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %q\n\n", cmd)
		_, _ = fmt.Fprint(stderr, usage)
		return 1
	}
}

func cmdSet(proxyURL string, scope sysproxy.ProxyScope, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := setProxy(ctx, proxyURL, scope)
	return finish("set", err, jsonOut, map[string]any{"proxy": redactProxy(proxyURL), "scope": scope.String()}, stdout, stderr)
}

// isNotSet reports whether err means "no proxy configured" rather than a failure
// (Windows reports a disabled proxy as ErrProxyNotEnabled).
func isNotSet(err error) bool {
	return errors.Is(err, sysproxy.ErrProxyNotSet) || errors.Is(err, sysproxy.ErrProxyNotEnabled)
}

func cmdGet(jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if jsonOut {
		cfg, err := getProxyCfg(ctx)
		switch {
		case err != nil && !isNotSet(err):
			printJSON(stdout, map[string]any{"ok": false, "error": err.Error()})
			return 1
		case err != nil || cfg == (sysproxy.ProxyConfig{}):
			printJSON(stdout, map[string]any{"error": "proxy not set"})
			return 2
		}
		// A PAC URL alone counts as configured: GetConfig reports it in cfg.PAC.
		// Some backends (KDE) store the login in the URL; never print it.
		cfg.HTTP, cfg.HTTPS, cfg.SOCKS, cfg.PAC = redactProxy(cfg.HTTP), redactProxy(cfg.HTTPS), redactProxy(cfg.SOCKS), redactProxy(cfg.PAC)
		printJSON(stdout, cfg)
		return 0
	}

	url, err := getProxy(ctx)
	switch {
	case err == nil:
		_, _ = fmt.Fprintln(stdout, redactProxy(url))
		return 0
	case !isNotSet(err):
		_, _ = fmt.Fprintln(stderr, "error: get failed: "+err.Error())
		return 1
	}
	// No manual proxy. Tell the user when a PAC URL is active instead, so that
	// "not set" is not mistaken for "nothing configured".
	if cfg, cerr := getProxyCfg(ctx); cerr == nil && cfg.PAC != "" {
		_, _ = fmt.Fprintln(stderr, "proxy not set (PAC active: "+cfg.PAC+"; see 'sysproxy get --json')")
	} else {
		_, _ = fmt.Fprintln(stderr, "proxy not set")
	}
	return 2
}

func cmdUnset(scope sysproxy.ProxyScope, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := unsetProxy(ctx, scope)
	return finish("unset", err, jsonOut, map[string]any{"scope": scope.String()}, stdout, stderr)
}

func cmdPAC(pacURL string, scope sysproxy.ProxyScope, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := setPAC(ctx, pacURL, scope)
	return finish("pac", err, jsonOut, map[string]any{"pac": pacURL, "scope": scope.String()}, stdout, stderr)
}

// finish reports the result of a set/unset/pac call. A non-critical error means
// the requested change was applied and only an optional step failed (for
// example /etc/environment without root), so it is a warning with exit code 0;
// anything else is a failure.
func finish(what string, err error, jsonOut bool, fields map[string]any, stdout, stderr io.Writer) int {
	if err == nil {
		printOK(jsonOut, fields, stdout)
		return 0
	}
	if !isNonCritical(err) {
		return dieJSON(jsonOut, what+" failed: "+err.Error(), stdout, stderr)
	}
	if jsonOut {
		fields["warning"] = err.Error()
		if requiresElevation(err) {
			fields["requires_elevation"] = true
		}
	} else {
		_, _ = fmt.Fprintln(stderr, "warning: "+err.Error())
	}
	printOK(jsonOut, fields, stdout)
	return 0
}

func cmdCheck(proxyURL string, timeout time.Duration, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := sysproxy.Check(ctx, proxyURL); err != nil {
		return dieJSON(jsonOut, "unreachable: "+err.Error(), stdout, stderr)
	}
	printOK(jsonOut, map[string]any{"proxy": redactProxy(proxyURL), "reachable": true}, stdout)
	return 0
}

func parseScope(s string) (sysproxy.ProxyScope, error) {
	switch s {
	case "shell":
		return sysproxy.ScopeShell, nil
	case "user":
		return sysproxy.ScopeUser, nil
	case "global":
		return sysproxy.ScopeGlobal, nil
	default:
		return 0, fmt.Errorf("invalid scope %q — use shell, user or global", s)
	}
}

// redactProxy masks the password of a proxy URL before it is echoed to the
// terminal or a log pipeline.
func redactProxy(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid URL>"
	}
	return u.Redacted()
}

func printOK(jsonOut bool, fields map[string]any, stdout io.Writer) {
	if jsonOut {
		fields["ok"] = true
		printJSON(stdout, fields)
	} else {
		_, _ = fmt.Fprintln(stdout, "ok")
	}
}

func printJSON(stdout io.Writer, v any) {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func die(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintln(stderr, "error: "+msg)
	return 1
}

func dieJSON(jsonOut bool, msg string, stdout, stderr io.Writer) int {
	if jsonOut {
		printJSON(stdout, map[string]any{"ok": false, "error": msg})
	} else {
		_, _ = fmt.Fprintln(stderr, "error: "+msg)
	}
	return 1
}
