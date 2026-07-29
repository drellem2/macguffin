package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/spf13/cobra"
)

var claimPID int

// resolveOwnerPID resolves the PID to stamp on a claim from an explicit --pid,
// falling back to $POGO_PID (the owning agent's PID, exported by pogo
// automation) and finally to 0, which the workitem layer reads as "the calling
// process". Shared by `mg claim` and `mg reclaim` so the two cannot drift: a
// reclaim that resolved the PID differently from the claim it re-stamps would
// re-stamp the wrong owner.
func resolveOwnerPID(flagPID int) (int, error) {
	if flagPID != 0 {
		return flagPID, nil
	}
	envPID := os.Getenv("POGO_PID")
	if envPID == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(envPID)
	if err != nil {
		return 0, fmt.Errorf("invalid POGO_PID %q: %w", envPID, err)
	}
	return pid, nil
}

var claimCmd = &cobra.Command{
	Use:   "claim ID",
	Short: "Atomically claim a work item by ID",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := resolveRoot()
		if err != nil {
			return err
		}

		pid, err := resolveOwnerPID(claimPID)
		if err != nil {
			return err
		}

		item, err := workitem.Claim(root, args[0], pid)
		if err != nil {
			return err
		}

		fmt.Printf("Claimed %s: %s\n", item.ID, item.Title)
		return nil
	},
}

func init() {
	claimCmd.Flags().IntVar(&claimPID, "pid", 0, "PID of the owning process (default: current process PID)")
}
