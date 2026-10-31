//go:build linux || darwin

package sysproxy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupRC creates the named rc files (with content) in a fresh HOME and pins
// SHELL so the "no rc file exists" fallback is deterministic.
func setupRC(t *testing.T, files map[string]string) string {
	t.Helper()
	home := setTestHome(t)
	t.Setenv("SHELL", "/bin/bash")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	return home
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// bashEval sources rcFile in a fresh bash and prints the named variables, one
// per line. It skips the test when bash is not installed.
func bashEval(t *testing.T, rcFile string, vars ...string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	script := ". " + shellQuote(rcFile) + "; "
	for _, v := range vars {
		script += `printf '%s\n' "${` + v + `-<unset>}"; `
	}
	cmd := exec.Command(bash, "-c", script) //nolint:gosec
	// An ambient http_proxy / NO_PROXY of the machine running the tests must
	// not be mistaken for something the rc file defined.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !containsString(proxyEnvNames, name) {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash failed: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

func TestSetUser_WritesBlockToExistingFiles(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": "alias ll='ls -l'\n", ".zshrc": ""})

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	for _, rc := range []string{".bashrc", ".zshrc"} {
		content := readFile(t, filepath.Join(home, rc))
		for _, want := range []string{
			blockBegin, blockEnd,
			"export http_proxy='http://proxy.example.com:8080'",
			"export HTTP_PROXY='http://proxy.example.com:8080'",
			"export no_proxy='localhost,127.0.0.1,::1'",
		} {
			if !strings.Contains(content, want) {
				t.Errorf("%s: missing %q in:\n%s", rc, want, content)
			}
		}
	}
	if got := readFile(t, filepath.Join(home, ".bashrc")); !strings.HasPrefix(got, "alias ll='ls -l'\n") {
		t.Errorf("existing content must stay first, got:\n%s", got)
	}
}

// F-03: creating ~/.bash_profile makes bash login shells stop reading ~/.profile.
func TestSetUser_DoesNotCreateShadowingFiles(t *testing.T) {
	home := setupRC(t, map[string]string{".profile": "export PATH=$HOME/bin:$PATH\n"})

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	for _, rc := range []string{".bashrc", ".zshrc", ".bash_profile"} {
		if exists(filepath.Join(home, rc)) {
			t.Errorf("%s must not be created when other rc files already exist", rc)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".profile")), "export http_proxy=") {
		t.Error(".profile should have received the block")
	}
}

func TestSetUser_CreatesOnlyOneFileWhenNoneExist(t *testing.T) {
	home := setupRC(t, nil)

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	var created []string
	for _, rc := range unixRCFiles {
		if exists(filepath.Join(home, rc)) {
			created = append(created, rc)
		}
	}
	if len(created) != 1 || created[0] != ".bashrc" {
		t.Errorf("want exactly [.bashrc] (SHELL=bash), got %v", created)
	}
	fi, _ := os.Stat(filepath.Join(home, ".bashrc"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("new rc file mode = %v, want 0600", fi.Mode().Perm())
	}
}

// The default no_proxy must not shadow a bypass list the user already defined.
func TestSetUser_DoesNotShadowUserNoProxy(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": "export NO_PROXY=corp.internal\n"})

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	got := bashEval(t, filepath.Join(home, ".bashrc"), "NO_PROXY", "no_proxy", "http_proxy")
	want := []string{"corp.internal", "<unset>", "http://proxy.example.com:8080"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("effective values = %q, want %q", got, want)
	}
}

// F-04: repeated Set must not pile up lines.
func TestSetUser_Idempotent(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": "echo hi\n"})

	for i := 0; i < 3; i++ {
		if err := setUser("http://proxy.example.com:8080"); err != nil {
			t.Fatal(err)
		}
	}

	content := readFile(t, filepath.Join(home, ".bashrc"))
	if n := strings.Count(content, blockBegin); n != 1 {
		t.Errorf("want 1 block, got %d:\n%s", n, content)
	}
	if n := strings.Count(content, "export http_proxy="); n != 1 {
		t.Errorf("want 1 http_proxy line, got %d", n)
	}
	if strings.Count(content, "\n") != 1+1+8+1 {
		t.Errorf("unexpected line count in:\n%s", content)
	}
}

// F-04: Unset must remove only our block.
func TestUnsetUser_KeepsUnrelatedLines(t *testing.T) {
	original := "export PATH=/x\n" +
		"export git_proxy=keepme\n" +
		"alias my_proxy='ssh -D1080 jump'\n" +
		"# NOTE: http_proxy= was removed from here\n" +
		"export NO_PROXY=corp.internal\n" +
		"export MYAPP_PROXY=1\n" +
		"echo end\n"
	home := setupRC(t, map[string]string{".bashrc": original})

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}
	if err := unsetUser(); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(home, ".bashrc")); got != original {
		t.Errorf("Set+Unset must restore the file exactly.\nwant:\n%s\ngot:\n%s", original, got)
	}
}

func TestUnsetUser_NoBlockLeavesFileUntouched(t *testing.T) {
	original := "export git_proxy=keepme"
	home := setupRC(t, map[string]string{".bashrc": original}) // no trailing newline on purpose

	if err := unsetUser(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(home, ".bashrc")); got != original {
		t.Errorf("file without a block must not be rewritten, got %q", got)
	}
}

// Files written by versions <= 0.5 have no markers; Unset still cleans them up
// but keeps a user's own NO_PROXY.
func TestUnsetUser_RemovesLegacyLines(t *testing.T) {
	legacy := "export PATH=/x\n" +
		"export http_proxy='http://old:1'\nexport HTTP_PROXY='http://old:1'\n" +
		"export https_proxy='http://old:1'\nexport HTTPS_PROXY='http://old:1'\n" +
		"export all_proxy='http://old:1'\nexport ALL_PROXY='http://old:1'\n" +
		"export no_proxy='localhost,127.0.0.1,::1'\nexport NO_PROXY='localhost,127.0.0.1,::1'\n" +
		"export AUTOPROXY='http://pac/p.pac'\n" +
		"export NO_PROXY='corp.internal'\n"
	home := setupRC(t, map[string]string{".bashrc": legacy})

	if err := unsetUser(); err != nil {
		t.Fatal(err)
	}

	want := "export PATH=/x\nexport NO_PROXY='corp.internal'\n"
	if got := readFile(t, filepath.Join(home, ".bashrc")); got != want {
		t.Errorf("want:\n%s\ngot:\n%s", want, got)
	}
}

// F-01: a quote in the URL must not break out of the export line.
func TestSetUser_QuotesHostileValues(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": ""})
	marker := filepath.Join(home, "PWNED")
	// Whitespace is rejected by validation, so go through the writer directly to
	// prove that quoting alone neutralises quotes, $(), backticks and ';'.
	hostile := "http://h.example:80/';touch${IFS}" + marker + ";'$(touch" + marker + ")`touch " + marker + "`"

	if err := setUser(hostile); err != nil {
		t.Fatal(err)
	}

	got := bashEval(t, filepath.Join(home, ".bashrc"), "http_proxy")
	if exists(marker) {
		t.Fatal("sourcing the rc file executed injected code")
	}
	if got[0] != hostile {
		t.Errorf("value did not round-trip.\nwant %q\ngot  %q", hostile, got[0])
	}
}

func TestSetUserPAC_QuotesHostileValues(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": ""})
	marker := filepath.Join(home, "PWNED")
	hostile := "https://x.example/p.pac'\ntouch " + marker + "\n#"

	if err := setUserPAC(hostile); err != nil {
		t.Fatal(err)
	}

	bashEval(t, filepath.Join(home, ".bashrc"))
	if exists(marker) {
		t.Fatal("PAC value escaped its quotes and ran code")
	}
}

func TestSetUserMulti_QuotesHostileNoProxy(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": ""})
	marker := filepath.Join(home, "PWNED")

	cfg := ProxyConfig{HTTP: "http://h:80", NoProxy: "x'\ntouch " + marker + "\n#"}
	if err := setUserMulti(cfg); err != nil {
		t.Fatal(err)
	}

	got := bashEval(t, filepath.Join(home, ".bashrc"), "no_proxy")
	if exists(marker) {
		t.Fatal("NoProxy escaped its quotes and ran code")
	}
	if strings.Join(got, "\n") != cfg.NoProxy {
		t.Errorf("NoProxy did not round-trip: %q", got)
	}
}

func TestSetUser_PACReplacesProxyAndViceVersa(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": ""})

	_ = setUser("http://proxy.example.com:8080")
	_ = setUserPAC("http://config.example.com/proxy.pac")
	content := readFile(t, filepath.Join(home, ".bashrc"))
	if strings.Contains(content, "http_proxy") || !strings.Contains(content, "export AUTOPROXY='http://config.example.com/proxy.pac'") {
		t.Errorf("PAC should replace the manual proxy:\n%s", content)
	}

	_ = setUser("http://proxy.example.com:8080")
	content = readFile(t, filepath.Join(home, ".bashrc"))
	if strings.Contains(content, "AUTOPROXY") || !strings.Contains(content, "export http_proxy=") {
		t.Errorf("manual proxy should replace PAC:\n%s", content)
	}
}

func TestSetUserMulti_MergesWithPreviousBlock(t *testing.T) {
	home := setupRC(t, map[string]string{".bashrc": ""})

	if err := setUserMulti(ProxyConfig{HTTP: "http://a:1", HTTPS: "http://a:1"}); err != nil {
		t.Fatal(err)
	}
	if err := setUserMulti(ProxyConfig{HTTP: "http://b:2"}); err != nil { // empty fields = unchanged
		t.Fatal(err)
	}

	content := readFile(t, filepath.Join(home, ".bashrc"))
	for _, want := range []string{"export http_proxy='http://b:2'", "export https_proxy='http://a:1'"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in:\n%s", want, content)
		}
	}
	if strings.Contains(content, "http://a:1'\nexport HTTP_PROXY") {
		t.Errorf("old HTTP value should be gone:\n%s", content)
	}
}

func TestSetUserMulti_EmptyConfigWritesNothing(t *testing.T) {
	home := setupRC(t, nil)
	if err := setUserMulti(ProxyConfig{}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(home, ".bashrc")) {
		t.Error("an empty config must not create any file")
	}
}

func TestSetUser_KeepsSymlinkAndMode(t *testing.T) {
	home := setupRC(t, nil)
	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "bashrc")
	if err := os.WriteFile(target, []byte("echo hi\n"), 0o640); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	link := filepath.Join(home, ".bashrc")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := setUser("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.bashrc must remain a symlink (err=%v)", err)
	}
	if !strings.Contains(readFile(t, target), "export http_proxy=") {
		t.Error("the symlink target should have been updated")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 preserved", fi.Mode().Perm())
	}
}

func TestSetUser_FailsWhenNothingWritable(t *testing.T) {
	home := setupRC(t, nil)
	if err := os.Chmod(home, 0o500); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) }) //nolint:gosec
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	if err := setUser("http://proxy.example.com:8080"); err == nil {
		t.Error("expected an error when no rc file could be written")
	}
}

// ── ScopeUser in public API ───────────────────────────────────────────────────

func TestSetContext_ScopeUser(t *testing.T) {
	home := setupRC(t, nil)
	t.Cleanup(unsetEnvVars)

	if err := Set("http://proxy.example.com:8080", ScopeUser); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(readFile(t, filepath.Join(home, ".bashrc")), "http_proxy") {
		t.Error(".bashrc missing proxy after ScopeUser Set")
	}
}

func TestUnsetContext_ScopeUser(t *testing.T) {
	home := setupRC(t, nil)
	t.Cleanup(unsetEnvVars)

	_ = Set("http://proxy.example.com:8080", ScopeUser)
	if err := Unset(ScopeUser); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(readFile(t, filepath.Join(home, ".bashrc")), "_proxy=") {
		t.Error(".bashrc still has proxy after ScopeUser Unset")
	}
}

func TestSetMultiContext_ScopeUser(t *testing.T) {
	home := setupRC(t, nil)
	t.Cleanup(unsetEnvVars)

	cfg := ProxyConfig{HTTP: "http://proxy.example.com:8080", HTTPS: "http://proxy.example.com:8080"}
	if err := SetMulti(cfg, ScopeUser); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(readFile(t, filepath.Join(home, ".bashrc")), "http_proxy") {
		t.Error(".bashrc missing proxy after SetMulti ScopeUser")
	}
}

func TestSetPACContext_ScopeUser(t *testing.T) {
	home := setupRC(t, nil)
	t.Cleanup(func() { _ = os.Unsetenv("AUTOPROXY") })

	if err := SetPAC("http://config.example.com/proxy.pac", ScopeUser); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(readFile(t, filepath.Join(home, ".bashrc")), "AUTOPROXY") {
		t.Error(".bashrc missing AUTOPROXY after SetPAC ScopeUser")
	}
}

// F-01 end to end: hostile input is either rejected by validation or quoted.
func TestPublicAPI_RejectsInjectionAttempts(t *testing.T) {
	home := setupRC(t, nil)
	t.Cleanup(unsetEnvVars)
	t.Cleanup(func() { _ = os.Unsetenv("AUTOPROXY") })
	marker := filepath.Join(home, "PWNED")

	if err := SetPAC("https://x.example/p.pac'\ntouch "+marker+"\n#", ScopeUser); err == nil {
		t.Error("PAC URL containing a newline must be rejected")
	}
	if err := SetMulti(ProxyConfig{HTTP: "http://h:80", NoProxy: "x'\ntouch " + marker + "\n#"}, ScopeUser); err == nil {
		t.Error("NoProxy containing a newline must be rejected")
	}
	if err := SetMulti(ProxyConfig{HTTP: "http://h:80", NoProxy: "$(touch " + marker + ")"}, ScopeUser); err == nil {
		t.Error("NoProxy containing $( must be rejected")
	}
	if exists(filepath.Join(home, ".bashrc")) {
		t.Error("rejected input must not leave any file behind")
	}

	// A quote in a URL is legal; it must be neutralised by quoting.
	if err := Set("http://h.example:80/';touch${IFS}"+marker+";'", ScopeUser); err != nil {
		t.Fatal(err)
	}
	bashEval(t, filepath.Join(home, ".bashrc"))
	if exists(marker) {
		t.Fatal("quote in proxy URL executed code")
	}
}

// F-11 / F-04: WithProxy(ScopeUser) puts every rc file back byte for byte —
// the user's own lines, an earlier go-sysproxy block, and the absence of files.
func TestWithProxy_ScopeUser_RestoresRCFilesExactly(t *testing.T) {
	newTrackingBackend(t)
	clearProxyEnv(t)

	cases := map[string]map[string]string{
		"user lines only": {
			".bashrc": "alias ll='ls -l'\nexport PATH=$PATH:/opt/bin\n",
		},
		"existing block": {
			".bashrc":  "# mine\n" + blockBegin + "\nexport http_proxy='http://corp:3128'\nexport no_proxy='mine.internal'\n" + blockEnd + "\nalias x=y\n",
			".zshrc":   "export NO_PROXY=mine.internal\n",
			".profile": "",
		},
		"CRLF file": {
			".bashrc": "a=1\r\nb=2\r\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			home := setupRC(t, files)
			err := WithProxy(context.Background(), "http://tmp:9999", ScopeUser, func(context.Context) error {
				got := readFile(t, filepath.Join(home, ".bashrc"))
				if !strings.Contains(got, "http://tmp:9999") {
					t.Errorf("temporary proxy not written inside fn:\n%s", got)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for file, want := range files {
				if got := readFile(t, filepath.Join(home, file)); got != want {
					t.Errorf("%s changed:\n got: %q\nwant: %q", file, got, want)
				}
			}
		})
	}
}

func TestWithProxy_ScopeUser_RemovesFileItCreated(t *testing.T) {
	newTrackingBackend(t)
	clearProxyEnv(t)
	home := setupRC(t, nil)

	err := WithProxy(context.Background(), "http://tmp:9999", ScopeUser, func(context.Context) error {
		if !exists(filepath.Join(home, ".bashrc")) {
			t.Error("rc file should exist while the proxy is set")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		t.Errorf("WithProxy left %s behind", e.Name())
	}
}

func TestWithProxyMulti_ScopeUser_RestoresMergedBlock(t *testing.T) {
	newTrackingBackend(t)
	clearProxyEnv(t)
	orig := blockBegin + "\nexport http_proxy='http://corp:3128'\nexport no_proxy='mine.internal'\n" + blockEnd + "\n"
	home := setupRC(t, map[string]string{".bashrc": orig})

	err := WithProxyMulti(context.Background(), ProxyConfig{HTTP: "http://tmp:1", HTTPS: "http://tmp:2", NoProxy: "tmp.internal"}, ScopeUser,
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(home, ".bashrc")); got != orig {
		t.Errorf("block not restored:\n got: %q\nwant: %q", got, orig)
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range proxyEnvNames {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}
