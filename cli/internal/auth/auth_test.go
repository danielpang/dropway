// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// --- store ------------------------------------------------------------------

func TestStore_RoundTripAndPerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	in := &Credentials{
		APIBase: "http://localhost:8080", TokenURL: "http://as/token", ClientID: "c1",
		Resource: "http://localhost:8080", AccessToken: "at", RefreshToken: "rt",
		Expiry: time.Now().Add(time.Hour).Round(time.Second),
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path, _ := CredentialsPath()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("credentials perm = %o, want 600", perm)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" || got.ClientID != "c1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if err := Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Load(); !os.IsNotExist(err) {
		t.Errorf("after Delete, Load err = %v, want not-exist", err)
	}
	if err := Delete(); err != nil {
		t.Errorf("Delete on missing should be nil, got %v", err)
	}
}

// --- token resolution -------------------------------------------------------

func TestToken_EnvWins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DROPWAY_API_KEY", "ci-token")
	got, err := Token(context.Background(), "http://localhost:8080")
	if err != nil || got != "ci-token" {
		t.Fatalf("Token = %q, %v; want the env token", got, err)
	}
}

func TestToken_NotSignedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv("DROPWAY_API_KEY")
	if _, err := Token(context.Background(), "http://localhost:8080"); err == nil ||
		!strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want a sign-in error", err)
	}
}

func TestToken_ValidStored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv("DROPWAY_API_KEY")
	must(t, Save(&Credentials{
		APIBase: "http://localhost:8080", AccessToken: "fresh",
		Expiry: time.Now().Add(time.Hour),
	}))
	got, err := Token(context.Background(), "http://localhost:8080")
	if err != nil || got != "fresh" {
		t.Fatalf("Token = %q, %v; want the stored token", got, err)
	}
}

func TestToken_WrongAPIBase(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv("DROPWAY_API_KEY")
	must(t, Save(&Credentials{APIBase: "http://other", AccessToken: "x", Expiry: time.Now().Add(time.Hour)}))
	if _, err := Token(context.Background(), "http://localhost:8080"); err == nil ||
		!strings.Contains(err.Error(), "login --api") {
		t.Fatalf("err = %v, want a wrong-host error", err)
	}
}

func TestToken_RefreshesWhenExpired(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Unsetenv("DROPWAY_API_KEY")

	var grant string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant = r.Form.Get("grant_type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed", "refresh_token": "rt2", "expires_in": 600,
		})
	}))
	defer ts.Close()

	must(t, Save(&Credentials{
		APIBase: "http://localhost:8080", TokenURL: ts.URL, ClientID: "c1",
		Resource: "http://localhost:8080", AccessToken: "stale", RefreshToken: "rt1",
		Expiry: time.Now().Add(-time.Minute), // expired
	}))

	got, err := Token(context.Background(), "http://localhost:8080")
	if err != nil || got != "refreshed" {
		t.Fatalf("Token = %q, %v; want a refreshed token", got, err)
	}
	if grant != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", grant)
	}
	// The new token (and rotated refresh token) should be persisted.
	c, _ := Load()
	if c.AccessToken != "refreshed" || c.RefreshToken != "rt2" {
		t.Errorf("refresh not persisted: %+v", c)
	}
}

// --- full login flow (fake browser) -----------------------------------------

func TestLogin_FullFlow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	mux := http.NewServeMux()
	var base string // set after server starts
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": base, "authorization_servers": []string{base},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"registration_endpoint":  base + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "cli-123"})
	})
	var sawVerifier, sawResource string
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sawVerifier = r.Form.Get("code_verifier")
		sawResource = r.Form.Get("resource")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "AT", "refresh_token": "RT", "expires_in": 600,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	base = ts.URL

	// The "browser": instead of opening a window, extract the redirect + state from
	// the authorize URL and hit the CLI's loopback callback with a code.
	opener := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=auth-code&state=" + q.Get("state"))
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
		return nil
	}

	creds, err := Login(context.Background(), base, opener)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if creds.AccessToken != "AT" || creds.RefreshToken != "RT" {
		t.Errorf("tokens not captured: %+v", creds)
	}
	if creds.ClientID != "cli-123" || creds.Resource != base || creds.APIBase != base {
		t.Errorf("creds metadata wrong: %+v", creds)
	}
	if sawVerifier == "" {
		t.Error("token exchange should carry a PKCE code_verifier")
	}
	if sawResource != base {
		t.Errorf("token exchange resource = %q, want %q", sawResource, base)
	}
}

// --- PKCE -------------------------------------------------------------------

func TestS256_MatchesSpec(t *testing.T) {
	v := "test-verifier"
	want := base64.RawURLEncoding.EncodeToString(func() []byte { s := sha256.Sum256([]byte(v)); return s[:] }())
	if got := s256(v); got != want {
		t.Errorf("s256 = %q, want %q", got, want)
	}
	if strings.ContainsAny(want, "=+/") {
		t.Error("challenge must be base64url without padding")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
