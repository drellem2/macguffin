package main

import (
	"fmt"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var archiveDays int

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Archive done items older than N days",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		maxAge := time.Duration(archiveDays) * 24 * time.Hour
		archived, err := workitem.Archive(root, maxAge)
		if err != nil {
			return err
		}

		if len(archived) == 0 {
			fmt.Println("No items to archive.")
			return nil
		}

		for _, item := range archived {
			fmt.Printf("Archived %s: %s\n", item.ID, item.Title)
		}
		fmt.Printf("Archived %d item(s).\n", len(archived))
		return nil
	},
}

func init() {
	archiveCmd.Flags().IntVar(&archiveDays, "days", 7, "archive done items older than this many days")
}
