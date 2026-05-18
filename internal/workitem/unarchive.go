package workitem

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/event"
)

// Unarchive restores an archived work item back to available/. It locates
// the item across archive/<YYYY-MM>/ partitions and moves both the .md
// file and any result.json sidecar.
func Unarchive(root, id string) (*Item, error) {
	src, status, err := FindPath(root, id)
	if err != nil {
		return nil, fmt.Errorf("work item %s not found", id)
	}
	if status != "archived" {
		return nil, fmt.Errorf("cannot unarchive %s: item is %s, not archived", id, status)
	}

	dst := filepath.Join(root, "work", "available", id+".md")
	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("unarchiving %s: %w", id, err)
	}

	// Move result sidecar if it exists. src points at archive/<partition>/<id>.md
	archiveDir := filepath.Dir(src)
	sidecarSrc := filepath.Join(archiveDir, id+".result.json")
	if _, err := os.Stat(sidecarSrc); err == nil {
		sidecarDst := filepath.Join(root, "work", "available", id+".result.json")
		if err := os.Rename(sidecarSrc, sidecarDst); err != nil {
			return nil, fmt.Errorf("unarchiving sidecar for %s: %w", id, err)
		}
	}

	item, err := readFile(dst)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.unarchive", map[string]string{
		"item_id":     id,
		"from_status": "archived",
		"to_status":   "available",
		"actor":       actorFor(item),
	})

	return item, nil
}
