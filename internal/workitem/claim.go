package workitem

import (
	"fmt"
	"os"
	"path/filepath"
)

// Claim atomically moves a work item from available/ to claimed/ using rename(2).
// The claimed filename is suffixed with the caller's PID for exactly-once semantics.
// Returns the claimed Item, or an error if the item doesn't exist or was already claimed.
func Claim(root, id string) (*Item, error) {
	src := filepath.Join(root, "work", "available", id+".md")
	pid := os.Getpid()
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", id, pid))

	// rename(2) is atomic on local filesystems.
	// If two processes race, exactly one succeeds; the other gets ENOENT.
	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("claiming %s: %w", id, err)
	}

	return readFile(dst)
}
