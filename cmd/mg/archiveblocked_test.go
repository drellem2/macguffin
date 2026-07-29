package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The blocked-on tag guard (mg-3c53). A `blocked-on-*` tag says a person still
// owes something on this item; `mg archive` discards the item. Before this, the
// only tag archive had ever been taught to read was `successor:`, and only on
// `type: design` — so a done `type: task` tagged `blocked-on-daniel` archived
// silently, and the tag that says "someone still owes something here" was
// invisible to the one operation that throws the record away.
//
// The general form: A GUARD WHOSE POPULATION IS NAMED BY ONE ATTRIBUTE CANNOT
// SEE THE SAME DEFECT WEARING ANOTHER. The successor guard's population is
// named by TYPE because it was built from an instance that happened to be a
// design. mg-cf48 is the identical failure one type over.
//
// The ORDER below is the point, as with the successor guard next door. THE
// REFUSAL IS PROVEN FIRST. A guard that silently allows and a guard that
// correctly found nothing to block are the same observation from outside, and
// the failure mode under test — losing real work — is silent and permanent.
// Only after the guard has been seen to fire do the passing controls run.

// seedDoneTagged creates a done item of the given type carrying tags, and
// returns its id.
func seedDoneTagged(t *testing.T, bin, root, typ, title string, tags ...string) string {
	t.Helper()
	return seedDoneOfType(t, bin, root, typ, title, "--tags="+strings.Join(tags, ","))
}

// --- The refusal, proven first ---------------------------------------------

// TestCLI_ArchiveBlockedOnTagIsRefused is the positive control and the mg-cf48
// regression: a done item tagged blocked-on-daniel must FAIL, loudly, and must
// still be in done/ afterwards.
func TestCLI_ArchiveBlockedOnTagIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "done, but a person still owes a ruling", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("mg archive on an item tagged blocked-on-daniel exited 0 — the guard did not fire\n%s", out)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s left done/ despite the refusal:\n%s", id, listOut)
	}
}

// TestCLI_ArchiveBlockedRefusalNamesTheTag: "blocked" and "no successor" are two
// different states with two different remedies. A refusal that does not name the
// tag it found tells the operator neither which obligation is outstanding nor
// who owes it, and cannot be told apart from the successor refusal.
func TestCLI_ArchiveBlockedRefusalNamesTheTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "waiting on a named person", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("expected refusal, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("refusal = %q, want it to name the tag it found", out)
	}
	if strings.Contains(out, "successor") {
		t.Errorf("refusal = %q talks about successors; the operator cannot tell which guard fired or which remedy applies", out)
	}
	if !strings.Contains(out, "--rm-tags") {
		t.Errorf("refusal = %q, want it to name the remedy that discharges the tag", out)
	}
}

// TestCLI_ArchiveBlockedRefusalIsExitFour pins the taxonomy: the item exists
// and is done but is in the wrong state for this operation — conflict, exit 4,
// the same class as the successor refusal. Not exit 2: the caller did not
// misuse the CLI. (The machine code slug, blocked_on_tag, is pinned by the
// forced-archive event test below, which is where it becomes observable.)
func TestCLI_ArchiveBlockedRefusalIsExitFour(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "conflict, not usage", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code != 4 {
		t.Fatalf("mg archive on a blocked item: exit %d, want 4 (conflict)\n%s", code, out)
	}
}

// TestCLI_ArchiveBlockedRefusalDoesNotOfferForce: --force applies to this guard
// (see below), but an agent that hits a refusal mid-cleanup, at speed, reaches
// for whatever the error message hands it. A guard whose own failure text
// teaches the bypass is decorative. It must nevertheless BE documented.
func TestCLI_ArchiveBlockedRefusalDoesNotOfferForce(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "a refusal that must not teach its own bypass", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("expected refusal, got exit 0\n%s", out)
	}
	if strings.Contains(out, "--force") {
		t.Errorf("refusal offers --force, making the guard decorative: %q", out)
	}

	help, code := mgArchive(t, bin, root, "archive", "--help")
	if code != 0 {
		t.Fatalf("mg archive --help: exit %d\n%s", code, help)
	}
	if !strings.Contains(help, "blocked-on-") {
		t.Errorf("`mg archive --help` does not document the blocked-on guard:\n%s", help)
	}
}

// TestCLI_ArchiveBlockedOnFiresForEveryType: the tag, not the type, names the
// population. Scoping this to one type would rebuild the exact defect it exists
// to fix — a guard that can only see the defect wearing the attribute the first
// instance happened to have.
func TestCLI_ArchiveBlockedOnFiresForEveryType(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, typ := range []string{"task", "bug", "chore"} {
		id := seedDoneTagged(t, bin, root, typ, "blocked "+typ, "blocked-on-daniel")
		out, code := mgArchive(t, bin, root, "archive", id)
		if code == 0 {
			t.Errorf("a done %s tagged blocked-on-daniel archived anyway\n%s", typ, out)
		}
	}
}

// TestCLI_ArchiveBlockedOnSuffixVariantsAreRefused: the convention is a family,
// not one string — blocked-on-daniel-confirm is live on mg-a96c. The check is a
// prefix, so a new person or a new qualifier needs no code change.
func TestCLI_ArchiveBlockedOnSuffixVariantsAreRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, tag := range []string{"blocked-on-daniel-confirm", "blocked-on-legal", "Blocked-On-Daniel"} {
		id := seedDoneTagged(t, bin, root, "task", "tagged "+tag, tag)
		out, code := mgArchive(t, bin, root, "archive", id)
		if code == 0 {
			t.Errorf("%s did not fire the guard\n%s", tag, out)
		}
	}
}

// TestCLI_ArchiveBlockedOnAmongOtherTagsIsRefused: real items carry several
// tags. The guard must find the blocked-on one wherever it sits, and name only
// it — the other tags are not the reason and quoting them back would bury it.
func TestCLI_ArchiveBlockedOnAmongOtherTagsIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "one blocked tag among several",
		"infra", "blocked-on-daniel", "cleanup")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code == 0 {
		t.Fatalf("the guard missed a blocked-on tag that was not first\n%s", out)
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("refusal = %q, want it to name the blocked-on tag", out)
	}
}

// TestCLI_ArchiveSuccessorDoesNotBypassBlocked: --successor answers the
// successor guard and no other. Naming a tracker for a recommendation says
// nothing about whether a person still owes something on this item, and letting
// it return early would hand out a one-flag bypass of a guard it never
// addressed. This is the seam the two guards share, so it is tested directly.
func TestCLI_ArchiveSuccessorDoesNotBypassBlocked(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "the tracker for the recommendation")
	design := seedDoneTagged(t, bin, root, "design", "tracked design that is also blocked", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", design, "--successor="+build)
	if code == 0 {
		t.Fatalf("--successor archived an item tagged blocked-on-daniel\n%s", out)
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("refusal = %q, want it to name the guard that actually fired", out)
	}

	// Same seam in the preview.
	out, code = mgArchive(t, bin, root, "archive", design, "--successor="+build, "--dry-run")
	if code == 0 {
		t.Fatalf("--dry-run --successor promised an archive the real run refuses\n%s", out)
	}
}

// TestCLI_ArchiveDryRunRefusesBlockedItem: a preview that says "Would archive"
// for an item the mutation refuses has drifted from the mutation it previews.
func TestCLI_ArchiveDryRunRefusesBlockedItem(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "previewed but not archivable", "blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "archive", id, "--dry-run")
	if code == 0 {
		t.Fatalf("--dry-run promised an archive the real run will refuse\n%s", out)
	}
	if strings.Contains(out, "Would archive") {
		t.Errorf("--dry-run said %q for an item the guard blocks", out)
	}
}

// --- The other half: the guard must PERMIT ---------------------------------

// TestCLI_ArchiveUntaggedItemStillArchives is the converse control. A guard that
// refuses everything is indistinguishable from a broken tool, and the sweep is
// routine cleanup that must keep working.
func TestCLI_ArchiveUntaggedItemStillArchives(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDone(t, bin, root, "ordinary done work with no tags")

	out, code := mgArchive(t, bin, root, "archive", id)
	if code != 0 {
		t.Fatalf("an untagged done item was refused: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Archived "+id) {
		t.Errorf("output = %q, want it to report archiving %s", out, id)
	}
}

// TestCLI_ArchiveDesignWithSuccessorStillArchives: no regression to the guard
// this one was modelled on. A tracked design with no blocked-on tag archives
// exactly as before.
func TestCLI_ArchiveDesignWithSuccessorStillArchives(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "build what the design recommends")
	design := seedDoneOfType(t, bin, root, "design", "design whose build is tracked")

	out, code := mgArchive(t, bin, root, "archive", design, "--successor="+build)
	if code != 0 {
		t.Fatalf("a tracked design was refused: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Archived "+design) {
		t.Errorf("output = %q, want it to report archiving %s", out, design)
	}
}

// TestCLI_ArchiveUnrelatedTagsAreUnaffected: the trigger is the blocked-on-
// prefix and nothing adjacent to it. A guard that fires on ordinary tags is a
// guard that gets switched off.
func TestCLI_ArchiveUnrelatedTagsAreUnaffected(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, tag := range []string{"blocked", "unblocked", "needs-daniel", "was-blocked-on-daniel", "gh-issue-blocked"} {
		id := seedDoneTagged(t, bin, root, "task", "tagged "+tag, tag)
		out, code := mgArchive(t, bin, root, "archive", id)
		if code != 0 {
			t.Errorf("tag %q blocked an archive it should not: exit %d\n%s", tag, code, out)
		}
	}
}

// TestCLI_ArchiveAfterRemovingTheTagSucceeds proves the remedy the refusal
// names actually works end to end: settle the obligation, drop the tag, archive.
// A hint that does not resolve the refusal is worse than no hint.
func TestCLI_ArchiveAfterRemovingTheTagSucceeds(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "blocked until the ruling lands", "blocked-on-daniel")

	if _, code := mgArchive(t, bin, root, "archive", id); code == 0 {
		t.Fatalf("guard did not fire, so this test proves nothing about the remedy")
	}

	if out, code := mgArchive(t, bin, root, "edit", id, "--rm-tags=blocked-on-daniel"); code != 0 {
		t.Fatalf("mg edit --rm-tags: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "archive", id)
	if code != 0 {
		t.Fatalf("removing the tag did not make the item archivable: exit %d\n%s", code, out)
	}
}

// --- --force: permitted, unoffered, recorded -------------------------------

// TestCLI_ArchiveBlockedForceWorksAndIsRecorded states the --force decision.
// --force DOES apply to this guard, for the same reason it applies to the
// successor guard: an obligation can be discharged out of band and the tag left
// behind, and without a recorded escape hatch the operator strips the tag by
// hand — the same bypass with none of the audit trail. The forced archive must
// leave a trace naming WHICH guard was bypassed.
func TestCLI_ArchiveBlockedForceWorksAndIsRecorded(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedDoneTagged(t, bin, root, "task", "block settled out of band, tag never removed", "blocked-on-daniel")

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
	if !strings.Contains(events, "blocked_on_tag") {
		t.Errorf("forced-archive event does not record WHICH guard was bypassed:\n%s", events)
	}
}

// --- The sweep must not be a bulk bypass -----------------------------------

// TestCLI_ArchiveSweepSkipsBlockedItems: `mg archive --days=0` archives
// everything past the threshold. If it took blocked items too, the targeted
// form's refusal would be one flag away from irrelevant.
func TestCLI_ArchiveSweepSkipsBlockedItems(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	blocked := seedDoneTagged(t, bin, root, "task", "blocked item in the sweep's path", "blocked-on-daniel")
	ordinary := seedDone(t, bin, root, "ordinary work the sweep should take")

	out, code := mgArchive(t, bin, root, "archive", "--days=0")
	if code != 0 {
		t.Fatalf("mg archive --days=0: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Archived "+ordinary) {
		t.Errorf("sweep did not archive the ordinary item %s:\n%s", ordinary, out)
	}
	if strings.Contains(out, "Archived "+blocked) {
		t.Fatalf("sweep archived the blocked item %s — the guard is bypassable by --days\n%s", blocked, out)
	}
	if !strings.Contains(out, blocked) {
		t.Errorf("sweep skipped %s silently; it must name what it left behind:\n%s", blocked, out)
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("sweep did not say WHY it skipped %s:\n%s", blocked, out)
	}

	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if !strings.Contains(listOut, blocked) {
		t.Errorf("skipped item %s is not still in done/:\n%s", blocked, listOut)
	}
}

// TestCLI_ArchiveSweepDistinguishesItsSkipReasons: two guards, two remedies. A
// sweep that lists both kinds under one summary line makes the operator guess
// which fix applies to which id.
func TestCLI_ArchiveSweepDistinguishesItsSkipReasons(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	blocked := seedDoneTagged(t, bin, root, "task", "waiting on a person", "blocked-on-daniel")
	bare := seedDoneOfType(t, bin, root, "design", "design nothing tracks")

	out, code := mgArchive(t, bin, root, "archive", "--days=0", "--dry-run")
	if code != 0 {
		t.Fatalf("mg archive --days=0 --dry-run: exit %d\n%s", code, out)
	}
	for _, id := range []string{blocked, bare} {
		if !strings.Contains(out, id) {
			t.Errorf("sweep preview did not name skipped item %s:\n%s", id, out)
		}
		if strings.Contains(out, "Would archive "+id) {
			t.Errorf("sweep preview promised to archive guarded item %s:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("sweep preview does not name the blocked-on reason:\n%s", out)
	}
	if !strings.Contains(out, "successor") {
		t.Errorf("sweep preview does not name the successor reason:\n%s", out)
	}

	// Both must still be in done/: --dry-run moves nothing.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	for _, id := range []string{blocked, bare} {
		if !strings.Contains(listOut, id) {
			t.Errorf("%s left done/ during a dry run:\n%s", id, listOut)
		}
	}
}
