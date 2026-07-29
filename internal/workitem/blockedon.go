package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// A `blocked-on-*` tag says A PERSON STILL OWES SOMETHING HERE. That is an
// obligation the item is the only record of, and archiving discards the record.
//
// The successor guard next door (successor.go) was built from mg-0418, which
// happened to be `type: design`, so its population is named by TYPE. mg-cf48 is
// the identical defect one type over: a `type: task`, `status: done` item
// carrying `blocked-on-daniel`, waiting on a ruling that has not landed —
// and `mg archive` took it silently, because the only tag it had ever been
// taught to read was `successor:` and the only type it fired on was `design`.
//
// That is the general form worth stating: A GUARD WHOSE POPULATION IS NAMED BY
// ONE ATTRIBUTE CANNOT SEE THE SAME DEFECT WEARING ANOTHER. `type: design`
// works as a trigger because a design's output IS a recommendation, so its
// build is undone by definition — a structural property needing no judgement.
// `type: task` has no such property; most done tasks really are complete, and
// a guard that fires on ordinary completions is a guard that gets switched off.
// Type cannot carry this. A tag can, and this one already exists: it is a live
// convention, and `mg list --tag=blocked-on-daniel` with no `--status` already
// spans every status and finds done items. Verified. So the index existed and
// the query existed; only the DESTRUCTIVE operation ignored them. This closes
// that, and nothing wider — the tag is the assertion, and mg does not have to
// infer anything from a body to read it.

// blockedOnTagPrefix namespaces the live "waiting on a person" convention:
// blocked-on-daniel, blocked-on-daniel-confirm, blocked-on-<whoever>.
const blockedOnTagPrefix = "blocked-on-"

// BlockedOnTags returns the blocked-on-* tags the item carries, in tag order,
// spelled exactly as stored — the refusal has to be able to quote the tag back,
// since "which one?" is the operator's first question and `--rm-tags` needs the
// literal string.
//
// Matching is a case-folded prefix and deliberately does NOT require a non-empty
// suffix. Both choices point the same way: this predicate only ever causes a
// REFUSAL, so folding case or accepting a bare `blocked-on-` can at worst refuse
// an archive that a human can complete in one more command, while the opposite
// error is silent and permanent. A false pass is the direction that matters.
func BlockedOnTags(item *Item) []string {
	var tags []string
	for _, t := range item.Tags {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), blockedOnTagPrefix) {
			tags = append(tags, t)
		}
	}
	return tags
}

// requireNotBlocked reports whether item may be archived: nil unless it carries
// a blocked-on-* tag.
//
// Unlike requireSuccessor this reads no store state and gates on no type. The
// tag is a claim the item makes about itself, it is true for as long as it is
// there, and removing it is the act of saying the obligation is discharged.
func requireNotBlocked(item *Item) error {
	tags := BlockedOnTags(item)
	if len(tags) == 0 {
		return nil
	}
	return errBlockedOn(item, tags)
}

// errBlockedOn is the refusal, and it NAMES THE TAG IT FOUND.
//
// Naming it is not decoration. "blocked" and "no successor" are two different
// states with two different remedies — remove a tag once a person has answered,
// versus file the item that tracks a recommendation — and an operator who gets
// an unnamed "archive refused" learns neither. The tag also names WHO, which is
// the only actionable fact in the refusal.
//
// Like errNoSuccessor, the hint does not mention --force. --force applies here
// (see ArchiveOpts.Force) but an agent hitting a refusal mid-cleanup, at speed,
// reaches for whatever the error message hands it; a guard whose own failure
// text teaches the bypass is decorative. The hint names the remedy that
// discharges the obligation instead of the one that discards it.
func errBlockedOn(item *Item, tags []string) *mgerr.Error {
	return mgerr.Conflict("blocked_on_tag",
		fmt.Sprintf("%s is tagged %s: archiving it would discard an item that is openly marked as still waiting on someone, and an archived item cannot be the tracker for outstanding work.",
			item.ID, strings.Join(tags, ", ")),
		fmt.Sprintf("Settle what the tag names, then run 'mg edit %s --rm-tags=%s' and archive it.",
			item.ID, strings.Join(tags, ",")))
}
