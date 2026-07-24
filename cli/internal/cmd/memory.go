// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/danielpang/dropway/cli/internal/api"
	"github.com/danielpang/dropway/cli/internal/auth"
)

// newMemoryCmd builds the `dropway memory` group: the CLI surface of org
// memory ("your agent knows your company"), so coding agents that shell out —
// or users curating from the terminal — reach the same memory the AI builder
// and MCP tools use.
func newMemoryCmd(memoryFactory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Search, add, and curate what Dropway remembers about your org",
		Long: "Org memory: durable facts (brand voice, colors, preferences) Dropway has\n" +
			"learned from your builds, chats, sites, and skills. `search` and `context`\n" +
			"retrieve it for a task; `add` records a new fact; `pin`/`unpin`/`rm` curate\n" +
			"(admin). Sign in first with `dropway login` (or set " + tokenEnv + " for CI).",
	}
	cmd.AddCommand(newMemorySearchCmd(memoryFactory))
	cmd.AddCommand(newMemoryListCmd(memoryFactory))
	cmd.AddCommand(newMemoryAddCmd(memoryFactory))
	cmd.AddCommand(newMemoryContextCmd(memoryFactory))
	cmd.AddCommand(newMemoryPinCmd(memoryFactory, true))
	cmd.AddCommand(newMemoryPinCmd(memoryFactory, false))
	cmd.AddCommand(newMemoryRmCmd(memoryFactory))
	return cmd
}

func newMemorySearchCmd(factory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	var (
		baseURL string
		k       int
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find the org memories most relevant to a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("memory search: %w", err)
			}
			rows, err := factory(baseURL, token).SearchMemories(cmd.Context(), args[0], k)
			if err != nil {
				return fmt.Errorf("memory search: %w", err)
			}
			printMemories(out, rows, true)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	cmd.Flags().IntVarP(&k, "top", "k", 8, "how many memories to retrieve")
	return cmd
}

func newMemoryListCmd(factory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	var (
		baseURL string
		kind    string
		pinned  bool
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the org's memories (pinned first, most recent next)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("memory list: %w", err)
			}
			rows, err := factory(baseURL, token).ListMemories(cmd.Context(), kind, pinned, limit)
			if err != nil {
				return fmt.Errorf("memory list: %w", err)
			}
			printMemories(out, rows, false)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: fact|preference|style|correction|manual")
	cmd.Flags().BoolVar(&pinned, "pinned", false, "only pinned memories")
	cmd.Flags().IntVar(&limit, "limit", 50, "max entries")
	return cmd
}

func newMemoryAddCmd(factory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	var (
		baseURL string
		kind    string
	)
	cmd := &cobra.Command{
		Use:   "add <content>",
		Short: "Record a durable fact about your org",
		Long: "Record one self-contained fact future builds should know, e.g.\n" +
			"  dropway memory add \"Every page ends with a 'Book a demo' CTA\" --kind preference",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("memory add: %w", err)
			}
			mem, created, err := factory(baseURL, token).AddMemory(cmd.Context(), args[0], kind)
			if err != nil {
				return fmt.Errorf("memory add: %w", err)
			}
			if created {
				fmt.Fprintf(out, "Remembered (%s): %s\n", mem.Kind, mem.Content)
			} else {
				fmt.Fprintf(out, "Already known — refreshed (%s): %s\n", mem.Kind, mem.Content)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	cmd.Flags().StringVar(&kind, "kind", "", "fact|preference|style|correction (default manual)")
	return cmd
}

// newMemoryContextCmd prints retrieved memory as a ready-to-paste
// <company_memory> block — the agent-facing affordance (`$(dropway memory
// context "task")` drops org context straight into a prompt).
func newMemoryContextCmd(factory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	var (
		baseURL string
		k       int
	)
	cmd := &cobra.Command{
		Use:   "context [query]",
		Short: "Print org memory as a context block for an agent prompt",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("memory context: %w", err)
			}
			client := factory(baseURL, token)
			var rows []api.Memory
			if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
				rows, err = client.SearchMemories(cmd.Context(), args[0], k)
			} else {
				rows, err = client.ListMemories(cmd.Context(), "", false, k+12)
			}
			if err != nil {
				return fmt.Errorf("memory context: %w", err)
			}
			if len(rows) == 0 {
				return nil // empty block would just waste prompt tokens
			}
			fmt.Fprintln(out, "<company_memory>")
			fmt.Fprintln(out, "Facts and preferences this organization's sites follow. Apply them unless instructed otherwise.")
			for _, m := range rows {
				if m.Disabled {
					continue
				}
				fmt.Fprintf(out, "- [%s] %s\n", m.Kind, m.Content)
			}
			fmt.Fprintln(out, "</company_memory>")
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	cmd.Flags().IntVarP(&k, "top", "k", 8, "how many searched memories to include")
	return cmd
}

func newMemoryPinCmd(factory func(baseURL, token string) api.MemoryClient, pin bool) *cobra.Command {
	var baseURL string
	use, short := "pin <id>", "Pin a memory so it always applies (admin)"
	if !pin {
		use, short = "unpin <id>", "Unpin a memory (admin)"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "memory pin"
			if !pin {
				name = "memory unpin"
			}
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			p := pin
			mem, err := factory(baseURL, token).PatchMemory(cmd.Context(), args[0], api.MemoryPatch{Pinned: &p})
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			state := "Pinned"
			if !pin {
				state = "Unpinned"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", state, mem.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

func newMemoryRmCmd(factory func(baseURL, token string) api.MemoryClient) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete a memory (admin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token, err := auth.Token(cmd.Context(), baseURL)
			if err != nil {
				return fmt.Errorf("memory rm: %w", err)
			}
			if err := factory(baseURL, token).DeleteMemory(cmd.Context(), args[0]); err != nil {
				return fmt.Errorf("memory rm: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Deleted.")
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "api", defaultAPIBase(), "Dropway API base URL")
	return cmd
}

// printMemories renders the aligned table every list-ish subcommand shares.
func printMemories(out io.Writer, rows []api.Memory, withDistance bool) {
	if len(rows) == 0 {
		fmt.Fprintln(out, "No memories yet. The AI builder learns as your org uses it, or add one with `dropway memory add`.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	if withDistance {
		fmt.Fprintln(tw, "ID\tKIND\tFLAGS\tDIST\tCONTENT")
	} else {
		fmt.Fprintln(tw, "ID\tKIND\tFLAGS\tCONTENT")
	}
	for _, m := range rows {
		var flags []string
		if m.Pinned {
			flags = append(flags, "pinned")
		}
		if m.Disabled {
			flags = append(flags, "disabled")
		}
		if withDistance {
			dist := "-"
			if m.Distance != nil {
				dist = fmt.Sprintf("%.2f", *m.Distance)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Kind, strings.Join(flags, ","), dist, m.Content)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.ID, m.Kind, strings.Join(flags, ","), m.Content)
		}
	}
	tw.Flush()
}

// defaultMemoryClientFactory builds the real memory HTTP client.
func defaultMemoryClientFactory(baseURL, token string) api.MemoryClient {
	return &api.HTTPClient{BaseURL: baseURL, Token: token}
}
