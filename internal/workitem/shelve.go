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
// shelved item are also shelved recursively. Returns all shelved items, with
// the item the caller NAMED first and everything the cascade hid after it.
//
// It refuses an item that declares a remainder with nothing tracking it, or one
// tagged blocked-on-*, unless WithShelveOverride records a reason — see
// shelveguard.go for the guards and for why shelve had none until mg-2cf0.
//
// THE GUARD APPLIES TO THE NAMED ITEM AND TO NOTHING ELSE. Extending it to the
// cascade would refuse the operator's shelve on the strength of an item they
// never mentioned, and would leave a dependent sitting in available/ with its
// dependency gone — a worse state than either outcome. The cascade's problem is
// that it was INVISIBLE, and the fix for an invisible move is to report it: the
// ids it hides now travel on the work.shelve event and are printed at the CLI.
func Shelve(root, id string, opts ...ShelveOption) ([]*Item, error) {
	o := newShelveOpts(opts)

	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}

	// Status is checked before the guards so an already-shelved or done item
	// still reports what it is, rather than being told about a successor it
	// could not act on anyway.
	if err := checkShelveable(id, status); err != nil {
		return nil, err
	}

	// Read BEFORE the move: the guards run on the item as it stands, and an
	// item that fails them must not have been moved to learn that.
	item, err := readFile(path)
	if err != nil {
		return nil, err
	}

	if guardErr := checkShelveGuards(root, item); guardErr != nil {
		if o.override == "" {
			return nil, guardErr
		}
		// Both halves are recorded: WHICH guard was bypassed, and WHAT the
		// operator knew that the guard did not. Either alone is unreadable
		// later — a code with no reason says only that somebody insisted, and a
		// reason with no code does not say what it answers.
		event.Emit(root, "work.shelve_forced", map[string]string{
			"item_id": item.ID,
			"guard":   mgerrCode(guardErr),
			"reason":  o.override,
			"actor":   actor(),
		})
	}

	return shelveCascade(root, id, "")
}

// checkShelveable reports whether an item in the given status may be shelved.
func checkShelveable(id, status string) error {
	switch status {
	case "available", "claimed", "pending":
		return nil
	case "shelved":
		return fmt.Errorf("work item %s is already shelved", id)
	default:
		return fmt.Errorf("cannot shelve %s: item is %s", id, status)
	}
}

// shelveCascade moves one item to shelved/ and then everything that depends on
// it, emitting one work.shelve per item moved. cascadedFrom names the item whose
// shelving pulled this one in, and is "" for the item the operator named.
//
// It runs NO guards: Shelve gates the named item and the cascade is reported
// rather than gated (see Shelve).
//
// The event is emitted AFTER the cascade so that its `dependents` field lists
// the items this shelve ACTUALLY hid, transitively, rather than the ones it
// hoped to. That puts a parent's event after its children's in the log; the
// children carry `cascaded_from` naming the parent, so causality is stated in
// the payload rather than inferred from line order, which is the more reliable
// of the two anyway.
func shelveCascade(root, id, cascadedFrom string) ([]*Item, error) {
	path, status, err := FindPath(root, id)
	if err != nil {
		return nil, err
	}
	if err := checkShelveable(id, status); err != nil {
		return nil, err
	}

	item, err := readFile(path)
	if err != nil {
		return nil, err
	}

	dst := filepath.Join(root, "work", "shelved", id+".md")
	if err := os.Rename(path, dst); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: could not be shelved: %s", id, fsErrText(err)))
	}

	// The result sidecar must follow the .md into shelved/.
	if err := moveResultSidecar(filepath.Dir(path), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: shelved, but result sidecar could not follow: %s", id, fsErrText(err)))
	}

	shelved := []*Item{item}
	var hidden []string

	// Shelve dependents: items whose depends list includes this ID.
	dependents, err := findDependents(root, id)
	if err != nil {
		dependents = nil // best-effort: shelve what we can, report what we hid
	}

	for _, dep := range dependents {
		more, err := shelveCascade(root, dep.ID, id)
		if err != nil {
			continue // skip items that can't be shelved (e.g., already done)
		}
		for _, m := range more {
			hidden = append(hidden, m.ID)
		}
		shelved = append(shelved, more...)
	}

	kvs := map[string]string{
		"item_id":     id,
		"from_status": status,
		"to_status":   "shelved",
		"actor":       actor(),
		// Always present, empty when nothing was hidden. A field that appears
		// only when non-empty makes "hid nothing" and "written before mg-2cf0"
		// the same observation; always writing it makes absence mean exactly
		// one thing.
		"dependents": strings.Join(hidden, ","),
	}
	if cascadedFrom != "" {
		kvs["cascaded_from"] = cascadedFrom
	}
	event.Emit(root, "work.shelve", kvs)

	return shelved, nil
}

// ShelveByTag shelves all items with the given tag (and their dependents).
// Returns the items it shelved AND the tagged items a guard refused, each with
// the refusal.
//
// It routes every item through Shelve rather than moving anything itself, so
// the guards apply here too: a bulk shelve that skipped them would be a bypass
// of the targeted form's refusal one flag away, and the guards would be
// decorative. There is deliberately NO override on this form — an override is a
// statement about ONE item that the operator knows something the guard does
// not, and a bulk one is a statement about items they have not looked at.
//
// Refused items are RETURNED rather than swallowed, mirroring the archive
// sweep's skipped list: one guarded item must not stop the rest, but a shelve
// that quietly declined some of its own selection is indistinguishable from one
// that shelved them, which is the silence these guards exist to break.
func ShelveByTag(root, tag string) (shelved []*Item, skipped []SkippedItem, err error) {
	// Collect the items to shelve from active statuses.
	var toShelve []*Item
	for _, status := range []string{"available", "claimed", "pending"} {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, item := range items {
			for _, t := range item.Tags {
				if t == tag {
					toShelve = append(toShelve, item)
					break
				}
			}
		}
	}

	if len(toShelve) == 0 {
		return nil, nil, fmt.Errorf("no items found with tag %q", tag)
	}

	shelvedSet := make(map[string]bool)
	for _, item := range toShelve {
		if shelvedSet[item.ID] {
			continue
		}
		// A tagged item an earlier cascade already hid is not a refusal, and
		// reporting it as one would fill the skipped list with items that went
		// exactly where the operator asked.
		if st, err := Status(root, item.ID); err == nil && st == "shelved" {
			continue
		}
		items, err := Shelve(root, item.ID)
		if err != nil {
			skipped = append(skipped, SkippedItem{Item: item, Reason: err})
			continue
		}
		for _, it := range items {
			if !shelvedSet[it.ID] {
				shelvedSet[it.ID] = true
				shelved = append(shelved, it)
			}
		}
	}

	return shelved, skipped, nil
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

	// gateOpen, not allDepsMet: an item shelved while snoozed must come back
	// still snoozed. Unshelving lifts the shelf, not every other gate on it.
	subdir := "available"
	if !gateOpen(item, doneIDs, snoozeNow()) {
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
		"actor":       actor(),
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
