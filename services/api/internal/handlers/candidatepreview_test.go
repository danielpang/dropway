// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/auth"
	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/go-chi/chi/v5"
)

func TestCandidateVisualPreviewBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, class      string
		allowed, corrupt bool
		want             int
	}{
		{"full private preview", auth.ClassHumanSession, true, false, 200},
		{"detached site", auth.ClassHumanSession, false, false, 404},
		{"unknown identity", "unknown", true, false, 403},
		{"corrupt artifact", auth.ClassHumanSession, true, true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := storage.NewFake()
			body := "<html><style>h1{color:red}</style><h1>" + strings.Repeat("content", 1000) + "</h1></html>"
			hash := sha256.Sum256([]byte(body))
			sha := hex.EncodeToString(hash[:])
			digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: sha}})
			raw, _ := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: map[string]manifest.Target{"index.html": {SHA256: sha, Size: int64(len(body)), ContentType: "text/html"}}})
			for _, id := range []string{"base", "proposed"} {
				if err := obj.PutManifest(context.Background(), "org_1", "site", id, raw); err != nil {
					t.Fatal(err)
				}
			}
			stored := body
			if tc.corrupt {
				stored = "corrupt"
			}
			if err := obj.PutBlob(context.Background(), "org_1", sha, strings.NewReader(stored), int64(len(stored)), "text/html"); err != nil {
				t.Fatal(err)
			}
			fs := newFakeStore()
			fs.p2().members["user_1"] = store.RoleMember
			a := &API{Store: &applyStore{SiteStore: fs, candidate: store.Candidate{ID: "candidate", SiteID: "site", BaseContentHash: digest, ContentHash: digest}, base: store.SiteVersion{ID: "base", ContentHash: digest}, proposed: store.SiteVersion{ID: "proposed", ContentHash: digest}}, Rooms: historicalRooms{allowed: tc.allowed}, Objects: obj}
			r := chi.NewRouter()
			r.Use(middleware.Auth(classedVerifier{claims: claims("user_1", "org_1", "member"), class: tc.class}))
			r.Get("/rooms/{roomID}/candidates/{candidateID}/preview", a.CandidatePreview)
			req := httptest.NewRequest("GET", "/rooms/room/candidates/candidate/preview", nil)
			req.Header.Set("Authorization", "Bearer test")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.want == 200 {
				if w.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal("preview must not be cached")
				}
				var payload struct{ Before, After []previewFile }
				if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				decoded, _ := base64.StdEncoding.DecodeString(payload.After[0].Data)
				if string(decoded) != body {
					t.Fatal("preview was truncated")
				}
			}
		})
	}
}
