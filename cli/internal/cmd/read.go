package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
)

// maxReadBytes caps how much of a fetched body the CLI streams, so `read` on an
// unexpectedly huge response can't run away. Generous for any served document.
const maxReadBytes = 8 << 20 // 8 MiB

// newReadCmd builds `dropway read <url-or-slug>`: fetch a served site over plain
// HTTP and stream its body to stdout. A full http(s) URL is fetched directly; a
// bare slug is resolved to the site's live URL via the API first. readFactory is
// injected so tests can resolve slugs without a live control plane.
func newReadCmd(readFactory func(baseURL, token string) api.ReadClient) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "read <url-or-slug>",
		Short: "Fetch a site's served content over HTTP and print it to stdout",
		Long: "Fetch a Dropway site over plain HTTP and write the response body to stdout.\n" +
			"Pass a full http(s) URL to fetch it directly, or a site slug to resolve its\n" +
			"live URL via the API first (sign in with `dropway login`).\n" +
			"Public sites need no auth; a gated site instead returns its sign-in page.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			target := args[0]

			url := target
			if !isHTTPURL(target) {
				resolved, err := resolveSlugURL(ctx, readFactory, baseURL, target)
				if err != nil {
					return fmt.Errorf("read: %w", err)
				}
				url = resolved
			}
			return fetchTo(ctx, cmd.OutOrStdout(), url)
		},
	}

	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// isHTTPURL reports whether s is already a fetchable http(s) URL (vs a bare slug).
func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// resolveSlugURL looks up a site by slug in the caller's org and returns its live
// URL. Requires auth, since the site list is org-scoped.
func resolveSlugURL(ctx context.Context, readFactory func(baseURL, token string) api.ReadClient, baseURL, slug string) (string, error) {
	token, err := auth.Token(ctx, baseURL)
	if err != nil {
		return "", err
	}
	resp, err := readFactory(baseURL, token).ListSites(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range resp.Sites {
		if s.Slug == slug {
			if s.LiveURL == "" {
				return "", fmt.Errorf("site %q has no live URL yet (nothing published)", slug)
			}
			return s.LiveURL, nil
		}
	}
	return "", fmt.Errorf("no site with slug %q in your org (try `dropway sites --all`)", slug)
}

// fetchTo GETs url and streams up to maxReadBytes of the body to w. A non-2xx
// status is an error (a gated site's sign-in redirect resolves to its 200 page,
// which streams through as-is).
func fetchTo(ctx context.Context, w io.Writer, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("read: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("read: %s returned %d", url, resp.StatusCode)
	}
	if _, err := io.Copy(w, io.LimitReader(resp.Body, maxReadBytes)); err != nil {
		return fmt.Errorf("read: stream body: %w", err)
	}
	return nil
}
