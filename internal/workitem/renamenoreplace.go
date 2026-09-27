package workitem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// renameNoReplace moves src to dst, refusing when dst already exists.
//
// os.Rename is rename(2), which REPLACES an existing destination silently. For
// a move between status directories that is data loss whenever the destination
// holds a different record under the same filename — the legacy same-id twins
// that predate globally-unique ids (mg-38b9) are exactly that case, and
// unarchiving one onto its live twin used to exit 0 with one file left
// (mg-1096).
//
// The check is race-safe: link(2) fails with EEXIST atomically when dst exists,
// so there is no window between "is it there?" and "move it" for another writer
// to fill. Only after the link succeeds is src unlinked; the two names share an
// inode, so the mtime the archive partitions on is preserved exactly as rename
// preserved it. If the unlink fails, the new link is removed again so the item
// is not left in two directories — no bytes are lost either way.
//
// A refusal is a conflict (exit 4) naming both paths. Any other failure is
// returned as the raw filesystem error for the caller to sanitize.
func renameNoReplace(id, src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errDestinationExists(id, src, dst)
		}
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// errDestinationExists is the refusal renameNoReplace returns. Both paths are
// named on purpose: the two files are different records sharing one id, and
// the operator has to look at both to decide which one is which.
func errDestinationExists(id, src, dst string) *mgerr.Error {
	return mgerr.Conflict(
		"destination_exists",
		fmt.Sprintf("%s: refusing to move %s onto %s — a record with the same id already exists there, and moving would destroy it.", id, src, dst),
		"Both files are untouched. Inspect them and move one aside by hand; do not renumber a legacy same-id twin (mg-38b9).",
	)
}
