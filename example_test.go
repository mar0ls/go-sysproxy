package sysproxy_test

import (
	"context"
	"fmt"
	"log"
	"time"

	sysproxy "github.com/mar0ls/go-sysproxy"
)

func ExampleSet() {
	if err := sysproxy.Set("http://user:pass@proxy.example.com:8080", sysproxy.ScopeGlobal); err != nil {
		log.Fatal(err)
	}
	fmt.Println("proxy set")
}

func ExampleSet_noAuth() {
	if err := sysproxy.Set("http://proxy.example.com:8080", sysproxy.ScopeGlobal); err != nil {
		log.Fatal(err)
	}
	fmt.Println("proxy set")
}

func ExampleSetContext() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := sysproxy.SetContext(ctx, "http://proxy.example.com:8080", sysproxy.ScopeGlobal)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("proxy set")
}

func ExampleUnset() {
	if err := sysproxy.Unset(sysproxy.ScopeGlobal); err != nil {
		log.Fatal(err)
	}
	fmt.Println("proxy cleared")
}

func ExampleSetMulti() {
	if err := sysproxy.SetMulti(sysproxy.ProxyConfig{
		HTTP:    "http://http-proxy.example.com:8080",
		HTTPS:   "http://https-proxy.example.com:8080",
		SOCKS:   "socks5://socks-proxy.example.com:1080",
		NoProxy: "localhost,10.0.0.0/8",
	}, sysproxy.ScopeGlobal); err != nil {
		log.Fatal(err)
	}
	fmt.Println("per-protocol proxies set")
}

func ExampleWithProxy() {
	ctx := context.Background()

	err := sysproxy.WithProxy(ctx, "socks5://proxy.example.com:1080", sysproxy.ScopeGlobal,
		func(ctx context.Context) error {
			fmt.Println("running with proxy")
			return nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleCheck() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sysproxy.Check(ctx, "http://proxy.example.com:8080"); err != nil {
		log.Printf("proxy unreachable: %v", err)
		return
	}
	fmt.Println("proxy reachable")
}

func ExampleWriteAppConfig() {
	if err := sysproxy.WriteAppConfig(sysproxy.AppGit, "http://proxy.example.com:8080"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("git proxy configured")
}

func ExampleWriteAppConfigContext() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := sysproxy.WriteAppConfigContext(ctx, sysproxy.AppGit, "http://proxy.example.com:8080")
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("git proxy configured")
}

func ExampleSetLogger() {
	// Disable logging (restores the default no-op state).
	sysproxy.SetLogger(nil)
	fmt.Println("logging disabled")
	// Output: logging disabled
}
