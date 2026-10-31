//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeDesktop installs shell-script stand-ins for gsettings, kwriteconfig5 and
// kreadconfig5 that keep their state in files, and points /etc/environment at
// a temp file. Set FAKE_FAIL to a substring of a command line to make the
// matching call fail.
type fakeDesktop struct {
	dir string
	etc string
}

const fakeGsettings = `#!/bin/sh
db="$FAKE_DB/gs"; mkdir -p "$db"
echo "gsettings $*" >> "$FAKE_DB/calls"
case "$*" in *"$FAKE_FAIL"*) [ -n "$FAKE_FAIL" ] && { echo "boom" >&2; exit 1; };; esac
f="$db/$2.$3"
case "$1" in
list-keys) case "$2" in org.gnome.system.proxy) exit 0;; esac; exit 1;;
get) if [ -f "$f" ]; then cat "$f"; else
       case "$3" in mode) echo "'none'";; port) echo 0;; ignore-hosts) echo "@as []";; *) echo "''";; esac
     fi;;
set) [ -n "$FAKE_NOSTORE" ] && exit 0; printf '%s\n' "$4" > "$f";;
esac
`

const fakeKwrite = `#!/bin/sh
db="$FAKE_DB/kde"; mkdir -p "$db"
echo "$(basename "$0") $*" >> "$FAKE_DB/calls"
case "$*" in *"$FAKE_FAIL"*) [ -n "$FAKE_FAIL" ] && { echo "boom" >&2; exit 1; };; esac
key=""; del=0; val=""
while [ $# -gt 0 ]; do
  case "$1" in
    --file|--group) shift 2;;
    --key) key="$2"; shift 2;;
    --delete) del=1; shift;;
    *) val="$1"; shift;;
  esac
done
if [ $del = 1 ]; then rm -f "$db/$key"; else printf '%s' "$val" > "$db/$key"; fi
`

const fakeKread = `#!/bin/sh
db="$FAKE_DB/kde"
key=""
while [ $# -gt 0 ]; do case "$1" in --key) key="$2"; shift 2;; *) shift;; esac; done
[ -f "$db/$key" ] && cat "$db/$key"
echo
`

func installFakeDesktop(t *testing.T, tools ...string) *fakeDesktop {
	t.Helper()
	f := &fakeDesktop{dir: t.TempDir(), etc: filepath.Join(t.TempDir(), "environment")}
	bin := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	scripts := map[string]string{
		"gsettings":     fakeGsettings,
		"kwriteconfig5": fakeKwrite, "kreadconfig5": fakeKread,
		"kwriteconfig6": fakeKwrite, "kreadconfig6": fakeKread,
	}
	for _, name := range tools {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(scripts[name]), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	// Only the fake bin dir plus the system dirs holding sh/cat/mkdir; a real
	// gsettings in /usr/bin would be shadowed, but make sure none leaks in.
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_DB", f.dir)
	t.Setenv("FAKE_FAIL", "")
	t.Setenv("FAKE_NOSTORE", "")
	orig := etcEnvironmentPath
	etcEnvironmentPath = f.etc
	t.Cleanup(func() { etcEnvironmentPath = orig })
	for _, tool := range []string{"gsettings", "kwriteconfig5", "kreadconfig5", "kwriteconfig6", "kreadconfig6"} {
		if p, err := lookPathIn(bin, tool); err != nil || !strings.HasPrefix(p, bin) {
			if !contains(tools, tool) && isAvailable(tool) {
				t.Skipf("a real %s is in PATH and cannot be hidden", tool)
			}
		}
	}
	return f
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func lookPathIn(dir, name string) (string, error) {
	p := filepath.Join(dir, name)
	_, err := os.Stat(p)
	return p, err
}

func (f *fakeDesktop) gs(schema, key string) string {
	data, err := os.ReadFile(filepath.Join(f.dir, "gs", schema+"."+key)) //nolint:gosec
	if err != nil {                                                      // the schema default, as the fake gsettings reports it
		switch key {
		case "mode":
			return "'none'"
		case "port":
			return "0"
		case "ignore-hosts":
			return "@as []"
		}
		return "''"
	}
	return strings.TrimSpace(string(data))
}

func (f *fakeDesktop) kde(key string) string {
	data, err := os.ReadFile(filepath.Join(f.dir, "kde", key)) //nolint:gosec
	if err != nil {
		return "<unset>"
	}
	return string(data)
}

func (f *fakeDesktop) calls() string {
	data, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return string(data)
}

func TestLinuxSetGlobal_ConfiguresGnomeAndKDE(t *testing.T) {
	f := installFakeDesktop(t, "gsettings", "kwriteconfig5", "kreadconfig5")
	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if f.gs("org.gnome.system.proxy", "mode") != "manual" || f.gs("org.gnome.system.proxy.http", "port") != "8080" || f.gs("org.gnome.system.proxy.https", "port") != "8080" {
		t.Errorf("GNOME not configured; calls:\n%s", f.calls())
	}
	if f.kde("ProxyType") != "1" || f.kde("httpProxy") != "http://proxy.example.com:8080" {
		t.Errorf("KDE not configured; calls:\n%s", f.calls())
	}
	if _, err := os.Stat(f.etc); err != nil {
		t.Errorf("/etc/environment not written: %v", err)
	}
}

// F-09: a failing gsettings call used to be swallowed and Set reported success.
func TestLinuxSetGlobal_ReportsDesktopFailure(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	t.Setenv("FAKE_FAIL", "proxy.https host")
	p, _ := parse("http://alice:s3cr3t@proxy.example.com:8080")
	err := setGlobal(context.Background(), p)
	if err == nil {
		t.Fatalf("setGlobal hid a gsettings failure; calls:\n%s", f.calls())
	}
	if IsNonCritical(err) {
		t.Errorf("a failed gsettings call is a hard error, got non-critical: %v", err)
	}
	if !strings.Contains(err.Error(), "org.gnome.system.proxy.https") {
		t.Errorf("error does not name the failing key: %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("error leaks the password: %v", err)
	}
}

// A hard failure must not be downgraded to a warning because /etc/environment
// also could not be written.
func TestLinuxSetGlobal_HardErrorIsNotMaskedByEtcWarning(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	t.Setenv("FAKE_FAIL", "mode")
	if err := os.Mkdir(f.etc, 0o755); err != nil { //nolint:gosec
		t.Fatal(err) // a directory: writing it fails
	}
	p, _ := parse("http://proxy.example.com:8080")
	err := setGlobal(context.Background(), p)
	if err == nil || IsNonCritical(err) || RequiresElevation(err) {
		t.Fatalf("err = %v (non-critical=%v), want a hard error", err, IsNonCritical(err))
	}
}

func TestLinuxSetGlobal_OnlyEtcFailingIsAWarning(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	if err := os.Mkdir(f.etc, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	p, _ := parse("http://proxy.example.com:8080")
	err := setGlobal(context.Background(), p)
	if err == nil || !IsNonCritical(err) {
		t.Fatalf("err = %v, want a non-critical warning (desktop settings were applied)", err)
	}
	if f.gs("org.gnome.system.proxy", "mode") != "manual" {
		t.Error("GNOME settings should have been applied")
	}
}

func TestLinuxSetGlobal_NoDesktopTools(t *testing.T) {
	f := installFakeDesktop(t)
	p, _ := parse("http://proxy.example.com:8080")

	// Headless but root-like: /etc/environment is all there is, and it worked.
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatalf("headless Set with a writable /etc/environment should succeed: %v", err)
	}

	// Nothing could be applied at all.
	if err := os.Remove(f.etc); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.etc, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	err := setGlobal(context.Background(), p)
	if !errors.Is(err, ErrToolMissing) || IsNonCritical(err) {
		t.Fatalf("err = %v, want a hard ErrToolMissing", err)
	}
}

func TestLinuxPACAndMulti_NoDesktopToolsIsToolMissing(t *testing.T) {
	installFakeDesktop(t)
	if err := setGlobalPAC(context.Background(), "http://x/p.pac"); !errors.Is(err, ErrToolMissing) {
		t.Errorf("setGlobalPAC err = %v, want ErrToolMissing", err)
	}
	if err := setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://h:1"}); !errors.Is(err, ErrToolMissing) {
		t.Errorf("setGlobalMulti err = %v, want ErrToolMissing", err)
	}
}

func TestLinuxGsettingsWithoutGnomeSchemaIsSkipped(t *testing.T) {
	// gsettings exists (glib ships it on KDE too) but does not know the schema.
	f := installFakeDesktop(t, "gsettings", "kwriteconfig5")
	script := "#!/bin/sh\necho \"gsettings $*\" >> \"$FAKE_DB/calls\"\n[ \"$1\" = list-keys ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(f.dir, "bin", "gsettings"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatalf("a KDE system with a bare gsettings binary must not fail: %v", err)
	}
	if strings.Contains(f.calls(), "gsettings set") {
		t.Errorf("gsettings set should not run without the GNOME schema:\n%s", f.calls())
	}
	if f.kde("ProxyType") != "1" {
		t.Error("KDE should have been configured")
	}
}

// F-08: reading with a bare gsettings binary (no GNOME schema, as on a CI
// runner) is "not set", not a failure: the CLI would otherwise exit 1.
func TestLinuxGetWithoutGnomeSchemaIsNotSet(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	script := "#!/bin/sh\n[ \"$1\" = list-keys ] && exit 1\necho \"No such schema\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(f.dir, "bin", "gsettings"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if _, err := getGlobal(context.Background()); !errors.Is(err, ErrProxyNotSet) {
		t.Errorf("getGlobal = %v, want ErrProxyNotSet", err)
	}
	if _, err := getGlobalConfig(context.Background()); !errors.Is(err, ErrProxyNotSet) {
		t.Errorf("getGlobalConfig = %v, want ErrProxyNotSet", err)
	}
}

// F-06 analogue on Linux and F-10: WithProxy restores GNOME, KDE and
// /etc/environment exactly.
func TestLinuxWithProxy_RestoresExactly(t *testing.T) {
	f := installFakeDesktop(t, "gsettings", "kwriteconfig5", "kreadconfig5")
	// Previous state: a PAC in GNOME, a manual proxy in KDE, an admin's file.
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.dir, "gs", "org.gnome.system.proxy.mode"), "'auto'\n")
	write(filepath.Join(f.dir, "gs", "org.gnome.system.proxy.autoconfig-url"), "'http://pac.example.com/p.pac'\n")
	write(filepath.Join(f.dir, "gs", "org.gnome.system.proxy.ignore-hosts"), "['localhost', '10.0.0.0/8']\n")
	write(filepath.Join(f.dir, "kde", "ProxyType"), "2")
	write(filepath.Join(f.dir, "kde", "Proxy Config Script"), "http://pac.example.com/p.pac")
	write(f.etc, "PATH=/usr/bin\nNO_PROXY=mine.internal\n")
	if err := os.Chmod(f.etc, 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	snap := func() map[string]string {
		m := map[string]string{}
		for _, k := range gnomeKeys {
			m[k.schema+" "+k.key] = f.gs(k.schema, k.key)
		}
		for _, k := range kdeKeys {
			m["kde "+k] = f.kde(k)
		}
		data, _ := os.ReadFile(f.etc)
		m["etc"] = string(data)
		return m
	}
	before := snap()

	var inside map[string]string
	err := WithProxy(context.Background(), "http://tmp.example.com:9999", ScopeGlobal, func(context.Context) error {
		inside = snap()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside["org.gnome.system.proxy mode"] != "manual" || !strings.Contains(inside["etc"], "tmp.example.com") {
		t.Fatalf("temporary proxy not applied inside fn: %v", inside)
	}
	if after := snap(); !reflect.DeepEqual(after, before) {
		for k := range before {
			if before[k] != after[k] {
				t.Errorf("%s: after = %q, before = %q", k, after[k], before[k])
			}
		}
	}
	fi, _ := os.Stat(f.etc)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("/etc/environment mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestLinuxWithProxy_RemovesEtcEnvironmentItCreated(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	err := WithProxy(context.Background(), "http://tmp.example.com:9999", ScopeGlobal, func(context.Context) error {
		if _, err := os.Stat(f.etc); err != nil {
			t.Errorf("/etc/environment should exist inside fn: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.etc); !os.IsNotExist(err) {
		t.Errorf("/etc/environment should be gone again, stat err = %v", err)
	}
	if got := f.gs("org.gnome.system.proxy", "mode"); got != "'none'" && got != "none" {
		t.Errorf("mode after restore = %q, want 'none'", got)
	}
}

func TestLinuxSnapshot_FailsWhenGsettingsUnreadable(t *testing.T) {
	installFakeDesktop(t, "gsettings")
	t.Setenv("FAKE_FAIL", "get org.gnome.system.proxy.socks")
	if _, err := snapshotGlobal(context.Background()); err == nil {
		t.Fatal("snapshot must fail when a setting cannot be read")
	}
}

// Without write access to /etc/environment (non-root) nothing changed there, so
// restoring must not try to write it and must not report an error.
func TestLinuxRestore_NoEtcWriteWhenUnchanged(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	if err := os.WriteFile(f.etc, []byte("PATH=/usr/bin\n"), 0o444); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	restore, err := snapshotGlobal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(context.Background()); err != nil {
		t.Fatalf("restore of an unchanged read-only file failed: %v", err)
	}
}

// Found on a real VM: without a D-Bus session bus "gsettings set" exits 0 but
// stores nothing. Set must notice by reading the value back.
func TestLinuxSetGlobal_DetectsGsettingsThatDoesNotStore(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	t.Setenv("FAKE_NOSTORE", "1")
	p, _ := parse("http://proxy.example.com:8080")

	ops := map[string]func() error{
		"set": func() error { return setGlobal(context.Background(), p) },
		"unset": func() error {
			// Unset only fails to stick when something was stored to begin with.
			if err := os.MkdirAll(filepath.Join(f.dir, "gs"), 0o755); err != nil { //nolint:gosec
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.dir, "gs", "org.gnome.system.proxy.mode"), []byte("'manual'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(f.dir, "gs", "org.gnome.system.proxy.mode")) //nolint:errcheck
			return unsetGlobal(context.Background())
		},
		"pac":   func() error { return setGlobalPAC(context.Background(), "http://x/p.pac") },
		"multi": func() error { return setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://h:1"}) },
		"multi (https only)": func() error {
			return setGlobalMulti(context.Background(), ProxyConfig{HTTPS: "http://h:1"})
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			err := op()
			if err == nil || IsNonCritical(err) {
				t.Fatalf("err = %v, want a hard error (calls:\n%s)", err, f.calls())
			}
			if !strings.Contains(err.Error(), "not stored") {
				t.Errorf("error does not explain the cause: %v", err)
			}
		})
	}
}

func TestLinuxRestore_DetectsGsettingsThatDoesNotStore(t *testing.T) {
	installFakeDesktop(t, "gsettings")
	restore, err := snapshotGlobal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NOSTORE", "1")
	// Make the current mode differ from the snapshot so the check is meaningful.
	t.Setenv("FAKE_NOSTORE", "")
	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NOSTORE", "1")
	if err := restore(context.Background()); err == nil {
		t.Fatal("restore reported success although gsettings stored nothing")
	}
}

func gnomeCfg(t *testing.T) ProxyConfig {
	t.Helper()
	cfg, err := getGlobalConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// F-13: an HTTP proxy fills http/https, a SOCKS proxy fills socks only, and the
// previous Set is cleared so the two never mix.
func TestLinuxSetGlobal_SchemeDecidesTheField(t *testing.T) {
	f := installFakeDesktop(t, "gsettings", "kwriteconfig5", "kreadconfig5")
	ctx := context.Background()

	p, _ := parse("socks5://sx.example.com:1080")
	if err := setGlobal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if f.gs("org.gnome.system.proxy.socks", "host") != "sx.example.com" || f.gs("org.gnome.system.proxy.socks", "port") != "1080" {
		t.Errorf("GNOME socks not set; calls:\n%s", f.calls())
	}
	for _, proto := range []string{"http", "https"} {
		if h := f.gs("org.gnome.system.proxy."+proto, "host"); h != "" {
			t.Errorf("GNOME %s host = %s after a socks5 Set, want empty", proto, h)
		}
	}
	if f.kde("socksProxy") != "socks5://sx.example.com:1080" || f.kde("httpProxy") != "" || f.kde("httpsProxy") != "" {
		t.Errorf("KDE after socks5 Set: socks=%q http=%q https=%q", f.kde("socksProxy"), f.kde("httpProxy"), f.kde("httpsProxy"))
	}
	if got, err := getGlobal(ctx); err != nil || got != "socks5://sx.example.com:1080" {
		t.Errorf("getGlobal after socks5 Set = %q, %v", got, err)
	}

	p, _ = parse("http://hx.example.com:3128")
	if err := setGlobal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if h := f.gs("org.gnome.system.proxy.socks", "host"); h != "" {
		t.Errorf("GNOME socks host = %s after an http Set, want empty", h)
	}
	if f.gs("org.gnome.system.proxy.socks", "port") != "0" {
		t.Errorf("GNOME socks port = %s after an http Set, want 0", f.gs("org.gnome.system.proxy.socks", "port"))
	}
	if f.kde("socksProxy") != "" || f.kde("httpsProxy") != "http://hx.example.com:3128" {
		t.Errorf("KDE after http Set: socks=%q https=%q", f.kde("socksProxy"), f.kde("httpsProxy"))
	}
	if got, err := getGlobal(ctx); err != nil || got != "http://hx.example.com:3128" {
		t.Errorf("getGlobal after http Set = %q, %v", got, err)
	}
}

// F-14: no port means the scheme's default, and an IPv6 literal is read back
// with brackets.
func TestLinuxSetGlobal_DefaultPortAndIPv6(t *testing.T) {
	f := installFakeDesktop(t, "gsettings")
	ctx := context.Background()
	p, _ := parse("http://proxy.example.com")
	if err := setGlobal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if f.gs("org.gnome.system.proxy.http", "port") != "80" {
		t.Errorf("port = %s, want 80", f.gs("org.gnome.system.proxy.http", "port"))
	}
	p, _ = parse("http://[::1]:3128")
	if err := setGlobal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if got, err := getGlobal(ctx); err != nil || got != "http://[::1]:3128" {
		t.Errorf("getGlobal = %q, %v, want http://[::1]:3128", got, err)
	}
}

// F-15: any one of HTTP/HTTPS/SOCKS switches GNOME to manual mode, and NoProxy
// is stored in ignore-hosts so GetConfig returns what SetMulti was given.
func TestLinuxSetGlobalMulti_ManualModeAndNoProxy(t *testing.T) {
	for name, cfg := range map[string]ProxyConfig{
		"https only": {HTTPS: "http://h.example.com:3128", NoProxy: "localhost,10.0.0.0/8"},
		"socks only": {SOCKS: "socks5://s.example.com:1080", NoProxy: "localhost,10.0.0.0/8"},
		"all three": {
			HTTP: "http://h.example.com:3128", HTTPS: "http://hs.example.com:3129",
			SOCKS: "socks5://s.example.com:1080", NoProxy: "localhost,10.0.0.0/8",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := installFakeDesktop(t, "gsettings")
			if err := setGlobalMulti(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			if f.gs("org.gnome.system.proxy", "mode") != "manual" {
				t.Errorf("mode = %s, want manual; calls:\n%s", f.gs("org.gnome.system.proxy", "mode"), f.calls())
			}
			if got := gnomeCfg(t); !reflect.DeepEqual(got, cfg) {
				t.Errorf("GetConfig = %+v, want %+v", got, cfg)
			}
		})
	}
}

func TestLinuxSetGlobalMulti_NoProxyAloneKeepsMode(t *testing.T) {
	f := installFakeDesktop(t, "gsettings", "kwriteconfig5", "kreadconfig5")
	if err := setGlobalMulti(context.Background(), ProxyConfig{NoProxy: "a.example.com;b.example.com"}); err != nil {
		t.Fatal(err)
	}
	if f.gs("org.gnome.system.proxy", "mode") != "'none'" || f.kde("ProxyType") != "<unset>" {
		t.Errorf("mode changed by a NoProxy-only config; calls:\n%s", f.calls())
	}
	if got := f.gs("org.gnome.system.proxy", "ignore-hosts"); got != "['a.example.com', 'b.example.com']" {
		t.Errorf("ignore-hosts = %s", got)
	}
	if f.kde("NoProxyFor") != "a.example.com,b.example.com" {
		t.Errorf("NoProxyFor = %q", f.kde("NoProxyFor"))
	}
}

// F-16: Plasma 6 ships kwriteconfig6/kreadconfig6 only. Set used to find no
// write tool and silently do nothing.
func TestLinuxKDE_Plasma6ToolsOnly(t *testing.T) {
	f := installFakeDesktop(t, "kwriteconfig6", "kreadconfig6")
	ctx := context.Background()
	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(ctx, p); err != nil {
		t.Fatal(err)
	}
	if f.kde("ProxyType") != "1" || f.kde("httpProxy") != "http://proxy.example.com:8080" {
		t.Errorf("KDE not configured via the 6 tools; calls:\n%s", f.calls())
	}
	if !strings.Contains(f.calls(), "kwriteconfig6 ") || strings.Contains(f.calls(), "kwriteconfig5 ") {
		t.Errorf("wrong tool used:\n%s", f.calls())
	}
	if got, err := getGlobal(ctx); err != nil || got != "http://proxy.example.com:8080" {
		t.Errorf("getGlobal = %q, %v", got, err)
	}

	restore, err := snapshotGlobal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := unsetGlobal(ctx); err != nil {
		t.Fatal(err)
	}
	if f.kde("ProxyType") != "0" {
		t.Errorf("unset did not reach KDE: %q", f.kde("ProxyType"))
	}
	if err := restore(ctx); err != nil {
		t.Fatal(err)
	}
	if f.kde("ProxyType") != "1" || f.kde("httpProxy") != "http://proxy.example.com:8080" {
		t.Errorf("snapshot/restore with the 6 tools failed; calls:\n%s", f.calls())
	}
}

// With both generations installed (a Plasma 6 desktop with leftover 5
// packages), the newer tool is the one that is used.
func TestLinuxKDE_PrefersPlasma6(t *testing.T) {
	f := installFakeDesktop(t, "kwriteconfig5", "kreadconfig5", "kwriteconfig6", "kreadconfig6")
	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.calls(), "kwriteconfig5 ") || !strings.Contains(f.calls(), "kwriteconfig6 ") {
		t.Errorf("expected only kwriteconfig6:\n%s", f.calls())
	}
}

func TestLinuxNoKDEWriteTool_IsReported(t *testing.T) {
	installFakeDesktop(t, "kreadconfig6") // can read, cannot write
	err := setGlobalPAC(context.Background(), "http://pac.example.com/p.pac")
	if !errors.Is(err, ErrToolMissing) {
		t.Errorf("err = %v, want ErrToolMissing", err)
	}
}

// Found on a real Plasma 6 VM: unset left the stored URLs, credentials
// included, in kioslaverc.
func TestLinuxKDE_UnsetLeavesNoCredentialsBehind(t *testing.T) {
	f := installFakeDesktop(t, "kwriteconfig6", "kreadconfig6")
	ctx := context.Background()
	p, _ := parse("http://alice:s3cr3tpw@proxy.example.com:8080")
	// The warning that /etc/environment got no credentials is expected.
	if err := setGlobal(ctx, p); err != nil && !IsNonCritical(err) {
		t.Fatal(err)
	}
	if err := unsetGlobal(ctx); err != nil && !IsNonCritical(err) {
		t.Fatal(err)
	}
	for _, key := range []string{"httpProxy", "httpsProxy", "ftpProxy", "socksProxy"} {
		if got := f.kde(key); got != "" {
			t.Errorf("%s still holds %q after unset", key, got)
		}
	}
	if f.kde("ProxyType") != "0" {
		t.Errorf("ProxyType = %q, want 0", f.kde("ProxyType"))
	}
	if _, err := getGlobal(ctx); !errors.Is(err, ErrProxyNotSet) {
		t.Errorf("getGlobal after unset = %v, want ErrProxyNotSet", err)
	}
}
