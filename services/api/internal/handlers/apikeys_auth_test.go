// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/apikey"
	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// fakeKeyStore is a minimal APIKeyStore for exercising the boundary authenticator's
// fail-closed policy without a database.
type fakeKeyStore struct {
	princ      store.APIKeyPrincipal
	resolved   bool // whether ResolveAPIKey should return the principal
	roleFn     func(orgID, userID string) (string, error)
	resolveErr error
}

// fakeKeyStore satisfies handlers.APIKeyStore.
var _ APIKeyStore = (*fakeKeyStore)(nil)

func (f *fakeKeyStore) ResolveAPIKey(_ context.Context, _ string) (store.APIKeyPrincipal, error) {
	if f.resolveErr != nil {
		return store.APIKeyPrincipal{}, f.resolveErr
	}
	if !f.resolved {
		return store.APIKeyPrincipal{}, store.ErrNotFound
	}
	return f.princ, nil
}
func (f *fakeKeyStore) TouchAPIKeyLastUsed(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeKeyStore) MemberRole(_ context.Context, orgID, userID string) (string, error) {
	if f.roleFn != nil {
		return f.roleFn(orgID, userID)
	}
	return store.RoleMember, nil
}
func (f *fakeKeyStore) CreateAPIKey(context.Context, store.Tenant, store.CreateAPIKeyParams) (store.APIKey, error) {
	return store.APIKey{}, nil
}
func (f *fakeKeyStore) ListAPIKeys(context.Context, store.Tenant) ([]store.APIKey, error) {
	return nil, nil
}
func (f *fakeKeyStore) RevokeAPIKey(context.Context, store.Tenant, string, audit.Context) (store.APIKey, error) {
	return store.APIKey{}, nil
}

// liveKeyStore returns a fake wired to accept a freshly generated secret whose hash
// resolves to a live, active principal.
func liveKeyStore() *fakeKeyStore {
	return &fakeKeyStore{
		resolved: true,
		princ: store.APIKeyPrincipal{
			ID:             "key-1",
			OrgID:          "org-1",
			CreatedBy:      "user-1",
			OrgStatus:      "active",
			APIKeysEnabled: true,
		},
	}
}

func mustSecret(t *testing.T) string {
	t.Helper()
	s, err := apikey.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return s
}

func TestAuthenticateAPIKey_HappyPath(t *testing.T) {
	fs := liveKeyStore()
	fs.roleFn = func(_, _ string) (string, error) { return store.RoleOwner, nil }
	a := NewKeyAuthenticator(fs, nil, false)

	princ, err := a.AuthenticateAPIKey(context.Background(), mustSecret(t))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if princ.KeyID != "key-1" {
		t.Errorf("KeyID = %q, want key-1", princ.KeyID)
	}
	if princ.Claims.Subject != "user-1" {
		t.Errorf("Subject = %q, want the creator user-1", princ.Claims.Subject)
	}
	if princ.Claims.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", princ.Claims.OrgID)
	}
	// The synthesized claims carry the creator's REAL role (for attribution); the
	// member-level ceiling is enforced separately at the admin gate.
	if princ.Claims.Role != store.RoleOwner {
		t.Errorf("Role = %q, want the creator's live role owner", princ.Claims.Role)
	}
}

func TestAuthenticateAPIKey_FailClosed(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		mutate func(*fakeKeyStore)
	}{
		{"unknown hash", func(f *fakeKeyStore) { f.resolved = false }},
		{"revoked", func(f *fakeKeyStore) { f.princ.RevokedAt = &past }},
		{"expired", func(f *fakeKeyStore) { f.princ.ExpiresAt = &past }},
		{"suspended org", func(f *fakeKeyStore) { f.princ.OrgStatus = "suspended" }},
		{"kill switch", func(f *fakeKeyStore) { f.princ.APIKeysEnabled = false }},
		{"creator left", func(f *fakeKeyStore) {
			f.roleFn = func(_, _ string) (string, error) { return "", store.ErrNoMembership }
		}},
		{"member table missing, no fallback", func(f *fakeKeyStore) {
			f.roleFn = func(_, _ string) (string, error) { return "", store.ErrAuthSchemaUnavailable }
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := liveKeyStore()
			c.mutate(fs)
			a := NewKeyAuthenticator(fs, nil, false)
			if _, err := a.AuthenticateAPIKey(context.Background(), mustSecret(t)); err == nil {
				t.Fatalf("%s: expected a fail-closed error, got nil", c.name)
			}
		})
	}
}

func TestAuthenticateAPIKey_MalformedSecret(t *testing.T) {
	a := NewKeyAuthenticator(liveKeyStore(), nil, false)
	if _, err := a.AuthenticateAPIKey(context.Background(), "not-a-key"); err == nil {
		t.Fatalf("expected malformed-secret rejection")
	}
}

func TestAuthenticateAPIKey_FallbackAllowsWhenSchemaMissing(t *testing.T) {
	fs := liveKeyStore()
	fs.roleFn = func(_, _ string) (string, error) { return "", store.ErrAuthSchemaUnavailable }
	a := NewKeyAuthenticator(fs, nil, true) // allowFallback

	princ, err := a.AuthenticateAPIKey(context.Background(), mustSecret(t))
	if err != nil {
		t.Fatalf("with fallback enabled, expected success, got %v", err)
	}
	if princ.Claims.Role != store.RoleMember {
		t.Errorf("fallback role = %q, want member", princ.Claims.Role)
	}
}

func TestAuthenticateAPIKey_RateLimited(t *testing.T) {
	// burst 1, so the second immediate request is throttled.
	l := newRateLimiter(1, 1, time.Minute)
	a := NewKeyAuthenticator(liveKeyStore(), l, false)
	secret := mustSecret(t)

	if _, err := a.AuthenticateAPIKey(context.Background(), secret); err != nil {
		t.Fatalf("first request should pass: %v", err)
	}
	_, err := a.AuthenticateAPIKey(context.Background(), secret)
	var rle *middleware.RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("second request: want *RateLimitedError, got %v", err)
	}
	if rle.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", rle.RetryAfter)
	}
}
