package agentkit

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
