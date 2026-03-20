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
func Done(root, id string, resultJSON json.RawMessage) (*Item, error) {
	claimedDir := filepath.Join(root, "work", "claimed")

	// Find the claimed file (has PID suffix: <id>.md.<pid>)
	entries, err := os.ReadDir(claimedDir)
	if err != nil {
		return nil, fmt.Errorf("reading claimed/: %w", err)
	}

	var srcPath string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+".md") {
			srcPath = filepath.Join(claimedDir, e.Name())
			break
		}
	}

	if srcPath == "" {
		return nil, fmt.Errorf("work item %s not found in claimed/", id)
	}

	dstPath := filepath.Join(root, "work", "done", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(srcPath, dstPath); err != nil {
		return nil, fmt.Errorf("completing %s: %w", id, err)
	}

	// Write result sidecar if provided
	if len(resultJSON) > 0 {
		sidecarPath := filepath.Join(root, "work", "done", id+".result.json")
		if err := os.WriteFile(sidecarPath, resultJSON, 0o644); err != nil {
			return nil, fmt.Errorf("writing result sidecar: %w", err)
		}
	}

	return readFile(dstPath)
}

// Status returns the lifecycle state of a work item: "available", "claimed", or "done".
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

// ListAll returns all work items across all statuses, grouped by status.
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
