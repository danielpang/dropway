// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package serve

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/serve/internal/edgeverify"
	"github.com/danielpang/dropway/services/serve/internal/manifest"
	"github.com/danielpang/dropway/services/serve/internal/ratelimit"
	"github.com/danielpang/dropway/services/serve/internal/route"
	"github.com/danielpang/dropway/services/serve/internal/servehttp"
)

// authzCallbackPath is the post-mint callback the dashboard 302s to on the content
// host (authz.ts AUTHZ_CALLBACK_PATH).
const authzCallbackPath = "/__authz/callback"

// edgeCookieNameSecure is the production host-only cookie carrying the edge token:
// the __Host- prefix forces browser-enforced Secure + Path=/ + no Domain (config.ts).
const edgeCookieNameSecure = "__Host-edge"

// edgeCookieNameInsecure is used ONLY when the content origin is plain http
// (CONTENT_SCHEME=http — a local/dev self-host). The __Host- prefix REQUIRES Secure,
// and browsers REJECT a Secure cookie over http, so a local http origin must use an
// unprefixed, non-Secure cookie — otherwise the callback's Set-Cookie is silently
// dropped and the viewer loops back to /authz forever (ERR_TOO_MANY_REDIRECTS). It
// stays HttpOnly + host-only (no Domain), so tenant JS still can't read it and it
// can't leak to a sibling host; only the Secure-transport guarantee is dropped, which
// is moot over loopback http. Mirrors the dashboard's scheme-derived useSecureCookies.
const edgeCookieNameInsecure = "edge"

// edgeCookieMaxAge matches the edge-token TTL (15m), per authz.ts EDGE_COOKIE_MAX_AGE.
const edgeCookieMaxAge = 15 * 60

// Config configures the Handler's gated-path behavior.
type Config struct {
	// AppAuthzURL is the dashboard /authz exchange origin a gated request with no /
	// invalid edge token 302s to (config.ts DEFAULT_APP_AUTHZ_URL).
	AppAuthzURL string

	// ContentScheme / ContentPort are the PUBLIC scheme + optional port serve uses to
	// build absolute URLs back to a content host (the post-mint callback redirect and
	// its follow-on to the site). The bare host has no port (serve routes by Host
	// header), so these come from config: production https on the default port; a local
	// http self-host uses http + :8090. Empty ContentScheme defaults to https.
	ContentScheme string
	ContentPort   string
}

// Handler is the content-serving HTTP handler — the full serve() lifecycle.
type Handler struct {
	resolver  RouteResolver
	store     storage.Store
	verifier  *edgeverify.Verifier
	limiter   *ratelimit.Limiter
	orgStatus OrgStatusReader // may be nil (fail open / skipped)
	cfg       Config
	now       func() time.Time
}

// New builds a Handler. orgStatus may be nil (then the org-status gate is skipped,
// matching the Worker's "no status KV configured" path). limiter may be nil (no
// rate limiting). verifier is required for gated serving; a nil verifier denies
// every gated request (fail closed).
func New(resolver RouteResolver, store storage.Store, verifier *edgeverify.Verifier,
	limiter *ratelimit.Limiter, orgStatus OrgStatusReader, cfg Config) *Handler {
	if cfg.AppAuthzURL == "" {
		cfg.AppAuthzURL = "https://app.dropway.dev/authz"
	}
	return &Handler{
		resolver:  resolver,
		store:     store,
		verifier:  verifier,
		limiter:   limiter,
		orgStatus: orgStatus,
		cfg:       cfg,
		now:       time.Now,
	}
}

// SetClock overrides the handler clock (tests).
func (h *Handler) SetClock(now func() time.Time) { h.now = now }

// ServeHTTP runs the serve() lifecycle in the EXACT order index.ts does — the
// order is security-load-bearing.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// STEP -1: method gate. Only GET/HEAD reach content; else 405 with the CONTENT
	// security headers (matches index.ts, which uses securityHeaders() here).
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd := w.Header()
		hd.Set("Allow", "GET, HEAD")
		servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
		w.WriteHeader(http.StatusMethodNotAllowed)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "Method Not Allowed")
		}
		return
	}

	// STEP -0.5: block service-worker registration BEFORE any cache/route lookup.
	if servehttp.IsServiceWorkerRequest(r) {
		h.notFound(w, r, nil, nil, false)
		return
	}

	host := route.NormalizeHost(hostOf(r))
	now := h.now()

	// STEP 0: edge rate limiting (before the route lookup / before storage). Fails
	// OPEN: a disabled/nil limiter always allows.
	if h.limiter != nil {
		if res := h.limiter.Allow(ratelimit.Identity(r, host)); !res.Allowed {
			h.tooManyRequests(w, r, res.RetryAfterSeconds)
			return
		}
	}

	// STEP 1: resolve host → route (authoritative; never client input). Split the
	// two failure shapes by status (mirrors the serving Worker's 404-vs-500 split):
	//   - unknown host / no live version (ErrHostNotFound) ⇒ 404 ("no such site").
	//   - any OTHER resolver/backend error ⇒ 500: a server-side failure is our
	//     problem, not "not found", so it's a visible, uncached 5xx that surfaces in
	//     monitoring (the generic page leaks no structure) rather than a silent 404.
	rt, err := h.resolver.Resolve(ctx, host)
	if err != nil {
		if errors.Is(err, ErrHostNotFound) {
			h.notFound(w, r, nil, nil, false)
			return
		}
		h.serverError(w, r)
		return
	}

	// STEP 2: edge link-expiry (public/unlisted). Inclusive boundary; malformed ⇒
	// expired (fail closed). 410 platform page.
	if route.IsRouteExpired(rt.RouteValue(), now) {
		h.linkExpired(w, r)
		return
	}

	// STEP 3: per-org suspension / over-limit, BEFORE serving ANY content (public OR
	// gated). Fails OPEN: nil reader skipped; a read error serves.
	if h.orgStatus != nil {
		if status, serr := h.orgStatus.OrgStatus(ctx, rt.OrgID); serr == nil && isBlockingStatus(status) {
			h.accountSuspended(w, r, status)
			return
		}
	}

	// STEP 3.5: LLM-access surface — robots.txt, AI-crawler gating, and the generated
	// /llms.txt index. Runs before content dispatch: public sites welcome crawlers and
	// expose /llms.txt; gated sites disallow crawlers (robots + 403) so their content is
	// reachable by LLMs only through the authenticated Dropway MCP server.
	if h.serveLLMMeta(w, r, &rt, host) {
		return
	}

	// STEP 4: dispatch by access mode.
	switch rt.AccessMode {
	case projection.AccessPublic:
		h.servePublic(w, r, &rt, host)
	case projection.AccessPassword, projection.AccessAllowlist, projection.AccessOrgOnly:
		h.serveGated(w, r, &rt, host)
	default:
		// Unreachable post-resolve, but fail closed.
		h.notFound(w, r, &rt, nil, false)
	}
}

// isBlockingStatus reports whether an org-status string blocks content.
func isBlockingStatus(status string) bool {
	return status == "suspended" || status == "over_limit"
}

// hostOf returns the request host: r.Host (which carries the Host header / :authority).
func hostOf(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

// resolvedBlob is the shared manifest→blob resolution result.
type resolvedBlob struct {
	servedPath  string
	contentType string
	body        io.ReadCloser
	contentLen  int64
	hasLen      bool
}

// resolveBlob is the shared core of the public + gated serve paths: sanitize the
// path, block SW scripts, load+validate the manifest, match an entry, fetch the
// blob. It returns ok=false and has ALREADY written the appropriate 404 (custom or
// platform) to w on a miss/drift. On ok=true the caller owns closing rb.body.
// gated=true forces every 404 it writes to be private/no-store (asPrivate parity).
func (h *Handler) resolveBlob(w http.ResponseWriter, r *http.Request, rt *Route, gated bool) (resolvedBlob, bool) {
	ctx := r.Context()

	clean, ok := route.CleanPath(pathOf(r))
	if !ok {
		// Unsafe path (traversal/NUL/backslash/bad-encoding) ⇒ 404, no leak.
		h.notFound(w, r, rt, nil, gated)
		return resolvedBlob{}, false
	}

	// Block conventional SW script filenames by path (belt-and-suspenders).
	if servehttp.IsServiceWorkerScript(clean) {
		h.notFound(w, r, rt, nil, gated)
		return resolvedBlob{}, false
	}

	m, ok := h.loadManifest(ctx, rt)
	if !ok {
		// Missing/corrupt/unsupported manifest ⇒ fail closed (default 404).
		h.notFound(w, r, rt, nil, gated)
		return resolvedBlob{}, false
	}

	match, ok := m.Resolve(clean)
	if !ok {
		// No served path matched ⇒ custom 404.html if present, else platform.
		h.notFound(w, r, rt, &m, gated)
		return resolvedBlob{}, false
	}

	body, err := h.store.GetBlob(ctx, rt.OrgID, match.Entry.SHA256)
	if err != nil {
		// A manifest entry whose blob is absent = projection drift ⇒ fail closed.
		h.notFound(w, r, rt, &m, gated)
		return resolvedBlob{}, false
	}

	rb := resolvedBlob{
		servedPath:  match.Path,
		contentType: contentTypeFor(match.Entry, match.Path),
		body:        body,
	}
	if match.Entry.HasSize {
		rb.contentLen = match.Entry.Size
		rb.hasLen = true
	}
	return rb, true
}

// loadManifest fetches + validates the version's deploy manifest. Returns ok=false
// on a missing or invalid manifest (caller fails closed).
func (h *Handler) loadManifest(ctx context.Context, rt *Route) (manifest.Manifest, bool) {
	raw, err := h.store.GetManifest(ctx, rt.OrgID, rt.SiteID, rt.VersionID)
	if err != nil {
		return manifest.Manifest{}, false
	}
	return manifest.Parse(raw)
}

// servePublic serves a public (JWT-free) request: resolve the blob and stream it
// with the public response headers + Cache-Control. No internal cache is kept
// (correctness first); the Cache-Control header is the load-bearing parity surface.
func (h *Handler) servePublic(w http.ResponseWriter, r *http.Request, rt *Route, _ string) {
	rb, ok := h.resolveBlob(w, r, rt, false)
	if !ok {
		return
	}
	defer rb.body.Close()
	h.writeContent(w, r, rb, false)
}

// serveGated serves a password/allowlist/org_only request through the unified
// edge-token cookie flow (gated.ts / authz.ts).
func (h *Handler) serveGated(w http.ResponseWriter, r *http.Request, rt *Route, host string) {
	// The post-mint callback is the only gated path that accepts a token in the URL.
	if pathOf(r) == authzCallbackPath {
		h.handleAuthzCallback(w, r, rt, host)
		return
	}

	// Read + verify the __Host-edge cookie against THIS route.
	token := h.readEdgeCookie(r)
	if token == "" || h.verifier == nil {
		h.redirectToAuthz(w, r, host)
		return
	}
	if _, ok := h.verifier.Verify(r.Context(), token, host, rt.SiteID, rt.AccessMode, rt.OrgID); !ok {
		// No/invalid cookie, JWKS outage, mode/site mismatch, expired, or revoked ⇒
		// bounce to the dashboard exchange. All collapse to a single fail-closed 302.
		h.redirectToAuthz(w, r, host)
		return
	}

	// Authorized. Serve the SAME bytes as public, but FORCE private/no-store so the
	// protected bytes never enter any shared cache. A gated 404 is also private.
	rb, ok := h.resolveBlob(w, r, rt, true)
	if !ok {
		return
	}
	defer rb.body.Close()
	h.writeContent(w, r, rb, true)
}

// writeContent writes a 200 content response. gated=true overrides Cache-Control
// to private/no-store + Vary: Cookie (asPrivate); gated=false uses the public
// Cache-Control by served path. HEAD strips the body.
func (h *Handler) writeContent(w http.ResponseWriter, r *http.Request, rb resolvedBlob, gated bool) {
	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Content-Type", rb.contentType)
	if gated {
		hd.Set("Cache-Control", "private, no-store, max-age=0, must-revalidate")
		hd.Set("Vary", "Cookie")
	} else {
		hd.Set("Cache-Control", servehttp.CacheControlFor(rb.servedPath))
	}
	if rb.hasLen {
		hd.Set("Content-Length", strconv.FormatInt(rb.contentLen, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rb.body)
	}
}

// handleAuthzCallback handles GET /__authz/callback?token=&next=: re-verify the
// ?token= fully against THIS route, Set-Cookie __Host-edge, then 302 to the safe
// same-host next. On ANY failure 302 back to the /authz exchange (fail closed).
func (h *Handler) handleAuthzCallback(w http.ResponseWriter, r *http.Request, rt *Route, host string) {
	q := r.URL.Query()
	token := q.Get("token")
	nextPath := route.SafeNextPath(q.Get("next"))

	ok := false
	if token != "" && h.verifier != nil {
		_, ok = h.verifier.Verify(r.Context(), token, host, rt.SiteID, rt.AccessMode, rt.OrgID)
	}
	if !ok {
		h.redirectToAuthz(w, r, host)
		return
	}

	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Cache-Control", "private, no-store, max-age=0, must-revalidate")
	hd.Set("Vary", "Cookie")
	hd.Set("Set-Cookie", h.edgeCookie(token))
	hd.Set("Location", h.contentURL(host, nextPath))
	w.WriteHeader(http.StatusFound)
}

// contentURL builds an absolute URL on the content host using the configured PUBLIC
// scheme + optional port (CONTENT_SCHEME / CONTENT_PORT). The bare host carries no
// port — serve routes by the Host header and strips it — so the externally-visible
// scheme/port come from config, not the request: production is https on the default
// port; a local http self-host is http://<host>:8090. pathAndQuery is appended as-is
// (already a safe, percent-encoded same-host path). Without this the gated callback
// would hardcode https on the default port and bounce a local browser to :443.
func (h *Handler) contentURL(host, pathAndQuery string) string {
	scheme := h.cfg.ContentScheme
	if scheme == "" {
		scheme = "https"
	}
	u := scheme + "://" + host
	if h.cfg.ContentPort != "" {
		u += ":" + h.cfg.ContentPort
	}
	return u + pathAndQuery
}

// redirectToAuthz 302s an unauthenticated / invalid-token gated request to the
// dashboard /authz exchange with host + a safe next (no-store).
func (h *Handler) redirectToAuthz(w http.ResponseWriter, r *http.Request, host string) {
	nextPath := route.SafeNextPath(rawPathAndQuery(r))
	u, err := url.Parse(h.cfg.AppAuthzURL)
	if err != nil {
		// A misconfigured authz URL must not serve content; fail closed to 404 (gated
		// context ⇒ private/no-store, never a publicly cacheable gated response).
		h.notFound(w, r, nil, nil, true)
		return
	}
	qs := u.Query()
	qs.Set("host", host)
	qs.Set("next", nextPath)
	u.RawQuery = qs.Encode()

	hd := w.Header()
	servehttp.ApplyHeaders(hd, servehttp.ContentSecurityHeaders())
	hd.Set("Cache-Control", "private, no-store, max-age=0, must-revalidate")
	hd.Set("Vary", "Cookie")
	hd.Set("Location", u.String())
	w.WriteHeader(http.StatusFound)
}

// edgeCookieName is the edge-token cookie name for the configured content scheme:
// the secure __Host- cookie over https, the plain unprefixed cookie over local http.
func (h *Handler) edgeCookieName() string {
	if h.cfg.ContentScheme == "http" {
		return edgeCookieNameInsecure
	}
	return edgeCookieNameSecure
}

// readEdgeCookie parses the edge-token cookie value, or "" when absent/empty.
func (h *Handler) readEdgeCookie(r *http.Request) string {
	c, err := r.Cookie(h.edgeCookieName())
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// edgeCookie builds the host-only Set-Cookie value for the edge token. Over https the
// __Host- prefix forces (browser-enforced) Secure + Path=/ + no Domain; HttpOnly keeps
// it out of tenant JS; SameSite=Lax carries it on the dashboard callback nav. Over a
// local http origin (CONTENT_SCHEME=http) the Secure attribute + __Host- prefix are
// dropped — a Secure cookie can't be set over http — keeping only HttpOnly + host-only.
func (h *Handler) edgeCookie(token string) string {
	c := h.edgeCookieName() + "=" + token +
		"; Path=/; HttpOnly; SameSite=Lax; Max-Age=" + strconv.Itoa(edgeCookieMaxAge)
	if h.cfg.ContentScheme != "http" {
		c += "; Secure"
	}
	return c
}

// pathOf returns the request URL pathname in its RAW, still-percent-encoded form
// (r.URL.EscapedPath), mirroring the Worker's url.pathname. This is critical: the
// Worker's cleanPath / safeNextPath decode EXACTLY ONCE, so they must receive the
// encoded path. Go's r.URL.Path is already decoded — feeding it to CleanPath would
// double-decode and could let a %252e%252e ("%2e%2e" → "..") traversal slip past.
// EscapedPath keeps the original encoding so our decode-once matches byte-for-byte.
func pathOf(r *http.Request) string {
	if r.URL != nil {
		return r.URL.EscapedPath()
	}
	return "/"
}

// rawPathAndQuery returns the raw pathname + "?" + raw query for the `next` hint
// (safe-normalized later by SafeNextPath, which decodes once — see pathOf).
func rawPathAndQuery(r *http.Request) string {
	if r.URL == nil {
		return "/"
	}
	p := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		return p + "?" + r.URL.RawQuery
	}
	return p
}
