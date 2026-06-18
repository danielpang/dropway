// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Package ratelimit is the serving plane's in-process edge rate limiter — the
// self-host equivalent of the Worker's native Rate Limiting binding (which has no
// off-Workers analogue). It is a fixed-window counter keyed by request identity
// (client IP, else host), defaulting to 600 req / 60s, mirroring
// edge/serving-worker/src/ratelimit.ts DEFAULT_RATE_LIMIT. It FAILS OPEN: a limiter
// that is disabled (limit <= 0) always allows. Rate limiting is a soft denial-of-
// wallet control; the authoritative caps live in the Go API.
package ratelimit

import (
	"net/http"
	"sync"
	"time"
)

// DefaultLimit / DefaultWindow are the conservative defaults (600 req / 60s).
const (
	DefaultLimit  = 600
	DefaultWindow = 60 * time.Second
)

// Limiter is a fixed-window, per-identity in-process rate limiter, safe for
// concurrent use. A zero-value Limiter is not usable; build with New.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	windows map[string]*windowCounter
	// lastSweep bounds the windows map by periodically dropping expired entries.
	lastSweep time.Time
}

type windowCounter struct {
	index int64
	count int
}

// New builds a Limiter. limit <= 0 disables limiting (always allow / fail open).
// window <= 0 uses DefaultWindow.
func New(limit int, window time.Duration) *Limiter {
	if window <= 0 {
		window = DefaultWindow
	}
	return &Limiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		windows: map[string]*windowCounter{},
	}
}

// Identity returns the rate-limit identity for a request: the client IP from
// CF-Connecting-IP / X-Real-IP if present, else "host:<host>" (ratelimit.ts
// rateLimitIdentity).
func Identity(r *http.Request, host string) string {
	ip := r.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip != "" {
		return "ip:" + ip
	}
	return "host:" + host
}

// Result is the outcome of a rate-limit check.
type Result struct {
	Allowed           bool
	RetryAfterSeconds int
}

// Allow records a request for identity and reports whether it is within the limit.
// Disabled limiters (limit <= 0) always allow (fail open). The window index is
// derived from wall-clock so each window is a fresh counter.
func (l *Limiter) Allow(identity string) Result {
	if l == nil || l.limit <= 0 {
		return Result{Allowed: true}
	}

	now := l.now()
	winSecs := int64(l.window / time.Second)
	if winSecs <= 0 {
		winSecs = 1
	}
	index := now.Unix() / winSecs

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	wc, ok := l.windows[identity]
	if !ok || wc.index != index {
		wc = &windowCounter{index: index, count: 0}
		l.windows[identity] = wc
	}
	wc.count++

	if wc.count > l.limit {
		// Seconds remaining in the current window.
		remaining := int(winSecs - (now.Unix() % winSecs))
		if remaining < 1 {
			remaining = 1
		}
		return Result{Allowed: false, RetryAfterSeconds: remaining}
	}
	return Result{Allowed: true}
}

// sweep drops counters from prior windows so the map stays bounded. Cheap and
// best-effort; runs at most ~once per window. Caller holds l.mu.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	winSecs := int64(l.window / time.Second)
	if winSecs <= 0 {
		winSecs = 1
	}
	cur := now.Unix() / winSecs
	for k, wc := range l.windows {
		if wc.index < cur {
			delete(l.windows, k)
		}
	}
}
