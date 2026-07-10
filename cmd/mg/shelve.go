package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var shelveTag string

var shelveCmd = &cobra.Command{
	Use:   "shelve [ID]",
	Short: "Shelve a work item and its dependents",
	Long: `Shelve hides a work item and all items that depend on it from normal
listing. Shelved items can be listed with 'mg list --status=shelved' and
restored with 'mg unshelve'.

Use --tag to shelve all items with a given tag.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		if shelveTag != "" {
			items, err := workitem.ShelveByTag(root, shelveTag)
			if err != nil {
				return err
			}
			for _, item := range items {
				fmt.Printf("Shelved %s: %s\n", item.ID, item.Title)
			}
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("requires a work item ID or --tag flag")
		}

		items, err := workitem.Shelve(root, args[0])
		if err != nil {
			return err
		}

		for _, item := range items {
			fmt.Printf("Shelved %s: %s\n", item.ID, item.Title)
		}
		return nil
	},
}

func init() {
	shelveCmd.Flags().StringVar(&shelveTag, "tag", "", "shelve all items with this tag")
}
