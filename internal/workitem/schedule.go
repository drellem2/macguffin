package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Schedule checks all items in pending/ and promotes any whose dependencies
// have all landed in done/ to available/. Returns the list of promoted items.
func Schedule(root string) ([]*Item, error) {
	pendingDir := filepath.Join(root, "work", "pending")
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		return nil, fmt.Errorf("reading pending/: %w", err)
	}

	// Build set of done item IDs
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}

	var promoted []*Item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(pendingDir, e.Name())
		item, err := readFile(path)
		if err != nil {
			continue // skip malformed files
		}

		if allDepsMet(item.Depends, doneIDs) {
			dst := filepath.Join(root, "work", "available", e.Name())
			if err := os.Rename(path, dst); err != nil {
				return nil, fmt.Errorf("promoting %s: %w", item.ID, err)
			}
			promoted = append(promoted, item)
		}
	}

	return promoted, nil
}

// doneIDSet returns a set of all item IDs in done/.
func doneIDSet(root string) (map[string]bool, error) {
	doneDir := filepath.Join(root, "work", "done")
	entries, err := os.ReadDir(doneDir)
	if err != nil {
		return nil, fmt.Errorf("reading done/: %w", err)
	}

	ids := make(map[string]bool)
	for _, e := range entries {
		name := e.Name()
		// Extract ID: gt-XXXX from gt-XXXX.md or gt-XXXX.result.json
		if idx := strings.Index(name, ".md"); idx > 0 {
			ids[name[:idx]] = true
		}
	}
	return ids, nil
}

// allDepsMet returns true if every dependency ID is in the done set.
func allDepsMet(deps []string, doneIDs map[string]bool) bool {
	for _, dep := range deps {
		if !doneIDs[dep] {
			return false
		}
	}
	return true
}
