// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/store"
)

func TestCandidateDiffVerifiesHashAndTruncates(t *testing.T) {
	body := "hello world"
	sum := sha256.Sum256([]byte(body))
	sha := hex.EncodeToString(sum[:])
	files := map[string]manifest.Target{"index.html": {SHA256: sha, Size: int64(len(body)), ContentType: "text/html"}}
	digest := manifest.Digest([]manifest.File{{Path: "index.html", SHA256: sha}})
	raw, err := json.Marshal(manifest.Stored{SchemaVersion: manifest.SchemaVersion, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	obj := storage.NewFake()
	if err := obj.PutBlob(context.Background(), "org", sha, strings.NewReader(body), int64(len(body)), "text/html"); err != nil {
		t.Fatal(err)
	}
	if err := obj.PutManifest(context.Background(), "org", "site", "ver", raw); err != nil {
		t.Fatal(err)
	}
	api := &API{Objects: obj}
	got, err := api.verifiedManifest(context.Background(), "org", "site", "ver", digest)
	if err != nil || got["index.html"].SHA256 != sha {
		t.Fatalf("manifest %+v %v", got, err)
	}
	if _, err := api.verifiedManifest(context.Background(), "org", "site", "ver", strings.Repeat("ab", 32)); !errors.Is(err, store.ErrCandidateArtifact) {
		t.Fatalf("mismatch err=%v", err)
	}
	long := strings.Repeat("x", diffTextCap+20)
	longSum := sha256.Sum256([]byte(long))
	longSHA := hex.EncodeToString(longSum[:])
	if err := obj.PutBlob(context.Background(), "org", longSHA, strings.NewReader(long), int64(len(long)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	text, cut, bin, err := api.readVerifiedText(context.Background(), "org", manifest.Target{SHA256: longSHA, Size: int64(len(long))})
	if err != nil || bin || !cut || !strings.Contains(text, "...[truncated]") {
		t.Fatalf("text=%q cut=%v bin=%v err=%v", text, cut, bin, err)
	}
}

func TestBinaryDiffHasNoBody(t *testing.T) {
	api := &API{Objects: storage.NewFake()}
	base := map[string]manifest.Target{}
	proposed := map[string]manifest.Target{"pic.png": {SHA256: "aa", Size: 10, ContentType: "image/png"}}
	files, _, _, err := api.diffFiles(context.Background(), "org", base, proposed)
	if err != nil || len(files) != 1 || !files[0].Binary || files[0].After != "" || files[0].Change != "added" {
		t.Fatalf("%+v %v", files, err)
	}
}
