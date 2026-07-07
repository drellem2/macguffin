package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/drellem2/macguffin/internal/event"
)

// Claim atomically moves a work item from available/ to claimed/ using rename(2).
// The claimed filename is suffixed with the owner PID for exactly-once semantics.
// If pid is 0, it defaults to the current process's PID.
// Returns the claimed Item, or an error if the item doesn't exist or was already claimed.
func Claim(root, id string, pid int) (*Item, error) {
	src := filepath.Join(root, "work", "available", id+".md")
	if pid == 0 {
		pid = os.Getpid()
	}
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", id, pid))

	// rename(2) is atomic on local filesystems.
	// If two processes race, exactly one succeeds; the other gets ENOENT.
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			// The source wasn't in available/ — diagnose where it really is.
			return nil, explainClaimFailure(root, id)
		}
		return nil, ioErr(fmt.Sprintf("%s: could not be claimed: %s", id, fsErrText(err)))
	}

	item, err := readFile(dst)
	if err != nil {
		return nil, err
	}

	event.Emit(root, "work.claim", map[string]string{
		"item_id":     id,
		"from_status": "available",
		"to_status":   "claimed",
		"actor":       actorFor(item),
		"pid":         strconv.Itoa(pid),
	})

	return item, nil
}
