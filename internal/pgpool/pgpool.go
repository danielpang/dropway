// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package pgpool centralizes pgxpool construction for every Go service so the
// connection budget is set in ONE place rather than relying on pgx's defaults.
//
// Why this exists: pgxpool's default MaxConns is max(4, runtime.NumCPU()). On a
// Fly shared-cpu-1x machine runtime.NumCPU() reports the HOST's core count (often
// 8–16), not the 1 vCPU share, so an uncapped pool silently sizes itself far larger
// than intended. Three uncapped services then race to exhaust a shared Postgres
// connection budget — the same class of failure the dashboard hit as
// EMAXCONNSESSION against Supabase's session-mode pooler (pool_size: 15).
//
// Each service passes a deliberate default cap; DB_MAX_CONNS overrides it at runtime
// so the budget can be retuned without a code change. Idle connections are released
// (MaxConnIdleTime) so pooler slots free up between bursts, and connections are
// recycled (MaxConnLifetime) so none lingers stale through the pooler. Runtime URLs
// should point at the TRANSACTION-mode pooler (Supabase port 6543), which multiplexes
// these onto few backends; the direct/session URL is only needed for migrations.
package pgpool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New builds a pgxpool with an explicit connection cap. defaultMaxConns is the
// service's chosen ceiling; the DB_MAX_CONNS env var overrides it when set to a
// positive integer. The returned pool must be Close()d by the caller.
func New(ctx context.Context, url string, defaultMaxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pgpool: parse config: %w", err)
	}

	cfg.MaxConns = maxConnsFromEnv(defaultMaxConns)
	// Release idle connections so the shared pooler reclaims their slots between
	// bursts; recycle live ones so none is held open indefinitely through the pooler.
	cfg.MaxConnIdleTime = 30 * time.Second
	cfg.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	go monitorSaturation(ctx, pool)
	return pool, nil
}

// monitorSaturation samples the pool on a ticker and logs when callers had to WAIT for
// a free connection (the pool hit its cap), or when acquires were CANCELED while waiting
// (actual request failures). A canceled acquire does NOT by itself prove the pool was
// exhausted: the wait may be a slow/hung dial to the database, or the REQUEST may have
// died for an unrelated reason (client disconnect, server timeout) while briefly
// waiting — read acquired_conns vs max_conns to tell the cases apart before touching
// DB_MAX_CONNS. It only logs on a non-zero delta, so a healthy pool stays silent.
// Exits when ctx is cancelled.
func monitorSaturation(ctx context.Context, pool *pgxpool.Pool) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var lastEmpty, lastCanceled int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s := pool.Stat()
			empty, canceled := s.EmptyAcquireCount(), s.CanceledAcquireCount()
			dEmpty, dCanceled := empty-lastEmpty, canceled-lastCanceled
			lastEmpty, lastCanceled = empty, canceled
			if dEmpty == 0 && dCanceled == 0 {
				continue
			}
			attrs := []any{
				"waited_acquires", dEmpty,
				"canceled_acquires", dCanceled,
				"acquired_conns", s.AcquiredConns(),
				"total_conns", s.TotalConns(),
				"max_conns", s.MaxConns(),
			}
			if dCanceled > 0 {
				slog.Error("postgres acquires canceled while waiting for a connection (pool at cap, slow dials, or the waiting requests were themselves canceled — compare acquired_conns to max_conns)", attrs...)
			} else {
				slog.Warn("postgres pool saturated: acquisitions waited for a free connection", attrs...)
			}
		}
	}
}

// maxConnsFromEnv returns DB_MAX_CONNS when it is a positive integer, else the
// service-provided default. An unset, empty, or invalid value falls back silently.
func maxConnsFromEnv(def int32) int32 {
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int32(n)
		}
	}
	return def
}
