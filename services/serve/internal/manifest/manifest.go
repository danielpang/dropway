// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package manifest is the Go port of the serving Worker's per-deploy manifest
// model + path resolution (edge/serving-worker/src/manifest.ts). It turns a
// cleaned request path into a content-addressed blob key via the published
// manifest, with directory-index + pretty-URL fallbacks, and fails CLOSED on any
// drift (schema_version != 1, bad sha256, empty content_type).
package manifest

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// SupportedSchemaVersion is the manifest-shape version this server understands.
// It MUST equal the Worker's SUPPORTED_MANIFEST_SCHEMA_VERSION and
// internal/manifest.SchemaVersion (== 1).
const SupportedSchemaVersion = 1

// NotFoundPath is the manifest key of a version's custom 404 page, if it ships one.
const NotFoundPath = "404.html"

var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Entry is one validated manifest entry: the content-addressed sha256, the
// authoritative content_type recorded at publish, and an optional decoded size.
type Entry struct {
	SHA256      string
	ContentType string
	Size        int64
	HasSize     bool
}

// Manifest is a parsed, validated deploy manifest: request-path → entry.
type Manifest struct {
	Files map[string]Entry
}

// rawManifest / rawEntry mirror the stored JSON shape (deployments.go
// storedManifest). schema_version + size use json.Number so we can enforce the
// "integer" and ">=0 finite" constraints faithfully.
type rawManifest struct {
	SchemaVersion *json.Number               `json:"schema_version"`
	Files         map[string]json.RawMessage `json:"files"`
}

type rawEntry struct {
	SHA256      *string      `json:"sha256"`
	ContentType *string      `json:"content_type"`
	Size        *json.Number `json:"size"`
}

// Parse validates untrusted manifest JSON into a Manifest, returning ok=false on
// any shape/version mismatch or a single malformed entry (the WHOLE manifest is
// rejected, mirroring parseManifest). Callers fail closed (404) on ok=false.
//
// NOTE on content_type: the Go publisher writes content_type with `omitempty`, so
// an entry CAN omit it; we follow the WORKER and REJECT a missing/empty
// content_type (fail closed) to stay byte-for-byte faithful.
func Parse(raw []byte) (Manifest, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var rm rawManifest
	if err := dec.Decode(&rm); err != nil {
		return Manifest{}, false
	}
	if rm.SchemaVersion == nil {
		return Manifest{}, false
	}
	sv, err := rm.SchemaVersion.Int64()
	if err != nil || sv != SupportedSchemaVersion {
		return Manifest{}, false
	}
	if rm.Files == nil {
		return Manifest{}, false
	}

	files := make(map[string]Entry, len(rm.Files))
	for path, rawEntry := range rm.Files {
		entry, ok := parseEntry(rawEntry)
		if !ok {
			return Manifest{}, false // any bad entry → reject the whole manifest
		}
		files[path] = entry
	}
	return Manifest{Files: files}, true
}

func parseEntry(raw json.RawMessage) (Entry, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var re rawEntry
	if err := dec.Decode(&re); err != nil {
		return Entry{}, false
	}
	if re.SHA256 == nil || !sha256RE.MatchString(*re.SHA256) {
		return Entry{}, false
	}
	if re.ContentType == nil || *re.ContentType == "" {
		return Entry{}, false
	}
	entry := Entry{SHA256: *re.SHA256, ContentType: *re.ContentType}
	if re.Size != nil {
		n, err := re.Size.Int64()
		if err == nil && n >= 0 {
			entry.Size = n
			entry.HasSize = true
		}
	}
	return entry, true
}

// Match is a resolved manifest entry plus the manifest key it matched (the served
// path, which drives Cache-Control).
type Match struct {
	Path  string
	Entry Entry
}

// Resolve resolves a cleaned, prefix-relative request path to a manifest entry,
// applying the static-site fallbacks in order. Returns ok=false when nothing
// matches. Faithful port of resolveManifestEntry/candidatePaths.
func (m Manifest) Resolve(cleanRelPath string) (Match, bool) {
	for _, candidate := range candidatePaths(cleanRelPath) {
		if entry, ok := m.Files[candidate]; ok {
			return Match{Path: candidate, Entry: entry}, true
		}
	}
	return Match{}, false
}

// NotFoundEntry returns the version's custom 404.html entry, if present.
func (m Manifest) NotFoundEntry() (Entry, bool) {
	e, ok := m.Files[NotFoundPath]
	return e, ok
}

// candidatePaths is a faithful port of candidatePaths: directory request → only
// "<path>index.html"; exact match; extension-less pretty path also tries
// "<path>/index.html" then "<path>.html".
func candidatePaths(cleanRelPath string) []string {
	if cleanRelPath == "" || strings.HasSuffix(cleanRelPath, "/") {
		return []string{cleanRelPath + "index.html"}
	}
	candidates := []string{cleanRelPath}
	if !hasExtension(cleanRelPath) {
		candidates = append(candidates, cleanRelPath+"/index.html", cleanRelPath+".html")
	}
	return candidates
}

// hasExtension reports whether the final path segment carries a file extension
// (a '.' that is neither the first nor the last char). Mirrors hasExtension.
func hasExtension(cleanRelPath string) bool {
	last := lastSegment(cleanRelPath)
	dot := strings.LastIndex(last, ".")
	return dot > 0 && dot < len(last)-1
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i != -1 {
		return p[i+1:]
	}
	return p
}
