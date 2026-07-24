// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package analytics

import "context"

// AuthRejection describes one rejected credential (or a downstream auth failure)
// for the `auth_rejected` product event — the PostHog-visible record of every 401
// the services hand out. Step pinpoints WHERE in the auth pipeline the rejection
// happened, so a report like "create_site 401s through MCP" is debuggable step by
// step from events alone:
//
//	surface=mcp  step=jwt_verify       the MCP gate rejected the bearer JWT
//	surface=mcp  step=api_key_auth     the MCP gate rejected a dw_live_ key
//	surface=mcp  step=org_claim        verified JWT carried no org_id
//	surface=mcp  step=org_lookup       the per-request org_meta check errored (403)
//	surface=mcp  step=mcp_disabled     the org's MCP kill switch is on (403)
//	surface=mcp  step=forwarded_write  the Go API refused the token the MCP
//	                                   server forwarded for a write tool
//	surface=api  step=jwt_verify       the API auth boundary rejected the JWT
//	surface=api  step=api_key_auth     the API auth boundary rejected a key
//
// TokenAud/TokenIss are UNVERIFIED hints decoded from the rejected token for
// diagnostics only (never an authz input).
type AuthRejection struct {
	Surface  string // "api" or "mcp"
	Kind     string // "jwt", "api_key", "authz", or "forwarded"
	Step     string // pipeline step, see above
	Reason   string // the real server-side reason (verifier error, API status+message)
	TokenAud string // unverified aud hint, when a token was presented
	TokenIss string // unverified iss hint, when a token was presented
	Method   string // HTTP method, or the MCP tool name for step=forwarded_write
	Path     string // request path
}

// CaptureAuthRejected emits the best-effort `auth_rejected` event. Requests
// carrying NO credential are deliberately never captured by callers: an
// unauthenticated probe (or the RFC 9728 challenge that legitimately starts every
// MCP connect) is noise, not an auth error. distinct_id "system" mirrors the
// dashboard's convention for events with no verified acting user. A nil emitter
// is a no-op.
func CaptureAuthRejected(ctx context.Context, e Emitter, r AuthRejection) {
	if e == nil {
		return
	}
	e.Capture(ctx, Event{
		DistinctID: "system",
		Event:      "auth_rejected",
		Properties: map[string]any{
			"surface":   r.Surface,
			"kind":      r.Kind,
			"step":      r.Step,
			"reason":    r.Reason,
			"token_aud": r.TokenAud,
			"token_iss": r.TokenIss,
			"method":    r.Method,
			"path":      r.Path,
			// Infra event with no person behind it — don't create person profiles.
			"$process_person_profile": false,
		},
	})
}
