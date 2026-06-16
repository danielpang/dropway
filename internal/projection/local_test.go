package projection

import (
	"context"
	"path/filepath"
	"testing"
)

func validRoute(ver string) RouteValue {
	return RouteValue{
		OrgID: "11111111-1111-1111-1111-111111111111", SiteID: "s", VersionID: ver,
		AccessMode: AccessPublic, SchemaVersion: SchemaVersion,
	}
}

func TestLocal_PutGetDelete(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	host := "site.dropwaycontent.com"

	if err := l.PutRoute(ctx, host, validRoute("v1")); err != nil {
		t.Fatal(err)
	}
	if rv, ok := l.Get(host); !ok || rv.VersionID != "v1" {
		t.Fatalf("get = %+v %v", rv, ok)
	}
	// PutRoute upsert (publish/rollback pointer flip).
	if err := l.PutRoute(ctx, host, validRoute("v2")); err != nil {
		t.Fatal(err)
	}
	if rv, _ := l.Get(host); rv.VersionID != "v2" {
		t.Errorf("after flip = %+v", rv)
	}
	if err := l.DeleteRoute(ctx, host); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.Get(host); ok {
		t.Error("route should be gone after delete")
	}
}

func TestLocal_RejectsInvalid(t *testing.T) {
	if err := NewLocal().PutRoute(context.Background(), "h", RouteValue{}); err == nil {
		t.Error("invalid route should be rejected")
	}
}

func TestLocal_RebuildReplacesAll(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	_ = l.PutRoute(ctx, "old.host", validRoute("v0"))

	routes := map[string]RouteValue{
		"a.dropwaycontent.com": validRoute("va"),
		"b.dropwaycontent.com": validRoute("vb"),
	}
	if err := l.RebuildFromDB(ctx, routes); err != nil {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("rebuild should replace projection, got %d entries", len(snap))
	}
	if _, ok := snap["old.host"]; ok {
		t.Error("stale route survived a rebuild")
	}
}

// TestLocal_OrgStatus proves the org-status projection: a blocking status is
// recorded (the edge would block), and "active" CLEARS it (the org is served).
func TestLocal_OrgStatus(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	const org = "11111111-1111-1111-1111-111111111111"

	// Initially nothing is projected → the org is served.
	if _, blocked := l.GetOrgStatus(org); blocked {
		t.Fatal("a fresh org must have no blocking status")
	}
	// Suspension/over_limit blocks at the edge.
	if err := l.SetOrgStatus(ctx, org, "over_limit"); err != nil {
		t.Fatal(err)
	}
	if status, blocked := l.GetOrgStatus(org); !blocked || status != "over_limit" {
		t.Fatalf("got (%q, %v), want (over_limit, true)", status, blocked)
	}
	// "active" CLEARS the flag (re-subscribe restores serving).
	if err := l.SetOrgStatus(ctx, org, "active"); err != nil {
		t.Fatal(err)
	}
	if status, blocked := l.GetOrgStatus(org); blocked {
		t.Fatalf("active must clear the flag, got (%q, %v)", status, blocked)
	}
	// Empty org id is rejected.
	if err := l.SetOrgStatus(ctx, "", "suspended"); err == nil {
		t.Error("empty org id must be rejected")
	}
}

// TestLocal_FileMirror proves the file-backed writer persists + reloads, which is
// what the offline self-host serving shim reads.
func TestLocal_FileMirror(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "projection.json")

	l, err := NewLocalFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.PutRoute(ctx, "x.dropwaycontent.com", validRoute("v9")); err != nil {
		t.Fatal(err)
	}

	// A fresh writer over the same file reloads the route.
	l2, err := NewLocalFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rv, ok := l2.Get("x.dropwaycontent.com"); !ok || rv.VersionID != "v9" {
		t.Fatalf("reload = %+v %v", rv, ok)
	}
}
