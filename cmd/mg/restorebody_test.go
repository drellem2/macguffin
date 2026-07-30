package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedWithBody creates an item and gives it a real body via a replace-mode
// edit, returning the ID. The edit is the point: it is what makes the generated
// body the first thing worth saving.
//
// The title is passed as --title, not as a positional. It used to be
// ("new", "task", title), where "task" was NOT the type — mg new joins its
// positionals into the title, so the item was titled "task Real body" while the
// body it was then given led with "# Real body". Under the old title coupling
// that mismatch silently stacked a second H1 above the caller's, so this helper
// was manufacturing mg-bac6's corruption on every run and every assertion still
// passed. Naming both fields is the same discipline the coupling guard now
// enforces on real callers.
func seedWithBody(t *testing.T, bin, root, title, body string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "new", "--type", "task", "--title", title)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)
	if out, code := mgArchive(t, bin, root, "edit", id, "--body", body); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}
	return id
}

func extractID(t *testing.T, newOutput string) string {
	t.Helper()
	for _, f := range strings.Fields(newOutput) {
		if strings.HasPrefix(f, "mg-") {
			return strings.TrimSuffix(f, ":")
		}
	}
	t.Fatalf("could not find an item id in %q", newOutput)
	return ""
}

// TestCLI_RestoreBody_RecoversADestroyedBody walks mg-9fc8's incident end to
// end at the CLI: a replace-mode edit destroys a body, and mg restore-body puts
// it back.
func TestCLI_RestoreBody_RecoversADestroyedBody(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	good := "# Real body\n\nA paragraph worth keeping.\n\n## A section\n\nMore prose.\n"
	id := seedWithBody(t, bin, root, "Real body", good)

	hashOut, code := mgArchive(t, bin, root, "show", id, "--body-hash")
	if code != 0 {
		t.Fatalf("mg show --body-hash: exit %d\n%s", code, hashOut)
	}
	wantHash := strings.TrimSpace(hashOut)

	// The incident: `mg show --body` does not exist, its usage error lands in
	// the file, and the unconditional `mg edit` writes it.
	clobber := "# Real body\nError: unknown flag: --body\n  → mg show ID [flags]\n"
	if out, code := mgArchive(t, bin, root, "edit", id, "--body", clobber); code != 0 {
		t.Fatalf("mg edit (clobber): exit %d\n%s", code, out)
	}

	listOut, code := mgArchive(t, bin, root, "restore-body", id, "--list")
	if code != 0 {
		t.Fatalf("mg restore-body --list: exit %d\n%s", code, listOut)
	}
	if !strings.Contains(listOut, "saved body") {
		t.Errorf("--list did not report a saved body:\n%s", listOut)
	}

	out, code := mgArchive(t, bin, root, "restore-body", id)
	if code != 0 {
		t.Fatalf("mg restore-body: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Restored "+id) {
		t.Errorf("restore output does not name what it did:\n%s", out)
	}

	gotHash, code := mgArchive(t, bin, root, "show", id, "--body-hash")
	if code != 0 {
		t.Fatalf("mg show --body-hash after restore: exit %d\n%s", code, gotHash)
	}
	if strings.TrimSpace(gotHash) != wantHash {
		t.Errorf("body hash after restore = %s, want %s", strings.TrimSpace(gotHash), wantHash)
	}
}

// TestCLI_RestoreBody_PositiveControl is the check mg-9fc8 asked for by name:
// prove the restore CAN fail. An item with nothing saved must exit non-zero and
// leave the body alone, rather than reporting success or writing an empty body.
func TestCLI_RestoreBody_PositiveControl(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "new", "task", "Nothing saved")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	id := extractID(t, out)

	before, code := mgArchive(t, bin, root, "show", id, "--body-hash")
	if code != 0 {
		t.Fatalf("mg show --body-hash: exit %d\n%s", code, before)
	}

	out, code = mgArchive(t, bin, root, "restore-body", id)
	if code == 0 {
		t.Fatalf("restore-body with no saved body exited 0:\n%s", out)
	}
	if code != 3 {
		t.Errorf("exit %d, want 3 (not_found)\n%s", code, out)
	}
	if !strings.Contains(out, "nothing to restore") {
		t.Errorf("error does not say there is nothing to restore:\n%s", out)
	}

	after, code := mgArchive(t, bin, root, "show", id, "--body-hash")
	if code != 0 {
		t.Fatalf("mg show --body-hash after failure: exit %d\n%s", code, after)
	}
	if after != before {
		t.Errorf("failed restore changed the body: hash %s -> %s", strings.TrimSpace(before), strings.TrimSpace(after))
	}

	// --list is a report, not a gate: it says so and exits 0.
	out, code = mgArchive(t, bin, root, "restore-body", id, "--list")
	if code != 0 {
		t.Errorf("restore-body --list with nothing saved: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "No saved bodies") {
		t.Errorf("--list output does not report the empty case:\n%s", out)
	}
}

// TestCLI_RestoreBody_PruneBound exercises the bound rather than assuming it:
// past bodyBackupKeep replace edits, exactly that many files remain on disk.
func TestCLI_RestoreBody_PruneBound(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	id := seedWithBody(t, bin, root, "Churn", "# Churn\n\nversion -1\n")
	const extra = 14
	for i := 0; i < extra; i++ {
		body := "# Churn\n\nversion " + string(rune('a'+i)) + "\n"
		if out, code := mgArchive(t, bin, root, "edit", id, "--body", body); code != 0 {
			t.Fatalf("mg edit %d: exit %d\n%s", i, code, out)
		}
	}

	dir := filepath.Join(root, "work", ".bodybak", id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 10 {
		t.Errorf("%d files in %s, want 10 (the prune bound)", len(entries), dir)
	}

	// The bound must not have cost us the most recent one.
	out, code := mgArchive(t, bin, root, "restore-body", id)
	if code != 0 {
		t.Fatalf("mg restore-body: exit %d\n%s", code, out)
	}
	body, code := mgArchive(t, bin, root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, body)
	}
	if !strings.Contains(body, "version "+string(rune('a'+extra-2))) {
		t.Errorf("restore did not return the most recent saved body:\n%s", body)
	}
}

// TestCLI_RestoreBody_ArchiveDoesNotOrphanBackups pins where the saved bodies
// go when an item leaves the live tree, and that restore still reaches them.
func TestCLI_RestoreBody_ArchiveDoesNotOrphanBackups(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	id := seedWithBody(t, bin, root, "Archived", "# Archived\n\nthe good body\n")
	if out, code := mgArchive(t, bin, root, "edit", id, "--body", "# Archived\n\nclobbered\n"); code != 0 {
		t.Fatalf("mg edit (clobber): exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "done", id); code != 0 {
		t.Fatalf("mg done: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "archive", id); code != 0 {
		t.Fatalf("mg archive: exit %d\n%s", code, out)
	}

	live := filepath.Join(root, "work", ".bodybak", id)
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("%s survives after archive (err=%v): backups orphaned in the live tree", live, err)
	}

	listOut, code := mgArchive(t, bin, root, "restore-body", id, "--list")
	if code != 0 {
		t.Fatalf("mg restore-body --list on an archived item: exit %d\n%s", code, listOut)
	}
	if !strings.Contains(listOut, filepath.Join("work", "archive")) {
		t.Errorf("--list does not show the backups in the archive partition:\n%s", listOut)
	}

	if out, code := mgArchive(t, bin, root, "restore-body", id); code != 0 {
		t.Fatalf("mg restore-body on an archived item: exit %d\n%s", code, out)
	}
	body, code := mgArchive(t, bin, root, "show", id, "--json")
	if code != 0 {
		t.Fatalf("mg show --json: exit %d\n%s", code, body)
	}
	if !strings.Contains(body, "the good body") {
		t.Errorf("archived item's body was not restored:\n%s", body)
	}
}

// TestCLI_RestoreBody_EventNamesTheBackup: the audit line that could previously
// only prove a body was destroyed now carries the path to the bytes.
func TestCLI_RestoreBody_EventNamesTheBackup(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	id := seedWithBody(t, bin, root, "Audited", "# Audited\n\nthe good body\n")
	if out, code := mgArchive(t, bin, root, "edit", id, "--body", "# Audited\n\nclobbered\n"); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "event", "list", "--type=work.edited", "--json")
	if code != 0 {
		t.Fatalf("mg event list: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "body_backup") {
		t.Errorf("work.edited does not name the saved body:\n%s", out)
	}
	if !strings.Contains(out, ".bodybak") {
		t.Errorf("body_backup does not point into the backup directory:\n%s", out)
	}
}
