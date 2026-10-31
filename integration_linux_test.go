//go:build linux

package sysproxy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// These tests drive the real gsettings (dconf), kwriteconfig6|5 and
// /etc/environment. Run them inside a D-Bus session, as root and as a normal
// user:
//
//	dbus-run-session -- ./sysproxy.test -test.run Integration -test.v

func itRun(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func itGS(t *testing.T, schema, key string) string {
	t.Helper()
	return itRun(t, "gsettings", "get", schema, key)
}

func itKDE(t *testing.T, key string) string {
	t.Helper()
	return itRun(t, kreadconfigBinary(), "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key)
}

func itRequireDesktopTools(t *testing.T) {
	t.Helper()
	requireIntegration(t)
	if !isAvailable("gsettings") || kwriteconfigBinary() == "" || kreadconfigBinary() == "" {
		t.Skip("gsettings and kwriteconfig/kreadconfig (6 or 5) are required")
	}
	if !gnomeUsable(context.Background()) {
		t.Skip("GNOME proxy schema not available")
	}
}

// itState reads everything the package touches, with the real tools.
func itState(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, k := range gnomeKeys {
		m[k.schema+" "+k.key] = itGS(t, k.schema, k.key)
	}
	for _, k := range kdeKeys {
		m["kde "+k] = itKDE(t, k)
	}
	data, err := os.ReadFile(etcEnvironmentPath)
	m["etc exists"] = map[bool]string{true: "yes", false: "no"}[err == nil]
	m["etc"] = string(data)
	return m
}

func itDiff(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: before %q, after %q", k, v, after[k])
		}
	}
}

func itCleanup(t *testing.T) {
	t.Helper()
	before := itState(t)
	t.Cleanup(func() { _ = Unset(ScopeGlobal) })
	_ = before
}

func TestIntegrationLinux_SetPACUnsetRoundTrip(t *testing.T) {
	itRequireDesktopTools(t)
	t.Cleanup(func() { _ = UnsetContext(context.Background(), ScopeGlobal) })

	err := SetContext(context.Background(), "http://proxy.example.com:8080", ScopeGlobal)
	if err != nil && !IsNonCritical(err) {
		t.Fatalf("Set: %v", err)
	}
	if os.Geteuid() == 0 && err != nil {
		t.Errorf("Set as root returned %v", err)
	}
	if os.Geteuid() != 0 && !RequiresElevation(err) {
		t.Errorf("Set as a normal user: err = %v, want a RequiresElevation warning", err)
	}
	if got := itGS(t, "org.gnome.system.proxy", "mode"); got != "'manual'" {
		t.Errorf("GNOME mode after Set = %s", got)
	}
	if got := itKDE(t, "ProxyType"); got != "1" {
		t.Errorf("KDE ProxyType after Set = %q", got)
	}
	cfg, gerr := GetConfig()
	if gerr != nil || cfg.HTTP != "http://proxy.example.com:8080" {
		t.Errorf("GetConfig after Set = %+v, %v", cfg, gerr)
	}

	if err := SetPAC("http://pac.example.com/proxy.pac", ScopeGlobal); err != nil {
		t.Fatalf("SetPAC: %v", err)
	}
	cfg, gerr = GetConfig()
	if gerr != nil || cfg.PAC != "http://pac.example.com/proxy.pac" || cfg.HTTP != "" {
		t.Errorf("GetConfig after SetPAC = %+v, %v", cfg, gerr)
	}

	err = Unset(ScopeGlobal)
	if err != nil && !IsNonCritical(err) {
		t.Fatalf("Unset: %v", err)
	}
	if got := itGS(t, "org.gnome.system.proxy", "mode"); got != "'none'" {
		t.Errorf("GNOME mode after Unset = %s", got)
	}
	if got := itKDE(t, "ProxyType"); got != "0" {
		t.Errorf("KDE ProxyType after Unset = %q", got)
	}
	if _, gerr := GetConfig(); gerr == nil {
		t.Error("GetConfig after Unset should report no proxy")
	}
}

// F-10 against the real tools: a previous PAC configuration with a bypass list
// must come back verbatim after WithProxy and WithProxyMulti.
func TestIntegrationLinux_WithProxyRestoresExactly(t *testing.T) {
	itRequireDesktopTools(t)
	itCleanup(t)
	itRun(t, "gsettings", "set", "org.gnome.system.proxy", "mode", "auto")
	itRun(t, "gsettings", "set", "org.gnome.system.proxy", "autoconfig-url", "http://pac.example.com/prev.pac")
	itRun(t, "gsettings", "set", "org.gnome.system.proxy", "ignore-hosts", "['localhost', '10.0.0.0/8', '*.corp.internal']")
	itRun(t, kwriteconfigBinary(), "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "2")
	itRun(t, kwriteconfigBinary(), "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "Proxy Config Script", "http://pac.example.com/prev.pac")
	before := itState(t)

	var inside string
	err := WithProxy(context.Background(), "http://tmp.example.com:9999", ScopeGlobal, func(context.Context) error {
		inside = itGS(t, "org.gnome.system.proxy", "mode")
		return nil
	})
	if err != nil && !IsNonCritical(err) {
		t.Fatalf("WithProxy: %v", err)
	}
	if inside != "'manual'" {
		t.Errorf("temporary proxy not active inside fn (mode %s)", inside)
	}
	itDiff(t, before, itState(t))

	err = WithProxyMulti(context.Background(), ProxyConfig{HTTP: "http://tmp.example.com:1", SOCKS: "socks5://tmp.example.com:2", NoProxy: "tmp.internal"}, ScopeGlobal,
		func(context.Context) error { return nil })
	if err != nil && !IsNonCritical(err) {
		t.Fatalf("WithProxyMulti: %v", err)
	}
	itDiff(t, before, itState(t))
}

// The invariant behind F-09: Set never reports success while the desktop
// setting did not change. Without a session bus (the typical SSH case from the
// VM logs) gsettings cannot reach dconf.
func TestIntegrationLinux_NoSilentFailureWithoutSessionBus(t *testing.T) {
	itRequireDesktopTools(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	err := SetContext(context.Background(), "http://proxy.example.com:8080", ScopeGlobal)
	t.Logf("Set without session bus: err = %v", err)
	out, gerr := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").CombinedOutput()
	t.Logf("gsettings get mode: %q err=%v", strings.TrimSpace(string(out)), gerr)
	applied := gerr == nil && strings.TrimSpace(string(out)) == "'manual'"
	if !applied && (err == nil || IsNonCritical(err)) {
		t.Fatalf("GNOME was not configured but Set reported success (err = %v)", err)
	}
}

// F-13/F-14/F-15 against the real tools. The expectations are about the
// outcome (what GetConfig reads back), not the commands used.
func TestIntegrationLinux_SchemeDefaultPortAndMulti(t *testing.T) {
	itRequireDesktopTools(t)
	itCleanup(t)
	ctx := context.Background()
	ok := func(err error) {
		t.Helper()
		if err != nil && !IsNonCritical(err) {
			t.Fatal(err)
		}
	}

	ok(SetContext(ctx, "socks5://sx.example.com", ScopeGlobal))
	cfg, err := GetConfig()
	if err != nil || cfg.SOCKS != "socks5://sx.example.com:1080" || cfg.HTTP != "" || cfg.HTTPS != "" {
		t.Errorf("after socks5 Set: %+v, %v (want only socks, default port 1080)", cfg, err)
	}
	if got, err := Get(); err != nil || got != "socks5://sx.example.com:1080" {
		t.Errorf("Get after socks5 Set = %q, %v", got, err)
	}

	ok(SetContext(ctx, "http://hx.example.com", ScopeGlobal))
	cfg, err = GetConfig()
	if err != nil || cfg.HTTP != "http://hx.example.com:80" || cfg.HTTPS != "http://hx.example.com:80" || cfg.SOCKS != "" {
		t.Errorf("after http Set: %+v, %v (want http+https on port 80, no socks)", cfg, err)
	}

	ok(UnsetContext(ctx, ScopeGlobal))
	want := ProxyConfig{HTTPS: "http://hs.example.com:3129", NoProxy: "localhost,10.0.0.0/8"}
	ok(SetMultiContext(ctx, want, ScopeGlobal))
	if got := itGS(t, "org.gnome.system.proxy", "mode"); got != "'manual'" {
		t.Errorf("mode after an HTTPS-only SetMulti = %s", got)
	}
	if cfg, err = GetConfig(); err != nil || cfg.HTTPS != want.HTTPS || cfg.NoProxy != want.NoProxy {
		t.Errorf("GetConfig after SetMulti = %+v, %v, want %+v", cfg, err, want)
	}
}
