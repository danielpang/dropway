// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// newDRCmd builds `shipped dr` with the `rebuild` subcommand — the documented
// disaster-recovery drill (ARCHITECTURE.md §13 row 8): rebuild the ENTIRE KV/D1
// routing projection from Postgres across every org and push it to the edge writer,
// restoring serving after a KV/D1 wipe. Postgres is authoritative; the projection is
// a rebuildable cache.
//
// The factory is injected so tests can supply a fake operator env; the default wires
// ops.Open (DATABASE_URL + CF_*/PROJECTION_FILE from the deployment env).
func newDRCmd(factory func(context.Context) (opsRunner, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dr",
		Short: "Disaster-recovery maintenance jobs",
		Long:  "Operator jobs for disaster recovery. `dr rebuild` rebuilds the edge routing projection from Postgres.",
	}
	cmd.AddCommand(newDRRebuildCmd(factory))
	return cmd
}

// newDRRebuildCmd builds `shipped dr rebuild`.
func newDRRebuildCmd(factory func(context.Context) (opsRunner, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the entire KV/D1 routing projection from Postgres (DR drill)",
		Long: "Enumerate every org, collect each org's live routes from Postgres under its own\n" +
			"RLS tenant context, and replay the whole set to the edge projection writer\n" +
			"(Cloudflare KV in production, a local writer/PROJECTION_FILE in dev). Use after a\n" +
			"KV/D1 wipe to restore serving. Reads DATABASE_URL + CF_* (or PROJECTION_FILE).",
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

			res, err := runner.RebuildProjection(ctx)
			if err != nil {
				return fmt.Errorf("dr rebuild: %w", err)
			}
			fmt.Fprintf(out, "DR rebuild complete: %d route(s) across %d org(s) re-pushed to the edge projection\n",
				res.Routes, res.Orgs)
			return nil
		},
	}
	return cmd
}
