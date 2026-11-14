// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestCredentialClassMixedAudienceIsMCP(t *testing.T) {
	const apiAud = "https://api.dropway.test"
	const mcpAud = "https://mcp.dropway.test/mcp"
	v := NewVerifier("http://jwks", "issuer", apiAud, WithExtraAudiences(MCPResourceAudiences("https://mcp.dropway.test")...))
	human := &Claims{RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{apiAud}}}
	if got := v.CredentialClass(human); got != ClassHumanSession {
		t.Fatalf("human class = %q", got)
	}
	for _, aud := range []jwt.ClaimStrings{
		{mcpAud},
		{apiAud, mcpAud},
		{mcpAud, apiAud},
		{"https://mcp.dropway.test", apiAud},
	} {
		got := v.CredentialClass(&Claims{RegisteredClaims: jwt.RegisteredClaims{Audience: aud}})
		if got != ClassMCPOAuth {
			t.Fatalf("audience %v class = %q, want mcp", aud, got)
		}
	}
	if got := v.CredentialClass(&Claims{}); got != "" {
		t.Fatalf("unmatched class = %q, want unclassified", got)
	}
	if got := v.CredentialClass(nil); got != "" {
		t.Fatalf("nil claims class = %q", got)
	}
}

func TestVerifyMixedAudienceTokenClassifiesMCP(t *testing.T) {
	pub, priv := newKey(t)
	js := newJWKSServer(map[string]ed25519.PublicKey{"k1": pub})
	defer js.Close()
	v := NewVerifier(js.URL, testIssuer, testAud,
		WithHTTPClient(js.Client()),
		WithExtraAudiences(MCPResourceAudiences("https://mcp.dropway.test")...))
	mcpAud := MCPResourceAudiences("https://mcp.dropway.test")[2]
	for _, aud := range []jwt.ClaimStrings{{testAud, mcpAud}, {mcpAud, testAud}} {
		claims := goodClaims()
		claims.Audience = aud
		raw := signEdDSA(t, priv, "k1", claims)
		got, err := v.Verify(context.Background(), raw)
		if err != nil {
			t.Fatalf("verify %v: %v", aud, err)
		}
		if class := v.CredentialClass(got); class != ClassMCPOAuth {
			t.Fatalf("verified %v class = %q", aud, class)
		}
	}
	human := goodClaims()
	got, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", human))
	if err != nil {
		t.Fatal(err)
	}
	if v.CredentialClass(got) != ClassHumanSession {
		t.Fatalf("api-only class = %q", v.CredentialClass(got))
	}
	if got.ExpiresAt == nil || got.ExpiresAt.Before(time.Now()) {
		t.Fatal("expected a verified unexpired token")
	}
}
