package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"testing"
	"time"
)

// errReader is an io.Reader that always fails — used to exercise PutBlob's
// ReadAll error branch.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// TestFake_PresignDefaultBase asserts that with no PresignBase set, a stable
// opaque https URL is returned (the default-host branch).
func TestFake_PresignDefaultBase(t *testing.T) {
	f := NewFake()
	url, err := f.PresignPut(context.Background(), "org-a", "deadbeef", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://fake.local/blobs/org-a/deadbeef" {
		t.Errorf("default presign url = %q", url)
	}
}

// TestFake_PresignBadBase asserts a malformed PresignBase surfaces the url.Parse
// error rather than returning a bogus URL.
func TestFake_PresignBadBase(t *testing.T) {
	f := NewFake()
	f.PresignBase = "://not a url" // missing scheme → url.Parse error
	if _, err := f.PresignPut(context.Background(), "o", "s", time.Minute); err == nil {
		t.Fatal("expected an error for a malformed PresignBase")
	}
}

// TestFake_PutBlobReadError asserts a failing reader propagates from PutBlob (so
// a staging failure isn't silently swallowed).
func TestFake_PutBlobReadError(t *testing.T) {
	f := NewFake()
	if err := f.PutBlob(context.Background(), "o", "s", errReader{}, 0, ""); err == nil {
		t.Fatal("expected the reader error to propagate")
	}
	// The failed put must not have staged anything.
	if ok, _, _ := f.HeadBlob(context.Background(), "o", "s"); ok {
		t.Error("a failed PutBlob should not stage a blob")
	}
}

// TestFake_ListBlobInfos_AgeAndIsolation asserts ListBlobInfos surfaces the
// per-blob LastModified (the GC age-guard input) and only enumerates the queried
// org's prefix (no cross-tenant leakage into the GC's "what exists" input).
func TestFake_ListBlobInfos_AgeAndIsolation(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Two blobs for org-a (one old orphan, one fresh) and one for org-b.
	if err := f.PutBlobBytesAt(ctx, "org-a", "sha-old", []byte("x"), old); err != nil {
		t.Fatal(err)
	}
	if err := f.PutBlobBytesAt(ctx, "org-a", "sha-fresh", []byte("y"), fresh); err != nil {
		t.Fatal(err)
	}
	if err := f.PutBlobBytesAt(ctx, "org-b", "sha-other", []byte("z"), fresh); err != nil {
		t.Fatal(err)
	}

	infos, err := f.ListBlobInfos(ctx, "org-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("org-a should have exactly 2 blobs (no org-b leakage), got %d: %+v", len(infos), infos)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].SHA < infos[j].SHA })
	// SHA is the last key segment (after blobs/<org>/), and LastModified is the
	// GC age-guard input — both must round-trip.
	byID := map[string]BlobInfo{infos[0].SHA: infos[0], infos[1].SHA: infos[1]}
	if got := byID["sha-old"]; !got.LastModified.Equal(old) {
		t.Errorf("sha-old LastModified = %v, want %v", got.LastModified, old)
	}
	if got := byID["sha-fresh"]; !got.LastModified.Equal(fresh) {
		t.Errorf("sha-fresh LastModified = %v, want %v", got.LastModified, fresh)
	}

	// An org with nothing stored lists empty (not an error).
	empty, err := f.ListBlobInfos(ctx, "org-empty")
	if err != nil || len(empty) != 0 {
		t.Errorf("empty org list = %v %v", empty, err)
	}
}

// TestFake_NowOverrideStampsBlob asserts the Now clock override stamps PutBlob's
// last-modified time, so a test can stage an "old orphan" without sleeping.
func TestFake_NowOverrideStampsBlob(t *testing.T) {
	ctx := context.Background()
	fixed := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	f := NewFake()
	f.Now = func() time.Time { return fixed }

	if err := f.PutBlobBytes(ctx, "o", "sha", []byte("data")); err != nil {
		t.Fatal(err)
	}
	infos, err := f.ListBlobInfos(ctx, "o")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || !infos[0].LastModified.Equal(fixed) {
		t.Fatalf("blob should carry the Now-override timestamp, got %+v", infos)
	}
}

// TestFake_DeleteBlob_Idempotent asserts DeleteBlob removes the blob, drops its
// age record, and is a no-op (not an error) on an absent key — so a GC re-run is
// safe.
func TestFake_DeleteBlob_Idempotent(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	if err := f.PutBlobBytes(ctx, "o", "sha", []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteBlob(ctx, "o", "sha"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := f.HeadBlob(ctx, "o", "sha"); ok {
		t.Error("blob should be gone after delete")
	}
	if infos, _ := f.ListBlobInfos(ctx, "o"); len(infos) != 0 {
		t.Errorf("delete should drop the age record too, got %+v", infos)
	}
	// Deleting an absent blob is idempotent (no error).
	if err := f.DeleteBlob(ctx, "o", "sha"); err != nil {
		t.Errorf("deleting an absent blob should be a no-op, got %v", err)
	}
}

// TestFake_GetBlob_ReturnsCopy asserts GetBlob hands back an independent copy so a
// caller mutating the returned bytes can't corrupt the staged blob.
func TestFake_GetBlob_ReturnsCopy(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	orig := []byte("immutable")
	if err := f.PutBlobBytes(ctx, "o", "sha", orig); err != nil {
		t.Fatal(err)
	}
	rc, err := f.GetBlob(ctx, "o", "sha")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	got[0] = 'X' // mutate the returned slice

	rc2, _ := f.GetBlob(ctx, "o", "sha")
	got2, _ := io.ReadAll(rc2)
	rc2.Close()
	if !bytes.Equal(got2, orig) {
		t.Errorf("stored blob was corrupted by a caller mutation: %q", got2)
	}
}

// TestFake_Manifest_ReturnsCopy asserts the manifest read is also defensively
// copied (mutating the returned bytes must not affect the stored manifest).
func TestFake_Manifest_ReturnsCopy(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	body := []byte(`{"v":1}`)
	if err := f.PutManifest(ctx, "o", "s", "v", body); err != nil {
		t.Fatal(err)
	}
	got1, _ := f.GetManifest(ctx, "o", "s", "v")
	got1[0] = 'X'
	got2, _ := f.GetManifest(ctx, "o", "s", "v")
	if string(got2) != `{"v":1}` {
		t.Errorf("stored manifest mutated through the returned slice: %s", got2)
	}
	// And PutManifest must have copied the input (mutating the source after the
	// put must not change the stored value).
	src := []byte(`{"v":2}`)
	_ = f.PutManifest(ctx, "o", "s", "v2", src)
	src[0] = 'X'
	stored, _ := f.GetManifest(ctx, "o", "s", "v2")
	if string(stored) != `{"v":2}` {
		t.Errorf("PutManifest did not copy its input: %s", stored)
	}
}
