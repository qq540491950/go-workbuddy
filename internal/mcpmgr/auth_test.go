package mcpmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"changeme/internal/config"
)

const (
	testAccessToken = "test-access-token-123"
	testAPIKey      = "test-api-key-456"
)

// newProtectedMCPServer hosts a real go-sdk MCP server behind an auth
// middleware. kind: "oauth2" (Bearer issued by the fake AS) | "api-key" | "bearer" (static).
func newProtectedMCPServer(t *testing.T, kind, asBase string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "answers pong",
		InputSchema: &jsonschema.Schema{Type: "object"},
	},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	check := func(r *http.Request) bool {
		switch kind {
		case "oauth2", "bearer":
			return r.Header.Get("Authorization") == "Bearer "+testAccessToken
		case "api-key":
			return r.Header.Get("X-API-Key") == testAPIKey
		}
		return true
	}

	// Mux registration happens after the server starts (same trick as the
	// official fixtures): the PRM document needs the final server URL.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resourceURL := srv.URL + "/mcp"
	mux.Handle("/.well-known/oauth-protected-resource/mcp",
		auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:             resourceURL,
			AuthorizationServers: []string{asBase},
			ScopesSupported:      []string{"mcp:tools"},
		}))
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !check(r) {
			w.Header().Set("WWW-Authenticate", "Bearer resource_metadata="+resourceURL+"/.well-known/oauth-protected-resource/mcp")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	return srv
}

// newFakeAuthorizationServer implements the MCP authorization-server surface:
// protected resource metadata, AS metadata, /authorize (PKCE echo) and /token.
func newFakeAuthorizationServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("response_type") != "code" || q.Get("client_id") == "" ||
			q.Get("code_challenge") == "" || q.Get("redirect_uri") == "" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		loc, _ := url.Parse(q.Get("redirect_uri"))
		rq := loc.Query()
		rq.Set("code", "auth-code-xyz")
		rq.Set("state", q.Get("state"))
		loc.RawQuery = rq.Encode()
		http.Redirect(w, r, loc.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "auth-code-xyz" {
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "PKCE verifier missing", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": testAccessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// browserSimulator "opens" the authorization URL like a user agent: follows
// the authorize redirect once and lands on the loopback callback.
func browserSimulator() func(string) error {
	return func(raw string) error {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}, Timeout: 10 * time.Second}
		resp, err := client.Get(raw)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		loc := resp.Header.Get("Location")
		if loc == "" {
			return fmt.Errorf("authorize did not redirect: status=%d url=%s body=%.200s", resp.StatusCode, raw, body)
		}
		_, err = client.Get(loc) // hits the app's loopback callback
		return err
	}
}

func listToolsThrough(t *testing.T, tr mcp.Transport) []string {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "workbuddy-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	resp, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := []string{}
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func TestMCPAuthBearer(t *testing.T) {
	srv := newProtectedMCPServer(t, "bearer", "")
	cfg := config.MCPServerConfig{
		ID: "m1", Name: " secured", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", AuthType: "bearer", AuthToken: testAccessToken,
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools: %v", names)
	}
}

func TestMCPAuthAPIKey(t *testing.T) {
	srv := newProtectedMCPServer(t, "api-key", "")
	cfg := config.MCPServerConfig{
		ID: "m2", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", AuthType: "api-key", AuthToken: testAPIKey, AuthHeader: "X-API-Key",
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools: %v", names)
	}
}

func TestMCPAuthOAuth2Flow(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	resource := newProtectedMCPServer(t, "oauth2", as.URL)
	defer func() { URLOpener = defaultURLOpener }()
	URLOpener = browserSimulator()

	cfg := config.MCPServerConfig{
		ID: "m3", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: resource.URL + "/mcp", AuthType: "oauth2",
		OAuthClientID: "workbuddy-desktop",
	}
	var pendingURL atomic.Value
	tr, err := buildTransport(cfg, nil, func(authURL string) {
		pendingURL.Store(authURL)
	})
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools after OAuth flow: %v", names)
	}
	if v := pendingURL.Load(); v == nil || !strings.Contains(v.(string), "/authorize") {
		t.Fatalf("auth-pending hook not fired with authorize URL: %v", v)
	}
	if !strings.Contains(names[0], "ping") {
		t.Fatalf("tool sanity: %v", names)
	}
}

func TestMCPAuthUnauthenticatedRejected(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	srv := newProtectedMCPServer(t, "oauth2", as.URL)
	cfg := config.MCPServerConfig{
		ID: "m4", Name: "noauth", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", // no AuthType
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "workbuddy-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, tr, nil)
	if err == nil {
		defer session.Close()
		_, err = session.ListTools(ctx, nil)
	}
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected unauthorized failure without credentials, got %v", err)
	}
}

// TestMCPAuthTokenPersistence runs the OAuth flow once with a config-backed
// token store, then simulates an app restart: a fresh transport seeded from
// the cache must connect WITHOUT opening the browser.
func TestMCPAuthTokenPersistence(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	resource := newProtectedMCPServer(t, "oauth2", as.URL)
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.MCPServerConfig{
		ID: "m5", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: resource.URL + "/mcp", AuthType: "oauth2",
		OAuthClientID: "workbuddy-desktop",
	}

	// First run: full consent flow; the browser simulator is required.
	defer func() { URLOpener = defaultURLOpener }()
	URLOpener = browserSimulator()
	tr1, err := buildTransport(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := listToolsThrough(t, tr1); len(names) != 1 {
		t.Fatalf("first flow tools: %v", names)
	}
	snap := store.Get()
	cached, ok := snap.MCPTokens[cfg.ID]
	if !ok || cached.AccessToken != testAccessToken || cached.TokenURL == "" {
		t.Fatalf("token not persisted: %+v (store has %+v)", cached, snap.MCPTokens)
	}

	// "Restart": fresh transport from the same store. The browser must NOT
	// be opened (opening fails the test).
	URLOpener = func(string) error { return fmt.Errorf("browser must not open for cached token") }
	tr2, err := buildTransport(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := listToolsThrough(t, tr2); len(names) != 1 || names[0] != "ping" {
		t.Fatalf("restart flow tools: %v", names)
	}
}
