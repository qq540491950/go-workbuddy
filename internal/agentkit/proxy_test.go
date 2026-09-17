package agentkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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
	if err := ApplyGlobalProxy(""); err != nil {
		t.Fatal(err)
	}
	if transport.Proxy != nil {
		t.Fatal("proxy not reset")
	}
	_ = url.Parse
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
	srv := httptest.NewServer(mux)
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
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

	tr := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"} // default transport path
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
