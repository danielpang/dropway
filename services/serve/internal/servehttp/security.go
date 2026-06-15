// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package servehttp

import (
	"net/http"
	"strings"
)

// ContentCSP is the permissive tenant-content CSP (security.ts CONTENT_CSP),
// copied verbatim.
const ContentCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; " +
	"font-src 'self' data: https:; " +
	"media-src 'self' data: blob: https:; " +
	"connect-src 'self' https:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// PlatformCSP is the strict CSP for platform-owned pages (security.ts PLATFORM_CSP).
const PlatformCSP = "default-src 'none'; " +
	"style-src 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'"

// baseSecurityHeaders is the always-on baseline (security.ts baseSecurityHeaders).
func baseSecurityHeaders() map[string]string {
	return map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"X-Frame-Options":              "DENY",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-site",
	}
}

// ContentSecurityHeaders is the header set for served tenant content (public +
// gated), custom-404, and the 405 page: base headers + the permissive content CSP.
func ContentSecurityHeaders() map[string]string {
	h := baseSecurityHeaders()
	h["Content-Security-Policy"] = ContentCSP
	return h
}

// PlatformSecurityHeaders is the header set for platform-owned pages (404/410/
// 429/503): base headers + the strict platform CSP.
func PlatformSecurityHeaders() map[string]string {
	h := baseSecurityHeaders()
	h["Content-Security-Policy"] = PlatformCSP
	return h
}

// ApplyHeaders writes a record onto an http.Header, skipping empty values.
func ApplyHeaders(dst http.Header, record map[string]string) {
	for k, v := range record {
		if v != "" {
			dst.Set(k, v)
		}
	}
}

// serviceWorkerScripts are the conventional SW script filenames (security.ts
// SERVICE_WORKER_SCRIPTS), matched on the final path segment case-insensitively.
var serviceWorkerScripts = map[string]struct{}{
	"sw.js":                    {},
	"service-worker.js":        {},
	"serviceworker.js":         {},
	"service-worker.min.js":    {},
	"sw.min.js":                {},
	"firebase-messaging-sw.js": {},
	"ngsw-worker.js":           {},
	"workbox-sw.js":            {},
}

// IsServiceWorkerRequest reports whether the request is a SW-script fetch
// (Service-Worker: script header), an exact case-sensitive value match.
func IsServiceWorkerRequest(r *http.Request) bool {
	return r.Header.Get("Service-Worker") == "script"
}

// IsServiceWorkerScript reports whether a cleaned path's final segment is a
// conventional service-worker script name (case-insensitive).
func IsServiceWorkerScript(cleanRelPath string) bool {
	last := cleanRelPath
	if i := strings.LastIndex(cleanRelPath, "/"); i != -1 {
		last = cleanRelPath[i+1:]
	}
	_, ok := serviceWorkerScripts[strings.ToLower(last)]
	return ok
}
