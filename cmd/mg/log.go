package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var logCmd = &cobra.Command{
	Use:                "log [args]",
	Short:              "Show git snapshot history (passes args to git log)",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}
		return workspace.Log(root, args)
	},
}
