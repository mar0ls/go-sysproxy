package sysproxy

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// THREAT_MODEL.md is a document that has to stay true. These tests read it and
// check what a machine can check: the structure of the threat register, and
// that every source file, workflow, script and test it names exists. Whether a
// mitigation is adequate stays a review decision.

const threatModelFile = "THREAT_MODEL.md"

var (
	tmRatings = map[string]int{"None": 0, "Low": 1, "Medium": 2, "High": 3, "Critical": 4}
	tmStatus  = map[string]bool{"Mitigated": true, "Partial": true, "Accepted": true, "Open": true}

	tmThreatHeader   = []string{"ID", "Threat", "Component", "Inherent", "Controls", "Evidence", "Residual", "Status"}
	tmControlHeader  = []string{"ID", "Control", "Implementation", "Evidence"}
	tmResidualHeader = []string{"ID", "Residual risk", "Rationale", "Next step"}

	tmThreatID  = regexp.MustCompile(`^[STRIDE][0-9]+$`)
	tmControlID = regexp.MustCompile(`^C[0-9]+$`)
	tmTestName  = regexp.MustCompile(`^(?:Test|Fuzz)[A-Za-z0-9_]+$`)
	tmFileRef   = regexp.MustCompile(`^[A-Za-z0-9_./-]+\.(?:go|md|yml|yaml|sh|ps1|mod)$`)
	tmBackticks = regexp.MustCompile("`([^`]+)`")
	tmSeparator = regexp.MustCompile(`^[\s|:-]+$`)
	tmTestDecl  = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]+)\(`)
)

type tmRow struct {
	line  int
	cells []string
}

type tmTable struct {
	line   int
	header []string
	rows   []tmRow
}

// parseTmTables returns the pipe tables of doc, ignoring fenced code blocks.
func parseTmTables(doc string) []tmTable {
	var tables []tmTable
	var cur []tmRow
	flush := func() {
		if len(cur) >= 2 && tmSeparator.MatchString(strings.Join(cur[1].cells, "|")) {
			tables = append(tables, tmTable{line: cur[0].line, header: cur[0].cells, rows: cur[2:]})
		}
		cur = nil
	}
	inFence := false
	for i, raw := range strings.Split(doc, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if strings.HasPrefix(line, "```") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "|") {
			flush()
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		cur = append(cur, tmRow{line: i + 1, cells: cells})
	}
	flush()
	return tables
}

func sameHeader(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// backticked returns the code spans of a table cell.
func backticked(cell string) []string {
	var out []string
	for _, m := range tmBackticks.FindAllStringSubmatch(cell, -1) {
		out = append(out, m[1])
	}
	return out
}

// checkThreatModel returns every way doc is inconsistent with itself or with the
// repository: testNames are the Test and Fuzz functions that exist, fileExists
// says whether a repository-relative path exists.
func checkThreatModel(doc string, testNames map[string]bool, fileExists func(string) bool) []string {
	var problems []string
	add := func(line int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s:%d: %s", threatModelFile, line, fmt.Sprintf(format, args...)))
	}

	// Every file and test named anywhere in the document must exist.
	inFence := false
	for i, raw := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(raw), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, tok := range backticked(raw) {
			switch {
			case tmFileRef.MatchString(tok):
				if !fileExists(tok) {
					add(i+1, "file %q does not exist", tok)
				}
			case tmTestName.MatchString(tok):
				if !testNames[tok] {
					add(i+1, "test %q does not exist", tok)
				}
			}
		}
	}

	var threats, controls, residual []tmTable
	for _, t := range parseTmTables(doc) {
		switch {
		case sameHeader(t.header, tmThreatHeader):
			threats = append(threats, t)
		case sameHeader(t.header, tmControlHeader):
			controls = append(controls, t)
		case sameHeader(t.header, tmResidualHeader):
			residual = append(residual, t)
		case len(t.header) > 1 && (t.header[1] == "Threat" || t.header[1] == "Control" || t.header[1] == "Residual risk"):
			add(t.line, "table header %v is not the expected column layout", t.header)
		}
	}
	if len(threats) == 0 || len(controls) != 1 || len(residual) != 1 {
		return append(problems, fmt.Sprintf("%s: expected threat tables, one control table and one residual-risk table, got %d, %d and %d",
			threatModelFile, len(threats), len(controls), len(residual)))
	}

	// Controls.
	controlIDs := map[string]bool{}
	for _, r := range controls[0].rows {
		if len(r.cells) != len(tmControlHeader) {
			add(r.line, "control row has %d cells, want %d", len(r.cells), len(tmControlHeader))
			continue
		}
		id := r.cells[0]
		switch {
		case !tmControlID.MatchString(id):
			add(r.line, "control id %q is not C<number>", id)
		case controlIDs[id]:
			add(r.line, "control %s is defined twice", id)
		}
		controlIDs[id] = true
		if len(backticked(r.cells[2])) == 0 {
			add(r.line, "control %s names no implementation file", id)
		}
		if !hasTests(r.cells[3]) {
			add(r.line, "control %s cites no test", id)
		}
		for _, tok := range backticked(r.cells[3]) {
			if !tmTestName.MatchString(tok) {
				add(r.line, "control %s: evidence %q is not a test", id, tok)
			}
		}
	}

	// Threats.
	threatStatus := map[string]string{}
	usedControls := map[string]bool{}
	for _, t := range threats {
		for _, r := range t.rows {
			if len(r.cells) != len(tmThreatHeader) {
				add(r.line, "threat row has %d cells, want %d", len(r.cells), len(tmThreatHeader))
				continue
			}
			id, inherent, ctrls, evidence, resid, status := r.cells[0], r.cells[3], r.cells[4], r.cells[5], r.cells[6], r.cells[7]
			if !tmThreatID.MatchString(id) {
				add(r.line, "threat id %q is not <S|T|R|I|D|E><number>", id)
			}
			if _, dup := threatStatus[id]; dup {
				add(r.line, "threat %s is defined twice", id)
			}
			threatStatus[id] = status

			ri, okI := tmRatings[inherent]
			rr, okR := tmRatings[resid]
			if !okI || inherent == "None" {
				add(r.line, "%s: inherent risk %q is not Low, Medium, High or Critical", id, inherent)
			}
			if !okR {
				add(r.line, "%s: residual risk %q is not None, Low, Medium, High or Critical", id, resid)
			}
			if okI && okR && rr > ri {
				add(r.line, "%s: residual risk %s is higher than the inherent risk %s", id, resid, inherent)
			}
			if !tmStatus[status] {
				add(r.line, "%s: status %q is not Mitigated, Partial, Accepted or Open", id, status)
			}
			if status == "Mitigated" && okR && rr > tmRatings["Medium"] {
				add(r.line, "%s: a Mitigated threat cannot keep a %s residual risk", id, resid)
			}

			var cited []string
			for _, c := range strings.Split(ctrls, ",") {
				if c = strings.TrimSpace(c); c != "" {
					cited = append(cited, c)
				}
			}
			for _, c := range cited {
				if !controlIDs[c] {
					add(r.line, "%s: control %q is not in the control catalogue", id, c)
				}
				usedControls[c] = true
			}
			for _, tok := range backticked(evidence) {
				if !tmTestName.MatchString(tok) {
					add(r.line, "%s: evidence %q is not a test", id, tok)
				}
			}
			if status == "Mitigated" || status == "Partial" {
				if len(cited) == 0 {
					add(r.line, "%s: a %s threat names no control", id, status)
				}
				if !hasTests(evidence) {
					add(r.line, "%s: a %s threat cites no test", id, status)
				}
			}
		}
	}
	for id := range controlIDs {
		if !usedControls[id] {
			problems = append(problems, fmt.Sprintf("%s: control %s is not used by any threat", threatModelFile, id))
		}
	}

	// Residual-risk register: exactly the threats that are not Mitigated.
	listed := map[string]bool{}
	for _, r := range residual[0].rows {
		if len(r.cells) != len(tmResidualHeader) {
			add(r.line, "residual row has %d cells, want %d", len(r.cells), len(tmResidualHeader))
			continue
		}
		id := r.cells[0]
		st, known := threatStatus[id]
		switch {
		case !known:
			add(r.line, "residual risk %s is not a threat in the register", id)
		case st == "Mitigated":
			add(r.line, "residual risk %s is listed but the threat is Mitigated", id)
		}
		if listed[id] {
			add(r.line, "residual risk %s is listed twice", id)
		}
		listed[id] = true
		if r.cells[2] == "" || r.cells[3] == "" {
			add(r.line, "residual risk %s needs a rationale and a next step", id)
		}
	}
	var missing []string
	for id, st := range threatStatus {
		if st != "Mitigated" && tmStatus[st] && !listed[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		problems = append(problems, fmt.Sprintf("%s: threat %s is %s but missing from the residual-risk table", threatModelFile, id, threatStatus[id]))
	}
	return problems
}

func hasTests(cell string) bool {
	for _, tok := range backticked(cell) {
		if tmTestName.MatchString(tok) {
			return true
		}
	}
	return false
}

// repoTestNames returns every Test and Fuzz function declared in the module,
// whatever its build tags, so a test that only exists on Windows still counts.
func repoTestNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // walking the module's own tree
		if err != nil {
			return err
		}
		for _, m := range tmTestDecl.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// TestThreatModel_MatchesCode keeps THREAT_MODEL.md from drifting away from the
// code: see section 9 of the document for what it enforces.
func TestThreatModel_MatchesCode(t *testing.T) {
	doc, err := os.ReadFile(threatModelFile)
	if err != nil {
		t.Fatalf("read %s: %v", threatModelFile, err)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.FromSlash(p)); return err == nil }
	for _, p := range checkThreatModel(string(doc), repoTestNames(t), exists) {
		t.Error(p)
	}
}

// The checker itself is tested on small documents: every rule has a case that
// must be reported, and the valid document must not be.
func TestThreatModelChecker(t *testing.T) {
	const valid = `
## Controls
| ID | Control | Implementation | Evidence |
|----|---------|----------------|----------|
| C1 | Validate | ` + "`validate.go`" + ` | ` + "`TestA`" + ` |
| C2 | Quote | ` + "`managedblock.go`" + ` | ` + "`TestB`" + ` |

## Threats
| ID | Threat | Component | Inherent | Controls | Evidence | Residual | Status |
|----|--------|-----------|----------|----------|----------|----------|--------|
| T1 | Injection | ` + "`validate.go`" + ` | High | C1, C2 | ` + "`TestA`" + `, ` + "`TestB`" + ` | Low | Mitigated |
| T2 | Crash | ` + "`validate.go`" + ` | Medium | C1 | ` + "`TestA`" + ` | Medium | Accepted |

## Residual
| ID | Residual risk | Rationale | Next step |
|----|---------------|-----------|-----------|
| T2 | A crash leaves state | defer does not run | document |
`
	tests := map[string]bool{"TestA": true, "TestB": true}
	exists := func(p string) bool { return p == "validate.go" || p == "managedblock.go" }

	if got := checkThreatModel(valid, tests, exists); len(got) != 0 {
		t.Fatalf("a valid document was reported: %v", got)
	}

	cases := []struct{ name, from, to, want string }{
		{"unknown status", "| Low | Mitigated |", "| Low | Done |", `status "Done"`},
		{"unknown rating", "| High | C1, C2 |", "| Huge | C1, C2 |", `inherent risk "Huge"`},
		{"residual above inherent", "| Medium | Accepted |", "| High | Accepted |", "higher than the inherent"},
		{"mitigated with a high residual", "| Low | Mitigated |", "| High | Mitigated |", "cannot keep a High residual"},
		{"missing test", "`TestB`" + ` | Low | Mitigated`, "`TestGone`" + ` | Low | Mitigated`, `test "TestGone" does not exist`},
		{"missing file", "| `managedblock.go` | `TestB` |", "| `gone.go` | `TestB` |", `file "gone.go" does not exist`},
		{"unknown control", "| High | C1, C2 |", "| High | C1, C9 |", `control "C9" is not in the control catalogue`},
		{"unused control", "| High | C1, C2 |", "| High | C1 |", "control C2 is not used by any threat"},
		{"mitigated without a control", "| High | C1, C2 |", "| High |  |", "names no control"},
		{"evidence that is not a test", "`TestA`, `TestB` | Low", "`validate.go` | Low", "is not a test"},
		{"duplicate id", "| T2 | Crash |", "| T1 | Crash |", "defined twice"},
		{"bad id", "| T2 | Crash |", "| X2 | Crash |", "is not <S|T|R|I|D|E><number>"},
		{"not mitigated but unlisted", "| T2 | A crash leaves state | defer does not run | document |", "| T9 | A crash leaves state | defer does not run | document |", "threat T2 is Accepted but missing"},
		{"listed although mitigated", "| T2 | A crash leaves state", "| T1 | A crash leaves state", "the threat is Mitigated"},
		{"residual without a next step", "| defer does not run | document |", "| defer does not run |  |", "needs a rationale and a next step"},
		{"wrong header", "| ID | Threat | Component | Inherent |", "| ID | Threat | Where | Inherent |", "not the expected column layout"},
	}
	for _, c := range cases {
		doc := strings.Replace(valid, c.from, c.to, 1)
		if doc == valid {
			t.Errorf("%s: the case does not change the document", c.name)
			continue
		}
		got := strings.Join(checkThreatModel(doc, tests, exists), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want a report containing %q, got:\n%s", c.name, c.want, got)
		}
	}

	// A fenced block is not parsed as a table and its code spans are not checked.
	fenced := valid + "\n```\n| ID | Threat |\n`gone.go` `TestGone`\n```\n"
	if got := checkThreatModel(fenced, tests, exists); len(got) != 0 {
		t.Errorf("a fenced block was inspected: %v", got)
	}
}
