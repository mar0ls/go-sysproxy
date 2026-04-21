//go:build linux

package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEtcEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment")

	if err := writeEtcEnvironment(path, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"http_proxy=http://proxy.example.com:8080",
		"HTTP_PROXY=http://proxy.example.com:8080",
		"https_proxy=http://proxy.example.com:8080",
		"no_proxy=localhost,127.0.0.1,::1",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing line %q in:\n%s", want, content)
		}
	}
}

func TestWriteEtcEnvironmentPreservesExistingLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment")
	existing := "EDITOR=vim\nPATH=/usr/local/bin\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeEtcEnvironment(path, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path) //nolint:gosec
	content := string(data)
	if !strings.Contains(content, "EDITOR=vim") {
		t.Error("EDITOR=vim should be preserved")
	}
	if !strings.Contains(content, "PATH=/usr/local/bin") {
		t.Error("PATH=/usr/local/bin should be preserved")
	}
}

func TestWriteEtcEnvironmentReplacesOldProxy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment")
	old := "http_proxy=http://old:1111\nHTTP_PROXY=http://old:1111\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeEtcEnvironment(path, "http://new:2222"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path) //nolint:gosec
	content := string(data)
	if strings.Contains(content, "old:1111") {
		t.Error("old proxy value should have been replaced")
	}
	if !strings.Contains(content, "new:2222") {
		t.Error("new proxy value should be present")
	}
}

func TestClearEtcEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "environment")
	content := "EDITOR=vim\nhttp_proxy=http://proxy.example.com:8080\nHTTP_PROXY=http://proxy.example.com:8080\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := clearEtcEnvironment(path); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path) //nolint:gosec
	result := string(data)
	if strings.Contains(result, "http_proxy") || strings.Contains(result, "HTTP_PROXY") {
		t.Error("proxy keys should be removed")
	}
	if !strings.Contains(result, "EDITOR=vim") {
		t.Error("non-proxy keys should be preserved")
	}
}

func TestClearEtcEnvironmentMissingFile(t *testing.T) {
	err := clearEtcEnvironment(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Errorf("missing file should not return error, got: %v", err)
	}
}
