package workitem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// This file centralizes user-facing error construction for the state-transition
// commands (claim/done/unclaim/reopen/shelve/unshelve). Those commands are
// implemented as rename(2) moves between work/<status>/ directories; when a
// move fails we must NOT let the raw *os.LinkError bubble up, because it leaks
// the maildir layout, the rename op, the PID-suffix lock, and a bare ENOENT —
// none of which is user-actionable. Instead we diagnose where the item really
// is and name the semantic problem. See docs/error-message-audit.md.

// statusWithPID reports the current lifecycle status of an item and, when it is
// claimed, the PID recorded on the claim filename (0 if absent/unparseable).
// status is "" when the item does not exist in any directory.
func statusWithPID(root, id string) (status string, pid int) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return "", 0
	}
	if m.Status == "claimed" {
		return m.Status, parseClaimPID(filepath.Base(m.Path))
	}
	return m.Status, 0
}

// remediation returns the standard next-step hint for an item currently in the
// given status. Empty when there is no obvious next step.
func remediation(status, id string) string {
	switch status {
	case "available":
		return fmt.Sprintf("Run 'mg claim %s' to claim it.", id)
	case "claimed":
		return fmt.Sprintf("Run 'mg unclaim %s' to release it, or 'mg show %s' to inspect.", id, id)
	case "done":
		return fmt.Sprintf("Run 'mg reopen %s' to move it back to claimed.", id)
	case "pending":
		return fmt.Sprintf("Run 'mg show %s' to see its unmet dependencies.", id)
	case "shelved":
		return fmt.Sprintf("Run 'mg unshelve %s' to restore it.", id)
	case "archived":
		return fmt.Sprintf("Run 'mg unarchive %s' to restore it.", id)
	}
	return ""
}

// errNoSuchItem is the standard "this ID does not exist anywhere" error. It is
// the not_found category (exit 3): a named entity that exists in no directory.
func errNoSuchItem(id string) *mgerr.Error {
	return mgerr.NotFound("no_such_item", fmt.Sprintf("%s: no such work item.", id), "")
}

// ioErr wraps a sanitized IO failure (from fsErrText) as the internal category
// (exit 1) with the io_error slug. The message is already scrubbed of maildir
// internals by the caller; we keep it verbatim and do NOT surface the raw
// *os.LinkError, matching the pre-taxonomy behavior while adding a machine slug.
func ioErr(msg string) *mgerr.Error {
	return &mgerr.Error{Category: mgerr.CatInternal, Code: "io_error", Message: msg}
}

// fsErrText extracts the underlying cause from an os.Rename/os.Open style error
// (an *os.LinkError or *fs.PathError) WITHOUT the operation name or the file
// paths it embeds — those leak internal maildir layout. For other errors it
// returns the error's own text.
func fsErrText(err error) string {
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// explainClaimFailure produces a user-facing error when 'claim' could not move
// the item out of available/, by diagnosing where the item actually is. The
// state cases are conflicts (exit 4); a genuinely-absent item is not_found
// (exit 3); the race case is a retryable conflict.
func explainClaimFailure(root, id string) *mgerr.Error {
	status, pid := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "claimed":
		who := ""
		if pid > 0 {
			who = fmt.Sprintf(" (by PID %d)", pid)
		}
		return mgerr.Conflict("already_claimed", fmt.Sprintf("%s: already claimed%s.", id, who), remediation("claimed", id))
	case "done":
		return mgerr.Conflict("already_done", fmt.Sprintf("%s: already done.", id), remediation("done", id))
	case "pending":
		return mgerr.Conflict("unmet_dependencies", fmt.Sprintf("%s: not available yet — it is waiting on unmet dependencies.", id), remediation("pending", id))
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("item_archived", fmt.Sprintf("%s: is archived.", id), remediation("archived", id))
	default:
		// Item is (or just became) available but the rename still failed —
		// most likely a race with another worker that just claimed it.
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not be claimed; it may have just been claimed by another worker. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainDoneFailure produces a user-facing error when 'done' could not find
// the item in claimed/.
func explainDoneFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed, so it cannot be completed.", id), remediation("available", id))
	case "pending":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed — it is still pending on dependencies.", id), remediation("pending", id))
	case "done":
		return mgerr.Conflict("already_done", fmt.Sprintf("%s: already done.", id), remediation("done", id))
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved, not claimed.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("item_archived", fmt.Sprintf("%s: is archived, not claimed.", id), remediation("archived", id))
	default:
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not be completed; its claim may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainUnclaimFailure produces a user-facing error when 'unclaim' could not
// find the item in claimed/.
func explainUnclaimFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed, so there is nothing to release.", id), "")
	case "pending":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed — it is pending on dependencies.", id), "")
	case "done":
		return mgerr.Conflict("already_done", fmt.Sprintf("%s: already done, not claimed.", id), remediation("done", id))
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved, not claimed.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("item_archived", fmt.Sprintf("%s: is archived, not claimed.", id), remediation("archived", id))
	default:
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not release claim; it may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainReclaimFailure produces a user-facing error when 'reclaim' could not
// find the item in claimed/. Every non-claimed status is a conflict (exit 4)
// rather than a fallback to claiming the item: `reclaim` re-stamps an existing
// claim and nothing else, because a reclaim that could also claim an available
// item would be a `claim` with its atomic refusal — the fleet's
// duplicate-dispatch guard — bypassed. So the available case names `mg claim`
// as the remedy and takes no action itself.
func explainReclaimFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed, so there is no claim to re-stamp.", id), remediation("available", id))
	case "pending":
		return mgerr.Conflict("not_claimed", fmt.Sprintf("%s: not claimed — it is pending on dependencies.", id), remediation("pending", id))
	case "done":
		return mgerr.Conflict("already_done", fmt.Sprintf("%s: already done, not claimed.", id), remediation("done", id))
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved, not claimed.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("item_archived", fmt.Sprintf("%s: is archived, not claimed.", id), remediation("archived", id))
	default:
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: claim could not be re-stamped; it may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainReopenFailure produces a user-facing error when 'reopen' could not
// find the item in done/.
func explainReopenFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is available, so there is nothing to reopen.", id), "")
	case "claimed":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is already claimed (in progress).", id), "")
	case "pending":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is pending on dependencies.", id), "")
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved, not done.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("item_archived", fmt.Sprintf("%s: is archived, not done.", id), remediation("archived", id))
	default:
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not be reopened; it may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainArchiveFailure produces a user-facing error when 'archive ID' could
// not act because the item is not in done/. Archiving is a mutation, so every
// non-done status must be a loud refusal: the caller asked for one specific
// item and must never be left believing an archive happened when it did not.
func explainArchiveFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is available, so there is nothing to archive.", id), remediation("available", id))
	case "claimed":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is claimed (in progress).", id), remediation("claimed", id))
	case "pending":
		return mgerr.Conflict("not_done", fmt.Sprintf("%s: not done — it is pending on dependencies.", id), remediation("pending", id))
	case "shelved":
		return mgerr.Conflict("item_shelved", fmt.Sprintf("%s: is shelved, not done.", id), remediation("shelved", id))
	case "archived":
		return mgerr.Conflict("already_archived", fmt.Sprintf("%s: already archived.", id), remediation("archived", id))
	default:
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not be archived; it may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	}
}

// explainUnshelveFailure produces a user-facing error when 'unshelve' could not
// find the item in shelved/.
func explainUnshelveFailure(root, id string) *mgerr.Error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "shelved":
		return mgerr.Conflict("claim_race", fmt.Sprintf("%s: could not be unshelved; it may have just changed. Run 'mg show %s' to check.", id, id), "").WithRetryable(true)
	default:
		return mgerr.Conflict("not_shelved", fmt.Sprintf("%s: is not shelved (status: %s), so there is nothing to unshelve.", id, status), "")
	}
}
