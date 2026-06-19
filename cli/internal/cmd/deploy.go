// Package cmd assembles the `dropway` CLI (cobra). Phase 1 ships `deploy`, which
// implements the full folder → live URL flow against the API:
// walk + hash → (create site) → prepare → upload
// only-missing blobs to presigned URLs → finalize → publish. The dry run (no
// --send) prints the plan without any network so it stays useful offline.
package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
	"github.com/danielpang/dropway/cli/internal/manifest"
)

// tokenEnv is the env var carrying a Bearer deploy token (CI / non-interactive).
const tokenEnv = "DROPWAY_TOKEN"

// newDeployCmd builds the `dropway deploy <dir>` command. clientFactory is
// injected so tests can supply a fake api.Client; the default builds the real
// HTTP client from flags + env.
func newDeployCmd(clientFactory func(baseURL, token string) api.Client) *cobra.Command {
	var (
		site      string
		siteID    string
		createNew bool
		baseURL   string
		send      bool
	)

	cmd := &cobra.Command{
		Use:   "deploy <dir>",
		Short: "Deploy a folder of static files to a live, access-controlled URL",
		Long: "Walk <dir>, compute a SHA-256 per file, and (with --send) run the full deploy:\n" +
			"  prepare → upload only-changed blobs → finalize → publish → print the live URL.\n" +
			"Without --send it prints the plan (the manifest it would upload) with no network.\n" +
			"For --send, sign in first with `dropway login` (or set " + tokenEnv + " for CI).\n" +
			"Target a site with --site-id, or --new --site <slug>.",
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
			files := api.ManifestFromBuild(m)

			fmt.Fprintf(out, "Deploying %q\n  %s\n\n", dir, m.Summary())

			// 2. Without --send, print the plan and stop (a dry run by design).
			if !send {
				printPlan(out, files)
				fmt.Fprintln(out, "\nThis was a dry run — nothing was uploaded and no site was created.")
				fmt.Fprintln(out, "To deploy for real, sign in once with `dropway login`, then re-run with --send:")
				fmt.Fprintf(out, "  dropway deploy %q --new --site <slug> --send   # create a new site\n", dir)
				fmt.Fprintf(out, "  dropway deploy %q --site-id <id> --send        # deploy to an existing site\n", dir)
				return nil
			}

			// 3. --send: resolve auth (DROPWAY_TOKEN, else the stored `dropway login`
			// credentials, refreshing as needed) + require a target site.
			ctx := context.Background()
			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("deploy: %w", err)
			}
			if siteID == "" && !createNew {
				return fmt.Errorf("deploy: --send requires --site-id <id>, or --new --site <slug> to create one")
			}
			client := clientFactory(baseURL, token)

			// 3a. Create the site first if requested.
			if createNew {
				if site == "" {
					return fmt.Errorf("deploy: --new requires --site <slug>")
				}
				s, err := client.CreateSite(ctx, api.CreateSiteRequest{Slug: site})
				if err != nil {
					return fmt.Errorf("create site: %w", err)
				}
				siteID = s.ID
				fmt.Fprintf(out, "Created site %s (%s)\n", s.Slug, s.ID)
			}

			// 4. Prepare: learn which blobs need upload.
			prep, err := client.PrepareDeployment(ctx, siteID, api.PrepareRequest{Manifest: files})
			if err != nil {
				return fmt.Errorf("prepare: %w", err)
			}
			fmt.Fprintf(out, "Prepared: %d/%d blob(s) need upload\n", len(prep.Missing), len(files))

			// 5. Upload only the missing blobs to their presigned URLs.
			if err := uploadMissing(ctx, client, dir, m, prep); err != nil {
				return err
			}

			// 6. Finalize: server verifies blobs, writes the manifest + version.
			fin, err := client.FinalizeDeployment(ctx, siteID, api.FinalizeRequest{
				Manifest: files,
				Digest:   m.Digest,
			})
			if err != nil {
				return fmt.Errorf("finalize: %w", err)
			}
			fmt.Fprintf(out, "Finalized version %s (v%d)\n", fin.VersionID, fin.VersionNo)

			// 7. Publish: flip the pointer + project the route to the edge.
			pub, err := client.Publish(ctx, siteID, api.PublishRequest{VersionID: fin.VersionID})
			if err != nil {
				return fmt.Errorf("publish: %w", err)
			}
			if pub.LiveURL == "" {
				fmt.Fprintln(out, "\n✓ Deploy successful (the API returned no live URL — check the dashboard)")
				return nil
			}
			fmt.Fprintf(out, "\n✓ Deploy successful\n  Live at %s\n", pub.LiveURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "site slug (with --new) to create")
	cmd.Flags().StringVar(&siteID, "site-id", "", "existing site id to deploy to")
	cmd.Flags().BoolVar(&createNew, "new", false, "create a new site (requires --site <slug>)")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	cmd.Flags().BoolVar(&send, "send", false, "actually run the deploy (sign in first with `dropway login`)")
	return cmd
}

// uploadMissing reads each missing blob's bytes from disk and PUTs them to the
// presigned URL the server returned. Only blobs the server doesn't already have
// are uploaded (only-changed-blob upload). A blob may back multiple paths;
// we find the first file with the matching hash.
func uploadMissing(ctx context.Context, client api.Client, dir string, m *manifest.Manifest, prep *api.PrepareResponse) error {
	// Index manifest entries by sha so we can locate a file path per missing sha.
	pathBySHA := make(map[string]string, len(m.Files))
	for _, e := range m.Files {
		if _, ok := pathBySHA[e.SHA256]; !ok {
			pathBySHA[e.SHA256] = e.Path
		}
	}
	for _, sha := range prep.Missing {
		url, ok := prep.Uploads[sha]
		if !ok {
			return fmt.Errorf("upload: server listed %s missing but gave no upload URL", sha)
		}
		relPath, ok := pathBySHA[sha]
		if !ok {
			return fmt.Errorf("upload: no local file matches blob %s", sha)
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err != nil {
			return fmt.Errorf("upload: read %s: %w", relPath, err)
		}
		if err := client.UploadBlob(ctx, url, data); err != nil {
			return fmt.Errorf("upload %s: %w", relPath, err)
		}
	}
	return nil
}

// printPlan writes the manifest the deploy would upload (the dry-run output).
func printPlan(out interface{ Write([]byte) (int, error) }, files []api.ManifestFile) {
	fmt.Fprintf(out, "Manifest (%d files):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(out, "  %s  %s  (%d bytes, %s)\n", f.SHA256[:12], f.Path, f.Size, f.ContentType)
	}
}

// defaultAPIBase resolves the API base from DROPWAY_API or the production default.
func defaultAPIBase() string {
	if v := os.Getenv("DROPWAY_API"); v != "" {
		return v
	}
	return "https://api.dropway.dev"
}

// defaultClientFactory builds the real HTTP client.
func defaultClientFactory(baseURL, token string) api.Client {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
