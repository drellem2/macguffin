package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// This file is the ONE place that turns a short work-item ID into a file on
// disk. Before it existed, seven call sites (Read, FindPath, Status, Done,
// Unclaim, Claim, Reopen) each walked work/ independently and each took the
// first filesystem hit. None counted matches, so a duplicated short ID — 4 hex
// digits is a 65,536-value space, and the birthday bound bites hard — resolved
// silently to whichever directory happened to be scanned first. Worse, `mg
// show` walked twice (once for the body, once for the status) and could render
// one item's body under another item's status.
//
// Resolve returns EVERY match, which is what makes "error on ambiguity"
// expressible at all. ResolveUnique is the resolver every caller should use.

// activeStates are the non-archive lifecycle directories under work/, in the
// order they are scanned. The order is only observable in the candidate list of
// an ambiguity error — a healthy store has exactly one match.
var activeStates = []string{"available", "claimed", "done", "pending", "shelved"}

// Match is one filesystem hit for a work-item ID.
type Match struct {
	ID        string // the short ID that was resolved
	Path      string // absolute path to the .md file
	Status    string // available | claimed | done | pending | shelved | archived
	Partition string // archive partition (e.g. "2026-07"); empty unless Status=="archived"
}

// matchesID reports whether a directory entry names the work item id. Claimed
// items carry a PID suffix (<id>.md.<pid>), so both forms count. Sidecars
// (<id>.result.json) deliberately do not.
func matchesID(name, id string) bool {
	return name == id+".md" || strings.HasPrefix(name, id+".md.")
}

// Resolve returns every file in the store that names the given ID, across the
// active lifecycle directories and every archive partition. A healthy store
// yields 0 or 1 matches; 2+ means the short ID is ambiguous.
//
// Unreadable directories are skipped rather than failing the resolve: a missing
// work/shelved/ must not make a live item unresolvable.
func Resolve(root, id string) ([]Match, error) {
	if id == "" {
		return nil, nil
	}

	var matches []Match

	for _, state := range activeStates {
		dir := filepath.Join(root, "work", state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if matchesID(e.Name(), id) {
				matches = append(matches, Match{
					ID:     id,
					Path:   filepath.Join(dir, e.Name()),
					Status: state,
				})
			}
		}
	}

	// Archive partitions (archive/YYYY-MM/), scanned in ascending name order.
	// NOTE: archived items sitting loose in work/archive/ rather than in a
	// partition are invisible here, exactly as they were to the seven walks
	// this function replaced. That is mg-a9c0, tracked separately; when it is
	// fixed, this loop is the only place that needs to change.
	archiveRoot := filepath.Join(root, "work", "archive")
	if partitions, err := os.ReadDir(archiveRoot); err == nil {
		for _, p := range partitions {
			if !p.IsDir() {
				continue
			}
			dir := filepath.Join(archiveRoot, p.Name())
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if matchesID(e.Name(), id) {
					matches = append(matches, Match{
						ID:        id,
						Path:      filepath.Join(dir, e.Name()),
						Status:    "archived",
						Partition: p.Name(),
					})
				}
			}
		}
	}

	return matches, nil
}

// ResolveUnique resolves an ID to exactly one item. It returns a not_found
// error when nothing matches and an ambiguous_id conflict, naming every
// candidate, when more than one does. It never guesses.
func ResolveUnique(root, id string) (Match, error) {
	matches, err := Resolve(root, id)
	if err != nil {
		return Match{}, err
	}
	switch len(matches) {
	case 0:
		return Match{}, errNoSuchItem(id)
	case 1:
		return matches[0], nil
	default:
		return Match{}, errAmbiguousID(root, id, matches)
	}
}

// ReadWithStatus loads an item and its lifecycle status from a SINGLE resolve.
// `mg show` used to call Read and Status separately: two independent walks that
// could disagree with each other when the ID was ambiguous, rendering one
// item's body under another item's status.
func ReadWithStatus(root, id string) (*Item, string, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, "", err
	}
	item, err := readFile(m.Path)
	if err != nil {
		return nil, "", err
	}
	return item, m.Status, nil
}

// errAmbiguousID reports that a short ID names more than one work item, listing
// every candidate. Paths are shown relative to the store root: the whole point
// of the error is to hand the operator the files, since mg cannot tell them
// apart by ID alone.
func errAmbiguousID(root, id string, matches []Match) *mgerr.Error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: ambiguous — %d work items share this ID:", id, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(root, m.Path)
		if err != nil {
			rel = m.Path
		}
		fmt.Fprintf(&b, "\n  %s (%s)", rel, m.Status)
	}
	return mgerr.Conflict("ambiguous_id", b.String(),
		"Short IDs are 4 hex digits and can collide. Inspect the files above directly; mg will not guess between them.")
}
