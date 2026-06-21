package workitem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
)

// Done atomically moves a claimed work item to done/ and writes an optional
// result sidecar JSON file. The item must currently be in claimed/.
// resultJSON may be nil if no result metadata is provided.
// After completing the item, any pending items whose dependencies are now
// fully satisfied are auto-promoted to available.
func Done(root, id string, resultJSON json.RawMessage) (*Item, []*Item, error) {
	claimedDir := filepath.Join(root, "work", "claimed")

	// Find the claimed file (has PID suffix: <id>.md.<pid>). A read failure
	// (e.g. claimed/ missing) is treated the same as "not present" — the
	// diagnosis below reports where the item actually is.
	entries, _ := os.ReadDir(claimedDir)

	var srcPath, srcName string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+".md") {
			srcName = e.Name()
			srcPath = filepath.Join(claimedDir, srcName)
			break
		}
	}

	if srcPath == "" {
		return nil, nil, explainDoneFailure(root, id)
	}

	// Extract the claim-holder PID from the filename (<id>.md.<pid>) for the event.
	claimPID := ""
	if dot := strings.LastIndex(srcName, "."); dot > 0 {
		if _, err := strconv.Atoi(srcName[dot+1:]); err == nil {
			claimPID = srcName[dot+1:]
		}
	}

	dstPath := filepath.Join(root, "work", "done", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(srcPath, dstPath); err != nil {
		return nil, nil, fmt.Errorf("%s: could not be completed: %s", id, fsErrText(err))
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

	event.Emit(root, "work.done", map[string]string{
		"item_id":     id,
		"from_status": "claimed",
		"to_status":   "done",
		"actor":       actorFor(item),
		"pid":         claimPID,
	})

	// Auto-promote pending items whose dependencies are now satisfied.
	promoted, err := Schedule(root)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-promoting pending items: %w", err)
	}

	return item, promoted, nil
}

// Status returns the lifecycle state of a work item: "available", "claimed", "done", or "archived".
func Status(root, id string) (string, error) {
	states := []string{"available", "claimed", "done", "pending", "shelved"}

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

	return "", errNoSuchItem(id)
}

// ListByStatus returns all work items in the given status directory.
// Valid statuses: "available", "claimed", "done".
func ListByStatus(root, status string) ([]*Item, error) {
	switch status {
	case "available", "claimed", "done", "pending", "shelved":
		// valid
	default:
		return nil, fmt.Errorf("invalid status %q (must be available, claimed, done, pending, or shelved)", status)
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
// Archived and shelved items are excluded — use ListByStatus to retrieve them.
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
