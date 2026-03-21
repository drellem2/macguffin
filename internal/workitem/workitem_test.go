package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateID(t *testing.T) {
	now := time.Now()
	id := GenerateID("test title", now)

	if !strings.HasPrefix(id, "gt-") {
		t.Errorf("ID should start with gt-, got %q", id)
	}
	// gt- plus 4 hex chars (2 bytes)
	if len(id) != 7 {
		t.Errorf("ID length should be 7 (gt-XXXX), got %d: %q", len(id), id)
	}

	// Same inputs produce same ID
	id2 := GenerateID("test title", now)
	if id != id2 {
		t.Errorf("same inputs should produce same ID: %q != %q", id, id2)
	}

	// Different inputs produce different IDs
	id3 := GenerateID("different title", now)
	if id == id3 {
		t.Errorf("different inputs should produce different IDs")
	}
}

func TestCreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "bug", "Auth tokens broken", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if !strings.HasPrefix(item.ID, "gt-") {
		t.Errorf("ID should start with gt-, got %q", item.ID)
	}
	if item.Type != "bug" {
		t.Errorf("Type = %q, want %q", item.Type, "bug")
	}
	if item.Title != "Auth tokens broken" {
		t.Errorf("Title = %q, want %q", item.Title, "Auth tokens broken")
	}
	if item.Creator == "" {
		t.Error("Creator should not be empty")
	}
	if item.Created.IsZero() {
		t.Error("Created should not be zero")
	}

	// Verify file exists
	path := filepath.Join(root, "work", "available", item.ID+".md")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s: %v", path, err)
	}
}

func TestRead(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	created, err := Create(root, "task", "Implement feature X", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	read, err := Read(root, created.ID)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if read.ID != created.ID {
		t.Errorf("ID = %q, want %q", read.ID, created.ID)
	}
	if read.Type != "task" {
		t.Errorf("Type = %q, want %q", read.Type, "task")
	}
	if read.Title != "Implement feature X" {
		t.Errorf("Title = %q, want %q", read.Title, "Implement feature X")
	}
	if read.Creator != created.Creator {
		t.Errorf("Creator = %q, want %q", read.Creator, created.Creator)
	}
}

func TestReadNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Read(root, "gt-000")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestList(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "bug", "First item", nil)
	if err != nil {
		t.Fatalf("Create 1 failed: %v", err)
	}
	_, err = Create(root, "task", "Second item", nil)
	if err != nil {
		t.Fatalf("Create 2 failed: %v", err)
	}

	items, err := List(root)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestListEmpty(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	items, err := List(root)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestParse(t *testing.T) {
	content := `---
id: gt-abc
type: bug
created: 2026-03-20T16:00:00Z
creator: alice
---

# Auth tokens broken

The refresh logic is wrong.
`

	item, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if item.ID != "gt-abc" {
		t.Errorf("ID = %q, want %q", item.ID, "gt-abc")
	}
	if item.Type != "bug" {
		t.Errorf("Type = %q, want %q", item.Type, "bug")
	}
	if item.Creator != "alice" {
		t.Errorf("Creator = %q, want %q", item.Creator, "alice")
	}
	if item.Title != "Auth tokens broken" {
		t.Errorf("Title = %q, want %q", item.Title, "Auth tokens broken")
	}
	if !strings.Contains(item.Body, "refresh logic") {
		t.Errorf("Body should contain description text")
	}
}

func TestParseMissingFrontmatter(t *testing.T) {
	_, err := Parse("no frontmatter here")
	if err == nil {
		t.Error("expected error for missing frontmatter")
	}
}

func TestParseMissingID(t *testing.T) {
	content := `---
type: bug
created: 2026-03-20T16:00:00Z
---

# Title
`
	_, err := Parse(content)
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestCreateWithRepo(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "task", "Repo tagged item", nil, WithRepo("/home/user/myproject"))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if item.Repo != "/home/user/myproject" {
		t.Errorf("Repo = %q, want %q", item.Repo, "/home/user/myproject")
	}

	// Read it back and verify repo is persisted
	read, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if read.Repo != "/home/user/myproject" {
		t.Errorf("Read Repo = %q, want %q", read.Repo, "/home/user/myproject")
	}
}

func TestCreateWithoutRepo(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "task", "No repo item", nil)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if item.Repo != "" {
		t.Errorf("Repo should be empty, got %q", item.Repo)
	}

	// Verify frontmatter does not contain repo line
	path := filepath.Join(root, "work", "available", item.ID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if strings.Contains(string(data), "repo:") {
		t.Error("frontmatter should not contain repo: when repo is empty")
	}
}

func TestParseWithRepo(t *testing.T) {
	content := `---
id: gt-abc
type: task
created: 2026-03-20T16:00:00Z
creator: bob
depends: []
repo: /home/bob/project
---

# Tagged item
`

	item, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if item.Repo != "/home/bob/project" {
		t.Errorf("Repo = %q, want %q", item.Repo, "/home/bob/project")
	}
}

func setupDirs(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{
		filepath.Join(root, "work", "available"),
		filepath.Join(root, "work", "claimed"),
		filepath.Join(root, "work", "done"),
		filepath.Join(root, "work", "pending"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}
