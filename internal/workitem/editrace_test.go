package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The read-then-write window in UpdateWithBodyChange, tested.
//
// The edit resolves the item's path (FindPath), reads it, applies fields, and
// writes the whole file back to the path it resolved. A claim is a rename(2) of
// that file out of available/. If the claim lands between the edit's read and
// its write, the write targets a path the item has LEFT. The design memo for the
// storage seam inferred, without testing, that os.WriteFile's O_CREATE then
// re-creates the old path — leaving one item in two status directories, one of
// them claimable again. This test drives that exact interleaving through the
// editBeforeWrite seam, so it is deterministic rather than load-dependent.
func TestUpdate_ClaimBetweenReadAndWriteDoesNotRecreateOldPath(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Raced", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := item.ID

	const pid = 4242
	claimed := false
	editBeforeWrite = func() {
		if claimed {
			return
		}
		claimed = true
		if _, err := Claim(root, id, pid); err != nil {
			t.Errorf("Claim inside the edit window: %v", err)
		}
	}
	defer func() { editBeforeWrite = nil }()

	_, editErr := Update(root, id, UpdateField{AddTags: []string{"raced"}})

	oldPath := filepath.Join(root, "work", "available", id+".md")
	if _, err := os.Stat(oldPath); err == nil {
		t.Fatalf("the edit re-created %s after the claim moved the item to claimed/ — one item now lives in two status directories (edit err: %v)", oldPath, editErr)
	}

	matches, err := Resolve(root, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(matches) != 1 || matches[0].Status != "claimed" {
		t.Fatalf("want exactly one record, in claimed/; got %+v", matches)
	}

	// The claim must have survived intact, and the edit must either have
	// landed on the item where it now is or have been refused — never
	// silently dropped while reporting success.
	data, err := os.ReadFile(matches[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	tagged := strings.Contains(string(data), "raced")
	if editErr == nil && !tagged {
		t.Fatalf("edit reported success but its tag is not on the stored item:\n%s", data)
	}
	if editErr != nil && tagged {
		t.Fatalf("edit was refused (%v) but its tag landed anyway", editErr)
	}
}
