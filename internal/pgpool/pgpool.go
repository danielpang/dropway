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
// so the budget can be retuned without a code change. One connection is kept warm
// (MinConns) so a quiet process — the MCP /healthz ping in particular — does not
// dial Postgres inside its own request deadline. Connections above that floor are
// released (MaxConnIdleTime) so pooler slots free up between bursts, and connections
// are recycled (MaxConnLifetime, with jitter) so none lingers stale through the
// pooler and a burst of connections opened together does not all die on the same
// acquire. Runtime URLs should point at the TRANSACTION-mode pooler (Supabase port
// 6543), which multiplexes these onto few backends; the direct/session URL is only
// needed for migrations.
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

const (
	// maxConnIdleTime bounds how long a connection above MinConns occupies a
	// pooler slot after a burst. It is shorter than a quiet minute on purpose.
	maxConnIdleTime = 30 * time.Second
	// maxConnLifetime recycles connections so none sits on the transaction
	// pooler until the pooler or a middleware kills it first.
	maxConnLifetime = 30 * time.Minute
	// maxConnLifetimeJitter spreads that expiry. With jitter left at zero, every
	// connection opened in the same burst reaches MaxConnLifetime together and
	// the next wave of acquires all dial at once.
	maxConnLifetimeJitter = 5 * time.Minute
	// healthCheckPeriod is how often the pool reaps idle connections and
	// replaces the warm one after its lifetime. It is shorter than the MCP
	// Fly health interval (60s) so that replacement dial runs on the pool's
	// background context, not inside /healthz's 3s budget.
	healthCheckPeriod = 15 * time.Second
	// minConns is the warm floor. Idle reaping destroys connections only while
	// the pool is above this, so the last connection stays checked-in and the
	// next acquire does not pay for a dial.
	minConns int32 = 1
)

// New builds a pgxpool with an explicit connection cap. defaultMaxConns is the
// service's chosen ceiling; the DB_MAX_CONNS env var overrides it when set to a
// positive integer. The returned pool must be Close()d by the caller.
func New(ctx context.Context, url string, defaultMaxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("pgpool: parse config: %w", err)
	}
	configurePool(cfg, defaultMaxConns)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	go monitorSaturation(ctx, pool)
	return pool, nil
}

// configurePool applies the shared budget. It does not dial.
func configurePool(cfg *pgxpool.Config, defaultMaxConns int32) {
	cfg.MaxConns = maxConnsFromEnv(defaultMaxConns)
	cfg.MinConns = minConns
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnLifetimeJitter = maxConnLifetimeJitter
	cfg.HealthCheckPeriod = healthCheckPeriod
}

// monitorSaturation samples the pool on a ticker. It logs when the pool is
// actually out of connections, and it logs at Warn — not Error — when an
// acquire was canceled while the pool still had room. A canceled acquire does
// not by itself prove the pool was exhausted: puddle counts it when the
// caller's context ends while a new connection is being dialed, and that dial
// keeps going in the background. The MCP /healthz ping (3s timeout, every 60s)
// is the steady source of those cancels whenever the pool has no warm
// connection. Error is reserved for a pool that cannot hand out or dial
// another connection, because slog.Error is mirrored to error tracking.
// It only logs on a non-zero delta, so a healthy pool stays silent.
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
			sample := poolSample{
				waited:       empty - lastEmpty,
				canceled:     canceled - lastCanceled,
				acquired:     s.AcquiredConns(),
				idle:         s.IdleConns(),
				total:        s.TotalConns(),
				constructing: s.ConstructingConns(),
				max:          s.MaxConns(),
			}
			lastEmpty, lastCanceled = empty, canceled
			logSaturation(slog.Default(), sample)
		}
	}
}

// poolSample is one monitor interval. waited and canceled are deltas;
// the connection counts are the snapshot at the end of the interval.
type poolSample struct {
	waited       int64
	canceled     int64
	acquired     int32
	idle         int32
	total        int32
	constructing int32
	max          int32
}

type reportKind int

const (
	reportNone reportKind = iota
	// reportWait: a successful acquire blocked because every slot was busy.
	reportWait
	// reportCallerCancel: an acquire's context ended and the pool still had
	// room to serve or dial. Typical cause is a short caller deadline during
	// a cold dial, or a request that was already canceled.
	reportCallerCancel
	// reportExhausted: an acquire was canceled and the pool had nothing idle
	// and no room to dial.
	reportExhausted
)

const (
	exhaustedMsg    = "postgres pool exhausted: acquires canceled while waiting for a connection"
	callerCancelMsg = "postgres acquire canceled by caller while dialing; pool had spare capacity"
	saturatedMsg    = "postgres pool saturated: acquisitions waited for a free connection"
)

// classifySaturation decides whether a sample is a real capacity problem.
//
// EmptyAcquireCount (waited) increments for every successful acquire that had
// to construct a connection, including a cold dial while the pool was nowhere
// near its cap. It is not, on its own, evidence of saturation. CanceledAcquireCount
// increments when the caller's context ends, including when the caller gives
// up during a dial that the pool then finishes in the background.
//
// The snapshot is taken at the end of the interval (up to 30s after the
// acquire), so a spike that fully drains can look idle. A pool that is still
// full, or that has no idle connection and no free slot, is treated as exhausted.
func classifySaturation(s poolSample) reportKind {
	if s.waited == 0 && s.canceled == 0 {
		return reportNone
	}
	if s.canceled > 0 && poolAtCap(s) {
		return reportExhausted
	}
	if s.canceled > 0 {
		return reportCallerCancel
	}
	if poolAtCap(s) {
		return reportWait
	}
	return reportNone
}

// poolAtCap reports whether an acquire would have to wait for some other
// caller to finish. An idle connection can be handed out immediately, and a
// pool below its max can dial. Neither of those is exhaustion.
func poolAtCap(s poolSample) bool {
	if s.max <= 0 {
		return false
	}
	return s.idle <= 0 && s.total >= s.max
}

func logSaturation(log *slog.Logger, s poolSample) {
	kind := classifySaturation(s)
	if kind == reportNone {
		return
	}
	attrs := []any{
		"waited_acquires", s.waited,
		"canceled_acquires", s.canceled,
		"acquired_conns", s.acquired,
		"idle_conns", s.idle,
		"total_conns", s.total,
		"constructing_conns", s.constructing,
		"max_conns", s.max,
	}
	switch kind {
	case reportExhausted:
		// "err" is what posthog-go's slog bridge copies into the exception
		// value. Without it the issue is titled from the message and the
		// value is "<no linked error>", so the counts never show up in the
		// issue itself.
		attrs = append(attrs, "err", saturationCause(s))
		log.Error(exhaustedMsg, attrs...)
	case reportCallerCancel:
		log.Warn(callerCancelMsg, attrs...)
	case reportWait:
		log.Warn(saturatedMsg, attrs...)
	}
}

func saturationCause(s poolSample) error {
	return fmt.Errorf("canceled_acquires=%d waited_acquires=%d acquired_conns=%d idle_conns=%d total_conns=%d constructing_conns=%d max_conns=%d",
		s.canceled, s.waited, s.acquired, s.idle, s.total, s.constructing, s.max)
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
