package sysproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installFakeCommand(t *testing.T, name string) string {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, name+".log")

	var (
		scriptPath string
		content    string
	)

	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, name+".bat")
		content = "@echo off\r\n" +
			"if not \"%SYSPROXY_TEST_FAIL_ON%\"==\"\" (\r\n" +
			"  echo %* | findstr /C:\"%SYSPROXY_TEST_FAIL_ON%\" >nul && exit /b 1\r\n" +
			")\r\n" +
			"echo %*>>\"%SYSPROXY_TEST_LOG%\"\r\n"
	} else {
		scriptPath = filepath.Join(dir, name)
		content = "#!/bin/sh\n" +
			"if [ -n \"$SYSPROXY_TEST_FAIL_ON\" ]; then\n" +
			"  case \"$*\" in\n" +
			"    *\"$SYSPROXY_TEST_FAIL_ON\"*) exit 1 ;;\n" +
			"  esac\n" +
			"fi\n" +
			"printf '%s\\n' \"$*\" >> \"$SYSPROXY_TEST_LOG\"\n"
	}

	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SYSPROXY_TEST_LOG", logPath)
	t.Setenv("SYSPROXY_TEST_FAIL_ON", "")

	return logPath
}

func readCommandLog(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func TestWriteGitProxy(t *testing.T) {
	logPath := installFakeCommand(t, "git")

	if err := writeGitProxy(context.Background(), "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	want := []string{
		"config --global http.proxy http://proxy.example.com:8080",
		"config --global https.proxy http://proxy.example.com:8080",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git commands = %q, want %q", lines, want)
	}
}

func TestWriteAppConfigGit(t *testing.T) {
	logPath := installFakeCommand(t, "git")

	if err := WriteAppConfig(AppGit, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("expected two git config calls, got %q", lines)
	}
}

func TestWriteGitProxyNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := writeGitProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !errors.Is(err, ErrToolMissing) {
		t.Fatalf("expected ErrToolMissing, got %v", err)
	}
}

func TestWriteGitProxyCommandFailure(t *testing.T) {
	_ = installFakeCommand(t, "git")
	t.Setenv("SYSPROXY_TEST_FAIL_ON", "http.proxy")

	err := writeGitProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !strings.Contains(err.Error(), "git config http.proxy") {
		t.Fatalf("expected wrapped git command error, got %v", err)
	}
}

func TestClearGitProxy(t *testing.T) {
	logPath := installFakeCommand(t, "git")

	if err := clearGitProxy(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	want := []string{
		"config --global --unset http.proxy",
		"config --global --unset https.proxy",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git commands = %q, want %q", lines, want)
	}
}

func TestClearAppConfigGit(t *testing.T) {
	logPath := installFakeCommand(t, "git")

	if err := ClearAppConfig(AppGit); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("expected two git unset calls, got %q", lines)
	}
}

func TestWriteNPMProxy(t *testing.T) {
	logPath := installFakeCommand(t, "npm")

	if err := writeNPMProxy(context.Background(), "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	want := []string{
		"config set proxy http://proxy.example.com:8080",
		"config set https-proxy http://proxy.example.com:8080",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("npm commands = %q, want %q", lines, want)
	}
}

func TestWriteAppConfigNPM(t *testing.T) {
	logPath := installFakeCommand(t, "npm")

	if err := WriteAppConfig(AppNPM, "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("expected two npm config calls, got %q", lines)
	}
}

func TestWriteNPMProxyCommandFailure(t *testing.T) {
	_ = installFakeCommand(t, "npm")
	t.Setenv("SYSPROXY_TEST_FAIL_ON", "https-proxy")

	err := writeNPMProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !strings.Contains(err.Error(), "npm config set https-proxy") {
		t.Fatalf("expected wrapped npm command error, got %v", err)
	}
}

func TestClearNPMProxy(t *testing.T) {
	logPath := installFakeCommand(t, "npm")

	if err := clearNPMProxy(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	want := []string{
		"config delete proxy",
		"config delete https-proxy",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("npm commands = %q, want %q", lines, want)
	}
}

func TestClearAppConfigNPM(t *testing.T) {
	logPath := installFakeCommand(t, "npm")

	if err := ClearAppConfig(AppNPM); err != nil {
		t.Fatal(err)
	}

	lines := readCommandLog(t, logPath)
	if len(lines) != 2 {
		t.Fatalf("expected two npm delete calls, got %q", lines)
	}
}

func TestClearPipConf(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".config", "pip", "pip.conf")

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "[global]\nproxy = http://proxy.example.com:8080\nindex-url = https://pypi.org/simple\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := clearPipConf(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "proxy = ") {
		t.Fatalf("proxy entry should be removed, got %q", text)
	}
	if !strings.Contains(text, "index-url = https://pypi.org/simple") {
		t.Fatalf("expected unrelated pip settings to stay, got %q", text)
	}
}

func TestClearAppConfigPip(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".config", "pip", "pip.conf")

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[global]\nproxy = http://proxy.example.com:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearAppConfig(AppPip); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "proxy = ") {
		t.Fatalf("proxy entry should be removed, got %q", string(data))
	}
}

func TestClearWgetRC(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".wgetrc")
	content := strings.Join([]string{
		"http_proxy = http://proxy.example.com:8080",
		"https_proxy = http://proxy.example.com:8080",
		"use_proxy = on",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := clearWgetRC(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "http_proxy = ") || strings.Contains(text, "https_proxy = ") {
		t.Fatalf("proxy entries should be removed, got %q", text)
	}
	if !strings.Contains(text, "use_proxy = on") {
		t.Fatalf("expected unrelated wget settings to stay, got %q", text)
	}
}

func TestClearAppConfigWget(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".wgetrc")
	if err := os.WriteFile(path, []byte("http_proxy = http://proxy.example.com:8080\nhttps_proxy = http://proxy.example.com:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearAppConfig(AppWget); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "proxy = ") {
		t.Fatalf("proxy entries should be removed, got %q", string(data))
	}
}

func TestClearAppConfigUnsupported(t *testing.T) {
	if err := ClearAppConfig("burp"); err == nil {
		t.Fatal("expected unsupported app error")
	}
}

func TestWriteAppConfigContextCanceled(t *testing.T) {
	logPath := installFakeCommand(t, "git")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WriteAppConfigContext(ctx, AppGit, "http://proxy.example.com:8080")
	if err == nil || err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}

	if lines := readCommandLog(t, logPath); len(lines) != 0 {
		t.Fatalf("expected no git commands after cancellation, got %q", lines)
	}
}

func TestClearAppConfigContextCanceled(t *testing.T) {
	logPath := installFakeCommand(t, "npm")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ClearAppConfigContext(ctx, AppNPM)
	if err == nil || err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}

	if lines := readCommandLog(t, logPath); len(lines) != 0 {
		t.Fatalf("expected no npm commands after cancellation, got %q", lines)
	}
}

func TestEditINIFileReplacesOnlyTargetSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pip.conf")
	content := strings.Join([]string{
		"[global]",
		"proxy = http://old.example.com:8080",
		"index-url = https://pypi.org/simple",
		"[install]",
		"proxy = http://keep.example.com:8080",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := editINIFile(path, "global", "proxy", "http://new.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "proxy = http://new.example.com:8080") {
		t.Fatalf("expected updated proxy entry, got %q", text)
	}
	if strings.Contains(text, "proxy = http://old.example.com:8080") {
		t.Fatalf("old proxy entry should be replaced, got %q", text)
	}
	if !strings.Contains(text, "[install]\nproxy = http://keep.example.com:8080") {
		t.Fatalf("expected other section to stay unchanged, got %q", text)
	}
}

func TestEditINIFileAddsMissingSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pip.conf")
	content := "[install]\ntrusted-host = pypi.org\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := editINIFile(path, "global", "proxy", "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[global]\nproxy = http://proxy.example.com:8080\n") {
		t.Fatalf("expected missing section to be appended, got %q", text)
	}
}

func TestRemoveINIKeyOnlyAffectsTargetSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pip.conf")
	content := strings.Join([]string{
		"[global]",
		"proxy = http://proxy.example.com:8080",
		"index-url = https://pypi.org/simple",
		"[install]",
		"proxy = http://keep.example.com:8080",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeINIKey(path, "global", "proxy"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "[global]\nproxy = http://proxy.example.com:8080") {
		t.Fatalf("global proxy entry should be removed, got %q", text)
	}
	if !strings.Contains(text, "index-url = https://pypi.org/simple") {
		t.Fatalf("expected unrelated keys in target section to stay, got %q", text)
	}
	if !strings.Contains(text, "[install]\nproxy = http://keep.example.com:8080") {
		t.Fatalf("expected same key in another section to stay, got %q", text)
	}
}

func TestWriteCurlRC(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".curlrc")

	if err := writeCurlRC("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy = http://proxy.example.com:8080") {
		t.Fatalf("expected proxy entry, got %q", string(data))
	}
}

func TestClearCurlRC(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".curlrc")
	if err := os.WriteFile(path, []byte("proxy = http://proxy.example.com:8080\nmax-time = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := clearCurlRC(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "proxy = ") {
		t.Fatalf("expected proxy entry removed, got %q", string(data))
	}
	if !strings.Contains(string(data), "max-time = 30") {
		t.Fatalf("expected unrelated entry to stay, got %q", string(data))
	}
}

func TestWriteWgetRC(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".wgetrc")

	if err := writeWgetRC("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "http_proxy = http://proxy.example.com:8080") {
		t.Fatalf("expected http_proxy entry, got %q", text)
	}
	if !strings.Contains(text, "https_proxy = http://proxy.example.com:8080") {
		t.Fatalf("expected https_proxy entry, got %q", text)
	}
}

func TestWritePipConf(t *testing.T) {
	home := setTestHome(t)
	path := filepath.Join(home, ".config", "pip", "pip.conf")

	if err := writePipConf("http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "[global]") {
		t.Fatalf("expected [global] section, got %q", text)
	}
	if !strings.Contains(text, "proxy = http://proxy.example.com:8080") {
		t.Fatalf("expected proxy entry, got %q", text)
	}
}

func TestClearGitProxyNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := clearGitProxy(context.Background())
	if err == nil || !errors.Is(err, ErrToolMissing) {
		t.Fatalf("expected ErrToolMissing, got %v", err)
	}
}

func TestWriteNPMProxyNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := writeNPMProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !errors.Is(err, ErrToolMissing) {
		t.Fatalf("expected ErrToolMissing, got %v", err)
	}
}

func TestClearNPMProxyNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := clearNPMProxy(context.Background())
	if err == nil || !errors.Is(err, ErrToolMissing) {
		t.Fatalf("expected ErrToolMissing, got %v", err)
	}
}

func TestWriteGitProxyHTTPSFailure(t *testing.T) {
	_ = installFakeCommand(t, "git")
	t.Setenv("SYSPROXY_TEST_FAIL_ON", "https.proxy")

	err := writeGitProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !strings.Contains(err.Error(), "git config https.proxy") {
		t.Fatalf("expected https.proxy error, got %v", err)
	}
}

func TestWriteNPMProxyFirstCmdFailure(t *testing.T) {
	_ = installFakeCommand(t, "npm")
	t.Setenv("SYSPROXY_TEST_FAIL_ON", "set proxy")

	err := writeNPMProxy(context.Background(), "http://proxy.example.com:8080")
	if err == nil || !strings.Contains(err.Error(), "npm config set proxy") {
		t.Fatalf("expected proxy set error, got %v", err)
	}
}

func TestWritePipConfMkdirAllError(t *testing.T) {
	home := setTestHome(t)
	// Block the pip config dir by creating a file where the directory would be.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "pip"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writePipConf("http://proxy.example.com:8080"); err == nil {
		t.Fatal("expected error when pip dir blocked by file")
	}
}

func TestEditKeyValueFileReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced the same way on Windows")
	}
	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte("key = val\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	if err := editKeyValueFile(path, "key", "newval", " = "); err == nil {
		t.Fatal("expected error reading unreadable file")
	}
}

func TestRemoveKeysFromFileReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced the same way on Windows")
	}
	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte("key = val\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	if err := removeKeysFromFile(path, " = ", "key"); err == nil {
		t.Fatal("expected error reading unreadable file")
	}
}

func TestRemoveINIKeyReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced the same way on Windows")
	}
	path := filepath.Join(t.TempDir(), "conf.ini")
	if err := os.WriteFile(path, []byte("[global]\nproxy = http://proxy.example.com\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	if err := removeINIKey(path, "global", "proxy"); err == nil {
		t.Fatal("expected error reading unreadable file")
	}
}

func TestEditINIFileReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced the same way on Windows")
	}
	path := filepath.Join(t.TempDir(), "conf.ini")
	if err := os.WriteFile(path, []byte("[global]\nproxy = old\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	if err := editINIFile(path, "global", "proxy", "http://proxy.example.com:8080"); err == nil {
		t.Fatal("expected error reading unreadable file")
	}
}

func TestWriteWgetRCReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced the same way on Windows")
	}
	home := setTestHome(t)
	path := filepath.Join(home, ".wgetrc")
	if err := os.WriteFile(path, []byte("old = val\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	if err := writeWgetRC("http://proxy.example.com:8080"); err == nil {
		t.Fatal("expected error reading unreadable .wgetrc")
	}
}
