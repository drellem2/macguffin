package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var reapCmd = &cobra.Command{
	Use:   "reap",
	Short: "Reclaim work items from dead claimant processes",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		reaped, err := workitem.Reap(root)
		if err != nil {
			return err
		}

		if len(reaped) == 0 {
			fmt.Println("No stale claims found.")
			return nil
		}

		for _, r := range reaped {
			fmt.Printf("Reaped %s (was claimed by PID %d)\n", r.ID, r.PID)
		}
		fmt.Printf("%d item(s) returned to available/\n", len(reaped))
		return nil
	},
}
