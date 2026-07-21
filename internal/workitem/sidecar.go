package workitem

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// resultSidecarName is the filename of a work item's result sidecar, written by
// `mg done` next to the item's .md file (see Done). It records the completion
// result (e.g. the merged branch) as JSON.
func resultSidecarName(id string) string { return id + ".result.json" }

// moveResultSidecar moves <id>.result.json from srcDir to dstDir when it exists.
//
// The result sidecar is a companion to the work item's .md file and must travel
// with it across EVERY status transition. A sidecar left behind in the old
// status directory is an orphan: it asserts a completion for an item that has
// since moved on, corrupting the audit trail (a .result.json sitting in
// available/ next to no .md claims the item is done when it is really archived).
// This class of bug — mg status transitions orphaning the sidecar — is exactly
// what mg-ab67 fixed; every rename of an <id>.md must be paired with a call
// here so it cannot recur.
//
// It is a no-op when the item has no sidecar (the common case for items that
// were never marked done), so callers can invoke it unconditionally after the
// .md rename. dstDir is created if it does not already exist. Following the
// convention of Archive/Unarchive, the .md rename is the authoritative,
// status-defining move; the sidecar is moved immediately after and any failure
// is surfaced loudly rather than swallowed.
func moveResultSidecar(srcDir, dstDir, id string) error {
	src := filepath.Join(srcDir, resultSidecarName(id))
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	return os.Rename(src, filepath.Join(dstDir, resultSidecarName(id)))
}

// StraySidecar is a result sidecar sitting in a directory that does not hold
// its work item's .md file.
//
// The sidecar is a companion to the .md and moveResultSidecar keeps the pair
// together across every transition, so a stray is by definition a leftover from
// a transition that predates that guarantee. What a stray is NOT, necessarily,
// is a redundant copy — see Differs.
type StraySidecar struct {
	ID       string // the work item the sidecar names
	Path     string // absolute path to the stray .result.json
	Location string // lifecycle dir the stray sits in (available|claimed|done|pending|shelved|archived)

	// ItemStatus is where the item's .md actually lives — the directory a
	// reader should take the item's result from. Empty when the item cannot be
	// resolved at all, in which case the stray is the only record left.
	ItemStatus string

	// Authoritative is the path to the sidecar beside the item's .md, and
	// AuthoritativeExists reports whether that file is actually present. An
	// absent authoritative copy makes the stray load-bearing: deleting it
	// destroys the item's only result.
	Authoritative       string
	AuthoritativeExists bool

	// Differs reports whether the stray's bytes differ from the authoritative
	// copy. It is the field that decides disposition, and the reason this type
	// exists rather than a bare list of paths.
	//
	// A stray that does NOT differ is a duplicate: deleting it loses nothing.
	// A stray that DOES differ is one of two things that look identical from
	// the filesystem and cannot be told apart mechanically:
	//
	//   - a SUPERSEDED draft, left behind when the item moved on and was
	//     re-completed (harmless to delete, actively misleading to read); or
	//   - the SURVIVING copy of content the authoritative file overwrote —
	//     e.g. a gh-issue gate carrier whose payload was replaced by a short
	//     status note during an archive/unarchive round-trip.
	//
	// Both were found in this store (mg-eb1e). Deleting every stray on sight
	// destroys the second kind, so a differing stray is reported for a human to
	// judge and never auto-resolved.
	Differs bool
}

// Redundant reports whether the stray is provably safe to delete: the
// authoritative copy exists and is byte-identical, so the stray carries no
// content that would be lost.
func (s StraySidecar) Redundant() bool { return s.AuthoritativeExists && !s.Differs }

// FindStraySidecars reports every result sidecar in the store that is not
// beside its item's .md file.
//
// It resolves each sidecar's ID through Resolve rather than trusting the
// directory the file was found in. That distinction is the whole point: the
// hazard this function exists to close is that `ls work/*/<id>.result.json`
// returns directories in ALPHABETICAL order, so an orphan in available/ or
// claimed/ is returned ahead of the real copy in done/. A reader who takes the
// first hit gets the stale file and cannot tell. Callers wanting one item's
// result must ask the resolver where the item is and read that explicit path —
// never a glob.
//
// Unreadable directories are skipped rather than failing the scan, matching
// Resolve: a missing work/shelved/ must not hide strays elsewhere.
func FindStraySidecars(root string) ([]StraySidecar, error) {
	workDir := filepath.Join(root, "work")

	// dirs to scan: the active lifecycle dirs, plus archive partitions and the
	// loose archive root — the same surface Resolve covers, so a stray cannot
	// hide in a corner the resolver can already see.
	type scanDir struct{ path, location string }
	scan := []scanDir{}
	for _, state := range activeStates {
		scan = append(scan, scanDir{filepath.Join(workDir, state), state})
	}
	archiveRoot := filepath.Join(workDir, "archive")
	scan = append(scan, scanDir{archiveRoot, "archived"})
	if entries, err := os.ReadDir(archiveRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				scan = append(scan, scanDir{filepath.Join(archiveRoot, e.Name()), "archived"})
			}
		}
	}

	var strays []StraySidecar
	for _, d := range scan {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".result.json") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".result.json")
			strayPath := filepath.Join(d.path, e.Name())

			// Beside-ness is decided by the .md sharing this DIRECTORY, not by
			// the resolver picking a winner. Archived twins of one ID can sit in
			// two partitions (see the @partition qualifier), which makes the ID
			// ambiguous to ResolveUnique — but a sidecar sharing a directory
			// with one of those twins is still beside its item, not a stray.
			// Checking co-location first is what keeps every archived twin's
			// own sidecar from being reported as an orphan.
			matches, err := Resolve(root, id)
			if err != nil {
				continue
			}
			beside := false
			for _, m := range matches {
				if filepath.Dir(m.Path) == d.path {
					beside = true
					break
				}
			}
			if beside {
				continue
			}

			s := StraySidecar{ID: id, Path: strayPath, Location: d.location}
			if len(matches) != 1 {
				// No item, or several equally plausible ones: nothing here is
				// authoritative, so the stray is the only record. Report it
				// rather than skipping — that is the load-bearing case.
				strays = append(strays, s)
				continue
			}
			itemDir := filepath.Dir(matches[0].Path)
			s.ItemStatus = matches[0].Status
			s.Authoritative = filepath.Join(itemDir, resultSidecarName(id))
			auth, err := os.ReadFile(s.Authoritative)
			if err == nil {
				s.AuthoritativeExists = true
				stray, err := os.ReadFile(strayPath)
				s.Differs = err != nil || !bytes.Equal(stray, auth)
			}
			strays = append(strays, s)
		}
	}
	return strays, nil
}
