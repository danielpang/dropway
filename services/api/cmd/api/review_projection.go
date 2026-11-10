// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package main

import (
	"context"
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
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			attempt, finish := context.WithTimeout(ctx, 20*time.Second)
			_, err := st.DrainCandidateProjections(attempt, writer)
			finish()
			if err != nil && ctx.Err() == nil {
				slog.Error("candidate publication recovery failed; durable effects remain queued", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
