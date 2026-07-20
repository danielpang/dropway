// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a small, in-memory token-bucket limiter keyed by an opaque
// string (here: client IP + target host). It is the FIRST layer of brute-force /
// denial-of-wallet protection on the unauthenticated password exchange (M3).
//
// In-memory is acceptable as a first layer because the API runs as a single
// always-on Fly machine (fly.toml: min_machines_running=1,
// auto_stop_machines=false), so there is exactly one process holding the state.
// If the API is ever scaled horizontally this must be promoted to a shared store
// (e.g. Redis); until then a per-process bucket is the right, minimal control.
//
// Each key gets a bucket that refills at `rate` tokens per second up to `burst`.
// A request costs one token; when the bucket is empty the request is throttled.
// A mutex-guarded map holds the buckets. Growth is bounded two ways: a background
// sweeper evicts buckets untouched past an expiry window, and a HARD cap
// (maxEntries) drops the oldest bucket on insert if a wide spray of distinct keys
// ever fills the map between sweeps (a backstop in case the Fly-Client-IP
// assumption in clientIPForRateLimit ever breaks and keys become spoofable).
const defaultMaxRateLimitEntries = 100_000

type rateLimiter struct {
	rate       float64       // tokens refilled per second
	burst      float64       // bucket capacity (max tokens)
	expiry     time.Duration // evict an idle (untouched) bucket after this long
	maxEntries int           // hard cap on tracked keys (memory backstop)
	now        func() time.Time
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
}

// tokenBucket is the per-key refill state. tokens is the current allowance;
// last is when it was last refilled.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds a limiter allowing `burst` immediate requests per key,
// refilling at `ratePerMinute` tokens per minute. Stale (full, untouched)
// buckets are evicted after `expiry`. A non-positive rate or burst is clamped to
// a safe minimum so a misconfigured env can never disable the control entirely.
func newRateLimiter(ratePerMinute, burst float64, expiry time.Duration) *rateLimiter {
	if ratePerMinute <= 0 {
		ratePerMinute = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if expiry <= 0 {
		expiry = 10 * time.Minute
	}
	return &rateLimiter{
		rate:       ratePerMinute / 60.0,
		burst:      burst,
		expiry:     expiry,
		maxEntries: defaultMaxRateLimitEntries,
		now:        time.Now,
		buckets:    make(map[string]*tokenBucket),
	}
}

// allow reports whether a request on `key` may proceed, consuming one token when
// it can. It returns the wait until the next token is available (zero when
// allowed) so callers can render a Retry-After header on a block.
func (l *rateLimiter) allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		// Bound the map before inserting a new key: prune expired buckets, and if
		// it's still at the cap (a live distinct-key flood), drop the oldest to make
		// room so memory can't grow without limit.
		if l.maxEntries > 0 && len(l.buckets) >= l.maxEntries {
			l.pruneExpiredLocked(now)
			if len(l.buckets) >= l.maxEntries {
				l.dropOldestLocked()
			}
		}
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		// Refill for the elapsed time, capped at burst.
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * l.rate
			if b.tokens > l.burst {
				b.tokens = l.burst
			}
			b.last = now
		}
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Throttled: time until the bucket holds one whole token again.
	missing := 1 - b.tokens
	wait := time.Duration(missing/l.rate*float64(time.Second)) + time.Second
	return false, wait
}

// sweep evicts buckets untouched for longer than expiry, bounding map growth. It
// is safe to call concurrently with allow.
func (l *rateLimiter) sweep() {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(now)
}

// pruneExpiredLocked deletes every bucket untouched for longer than expiry. The
// caller MUST hold l.mu.
func (l *rateLimiter) pruneExpiredLocked(now time.Time) {
	cutoff := now.Add(-l.expiry)
	for key, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// dropOldestLocked evicts the single least-recently-touched bucket, the backstop
// when the map is at its hard cap and every bucket is still live (a distinct-key
// flood). The evicted key has refilled toward burst anyway, so dropping it only
// resets a near-idle attacker bucket. The caller MUST hold l.mu.
func (l *rateLimiter) dropOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for key, b := range l.buckets {
		if first || b.last.Before(oldest) {
			oldestKey, oldest, first = key, b.last, false
		}
	}
	if !first {
		delete(l.buckets, oldestKey)
	}
}

// runSweeper periodically evicts stale buckets until stop is closed. main wires
// it on a goroutine for the process lifetime.
func (l *rateLimiter) runSweeper(stop <-chan struct{}) {
	t := time.NewTicker(l.expiry)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			l.sweep()
		}
	}
}

// WirePasswordRateLimiter installs the password-endpoint limiter on the API and
// starts its background sweeper, which runs until stop is closed. ratePerMin is
// the sustained attempts/minute per (IP + host) key; burst is how many may arrive
// back to back. Both are clamped to a safe minimum inside newRateLimiter so a
// misconfigured env can never disable the control.
func (a *API) WirePasswordRateLimiter(ratePerMin, burst int, stop <-chan struct{}) {
	l := newRateLimiter(float64(ratePerMin), float64(burst), 10*time.Minute)
	a.PasswordRateLimiter = l
	go l.runSweeper(stop)
}

// WireAPIKeyAuth builds the API-key boundary authenticator (a.KeyAuth) from the
// wired key store, with two rate limiters, and starts their sweepers until stop is
// closed. keyPerMin/keyBurst is the sustained/immediate budget per key id (the
// primary control); ipPerMin/ipBurst is a GENEROUS per-client-IP budget consulted
// before any DB lookup, which bounds a spray of bad/unknown secrets without
// throttling legitimate multi-key CI behind a shared egress IP. It is a no-op when
// no key store is configured (DB-less), leaving a.KeyAuth nil so the router accepts
// JWTs only. Call AFTER a.Keys is set and AllowJWTRoleFallback is configured.
func (a *API) WireAPIKeyAuth(keyPerMin, keyBurst, ipPerMin, ipBurst int, stop <-chan struct{}) {
	if a.Keys == nil {
		return
	}
	keyLimiter := newRateLimiter(float64(keyPerMin), float64(keyBurst), 10*time.Minute)
	ipLimiter := newRateLimiter(float64(ipPerMin), float64(ipBurst), 10*time.Minute)
	go keyLimiter.runSweeper(stop)
	go ipLimiter.runSweeper(stop)
	a.KeyAuth = NewKeyAuthenticator(a.Keys, keyLimiter, ipLimiter, a.AllowJWTRoleFallback)
}

// clientIPForRateLimit extracts the best-effort client IP for the rate-limit key. The API
// sits behind Fly's proxy, so Fly-Client-IP is the trustworthy source. We fall
// back to the left-most X-Forwarded-For entry, then the raw RemoteAddr.
//
// These headers are attacker-controllable, but that is acceptable HERE: a spoofed
// value only changes which bucket the attacker lands in, throttling themselves.
// The value is used ONLY as a rate-limit key, never for authorization or audit.
func clientIPForRateLimit(r *http.Request) string {
	if fly := strings.TrimSpace(r.Header.Get("Fly-Client-IP")); fly != "" {
		return fly
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Left-most entry is the original client per the XFF convention.
		if first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); first != "" {
			return first
		}
	}
	// RemoteAddr is "host:port"; keep just the host when we can.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
