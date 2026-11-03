package workitem

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/store"
)

// Reopen atomically moves a work item from done/ back to claimed/.
// The item must currently be in done/. Returns the reopened Item.
func Reopen(root, id string) (*Item, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "done" {
		return nil, explainReopenFailure(root, id)
	}

	// A reopened claim records no holder (claimed/<id>.md on the filesystem,
	// as it always has): nobody claimed it, it was handed back.
	if err := transition(root, m.ID, m.State(), claimedBy(""), unheld()); err != nil {
		if isLost(err) {
			return nil, explainReopenFailure(root, id)
		}
		return nil, ioErr(fmt.Sprintf("%s: could not be reopened: %s", id, fsErrText(err)))
	}

	item, err := readIn(root, m.ID, store.Claimed)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.reopen", map[string]string{
		"item_id":     id,
		"from_status": "done",
		"to_status":   "claimed",
		"actor":       actor(),
	})

	return item, nil
}
