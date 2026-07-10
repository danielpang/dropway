// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ai

import (
	"context"
	"time"

	"github.com/danielpang/dropway/internal/sandbox"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// ensureSandbox returns a live sandbox for the session, reusing the cached one
// when it is still within its idle window and recreating (and reseeding from the
// session's base version) otherwise. A recreated sandbox's id + new idle
// deadline are written back to the session row.
//
// The sandbox is disposable: session state lives in Postgres (the transcript),
// so a dead machine is transparently replaced. Reseeding from the base version
// (not the latest draft) keeps a turn deterministic from the same starting
// point; the transcript replay carries the model's intent forward.
func (r *Runner) ensureSandbox(ctx context.Context, t store.Tenant, sess *store.AISession) (sandbox.Sandbox, error) {
	// Reuse a cached sandbox that is still within its idle window.
	if sess.SandboxID != "" && sess.SandboxExpiresAt != nil && sess.SandboxExpiresAt.After(time.Now()) {
		if sb, err := r.Sandboxes.Get(ctx, sess.SandboxID); err == nil {
			// A cheap liveness probe: if the agent is gone, fall through to create.
			if err := sandbox.WaitReady(ctx, sb); err == nil {
				return sb, nil
			}
		}
		// Cached handle is dead; best-effort destroy before recreating.
		_ = r.Sandboxes.Destroy(ctx, sess.SandboxID)
	}

	sb, err := r.Sandboxes.Create(ctx, sandbox.Spec{
		OrgID:     t.OrgID,
		SessionID: sess.ID,
		TTL:       r.SandboxTTL,
		Egress:    sandbox.EgressFull,
	})
	if err != nil {
		return nil, err
	}

	// Seed the current site into the sandbox so the model edits real content.
	base := ""
	if sess.BaseVersionID != nil {
		base = *sess.BaseVersionID
	}
	if err := seedSandbox(ctx, r.Objects, sb, t.OrgID, sess.SiteID, base); err != nil {
		_ = r.Sandboxes.Destroy(ctx, sb.ID())
		return nil, err
	}

	idle := r.sandboxIdleDeadline()
	if err := r.Store.SetAISessionSandbox(ctx, t, sess.ID, sb.ID(), &idle); err != nil {
		// Non-fatal for this turn: the sandbox is live and usable; the cache write
		// failing just means the next turn recreates it.
		sess.SandboxID = sb.ID()
		return sb, nil
	}
	sess.SandboxID = sb.ID()
	sess.SandboxExpiresAt = &idle
	return sb, nil
}

func (r *Runner) sandboxIdleDeadline() time.Time {
	idle := 15 * time.Minute
	return time.Now().Add(idle)
}
