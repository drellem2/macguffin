package workitem

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
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
	srcPath := m.Path
	srcName := filepath.Base(srcPath)

	claimed, err := readFile(srcPath)
	if err != nil {
		return nil, nil, err
	}

	srcDir := filepath.Dir(srcPath)
	doneDir := filepath.Join(root, "work", "done")

	// The result lands FIRST, beside the item where it currently sits, so that
	// nothing below can cost the caller their payload. See the doc comment: the
	// guards are allowed to refuse, they are not allowed to charge for it.
	//
	// Fold any pre-existing result into the fresh one before writing, so an
	// irreconcilable pair fails with both copies still on disk rather than
	// half-reconciled. The prior result is the one the move below would leave
	// at the destination: normally the copy travelling out of the origin
	// directory, or — when the origin has none — a copy already sitting in
	// done/ from an earlier completion.
	resultPreserved := false
	if len(resultJSON) > 0 {
		priorPath := filepath.Join(srcDir, resultSidecarName(id))
		if _, err := os.Stat(priorPath); os.IsNotExist(err) {
			priorPath = filepath.Join(doneDir, resultSidecarName(id))
		}
		sidecarBytes, err := mergeResultSidecar(priorPath, id, resultJSON)
		if err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(srcDir, resultSidecarName(id)), sidecarBytes, 0o644); err != nil {
			return nil, nil, fmt.Errorf("writing result sidecar: %w", err)
		}
		resultPreserved = true
	}

	// Guards run against the item on disk. Linking a successor comes first so
	// that `mg done <id> --successor <id>` is a single step for the agent: the
	// tag is validated and written, and the guard then sees the discharged
	// obligation. linkSuccessor validates before it writes, so a bad target
	// leaves the file untouched.
	if o.successor != "" {
		if err := linkSuccessor(root, srcPath, claimed, o.successor); err != nil {
			return nil, nil, notePreservedResult(err, id, srcDir, resultPreserved)
		}
	}
	if err := requireRemainderDischarged(root, claimed); err != nil {
		return nil, nil, notePreservedResult(err, id, srcDir, resultPreserved)
	}

	// Extract the claim-holder PID from the filename (<id>.md.<pid>) for the event.
	claimPID := ""
	if dot := strings.LastIndex(srcName, "."); dot > 0 {
		if _, err := strconv.Atoi(srcName[dot+1:]); err == nil {
			claimPID = srcName[dot+1:]
		}
	}

	dstPath := filepath.Join(doneDir, id+".md")

	// rename(2) is atomic on local filesystems.
	if err := os.Rename(srcPath, dstPath); err != nil {
		return nil, nil, ioErr(fmt.Sprintf("%s: could not be completed: %s", id, fsErrText(err)))
	}

	// Carry the sidecar forward with the .md. This one move now covers both
	// cases: a result this call merged and wrote above, and a result that was
	// already sitting in claimed/ from an earlier done -> reopen -> done round
	// trip. Without it either would be orphaned — a .result.json with no .md
	// beside it, invisible to `mg show` and asserting a stale completion.
	//
	// There is deliberately no second write into done/ after this. The merge
	// happened before the write above, so the file this moves IS the merged
	// result; a post-rename write would only be a chance to disagree with it.
	if err := moveResultSidecar(srcDir, doneDir, id); err != nil {
		return nil, nil, fmt.Errorf("moving result sidecar: %w", err)
	}

	item, err := readFile(dstPath)
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

	dir := filepath.Join(root, "work", status)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s/: %w", status, err)
	}

	var items []*Item
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") && !strings.Contains(e.Name(), ".md.") {
			continue
		}
		// Skip .result.json sidecars
		if strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		item, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip malformed files
		}
		items = append(items, item)
	}

	return items, nil
}

// ListAll returns all work items across active statuses, grouped by status.
// Archived and shelved items are excluded — use ListByStatus to retrieve them.
func ListAll(root string) (map[string][]*Item, error) {
	result := make(map[string][]*Item)
	for _, status := range []string{"available", "claimed", "done", "pending"} {
		items, err := ListByStatus(root, status)
		if err != nil {
			continue
		}
		if len(items) > 0 {
			result[status] = items
		}
	}
	return result, nil
}
