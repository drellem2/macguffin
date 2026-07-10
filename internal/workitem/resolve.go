package workitem

import (
	"fmt"
	"io"
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
//
// Ambiguity, though, is only ambiguity among candidates of the same
// TERMINALITY. A live work item and a historical record are not two equally
// plausible answers to "what is mg-4fa7": one is work, one is an archive entry.
// Refusing to answer there stopped `mg show`/`mg claim` on five live items
// whose IDs happened to shadow an archived twin. So ResolveUnique prefers the
// single live candidate — and says so on stderr, because the bug this file was
// written to fix was silence, not shadowing.

// activeStates are the non-archive lifecycle directories under work/, in the
// order they are scanned. The order is only observable in the candidate list of
// an ambiguity error — a healthy store has exactly one match.
var activeStates = []string{"available", "claimed", "done", "pending", "shelved"}

// liveStates are the statuses of a work item that is still part of the
// workflow: something an agent can still claim, finish, or unshelve. "done" and
// "archived" are terminal — historical records, not work. Precedence between
// live and terminal is what makes a shadowed ID answerable at all.
var liveStates = map[string]bool{
	"available": true,
	"claimed":   true,
	"pending":   true,
	"shelved":   true,
}

// shadowNotice is where ResolveUnique reports a shadowed twin. It is STDERR and
// must stay STDERR: `mg show --json` and `mg list --json` put a machine-readable
// contract on stdout, and pogo parses it. A note on stdout corrupts them.
// Tests swap this out.
var shadowNotice io.Writer = os.Stderr

// Match is one filesystem hit for a work-item ID.
type Match struct {
	ID        string // the short ID that was resolved
	Path      string // absolute path to the .md file
	Status    string // available | claimed | done | pending | shelved | archived
	Partition string // archive partition (e.g. "2026-07"); empty unless Status=="archived"
}

// Live reports whether the match is a live work item rather than a terminal
// record (done or archived).
func (m Match) Live() bool { return liveStates[m.Status] }

// matchesID reports whether a directory entry names the work item id. Claimed
// items carry a PID suffix (<id>.md.<pid>), so both forms count. Sidecars
// (<id>.result.json) deliberately do not.
func matchesID(name, id string) bool {
	return name == id+".md" || strings.HasPrefix(name, id+".md.")
}

// matchesArchivedID reports whether a loose entry sitting directly under
// work/archive/ names the given archived item. Archived records never carry the
// claimed/ PID suffix (<id>.md.<pid>), so the match is STRICT — only "<id>.md"
// counts. That strictness is the point of scanning loose files at all: it keeps
// a total scan from ingesting editor backups (<id>.md.bak, <id>.md~) or result
// sidecars (<id>.result.json) as phantom archive twins.
func matchesArchivedID(name, id string) bool {
	return name == id+".md"
}

// Resolve returns every file in the store that names the given ID, across the
// active lifecycle directories, every archive partition, AND loose archived
// records sitting directly under work/archive/. A healthy store yields 0 or 1
// matches; 2+ means the short ID is ambiguous.
//
// It is TOTAL over the store on purpose: Create mints through Resolve, so a
// record the resolver cannot see is a record a new mint cannot collide with —
// which is exactly how an item gets born a silent alias of a twin. See Create.
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

	// Archive records live either in a month partition (archive/YYYY-MM/) or
	// loose directly under work/archive/. Both are scanned so Resolve stays total
	// over the store: a loose file — left by a future partition scheme, an
	// interrupted archive, or a restored backup — must be visible, or the next
	// mint that draws its ID slips past Create's collision check and is born a
	// silent alias the ambiguity guard can never surface (Resolve can't see the
	// twin). Partition entries keep the claimed/ PID-suffix tolerance of
	// matchesID; loose entries match strictly (see matchesArchivedID). ReadDir
	// returns names in ascending order, so partitions resolve oldest-first.
	archiveRoot := filepath.Join(root, "work", "archive")
	if entries, err := os.ReadDir(archiveRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				// A loose archived record: no partition, strict name match.
				if matchesArchivedID(e.Name(), id) {
					matches = append(matches, Match{
						ID:     id,
						Path:   filepath.Join(archiveRoot, e.Name()),
						Status: "archived",
					})
				}
				continue
			}
			dir := filepath.Join(archiveRoot, e.Name())
			partEntries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, pe := range partEntries {
				if matchesID(pe.Name(), id) {
					matches = append(matches, Match{
						ID:        id,
						Path:      filepath.Join(dir, pe.Name()),
						Status:    "archived",
						Partition: e.Name(),
					})
				}
			}
		}
	}

	return matches, nil
}

// ResolveUnique resolves an ID to exactly one item. It never guesses between
// candidates that are equally plausible answers — but liveness breaks the tie:
//
//	0 candidates                  -> not_found
//	1 candidate                   -> it
//	exactly 1 LIVE candidate      -> that one, plus a stderr note naming the
//	                                 terminal twin(s) it shadows
//	2+ live candidates            -> ambiguous_id, naming every candidate
//	0 live and 2+ terminal ones   -> ambiguous_id, naming every candidate
//
// The stderr note is the point: an auditor chasing an ID out of an old commit
// trailer gets the live item AND the exact path of the archived record, which
// is strictly more than either the old silent wrong answer or a bare error.
//
// # Partition qualifier
//
// When two archived twins of one ID live in different month partitions, both
// are terminal, so liveness cannot break the tie — the bare ID is unresolvable
// on purpose. The escape hatch is a partition qualifier: "mg-4fa7@2026-04"
// resolves the id "mg-4fa7" but keeps only the match stored in partition
// "2026-04" before the uniqueness check runs. It is a PREDICATE on the match
// set Resolve already returns, not a second walk: split the input on '@', and
// when a partition is named, filter matches to it, then apply the ordinary
// count-based rules above to what survives. A qualifier that names a partition
// holding no twin is not_found (errNoSuchPartition) — never a silent wrong
// answer — and an empty qualifier ("mg-4fa7@") is a usage error. Only archived
// records carry a partition, so a live item is never selectable by qualifier.
func ResolveUnique(root, id string) (Match, error) {
	bareID, partition, qualified := strings.Cut(id, "@")
	if qualified && partition == "" {
		return Match{}, errEmptyPartition(id)
	}

	matches, err := Resolve(root, bareID)
	if err != nil {
		return Match{}, err
	}

	if qualified {
		var filtered []Match
		for _, m := range matches {
			if m.Partition == partition {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) == 0 {
			if len(matches) == 0 {
				return Match{}, errNoSuchItem(bareID)
			}
			return Match{}, errNoSuchPartition(bareID, partition, matches)
		}
		matches = filtered
	}

	switch len(matches) {
	case 0:
		return Match{}, errNoSuchItem(bareID)
	case 1:
		return matches[0], nil
	}

	var live []Match
	for _, m := range matches {
		if m.Live() {
			live = append(live, m)
		}
	}
	if len(live) != 1 {
		// Two live items are a genuine ambiguity; so are two archived ones.
		// Both cases hand the operator every candidate and refuse to pick.
		return Match{}, errAmbiguousID(root, bareID, matches)
	}

	warnShadowed(root, bareID, live[0], matches)
	return live[0], nil
}

// warnShadowed prints one stderr line per terminal record that shares the
// resolved ID. Resolve, but never silently.
func warnShadowed(root, id string, chosen Match, matches []Match) {
	if shadowNotice == nil {
		return
	}
	for _, m := range matches {
		if m.Path == chosen.Path {
			continue
		}
		fmt.Fprintf(shadowNotice, "note: %s also names %s item at %s\n",
			id, articled(m.Status), relPath(root, m.Path))
	}
}

// articled renders a status with its indefinite article ("an archived", "a done").
func articled(status string) string {
	if strings.ContainsRune("aeiou", rune(status[0])) {
		return "an " + status
	}
	return "a " + status
}

// relPath renders a store path relative to the store root, falling back to the
// absolute path. Operators navigate by the store-relative form.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
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
	var firstPart string
	for _, m := range matches {
		fmt.Fprintf(&b, "\n  %s (%s)", relPath(root, m.Path), m.Status)
		if firstPart == "" && m.Partition != "" {
			firstPart = m.Partition
		}
	}
	hint := "Short IDs are 4 hex digits and can collide. Inspect the files above directly; mg will not guess between them."
	// When the collision is between archived twins in different partitions,
	// name the escape hatch at the moment it is needed: a partition qualifier
	// picks one twin. (mg-0d0c: @partition is discoverable from the error.)
	if firstPart != "" {
		hint = fmt.Sprintf("%s Disambiguate an archived twin by its partition, e.g. %s@%s.", hint, id, firstPart)
	}
	return mgerr.Conflict("ambiguous_id", b.String(), hint)
}

// errEmptyPartition rejects a qualifier with nothing after the '@'
// ("mg-4fa7@"). It is a usage error (exit 2): the caller asked to filter by a
// partition but named none. Reported rather than silently treated as the bare
// id so a shell-truncated qualifier does not resolve to a surprising item.
func errEmptyPartition(id string) *mgerr.Error {
	return mgerr.Usage("empty_partition",
		fmt.Sprintf("%s: empty partition after '@'.", id),
		"Name a partition, e.g. mg-4fa7@2026-04, or drop the '@' to resolve the bare id.")
}

// errNoSuchPartition reports that a partition-qualified lookup named a partition
// that holds no twin of the id — even though the id exists elsewhere. It is
// not_found (exit 3): the qualified entity does not exist. It lists the
// partitions where the id IS archived so the operator can fix the qualifier,
// and it is deliberately distinct from errNoSuchItem so "<id>@2099-99" (a real
// id, wrong partition) is never confused with a wholly unknown id — and, above
// all, never silently returns the wrong twin.
func errNoSuchPartition(id, partition string, matches []Match) *mgerr.Error {
	var parts []string
	seen := map[string]bool{}
	for _, m := range matches {
		if m.Partition != "" && !seen[m.Partition] {
			seen[m.Partition] = true
			parts = append(parts, m.Partition)
		}
	}
	msg := fmt.Sprintf("%s@%s: no work item %s in archive partition %s.", id, partition, id, partition)
	var hint string
	if len(parts) > 0 {
		hint = fmt.Sprintf("%s is archived in: %s. Qualify with one of those partitions.", id, strings.Join(parts, ", "))
	} else {
		hint = fmt.Sprintf("%s has no archived twin to qualify; drop the @%s.", id, partition)
	}
	return mgerr.NotFound("no_such_partition", msg, hint)
}
