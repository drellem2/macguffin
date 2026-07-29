package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moveForTest relocates an item's .md between two work/ subdirectories,
// building a store shape that the current code would not produce. It is how
// pre-fix on-disk state is reproduced without re-breaking anything live.
func moveForTest(t *testing.T, root, from, to, id string) {
	t.Helper()
	src := filepath.Join(root, "work", from, id+".md")
	dst := filepath.Join(root, "work", to, id+".md")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("moving %s from %s to %s: %v", id, from, to, err)
	}
}

// The three strands measured on 2026-07-29, reproduced as fixtures rather than
// by re-breaking the live tickets (which were unstranded by hand):
//
//	mg-459c  — parent mg-7abf merged, went done, and was archived in the same
//	           cycle. The dependent never released.
//	mg-344a  — pending on mg-7d8a, "GATE: pause all one-third work until Mon
//	mg-b8f9    2026-07-14", which was shelved. Both sat two weeks behind a gate
//	           everyone had stopped honouring.
//
// Each test constructs the strand and asserts it does not happen. A fix
// verified only on the happy path has not been verified against this defect.

// mustStatus fails the test if the item cannot be located.
func mustStatus(t *testing.T, root, id string) string {
	t.Helper()
	status, err := Status(root, id)
	if err != nil {
		t.Fatalf("Status(%s): %v", id, err)
	}
	return status
}

// completeAndArchive drives a parent all the way through done into archive/,
// which is the mg-459c sequence: merge, done, archive in the same cycle.
func completeAndArchive(t *testing.T, root, id string) {
	t.Helper()
	if _, err := Claim(root, id, 0); err != nil {
		t.Fatalf("Claim(%s): %v", id, err)
	}
	if _, _, err := Done(root, id, nil); err != nil {
		t.Fatalf("Done(%s): %v", id, err)
	}
	if _, err := ArchiveItem(root, id, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem(%s): %v", id, err)
	}
	if got := mustStatus(t, root, id); got != "archived" {
		t.Fatalf("parent %s status = %q, want archived", id, got)
	}
}

// TestStrand_ChildFiledAfterParentArchived is the mg-459c strand. The dependent
// is filed AFTER the parent has already merged, completed, and been archived —
// so no future Done sweep is owed to it. Under the old placement rule it went
// to pending/ regardless and waited for a completion that had already happened,
// released only if some unrelated item happened to complete later. That is the
// "timing luck" the two surviving pairs in the same chain ran on.
func TestStrand_ChildFiledAfterParentArchived(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "merge the thing", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	completeAndArchive(t, root, parent.ID)

	child, err := Create(root, "mg-", "audit", "audit the merged thing", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// The dependency is already satisfied, so the item is not waiting on
	// anything. It must be available immediately, not parked behind a sweep.
	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("child of archived parent status = %q, want available (stranded in pending/)", got)
	}

	// And it must not need a sweep to get there.
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("expected the child to be placed at filing time, but Schedule promoted %d item(s)", len(promoted))
	}
}

// TestStrand_ChildFiledAfterParentDone covers the same shape one step earlier:
// the parent is done but not yet archived. Archiving must not be what decides
// this, and neither must a later unrelated completion.
func TestStrand_ChildFiledAfterParentDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "merge the thing", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := Claim(root, parent.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, parent.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	child, err := Create(root, "mg-", "audit", "audit the merged thing", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("child of done parent status = %q, want available", got)
	}
}

// TestStrand_PreFiledChildSurvivesParentArchive is the pre-filed-audit pattern
// running end to end: the audit exists BEFORE the parent completes, the parent
// merges and is archived in the same cycle, and the audit releases. This is the
// path that worked only on timing luck; it must now work on either ordering.
func TestStrand_PreFiledChildSurvivesParentArchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "merge the thing", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "audit", "pre-filed audit", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("pre-filed child status = %q, want pending (parent is still live)", got)
	}

	completeAndArchive(t, root, parent.ID)

	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("pre-filed child status = %q, want available after parent done+archived", got)
	}
}

// TestStrand_ArchivedParentStaysSatisfied pins the semantics directly: a parent
// that has passed through done satisfies the dependency whatever its current
// state. Archiving is a filing decision about completed work, not a
// repudiation of the completion.
func TestStrand_ArchivedParentStaysSatisfied(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "the parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	completeAndArchive(t, root, parent.ID)

	deps, err := classifyDeps(root, []string{parent.ID})
	if err != nil {
		t.Fatalf("classifyDeps: %v", err)
	}
	if deps[0].State != DepSatisfied {
		t.Errorf("archived parent dep state = %q, want %q", deps[0].State, DepSatisfied)
	}
	if !deps[0].Reachable() {
		t.Error("archived parent should be reachable")
	}
}

// TestStrand_ChildFiledOnShelvedGate is the mg-344a / mg-b8f9 strand: two
// dependents filed onto mg-7d8a, a shelved time gate. A shelved parent never
// reaches done, so `pending` was a promise the store could not keep.
//
// Implemented semantics: shelving means "not now", NOT "cancelled". The
// dependent is filed as SHELVED alongside its parent — not released, and not
// left masquerading as a pending item that is waiting correctly. This is the
// same treatment Shelve already gives dependents that exist when the parent is
// shelved; filing was the last door left open.
func TestStrand_ChildFiledOnShelvedGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	gate, err := Create(root, "mg-", "task", "GATE: pause all one-third work until Mon 2026-07-14", nil)
	if err != nil {
		t.Fatalf("Create gate: %v", err)
	}
	if _, err := Shelve(root, gate.ID); err != nil {
		t.Fatalf("Shelve gate: %v", err)
	}

	var children []*Item
	for _, title := range []string{"one-third deliverable A", "one-third deliverable B"} {
		c, err := Create(root, "mg-", "task", title, []string{gate.ID})
		if err != nil {
			t.Fatalf("Create %q: %v", title, err)
		}
		children = append(children, c)
	}

	for _, c := range children {
		if got := mustStatus(t, root, c.ID); got != "shelved" {
			t.Errorf("child %s of shelved gate status = %q, want shelved (a pending child of a shelved parent waits forever)", c.ID, got)
		}
	}

	// Not stranded: nothing is sitting in pending/ pretending to wait.
	stranded, err := Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 0 {
		t.Errorf("expected no strands, got %d: %v", len(stranded), stranded)
	}

	// A sweep must not release them either — shelved is not a release.
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("Schedule released %d child(ren) of a shelved parent; shelving is not a completion", len(promoted))
	}
}

// TestStrand_ShelvedGateRoundTrip proves the chosen semantics are recoverable:
// lifting the gate brings the dependents back as pending, and completing the
// gate then releases them normally. Shelving parks work; it does not destroy
// the chain.
func TestStrand_ShelvedGateRoundTrip(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	gate, err := Create(root, "mg-", "task", "GATE: pause until Monday", nil)
	if err != nil {
		t.Fatalf("Create gate: %v", err)
	}
	if _, err := Shelve(root, gate.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	child, err := Create(root, "mg-", "task", "gated deliverable", []string{gate.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// The gate's date passes; someone lifts it.
	if _, err := Unshelve(root, gate.ID); err != nil {
		t.Fatalf("Unshelve: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("child status after gate unshelved = %q, want pending", got)
	}

	// Completing the gate releases the dependent through the normal path.
	if _, err := Claim(root, gate.ID, 0); err != nil {
		t.Fatalf("Claim gate: %v", err)
	}
	if _, _, err := Done(root, gate.ID, nil); err != nil {
		t.Fatalf("Done gate: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "available" {
		t.Errorf("child status after gate done = %q, want available", got)
	}
}

// TestStranded_DetectsHistoricalShelvedStrand covers the items already on disk
// when this fix lands. Placement stops NEW strands; it cannot move the ones
// that were filed under the old rule. Those are found by the detector.
func TestStranded_DetectsHistoricalShelvedStrand(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	gate, err := Create(root, "mg-", "task", "GATE: pause until Monday", nil)
	if err != nil {
		t.Fatalf("Create gate: %v", err)
	}
	// A dependent filed while the gate was still live lands in pending/...
	child, err := Create(root, "mg-", "task", "gated deliverable", []string{gate.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	// ...and is moved to shelved/ with the gate by Shelve's cascade. Put it
	// back in pending/ to reproduce a store that predates this fix: a pending
	// item whose parent is shelved.
	if _, err := Shelve(root, gate.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	moveForTest(t, root, "shelved", "pending", child.ID)

	stranded, err := Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 1 {
		t.Fatalf("Stranded returned %d item(s), want 1", len(stranded))
	}
	if stranded[0].Item.ID != child.ID {
		t.Errorf("stranded ID = %q, want %q", stranded[0].Item.ID, child.ID)
	}
	// The report must NAME the parent responsible — an unattributed count is
	// the guesswork the strand caused in the first place.
	reason := stranded[0].Reason()
	if !strings.Contains(reason, gate.ID) || !strings.Contains(reason, "shelved") {
		t.Errorf("Reason() = %q, want it to name %s as shelved", reason, gate.ID)
	}

	// The sweep must leave it alone: it cannot be promoted, only reported.
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("Schedule promoted %d item(s) behind a shelved parent", len(promoted))
	}
}

// TestStranded_DetectsUnknownParent covers the same silent-and-permanent shape
// reached by a different route: a dependency on an ID that does not exist —
// a typo, or a parent removed out from under the dependent. Placement does not
// refuse it, so the detector must name it.
func TestStranded_DetectsUnknownParent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	child, err := Create(root, "mg-", "task", "waits on a ghost", []string{"mg-ffff"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("status = %q, want pending", got)
	}

	stranded, err := Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 1 {
		t.Fatalf("Stranded returned %d item(s), want 1", len(stranded))
	}
	if reason := stranded[0].Reason(); !strings.Contains(reason, "mg-ffff") || !strings.Contains(reason, "does not exist") {
		t.Errorf("Reason() = %q, want it to name mg-ffff as nonexistent", reason)
	}
}

// TestStranded_IgnoresCorrectlyWaitingItem is the other half of the detector's
// contract. `pending` on a live parent is exactly what a correctly-waiting item
// looks like, and reporting those would drown the real strands — the detector
// would then need a reader willing to ignore it, which is no detector at all.
func TestStranded_IgnoresCorrectlyWaitingItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "live parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := Create(root, "mg-", "task", "waiting correctly", []string{parent.ID}); err != nil {
		t.Fatalf("Create child: %v", err)
	}

	stranded, err := Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 0 {
		t.Errorf("a dependent of a live parent is waiting, not stranded; got %d strand(s)", len(stranded))
	}

	// Also true while the parent is claimed — mid-flight is still reachable.
	if _, err := Claim(root, parent.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	stranded, err = Stranded(root)
	if err != nil {
		t.Fatalf("Stranded: %v", err)
	}
	if len(stranded) != 0 {
		t.Errorf("a dependent of a claimed parent is waiting, not stranded; got %d strand(s)", len(stranded))
	}
}

// TestStrand_MixedDepsShelvedWins pins precedence when a dependent names both a
// satisfied parent and a shelved one: it is parked, not released. Releasing on
// a partial answer is how a chain loses a gate.
func TestStrand_MixedDepsShelvedWins(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	doneParent, err := Create(root, "mg-", "task", "finished parent", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	completeAndArchive(t, root, doneParent.ID)

	gate, err := Create(root, "mg-", "task", "GATE", nil)
	if err != nil {
		t.Fatalf("Create gate: %v", err)
	}
	if _, err := Shelve(root, gate.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	child, err := Create(root, "mg-", "task", "needs both", []string{doneParent.ID, gate.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "shelved" {
		t.Errorf("status = %q, want shelved (one dep satisfied, one shelved)", got)
	}
}
