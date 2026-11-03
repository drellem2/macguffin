package workitem

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
)

// DoneOption configures an optional behaviour of Done.
type DoneOption func(*doneOpts)

type doneOpts struct {
	// successor is the id of the item that carries this item's recommendation
	// forward, written onto the item as a successor: tag before it moves.
	successor string
}

// WithDoneSuccessor records the id of the item that tracks what this one
// recommends, discharging the declared-remainder guard. See remainder.go.
func WithDoneSuccessor(id string) DoneOption {
	return func(o *doneOpts) { o.successor = id }
}

// Done atomically moves a claimed work item to done/ and writes an optional
// result sidecar JSON file. The item must currently be in claimed/.
// resultJSON may be nil if no result metadata is provided.
// After completing the item, any pending items whose dependencies are now
// fully satisfied are auto-promoted to available.
//
// An item that DECLARES a remainder (see remainder.go) is refused unless
// something is tracking what it recommends. The refusal leaves the item in
// claimed/ so the agent standing there can file the successor and retry.
//
// THE RESULT IS WRITTEN BEFORE THE GUARDS RUN, and that ordering is the point
// (mg-9259). The guards used to sit ahead of every mutation, which read as the
// conservative choice — "a refused completion leaves the store exactly as it
// was". What it actually did was DISCARD the caller's --result on refusal,
// because the payload only ever existed in argv. So the agent that had just
// finished a triage faced "supply a successor or lose your work" at a moment
// when, on the gh-issue track, no id could legally satisfy the guard: the
// build ticket is not filed until after the human gate. The only move that got
// the work through was a REAL id naming the WRONG item, and mg accepted that
// silently. A guard whose refusal costs the operator their payload does not
// buy safety; it buys a fabricated argument.
//
// So the result is merged and written into the item's CURRENT directory first.
// A refusal now costs a retry rather than the work, and the refusal says so.
// The sidecar is a companion to the .md and every transition moves the pair
// together (see moveResultSidecar), so a result parked beside a claimed item
// travels wherever the item goes next — into done/ on the retry, back to
// available/ on an unclaim. Nothing is stranded by the early write.
func Done(root, id string, resultJSON json.RawMessage, opts ...DoneOption) (*Item, []*Item, error) {
	var o doneOpts
	for _, apply := range opts {
		apply(&o)
	}

	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, nil, err
	}
	if m.Status != "claimed" {
		return nil, nil, explainDoneFailure(root, id)
	}
	claimed, err := readMatch(m)
	if err != nil {
		return nil, nil, err
	}

	b, err := be(root)
	if err != nil {
		return nil, nil, err
	}

	// The result lands FIRST, beside the item where it currently sits, so that
	// nothing below can cost the caller their payload. See the doc comment: the
	// guards are allowed to refuse, they are not allowed to charge for it.
	//
	// The result sidecar is a companion FILE, so it exists only on a backend
	// whose items are local files (store.Local). On any other backend a
	// --result is refused here, before anything moves, rather than dropped.
	srcDir := ""
	resultPreserved := false
	if len(resultJSON) > 0 {
		local, ok := localOf(b)
		if !ok {
			return nil, nil, errResultsUnsupported(id)
		}
		srcDir = filepath.Dir(m.Path)
		if err := writeResultBeside(srcDir, local.StatusDir(store.Done), id, resultJSON); err != nil {
			return nil, nil, err
		}
		resultPreserved = true
	}

	// Guards run against the item as stored. Linking a successor comes first so
	// that `mg done <id> --successor <id>` is a single step for the agent: the
	// tag is validated and written, and the guard then sees the discharged
	// obligation. linkSuccessor validates before it writes, so a bad target
	// leaves the item untouched.
	if o.successor != "" {
		if err := linkSuccessorBothWays(root, m, claimed, o.successor); err != nil {
			return nil, nil, notePreservedResult(err, id, srcDir, resultPreserved)
		}
	} else {
		// An item can carry a successor: tag that no --successor supplied — from
		// `mg edit --add-tags`, from `mg new --tags`, or from a previous run of
		// this command. Reconciling here means the reverse link is a property of
		// the LINK rather than of the route that filed it, so `mg show <successor>`
		// names what it inherited however the inheritance was recorded.
		reconcileBacklinks(root, claimed)
	}
	if err := requireRemainderDischarged(root, claimed); err != nil {
		return nil, nil, notePreservedResult(err, id, srcDir, resultPreserved)
	}

	// The claim holder (on the filesystem, the PID of <id>.md.<pid>) for the event.
	claimPID := ""
	if p := holderPID(m.Holder); p > 0 {
		claimPID = m.Holder
	}

	// The atomic instant, expecting the exact claim that was resolved. The
	// backend carries the result sidecar with the item: a result this call
	// wrote above, or one already sitting in claimed/ from an earlier
	// done -> reopen -> done round trip. There is deliberately no second write
	// into done/ after this — the merge happened before the write above, so
	// the file that moves IS the merged result.
	if err := b.Transition(m.ID, m.State(), store.State{Status: store.Done}, writer(0)); err != nil {
		if isLost(err) {
			return nil, nil, explainDoneFailure(root, id)
		}
		return nil, nil, ioErr(fmt.Sprintf("%s: could not be completed: %s", id, fsErrText(err)))
	}

	item, err := readIn(root, m.ID, store.Done)
	if err != nil {
		return nil, nil, err
	}

	event.Emit(root, "work.done", map[string]string{
		"item_id":     id,
		"from_status": "claimed",
		"to_status":   "done",
		"actor":       actor(),
		"pid":         claimPID,
	})

	// Auto-promote pending items whose dependencies are now satisfied.
	promoted, err := Schedule(root)
	if err != nil {
		return nil, nil, fmt.Errorf("auto-promoting pending items: %w", err)
	}

	return item, promoted, nil
}

// notePreservedResult tells a refused caller that their --result survived.
//
// Writing the sidecar ahead of the guards (see Done) removes the DATA LOSS, but
// on its own it does not remove the PRESSURE: an operator who watches `mg done`
// exit non-zero has every reason to assume the payload went with it, and acts
// on that assumption by reaching for any id that gets the command through. The
// reassurance therefore has to be in the refusal itself, at the moment and place
// the wrong decision gets made — not in `--help`, and not in a doc.
//
// It is appended to the HINT rather than the message because it is remediation:
// it changes what the reader should do next (retry, cheaply) and not what went
// wrong. Both refusal paths get it — a bad `--successor` target and an
// undischarged declaration are the same situation from the caller's side.
func notePreservedResult(err error, id, dir string, preserved bool) error {
	if !preserved {
		return err
	}
	var e *mgerr.Error
	if !errors.As(err, &e) {
		return err
	}
	return e.AppendHint(fmt.Sprintf(
		"Your --result is already recorded at %s and is NOT lost: it stays with %s and is carried into done/ when the retry succeeds, whether or not you pass --result again.",
		filepath.Join(dir, resultSidecarName(id)), id))
}

// Status returns the lifecycle state of a work item: "available", "claimed",
// "done", "pending", "shelved", or "archived". It errors if the ID is
// ambiguous. See Resolve.
func Status(root, id string) (string, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return "", err
	}
	return m.Status, nil
}

// ListByStatus returns all work items in the given status directory.
// Valid statuses: "available", "claimed", "done".
func ListByStatus(root, status string) ([]*Item, error) {
	switch status {
	case "available", "claimed", "done", "pending", "shelved":
		// valid
	default:
		return nil, fmt.Errorf("invalid status %q (must be available, claimed, done, pending, or shelved)", status)
	}

	docs, err := listDocs(root, store.Status(status))
	if err != nil {
		return nil, fmt.Errorf("reading %s/: %w", status, err)
	}

	var items []*Item
	for i := range docs {
		item, err := itemOf(&docs[i])
		if err != nil {
			continue // skip malformed files
		}
		items = append(items, item)
	}

	return items, nil
}

// listAllOrder is the order ListAll reads the lifecycle directories, and it is
// the order items FLOW between them: pending → available → claimed → done.
//
// That ordering is load-bearing, not cosmetic. Enumeration is four separate
// ReadDirs and nothing holds the store still across them, so another process —
// an agent's `mg claim`, or the auto-promoter that now runs on every single mg
// invocation — can rename an item out of one directory and into another
// between two of the reads. Which way that lands depends entirely on the order:
//
//   - source read BEFORE destination: the item is seen twice, once on each side
//     of the rename. Recoverable — dedupe below drops the stale sighting.
//   - destination read BEFORE source: the item is seen NOT AT ALL. It is absent
//     from the destination (not moved yet) and absent from the source (already
//     moved). Unrecoverable, and silent.
//
// The order was previously available, claimed, done, pending — which read
// pending, the source of every promotion, LAST. So a promotion landing mid-scan
// made the promoted item vanish from its own listing. `mg list --json` emitted
// zero lines for a store holding one item, and because that stream is NDJSON,
// zero lines is zero bytes: a `jq` consumer reads it as "no results" rather than
// as an error. Reading sources first converts that silent disappearance into a
// duplicate, which is a thing code can actually fix.
//
// WHAT THIS ORDER DOES NOT COVER. The lifecycle is not a line, it is a cycle:
// `mg unclaim` moves claimed -> available and `mg reopen` moves done ->
// available, both backwards along this order. No single ordering can read every
// transition's source first when the transitions form a cycle, so those two
// edges keep the vanishing window and ordering alone cannot close it. They are
// left as they were, deliberately: they fire when a specific agent unclaims or
// reopens at that exact instant, whereas promotion now fires on EVERY mg
// invocation in the fleet, which is the difference between a race you might hit
// and one CI hit on the first run after it shipped.
var listAllOrder = []string{"pending", "available", "claimed", "done"}

// ListAll returns all work items across active statuses, grouped by status.
// Archived and shelved items are excluded — use ListByStatus to retrieve them.
//
// An item caught mid-rename is reported once, under the last status it was seen
// in. Last, not first, because listAllOrder reads sources before destinations:
// the later sighting is the one on the far side of the rename, so it is the
// item's newer location.
func ListAll(root string) (map[string][]*Item, error) {
	result := make(map[string][]*Item)
	seenIn := make(map[string]string) // item ID -> status group currently holding it
	for _, status := range listAllOrder {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		for _, item := range items {
			if prev, dup := seenIn[item.ID]; dup {
				result[prev] = dropByID(result[prev], item.ID)
				if len(result[prev]) == 0 {
					delete(result, prev)
				}
			}
			result[status] = append(result[status], item)
			seenIn[item.ID] = status
		}
	}
	return result, nil
}

// dropByID returns items without the entry for id, preserving order.
func dropByID(items []*Item, id string) []*Item {
	out := items[:0]
	for _, item := range items {
		if item.ID != id {
			out = append(out, item)
		}
	}
	return out
}
