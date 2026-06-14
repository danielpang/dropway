// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package logx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// TestMiddleware_PropagatesAndEchoesRequestID asserts that an INBOUND X-Request-Id
// is honored (chi RequestID reuses it) and ECHOED on the response, and that the
// per-request logger + context request id reflect the same value — the end-to-end
// correlation hook (ARCHITECTURE.md §2.3).
func TestMiddleware_PropagatesAndEchoesRequestID(t *testing.T) {
	const inbound = "edge-trace-abc123"

	var seen string
	final := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	})

	// Mirror the production chain: chi RequestID then logx.Middleware.
	h := chimw.RequestID(Middleware(nil)(final))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(RequestIDHeader, inbound)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if seen != inbound {
		t.Errorf("handler saw request id %q, want inbound %q (not propagated)", seen, inbound)
	}
	if got := rr.Header().Get(RequestIDHeader); got != inbound {
		t.Errorf("response %s = %q, want %q (not echoed)", RequestIDHeader, got, inbound)
	}
}

// TestValidRequestID asserts the allowlist accepts safe correlation ids and rejects
// forgeable / oversized ones (the log/audit-forgery guard).
func TestValidRequestID(t *testing.T) {
	good := []string{
		"edge-trace-abc123",
		"ABC_def.123-XYZ",
		"0",
		strings.Repeat("a", maxRequestIDLen), // exactly the cap is fine
	}
	for _, s := range good {
		if !validRequestID(s) {
			t.Errorf("validRequestID(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",                                     // empty
		"has space",                            // space
		"line\ninjection",                      // newline → log forgery
		"carriage\rreturn",                     // CR
		"tab\tchar",                            // control char
		"null\x00byte",                         // NUL
		"semi;colon",                           // outside the charset
		"slash/path",                           // outside the charset
		"emoji😀",                               // non-ASCII
		strings.Repeat("a", maxRequestIDLen+1), // over the length cap
	}
	for _, s := range bad {
		if validRequestID(s) {
			t.Errorf("validRequestID(%q) = true, want false", s)
		}
	}
}

// TestSanitizeRequestID_DropsForgedHonorsValid asserts the sanitizer strips a
// forgeable inbound X-Request-Id (so chi mints a fresh, safe one) while passing a
// well-formed inbound id through untouched (end-to-end correlation preserved).
func TestSanitizeRequestID_DropsForgedHonorsValid(t *testing.T) {
	// Mirror the production chain: SanitizeRequestID → chi.RequestID → logx.Middleware.
	chain := func(final http.Handler) http.Handler {
		return SanitizeRequestID(chimw.RequestID(Middleware(nil)(final)))
	}

	// (1) A forged id with an injected newline must NOT survive — a fresh id is used.
	var seen string
	h := chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))
	const forged = "abc\ninjected fake log line"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(RequestIDHeader, forged)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if seen == forged || strings.ContainsAny(seen, "\r\n") {
		t.Errorf("forged X-Request-Id survived sanitization: handler saw %q", seen)
	}
	if seen == "" {
		t.Error("a fresh request id should have been generated for the forged input")
	}
	if echoed := rr.Header().Get(RequestIDHeader); strings.ContainsAny(echoed, "\r\n") {
		t.Errorf("forged id echoed back unsanitized: %q", echoed)
	}

	// (2) A well-formed inbound id is honored verbatim (correlation preserved).
	const good = "edge-trace-abc123"
	h2 := chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.Header.Set(RequestIDHeader, good)
	rr2 := httptest.NewRecorder()
	h2.ServeHTTP(rr2, req2)
	if seen != good {
		t.Errorf("valid inbound id not honored: handler saw %q, want %q", seen, good)
	}
	if echoed := rr2.Header().Get(RequestIDHeader); echoed != good {
		t.Errorf("valid inbound id not echoed: %q, want %q", echoed, good)
	}
}

// TestMiddleware_GeneratesRequestIDWhenAbsent asserts a request id is generated and
// echoed when the caller sends none.
func TestMiddleware_GeneratesRequestIDWhenAbsent(t *testing.T) {
	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	h := chimw.RequestID(Middleware(nil)(final))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rr.Header().Get(RequestIDHeader) == "" {
		t.Error("expected a generated X-Request-Id on the response")
	}
}
