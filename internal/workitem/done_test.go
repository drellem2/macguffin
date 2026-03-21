package workitem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Fix the widget", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	result := json.RawMessage(`{"status":"fixed","commit":"abc123"}`)
	done, err := Done(root, item.ID, result)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	if done.ID != item.ID {
		t.Errorf("ID = %q, want %q", done.ID, item.ID)
	}
	if done.Title != item.Title {
		t.Errorf("Title = %q, want %q", done.Title, item.Title)
	}

	// File should be in done/
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected file at %s: %v", donePath, err)
	}

	// Result sidecar should exist
	sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Errorf("expected sidecar at %s: %v", sidecarPath, err)
	}

	// Verify sidecar content
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	if string(data) != `{"status":"fixed","commit":"abc123"}` {
		t.Errorf("sidecar content = %q, want result JSON", string(data))
	}

	// File should NOT be in claimed/
	claimedDir := filepath.Join(root, "work", "claimed")
	entries, _ := os.ReadDir(claimedDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			t.Errorf("item still in claimed/: %s", e.Name())
		}
	}

	// File should NOT be in available/
	availDir := filepath.Join(root, "work", "available")
	entries, _ = os.ReadDir(availDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			t.Errorf("item still in available/: %s", e.Name())
		}
	}
}

func TestDoneNoResult(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Simple task", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	done, err := Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	if done.ID != item.ID {
		t.Errorf("ID = %q, want %q", done.ID, item.ID)
	}

	// No sidecar should exist
	sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
	if _, err := os.Stat(sidecarPath); err == nil {
		t.Error("sidecar should not exist when no result provided")
	}
}

func TestDoneNotClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create but don't claim
	item, err := Create(root, "mg-", "bug", "Not claimed yet", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Done(root, item.ID, nil)
	if err == nil {
		t.Error("expected error for unclaimed item")
	}
}

func TestDoneNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Done(root, "gt-000", nil)
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestDoneReadable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Readable after done", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Read should still find the item in done/
	found, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after done: %v", err)
	}
	if found.ID != item.ID {
		t.Errorf("Read ID = %q, want %q", found.ID, item.ID)
	}
}

func TestStatus(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Status check", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "available" {
		t.Errorf("status = %q, want %q", status, "available")
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	status, err = Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "claimed" {
		t.Errorf("status = %q, want %q", status, "claimed")
	}

	_, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	status, err = Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want %q", status, "done")
	}
}

func TestStatusNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Status(root, "gt-000")
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestListByStatus(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create 3 items
	item1, _ := Create(root, "mg-", "bug", "Item one", nil)
	item2, _ := Create(root, "mg-", "task", "Item two", nil)
	item3, _ := Create(root, "mg-", "bug", "Item three", nil)

	// All should be available
	avail, err := ListByStatus(root, "available")
	if err != nil {
		t.Fatalf("ListByStatus available: %v", err)
	}
	if len(avail) != 3 {
		t.Errorf("available count = %d, want 3", len(avail))
	}

	// Claim item1 and item2
	Claim(root, item1.ID)
	Claim(root, item2.ID)

	avail, _ = ListByStatus(root, "available")
	if len(avail) != 1 {
		t.Errorf("available count = %d, want 1", len(avail))
	}

	claimed, _ := ListByStatus(root, "claimed")
	if len(claimed) != 2 {
		t.Errorf("claimed count = %d, want 2", len(claimed))
	}

	// Done item1
	Done(root, item1.ID, nil)

	done, _ := ListByStatus(root, "done")
	if len(done) != 1 {
		t.Errorf("done count = %d, want 1", len(done))
	}

	// item3 still available
	avail, _ = ListByStatus(root, "available")
	if len(avail) != 1 {
		t.Errorf("available count = %d, want 1", len(avail))
	}
	if avail[0].ID != item3.ID {
		t.Errorf("available item ID = %q, want %q", avail[0].ID, item3.ID)
	}
}

func TestListByStatusInvalid(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := ListByStatus(root, "invalid")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestFullLifecycle(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create
	item, err := Create(root, "mg-", "bug", "Full lifecycle test", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify available
	status, _ := Status(root, item.ID)
	if status != "available" {
		t.Fatalf("expected available, got %q", status)
	}

	// Claim
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	status, _ = Status(root, item.ID)
	if status != "claimed" {
		t.Fatalf("expected claimed, got %q", status)
	}

	// Done with result
	result := json.RawMessage(`{"status":"fixed","commit":"abc123"}`)
	_, err = Done(root, item.ID, result)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	status, _ = Status(root, item.ID)
	if status != "done" {
		t.Fatalf("expected done, got %q", status)
	}

	// Verify done/ has the file
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("done file missing: %v", err)
	}

	// Verify result sidecar
	sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parsing sidecar: %v", err)
	}
	if parsed["status"] != "fixed" {
		t.Errorf("result status = %q, want %q", parsed["status"], "fixed")
	}

	// Verify list by status
	avail, _ := ListByStatus(root, "available")
	if len(avail) != 0 {
		t.Errorf("available count = %d, want 0", len(avail))
	}
	claimedItems, _ := ListByStatus(root, "claimed")
	if len(claimedItems) != 0 {
		t.Errorf("claimed count = %d, want 0", len(claimedItems))
	}
	doneItems, _ := ListByStatus(root, "done")
	if len(doneItems) != 1 {
		t.Errorf("done count = %d, want 1", len(doneItems))
	}

	// Read still works
	found, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if found.Title != "Full lifecycle test" {
		t.Errorf("Title = %q, want %q", found.Title, "Full lifecycle test")
	}
}
