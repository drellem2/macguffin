package workitem

import (
	"strings"
	"testing"
	"time"
)

// The defect this file pins: the sweep reported the snooze gate and stayed
// silent about the dependency gate, so a pending set held entirely by
// dependencies produced "No items promoted." and nothing else. Held is the
// reader for the WHOLE held population, so each gate is asserted alone and
// both together.

// A dependency-only hold is the case that was invisible. It must be listed,
// naming the parent and what the parent is actually doing.
func TestHeld_DependencyOnlyHoldIsListed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	snoozeAt(t, time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC))

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := Claim(root, parent.ID, 0); err != nil {
		t.Fatalf("Claim parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if got := mustStatus(t, root, child.ID); got != "pending" {
		t.Fatalf("child status = %q, want pending", got)
	}

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("Held returned %d items, want 1 (the dependency-gated child)", len(held))
	}
	h := held[0]
	if h.Item.ID != child.ID {
		t.Errorf("held item = %s, want the child %s", h.Item.ID, child.ID)
	}
	if h.Snoozed || h.BadSnooze != "" {
		t.Errorf("an item with no snooze must not report a snooze gate: %+v", h)
	}
	gates := h.Gates(snoozeNow())
	// The status, not the coarse state: "claimed" and "available" both read as
	// DepWaiting and call for different reactions.
	for _, want := range []string{"depends: ", parent.ID, "(claimed)"} {
		if !strings.Contains(gates, want) {
			t.Errorf("gates %q does not contain %q", gates, want)
		}
	}
}

// The positive control. An empty pending/ must produce an empty list — a
// reader that always returned something would pass the test above while
// printing a header over nothing.
func TestHeld_EmptyWhenNothingIsPending(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	snoozeAt(t, time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC))

	if _, err := Create(root, "mg-", "task", "Unblocked", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	if len(held) != 0 {
		t.Fatalf("Held returned %d items over an empty pending/, want 0: %+v", len(held), held)
	}
}

// The two gates are independent, and either can be the one still closed — so
// an item held by both says both.
func TestHeld_BothGatesAreNamed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC)
	snoozeAt(t, now)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}
	if _, _, err := SnoozeItem(root, child.ID, now.Add(90*time.Minute)); err != nil {
		t.Fatalf("SnoozeItem: %v", err)
	}

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("Held returned %d items, want 1", len(held))
	}
	gates := held[0].Gates(now)
	for _, want := range []string{"depends: ", parent.ID, "(available)", "snoozed: wakes ", "in 1h 30m"} {
		if !strings.Contains(gates, want) {
			t.Errorf("gates %q does not contain %q — both gates must be named", gates, want)
		}
	}
}

// An unreachable parent is still a gate, and the report says so in the words
// the operator needs: the id does not exist, or the parent is parked.
func TestHeld_NamesUnreachableParents(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	snoozeAt(t, time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC))

	shelvedParent, err := Create(root, "mg-", "task", "Parked", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	if _, err := Shelve(root, shelvedParent.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	ghost, err := Create(root, "mg-", "task", "Waiting on a ghost", []string{"mg-nope"})
	if err != nil {
		t.Fatalf("Create ghost dependent: %v", err)
	}
	orphan, err := Create(root, "mg-", "task", "Waiting on a parked parent", []string{shelvedParent.ID})
	if err != nil {
		t.Fatalf("Create shelved dependent: %v", err)
	}
	// A dependent of a shelved parent is filed shelved (see placeForDeps); the
	// pre-fix on-disk shape this reports on is one that reached pending/.
	if got := mustStatus(t, root, orphan.ID); got == "shelved" {
		moveForTest(t, root, "shelved", "pending", orphan.ID)
	}

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	byID := map[string]string{}
	for _, h := range held {
		byID[h.Item.ID] = h.Gates(snoozeNow())
	}
	if got := byID[ghost.ID]; !strings.Contains(got, "mg-nope (does not exist)") {
		t.Errorf("gates for a nonexistent parent = %q, want it named as nonexistent", got)
	}
	if got := byID[orphan.ID]; !strings.Contains(got, "(shelved)") {
		t.Errorf("gates for a shelved parent = %q, want the parent's status", got)
	}
}

// Ordering is soonest-wake first — the property SnoozedPending had, kept —
// with dependency-only holds after, since they carry no date to plan around.
func TestHeld_SoonestWakeFirstThenDependencyHolds(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	snoozeAt(t, now)

	parent, err := Create(root, "mg-", "task", "Parent", nil)
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	depOnly, err := Create(root, "mg-", "task", "Dependency hold", []string{parent.ID})
	if err != nil {
		t.Fatalf("Create dependent: %v", err)
	}
	later, err := Create(root, "mg-", "task", "Later", nil)
	if err != nil {
		t.Fatalf("Create later: %v", err)
	}
	sooner, err := Create(root, "mg-", "task", "Sooner", nil)
	if err != nil {
		t.Fatalf("Create sooner: %v", err)
	}
	if _, _, err := SnoozeItem(root, later.ID, now.Add(72*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem later: %v", err)
	}
	if _, _, err := SnoozeItem(root, sooner.ID, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("SnoozeItem sooner: %v", err)
	}

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	var got []string
	for _, h := range held {
		got = append(got, h.Item.ID)
	}
	want := []string{sooner.ID, later.ID, depOnly.ID}
	if len(got) != len(want) {
		t.Fatalf("Held returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Held order = %v, want %v", got, want)
		}
	}
}

// A gate nothing can parse is still a gate. It holds the item (see
// snoozeHolds), so it belongs in the gate list, said in full.
func TestHeld_MalformedSnoozeIsAGate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	now := time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC)
	snoozeAt(t, now)

	item, err := Create(root, "mg-", "task", "Hand-edited gate", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeMalformedSnooze(t, root, item.ID, "next tuesday")

	held, err := Held(root)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("Held returned %d items, want 1", len(held))
	}
	if held[0].BadSnooze != "next tuesday" {
		t.Errorf("BadSnooze = %q, want the value verbatim", held[0].BadSnooze)
	}
	if gates := held[0].Gates(now); !strings.Contains(gates, "next tuesday") || !strings.Contains(gates, "can never open") {
		t.Errorf("gates %q must name the unparseable value and that it can never open", gates)
	}
}
