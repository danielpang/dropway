// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package apikeygrant decides what an already-authenticated org API key may do.
// Human sessions are not subject to it. Project rooms are session-only.
// A null site_id keeps the legacy full-key behavior for keys that carry sites:*.
package apikeygrant

import (
	"errors"
	"net/http"
	"path"
	"strings"
)

const (
	ReasonRooms = "project_rooms_session_only"
	ReasonScope = "scope"
	ReasonSite  = "site_id"
)

// Denial is a forbidden key use. Reason is a stable code.
type Denial struct{ Reason string }

func (d *Denial) Error() string { return "apikeygrant: " + d.Reason }

// Decide reports whether a keyed request may proceed. paths are the raw URL path.
func Decide(method, rawPath string, scopes []string, siteID *string) error {
	p := path.Clean("/" + strings.TrimPrefix(rawPath, "/"))
	if p == "/v1/me" {
		return nil
	}
	if strings.HasPrefix(p, "/v1/project-rooms") || p == "/v1/orgs/project-rooms" {
		return &Denial{Reason: ReasonRooms}
	}
	if isSitePath(p) {
		if !scopeAllows(method, scopes) {
			return &Denial{Reason: ReasonScope}
		}
		if siteID != nil && *siteID != "" {
			id := sitePathID(p)
			if id == "" || id != *siteID {
				return &Denial{Reason: ReasonSite}
			}
		}
		return nil
	}
	if siteID != nil && *siteID != "" {
		return &Denial{Reason: ReasonSite}
	}
	if has(scopes, "sites:*") {
		return nil
	}
	return &Denial{Reason: ReasonScope}
}

func isSitePath(p string) bool {
	return p == "/v1/sites" || strings.HasPrefix(p, "/v1/sites/")
}

func sitePathID(p string) string {
	rest := strings.TrimPrefix(p, "/v1/sites/")
	if rest == "" || rest == p {
		return ""
	}
	id, _, _ := strings.Cut(rest, "/")
	return id
}

func scopeAllows(method string, scopes []string) bool {
	if has(scopes, "sites:*") {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return has(scopes, "sites:read")
	default:
		return has(scopes, "sites:write")
	}
}

func has(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// ErrDenied is the type callers errors.As.
var ErrDenied = errors.New("apikeygrant denied")
