// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package config

import "testing"

// When JWKS_URL is set, the verifier pins iss/aud — but golang-jwt skips those
// checks if the expected value is empty, so Load MUST refuse an empty
// JWT_ISSUER/JWT_AUDIENCE (audit HIGH: otherwise any EdDSA token is accepted).
func TestLoad_RequiresIssAudWhenJWKSSet(t *testing.T) {
	base := map[string]string{
		"JWKS_URL":     "https://app.example/api/auth/jwks",
		"JWT_ISSUER":   "https://app.example",
		"JWT_AUDIENCE": "https://api.example",
	}

	cases := []struct {
		name    string
		mutate  map[string]string // overrides applied on top of base
		wantErr bool
	}{
		{"all set → ok", nil, false},
		{"empty issuer → error", map[string]string{"JWT_ISSUER": ""}, true},
		{"empty audience → error", map[string]string{"JWT_AUDIENCE": ""}, true},
		{"both empty → error", map[string]string{"JWT_ISSUER": "", "JWT_AUDIENCE": ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Clean slate so a leaked env var can't mask the case.
			for _, k := range []string{"JWKS_URL", "JWT_ISSUER", "JWT_AUDIENCE", "PORT"} {
				t.Setenv(k, "")
			}
			for k, v := range base {
				t.Setenv(k, v)
			}
			for k, v := range c.mutate {
				t.Setenv(k, v)
			}
			_, err := Load()
			if c.wantErr && err == nil {
				t.Fatalf("Load() = nil error, want error")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("Load() = %v, want nil error", err)
			}
		})
	}
}

// With NO JWKS configured (auth disabled / DB-less dev), empty iss/aud is fine —
// the guard only applies when the API actually verifies tokens.
func TestLoad_NoJWKS_AllowsEmptyIssAud(t *testing.T) {
	for _, k := range []string{"JWKS_URL", "JWT_ISSUER", "JWT_AUDIENCE", "PORT"} {
		t.Setenv(k, "")
	}
	if _, err := Load(); err != nil {
		t.Fatalf("Load() with no JWKS should not error, got %v", err)
	}
}
