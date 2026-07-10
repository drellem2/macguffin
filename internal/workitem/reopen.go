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
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	if m.Status != "done" {
		return nil, explainReopenFailure(root, id)
	}

	src := m.Path
	dst := filepath.Join(root, "work", "claimed", id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			return nil, explainReopenFailure(root, id)
		}
		return nil, ioErr(fmt.Sprintf("%s: could not be reopened: %s", id, fsErrText(err)))
	}

	// The result sidecar must follow the .md, or it is orphaned in done/.
	if err := moveResultSidecar(filepath.Dir(src), filepath.Dir(dst), id); err != nil {
		return nil, ioErr(fmt.Sprintf("%s: reopened, but result sidecar could not follow: %s", id, fsErrText(err)))
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
