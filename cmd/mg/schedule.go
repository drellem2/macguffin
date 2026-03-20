package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Promote pending items whose dependencies are met",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		promoted, err := workitem.Schedule(root)
		if err != nil {
			return err
		}

		if len(promoted) == 0 {
			fmt.Println("No items promoted.")
			return nil
		}

		for _, item := range promoted {
			fmt.Printf("Promoted %s: %s\n", item.ID, item.Title)
		}
		return nil
	},
}
