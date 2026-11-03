package fsstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drellem2/macguffin/store"
)

// FS is the filesystem store.Backend rooted at an mg workspace.
type FS struct {
	Root string
}

var (
	_ store.Backend    = (*FS)(nil)
	_ store.Twins      = (*FS)(nil)
	_ store.IDLister   = (*FS)(nil)
	_ store.Unarchiver = (*FS)(nil)
	_ store.Local      = (*FS)(nil)
)

// Open is the store.Opener for the filesystem backend. It touches nothing on
// disk: opening a store that `mg init` has not created yet is not an error,
// because the commands that need one report that themselves, better.
func Open(root string) (store.Backend, error) {
	return &FS{Root: root}, nil
}

// StatusDir is the directory holding items of status st (archive partitions
// live under StatusDir(root, store.Archived)).
func StatusDir(root string, st store.Status) string {
	if st == store.Archived {
		return filepath.Join(root, "work", "archive") // the one status whose directory is not its name
	}
	return filepath.Join(root, "work", string(st))
}

// FileName is the file name an item takes in state st: <id>.md, or
// <id>.md.<holder> for a held claim.
func FileName(id string, st store.State) string {
	if st.Status == store.Claimed && st.Holder != "" {
		return id + ".md." + st.Holder
	}
	return id + ".md"
}

// ResultSidecarName is the file name of an item's result sidecar.
func ResultSidecarName(id string) string { return id + ".result.json" }

// MoveSidecar carries id's result sidecar, if it has one, from srcDir to
// dstDir. The sidecar is a companion to the .md and must travel with it across
// every transition; one left behind is an orphan asserting a completion for an
// item that has moved on (mg-ab67). It is a no-op when there is no sidecar.
// dstDir is created if needed.
func MoveSidecar(srcDir, dstDir, id string) error {
	src := filepath.Join(srcDir, ResultSidecarName(id))
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	return MoveFile(src, filepath.Join(dstDir, ResultSidecarName(id)))
}

// withHolder fills an unset claim holder from the actor's PID: on the
// filesystem a claim's holder IS the owner PID (claimed/<id>.md.<pid>), and
// the rest of the Actor has nowhere to live without changing the on-disk
// layout, which this backend does not do.
func withHolder(st store.State, by store.Actor) store.State {
	if st.Status == store.Claimed && st.Holder == "" && by.PID != 0 {
		st.Holder = strconv.Itoa(by.PID)
	}
	return st
}

// current is the state id is observed in right now, for an ErrLost. nil when
// the item cannot be found, or cannot be pinned to one location.
func (b *FS) current(id string) *store.State {
	locs := Locate(b.Root, id)
	if len(locs) != 1 {
		return nil
	}
	st := stateOf(locs[0], id)
	return &st
}

func (b *FS) lost(id string, expect store.State) error {
	return &store.ErrLost{ID: id, Expect: expect, Current: b.current(id)}
}

// Create implements store.Backend: the store-wide collision scan, then an
// O_EXCL create. Either refusal wraps store.ErrExists.
func (b *FS) Create(id string, st store.State, content []byte, by store.Actor) error {
	st = withHolder(st, by)
	if st.Status == store.Archived {
		return fmt.Errorf("fsstore: %s: items are archived with Archive, not created archived", id)
	}
	if len(Locate(b.Root, id)) > 0 {
		return fmt.Errorf("%s: %w", id, store.ErrExists)
	}
	path := filepath.Join(StatusDir(b.Root, st.Status), FileName(id, st))
	if err := CreateExclusive(path, content); err != nil {
		if isExist(err) {
			return fmt.Errorf("%s: %w", id, store.ErrExists)
		}
		return err
	}
	return nil
}

// Get implements store.Backend.
func (b *FS) Get(id string) (*store.Doc, error) {
	locs := Locate(b.Root, id)
	switch len(locs) {
	case 0:
		return nil, fmt.Errorf("%s: %w", id, store.ErrNotFound)
	case 1:
	default:
		return nil, fmt.Errorf("%s: %d records: %w", id, len(locs), store.ErrAmbiguous)
	}
	return b.read(id, locs[0])
}

func (b *FS) read(id string, l Location) (*store.Doc, error) {
	data, err := os.ReadFile(l.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", id, store.ErrNotFound)
		}
		return nil, err
	}
	doc := &store.Doc{
		ID:        id,
		State:     stateOf(l, id),
		Partition: l.Partition,
		Content:   data,
		Hash:      store.HashOf(data),
		Where:     l.Path,
	}
	if info, err := os.Stat(l.Path); err == nil {
		doc.Mtime = info.ModTime()
	}
	return doc, nil
}

// idOf extracts the item id from an item file name (<id>.md or
// <id>.md.<holder>); ok is false for anything else (sidecars, backups).
func idOf(name string, st store.Status) (string, bool) {
	if id, ok := strings.CutSuffix(name, ".md"); ok && id != "" {
		return id, true
	}
	if st == store.Claimed {
		if id, _, ok := strings.Cut(name, ".md."); ok && id != "" {
			return id, true
		}
	}
	return "", false
}

// List implements store.Backend.
func (b *FS) List(status store.Status) ([]store.Doc, error) {
	var locs []Location
	var ids []string
	add := func(dir string, st store.Status, partition string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			id, ok := idOf(e.Name(), st)
			if !ok {
				continue
			}
			locs = append(locs, Location{Path: filepath.Join(dir, e.Name()), Status: st, Partition: partition})
			ids = append(ids, id)
		}
		return nil
	}
	if status == store.Archived {
		archiveRoot := StatusDir(b.Root, store.Archived)
		if err := add(archiveRoot, store.Archived, ""); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(archiveRoot)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				if err := add(filepath.Join(archiveRoot, e.Name()), store.Archived, e.Name()); err != nil {
					return nil, err
				}
			}
		}
	} else if err := add(StatusDir(b.Root, status), status, ""); err != nil {
		return nil, err
	}

	docs := make([]store.Doc, 0, len(locs))
	for i, l := range locs {
		doc, err := b.read(ids[i], l)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue // moved since the ReadDir; it is listed where it landed, or not at all
			}
			return nil, err
		}
		docs = append(docs, *doc)
	}
	return docs, nil
}

// find returns the one file for id in expect's directory that satisfies
// expect, or a *store.ErrLost.
func (b *FS) find(id string, expect store.State) (string, error) {
	dir := StatusDir(b.Root, expect.Status)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if !MatchesID(e.Name(), id) {
			continue
		}
		st := stateOf(Location{Path: e.Name(), Status: expect.Status}, id)
		if st.Satisfies(expect) {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	switch len(found) {
	case 0:
		return "", b.lost(id, expect)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%s: %d records in %s: %w", id, len(found), expect.Status, store.ErrAmbiguous)
	}
}

// Transition implements store.Backend: one rename(2), then the sidecar
// follows. Of two racing Transitions out of one state, the loser's rename gets
// ENOENT and is reported as *store.ErrLost.
func (b *FS) Transition(id string, expect, to store.State, by store.Actor) error {
	to = withHolder(to, by)
	if expect.Status == store.Archived || to.Status == store.Archived {
		return fmt.Errorf("fsstore: %s: archive moves go through Archive, not Transition", id)
	}
	src, err := b.find(id, expect)
	if err != nil {
		return err
	}
	dst := filepath.Join(StatusDir(b.Root, to.Status), FileName(id, to))
	if src == dst {
		return nil
	}
	// Never REPLACE a record: rename(2) over an existing destination is a
	// silent delete, and a legacy same-id twin can sit exactly there. This is
	// a check, not part of the atomic step — the race it leaves (a twin
	// created at dst in the microseconds before the rename) needs a same-id
	// create, which Create's store-wide scan already refuses.
	if _, err := os.Lstat(dst); err == nil {
		if _, err := os.Lstat(src); os.IsNotExist(err) {
			return b.lost(id, expect) // a racer moved it there first
		}
		return fmt.Errorf("%s: %s already holds a record: %w", id, to.Status, store.ErrExists)
	}
	if err := MoveItem(src, dst); err != nil {
		if os.IsNotExist(err) {
			return b.lost(id, expect)
		}
		return err
	}
	if filepath.Dir(src) != filepath.Dir(dst) {
		if err := MoveSidecar(filepath.Dir(src), filepath.Dir(dst), id); err != nil {
			return fmt.Errorf("%s: moved, but result sidecar could not follow: %w", id, err)
		}
	}
	return nil
}

// Edit implements store.Backend through RewriteExisting: no O_CREATE, so an
// item a concurrent Transition moved away is followed (when the move lands
// after the open) or refused as lost (when it lands before) — never recreated.
//
// With legacy same-id twins, ifUnchanged picks the record: the one whose
// stored bytes it hashes — the record the caller read. Without a hash, or
// when it matches none or several, the edit is refused.
func (b *FS) Edit(id string, ifUnchanged store.Hash, content []byte, _ store.Actor) error {
	locs := Locate(b.Root, id)
	switch len(locs) {
	case 0:
		return fmt.Errorf("%s: %w", id, store.ErrNotFound)
	case 1:
	default:
		var picked []Location
		if ifUnchanged != "" {
			for _, l := range locs {
				if data, err := os.ReadFile(l.Path); err == nil && store.HashOf(data) == ifUnchanged {
					picked = append(picked, l)
				}
			}
		}
		if len(picked) != 1 {
			return fmt.Errorf("%s: %d records: %w", id, len(locs), store.ErrAmbiguous)
		}
		locs = picked
	}
	seen := stateOf(locs[0], id)
	if err := RewriteExisting(locs[0].Path, ifUnchanged, content); err != nil {
		var ch *store.ErrChanged
		if errors.As(err, &ch) {
			ch.ID = id
			return ch
		}
		if os.IsNotExist(err) {
			return b.lost(id, seen)
		}
		return err
	}
	return nil
}

// Archive implements store.Backend: MoveNoReplace into the partition, so a
// same-id record already there is refused rather than destroyed (mg-1096).
func (b *FS) Archive(id string, partition string, _ store.Actor) error {
	expect := store.State{Status: store.Done}
	src, err := b.find(id, expect)
	if err != nil {
		return err
	}
	dir := filepath.Join(StatusDir(b.Root, store.Archived), partition)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(src))
	if err := MoveNoReplace(src, dst); err != nil {
		if isExist(err) {
			return fmt.Errorf("%s: partition %s: %w", id, partition, store.ErrExists)
		}
		if os.IsNotExist(err) {
			return b.lost(id, expect)
		}
		return err
	}
	if err := MoveSidecar(filepath.Dir(src), dir, id); err != nil {
		return fmt.Errorf("%s: archived, but result sidecar could not follow: %w", id, err)
	}
	return nil
}

// GetAll implements store.Twins: every record id names, in Locate's order.
//
// A record renamed between the scan and its read (a concurrent claim or
// re-stamp) is not dropped: the scan is repeated, so an item that exists
// throughout is never reported as missing because it was moving.
func (b *FS) GetAll(id string) ([]store.Doc, error) {
	const rescans = 5
	for attempt := 1; ; attempt++ {
		var docs []store.Doc
		moved := false
		for _, l := range Locate(b.Root, id) {
			d, err := b.read(id, l)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					moved = true
					continue
				}
				return nil, err
			}
			docs = append(docs, *d)
		}
		if !moved || attempt == rescans {
			return docs, nil
		}
	}
}

// IDs implements store.IDLister from directory entries alone — no item is
// opened. Names match exactly as Locate matches them: "<id>.md", the claimed
// "<id>.md.<holder>", and under archive partitions the same; loose entries
// directly under work/archive/ only as "<id>.md" (MatchesArchivedID).
func (b *FS) IDs(status store.Status) ([]string, error) {
	var ids []string
	collect := func(dir string, strict bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strict {
				if id, ok := strings.CutSuffix(name, ".md"); ok && id != "" {
					ids = append(ids, id)
				}
				continue
			}
			if idx := strings.Index(name, ".md"); idx > 0 {
				if rest := name[idx:]; rest == ".md" || strings.HasPrefix(rest, ".md.") {
					ids = append(ids, name[:idx])
				}
			}
		}
	}
	if status != store.Archived {
		collect(StatusDir(b.Root, status), false)
		return ids, nil
	}
	archiveRoot := StatusDir(b.Root, store.Archived)
	collect(archiveRoot, true)
	entries, err := os.ReadDir(archiveRoot)
	if err != nil {
		return ids, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			collect(filepath.Join(archiveRoot, e.Name()), false)
		}
	}
	return ids, nil
}

// Unarchive implements store.Unarchiver: MoveNoReplace out of the partition
// ("" = a loose record directly under work/archive/), so a same-id record
// already in to's directory is refused rather than destroyed (mg-1096). The
// result sidecar follows.
func (b *FS) Unarchive(id string, partition string, to store.State, by store.Actor) error {
	to = withHolder(to, by)
	expect := store.State{Status: store.Archived}
	if to.Status == store.Archived {
		return fmt.Errorf("fsstore: %s: cannot unarchive into the archive", id)
	}
	dir := filepath.Join(StatusDir(b.Root, store.Archived), partition)
	src := filepath.Join(dir, FileName(id, expect))
	if _, err := os.Lstat(src); err != nil {
		if os.IsNotExist(err) {
			return b.lost(id, expect)
		}
		return err
	}
	dstDir := StatusDir(b.Root, to.Status)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	if err := MoveNoReplace(src, filepath.Join(dstDir, FileName(id, to))); err != nil {
		if isExist(err) {
			return fmt.Errorf("%s: %s: %w", id, to.Status, store.ErrExists)
		}
		if os.IsNotExist(err) {
			return b.lost(id, expect)
		}
		return err
	}
	if err := MoveSidecar(dir, dstDir, id); err != nil {
		return fmt.Errorf("%s: unarchived, but result sidecar could not follow: %w", id, err)
	}
	return nil
}

// StatusDir implements store.Local.
func (b *FS) StatusDir(st store.Status) string { return StatusDir(b.Root, st) }

// WorkDir implements store.Local.
func (b *FS) WorkDir() string { return filepath.Join(b.Root, "work") }

// MoveCompanion implements store.Local: rename(2) of a companion file.
func (b *FS) MoveCompanion(src, dst string) error { return MoveFile(src, dst) }

// Events implements store.Backend.
func (b *FS) Events() store.EventLog { return EventLog(b.Root) }

// Capabilities implements store.Backend. The filesystem keeps every
// per-command behaviour mg has always had.
func (b *FS) Capabilities() store.Capabilities {
	return store.Capabilities{AutoPromote: true}
}
