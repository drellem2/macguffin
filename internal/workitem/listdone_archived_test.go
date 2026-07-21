package workitem

import (
	"os"
	"path/filepath"
	"testing"
)

// hasID reports whether items contains an item whose ID is exactly id. It exists
// so the assertions below match on the ID *column*, never a substring — the
// mg-c13b report's "INCLUDES mg-de08" was an instrument error in which a grep of
// the human-formatted listing matched OTHER items whose titles merely mentioned
// "de08". A test that guards this invariant must not repeat that mistake.
func hasID(items []*Item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// TestDoneListingExcludesArchivedViaBothPaths is the mg-c13b regression guard.
//
// `mg list --status=done` reads work/done/ via ListByStatus(root, "done"). An
// item that has been archived lives in work/archive/<partition>/ and must not
// appear in that done listing — regardless of *which* archive path moved it:
//
//   - the LEGACY sweep  Archive(root, 0)   (the old `mg archive --days=0` form), and
//   - the TARGETED path ArchiveItem(root, id) (mg-322f's `mg archive <id>`).
//
// Both route their moves through archiveFile, which os.Rename's the .md out of
// done/, so neither leaves a stale copy behind. This test pins that: an item
// archived by either path is absent from the done listing, present in the
// archive listing exactly once, and leaves no artifact in done/. A live control
// item that was never archived must remain visible, so the exclusion cannot be
// achieved by simply emptying the listing.
func TestDoneListingExcludesArchivedViaBothPaths(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Archived by the legacy --days=0 sweep. Create it alone first so the
	// unfiltered sweep takes exactly this item and nothing else.
	legacy := doneItem(t, root, "archived by legacy --days=0 sweep")
	swept, _, err := Archive(root, 0)
	if err != nil {
		t.Fatalf("Archive(root, 0): %v", err)
	}
	if len(swept) != 1 || swept[0].ID != legacy.ID {
		t.Fatalf("legacy sweep archived %v, want just %s", swept, legacy.ID)
	}

	// Archived by the targeted mg-322f path.
	targeted := doneItem(t, root, "archived by targeted mg archive <id>")
	if _, err := ArchiveItem(root, targeted.ID, ArchiveOpts{}); err != nil {
		t.Fatalf("ArchiveItem(%s): %v", targeted.ID, err)
	}

	// A live control that was never archived: it must stay in the done listing,
	// proving the exclusion is targeted and not a listing that lies the other way.
	live := doneItem(t, root, "still done, never archived")

	done, err := ListByStatus(root, "done")
	if err != nil {
		t.Fatalf("ListByStatus(done): %v", err)
	}
	if hasID(done, legacy.ID) {
		t.Errorf("mg list --status=done includes %s, which was archived by the legacy --days=0 sweep", legacy.ID)
	}
	if hasID(done, targeted.ID) {
		t.Errorf("mg list --status=done includes %s, which was archived by the targeted path", targeted.ID)
	}
	if !hasID(done, live.ID) {
		t.Errorf("mg list --status=done is missing %s, a live done item that was never archived", live.ID)
	}

	// Both archived items must be present in the archive listing, exactly once
	// each — no stale twin left in done/ that a duplicate move could produce.
	archived, err := ListArchived(root)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	for _, id := range []string{legacy.ID, targeted.ID} {
		if n := countID(archived, id); n != 1 {
			t.Errorf("archive listing has %d copies of %s, want exactly 1", n, id)
		}
	}
	if hasID(archived, live.ID) {
		t.Errorf("archive listing includes %s, which was never archived", live.ID)
	}

	// The crux of the mg-c13b hypothesis ("the old path leaves a stale artifact"):
	// neither archived item may leave a .md behind in done/.
	for _, id := range []string{legacy.ID, targeted.ID} {
		if _, err := os.Stat(filepath.Join(root, "work", "done", id+".md")); !os.IsNotExist(err) {
			t.Errorf("%s left a stale .md in done/ after archiving (err=%v)", id, err)
		}
	}
}

// countID returns how many items in the slice have exactly the given ID.
func countID(items []*Item, id string) int {
	n := 0
	for _, it := range items {
		if it.ID == id {
			n++
		}
	}
	return n
}
