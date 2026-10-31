//go:build windows

package sysproxy

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// failedExit is an *exec.ExitError produced by a real process that exits 1,
// like `reg query` does for a missing value and for an unreadable key alike.
func failedExit(t *testing.T) error {
	t.Helper()
	err := exec.Command("cmd", "/c", "exit", "1").Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected an exit error, got %v", err)
	}
	return err
}

func useRegQuery(t *testing.T, fn func(args ...string) ([]byte, error)) {
	t.Helper()
	orig := regQuery
	regQuery = func(_ context.Context, args ...string) ([]byte, error) { return fn(args...) }
	t.Cleanup(func() { regQuery = orig })
}

// reg exits 1 for a missing value and for a key it may not read; only the
// first means "absent".
func TestReadRegValue_AbsentVersusUnreadable(t *testing.T) {
	exit := failedExit(t)

	// Value missing, key readable.
	useRegQuery(t, func(args ...string) ([]byte, error) {
		if len(args) > 0 {
			return nil, exit
		}
		return []byte("HKEY_CURRENT_USER\\...\\Internet Settings\n"), nil
	})
	if v, err := readRegValue(context.Background(), "ProxyServer"); err != nil || v.present {
		t.Errorf("missing value: %+v, %v; want absent and no error", v, err)
	}

	// The key cannot be read either (access denied): not "absent".
	useRegQuery(t, func(args ...string) ([]byte, error) { return nil, exit })
	if _, err := readRegValue(context.Background(), "ProxyServer"); err == nil {
		t.Error("an unreadable key was reported as an absent value")
	}
}

func TestSetGlobalMulti_FailsInsteadOfDiscardingWhatItCannotRead(t *testing.T) {
	exit := failedExit(t)
	useRegQuery(t, func(args ...string) ([]byte, error) { return nil, exit })
	err := setGlobalMulti(context.Background(), ProxyConfig{HTTP: "http://h.example.com:80"})
	if err == nil {
		t.Fatal("SetMulti went ahead although ProxyServer could not be read")
	}
}
