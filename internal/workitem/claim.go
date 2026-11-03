package workitem

import (
	"fmt"
	"strconv"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/store"
)

// Claim atomically moves a work item from available/ to claimed/ through the
// backend's Transition — rename(2) on the filesystem, where the claimed
// filename is suffixed with the owner PID for exactly-once semantics.
// If pid is 0, the claim is the installed Actor's (the CLI edge's process).
// Returns the claimed Item, or an error if the item doesn't exist or was already claimed.
func Claim(root, id string, pid int) (*Item, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "available" {
		return nil, explainClaimFailure(root, id)
	}

	by := writer(pid)

	// The single atomic instant: of two racing claims exactly one Transition
	// succeeds and the loser gets *store.ErrLost. The backend carries any
	// result sidecar with the item (one may sit in available/ after an
	// unarchive) rather than orphaning it behind.
	if err := transition(root, m.ID, m.State(), claimedBy(""), by); err != nil {
		if isLost(err) {
			// The item wasn't in available/ — diagnose where it really is.
			return nil, explainClaimFailure(root, id)
		}
		return nil, ioErr(fmt.Sprintf("%s: could not be claimed: %s", id, fsErrText(err)))
	}

	item, err := readIn(root, m.ID, store.Claimed)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.claim", map[string]string{
		"item_id":     id,
		"from_status": "available",
		"to_status":   "claimed",
		"actor":       actor(),
		"pid":         strconv.Itoa(by.PID),
	})

	return item, nil
}
