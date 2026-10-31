package agentkit

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ApplyGlobalProxy routes all clients that use http.DefaultTransport (model
// adapters, embeddings, MCP, gemini SDK) through the given proxy URL.
// Call once at startup BEFORE any request; pass "" to reset to direct.
func ApplyGlobalProxy(proxyURL string) error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("默认传输层类型异常（%T），无法配置代理", http.DefaultTransport)
	}
	if proxyURL == "" {
		// Fall back to the process environment (HTTP_PROXY / HTTPS_PROXY /
		// NO_PROXY) instead of disabling it: blanking the func used to
		// silently ignore proxies the user configured outside the app.
		transport.Proxy = http.ProxyFromEnvironment
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("代理地址解析失败: %w", err)
	}
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if isLoopbackHost(req.URL.Hostname()) {
			return nil, nil // local Ollama / local MCP / A2A round-trips stay direct
		}
		return u, nil
	}
	return nil
}

// isLoopbackHost reports whether host (no port, no brackets) is the local
// machine.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
