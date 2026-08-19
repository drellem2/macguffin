package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The end-to-end half of mg-e7ff, in the exact order of operations that
// produced it. The unit tests next door
// (internal/workitem/unclaimdepends_test.go) pin the placement rule; this one
// pins the operator-visible sequence, because the whole finding is that nothing
// errored and nothing said anything.

// seedNew creates an item and returns its ID. --no-repo because a test root has
// no repo to record and mg refuses an ephemeral one.
func seedNew(t *testing.T, bin, root, title string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "new", "--title="+title, "--no-repo")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	_, rest, ok := strings.Cut(out, "Created ")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	return strings.TrimSpace(id)
}

// TestCLI_UnclaimHoldsAnItemWithAnUnmetDependency walks the measured sequence:
// claim the child, add the dependency while it is claimed (which correctly does
// not demote it), then release. Before mg-e7ff the release put it straight into
// available/, where stall-watch and priority-wake advertised it as ready.
func TestCLI_UnclaimHoldsAnItemWithAnUnmetDependency(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	parent := seedNew(t, bin, root, "the parent nobody has finished")
	child := seedNew(t, bin, root, "the child that must wait")

	// The parent is claimed — live, unmet, and reachable.
	if out, code := mgArchive(t, bin, root, "claim", parent); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", parent, code, out)
	}
	if out, code := mgArchive(t, bin, root, "claim", child); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", child, code, out)
	}
	if out, code := mgArchive(t, bin, root, "edit", child, "--add-depends="+parent); code != 0 {
		t.Fatalf("mg edit --add-depends: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "unclaim", child)
	if code != 0 {
		t.Fatalf("mg unclaim %s: exit %d\n%s", child, code, out)
	}
	if !strings.Contains(out, "pending/") {
		t.Errorf("unclaim output = %q, want it to say the item is held in pending/", out)
	}
	if !strings.Contains(out, parent) {
		t.Errorf("unclaim output = %q, want it to name the parent %s that held it", out, parent)
	}

	// The store, not the message, is the claim being made.
	avail, _ := mgArchive(t, bin, root, "list", "--status=available")
	if strings.Contains(avail, child) {
		t.Errorf("%s reached available/ with an unmet dependency — this is mg-e7ff:\n%s", child, avail)
	}
	pending, _ := mgArchive(t, bin, root, "list", "--status=pending")
	if !strings.Contains(pending, child) {
		t.Errorf("%s is not in pending/ after the release:\n%s", child, pending)
	}

	// And the sweep can now see it, which it could not before: its held report
	// reads pending/, and the item was not there to be read.
	sched, _ := mgArchive(t, bin, root, "schedule")
	if !strings.Contains(sched, child) {
		t.Errorf("`mg schedule` does not mention %s:\n%s", child, sched)
	}
}

// The population control. An ordinary release — no dependency, no snooze — must
// still land in available/ and say nothing extra, or the line above is noise on
// every claim the fleet releases.
func TestCLI_UnclaimIsSilentAndAvailableForAnUngatedItem(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedNew(t, bin, root, "an ordinary task")
	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}

	out, code := mgArchive(t, bin, root, "unclaim", id)
	if code != 0 {
		t.Fatalf("mg unclaim: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "pending/") {
		t.Errorf("output = %q, want no hold note on an ungated release", out)
	}
	avail, _ := mgArchive(t, bin, root, "list", "--status=available")
	if !strings.Contains(avail, id) {
		t.Errorf("%s did not reach available/:\n%s", id, avail)
	}
}

// TestCLI_ScheduleReportsAGatedItemSittingInAvailable is scope 3: the
// reconciliation check. It is the detector that would have found this without
// somebody noticing by eye that two items with the same dependency were in
// different directories.
func TestCLI_ScheduleReportsAGatedItemSittingInAvailable(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	parent := seedNew(t, bin, root, "the parent")
	child := seedNew(t, bin, root, "the child")

	if out, code := mgArchive(t, bin, root, "claim", parent); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", parent, code, out)
	}

	// A consistent store must be quiet, or the warning is ignored by the time
	// it matters.
	clean, code := mgArchive(t, bin, root, "schedule")
	if code != 0 {
		t.Fatalf("mg schedule: exit %d\n%s", code, clean)
	}
	if strings.Contains(clean, "CLOSED gate") {
		t.Errorf("`mg schedule` warned over a consistent store:\n%s", clean)
	}

	// Produce the inconsistency the way the release used to: claim the child,
	// add the dependency, and put it in available/ by hand — which is what the
	// old release path did.
	if out, code := mgArchive(t, bin, root, "claim", child); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", child, code, out)
	}
	if out, code := mgArchive(t, bin, root, "edit", child, "--add-depends="+parent); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}
	if out, code := mgArchive(t, bin, root, "unclaim", child); code != 0 {
		t.Fatalf("mg unclaim: exit %d\n%s", code, out)
	}
	// It is in pending/ now (that is the fix). Force it into available/ to
	// stand in for a hand-edit, a crash mid-rename, or a path nobody found.
	if err := movePendingToAvailable(root, child); err != nil {
		t.Fatalf("producing the inconsistency: %v", err)
	}

	out, code := mgArchive(t, bin, root, "schedule")
	if code != 0 {
		t.Fatalf("mg schedule: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "CLOSED gate") {
		t.Errorf("`mg schedule` did not report the gated item in available/:\n%s", out)
	}
	if !strings.Contains(out, child) {
		t.Errorf("`mg schedule` did not name %s:\n%s", child, out)
	}
	if !strings.Contains(out, parent) {
		t.Errorf("`mg schedule` did not name the gate %s:\n%s", parent, out)
	}
	// A detector, not a repair: the item must still be where it was found.
	avail, _ := mgArchive(t, bin, root, "list", "--status=available")
	if !strings.Contains(avail, child) {
		t.Errorf("`mg schedule` moved the item it reported; it must only report:\n%s", avail)
	}
}

// movePendingToAvailable renames one item out of pending/ into available/,
// standing in for whatever produced the inconsistency the detector reports on.
func movePendingToAvailable(root, id string) error {
	return os.Rename(
		filepath.Join(root, "work", "pending", id+".md"),
		filepath.Join(root, "work", "available", id+".md"),
	)
}
