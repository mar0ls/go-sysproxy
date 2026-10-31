//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// etcEnvironmentPath is a variable so tests can point it at a temp file.
var etcEnvironmentPath = "/etc/environment"

// etcEnvironmentResult classifies an /etc/environment error: lack of privileges
// is reported as RequiresElevation, anything else as non-critical (the desktop
// settings were already applied).
func etcEnvironmentResult(err error) error {
	if err == nil || IsNonCritical(err) {
		return err
	}
	if errors.Is(err, os.ErrPermission) {
		return asElevationError(fmt.Errorf("sysproxy: %s write failed (root required for system-wide effect): %w", etcEnvironmentPath, err))
	}
	return &nonCriticalError{err: err}
}

func runGsettings(ctx context.Context, args ...string) error {
	out, err := systemCommand(ctx, "gsettings", args...).CombinedOutput()
	return execError("gsettings", gsWhat(args), err, out)
}

func runKwriteconfig(ctx context.Context, args ...string) error {
	tool := kwriteconfigBinary()
	if tool == "" {
		return fmt.Errorf("%w: kwriteconfig6/kwriteconfig5", ErrToolMissing)
	}
	out, err := systemCommand(ctx, tool, args...).CombinedOutput()
	return execError(tool, kdeWhat(args), err, out)
}

// gsWhat / kdeWhat describe a call for an error message without its value
// argument, which may be a proxy URL with credentials.
func gsWhat(args []string) string {
	if len(args) >= 3 {
		return strings.Join(args[:3], " ")
	}
	return strings.Join(args, " ")
}

func kdeWhat(args []string) string {
	for i, a := range args {
		if a == "--key" && i+1 < len(args) {
			return "--key " + args[i+1]
		}
	}
	return ""
}

const gnomeProxySchema = "org.gnome.system.proxy"

// gnomeUsable reports whether gsettings exists and knows the GNOME proxy
// schema. gsettings alone is not enough: it ships with glib on KDE systems too.
func gnomeUsable(ctx context.Context) bool {
	return hasSystemTool("gsettings") && runGsettings(ctx, "list-keys", gnomeProxySchema) == nil
}

// kdeWritable reports whether KDE settings can be both written and read back.
// Set changes the KDE keys only when WithProxy can also save and restore them,
// so a kwriteconfig without its kreadconfig counts as no KDE at all.
func kdeWritable() bool { return kwriteconfigBinary() != "" && kreadconfigBinary() != "" }

// kdeSet writes one key of the "Proxy Settings" group of kioslaverc.
func kdeSet(ctx context.Context, key, value string) error {
	return runKwriteconfig(ctx, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key, value)
}

// steps collects the failures of a sequence of backend steps.
type steps struct {
	ctx  context.Context
	errs []error
	// restoring marks steps that put a saved state back: they never add the
	// "written for root" sudo warning, which is about what a Set changed.
	restoring bool
}

func (s *steps) gs(args ...string) {
	if err := runGsettings(s.ctx, args...); err != nil {
		s.errs = append(s.errs, err)
	}
}

func (s *steps) kde(key, value string) {
	if err := kdeSet(s.ctx, key, value); err != nil {
		s.errs = append(s.errs, err)
	}
}

// verify reads a gsettings key back and compares it with want. "gsettings set"
// exits 0 even when no dconf service is reachable (no D-Bus session bus, e.g.
// over plain SSH or sudo): it then falls back to a throw-away in-memory
// backend and the change vanishes with the process. Only a read-back from a
// new process proves the value was stored.
func (s *steps) verify(schema, key, want string) {
	out, err := systemCommand(s.ctx, "gsettings", "get", schema, key).Output()
	got := unquoteGVariant(string(out))
	if err != nil || got != unquoteGVariant(want) {
		s.errs = append(s.errs, fmt.Errorf(
			"sysproxy: gsettings accepted %s %s=%s but it reads back as %q: the setting was not stored "+
				"(is a D-Bus session bus and the dconf service reachable? run it from the desktop session, not via plain sudo/ssh)",
			schema, key, want, got))
	}
}

func unquoteGVariant(v string) string { return strings.Trim(strings.TrimSpace(v), "'") }

// finish combines the desktop failures with the /etc/environment outcome etcErr
// (nil when the operation does not touch that file). desktop says whether any
// desktop tool took part.
//
//   - a desktop step failed: hard error (the /etc/environment problem is folded
//     in as text so the result cannot be mistaken for a warning);
//   - only /etc/environment failed: that warning, as non-critical;
//   - everything worked but the process runs as root through sudo: a
//     non-critical warning that the desktop settings are root's, not the user's;
//   - nothing could run at all: ErrToolMissing.
func (s *steps) finish(desktop bool, etcErr error, what string) error {
	if len(s.errs) > 0 {
		errs := s.errs
		if etcErr != nil {
			errs = append(errs, flatten(etcErr))
		}
		return errors.Join(errs...)
	}
	if !desktop {
		if etcErr != nil {
			return fmt.Errorf("%w: neither gsettings (GNOME) nor kwriteconfig6/5 (KDE) found, and %s: %v", ErrToolMissing, what, etcErr)
		}
		return nil
	}
	if s.restoring || (etcErr != nil && !IsNonCritical(etcErr)) {
		return etcErr
	}
	return combine(etcErr, sudoDesktopWarning())
}

// noDesktopTool is the error for operations that only have desktop settings to
// change when none of the tools exists.
func noDesktopTool() error {
	return fmt.Errorf("%w: neither gsettings (GNOME) nor kwriteconfig6/5 (KDE) found", ErrToolMissing)
}

func setGlobal(ctx context.Context, p *proxy) error {
	st := &steps{ctx: ctx}
	gnome, kde := gnomeUsable(ctx), kdeWritable()
	// The scheme decides the field: an HTTP proxy goes to http/https(/ftp), a
	// SOCKS one to socks only. The other fields are cleared so the result is
	// exactly what was asked for, not a mix with an earlier Set.
	if gnome {
		st.gs("set", gnomeProxySchema, "mode", "manual")
		for _, proto := range []string{"http", "https", "socks"} {
			schema := gnomeProxySchema + "." + proto
			if (proto == "socks") == p.isSOCKS() {
				st.gs("set", schema, "host", p.host)
				st.gs("set", schema, "port", p.port)
			} else {
				st.gs("set", schema, "host", "")
				st.gs("set", schema, "port", "0")
			}
		}
		st.verify(gnomeProxySchema, "mode", "manual")
	}
	if kde {
		st.kde("ProxyType", "1")
		for _, key := range []string{"httpProxy", "httpsProxy", "ftpProxy", "socksProxy"} {
			if (key == "socksProxy") == p.isSOCKS() {
				st.kde(key, p.rawURL)
			} else {
				st.kde(key, "")
			}
		}
	}
	etcErr := etcEnvironmentResult(writeEtcEnvironment(etcEnvironmentPath, p.rawURL))
	return st.finish(gnome || kde, etcErr, etcEnvironmentPath+" could not be written")
}

func unsetGlobal(ctx context.Context) error {
	st := &steps{ctx: ctx}
	gnome, kde := gnomeUsable(ctx), kdeWritable()
	if gnome {
		st.gs("set", gnomeProxySchema, "mode", "none")
		st.verify(gnomeProxySchema, "mode", "none")
	}
	if kde {
		st.kde("ProxyType", "0")
		// ProxyType=0 already disables the proxy; the stored URLs may carry
		// credentials, so they are not left behind either.
		for _, key := range []string{"httpProxy", "httpsProxy", "ftpProxy", "socksProxy", "Proxy Config Script"} {
			st.kde(key, "")
		}
	}
	etcErr := etcEnvironmentResult(clearEtcEnvironment(etcEnvironmentPath))
	return st.finish(gnome || kde, etcErr, etcEnvironmentPath+" could not be cleared")
}

func getGlobal(ctx context.Context) (string, error) {
	if gnomeUsable(ctx) {
		if url, err := getGlobalGnome(ctx); err == nil {
			return url, nil
		} else if !errors.Is(err, ErrProxyNotSet) {
			return "", err
		}
	}
	if kreadconfigBinary() != "" {
		if url, err := getGlobalKDE(ctx); err == nil {
			return url, nil
		} else if !errors.Is(err, ErrProxyNotSet) {
			return "", err
		}
	}
	return "", ErrProxyNotSet
}

func getGlobalGnome(ctx context.Context) (string, error) {
	out, err := systemCommand(ctx, "gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	if err != nil {
		return "", fmt.Errorf("sysproxy: gsettings mode: %w", err)
	}
	mode := strings.Trim(strings.TrimSpace(string(out)), "'")
	if mode != "manual" {
		return "", ErrProxyNotSet
	}
	// First usable kind wins: http, https, then socks (so a proxy set from a
	// socks5:// URL can be read back).
	for _, q := range []struct{ proto, scheme string }{{"http", "http"}, {"https", "http"}, {"socks", "socks5"}} {
		schema := gnomeProxySchema + "." + q.proto
		host, err1 := systemCommand(ctx, "gsettings", "get", schema, "host").Output()
		port, err2 := systemCommand(ctx, "gsettings", "get", schema, "port").Output()
		if err1 != nil || err2 != nil {
			return "", fmt.Errorf("sysproxy: cannot read proxy settings")
		}
		h := strings.Trim(strings.TrimSpace(string(host)), "'")
		p := strings.TrimSpace(string(port))
		if h != "" && p != "0" {
			return q.scheme + "://" + joinHostPort(h, p), nil
		}
	}
	return "", ErrProxyNotSet
}

// gsettingsField reads a single string field from a gsettings schema.
func gsettingsField(ctx context.Context, schema, key string) string {
	out, err := systemCommand(ctx, "gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'")
}

// parseGsettingsArray converts a GLib array string like ['a', 'b'] to "a,b".
func parseGsettingsArray(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), "[]")
	var parts []string
	for _, item := range strings.Split(s, ",") {
		if v := strings.Trim(strings.TrimSpace(item), "'"); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ",")
}

func getGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	if gnomeUsable(ctx) {
		cfg, err := getGlobalConfigGnome(ctx)
		if err == nil {
			return cfg, nil
		}
		if !errors.Is(err, ErrProxyNotSet) {
			return ProxyConfig{}, err
		}
	}
	if kreadconfigBinary() != "" {
		cfg, err := getGlobalConfigKDE(ctx)
		if err == nil {
			return cfg, nil
		}
		if !errors.Is(err, ErrProxyNotSet) {
			return ProxyConfig{}, err
		}
	}
	return ProxyConfig{}, ErrProxyNotSet
}

func getGlobalConfigGnome(ctx context.Context) (ProxyConfig, error) {
	mode := gsettingsField(ctx, "org.gnome.system.proxy", "mode")
	switch mode {
	case "auto":
		pac := gsettingsField(ctx, "org.gnome.system.proxy", "autoconfig-url")
		if pac == "" {
			return ProxyConfig{}, ErrProxyNotSet
		}
		return ProxyConfig{PAC: pac}, nil
	case "manual":
		// handled below
	default:
		return ProxyConfig{}, ErrProxyNotSet
	}

	var cfg ProxyConfig
	for _, q := range []struct {
		schema string
		scheme string
		dest   *string
	}{
		{"org.gnome.system.proxy.http", "http", &cfg.HTTP},
		{"org.gnome.system.proxy.https", "http", &cfg.HTTPS},
		{"org.gnome.system.proxy.socks", "socks5", &cfg.SOCKS},
	} {
		h := gsettingsField(ctx, q.schema, "host")
		p := gsettingsField(ctx, q.schema, "port")
		if h != "" && p != "0" {
			*q.dest = q.scheme + "://" + joinHostPort(h, p)
		}
	}

	raw, _ := systemCommand(ctx, "gsettings", "get", "org.gnome.system.proxy", "ignore-hosts").Output()
	cfg.NoProxy = parseGsettingsArray(string(raw))

	if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	return cfg, nil
}

func setGlobalPAC(ctx context.Context, pacURL string) error {
	st := &steps{ctx: ctx}
	gnome, kde := gnomeUsable(ctx), kdeWritable()
	if !gnome && !kde {
		return noDesktopTool()
	}
	if gnome {
		st.gs("set", gnomeProxySchema, "mode", "auto")
		st.gs("set", gnomeProxySchema, "autoconfig-url", pacURL)
		st.verify(gnomeProxySchema, "mode", "auto")
	}
	if kde {
		st.kde("ProxyType", "2")
		st.kde("Proxy Config Script", pacURL)
	}
	return st.finish(true, nil, "")
}

func setGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	st := &steps{ctx: ctx}
	gnome, kde := gnomeUsable(ctx), kdeWritable()
	if !gnome && !kde {
		return noDesktopTool()
	}
	anyProxy := cfg.HTTP != "" || cfg.HTTPS != "" || cfg.SOCKS != ""
	bypass := splitNoProxy(cfg.NoProxy)
	if gnome {
		if anyProxy {
			st.gs("set", gnomeProxySchema, "mode", "manual")
		}
		for _, f := range []struct{ proto, url string }{{"http", cfg.HTTP}, {"https", cfg.HTTPS}, {"socks", cfg.SOCKS}} {
			if f.url == "" {
				continue
			}
			host, port := endpointFromURL(f.url)
			st.gs("set", gnomeProxySchema+"."+f.proto, "host", host)
			st.gs("set", gnomeProxySchema+"."+f.proto, "port", port)
		}
		if len(bypass) > 0 {
			st.gs("set", gnomeProxySchema, "ignore-hosts", gvariantStrings(bypass))
		}
		if anyProxy {
			st.verify(gnomeProxySchema, "mode", "manual")
		}
	}
	if kde {
		if anyProxy {
			st.kde("ProxyType", "1")
		}
		if cfg.HTTP != "" {
			st.kde("httpProxy", cfg.HTTP)
		}
		if cfg.HTTPS != "" {
			st.kde("httpsProxy", cfg.HTTPS)
		}
		if cfg.SOCKS != "" {
			st.kde("socksProxy", cfg.SOCKS)
		}
		if len(bypass) > 0 {
			st.kde("NoProxyFor", strings.Join(bypass, ","))
		}
	}
	return st.finish(true, nil, "")
}

// gvariantStrings formats list as a GVariant string array, the form gsettings
// takes for ignore-hosts. validateNoProxy has already ruled out quotes and
// backslashes, so plain single-quoting is safe.
func gvariantStrings(list []string) string {
	return "['" + strings.Join(list, "', '") + "']"
}

// gnomeKeys are the gsettings keys the package writes (plus ignore-hosts, the
// bypass list). WithProxy saves and restores them verbatim, in GVariant text.
var gnomeKeys = []struct{ schema, key string }{
	{gnomeProxySchema, "mode"},
	{gnomeProxySchema, "autoconfig-url"},
	{gnomeProxySchema, "ignore-hosts"},
	{gnomeProxySchema + ".http", "host"},
	{gnomeProxySchema + ".http", "port"},
	{gnomeProxySchema + ".https", "host"},
	{gnomeProxySchema + ".https", "port"},
	{gnomeProxySchema + ".socks", "host"},
	{gnomeProxySchema + ".socks", "port"},
}

// kdeKeys are the kioslaverc "Proxy Settings" keys the package writes.
var kdeKeys = []string{
	"ProxyType", "httpProxy", "httpsProxy", "ftpProxy", "socksProxy",
	"NoProxyFor", "Proxy Config Script",
}

// keyValue is one saved setting: a gsettings "schema key" or a kioslaverc key.
type keyValue struct {
	key, value string
}

// etcState is /etc/environment as WithProxy found it.
type etcState struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func readEtcState(path string) (etcState, error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return etcState{}, nil
	}
	if err != nil {
		return etcState{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return etcState{}, err
	}
	return etcState{exists: true, data: data, mode: fi.Mode().Perm()}, nil
}

// restoreEtcState puts /etc/environment back, writing only if it changed.
func restoreEtcState(path string, want etcState) error {
	now, err := readEtcState(path)
	if err != nil {
		return err
	}
	if now.exists == want.exists && string(now.data) == string(want.data) {
		return nil
	}
	if !want.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, want.data, want.mode)
}

func kdeRead(ctx context.Context, key string) (string, error) {
	tool := kreadconfigBinary()
	if tool == "" {
		return "", ErrToolMissing
	}
	out, err := systemCommand(ctx, tool, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key).Output()
	if err != nil {
		return "", execError(tool, "--key "+key, err, nil)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func snapshotGlobal(ctx context.Context) (restoreFunc, error) {
	var gs []keyValue // value is the raw GVariant text
	if gnomeUsable(ctx) {
		for _, k := range gnomeKeys {
			out, err := systemCommand(ctx, "gsettings", "get", k.schema, k.key).Output()
			if err != nil {
				return nil, execError("gsettings", "get "+k.schema+" "+k.key, err, nil)
			}
			gs = append(gs, keyValue{k.schema + " " + k.key, strings.TrimSpace(string(out))})
		}
	}
	var kde []keyValue
	if kdeWritable() {
		for _, key := range kdeKeys {
			v, err := kdeRead(ctx, key)
			if err != nil {
				return nil, err
			}
			kde = append(kde, keyValue{key, v})
		}
	}
	etc, err := readEtcState(etcEnvironmentPath)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context) error {
		st := &steps{ctx: ctx, restoring: true}
		for _, v := range gs {
			schema, key, _ := strings.Cut(v.key, " ")
			st.gs("set", schema, key, v.value)
			if schema == gnomeProxySchema && key == "mode" {
				st.verify(schema, key, v.value)
			}
		}
		for _, v := range kde {
			if v.value == "" {
				st.errs = appendIf(st.errs, runKwriteconfig(ctx, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", v.key, "--delete"))
				continue
			}
			st.kde(v.key, v.value)
		}
		return st.finish(true, etcEnvironmentResult(restoreEtcState(etcEnvironmentPath, etc)), etcEnvironmentPath+" could not be restored")
	}, nil
}

func appendIf(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}

// linuxBackend implements globalBackend using gsettings/kwriteconfig6|5 and /etc/environment.
type linuxBackend struct{}

func (linuxBackend) SetGlobal(ctx context.Context, p *proxy) error { return setGlobal(ctx, p) }
func (linuxBackend) UnsetGlobal(ctx context.Context) error         { return unsetGlobal(ctx) }
func (linuxBackend) GetGlobal(ctx context.Context) (string, error) { return getGlobal(ctx) }
func (linuxBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	return getGlobalConfig(ctx)
}
func (linuxBackend) SetGlobalPAC(ctx context.Context, u string) error { return setGlobalPAC(ctx, u) }
func (linuxBackend) SetGlobalMulti(ctx context.Context, c ProxyConfig) error {
	return setGlobalMulti(ctx, c)
}

func (linuxBackend) SnapshotGlobal(ctx context.Context) (restoreFunc, error) {
	return snapshotGlobal(ctx)
}

func init() { activeBackend = linuxBackend{} }

// sudoDesktopWarning is the non-critical warning for desktop settings that
// were written for root's account. It is nil outside sudo.
func sudoDesktopWarning() error {
	caller := sudoCaller()
	if caller == "" {
		return nil
	}
	return &nonCriticalError{err: fmt.Errorf(
		"sysproxy: running as root through sudo (SUDO_USER=%s): the GNOME/KDE settings were written for root's account, "+
			"not for %s's desktop session; run without sudo to change the user's own settings", caller, caller)}
}
