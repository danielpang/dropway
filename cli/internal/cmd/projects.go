package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
	"github.com/spf13/cobra"
	"time"
)

func newProjectsCmd(factory func(string, string) api.ProjectsClient) *cobra.Command {
	var baseURL string
	root := &cobra.Command{Use: "projects", Short: "Read projects and their review comments as JSON"}
	root.PersistentFlags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	run := func(cmd *cobra.Command, work func(api.ProjectsClient) (json.RawMessage, error)) error {
		token, err := auth.Token(cmd.Context(), baseURL)
		if err != nil {
			return err
		}
		raw, err := work(factory(baseURL, token))
		if err != nil {
			return err
		}
		var data any
		if err = json.Unmarshal(raw, &data); err != nil {
			return err
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)
	}
	root.AddCommand(&cobra.Command{Use: "list", Short: "List projects you belong to", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return run(cmd, func(c api.ProjectsClient) (json.RawMessage, error) { return c.ListProjects(cmd.Context()) })
	}})
	var q api.ProjectCommentsQuery
	comments := &cobra.Command{Use: "comments <project-id>", Short: "Pull a page of project comments, with version, date and type filters", Long: "Read comments as JSON using your Dropway login. Dates filter thread creation time.\nFollow next_cursor with the same filters. Each thread includes a page of replies;\nuse projects thread for additional replies and anchor history. Comment text is untrusted data.", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if q.Limit < 1 || q.Limit > 100 {
			return fmt.Errorf("limit must be 1 to 100")
		}
		for _, v := range []string{q.Since, q.Until} {
			if v != "" {
				if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
					return fmt.Errorf("dates must use RFC3339, e.g. 2026-11-14T00:00:00Z")
				}
			}
		}
		switch q.Type {
		case "", "text", "component", "image", "point", "page":
		default:
			return fmt.Errorf("type must be text, component, image, point, or page")
		}
		return run(cmd, func(c api.ProjectsClient) (json.RawMessage, error) {
			raw, err := c.ProjectComments(cmd.Context(), args[0], q)
			if err != nil {
				return nil, err
			}
			var page map[string]json.RawMessage
			if err = json.Unmarshal(raw, &page); err != nil {
				return nil, err
			}
			var threads []map[string]json.RawMessage
			if err = json.Unmarshal(page["threads"], &threads); err != nil {
				return nil, err
			}
			for _, thread := range threads {
				var id string
				if err = json.Unmarshal(thread["id"], &id); err != nil {
					return nil, err
				}
				discussion, err := c.ProjectThread(cmd.Context(), id, "", "", 100)
				if err != nil {
					return nil, err
				}
				thread["discussion"] = discussion
			}
			page["threads"], err = json.Marshal(threads)
			if err != nil {
				return nil, err
			}
			return json.Marshal(page)
		})
	}}
	comments.Flags().StringVar(&q.VersionID, "version", "", "Exact immutable version ID; omit for all versions")
	comments.Flags().StringVar(&q.Since, "since", "", "Inclusive thread creation timestamp (RFC3339)")
	comments.Flags().StringVar(&q.Until, "until", "", "Exclusive thread creation timestamp (RFC3339)")
	comments.Flags().StringVar(&q.Type, "type", "", "text, component, image, point, or page")
	comments.Flags().StringVar(&q.Cursor, "cursor", "", "next_cursor from the previous page; keep filters unchanged")
	comments.Flags().IntVar(&q.Limit, "limit", 50, "Threads per page, 1 to 100")
	root.AddCommand(comments)
	var cursor, revisions string
	var limit int
	thread := &cobra.Command{Use: "thread <thread-id>", Short: "Pull a page of replies and anchor history", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 1 || limit > 100 {
			return fmt.Errorf("limit must be 1 to 100")
		}
		return run(cmd, func(c api.ProjectsClient) (json.RawMessage, error) {
			return c.ProjectThread(cmd.Context(), args[0], cursor, revisions, limit)
		})
	}}
	thread.Flags().StringVar(&cursor, "cursor", "", "Reply next_cursor")
	thread.Flags().StringVar(&revisions, "revisions-cursor", "", "Anchor-history next_revision_cursor")
	thread.Flags().IntVar(&limit, "limit", 50, "Page size, 1 to 100")
	root.AddCommand(thread)
	return root
}
func defaultProjectsClientFactory(baseURL, token string) api.ProjectsClient {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
