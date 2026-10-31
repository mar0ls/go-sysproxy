//go:build windows

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func runReg(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", args...).CombinedOutput() //nolint:gosec
	return execError("reg", regWhat(args), err, out)
}

func runCmdkey(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(normalizeContext(ctx), "cmdkey", args...).CombinedOutput() //nolint:gosec
	// Never include the arguments: /pass: carries the password.
	return execError("cmdkey", "", err, out)
}

// regWhat describes a reg call for an error message: the verb and the value
// name, not the data.
func regWhat(args []string) string {
	what := ""
	if len(args) > 0 {
		what = args[0]
	}
	for i, a := range args {
		if strings.EqualFold(a, "/v") && i+1 < len(args) {
			what += " " + args[i+1]
		}
	}
	return what
}

// regBatch runs reg commands and collects failures: run records them, try only
// logs (deleting a value that does not exist fails and is fine).
type regBatch struct {
	ctx  context.Context
	errs []error
}

func (b *regBatch) run(args ...string) {
	if err := runReg(b.ctx, args...); err != nil {
		b.errs = append(b.errs, err)
	}
}

func (b *regBatch) try(args ...string) {
	if err := runReg(b.ctx, args...); err != nil {
		logf("windows: ignored: %v", err)
	}
}

// finish returns the registry failures as a hard error; otherwise warn (a
// failure of an optional step such as storing credentials) as non-critical.
func (b *regBatch) finish(warn error) error {
	if len(b.errs) > 0 {
		errs := b.errs
		if warn != nil {
			errs = append(errs, warn)
		}
		return errors.Join(errs...)
	}
	if warn != nil {
		return &nonCriticalError{err: warn}
	}
	return nil
}

func setGlobal(ctx context.Context, p *proxy) error {
	b := &regBatch{ctx: ctx}
	b.try("delete", regKey, "/v", "AutoConfigURL", "/f")
	b.run("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	b.run("add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", proxyServerValue(p), "/f")
	b.run("add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", "localhost;127.0.0.1;::1", "/f")
	var warn error
	if p.user != "" {
		if err := runCmdkey(ctx, "/add:"+p.host, "/user:"+p.user, "/pass:"+p.pass); err != nil {
			warn = fmt.Errorf("sysproxy: proxy credentials were not stored in Credential Manager: %w", err)
		}
	}
	refreshWinINET(ctx)
	return b.finish(warn)
}

// refreshWinINET asks WinINET to re-read the settings. It is best effort: the
// registry change is what matters.
func refreshWinINET(context.Context) {
	if err := notifyWinINET(); err != nil {
		logf("windows: ignored: %v", err)
	}
}

func unsetGlobal(ctx context.Context) error {
	b := &regBatch{ctx: ctx}
	host, _ := currentProxyHost(ctx)
	b.run("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	b.try("delete", regKey, "/v", "ProxyServer", "/f")
	b.try("delete", regKey, "/v", "ProxyOverride", "/f")
	b.try("delete", regKey, "/v", "AutoConfigURL", "/f")
	if host != "" {
		if err := runCmdkey(ctx, "/delete:"+host); err != nil {
			logf("windows: ignored: %v", err)
		}
	}
	return b.finish(nil)
}

// currentProxyHost reads the proxy host from the registry so Unset can clean
// up Credential Manager without requiring the caller to pass the original URL.
func currentProxyHost(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
	if err != nil {
		return "", err
	}
	if _, data, ok := parseRegQuery(string(out), "ProxyServer"); ok && data != "" {
		return proxyServerHost(data), nil
	}
	return "", fmt.Errorf("sysproxy: ProxyServer not found in registry")
}

func getGlobal(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyEnable").Output()
	if err != nil {
		// ProxyEnable missing → check PAC
		if pac, perr := readAutoConfigURL(ctx); perr == nil && pac != "" {
			return pac, nil
		}
		return "", ErrProxyNotEnabled
	}
	if !strings.Contains(string(out), "0x1") {
		if pac, perr := readAutoConfigURL(ctx); perr == nil && pac != "" {
			return pac, nil
		}
		return "", ErrProxyNotEnabled
	}
	out, err = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
	if err != nil {
		return "", fmt.Errorf("sysproxy: cannot read ProxyServer: %w", err)
	}
	server := extractRegValue(string(out), "ProxyServer")
	if server == "" {
		return "", ErrProxyNotSet
	}
	// First kind wins: http, https, then socks (so a proxy set from a socks5://
	// URL can be read back).
	if u := firstProxyURL(parseWindowsProxyServer(server)); u != "" {
		return u, nil
	}
	return "", ErrProxyNotSet
}

// readAutoConfigURL reads AutoConfigURL from the registry. A missing value
// returns "" and nil so callers can distinguish "no PAC" from a real error.
func readAutoConfigURL(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "AutoConfigURL").Output()
	if err != nil {
		return "", nil //nolint:nilerr
	}
	return extractRegValue(string(out), "AutoConfigURL"), nil
}

func setGlobalPAC(ctx context.Context, pacURL string) error {
	b := &regBatch{ctx: ctx}
	b.run("add", regKey, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", pacURL, "/f")
	b.run("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	refreshWinINET(ctx)
	return b.finish(nil)
}

func setGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	b := &regBatch{ctx: ctx}
	b.try("delete", regKey, "/v", "AutoConfigURL", "/f")
	b.run("add", regKey, "/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	var servers []string
	if cfg.HTTP != "" {
		servers = append(servers, "http="+hostPortFromURL(cfg.HTTP))
	}
	if cfg.HTTPS != "" {
		servers = append(servers, "https="+hostPortFromURL(cfg.HTTPS))
	}
	if cfg.SOCKS != "" {
		servers = append(servers, "socks="+hostPortFromURL(cfg.SOCKS))
	}
	if len(servers) > 0 {
		b.run("add", regKey, "/v", "ProxyServer", "/t", "REG_SZ", "/d", strings.Join(servers, ";"), "/f")
	}
	if bypass := splitNoProxy(cfg.NoProxy); len(bypass) > 0 {
		b.run("add", regKey, "/v", "ProxyOverride", "/t", "REG_SZ", "/d", proxyOverrideValue(bypass), "/f")
	}
	refreshWinINET(ctx)
	return b.finish(nil)
}

func getGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	pac, _ := readAutoConfigURL(ctx)

	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyEnable").Output()
	proxyEnabled := err == nil && strings.Contains(string(out), "0x1")

	if !proxyEnabled {
		if pac != "" {
			return ProxyConfig{PAC: pac}, nil
		}
		return ProxyConfig{}, ErrProxyNotEnabled
	}

	out, err = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyServer").Output()
	if err != nil {
		return ProxyConfig{}, fmt.Errorf("sysproxy: cannot read ProxyServer: %w", err)
	}
	server := extractRegValue(string(out), "ProxyServer")
	if server == "" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	cfg := parseWindowsProxyServer(server)
	cfg.PAC = pac

	out, _ = exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", "ProxyOverride").Output()
	cfg.NoProxy = extractRegValue(string(out), "ProxyOverride")

	return cfg, nil
}

func psLines(pairs []envPair) []string {
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		lines = append(lines, "$env:"+p.name+" = "+psQuote(p.value))
	}
	return lines
}

// applyProfileBlock writes body into every PowerShell profile, creating the
// profile directory and file if needed. It fails only when no profile could be
// written; partial failures are logged.
func applyProfileBlock(body []string, merge bool, yield ...string) error {
	profiles, err := powershellProfiles()
	if err != nil {
		return err
	}
	var errs []error
	written := 0
	for _, psProfile := range profiles {
		err := os.MkdirAll(filepath.Dir(psProfile), 0o700)
		if err == nil {
			err = applyBlockFile(psProfile, body, merge, yield...)
		}
		if err != nil {
			logf("windows: %s: %v", psProfile, err)
			errs = append(errs, fmt.Errorf("sysproxy: %w", err))
			continue
		}
		written++
	}
	if written == 0 {
		return errors.Join(errs...)
	}
	return nil
}

func setUser(proxyURL string) error {
	return applyProfileBlock(psLines([]envPair{
		{"http_proxy", proxyURL},
		{"HTTP_PROXY", proxyURL},
		{"https_proxy", proxyURL},
		{"HTTPS_PROXY", proxyURL},
		{"all_proxy", proxyURL},
		{"ALL_PROXY", proxyURL},
		{"no_proxy", defaultNoProxy},
		{"NO_PROXY", defaultNoProxy},
	}), false, noProxyNames...)
}

// unsetUser removes the go-sysproxy block (and unmarked lines written by
// versions <= 0.5) from the PowerShell profile. Other lines are never touched.
func unsetUser() error {
	profiles, err := powershellProfiles()
	if err != nil {
		return err
	}
	var errs []error
	for _, psProfile := range profiles {
		if err := removeBlockFile(psProfile); err != nil {
			errs = append(errs, fmt.Errorf("sysproxy: %w", err))
		}
	}
	return errors.Join(errs...)
}

// snapshotUser records the managed block of the PowerShell profile for
// WithProxy to restore.
func snapshotUser() (func() error, error) {
	profiles, err := powershellProfiles()
	if err != nil {
		return nil, err
	}
	snaps, err := snapshotBlockFiles(profiles)
	if err != nil {
		return nil, err
	}
	return func() error { return restoreBlockFiles(snaps) }, nil
}

func setUserPAC(pacURL string) error {
	return applyProfileBlock(psLines([]envPair{{"AUTOPROXY", pacURL}}), false)
}

func setUserMulti(cfg ProxyConfig) error {
	var pairs []envPair
	if cfg.HTTP != "" {
		pairs = append(pairs, envPair{"http_proxy", cfg.HTTP}, envPair{"HTTP_PROXY", cfg.HTTP})
	}
	if cfg.HTTPS != "" {
		pairs = append(pairs, envPair{"https_proxy", cfg.HTTPS}, envPair{"HTTPS_PROXY", cfg.HTTPS})
	}
	if cfg.SOCKS != "" {
		pairs = append(pairs, envPair{"all_proxy", cfg.SOCKS}, envPair{"ALL_PROXY", cfg.SOCKS})
	}
	if cfg.NoProxy != "" {
		pairs = append(pairs, envPair{"no_proxy", cfg.NoProxy}, envPair{"NO_PROXY", cfg.NoProxy})
	}
	if len(pairs) == 0 {
		return nil
	}
	return applyProfileBlock(psLines(pairs), true)
}

// regValue is one value of the Internet Settings key as WithProxy found it.
type regValue struct {
	name, typ, data string
	present         bool
}

// regValueNames are the values the package writes.
var regValueNames = []string{"ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL"}

func readRegValue(ctx context.Context, name string) (regValue, error) {
	out, err := exec.CommandContext(normalizeContext(ctx), "reg", "query", regKey, "/v", name).Output() //nolint:gosec
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return regValue{name: name}, nil // value does not exist
		}
		return regValue{}, execError("reg", "query "+name, err, nil)
	}
	typ, data, ok := parseRegQuery(string(out), name)
	if !ok {
		return regValue{}, fmt.Errorf("sysproxy: cannot parse reg query output for %s", name)
	}
	return regValue{name: name, typ: typ, data: data, present: true}, nil
}

func snapshotGlobal(ctx context.Context) (restoreFunc, error) {
	vals := make([]regValue, 0, len(regValueNames))
	for _, name := range regValueNames {
		v, err := readRegValue(ctx, name)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	var oldHost string
	for _, v := range vals {
		if v.name == "ProxyServer" && v.present {
			oldHost = proxyServerHost(v.data)
		}
	}
	return func(ctx context.Context) error {
		// Credentials stored for a temporary proxy must not outlive it.
		host, _ := currentProxyHost(ctx)
		b := &regBatch{ctx: ctx}
		for _, v := range vals {
			if v.present {
				b.run("add", regKey, "/v", v.name, "/t", v.typ, "/d", v.data, "/f")
			} else {
				b.try("delete", regKey, "/v", v.name, "/f")
			}
		}
		if host != "" && host != oldHost {
			if err := runCmdkey(ctx, "/delete:"+host); err != nil {
				logf("windows: ignored: %v", err)
			}
		}
		refreshWinINET(ctx)
		return b.finish(nil)
	}, nil
}

// windowsBackend implements globalBackend using the Windows registry and WinINET.
type windowsBackend struct{}

func (windowsBackend) SetGlobal(ctx context.Context, p *proxy) error { return setGlobal(ctx, p) }
func (windowsBackend) UnsetGlobal(ctx context.Context) error         { return unsetGlobal(ctx) }
func (windowsBackend) GetGlobal(ctx context.Context) (string, error) { return getGlobal(ctx) }
func (windowsBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	return getGlobalConfig(ctx)
}
func (windowsBackend) SetGlobalPAC(ctx context.Context, u string) error { return setGlobalPAC(ctx, u) }
func (windowsBackend) SetGlobalMulti(ctx context.Context, c ProxyConfig) error {
	return setGlobalMulti(ctx, c)
}

func (windowsBackend) SnapshotGlobal(ctx context.Context) (restoreFunc, error) {
	return snapshotGlobal(ctx)
}

func init() { activeBackend = windowsBackend{} }

func hostPortFromURL(rawURL string) string {
	host, port := endpointFromURL(rawURL)
	return net.JoinHostPort(host, port)
}

const psProfileName = "Microsoft.PowerShell_profile.ps1"

// powershellProfiles returns the current-user profiles to edit: Windows
// PowerShell 5's, and PowerShell 7's when its directory already exists (a
// directory is not created for a shell that is not installed).
func powershellProfiles() ([]string, error) {
	docs, err := documentsDir()
	if err != nil {
		return nil, err
	}
	profiles := []string{filepath.Join(docs, "WindowsPowerShell", psProfileName)}
	pwsh := filepath.Join(docs, "PowerShell")
	if fi, err := os.Stat(pwsh); err == nil && fi.IsDir() {
		profiles = append(profiles, filepath.Join(pwsh, psProfileName))
	}
	return profiles, nil
}

// documentsDir is the Documents folder profiles live in. It follows folder
// redirection (OneDrive) when USERPROFILE is the real profile directory; when
// USERPROFILE points elsewhere (a test, a service account) that wins.
func documentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if real, err := knownFolder(folderIDProfile); err == nil && strings.EqualFold(filepath.Clean(real), filepath.Clean(home)) {
		if docs, err := knownFolder(folderIDDocuments); err == nil && docs != "" {
			return docs, nil
		}
	}
	return filepath.Join(home, "Documents"), nil
}
