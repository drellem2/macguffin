package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	now := snoozeNow()

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

		if gateOpen(item, doneIDs, now) {
			// promote() carries the whole move, including clearing the spent
			// gate. A false with no error means a concurrent promoter — the
			// per-invocation AutoPromote, another agent's `mg done` — won the
			// rename; the item is in available/ either way, and only the winner
			// reports it so a promotion is never announced twice.
			ok, err := promote(root, path, e.Name(), item)
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

	// Scan done/
	doneDir := filepath.Join(root, "work", "done")
	entries, err := os.ReadDir(doneDir)
	if err != nil {
		return nil, fmt.Errorf("reading done/: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if idx := strings.Index(name, ".md"); idx > 0 {
			ids[name[:idx]] = true
		}
	}

	// Scan archive/ partitions
	archiveRoot := filepath.Join(root, "work", "archive")
	partitions, err := os.ReadDir(archiveRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return ids, nil
		}
		return nil, fmt.Errorf("reading archive/: %w", err)
	}
	for _, p := range partitions {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(archiveRoot, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if idx := strings.Index(name, ".md"); idx > 0 {
				ids[name[:idx]] = true
			}
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
