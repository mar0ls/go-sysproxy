//go:build !linux && !darwin && !windows

package sysproxy

import (
	"context"
	"fmt"
	"runtime"
)

// errUnsupported wraps ErrUnsupportedPlatform with the current GOOS so callers
// can use errors.Is while still getting a specific message.

func setUser(_ string) error           { return errUnsupported() }
func unsetUser() error                 { return errUnsupported() }
func setUserPAC(_ string) error        { return errUnsupported() }
func setUserMulti(_ ProxyConfig) error { return errUnsupported() }

// otherBackend implements globalBackend for unsupported operating systems.
type otherBackend struct{}

func (otherBackend) SetGlobal(_ context.Context, _ *proxy) error { return errUnsupported() }
func (otherBackend) UnsetGlobal(_ context.Context) error         { return errUnsupported() }
func (otherBackend) GetGlobal(_ context.Context) (string, error) { return "", errUnsupported() }
func (otherBackend) GetGlobalConfig(_ context.Context) (ProxyConfig, error) {
	return ProxyConfig{}, errUnsupported()
}
func (otherBackend) SetGlobalPAC(_ context.Context, _ string) error        { return errUnsupported() }
func (otherBackend) SetGlobalMulti(_ context.Context, _ ProxyConfig) error { return errUnsupported() }

func init() { activeBackend = otherBackend{} }

func errUnsupported() error {
	return fmt.Errorf("%w %q", ErrUnsupportedPlatform, runtime.GOOS)
}
