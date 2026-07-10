package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var unclaimCmd = &cobra.Command{
	Use:   "unclaim ID",
	Short: "Release a claim, returning the work item to available/",
	Long: `Release a claim on a work item, returning it to available/.

Unclaim is an explicit, targeted operation: it ignores the recorded claimant
PID. The recorded PID is unreliable because it may be the short-lived
'mg claim' subprocess rather than the owning agent — relying on it can
release claims held by live, healthy workers. To release a claim, the caller
must know the work item ID.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		res, err := workitem.Unclaim(root, args[0])
		if err != nil {
			return err
		}

		if res.PID > 0 {
			fmt.Printf("Unclaimed %s (was claimed by PID %d)\n", res.ID, res.PID)
		} else {
			fmt.Printf("Unclaimed %s\n", res.ID)
		}
		return nil
	},
}
