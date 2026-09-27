package workitem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
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
	return unshelve(root, id, false, 0)
}

// UnshelveClaim restores a shelved work item that was CLAIMED when it was
// shelved, making it the caller's claim (stamped with pid; 0 means the calling
// process) in a single rename, so it is never visible in available/ where a
// dispatcher could hand it to someone else (drellem2/macguffin#34).
//
// It is a NEW claim, not a restore of the old one: shelve drops the claim's
// PID, and mg never invents an owner (see restorableStatuses in unarchive.go).
// The one thing it does take from the log is the permission — it refuses
// unless the item's latest work.shelve says from_status=claimed, and it refuses
// rather than guesses when the log does not say.
//
// An item whose gates are closed still goes to pending/, unclaimed, exactly as
// a plain unshelve would put it: a claim on work that cannot start yet is not
// something --claim may manufacture. Dependents come back exactly as a plain
// unshelve brings them back — the claim applies to the named item only.
func UnshelveClaim(root, id string, pid int) ([]*Item, error) {
	return unshelve(root, id, true, pid)
}

// shelvedFrom returns the status id held when it was last shelved, read from
// the work.shelve record in the event log, or "" when the log does not say.
// Last record wins, as in archivedFrom: an item may be shelved, restored and
// shelved again, and only the most recent transition describes it now.
func shelvedFrom(root, id string) string {
	entries, err := event.List(root, event.ListOpts{Type: "work.shelve"})
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Extra["item_id"] == id {
			return entries[i].Extra["from_status"]
		}
	}
	return ""
}

// errNotShelvedFromClaimed is the refusal UnshelveClaim returns when the log
// does not say the item was claimed when it was shelved — either because it
// says something else, or because it says nothing (from == "").
func errNotShelvedFromClaimed(id, from string) *mgerr.Error {
	hint := fmt.Sprintf("Run 'mg unshelve %s' and then 'mg claim %s'.", id, id)
	if from == "" {
		return mgerr.Conflict(
			"unknown_prior_status",
			fmt.Sprintf("%s: the store has no record of the status it held when it was shelved, so --claim cannot tell it was claimed.", id),
			hint,
		)
	}
	return mgerr.Conflict(
		"not_shelved_from_claimed",
		fmt.Sprintf("%s: was %s, not claimed, when it was shelved; --claim only takes back an item that was claimed.", id, from),
		hint,
	)
}

func unshelve(root, id string, claim bool, pid int) ([]*Item, error) {
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

	// Checked before the gates: an item that was never claimed is refused
	// whether or not it could start now, so the answer does not depend on
	// the state of its dependencies.
	if claim {
		if from := shelvedFrom(root, item.ID); from != "claimed" {
			return nil, errNotShelvedFromClaimed(item.ID, from)
		}
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

	kvs := map[string]string{
		"item_id":     id,
		"from_status": "shelved",
		"actor":       actor(),
	}
	var dst string
	if claim && subdir == "available" {
		subdir = "claimed"
		if pid == 0 {
			pid = os.Getpid()
		}
		kvs["pid"] = strconv.Itoa(pid)
		dst = filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", id, pid))
		// One rename, straight from shelved/ to claimed/: the item is never in
		// available/, so no dispatcher can claim it in between. renameNoReplace
		// so a same-id record already at dst is refused, not destroyed.
		if err := renameNoReplace(id, src, dst); err != nil {
			var me *mgerr.Error
			if errors.As(err, &me) {
				return nil, me
			}
			if os.IsNotExist(err) {
				return nil, explainUnshelveFailure(root, id)
			}
			return nil, ioErr(fmt.Sprintf("%s: could not be unshelved: %s", id, fsErrText(err)))
		}
	} else {
		dst = filepath.Join(root, "work", subdir, id+".md")
		if err := os.Rename(src, dst); err != nil {
			return nil, ioErr(fmt.Sprintf("%s: could not be unshelved: %s", id, fsErrText(err)))
		}
	}
	kvs["to_status"] = subdir

	// The result sidecar must follow the .md out of shelved/.
	if err := moveResultSidecar(filepath.Dir(src), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: unshelved, but result sidecar could not follow: %s", id, fsErrText(err)))
	}

	event.Emit(root, "work.unshelve", kvs)

	unshelved := []*Item{item}

	// Unshelve dependents that were shelved along with this item
	dependents, err := findShelvedDependents(root, id)
	if err != nil {
		return unshelved, nil
	}

	for _, dep := range dependents {
		more, err := unshelve(root, dep.ID, false, 0)
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
