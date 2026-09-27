package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var (
	unshelveClaim bool
	unshelvePID   int
)

var unshelveCmd = &cobra.Command{
	Use:   "unshelve ID",
	Short: "Restore a shelved work item and its dependents",
	Long: `Unshelve restores a previously shelved work item and any of its
dependents that are also shelved. Items with unmet dependencies are
placed in pending/; others go to available/.

--claim takes back an item that was CLAIMED when it was shelved: instead of
available/, it moves the item straight into claimed/ as the caller's claim
(--pid, else $POGO_PID, else this process), in one rename, so no dispatcher
can claim it in between. It is a new claim, not the old owner's — shelve does
not record the PID it drops. It refuses unless the item's latest shelve record
says it was claimed, and an item with unmet dependencies still goes to
pending/, unclaimed — and that still exits 0, because the unshelve itself
succeeded: a script that needs the claim must check the output or 'mg show',
not the exit status. Dependents come back as they would without --claim.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		if cmd.Flags().Changed("pid") && !unshelveClaim {
			return mgerr.Usage("usage", "--pid only applies with --claim.", cmd.UseLine())
		}

		var items []*workitem.Item
		if unshelveClaim {
			pid, err := resolveOwnerPID(unshelvePID)
			if err != nil {
				return err
			}
			items, err = workitem.UnshelveClaim(root, args[0], pid)
			if err != nil {
				return err
			}
		} else {
			items, err = workitem.Unshelve(root, args[0])
			if err != nil {
				return err
			}
		}

		for i, item := range items {
			if i == 0 && unshelveClaim {
				// Say where the named item landed: --claim may have put it in
				// pending/ rather than claimed/, and a caller who reads only
				// "Unshelved" would go on to work an item it does not hold.
				switch st, err := workitem.Status(root, item.ID); {
				case err != nil:
					fmt.Printf("Unshelved %s: %s (could not confirm the claim: %v)\n", item.ID, item.Title, err)
				case st == "claimed":
					fmt.Printf("Unshelved and claimed %s: %s\n", item.ID, item.Title)
				case st == "pending":
					fmt.Printf("Unshelved %s: %s (not claimed: it is pending — its gates are not open)\n", item.ID, item.Title)
				default:
					fmt.Printf("Unshelved %s: %s (not claimed: it is %s)\n", item.ID, item.Title, st)
				}
				continue
			}
			fmt.Printf("Unshelved %s: %s\n", item.ID, item.Title)
		}
		return nil
	},
}

func init() {
	unshelveCmd.Flags().BoolVar(&unshelveClaim, "claim", false, "take back an item that was claimed when shelved, as the caller's claim")
	unshelveCmd.Flags().IntVar(&unshelvePID, "pid", 0, "with --claim: PID of the owning process (default: $POGO_PID, else current process)")
}
