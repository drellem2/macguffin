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
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, nil, err
	}
	if m.Status != "claimed" {
		return nil, nil, explainDoneFailure(root, id)
	}
	srcPath := m.Path
	srcName := filepath.Base(srcPath)

	// Extract the claim-holder PID from the filename (<id>.md.<pid>) for the event.
	claimPID := ""
	if dot := strings.LastIndex(srcName, "."); dot > 0 {
		if _, err := strconv.Atoi(srcName[dot+1:]); err == nil {
			claimPID = srcName[dot+1:]
		}
	}

	doneDir := filepath.Join(root, "work", "done")
	dstPath := filepath.Join(doneDir, id+".md")

	// Fold any pre-existing result into the fresh one BEFORE the rename, so an
	// irreconcilable pair fails with the item still claimed and both copies on
	// disk, rather than half-transitioned. The prior result is the one the move
	// below will leave at the destination: normally the copy travelling out of
	// the origin directory, or — when the origin has none — a copy already
	// sitting in done/ from an earlier completion.
	var sidecarBytes []byte
	if len(resultJSON) > 0 {
		priorPath := filepath.Join(filepath.Dir(srcPath), resultSidecarName(id))
		if _, err := os.Stat(priorPath); os.IsNotExist(err) {
			priorPath = filepath.Join(doneDir, resultSidecarName(id))
		}
		sidecarBytes, err = mergeResultSidecar(priorPath, id, resultJSON)
		if err != nil {
			return nil, nil, err
		}
	}

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(srcPath, dstPath); err != nil {
		return nil, nil, ioErr(fmt.Sprintf("%s: could not be completed: %s", id, fsErrText(err)))
	}

	// Carry any pre-existing sidecar forward with the .md. An item arriving here
	// via done -> reopen -> done already has a result in claimed/ (put there by
	// Reopen); without this move it is orphaned — a .result.json with no .md
	// beside it, invisible to `mg show` and asserting a stale completion.
	//
	// Done is the only transition that may also WRITE a sidecar, so the move
	// must come first: the merged result below then lands on top of the carried
	// one at the destination. Reversing the order would clobber the new result
	// with the stale one.
	if err := moveResultSidecar(filepath.Dir(srcPath), doneDir, id); err != nil {
		return nil, nil, fmt.Errorf("moving result sidecar: %w", err)
	}

	// Write the result sidecar if one was provided. sidecarBytes already carries
	// forward any keys of the prior result the fresh one did not set — Done must
	// not destroy a result it did not write. See mergeResultSidecar.
	if len(sidecarBytes) > 0 {
		sidecarPath := filepath.Join(doneDir, resultSidecarName(id))
		if err := os.WriteFile(sidecarPath, sidecarBytes, 0o644); err != nil {
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

// Status returns the lifecycle state of a work item: "available", "claimed",
// "done", "pending", "shelved", or "archived". It errors if the ID is
// ambiguous. See Resolve.
func Status(root, id string) (string, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return "", err
	}
	return m.Status, nil
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
