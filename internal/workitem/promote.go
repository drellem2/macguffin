package workitem

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
)

// promote releases one pending item to available/. It is THE promotion, used by
// both the explicit sweep (Schedule) and the opportunistic promoter every mg
// process runs (AutoPromote), so there is exactly one implementation of "a gate
// opened, move the file" for every concurrent promoter to agree with.
//
// # Why the rename comes first
//
// mg has one concurrency primitive and it is rename(2): if two processes race,
// exactly one succeeds and the other gets ENOENT (see Claim). Promotion is now
// on the path of EVERY mg invocation, so promoters race constantly — twelve
// polecats plus crew — and it must be the rename, not a preceding write, that
// decides the winner.
//
// This inverts the order this code used to have. It used to clear the spent
// `snooze:` value in pending/ and then rename, so that a crash between the two
// left an un-gated pending item rather than a gated available one. Under
// concurrency that order is not merely suboptimal, it is wrong: os.WriteFile
// opens O_CREATE|O_TRUNC, so a second promoter writing after the first has
// already renamed the file away RE-CREATES it in pending/, leaving the same
// item in two directories at once. A duplicated item is a corrupt store; the
// residue of the new order is not.
//
// The new order's residue, if the process dies in the window between the rename
// and the clear, is an available item still carrying an ELAPSED wake time. That
// gate holds nothing — snoozeHolds is false once the time has passed — so the
// item is claimable, listed, and visible to stall-watch. It is inert, visible
// and self-healing: ClearSpentGates wipes it on the next `mg schedule`, and
// `mg unsnooze` clears it by hand. The old order's residue was an item invisible
// until the next sweep, which is the failure this whole feature exists to end.
//
// Returns false with a nil error when another promoter won the race. That is
// not a failure: the item is in available/ either way, which is all any caller
// wanted.
func promote(root string, m Match, item *Item) (bool, error) {
	// The single atomic instant. Everything before it is advisory; everything
	// after it is done by the one process that won. Pending items normally
	// have no sidecar, but the backend carries one if it is there: a
	// .result.json must never be left behind a moved .md.
	if err := transition(root, m.ID, m.State(), inStatus("available"), writer(0)); err != nil {
		if isLost(err) {
			return false, nil // another promoter got there first
		}
		return false, fmt.Errorf("promoting %s: %w", item.ID, err)
	}

	cleared, err := clearSpentSnooze(root, item.ID, "available")
	if err != nil {
		return false, err
	}
	if cleared {
		// Mirror the write onto the caller's copy. Both current callers use only
		// ID and Title, but returning a promoted item that still advertises the
		// gate it was just released from is a trap laid for the next one.
		item.ClearSnooze()
	}
	return true, nil
}

// clearSpentSnooze removes a wake time that has already done its work, from
// the item where it now lives (status: available/ after a promotion).
//
// A gate that has released its item is spent, and leaving the value behind
// makes an available item read as though it were still scheduled. It re-reads
// the item rather than rendering the copy the caller already parsed, so the
// window in which a concurrent `mg edit` could be overwritten is the microsecond
// between this read and this write rather than the whole promotion — and the
// write is guarded by the hash of that read, so not even then.
//
// It reports whether it actually cleared anything, so the caller can mirror the
// write onto its own copy of the item.
//
// It is a no-op, not an error, when the item is no longer in available/:
// between the transition and this call another process may have claimed it,
// and a claim carrying an inert elapsed gate is a cosmetic leftover, not a
// reason to fail a promotion that already succeeded. ClearSpentGates collects
// those. The write never re-creates the item where it was (Backend.Edit).
func clearSpentSnooze(root, id, status string) (bool, error) {
	m, err := matchIn(root, id, store.Status(status))
	if err != nil {
		return false, nil
	}
	woke := ""
	_, _, err = modifyItem(root, m, "promote", status, func(it *Item) (bool, error) {
		if it.SnoozeRaw == "" {
			woke = ""
			return false, nil
		}
		woke = it.SnoozeRaw
		it.ClearSnooze()
		return true, nil
	})
	if err != nil {
		if mgerr.Coerce(err).Code == "edit_raced" {
			return false, nil
		}
		return false, fmt.Errorf("clearing the snooze on %s: %w", id, err)
	}
	if woke == "" {
		return false, nil
	}

	event.Emit(root, "work.snooze_elapsed", map[string]string{
		"item_id": id,
		"until":   woke,
		"actor":   actor(),
	})
	return true, nil
}
