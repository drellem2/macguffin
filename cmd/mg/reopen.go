package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var reopenCmd = &cobra.Command{
	Use:   "reopen ID",
	Short: "Move a done work item back to claimed",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Reopen(root, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Reopened %s: %s\n", item.ID, item.Title)
		return nil
	},
}
