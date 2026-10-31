package sysproxy

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// trackingBackend records the order of backend calls so tests can assert that
// WithProxy snapshots before it changes anything and restores afterwards.
type trackingBackend struct {
	*mockBackend
	log           *[]string
	restoreErr    error
	setErr        error
	snapErr       error
	restoreCtxErr error // ctx.Err() as seen inside restore
	restored      bool
}

func newTrackingBackend(t *testing.T) *trackingBackend {
	t.Helper()
	tb := &trackingBackend{mockBackend: &mockBackend{}, log: &[]string{}}
	m := tb.mockBackend
	m.snapshotGlobalFn = func(_ context.Context) (restoreFunc, error) {
		*tb.log = append(*tb.log, "snapshot")
		if tb.snapErr != nil {
			return nil, tb.snapErr
		}
		return func(ctx context.Context) error {
			*tb.log = append(*tb.log, "restore")
			tb.restored, tb.restoreCtxErr = true, ctx.Err()
			return tb.restoreErr
		}, nil
	}
	m.setGlobalFn = func(_ context.Context, p *proxy) error {
		*tb.log = append(*tb.log, "set "+p.rawURL)
		return tb.setErr
	}
	m.setGlobalMultiFn = func(_ context.Context, _ ProxyConfig) error {
		*tb.log = append(*tb.log, "setmulti")
		return tb.setErr
	}
	useMockBackend(t, m)
	return tb
}

func TestWithProxy_SnapshotThenSetThenFnThenRestore(t *testing.T) {
	tb := newTrackingBackend(t)
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error {
		*tb.log = append(*tb.log, "fn")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"snapshot", "set http://tmp:9999", "fn", "restore"}
	if !reflect.DeepEqual(*tb.log, want) {
		t.Fatalf("call order = %v, want %v", *tb.log, want)
	}
}

func TestWithProxyMulti_SnapshotThenSetThenFnThenRestore(t *testing.T) {
	tb := newTrackingBackend(t)
	err := WithProxyMulti(context.Background(), ProxyConfig{HTTP: "http://tmp:9999"}, ScopeGlobal, func(context.Context) error {
		*tb.log = append(*tb.log, "fn")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"snapshot", "setmulti", "fn", "restore"}
	if !reflect.DeepEqual(*tb.log, want) {
		t.Fatalf("call order = %v, want %v", *tb.log, want)
	}
}

func TestWithProxy_RestoresWhenFnFails(t *testing.T) {
	tb := newTrackingBackend(t)
	boom := errors.New("boom")
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want fn's error", err)
	}
	if last := (*tb.log)[len(*tb.log)-1]; last != "restore" {
		t.Fatalf("restore did not run: %v", *tb.log)
	}
}

func TestWithProxy_RestoresWhenFnPanics(t *testing.T) {
	tb := newTrackingBackend(t)
	func() {
		defer func() { _ = recover() }()
		_ = WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { panic("x") })
	}()
	if last := (*tb.log)[len(*tb.log)-1]; last != "restore" {
		t.Fatalf("restore did not run after panic: %v", *tb.log)
	}
}

// A Set that fails half way has already changed the system; restore must run
// and fn must not.
func TestWithProxy_RestoresWhenSetFails(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.setErr = errors.New("networksetup failed")
	called := false
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, tb.setErr) {
		t.Fatalf("err = %v, want the set error", err)
	}
	if called {
		t.Error("fn must not run when Set failed")
	}
	if last := (*tb.log)[len(*tb.log)-1]; last != "restore" {
		t.Fatalf("restore did not run after failed Set: %v", *tb.log)
	}
}

func TestWithProxy_NonCriticalSetStillRunsFn(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.setErr = &nonCriticalError{err: errors.New("/etc/environment not writable")}
	called := false
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !called {
		t.Error("fn must run when Set only produced a warning")
	}
}

func TestWithProxy_SnapshotFailureChangesNothing(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.snapErr = errors.New("cannot read settings")
	called := false
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, tb.snapErr) {
		t.Fatalf("err = %v, want the snapshot error", err)
	}
	if called || !reflect.DeepEqual(*tb.log, []string{"snapshot"}) {
		t.Fatalf("nothing may change after a failed snapshot; log = %v, fn called = %v", *tb.log, called)
	}
}

func TestWithProxy_RestoreErrorIsReported(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.restoreErr = errors.New("restore failed")

	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return nil })
	if !errors.Is(err, tb.restoreErr) {
		t.Fatalf("err = %v, want the restore error", err)
	}

	boom := errors.New("boom")
	err = WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return boom })
	if !errors.Is(err, boom) || !errors.Is(err, tb.restoreErr) {
		t.Fatalf("err = %v, want both fn's and the restore error", err)
	}
}

func TestWithProxy_RestoreRunsAfterContextCancel(t *testing.T) {
	tb := newTrackingBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	_ = WithProxy(ctx, "http://tmp:9999", ScopeGlobal, func(context.Context) error {
		cancel()
		return ctx.Err()
	})
	if !tb.restored {
		t.Fatal("restore did not run")
	}
	if err := tb.restoreCtxErr; err != nil {
		t.Fatalf("restore context is canceled (%v): the proxy would stay set", err)
	}
}

func TestWithProxy_InvalidInputsDoNotSnapshot(t *testing.T) {
	tb := newTrackingBackend(t)
	if err := WithProxy(context.Background(), "not a url", ScopeGlobal, func(context.Context) error { return nil }); err == nil {
		t.Error("invalid URL must be rejected")
	}
	if err := WithProxy(context.Background(), "http://x:1", ProxyScope(9), func(context.Context) error { return nil }); err == nil {
		t.Error("invalid scope must be rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WithProxy(ctx, "http://x:1", ScopeGlobal, func(context.Context) error { return nil }); err == nil {
		t.Error("canceled context must be rejected")
	}
	if len(*tb.log) != 0 {
		t.Errorf("backend was called: %v", *tb.log)
	}
}

// caseSensitiveEnv is false on Windows, where http_proxy and HTTP_PROXY are the
// same variable.
var caseSensitiveEnv = runtime.GOOS != "windows"

// F-11: the process environment is restored exactly — variables that were set
// come back with their values, variables that were not set are unset again.
func TestWithProxy_RestoresEnvironmentExactly(t *testing.T) {
	for _, scope := range []ProxyScope{ScopeShell, ScopeGlobal} {
		t.Run(scope.String(), func(t *testing.T) {
			newTrackingBackend(t)
			for _, k := range proxyEnvNames {
				t.Setenv(k, "") // register restoration of the test environment
				_ = os.Unsetenv(k)
			}
			t.Setenv("HTTP_PROXY", "http://preexisting:1")
			t.Setenv("NO_PROXY", "mine.internal")
			if caseSensitiveEnv {
				t.Setenv("http_proxy", "") // set, but empty: must stay set-and-empty
			}

			var inside string
			err := WithProxy(context.Background(), "http://tmp:9999", scope, func(context.Context) error {
				inside = os.Getenv("HTTP_PROXY")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if inside != "http://tmp:9999" {
				t.Errorf("HTTP_PROXY inside fn = %q, want the temporary proxy", inside)
			}
			if got := os.Getenv("HTTP_PROXY"); got != "http://preexisting:1" {
				t.Errorf("HTTP_PROXY after = %q, want the original", got)
			}
			if got := os.Getenv("NO_PROXY"); got != "mine.internal" {
				t.Errorf("NO_PROXY after = %q, want mine.internal", got)
			}
			if v, ok := os.LookupEnv("http_proxy"); caseSensitiveEnv && (!ok || v != "") {
				t.Errorf("http_proxy after = %q (set=%v), want set-and-empty", v, ok)
			}
			unset := []string{"HTTPS_PROXY", "ALL_PROXY", "AUTOPROXY"}
			if caseSensitiveEnv {
				unset = append(unset, "https_proxy", "all_proxy", "no_proxy")
			}
			for _, k := range unset {
				if v, ok := os.LookupEnv(k); ok {
					t.Errorf("%s = %q after WithProxy, want unset", k, v)
				}
			}
		})
	}
}

func TestWithProxyMulti_RestoresEnvironmentExactly(t *testing.T) {
	newTrackingBackend(t)
	for _, k := range proxyEnvNames {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	t.Setenv("HTTPS_PROXY", "http://keep:3")

	err := WithProxyMulti(context.Background(), ProxyConfig{HTTPS: "http://tmp:4", NoProxy: "tmp.internal"}, ScopeShell,
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("HTTPS_PROXY"); got != "http://keep:3" {
		t.Errorf("HTTPS_PROXY = %q, want http://keep:3", got)
	}
	if v, ok := os.LookupEnv("NO_PROXY"); ok {
		t.Errorf("NO_PROXY = %q, want unset", v)
	}
}

func TestSplitNoProxy(t *testing.T) {
	got := splitNoProxy(" localhost, 10.0.0.0/8 ;*.corp.internal,, ")
	want := []string{"localhost", "10.0.0.0/8", "*.corp.internal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitNoProxy = %q, want %q", got, want)
	}
	if got := splitNoProxy(""); len(got) != 0 {
		t.Fatalf("splitNoProxy(\"\") = %q", got)
	}
}

func TestExecError(t *testing.T) {
	if execError("t", "x", nil, nil) != nil {
		t.Error("nil error must stay nil")
	}
	err := execError("networksetup", `-setwebproxy "Wi-Fi"`, errors.New("exit status 4"), []byte("\n** Error: The parameters were not valid.\nmore\n"))
	for _, want := range []string{"networksetup", "-setwebproxy", "exit status 4", "The parameters were not valid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "more") {
		t.Errorf("only the first output line belongs in the error: %q", err)
	}
}

func TestFlattenDropsClassification(t *testing.T) {
	nc := asElevationError(errors.New("root required"))
	joined := errors.Join(errors.New("hard"), flatten(nc))
	if IsNonCritical(joined) || RequiresElevation(joined) {
		t.Fatal("a hard failure must not look like a warning")
	}
	if !strings.Contains(joined.Error(), "root required") {
		t.Fatalf("message lost: %v", joined)
	}
}

// A restore step that did not happen is a failure, whatever its classification
// (a failed /etc/environment write is reported as non-critical by the Linux
// backend); and it must never make fn's error look like a warning.
func TestWithProxy_NonCriticalRestoreErrorIsStillAFailureAndNeverTaints(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.restoreErr = &nonCriticalError{err: errors.New("write /etc/environment: input/output error")}

	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return nil })
	if err == nil || IsNonCritical(err) {
		t.Errorf("err = %v (non-critical=%v); want a hard restore failure", err, IsNonCritical(err))
	}

	boom := errors.New("boom")
	err = WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return boom })
	if !errors.Is(err, boom) || IsNonCritical(err) {
		t.Errorf("err = %v (non-critical=%v); want fn's error and no warning classification", err, IsNonCritical(err))
	}
}

func TestWithProxy_HardRestoreFailureIsStillReported(t *testing.T) {
	tb := newTrackingBackend(t)
	tb.restoreErr = errors.New("registry refused the write")
	err := WithProxy(context.Background(), "http://tmp:9999", ScopeGlobal, func(context.Context) error { return nil })
	if err == nil || IsNonCritical(err) {
		t.Errorf("err = %v, want a hard restore failure", err)
	}
}
