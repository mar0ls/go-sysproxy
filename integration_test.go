package sysproxy

import (
	"os"
	"testing"
)

// requireIntegration skips tests that change the real system proxy settings.
// They are meant for throw-away VMs:
//
//	SYSPROXY_INTEGRATION=1 go test -run Integration -v .
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("SYSPROXY_INTEGRATION") != "1" {
		t.Skip("set SYSPROXY_INTEGRATION=1 to run tests that change the real system proxy")
	}
	// Set* also writes the proxy variables of this process; do not leak them
	// into the tests that follow.
	env := snapshotEnv()
	t.Cleanup(func() { _ = env.restore() })
}
