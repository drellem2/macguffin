package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mgArchive runs the mg binary against root and returns combined output and
// the process exit code.
func mgArchive(t *testing.T, bin, root string, args ...string) (string, int) {
	t.Helper()
	full := append([]string{"--root=" + root}, args...)
	cmd := exec.Command(bin, full...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("mg %v: %v", args, err)
	}
	return string(out), code
}

// seedDone creates a done item and returns its ID.
func seedDone(t *testing.T, bin, root, title string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "new", "task", title)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	// "Created mg-XXXX: task <title>"
	_, rest, ok := strings.Cut(out, "Created ")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id = strings.TrimSpace(id)

	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", id, code, out)
	}
	if out, code := mgArchive(t, bin, root, "done", id); code != 0 {
		t.Fatalf("mg done %s: exit %d\n%s", id, code, out)
	}
	return id
}

func archiveTestRoot(t *testing.T, bin string) string {
	t.Helper()
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}
	return root
}

// TestCLI_ArchiveIDArchivesThatItem is the mg-322f regression: `mg archive <id>`
// — the form the mayor prompt documents — previously accepted the ID, ignored
// it, printed "No items to archive." and exited 0. It must now archive it.
func TestCLI_ArchiveIDArchivesThatItem(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDone(t, bin, root, "archive me by name")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code != 0 {
		t.Fatalf("mg archive %s: exit %d\n%s", id, code, out)
	}
	if !strings.Contains(out, "Archived "+id) {
		t.Errorf("output = %q, want it to report archiving %s", out, id)
	}
	if strings.Contains(out, "No items to archive") {
		t.Errorf("mg archive %s reported the old silent no-op: %q", id, out)
	}

	// It must really be gone from the live list.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if strings.Contains(listOut, id) {
		t.Errorf("%s still listed as done after archive:\n%s", id, listOut)
	}
}

// TestCLI_ArchiveUnknownIDIsLoud is the RED half of the contract: an archive
// that does nothing must exit non-zero and say so, never exit 0 quietly.
func TestCLI_ArchiveUnknownIDIsLoud(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, code := mgArchive(t, bin, root, "archive", "mg-nope")
	if code == 0 {
		t.Errorf("mg archive mg-nope exited 0 — a no-op must be loud\n%s", out)
	}
	if strings.Contains(out, "No items to archive") {
		t.Errorf("unknown id reported the cheerful sweep message: %q", out)
	}
}

// TestCLI_ArchiveNonDoneIsLoud: archiving a not-done item must refuse audibly.
func TestCLI_ArchiveNonDoneIsLoud(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, _ := mgArchive(t, bin, root, "new", "task", "not done yet")
	_, rest, _ := strings.Cut(out, "Created ")
	id, _, _ := strings.Cut(rest, ":")
	id = strings.TrimSpace(id)

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Errorf("mg archive on an available item exited 0\n%s", out)
	}
	if !strings.Contains(out, "not done") {
		t.Errorf("output = %q, want it to name the real problem (not done)", out)
	}
}

// TestCLI_ArchiveIDCannotReachTheSweep is the regression guard for the composed
// failure in mg-322f. The targeted form must never widen into --days=0's
// mass-archive, which is what silently ate gh-issue gate carriers. Every item
// the caller did not name must survive — including ones old enough for any
// sweep to have taken.
func TestCLI_ArchiveIDCannotReachTheSweep(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	target := seedDone(t, bin, root, "the one I named")
	carrier := seedDone(t, bin, root, "gate carrier — must survive")

	// Backdate the carrier past every sweep threshold. Without this the test
	// cannot fail: a fresh carrier survives an accidental sweep on its own age,
	// and the guard would pass while proving nothing.
	old := time.Now().Add(-90 * 24 * time.Hour)
	carrierPath := filepath.Join(root, "work", "done", carrier+".md")
	if err := os.Chtimes(carrierPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if out, code := mgArchive(t, bin, root, "archive", target); code != 0 {
		t.Fatalf("mg archive %s: exit %d\n%s", target, code, out)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, carrier) {
		t.Errorf("targeted archive of %s also removed %s — it widened into a sweep:\n%s",
			target, carrier, listOut)
	}
}

// TestCLI_ArchiveIDWithDaysIsUsageError: the two forms are exclusive. Guessing
// between "archive one item" and "archive every done item" is exactly the
// silent-destroyer risk this ticket exists to prevent.
func TestCLI_ArchiveIDWithDaysIsUsageError(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	target := seedDone(t, bin, root, "named")
	carrier := seedDone(t, bin, root, "must survive")

	out, code := mgArchive(t, bin, root, "archive", target, "--days=0")
	if code == 0 {
		t.Errorf("mg archive ID --days=0 exited 0, want a usage error\n%s", out)
	}

	// Critically: it must not have archived anything on its way to erroring.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	for _, id := range []string{target, carrier} {
		if !strings.Contains(listOut, id) {
			t.Errorf("rejected invocation still archived %s:\n%s", id, listOut)
		}
	}
}

// TestCLI_ArchiveTooManyArgsIsUsageError pins the cobra arity: extra positional
// args must not be silently swallowed the way the single one used to be.
func TestCLI_ArchiveTooManyArgsIsUsageError(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, code := mgArchive(t, bin, root, "archive", "mg-aaaa", "mg-bbbb")
	if code == 0 {
		t.Errorf("mg archive with two IDs exited 0, want a usage error\n%s", out)
	}
}

// TestCLI_ArchiveSweepStillWorks is the negative control: the error path is
// CONDITIONAL, not hard-wired. The bare sweep must still archive.
func TestCLI_ArchiveSweepStillWorks(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	a := seedDone(t, bin, root, "sweep me a")
	b := seedDone(t, bin, root, "sweep me b")

	out, code := mgArchive(t, bin, root, "archive", "--days=0")
	if code != 0 {
		t.Fatalf("mg archive --days=0: exit %d\n%s", code, out)
	}
	for _, id := range []string{a, b} {
		if !strings.Contains(out, "Archived "+id) {
			t.Errorf("sweep output %q missing %s", out, id)
		}
	}
	if !strings.Contains(out, "Archived 2 item(s)") {
		t.Errorf("sweep output = %q, want a count of 2", out)
	}
}

// TestCLI_ArchiveDryRunMovesNothing: the sweep is an unfiltered mass mutation,
// so its preview must be trustworthy — it lists and does not touch.
func TestCLI_ArchiveDryRunMovesNothing(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDone(t, bin, root, "preview me")

	out, code := mgArchive(t, bin, root, "archive", "--days=0", "--dry-run")
	if code != 0 {
		t.Fatalf("mg archive --dry-run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Would archive "+id) {
		t.Errorf("dry-run output = %q, want it to preview %s", out, id)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, id) {
		t.Errorf("--dry-run actually archived %s:\n%s", id, listOut)
	}
}

// TestCLI_ArchiveEmptySweepStillReportsNoItems: the sweep's "nothing to do" is
// legitimately not an error — only the *targeted* form promises an item.
func TestCLI_ArchiveEmptySweepStillReportsNoItems(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, code := mgArchive(t, bin, root, "archive", "--days=0")
	if code != 0 {
		t.Errorf("empty sweep exited %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "No items to archive") {
		t.Errorf("output = %q, want the no-items message", out)
	}
}
