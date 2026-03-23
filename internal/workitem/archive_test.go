package workitem

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestArchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create, claim, and complete an item
	item, err := Create(root, "mg-", "bug", "Old bug", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Backdate the done file to 10 days ago
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(donePath, old, old)

	// Archive with 7-day threshold
	archived, err := Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("archived count = %d, want 1", len(archived))
	}
	if archived[0].ID != item.ID {
		t.Errorf("archived ID = %q, want %q", archived[0].ID, item.ID)
	}

	// File should no longer be in done/
	if _, err := os.Stat(donePath); !os.IsNotExist(err) {
		t.Error("item still in done/ after archive")
	}

	// File should be in archive/<partition>/
	partition := old.Format("2006-01")
	archivePath := filepath.Join(root, "work", "archive", partition, item.ID+".md")
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("archived file missing at %s: %v", archivePath, err)
	}
}

func TestArchiveSkipsRecent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Recent task", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Don't backdate — item is fresh
	archived, err := Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if len(archived) != 0 {
		t.Errorf("archived count = %d, want 0 (item is recent)", len(archived))
	}

	// File should still be in done/
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("item should still be in done/: %v", err)
	}
}

func TestArchiveWithSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Bug with result", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err = Done(root, item.ID, []byte(`{"branch":"fix-123"}`))
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Backdate
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
	old := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(donePath, old, old)
	os.Chtimes(sidecarPath, old, old)

	archived, err := Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("archived count = %d, want 1", len(archived))
	}

	// Sidecar should also be moved
	partition := old.Format("2006-01")
	archivedSidecar := filepath.Join(root, "work", "archive", partition, item.ID+".result.json")
	if _, err := os.Stat(archivedSidecar); err != nil {
		t.Errorf("sidecar not archived: %v", err)
	}

	// Original sidecar should be gone
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Error("sidecar still in done/ after archive")
	}
}

func TestListArchived(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Archived task", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Backdate and archive
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(donePath, old, old)

	_, err = Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// ListArchived should find it
	items, err := ListArchived(root)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListArchived count = %d, want 1", len(items))
	}
	if items[0].ID != item.ID {
		t.Errorf("ListArchived ID = %q, want %q", items[0].ID, item.ID)
	}
}

func TestArchivedItemReadable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Will be archived", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	_, _, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Backdate and archive
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(donePath, old, old)

	_, err = Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// Read should still find the item
	found, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after archive: %v", err)
	}
	if found.ID != item.ID {
		t.Errorf("Read ID = %q, want %q", found.ID, item.ID)
	}

	// Status should report "archived"
	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "archived" {
		t.Errorf("status = %q, want %q", status, "archived")
	}
}

func TestArchiveEmpty(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	archived, err := Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if len(archived) != 0 {
		t.Errorf("archived count = %d, want 0", len(archived))
	}
}

func TestListArchivedEmpty(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	items, err := ListArchived(root)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("ListArchived count = %d, want 0", len(items))
	}
}
