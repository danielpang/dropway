// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package handlers

import (
	"net/http"
	"time"

	"github.com/danielpang/dropway/internal/httpx"
)

// ReviewReadLimiter shares one per-user budget across room and comment reads.
// It runs after authentication; neither headers nor cursor contents choose the
// bucket. Like the existing password limiter, this is per API process and has a
// hard memory cap. Multiple API replicas multiply the aggregate allowance.
func ReviewReadLimiter() func(http.Handler) http.Handler {
	return reviewReadLimiter(newRateLimiter(120, 30, 10*time.Minute))
}

func reviewReadLimiter(limiter *rateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && allowCommentRead(r) {
				if t, ok := tenant(r.Context()); ok {
					if allowed, retry := limiter.allow(t.OrgID + ":" + t.UserID); !allowed {
						httpx.WriteRateLimited(w, retry, "rate_limited", "too many review reads; retry after the indicated delay")
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
