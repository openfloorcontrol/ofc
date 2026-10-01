package furniture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/auth/extauth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// ErrNotAuthorized means an MCP server needs OAuth consent that nobody can
// give here; `ofc auth <furniture>` gives it.
var ErrNotAuthorized = errors.New("not authorized")

// OAuth describes how one MCP furniture authorizes with OAuth.
type OAuth struct {
	Name string // furniture name, for messages

	// ClientCredentials selects the client credentials grant (no person
	// involved); otherwise the authorization code grant is used.
	ClientCredentials bool
	ClientID          string // pre-registered client; empty: dynamic registration
	ClientSecret      string
	Scopes            []string // empty: what the server advertises

	// TokenFile stores the authorization code grant's client and token.
	// It is the source of truth: every request reads it, refreshes go
	// through it, so a token stored by another process (e.g. `ofc auth`)
	// is used right away.
	TokenFile string

	// Consent shows a person the authorization URL. Nil means nobody can
	// consent here: an unauthorized server fails with ErrNotAuthorized.
	Consent func(authURL string)
}

// Handler returns the OAuth handler for the MCP transport.
func (o OAuth) Handler() (auth.OAuthHandler, error) {
	if o.ClientCredentials {
		return extauth.NewClientCredentialsHandler(&extauth.ClientCredentialsHandlerConfig{
			Credentials: &oauthex.ClientCredentials{
				ClientID:         o.ClientID,
				ClientSecretAuth: &oauthex.ClientSecretAuth{ClientSecret: o.ClientSecret},
			},
		})
	}

	store := &tokenFile{path: o.TokenFile, name: o.Name}
	var initial oauth2.TokenSource
	if store.exists() {
		initial = store
	}
	if o.Consent == nil {
		return &noConsentHandler{name: o.Name, ts: initial}, nil
	}

	redirect, err := loopbackRedirectURL()
	if err != nil {
		return nil, err
	}
	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL: redirect,
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return fetchCode(ctx, redirect, args.URL, o.Consent)
		},
		RequestRefreshToken: true,
		InitialTokenSource:  initial,
		NewTokenSource: func(ctx context.Context, c *oauth2.Config, t *oauth2.Token) (oauth2.TokenSource, error) {
			if err := store.save(c, t); err != nil {
				return nil, err
			}
			return store, nil
		},
	}
	if len(o.Scopes) > 0 {
		cfg.ScopeFilter = func([]string) []string { return o.Scopes }
	}
	if o.ClientID != "" {
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: o.ClientID}
		if o.ClientSecret != "" {
			cfg.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: o.ClientSecret}
		}
	} else {
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "ofc",
				RedirectURIs:            []string{redirect},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		}
	}
	return auth.NewAuthorizationCodeHandler(cfg)
}

// noConsentHandler uses the stored token and fails, instead of starting a
// consent flow, when the server asks for authorization.
type noConsentHandler struct {
	name string
	ts   oauth2.TokenSource // nil if no token was stored
}

func (h *noConsentHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return h.ts, nil
}

func (h *noConsentHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return notAuthorized(h.name)
}

func notAuthorized(name string) error {
	return fmt.Errorf("%w: run `ofc auth %s` to authorize", ErrNotAuthorized, name)
}

// loopbackRedirectURL picks a free loopback port for the consent callback.
func loopbackRedirectURL() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("pick a port for the OAuth callback: %w", err)
	}
	addr := l.Addr().String()
	l.Close()
	return "http://" + addr + "/callback", nil
}

// fetchCode shows authURL via consent and waits for the authorization
// server to redirect the browser to redirect with the code.
func fetchCode(ctx context.Context, redirect, authURL string, consent func(string)) (*auth.AuthorizationResult, error) {
	u, err := url.Parse(redirect)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("listen for the OAuth callback on %s: %w", u.Host, err)
	}
	results := make(chan *auth.AuthorizationResult, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != u.Path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization failed: "+e, http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "ofc is authorized. You can close this tab.")
		select {
		case results <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}:
		default:
		}
	})}
	go srv.Serve(l)
	defer srv.Close()

	consent(authURL)
	select {
	case res := <-results:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Minute):
		return nil, fmt.Errorf("no OAuth consent within 10 minutes")
	}
}

// storedSession is what tokenFile keeps: enough to refresh the token.
type storedSession struct {
	ClientID     string           `json:"client_id"`
	ClientSecret string           `json:"client_secret,omitempty"`
	AuthURL      string           `json:"auth_url"`
	TokenURL     string           `json:"token_url"`
	AuthStyle    oauth2.AuthStyle `json:"auth_style"`
	Scopes       []string         `json:"scopes,omitempty"`
	Token        *oauth2.Token    `json:"token"`
}

func (s *storedSession) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.ClientID,
		ClientSecret: s.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: s.AuthURL, TokenURL: s.TokenURL, AuthStyle: s.AuthStyle},
		Scopes:       s.Scopes,
	}
}

// tokenFile is an oauth2.TokenSource backed by a file. Token reads the
// file under a lock and, if the token has expired, refreshes and writes it
// back, so processes sharing the file never use a rotated-out refresh
// token.
type tokenFile struct {
	path string
	name string
}

func (f *tokenFile) exists() bool {
	_, err := os.Stat(f.path)
	return err == nil
}

func (f *tokenFile) Token() (*oauth2.Token, error) {
	unlock, err := lockFile(f.path)
	if err != nil {
		return nil, err
	}
	defer unlock()

	s, err := f.read()
	if err != nil {
		return nil, err
	}
	if s.Token.Valid() {
		return s.Token, nil
	}
	tok, err := s.config().TokenSource(context.Background(), s.Token).Token()
	if err != nil {
		return nil, fmt.Errorf("%s: refreshing the OAuth token failed: %w (run `ofc auth %s`)", f.name, err, f.name)
	}
	s.Token = tok
	return tok, f.write(s)
}

func (f *tokenFile) save(c *oauth2.Config, t *oauth2.Token) error {
	unlock, err := lockFile(f.path)
	if err != nil {
		return err
	}
	defer unlock()
	return f.write(&storedSession{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		AuthURL:      c.Endpoint.AuthURL,
		TokenURL:     c.Endpoint.TokenURL,
		AuthStyle:    c.Endpoint.AuthStyle,
		Scopes:       c.Scopes,
		Token:        t,
	})
}

func (f *tokenFile) read() (*storedSession, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, notAuthorized(f.name)
	}
	if err != nil {
		return nil, err
	}
	var s storedSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("read %s: %w", f.path, err)
	}
	if s.Token == nil {
		return nil, notAuthorized(f.name)
	}
	return &s, nil
}

// write replaces the file atomically; it is readable only by its owner.
func (f *tokenFile) write(s *storedSession) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
