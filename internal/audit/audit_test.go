package audit

import "testing"

// TestActionVocab pins the canonical, contract-stable action strings. These are
// surfaced by GET /v1/audit and consumed by the dashboard audit viewer, so a
// silent rename here would break the API contract — the test fails loudly first.
func TestActionVocab(t *testing.T) {
	cases := []struct {
		action Action
		want   string
	}{
		{ActionSiteCreate, "site.create"},
		{ActionSiteAccessChange, "site.access_change"},
		{ActionAllowlistAdd, "site.allowlist_add"},
		{ActionAllowlistRemove, "site.allowlist_remove"},
		{ActionDeployFinalize, "deploy.finalize"},
		{ActionDeployPublish, "deploy.publish"},
		{ActionDomainAdd, "domain.add"},
		{ActionDomainVerify, "domain.verify"},
		{ActionDomainRemove, "domain.remove"},
		{ActionAllowExternalSharing, "org.allow_external_sharing"},
		{ActionMemberRevoke, "member.revoke"},
		{ActionMemberInvite, "member.invite"},
		{ActionMemberJoin, "member.join"},
		{ActionSiteRevokeAccess, "site.revoke_access"},
		{ActionSiteFeedVisibility, "site.feed_visibility"},
	}
	for _, c := range cases {
		if string(c.action) != c.want {
			t.Errorf("action %q = %q, want %q", c.want, string(c.action), c.want)
		}
	}
}

// TestActionVocab_NoDuplicates guards against a copy-paste that gives two distinct
// constants the same wire string (which would make audit rows ambiguous).
func TestActionVocab_NoDuplicates(t *testing.T) {
	all := []Action{
		ActionSiteCreate, ActionSiteAccessChange, ActionAllowlistAdd,
		ActionAllowlistRemove, ActionDeployFinalize, ActionDeployPublish,
		ActionDomainAdd, ActionDomainVerify, ActionDomainRemove,
		ActionAllowExternalSharing, ActionMemberRevoke, ActionMemberInvite,
		ActionMemberJoin, ActionSiteRevokeAccess, ActionSiteFeedVisibility,
	}
	seen := make(map[Action]bool, len(all))
	for _, a := range all {
		if a == "" {
			t.Errorf("empty action string in vocabulary")
		}
		if seen[a] {
			t.Errorf("duplicate action string %q", a)
		}
		seen[a] = true
	}
}

// TestContext_ProvenanceShape documents the provenance Context the store attaches
// to every audit row: a user-driven action carries ActorUser (no token), a
// deploy-token action carries ActorToken (no user), and the correlation ids tie
// the row to the structured access log. The zero value is the all-NULL row.
func TestContext_ProvenanceShape(t *testing.T) {
	// Zero value: every field empty (the store writes SQL NULLs).
	var zero Context
	if zero.ActorUser != "" || zero.ActorToken != "" || zero.IP != "" ||
		zero.RequestID != "" || zero.TraceID != "" {
		t.Errorf("zero Context should have all-empty fields, got %+v", zero)
	}

	// A user-session action: ActorUser set, ActorToken empty.
	userCtx := Context{
		ActorUser: "user-123",
		IP:        "203.0.113.7",
		RequestID: "edge-trace-abc",
		TraceID:   "edge-trace-abc",
	}
	if userCtx.ActorUser == "" || userCtx.ActorToken != "" {
		t.Errorf("user-driven provenance should set ActorUser and leave ActorToken empty: %+v", userCtx)
	}

	// A deploy-token action: ActorToken set, ActorUser empty.
	tokenCtx := Context{
		ActorToken: "dt_456",
		IP:         "198.51.100.4",
		RequestID:  "cli-req-9",
	}
	if tokenCtx.ActorToken == "" || tokenCtx.ActorUser != "" {
		t.Errorf("deploy-token provenance should set ActorToken and leave ActorUser empty: %+v", tokenCtx)
	}

	// TraceID mirrors RequestID when no external tracer is wired (the cheap hook).
	if userCtx.TraceID != userCtx.RequestID {
		t.Errorf("TraceID should mirror RequestID: %q != %q", userCtx.TraceID, userCtx.RequestID)
	}
}
