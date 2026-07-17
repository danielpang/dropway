// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package route

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"

	"github.com/danielpang/dropway/internal/projection"
)

// RouteValue is the in-process route type. It reuses the cross-language contract
// struct from internal/projection so field names/JSON tags stay in lock-step with
// kv-route.schema.json (no second divergent struct).
type RouteValue = projection.RouteValue

// SupportedSchemaVersion / MinSupportedSchemaVersion pin the accepted route-value
// schema range, reusing the projection constants so the pin stays in lock-step
// with the Go API writer + the Worker.
const (
	SupportedSchemaVersion    = projection.SchemaVersion    // 4
	MinSupportedSchemaVersion = projection.MinSchemaVersion // 1
)

var uuidRE = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var allowedRouteKeys = map[string]struct{}{
	"org_id":         {},
	"site_id":        {},
	"version_id":     {},
	"access_mode":    {},
	"schema_version": {},
	"expires_at":     {},
	"plan_tier":      {},
	"chat_id":        {},
}

// ParseRouteValue validates an untrusted JSON route value into a RouteValue,
// returning ok=false on ANY shape/version mismatch so callers fail closed (404).
// It is a faithful port of contracts/src/index.ts parseKVRouteValue:
//
//   - input must be a JSON object (not null/array),
//   - REJECT any key outside the allowed set (additionalProperties:false drift),
//   - org_id/site_id/version_id must be UUID strings,
//   - access_mode must be one of the enum,
//   - schema_version must be an integer in [MIN, SUPPORTED],
//   - expires_at optional; if present (non-null) must be an RFC3339 timestamp,
//   - plan_tier optional (v3+); if present (non-null) must be a string.
func ParseRouteValue(raw []byte) (RouteValue, bool) {
	var generic map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil || generic == nil {
		return RouteValue{}, false
	}
	// Reject a trailing token (e.g. an array/second value) just like a strict parse.
	if dec.More() {
		return RouteValue{}, false
	}

	// Reject unknown keys (the drift tripwire).
	for k := range generic {
		if _, ok := allowedRouteKeys[k]; !ok {
			return RouteValue{}, false
		}
	}

	var out RouteValue

	for _, k := range []struct {
		name string
		dst  *string
	}{
		{"org_id", &out.OrgID},
		{"site_id", &out.SiteID},
		{"version_id", &out.VersionID},
	} {
		raw, present := generic[k.name]
		if !present {
			return RouteValue{}, false
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || !uuidRE.MatchString(s) {
			return RouteValue{}, false
		}
		*k.dst = s
	}

	// access_mode enum.
	amRaw, present := generic["access_mode"]
	if !present {
		return RouteValue{}, false
	}
	var am string
	if err := json.Unmarshal(amRaw, &am); err != nil {
		return RouteValue{}, false
	}
	switch am {
	case projection.AccessPublic, projection.AccessPassword,
		projection.AccessAllowlist, projection.AccessOrgOnly:
	default:
		return RouteValue{}, false
	}
	out.AccessMode = am

	// schema_version: integer in [MIN, SUPPORTED].
	svRaw, present := generic["schema_version"]
	if !present {
		return RouteValue{}, false
	}
	var num json.Number
	if err := json.Unmarshal(svRaw, &num); err != nil {
		return RouteValue{}, false
	}
	sv, err := num.Int64()
	if err != nil {
		return RouteValue{}, false // non-integer (e.g. 1.5) → reject
	}
	if sv < MinSupportedSchemaVersion || sv > SupportedSchemaVersion {
		return RouteValue{}, false
	}
	out.SchemaVersion = int(sv)

	// expires_at: optional RFC3339 string (null/absent → empty).
	if eaRaw, present := generic["expires_at"]; present {
		if string(eaRaw) != "null" {
			var ea string
			if err := json.Unmarshal(eaRaw, &ea); err != nil {
				return RouteValue{}, false
			}
			if _, err := time.Parse(time.RFC3339, ea); err != nil {
				return RouteValue{}, false
			}
			out.ExpiresAt = ea
		}
	}

	// plan_tier: optional string (v3+; null/absent → empty). The serve service does
	// not act on it — the free-tier banner is an edge-Worker feature — but it is
	// parsed (and rejected if non-string) so this stays a faithful port of the
	// contract and a v3 value carrying it isn't tripped by the unknown-key guard.
	if ptRaw, present := generic["plan_tier"]; present {
		if string(ptRaw) != "null" {
			var pt string
			if err := json.Unmarshal(ptRaw, &pt); err != nil {
				return RouteValue{}, false
			}
			out.PlanTier = pt
		}
	}

	// chat_id: optional UUID string (v4+; null/absent → empty) — the site's
	// attached "How this was made" chat log. The serve service does not act on
	// it (the panel is an edge-Worker feature), but it is parsed (and rejected
	// if malformed) so this stays a faithful port of the contract.
	if ciRaw, present := generic["chat_id"]; present {
		if string(ciRaw) != "null" {
			var ci string
			if err := json.Unmarshal(ciRaw, &ci); err != nil || !uuidRE.MatchString(ci) {
				return RouteValue{}, false
			}
			out.ChatID = ci
		}
	}

	return out, true
}

// IsRouteExpired reports whether a route has expired as of now, mirroring
// contracts isRouteExpired: no expires_at → never; an unparseable timestamp →
// expired (fail closed); else inclusive boundary now >= exp.
func IsRouteExpired(v RouteValue, now time.Time) bool {
	if v.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, v.ExpiresAt)
	if err != nil {
		return true
	}
	return !now.Before(exp) // now >= exp
}
