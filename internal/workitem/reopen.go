package workitem

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/event"
)

// Reopen atomically moves a work item from done/ back to claimed/.
// The item must currently be in done/. Returns the reopened Item.
func Reopen(root, id string) (*Item, error) {
	src := filepath.Join(root, "work", "done", id+".md")
	dst := filepath.Join(root, "work", "claimed", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			return nil, explainReopenFailure(root, id)
		}
		return nil, fmt.Errorf("%s: could not be reopened: %s", id, fsErrText(err))
	}

	item, err := readFile(dst)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.reopen", map[string]string{
		"item_id":     id,
		"from_status": "done",
		"to_status":   "claimed",
		"actor":       actorFor(item),
	})

	return item, nil
}
