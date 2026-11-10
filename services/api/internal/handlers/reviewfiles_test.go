// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http/httptest"
	"strings"
	"testing"
)

type historicalStore struct {
	SiteStore
	version store.SiteVersion
}

func (s historicalStore) GetSiteVersion(context.Context, store.Tenant, string) (store.SiteVersion, error) {
	return s.version, nil
}

type historicalRooms struct {
	ProjectRoomStore
	allowed bool
}

func (s historicalRooms) RoomAccess(context.Context, store.Tenant, string, string) (string, projectroom.SiteRights, error) {
	if !s.allowed {
		return "", projectroom.SiteRights{}, store.ErrNotFound
	}
	return "reviewer", projectroom.SiteRights{CanRead: true}, nil
}

func TestHistoricalReviewFileBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, class, path, versionSite string
		allowed, corrupt               bool
		want                           int
	}{
		{"human", auth.ClassHumanSession, "index.html", "site", true, false, 200},
		{"mcp", auth.ClassMCPOAuth, "index.html", "site", true, false, 200},
		{"unknown", "unknown", "index.html", "site", true, false, 403},
		{"detached", auth.ClassHumanSession, "index.html", "site", false, false, 404},
		{"wrongsite", auth.ClassHumanSession, "index.html", "other", true, false, 404},
		{"traversal", auth.ClassHumanSession, "../index.html", "site", true, false, 400},
		{"missing", auth.ClassHumanSession, "absent.html", "site", true, false, 404},
		{"corrupt", auth.ClassHumanSession, "index.html", "site", true, true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			body := []byte(`<html><script>hostile()</script><h1>historical</h1></html>`)
			hash := sha256.Sum256(body)
			sha := hex.EncodeToString(hash[:])
			digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: sha}})
			obj := storage.NewFake()
			raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{"index.html": {SHA256: sha, Size: int64(len(body)), ContentType: "text/html"}}})
			if err := obj.PutManifest(ctx, "org_1", "site", "old-version", raw); err != nil {
				t.Fatal(err)
			}
			if tc.corrupt {
				body = []byte("corrupt")
			}
			if err := obj.PutBlob(ctx, "org_1", sha, strings.NewReader(string(body)), int64(len(body)), "text/html"); err != nil {
				t.Fatal(err)
			}
			fs := newFakeStore()
			fs.p2().members["user_1"] = store.RoleMember
			a := &API{Store: historicalStore{SiteStore: fs, version: store.SiteVersion{ID: "old-version", OrgID: "org_1", SiteID: tc.versionSite, ContentHash: digest}}, Rooms: historicalRooms{allowed: tc.allowed}, Objects: obj}
			r := chi.NewRouter()
			r.Use(middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: tc.class}))
			r.Get("/rooms/{roomID}/sites/{siteID}/versions/{versionID}/files", a.ReadReviewVersionFile)
			req := httptest.NewRequest("GET", "/rooms/room/sites/site/versions/old-version/files?path="+tc.path, nil)
			req.Header.Set("Authorization", "Bearer test")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status%d want%d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == 200 {
				if strings.Contains(rec.Body.String(), "<script>") {
					t.Fatal("executable raw markup in response")
				}
				if rec.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal("private file may be cached")
				}
				if !strings.Contains(rec.Body.String(), `"version_id":"old-version"`) {
					t.Fatal("historical version pin lost")
				}
			}
		})
	}
}
