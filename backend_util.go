package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// execError turns the failure of an external tool into an error that names the
// tool and the operation but never the full argument list — arguments can carry
// credentials. A missing binary is reported as ErrToolMissing.
func execError(tool, what string, err error, output []byte) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrToolMissing, tool)
	}
	msg := firstLine(output)
	if msg != "" {
		return fmt.Errorf("sysproxy: %s %s: %w: %s", tool, what, err, msg)
	}
	return fmt.Errorf("sysproxy: %s %s: %w", tool, what, err)
}

// firstLine returns the first non-empty line of out, shortened for use in an
// error message.
func firstLine(out []byte) string {
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 200 {
				l = l[:200] + "…"
			}
			return l
		}
	}
	return ""
}

// flatten drops the classification (non-critical / needs elevation) of err but
// keeps its message. It is used when err is folded into a hard failure: a
// joined error would otherwise still satisfy IsNonCritical and a caller that
// tolerates warnings would swallow the real failure.
func flatten(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(err.Error())
}

// combine returns the earlier error if the later one is nil and vice versa, so
// errors.Is/As keep working against a single error without a join wrapper.
func combine(first, second error) error {
	switch {
	case second == nil:
		return first
	case first == nil:
		return second
	default:
		return errors.Join(first, second)
	}
}

// splitNoProxy splits a comma or semicolon separated bypass list into trimmed,
// non-empty entries.
func splitNoProxy(s string) []string {
	var out []string
	for _, e := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// proxyEnvNames are the process environment variables the Set* functions
// write. WithProxy snapshots them so the process environment is restored
// exactly, including variables that were not set before.
var proxyEnvNames = []string{
	"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
	"all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY", "AUTOPROXY",
}

type envSnapshot struct {
	value map[string]string
	set   map[string]bool
}

func snapshotEnv() envSnapshot {
	s := envSnapshot{value: map[string]string{}, set: map[string]bool{}}
	for _, k := range proxyEnvNames {
		if v, ok := os.LookupEnv(k); ok {
			s.value[k], s.set[k] = v, true
		}
	}
	return s
}

func (s envSnapshot) restore() error {
	var errs []error
	for _, k := range proxyEnvNames {
		var err error
		if s.set[k] {
			err = os.Setenv(k, s.value[k])
		} else {
			err = os.Unsetenv(k)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// restoreFunc undoes a change; it is called once, with a context that is not
// canceled when the caller's is.
type restoreFunc func(ctx context.Context) error

func noRestore(context.Context) error { return nil }
