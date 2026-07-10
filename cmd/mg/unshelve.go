package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var unshelveCmd = &cobra.Command{
	Use:   "unshelve ID",
	Short: "Restore a shelved work item and its dependents",
	Long: `Unshelve restores a previously shelved work item and any of its
dependents that are also shelved. Items with unmet dependencies are
placed in pending/; others go to available/.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		items, err := workitem.Unshelve(root, args[0])
		if err != nil {
			return err
		}

		for _, item := range items {
			fmt.Printf("Unshelved %s: %s\n", item.ID, item.Title)
		}
		return nil
	},
}
