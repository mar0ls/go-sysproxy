package buildinfo

import (
	"strings"
	"testing"
)

func TestSummary_Defaults(t *testing.T) {
	got := Summary()
	for _, want := range []string{Version, "commit " + Commit, "built " + BuildDate} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() = %q, missing %q", got, want)
		}
	}
}

func TestSummary_HonoursOverrides(t *testing.T) {
	origV, origC, origD := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = origV, origC, origD })

	Version = "v1.2.3"
	Commit = "deadbeef"
	BuildDate = "2026-07-03"

	want := "v1.2.3 (commit deadbeef, built 2026-07-03)"
	if got := Summary(); got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}
