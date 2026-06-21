package workitem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	status, err := Status(root, id)
	if err != nil {
		return "", 0
	}
	if status == "claimed" {
		claimedDir := filepath.Join(root, "work", "claimed")
		if entries, err := os.ReadDir(claimedDir); err == nil {
			for _, e := range entries {
				name := e.Name()
				if name == id+".md" || strings.HasPrefix(name, id+".md.") {
					return status, parseClaimPID(name)
				}
			}
		}
	}
	return status, 0
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

// withHint joins a problem statement with an optional remediation hint.
func withHint(problem, hint string) error {
	if hint == "" {
		return errors.New(problem)
	}
	return fmt.Errorf("%s %s", problem, hint)
}

// errNoSuchItem is the standard "this ID does not exist anywhere" message.
func errNoSuchItem(id string) error {
	return fmt.Errorf("%s: no such work item.", id)
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
// the item out of available/, by diagnosing where the item actually is.
func explainClaimFailure(root, id string) error {
	status, pid := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "claimed":
		who := ""
		if pid > 0 {
			who = fmt.Sprintf(" (by PID %d)", pid)
		}
		return withHint(fmt.Sprintf("%s: already claimed%s.", id, who), remediation("claimed", id))
	case "done":
		return withHint(fmt.Sprintf("%s: already done.", id), remediation("done", id))
	case "pending":
		return withHint(fmt.Sprintf("%s: not available yet — it is waiting on unmet dependencies.", id), remediation("pending", id))
	case "shelved":
		return withHint(fmt.Sprintf("%s: is shelved.", id), remediation("shelved", id))
	case "archived":
		return withHint(fmt.Sprintf("%s: is archived.", id), remediation("archived", id))
	default:
		// Item is (or just became) available but the rename still failed —
		// most likely a race with another worker that just claimed it.
		return fmt.Errorf("%s: could not be claimed; it may have just been claimed by another worker. Run 'mg show %s' to check.", id, id)
	}
}

// explainDoneFailure produces a user-facing error when 'done' could not find
// the item in claimed/.
func explainDoneFailure(root, id string) error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return withHint(fmt.Sprintf("%s: not claimed, so it cannot be completed.", id), remediation("available", id))
	case "pending":
		return withHint(fmt.Sprintf("%s: not claimed — it is still pending on dependencies.", id), remediation("pending", id))
	case "done":
		return withHint(fmt.Sprintf("%s: already done.", id), remediation("done", id))
	case "shelved":
		return withHint(fmt.Sprintf("%s: is shelved, not claimed.", id), remediation("shelved", id))
	case "archived":
		return withHint(fmt.Sprintf("%s: is archived, not claimed.", id), remediation("archived", id))
	default:
		return fmt.Errorf("%s: could not be completed; its claim may have just changed. Run 'mg show %s' to check.", id, id)
	}
}

// explainUnclaimFailure produces a user-facing error when 'unclaim' could not
// find the item in claimed/.
func explainUnclaimFailure(root, id string) error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return fmt.Errorf("%s: not claimed, so there is nothing to release.", id)
	case "pending":
		return fmt.Errorf("%s: not claimed — it is pending on dependencies.", id)
	case "done":
		return withHint(fmt.Sprintf("%s: already done, not claimed.", id), remediation("done", id))
	case "shelved":
		return withHint(fmt.Sprintf("%s: is shelved, not claimed.", id), remediation("shelved", id))
	case "archived":
		return withHint(fmt.Sprintf("%s: is archived, not claimed.", id), remediation("archived", id))
	default:
		return fmt.Errorf("%s: could not release claim; it may have just changed. Run 'mg show %s' to check.", id, id)
	}
}

// explainReopenFailure produces a user-facing error when 'reopen' could not
// find the item in done/.
func explainReopenFailure(root, id string) error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "available":
		return fmt.Errorf("%s: not done — it is available, so there is nothing to reopen.", id)
	case "claimed":
		return fmt.Errorf("%s: not done — it is already claimed (in progress).", id)
	case "pending":
		return fmt.Errorf("%s: not done — it is pending on dependencies.", id)
	case "shelved":
		return withHint(fmt.Sprintf("%s: is shelved, not done.", id), remediation("shelved", id))
	case "archived":
		return withHint(fmt.Sprintf("%s: is archived, not done.", id), remediation("archived", id))
	default:
		return fmt.Errorf("%s: could not be reopened; it may have just changed. Run 'mg show %s' to check.", id, id)
	}
}

// explainUnshelveFailure produces a user-facing error when 'unshelve' could not
// find the item in shelved/.
func explainUnshelveFailure(root, id string) error {
	status, _ := statusWithPID(root, id)
	switch status {
	case "":
		return errNoSuchItem(id)
	case "shelved":
		return fmt.Errorf("%s: could not be unshelved; it may have just changed. Run 'mg show %s' to check.", id, id)
	default:
		return fmt.Errorf("%s: is not shelved (status: %s), so there is nothing to unshelve.", id, status)
	}
}
