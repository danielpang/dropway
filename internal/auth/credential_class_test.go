// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package auth

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestCredentialClassUsesVerifiedAudience(t *testing.T) {
	v := NewVerifier("http://jwks", "issuer", "https://api.dropway.test", WithExtraAudiences(MCPResourceAudiences("https://mcp.dropway.test")...))
	human := &Claims{RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"https://api.dropway.test"}}}
	if got := v.CredentialClass(human); got != ClassHumanSession {
		t.Fatalf("human class = %q", got)
	}
	mcp := &Claims{RegisteredClaims: jwt.RegisteredClaims{Audience: jwt.ClaimStrings{"https://mcp.dropway.test/mcp"}}}
	if got := v.CredentialClass(mcp); got != ClassMCPOAuth {
		t.Fatalf("mcp class = %q", got)
	}
	if got := v.CredentialClass(&Claims{}); got != ClassMCPOAuth {
		t.Fatalf("unmatched extra-audience verifier class = %q", got)
	}
}
