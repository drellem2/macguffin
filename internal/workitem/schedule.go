package workitem

import (
	"fmt"

	"github.com/drellem2/macguffin/store"
)

// Schedule checks all items in pending/ and promotes any whose gates have all
// opened — every dependency landed in done/ or archive/, and any `snooze:`
// wake time now in the past. Returns the list of promoted items.
//
// The snooze half is LEVEL-triggered on purpose: it asks whether the wake time
// has passed, not whether it just arrived. A sweep that does not run at the
// wake instant — pogod down, host asleep, driver unregistered for an hour —
// therefore delays the item until the next sweep and can never lose it. Every
// promotion this sweep makes is one it would make again from the same on-disk
// state, which is what makes a missed fire survivable.
//
// This is no longer the ONLY thing that opens a clock gate. AutoPromote runs in
// every mg process and opens elapsed snoozes without anybody scheduling it;
// this remains the full sweep — both gates, plus the held and stranded reports
// that nothing else produces. The two share promote(), so they can run
// concurrently against the same store without either losing or duplicating an
// item.
func Schedule(root string) ([]*Item, error) {
	pending, err := listDocs(root, store.Pending)
	if err != nil {
		return nil, fmt.Errorf("reading pending/: %w", err)
	}

	// Build set of done item IDs
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}

	now := snoozeNow()

	var promoted []*Item
	for i := range pending {
		m := matchOf(pending[i])
		item, err := readMatch(m)
		if err != nil {
			continue // skip malformed files
		}

		if gateOpen(item, doneIDs, now) {
			// promote() carries the whole move, including clearing the spent
			// gate. A false with no error means a concurrent promoter — the
			// per-invocation AutoPromote, another agent's `mg done` — won the
			// rename; the item is in available/ either way, and only the winner
			// reports it so a promotion is never announced twice.
			ok, err := promote(root, m, item)
			if err != nil {
				return nil, err
			}
			if ok {
				promoted = append(promoted, item)
			}
		}
	}

	return promoted, nil
}

// doneIDSet returns a set of all item IDs that are completed,
// checking both done/ and archive/ directories.
func doneIDSet(root string) (map[string]bool, error) {
	ids := make(map[string]bool)
	for _, status := range []string{"done", "archived"} {
		got, err := idsIn(root, status)
		if err != nil {
			return nil, fmt.Errorf("reading %s/: %w", status, err)
		}
		for _, id := range got {
			ids[id] = true
		}
	}
	return ids, nil
}

// allDepsMet returns true if every dependency ID is in the done set. It is the
// dependency half of gateOpen (see snooze.go) — call gateOpen, not this,
// anywhere the question is "may this item be released to available/".
func allDepsMet(deps []string, doneIDs map[string]bool) bool {
	for _, dep := range deps {
		if !doneIDs[dep] {
			return false
		}
	}
	return true
}
