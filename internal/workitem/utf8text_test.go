package workitem

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// Negative control first: ordinary multibyte text must still be writable on
// every field the check covers.
func TestUTF8_ValidMultibyteIsAccepted(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Café ✓", nil,
		WithBody("naïve — 🚀\n"), WithTags([]string{"über"}),
		WithAssignee("zoë"), WithRepo("/tmp/résumé"), WithBranch("fix/ß"))
	if err != nil {
		t.Fatalf("valid UTF-8 refused on create: %v", err)
	}
	title, body, app := "Café ✓✓", "# Café ✓✓\n\nnew — body\n", "\nmore — text\n"
	if _, err := Update(root, item.ID, UpdateField{Title: &title, Body: &body}); err != nil {
		t.Fatalf("valid UTF-8 refused on edit: %v", err)
	}
	if _, err := Update(root, item.ID, UpdateField{AppendBody: &app, AddTags: []string{"日本"}}); err != nil {
		t.Fatalf("valid UTF-8 refused on append: %v", err)
	}
}

func TestUTF8_CreateRefusesEachField(t *testing.T) {
	bad := "ok\xc3" // truncated two-byte sequence at offset 2
	cases := []struct {
		name  string
		title string
		opts  []CreateOption
		want  string
	}{
		{"title", "t" + bad, nil, "title: invalid UTF-8 at byte offset 3"},
		{"body", "t", []CreateOption{WithBody(bad)}, "body: invalid UTF-8 at byte offset 2"},
		{"tags", "t", []CreateOption{WithTags([]string{"fine", bad})}, "tags[1]: invalid UTF-8 at byte offset 2"},
		{"assignee", "t", []CreateOption{WithAssignee(bad)}, "assignee: invalid UTF-8 at byte offset 2"},
		{"repo", "t", []CreateOption{WithRepo(bad)}, "repo: invalid UTF-8 at byte offset 2"},
		{"branch", "t", []CreateOption{WithBranch(bad)}, "branch: invalid UTF-8 at byte offset 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			_, err := Create(root, "mg-", "task", tc.title, nil, tc.opts...)
			wantUsageCode(t, err, "invalid_utf8")
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
			if files := storeFiles(t, root); len(files) != 0 {
				t.Fatalf("a refused filing wrote %d file(s): %v", len(files), files)
			}
		})
	}
}

func TestUTF8_UpdateRefusesAndLeavesItemByteIdentical(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "Target", nil, WithBody("# Target\n\noriginal\n"))
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := FindPath(root, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	filesBefore := storeFiles(t, root)

	bad := "caf\xe9"
	cases := []struct {
		name   string
		fields UpdateField
		want   string
	}{
		{"title", UpdateField{Title: &bad}, "title: invalid UTF-8 at byte offset 3"},
		{"body", UpdateField{Body: &bad}, "body: invalid UTF-8 at byte offset 3"},
		{"append-body", UpdateField{AppendBody: &bad}, "append-body: invalid UTF-8 at byte offset 3"},
		{"assignee", UpdateField{Assignee: &bad}, "assignee: invalid UTF-8 at byte offset 3"},
		{"repo", UpdateField{Repo: &bad}, "repo: invalid UTF-8 at byte offset 3"},
		{"tags", UpdateField{Tags: []string{bad}}, "tags[0]: invalid UTF-8 at byte offset 3"},
		{"add-tags", UpdateField{AddTags: []string{"x", bad}}, "add-tags[1]: invalid UTF-8 at byte offset 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Update(root, item.ID, tc.fields)
			wantUsageCode(t, err, "invalid_utf8")
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %q", err, tc.want)
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("a refused edit changed the stored item")
			}
			if got := storeFiles(t, root); len(got) != len(filesBefore) {
				t.Fatalf("a refused edit left files behind (a body backup?): %v", got)
			}
		})
	}
}

// Only introduced text is checked. An item that is ALREADY damaged on disk —
// possible on any store written before this check — must stay editable, and a
// full --body replacement must be able to repair it.
func TestUTF8_DamagedStoredItemStaysEditableAndRepairable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "Damaged", nil, WithBody("# Damaged\n\nplaceholder\n"))
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := FindPath(root, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	damaged := strings.Replace(string(data), "placeholder", "caf\xe9", 1)
	if utf8.ValidString(damaged) {
		t.Fatal("setup: the planted body should be invalid UTF-8")
	}
	if err := os.WriteFile(path, []byte(damaged), 0o644); err != nil {
		t.Fatal(err)
	}

	app := "\nA note appended to a damaged item.\n"
	if _, err := Update(root, item.ID, UpdateField{AppendBody: &app}); err != nil {
		t.Fatalf("appending valid text to a damaged item was refused: %v", err)
	}
	tag := []string{"triaged"}
	if _, err := Update(root, item.ID, UpdateField{AddTags: tag}); err != nil {
		t.Fatalf("a metadata edit on a damaged item was refused: %v", err)
	}

	fixed := "# Damaged\n\ncafé\n"
	if _, err := Update(root, item.ID, UpdateField{Body: &fixed}); err != nil {
		t.Fatalf("a repairing --body replacement was refused: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !utf8.Valid(after) {
		t.Fatal("the body replacement did not repair the item")
	}
}
