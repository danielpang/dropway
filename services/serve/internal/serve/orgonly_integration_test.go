// SPDX-License-Identifier: FSL-1.1-Apache-2.0

//go:build integration

// Integration test: ACCESSING an org-only (restricted) site through the real
// `serve` content handler backed by REAL object storage (MinIO), not the in-memory
// fake. It proves the serving half of the org-only contract end to end:
//
//   - no edge cookie                 → 302 to the dashboard /authz exchange;
//   - a valid org_only edge token    → 200 + the published bytes streamed from
//                                       MinIO, with private/no-store (never cached);
//   - an EXPIRED token               → fail closed → 302;
//   - a token minted for ANOTHER host → fail closed → 302 (aud binding).
//
// The token here is minted with the SAME internal/edgetoken.Signer the API uses
// after it authorizes a viewer — so it is byte-identical to a real issued token.
// WHO is allowed to obtain one (the live org-membership re-check) is covered by the
// API-side mint test (services/api/internal/integration, AuthorizeMint member vs
// non-member → ErrNotOrgMember). Together they cover the whole org_only loop.
//
// Run with:
//
//	go test -tags integration ./services/serve/internal/serve/...
//
// Requires Docker (a MinIO container is started + torn down via t.Cleanup).
package serve_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/edgetoken"
	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/serve"
)

const (
	serveITMinioName = "dropway-serve-it-minio"
	serveITMinioPort = "59010" // distinct from the api IT MinIO (59000) so both can run
	serveITMinioUser = "dropway"
	serveITMinioPass = "dropway-dev-secret"
	serveITBucket    = "dropway-blobs"
)

func TestIntegration_OrgOnly_ServeFromObjectStore(t *testing.T) {
	ctx := context.Background()

	s3 := startServeITMinio(t, ctx)
	if err := s3.EnsureBucket(ctx); err != nil {
		t.Fatalf("ensure bucket: %v", err)
	}

	// Publish one org_only page into real object storage: the content-addressed blob
	// + a real deploy manifest mapping "index.html" → that blob.
	const page = "<h1>members only</h1>"
	stageServeITVersion(t, ctx, s3, "index.html", page, "text/html; charset=utf-8")

	// The real serve handler over the real S3 store, with an org_only route and a
	// verifier trusting our signer (empty revocation list → nothing revoked).
	signer := testSigner(t)
	orgOnly := serve.Route{
		OrgID: testOrgID, SiteID: testSiteID, VersionID: testVersionID,
		AccessMode: projection.AccessOrgOnly,
	}
	h := newHandler(
		fakeResolver{map[string]serve.Route{testHost: orgOnly}},
		s3, signer,
		fakeRevoked{minIATs: map[string]int64{}, errOn: map[string]bool{}},
	)

	// 1) No edge cookie → 302 to the dashboard /authz exchange (no content leak).
	rec := doRequest(h, http.MethodGet, testHost, "/", nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("no-token: status = %d, want 302; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/authz") || !strings.Contains(loc, "host="+testHost) {
		t.Fatalf("no-token: Location = %q, want the /authz exchange with host=%s", loc, testHost)
	}
	if strings.Contains(rec.Body.String(), "members only") {
		t.Fatal("no-token response leaked content")
	}

	// 2) A valid org_only token → 200 + the bytes from MinIO + private/no-store.
	tok := mint(t, signer, testHost, testSiteID, edgetoken.ModeOrgOnly, time.Minute)
	rec = doRequest(h, http.MethodGet, testHost, "/", nil, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid-token: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != page {
		t.Fatalf("valid-token: body = %q, want %q (served from MinIO)", got, page)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "private") || !strings.Contains(cc, "no-store") {
		t.Fatalf("valid-token: Cache-Control = %q, want private/no-store", cc)
	}

	// 3) An EXPIRED token → fail closed → 302.
	expired := mint(t, signer, testHost, testSiteID, edgetoken.ModeOrgOnly, -time.Minute)
	if rec = doRequest(h, http.MethodGet, testHost, "/", nil, expired); rec.Code != http.StatusFound {
		t.Fatalf("expired-token: status = %d, want 302", rec.Code)
	}

	// 4) A token minted for ANOTHER host → aud mismatch → fail closed → 302.
	wrongHost := mint(t, signer, otherHost, testSiteID, edgetoken.ModeOrgOnly, time.Minute)
	if rec = doRequest(h, http.MethodGet, testHost, "/", nil, wrongHost); rec.Code != http.StatusFound {
		t.Fatalf("wrong-host token: status = %d, want 302 (aud binding)", rec.Code)
	}

	t.Log("PASS: org_only serve — no cookie → 302, valid token serves bytes from MinIO (private/no-store), expired → 302, wrong-host token → 302")
}

// stageServeITVersion uploads one file's bytes to real object storage and writes a
// real deploy manifest (schema_version 1, sha-keyed) so the serve handler resolves
// "/" → the blob, exactly as a published deploy would.
func stageServeITVersion(t *testing.T, ctx context.Context, s3 *storage.S3Store, path, content, ctype string) {
	t.Helper()
	sha := sha256Hex([]byte(content))
	if err := s3.PutBlob(ctx, testOrgID, sha, strings.NewReader(content), int64(len(content)), ctype); err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	mf, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"files": map[string]any{
			path: map[string]any{"sha256": sha, "content_type": ctype, "size": len(content)},
		},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := s3.PutManifest(ctx, testOrgID, testSiteID, testVersionID, mf); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
}

// startServeITMinio starts a throwaway MinIO container and returns an S3Store
// pointed at it. Torn down via t.Cleanup.
func startServeITMinio(t *testing.T, ctx context.Context) *storage.S3Store {
	t.Helper()
	_ = exec.Command("docker", "rm", "-f", serveITMinioName).Run()
	out, err := exec.Command("docker", "run", "-d", "--name", serveITMinioName,
		"-e", "MINIO_ROOT_USER="+serveITMinioUser, "-e", "MINIO_ROOT_PASSWORD="+serveITMinioPass,
		"-p", serveITMinioPort+":9000", "minio/minio:latest", "server", "/data").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run minio: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", serveITMinioName).Run() })

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+serveITMinioPort, time.Second); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(time.Second)
	}
	time.Sleep(time.Second) // small grace after the port opens

	s3, err := storage.NewS3Store(ctx, storage.S3Config{
		Bucket:          serveITBucket,
		Region:          "us-east-1",
		Endpoint:        "http://127.0.0.1:" + serveITMinioPort,
		AccessKeyID:     serveITMinioUser,
		SecretAccessKey: serveITMinioPass,
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("minio store: %v", err)
	}
	return s3
}
