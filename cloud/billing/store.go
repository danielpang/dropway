//go:build cloud

package billing

// store.go is the PROPRIETARY, cloud-only Postgres persistence for billing. It is
// the concrete EventProcessor (atomic dedupe + apply) the webhook Handler
// (billing.go) depends on through an interface, plus the SubscriptionStore writes
// and the reads used by the billing page.
//
// THE ONE LEGITIMATE SYSTEM WRITE PATH (docs/ARCHITECTURE.md §9 / §14):
// the webhook carries NO user JWT, yet it must UPDATE app.org_meta.plan_tier —
// and app.org_meta is FORCE ROW LEVEL SECURITY with a tenant policy keyed on the
// org id (id = current_setting('app.current_org_id')). So every method that
// touches app.org_meta opens a tx and FIRST runs
//
//	SET LOCAL app.current_org_id = <org id FROM THE EVENT>
//
// via set_config(...,true) — exactly the per-request tenant context the Go API
// uses — so the UPDATE is RLS-permitted AND strictly scoped to that one org. A
// forged event for org B can therefore never write org A's plan_tier: the GUC
// scopes the write to the event's own org. billing.* tables have NO RLS (they're
// the cloud-only mirror), so the dedupe ledger + subscriptions table are written
// directly; only the cross-schema org_meta write needs the GUC.
//
// We connect as the SAME non-BYPASSRLS shipped_app pool the rest of the API uses
// (the GUC scoping — not a privileged role — is the isolation, §9).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/shipped/internal/projection"
)

// Free-tier caps mirrored from §9 (the cloud quota bands). A read-only downgrade
// sets org_status='over_limit' when the org now exceeds either, so the dashboard
// shows the banner and new actions are blocked — but NO data is deleted.
const (
	freeSitesPerUserCap  = 10
	freeMembersPerOrgCap = 5
)

// BillingStore is the pgx-backed persistence. Construct with NewStore.
type BillingStore struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// status projects the per-org suspension/over-limit flag to the edge
	// (org_status:<orgID> in KV). It is BEST-EFFORT and applied AFTER the DB commit:
	// the DB is the source of truth, the KV flag is a rebuildable projection that
	// makes the suspension actually block at the Worker. nil → no edge projection
	// (the DB write still lands; the edge just won't get the fast flag). Set it with
	// WithOrgStatusWriter.
	status projection.OrgStatusWriter
}

// NewStore wraps the shared shipped_app pool. The pool MUST be the non-BYPASSRLS
// runtime pool; the per-event SET LOCAL app.current_org_id is what authorizes and
// scopes the single cross-schema org_meta write.
func NewStore(pool *pgxpool.Pool) *BillingStore {
	return &BillingStore{pool: pool, log: slog.Default()}
}

// WithOrgStatusWriter attaches the edge org-status projection writer so a billing
// org_status change (suspended / over_limit / active) is pushed to KV after the DB
// commit, making suspension enforceable at the serving Worker. Returns the store for
// chaining. Passing nil leaves edge projection disabled.
func (s *BillingStore) WithOrgStatusWriter(w projection.OrgStatusWriter) *BillingStore {
	s.status = w
	return s
}

// Compile-time proof the store satisfies the webhook interfaces.
var (
	_ EventProcessor    = (*BillingStore)(nil)
	_ SubscriptionStore = (*BillingStore)(nil)
)

// ProcessEvent records the Stripe event id in the dedupe ledger AND applies the
// entitlement change in ONE transaction (§9 idempotency + the lost-update fix). It
// reports applied=false when the id was already present (a replay → no-op).
//
// THE ATOMICITY GUARANTEE (FIX 1): the ledger INSERT … ON CONFLICT (event_id) DO
// NOTHING and the entitlement write (UpsertSubscription / SetCanceled, via the SET
// LOCAL app.current_org_id RLS scope) share a SINGLE transaction. So if the apply
// fails, BOTH roll back — no ledger row is committed — and Stripe's retry, seeing
// the event un-recorded, re-applies cleanly. The previous design committed the
// ledger row in its own autocommit tx BEFORE the apply; a failed apply then left
// the id recorded, so every retry short-circuited to "duplicate_ignored" and the
// paying customer's plan_tier never flipped.
//
// An unhandled event type still records the id (so it isn't reprocessed) but does
// no entitlement write (applied=true, no-op).
func (s *BillingStore) ProcessEvent(ctx context.Context, ev Event) (applied bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("billing: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Dedupe ledger insert FIRST, in this tx. ON CONFLICT DO NOTHING → a replay
	// inserts no row; we then commit (a no-op for the ledger) and report applied=false.
	tag, err := tx.Exec(ctx,
		`INSERT INTO billing.processed_stripe_events (event_id, event_type)
		 VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING`,
		ev.ID, ev.Type,
	)
	if err != nil {
		return false, fmt.Errorf("billing: mark processed %s: %w", ev.ID, err)
	}
	if tag.RowsAffected() == 0 {
		// Already processed: nothing to apply. Commit (no rows changed) and report
		// the replay so the handler 200s "duplicate_ignored".
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("billing: commit: %w", err)
		}
		return false, nil
	}

	// Apply the entitlement change INSIDE this same tx. The org-scoped writes set
	// SET LOCAL app.current_org_id on tx so the cross-schema org_meta UPDATE is
	// RLS-permitted and scoped to the event's own org (§9). If this errors, the
	// deferred Rollback discards the just-inserted ledger row too. The adapter
	// records the resulting (org, org_status) so we can project it to the edge AFTER
	// the commit (the DB is authoritative; the KV flag is a best-effort projection).
	sink := &txSubsStore{tx: tx}
	if err := applyEvent(ctx, sink, s.log, ev); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("billing: commit: %w", err)
	}

	// Best-effort edge projection AFTER the durable commit: push the new org_status
	// to KV so a suspension/over_limit actually BLOCKS at the serving Worker (the DB
	// column alone never reaches the edge). A failure here is logged, NOT returned —
	// the DB is the source of truth and the projection is rebuildable, so we must not
	// 500 the webhook (which would make Stripe retry an already-applied event).
	s.projectOrgStatus(ctx, sink.orgID, sink.orgStatus)
	return true, nil
}

// projectOrgStatus pushes an org's status to the edge org-status projection (KV),
// best-effort. No-op when no writer is wired or when there is nothing to project
// (an unhandled event records no org/status). Logged, never fatal — the DB commit
// already succeeded and the projection is rebuildable from Postgres.
func (s *BillingStore) projectOrgStatus(ctx context.Context, orgID, status string) {
	if s.status == nil || orgID == "" || status == "" {
		return
	}
	if err := s.status.SetOrgStatus(ctx, orgID, status); err != nil {
		s.logger().Error("edge org_status projection failed (DB is source of truth; will be rebuilt)",
			"org_id", orgID, "org_status", status, "err", err)
	}
}

// logger returns the store's logger, defaulting to slog.Default() when unset (so a
// zero-value store built in a test never nil-panics on a log call).
func (s *BillingStore) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// txSubsStore adapts an open pgx.Tx to the SubscriptionStore surface applyEvent
// dispatches to, so the entitlement write runs in the SAME tx as the dedupe-ledger
// insert (FIX 1). It establishes the per-event RLS tenant context on the tx before
// the cross-schema org_meta write, exactly as the standalone inOrgTx helper does.
//
// It also CAPTURES the org id + the resulting org_status of the apply so the caller
// (ProcessEvent) can project that status to the edge AFTER the commit. It is a
// pointer so those captured fields are observable by the caller.
type txSubsStore struct {
	tx pgx.Tx

	orgID     string // org the apply touched (empty for an unhandled event)
	orgStatus string // resulting org_status to project ("active" | "over_limit" | ...)
}

var _ SubscriptionStore = (*txSubsStore)(nil)

func (t *txSubsStore) UpsertSubscription(ctx context.Context, d EventData) error {
	if d.OrgID == "" {
		return errors.New("billing: UpsertSubscription with empty OrgID")
	}
	if err := setOrgContext(ctx, t.tx, d.OrgID); err != nil {
		return err
	}
	if err := upsertSubscriptionTx(ctx, t.tx, d); err != nil {
		return err
	}
	// A healthy active subscription resets org_status to 'active' (clears any edge
	// block) — mirrors the 'active' the UPSERT writes.
	t.orgID, t.orgStatus = d.OrgID, "active"
	return nil
}

func (t *txSubsStore) SetCanceled(ctx context.Context, orgID string) error {
	if orgID == "" {
		return errors.New("billing: SetCanceled with empty OrgID")
	}
	if err := setOrgContext(ctx, t.tx, orgID); err != nil {
		return err
	}
	status, err := setCanceledTx(ctx, t.tx, orgID)
	if err != nil {
		return err
	}
	// On cancel the org becomes 'over_limit' (read-only) if it now exceeds Free caps,
	// else 'active'. Project whichever it is so the edge blocks/clears accordingly.
	t.orgID, t.orgStatus = orgID, status
	return nil
}

// UpsertSubscription persists a paid entitlement. THIS IS THE ONLY WRITER OF
// plan_tier (§9). In ONE tx it:
//
//  1. SET LOCAL app.current_org_id = d.OrgID  (RLS scope for the org_meta write);
//  2. UPSERT billing.subscriptions (ON CONFLICT (org_id) DO UPDATE …, org_status
//     reset to 'active', updated_at=now());
//  3. UPDATE app.org_meta SET plan_tier = d.PlanTier WHERE id = d.OrgID.
//
// Because the GUC equals d.OrgID, the org_meta UPDATE is permitted by the tenant
// policy and can ONLY touch that one org's row — a webhook for one org can never
// write another org's plan_tier.
func (s *BillingStore) UpsertSubscription(ctx context.Context, d EventData) error {
	if d.OrgID == "" {
		return errors.New("billing: UpsertSubscription with empty OrgID")
	}
	if err := s.inOrgTx(ctx, d.OrgID, func(tx pgx.Tx) error {
		return upsertSubscriptionTx(ctx, tx, d)
	}); err != nil {
		return err
	}
	// A healthy active subscription clears any edge block (best-effort, post-commit).
	s.projectOrgStatus(ctx, d.OrgID, "active")
	return nil
}

// upsertSubscriptionTx does the actual UPSERT + org_meta plan_tier write inside an
// already-open, org-scoped tx (the GUC must already be set for d.OrgID). Shared by
// the standalone UpsertSubscription and the atomic ProcessEvent path (FIX 1).
//
// FIX 3: billing.subscriptions.stripe_customer_id is NOT NULL UNIQUE. An event with
// an empty customer id and NO existing row to COALESCE from would violate the NOT
// NULL constraint, fail the INSERT, and 500 the webhook → Stripe retries forever.
// We detect that case up front and return errUnfulfillableEvent so the handler 400s
// (a permanent acknowledgment) instead of looping. When a row already exists we
// keep its customer id via COALESCE(NULLIF(...)) and proceed.
func upsertSubscriptionTx(ctx context.Context, tx pgx.Tx, d EventData) error {
	tier := d.PlanTier
	if tier == "" {
		tier = TierFree
	}

	if d.StripeCustomerID == "" {
		// No customer id on the event. The UPSERT can only succeed if an existing
		// row already carries one (kept via COALESCE below); otherwise the NOT NULL
		// insert would fail forever. Probe for an existing row's customer id.
		var existing string
		err := tx.QueryRow(ctx,
			`SELECT stripe_customer_id FROM billing.subscriptions WHERE org_id = $1`, d.OrgID,
		).Scan(&existing)
		switch {
		case errors.Is(err, pgx.ErrNoRows), existing == "":
			// No row (or somehow a blank one) to COALESCE from → unfulfillable. The
			// handler maps this to a 400 so Stripe stops retrying (FIX 3 / §9).
			return fmt.Errorf("%w: subscription event for org %s has no stripe_customer_id and no existing row",
				errUnfulfillableEvent, d.OrgID)
		case err != nil:
			return fmt.Errorf("billing: probe existing customer for %s: %w", d.OrgID, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO billing.subscriptions
		   (org_id, stripe_customer_id, stripe_subscription_id, plan_tier,
		    seats, status, cancel_at_period_end, current_period_end, org_status)
		 VALUES ($1, $2, NULLIF($3,''), $4, $5, $6, $7, $8, 'active')
		 ON CONFLICT (org_id) DO UPDATE SET
		    stripe_customer_id     = COALESCE(NULLIF(EXCLUDED.stripe_customer_id, ''), billing.subscriptions.stripe_customer_id),
		    stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, billing.subscriptions.stripe_subscription_id),
		    plan_tier              = EXCLUDED.plan_tier,
		    seats                  = EXCLUDED.seats,
		    status                 = EXCLUDED.status,
		    cancel_at_period_end   = EXCLUDED.cancel_at_period_end,
		    current_period_end     = EXCLUDED.current_period_end,
		    org_status             = 'active',
		    updated_at             = now()`,
		d.OrgID, d.StripeCustomerID, d.StripeSubscriptionID, string(tier),
		d.Seats, normalizeStatus(d.Status), d.CancelAtPeriodEnd, periodEnd(d.CurrentPeriodEnd),
	); err != nil {
		return fmt.Errorf("billing: upsert subscription %s: %w", d.OrgID, err)
	}

	// The only writer of plan_tier — RLS-scoped to d.OrgID by the GUC the caller set.
	if _, err := tx.Exec(ctx,
		`UPDATE app.org_meta SET plan_tier = $1 WHERE id = $2`,
		string(tier), d.OrgID,
	); err != nil {
		return fmt.Errorf("billing: update org_meta.plan_tier %s: %w", d.OrgID, err)
	}
	return nil
}

// SetCanceled handles customer.subscription.deleted: a READ-ONLY downgrade to
// Free (§9 — NEVER delete data). In one org-scoped tx it sets the subscription to
// plan_tier='free', status='canceled', drops org_meta.plan_tier to 'free', and
// computes org_status: 'over_limit' if the org now exceeds the Free caps (a user
// with > 10 sites OR the org with > 5 members), else 'active'. The org keeps all
// its sites/members; they just become read-only/over-limit until it re-subscribes.
func (s *BillingStore) SetCanceled(ctx context.Context, orgID string) error {
	if orgID == "" {
		return errors.New("billing: SetCanceled with empty OrgID")
	}
	var status string
	if err := s.inOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		st, err := setCanceledTx(ctx, tx, orgID)
		status = st
		return err
	}); err != nil {
		return err
	}
	// Best-effort edge projection after the durable commit (same model as
	// ProcessEvent): a cancel may push the org to over_limit → block at the edge.
	s.projectOrgStatus(ctx, orgID, status)
	return nil
}

// setCanceledTx does the read-only downgrade inside an already-open, org-scoped tx
// (the GUC must already be set for orgID). Shared by the standalone SetCanceled and
// the atomic ProcessEvent path (FIX 1). It returns the computed org_status
// ('over_limit' or 'active') so the caller can project it to the edge.
func setCanceledTx(ctx context.Context, tx pgx.Tx, orgID string) (string, error) {
	over, err := orgExceedsFreeCaps(ctx, tx, orgID)
	if err != nil {
		return "", err
	}
	orgStatus := "active"
	if over {
		orgStatus = "over_limit"
	}

	// The subscription row may not exist (org never paid); UPDATE is then a
	// no-op, which is fine — org_meta is already 'free'. When it does exist we
	// downgrade it without touching stripe_customer_id (kept for re-subscribe).
	if _, err := tx.Exec(ctx,
		`UPDATE billing.subscriptions SET
		    plan_tier            = 'free',
		    status               = 'canceled',
		    cancel_at_period_end = false,
		    org_status           = $2,
		    updated_at           = now()
		 WHERE org_id = $1`,
		orgID, orgStatus,
	); err != nil {
		return "", fmt.Errorf("billing: cancel subscription %s: %w", orgID, err)
	}

	// Downgrade the authoritative entitlement (RLS-scoped to orgID).
	if _, err := tx.Exec(ctx,
		`UPDATE app.org_meta SET plan_tier = 'free' WHERE id = $1`,
		orgID,
	); err != nil {
		return "", fmt.Errorf("billing: downgrade org_meta.plan_tier %s: %w", orgID, err)
	}
	return orgStatus, nil
}

// ReadPlanTier returns the org's authoritative plan tier for the billing page. It
// reads app.org_meta under the org's own RLS context (so it's symmetric with the
// write path and can never read another org). A missing row defaults to 'free'.
func (s *BillingStore) ReadPlanTier(ctx context.Context, orgID string) (PlanTier, error) {
	if orgID == "" {
		return TierFree, errors.New("billing: ReadPlanTier with empty OrgID")
	}
	var tier string
	err := s.inOrgTx(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT plan_tier FROM app.org_meta WHERE id = $1), 'free')::text`,
			orgID,
		).Scan(&tier)
	})
	if err != nil {
		return TierFree, err
	}
	if tier == "" {
		tier = string(TierFree)
	}
	return PlanTier(tier), nil
}

// Subscription is the billing-page view of an org's current subscription row.
type Subscription struct {
	OrgID             string
	StripeCustomerID  string
	PlanTier          PlanTier
	Seats             int64
	Status            string
	OrgStatus         string
	CancelAtPeriodEnd bool
}

// GetSubscription returns the org's subscription row (for the billing page and to
// resolve the existing Stripe customer in Checkout/Portal). ok=false when the org
// has never had a subscription row (it's still implicitly Free).
func (s *BillingStore) GetSubscription(ctx context.Context, orgID string) (sub Subscription, ok bool, err error) {
	if orgID == "" {
		return Subscription{}, false, errors.New("billing: GetSubscription with empty OrgID")
	}
	// billing.subscriptions has no RLS; a plain read scoped by the org_id PK is
	// safe and needs no GUC.
	row := s.pool.QueryRow(ctx,
		`SELECT org_id, stripe_customer_id, plan_tier, seats, status, org_status, cancel_at_period_end
		   FROM billing.subscriptions WHERE org_id = $1`, orgID)
	var tier, status, orgStatus string
	err = row.Scan(&sub.OrgID, &sub.StripeCustomerID, &tier, &sub.Seats, &status, &orgStatus, &sub.CancelAtPeriodEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false, nil
	}
	if err != nil {
		return Subscription{}, false, fmt.Errorf("billing: get subscription %s: %w", orgID, err)
	}
	sub.PlanTier, sub.Status, sub.OrgStatus = PlanTier(tier), status, orgStatus
	return sub, true, nil
}

// SaveCustomerID records the Stripe customer id for an org BEFORE the first
// subscription exists (Checkout flow): the org row may not have a subscription
// yet, so this upserts a minimal Free row carrying just the customer id. It is
// RLS-irrelevant (billing.subscriptions has no RLS) but we still write nothing to
// org_meta here — plan_tier only flips via the signed webhook.
func (s *BillingStore) SaveCustomerID(ctx context.Context, orgID, customerID string) error {
	if orgID == "" || customerID == "" {
		return errors.New("billing: SaveCustomerID requires orgID and customerID")
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO billing.subscriptions (org_id, stripe_customer_id, plan_tier, status, org_status)
		 VALUES ($1, $2, 'free', 'active', 'active')
		 ON CONFLICT (org_id) DO UPDATE SET
		    stripe_customer_id = EXCLUDED.stripe_customer_id,
		    updated_at         = now()`,
		orgID, customerID,
	)
	if err != nil {
		return fmt.Errorf("billing: save customer id %s: %w", orgID, err)
	}
	return nil
}

// inOrgTx runs fn in a tx whose FIRST statement establishes the RLS tenant context
// for orgID via set_config('app.current_org_id', orgID, true) — the transaction-
// local GUC the org_meta policy reads. set_config (not bare SET LOCAL) is used so
// the org id binds as a parameter (no SQL injection from an event field), exactly
// as internal/middleware.SetTenantContext does on the request path.
func (s *BillingStore) inOrgTx(ctx context.Context, orgID string, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("billing: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrgContext(ctx, tx, orgID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit: %w", err)
	}
	return nil
}

// setOrgContext establishes the RLS tenant context for orgID on an already-open tx
// via set_config('app.current_org_id', orgID, true) — the transaction-local GUC the
// org_meta policy reads. set_config (not bare SET LOCAL) is used so the org id binds
// as a parameter (no SQL injection from an event field), exactly as
// internal/middleware.SetTenantContext does on the request path. It is called by
// inOrgTx and by the ProcessEvent path (which establishes the context mid-tx, after
// the dedupe-ledger insert, before the org-scoped entitlement write — FIX 1).
func setOrgContext(ctx context.Context, tx pgx.Tx, orgID string) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_org_id', $1, true)`, orgID); err != nil {
		return fmt.Errorf("billing: set tenant context: %w", err)
	}
	return nil
}

// orgExceedsFreeCaps reports whether, after a downgrade to Free, the org would be
// over the Free caps: ANY user owns > 10 sites, OR the org has > 5 members. The
// sites read is RLS-scoped (the GUC is set on the tx); the member count reads the
// Better-Auth-owned auth.member table explicitly by org (outside app RLS). A
// missing auth.member table (self-host pre-Better-Auth) counts as 0 members.
func orgExceedsFreeCaps(ctx context.Context, tx pgx.Tx, orgID string) (bool, error) {
	// Max sites owned by any single user in the org (per-USER cap).
	var maxSitesPerUser int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(c), 0) FROM (
		    SELECT count(*) AS c FROM app.sites
		    WHERE org_id = $1 GROUP BY owner_user_id
		 ) t`, orgID,
	).Scan(&maxSitesPerUser); err != nil {
		return false, fmt.Errorf("billing: count sites for over-limit check: %w", err)
	}
	if maxSitesPerUser > freeSitesPerUserCap {
		return true, nil
	}

	var members int64
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM auth.member WHERE "organizationId" = $1`, orgID,
	).Scan(&members)
	if err != nil {
		if isUndefinedTable(err) {
			members = 0 // Better Auth not migrated (self-host) → treat as empty.
		} else {
			return false, fmt.Errorf("billing: count members for over-limit check: %w", err)
		}
	}
	return members > freeMembersPerOrgCap, nil
}

// isUndefinedTable reports a Postgres "relation does not exist" (SQLSTATE 42P01),
// used to tolerate a missing auth.member table on a self-host that hasn't migrated
// Better Auth.
func isUndefinedTable(err error) bool {
	type pgcoder interface{ SQLState() string }
	var pe pgcoder
	return errors.As(err, &pe) && pe.SQLState() == "42P01"
}

// normalizeStatus maps an empty status to 'active' and constrains it to the values
// allowed by the billing.subscriptions.status CHECK; anything unexpected becomes
// 'active' (the webhook already verified the event, so this only guards blanks).
func normalizeStatus(status string) string {
	switch status {
	case "active", "trialing", "past_due", "canceled", "incomplete":
		return status
	default:
		return "active"
	}
}

// periodEnd converts a unix-seconds current_period_end into a value pgx stores as
// timestamptz, or nil when unset (0).
func periodEnd(unix int64) any {
	if unix <= 0 {
		return nil
	}
	return timeFromUnix(unix)
}
