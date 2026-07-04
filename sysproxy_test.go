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

func TestWithProxy_RestoresPrevious(t *testing.T) {
	prev := ProxyConfig{
		HTTP:  "http://prev.example.com:8080",
		HTTPS: "http://prev.example.com:8080",
		SOCKS: "socks5://prev.example.com:1080",
	}
	const next = "http://next.example.com:9090"

	setLog := []string{}
	multiLog := []ProxyConfig{}
	useMockBackend(t, &mockBackend{
		setGlobalFn: func(_ context.Context, p *proxy) error {
			setLog = append(setLog, p.rawURL)
			return nil
		},
		setGlobalMultiFn: func(_ context.Context, cfg ProxyConfig) error {
			multiLog = append(multiLog, cfg)
			return nil
		},
		unsetGlobalFn:     func(_ context.Context) error { return nil },
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return prev, nil },
	})
	t.Cleanup(unsetEnvVars)

	err := WithProxy(context.Background(), next, ScopeGlobal, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(setLog) != 1 || setLog[0] != next {
		t.Fatalf("expected exactly one SetGlobal(%q), got %v", next, setLog)
	}
	if len(multiLog) != 1 || multiLog[0] != prev {
		t.Fatalf("expected restore SetMulti(%+v), got %+v", prev, multiLog)
	}
}

func TestWithProxy_UnsetsWhenNoPrevious(t *testing.T) {
	unsetCalled := 0
	useMockBackend(t, &mockBackend{
		setGlobalFn:       func(_ context.Context, _ *proxy) error { return nil },
		unsetGlobalFn:     func(_ context.Context) error { unsetCalled++; return nil },
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return ProxyConfig{}, ErrProxyNotSet },
	})
	t.Cleanup(unsetEnvVars)

	err := WithProxy(context.Background(), "http://next:9090", ScopeGlobal, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsetCalled != 1 {
		t.Fatalf("expected exactly one Unset, got %d", unsetCalled)
	}
}

func TestWithProxyMulti_RestoresPAC(t *testing.T) {
	pacURL := "http://config.example.com/proxy.pac"
	prev := ProxyConfig{PAC: pacURL}
	next := ProxyConfig{HTTP: "http://next.example.com:8080"}

	pacRestored := ""
	useMockBackend(t, &mockBackend{
		setGlobalMultiFn:  func(_ context.Context, _ ProxyConfig) error { return nil },
		setGlobalPACFn:    func(_ context.Context, u string) error { pacRestored = u; return nil },
		unsetGlobalFn:     func(_ context.Context) error { return nil },
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return prev, nil },
	})
	t.Cleanup(unsetEnvVars)

	err := WithProxyMulti(context.Background(), next, ScopeGlobal, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pacRestored != pacURL {
		t.Fatalf("expected PAC %q to be restored, got %q", pacURL, pacRestored)
	}
}

func TestWithProxy_RestoresMultiWhenSnapshotHasProtocols(t *testing.T) {
	prev := ProxyConfig{
		HTTP:  "http://prev.example.com:8080",
		HTTPS: "http://prev.example.com:8443",
	}
	restored := ProxyConfig{}
	useMockBackend(t, &mockBackend{
		setGlobalFn:      func(_ context.Context, _ *proxy) error { return nil },
		setGlobalMultiFn: func(_ context.Context, cfg ProxyConfig) error { restored = cfg; return nil },
		unsetGlobalFn: func(_ context.Context) error {
			t.Fatal("Unset should not be called when snapshot has state")
			return nil
		},
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return prev, nil },
	})
	t.Cleanup(unsetEnvVars)

	err := WithProxy(context.Background(), "http://next:9090", ScopeGlobal, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored != prev {
		t.Fatalf("SetMulti restore = %+v, want %+v", restored, prev)
	}
}

func TestWithProxyMulti_UnsetsWhenNoPrevious(t *testing.T) {
	unsetCalled := 0
	useMockBackend(t, &mockBackend{
		setGlobalMultiFn:  func(_ context.Context, _ ProxyConfig) error { return nil },
		unsetGlobalFn:     func(_ context.Context) error { unsetCalled++; return nil },
		getGlobalConfigFn: func(_ context.Context) (ProxyConfig, error) { return ProxyConfig{}, ErrProxyNotSet },
	})
	t.Cleanup(unsetEnvVars)

	err := WithProxyMulti(context.Background(), ProxyConfig{HTTP: "http://x:8080"}, ScopeGlobal, func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsetCalled != 1 {
		t.Fatalf("expected exactly one Unset, got %d", unsetCalled)
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

func TestHostFromURL(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://proxy.example.com:8080", "proxy.example.com"},
		{"socks5://user:pass@proxy.example.com:1080", "proxy.example.com"},
		{"://bad", ""},
	}
	for _, c := range cases {
		if got := hostFromURL(c.url); got != c.want {
			t.Errorf("hostFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestPortFromURL(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://proxy.example.com:8080", "8080"},
		{"socks5://proxy.example.com:1080", "1080"},
		{"://bad", ""},
	}
	for _, c := range cases {
		if got := portFromURL(c.url); got != c.want {
			t.Errorf("portFromURL(%q) = %q, want %q", c.url, got, c.want)
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
