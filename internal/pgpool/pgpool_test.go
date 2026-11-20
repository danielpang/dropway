// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package pgpool

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMaxConnsFromEnv covers the DB_MAX_CONNS override: a valid positive integer
// wins, and every invalid/absent form falls back to the service's default.
func TestMaxConnsFromEnv(t *testing.T) {
	const def int32 = 8
	cases := []struct {
		name string
		set  bool
		val  string
		want int32
	}{
		{"unset falls back", false, "", def},
		{"empty falls back", true, "", def},
		{"valid override", true, "3", 3},
		{"zero falls back", true, "0", def},
		{"negative falls back", true, "-2", def},
		{"non-numeric falls back", true, "lots", def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("DB_MAX_CONNS", tc.val)
			} else {
				// t.Setenv can't unset; the test binary's env has no DB_MAX_CONNS,
				// so the unset case relies on it being absent by default.
			}
			if got := maxConnsFromEnv(def); got != tc.want {
				t.Errorf("maxConnsFromEnv(%d) with DB_MAX_CONNS=%q = %d, want %d", def, tc.val, got, tc.want)
			}
		})
	}
}

func TestConfigurePoolKeepsOneWarmConnAndSpreadsLifetime(t *testing.T) {
	cfg := mustParsePoolConfig(t)
	configurePool(cfg, 8)

	if cfg.MaxConns != 8 {
		t.Errorf("MaxConns = %d, want 8", cfg.MaxConns)
	}
	if cfg.MinConns != 1 {
		t.Errorf("MinConns = %d, want 1 (warm connection so a health check does not cold-dial)", cfg.MinConns)
	}
	if cfg.MaxConnLifetime != 30*time.Minute {
		t.Errorf("MaxConnLifetime = %s, want 30m", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnLifetimeJitter != 5*time.Minute {
		t.Errorf("MaxConnLifetimeJitter = %s, want 5m", cfg.MaxConnLifetimeJitter)
	}
	if cfg.MaxConnIdleTime != 30*time.Second {
		t.Errorf("MaxConnIdleTime = %s, want 30s", cfg.MaxConnIdleTime)
	}
	if cfg.HealthCheckPeriod != 15*time.Second {
		t.Errorf("HealthCheckPeriod = %s, want 15s", cfg.HealthCheckPeriod)
	}
}

func TestConfigurePoolClampsWarmFloorToCap(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "1")
	cfg := mustParsePoolConfig(t)
	configurePool(cfg, 8)
	if cfg.MaxConns != 1 || cfg.MinConns != 1 {
		t.Errorf("MaxConns=%d MinConns=%d, want both 1 so the floor never exceeds the cap", cfg.MaxConns, cfg.MinConns)
	}
}

func TestConfigurePoolHonorsCapOverride(t *testing.T) {
	t.Setenv("DB_MAX_CONNS", "3")
	cfg := mustParsePoolConfig(t)
	configurePool(cfg, 8)
	if cfg.MaxConns != 3 {
		t.Errorf("MaxConns = %d, want 3 from DB_MAX_CONNS", cfg.MaxConns)
	}
	if cfg.MinConns != 1 {
		t.Errorf("MinConns = %d, want 1 under a cap of 3", cfg.MinConns)
	}
}

func mustParsePoolConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

func TestClassifySaturation(t *testing.T) {
	// Shapes taken from the production issue: every occurrence was the MCP
	// pool, max 4, acquired 0–1, total 0–2, exactly one canceled acquire,
	// and zero of them had acquired_conns >= max_conns.
	cases := []struct {
		name string
		s    poolSample
		want reportKind
	}{
		{"quiet", poolSample{max: 4, idle: 1, total: 1}, reportNone},
		{
			"production caller cancel while idle",
			poolSample{canceled: 1, acquired: 0, idle: 2, total: 2, max: 4},
			reportCallerCancel,
		},
		{
			"production cancel with one successful cold dial",
			poolSample{waited: 1, canceled: 1, acquired: 0, idle: 2, total: 2, max: 4},
			reportCallerCancel,
		},
		{
			"cancel while the pool can still dial",
			poolSample{canceled: 1, acquired: 0, idle: 0, total: 0, max: 4},
			reportCallerCancel,
		},
		{
			"already-canceled request with idle connections",
			poolSample{canceled: 1, acquired: 0, idle: 4, total: 4, max: 4},
			reportCallerCancel,
		},
		{
			"cold dial is not saturation",
			poolSample{waited: 1, acquired: 0, idle: 1, total: 1, max: 4},
			reportNone,
		},
		{
			"waiters blocked on a full pool",
			poolSample{waited: 3, acquired: 4, idle: 0, total: 4, max: 4},
			reportWait,
		},
		{
			"cancels while every connection is checked out",
			poolSample{canceled: 2, acquired: 4, idle: 0, total: 4, max: 4},
			reportExhausted,
		},
		{
			"cancels while every slot is still dialing",
			poolSample{canceled: 1, constructing: 4, idle: 0, total: 4, max: 4},
			reportExhausted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySaturation(tc.s); got != tc.want {
				t.Errorf("classifySaturation(%+v) = %d, want %d", tc.s, got, tc.want)
			}
		})
	}
}

func TestLogSaturationLevels(t *testing.T) {
	cases := []struct {
		name      string
		s         poolSample
		wantLevel string
		wantMsg   string
		wantErr   bool
	}{
		{
			name:      "spare capacity is a warning, not an exception",
			s:         poolSample{canceled: 1, acquired: 0, idle: 2, total: 2, max: 4},
			wantLevel: "WARN",
			wantMsg:   callerCancelMsg,
		},
		{
			name:      "full pool is an error with a linked cause",
			s:         poolSample{canceled: 2, waited: 1, acquired: 4, idle: 0, total: 4, max: 4},
			wantLevel: "ERROR",
			wantMsg:   exhaustedMsg,
			wantErr:   true,
		},
		{
			name:      "waiters on a full pool warn",
			s:         poolSample{waited: 2, acquired: 8, idle: 0, total: 8, max: 8},
			wantLevel: "WARN",
			wantMsg:   saturatedMsg,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := captureSaturationLog(t, tc.s)
			if !strings.Contains(line, `"level":"`+tc.wantLevel+`"`) {
				t.Errorf("level: log = %s", line)
			}
			if !strings.Contains(line, tc.wantMsg) {
				t.Errorf("message: log = %s", line)
			}
			if tc.wantErr {
				if !strings.Contains(line, `"err":"canceled_acquires=2`) || !strings.Contains(line, "acquired_conns=4") || !strings.Contains(line, "max_conns=4") {
					t.Errorf("err attr: log = %s", line)
				}
			} else if strings.Contains(line, `"level":"ERROR"`) {
				t.Errorf("spare-capacity sample logged at ERROR: %s", line)
			}
		})
	}
}

func TestLogSaturationStaysSilentWhenHealthy(t *testing.T) {
	line := captureSaturationLog(t, poolSample{acquired: 0, idle: 1, total: 1, max: 4})
	if line != "" {
		t.Errorf("healthy pool logged %s", line)
	}
}

func captureSaturationLog(t *testing.T, s poolSample) string {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	logSaturation(log, s)
	return strings.TrimSpace(buf.String())
}
