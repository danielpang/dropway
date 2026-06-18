package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestHostForSite asserts the ORG-NAMESPACED single-label content host scheme:
// <org>--<app>.dropwaycontent.com (the Worker wildcard matches exactly one
// label, and the `--` separator keeps org+app on that single label).
func TestHostForSite(t *testing.T) {
	if got := HostForSite("acme", "blog"); got != "acme--blog.dropwaycontent.com" {
		t.Errorf("HostForSite = %q", got)
	}
	if got := HostForSite("acme", "my-cool-site"); got != "acme--my-cool-site."+ContentDomain {
		t.Errorf("HostForSite = %q", got)
	}
	// The host is a SINGLE DNS label before the domain (one dot-segment ahead of
	// ContentDomain): the wildcard cert matches exactly one label.
	if got := HostForSite("acme", "blog"); got != "acme--blog."+ContentDomain {
		t.Errorf("HostForSite single-label = %q", got)
	}
	// RouteKey + HostForSite compose to the global KV key.
	if got := RouteKey(HostForSite("acme", "blog")); got != "route:acme--blog.dropwaycontent.com" {
		t.Errorf("RouteKey(HostForSite) = %q", got)
	}
}

// TestOrgStatusKey_And_Constants pins the org-status KV key + the "active" sentinel
// (both must match the serving Worker's constants).
func TestOrgStatusKey_And_Constants(t *testing.T) {
	if got := OrgStatusKey("org-123"); got != "org_status:org-123" {
		t.Errorf("OrgStatusKey = %q", got)
	}
	if OrgStatusActive != "active" {
		t.Errorf("OrgStatusActive = %q, want active", OrgStatusActive)
	}
	if ContentDomain != "dropwaycontent.com" {
		t.Errorf("ContentDomain = %q", ContentDomain)
	}
}

// TestNewLocalFile_LoadError asserts a corrupt mirror file surfaces an error from
// the loader (rather than silently starting empty and masking a data problem).
func TestNewLocalFile_LoadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{ this is not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalFile(path); err == nil {
		t.Fatal("NewLocalFile over a corrupt file should error")
	}
}

// TestNewLocalFile_MissingAndEmpty asserts the loader treats a missing file and an
// empty file as "start empty, no error" (the offline serving shim bootstrap).
func TestNewLocalFile_MissingAndEmpty(t *testing.T) {
	// Missing file.
	missing := filepath.Join(t.TempDir(), "nope.json")
	l, err := NewLocalFile(missing)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(l.Snapshot()) != 0 {
		t.Error("missing file should load empty")
	}

	// Empty file.
	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l2, err := NewLocalFile(empty)
	if err != nil {
		t.Fatalf("empty file should not error: %v", err)
	}
	if len(l2.Snapshot()) != 0 {
		t.Error("empty file should load empty")
	}
}

// TestLocal_FlushError asserts a mirror path under a non-writable parent surfaces
// the flush error from PutRoute (the file-write branch). We point Path at a
// location whose parent is a regular file, so MkdirAll fails.
func TestLocal_FlushError(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Path's parent dir ("afile") is actually a file → os.MkdirAll fails.
	l := &Local{
		Path:      filepath.Join(notADir, "sub", "projection.json"),
		routes:    map[string]RouteValue{},
		revoked:   nil,
		orgStatus: nil,
	}
	err := l.PutRoute(context.Background(), "h.dropwaycontent.com", RouteValue{
		OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion,
	})
	if err == nil {
		t.Fatal("PutRoute should surface the mirror-file write error")
	}
}

// TestLocal_RebuildRejectsInvalid asserts RebuildFromDB validates every supplied
// route and refuses the whole rebuild if any is malformed (a bad row must not
// half-replace the projection).
func TestLocal_RebuildRejectsInvalid(t *testing.T) {
	l := NewLocal()
	_ = l.PutRoute(context.Background(), "keep.dropwaycontent.com", RouteValue{
		OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion,
	})
	bad := map[string]RouteValue{
		"good.host": {OrgID: "o", SiteID: "s", VersionID: "v", AccessMode: AccessPublic, SchemaVersion: SchemaVersion},
		"bad.host":  {OrgID: "o"}, // missing required fields
	}
	if err := l.RebuildFromDB(context.Background(), bad); err == nil {
		t.Fatal("rebuild with a malformed route should error")
	}
	// The pre-existing projection is untouched (rebuild rejected before replacing).
	if _, ok := l.Get("keep.dropwaycontent.com"); !ok {
		t.Error("a rejected rebuild must not wipe the existing projection")
	}
}
