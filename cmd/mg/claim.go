package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/drellem2/macguffin/internal/workitem"
	"github.com/drellem2/macguffin/internal/workspace"
	"github.com/spf13/cobra"
)

var claimPID int

var claimCmd = &cobra.Command{
	Use:   "claim ID",
	Short: "Atomically claim a work item by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := workspace.DefaultRoot()
		if err != nil {
			return err
		}

		pid := claimPID
		// Fall back to POGO_PID env var if --pid not explicitly set
		if pid == 0 {
			if envPID := os.Getenv("POGO_PID"); envPID != "" {
				pid, err = strconv.Atoi(envPID)
				if err != nil {
					return fmt.Errorf("invalid POGO_PID %q: %w", envPID, err)
				}
			}
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
