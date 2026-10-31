package sysproxy

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Everything go-sysproxy writes into a file it does not own (shell rc files,
// the PowerShell profile, /etc/environment) lives between these two marker
// lines. The block is replaced as a unit and removed as a unit, so a user's own
// lines are never touched.
const (
	blockBegin = "# >>> go-sysproxy >>>"
	blockEnd   = "# <<< go-sysproxy <<<"
)

const defaultNoProxy = "localhost,127.0.0.1,::1"

// envPair is one environment variable to write; order is preserved in a block.
type envPair struct{ name, value string }

// shellQuote quotes s as a single POSIX shell word that expands to exactly s.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote quotes s as a PowerShell single-quoted string. PowerShell treats the
// typographic single quotes U+2018..U+201B like ASCII ', so all of them are
// neutralised.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteString("''")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

type lineRange struct{ start, end int }

// findBlocks returns the complete begin..end ranges in lines. A begin marker
// without a matching end is ignored, so a damaged file never causes the lines
// after it to be treated as ours.
func findBlocks(lines []string) []lineRange {
	var blocks []lineRange
	start := -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case blockBegin:
			start = i
		case blockEnd:
			if start >= 0 {
				blocks = append(blocks, lineRange{start, i})
				start = -1
			}
		}
	}
	return blocks
}

// blockBody returns the lines inside the first managed block of content.
func blockBody(content string) []string {
	lines := splitLines(content)
	blocks := findBlocks(lines)
	if len(blocks) == 0 {
		return nil
	}
	var body []string
	for _, l := range lines[blocks[0].start+1 : blocks[0].end] {
		body = append(body, strings.TrimSuffix(l, "\r"))
	}
	return body
}

// updateBlock returns content with its managed block(s) replaced by body. An
// empty body removes the block. The block is appended when none exists. When
// nothing needs to change the input is returned byte for byte.
func updateBlock(content string, body []string) string {
	lines := splitLines(content)
	blocks := findBlocks(lines)
	if len(blocks) == 0 && len(body) == 0 {
		return content
	}

	eol := ""
	if strings.Contains(content, "\r\n") {
		eol = "\r"
	}
	var block []string
	if len(body) > 0 {
		block = append(block, blockBegin+eol)
		for _, l := range body {
			block = append(block, l+eol)
		}
		block = append(block, blockEnd+eol)
	}

	skip := make([]bool, len(lines))
	for _, b := range blocks {
		for i := b.start; i <= b.end; i++ {
			skip[i] = true
		}
	}
	var out []string
	inserted := false
	for i, l := range lines {
		if skip[i] {
			if !inserted && block != nil && i == blocks[0].start {
				out = append(out, block...)
				inserted = true
			}
			continue
		}
		out = append(out, l)
	}
	if !inserted && block != nil {
		out = append(out, block...)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

var (
	varNameRE = regexp.MustCompile(`^(?:export\s+|\$env:)([A-Za-z_][A-Za-z0-9_]*)\s*=`)

	// legacyLineRE matches the exact lines written by go-sysproxy <= 0.5, which
	// had no markers: `export NAME='…'` for shells, `$env:NAME = '…'` for
	// PowerShell. no_proxy/NO_PROXY are only matched with the default value so
	// a user's own bypass list survives.
	legacyLineRE = regexp.MustCompile(
		`^(?:export\s+|\$env:)(?:(?:http|https|all)_proxy|(?:HTTP|HTTPS|ALL)_PROXY|AUTOPROXY)\s*=\s*'.*'\s*$`)
	legacyNoProxyRE = regexp.MustCompile(
		`^(?:export\s+|\$env:)(?:no_proxy|NO_PROXY)\s*=\s*'localhost,127\.0\.0\.1,::1'\s*$`)
)

// userVarRE matches the ways a user's own start-up file defines a variable:
// export NAME=, declare -x NAME=, typeset -x NAME=, readonly NAME=, a plain
// NAME= assignment and PowerShell's $env:NAME =.
var userVarRE = regexp.MustCompile(`^\s*(?:(?:export|declare\s+-x|typeset\s+-x|readonly)\s+|\$env:)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// definedVar returns the variable a line of the user's own file assigns, or "".
func definedVar(line string) string {
	if m := userVarRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

func varName(line string) string {
	if m := varNameRE.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// mergeBody returns old without the variables that updated redefines, followed
// by updated. It implements "fields left empty are not changed" for SetMulti.
func mergeBody(old, updated []string) []string {
	names := make(map[string]bool, len(updated))
	for _, l := range updated {
		if n := varName(l); n != "" {
			names[n] = true
		}
	}
	var out []string
	for _, l := range old {
		if n := varName(l); n != "" && names[n] {
			continue
		}
		out = append(out, l)
	}
	return append(out, updated...)
}

// removeLegacyLines drops the unmarked lines older versions wrote. It returns
// content unchanged when there are none.
func removeLegacyLines(content string) string {
	lines := splitLines(content)
	kept := lines[:0:0]
	for _, l := range lines {
		t := strings.TrimRight(l, "\r")
		if legacyLineRE.MatchString(t) || legacyNoProxyRE.MatchString(t) {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == len(lines) {
		return content
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// noProxyNames are the bypass-list variables. The default value go-sysproxy
// writes for them yields to a definition the user already has.
var noProxyNames = []string{"no_proxy", "NO_PROXY"}

// dropIfUserDefined removes from body the lines for names, when the part of
// content outside the managed block already defines any of names.
func dropIfUserDefined(body []string, content string, names []string) []string {
	userDefined := false
	for _, l := range splitLines(updateBlock(content, nil)) {
		if v := definedVar(l); v != "" && containsString(names, v) {
			userDefined = true
			break
		}
	}
	if !userDefined {
		return body
	}
	kept := body[:0:0]
	for _, l := range body {
		if !containsString(names, varName(l)) {
			kept = append(kept, l)
		}
	}
	return kept
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// applyBlockFile writes body as the managed block of path, creating the file
// (mode 0600) if needed. With merge, variables already in the block that body
// does not redefine are kept. Lines for the variables in yield are left out
// when the user's own part of the file already defines any of them.
func applyBlockFile(path string, body []string, merge bool, yield ...string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	if len(yield) > 0 {
		body = dropIfUserDefined(body, content, yield)
	}
	if merge {
		body = mergeBody(blockBody(content), body)
	}
	return writeFileAtomic(path, []byte(updateBlock(content, body)), 0o600)
}

// removeBlockFile removes the managed block and any legacy unmarked proxy lines
// from path. A missing file, or one with nothing to remove, is left untouched.
func removeBlockFile(path string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	content := string(data)
	out := removeLegacyLines(updateBlock(content, nil))
	if out == content {
		return nil
	}
	return writeFileAtomic(path, []byte(out), 0o600)
}

// blockSnapshot records how one file looked before WithProxy: whether it
// existed and what its managed block contained (nil = no block).
type blockSnapshot struct {
	path    string
	existed bool
	body    []string
}

func snapshotBlockFiles(paths []string) ([]blockSnapshot, error) {
	fileMu.Lock()
	defer fileMu.Unlock()

	snaps := make([]blockSnapshot, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec
		switch {
		case err == nil:
			snaps = append(snaps, blockSnapshot{path: path, existed: true, body: blockBody(string(data))})
		case os.IsNotExist(err):
			snaps = append(snaps, blockSnapshot{path: path})
		default:
			return nil, err
		}
	}
	return snaps, nil
}

// restore puts the managed block back the way it was. Lines outside the block
// are never touched; a file that did not exist before and is empty again is
// removed.
func (s blockSnapshot) restore() error {
	fileMu.Lock()
	defer fileMu.Unlock()

	data, err := os.ReadFile(s.path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	out := updateBlock(content, s.body)
	switch {
	case exists && out == content, !exists && out == "":
		return nil
	case exists && out == "" && !s.existed:
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(s.path, []byte(out), 0o600)
}

func restoreBlockFiles(snaps []blockSnapshot) error {
	var errs []error
	for _, s := range snaps {
		if err := s.restore(); err != nil {
			errs = append(errs, fmt.Errorf("sysproxy: restore %s: %w", s.path, err))
		}
	}
	return errors.Join(errs...)
}
