// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package storage

import (
	"bytes"
	"context"
	"io"
	"net/url"
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

	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
}

// NewFake returns an empty in-memory store.
func NewFake() *Fake {
	return &Fake{
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
	}
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

// PutBlob stages a blob's bytes.
func (f *Fake) PutBlob(_ context.Context, orgID, sha256 string, r io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[BlobKey(orgID, sha256)] = data
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

// PutBlobBytes is a test convenience: stage a blob from a byte slice.
func (f *Fake) PutBlobBytes(ctx context.Context, orgID, sha256 string, data []byte) error {
	return f.PutBlob(ctx, orgID, sha256, bytes.NewReader(data), int64(len(data)), "")
}

// Ensure Fake satisfies Store.
var _ Store = (*Fake)(nil)
