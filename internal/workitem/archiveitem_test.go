package workitem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// doneItem creates, claims and completes an item, returning it.
func doneItem(t *testing.T, root, title string) *Item {
	t.Helper()

	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	return item
}

func TestArchiveItemArchivesNamedItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item := doneItem(t, root, "targeted")

	got, err := ArchiveItem(root, item.ID, ArchiveOpts{})
	if err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}
	if got.ID != item.ID {
		t.Errorf("archived ID = %q, want %q", got.ID, item.ID)
	}

	if _, err := os.Stat(filepath.Join(root, "work", "done", item.ID+".md")); !os.IsNotExist(err) {
		t.Error("item still in done/ after ArchiveItem")
	}

	archived, err := ListArchived(root)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != item.ID {
		t.Errorf("archive contents = %v, want just %s", archived, item.ID)
	}
}

// TestArchiveItemIsFresh guards the age dimension: the targeted form must not
// inherit the sweep's threshold. A just-completed item is archivable by ID.
func TestArchiveItemIsFresh(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item := doneItem(t, root, "brand new")

	// The sweep would decline this item at the default threshold...
	swept, _, err := Archive(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if len(swept) != 0 {
		t.Fatalf("sweep archived %d fresh item(s), want 0", len(swept))
	}

	// ...but naming it explicitly must work.
	if _, err := ArchiveItem(root, item.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem on fresh item: %v", err)
	}
}

// TestArchiveItemTouchesNothingElse is the regression guard for the composed
// failure in mg-322f: routine archiving silently removed items that callers
// still needed (gh-issue gate carriers parked in done/). The targeted form
// must move the named item and *only* the named item — never widening to the
// sweep, whose --days=0 form takes everything in done/.
func TestArchiveItemTouchesNothingElse(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	target := doneItem(t, root, "the one I asked for")
	carrier := doneItem(t, root, "gate carrier — must survive")
	other := doneItem(t, root, "bystander")

	// Backdate the bystanders past every plausible sweep threshold, so that a
	// fallback into Archive() semantics would certainly take them.
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, id := range []string{carrier.ID, other.ID} {
		p := filepath.Join(root, "work", "done", id+".md")
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	if _, err := ArchiveItem(root, target.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	for _, id := range []string{carrier.ID, other.ID} {
		if _, err := os.Stat(filepath.Join(root, "work", "done", id+".md")); err != nil {
			t.Errorf("ArchiveItem removed %s, which it was not asked to touch: %v", id, err)
		}
	}

	archived, err := ListArchived(root)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("archived %d items, want exactly 1 (the named one)", len(archived))
	}
	if archived[0].ID != target.ID {
		t.Errorf("archived %s, want %s", archived[0].ID, target.ID)
	}
}

// TestArchiveItemUnknownIDArchivesNothing is the destructive-edge guard: a bad
// id must be an error, and must NOT degrade into a sweep.
func TestArchiveItemUnknownIDArchivesNothing(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	survivor := doneItem(t, root, "must survive a bad id")
	old := time.Now().Add(-30 * 24 * time.Hour)
	p := filepath.Join(root, "work", "done", survivor.ID+".md")
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, err := ArchiveItem(root, "mg-nope", ArchiveOpts{}); err == nil {
		t.Fatal("ArchiveItem on unknown id returned nil error — a no-op must be loud")
	}

	if _, err := os.Stat(p); err != nil {
		t.Errorf("ArchiveItem with an unknown id archived %s anyway: %v", survivor.ID, err)
	}
}

func TestArchiveItemRejectsNonDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	avail, err := Create(root, "mg-", "task", "still available", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, err := Create(root, "mg-", "task", "in progress", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, claimed.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"available", avail.ID},
		{"claimed", claimed.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ArchiveItem(root, tc.id, ArchiveOpts{})
			if err == nil {
				t.Fatalf("ArchiveItem on %s item returned nil error", tc.name)
			}
			var mge *mgerr.Error
			if !errors.As(err, &mge) {
				t.Fatalf("error is %T, want *mgerr.Error", err)
			}
			if mge.Category != mgerr.CatConflict {
				t.Errorf("category = %v, want conflict", mge.Category)
			}
		})
	}
}

func TestArchiveItemRejectsAlreadyArchived(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item := doneItem(t, root, "archive me twice")
	if _, err := ArchiveItem(root, item.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("first ArchiveItem: %v", err)
	}

	_, err := ArchiveItem(root, item.ID, ArchiveOpts{})
	if err == nil {
		t.Fatal("second ArchiveItem returned nil error, want a conflict")
	}
	var mge *mgerr.Error
	if !errors.As(err, &mge) {
		t.Fatalf("error is %T, want *mgerr.Error", err)
	}
	if mge.Category != mgerr.CatConflict {
		t.Errorf("category = %v, want conflict", mge.Category)
	}
}

// TestArchiveItemMovesSidecar mirrors the sweep's sidecar contract (mg-ab67):
// the .result.json must travel with the .md.
func TestArchiveItemMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "with result", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, []byte(`{"branch":"x"}`)); err != nil {
		t.Fatalf("Done: %v", err)
	}

	if _, err := ArchiveItem(root, item.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "work", "done", item.ID+".result.json")); !os.IsNotExist(err) {
		t.Error("sidecar left behind in done/")
	}

	partition := time.Now().Format("2006-01")
	sidecar := filepath.Join(root, "work", "archive", partition, item.ID+".result.json")
	if _, err := os.Stat(sidecar); err != nil {
		t.Errorf("sidecar missing at %s: %v", sidecar, err)
	}
}

func TestArchiveDryRunMovesNothing(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item := doneItem(t, root, "preview me")
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(donePath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	preview, _, err := ArchiveDryRun(root, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("ArchiveDryRun: %v", err)
	}
	if len(preview) != 1 || preview[0].ID != item.ID {
		t.Fatalf("preview = %v, want just %s", preview, item.ID)
	}

	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("ArchiveDryRun moved %s — a preview must not mutate: %v", item.ID, err)
	}
}

// TestArchiveDryRunMatchesArchive keeps the preview honest: what it lists must
// be exactly what the sweep then does.
func TestArchiveDryRunMatchesArchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	for _, title := range []string{"a", "b", "c"} {
		doneItem(t, root, title)
	}

	preview, _, err := ArchiveDryRun(root, 0)
	if err != nil {
		t.Fatalf("ArchiveDryRun: %v", err)
	}
	archived, _, err := Archive(root, 0)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	if len(preview) != len(archived) {
		t.Fatalf("preview listed %d items, sweep archived %d", len(preview), len(archived))
	}

	seen := map[string]bool{}
	for _, i := range preview {
		seen[i.ID] = true
	}
	for _, i := range archived {
		if !seen[i.ID] {
			t.Errorf("sweep archived %s, which the preview did not list", i.ID)
		}
	}
}
