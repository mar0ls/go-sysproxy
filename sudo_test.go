package sysproxy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func asRoot(t *testing.T, root bool) {
	t.Helper()
	orig := privileged
	privileged = func() bool { return root }
	t.Cleanup(func() { privileged = orig })
}

func fakeUserHome(t *testing.T, homes map[string]string) {
	t.Helper()
	orig := lookupUserHome
	lookupUserHome = func(name string) (string, error) {
		if h, ok := homes[name]; ok {
			return h, nil
		}
		return "", errors.New("unknown user")
	}
	t.Cleanup(func() { lookupUserHome = orig })
}

func TestSudoCaller(t *testing.T) {
	cases := []struct {
		root bool
		env  string
		want string
	}{
		{false, "alice", ""}, // not root: SUDO_USER is stale environment
		{true, "", ""},       // plain root login
		{true, "root", ""},   // sudo -u root from root
		{true, "alice", "alice"},
	}
	for _, c := range cases {
		asRoot(t, c.root)
		t.Setenv("SUDO_USER", c.env)
		if got := sudoCaller(); got != c.want {
			t.Errorf("root=%v SUDO_USER=%q: sudoCaller = %q, want %q", c.root, c.env, got, c.want)
		}
	}
}

func TestSudoHomeWarning(t *testing.T) {
	home := setTestHome(t)
	asRoot(t, true)
	t.Setenv("SUDO_USER", "alice")

	fakeUserHome(t, map[string]string{"alice": "/home/alice"})
	err := sudoHomeWarning()
	if !IsNonCritical(err) {
		t.Fatalf("different home: err = %v, want a non-critical warning", err)
	}
	for _, want := range []string{"SUDO_USER=alice", home, "/home/alice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("warning lacks %q: %v", want, err)
		}
	}

	fakeUserHome(t, map[string]string{"alice": home + string(os.PathSeparator) + "."})
	if err := sudoHomeWarning(); err != nil {
		t.Errorf("HOME kept (same directory): err = %v, want nil", err)
	}

	fakeUserHome(t, nil) // the caller cannot be looked up: do not guess
	if err := sudoHomeWarning(); err != nil {
		t.Errorf("unknown caller: err = %v, want nil", err)
	}

	asRoot(t, false)
	fakeUserHome(t, map[string]string{"alice": "/home/alice"})
	if err := sudoHomeWarning(); err != nil {
		t.Errorf("not root: err = %v, want nil", err)
	}
}

func TestWithWarning(t *testing.T) {
	hard, soft := errors.New("hard"), &nonCriticalError{err: errors.New("soft")}
	if got := withWarning(hard, soft); got != hard || IsNonCritical(got) {
		t.Errorf("a hard error must win, got %v", got)
	}
	if got := withWarning(nil, soft); got != soft {
		t.Errorf("a warning must be returned when there is no error, got %v", got)
	}
	if got := withWarning(nil, nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// ScopeUser and WriteAppConfig write below HOME; as root through sudo that is
// usually root's home, and the caller is told.
func TestUserScopeFiles_WarnWhenHomeIsNotTheCallers(t *testing.T) {
	home := setTestHome(t)
	asRoot(t, true)
	t.Setenv("SUDO_USER", "alice")
	fakeUserHome(t, map[string]string{"alice": filepath.Join(t.TempDir(), "alice")})

	err := WriteAppConfig(AppCurl, "http://proxy.example.com:8080")
	if !IsNonCritical(err) {
		t.Fatalf("WriteAppConfig: err = %v, want a non-critical warning", err)
	}
	if _, serr := os.Stat(filepath.Join(home, ".curlrc")); serr != nil {
		t.Errorf("the file must still have been written: %v", serr)
	}
	if err := ClearAppConfig(AppCurl); !IsNonCritical(err) {
		t.Errorf("ClearAppConfig: err = %v, want a non-critical warning", err)
	}
	if err := WriteAppConfig(AppCurl, "http://bad host:1"); err == nil || IsNonCritical(err) {
		t.Errorf("invalid input must stay a hard error, got %v", err)
	}
}
