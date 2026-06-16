// Package ops exposes the operator entrypoints for Dropway's Phase-4 maintenance
// jobs — the R2 version GC and the DR projection rebuild — wrapped so the `dropway`
// CLI (rooted at cli/, which cannot import services/api/internal/*) can drive them
// (docs/ARCHITECTURE.md §12 R2 version GC, §13 row 8 KV/D1 rebuild-from-Postgres DR
// drill).
//
// It builds the SAME non-BYPASSRLS store, S3/R2 storage, and KV/local projection
// writers the API server uses from the SAME environment variables (config.Load),
// so an operator runs `dropway gc` / `dropway dr rebuild` with the deployment's
// existing env and gets behavior identical to the running server. It imports only
// core packages (no cloud/ee), keeping the open-core boundary clean.
package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielpang/dropway/internal/projection"
	"github.com/danielpang/dropway/internal/quota"
	"github.com/danielpang/dropway/internal/storage"
	"github.com/danielpang/dropway/services/api/internal/config"
	"github.com/danielpang/dropway/services/api/internal/store"
)

// Env is the resolved operator environment: a DB pool + store, optional object
// storage (needed for GC) and a projection writer (needed for DR rebuild). Build it
// with Open and Close it when done.
type Env struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	Store *store.Store
	// Objects is the R2/S3 object store (nil when S3_BUCKET is unset — GC then errs).
	Objects storage.Store
	// Projection is the edge-projection writer (Cloudflare KV in prod, local in dev).
	Projection projection.Writer
}

// Open loads config from the environment and builds the operator Env: a pgx pool as
// the non-BYPASSRLS dropway_app role, the store, object storage (if configured), and
// the projection writer. The caller MUST Close it.
func Open(ctx context.Context) (*Env, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.DatabaseURL == "" {
		return nil, errors.New("ops: DATABASE_URL is required (the maintenance jobs read Postgres)")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("ops: connect db: %w", err)
	}
	e := &Env{
		cfg:        cfg,
		pool:       pool,
		Store:      store.New(pool, quota.Unlimited{}),
		Projection: newProjectionWriter(cfg),
	}
	if cfg.S3Bucket != "" {
		obj, err := storage.NewS3Store(ctx, storage.S3Config{
			Bucket:          cfg.S3Bucket,
			Region:          cfg.S3Region,
			Endpoint:        cfg.S3Endpoint,
			PublicEndpoint:  cfg.S3PublicEndpoint,
			AccessKeyID:     cfg.S3AccessKeyID,
			SecretAccessKey: cfg.S3SecretAccessKey,
			UsePathStyle:    cfg.S3ForcePathStyle,
		})
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("ops: object storage: %w", err)
		}
		e.Objects = obj
	}
	return e, nil
}

// Close releases the DB pool.
func (e *Env) Close() {
	if e.pool != nil {
		e.pool.Close()
	}
}

// GCParams configures the R2 version GC run.
type GCParams struct {
	// OrgID, when set, GCs only that org; empty GCs EVERY org.
	OrgID string
	// KeepLastN versions per site to retain in addition to the live version.
	KeepLastN int
	// MinAge is the age guard: an orphan blob is only deleted once older than this
	// (presign TTL + safety margin). Zero uses store.DefaultGCMinAge — the safe
	// default that keeps an in-flight deploy's just-uploaded blobs from being GC'd.
	MinAge time.Duration
	// DryRun reports orphans without deleting.
	DryRun bool
}

// GCResult is the ops-level summary of one org's GC pass. It is a stable, public
// shape the CLI prints without importing the internal store.
type GCResult struct {
	OrgID            string
	RetainedVersions int
	ReferencedBlobs  int
	ScannedBlobs     int
	OrphanCount      int
	SkippedFresh     int // unreferenced blobs spared by the age guard (in-flight deploys)
	Deleted          int
}

// RebuildResult is the ops-level summary of a DR rebuild.
type RebuildResult struct {
	Orgs   int
	Routes int
}

// GC runs the R2 version GC. With OrgID set it GCs that org; otherwise every org.
// It returns one GCResult per org processed.
func (e *Env) GC(ctx context.Context, p GCParams) ([]GCResult, error) {
	if e.Objects == nil {
		return nil, errors.New("ops: GC needs object storage (set S3_BUCKET + S3_* env)")
	}
	pol := store.GCPolicy{KeepLastN: p.KeepLastN, MinAge: p.MinAge, DryRun: p.DryRun}

	var raw []store.GCResult
	if p.OrgID != "" {
		r, err := e.Store.GCOrg(ctx, e.Objects, p.OrgID, pol)
		if err != nil {
			return nil, err
		}
		raw = []store.GCResult{r}
	} else {
		rs, err := e.Store.GCAllOrgs(ctx, e.Objects, pol)
		if err != nil {
			return nil, err
		}
		raw = rs
	}

	out := make([]GCResult, len(raw))
	for i, r := range raw {
		out[i] = GCResult{
			OrgID:            r.OrgID,
			RetainedVersions: r.RetainedVersions,
			ReferencedBlobs:  r.ReferencedBlobs,
			ScannedBlobs:     r.ScannedBlobs,
			OrphanCount:      len(r.Orphans),
			SkippedFresh:     r.SkippedFresh,
			Deleted:          r.Deleted,
		}
	}
	return out, nil
}

// RebuildProjection runs the DR drill: rebuild the entire KV/D1 routing projection
// from Postgres across ALL orgs and push it through the projection writer.
func (e *Env) RebuildProjection(ctx context.Context) (RebuildResult, error) {
	r, err := e.Store.RebuildAllOrgs(ctx, e.Projection)
	if err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Orgs: r.Orgs, Routes: r.Routes}, nil
}

// newProjectionWriter mirrors the API server's selection: Cloudflare KV when the
// CF_* creds are present (production), else a local writer (optionally mirrored to
// PROJECTION_FILE) for dev/self-host. The DR rebuild MUST target the SAME writer the
// server uses or it would rebuild the wrong projection.
func newProjectionWriter(cfg config.Config) projection.Writer {
	if cfg.CFAccountID != "" && cfg.CFKVNamespaceID != "" && cfg.CFAPIToken != "" {
		return projection.NewCloudflareKV(cfg.CFAccountID, cfg.CFKVNamespaceID, cfg.CFAPIToken)
	}
	if cfg.ProjectionFilePath != "" {
		if l, err := projection.NewLocalFile(cfg.ProjectionFilePath); err == nil {
			return l
		}
	}
	return projection.NewLocal()
}
