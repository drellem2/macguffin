package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReopen(t *testing.T) {
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

	_, _, err = Done(root, item.ID, nil)
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	reopened, err := Reopen(root, item.ID)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	if reopened.ID != item.ID {
		t.Errorf("ID = %q, want %q", reopened.ID, item.ID)
	}
	if reopened.Title != item.Title {
		t.Errorf("Title = %q, want %q", reopened.Title, item.Title)
	}

	// File should be in available/
	availPath := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(availPath); err != nil {
		t.Errorf("expected file at %s: %v", availPath, err)
	}

	// File should NOT be in done/
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	if _, err := os.Stat(donePath); !os.IsNotExist(err) {
		t.Errorf("item still in done/: %v", err)
	}
}

func TestReopenNotInDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create but don't claim or done — item is in available/
	item, err := Create(root, "mg-", "bug", "Still available", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Reopen(root, item.ID)
	if err == nil {
		t.Error("expected error for item not in done/")
	}
	if !strings.Contains(err.Error(), "not found in done/") {
		t.Errorf("error = %q, want mention of done/", err)
	}
}

func TestReopenFailsIfClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "In progress", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err = Reopen(root, item.ID)
	if err == nil {
		t.Error("expected error for item in claimed/ (not done/)")
	}
}
