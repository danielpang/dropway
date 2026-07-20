package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
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

	cmd.AddCommand(newSitesDeleteCmd(readFactory))
	return cmd
}

// newSitesDeleteCmd builds `dropway sites delete <id-or-slug>`: permanently
// remove a site and all its versions. Prompts for confirmation unless --yes is
// passed (required in CI, where there is no interactive stdin).
func newSitesDeleteCmd(readFactory func(baseURL, token string) api.ReadClient) *cobra.Command {
	var (
		yes     bool
		baseURL string
	)
	cmd := &cobra.Command{
		Use:   "delete <id-or-slug>",
		Short: "Permanently delete a site and all its versions",
		Long: "Delete a site by id or slug. This removes every version and its live URL and\n" +
			"cannot be undone. You can delete a site you own; deleting someone else's needs\n" +
			"an org admin. Use --yes to skip the prompt in CI.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("sites delete: %w", err)
			}
			client := readFactory(baseURL, token)

			resp, err := client.ListSites(ctx)
			if err != nil {
				return fmt.Errorf("sites delete: %w", err)
			}
			site, err := findSite(resp.Sites, args[0])
			if err != nil {
				return fmt.Errorf("sites delete: %w", err)
			}

			if !yes {
				fmt.Fprintf(out, "Permanently delete site %q (%s)? This cannot be undone. [y/N]: ", site.Slug, site.ID)
				if !confirmed(cmd.InOrStdin()) {
					fmt.Fprintln(out, "Aborted.")
					return nil
				}
			}

			if err := client.DeleteSite(ctx, site.ID); err != nil {
				return fmt.Errorf("sites delete: %w", err)
			}
			fmt.Fprintf(out, "Deleted site %q (%s).\n", site.Slug, site.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (required in CI)")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// findSite resolves a site by exact id or exact slug within the org's sites.
func findSite(sites []api.Site, idOrSlug string) (api.Site, error) {
	for _, s := range sites {
		if s.ID == idOrSlug || s.Slug == idOrSlug {
			return s, nil
		}
	}
	return api.Site{}, fmt.Errorf("no site with id or slug %q in this org", idOrSlug)
}

// confirmed reads one line and reports whether it is an affirmative (y/yes). An
// empty line or EOF (e.g. non-interactive CI without --yes) is a no.
func confirmed(in io.Reader) bool {
	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(sc.Text())) {
	case "y", "yes":
		return true
	default:
		return false
	}
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
