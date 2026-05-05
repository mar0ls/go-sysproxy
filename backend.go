package sysproxy

import "context"

// globalBackend is the interface for OS-specific proxy operations.
// Platform files register an implementation via init(); tests swap it with a mock.
type globalBackend interface {
	SetGlobal(ctx context.Context, p *proxy) error
	UnsetGlobal(ctx context.Context) error
	GetGlobal(ctx context.Context) (string, error)
	GetGlobalConfig(ctx context.Context) (ProxyConfig, error)
	SetGlobalPAC(ctx context.Context, pacURL string) error
	SetGlobalMulti(ctx context.Context, cfg ProxyConfig) error
}

// activeBackend is the platform implementation used at runtime.
// Tests may replace it with a mock via useMockBackend.
var activeBackend globalBackend
