package sysproxy

import (
	"context"
	"os"
	"testing"
)

func TestSetContextCanceledDoesNotMutateEnv(t *testing.T) {
	t.Cleanup(unsetEnvVars)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SetContext(ctx, "http://proxy.example.com:8080", ScopeShell)
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if got := os.Getenv("http_proxy"); got != "" {
		t.Fatalf("http_proxy should stay unset, got %q", got)
	}
}

func TestUnsetContextCanceledDoesNotMutateEnv(t *testing.T) {
	t.Cleanup(unsetEnvVars)
	setEnvVars("http://proxy.example.com:8080")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := UnsetContext(ctx, ScopeShell)
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if got := os.Getenv("http_proxy"); got == "" {
		t.Fatal("http_proxy should remain set after canceled unset")
	}
}

func TestSetMultiContextCanceledDoesNotMutateEnv(t *testing.T) {
	t.Cleanup(unsetEnvVars)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SetMultiContext(ctx, ProxyConfig{HTTP: "http://proxy.example.com:8080"}, ScopeShell)
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if got := os.Getenv("http_proxy"); got != "" {
		t.Fatalf("http_proxy should stay unset, got %q", got)
	}
}

func TestSetPACContextCanceledDoesNotMutateEnv(t *testing.T) {
	t.Cleanup(func() {
		_ = os.Unsetenv("AUTOPROXY")
		unsetEnvVars()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SetPACContext(ctx, "https://config.example.com/proxy.pac", ScopeShell)
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if got := os.Getenv("AUTOPROXY"); got != "" {
		t.Fatalf("AUTOPROXY should stay unset, got %q", got)
	}
}

func TestGetContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetContext(ctx)
	if err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestSetWrapperStillWorks(t *testing.T) {
	t.Cleanup(unsetEnvVars)

	if err := Set("http://proxy.example.com:8080", ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("http_proxy"); got != "http://proxy.example.com:8080" {
		t.Fatalf("http_proxy = %q", got)
	}
}

func TestUnsetWrapperStillWorks(t *testing.T) {
	t.Cleanup(unsetEnvVars)
	setEnvVars("http://proxy.example.com:8080")

	if err := Unset(ScopeShell); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("http_proxy"); got != "" {
		t.Fatalf("http_proxy should be unset, got %q", got)
	}
}
