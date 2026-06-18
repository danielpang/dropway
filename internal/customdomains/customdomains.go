// Package customdomains is the Cloudflare-for-SaaS custom-hostname seam
// (Phase 2 Enterprise). A site owner
// maps a custom hostname (e.g. docs.acme.com) to their site; Cloudflare for SaaS
// issues the edge cert and the user proves control via a DNS DCV record.
//
// The Provider interface lets the Go API drive that flow without coupling to the
// Cloudflare REST API: the real implementation (CloudflareProvider) calls the CF
// "custom hostnames" API; the Fake (fake.go) is an in-memory stand-in for tests
// and offline/self-host. The Go API stores the returned hostname id + DCV record
// in app.domains and polls Status to advance the pending→verifying→active machine.
package customdomains

import "context"

// VerifyState is the normalized custom-hostname lifecycle state. It maps the CF
// custom-hostname status onto the app.domains.verify_status machine.
type VerifyState string

const (
	// StatePending — created, awaiting the user's DNS DCV record.
	StatePending VerifyState = "pending"
	// StateVerifying — CF is validating the DCV record.
	StateVerifying VerifyState = "verifying"
	// StateActive — hostname validated + cert issued (maps to verify_status
	// 'verified'). The Go API writes the KV route on this transition.
	StateActive VerifyState = "active"
	// StateFailed — validation failed (bad/missing DNS).
	StateFailed VerifyState = "failed"
)

// DCVRecord is the DNS record the user must create to prove control of the
// hostname (a TXT record for DCV). Surfaced to the user verbatim.
type DCVRecord struct {
	Name  string // the record name (host)
	Type  string // "TXT" / "CNAME"
	Value string // the record value
}

// String renders the DCV record as "<name> <type> <value>" for storage/display.
func (d DCVRecord) String() string {
	if d.Name == "" && d.Value == "" {
		return ""
	}
	return d.Name + " " + d.Type + " " + d.Value
}

// CreateResult is returned by CreateCustomHostname.
type CreateResult struct {
	ID  string    // the Cloudflare custom-hostname id (opaque; used by Status)
	DCV DCVRecord // the DNS record the user must add
}

// StatusResult is returned by Status.
type StatusResult struct {
	State VerifyState
	// TLSIssued reports whether the edge certificate has been issued.
	TLSIssued bool
	// DCV is the (possibly updated) DCV record CF expects; empty once active.
	DCV DCVRecord
}

// Provider is the Cloudflare-for-SaaS custom-hostname surface.
type Provider interface {
	// CreateCustomHostname registers a custom hostname with Cloudflare for SaaS and
	// returns its id + the DNS DCV record the user must create.
	CreateCustomHostname(ctx context.Context, hostname string) (CreateResult, error)
	// Status polls the custom hostname's verification + TLS state by its id.
	Status(ctx context.Context, id string) (StatusResult, error)
	// DeleteCustomHostname removes the custom hostname from Cloudflare for SaaS by
	// id (called when the user removes a custom domain). Idempotent: deleting an
	// already-removed/unknown id is not an error, so removal stays clean even when
	// the CF side is already gone.
	DeleteCustomHostname(ctx context.Context, id string) error
}
