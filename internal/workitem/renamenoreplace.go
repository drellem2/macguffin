package workitem

import (
	"fmt"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// A move between status directories must never REPLACE an existing
// destination. rename(2) replaces silently, and for a move onto a directory
// holding a different record under the same name that is data loss — the
// legacy same-id twins that predate globally-unique ids (mg-38b9) are exactly
// that case, and unarchiving one onto its live twin used to exit 0 with one
// file left (mg-1096). The refusing move itself is the backend's
// (Backend.Archive, store.Unarchiver; on the filesystem, link(2) then unlink);
// what is left here is how mg reports a refusal.

// errDestinationExists is how a refused no-replace move is reported. Both paths are
// named on purpose: the two files are different records sharing one id, and
// the operator has to look at both to decide which one is which.
func errDestinationExists(id, src, dst string) *mgerr.Error {
	return mgerr.Conflict(
		"destination_exists",
		fmt.Sprintf("%s: refusing to move %s onto %s — a record with the same id already exists there, and moving would destroy it.", id, src, dst),
		"Both files are untouched. Inspect them and move one aside by hand; do not renumber a legacy same-id twin (mg-38b9).",
	)
}
