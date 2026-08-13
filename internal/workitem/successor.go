package workitem

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// A design item's OUTPUT IS A RECOMMENDATION. That is a fact about its TYPE,
// not about its wording: at the moment a design is done, the thing it
// recommends is undone by construction. Archiving it therefore hides an
// outstanding piece of work unless something else is tracking that work.
//
// mg-ab67 established the rule — an archived ticket cannot be the tracker for
// undone work. Forty minutes later the agent that had just written that rule
// down archived mg-2da4, a completed design whose build had no ticket, while
// holding the rule in hand. That agent then AUDITED its own archiving and
// reported it clean, because the audit grepped bodies for "STILL IN SCOPE" and
// a design ticket recommending a build almost never says those words. The
// instrument was sound; the predicate was lifted from one instance's surface
// form and inherited that instance's accidents. Knowing the rule is not a
// mechanism, and neither is a grep for how the rule once happened to be phrased.
//
// So the TRIGGER here is `type == design` — structural, no wording involved —
// AND SO IS THE SATISFACTION CONDITION. This deliberately does NOT scan bodies
// or any free text for mentions of the id. A search cannot distinguish a thing
// from talk about the thing: a body reading "unlike mg-2da4, this one..."
// mentions the id and tracks nothing. That is a FALSE PASS, and a false pass is
// the direction that matters — it is silent, permanent, and precisely the
// failure this guard exists to prevent. The link is an explicit structured
// pointer or it does not count.
//
// The pointer is a `successor:<id>` tag on the design item itself, written by
// `mg archive <id> --successor <build-id>`. Keeping it ON the design means the
// archived record names its own tracker for any later reader, and checking it
// is a single-file read rather than a store-wide search.

// successorTagPrefix namespaces the structured design→successor link in tags.
// It is an ordinary tag, so `mg edit --add-tags=successor:mg-158e`, `mg list
// --tag=...` and `mg show` all reach it without an item-schema change.
const successorTagPrefix = "successor:"

// designType is the item type whose completion implies undone downstream work.
// It is deliberately the ONLY type this guard fires on. mg-9795 was archived
// correctly (an agent exited; nothing had been recommended), and a guard that
// fires on ordinary completions is a guard that gets switched off.
const designType = "design"

// SuccessorTag renders the structured link tag for a successor id.
func SuccessorTag(id string) string { return successorTagPrefix + id }

// SuccessorIDs returns the ids named by the item's successor: tags, in tag order.
func SuccessorIDs(item *Item) []string {
	var ids []string
	for _, t := range item.Tags {
		rest, ok := strings.CutPrefix(t, successorTagPrefix)
		if !ok {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			ids = append(ids, rest)
		}
	}
	return ids
}

// SuccessorRef is what a successor: tag actually resolved to — the id, and the
// title and status of the item wearing it.
//
// It exists so a caller can PRINT what it just linked. `--successor` validates
// that the id EXISTS, and existence is the only thing it can cheaply check:
// mg-9259 measured the two structural alternatives against the live store on
// 2026-08-07 and both over-fire outright.
//
//	require the successor to name this item back (depends:)   0 of 40 links
//	                                                          would pass — the
//	                                                          back-reference has
//	                                                          never once been
//	                                                          written.
//	refuse a successor that is itself done/archived          29 of 40 links
//	                                                          would be refused.
//	                                                          Most are old
//	                                                          designs whose build
//	                                                          has since landed,
//	                                                          which is the
//	                                                          LEGITIMATE case.
//
// That is an over-fire count, not an escape count, and remainder.go records
// what a guard firing at that volume costs: mg self-installs on merge, so one
// that blocks routine completions gets removed by whoever it inconveniences.
//
// So the remaining defect is not that the check is weak — it is that a wrong
// successor is INVISIBLE. `mg done <id> --successor <real-but-wrong-id>` exited
// 0 and printed nothing about what it had linked, which is what let a
// fabricated id gate a live pending item on a ticket that can never complete.
// A title on stdout at the callsite is not a guard and does not pretend to be
// one; it is the difference between a mistake anyone reads and a mistake nobody
// can see.
type SuccessorRef struct {
	ID     string // the id as recorded on the tag
	Title  string // the successor's title, or empty if it could not be read
	Status string // available | claimed | done | pending | shelved | archived
}

// DescribeSuccessors resolves every successor: tag on item and reports what
// each one names, in tag order.
//
// A tag that resolves to nothing, or to an ambiguous pair, still yields a ref —
// with an empty Status — because the caller's job here is to SHOW what is
// recorded, and a pointer at nothing is exactly the thing worth showing. Read
// errors are likewise reported as an empty Title rather than failing the
// caller: this runs after a completion has already happened, and a display
// helper must never turn a landed transition into an error.
func DescribeSuccessors(root string, item *Item) []SuccessorRef {
	return describeLinks(root, SuccessorIDs(item))
}

// requireSuccessor reports whether item may be archived: nil when it is not a
// design, or when it carries a successor: tag naming an item that still exists.
//
// The pointer is re-resolved here rather than trusted from when it was written,
// because the guard's promise is about the state of the store NOW — a tag
// pointing at a deleted item tracks nothing, exactly like no tag at all.
func requireSuccessor(root string, item *Item) error {
	if item.Type != designType {
		return nil
	}

	ids := SuccessorIDs(item)
	if len(ids) == 0 {
		return errNoSuccessor(item)
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

	return errDanglingSuccessor(item, ids)
}

// checkSuccessorTarget validates a proposed successor id without writing it.
//
// The target must resolve to a real item, and must not be the design itself: a
// pointer at nothing and a pointer at oneself are both false passes, and this
// is the one moment mg can cheaply tell the difference.
func checkSuccessorTarget(root string, item *Item, successor string) error {
	if successor == item.ID {
		return mgerr.Usage("self_successor",
			fmt.Sprintf("%s: an item cannot be its own successor.", item.ID),
			"Name the item that tracks what this design recommends.")
	}

	matches, err := Resolve(root, successor)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return mgerr.NotFound("no_such_successor",
			fmt.Sprintf("%s: no such work item, so it cannot track %s.", successor, item.ID),
			fmt.Sprintf("File the follow-up item first, then run 'mg archive %s --successor <id>' with its id.", item.ID))
	}
	return nil
}

// linkSuccessor writes a successor: tag onto the design item at path and
// persists it, so the link survives the archive as part of the record.
func linkSuccessor(root, path string, item *Item, successor string) error {
	if err := checkSuccessorTarget(root, item, successor); err != nil {
		return err
	}

	tag := SuccessorTag(successor)
	for _, t := range item.Tags {
		if t == tag {
			return nil
		}
	}
	item.Tags = append(item.Tags, tag)

	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		return ioErr(fmt.Sprintf("%s: could not record successor %s: %s", item.ID, successor, fsErrText(err)))
	}
	return nil
}

// linkSuccessorBothWays records the forward link and then the reverse one.
//
// The order is not arrangeable. The forward tag is the half the guards read and
// the half a refusal is scored against, so it is written first and its error is
// returned; the reverse half is best-effort and reports its own failures. A
// reverse link written before a forward link that then failed would leave the
// successor claiming an inheritance from an item that never named it.
//
// reconcileBacklinks runs on the item AFTER the new tag is on it, so this call
// covers both the id supplied by --successor and any successor: tag the item was
// already carrying. See predecessor.go for why every route is reconciled and not
// just this one.
func linkSuccessorBothWays(root, path string, item *Item, successor string) error {
	if err := linkSuccessor(root, path, item, successor); err != nil {
		return err
	}
	reconcileBacklinks(root, item)
	return nil
}

// errNoSuccessor is the refusal this whole file exists for.
//
// The hint names `--successor <id>` and NOTHING ELSE. `--force` exists (some
// designs are genuinely abandoned rather than implemented) but is deliberately
// UNOFFERED here: an agent that hits a refusal mid-cleanup, at speed, reaches
// for whatever the error message hands it, and a guard whose own failure text
// teaches the bypass is decorative — the same reasoning as the body ratchet's
// inventory comment. `--force` is discoverable in `mg archive --help`, where it
// is read deliberately rather than under time pressure, and using it emits a
// work.archive_forced event so a forced archive is not invisible afterwards.
func errNoSuccessor(item *Item) *mgerr.Error {
	return mgerr.Conflict("design_without_successor",
		fmt.Sprintf("%s is a done design with no successor: archiving it would hide the work it recommends, since an archived item cannot be the tracker for undone work.", item.ID),
		fmt.Sprintf("File the item that tracks the recommendation, then run 'mg archive %s --successor <id>'.", item.ID))
}

// mgerrCode extracts the machine slug from a taxonomy error, for recording in
// a work.archive_forced event. An audit of forced archives needs to know WHICH
// guard was bypassed, not merely that one was.
func mgerrCode(err error) string {
	var e *mgerr.Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return "unknown"
}

// errDanglingSuccessor reports a successor: tag that names nothing. It is
// distinct from errNoSuccessor because "you pointed at a deleted item" and "you
// pointed at nothing" need different fixes, and collapsing them would hide the
// fact that a link the operator believed was in place has rotted.
func errDanglingSuccessor(item *Item, ids []string) *mgerr.Error {
	return mgerr.Conflict("dangling_successor",
		fmt.Sprintf("%s names successor %s, which no longer exists: nothing is tracking what this design recommends.", item.ID, strings.Join(ids, ", ")),
		fmt.Sprintf("File the item that tracks the recommendation, then run 'mg archive %s --successor <id>'.", item.ID))
}
