package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// archiveItem is a helper that creates, claims, dones, backdates, and archives
// an item, returning the created item and the year-month partition.
func archiveItem(t *testing.T, root, title string, result []byte) (*Item, string) {
	t.Helper()

	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, result); err != nil {
		t.Fatalf("Done: %v", err)
	}

	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(donePath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if result != nil {
		sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
		if err := os.Chtimes(sidecarPath, old, old); err != nil {
			t.Fatalf("Chtimes sidecar: %v", err)
		}
	}

	if _, err := Archive(root, 7*24*time.Hour); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	return item, old.Format("2006-01")
}

func TestUnarchiveBasic(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, partition := archiveItem(t, root, "Restore me", nil)

	unarchived, err := Unarchive(root, item.ID)
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if unarchived.ID != item.ID {
		t.Errorf("ID = %q, want %q", unarchived.ID, item.ID)
	}
	if unarchived.Title != item.Title {
		t.Errorf("Title = %q, want %q", unarchived.Title, item.Title)
	}

	// File should now be in available/
	availablePath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availablePath); err != nil {
		t.Errorf("expected file at %s: %v", availablePath, err)
	}

	// File should NOT be in archive/<partition>/
	archivePath := filepath.Join(root, "work", "archive", partition, item.ID+".md")
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("item still in archive/ after unarchive")
	}

	// Status should report available
	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "available" {
		t.Errorf("status = %q, want available", status)
	}
}

func TestUnarchiveMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, partition := archiveItem(t, root, "Has result", []byte(`{"branch":"fix-x"}`))

	if _, err := Unarchive(root, item.ID); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}

	// Sidecar should now be in available/
	availableSidecar := filepath.Join(root, "work", "available", item.ID+".result.json")
	if _, err := os.Stat(availableSidecar); err != nil {
		t.Errorf("sidecar not moved to available/: %v", err)
	}

	// Sidecar should NOT be in archive/<partition>/
	archivedSidecar := filepath.Join(root, "work", "archive", partition, item.ID+".result.json")
	if _, err := os.Stat(archivedSidecar); !os.IsNotExist(err) {
		t.Error("sidecar still in archive/ after unarchive")
	}
}

func TestUnarchiveNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Unarchive(root, "mg-nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent item")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of not found", err)
	}
}

func TestUnarchiveFailsIfAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Just created", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Unarchive(root, item.ID)
	if err == nil {
		t.Fatal("expected error for item in available/")
	}
	if !strings.Contains(err.Error(), "available") || !strings.Contains(err.Error(), "not archived") {
		t.Errorf("error = %q, want mention of available/not archived", err)
	}
}

func TestUnarchiveFailsIfClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "In progress", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = Unarchive(root, item.ID)
	if err == nil {
		t.Fatal("expected error for claimed item")
	}
	if !strings.Contains(err.Error(), "claimed") {
		t.Errorf("error = %q, want mention of claimed", err)
	}
}

func TestUnarchiveFailsIfDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Done item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	_, err = Unarchive(root, item.ID)
	if err == nil {
		t.Fatal("expected error for done item")
	}
	if !strings.Contains(err.Error(), "done") {
		t.Errorf("error = %q, want mention of done", err)
	}
}

func TestUnarchiveFailsIfShelved(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Shelved item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	_, err = Unarchive(root, item.ID)
	if err == nil {
		t.Fatal("expected error for shelved item")
	}
	if !strings.Contains(err.Error(), "shelved") {
		t.Errorf("error = %q, want mention of shelved", err)
	}
}

func TestEmitUnarchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Track me", nil)

	if _, err := Unarchive(root, item.ID); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.unarchive")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["from_status"] != "archived" {
		t.Errorf("from_status = %q, want archived", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "available" {
		t.Errorf("to_status = %q, want available", e.Extra["to_status"])
	}
	if e.Extra["actor"] == "" {
		t.Error("actor should be set")
	}
	if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
		t.Errorf("ts %q not RFC3339: %v", e.Ts, err)
	}
}
