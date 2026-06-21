//go:build integration

// Membership-audit integration test: the invite/join audit trail exercised against
// real Postgres 16 (goose migrations as the non-BYPASSRLS dropway_app role) + a real
// Better Auth `identity.member` table.
//
// Run with:
//
//	go test -tags integration -run TestIntegration_MembershipAudit ./services/api/internal/integration/...
//
// Why this exists: the handler unit tests fake the store, so the LIVE membership read
// (identity.member) and the RLS audit write are never exercised together. This ties
// them: the same MemberRole lookup the RecordMemberInvite/RecordMemberJoin handlers
// gate on, then a real member.invite + member.join row written under RLS and read back
// newest-first, plus cross-tenant isolation.
//
// Covered (all falsifiable):
//   - MemberRole resolves the seeded admin (IsAdminRole) + member roles, and returns
//     ErrNoMembership for a non-member (the RecordMemberJoin 400 path).
//   - WriteAudit(member.invite) by the admin and WriteAudit(member.join) by the joiner
//     (target = the joiner's OWN id) land RLS-scoped rows; ListAudit reads them back
//     newest-first with the actor, target, and metadata preserved.
//   - A DIFFERENT org sees NONE of those rows (cross-tenant audit isolation).

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/audit"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestIntegration_MembershipAudit(t *testing.T) {
	ctx := context.Background()
	repoRoot := repoRoot(t)

	startPostgres(t)
	applyMigrations(t, repoRoot)

	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as dropway_app: %v", err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})

	orgA := "55555555-5555-5555-5555-555555555551"
	orgB := "55555555-5555-5555-5555-555555555552"
	admin := "a0000000-0000-0000-0000-0000000005a1"  // the inviter (admin in org A)
	joiner := "a0000000-0000-0000-0000-0000000005a2" // the new member who accepted
	outsider := "a0000000-0000-0000-0000-0000000005a3"
	tAdmin := store.Tenant{OrgID: orgA, UserID: admin}
	tJoiner := store.Tenant{OrgID: orgA, UserID: joiner}
	tB := store.Tenant{OrgID: orgB, UserID: "b0000000-0000-0000-0000-0000000005b1"}

	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgA)
	mustExec(t, "INSERT INTO app.org_meta (id, allow_external_sharing) VALUES ($1, true)", orgB)
	seedAuthOrg(t, orgA, "orgaudit-a")
	seedAuthOrg(t, orgB, "orgaudit-b")
	must(t, st.EnsureOrgProvisioned(ctx, tAdmin))
	must(t, st.EnsureOrgProvisioned(ctx, tB))

	// Better Auth membership the Go API reads LIVE: an admin (the inviter) and a plain
	// member (the new joiner) in org A. This is what RecordMemberInvite (admin gate)
	// and RecordMemberJoin (membership re-check) authorize against.
	seedAuthMemberTable(t)
	insertMember(t, orgA, admin, store.RoleAdmin)
	insertMember(t, orgA, joiner, store.RoleMember)

	// =======================================================================
	// 1. The live-membership authorization the two handlers actually gate on.
	// =======================================================================
	role, err := st.MemberRole(ctx, orgA, admin)
	must(t, err)
	if role != store.RoleAdmin || !store.IsAdminRole(role) {
		t.Fatalf("admin MemberRole = %q (IsAdmin=%v), want admin (RecordMemberInvite gate)", role, store.IsAdminRole(role))
	}
	jrole, err := st.MemberRole(ctx, orgA, joiner)
	must(t, err)
	if jrole != store.RoleMember {
		t.Fatalf("joiner MemberRole = %q, want member", jrole)
	}
	// RecordMemberJoin rejects a non-member (handler maps ErrNoMembership → 400).
	if _, err := st.MemberRole(ctx, orgA, outsider); !errors.Is(err, store.ErrNoMembership) {
		t.Fatalf("outsider MemberRole err = %v, want ErrNoMembership (the join 400 path)", err)
	}

	// =======================================================================
	// 2. member.invite recorded by the admin, scoped by RLS to org A.
	// =======================================================================
	_, err = st.WriteAudit(ctx, tAdmin, store.AuditRecord{
		Action:   audit.ActionMemberInvite,
		Target:   "invite:new@team.com",
		Metadata: map[string]any{"email": "new@team.com", "role": store.RoleMember},
		Ctx:      audit.Context{ActorUser: admin, IP: "203.0.113.9", RequestID: "req-inv-1", TraceID: "req-inv-1"},
	})
	must(t, err)

	// =======================================================================
	// 3. member.join recorded by the joiner for THEMSELVES (target = own id).
	// =======================================================================
	_, err = st.WriteAudit(ctx, tJoiner, store.AuditRecord{
		Action:   audit.ActionMemberJoin,
		Target:   "member:" + joiner,
		Metadata: map[string]any{"role": jrole},
		Ctx:      audit.Context{ActorUser: joiner, RequestID: "req-join-1", TraceID: "req-join-1"},
	})
	must(t, err)

	// =======================================================================
	// 4. Read back newest-first under org A; assert actor/target/metadata.
	// =======================================================================
	entries, err := st.ListAudit(ctx, tAdmin, store.ListAuditParams{Limit: 10})
	must(t, err)
	if len(entries) != 2 {
		t.Fatalf("expected 2 membership audit rows for org A, got %d", len(entries))
	}
	// join was written last → newest-first.
	join := entries[0]
	if join.Action != string(audit.ActionMemberJoin) {
		t.Errorf("newest action = %q, want member.join", join.Action)
	}
	if join.Target != "member:"+joiner {
		t.Errorf("join target = %q, want member:%s", join.Target, joiner)
	}
	if join.ActorUser == nil || *join.ActorUser != joiner {
		t.Errorf("join actor_user = %v, want %s (the joiner records themselves)", join.ActorUser, joiner)
	}
	if join.Metadata["role"] != store.RoleMember {
		t.Errorf("join metadata.role = %v, want member", join.Metadata["role"])
	}
	invite := entries[1]
	if invite.Action != string(audit.ActionMemberInvite) {
		t.Errorf("older action = %q, want member.invite", invite.Action)
	}
	if invite.Target != "invite:new@team.com" {
		t.Errorf("invite target = %q, want invite:new@team.com", invite.Target)
	}
	if invite.ActorUser == nil || *invite.ActorUser != admin {
		t.Errorf("invite actor_user = %v, want %s (the inviter)", invite.ActorUser, admin)
	}
	if invite.Metadata["email"] != "new@team.com" || invite.Metadata["role"] != store.RoleMember {
		t.Errorf("invite metadata = %+v, want {email:new@team.com, role:member}", invite.Metadata)
	}

	// =======================================================================
	// 5. Cross-tenant isolation: org B sees NONE of org A's membership rows.
	// =======================================================================
	bEntries, err := st.ListAudit(ctx, tB, store.ListAuditParams{Limit: 10})
	must(t, err)
	if len(bEntries) != 0 {
		t.Fatalf("AUDIT LEAK: org B sees %d of org A's membership rows", len(bEntries))
	}

	t.Log("PASS: live identity.member role check (admin/member/non-member) + member.invite/member.join RLS audit write/read newest-first + cross-tenant isolation")
}
