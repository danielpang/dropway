// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/auth"
)

// newLoginCmd builds `dropway login`: open the browser, sign in, and store
// credentials so later commands authenticate without a token to copy.
func newLoginCmd() *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Dropway in your browser",
		Long: "Open a browser tab to sign in to Dropway. After you approve, the CLI\n" +
			"stores credentials locally and refreshes them automatically, so `dropway\n" +
			"deploy` just works. For CI, set DROPWAY_TOKEN instead (it takes precedence).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			creds, err := auth.Login(ctx, baseURL, func(url string) error {
				fmt.Fprintf(out, "Opening your browser to sign in:\n  %s\n\n", url)
				if err := auth.OpenBrowser(url); err != nil {
					fmt.Fprintln(out, "Couldn't open a browser automatically — open the URL above.")
				}
				fmt.Fprintln(out, "Waiting for authorization…")
				return nil
			})
			if err != nil {
				return err
			}
			if err := auth.Save(creds); err != nil {
				return fmt.Errorf("login: save credentials: %w", err)
			}
			path, _ := auth.CredentialsPath()
			fmt.Fprintf(out, "\nSigned in to %s.\nCredentials saved to %s\n", creds.APIBase, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// newLogoutCmd builds `dropway logout`: remove stored credentials.
func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored Dropway credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := auth.Delete(); err != nil {
				return fmt.Errorf("logout: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Signed out.")
			return nil
		},
	}
}
