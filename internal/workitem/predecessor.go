package workitem

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
)

// WHY THE LINK IS WRITTEN ON BOTH ENDS (mg-3386).
//
// The ticket that produced this file reported that `mg done --successor` left
// NO durable trace, so an enforced declares-remainder gate and a bypassed one
// were indistinguishable. Measured against the live store before writing a
// line of it, HALF of that report was already false:
//
//	mg show mg-0e8c --json | jq .tags
//	  [... "declares-remainder", "successor:mg-28b6"]      <- the link is there
//	mg show mg-1006 --json | jq .tags
//	  [... "declares-remainder", "successor:mg-f32a"]      <- and there
//
// mg-9259 had shipped the forward link as a `successor:` tag, on both of the
// items the report cited, before the report was written. The reporter looked at
// `jq 'keys'` and at the result sidecar — the two places a person reasonably
// looks for a field — and the link was in neither, because it is a TAG VALUE
// and not a key. So the observation was right, the inference was wrong, and the
// thing that made a correct actor file a false bug about a working mechanism was
// not the absence of the trace. It was that the trace was unreadable from where
// anybody stood.
//
// That splits the remaining defect into two, and this file is the second:
//
//	VISIBILITY  the link must be findable by someone who greps for "successor"
//	            in the item's fields, not only by someone who already knows the
//	            tag convention. Fixed in cmd/mg (show.go, list.go): `successor`,
//	            `predecessor` and `declares_remainder` are first-class JSON
//	            fields, derived from the tags, and `mg show` prints a resolved
//	            Successor line naming the title and current status.
//
//	RECIPROCITY the successor names no predecessor, so the chain is walkable in
//	            one direction only. Given a closed item you can find its
//	            tracker; given the tracker you cannot find what it inherited.
//	            That is this file.
//
// THIS WRITES THE BACK-REFERENCE AND NEVER GATES ON IT. The distinction is the
// whole design, and it is the one mg-9259 paid for with a measurement:
//
//	require the successor to name this item back   0 of 40 links would pass.
//	                                               The back-reference has never
//	                                               once been written.
//
// A guard that refuses 40 out of 40 legitimate links is not a strict guard, it
// is a broken command, and mg self-installs on merge — remainder.go records
// what happens to a guard that blocks routine completions. Writing what has
// never been written is what makes the reciprocal link TRUE going forward.
// Reading it as a precondition would refuse every link that predates it. So
// `requireRemainderDischarged` and `requireSuccessor` are untouched by this
// file: they still read the forward tag and nothing else.
//
// FAILURE IS NON-FATAL AND LOUD. The back-link write touches a DIFFERENT item
// than the one being completed — one that may be claimed by another agent, or
// gone between the resolve and the write. A close that already satisfied its
// gate must not be turned into a refusal because a second file could not be
// updated; the forward link is the enforced half and it is already on disk.
//
// But a best-effort write that fails silently is this ticket's own defect one
// level down: a reciprocal link that is sometimes absent, with nothing saying
// which absences were failures, leaves a reader unable to tell an unwritten
// back-link from an unwalkable chain. So every failed back-link is reported
// TWICE, and the pair is the point:
//
//	a note on STDERR naming both ends and the reason, for the operator standing
//	there — stderr and not stdout, for the reason resolve.go's shadowNotice
//	gives: `mg show --json` and `mg list --json` put a parsed contract on stdout.
//
//	a work.backlink_failed EVENT, for the reader who arrives later. A note is
//	what mg-9259 already had and what mg-3386 was filed about: it is true when
//	printed and gone by the time anyone asks. `mg event list
//	--type=work.backlink_failed` is the durable half, and an empty result is the
//	statement that every one-directional chain in the store is one nothing ever
//	tried to close.

// predecessorTagPrefix namespaces the reverse half of the successor link.
//
// It is an ordinary tag for the same reason `successor:` is: tags are the only
// attribute mg already indexes, renders, filters (`mg list --tag=...`) and lets
// an operator retract (`mg edit --rm-tags=...`), so the reciprocal link needs no
// item-schema change and no migration for the 1,955 items that predate it.
const predecessorTagPrefix = "predecessor:"

// PredecessorTag renders the structured back-link tag for a predecessor id.
func PredecessorTag(id string) string { return predecessorTagPrefix + id }

// PredecessorIDs returns the ids named by the item's predecessor: tags, in tag
// order. It is the exact mirror of SuccessorIDs, including the trimming: a tag
// written by hand as "predecessor: mg-1234" names mg-1234, because a link that
// misses on one space is a link nobody can walk.
func PredecessorIDs(item *Item) []string {
	var ids []string
	for _, t := range item.Tags {
		rest, ok := strings.CutPrefix(t, predecessorTagPrefix)
		if !ok {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			ids = append(ids, rest)
		}
	}
	return ids
}

// DescribePredecessors resolves every predecessor: tag on item and reports what
// each one names, in tag order.
//
// It shares SuccessorRef with the forward direction deliberately: the shape of
// "a link, and what it resolved to right now" does not differ by direction, and
// a second identical struct would be one more thing to keep in step. The same
// display-helper contract holds — an unresolvable tag yields a ref with an
// empty Status rather than an error, because a pointer at nothing is precisely
// the thing a reader needs to see.
func DescribePredecessors(root string, item *Item) []SuccessorRef {
	return describeLinks(root, PredecessorIDs(item))
}

// describeLinks resolves a list of link ids to what they currently name. It is
// the shared body of DescribeSuccessors and DescribePredecessors.
func describeLinks(root string, ids []string) []SuccessorRef {
	refs := make([]SuccessorRef, 0, len(ids))
	for _, sid := range ids {
		ref := SuccessorRef{ID: sid}
		matches, err := Resolve(root, sid)
		if err == nil && len(matches) == 1 {
			ref.Status = matches[0].Status
			if s, err := readFile(matches[0].Path); err == nil {
				ref.Title = s.Title
			}
		}
		refs = append(refs, ref)
	}
	return refs
}

// backlinkNotice is where a failed or skipped reciprocal write reports itself.
// It is STDERR and must stay STDERR — see the file comment. Tests swap it out.
var backlinkNotice io.Writer = os.Stderr

// reconcileBacklinks writes a predecessor: tag onto every item that `item`
// names as a successor, so the link is walkable from either end.
//
// It reconciles ALL of the item's successor: tags rather than only the id this
// run supplied, because the tag has three routes onto an item — `--successor`,
// `mg edit --add-tags=successor:...`, and `mg new --tags=successor:...` — and a
// back-link that exists for one route and not the others is a chain that breaks
// depending on how it was filed. Which is the failure mode, not a variant of it.
//
// It NEVER returns an error, by construction. Every caller reaches it holding a
// completion or an archival that has already satisfied its gate, and there is no
// outcome here that should undo one. What it does instead is say so: each end it
// could not write is named on backlinkNotice with the reason.
//
// Already-correct links are silent and cost one read: re-running `mg done` after
// a refusal, or reconciling an item whose back-links were written by an earlier
// run, writes nothing and prints nothing.
func reconcileBacklinks(root string, item *Item) {
	for _, sid := range SuccessorIDs(item) {
		err := linkPredecessor(root, sid, item.ID)
		if err == nil {
			continue
		}

		fmt.Fprintf(backlinkNotice,
			"note: %s names successor %s, but the reverse link could not be recorded on %s: %s\n"+
				"      the forward link is on %s and is unaffected; 'mg edit %s --add-tags=%s' records the reverse by hand.\n",
			item.ID, sid, sid, err, item.ID, sid, PredecessorTag(item.ID))

		// AND DURABLY, not only on the terminal that happened to be attached.
		//
		// This is mg-3386's own lesson applied to mg-3386's fix. The ticket's
		// sharpest complaint was that `mg done --successor` printed a line and
		// recorded nothing, so the mechanism could not be audited after the
		// scrollback was gone. A best-effort reverse link whose only failure
		// record is a note on stderr would reproduce that exactly: a reader
		// finding a one-directional chain months later could not tell an
		// unwritten back-link from one that was attempted and failed, which is
		// the same "working and broken look identical" the ticket is about.
		//
		// So the failure is an event. `mg event list --type=work.backlink_failed`
		// answers "which reverse links did mg try and fail to write", and an
		// empty result means the one-directional chains in the store are ones
		// nothing ever attempted rather than ones that silently rotted.
		event.Emit(root, "work.backlink_failed", map[string]string{
			"item_id":   item.ID,
			"successor": sid,
			"reason":    err.Error(),
			"actor":     actor(),
		})
	}
}

// linkPredecessor writes a predecessor: tag naming predecessorID onto the item
// named by successorID, and persists it.
//
// The target is resolved with ResolveUnique rather than Resolve so an ambiguous
// id — an archived twin sharing a short id across partitions — writes NOTHING
// and says why. Guessing which twin inherited the remainder would put a false
// link in the store, and a false link is worse than a missing one: a missing
// link is visibly missing, while a wrong one answers the question confidently.
//
// The read-modify-write here is unsynchronised, exactly as linkSuccessor,
// unclaim and the snooze writers are. That is the store's existing concurrency
// posture (see UpdateWithBodyChange for why mg does not lock), and the exposure
// is one tag append on an item nobody is being asked to hold — but it IS an
// exposure, and naming it is cheaper than discovering it.
func linkPredecessor(root, successorID, predecessorID string) error {
	if successorID == predecessorID {
		// checkSuccessorTarget already refuses a self-successor on the write
		// path. This catches a self-link that reached the store some other way
		// and declines to make it reciprocal, rather than writing a tag that
		// says an item preceded itself.
		return fmt.Errorf("an item cannot be its own predecessor")
	}

	match, err := ResolveUnique(root, successorID)
	if err != nil {
		return err
	}

	target, err := readFile(match.Path)
	if err != nil {
		return err
	}

	tag := PredecessorTag(predecessorID)
	for _, t := range target.Tags {
		if t == tag {
			return nil
		}
	}
	target.Tags = append(target.Tags, tag)

	if err := os.WriteFile(match.Path, []byte(Render(target)), 0o644); err != nil {
		return fmt.Errorf("%s", fsErrText(err))
	}
	return nil
}
