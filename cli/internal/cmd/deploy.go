// Package cmd assembles the `shipped` CLI (cobra). Phase 1 ships `deploy`, which
// builds the content-addressed manifest and prints the request it would POST to
// /v1/deployments/prepare (docs/ARCHITECTURE.md §7.1). The actual upload/publish
// is gated behind --send so the command is useful (and testable) without a
// running server.
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/danielpang/shipped/cli/internal/api"
	"github.com/danielpang/shipped/cli/internal/manifest"
)

// tokenEnv is the env var carrying the Bearer deploy token.
const tokenEnv = "SHIPPED_TOKEN"

// newDeployCmd builds the `shipped deploy <dir>` command. clientFactory is
// injected so tests can supply a fake api.Client; the default builds the real
// HTTP client from flags + env.
func newDeployCmd(clientFactory func(baseURL, token string) api.Client) *cobra.Command {
	var (
		site    string
		baseURL string
		send    bool
	)

	cmd := &cobra.Command{
		Use:   "deploy <dir>",
		Short: "Build a content-addressed manifest for a directory and prepare a deployment",
		Long: "Walk <dir>, compute a SHA-256 per file, build a path→hash manifest, and " +
			"print the deploy summary plus the JSON it would POST to /v1/deployments/prepare. " +
			"Pass --send to actually call the API (requires " + tokenEnv + ").",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			out := cmd.OutOrStdout()

			// 1. Build the manifest (local, no network).
			m, err := manifest.Build(dir)
			if err != nil {
				return err
			}
			if len(m.Files) == 0 {
				return fmt.Errorf("deploy: %q contains no files to deploy", dir)
			}

			req := api.PrepareRequest{
				SiteSlug:  site,
				Digest:    m.Digest,
				Files:     m.Files,
				TotalSize: m.TotalSize,
			}

			// 2. Summary + the JSON body (always printed — this is the plan).
			fmt.Fprintf(out, "Deploying %q\n  %s\n\n", dir, m.Summary())
			body, err := api.MarshalRequest(req)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "POST %s/v1/deployments/prepare\n%s\n", baseURL, body)

			// 3. Without --send, stop here (a dry run by design).
			if !send {
				fmt.Fprintln(out, "\n(dry run — pass --send to upload; set "+tokenEnv+" for auth)")
				return nil
			}

			// 4. --send: require the token, call the API.
			token := os.Getenv(tokenEnv)
			if token == "" {
				return fmt.Errorf("deploy: --send requires %s to be set", tokenEnv)
			}
			client := clientFactory(baseURL, token)
			resp, err := client.PrepareDeployment(context.Background(), req)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "\nPrepared deployment %s: %d/%d blob(s) need upload\n",
				resp.DeploymentID, len(resp.MissingSHA), len(m.Files))
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "target site slug (optional on first deploy)")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Shipped API base URL")
	cmd.Flags().BoolVar(&send, "send", false, "actually call the API (requires "+tokenEnv+")")
	return cmd
}

// defaultAPIBase resolves the API base from SHIPPED_API or the production default.
func defaultAPIBase() string {
	if v := os.Getenv("SHIPPED_API"); v != "" {
		return v
	}
	return "https://api.shipped.app"
}

// defaultClientFactory builds the real HTTP client.
func defaultClientFactory(baseURL, token string) api.Client {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
