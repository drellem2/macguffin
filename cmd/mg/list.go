package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List work items",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		if listStatus != "" {
			items, err := workitem.ListByStatus(root, listStatus)
			if err != nil {
				return err
			}

			if len(items) == 0 {
				fmt.Printf("No %s work items.\n", listStatus)
				return nil
			}

			for _, item := range items {
				fmt.Printf("%-10s %-8s %s\n", item.ID, item.Type, item.Title)
			}
			return nil
		}

		grouped, err := workitem.ListAll(root)
		if err != nil {
			return err
		}

		if len(grouped) == 0 {
			fmt.Println("No work items.")
			return nil
		}

		order := []string{"available", "claimed", "done", "pending"}
		for _, s := range order {
			items := grouped[s]
			if len(items) == 0 {
				continue
			}
			fmt.Printf("%s:\n", s)
			for _, item := range items {
				fmt.Printf("  %-10s %-8s %s\n", item.ID, item.Type, item.Title)
			}
		}

		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "filter by status (available, claimed, done)")
}
