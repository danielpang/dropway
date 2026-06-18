package quota

import (
	"errors"
	"testing"
)

// TestAsExceeded_NonMatch asserts AsExceeded returns (nil, false) for an error
// chain that contains no *ExceededError — the negative branch httpx.WriteError
// relies on to fall through to the sentinel/500 mapping.
func TestAsExceeded_NonMatch(t *testing.T) {
	if ex, ok := AsExceeded(errors.New("some other failure")); ok || ex != nil {
		t.Errorf("AsExceeded(non-quota) = (%v, %v), want (nil, false)", ex, ok)
	}
	if ex, ok := AsExceeded(nil); ok || ex != nil {
		t.Errorf("AsExceeded(nil) = (%v, %v), want (nil, false)", ex, ok)
	}
}

// TestExceededError_Error asserts the human-readable Error() string carries the
// resource, the count/cap, and the plan tier (what surfaces in logs and the CLI
// upgrade message).
func TestExceededError_Error(t *testing.T) {
	e := &ExceededError{
		Limit:    ResourceSitePerOrg,
		Current:  10,
		Max:      10,
		PlanTier: "free",
	}
	got := e.Error()
	for _, want := range []string{"sites_per_org", "10/10", "free"} {
		if !contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
