// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// httpClient is the shared client for the discovery/token calls (short timeouts;
// the browser step itself is unbounded and handled separately).
var httpClient = &http.Client{Timeout: 20 * time.Second}

// protectedResource is the RFC 9728 metadata the API serves.
type protectedResource struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// asMetadata is the subset of RFC 8414 authorization-server metadata we use.
type asMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// tokenResponse is the OAuth token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// Opener opens a URL in the user's browser.
type Opener func(url string) error

// Login runs the full browser sign-in against the API at apiBase and returns
// stored-ready credentials. It discovers the authorization server from the API's
// RFC 9728 metadata, registers a public client (DCR) bound to a loopback redirect,
// runs the PKCE authorization-code flow, and exchanges the code for tokens.
func Login(ctx context.Context, apiBase string, open Opener) (*Credentials, error) {
	apiBase = strings.TrimRight(apiBase, "/")

	pr, err := discoverResource(ctx, apiBase)
	if err != nil {
		return nil, err
	}
	if len(pr.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("login: API advertised no authorization server")
	}
	as, err := discoverAS(ctx, pr.AuthorizationServers[0])
	if err != nil {
		return nil, err
	}
	if as.AuthorizationEndpoint == "" || as.TokenEndpoint == "" || as.RegistrationEndpoint == "" {
		return nil, fmt.Errorf("login: authorization server is missing required endpoints")
	}

	// Loopback listener first, so we register the exact redirect URI we'll serve.
	// Bind the IPv4 loopback explicitly; `localhost` resolves to it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("login: open loopback listener: %w", err)
	}
	defer ln.Close()
	// Use the `localhost` hostname, NOT the bare 127.0.0.1 IP literal: the Dropway
	// authorization server (Better Auth's OAuth provider) accepts a `localhost`
	// loopback redirect but rejects a sole 127.0.0.1 redirect with invalid_redirect,
	// which would dead-end the browser before consent. The listener above still binds
	// 127.0.0.1, and localhost resolves to the loopback, so the callback still lands here.
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	clientID, err := registerClient(ctx, as.RegistrationEndpoint, redirectURI)
	if err != nil {
		return nil, err
	}

	verifier := randomString(48)
	challenge := s256(verifier)
	state := randomString(24)

	authURL := as.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"offline_access"},
		"resource":              {pr.Resource},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	// Catch the redirect on the loopback server.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: callbackHandler(state, codeCh, errCh)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if err := open(authURL); err != nil {
		// Non-fatal: the user can copy the URL we print from the caller.
		_ = err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("login: timed out waiting for browser authorization")
	case code := <-codeCh:
		tok, err := exchangeCode(ctx, as.TokenEndpoint, clientID, code, verifier, redirectURI, pr.Resource)
		if err != nil {
			return nil, err
		}
		return credsFromToken(apiBase, as.TokenEndpoint, clientID, pr.Resource, tok), nil
	}
}

// Token resolves a bearer token for calls to apiBase. DROPWAY_TOKEN wins (CI);
// otherwise it uses the stored login, refreshing the access token when expired.
func Token(ctx context.Context, apiBase string) (string, error) {
	if t := strings.TrimSpace(os.Getenv("DROPWAY_TOKEN")); t != "" {
		return t, nil
	}
	apiBase = strings.TrimRight(apiBase, "/")
	c, err := Load()
	if err != nil {
		return "", fmt.Errorf("not signed in: run `dropway login`")
	}
	if strings.TrimRight(c.APIBase, "/") != apiBase {
		return "", fmt.Errorf("signed in to %s, not %s: run `dropway login --api %s`", c.APIBase, apiBase, apiBase)
	}
	if !c.expired() {
		return c.AccessToken, nil
	}
	if c.RefreshToken == "" {
		return "", fmt.Errorf("session expired: run `dropway login`")
	}
	tok, err := refresh(ctx, c.TokenURL, c.ClientID, c.RefreshToken, c.Resource)
	if err != nil {
		return "", fmt.Errorf("session expired: run `dropway login` (%w)", err)
	}
	updated := credsFromToken(c.APIBase, c.TokenURL, c.ClientID, c.Resource, tok)
	if updated.RefreshToken == "" {
		updated.RefreshToken = c.RefreshToken // some servers omit it on refresh
	}
	_ = Save(updated)
	return updated.AccessToken, nil
}

// --- internals --------------------------------------------------------------

func discoverResource(ctx context.Context, apiBase string) (*protectedResource, error) {
	var pr protectedResource
	if err := getJSON(ctx, apiBase+"/.well-known/oauth-protected-resource", &pr); err != nil {
		return nil, fmt.Errorf("login: discover API auth config: %w", err)
	}
	if pr.Resource == "" {
		pr.Resource = apiBase
	}
	return &pr, nil
}

func discoverAS(ctx context.Context, asBase string) (*asMetadata, error) {
	var as asMetadata
	u := strings.TrimRight(asBase, "/") + "/.well-known/oauth-authorization-server"
	if err := getJSON(ctx, u, &as); err != nil {
		return nil, fmt.Errorf("login: discover authorization server: %w", err)
	}
	return &as, nil
}

func registerClient(ctx context.Context, regEndpoint, redirectURI string) (string, error) {
	body := map[string]any{
		"client_name":                "Dropway CLI",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := postJSON(ctx, regEndpoint, body, &out); err != nil {
		return "", fmt.Errorf("login: register client: %w", err)
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("login: registration returned no client_id")
	}
	return out.ClientID, nil
}

func exchangeCode(ctx context.Context, tokenURL, clientID, code, verifier, redirectURI, resource string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
		"resource":      {resource},
	}
	return postForm(ctx, tokenURL, form)
}

func refresh(ctx context.Context, tokenURL, clientID, refreshToken, resource string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"resource":      {resource},
	}
	return postForm(ctx, tokenURL, form)
}

func credsFromToken(apiBase, tokenURL, clientID, resource string, tok *tokenResponse) *Credentials {
	exp := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.ExpiresIn == 0 {
		exp = time.Now().Add(10 * time.Minute) // conservative default
	}
	return &Credentials{
		APIBase:      strings.TrimRight(apiBase, "/"),
		TokenURL:     tokenURL,
		ClientID:     clientID,
		Resource:     resource,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       exp,
	}
}

// callbackHandler serves the loopback redirect: it validates state, captures the
// code, and shows the user a small "you can close this tab" page.
func callbackHandler(state string, codeCh chan<- string, errCh chan<- error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeDone(w, "Authorization failed. You can close this tab.")
			errCh <- fmt.Errorf("login: authorization error: %s", e)
			return
		}
		if q.Get("state") != state {
			writeDone(w, "Authorization failed (state mismatch). You can close this tab.")
			errCh <- fmt.Errorf("login: state mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			writeDone(w, "Authorization failed (no code). You can close this tab.")
			errCh <- fmt.Errorf("login: no authorization code")
			return
		}
		writeDone(w, "Signed in to Dropway. You can close this tab and return to the CLI.")
		codeCh <- code
	})
}

func writeDone(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><meta charset=utf-8><title>Dropway</title>"+
		"<body style=\"font:16px system-ui;display:grid;place-items:center;height:100vh;margin:0\">"+
		"<p>"+msg+"</p></body>")
}

// --- tiny HTTP helpers ------------------------------------------------------

func getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	return doJSON(req, out)
}

func postJSON(ctx context.Context, u string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return doJSON(req, out)
}

func postForm(ctx context.Context, u string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok tokenResponse
	if err := doJSON(req, &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access_token")
	}
	return &tok, nil
}

func doJSON(req *http.Request, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d: %s", req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// --- PKCE / random ----------------------------------------------------------

func randomString(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; if it does, panic rather than emit a weak value.
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
