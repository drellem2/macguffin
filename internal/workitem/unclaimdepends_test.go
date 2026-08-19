package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claimItem moves an item into claimed/ under this process's PID, the way the
// tests in unclaim_test.go simulate a claim.
func claimItem(t *testing.T, root, id string) string {
	t.Helper()
	src := filepath.Join(root, "work", "available", id+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", id, os.Getpid()))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim of %s: %v", id, err)
	}
	return dst
}

// TestUnclaimHoldsAnItemWhoseDependencyIsUnmet is the mg-e7ff regression, in
// the exact order of operations that produced it: the item is CLAIMED when the
// dependency is added — which deliberately does not demote it, because a worker
// is on it — and the release is therefore the first moment anything could read
// the edge. It used to read nothing.
func TestUnclaimHoldsAnItemWhoseDependencyIsUnmet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "the parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "the child", nil)
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// The parent is live (claimed), so the dependency is unmet and reachable.
	claimItem(t, root, parent.ID)
	claimed := claimItem(t, root, child.ID)

	// The edge lands while the child is claimed. Edit does not demote a claimed
	// item; that is correct and is precisely what leaves the release holding
	// the whole gate.
	if _, err := Update(root, child.ID, UpdateField{AddDepends: []string{parent.ID}}); err != nil {
		t.Fatalf("Update --add-depends: %v", err)
	}
	if _, err := os.Stat(claimed); err != nil {
		t.Fatalf("adding a dependency moved a CLAIMED item; it must not: %v", err)
	}

	res, err := Unclaim(root, child.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}

	if res.Status != "pending" {
		t.Errorf("res.Status = %q, want %q — the release returned a gated item to the dispatchable pool", res.Status, "pending")
	}
	if _, err := os.Stat(filepath.Join(root, "work", "pending", child.ID+".md")); err != nil {
		t.Errorf("item is not in pending/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", child.ID+".md")); !os.IsNotExist(err) {
		t.Errorf("item is in available/ with an unmet dependency (err=%v) — this is mg-e7ff", err)
	}

	// The reason has to be carried, not merely the placement: an operator who
	// releases a claim and watches the item vanish into pending/ needs the
	// parent named, and it is the same rendering `mg schedule` prints.
	reason := res.Held.Gates(time.Now().UTC())
	if !strings.Contains(reason, parent.ID) {
		t.Errorf("hold reason %q does not name the parent %s", reason, parent.ID)
	}
	if !strings.Contains(reason, "(claimed)") {
		t.Errorf("hold reason %q does not say what the parent is doing", reason)
	}
}

// A met dependency must not hold anything: the overwhelmingly common release is
// of an item whose parents are done, and it still goes straight to available/.
func TestUnclaimReleasesWhenEveryDependencyIsMet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "the parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	claimItem(t, root, parent.ID)
	if _, _, err := Done(root, parent.ID, nil); err != nil {
		t.Fatalf("Done parent: %v", err)
	}

	child, err := Create(root, "mg-", "task", "the child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	claimItem(t, root, child.ID)

	res, err := Unclaim(root, child.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.Status != "available" {
		t.Errorf("res.Status = %q, want %q", res.Status, "available")
	}
	if res.Held.Closed() {
		t.Errorf("a met dependency reported a closed gate: %+v", res.Held)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", child.ID+".md")); err != nil {
		t.Errorf("item not in available/: %v", err)
	}
}

// The snooze gate is the other half of gateOpen, and it was equally unread by
// the release path. A claimed item with a future wake time must not come back
// into the dispatchable pool ahead of its own schedule.
func TestUnclaimHoldsAnItemWhoseSnoozeHasNotElapsed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	snoozeAt(t, now)

	item, err := Create(root, "mg-", "task", "snoozed and claimed", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Stamp the gate directly: `mg snooze` would move it to pending/, and the
	// state under test is a CLAIMED item carrying a future wake time.
	claimed := claimItem(t, root, item.ID)
	parsed, err := readFile(claimed)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	parsed.SetSnooze(now.Add(6 * time.Hour))
	if err := os.WriteFile(claimed, []byte(Render(parsed)), 0o644); err != nil {
		t.Fatalf("stamping snooze: %v", err)
	}

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.Status != "pending" {
		t.Errorf("res.Status = %q, want %q — a future wake time did not hold the release", res.Status, "pending")
	}
	if !res.Held.Snoozed {
		t.Errorf("hold does not report the snooze gate: %+v", res.Held)
	}
}

// The event has to record where the item actually went. It carried a hardcoded
// to_status=available, which was true only while the destination was.
func TestUnclaimEventRecordsTheDirectoryItLandedIn(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "the parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "the child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	// Create places a dependent with an unmet parent in pending/; move it into
	// claimed/ from there.
	src := filepath.Join(root, "work", "pending", child.ID+".md")
	dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", child.ID, os.Getpid()))
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulating claim: %v", err)
	}

	if _, err := Unclaim(root, child.ID); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.unclaim")
	if e.Extra["to_status"] != "pending" {
		t.Errorf("to_status = %q, want pending — the log says the item went somewhere it did not", e.Extra["to_status"])
	}
	if !strings.Contains(e.Extra["held_by"], parent.ID) {
		t.Errorf("held_by = %q, want it to name %s", e.Extra["held_by"], parent.ID)
	}
}

// Undemoted is the detector for the state the bug produced: an item in
// available/ whose gate is closed. It must find one, and must stay quiet over a
// consistent store.
func TestUndemotedFindsAGatedItemInAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, err := Create(root, "mg-", "task", "the parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	claimItem(t, root, parent.ID)

	clean, err := Create(root, "mg-", "task", "no gate at all", nil)
	if err != nil {
		t.Fatalf("Create clean: %v", err)
	}

	misplaced, err := Undemoted(root)
	if err != nil {
		t.Fatalf("Undemoted: %v", err)
	}
	if len(misplaced) != 0 {
		t.Fatalf("Undemoted reported %d item(s) over a consistent store: %+v", len(misplaced), misplaced)
	}

	// Now produce the inconsistency by hand — which is the only way it can
	// arise once every placement path consults the gate.
	gated, err := Create(root, "mg-", "task", "gated but available", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create gated: %v", err)
	}
	if err := os.Rename(
		filepath.Join(root, "work", "pending", gated.ID+".md"),
		filepath.Join(root, "work", "available", gated.ID+".md"),
	); err != nil {
		t.Fatalf("producing the inconsistency: %v", err)
	}

	misplaced, err = Undemoted(root)
	if err != nil {
		t.Fatalf("Undemoted: %v", err)
	}
	if len(misplaced) != 1 {
		t.Fatalf("Undemoted found %d item(s), want 1: %+v", len(misplaced), misplaced)
	}
	if misplaced[0].Item.ID != gated.ID {
		t.Errorf("Undemoted named %s, want %s", misplaced[0].Item.ID, gated.ID)
	}
	if got := misplaced[0].Gates(time.Now().UTC()); !strings.Contains(got, parent.ID) {
		t.Errorf("gate description %q does not name the parent %s", got, parent.ID)
	}
	_ = clean

	// And it is a DETECTOR: nothing moved.
	if _, err := os.Stat(filepath.Join(root, "work", "available", gated.ID+".md")); err != nil {
		t.Errorf("Undemoted moved the item it reported; it must only report: %v", err)
	}
}
