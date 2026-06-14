// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package ops

import (
	"path/filepath"
	"testing"

	"github.com/danielpang/shipped/internal/projection"
	"github.com/danielpang/shipped/services/api/internal/config"
)

// The DB/R2-bound operator entrypoints (Open, GC, RebuildProjection) are exercised
// by the docker INTEGRATION suite. This file unit-tests the pure decision logic the
// ops package owns: newProjectionWriter's config-driven selection (the DR rebuild
// MUST target the SAME writer the server uses, so the selection must match the
// server's) and the lifecycle resilience of Close on a not-fully-built Env.

// ---------------------------------------------------------------------------
// newProjectionWriter — Cloudflare KV when the full CF_* creds are present, else a
// local writer (file-backed when PROJECTION_FILE is set, in-memory otherwise).
// ---------------------------------------------------------------------------

func TestNewProjectionWriter_CloudflareKVWhenCredsPresent(t *testing.T) {
	cfg := config.Config{
		CFAccountID:     "acct-1",
		CFKVNamespaceID: "ns-1",
		CFAPIToken:      "tok-1",
	}
	w := newProjectionWriter(cfg)
	if _, ok := w.(*projection.CloudflareKV); !ok {
		t.Fatalf("full CF_* creds → Cloudflare KV writer, got %T", w)
	}
}

func TestNewProjectionWriter_PartialCFCreds_FallBackToLocal(t *testing.T) {
	// A partial CF config (missing the API token) must NOT select Cloudflare KV — it
	// would fail at write time. It falls back to a local writer.
	cfg := config.Config{
		CFAccountID:     "acct-1",
		CFKVNamespaceID: "ns-1",
		// CFAPIToken intentionally empty.
	}
	w := newProjectionWriter(cfg)
	if _, ok := w.(*projection.CloudflareKV); ok {
		t.Fatal("partial CF creds must NOT select Cloudflare KV")
	}
	if _, ok := w.(*projection.Local); !ok {
		t.Fatalf("partial CF creds → local writer, got %T", w)
	}
}

func TestNewProjectionWriter_FileBackedWhenProjectionFileSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projection.json")
	cfg := config.Config{ProjectionFilePath: path}
	w := newProjectionWriter(cfg)
	// NewLocalFile returns a *projection.Local backed by the file.
	if _, ok := w.(*projection.Local); !ok {
		t.Fatalf("PROJECTION_FILE set → local (file-backed) writer, got %T", w)
	}
}

func TestNewProjectionWriter_InMemoryWhenNothingConfigured(t *testing.T) {
	w := newProjectionWriter(config.Config{})
	if _, ok := w.(*projection.Local); !ok {
		t.Fatalf("no CF creds / no file → in-memory local writer, got %T", w)
	}
	if w == nil {
		t.Fatal("newProjectionWriter must never return nil (the publish path dereferences it)")
	}
}

func TestNewProjectionWriter_BadProjectionFilePath_FallsBackToInMemory(t *testing.T) {
	// An unwritable PROJECTION_FILE path (a file under a non-existent dir) makes
	// NewLocalFile fail; the selection must degrade to the in-memory writer rather
	// than return nil and crash the publish path.
	cfg := config.Config{ProjectionFilePath: filepath.Join(t.TempDir(), "no-such-dir", "p.json")}
	w := newProjectionWriter(cfg)
	if w == nil {
		t.Fatal("a bad PROJECTION_FILE must fall back to a non-nil in-memory writer")
	}
	if _, ok := w.(*projection.Local); !ok {
		t.Fatalf("bad file path → local in-memory writer, got %T", w)
	}
}

// ---------------------------------------------------------------------------
// Env.Close must be safe even when the pool was never opened (a partially-built
// Env, e.g. after an Open failure path).
// ---------------------------------------------------------------------------

func TestEnv_Close_NilPoolIsSafe(t *testing.T) {
	e := &Env{} // pool is nil
	// Must not panic.
	e.Close()
}
