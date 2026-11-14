// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package jobtoken

import (
	"strings"
	"testing"
	"time"
)

func sample() Claims {
	return Claims{
		OrgID: "org", RoomID: "room", SiteID: "site", VersionID: "ver",
		AttemptID: "att", SessionID: "ses", InitiatorUserID: "user",
		CredentialRef: "11111111-1111-1111-1111-111111111111",
		Provider:      "anthropic", ModelID: "claude-sonnet-4-5",
		HarnessID: "pi", HarnessVersion: "unverified",
	}
}

func bind(aud, scope string) Binding {
	c := sample()
	return Binding{
		Audience: aud, Scope: scope,
		OrgID: c.OrgID, RoomID: c.RoomID, SiteID: c.SiteID, VersionID: c.VersionID,
		AttemptID: c.AttemptID, SessionID: c.SessionID, InitiatorUserID: c.InitiatorUserID,
		CredentialRef: c.CredentialRef, Provider: c.Provider, ModelID: c.ModelID,
		HarnessID: c.HarnessID, HarnessVersion: c.HarnessVersion,
	}
}

func TestTokenPinnedAndNotAProviderKey(t *testing.T) {
	s, err := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	tok, err := s.IssueAttempt(sample(), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tok, "sk-") || strings.Contains(tok, "fly") {
		t.Fatal("token must not carry a provider or fly secret")
	}
	got, err := s.Verify(tok, bind(AudAttempt, ScopeSubmit), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelID != "claude-sonnet-4-5" || got.CredentialRef != sample().CredentialRef {
		t.Fatalf("snapshot drifted: %+v", got)
	}
	gw, err := s.IssueGateway(sample(), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(gw, bind(AudAttempt, ScopeSubmit), now); err == nil {
		t.Fatal("gateway token must not submit an attempt")
	}
	other := bind(AudAttempt, ScopeSubmit)
	other.OrgID = "other-org"
	if _, err := s.Verify(tok, other, now); err != ErrBinding {
		t.Fatalf("cross-tenant err = %v", err)
	}
	if _, err := s.Verify(tok, bind(AudAttempt, ScopeSubmit), now.Add(MaxTTL)); err != ErrExpired {
		t.Fatal("expired token must fail")
	}
}

func TestTokenRejectsWildcardAndShortKey(t *testing.T) {
	if _, err := NewSigner([]byte("short")); err != ErrDisabled {
		t.Fatalf("short key err = %v", err)
	}
	if hasScope([]string{"sites:*"}, "sites:read") {
		t.Fatal("wildcard scopes are not honored")
	}
}
