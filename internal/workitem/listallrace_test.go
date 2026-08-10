package workitem

import (
	"os"
	"path/filepath"
	"testing"
)

// ListAll walks four directories with four separate ReadDirs and nothing holds
// the store still across them. Since mg-ad63 every invocation runs the
// auto-promoter, so a rename landing mid-walk is no longer a rare interleaving
// between two agents — it is the ordinary case, and the walk order decides
// whether that rename is survivable.
//
// These tests pin the two halves of the fix: the order (sources before
// destinations, so nothing can fall through the gap) and the dedupe (which
// cleans up the duplicate that order deliberately produces).

// TestListAllReadsTransitionSourcesBeforeDestinations is the invariant that
// makes an item impossible to miss.
//
// A rename between two reads is seen twice if the SOURCE is read first, and not
// at all if the DESTINATION is read first. Only one of those is recoverable, so
// every transition mg performs must appear in listAllOrder source-first:
// promotion (pending -> available), claiming (available -> claimed), and
// completion (claimed -> done).
//
// This asserts the order directly rather than by racing it. The window is
// microseconds wide and a timing test that passes proves nothing; the ordering
// is the actual contract, so the ordering is what gets checked.
//
// The list below is the FORWARD flow only, and that is a real limit rather than
// an oversight: `mg unclaim` (claimed -> available) and `mg reopen` (done ->
// available) run backwards along it, and a cycle cannot be linearised. Those two
// edges still have the vanishing window. Do not read a passing test here as
// "ListAll is race-free" — read it as "the transition that fires on every mg
// invocation is safe, and the two that fire on an explicit human action are not".
func TestListAllReadsTransitionSourcesBeforeDestinations(t *testing.T) {
	pos := make(map[string]int, len(listAllOrder))
	for i, status := range listAllOrder {
		pos[status] = i
	}

	transitions := []struct {
		src, dst, what string
	}{
		{"pending", "available", "promotion of an elapsed snooze"},
		{"available", "claimed", "an agent claiming work"},
		{"claimed", "done", "an agent finishing work"},
	}
	for _, tr := range transitions {
		srcAt, ok := pos[tr.src]
		if !ok {
			t.Fatalf("listAllOrder does not read %s/ at all", tr.src)
		}
		dstAt, ok := pos[tr.dst]
		if !ok {
			t.Fatalf("listAllOrder does not read %s/ at all", tr.dst)
		}
		if srcAt > dstAt {
			t.Errorf("listAllOrder reads %s/ before %s/, so %s landing mid-walk makes the item vanish from both: %v",
				tr.dst, tr.src, tr.what, listAllOrder)
		}
	}
}

// TestListAllReportsAnItemCaughtMidRenameExactlyOnce exercises the state a
// mid-walk rename actually presents to the reader.
//
// Reading sources first means an item that moves during the walk is observed on
// both sides of its rename — which, from ListAll's point of view, is
// indistinguishable from the same ID being present in two directories at once.
// So that is what the test builds, directly and deterministically, rather than
// trying to hit a microsecond window.
//
// The item must come back exactly once, and under available/: the walk reads
// pending/ first, so the later sighting is the one after the rename, and the
// later sighting is the item's newer location.
func TestListAllReportsAnItemCaughtMidRenameExactlyOnce(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "caught mid-rename", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create leaves the item in available/. Copy it back into pending/ so the
	// same ID is in both, which is precisely what a scan straddling the
	// promoter's rename sees.
	availablePath := filepath.Join(root, "work", "available", item.ID+".md")
	data, err := os.ReadFile(availablePath)
	if err != nil {
		t.Fatalf("reading %s: %v", availablePath, err)
	}
	pendingPath := filepath.Join(root, "work", "pending", item.ID+".md")
	if err := os.WriteFile(pendingPath, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", pendingPath, err)
	}

	grouped, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var sightings []string
	for status, items := range grouped {
		for _, it := range items {
			if it.ID == item.ID {
				sightings = append(sightings, status)
			}
		}
	}
	if len(sightings) != 1 {
		t.Fatalf("item %s reported %d times %v, want exactly once — an item mid-rename must be neither dropped nor doubled",
			item.ID, len(sightings), sightings)
	}
	if sightings[0] != "available" {
		t.Errorf("item %s reported under %s, want available — the later sighting is the post-rename one",
			item.ID, sightings[0])
	}
}

// A group emptied entirely by dedupe must not linger as an empty slice. Callers
// range over the map and print a heading per non-empty status; a retained empty
// group prints a heading with nothing under it.
func TestListAllDropsAGroupLeftEmptyByDedupe(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "only item", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "work", "available", item.ID+".md"))
	if err != nil {
		t.Fatalf("reading item: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "work", "pending", item.ID+".md"), data, 0o644); err != nil {
		t.Fatalf("writing pending copy: %v", err)
	}

	grouped, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if items, present := grouped["pending"]; present {
		t.Errorf("pending/ contributed only the deduped sighting, so it must not appear as a group at all, got %d items", len(items))
	}
}
