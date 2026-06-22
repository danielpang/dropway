// PostHog client for the errtrack seam. It is the shipped Reporter, selected at
// runtime when ERROR_TRACKING_PROVIDER=posthog (or, by default, whenever a
// PostHog key is present). It wraps the official posthog-go SDK:
//
//   - WrapSlogHandler uses the SDK's SlogCaptureHandler, which mirrors slog
//     records at/above Error to PostHog as $exception events. It pulls the
//     description from the "err"/"error" log attr — exactly Dropway's logging
//     convention (slog.Error("...", "err", err)) — so logged failures become
//     error-tracking issues with no call-site changes.
//   - CaptureException builds a $exception via NewDefaultException for handled
//     errors a caller wants reported explicitly.
//
// Lives in internal/ (OSS) and is runtime-selected, consistent with the other
// vendor clients here (projection/cloudflare.go, customdomains/cloudflare.go).
package errtrack

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/posthog/posthog-go"

	"github.com/danielpang/dropway/internal/phclient"
)

func init() {
	Register("posthog", newPostHogReporter)
}

// posthogKey returns the PostHog project key (phc_…) from POSTHOG_KEY — the same
// var the edge worker and dashboard already use, so all services share one secret.
func posthogKey() string {
	return strings.TrimSpace(os.Getenv("POSTHOG_KEY"))
}

// newPostHogReporter constructs the PostHog Reporter.
//
// When the caller lent a shared client (Options.SharedPostHogClient, e.g. the API
// shares one client with product analytics), the reporter BORROWS it: it does not
// build a second client and does not close it on shutdown. Otherwise it builds and
// OWNS its own client via phclient, and Close() drains it. Errors (→ FromEnv falls
// back to Noop) when no shared client is given and no key is configured.
func newPostHogReporter(opts Options) (Reporter, error) {
	if opts.SharedPostHogClient != nil {
		return &posthogReporter{
			client:  opts.SharedPostHogClient,
			service: opts.Service,
			env:     opts.Environment,
			owns:    false,
		}, nil
	}
	client, err := phclient.New(phclient.ConfigFromEnv())
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("POSTHOG_KEY not set")
	}
	return &posthogReporter{client: client, service: opts.Service, env: opts.Environment, owns: true}, nil
}

type posthogReporter struct {
	client  posthog.Client
	service string
	env     string
	// owns reports whether this reporter built the client (and must close it) or
	// borrowed a shared one (the caller closes it).
	owns bool
}

// systemDistinctID attributes exceptions with no acting user (background work,
// startup, infra). PostHog's SDK drops a capture when the distinct id is empty,
// so we always supply this fallback.
const systemDistinctID = "system"

func (p *posthogReporter) distinctID(ctx context.Context) string {
	if id := DistinctID(ctx); id != "" {
		return id
	}
	return systemDistinctID
}

func (p *posthogReporter) CaptureException(ctx context.Context, err error, props map[string]any) {
	if err == nil {
		return
	}
	ex := posthog.NewDefaultException(time.Now(), p.distinctID(ctx), exceptionType(err), err.Error())
	properties := posthog.NewProperties()
	properties.Set("service", p.service)
	properties.Set("environment", p.env)
	for k, v := range props {
		properties.Set(k, v)
	}
	ex.Properties = properties
	_ = posthog.EnqueueWithContext(ctx, p.client, ex)
}

func (p *posthogReporter) WrapSlogHandler(base slog.Handler) slog.Handler {
	return posthog.NewSlogCaptureHandler(base, p.client,
		// Capture Error and above as exceptions; lower levels still log normally.
		posthog.WithMinCaptureLevel(slog.LevelError),
		posthog.WithDistinctIDFn(func(ctx context.Context, _ slog.Record) string {
			return p.distinctID(ctx)
		}),
		posthog.WithPropertiesFn(func(ctx context.Context, r slog.Record) posthog.Properties {
			// Copy every log attr (incl. request_id from logx and the "err" value)
			// onto the exception, then stamp service + environment.
			props := posthog.SlogAttrsAsProperties(ctx, r)
			props.Set("service", p.service)
			props.Set("environment", p.env)
			return props
		}),
	)
}

// Close drains the client only when this reporter owns it. A borrowed shared
// client is closed by its owner (e.g. the API main), so closing here would be a
// double-close.
func (p *posthogReporter) Close() {
	if p.owns && p.client != nil {
		_ = p.client.Close()
	}
}

// exceptionType renders a stable title for the error-tracking issue: the error's
// concrete Go type (e.g. "*pgconn.PgError"), falling back to "error".
func exceptionType(err error) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	return t.String()
}
