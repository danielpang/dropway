// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/danielpang/dropway/internal/manifest"
	"github.com/danielpang/dropway/internal/sandbox"
	"github.com/danielpang/dropway/internal/storage"
)

// seedSandbox streams a site version's files into the sandbox working tree so
// the model edits the CURRENT site rather than starting blank. It reads the
// version's manifest, then tars each blob in and imports it. A nil/empty base
// (a brand-new site with no version yet) is a no-op: the model starts from an
// empty directory.
func seedSandbox(ctx context.Context, objects storage.Store, sb sandbox.Sandbox, orgID, siteID, versionID string) error {
	if versionID == "" {
		return nil
	}
	body, err := objects.GetManifest(ctx, orgID, siteID, versionID)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil // a pending/empty version has no files to seed
		}
		return fmt.Errorf("ai seed: read manifest: %w", err)
	}
	var mani manifest.Stored
	if err := json.Unmarshal(body, &mani); err != nil {
		return fmt.Errorf("ai seed: parse manifest: %w", err)
	}
	if len(mani.Files) == 0 {
		return nil
	}

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		for path, target := range mani.Files {
			blob, err := objects.GetBlob(ctx, orgID, target.SHA256)
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("ai seed: get blob %s: %w", target.SHA256, err))
				return
			}
			data, err := io.ReadAll(blob)
			_ = blob.Close()
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			hdr := &tar.Header{Name: path, Mode: 0o644, Size: int64(len(data))}
			if err := tw.WriteHeader(hdr); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := tw.Write(data); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		_ = tw.Close()
		_ = pw.Close()
	}()

	if err := sb.ImportTar(ctx, sandbox.DefaultWorkdir, pr); err != nil {
		_ = pr.CloseWithError(err)
		return fmt.Errorf("ai seed: import: %w", err)
	}
	return nil
}
