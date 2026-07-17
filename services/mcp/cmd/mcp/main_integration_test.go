// SPDX-License-Identifier: FSL-1.1-Apache-2.0

//go:build integration

// Integration test for the MCP server's HTTP surface. It stands the real mux up
// in-process (newMux) against a REAL Postgres (the CI `mcp` job's service) and a
// test JWKS, then hits the endpoints end to end:
//
//   - GET  /healthz                                  → 200
//   - GET  /.well-known/oauth-protected-resource     → RFC 9728 doc (resource + AS)
//   - POST /mcp  (no token)                           → 401 + WWW-Authenticate
//   - POST /mcp  (garbage token)                      → 401
//   - POST /mcp  (valid token) initialize             → 200, serverInfo "dropway"
//   - POST /mcp  (valid token, org mcp_enabled=false) → 403  (per-request kill-switch)
//   - POST /mcp  (valid token, re-enabled)            → 200
//
// Run with:  go test -tags integration ./services/mcp/cmd/mcp/...
// Env (defaults match the CI job): MCP_IT_OWNER_DSN, MCP_IT_APP_DSN.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreauth "github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/services/mcp/internal/store"
	"github.com/danielpang/dropway/services/mcp/internal/tools"
)

const (
	itIssuer   = "http://dashboard.test"
	itResource = "http://mcp.test" // OAuth resource == audience the verifier pins
	itKID      = "it-key-1"
	itOrgID    = "33333333-3333-3333-3333-333333333333"
	itUserID   = "44444444-4444-4444-4444-444444444444"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// stubBlobs satisfies tools.Blobs; initialize never reads blobs, so the methods
// just error if something unexpectedly calls them.
type stubBlobs struct{}

func (stubBlobs) GetManifest(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("stub: GetManifest not implemented")
}
func (stubBlobs) GetSkillManifest(context.Context, string, string, string) ([]byte, error) {
	return nil, errors.New("stub: GetSkillManifest not implemented")
}
func (stubBlobs) GetBlob(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("stub: GetBlob not implemented")
}

// jwks serves an Ed25519 public key as an OKP JWK so the real coreauth.Verifier
// can validate tokens we mint with the matching private key.
func newJWKS(t *testing.T, pub ed25519.PublicKey) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "kid": itKID,
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}},
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func mintToken(t *testing.T, priv ed25519.PrivateKey, aud string) string {
	t.Helper()
	claims := coreauth.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    itIssuer,
			Audience:  jwt.ClaimStrings{aud},
			Subject:   itUserID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		},
		OrgID: itOrgID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = itKID
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

// setMCPEnabled flips org_meta.mcp_enabled for the test org (owner connection,
// BYPASSRLS) within a tenant-scoped tx so the WITH CHECK / RLS policies are happy.
func setMCPEnabled(t *testing.T, ctx context.Context, owner *pgxpool.Pool, enabled bool) {
	t.Helper()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", itOrgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE app.org_meta SET mcp_enabled = $2 WHERE id = $1", itOrgID, enabled); err != nil {
		t.Fatalf("update mcp_enabled: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func initializeBody() string {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "it", "version": "0"},
		},
	})
	return string(b)
}

func postMCP(t *testing.T, url, token string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(initializeBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestMCPServer_Endpoints(t *testing.T) {
	ctx := context.Background()

	ownerDSN := env("MCP_IT_OWNER_DSN", "postgres://postgres:postgres@localhost:5432/dropway?sslmode=disable")
	appDSN := env("MCP_IT_APP_DSN", "postgres://dropway_app:dropway_app_ci_pw@localhost:5432/dropway?sslmode=disable")

	owner, err := pgxpool.New(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("owner pool: %v", err)
	}
	defer owner.Close()

	// Seed the test org (mcp_enabled defaults true). org_usage satisfies any FK/UX.
	seed := func() {
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatalf("seed begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org_id', $1, true)", itOrgID); err != nil {
			t.Fatalf("seed set org: %v", err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO app.org_meta (id) VALUES ($1) ON CONFLICT (id) DO NOTHING", itOrgID); err != nil {
			t.Fatalf("seed org_meta: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("seed commit: %v", err)
		}
	}
	seed()

	// The store connects as the non-BYPASSRLS dropway_app role, exactly like prod.
	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("app pool: %v", err)
	}
	defer appPool.Close()

	// Real verifier pointed at a test JWKS; aud == itResource (the resource the mux
	// advertises and we mint into the token).
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	jwks := newJWKS(t, pub)
	defer jwks.Close()

	// Mirror production wiring: accept the trailing-slash resource form too (some
	// MCP clients, e.g. mcp-remote, canonicalize the resource and append "/").
	verifier := coreauth.NewVerifier(jwks.URL, itIssuer, itResource,
		coreauth.WithExtraAudiences(itResource+"/"))
	st := store.New(appPool)
	svc := &tools.Service{Store: st, Skills: st, Chats: st, Blobs: stubBlobs{}}

	ts := httptest.NewServer(newMux(verifier, st, svc, itResource, itIssuer))
	defer ts.Close()

	// 1) health
	if resp, err := http.Get(ts.URL + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("/healthz = %v (err %v), want 200", respCode(resp), err)
	}

	// 2) RFC 9728 protected-resource metadata
	{
		resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("resource metadata status = %v (err %v)", respCode(resp), err)
		}
		var doc struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&doc)
		resp.Body.Close()
		if doc.Resource != itResource {
			t.Fatalf("resource = %q, want %q", doc.Resource, itResource)
		}
		if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != itIssuer {
			t.Fatalf("authorization_servers = %v, want [%q]", doc.AuthorizationServers, itIssuer)
		}
	}

	// 3) /mcp with no token → 401 + WWW-Authenticate pointing at the resource metadata
	{
		resp, _ := postMCP(t, ts.URL, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no-token /mcp = %d, want 401", resp.StatusCode)
		}
		if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "resource_metadata=") {
			t.Fatalf("WWW-Authenticate = %q, want resource_metadata hint", wa)
		}
	}

	// 4) /mcp with a garbage token → 401
	{
		resp, _ := postMCP(t, ts.URL, "not-a-real-jwt")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad-token /mcp = %d, want 401", resp.StatusCode)
		}
	}

	// 5) /mcp with a valid token → 200 initialize (serverInfo "dropway"). Both
	// audience forms must work: the bare resource AND the trailing-slash variant a
	// client like mcp-remote sends — neither should be rejected.
	token := mintToken(t, priv, itResource)
	for _, tc := range []struct {
		name string
		aud  string
	}{
		{"no trailing slash", itResource},
		{"trailing slash", itResource + "/"},
	} {
		tok := mintToken(t, priv, tc.aud)
		resp, body := postMCP(t, ts.URL, tok)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("valid-token initialize [aud=%s] = %d, want 200; body: %s", tc.name, resp.StatusCode, body)
		}
		if !strings.Contains(body, "dropway") || !strings.Contains(body, "serverInfo") {
			t.Fatalf("initialize body [aud=%s] missing serverInfo/dropway: %s", tc.name, body)
		}
	}

	// A token for an UNRELATED resource is still rejected (no blanket acceptance).
	{
		resp, _ := postMCP(t, ts.URL, mintToken(t, priv, "http://evil.example"))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong-audience token /mcp = %d, want 401", resp.StatusCode)
		}
	}

	// 6) org mcp_enabled=false → same token now 403 (per-request kill-switch)
	setMCPEnabled(t, ctx, owner, false)
	{
		resp, body := postMCP(t, ts.URL, token)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("mcp_enabled=false /mcp = %d, want 403; body: %s", resp.StatusCode, body)
		}
	}

	// 7) re-enable → 200 again
	setMCPEnabled(t, ctx, owner, true)
	{
		resp, body := postMCP(t, ts.URL, token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("re-enabled /mcp = %d, want 200; body: %s", resp.StatusCode, body)
		}
	}
}

func respCode(r *http.Response) any {
	if r == nil {
		return "nil"
	}
	return r.StatusCode
}
