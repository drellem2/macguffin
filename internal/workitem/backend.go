package workitem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
)

// This file is the ONLY place internal/workitem gets at storage. Every item
// document is created, read, listed, moved, rewritten and archived through the
// store.Backend that backendFor returns for the workspace root — never through
// a path. The guard test (backendguard_test.go) holds the rest of the package
// to that.
//
// The backend is installed, not chosen here: mgcli.Main hands SetOpener the
// store.Opener it was built with, exactly as it hands the event hook to
// event.SetLogOpener. The one reference to the filesystem backend below is
// the default for callers that never install one (this package's own tests,
// internal/flow's) — it is fsstore.Open, an Opener like any other, so even the
// default is reached through store.Backend and nothing else.

var (
	backendMu    sync.Mutex
	opener       store.Opener = fsstore.Open
	backendCache              = map[string]backendEntry{}
	actorFunc    func() store.Actor
)

type backendEntry struct {
	b   store.Backend
	err error
}

// SetOpener installs the storage backend every function in this package uses.
// nil restores the filesystem default. Opened backends are cached per root.
func SetOpener(open store.Opener) {
	backendMu.Lock()
	defer backendMu.Unlock()
	if open == nil {
		open = fsstore.Open
	}
	opener = open
	backendCache = map[string]backendEntry{}
}

// SetActor installs the function that builds the store.Actor for this
// process's writes. The CLI edge (mgcli) builds it — attributed name, host,
// PID, nonce — so the identity a backend records is decided where the
// invocation is, not reconstructed down here. nil restores defaultActor.
func SetActor(f func() store.Actor) {
	backendMu.Lock()
	actorFunc = f
	backendMu.Unlock()
}

// backendFor returns the backend for root, opening it on first use.
func backendFor(root string) (store.Backend, error) {
	backendMu.Lock()
	defer backendMu.Unlock()
	if e, ok := backendCache[root]; ok {
		return e.b, e.err
	}
	b, err := opener(root)
	if err == nil && b == nil {
		err = errors.New("storage backend opener returned no backend")
	}
	backendCache[root] = backendEntry{b, err}
	return b, err
}

// be is backendFor for call sites that turn a failure into mg's internal
// error category.
func be(root string) (store.Backend, error) {
	b, err := backendFor(root)
	if err != nil {
		return nil, ioErr(fmt.Sprintf("could not open the work-item store: %v", err))
	}
	return b, nil
}

// writer is the Actor for a write. pid, when non-zero, overrides the
// installed actor's PID: it is `mg claim --pid`, pogod claiming on a
// polecat's behalf, already decided at the CLI edge and passed down.
func writer(pid int) store.Actor {
	backendMu.Lock()
	f := actorFunc
	backendMu.Unlock()
	var a store.Actor
	if f != nil {
		a = f()
	} else {
		a = defaultActor()
	}
	if pid != 0 {
		a.PID = pid
	}
	return a
}

// unheld is the Actor for a transition into a claim that must record NO
// holder (`mg reopen`, an unshelve back to claimed without --pid): the
// filesystem backend derives the claimed/<id>.md.<pid> suffix from the Actor's
// PID, and a reopened claim has always been a bare claimed/<id>.md.
func unheld() store.Actor {
	a := writer(0)
	a.PID = 0
	return a
}

// defaultActor is the Actor when the CLI edge installed none: the same
// attributed name as the audit log, this host, this process.
func defaultActor() store.Actor {
	host, _ := os.Hostname()
	pid := os.Getpid()
	return store.Actor{Name: actor(), Host: host, PID: pid, Nonce: strconv.Itoa(pid)}
}

// companionsOf is the backend's companion store for root: the result
// sidecars, saved bodies and sweep stamp. Every backend has one.
func companionsOf(root string) (store.Companions, error) {
	b, err := be(root)
	if err != nil {
		return nil, err
	}
	return b.Companions(), nil
}

// InitStore prepares root's store for first use (`mg init`): the backend's
// own layout when it has one (store.Initializer), nothing otherwise.
func InitStore(root string) error {
	b, err := be(root)
	if err != nil {
		return err
	}
	if i, ok := b.(store.Initializer); ok {
		return i.Init()
	}
	return nil
}

// refOf is the store.Ref of the record a Match names.
func refOf(m Match) store.Ref {
	return store.Ref{ID: m.ID, Status: store.Status(m.Status), Partition: m.Partition}
}

// statusLabel names the directory of status st for a human message, on a
// backend that has one (store.Local); "" otherwise. Nothing reads or writes
// through it.
func statusLabel(root string, st string) string {
	b, err := backendFor(root)
	if err != nil {
		return ""
	}
	l, ok := b.(store.Local)
	if !ok {
		return ""
	}
	return l.StatusDir(store.Status(st))
}

// where is a doc's human-readable location: the backend's own answer, or
// "<status>/<id>" when it has none.
func where(d *store.Doc) string {
	if d.Where != "" {
		return d.Where
	}
	if d.Partition != "" {
		return filepath.Join(string(d.State.Status), d.Partition, d.ID)
	}
	return filepath.Join(string(d.State.Status), d.ID)
}

// itemOf parses a stored doc into an Item.
func itemOf(d *store.Doc) (*Item, error) {
	item, err := Parse(string(d.Content))
	if err != nil {
		return nil, err
	}
	item.Mtime = d.Mtime
	return item, nil
}

// getDoc reads the one record id names. A missing item is (nil, nil) so a
// caller can tell "gone" from "broken".
func getDoc(root, id string) (*store.Doc, error) {
	b, err := be(root)
	if err != nil {
		return nil, err
	}
	d, err := b.Get(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// readMatch parses the record a Match names, as Resolve read it.
func readMatch(m Match) (*Item, error) {
	if m.doc == nil {
		return nil, errNoSuchItem(m.ID)
	}
	return itemOf(m.doc)
}

// readID reads and parses the one item id names, through Resolve so a live
// item shadowing an archived twin still reads.
func readID(root, id string) (*Item, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	return readMatch(m)
}

// listDocs lists status through the backend.
func listDocs(root string, st store.Status) ([]store.Doc, error) {
	b, err := be(root)
	if err != nil {
		return nil, err
	}
	return b.List(st)
}

// transition is Backend.Transition with mg's state names.
func transition(root, id string, expect, to store.State, by store.Actor) error {
	b, err := be(root)
	if err != nil {
		return err
	}
	return b.Transition(id, expect, to, by)
}

// isLost reports a Transition/Edit refusal because the item was not where the
// caller expected it.
func isLost(err error) bool {
	var lost *store.ErrLost
	return errors.As(err, &lost) || errors.Is(err, store.ErrNotFound)
}

// inStatus builds a store.State from mg's status string.
func inStatus(status string) store.State { return store.State{Status: store.Status(status)} }

// claimedBy is the claimed state held by holder ("" = any holder).
func claimedBy(holder string) store.State {
	return store.State{Status: store.Claimed, Holder: holder}
}

// holderPID is a claim holder as a PID: the filesystem backend's holder IS the
// owner PID. 0 when the holder is absent or is not a PID (a shared backend's
// user name).
func holderPID(holder string) int {
	if holder == "" {
		return 0
	}
	return parseClaimPID("." + holder)
}

// readIn reads id's record in status, for a caller that has just moved it
// there. It resolves silently — ResolveUnique already told the operator about
// any shadowed twin, once — and picks the record in status from the twin set.
func readIn(root, id string, status store.Status) (*Item, error) {
	m, err := matchIn(root, id, status)
	if err != nil {
		return nil, err
	}
	return readMatch(m)
}

// matchIn is readIn's Match.
func matchIn(root, id string, status store.Status) (Match, error) {
	matches, err := Resolve(root, id)
	if err != nil {
		return Match{}, err
	}
	var found []Match
	for _, m := range matches {
		if m.Status == string(status) {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Match{}, errNoSuchItem(id)
	default:
		return Match{}, errAmbiguousID(root, id, found)
	}
}

// rmwBeforeWrite is a test seam: when set, every read-modify-write below calls
// it with the site's name between its read and its write, so a race test can
// land a concurrent claim (or edit) in exactly that window. See rmwrace_test.go.
var rmwBeforeWrite func(site string)

// modifyItem is mg's one read-modify-write of an item document.
//
// apply mutates the parsed item and reports whether it changed anything; an
// unchanged item writes nothing. The write is Backend.Edit guarded by the hash
// of the bytes apply was given, so it lands only on the record that was read:
// never a re-creation of a path the item has left (the os.WriteFile shape this
// replaced, whose O_CREATE resurrected an item a claim had just moved away),
// and never over a concurrent edit.
//
// When the guard refuses, the whole read-modify-write runs again against the
// item as it now is, up to maxEditAttempts times. stay names the status the
// item must still be in for that retry to make sense (a snooze written onto an
// available item before it moves to pending is wrong once someone has claimed
// it); "" follows the item wherever it went. An item that left stay — or is
// gone — is a lost race: nothing is written, and errRaced says so.
func modifyItem(root string, m Match, site, stay string, apply func(*Item) (bool, error)) (*Item, Match, error) {
	b, err := be(root)
	if err != nil {
		return nil, m, err
	}
	for attempt := 1; ; attempt++ {
		item, err := readMatch(m)
		if err != nil {
			return nil, m, err
		}
		changed, err := apply(item)
		if err != nil || !changed {
			return item, m, err
		}
		if rmwBeforeWrite != nil {
			rmwBeforeWrite(site)
		}
		err = b.Edit(m.ID, m.doc.Hash, []byte(Render(item)), writer(0))
		if err == nil {
			// Reflect what was stored, so a caller that goes on to move the
			// item expects the state it was written in.
			content := []byte(Render(item))
			d := *m.doc
			d.Content, d.Hash = content, store.HashOf(content)
			m.doc = &d
			return item, m, nil
		}
		var changedErr *store.ErrChanged
		if !errors.As(err, &changedErr) && !isLost(err) {
			return nil, m, ioErr(fmt.Sprintf("%s: could not be saved: %s", m.ID, fsErrText(err)))
		}
		if attempt == maxEditAttempts {
			return nil, m, errRaced(m.ID, attempt)
		}
		next, rerr := rematch(root, m.ID, stay)
		if rerr != nil {
			return nil, m, errRaced(m.ID, attempt)
		}
		m = next
	}
}

// rematch re-resolves id for a retried read-modify-write: in status stay, or
// wherever the single live-or-only record now is when stay is "".
func rematch(root, id, stay string) (Match, error) {
	if stay != "" {
		return matchIn(root, id, store.Status(stay))
	}
	return resolveUnique(root, id, false)
}

// errRaced is a read-modify-write that lost to a concurrent writer and wrote
// nothing.
func errRaced(id string, attempts int) error {
	return mgerr.Conflict("edit_raced",
		fmt.Sprintf("%s: the item changed or moved while it was being written (%d attempts). Nothing was written by the last attempt.", id, attempts),
		fmt.Sprintf("Re-run the command; 'mg show %s' shows where the item is now.", id)).WithRetryable(true)
}
