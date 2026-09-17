package mcpmgr

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"changeme/internal/config"
)

// URLOpener opens the authorization URL in the user's default browser.
// Injectable for tests.
var URLOpener = defaultURLOpener

func defaultURLOpener(raw string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", raw).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}

// authHTTPClient builds an *http.Client applying static config headers plus,
// for the simple credential kinds, the auth header (Bearer token or named
// API key). Used for streamable HTTP transports without full OAuth.
func authHTTPClient(cfg config.MCPServerConfig) *http.Client {
	headerName := cfg.AuthHeader
	if headerName == "" {
		headerName = "X-API-Key"
	}
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}
		switch cfg.AuthType {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		case "api-key":
			req.Header.Set(headerName, cfg.AuthToken)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// headersOnlyClient applies just the static config headers (no auth kind).
func headersOnlyClient(cfg config.MCPServerConfig) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// newOAuthTransport wires the official go-sdk authorization-code handler
// (OAuth 2.1 + PKCE per the MCP authorization spec) into a streamable HTTP
// transport. The loopback redirect listener is created here so the redirect
// URL is stable for the handler; it serves only while the consent flow runs.
func newOAuthTransport(cfg config.MCPServerConfig) (mcp.Transport, error) {
	if cfg.OAuthClientID == "" {
		return nil, fmt.Errorf("oauth2 认证需要预注册的 client id")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.OAuthRedirectPort))
	if err != nil {
		return nil, fmt.Errorf("本地回调端口监听失败: %w", err)
	}
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	fetcher := newBrowserAuthCodeFetcher(ln)
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		PreregisteredClient: &oauthex.ClientCredentials{ClientID: cfg.OAuthClientID},
		RedirectURL:         redirectURL,
		AuthorizationCodeFetcher: fetcher,
	})
	if err != nil {
		ln.Close()
		return nil, err
	}
	return &mcp.StreamableClientTransport{Endpoint: cfg.URL, OAuthHandler: handler}, nil
}

// newBrowserAuthCodeFetcher serves the loopback callback on the pre-bound
// listener, opens the system browser at the authorization URL and resolves
// with the code the authorization server redirects back with (5 min cap).
func newBrowserAuthCodeFetcher(ln net.Listener) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		codeCh := make(chan *auth.AuthorizationResult, 1)
		errCh := make(chan error, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if e := q.Get("error"); e != "" {
				fmt.Fprintf(w, "<html><body><h3>授权失败</h3><p>%s: %s</p></body></html>", e, q.Get("error_description"))
				errCh <- fmt.Errorf("授权服务器返回 %s: %s", e, q.Get("error_description"))
				return
			}
			fmt.Fprint(w, "<html><body><h3>已收到授权，请返回 WorkBuddy Agent。</h3></body></html>")
			codeCh <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}
		})
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		serveDone := make(chan struct{})
		go func() {
			_ = srv.Serve(ln)
			close(serveDone)
		}()
		defer func() {
			_ = srv.Close()
			<-serveDone
		}()

		if err := URLOpener(args.URL); err != nil {
			return nil, fmt.Errorf("无法打开浏览器完成授权: %w", err)
		}
		select {
		case res := <-codeCh:
			return res, nil
		case err := <-errCh:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Minute):
			return nil, fmt.Errorf("授权等待超时（5 分钟）")
		}
	}
}

// roundTripperFunc adapts a function to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
