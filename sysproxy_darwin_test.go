//go:build darwin

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestParseAutoProxyOutput(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantURL string
		wantOK  bool
	}{
		{
			name:    "enabled with URL",
			input:   "URL: https://config.example.com/proxy.pac\nEnabled: Yes\n",
			wantURL: "https://config.example.com/proxy.pac",
			wantOK:  true,
		},
		{
			name:  "disabled",
			input: "URL: https://config.example.com/proxy.pac\nEnabled: No\n",
		},
		{
			name:  "(null) placeholder",
			input: "URL: (null)\nEnabled: Yes\n",
		},
		{
			name:  "empty",
			input: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, ok := parseAutoProxyOutput(c.input)
			if url != c.wantURL || ok != c.wantOK {
				t.Errorf("parseAutoProxyOutput(%q) = (%q, %v), want (%q, %v)",
					c.input, url, ok, c.wantURL, c.wantOK)
			}
		})
	}
}

func TestParseNSProxyOutput(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantHost    string
		wantPort    string
		wantEnabled bool
	}{
		{
			name:     "enabled with host and port",
			input:    "Enabled: Yes\nServer: proxy.example.com\nPort: 8080\n",
			wantHost: "proxy.example.com", wantPort: "8080", wantEnabled: true,
		},
		{
			name:     "disabled",
			input:    "Enabled: No\nServer: proxy.example.com\nPort: 8080\n",
			wantHost: "proxy.example.com", wantPort: "8080", wantEnabled: false,
		},
		{
			name:        "empty output",
			input:       "",
			wantHost:    "",
			wantPort:    "",
			wantEnabled: false,
		},
		{
			name:        "no server line",
			input:       "Enabled: Yes\nPort: 8080\n",
			wantHost:    "",
			wantPort:    "8080",
			wantEnabled: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, p, ok := parseNSProxyOutput(c.input)
			if h != c.wantHost || p != c.wantPort || ok != c.wantEnabled {
				t.Errorf("parseNSProxyOutput(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.input, h, p, ok, c.wantHost, c.wantPort, c.wantEnabled)
			}
		})
	}
}

// nsFakeProxy is one manual proxy kind of a fake network service.
type nsFakeProxy struct {
	host, port    string
	enabled, auth bool
}

type nsFakeService struct {
	web, secureWeb, socks nsFakeProxy
	pacURL                string
	pacEnabled            bool
	bypass                []string
}

// fakeNetworksetup is a stateful model of networksetup with the quirks that
// matter: every -set<kind>proxy call turns that proxy ON as a side effect,
// -setautoproxyurl "" is rejected, -setproxybypassdomains takes one argument
// per domain, "Empty" clears the list, and an empty list is printed as a
// sentence rather than nothing.
type fakeNetworksetup struct {
	order []string
	// devices maps a service to its device; route is the default-route
	// interface ("" = route fails).
	devices map[string]string
	route   string
	svc     map[string]*nsFakeService
	calls   []string
	// fail, when set, is consulted for every set command.
	fail func(args []string) error
}

func installFakeNetworksetup(t *testing.T, services ...string) *fakeNetworksetup {
	t.Helper()
	f := &fakeNetworksetup{order: services, svc: map[string]*nsFakeService{}}
	for _, s := range services {
		// networksetup reports an unconfigured proxy as empty host, port 0.
		f.svc[s] = &nsFakeService{web: nsFakeProxy{port: "0"}, secureWeb: nsFakeProxy{port: "0"}, socks: nsFakeProxy{port: "0"}}
	}
	origRun, origOut := networksetupRun, networksetupOutput
	t.Cleanup(func() { networksetupRun, networksetupOutput = origRun, origOut })
	origRoute := routeDefault
	t.Cleanup(func() { routeDefault = origRoute })
	routeDefault = func(context.Context) ([]byte, error) {
		if f.route == "" {
			return nil, errors.New("route: not in table")
		}
		return []byte("   route to: default\n    gateway: 192.168.50.1\n  interface: " + f.route + "\n"), nil
	}
	networksetupOutput = f.output
	networksetupRun = f.run
	return f
}

// kind returns the fake proxy a "web"/"secureweb"/"socksfirewall" verb refers to.
func (s *nsFakeService) kind(name string) *nsFakeProxy {
	switch name {
	case "web":
		return &s.web
	case "secureweb":
		return &s.secureWeb
	case "socksfirewall":
		return &s.socks
	}
	return nil
}

// on reports whether the proxy kind ("web", "secureweb", "socksfirewall",
// "auto") is enabled on svc.
func (f *fakeNetworksetup) on(kind, svc string) bool {
	s := f.svc[svc]
	if kind == "auto" {
		return s.pacEnabled
	}
	return s.kind(kind).enabled
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func (f *fakeNetworksetup) output(_ context.Context, args ...string) ([]byte, error) {
	if len(args) == 1 && args[0] == "-listnetworkserviceorder" {
		out := "An asterisk (*) denotes that a network service is disabled.\n"
		for i, s := range f.order {
			out += fmt.Sprintf("(%d) %s\n(Hardware Port: %s, Device: %s)\n\n", i+1, s, s, f.devices[s])
		}
		return []byte(out), nil
	}
	if len(args) == 1 && args[0] == "-listallnetworkservices" {
		out := "An asterisk (*) denotes that a network service is disabled.\n"
		return []byte(out + strings.Join(f.order, "\n") + "\n"), nil
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("fake: unexpected %v", args)
	}
	s := f.svc[args[1]]
	if s == nil {
		return nil, errors.New("** Error: Unable to find item in network database.")
	}
	switch args[0] {
	case "-getautoproxyurl":
		url := s.pacURL
		if url == "" {
			url = "(null)"
		}
		return []byte(fmt.Sprintf("URL: %s\nEnabled: %s\n", url, yesNo(s.pacEnabled))), nil
	case "-getproxybypassdomains":
		if len(s.bypass) == 0 {
			return []byte("There aren't any bypass domains set on this service.\n"), nil
		}
		return []byte(strings.Join(s.bypass, "\n") + "\n"), nil
	}
	for _, kind := range []string{"web", "secureweb", "socksfirewall"} {
		if args[0] == "-get"+kind+"proxy" {
			p := s.kind(kind)
			auth := "0"
			if p.auth {
				auth = "1"
			}
			port := p.port
			if port == "" {
				port = "0"
			}
			return []byte(fmt.Sprintf("Enabled: %s\nServer: %s\nPort: %s\nAuthenticated Proxy Enabled: %s\n",
				yesNo(p.enabled), p.host, port, auth)), nil
		}
	}
	return nil, fmt.Errorf("fake: unexpected %v", args)
}

func (f *fakeNetworksetup) run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.fail != nil {
		if err := f.fail(args); err != nil {
			return err
		}
	}
	if len(args) < 2 {
		return errors.New("fake: too few arguments")
	}
	flag, name := args[0], args[1]
	s := f.svc[name]
	if s == nil {
		return errors.New("** Error: Unable to find item in network database.")
	}
	for _, kind := range []string{"web", "secureweb", "socksfirewall"} {
		p := s.kind(kind)
		switch flag {
		case "-set" + kind + "proxy":
			if len(args) < 4 {
				return errors.New("** Error: The parameters were not valid.")
			}
			p.host, p.port = args[2], args[3]
			p.enabled = true // the quirk
			if len(args) >= 5 {
				p.auth = args[4] == "on"
			}
			return nil
		case "-set" + kind + "proxystate":
			p.enabled = args[2] == "on"
			return nil
		}
	}
	switch flag {
	case "-setautoproxyurl":
		if args[2] == "" {
			return errors.New("** Error: The parameters were not valid.")
		}
		s.pacURL, s.pacEnabled = args[2], true
	case "-setautoproxystate":
		s.pacEnabled = args[2] == "on"
	case "-setproxybypassdomains":
		if len(args) == 3 && args[2] == "Empty" {
			s.bypass = nil
		} else {
			s.bypass = append([]string(nil), args[2:]...)
		}
	default:
		return fmt.Errorf("fake: unexpected %v", args)
	}
	return nil
}

func (f *fakeNetworksetup) anyEnabled() []string {
	var on []string
	for _, name := range f.order {
		for _, kind := range []string{"web", "secureweb", "socksfirewall", "auto"} {
			if f.on(kind, name) {
				on = append(on, kind+"|"+name)
			}
		}
	}
	return on
}

func TestUnsetGlobal_LeavesNoProxyEnabled(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	// Start from a state where everything is enabled (e.g. after a Set).
	for _, name := range f.order {
		s := f.svc[name]
		s.web = nsFakeProxy{"p", "1", true, false}
		s.secureWeb = nsFakeProxy{"p", "1", true, false}
		s.socks = nsFakeProxy{"p", "1", true, false}
		s.pacURL, s.pacEnabled = "http://x/p.pac", true
	}

	if err := unsetGlobal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if on := f.anyEnabled(); len(on) > 0 {
		t.Errorf("still enabled after unsetGlobal: %v; calls:\n%s", on, strings.Join(f.calls, "\n"))
	}
}

func TestSetGlobal_ManualDisablesPAC(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi")
	f.svc["Wi-Fi"].pacURL, f.svc["Wi-Fi"].pacEnabled = "http://x/p.pac", true

	p, _ := parse("http://proxy.example.com:8080")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if f.on("auto", "Wi-Fi") {
		t.Error("PAC must be disabled after a manual Set")
	}
	for _, kind := range []string{"web", "secureweb"} {
		if !f.on(kind, "Wi-Fi") {
			t.Errorf("%s proxy should be enabled after Set", kind)
		}
	}
}

// F-13: the scheme decides the field. An HTTP proxy must not end up in the
// SOCKS field (and the other way round); the unused kind is switched off.
func TestSetGlobal_SchemeDecidesTheField(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	set := func(raw string) {
		t.Helper()
		p, err := parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := setGlobal(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	state := func(svc string) (web, secure, socks bool) {
		return f.on("web", svc), f.on("secureweb", svc), f.on("socksfirewall", svc)
	}

	set("socks5://sx.example.com:1080")
	for _, svc := range f.order {
		if web, secure, socks := state(svc); web || secure || !socks {
			t.Errorf("%s after socks5 Set: web=%v secure=%v socks=%v, want only socks", svc, web, secure, socks)
		}
		if s := f.svc[svc].socks; s.host != "sx.example.com" || s.port != "1080" {
			t.Errorf("%s socks = %+v", svc, s)
		}
	}
	if got, err := getGlobal(context.Background()); err != nil || got != "socks5://sx.example.com:1080" {
		t.Errorf("getGlobal after socks5 Set = %q, %v", got, err)
	}

	set("http://hx.example.com:3128")
	for _, svc := range f.order {
		if web, secure, socks := state(svc); !web || !secure || socks {
			t.Errorf("%s after http Set: web=%v secure=%v socks=%v, want only web+secure", svc, web, secure, socks)
		}
		if w := f.svc[svc].web; w.host != "hx.example.com" || w.port != "3128" {
			t.Errorf("%s web = %+v", svc, w)
		}
	}
	if got, err := getGlobal(context.Background()); err != nil || got != "http://hx.example.com:3128" {
		t.Errorf("getGlobal after http Set = %q, %v", got, err)
	}
}

// F-14: no port in the URL means the scheme's default, never an empty argument.
func TestSetGlobal_DefaultPortAndIPv6(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi")
	for raw, want := range map[string][2]string{
		"http://proxy.example.com":  {"proxy.example.com", "80"},
		"https://proxy.example.com": {"proxy.example.com", "443"},
		"http://[::1]:3128":         {"::1", "3128"},
	} {
		p, _ := parse(raw)
		if err := setGlobal(context.Background(), p); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if w := f.svc["Wi-Fi"].web; w.host != want[0] || w.port != want[1] {
			t.Errorf("%s: web = %+v, want %v", raw, w, want)
		}
	}

	p, _ := parse("socks5://sx.example.com")
	if err := setGlobal(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if s := f.svc["Wi-Fi"].socks; s.port != "1080" {
		t.Errorf("socks default port = %q, want 1080", s.port)
	}
	if err := setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://mx.example.com", HTTPS: "https://mx.example.com"}); err != nil {
		t.Fatal(err)
	}
	if w, sw := f.svc["Wi-Fi"].web, f.svc["Wi-Fi"].secureWeb; w.port != "80" || sw.port != "443" {
		t.Errorf("SetMulti default ports: web=%s secure=%s, want 80/443", w.port, sw.port)
	}
}

// F-06: PAC mode must switch the manual proxies off, otherwise both are active
// and getGlobal keeps reporting the manual ones.
func TestSetGlobalPAC_DisablesManualProxies(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	for _, name := range f.order {
		s := f.svc[name]
		s.web = nsFakeProxy{"p", "1", true, false}
		s.secureWeb = nsFakeProxy{"p", "1", true, false}
		s.socks = nsFakeProxy{"p", "1", true, false}
	}

	if err := setGlobalPAC(context.Background(), "http://pac.example.com/proxy.pac"); err != nil {
		t.Fatal(err)
	}
	for _, name := range f.order {
		for _, kind := range []string{"web", "secureweb", "socksfirewall"} {
			if f.on(kind, name) {
				t.Errorf("%s still enabled on %s after SetPAC", kind, name)
			}
		}
		if !f.on("auto", name) || f.svc[name].pacURL != "http://pac.example.com/proxy.pac" {
			t.Errorf("PAC not active on %s: %+v", name, f.svc[name])
		}
	}
	cfg, err := getGlobalConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PAC == "" || cfg.HTTP != "" || cfg.HTTPS != "" || cfg.SOCKS != "" {
		t.Errorf("GetConfig after SetPAC = %+v, want PAC only", cfg)
	}
}

// F-09: a failing networksetup call must surface as an error that names the
// operation and service but never the password.
func TestSetGlobal_ReportsFailureWithoutLeakingPassword(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	f.fail = func(args []string) error {
		if args[0] == "-setsecurewebproxy" && args[1] == "USB LAN" {
			return execError("networksetup", nsWhat(args), errors.New("exit status 4"), []byte("** Error: The parameters were not valid.\n"))
		}
		return nil
	}
	p, _ := parse("http://alice:s3cr3t@proxy.example.com:8080")
	err := setGlobal(context.Background(), p)
	if err == nil {
		t.Fatal("setGlobal reported success although a command failed")
	}
	for _, want := range []string{"-setsecurewebproxy", "USB LAN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("error leaks the password: %v", err)
	}
	// The other service must still have been configured.
	if !f.on("web", "Wi-Fi") {
		t.Error("a failure on one service must not stop the others")
	}
	if IsNonCritical(err) {
		t.Error("a failed networksetup call is a hard error")
	}
}

func TestNetworksetupFailuresPropagate(t *testing.T) {
	boom := errors.New("boom")
	ops := map[string]func(context.Context) error{
		"unset": unsetGlobal,
		"pac":   func(ctx context.Context) error { return setGlobalPAC(ctx, "http://x/p.pac") },
		"multi": func(ctx context.Context) error {
			return setGlobalMulti(ctx, ProxyConfig{HTTP: "http://h:1", NoProxy: "a,b"})
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			f := installFakeNetworksetup(t, "Wi-Fi")
			f.fail = func(args []string) error {
				if strings.HasSuffix(args[0], "state") {
					return boom
				}
				return nil
			}
			if name == "multi" {
				f.fail = func(args []string) error {
					if args[0] == "-setproxybypassdomains" {
						return boom
					}
					return nil
				}
			}
			if err := op(context.Background()); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the networksetup failure", err)
			}
		})
	}
}

func TestNetworkServices_NoneIsAnError(t *testing.T) {
	installFakeNetworksetup(t)
	p, _ := parse("http://h:1")
	if err := setGlobal(context.Background(), p); err == nil {
		t.Fatal("setGlobal with no network services must fail, not silently succeed")
	}
}

func TestNetworksetupMissingIsToolMissing(t *testing.T) {
	orig := networksetupOutput
	t.Cleanup(func() { networksetupOutput = orig })
	t.Setenv("PATH", t.TempDir())
	networksetupOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		out, err := exec.CommandContext(ctx, "networksetup", args...).Output() //nolint:gosec
		return out, execError("networksetup", nsWhat(args), err, out)
	}
	p, _ := parse("http://h:1")
	if err := setGlobal(context.Background(), p); !errors.Is(err, ErrToolMissing) {
		t.Fatalf("err = %v, want ErrToolMissing", err)
	}
}

// F-12: -setproxybypassdomains takes one argument per domain.
func TestSetGlobalMulti_NoProxyIsOneArgumentPerDomain(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi")
	err := setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://h:1", NoProxy: "localhost, 10.0.0.0/8;*.corp.internal"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"localhost", "10.0.0.0/8", "*.corp.internal"}
	if got := f.svc["Wi-Fi"].bypass; !reflect.DeepEqual(got, want) {
		t.Fatalf("bypass = %q, want %q (calls: %v)", got, want, f.calls)
	}
	cfg, err := getGlobalConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoProxy != "localhost,10.0.0.0/8,*.corp.internal" {
		t.Errorf("GetConfig NoProxy = %q", cfg.NoProxy)
	}
}

func TestParseBypassDomains(t *testing.T) {
	cases := map[string][]string{
		"": nil,
		"There aren't any bypass domains set on this service.\n": nil,
		"*.local\n169.254/16\n":                                  {"*.local", "169.254/16"},
		"  a.example.com \n\nb.example.com\n":                    {"a.example.com", "b.example.com"},
	}
	for in, want := range cases {
		if got := parseBypassDomains(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseBypassDomains(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetGlobalConfig_EmptyBypassListIsEmpty(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi")
	f.svc["Wi-Fi"].web = nsFakeProxy{"h", "1", true, false}
	cfg, err := getGlobalConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoProxy != "" {
		t.Errorf("NoProxy = %q: the 'no bypass domains' sentence was taken for a domain", cfg.NoProxy)
	}
}

// F-10: SnapshotGlobal/restore returns every service to exactly the state it
// was in, including services other than the first, disabled-but-configured
// proxies, authentication flags, PAC and the bypass list — and including
// fields that were empty.
func TestSnapshotRestore_IsExact(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN", "VPN")
	f.svc["Wi-Fi"].web = nsFakeProxy{"corp", "3128", true, false}
	f.svc["Wi-Fi"].bypass = []string{"corp.internal", "*.local"}
	f.svc["USB LAN"].secureWeb = nsFakeProxy{"old", "8443", false, false} // configured but off
	f.svc["USB LAN"].pacURL, f.svc["USB LAN"].pacEnabled = "http://pac/old.pac", false
	f.svc["VPN"].socks = nsFakeProxy{"s", "1080", true, true}
	before := map[string]nsFakeService{}
	for k, v := range f.svc {
		before[k] = *v
	}
	// The password of an authenticated proxy cannot be read back, so a Set with
	// "no authentication" in between loses the flag; everything else is exact.
	vpn := before["VPN"]
	vpn.socks.auth = false
	before["VPN"] = vpn

	restore, err := snapshotGlobal(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Temporary configuration of every kind.
	for _, raw := range []string{"http://tmp:9999", "socks5://tmp:9998"} {
		p, _ := parse(raw)
		if err := setGlobal(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://tmp:1", NoProxy: "tmp.internal"}); err != nil {
		t.Fatal(err)
	}
	if err := setGlobalPAC(context.Background(), "http://pac/tmp.pac"); err != nil {
		t.Fatal(err)
	}

	if err := restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, want := range before {
		got := *f.svc[name]
		// A leftover PAC URL is inert and cannot be cleared via networksetup.
		if want.pacURL == "" {
			got.pacURL = ""
		}
		// Credentials of a previously authenticated proxy cannot be restored,
		// but the host/port/state must be.
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s after restore = %+v, want %+v", name, got, want)
		}
	}
}

func TestSnapshotRestore_SkipsUnchangedServices(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	f.svc["Wi-Fi"].web = nsFakeProxy{"corp", "3128", true, false}
	restore, err := snapshotGlobal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("restore rewrote unchanged services: %v", f.calls)
	}
}

func TestSnapshotGlobal_FailsWhenStateUnreadable(t *testing.T) {
	installFakeNetworksetup(t, "Wi-Fi")
	orig := networksetupOutput
	t.Cleanup(func() { networksetupOutput = orig })
	networksetupOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[0] == "-getsocksfirewallproxy" {
			return nil, errors.New("denied")
		}
		return orig(ctx, args...)
	}
	if _, err := snapshotGlobal(context.Background()); err == nil {
		t.Fatal("snapshot must fail when the current state cannot be read")
	}
}

// The whole WithProxy path on the real darwin backend with the fake tool.
func TestWithProxy_Darwin_RestoresExactly(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi", "USB LAN")
	f.svc["Wi-Fi"].web = nsFakeProxy{"corp", "3128", true, false}
	f.svc["Wi-Fi"].bypass = []string{"corp.internal"}
	before := map[string]nsFakeService{"Wi-Fi": *f.svc["Wi-Fi"], "USB LAN": *f.svc["USB LAN"]}

	var inside nsFakeProxy
	err := WithProxy(context.Background(), "socks5://tmp:9999", ScopeGlobal, func(context.Context) error {
		inside = f.svc["Wi-Fi"].socks
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inside.enabled || inside.host != "tmp" {
		t.Errorf("temporary proxy was not active inside fn: %+v", inside)
	}
	for name, want := range before {
		got := *f.svc[name]
		got.pacURL = want.pacURL
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s after WithProxy = %+v, want %+v", name, got, want)
		}
	}
}

func TestParseDefaultInterface(t *testing.T) {
	out := "   route to: default\ndestination: default\n    gateway: 192.168.50.1\n  interface: en0\n      flags: <UP>\n"
	if got := parseDefaultInterface(out); got != "en0" {
		t.Fatalf("got %q", got)
	}
	if got := parseDefaultInterface("route: writing to routing socket: not in table\n"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestServiceForDevice(t *testing.T) {
	out := "An asterisk (*) denotes that a network service is disabled.\n" +
		"(1) USB 10/100 LAN\n(Hardware Port: USB 10/100 LAN, Device: en5)\n\n" +
		"(2) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en0)\n\n" +
		"(3) mikrus-wg\n(Hardware Port: com.wireguard.macos, Device: )\n"
	if got := serviceForDevice(out, "en0"); got != "Wi-Fi" {
		t.Fatalf("en0: got %q", got)
	}
	if got := serviceForDevice(out, "en5"); got != "USB 10/100 LAN" {
		t.Fatalf("en5: got %q", got)
	}
	if got := serviceForDevice(out, "en9"); got != "" {
		t.Fatalf("en9: got %q", got)
	}
}

// F-19: Get reported the first service (an idle adapter) while Set wrote to all.
func TestGet_ReadsTheServiceWithTheDefaultRoute(t *testing.T) {
	f := installFakeNetworksetup(t, "USB LAN", "Wi-Fi")
	f.devices = map[string]string{"USB LAN": "en5", "Wi-Fi": "en0"}
	f.route = "en0"
	f.svc["USB LAN"].pacEnabled, f.svc["USB LAN"].pacURL = true, "https://stale.example/proxy.pac"
	f.svc["Wi-Fi"].web = nsFakeProxy{enabled: true, host: "10.0.0.1", port: "3128"}

	got, err := getGlobal(context.Background())
	if err != nil || got != "http://10.0.0.1:3128" {
		t.Fatalf("Get = %q, %v; want the Wi-Fi proxy", got, err)
	}
	cfg, err := getGlobalConfig(context.Background())
	if err != nil || cfg.HTTP != "http://10.0.0.1:3128" || cfg.PAC != "" {
		t.Fatalf("GetConfig = %+v, %v", cfg, err)
	}
}

func TestGet_FallsBackToFirstServiceWithoutRoute(t *testing.T) {
	f := installFakeNetworksetup(t, "USB LAN", "Wi-Fi")
	f.devices = map[string]string{"USB LAN": "en5", "Wi-Fi": "en0"}
	f.svc["USB LAN"].web = nsFakeProxy{enabled: true, host: "10.0.0.2", port: "8080"}

	f.route = "" // route fails
	if got, err := getGlobal(context.Background()); err != nil || got != "http://10.0.0.2:8080" {
		t.Fatalf("no route: %q, %v", got, err)
	}
	f.route = "utun3" // VPN interface that is not a network service
	if got, err := getGlobal(context.Background()); err != nil || got != "http://10.0.0.2:8080" {
		t.Fatalf("vpn route: %q, %v", got, err)
	}
}

// GetConfig must produce URLs that parse, IPv6 literals included.
func TestGetGlobalConfig_IPv6HostsAreBracketed(t *testing.T) {
	f := installFakeNetworksetup(t, "Wi-Fi")
	f.svc["Wi-Fi"].web = nsFakeProxy{enabled: true, host: "::1", port: "8080"}
	f.svc["Wi-Fi"].socks = nsFakeProxy{enabled: true, host: "fd00::5", port: "1080"}
	cfg, err := getGlobalConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP != "http://[::1]:8080" || cfg.SOCKS != "socks5://[fd00::5]:1080" {
		t.Errorf("GetConfig = %+v", cfg)
	}
	if got, err := getGlobal(context.Background()); err != nil || got != cfg.HTTP {
		t.Errorf("Get = %q, %v; want the same URL as GetConfig", got, err)
	}
}
