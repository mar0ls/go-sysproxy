//go:build !linux && !darwin && !windows

package sysproxy

import (
	"fmt"
	"runtime"
)

func setGlobal(_ *proxy) error            { return errUnsupported() }
func unsetGlobal() error                  { return errUnsupported() }
func getGlobal() (string, error)          { return "", errUnsupported() }
func setGlobalPAC(_ string) error         { return errUnsupported() }
func setGlobalMulti(_ ProxyConfig) error  { return errUnsupported() }
func setUser(_ string) error              { return errUnsupported() }
func unsetUser() error                    { return errUnsupported() }
func setUserPAC(_ string) error           { return errUnsupported() }
func setUserMulti(_ ProxyConfig) error    { return errUnsupported() }

func errUnsupported() error {
	return fmt.Errorf("sysproxy: unsupported OS %q", runtime.GOOS)
}
