package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit_CreatesDirectoryTree(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")

	if err := Init(mgRoot); err != nil {
		t.Fatalf("Init(%q) failed: %v", mgRoot, err)
	}

	expected := []string{
		"work/available",
		"work/claimed",
		"work/done",
		"agents",
		"mail",
		"log",
	}

	for _, rel := range expected {
		path := filepath.Join(mgRoot, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected directory %s to exist: %v", rel, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", rel)
		}
	}
}

func TestInit_Idempotent(t *testing.T) {
	root := t.TempDir()
	mgRoot := filepath.Join(root, ".macguffin")

	if err := Init(mgRoot); err != nil {
		t.Fatalf("first Init failed: %v", err)
	}

	// Create a file to verify Init doesn't destroy existing data
	marker := filepath.Join(mgRoot, "work", "available", "test.md")
	if err := os.WriteFile(marker, []byte("test"), 0o644); err != nil {
		t.Fatalf("creating marker file: %v", err)
	}

	if err := Init(mgRoot); err != nil {
		t.Fatalf("second Init failed: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file should survive idempotent Init: %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	root, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot() failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".macguffin")
	if root != expected {
		t.Errorf("DefaultRoot() = %q, want %q", root, expected)
	}
}
