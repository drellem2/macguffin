package main

import (
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Create a git snapshot of current state",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}
		return workspace.Snapshot(root)
	},
}
