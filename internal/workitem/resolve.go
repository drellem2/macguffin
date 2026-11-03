package workitem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
)

// This file is the ONE place that turns a short work-item ID into a file on
// disk. Before it existed, seven call sites (Read, FindPath, Status, Done,
// Unclaim, Claim, Reopen) each walked work/ independently and each took the
// first filesystem hit. None counted matches, so a duplicated short ID — IDs
// were 4 hex digits then, a 65,536-value space where the birthday bound bites
// hard, and every one of those IDs still resolves today — resolved
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

// Match is one stored record for a work-item ID.
type Match struct {
	ID string // the short ID that was resolved
	// Path is where the record is stored, for humans: the absolute .md path on
	// the filesystem backend (store.Doc.Where), "<status>/<id>" on a backend
	// with no better answer. It is a LABEL — nothing reads or writes through it.
	Path      string
	Status    string // available | claimed | done | pending | shelved | archived
	Partition string // archive partition (e.g. "2026-07"); empty unless Status=="archived"
	// Holder is who holds a claimed record, in the backend's terms (fsstore:
	// the claim PID). Empty for every other status.
	Holder string

	doc   *store.Doc // the record as Resolve read it
	local bool       // Path is a real file on a store.Local backend
}

// State is the match's store.State, the EXPECT side of a Transition out of it.
func (m Match) State() store.State {
	return store.State{Status: store.Status(m.Status), Holder: m.Holder}
}

func matchOf(d store.Doc) Match {
	return Match{
		ID:        d.ID,
		Path:      where(&d),
		Status:    string(d.State.Status),
		Partition: d.Partition,
		Holder:    d.State.Holder,
		doc:       &d,
	}
}

// Live reports whether the match is a live work item rather than a terminal
// record (done or archived).
func (m Match) Live() bool { return liveStates[m.Status] }

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
	// The walk itself is the backend's. On the filesystem it is GetAll over
	// fsstore.Locate — the same store-wide scan that backend's Create runs
	// before its O_EXCL, so one scan cannot disagree with itself about what
	// the store holds. A backend that cannot hold twins answers through Get.
	if id == "" {
		return nil, nil
	}
	b, err := be(root)
	if err != nil {
		return nil, err
	}
	var docs []store.Doc
	if tw, ok := b.(store.Twins); ok {
		docs, err = tw.GetAll(id)
		if err != nil {
			return nil, err
		}
	} else {
		d, err := b.Get(id)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			docs = []store.Doc{*d}
		}
	}
	_, local := localOf(b)
	matches := make([]Match, 0, len(docs))
	for _, d := range docs {
		m := matchOf(d)
		m.ID = id
		m.local = local
		matches = append(matches, m)
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
	return resolveUnique(root, id, true)
}

// resolveUnique is ResolveUnique; warn=false skips the shadow notice for a
// caller re-resolving an id the operator has already been told about.
func resolveUnique(root, id string, warn bool) (Match, error) {
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

	if warn {
		warnShadowed(root, bareID, live[0], matches)
	}
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
	item, m, err := ReadWithMatch(root, id)
	if err != nil {
		return nil, "", err
	}
	return item, m.Status, nil
}

// ReadWithMatch is ReadWithStatus plus the Match it resolved through, for
// callers that need the item's LOCATION and not just its status — reading the
// result sidecar beside it, above all (see SidecarOf).
//
// It exists so those callers do not resolve a second time. A second resolve is
// not merely wasted work: an @partition-qualified id resolves once and then
// carries a BARE id on the Item, which a re-resolve can find ambiguous, and a
// shadowed id prints its stderr note once per resolve.
func ReadWithMatch(root, id string) (*Item, Match, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, Match{}, err
	}
	item, err := readMatch(m)
	if err != nil {
		return nil, Match{}, err
	}
	return item, m, nil
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
	hint := "Short IDs are only 4 or 5 hex digits, and IDs minted before v0.3.0 could be duplicated. Inspect the files above directly; mg will not guess between them."
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

// AllIDs returns the id of every work item in the store — live AND terminal,
// including every archive partition — as a set.
//
// It exists because the same question asked per-name does not scale. Resolve
// walks the whole store for ONE id, which is right for one id and ruinous for a
// thousand: `mg mail list` enumerates 1361 mailboxes and asks of each whether a
// work item vouches for the name, and 1361 total walks of a 4118-file archive
// is seconds of work to answer a question one walk answers. Callers testing
// many names build this once; callers testing a single name should keep using
// Resolve, which is cheaper for that case and cannot go stale.
//
// Terminal ids are included, unlike LiveIDs. The two feed different questions:
// LiveIDs answers "who might the sender have MEANT", where a months-retired id
// is a wrong guess, while this answers "is this name a real work item at all",
// where a finished item still explains a mailbox named after it.
//
// Unreadable directories are skipped rather than failing, matching Resolve: a
// missing work/shelved/ must not turn every mailbox into an unvouched one.
func AllIDs(root string) map[string]bool {
	ids := map[string]bool{}
	for _, status := range append(append([]string{}, activeStates...), "archived") {
		got, err := idsIn(root, status)
		if err != nil {
			continue
		}
		for _, id := range got {
			ids[id] = true
		}
	}
	return ids
}

// idsIn returns the ids stored in status, through the backend's cheap id
// enumeration when it has one (store.IDLister: names only, no documents read)
// and through List otherwise. Archived covers every partition AND loose
// records directly under the archive, matching Resolve, so a bulk answer
// cannot disagree with a single-name one. The filesystem backend's names are
// matched exactly as Resolve matches them — "<id>.md" and the claimed
// "<id>.md.<pid>", never a sidecar or an editor's "<id>.md.bak" left beside
// the archive.
func idsIn(root, status string) ([]string, error) {
	b, err := be(root)
	if err != nil {
		return nil, err
	}
	if l, ok := b.(store.IDLister); ok {
		return l.IDs(store.Status(status))
	}
	docs, err := b.List(store.Status(status))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.ID)
	}
	return out, nil
}

// idFromEntry recovers the work-item id a directory entry names, or "" if it
// names no item. Only "<id>.md" and the claimed form "<id>.md.<pid>" count;
// sidecars ("<id>.result.json") and editor leavings ("<id>.md.bak") do not.
func idFromEntry(name string) string {
	idx := strings.Index(name, ".md")
	if idx <= 0 {
		return ""
	}
	rest := name[idx:]
	if rest != ".md" && !strings.HasPrefix(rest, ".md.") {
		return ""
	}
	return name[:idx]
}

// LiveIDs returns the ids of every LIVE work item in the store (available,
// claimed, pending, shelved), sorted. Terminal records — done and archived —
// are excluded on purpose: this feeds "did you mean ...?" for a mistyped
// mailbox name, and the name a sender meant to type is an agent still working,
// not an id retired months ago.
//
// It reads directory entries only, never file contents: the id is the file name
// up to ".md" (claimed items carry a "<id>.md.<pid>" suffix), so parsing every
// item to recover an id already spelled on disk would make a suggestion cost a
// full store walk. Unreadable state directories are skipped rather than
// failing — a suggestion is a courtesy, and half of one beats an error.
func LiveIDs(root string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, state := range activeStates {
		if !liveStates[state] {
			continue
		}
		got, err := idsIn(root, state)
		if err != nil {
			continue
		}
		for _, id := range got {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}
