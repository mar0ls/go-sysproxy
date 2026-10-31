//go:build windows

package sysproxy

import (
	"testing"
	"time"
)

// TEMPORARY diagnostic: does InternetSetOption write deleted values back?
func TestIntegrationWindows_DiagWinINET(t *testing.T) {
	requireIntegration(t)
	itRegRestoreOnCleanup(t)
	dump := func(label string) { t.Logf("DIAG %-28s %v", label, itRegState(t)) }
	clear := func() {
		for _, n := range []string{"ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL"} {
			itRegDelete(t, n)
		}
	}
	fill := func() {
		itRegSet(t, "ProxyEnable", "REG_DWORD", "1")
		itRegSet(t, "ProxyServer", "REG_SZ", "tmp.example.com:9999")
		itRegSet(t, "ProxyOverride", "REG_SZ", "localhost")
	}
	for _, opts := range [][]uintptr{{39}, {37}, {39, 37}} {
		clear()
		dump("cleared")
		fill()
		for _, o := range opts {
			procSetOptW.Call(0, o, 0, 0)
		}
		dump("filled+notified")
		clear()
		dump("cleared again")
		for _, o := range opts {
			r, _, e := procSetOptW.Call(0, o, 0, 0)
			t.Logf("DIAG option %d -> %d %v", o, r, e)
		}
		dump("notified after clear")
		time.Sleep(2 * time.Second)
		dump("2s later")
		t.Logf("DIAG ---- done opts=%v", opts)
	}
}
