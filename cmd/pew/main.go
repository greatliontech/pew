// Command pew manages Go benchmark provenance, staleness, and comparison.
// See docs/specs/spec.md for the design contract.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	// Every verb reports the stretch in flight on the one cadence (spec
	// REQ-pew-progress); the reporter stops before the error prints,
	// on every path — cobra runs no post-run hook for a failing verb.
	ctx, stop := startReporter(context.Background(), os.Stderr, progressCadence)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pew:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a command error to the process exit status (spec §10.1):
// errors — including a detected regression — exit 1, while the
// --fail-on-regression empty-comparison failure exits 2, so a CI consumer can
// tell "measured and regressed" from "measured nothing".
func exitCode(err error) int {
	var stopped *interruptedError
	if errors.As(err, &stopped) {
		return 130
	}
	if onlyUnfulfilled(err) {
		return 2
	}
	return 1
}

// onlyUnfulfilled classifies the complete error tree. A joined operational
// failure (including finalization) cannot be hidden by an unfulfilled sibling.
// Wrapping keeps a cause's classification; interruption is handled first above.
func onlyUnfulfilled(err error) bool {
	switch e := err.(type) {
	case *nothingComparedError, *profileUnfulfilledError:
		return true
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyUnfulfilled(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return onlyUnfulfilled(e.Unwrap())
	default:
		return false
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
		newABCmd(),
		newStatusCmd(),
		newStatCmd(),
		newGCCmd(),
		newGuidanceCmd(),
	)
	renderKnobUsage(root)
	return root
}
