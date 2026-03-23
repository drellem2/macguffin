package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show ID",
	Short: "Show a work item by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		item, err := workitem.Read(root, args[0])
		if err != nil {
			return err
		}

		status, err := workitem.Status(root, args[0])
		if err != nil {
			return err
		}

		fmt.Printf("%-10s %s\n", "ID:", item.ID)
		fmt.Printf("%-10s %s\n", "Type:", item.Type)
		fmt.Printf("%-10s %s\n", "Status:", status)
		fmt.Printf("%-10s %s\n", "Created:", item.Created.Format("2006-01-02 15:04:05Z"))
		fmt.Printf("%-10s %s\n", "Creator:", item.Creator)
		fmt.Printf("%-10s %s\n", "Title:", item.Title)

		if item.Body != "" {
			fmt.Printf("\n%s", item.Body)
		}

		return nil
	},
}
