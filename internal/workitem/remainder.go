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
//
// EMISSION IS NOT ENFORCEMENT (mg-966d).
//
// The paragraph above was true of the SPELLING and false of everything else,
// because the declaration shipped as an OPT-IN boolean. The forgettable step did
// not go away; it moved, from "remember the tag string" to "remember the flag" —
// the second thing wearing the first thing's clothes. Measured over the live
// store on 2026-07-29, sixteen hours after the guard merged: ZERO items carried
// the marker, including mg-ee98 (the triage that died of exactly this defect) and
// every triage and design filed since by agents holding the ticket in context.
// The guard was live and could not fire.
//
// So mg picks the DEFAULT at creation, from the item's type and from a triage
// carrier block, and writes it into the item as an explicit, visible tag the
// filer can drop with `--no-declares-remainder`. This does NOT reintroduce the
// rejected `type == design` predicate, and the distinction is the whole of it:
//
//	ENFORCEMENT stays keyed on the declaration. requireRemainderDischarged
//	below reads the tag and never the type. Unchanged, and it must stay that way.
//
//	EMISSION uses the type to choose a default, which is then written down.
//
// A default is wrong in the safe direction: it is on the item in plain text, the
// filer can remove it before or after filing, and the item says what it claims.
// A type-keyed GUARD is wrong invisibly, at `mg done` time, on an item that never
// said anything — which is what mg-8970 rejected and what stays rejected.

// typesDeclaringRemainderByDefault names the item types whose output IS a
// recommendation by construction, so an item of that type declares a remainder
// unless the filer says otherwise.
//
// Membership is decided by one question: does completing this item leave the
// thing it produced UNDONE? A design recommends a build. A scoping produces a
// scope somebody else executes. An audit produces findings. An idea is a
// proposal. In each case the item's own completion is the moment its output
// becomes work nothing tracks.
//
// Deliberately absent: `task`, `bug`, `chore`, `doc`, `qa` — these DO the thing,
// so completing one discharges it. Also absent: `decision`, whose output binds
// rather than recommends. The set is kept narrow because every member is a
// default that a filer has to notice and remove when it is wrong, and the
// population that matters (1,876 of 1,955 items on 2026-07-29) is `task`.
//
// A `type: task` TRIAGE is not reachable from the type at all — that was
// mg-ee98's escape, and it is caught by the carrier block instead; see
// BodyDeclaresRemainderByDefault.
var typesDeclaringRemainderByDefault = map[string]bool{
	"design":  true,
	"scoping": true,
	"audit":   true,
	"idea":    true,
}

// TypeDeclaresRemainderByDefault reports whether `mg new --type=<itemType>`
// should emit the declaration when the filer passes neither
// --declares-remainder nor --no-declares-remainder.
//
// Matching is case-folded and trimmed to match how the type is compared
// everywhere else, and because a default that misses on " Design" would be a
// silent no-op rather than an error.
func TypeDeclaresRemainderByDefault(itemType string) bool {
	return typesDeclaringRemainderByDefault[strings.ToLower(strings.TrimSpace(itemType))]
}

// BodyDeclaresRemainderByDefault reports whether the body's LEADING CARRIER
// BLOCK marks this item as a triage, whose output is a verdict and nothing else.
//
// This is the mg-ee98 shape and the reason the type alone is not enough: that
// item was `type: task`, its verdict was IMPLEMENT on a reproduced data-loss
// mechanism, and nothing carried the fix. Triage is not a type; it is a position
// in the gh-issue workflow, declared by the filer in the body's opening block.
//
// Reading `stage:` HERE, at creation, is not the stage-keyed predicate mg-8970
// rejected. That one asked "is this stage non-terminal?" of an item at `mg done`
// time and over-fired on 41 items whose stage meant a PAUSE. This one asks
// whether the filer wrote `stage: triage` in the block they are filing right
// now, and the answer only picks a default that lands in the item as text. A
// triage that turns out to owe nothing drops the tag; a pause is never filed as
// a triage in the first place.
func BodyDeclaresRemainderByDefault(body string) bool {
	return strings.EqualFold(leadingCarrierValue(body, "stage"), "triage")
}

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
// The hint offers `--successor <id>`. Retracting the declaration
// (`mg edit <id> --rm-tags=declares-remainder`) is the correct move when a
// triage concludes that nothing is owed after all, and it is documented in
// `mg done --help` — where it is read deliberately. It is deliberately ABSENT
// here for the reason errNoSuccessor gives: an agent that hits a refusal at
// speed reaches for whatever the message hands it, and a guard whose own
// failure text teaches the retraction is decorative.
//
// IT ALSO NAMES THE WAY OUT THAT IS NOT A WAY OUT (mg-ed7b), and the two are
// not the same kind of sentence. On the gh-issue track this refusal fires at a
// moment when NO id can legally satisfy it: the successor build ticket is not
// filed until after the human gate, so the agent standing here has finished its
// work, cannot complete, and cannot name anything. Offering it only `--successor`
// leaves it to improvise a hold, and what it improvises is holding the CLAIM —
// which says someone took the item and says nothing about why, and which a
// sweeper collecting claims from dead agents released five times over on
// 2026-08-07. The pressure is the same shape mg-9259 found in this very guard:
// a refusal that names no reachable move buys a fabricated one.
//
// Handing the item to whoever it waits on does not discharge anything. The item
// stays open, keeps its declaration, and trips this guard again at the next
// `mg done` — so unlike the retraction, naming it cannot make the guard
// decorative. What it changes is where the item waits, and whether the waiting
// is legible to anything but the protocol that invented it.
func errRemainderWithoutSuccessor(item *Item) *mgerr.Error {
	return mgerr.Conflict("remainder_without_successor",
		fmt.Sprintf("%s declares a remainder (tag %q) and names no successor: completing it would discard the work it recommends, since a completed item cannot be the tracker for undone work.", item.ID, DeclaresRemainderTag),
		fmt.Sprintf("File the item that carries the recommendation forward, then run 'mg done %s --successor <id>'. If the successor cannot exist yet because this item is waiting on someone, do not hold the claim to hold the item — a claim carries no reason and a sweeper cannot tell it from an abandoned one. Say who it waits on and release it: 'mg unclaim %s --assignee=human'.", item.ID, item.ID))
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
