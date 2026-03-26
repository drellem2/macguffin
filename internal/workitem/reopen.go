package workitem

import (
	"fmt"
	"os"
	"path/filepath"
)

// Reopen atomically moves a work item from done/ back to claimed/.
// The item must currently be in done/. Returns the reopened Item.
func Reopen(root, id string) (*Item, error) {
	src := filepath.Join(root, "work", "done", id+".md")
	dst := filepath.Join(root, "work", "claimed", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("work item %s not found in done/", id)
		}
		return nil, fmt.Errorf("reopening %s: %w", id, err)
	}

	return readFile(dst)
}
