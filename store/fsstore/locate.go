package fsstore

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/store"
)

// Location is one file in the store that names a work-item id.
type Location struct {
	Path      string       // absolute path to the .md file
	Status    store.Status // the directory it was found in
	Partition string       // archive partition (e.g. "2026-07"); "" unless Status == Archived
}

// MatchesID reports whether a directory entry names the work item id. Claimed
// items carry a holder suffix (<id>.md.<pid>), so both forms count. Sidecars
// (<id>.result.json) deliberately do not.
func MatchesID(name, id string) bool {
	return name == id+".md" || strings.HasPrefix(name, id+".md.")
}

// MatchesArchivedID reports whether a loose entry sitting directly under
// work/archive/ names the given archived item. Archived records never carry the
// claimed/ PID suffix (<id>.md.<pid>), so the match is STRICT — only "<id>.md"
// counts. That strictness is the point of scanning loose files at all: it keeps
// a total scan from ingesting editor backups (<id>.md.bak, <id>.md~) or result
// sidecars (<id>.result.json) as phantom archive twins.
func MatchesArchivedID(name, id string) bool {
	return name == id+".md"
}

// Locate returns every file in the store under root that names id, across the
// active lifecycle directories, every archive partition, AND loose archived
// records sitting directly under work/archive/. A healthy store yields 0 or 1;
// 2+ means the short id is ambiguous.
//
// It is TOTAL over the store on purpose: it is the store-wide collision scan
// that mg's Create runs before its O_EXCL create, so a record this cannot see
// is a record a new mint cannot collide with — which is exactly how an item
// gets born a silent alias of a twin.
//
// Unreadable directories are skipped rather than failing the scan: a missing
// work/shelved/ must not make a live item unresolvable.
func Locate(root, id string) []Location {
	if id == "" {
		return nil
	}

	var locs []Location

	for _, st := range store.ActiveStatuses {
		dir := filepath.Join(root, "work", string(st))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if MatchesID(e.Name(), id) {
				locs = append(locs, Location{Path: filepath.Join(dir, e.Name()), Status: st})
			}
		}
	}

	// Archive records live either in a month partition (archive/YYYY-MM/) or
	// loose directly under work/archive/. Both are scanned so the scan stays
	// total over the store: a loose file — left by a future partition scheme,
	// an interrupted archive, or a restored backup — must be visible, or the
	// next mint that draws its id slips past the collision check. Partition
	// entries keep the claimed/ PID-suffix tolerance of MatchesID; loose
	// entries match strictly (see MatchesArchivedID). ReadDir returns names in
	// ascending order, so partitions resolve oldest-first.
	archiveRoot := filepath.Join(root, "work", "archive")
	if entries, err := os.ReadDir(archiveRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				if MatchesArchivedID(e.Name(), id) {
					locs = append(locs, Location{Path: filepath.Join(archiveRoot, e.Name()), Status: store.Archived})
				}
				continue
			}
			dir := filepath.Join(archiveRoot, e.Name())
			partEntries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, pe := range partEntries {
				if MatchesID(pe.Name(), id) {
					locs = append(locs, Location{Path: filepath.Join(dir, pe.Name()), Status: store.Archived, Partition: e.Name()})
				}
			}
		}
	}

	return locs
}

// holderOf returns the holder suffix of a claimed/ file name (<id>.md.<holder>),
// or "" for a bare <id>.md.
func holderOf(name, id string) string {
	return strings.TrimPrefix(strings.TrimPrefix(name, id+".md"), ".")
}

// stateOf is the store.State a Location represents.
func stateOf(l Location, id string) store.State {
	st := store.State{Status: l.Status}
	if l.Status == store.Claimed {
		st.Holder = holderOf(filepath.Base(l.Path), id)
	}
	return st
}
