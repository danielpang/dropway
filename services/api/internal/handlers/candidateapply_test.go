// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/projectroom"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

func TestCandidateApplyVerifiesFilesOutsidePreview(t *testing.T) {
	ctx := context.Background()
	obj := storage.NewFake()
	api := &API{Objects: obj}
	files := map[string]manifest.Target{}
	digestFiles := []manifest.File{}
	// The last path would be omitted by the diff cap; its binary body is larger
	// than the preview reader. Application must verify it anyway.
	for i := 0; i < diffFileCap+1; i++ {
		body := "ok"
		if i == diffFileCap {
			body = strings.Repeat("\x00", diffReadCap+1)
		}
		sum := sha256.Sum256([]byte(body))
		sha := hex.EncodeToString(sum[:])
		path := fmt.Sprintf("%03d.bin", i)
		files[path] = manifest.Target{SHA256: sha, Size: int64(len(body)), ContentType: "application/octet-stream"}
		digestFiles = append(digestFiles, manifest.File{Path: path, SHA256: sha})
		if err := obj.PutBlob(ctx, "org", sha, strings.NewReader(body), int64(len(body)), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: files})
	if err := obj.PutManifest(ctx, "org", "site", "ver", raw); err != nil {
		t.Fatal(err)
	}
	ver := store.SiteVersion{ID: "ver", ContentHash: manifest.Digest(digestFiles)}
	if err := api.verifyCandidateArtifact(ctx, "org", "site", ver); err != nil {
		t.Fatal(err)
	}
	target := files[fmt.Sprintf("%03d.bin", diffFileCap)]
	if err := obj.PutBlob(ctx, "org", target.SHA256, strings.NewReader("corrupt"), 7, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err := api.verifyCandidateArtifact(ctx, "org", "site", ver); !errors.Is(err, store.ErrCandidateArtifact) {
		t.Fatalf("omitted/binary corruption: %v", err)
	}
}

type applyStore struct {
	SiteStore
	candidate      store.Candidate
	base, proposed store.SiteVersion
}

func (s *applyStore) CandidateVersions(context.Context, store.Tenant, string, string) (store.Candidate, store.SiteVersion, store.SiteVersion, error) {
	return s.candidate, s.base, s.proposed, nil
}

type applyRooms struct {
	ProjectRoomStore
	called bool
}

func (s *applyRooms) ApplyCandidate(ctx context.Context, t store.Tenant, room string, req projectroom.ApprovalRequest) (store.CandidateApplication, error) {
	s.called = true
	if req.VerifiedVersionID != "proposed" || !store.HumanSessionFromContext(ctx) {
		return store.CandidateApplication{}, errors.New("unverified application")
	}
	return store.CandidateApplication{VersionID: "proposed", Changed: false, Publish: store.PublishResult{Host: "org--site.dropwaycontent.com", Routes: []store.RouteUpdate{{Host: "org--site.dropwaycontent.com", Route: projection.RouteValue{OrgID: "org_1", SiteID: "site", VersionID: "proposed", AccessMode: projection.AccessOrgOnly, SchemaVersion: projection.SchemaVersion}}, {Host: "custom.example.com", Route: projection.RouteValue{OrgID: "org_1", SiteID: "site", VersionID: "proposed", AccessMode: projection.AccessOrgOnly, SchemaVersion: projection.SchemaVersion}}}}}, nil
}

func (s *applyRooms) FlushCandidateProjection(ctx context.Context, t store.Tenant, candidateID string, w ProjectionWriter) error {
	for _, host := range []string{"org--site.dropwaycontent.com", "custom.example.com"} {
		if err := w.PutRoute(ctx, host, projection.RouteValue{OrgID: t.OrgID, SiteID: "site", VersionID: "proposed", AccessMode: projection.AccessOrgOnly, SchemaVersion: projection.SchemaVersion}); err != nil {
			return err
		}
	}
	return nil
}

func TestCandidateApplyHTTPBoundaryAndProjection(t *testing.T) {
	for _, class := range []string{auth.ClassHumanSession, auth.ClassMCPOAuth, "unknown", ""} {
		t.Run(class, func(t *testing.T) {
			obj := storage.NewFake()
			digest := manifest.Digest(nil)
			raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{}})
			for _, id := range []string{"base", "proposed"} {
				if err := obj.PutManifest(context.Background(), "org_1", "site", id, raw); err != nil {
					t.Fatal(err)
				}
			}
			fs := newFakeStore()
			fs.p2().members["user_1"] = store.RoleMember
			st := &applyStore{SiteStore: fs, candidate: store.Candidate{ID: "cand", SiteID: "site", State: "approved", ContentHash: digest, BaseVersionID: "base", BaseContentHash: digest}, base: store.SiteVersion{ID: "base", ContentHash: digest}, proposed: store.SiteVersion{ID: "proposed", ContentHash: digest}}
			rooms := &applyRooms{}
			proj := projection.NewLocal()
			api := NewFull(quota.Unlimited{}, st, obj, proj)
			api.Rooms = rooms
			r := chi.NewRouter()
			r.Use(middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: class}))
			r.Post("/rooms/{roomID}/candidates/{candidateID}/apply", api.ApplyCandidate)
			if class == auth.ClassHumanSession {
				for _, extra := range []string{`,"candidate_id":"another"`, `,"verified_version_id":"attacker"`} {
					bad := httptest.NewRequest(http.MethodPost, "/rooms/room/candidates/cand/apply", strings.NewReader(fmt.Sprintf(`{"content_hash":%q,"base_version_id":"base","base_content_hash":%q%s}`, digest, digest, extra)))
					bad.Header.Set("Authorization", "Bearer test")
					rr := httptest.NewRecorder()
					r.ServeHTTP(rr, bad)
					if rr.Code != http.StatusBadRequest || rooms.called {
						t.Fatalf("bad identity reached apply: %d %s", rr.Code, rr.Body.String())
					}
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/rooms/room/candidates/cand/apply", strings.NewReader(fmt.Sprintf(`{"content_hash":%q,"base_version_id":"base","base_content_hash":%q}`, digest, digest)))
			req.Header.Set("Authorization", "Bearer test")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if class != auth.ClassHumanSession {
				if rec.Code != http.StatusForbidden || rooms.called {
					t.Fatalf("non-human status=%d called=%v", rec.Code, rooms.called)
				}
				return
			}
			if rec.Code != http.StatusOK || !rooms.called {
				t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, host := range []string{"org--site.dropwaycontent.com", "custom.example.com"} {
				route, ok := proj.Get(host)
				if !ok || route.VersionID != "proposed" {
					t.Fatalf("route %s %+v %v", host, route, ok)
				}
			}
		})
	}
}
