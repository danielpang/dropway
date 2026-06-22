// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// fixedClock returns a limiter whose clock the test drives manually, so refill /
// expiry are deterministic (no sleeps).
func fixedClock(l *rateLimiter, t *time.Time) {
	l.now = func() time.Time { return *t }
}

func TestRateLimiter_AllowsUnderTheLimit(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 5, time.Minute) // 1/sec sustained, burst 5
	fixedClock(l, &now)

	// The first `burst` requests, all at the same instant, must be allowed.
	for i := 0; i < 5; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
}

func TestRateLimiter_BlocksOverTheLimit(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 3, time.Minute)
	fixedClock(l, &now)

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("k"); !ok {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	ok, retryAfter := l.allow("k")
	if ok {
		t.Fatal("request over the burst should be blocked")
	}
	if retryAfter <= 0 {
		t.Fatalf("blocked request must report a positive Retry-After, got %v", retryAfter)
	}
}

func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 1, time.Minute) // burst 1
	fixedClock(l, &now)

	if ok, _ := l.allow("a"); !ok {
		t.Fatal("first hit on key a should pass")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("second hit on key a should be blocked")
	}
	// A different key has its own bucket and is unaffected.
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("first hit on key b should pass despite key a being throttled")
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 1, time.Minute) // 1 token/sec, burst 1
	fixedClock(l, &now)

	if ok, _ := l.allow("k"); !ok {
		t.Fatal("first hit should pass")
	}
	if ok, _ := l.allow("k"); ok {
		t.Fatal("immediate second hit should be blocked")
	}
	// Advance one second: one token refills.
	now = now.Add(time.Second)
	if ok, _ := l.allow("k"); !ok {
		t.Fatal("after 1s refill the request should pass again")
	}
}

func TestRateLimiter_SweepEvictsStaleBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 5, time.Minute)
	fixedClock(l, &now)

	l.allow("k")
	if len(l.buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(l.buckets))
	}
	// Advance past the expiry window, then sweep: the idle bucket is evicted.
	now = now.Add(2 * time.Minute)
	l.sweep()
	if len(l.buckets) != 0 {
		t.Fatalf("stale bucket should be evicted, %d remain", len(l.buckets))
	}
}

// The hard cap bounds the map even under a flood of DISTINCT, still-live keys
// (the case the time-based sweeper alone can't catch between ticks).
func TestRateLimiter_HardCapBoundsDistinctKeys(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(60, 5, time.Minute)
	fixedClock(l, &now)
	l.maxEntries = 10

	// Spray 1000 distinct keys WITHOUT advancing the clock, so none expire — only
	// the hard cap can bound growth here.
	for i := 0; i < 1000; i++ {
		l.allow("key-" + strconv.Itoa(i))
	}
	if len(l.buckets) > l.maxEntries {
		t.Fatalf("map grew past the cap: %d entries, cap %d", len(l.buckets), l.maxEntries)
	}
}

// Concurrency: many goroutines hammering ONE key with a frozen clock (no refill)
// must let exactly `burst` through, proving the read-modify-write is atomic under
// the lock with no race or over-grant. Run with -race.
func TestRateLimiter_ConcurrentSingleKeyAllowsExactlyBurst(t *testing.T) {
	now := time.Unix(0, 0)
	const burst = 5
	l := newRateLimiter(60, burst, time.Minute)
	fixedClock(l, &now) // frozen: no refill during the test

	const goroutines = 200
	var allowed int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if ok, _ := l.allow("same-key"); ok {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != burst {
		t.Fatalf("concurrent allows on one key = %d, want exactly %d (burst)", allowed, burst)
	}
}

func TestClientIPForRateLimit_HeaderPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		fly        string
		xff        string
		remoteAddr string
		want       string
	}{
		{name: "fly wins", fly: "1.1.1.1", xff: "2.2.2.2", remoteAddr: "3.3.3.3:9", want: "1.1.1.1"},
		{name: "xff leftmost", xff: "2.2.2.2, 9.9.9.9", remoteAddr: "3.3.3.3:9", want: "2.2.2.2"},
		{name: "remoteaddr host", remoteAddr: "3.3.3.3:9", want: "3.3.3.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.fly != "" {
				r.Header.Set("Fly-Client-IP", tc.fly)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIPForRateLimit(r); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAuthzPassword_RateLimited_429 asserts the password endpoint returns 429
// (with a Retry-After header) after the burst is exhausted, and that the throttle
// fires BEFORE the store/bcrypt work (denial-of-wallet protection).
func TestAuthzPassword_RateLimited_429(t *testing.T) {
	fs := newFakeStore()
	hash := mustHash(t, "swordfish")
	calls := 0
	fs.p2().passwordFn = func(host string) (store.PasswordDecision, string, error) {
		calls++
		return store.PasswordDecision{Host: host, SiteID: "site_1", OrgID: "org_1", Mode: projection.AccessPassword}, hash, nil
	}
	a := NewFull(quota.Unlimited{}, fs, nil, nil)
	a.EdgeSigner = testSigner(t)
	// Small bucket so the test is fast: burst 2, slow refill.
	a.PasswordRateLimiter = newRateLimiter(1, 2, time.Minute)
	h := mountAccess(a, nil)

	const body = `{"host":"acme.dropwaycontent.com","password":"nope"}`
	// First two attempts are allowed (they 401 on the wrong password).
	for i := 0; i < 2; i++ {
		rr := postJSON(h, "/v1/authz/password", body)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401: %s", i+1, rr.Code, rr.Body.String())
		}
	}
	callsBefore := calls

	// The third attempt is throttled: 429 + Retry-After, and no further store call.
	rr := postJSON(h, "/v1/authz/password", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled attempt: status = %d, want 429: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("429 response must carry a Retry-After header")
	}
	if calls != callsBefore {
		t.Errorf("throttled request must not reach the store/bcrypt (calls %d -> %d)", callsBefore, calls)
	}
}
