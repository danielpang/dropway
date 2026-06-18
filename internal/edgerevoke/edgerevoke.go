// Package edgerevoke defines the hard-revocation denylist contract shared by the
// Go API (the only writer) and the serving Worker + the /authz exchange (readers).
//
// The common revocation case is just the short edge-token TTL (≤15m), minted from
// the revocable Better Auth session. Hard revocation makes it IMMEDIATE by writing a
// per-subject min_iat into a Cloudflare KV denylist that the Worker + /authz check
// on every gated request. There are exactly THREE token-revocation triggers: member
// removal, site unshare / access tightened, and allow_external_sharing disabled:
//
//	revoked:user:<userId>   → { "min_iat": <unix seconds> }   all that user's edge tokens issued before min_iat are invalid
//	revoked:site:<siteId>   → { "min_iat": ... }              unshare / access tightened
//	revoked:org:<orgId>     → { "min_iat": ... }              org-wide (allow_external_sharing disabled)
//
// Billing suspension / over_limit is NOT a token revocation: it would hard-cut
// existing viewers, contradicting the read-only over_limit model. It instead
// sets the per-org org_status:<orgId> KV flag (projection.OrgStatusWriter) and the
// edge serves a read-only platform block page — existing tokens stay valid.
//
// For an edge token with claims {sub, site_id, org}, a reader REJECTS (→ 302 to
// /authz to re-auth) if ANY of revoked:user:<sub> / revoked:site:<site_id> /
// revoked:org:<org> has min_iat > token.iat. Writes are IDEMPOTENT — a write takes
// max(existing, new) min_iat, so a denylist only ever tightens. The denylist is
// REBUILDABLE: a stale/lost key fails CLOSED (an extra re-auth), never opens access.
//
// This package is deliberately dependency-light (stdlib only) so the writer
// implementations (internal/projection) and the API store/handlers can share the
// key scheme + value shape without importing each other.
package edgerevoke

import "fmt"

// Kind is the subject class a denylist key revokes.
type Kind string

const (
	// KindUser revokes all of a user's edge tokens issued before min_iat (member
	// removal, ban).
	KindUser Kind = "user"
	// KindSite revokes all edge tokens for a site issued before min_iat (unshare /
	// access tightened).
	KindSite Kind = "site"
	// KindOrg revokes all edge tokens for an org issued before min_iat
	// (allow_external_sharing disabled). NOT used for billing suspension/over_limit,
	// which is the read-only org_status KV flag, not a token revocation.
	KindOrg Kind = "org"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindUser, KindSite, KindOrg:
		return true
	default:
		return false
	}
}

// KeyPrefix is the namespace prefix for every denylist key in the (shared ROUTES)
// KV. It is distinct from the "route:" prefix so the denylist can reuse the same
// namespace without colliding with route values.
const KeyPrefix = "revoked:"

// Key returns the KV key for a (kind, id) denylist entry, e.g.
// "revoked:user:<id>". The Worker + /authz build the same key to look it up.
func Key(kind Kind, id string) string {
	return KeyPrefix + string(kind) + ":" + id
}

// Value is the denylist entry stored at Key(kind, id): every edge token for that
// subject with iat < MinIAT is invalid. JSON shape is fixed by the cross-language
// contract (the Worker parses `min_iat`); keep the tag stable.
type Value struct {
	MinIAT int64 `json:"min_iat"`
}

// Validate checks the value is well-formed (a positive unix timestamp) before it
// can be written, so a malformed denylist entry can never be pushed to the edge.
func (v Value) Validate() error {
	if v.MinIAT <= 0 {
		return fmt.Errorf("edgerevoke: min_iat must be positive, got %d", v.MinIAT)
	}
	return nil
}
