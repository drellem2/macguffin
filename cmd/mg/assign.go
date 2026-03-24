package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var assignCmd = &cobra.Command{
	Use:   "assign ID ASSIGNEE",
	Short: "Assign a work item (shortcut for edit --assignee)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		id := args[0]
		assignee := args[1]

		fields := workitem.UpdateField{
			Assignee: &assignee,
		}

		item, err := workitem.Update(root, id, fields)
		if err != nil {
			return err
		}

		fmt.Printf("Assigned %s to %s: %s\n", item.ID, assignee, item.Title)
		return nil
	},
}
