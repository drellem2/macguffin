package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UnclaimResult describes a released claim.
type UnclaimResult struct {
	ID  string
	PID int // PID recorded on the claim (0 if absent or unparseable)
}

// Unclaim atomically releases a claim, moving the work item back to available/.
// The PID recorded on the claim is reported but not consulted — the recorded
// PID is unreliable because it may be the short-lived `mg claim` subprocess
// rather than the owning agent. Releasing a claim is therefore an explicit,
// targeted operation: the caller must know the work item ID.
func Unclaim(root, id string) (*UnclaimResult, error) {
	claimedDir := filepath.Join(root, "work", "claimed")
	availableDir := filepath.Join(root, "work", "available")

	entries, err := os.ReadDir(claimedDir)
	if err != nil {
		return nil, fmt.Errorf("reading claimed/: %w", err)
	}

	var srcName string
	for _, e := range entries {
		name := e.Name()
		if name == id+".md" || strings.HasPrefix(name, id+".md.") {
			srcName = name
			break
		}
	}

	if srcName == "" {
		return nil, fmt.Errorf("work item %s not found in claimed/", id)
	}

	pid := parseClaimPID(srcName)

	src := filepath.Join(claimedDir, srcName)
	dst := filepath.Join(availableDir, id+".md")

	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("releasing claim on %s: %w", id, err)
	}

	return &UnclaimResult{ID: id, PID: pid}, nil
}

// parseClaimPID extracts the PID suffix from a claimed filename of the form
// "<id>.md.<pid>". Returns 0 if there is no PID suffix or it doesn't parse.
func parseClaimPID(name string) int {
	lastDot := strings.LastIndex(name, ".")
	if lastDot < 0 {
		return 0
	}
	pidStr := name[lastDot+1:]
	pid := 0
	for _, c := range pidStr {
		if c < '0' || c > '9' {
			return 0
		}
		pid = pid*10 + int(c-'0')
	}
	return pid
}
