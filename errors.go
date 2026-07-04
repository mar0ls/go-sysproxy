package sysproxy

import "errors"

// Sentinel errors. Callers can match with errors.Is:
//
//	if errors.Is(err, sysproxy.ErrProxyNotSet) { ... }
var (
	// ErrProxyNotSet is returned by Get / GetConfig when no manual proxy is
	// configured on the current system.
	ErrProxyNotSet = errors.New("sysproxy: proxy not set")

	// ErrProxyNotEnabled is returned by Get / GetConfig when the proxy entry
	// exists in the OS store but is currently disabled (e.g. Windows registry
	// ProxyEnable=0 without an AutoConfigURL).
	ErrProxyNotEnabled = errors.New("sysproxy: proxy not enabled")

	// ErrUnsupportedPlatform is returned by backends on operating systems that
	// have no sysproxy implementation compiled in.
	ErrUnsupportedPlatform = errors.New("sysproxy: unsupported platform")

	// ErrToolMissing is returned when a required external tool
	// (gsettings, networksetup, reg, git, npm, ...) is not available in PATH.
	ErrToolMissing = errors.New("sysproxy: required tool not found in PATH")
)

// nonCriticalError wraps errors from operations that do not invalidate the
// overall request. For example, /etc/environment on Linux requires root; if
// the rest of the configuration (GNOME/KDE/process env) succeeded, the caller
// should be able to distinguish that warning from a hard failure.
type nonCriticalError struct {
	err error
}

func (e *nonCriticalError) Error() string {
	if e == nil || e.err == nil {
		return "sysproxy: non-critical error"
	}
	return e.err.Error()
}

func (e *nonCriticalError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// IsNonCritical reports whether err is a non-fatal warning emitted by sysproxy
// (for example, failure to write /etc/environment without root privileges).
// When true, the rest of the operation should be considered successful.
func IsNonCritical(err error) bool {
	if err == nil {
		return false
	}
	var nc *nonCriticalError
	return errors.As(err, &nc)
}

// elevationError wraps errors that indicate the operation would have succeeded
// with elevated privileges (root on Unix, Administrator on Windows).
type elevationError struct {
	err error
}

func (e *elevationError) Error() string {
	if e == nil || e.err == nil {
		return "sysproxy: elevation required"
	}
	return e.err.Error()
}

func (e *elevationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// RequiresElevation reports whether err indicates the operation could not
// complete because elevated privileges were needed (e.g. writing
// /etc/environment on Linux without root). Errors that satisfy this predicate
// also satisfy IsNonCritical.
func RequiresElevation(err error) bool {
	if err == nil {
		return false
	}
	var ee *elevationError
	return errors.As(err, &ee)
}

// asElevationError wraps err so that both RequiresElevation and IsNonCritical
// return true. Backends use it when a permission error blocked one step but
// the rest of the operation succeeded.
func asElevationError(err error) error {
	if err == nil {
		return nil
	}
	return &nonCriticalError{err: &elevationError{err: err}}
}
