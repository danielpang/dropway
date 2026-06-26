// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/manifest"
	"github.com/danielpang/dropway/services/serve/internal/servehttp"
)

// writePage writes a platform-owned HTML page with the strict platform CSP. Used
// for the default 404, 410, 429, and 503 pages. The body is suppressed for HEAD.
func writePage(w http.ResponseWriter, r *http.Request, status int, cacheControl, body string, extra map[string]string) {
	h := w.Header()
	servehttp.ApplyHeaders(h, servehttp.PlatformSecurityHeaders())
	for k, v := range extra {
		h.Set(k, v)
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", cacheControl)
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// notFound writes the 404 response. When route + manifest are known and the
// manifest ships a 404.html whose blob exists, it serves that custom tenant page
// (tenant CSP); otherwise the platform default 404 (platform CSP). Mirrors
// index.ts notFound + gated.ts asPrivate: on a GATED route every 404 (custom or
// platform) MUST be private/no-store + Vary:Cookie so protected-origin responses
// never enter a shared cache; public 404s stay publicly cacheable (max-age=30).
// A nil manifest skips the custom path.
func (h *Handler) notFound(w http.ResponseWriter, r *http.Request, route *Route, m *manifest.Manifest, gated bool) {
	cacheControl := "public, max-age=30"
	if gated {
		cacheControl = "private, no-store, max-age=0, must-revalidate"
	}
	if route != nil && m != nil {
		if entry, ok := m.NotFoundEntry(); ok {
			body, err := h.store.GetBlob(r.Context(), route.OrgID, entry.SHA256)
			if err == nil {
				defer body.Close()
				hd := w.Header()
				servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
				hd.Set("Content-Type", entry.ContentType)
				hd.Set("Cache-Control", cacheControl)
				if gated {
					hd.Set("Vary", "Cookie")
				}
				w.WriteHeader(http.StatusNotFound)
				if r.Method != http.MethodHead {
					_, _ = io.Copy(w, body)
				}
				return
			}
			// A missing custom-404 blob (or read error) falls through to platform 404.
		}
	}
	var extra map[string]string
	if gated {
		extra = map[string]string{"Vary": "Cookie"}
	}
	writePage(w, r, http.StatusNotFound, cacheControl, servehttp.Default404HTML, extra)
}

// linkExpired writes the 410 platform "link expired" page (no-store).
func (h *Handler) linkExpired(w http.ResponseWriter, r *http.Request) {
	writePage(w, r, http.StatusGone, "no-store", servehttp.LinkExpiredHTML, nil)
}

// serverError writes the 500 platform page (no-store) for a SERVER-SIDE failure
// resolving the host — a resolver/backend error, as opposed to a genuinely unknown
// host (which is a clean 404). The page is generic so it never leaks the internal
// reason. Mirrors the serving Worker's projectionError(): our problem is a visible,
// uncached 5xx (surfaces in monitoring), not a silent 404.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	writePage(w, r, http.StatusInternalServerError, "no-store", servehttp.ServerError500HTML, nil)
}

// tooManyRequests writes the 429 platform page with Retry-After (no-store).
func (h *Handler) tooManyRequests(w http.ResponseWriter, r *http.Request, retryAfterSeconds int) {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	writePage(w, r, http.StatusTooManyRequests, "no-store", servehttp.TooManyRequestsHTML,
		map[string]string{"Retry-After": strconv.Itoa(retryAfterSeconds)})
}

// accountSuspended writes the 503 platform page for a blocking org status (503,
// Retry-After: 300, no-store). over_limit and suspended share a page with distinct copy.
func (h *Handler) accountSuspended(w http.ResponseWriter, r *http.Request, status string) {
	body := servehttp.AccountSuspendedHTML
	if status == "over_limit" {
		body = servehttp.AccountOverLimitHTML
	}
	writePage(w, r, http.StatusServiceUnavailable, "no-store", body,
		map[string]string{"Retry-After": "300"})
}

// isStorageMiss reports whether a storage error is an expected absent-object miss.
func isStorageMiss(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// contentTypeFor returns the entry's content_type verbatim, falling back to the
// extension MIME map only when absent (it never is, post-validation).
func contentTypeFor(entry manifest.Entry, servedPath string) string {
	if entry.ContentType != "" {
		return entry.ContentType
	}
	return servehttp.ContentTypeFor(servedPath)
}
