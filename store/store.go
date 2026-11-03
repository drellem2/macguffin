// Package store is mg's storage-backend seam: the operations mg needs from the
// place its work items live, named by what they MEAN to mg rather than by how
// the filesystem happens to do them.
//
// mg's own build uses the filesystem backend in store/fsstore, which is the
// store exactly as it has always been laid out on disk (~/.macguffin/work/...).
// A separate module can build mg against another backend by implementing
// Backend and calling mgcli.Main with an Opener for it. That module imports
// this package and mgcli; macguffin never imports it back.
//
// # The atomic primitive is named
//
// Every lifecycle move — claim, unclaim, reclaim, done, reopen, promote,
// shelve, snooze — is a Transition(id, expect, to). It succeeds for exactly one
// caller that expected the item's current state, and every other caller gets a
// typed *ErrLost naming the state the item is actually in. On the filesystem
// that property is rename(2): two renames of one source, one wins and the loser
// gets ENOENT. Another backend has to provide the same property by its own
// means (a compare-and-swap, a conflicting write that is refused), and the
// contract tests in store/storetest check it does.
//
// Nothing here hides that step inside a "write a file" call. Edit is a separate
// operation with its own precondition (ifUnchanged), and it must never move an
// item or recreate one that a concurrent Transition moved away.
//
// # What the backend does NOT know
//
// Item content is opaque bytes. Parsing frontmatter, deciding where a new item
// is placed, depends gating, successors, snooze gates — all of that is mg's
// logic and stays in mg. The backend stores documents, knows each one's state,
// and moves them between states atomically.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Status is an item's lifecycle state.
type Status string

// The statuses mg knows. They are the directory names of the filesystem
// backend, which is why they are spelled the way they are.
const (
	Available Status = "available"
	Claimed   Status = "claimed"
	Done      Status = "done"
	Pending   Status = "pending"
	Shelved   Status = "shelved"
	Archived  Status = "archived"
)

// ActiveStatuses are the non-archive statuses, in the order mg scans them.
var ActiveStatuses = []Status{Available, Claimed, Done, Pending, Shelved}

// State is a Status plus, for Claimed, who holds the claim.
//
// Holder is opaque to mg's logic beyond equality. The filesystem backend
// stores it as the claimed/<id>.md.<holder> suffix (today: an owner PID); a
// shared backend can store a user name. An empty Holder on a non-claimed
// state means "nobody".
//
// As the EXPECT side of a Transition, an empty Holder on Claimed means "held
// by anyone": expecting {Claimed, ""} is satisfied by any claim. A non-empty
// Holder must match exactly.
//
// As the TO side, an empty Holder on Claimed asks the backend to derive the
// holder from the Transition's Actor (fsstore: the Actor's PID). In a state a
// backend reports (Get, ErrLost.Current), Holder says WHO holds the claim, in
// the backend's own terms.
type State struct {
	Status Status
	Holder string
}

// String renders a state for messages, e.g. "claimed by 1234" or "available".
func (s State) String() string {
	if s.Holder != "" {
		return fmt.Sprintf("%s by %s", s.Status, s.Holder)
	}
	return string(s.Status)
}

// Satisfies reports whether the current state s satisfies expect, under the
// "empty holder means any holder" rule above.
func (s State) Satisfies(expect State) bool {
	if s.Status != expect.Status {
		return false
	}
	return expect.Holder == "" || s.Holder == expect.Holder
}

// Actor is who is performing a write. It is IN THE CALL, not inferred by the
// backend, because on a shared store every agent may authenticate as the same
// user: two identical claims by "the same" user would both land, so a claim
// needs a per-process identity (host, PID, a nonce) to be distinguishable at
// all. Backends record what they can of it:
//
//   - fsstore records PID as the claimed/<id>.md.<pid> suffix (the Holder) —
//     exactly what a claim has always recorded, with no on-disk change — and
//     ignores the rest. A zero PID records no holder (a bare claimed/<id>.md,
//     which is what `mg reopen` has always produced).
//   - a shared backend can record all of it on the item's state line.
//
// Name is the attributed identity (MG_ACTOR / POGO_AGENT_NAME / OS user — the
// same answer mg's audit log gives). Nonce is any value unique to this
// process's attempt.
type Actor struct {
	Name  string
	Host  string
	PID   int
	Nonce string
}

// Hash identifies a version of an item's content. It is the lowercase hex
// SHA-256 of the full stored bytes.
type Hash string

// HashOf returns the Hash of content.
func HashOf(content []byte) Hash {
	sum := sha256.Sum256(content)
	return Hash(hex.EncodeToString(sum[:]))
}

// Doc is one stored item: its id, where it is in the lifecycle, and its bytes.
type Doc struct {
	ID    string
	State State
	// Partition is the archive partition (e.g. "2026-07") of an archived item;
	// empty for every other status.
	Partition string
	Content   []byte
	// Hash is HashOf(Content), the token Edit's ifUnchanged compares against.
	Hash Hash
	// Mtime is the last-modified time of the stored content, when the backend
	// has one; zero otherwise.
	Mtime time.Time
	// Where names the stored record for humans — in an ambiguity error, a
	// shadow notice, a conflict naming two records. fsstore: the absolute file
	// path. A backend with no better answer can leave it empty; mg then
	// renders "<status>/<id>".
	Where string
}

// ErrNotFound is returned (wrapped) when no item has the id.
var ErrNotFound = errors.New("store: item not found")

// ErrExists is returned (wrapped) by Create when the id already names an item
// ANYWHERE in the store, archive included. mg remints on it.
var ErrExists = errors.New("store: id already exists")

// ErrAmbiguous is returned (wrapped) when one id names more than one stored
// document (a legacy same-id twin). The filesystem backend can hold those; a
// backend that cannot need never return it.
var ErrAmbiguous = errors.New("store: id is ambiguous")

// ErrLost is the refusal of the atomic primitive: the item was not in the
// state the caller expected, so nothing was moved (Transition) or written
// (Edit). Current is the state the backend observed instead, or nil when the
// item could not be found at all.
type ErrLost struct {
	ID      string
	Expect  State
	Current *State
}

func (e *ErrLost) Error() string {
	if e.Current == nil {
		return fmt.Sprintf("store: %s: expected %s, but the item is gone", e.ID, e.Expect)
	}
	return fmt.Sprintf("store: %s: expected %s, but it is %s", e.ID, e.Expect, *e.Current)
}

// ErrChanged is Edit's refusal: the stored content no longer hashes to
// ifUnchanged. Nothing was written. Current is the hash actually stored.
type ErrChanged struct {
	ID      string
	Want    Hash
	Current Hash
}

func (e *ErrChanged) Error() string {
	return fmt.Sprintf("store: %s: content changed (want %.12s, stored %.12s)", e.ID, e.Want, e.Current)
}

// Capabilities are behaviours of mg itself that a backend may switch off.
type Capabilities struct {
	// AutoPromote: every mg command opportunistically promotes elapsed
	// snoozes before it runs. That is one ReadDir on a local disk; on a shared
	// backend it would make every read a potential write from every
	// collaborator at once, so such a backend turns it off and runs one
	// sweeper (`mg schedule`) instead. The filesystem backend keeps it on.
	AutoPromote bool
}

// EventsInfo describes the event log for cheap change detection (mg flow
// --live) and for "is there a log at all" checks.
type EventsInfo struct {
	Exists  bool
	Size    int64
	ModTime time.Time
	// Where names the log for humans ("no events found at <Where>").
	Where string
}

// EventLog is the backend's append-only audit log: one JSON object per line.
// mg marshals each event; the backend only stores and returns lines.
type EventLog interface {
	// AppendEvent appends one line. line carries its trailing newline.
	AppendEvent(line []byte) error
	// ReadEvents returns the whole log, oldest first, as stored. A log that
	// does not exist yet returns (nil, nil).
	ReadEvents() ([]byte, error)
	// StatEvents describes the log without reading it.
	StatEvents() (EventsInfo, error)
}

// Backend is a work-item store.
type Backend interface {
	// Create stores a new item in state st. It returns an error wrapping
	// ErrExists when id already names any item in the store, archive
	// included, and must never overwrite one; that refusal is what makes
	// mg's short ids safe to mint.
	Create(id string, st State, content []byte, by Actor) error

	// Get returns the one item named id. ErrNotFound when there is none;
	// ErrAmbiguous when there is more than one.
	Get(id string) (*Doc, error)

	// List returns every item currently in status, in no particular order.
	// Archived items are listed across all partitions.
	List(status Status) ([]Doc, error)

	// Transition moves id from expect to to, atomically. If the item is not
	// in a state satisfying expect at the instant of the move, nothing moves
	// and the error is a *ErrLost. Of two concurrent Transitions expecting
	// the same state, exactly one succeeds.
	//
	// Transition is the ONLY way an item's state changes.
	Transition(id string, expect, to State, by Actor) error

	// Edit replaces id's content, keeping its state, provided the stored
	// content still hashes to ifUnchanged (an empty ifUnchanged skips the
	// check). A changed item refuses with *ErrChanged; an item that is gone
	// refuses with *ErrLost. Edit must never recreate an item at a state it
	// has left: a Transition that lands mid-Edit wins, and the Edit either
	// follows the item or refuses.
	//
	// Edit can never change state, whatever content says: a backend that
	// keeps state inside the stored document (a "state:" line) must preserve
	// it across an Edit, and state changes go only through Transition.
	Edit(id string, ifUnchanged Hash, content []byte, by Actor) error

	// Archive moves a done item into the archive partition (e.g. "2026-11").
	// It refuses with *ErrLost when the item is not done, and wraps
	// ErrExists when the partition already holds a record with that id.
	Archive(id string, partition string, by Actor) error

	// Events is the audit log hook.
	Events() EventLog

	// Capabilities reports which per-command mg behaviours this backend
	// allows.
	Capabilities() Capabilities
}

// Opener opens the backend for an mg workspace root (the directory --root or
// MG_ROOT resolves to). mgcli.Main takes one so the backend is opened for
// whatever root the invocation names.
type Opener func(root string) (Backend, error)

// The interfaces below are OPTIONAL extensions. mg type-asserts for them and
// degrades as documented when a backend does not provide one. None of them is
// needed for mg's lifecycle (new, claim, done, edit, archive) to work.

// Twins is implemented by a backend that can hold more than one record under
// one id (the legacy same-id twins that predate globally-unique ids). GetAll
// returns every record id names, in a stable order; it returns an empty slice,
// not ErrNotFound, when there are none. mg resolves a twin set itself (a live
// record shadows an archived one; "id@partition" selects an archived twin).
//
// A backend without it is taken to hold at most one record per id, and mg
// resolves through Get.
type Twins interface {
	GetAll(id string) ([]Doc, error)
}

// IDLister is implemented by a backend that can enumerate the ids in a status
// without reading every document. mg asks it for bulk "is this a real work
// item" questions (mail lists ~1400 mailboxes against a ~4000-record
// archive). A backend without it is enumerated through List.
type IDLister interface {
	IDs(status Status) ([]string, error)
}

// Unarchiver is implemented by a backend that can bring an archived record
// back into an active state. It must refuse (wrapping ErrExists) rather than
// overwrite when to's status already holds a record with the id, and refuse
// with *ErrLost when the item is not archived in partition. A backend without
// it cannot unarchive, and `mg unarchive` says so.
type Unarchiver interface {
	Unarchive(id string, partition string, to State, by Actor) error
}

// Local is implemented by a backend whose items are files on this machine
// (fsstore). mg keeps a few COMPANION files beside an item — the result
// sidecar <id>.result.json, saved prior bodies, the snooze sweep stamp — and
// those are local files by nature. StatusDir names the directory holding items
// of status st (for Archived, the archive root; partitions are its
// subdirectories); WorkDir names the directory holding every status.
//
// A backend that is not Local has no companion files: mg's result sidecar,
// body backups and sweep stamp are unavailable on it and the commands that
// need them say so. Moving those behind the seam is a follow-up, not part of
// the Backend contract.
type Local interface {
	StatusDir(st Status) string
	WorkDir() string
	// MoveCompanion renames a companion file (a saved body travelling into an
	// archive partition). It is the backend's, not mg's, so every rename on a
	// store path stays inside the backend.
	MoveCompanion(src, dst string) error
}
