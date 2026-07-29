package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// replaceBody is the destructive edit this whole file exists about: a
// mode=replace write that sends a complete body computed elsewhere.
func replaceBody(t *testing.T, root, id, body string) *Item {
	t.Helper()
	item, err := Update(root, id, UpdateField{Body: &body})
	if err != nil {
		t.Fatalf("Update(%s) replace body: %v", id, err)
	}
	return item
}

// TestBodyBackup_ReplaceIsRecoverable is mg-9fc8's acceptance criterion:
// destroy a body on a scratch item, then get it back.
func TestBodyBackup_ReplaceIsRecoverable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Scratch item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The real body: many lines of prose worth losing.
	var lines []string
	lines = append(lines, "\n# Scratch item\n")
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("line %d of the real body\n", i))
	}
	good := strings.Join(lines, "")
	replaceBody(t, root, item.ID, good)

	stored, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantHash := BodyHash(stored.Body)

	// Now the incident: a failed read hands mg two lines of a shell error and
	// mg faithfully writes them.
	replaceBody(t, root, item.ID, "\n# Scratch item\nError: unknown flag: --body\n")

	wrecked, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after clobber: %v", err)
	}
	if strings.Contains(wrecked.Body, "line 39 of the real body") {
		t.Fatal("setup is wrong: the body was not actually destroyed")
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("no backup was saved for a replace-mode edit")
	}

	restored, used, err := RestoreBody(root, item.ID, "", "")
	if err != nil {
		t.Fatalf("RestoreBody: %v", err)
	}
	if got := BodyHash(restored.Body); got != wantHash {
		t.Errorf("restored body hash = %s, want %s\nrestored:\n%s", got, wantHash, restored.Body)
	}
	if used.Stamp == "" {
		t.Error("RestoreBody returned a backup with no stamp")
	}

	// And it is durable, not just returned.
	reread, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after restore: %v", err)
	}
	if BodyHash(reread.Body) != wantHash {
		t.Errorf("restore did not persist: stored hash = %s, want %s", BodyHash(reread.Body), wantHash)
	}
}

// TestBodyBackup_RestoreCanFail is the positive control mg-9fc8 asked for: a
// restore against an item with nothing saved must ERROR, not quietly succeed
// and not write an empty body. A recovery command that reports success when it
// recovered nothing is a second way to lose a body.
func TestBodyBackup_RestoreCanFail(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Never overwritten", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	_, _, err = RestoreBody(root, item.ID, "", "")
	if err == nil {
		t.Fatal("RestoreBody on an item with no saved body returned nil error")
	}
	if code := mgerrCode(err); code != "no_body_backup" {
		t.Errorf("error code = %q, want no_body_backup (err: %v)", code, err)
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after failed restore: %v", err)
	}
	if after.Body != before.Body {
		t.Errorf("failed restore modified the body:\nbefore: %q\nafter:  %q", before.Body, after.Body)
	}
	if strings.TrimSpace(after.Body) == "" {
		t.Error("failed restore left an empty body")
	}
}

// TestBodyBackup_PruneIsExercised holds the bound to its claim: 10 kept per
// item, and the ten kept are the ten most recent — not ten arbitrary ones.
func TestBodyBackup_PruneIsExercised(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Churned", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const writes = bodyBackupKeep + 5
	for i := 0; i < writes; i++ {
		replaceBody(t, root, item.ID, fmt.Sprintf("\n# Churned\n\nversion %d\n", i))
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) != bodyBackupKeep {
		t.Fatalf("kept %d backups, want exactly %d", len(backups), bodyBackupKeep)
	}

	// The first write overwrote the generated body, so the saved versions are
	// "version 0" .. "version writes-2". Keeping the newest bodyBackupKeep of
	// those means the oldest survivor is version (writes-1)-bodyBackupKeep.
	got := map[string]bool{}
	for _, b := range backups {
		data, err := os.ReadFile(b.Path)
		if err != nil {
			t.Fatalf("read backup %s: %v", b.Path, err)
		}
		got[strings.TrimSpace(string(data))] = true
	}
	for i := writes - 1 - bodyBackupKeep; i <= writes-2; i++ {
		want := fmt.Sprintf("# Churned\n\nversion %d", i)
		if !got[want] {
			t.Errorf("version %d should have survived the prune but did not", i)
		}
	}
	for i := 0; i < writes-1-bodyBackupKeep; i++ {
		want := fmt.Sprintf("# Churned\n\nversion %d", i)
		if got[want] {
			t.Errorf("version %d should have been pruned but survived", i)
		}
	}

	// Newest-first ordering is the contract --list and the default restore both
	// depend on.
	for i := 1; i < len(backups); i++ {
		if backups[i-1].Stamp <= backups[i].Stamp {
			t.Errorf("backups not newest-first: %s then %s", backups[i-1].Stamp, backups[i].Stamp)
		}
	}
}

// TestBodyBackup_NotSavedForAppendOrTitle pins the documented scope. An append
// composes against the body on disk and cannot destroy a section it never saw;
// a --title edit rewrites one line. Backing either up would burn slots in a
// bounded store to protect writes that were never at risk.
func TestBodyBackup_NotSavedForAppendOrTitle(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Safe writes", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	add := "## a dated section\n"
	if _, err := Update(root, item.ID, UpdateField{AppendBody: &add}); err != nil {
		t.Fatalf("append: %v", err)
	}
	title := "Safe writes, retitled"
	if _, err := Update(root, item.ID, UpdateField{Title: &title}); err != nil {
		t.Fatalf("retitle: %v", err)
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("appends and --title saved %d bodies, want 0", len(backups))
	}
}

// TestBodyBackup_RestoreIsItselfUndoable: the restore is a replace, so it saves
// what it overwrites. Restoring the wrong version must not be terminal.
func TestBodyBackup_RestoreIsItselfUndoable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Undo", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	replaceBody(t, root, item.ID, "\n# Undo\n\nversion one\n")
	replaceBody(t, root, item.ID, "\n# Undo\n\nversion two\n")

	// Restore takes us back to version one; version two must now be saved.
	restored, _, err := RestoreBody(root, item.ID, "", "")
	if err != nil {
		t.Fatalf("RestoreBody: %v", err)
	}
	if !strings.Contains(restored.Body, "version one") {
		t.Fatalf("restored body = %q, want version one", restored.Body)
	}

	again, _, err := RestoreBody(root, item.ID, "", "")
	if err != nil {
		t.Fatalf("second RestoreBody: %v", err)
	}
	if !strings.Contains(again.Body, "version two") {
		t.Errorf("undo restored %q, want version two back", again.Body)
	}
}

// TestBodyBackup_FromSelectsAndRefuses covers --from's three outcomes: an exact
// pick, a prefix naming nothing, and a prefix naming several.
func TestBodyBackup_FromSelectsAndRefuses(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Pick one", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 3; i++ {
		replaceBody(t, root, item.ID, fmt.Sprintf("\n# Pick one\n\nv%d\n", i))
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("got %d backups, want 3", len(backups))
	}

	// Exact stamp: the oldest one, which is not what the default would pick.
	oldest := backups[len(backups)-1]
	restored, used, err := RestoreBody(root, item.ID, oldest.Stamp, "")
	if err != nil {
		t.Fatalf("RestoreBody --from=%s: %v", oldest.Stamp, err)
	}
	if used.Stamp != oldest.Stamp {
		t.Errorf("restored %s, want %s", used.Stamp, oldest.Stamp)
	}
	want, err := os.ReadFile(oldest.Path)
	if err != nil {
		t.Fatalf("read %s: %v", oldest.Path, err)
	}
	if restored.Body != string(want) {
		t.Errorf("body = %q, want %q", restored.Body, string(want))
	}

	if _, _, err := RestoreBody(root, item.ID, "19700101T000000.000Z", ""); err == nil {
		t.Error("--from naming no backup should error")
	} else if code := mgerrCode(err); code != "body_backup_not_found" {
		t.Errorf("code = %q, want body_backup_not_found", code)
	}

	// Every stamp shares the "20" century prefix, so this names all of them.
	if _, _, err := RestoreBody(root, item.ID, "20", ""); err == nil {
		t.Error("an ambiguous --from should be refused, not resolved to a guess")
	} else if code := mgerrCode(err); code != "ambiguous_body_backup" {
		t.Errorf("code = %q, want ambiguous_body_backup", code)
	}
}

// TestBodyBackup_ArchiveAndUnarchiveCarryBackups pins where backups go on the
// transition that takes an item out of the live tree — the acceptance
// criterion that archiving must not orphan them.
func TestBodyBackup_ArchiveAndUnarchiveCarryBackups(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Goes to the archive", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	replaceBody(t, root, item.ID, "\n# Goes to the archive\n\nthe good body\n")
	replaceBody(t, root, item.ID, "\n# Goes to the archive\n\nclobbered\n")

	before, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("got %d backups before archive, want 2", len(before))
	}

	if _, err := Claim(root, item.ID, os.Getpid()); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := ArchiveItem(root, item.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	// Nothing is left behind in the live tree...
	live := filepath.Join(liveBodyBackupParent(root), item.ID)
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("%s still exists after archive (err=%v); backups were orphaned in the live tree", live, err)
	}

	// ...and they are findable, and restorable, from the partition.
	after, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups after archive: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("got %d backups after archive, want 2", len(after))
	}
	for _, b := range after {
		if !strings.Contains(b.Path, filepath.Join("work", "archive")) {
			t.Errorf("backup %s did not move into the archive partition", b.Path)
		}
	}
	restored, _, err := RestoreBody(root, item.ID, "", "")
	if err != nil {
		t.Fatalf("RestoreBody on an archived item: %v", err)
	}
	if !strings.Contains(restored.Body, "the good body") {
		t.Errorf("restored %q, want the good body", restored.Body)
	}

	// Unarchive brings them back out with the record.
	if _, _, err := Unarchive(root, item.ID, "done"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	back, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups after unarchive: %v", err)
	}
	if len(back) == 0 {
		t.Fatal("backups did not come back out of the archive")
	}
	for _, b := range back {
		if strings.Contains(b.Path, filepath.Join("work", "archive")) {
			t.Errorf("backup %s stayed in the archive after unarchive", b.Path)
		}
	}
}

// TestBodyBackup_ShelveKeepsBackupsInPlace: shelving is a live-tree move, so
// the backups do not travel and restore keeps working across it.
func TestBodyBackup_ShelveKeepsBackupsInPlace(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Shelved", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	replaceBody(t, root, item.ID, "\n# Shelved\n\nthe good body\n")
	replaceBody(t, root, item.ID, "\n# Shelved\n\nclobbered\n")

	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups after shelve: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("got %d backups after shelve, want 2", len(backups))
	}
	for _, b := range backups {
		if filepath.Dir(b.Path) != filepath.Join(liveBodyBackupParent(root), item.ID) {
			t.Errorf("shelving moved a backup to %s", b.Path)
		}
	}
	restored, _, err := RestoreBody(root, item.ID, "", "")
	if err != nil {
		t.Fatalf("RestoreBody on a shelved item: %v", err)
	}
	if !strings.Contains(restored.Body, "the good body") {
		t.Errorf("restored %q, want the good body", restored.Body)
	}
}

// TestBodyBackup_StampsAreUniqueAndOrdered guards the property prune leans on:
// several saves inside one millisecond still sort by write order. Without the
// stamp bump, "the ten most recent" would be a sort by hash.
func TestBodyBackup_StampsAreUniqueAndOrdered(t *testing.T) {
	dir := t.TempDir()
	frozen := time.Date(2026, 7, 29, 16, 14, 0, 0, time.UTC)

	var paths []string
	for i := 0; i < 5; i++ {
		p, err := saveBodyBackup(dir, fmt.Sprintf("body %d\n", i), frozen)
		if err != nil {
			t.Fatalf("saveBodyBackup: %v", err)
		}
		paths = append(paths, filepath.Base(p))
	}

	seen := map[string]bool{}
	for i, name := range paths {
		stamp, _, ok := parseBackupName(name)
		if !ok {
			t.Fatalf("saved name %q does not parse as a backup", name)
		}
		if seen[stamp] {
			t.Errorf("stamp %s reused; write order is no longer recoverable from the name", stamp)
		}
		seen[stamp] = true
		if i > 0 && paths[i-1] >= name {
			t.Errorf("names not in write order: %q then %q", paths[i-1], name)
		}
	}
}

// TestBodyBackup_SaveFailureRefusesTheEdit: if the prior body cannot be saved,
// the edit does not happen. A recovery guarantee that silently stops holding is
// worse than none, because it is relied on.
func TestBodyBackup_SaveFailureRefusesTheEdit(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Cannot back up", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	replaceBody(t, root, item.ID, "\n# Cannot back up\n\nthe good body\n")

	before, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// A regular FILE where the backup directory needs to be: MkdirAll fails.
	blocker := liveBodyBackupParent(root)
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatalf("clearing %s: %v", blocker, err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}

	clobber := "\n# Cannot back up\n\nError: unknown flag: --body\n"
	if _, err := Update(root, item.ID, UpdateField{Body: &clobber}); err == nil {
		t.Fatal("replace succeeded even though the prior body could not be saved")
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after refusal: %v", err)
	}
	if after.Body != before.Body {
		t.Errorf("refused edit still modified the body:\nbefore: %q\nafter:  %q", before.Body, after.Body)
	}
}

// TestBodyBackup_StrayFilesAreNotOfferedAsBodies: only files this package wrote
// are restorable. Offering an editor swapfile or a stray note as a prior body
// would make restore a way to write arbitrary content over a real one.
func TestBodyBackup_StrayFilesAreNotOfferedAsBodies(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Strays", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	replaceBody(t, root, item.ID, "\n# Strays\n\nreal\n")

	dir := filepath.Join(liveBodyBackupParent(root), item.ID)
	for _, name := range []string{"notes.md", "README", "20260729T161400.000Z.md", "not-a-stamp-deadbeef.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("stray\n"), 0o644); err != nil {
			t.Fatalf("write stray %s: %v", name, err)
		}
	}

	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("got %d restorable bodies, want 1 (strays must be ignored): %+v", len(backups), backups)
	}
}
