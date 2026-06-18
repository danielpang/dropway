// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package servehttp ports the serving Worker's HTTP response policy: the MIME
// table + Content-Type derivation, the Cache-Control policy (immutable hashed
// assets vs short-TTL HTML), the exact security headers (CONTENT_CSP /
// PLATFORM_CSP + base headers), the service-worker block, and the platform error
// pages (404/410/429/503). All strings are copied verbatim from
// edge/serving-worker/src/{http,security,index}.ts.
package servehttp

import (
	"regexp"
	"strings"
)

// mimeTable mirrors http.ts MIME (used only when an entry's content_type is
// absent — we serve the manifest content_type verbatim otherwise).
var mimeTable = map[string]string{
	"html":        "text/html; charset=utf-8",
	"htm":         "text/html; charset=utf-8",
	"xml":         "application/xml; charset=utf-8",
	"txt":         "text/plain; charset=utf-8",
	"md":          "text/markdown; charset=utf-8",
	"css":         "text/css; charset=utf-8",
	"js":          "text/javascript; charset=utf-8",
	"mjs":         "text/javascript; charset=utf-8",
	"map":         "application/json; charset=utf-8",
	"json":        "application/json; charset=utf-8",
	"png":         "image/png",
	"jpg":         "image/jpeg",
	"jpeg":        "image/jpeg",
	"gif":         "image/gif",
	"webp":        "image/webp",
	"avif":        "image/avif",
	"svg":         "image/svg+xml",
	"ico":         "image/x-icon",
	"woff":        "font/woff",
	"woff2":       "font/woff2",
	"ttf":         "font/ttf",
	"otf":         "font/otf",
	"eot":         "application/vnd.ms-fontobject",
	"wasm":        "application/wasm",
	"pdf":         "application/pdf",
	"webmanifest": "application/manifest+json",
}

const defaultContentType = "application/octet-stream"

// shortTTLSeconds is the short Cache-Control TTL for HTML / non-fingerprinted
// assets (http.ts SHORT_TTL_SECONDS).
const shortTTLSeconds = 60

// hashedAssetRE matches a >=8-char hex/base62 fingerprint token delimited by
// '.', '-' or '_' before the extension (http.ts isHashedAsset).
var hashedAssetRE = regexp.MustCompile(`[.\-_][0-9a-zA-Z]{8,}\.[0-9a-zA-Z]+$`)

// extensionOf returns the lowercased final extension of a key, or "" — a faithful
// port of http.ts extensionOf (a leading-dot-only or trailing-dot is no extension).
func extensionOf(key string) string {
	last := key
	if i := strings.LastIndex(key, "/"); i != -1 {
		last = key[i+1:]
	}
	dot := strings.LastIndex(last, ".")
	if dot <= 0 || dot == len(last)-1 {
		return ""
	}
	return strings.ToLower(last[dot+1:])
}

// ContentTypeFor maps a key to a Content-Type via the MIME table, defaulting to
// octet-stream. Only used as a fallback when the manifest entry has no content_type.
func ContentTypeFor(key string) string {
	if mt, ok := mimeTable[extensionOf(key)]; ok {
		return mt
	}
	return defaultContentType
}

// IsHTML reports whether a key is an HTML entry document (never immutable).
func IsHTML(key string) bool {
	ext := extensionOf(key)
	return ext == "html" || ext == "htm"
}

// IsHashedAsset reports whether an asset name looks content-hash-fingerprinted
// (safe to cache immutably). HTML is never treated as immutable.
func IsHashedAsset(key string) bool {
	if IsHTML(key) {
		return false
	}
	last := key
	if i := strings.LastIndex(key, "/"); i != -1 {
		last = key[i+1:]
	}
	return hashedAssetRE.MatchString(last)
}

// CacheControlFor returns the public Cache-Control for a served path: immutable
// for hashed assets, short-TTL revalidatable otherwise (http.ts cacheControlFor).
func CacheControlFor(servedPath string) string {
	if IsHashedAsset(servedPath) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=60, must-revalidate"
}
