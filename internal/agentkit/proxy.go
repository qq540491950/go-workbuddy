package agentkit

import (
	"fmt"
	"net/http"
	"net/url"
)

// ApplyGlobalProxy routes all clients that use http.DefaultTransport (model
// adapters, embeddings, MCP, gemini SDK) through the given proxy URL.
// Call once at startup BEFORE any request; pass "" to reset to direct.
func ApplyGlobalProxy(proxyURL string) error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("default transport is %T, cannot configure proxy", http.DefaultTransport)
	}
	if proxyURL == "" {
		transport.Proxy = nil
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("代理地址解析失败: %w", err)
	}
	transport.Proxy = func(*http.Request) (*url.URL, error) { return u, nil }
	return nil
}
