package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var unarchiveCmd = &cobra.Command{
	Use:   "unarchive ID",
	Short: "Restore an archived work item to available",
	Long: `Unarchive restores a previously archived work item back to
available/. The item must currently be in the archive. Any result.json
sidecar from the prior done state is moved alongside the work item.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Unarchive(root, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Unarchived %s: %s\n", item.ID, item.Title)
		return nil
	},
}
