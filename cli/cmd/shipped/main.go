// Command shipped is the Shipped CLI: `shipped deploy <dir>` turns a folder of
// static files into a prepared (and, with --send, live) deployment.
package main

import (
	"fmt"
	"os"

	"github.com/danielpang/shipped/cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "shipped: "+err.Error())
		os.Exit(1)
	}
}
