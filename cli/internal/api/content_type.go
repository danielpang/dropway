// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"mime"
	"path/filepath"
	"strings"
)

// contentTypeFor guesses a file's content-type from its extension so the served
// blob carries the right Content-Type (the serving Worker reads it from the
// deploy manifest). Falls back to a binary default for unknown extensions.
func contentTypeFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	// A few common static-site extensions the stdlib mime table can miss.
	switch ext {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".wasm":
		return "application/wasm"
	case ".webmanifest":
		return "application/manifest+json"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
