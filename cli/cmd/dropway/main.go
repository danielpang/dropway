// Command dropway is the Dropway CLI: `dropway deploy <dir>` turns a folder of
// static files into a prepared (and, with --send, live) deployment.
package main

import (
	"fmt"
	"os"

	"github.com/danielpang/dropway/cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dropway: "+err.Error())
		os.Exit(1)
	}
}
