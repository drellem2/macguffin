package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AutoPromote is the answer to "a snoozed item became available only when one
// particular cron on one particular agent happened to run".
//
// Every mg process calls this. A snooze whose wake time has passed is opened by
// whichever mg invocation next touches the store — `mg list`, `mg claim`,
// `mg show`, anything — so readiness is a property of the store and the binary
// rather than of mayor's `mg-schedule-sweep` still being alive. The sweep that
// reported "the previous sweep ran 4d 9h ago" had left four days of open gates
// shut; nothing here can go four days without running unless nobody is using mg
// at all, in which case there is nobody the delay could matter to.
//
// # Why this is not the whole sweep
//
// It promotes the CLOCK gate only. It skips any pending item with no `snooze:`
// value, and it does not compute the done-set unless it has found at least one
// elapsed wake time. That matters because this runs on `mg list`: the common
// case is one ReadDir of pending/ (by construction the small directory), a parse
// of each small file, and zero writes and zero further syscalls.
//
// The dependency gate is deliberately left to Schedule. It is not the gate that
// went stale — a dependency opens when its parent is completed, and `mg done`
// already sweeps at that instant, so the dependency gate has never depended on
// a cron the way the clock gate did.
//
// Both gates are still ANDed for anything it does promote: gateOpen is the one
// predicate, and an item whose wake time passed while a parent is still open is
// not ready.
//
// # Errors
//
// It returns them; it does not print them. The caller runs it beside the user's
// actual command and must never fail that command with it — an unwritable store
// makes `mg list` print a warning, never exit non-zero.
func AutoPromote(root string) ([]*Item, error) {
	pendingDir := filepath.Join(root, "work", "pending")
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		return nil, fmt.Errorf("reading pending/: %w", err)
	}

	now := snoozeNow()

	// Pass one: find elapsed clock gates. No writes, and no done-set — the
	// overwhelmingly common outcome is that this list is empty and the process
	// has done nothing but read one directory.
	type candidate struct {
		name string
		item *Item
	}
	var ready []candidate
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(pendingDir, e.Name())
		item, err := readFile(path)
		if err != nil {
			continue // skip malformed files; Stranded is the channel for those
		}
		if item.SnoozeRaw == "" {
			continue // no clock gate: not this promoter's business
		}
		if item.snoozeHolds(now) {
			continue // still closed, or malformed and therefore held (see snoozeHolds)
		}
		ready = append(ready, candidate{name: e.Name(), item: item})
	}
	if len(ready) == 0 {
		return nil, nil
	}

	// Pass two: only now is the expensive half worth paying for.
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}

	var promoted []*Item
	for _, c := range ready {
		if !allDepsMet(c.item.Depends, doneIDs) {
			continue // the clock opened, a parent has not
		}
		ok, err := promote(root, filepath.Join(pendingDir, c.name), c.name, c.item)
		if err != nil {
			return promoted, err
		}
		if ok {
			promoted = append(promoted, c.item)
		}
	}
	return promoted, nil
}

// ClearSpentGates wipes elapsed `snooze:` values off items that are no longer
// pending, and reports the ids it cleared.
//
// This is the self-heal for promote's one residue. A process killed between the
// rename into available/ and the clear that follows it leaves an inert elapsed
// wake time on the item; so does a claim landing inside that same window, which
// carries the item into claimed/ before the clear can find it. Neither state
// gates anything, but both read as though the item were still scheduled, and a
// remedy that leaves its own litter around is not one you can trust twice.
//
// It runs from `mg schedule`, not from the every-invocation path: it costs two
// directory scans, and there is nothing urgent about cosmetic litter.
//
// A FUTURE wake time on a non-pending item is left alone. Only a hand-edit
// produces one, UnsnoozeItem already documents it as inert, and silently
// deleting a gate somebody typed on purpose is not a repair.
func ClearSpentGates(root string) ([]string, error) {
	now := snoozeNow()
	var cleared []string
	for _, status := range []string{"available", "claimed"} {
		dir := filepath.Join(root, "work", status)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cleared, fmt.Errorf("reading %s/: %w", status, err)
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".json") {
				continue
			}
			if !strings.HasSuffix(name, ".md") && !strings.Contains(name, ".md.") {
				continue
			}
			path := filepath.Join(dir, name)
			item, err := readFile(path)
			if err != nil {
				continue
			}
			if item.SnoozeRaw == "" || item.snoozeHolds(now) {
				continue
			}
			if _, err := clearSpentSnooze(root, path, item.ID); err != nil {
				return cleared, err
			}
			cleared = append(cleared, item.ID)
		}
	}
	return cleared, nil
}

// Unpromoted returns pending items whose gates are ALL open — which, after a
// sweep, must be none.
//
// This is the detector that replaces `mg schedule`'s staleness warning as the
// snooze safety net. That warning worked by measuring the sweep's own absence,
// which stops meaning anything once promotion no longer needs the sweep. This
// measures the thing itself: an item sitting in pending/ with nothing holding
// it, immediately after a sweep that was supposed to move it, is positive
// evidence that promotion is FAILING — a read-only store, a full disk, a
// permissions change — rather than evidence that a cron went missing.
//
// It is strictly better evidence than the warning was. An absent cron was a
// proxy for "snoozes may not be opening"; this is the observation itself, and
// it fires for causes the cron check could never have seen.
func Unpromoted(root string) ([]*Item, error) {
	pending, err := ListByStatus(root, "pending")
	if err != nil {
		return nil, err
	}
	doneIDs, err := doneIDSet(root)
	if err != nil {
		return nil, err
	}
	now := snoozeNow()
	var out []*Item
	for _, item := range pending {
		if gateOpen(item, doneIDs, now) {
			out = append(out, item)
		}
	}
	return out, nil
}
