package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Get ───────────────────────────────────────────────────────────────────────

func TestGet_NotSet(t *testing.T) {
	proxy, err := Get()
	if err == nil && proxy != "" {
		t.Error("expected error or empty proxy when not set")
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
		url  string
		want bool // true = valid
	}{
		{"http://proxy.example.com:8080", true},
		{"https://proxy.example.com:8080", true},
		{"socks5://proxy.example.com:1080", true},
		{"http://user:pass@proxy.example.com:8080", true},
		{"http://localhost:8080", true}, // localhost allowed — library should not restrict this
		{"://bad url", false},
		{"http://proxy.example.com:99999", false},
		{"http://proxy.example.com:0", false},
		{"", false},
	}
	for _, c := range cases {
		err := validateProxyURL(c.url)
		if (err == nil) != c.want {
			t.Errorf("validateProxyURL(%q): got err=%v, want valid=%v", c.url, err, c.want)
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
