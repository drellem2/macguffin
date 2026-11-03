package workitem

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
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
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, "", err
	}
	if m.Status != "archived" {
		return nil, "", fmt.Errorf("cannot unarchive %s: item is %s, not archived", id, m.Status)
	}

	item, err := readMatch(m)
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

	b, err := be(root)
	if err != nil {
		return nil, "", err
	}
	un, ok := b.(store.Unarchiver)
	if !ok {
		return nil, "", mgerr.Usage("unarchive_unsupported",
			fmt.Sprintf("%s: this storage backend cannot restore archived items.", item.ID), "")
	}
	// Never a replacing move: a legacy same-id twin may already live in the
	// destination, and the backend refuses rather than destroys it (mg-1096).
	// The result sidecar travels with the record.
	if err := un.Unarchive(m.ID, m.Partition, inStatus(status), writer(0)); err != nil {
		if errors.Is(err, store.ErrExists) {
			dst := filepath.Join(status, item.ID)
			if d := statusDirOf(root, status); d != "" {
				dst = filepath.Join(d, filepath.Base(m.Path))
			}
			return nil, "", errDestinationExists(item.ID, m.Path, dst)
		}
		return nil, "", ioErr(fmt.Sprintf("%s: could not be unarchived: %s", item.ID, fsErrText(err)))
	}

	// Saved prior bodies come back out of the partition with the record, so the
	// archive→unarchive round trip leaves them where a live item's backups
	// live. Without this they would stay behind in archive/<partition>/.bodybak/
	// while the item is edited from work/, and the two halves would diverge.
	// (Companion files: only on a local backend.)
	if live := liveBodyBackupParent(root); live != "" {
		if err := moveBodyBackups(root, filepath.Join(filepath.Dir(m.Path), bodyBackupDirName), live, item.ID); err != nil {
			return nil, "", ioErr(fmt.Sprintf("%s: could not be unarchived (saved bodies): %s", item.ID, fsErrText(err)))
		}
	}

	item, err = readIn(root, m.ID, store.Status(status))
	if err != nil {
		return nil, "", err
	}

	event.Emit(root, "work.unarchive", map[string]string{
		"item_id":     item.ID,
		"from_status": "archived",
		"to_status":   status,
		"actor":       actor(),
	})

	return item, status, nil
}
