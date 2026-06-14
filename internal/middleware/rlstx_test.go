package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/danielpang/shipped/internal/auth"
)

// fakeTx records the statements executed against it so a test can assert the
// exact SET LOCAL contract without a live Postgres.
type fakeTx struct {
	calls   []execCall
	failOn  int   // 1-based index of the call that should fail; 0 = never
	failErr error // error to return for the failing call
}

type execCall struct {
	sql  string
	args []any
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (any, error) {
	f.calls = append(f.calls, execCall{sql: sql, args: args})
	if f.failOn != 0 && len(f.calls) == f.failOn {
		return nil, f.failErr
	}
	return nil, nil
}

// TestSetTenantContext is the required unit test for the SET LOCAL helper.
func TestSetTenantContext(t *testing.T) {
	tx := &fakeTx{}
	if err := SetTenantContext(context.Background(), tx, "user_abc", "org_xyz"); err != nil {
		t.Fatalf("SetTenantContext: %v", err)
	}

	if len(tx.calls) != 2 {
		t.Fatalf("got %d exec calls, want 2", len(tx.calls))
	}

	// First call sets the user id, transaction-local.
	c0 := tx.calls[0]
	if c0.sql != `SELECT set_config('app.current_user_id', $1, true)` {
		t.Errorf("call 0 sql = %q", c0.sql)
	}
	if len(c0.args) != 1 || c0.args[0] != "user_abc" {
		t.Errorf("call 0 args = %v, want [user_abc]", c0.args)
	}

	// Second call sets the org id, transaction-local.
	c1 := tx.calls[1]
	if c1.sql != `SELECT set_config('app.current_org_id', $1, true)` {
		t.Errorf("call 1 sql = %q", c1.sql)
	}
	if len(c1.args) != 1 || c1.args[0] != "org_xyz" {
		t.Errorf("call 1 args = %v, want [org_xyz]", c1.args)
	}
}

func TestSetTenantContext_MissingIdentifiers(t *testing.T) {
	cases := []struct {
		name          string
		userID, orgID string
	}{
		{"empty user", "", "org_xyz"},
		{"empty org", "user_abc", ""},
		{"both empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &fakeTx{}
			err := SetTenantContext(context.Background(), tx, tc.userID, tc.orgID)
			if !errors.Is(err, ErrMissingTenant) {
				t.Fatalf("err = %v, want ErrMissingTenant", err)
			}
			// Fail closed: nothing should have been executed.
			if len(tx.calls) != 0 {
				t.Errorf("executed %d statements on invalid input, want 0", len(tx.calls))
			}
		})
	}
}

func TestSetTenantContext_PropagatesExecError(t *testing.T) {
	sentinel := errors.New("connection reset")

	// Fail on the first Exec (user id).
	tx := &fakeTx{failOn: 1, failErr: sentinel}
	err := SetTenantContext(context.Background(), tx, "u", "o")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want to wrap sentinel", err)
	}
	if len(tx.calls) != 1 {
		t.Errorf("got %d calls, want 1 (should stop after the first failure)", len(tx.calls))
	}

	// Fail on the second Exec (org id).
	tx2 := &fakeTx{failOn: 2, failErr: sentinel}
	err = SetTenantContext(context.Background(), tx2, "u", "o")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want to wrap sentinel", err)
	}
	if len(tx2.calls) != 2 {
		t.Errorf("got %d calls, want 2", len(tx2.calls))
	}
}

func TestSetTenantContextFromClaims(t *testing.T) {
	c := &auth.Claims{OrgID: "org_42"}
	c.Subject = "user_7" // RegisteredClaims.Subject → UserID()

	tx := &fakeTx{}
	if err := SetTenantContextFromClaims(context.Background(), tx, c); err != nil {
		t.Fatalf("from claims: %v", err)
	}
	if len(tx.calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(tx.calls))
	}
	if tx.calls[0].args[0] != "user_7" || tx.calls[1].args[0] != "org_42" {
		t.Errorf("derived args = %v / %v", tx.calls[0].args, tx.calls[1].args)
	}

	// nil claims → fail closed.
	if err := SetTenantContextFromClaims(context.Background(), &fakeTx{}, nil); !errors.Is(err, ErrMissingTenant) {
		t.Errorf("nil claims err = %v, want ErrMissingTenant", err)
	}
}
