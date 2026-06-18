// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package serve is the request lifecycle of the self-host content server — a
// faithful Go port of edge/serving-worker/src/index.ts serve(). The ORDER of the
// checks below is security-load-bearing; do not reorder. It is the system's
// security boundary, so every check fails CLOSED unless the Worker fails OPEN
// (rate limit + org status only).
package serve

import (
	"context"
	"time"

	"github.com/danielpang/dropway/services/serve/internal/route"
)

// Route is the resolved routing identity for a host: the Worker's RouteValue
// equivalent, derived solely from the authoritative resolver (never client input).
// VersionID is the live current_version_id (a host with no live version must not
// resolve). ExpiresAt is the public/unlisted edge link-expiry (nil ⇒ never).
type Route struct {
	OrgID      string
	SiteID     string
	VersionID  string
	AccessMode string
	ExpiresAt  *time.Time
}

// RouteValue projects the resolved Route onto the cross-language route contract so
// the expiry/parse helpers (route.IsRouteExpired) and the cache-key folding read a
// single shape.
func (r Route) RouteValue() route.RouteValue {
	rv := route.RouteValue{
		OrgID:         r.OrgID,
		SiteID:        r.SiteID,
		VersionID:     r.VersionID,
		AccessMode:    r.AccessMode,
		SchemaVersion: route.SupportedSchemaVersion,
	}
	if r.ExpiresAt != nil {
		rv.ExpiresAt = r.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return rv
}

// RouteResolver resolves a normalized host to its Route. It MUST return
// ErrHostNotFound for an unknown host OR a host with no live version (fail closed
// ⇒ 404). Any other error is a backend failure (the handler 404s, never leaks a
// 5xx that exposes structure). Implemented over Postgres app.resolve_host by the
// store adapter (non-BYPASSRLS dropway_app role).
type RouteResolver interface {
	Resolve(ctx context.Context, normalizedHost string) (Route, error)
}

// ErrHostNotFound is the resolver's "unknown host / no live version" sentinel.
// Implementations should return this (or wrap store.ErrHostNotFound) so the
// handler maps it to a fail-closed 404 distinctly from a backend error.
type hostNotFoundError struct{}

func (hostNotFoundError) Error() string { return "serve: host not found" }

// ErrHostNotFound is returned by a RouteResolver for an unknown host or a host
// with no live version.
var ErrHostNotFound error = hostNotFoundError{}

// OrgStatusReader reads the per-org suspension / over-limit status. It returns
// "suspended" or "over_limit" to BLOCK content (503), anything else (incl. "") to
// serve. It FAILS OPEN: a nil reader or any read error ⇒ serve (the Go API +
// billing remain authoritative). orgID comes from the resolved route.
type OrgStatusReader interface {
	OrgStatus(ctx context.Context, orgID string) (status string, err error)
}
