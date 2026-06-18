//go:build cloud

package billing

// store_unit_test.go covers the DB-FREE logic in store.go: the pure helpers
// (normalizeStatus, periodEnd, timeFromUnix, isUndefinedTable, logger), the
// NewStore constructor, and the empty-id guards that every public store method
// runs BEFORE it touches the pool. The org-scoped SQL itself (UpsertSubscription,
// SetCanceled, ReadPlanTier, GetSubscription, SaveCustomerID, orgExceedsFreeCaps)
// needs a live Postgres and is exercised by the docker integration suite, NOT here.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeStatus(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Allowed statuses pass through verbatim.
		{"active", "active"},
		{"trialing", "trialing"},
		{"past_due", "past_due"},
		{"canceled", "canceled"},
		{"incomplete", "incomplete"},
		// An empty status (a blank on a verified event) collapses to 'active'; any
		// other UNRECOGNIZED status maps to 'past_due' (NOT 'active') so a non-paying
		// status is never recorded as healthy (M6). The authoritative entitlement
		// gate is applyEvent/isEntitledStatus (a non-entitled status never reaches
		// this UPSERT with a paid tier).
		{"", "active"},
		{"unpaid", "past_due"},
		{"incomplete_expired", "past_due"},
		{"paused", "past_due"},
		{"weird_future_status", "past_due"},
	}
	for _, tc := range cases {
		if got := normalizeStatus(tc.in); got != tc.want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPeriodEnd(t *testing.T) {
	// 0 / negative → nil (no current_period_end on the event).
	if got := periodEnd(0); got != nil {
		t.Errorf("periodEnd(0) = %v, want nil", got)
	}
	if got := periodEnd(-1); got != nil {
		t.Errorf("periodEnd(-1) = %v, want nil", got)
	}
	// A positive unix second yields the matching UTC time.Time pgx stores as
	// timestamptz.
	const unix = 1893456000
	got := periodEnd(unix)
	ts, ok := got.(time.Time)
	if !ok {
		t.Fatalf("periodEnd(%d) returned %T, want time.Time", unix, got)
	}
	if !ts.Equal(time.Unix(unix, 0).UTC()) {
		t.Errorf("periodEnd(%d) = %v, want %v", unix, ts, time.Unix(unix, 0).UTC())
	}
	if ts.Location() != time.UTC {
		t.Errorf("periodEnd must be UTC, got %v", ts.Location())
	}
}

func TestTimeFromUnix(t *testing.T) {
	const unix = 1700000000
	got := timeFromUnix(unix)
	if !got.Equal(time.Unix(unix, 0).UTC()) {
		t.Errorf("timeFromUnix(%d) = %v, want %v", unix, got, time.Unix(unix, 0).UTC())
	}
	if got.Location() != time.UTC {
		t.Errorf("timeFromUnix must return UTC, got %v", got.Location())
	}
}

// isUndefinedTable tolerates a missing identity.member table (self-host that hasn't
// migrated Better Auth): only SQLSTATE 42P01 (undefined_table) qualifies, and a
// plain error or a different SQLSTATE must NOT be swallowed.
func TestIsUndefinedTable(t *testing.T) {
	if !isUndefinedTable(&pgconn.PgError{Code: "42P01"}) {
		t.Error("42P01 (undefined_table) must be recognized")
	}
	// Wrapped still matches (errors.As walks the chain).
	wrapped := errors.Join(errors.New("count members"), &pgconn.PgError{Code: "42P01"})
	if !isUndefinedTable(wrapped) {
		t.Error("a wrapped 42P01 must still be recognized")
	}
	// A different SQLSTATE (e.g. 42501 insufficient_privilege) is a real error.
	if isUndefinedTable(&pgconn.PgError{Code: "42501"}) {
		t.Error("a non-42P01 PgError must not be treated as undefined_table")
	}
	// A plain non-pg error is not undefined_table.
	if isUndefinedTable(errors.New("boom")) {
		t.Error("a plain error must not be treated as undefined_table")
	}
	if isUndefinedTable(nil) {
		t.Error("nil must not be treated as undefined_table")
	}
}

// NewStore wraps a pool with the default logger and no edge projection; the
// chaining WithOrgStatusWriter attaches a writer.
func TestNewStore_DefaultsAndChaining(t *testing.T) {
	s := NewStore(nil) // nil pool is fine: we don't call a DB method here.
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.log == nil {
		t.Error("NewStore must set a non-nil logger")
	}
	if s.status != nil {
		t.Error("NewStore must start with no edge org-status writer")
	}
	w := &fakeOrgStatusWriter{}
	if got := s.WithOrgStatusWriter(w); got != s {
		t.Error("WithOrgStatusWriter must return the same store for chaining")
	}
	if s.status == nil {
		t.Error("WithOrgStatusWriter must attach the writer")
	}
	// Passing nil disables projection again.
	if s.WithOrgStatusWriter(nil).status != nil {
		t.Error("WithOrgStatusWriter(nil) must leave projection disabled")
	}
}

// logger falls back to slog.Default() so a zero-value store never nil-panics on a
// log call, and returns the explicit logger when one is set.
func TestLogger_Fallback(t *testing.T) {
	if (&BillingStore{}).logger() == nil {
		t.Error("zero-value store logger() must fall back to a non-nil logger")
	}
	custom := slog.New(slog.NewTextHandler(nil, nil))
	// A custom logger is returned as-is.
	if (&BillingStore{log: custom}).logger() != custom {
		t.Error("logger() must return the explicit logger when set")
	}
}

// Every public store method validates its id arguments BEFORE opening a tx, so a
// store with a nil pool still returns the guard error rather than panicking on the
// DB. This pins the empty-input contract without a live database.
func TestStoreMethods_EmptyIDGuards(t *testing.T) {
	s := &BillingStore{} // nil pool: a guard must fire before any pool access.
	ctx := context.Background()

	if err := s.UpsertSubscription(ctx, EventData{OrgID: ""}); err == nil {
		t.Error("UpsertSubscription with empty OrgID must error")
	}
	if err := s.SetCanceled(ctx, ""); err == nil {
		t.Error("SetCanceled with empty OrgID must error")
	}
	if _, err := s.ReadPlanTier(ctx, ""); err == nil {
		t.Error("ReadPlanTier with empty OrgID must error")
	}
	if _, _, err := s.GetSubscription(ctx, ""); err == nil {
		t.Error("GetSubscription with empty OrgID must error")
	}
	// SaveCustomerID requires BOTH ids.
	if err := s.SaveCustomerID(ctx, "", "cus_1"); err == nil {
		t.Error("SaveCustomerID with empty orgID must error")
	}
	if err := s.SaveCustomerID(ctx, "org_1", ""); err == nil {
		t.Error("SaveCustomerID with empty customerID must error")
	}
}

// ReadPlanTier defaults a missing tier to Free even on the guard error path's
// caller; here we only assert the guard returns TierFree alongside the error (the
// contract its signature promises so callers can treat the result as Free-safe).
func TestReadPlanTier_EmptyOrg_ReturnsFreeAndError(t *testing.T) {
	tier, err := (&BillingStore{}).ReadPlanTier(context.Background(), "")
	if err == nil {
		t.Fatal("empty org must error")
	}
	if tier != TierFree {
		t.Errorf("tier on guard error = %q, want free", tier)
	}
}

// The txSubsStore apply adapter rejects an empty OrgID on both methods BEFORE it
// touches the tx, mirroring the standalone guards (it is a pointer adapter shared
// by the atomic ProcessEvent path).
func TestTxSubsStore_EmptyOrgGuards(t *testing.T) {
	a := &txSubsStore{} // nil tx: the guard must fire before any tx use.
	if err := a.UpsertSubscription(context.Background(), EventData{OrgID: ""}); err == nil {
		t.Error("txSubsStore.UpsertSubscription with empty OrgID must error")
	}
	if err := a.SetCanceled(context.Background(), ""); err == nil {
		t.Error("txSubsStore.SetCanceled with empty OrgID must error")
	}
	// A guard failure must NOT have captured any org/status to project.
	if a.orgID != "" || a.orgStatus != "" {
		t.Errorf("guarded failure must capture nothing, got org=%q status=%q", a.orgID, a.orgStatus)
	}
}
