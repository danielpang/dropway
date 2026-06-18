package cmd

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the `dropway` root command with all subcommands wired in.
// Exported so main.go and tests can construct and execute it.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "dropway",
		Short:         "Dropway — a folder of files → a live, access-controlled URL",
		Version:       versionString(), // enables `dropway --version`
		SilenceUsage:  true,            // don't dump usage on a runtime error
		SilenceErrors: true,            // we print errors ourselves in main
	}
	// Print our own one-line summary verbatim; cobra's default template would
	// prepend a redundant "dropway version " before it.
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newVersionCmd())
	root.AddCommand(newDeployCmd(defaultClientFactory))
	// Phase-4 operator jobs (direct DB/R2 access, not the deploy-token API path).
	root.AddCommand(newGCCmd(defaultOpsFactory))
	root.AddCommand(newDRCmd(defaultOpsFactory))
	return root
}

// Execute runs the root command; main.go calls it.
func Execute() error {
	return NewRootCmd().Execute()
}
