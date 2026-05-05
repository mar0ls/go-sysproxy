package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// testBinary returns the path to a freshly built sysproxy binary.
// The binary is built once per test run and removed when the test suite exits.
func testBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/sysproxy"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build sysproxy: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if ex, ok := err.(*exec.ExitError); ok {
			exitCode = ex.ExitCode()
		} else {
			t.Fatalf("exec error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestVersion checks that the version command prints something sensible.
func TestVersion(t *testing.T) {
	bin := testBinary(t)
	out, _, code := run(t, bin, "version")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "dev") && !strings.Contains(out, ".") {
		t.Errorf("unexpected version output: %q", out)
	}
}

// TestHelp checks that the help text is printed and exit 0.
func TestHelp(t *testing.T) {
	bin := testBinary(t)
	for _, arg := range []string{"help", "--help", "-h"} {
		out, _, code := run(t, bin, arg)
		if code != 0 {
			t.Errorf("%s: exit code %d", arg, code)
		}
		if !strings.Contains(out, "sysproxy") {
			t.Errorf("%s: expected usage text, got %q", arg, out)
		}
	}
}

// TestNoArgs checks that running without arguments exits 1 and prints usage.
func TestNoArgs(t *testing.T) {
	bin := testBinary(t)
	_, stderr, code := run(t, bin)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "sysproxy") {
		t.Errorf("expected usage on stderr, got %q", stderr)
	}
}

// TestUnknownCommand exits 1 and prints something useful.
func TestUnknownCommand(t *testing.T) {
	bin := testBinary(t)
	_, stderr, code := run(t, bin, "foobar")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "foobar") {
		t.Errorf("expected command name in error, got %q", stderr)
	}
}

// TestVersionJSON checks --json flag on version (unsupported; just ensure no panic).
func TestGetNotSet_ExitCode2(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("SYSPROXY_INTEGRATION") == "" {
		t.Skip("skipping get test outside CI/SYSPROXY_INTEGRATION to avoid touching OS settings")
	}
	bin := testBinary(t)
	_, _, code := run(t, bin, "get")
	// exit 2 = not set, 0 = set — both are valid outcomes
	if code != 0 && code != 2 {
		t.Fatalf("expected exit 0 or 2, got %d", code)
	}
}

// TestGetJSON_OutputShape verifies the JSON output is valid JSON with expected key.
func TestGetJSON_OutputShape(t *testing.T) {
	if os.Getenv("CI") == "" && os.Getenv("SYSPROXY_INTEGRATION") == "" {
		t.Skip("skipping get test outside CI/SYSPROXY_INTEGRATION to avoid touching OS settings")
	}
	bin := testBinary(t)
	out, stderr, code := run(t, bin, "get", "--json")
	if code != 0 && code != 2 {
		t.Fatalf("unexpected exit %d: %s", code, stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %q", err, out)
	}
	if _, hasProxy := m["proxy"]; !hasProxy {
		if _, hasErr := m["error"]; !hasErr {
			t.Errorf("JSON output missing 'proxy' or 'error' key: %v", m)
		}
	}
}

// TestCheck_InvalidURL exits 1 for a clearly unreachable host.
func TestCheck_InvalidURL(t *testing.T) {
	bin := testBinary(t)
	_, _, code := run(t, bin, "check", "http://192.0.2.1:9999", "--timeout", "500ms")
	if code != 1 {
		t.Fatalf("expected exit 1 for unreachable proxy, got %d", code)
	}
}

// TestCheck_JSONError verifies --json on failure produces valid JSON.
func TestCheck_JSONError(t *testing.T) {
	bin := testBinary(t)
	out, _, code := run(t, bin, "check", "http://192.0.2.1:9999", "--timeout", "500ms", "--json")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %q", err, out)
	}
	if _, ok := m["error"]; !ok {
		t.Errorf("expected 'error' key in JSON, got %v", m)
	}
}

// TestSetMissingURL exits 1 with helpful message.
func TestSetMissingURL(t *testing.T) {
	bin := testBinary(t)
	_, stderr, code := run(t, bin, "set")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "set") {
		t.Errorf("expected usage hint, got %q", stderr)
	}
}

// TestInvalidScope exits 1 with a clear message.
func TestInvalidScope(t *testing.T) {
	bin := testBinary(t)
	_, stderr, code := run(t, bin, "set", "http://127.0.0.1:8080", "--scope", "badscope")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "badscope") && !strings.Contains(stderr, "scope") {
		t.Errorf("expected scope error, got %q", stderr)
	}
}

// TestInvalidTimeout exits 1 with a clear message.
func TestInvalidTimeout(t *testing.T) {
	bin := testBinary(t)
	_, stderr, code := run(t, bin, "check", "http://127.0.0.1:9999", "--timeout", "notaduration")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "timeout") {
		t.Errorf("expected timeout error, got %q", stderr)
	}
}
