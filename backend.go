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

	// SnapshotGlobal captures the system proxy state in the backend's native
	// terms and returns a function that puts it back exactly — including
	// fields that were empty — so WithProxy can undo a temporary change.
	SnapshotGlobal(ctx context.Context) (restoreFunc, error)
}

// activeBackend is the platform implementation used at runtime.
// Tests may replace it with a mock via useMockBackend.
var activeBackend globalBackend
