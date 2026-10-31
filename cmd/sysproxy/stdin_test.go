package main

import (
	"context"
	"strings"
	"testing"

	sysproxy "github.com/mar0ls/go-sysproxy"
)

// withStdin replaces the reader a URL of "-" is taken from.
func withStdin(t *testing.T, input string) {
	t.Helper()
	orig := stdin
	t.Cleanup(func() { stdin = orig })
	stdin = strings.NewReader(input)
}

// captureCalls replaces set/pac with recorders and returns what they received.
func captureCalls(t *testing.T) (got *[]string) {
	t.Helper()
	origSet, origPAC := setProxy, setPAC
	t.Cleanup(func() { setProxy, setPAC = origSet, origPAC })
	got = new([]string)
	setProxy = func(_ context.Context, u string, _ sysproxy.ProxyScope) error {
		*got = append(*got, "set "+u)
		return nil
	}
	setPAC = func(_ context.Context, u string, _ sysproxy.ProxyScope) error {
		*got = append(*got, "pac "+u)
		return nil
	}
	return got
}

// I5: a password given as an argument is visible in the process list and the
// shell history; "-" takes the URL from stdin instead.
func TestURLArg_FromStdin(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"plain line", "http://u:pw@proxy.example.com:8080\n", "http://u:pw@proxy.example.com:8080"},
		{"no trailing newline", "http://proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"CRLF", "http://proxy.example.com:8080\r\n", "http://proxy.example.com:8080"},
		{"surrounding blanks", "  http://proxy.example.com:8080 \t\n", "http://proxy.example.com:8080"},
		{"only the first line", "http://first.example.com:1\nhttp://second.example.com:2\n", "http://first.example.com:1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := captureCalls(t)
			withStdin(t, c.input)
			code, _, errOut := runCLI("set", "-", "--scope", "shell")
			if code != 0 || len(*calls) != 1 || (*calls)[0] != "set "+c.want {
				t.Fatalf("exit %d, calls %q, stderr %q; want one call with %q", code, *calls, errOut, c.want)
			}
		})
	}
}

func TestURLArg_PACFromStdin(t *testing.T) {
	calls := captureCalls(t)
	withStdin(t, "https://pac.example.com/p.pac\n")
	if code, _, errOut := runCLI("pac", "-"); code != 0 || len(*calls) != 1 || (*calls)[0] != "pac https://pac.example.com/p.pac" {
		t.Fatalf("exit %d, calls %q, stderr %q", code, *calls, errOut)
	}
}

func TestURLArg_StdinErrorsAreExit1AndNothingIsChanged(t *testing.T) {
	cases := map[string]string{
		"empty":           "",
		"only a newline":  "\n",
		"only blanks":     "   \r\n",
		"too long a line": strings.Repeat("a", maxURLLine+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			calls := captureCalls(t)
			withStdin(t, input)
			code, _, errOut := runCLI("set", "-")
			if code != 1 || len(*calls) != 0 {
				t.Fatalf("exit %d, calls %q; want exit 1 and no call", code, *calls)
			}
			if !strings.Contains(errOut, "stdin") {
				t.Errorf("the message does not mention stdin: %q", errOut)
			}
		})
	}
}

// The URL read from stdin is handled like one from the command line: its
// password is masked in the output.
func TestURLArg_StdinPasswordIsMaskedInOutput(t *testing.T) {
	captureCalls(t)
	withStdin(t, "http://alice:s3cr3tpw@proxy.example.com:8080\n")
	code, out, errOut := runCLI("set", "-", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(out+errOut, "s3cr3tpw") || !strings.Contains(out, "alice:xxxxx@") {
		t.Errorf("output must show the masked URL only:\n%s%s", out, errOut)
	}
}

func TestURLArg_CheckReadsStdinAndKeepsThePasswordOut(t *testing.T) {
	withStdin(t, "http://alice:s3cr3tpw@127.0.0.1:1\n") // nothing listens on port 1
	code, out, errOut := runCLI("check", "-", "--timeout", "2s")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (unreachable): %s%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "s3cr3tpw") {
		t.Errorf("output leaks the password:\n%s%s", out, errOut)
	}
}

func TestURLArg_ArgumentsAreUnchanged(t *testing.T) {
	calls := captureCalls(t)
	withStdin(t, "http://from-stdin.example.com:1\n") // must not be read
	if code, _, _ := runCLI("set", "http://arg.example.com:2", "--scope", "shell"); code != 0 || (*calls)[0] != "set http://arg.example.com:2" {
		t.Fatalf("calls %q", *calls)
	}
	if code, _, _ := runCLI("set"); code != 1 {
		t.Errorf("set without a URL: exit %d, want 1", code)
	}
}
