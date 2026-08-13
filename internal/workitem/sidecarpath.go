package workitem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// This file answers the one question the store could not previously be asked:
// WHERE is this item's result?
//
// Until it existed every consumer built the path by hand, and the shape
// everyone reached for was a glob:
//
//	ls ~/.macguffin/work/*/<id>.result.json
//
// That glob is ONE LEVEL deep and the archive is nested by month
// (work/archive/2026-08/<id>.result.json), so it cannot match an archived
// sidecar by construction — and the archive holds ~94% of the store's
// sidecars. On 2026-08-13 two agents, ninety minutes apart, took the glob's
// empty output as proof that a result did not exist; both items had one, both
// said verdict=pass, and both false negatives reached work-item bodies. Their
// shape is the same: a failing glob does not error INTO the result, it errors
// BESIDE it, and what lands in the result is the empty set.
//
// SidecarFor closes that by never searching at all. It asks the resolver where
// the item's .md is and derives the sidecar from THAT directory, so the answer
// follows the item into archive/2026-08/ (or anywhere a future partition scheme
// puts it) with no knowledge of the layout. It returns one path or an error;
// it never returns a candidate list, because picking from a candidate list is
// the failure this replaces.

// Sidecar is the located result sidecar of one work item.
//
// Path is always populated when SidecarFor returns without error — it is where
// the sidecar WOULD be, derived from the item's own .md — and Exists says
// whether the file is actually there. Keeping those separate is deliberate:
// "the item has no result" and "I could not look" must never render as the same
// empty string, and a caller that only ever sees a path cannot tell them apart.
type Sidecar struct {
	ID        string // the item's short id, without any @partition qualifier
	Path      string // absolute path to <id>.result.json beside the item's .md
	Status    string // available | claimed | done | pending | shelved | archived
	Partition string // archive partition (e.g. "2026-08"); empty unless archived
	Exists    bool   // whether Path is actually present on disk
}

// SidecarFor derives the path a work item's result sidecar occupies and reports
// whether it is there.
//
// It resolves the item first (honouring the @partition qualifier, the live/
// terminal shadow rule, and the ambiguity refusal — see ResolveUnique) and then
// joins the sidecar name onto the item's OWN directory. No directory is
// scanned for the sidecar and no pattern is matched, so the month-nested
// archive needs no special case here: whatever directory holds the .md holds
// the result.
//
// The three outcomes are distinct on purpose:
//
//	item does not resolve   -> error (not_found / ambiguous_id), Sidecar zero
//	item resolves, no file  -> nil error, Exists=false, Path populated
//	the stat itself failed  -> error (io_error), so an unreadable store can
//	                           never be reported as "no result"
func SidecarFor(root, id string) (Sidecar, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return Sidecar{}, err
	}
	return SidecarOf(m)
}

// SidecarOf is SidecarFor for a caller that has ALREADY resolved the item —
// `mg show`, which needs the item's body and its result from one resolve. The
// derivation is the same and is deliberately the only one: the sidecar's
// directory is the item's directory, never a directory chosen by search.
func SidecarOf(m Match) (Sidecar, error) {
	sc := Sidecar{
		ID:        m.ID,
		Path:      filepath.Join(filepath.Dir(m.Path), resultSidecarName(m.ID)),
		Status:    m.Status,
		Partition: m.Partition,
	}
	info, err := os.Stat(sc.Path)
	switch {
	case err == nil:
		if info.IsDir() {
			// A directory where the sidecar belongs is a broken store, not an
			// absent result. Saying so beats reporting "no result" for
			// something that is plainly there.
			return sc, ioErr(fmt.Sprintf("%s: %s is a directory, not a result sidecar.", sc.ID, sc.Path))
		}
		sc.Exists = true
	case os.IsNotExist(err):
		sc.Exists = false
	default:
		// Permission denied, an I/O error, a broken symlink chain: we did not
		// learn that the result is absent, we learned that we could not look.
		return sc, ioErr(fmt.Sprintf("%s: cannot read %s: %s", sc.ID, sc.Path, fsErrText(err)))
	}
	return sc, nil
}

// ReadSidecar returns the bytes of an item's result sidecar.
//
// An absent sidecar is ErrNoSidecar (not_found, exit 3) — a DIFFERENT category
// and a different slug from the io_error (internal, exit 1) an unreadable store
// produces, so a caller branching on the exit code can tell "there is no
// result" from "I could not find out". Neither returns bytes, so neither can be
// rendered as an empty result.
func ReadSidecar(root, id string) (Sidecar, []byte, error) {
	sc, err := SidecarFor(root, id)
	if err != nil {
		return sc, nil, err
	}
	if !sc.Exists {
		return sc, nil, ErrNoSidecar(sc)
	}
	data, err := os.ReadFile(sc.Path)
	if err != nil {
		// The file was there a moment ago, so this is a read failure and not an
		// absence — it must not collapse into ErrNoSidecar.
		return sc, nil, ioErr(fmt.Sprintf("%s: cannot read %s: %s", sc.ID, sc.Path, fsErrText(err)))
	}
	return sc, data, nil
}

// SidecarResult decodes an item's result sidecar as JSON for embedding in a
// machine-readable view.
//
// ok is false when the sidecar is absent OR when its bytes are not valid JSON;
// the two are distinguished by Sidecar.Exists, which the caller already holds.
// A sidecar that is present but unparseable is NOT an error here: `mg show
// --json` must still render the item, and a reader that sees result_path set
// with result null has been told exactly where to look by hand.
func SidecarResult(root, id string) (Sidecar, json.RawMessage, bool, error) {
	sc, err := SidecarFor(root, id)
	if err != nil {
		return sc, nil, false, err
	}
	return sidecarResult(sc)
}

// SidecarResultOf is SidecarResult for an already-resolved item. See SidecarOf.
func SidecarResultOf(m Match) (Sidecar, json.RawMessage, bool, error) {
	sc, err := SidecarOf(m)
	if err != nil {
		return sc, nil, false, err
	}
	return sidecarResult(sc)
}

func sidecarResult(sc Sidecar) (Sidecar, json.RawMessage, bool, error) {
	if !sc.Exists {
		return sc, nil, false, nil
	}
	data, err := os.ReadFile(sc.Path)
	if err != nil {
		return sc, nil, false, ioErr(fmt.Sprintf("%s: cannot read %s: %s", sc.ID, sc.Path, fsErrText(err)))
	}
	if !json.Valid(data) {
		return sc, nil, false, nil
	}
	return sc, json.RawMessage(data), true, nil
}

// ErrNoSidecar reports that an item exists but has recorded no result. It names
// the exact path that is absent, because the next question a reader has is
// "absent WHERE" — and answering it is what stops them reaching for a glob.
func ErrNoSidecar(sc Sidecar) *mgerr.Error {
	return mgerr.NotFound("no_sidecar",
		fmt.Sprintf("%s: no result sidecar (item is %s).", sc.ID, sc.Status),
		fmt.Sprintf("The item exists and has recorded no result; %s does not exist. This is not a failed lookup — do not retry it as a glob.", sc.Path))
}
