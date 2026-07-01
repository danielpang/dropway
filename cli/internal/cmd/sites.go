package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
)

// newSitesCmd builds `dropway sites`: list the sites the caller owns, or every
// site in the org with --all. readFactory is injected so tests can supply a fake
// ReadClient without a live server.
func newSitesCmd(readFactory func(baseURL, token string) api.ReadClient) *cobra.Command {
	var (
		all     bool
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "sites",
		Short: "List your Dropway sites (or every site in the org with --all)",
		Long: "List sites in your active org. By default only the sites you own are shown;\n" +
			"pass --all to list every site in the org.\n" +
			"Sign in first with `dropway login` (or set " + tokenEnv + " for CI).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("sites: %w", err)
			}
			client := readFactory(baseURL, token)

			resp, err := client.ListSites(ctx)
			if err != nil {
				return fmt.Errorf("sites: %w", err)
			}
			// The caller's id both filters the personal view and labels "you" in the
			// org-wide view. It's one cheap call, so fetch it in either mode.
			me, err := client.Me(ctx)
			if err != nil {
				return fmt.Errorf("sites: %w", err)
			}

			sites := resp.Sites
			if !all {
				owned := make([]api.Site, 0, len(sites))
				for _, s := range sites {
					if s.OwnerID == me.UserID {
						owned = append(owned, s)
					}
				}
				sites = owned
			}

			if len(sites) == 0 {
				if all {
					fmt.Fprintln(out, "No sites in this org yet.")
				} else {
					fmt.Fprintln(out, "You don't own any sites yet. Try `dropway sites --all` to see the whole org.")
				}
				return nil
			}

			sort.Slice(sites, func(i, j int) bool { return sites[i].Slug < sites[j].Slug })
			printSites(out, sites, all, me.UserID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "list every site in the org, not just the ones you own")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// printSites renders an aligned site table. The org-wide view adds an OWNER
// column ("you" for the caller's own sites, else a short owner id).
func printSites(out io.Writer, sites []api.Site, all bool, meID string) {
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	if all {
		fmt.Fprintln(tw, "SLUG\tOWNER\tACCESS\tLIVE URL")
		for _, s := range sites {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Slug, ownerLabel(s.OwnerID, meID), s.AccessMode, s.LiveURL)
		}
	} else {
		fmt.Fprintln(tw, "SLUG\tACCESS\tLIVE URL")
		for _, s := range sites {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Slug, s.AccessMode, s.LiveURL)
		}
	}
	_ = tw.Flush()
}

// ownerLabel shows "you" for the caller's own sites, else a short prefix of the
// owner id (the CLI doesn't resolve ids to emails).
func ownerLabel(ownerID, meID string) string {
	if ownerID != "" && ownerID == meID {
		return "you"
	}
	if len(ownerID) > 10 {
		return ownerID[:10] + "…"
	}
	return ownerID
}

// defaultReadClientFactory builds the real read-only HTTP client.
func defaultReadClientFactory(baseURL, token string) api.ReadClient {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
