package buildinfo

import "fmt"

// Injected via -ldflags at build time.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Summary returns a one-line build description for CLI output and logs.
func Summary() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, BuildDate)
}
