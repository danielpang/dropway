// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Recovery starts immediately after restart, then polls durable publication
// effects. Every drain has a deadline; shutdown waits for cancellation before
// the database pool closes. The store always reconciles current DB routes.
func startCandidateProjectionRecovery(st *store.Store, writer projection.Writer) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Run once immediately, then every 30s. A daily write-quota rejection
		// lengthens the wait: the free-plan counter will not move again until
		// 00:00 UTC, and probing on the short tick only produces more 429s.
		// QuotaBackoff is shorter than that reset so a Workers plan upgrade is
		// picked up the same day. Unchanged routes do not reach this branch —
		// PutRoute skips a write that already matches Postgres.
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			attempt, finish := context.WithTimeout(ctx, 20*time.Second)
			_, err := st.DrainCandidateProjections(attempt, writer)
			finish()
			wait := 30 * time.Second
			if err != nil && ctx.Err() == nil {
				if errors.Is(err, projection.ErrDailyQuota) {
					wait = projection.QuotaBackoff
					slog.Warn("candidate publication recovery paused; cloudflare daily kv write quota is exhausted",
						"err", err, "retry_in", wait.String())
				} else {
					slog.Error("candidate publication recovery failed; durable effects remain queued", "err", err)
				}
			}
			timer.Reset(wait)
		}
	}()
	return func() { cancel(); <-done }
}
