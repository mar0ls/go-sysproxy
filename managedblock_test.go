package sysproxy

import (
	"strings"
	"testing"
)

func TestUpdateBlock(t *testing.T) {
	body := []string{"A=1", "B=2"}
	block := blockBegin + "\nA=1\nB=2\n" + blockEnd + "\n"

	cases := []struct {
		name, in string
		body     []string
		want     string
	}{
		{"append to empty", "", body, block},
		{"append after content", "x\ny\n", body, "x\ny\n" + block},
		{"append when no trailing newline", "x", body, "x\n" + block},
		{"replace in place", "top\n" + blockBegin + "\nOLD\n" + blockEnd + "\nbottom\n", body, "top\n" + block + "bottom\n"},
		{"remove", "top\n" + block + "bottom\n", nil, "top\nbottom\n"},
		{"remove last block empties file", block, nil, ""},
		{"nothing to remove returns input untouched", "a\nb", nil, "a\nb"},
		{"duplicate blocks collapse into the first", block + "mid\n" + block, body, block + "mid\n"},
		{"begin without end is ignored", blockBegin + "\nuser line\n", body, blockBegin + "\nuser line\n" + block},
		{"stray begin before a real block", blockBegin + "\nkeep\n" + block, nil, blockBegin + "\nkeep\n"},
		{
			"CRLF preserved", "a\r\nb\r\n", body,
			"a\r\nb\r\n" + blockBegin + "\r\nA=1\r\nB=2\r\n" + blockEnd + "\r\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := updateBlock(c.in, c.body); got != c.want {
				t.Errorf("updateBlock(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestUpdateBlock_IdempotentAndReversible(t *testing.T) {
	orig := "export PATH=/x\n\n# my stuff\nalias a=b\n"
	body := []string{"export X='1'"}

	once := updateBlock(orig, body)
	if twice := updateBlock(once, body); twice != once {
		t.Errorf("not idempotent:\n%q\n%q", once, twice)
	}
	if back := updateBlock(once, nil); back != orig {
		t.Errorf("remove did not restore the original:\n got %q\nwant %q", back, orig)
	}
}

func TestBlockBody(t *testing.T) {
	in := "x\n" + blockBegin + "\r\nA=1\r\nB=2\r\n" + blockEnd + "\ny\n"
	got := blockBody(in)
	if strings.Join(got, "|") != "A=1|B=2" {
		t.Errorf("blockBody = %q", got)
	}
	if blockBody("no block here\n") != nil {
		t.Error("expected nil when there is no block")
	}
}

func TestMergeBody(t *testing.T) {
	old := []string{"export http_proxy='a'", "export HTTP_PROXY='a'", "export https_proxy='a'", "# comment"}
	upd := []string{"export http_proxy='b'", "export HTTP_PROXY='b'"}
	got := strings.Join(mergeBody(old, upd), "\n")
	want := "export https_proxy='a'\n# comment\nexport http_proxy='b'\nexport HTTP_PROXY='b'"
	if got != want {
		t.Errorf("mergeBody:\n got %q\nwant %q", got, want)
	}
}

func TestRemoveLegacyLines(t *testing.T) {
	in := "keep1\n" +
		"export http_proxy='http://p:1'\n" +
		"$env:HTTPS_PROXY = 'http://p:1'\n" +
		"export AUTOPROXY='http://pac'\n" +
		"export no_proxy='localhost,127.0.0.1,::1'\n" +
		"export NO_PROXY='corp.internal'\n" + // user's own — must survive
		"export git_proxy='x'\n" + // not ours — must survive
		"keep2\n"
	want := "keep1\nexport NO_PROXY='corp.internal'\nexport git_proxy='x'\nkeep2\n"
	if got := removeLegacyLines(in); got != want {
		t.Errorf("removeLegacyLines:\n got %q\nwant %q", got, want)
	}
	if got := removeLegacyLines("a\nb"); got != "a\nb" {
		t.Errorf("unchanged input must be returned byte for byte, got %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":       `'plain'`,
		"":            `''`,
		"it's":        `'it'\''s'`,
		"$(x) `y` \\": "'$(x) `y` \\'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPSQuote(t *testing.T) {
	cases := map[string]string{
		"plain":         `'plain'`,
		"it's":          `'it''s'`,
		"a\u2019b":      `'a''b'`, // typographic quotes count as quotes in PowerShell
		"$env:X `n":     "'$env:X `n'",
		"\u2018x\u201b": `'''x'''`,
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// unquoteShell is a minimal POSIX single-quote reader used to check that
// shellQuote output always decodes back to its input.
func unquoteShell(q string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(q); {
		switch {
		case q[i] == '\'':
			j := strings.IndexByte(q[i+1:], '\'')
			if j < 0 {
				return "", false
			}
			out.WriteString(q[i+1 : i+1+j])
			i += j + 2
		case strings.HasPrefix(q[i:], `\'`):
			out.WriteByte('\'')
			i += 2
		default:
			return "", false
		}
	}
	return out.String(), true
}

func FuzzShellQuote(f *testing.F) {
	for _, s := range []string{"", "plain", "it's", "';touch x;'", "a\nb", "$(x)", "`y`", `\`, "'''"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, ok := unquoteShell(shellQuote(s))
		if !ok || got != s {
			t.Fatalf("shellQuote(%q) = %q does not decode back (got %q ok=%v)", s, shellQuote(s), got, ok)
		}
	})
}

// FuzzPSQuote checks that no single-quote character of any kind appears
// unescaped inside the quoted string.
func FuzzPSQuote(f *testing.F) {
	for _, s := range []string{"", "plain", "it's", "'; calc; '", "\u2018x\u2019", "a\nb"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := psQuote(s)
		if len(q) < 2 || q[0] != '\'' || q[len(q)-1] != '\'' {
			t.Fatalf("psQuote(%q) = %q is not wrapped in quotes", s, q)
		}
		inner := q[1 : len(q)-1]
		for _, r := range []string{"\u2018", "\u2019", "\u201a", "\u201b"} {
			if strings.Contains(inner, r) {
				t.Fatalf("psQuote(%q) kept typographic quote %U", s, []rune(r)[0])
			}
		}
		if strings.Count(inner, "'")%2 != 0 || strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
			t.Fatalf("psQuote(%q) = %q has an unescaped quote", s, q)
		}
	})
}

// The default bypass list yields to one the user defined, in whatever form.
func TestDropIfUserDefined_RecognisesCommonDefinitionForms(t *testing.T) {
	body := []string{"export http_proxy='http://p:1'", "export no_proxy='localhost,127.0.0.1,::1'", "export NO_PROXY='localhost,127.0.0.1,::1'"}
	defined := map[string]string{
		"export":            "export no_proxy=corp.local\n",
		"export quoted":     "export NO_PROXY='corp.local'\n",
		"plain assignment":  "no_proxy=corp.local\nexport no_proxy\n",
		"declare -x":        "declare -x NO_PROXY=corp.local\n",
		"typeset -x":        "typeset -x no_proxy=corp.local\n",
		"indented":          "  export no_proxy=corp.local\n",
		"powershell":        "$env:NO_PROXY = 'corp.local'\n",
		"after other lines": "alias ll='ls -l'\nexport PATH=$HOME/bin:$PATH\nNO_PROXY=corp.local\n",
	}
	for name, content := range defined {
		got := dropIfUserDefined(body, content, noProxyNames)
		if len(got) != 1 || !strings.Contains(got[0], "http_proxy") {
			t.Errorf("%s: body = %q, want only the http_proxy line", name, got)
		}
	}

	notDefined := map[string]string{
		"comment":            "# export no_proxy=corp.local\n",
		"another variable":   "export no_proxy_old=corp.local\nexport MY_NO_PROXY=x\n",
		"value mentions it":  "echo no_proxy=corp.local\n",
		"empty":              "",
		"managed block only": "# >>> go-sysproxy >>>\nexport no_proxy='x'\n# <<< go-sysproxy <<<\n",
	}
	for name, content := range notDefined {
		if got := dropIfUserDefined(body, content, noProxyNames); len(got) != len(body) {
			t.Errorf("%s: body = %q, want it unchanged", name, got)
		}
	}
}
