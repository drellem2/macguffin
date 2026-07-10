package workitem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// pinClock freezes the mint clock for the duration of a test. Create hashes
// (title, created) into the short ID, so a pinned clock plus a repeated title
// forces the collision that is otherwise a 1-in-65,536 event.
func pinClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = prev })
}

func fixedTime() time.Time {
	return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
}

func mgerrOf(t *testing.T, err error) *mgerr.Error {
	t.Helper()
	var me *mgerr.Error
	if !errors.As(err, &me) {
		t.Fatalf("error is not *mgerr.Error: %v", err)
	}
	return me
}

// TestCreate_CollidingIDsBothSurvive is the data-loss regression test.
//
// Both Creates hash the SAME title at the SAME instant, so both derive the same
// nonce-0 short ID. The old implementation wrote with os.WriteFile, which
// truncates: the second mint silently destroyed the first item's file and both
// Items claimed the same ID. Now the second mint must remint onto a fresh ID and
// both items must survive intact.
func TestCreate_CollidingIDsBothSurvive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	pinClock(t, fixedTime())

	const title = "Auth tokens broken"

	first, err := Create(root, "mg-", "bug", title, nil, WithAssignee("alice"), WithBody("\n# "+title+"\nfirst body\n"))
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Same title, same pinned clock: the naive ID is identical.
	if want := GenerateID("mg-", title, fixedTime()); first.ID != want {
		t.Fatalf("first item should hold the nonce-0 ID %q, got %q", want, first.ID)
	}

	second, err := Create(root, "mg-", "bug", title, nil, WithAssignee("bob"), WithBody("\n# "+title+"\nsecond body\n"))
	if err != nil {
		t.Fatalf("second Create (must remint, not truncate): %v", err)
	}

	if second.ID == first.ID {
		t.Fatalf("second mint reused the colliding ID %q — the first item was overwritten", first.ID)
	}

	// Both files exist, and each holds its own item.
	for _, tc := range []struct {
		item     *Item
		assignee string
		body     string
	}{
		{first, "alice", "first body"},
		{second, "bob", "second body"},
	} {
		path := filepath.Join(root, "work", "available", tc.item.ID+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: item file missing after the colliding mint: %v", tc.item.ID, err)
		}
		got, err := Parse(string(data))
		if err != nil {
			t.Fatalf("%s: unparseable: %v", tc.item.ID, err)
		}
		if got.ID != tc.item.ID {
			t.Errorf("%s: file holds id %q", tc.item.ID, got.ID)
		}
		if got.Assignee != tc.assignee {
			t.Errorf("%s: assignee = %q, want %q — the wrong item's content is at this path", tc.item.ID, got.Assignee, tc.assignee)
		}
		if !strings.Contains(got.Body, tc.body) {
			t.Errorf("%s: body lost %q", tc.item.ID, tc.body)
		}
	}

	// And each resolves unambiguously to itself.
	for _, item := range []*Item{first, second} {
		got, err := Read(root, item.ID)
		if err != nil {
			t.Fatalf("Read(%s): %v", item.ID, err)
		}
		if got.Assignee != item.Assignee {
			t.Errorf("Read(%s) returned the wrong item (assignee %q)", item.ID, got.Assignee)
		}
	}
}

// TestCreate_NeverTruncatesAnExistingFile pins the O_EXCL guarantee directly:
// whatever sits at the target path, Create must not write through it. This is
// the assertion that fails on the pre-fix os.WriteFile implementation.
func TestCreate_NeverTruncatesAnExistingFile(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	pinClock(t, fixedTime())

	const title = "victim"
	id := GenerateID("mg-", title, fixedTime())
	victim := filepath.Join(root, "work", "available", id+".md")

	sentinel := "---\nid: " + id + "\ntype: task\ncreated: 2026-01-01T00:00:00Z\ncreator: alice\ndepends: []\n---\n\n# victim\nDO NOT DESTROY\n"
	if err := os.WriteFile(victim, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.ID == id {
		t.Fatalf("Create reused the occupied ID %q", id)
	}

	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("victim file gone: %v", err)
	}
	if string(data) != sentinel {
		t.Fatalf("victim file was modified — os.WriteFile-style truncation is back:\n%s", data)
	}
}

// TestCreate_RemintsAroundArchivedAndShelvedIDs covers the alias half of the
// bug: O_EXCL only guards the one directory being written, but every collision
// observed in the wild lives in archive/ or shelved/. A new item must not be
// born as an alias of an old one.
func TestCreate_RemintsAroundArchivedAndShelvedIDs(t *testing.T) {
	for _, dir := range []string{
		filepath.Join("archive", "2026-05"),
		"shelved",
		"done",
	} {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			pinClock(t, fixedTime())

			const title = "occupied elsewhere"
			id := GenerateID("mg-", title, fixedTime())

			occupied := filepath.Join(root, "work", dir)
			if err := os.MkdirAll(occupied, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(occupied, id+".md"), []byte("---\nid: "+id+"\n---\n\n# old\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			item, err := Create(root, "mg-", "task", title, nil)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if item.ID == id {
				t.Fatalf("new item was minted as an alias of the %s item %q", dir, id)
			}
			if matches, _ := Resolve(root, item.ID); len(matches) != 1 {
				t.Fatalf("new id %q resolves to %d matches, want 1", item.ID, len(matches))
			}
		})
	}
}

// TestCreate_RetryIsBounded is the anti-infinite-loop test.
//
// GenerateID is a deterministic hash, not a random draw: a remint loop that
// does not perturb the hash input re-derives the identical ID forever. Here
// every ID the loop can possibly produce is pre-occupied, so a nonce-less retry
// would spin. Create must give up and return an error, promptly.
func TestCreate_RetryIsBounded(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	pinClock(t, fixedTime())

	const title = "wall to wall"

	// Occupy every ID reachable by nonce 0..maxMintAttempts-1. Distinct nonces
	// can hash to the same 2-byte ID, so this may be fewer than 64 files.
	occupied := map[string]bool{}
	for nonce := 0; nonce < maxMintAttempts; nonce++ {
		id := generateID("mg-", title, fixedTime(), nonce)
		if occupied[id] {
			continue
		}
		occupied[id] = true
		if err := os.WriteFile(filepath.Join(root, "work", "available", id+".md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	type result struct {
		item *Item
		err  error
	}
	done := make(chan result, 1)
	go func() {
		item, err := Create(root, "mg-", "task", title, nil)
		done <- result{item, err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("Create succeeded with id %q, but every reachable id was occupied", r.item.ID)
		}
		if code := mgerrOf(t, r.err).Code; code != "id_exhausted" {
			t.Errorf("code = %q, want id_exhausted", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Create did not terminate — the remint loop is not perturbing the hash input")
	}
}

// TestGenerateID_NonceZeroIsHistorical guards the compatibility of every ID ever
// minted: nonce 0 must keep hashing exactly (title, created).
func TestGenerateID_NonceZeroIsHistorical(t *testing.T) {
	now := fixedTime()
	if got, want := generateID("mg-", "t", now, 0), GenerateID("mg-", "t", now); got != want {
		t.Errorf("nonce 0 changed the historical id: %q != %q", got, want)
	}
	seen := map[string]bool{}
	for nonce := 0; nonce < 8; nonce++ {
		seen[generateID("mg-", "t", now, nonce)] = true
	}
	if len(seen) < 4 {
		t.Errorf("nonce barely perturbs the hash: 8 nonces produced %d distinct ids", len(seen))
	}
}

func TestResolve_FindsEveryMatch(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id := "mg-c2af"
	writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")
	writeAt(t, root, filepath.Join("archive", "2026-07"), id+".md")

	matches, err := Resolve(root, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("Resolve found %d matches, want 2: %+v", len(matches), matches)
	}
	// Ascending partition order, so an older month would have shadowed a newer.
	if matches[0].Partition != "2026-05" || matches[1].Partition != "2026-07" {
		t.Errorf("partitions = %q, %q", matches[0].Partition, matches[1].Partition)
	}
	for _, m := range matches {
		if m.Status != "archived" {
			t.Errorf("status = %q, want archived", m.Status)
		}
	}
}

func TestResolve_ClaimedPIDSuffixAndSidecars(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	writeAt(t, root, "claimed", "mg-1234.md.991")
	writeAt(t, root, "done", "mg-5678.md")
	writeAt(t, root, "done", "mg-5678.result.json")

	if m, err := ResolveUnique(root, "mg-1234"); err != nil || m.Status != "claimed" {
		t.Errorf("claimed pid-suffixed file: %+v, %v", m, err)
	}
	// The sidecar must not count as a second match.
	if matches, _ := Resolve(root, "mg-5678"); len(matches) != 1 {
		t.Errorf("result sidecar counted as a match: %+v", matches)
	}
}

// TestAmbiguousID_IsLoudEverywhere is the anti-silent-wrong-answer test: every
// command that resolves an ID must refuse rather than guess.
func TestAmbiguousID_IsLoudEverywhere(t *testing.T) {
	id := "mg-c2af"

	// Each case seeds a live copy plus an archived copy of the same ID, then
	// runs the command that would previously have taken the first hit.
	cases := []struct {
		name string
		live string // directory holding the live copy
		file string
		run  func(root string) error
	}{
		{"read", "available", id + ".md", func(root string) error { _, err := Read(root, id); return err }},
		{"status", "available", id + ".md", func(root string) error { _, err := Status(root, id); return err }},
		{"findpath", "available", id + ".md", func(root string) error { _, _, err := FindPath(root, id); return err }},
		{"claim", "available", id + ".md", func(root string) error { _, err := Claim(root, id, 1); return err }},
		{"shelve", "available", id + ".md", func(root string) error { _, err := Shelve(root, id); return err }},
		{"edit", "available", id + ".md", func(root string) error {
			typ := "bug"
			_, err := Update(root, id, UpdateField{Type: &typ})
			return err
		}},
		{"unclaim", "claimed", id + ".md.991", func(root string) error { _, err := Unclaim(root, id); return err }},
		{"done", "claimed", id + ".md.991", func(root string) error { _, _, err := Done(root, id, nil); return err }},
		{"reopen", "done", id + ".md", func(root string) error { _, err := Reopen(root, id); return err }},
		{"unshelve", "shelved", id + ".md", func(root string) error { _, err := Unshelve(root, id); return err }},
		{"unarchive", "available", id + ".md", func(root string) error { _, err := Unarchive(root, id); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			writeAt(t, root, tc.live, tc.file)
			writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")

			err := tc.run(root)
			if err == nil {
				t.Fatalf("%s silently picked one of two items sharing id %s", tc.name, id)
			}
			me := mgerrOf(t, err)
			if me.Code != "ambiguous_id" {
				t.Fatalf("code = %q, want ambiguous_id (err: %v)", me.Code, err)
			}
			if me.Category != mgerr.CatConflict {
				t.Errorf("category = %v, want conflict", me.Category)
			}
			// The message must name both candidates, or it is not actionable.
			for _, want := range []string{filepath.Join("work", "archive", "2026-05", id+".md"), filepath.Join("work", tc.live, tc.file)} {
				if !strings.Contains(me.Message, want) {
					t.Errorf("message does not name candidate %q:\n%s", want, me.Message)
				}
			}
		})
	}
}

// TestResolve_UnknownIDIsNotFound keeps the ambiguity work from swallowing the
// plain missing-item case.
func TestResolve_UnknownIDIsNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := ResolveUnique(root, "mg-dead")
	if err == nil {
		t.Fatal("want an error for an unknown id")
	}
	if code := mgerrOf(t, err).Code; code != "no_such_item" {
		t.Errorf("code = %q, want no_such_item", code)
	}
}

func TestReadWithStatus_SingleResolve(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	created, err := Create(root, "mg-", "task", "a title", nil)
	if err != nil {
		t.Fatal(err)
	}

	item, status, err := ReadWithStatus(root, created.ID)
	if err != nil {
		t.Fatalf("ReadWithStatus: %v", err)
	}
	if item.ID != created.ID || status != "available" {
		t.Errorf("got (%s, %s), want (%s, available)", item.ID, status, created.ID)
	}

	// Ambiguity is caught here too, so `show` can never render one item's body
	// under another item's status.
	writeAt(t, root, filepath.Join("archive", "2026-05"), created.ID+".md")
	if _, _, err := ReadWithStatus(root, created.ID); err == nil {
		t.Fatal("ReadWithStatus should error on an ambiguous id")
	}
}

// writeAt writes a minimal valid work item named file into work/<dir>/.
func writeAt(t *testing.T, root, dir, file string) {
	t.Helper()
	full := filepath.Join(root, "work", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	id := file
	if i := strings.Index(id, ".md"); i >= 0 {
		id = id[:i]
	}
	body := "---\nid: " + id + "\ntype: task\ncreated: 2026-05-04T18:19:17Z\ncreator: tester\ndepends: []\n---\n\n# " + id + " in " + dir + "\n"
	if err := os.WriteFile(filepath.Join(full, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
