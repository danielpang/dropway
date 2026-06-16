// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/services/api/ops"
)

// opsRunner is the operator surface the gc/dr commands need — the subset of *ops.Env
// they call. Defining it as an interface keeps the commands unit-testable with a
// fake (no live Postgres/R2). *ops.Env satisfies it.
type opsRunner interface {
	GC(ctx context.Context, p ops.GCParams) ([]ops.GCResult, error)
	RebuildProjection(ctx context.Context) (ops.RebuildResult, error)
	Close()
}

// defaultOpsFactory builds the real operator env from the deployment environment
// (DATABASE_URL + S3_* + CF_*). Injected so tests can supply a fake.
func defaultOpsFactory(ctx context.Context) (opsRunner, error) {
	return ops.Open(ctx)
}

// newGCCmd builds `dropway gc`: the R2 version GC operator job. It deletes blobs no
// longer referenced by any retained deployment manifest, honoring a retention policy
// (keep current + last N versions per site), per ARCHITECTURE.md §12.
//
// It is an OPERATOR command (not the deploy-token API path): it reads the deployment
// env (DATABASE_URL + S3_*), connecting to Postgres as the non-BYPASSRLS dropway_app
// role, and scopes every blob list/delete to an org's prefix.
func newGCCmd(factory func(context.Context) (opsRunner, error)) *cobra.Command {
	var (
		org    string
		keep   int
		minAge time.Duration
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage-collect orphaned R2 blobs (keep current + last N versions)",
		Long: "Delete content-addressed blobs no longer referenced by any retained deploy\n" +
			"manifest. Retention keeps each site's current (live) version plus the last N\n" +
			"versions; the current version's blobs are NEVER deleted. Reads DATABASE_URL +\n" +
			"S3_* from the environment; --dry-run reports orphans without deleting.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()

			runner, err := factory(ctx)
			if err != nil {
				return err
			}
			defer runner.Close()

			results, err := runner.GC(ctx, ops.GCParams{OrgID: org, KeepLastN: keep, MinAge: minAge, DryRun: dryRun})
			if err != nil {
				return fmt.Errorf("gc: %w", err)
			}

			mode := "deleted"
			if dryRun {
				mode = "would delete (dry run)"
			}
			var totalOrphans, totalScanned, totalFresh int
			for _, r := range results {
				totalOrphans += r.OrphanCount
				totalScanned += r.ScannedBlobs
				totalFresh += r.SkippedFresh
				fmt.Fprintf(out, "org %s: scanned %d blob(s), retained %d version(s), %d referenced, %s %d orphan(s), spared %d fresh\n",
					r.OrgID, r.ScannedBlobs, r.RetainedVersions, r.ReferencedBlobs, mode, r.OrphanCount, r.SkippedFresh)
			}
			fmt.Fprintf(out, "\nGC complete: %d org(s), %d blob(s) scanned, %d orphan(s) %s, %d fresh blob(s) spared (age guard)\n",
				len(results), totalScanned, totalOrphans, mode, totalFresh)
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "GC only this org id (default: every org)")
	cmd.Flags().IntVar(&keep, "keep", 5, "versions per site to retain in addition to the live version")
	cmd.Flags().DurationVar(&minAge, "min-age", 0, "only delete orphan blobs older than this (0 → safe default: presign TTL + 1h)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report orphans without deleting")
	return cmd
}
