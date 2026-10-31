//go:build windows

package sysproxy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the real registry (via reg.exe) and Windows PowerShell:
//
//	sysproxy.test.exe -test.run Integration -test.v   (with SYSPROXY_INTEGRATION=1)

func itReg(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("reg", args...).CombinedOutput() //nolint:gosec
	return string(out), err
}

// itRegState returns "type|data" for every value the package writes, or
// "<absent>".
func itRegState(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, name := range regValueNames {
		out, err := itReg(t, "query", regKey, "/v", name)
		if err != nil {
			m[name] = "<absent>"
			continue
		}
		typ, data, ok := parseRegQuery(out, name)
		if !ok {
			t.Fatalf("cannot parse reg query output for %s:\n%s", name, out)
		}
		m[name] = typ + "|" + data
	}
	return m
}

func itRegRestoreOnCleanup(t *testing.T) {
	t.Helper()
	orig := itRegState(t)
	t.Cleanup(func() {
		for name, v := range orig {
			if v == "<absent>" {
				_, _ = itReg(t, "delete", regKey, "/v", name, "/f")
				continue
			}
			typ, data, _ := strings.Cut(v, "|")
			_, _ = itReg(t, "add", regKey, "/v", name, "/t", typ, "/d", data, "/f")
		}
	})
}

func itRegSet(t *testing.T, name, typ, data string) {
	t.Helper()
	if out, err := itReg(t, "add", regKey, "/v", name, "/t", typ, "/d", data, "/f"); err != nil {
		t.Fatalf("reg add %s: %v\n%s", name, err, out)
	}
}

func itRegDelete(t *testing.T, name string) {
	t.Helper()
	_, _ = itReg(t, "delete", regKey, "/v", name, "/f")
}

func TestIntegrationWindows_SetPACUnsetRoundTrip(t *testing.T) {
	requireIntegration(t)
	itRegRestoreOnCleanup(t)
	itRegDelete(t, "AutoConfigURL")
	itRegDelete(t, "ProxyServer")
	itRegSet(t, "ProxyEnable", "REG_DWORD", "0")

	ctx := context.Background()
	if err := SetContext(ctx, "http://proxy.example.com:8080", ScopeGlobal); err != nil {
		t.Fatalf("Set: %v", err)
	}
	st := itRegState(t)
	if st["ProxyEnable"] != "REG_DWORD|0x1" || st["ProxyServer"] != "REG_SZ|proxy.example.com:8080" {
		t.Errorf("registry after Set: %v", st)
	}
	if cfg, err := GetConfig(); err != nil || cfg.HTTP != "http://proxy.example.com:8080" {
		t.Errorf("GetConfig after Set = %+v, %v", cfg, err)
	}

	if err := SetPAC("http://pac.example.com/proxy.pac", ScopeGlobal); err != nil {
		t.Fatalf("SetPAC: %v", err)
	}
	st = itRegState(t)
	if st["ProxyEnable"] != "REG_DWORD|0x0" || st["AutoConfigURL"] != "REG_SZ|http://pac.example.com/proxy.pac" {
		t.Errorf("registry after SetPAC: %v", st)
	}
	if cfg, err := GetConfig(); err != nil || cfg.PAC != "http://pac.example.com/proxy.pac" {
		t.Errorf("GetConfig after SetPAC = %+v, %v", cfg, err)
	}

	if err := SetMulti(ProxyConfig{HTTP: "http://h.example.com:80", HTTPS: "http://s.example.com:443", SOCKS: "socks5://k.example.com:1080", NoProxy: "localhost;10.*;<local>"}, ScopeGlobal); err != nil {
		t.Fatalf("SetMulti: %v", err)
	}
	st = itRegState(t)
	if st["AutoConfigURL"] != "<absent>" || !strings.Contains(st["ProxyServer"], "https=s.example.com:443") || st["ProxyOverride"] != "REG_SZ|localhost;10.*;<local>" {
		t.Errorf("registry after SetMulti: %v", st)
	}

	if err := Unset(ScopeGlobal); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	st = itRegState(t)
	if st["ProxyEnable"] != "REG_DWORD|0x0" || st["ProxyServer"] != "<absent>" || st["AutoConfigURL"] != "<absent>" {
		t.Errorf("registry after Unset: %v", st)
	}
}

// F-10 on the real registry: values with spaces, values that were absent, and
// a PAC configuration all come back exactly.
func TestIntegrationWindows_WithProxyRestoresExactly(t *testing.T) {
	requireIntegration(t)
	itRegRestoreOnCleanup(t)

	scenarios := map[string]func(){
		"manual proxy with spaced bypass list": func() {
			itRegDelete(t, "AutoConfigURL")
			itRegSet(t, "ProxyEnable", "REG_DWORD", "1")
			itRegSet(t, "ProxyServer", "REG_SZ", "http=corp.example.com:3128;https=corp.example.com:3129")
			itRegSet(t, "ProxyOverride", "REG_SZ", "localhost;*.corp.internal 10.*;<local>")
		},
		"PAC only": func() {
			itRegSet(t, "ProxyEnable", "REG_DWORD", "0")
			itRegDelete(t, "ProxyServer")
			itRegDelete(t, "ProxyOverride")
			itRegSet(t, "AutoConfigURL", "REG_SZ", "http://pac.example.com/prev.pac")
		},
		"nothing configured": func() {
			itRegDelete(t, "ProxyEnable")
			itRegDelete(t, "ProxyServer")
			itRegDelete(t, "ProxyOverride")
			itRegDelete(t, "AutoConfigURL")
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			setup()
			before := itRegState(t)
			var inside map[string]string
			err := WithProxy(context.Background(), "http://tmp.example.com:9999", ScopeGlobal, func(context.Context) error {
				inside = itRegState(t)
				return nil
			})
			if err != nil {
				t.Fatalf("WithProxy: %v", err)
			}
			if inside["ProxyServer"] != "REG_SZ|tmp.example.com:9999" {
				t.Errorf("temporary proxy not active inside fn: %v", inside)
			}
			for k, v := range before {
				if got := itRegState(t)[k]; got != v {
					t.Errorf("%s: before %q, after WithProxy %q", k, v, got)
				}
			}
			// F-13: SOCKS needs its own ProxyServer entry and reads back as SOCKS.
			var gotSOCKS string
			err = WithProxy(context.Background(), "socks5://tmp.example.com:9998", ScopeGlobal, func(context.Context) error {
				inside = itRegState(t)
				gotSOCKS, _ = Get()
				return nil
			})
			if err != nil {
				t.Fatalf("WithProxy(socks5): %v", err)
			}
			if inside["ProxyServer"] != "REG_SZ|socks=tmp.example.com:9998" || gotSOCKS != "socks5://tmp.example.com:9998" {
				t.Errorf("socks5 Set: ProxyServer = %q, Get = %q", inside["ProxyServer"], gotSOCKS)
			}
			for k, v := range before {
				if got := itRegState(t)[k]; got != v {
					t.Errorf("%s: before %q, after WithProxy(socks5) %q", k, v, got)
				}
			}
			if err := WithProxyMulti(context.Background(), ProxyConfig{HTTP: "http://tmp.example.com:1", NoProxy: "tmp"}, ScopeGlobal, func(context.Context) error { return nil }); err != nil {
				t.Fatalf("WithProxyMulti: %v", err)
			}
			for k, v := range before {
				if got := itRegState(t)[k]; got != v {
					t.Errorf("%s: before %q, after WithProxyMulti %q", k, v, got)
				}
			}
		})
	}
}

// F-09: a registry that refuses the write (as under a restrictive GPO) must
// make Set fail instead of reporting success.
func TestIntegrationWindows_RegistryWriteDeniedIsReported(t *testing.T) {
	requireIntegration(t)
	itRegRestoreOnCleanup(t)

	const key = `HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	ps := func(script string) string {
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script) //nolint:gosec
		// A PSModulePath inherited from PowerShell 7 keeps Windows PowerShell from
		// loading Microsoft.PowerShell.Security (Get-Acl).
		cmd.Env = withoutEnv(os.Environ(), "PSModulePath")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("powershell: %v\n%s", err, out)
		}
		return string(out)
	}
	ps(`$k='` + key + `'; $acl=Get-Acl $k; $r=New-Object System.Security.AccessControl.RegistryAccessRule([Security.Principal.WindowsIdentity]::GetCurrent().Name,'SetValue','Deny'); $acl.AddAccessRule($r); Set-Acl $k $acl`)
	t.Cleanup(func() {
		ps(`$k='` + key + `'; $acl=Get-Acl $k; $acl.Access | Where-Object { $_.AccessControlType -eq 'Deny' -and $_.IdentityReference -eq [Security.Principal.WindowsIdentity]::GetCurrent().Name } | ForEach-Object { [void]$acl.RemoveAccessRule($_) }; Set-Acl $k $acl`)
	})

	err := SetContext(context.Background(), "http://proxy.example.com:8080", ScopeGlobal)
	t.Logf("Set with a write-protected key: %v", err)
	if err == nil || IsNonCritical(err) {
		t.Fatalf("Set reported success although the registry refused the write (err = %v)", err)
	}
	if err := SetPAC("http://pac.example.com/p.pac", ScopeGlobal); err == nil {
		t.Error("SetPAC reported success although the registry refused the write")
	}
	if err := Unset(ScopeGlobal); err == nil {
		t.Error("Unset reported success although the registry refused the write")
	}
	if err := WithProxy(context.Background(), "http://tmp.example.com:1", ScopeGlobal, func(context.Context) error {
		t.Error("fn must not run when the proxy could not be set")
		return nil
	}); err == nil {
		t.Error("WithProxy reported success although the registry refused the write")
	}
}

// F-01 on a real PowerShell: the profile is dot-sourced with hostile values and
// must neither run them nor mangle them. WithProxy(ScopeUser) must give the
// profile back byte for byte, CRLF included.
func TestIntegrationWindows_UserScopeProfile(t *testing.T) {
	requireIntegration(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	profile, err := powershellProfile()
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(home, "pwned.txt")

	evalProfile := func(vars ...string) []string {
		// UTF-8 output: the console code page cannot represent U+2019 and would
		// turn it into an ASCII quote.
		script := "[Console]::OutputEncoding = [Text.Encoding]::UTF8; . '" + profile + "'; "
		for _, v := range vars {
			script += "[Console]::Out.WriteLine([string]$env:" + v + "); "
		}
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput() //nolint:gosec
		if err != nil {
			t.Fatalf("powershell: %v\n%s", err, out)
		}
		return strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	}

	hostile := []string{
		"http://h.example.com/p'x;$([IO.File]::WriteAllText('" + strings.ReplaceAll(sentinel, `\`, `\\`) + "','x')).pac",
		"http://h.example.com/p\u2019x;$([IO.File]::WriteAllText('" + strings.ReplaceAll(sentinel, `\`, `\\`) + "','x')).pac",
		"http://h.example.com/`$env:USERNAME/a%27b.pac",
	}
	for _, pac := range hostile {
		if err := SetPAC(pac, ScopeUser); err != nil {
			t.Fatalf("SetPAC(%q): %v", pac, err)
		}
		if got := evalProfile("AUTOPROXY")[0]; got != pac {
			t.Errorf("AUTOPROXY = %q, want %q", got, pac)
		}
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatalf("injection succeeded for %q", pac)
		}
	}
	if err := Unset(ScopeUser); err != nil {
		t.Fatal(err)
	}

	// WithProxy restores a CRLF profile with the user's own lines exactly.
	orig := "Set-Alias ll Get-ChildItem\r\n" + blockBegin + "\r\n$env:http_proxy = 'http://corp:3128'\r\n" + blockEnd + "\r\n$x = 1\r\n"
	if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	var inside string
	err = WithProxy(context.Background(), "http://tmp.example.com:9999", ScopeUser, func(context.Context) error {
		inside = evalProfile("http_proxy")[0]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if inside != "http://tmp.example.com:9999" {
		t.Errorf("http_proxy inside fn = %q", inside)
	}
	data, _ := os.ReadFile(profile) //nolint:gosec
	if string(data) != orig {
		t.Errorf("profile changed:\n got: %q\nwant: %q", data, orig)
	}
}

// withoutEnv returns env without the variable called name (case-insensitive).
func withoutEnv(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, name) {
			out = append(out, kv)
		}
	}
	return out
}
