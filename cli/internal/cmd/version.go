// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Build metadata. These are overridden at release time with
//
//	-ldflags "-X github.com/danielpang/dropway/cli/internal/cmd.version=v1.2.3 ..."
//
// (see .github/workflows/release.yml). On a plain `go build` / `go install`
// they keep these dev defaults, so `dropway version` always works.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionString renders the one-line summary used by both `--version` and the
// first line of `dropway version`.
func versionString() string {
	return fmt.Sprintf("dropway %s (%s) built %s %s/%s",
		version, commit, date, runtime.GOOS, runtime.GOARCH)
}

// newVersionCmd builds `dropway version`, printing build metadata. The install
// script (install.sh) runs this to confirm a successful install.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dropway version and build info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return err
		},
	}
}
