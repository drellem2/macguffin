// Package fsstore is mg's filesystem storage backend: the store exactly as it
// has always been laid out on disk.
//
//	<root>/work/{available,claimed,done,pending,shelved}/<id>.md
//	<root>/work/claimed/<id>.md.<holder>        (holder = owner PID)
//	<root>/work/archive/<YYYY-MM>/<id>.md       (+ loose <root>/work/archive/<id>.md)
//	<root>/work/<status>/<id>.result.json       (result sidecar, travels with the .md)
//	<root>/events.jsonl                         (audit log)
//
// An item's status is the directory it is in; there is no status field in the
// file. That is why a transition is a rename(2) and nothing else.
//
// # This package owns the atomic filesystem steps
//
// Every rename(2), link(2) and O_EXCL create on a work-item path in macguffin
// lives in this package — the guard test in store/fsstore enforces it. The
// primitives below (MoveItem, MoveNoReplace, CreateExclusive, RewriteExisting,
// MoveFile) are what internal/workitem's lifecycle logic calls at the single
// instant each operation becomes true, and the store.Backend methods in fs.go
// are built from the same primitives. Two implementations of "the atomic step"
// would be two chances to disagree about it.
package fsstore

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/drellem2/macguffin/store"
)

// MoveItem is the Transition step: rename(2) of an item's file from one status
// directory to another (or within one, for a re-stamped claim).
//
// rename(2) is atomic on a local filesystem. When two processes race to move
// the same source, exactly one succeeds and the other gets ENOENT — that is the
// "exactly one wins" property store.Backend.Transition promises. The raw error
// is returned so callers keep their own diagnostics (os.IsNotExist → "lost").
//
// It REPLACES an existing destination silently, which is right for a move into
// a directory the id cannot already occupy. Where a same-id record can sit at
// dst (archive partitions, a legacy twin), use MoveNoReplace.
func MoveItem(src, dst string) error {
	return os.Rename(src, dst)
}

// MoveNoReplace moves src to dst, refusing when dst already exists.
//
// The check is race-safe: link(2) fails with EEXIST atomically when dst exists,
// so there is no window between "is it there?" and "move it" for another writer
// to fill. Only after the link succeeds is src unlinked; the two names share an
// inode, so the mtime the archive partitions on is preserved exactly as rename
// preserved it. If the unlink fails, the new link is removed again so the item
// is not left in two directories — no bytes are lost either way.
//
// A refusal returns an error satisfying errors.Is(err, fs.ErrExist). Any other
// failure is the raw filesystem error.
func MoveNoReplace(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// CreateExclusive creates path with content, never truncating an existing
// file: O_EXCL makes "is the id free?" and "take it" one step. The
// fs.ErrExist it returns on collision is what drives mg's remint loop.
func CreateExclusive(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RewriteExisting replaces the content of the file at path — which must
// already exist — keeping its name, mode and therefore its status.
//
// It deliberately has no O_CREATE. os.WriteFile opens O_CREATE|O_TRUNC, so a
// read-modify-write that loses a race to a rename (a claim landing between the
// edit's read and its write) RE-CREATES the file at the path the item just
// left, and the item is then in two status directories at once. Without
// O_CREATE the open fails with ENOENT instead, which the caller reports as
// lost (the error satisfies os.IsNotExist).
//
// If the rename lands after the open, the write goes to the open inode — the
// item at its NEW path — which is the right place for it: the edit follows the
// item instead of resurrecting it.
//
// ifUnchanged, when non-empty, is checked against the bytes read through the
// same open descriptor before anything is truncated; a mismatch returns
// *store.ErrChanged and leaves the file untouched. On the filesystem this is
// compare-then-write, not compare-and-swap: a writer landing in the
// microseconds between the check and the write is not seen. Closing that
// would take a lock, which mg deliberately does not hold (see
// workitem.UpdateWithBodyChange).
func RewriteExisting(path string, ifUnchanged store.Hash, content []byte) error {
	flag := os.O_WRONLY | os.O_TRUNC
	if ifUnchanged != "" {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return err
	}
	if ifUnchanged != "" {
		stored, err := io.ReadAll(f)
		if err != nil {
			f.Close()
			return err
		}
		if got := store.HashOf(stored); got != ifUnchanged {
			f.Close()
			return &store.ErrChanged{Want: ifUnchanged, Current: got}
		}
		if err := f.Truncate(0); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// MoveFile renames a companion file — a result sidecar, a saved prior body —
// alongside the item it belongs to. It is rename(2), exactly as MoveItem, and
// is separate only so that a reader can tell the status-defining move from the
// moves that follow it.
func MoveFile(src, dst string) error {
	return os.Rename(src, dst)
}

// isExist reports a "destination already exists" refusal from MoveNoReplace or
// CreateExclusive.
func isExist(err error) bool { return errors.Is(err, fs.ErrExist) }
