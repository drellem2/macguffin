package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateTitle(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Original title", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "Updated title"
	updated, err := Update(root, item.ID, UpdateField{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Title != "Updated title" {
		t.Errorf("Title = %q, want %q", updated.Title, "Updated title")
	}

	// Re-read and verify persistence
	read, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Title != "Updated title" {
		t.Errorf("persisted Title = %q, want %q", read.Title, "Updated title")
	}
	// Body should contain the updated heading
	if !strings.Contains(read.Body, "# Updated title") {
		t.Errorf("Body should contain updated heading, got: %s", read.Body)
	}
}

func TestUpdateBody(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Item with body", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newBody := "\n# Item with body\n\nSome detailed description.\n"
	updated, err := Update(root, item.ID, UpdateField{Body: &newBody})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if !strings.Contains(updated.Body, "detailed description") {
		t.Errorf("Body should contain new text, got: %s", updated.Body)
	}
}

func TestUpdateType(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Change type", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newType := "bug"
	updated, err := Update(root, item.ID, UpdateField{Type: &newType})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Type != "bug" {
		t.Errorf("Type = %q, want %q", updated.Type, "bug")
	}

	read, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if read.Type != "bug" {
		t.Errorf("persisted Type = %q, want %q", read.Type, "bug")
	}
}

func TestUpdateDependsReplace(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Dep test", []string{"mg-aaa", "mg-bbb"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newDeps := []string{"mg-ccc"}
	updated, err := Update(root, item.ID, UpdateField{Depends: newDeps})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Depends) != 1 || updated.Depends[0] != "mg-ccc" {
		t.Errorf("Depends = %v, want [mg-ccc]", updated.Depends)
	}
}

func TestUpdateDependsAdd(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Add dep", []string{"mg-aaa"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := Update(root, item.ID, UpdateField{AddDepends: []string{"mg-bbb"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Depends) != 2 {
		t.Fatalf("Depends length = %d, want 2", len(updated.Depends))
	}
	if updated.Depends[1] != "mg-bbb" {
		t.Errorf("Depends[1] = %q, want %q", updated.Depends[1], "mg-bbb")
	}
}

func TestUpdateDependsAddNoDuplicates(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Dup dep", []string{"mg-aaa"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := Update(root, item.ID, UpdateField{AddDepends: []string{"mg-aaa", "mg-bbb"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Depends) != 2 {
		t.Errorf("Depends = %v, want [mg-aaa, mg-bbb]", updated.Depends)
	}
}

func TestUpdateDependsRemove(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Rm dep", []string{"mg-aaa", "mg-bbb", "mg-ccc"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := Update(root, item.ID, UpdateField{RmDepends: []string{"mg-bbb"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Depends) != 2 {
		t.Fatalf("Depends length = %d, want 2", len(updated.Depends))
	}
	for _, d := range updated.Depends {
		if d == "mg-bbb" {
			t.Error("mg-bbb should have been removed")
		}
	}
}

func TestUpdateTags(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Tag test", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tags := []string{"urgent", "backend"}
	updated, err := Update(root, item.ID, UpdateField{Tags: tags})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Tags) != 2 {
		t.Fatalf("Tags = %v, want [urgent backend]", updated.Tags)
	}

	// Verify persistence
	read, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.Tags) != 2 || read.Tags[0] != "urgent" || read.Tags[1] != "backend" {
		t.Errorf("persisted Tags = %v, want [urgent backend]", read.Tags)
	}
}

func TestUpdateAddTags(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// First create and set tags
	item, err := Create(root, "mg-", "task", "Add tag", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tags := []string{"frontend"}
	_, err = Update(root, item.ID, UpdateField{Tags: tags})
	if err != nil {
		t.Fatalf("Update tags: %v", err)
	}

	// Now add incrementally
	updated, err := Update(root, item.ID, UpdateField{AddTags: []string{"urgent"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Tags) != 2 {
		t.Errorf("Tags = %v, want [frontend urgent]", updated.Tags)
	}
}

func TestUpdateRmTags(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Rm tag", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tags := []string{"a", "b", "c"}
	_, err = Update(root, item.ID, UpdateField{Tags: tags})
	if err != nil {
		t.Fatalf("Update tags: %v", err)
	}

	updated, err := Update(root, item.ID, UpdateField{RmTags: []string{"b"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(updated.Tags) != 2 {
		t.Fatalf("Tags length = %d, want 2", len(updated.Tags))
	}
	for _, tag := range updated.Tags {
		if tag == "b" {
			t.Error("tag 'b' should have been removed")
		}
	}
}

func TestUpdateRepo(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Repo test", nil, WithRepo("/old/repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newRepo := "/new/repo"
	updated, err := Update(root, item.ID, UpdateField{Repo: &newRepo})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Repo != "/new/repo" {
		t.Errorf("Repo = %q, want %q", updated.Repo, "/new/repo")
	}
}

func TestUpdateNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	title := "nope"
	_, err := Update(root, "mg-0000", UpdateField{Title: &title})
	if err == nil {
		t.Error("expected error for nonexistent item")
	}
}

func TestUpdateClaimedItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Claimed edit", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate claiming by moving to claimed/
	src := filepath.Join(root, "work", "available", item.ID+".md")
	dst := filepath.Join(root, "work", "claimed", item.ID+".md.12345")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("simulate claim: %v", err)
	}

	newTitle := "Edited while claimed"
	updated, err := Update(root, item.ID, UpdateField{Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Title != "Edited while claimed" {
		t.Errorf("Title = %q, want %q", updated.Title, "Edited while claimed")
	}
}

func TestUpdateMultipleFields(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Multi update", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newTitle := "New title"
	newType := "bug"
	updated, err := Update(root, item.ID, UpdateField{
		Title:    &newTitle,
		Type:     &newType,
		AddTags:  []string{"critical"},
		Depends:  []string{"mg-aaa"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if updated.Title != "New title" {
		t.Errorf("Title = %q", updated.Title)
	}
	if updated.Type != "bug" {
		t.Errorf("Type = %q", updated.Type)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "critical" {
		t.Errorf("Tags = %v", updated.Tags)
	}
	if len(updated.Depends) != 1 || updated.Depends[0] != "mg-aaa" {
		t.Errorf("Depends = %v", updated.Depends)
	}
}

func TestFindPath(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Find me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	path, status, err := FindPath(root, item.ID)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}

	if status != "available" {
		t.Errorf("status = %q, want %q", status, "available")
	}
	if !strings.Contains(path, item.ID) {
		t.Errorf("path %q should contain ID %q", path, item.ID)
	}
}

func TestRender(t *testing.T) {
	item := &Item{
		ID:      "mg-abcd",
		Type:    "bug",
		Creator: "alice",
		Depends: []string{"mg-1111"},
		Tags:    []string{"urgent", "backend"},
		Repo:    "/some/repo",
		Title:   "Fix the thing",
		Body:    "\n# Fix the thing\n\nDetails here.\n",
	}
	item.Created, _ = time.Parse(time.RFC3339, "2026-03-20T16:00:00Z")

	rendered := Render(item)
	parsed, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse(Render()): %v", err)
	}

	if parsed.ID != item.ID {
		t.Errorf("ID = %q, want %q", parsed.ID, item.ID)
	}
	if parsed.Type != item.Type {
		t.Errorf("Type = %q, want %q", parsed.Type, item.Type)
	}
	if len(parsed.Depends) != 1 || parsed.Depends[0] != "mg-1111" {
		t.Errorf("Depends = %v", parsed.Depends)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "urgent" {
		t.Errorf("Tags = %v", parsed.Tags)
	}
	if parsed.Repo != "/some/repo" {
		t.Errorf("Repo = %q", parsed.Repo)
	}
	if parsed.Title != "Fix the thing" {
		t.Errorf("Title = %q", parsed.Title)
	}
}

func TestRenderNoTags(t *testing.T) {
	item := &Item{
		ID:      "mg-abcd",
		Type:    "task",
		Creator: "bob",
		Title:   "No tags",
		Body:    "\n# No tags\n",
	}
	item.Created, _ = time.Parse(time.RFC3339, "2026-03-20T16:00:00Z")

	rendered := Render(item)
	if strings.Contains(rendered, "tags:") {
		t.Error("rendered output should not contain tags: when tags are empty")
	}
}

