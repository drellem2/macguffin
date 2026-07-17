package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// restorableStatuses are the states Unarchive can put an item back into.
//
// "claimed" is deliberately absent. A claimed item is stored as
// <id>.md.<pid>, and the PID is a live ownership record: restoring into
// claimed/ would mean inventing a PID for a process that never claimed this
// item. Restoring to available and letting the caller run 'mg claim' produces
// a real claim instead of a forged one.
var restorableStatuses = []string{"available", "done", "pending", "shelved"}

func isRestorableStatus(s string) bool {
	for _, r := range restorableStatuses {
		if r == s {
			return true
		}
	}
	return false
}

// archivedFrom returns the status id held when it was last archived, read from
// the work.archive record in the event log, or "" when the log does not say.
//
// "" means "the store does not know", and is never a licence to pick a
// default: Unarchive refuses rather than restore a status this function did
// not actually find. The log genuinely may not say — event.Emit is
// best-effort, the log can be rotated away, and a file moved into archive/ by
// hand leaves no record at all.
func archivedFrom(root, id string) string {
	entries, err := event.List(root, event.ListOpts{Type: "work.archive"})
	if err != nil {
		return ""
	}

	// Last record wins: an item may have been archived, restored and archived
	// again, and only the most recent transition describes the file on disk now.
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Extra["item_id"] != id {
			continue
		}
		if from := entries[i].Extra["from_status"]; isRestorableStatus(from) {
			return from
		}
		// A record naming a status we cannot faithfully rebuild (or a garbled
		// one) is not a partial answer to fall forward from. Stop here and let
		// the caller name the target.
		return ""
	}
	return ""
}

// errUnknownPriorStatus is the refusal Unarchive returns when it cannot
// establish what the item was before it was archived. It names the item and
// makes the caller supply the target, because the alternative — guessing — is
// what this error exists to prevent. Restoring an item to "available" that was
// really "done" hands finished work back to the dispatch loop as fresh work,
// and unarchive is run by someone mid-recovery who will not audit the result.
func errUnknownPriorStatus(id, title string) *mgerr.Error {
	return mgerr.Conflict(
		"unknown_prior_status",
		fmt.Sprintf("%s (%s): archived, but the store has no record of the status it held when it was archived.", id, title),
		fmt.Sprintf("Restore it explicitly with the status it should have, e.g. 'mg unarchive %s --status=done'. Valid: %s.",
			id, strings.Join(restorableStatuses, ", ")),
	)
}

// Unarchive restores an archived work item to the status it held when it was
// archived, moving both the .md file and any result.json sidecar. It returns
// the item and the status it was restored to.
//
// The archive→unarchive round-trip is state-preserving or it fails: passing an
// empty status asks Unarchive to recover the prior status from the event log,
// and it returns errUnknownPriorStatus if the log does not say. It never falls
// back to a default. A non-empty status is the caller asserting the target
// explicitly and is used as given.
func Unarchive(root, id, status string) (*Item, string, error) {
	src, cur, err := FindPath(root, id)
	if err != nil {
		return nil, "", err
	}
	if cur != "archived" {
		return nil, "", fmt.Errorf("cannot unarchive %s: item is %s, not archived", id, cur)
	}

	item, err := readFile(src)
	if err != nil {
		return nil, "", err
	}

	if status == "" {
		// item.ID, not the caller's id: the event log keys on the canonical
		// ID, while id may be a short form or carry an @partition qualifier.
		status = archivedFrom(root, item.ID)
		if status == "" {
			return nil, "", errUnknownPriorStatus(item.ID, item.Title)
		}
	}
	if !isRestorableStatus(status) {
		return nil, "", mgerr.Usage(
			"invalid_value",
			fmt.Sprintf("cannot unarchive %s to %q.", item.ID, status),
			fmt.Sprintf("Valid statuses: %s.", strings.Join(restorableStatuses, ", ")),
		)
	}

	dstDir := filepath.Join(root, "work", status)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, "", ioErr(fmt.Sprintf("%s: could not be unarchived: %s", item.ID, fsErrText(err)))
	}
	dst := filepath.Join(dstDir, item.ID+".md")
	if err := os.Rename(src, dst); err != nil {
		return nil, "", ioErr(fmt.Sprintf("%s: could not be unarchived: %s", item.ID, fsErrText(err)))
	}

	// Move result sidecar if it exists, so it travels with the .md. src points
	// at archive/<partition>/<id>.md; dst is <status>/<id>.md.
	if err := moveResultSidecar(filepath.Dir(src), dstDir, item.ID); err != nil {
		return nil, "", ioErr(fmt.Sprintf("%s: could not be unarchived (result file): %s", item.ID, fsErrText(err)))
	}

	item, err = readFile(dst)
	if err != nil {
		return nil, "", err
	}

	event.Emit(root, "work.unarchive", map[string]string{
		"item_id":     item.ID,
		"from_status": "archived",
		"to_status":   status,
		"actor":       actorFor(item),
	})

	return item, status, nil
}
