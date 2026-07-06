// Package manifest builds a content-addressed deploy manifest from a directory:
// walk the tree, compute SHA-256 per file, and record path→hash plus size. This
// is the client side of the deploy contract: the CLI
// computes hashes locally so only missing blobs ever upload.
//
// The whole-deploy digest is computed by the SHARED root-level package
// internal/manifest (Digest), which the server also uses to recompute and verify
// the digest — so the CLI and server can never drift on the digest format.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sharedmanifest "github.com/danielpang/dropway/internal/manifest"
)

// Entry is one file in the manifest.
type Entry struct {
	// Path is the forward-slash relative path from the deploy root (the URL
	// path served from the site, e.g. "index.html", "assets/app.js").
	Path string `json:"path"`
	// SHA256 is the lowercase hex content hash. The server derives the R2 key
	// from the authenticated org + this hash and re-verifies stored bytes match.
	SHA256 string `json:"sha256"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
}

// Manifest is the full set of files in a deploy, plus a digest over them.
type Manifest struct {
	// Files is sorted by Path for a deterministic digest and stable output.
	Files []Entry `json:"files"`
	// Digest is the SHA-256 over the sorted "<sha256>  <path>\n" lines — a single
	// content-address for the whole deploy (used for idempotency/dedup).
	Digest string `json:"digest"`
	// TotalSize is the sum of all file sizes.
	TotalSize int64 `json:"total_size"`
}

// Build walks root and returns a Manifest. Symlinks are skipped (a deploy is
// plain files); hidden files are included (a static site may legitimately ship
// dotfiles like .well-known). Returns an error if root isn't a directory.
func Build(root string) (*Manifest, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("manifest: stat %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("manifest: %q is not a directory", root)
	}

	var entries []Entry
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip directories themselves and anything that isn't a regular file
		// (symlinks, sockets, devices) — only real bytes go in a deploy.
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Normalize to forward slashes for a cross-platform-stable manifest.
		rel = filepath.ToSlash(rel)

		sum, size, err := hashFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{Path: rel, SHA256: sum, Size: size})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("manifest: walk %q: %w", root, walkErr)
	}

	// Deterministic order → deterministic digest + stable output.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	var total int64
	for _, e := range entries {
		total += e.Size
	}

	return &Manifest{
		Files:     entries,
		Digest:    Digest(entries),
		TotalSize: total,
	}, nil
}

// Digest returns the whole-deploy content address over entries, delegating to
// the shared internal/manifest.Digest so the CLI and the server agree byte-for-
// byte on the format (the sorted "<sha256>  <path>\n" lines).
func Digest(entries []Entry) string {
	files := make([]sharedmanifest.File, len(entries))
	for i, e := range entries {
		files[i] = sharedmanifest.File{Path: e.Path, SHA256: e.SHA256}
	}
	return sharedmanifest.Digest(files)
}

// hashFile streams a file through SHA-256, returning the hex digest and size.
func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// PathHashMap returns the manifest as a path→hash map (the shape the dashboard's
// drag-and-drop path and the /deployments/prepare body both speak).
func (m *Manifest) PathHashMap() map[string]string {
	out := make(map[string]string, len(m.Files))
	for _, e := range m.Files {
		out[e.Path] = e.SHA256
	}
	return out
}

// Summary is a short, human-readable description for CLI output.
func (m *Manifest) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d file(s), %s, digest %s",
		len(m.Files), HumanBytes(m.TotalSize), short(m.Digest))
	return b.String()
}

func short(hexStr string) string {
	if len(hexStr) <= 12 {
		return hexStr
	}
	return hexStr[:12]
}

// HumanBytes formats a byte count as a short human-readable size (e.g.
// "2.0 KB"). Exported so CLI commands can reuse the same scale/format the
// manifest summary uses.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
