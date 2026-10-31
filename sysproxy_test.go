package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet_NotSet(t *testing.T) {
	useMockBackend(t, &mockBackend{
		getGlobalFn: func(_ context.Context) (string, error) {
			return "", fmt.Errorf("sysproxy: proxy not set")
		},
	})
	_, err := Get()
	if err == nil {
		t.Error("expected error when proxy not set")
	}
}

func TestGet_Set(t *testing.T) {
	const want = "http://proxy.example.com:8080"
	useMockBackend(t, &mockBackend{
		getGlobalFn: func(_ context.Context) (string, error) {
			return want, nil
		},
	})
	got, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Get() = %q, want %q", got, want)
	}
}

// ── parse ─────────────────────────────────────────────────────────────────────

func TestParseWithAuth(t *testing.T) {
	p, err := parse("http://user:pass@proxy.example.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	if p.host != "proxy.example.com" {
		t.Errorf("host: got %q, want %q", p.host, "proxy.example.com")
	}
	if p.port != "8080" {
		t.Errorf("port: got %q, want %q", p.port, "8080")
	}
	if p.user != "user" {
		t.Errorf("user: got %q, want %q", p.user, "user")
	}
	if p.pass != "pass" {
		t.Errorf("pass: got %q, want %q", p.pass, "pass")
	}
}

func TestParseWithoutAuth(t *testing.T) {
	p, err := parse("http://proxy.example.com:3128")
	if err != nil {
		t.Fatal(err)
	}
	if p.host != "proxy.example.com" {
		t.Errorf("host: got %q, want %q", p.host, "proxy.example.com")
	}
	if p.user != "" || p.pass != "" {
		t.Errorf("expected empty credentials, got user=%q pass=%q", p.user, p.pass)
	}
}

func TestParseInvalidURL(t *testing.T) {
	_, err := parse("://bad url")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestParseEmpty(t *testing.T) {
	p, err := parse("")
	if err != nil {
		t.Fatal(err)
	}
	if p.host != "" || p.port != "" {
		t.Errorf("expected empty host/port, got host=%q port=%q", p.host, p.port)
	}
}

func TestParseSocks5(t *testing.T) {
	p, err := parse("socks5://user:pass@proxy.example.com:1080")
	if err != nil {
		t.Fatal(err)
	}
	if p.host != "proxy.example.com" || p.port != "1080" {
		t.Errorf("socks5 parse: host=%q port=%q", p.host, p.port)
	}
}

// ── validateProxyURL ──────────────────────────────────────────────────────────

func TestValidateProxyURL(t *testing.T) {
	cases := []struct {
		url         string
		want        bool   // true = valid
		wantErrFrag string // non-empty: substring expected in error message
	}{
		{"http://proxy.example.com:8080", true, ""},
		{"https://proxy.example.com:8080", true, ""},
		{"socks5://proxy.example.com:1080", true, ""},
		{"http://user:pass@proxy.example.com:8080", true, ""},
		{"http://localhost:8080", true, ""},
		{"://bad url", false, "scheme"},
		{"http://", false, "missing host"},
		{"http://proxy.example.com:99999", false, "out of range"},
		{"http://proxy.example.com:0", false, "out of range"},
		{"", false, "missing scheme"},
	}
	for _, c := range cases {
		err := validateProxyURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("validateProxyURL(%q): got err=%v, want valid=%v", c.url, err, c.want)
			continue
		}
		if !c.want && c.wantErrFrag != "" && !strings.Contains(err.Error(), c.wantErrFrag) {
			t.Errorf("validateProxyURL(%q): error = %q, want to contain %q", c.url, err.Error(), c.wantErrFrag)
		}
	}
}

// ── env vars ──────────────────────────────────────────────────────────────────

func TestSetEnvVars(t *testing.T) {
	const proxyURL = "http://proxy.example.com:8080"
	t.Cleanup(unsetEnvVars)

	setEnvVars(proxyURL)

	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		if got := os.Getenv(k); got != proxyURL {
			t.Errorf("%s = %q, want %q", k, got, proxyURL)
		}
	}
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		if got := os.Getenv(k); got == "" {
			t.Errorf("%s should be set, got empty", k)
		}
	}
}

func TestUnsetEnvVars(t *testing.T) {
	setEnvVars("http://proxy.example.com:8080")
	unsetEnvVars()

	for _, k := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY",
	} {
		if got := os.Getenv(k); got != "" {
			t.Errorf("%s should be unset, got %q", k, got)
		}
	}
}

// ── Set error path ────────────────────────────────────────────────────────────

func TestSetInvalidURL(t *testing.T) {
	err := Set("://bad url", ScopeShell)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestSetScopeShell(t *testing.T) {
	t.Cleanup(unsetEnvVars)
	if err := Set("http://proxy.example.com:8080", ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("http_proxy"); got != "http://proxy.example.com:8080" {
		t.Errorf("http_proxy = %q", got)
	}
}

func TestUnsetScopeShell(t *testing.T) {
	setEnvVars("http://proxy.example.com:8080")
	if err := Unset(ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("http_proxy"); got != "" {
		t.Errorf("http_proxy should be unset, got %q", got)
	}
}

func TestInvalidScope(t *testing.T) {
	if err := Set("http://proxy.example.com:8080", ProxyScope(99)); err == nil {
		t.Error("expected error for invalid scope")
	}
	if err := Unset(ProxyScope(99)); err == nil {
		t.Error("expected error for invalid scope")
	}
}

// ── Logger ────────────────────────────────────────────────────────────────────

type testLogger struct{ msgs []string }

func (l *testLogger) Log(msg string) { l.msgs = append(l.msgs, msg) }

func TestSetLogger(t *testing.T) {
	l := &testLogger{}
	SetLogger(l)
	t.Cleanup(func() { SetLogger(nil) })

	_ = Set("http://proxy.example.com:8080", ScopeShell)

	if len(l.msgs) == 0 {
		t.Error("expected at least one log message after Set")
	}
}

// ── WriteAppConfig / ClearAppConfig ──────────────────────────────────────────

func setTestHome(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if volume := filepath.VolumeName(home); volume != "" {
		t.Setenv("HOMEDRIVE", volume)
		t.Setenv("HOMEPATH", strings.TrimPrefix(home, volume))
	}

	return home
}

func TestWriteAppConfigCurl(t *testing.T) {
	home := setTestHome(t)

	if err := WriteAppConfig(AppCurl, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".curlrc")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy = http://proxy.example.com:8080") {
		t.Errorf("unexpected content: %s", data)
	}
}

func TestClearAppConfigCurl(t *testing.T) {
	home := setTestHome(t)

	_ = WriteAppConfig(AppCurl, "http://proxy.example.com:8080")
	if err := ClearAppConfig(AppCurl); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".curlrc")) //nolint:gosec
	if strings.Contains(string(data), "proxy") {
		t.Errorf("proxy should be removed, got: %s", data)
	}
}

func TestWriteAppConfigPip(t *testing.T) {
	home := setTestHome(t)

	if err := WriteAppConfig(AppPip, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".config", "pip", "pip.conf")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy = http://proxy.example.com:8080") {
		t.Errorf("unexpected pip.conf content: %s", data)
	}
}

func TestWriteAppConfigWget(t *testing.T) {
	home := setTestHome(t)

	if err := WriteAppConfig(AppWget, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".wgetrc")) //nolint:gosec
	if !strings.Contains(string(data), "http_proxy = http://proxy.example.com:8080") {
		t.Errorf("unexpected .wgetrc content: %s", data)
	}
}

func TestWriteAppConfigUnsupported(t *testing.T) {
	if err := WriteAppConfig("burp", "http://proxy.example.com:8080"); err == nil {
		t.Error("expected error for unsupported app")
	}
}

// ── ScopeGlobal via mock backend ──────────────────────────────────────────────

func TestSetGlobal_CallsBackend(t *testing.T) {
	var called bool
	useMockBackend(t, &mockBackend{
		setGlobalFn: func(_ context.Context, p *proxy) error {
			called = true
			if p.host != "proxy.example.com" || p.port != "8080" {
				t.Errorf("unexpected proxy: host=%q port=%q", p.host, p.port)
			}
			return nil
		},
	})
	t.Cleanup(unsetEnvVars)

	if err := Set("http://proxy.example.com:8080", ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("backend.SetGlobal was not called")
	}
}

func TestSetGlobal_BackendError(t *testing.T) {
	useMockBackend(t, &mockBackend{
		setGlobalFn: func(_ context.Context, _ *proxy) error {
			return errors.New("backend error")
		},
	})
	t.Cleanup(unsetEnvVars)

	if err := Set("http://proxy.example.com:8080", ScopeGlobal); err == nil {
		t.Error("expected error from backend")
	}
}

func TestUnsetGlobal_CallsBackend(t *testing.T) {
	var called bool
	useMockBackend(t, &mockBackend{
		unsetGlobalFn: func(_ context.Context) error {
			called = true
			return nil
		},
	})
	t.Cleanup(unsetEnvVars)

	if err := Unset(ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("backend.UnsetGlobal was not called")
	}
}

func TestGetContext_PropagatesURL(t *testing.T) {
	const want = "http://proxy.example.com:9090"
	useMockBackend(t, &mockBackend{
		getGlobalFn: func(_ context.Context) (string, error) { return want, nil },
	})

	got, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Get() = %q, want %q", got, want)
	}
}

func TestSetMultiGlobal_CallsBackend(t *testing.T) {
	var got ProxyConfig
	useMockBackend(t, &mockBackend{
		setGlobalMultiFn: func(_ context.Context, cfg ProxyConfig) error {
			got = cfg
			return nil
		},
	})
	t.Cleanup(unsetEnvVars)

	want := ProxyConfig{
		HTTP:  "http://http.example.com:8080",
		HTTPS: "http://https.example.com:8080",
	}
	if err := SetMulti(want, ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if got.HTTP != want.HTTP || got.HTTPS != want.HTTPS {
		t.Errorf("SetMulti passed %+v, want %+v", got, want)
	}
}

func TestSetPACGlobal_CallsBackend(t *testing.T) {
	const pacURL = "http://config.example.com/proxy.pac"
	var called bool
	useMockBackend(t, &mockBackend{
		setGlobalPACFn: func(_ context.Context, u string) error {
			called = true
			if u != pacURL {
				t.Errorf("SetGlobalPAC got %q, want %q", u, pacURL)
			}
			return nil
		},
	})
	t.Cleanup(unsetEnvVars)

	if err := SetPAC(pacURL, ScopeGlobal); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("backend.SetGlobalPAC was not called")
	}
}

func TestWithProxy_PropagatesFnError(t *testing.T) {
	useMockBackend(t, &mockBackend{
		setGlobalFn:       func(_ context.Context, _ *proxy) error { return nil },
		unsetGlobalFn:     func(_ context.Context) error { return nil },
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return ProxyConfig{}, ErrProxyNotSet },
	})
	t.Cleanup(unsetEnvVars)

	sentinel := errors.New("boom")
	err := WithProxy(context.Background(), "http://x:8080", ScopeGlobal, func(_ context.Context) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// ── SetMultiContext / SetPACContext – ScopeShell ──────────────────────────────

func TestSetMultiContext_ScopeShell(t *testing.T) {
	t.Cleanup(unsetEnvVars)

	cfg := ProxyConfig{HTTP: "http://proxy.example.com:8080", HTTPS: "http://proxy.example.com:8080"}
	if err := SetMulti(cfg, ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("http_proxy"); got != cfg.HTTP {
		t.Errorf("http_proxy = %q, want %q", got, cfg.HTTP)
	}
}

func TestSetPACContext_ScopeShell(t *testing.T) {
	t.Cleanup(func() { _ = os.Unsetenv("AUTOPROXY") })

	if err := SetPAC("http://config.example.com/proxy.pac", ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AUTOPROXY"); got != "http://config.example.com/proxy.pac" {
		t.Errorf("AUTOPROXY = %q", got)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// F-14: a URL without a port gets the default of its scheme, never "".
func TestEndpointFromURL(t *testing.T) {
	cases := []struct{ url, host, port string }{
		{"http://proxy.example.com:8080", "proxy.example.com", "8080"},
		{"socks5://user:pass@proxy.example.com:1080", "proxy.example.com", "1080"},
		{"http://proxy.example.com", "proxy.example.com", "80"},
		{"https://proxy.example.com", "proxy.example.com", "443"},
		{"socks5://proxy.example.com", "proxy.example.com", "1080"},
		{"socks5h://proxy.example.com", "proxy.example.com", "1080"},
		{"http://[::1]:3128", "::1", "3128"},
		{"http://[2001:db8::1]", "2001:db8::1", "80"},
		{"://bad", "", ""},
	}
	for _, c := range cases {
		host, port := endpointFromURL(c.url)
		if host != c.host || port != c.port {
			t.Errorf("endpointFromURL(%q) = (%q, %q), want (%q, %q)", c.url, host, port, c.host, c.port)
		}
	}
}

func TestProxyHostPortBracketsIPv6(t *testing.T) {
	cases := map[string]string{
		"http://proxy.example.com:8080": "proxy.example.com:8080",
		"http://[::1]:8080":             "[::1]:8080",
		"http://[2001:db8::1]":          "[2001:db8::1]:80",
		"http://10.0.0.1":               "10.0.0.1:80",
	}
	for raw, want := range cases {
		p, err := parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.hostPort(); got != want {
			t.Errorf("hostPort(%q) = %q, want %q", raw, got, want)
		}
	}
}

// F-13: which system field a proxy URL belongs in is decided by its scheme.
func TestProxyIsSOCKS(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://h:1": false, "https://h:1": false,
		"socks5://h:1": true, "socks5h://h:1": true, "socks4://h:1": true, "socks4a://h:1": true, "socks://h:1": true,
	} {
		p, err := parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if p.isSOCKS() != want {
			t.Errorf("isSOCKS(%q) = %v, want %v", raw, !want, want)
		}
	}
}

// ── normalizeContext ──────────────────────────────────────────────────────────

func TestNormalizeContext_Nil(t *testing.T) {
	var nilCtx context.Context // typed nil, avoids SA1012 on literal nil
	ctx := normalizeContext(nilCtx)
	if ctx == nil {
		t.Error("normalizeContext(nil) returned nil")
	}
}

func TestNormalizeContext_NonNil(t *testing.T) {
	orig := context.Background()
	if got := normalizeContext(orig); got != orig {
		t.Error("normalizeContext should return the same non-nil context")
	}
}

// ── validatePACURL ────────────────────────────────────────────────────────────

func TestValidatePACURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://config.example.com/proxy.pac", true},
		{"https://config.example.com/proxy.pac", true},
		{"file:///etc/proxy.pac", true},
		{"ftp://bad.example.com/proxy.pac", false},
		{"", false},
	}
	for _, c := range cases {
		err := validatePACURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("validatePACURL(%q): got err=%v, want valid=%v", c.url, err, c.want)
		}
	}
}

// ── GetConfig via mock backend ────────────────────────────────────────────────

func TestGetConfig_ReturnsFull(t *testing.T) {
	want := ProxyConfig{
		HTTP:    "http://http.example.com:8080",
		HTTPS:   "http://https.example.com:8080",
		SOCKS:   "socks5://socks.example.com:1080",
		NoProxy: "localhost,10.0.0.0/8",
	}
	useMockBackend(t, &mockBackend{
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return want, nil },
	})

	got, err := GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("GetConfig() = %+v, want %+v", got, want)
	}
}

func TestGetConfig_BackendError(t *testing.T) {
	useMockBackend(t, &mockBackend{
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) {
			return ProxyConfig{}, errors.New("proxy not set")
		},
	})

	_, err := GetConfig()
	if err == nil {
		t.Error("expected error from backend")
	}
}

func TestGetConfigContext_CanceledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetConfigContext(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// ── ProxyScope.String ─────────────────────────────────────────────────────────

func TestProxyScopeString(t *testing.T) {
	cases := []struct {
		scope ProxyScope
		want  string
	}{
		{ScopeShell, "shell"},
		{ScopeUser, "user"},
		{ScopeGlobal, "global"},
		{ProxyScope(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.scope.String(); got != c.want {
			t.Errorf("ProxyScope(%d).String() = %q, want %q", c.scope, got, c.want)
		}
	}
}

// ── F-01 / F-02: validation hardening and redaction ──────────────────────────

func TestValidatePACURL_RejectsUnsafe(t *testing.T) {
	for _, bad := range []string{
		"https://x.example/p.pac'\ntouch /tmp/x\n#",
		"https://x.example/p.pac\r\nfoo",
		"https://x.example/a b.pac",
		"https://x.example/\x00",
		"http://",
		"https:///p.pac",
	} {
		if err := validatePACURL(bad); err == nil {
			t.Errorf("validatePACURL(%q) accepted unsafe input", bad)
		}
	}
	if err := validatePACURL("file:///etc/proxy.pac"); err != nil {
		t.Errorf("file URL must stay valid: %v", err)
	}
}

func TestValidateProxyURL_RejectsWhitespaceAndControl(t *testing.T) {
	for _, bad := range []string{"http://h:80/a b", "http://h:80/\t", "http://h:80/\u2028x"} {
		if err := validateProxyURL(bad); err == nil {
			t.Errorf("validateProxyURL(%q) accepted whitespace/control characters", bad)
		}
	}
}

func TestValidateNoProxy(t *testing.T) {
	for _, ok := range []string{"", "localhost,127.0.0.1,::1", "10.0.0.0/8;*.corp.example.com", "<local>", "[::1]:8080", ".example.com, foo.bar"} {
		if err := validateNoProxy(ok); err != nil {
			t.Errorf("validateNoProxy(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"a'b", "a\"b", "a\nb", "a\\b", "$(x)", "`x`", "a\tb", "a&b", "a|b", "a\x00b"} {
		if err := validateNoProxy(bad); err == nil {
			t.Errorf("validateNoProxy(%q) accepted a dangerous character", bad)
		}
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"http://u:secret@h:80":       "http://u:***@h:80",
		"http://u:secret@h:80/p?q=1": "http://u:***@h:80/p?q=1",
		"http://token@h:80":          "http://token@h:80",
		"http://h:80/p@x":            "http://h:80/p@x",
		"u:secret@h:80":              "u:***@h:80",
		"http://u:p@ss@h:80":         "http://u:***@h:80",
		"plain":                      "plain",
		"":                           "",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidationErrorsDoNotLeakPassword(t *testing.T) {
	for _, raw := range []string{
		"http://alice:s3cr3t@/nohost",
		"http://alice:s3cr3t@h:99999",
		"http://alice:s3cr3t@h:80/a b",
		"alice:s3cr3t@h:80",
		"http://alice:s3cr3t@h:80/%zz",
	} {
		err := validateProxyURL(raw)
		if err == nil {
			t.Errorf("validateProxyURL(%q) unexpectedly succeeded", raw)
			continue
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("validateProxyURL(%q) error leaks the password: %v", raw, err)
		}
	}
	if _, err := parse("http://alice:s3cr3t@h:80/%zz"); err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("parse error must not leak the password: %v", err)
	}
	if err := validatePACURL("https://alice:s3cr3t@x.example/a b"); err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("PAC error must not leak the password: %v", err)
	}
}

func TestLogger_DoesNotLeakPassword(t *testing.T) {
	l := &testLogger{}
	SetLogger(l)
	t.Cleanup(func() { SetLogger(nil) })
	setTestHome(t)
	t.Cleanup(unsetEnvVars)

	const url = "http://alice:s3cr3t@proxy.example.com:8080"
	_ = Set(url, ScopeShell)
	_ = WriteAppConfig(AppCurl, url)

	if len(l.msgs) < 2 {
		t.Fatalf("expected log messages, got %v", l.msgs)
	}
	for _, m := range l.msgs {
		if strings.Contains(m, "s3cr3t") {
			t.Errorf("log line leaks the password: %s", m)
		}
	}
}

// F-18: SetMulti and SetPAC left no trace, unlike Set.
func TestLogger_CoversSetMultiAndSetPACWithoutPasswords(t *testing.T) {
	l := &testLogger{}
	SetLogger(l)
	t.Cleanup(func() { SetLogger(nil) })
	setTestHome(t)
	t.Cleanup(unsetEnvVars)

	if err := SetMulti(ProxyConfig{HTTP: "http://alice:s3cr3t@proxy.example.com:8080", SOCKS: "socks5://proxy.example.com:1080", NoProxy: "localhost"}, ScopeShell); err != nil {
		t.Fatal(err)
	}
	if err := SetPAC("https://bob:hunter2@pac.example.com/p.pac", ScopeShell); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(l.msgs, "\n")
	for _, want := range []string{"set multi scope=shell", "http=http://alice:***@proxy.example.com:8080", "socks=socks5://proxy.example.com:1080", "set pac scope=shell"} {
		if !strings.Contains(joined, want) {
			t.Errorf("log lacks %q:\n%s", want, joined)
		}
	}
	for _, secret := range []string{"s3cr3t", "hunter2"} {
		if strings.Contains(joined, secret) {
			t.Errorf("log leaks %q:\n%s", secret, joined)
		}
	}
}

// The PAC URL variable set by SetPAC(ScopeShell) is removed again by Unset.
func TestUnsetScopeShell_ClearsAutoproxy(t *testing.T) {
	setTestHome(t)
	t.Cleanup(unsetEnvVars)
	if err := SetPAC("https://pac.example.com/p.pac", ScopeShell); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AUTOPROXY") == "" {
		t.Fatal("SetPAC did not set AUTOPROXY")
	}
	if err := Unset(ScopeShell); err != nil {
		t.Fatal(err)
	}
	if v, ok := os.LookupEnv("AUTOPROXY"); ok {
		t.Errorf("AUTOPROXY still set to %q after Unset", v)
	}
}

// Every variable WithProxy snapshots is one that Unset clears, so the two
// lists cannot drift apart.
func TestUnsetEnvVars_ClearsEveryManagedVariable(t *testing.T) {
	for _, k := range proxyEnvNames {
		t.Setenv(k, "x")
	}
	unsetEnvVars()
	for _, k := range proxyEnvNames {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set to %q", k, v)
		}
	}
}

// Internationalised host names are written in punycode: profile files and code
// pages do not all read non-ASCII text back unchanged. The error says so.
func TestValidateNoProxy_NonASCIINamesPointToPunycode(t *testing.T) {
	for _, ok := range []string{"*.xn--mnchen-3ya.de", "xn--r8jz45g.jp,localhost"} {
		if err := validateNoProxy(ok); err != nil {
			t.Errorf("validateNoProxy(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"*.münchen.de", "例え.jp", "пример.рф"} {
		err := validateNoProxy(bad)
		if err == nil || !strings.Contains(err.Error(), "punycode") {
			t.Errorf("validateNoProxy(%q) = %v, want a punycode hint", bad, err)
		}
	}
	for _, bad := range []string{"a\u202eb", "a\u200bb", "a\u2028b", "x\u0085y", "a'b", "a\"b", "a$b"} {
		if err := validateNoProxy(bad); err == nil {
			t.Errorf("validateNoProxy(%q) accepted a control, format or shell character", bad)
		}
	}
}

// Every variable the env setters write is one Unset clears and WithProxy
// snapshots; a new variable cannot be added to one list only.
func TestEnvSetters_OnlyTouchManagedVariables(t *testing.T) {
	managed := map[string]bool{}
	for _, k := range proxyEnvNames {
		managed[k] = true
	}
	snap := func() map[string]string {
		m := map[string]string{}
		for _, kv := range os.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		return m
	}
	for name, set := range map[string]func(){
		"setEnvVars":    func() { setEnvVars("http://p:1") },
		"setEnvVarsPAC": func() { setEnvVarsPAC("http://p/x.pac") },
		"setEnvVarsMulti": func() {
			setEnvVarsMulti(ProxyConfig{HTTP: "http://a:1", HTTPS: "http://b:2", SOCKS: "socks5://c:3", NoProxy: "x"})
		},
	} {
		before := snap()
		set()
		for k, v := range snap() {
			if old, ok := before[k]; (!ok || old != v) && !managed[k] {
				t.Errorf("%s set %s, which is not in proxyEnvNames", name, k)
			}
		}
		unsetEnvVars()
	}
}

func TestJoinHostPort(t *testing.T) {
	cases := [][3]string{
		{"proxy.example.com", "8080", "proxy.example.com:8080"},
		{"::1", "8080", "[::1]:8080"},
		{"[::1]", "8080", "[::1]:8080"}, // already bracketed: no second pair
		{"fd00::5", "1080", "[fd00::5]:1080"},
	}
	for _, c := range cases {
		if got := joinHostPort(c[0], c[1]); got != c[2] {
			t.Errorf("joinHostPort(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
