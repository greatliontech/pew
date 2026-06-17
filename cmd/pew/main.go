// Command pew manages Go benchmark provenance, staleness, and comparison.
// See docs/spec.md for the design contract.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "pew:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "pew",
		Short:         "Manage Go benchmark provenance, staleness, and comparison",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newRunCmd(),
		newStatusCmd(),
		newStatCmd(),
		newGCCmd(),
	)
	return root
}
