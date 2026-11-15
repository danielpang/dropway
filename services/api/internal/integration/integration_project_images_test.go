//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

type oneImageQuota struct{ quota.Unlimited }

func (oneImageQuota) Allow(tier string, resource quota.Resource, current int64) error {
	if resource == quota.ResourceSitePerOrg && current >= 1 {
		return &quota.ExceededError{Limit: resource, Current: current, Max: 1, PlanTier: tier}
	}
	return nil
}

func TestIntegration_ProjectImageResource(t *testing.T) {
	startPostgres(t)
	applyMigrations(t, repoRoot(t))
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, appDSN)
	must(t, err)
	defer pool.Close()
	st := store.New(pool, oneImageQuota{})
	org := "11111111-1111-1111-1111-111111111111"
	user := "a0000000-0000-0000-0000-000000000001"
	tenant := store.Tenant{OrgID: org, UserID: user}
	mustExec(t, `INSERT INTO app.org_meta(id,allow_external_sharing,default_visibility) VALUES($1,true,'public')`, org)
	seedAuthOrg(t, org, "images")
	must(t, st.EnsureOrgProvisioned(ctx, tenant))
	image, err := st.CreateProjectImage(ctx, tenant, "image-test", "Landing page.png")
	must(t, err)
	if image.ResourceKind != "image" || image.Title != "Landing page.png" || image.AccessMode != "org_only" || image.FeedVisible {
		t.Fatalf("unsafe image defaults: %+v", image)
	}
	listed, err := st.ListSites(ctx, tenant)
	must(t, err)
	if len(listed) != 1 || listed[0].ResourceKind != "image" {
		t.Fatalf("image identity lost: %+v", listed)
	}
	if _, err = st.GetSite(ctx, store.Tenant{OrgID: "22222222-2222-2222-2222-222222222222", UserID: user}, image.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant image visible: %v", err)
	}
	if _, err = st.CreateProjectImage(ctx, tenant, "image-two", "Other.png"); err == nil {
		t.Fatal("image bypassed resource quota")
	}
	room, err := st.CreateProjectRoom(ctx, tenant, "Design review", store.AuditRecord{})
	must(t, err)
	version, err := st.CreateSiteVersion(ctx, tenant, store.CreateSiteVersionParams{SiteID: image.ID, ContentHash: strings.Repeat("ab", 32), SizeBytes: 100, Status: "ready"})
	must(t, err)
	must(t, st.AddProjectRoomSite(ctx, tenant, room.ID, image.ID, store.AuditRecord{}))
	thread, err := st.CreateReviewThread(ctx, tenant, room.ID, image.ID, version.ID, "/", "point", []byte(`{"x":0.5,"y":0.5,"viewport":{"width":100,"height":100},"rendition":{"width":100,"height":100},"scroll":{"x":0,"y":0},"image":{"intrinsic_width":100,"intrinsic_height":100,"x":50,"y":50,"stable_id":"static-v1:project-image"}}`))
	must(t, err)
	must(t, st.SetProjectRoomsEnabled(ctx, tenant, false, store.AuditRecord{}))
	if _, err := st.CreateProjectImage(ctx, tenant, "image-disabled", "Disabled.png"); !errors.Is(err, store.ErrProjectRoomsDisabled) {
		t.Fatalf("image upload bypassed organization disable: %v", err)
	}
	if thread.VersionID != version.ID || thread.SiteID != image.ID {
		t.Fatalf("image comment lost immutable version: %+v", thread)
	}
}
