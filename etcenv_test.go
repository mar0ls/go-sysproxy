package sysproxy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func etcPath(t *testing.T, initial string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "environment")
	if initial != "" {
		if err := os.WriteFile(path, []byte(initial), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	return path
}

func readEtc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// F-05: an administrator's NO_PROXY must survive, and so must the file's shape.
func TestWriteEtcEnvironment_KeepsAdminNoProxyAndFormat(t *testing.T) {
	initial := "PATH=\"/usr/bin\"\nNO_PROXY=corp.internal\nLANG=C\n"
	path := etcPath(t, initial)

	for i := 0; i < 2; i++ {
		if err := writeEtcEnvironment(path, "http://h:80"); err != nil {
			t.Fatal(err)
		}
	}

	got := readEtc(t, path)
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("file must end with a newline, got %q", got[max(0, len(got)-20):])
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("unexpected blank line in:\n%s", got)
	}
	if !strings.HasPrefix(got, initial) {
		t.Errorf("existing lines must stay untouched and first:\n%s", got)
	}
	if strings.Count(got, "NO_PROXY=") != 1 || !strings.Contains(got, "NO_PROXY=corp.internal") {
		t.Errorf("admin NO_PROXY must be kept and not duplicated:\n%s", got)
	}
	// lower-case variant did not exist, so ours is added
	if !strings.Contains(got, "no_proxy="+defaultNoProxy) {
		t.Errorf("missing default no_proxy:\n%s", got)
	}
	if strings.Count(got, blockBegin) != 1 {
		t.Errorf("want exactly one block:\n%s", got)
	}
}

// F-02/F-05: the file is world-readable, so credentials must not be written.
func TestWriteEtcEnvironment_DropsCredentials(t *testing.T) {
	path := etcPath(t, "")

	err := writeEtcEnvironment(path, "http://alice:s3cr3t@proxy.example.com:8080/#frag")

	if !IsNonCritical(err) || !errors.Is(err, errCredentialsOmitted) {
		t.Fatalf("want a non-critical credentials-omitted error, got %v", err)
	}
	got := readEtc(t, path)
	if strings.Contains(got, "s3cr3t") || strings.Contains(got, "alice") {
		t.Errorf("credentials leaked into the file:\n%s", got)
	}
	if !strings.Contains(got, "http_proxy=http://proxy.example.com:8080/\n") {
		t.Errorf("sanitised URL missing:\n%s", got)
	}
}

func TestWriteEtcEnvironment_ModeStaysReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not meaningful on Windows")
	}
	path := etcPath(t, "")
	if err := writeEtcEnvironment(path, "http://h:80"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("new /etc/environment mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestWriteEtcEnvironment_CleansUpLegacyUnmarkedLines(t *testing.T) {
	legacy := "EDITOR=vim\n\nhttp_proxy=http://old:1\nHTTP_PROXY=http://old:1\n" +
		"no_proxy=localhost,127.0.0.1,::1\nNO_PROXY=localhost,127.0.0.1,::1"
	path := etcPath(t, legacy)

	if err := writeEtcEnvironment(path, "http://new:2"); err != nil {
		t.Fatal(err)
	}

	got := readEtc(t, path)
	if strings.Contains(got, "old:1") {
		t.Errorf("legacy proxy lines must be replaced:\n%s", got)
	}
	if strings.Count(got, "http_proxy=") != 1 || strings.Count(got, "NO_PROXY=") != 1 {
		t.Errorf("duplicated keys:\n%s", got)
	}
	if !strings.Contains(got, "EDITOR=vim") {
		t.Errorf("EDITOR lost:\n%s", got)
	}
}

func TestClearEtcEnvironment_RestoresOriginalExactly(t *testing.T) {
	initial := "PATH=\"/usr/bin\"\nNO_PROXY=corp.internal\nLANG=C\n"
	path := etcPath(t, initial)

	if err := writeEtcEnvironment(path, "http://h:80"); err != nil {
		t.Fatal(err)
	}
	if err := clearEtcEnvironment(path); err != nil {
		t.Fatal(err)
	}

	if got := readEtc(t, path); got != initial {
		t.Errorf("Set+Clear must restore the file.\nwant %q\n got %q", initial, got)
	}
}

// An unprivileged Unset on a file with nothing to remove must not need write access.
func TestClearEtcEnvironment_NothingToRemoveDoesNotWrite(t *testing.T) {
	path := etcPath(t, "PATH=/usr/bin\nno_proxy=custom\n")
	before, _ := os.Stat(path)

	if err := clearEtcEnvironment(path); err != nil {
		t.Fatal(err)
	}

	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("file must not be rewritten when there is nothing to remove")
	}
	if got := readEtc(t, path); got != "PATH=/usr/bin\nno_proxy=custom\n" {
		t.Errorf("content changed: %q", got)
	}
}

func TestEtcSanitizeURL(t *testing.T) {
	cases := []struct {
		in, want string
		creds    bool
	}{
		{"http://h:80", "http://h:80", false},
		{"http://u:p@h:80", "http://h:80", true},
		{"http://u@h:80/x#f", "http://h:80/x", true},
		{"socks5://h:1080", "socks5://h:1080", false},
	}
	for _, c := range cases {
		got, creds := etcSanitizeURL(c.in)
		if got != c.want || creds != c.creds {
			t.Errorf("etcSanitizeURL(%q) = (%q, %v), want (%q, %v)", c.in, got, creds, c.want, c.creds)
		}
	}
}

// Unset must not try to rewrite a file it has nothing to remove from, even when
// the file lacks a trailing newline (which a rewrite would add).
func TestClearEtcEnvironment_UntouchedFileWithoutTrailingNewline(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a read-only directory that is enforced")
	}
	path := etcPath(t, "PATH=/usr/bin")
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o555); err != nil { //nolint:gosec // test: make a rewrite impossible
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) //nolint:gosec
	if err := os.Chmod(path, 0o444); err != nil {  //nolint:gosec // test: read-only on purpose
		t.Fatal(err)
	}

	if err := clearEtcEnvironment(path); err != nil {
		t.Fatalf("clearEtcEnvironment on a file with nothing to remove: %v", err)
	}
	if got := readEtc(t, path); got != "PATH=/usr/bin" {
		t.Errorf("file changed to %q", got)
	}
}
