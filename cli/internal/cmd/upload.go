package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/manifest"
)

// blobUploader is the one method uploadMissing needs from a client. Both
// api.Client (deploy) and api.SkillsClient (skills push) satisfy it, so the
// two upload flows share this loop instead of each re-implementing it.
type blobUploader interface {
	UploadBlob(ctx context.Context, presignedURL string, data []byte) error
}

// uploadMissing reads each missing blob's bytes from disk and PUTs them to the
// presigned URL the server returned. Only blobs the server doesn't already have
// are uploaded (only-changed-blob upload). A blob may back multiple paths;
// we find the first file with the matching hash.
func uploadMissing(ctx context.Context, client blobUploader, dir string, m *manifest.Manifest, prep *api.PrepareResponse) error {
	// Index manifest entries by sha so we can locate a file path per missing sha.
	pathBySHA := make(map[string]string, len(m.Files))
	for _, e := range m.Files {
		if _, ok := pathBySHA[e.SHA256]; !ok {
			pathBySHA[e.SHA256] = e.Path
		}
	}
	for _, sha := range prep.Missing {
		url, ok := prep.Uploads[sha]
		if !ok {
			return fmt.Errorf("upload: server listed %s missing but gave no upload URL", sha)
		}
		relPath, ok := pathBySHA[sha]
		if !ok {
			return fmt.Errorf("upload: no local file matches blob %s", sha)
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err != nil {
			return fmt.Errorf("upload: read %s: %w", relPath, err)
		}
		if err := client.UploadBlob(ctx, url, data); err != nil {
			return fmt.Errorf("upload %s: %w", relPath, err)
		}
	}
	return nil
}
