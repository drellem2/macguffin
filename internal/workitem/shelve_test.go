package workitem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/internal/mgerr"
)

func TestShelveBasic(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "shelve me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	shelved, err := Shelve(root, item.ID)
	if err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	if len(shelved) != 1 {
		t.Fatalf("expected 1 shelved item, got %d", len(shelved))
	}
	if shelved[0].ID != item.ID {
		t.Errorf("shelved item ID = %q, want %q", shelved[0].ID, item.ID)
	}

	// Item should be in shelved/
	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "shelved" {
		t.Errorf("status = %q, want shelved", status)
	}

	// Item should not appear in available/
	available, _ := ListByStatus(root, "available")
	for _, a := range available {
		if a.ID == item.ID {
			t.Error("shelved item still appears in available")
		}
	}
}

func TestShelveAlreadyShelved(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "double shelve", nil)
	Shelve(root, item.ID)

	_, err := Shelve(root, item.ID)
	if err == nil {
		t.Error("expected error shelving already-shelved item")
	}
}

func TestShelveCascadesToDependents(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, _ := Create(root, "mg-", "task", "parent task", nil)
	child, _ := Create(root, "mg-", "task", "child task", []string{parent.ID})

	// Child should be in pending/
	status, _ := Status(root, child.ID)
	if status != "pending" {
		t.Fatalf("child status = %q, want pending", status)
	}

	shelved, err := Shelve(root, parent.ID)
	if err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	// Both parent and child should be shelved
	if len(shelved) != 2 {
		t.Fatalf("expected 2 shelved items, got %d", len(shelved))
	}

	parentStatus, _ := Status(root, parent.ID)
	childStatus, _ := Status(root, child.ID)
	if parentStatus != "shelved" {
		t.Errorf("parent status = %q, want shelved", parentStatus)
	}
	if childStatus != "shelved" {
		t.Errorf("child status = %q, want shelved", childStatus)
	}
}

func TestUnshelveBasic(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "unshelve me", nil)
	Shelve(root, item.ID)

	unshelved, err := Unshelve(root, item.ID)
	if err != nil {
		t.Fatalf("Unshelve: %v", err)
	}
	if len(unshelved) != 1 {
		t.Fatalf("expected 1 unshelved item, got %d", len(unshelved))
	}

	status, _ := Status(root, item.ID)
	if status != "available" {
		t.Errorf("status = %q, want available", status)
	}
}

func TestUnshelveWithUnmetDeps(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create parent and dependent child
	parent, _ := Create(root, "mg-", "task", "parent unshelve", nil)
	child, _ := Create(root, "mg-", "task", "child unshelve", []string{parent.ID})

	// Shelve both
	Shelve(root, parent.ID)

	// Unshelve child only — parent is still shelved so deps are unmet
	// First move child manually to test the dep logic
	src := filepath.Join(root, "work", "shelved", child.ID+".md")
	// child was already shelved by cascade, so we unshelve it
	unshelved, err := Unshelve(root, child.ID)
	if err != nil {
		// child might not exist if cascade didn't shelve it — check
		if _, serr := os.Stat(src); os.IsNotExist(serr) {
			t.Skip("child was not cascade-shelved")
		}
		t.Fatalf("Unshelve child: %v", err)
	}

	if len(unshelved) != 1 {
		t.Fatalf("expected 1 unshelved, got %d", len(unshelved))
	}

	// Child should go to pending since parent is still shelved
	status, _ := Status(root, child.ID)
	if status != "pending" {
		t.Errorf("status = %q, want pending (unmet deps)", status)
	}
}

func TestUnshelveCascade(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, _ := Create(root, "mg-", "task", "cascade parent", nil)
	child, _ := Create(root, "mg-", "task", "cascade child", []string{parent.ID})

	// Shelve parent (cascades to child)
	Shelve(root, parent.ID)

	// Unshelve parent (should cascade to child)
	unshelved, err := Unshelve(root, parent.ID)
	if err != nil {
		t.Fatalf("Unshelve: %v", err)
	}

	if len(unshelved) != 2 {
		t.Fatalf("expected 2 unshelved items, got %d", len(unshelved))
	}

	parentStatus, _ := Status(root, parent.ID)
	childStatus, _ := Status(root, child.ID)
	if parentStatus != "available" {
		t.Errorf("parent status = %q, want available", parentStatus)
	}
	if childStatus != "pending" {
		t.Errorf("child status = %q, want pending", childStatus)
	}
}

func TestShelveByTag(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	Create(root, "mg-", "task", "tagged item one", nil, WithTags([]string{"pogo-darwin"}))
	Create(root, "mg-", "task", "tagged item two", nil, WithTags([]string{"pogo-darwin", "other"}))
	Create(root, "mg-", "task", "untagged item", nil)

	shelved, _, err := ShelveByTag(root, "pogo-darwin")
	if err != nil {
		t.Fatalf("ShelveByTag: %v", err)
	}

	if len(shelved) != 2 {
		t.Fatalf("expected 2 shelved items, got %d", len(shelved))
	}

	// Untagged item should still be available
	available, _ := ListByStatus(root, "available")
	if len(available) != 1 {
		t.Errorf("expected 1 available item, got %d", len(available))
	}
}

func TestShelveByTagNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, _, err := ShelveByTag(root, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tag")
	}
}

func TestListShelved(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item1, _ := Create(root, "mg-", "task", "list shelved one", nil)
	item2, _ := Create(root, "mg-", "task", "list shelved two", nil)
	Create(root, "mg-", "task", "not shelved", nil)

	Shelve(root, item1.ID)
	Shelve(root, item2.ID)

	shelved, err := ListShelved(root)
	if err != nil {
		t.Fatalf("ListShelved: %v", err)
	}
	if len(shelved) != 2 {
		t.Errorf("expected 2 shelved, got %d", len(shelved))
	}

	// ListByStatus should also work
	byStatus, err := ListByStatus(root, "shelved")
	if err != nil {
		t.Fatalf("ListByStatus shelved: %v", err)
	}
	if len(byStatus) != 2 {
		t.Errorf("ListByStatus expected 2, got %d", len(byStatus))
	}
}

func TestShelvedHiddenFromListAll(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "hidden item", nil)
	Shelve(root, item.ID)

	grouped, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	for status, items := range grouped {
		for _, it := range items {
			if it.ID == item.ID {
				t.Errorf("shelved item %s found in ListAll under %s", item.ID, status)
			}
		}
	}
}

func TestUnshelveNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Unshelve(root, "mg-nonexistent")
	if err == nil {
		t.Error("expected error unshelving nonexistent item")
	}
}

func TestShelveFromClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "claimed then shelved", nil)
	Claim(root, item.ID, 0)

	shelved, err := Shelve(root, item.ID)
	if err != nil {
		t.Fatalf("Shelve from claimed: %v", err)
	}
	if len(shelved) != 1 {
		t.Fatalf("expected 1 shelved, got %d", len(shelved))
	}

	status, _ := Status(root, item.ID)
	if status != "shelved" {
		t.Errorf("status = %q, want shelved", status)
	}
}

func TestShelveCannotShelveDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "done item", nil)
	Claim(root, item.ID, 0)
	Done(root, item.ID, nil)

	_, err := Shelve(root, item.ID)
	if err == nil {
		t.Error("expected error shelving done item")
	}
}

// --- mg unshelve --claim (drellem2/macguffin#34) ---

func TestUnshelveClaimTakesBackAClaimedItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "claimed, shelved, taken back", nil)
	if _, err := Claim(root, item.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	got, err := UnshelveClaim(root, item.ID, 5151)
	if err != nil {
		t.Fatalf("UnshelveClaim: %v", err)
	}
	if len(got) != 1 || got[0].ID != item.ID {
		t.Fatalf("UnshelveClaim returned %v, want just %s", got, item.ID)
	}

	// A new claim, stamped with the CALLER's pid — not the 4242 shelve dropped.
	claimed := filepath.Join(root, "work", "claimed", item.ID+".md.5151")
	if _, err := os.Stat(claimed); err != nil {
		t.Fatalf("expected claim file %s: %v", claimed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", item.ID+".md")); !os.IsNotExist(err) {
		t.Errorf("item also present in available/ (err=%v)", err)
	}
	if _, pid := statusWithPID(root, item.ID); pid != 5151 {
		t.Errorf("claim pid = %d, want 5151", pid)
	}

	// A new claim emits work.claim, which is what opens a `mg spend` interval.
	claims, err := event.List(root, event.ListOpts{Type: "work.claim"})
	if err != nil {
		t.Fatalf("event.List: %v", err)
	}
	last := claims[len(claims)-1].Extra
	if last["item_id"] != item.ID || last["pid"] != "5151" || last["from_status"] != "shelved" {
		t.Errorf("last work.claim = %v, want item %s, pid 5151, from_status shelved", last, item.ID)
	}
}

func TestUnshelveClaimRefusesItemShelvedFromAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "never claimed", nil)
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	_, err := UnshelveClaim(root, item.ID, 5151)
	if err == nil {
		t.Fatal("UnshelveClaim succeeded on an item shelved from available")
	}
	if code := mgerrCode(err); code != "not_shelved_from_claimed" {
		t.Errorf("code = %q, want not_shelved_from_claimed (err: %v)", code, err)
	}
	if st, _ := Status(root, item.ID); st != "shelved" {
		t.Errorf("refused unshelve moved the item: status = %q, want shelved", st)
	}
}

// The LATEST shelve decides: an item shelved from claimed, restored, and
// shelved again from available is not one --claim may take.
func TestUnshelveClaimReadsTheLatestShelve(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := Create(root, "mg-", "task", "shelved twice", nil)
	Claim(root, item.ID, 4242)
	Shelve(root, item.ID)
	if _, err := Unshelve(root, item.ID); err != nil {
		t.Fatalf("Unshelve: %v", err)
	}
	Shelve(root, item.ID) // now from available

	if _, err := UnshelveClaim(root, item.ID, 5151); mgerrCode(err) != "not_shelved_from_claimed" {
		t.Errorf("err = %v, want not_shelved_from_claimed", err)
	}
}

func TestUnshelveClaimRefusesWhenTheLogHasNoRecord(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Moved into shelved/ by hand: no work.shelve record exists.
	item, _ := Create(root, "mg-", "task", "hand-shelved", nil)
	src := filepath.Join(root, "work", "available", item.ID+".md")
	if err := os.Rename(src, filepath.Join(root, "work", "shelved", item.ID+".md")); err != nil {
		t.Fatal(err)
	}

	if _, err := UnshelveClaim(root, item.ID, 5151); mgerrCode(err) != "unknown_prior_status" {
		t.Errorf("err = %v, want unknown_prior_status", err)
	}
}

func TestUnshelveClaimWithUnmetDependencyGoesToPending(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, _ := Create(root, "mg-", "task", "parent", nil)
	Claim(root, parent.ID, 0)
	if _, _, err := Done(root, parent.ID, nil); err != nil {
		t.Fatalf("Done parent: %v", err)
	}
	child, _ := Create(root, "mg-", "task", "child", []string{parent.ID})
	if _, err := Claim(root, child.ID, 4242); err != nil {
		t.Fatalf("Claim child: %v", err)
	}
	Shelve(root, child.ID)
	// The dependency becomes unmet again while the child is on the shelf.
	if _, err := Reopen(root, parent.ID); err != nil {
		t.Fatalf("Reopen parent: %v", err)
	}

	if _, err := UnshelveClaim(root, child.ID, 5151); err != nil {
		t.Fatalf("UnshelveClaim: %v", err)
	}
	if st, _ := Status(root, child.ID); st != "pending" {
		t.Errorf("status = %q, want pending (unmet dependency, so no claim)", st)
	}
	claims, _ := event.List(root, event.ListOpts{Type: "work.claim"})
	for _, c := range claims {
		if c.Extra["item_id"] == child.ID && c.Extra["pid"] == "5151" {
			t.Errorf("work.claim emitted for an item that landed in pending: %v", c.Extra)
		}
	}
}

// mg done on a shelved item that was claimed when shelved points at --claim;
// a plain 'mg unshelve' would send it to available/, where it cannot be done.
func TestDoneOnShelvedClaimedItemHintsClaim(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	claimed, _ := Create(root, "mg-", "task", "was claimed", nil)
	Claim(root, claimed.ID, 4242)
	Shelve(root, claimed.ID)

	_, _, err := Done(root, claimed.ID, nil)
	if err == nil {
		t.Fatal("Done succeeded on a shelved item")
	}
	if want := "mg unshelve " + claimed.ID + " --claim"; !strings.Contains(mgerrHint(err), want) {
		t.Errorf("hint = %q, want it to contain %q", mgerrHint(err), want)
	}

	avail, _ := Create(root, "mg-", "task", "was available", nil)
	Shelve(root, avail.ID)
	_, _, err = Done(root, avail.ID, nil)
	if h := mgerrHint(err); strings.Contains(h, "--claim") || !strings.Contains(h, "mg unshelve "+avail.ID) {
		t.Errorf("hint = %q, want the plain unshelve hint", h)
	}
}

func mgerrHint(err error) string {
	var me *mgerr.Error
	if errors.As(err, &me) {
		return me.Hint
	}
	return ""
}

func TestUnshelveByTag(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	a, _ := Create(root, "mg-", "task", "tagged one", nil, WithTags([]string{"grp"}))
	b, _ := Create(root, "mg-", "task", "tagged two", nil, WithTags([]string{"grp", "other"}))
	c, _ := Create(root, "mg-", "task", "shelved, untagged", nil)
	for _, id := range []string{a.ID, b.ID, c.ID} {
		if _, err := Shelve(root, id); err != nil {
			t.Fatalf("Shelve %s: %v", id, err)
		}
	}

	unshelved, skipped, err := UnshelveByTag(root, "grp")
	if err != nil {
		t.Fatalf("UnshelveByTag: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}
	if len(unshelved) != 2 {
		t.Fatalf("unshelved %d items, want 2", len(unshelved))
	}
	for _, id := range []string{a.ID, b.ID} {
		if st, _ := Status(root, id); st != "available" {
			t.Errorf("%s status = %q, want available", id, st)
		}
	}
	if st, _ := Status(root, c.ID); st != "shelved" {
		t.Errorf("untagged item status = %q, want it still shelved", st)
	}
}

func TestUnshelveByTagNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// An AVAILABLE item with the tag is not a match: only the shelf is searched.
	Create(root, "mg-", "task", "tagged but not shelved", nil, WithTags([]string{"grp"}))

	_, _, err := UnshelveByTag(root, "grp")
	if err == nil || !strings.Contains(err.Error(), `no shelved items found with tag "grp"`) {
		t.Errorf("err = %v, want the zero-match error", err)
	}
}

// Two tagged items where restoring the parent recursively restores the child:
// the second must be neither an error nor a skip, and must be listed once.
//
// The shelf is read in filename (random ID) order, and the recursive path is
// taken only when the parent is restored FIRST — so the fixture is rebuilt in a
// fresh root until parent.ID sorts before child.ID. Without that, the dedupe
// went unexercised about half the time (mg-76f6 round 1).
func TestUnshelveByTagDedupesRecursiveRestore(t *testing.T) {
	var root string
	var parent, child *Item
	for i := 0; ; i++ {
		if i == 64 {
			t.Fatal("could not build a fixture with parent.ID < child.ID in 64 tries")
		}
		root = t.TempDir()
		setupDirs(t, root)
		parent, _ = Create(root, "mg-", "task", "parent", nil, WithTags([]string{"grp"}))
		child, _ = Create(root, "mg-", "task", "child", []string{parent.ID}, WithTags([]string{"grp"}))
		if parent.ID < child.ID {
			break
		}
	}
	if _, _, err := ShelveByTag(root, "grp"); err != nil {
		t.Fatalf("ShelveByTag: %v", err)
	}

	unshelved, skipped, err := UnshelveByTag(root, "grp")
	if err != nil {
		t.Fatalf("UnshelveByTag: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %+v, want none", skipped)
	}
	if len(unshelved) != 2 {
		t.Fatalf("unshelved %d items, want 2 (each once)", len(unshelved))
	}
	if st, _ := Status(root, child.ID); st != "pending" {
		t.Errorf("child status = %q, want pending (its dependency is not done)", st)
	}
}

// A cascade-shelved dependent comes back with its tagged parent even though it
// does not carry the tag — the documented semantics.
func TestUnshelveByTagRestoresUntaggedCascadeDependents(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	parent, _ := Create(root, "mg-", "task", "parent", nil, WithTags([]string{"grp"}))
	dep, _ := Create(root, "mg-", "task", "untagged dependent", []string{parent.ID})
	if _, err := Shelve(root, parent.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}
	if st, _ := Status(root, dep.ID); st != "shelved" {
		t.Fatalf("dependent status = %q, want shelved by the cascade", st)
	}

	unshelved, _, err := UnshelveByTag(root, "grp")
	if err != nil {
		t.Fatalf("UnshelveByTag: %v", err)
	}
	if len(unshelved) != 2 {
		t.Errorf("unshelved %d items, want 2", len(unshelved))
	}
	if st, _ := Status(root, dep.ID); st == "shelved" {
		t.Errorf("untagged cascade dependent is still shelved")
	}
}
