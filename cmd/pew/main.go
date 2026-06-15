// Command pew manages Go benchmark provenance, staleness, and comparison.
// See docs/spec.md for the design contract.
package main

import (
	"errors"
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
		stub("gc", "Remove stored results for benchmarks no longer in the code"),
	)
	return root
}

// stub is a not-yet-implemented subcommand. Each is filled in by its plan chunk
// (docs/plan.md); until then it fails honestly rather than pretending to work.
func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(*cobra.Command, []string) error {
			return errors.New(use + ": not implemented yet")
		},
	}
}
