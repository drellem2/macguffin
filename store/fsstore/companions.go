package fsstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drellem2/macguffin/store"
)

// The companion files, exactly where they have always been:
//
//	<root>/work/<status>/<id>.result.json                 result sidecar, beside the .md
//	<root>/work/archive/<partition>/<id>.result.json      ... of an archived record
//	<root>/work/.bodybak/<id>/<name>                      saved bodies of a live item
//	<root>/work/archive/<partition>/.bodybak/<id>/<name>  ... of an archived one
//	<root>/work/.last-sweep                               the sweep stamp
//
// Every path to one is built here and nowhere else (internal/workitem's guard
// test refuses a companion name outside this package).

// BodyBackupDirName is the directory saved bodies live under. The leading dot
// keeps it out of anything globbing work/*/ for items or sidecars.
const BodyBackupDirName = ".bodybak"

// SweepStampName is the file `mg schedule` stamps on every run, in work/.
const SweepStampName = ".last-sweep"

// BodyBackupParent is work/.bodybak/, the parent of one directory per live
// item's saved bodies.
func BodyBackupParent(root string) string {
	return filepath.Join(root, "work", BodyBackupDirName)
}

// Companions implements store.Backend.
func (b *FS) Companions() store.Companions { return companions{root: b.Root} }

type companions struct{ root string }

var _ store.Companions = companions{}

// recordDir is the directory ref's record sits in.
func (c companions) recordDir(ref store.Ref) string {
	d := StatusDir(c.root, ref.Status)
	if ref.Status == store.Archived && ref.Partition != "" {
		d = filepath.Join(d, ref.Partition)
	}
	return d
}

func notFound(what string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("%s: %w", what, store.ErrNotFound)
	}
	return err
}

func (c companions) ResultWhere(ref store.Ref) string {
	return filepath.Join(c.recordDir(ref), ResultSidecarName(ref.ID))
}

func (c companions) ReadResult(ref store.Ref) ([]byte, error) {
	path := c.ResultWhere(ref)
	info, err := os.Stat(path)
	if err != nil {
		return nil, notFound(path, err)
	}
	if info.IsDir() {
		// A directory where the sidecar belongs is a broken store, not an
		// absent result.
		return nil, fmt.Errorf("%s is a directory, not a result sidecar", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, notFound(path, err)
	}
	return data, nil
}

func (c companions) WriteResult(ref store.Ref, data []byte) error {
	return os.WriteFile(c.ResultWhere(ref), data, 0o644)
}

// Results scans the active status directories, the loose archive root and
// every archive partition — the surface Locate covers, so a stray cannot hide
// in a corner the resolver can see. Unreadable directories are skipped.
func (c companions) Results() ([]store.Ref, error) {
	type dir struct {
		path string
		st   store.Status
		part string
	}
	dirs := []dir{}
	for _, st := range store.ActiveStatuses {
		dirs = append(dirs, dir{StatusDir(c.root, st), st, ""})
	}
	archive := StatusDir(c.root, store.Archived)
	dirs = append(dirs, dir{archive, store.Archived, ""})
	if entries, err := os.ReadDir(archive); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, dir{filepath.Join(archive, e.Name()), store.Archived, e.Name()})
			}
		}
	}
	var out []store.Ref
	for _, d := range dirs {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), resultSuffix) {
				continue
			}
			out = append(out, store.Ref{ID: strings.TrimSuffix(e.Name(), resultSuffix), Status: d.st, Partition: d.part})
		}
	}
	return out, nil
}

// BodiesWhere: one directory per item for every live status (a saved body
// asserts nothing about status, so it does not move on claim/done/shelve),
// and one inside the partition for an archived record.
func (c companions) BodiesWhere(ref store.Ref) string {
	if ref.Status == store.Archived {
		return filepath.Join(c.recordDir(ref), BodyBackupDirName, ref.ID)
	}
	return filepath.Join(BodyBackupParent(c.root), ref.ID)
}

func (c companions) SaveBody(ref store.Ref, name string, body []byte) error {
	dir := c.BodiesWhere(ref)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), body, 0o644)
}

func (c companions) Bodies(ref store.Ref) ([]string, error) {
	entries, err := os.ReadDir(c.BodiesWhere(ref))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (c companions) ReadBody(ref store.Ref, name string) ([]byte, error) {
	path := filepath.Join(c.BodiesWhere(ref), name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, notFound(path, err)
	}
	return data, nil
}

func (c companions) RemoveBody(ref store.Ref, name string) error {
	if err := os.Remove(filepath.Join(c.BodiesWhere(ref), name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MoveBodies moves file by file rather than renaming the directory: the
// destination can already exist (two archived twins of one id can share a
// partition), and a rename onto a non-empty directory fails on every Unix.
func (c companions) MoveBodies(from, to store.Ref) error {
	src, dst := c.BodiesWhere(from), c.BodiesWhere(to)
	if src == dst {
		return nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := MoveFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	// Best effort: succeeds only once the directory is empty, and a leftover
	// empty directory is not worth failing a completed archive over.
	_ = os.Remove(src)
	return nil
}

func (c companions) sweepStampPath() string {
	return filepath.Join(c.root, "work", SweepStampName)
}

func (c companions) ReadSweepStamp() ([]byte, error) {
	path := c.sweepStampPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, notFound(path, err)
	}
	return data, nil
}

func (c companions) WriteSweepStamp(data []byte) error {
	return os.WriteFile(c.sweepStampPath(), data, 0o644)
}

// Init implements store.Initializer: the status directories `mg init` has
// always laid out.
func (b *FS) Init() error {
	for _, st := range []store.Status{store.Available, store.Claimed, store.Done, store.Pending, store.Archived, store.Shelved} {
		d := StatusDir(b.Root, st)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", d, err)
		}
	}
	return nil
}
