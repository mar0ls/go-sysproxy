//go:build !linux && !darwin && !windows

package sysproxy

import (
	"context"
	"fmt"
	"runtime"
)

func setGlobal(_ context.Context, _ *proxy) error    { return errUnsupported() }
func unsetGlobal(_ context.Context) error            { return errUnsupported() }
func getGlobal(_ context.Context) (string, error)    { return "", errUnsupported() }
func setGlobalPAC(_ context.Context, _ string) error { return errUnsupported() }
func setGlobalMulti(_ context.Context, _ ProxyConfig) error {
	return errUnsupported()
}
func setUser(_ string) error           { return errUnsupported() }
func unsetUser() error                 { return errUnsupported() }
func setUserPAC(_ string) error        { return errUnsupported() }
func setUserMulti(_ ProxyConfig) error { return errUnsupported() }

func errUnsupported() error {
	return fmt.Errorf("sysproxy: unsupported OS %q", runtime.GOOS)
}
