package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
)

// Shelve atomically moves a work item to shelved/. The item can be in any
// active status (available, claimed, pending). Items that depend on the
// shelved item are also shelved recursively. Returns all shelved items.
func Shelve(root, id string) ([]*Item, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}

	switch status {
	case "available", "claimed", "pending":
		// valid — can be shelved
	case "shelved":
		return nil, fmt.Errorf("work item %s is already shelved", id)
	default:
		return nil, fmt.Errorf("cannot shelve %s: item is %s", id, status)
	}

	dst := filepath.Join(root, "work", "shelved", id+".md")
	if err := os.Rename(path, dst); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not be shelved: %s", id, fsErrText(err)))
	}

	// The result sidecar must follow the .md into shelved/.
	if err := moveResultSidecar(filepath.Dir(path), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: shelved, but result sidecar could not follow: %s", id, fsErrText(err)))
	}

	item, err := readFile(dst)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.shelve", map[string]string{
		"item_id":     id,
		"from_status": status,
		"to_status":   "shelved",
		"actor":       actorFor(item),
	})

	shelved := []*Item{item}

	// Shelve dependents: items whose depends list includes this ID.
	dependents, err := findDependents(root, id)
	if err != nil {
		return shelved, nil // best-effort: return what we shelved
	}

	for _, dep := range dependents {
		more, err := Shelve(root, dep.ID)
		if err != nil {
			continue // skip items that can't be shelved (e.g., already done)
		}
		shelved = append(shelved, more...)
	}

	return shelved, nil
}

// ShelveByTag shelves all items with the given tag (and their dependents).
// Returns all shelved items.
func ShelveByTag(root, tag string) ([]*Item, error) {
	// Collect IDs of items to shelve from active statuses
	var toShelve []string
	for _, status := range []string{"available", "claimed", "pending"} {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, item := range items {
			for _, t := range item.Tags {
				if t == tag {
					toShelve = append(toShelve, item.ID)
					break
				}
			}
		}
	}

	if len(toShelve) == 0 {
		return nil, fmt.Errorf("no items found with tag %q", tag)
	}

	var shelved []*Item
	shelvedSet := make(map[string]bool)
	for _, id := range toShelve {
		if shelvedSet[id] {
			continue
		}
		items, err := Shelve(root, id)
		if err != nil {
			continue
		}
		for _, item := range items {
			if !shelvedSet[item.ID] {
				shelvedSet[item.ID] = true
				shelved = append(shelved, item)
			}
		}
	}

	return shelved, nil
}

// Unshelve restores a shelved work item. Items with unmet dependencies go
// to pending/; otherwise they go to available/. Returns all unshelved items.
func Unshelve(root, id string) ([]*Item, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "shelved" {
		return nil, explainUnshelveFailure(root, id)
	}

	src := m.Path
	item, err := readFile(src)
	if err != nil {
		return nil, err
	}

	// Determine destination based on dependencies
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}

	subdir := "available"
	if len(item.Depends) > 0 && !allDepsMet(item.Depends, doneIDs) {
		subdir = "pending"
	}

	dst := filepath.Join(root, "work", subdir, id+".md")
	if err := os.Rename(src, dst); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not be unshelved: %s", id, fsErrText(err)))
	}

	// The result sidecar must follow the .md out of shelved/.
	if err := moveResultSidecar(filepath.Dir(src), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: unshelved, but result sidecar could not follow: %s", id, fsErrText(err)))
	}

	event.Emit(root, "work.unshelve", map[string]string{
		"item_id":     id,
		"from_status": "shelved",
		"to_status":   subdir,
		"actor":       actorFor(item),
	})

	unshelved := []*Item{item}

	// Unshelve dependents that were shelved along with this item
	dependents, err := findShelvedDependents(root, id)
	if err != nil {
		return unshelved, nil
	}

	for _, dep := range dependents {
		more, err := Unshelve(root, dep.ID)
		if err != nil {
			continue
		}
		unshelved = append(unshelved, more...)
	}

	return unshelved, nil
}

// findDependents returns items in active statuses that depend on the given ID.
func findDependents(root, id string) ([]*Item, error) {
	var dependents []*Item
	for _, status := range []string{"available", "claimed", "pending"} {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, item := range items {
			for _, dep := range item.Depends {
				if dep == id {
					dependents = append(dependents, item)
					break
				}
			}
		}
	}
	return dependents, nil
}

// findShelvedDependents returns shelved items that depend on the given ID.
func findShelvedDependents(root, id string) ([]*Item, error) {
	items, err := ListByStatus(root, "shelved")
	if err != nil {
		return nil, err
	}

	var dependents []*Item
	for _, item := range items {
		for _, dep := range item.Depends {
			if dep == id {
				dependents = append(dependents, item)
				break
			}
		}
	}
	return dependents, nil
}

// ListShelved returns all shelved work items, looking for items that have
// their ID prefix in the filename.
func ListShelved(root string) ([]*Item, error) {
	dir := filepath.Join(root, "work", "shelved")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading shelved/: %w", err)
	}

	var items []*Item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		item, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
