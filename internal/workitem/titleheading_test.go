package workitem

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// The invariant under test, asserted rather than spelled out as a literal:
//
//	Parse(Render(item)).Title == item.Title, and the title's heading is the
//	FIRST heading in the stored body.
//
// It is stated as a round trip on purpose. A test that pinned a heading count
// or a specific title would decay on the next legitimate change to either side
// of the coupling; a round trip stays true for any title and any body, which is
// the property the two contradictory 2026-07 measurements each half-observed
// (mg-bac6). Every case below feeds this one assertion.
func assertTitleRoundTrips(t *testing.T, item *Item) {
	t.Helper()
	rendered := Render(item)
	back, err := Parse(rendered)
	if err != nil {
		t.Fatalf("Parse(Render(item)): %v\nrendered:\n%s", err, rendered)
	}
	if back.Title != item.Title {
		t.Errorf("title did not round-trip:\n  set    %q\n  read   %q\nstored body:\n%s",
			item.Title, back.Title, back.Body)
	}
	// The title's heading must be the first one, since that is the only one a
	// read can ever return.
	idx, heading, found := firstHeadingLine(back.Body)
	if !found {
		t.Errorf("stored body carries no '# ' heading at all; nothing could derive a title\nbody:\n%s", back.Body)
		return
	}
	if heading != item.Title {
		t.Errorf("first heading (line %d) is %q, want the title %q", idx, heading, item.Title)
	}
}

// TestTitleRoundTripsForEveryBodyShape runs the shape matrix from mg-bac6
// through the round-trip invariant. These are the four shapes the two probes
// disagreed about, and the point is that the answer is now the same for all of
// them: the title survives, and it is the first heading.
func TestTitleRoundTripsForEveryBodyShape(t *testing.T) {
	const title = "the stored title"
	cases := []struct {
		name string
		body string
	}{
		{"no heading at all", "just prose, no heading\n"},
		{"empty body", ""},
		{"heading already says the title", "# " + title + "\n\nprose\n"},
		{"heading says something else", "# a different heading\n\nprose\n"},
		{"second heading below a matching first", "# " + title + "\n\nprose\n\n# a later section\n"},
		{"second heading below a differing first", "# a different heading\n\nprose\n\n# " + title + "\n"},
		{"blockquoted heading above a matching one", "> # a quoted heading\n\n# " + title + "\n\nprose\n"},
		{"blockquoted heading only", "> # a quoted heading\n\nprose\n"},
		{"title appears mid-line, not as a heading", "see # " + title + " for context\n"},
		{"heading differing by one character", "# " + title + ",\n\nprose\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := &Item{ID: "mg-0001", Title: title, Body: tc.body}
			assertTitleRoundTrips(t, item)
		})
	}
}

// TestComposeBodyNeverStacksATitleHeading is the structural half of the fix: the
// duplicate-H1 state has nowhere to live, rather than being detected after the
// fact.
//
// The old rule prepended "# "+title above the whole body whenever
// strings.Contains(body, "# "+title) was false. A heading reworded by ONE
// character was therefore enough to produce two near-identical H1s — the shape
// found in mg-c8d5 and in 196 of 2009 stored bodies. There is now no input that
// makes mg author a second heading of its own.
func TestComposeBodyNeverStacksATitleHeading(t *testing.T) {
	const title = "pogo#92 follow-up CODE HALF: the docs claim the sidecar records pr_flow"
	// The real specimen's delta: one comma.
	reworded := "# " + title[:16] + "," + title[16:] + "\n\nprose\n"

	item := &Item{ID: "mg-c8d5", Title: title, Body: reworded}
	stored := composeBody(item)

	if n := countHeadings(stored); n != 1 {
		t.Errorf("composeBody produced %d headings, want 1 — a near-duplicate was stacked:\n%s", n, stored)
	}
	if _, heading, _ := firstHeadingLine(stored); heading != title {
		t.Errorf("first heading is %q, want %q", heading, title)
	}
	assertTitleRoundTrips(t, item)
}

// TestComposeBodyIsIdempotent matters because Update normalises the in-memory
// body through composeBody and Render calls it again on the way to disk. If it
// were not idempotent, every write would reshape the body a little more.
func TestComposeBodyIsIdempotent(t *testing.T) {
	for _, body := range []string{"", "prose only\n", "# t\n\nprose\n", "# other\n\nprose\n", "> # quoted\n\nprose\n"} {
		item := &Item{ID: "mg-0001", Title: "t", Body: body}
		once := composeBody(item)
		twice := composeBody(&Item{ID: "mg-0001", Title: "t", Body: once})
		if once != twice {
			t.Errorf("composeBody not idempotent for %q:\n once: %q\n twice: %q", body, once, twice)
		}
	}
}

// TestComposeBodyPreservesSubstanceByteForByte. Both 2026-07 observations agreed
// that prose survived and only the heading region moved; that is worth pinning,
// because the in-place heading rewrite is the part of this change that touches
// caller bytes.
func TestComposeBodyPreservesSubstanceByteForByte(t *testing.T) {
	const substance = "A paragraph worth keeping.\n\n## A section\n\nMore prose with `backticks` and $VARS.\n"
	item := &Item{ID: "mg-0001", Title: "the title", Body: "# a different heading\n\n" + substance}
	stored := composeBody(item)
	if !strings.Contains(stored, substance) {
		t.Errorf("substance was not preserved byte-for-byte:\n%s", stored)
	}
	if !strings.HasPrefix(stored, "# the title\n") {
		t.Errorf("heading was not rewritten in place; got:\n%s", stored)
	}
}

// TestBlockquotedHeadingCannotBecomeTheTitle settles a claim that had been
// carried in agent memory as UNVERIFIED-BUT-LOAD-BEARING: that blockquoting a
// prepended "> # heading" left titles intact. It does, and for a reason rather
// than by luck — Parse matches HasPrefix(line, "# ") on the raw line, so an
// indented or quoted heading is not a heading to it. firstHeadingLine applies
// the same rule, which is what keeps the write side and the read side from
// drifting apart again.
func TestBlockquotedHeadingCannotBecomeTheTitle(t *testing.T) {
	const title = "the stored title"
	item := &Item{ID: "mg-0001", Title: title, Body: "> # a quoted heading\n\n# " + title + "\n\nprose\n"}
	assertTitleRoundTrips(t, item)

	if _, _, found := firstHeadingLine("> # a quoted heading\n"); found {
		t.Error("a blockquoted heading was treated as a heading; Parse would disagree")
	}
}

// TestDetectTitleSideEffect covers the guard's trigger surface directly: which
// bodies rename an item and which do not.
func TestDetectTitleSideEffect(t *testing.T) {
	const title = "the stored title"
	cases := []struct {
		name       string
		body       string
		wantMoved  bool
		wantTarget string
	}{
		{"no heading — title is synthesised, nothing moves", "prose\n", false, ""},
		{"heading matches — nothing moves", "# " + title + "\n\nprose\n", false, ""},
		{"heading replaced — the item is renamed", "# something else\n\nprose\n", true, "something else"},
		{"heading prepended above the old one — the item is renamed", "# prepended\n\n# " + title + "\n", true, "prepended"},
		{"heading reworded by one comma — the item is renamed", "# " + title + ",\n", true, title + ","},
		{"blockquoted heading — invisible to the read, nothing moves", "> # quoted\n\n# " + title + "\n", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			se, moved := detectTitleSideEffect(title, tc.body)
			if moved != tc.wantMoved {
				t.Fatalf("moved=%v, want %v (se=%+v)", moved, tc.wantMoved, se)
			}
			if moved && se.To != tc.wantTarget {
				t.Errorf("would retitle to %q, want %q", se.To, tc.wantTarget)
			}
			if moved && se.From != title {
				t.Errorf("From=%q, want %q", se.From, title)
			}
		})
	}
}

// TestUpdate_RefusesToAddADuplicateTitleHeading covers the second door into the
// corrupted state: --title naming a title the supplied body ALREADY carries
// below its first heading, so the in-place rewrite would author the duplicate.
func TestUpdate_RefusesToAddADuplicateTitleHeading(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	title := "the defended title"
	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A prepend that leaves the old heading in place below it, with --title
	// naming the title being defended — the 2026-07-21 arm.
	body := "# a prepended section\n\nnotes\n\n# " + title + "\n\nprose\n"
	_, err = Update(root, item.ID, UpdateField{Title: &title, Body: &body})
	if err == nil {
		t.Fatal("Update accepted an edit that would leave two headings saying the title")
	}
	var mge *mgerr.Error
	if !errors.As(err, &mge) || mge.Code != "duplicate_title_heading" {
		t.Fatalf("error = %v, want a duplicate_title_heading conflict", err)
	}

	// The refusal left the item alone.
	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if after.Title != title {
		t.Errorf("title moved to %q", after.Title)
	}
	if n := countTitleHeadings(after.Body, title); n != 1 {
		t.Errorf("stored body carries the title %d times, want 1", n)
	}
}

// TestUpdate_DoesNotPunishAPreexistingDuplicate. 196 stored bodies already carry
// a stacked title from before this fix. An unrelated metadata edit on one of them
// must still work — mg does not get to refuse work because of damage it did
// earlier, and refusing would strand those items rather than fix them.
func TestUpdate_DoesNotPunishAPreexistingDuplicate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	title := "an already corrupted title"
	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Reproduce the stored corruption directly on disk, as the old code would
	// have left it: the title heading twice.
	path, _, err := FindPath(root, item.ID)
	if err != nil {
		t.Fatalf("findpath: %v", err)
	}
	corrupt, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	stacked := strings.Replace(string(corrupt), "# "+title, "# "+title+"\n# "+title, 1)
	if err := os.WriteFile(path, []byte(stacked), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	assignee := "someone"
	if _, err := Update(root, item.ID, UpdateField{Assignee: &assignee}); err != nil {
		t.Fatalf("a metadata edit on a pre-existing duplicate was refused: %v", err)
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if after.Assignee != assignee {
		t.Errorf("assignee = %q, want %q", after.Assignee, assignee)
	}
	// Left exactly as found — not silently repaired.
	if n := countTitleHeadings(after.Body, title); n != 2 {
		t.Errorf("pre-existing duplicate count = %d, want it preserved at 2", n)
	}
}

// TestErrSilentRetitleIsAConflictNamingBothRemedies. The error is the whole
// deliverable for a caller who hits it, so its category and its hint are part of
// the contract: exit 4 (not retryable unchanged), and both directions offered,
// because each direction of this coupling was reported as "the" bug by an agent
// who had measured only their own.
func TestErrSilentRetitleIsAConflictNamingBothRemedies(t *testing.T) {
	err := errSilentRetitle("mg-0001", titleSideEffect{From: "old", To: "new"})
	if err.Category != mgerr.CatConflict {
		t.Errorf("category = %v, want conflict", err.Category)
	}
	if err.Retryable {
		t.Error("retryable = true; re-running the identical command can never succeed")
	}
	if !strings.Contains(err.Message, "old") || !strings.Contains(err.Message, "new") {
		t.Errorf("message names neither title: %q", err.Message)
	}
	if !strings.Contains(err.Hint, "--title") {
		t.Errorf("hint does not offer the rename remedy: %q", err.Hint)
	}
	if !strings.Contains(err.Hint, "no leading") {
		t.Errorf("hint does not offer the keep-the-title remedy: %q", err.Hint)
	}
}
