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

// Middleware derives a per-request logger from base, tags it with the chi
// request id (set by chimiddleware.RequestID, which must run before this), and
// stores it in the request context. It logs one structured line per request at
// completion with method, path, status, and byte count.
//
// It must be mounted AFTER chi's RequestID middleware so the id is present.
func Middleware(base *slog.Logger) func(http.Handler) http.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := chimw.GetReqID(r.Context())
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
