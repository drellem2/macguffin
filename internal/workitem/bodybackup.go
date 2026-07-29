package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// BODY BACKUPS — the recovery half of the record a replace-mode edit leaves.
//
// mg already recorded enough to PROVE a body was destroyed and not enough to
// restore it. `work.edited` carries body_hash_before, body_hash_after and the
// line counts, so events.jsonl can say "149 lines became 4, and the 149 hashed
// to bd64582b" — and every one of those facts is useless for getting the 149
// lines back. The store is not a git repo, so there is no VCS fallback either.
//
// This file adds the missing half: before a replace-mode edit overwrites a
// body, the prior body is written to a file. That is all it does.
//
// WHY A BACKUP AND NOT A GUARD. The tempting fix for mg-9fc8's incident is a
// predictive block — refuse a replace that shrinks the body by >90%, or whose
// replacement text looks like an error message. Do not build that:
//
//   - A shrink ratio hard-codes a fact about normal edits and decays. A
//     legitimate rewrite that condenses a bloated body gets refused, which
//     trains people to reach for --force, and a --force people reach for by
//     reflex is not a control.
//   - Content-sniffing for "Error:" fails on any item that legitimately quotes
//     an error message. mg-9fc8's own body would trip it.
//
// A blocking control has to be right about the FUTURE. A backup only has to be
// CHEAP, and it is correct for every failure mode including the ones nobody
// predicted: a wrong path, a truncated pipe, an editor crash, a bad sed. That
// asymmetry is the whole argument — a false block costs a real edit and erodes
// trust in the guard, while a useless backup costs a few KB.
//
// WHAT THIS DOES NOT COVER. Only mode=replace. --append-body-file is not a
// replace: it composes against the body on disk at write time and cannot
// destroy a section it never saw, so it is already safe and is deliberately not
// backed up. Nor is a --title edit, which rewrites the "# heading" line in place
// and leaves every other byte alone. This is about the wholesale overwrite path
// and nothing else.
//
// NOR IS IT THE ANSWER TO A LOST UPDATE. --if-unchanged proves no one ELSE
// wrote between your read and your write; it says nothing about whether your
// own read succeeded, which is the end mg-9fc8's corruption entered from. The
// two are complementary and neither subsumes the other.

// bodyBackupDirName is the directory backups live under. The leading dot keeps
// it out of the way of anything globbing work/*/ for items or sidecars, and it
// is not a lifecycle state: nothing in activeStates names it, so Resolve and
// FindStraySidecars never descend into it.
const bodyBackupDirName = ".bodybak"

// bodyBackupKeep is how many prior bodies are retained per item. Ten covers the
// read-modify-write loops that actually happen (an agent rewriting one item a
// handful of times in a turn) and bounds the store: a body is a few KB, so the
// worst case for an item edited forever is tens of KB, not unbounded growth.
// The bound is enforced on every save — see pruneBodyBackups — rather than by a
// sweep somebody has to remember to run.
const bodyBackupKeep = 10

// bodyBackupStamp is the filename timestamp layout: UTC, millisecond precision,
// and lexicographically ordered so sorting names sorts by time. Sub-second
// precision is load-bearing, not decoration — a single agent turn can rewrite
// one body several times inside one second, and a second-granularity stamp
// would make "the ten most recent" a sort by hash, i.e. arbitrary. Exact
// collisions are resolved by nextBackupName, so name order IS write order.
const bodyBackupStamp = "20060102T150405.000Z"

// hashPrefixLen is how much of the body hash goes in the filename. It is an
// identifier for humans reading `ls`, not a checksum: the full hash of the
// contents is recomputable from the file itself at any time.
const hashPrefixLen = 8

// BodyBackup is one saved prior body.
type BodyBackup struct {
	ID    string    // the work item the body belongs to
	Stamp string    // filename timestamp, e.g. "20260729T161400.812Z" — what --from names
	Hash  string    // hash prefix recorded in the filename
	Path  string    // absolute path to the saved body
	Time  time.Time // Stamp parsed back to a time; zero if unparseable
	Lines int       // line count of the saved body, for the listing
	Bytes int64     // size on disk, for the listing
}

// bodyBackupDirFor returns the directory holding a given item's backups.
//
// Backups are keyed by ID and live in ONE place per item, derived from where
// the item's own .md sits:
//
//	live (available/claimed/done/pending/shelved)  work/.bodybak/<id>/
//	archived                                       work/archive/<partition>/.bodybak/<id>/
//
// A single shared directory for every live status is deliberate. The result
// sidecar has to travel with its .md because a .result.json in the wrong
// lifecycle directory ASSERTS something false about the item's status (see
// moveResultSidecar); a saved prior body asserts nothing about status, so
// pinning it to a status directory would buy nothing and cost a move on every
// one of claim/unclaim/done/reopen/shelve/unshelve — six call sites, any one of
// which could be missed, each miss orphaning the backups silently.
//
// Archive is the exception because it is the transition that means "this leaves
// the live tree". Backups follow the record into its partition so the archive
// stays self-contained and work/.bodybak/ does not accumulate directories for
// items nobody will edit again — that is where they go, and mg unarchive brings
// them back. See archiveFile and Unarchive.
func bodyBackupDirFor(root, itemPath, status, id string) string {
	if status == "archived" {
		return filepath.Join(filepath.Dir(itemPath), bodyBackupDirName, id)
	}
	return filepath.Join(root, "work", bodyBackupDirName, id)
}

// liveBodyBackupParent is work/.bodybak/ — the parent holding one directory per
// live item. Archive and Unarchive move a single child of it.
func liveBodyBackupParent(root string) string {
	return filepath.Join(root, "work", bodyBackupDirName)
}

// parseBackupName splits a backup filename into its stamp and hash prefix. The
// stamp carries no "-", so the first one is the separator. A name that does not
// fit the shape is not a backup and is ignored everywhere — that is what keeps
// a stray file dropped into the directory from being offered as a restorable
// body.
func parseBackupName(name string) (stamp, hash string, ok bool) {
	if !strings.HasSuffix(name, ".md") {
		return "", "", false
	}
	base := strings.TrimSuffix(name, ".md")
	stamp, hash, found := strings.Cut(base, "-")
	if !found || stamp == "" || hash == "" {
		return "", "", false
	}
	if _, err := time.Parse(bodyBackupStamp, stamp); err != nil {
		return "", "", false
	}
	return stamp, hash, true
}

// nextBackupName picks a filename whose stamp is unused in dir, advancing the
// clock by a millisecond at a time until it finds one.
//
// The point is not to avoid clobbering a file — two saves in the same
// millisecond with different content already get different hash prefixes. It is
// to keep the stamp UNIQUE, so that sorting names is sorting by write order.
// Without it, two backups sharing a millisecond would be ordered by hash, and
// "prune all but the ten most recent" would delete an arbitrary one.
func nextBackupName(dir string, now time.Time, hash string) string {
	used := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if stamp, _, ok := parseBackupName(e.Name()); ok {
				used[stamp] = true
			}
		}
	}
	stamp := now.UTC().Format(bodyBackupStamp)
	for used[stamp] {
		now = now.Add(time.Millisecond)
		stamp = now.UTC().Format(bodyBackupStamp)
	}
	return stamp + "-" + hash + ".md"
}

// saveBodyBackup writes body into dir under a fresh timestamped name and prunes
// the directory back to bodyBackupKeep. It returns the path written.
//
// The body is written VERBATIM — the same bytes `mg show ID --json` would have
// emitted for it — so restoring is a byte-for-byte replay and not a rendering.
func saveBodyBackup(dir, body string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := nextBackupName(dir, now, BodyHash(body)[:hashPrefixLen])
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	if err := pruneBodyBackups(dir, bodyBackupKeep); err != nil {
		return "", err
	}
	return path, nil
}

// pruneBodyBackups deletes all but the keep most recent backups in dir.
//
// It runs on every save rather than in a periodic sweep: a bound enforced by a
// sweep is a bound that holds only as often as the sweep runs, and there is no
// sweep in this store to hang it off. Files that are not backups are left
// alone — pruning is entitled to delete what this file wrote and nothing else.
func pruneBodyBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, _, ok := parseBackupName(e.Name()); ok {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil
	}
	// Newest first, then drop everything past the keep-th.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// moveBodyBackups moves <srcParent>/<id>/ into <dstParent>/<id>/, file by file,
// and prunes the destination. It is a no-op when the item has no backups, so
// callers can invoke it unconditionally after moving an .md.
//
// File-by-file rather than one directory rename because the destination can
// already exist: two archived twins of one short ID can share a partition, and
// a rename onto a non-empty directory fails on every Unix. Merging and then
// re-pruning keeps the per-item bound true after the move.
func moveBodyBackups(srcParent, dstParent, id string) error {
	src := filepath.Join(srcParent, id)
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dst := filepath.Join(dstParent, id)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	if err := pruneBodyBackups(dst, bodyBackupKeep); err != nil {
		return err
	}
	// Best effort: succeeds only once the directory is empty, and a leftover
	// empty directory is not worth failing a completed archive over.
	_ = os.Remove(src)
	return nil
}

// ListBodyBackups returns every saved prior body for an item, NEWEST FIRST.
//
// An item with no backups is an empty list and no error — "nothing has ever
// overwritten this body" is a legitimate answer to the question. Deciding that
// the absence is a problem belongs to RestoreBody, which is the caller that
// cannot proceed without one.
func ListBodyBackups(root, id string) ([]BodyBackup, error) {
	m, err := ResolveUnique(root, id)
	if err != nil {
		return nil, err
	}
	dir := bodyBackupDirFor(root, m.Path, m.Status, m.ID)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var backups []BodyBackup
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		stamp, hash, ok := parseBackupName(e.Name())
		if !ok {
			continue
		}
		b := BodyBackup{
			ID:    m.ID,
			Stamp: stamp,
			Hash:  hash,
			Path:  filepath.Join(dir, e.Name()),
		}
		if t, err := time.Parse(bodyBackupStamp, stamp); err == nil {
			b.Time = t
		}
		if info, err := e.Info(); err == nil {
			b.Bytes = info.Size()
		}
		if data, err := os.ReadFile(b.Path); err == nil {
			b.Lines = countBodyLines(string(data))
		}
		backups = append(backups, b)
	}

	sort.Slice(backups, func(i, j int) bool { return backups[i].Stamp > backups[j].Stamp })
	return backups, nil
}

// RestoreBody replaces an item's body with a saved prior one and returns the
// updated item alongside the backup it replayed.
//
// from selects the backup by STAMP PREFIX; empty means the most recent. A
// prefix that names no backup, or more than one, is refused rather than
// resolved to a best guess — picking for the caller is how you restore the
// wrong version onto a body you just destroyed, and there is no third copy.
//
// The restore is itself a replace-mode edit and routes through Update, so the
// body it is about to overwrite gets backed up first. Restoring the wrong
// version is therefore undoable, which is the property that makes restoring at
// all a safe thing to try.
//
// The saved bytes are replayed exactly as `mg edit --body-file` would write
// them, including the "# Title" heading they carried when they were saved.
func RestoreBody(root, id, from, ifUnchanged string) (*Item, *BodyBackup, error) {
	backups, err := ListBodyBackups(root, id)
	if err != nil {
		return nil, nil, err
	}
	if len(backups) == 0 {
		return nil, nil, errNoBodyBackup(root, id)
	}

	chosen, err := pickBodyBackup(id, backups, from)
	if err != nil {
		return nil, nil, err
	}

	data, err := os.ReadFile(chosen.Path)
	if err != nil {
		return nil, nil, ioErr(fmt.Sprintf("%s: saved body %s could not be read: %s", id, chosen.Stamp, fsErrText(err)))
	}

	body := string(data)
	item, err := Update(root, id, UpdateField{Body: &body, IfUnchanged: ifUnchanged})
	if err != nil {
		return nil, nil, err
	}
	return item, chosen, nil
}

// pickBodyBackup resolves a --from prefix against a newest-first list.
func pickBodyBackup(id string, backups []BodyBackup, from string) (*BodyBackup, error) {
	if from == "" {
		return &backups[0], nil
	}
	var hits []BodyBackup
	for _, b := range backups {
		if strings.HasPrefix(b.Stamp, from) {
			hits = append(hits, b)
		}
	}
	switch len(hits) {
	case 1:
		return &hits[0], nil
	case 0:
		return nil, mgerr.NotFound("body_backup_not_found",
			fmt.Sprintf("%s: no saved body whose timestamp starts with %q.", id, from),
			fmt.Sprintf("List what is saved with 'mg restore-body %s --list' and pass one of those timestamps.", id))
	default:
		return nil, mgerr.Usage("ambiguous_body_backup",
			fmt.Sprintf("%s: %q names %d saved bodies (%s); refusing to guess which one you meant.",
				id, from, len(hits), strings.Join(backupStamps(hits), ", ")),
			fmt.Sprintf("Pass a longer --from. 'mg restore-body %s --list' prints each timestamp in full.", id))
	}
}

func backupStamps(backups []BodyBackup) []string {
	out := make([]string, 0, len(backups))
	for _, b := range backups {
		out = append(out, b.Stamp)
	}
	return out
}

// errNoBodyBackup is the refusal that makes "restored" mean something.
//
// A restore command that succeeds quietly when there is nothing to restore —
// or, worse, writes an empty body — turns the one instrument for recovering a
// destroyed body into a second way to destroy it. So the empty case is an
// error, exit 3, and it names the directory it looked in so the caller can see
// for themselves that the cupboard is bare rather than take mg's word for it.
//
// It is reachable for an ordinary reason: backups start at the first
// replace-mode edit AFTER this shipped. An item whose body was destroyed before
// then has nothing saved, and no amount of retrying will change that.
func errNoBodyBackup(root, id string) *mgerr.Error {
	dir := "the store"
	if m, err := ResolveUnique(root, id); err == nil {
		dir = bodyBackupDirFor(root, m.Path, m.Status, m.ID)
	}
	return mgerr.NotFound("no_body_backup",
		fmt.Sprintf("%s: no prior body is saved — nothing to restore.", id),
		fmt.Sprintf("mg saves a body only when a replace-mode edit (--body/--body-file) overwrites it, "+
			"and only from the point this was installed; appends and --title edits do not overwrite anything. "+
			"Looked in %s. If the body was destroyed before then, the 'work.edited' event for it "+
			"('mg event list --type=work.edited') records the before/after hashes but not the bytes.", dir))
}
