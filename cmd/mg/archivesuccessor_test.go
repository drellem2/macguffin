package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The design-successor guard (mg-12a0). A `type: design` item's output IS a
// recommendation, so at the moment it is done the thing it recommends is undone
// by construction; archiving it hides that work, because an archived item
// cannot be the tracker for undone work.
//
// The ORDER of the tests below is deliberate and is the point of the exercise.
// The refusal is proven FIRST, and proven to be observed refusing the bare case,
// before anything asserts that a linked design passes. A guard that has only
// ever been seen to pass has not been tested — and here a false PASS is silent
// and permanent, which is precisely the failure the guard exists to prevent.
// The converse control follows: a guard that refuses everything is
// indistinguishable from a broken tool.
//
// This is the failure that produced the ticket: the rule was extracted from
// mg-ab67 at ~02:20, and the agent that wrote it down archived mg-2da4 — a done
// design whose build had no ticket — bare at 03:01Z, holding the rule. It then
// audited its own archiving and reported it clean, because the audit grepped
// bodies for "STILL IN SCOPE" and a design recommending a build does not say
// those words. Knowing is not a mechanism; neither is a grep.

// seedDoneOfType creates a done item of the given type and returns its id.
//
// It files with --no-declares-remainder (unless the caller forced the
// declaration on) because these tests are about the ARCHIVE-time guards, and
// since mg-966d a `--type=design` filing declares a remainder by default — which
// `mg done` refuses, so the helper could not reach done/ at all. Opting out here
// seeds precisely the shape the archive guards exist for: an item that reached
// done/ having declared nothing, which is every design filed before mg-8970 and
// every one filed with the escape since. The default itself is proven in
// newdeclares_test.go, where it is the subject rather than the fixture.
func seedDoneOfType(t *testing.T, bin, root, typ, title string, extra ...string) string {
	t.Helper()
	args := append([]string{"new", "--type=" + typ, title}, extra...)
	if !hasFlag(extra, "--declares-remainder") {
		args = append(args, "--no-declares-remainder")
	}
	out, code := mgArchive(t, bin, root, args...)
	if code != 0 {
		t.Fatalf("mg new %s: exit %d\n%s", typ, code, out)
	}
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

// hasFlag reports whether args contains the named flag, in either the bare
// `--flag` or the `--flag=value` form.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// seedAvailable creates an item and leaves it available, returning its id.
func seedAvailable(t *testing.T, bin, root, typ, title string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "new", "--type="+typ, title)
	if code != 0 {
		t.Fatalf("mg new %s: exit %d\n%s", typ, code, out)
	}
	_, rest, _ := strings.Cut(out, "Created ")
	id, _, _ := strings.Cut(rest, ":")
	return strings.TrimSpace(id)
}

// --- The refusal, proven first ---------------------------------------------

// TestCLI_ArchiveBareDesignIsRefused is the mg-2da4 regression and the primary
// control: a done design nothing tracks must FAIL, loudly, naming what is
// missing — and must still be in done/ afterwards.
func TestCLI_ArchiveBareDesignIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "recommend building the thing")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("mg archive on a bare design exited 0 — the guard did not fire\n%s", out)
	}
	if !strings.Contains(out, "successor") {
		t.Errorf("refusal = %q, want it to name what is missing (a successor)", out)
	}
	if !strings.Contains(out, "--successor") {
		t.Errorf("refusal = %q, want it to name the remedy '--successor <id>'", out)
	}

	// Refusing must not have moved anything: the item is still archivable
	// later, once a successor exists.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s left done/ despite the refusal:\n%s", id, listOut)
	}
}

// TestCLI_ArchiveRefusalDoesNotOfferForce is CONSTRAINT 2. --force exists, but
// an agent that hits a refusal mid-cleanup, at speed, reaches for whatever the
// error message hands it. If the failure text teaches the bypass, the outcome
// is not "rule violated" but "rule forced" — which looks clean in the archive
// and is invisible afterwards. So the remedy the message names is --successor,
// and --force is discoverable only in `mg archive --help`, read deliberately.
func TestCLI_ArchiveRefusalDoesNotOfferForce(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "a design whose refusal must not teach the bypass")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("expected refusal, got exit 0\n%s", out)
	}
	if strings.Contains(out, "--force") {
		t.Errorf("refusal offers --force, making the guard decorative: %q", out)
	}

	// It must nevertheless BE documented, or the abandoned-design case has no
	// path at all.
	help, code := mgArchive(t, bin, root, "archive", "--help")
	if code != 0 {
		t.Fatalf("mg archive --help: exit %d\n%s", code, help)
	}
	if !strings.Contains(help, "--force") {
		t.Errorf("--force is absent from `mg archive --help`, so an abandoned design has no documented archive path:\n%s", help)
	}
}

// TestCLI_ArchiveDesignWithMentionOnlyIsStillRefused is CONSTRAINT 1: the
// satisfaction condition must be structural, not textual. A search cannot
// distinguish a thing from talk about the thing. A later item whose body merely
// MENTIONS the design's id tracks nothing, and must not satisfy the guard —
// this is the false-pass direction, and false passes are silent and permanent.
func TestCLI_ArchiveDesignWithMentionOnlyIsStillRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design that gets name-dropped elsewhere")

	// An item that talks ABOUT the design without tracking it — exactly the
	// shape a body-scanning predicate would wave through.
	if out, code := mgArchive(t, bin, root, "new", "task",
		"unrelated work", "--body=Unlike "+id+", this one is straightforward."); code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("a passing mention of %s satisfied the guard — the check is textual, not structural\n%s", id, out)
	}
}

// TestCLI_ArchiveDesignWithDanglingSuccessorIsRefused: a successor: tag naming
// an item that does not exist tracks nothing. The pointer is re-resolved at
// archive time rather than trusted from when it was written.
func TestCLI_ArchiveDesignWithDanglingSuccessorIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design pointing at a ghost",
		"--tags=successor:mg-ffff")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("a successor tag naming nothing satisfied the guard\n%s", out)
	}
	if !strings.Contains(out, "mg-ffff") {
		t.Errorf("refusal = %q, want it to name the dangling successor", out)
	}
}

// TestCLI_ArchiveSuccessorMustExist: `--successor <id>` naming nothing must be
// rejected outright rather than written as a tag — otherwise the remedy the
// failure message hands you is itself a false pass one step removed.
func TestCLI_ArchiveSuccessorMustExist(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design with a made-up successor")

	out, code := mgArchive(t, bin, root, "archive", id, "--successor=mg-ffff")
	if code == 0 {
		t.Fatalf("--successor=mg-ffff (no such item) was accepted\n%s", out)
	}
	if !strings.Contains(out, "no such work item") {
		t.Errorf("output = %q, want it to say the successor does not exist", out)
	}
}

// TestCLI_ArchiveSelfSuccessorIsRefused: an item cannot track itself. Without
// this, `--successor <the-design's-own-id>` is a one-flag bypass that looks
// like compliance in the archived record.
func TestCLI_ArchiveSelfSuccessorIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design pointing at itself")

	out, code := mgArchive(t, bin, root, "archive", id, "--successor="+id)
	if code == 0 {
		t.Fatalf("a self-successor was accepted\n%s", out)
	}
	if !strings.Contains(out, "own successor") {
		t.Errorf("output = %q, want it to name the self-reference", out)
	}
}

// --- The other half: the guard must PERMIT ---------------------------------

// TestCLI_ArchiveDesignWithSuccessorSucceeds is the converse control. A guard
// that refuses everything is indistinguishable from a broken tool.
func TestCLI_ArchiveDesignWithSuccessorSucceeds(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	design := seedDoneOfType(t, bin, root, "design", "design whose build is tracked")
	build := seedAvailable(t, bin, root, "task", "build what the design recommends")

	out, code := mgArchive(t, bin, root, "archive", design, "--successor="+build)
	if code != 0 {
		t.Fatalf("mg archive %s --successor=%s: exit %d\n%s", design, build, code, out)
	}
	if !strings.Contains(out, "Archived "+design) {
		t.Errorf("output = %q, want it to report archiving %s", out, design)
	}

	// The link must survive into the archived record: a later reader of the
	// archive has to be able to find the tracker without the argv that made it.
	showOut, code := mgArchive(t, bin, root, "show", design)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", design, code, showOut)
	}
	if !strings.Contains(showOut, "successor:"+build) {
		t.Errorf("archived %s does not carry successor:%s:\n%s", design, build, showOut)
	}
}

// TestCLI_ArchivePreLinkedDesignSucceeds: the tag is an ordinary tag, so a
// design linked earlier (via `mg edit --add-tags` or at filing time) archives
// with no flag at all. This is the mg-e0ca / mg-b399 shape from the ticket's
// retroactive validation — designs whose successors already existed.
func TestCLI_ArchivePreLinkedDesignSucceeds(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "the successor, filed first")
	design := seedDoneOfType(t, bin, root, "design", "design linked at filing time",
		"--tags=successor:"+build)

	out, code := mgArchive(t, bin, root, "archive", design)
	if code != 0 {
		t.Fatalf("a pre-linked design was refused: exit %d\n%s", code, out)
	}
}

// TestCLI_ArchiveNonDesignIsUnaffected: the guard is deliberately narrow. It
// fires on type=design ONLY — mg-9795 was archived correctly (an agent exited;
// nothing had been recommended), and a guard that fires on ordinary completions
// is a guard that gets switched off.
func TestCLI_ArchiveNonDesignIsUnaffected(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, typ := range []string{"task", "bug", "chore"} {
		id := seedDoneOfType(t, bin, root, typ, "ordinary "+typ+" completion")
		out, code := mgArchive(t, bin, root, "archive", id)
		if code != 0 {
			t.Errorf("mg archive on a done %s was refused: exit %d\n%s", typ, code, out)
		}
	}
}

// --- --force: permitted, unoffered, recorded -------------------------------

// TestCLI_ArchiveForceWorksAndIsRecorded: some designs are genuinely abandoned
// rather than implemented, and that is a legitimate archive. But a forced
// archive must leave a trace, or "rule forced" is indistinguishable from "rule
// satisfied" to everyone who reads the archive afterwards.
func TestCLI_ArchiveForceWorksAndIsRecorded(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design we abandoned")

	out, code := mgArchive(t, bin, root, "archive", id, "--force")
	if code != 0 {
		t.Fatalf("mg archive %s --force: exit %d\n%s", id, code, out)
	}
	if !strings.Contains(out, "Archived "+id) {
		t.Errorf("output = %q, want it to report archiving %s", out, id)
	}

	data, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}
	events := string(data)
	if !strings.Contains(events, "work.archive_forced") {
		t.Errorf("forced archive left no work.archive_forced event:\n%s", events)
	}
	if !strings.Contains(events, "design_without_successor") {
		t.Errorf("forced-archive event does not record WHICH guard was bypassed:\n%s", events)
	}
}

// TestCLI_ArchiveForceIsNotRecordedForAnOrdinaryArchive: --force on an item the
// guard would have permitted anyway must NOT emit the event, or the audit trail
// fills with noise and stops meaning anything.
func TestCLI_ArchiveForceIsNotRecordedForAnOrdinaryArchive(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "task", "an ordinary task, forced for no reason")
	if out, code := mgArchive(t, bin, root, "archive", id, "--force"); code != 0 {
		t.Fatalf("mg archive %s --force: exit %d\n%s", id, code, out)
	}

	data, err := os.ReadFile(filepath.Join(root, "events.jsonl"))
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}
	if strings.Contains(string(data), "work.archive_forced") {
		t.Errorf("--force on an unguarded item emitted a forced event:\n%s", data)
	}
}

// --- The sweep must not be a bulk bypass -----------------------------------

// TestCLI_ArchiveSweepSkipsBareDesigns: `mg archive --days=0` archives
// everything past the threshold. If it took bare designs too, the targeted
// form's refusal would be one flag away from irrelevant. The sweep skips them,
// leaves them in done/ where they stay visible, and NAMES them.
func TestCLI_ArchiveSweepSkipsBareDesigns(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	bare := seedDoneOfType(t, bin, root, "design", "bare design in the sweep's path")
	ordinary := seedDone(t, bin, root, "ordinary work the sweep should take")

	out, code := mgArchive(t, bin, root, "archive", "--days=0")
	if code != 0 {
		t.Fatalf("mg archive --days=0: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Archived "+ordinary) {
		t.Errorf("sweep did not archive the ordinary item %s:\n%s", ordinary, out)
	}
	if strings.Contains(out, "Archived "+bare) {
		t.Fatalf("sweep archived the bare design %s — the guard is bypassable by --days\n%s", bare, out)
	}
	if !strings.Contains(out, bare) {
		t.Errorf("sweep skipped %s silently; it must name what it left behind:\n%s", bare, out)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, bare) {
		t.Errorf("skipped design %s is not still in done/:\n%s", bare, listOut)
	}
}

// TestCLI_ArchiveSweepRejectsGuardFlags: --successor and --force answer a guard
// that only the targeted form raises. `mg archive --days=0 --force` would be a
// bulk bypass; silently ignoring the flag would be worse still.
func TestCLI_ArchiveSweepRejectsGuardFlags(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, flag := range []string{"--force", "--successor=mg-ffff"} {
		out, code := mgArchive(t, bin, root, "archive", "--days=0", flag)
		if code != 2 {
			t.Errorf("mg archive --days=0 %s: exit %d, want 2 (usage)\n%s", flag, code, out)
		}
	}
}

// --- The preview must not promise what the mutation will refuse ------------

// TestCLI_ArchiveDryRunRefusesBareDesign: a dry run that says "Would archive"
// for an item the real run refuses has drifted from the mutation it previews.
func TestCLI_ArchiveDryRunRefusesBareDesign(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneOfType(t, bin, root, "design", "design previewed but not archivable")

	out, code := mgArchive(t, bin, root, "archive", id, "--dry-run")
	if code == 0 {
		t.Fatalf("--dry-run promised an archive the real run will refuse\n%s", out)
	}
	if strings.Contains(out, "Would archive") {
		t.Errorf("--dry-run said %q for an item the guard blocks", out)
	}
}

// TestCLI_ArchiveDryRunWritesNoSuccessorTag: the preview honours --successor to
// answer "would this work?", but must not mutate the item to find out.
func TestCLI_ArchiveDryRunWritesNoSuccessorTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	design := seedDoneOfType(t, bin, root, "design", "design previewed with a successor")
	build := seedAvailable(t, bin, root, "task", "the tracker")

	out, code := mgArchive(t, bin, root, "archive", design, "--successor="+build, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run with a valid successor: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Would archive "+design) {
		t.Errorf("output = %q, want it to preview archiving %s", out, design)
	}

	showOut, _ := mgArchive(t, bin, root, "show", design)
	if strings.Contains(showOut, "successor:"+build) {
		t.Errorf("--dry-run wrote the successor tag:\n%s", showOut)
	}
}
