package main

import (
	"fmt"

	guidancepkg "github.com/greatliontech/gofresh/guidance"
	pew "github.com/greatliontech/pew"
	"github.com/spf13/cobra"
)

// guidanceDoc is the embedded guidance document; a malformed document
// is a build defect the parse-pinning test surfaces, so command
// construction fails loudly rather than serving nothing.
func guidanceDoc() *guidancepkg.Document {
	doc, err := pew.GuidanceDocument()
	if err != nil {
		panic("pew: embedded guidance document malformed: " + err.Error())
	}
	return doc
}

// guidanceShort and guidanceHelp are a command's served prose under
// its cli spelling, read from the guidance document at construction —
// never a second literal (REQ-pew-guidance). Help is the knobless
// rendering: cobra renders its own Flags block.
func guidanceShort(verb string) string {
	d, err := guidanceDoc().Description("cli", verb)
	if err != nil {
		panic("pew: " + err.Error())
	}
	return d
}

func guidanceHelp(verb string) string {
	l, err := guidanceDoc().Help("cli", verb)
	if err != nil {
		panic("pew: " + err.Error())
	}
	return l
}

// newGuidanceCmd serves the guidance document itself: a verb's full
// section, or the decision map for orientation.
func newGuidanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guidance [verb]",
		Short: guidanceShort("guidance"),
		Long:  guidanceHelp("guidance"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), guidanceDoc().Orientation())
				return nil
			}
			long, err := guidanceDoc().Long("cli", args[0])
			if err != nil {
				return fmt.Errorf("%w; run guidance with no verb for the decision map, which names every verb", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), long)
			return nil
		},
	}
}
