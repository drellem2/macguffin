package workitem

import (
	"strings"
	"testing"
)

// mg-2cf0. `mg shelve` was the third stop in the close/retire/declare-done
// enumeration and the only one with no guard of any kind: no declaration check,
// no blocked-on check, no successor requirement, no override. `mg archive` had
// two guards plus a recorded --force; `mg done` had one.
//
// THE REFUSALS ARE PROVEN FIRST, and there is one test per predicate arm that
// fails if that arm is removed, for the reason archiveblocked_test.go gives: a
// guard that silently allows and a guard that correctly found nothing to block
// are the same observation from outside, and the failure under test — hiding
// work nobody is tracking — is silent and permanent.

// shelveRoot returns an initialised store.
func shelveRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	setupDirs(t, root)
	return root
}

// --- Arm 1: blocked-on-* -----------------------------------------------------

// TestShelveRefusesBlockedOn is the mg-e925 shape: an item openly tagged as
// waiting on a named person, which shelve took silently.
func TestShelveRefusesBlockedOn(t *testing.T) {
	root := shelveRoot(t)
	item, err := Create(root, "mg-", "task", "waiting on a ruling", nil,
		WithTags([]string{"blocked-on-daniel"}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Shelve(root, item.ID)
	if err == nil {
		t.Fatal("shelving an item tagged blocked-on-daniel succeeded — the blocked-on arm did not fire")
	}
	if !strings.Contains(err.Error(), "blocked-on-daniel") {
		t.Errorf("refusal = %q, want it to name the tag it found", err)
	}
	if st, _ := Status(root, item.ID); st == "shelved" {
		t.Error("item was shelved despite the refusal")
	}
}

// TestShelveBlockedOnNotAnsweredBySuccessor: a successor names a tracker for a
// recommendation. It says nothing about whether a person still owes something,
// and treating it as an answer would make one tag discharge two unrelated
// obligations. Archive behaves the same way (CheckArchivable).
func TestShelveBlockedOnNotAnsweredBySuccessor(t *testing.T) {
	root := shelveRoot(t)
	tracker, _ := Create(root, "mg-", "task", "the tracker", nil)
	item, _ := Create(root, "mg-", "task", "blocked but tracked", nil,
		WithTags([]string{"blocked-on-daniel", SuccessorTag(tracker.ID)}))

	if _, err := Shelve(root, item.ID); err == nil {
		t.Fatal("a successor: tag answered the blocked-on guard; it must not")
	}
}

// --- Arm 2: the declares-remainder tag ---------------------------------------

// TestShelveRefusesDeclaresRemainderTag is the explicit declaration: the item
// says its own output is a recommendation, and nothing names what carries it.
func TestShelveRefusesDeclaresRemainderTag(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "a triage verdict", nil,
		WithTags([]string{DeclaresRemainderTag}))

	_, err := Shelve(root, item.ID)
	if err == nil {
		t.Fatal("shelving a declares-remainder item with no successor succeeded — the declaration arm did not fire")
	}
	if !strings.Contains(err.Error(), DeclaresRemainderTag) {
		t.Errorf("refusal = %q, want it to name the tag that fired", err)
	}
}

// --- Arm 3: the type set -----------------------------------------------------

// TestShelveRefusesRecommendationTypes covers every member of the set whose
// output IS a recommendation. It fires WITHOUT the tag, which is the point: the
// tag only started being written on 2026-07-29, and the entire 181-item live
// shelf predates it.
func TestShelveRefusesRecommendationTypes(t *testing.T) {
	for _, typ := range []string{"design", "scoping", "audit", "idea"} {
		t.Run(typ, func(t *testing.T) {
			root := shelveRoot(t)
			item, _ := Create(root, "mg-", typ, "a "+typ+" nothing tracks", nil)

			_, err := Shelve(root, item.ID)
			if err == nil {
				t.Fatalf("shelving a type=%s with no successor succeeded — the type arm did not fire", typ)
			}
			if !strings.Contains(err.Error(), typ) {
				t.Errorf("refusal = %q, want it to name the type that fired", err)
			}
		})
	}
}

// TestShelveAllowsOrdinaryTypes is the false-positive control, and it is the
// half that keeps the guard installed. A guard that fires on ordinary work is a
// guard that gets switched off (mg-3412).
func TestShelveAllowsOrdinaryTypes(t *testing.T) {
	for _, typ := range []string{"task", "bug", "chore", "doc", "qa", "decision"} {
		t.Run(typ, func(t *testing.T) {
			root := shelveRoot(t)
			item, _ := Create(root, "mg-", typ, "an ordinary "+typ, nil)

			if _, err := Shelve(root, item.ID); err != nil {
				t.Fatalf("shelving an ordinary type=%s was refused: %v", typ, err)
			}
			if st, _ := Status(root, item.ID); st != "shelved" {
				t.Errorf("status = %q, want shelved", st)
			}
		})
	}
}

// --- Arm 4: the triage carrier block -----------------------------------------

// TestShelveRefusesTriageCarrierBlock is the mg-a661 shape and the reason the
// type alone is not enough: a `type: task` whose body's LEADING carrier block
// says `stage: triage`. Triage is a position in the gh-issue workflow, not a
// type, and its output is a verdict and nothing else.
func TestShelveRefusesTriageCarrierBlock(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "triage: some external issue", nil,
		WithBody("workflow: gh-issue\nstage: triage\ngh: drellem2/pogo#79\n\nTriage this and return a recommendation.\n"))

	_, err := Shelve(root, item.ID)
	if err == nil {
		t.Fatal("shelving a stage: triage item with no successor succeeded — the carrier-block arm did not fire")
	}
	if !strings.Contains(err.Error(), "triage") {
		t.Errorf("refusal = %q, want it to name the triage block that fired", err)
	}
}

// TestShelveAllowsStageMentionedInProse: a `stage:` line buried in prose is a
// mention, not a declaration. leadingCarrierValue stops at the first line of
// prose and this must inherit that, or a body that discusses triage comes to
// mean the item IS one.
func TestShelveAllowsStageMentionedInProse(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "ordinary work", nil,
		WithBody("Do the thing.\n\nUnlike the gh-issue flow, this is not\nstage: triage\nand never was.\n"))

	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("a stage: line in prose refused the shelve: %v", err)
	}
}

// --- The successor escape ----------------------------------------------------

// TestShelveAllowsWhenSuccessorNamed: the guard is satisfied by a structured
// pointer at an item that still exists, and by nothing else.
func TestShelveAllowsWhenSuccessorNamed(t *testing.T) {
	root := shelveRoot(t)
	tracker, _ := Create(root, "mg-", "task", "carries the build forward", nil)
	item, _ := Create(root, "mg-", "design", "a design that is tracked", nil,
		WithTags([]string{SuccessorTag(tracker.ID)}))

	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("shelving a design naming a live successor was refused: %v", err)
	}
	if st, _ := Status(root, item.ID); st != "shelved" {
		t.Errorf("status = %q, want shelved", st)
	}
}

// TestShelveRefusesDanglingSuccessor: the pointer is re-resolved rather than
// trusted from when it was written. A tag naming a deleted item tracks nothing,
// exactly like no tag at all — but it needs a different fix, so it gets a
// different refusal.
func TestShelveRefusesDanglingSuccessor(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "design", "points at a ghost", nil,
		WithTags([]string{SuccessorTag("mg-nosuch")}))

	_, err := Shelve(root, item.ID)
	if err == nil {
		t.Fatal("a successor: tag naming nothing satisfied the guard")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("refusal = %q, want it to say the successor is gone", err)
	}
}

// --- The override ------------------------------------------------------------

// TestShelveOverridePermitsAndRecords: the override is a STRING and its use is
// recorded with BOTH halves — the guard it bypassed and the reason. A code with
// no reason says only that somebody insisted.
func TestShelveOverridePermitsAndRecords(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "design", "genuinely abandoned", nil)

	const reason = "abandoned in favour of mg-1234, which carries the build"
	if _, err := Shelve(root, item.ID, WithShelveOverride(reason)); err != nil {
		t.Fatalf("override did not permit the shelve: %v", err)
	}
	if st, _ := Status(root, item.ID); st != "shelved" {
		t.Errorf("status = %q, want shelved", st)
	}

	e := findEvent(t, readEvents(t, root), "work.shelve_forced")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["reason"] != reason {
		t.Errorf("reason = %q, want %q", e.Extra["reason"], reason)
	}
	if e.Extra["guard"] != "shelve_without_successor" {
		t.Errorf("guard = %q, want the code of the refusal that was bypassed", e.Extra["guard"])
	}
}

// TestShelveOverrideRecordsWhichGuard: the recorded guard must be the one that
// actually fired, or an audit of forced shelves cannot tell a bypassed
// blocked-on from a bypassed successor.
func TestShelveOverrideRecordsWhichGuard(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "block discharged out of band", nil,
		WithTags([]string{"blocked-on-daniel"}))

	if _, err := Shelve(root, item.ID, WithShelveOverride("daniel answered in chat")); err != nil {
		t.Fatalf("override did not permit the shelve: %v", err)
	}
	e := findEvent(t, readEvents(t, root), "work.shelve_forced")
	if e.Extra["guard"] != "shelve_blocked_on_tag" {
		t.Errorf("guard = %q, want shelve_blocked_on_tag", e.Extra["guard"])
	}
}

// TestShelveWhitespaceIsNotAnOverride: an override satisfiable by the space bar
// is a boolean wearing a string's clothes.
func TestShelveWhitespaceIsNotAnOverride(t *testing.T) {
	for _, reason := range []string{"", "   ", "\t\n "} {
		root := shelveRoot(t)
		item, _ := Create(root, "mg-", "design", "still untracked", nil)

		if _, err := Shelve(root, item.ID, WithShelveOverride(reason)); err == nil {
			t.Errorf("override %q permitted the shelve", reason)
		}
		for _, e := range readEvents(t, root) {
			if e.Type == "work.shelve_forced" {
				t.Errorf("override %q emitted work.shelve_forced without shelving anything", reason)
			}
		}
	}
}

// TestShelveNoForcedEventWhenNoGuardFires: an override passed on an item no
// guard refused must not manufacture a record of a bypass that never happened.
func TestShelveNoForcedEventWhenNoGuardFires(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "ordinary work", nil)

	if _, err := Shelve(root, item.ID, WithShelveOverride("belt and braces")); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	for _, e := range readEvents(t, root) {
		if e.Type == "work.shelve_forced" {
			t.Error("work.shelve_forced emitted for an item no guard refused")
		}
	}
}

// --- The cascade is reported, not gated --------------------------------------

// TestShelveEventCarriesDependents is R2: the call site already computes the
// list of items it hides and used to discard it. 32 of the 175 items on the
// live shelf on 2026-07-30 got there as a dependent with nothing recording it.
func TestShelveEventCarriesDependents(t *testing.T) {
	root := shelveRoot(t)
	parent, _ := Create(root, "mg-", "task", "the target", nil)
	child, _ := Create(root, "mg-", "task", "an audit aimed at the target", []string{parent.ID})
	grandchild, _ := Create(root, "mg-", "task", "a follow-up to the audit", []string{child.ID})

	shelved, err := Shelve(root, parent.ID)
	if err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	if len(shelved) != 3 {
		t.Fatalf("shelved %d items, want 3", len(shelved))
	}

	var parentEvent, childEvent bool
	for _, e := range readEvents(t, root) {
		if e.Type != "work.shelve" {
			continue
		}
		switch e.Extra["item_id"] {
		case parent.ID:
			parentEvent = true
			deps := e.Extra["dependents"]
			if !strings.Contains(deps, child.ID) || !strings.Contains(deps, grandchild.ID) {
				t.Errorf("parent dependents = %q, want the whole subtree it hid (%s, %s)", deps, child.ID, grandchild.ID)
			}
			if _, ok := e.Extra["cascaded_from"]; ok {
				t.Error("the item the operator named carries cascaded_from")
			}
		case child.ID:
			childEvent = true
			if e.Extra["cascaded_from"] != parent.ID {
				t.Errorf("cascaded_from = %q, want %q", e.Extra["cascaded_from"], parent.ID)
			}
			if e.Extra["dependents"] != grandchild.ID {
				t.Errorf("child dependents = %q, want %q", e.Extra["dependents"], grandchild.ID)
			}
		}
	}
	if !parentEvent || !childEvent {
		t.Errorf("missing work.shelve events (parent=%v child=%v)", parentEvent, childEvent)
	}
}

// TestShelveDependentsFieldAlwaysPresent: a field written only when non-empty
// makes "hid nothing" and "written before mg-2cf0" the same observation.
func TestShelveDependentsFieldAlwaysPresent(t *testing.T) {
	root := shelveRoot(t)
	item, _ := Create(root, "mg-", "task", "lonely item", nil)
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.shelve")
	deps, ok := e.Extra["dependents"]
	if !ok {
		t.Fatal("work.shelve has no dependents field")
	}
	if deps != "" {
		t.Errorf("dependents = %q, want empty", deps)
	}
}

// TestShelveCascadeIsNotGated: the guard applies to the item the operator NAMED
// and to nothing else. A gate on the cascade would refuse a shelve on the
// strength of an item they never mentioned, and would leave the dependent in
// available/ with its dependency gone.
func TestShelveCascadeIsNotGated(t *testing.T) {
	root := shelveRoot(t)
	parent, _ := Create(root, "mg-", "task", "an ordinary target", nil)
	child, _ := Create(root, "mg-", "design", "a design aimed at the target", []string{parent.ID})

	shelved, err := Shelve(root, parent.ID)
	if err != nil {
		t.Fatalf("a guarded DEPENDENT refused the shelve of its parent: %v", err)
	}
	if len(shelved) != 2 {
		t.Fatalf("shelved %d items, want 2 (the guarded dependent must still travel)", len(shelved))
	}
	if st, _ := Status(root, child.ID); st != "shelved" {
		t.Errorf("dependent status = %q, want shelved", st)
	}
}

// --- ShelveByTag -------------------------------------------------------------

// TestShelveByTagHonoursTheGuard: a bulk shelve that skipped the guard would be
// a bypass of the targeted form's refusal one flag away.
func TestShelveByTagHonoursTheGuard(t *testing.T) {
	root := shelveRoot(t)
	ok1, _ := Create(root, "mg-", "task", "ordinary tagged one", nil, WithTags([]string{"sweep"}))
	guarded, _ := Create(root, "mg-", "design", "untracked design, tagged", nil, WithTags([]string{"sweep"}))
	ok2, _ := Create(root, "mg-", "task", "ordinary tagged two", nil, WithTags([]string{"sweep"}))

	shelved, skipped, err := ShelveByTag(root, "sweep")
	if err != nil {
		t.Fatalf("ShelveByTag: %v", err)
	}

	if len(skipped) != 1 || skipped[0].Item.ID != guarded.ID {
		t.Fatalf("skipped = %v, want exactly the guarded item %s", skipped, guarded.ID)
	}
	if skipped[0].Reason == nil || !strings.Contains(skipped[0].Reason.Error(), "successor") {
		t.Errorf("skip reason = %v, want the refusal that stopped it", skipped[0].Reason)
	}
	if st, _ := Status(root, guarded.ID); st == "shelved" {
		t.Error("the bulk form shelved a guarded item")
	}

	// One guarded item must not stop the rest.
	if len(shelved) != 2 {
		t.Fatalf("shelved %d items, want 2", len(shelved))
	}
	for _, id := range []string{ok1.ID, ok2.ID} {
		if st, _ := Status(root, id); st != "shelved" {
			t.Errorf("%s status = %q, want shelved", id, st)
		}
	}
}

// TestShelveByTagDoesNotReportCascadedItemsAsSkipped: a tagged item an earlier
// cascade already hid went exactly where the operator asked. Reporting it as a
// refusal would fill the skipped list with successes.
func TestShelveByTagDoesNotReportCascadedItemsAsSkipped(t *testing.T) {
	root := shelveRoot(t)
	parent, _ := Create(root, "mg-", "task", "tagged parent", nil, WithTags([]string{"sweep"}))
	Create(root, "mg-", "task", "tagged child", []string{parent.ID}, WithTags([]string{"sweep"}))

	shelved, skipped, err := ShelveByTag(root, "sweep")
	if err != nil {
		t.Fatalf("ShelveByTag: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}
	if len(shelved) != 2 {
		t.Errorf("shelved %d items, want 2", len(shelved))
	}
}
