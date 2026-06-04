package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	sysproxy "github.com/mar0ls/go-sysproxy"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
	})

	fn()
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

func TestParseScope(t *testing.T) {
	tests := []struct {
		in      string
		want    sysproxy.ProxyScope
		wantErr bool
	}{
		{in: "shell", want: sysproxy.ScopeShell},
		{in: "user", want: sysproxy.ScopeUser},
		{in: "global", want: sysproxy.ScopeGlobal},
		{in: "bad", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parseScope(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseScope(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseScope(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseScope(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestPrintJSON(t *testing.T) {
	out := captureStdout(t, func() {
		printJSON(os.Stdout, map[string]any{"ok": true})
	})

	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if got, ok := v["ok"].(bool); !ok || !got {
		t.Fatalf("unexpected JSON content: %v", v)
	}
}

func TestPrintOKTextAndJSON(t *testing.T) {
	outText := captureStdout(t, func() {
		printOK(false, map[string]any{"x": 1}, os.Stdout)
	})
	if strings.TrimSpace(outText) != "ok" {
		t.Fatalf("printOK text output = %q, want %q", strings.TrimSpace(outText), "ok")
	}

	outJSON := captureStdout(t, func() {
		printOK(true, map[string]any{"scope": "global"}, os.Stdout)
	})
	var v map[string]any
	if err := json.Unmarshal([]byte(outJSON), &v); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if got, ok := v["ok"].(bool); !ok || !got {
		t.Fatalf("expected ok=true in JSON, got %v", v)
	}
	if v["scope"] != "global" {
		t.Fatalf("expected scope=global in JSON, got %v", v)
	}
}

// TestRunUsageAndErrors exercises the argument-parsing and dispatch branches of
// run that return before touching any OS proxy setting.
func TestRunUsageAndErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		inStdout string
		inStderr string
	}{
		{name: "no args", args: nil, wantCode: 1, inStderr: "sysproxy"},
		{name: "help", args: []string{"help"}, wantCode: 0, inStdout: "Usage"},
		{name: "help long", args: []string{"--help"}, wantCode: 0, inStdout: "Usage"},
		{name: "help short", args: []string{"-h"}, wantCode: 0, inStdout: "Usage"},
		{name: "version", args: []string{"version"}, wantCode: 0, inStdout: "commit"},
		{name: "unknown command", args: []string{"frobnicate"}, wantCode: 1, inStderr: "frobnicate"},
		{name: "set missing url", args: []string{"set"}, wantCode: 1, inStderr: "usage: sysproxy set"},
		{name: "pac missing url", args: []string{"pac"}, wantCode: 1, inStderr: "usage: sysproxy pac"},
		{name: "check missing url", args: []string{"check"}, wantCode: 1, inStderr: "usage: sysproxy check"},
		{name: "invalid scope", args: []string{"set", "http://127.0.0.1:8080", "--scope", "bogus"}, wantCode: 1, inStderr: "invalid scope"},
		{name: "invalid timeout", args: []string{"check", "http://127.0.0.1:1", "--timeout", "nope"}, wantCode: 1, inStderr: "invalid --timeout"},
		{name: "scope without value", args: []string{"set", "http://127.0.0.1:8080", "--scope"}, wantCode: 1, inStderr: "--scope requires"},
		{name: "timeout without value", args: []string{"check", "http://127.0.0.1:1", "--timeout"}, wantCode: 1, inStderr: "--timeout requires"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("run(%v) = %d, want %d (stderr=%q)", tt.args, code, tt.wantCode, stderr.String())
			}
			if tt.inStdout != "" && !strings.Contains(stdout.String(), tt.inStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.inStdout)
			}
			if tt.inStderr != "" && !strings.Contains(stderr.String(), tt.inStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.inStderr)
			}
		})
	}
}

// TestRunCheckUnreachable drives the check command through run for an address
// that cannot be reached, covering cmdCheck's error path without changing any
// OS proxy state.
func TestRunCheckUnreachable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "http://192.0.2.1:9999", "--timeout", "300ms"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for unreachable proxy, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unreachable") {
		t.Errorf("expected 'unreachable' on stderr, got %q", stderr.String())
	}
}

// TestRunCheckUnreachableJSON covers the JSON error branch of cmdCheck via run.
func TestRunCheckUnreachableJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"check", "http://192.0.2.1:9999", "--timeout", "300ms", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %q", err, stdout.String())
	}
	if _, ok := m["error"]; !ok {
		t.Errorf("expected 'error' key in JSON, got %v", m)
	}
}
