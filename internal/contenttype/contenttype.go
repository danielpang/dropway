// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package contenttype is the ONE Go source of truth for guessing a deploy file's
// content type from its path extension. Both the CLI (cli/internal/api) and the MCP
// server's deploy client (services/mcp/internal/apiclient) record this in the deploy
// manifest so the serving Worker/serve sends the right Content-Type. It is NOT part
// of the deploy digest, so it isn't load-bearing for correctness — but sharing it
// keeps the two Go deploy paths from drifting.
package contenttype

import (
	"mime"
	"path/filepath"
	"strings"
)

// ForPath guesses a file's content type from its extension, falling back to a
// binary default for unknown extensions. It prefers the stdlib mime table and
// overrides a few common static-site extensions the table can miss or get wrong.
func ForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	// Overrides first: pin the static-site essentials so the result doesn't vary
	// with a host's /etc/mime.types, and so JS/JSON carry the charset we want.
	switch ext {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".wasm":
		return "application/wasm"
	case ".webmanifest":
		return "application/manifest+json"
	case ".svg":
		return "image/svg+xml"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
