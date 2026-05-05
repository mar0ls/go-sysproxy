//go:build linux || darwin

package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetUser_WritesRCFiles(t *testing.T) {
	home := setTestHome(t)

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	for _, rc := range unixRCFiles {
		data, err := os.ReadFile(filepath.Join(home, rc)) //nolint:gosec
		if err != nil {
			t.Fatalf("rc file %s missing: %v", rc, err)
		}
		content := string(data)
		if !strings.Contains(content, "export http_proxy='http://proxy.example.com:8080'") {
			t.Errorf("%s: missing http_proxy export, got:\n%s", rc, content)
		}
		if !strings.Contains(content, "export HTTP_PROXY='http://proxy.example.com:8080'") {
			t.Errorf("%s: missing HTTP_PROXY export", rc)
		}
		if !strings.Contains(content, "export no_proxy=") {
			t.Errorf("%s: missing no_proxy export", rc)
		}
	}
}

func TestUnsetUser_RemovesProxyLines(t *testing.T) {
	home := setTestHome(t)

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}
	if err := unsetUser(); err != nil {
		t.Fatal(err)
	}

	for _, rc := range unixRCFiles {
		data, _ := os.ReadFile(filepath.Join(home, rc)) //nolint:gosec
		if strings.Contains(string(data), "_proxy=") || strings.Contains(string(data), "_PROXY=") {
			t.Errorf("%s: proxy lines not removed:\n%s", rc, data)
		}
	}
}

func TestSetUserPAC_WritesAUTOPROXY(t *testing.T) {
	home := setTestHome(t)

	if err := setUserPAC("http://config.example.com/proxy.pac"); err != nil {
		t.Fatal(err)
	}

	for _, rc := range unixRCFiles {
		data, err := os.ReadFile(filepath.Join(home, rc)) //nolint:gosec
		if err != nil {
			t.Fatalf("rc file %s missing: %v", rc, err)
		}
		if !strings.Contains(string(data), "export AUTOPROXY='http://config.example.com/proxy.pac'") {
			t.Errorf("%s: missing AUTOPROXY export, got:\n%s", rc, data)
		}
	}
}

func TestSetUserMulti_WritesPerProtocol(t *testing.T) {
	home := setTestHome(t)

	cfg := ProxyConfig{
		HTTP:    "http://http.example.com:8080",
		HTTPS:   "http://https.example.com:8080",
		SOCKS:   "socks5://socks.example.com:1080",
		NoProxy: "localhost,10.0.0.0/8",
	}
	if err := setUserMulti(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	for _, want := range []string{
		"export http_proxy='" + cfg.HTTP + "'",
		"export https_proxy='" + cfg.HTTPS + "'",
		"export all_proxy='" + cfg.SOCKS + "'",
		"export no_proxy='" + cfg.NoProxy + "'",
	} {
		if !strings.Contains(content, want) {
			t.Errorf(".bashrc missing %q", want)
		}
	}
}

func TestSetUserMulti_EmptyFieldsSkipped(t *testing.T) {
	home := setTestHome(t)

	if err := setUserMulti(ProxyConfig{HTTP: "http://proxy.example.com:8080"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	content := string(data)
	if strings.Contains(content, "https_proxy") || strings.Contains(content, "all_proxy") {
		t.Errorf(".bashrc should not contain https/socks entries, got:\n%s", content)
	}
}

// ── ScopeUser in public API ───────────────────────────────────────────────────

func TestSetContext_ScopeUser(t *testing.T) {
	home := setTestHome(t)
	t.Cleanup(unsetEnvVars)

	if err := Set("http://proxy.example.com:8080", ScopeUser); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "http_proxy") {
		t.Error(".bashrc missing proxy after ScopeUser Set")
	}
}

func TestUnsetContext_ScopeUser(t *testing.T) {
	home := setTestHome(t)
	t.Cleanup(unsetEnvVars)

	_ = Set("http://proxy.example.com:8080", ScopeUser)
	if err := Unset(ScopeUser); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	if strings.Contains(string(data), "_proxy=") {
		t.Error(".bashrc still has proxy after ScopeUser Unset")
	}
}

func TestSetMultiContext_ScopeUser(t *testing.T) {
	home := setTestHome(t)
	t.Cleanup(unsetEnvVars)

	cfg := ProxyConfig{HTTP: "http://proxy.example.com:8080", HTTPS: "http://proxy.example.com:8080"}
	if err := SetMulti(cfg, ScopeUser); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	if !strings.Contains(string(data), "http_proxy") {
		t.Error(".bashrc missing proxy after SetMulti ScopeUser")
	}
}

func TestSetPACContext_ScopeUser(t *testing.T) {
	home := setTestHome(t)
	t.Cleanup(func() { _ = os.Unsetenv("AUTOPROXY") })

	if err := SetPAC("http://config.example.com/proxy.pac", ScopeUser); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec
	if !strings.Contains(string(data), "AUTOPROXY") {
		t.Error(".bashrc missing AUTOPROXY after SetPAC ScopeUser")
	}
}
