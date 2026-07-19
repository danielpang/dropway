package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
)

// newWhoamiCmd builds `dropway whoami`: print the authenticated identity (user,
// org, role) and WHICH credential source is active — an API key from
// DROPWAY_API_KEY, or the stored `dropway login`. The credential source is
// reported so a CI run's precedence (env key wins over a stored session) is never
// a surprise. readFactory is injected so tests can supply a fake ReadClient.
func newWhoamiCmd(readFactory func(baseURL, token string) api.ReadClient) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show who you're authenticated as and which credential is in use",
		Long: "Print your Dropway identity (user, org, role) and the active credential\n" +
			"source: an API key from " + tokenEnv + " (which takes precedence), or your\n" +
			"stored `dropway login` session.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("whoami: %w", err)
			}
			me, err := readFactory(baseURL, token).Me(ctx)
			if err != nil {
				return fmt.Errorf("whoami: %w", err)
			}

			keyed := auth.UsingAPIKey()
			source := "interactive login"
			if keyed {
				source = "API key (" + tokenEnv + ")"
			}
			fmt.Fprintf(out, "User:  %s\n", me.UserID)
			fmt.Fprintf(out, "Org:   %s\n", me.OrgID)
			if me.Role != "" {
				// /v1/me returns the key creator's role; a keyed session is
				// capped at member-level regardless, so say so rather than
				// implying the key can act as (say) an owner.
				if keyed {
					fmt.Fprintf(out, "Role:  %s (API keys are limited to member-level actions)\n", me.Role)
				} else {
					fmt.Fprintf(out, "Role:  %s\n", me.Role)
				}
			}
			fmt.Fprintf(out, "Auth:  %s\n", source)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}
