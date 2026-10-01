package furniture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// oauthServer is an MCP server behind OAuth, which is also its own
// authorization server (metadata, dynamic registration, token endpoint).
type oauthServer struct {
	*httptest.Server
	mu        sync.Mutex
	valid     map[string]bool // access tokens the MCP endpoint accepts
	refreshes int
}

func newOAuthServer(t *testing.T) *oauthServer {
	t.Helper()
	s := &oauthServer{valid: map[string]bool{}}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "kb", Version: "1"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "ping"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)

	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"resource":              s.URL + "/mcp",
			"authorization_servers": []string{s.URL},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"issuer":                           s.URL,
			"authorization_endpoint":           s.URL + "/authorize",
			"token_endpoint":                   s.URL + "/token",
			"registration_endpoint":            s.URL + "/register",
			"response_types_supported":         []string{"code"},
			"code_challenge_methods_supported": []string{"S256"},
			"scopes_supported":                 []string{"kb.read", "offline_access"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var meta map[string]any
		json.NewDecoder(r.Body).Decode(&meta)
		meta["client_id"] = "dcr-client"
		writeJSON(w, 201, meta)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		s.mu.Lock()
		defer s.mu.Unlock()
		var access, refresh string
		switch {
		case r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code") == "code-1":
			access, refresh = "tok-1", "ref-1"
		case r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "ref-1":
			access, refresh = "tok-2", "ref-2"
			s.refreshes++
		default:
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		s.valid[access] = true
		writeJSON(w, 200, map[string]any{
			"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.valid[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		s.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, s.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// browser plays the person consenting: it goes straight to the redirect
// URL with the authorization code.
func browser(t *testing.T) func(string) {
	return func(authURL string) {
		u, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("bad auth URL %q: %v", authURL, err)
			return
		}
		q := u.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			t.Errorf("redirect: %v", err)
			return
		}
		resp.Body.Close()
	}
}

func connectOAuth(t *testing.T, srv *oauthServer, o OAuth) (*ExternalMCP, error) {
	t.Helper()
	h, err := o.Handler()
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return NewExternalMCPFromURL(ctx, o.Name, srv.URL+"/mcp", nil, h)
}

func readSession(t *testing.T, path string) storedSession {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	var s storedSession
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOAuthConsentRegistersAndStoresToken(t *testing.T) {
	srv := newOAuthServer(t)
	file := filepath.Join(t.TempDir(), "kb.json")

	m, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: file, Consent: browser(t)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer m.Close()
	if len(m.Tools()) != 1 {
		t.Errorf("tools = %v", m.Tools())
	}

	s := readSession(t, file)
	if s.ClientID != "dcr-client" || s.Token.RefreshToken != "ref-1" || s.TokenURL != srv.URL+"/token" {
		t.Errorf("stored session = %+v", s)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", info.Mode().Perm())
	}
}

// A failed code exchange is reported after one consent; the transport's
// retry must not ask the person again.
func TestOAuthFailedExchangeAsksOnce(t *testing.T) {
	srv := newOAuthServer(t)
	consents := 0
	badBrowser := func(authURL string) {
		consents++
		u, _ := url.Parse(authURL)
		q := u.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?code=WRONG&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			resp.Body.Close()
		}
	}
	_, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: filepath.Join(t.TempDir(), "kb.json"), Consent: badBrowser})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("err = %v, want the token endpoint's error", err)
	}
	if consents != 1 {
		t.Errorf("consents = %d, want 1", consents)
	}
}

// A callback with the wrong state is refused, and the right one still
// completes the consent.
func TestOAuthCallbackWithWrongStateIsIgnored(t *testing.T) {
	srv := newOAuthServer(t)
	var refused int
	browser := func(authURL string) {
		u, _ := url.Parse(authURL)
		q := u.Query()
		cb := q.Get("redirect_uri")
		if resp, err := http.Get(cb + "?code=code-1"); err == nil { // state cut off
			if resp.StatusCode == http.StatusBadRequest {
				refused++
			}
			resp.Body.Close()
		}
		resp, err := http.Get(cb + "?code=code-1&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			resp.Body.Close()
		}
	}
	m, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: filepath.Join(t.TempDir(), "kb.json"), Consent: browser})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	m.Close()
	if refused != 1 {
		t.Errorf("callback without state: refused %d times, want 1", refused)
	}
}

func TestCallbackListenAddr(t *testing.T) {
	cases := []struct {
		url  string
		port int
		want string // "" for an error
	}{
		{"http://127.0.0.1:8765/callback", 0, "127.0.0.1:8765"},
		{"http://localhost:8765/callback", 0, "localhost:8765"},
		{"http://pi.tail.ts.net:8765/callback", 0, ":8765"},
		{"http://pi.tail.ts.net/callback", 0, ""},                         // no port
		{"https://pi.tail.ts.net:8765/callback", 0, ""},                   // ofc serves plain http
		{"https://ofc-auth.example.com/callback", 8765, "127.0.0.1:8765"}, // behind a proxy
		{"ftp://example.com/callback", 8765, ""},
	}
	for _, c := range cases {
		got, err := callbackListenAddr(c.url, c.port)
		if c.want == "" && err == nil {
			t.Errorf("%s (port %d): want an error, got %q", c.url, c.port, got)
		}
		if c.want != "" && got != c.want {
			t.Errorf("%s (port %d): got %q (%v), want %q", c.url, c.port, got, err, c.want)
		}
	}
}

// With an https callback behind a proxy, ofc registers the https URL and
// listens on plain http on 127.0.0.1:CallbackPort.
func TestOAuthCallbackBehindProxy(t *testing.T) {
	srv := newOAuthServer(t)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	callback := "https://ofc-auth.example.com/callback"

	var gotRedirect string
	viaProxy := func(authURL string) {
		u, _ := url.Parse(authURL)
		q := u.Query()
		gotRedirect = q.Get("redirect_uri")
		// The proxy forwards https://ofc-auth.example.com/callback to ofc.
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=code-1&state=%s", port, url.QueryEscape(q.Get("state"))))
		if err == nil {
			resp.Body.Close()
		}
	}
	m, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: filepath.Join(t.TempDir(), "kb.json"),
		Consent: viaProxy, CallbackURL: callback, CallbackPort: port})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	m.Close()
	if gotRedirect != callback {
		t.Errorf("redirect_uri = %q, want %q", gotRedirect, callback)
	}
}

// With a configured callback URL, ofc registers that URL and listens on
// its port on all interfaces.
func TestOAuthConfiguredCallback(t *testing.T) {
	srv := newOAuthServer(t)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	callback := fmt.Sprintf("http://ofc-test.invalid:%d/callback", port)

	var gotRedirect string
	viaLoopback := func(authURL string) {
		u, _ := url.Parse(authURL)
		q := u.Query()
		gotRedirect = q.Get("redirect_uri")
		local := strings.Replace(gotRedirect, "ofc-test.invalid", "127.0.0.1", 1)
		resp, err := http.Get(local + "?code=code-1&state=" + url.QueryEscape(q.Get("state")))
		if err == nil {
			resp.Body.Close()
		}
	}
	m, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: filepath.Join(t.TempDir(), "kb.json"),
		Consent: viaLoopback, CallbackURL: callback})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	m.Close()
	if gotRedirect != callback {
		t.Errorf("redirect_uri = %q, want %q", gotRedirect, callback)
	}
}

func TestOAuthWithoutConsentFailsWithHint(t *testing.T) {
	srv := newOAuthServer(t)
	_, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: filepath.Join(t.TempDir(), "kb.json")})
	if err == nil || !strings.Contains(err.Error(), "ofc auth kb") {
		t.Fatalf("err = %v, want a hint to run `ofc auth kb`", err)
	}
}

// A process without consent uses the token another process stored, and
// refreshes it when it has expired.
func TestOAuthStoredTokenIsUsedAndRefreshed(t *testing.T) {
	srv := newOAuthServer(t)
	file := filepath.Join(t.TempDir(), "kb.json")
	m, err := connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: file, Consent: browser(t)})
	if err != nil {
		t.Fatalf("consent: %v", err)
	}
	m.Close()

	// Expire the stored token.
	s := readSession(t, file)
	s.Token.Expiry = time.Now().Add(-time.Minute)
	data, _ := json.Marshal(s)
	os.WriteFile(file, data, 0o600)

	m, err = connectOAuth(t, srv, OAuth{Name: "kb", TokenFile: file})
	if err != nil {
		t.Fatalf("connect with stored token: %v", err)
	}
	defer m.Close()
	if _, err := m.Call("ping", map[string]interface{}{}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := readSession(t, file).Token; got.AccessToken != "tok-2" || got.RefreshToken != "ref-2" {
		t.Errorf("after refresh: %+v", got)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", srv.refreshes)
	}
}

func TestOAuthNotAuthorizedIsErrNotAuthorized(t *testing.T) {
	f := &tokenFile{path: filepath.Join(t.TempDir(), "none.json"), name: "kb"}
	if _, err := f.Token(); !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("err = %v, want ErrNotAuthorized", err)
	}
}
