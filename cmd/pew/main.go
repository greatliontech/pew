// Command pew manages Go benchmark provenance, staleness, and comparison.
// See docs/specs/spec.md for the design contract.
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
		os.Exit(exitCode(err))
	}
}

// exitCode maps a command error to the process exit status (spec §10.1):
// errors — including a detected regression — exit 1, while the
// --fail-on-regression empty-comparison failure exits 2, so a CI consumer can
// tell "measured and regressed" from "measured nothing".
func exitCode(err error) int {
	var empty *nothingComparedError
	if errors.As(err, &empty) {
		return 2
	}
	return 1
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
