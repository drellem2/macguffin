package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The read-modify-write windows p698a8 listed beside mg edit's (editrace_test.go)
// and did not convert: snooze, unsnooze, the predecessor and successor link
// writes, and unclaim's assignee write. Each used to be os.WriteFile on the
// path it had resolved, and os.WriteFile opens O_CREATE|O_TRUNC — so a move
// landing between the read and the write had the write RE-CREATE the item at
// the path it had just left: one item, two status directories.
//
// Each test lands a real concurrent move in exactly that window, through the
// rmwBeforeWrite seam, so it is deterministic rather than load-dependent. They
// were each run against the old write shape (os.WriteFile of Match.Path in
// place of Backend.Edit in modifyItem) and FAILED with two records; they pass
// on the guarded Backend.Edit. See the mg-b979b commit for the transcript.

// raceAt runs move once, the first time the read-modify-write at site reaches
// its write.
func raceAt(t *testing.T, site string, move func()) {
	t.Helper()
	fired := false
	rmwBeforeWrite = func(s string) {
		if s != site || fired {
			return
		}
		fired = true
		move()
	}
	t.Cleanup(func() {
		rmwBeforeWrite = nil
		if !fired {
			t.Errorf("the %s read-modify-write never reached its write; the race was not exercised", site)
		}
	})
}

// recordsOnDisk lists every file in the store naming id, straight off the
// directories — independent of Resolve, so a duplicate cannot hide behind the
// instrument under test.
func recordsOnDisk(t *testing.T, root, id string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(filepath.Join(root, "work"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if name == id+".md" || strings.HasPrefix(name, id+".md.") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// wantOneRecord asserts the item is stored exactly once, in wantDir.
func wantOneRecord(t *testing.T, root, id, wantDir string) {
	t.Helper()
	recs := recordsOnDisk(t, root, id)
	if len(recs) != 1 {
		t.Fatalf("%s is stored %d times after the race — the write re-created the path the move left: %v", id, len(recs), recs)
	}
	if got := filepath.Base(filepath.Dir(recs[0])); got != wantDir {
		t.Fatalf("%s ended in %s/, want %s/ (the concurrent move must win): %v", id, got, wantDir, recs)
	}
}

func TestSnooze_ClaimBetweenReadAndWriteDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "snooze raced", nil)
	if err != nil {
		t.Fatal(err)
	}
	raceAt(t, "snooze", func() {
		if _, err := Claim(root, item.ID, 4343); err != nil {
			t.Errorf("Claim inside the snooze window: %v", err)
		}
	})
	_, _, snoozeErr := SnoozeItem(root, item.ID, time.Now().Add(time.Hour))
	wantOneRecord(t, root, item.ID, "claimed")
	if snoozeErr == nil {
		t.Fatalf("snooze reported success, but the item it was filing into pending/ was claimed under it")
	}
}

func TestUnsnooze_ClaimBetweenReadAndWriteDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	// An available item carrying an inert snooze: unsnooze clears it in place.
	item, err := Create(root, "mg-", "task", "unsnooze raced", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "work", "available", item.ID+".md")
	item.SetSnooze(time.Now().Add(-time.Hour))
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		t.Fatal(err)
	}
	raceAt(t, "unsnooze", func() {
		if _, err := Claim(root, item.ID, 4344); err != nil {
			t.Errorf("Claim inside the unsnooze window: %v", err)
		}
	})
	_, _, _ = UnsnoozeItem(root, item.ID) // may follow the item or refuse
	wantOneRecord(t, root, item.ID, "claimed")
}

func TestPredecessorLink_ClaimBetweenReadAndWriteDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	succ, err := Create(root, "mg-", "task", "the successor", nil)
	if err != nil {
		t.Fatal(err)
	}
	pred, err := Create(root, "mg-", "task", "the predecessor", nil)
	if err != nil {
		t.Fatal(err)
	}
	raceAt(t, "predecessor", func() {
		if _, err := Claim(root, succ.ID, 4345); err != nil {
			t.Errorf("Claim inside the predecessor-link window: %v", err)
		}
	})
	linkErr := linkPredecessor(root, succ.ID, pred.ID)
	wantOneRecord(t, root, succ.ID, "claimed")

	// The link followed the item or was refused — never dropped while
	// reporting success.
	got, err := Read(root, succ.ID)
	if err != nil {
		t.Fatal(err)
	}
	tagged := false
	for _, tag := range got.Tags {
		tagged = tagged || tag == PredecessorTag(pred.ID)
	}
	if linkErr == nil && !tagged {
		t.Fatalf("linkPredecessor reported success but the tag is not on the stored item: %v", got.Tags)
	}
}

func TestSuccessorLink_UnclaimBetweenReadAndWriteDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	design, err := Create(root, "mg-", "design", "the design", nil)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := Create(root, "mg-", "task", "the tracker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(root, design.ID, 4346); err != nil {
		t.Fatal(err)
	}
	m, err := ResolveUnique(root, design.ID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := readMatch(m)
	if err != nil {
		t.Fatal(err)
	}
	raceAt(t, "successor", func() {
		if _, err := Unclaim(root, design.ID); err != nil {
			t.Errorf("Unclaim inside the successor-link window: %v", err)
		}
	})
	_ = linkSuccessor(root, m, item, tracker.ID)
	wantOneRecord(t, root, design.ID, "available")
}

func TestUnclaimAssignee_ReclaimBetweenReadAndWriteDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "unclaim raced", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(root, item.ID, 4347); err != nil {
		t.Fatal(err)
	}
	// A re-stamp is a move WITHIN claimed/: the old write re-created the
	// claim under the PID it had just been renamed away from.
	raceAt(t, "unclaim", func() {
		if _, err := Reclaim(root, item.ID, 4348); err != nil {
			t.Errorf("Reclaim inside the unclaim window: %v", err)
		}
	})
	_, unclaimErr := Unclaim(root, item.ID, WithUnclaimAssignee("parked"))
	recs := recordsOnDisk(t, root, item.ID)
	if len(recs) != 1 {
		t.Fatalf("%s is stored %d times after the race — the write re-created the claim the re-stamp left: %v", item.ID, len(recs), recs)
	}
	if unclaimErr == nil {
		t.Fatalf("unclaim reported success, but it released nothing: the claim was re-stamped under it (%v)", recs)
	}
	if want := filepath.Join("work", "claimed", item.ID+".md.4348"); recs[0] != want {
		t.Fatalf("the re-stamped claim is at %s, want %s", recs[0], want)
	}
}
