package workitem

import (
	"fmt"
	"strconv"

	"github.com/drellem2/macguffin/internal/event"
)

// ReclaimResult describes a re-stamped claim.
type ReclaimResult struct {
	ID     string
	OldPID int  // PID that was recorded on the claim (0 if absent or unparseable)
	NewPID int  // PID now recorded on the claim
	Moved  bool // false when the claim already carried NewPID (no-op)
}

// Reclaim re-stamps the owner PID recorded on an existing claim, WITHOUT the
// item leaving claimed/.
//
// That last property is the entire point of this function, not an
// implementation detail. A caller that already holds a claim and wants to
// hand it to a different process has exactly one other route — Unclaim then
// Claim — and that route parks the item in available/ for the duration, where
// a concurrent claimant can take it. pogod hits this on every dispatch: it
// claims the work item itself at spawn (so an item being worked is never
// invisible to an ownership check) and the worker then re-stamps the claim to
// its own PID as its first act. A round-trip through available/ would reopen
// the duplicate-dispatch window that pogod's spawn-time claim exists to close.
// So the implementation is one rename(2) WITHIN claimed/, and must stay that
// way; TestReclaimNeverPassesThroughAvailable pins it from the outside.
//
// Requires the item to be in claimed/: there is no claim to re-stamp
// otherwise, and Reclaim deliberately cannot claim an available item — that
// would make it a Claim whose atomic refusal (the fleet's duplicate-dispatch
// guard) had been bypassed.
//
// If pid is 0 it defaults to the current process's PID, as Claim does.
// Re-stamping to the PID already recorded is a no-op and succeeds.
func Reclaim(root, id string, pid int) (*ReclaimResult, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "claimed" {
		return nil, explainReclaimFailure(root, id)
	}

	by := writer(pid)
	pid = by.PID
	oldPID := holderPID(m.Holder)

	// Idempotent: the claim already names this holder. Compare holders rather
	// than PIDs, so a claim with no parsable PID suffix (oldPID 0) is still
	// re-stamped instead of being mistaken for "already pid 0".
	if m.Holder == strconv.Itoa(pid) {
		return &ReclaimResult{ID: id, OldPID: oldPID, NewPID: pid, Moved: false}, nil
	}

	// A claimed -> claimed Transition expecting the exact holder: on the
	// filesystem a rename(2) within claimed/ — atomic, and the item is in
	// claimed/ before and after with no state in between.
	if err := transition(root, m.ID, m.State(), claimedBy(""), by); err != nil {
		if isLost(err) {
			// The claim vanished under us — diagnose where the item really is.
			return nil, explainReclaimFailure(root, id)
		}
		return nil, ioErr(fmt.Sprintf("%s: claim could not be re-stamped: %s", id, fsErrText(err)))
	}

	// Same event type as Claim, by design: a re-stamp IS a claim by a new owner,
	// and `mg spend` pairs a work.claim with the next release to attribute an
	// actor's spend — a handover that emitted nothing would bill the worker's
	// whole run to whoever claimed on its behalf. from_status and to_status are
	// both "claimed" because nothing moved between statuses.
	kvs := map[string]string{
		"item_id":     id,
		"from_status": "claimed",
		"to_status":   "claimed",
		"actor":       actor(),
		"pid":         strconv.Itoa(pid),
	}
	if oldPID > 0 {
		kvs["prev_pid"] = strconv.Itoa(oldPID)
	}
	event.Emit(root, "work.claim", kvs)

	return &ReclaimResult{ID: id, OldPID: oldPID, NewPID: pid, Moved: true}, nil
}
