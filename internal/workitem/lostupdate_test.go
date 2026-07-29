package workitem

import (
	"errors"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// This file is the regression suite for mg-f326: `mg edit --body`/`--body-file`
// was a read-modify-write with no version check, so a writer working from a
// stale read destroyed whatever landed in between and was told "Updated" with
// exit 0. Three agents did this to each other in two hours.
//
// The load-bearing tests here are the INTERLEAVING ones. A test that only
// asserted the happy path would prove nothing about a control whose entire job
// is to fire on the unhappy one, so each guarded case is paired with an
// unguarded control that performs the same interleaving and asserts the clobber
// still happens. If the guard silently stopped guarding, the guarded case goes
// red; if the interleaving stopped being a hazard at all, the control goes red
// and tells us these tests are no longer exercising anything.

// bodyOf re-reads an item's stored body from disk. Every assertion below goes
// through disk rather than through a returned *Item, because "the write exited
// 0 and returned a plausible item" is exactly the reassurance this ticket
// exists to distrust.
func bodyOf(t *testing.T, root, id string) string {
	t.Helper()
	item, err := Read(root, id)
	if err != nil {
		t.Fatalf("Read %s: %v", id, err)
	}
	return item.Body
}

// seedItem creates an item whose stored body is a known multi-section document,
// and returns its ID plus the body hash a reader would have captured.
func seedItem(t *testing.T, root, title, body string) (id, hash string) {
	t.Helper()
	item, err := Create(root, "mg-", "task", title, nil, WithBody(body))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return item.ID, BodyHash(bodyOf(t, root, item.ID))
}

// TestUpdate_IfUnchanged_RefusesLostUpdate is the ticket's acceptance criterion.
// It performs the exact interleaving from incident 1 — A reads, B writes, A
// writes — and asserts A's write is REFUSED, not merged, not accepted.
func TestUpdate_IfUnchanged_RefusesLostUpdate(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := "\n# Contested\n\n## original\n\nthe body as both agents first read it.\n"
	id, hashA := seedItem(t, root, "Contested", base)

	// --- A reads. hashA is the version A is holding. ---

	// --- B writes a reconciliation, working from the same read. B is
	//     unguarded, exactly like every writer before this change. ---
	bBody := base + "\n## B's reconciliation\n\neighty-five lines of analysis.\n"
	if _, err := Update(root, id, UpdateField{Body: &bBody}); err != nil {
		t.Fatalf("B's write: %v", err)
	}

	// --- A now writes a full body composed from the read that predates B. ---
	aBody := base + "\n## A's section\n\nA never saw the other agent's work.\n"
	_, err := Update(root, id, UpdateField{Body: &aBody, IfUnchanged: hashA})

	if err == nil {
		t.Fatal("A's stale full-body write was ACCEPTED — the lost-update guard did not fire")
	}

	// The failure must be loud in the machine-readable sense too: exit 4, a
	// stable slug, and not retryable (retrying the same stale hash can never
	// succeed, and a caller that retried on it would spin).
	var mgErr *mgerr.Error
	if !errors.As(err, &mgErr) {
		t.Fatalf("refusal must be a typed mgerr.Error, got %T: %v", err, err)
	}
	if mgErr.Code != "body_changed" {
		t.Errorf("Code = %q, want %q", mgErr.Code, "body_changed")
	}
	if got := mgErr.Category.ExitCode(); got != 4 {
		t.Errorf("exit code = %d, want 4 (conflict)", got)
	}
	if mgErr.Retryable {
		t.Error("a stale-hash refusal must not be marked retryable: the identical retry can never succeed")
	}

	// The failure must name what it can observe about the change.
	msg := mgErr.Message
	for _, want := range []string{id, hashA, BodyHash(bodyOf(t, root, id)), "lines"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message must mention %q, got:\n%s", want, msg)
		}
	}

	// And the whole point: B's work is still on disk, untouched.
	stored := bodyOf(t, root, id)
	if !strings.Contains(stored, "B's reconciliation") {
		t.Errorf("B's work was destroyed by the refused write:\n%s", stored)
	}
	if strings.Contains(stored, "A's section") {
		t.Errorf("the refused write was partially applied:\n%s", stored)
	}
}

// TestUpdate_UnguardedFullBodyWrite_StillClobbers is the CONTROL that gives the
// test above its teeth. It runs the identical interleaving with no
// --if-unchanged and asserts the clobber DOES happen.
//
// Two things depend on this:
//
//  1. It proves the interleaving is a real hazard, so the guarded test passes
//     because the guard fired — not because the scenario was inert.
//  2. It pins the deliberate compatibility decision. mg self-installs on merge
//     across the whole fleet, and this is the most-used write path in the
//     fleet's own tooling, so a bare --body-file keeps its historical
//     behaviour. If someone later makes the check default-on, this test goes
//     red and forces that to be a decision rather than a side effect.
func TestUpdate_UnguardedFullBodyWrite_StillClobbers(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := "\n# Contested\n\n## original\n\nthe body as both agents first read it.\n"
	id, _ := seedItem(t, root, "Contested", base)

	bBody := base + "\n## B's reconciliation\n\neighty-five lines of analysis.\n"
	if _, err := Update(root, id, UpdateField{Body: &bBody}); err != nil {
		t.Fatalf("B's write: %v", err)
	}

	aBody := base + "\n## A's section\n\nA never saw the other agent's work.\n"
	if _, err := Update(root, id, UpdateField{Body: &aBody}); err != nil {
		t.Fatalf("A's unguarded write should still succeed: %v", err)
	}

	stored := bodyOf(t, root, id)
	if strings.Contains(stored, "B's reconciliation") {
		t.Error("unguarded --body no longer clobbers: either it became guarded by default " +
			"(a fleet-wide behaviour change that must be deliberate) or this test stopped " +
			"exercising the interleaving")
	}
}

// TestUpdate_IfUnchanged_TitleClobber covers incident 2, where an agent's
// retitle was overwritten seconds later by a writer holding an older read. The
// title lives in the body's "# heading" line, so the body hash moves when the
// title moves and the same guard catches it.
func TestUpdate_IfUnchanged_TitleClobber(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id, hashA := seedItem(t, root, "Original", "\n# Original\n\nshared body.\n")

	newTitle := "SUPERSEDED by mg-6c4b"
	if _, err := Update(root, id, UpdateField{Title: &newTitle}); err != nil {
		t.Fatalf("B's retitle: %v", err)
	}

	aTitle := "A's title, composed before B retitled"
	_, err := Update(root, id, UpdateField{Title: &aTitle, IfUnchanged: hashA})
	if err == nil {
		t.Fatal("a stale-read retitle was ACCEPTED; the body hash must cover the # heading line")
	}

	if got := bodyOf(t, root, id); !strings.Contains(got, newTitle) {
		t.Errorf("B's title was destroyed: %q", got)
	}
}

// TestUpdate_IfUnchanged_PassesWhenNothingIntervened pins that the guard is not
// simply always-refusing. Paired with the interleaving test above, this is what
// makes --if-unchanged usable rather than merely loud.
func TestUpdate_IfUnchanged_PassesWhenNothingIntervened(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id, hash := seedItem(t, root, "Quiet", "\n# Quiet\n\nnobody else is editing.\n")

	newBody := "\n# Quiet\n\nrewritten in peace.\n"
	if _, err := Update(root, id, UpdateField{Body: &newBody, IfUnchanged: hash}); err != nil {
		t.Fatalf("guarded write over an unchanged body must succeed: %v", err)
	}
	if got := bodyOf(t, root, id); !strings.Contains(got, "rewritten in peace") {
		t.Errorf("guarded write did not land: %q", got)
	}
}

// TestUpdate_IfUnchanged_AcceptsPrefix pins that a shortened hash works, since a
// 64-character value is what makes people paste from the wrong buffer.
func TestUpdate_IfUnchanged_AcceptsPrefix(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id, hash := seedItem(t, root, "Prefix", "\n# Prefix\n\nbody.\n")

	newBody := "\n# Prefix\n\nchanged.\n"
	if _, err := Update(root, id, UpdateField{Body: &newBody, IfUnchanged: hash[:minHashPrefix]}); err != nil {
		t.Fatalf("an %d-char prefix must be accepted: %v", minHashPrefix, err)
	}
	if got := bodyOf(t, root, id); !strings.Contains(got, "changed") {
		t.Errorf("prefix-guarded write did not land: %q", got)
	}
}

// TestUpdate_IfUnchanged_RejectsUnusableValue pins that a value which is not a
// body hash is a usage error rather than a guard that quietly never matches (a
// wedged writer) or quietly always matches (no guard at all).
func TestUpdate_IfUnchanged_RejectsUnusableValue(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id, hash := seedItem(t, root, "Bad hash", "\n# Bad hash\n\nbody.\n")

	cases := []struct {
		name  string
		value string
	}{
		{"too short to be unambiguous", hash[:minHashPrefix-1]},
		{"not hex", strings.Repeat("z", 16)},
		{"longer than a sha256", hash + "00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newBody := "\n# Bad hash\n\nrewritten.\n"
			_, err := Update(root, id, UpdateField{Body: &newBody, IfUnchanged: tc.value})
			if err == nil {
				t.Fatalf("--if-unchanged=%q was accepted", tc.value)
			}
			var mgErr *mgerr.Error
			if !errors.As(err, &mgErr) {
				t.Fatalf("want typed error, got %T: %v", err, err)
			}
			if got := mgErr.Category.ExitCode(); got != 2 {
				t.Errorf("exit code = %d, want 2 (usage)", got)
			}
			if got := bodyOf(t, root, id); strings.Contains(got, "rewritten") {
				t.Error("a rejected --if-unchanged value must not write anything")
			}
		})
	}
}

// TestUpdate_AppendBody_CannotClobber is the other half of the fix, and the one
// that would have prevented all three incidents with no coordination at all:
// every collision that night was two agents appending separate dated sections
// that had no reason to conflict.
//
// The interleaving is the same as the lost-update test — A reads, B writes, A
// writes — except A appends. Both sections must survive.
func TestUpdate_AppendBody_CannotClobber(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	base := "\n# Shared\n\n## original\n\nthe body as both agents first read it.\n"
	id, _ := seedItem(t, root, "Shared", base)

	// B appends first, from the shared read.
	bSection := "## B's reconciliation\n\neighty-five lines of analysis.\n"
	if _, err := Update(root, id, UpdateField{AppendBody: &bSection}); err != nil {
		t.Fatalf("B's append: %v", err)
	}

	// A appends from a read that predates B's write and never saw it.
	aSection := "## A's section\n\nwritten without ever seeing the other agent.\n"
	if _, err := Update(root, id, UpdateField{AppendBody: &aSection}); err != nil {
		t.Fatalf("A's append: %v", err)
	}

	stored := bodyOf(t, root, id)
	for _, want := range []string{"## original", "B's reconciliation", "A's section"} {
		if !strings.Contains(stored, want) {
			t.Errorf("append lost %q from the body:\n%s", want, stored)
		}
	}
	// Order matters for a running log: later appends go after earlier ones.
	if strings.Index(stored, "A's section") < strings.Index(stored, "B's reconciliation") {
		t.Errorf("appends must accumulate in write order:\n%s", stored)
	}
}

// TestAppendToBody pins the join rule, which is the only part of an append that
// is not verbatim. Markdown is why: a "## heading" placed directly under the
// previous line is paragraph text, not a heading.
func TestAppendToBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		text string
		want string
	}{
		{"trailing newline", "a\n", "b\n", "a\n\nb\n"},
		{"no trailing newline", "a", "b\n", "a\n\nb\n"},
		{"run of trailing newlines collapses", "a\n\n\n\n", "b\n", "a\n\nb\n"},
		{"empty body", "", "b\n", "b\n"},
		{"blank body", "\n\n", "b\n", "b\n"},
		{"appended text is verbatim", "a\n", "  b\n\n\nc  ", "a\n\n  b\n\n\nc  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := appendToBody(tc.body, tc.text); got != tc.want {
				t.Errorf("appendToBody(%q, %q) = %q, want %q", tc.body, tc.text, got, tc.want)
			}
		})
	}
}

// TestUpdate_TitleAloneIsBodySafe pins the one claim mg-f326 recorded as
// already-safe: --title alone rewrites the "# heading" line and nothing else.
// It is currently the sole edit two agents can make to a live item without
// coordinating, so it is worth a test rather than being left to be
// rediscovered — and if it ever stops being true, this is where that shows up.
func TestUpdate_TitleAloneIsBodySafe(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	body := "\n# Original title\n\n## section one\n\ncontent with `backticks` and $VARS.\n\n" +
		"## section two\n\nmore content, 2026-07-29.\n"
	id, _ := seedItem(t, root, "Original title", body)
	before := bodyOf(t, root, id)

	newTitle := "Retitled, body untouched"
	if _, err := Update(root, id, UpdateField{Title: &newTitle}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after := bodyOf(t, root, id)

	if !strings.Contains(after, "# "+newTitle) {
		t.Errorf("heading was not rewritten:\n%s", after)
	}
	// Everything that is not the heading line must be byte-identical.
	strip := func(s, title string) string {
		return strings.Replace(s, "# "+title+"\n", "", 1)
	}
	if got, want := strip(after, newTitle), strip(before, "Original title"); got != want {
		t.Errorf("--title rewrote more than the heading line.\nbefore: %q\n after: %q", want, got)
	}
	if got, want := countBodyLines(after), countBodyLines(before); got != want {
		t.Errorf("--title changed the body line count: %d → %d", want, got)
	}
}

// TestUpdate_BodyChangeReport pins the size delta the CLI prints on the success
// line. Incident 1 took a body from 227 to 113 lines and reported only
// "Updated"; the loss was found seven minutes later by a re-read and a grep.
func TestUpdate_BodyChangeReport(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// A five-line stored body: "", "# Sized", "", "one", "two".
	id, _ := seedItem(t, root, "Sized", "\n# Sized\n\none\ntwo\n")

	shrunk := "\n# Sized\n"
	_, change, err := UpdateWithBodyChange(root, id, UpdateField{Body: &shrunk})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !change.Changed {
		t.Fatal("Changed = false for a body that shrank")
	}
	if change.Mode != "replace" {
		t.Errorf("Mode = %q, want %q", change.Mode, "replace")
	}
	if change.LinesBefore <= change.LinesAfter {
		t.Errorf("expected a shrink, got %d → %d lines", change.LinesBefore, change.LinesAfter)
	}
	if change.HashBefore == change.HashAfter {
		t.Error("hashes must differ when the body changed")
	}

	// A no-op body write reports no change, so the CLI stays quiet rather than
	// crying wolf on every --tags edit.
	same := bodyOf(t, root, id)
	_, change, err = UpdateWithBodyChange(root, id, UpdateField{Body: &same})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if change.Changed {
		t.Errorf("Changed = true for an identical body (%d → %d lines)", change.LinesBefore, change.LinesAfter)
	}
}

// TestBodyHash_MatchesStoredBody pins the contract callers depend on: the hash
// mg reports for an item equals BodyHash of the body mg hands back. If these
// ever diverged, --if-unchanged would refuse every write from a caller who did
// exactly what the docs say.
func TestBodyHash_MatchesStoredBody(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	id, hash := seedItem(t, root, "Round trip", "\n# Round trip\n\nbody with `metachars` and $VARS.\n")

	if got := BodyHash(bodyOf(t, root, id)); got != hash {
		t.Errorf("hash of the stored body changed across a read: %s != %s", got, hash)
	}
	// A no-op update must not move the hash either — otherwise a caller who
	// re-read after any unrelated edit would be holding a hash that never
	// matches.
	assignee := "someone"
	if _, err := Update(root, id, UpdateField{Assignee: &assignee}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := BodyHash(bodyOf(t, root, id)); got != hash {
		t.Errorf("a frontmatter-only edit moved the body hash: %s != %s", got, hash)
	}
}
