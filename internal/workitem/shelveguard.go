package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// SHELVE WAS THE ONE STOP WITH NO GUARD AT ALL (mg-2cf0).
//
// Three operations take a work item out of the queue for good: `mg done`,
// `mg archive`, `mg shelve`. `mg archive` carries two guards plus a recorded
// override (blockedon.go, successor.go, ArchiveOpts.Force). `mg done` carries
// one (remainder.go). `mg shelve` carried NONE — no declaration check, no
// blocked-on check, no successor requirement, no override — and it is the
// cheapest of the three to reach: one command, no claim, no status precondition
// beyond "not already gone".
//
// It is also the only one of the three that MOVES OTHER PEOPLE'S ITEMS. Shelving
// a target recursively shelves every open item that depends on it, so shelving a
// target hides every pre-filed audit, follow-up and remainder aimed AT that
// target. pogo's mg-2530 names the hazard exactly: "shelved is a pair somebody
// dropped, and if shelving counted then abandoning an audit would discharge the
// obligation it exists to create." mg-2530 stopped `shelved/` counting for the
// dispatch scan; nothing stopped shelve from CREATING that state.
//
// Measured on the live shelf on 2026-07-30 (181 items):
//
//	32 of 175   were shelved as a dependent — nobody named them
//	 0 of 181   carry a successor: tag
//	 0 of 181   have an open dependent — not health, but a consequence of the
//	            cascade, which cannot leave one by construction
//
// The predicate below matches 3 of those 181: mg-e925 (blocked-on-daniel),
// mg-a08c (design + declares-remainder), mg-a661 (a `stage: triage` body).
// 1.7%, no false positives among them — an unbuilt design that nothing tracks
// IS the failure — and it is not an already-met bar, since none of the 181
// names a successor.
//
// WHAT IS PORTED AND WHAT IS NOT. The PREDICATES are shared with archive and
// done, deliberately: BlockedOnTags, DeclaresRemainder,
// TypeDeclaresRemainderByDefault, BodyDeclaresRemainderByDefault and
// SuccessorIDs all live next door and are read from here rather than copied. A
// second copy of a predicate is a second thing to keep in sync, and the whole
// defect being fixed is one operation reading an index that two others already
// read. The PROSE is not shared: a refusal that tells an operator to archive
// something when they asked to shelve it names the wrong remedy.
//
// The guard fires on the item the operator NAMED and on nothing else. It is
// deliberately not applied to the cascade — see Shelve. A cascade gate would
// refuse a shelve on the strength of an item the operator never mentioned, and
// the cascade's fix is a FIELD (the dependents list, now on work.shelve), not a
// second gate.

// ShelveOption configures a shelve.
type ShelveOption func(*shelveOpts)

type shelveOpts struct {
	// override is the operator's recorded reason for shelving an item a guard
	// refused. Empty means no override.
	override string
}

// WithShelveOverride records WHY the operator is shelving an item a guard
// refused, and permits the shelve.
//
// IT IS A STRING, NOT A BOOLEAN, and that is the whole design. A bare --force
// records that somebody overrode the gate and loses the only thing a later
// reader needs, which is WHAT THEY KNEW THAT THE GATE DID NOT. `work.archive_forced`
// records a guard code with no reason and pogo's mg-2530 `--pairing-override`
// takes a string for this reason; this follows the string.
//
// The reason is trimmed, and whitespace is not a reason: `--override="   "` is
// an empty override and the guard still refuses. An override that can be
// satisfied by a space bar is a boolean wearing a string's clothes.
func WithShelveOverride(reason string) ShelveOption {
	return func(o *shelveOpts) { o.override = strings.TrimSpace(reason) }
}

func newShelveOpts(opts []ShelveOption) shelveOpts {
	var o shelveOpts
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// checkShelveGuards runs every guard that can refuse a shelve and returns the
// first refusal, or nil.
//
// The blocked-on tag is checked FIRST, for the reason checkArchiveGuards checks
// it first: either refusal is correct when both fire, so the order decides which
// one the operator hears, and "a named person still owes something on this item"
// is the fact no other query surfaces at this moment. It is also the cheaper
// check — it reads no store state and gates on no type.
func checkShelveGuards(root string, item *Item) error {
	if err := requireNotBlockedForShelve(item); err != nil {
		return err
	}
	return requireShelveSuccessor(root, item)
}

// requireNotBlockedForShelve is the blocked-on guard (blockedon.go), ported.
//
// It shares the PREDICATE — BlockedOnTags, the same case-folded prefix match
// over the same live `blocked-on-<who>` convention — and supplies its own
// refusal, because the remedy differs by operation and a refusal that names the
// wrong one is worse than a terse one.
//
// A successor: tag does NOT answer this arm, exactly as it does not answer it
// at archive time (see CheckArchivable). Naming a tracker says nothing about
// whether a person still owes something here; treating it as an answer would
// make one tag discharge two unrelated obligations.
func requireNotBlockedForShelve(item *Item) error {
	tags := BlockedOnTags(item)
	if len(tags) == 0 {
		return nil
	}
	return mgerr.Conflict("shelve_blocked_on_tag",
		fmt.Sprintf("%s is tagged %s: shelving it would hide an item that is openly marked as still waiting on someone, and a shelved item cannot be the tracker for outstanding work.",
			item.ID, strings.Join(tags, ", ")),
		fmt.Sprintf("Settle what the tag names, then run 'mg edit %s --rm-tags=%s' and shelve it.",
			item.ID, strings.Join(tags, ",")))
}

// shelveDeclaresRemainder reports whether the item's output is a
// RECOMMENDATION — something completing or hiding the item leaves undone.
//
// It is the union of the three signals mg already uses, and it reads all three
// on purpose. `mg done`'s guard (requireRemainderDischarged) fires ONLY on the
// explicit tag, which is right there: mg-8970 rejected a type-keyed guard at
// done time because it fires on ordinary completions, and mg-966d made the tag
// the thing `mg new` writes by default so the declaration is never forgotten.
//
// But `mg new` only started writing that tag on 2026-07-29. Every item filed
// before it declares nothing, which is the entire 181-item shelf, so a
// tag-only predicate at shelve time would fire on 1 of 181 — mg-a08c, the
// deliberate control filed to prove the emission works. The type and the triage
// carrier block are the SAME defaults `mg new` computes (see remainder.go); read
// here they say "this item would have declared a remainder had it been filed
// today", which is the property that matters and the only one available on a
// pre-mg-966d item.
//
// This does NOT reintroduce the rejected type-keyed guard at `mg done`:
// requireRemainderDischarged is untouched, still reads the tag and nothing else,
// and this function is called from nowhere but shelve. The two differ because
// the operations differ — `mg done` is the end of work that WAS done, `mg shelve`
// is the abandonment of work that was not, and abandoning a recommendation is
// the case this exists for.
//
// KNOWN COST, stated rather than hidden: an item filed
// `--no-declares-remainder` on a type in the default set is still refused here,
// because the opt-out leaves no marker to read — absence of the tag on a design
// means "filed before mg-966d" and "opted out" indistinguishably. That refusal
// is recoverable in one command with a recorded reason; the opposite error is
// silent and permanent.
func shelveDeclaresRemainder(item *Item) bool {
	return DeclaresRemainder(item) ||
		TypeDeclaresRemainderByDefault(item.Type) ||
		BodyDeclaresRemainderByDefault(item.Body)
}

// shelveRemainderSource names WHICH signal fired, so the refusal can say why
// this item is held to a successor. "Your design has no successor" and "your
// triage has no successor" are answered the same way but found differently, and
// an operator who cannot see which arm fired cannot tell a correct refusal from
// a mis-typed item.
func shelveRemainderSource(item *Item) string {
	switch {
	case DeclaresRemainder(item):
		return fmt.Sprintf("carries the %q tag", DeclaresRemainderTag)
	case TypeDeclaresRemainderByDefault(item.Type):
		return fmt.Sprintf("is type %q, whose output is a recommendation", item.Type)
	case BodyDeclaresRemainderByDefault(item.Body):
		return "is a triage (its body's carrier block says stage: triage)"
	}
	return ""
}

// requireShelveSuccessor is the successor guard (successor.go), ported and
// widened from `type == design` to every way an item can say its output is a
// recommendation.
//
// The pointer is RE-RESOLVED rather than trusted from when it was written, for
// the reason requireSuccessor re-resolves it: the guard's promise is about the
// store NOW, and a tag naming a deleted item tracks nothing — exactly like no
// tag at all.
func requireShelveSuccessor(root string, item *Item) error {
	if !shelveDeclaresRemainder(item) {
		return nil
	}

	ids := SuccessorIDs(item)
	if len(ids) == 0 {
		return errShelveWithoutSuccessor(item)
	}

	for _, sid := range ids {
		matches, err := Resolve(root, sid)
		if err != nil {
			return err
		}
		if len(matches) > 0 {
			return nil
		}
	}

	return errShelveDanglingSuccessor(item, ids)
}

// errShelveWithoutSuccessor is the refusal this file exists for.
//
// The hint names the remedy that DISCHARGES the obligation — file the tracker
// and link it — and never --override. --override exists and is documented in
// `mg shelve --help`, where it is read deliberately; an agent that hits a
// refusal mid-cleanup at speed reaches for whatever the error message hands it,
// and a guard whose own failure text teaches the bypass is decorative. Same
// reasoning as errNoSuccessor and errBlockedOn next door.
func errShelveWithoutSuccessor(item *Item) *mgerr.Error {
	return mgerr.Conflict("shelve_without_successor",
		fmt.Sprintf("%s %s and names no successor: shelving it would hide the work it recommends, since a shelved item cannot be the tracker for undone work.",
			item.ID, shelveRemainderSource(item)),
		fmt.Sprintf("File the item that carries the recommendation forward, then run 'mg edit %s --add-tags=successor:<id>' and shelve it.", item.ID))
}

// errShelveDanglingSuccessor is kept distinct from the bare refusal for the
// reason errDanglingSuccessor is: "you pointed at a deleted item" and "you
// pointed at nothing" need different fixes, and collapsing them hides the fact
// that a link the operator believed was in place has rotted.
func errShelveDanglingSuccessor(item *Item, ids []string) *mgerr.Error {
	return mgerr.Conflict("shelve_dangling_successor",
		fmt.Sprintf("%s %s and names successor %s, which no longer exists: nothing is tracking what it recommends.",
			item.ID, shelveRemainderSource(item), strings.Join(ids, ", ")),
		fmt.Sprintf("File the item that carries the recommendation forward, then run 'mg edit %s --add-tags=successor:<id>' and shelve it.", item.ID))
}
