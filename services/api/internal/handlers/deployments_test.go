package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/middleware"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// stringReader is a tiny helper so tests can build JSON bodies inline.
func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

// routeValueFor builds a RouteValue (used by the fake store's Publish).
func routeValueFor(orgID, siteID, versionID, mode string) projection.RouteValue {
	return projection.RouteValue{
		OrgID: orgID, SiteID: siteID, VersionID: versionID,
		AccessMode: mode, SchemaVersion: projection.SchemaVersion,
	}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// routerFor builds a chi router mirroring services/api/internal/router but local
// to this test, so the deploy-flow tests exercise URL routing (chi.URLParam) the
// production way WITHOUT importing the router package (which would create an
// import cycle: router → handlers). It wires the same Auth + ensure-org chain.
func routerFor(a *API, orgID, userID string) http.Handler {
	v := fakeVerifier{claims: claims(userID, orgID, "owner")}
	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Auth(v))
		r.Use(a.EnsureOrgProvisioned)
		r.Route("/sites", func(r chi.Router) {
			r.Post("/", a.CreateSite)
			r.Get("/", a.ListSites)
			r.Get("/{id}", a.GetSite)
			r.Post("/{id}/deployments/prepare", a.PrepareDeployment)
			r.Post("/{id}/deployments", a.FinalizeDeployment)
			r.Post("/{id}/publish", a.Publish)
		})
	})
	return r
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestDeployFlow_PrepareFinalizePublish exercises the full Phase-1 loop against
// the in-memory fakes: create site → prepare (all blobs missing) → upload blobs
// to the fake store → finalize (server-verifies + writes manifest + version) →
// publish (flips pointer + writes the KV projection). Asserts the projection
// RouteValue is exactly what the contract expects.
func TestDeployFlow_PrepareFinalizePublish(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	proj := projection.NewLocal()
	a := NewFull(quota.Unlimited{}, fs, obj, proj)
	h := routerFor(a, "org_1", "user_1")

	// 1. Create the site.
	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"my-docs","access_mode":"public"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rr.Code, rr.Body.String())
	}
	var site siteResponse
	mustJSON(t, rr, &site)
	siteID := site.ID

	// Two files; index.html + app.js.
	idx := []byte("<h1>hi</h1>")
	js := []byte("console.log(1)")
	idxSHA, jsSHA := sha(idx), sha(js)
	files := []ManifestFile{
		{Path: "index.html", SHA256: idxSHA, Size: int64(len(idx)), ContentType: "text/html"},
		{Path: "app.js", SHA256: jsSHA, Size: int64(len(js)), ContentType: "text/javascript"},
	}
	mf, _ := json.Marshal(files)

	// 2. Prepare: nothing uploaded yet → both blobs missing.
	rr = do(t, h, http.MethodPost, "/v1/sites/"+siteID+"/deployments/prepare",
		`{"manifest":`+string(mf)+`}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("prepare: %d %s", rr.Code, rr.Body.String())
	}
	var prep prepareResponse
	mustJSON(t, rr, &prep)
	if len(prep.Missing) != 2 || len(prep.Uploads) != 2 {
		t.Fatalf("prepare missing/uploads = %+v", prep)
	}

	// 3. "Upload" the blobs (the real path PUTs to the presigned URL; the Fake
	// stages bytes directly under the same per-org key).
	must(t, obj.PutBlobBytes(context.Background(), "org_1", idxSHA, idx))
	must(t, obj.PutBlobBytes(context.Background(), "org_1", jsSHA, js))

	// 4. Finalize: server-verifies blobs, writes manifest + immutable version. The
	// digest is the server-derived whole-deploy content address (shared manifest).
	digest := manifest.Digest([]manifest.File{
		{Path: "index.html", SHA256: idxSHA},
		{Path: "app.js", SHA256: jsSHA},
	})
	rr = do(t, h, http.MethodPost, "/v1/sites/"+siteID+"/deployments",
		`{"digest":"`+digest+`","manifest":`+string(mf)+`}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("finalize: %d %s", rr.Code, rr.Body.String())
	}
	var fin finalizeResponse
	mustJSON(t, rr, &fin)
	if fin.VersionID == "" {
		t.Fatal("finalize returned no version_id")
	}

	// The manifest object was written under manifests/<org>/<site>/<ver>.json.
	manifestBytes, err := obj.GetManifest(context.Background(), "org_1", siteID, fin.VersionID)
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	// C1: the manifest MUST be stamped with the MANIFEST schema version (the value
	// the serving Worker pins, manifest.SchemaVersion), NOT projection.SchemaVersion
	// (the KV route contract). Sourcing it from the route contract made every deploy
	// 404 once the route schema bumped to v2. Pin it here so the two can never be
	// conflated again, and assert it equals 1 (the Worker's SUPPORTED_MANIFEST_SCHEMA_VERSION).
	var writtenManifest struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(manifestBytes, &writtenManifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if writtenManifest.SchemaVersion != manifest.SchemaVersion {
		t.Errorf("manifest schema_version = %d, want manifest.SchemaVersion (%d)",
			writtenManifest.SchemaVersion, manifest.SchemaVersion)
	}
	if manifest.SchemaVersion != 1 {
		t.Errorf("manifest.SchemaVersion = %d, but the serving Worker pins SUPPORTED_MANIFEST_SCHEMA_VERSION=1; "+
			"bump both in lock-step or new deploys will 404", manifest.SchemaVersion)
	}

	// 5. Publish: flips the pointer and writes the KV projection.
	rr = do(t, h, http.MethodPost, "/v1/sites/"+siteID+"/publish",
		`{"version_id":"`+fin.VersionID+`"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body.String())
	}
	var pub publishResponse
	mustJSON(t, rr, &pub)
	if pub.LiveURL != "https://org-my-docs.dropwaycontent.com" {
		t.Errorf("live_url = %q", pub.LiveURL)
	}

	// 6. Assert the projection RouteValue matches the contract.
	rv, ok := proj.Get("org-my-docs.dropwaycontent.com")
	if !ok {
		t.Fatal("no route projected for docs host")
	}
	if rv.OrgID != "org_1" || rv.SiteID != siteID || rv.VersionID != fin.VersionID {
		t.Errorf("route value = %+v", rv)
	}
	if rv.AccessMode != projection.AccessPublic || rv.SchemaVersion != projection.SchemaVersion {
		t.Errorf("route mode/version = %+v", rv)
	}
}

// TestFinalize_RejectsTamperedBlob proves the server re-derives the stored
// bytes' hash and rejects content that doesn't match the claimed sha256.
func TestFinalize_RejectsTamperedBlob(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, projection.NewLocal())
	h := routerFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"site"}`)
	var site siteResponse
	mustJSON(t, rr, &site)

	claimed := sha([]byte("the real bytes"))
	// Stage DIFFERENT bytes under the claimed key (a lying client). The tampered
	// bytes are 8 long so the size matches the manifest claim — this isolates the
	// failure to the hash mismatch, not the size guard.
	tampered := []byte("tampered")
	must(t, obj.PutBlobBytes(context.Background(), "org_1", claimed, tampered))

	mf := `[{"path":"index.html","sha256":"` + claimed + `","size":` + itoa(len(tampered)) + `}]`
	// Send the CORRECT server-derived digest so finalize passes the digest check
	// and actually reaches blob verification — which must reject the tampered blob.
	digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: claimed}})
	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/deployments",
		`{"digest":"`+digest+`","manifest":`+mf+`}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("finalize tampered: %d %s, want 400", rr.Code, rr.Body.String())
	}
}

// TestFinalize_RejectsTamperedDigest proves the server recomputes the whole-
// deploy digest and rejects a client digest that doesn't match the manifest, so
// the content_hash idempotency key can't be forged (FIX 2).
func TestFinalize_RejectsTamperedDigest(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, projection.NewLocal())
	h := routerFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"site"}`)
	var site siteResponse
	mustJSON(t, rr, &site)

	body := []byte("real bytes")
	bs := sha(body)
	must(t, obj.PutBlobBytes(context.Background(), "org_1", bs, body))

	mf := `[{"path":"index.html","sha256":"` + bs + `","size":` + itoa(len(body)) + `}]`
	// A valid-looking but WRONG digest (any other 64-hex value).
	forged := sha([]byte("a different deploy entirely"))
	if forged == manifest.Digest([]manifest.File{{Path: "index.html", SHA256: bs}}) {
		t.Fatal("test setup: forged digest accidentally matches the real one")
	}
	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/deployments",
		`{"digest":"`+forged+`","manifest":`+mf+`}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("finalize forged digest: %d %s, want 400", rr.Code, rr.Body.String())
	}

	// And the idempotency key is server-derived: the SAME manifest with the CORRECT
	// digest succeeds, and the stored content_hash is the server digest (not any
	// client-controlled value).
	correct := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: bs}})
	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/deployments",
		`{"digest":"`+correct+`","manifest":`+mf+`}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("finalize correct digest: %d %s, want 201", rr.Code, rr.Body.String())
	}
	var fin finalizeResponse
	mustJSON(t, rr, &fin)
	v, ok := fs.versions[fin.VersionID]
	if !ok {
		t.Fatalf("version %s not recorded", fin.VersionID)
	}
	if v.ContentHash != correct {
		t.Errorf("content_hash = %q, want server-derived digest %q", v.ContentHash, correct)
	}
	// And size_bytes is the server-observed blob length, not any client claim.
	if v.SizeBytes != int64(len(body)) {
		t.Errorf("size_bytes = %d, want server-observed %d", v.SizeBytes, len(body))
	}
}

// TestFinalize_RejectsSizeMismatch proves the server uses the stored object's
// length and rejects a client-claimed size that disagrees (FIX 3).
func TestFinalize_RejectsSizeMismatch(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	a := NewFull(quota.Unlimited{}, fs, obj, projection.NewLocal())
	h := routerFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"site"}`)
	var site siteResponse
	mustJSON(t, rr, &site)

	body := []byte("ten bytes!") // 10 bytes
	bs := sha(body)
	must(t, obj.PutBlobBytes(context.Background(), "org_1", bs, body))

	// Manifest lies: claims size 999 for a 10-byte object.
	mf := `[{"path":"index.html","sha256":"` + bs + `","size":999}]`
	digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: bs}})
	rr = do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/deployments",
		`{"digest":"`+digest+`","manifest":`+mf+`}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("finalize size mismatch: %d %s, want 400", rr.Code, rr.Body.String())
	}
}

// TestRollback_PublishOlderVersion proves publishing an older version flips the
// pointer back (rollback == publish an earlier version_id).
func TestRollback_PublishOlderVersion(t *testing.T) {
	fs := newFakeStore()
	fs.p2().members["user_1"] = "member"
	obj := storage.NewFake()
	proj := projection.NewLocal()
	a := NewFull(quota.Unlimited{}, fs, obj, proj)
	h := routerFor(a, "org_1", "user_1")

	rr := do(t, h, http.MethodPost, "/v1/sites", `{"slug":"my-app","access_mode":"public"}`)
	var site siteResponse
	mustJSON(t, rr, &site)

	v1 := stageVersion(t, h, obj, site.ID, "v1-content")
	v2 := stageVersion(t, h, obj, site.ID, "v2-content")

	// Publish v2, then roll back to v1.
	if rr := do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v2+`"}`); rr.Code != http.StatusOK {
		t.Fatalf("publish v2: %d %s", rr.Code, rr.Body.String())
	}
	if rv, _ := proj.Get("org-my-app.dropwaycontent.com"); rv.VersionID != v2 {
		t.Fatalf("expected v2 live, got %q", rv.VersionID)
	}
	if rr := do(t, h, http.MethodPost, "/v1/sites/"+site.ID+"/publish", `{"version_id":"`+v1+`"}`); rr.Code != http.StatusOK {
		t.Fatalf("rollback to v1: %d %s", rr.Code, rr.Body.String())
	}
	if rv, _ := proj.Get("org-my-app.dropwaycontent.com"); rv.VersionID != v1 {
		t.Fatalf("rollback failed: live = %q, want %q", rv.VersionID, v1)
	}
}

// stageVersion uploads a single-file deploy and finalizes it, returning the id.
func stageVersion(t *testing.T, h http.Handler, obj *storage.Fake, siteID, content string) string {
	t.Helper()
	b := []byte(content)
	bs := sha(b)
	must(t, obj.PutBlobBytes(context.Background(), "org_1", bs, b))
	mf := `[{"path":"index.html","sha256":"` + bs + `","size":` + itoa(len(b)) + `}]`
	// Server-derived digest; unique per content → distinct version.
	digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: bs}})
	rr := do(t, h, http.MethodPost, "/v1/sites/"+siteID+"/deployments",
		`{"digest":"`+digest+`","manifest":`+mf+`}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("stageVersion finalize: %d %s", rr.Code, rr.Body.String())
	}
	var fin finalizeResponse
	mustJSON(t, rr, &fin)
	return fin.VersionID
}

func mustJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rr.Body.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Ensure the store sentinel type is referenced (keeps the import honest as the
// flow tests use ErrNotFound semantics indirectly via the fake store).
var _ = store.ErrNotFound

// TestDeployWarnings_RootIndex covers the only deploy advisory today: a missing
// root index.html (the Worker resolves "/" to exactly that key, so its absence
// makes the root show a file listing instead of a page). It is a WARNING, not an
// error — the deploy still succeeds.
func TestDeployWarnings_RootIndex(t *testing.T) {
	// No root index.html → one warning. A NESTED index.html does not count: the
	// Worker only resolves "/" to a top-level "index.html".
	got := deployWarnings(map[string]manifestTarget{
		"about.html":      {},
		"assets/app.js":   {},
		"blog/index.html": {},
	})
	if len(got) != 1 || !strings.Contains(got[0], "index.html") {
		t.Fatalf("expected a missing-root-index warning, got %v", got)
	}

	// Root index.html present → no warnings.
	if got := deployWarnings(map[string]manifestTarget{
		"index.html":    {},
		"assets/app.js": {},
	}); len(got) != 0 {
		t.Fatalf("expected no warnings with a root index.html, got %v", got)
	}

	// Empty deploy → still warns (no root index).
	if got := deployWarnings(map[string]manifestTarget{}); len(got) != 1 {
		t.Fatalf("expected a warning for an empty deploy, got %v", got)
	}
}
