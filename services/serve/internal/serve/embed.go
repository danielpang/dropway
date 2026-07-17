// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/serve/internal/servehttp"
)

// embedQueryParam is the query key that opts the top document into the framable embed
// surface (?embed=1). Mirrors edge/serving-worker/src/embed.ts EMBED_QUERY_PARAM.
const embedQueryParam = "embed"

// isEmbedRequested reports whether the request opts into embed mode — the presence of
// `?embed` (any value, incl. empty). Mirrors index.ts isEmbedRequested / isRawRequested.
func isEmbedRequested(r *http.Request) bool {
	if r.URL == nil {
		return false
	}
	return r.URL.Query().Has(embedQueryParam)
}

// serveEmbed serves a request that opted into the framable embed surface (?embed=1),
// mirroring index.ts serveEmbed MINUS the "Powered by Dropway" badge injection — the
// Go self-host serve never injects Dropway attribution (its no-banner posture), so an
// embedded self-hosted site carries no badge.
//
//   - GATED (password/allowlist/org_only): NEVER serve the bytes into an embed. Write
//     the framable "Sign in to view" placeholder that links out to the real site; fail
//     closed for every non-public mode (an embed can't bypass the gate).
//   - PUBLIC: resolve + stream the blob exactly like the public path, but with FRAMABLE
//     headers (X-Frame-Options dropped, CSP frame-ancestors *).
func (h *Handler) serveEmbed(w http.ResponseWriter, r *http.Request, rt *Route, host string) {
	if rt.AccessMode != projection.AccessPublic {
		h.writeEmbedGate(w, r, host)
		return
	}

	rb, ok := h.resolveBlob(w, r, rt, false)
	if !ok {
		return
	}
	defer rb.body.Close()

	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.FramableContentSecurityHeaders())
	hd.Set("Content-Type", rb.contentType)
	hd.Set("Cache-Control", servehttp.CacheControlFor(rb.servedPath))
	if rb.hasLen {
		hd.Set("Content-Length", strconv.FormatInt(rb.contentLen, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rb.body)
	}
}

// writeEmbedGate writes the framable "Sign in to view" placeholder served when a GATED
// site is requested in embed mode. It NEVER contains tenant content — a platform page
// that fully substitutes for the private site inside the frame, linking out (new tab)
// to the real site where the viewer authenticates. `no-store` so a later access change
// is visible immediately; framable platform headers so it renders inside the iframe.
func (h *Handler) writeEmbedGate(w http.ResponseWriter, r *http.Request, host string) {
	// The site ROOT with no ?embed=1: clicking it opens the gated site (new tab) and
	// runs the normal /authz sign-in.
	body := []byte(renderEmbedGateHTML(h.contentURL(host, "/")))

	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.FramablePlatformSecurityHeaders())
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// renderEmbedGateHTML renders the private-site placeholder. siteURL is HTML-escaped
// before interpolation. Kept byte-compatible with embed.ts renderEmbedGateHtml.
func renderEmbedGateHTML(siteURL string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Private site</title>
<style>
  :root { color-scheme: light dark; }
  html, body { height: 100%%; }
  body { margin: 0; font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI",
         Roboto, Helvetica, Arial, sans-serif;
         display: grid; place-items: center; padding: 1.5rem; }
  main { text-align: center; max-width: 22rem; }
  .lock { font-size: 1.75rem; line-height: 1; margin-bottom: .5rem; }
  h1 { font-size: 1.05rem; margin: 0 0 .35rem; }
  p { opacity: .7; margin: 0 0 1rem; font-size: .9rem; }
  a.cta { display: inline-block; padding: .5rem 1rem; border-radius: .5rem;
          background: #4f46e5; color: #fff; text-decoration: none;
          font-weight: 600; font-size: .9rem; }
</style>
</head>
<body>
  <main>
    <div class="lock" aria-hidden="true">🔒</div>
    <h1>This site is private</h1>
    <p>You need to sign in to view this content.</p>
    <a class="cta" href="%s" target="_blank" rel="noopener noreferrer">Sign in to view</a>
  </main>
</body>
</html>
`, html.EscapeString(siteURL))
}
