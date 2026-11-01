package agentkit

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestApplyGlobalProxy: with a proxy configured, requests via
// http.DefaultTransport arrive at the proxy server (absolute-form URI).
func TestApplyGlobalProxy(t *testing.T) {
	t.Cleanup(func() { _ = ApplyGlobalProxy("") })
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("default transport is not *http.Transport")
	}
	origProxy := transport.Proxy
	t.Cleanup(func() { transport.Proxy = origProxy })

	var hitProxy bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitProxy = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxySrv.Close()

	if err := ApplyGlobalProxy(proxySrv.URL); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://destination.example/anything")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	resp.Body.Close()
	if !hitProxy {
		t.Fatal("request did not reach the proxy")
	}

	// Reset returns to direct.
	// "Direct" now means ProxyFromEnvironment: an empty in-app proxy must
	// not disable the user's HTTP_PROXY/HTTPS_PROXY environment.
	if err := ApplyGlobalProxy(""); err != nil {
		t.Fatal(err)
	}
	if transport.Proxy == nil {
		t.Fatal("proxy not reset to environment fallback")
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("reset must delegate to ProxyFromEnvironment")
	}
}

// TestApplyGlobalProxyEmptyFallsBackToEnvironment: without an in-app proxy
// the transport must honor HTTP_PROXY/HTTPS_PROXY — blanking the proxy func
// used to silently ignore environment proxies. The fallback delegates to
// ProxyFromEnvironment, whose env handling (including NO_PROXY) is stdlib
// behavior; here we pin that the delegation actually happens.
func TestApplyGlobalProxyEmptyFallsBackToEnvironment(t *testing.T) {
	t.Cleanup(func() { _ = ApplyGlobalProxy("") })
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("default transport is not *http.Transport")
	}
	origProxy := transport.Proxy
	t.Cleanup(func() { transport.Proxy = origProxy })

	if err := ApplyGlobalProxy(""); err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("empty in-app proxy must fall back to ProxyFromEnvironment")
	}
}

// TestApplyGlobalProxySkipsLoopback pins the loopback carve-out: local
// Ollama, local MCP servers and A2A round-trips must never be pushed through
// the configured proxy.
func TestApplyGlobalProxySkipsLoopback(t *testing.T) {
	t.Cleanup(func() { _ = ApplyGlobalProxy("") })
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("default transport is not *http.Transport")
	}
	origProxy := transport.Proxy
	t.Cleanup(func() { transport.Proxy = origProxy })

	if err := ApplyGlobalProxy("http://proxy.example:7890"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"http://127.0.0.1:11434/v1",
		"http://localhost:9246/stub",
		"http://[::1]:8080/a2a",
	} {
		req := httptest.NewRequest(http.MethodGet, raw, nil)
		u, err := transport.Proxy(req)
		if err != nil || u != nil {
			t.Errorf("%s: loopback must stay direct, got %v err %v", raw, u, err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://api.deepseek.com/v1", nil)
	u, err := transport.Proxy(req)
	if err != nil || u == nil || u.Host != "proxy.example:7890" {
		t.Errorf("public host must use the proxy, got %v err %v", u, err)
	}
}

func TestApplyGlobalProxyInvalid(t *testing.T) {
	// A syntactically broken proxy must not corrupt the transport.
	if err := ApplyGlobalProxy("://bad"); err == nil {
		t.Fatal("expected parse error")
	}
}

// TestGlobalProxyCoversMCPTransport pins the proxy coverage contract: a
// streamable-http MCP transport with no explicit HTTPClient must route
// through http.DefaultTransport, so the global proxy setting applies.
func TestGlobalProxyCoversMCPTransport(t *testing.T) {
	var hitProxy atomic.Bool
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "answers pong",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	// Bind the origin to 127.0.0.2: loopback traffic is exempt from the
	// global proxy by design, and the exemption list is 127.0.0.1/localhost/
	// ::1 — so this address keeps the MCP-over-proxy contract observable
	// while everything stays on the local machine. Platforms that cannot
	// route the rest of 127/8 (older macOS) skip instead of failing.
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("platform does not route 127.0.0.2: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	base := "http://" + ln.Addr().String()
	target := mustParseURL(base)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if hitProxy.Load() {
			w.Header().Set("X-Proxied", "1")
		}
		mcpHandler.ServeHTTP(w, r)
	})

	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitProxy.Store(true)
		// Forward with a DIRECT transport: the real destination does not
		// loop back through the global proxy it is advertised by.
		rp := httputil.NewSingleHostReverseProxy(target)
		direct := http.DefaultTransport.(*http.Transport).Clone()
		direct.Proxy = nil
		rp.Transport = direct
		rp.ServeHTTP(w, r)
	}))
	defer proxySrv.Close()

	t.Cleanup(func() { _ = ApplyGlobalProxy("") })
	if err := ApplyGlobalProxy(proxySrv.URL); err != nil {
		t.Fatal(err)
	}

	tr := &mcp.StreamableClientTransport{Endpoint: base + "/mcp"} // default transport path
	client := mcp.NewClient(&mcp.Implementation{Name: "proxy-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	resp, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tools) != 1 || !hitProxy.Load() {
		t.Fatalf("proxy not traversed: tools=%v hit=%v", resp.Tools, hitProxy.Load())
	}
}

// TestGlobalProxyAppliesToEmptyClients documents/verifies that SDK-style
// fallback clients (empty &http.Client{} as used by the gemini SDK and the
// a2a-go JSONRPC client) ride http.DefaultTransport, so the global proxy
// applies to them too.
func TestGlobalProxyAppliesToEmptyClients(t *testing.T) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("default transport is not *http.Transport")
	}
	origProxy := transport.Proxy
	t.Cleanup(func() { transport.Proxy = origProxy })

	var hitProxy atomic.Bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitProxy.Store(true)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxySrv.Close()

	if err := ApplyGlobalProxy(proxySrv.URL); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{} // same shape as the gemini/a2a fallbacks
	// The request TARGET must be a non-loopback host for the proxy func to
	// engage (loopback targets are exempt by design); the proxy itself still
	// listens on the local httptest addr.
	resp, err := client.Get("http://destination.example/v1beta/models")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !hitProxy.Load() {
		t.Fatal("empty-client requests must traverse the global proxy")
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
