// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package contenttype is the ONE Go source of truth for guessing a deploy file's
// content type from its path extension. Both the CLI (cli/internal/api) and the MCP
// server's deploy client (services/mcp/internal/apiclient) record this in the deploy
// manifest so the serving Worker/serve sends the right Content-Type. It is NOT part
// of the deploy digest, so it isn't load-bearing for correctness — but sharing it
// keeps the two Go deploy paths from drifting.
package contenttype

import (
	"path/filepath"
	"strings"
)

// byExt maps a lowercased file extension (with leading dot) to its content type. It
// is an EXPLICIT table — deliberately not the stdlib mime package — so the result is
// identical on every OS (mime.TypeByExtension also reads the host's /etc/mime.types,
// which made the same file get different types on Linux vs macOS). The set mirrors
// the dashboard's table (apps/dashboard/lib/deploy-manifest.ts) so all deploy paths
// agree; anything not listed falls back to application/octet-stream.
var byExt = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".svg":         "image/svg+xml",
	".wasm":        "application/wasm",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml",
	".ico":         "image/x-icon",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".eot":         "application/vnd.ms-fontobject",
	".pdf":         "application/pdf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
	".wav":         "audio/wav",
}

// ForPath guesses a file's content type from its extension via an explicit table,
// falling back to a binary default for unknown extensions. The result does not vary
// with the host OS.
func ForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ct, ok := byExt[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

// Known reports whether the path's extension is in the explicit table (i.e. ForPath
// returns a table entry rather than the application/octet-stream fallback). Callers
// that must derive an authoritative content type from the extension use this to tell
// a recognized extension apart from the binary default.
func Known(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := byExt[ext]
	return ok
}
