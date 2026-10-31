//go:build darwin

package sysproxy

import (
	"context"
	"reflect"
	"testing"
)

// TestIntegrationDarwin_WithProxyRestoresExactly runs WithProxy against the
// real networksetup and compares every network service before and after. It
// changes the proxy of all services for a moment, so it is opt-in:
//
//	SYSPROXY_INTEGRATION=1 go test -run Integration -v .
func TestIntegrationDarwin_WithProxyRestoresExactly(t *testing.T) {
	requireIntegration(t)
	ctx := context.Background()
	services, err := macOSNetworkServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []nsServiceState {
		var all []nsServiceState
		for _, svc := range services {
			st, err := readServiceState(ctx, svc)
			if err != nil {
				t.Fatalf("read %q: %v", svc, err)
			}
			all = append(all, st)
		}
		return all
	}
	before := read()

	err = WithProxy(ctx, "http://127.0.0.1:9", ScopeGlobal, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed:\n before: %+v\n after:  %+v", before, after)
	}

	// F-13: a SOCKS URL lands in the SOCKS field only, and reads back as such.
	var gotSOCKS string
	err = WithProxy(ctx, "socks5://127.0.0.1:9", ScopeGlobal, func(ctx context.Context) error {
		cfg, cerr := GetConfig()
		if cerr != nil {
			return cerr
		}
		if cfg.HTTP != "" || cfg.HTTPS != "" {
			t.Errorf("a socks5 Set filled HTTP/HTTPS: %+v", cfg)
		}
		gotSOCKS, cerr = Get()
		return cerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSOCKS != "socks5://127.0.0.1:9" {
		t.Errorf("Get inside WithProxy(socks5) = %q", gotSOCKS)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed after WithProxy(socks5):\n before: %+v\n after:  %+v", before, after)
	}

	err = WithProxyMulti(ctx, ProxyConfig{HTTP: "http://127.0.0.1:9", NoProxy: "tmp.internal, 10.0.0.0/8"}, ScopeGlobal,
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed after WithProxyMulti:\n before: %+v\n after:  %+v", before, after)
	}
}
