package workitem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Done atomically moves a claimed work item to done/ and writes an optional
// result sidecar JSON file. The item must currently be in claimed/.
// resultJSON may be nil if no result metadata is provided.
// After completing the item, any pending items whose dependencies are now
// fully satisfied are auto-promoted to available.
func Done(root, id string, resultJSON json.RawMessage) (*Item, []*Item, error) {
	claimedDir := filepath.Join(root, "work", "claimed")

	// Find the claimed file (has PID suffix: <id>.md.<pid>)
	entries, err := os.ReadDir(claimedDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading claimed/: %w", err)
	}

	var srcPath string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+".md") {
			srcPath = filepath.Join(claimedDir, e.Name())
			break
		}
	}

	if srcPath == "" {
		return nil, nil, fmt.Errorf("work item %s not found in claimed/", id)
	}

	dstPath := filepath.Join(root, "work", "done", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(srcPath, dstPath); err != nil {
		return nil, nil, fmt.Errorf("completing %s: %w", id, err)
	}

	// Write result sidecar if provided
	if len(resultJSON) > 0 {
		sidecarPath := filepath.Join(root, "work", "done", id+".result.json")
		if err := os.WriteFile(sidecarPath, resultJSON, 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing result sidecar: %w", err)
		}
	}

	item, err := readFile(dstPath)
	if err != nil {
		return nil, nil, err
	}

	// Auto-promote pending items whose dependencies are now satisfied.
	promoted, err := Schedule(root)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-promoting pending items: %w", err)
	}

	return item, promoted, nil
}

// Status returns the lifecycle state of a work item: "available", "claimed", "done", or "archived".
func Status(root, id string) (string, error) {
	states := []string{"available", "claimed", "done", "pending"}

	for _, state := range states {
		dir := filepath.Join(root, "work", state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), id+".md") {
				return state, nil
			}
		}
	}

	// Check archive partitions
	archiveRoot := filepath.Join(root, "work", "archive")
	partitions, err := os.ReadDir(archiveRoot)
	if err == nil {
		for _, p := range partitions {
			if !p.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(archiveRoot, p.Name()))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), id+".md") {
					return "archived", nil
				}
			}
		}
	}

	return "", fmt.Errorf("work item %s not found", id)
}

// ListByStatus returns all work items in the given status directory.
// Valid statuses: "available", "claimed", "done".
func ListByStatus(root, status string) ([]*Item, error) {
	switch status {
	case "available", "claimed", "done", "pending":
		// valid
	default:
		return nil, fmt.Errorf("invalid status %q (must be available, claimed, done, or pending)", status)
	}

	dir := filepath.Join(root, "work", status)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s/: %w", status, err)
	}

	var items []*Item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") && !strings.Contains(e.Name(), ".md.") {
			continue
		}
		// Skip .result.json sidecars
		if strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		item, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		items = append(items, item)
	}

	return items, nil
}

// ListAll returns all work items across active statuses, grouped by status.
// Done and archived items are excluded by default — use ListByStatus or
// ListArchived to retrieve them.
func ListAll(root string) (map[string][]*Item, error) {
	result := make(map[string][]*Item)
	for _, status := range []string{"available", "claimed", "done", "pending"} {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		if len(items) > 0 {
			result[status] = items
		}
	}
	return result, nil
}
