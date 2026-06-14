// Package logx provides structured, request-correlated logging for the Go API
// (docs/ARCHITECTURE.md §2.3 Observability: "request_id correlated edge→Go→
// Postgres"). It is a thin wrapper over the standard library's log/slog: the
// HTTP layer stashes a per-request *slog.Logger (already tagged with the chi
// request id) in the context, and handlers/stores retrieve it with FromContext.
//
// Keeping this dependency-light (slog only) means the core never pulls a logging
// framework, and the same logger threads through the store and projection writers
// so a single request_id ties the publish flow together end to end.
package logx

import (
	"context"
	"log/slog"
	"net/http"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// ctxKey is the unexported context key under which the request logger is stored.
type ctxKey struct{}

// FromContext returns the request-scoped logger, or slog.Default() if none was
// installed (so callers never have to nil-check).
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// WithLogger returns a copy of ctx carrying l as the request logger.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// RequestIDHeader is the HTTP header that carries the request correlation id. The
// Go API HONORS an inbound value (chi's RequestID middleware reuses it) and ECHOES
// it on every response, so a caller / the edge Worker can propagate one id across
// the whole edge→Go→Postgres path (ARCHITECTURE.md §2.3). It matches
// chimiddleware.RequestIDHeader's default.
const RequestIDHeader = "X-Request-Id"

// maxRequestIDLen caps the accepted inbound X-Request-Id length. A correlation id
// is short; anything longer is rejected (replaced with a generated id) to bound the
// bytes that land in every log line and audit row.
const maxRequestIDLen = 128

// validRequestID reports whether s is a safe inbound correlation id: a NON-EMPTY,
// length-bounded string drawn only from an unambiguous, control-character-free
// charset (ASCII alphanumerics plus '-', '_', '.'). This is the allowlist that
// blocks LOG / AUDIT FORGERY: a client-supplied X-Request-Id is written verbatim
// into structured logs and audit rows, so an injected newline or control char could
// forge a fake log line or split a record. Anything outside this set is rejected and
// a fresh id is generated instead (SanitizeRequestID).
func validRequestID(s string) bool {
	if s == "" || len(s) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

// SanitizeRequestID is middleware that must run BEFORE chi's RequestID middleware.
// chi REUSES an inbound X-Request-Id verbatim; since that value flows unmodified
// into structured logs and audit rows, an attacker could inject newlines / control
// chars to forge or split log/audit records. This middleware STRIPS an inbound
// header that fails the bounded-charset/length allowlist (validRequestID), so chi
// then generates a fresh, safe id. A well-formed inbound id is left untouched, so
// legitimate edge→Go→Postgres correlation still works end to end.
func SanitizeRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get(RequestIDHeader); v != "" && !validRequestID(v) {
			// Untrusted/forgeable value → drop it so chi.RequestID mints a fresh id.
			r.Header.Del(RequestIDHeader)
		}
		next.ServeHTTP(w, r)
	})
}

// RequestIDFromContext returns the per-request correlation id (set by chi's
// RequestID middleware, honoring an inbound X-Request-Id). Empty when unset.
func RequestIDFromContext(ctx context.Context) string {
	return chimw.GetReqID(ctx)
}

// Middleware derives a per-request logger from base, tags it with the chi
// request id (set by chimiddleware.RequestID, which must run before this), stores
// it in the request context, AND echoes the id on the response (X-Request-Id) so
// the value is observable end to end. It logs one structured line per request at
// completion with method, path, status, byte count, and the request id.
//
// It must be mounted AFTER chi's RequestID middleware so the id is present.
func Middleware(base *slog.Logger) func(http.Handler) http.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := chimw.GetReqID(r.Context())
			// Echo the correlation id back so the caller/edge can tie its logs to
			// ours. Set BEFORE ServeHTTP so it lands even if the handler writes the
			// status early. trace_id mirrors request_id (cheap tracing hook; no OTel).
			if reqID != "" {
				w.Header().Set(RequestIDHeader, reqID)
			}
			l := base.With("request_id", reqID)
			ctx := WithLogger(r.Context(), l)

			ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r.WithContext(ctx))

			l.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"bytes", ww.bytes,
			)
		})
	}
}

// statusRecorder captures the response status and size for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}
