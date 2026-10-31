package sysproxy

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

// The /etc/environment logic lives in an untagged file so it can be tested on
// every platform; only the Linux backend calls it.

var etcProxyKeys = map[string]bool{
	"http_proxy": true, "HTTP_PROXY": true,
	"https_proxy": true, "HTTPS_PROXY": true,
	"all_proxy": true, "ALL_PROXY": true,
}

// errCredentialsOmitted is wrapped in a nonCriticalError when the proxy URL
// carried credentials that were deliberately not written to the file.
var errCredentialsOmitted = errors.New("sysproxy: credentials were not written to world-readable /etc/environment")

func envKey(line string) string {
	return strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
}

func envValue(line string) string {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// etcSanitizeURL strips userinfo and fragment from proxyURL. /etc/environment is
// world-readable (0644), so a password must never end up there.
func etcSanitizeURL(proxyURL string) (clean string, hadCredentials bool) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL, false
	}
	hadCredentials = u.User != nil
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	return u.String(), hadCredentials
}

// stripEtcOwnedLines removes our managed block and the unmarked proxy lines
// written by older versions. A no_proxy/NO_PROXY line is only removed when it
// carries the old default value, so an administrator's own bypass list is kept.
func stripEtcOwnedLines(content string) string {
	lines := splitLines(updateBlock(content, nil))
	kept := lines[:0:0]
	for _, l := range lines {
		key := envKey(l)
		if etcProxyKeys[key] {
			continue
		}
		if (key == "no_proxy" || key == "NO_PROXY") && envValue(l) == defaultNoProxy {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}

// writeEtcEnvironment writes the proxy variables for proxyURL into path inside
// a managed block, leaving every other line (including a custom NO_PROXY)
// alone. Credentials are stripped; in that case the returned error is
// non-critical and the rest of the file has been written.
func writeEtcEnvironment(path, proxyURL string) error {
	value, hadCreds := etcSanitizeURL(proxyURL)

	fileMu.Lock()
	defer fileMu.Unlock()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rest := stripEtcOwnedLines(string(data))

	existing := map[string]bool{}
	for _, l := range splitLines(rest) {
		existing[envKey(l)] = true
	}
	body := []string{
		"http_proxy=" + value, "HTTP_PROXY=" + value,
		"https_proxy=" + value, "HTTPS_PROXY=" + value,
		"all_proxy=" + value, "ALL_PROXY=" + value,
	}
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		if !existing[k] {
			body = append(body, k+"="+defaultNoProxy)
		}
	}

	if err := writeFileAtomic(path, []byte(updateBlock(rest, body)), 0o644); err != nil {
		return err
	}
	if hadCreds {
		return &nonCriticalError{err: errCredentialsOmitted}
	}
	return nil
}

// clearEtcEnvironment removes everything writeEtcEnvironment (or an older
// version) added. A missing file, or one with nothing to remove, is not an
// error and is not rewritten — so an unprivileged Unset only fails when there
// really is something it could not remove.
func clearEtcEnvironment(path string) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	out := stripEtcOwnedLines(string(data))
	if out == string(data) {
		return nil
	}
	return writeFileAtomic(path, []byte(out), 0o644)
}
