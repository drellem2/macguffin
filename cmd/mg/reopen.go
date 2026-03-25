package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var reopenCmd = &cobra.Command{
	Use:   "reopen ID",
	Short: "Move a done work item back to available",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
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
