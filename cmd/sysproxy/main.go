package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	// shared flags
	scopeStr := "global"
	jsonOut := false
	timeoutStr := "5s"
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			i++
			if i >= len(args) {
				die("--scope requires a value: shell|user|global")
			}
			scopeStr = args[i]
		case "--json":
			jsonOut = true
		case "--timeout":
			i++
			if i >= len(args) {
				die("--timeout requires a duration value, e.g. 5s")
			}
			timeoutStr = args[i]
		default:
			positional = append(positional, args[i])
		}
	}

	scope, err := parseScope(scopeStr)
	if err != nil {
		die(err.Error())
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		die("invalid --timeout value: " + err.Error())
	}

	switch cmd {
	case "set":
		if len(positional) < 1 {
			die("usage: sysproxy set <url>")
		}
		cmdSet(positional[0], scope, jsonOut)
	case "get":
		cmdGet(jsonOut)
	case "unset":
		cmdUnset(scope, jsonOut)
	case "pac":
		if len(positional) < 1 {
			die("usage: sysproxy pac <url>")
		}
		cmdPAC(positional[0], scope, jsonOut)
	case "check":
		if len(positional) < 1 {
			die("usage: sysproxy check <url>")
		}
		cmdCheck(positional[0], timeout, jsonOut)
	case "version":
		fmt.Println(buildinfo.Summary())
	case "--help", "-h", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}

func cmdSet(proxyURL string, scope sysproxy.ProxyScope, jsonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sysproxy.SetContext(ctx, proxyURL, scope); err != nil {
		dieJSON(jsonOut, "set failed: "+err.Error())
	}
	printOK(jsonOut, map[string]any{"proxy": proxyURL, "scope": scope.String()})
}

func cmdGet(jsonOut bool) {
	url, err := sysproxy.Get()
	if err != nil {
		if jsonOut {
			printJSON(map[string]any{"error": "proxy not set"})
		} else {
			fmt.Fprintln(os.Stderr, "proxy not set")
		}
		os.Exit(2)
	}
	if jsonOut {
		printJSON(map[string]any{"proxy": url})
	} else {
		fmt.Println(url)
	}
}

func cmdUnset(scope sysproxy.ProxyScope, jsonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sysproxy.UnsetContext(ctx, scope); err != nil {
		dieJSON(jsonOut, "unset failed: "+err.Error())
	}
	printOK(jsonOut, map[string]any{"scope": scope.String()})
}

func cmdPAC(pacURL string, scope sysproxy.ProxyScope, jsonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sysproxy.SetPACContext(ctx, pacURL, scope); err != nil {
		dieJSON(jsonOut, "pac failed: "+err.Error())
	}
	printOK(jsonOut, map[string]any{"pac": pacURL, "scope": scope.String()})
}

func cmdCheck(proxyURL string, timeout time.Duration, jsonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := sysproxy.Check(ctx, proxyURL); err != nil {
		dieJSON(jsonOut, "unreachable: "+err.Error())
	}
	printOK(jsonOut, map[string]any{"proxy": proxyURL, "reachable": true})
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

func printOK(jsonOut bool, fields map[string]any) {
	if jsonOut {
		fields["ok"] = true
		printJSON(fields)
	} else {
		fmt.Println("ok")
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}

func dieJSON(jsonOut bool, msg string) {
	if jsonOut {
		printJSON(map[string]any{"ok": false, "error": msg})
	} else {
		fmt.Fprintln(os.Stderr, "error: "+msg)
	}
	os.Exit(1)
}
