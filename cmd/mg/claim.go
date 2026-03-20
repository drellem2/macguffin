package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var claimCmd = &cobra.Command{
	Use:   "claim ID",
	Short: "Atomically claim a work item by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Claim(root, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Claimed %s: %s\n", item.ID, item.Title)
		return nil
	},
}
