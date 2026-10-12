package main

import (
	"fmt"

	"github.com/greatliontech/gofresh/toolversion"
	"github.com/spf13/cobra"
)

func versionString() string { return "pew " + toolversion.Identity() }

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: guidanceShort("version"), Long: guidanceHelp("version"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionString())
			return err
		},
	}
}
