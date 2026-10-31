package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	sysproxy "github.com/mar0ls/go-sysproxy"
)

// stubAPI replaces the library entry points for one test.
func stubAPI(t *testing.T, setErr, unsetErr, pacErr error) {
	t.Helper()
	origSet, origUnset, origPAC := setProxy, unsetProxy, setPAC
	t.Cleanup(func() { setProxy, unsetProxy, setPAC = origSet, origUnset, origPAC })
	setProxy = func(context.Context, string, sysproxy.ProxyScope) error { return setErr }
	unsetProxy = func(context.Context, sysproxy.ProxyScope) error { return unsetErr }
	setPAC = func(context.Context, string, sysproxy.ProxyScope) error { return pacErr }
}

func stubGet(t *testing.T, url string, urlErr error, cfg sysproxy.ProxyConfig, cfgErr error) {
	t.Helper()
	origGet, origCfg := getProxy, getProxyCfg
	t.Cleanup(func() { getProxy, getProxyCfg = origGet, origCfg })
	getProxy = func(context.Context) (string, error) { return url, urlErr }
	getProxyCfg = func(context.Context) (sysproxy.ProxyConfig, error) { return cfg, cfgErr }
}

// softError stands in for the library's non-critical error type, which cannot
// be constructed outside the package; the classifiers are stubbed to match.
type softError struct{ elevation bool }

func (e *softError) Error() string { return "open /etc/environment: permission denied" }

func stubClassifiers(t *testing.T) {
	t.Helper()
	origNC, origEl := isNonCritical, requiresElevation
	t.Cleanup(func() { isNonCritical, requiresElevation = origNC, origEl })
	isNonCritical = func(err error) bool { var s *softError; return errors.As(err, &s) }
	requiresElevation = func(err error) bool {
		var s *softError
		return errors.As(err, &s) && s.elevation
	}
}

func warning(t *testing.T) error {
	t.Helper()
	stubClassifiers(t)
	return &softError{}
}

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// F-07: a non-critical error is a warning, not a failure.
func TestWriteCommands_NonCriticalIsWarningExit0(t *testing.T) {
	warn := warning(t)
	stubAPI(t, warn, warn, warn)
	cmds := map[string][]string{
		"set":   {"set", "http://127.0.0.1:8080"},
		"unset": {"unset"},
		"pac":   {"pac", "https://example.com/proxy.pac"},
	}
	for name, args := range cmds {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runCLI(args...)
			if code != 0 {
				t.Fatalf("exit %d, want 0 (stderr=%q)", code, stderr)
			}
			if !strings.Contains(stderr, "warning: ") || !strings.Contains(stderr, "/etc/environment") {
				t.Errorf("warning missing on stderr: %q", stderr)
			}
			if strings.TrimSpace(stdout) != "ok" {
				t.Errorf("stdout = %q, want ok", stdout)
			}
		})
	}
}

func TestWriteCommands_NonCriticalJSON(t *testing.T) {
	stubClassifiers(t)
	warn := error(&softError{elevation: true})
	stubAPI(t, warn, warn, warn)
	for _, args := range [][]string{
		{"set", "http://127.0.0.1:8080", "--json"},
		{"unset", "--json"},
		{"pac", "https://example.com/proxy.pac", "--json"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d, want 0 (stderr=%q)", args, code, stderr)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(stdout), &m); err != nil {
			t.Fatalf("%v: invalid JSON %q: %v", args, stdout, err)
		}
		if m["ok"] != true || m["warning"] == nil || m["requires_elevation"] != true {
			t.Errorf("%v: unexpected JSON %v", args, m)
		}
	}
}

func TestWriteCommands_HardErrorExit1(t *testing.T) {
	hard := errors.New("sysproxy: networksetup -setwebproxy: exit status 1")
	stubAPI(t, hard, hard, hard)
	for _, args := range [][]string{
		{"set", "http://127.0.0.1:8080"},
		{"unset"},
		{"pac", "https://example.com/proxy.pac"},
	} {
		code, stdout, stderr := runCLI(args...)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1", args, code)
		}
		if !strings.Contains(stderr, "failed") || stdout != "" {
			t.Errorf("%v: stdout=%q stderr=%q", args, stdout, stderr)
		}
	}
	code, stdout, _ := runCLI("set", "http://127.0.0.1:8080", "--json")
	var m map[string]any
	if code != 1 || json.Unmarshal([]byte(stdout), &m) != nil || m["ok"] != false {
		t.Errorf("json hard error: code=%d stdout=%q", code, stdout)
	}
}

// F-08: a PAC URL alone is "configured" for get --json.
func TestGetJSON_PACOnly(t *testing.T) {
	stubGet(t, "", sysproxy.ErrProxyNotSet, sysproxy.ProxyConfig{PAC: "https://example.com/proxy.pac"}, nil)
	code, stdout, _ := runCLI("get", "--json")
	if code != 0 {
		t.Fatalf("exit %d, want 0 (stdout=%q)", code, stdout)
	}
	var cfg sysproxy.ProxyConfig
	if err := json.Unmarshal([]byte(stdout), &cfg); err != nil || cfg.PAC == "" {
		t.Errorf("stdout = %q (err %v)", stdout, err)
	}
}

func TestGetJSON_NotSetAndErrors(t *testing.T) {
	stubGet(t, "", sysproxy.ErrProxyNotSet, sysproxy.ProxyConfig{}, nil)
	if code, stdout, _ := runCLI("get", "--json"); code != 2 || !strings.Contains(stdout, "proxy not set") {
		t.Errorf("empty config: code=%d stdout=%q", code, stdout)
	}
	stubGet(t, "", nil, sysproxy.ProxyConfig{}, sysproxy.ErrProxyNotSet)
	if code, _, _ := runCLI("get", "--json"); code != 2 {
		t.Errorf("ErrProxyNotSet from GetConfig: code=%d, want 2", code)
	}
	stubGet(t, "", nil, sysproxy.ProxyConfig{}, fmt.Errorf("%w: gsettings", sysproxy.ErrToolMissing))
	code, stdout, _ := runCLI("get", "--json")
	if code != 1 || !strings.Contains(stdout, `"ok": false`) || !strings.Contains(stdout, "gsettings") {
		t.Errorf("real error must not look like 'not set': code=%d stdout=%q", code, stdout)
	}
}

// Windows reports a disabled proxy as ErrProxyNotEnabled; that is "not set", not a failure.
func TestGet_ProxyNotEnabledIsNotSet(t *testing.T) {
	stubGet(t, "", sysproxy.ErrProxyNotEnabled, sysproxy.ProxyConfig{}, sysproxy.ErrProxyNotEnabled)
	if code, _, _ := runCLI("get"); code != 2 {
		t.Errorf("get: code=%d, want 2", code)
	}
	if code, _, _ := runCLI("get", "--json"); code != 2 {
		t.Errorf("get --json: code=%d, want 2", code)
	}
}

func TestGetText(t *testing.T) {
	stubGet(t, "http://127.0.0.1:8080", nil, sysproxy.ProxyConfig{}, nil)
	if code, stdout, _ := runCLI("get"); code != 0 || strings.TrimSpace(stdout) != "http://127.0.0.1:8080" {
		t.Errorf("set: code=%d stdout=%q", code, stdout)
	}

	stubGet(t, "", sysproxy.ErrProxyNotSet, sysproxy.ProxyConfig{}, sysproxy.ErrProxyNotSet)
	if code, _, stderr := runCLI("get"); code != 2 || strings.TrimSpace(stderr) != "proxy not set" {
		t.Errorf("not set: code=%d stderr=%q", code, stderr)
	}

	stubGet(t, "", sysproxy.ErrProxyNotSet, sysproxy.ProxyConfig{PAC: "https://example.com/proxy.pac"}, nil)
	if code, stdout, stderr := runCLI("get"); code != 2 || stdout != "" || !strings.Contains(stderr, "PAC active: https://example.com/proxy.pac") {
		t.Errorf("pac: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	stubGet(t, "", errors.New("networksetup: boom"), sysproxy.ProxyConfig{}, nil)
	if code, _, stderr := runCLI("get"); code != 1 || !strings.Contains(stderr, "boom") {
		t.Errorf("real error: code=%d stderr=%q", code, stderr)
	}
}

// Some backends (KDE) store the login inside the proxy URL; get must not print it.
func TestGet_NeverPrintsPasswords(t *testing.T) {
	cfg := sysproxy.ProxyConfig{
		HTTP:  "http://alice:s3cr3tpw@proxy.example.com:8080",
		SOCKS: "socks5://bob:hunter2@proxy.example.com:1080",
		PAC:   "https://carol:pacpw@pac.example.com/p.pac",
	}
	stubGet(t, cfg.HTTP, nil, cfg, nil)
	for _, args := range [][]string{{"get"}, {"get", "--json"}} {
		code, out, errOut := runCLI(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d (%s)", args, code, errOut)
		}
		for _, secret := range []string{"s3cr3tpw", "hunter2", "pacpw"} {
			if strings.Contains(out+errOut, secret) {
				t.Errorf("%v leaks %q:\n%s", args, secret, out)
			}
		}
		if !strings.Contains(out, "proxy.example.com:8080") {
			t.Errorf("%v lost the host:\n%s", args, out)
		}
	}
}

// A PAC URL can carry a login too; neither the pac command nor get may echo it.
func TestPAC_NeverPrintsPasswords(t *testing.T) {
	const pac = "https://carol:pacpw@pac.example.com/p.pac"
	stubAPI(t, nil, nil, nil)
	for _, args := range [][]string{{"pac", pac}, {"pac", pac, "--json"}} {
		code, out, errOut := runCLI(args...)
		if code != 0 || strings.Contains(out+errOut, "pacpw") {
			t.Errorf("%v: exit %d, output leaks the password:\n%s%s", args, code, out, errOut)
		}
	}

	stubGet(t, "", sysproxy.ErrProxyNotSet, sysproxy.ProxyConfig{PAC: pac}, nil)
	code, out, errOut := runCLI("get")
	if code != 2 || strings.Contains(out+errOut, "pacpw") || !strings.Contains(errOut, "pac.example.com") {
		t.Errorf("get with a PAC URL: exit %d, out %q, err %q", code, out, errOut)
	}
}
