//go:build integration

// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package integration

import (
	"context"
	"errors"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func TestIntegration_RoomActivity(t *testing.T) {
	ctx := context.Background()
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	pool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool, quota.Unlimited{})
	org := "11111111-1111-1111-1111-111111111111"
	owner := "a0000000-0000-0000-0000-000000000001"
	reviewer := "a0000000-0000-0000-0000-000000000002"
	tn := store.Tenant{OrgID: org, UserID: owner}
	tr := store.Tenant{OrgID: org, UserID: reviewer}
	mustExec(t, "INSERT INTO app.org_meta (id,allow_external_sharing) VALUES ($1,true)", org)
	seedAuthOrg(t, org, "orga")
	must(t, st.EnsureOrgProvisioned(ctx, tn))
	must(t, st.SetProjectRoomsEnabled(ctx, tn, true, store.AuditRecord{}))
	mustExec(t, `CREATE TABLE IF NOT EXISTS identity.member (id text PRIMARY KEY DEFAULT gen_random_uuid()::text,"organizationId" uuid NOT NULL,"userId" uuid NOT NULL,"role" text NOT NULL)`)
	mustExec(t, `GRANT USAGE ON SCHEMA identity TO dropway_app`)
	mustExec(t, `GRANT SELECT ON identity.member TO dropway_app`)
	mustExec(t, `INSERT INTO identity.member ("organizationId","userId","role") VALUES ($1,$2,'member')`, org, reviewer)
	room, err := st.CreateProjectRoom(ctx, tn, "Review", store.AuditRecord{})
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.AddProjectRoomMember(ctx, tn, room.ID, reviewer, "reviewer", store.AuditRecord{}))
	site, err := st.CreateSite(ctx, tn, "activity", projection.AccessOrgOnly)
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.AddProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	// Tied timestamps exercise the UUID tiebreaker, not merely time pagination.
	mustExec(t, `UPDATE app.review_activity SET created_at='2026-01-01T00:00:00Z' WHERE room_id=$1`, room.ID)
	first, err := st.PageRoomActivity(ctx, tr, room.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" || first.Unread != 3 {
		t.Fatalf("first page %+v", first)
	}
	// Inserts after traversal creation are visible only in a fresh traversal.
	mustExec(t, `INSERT INTO app.review_activity (org_id,room_id,actor_user_id,kind,created_at) VALUES ($1,$2,$3,'member_role_changed',clock_timestamp())`, org, room.ID, owner)
	if _, err := st.PageRoomActivity(ctx, tn, room.ID, first.NextCursor, 1); !errors.Is(err, projectroom.ErrBadCursor) {
		t.Fatalf("cross-user cursor accepted: %v", err)
	}
	seen := map[string]bool{}
	cursor := first.NextCursor
	seen[first.Items[0].ID] = true
	for {
		page, err := st.PageRoomActivity(ctx, tr, room.ID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate activity %s", item.ID)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("pagination got %d activities", len(seen))
	}
	fresh, err := st.PageRoomActivity(ctx, tr, room.ID, "", 100)
	if err != nil || len(fresh.Items) != 4 {
		t.Fatalf("fresh traversal %+v %v", fresh, err)
	}
	if _, err := st.PageRoomActivity(ctx, tr, room.ID, "unsigned-legacy-cursor", 1); !errors.Is(err, projectroom.ErrBadCursor) {
		t.Fatalf("foreign cursor %v", err)
	}
	if _, err := st.MarkProjectRoomRead(ctx, tr, room.ID); !errors.Is(err, store.ErrRoomForbidden) {
		t.Fatalf("nonhuman read ack %v", err)
	}
	if _, err := st.MarkProjectRoomRead(store.WithHumanSession(ctx, true), tr, room.ID); err != nil {
		t.Fatal(err)
	}
	page, err := st.PageRoomActivity(ctx, tr, room.ID, "", 100)
	if err != nil || page.Unread != 0 {
		t.Fatalf("read state %+v %v", page, err)
	}
	// Detaching hides historic site metadata, including the detach event itself.
	must(t, st.RemoveProjectRoomSite(ctx, tn, room.ID, site.ID, store.AuditRecord{}))
	page, err = st.PageRoomActivity(ctx, tr, room.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Unread != 0 {
		t.Fatalf("detached site leaked: %+v", page)
	}
	for _, item := range page.Items {
		if item.SiteID != nil {
			t.Fatalf("detached site metadata %+v", item)
		}
	}
	must(t, st.RemoveProjectRoomMember(ctx, tn, room.ID, reviewer, store.AuditRecord{}))
	if _, err := st.PageRoomActivity(ctx, tr, room.ID, first.NextCursor, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked member continuation %v", err)
	}
	if _, err := st.MarkProjectRoomRead(store.WithHumanSession(ctx, true), tr, room.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked member ack %v", err)
	}
	must(t, st.SetProjectRoomsEnabled(ctx, tn, false, store.AuditRecord{}))
	if _, err := st.PageRoomActivity(ctx, tn, room.ID, "", 10); !errors.Is(err, store.ErrProjectRoomsDisabled) {
		t.Fatalf("disabled room %v", err)
	}
}
