package sysproxy

import (
	"context"
	"testing"
)

// mockBackend is a test double for globalBackend.
// Any nil field falls back to a no-op that returns nil.
type mockBackend struct {
	setGlobalFn       func(ctx context.Context, p *proxy) error
	unsetGlobalFn     func(ctx context.Context) error
	getGlobalFn       func(ctx context.Context) (string, error)
	getGlobalConfigFn func(ctx context.Context) (ProxyConfig, error)
	setGlobalPACFn    func(ctx context.Context, pacURL string) error
	setGlobalMultiFn  func(ctx context.Context, cfg ProxyConfig) error
}

func (m *mockBackend) SetGlobal(ctx context.Context, p *proxy) error {
	if m.setGlobalFn != nil {
		return m.setGlobalFn(ctx, p)
	}
	return nil
}

func (m *mockBackend) UnsetGlobal(ctx context.Context) error {
	if m.unsetGlobalFn != nil {
		return m.unsetGlobalFn(ctx)
	}
	return nil
}

func (m *mockBackend) GetGlobal(ctx context.Context) (string, error) {
	if m.getGlobalFn != nil {
		return m.getGlobalFn(ctx)
	}
	return "", nil
}

func (m *mockBackend) GetGlobalConfig(ctx context.Context) (ProxyConfig, error) {
	if m.getGlobalConfigFn != nil {
		return m.getGlobalConfigFn(ctx)
	}
	return ProxyConfig{}, nil
}

func (m *mockBackend) SetGlobalPAC(ctx context.Context, pacURL string) error {
	if m.setGlobalPACFn != nil {
		return m.setGlobalPACFn(ctx, pacURL)
	}
	return nil
}

func (m *mockBackend) SetGlobalMulti(ctx context.Context, cfg ProxyConfig) error {
	if m.setGlobalMultiFn != nil {
		return m.setGlobalMultiFn(ctx, cfg)
	}
	return nil
}

// useMockBackend replaces activeBackend for the duration of test t and
// restores the original on cleanup.
func useMockBackend(t *testing.T, m *mockBackend) {
	t.Helper()
	orig := activeBackend
	activeBackend = m
	t.Cleanup(func() { activeBackend = orig })
}
