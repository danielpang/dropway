// Package errtrack is Dropway's vendor-neutral error-tracking seam. It is the
// error-reporting analogue of the quota.Provider / projection.Writer /
// customdomains.Provider pattern: a small interface plus a no-op default, with a
// concrete client selected at runtime by environment.
//
// The shipped client is PostHog (posthog.go), but a self-hoster can point the
// same seam at Datadog / Sentry / an OTLP collector by implementing Reporter and
// Register-ing a constructor under their own ERROR_TRACKING_PROVIDER name — no
// call site changes. The default (no provider configured) is Noop, so the OSS /
// self-host build sends nothing unless explicitly wired.
//
// Coverage model (how "any error, caught or uncaught" reaches the sink):
//
//   - Logged errors: WrapSlogHandler decorates the service's base slog.Handler so
//     every record at/above Error is mirrored to the sink as an exception. Because
//     httpx.WriteError and the handlers already slog.Error their failures, this is
//     the broad net — no edits to those call sites.
//   - Panics: Recoverer recovers, logs via slog.Error (so the wrapped handler
//     captures it), and writes a 500.
//   - Background goroutines: SafeGo runs fn with the same recover→slog.Error guard.
//   - Anything else: call Reporter.CaptureException directly for a handled error
//     you want reported without (or in addition to) logging.
package errtrack

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/posthog/posthog-go"
)

// Reporter is the vendor-neutral error sink. Implementations must be safe for
// concurrent use and must never panic — error reporting is best-effort and must
// not take down the path that produced the error.
type Reporter interface {
	// CaptureException reports err to the sink with optional extra properties. The
	// context may carry a distinct id (see WithDistinctID) used to attribute the
	// exception to a user. A nil err is ignored.
	CaptureException(ctx context.Context, err error, props map[string]any)

	// WrapSlogHandler decorates base so records at/above the provider's capture
	// level are mirrored to the sink as exceptions, while still being passed
	// through to base. Noop returns base unchanged.
	WrapSlogHandler(base slog.Handler) slog.Handler

	// Close flushes any buffered events. Call once on shutdown.
	Close()
}

// Noop is the default Reporter: it sends nothing. Used when no provider is
// configured (OSS / self-host without error tracking).
type Noop struct{}

func (Noop) CaptureException(context.Context, error, map[string]any) {}
func (Noop) WrapSlogHandler(base slog.Handler) slog.Handler         { return base }
func (Noop) Close()                                                 {}

// ---- distinct-id context plumbing ------------------------------------------

type distinctIDKey struct{}

// WithDistinctID attaches a distinct id (typically the acting user's id) to ctx
// so exceptions captured downstream — via the slog bridge, Recoverer, or
// CaptureException — are attributed to that user. An empty id is a no-op.
func WithDistinctID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, distinctIDKey{}, id)
}

// DistinctID returns the distinct id attached with WithDistinctID, or "" if none.
func DistinctID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(distinctIDKey{}).(string); ok {
		return id
	}
	return ""
}

// ---- provider registry + env selection -------------------------------------

// Constructor builds a Reporter from Options. It returns an error when the
// provider can't be configured (e.g. a missing key); FromEnv then falls back to
// Noop so a misconfiguration never crashes the service.
type Constructor func(Options) (Reporter, error)

// Options carry shared metadata stamped onto every reported exception.
type Options struct {
	Service     string // logical service name, e.g. "api", "serve", "mcp"
	Environment string // deployment label, e.g. "production"
	// SharedPostHogClient, when non-nil, is a posthog-go client the caller already
	// built (e.g. shared with product analytics). The posthog provider reuses it
	// instead of constructing its own — one client per process. The provider then
	// BORROWS the client and must not close it; the caller owns its lifecycle.
	SharedPostHogClient posthog.Client
}

// Option customizes FromEnv.
type Option func(*envOptions)

type envOptions struct {
	sharedPostHogClient posthog.Client
}

// WithSharedPostHogClient lends FromEnv an already-built posthog-go client so the
// posthog provider reuses it instead of creating a second one. Use this when the
// process also emits product analytics (internal/analytics) over the same client.
// The caller retains ownership and must Close the client itself.
func WithSharedPostHogClient(client posthog.Client) Option {
	return func(o *envOptions) { o.sharedPostHogClient = client }
}

var registry = map[string]Constructor{}

// Register makes a provider available to FromEnv under name. Built-in providers
// register in their own init(); self-hosters can call Register from their main
// before FromEnv to add a custom sink. Not safe for concurrent use — call during
// init / before FromEnv.
func Register(name string, c Constructor) {
	registry[strings.ToLower(name)] = c
}

// FromEnv selects a Reporter for service from the environment and returns it
// alongside a human-readable label for startup logging. It never returns nil.
//
//	ERROR_TRACKING_PROVIDER  provider name (e.g. "posthog"); "none"/"off" disables.
//	                         When unset, defaults to "posthog" if a PostHog key is
//	                         present, else "none".
//	ENVIRONMENT              deployment label stamped onto every exception.
//
// A misconfigured or unknown provider degrades to Noop with a warning rather than
// failing startup — error tracking must never be load-bearing.
//
// Pass WithSharedPostHogClient to reuse an existing posthog-go client (shared with
// product analytics) instead of building a second one.
func FromEnv(service string, opts ...Option) (Reporter, string) {
	var eo envOptions
	for _, o := range opts {
		o(&eo)
	}
	env := strings.TrimSpace(os.Getenv("ENVIRONMENT"))
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("ERROR_TRACKING_PROVIDER")))
	if provider == "" {
		if posthogKey() != "" {
			provider = "posthog"
		} else {
			provider = "none"
		}
	}
	switch provider {
	case "none", "noop", "off", "disabled":
		return Noop{}, "none"
	}
	c, ok := registry[provider]
	if !ok {
		slog.Warn("errtrack: unknown ERROR_TRACKING_PROVIDER — error tracking disabled", "provider", provider)
		return Noop{}, "none (unknown provider: " + provider + ")"
	}
	rep, err := c(Options{Service: service, Environment: env, SharedPostHogClient: eo.sharedPostHogClient})
	if err != nil {
		slog.Warn("errtrack: provider init failed — error tracking disabled", "provider", provider, "err", err)
		return Noop{}, "none (" + provider + " init failed)"
	}
	return rep, provider
}

// ---- HTTP panic recovery + background-goroutine guard -----------------------

// Recoverer is HTTP middleware that recovers panics, logs them via slog.Error
// (so a WrapSlogHandler-wrapped default logger mirrors them to the sink, carrying
// the stack as a property), and writes an opaque 500. It is a drop-in replacement
// for chi's middleware.Recoverer that also feeds error tracking. It deliberately
// does NOT take a Reporter: capture flows through the wrapped default logger, so
// it works for any provider and never double-reports.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler is the stdlib's intentional abort signal; the
			// server suppresses it. Re-panic so we don't convert it into a 500.
			if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
				panic(rec)
			}
			err := asError(rec)
			slog.ErrorContext(r.Context(), "panic recovered",
				"err", err,
				"stack", string(debug.Stack()),
				"path", r.URL.Path,
				"method", r.Method,
				"panic", true,
			)
			// The status line may already be committed by a partial write; only
			// then can we not send a clean 500. Best-effort either way.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal_error"}`))
		}()
		next.ServeHTTP(w, r)
	})
}

// SafeGo runs fn in a new goroutine with a recover guard: a panic is logged via
// slog.Error (mirrored to the sink by a wrapped default logger) instead of
// crashing the process. name labels the goroutine in the log/exception. ctx is
// used for distinct-id attribution and is passed to the error log.
func SafeGo(ctx context.Context, name string, fn func()) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(ctx, "background goroutine panic",
					"err", asError(rec),
					"stack", string(debug.Stack()),
					"goroutine", name,
					"panic", true,
				)
			}
		}()
		fn()
	}()
}

// asError coerces a recovered panic value to an error.
func asError(rec any) error {
	if err, ok := rec.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", rec)
}
