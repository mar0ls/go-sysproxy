package sysproxy

import "errors"

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
