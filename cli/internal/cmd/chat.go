package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
	"github.com/danielpang/dropway/internal/chatspec"
)

// newChatCmd builds the `dropway chat` command group: share an agent-session
// export as an org chat log, browse/append to the library, and manage a log's
// site attachment + served panel. chatFactory is injected so tests supply a
// fake api.ChatClient without a live server.
func newChatCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Share agent chat sessions with your org (share, list, show, append)",
		Long: "Shared chat logs: publish a coding-session transcript (Claude Code JSONL,\n" +
			"a ChatGPT export, or plain text) to your org's library, optionally attach it\n" +
			"to a site so viewers see the conversation behind the deploy, and append\n" +
			"follow-up turns or action annotations as work continues.\n" +
			"Sign in first with `dropway login` (or set " + tokenEnv + " for CI).",
	}
	cmd.AddCommand(newChatShareCmd(chatFactory))
	cmd.AddCommand(newChatListCmd(chatFactory))
	cmd.AddCommand(newChatShowCmd(chatFactory))
	cmd.AddCommand(newChatAppendCmd(chatFactory))
	cmd.AddCommand(newChatAttachCmd(chatFactory))
	cmd.AddCommand(newChatDetachCmd(chatFactory))
	cmd.AddCommand(newChatPanelCmd(chatFactory))
	cmd.AddCommand(newChatDeleteCmd(chatFactory))
	cmd.AddCommand(newChatDeleteMessageCmd(chatFactory))
	return cmd
}

// ---------------------------------------------------------------------------
// dropway chat share
// ---------------------------------------------------------------------------

// newChatShareCmd builds `dropway chat share <export-file>`: read the export,
// create a chat log seeded with it (optionally attached to a site by slug),
// and disclose any trimming the server applied.
func newChatShareCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var (
		site          string
		title         string
		source        string
		format        string
		deriveActions bool
		baseURL       string
	)

	cmd := &cobra.Command{
		Use:   "share <export-file>",
		Short: "Share a session export as an org chat log",
		Long: "Read a conversation export (Claude Code JSONL, a ChatGPT JSON export, or\n" +
			"plain text) and publish it as a shared chat log. --site attaches it to one\n" +
			"of your sites (by slug) so the conversation can be served next to the deploy.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if err := validateChatSource(source); err != nil {
				return fmt.Errorf("chat share: %w", err)
			}
			if err := validateChatFormat(format); err != nil {
				return fmt.Errorf("chat share: %w", err)
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("chat share: %w", err)
			}
			if len(data) == 0 {
				return fmt.Errorf("chat share: %q is empty — nothing to share", args[0])
			}

			ctx := context.Background()
			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat share: %w", err)
			}
			client := chatFactory(baseURL, token)

			var siteID *string
			if site != "" {
				id, err := resolveSiteID(ctx, client, site)
				if err != nil {
					return fmt.Errorf("chat share: %w", err)
				}
				siteID = &id
			}

			resp, err := client.CreateChatLog(ctx, api.CreateChatLogRequest{
				Title:      title,
				SourceTool: source,
				SiteID:     siteID,
				ChatImport: api.ChatImport{
					Transcript:    string(data),
					Format:        format,
					DeriveActions: deriveActions,
				},
			})
			if err != nil {
				return fmt.Errorf("chat share: %w", err)
			}

			fmt.Fprintf(out, "✓ Shared chat %s (%d message(s))\n", resp.ChatLog.ID, resp.Appended-resp.Pruned)
			if site != "" {
				fmt.Fprintf(out, "  attached to site %s\n", site)
			}
			printChatTrim(out, resp.Appended, resp.Pruned, resp.Dropped)
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "site slug to attach the chat to")
	cmd.Flags().StringVar(&title, "title", "", "chat log title")
	cmd.Flags().StringVar(&source, "source", "", "tool the session came from: claude_code|chatgpt|cursor|other")
	cmd.Flags().StringVar(&format, "format", "auto", "export format: auto|claude_code|chatgpt|text")
	cmd.Flags().BoolVar(&deriveActions, "derive-actions", false, "condense tool activity in the export into action annotations")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// printChatTrim discloses server-side trimming: pruned rows fell off the
// tier's retention window, dropped ones were cut by the import bound. Silent
// truncation would misrepresent what got shared, so the CLI always says so.
func printChatTrim(out io.Writer, appended, pruned, dropped int) {
	if pruned <= 0 && dropped <= 0 {
		return
	}
	kept := appended - pruned
	total := appended + dropped
	fmt.Fprintf(out, "  kept the last %d of %d — upgrade to keep full history\n", kept, total)
}

// ---------------------------------------------------------------------------
// dropway chat list
// ---------------------------------------------------------------------------

// newChatListCmd builds `dropway chat list`: an aligned table of the org's
// shared chat logs, with attached sites shown by slug.
func newChatListCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your org's shared chat logs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat list: %w", err)
			}
			client := chatFactory(baseURL, token)

			resp, err := client.ListChatLogs(ctx)
			if err != nil {
				return fmt.Errorf("chat list: %w", err)
			}
			if len(resp.ChatLogs) == 0 {
				fmt.Fprintln(out, "No shared chats yet. Share one with `dropway chat share <export-file>`.")
				return nil
			}

			// One sites call maps attached site ids back to slugs for the table.
			slugByID := map[string]string{}
			if sites, err := client.ListSites(ctx); err == nil {
				for _, s := range sites.Sites {
					slugByID[s.ID] = s.Slug
				}
			}
			printChatLogs(out, resp.ChatLogs, slugByID)
			return nil
		},
	}

	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// printChatLogs renders an aligned chat-log table (same tabwriter style as
// printSites/printSkills).
func printChatLogs(out io.Writer, logs []api.ChatLog, slugByID map[string]string) {
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tSOURCE\tMESSAGES\tSITE\tPANEL")
	for _, l := range logs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
			l.ID, orDash(l.Title), orDash(l.SourceTool), l.MessageCount,
			chatSiteLabel(l.SiteID, slugByID), panelLabel(l.PanelEnabled))
	}
	_ = tw.Flush()
}

// chatSiteLabel shows the attached site's slug, its raw id when the slug is
// unknown (e.g. a site the caller can't list), or "-" when unattached.
func chatSiteLabel(siteID *string, slugByID map[string]string) string {
	if siteID == nil || *siteID == "" {
		return "-"
	}
	if slug, ok := slugByID[*siteID]; ok {
		return slug
	}
	return *siteID
}

func panelLabel(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---------------------------------------------------------------------------
// dropway chat show
// ---------------------------------------------------------------------------

// newChatShowCmd builds `dropway chat show <chat-id>`: print a log's messages,
// rendering action annotations with their edit/tool icon.
func newChatShowCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var (
		afterSeq int
		baseURL  string
	)

	cmd := &cobra.Command{
		Use:   "show <chat-id>",
		Short: "Print a shared chat's messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat show: %w", err)
			}
			client := chatFactory(baseURL, token)

			resp, err := client.ListChatMessages(ctx, args[0], afterSeq, 0)
			if err != nil {
				return fmt.Errorf("chat show: %w", err)
			}
			if len(resp.Messages) == 0 {
				fmt.Fprintln(out, "No messages.")
				return nil
			}
			for _, m := range resp.Messages {
				fmt.Fprintln(out, formatChatMessage(m))
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&afterSeq, "after-seq", 0, "only messages after this sequence number")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// formatChatMessage renders one message: chat turns as "[seq] role: content",
// action annotations as "[seq] ✎ paths… — comment" (file edits) or
// "[seq] ⚙ tool — comment" (tool runs).
func formatChatMessage(m api.ChatMessage) string {
	if m.Kind != chatspec.KindAction {
		return fmt.Sprintf("[%d] %s: %s", m.Seq, m.Role, m.Content)
	}
	var meta api.ChatActionMeta
	_ = json.Unmarshal(m.Meta, &meta)
	var head string
	switch meta.Action {
	case chatspec.ActionFileEdit:
		head = fmt.Sprintf("[%d] ✎ %s", m.Seq, strings.Join(meta.Paths, ", "))
	default: // tool_use (and anything newer degrades to the tool shape)
		head = fmt.Sprintf("[%d] ⚙ %s", m.Seq, meta.Tool)
	}
	if m.Content != "" {
		head += " — " + m.Content
	}
	return head
}

// ---------------------------------------------------------------------------
// dropway chat append
// ---------------------------------------------------------------------------

// newChatAppendCmd builds `dropway chat append`: add one turn or action
// annotation — or import another export file — to a log, targeted by chat id
// or by the attached site's slug (which creates the log if the site has none).
func newChatAppendCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var (
		site    string
		file    string
		message string
		role    string
		action  string
		paths   []string
		tool    string
		comment string
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "append [<chat-id>]",
		Short: "Append a message, action annotation, or export file to a shared chat",
		Long: "Append to a chat log by id, or with --site <slug> to the log attached to\n" +
			"that site (creating one if the site has none). Provide exactly one of\n" +
			"--message (a chat turn), --action (a file_edit/tool_use annotation), or\n" +
			"--file (import another export).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			chatID := ""
			if len(args) == 1 {
				chatID = args[0]
			}
			if (chatID == "") == (site == "") {
				return fmt.Errorf("chat append: pass a chat id OR --site <slug> (exactly one)")
			}
			payload, err := buildAppendPayload(file, message, role, action, paths, tool, comment)
			if err != nil {
				return fmt.Errorf("chat append: %w", err)
			}

			ctx := context.Background()
			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat append: %w", err)
			}
			client := chatFactory(baseURL, token)

			var resp *api.ChatAppendResponse
			if chatID != "" {
				resp, err = client.AppendChatMessages(ctx, chatID, *payload)
			} else {
				var siteID string
				siteID, err = resolveSiteID(ctx, client, site)
				if err != nil {
					return fmt.Errorf("chat append: %w", err)
				}
				resp, err = client.AppendSiteChat(ctx, siteID, *payload)
			}
			if err != nil {
				return fmt.Errorf("chat append: %w", err)
			}

			target := chatID
			if target == "" {
				target = "site " + site + "'s chat"
			}
			fmt.Fprintf(out, "✓ Appended %d message(s) to %s\n", len(resp.Messages), target)
			printChatTrim(out, len(resp.Messages), resp.Pruned, resp.Dropped)
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "target the chat attached to this site slug (instead of a chat id)")
	cmd.Flags().StringVar(&file, "file", "", "export file to import")
	cmd.Flags().StringVar(&message, "message", "", "chat message text to append")
	cmd.Flags().StringVar(&role, "role", chatspec.RoleUser, "role for --message: user|assistant")
	cmd.Flags().StringVar(&action, "action", "", "append an action annotation: file_edit|tool_use")
	cmd.Flags().StringSliceVar(&paths, "path", nil, "file path(s) the action touched (with --action; comma-separated or repeated)")
	cmd.Flags().StringVar(&tool, "tool", "", "tool name (with --action tool_use)")
	cmd.Flags().StringVar(&comment, "comment", "", "commentary for the action annotation (with --action)")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// buildAppendPayload turns the append flags into one import payload, requiring
// exactly one of --file / --message / --action. Pure — unit-tested directly.
func buildAppendPayload(file, message, role, action string, paths []string, tool, comment string) (*api.ChatImport, error) {
	given := 0
	for _, set := range []bool{file != "", message != "", action != ""} {
		if set {
			given++
		}
	}
	if given != 1 {
		return nil, fmt.Errorf("provide exactly one of --file, --message, or --action")
	}

	switch {
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("%q is empty — nothing to append", file)
		}
		return &api.ChatImport{Transcript: string(data)}, nil

	case message != "":
		if role != chatspec.RoleUser && role != chatspec.RoleAssistant {
			return nil, fmt.Errorf("--role must be user or assistant, got %q", role)
		}
		return &api.ChatImport{Messages: []api.ChatMessageInput{
			{Kind: chatspec.KindChat, Role: role, Content: message},
		}}, nil

	default: // action
		meta := &api.ChatActionMeta{Action: action, Tool: tool, Paths: paths}
		switch action {
		case chatspec.ActionFileEdit:
			if len(paths) == 0 {
				return nil, fmt.Errorf("--action file_edit requires --path")
			}
		case chatspec.ActionToolUse:
			if tool == "" {
				return nil, fmt.Errorf("--action tool_use requires --tool")
			}
		default:
			return nil, fmt.Errorf("--action must be file_edit or tool_use, got %q", action)
		}
		return &api.ChatImport{Messages: []api.ChatMessageInput{
			{Kind: chatspec.KindAction, Role: chatspec.RoleAssistant, Content: comment, Meta: meta},
		}}, nil
	}
}

// ---------------------------------------------------------------------------
// dropway chat attach / detach / panel / delete / delete-message
// ---------------------------------------------------------------------------

// newChatAttachCmd builds `dropway chat attach <chat-id> --site <slug>`.
func newChatAttachCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var (
		site    string
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "attach <chat-id>",
		Short: "Attach a shared chat to one of your sites",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat attach: %w", err)
			}
			client := chatFactory(baseURL, token)

			siteID, err := resolveSiteID(ctx, client, site)
			if err != nil {
				return fmt.Errorf("chat attach: %w", err)
			}
			if _, err := client.SetChatLogSite(ctx, args[0], &siteID); err != nil {
				return fmt.Errorf("chat attach: %w", err)
			}
			fmt.Fprintf(out, "✓ Attached chat %s to site %s\n", args[0], site)
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "site slug to attach the chat to")
	_ = cmd.MarkFlagRequired("site")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// newChatDetachCmd builds `dropway chat detach <chat-id>`.
func newChatDetachCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "detach <chat-id>",
		Short: "Detach a shared chat from its site",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat detach: %w", err)
			}
			client := chatFactory(baseURL, token)

			if _, err := client.SetChatLogSite(ctx, args[0], nil); err != nil {
				return fmt.Errorf("chat detach: %w", err)
			}
			fmt.Fprintf(out, "✓ Detached chat %s from its site\n", args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// newChatPanelCmd builds `dropway chat panel <chat-id> --enabled=true|false`.
func newChatPanelCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var (
		enabled bool
		baseURL string
	)

	cmd := &cobra.Command{
		Use:   "panel <chat-id> --enabled=true|false",
		Short: "Enable or disable the served chat panel on the attached site",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat panel: %w", err)
			}
			client := chatFactory(baseURL, token)

			log, err := client.SetChatLogPanel(ctx, args[0], enabled)
			if err != nil {
				return fmt.Errorf("chat panel: %w", err)
			}
			fmt.Fprintf(out, "✓ Chat panel %s for chat %s\n", panelLabel(log.PanelEnabled), log.ID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&enabled, "enabled", false, "whether the site serves the chat panel")
	_ = cmd.MarkFlagRequired("enabled")
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// newChatDeleteCmd builds `dropway chat delete <chat-id>`.
func newChatDeleteCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "delete <chat-id>",
		Short: "Delete a shared chat and all its messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat delete: %w", err)
			}
			client := chatFactory(baseURL, token)

			if err := client.DeleteChatLog(ctx, args[0]); err != nil {
				return fmt.Errorf("chat delete: %w", err)
			}
			fmt.Fprintf(out, "✓ Deleted chat %s\n", args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// newChatDeleteMessageCmd builds `dropway chat delete-message <chat-id> <seq>`.
func newChatDeleteMessageCmd(chatFactory func(baseURL, token string) api.ChatClient) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "delete-message <chat-id> <seq>",
		Short: "Delete one message from a shared chat (mistakes, pasted secrets)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			seq, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("chat delete-message: invalid seq %q", args[1])
			}

			token, err := auth.Token(ctx, baseURL)
			if err != nil {
				return fmt.Errorf("chat delete-message: %w", err)
			}
			client := chatFactory(baseURL, token)

			if err := client.DeleteChatMessage(ctx, args[0], int32(seq)); err != nil {
				return fmt.Errorf("chat delete-message: %w", err)
			}
			fmt.Fprintf(out, "✓ Deleted message %d from chat %s\n", seq, args[0])
			return nil
		},
	}

	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// ---------------------------------------------------------------------------
// helpers (pure — unit-tested directly)
// ---------------------------------------------------------------------------

// resolveSiteID maps a site slug to its id via the sites list (the chat API
// takes ids; slugs are the human handle every other command uses).
func resolveSiteID(ctx context.Context, client api.ChatClient, siteSlug string) (string, error) {
	resp, err := client.ListSites(ctx)
	if err != nil {
		return "", fmt.Errorf("list sites: %w", err)
	}
	for _, s := range resp.Sites {
		if s.Slug == siteSlug {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("no site with slug %q in your org (try `dropway sites --all`)", siteSlug)
}

// validateChatSource pre-checks --source (the API stores it as display
// metadata, but a typo'd enum should fail fast client-side).
func validateChatSource(source string) error {
	switch source {
	case "", chatspec.SourceClaudeCode, chatspec.SourceChatGPT, chatspec.SourceCursor, chatspec.SourceOther:
		return nil
	}
	return fmt.Errorf("--source must be claude_code, chatgpt, cursor, or other, got %q", source)
}

// validateChatFormat pre-checks --format against the parser's vocabulary.
func validateChatFormat(format string) error {
	switch format {
	case "", "auto", "claude_code", "chatgpt", "text":
		return nil
	}
	return fmt.Errorf("--format must be auto, claude_code, chatgpt, or text, got %q", format)
}

// defaultChatClientFactory builds the real chat HTTP client.
func defaultChatClientFactory(baseURL, token string) api.ChatClient {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
