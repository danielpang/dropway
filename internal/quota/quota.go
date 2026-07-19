// Package quota defines the open-core seam between the FSL core and the
// proprietary cloud quota/billing module.
//
// The seam is a PURE POLICY FUNCTION with no database access:
//
//	Provider.Allow(planTier string, res Resource, current int64) error
//
// It answers a single question — "given this plan tier and the current count of
// `res`, may we create one more?" — and returns an *ExceededError (→ HTTP 402)
// when current+1 would cross the tier's hard cap. The DB mechanics that make the
// check race-safe (the per-(org,user) advisory lock, the COUNT, reading
// org_meta.plan_tier, and the INSERT) all live in the STORE, inside the
// resource-creation transaction (internal/store). Keeping the provider pure means
// the policy is trivially unit-testable, has no TOCTOU surface of its own, and
// the core never imports any cloud/ code.
//
// The CORE ships the Unlimited implementation (every action allowed → a
// self-hosted build has no caps). The hosted build wires in cloud/quota's real
// Provider (the Free 10-sites → Pro 100-sites → Enterprise unlimited hard caps,
// with FREE seats on every tier), selected via the `cloud` build tag in
// services/api/cmd/api.
package quota

import (
	"errors"
	"fmt"
)

// Resource is the thing being created that may be capped.
type Resource string

const (
	// ResourceSitePerOrg caps the number of sites in an ORG (the workspace), pooled
	// across all members. This is the seat-free pricing lever: you
	// move up a tier when you need MORE SITES, not more seats. The store counts every
	// site in the org (CountSitesForOrg) under a per-org advisory lock.
	ResourceSitePerOrg Resource = "sites_per_org"
	// ResourceMemberPerOrg is the org-seat policy seam. Seats are FREE under the
	// current pricing (unlimited members on every plan), so the cloud provider returns
	// unlimited for it and the members preflight always passes; the seam stays so seat
	// policy could be re-tightened in the provider without any store/handler change.
	ResourceMemberPerOrg Resource = "members_per_org"
	// ResourceStorageBytesPerOrg is a CONTINUOUS resource (bytes), checked with
	// AllowN(current, delta) rather than Allow's "+1" — a deploy adds `delta` bytes
	// of new (dedup-aware) blob storage.
	ResourceStorageBytesPerOrg Resource = "storage_bytes_per_org"
	// ResourceSkillPerOrg caps the number of shared skills in an org. Unlimited on
	// every tier today (the seam exists so the cloud provider can tighten it
	// without a store/handler change).
	ResourceSkillPerOrg Resource = "skills_per_org"
	// ResourceSkillPerFolder caps how many skills a single skill FOLDER may hold.
	// The cloud provider limits the free tier to 10 skills per folder
	// (pro/business/enterprise unlimited); OSS self-host is Unlimited. Checked
	// under a per-folder advisory lock inside the membership-insert tx.
	ResourceSkillPerFolder Resource = "skills_per_folder"
	// ResourceCustomDomainPerOrg gates custom domains as a PAID feature rather than
	// a count band: the cloud provider caps the free tier at 0 (so registering the
	// first custom hostname returns 402 {next_tier: pro}) and leaves every paid tier
	// unlimited. OSS self-host is Unlimited. Checked by the AddDomain preflight
	// BEFORE the Cloudflare custom hostname is provisioned, so a free org never
	// creates a provider-side hostname it isn't entitled to.
	ResourceCustomDomainPerOrg Resource = "custom_domains_per_org"
	// ResourceChatMessagePerLog caps how many messages one shared chat log holds.
	// The free tier is a ROLLING WINDOW (see Windower), not a hard cap: appends
	// never 402, the store prunes the oldest rows past the window instead. Paid
	// tiers never auto-delete, so pro is a hard cap (100 → 402 {next_tier:
	// business}) and business/enterprise are unlimited. Checked/pruned under a
	// per-log advisory lock inside the append tx.
	ResourceChatMessagePerLog Resource = "chat_messages_per_log"
	// ResourceChatLogPerOrg caps the number of chat logs in an org. Unlimited on
	// every tier today (per-log content is bounded by window/cap × message size,
	// and every viewer-facing surface is bounded by the site caps); the seam
	// exists so the cloud provider can tighten it without a store/handler change.
	ResourceChatLogPerOrg Resource = "chat_logs_per_org"
	// ResourceAPIKeyPerOrg caps the number of org-scoped API keys. DORMANT:
	// unlimited on every tier today (per-key blast radius is bounded by the
	// member-level role ceiling + revocation, and the surface is already bounded by
	// the per-key rate limit + the site caps). The seam exists so the cloud provider
	// can cap key counts later without a store/handler change (the ResourceSkillPerOrg
	// pattern).
	ResourceAPIKeyPerOrg Resource = "api_keys_per_org"
)

// ExceededError is returned by an enforcing Provider when an action would cross
// a hard cap. The Go API surfaces this as HTTP 402 with this body so the
// dashboard can open the subscription modal (or the contact-sales CTA).
type ExceededError struct {
	Limit      Resource `json:"limit"`
	Current    int64    `json:"current"`
	Max        int64    `json:"max"`
	PlanTier   string   `json:"plan_tier"`
	NextTier   string   `json:"next_tier,omitempty"`
	UpgradeURL string   `json:"upgrade_url,omitempty"`
	SalesURL   string   `json:"sales_url,omitempty"`
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota exceeded: %s (%d/%d) on plan %q", e.Limit, e.Current, e.Max, e.PlanTier)
}

// AsExceeded extracts an *ExceededError from an error chain, if present.
func AsExceeded(err error) (*ExceededError, bool) {
	var e *ExceededError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Provider is the pure quota policy. Given the org's live plan tier and the
// CURRENT count of `res` (how many already exist), Allow reports whether creating
// ONE MORE is permitted: it returns nil when current+1 is within the cap and an
// *ExceededError when current+1 would cross it.
//
// Allow MUST be free of side effects and IO: race-safety is the store's job (it
// holds a per-(org,subject) advisory lock across the COUNT → Allow → INSERT so two
// concurrent same-subject creates can't both slip past the cap). A pure policy
// also means the `current` passed in must be the live count read under that lock.
type Provider interface {
	// Allow reports whether creating ONE MORE of a discrete resource (a site, a
	// member) is within the tier cap — i.e. current+1 <= cap.
	Allow(planTier string, res Resource, current int64) error
	// AllowN reports whether ADDING n units of res to current stays within the tier
	// cap — i.e. current+n <= cap. For discrete resources n=1 is identical to Allow;
	// for a CONTINUOUS resource (storage bytes) n is the size delta. The store passes
	// the live `current` read under the per-org advisory lock so it stays race-safe.
	AllowN(planTier string, res Resource, current, n int64) error
}

// Windower is the OPTIONAL window-semantics side of a Provider: instead of a
// hard cap (Allow → 402), some (tier, resource) bands keep the NEWEST n items
// and silently prune older ones — the free tier's rolling last-10 chat-log
// window. The store type-asserts its Provider to Windower inside the append
// tx: window set → INSERT then DELETE rows beyond the newest n; no window (or
// the Provider doesn't implement Windower) → the standard COUNT → AllowN →
// 402 path. Kept as a separate interface so existing Provider implementations
// (Unlimited, test fakes) stay valid without a window concept.
type Windower interface {
	// RetentionWindow returns the rolling-retention window for (planTier, res):
	// keep the newest n, prune the rest. ok=false means no window applies and
	// the caller should use the Allow/AllowN hard-cap path instead.
	RetentionWindow(planTier string, res Resource) (n int64, ok bool)
}

// RetentionWindow resolves the rolling window for (planTier, res) if p also
// implements Windower; otherwise no window applies.
func RetentionWindow(p Provider, planTier string, res Resource) (int64, bool) {
	if w, ok := p.(Windower); ok {
		return w.RetentionWindow(planTier, res)
	}
	return 0, false
}

// Unlimited is the core/self-host Provider: every action is allowed regardless of
// tier or count (OSS self-host has no caps). This is the default; cloud builds
// replace it via dependency injection (wire_cloud.go).
type Unlimited struct{}

// Allow always permits the action.
func (Unlimited) Allow(string, Resource, int64) error { return nil }

// AllowN always permits the action (self-host has no caps).
func (Unlimited) AllowN(string, Resource, int64, int64) error { return nil }

// Ensure Unlimited satisfies Provider.
var _ Provider = Unlimited{}
