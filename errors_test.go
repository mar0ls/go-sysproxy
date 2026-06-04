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
