package main

import (
	"fmt"
	"strings"

	guidancepkg "github.com/greatliontech/gofresh/guidance"
	pew "github.com/greatliontech/pew"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
// its cli spelling, the document's registration for the verb read at
// construction — never a second literal (REQ-pew-guidance). Help is the
// knobless rendering: cobra renders its own Flags block.
func guidanceShort(verb string) string { return pew.GuidanceRegistration("cli", verb).Description }

func guidanceHelp(verb string) string { return pew.GuidanceRegistration("cli", verb).Help }

// visitLeafVerbs is the one traversal of the served CLI surface: every
// visible leaf verb by its cli spelling (words joined for a nested
// verb), cobra's help and completion plumbing skipped — the rule the
// coverage judgment and the usage rendering share, so the covered set
// and the rendered set can never differ. pew has no hidden verb and no
// verb with subcommands today; the arms are the rule's, not pew's.
func visitLeafVerbs(root *cobra.Command, visit func(name string, c *cobra.Command)) {
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, child := range c.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			visit(name, child)
		}
	}
	walk("", root)
}

// renderKnobUsage sets every visible leaf verb's local flag usage to
// the document's usage projection for the cli surface (gofresh's
// Knob.Usage: the knob's first clause in pflag's grammar), so no flag
// carries a second spelling of its prose (REQ-pew-guidance). Local
// flags only: pew's root registers no persistent flag, and the
// document names a verb's own knobs. It runs at construction, before
// cobra adds its help flag, so every flag visited is a knob; a flag
// the document does not name is a build defect and panics here.
func renderKnobUsage(root *cobra.Command) {
	visitLeafVerbs(root, func(name string, c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			f.Usage = pew.GuidanceKnob("cli", name, f.Name).Usage()
		})
	})
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
