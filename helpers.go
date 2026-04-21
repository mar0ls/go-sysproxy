package sysproxy

import (
	"net/url"
	"os/exec"
)

func isAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func portFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Port()
}
