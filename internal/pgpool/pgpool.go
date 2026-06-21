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

	return pgxpool.NewWithConfig(ctx, cfg)
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
