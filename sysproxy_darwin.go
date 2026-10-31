//go:build darwin

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
)

// networksetupRun and networksetupOutput are the only places that exec
// networksetup; tests replace them to record calls and fake output.
var (
	networksetupRun = func(ctx context.Context, args ...string) error {
		out, err := systemCommand(ctx, "networksetup", args...).CombinedOutput()
		return execError("networksetup", nsWhat(args), err, out)
	}
	networksetupOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		out, err := systemCommand(ctx, "networksetup", args...).Output()
		return out, execError("networksetup", nsWhat(args), err, out)
	}
	// routeDefault prints the default route; tests replace it.
	routeDefault = func(ctx context.Context) ([]byte, error) {
		return systemCommand(ctx, "route", "-n", "get", "default").Output()
	}
)

// nsWhat describes a networksetup call for an error message: the flag and the
// network service, never the remaining arguments (they may hold a password).
func nsWhat(args []string) string {
	switch len(args) {
	case 0:
		return ""
	case 1:
		return args[0]
	default:
		return fmt.Sprintf("%s %q", args[0], args[1])
	}
}

func runNetworkSetup(ctx context.Context, args ...string) error {
	return networksetupRun(ctx, args...)
}

// nsBatch runs networksetup commands and collects the failures: run records
// them, try only logs (for cosmetic steps whose failure does not change the
// effective proxy state).
type nsBatch struct {
	ctx  context.Context
	errs []error
}

func (b *nsBatch) run(args ...string) {
	if err := runNetworkSetup(b.ctx, args...); err != nil {
		b.errs = append(b.errs, err)
	}
}

func (b *nsBatch) try(args ...string) {
	if err := runNetworkSetup(b.ctx, args...); err != nil {
		logf("darwin: ignored: %v", err)
	}
}

func (b *nsBatch) err() error { return errors.Join(b.errs...) }

func setGlobal(ctx context.Context, p *proxy) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	// The scheme decides the field: a SOCKS proxy goes to the SOCKS firewall
	// proxy, an HTTP one to web + secure web. The other kind is switched off so
	// the result is exactly what was asked for, not a mix with an earlier Set.
	httpKinds, socksKinds := nsProxyKinds[:2], nsProxyKinds[2:]
	use, drop := httpKinds, socksKinds
	if p.isSOCKS() {
		use, drop = socksKinds, httpKinds
	}
	b := &nsBatch{ctx: ctx}
	for _, svc := range services {
		// Ensure manual proxy mode is authoritative and PAC does not stay active.
		b.run("-setautoproxystate", svc, "off")
		for _, k := range use {
			if p.user != "" {
				b.run(k.set, svc, p.host, p.port, "on", p.user, p.pass)
			} else {
				// "off" = no authentication; without it a previously configured
				// authenticated proxy would keep asking for credentials.
				b.run(k.set, svc, p.host, p.port, "off")
			}
			b.run(k.state, svc, "on")
		}
		for _, k := range drop {
			b.run(k.state, svc, "off")
		}
	}
	return b.err()
}

func unsetGlobal(ctx context.Context) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	b := &nsBatch{ctx: ctx}
	for _, svc := range services {
		// Clear host/port first so the previous values do not stay visible in
		// System Settings. The -set*proxy commands switch the proxy ON as a side
		// effect, so the state must be turned off afterwards — doing it in the
		// other order leaves an "enabled" proxy with an empty server on every
		// service. Clearing is cosmetic; turning the state off is what counts.
		b.try("-setwebproxy", svc, "", "0")
		b.try("-setsecurewebproxy", svc, "", "0")
		b.try("-setsocksfirewallproxy", svc, "", "0")
		// Unset clears both manual and auto-proxy modes.
		b.run("-setautoproxystate", svc, "off")
		b.run("-setwebproxystate", svc, "off")
		b.run("-setsecurewebproxystate", svc, "off")
		b.run("-setsocksfirewallproxystate", svc, "off")
	}
	return b.err()
}

// parseNSProxyOutput extracts host, port and enabled state from networksetup output.
func parseNSProxyOutput(output string) (host, port string, enabled bool) {
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "Enabled: Yes"):
			enabled = true
		case strings.HasPrefix(line, "Server:"):
			host = strings.TrimSpace(strings.TrimPrefix(line, "Server:"))
		case strings.HasPrefix(line, "Port:"):
			port = strings.TrimSpace(strings.TrimPrefix(line, "Port:"))
		}
	}
	return
}

func getGlobal(ctx context.Context) (string, error) {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return "", err
	}
	svc := primaryNetworkService(ctx, services)
	// First enabled kind wins: web, secure web, then SOCKS (so a proxy set from
	// a socks5:// URL can be read back).
	for i, k := range nsProxyKinds {
		out, err := networksetupOutput(ctx, k.get, svc)
		if err != nil {
			return "", err
		}
		h, p, ok := parseNSProxyOutput(string(out))
		if ok && h != "" && p != "0" {
			scheme := "http"
			if i == 2 {
				scheme = "socks5"
			}
			return scheme + "://" + net.JoinHostPort(h, p), nil
		}
	}
	if pac, ok := readMacPAC(ctx, svc); ok {
		return pac, nil
	}
	return "", ErrProxyNotSet
}

// readMacPAC returns the auto-proxy (PAC) URL for a network service when
// auto-proxy is on. The second return value is false when auto-proxy is off
// or the URL is empty.
func readMacPAC(ctx context.Context, svc string) (string, bool) {
	out, err := networksetupOutput(ctx, "-getautoproxyurl", svc)
	if err != nil {
		return "", false
	}
	return parseAutoProxyOutput(string(out))
}

// parseAutoProxyState extracts the URL and the enabled flag from
// `networksetup -getautoproxyurl` output. A "(null)" URL counts as empty.
func parseAutoProxyState(out string) (url string, enabled bool) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "URL:"):
			url = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
		case strings.HasPrefix(line, "Enabled: Yes"):
			enabled = true
		}
	}
	if url == "(null)" {
		url = ""
	}
	return url, enabled
}

// parseAutoProxyOutput extracts a PAC URL from `networksetup -getautoproxyurl`
// output. It returns ok=false when auto-proxy is disabled or the URL is a
// placeholder (empty, "(null)").
func parseAutoProxyOutput(out string) (string, bool) {
	url, enabled := parseAutoProxyState(out)
	if !enabled || url == "" {
		return "", false
	}
	return url, true
}

// parseBypassDomains parses `networksetup -getproxybypassdomains`. With an
// empty list the tool prints a sentence instead of nothing; it is not a domain.
func parseBypassDomains(out string) []string {
	var domains []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.Contains(l, "aren't any bypass domains") {
			continue
		}
		domains = append(domains, l)
	}
	return domains
}

func getGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return ProxyConfig{}, err
	}
	svc := primaryNetworkService(ctx, services)
	ctx = normalizeContext(ctx)

	var cfg ProxyConfig
	for _, q := range []struct {
		flag   string
		scheme string
		dest   *string
	}{
		{"-getwebproxy", "http", &cfg.HTTP},
		{"-getsecurewebproxy", "http", &cfg.HTTPS},
		{"-getsocksfirewallproxy", "socks5", &cfg.SOCKS},
	} {
		out, err := networksetupOutput(ctx, q.flag, svc)
		if err == nil {
			h, p, ok := parseNSProxyOutput(string(out))
			if ok && h != "" && p != "0" {
				*q.dest = q.scheme + "://" + h + ":" + p
			}
		}
	}

	out, err := networksetupOutput(ctx, "-getproxybypassdomains", svc)
	if err == nil {
		cfg.NoProxy = strings.Join(parseBypassDomains(string(out)), ",")
	}

	if pac, ok := readMacPAC(ctx, svc); ok {
		cfg.PAC = pac
	}

	if cfg.HTTP == "" && cfg.HTTPS == "" && cfg.SOCKS == "" && cfg.PAC == "" {
		return ProxyConfig{}, ErrProxyNotSet
	}
	return cfg, nil
}

func setGlobalPAC(ctx context.Context, pacURL string) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	b := &nsBatch{ctx: ctx}
	for _, svc := range services {
		// getGlobal prefers manual proxies over PAC, and macOS applies both when
		// both are enabled, so PAC mode must switch the manual ones off.
		b.run("-setwebproxystate", svc, "off")
		b.run("-setsecurewebproxystate", svc, "off")
		b.run("-setsocksfirewallproxystate", svc, "off")
		b.run("-setautoproxyurl", svc, pacURL)
		b.run("-setautoproxystate", svc, "on")
	}
	return b.err()
}

func setGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return err
	}
	b := &nsBatch{ctx: ctx}
	for _, svc := range services {
		// Multi-protocol manual config should disable PAC mode.
		b.run("-setautoproxystate", svc, "off")
		for i, u := range []string{cfg.HTTP, cfg.HTTPS, cfg.SOCKS} {
			if u == "" {
				continue
			}
			host, port := endpointFromURL(u)
			b.run(nsProxyKinds[i].set, svc, host, port)
			b.run(nsProxyKinds[i].state, svc, "on")
		}
		if domains := splitNoProxy(cfg.NoProxy); len(domains) > 0 {
			// One argument per domain: networksetup does not split on commas.
			b.run(append([]string{"-setproxybypassdomains", svc}, domains...)...)
		}
	}
	return b.err()
}

// nsProxyState is one manual proxy kind of a network service as networksetup
// reports it.
type nsProxyState struct {
	host, port    string
	enabled, auth bool
}

func parseNSProxyState(out string) nsProxyState {
	var st nsProxyState
	st.host, st.port, st.enabled = parseNSProxyOutput(out)
	st.auth = strings.Contains(out, "Authenticated Proxy Enabled: 1")
	return st
}

// nsServiceState is everything the package may change on one network service.
type nsServiceState struct {
	svc        string
	web        nsProxyState
	secureWeb  nsProxyState
	socks      nsProxyState
	pacURL     string
	pacEnabled bool
	bypass     []string
}

// nsProxyKinds pairs each manual proxy kind with its networksetup verbs.
var nsProxyKinds = []struct {
	get, set, state string
	field           func(*nsServiceState) *nsProxyState
}{
	{"-getwebproxy", "-setwebproxy", "-setwebproxystate", func(s *nsServiceState) *nsProxyState { return &s.web }},
	{"-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate", func(s *nsServiceState) *nsProxyState { return &s.secureWeb }},
	{"-getsocksfirewallproxy", "-setsocksfirewallproxy", "-setsocksfirewallproxystate", func(s *nsServiceState) *nsProxyState { return &s.socks }},
}

func readServiceState(ctx context.Context, svc string) (nsServiceState, error) {
	st := nsServiceState{svc: svc}
	for _, k := range nsProxyKinds {
		out, err := networksetupOutput(ctx, k.get, svc)
		if err != nil {
			return st, err
		}
		*k.field(&st) = parseNSProxyState(string(out))
	}
	out, err := networksetupOutput(ctx, "-getautoproxyurl", svc)
	if err != nil {
		return st, err
	}
	st.pacURL, st.pacEnabled = parseAutoProxyState(string(out))
	out, err = networksetupOutput(ctx, "-getproxybypassdomains", svc)
	if err != nil {
		return st, err
	}
	st.bypass = parseBypassDomains(string(out))
	return st, nil
}

// restoreServiceState makes svc match st exactly.
func restoreServiceState(ctx context.Context, st nsServiceState) error {
	b := &nsBatch{ctx: ctx}
	svc := st.svc
	for _, k := range nsProxyKinds {
		ps := *k.field(&st)
		switch {
		case ps.host != "" && ps.auth:
			// The password cannot be read back; leave the authentication flag as is.
			b.run(k.set, svc, ps.host, ps.port)
		case ps.host != "":
			b.run(k.set, svc, ps.host, ps.port, "off")
		default:
			b.run(k.set, svc, "", "0")
		}
		// -set*proxy turns the proxy on, so the state always comes last.
		b.run(k.state, svc, onOff(ps.enabled))
	}
	if st.pacURL != "" {
		b.run("-setautoproxyurl", svc, st.pacURL)
	}
	// An empty URL cannot be written back (networksetup rejects ""); switching
	// the state off is enough to make a leftover URL inert.
	b.run("-setautoproxystate", svc, onOff(st.pacEnabled && st.pacURL != ""))
	if len(st.bypass) > 0 {
		b.run(append([]string{"-setproxybypassdomains", svc}, st.bypass...)...)
	} else {
		b.run("-setproxybypassdomains", svc, "Empty")
	}
	return b.err()
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func snapshotGlobal(ctx context.Context) (restoreFunc, error) {
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		return nil, err
	}
	before := make([]nsServiceState, 0, len(services))
	for _, svc := range services {
		st, err := readServiceState(ctx, svc)
		if err != nil {
			return nil, fmt.Errorf("sysproxy: cannot snapshot proxy settings of %q: %w", svc, err)
		}
		before = append(before, st)
	}
	return func(ctx context.Context) error {
		var errs []error
		for _, want := range before {
			// Only touch services that actually differ, so unrelated ones are
			// never rewritten.
			if now, err := readServiceState(ctx, want.svc); err == nil && reflect.DeepEqual(now, want) {
				continue
			}
			if err := restoreServiceState(ctx, want); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}, nil
}

// darwinBackend implements globalBackend using macOS networksetup.
type darwinBackend struct{}

func (darwinBackend) SetGlobal(ctx context.Context, p *proxy) error { return setGlobal(ctx, p) }
func (darwinBackend) UnsetGlobal(ctx context.Context) error         { return unsetGlobal(ctx) }
func (darwinBackend) GetGlobal(ctx context.Context) (string, error) { return getGlobal(ctx) }
func (darwinBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	return getGlobalConfig(ctx)
}
func (darwinBackend) SetGlobalPAC(ctx context.Context, u string) error { return setGlobalPAC(ctx, u) }
func (darwinBackend) SetGlobalMulti(ctx context.Context, c ProxyConfig) error {
	return setGlobalMulti(ctx, c)
}

func (darwinBackend) SnapshotGlobal(ctx context.Context) (restoreFunc, error) {
	return snapshotGlobal(ctx)
}

func init() { activeBackend = darwinBackend{} }

func macOSNetworkServices(ctx context.Context) ([]string, error) {
	out, err := networksetupOutput(ctx, "-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var svcs []string
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, strings.TrimSpace(line))
	}
	if len(svcs) == 0 {
		return nil, fmt.Errorf("sysproxy: networksetup: no enabled network services found")
	}
	return svcs, nil
}

// primaryNetworkService picks the service Get reports on: the one that carries
// the default route, because Set writes to every service and the first one in
// the list is often an idle adapter. When the route or its service cannot be
// determined (no network, a VPN interface) it falls back to the first service.
func primaryNetworkService(ctx context.Context, services []string) string {
	out, err := routeDefault(ctx)
	if err != nil {
		logf("darwin: default route unavailable, using %q: %v", services[0], err)
		return services[0]
	}
	iface := parseDefaultInterface(string(out))
	if iface == "" {
		return services[0]
	}
	order, err := networksetupOutput(ctx, "-listnetworkserviceorder")
	if err != nil {
		logf("darwin: service order unavailable, using %q: %v", services[0], err)
		return services[0]
	}
	svc := serviceForDevice(string(order), iface)
	for _, s := range services {
		if s == svc {
			return svc
		}
	}
	return services[0]
}

// parseDefaultInterface extracts the interface of `route -n get default`.
func parseDefaultInterface(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "interface" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// serviceForDevice maps a device (en0) to its service name using the output of
// `networksetup -listnetworkserviceorder`: a "(N) Service" line followed by a
// "(Hardware Port: X, Device: en0)" line.
func serviceForDevice(out, device string) string {
	var name string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "(Hardware Port:"):
			if strings.HasSuffix(line, "Device: "+device+")") {
				return name
			}
		case strings.HasPrefix(line, "("):
			if _, rest, ok := strings.Cut(line, ") "); ok {
				name = strings.TrimSpace(rest)
			}
		}
	}
	return ""
}
