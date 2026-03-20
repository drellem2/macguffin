package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ReapResult describes a single reaped work item.
type ReapResult struct {
	ID  string
	PID int
}

// Reap scans claimed/ for work items whose claimant PID is dead and moves them
// back to available/. A PID is considered dead if kill(pid, 0) returns an error
// (ESRCH). Returns the list of reaped items.
func Reap(root string) ([]ReapResult, error) {
	claimedDir := filepath.Join(root, "work", "claimed")
	availableDir := filepath.Join(root, "work", "available")

	entries, err := os.ReadDir(claimedDir)
	if err != nil {
		return nil, fmt.Errorf("reading claimed/: %w", err)
	}

	var reaped []ReapResult
	for _, e := range entries {
		name := e.Name()

		// Claimed files have format: <id>.md.<pid>
		// Find the last dot-separated segment as the PID
		lastDot := strings.LastIndex(name, ".")
		if lastDot < 0 {
			continue
		}
		pidStr := name[lastDot+1:]
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			continue // not a PID-suffixed file
		}

		// Check if the process is alive via kill(pid, 0)
		if isProcessAlive(pid) {
			continue
		}

		// Extract the base filename without PID suffix: <id>.md
		baseName := name[:lastDot]

		src := filepath.Join(claimedDir, name)
		dst := filepath.Join(availableDir, baseName)

		// Atomic rename back to available/
		if err := os.Rename(src, dst); err != nil {
			continue // file may have been reaped by another process
		}

		// Extract ID from baseName (strip .md suffix)
		id := strings.TrimSuffix(baseName, ".md")

		reaped = append(reaped, ReapResult{ID: id, PID: pid})
	}

	return reaped, nil
}

// isProcessAlive checks whether a process with the given PID exists.
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
