// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package storage

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Store for unit tests. PresignPut returns a synthetic URL
// (no real upload happens); tests that exercise the upload path call PutBlob to
// stage bytes. It is safe for concurrent use.
//
// PresignBase, if set, is used as the scheme+host of the synthetic presign URL so
// a test HTTP server can intercept the PUT (the integration test points it at a
// httptest server that writes through to PutBlob). When empty, a stable opaque
// URL is returned.
type Fake struct {
	PresignBase string // e.g. "http://127.0.0.1:1234" ; empty → "https://fake.local"

	// Now, if set, supplies the timestamp PutBlob records as a blob's last-modified
	// time. It lets a test stage a blob "in the past" (an old orphan) so the GC age
	// guard is exercisable without sleeping. nil → time.Now.
	Now func() time.Time

	mu        sync.Mutex
	blobs     map[string][]byte
	blobMod   map[string]time.Time // blob key → last-modified (GC age guard input)
	manifests map[string][]byte
}

// NewFake returns an empty in-memory store.
func NewFake() *Fake {
	return &Fake{
		blobs:     map[string][]byte{},
		blobMod:   map[string]time.Time{},
		manifests: map[string][]byte{},
	}
}

// now returns the Fake's clock (Now override or time.Now).
func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// PresignPut returns a synthetic URL whose path encodes the blob key, so a test
// server can route the PUT back to the matching key.
func (f *Fake) PresignPut(_ context.Context, orgID, sha256 string, _ time.Duration) (string, error) {
	base := f.PresignBase
	if base == "" {
		base = "https://fake.local"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + BlobKey(orgID, sha256)
	return u.String(), nil
}

// HeadBlob reports blob presence and size.
func (f *Fake) HeadBlob(_ context.Context, orgID, sha256 string) (bool, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.blobs[BlobKey(orgID, sha256)]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(b)), nil
}

// GetBlob returns a reader over the stored bytes.
func (f *Fake) GetBlob(_ context.Context, orgID, sha256 string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.blobs[BlobKey(orgID, sha256)]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

// PutBlob stages a blob's bytes, recording the current (Now-override) time as its
// last-modified timestamp — the GC age-guard input ListBlobInfos surfaces.
func (f *Fake) PutBlob(_ context.Context, orgID, sha256 string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := BlobKey(orgID, sha256)
	f.blobs[key] = data
	f.blobMod[key] = f.now()
	return nil
}

// PutManifest stages a deploy manifest.
func (f *Fake) PutManifest(_ context.Context, orgID, siteID, versionID string, manifest []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.manifests[ManifestKey(orgID, siteID, versionID)] = append([]byte(nil), manifest...)
	return nil
}

// GetManifest reads a staged manifest.
func (f *Fake) GetManifest(_ context.Context, orgID, siteID, versionID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.manifests[ManifestKey(orgID, siteID, versionID)]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

// ListBlobInfos returns every blob under the org's prefix as a {SHA, LastModified}
// pair (the GC age-guard input).
func (f *Fake) ListBlobInfos(_ context.Context, orgID string) ([]BlobInfo, error) {
	prefix := BlobKey(orgID, "") // "blobs/<org>/"
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []BlobInfo
	for key := range f.blobs {
		if strings.HasPrefix(key, prefix) {
			out = append(out, BlobInfo{SHA: key[len(prefix):], LastModified: f.blobMod[key]})
		}
	}
	return out, nil
}

// DeleteBlob removes a blob (idempotent — deleting an absent blob is a no-op).
func (f *Fake) DeleteBlob(_ context.Context, orgID, sha256 string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := BlobKey(orgID, sha256)
	delete(f.blobs, key)
	delete(f.blobMod, key)
	return nil
}

// PutBlobBytes is a test convenience: stage a blob from a byte slice (recorded with
// the Fake's current clock as its last-modified time).
func (f *Fake) PutBlobBytes(ctx context.Context, orgID, sha256 string, data []byte) error {
	return f.PutBlob(ctx, orgID, sha256, bytes.NewReader(data), int64(len(data)), "")
}

// PutBlobBytesAt is a test convenience: stage a blob and stamp it with an explicit
// last-modified time, so a test can create an "old" or "fresh" orphan to exercise
// the GC age guard without sleeping.
func (f *Fake) PutBlobBytesAt(ctx context.Context, orgID, sha256 string, data []byte, modTime time.Time) error {
	if err := f.PutBlob(ctx, orgID, sha256, bytes.NewReader(data), int64(len(data)), ""); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobMod[BlobKey(orgID, sha256)] = modTime
	return nil
}

// Ensure Fake satisfies Store.
var _ Store = (*Fake)(nil)
