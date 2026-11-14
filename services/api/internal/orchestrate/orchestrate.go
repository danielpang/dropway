// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package orchestrate describes the server-side Pi worker. Pi does not run
// inside a Sprite. Compatibility with the Pi SDK is NOT verified: custom
// tools, default-tool disabling, extension autoload, session persistence,
// cancel, and usage accounting must be confirmed against current docs before
// an adapter exists. This package performs no I/O.
package orchestrate

import "errors"

const (
	HarnessPi = "pi"
	// PiSDKCompatibilityVerified is false until the SDK is shown to replace
	// default file/shell tools, refuse extension and host-context autoload,
	// persist sessions outside the worker disk, cancel a run, and report usage
	// without injecting a provider key.
	PiSDKCompatibilityVerified = false
)

var ErrTool = errors.New("orchestrate: tool not allowed")

// SessionSnapshot is pinned when a session is claimed. Later edits to the
// user's model or credential selection do not change an in-flight session.
// CredentialRef is an opaque id owned by InitiatorUserID, never a secret.
type SessionSnapshot struct {
	OrgID           string `json:"org_id"`
	RoomID          string `json:"room_id"`
	SiteID          string `json:"site_id"`
	VersionID       string `json:"version_id"`
	InitiatorUserID string `json:"initiator_user_id"`
	CredentialRef   string `json:"credential_ref"`
	Provider        string `json:"provider"`
	ModelID         string `json:"model_id"`
	HarnessID       string `json:"harness_id"`
	HarnessVersion  string `json:"harness_version"`
}

// ReleasePlan is what clarification must do. The session record would be
// stored by Dropway; the worker and Sprite are released; continuation is a
// new execution attempt with the same pinned snapshot.
type ReleasePlan struct {
	PersistSessionInDropway  bool            `json:"persist_session_in_dropway"`
	ReleaseWorker            bool            `json:"release_worker"`
	ReleaseSprite            bool            `json:"release_sprite"`
	ContinuationFreshAttempt bool            `json:"continuation_fresh_attempt"`
	Pinned                   SessionSnapshot `json:"pinned"`
}

// PlanClarification returns the required release. It does not persist or
// destroy anything.
func PlanClarification(s SessionSnapshot) ReleasePlan {
	return ReleasePlan{
		PersistSessionInDropway:  true,
		ReleaseWorker:            true,
		ReleaseSprite:            true,
		ContinuationFreshAttempt: true,
		Pinned:                   s,
	}
}

// Placement is where a tool is allowed to run.
type Placement struct {
	Backend string // "sprite" or "dropway_api"
}

// Place maps a tool name. Local execution is not a fallback. Unknown tools fail.
func Place(tool string) (Placement, error) {
	switch tool {
	case "read", "write", "edit", "bash", "shell", "build", "grep", "ls", "glob":
		return Placement{Backend: "sprite"}, nil
	case "list_threads", "get_thread", "list_candidates", "reply_thread":
		return Placement{Backend: "dropway_api"}, nil
	default:
		return Placement{}, ErrTool
	}
}

// LocalFallbackAllowed is always false. The orchestration worker must not
// read or execute the host filesystem when a Sprite tool fails.
func LocalFallbackAllowed() bool { return false }

// AutoloadExtensions is always false. Pi must not load repo extensions,
// plugins, AGENTS.md, or skills from the workspace.
func AutoloadExtensions() bool { return false }

// CredentialOwnedBy reports whether a credential reference may be used.
// Billing v1 charges the initiating user. Room owners and teammates are not
// implicit donors.
func CredentialOwnedBy(initiatorID, ownerID string) bool {
	return initiatorID != "" && initiatorID == ownerID
}
