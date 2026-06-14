package storage

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestKeys_AreContentAddressedAndPerOrg(t *testing.T) {
	if got := BlobKey("org-a", "abc123"); got != "blobs/org-a/abc123" {
		t.Errorf("BlobKey = %q", got)
	}
	if got := ManifestKey("org-a", "site-1", "ver-9"); got != "manifests/org-a/site-1/ver-9.json" {
		t.Errorf("ManifestKey = %q", got)
	}
	// The per-org prefix means the SAME content in two orgs lands at distinct
	// keys — no cross-tenant dedup oracle (§10).
	if BlobKey("org-a", "sha") == BlobKey("org-b", "sha") {
		t.Error("blob keys must differ across orgs for the same content")
	}
}

func TestFake_PutHeadGet(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	if ok, _, _ := f.HeadBlob(ctx, "o", "sha"); ok {
		t.Fatal("absent blob should not exist")
	}
	data := []byte("hello world")
	if err := f.PutBlobBytes(ctx, "o", "sha", data); err != nil {
		t.Fatal(err)
	}
	ok, size, err := f.HeadBlob(ctx, "o", "sha")
	if err != nil || !ok || size != int64(len(data)) {
		t.Fatalf("head = %v %d %v", ok, size, err)
	}
	rc, err := f.GetBlob(ctx, "o", "sha")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) {
		t.Errorf("get = %q", got)
	}
}

func TestFake_GetMissing(t *testing.T) {
	if _, err := NewFake().GetBlob(context.Background(), "o", "nope"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := NewFake().GetManifest(context.Background(), "o", "s", "v"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFake_PresignEncodesKey(t *testing.T) {
	f := NewFake()
	f.PresignBase = "http://127.0.0.1:9999"
	url, err := f.PresignPut(context.Background(), "org-a", "deadbeef", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := "http://127.0.0.1:9999/blobs/org-a/deadbeef"
	if url != want {
		t.Errorf("presign url = %q, want %q", url, want)
	}
}

func TestFake_Manifest(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	body := []byte(`{"schema_version":1}`)
	if err := f.PutManifest(ctx, "o", "s", "v", body); err != nil {
		t.Fatal(err)
	}
	got, err := f.GetManifest(ctx, "o", "s", "v")
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("manifest = %q %v", got, err)
	}
}
