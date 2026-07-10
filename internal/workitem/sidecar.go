package workitem

import (
	"os"
	"path/filepath"
)

// resultSidecarName is the filename of a work item's result sidecar, written by
// `mg done` next to the item's .md file (see Done). It records the completion
// result (e.g. the merged branch) as JSON.
func resultSidecarName(id string) string { return id + ".result.json" }

// moveResultSidecar moves <id>.result.json from srcDir to dstDir when it exists.
//
// The result sidecar is a companion to the work item's .md file and must travel
// with it across EVERY status transition. A sidecar left behind in the old
// status directory is an orphan: it asserts a completion for an item that has
// since moved on, corrupting the audit trail (a .result.json sitting in
// available/ next to no .md claims the item is done when it is really archived).
// This class of bug — mg status transitions orphaning the sidecar — is exactly
// what mg-ab67 fixed; every rename of an <id>.md must be paired with a call
// here so it cannot recur.
//
// It is a no-op when the item has no sidecar (the common case for items that
// were never marked done), so callers can invoke it unconditionally after the
// .md rename. dstDir is created if it does not already exist. Following the
// convention of Archive/Unarchive, the .md rename is the authoritative,
// status-defining move; the sidecar is moved immediately after and any failure
// is surfaced loudly rather than swallowed.
func moveResultSidecar(srcDir, dstDir, id string) error {
	src := filepath.Join(srcDir, resultSidecarName(id))
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	return os.Rename(src, filepath.Join(dstDir, resultSidecarName(id)))
}
