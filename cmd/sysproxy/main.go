package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
  0  success
  1  error
  2  proxy not set (get)

Examples:
  sysproxy set http://127.0.0.1:8080
  sysproxy set http://user:pass@proxy.corp.com:8080 --scope global
  sysproxy get --json
  sysproxy unset
  sysproxy pac https://config.example.com/proxy.pac
  sysproxy check http://proxy.corp.com:8080 --timeout 10s
`

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

	if err := sysproxy.SetContext(ctx, proxyURL, scope); err != nil {
		return dieJSON(jsonOut, "set failed: "+err.Error(), stdout, stderr)
	}
	printOK(jsonOut, map[string]any{"proxy": proxyURL, "scope": scope.String()}, stdout)
	return 0
}

func cmdGet(jsonOut bool, stdout, stderr io.Writer) int {
	if jsonOut {
		cfg, err := sysproxy.GetConfig()
		if err != nil {
			printJSON(stdout, map[string]any{"error": err.Error()})
			return 2
		}
		if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" {
			printJSON(stdout, map[string]any{"error": "proxy not set"})
			return 2
		}
		printJSON(stdout, cfg)
		return 0
	}
	url, err := sysproxy.Get()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "proxy not set")
		return 2
	}
	_, _ = fmt.Fprintln(stdout, url)
	return 0
}

func cmdUnset(scope sysproxy.ProxyScope, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sysproxy.UnsetContext(ctx, scope); err != nil {
		return dieJSON(jsonOut, "unset failed: "+err.Error(), stdout, stderr)
	}
	printOK(jsonOut, map[string]any{"scope": scope.String()}, stdout)
	return 0
}

func cmdPAC(pacURL string, scope sysproxy.ProxyScope, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sysproxy.SetPACContext(ctx, pacURL, scope); err != nil {
		return dieJSON(jsonOut, "pac failed: "+err.Error(), stdout, stderr)
	}
	printOK(jsonOut, map[string]any{"pac": pacURL, "scope": scope.String()}, stdout)
	return 0
}

func cmdCheck(proxyURL string, timeout time.Duration, jsonOut bool, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := sysproxy.Check(ctx, proxyURL); err != nil {
		return dieJSON(jsonOut, "unreachable: "+err.Error(), stdout, stderr)
	}
	printOK(jsonOut, map[string]any{"proxy": proxyURL, "reachable": true}, stdout)
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
