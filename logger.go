package sysproxy

import (
	"fmt"
	"sync"
)

// Logger receives audit messages emitted by sysproxy operations.
// It is satisfied by any logger that exposes a single Log(string) method,
// making it easy to wrap slog, zap, zerolog, or the standard log package:
//
//	type slogAdapter struct{ l *slog.Logger }
//	func (a slogAdapter) Log(msg string) { a.l.Info(msg) }
//	sysproxy.SetLogger(slogAdapter{slog.Default()})
type Logger interface {
	Log(msg string)
}

var (
	logMu     sync.RWMutex
	globalLog Logger
)

// SetLogger installs l as the global logger for all sysproxy operations.
// Pass nil to disable logging (the default).
func SetLogger(l Logger) {
	logMu.Lock()
	globalLog = l
	logMu.Unlock()
}

func logf(format string, args ...any) {
	logMu.RLock()
	l := globalLog
	logMu.RUnlock()
	if l != nil {
		l.Log(fmt.Sprintf(format, args...))
	}
}
