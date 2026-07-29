package main

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var reclaimPID int

var reclaimCmd = &cobra.Command{
	Use:   "reclaim ID",
	Short: "Re-stamp the owner PID on a claim you already hold",
	Long: `Re-stamp the owner PID recorded on an existing claim, without the item
leaving claimed/.

This is the handover half of a claim made on someone else's behalf. pogod
claims a work item at spawn time, with its own PID, before the worker process
exists — so an item being worked is never invisible to an ownership check. The
worker's first act is then 'mg reclaim <id>', which moves the recorded PID to
its own. That the PID changed is a positive signal that the worker itself
acted, which nothing else in the store provides.

The item must already be in claimed/; reclaim never claims an available item
(exit 4, 'not_claimed' — use 'mg claim' for that). Keeping the two verbs apart
is deliberate: 'mg claim' refusing a non-available item is what makes two
concurrent dispatches onto one item impossible, and a flag on 'claim' that
skipped the precondition would put that guard one typo away from being off.

The item never leaves claimed/. The implementation is a single rename(2) within
claimed/ — '<id>.md.<old>' to '<id>.md.<new>' — not an unclaim followed by a
claim, which would park the item in available/ for the duration and reopen the
window pogod's spawn-time claim exists to close.

Re-stamping to the PID already recorded exits 0 and changes nothing, so a
worker that repeats this step after a context compaction gets a no-op rather
than an error that reads as a failure.

--pid defaults to $POGO_PID, then to the calling process's PID, matching
'mg claim'.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		pid, err := resolveOwnerPID(reclaimPID)
		if err != nil {
			return err
		}

		res, err := workitem.Reclaim(root, args[0], pid)
		if err != nil {
			return err
		}

		// Print the transition, so an operator reading a transcript can tell
		// which side of the handover they are looking at.
		switch {
		case !res.Moved:
			fmt.Printf("Reclaimed %s: pid %d (unchanged)\n", res.ID, res.NewPID)
		case res.OldPID > 0:
			fmt.Printf("Reclaimed %s: pid %d -> %d\n", res.ID, res.OldPID, res.NewPID)
		default:
			fmt.Printf("Reclaimed %s: pid unrecorded -> %d\n", res.ID, res.NewPID)
		}
		return nil
	},
}

func init() {
	reclaimCmd.Flags().IntVar(&reclaimPID, "pid", 0, "PID to record as the owner (default: $POGO_PID, else current process PID)")
}
