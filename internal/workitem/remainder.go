package workitem

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// THE RULE, stated once and not proxied:
//
//	an item whose output is a RECOMMENDATION must have a successor carrying it
//	forward, or completing it discards the recommendation.
//
// Three people wrote three different predicates for that rule and each caught
// the others' and not their own:
//
//	type == design            <- a proxy. Triage is not a type: mg-ee98 was a
//	                             `type: task` gh-issue triage whose verdict was
//	                             IMPLEMENT on a reproduced data-loss mechanism,
//	                             and nothing carried the fix. requireSuccessor
//	                             never fired. A human noticed the ABSENCE and
//	                             filed the build (mg-dd92) by hand.
//	stage is non-terminal     <- also a proxy, and it OVER-FIRES. A stage-shaped
//	                             pause owes nothing; "waiting on someone else"
//	                             and "deliberately set aside" are indistinguish-
//	                             able to a predicate that only asks whether a
//	                             stage is an end state.
//
// Measured before writing this, over the whole store (1,955 items, 2026-07-29):
// 55 items carry a `stage:` carrier line — triage 8, gated 12, build 4,
// review 17, merge 14 — and ZERO of them carry a `successor:` tag. A
// stage-keyed predicate treating anything but `merge` as non-terminal would
// therefore have fired on 41 items, 34 of them already archived. That number is
// the argument: it is not an escape count, it is an OVER-FIRE count, because
// most of those items are mid-workflow positions whose remainder is in flight
// rather than owed as a new ticket. mg self-installs on merge, so a guard that
// blocks routine completions at that volume gets removed by whoever it
// inconveniences — the failure mg-3412 recorded.
//
// So a fourth proxy is not the answer. The property is "THIS ITEM DECLARES A
// REMAINDER", and the fix is to make the item DECLARE it and have the guard
// fire on the declaration:
//
//   - An item that never declares never trips the guard. No inference, no
//     population guessing, no stage vocabulary to keep in sync.
//   - A snooze (mg-0a5f) is a single `snooze:` frontmatter timestamp on a
//     pending/ item. It declares nothing here, so the paused-vs-owes-a-
//     remainder discrimination that sank the stage predicate does not arise:
//     there is nothing for this guard to read on a snoozed item.
//   - It fails in the SAFE direction. A triage that forgets to declare
//     completes exactly as it does today. Worst case is the status quo, never a
//     blocked queue.
//
// The declaration is emitted by mg itself — `mg new --declares-remainder` — and
// not by a prompt template. Partly because `internal/agent/prompts/**` is a
// red line whose hand-authorisation queue stood at four items on 2026-07-29,
// but mostly because a declaration THE TOOL WRITES CANNOT BE FORGOTTEN in the
// way a convention a template merely asks for can.

// DeclaresRemainderTag is the declaration itself. It is an ordinary tag, so
// `mg list --tag=declares-remainder` finds every outstanding one across every
// status, `mg show` displays it, and `mg edit --add-tags/--rm-tags` reaches it
// on an item that already exists — none of which needs an item-schema change.
//
// It is the same carrier the two live guards next door use (successor:,
// blocked-on-*), which is not a coincidence: a tag is the only attribute mg
// already indexes, renders, and lets an operator retract.
// It is exported so `mg new --declares-remainder` writes the one canonical
// spelling instead of a second copy of the string living in cmd/mg.
const DeclaresRemainderTag = "declares-remainder"

// DeclaresRemainder reports whether the item declares that its output is a
// recommendation which something else must carry forward.
//
// Matching is case-folded and space-trimmed for the same reason BlockedOnTags
// is: this predicate only ever causes a REFUSAL, so being generous can at worst
// refuse a completion a human finishes with one more flag, while being stingy
// is silent and permanent.
func DeclaresRemainder(item *Item) bool {
	for _, t := range item.Tags {
		if strings.EqualFold(strings.TrimSpace(t), DeclaresRemainderTag) {
			return true
		}
	}
	return false
}

// requireRemainderDischarged reports whether item may be completed: nil unless
// it declares a remainder and names no successor that still exists.
//
// The successor pointer is re-resolved rather than trusted from when it was
// written, exactly as requireSuccessor does: the guard's promise is about the
// store NOW, and a tag pointing at a deleted item tracks nothing.
func requireRemainderDischarged(root string, item *Item) error {
	if !DeclaresRemainder(item) {
		return nil
	}

	ids := SuccessorIDs(item)
	if len(ids) == 0 {
		return errRemainderWithoutSuccessor(item)
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

	return errRemainderDanglingSuccessor(item, ids)
}

// errRemainderWithoutSuccessor is the refusal this file exists for. It names
// the ITEM and the REASON, because an agent that hits it is mid-protocol and
// the two questions it will ask are "which one?" and "why now?".
//
// The hint offers `--successor <id>` and nothing else. Retracting the
// declaration (`mg edit <id> --rm-tags=declares-remainder`) is the correct move
// when a triage concludes that nothing is owed after all, and it is documented
// in `mg done --help` — where it is read deliberately. It is deliberately
// ABSENT here for the reason errNoSuccessor gives: an agent that hits a refusal
// at speed reaches for whatever the message hands it, and a guard whose own
// failure text teaches the retraction is decorative.
func errRemainderWithoutSuccessor(item *Item) *mgerr.Error {
	return mgerr.Conflict("remainder_without_successor",
		fmt.Sprintf("%s declares a remainder (tag %q) and names no successor: completing it would discard the work it recommends, since a completed item cannot be the tracker for undone work.", item.ID, DeclaresRemainderTag),
		fmt.Sprintf("File the item that carries the recommendation forward, then run 'mg done %s --successor <id>'.", item.ID))
}

// errRemainderDanglingSuccessor is kept distinct from the bare refusal above
// for the reason errDanglingSuccessor is kept distinct from errNoSuccessor:
// "you pointed at a deleted item" and "you pointed at nothing" need different
// fixes, and collapsing them hides the fact that a link the operator believed
// was in place has rotted.
func errRemainderDanglingSuccessor(item *Item, ids []string) *mgerr.Error {
	return mgerr.Conflict("remainder_dangling_successor",
		fmt.Sprintf("%s declares a remainder and names successor %s, which no longer exists: nothing is tracking what it recommends.", item.ID, strings.Join(ids, ", ")),
		fmt.Sprintf("File the item that carries the recommendation forward, then run 'mg done %s --successor <id>'.", item.ID))
}
