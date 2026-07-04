package sysproxy

import (
	"errors"
	"testing"
)

func TestIsNonCritical(t *testing.T) {
	if IsNonCritical(nil) {
		t.Fatal("nil error must not be non-critical")
	}

	plain := errors.New("plain")
	if IsNonCritical(plain) {
		t.Fatal("plain error must not be non-critical")
	}

	nc := &nonCriticalError{err: plain}
	if !IsNonCritical(nc) {
		t.Fatal("nonCriticalError must be detected")
	}

	wrapped := errors.New("wrapper: " + nc.Error())
	if IsNonCritical(wrapped) {
		t.Fatal("string-wrapped error must not be detected via errors.As")
	}
}

func TestNonCriticalErrorMethods(t *testing.T) {
	base := errors.New("permission denied")
	nc := &nonCriticalError{err: base}

	if got := nc.Error(); got != "permission denied" {
		t.Fatalf("Error() = %q, want %q", got, "permission denied")
	}
	if !errors.Is(nc, base) {
		t.Fatal("Unwrap should expose wrapped error")
	}

	var nilNC *nonCriticalError
	if got := nilNC.Error(); got == "" {
		t.Fatal("nil receiver Error() should return fallback message")
	}
	if nilNC.Unwrap() != nil {
		t.Fatal("nil receiver Unwrap() should return nil")
	}
}

func TestRequiresElevation(t *testing.T) {
	if RequiresElevation(nil) {
		t.Fatal("nil error must not require elevation")
	}
	if RequiresElevation(errors.New("plain")) {
		t.Fatal("plain error must not require elevation")
	}

	base := errors.New("permission denied")
	err := asElevationError(base)
	if !RequiresElevation(err) {
		t.Fatal("elevationError should be detected")
	}
	if !IsNonCritical(err) {
		t.Fatal("elevationError should also be non-critical")
	}
	if !errors.Is(err, base) {
		t.Fatal("elevationError should unwrap to underlying cause")
	}

	// asElevationError(nil) must be nil.
	if asElevationError(nil) != nil {
		t.Fatal("asElevationError(nil) should return nil")
	}
}

func TestElevationErrorMethods(t *testing.T) {
	base := errors.New("permission denied")
	ee := &elevationError{err: base}
	if got := ee.Error(); got != "permission denied" {
		t.Fatalf("Error() = %q, want %q", got, "permission denied")
	}
	if !errors.Is(ee, base) {
		t.Fatal("elevationError.Unwrap should expose wrapped error")
	}

	var nilEE *elevationError
	if got := nilEE.Error(); got == "" {
		t.Fatal("nil receiver Error() should return fallback message")
	}
	if nilEE.Unwrap() != nil {
		t.Fatal("nil receiver Unwrap() should return nil")
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	// Distinct instances so callers can errors.Is precisely.
	for _, e := range []error{ErrProxyNotSet, ErrProxyNotEnabled, ErrUnsupportedPlatform, ErrToolMissing} {
		if e == nil {
			t.Fatal("sentinel error unexpectedly nil")
		}
	}
	if errors.Is(ErrProxyNotSet, ErrProxyNotEnabled) {
		t.Fatal("distinct sentinels must not compare equal")
	}
}
