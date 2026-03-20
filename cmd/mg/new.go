package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var newType string

var newCmd = &cobra.Command{
	Use:   "new [--type=TYPE] TITLE...",
	Short: "Create a new work item",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := strings.Join(args, " ")
		if title == "" {
			return fmt.Errorf("title is required")
		}

		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Create(root, newType, title)
		if err != nil {
			return err
		}

		fmt.Printf("Created %s: %s\n", item.ID, item.Title)
		return nil
	},
}

func init() {
	newCmd.Flags().StringVar(&newType, "type", "task", "work item type")
}
