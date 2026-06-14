// Package manifest holds the ONE source of truth for the whole-deploy content
// digest (docs/ARCHITECTURE.md §7.1). Both the CLI (cli/internal/manifest, which
// walks a directory and re-exports this) and the Go API server
// (services/api/internal/handlers, which recomputes the digest from the request
// manifest to reject a tampered client digest) call Digest here, so the digest
// format can never drift between the two.
//
// The digest is a single content-address for an entire deploy: the SHA-256 over
// the files sorted by path, one git-like "<sha256>  <path>\n" line per file (note
// the TWO spaces between hash and path). It is the version's content_hash and the
// UNIQUE(site_id, content_hash) idempotency key, so the exact byte layout is
// load-bearing and MUST stay stable.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// SchemaVersion is the contract version of the stored per-deploy manifest JSON
// (manifests/<org>/<site>/<version>.json) the serving Worker reads and pins. It
// MUST equal SUPPORTED_MANIFEST_SCHEMA_VERSION in edge/serving-worker/src/
// manifest.ts.
//
// This is a SEPARATE contract from internal/projection.SchemaVersion (the KV
// route value). The two evolve on independent cadences, so they must NEVER be
// sourced from the same constant: bumping the route contract (e.g. v1→v2 to add
// expires_at) must not flip the manifest version the Worker accepts, or every new
// deploy's manifest is rejected and the site 404s. A handler round-trip test pins
// this value to what the Worker accepts.
const SchemaVersion = 1

// File is the minimal (path, sha256) pair the digest is computed over. Both the
// CLI Entry and the server's ManifestFile project onto this shape.
type File struct {
	// Path is the forward-slash relative path served from the site
	// (e.g. "index.html", "assets/app.js").
	Path string
	// SHA256 is the lowercase hex content hash of the file's bytes.
	SHA256 string
}

// Digest returns the whole-deploy content address: the SHA-256 over the files
// sorted by Path, one "<sha256>  <path>\n" line each (two-space separator). The
// input slice is not mutated (sorting happens on a copy), so callers may pass
// their manifest in any order and get the same stable result.
func Digest(files []File) string {
	sorted := make([]File, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	for _, f := range sorted {
		// One line per file: "<sha256>  <path>\n" (git-like, stable). The two
		// spaces between hash and path are part of the contract.
		fmt.Fprintf(h, "%s  %s\n", f.SHA256, f.Path)
	}
	return hex.EncodeToString(h.Sum(nil))
}
