package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var unarchiveStatus string

var unarchiveCmd = &cobra.Command{
	Use:   "unarchive ID",
	Short: "Restore an archived work item to the status it held when archived",
	Long: `Unarchive restores a previously archived work item to the status it
held at the moment it was archived, reading that status from the event log.
The item must currently be in the archive. Any result.json sidecar from the
prior done state is moved alongside the work item.

If the event log has no record of the prior status — it was rotated away, or
the file was moved into archive/ by hand — unarchive REFUSES rather than pick
a status for you. Restoring a done item as available would hand finished work
back to the dispatch loop as if it were fresh. Pass --status to say where the
item belongs:

    mg unarchive mg-1234 --status=done

--status is also how you redirect an item somewhere other than where it was.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		item, status, err := workitem.Unarchive(root, args[0], unarchiveStatus)
		if err != nil {
			return err
		}

		fmt.Printf("Unarchived %s to %s: %s\n", item.ID, status, item.Title)
		return nil
	},
}

func init() {
	unarchiveCmd.Flags().StringVar(&unarchiveStatus, "status", "",
		"status to restore the item to (default: the status it held when archived)")
}
