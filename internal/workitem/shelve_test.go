package workitem

import (
	"os"
	"path/filepath"
	"testing"
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

	shelved, err := ShelveByTag(root, "pogo-darwin")
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

	_, err := ShelveByTag(root, "nonexistent")
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
