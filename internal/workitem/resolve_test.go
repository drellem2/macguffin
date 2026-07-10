package workitem

import (
	"bytes"
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

// TestCreate_RemintsAroundLooseArchivedID is the mg-c3f9 hole-closing test, and
// its own positive control. It strands a minted item LOOSE in work/archive/
// (no partition) — the exact shape a9c0 swept out of the real store by hand —
// and asserts Create refuses to re-mint that ID. Against the pre-fix reader,
// which skipped every non-directory under archive/, this loose twin was
// invisible: the collision check passed and the new item was born a silent
// alias. This test FAILS there and passes only once Resolve is total.
func TestCreate_RemintsAroundLooseArchivedID(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	pinClock(t, fixedTime())

	const title = "stranded loose in archive"
	id := GenerateID("mg-", title, fixedTime())

	// Strand a record loose directly under archive/ — NOT inside a partition.
	archiveRoot := filepath.Join(root, "work", "archive")
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveRoot, id+".md"), []byte("---\nid: "+id+"\n---\n\n# old loose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.ID == id {
		t.Fatalf("new item was minted as a silent alias of the loose archived item %q — the reader is not total", id)
	}
	if matches, _ := Resolve(root, item.ID); len(matches) != 1 {
		t.Fatalf("new id %q resolves to %d matches, want 1", item.ID, len(matches))
	}

	// The stranded record itself is now resolvable, as archived.
	m, err := ResolveUnique(root, id)
	if err != nil {
		t.Fatalf("ResolveUnique(stranded loose id): %v", err)
	}
	if m.Status != "archived" || m.Partition != "" {
		t.Errorf("stranded record resolved as (%q, partition=%q), want (archived, empty)", m.Status, m.Partition)
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

// TestResolve_LooseArchivedFile is the mg-c3f9 fix: an archived record sitting
// LOOSE directly under work/archive/ (not in a month partition) must resolve,
// as archived with an empty partition. The pre-fix reader skipped every
// non-directory under archive/, so a loose twin was invisible — and a new mint
// drawing its ID slipped past Create's whole-store collision check.
func TestResolve_LooseArchivedFile(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id := "mg-c2af"
	writeAt(t, root, "archive", id+".md") // loose, no partition

	matches, err := Resolve(root, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("Resolve found %d matches, want 1: %+v", len(matches), matches)
	}
	m := matches[0]
	if m.Status != "archived" {
		t.Errorf("status = %q, want archived", m.Status)
	}
	if m.Partition != "" {
		t.Errorf("partition = %q, want empty for a loose archived record", m.Partition)
	}
	if want := filepath.Join(root, "work", "archive", id+".md"); m.Path != want {
		t.Errorf("path = %q, want %q", m.Path, want)
	}

	// A single loose archived record is answerable, not ambiguous.
	if got, err := ResolveUnique(root, id); err != nil || got.Status != "archived" {
		t.Errorf("ResolveUnique(loose archived) = (%+v, %v)", got, err)
	}
}

// TestResolve_LooseArchiveStrictMatch pins the strictness of the loose-archive
// scan: only "<id>.md" counts. A total scan must not ingest editor backups,
// result sidecars, or stray PID-suffixed files sitting loose under archive/ as
// phantom twins — that would manufacture ambiguity errors out of noise.
func TestResolve_LooseArchiveStrictMatch(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	archiveRoot := filepath.Join(root, "work", "archive")
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "mg-c2af"
	// The one true record, buried in noise that all names the same id.
	for _, name := range []string{
		id + ".md",          // the only match
		id + ".md.bak",      // editor backup
		id + ".md~",         // editor backup
		id + ".result.json", // result sidecar
		id + ".md.991",      // PID-suffixed form — legal in claimed/, not archive/
	} {
		if err := os.WriteFile(filepath.Join(archiveRoot, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := Resolve(root, id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("loose-archive scan matched %d entries, want exactly 1 (<id>.md): %+v", len(matches), matches)
	}
	if want := filepath.Join(archiveRoot, id+".md"); matches[0].Path != want {
		t.Errorf("matched the wrong loose entry: %q, want %q", matches[0].Path, want)
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

// captureShadowNotice redirects the resolver's stderr note into a buffer for
// the duration of a test.
func captureShadowNotice(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := shadowNotice
	shadowNotice = &buf
	t.Cleanup(func() { shadowNotice = prev })
	return &buf
}

// TestAmbiguousID_IsLoudEverywhere is the anti-silent-wrong-answer test: every
// command that resolves an ID must refuse rather than guess between two
// candidates of the SAME terminality. (Liveness breaks the tie when the
// candidates differ — that is TestResolveUnique_LivePrecedence.)
func TestAmbiguousID_IsLoudEverywhere(t *testing.T) {
	id := "mg-c2af"

	// Each case seeds the copy the command operates on plus a second copy of
	// equal terminality, then runs the command that would previously have taken
	// the first filesystem hit. Live cases are shadowed by another live copy;
	// the terminal (done) case is shadowed by an archived one.
	cases := []struct {
		name   string
		dir    string // directory holding the copy the command operates on
		file   string
		shadow string // directory holding the equally-terminal twin
		run    func(root string) error
	}{
		{"read", "available", id + ".md", "pending", func(root string) error { _, err := Read(root, id); return err }},
		{"status", "available", id + ".md", "pending", func(root string) error { _, err := Status(root, id); return err }},
		{"findpath", "available", id + ".md", "pending", func(root string) error { _, _, err := FindPath(root, id); return err }},
		{"claim", "available", id + ".md", "pending", func(root string) error { _, err := Claim(root, id, 1); return err }},
		{"shelve", "available", id + ".md", "pending", func(root string) error { _, err := Shelve(root, id); return err }},
		{"edit", "available", id + ".md", "pending", func(root string) error {
			typ := "bug"
			_, err := Update(root, id, UpdateField{Type: &typ})
			return err
		}},
		{"unclaim", "claimed", id + ".md.991", "pending", func(root string) error { _, err := Unclaim(root, id); return err }},
		{"done", "claimed", id + ".md.991", "pending", func(root string) error { _, _, err := Done(root, id, nil); return err }},
		{"reopen", "done", id + ".md", filepath.Join("archive", "2026-05"), func(root string) error { _, err := Reopen(root, id); return err }},
		{"unshelve", "shelved", id + ".md", "pending", func(root string) error { _, err := Unshelve(root, id); return err }},
		{"unarchive", "available", id + ".md", "pending", func(root string) error { _, err := Unarchive(root, id); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			writeAt(t, root, tc.dir, tc.file)
			writeAt(t, root, tc.shadow, id+".md")

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
			for _, want := range []string{filepath.Join("work", tc.shadow, id+".md"), filepath.Join("work", tc.dir, tc.file)} {
				if !strings.Contains(me.Message, want) {
					t.Errorf("message does not name candidate %q:\n%s", want, me.Message)
				}
			}
		})
	}
}

// TestResolveUnique_LivePrecedence pins the architect's ruling on mg-4fa7: a
// live work item and an archived record are not two equally plausible answers
// to the same question. Exactly one live candidate wins — loudly.
func TestResolveUnique_LivePrecedence(t *testing.T) {
	id := "mg-4fa7"

	for _, live := range []string{"available", "claimed", "pending", "shelved"} {
		t.Run("live="+live, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			file := id + ".md"
			if live == "claimed" {
				file = id + ".md.991"
			}
			writeAt(t, root, live, file)
			writeAt(t, root, filepath.Join("archive", "2026-04"), id+".md")
			notice := captureShadowNotice(t)

			m, err := ResolveUnique(root, id)
			if err != nil {
				t.Fatalf("ResolveUnique should prefer the live %s item: %v", live, err)
			}
			if m.Status != live {
				t.Errorf("resolved to status %q, want %q", m.Status, live)
			}
			if want := filepath.Join(root, "work", live, file); m.Path != want {
				t.Errorf("path = %q, want %q", m.Path, want)
			}

			// Resolve, but never silently: the note must name the archived
			// twin's exact path so an auditor can go read it.
			got := notice.String()
			if wantPath := filepath.Join("work", "archive", "2026-04", id+".md"); !strings.Contains(got, wantPath) {
				t.Errorf("shadow note does not name the archived path %q:\n%q", wantPath, got)
			}
			if !strings.Contains(got, id) || !strings.Contains(got, "archived") {
				t.Errorf("shadow note should name the id and the shadowed status: %q", got)
			}
		})
	}
}

// TestResolveUnique_DoneIsTerminalNotLive guards the terminality classification:
// a done item is a historical record, so done-vs-archived is a real ambiguity.
func TestResolveUnique_DoneIsTerminalNotLive(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-c2af"
	writeAt(t, root, "done", id+".md")
	writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")
	captureShadowNotice(t)

	if _, err := ResolveUnique(root, id); err == nil {
		t.Fatal("done + archived are both terminal — this must stay ambiguous")
	} else if code := mgerrOf(t, err).Code; code != "ambiguous_id" {
		t.Errorf("code = %q, want ambiguous_id", code)
	}
}

// TestResolveUnique_TwoLiveIsAmbiguous: liveness breaks a live-vs-terminal tie,
// never a live-vs-live one. (The O_EXCL mint means there are zero such cases
// today and there will never be another; this is the backstop.)
func TestResolveUnique_TwoLiveIsAmbiguous(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-c2af"
	writeAt(t, root, "available", id+".md")
	writeAt(t, root, "shelved", id+".md")
	notice := captureShadowNotice(t)

	_, err := ResolveUnique(root, id)
	if err == nil {
		t.Fatal("two live candidates must not resolve")
	}
	me := mgerrOf(t, err)
	if me.Code != "ambiguous_id" {
		t.Fatalf("code = %q, want ambiguous_id", me.Code)
	}
	for _, want := range []string{
		filepath.Join("work", "available", id+".md"),
		filepath.Join("work", "shelved", id+".md"),
	} {
		if !strings.Contains(me.Message, want) {
			t.Errorf("message does not name candidate %q:\n%s", want, me.Message)
		}
	}
	if notice.String() != "" {
		t.Errorf("an ambiguity must not also emit a shadow note: %q", notice)
	}
}

// TestResolveUnique_ZeroLiveTwoArchived is the eleven-collision case: no live
// candidate, so there is nothing to prefer. Error, naming both paths.
func TestResolveUnique_ZeroLiveTwoArchived(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-c2af"
	writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")
	writeAt(t, root, filepath.Join("archive", "2026-07"), id+".md")
	notice := captureShadowNotice(t)

	_, err := ResolveUnique(root, id)
	if err == nil {
		t.Fatal("two archived candidates must not resolve")
	}
	me := mgerrOf(t, err)
	if me.Code != "ambiguous_id" {
		t.Fatalf("code = %q, want ambiguous_id", me.Code)
	}
	for _, want := range []string{
		filepath.Join("work", "archive", "2026-05", id+".md"),
		filepath.Join("work", "archive", "2026-07", id+".md"),
	} {
		if !strings.Contains(me.Message, want) {
			t.Errorf("message does not name candidate %q:\n%s", want, me.Message)
		}
	}
	if notice.String() != "" {
		t.Errorf("an ambiguity must not also emit a shadow note: %q", notice)
	}
}

// TestResolveUnique_SingleCandidateIsSilent: the note is a shadow warning, not
// a resolve trace. The overwhelmingly common path must stay quiet.
func TestResolveUnique_SingleCandidateIsSilent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-c2af"
	writeAt(t, root, "available", id+".md")
	notice := captureShadowNotice(t)

	if _, err := ResolveUnique(root, id); err != nil {
		t.Fatalf("ResolveUnique: %v", err)
	}
	if notice.String() != "" {
		t.Errorf("unshadowed resolve emitted stderr noise: %q", notice)
	}
}

// TestResolveUnique_ShadowedResolveWorksEverywhere is the acceptance test in
// library form: the commands that mg-4fa7 needs must go through, not error.
func TestResolveUnique_ShadowedResolveWorksEverywhere(t *testing.T) {
	id := "mg-4fa7"
	cases := []struct {
		name string
		dir  string
		file string
		run  func(root string) error
	}{
		{"read", "available", id + ".md", func(root string) error { _, err := Read(root, id); return err }},
		{"status", "available", id + ".md", func(root string) error { _, err := Status(root, id); return err }},
		{"claim", "available", id + ".md", func(root string) error { _, err := Claim(root, id, 1); return err }},
		{"done", "claimed", id + ".md.991", func(root string) error { _, _, err := Done(root, id, nil); return err }},
		{"unclaim", "claimed", id + ".md.991", func(root string) error { _, err := Unclaim(root, id); return err }},
		{"unshelve", "shelved", id + ".md", func(root string) error { _, err := Unshelve(root, id); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)
			writeAt(t, root, tc.dir, tc.file)
			writeAt(t, root, filepath.Join("archive", "2026-04"), id+".md")
			notice := captureShadowNotice(t)

			if err := tc.run(root); err != nil {
				t.Fatalf("%s on a live item shadowed by an archived twin: %v", tc.name, err)
			}
			if !strings.Contains(notice.String(), filepath.Join("archive", "2026-04", id+".md")) {
				t.Errorf("%s resolved the shadow silently: %q", tc.name, notice)
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

// TestResolveUnique_PartitionQualifier is the mg-0d0c acceptance test in library
// form and its own positive control. Two archived twins of one id live in
// different partitions; both are terminal, so liveness cannot break the tie and
// the bare id is unresolvable (that is correct). The @partition qualifier is the
// escape hatch: it must select the twin in the named partition — and prove it
// can FAIL, so a wrong partition errors rather than silently returning a twin.
func TestResolveUnique_PartitionQualifier(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-4fa7"
	writeAt(t, root, filepath.Join("archive", "2026-04"), id+".md")
	writeAt(t, root, filepath.Join("archive", "2026-07"), id+".md")

	// Bare id: ambiguous. The qualifier is the ONLY way to disambiguate.
	if _, err := ResolveUnique(root, id); err == nil {
		t.Fatal("bare id with two archived twins must stay ambiguous")
	} else if code := mgerrOf(t, err).Code; code != "ambiguous_id" {
		t.Fatalf("bare id: code = %q, want ambiguous_id", code)
	}

	// Each qualifier resolves to its OWN twin — not the same one twice.
	for _, part := range []string{"2026-04", "2026-07"} {
		m, err := ResolveUnique(root, id+"@"+part)
		if err != nil {
			t.Fatalf("%s@%s: %v", id, part, err)
		}
		if m.Status != "archived" || m.Partition != part {
			t.Errorf("%s@%s resolved to (%q, partition=%q), want (archived, %q)", id, part, m.Status, m.Partition, part)
		}
		if want := filepath.Join(root, "work", "archive", part, id+".md"); m.Path != want {
			t.Errorf("%s@%s path = %q, want %q", id, part, m.Path, want)
		}
	}

	// The two qualifiers must reach DISTINCT files, or the disambiguator is a
	// no-op that returns whatever the resolver found first.
	a, _ := ResolveUnique(root, id+"@2026-04")
	b, _ := ResolveUnique(root, id+"@2026-07")
	if a.Path == b.Path {
		t.Fatalf("both qualifiers resolved to the same file %q — @partition did not disambiguate", a.Path)
	}

	// The control CAN fail: a partition with no twin must error (not_found),
	// naming the partitions where the id actually lives — never a wrong twin.
	_, err := ResolveUnique(root, id+"@2099-99")
	if err == nil {
		t.Fatal("a partition with no such twin must error, not silently return a twin")
	}
	me := mgerrOf(t, err)
	if me.Code != "no_such_partition" {
		t.Errorf("wrong-partition code = %q, want no_such_partition", me.Code)
	}
	if me.Category != mgerr.CatNotFound {
		t.Errorf("wrong-partition category = %v, want not_found", me.Category)
	}
	for _, part := range []string{"2026-04", "2026-07"} {
		if !strings.Contains(me.Hint, part) {
			t.Errorf("no_such_partition hint should list available partition %q: %q", part, me.Hint)
		}
	}
}

// TestResolveUnique_PartitionQualifierRegressions covers the qualifier on the
// ordinary (non-duplicated) store: it must not break the common case, and a
// bare id with no duplicate must still resolve.
func TestResolveUnique_PartitionQualifierRegressions(t *testing.T) {
	t.Run("qualifier on a non-duplicated archived id", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		id := "mg-65af"
		writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")

		m, err := ResolveUnique(root, id+"@2026-05")
		if err != nil {
			t.Fatalf("@partition on a lone archived twin: %v", err)
		}
		if m.Partition != "2026-05" {
			t.Errorf("partition = %q, want 2026-05", m.Partition)
		}
		// Bare id (no duplicate) still resolves, unqualified.
		if _, err := ResolveUnique(root, id); err != nil {
			t.Errorf("bare non-duplicated id: %v", err)
		}
	})

	t.Run("qualifier naming the wrong partition of a lone id", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		id := "mg-65af"
		writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")

		if _, err := ResolveUnique(root, id+"@2026-06"); err == nil {
			t.Fatal("wrong partition must error even when the id is not duplicated")
		} else if code := mgerrOf(t, err).Code; code != "no_such_partition" {
			t.Errorf("code = %q, want no_such_partition", code)
		}
	})

	t.Run("qualifier on a wholly unknown id is not_found", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		if _, err := ResolveUnique(root, "mg-dead@2026-05"); err == nil {
			t.Fatal("unknown id with a qualifier must error")
		} else if code := mgerrOf(t, err).Code; code != "no_such_item" {
			t.Errorf("code = %q, want no_such_item", code)
		}
	})

	t.Run("empty partition after @ is a usage error", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		id := "mg-65af"
		writeAt(t, root, filepath.Join("archive", "2026-05"), id+".md")

		_, err := ResolveUnique(root, id+"@")
		if err == nil {
			t.Fatal("an empty qualifier must not silently resolve")
		}
		me := mgerrOf(t, err)
		if me.Code != "empty_partition" {
			t.Errorf("code = %q, want empty_partition", me.Code)
		}
		if me.Category != mgerr.CatUsage {
			t.Errorf("category = %v, want usage", me.Category)
		}
	})

	t.Run("a live id with no @ is unaffected", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		id := "mg-65af"
		writeAt(t, root, "available", id+".md")
		if m, err := ResolveUnique(root, id); err != nil || m.Status != "available" {
			t.Errorf("bare live id regressed: (%+v, %v)", m, err)
		}
	})
}

// TestAmbiguousID_NamesPartitionEscapeHatch pins the acceptance requirement that
// the bare-id ambiguity error advertises the @partition form, so an auditor who
// hits exit-4 learns the escape hatch at the exact moment it is needed.
func TestAmbiguousID_NamesPartitionEscapeHatch(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id := "mg-c2af"
	writeAt(t, root, filepath.Join("archive", "2026-04"), id+".md")
	writeAt(t, root, filepath.Join("archive", "2026-07"), id+".md")

	_, err := ResolveUnique(root, id)
	if err == nil {
		t.Fatal("two archived twins must be ambiguous")
	}
	me := mgerrOf(t, err)
	if !strings.Contains(me.Hint, "@") || !strings.Contains(me.Hint, id) {
		t.Errorf("ambiguity hint should name the %s@<partition> escape hatch: %q", id, me.Hint)
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

	// An archived twin does not make the id ambiguous — the live item wins,
	// with a note on stderr.
	writeAt(t, root, filepath.Join("archive", "2026-05"), created.ID+".md")
	notice := captureShadowNotice(t)
	item, status, err = ReadWithStatus(root, created.ID)
	if err != nil || status != "available" {
		t.Fatalf("shadowed by an archived twin: got (%v, %q), want the live item", err, status)
	}
	if notice.String() == "" {
		t.Error("ReadWithStatus resolved a shadowed id silently")
	}

	// A second LIVE copy is a real ambiguity, and is caught here too, so `show`
	// can never render one item's body under another item's status.
	writeAt(t, root, "pending", created.ID+".md")
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
