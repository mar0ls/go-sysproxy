package sysproxy

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCheckReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ln.Close()
	}()

	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := Check(ctx, "http://"+ln.Addr().String()); err != nil {
		t.Fatalf("Check returned error for reachable proxy: %v", err)
	}

	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("listener accept failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test listener to accept connection")
	}
}

func TestCheckInvalidURL(t *testing.T) {
	err := Check(context.Background(), "://bad url")
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
	if !strings.Contains(err.Error(), "invalid proxy URL") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestCheckDeadlineExceededUsesDefaultPort(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := Check(ctx, "http://127.0.0.1")
	if err == nil {
		t.Fatal("expected deadline exceeded error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:80") {
		t.Fatalf("expected default port to be used in error, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped context deadline exceeded, got %v", err)
	}
}

func TestDefaultProxyPort(t *testing.T) {
	tests := []struct {
		scheme string
		want   string
	}{
		{scheme: "http", want: "80"},
		{scheme: "https", want: "443"},
		{scheme: "socks5", want: "1080"},
		{scheme: "socks4", want: "1080"},
		{scheme: "socks", want: "1080"},
		{scheme: "ftp", want: "80"},
	}

	for _, tt := range tests {
		if got := defaultProxyPort(tt.scheme); got != tt.want {
			t.Fatalf("defaultProxyPort(%q) = %q, want %q", tt.scheme, got, tt.want)
		}
	}
}
