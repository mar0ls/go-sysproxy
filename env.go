package sysproxy

import "os"

func setEnvVars(proxyURL string) {
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		_ = os.Setenv(k, proxyURL)
	}
	_ = os.Setenv("no_proxy", "localhost,127.0.0.1,::1")
	_ = os.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
}

func unsetEnvVars() {
	for _, k := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY",
	} {
		_ = os.Unsetenv(k)
	}
}

func setEnvVarsPAC(pacURL string) {
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		_ = os.Setenv(k, "")
	}
	_ = os.Setenv("no_proxy", "localhost,127.0.0.1,::1")
	_ = os.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
	_ = os.Setenv("AUTOPROXY", pacURL)
}

func setEnvVarsMulti(cfg ProxyConfig) {
	if cfg.HTTP != "" {
		_ = os.Setenv("http_proxy", cfg.HTTP)
		_ = os.Setenv("HTTP_PROXY", cfg.HTTP)
	}
	if cfg.HTTPS != "" {
		_ = os.Setenv("https_proxy", cfg.HTTPS)
		_ = os.Setenv("HTTPS_PROXY", cfg.HTTPS)
	}
	if cfg.SOCKS != "" {
		_ = os.Setenv("all_proxy", cfg.SOCKS)
		_ = os.Setenv("ALL_PROXY", cfg.SOCKS)
	}
	if cfg.NoProxy != "" {
		_ = os.Setenv("no_proxy", cfg.NoProxy)
		_ = os.Setenv("NO_PROXY", cfg.NoProxy)
	}
}
