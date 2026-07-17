package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// archiveItem is a helper that creates, claims, dones, backdates, and archives
// an item, returning the created item and the year-month partition.
func archiveItem(t *testing.T, root, title string, result []byte) (*Item, string) {
	t.Helper()

	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, result); err != nil {
		t.Fatalf("Done: %v", err)
	}

	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(donePath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if result != nil {
		sidecarPath := filepath.Join(root, "work", "done", item.ID+".result.json")
		if err := os.Chtimes(sidecarPath, old, old); err != nil {
			t.Fatalf("Chtimes sidecar: %v", err)
		}
	}

	if _, err := Archive(root, 7*24*time.Hour); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	return item, old.Format("2006-01")
}

func TestUnarchiveBasic(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, partition := archiveItem(t, root, "Restore me", nil)

	unarchived, restored, err := Unarchive(root, item.ID, "")
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if unarchived.ID != item.ID {
		t.Errorf("ID = %q, want %q", unarchived.ID, item.ID)
	}
	if unarchived.Title != item.Title {
		t.Errorf("Title = %q, want %q", unarchived.Title, item.Title)
	}
	if restored != "done" {
		t.Errorf("restored to %q, want done", restored)
	}

	// archiveItem archives a done item, so the round-trip must land in done/.
	// Restoring to available/ would put finished work back in front of the
	// dispatch loop as if it were fresh.
	donePath := filepath.Join(root, "work", "done", item.ID+".md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected file at %s: %v", donePath, err)
	}

	// File should NOT be in archive/<partition>/
	archivePath := filepath.Join(root, "work", "archive", partition, item.ID+".md")
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("item still in archive/ after unarchive")
	}

	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

func TestUnarchiveMovesSidecar(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, partition := archiveItem(t, root, "Has result", []byte(`{"branch":"fix-x"}`))

	if _, _, err := Unarchive(root, item.ID, ""); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}

	// Sidecar follows the .md to whichever status it was restored to (done/).
	doneSidecar := filepath.Join(root, "work", "done", item.ID+".result.json")
	if _, err := os.Stat(doneSidecar); err != nil {
		t.Errorf("sidecar not moved to done/: %v", err)
	}

	// Sidecar should NOT be in archive/<partition>/
	archivedSidecar := filepath.Join(root, "work", "archive", partition, item.ID+".result.json")
	if _, err := os.Stat(archivedSidecar); !os.IsNotExist(err) {
		t.Error("sidecar still in archive/ after unarchive")
	}
}

func TestUnarchiveNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, _, err := Unarchive(root, "mg-nonexistent", "")
	if err == nil {
		t.Fatal("expected error for nonexistent item")
	}
	if !strings.Contains(err.Error(), "no such work item") {
		t.Errorf("error = %q, want mention of no such work item", err)
	}
}

func TestUnarchiveFailsIfAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Just created", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, err = Unarchive(root, item.ID, "")
	if err == nil {
		t.Fatal("expected error for item in available/")
	}
	if !strings.Contains(err.Error(), "available") || !strings.Contains(err.Error(), "not archived") {
		t.Errorf("error = %q, want mention of available/not archived", err)
	}
}

func TestUnarchiveFailsIfClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "In progress", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, _, err = Unarchive(root, item.ID, "")
	if err == nil {
		t.Fatal("expected error for claimed item")
	}
	if !strings.Contains(err.Error(), "claimed") {
		t.Errorf("error = %q, want mention of claimed", err)
	}
}

func TestUnarchiveFailsIfDone(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Done item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}

	_, _, err = Unarchive(root, item.ID, "")
	if err == nil {
		t.Fatal("expected error for done item")
	}
	if !strings.Contains(err.Error(), "done") {
		t.Errorf("error = %q, want mention of done", err)
	}
}

func TestUnarchiveFailsIfShelved(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Shelved item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Shelve(root, item.ID); err != nil {
		t.Fatalf("Shelve: %v", err)
	}

	_, _, err = Unarchive(root, item.ID, "")
	if err == nil {
		t.Fatal("expected error for shelved item")
	}
	if !strings.Contains(err.Error(), "shelved") {
		t.Errorf("error = %q, want mention of shelved", err)
	}
}

func TestEmitUnarchive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Track me", nil)

	if _, _, err := Unarchive(root, item.ID, ""); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}

	e := findEvent(t, readEvents(t, root), "work.unarchive")
	if e.Extra["item_id"] != item.ID {
		t.Errorf("item_id = %q, want %q", e.Extra["item_id"], item.ID)
	}
	if e.Extra["from_status"] != "archived" {
		t.Errorf("from_status = %q, want archived", e.Extra["from_status"])
	}
	if e.Extra["to_status"] != "done" {
		t.Errorf("to_status = %q, want done", e.Extra["to_status"])
	}
	if e.Extra["actor"] == "" {
		t.Error("actor should be set")
	}
	if _, err := time.Parse(time.RFC3339, e.Ts); err != nil {
		t.Errorf("ts %q not RFC3339: %v", e.Ts, err)
	}
}

// TestUnarchiveRestoresPriorStatusNotAvailable is the regression test for
// mg-a532. An accidentally-archived done item must come back done. The bug it
// pins: unarchive restored to available unconditionally, so a recovered
// gh-issue gate carrier re-entered the dispatch loop looking like fresh work,
// ready to re-ack a reporter a human had already acked.
func TestUnarchiveRestoresPriorStatusNotAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Gate carrier", nil)

	_, restored, err := Unarchive(root, item.ID, "")
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if restored != "done" {
		t.Fatalf("restored to %q, want done", restored)
	}

	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status == "available" {
		t.Fatal("unarchive restored a done item to available: the archive round-trip is not state-preserving")
	}
	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

// TestUnarchiveRoundTripPreservesBody guards the other half of the mayor's
// manual audit: the body (stage: gated, the triage packet, the gh: ref) must
// survive the round-trip intact, not just the status.
func TestUnarchiveRoundTripPreservesBody(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Carrier", nil,
		WithBody("stage: gated\n\ngh: drellem2/pogo#89\n"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := ArchiveItem(root, item.ID); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	restoredItem, restored, err := Unarchive(root, item.ID, "")
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if restored != "done" {
		t.Errorf("restored to %q, want done", restored)
	}
	for _, want := range []string{"stage: gated", "gh: drellem2/pogo#89"} {
		if !strings.Contains(restoredItem.Body, want) {
			t.Errorf("body lost %q after round-trip; body = %q", want, restoredItem.Body)
		}
	}
}

// TestUnarchiveRefusesWhenPriorStatusUnknown covers the ruling: when the store
// cannot say what the item was, unarchive refuses instead of guessing. Here the
// event log has been rotated away, which is exactly when a caller is least able
// to spot a wrong-but-plausible restore.
func TestUnarchiveRefusesWhenPriorStatusUnknown(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, partition := archiveItem(t, root, "No record of me", nil)

	if err := os.Remove(filepath.Join(root, "events.jsonl")); err != nil {
		t.Fatalf("removing event log: %v", err)
	}

	_, _, err := Unarchive(root, item.ID, "")
	if err == nil {
		t.Fatal("expected refusal when the prior status is unknown, got success")
	}
	if me := mgerrOf(t, err); me.Code != "unknown_prior_status" || me.Category != mgerr.CatConflict {
		t.Errorf("error = %v/%v, want conflict/unknown_prior_status", me.Category, me.Code)
	}
	if !strings.Contains(err.Error(), item.ID) {
		t.Errorf("error should name the item; got %q", err)
	}

	// A refusal must not move anything: the item stays put, recoverable.
	archivePath := filepath.Join(root, "work", "archive", partition, item.ID+".md")
	if _, err := os.Stat(archivePath); err != nil {
		t.Errorf("refusal moved the item out of archive/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "available", item.ID+".md")); !os.IsNotExist(err) {
		t.Error("refusal left the item in available/ — it must guess nothing and move nothing")
	}
}

// TestUnarchiveExplicitStatusOverridesLookup is the caller's way out of the
// refusal above, and the way to redirect an item somewhere other than where it
// was. It is an assertion by the caller, so it is taken as given.
func TestUnarchiveExplicitStatusOverridesLookup(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Send me back to work", nil)

	_, restored, err := Unarchive(root, item.ID, "available")
	if err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if restored != "available" {
		t.Errorf("restored to %q, want available", restored)
	}

	status, err := Status(root, item.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != "available" {
		t.Errorf("status = %q, want available", status)
	}
}

// TestUnarchiveExplicitStatusRecoversWithoutEventLog pins the refusal as a
// door, not a wall: the same item that refuses in the dark restores cleanly
// once the caller names the target.
func TestUnarchiveExplicitStatusRecoversWithoutEventLog(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Recover me by hand", nil)
	if err := os.Remove(filepath.Join(root, "events.jsonl")); err != nil {
		t.Fatalf("removing event log: %v", err)
	}

	_, restored, err := Unarchive(root, item.ID, "done")
	if err != nil {
		t.Fatalf("Unarchive with explicit status: %v", err)
	}
	if restored != "done" {
		t.Errorf("restored to %q, want done", restored)
	}
	if status, _ := Status(root, item.ID); status != "done" {
		t.Errorf("status = %q, want done", status)
	}
}

func TestUnarchiveRejectsInvalidStatus(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// "claimed" is rejected on purpose: a claim carries an owning PID that
	// unarchive would have to invent. "banana" is simply not a status.
	for _, status := range []string{"claimed", "archived", "banana"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			item, partition := archiveItem(t, root, "Bad target", nil)

			_, _, err := Unarchive(root, item.ID, status)
			if err == nil {
				t.Fatalf("expected error for --status=%s", status)
			}
			if me := mgerrOf(t, err); me.Code != "invalid_value" || me.Category != mgerr.CatUsage {
				t.Errorf("error = %v/%v, want usage/invalid_value", me.Category, me.Code)
			}

			archivePath := filepath.Join(root, "work", "archive", partition, item.ID+".md")
			if _, err := os.Stat(archivePath); err != nil {
				t.Errorf("rejected status still moved the item: %v", err)
			}
		})
	}
}

// TestUnarchiveUsesMostRecentArchiveRecord: an item archived, restored, and
// archived again must follow its latest transition, not its first.
func TestUnarchiveUsesMostRecentArchiveRecord(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, _ := archiveItem(t, root, "Twice around", nil)

	// First round-trip, redirected to available.
	if _, restored, err := Unarchive(root, item.ID, "available"); err != nil {
		t.Fatalf("first Unarchive: %v", err)
	} else if restored != "available" {
		t.Fatalf("first restore to %q, want available", restored)
	}

	// Take it to done and archive it a second time.
	if _, err := Claim(root, item.ID, 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := ArchiveItem(root, item.ID); err != nil {
		t.Fatalf("ArchiveItem: %v", err)
	}

	_, restored, err := Unarchive(root, item.ID, "")
	if err != nil {
		t.Fatalf("second Unarchive: %v", err)
	}
	if restored != "done" {
		t.Errorf("restored to %q, want done (the most recent archive record)", restored)
	}
}
